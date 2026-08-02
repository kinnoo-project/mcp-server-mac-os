# Maps data tier — deferred by owner decision, not in the active build order

**Status:** deferred 2026-08-01, at the same time the GUI-handoff Maps capability
(`application-maps`) shipped. No code written for this tier. This note preserves
the design so a future revisit starts from the decision rather than re-deriving
it (mirrors `stocks-capability-deferred.md` and `shortcuts-runner-deferred.md`).

## What shipped, and what it can't do

`application-maps` v1 ships three operations — `directions`, `search_locations`,
`show_location` — that build a `maps://` URL Go-side and open Maps.app. They put
the answer **on screen**. See `docs/issues/note-maps-design.md`.

What they cannot do is return a value. Ask "how far is the drive to San
Francisco?" and the server opens the route; the mileage and the ETA are drawn in
the Maps window and never reach the conversation. Same for "what's the closest
coffee shop?" — the results are on screen, but no name, address, or distance
comes back. That is not an implementation gap: Maps.app has no AppleScript
dictionary and no first-party CLI, so there is nothing to query.

## The deferred design: Shortcuts as the data tier

macOS Shortcuts ships App Intents that *do* return this data, and the server
already has a `shortcuts` domain that runs a user-installed shortcut by name.
The pieces that would close the gap:

- **`Get travel time`** (Maps actions) — takes a start and end, returns a
  duration and distance. Backs a `travel_time` operation answering "how far" and
  "how long" with real numbers.
- **`Search local businesses` / `Get nearby places`** — returns a list of places
  with names, addresses, and distances. Backs a `find_places` operation, and
  makes "the closest coffee shop" answerable as a specific place rather than a
  window.
- Both would be **read-only** operations returning rendered text, unlike the
  auto-commit GUI ops that ship today.

Shape: the repo would ship `.shortcut` files, the user imports them once, and
the operations invoke them by a pinned name through the existing runner.

## Why it's deferred

1. **This version intentionally ships no shortcuts for the user to import.** That
   is the owner's call and the primary reason. Adding an import step — plus the
   Shortcuts permission prompts that come with a first run — changes the setup
   story for the whole server, not just for Maps.
2. **The runner cannot capture structured output yet.** `run_shortcut` builds
   `shortcuts run [-i <path>] -- <name>`; it does not pass `-o/--output-path`, so
   only whatever the shortcut writes to stdout is rendered. A data tier needs an
   output path (or a stdout contract) added to the runner first.
3. **Output format is fragile.** A shortcut's result formatting is set by the
   shortcut, not by this repo, so parsing it is a versioned contract with
   something the user can edit. That wants a deliberate design (a fixed
   text/JSON envelope the shipped shortcuts always emit), not an ad-hoc parse.
4. **Names are not namespaced.** Invoking by name collides with whatever the user
   already has installed; a shipped shortcut would need a distinctive prefix and
   a stage-time existence check with a clear "import this first" error.

## Prerequisites if it's ever picked up

- Extend `run_shortcut` (or add a sibling) with output capture — the same change
  benefits every shortcut-backed capability, not just Maps.
- Decide the shipped-shortcut distribution and import-verification story once,
  server-wide.
- Keep the GUI operations. They stay the right answer for "take me there" and
  for anything the user wants to *look at*; the data tier would complement them,
  not replace them.

## Explicitly still out of scope

- **A third-party directions/geocoding API client.** Out of scope for a server
  whose job is bridging native macOS, on the same grounds that tabled the Stocks
  capability.
- **A compiled Swift/MapKit helper.** It would give the best data with no user
  setup, but it breaks the zero-dependency Go build (needs `swiftc`, including in
  CI) and requires widening `policy.allowedBinDirs` beyond the four trusted
  system directories to admit a repo-shipped binary. That is a trust-model
  change, and it should be decided on its own merits rather than smuggled in as
  a Maps feature.
