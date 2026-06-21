// mutate_process_test.go tests the two mutating process operations: PID
// validation shared by both, the graceful-quit app resolution and its mandatory
// osascript option-injection regression, and that terminate_process hardcodes
// SIGTERM (never SIGKILL). Staging performs only read-only probes, so these
// tests stage against the test process's OWN PID (which exists and is > 1)
// without ever executing the forward command.
package engine

import (
	"context"
	"os"
	"strconv"
	"testing"

	"mcp-server-mac-os/internal/registry"
)

func TestRequirePID(t *testing.T) {
	if _, err := requirePID("op", map[string]any{}); err == nil {
		t.Errorf("missing pid should be rejected")
	}
	for _, pid := range []int{0, 1, -3} {
		if _, err := requirePID("op", map[string]any{"pid": pid}); err == nil {
			t.Errorf("pid %d should be rejected (protected low PIDs)", pid)
		}
	}
	if got, err := requirePID("op", map[string]any{"pid": 4242}); err != nil || got != 4242 {
		t.Errorf("requirePID(4242) = %d,%v", got, err)
	}
}

// TestQuitProcess_OptionInjection is the mandatory regression for the osascript
// surface: a flag-like application name must be carried as DATA, never parsed as
// an option. It checks both guards quit_process relies on — the unconditional
// "--" terminator and the explicit validateAppNameValue check.
func TestQuitProcess_OptionInjection(t *testing.T) {
	// The "--" terminator must precede the app name, so even a value like "-e"
	// (which osascript would otherwise read as "execute this next statement")
	// lands in `on run argv` as data.
	cmd := osascriptCommand(quitScript, "-e")
	dashIdx, valIdx := -1, -1
	for i, a := range cmd.Args {
		if a == "--" && dashIdx == -1 {
			dashIdx = i
		}
		if a == "-e" {
			valIdx = i
		}
	}
	if dashIdx == -1 || valIdx == -1 || valIdx < dashIdx {
		t.Fatalf("flag-like app name not placed after '--': args=%v", cmd.Args)
	}

	// And the belt-and-suspenders validation rejects a dash-leading bundle name
	// outright before it could ever reach argv.
	if _, err := validateAppNameValue("quit_process", "application", "-e"); err == nil {
		t.Errorf("dash-leading app name should be rejected by validateAppNameValue")
	}
}

// TestQuitProcess_RejectsNonApp confirms a non-GUI process (the test binary
// itself, which is not inside a .app bundle) is refused with a pointer to
// terminate_process rather than producing a quit plan.
func TestQuitProcess_RejectsNonApp(t *testing.T) {
	in := map[string]any{"pid": os.Getpid()}
	if _, err := stageQuitProcess(context.Background(), registry.Capability{}, in); err == nil {
		t.Errorf("quit_process on a non-app process should be rejected")
	}
}

// TestTerminateProcess_HardcodesTERM stages a SIGTERM against the test process's
// own PID (a real, existing, >1 PID) and asserts the forward command is exactly
// `kill -TERM <pid>` with no inverse — i.e. it can never be a force-kill and is
// not undoable.
func TestTerminateProcess_HardcodesTERM(t *testing.T) {
	pid := os.Getpid()
	plan, err := stageTerminateProcess(context.Background(), registry.Capability{}, map[string]any{"pid": pid})
	if err != nil {
		t.Fatalf("stageTerminateProcess: %v", err)
	}
	if plan.Forward.Binary != "kill" {
		t.Errorf("forward binary = %q, want kill", plan.Forward.Binary)
	}
	want := []string{"-TERM", strconv.Itoa(pid)}
	if len(plan.Forward.Args) != 2 || plan.Forward.Args[0] != want[0] || plan.Forward.Args[1] != want[1] {
		t.Errorf("forward args = %v, want %v", plan.Forward.Args, want)
	}
	if plan.Inverse != nil {
		t.Errorf("terminate_process must have no inverse (not undoable), got %+v", plan.Inverse)
	}
}

// TestMutateProcess_PartitionByAppBundle locks the contract that the two
// mutators cleanly partition the process space on the SAME discriminator
// (appNameFromExePath): a GUI-app executable path is what quit_process accepts
// and terminate_process must refuse, while a plain binary path is the reverse.
// stageTerminateProcess's GUI-app refusal and stageQuitProcess's non-app
// refusal both branch on this, so a change to the discriminator that broke the
// partition would surface here. (The live refusal itself mirrors
// TestQuitProcess_RejectsNonApp / TestTerminateProcess_HardcodesTERM, which
// stage against the non-app test-binary PID.)
func TestMutateProcess_PartitionByAppBundle(t *testing.T) {
	const appPath = "/Applications/Safari.app/Contents/MacOS/Safari"
	const binPath = "/usr/sbin/cupsd"
	if appNameFromExePath(appPath) == "" {
		t.Errorf("a .app path must be classified as a GUI app (quit_process accepts, terminate_process refuses)")
	}
	if appNameFromExePath(binPath) != "" {
		t.Errorf("a plain binary path must NOT be classified as a GUI app (terminate_process accepts, quit_process refuses)")
	}
}

// TestMutateProcess_RejectsLowPID confirms both mutators refuse the protected
// low PIDs before any probing.
func TestMutateProcess_RejectsLowPID(t *testing.T) {
	ctx := context.Background()
	for _, pid := range []int{0, 1} {
		if _, err := stageQuitProcess(ctx, registry.Capability{}, map[string]any{"pid": pid}); err == nil {
			t.Errorf("quit_process pid %d should be rejected", pid)
		}
		if _, err := stageTerminateProcess(ctx, registry.Capability{}, map[string]any{"pid": pid}); err == nil {
			t.Errorf("terminate_process pid %d should be rejected", pid)
		}
	}
}
