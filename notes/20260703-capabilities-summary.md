I've got the full picture — here's the capability breakdown for this MCP server:

**19 domains, 131 operations**, exposed as a small set of fixed MCP tools (e.g. `filesystem`, `application-mail`, `system`, etc.) each fronting a manifest-driven registry of operations:

| Category | Tool name | What it covers |
|---|---|---|
| 📁 Files & disk | `filesystem` | find, search (Spotlight), list, measure, create/move/copy/delete (trash), append, zip/unzip |
| ✉️ Mail | `application-mail` | list/read/search inbox, compose & send |
| 📅 Calendar | `application-calendar` | view/create/move/cancel events |
| ✅ Reminders | `application-reminders` | list, create, complete |
| 💬 Messages | `application-messages` | read/search conversations, send iMessage (+ attachments) |
| 📞 Phone | `application-phone` | find contact, place calls (cellular/FaceTime audio/video) |
| 📝 Notes | `application-notes` | search/read/create/append |
| 📷 Photos | `application-photos` | search/export, favorite/rename/keyword, albums (no delete — Photos automation can't) |
| 👤 Contacts | `application-contacts` | read full card, add new contact |
| 🎵 Music | `application-music` | now-playing, pause/skip/back |
| 🚀 Apps & windows | `application` | list installed/open, launch/focus/quit, open files/URLs, App Store lookup, window move/resize/minimize |
| 🧭 Safari | `application-safari` | read open tabs / front tab (read-only) |
| 📸 Screenshots | `screenshot` | full screen, region, or specific app window |
| 📋 Clipboard | `clipboard` | read/write text |
| 🖨️ Printers | `printer` | list printers/queue, print, test page |
| ⚙️ System | `system` | Wi-Fi/Bluetooth/battery/hardware info, deep-links into System Settings panes, notifications/TTS, display sleep |
| 🌐 Network | `network` | IP/DNS/gateway, ping, listening ports, connectivity diagnosis (read-only) |
| 📊 Process/resources | `process` | CPU/mem/GPU load, per-PID detail, launchd agents, graceful quit/terminate (no force-kill) |
| 🎛️ Preferences | `preferences` | curated Finder/Dock/accessibility/appearance toggles |

Plus cross-cutting infrastructure tools: `execute` (commits a staged mutation), `undo` (reverses a completed one), and `pipeline` (composes multiple read-only steps server-side).

Design invariant: reads run immediately; anything that mutates state goes through a stage → confirm → execute flow, and reversible ones hand back an undo token (per `docs/architecture.md` and the capability-engine spec in `docs/specs/`).
