// builders_filesystem.go holds the named argv builders for filesystem utilities
// whose command grammar is too irregular for the generic builder:
//
//   - find: the search root must come FIRST (before any test), and name filters
//     are combined into a single OR-grouped expression — neither of which the
//     generic "flags then -- then positionals" layout can express.
//   - grep: the pattern must be passed via -e (so a pattern beginning with "-"
//     is never read as a flag), and line numbering is on by default UNLESS a
//     summary mode is requested — conditional logic the declarative builder
//     cannot represent.
//
// Both builders read already-validated parameters from the normalized map, so
// they focus purely on assembling a correct, fully-tokenized argument vector.
package engine

import (
	"fmt"
	"strconv"

	"mcp-server-mac-os/internal/registry"
)

// buildFind assembles the argument vector for find.
//
// Layout: <root> [-maxdepth N] [-mindepth N] [-type X] [ ( <name tests> ) ].
// The global depth options must precede the tests in BSD find, and the name
// tests (-name / -iname / per-extension globs) are wrapped in a parenthesized
// OR group so callers can ask for, say, "all png or jpg files" naturally.
func buildFind(c registry.Capability, in map[string]any) ([]string, error) {
	root, _ := getString(in, "path")
	// Injection guard: in find, a leading "-" on the first token is parsed as
	// part of the expression rather than a path. Refuse it and tell the caller
	// to disambiguate with "./".
	if len(root) > 0 && root[0] == '-' {
		return nil, fmt.Errorf("find: path %q begins with '-' and is not allowed; prefix it with ./", root)
	}

	args := []string{root}
	if v, ok := getInt(in, "max_depth"); ok && v > 0 {
		args = append(args, "-maxdepth", strconv.Itoa(v))
	}
	if v, ok := getInt(in, "min_depth"); ok && v > 0 {
		args = append(args, "-mindepth", strconv.Itoa(v))
	}
	// type is an enum parameter, so the normalizer has already confirmed it is
	// one of f/d/l before we get here.
	if t, ok := getString(in, "type"); ok && t != "" {
		args = append(args, "-type", t)
	}

	// Collect the individual name tests, then OR-group them.
	var nameTests [][]string
	if v, ok := getString(in, "name_pattern"); ok && v != "" {
		nameTests = append(nameTests, []string{"-name", v})
	}
	if v, ok := getString(in, "iname_pattern"); ok && v != "" {
		nameTests = append(nameTests, []string{"-iname", v})
	}
	if exts, ok := getStringList(in, "extensions"); ok {
		for _, ext := range exts {
			// Keep the OR expression tightly bounded: extensions must look like
			// ordinary filename suffixes, never glob/path/shell metacharacters.
			if !isSafeExtension(ext) {
				return nil, fmt.Errorf("find: extension %q contains disallowed characters", ext)
			}
			nameTests = append(nameTests, []string{"-iname", "*." + ext})
		}
	}
	if len(nameTests) > 0 {
		args = append(args, "(")
		for i, test := range nameTests {
			if i > 0 {
				args = append(args, "-o")
			}
			args = append(args, test...)
		}
		args = append(args, ")")
	}
	return args, nil
}

// isSafeExtension reports whether ext is a plain filename suffix (letters,
// digits, underscore, hyphen; 1–16 chars). This blocks any attempt to smuggle
// glob or shell metacharacters into the find expression via the extensions list.
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

// buildGrep assembles the argument vector for grep.
//
// -I (skip binary files) is always passed so output stays meaningful for an LLM.
// Line numbers are added by default unless the caller selects a summary mode
// (files_with_matches or count), matching how a human typically wants grep to
// behave. The pattern is always passed via -e, and "--" separates options from
// the path operands.
func buildGrep(c registry.Capability, in map[string]any) ([]string, error) {
	args := []string{"-I"}
	if getBool(in, "recursive") {
		args = append(args, "-r")
	}
	if getBool(in, "ignore_case") {
		args = append(args, "-i")
	}

	filesWithMatches := getBool(in, "files_with_matches")
	count := getBool(in, "count")
	if getBool(in, "line_numbers") || (!filesWithMatches && !count) {
		args = append(args, "-n")
	}
	if filesWithMatches {
		args = append(args, "-l")
	}
	if count {
		args = append(args, "-c")
	}
	if v, ok := getInt(in, "max_count"); ok && v > 0 {
		args = append(args, "-m", strconv.Itoa(v))
	}
	if v, ok := getInt(in, "context"); ok && v > 0 {
		args = append(args, "-C", strconv.Itoa(v))
	}
	if v, ok := getString(in, "include"); ok && v != "" {
		args = append(args, "--include="+v)
	}

	pattern, _ := getString(in, "pattern")
	args = append(args, "-e", pattern, "--")

	paths, _ := getStringList(in, "paths")
	args = append(args, paths...)
	return args, nil
}
