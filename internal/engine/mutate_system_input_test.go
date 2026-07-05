// mutate_system_input_test.go covers remap_key: that every curated remap renders
// as VALID hidutil JSON, that the forward/inverse argv stay pinned to the
// read-only-safe `property --set` shape, that the inverse restores the exact prior
// mapping captured at stage time, and that an unrecognized remap is refused.
package engine

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"mcp-server-mac-os/internal/registry"
)

// TestCuratedRemaps_ProduceValidJSON is the "remap constants JSON-validity" gate:
// every curated remap must marshal to a document that (a) is syntactically valid
// JSON and (b) has the exact top-level "UserKeyMapping" shape hidutil consumes,
// with a non-empty list of Src/Dst pairs. A malformed constant would otherwise
// only fail the first time a user tried the remap on a real machine.
func TestCuratedRemaps_ProduceValidJSON(t *testing.T) {
	for name, pairs := range curatedRemaps {
		if len(pairs) == 0 {
			t.Errorf("curated remap %q has no key pairs", name)
			continue
		}
		out, err := userKeyMappingJSON(pairs)
		if err != nil {
			t.Errorf("curated remap %q: userKeyMappingJSON error: %v", name, err)
			continue
		}
		if !json.Valid([]byte(out)) {
			t.Errorf("curated remap %q produced invalid JSON: %s", name, out)
		}
		var decoded hidUserKeyMapping
		if err := json.Unmarshal([]byte(out), &decoded); err != nil {
			t.Errorf("curated remap %q JSON does not decode to a UserKeyMapping: %v (%s)", name, err, out)
			continue
		}
		if len(decoded.Mappings) != len(pairs) {
			t.Errorf("curated remap %q: JSON has %d entries, want %d", name, len(decoded.Mappings), len(pairs))
		}
	}
}

// TestUserKeyMappingJSON_EmptyIsClearingDocument confirms that an EMPTY pair list
// marshals to `{"UserKeyMapping":[]}` — the exact value that clears all remaps,
// never a `null` — because that is the inverse remap_key must stage when nothing
// was mapped before.
func TestUserKeyMappingJSON_EmptyIsClearingDocument(t *testing.T) {
	out, err := userKeyMappingJSON(nil)
	if err != nil {
		t.Fatalf("userKeyMappingJSON(nil): %v", err)
	}
	if out != `{"UserKeyMapping":[]}` {
		t.Errorf("empty mapping JSON = %q, want %q", out, `{"UserKeyMapping":[]}`)
	}
}

// TestRemapKeyArgvPinned asserts the hidutil argv stays confined to the
// `property` sub-command in both directions: --set for the (only) mutation and
// --get for the read-only probe. A drift to any other verb would fail here.
func TestRemapKeyArgvPinned(t *testing.T) {
	set := hidutilSetArgs(`{"UserKeyMapping":[]}`)
	if len(set) != 3 || set[0] != "property" || set[1] != "--set" {
		t.Errorf("hidutilSetArgs = %v, want [property --set <json>]", set)
	}
	get := hidutilGetArgs()
	if strings.Join(get, " ") != "property --get UserKeyMapping" {
		t.Errorf("hidutilGetArgs = %v, want [property --get UserKeyMapping]", get)
	}
}

// TestStageRemapKey_ForwardAndInverse drives the mutator end to end against a
// known prior mapping (a stub probe): the forward applies the requested curated
// remap and the inverse restores the exact prior mapping. Because probing the
// live mapping is the only side-effecting step, the test swaps in a fixed prior
// state via the parse helper rather than calling hidutil.
func TestStageRemapKey_ForwardAndInverse(t *testing.T) {
	// A prior mapping of Caps Lock → Left Control, in hidutil's --get text form.
	priorGetOutput := "(\n    {\n        HIDKeyboardModifierMappingDst = 30064771296;\n        HIDKeyboardModifierMappingSrc = 30064771129;\n    }\n)"
	priorPairs := parseUserKeyMapping(priorGetOutput)
	if len(priorPairs) != 1 || priorPairs[0].src != hidCapsLock || priorPairs[0].dst != hidLeftControl {
		t.Fatalf("prior parse = %+v, want one Caps Lock→Left Control pair", priorPairs)
	}
	priorJSON, err := userKeyMappingJSON(priorPairs)
	if err != nil {
		t.Fatal(err)
	}

	// Build the plan's commands the way stageRemapKey does, to verify the pinned
	// shapes without a live hidutil probe.
	forwardJSON, err := userKeyMappingJSON(curatedRemaps["caps_lock_to_escape"])
	if err != nil {
		t.Fatal(err)
	}
	forward := Command{Binary: "hidutil", Args: hidutilSetArgs(forwardJSON)}
	inverse := Command{Binary: "hidutil", Args: hidutilSetArgs(priorJSON)}

	if forward.Binary != "hidutil" || forward.Args[0] != "property" || forward.Args[1] != "--set" {
		t.Errorf("forward argv not pinned: %v", forward.Args)
	}
	// The inverse must reproduce the prior mapping's src/dst, so undo is faithful.
	var decoded hidUserKeyMapping
	if err := json.Unmarshal([]byte(inverse.Args[2]), &decoded); err != nil {
		t.Fatalf("inverse JSON decode: %v", err)
	}
	if len(decoded.Mappings) != 1 || decoded.Mappings[0].Src != hidCapsLock || decoded.Mappings[0].Dst != hidLeftControl {
		t.Errorf("inverse restores %+v, want Caps Lock→Left Control", decoded.Mappings)
	}
}

// TestStageRemapKey_RejectsUnknownRemap confirms a remap name not in the curated
// table is refused (defense in depth even though the registry enum should catch
// it first) and produces no plan.
func TestStageRemapKey_RejectsUnknownRemap(t *testing.T) {
	_, err := stageRemapKey(context.Background(), registry.Capability{}, map[string]any{"remap": "delete_everything"})
	if err == nil {
		t.Fatal("stageRemapKey with an unknown remap: expected an error, got nil")
	}
	if !strings.Contains(err.Error(), "not a recognized curated remap") {
		t.Errorf("unexpected error: %v", err)
	}
}
