// case.go defines the eval case format and loads it from disk.
//
// Cases are JSON, not YAML: the project already has a precedent for this
// (internal/registry/manifests are JSON specifically to avoid a new
// dependency, since the standard library has no YAML support) and the same
// reasoning applies here — a hand-rolled or vendored YAML parser is not worth
// it for what is structurally identical to the manifest format: an array of
// declarative records loaded by encoding/json.
package evals

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// Expectation is the assertion checked against one turn's tool-call activity.
// Every field is optional; an empty Expectation means "no tool call and no
// particular text expected" (rarely useful on its own, but valid).
type Expectation struct {
	// Tool, if set, must be among the tools called during this turn. Empty means
	// "expect no tool call at all this turn" — for refusal-style cases.
	Tool string `json:"tool,omitempty"`
	// Operation, if set, is checked against the "operation" argument of at least
	// one call to Tool this turn (only meaningful when Tool is a domain tool like
	// "filesystem" or "preferences", which take an operation parameter).
	Operation string `json:"operation,omitempty"`
	// ForbidTools lists tool names that must NOT be called during this turn. This
	// is the harness's primary safety assertion: a case stages a mutation and
	// sets ForbidTools: ["execute"] to confirm the model didn't chain straight
	// through to commit without a human turn in between.
	ForbidTools []string `json:"forbid_tools,omitempty"`
	// TextContains, if set, are substrings that must all appear somewhere in the
	// turn's final (non-tool-call) text response. Intentionally loose — exact
	// wording isn't the point, just that a refusal/confirmation actually
	// happened in some form.
	TextContains []string `json:"text_contains,omitempty"`
	// ToolSucceeds, when non-nil and true, asserts that the call to Tool this
	// turn did NOT come back as a tool-level error block. It closes a hole in the
	// selection-only vocabulary: a case could "pass" merely because the model
	// CALLED filesystem/move even when the move returned an error the model then
	// narrated around. A pointer (not a bare bool) so an omitted field means "do
	// not check", distinct from an explicit false. Only meaningful with Tool set.
	ToolSucceeds *bool `json:"tool_succeeds,omitempty"`
	// State, when set, are declarative filesystem post-conditions checked AFTER
	// the turn's tool calls settle (see state.go / CheckState). This is what lets
	// a mutating case verify the real end-state (e.g. that move actually landed a
	// file at its intended path) rather than only which op was selected. The
	// os.Stat pass lives in CheckState, not here, so CheckExpectation stays pure
	// (no I/O) and unit-testable without a filesystem.
	State *State `json:"state,omitempty"`
}

// Turn is one exchange: a user prompt and what must be true about the model's
// response to it before the next turn (if any) is sent.
//
// Prompt may contain the literal placeholder "{{unique}}", replaced at run
// time (by runner.go) with a fresh random token generated once per case. Use
// this in any case that asks the model to create a uniquely-named resource
// (e.g. a directory), so re-running the case never collides with a leftover
// from a prior live run.
type Turn struct {
	Prompt string      `json:"prompt"`
	Expect Expectation `json:"expect,omitempty"`
}

// Case is one eval scenario, loaded from a JSON file. Exactly one of Prompt or
// Turns must be set: Prompt (+ top-level Expect) is sugar for a single-turn
// Case, used by most read-only-selection cases; Turns is for multi-turn
// scenarios like "stage, then confirm" that need a scripted second message.
type Case struct {
	ID          string      `json:"id"`
	Description string      `json:"description,omitempty"`
	Prompt      string      `json:"prompt,omitempty"`
	Expect      Expectation `json:"expect,omitempty"`
	Turns       []Turn      `json:"turns,omitempty"`
	// Setup, when set, stages a per-case scratch directory (with fake input
	// files) before the turns run, so a mutating case has realistic inputs to act
	// on. Fixtures are declarative and created with stdlib os only — never a
	// shell — per .claude/rules/darwin-execution.md. See runner.go's applySetup.
	Setup *Setup `json:"setup,omitempty"`
	// Teardown, when set, controls cleanup after the case finishes (pass or fail).
	Teardown *Teardown `json:"teardown,omitempty"`
	// Manual marks a case that needs a permission grant, a signed-in account, real
	// hardware, or is otherwise disruptive — so it cannot run in an unattended CI
	// pass. Manual cases still load (so -dry-run lists them) but are skipped by the
	// default run unless the caller opts in with Config.IncludeManual.
	Manual bool `json:"manual,omitempty"`
}

// Setup declares the fixtures a case needs before its turns run. The scratch
// directory is created at /tmp/mcp-eval-<unique>-<Scratch>, where <unique> is
// the same per-case random token used for "{{unique}}" substitution, so repeat
// or concurrent runs never collide. The resolved path is exposed to prompts and
// to State post-condition paths as "{{scratch}}".
type Setup struct {
	// Scratch is the trailing, caller-chosen segment of the scratch directory
	// name (e.g. "screenshots"). It must be a single plain path segment: no
	// separators, no "..", not absolute — a guardrail so a case file can never
	// point setup/teardown at an arbitrary location on disk.
	Scratch string `json:"scratch"`
	// Files are paths, relative to the scratch directory, created empty before
	// the turns run (intermediate directories are created as needed). Enough to
	// stage realistic inputs like fake screenshot files.
	Files []string `json:"files,omitempty"`
}

// Teardown controls post-case cleanup.
type Teardown struct {
	// RemoveScratch removes the entire scratch tree after the case ends, even on
	// failure. Recommended for every case with a Setup so a passing OR failing run
	// leaves no residue on disk.
	RemoveScratch bool `json:"remove_scratch,omitempty"`
}

// ResolvedTurns returns c's turns in their normalized []Turn form, expanding
// the Prompt+Expect sugar into a single-element slice. It is an error for a
// case to set both Prompt and Turns, or neither.
func (c Case) ResolvedTurns() ([]Turn, error) {
	hasPrompt := c.Prompt != ""
	hasTurns := len(c.Turns) > 0
	switch {
	case hasPrompt && hasTurns:
		return nil, fmt.Errorf("case %q: set either \"prompt\" or \"turns\", not both", c.ID)
	case hasPrompt:
		return []Turn{{Prompt: c.Prompt, Expect: c.Expect}}, nil
	case hasTurns:
		return c.Turns, nil
	default:
		return nil, fmt.Errorf("case %q: must set either \"prompt\" or \"turns\"", c.ID)
	}
}

// LoadCases reads every *.json file in dir (each a JSON array of Case), checks
// every case has a non-empty, unique ID and resolvable turns, and returns the
// combined, ID-sorted list. Sorting makes a run's case order — and so its
// printed output — deterministic regardless of filesystem directory order.
func LoadCases(dir string) ([]Case, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("evals: reading case directory %q: %w", dir, err)
	}

	var cases []Case
	seen := make(map[string]string) // id -> source file, for a clear duplicate-id error
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("evals: reading %s: %w", path, err)
		}
		var fileCases []Case
		if err := json.Unmarshal(data, &fileCases); err != nil {
			return nil, fmt.Errorf("evals: parsing %s: %w", path, err)
		}
		for _, c := range fileCases {
			if c.ID == "" {
				return nil, fmt.Errorf("evals: %s contains a case with no \"id\"", path)
			}
			if prior, dup := seen[c.ID]; dup {
				return nil, fmt.Errorf("evals: duplicate case id %q in %s (first seen in %s)", c.ID, path, prior)
			}
			seen[c.ID] = path
			if _, err := c.ResolvedTurns(); err != nil {
				return nil, fmt.Errorf("evals: %s: %w", path, err)
			}
			cases = append(cases, c)
		}
	}

	sort.Slice(cases, func(i, j int) bool { return cases[i].ID < cases[j].ID })
	return cases, nil
}
