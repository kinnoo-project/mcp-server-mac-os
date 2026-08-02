// mutate_maps_test.go covers the Apple Maps operations.
//
// Nothing here opens Maps. Every Maps operation's effect is "run `open` with a
// URL", and the URL is assembled by pure functions, so the tests stage plans and
// inspect the resulting command instead of executing it — the same approach the
// other `open`-backed mutators take. The live path (a real Maps window showing a
// real route) is on the manual smoke checklist, since only a human can confirm
// what the window actually displays.
package engine

import (
	"net/url"
	"strings"
	"testing"

	"mcp-server-mac-os/internal/registry"
)

// mapsCap is the capability value handed to the Maps mutators in these tests.
// It is deliberately zero: the Maps mutators read every input from the
// normalized parameter map and derive nothing from the capability record, so a
// zero value keeps these unit tests independent of manifest loading.
var mapsCap = registry.Capability{}

// stageMapsURL stages an operation and returns the URL its forward command would
// open, failing the test if the plan does not have the exact shape every Maps
// operation promises: `open -- <maps:// URL>` with no undo.
func stageMapsURL(t *testing.T, stage func() (*StagedPlan, error)) (string, *StagedPlan) {
	t.Helper()
	plan, err := stage()
	if err != nil {
		t.Fatalf("stage returned an error: %v", err)
	}
	if plan.Forward.Binary != "open" {
		t.Fatalf("forward binary = %q, want open", plan.Forward.Binary)
	}
	if len(plan.Forward.Args) != 2 || plan.Forward.Args[0] != "--" {
		t.Fatalf("forward args = %q, want exactly [-- <url>]", plan.Forward.Args)
	}
	if plan.Inverse != nil {
		t.Errorf("opening a Maps window has no inverse, got %+v", plan.Inverse)
	}
	if !strings.HasPrefix(plan.Forward.Args[1], "maps://?") {
		t.Fatalf("URL %q does not start with the pinned maps:// scheme", plan.Forward.Args[1])
	}
	return plan.Forward.Args[1], plan
}

// mapsQuery parses a staged Maps URL back into its query parameters so tests can
// assert on decoded values rather than on brittle encoded strings.
func mapsQuery(t *testing.T, raw string) url.Values {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("staged URL %q does not parse: %v", raw, err)
	}
	if u.Scheme != "maps" {
		t.Fatalf("staged URL scheme = %q, want maps", u.Scheme)
	}
	return u.Query()
}

func TestMapsDirFlag_AllModes(t *testing.T) {
	for mode, want := range map[string]string{
		"driving": "d",
		"walking": "w",
		"transit": "r",
		"cycling": "c",
	} {
		got, err := mapsDirFlag(mode)
		if err != nil {
			t.Errorf("%s: unexpected error: %v", mode, err)
			continue
		}
		if got != want {
			t.Errorf("%s: dirflg = %q, want %q", mode, got, want)
		}
	}
	// An unknown mode must fail loudly rather than fall back to driving: silently
	// answering a bike question with a car route would be a wrong answer.
	if _, err := mapsDirFlag("teleport"); err == nil {
		t.Error("expected an error for an unknown travel mode")
	}
}

// TestStageDirections_DefaultsToDrivingFromCurrentLocation checks the commonest
// shape: destination only. No saddr means Maps routes from where the Mac is.
func TestStageDirections_DefaultsToDrivingFromCurrentLocation(t *testing.T) {
	raw, plan := stageMapsURL(t, func() (*StagedPlan, error) {
		return stageDirections(nil, mapsCap, map[string]any{
			"destination": "San Francisco, CA",
		})
	})
	q := mapsQuery(t, raw)
	if got := q.Get("daddr"); got != "San Francisco, CA" {
		t.Errorf("daddr = %q, want the destination verbatim", got)
	}
	if q.Has("saddr") {
		t.Errorf("saddr should be absent when no origin is given, got %q", q.Get("saddr"))
	}
	if got := q.Get("dirflg"); got != "d" {
		t.Errorf("dirflg = %q, want d (driving default)", got)
	}
	if !strings.Contains(plan.Preview, "your current location") {
		t.Errorf("preview should say the route starts at the current location:\n%s", plan.Preview)
	}
}

func TestStageDirections_OriginAndModes(t *testing.T) {
	for mode, wantFlag := range map[string]string{
		"driving": "d",
		"walking": "w",
		"transit": "r",
		"cycling": "c",
	} {
		raw, _ := stageMapsURL(t, func() (*StagedPlan, error) {
			return stageDirections(nil, mapsCap, map[string]any{
				"destination": "Apple Park",
				"origin":      "Cupertino, CA",
				"mode":        mode,
			})
		})
		q := mapsQuery(t, raw)
		if got := q.Get("saddr"); got != "Cupertino, CA" {
			t.Errorf("%s: saddr = %q, want the origin verbatim", mode, got)
		}
		if got := q.Get("daddr"); got != "Apple Park" {
			t.Errorf("%s: daddr = %q", mode, got)
		}
		if got := q.Get("dirflg"); got != wantFlag {
			t.Errorf("%s: dirflg = %q, want %q", mode, got, wantFlag)
		}
	}
}

// TestStageDirections_CyclingCarriesCaveat guards the one travel mode Apple's
// published URL-scheme reference does not cover: the user must be told to check
// what Maps actually opened rather than trusting a possibly-driving route.
func TestStageDirections_CyclingCarriesCaveat(t *testing.T) {
	_, plan := stageMapsURL(t, func() (*StagedPlan, error) {
		return stageDirections(nil, mapsCap, map[string]any{
			"destination": "Apple Park",
			"mode":        "cycling",
		})
	})
	if !strings.Contains(plan.Preview, "cycling") {
		t.Errorf("preview should name the cycling mode:\n%s", plan.Preview)
	}
	if !strings.Contains(plan.Preview, "driving one instead") {
		t.Errorf("cycling preview should carry the unsupported-mode caveat:\n%s", plan.Preview)
	}
}

func TestStageSearchLocations_NearFolding(t *testing.T) {
	// With an area: it is folded into the single q value, because there is no
	// geocoder here to turn a place name into coordinates.
	raw, plan := stageMapsURL(t, func() (*StagedPlan, error) {
		return stageSearchLocations(nil, mapsCap, map[string]any{
			"query": "Philz Coffee",
			"near":  "Pleasanton, CA",
		})
	})
	q := mapsQuery(t, raw)
	if got := q.Get("q"); got != "Philz Coffee near Pleasanton, CA" {
		t.Errorf("q = %q, want the area folded into the search text", got)
	}
	if !strings.Contains(plan.Preview, "Pleasanton, CA") {
		t.Errorf("preview should name the area:\n%s", plan.Preview)
	}

	// Without an area: the query is untouched and Maps searches around the Mac.
	raw2, plan2 := stageMapsURL(t, func() (*StagedPlan, error) {
		return stageSearchLocations(nil, mapsCap, map[string]any{
			"query": "coffee shops",
		})
	})
	if got := mapsQuery(t, raw2).Get("q"); got != "coffee shops" {
		t.Errorf("q = %q, want the query unchanged when no area is given", got)
	}
	if !strings.Contains(plan2.Preview, "current location") {
		t.Errorf("preview should say the search is around the current location:\n%s", plan2.Preview)
	}
}

func TestStageShowLocation_PinsTheAddress(t *testing.T) {
	raw, _ := stageMapsURL(t, func() (*StagedPlan, error) {
		return stageShowLocation(nil, mapsCap, map[string]any{
			"address": "1 Infinite Loop, Cupertino, CA",
		})
	})
	q := mapsQuery(t, raw)
	if got := q.Get("address"); got != "1 Infinite Loop, Cupertino, CA" {
		t.Errorf("address = %q, want the address verbatim", got)
	}
	if q.Has("q") || q.Has("daddr") {
		t.Errorf("show_location should use only the address key, got %v", q)
	}
}

// TestStageMaps_PreviewsStateTheHandoff is the honesty guard: every Maps preview
// must tell the user the answer is on screen and not in the conversation, so the
// model never implies it read a distance or an ETA it cannot see.
func TestStageMaps_PreviewsStateTheHandoff(t *testing.T) {
	previews := []struct {
		name string
		plan func() (*StagedPlan, error)
	}{
		{"directions", func() (*StagedPlan, error) {
			return stageDirections(nil, mapsCap, map[string]any{"destination": "SFO"})
		}},
		{"search_locations", func() (*StagedPlan, error) {
			return stageSearchLocations(nil, mapsCap, map[string]any{"query": "gas stations"})
		}},
		{"show_location", func() (*StagedPlan, error) {
			return stageShowLocation(nil, mapsCap, map[string]any{"address": "Golden Gate Bridge"})
		}},
	}
	for _, p := range previews {
		plan, err := p.plan()
		if err != nil {
			t.Fatalf("%s: %v", p.name, err)
		}
		if !strings.Contains(plan.Preview, "Maps window") {
			t.Errorf("%s preview must state the GUI handoff:\n%s", p.name, plan.Preview)
		}
	}
}

func TestValidateMapsText_Rejections(t *testing.T) {
	cases := []struct {
		name string
		in   string
	}{
		{"empty", ""},
		{"whitespace only", "   "},
		{"newline", "Apple Park\nrm -rf /"},
		{"NUL", "Apple\x00Park"},
		{"DEL", "Apple\x7fPark"},
		{"too long", strings.Repeat("a", maxMapsTextLen+1)},
	}
	for _, tc := range cases {
		if _, err := validateMapsText("directions", "destination", tc.in); err == nil {
			t.Errorf("%s: expected a validation error", tc.name)
		}
	}
	// A leading dash is fine here (unlike an app name or a path): the value is
	// percent-encoded inside the URL and never reaches argv on its own.
	if got, err := validateMapsText("search_locations", "query", " -e "); err != nil || got != "-e" {
		t.Errorf("dash-leading search text should be accepted and trimmed, got %q err=%v", got, err)
	}
}

func TestStageMaps_MissingRequiredParamFails(t *testing.T) {
	if _, err := stageDirections(nil, mapsCap, map[string]any{}); err == nil {
		t.Error("directions without a destination should fail")
	}
	if _, err := stageSearchLocations(nil, mapsCap, map[string]any{}); err == nil {
		t.Error("search_locations without a query should fail")
	}
	if _, err := stageShowLocation(nil, mapsCap, map[string]any{}); err == nil {
		t.Error("show_location without an address should fail")
	}
}

// TestMaps_HostileValuesLandAsData is the injection regression test the
// reviewedFreeTextMutators entries point at. Every Maps operation embeds
// model-supplied text in a URL handed to `open`, so the guarantee under test is
// that a value like "-e" or "; rm -rf /" survives as an inert, percent-encoded
// query VALUE — it never splits into a second argument, never becomes a flag,
// and never changes the scheme `open` will dispatch.
func TestMaps_HostileValuesLandAsData(t *testing.T) {
	ops := []struct {
		name  string
		key   string
		stage func(h string) (*StagedPlan, error)
	}{
		{"directions", "daddr", func(h string) (*StagedPlan, error) {
			return stageDirections(nil, mapsCap, map[string]any{"destination": h})
		}},
		{"directions/origin", "saddr", func(h string) (*StagedPlan, error) {
			return stageDirections(nil, mapsCap, map[string]any{"destination": "SFO", "origin": h})
		}},
		{"search_locations", "q", func(h string) (*StagedPlan, error) {
			return stageSearchLocations(nil, mapsCap, map[string]any{"query": h})
		}},
		{"show_location", "address", func(h string) (*StagedPlan, error) {
			return stageShowLocation(nil, mapsCap, map[string]any{"address": h})
		}},
	}

	for _, op := range ops {
		for _, h := range hostileValues {
			plan, err := op.stage(h)
			if err != nil {
				// Control-character payloads are rejected up front by
				// validateMapsText. That is a valid outcome: nothing is built at all.
				if strings.ContainsAny(h, "\n\x00") {
					continue
				}
				t.Errorf("%s: %q was rejected unexpectedly: %v", op.name, h, err)
				continue
			}

			// The command must stay exactly two tokens: the terminator and one URL.
			// A hostile value that had escaped encoding would show up here as extra
			// argv elements.
			args := plan.Forward.Args
			if plan.Forward.Binary != "open" || len(args) != 2 || args[0] != "--" {
				t.Errorf("%s: %q produced argv %q, want [-- <url>]", op.name, h, args)
				continue
			}
			if !strings.HasPrefix(args[1], "maps://?") {
				t.Errorf("%s: %q escaped the pinned scheme: %q", op.name, h, args[1])
				continue
			}
			// And it must round-trip: the decoded query value equals the original
			// hostile string, proving it rode as data.
			u, err := url.Parse(args[1])
			if err != nil {
				t.Errorf("%s: %q produced an unparseable URL %q: %v", op.name, h, args[1], err)
				continue
			}
			want := strings.TrimSpace(h)
			if got := u.Query().Get(op.key); got != want {
				t.Errorf("%s: %q landed as %q under key %q, want the value verbatim", op.name, h, got, op.key)
			}
		}
	}
}
