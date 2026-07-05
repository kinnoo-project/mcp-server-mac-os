// mutate_shortcuts.go implements the one mutating operation of the `shortcuts`
// domain (V8): running a user-authored Shortcut.
//
//   - run_shortcut — trigger a shortcut by name (shortcuts run -- <name>)
//
// # Why this is the project's highest-risk capability
//
// A shortcut is arbitrary automation authored OUTSIDE this server: it can send
// messages, toggle Focus/HomeKit, delete files, hit the network — anything the
// Shortcuts app can do. Its blast radius is therefore unbounded and there is no
// meaningful inverse (you cannot "un-run" a shortcut's side effects). So the
// capability is classified HIGH risk / irreversible and is ALWAYS staged behind
// the human-approval gate — never auto-committed — and pinned as such in
// registry.TestSecurity_DangerousOpsAreGated so that classification can never be
// softened.
//
// # Why the inputs are safe
//
// The only model-controlled values are the shortcut `name` and an optional
// `input` file path. `shortcuts` is a Swift ArgumentParser tool, so it honours a
// "--" end-of-options terminator; the forward argv places the name AFTER "--" so
// even a dash-leading name lands as the positional shortcut name, never as a flag
// (TestShortcuts_RunArgvTerminatesName pins this). Belt-and-suspenders, the name
// is also rejected up front if it is empty or dash-leading, and the optional input
// path goes through validateExistingOperand (dash-rejected, resolved absolute,
// must exist). Staging additionally verifies the name matches an existing shortcut
// (listShortcutNames) so a run can only ever be staged against a real target.
package engine

import (
	"context"
	"fmt"
	"strings"

	"mcp-server-mac-os/internal/registry"
)

// shortcutsRunArgs is the pinned forward argv for run_shortcut. The name is placed
// after a "--" terminator so it is always parsed as the positional shortcut name,
// never as an option; an optional validated input file rides as `-i <path>` BEFORE
// the terminator (a flag must precede "--" to be recognised). Kept as a pure
// function so the terminator-placement regression can assert the exact shape.
func shortcutsRunArgs(name, inputPath string) []string {
	args := []string{"run"}
	if inputPath != "" {
		args = append(args, "-i", inputPath)
	}
	// "--" ends option parsing; everything after is the positional name.
	return append(args, "--", name)
}

// validateShortcutName guards the model-controlled shortcut name. The forward
// argv already terminates options with "--", so the dash rejection is a
// conservative second layer (consistent with the other mutators) that also turns a
// confusing "-foo" into a clear error. It TRIMS surrounding whitespace and returns
// the trimmed value — this matters because the stage-time existence check compares
// against listShortcutNames, which also trims each listed name; without trimming
// here, a name with stray leading/trailing whitespace would clear validation but
// then fail the existence check with a confusing "no such shortcut". It rejects
// empty and any ASCII control character (r < 0x20 or DEL), matching
// validateKeychainQuery / rejectControlChars — so tab/ESC and the like can never
// reach the argv, the existence probe, or the human-facing preview. A shortcut
// name is otherwise free-form (users name shortcuts anything); the "--" terminator
// plus the existence check are what make it safe.
func validateShortcutName(op, raw string) (string, error) {
	name := strings.TrimSpace(raw)
	if name == "" {
		return "", fmt.Errorf("%s: 'name' is required (the exact shortcut name from list_shortcuts)", op)
	}
	if strings.HasPrefix(name, "-") {
		return "", fmt.Errorf("%s: name %q begins with '-' and is not allowed (it would look like a command-line option)", op, name)
	}
	for _, r := range name {
		if r < 0x20 || r == 0x7f {
			return "", fmt.Errorf("%s: name %q contains a control character and is not allowed", op, name)
		}
	}
	return name, nil
}

// stageRunShortcut stages running a shortcut by name. It validates the name,
// resolves an optional input file, and — crucially — confirms the shortcut
// actually exists before staging, so the human-facing preview can only ever name a
// real shortcut (staging a run of a typo'd name is refused with the "did you mean"
// affordance of pointing at list_shortcuts). There is NO inverse: a shortcut's
// side effects cannot be reversed, so the plan is irreversible and the preview
// states plainly that this cannot be undone.
func stageRunShortcut(ctx context.Context, _ registry.Capability, in map[string]any) (*StagedPlan, error) {
	rawName, _ := getString(in, "name")
	name, err := validateShortcutName("run_shortcut", rawName)
	if err != nil {
		return nil, err
	}

	// Optional input file. Absent is the common case; when present it must be an
	// existing file (validateExistingOperand rejects dash-leading and resolves it
	// absolute so it can never be read as a flag).
	var inputPath string
	if raw, ok := getString(in, "input"); ok && strings.TrimSpace(raw) != "" {
		inputPath, err = validateExistingOperand("run_shortcut", "input", raw)
		if err != nil {
			return nil, err
		}
	}

	// Stage-time existence check: only stage a run against a shortcut that is
	// really present, so the preview names a genuine target rather than inviting
	// confirmation of a no-op (or a near-miss name).
	names, err := listShortcutNames(ctx)
	if err != nil {
		return nil, fmt.Errorf("run_shortcut: could not list existing shortcuts to verify %q: %w", name, err)
	}
	if !names[name] {
		return nil, fmt.Errorf(
			"run_shortcut: no shortcut named %q exists on this Mac; run list_shortcuts to see the exact names",
			name)
	}

	preview := fmt.Sprintf(
		"Run the shortcut %q. A shortcut is automation you authored, so its effect is whatever that "+
			"shortcut does — this can change files, send messages, toggle Focus/HomeKit, and MORE, and it "+
			"CANNOT be undone.",
		name)
	if inputPath != "" {
		preview += fmt.Sprintf(" It will be given the file %s as input.", inputPath)
	}

	return &StagedPlan{
		Preview: preview,
		Forward: Command{Binary: "shortcuts", Args: shortcutsRunArgs(name, inputPath)},
		Inverse: nil, // a shortcut's side effects are irreversible; no undo
	}, nil
}
