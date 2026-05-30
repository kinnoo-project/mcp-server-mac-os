package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Type aliases keep the test helpers below short and readable.
type (
	mcpResult = mcp.CallToolResult
	mcpText   = mcp.TextContent
)

// makeTempTree builds a small, deterministic directory layout used by every
// table-driven test below. Returning the root keeps each test hermetic.
func makeTempTree(t *testing.T) string {
	t.Helper()
	root := t.TempDir()

	mustWrite := func(rel, body string) {
		full := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", full, err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", full, err)
		}
	}

	mustWrite("hello.txt", "hello world\nsecond line\n")
	mustWrite("notes.md", "# Notes\nTODO: ship MCP\n")
	mustWrite("images/cat.png", "\x89PNG\r\n\x1a\nfake png body")
	mustWrite("images/dog.JPG", "\xFF\xD8\xFF\xE0fake jpg body")
	mustWrite("images/readme.txt", "not an image")
	mustWrite("nested/a/b/c/deep.txt", "deeply nested\n")
	return root
}

func TestExpandUserPath(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skipf("no home dir: %v", err)
	}
	cases := []struct {
		in, want string
	}{
		{"", ""},
		{"~", home},
		{"~/Downloads", filepath.Join(home, "Downloads")},
		{"/etc/hosts", "/etc/hosts"},
		{"./relative", "./relative"},
	}
	for _, c := range cases {
		got, err := expandUserPath(c.in)
		if err != nil {
			t.Fatalf("expandUserPath(%q): %v", c.in, err)
		}
		if got != c.want {
			t.Errorf("expandUserPath(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestResolveBinary_AllowedDirs(t *testing.T) {
	abs, err := resolveBinary("ls")
	if err != nil {
		t.Fatalf("resolveBinary(ls): %v", err)
	}
	if !strings.HasPrefix(abs, "/bin/") && !strings.HasPrefix(abs, "/usr/bin/") &&
		!strings.HasPrefix(abs, "/sbin/") && !strings.HasPrefix(abs, "/usr/sbin/") {
		t.Errorf("resolved binary %q is outside trusted directories", abs)
	}
}

func TestResolveBinary_RejectsPathSeparators(t *testing.T) {
	if _, err := resolveBinary("../evil"); err == nil {
		t.Fatal("expected error for binary name containing path separator")
	}
}

func TestCompactOutput(t *testing.T) {
	short := strings.Repeat("a", 100)
	if got := compactOutput(short); got != short {
		t.Error("short output should be returned unchanged")
	}
	long := strings.Repeat("x", maxOutputBytes*3)
	got := compactOutput(long)
	if !strings.Contains(got, "truncated") {
		t.Error("long output should include a truncation notice")
	}
	if len(got) >= len(long) {
		t.Error("long output should actually shrink")
	}
}

func TestLsHandler(t *testing.T) {
	root := makeTempTree(t)
	ctx := context.Background()
	res, _, err := lsHandler(ctx, nil, LsArgs{Path: root})
	if err != nil {
		t.Fatalf("lsHandler: %v", err)
	}
	if res.IsError {
		t.Fatalf("ls returned error: %s", textOf(res))
	}
	out := textOf(res)
	for _, want := range []string{"hello.txt", "notes.md", "images", "nested"} {
		if !strings.Contains(out, want) {
			t.Errorf("ls output missing %q: %s", want, out)
		}
	}
}

func TestLsHandler_LongHumanRecursive(t *testing.T) {
	root := makeTempTree(t)
	res, _, err := lsHandler(context.Background(), nil, LsArgs{
		Path: root, Long: true, Human: true, Recursive: true,
	})
	if err != nil || res.IsError {
		t.Fatalf("ls -lhR failed: %v / %s", err, textOf(res))
	}
	out := textOf(res)
	if !strings.Contains(out, "deep.txt") {
		t.Errorf("recursive ls did not surface nested file: %s", out)
	}
}

func TestPwdHandler(t *testing.T) {
	res, _, err := pwdHandler(context.Background(), nil, PwdArgs{})
	if err != nil || res.IsError {
		t.Fatalf("pwd failed: %v", err)
	}
	wd, _ := os.Getwd()
	if textOf(res) != wd {
		t.Errorf("pwd = %q, want %q", textOf(res), wd)
	}
}

func TestFileHandler(t *testing.T) {
	root := makeTempTree(t)
	res, _, err := fileHandler(context.Background(), nil, FileArgs{
		Paths: []string{filepath.Join(root, "hello.txt"), filepath.Join(root, "images/cat.png")},
	})
	if err != nil || res.IsError {
		t.Fatalf("file failed: %v / %s", err, textOf(res))
	}
	out := textOf(res)
	if !strings.Contains(strings.ToLower(out), "text") {
		t.Errorf("expected text classification for hello.txt: %s", out)
	}
	if !strings.Contains(strings.ToLower(out), "png") {
		t.Errorf("expected png classification for cat.png: %s", out)
	}
}

func TestFileHandler_RequiresPaths(t *testing.T) {
	res, _, _ := fileHandler(context.Background(), nil, FileArgs{})
	if !res.IsError {
		t.Fatal("expected IsError when no paths supplied")
	}
}

func TestGrepHandler_FindsMatches(t *testing.T) {
	root := makeTempTree(t)
	res, _, err := grepHandler(context.Background(), nil, GrepArgs{
		Pattern: "TODO", Paths: []string{root}, Recursive: true,
	})
	if err != nil || res.IsError {
		t.Fatalf("grep failed: %v / %s", err, textOf(res))
	}
	out := textOf(res)
	if !strings.Contains(out, "ship MCP") {
		t.Errorf("grep should have matched TODO line, got: %s", out)
	}
}

func TestGrepHandler_NoMatchesIsNotAnError(t *testing.T) {
	root := makeTempTree(t)
	res, _, err := grepHandler(context.Background(), nil, GrepArgs{
		Pattern: "this-string-does-not-exist-xyzzy", Paths: []string{root}, Recursive: true,
	})
	if err != nil {
		t.Fatalf("grep transport error: %v", err)
	}
	// Exit code 1 from grep (no match) is data, not failure.
	if res.IsError {
		t.Fatalf("grep no-match should not be IsError: %s", textOf(res))
	}
	if !strings.Contains(textOf(res), "exit code: 1") {
		t.Errorf("expected exit-code annotation, got: %s", textOf(res))
	}
}

func TestGrepHandler_PatternStartingWithDashIsSafe(t *testing.T) {
	// Without `-e`, grep would treat "-v" as a flag and dump the whole file.
	root := makeTempTree(t)
	res, _, err := grepHandler(context.Background(), nil, GrepArgs{
		Pattern: "-v", Paths: []string{filepath.Join(root, "hello.txt")},
	})
	if err != nil {
		t.Fatalf("grep transport error: %v", err)
	}
	if res.IsError {
		t.Fatalf("grep should treat dash-leading pattern as data: %s", textOf(res))
	}
}

func TestDuHandler_Summary(t *testing.T) {
	root := makeTempTree(t)
	res, _, err := duHandler(context.Background(), nil, DuArgs{
		Paths: []string{root}, Summary: true, Human: true,
	})
	if err != nil || res.IsError {
		t.Fatalf("du failed: %v / %s", err, textOf(res))
	}
	if !strings.Contains(textOf(res), root) {
		t.Errorf("du output should mention root path: %s", textOf(res))
	}
}

func TestFindHandler_Extensions(t *testing.T) {
	root := makeTempTree(t)
	res, _, err := findHandler(context.Background(), nil, FindArgs{
		Path: root, Type: "f", Extensions: []string{"png", "jpg"},
	})
	if err != nil || res.IsError {
		t.Fatalf("find failed: %v / %s", err, textOf(res))
	}
	out := textOf(res)
	if !strings.Contains(out, "cat.png") {
		t.Errorf("find should have found cat.png: %s", out)
	}
	if !strings.Contains(out, "dog.JPG") {
		t.Errorf("find should be case-insensitive on extensions: %s", out)
	}
	if strings.Contains(out, "readme.txt") {
		t.Errorf("find should NOT include readme.txt: %s", out)
	}
}

func TestFindHandler_RejectsUnsafeExtension(t *testing.T) {
	res, _, _ := findHandler(context.Background(), nil, FindArgs{
		Path: "/tmp", Extensions: []string{"png;rm -rf /"},
	})
	if !res.IsError {
		t.Fatal("expected IsError for extension containing shell metacharacters")
	}
}

func TestFindHandler_RejectsUnknownType(t *testing.T) {
	res, _, _ := findHandler(context.Background(), nil, FindArgs{
		Path: "/tmp", Type: "x",
	})
	if !res.IsError {
		t.Fatal("expected IsError for disallowed find -type")
	}
}

func TestStatHandler(t *testing.T) {
	root := makeTempTree(t)
	res, _, err := statHandler(context.Background(), nil, StatArgs{
		Paths: []string{filepath.Join(root, "hello.txt")},
	})
	if err != nil || res.IsError {
		t.Fatalf("stat failed: %v / %s", err, textOf(res))
	}
	if textOf(res) == "(no output)" {
		t.Error("stat returned no output")
	}
}

func TestWcHandler(t *testing.T) {
	root := makeTempTree(t)
	res, _, err := wcHandler(context.Background(), nil, WcArgs{
		Paths: []string{filepath.Join(root, "hello.txt")}, Lines: true,
	})
	if err != nil || res.IsError {
		t.Fatalf("wc failed: %v / %s", err, textOf(res))
	}
	if !strings.Contains(textOf(res), "2") {
		t.Errorf("wc -l should report 2 lines: %s", textOf(res))
	}
}

func TestRegisterToolsExposesOnlyReadOnly(t *testing.T) {
	// This is a sanity assertion that we never accidentally wire a mutating
	// command into the server. If you find yourself adding to this list,
	// re-read CLAUDE.md §3 first.
	disallowed := []string{"rm", "mv", "cp", "mkdir", "rmdir", "touch",
		"chmod", "chown", "ln", "dd", "tee", "sh", "bash", "zsh"}
	src, err := os.ReadFile("tools.go")
	if err != nil {
		t.Fatalf("read tools.go: %v", err)
	}
	body := string(src)
	for _, bad := range disallowed {
		// Look for a registration like resolveBinary("rm" or runTool(ctx, "rm".
		needle := "\"" + bad + "\""
		// Allow the word to appear in comments or unrelated code; restrict
		// to forms that suggest invocation.
		if strings.Contains(body, "resolveBinary("+needle) ||
			strings.Contains(body, "runTool(ctx, "+needle) {
			t.Errorf("mutating command %q must not be wired into tools.go", bad)
		}
	}
}

// textOf extracts the first TextContent string out of a CallToolResult, or ""
// if none is present. Centralized so individual tests stay readable.
func textOf(res *mcpResult) string {
	if res == nil {
		return ""
	}
	for _, c := range res.Content {
		if tc, ok := c.(*mcpText); ok {
			return tc.Text
		}
	}
	return ""
}
