**issue**

Calendar/Reminders mutations are reversible (the whole point of making delete
"reversible" rather than "irreversible" like `send_mail`), but the reversal is a
*compensating* AppleScript action built from state captured at stage time, not a
true storage-level rollback. That leaves three known fidelity gaps a user should
understand:

1. **Re-created items get a NEW internal identifier.** `delete_event` /
   `delete_reminder` undo re-creates the item from its captured summary, times,
   location, notes (and, for reminders, due shape + completed flag). The new
   event/reminder is functionally the same but has a fresh `uid`/`id`. Anything
   that referenced the old identifier (a previous `query_events` result the
   model is still holding) will no longer resolve after an undo.

2. **`add_*` undo and pre-existing duplicates.** Because a just-created item's
   identifier isn't known until it exists, `add_event` / `add_reminder` undo
   deletes by natural key — calendar + summary + exact start date for events,
   list + name for reminders — and removes only the FIRST match. If an identical
   item already existed, undo could delete that one instead of the one just
   added. Deliberately deleting only the first match (never all matches) bounds
   the blast radius to a single item.

3. **Minute-precision and unmodelled fields.** Captured times round-trip at
   minute precision (seconds are dropped), and fields the v1 schema does not
   model — recurrence rules, attendees/invitees, alarms, URLs, all-day event
   flags — are not captured and therefore not restored by an undo. Undo restores
   the fields these operations actually manage, not a byte-exact copy.

These are acceptable for the v1 scope (the common case is undoing an action
moments after it happened, on simple events/reminders the server itself
created). A higher-fidelity approach (e.g. storing the item via an embedded
hidden marker so undo can match it exactly, or capturing recurrence/attendees)
is a documented future option, not built now.

**fixed**

Not a defect — recorded as a known limitation. The caveats are surfaced to the
user in each operation's staged preview ("the re-created event will have a new
internal uid") and in the README's `application-calendar` section.
