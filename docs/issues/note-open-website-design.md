**note**

`open_website` (in the `application` domain, `internal/engine/mutate_apps.go`)
opens a web address in a browser — the third member of the "open …" family
alongside `open_application` (installed apps) and `open_file` (files on disk).
Examples: "open YouTube", "open CNN.com", "open YouTube on Chrome".

Design decisions:

- **The model routes; the server validates.** This codebase exposes operations
  as data and lets the model pick which one to call. So the "is YouTube an app, a
  website, or a file?" judgment stays with the model — it already knows YouTube is
  a website and resolves it to `youtube.com`. The server does NOT try to be a smart
  dispatcher that guesses; it ships three explicit operations, each with a summary
  that tells the model when to reach for it, and validates hard. The
  `open_application` summary was also enriched to say it launches *installed* apps
  and to point web addresses at `open_website` — a routing nudge in the prompt, not
  server-side fallback logic.

- **Scheme is constrained to http/https.** `open` will launch whatever scheme it
  is handed (`file://` reads a local file, `tel:` places a call, a custom scheme
  triggers its registered handler), so a model-supplied address is never passed
  through verbatim. `normalizeWebsiteURL` upgrades a bare domain (`youtube.com`) to
  `https://`, and rejects every non-web scheme. This mirrors the "the scheme is
  chosen by our code, never by the model" discipline already used by `call`
  (`mutate_phone.go`) and `open_settings` (`mutate_system.go`). A subtlety worth
  recording: a scheme-only value with no `://` (e.g. `tel:911`) had to be rejected
  *before* the bare-domain default, otherwise it would have been silently turned
  into the valid-but-wrong `https://tel:911`. `url.Parse` also misreads a bare
  `host:port` (`example.com:8080`) as a scheme, so a "scheme" containing `.` is
  treated as a host:port (a bare domain), not a real scheme.

- **Staged, no undo.** Unlike `open_application` (auto-commit), `open_website` is
  staged behind the execute confirmation gate (the user chose this over
  auto-commit). It offers no undo: there is no reliable way to close exactly the
  tab/window that opened. The forward command places the URL after a `--`
  terminator (`open -- <url>`, or `open -a <browser> -- <url>`) so it can never be
  read as an option.

- **No hardcoded site dictionary.** The server does not map "YouTube" →
  "youtube.com"; that resolution is the model's job. Baking a site list into the
  server would be brittle and is unnecessary given the model already knows them.
