// builtins_photos_test.go tests the pure, in-Go logic of the Photos read builtins:
// parsing the AppleScript scripts' delimited stdout, capping output, and rendering
// both the bulk row listing and the single-item detail (including the GPS-present
// vs GPS-absent branches, since coordinates are surfaced only by get_photo).
//
// The option-injection hardening that guards the free-text inputs (search_photos'
// query, get_photo's id, get_album_photos' album) is the SAME runOsascript "--"
// terminator seam proven by TestOsascriptCommand_InsertsTerminator
// (applescript_test.go) and TestInjection_OsascriptTerminatesHostileData
// (injection_sweep_test.go): every value reaches the fixed script as `on run argv`
// data after "--", so it is not re-driven here.
//
// SAFETY: nothing here launches osascript or touches the real Photos app — these
// tests feed the parser/renderer the kind of rows the scripts emit.
package engine

import (
	"strings"
	"testing"
)

// TestParsePhotoRows verifies well-formed tab-delimited rows are parsed and that a
// row with the wrong field count is skipped rather than corrupting the result.
func TestParsePhotoRows(t *testing.T) {
	stdout := strings.Join([]string{
		"ABC123/L0/001\tBeach day\tIMG_0001.HEIC\t2026-06-19 09:30\ttrue\t4032x3024",
		"malformed-row-without-tabs",
		"DEF456/L0/001\t\tIMG_0002.JPG\t2026-06-20 14:05\tfalse\t1920x1080",
		"", // blank line dropped by asRows
	}, "\n")

	got := parsePhotoRows(stdout)
	if len(got) != 2 {
		t.Fatalf("expected 2 parsed rows (malformed skipped), got %d: %+v", len(got), got)
	}
	if got[0].id != "ABC123/L0/001" || got[0].title != "Beach day" ||
		got[0].filename != "IMG_0001.HEIC" || got[0].date != "2026-06-19 09:30" ||
		got[0].favorite != "true" || got[0].dims != "4032x3024" {
		t.Errorf("first row parsed wrong: %+v", got[0])
	}
}

// TestRenderPhotoList verifies the listing exposes each item's id (what
// get_photo/export_photo consume), marks favorites, and uses a placeholder for an
// empty title.
func TestRenderPhotoList(t *testing.T) {
	out := renderPhotoList("2 item(s):", []photoRow{
		{id: "ABC123/L0/001", title: "Beach day", filename: "IMG_0001.HEIC", date: "2026-06-19 09:30", favorite: "true", dims: "4032x3024"},
		{id: "DEF456/L0/001", title: "", filename: "IMG_0002.JPG", date: "2026-06-20 14:05", favorite: "false", dims: "1920x1080"},
	})
	for _, want := range []string{"ABC123/L0/001", "Beach day", "★", "DEF456/L0/001", "(untitled)", "IMG_0002.JPG"} {
		if !strings.Contains(out, want) {
			t.Errorf("rendered list missing %q:\n%s", want, out)
		}
	}
	// A non-favorite must not get a star.
	if strings.Count(out, "★") != 1 {
		t.Errorf("expected exactly one favorite star, got:\n%s", out)
	}
}

// TestRenderPhotoDetail_WithLocation verifies the single-item detail surfaces GPS
// coordinates and altitude when present, plus the other metadata fields.
func TestRenderPhotoDetail_WithLocation(t *testing.T) {
	// id, title, filename, date, favorite, width, height, size,
	// altitude, latitude, longitude, description, keywords
	f := []string{
		"ABC123/L0/001", "Beach day", "IMG_0001.HEIC", "2026-06-19 09:30", "true",
		"4032", "3024", "2.4 MB", "12.5", "37.8199", "-122.4783", "Golden hour", "beach, sunset",
	}
	out := renderPhotoDetail(f)
	for _, want := range []string{"Beach day", "ABC123/L0/001", "IMG_0001.HEIC", "favorite: true",
		"4032x3024", "2.4 MB", "37.8199, -122.4783", "altitude 12.5 m", "Golden hour", "beach, sunset"} {
		if !strings.Contains(out, want) {
			t.Errorf("detail missing %q:\n%s", want, out)
		}
	}
}

// TestRenderPhotoDetail_NoLocation verifies an item without GPS shows a clear
// "(none)" for location/description/keywords rather than blank or stale values.
func TestRenderPhotoDetail_NoLocation(t *testing.T) {
	f := []string{
		"DEF456/L0/001", "", "IMG_0002.JPG", "2026-06-20 14:05", "false",
		"1920", "1080", "", "", "", "", "", "",
	}
	out := renderPhotoDetail(f)
	if !strings.Contains(out, "location: (none)") {
		t.Errorf("expected 'location: (none)' for an item without GPS:\n%s", out)
	}
	if !strings.Contains(out, "description: (none)") || !strings.Contains(out, "keywords: (none)") {
		t.Errorf("expected '(none)' placeholders for empty description/keywords:\n%s", out)
	}
	// With no size captured, the size line is omitted entirely.
	if strings.Contains(out, "size:") {
		t.Errorf("size line should be omitted when size is empty:\n%s", out)
	}
}

// TestCappedPhotoLimit covers the default, the hard ceiling, and a non-positive
// value falling back to the default.
func TestCappedPhotoLimit(t *testing.T) {
	cases := []struct {
		in   map[string]any
		want int
	}{
		{map[string]any{}, defaultPhotoLimit},
		{map[string]any{"limit": 10}, 10},
		{map[string]any{"limit": 999}, maxPhotoLimit},
		{map[string]any{"limit": 0}, defaultPhotoLimit},
		{map[string]any{"limit": -5}, defaultPhotoLimit},
	}
	for _, c := range cases {
		if got := cappedPhotoLimit(c.in); got != c.want {
			t.Errorf("cappedPhotoLimit(%v) = %d, want %d", c.in, got, c.want)
		}
	}
}
