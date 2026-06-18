// Command runevals drives real Anthropic model calls through the MCP server's
// actual tool surface and checks the model's tool-call behavior against a set
// of declarative case files — see internal/evals and docs/TESTS.md.
//
// Unlike `go test ./...`, this is NOT free or deterministic: every case that
// isn't skipped by -dry-run makes one or more real, billed Anthropic API
// calls. Run with -dry-run while iterating on case files; only drop -dry-run
// when you intend to spend API credits on a real pass.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"mcp-server-mac-os/internal/evals"
)

func main() {
	os.Exit(run())
}

func run() int {
	casesDir := flag.String("cases", "evals/cases", "directory of *.json eval case files")
	model := flag.String("model", "claude-sonnet-4-6", "Anthropic model ID to evaluate")
	only := flag.String("only", "", "run a single case by id (for cheap iteration)")
	dryRun := flag.Bool("dry-run", false, "load cases and resolve tool schemas only; make no API calls")
	flag.Parse()

	ctx := context.Background()

	if *dryRun {
		cases, toolNames, err := evals.LoadAndDescribe(ctx, evals.Config{CasesDir: *casesDir})
		if err != nil {
			fmt.Fprintf(os.Stderr, "runevals: %v\n", err)
			return 1
		}
		fmt.Printf("dry run: %d case(s) loaded from %s, %d tool(s) resolved: %v\n", len(cases), *casesDir, len(toolNames), toolNames)
		for _, c := range cases {
			if *only != "" && c.ID != *only {
				continue
			}
			turns, err := c.ResolvedTurns()
			if err != nil {
				fmt.Fprintf(os.Stderr, "runevals: case %q: %v\n", c.ID, err)
				return 1
			}
			fmt.Printf("  - %s (%d turn(s))\n", c.ID, len(turns))
		}
		fmt.Println("dry run OK; no API calls were made.")
		return 0
	}

	apiKey := os.Getenv("ANTHROPIC_API_KEY")
	if apiKey == "" {
		fmt.Fprintln(os.Stderr, "runevals: ANTHROPIC_API_KEY is not set. Set it to run live evals (these make real, billed API calls), or pass -dry-run to validate cases for free.")
		return 1
	}

	results, err := evals.RunAll(ctx, evals.Config{
		APIKey:   apiKey,
		Model:    *model,
		CasesDir: *casesDir,
		Only:     *only,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "runevals: %v\n", err)
		return 1
	}

	failed := 0
	for _, r := range results {
		if r.Passed() {
			fmt.Printf("PASS  %s\n", r.ID)
		} else {
			failed++
			fmt.Printf("FAIL  %s: %v\n", r.ID, r.Err)
		}
	}
	fmt.Printf("\n%d/%d passed\n", len(results)-failed, len(results))
	if failed > 0 {
		return 1
	}
	return 0
}
