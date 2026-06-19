**issue**

The `application-phone` `call` capability cannot, and does not try to,
automatically pick the "right" calling method. Two facts about macOS make this
impossible from a server:

1. **There is no API to test FaceTime reachability.** Whether a given number is
   an Apple user reachable over FaceTime can only be discovered by actually
   attempting the call — there is no lookup. So a `facetime_audio` /
   `facetime_video` call to a non-Apple number simply fails inside the FaceTime
   UI; it cannot be detected beforehand.

2. **There is no reliable signal for iPhone pairing/Continuity.** A `cellular`
   (`tel:`) call is routed through a paired iPhone via Continuity. If no iPhone
   is paired (or Wi-Fi Calling is off), the `tel:` handoff falls back or fails —
   again, not something the server can know in advance.

Because of this, **method selection is guidance the model applies, not server
logic.** The `call` operation's description encodes the preferred policy
(default `cellular`; fall back to `facetime_audio` when cellular isn't
available or the contact is an Apple user; use `facetime_video` only on an
explicit video/"FaceTime" request), and the `method` parameter defaults to
`cellular`. When a chosen method fails in the system UI, the remedy is to
re-issue `call` with a different `method`.

A second, smaller limitation: `call` dials phone numbers only. FaceTime can also
address an Apple ID **email**, which the v1 schema does not accept.

**fixed**

Not a defect — recorded as a known limitation and surfaced to the user in the
README's `application-phone` section. Apple-ID-email addressing is a documented
fast-follow, not built now.
