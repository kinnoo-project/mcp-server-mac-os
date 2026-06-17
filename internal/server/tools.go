// Package server is the MCP adapter layer: it exposes the engine's capabilities
// through the small, fixed set of engine tools the LLM actually sees (Pattern A).
//
// # Architecture role
//
// The model never sees one tool per capability. Instead it sees a handful of
// generic tools — this phase ships `query` for read-only capabilities — and
// names the capability it wants as a parameter. Capabilities themselves are
// discovered as DATA (via list_capabilities/describe_capability, added in a
// later slice). This keeps the tool surface fixed and stable no matter how many
// capabilities the registry grows to hold.
//
// The server wires together the two layers it depends on — the registry (what
// can be done) and the engine (how it is done) — and owns request-shaped
// concerns: looking a capability up, enforcing the read-only contract of the
// query path, and translating engine output/errors into MCP results.
package server

import (
	"context"
	"fmt"

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

// Register wires this server's tools onto an MCP server. The read-only phase
// exposes a fixed surface of three tools — two for discovery and one to execute
// — regardless of how many capabilities the registry holds. Mutation tools
// (plan/commit/undo) are added in a later phase.
func (s *Server) Register(srv *mcp.Server) {
	menu := capabilityMenu(s.reg)

	mcp.AddTool(srv, &mcp.Tool{
		Name: "list_capabilities",
		Description: "List the macOS operations this server can perform, optionally filtered " +
			"by category, as JSON. Available capabilities:\n" + menu,
	}, s.ListCapabilities)

	mcp.AddTool(srv, &mcp.Tool{
		Name: "describe_capability",
		Description: "Return one capability's metadata and a JSON Schema for its parameters. " +
			"Call this when you need the exact parameters a capability accepts.",
	}, s.DescribeCapability)

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "query",
		Description: s.queryDescription(menu),
	}, s.Query)
}

// QueryArgs is the input schema for the query tool. Per Pattern A the schema is
// intentionally generic: Capability names the operation and Params is an open
// object validated at call time against that capability's ParamSpec, rather than
// enumerating every capability's parameters here.
type QueryArgs struct {
	Capability string         `json:"capability" jsonschema:"Name of the read-only capability to run; must be one returned by list_capabilities."`
	Params     map[string]any `json:"params,omitempty" jsonschema:"Capability-specific parameters; validated against the capability schema at call time."`
}

// Query executes a single read-only capability and returns its output.
//
// It enforces the query path's contract in order: the capability must exist
// (otherwise a structured not_found is returned) and must be read-only (mutating
// capabilities will route through the future plan/commit path, never query).
func (s *Server) Query(ctx context.Context, _ *mcp.CallToolRequest, in QueryArgs) (*mcp.CallToolResult, any, error) {
	if in.Capability == "" {
		return errorResult("query: 'capability' is required")
	}
	cap, ok := s.reg.Lookup(in.Capability)
	if !ok {
		return s.notFound(in.Capability)
	}
	if cap.Reversibility != registry.ReadOnly {
		return errorResult("query: capability %q is not read-only; mutating capabilities use the plan/commit path, not query", in.Capability)
	}
	out, err := s.eng.Run(ctx, cap, in.Params)
	if err != nil {
		return errorResult("query %q: %v", in.Capability, err)
	}
	return textResult(out)
}

// notFound returns a structured "unsupported capability" result that names the
// available read-only capabilities, steering the model toward a valid choice (or
// toward telling the user it is unsupported) rather than guessing or silently
// falling back to a raw shell command.
func (s *Server) notFound(name string) (*mcp.CallToolResult, any, error) {
	available := make([]string, 0, s.reg.Len())
	for _, c := range s.reg.List("") {
		if c.Reversibility == registry.ReadOnly {
			available = append(available, c.Name)
		}
	}
	return errorResult("query: unknown capability %q. Available read-only capabilities: %v. Call list_capabilities to discover them.", name, available)
}

// queryDescription is the tool description shown to the model. It embeds the
// capability menu so the model can pick a capability without first calling
// list_capabilities (the common case costs zero discovery round-trips); full
// parameter schemas still come from describe_capability on demand.
func (s *Server) queryDescription(menu string) string {
	return "Run a read-only macOS inspection capability by name. Supply 'capability' and " +
		"'params' matching that capability's schema (see describe_capability for details). " +
		"Available capabilities:\n" + menu
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
