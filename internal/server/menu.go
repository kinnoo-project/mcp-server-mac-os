// menu.go builds the compact "capability menu" that is embedded directly into
// the discovery/query tool descriptions.
//
// Why embed a menu at all: under Pattern A the model sees only the fixed engine
// tools, not one tool per capability. Listing the capability names and one-line
// summaries inside the tool description lets the model choose a capability in
// the common case WITHOUT a separate list_capabilities round-trip — collapsing
// what would otherwise be an extra request into zero. Full parameter detail
// still comes on demand from describe_capability.
package server

import (
	"sort"
	"strings"

	"mcp-server-mac-os/internal/registry"
)

// capabilityMenu renders the registry's capabilities as a stable, newline-
// separated list of "name — summary (risk: X)" lines, grouped by category. The
// output is deterministic (categories and names sorted) so the tool description
// does not churn between boots.
func capabilityMenu(reg *registry.Registry) string {
	caps := reg.All()

	// Group capability lines by category.
	byCategory := make(map[string][]string)
	for _, c := range caps {
		line := "  - " + c.Name + " — " + c.Summary + " (risk: " + string(c.Risk) + ")"
		byCategory[c.Category] = append(byCategory[c.Category], line)
	}

	categories := make([]string, 0, len(byCategory))
	for cat := range byCategory {
		categories = append(categories, cat)
	}
	sort.Strings(categories)

	var b strings.Builder
	for i, cat := range categories {
		if i > 0 {
			b.WriteString("\n")
		}
		b.WriteString(cat)
		b.WriteString(":\n")
		lines := byCategory[cat]
		sort.Strings(lines)
		b.WriteString(strings.Join(lines, "\n"))
	}
	return b.String()
}
