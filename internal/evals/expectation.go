// expectation.go checks a Turn's Expectation against what actually happened
// during that turn. It is pure (no network, no MCP) so it can be unit tested
// against a hand-built TurnOutcome — see runner_test.go — independent of the
// agent loop in runner.go that produces a real TurnOutcome from a live model.
package evals

import (
	"fmt"
	"strings"
)

// TurnOutcome records what happened during one turn: which tools were called
// (in call order, possibly with duplicates), the "operation" argument seen on
// each call to a domain tool, and the turn's accumulated text response.
type TurnOutcome struct {
	ToolsCalled      []string
	OperationsByTool map[string][]string
	FinalText        string
	// ErroredTools lists the names of tools whose result this turn came back as
	// an error block (tool-level error or transport failure — see
	// toolResultText). It backs the tool_succeeds assertion: a tool can be
	// "called" yet have failed, which selection-only checks miss.
	ErroredTools []string
}

// erroredSet returns o.ErroredTools as a set for membership checks.
func (o TurnOutcome) erroredSet() map[string]bool {
	set := make(map[string]bool, len(o.ErroredTools))
	for _, name := range o.ErroredTools {
		set[name] = true
	}
	return set
}

// calledSet returns o.ToolsCalled as a set for membership checks.
func (o TurnOutcome) calledSet() map[string]bool {
	set := make(map[string]bool, len(o.ToolsCalled))
	for _, name := range o.ToolsCalled {
		set[name] = true
	}
	return set
}

// CheckExpectation reports nil if outcome satisfies exp, or a descriptive
// error naming exactly which assertion failed and what was actually observed
// (so a failing eval is debuggable from the error message alone, without
// needing to re-run with extra logging).
func CheckExpectation(exp Expectation, outcome TurnOutcome) error {
	called := outcome.calledSet()

	switch {
	case exp.Tool == "" && len(outcome.ToolsCalled) > 0:
		return fmt.Errorf("expected no tool call, but called: %v", outcome.ToolsCalled)
	case exp.Tool != "" && !called[exp.Tool]:
		return fmt.Errorf("expected tool %q to be called, but it wasn't (called: %v)", exp.Tool, outcome.ToolsCalled)
	}

	if exp.Operation != "" {
		ops := outcome.OperationsByTool[exp.Tool]
		if !contains(ops, exp.Operation) {
			return fmt.Errorf("expected tool %q to be called with operation %q, but saw operations: %v", exp.Tool, exp.Operation, ops)
		}
	}

	for _, forbidden := range exp.ForbidTools {
		if called[forbidden] {
			return fmt.Errorf("tool %q must not be called this turn, but was (called: %v)", forbidden, outcome.ToolsCalled)
		}
	}

	for _, substr := range exp.TextContains {
		if !strings.Contains(outcome.FinalText, substr) {
			return fmt.Errorf("expected response text to contain %q, got: %q", substr, outcome.FinalText)
		}
	}

	// tool_succeeds: the expected tool was called (verified above) but must also
	// not have come back as an error block. This catches "right op, but it
	// failed" — the model narrating around a move that actually errored.
	if exp.ToolSucceeds != nil && *exp.ToolSucceeds {
		if outcome.erroredSet()[exp.Tool] {
			return fmt.Errorf("expected tool %q to succeed, but its result was an error block", exp.Tool)
		}
	}

	return nil
}

func contains(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}
