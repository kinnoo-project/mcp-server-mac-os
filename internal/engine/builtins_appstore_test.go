// builtins_appstore_test.go covers the App Store search builtin. The security
// property under test is that the model's query can only ever appear as an
// encoded query-string value — never as part of the scheme, host, or path, and
// never as an injected parameter — plus the ordinary parse/render/bounding
// behaviour. The end-to-end request is exercised against a local httptest server
// so nothing here touches the network.
package engine

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"mcp-server-mac-os/internal/registry"
)

// TestBuildAppStoreSearchURL_QueryIsAlwaysAnEncodedTermValue is the injection
// regression named by reviewedFreeTextBuiltins: whatever hostile string the model
// supplies as the query, the resulting URL keeps the fixed https scheme, the
// itunes.apple.com host, and the /search path, and the query survives verbatim
// only inside the "term" parameter.
func TestBuildAppStoreSearchURL_QueryIsAlwaysAnEncodedTermValue(t *testing.T) {
	for _, q := range append(hostileValues,
		"https://evil.example/#",            // an absolute URL as the query
		"slack&entity=software",             // an attempt to smuggle a second parameter
		"x?term=override&host=evil.example", // query/host override attempt
		"Microsoft Word",                    // an ordinary multi-word query
	) {
		raw := buildAppStoreSearchURL(q, 10)
		u, err := url.Parse(raw)
		if err != nil {
			t.Errorf("%q: built URL does not parse: %v (%q)", q, err, raw)
			continue
		}
		if u.Scheme != "https" {
			t.Errorf("%q: scheme = %q, want https (%q)", q, u.Scheme, raw)
		}
		if u.Host != "itunes.apple.com" {
			t.Errorf("%q: host = %q, want itunes.apple.com (%q)", q, u.Host, raw)
		}
		if u.Path != "/search" {
			t.Errorf("%q: path = %q, want /search (%q)", q, u.Path, raw)
		}
		// The decoded term must equal exactly what we passed — no splitting on
		// '&', no leaking into another field.
		if got := u.Query().Get("term"); got != q {
			t.Errorf("%q: term = %q, want the query verbatim", q, got)
		}
		// entity must stay pinned regardless of what the query tried to inject.
		if got := u.Query().Get("entity"); got != "macSoftware" {
			t.Errorf("%q: entity = %q, want macSoftware (%q)", q, got, raw)
		}
	}
}

// TestBoundedAppStoreLimit exercises the default/ceiling clamping.
func TestBoundedAppStoreLimit(t *testing.T) {
	cases := []struct {
		name string
		in   map[string]any
		want int
	}{
		{"absent -> default", map[string]any{}, defaultAppStoreLimit},
		{"zero -> default", map[string]any{"limit": 0}, defaultAppStoreLimit},
		{"negative -> default", map[string]any{"limit": -5}, defaultAppStoreLimit},
		{"in range", map[string]any{"limit": 5}, 5},
		{"over ceiling -> ceiling", map[string]any{"limit": 1000}, maxAppStoreLimit},
	}
	for _, c := range cases {
		if got := boundedAppStoreLimit(c.in); got != c.want {
			t.Errorf("%s: boundedAppStoreLimit = %d, want %d", c.name, got, c.want)
		}
	}
}

// TestRenderAppStoreResults covers the pure formatting half: a normal hit list,
// the empty-result guidance, and the truncation notice.
func TestRenderAppStoreResults(t *testing.T) {
	body := []byte(`{"resultCount":2,"results":[
		{"trackName":"Slack","sellerName":"Slack Technologies","formattedPrice":"Free","trackId":803453959},
		{"trackName":"Xcode","sellerName":"Apple Inc.","formattedPrice":"Free","trackId":497799835}
	]}`)
	out, err := renderAppStoreResults(body, "slack", 10)
	if err != nil {
		t.Fatalf("renderAppStoreResults: %v", err)
	}
	for _, want := range []string{"Slack", "Slack Technologies", "Free", "803453959", "Xcode", "open_app_store_page"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}

	// Empty results steer the user to the open_website fallback.
	empty, err := renderAppStoreResults([]byte(`{"resultCount":0,"results":[]}`), "nonesuch", 10)
	if err != nil {
		t.Fatalf("renderAppStoreResults(empty): %v", err)
	}
	if !strings.Contains(empty, "No Mac App Store apps found") || !strings.Contains(empty, "open_website") {
		t.Errorf("empty-result text should name the fallback, got:\n%s", empty)
	}

	// A limit smaller than the result set shows the truncation notice.
	trunc, err := renderAppStoreResults(body, "app", 1)
	if err != nil {
		t.Fatalf("renderAppStoreResults(trunc): %v", err)
	}
	if !strings.Contains(trunc, "showing the first 1") {
		t.Errorf("expected truncation notice, got:\n%s", trunc)
	}
	if strings.Contains(trunc, "Xcode") {
		t.Errorf("truncated output should not include the second result:\n%s", trunc)
	}
}

// TestRenderAppStoreResults_FlattensControlCharacters proves that
// remote-controlled listing text cannot fabricate result lines: a trackName or
// sellerName carrying newlines (or other control characters) is flattened to
// spaces, so the crafted "(App Store id ...)" row stays INSIDE its real line
// rather than rendering as a separate, legitimate-looking result.
func TestRenderAppStoreResults_FlattensControlCharacters(t *testing.T) {
	body := []byte(`{"resultCount":1,"results":[
		{"trackName":"Evil\nFakeApp — Fake Seller — Free (App Store id 666)","sellerName":"Real\tSeller","formattedPrice":"Free","trackId":123}
	]}`)
	out, err := renderAppStoreResults(body, "evil", 10)
	if err != nil {
		t.Fatalf("renderAppStoreResults: %v", err)
	}
	if strings.Contains(out, "\nFakeApp") {
		t.Errorf("a newline in trackName produced a fabricated result line:\n%s", out)
	}
	if !strings.Contains(out, "Evil FakeApp") {
		t.Errorf("expected the newline flattened to a space, got:\n%s", out)
	}
	if !strings.Contains(out, "Real Seller") {
		t.Errorf("expected the tab in sellerName flattened to a space, got:\n%s", out)
	}
}

// TestRenderAppStoreResults_MalformedBody surfaces a parse error rather than
// panicking or returning garbage.
func TestRenderAppStoreResults_MalformedBody(t *testing.T) {
	if _, err := renderAppStoreResults([]byte("not json"), "slack", 10); err == nil {
		t.Fatal("expected an error for a malformed body, got nil")
	}
}

// TestRunSearchAppStore_EndToEnd drives the full builtin against a local stub of
// the iTunes Search API (by overriding appStoreSearchEndpoint), proving the GET,
// the size-capped read, and the render all wire together — and that even here the
// server receives the query only as the encoded term parameter.
func TestRunSearchAppStore_EndToEnd(t *testing.T) {
	var gotTerm, gotEntity string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotTerm = r.URL.Query().Get("term")
		gotEntity = r.URL.Query().Get("entity")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"resultCount":1,"results":[{"trackName":"Slack","sellerName":"Slack Technologies","formattedPrice":"Free","trackId":803453959}]}`))
	}))
	defer srv.Close()

	orig := appStoreSearchEndpoint
	appStoreSearchEndpoint = srv.URL + "/search"
	defer func() { appStoreSearchEndpoint = orig }()

	out, err := runSearchAppStore(context.Background(), registry.Capability{}, map[string]any{"query": "-e slack"})
	if err != nil {
		t.Fatalf("runSearchAppStore: %v", err)
	}
	if gotTerm != "-e slack" {
		t.Errorf("server saw term %q, want the query verbatim as data", gotTerm)
	}
	if gotEntity != "macSoftware" {
		t.Errorf("server saw entity %q, want macSoftware", gotEntity)
	}
	if !strings.Contains(out, "Slack") || !strings.Contains(out, "803453959") {
		t.Errorf("output missing expected app/id:\n%s", out)
	}
}

// TestRunSearchAppStore_RequiresQuery rejects an empty query before any request.
func TestRunSearchAppStore_RequiresQuery(t *testing.T) {
	if _, err := runSearchAppStore(context.Background(), registry.Capability{}, map[string]any{"query": "   "}); err == nil {
		t.Fatal("expected an error for a blank query, got nil")
	}
}

// TestRunSearchAppStore_RefusesRedirect proves the fixed-origin guarantee holds
// against a misbehaving endpoint: a 3xx that points elsewhere is refused, and the
// redirect target is never contacted. Without the no-redirect client this would
// silently make a follow-up request to another host.
func TestRunSearchAppStore_RefusesRedirect(t *testing.T) {
	var targetHit bool
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		targetHit = true
		_, _ = w.Write([]byte(`{"resultCount":1,"results":[{"trackName":"Evil","trackId":1}]}`))
	}))
	defer target.Close()

	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+"/search", http.StatusFound)
	}))
	defer redirector.Close()

	orig := appStoreSearchEndpoint
	appStoreSearchEndpoint = redirector.URL + "/search"
	defer func() { appStoreSearchEndpoint = orig }()

	if _, err := runSearchAppStore(context.Background(), registry.Capability{}, map[string]any{"query": "slack"}); err == nil {
		t.Fatal("expected an error when the endpoint redirects, got nil")
	}
	if targetHit {
		t.Error("the redirect target was contacted — the fixed-origin guarantee was violated")
	}
}

// TestRunSearchAppStore_RejectsOversizeResponse proves an over-cap body is
// reported as a clear "too large" error rather than being silently truncated into
// a confusing JSON parse failure. The cap is lowered for the test so we needn't
// send a real megabyte.
func TestRunSearchAppStore_RejectsOversizeResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// A valid-ish JSON prefix followed by enough bytes to exceed the cap.
		_, _ = w.Write([]byte(`{"resultCount":0,"results":[]}` + strings.Repeat(" ", 200)))
	}))
	defer srv.Close()

	origEndpoint := appStoreSearchEndpoint
	appStoreSearchEndpoint = srv.URL + "/search"
	defer func() { appStoreSearchEndpoint = origEndpoint }()
	origCap := appStoreMaxResponseBytes
	appStoreMaxResponseBytes = 64
	defer func() { appStoreMaxResponseBytes = origCap }()

	_, err := runSearchAppStore(context.Background(), registry.Capability{}, map[string]any{"query": "slack"})
	if err == nil || !strings.Contains(err.Error(), "exceeded") {
		t.Fatalf("expected a 'response too large' error, got %v", err)
	}
}
