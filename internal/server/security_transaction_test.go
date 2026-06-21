// security_transaction_test.go is part of the production security gate (see
// docs/TESTS.md). It drives the real MCP server adversarially to prove the
// stage→execute→undo token gate cannot be abused to run a mutation that was
// never staged, run a staged one twice, or undo with the wrong token:
//
//   - execute with a forged/garbage token does nothing,
//   - a token is one-shot: a second execute with the same token is refused,
//   - the execute and undo token namespaces do not cross (a stage `req_` token
//     is not a valid `undo_` token, and vice versa),
//   - an undo token is likewise one-shot.
//
// These guard the property that "what runs is exactly what a human approved,
// exactly once." Token uniqueness/TTL is unit-tested at the store level in
// internal/transaction; here we verify the server enforces it end to end.
package server

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// callTool is a thin wrapper that fails the test on a transport error (as
// opposed to a tool-level error, which is reported in the result's IsError).
func callTool(t *testing.T, cs *mcp.ClientSession, name string, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("CallTool %s: %v", name, err)
	}
	return res
}

// TestSecurityTxn_ExecuteRejectsForgedToken confirms a token the server never
// issued is refused, so a mutation cannot be conjured without first staging it.
func TestSecurityTxn_ExecuteRejectsForgedToken(t *testing.T) {
	cs := connectClient(t)
	for _, forged := range []string{"req_deadbeef", "not-a-token", "", "undo_abc123"} {
		res := callTool(t, cs, "execute", map[string]any{"token": forged})
		if !res.IsError {
			t.Errorf("execute with forged token %q should error, got success: %s", forged, textOf(res))
		}
	}
}

// TestSecurityTxn_ExecuteIsOneShot confirms a staged plan can be committed only
// once: the token is consumed on first use, so a replay cannot re-run the
// mutation (which for a real destructive op would double its effect).
func TestSecurityTxn_ExecuteIsOneShot(t *testing.T) {
	cs := connectClient(t)
	target := filepath.Join(t.TempDir(), "made-once")

	staged := callTool(t, cs, "filesystem", map[string]any{
		"operation": "mkdir",
		"params":    map[string]any{"path": target},
	})
	if staged.IsError {
		t.Fatalf("stage mkdir errored: %s", textOf(staged))
	}
	token := extractToken(t, textOf(staged), "req_")

	first := callTool(t, cs, "execute", map[string]any{"token": token})
	if first.IsError {
		t.Fatalf("first execute errored: %s", textOf(first))
	}
	if info, err := os.Stat(target); err != nil || !info.IsDir() {
		t.Fatalf("first execute should have created the directory; stat err = %v", err)
	}

	// Second execute with the same (now-consumed) token must be refused.
	second := callTool(t, cs, "execute", map[string]any{"token": token})
	if !second.IsError {
		t.Errorf("second execute with a consumed token should error, got success: %s", textOf(second))
	}
}

// TestSecurityTxn_UndoRejectsStageToken confirms the execute and undo token
// namespaces do not cross: a stage (`req_`) token is not accepted by undo, and a
// fabricated undo token is refused — so a caller cannot reverse (or replay) a
// mutation by feeding the wrong token to the wrong endpoint.
func TestSecurityTxn_UndoRejectsStageToken(t *testing.T) {
	cs := connectClient(t)
	target := filepath.Join(t.TempDir(), "made-by-mcp")

	staged := callTool(t, cs, "filesystem", map[string]any{
		"operation": "mkdir",
		"params":    map[string]any{"path": target},
	})
	if staged.IsError {
		t.Fatalf("stage mkdir errored: %s", textOf(staged))
	}
	stageToken := extractToken(t, textOf(staged), "req_")

	// Using the stage token as an undo token must fail (wrong namespace).
	res := callTool(t, cs, "undo", map[string]any{"undo_token": stageToken})
	if !res.IsError {
		t.Errorf("undo with a stage token should error, got success: %s", textOf(res))
	}
	// A fabricated undo token must also fail.
	res = callTool(t, cs, "undo", map[string]any{"undo_token": "undo_deadbeef"})
	if !res.IsError {
		t.Errorf("undo with a forged token should error, got success: %s", textOf(res))
	}
}

// TestSecurityTxn_UndoIsOneShot confirms an undo token, like an execute token, is
// consumed on first use: a second undo with the same token is refused.
func TestSecurityTxn_UndoIsOneShot(t *testing.T) {
	cs := connectClient(t)
	target := filepath.Join(t.TempDir(), "made-by-mcp")

	staged := callTool(t, cs, "filesystem", map[string]any{
		"operation": "mkdir",
		"params":    map[string]any{"path": target},
	})
	if staged.IsError {
		t.Fatalf("stage mkdir errored: %s", textOf(staged))
	}
	token := extractToken(t, textOf(staged), "req_")

	executed := callTool(t, cs, "execute", map[string]any{"token": token})
	if executed.IsError {
		t.Fatalf("execute errored: %s", textOf(executed))
	}
	undoToken := extractToken(t, textOf(executed), "undo_")

	firstUndo := callTool(t, cs, "undo", map[string]any{"undo_token": undoToken})
	if firstUndo.IsError {
		t.Fatalf("first undo errored: %s", textOf(firstUndo))
	}
	secondUndo := callTool(t, cs, "undo", map[string]any{"undo_token": undoToken})
	if !secondUndo.IsError {
		t.Errorf("second undo with a consumed token should error, got success: %s", textOf(secondUndo))
	}
}
