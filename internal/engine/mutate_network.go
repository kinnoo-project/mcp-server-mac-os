// mutate_network.go implements the `network` domain's single mutating
// capability: flush_dns_cache. Every other network operation is a pure read and
// lives in builtins_network.go; this one changes system state (it clears a
// cache), so it belongs on the mutation seam alongside the other mutators.
//
// # Why this is auto-commit rather than gated
//
// Flushing the resolver cache is benign and self-healing: the cache simply
// repopulates on demand as names are looked up again, so there is nothing to
// regret and nothing to restore. That puts it in the same low-stakes lane as
// display_sleep — safe to run immediately (registry-enforced none/low risk) with
// no execute-token round trip. It is classified irreversible because a flushed
// cache cannot be "un-flushed" (the prior entries are simply gone), so the plan
// carries a nil Inverse and the auto-commit path appends its own "This cannot be
// undone." note.
//
// # Scope limit
//
// `dscacheutil -flushcache` clears the directory-services cache but does NOT
// restart mDNSResponder; a full DNS reset (`killall -HUP mDNSResponder`) needs
// administrator rights and is deliberately out of scope, so the preview says so
// rather than implying a more complete flush than actually happens.
package engine

import (
	"context"

	"mcp-server-mac-os/internal/registry"
)

// stageFlushDNSCache stages (for immediate auto-commit) a flush of the
// directory-services DNS cache. The invocation is a fixed constant with no
// operands, so no model input reaches argv and there is no injection surface to
// guard. There is no inverse — a cache is transient state with nothing to
// restore — so the plan's Inverse is nil.
func stageFlushDNSCache(_ context.Context, _ registry.Capability, _ map[string]any) (*StagedPlan, error) {
	return &StagedPlan{
		// State only the effect and the scope caveat here; the auto-commit path
		// appends "This cannot be undone." for a nil-inverse op, so repeating it
		// would duplicate the message.
		Preview: "Flush the directory-services DNS cache so the next name lookups resolve fresh. This is benign — the cache repopulates on demand. Note: it clears dscacheutil's cache only; a full mDNSResponder reset needs administrator rights and is out of scope.",
		Forward: Command{Binary: "dscacheutil", Args: []string{"-flushcache"}},
		Inverse: nil, // a cache is transient: once flushed there is nothing to put back
	}, nil
}
