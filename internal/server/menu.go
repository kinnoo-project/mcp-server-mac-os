// menu.go renders a domain tool's description: the model-readable menu of the
// operations in that domain and the parameters each one accepts.
//
// Why embed the full menu in the tool description: the model sees only the domain
// tools, not one tool per operation. Spelling out every operation and its
// parameters inside the description lets the model form a correct call in one
// shot, with no separate discovery round-trip. The capability ParamSpec list
// remains the single source of truth; this rendering is derived from it and so
// can never drift from what the engine actually validates.
package server

import (
	"sort"
	"strings"

	"mcp-server-mac-os/internal/registry"
)

// domainToolDescription builds the description for the domain tool that exposes
// the given category. Operations are listed in sorted order (deterministic across
// boots) with their summary, risk, and parameter details, so the description is
// stable and does not churn between restarts.
func domainToolDescription(category string, caps []registry.Capability) string {
	sorted := append([]registry.Capability(nil), caps...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Name < sorted[j].Name })

	var b strings.Builder
	b.WriteString("Perform a macOS ")
	b.WriteString(category)
	b.WriteString(" operation. Set 'operation' to one of the operations below and ")
	b.WriteString("'params' to that operation's parameters.\n\nOperations:")

	for _, c := range sorted {
		b.WriteString("\n  - ")
		b.WriteString(c.Name)
		b.WriteString(" — ")
		b.WriteString(c.Summary)
		b.WriteString(" (risk: ")
		b.WriteString(string(c.Risk))
		b.WriteString("; ")
		b.WriteString(executionLane(c))
		b.WriteString(")")
		b.WriteString(renderParams(c.Params))
	}
	return b.String()
}

// executionLane describes, in one phrase, how calling an operation behaves, so
// the model knows up front whether a call runs immediately or returns a token to
// confirm. The three lanes mirror server.runDomainOperation's dispatch:
//   - read-only operations run immediately and return their output;
//   - auto-commit mutations also run immediately, and MAY return an undo token
//     when the change turns out to be reversible;
//   - every other mutation is STAGED behind the `execute` confirmation step.
//
// The auto-commit lane deliberately hedges ("may return an undo token"): some
// reversible-category operations still omit an inverse at stage time — e.g.
// open_application offers no undo when it finds the app was already running — so
// promising an undo unconditionally would overstate the guarantee to the model.
func executionLane(c registry.Capability) string {
	switch {
	case c.Reversibility == registry.ReadOnly:
		return "runs immediately"
	case c.AutoCommit && c.Reversibility == registry.Irreversible:
		return "runs immediately; cannot be undone"
	case c.AutoCommit:
		return "runs immediately; may return an undo token"
	default:
		return "STAGED — confirm with the user, then execute"
	}
}

// renderParams renders a capability's parameters as indented lines beneath its
// operation, or notes that it takes none. Each line states the parameter name,
// its type, whether it is required, and its description — everything the model
// needs to populate 'params' correctly without a separate schema lookup.
func renderParams(params []registry.ParamSpec) string {
	if len(params) == 0 {
		return "\n      (no parameters)"
	}
	var b strings.Builder
	for _, p := range params {
		b.WriteString("\n      ")
		b.WriteString(p.Name)
		b.WriteString(" (")
		b.WriteString(string(p.Type))
		if p.Required {
			b.WriteString(", required")
		}
		b.WriteString(")")
		if p.Description != "" {
			b.WriteString(": ")
			b.WriteString(p.Description)
		}
	}
	return b.String()
}
