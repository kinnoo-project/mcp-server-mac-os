# Project Summary: Comprehensive macOS Automation MCP Server

## 1. System Mission & Strategic Vision
The objective of this project is to make an MCP client connected to this server the **command center for the majority of daily tasks on macOS**. Through natural language, a user should be able to drive the everyday surface of the Mac from a single conversational interface: work across the filesystem and the built-in apps (Mail, Calendar, Reminders, Contacts/Phone, Messages, Notes, Photos, Safari, Music, Maps), find and launch applications and drive their windows, manage printers and print, capture the screen, diagnose the network, inspect running processes and resource use, check backups and disks, verify app trust, run their own Shortcuts, and read or adjust system state — Wi-Fi, Bluetooth, battery, appearance, accessibility, and other preferences.

The surface as it stands is **174 operations across 23 domains** (see `docs/architecture.md` for the full catalog). The guiding boundary is *user parity*: a capability belongs here if an ordinary user could perform it themselves on their own machine, even when that needs a one-time permission grant (Full Disk Access, Automation, Screen Recording). Anything that **strictly requires administrator or root privileges is out of scope** — this is deliberately not a root-level sysadmin server.

Where an action genuinely requires administrator rights that cannot be obtained over the server's non-interactive transport (for example connecting or re-enabling a printer, or toggling Bluetooth/Wi-Fi/Low-Power), the server does not fail silently: it performs the readable parts directly and **hands the user off to the exact System Settings pane** to finish the privileged step with a click.

Rather than executing open-ended, string-concatenated shell commands (which are vulnerable to injection and runtime syntax errors), this server implements a robust, generic, tokenized execution wrapper built on top of the official Model Context Protocol Go SDK. Operations are described as **data** (JSON capability manifests) executed by a fixed engine, so new operations are added as manifest entries rather than bespoke per-operation code.

## 2. Platform Compatibility & Architectural Bounds
To ensure seamless operation across historical Apple Silicon models and modern Mac environments, the server adheres to the following platform constraints:

- **Minimum Platform Support**: macOS 13.0 Ventura (Darwin 22). Older releases are **not** supported. Ventura re-architected System Settings and its `x-apple.systempreferences:` deep-link identifiers, so the server targets only the modern pane identifiers (no per-version detection). This baseline also guarantees availability of the native `-json` output flags across primary system sub-profilers and provides a clean foundation for Apple Silicon execution loops.
- **Hardware Target Architecture**: Universal 2 Binary Target compilation. The binary build must generate native optimization tracks for both Apple Silicon (`arm64`) and Intel (`amd64`) platforms.
- **Data Exchange Format**: Standard JSON structures. Coding agents are forbidden from writing brittle text-scraping regex configurations (e.g., using `grep`, `sed`, or `awk`) where native machine-readable outputs (`-json` flags or `.plist` conversions via `plutil`) can be parsed into native Go data structures instead.

## 3. High-Level Execution Topology
The server operates over a standard input/output (`stdio`) transport stream layer as a stateful, transactional worker process.

The model does **not** see one tool per operation. Each capability *category* is projected as a single **domain tool** carrying the full menu of its operations, so the tool surface is **26 tools (23 domains + `execute`, `undo`, `pipeline`) fronting 174 operations** — and adding an operation is a manifest entry that lengthens one description rather than growing the surface. Reads run immediately; the 70 mutating operations resolve their forward *and* inverse commands at stage time and park behind a one-shot token, so what executes is exactly what was previewed. Subprocess output is compacted to a 32 KB head/tail budget and every command carries a two-minute ceiling, so neither a verbose utility nor a runaway scan can saturate the model's context or the server.
