// builtins_appstore.go implements search_app_store — the "is there an app for
// this, and where do I get it?" read that backs the download/install flow
// (paired with the open_app_store_page mutator in mutate_apps.go).
//
// # The project's first outbound-HTTP builtin
//
// Every other builtin composes a trusted LOCAL binary; this one instead makes a
// single HTTPS GET against Apple's public iTunes Search API
// (https://itunes.apple.com/search) to look up Mac App Store titles. There is no
// first-party CLI for App Store search (the `mas` tool is third-party and lives
// outside this server's trusted-binary policy), so a direct HTTP call is the only
// non-shell way to answer the query. We use the standard library's net/http
// rather than shelling out to curl for the same reason the rest of the engine
// avoids shells: an in-process request has no argv/shell surface to harden, the
// URL is assembled structurally (see buildAppStoreSearchURL), and the call is
// bound to the request context with its own timeout and a response-size cap.
//
// # Why the model's query is safe
//
// Nothing the model supplies shapes the request beyond the value of a single
// query-string parameter. The scheme, host, and path are Go-side constants; the
// search text is carried as the url.Values "term" key, which percent-encodes it,
// so a hostile value like "-e" or "https://evil.example/#" can only ever land as
// an encoded query value — it can never redirect the host or inject a new
// parameter. That is this builtin's entry in reviewedFreeTextBuiltins.
package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"mcp-server-mac-os/internal/registry"
)

// appStoreSearchEndpoint is the FIXED base URL for the App Store lookup. It is a
// var (not a const) solely so a test can point the builtin at a local httptest
// server; production never changes it. The model cannot influence it — only the
// "term" query value below is model-controlled.
var appStoreSearchEndpoint = "https://itunes.apple.com/search"

// appStoreHTTPClient is the dedicated client for the App Store lookup. It
// REFUSES to follow redirects: the endpoint is a fixed Apple host that answers
// 200 with JSON, and a 3xx would silently send the follow-up request to a
// DIFFERENT origin — defeating the whole "scheme/host/path are Go-side
// constants" guarantee this builtin rests on. Refusing a redirect surfaces as a
// clear error instead. (Its timeout is applied per-request via the context, not
// on the client, so the httptest-backed tests can override the endpoint freely.)
var appStoreHTTPClient = &http.Client{
	CheckRedirect: func(*http.Request, []*http.Request) error {
		return errors.New("refusing to follow a redirect to another origin")
	},
}

// appStoreMaxResponseBytes caps how much of the response body we read, so a
// surprise multi-megabyte payload cannot balloon memory or the model's context.
// 1 MiB comfortably holds the default result set. A var (not a const) so a test
// can lower it to exercise the over-limit path without sending a real megabyte.
var appStoreMaxResponseBytes = 1 << 20

const (
	// appStoreSearchTimeout bounds the single outbound request so a slow or
	// hanging endpoint cannot tie up the handler; it is well under the engine's
	// 2-minute subprocess ceiling because this is a lightweight JSON lookup.
	appStoreSearchTimeout = 10 * time.Second
	// Result-count bounds mirror the app-listing reads: a readable default and a
	// hard ceiling so a broad search cannot flood the model's context.
	defaultAppStoreLimit = 10
	maxAppStoreLimit     = 25
)

// appStoreResult is the subset of an iTunes Search API result entry this builtin
// surfaces: enough for the user to recognise the app and for open_app_store_page
// to target it by id. Unmapped fields in the response are ignored.
type appStoreResult struct {
	TrackName      string `json:"trackName"`
	SellerName     string `json:"sellerName"`
	FormattedPrice string `json:"formattedPrice"`
	TrackID        int64  `json:"trackId"`
}

// appStoreSearchResponse is the top-level shape of the iTunes Search API reply.
type appStoreSearchResponse struct {
	ResultCount int              `json:"resultCount"`
	Results     []appStoreResult `json:"results"`
}

// runSearchAppStore performs the outbound App Store lookup: it builds the fixed
// search URL with the model's query as an encoded parameter, issues one
// context-bound GET with a size-capped body read, and renders the parsed results
// into a bounded listing that names each app's App Store id (the input
// open_app_store_page needs).
func runSearchAppStore(ctx context.Context, _ registry.Capability, in map[string]any) (string, error) {
	query, _ := getString(in, "query")
	query = strings.TrimSpace(query)
	if query == "" {
		return "", fmt.Errorf("search_app_store: 'query' is required")
	}
	limit := boundedAppStoreLimit(in)

	// Bind an independent timeout on top of the request context so a hung
	// endpoint is abandoned even if the client never cancels.
	ctx, cancel := context.WithTimeout(ctx, appStoreSearchTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, buildAppStoreSearchURL(query, limit), nil)
	if err != nil {
		return "", fmt.Errorf("search_app_store: could not build request: %w", err)
	}
	resp, err := appStoreHTTPClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("search_app_store: request to the App Store failed (check your internet connection): %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("search_app_store: the App Store returned HTTP %d", resp.StatusCode)
	}

	// Read one byte past the cap so an over-limit body is DETECTED rather than
	// silently truncated (which would only surface later as a confusing JSON
	// parse error). If we got more than the cap, the response is too large.
	body, err := io.ReadAll(io.LimitReader(resp.Body, int64(appStoreMaxResponseBytes)+1))
	if err != nil {
		return "", fmt.Errorf("search_app_store: reading the App Store response failed: %w", err)
	}
	if len(body) > appStoreMaxResponseBytes {
		return "", fmt.Errorf("search_app_store: the App Store response exceeded the %d-byte limit", appStoreMaxResponseBytes)
	}
	return renderAppStoreResults(body, query, limit)
}

// boundedAppStoreLimit reads the optional "limit" parameter, applying the default
// when absent/invalid and the hard ceiling regardless of what is asked.
func boundedAppStoreLimit(in map[string]any) int {
	limit, ok := getInt(in, "limit")
	if !ok || limit <= 0 {
		return defaultAppStoreLimit
	}
	if limit > maxAppStoreLimit {
		return maxAppStoreLimit
	}
	return limit
}

// buildAppStoreSearchURL assembles the lookup URL. It is pure and split out so a
// test can prove the security-critical property directly: whatever the query
// contains, the scheme/host/path come only from appStoreSearchEndpoint and the
// query text appears solely as the percent-encoded "term" value. entity is
// pinned to macSoftware so only Mac apps come back.
func buildAppStoreSearchURL(query string, limit int) string {
	q := url.Values{}
	q.Set("term", query)
	q.Set("entity", "macSoftware")
	q.Set("limit", strconv.Itoa(limit))

	// appStoreSearchEndpoint is a controlled constant; parse errors are not
	// reachable in production, so fall back to raw concatenation only defensively.
	u, err := url.Parse(appStoreSearchEndpoint)
	if err != nil {
		return appStoreSearchEndpoint + "?" + q.Encode()
	}
	u.RawQuery = q.Encode()
	return u.String()
}

// renderAppStoreResults is the pure formatting half of runSearchAppStore: it
// parses the JSON body and renders a bounded, human-readable listing, split out
// so parsing/formatting/truncation is unit-testable without a network call. Each
// line ends with the App Store id so the model can feed it to open_app_store_page.
func renderAppStoreResults(body []byte, query string, limit int) (string, error) {
	var parsed appStoreSearchResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return "", fmt.Errorf("search_app_store: could not parse the App Store response: %w", err)
	}
	if len(parsed.Results) == 0 {
		return fmt.Sprintf("No Mac App Store apps found for %q. It may not be on the App Store — try open_website to the vendor's download page instead.", query), nil
	}

	results := parsed.Results
	truncated := false
	if len(results) > limit {
		results = results[:limit]
		truncated = true
	}

	var b strings.Builder
	fmt.Fprintf(&b, "Found %d Mac App Store result(s) for %q", len(parsed.Results), query)
	if truncated {
		fmt.Fprintf(&b, " (showing the first %d)", limit)
	}
	b.WriteString(":\n")
	for _, r := range results {
		price := strings.TrimSpace(r.FormattedPrice)
		if price == "" {
			price = "price unknown"
		}
		seller := strings.TrimSpace(r.SellerName)
		if seller == "" {
			seller = "unknown seller"
		}
		name := strings.TrimSpace(r.TrackName)
		if name == "" {
			name = "(untitled)"
		}
		fmt.Fprintf(&b, "  %s — %s — %s (App Store id %d)\n", name, seller, price, r.TrackID)
	}
	b.WriteString("\nTo open one of these in the App Store, use open_app_store_page with its id.")
	return compactOutput(b.String()), nil
}
