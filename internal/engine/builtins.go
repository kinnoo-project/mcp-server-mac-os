// builtins.go implements "builtin" capabilities: ones the server answers itself,
// in Go, without launching any external program.
//
// The name is borrowed from shells, where a "builtin" such as cd or pwd is
// handled by the shell directly rather than by executing a separate binary. The
// sole builtin today is pwd: the current working directory is a property of THIS
// process that we already hold, so the right answer comes from a one-line
// standard-library call. Spawning /bin/pwd would only re-report the very
// directory the child inherited from us — the same answer at the cost of a
// subprocess. A builtin capability therefore has no Binary and produces its
// output text directly.
package engine

import (
	"context"
	"os"

	"mcp-server-mac-os/internal/registry"
)

// BuiltinFunc produces a capability's output directly, with no subprocess. Like
// an ArgBuilder it receives the normalized parameters, but it returns finished
// output text rather than an argument vector.
type BuiltinFunc func(ctx context.Context, c registry.Capability, in map[string]any) (string, error)

// builtins maps a capability's builder name to its builtin implementation. A
// name present here is served in-process; a name present in the argv `builders`
// map is run as a subprocess. The two sets are disjoint.
var builtins = map[string]BuiltinFunc{
	"pwd":           runPwd,
	"largest_files": runLargestFiles,
}

// lookupBuiltin returns the builtin for a builder name and whether one exists.
func lookupBuiltin(name string) (BuiltinFunc, bool) {
	f, ok := builtins[name]
	return f, ok
}

// runPwd returns the server process's current working directory.
func runPwd(_ context.Context, _ registry.Capability, _ map[string]any) (string, error) {
	return os.Getwd()
}
