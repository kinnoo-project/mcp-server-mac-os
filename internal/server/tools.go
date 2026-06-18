// Package server is the MCP adapter layer: it projects the capability registry
// onto the set of tools the model actually sees.
//
// # Architecture role
//
// The model does not see one tool per operation, nor a single generic dispatch
// tool. Instead each capability *category* is exposed as one "domain" tool — for
// example a `filesystem` tool — that accepts the name of an operation within that
// domain plus that operation's parameters. This keeps the tool surface small and
// semantically meaningful (the model picks a domain it understands, then an
// operation within it) while the capabilities themselves remain pure registry
// DATA: adding an operation is a manifest entry, not a new Go tool, and it does
// not grow the tool surface — only a brand-new category does.
//
// Each domain tool's description embeds the full menu of its operations and their
// parameters, so the model can form a correct call in one shot without a separate
// discovery round-trip.
//
// The server wires together the two layers it depends on — the registry (what can
// be done) and the engine (how it is done) — and owns request-shaped concerns:
// resolving the named operation within its domain, enforcing the read-only
// contract of this phase, and translating engine output/errors into MCP results.
// Mutating operations will route through a staged execute/undo path in a later
// phase; for now only read-only operations are reachable.
package server

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"mcp-server-mac-os/internal/engine"
	"mcp-server-mac-os/internal/registry"
	"mcp-server-mac-os/internal/transaction"
)

// stagedTTL bounds how long a staged-but-uncommitted plan remains executable.
// It is generous enough for a human to read the preview and decide, but short
// enough that an abandoned plan cannot be committed much later against a system
// whose state has since drifted from what staging observed.
const stagedTTL = 15 * time.Minute

// undoTTL bounds how long an applied change remains reversible. It is longer
// than stagedTTL because a user may reasonably ask to undo a while after the
// change landed, but it is still finite so the journal cannot grow without
// bound.
const undoTTL = time.Hour

// stagedRecord is what the staged-plan store holds between a mutating domain
// call and its eventual commit: the engine's resolved plan plus the operation
// name, which is carried only so commit messages can name what ran.
type stagedRecord struct {
	operation string
	plan      *engine.StagedPlan
}

// Server holds the dependencies shared by every tool handler. It is constructed
// once at boot and is safe for concurrent use: the registry and engine are
// stateless, and the two transaction stores are internally synchronized.
type Server struct {
	reg *registry.Registry
	eng *engine.Engine
	// staged holds validated, not-yet-committed mutation plans keyed by the
	// opaque token handed back to the model; commit consumes the token here.
	staged *transaction.Store[stagedRecord]
	// undo holds the inverse command of each committed reversible change, keyed
	// by the undo token; an undo call consumes the token here.
	undo *transaction.Store[engine.Command]
}

// New constructs a Server and performs a startup fail-fast check that every
// registered capability names a builder the engine can run. Wiring this check
// here — at the point the registry and engine first meet — means a manifest that
// references a non-existent builder crashes the process at boot, not on a user's
// request.
func New(reg *registry.Registry, eng *engine.Engine) (*Server, error) {
	if err := eng.ValidateBuilders(reg.All()); err != nil {
		return nil, err
	}
	return &Server{
		reg:    reg,
		eng:    eng,
		staged: transaction.NewStore[stagedRecord]("req_", stagedTTL),
		undo:   transaction.NewStore[engine.Command]("undo_", undoTTL),
	}, nil
}

// Register exposes one domain tool per capability category, plus the two shared
// tools that drive the mutation lifecycle (execute, undo). The per-domain
// surface grows only when a new *category* is introduced — adding an operation
// to an existing category is a manifest edit that simply lengthens that domain
// tool's embedded menu, leaving the number of tools unchanged. The execute/undo
// pair is fixed regardless of how many mutating operations exist.
func (s *Server) Register(srv *mcp.Server) {
	for _, category := range s.Domains() {
		caps := s.reg.List(category)
		mcp.AddTool(srv, &mcp.Tool{
			Name:        category,
			Description: domainToolDescription(category, caps),
		}, s.domainHandler(category))
	}

	mcp.AddTool(srv, &mcp.Tool{
		Name: "execute",
		Description: "Commit a change that a domain tool previously STAGED, using the token it returned. " +
			"This is the only step that actually modifies the system, so a human should approve it: " +
			"confirm with the user before calling it. On success it returns the result and, when the change " +
			"is reversible, an undo_token for the `undo` tool. A token works once and expires.",
	}, s.Execute)

	mcp.AddTool(srv, &mcp.Tool{
		Name: "undo",
		Description: "Reverse a reversible change that `execute` previously applied, using the undo_token it " +
			"returned. A given undo_token works once and expires.",
	}, s.Undo)

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "pipeline",
		Description: s.pipelineToolDescription(),
	}, s.Pipeline)
}

// Domains returns the sorted, unique set of capability categories — one MCP
// domain tool is exposed per entry. Sorting keeps the registered tool set
// deterministic across boots.
func (s *Server) Domains() []string {
	seen := make(map[string]bool)
	var cats []string
	for _, c := range s.reg.All() {
		if !seen[c.Category] {
			seen[c.Category] = true
			cats = append(cats, c.Category)
		}
	}
	sort.Strings(cats)
	return cats
}

// DomainArgs is the input shape shared by every domain tool. It is intentionally
// generic: Operation names the capability within the domain and Params is an open
// object validated at call time against that capability's ParamSpec. The valid
// operations and their parameters are spelled out in each domain tool's
// description rather than as a static schema enum, because they differ per domain
// and a single Go struct cannot carry per-tool constraints.
type DomainArgs struct {
	Operation string         `json:"operation" jsonschema:"The operation to run within this domain; must be one of the operations listed in this tool's description."`
	Params    map[string]any `json:"params,omitempty" jsonschema:"Parameters for the chosen operation, matching the parameters listed for that operation in this tool's description."`
}

// domainHandler returns the MCP handler for one domain tool, closing over the
// category it serves so the handler can confirm the requested operation actually
// belongs to this domain (and not, say, smuggle in a capability from another).
func (s *Server) domainHandler(category string) func(context.Context, *mcp.CallToolRequest, DomainArgs) (*mcp.CallToolResult, any, error) {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in DomainArgs) (*mcp.CallToolResult, any, error) {
		return s.runDomainOperation(ctx, category, in)
	}
}

// runDomainOperation resolves one operation within a domain and dispatches it
// down one of two paths depending on whether it changes system state.
//
// The contract is enforced in order: an operation must be named and must exist
// within THIS domain (a capability from another category is rejected, not
// silently run). A read-only operation then runs immediately and returns its
// output. A mutating operation does NOT run here — it is STAGED: the engine
// validates it and computes the forward/inverse commands, the plan is stashed
// server-side under an opaque token, and the model receives the token plus a
// human-readable preview. Nothing changes on the system until that token is
// handed to the `execute` tool, which is the deliberate human-approval gate.
func (s *Server) runDomainOperation(ctx context.Context, category string, in DomainArgs) (*mcp.CallToolResult, any, error) {
	if in.Operation == "" {
		return errorResult("%s: 'operation' is required; choose one of: %v", category, s.operationNames(category))
	}
	c, ok := s.reg.Lookup(in.Operation)
	if !ok || c.Category != category {
		return errorResult("%s: unknown operation %q in this domain. Available operations: %v", category, in.Operation, s.operationNames(category))
	}
	if c.Reversibility == registry.ReadOnly {
		out, err := s.eng.Run(ctx, c, in.Params)
		if err != nil {
			return errorResult("%s.%s: %v", category, in.Operation, err)
		}
		return textResult(out)
	}
	return s.stageMutation(ctx, category, c, in.Params)
}

// stageMutation validates and stages a mutating operation, returning the token +
// preview the caller must show the user before committing. It performs no
// system change itself; that is the `execute` tool's job.
func (s *Server) stageMutation(ctx context.Context, category string, c registry.Capability, params map[string]any) (*mcp.CallToolResult, any, error) {
	plan, err := s.eng.Stage(ctx, c, params)
	if err != nil {
		return errorResult("%s.%s: %v", category, c.Name, err)
	}
	token, err := s.staged.Put(stagedRecord{operation: c.Name, plan: plan})
	if err != nil {
		return errorResult("%s.%s: could not stage operation: %v", category, c.Name, err)
	}
	return textResult(fmt.Sprintf(
		"STAGED — nothing has run yet.\n\n%s\n\nTo apply this change, confirm with the user, then call the `execute` "+
			"tool with token %q. To discard it, do nothing; the staged action expires on its own.",
		plan.Preview, token,
	))
}

// ExecuteArgs is the input to the execute tool: the opaque token returned when a
// mutating operation was staged.
type ExecuteArgs struct {
	Token string `json:"token" jsonschema:"required,The token returned by a domain tool when it staged a mutating operation."`
}

// Execute commits a previously staged plan. It consumes the token (so a plan can
// be committed at most once), runs the staged forward command exactly as it was
// validated and previewed, and — when the change is reversible — stashes its
// inverse under a fresh undo token returned to the caller.
func (s *Server) Execute(ctx context.Context, _ *mcp.CallToolRequest, in ExecuteArgs) (*mcp.CallToolResult, any, error) {
	if in.Token == "" {
		return errorResult("execute: 'token' is required (it is returned when a mutating operation is staged)")
	}
	rec, ok := s.staged.Take(in.Token)
	if !ok {
		return errorResult("execute: unknown or expired token %q; stage the operation again to get a fresh token", in.Token)
	}

	out, err := s.eng.RunCommand(ctx, rec.plan.Forward)
	if err != nil {
		return errorResult("execute %q: %v", rec.operation, err)
	}

	// An irreversible plan carries no inverse, so there is no undo to offer.
	if rec.plan.Inverse == nil {
		return textResult(fmt.Sprintf("%s\n\nApplied %q. This change cannot be undone.", out, rec.operation))
	}

	undoToken, err := s.undo.Put(*rec.plan.Inverse)
	if err != nil {
		// The change already happened; we just can't register an undo for it.
		return textResult(fmt.Sprintf(
			"%s\n\nApplied %q, but could not register an undo (%v); it cannot be reversed automatically.",
			out, rec.operation, err))
	}
	return textResult(fmt.Sprintf(
		"%s\n\nApplied %q. To reverse it, call the `undo` tool with undo_token %q.",
		out, rec.operation, undoToken,
	))
}

// UndoArgs is the input to the undo tool: the token returned by execute for a
// reversible change.
type UndoArgs struct {
	UndoToken string `json:"undo_token" jsonschema:"required,The undo_token returned by the execute tool after applying a reversible change."`
}

// Undo reverses a previously applied reversible change. It consumes the undo
// token (so a reversal happens at most once) and runs the inverse command the
// engine captured at stage time.
func (s *Server) Undo(ctx context.Context, _ *mcp.CallToolRequest, in UndoArgs) (*mcp.CallToolResult, any, error) {
	if in.UndoToken == "" {
		return errorResult("undo: 'undo_token' is required (it is returned by execute after a reversible change)")
	}
	inverse, ok := s.undo.Take(in.UndoToken)
	if !ok {
		return errorResult("undo: unknown or expired undo_token %q; the change may already have been undone or is no longer reversible", in.UndoToken)
	}
	out, err := s.eng.RunCommand(ctx, inverse)
	if err != nil {
		return errorResult("undo: %v", err)
	}
	return textResult(fmt.Sprintf("%s\n\nThe change has been reversed.", out))
}

// operationNames lists the operation names available in a domain, in manifest
// order, for use in usage and not-found diagnostics.
func (s *Server) operationNames(category string) []string {
	caps := s.reg.List(category)
	names := make([]string, 0, len(caps))
	for _, c := range caps {
		names = append(names, c.Name)
	}
	return names
}

// ---------------------------------------------------------------------------
// MCP result helpers
// ---------------------------------------------------------------------------

// textResult wraps a plain string into the dual-return shape the MCP Go SDK tool
// handler contract expects. The structured-output value is nil to avoid emitting
// an empty `{}` payload alongside the text.
func textResult(s string) (*mcp.CallToolResult, any, error) {
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: s}},
	}, nil, nil
}

// errorResult marks a CallToolResult as a tool-level error while still returning
// useful diagnostic text to the model.
func errorResult(format string, args ...any) (*mcp.CallToolResult, any, error) {
	return &mcp.CallToolResult{
		IsError: true,
		Content: []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf(format, args...)}},
	}, nil, nil
}
