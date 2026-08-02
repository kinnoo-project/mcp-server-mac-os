// mutate_maps.go implements the Apple Maps operations: opening Maps to a route,
// to a place search, or to a single pinned address.
//
// # Why every Maps operation is a "GUI handoff"
//
// Maps.app ships no AppleScript dictionary and no first-party CLI, so there is
// no supported way to ASK it a question and read the answer back. The only
// native control surface is its `maps://` URL scheme, which opens a window
// showing the result. That shapes the whole capability: these operations put the
// answer on the user's screen, they never return it to the conversation. The
// route distance, the ETA, and the list of nearby places exist only in the Maps
// window. Every operation summary and preview says so explicitly, because the
// model needs to set that expectation when it answers a question like "how far
// is the drive to San Francisco?" — it opens the route rather than reciting a
// number it cannot know.
//
// A future data tier (Apple Shortcuts wrapping "Get travel time" and "Search
// local businesses", executed through the existing shortcuts runner) could
// return real values; it is deliberately out of scope for this version, which
// ships no shortcuts for the user to import. See
// docs/ideas/maps-data-tier-deferred.md.
//
// # Lane: auto-commit, irreversible
//
// Opening a Maps window is benign — it needs no permission grant, changes no
// persistent state, and there is nothing to undo (hence a nil Inverse). So these
// operations are registry-marked auto_commit and run immediately rather than
// waiting behind the execute confirmation token; making "directions to the
// airport" a two-step confirmation would defeat the point of the capability.
//
// # Why the URL is safe to build from model-supplied text
//
// `open` dispatches WHATEVER scheme it is handed, so a model-supplied URL can
// never be passed through verbatim (that is precisely why open_website stays
// staged behind the execute gate). Here the model never supplies a URL: it
// supplies a destination, a search phrase, or an address, and this file
// assembles the URL around it. The scheme ("maps://") and every query key
// ("daddr", "saddr", "dirflg", "q", "address") are Go constants, the travel mode
// is a registry enum mapped through a fixed table, and the free text is
// percent-encoded by net/url before it is ever concatenated. A hostile value
// like "-e" or "; rm -rf /" therefore lands inside the query string as inert
// encoded data — it cannot become a second argument, a flag, or a different
// scheme. This is the same "the scheme is chosen by this code, never by the
// model" discipline that mutate_phone.go (tel:/facetime:), mutate_system.go
// (x-apple.systempreferences:), and stageOpenAppStorePage (macappstore:) apply.
// The forward command still places the finished URL after a "--" terminator so
// `open` cannot read it as one of its own options either.
package engine

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"mcp-server-mac-os/internal/registry"
)

// mapsURLScheme is the fixed scheme every Maps operation opens. It is a constant
// precisely so no code path can derive it from model input.
const mapsURLScheme = "maps://"

// maxMapsTextLen caps a single free-text Maps field. Real destinations, search
// phrases, and addresses are far shorter; the cap keeps an absurd payload out of
// the URL (and out of the preview shown to the user) without constraining any
// legitimate input.
const maxMapsTextLen = 256

// mapsDirFlags maps the registry's travel-mode enum onto the `dirflg` values the
// Maps URL scheme understands. Keeping it a table (rather than passing the mode
// through) guarantees the URL can only ever carry one of these four literals.
//
// Apple documents d/w/r; cycling ("c") follows the same pattern and matches the
// cycling directions Maps has shipped since Ventura, but it is the one value not
// covered by Apple's published URL-scheme reference. mapsModeCaveat below is
// attached to the preview for that mode so the user is told to confirm what Maps
// actually opened rather than being quietly given a driving route for a bike
// question. If it proves unsupported on a target macOS, the fix is to drop
// "cycling" from the manifest enum — never to silently substitute another mode.
var mapsDirFlags = map[string]string{
	"driving": "d",
	"walking": "w",
	"transit": "r",
	"cycling": "c",
}

// mapsModeCaveat is appended to the cycling preview. See mapsDirFlags.
const mapsModeCaveat = " (If this version of Maps can't show a cycling route it may open a driving one instead — check the mode shown in the window.)"

// mapsHandoffNote is the sentence every Maps preview carries. It is a single
// constant so the honesty guarantee — "the answer is on screen, not in this
// conversation" — is stated identically by all three operations and cannot drift
// apart as they evolve.
const mapsHandoffNote = "The answer appears in the Maps window; this tool can't read it back into the conversation."

// validateMapsText sanity-checks one free-text Maps field and returns the
// trimmed value. The structural injection defence is the percent-encoding in
// buildMapsURL (plus the "--" terminator in openMapsPlan); this adds the same
// clear-error guards the other mutators use, so bad input fails as a readable
// validation message instead of producing a nonsense URL.
//
// Control characters are rejected outright rather than encoded: no real place
// name contains one, and they are exactly what a smuggled second argument would
// rely on. A leading dash is deliberately NOT rejected — unlike an app name or a
// path, the value never reaches argv on its own (it is encoded inside a token
// that always begins "maps://"), and a legitimate search can start with one.
func validateMapsText(op, field, raw string) (string, error) {
	v := strings.TrimSpace(raw)
	if v == "" {
		return "", fmt.Errorf("%s: '%s' is required", op, field)
	}
	for _, r := range v {
		if r < 0x20 || r == 0x7f {
			return "", fmt.Errorf("%s: '%s' must not contain control characters", op, field)
		}
	}
	if len(v) > maxMapsTextLen {
		return "", fmt.Errorf("%s: '%s' is too long (%d characters, maximum %d)", op, field, len(v), maxMapsTextLen)
	}
	return v, nil
}

// mapsDirFlag translates a travel-mode enum value into its `dirflg` literal. The
// registry has already constrained the parameter to the enum, so an unknown mode
// means the manifest and this table have drifted apart; that is reported as an
// error rather than silently defaulting to driving, which would answer a walking
// or cycling question with a car route.
func mapsDirFlag(mode string) (string, error) {
	flag, ok := mapsDirFlags[strings.ToLower(strings.TrimSpace(mode))]
	if !ok {
		return "", fmt.Errorf("directions: unsupported travel mode %q", mode)
	}
	return flag, nil
}

// foldNear merges an optional area into the search text, producing e.g.
// "Philz Coffee near Pleasanton, CA". The area is folded into the query rather
// than sent as a separate coordinate parameter because the server has no
// geocoder: it cannot turn "Pleasanton, CA" into the latitude/longitude pair the
// URL scheme's location parameters expect. Maps resolves the phrase itself, and
// with no area at all it searches around the Mac's current location.
func foldNear(query, near string) string {
	if near == "" {
		return query
	}
	return query + " near " + near
}

// buildMapsURL assembles the final URL from a fixed scheme and an encoded query
// string. Every value in q is percent-encoded by url.Values.Encode, which is
// what makes model-supplied text inert inside the URL.
func buildMapsURL(q url.Values) string {
	return mapsURLScheme + "?" + q.Encode()
}

// openMapsPlan wraps a finished URL in the StagedPlan every Maps operation
// returns: `open -- <url>` (the terminator keeps `open` from reading the URL as
// an option) and a nil Inverse, because a window that opened is not something
// undo can meaningfully close.
func openMapsPlan(preview, mapsURL string) *StagedPlan {
	return &StagedPlan{
		Preview: preview,
		Forward: Command{Binary: "open", Args: []string{"--", mapsURL}},
		Inverse: nil,
	}
}

// stageDirections stages opening Maps with a route to a destination, optionally
// from an explicit origin (omitted means Maps routes from the Mac's current
// location) and in a chosen travel mode.
//
// This is also how "how far is it?" and "how long does it take?" questions are
// answered: the distance and ETA are shown on the route, so the operation opens
// the route and the preview tells the user where to read them.
func stageDirections(_ context.Context, _ registry.Capability, in map[string]any) (*StagedPlan, error) {
	destRaw, _ := getString(in, "destination")
	destination, err := validateMapsText("directions", "destination", destRaw)
	if err != nil {
		return nil, err
	}

	// Optional: an explicit starting point. Left out, Maps uses where the Mac is.
	var origin string
	if originRaw, ok := getString(in, "origin"); ok && strings.TrimSpace(originRaw) != "" {
		origin, err = validateMapsText("directions", "origin", originRaw)
		if err != nil {
			return nil, err
		}
	}

	// The registry supplies the enum default ("driving") when the caller omits it.
	mode, _ := getString(in, "mode")
	if strings.TrimSpace(mode) == "" {
		mode = "driving"
	}
	dirflg, err := mapsDirFlag(mode)
	if err != nil {
		return nil, err
	}

	q := url.Values{}
	q.Set("daddr", destination)
	if origin != "" {
		q.Set("saddr", origin)
	}
	q.Set("dirflg", dirflg)

	from := "your current location"
	if origin != "" {
		from = origin
	}
	preview := fmt.Sprintf("Open Maps with %s directions from %s to %s. %s",
		mapsModeAdjective(mode), from, destination, mapsHandoffNote)
	if dirflg == "c" {
		preview += mapsModeCaveat
	}
	return openMapsPlan(preview, buildMapsURL(q)), nil
}

// mapsModeAdjective renders a travel mode the way a person would say it in the
// preview sentence ("walking directions", "transit directions").
func mapsModeAdjective(mode string) string {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "walking":
		return "walking"
	case "transit":
		return "transit"
	case "cycling":
		return "cycling"
	default:
		return "driving"
	}
}

// stageSearchLocations stages opening Maps on a search for places or businesses
// — "Mexican restaurants", "Philz Coffee", "Tesla Supercharger". An optional
// area narrows it; without one Maps searches around the current location and
// orders results by proximity, which is how "what's the closest X?" is answered.
//
// Note what this cannot do: there is no way to impose a radius ("within 5
// miles") or to pick the single nearest result programmatically, because no
// results come back to us. Proximity preference is expressible only in the
// wording of the search Maps performs.
func stageSearchLocations(_ context.Context, _ registry.Capability, in map[string]any) (*StagedPlan, error) {
	queryRaw, _ := getString(in, "query")
	query, err := validateMapsText("search_locations", "query", queryRaw)
	if err != nil {
		return nil, err
	}

	var near string
	if nearRaw, ok := getString(in, "near"); ok && strings.TrimSpace(nearRaw) != "" {
		near, err = validateMapsText("search_locations", "near", nearRaw)
		if err != nil {
			return nil, err
		}
	}

	// Both fields are validated before folding, so the combined phrase inherits
	// their guarantees; it is then encoded as a single "q" value.
	search := foldNear(query, near)
	q := url.Values{}
	q.Set("q", search)

	where := "near your current location"
	if near != "" {
		where = fmt.Sprintf("near %s", near)
	}
	preview := fmt.Sprintf("Open Maps searching for %q %s, closest first. %s",
		query, where, mapsHandoffNote)
	return openMapsPlan(preview, buildMapsURL(q)), nil
}

// stageShowLocation stages opening Maps pinned to one specific address or place.
// It is the "show me where this is" counterpart to search_locations (which
// explores a category) and directions (which routes to somewhere), and it pairs
// naturally with the contacts capability: look up a contact's address, then show
// it on the map.
func stageShowLocation(_ context.Context, _ registry.Capability, in map[string]any) (*StagedPlan, error) {
	addressRaw, _ := getString(in, "address")
	address, err := validateMapsText("show_location", "address", addressRaw)
	if err != nil {
		return nil, err
	}

	q := url.Values{}
	q.Set("address", address)

	preview := fmt.Sprintf("Open Maps showing %s. %s", address, mapsHandoffNote)
	return openMapsPlan(preview, buildMapsURL(q)), nil
}
