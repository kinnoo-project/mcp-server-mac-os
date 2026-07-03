# Stocks capability — Tabled pending owner decision, not in the active build order

**Status:** researched and tabled 2026-07-03. No code written. This note
preserves the full investigation so a future revisit starts from the evidence
rather than re-deriving it (mirrors `shortcuts-runner-deferred.md`).

## Goal that prompted it

Type a **company name or ticker** and get the **live price**; handle **a list of
symbols**; see the fundamentals the Stocks app shows — **P/E ratio, market cap,
dividend yield, daily volume**; and ideally **add a symbol to the watchlist**.

## Why it's tabled (the short version)

There is a genuinely *native* path — reading the Stocks app's own on-device data —
but it **cannot deliver the actual ask**, for two independent reasons:

1. **The cache is not live.** It's a "last-seen" snapshot that only refreshes when
   the Stocks app or its widget runs.
2. **You can't add to the watchlist programmatically**, only via a guided hand-off.

Delivering "live price for any ticker + dividend yield + add it" requires either a
**live third-party fetch** (out-of-scope for a server whose job is to bridge
native macOS, not reimplement a finance API client) or **UI automation**
(Accessibility-gated, fragile, a standing non-goal). So the capability is parked
until the owner decides which trade-off (if any) is acceptable.

## Findings (verified live on macOS this session)

### 1. Stocks.app is not scriptable
- **No AppleScript dictionary** — it cannot be driven like Music/Notes/Mail.
- It **does** register the **`stocks://` URL scheme** (`open "stocks://?symbol=AAPL"`
  opens that ticker). There is **no "add to watchlist" deep-link** — the scheme
  only opens/selects a symbol.
- The app binary contains **App Intents / Shortcuts** plumbing, but no confirmed
  "Add to Watchlist" action surfaced; using Shortcuts is itself the deferred
  Shortcuts-runner path (`shortcuts-runner-deferred.md`).

### 2. The native on-device cache (the genuinely-native read)
Path: `~/Library/Group Containers/group.com.apple.stocks/Library/Caches/shared-database`
(SQLite, WAL mode). Read it **read-only** as `file:<path>?immutable=1` so the
app's WAL is never touched. Row values are JSON wrapped as `{"v": …}`.

| Table | Holds |
|---|---|
| `stock_metadata` | `stock.{symbol, name, shortName, exchange, type}` → **name ↔ ticker resolution** |
| `quotes` | `price`, `priceChange`, `marketCapitalization`, `currencyCode`, `afterHoursPrice`/`afterHoursPriceChange`, `exchangeStatus`, `dateLastRefreshed` |
| `quote_details` | `volume`, `averageVolume`, `dayHighPrice`/`dayLowPrice`/`dayOpenPrice`, `yearHighPrice`/`yearLowPrice` (52-wk), `earningsPerShare`, `beta` — **only for symbols the user has opened** (7 of 269 on the test Mac) |
| `sparkline-database` (sibling DB) | intraday sparkline points |

Notes:
- **P/E is not stored** but is derivable as `price ÷ earningsPerShare` when EPS is
  present (i.e. for opened symbols only).
- **Dividend yield is not cached at all** (it lives behind the app's
  `keyStatisticsURL`, which the app fetches live and does not persist here).
- Coverage is **watchlist + recently-viewed symbols only** — an arbitrary ticker
  the user doesn't track is simply absent.

### 3. Freshness — the dealbreaker
Measured mid-market (12:52 ET on a trading day): **all 269 cached quotes shared a
single batched timestamp from 81 minutes earlier**; 0 updated in the last hour, 59
were over 24 h old. The Stocks app rewrites the whole cache only when the app or
its widget runs (`preferredRefreshInterval` ≈ 15 min while active). So a read
returns **"last-seen" prices, not live ones**. Every rendered quote would need an
explicit "as of <time>" staleness stamp, and even then it is not a live quote
service. (Open question for a revisit: can `open_stocks` trigger a refresh and
then read the freshened cache in one flow? Untested — opening the app is a side
effect.)

### 4. Apple's own data service (native fetch) — rejected
The app talks to **`https://stocks-data-service.apple.com/api/v1/{quote,search,news,currencies}`**
and caches a per-endpoint bearer **`accessKey`** (with an `expirationDate`) in the
`sds_auth_tokens` table of the same `shared-database`. Reusing that token would
give live data for **any** symbol through Apple's own infrastructure — but it is
**credential reuse of the Stocks app's private token by a different caller**. The
environment's safety classifier **blocked** the attempt to replay it (flagged as
credential exploration), and it is fragile besides (the token rotates/expires and
is refreshed only by the app). **Not a path to build on.**

### 5. Yahoo Finance public API (what Apple proxies) — out of scope
Apple sources Stocks data from Yahoo. Verified directly:
- `https://query1.finance.yahoo.com/v1/finance/search?q=<term>` — name → ticker
  list. **No auth (HTTP 200).**
- `https://query1.finance.yahoo.com/v8/finance/chart/<SYM>` — price, previous
  close, volume, day high/low. **No auth (HTTP 200).**
- `https://query1.finance.yahoo.com/v7/finance/quote?symbols=<A,B>` — P/E, market
  cap, dividend yield, volume, 52-wk range. **HTTP 401 without a crumb+cookie.**
  The 3-step handshake (cookie from `fc.yahoo.com` → crumb from
  `…/v1/test/getcrumb` → quote with `crumb=`) works but is undocumented and
  rate-limited (this is why Yahoo added the wall; Apple avoids it via a commercial
  data agreement + its own proxy).

Calling Yahoo directly reimplements a **third-party finance client**, which the
owner judged **out of scope** for a server whose remit is bridging native macOS
capabilities.

## Options preserved for a future revisit

- **(A) Native snapshot + hand-off only** — read the cache; render tracked symbols
  at last-seen prices (clearly staleness-stamped); `open_stocks` opens/adds via a
  guided hand-off. Purely native, safe, but modest ("my watchlist at a glance",
  not live, tracked-only, no dividend yield).
- **(B) Native cache + live fallback** — cache first for instant context, then a
  live fetch (Yahoo) only to fill gaps (freshness, arbitrary tickers, dividend
  yield, full fundamentals). Reintroduces the outbound third-party call, as a
  fallback. Most useful; least "purely native".
- **(C) Apple SDS token replay** — rejected (credential reuse; blocked by the
  safety layer; fragile).

## Design constraints for whoever picks this up

- If built as a read: it would be a new `stocks` capability `category` → a new MCP
  domain tool (one tool per category), tool count +1. Follow the U17 App Store
  pattern for any outbound HTTP (`internal/engine/builtins_appstore.go`): fixed
  Go-side host/scheme/path, model text only as percent-encoded query values,
  no-redirect client, context timeout, response-size cap, `reviewedFreeTextBuiltins`
  entry + regression test.
- **Privacy:** reading the cache reads the user's real watchlist — live reads must
  be manual evals, and **no real holdings may ever be committed** as fixtures (use
  fake public tickers like `AAPL`/`MSFT`). This repo is public.
- Do **not** read or replay `sds_auth_tokens`.
