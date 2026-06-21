# 🖥️ mcp-server-mac-os

> **Talk to your Mac.** A Model Context Protocol (MCP) server that turns
> plain-language requests into safe, native macOS actions — your files, Mail,
> Calendar, Reminders, Messages, Notes, Contacts & calls, apps, printers, and
> system settings — from any MCP-aware client like **Claude Code** or **Claude Desktop**.

[![Platform: macOS 13+](https://img.shields.io/badge/platform-macOS%2013%2B-black?logo=apple)](https://www.apple.com/macos/)
[![Go 1.26+](https://img.shields.io/badge/Go-1.26%2B-00ADD8?logo=go&logoColor=white)](https://go.dev/)
[![MCP](https://img.shields.io/badge/MCP-go--sdk%20v1.4.1-6E56CF)](https://modelcontextprotocol.io/)
[![License: AGPL-3.0](https://img.shields.io/badge/license-AGPL--3.0-blue.svg)](LICENSE)

Connect it once and your AI assistant becomes a genuine **macOS command center**.
Ask in ordinary language —

> *"What are my 10 biggest files?"* · *"Text Bob I'm running 10 minutes late."* ·
> *"Put a dentist appointment on Thursday at 2pm."* · *"Find my tax return and
> email it to my accountant."* · *"Open System Settings to Wi-Fi."* ·
> *"Turn on Reduce Motion."*

— and the model answers by calling **real, audited macOS tools**. Every action
that *reads* runs instantly; every action that *changes something* is previewed
and gated behind an explicit confirmation you approve. Nothing happens to your
Mac without you seeing exactly what will happen first.

---

## ✨ What you can do

This isn't a sandbox of toy commands — it's a practical, everyday assistant for
the Mac you already use. **12 domains, ~61 operations**, each invokable in plain
English. Read operations return immediately; **bold** ones change system state
and always ask first (see [Safe by design](#-safe-by-design)).

### 📁 Files & disk

Find things, measure things, and tidy up — without memorizing `find` flags or
`du` incantations.

- *"What are the 10 biggest files under my home directory?"*
- *"List every PNG, JPG, and HEIC under `~/Pictures`."*
- *"Which files in this project mention `TODO`?"*
- *"How big is my Downloads folder?"*
- *"How many lines are in `/var/log/system.log`?"*
- **"Create a folder called `drafts` in my Documents."** *(previewed; undoable)*

### ✉️ Mail

Search your inbox and draft outgoing mail — with the recipient and full body shown
verbatim before anything sends.

- *"Find emails mentioning invoice INV-4471."*
- **"Email Alice to say I'll be 10 minutes late."** *(previewed; **cannot** be undone)*
- **"Find my 2025 tax return and email it to my accountant."** *(locates the file, then attaches it)*

### 📅 Calendar & Reminders

Read your schedule and manage events and to-dos — every change reversible with a
single "undo."

- *"What's on my calendar this week?"*
- **"Put a dentist appointment on Thursday at 2pm."**
- **"Move my 3pm review to 4pm."** · **"Cancel Friday's standup."**
- *"What reminders are due this week?"*
- **"Remind me to call the bank on Monday."** · **"Mark the dry-cleaning reminder done."**

### 💬 Messages & 📞 Calls

Read recent conversations, send texts (with attachments), and place calls — by
contact name or number.

- *"Any new messages?"* · *"Show my recent texts with Alice."*
- **"Text Bob that I'm running late."** · **"Send this PDF to Alice on iMessage."**
- *"What's Mom's number?"*
- **"Call Mom."** · **"FaceTime Bob."** *(names exactly who will be dialed, and how)*

### 📝 Notes

Find and read your notes, and jot new ones down — changes previewed and undoable.

- *"What folders do I have in Notes?"* · *"Show my most recent notes."*
- *"Find my note about the wifi password."* · *"Read the note titled 'Packing list'."*
- **"Make a note titled 'Trip ideas' with these three places."** *(previewed; undo deletes it)*
- **"Add 'buy sunscreen' to my packing-list note."** *(previewed; undo restores the prior contents)*

### 🚀 Apps

See what's installed and what's open, and drive your apps.

- *"What apps are installed?"* · *"What's open right now?"*
- **"Open Notes."** · **"Bring Safari to the front."** *(run immediately)*
- **"Open Leah.png in Preview."** *(previewed first — and it warns if the app may not handle that file type)*
- **"Open this PDF."** *(no app named — opens in your default app for that type)*
- **"Quit Mail."** *(previewed first — unsaved work matters)*

### 📸 Screenshots

Give the assistant eyes on your screen — it captures the desktop to an image file
and hands back the path (plus size and dimensions) so it can look at what you see.

- *"Take a screenshot."* · *"Grab a screenshot of my second display as a JPG."*
- *"Screenshot my screen and save it to ~/Desktop/login.png."* *(say where, or it
  defaults to `~/Pictures/Screenshots`; it won't overwrite an existing file)*
- Needs the **Screen Recording** permission, and says so plainly if it isn't
  granted yet.

### 🖨️ Printers & ⚙️ System

Check hardware and network status, print, and jump straight to the right Settings
pane for anything that needs admin rights.

- *"What printers do I have, and are they on?"* · *"What's in the print queue?"*
- **"Print this PDF."** · **"Print a test page on the office laser."**
- *"Is Wi-Fi on, and what am I joined to?"* · *"Battery level? Is Low Power Mode on?"*
- *"Is Bluetooth on? What's connected, and what's paired?"*
- **"Open System Settings to Wi-Fi."** *(opens the pane for you to finish)*

### 🌐 Network & diagnostics

Answer everyday network questions and let the model diagnose connectivity issues
by composing these probes. All read-only — nothing here changes your network
configuration.

- *"What's my IP, router, and MAC address? How many devices fit on this network?"*
- *"What DNS servers am I using?"* · *"What other devices are on my network?"*
- *"Can you ping the router? Can you reach 8.8.8.8?"* · *"Does apple.com resolve?"*
- *"What ports am I listening on, and which apps own them?"*
- *"I can't reach the internet — can you diagnose it?"* *(checks the gateway, then a
  public IP, then DNS — and tells you where it breaks)*

> ℹ️ Turning Bluetooth on/off has no command line on macOS, so the model hands you
> off to System Settings for that — it can still tell you what's connected and paired.

### 📊 Processes & resources

See what's running and what it costs, find runaway or zombie processes, and stop a
misbehaving one *gracefully* — never a force-kill.

- *"What's eating my CPU / memory / battery right now?"* · *"Show the top memory hogs."*
- *"How loaded is my Mac overall?"* *(load average, per-core)* · *"How much RAM is free?"*
- *"How busy is the GPU?"* *(whole-device — per-process GPU isn't exposed without admin rights)*
- *"Tell me everything about PID 1234."* *(command, the binary responsible, parent,
  origin, start time, zombie state, whether launchd auto-starts it)*
- *"What did I install that starts automatically?"* *(launchd agents & daemons)*
- *"Quit Safari"* / *"stop that stuck process"* — a GUI app gets the normal **Quit**
  command (so it can prompt to save); a daemon gets a polite **SIGTERM**. Both are
  **staged for your confirmation** and never force-killed.

> ℹ️ Stopping a process is staged → you approve → it runs. We deliberately never send
> SIGKILL (force kill), which gives a program no chance to save or clean up.

### 🎛️ Preferences & accessibility

Flip well-known Finder, Dock, keyboard, and accessibility toggles — a curated set
of safe, reversible switches (no security-sensitive settings, ever).

- **"Show hidden files in Finder."** · **"Auto-hide the Dock."**
- **"Turn on Reduce Motion."** · **"Increase contrast."**
- **"Turn off smart quotes."** *(every one of these is undoable)*

> 💡 Need more than one step? Ask naturally — *"How many `.log` files are under
> `/var/log`?"* — and the model composes the right tools for you. The full
> capability catalog, with the exact tool each prompt maps to, is in
> **[the architecture reference](docs/architecture.md#capabilities)**.

---

## 🔒 Safe by design

Giving an AI control of your Mac should make you cautious. This server is built so
that trust is earned structurally, not just promised:

- **Read vs. change is a hard line.** Anything that inspects your system runs and
  returns immediately. Anything that *changes* it is **staged** first: the model
  receives a plain-language preview and a one-time token, and **nothing happens
  until a separate `execute` step** — the step your MCP client gates with its own
  "Allow this tool call?" prompt.
- **Undo built in.** Reversible changes hand back an `undo` token. Created a folder
  or flipped a setting by mistake? "Undo that." Irreversible actions (sending mail,
  placing a call, printing) say so up front and show you everything verbatim before
  you commit.
- **No shell, ever.** Native utilities are run with explicit, tokenized arguments —
  never a shell string — so there is nothing to inject into. Binaries must resolve
  under `/bin`, `/sbin`, `/usr/bin`, or `/usr/sbin`, blocking rogue substitutes.
- **Curated, not open-ended.** Settings changes are limited to a vetted allowlist
  of harmless toggles; AppleScript, `tel:`/`facetime:` URLs, and SQL queries are
  all built from fixed templates with your input bound strictly as *data*.
- **Your permissions, your prompts.** The server runs as you and inherits your
  macOS permissions. The first time it touches protected data (Mail, Contacts,
  Messages, Desktop/Documents/Downloads…), macOS prompts you to grant access —
  once.

The complete, line-by-line threat model is in
**[Why this server is safe to expose](docs/architecture.md#why-this-server-is-safe-to-expose)**.

---

## 🚀 Get started

### Requirements

- **macOS 13 Ventura or newer** (Apple Silicon or Intel)
- **[Go 1.26+](https://go.dev/dl/)** to build from source
- An MCP-aware client: **[Claude Code](https://claude.com/claude-code)** or
  **[Claude Desktop](https://claude.ai/download)**

### Build

```bash
go mod tidy
go build -o bin/macos-darwin-mcp ./cmd/macos-darwin-mcp
```

For a **Universal 2** binary that runs natively on both Apple Silicon and Intel:

```bash
GOOS=darwin GOARCH=arm64  go build -o bin/mcp-server-arm64 ./cmd/macos-darwin-mcp
GOOS=darwin GOARCH=amd64  go build -o bin/mcp-server-intel ./cmd/macos-darwin-mcp
lipo -create -output bin/macos-darwin-mcp bin/mcp-server-arm64 bin/mcp-server-intel
rm bin/mcp-server-arm64 bin/mcp-server-intel
```

### Connect it

**Claude Code:**

```bash
claude mcp add mac-os-fs -- /absolute/path/to/bin/macos-darwin-mcp
claude mcp list   # should show mac-os-fs connected
```

**Claude Desktop** — edit
`~/Library/Application Support/Claude/claude_desktop_config.json`:

```json
{
  "mcpServers": {
    "mac-os-fs": {
      "command": "/absolute/path/to/bin/macos-darwin-mcp"
    }
  }
}
```

Then **restart your client** (Claude Code session, or quit & relaunch Claude
Desktop) — MCP clients load the tool list once at startup and don't hot-reload, so
always restart after (re)building.

You'll now have the 14 domain tools (`filesystem`, `preferences`, `application`,
`application-mail`/`-calendar`/`-reminders`/`-phone`/`-messages`/`-notes`,
`printer`, `system`, `network`, `process`, `screenshot`) plus the shared `execute`,
`undo`, and `pipeline` tools. Try one of the prompts from
[What you can do](#-what-you-can-do) and watch the model pick the right tool.

> **Note on permissions:** reading Messages needs **Full Disk Access**, taking
> screenshots needs **Screen Recording**, and automating
> Mail/Calendar/Reminders/Contacts/Messages/Notes needs a one-time **Automation**
> grant — all prompted by macOS the first time, under *System Settings → Privacy &
> Security*. Granting once is enough.

---

## 🤝 Contributing

Contributions are very welcome — new capabilities, better docs, eval cases, bug
reports, and design discussion alike. The whole point of the architecture is that
**most new operations are a JSON manifest entry, not new Go code**, so adding to
your Mac's command center is genuinely approachable.

Start with **[CONTRIBUTING.md](CONTRIBUTING.md)** for the development setup, the
build/test/lint pipeline, how to add a capability, and the project's coding and
PR conventions.

---

## 🗺️ Roadmap

The read-only foundation, the 10-domain tool surface, the
stage → execute → undo mutation gate, and read-only composition (`pipeline`) are
all in place, with mutation proved across every undo shape the design anticipated
(fixed inverse, prior-state-dependent inverse, and genuinely irreversible).
What's next:

- **Eval breadth** — widen model coverage and add cases as new domains ship (the
  harness runs 18 cases against `claude-sonnet-4-6` today).
- **More capabilities** — more curated `preferences` settings, more `application-*`
  depth, and mutating capabilities in new domains (e.g. networking).
- **Irreversible *file* operations** — a Trash / `/tmp/mcp-fallback/` recovery path
  so destructive file ops get a practical undo even without a true inverse.
- **Multi-step *mutation* plans** — stage and commit several changes with a
  best-effort + report failure policy.
- **Force mode** — an explicit opt-in to skip the confirmation step for low-risk
  reversible operations (never for irreversible ones).

See [`docs/`](docs/) for the design notes and approved plans.

---

## 📐 Architecture & docs

This README is the tour; the engineering details live alongside the code:

- **[docs/architecture.md](docs/architecture.md)** — system design and request
  flow diagrams, the full capability catalog, the mutation model, the complete
  safety/threat model, the codebase map, and how to develop/test/eval.
- **[docs/TESTS.md](docs/TESTS.md)** — what the test suite verifies (and what it
  deliberately doesn't).
- **[docs/ideas/](docs/ideas/)**, **[docs/specs/](docs/specs/)**,
  **[docs/issues/](docs/issues/)** — design rationale, specs, and notes/known
  limitations.
- **[CLAUDE.md](CLAUDE.md)** and **[.claude/rules/](.claude/rules/)** — the
  non-negotiable engineering axioms every change follows.

---

## 📄 License

Licensed under the **GNU Affero General Public License v3.0** — see
[LICENSE](LICENSE). Contributions are accepted under the same license.
