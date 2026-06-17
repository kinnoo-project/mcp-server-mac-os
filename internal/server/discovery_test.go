// discovery_test.go verifies the two discovery tools: list_capabilities returns
// the full catalog (and filters by category), and describe_capability returns
// well-formed metadata plus a JSON Schema for a capability's parameters.
package server

import (
	"context"
	"encoding/json"
	"testing"
)

// TestListCapabilities_All confirms every registered capability is listed.
func TestListCapabilities_All(t *testing.T) {
	s := newTestServer(t)
	res, _, err := s.ListCapabilities(context.Background(), nil, ListCapabilitiesArgs{})
	if err != nil {
		t.Fatalf("ListCapabilities: %v", err)
	}
	var got []capabilitySummary
	if err := json.Unmarshal([]byte(textOf(res)), &got); err != nil {
		t.Fatalf("list output is not valid JSON: %v", err)
	}
	if len(got) != s.reg.Len() {
		t.Errorf("listed %d capabilities, want %d", len(got), s.reg.Len())
	}
	// Spot-check that a known capability is present with its metadata.
	var sawLs bool
	for _, c := range got {
		if c.Name == "ls" {
			sawLs = true
			if c.Category != "filesystem" || c.Reversibility != "read_only" {
				t.Errorf("ls summary has unexpected metadata: %+v", c)
			}
		}
	}
	if !sawLs {
		t.Error("ls missing from list_capabilities output")
	}
}

// TestListCapabilities_Filter confirms category filtering, including an empty
// result for an unknown category.
func TestListCapabilities_Filter(t *testing.T) {
	s := newTestServer(t)

	res, _, _ := s.ListCapabilities(context.Background(), nil, ListCapabilitiesArgs{Category: "filesystem"})
	var fs []capabilitySummary
	if err := json.Unmarshal([]byte(textOf(res)), &fs); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if len(fs) == 0 {
		t.Error("filesystem category should not be empty")
	}

	res, _, _ = s.ListCapabilities(context.Background(), nil, ListCapabilitiesArgs{Category: "nope"})
	var none []capabilitySummary
	if err := json.Unmarshal([]byte(textOf(res)), &none); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if len(none) != 0 {
		t.Errorf("unknown category should list nothing, got %d", len(none))
	}
}

// TestDescribeCapability_LS confirms describe returns metadata plus a parameter
// JSON Schema that includes the ls 'path' property.
func TestDescribeCapability_LS(t *testing.T) {
	s := newTestServer(t)
	res, _, err := s.DescribeCapability(context.Background(), nil, DescribeCapabilityArgs{Capability: "ls"})
	if err != nil {
		t.Fatalf("DescribeCapability: %v", err)
	}
	if res.IsError {
		t.Fatalf("describe ls errored: %s", textOf(res))
	}
	var desc map[string]any
	if err := json.Unmarshal([]byte(textOf(res)), &desc); err != nil {
		t.Fatalf("describe output is not valid JSON: %v", err)
	}
	if desc["name"] != "ls" {
		t.Errorf("describe name = %v, want ls", desc["name"])
	}
	params, ok := desc["parameters"].(map[string]any)
	if !ok {
		t.Fatal("describe output missing parameters schema object")
	}
	if params["type"] != "object" {
		t.Errorf("parameters schema type = %v, want object", params["type"])
	}
	props, ok := params["properties"].(map[string]any)
	if !ok || props["path"] == nil {
		t.Errorf("parameters schema missing 'path' property: %v", params)
	}
}

// TestDescribeCapability_Schema checks the schema renderer carries enum and
// required information through, using find (enum 'type', required 'path').
func TestDescribeCapability_FindSchema(t *testing.T) {
	s := newTestServer(t)
	res, _, _ := s.DescribeCapability(context.Background(), nil, DescribeCapabilityArgs{Capability: "find"})
	var desc map[string]any
	if err := json.Unmarshal([]byte(textOf(res)), &desc); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	params := desc["parameters"].(map[string]any)

	required, _ := params["required"].([]any)
	if !contains(required, "path") {
		t.Errorf("find schema should mark 'path' required, got %v", required)
	}
	props := params["properties"].(map[string]any)
	typeProp, ok := props["type"].(map[string]any)
	if !ok {
		t.Fatal("find schema missing 'type' property")
	}
	enum, ok := typeProp["enum"].([]any)
	if !ok || len(enum) != 3 {
		t.Errorf("find 'type' should expose a 3-value enum, got %v", typeProp["enum"])
	}
}

// TestDescribeCapability_Unknown confirms a structured error for a bad name.
func TestDescribeCapability_Unknown(t *testing.T) {
	s := newTestServer(t)
	res, _, _ := s.DescribeCapability(context.Background(), nil, DescribeCapabilityArgs{Capability: "teleport"})
	if !res.IsError {
		t.Fatal("expected IsError for unknown capability")
	}
}

func contains(haystack []any, needle string) bool {
	for _, h := range haystack {
		if s, ok := h.(string); ok && s == needle {
			return true
		}
	}
	return false
}
