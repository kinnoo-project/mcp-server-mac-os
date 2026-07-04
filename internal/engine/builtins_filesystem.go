// builtins_filesystem.go implements filesystem builtins — capabilities answered
// in-process rather than by a single external program.
//
// largest_files is the motivating case. "Show me the biggest files under X" has
// no single-command answer: the shell idiom (du -a | sort -rn | head) is a
// PIPELINE — one command's output ranked and trimmed by the next — and the
// project forbids shell pipelines (no `sh -c`, one binary per invocation). It is
// also exactly the kind of question that floods a model's context: a raw `find`
// returns one line per file (potentially megabytes) when the user wanted ten
// numbers. This builtin answers the question directly: it walks the tree once,
// keeps only the N largest files in a bounded heap, and returns just those N
// ranked lines. The ranking that a shell would do in `sort | head` happens here
// in Go, so the output is small by construction and never needs truncation.
package engine

import (
	"container/heap"
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"mcp-server-mac-os/internal/registry"
)

// maxLargestFilesCount caps how many results largest_files will return. Because
// builtin output bypasses the subprocess truncation budget, this bound is what
// keeps the response context-safe no matter what count a caller requests.
const maxLargestFilesCount = 100

// defaultLargestFilesCount is the number of files returned when count is omitted.
const defaultLargestFilesCount = 10

// fileEntry is one ranked file: its absolute path and logical size in bytes.
type fileEntry struct {
	path string
	size int64
}

// runLargestFiles walks a directory tree in-process and returns the N largest
// regular files, ranked descending by size.
//
// Design notes:
//   - Only regular files are considered; directories, symlinks, and special
//     files are skipped, so the result answers "largest FILES" precisely (unlike
//     `du`, whose directory totals would otherwise dominate the ranking).
//   - Unreadable directories (e.g. permission denied) are skipped rather than
//     aborting the whole scan; the count of skipped paths is reported so the
//     answer is honest about coverage.
//   - Only the top N entries are ever held in memory (a bounded min-heap), so the
//     scan is memory-safe even over very large trees.
//   - The request context is honored: a cancelled request stops the walk.
func runLargestFiles(ctx context.Context, _ registry.Capability, in map[string]any) (string, error) {
	dir, ok := getString(in, "dir")
	if !ok || dir == "" {
		dir = "."
	}
	count, ok := getInt(in, "count")
	if !ok || count <= 0 {
		count = defaultLargestFilesCount
	}
	if count > maxLargestFilesCount {
		count = maxLargestFilesCount
	}

	root, err := filepath.Abs(dir)
	if err != nil {
		return "", fmt.Errorf("largest_files: resolving %q: %w", dir, err)
	}
	info, err := os.Stat(root)
	if err != nil {
		return "", fmt.Errorf("largest_files: %w", err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("largest_files: %q is not a directory", root)
	}
	// filepath.WalkDir below does NOT follow a symlinked ROOT argument (it Lstats
	// root itself, sees a symlink rather than a directory, and silently walks
	// nothing) even though os.Stat above happily followed it to confirm it's a
	// directory. /etc, /tmp, and /var are all top-level symlinks on macOS, so
	// without this resolution "biggest files under /etc" would silently come
	// back empty instead of erroring. Falling back to the unresolved root on
	// error keeps the prior (broken-on-symlinks) behavior for anything
	// EvalSymlinks itself can't handle, rather than turning a walk failure into
	// a resolution failure.
	if resolved, err := filepath.EvalSymlinks(root); err == nil {
		root = resolved
	}

	top := &minHeap{}
	var scanned, skipped int

	walkErr := filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if cerr := ctx.Err(); cerr != nil {
			return cerr
		}
		if walkErr != nil {
			// Unreadable directory or entry (e.g. EACCES): skip and keep going.
			skipped++
			if d != nil && d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		fi, err := d.Info()
		if err != nil {
			skipped++
			return nil
		}
		scanned++
		entry := fileEntry{path: path, size: fi.Size()}
		switch {
		case top.Len() < count:
			heap.Push(top, entry)
		case (*top)[0].size < entry.size:
			// The new file is larger than the smallest we are keeping: evict it.
			heap.Pop(top)
			heap.Push(top, entry)
		}
		return nil
	})
	if walkErr != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		return "", fmt.Errorf("largest_files: scanning %q: %w", root, walkErr)
	}

	// Drain the min-heap into a slice, largest first.
	ranked := make([]fileEntry, top.Len())
	for i := len(ranked) - 1; i >= 0; i-- {
		ranked[i] = heap.Pop(top).(fileEntry)
	}
	return formatLargestFiles(root, ranked, scanned, skipped), nil
}

// formatLargestFiles renders the ranked files as a compact, human-readable list
// with a coverage footer.
func formatLargestFiles(root string, ranked []fileEntry, scanned, skipped int) string {
	if len(ranked) == 0 {
		return fmt.Sprintf("No files found under %s (scanned %d, skipped %d unreadable).", root, scanned, skipped)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Top %d largest files under %s:\n", len(ranked), root)
	for i, e := range ranked {
		fmt.Fprintf(&b, "%2d. %9s  %s\n", i+1, formatBytes(e.size), e.path)
	}
	fmt.Fprintf(&b, "(scanned %d files", scanned)
	if skipped > 0 {
		fmt.Fprintf(&b, ", skipped %d unreadable path(s)", skipped)
	}
	b.WriteString(")")
	return b.String()
}

// formatBytes renders a byte count as a compact human-readable size using
// 1024-based units (e.g. 1536 -> "1.5K", 3221225472 -> "3.0G").
func formatBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%dB", n)
	}
	div, exp := int64(unit), 0
	for v := n / unit; v >= unit; v /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f%c", float64(n)/float64(div), "KMGTPE"[exp])
}

// minHeap is a size-bounded min-heap of fileEntry keyed on size: the smallest
// file sits at the root so it can be evicted when a larger file is found. Keeping
// only the requested number of entries makes the scan memory-safe over arbitrarily
// large trees. It implements heap.Interface.
type minHeap []fileEntry

func (h minHeap) Len() int           { return len(h) }
func (h minHeap) Less(i, j int) bool { return h[i].size < h[j].size }
func (h minHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }

func (h *minHeap) Push(x any) { *h = append(*h, x.(fileEntry)) }

func (h *minHeap) Pop() any {
	old := *h
	n := len(old)
	last := old[n-1]
	*h = old[:n-1]
	return last
}
