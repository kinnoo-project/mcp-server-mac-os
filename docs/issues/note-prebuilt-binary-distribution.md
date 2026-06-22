**note**

Distributing a prebuilt (ideally signed) binary via GitHub Releases so users don't
have to clone the repo, install Go, and build from source. Decision: Jerry will
first sign up for the Apple Developer Program ($99/yr), after which we'll add a
signed/notarized binary as a release candidate to the CI workflow. Captured below
verbatim are the originating prompt and the full response that surveyed the
approach and its limitations.

---

**Prompt (verbatim):**

Instead of a user having to clone the repo, install Go and build the binary themselves, is there a way I can upload the binary itself to a repo somewhere, and allow users to download the binary and add / start the MCP server directly from the binary? Are there any limitations to this approach (e.g., compatibility issues)?

---

**Response (verbatim):**

Yes — this is a solved problem, and the standard answer is **GitHub Releases**. You attach the prebuilt binary as a release asset; users download it, make it executable, and point `claude mcp add` straight at it. No clone, no Go, no `lipo`. But there's one macOS-specific wrinkle (Gatekeeper) that's the real story here, so let me lay out the whole picture.

## The basic approach

You already produce a **Universal 2** binary (the `lipo` step in CLAUDE.md), which is the right artifact — one file that runs natively on both Apple Silicon and Intel. Upload that to a tagged GitHub Release, and the user side becomes:

```bash
curl -L -o macos-darwin-mcp \
  https://github.com/<you>/mcp-server-mac-os/releases/download/v0.1.0/macos-darwin-mcp
chmod +x macos-darwin-mcp
claude mcp add mac-os-mcp -- "$(pwd)/macos-darwin-mcp"
```

This can be fully automated with a GitHub Actions workflow that builds both slices, runs `lipo`, and uploads the result on every `v*` tag.

## The limitations — in order of how much they'll actually bite

**1. Gatekeeper / code signing — this is the big one.** Any binary downloaded from the internet gets a `com.apple.quarantine` attribute. When the user (or Claude Code) first launches an **unsigned, un-notarized** executable, macOS blocks it: *"cannot be opened because Apple cannot check it for malicious software."* There are three ways to handle this, in increasing order of polish:

- **Tell users to strip quarantine manually** — free, but ugly and a little alarming:
  ```bash
  xattr -d com.apple.quarantine macos-darwin-mcp
  ```
- **Ad-hoc sign** (`codesign -s -`) — silences *some* checks but does **not** clear the quarantine prompt for downloaded files. Doesn't really solve it.
- **Proper Developer ID signing + notarization** — the real fix. The binary launches with zero friction. This requires enrollment in the **Apple Developer Program ($99/year)**, then `codesign` with your Developer ID cert and `notarytool` to submit to Apple. This is the only path that gives a clean experience, and it's worth weighing given the project's nature.

That last point matters more than usual here: **this server controls the user's entire Mac.** A binary that can read your Messages and send mail is exactly the kind of thing a security-conscious user will hesitate to run unsigned. Which leads to —

**2. Supply-chain trust.** Building from source is *auditable* — the user can read what they're running. A prebuilt binary asks them to trust your CI. Mitigate with **published SHA-256 checksums** (a `checksums.txt` in the release) and, ideally, build provenance (GitHub's `attest-build-provenance` action). This doesn't replace signing; it complements it.

**3. CGO / portability.** Make sure releases are built with **`CGO_ENABLED=0`** so you ship a fully static, dependency-free binary. Since this server shells out to system tools rather than linking C libraries, that should be a no-op — but it's worth pinning explicitly, because a cgo-linked binary can pick up dynamic library or macOS-SDK-version dependencies that break on other machines.

**4. Minimum OS version.** Your floor is already Ventura (Darwin 22), and a static Go binary has no OS-version dependency beyond the syscalls it makes. Just keep stating "macOS 13+" on the release.

**5. No auto-update.** Source users `git pull`; binary users won't know a new version exists. Not fatal, but plan for it — clear version tags, and maybe a note in the README to check Releases.

## My recommendation

Offer **both**: keep build-from-source as the auditable path, and add a Release binary for convenience. Start with **checksums + the `xattr` instruction** to ship immediately, and treat **Developer ID notarization as the next step** once you decide the $99/yr is worth it — for a whole-Mac control server, I'd lean toward doing it before any wide promotion.

Want me to:
- Add a **`.github/workflows/release.yml`** that builds the Universal 2 binary, generates checksums, and publishes on `v*` tags, and
- Add a **"Download a prebuilt binary"** subsection to the README's Get started (with the quarantine note)?

I can wire up the unsigned-with-checksums version now and leave clearly-marked hooks for notarization later. Just say the word.
