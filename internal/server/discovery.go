// discovery.go implements the two discovery tools that let the model learn what
// capabilities exist and how to call them — the runtime introspection that makes
// Pattern A work.
//
//   - list_capabilities is the catalog: "what can this server do?" It returns the
//     capabilities (optionally filtered by category) as JSON data.
//   - describe_capability is the detail view: "exactly how do I call X?" It
//     returns one capability's metadata plus a JSON Schema for its parameters,
//     derived from the same ParamSpec the engine validates against.
//
// Output is JSON text so the model can parse it mechanically; JSON is also the
// MCP wire format, so this keeps the data model consistent end to end.
package server

import (
	"context"
	"encoding/json"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// ListCapabilitiesArgs is the input schema for list_capabilities.
type ListCapabilitiesArgs struct {
	Category string `json:"category,omitempty" jsonschema:"Optional category filter such as filesystem; omit to list every capability."`
}

// capabilitySummary is the per-capability record returned by list_capabilities:
// enough for the model to choose, without the full parameter schema.
type capabilitySummary struct {
	Name          string `json:"name"`
	Summary       string `json:"summary"`
	Category      string `json:"category"`
	Risk          string `json:"risk"`
	Reversibility string `json:"reversibility"`
}

// ListCapabilities returns the registered capabilities, optionally filtered by
// category, as a JSON array.
func (s *Server) ListCapabilities(_ context.Context, _ *mcp.CallToolRequest, in ListCapabilitiesArgs) (*mcp.CallToolResult, any, error) {
	caps := s.reg.List(in.Category)
	out := make([]capabilitySummary, 0, len(caps))
	for _, c := range caps {
		out = append(out, capabilitySummary{
			Name:          c.Name,
			Summary:       c.Summary,
			Category:      c.Category,
			Risk:          string(c.Risk),
			Reversibility: string(c.Reversibility),
		})
	}
	return jsonResult(out)
}

// DescribeCapabilityArgs is the input schema for describe_capability.
type DescribeCapabilityArgs struct {
	Capability string `json:"capability" jsonschema:"Name of the capability to describe; must be one returned by list_capabilities."`
}

// DescribeCapability returns one capability's metadata and a JSON Schema for its
// parameters. An unknown name yields a structured error naming the alternatives,
// consistent with the query path.
func (s *Server) DescribeCapability(_ context.Context, _ *mcp.CallToolRequest, in DescribeCapabilityArgs) (*mcp.CallToolResult, any, error) {
	if in.Capability == "" {
		return errorResult("describe_capability: 'capability' is required")
	}
	c, ok := s.reg.Lookup(in.Capability)
	if !ok {
		return errorResult("describe_capability: unknown capability %q. Available: %v.", in.Capability, s.capabilityNames())
	}
	desc := map[string]any{
		"name":          c.Name,
		"summary":       c.Summary,
		"category":      c.Category,
		"risk":          string(c.Risk),
		"reversibility": string(c.Reversibility),
		"parameters":    paramsSchema(c),
	}
	return jsonResult(desc)
}

// capabilityNames returns every registered capability name, for use in
// not-found diagnostics.
func (s *Server) capabilityNames() []string {
	caps := s.reg.All()
	names := make([]string, 0, len(caps))
	for _, c := range caps {
		names = append(names, c.Name)
	}
	return names
}

// jsonResult marshals v to indented JSON and wraps it as a text result. A
// marshal failure is itself returned as a tool error rather than panicking.
func jsonResult(v any) (*mcp.CallToolResult, any, error) {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return errorResult("internal error: could not encode result: %v", err)
	}
	return textResult(string(b))
}
