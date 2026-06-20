# Project Summary: Comprehensive macOS Automation MCP Server

## 1. System Mission & Strategic Vision
The objective of this project is to make an MCP client connected to this server the **command center for the majority of daily tasks on macOS**. Through natural language, a user should be able to drive the everyday surface of the Mac — find and launch applications, manage printers (list them, check status, print a test page), read the state of Wi-Fi, Bluetooth, and battery, adjust accessibility and other preferences, and work across Mail, Calendar, Reminders, Contacts/Phone, Messages, and the filesystem — from a single conversational interface.

Where an action genuinely requires administrator rights that cannot be obtained over the server's non-interactive transport (for example connecting or re-enabling a printer, or toggling Bluetooth/Wi-Fi/Low-Power), the server does not fail silently: it performs the readable parts directly and **hands the user off to the exact System Settings pane** to finish the privileged step with a click.

Rather than executing open-ended, string-concatenated shell commands (which are vulnerable to injection and runtime syntax errors), this server implements a robust, generic, tokenized execution wrapper built on top of the official Model Context Protocol Go SDK. Operations are described as **data** (JSON capability manifests) executed by a fixed engine, so new operations are added as manifest entries rather than bespoke per-operation code.

## 2. Platform Compatibility & Architectural Bounds
To ensure seamless operation across historical Apple Silicon models and modern Mac environments, the server adheres to the following platform constraints:

- **Minimum Platform Support**: macOS 13.0 Ventura (Darwin 22). Older releases are **not** supported. Ventura re-architected System Settings and its `x-apple.systempreferences:` deep-link identifiers, so the server targets only the modern pane identifiers (no per-version detection). This baseline also guarantees availability of the native `-json` output flags across primary system sub-profilers and provides a clean foundation for Apple Silicon execution loops.
- **Hardware Target Architecture**: Universal 2 Binary Target compilation. The binary build must generate native optimization tracks for both Apple Silicon (`arm64`) and Intel (`amd64`) platforms.
- **Data Exchange Format**: Standard JSON structures. Coding agents are forbidden from writing brittle text-scraping regex configurations (e.g., using `grep`, `sed`, or `awk`) where native machine-readable outputs (`-json` flags or `.plist` conversions via `plutil`) can be parsed into native Go data structures instead.

## 3. High-Level Execution Topology
The server operates over a standard input/output (`stdio`) transport stream layer as a stateful, transactional worker process.
