**note**

Unit 14 of the capability-expansion roadmap: a new `application-contacts` tool
with two operations — `get_contact` (read-only) and `create_contact` (staged,
reversible) — that let the assistant read a person's full address-book card and
add a new one. This note records the design choices.

## Why a new tool, and how it relates to application-phone

A new capability `category` in the registry is projected as a new domain tool by
the server (`internal/server/tools.go`), so `application-contacts` appears
alongside `application-phone`, `application-mail`, etc. — no server code change,
just the `contacts.json` manifest. Contacts already had a foothold in the
codebase: `application-phone/find_contact` (`builtins_phone.go`) searches
Contacts but returns **numbers only**, because its job is to resolve a name into
a number to dial. `get_contact` deliberately leaves that numbers-only contract
untouched and instead returns the FULL card (phones, emails, postal addresses,
birthday, organization) for the "show me Jane's details" ask. Both drive
Contacts through the same hardened `osascript` seam, and the read reuses
`phoneScriptError` — the Contacts Automation permission surface is identical, so
there is one error-mapping helper, not two.

## get_contact — typed field rows

Contacts exposes card data only through its scripting dictionary, so `get_contact`
is a FIXED AppleScript program plus Go-side parsing, an in-process builtin through
`runOsascript`. A card has a **variable** number of phones, emails, and addresses,
which does not fit the one-wide-row-per-record shape the other reads use. Instead
the script emits one typed row per field:

    personIndex \t fieldType \t label \t value

where `fieldType` is `name`/`organization`/`birthday`/`phone`/`email`/`address`.
This keeps the output a simple one-record-per-line stream; the Go side
(`parseContactFields` → `renderContactCards`) groups rows by `personIndex` to
rebuild each card. Every value is `_clean`-flattened so a field containing a tab
or newline cannot corrupt the row contract, `missing value` coerces to empty via
`_str`, the match count is capped INSIDE the script (like `find_contact`'s
`maxPeople`) so a broad query cannot flood the subprocess, and the rendered output
is bounded by the shared `compactOutput` budget. A small `_addr` AppleScript
handler joins the non-empty components of a postal address into one line (kept out
of the shared `asDateHelpers` because only this capability needs it); the birthday
is emitted via the existing `_fmt` handler and trimmed to just the date in Go.

## create_contact — the stage-time id problem, solved with a marker

A mutator resolves BOTH its forward and inverse at stage time (see `mutate.go`),
but a brand-new contact has no stable id until commit, so the inverse cannot name
the person by id. `create_contact` solves this the way the roadmap prescribes:
the forward writes a **unique, crypto-random marker** (`mcp-created-contact-<hex>`,
from `crypto/rand` — the same generator used by `internal/transaction` and
`messages_sandbox`) into the new person's `note` field, and the inverse deletes
the person whose note EQUALS that marker. Because the marker is unique, the
compensating delete can only ever match the one card this operation created — no
pre-existing contact can collide with it (contrast `create_note`, whose
title-based inverse can match a same-title note). That is why the operation is
classified **compensatable / medium / ST** (staged behind the `execute` token,
inverse is a targeted delete rather than a byte-exact restore of prior state).

## Guardrails

- Every model value crosses into AppleScript as `--`-terminated `on run argv`
  data (`osascriptCommand`/`runOsascript`), so a value beginning with `-` is
  inert. `get_contact` carries a `reviewedFreeTextBuiltins` entry (it is a
  free-text builtin) pointing at `TestGetContact_HostileNameLandsAsData`;
  `create_contact` is a mutator (covered structurally by the same seam) and ships
  `TestStageCreateContact_HostileFieldLandsAsData`.
- On top of the structural guard, `create_contact` validates field SHAPE: at
  least one of `first_name`/`last_name`/`organization` must be present (no
  entirely-blank card), a phone must look like a phone number (digits, optional
  leading `+`, spaces/dashes/parens/dots — no letters), an email must be
  plausible (`plausibleEmail`), and no field may contain a control character
  (which would corrupt the one-line staging preview).
- Empty optional fields are left UNSET on the created card rather than written as
  blanks, so a contact created with only a first name does not carry an empty
  organization or a blank phone.

## Non-goal for v1: editing existing contacts

`create_contact` only ADDS a card; it never edits an existing one. Editing would
need a stable way to name the target and a per-field prior-state probe for the
inverse — meaningful scope that is not in this unit. Deleting an arbitrary contact
is likewise out (only the self-created card is removed, via undo), matching the
project's stance of keeping high-blast-radius destructive verbs out.

## Tests & evals

Pure-Go tests only — no test launches `osascript` or touches the real address
book (see the Contacts safety note in `docs/TESTS.md`). Routing is checked by the
CI-safe selection cases `get_contact_routing` and `create_contact_routing`
(`domain_selection.json`; the create case stages with `forbid_tools: ["execute"]`,
which never touches Contacts). Live behavior is manual: `m_contacts_get_card` and
the self-cleaning `m_contacts_create_then_undo` (`manual_smoke.json`), both using
the fake `Jane Doe` / `jane@example.com` / `+15555550123` placeholders — no real
PII in any committed case.

## Stale installed binary

As with the recent units, the MCP server binary installed in the live client is
older than this change, so in-session `/runevals` cannot route the new
`application-contacts` operations yet. Wiring is verified instead by the unit
tests, `TestValidateBuilders` (every manifest builder has an implementation), the
injection-sweep coverage gate, and the server tool-surface integration test
(now asserting 21 tools). `bin/macos-darwin-mcp` was rebuilt for the next session.
