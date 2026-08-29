**issue**
The mac-os MCP server's `filesystem` `extract` operation doesn't handle plain `.tar` archives — it only supports `.zip`, `.tar.gz`, and `.tgz`. Attempting to extract a `.tar` file fails with: `extract: archive "..." must end in one of .zip, .tar.gz, .tgz`.

Worked around by extracting directly with the `tar` CLI via shell instead of the MCP tool.

**fixed**
Fixed 2026-08-29. `extract` now accepts a plain `.tar`; `compress` still only creates
compressed formats. Full writeup: `docs/issues/bug-extract-rejects-plain-tar.md`.
