// mutate_system_input.go implements remap_key: the `system` domain's keyboard
// modifier-remapping capability, built on macOS's hidutil.
//
// # Why this is a curated enum, not a free-form mapping
//
// `hidutil property --set '{"UserKeyMapping":[...]}'` can remap ANY key to any
// other, which is powerful and easy to misuse — a bad mapping can make the
// keyboard unusable, and neither the model nor a human reviewing a confirmation
// prompt can safely assess an arbitrary raw HID-usage payload. So remap_key does
// NOT accept a mapping: it takes a closed `remap` enum (validated by the registry
// against the manifest, and again here against curatedRemaps as defense in depth)
// naming one of a handful of common, vetted remaps. The actual HID usage codes
// behind each remap live in this Go table, never in model input — the same
// data-not-code posture write_setting takes for `defaults` domains/keys.
//
// # Why undo needs a probe (like write_setting, unlike mkdir)
//
// A remap's inverse must restore whatever mapping was in effect BEFORE — which
// might be empty, one of our curated remaps, or a custom mapping the user set up
// themselves. So staging first reads the current UserKeyMapping (a harmless
// `hidutil property --get`), converts it back into the JSON `--set` expects, and
// bakes that exact prior mapping into the inverse. That is the "resolve
// everything at stage time" discipline: what undo restores is captured before the
// forward ever runs.
//
// # Why the JSON is built, never interpolated
//
// Both the forward mapping (from the curated table) and the inverse mapping (from
// the parsed prior state) are assembled with encoding/json from typed integer
// usage codes, so the payload is always valid JSON and can never carry an
// injected fragment. The only model input is the closed enum that selects a row.
package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"

	"mcp-server-mac-os/internal/registry"
)

// HID keyboard usage codes, in hidutil's (usagePage<<32 | usage) form on the
// keyboard/keypad page (0x07). These name the physical keys the curated remaps
// move around; keeping them as named constants makes the remap table below
// self-documenting instead of a wall of magic numbers.
const (
	hidCapsLock     uint64 = 0x700000039 // Caps Lock
	hidEscape       uint64 = 0x700000029 // Escape
	hidLeftControl  uint64 = 0x7000000E0 // Left Control
	hidLeftAlt      uint64 = 0x7000000E2 // Left Option/Alt
	hidLeftGUI      uint64 = 0x7000000E3 // Left Command (GUI)
	hidRightControl uint64 = 0x7000000E4 // Right Control
	hidRightAlt     uint64 = 0x7000000E6 // Right Option/Alt
	hidRightGUI     uint64 = 0x7000000E7 // Right Command (GUI)
	// hidNoEvent is HID keyboard usage 0x00 ("no event indicated"): remapping a
	// key here is macOS's standard way to DISABLE it — the key still exists but
	// produces nothing. Used by the disable_caps_lock remap.
	hidNoEvent uint64 = 0x700000000
)

// keyPair is one source→destination remap: pressing the physical `src` key makes
// the system behave as though `dst` were pressed.
type keyPair struct {
	src uint64
	dst uint64
}

// curatedRemaps is the closed set of remaps remap_key may apply. Each value is
// the list of source→destination pairs that make up that remap (a swap needs two
// pairs per side so BOTH keys change roles). This table — and ONLY this table —
// is the source of forward mappings; a hostile or nonsensical mapping can never
// be requested because the model only ever selects one of these keys.
//
// It must stay in sync with the "remap" enum in
// internal/registry/manifests/system.json; TestRemapEnumMatchesCuratedTable
// guards against the two drifting apart.
var curatedRemaps = map[string][]keyPair{
	"caps_lock_to_escape":  {{hidCapsLock, hidEscape}},
	"caps_lock_to_control": {{hidCapsLock, hidLeftControl}},
	"disable_caps_lock":    {{hidCapsLock, hidNoEvent}},
	"swap_command_option": {
		{hidLeftGUI, hidLeftAlt}, {hidLeftAlt, hidLeftGUI},
		{hidRightGUI, hidRightAlt}, {hidRightAlt, hidRightGUI},
	},
	"swap_control_command": {
		{hidLeftControl, hidLeftGUI}, {hidLeftGUI, hidLeftControl},
		{hidRightControl, hidRightGUI}, {hidRightGUI, hidRightControl},
	},
}

// CuratedRemapKeys returns the sorted remap names the curated table recognizes.
// It exists so a cross-package test (internal/server) can assert this table
// exactly matches the manifest's "remap" enum, catching drift between the two
// declarations of the same closed set.
func CuratedRemapKeys() []string {
	keys := make([]string, 0, len(curatedRemaps))
	for k := range curatedRemaps {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// hidMappingEntry is the JSON shape hidutil's UserKeyMapping array holds, one per
// remapped key. The json tags reproduce hidutil's exact key names.
type hidMappingEntry struct {
	Src uint64 `json:"HIDKeyboardModifierMappingSrc"`
	Dst uint64 `json:"HIDKeyboardModifierMappingDst"`
}

// hidUserKeyMapping is the top-level object `hidutil property --set` consumes.
type hidUserKeyMapping struct {
	Mappings []hidMappingEntry `json:"UserKeyMapping"`
}

// userKeyMappingJSON renders a set of remap pairs as the exact JSON document
// hidutil's `property --set` expects. It marshals via encoding/json from typed
// integers, so the result is always valid JSON with no room for injection, and an
// EMPTY pair list yields `{"UserKeyMapping":[]}` (the value that clears all
// remaps) rather than a null — which is what the inverse of a "there was nothing
// before" state must be.
func userKeyMappingJSON(pairs []keyPair) (string, error) {
	entries := make([]hidMappingEntry, 0, len(pairs))
	for _, p := range pairs {
		entries = append(entries, hidMappingEntry{Src: p.src, Dst: p.dst})
	}
	b, err := json.Marshal(hidUserKeyMapping{Mappings: entries})
	if err != nil {
		return "", fmt.Errorf("could not build UserKeyMapping JSON: %w", err)
	}
	return string(b), nil
}

// hidutilSetArgs / hidutilGetArgs are the pinned hidutil argument vectors. Both
// stay confined to the `property` sub-command: --set writes UserKeyMapping (the
// only mutation) and --get reads it (read-only). Kept as pure functions so a test
// can assert the argv shape never grows a different, unreviewed hidutil verb.
func hidutilSetArgs(mappingJSON string) []string {
	return []string{"property", "--set", mappingJSON}
}
func hidutilGetArgs() []string {
	return []string{"property", "--get", "UserKeyMapping"}
}

// stageRemapKey stages a curated keyboard remap. It looks up the requested remap
// in curatedRemaps (rejecting anything not on the list, even though the registry
// enum should already have caught it — a mutator must never trust that a future
// manifest edit kept the two in sync), reads the CURRENT mapping so undo can
// restore it, and returns a plan whose forward applies the curated mapping and
// whose inverse restores whatever was in effect at stage time.
func stageRemapKey(ctx context.Context, _ registry.Capability, in map[string]any) (*StagedPlan, error) {
	name, _ := getString(in, "remap")
	pairs, ok := curatedRemaps[name]
	if !ok {
		return nil, fmt.Errorf("remap_key: %q is not a recognized curated remap", name)
	}

	forwardJSON, err := userKeyMappingJSON(pairs)
	if err != nil {
		return nil, fmt.Errorf("remap_key: %w", err)
	}

	// Capture the prior mapping so undo restores exactly what was there — empty,
	// a curated remap, or a user's own custom mapping.
	prior, err := probeUserKeyMapping(ctx)
	if err != nil {
		return nil, fmt.Errorf("remap_key: could not read the current key mapping: %w", err)
	}
	priorPairs := parseUserKeyMapping(prior)
	priorJSON, err := userKeyMappingJSON(priorPairs)
	if err != nil {
		return nil, fmt.Errorf("remap_key: %w", err)
	}

	inverse := Command{Binary: "hidutil", Args: hidutilSetArgs(priorJSON)}
	return &StagedPlan{
		Preview: fmt.Sprintf(
			"Apply the %q keyboard remap (%s). Undo will restore the previous mapping (%s). "+
				"Note: this takes effect immediately but does NOT survive a reboot — macOS clears key "+
				"remappings on restart.",
			name, describeRemapPairs(pairs), describePriorMapping(priorPairs)),
		Forward: Command{Binary: "hidutil", Args: hidutilSetArgs(forwardJSON)},
		Inverse: &inverse,
	}, nil
}
