# Project Summary: Comprehensive macOS Automation MCP Server

## 1. System Mission & Strategic Vision
The objective of this project is to build an unconstrained, production-grade Model Context Protocol (MCP) server that safely exposes the native macOS terminal and tool ecosystem to AI clients using natural language. 

Rather than executing open-ended, string-concatenated shell commands (which are vulnerable to injection and runtime syntax errors), this server implements a robust, generic, tokenized execution wrapper built on top of the official Model Context Protocol Go SDK.

## 2. Platform Compatibility & Architectural Bounds
To ensure seamless operation across historical Apple Silicon models and modern Mac environments, the server adheres to the following platform constraints:

- **Minimum Platform Support**: macOS 11.0 Big Sur (Darwin 20.1.0). This baseline guarantees universal availability of the native `-json` output flags across primary system sub-profilers and provides a clean foundation for Apple Silicon execution loops.
- **Hardware Target Architecture**: Universal 2 Binary Target compilation. The binary build must generate native optimization tracks for both Apple Silicon (`arm64`) and Intel (`amd64`) platforms.
- **Data Exchange Format**: Standard JSON structures. Coding agents are forbidden from writing brittle text-scraping regex configurations (e.g., using `grep`, `sed`, or `awk`) where native machine-readable outputs (`-json` flags or `.plist` conversions via `plutil`) can be parsed into native Go data structures instead.

## 3. High-Level Execution Topology
The server operates over a standard input/output (`stdio`) transport stream layer as a stateful, transactional worker process.
