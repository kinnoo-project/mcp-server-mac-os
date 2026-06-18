// integration_test.go exercises the server the way a real MCP client does:
// a client and our server are connected over the SDK's in-memory transport, and
// we drive the actual protocol (list tools, call tools). This verifies the whole
// stack end to end — registration, schema generation, dispatch, and result
// encoding — through the same machinery a production client uses, without the
// flakiness of hand-framing JSON-RPC over a pipe.
package server

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"mcp-server-mac-os/internal/engine"
	"mcp-server-mac-os/internal/registry"
)

// connectClient wires a real Server to a client over in-memory transports
// (Connect, in inprocess.go — also shared by the eval harness) and returns the
// connected client session, registering its cleanup with the test.
func connectClient(t *testing.T) *mcp.ClientSession {
	t.Helper()
	cs, cleanup, err := Connect(context.Background())
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	t.Cleanup(cleanup)
	return cs
}

// TestIntegration_ToolSurface confirms the protocol exposes one domain tool per
// category — `filesystem` and `preferences` — alongside the two fixed
// mutation-lifecycle tools (`execute`, `undo`), and that each domain tool's
// description embeds its full operation menu so the model needs no separate
// discovery call.
func TestIntegration_ToolSurface(t *testing.T) {
	cs := connectClient(t)
	lt, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}

	descs := map[string]string{}
	for _, tool := range lt.Tools {
		descs[tool.Name] = tool.Description
	}
	for _, want := range []string{"filesystem", "preferences", "execute", "undo"} {
		if _, ok := descs[want]; !ok {
			t.Errorf("expected tool %q in surface, got %v", want, toolNames(lt))
		}
	}
	if len(lt.Tools) != 4 {
		t.Errorf("expected exactly 4 tools (filesystem, preferences, execute, undo), got %v", toolNames(lt))
	}

	for _, op := range []string{"ls", "pwd", "file", "stat", "wc", "du", "find", "grep", "largest_files", "mkdir"} {
		if !strings.Contains(descs["filesystem"], op) {
			t.Errorf("filesystem tool description missing operation %q", op)
		}
	}
	if !strings.Contains(descs["preferences"], "write_setting") {
		t.Errorf("preferences tool description missing operation %q", "write_setting")
	}
}

// TestIntegration_WriteSettingStageOnly calls the real `preferences` domain
// tool with a real curated setting and confirms staging returns a token +
// preview without modifying anything.
//
// SAFETY: this deliberately stops at staging and never calls `execute`. Stage
// only performs a read-only `defaults read` probe of the developer's/CI
// machine's actual current value — it must NEVER write to it. Exercising a
// real forward/inverse write against a disposable domain is covered instead by
// the engine-level tests in internal/engine/mutate_preferences_test.go, which
// use a synthetic allowlist entry pointed at a temp file precisely so the real
// system settings are never touched by the test suite.
func TestIntegration_WriteSettingStageOnly(t *testing.T) {
	cs := connectClient(t)
	staged, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "preferences",
		Arguments: map[string]any{
			"operation": "write_setting",
			"params":    map[string]any{"setting": "finder_show_hidden_files", "value": true},
		},
	})
	if err != nil {
		t.Fatalf("CallTool stage write_setting: %v", err)
	}
	if staged.IsError {
		t.Fatalf("stage write_setting returned error: %s", textOf(staged))
	}
	text := textOf(staged)
	if !strings.Contains(text, "STAGED") {
		t.Errorf("expected a STAGED preview, got: %s", text)
	}
	_ = extractToken(t, text, "req_") // fails the test if no token is present
}

// TestDefaultsAllowlist_MatchesManifestEnum guards against the write_setting
// allowlist being declared twice (once as the manifest's "setting" enum, once
// as the engine's defaultsAllowlist map) and the two drifting apart — which
// would either silently hide a curated setting from the model or let the enum
// admit a name the engine doesn't actually recognize.
func TestDefaultsAllowlist_MatchesManifestEnum(t *testing.T) {
	reg, err := registry.Load()
	if err != nil {
		t.Fatalf("registry.Load(): %v", err)
	}
	capability, ok := reg.Lookup("write_setting")
	if !ok {
		t.Fatal("write_setting capability not found in registry")
	}
	var manifestEnum []string
	for _, p := range capability.Params {
		if p.Name == "setting" {
			manifestEnum = p.Enum
		}
	}
	if manifestEnum == nil {
		t.Fatal("write_setting manifest entry has no 'setting' param with an enum")
	}
	sort.Strings(manifestEnum)
	engineKeys := engine.DefaultsAllowlistKeys() // already sorted

	if len(manifestEnum) != len(engineKeys) {
		t.Fatalf("manifest enum has %d settings, engine allowlist has %d: manifest=%v engine=%v",
			len(manifestEnum), len(engineKeys), manifestEnum, engineKeys)
	}
	for i := range manifestEnum {
		if manifestEnum[i] != engineKeys[i] {
			t.Fatalf("manifest enum and engine allowlist diverge: manifest=%v engine=%v", manifestEnum, engineKeys)
		}
	}
}

// TestIntegration_MutationLifecycle drives the full stage → execute → undo flow
// over the real protocol, the way an MCP client would: staging mkdir returns a
// token and creates nothing; execute(token) creates the directory and returns an
// undo_token; undo(undo_token) removes it again.
func TestIntegration_MutationLifecycle(t *testing.T) {
	cs := connectClient(t)
	ctx := context.Background()
	target := filepath.Join(t.TempDir(), "made-by-mcp")

	// Stage: should hand back a token and leave the filesystem untouched.
	staged, err := cs.CallTool(ctx, &mcp.CallToolParams{
		Name:      "filesystem",
		Arguments: map[string]any{"operation": "mkdir", "params": map[string]any{"path": target}},
	})
	if err != nil {
		t.Fatalf("CallTool stage mkdir: %v", err)
	}
	if staged.IsError {
		t.Fatalf("stage mkdir returned error: %s", textOf(staged))
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("staging must not create the directory; stat err = %v", err)
	}
	token := extractToken(t, textOf(staged), "req_")

	// Execute: should create the directory and return an undo_token.
	executed, err := cs.CallTool(ctx, &mcp.CallToolParams{
		Name:      "execute",
		Arguments: map[string]any{"token": token},
	})
	if err != nil {
		t.Fatalf("CallTool execute: %v", err)
	}
	if executed.IsError {
		t.Fatalf("execute returned error: %s", textOf(executed))
	}
	if info, err := os.Stat(target); err != nil || !info.IsDir() {
		t.Fatalf("execute should have created the directory; stat err = %v", err)
	}
	undoToken := extractToken(t, textOf(executed), "undo_")

	// Undo: should remove the directory again.
	undone, err := cs.CallTool(ctx, &mcp.CallToolParams{
		Name:      "undo",
		Arguments: map[string]any{"undo_token": undoToken},
	})
	if err != nil {
		t.Fatalf("CallTool undo: %v", err)
	}
	if undone.IsError {
		t.Fatalf("undo returned error: %s", textOf(undone))
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Errorf("undo should have removed the directory; stat err = %v", err)
	}
}

// extractToken pulls the first QUOTED token carrying the given prefix out of a
// tool's text result, failing the test if none is present. Anchoring on the
// opening quote (e.g. `"req_abc123"`) avoids matching the prefix where it appears
// as plain prose — notably the label "undo_token" in the execute message — and
// mirrors how a model would read the token back out of the response.
func extractToken(t *testing.T, text, prefix string) string {
	t.Helper()
	marker := `"` + prefix
	idx := strings.Index(text, marker)
	if idx < 0 {
		t.Fatalf("expected a quoted %s token in result, got: %s", prefix, text)
	}
	tok := text[idx+1:] // skip the opening quote
	end := strings.IndexByte(tok, '"')
	if end < 0 {
		t.Fatalf("expected a closing quote after %s token, got: %s", prefix, text)
	}
	return tok[:end]
}

// TestIntegration_FilesystemPwd calls the pwd operation through the filesystem
// domain tool over the protocol and confirms a real working-directory string
// comes back.
func TestIntegration_FilesystemPwd(t *testing.T) {
	cs := connectClient(t)
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "filesystem",
		Arguments: map[string]any{"operation": "pwd"},
	})
	if err != nil {
		t.Fatalf("CallTool filesystem pwd: %v", err)
	}
	if res.IsError {
		t.Fatalf("filesystem pwd returned error: %s", textOf(res))
	}
	if !strings.HasPrefix(textOf(res), "/") {
		t.Errorf("pwd should return an absolute path, got %q", textOf(res))
	}
}

func toolNames(lt *mcp.ListToolsResult) []string {
	names := make([]string, 0, len(lt.Tools))
	for _, tool := range lt.Tools {
		names = append(names, tool.Name)
	}
	return names
}
