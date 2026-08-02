**note**

Design record for the `application-maps` capability (v1, GUI handoff only).

## The constraint that shaped everything

Maps.app has **no AppleScript dictionary and no first-party CLI**. There is no
supported way to ask it a question and read the answer. The only native control
surface is its `maps://` URL scheme, which *opens a window* showing a result.

So v1 cannot answer "how far is the drive to San Francisco?" with a number. It
can only put the route on screen. Every operation summary and every preview says
this explicitly, and `TestStageMaps_PreviewsStateTheHandoff` enforces it, because
the failure mode that actually costs the user something is the model *implying*
it read a distance it never received.

Two alternatives were considered and rejected for v1:

- **A Swift/MapKit helper** (`MKDirections`, `MKLocalSearch`, `CLGeocoder`) would
  give real data with no user setup. It was rejected because it breaks two
  standing invariants at once: the zero-dependency Go build (it needs `swiftc`,
  including in CI) and `policy.allowedBinDirs`, which admits only `/bin`,
  `/sbin`, `/usr/bin`, `/usr/sbin` — a repo-shipped binary cannot live there
  without widening the trust boundary.
- **Repo-shipped Apple Shortcuts** run through the existing `shortcuts` runner
  would give real data with no build change. Deferred by owner decision: this
  version of the server intentionally ships no shortcuts for the user to import.
  Written up in `docs/ideas/maps-data-tier-deferred.md`.

## Operations, and why three

- `directions` — a route, with an optional origin (omitted means "from this
  Mac") and a travel mode. It is also how *distance* and *duration* questions are
  answered, since both are drawn on the route.
- `search_locations` — a category or business search. Maps orders results by
  distance, which is what answers "what's the closest X?".
- `show_location` — pins one specific address. Kept separate from
  `search_locations` because "show me where 1 Infinite Loop is" is a different
  intent from "find me coffee", and a distinct operation name gives the model an
  unambiguous target instead of a query it has to phrase carefully.

Map type (`t=m/k/r`, standard/satellite/transit overlay) was deliberately
omitted: it is cosmetic, it is one click inside Maps, and it would add enum
surface for no capability gain.

## Lane: auto-commit, low risk, irreversible

Opening a Maps window needs no permission grant, changes no persistent state,
and has nothing to undo (hence `Inverse: nil`). That puts it in the same lane as
`open_settings` and `open_app_store_page`, both of which also assemble their URL
Go-side and run immediately.

It is deliberately **not** in `open_website`'s staged lane. The distinction is
structural, not a matter of taste: `open_website` takes a whole URL from the
model, so `open` could be pointed at any scheme it registers. Here the model
supplies only a *destination*, *search phrase*, or *address*; the scheme
(`maps://`) and every query key (`daddr`, `saddr`, `dirflg`, `q`, `address`) are
Go constants, and the travel mode passes through a fixed table. Free text is
percent-encoded by `url.Values` before it joins the URL, so a hostile value like
`-e` or `; rm -rf /` becomes an inert encoded query value — it cannot split into
a second argument, become a flag, or change the scheme being dispatched. The
finished URL still rides after a `--` terminator.

Staging these would have cost real usability (a confirmation round-trip on
"directions to the airport") to guard against nothing.

## `open -- <url>`, not `open -a Maps -- <url>`

No existing scheme-URL mutator pins a handler app — `tel:`, `facetime:`, and
`macappstore:` all let Launch Services route the URL — and `maps://` is
OS-registered to Maps.app on every supported macOS. Pinning would add a failure
mode (a wrong or renamed app name) without adding a guarantee.

## The cycling caveat

Apple's published URL-scheme reference documents `dirflg` values `d`, `w`, and
`r`. Cycling (`c`) follows the same pattern and matches the cycling directions
Maps has shipped since Ventura, but it is **not** in the documented set.

Rather than drop the mode (the owner's use cases explicitly include "how far is
the bike ride…"), the cycling preview carries a caveat telling the user to check
the mode shown in the window. If a target macOS turns out to mishandle it, the
remediation is to **drop `cycling` from the manifest enum** — never to
substitute another mode Go-side, which would answer a bike question with a car
route and no visible sign of the swap. `m_maps_cycling_mode` is the manual smoke
case that re-checks this per release.

## Limitations worth knowing

- **No data comes back.** Distances, ETAs, and result lists exist only in the
  Maps window.
- **The server never learns the user's location.** "Near me" and an omitted
  origin are resolved by Maps.app through Location Services; nothing about the
  Mac's position passes through this code.
- **No radius filter.** "Within 5 miles" cannot be enforced — no results return,
  so nothing can be filtered. Proximity preference only shapes the words Maps
  searches for; Maps' own distance ordering does the rest.
- **No multi-stop routes, no auto-started turn-by-turn navigation, no ETA
  sharing.** The URL scheme does not expose them.
- **No coordinate-anchored search.** Without a geocoder an area name cannot
  become the `sll`/`ll` coordinates the scheme expects, so an area is folded into
  the search text instead (`"Philz Coffee near Pleasanton, CA"`).

## Composition that already works

`application-contacts/get_contact` returns a contact's postal address, which
feeds straight into `directions` or `show_location` as free text. No new code was
needed for "give me directions to Jane's place"; `m_maps_contact_address_composition`
covers it.
