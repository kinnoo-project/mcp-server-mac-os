**note**

Unit 17 (the final unit) of the capability-expansion roadmap: the "download X"
flow. It extends the existing `application` tool with two operations — a
read-only App Store search and an auto-commit "open this app's store page" — and
introduces the project's first **outbound-HTTP** builtin. This note records the
design choices.

## The flow, and why no install automation

"Download Slack" decomposes into three model-driven steps, each an existing or
new operation — no orchestration code:

1. `search_applications` (existing) — is it already installed locally?
2. `search_app_store` (new) — find it on the Mac App Store, with its numeric id.
3. `open_app_store_page` (new) — open that app's page in the App Store app so the
   user clicks "Get"/"Buy" themselves.

If the app isn't on the store, the model falls back to the existing staged
`open_website` (the vendor's download page). All of this is tool-description
guidance, not a fixed pipeline. **Installing/purchasing is never automated**:
there is no first-party CLI for it (the third-party `mas` tool is outside the
trusted-binary policy) and a purchase needs an Apple ID interaction. So the
server's job ends at opening the page — the transaction stays a human click. This
matches the roadmap's explicit non-goal.

## search_app_store — the first outbound-HTTP builtin

Every other builtin composes a trusted LOCAL binary. There is no local CLI for
App Store search, so this one issues a single HTTPS GET to Apple's public iTunes
Search API (`https://itunes.apple.com/search`, `entity=macSoftware`), parsing
name/seller/price/`trackId` from the JSON. It is a builtin (`binary: ""`,
`builtins_appstore.go`), read-only, low risk.

**Why net/http, not curl.** The same reason the whole engine avoids shells: an
in-process request has no argv/shell surface to harden, and it lets the URL be
assembled structurally. `curl` would also be a new binary to trust and would
reintroduce an argv the model's query flows through. The request is bound to the
request context with its own 10-second timeout and a 1-MiB response cap, so a
slow or oversized reply can't tie up the handler or flood the model's context.

**Why the query is injection-inert.** Nothing the model supplies shapes the
request beyond one query-string value. Scheme, host, and path come only from the
Go-side `appStoreSearchEndpoint` constant; the search text is carried as the
`url.Values` `term` key, which percent-encodes it. So a hostile value like `-e`
or `https://evil.example/#` can only land as an encoded `term` value — it can
never redirect the host or inject a second parameter. This is the guard recorded
in `reviewedFreeTextBuiltins`, pinned by
`TestBuildAppStoreSearchURL_QueryIsAlwaysAnEncodedTermValue`. No `--`/dash-guard
is needed precisely because there is no CLI and no argv — the encoding, not a
terminator, is the defense. (The pure URL-builder and JSON-render halves are
split out so both are unit-testable, and the full builtin is exercised
end-to-end against a local `httptest` stub so the suite never hits the network.)

## open_app_store_page — auto-commit, numeric id only

The forward command is `open macappstore://apps.apple.com/app/id<trackId>`. It
takes an **int** `track_id` (from a `search_app_store` result), not a name or
URL, on purpose: an int parameter is constrained to a whole number by the
normalizer before the mutator runs, so the `macappstore://` URL is assembled
entirely from digits — there is no free text in it and no injection surface. This
is the key difference from `open_website`, whose URL is free-form and therefore
stays **staged** behind the execute gate. Opening the App Store to a page is a
benign window-open with nothing to undo, so — like `open_settings` and
`focus_application` — it is **auto-commit** and **irreversible/low** (nil
Inverse; the auto-commit lane renders its own "cannot be undone").

## Surface / permissions

Both ops live in the existing `application` category, so no new MCP tool (tool
count stays 22). No TCC/Automation permission is involved — the search is a plain
network call and `open` needs no grant. The only runtime dependency is an
internet connection for the search, which the manifest summary and the error text
call out. Evals: one CI-safe selection routing case
(`search_app_store_routing`), with the live search, full download flow, and
not-in-store fallback kept manual (network/window side effects).
