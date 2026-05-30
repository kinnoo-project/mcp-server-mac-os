package main

import (
	"context"
	"fmt"
	"os"
	"strconv"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// textResult wraps a plain string into the dual-return shape expected by the
// MCP Go SDK tool handler contract.
func textResult(s string) (*mcp.CallToolResult, any, error) {
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: s}},
	}, nil, nil
}

// errorResult marks a CallToolResult as an error from the tool's perspective
// while still returning useful diagnostic text to the model.
func errorResult(format string, args ...any) (*mcp.CallToolResult, any, error) {
	return &mcp.CallToolResult{
		IsError: true,
		Content: []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf(format, args...)}},
	}, nil, nil
}

// runTool is the shared execution path for every read-only tool: it resolves a
// trusted absolute binary path, invokes it with the caller-provided
// pre-tokenized argument vector, and renders the result.
func runTool(ctx context.Context, binaryName string, args []string) (*mcp.CallToolResult, any, error) {
	bin, err := resolveBinary(binaryName)
	if err != nil {
		return errorResult("could not resolve %s: %v", binaryName, err)
	}
	res, err := runCommand(ctx, bin, args...)
	if err != nil {
		return errorResult("error executing %s: %v", binaryName, err)
	}
	return textResult(formatRunResult(res))
}

// ---------------------------------------------------------------------------
// ls
// ---------------------------------------------------------------------------

type LsArgs struct {
	Path       string `json:"path,omitempty" jsonschema:"Directory or file path to list. Defaults to the server's current working directory. Supports leading ~ for the user's home."`
	All        bool   `json:"all,omitempty" jsonschema:"Include entries starting with a dot (passes -A)."`
	Long       bool   `json:"long,omitempty" jsonschema:"Use the long listing format with permissions, owner, size, and mtime (passes -l)."`
	Human      bool   `json:"human_readable,omitempty" jsonschema:"Print sizes in human-readable units when combined with long (passes -h)."`
	Recursive  bool   `json:"recursive,omitempty" jsonschema:"Recursively list subdirectories (passes -R). Use sparingly on deep trees."`
	SortBySize bool   `json:"sort_by_size,omitempty" jsonschema:"Sort by file size, largest first (passes -S)."`
	SortByTime bool   `json:"sort_by_time,omitempty" jsonschema:"Sort by modification time, newest first (passes -t)."`
}

func lsHandler(ctx context.Context, _ *mcp.CallToolRequest, in LsArgs) (*mcp.CallToolResult, any, error) {
	flags := []string{}
	if in.All {
		flags = append(flags, "-A")
	}
	if in.Long {
		flags = append(flags, "-l")
	}
	if in.Human {
		flags = append(flags, "-h")
	}
	if in.Recursive {
		flags = append(flags, "-R")
	}
	if in.SortBySize {
		flags = append(flags, "-S")
	}
	if in.SortByTime {
		flags = append(flags, "-t")
	}

	target := in.Path
	if target == "" {
		target = "."
	}
	expanded, err := expandUserPath(target)
	if err != nil {
		return errorResult("could not expand path %q: %v", target, err)
	}
	args := append(flags, "--", expanded)
	return runTool(ctx, "ls", args)
}

// ---------------------------------------------------------------------------
// pwd
// ---------------------------------------------------------------------------

type PwdArgs struct{}

func pwdHandler(_ context.Context, _ *mcp.CallToolRequest, _ PwdArgs) (*mcp.CallToolResult, any, error) {
	wd, err := os.Getwd()
	if err != nil {
		return errorResult("could not determine working directory: %v", err)
	}
	return textResult(wd)
}

// ---------------------------------------------------------------------------
// file
// ---------------------------------------------------------------------------

type FileArgs struct {
	Paths []string `json:"paths" jsonschema:"One or more file paths to identify. Supports leading ~ for the user's home."`
}

func fileHandler(ctx context.Context, _ *mcp.CallToolRequest, in FileArgs) (*mcp.CallToolResult, any, error) {
	if len(in.Paths) == 0 {
		return errorResult("file: at least one path is required")
	}
	expanded := make([]string, 0, len(in.Paths)+1)
	expanded = append(expanded, "--")
	for _, p := range in.Paths {
		ep, err := expandUserPath(p)
		if err != nil {
			return errorResult("could not expand path %q: %v", p, err)
		}
		expanded = append(expanded, ep)
	}
	return runTool(ctx, "file", expanded)
}

// ---------------------------------------------------------------------------
// grep
// ---------------------------------------------------------------------------

type GrepArgs struct {
	Pattern        string   `json:"pattern" jsonschema:"Regular expression pattern to search for. Always passed via -e to avoid being interpreted as a flag."`
	Paths          []string `json:"paths" jsonschema:"One or more file or directory paths to search. Supports leading ~ for the user's home."`
	Recursive      bool     `json:"recursive,omitempty" jsonschema:"Recurse into directories (passes -r)."`
	IgnoreCase     bool     `json:"ignore_case,omitempty" jsonschema:"Case-insensitive match (passes -i)."`
	LineNumbers    bool     `json:"line_numbers,omitempty" jsonschema:"Prefix matches with line numbers (passes -n). Defaults to true."`
	FilesWithMatch bool     `json:"files_with_matches,omitempty" jsonschema:"List only the names of files containing matches (passes -l)."`
	Count          bool     `json:"count,omitempty" jsonschema:"Print only a count of matching lines per file (passes -c)."`
	MaxCount       int      `json:"max_count,omitempty" jsonschema:"Stop after this many matches per file (passes -m)."`
	Include        string   `json:"include,omitempty" jsonschema:"Restrict recursive search to files matching this glob (passes --include=)."`
	Context        int      `json:"context,omitempty" jsonschema:"Print N lines of leading and trailing context around each match (passes -C N)."`
}

func grepHandler(ctx context.Context, _ *mcp.CallToolRequest, in GrepArgs) (*mcp.CallToolResult, any, error) {
	if in.Pattern == "" {
		return errorResult("grep: pattern must not be empty")
	}
	if len(in.Paths) == 0 {
		return errorResult("grep: at least one path is required")
	}
	args := []string{"-I"} // skip binary files; keeps output meaningful for an LLM.
	if in.Recursive {
		args = append(args, "-r")
	}
	if in.IgnoreCase {
		args = append(args, "-i")
	}
	// Default to line numbers unless the caller explicitly opts out via a
	// summary mode (FilesWithMatch / Count).
	if in.LineNumbers || (!in.FilesWithMatch && !in.Count) {
		args = append(args, "-n")
	}
	if in.FilesWithMatch {
		args = append(args, "-l")
	}
	if in.Count {
		args = append(args, "-c")
	}
	if in.MaxCount > 0 {
		args = append(args, "-m", strconv.Itoa(in.MaxCount))
	}
	if in.Context > 0 {
		args = append(args, "-C", strconv.Itoa(in.Context))
	}
	if in.Include != "" {
		args = append(args, "--include="+in.Include)
	}
	args = append(args, "-e", in.Pattern, "--")
	for _, p := range in.Paths {
		ep, err := expandUserPath(p)
		if err != nil {
			return errorResult("could not expand path %q: %v", p, err)
		}
		args = append(args, ep)
	}
	return runTool(ctx, "grep", args)
}

// ---------------------------------------------------------------------------
// du
// ---------------------------------------------------------------------------

type DuArgs struct {
	Paths    []string `json:"paths" jsonschema:"One or more directory or file paths to measure. Supports leading ~ for the user's home."`
	Human    bool     `json:"human_readable,omitempty" jsonschema:"Print sizes in human-readable units like 1.2K, 47M, 3G (passes -h)."`
	Summary  bool     `json:"summary,omitempty" jsonschema:"Display only a total for each argument (passes -s)."`
	MaxDepth int      `json:"max_depth,omitempty" jsonschema:"Limit reporting to entries at most N levels deep (passes -d N). Ignored when summary is true."`
	Total    bool     `json:"total,omitempty" jsonschema:"Produce a grand total across all arguments (passes -c)."`
}

func duHandler(ctx context.Context, _ *mcp.CallToolRequest, in DuArgs) (*mcp.CallToolResult, any, error) {
	if len(in.Paths) == 0 {
		return errorResult("du: at least one path is required")
	}
	args := []string{}
	if in.Human {
		args = append(args, "-h")
	}
	if in.Summary {
		args = append(args, "-s")
	} else if in.MaxDepth > 0 {
		args = append(args, "-d", strconv.Itoa(in.MaxDepth))
	}
	if in.Total {
		args = append(args, "-c")
	}
	args = append(args, "--")
	for _, p := range in.Paths {
		ep, err := expandUserPath(p)
		if err != nil {
			return errorResult("could not expand path %q: %v", p, err)
		}
		args = append(args, ep)
	}
	return runTool(ctx, "du", args)
}

// ---------------------------------------------------------------------------
// find
// ---------------------------------------------------------------------------

type FindArgs struct {
	Path         string   `json:"path" jsonschema:"Root directory to search under. Supports leading ~ for the user's home."`
	NamePattern  string   `json:"name_pattern,omitempty" jsonschema:"Glob pattern to match the base file name, e.g. *.png (passes -name)."`
	INamePattern string   `json:"iname_pattern,omitempty" jsonschema:"Case-insensitive variant of name_pattern (passes -iname)."`
	Type         string   `json:"type,omitempty" jsonschema:"Restrict to a file type. One of f (regular file), d (directory), l (symlink)."`
	MaxDepth     int      `json:"max_depth,omitempty" jsonschema:"Maximum directory depth to descend (passes -maxdepth)."`
	MinDepth     int      `json:"min_depth,omitempty" jsonschema:"Minimum directory depth before matching begins (passes -mindepth)."`
	Extensions   []string `json:"extensions,omitempty" jsonschema:"Convenience filter: list of file extensions without the leading dot (e.g. [\"png\",\"jpg\"]). OR-combined and merged with name_pattern via -o."`
}

// allowedFindTypes is a deliberately tiny whitelist. -exec, -delete, -prune,
// and other dangerous primaries are not exposed.
var allowedFindTypes = map[string]bool{"f": true, "d": true, "l": true}

func findHandler(ctx context.Context, _ *mcp.CallToolRequest, in FindArgs) (*mcp.CallToolResult, any, error) {
	if in.Path == "" {
		return errorResult("find: path is required")
	}
	root, err := expandUserPath(in.Path)
	if err != nil {
		return errorResult("could not expand path %q: %v", in.Path, err)
	}
	args := []string{root}
	// -maxdepth/-mindepth are global options and must come before tests in BSD find.
	if in.MaxDepth > 0 {
		args = append(args, "-maxdepth", strconv.Itoa(in.MaxDepth))
	}
	if in.MinDepth > 0 {
		args = append(args, "-mindepth", strconv.Itoa(in.MinDepth))
	}
	if in.Type != "" {
		if !allowedFindTypes[in.Type] {
			return errorResult("find: type %q not allowed; use one of f, d, l", in.Type)
		}
		args = append(args, "-type", in.Type)
	}

	// Combine name_pattern, iname_pattern, and extensions into a single
	// OR-grouped expression so callers can ask for "all images" with a
	// natural list of extensions.
	var nameTerms [][]string
	if in.NamePattern != "" {
		nameTerms = append(nameTerms, []string{"-name", in.NamePattern})
	}
	if in.INamePattern != "" {
		nameTerms = append(nameTerms, []string{"-iname", in.INamePattern})
	}
	for _, ext := range in.Extensions {
		// Validate: extensions must not contain glob/path metacharacters
		// beyond ordinary filename letters/digits/_-. This keeps the OR
		// expression tightly bounded.
		if !isSafeExtension(ext) {
			return errorResult("find: extension %q contains disallowed characters", ext)
		}
		nameTerms = append(nameTerms, []string{"-iname", "*." + ext})
	}
	if len(nameTerms) > 0 {
		args = append(args, "(")
		for i, term := range nameTerms {
			if i > 0 {
				args = append(args, "-o")
			}
			args = append(args, term...)
		}
		args = append(args, ")")
	}
	return runTool(ctx, "find", args)
}

func isSafeExtension(ext string) bool {
	if ext == "" || len(ext) > 16 {
		return false
	}
	for _, r := range ext {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		case r == '_' || r == '-':
		default:
			return false
		}
	}
	return true
}

// ---------------------------------------------------------------------------
// stat
// ---------------------------------------------------------------------------

type StatArgs struct {
	Paths []string `json:"paths" jsonschema:"One or more file or directory paths to stat. Supports leading ~ for the user's home."`
}

func statHandler(ctx context.Context, _ *mcp.CallToolRequest, in StatArgs) (*mcp.CallToolResult, any, error) {
	if len(in.Paths) == 0 {
		return errorResult("stat: at least one path is required")
	}
	args := []string{"--"}
	for _, p := range in.Paths {
		ep, err := expandUserPath(p)
		if err != nil {
			return errorResult("could not expand path %q: %v", p, err)
		}
		args = append(args, ep)
	}
	return runTool(ctx, "stat", args)
}

// ---------------------------------------------------------------------------
// wc - useful for "how many files / lines / bytes" type questions.
// ---------------------------------------------------------------------------

type WcArgs struct {
	Paths []string `json:"paths" jsonschema:"One or more file paths to count."`
	Lines bool     `json:"lines,omitempty" jsonschema:"Count lines (passes -l)."`
	Words bool     `json:"words,omitempty" jsonschema:"Count words (passes -w)."`
	Bytes bool     `json:"bytes,omitempty" jsonschema:"Count bytes (passes -c)."`
	Chars bool     `json:"chars,omitempty" jsonschema:"Count characters (passes -m)."`
}

func wcHandler(ctx context.Context, _ *mcp.CallToolRequest, in WcArgs) (*mcp.CallToolResult, any, error) {
	if len(in.Paths) == 0 {
		return errorResult("wc: at least one path is required")
	}
	args := []string{}
	if in.Lines {
		args = append(args, "-l")
	}
	if in.Words {
		args = append(args, "-w")
	}
	if in.Bytes {
		args = append(args, "-c")
	}
	if in.Chars {
		args = append(args, "-m")
	}
	args = append(args, "--")
	for _, p := range in.Paths {
		ep, err := expandUserPath(p)
		if err != nil {
			return errorResult("could not expand path %q: %v", p, err)
		}
		args = append(args, ep)
	}
	return runTool(ctx, "wc", args)
}

// registerTools wires every read-only tool onto the provided MCP server. It is
// extracted from main() so tests can drive the same registration logic.
func registerTools(server *mcp.Server) {
	mcp.AddTool(server, &mcp.Tool{
		Name: "ls",
		Description: "List the contents of a directory or describe a single file. " +
			"Read-only wrapper around macOS /bin/ls. Use this for navigation-style questions " +
			"like \"what's in my Downloads folder?\".",
	}, lsHandler)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "pwd",
		Description: "Return the absolute path of the MCP server's current working directory.",
	}, pwdHandler)

	mcp.AddTool(server, &mcp.Tool{
		Name: "file",
		Description: "Identify the type of one or more files using macOS /usr/bin/file " +
			"(e.g. \"PNG image data\", \"ASCII text\", \"Mach-O 64-bit executable\").",
	}, fileHandler)

	mcp.AddTool(server, &mcp.Tool{
		Name: "grep",
		Description: "Search files or directories for a regular expression. Read-only " +
			"wrapper around macOS /usr/bin/grep. Binary files are skipped (-I).",
	}, grepHandler)

	mcp.AddTool(server, &mcp.Tool{
		Name: "du",
		Description: "Report disk usage for one or more paths using macOS /usr/bin/du. " +
			"Use summary=true and human_readable=true for quick \"how big is this folder?\" questions.",
	}, duHandler)

	mcp.AddTool(server, &mcp.Tool{
		Name: "find",
		Description: "Locate files by name, extension, type, or depth using macOS /usr/bin/find. " +
			"Read-only: -exec, -delete, and -prune are not exposed. Useful for prompts like " +
			"\"list all PNG and JPG files under ~/Pictures\".",
	}, findHandler)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "stat",
		Description: "Show detailed metadata (size, mtime, permissions, owner) for one or more paths.",
	}, statHandler)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "wc",
		Description: "Count lines, words, characters, or bytes in one or more files.",
	}, wcHandler)
}
