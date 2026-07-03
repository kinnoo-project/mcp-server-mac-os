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
// category — `filesystem`, `preferences`, `application-mail`,
// `application-calendar`, and `application-reminders` — alongside the three
// fixed cross-cutting tools (`execute`, `undo`, `pipeline`), and that each
// domain tool's description embeds its full operation menu so the model needs no
// separate discovery call.
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
	for _, want := range []string{"filesystem", "preferences", "application", "printer", "system", "network", "process", "screenshot", "clipboard", "application-mail", "application-calendar", "application-reminders", "application-phone", "application-messages", "application-notes", "application-photos", "application-safari", "application-contacts", "application-music", "execute", "undo", "pipeline"} {
		if _, ok := descs[want]; !ok {
			t.Errorf("expected tool %q in surface, got %v", want, toolNames(lt))
		}
	}
	if len(lt.Tools) != 22 {
		t.Errorf("expected exactly 22 tools (filesystem, preferences, application, printer, system, network, process, screenshot, clipboard, application-mail, application-calendar, application-reminders, application-phone, application-messages, application-notes, application-photos, application-safari, application-contacts, application-music, execute, undo, pipeline), got %v", toolNames(lt))
	}

	for _, op := range []string{"ls", "pwd", "file", "stat", "wc", "du", "find", "grep", "largest_files", "mkdir", "sort", "head", "compress", "extract"} {
		if !strings.Contains(descs["filesystem"], op) {
			t.Errorf("filesystem tool description missing operation %q", op)
		}
	}
	if !strings.Contains(descs["preferences"], "write_setting") {
		t.Errorf("preferences tool description missing operation %q", "write_setting")
	}
	for _, op := range []string{"search_mail", "send_mail"} {
		if !strings.Contains(descs["application-mail"], op) {
			t.Errorf("application-mail tool description missing operation %q", op)
		}
	}
	for _, op := range []string{"list_calendars", "query_events", "add_event", "modify_event", "delete_event"} {
		if !strings.Contains(descs["application-calendar"], op) {
			t.Errorf("application-calendar tool description missing operation %q", op)
		}
	}
	for _, op := range []string{"list_reminders", "add_reminder", "modify_reminder", "complete_reminder", "delete_reminder"} {
		if !strings.Contains(descs["application-reminders"], op) {
			t.Errorf("application-reminders tool description missing operation %q", op)
		}
	}
	for _, op := range []string{"find_contact", "call"} {
		if !strings.Contains(descs["application-phone"], op) {
			t.Errorf("application-phone tool description missing operation %q", op)
		}
	}
	for _, op := range []string{"check_messages", "search_messages", "read_conversation", "list_conversations", "send_message"} {
		if !strings.Contains(descs["application-messages"], op) {
			t.Errorf("application-messages tool description missing operation %q", op)
		}
	}
	for _, op := range []string{"list_notes", "search_notes", "read_note", "list_folders", "create_note", "append_to_note"} {
		if !strings.Contains(descs["application-notes"], op) {
			t.Errorf("application-notes tool description missing operation %q", op)
		}
	}
	for _, op := range []string{"list_tabs", "current_tab"} {
		if !strings.Contains(descs["application-safari"], op) {
			t.Errorf("application-safari tool description missing operation %q", op)
		}
	}
	for _, op := range []string{"get_contact", "create_contact"} {
		if !strings.Contains(descs["application-contacts"], op) {
			t.Errorf("application-contacts tool description missing operation %q", op)
		}
	}
	for _, op := range []string{"now_playing", "play_pause", "next_track", "previous_track"} {
		if !strings.Contains(descs["application-music"], op) {
			t.Errorf("application-music tool description missing operation %q", op)
		}
	}
	for _, op := range []string{"list_applications", "search_applications", "search_app_store", "open_app_store_page", "list_running_applications", "open_application", "focus_application", "quit_application"} {
		if !strings.Contains(descs["application"], op) {
			t.Errorf("application tool description missing operation %q", op)
		}
	}
	for _, op := range []string{"list_printers", "list_print_jobs", "print_file", "print_test_page"} {
		if !strings.Contains(descs["printer"], op) {
			t.Errorf("printer tool description missing operation %q", op)
		}
	}
	for _, op := range []string{"wifi_status", "list_preferred_wifi", "bluetooth_status", "power_status", "open_settings"} {
		if !strings.Contains(descs["system"], op) {
			t.Errorf("system tool description missing operation %q", op)
		}
	}
	for _, op := range []string{"current_network", "dns_servers", "ping_host", "dns_lookup", "listening_ports", "lan_devices", "scan_lan"} {
		if !strings.Contains(descs["network"], op) {
			t.Errorf("network tool description missing operation %q", op)
		}
	}
	for _, op := range []string{"list_processes", "process_info", "cpu_load", "memory_stats", "gpu_stats", "startup_items", "quit_process", "terminate_process"} {
		if !strings.Contains(descs["process"], op) {
			t.Errorf("process tool description missing operation %q", op)
		}
	}
	if !strings.Contains(descs["screenshot"], "capture_screen") {
		t.Errorf("screenshot tool description missing operation %q", "capture_screen")
	}
	for _, name := range []string{"find", "wc", "grep", "sort", "head"} {
		if !strings.Contains(descs["pipeline"], name) {
			t.Errorf("pipeline tool description missing eligible capability %q", name)
		}
	}
	eligible := eligibleCapabilitiesFromDescription(t, descs["pipeline"])
	for _, name := range []string{"pwd", "largest_files", "mkdir", "write_setting", "search_mail", "send_mail"} {
		if eligible[name] {
			t.Errorf("pipeline tool description lists ineligible capability %q as eligible", name)
		}
	}
}

// eligibleCapabilitiesFromDescription parses the pipeline tool's "Eligible
// capabilities (...): a, b, c." line out of its description into a set, so
// tests can check exact membership rather than substring-matching the whole
// description (which would be fooled by, e.g., "find" appearing inside
// "findings").
func eligibleCapabilitiesFromDescription(t *testing.T, desc string) map[string]bool {
	t.Helper()
	const marker = "may appear as a stage): "
	idx := strings.Index(desc, marker)
	if idx < 0 {
		t.Fatalf("pipeline description missing the eligible-capabilities line: %s", desc)
	}
	list := desc[idx+len(marker):]
	if end := strings.Index(list, ".\n"); end >= 0 {
		list = list[:end]
	}
	set := make(map[string]bool)
	for _, name := range strings.Split(list, ", ") {
		set[strings.TrimSuffix(name, ".")] = true
	}
	return set
}

// TestIntegration_SearchMailNoMatch calls the real application-mail domain
// tool's search_mail operation over the protocol with a query engineered to
// match nothing, confirming the real path works end to end without ever
// touching real mail content (see the SAFETY note in builtins_mail_test.go
// for why this query is safe to run for real).
func TestIntegration_SearchMailNoMatch(t *testing.T) {
	cs := connectClient(t)
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "application-mail",
		Arguments: map[string]any{
			"operation": "search_mail",
			"params":    map[string]any{"query": "zzz-search-mail-test-token-guaranteed-no-match-9f3e7c1a"},
		},
	})
	if err != nil {
		t.Fatalf("CallTool search_mail: %v", err)
	}
	if res.IsError {
		t.Fatalf("search_mail returned error: %s", textOf(res))
	}
	if !strings.Contains(textOf(res), "No mail found") {
		t.Errorf("expected a clean no-results message, got %q", textOf(res))
	}
}

// TestIntegration_SendMailStageOnly calls the real application-mail domain
// tool's send_mail operation and confirms staging returns a token + preview.
//
// SAFETY: this deliberately stops at staging and NEVER calls execute.
// send_mail is irreversible — there is no synthetic/disposable target the
// way mkdir's temp directory or write_setting's synthetic allowlist entry
// provide. Calling execute on a staged send_mail plan would send a real
// email with no way to undo it, so no test anywhere in this suite may do
// that.
func TestIntegration_SendMailStageOnly(t *testing.T) {
	cs := connectClient(t)
	dir := t.TempDir()
	attachment := filepath.Join(dir, "itinerary.pdf")
	if err := os.WriteFile(attachment, []byte("fake pdf bytes"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	staged, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "application-mail",
		Arguments: map[string]any{
			"operation": "send_mail",
			"params": map[string]any{
				"to":          []any{"test-recipient@example.com"},
				"subject":     "integration test — never sent",
				"body":        "This plan must never be executed by any test.",
				"attachments": []any{attachment},
			},
		},
	})
	if err != nil {
		t.Fatalf("CallTool stage send_mail: %v", err)
	}
	if staged.IsError {
		t.Fatalf("stage send_mail returned error: %s", textOf(staged))
	}
	text := textOf(staged)
	if !strings.Contains(text, "STAGED") {
		t.Errorf("expected a STAGED preview, got: %s", text)
	}
	if !strings.Contains(text, "cannot be undone") {
		t.Errorf("expected the irreversibility warning in the preview, got: %s", text)
	}
	if !strings.Contains(text, "Attachments: itinerary.pdf") {
		t.Errorf("expected the attachment filename in the preview, got: %s", text)
	}
	_ = extractToken(t, text, "req_") // fails the test if no token is present
}

// TestIntegration_PipelineFindThenWc drives a real two-stage pipeline over the
// actual protocol: find lists matching files, wc -l counts them.
func TestIntegration_PipelineFindThenWc(t *testing.T) {
	cs := connectClient(t)
	dir := t.TempDir()
	for _, name := range []string{"a.log", "b.log"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644); err != nil {
			t.Fatalf("write fixture %s: %v", name, err)
		}
	}

	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "pipeline",
		Arguments: map[string]any{
			"stages": []any{
				map[string]any{"capability": "find", "params": map[string]any{"path": dir, "extensions": []any{"log"}}},
				map[string]any{"capability": "wc", "params": map[string]any{"lines": true}},
			},
		},
	})
	if err != nil {
		t.Fatalf("CallTool pipeline: %v", err)
	}
	if res.IsError {
		t.Fatalf("pipeline find|wc returned error: %s", textOf(res))
	}
	if !strings.Contains(textOf(res), "2") {
		t.Errorf("expected wc -l to report 2 files, got %q", textOf(res))
	}
}

// TestIntegration_PipelineRejectsMutatorStage confirms the pipeline tool
// reports a clear, structured error (not a panic, not a silent no-op) when
// asked to chain a mutator.
func TestIntegration_PipelineRejectsMutatorStage(t *testing.T) {
	cs := connectClient(t)
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "pipeline",
		Arguments: map[string]any{
			"stages": []any{
				map[string]any{"capability": "mkdir", "params": map[string]any{"path": filepath.Join(t.TempDir(), "x")}},
			},
		},
	})
	if err != nil {
		t.Fatalf("CallTool pipeline: %v", err)
	}
	if !res.IsError {
		t.Fatal("expected an error: mkdir is a mutator and cannot be a pipeline stage")
	}
	if !strings.Contains(textOf(res), "mkdir") {
		t.Errorf("error should name the rejected capability, got: %s", textOf(res))
	}
}

// TestIntegration_PipelineRejectsUnknownCapability confirms a typo'd or
// nonexistent capability name is reported clearly rather than dispatched.
func TestIntegration_PipelineRejectsUnknownCapability(t *testing.T) {
	cs := connectClient(t)
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "pipeline",
		Arguments: map[string]any{
			"stages": []any{map[string]any{"capability": "does-not-exist"}},
		},
	})
	if err != nil {
		t.Fatalf("CallTool pipeline: %v", err)
	}
	if !res.IsError {
		t.Fatal("expected an error for an unknown capability name")
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

// TestReadSettingEnum_MatchesDefaultsAllowlist guards the read side of the
// curated-preferences allowlist: read_setting shares write_setting's setting
// enum and the engine's defaultsAllowlist map, so its manifest enum must match
// the allowlist exactly. Without this, a setting could be writable but not
// readable (or vice versa) if the two manifest enums drifted apart.
func TestReadSettingEnum_MatchesDefaultsAllowlist(t *testing.T) {
	reg, err := registry.Load()
	if err != nil {
		t.Fatalf("registry.Load(): %v", err)
	}
	capability, ok := reg.Lookup("read_setting")
	if !ok {
		t.Fatal("read_setting capability not found in registry")
	}
	var manifestEnum []string
	for _, p := range capability.Params {
		if p.Name == "setting" {
			manifestEnum = p.Enum
		}
	}
	if manifestEnum == nil {
		t.Fatal("read_setting manifest entry has no 'setting' param with an enum")
	}
	sort.Strings(manifestEnum)
	engineKeys := engine.DefaultsAllowlistKeys() // already sorted

	if len(manifestEnum) != len(engineKeys) {
		t.Fatalf("read_setting enum has %d settings, engine allowlist has %d: manifest=%v engine=%v",
			len(manifestEnum), len(engineKeys), manifestEnum, engineKeys)
	}
	for i := range manifestEnum {
		if manifestEnum[i] != engineKeys[i] {
			t.Fatalf("read_setting enum and engine allowlist diverge: manifest=%v engine=%v", manifestEnum, engineKeys)
		}
	}
}

// TestSettingsPanes_MatchManifestEnum guards against the open_settings pane list
// being declared twice (once as the manifest's "pane" enum, once as the engine's
// settingsPaneURLs map) and the two drifting apart — which would let the enum
// admit a pane the engine has no URL for, or hide a URL the model can never reach.
func TestSettingsPanes_MatchManifestEnum(t *testing.T) {
	reg, err := registry.Load()
	if err != nil {
		t.Fatalf("registry.Load(): %v", err)
	}
	capability, ok := reg.Lookup("open_settings")
	if !ok {
		t.Fatal("open_settings capability not found in registry")
	}
	var manifestEnum []string
	for _, p := range capability.Params {
		if p.Name == "pane" {
			manifestEnum = p.Enum
		}
	}
	if manifestEnum == nil {
		t.Fatal("open_settings manifest entry has no 'pane' param with an enum")
	}
	sort.Strings(manifestEnum)
	engineKeys := engine.SettingsPaneKeys() // already sorted

	if len(manifestEnum) != len(engineKeys) {
		t.Fatalf("manifest enum has %d panes, engine map has %d: manifest=%v engine=%v",
			len(manifestEnum), len(engineKeys), manifestEnum, engineKeys)
	}
	for i := range manifestEnum {
		if manifestEnum[i] != engineKeys[i] {
			t.Fatalf("manifest enum and engine pane map diverge: manifest=%v engine=%v", manifestEnum, engineKeys)
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

// TestIntegration_WriteFileLifecycle drives write_file through the real MCP
// protocol: stage (nothing on disk, token returned), execute (the file appears
// with the exact bytes — proving the stdin payload survives the token store),
// undo (the file is recycled into the sandbox Trash). $HOME is redirected so
// the Trash-routed inverse never touches the real Trash.
func TestIntegration_WriteFileLifecycle(t *testing.T) {
	home := t.TempDir()
	if err := os.Mkdir(filepath.Join(home, ".Trash"), 0o755); err != nil {
		t.Fatalf("creating sandbox Trash: %v", err)
	}
	t.Setenv("HOME", home)

	cs := connectClient(t)
	ctx := context.Background()
	target := filepath.Join(t.TempDir(), "written-by-mcp.txt")
	content := "first line\nsecond line\n"

	staged, err := cs.CallTool(ctx, &mcp.CallToolParams{
		Name:      "filesystem",
		Arguments: map[string]any{"operation": "write_file", "params": map[string]any{"path": target, "content": content}},
	})
	if err != nil {
		t.Fatalf("CallTool stage write_file: %v", err)
	}
	if staged.IsError {
		t.Fatalf("stage write_file returned error: %s", textOf(staged))
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("staging must not create the file; stat err = %v", err)
	}
	token := extractToken(t, textOf(staged), "req_")

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
	if got, err := os.ReadFile(target); err != nil || string(got) != content {
		t.Fatalf("execute should have written the exact bytes; got %q (err %v)", got, err)
	}
	undoToken := extractToken(t, textOf(executed), "undo_")

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
		t.Errorf("undo should have removed the created file; stat err = %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, ".Trash", "written-by-mcp.txt")); err != nil {
		t.Errorf("undo should have recycled the file into the sandbox Trash: %v", err)
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
