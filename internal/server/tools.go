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

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"mcp-server-mac-os/internal/engine"
	"mcp-server-mac-os/internal/registry"
)

// Server holds the dependencies shared by every tool handler. It is constructed
// once at boot and is safe for concurrent use (its collaborators are stateless).
type Server struct {
	reg *registry.Registry
	eng *engine.Engine
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
	return &Server{reg: reg, eng: eng}, nil
}

// Register exposes one domain tool per capability category. The surface grows
// only when a new *category* is introduced — adding an operation to an existing
// category is a manifest edit that simply lengthens that domain tool's embedded
// menu, leaving the number of tools unchanged.
func (s *Server) Register(srv *mcp.Server) {
	for _, category := range s.Domains() {
		caps := s.reg.List(category)
		mcp.AddTool(srv, &mcp.Tool{
			Name:        category,
			Description: domainToolDescription(category, caps),
		}, s.domainHandler(category))
	}
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

// runDomainOperation resolves and runs one operation within a domain.
//
// The contract is enforced in order: an operation must be named, must exist
// within THIS domain (a capability from another category is rejected, not
// silently run), and — in this read-only phase — must be read-only. A mutating
// operation is recognized but refused with a clear message until the staged
// execute/undo path lands, so the model is told why rather than getting a vague
// failure.
func (s *Server) runDomainOperation(ctx context.Context, category string, in DomainArgs) (*mcp.CallToolResult, any, error) {
	if in.Operation == "" {
		return errorResult("%s: 'operation' is required; choose one of: %v", category, s.operationNames(category))
	}
	c, ok := s.reg.Lookup(in.Operation)
	if !ok || c.Category != category {
		return errorResult("%s: unknown operation %q in this domain. Available operations: %v", category, in.Operation, s.operationNames(category))
	}
	if c.Reversibility != registry.ReadOnly {
		return errorResult("%s: operation %q changes system state; the staged execute/undo path for mutations is not available yet", category, in.Operation)
	}
	out, err := s.eng.Run(ctx, c, in.Params)
	if err != nil {
		return errorResult("%s.%s: %v", category, in.Operation, err)
	}
	return textResult(out)
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
