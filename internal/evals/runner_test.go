// runner_test.go exercises CheckExpectation against hand-built TurnOutcome
// values — no network, no MCP, no API key needed. This is what actually
// proves the harness's assertion logic is correct; the live agent loop in
// runner.go is only exercisable against a real model (see -dry-run for the
// no-cost path that exercises everything else).
package evals

import "testing"

func TestCheckExpectation_ToolCalledAsExpected(t *testing.T) {
	exp := Expectation{Tool: "filesystem", Operation: "ls"}
	outcome := TurnOutcome{
		ToolsCalled:      []string{"filesystem"},
		OperationsByTool: map[string][]string{"filesystem": {"ls"}},
	}
	if err := CheckExpectation(exp, outcome); err != nil {
		t.Errorf("expected no error, got %v", err)
	}
}

func TestCheckExpectation_WrongToolFails(t *testing.T) {
	exp := Expectation{Tool: "filesystem"}
	outcome := TurnOutcome{ToolsCalled: []string{"preferences"}}
	if err := CheckExpectation(exp, outcome); err == nil {
		t.Fatal("expected an error when the expected tool wasn't called")
	}
}

func TestCheckExpectation_WrongOperationFails(t *testing.T) {
	exp := Expectation{Tool: "filesystem", Operation: "mkdir"}
	outcome := TurnOutcome{
		ToolsCalled:      []string{"filesystem"},
		OperationsByTool: map[string][]string{"filesystem": {"ls"}},
	}
	if err := CheckExpectation(exp, outcome); err == nil {
		t.Fatal("expected an error when the operation doesn't match")
	}
}

func TestCheckExpectation_NoToolCallExpectedButOneHappened(t *testing.T) {
	exp := Expectation{} // empty Tool means "expect no call"
	outcome := TurnOutcome{ToolsCalled: []string{"filesystem"}}
	if err := CheckExpectation(exp, outcome); err == nil {
		t.Fatal("expected an error when a tool was called but none was expected")
	}
}

func TestCheckExpectation_NoToolCallExpectedAndNoneHappened(t *testing.T) {
	exp := Expectation{TextContains: []string{"cannot"}}
	outcome := TurnOutcome{FinalText: "I cannot do that."}
	if err := CheckExpectation(exp, outcome); err != nil {
		t.Errorf("expected no error, got %v", err)
	}
}

// TestCheckExpectation_ForbidToolsCatchesAutoConfirm is the harness's core
// safety check: staging a mutation and then immediately calling execute in the
// same turn (auto-confirming without a human turn in between) must fail.
func TestCheckExpectation_ForbidToolsCatchesAutoConfirm(t *testing.T) {
	exp := Expectation{Tool: "filesystem", Operation: "mkdir", ForbidTools: []string{"execute"}}
	outcome := TurnOutcome{
		ToolsCalled:      []string{"filesystem", "execute"},
		OperationsByTool: map[string][]string{"filesystem": {"mkdir"}},
	}
	err := CheckExpectation(exp, outcome)
	if err == nil {
		t.Fatal("expected an error: execute was called in the same turn as staging")
	}
}

func TestCheckExpectation_ForbidToolsPassesWhenAbsent(t *testing.T) {
	exp := Expectation{Tool: "filesystem", ForbidTools: []string{"execute"}}
	outcome := TurnOutcome{ToolsCalled: []string{"filesystem"}}
	if err := CheckExpectation(exp, outcome); err != nil {
		t.Errorf("expected no error, got %v", err)
	}
}

func TestCheckExpectation_TextContainsAllRequiredSubstrings(t *testing.T) {
	exp := Expectation{TextContains: []string{"cannot", "supported"}}
	outcome := TurnOutcome{FinalText: "That action is not supported; I cannot do it."}
	if err := CheckExpectation(exp, outcome); err != nil {
		t.Errorf("expected no error, got %v", err)
	}
}

func TestCheckExpectation_TextContainsMissingSubstringFails(t *testing.T) {
	exp := Expectation{TextContains: []string{"nonexistent-phrase"}}
	outcome := TurnOutcome{FinalText: "Sure, I'll do that."}
	if err := CheckExpectation(exp, outcome); err == nil {
		t.Fatal("expected an error when a required substring is missing")
	}
}

// TestCheckExpectation_ToolSucceedsPasses covers the happy path: the expected
// tool was called and did not error, so tool_succeeds:true is satisfied.
func TestCheckExpectation_ToolSucceedsPasses(t *testing.T) {
	yes := true
	exp := Expectation{Tool: "filesystem", Operation: "move", ToolSucceeds: &yes}
	outcome := TurnOutcome{
		ToolsCalled:      []string{"filesystem"},
		OperationsByTool: map[string][]string{"filesystem": {"move"}},
	}
	if err := CheckExpectation(exp, outcome); err != nil {
		t.Errorf("expected no error, got %v", err)
	}
}

// TestCheckExpectation_ToolSucceedsCatchesErroredTool is the point of the field:
// the model called the right tool/op, but the call came back as an error block.
// Selection-only checks miss this; tool_succeeds:true must fail it.
func TestCheckExpectation_ToolSucceedsCatchesErroredTool(t *testing.T) {
	yes := true
	exp := Expectation{Tool: "filesystem", Operation: "move", ToolSucceeds: &yes}
	outcome := TurnOutcome{
		ToolsCalled:      []string{"filesystem"},
		OperationsByTool: map[string][]string{"filesystem": {"move"}},
		ErroredTools:     []string{"filesystem"},
	}
	if err := CheckExpectation(exp, outcome); err == nil {
		t.Fatal("expected an error: the expected tool's result was an error block")
	}
}

// TestCheckExpectation_ToolSucceedsUnsetIgnoresErrors confirms the pointer
// semantics: with the field omitted, an errored tool does not fail the case
// (a case that doesn't opt into the check keeps its old behavior).
func TestCheckExpectation_ToolSucceedsUnsetIgnoresErrors(t *testing.T) {
	exp := Expectation{Tool: "filesystem"} // ToolSucceeds nil
	outcome := TurnOutcome{
		ToolsCalled:  []string{"filesystem"},
		ErroredTools: []string{"filesystem"},
	}
	if err := CheckExpectation(exp, outcome); err != nil {
		t.Errorf("expected no error when tool_succeeds is unset, got %v", err)
	}
}
