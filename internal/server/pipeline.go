// pipeline.go exposes the `pipeline` tool: a third fixed, cross-cutting tool
// (alongside execute/undo) that composes several read-only capabilities by
// name, piping each stage's output into the next. It is cross-cutting rather
// than scoped to one domain tool because it names capabilities directly
// (registry.Lookup is global) and may, in principle, chain capabilities from
// different categories. The actual execution — argv assembly, the policy
// trust check, stdin wiring between stages — lives in
// internal/engine/pipeline.go; this file only resolves stage names to
// capabilities and renders the tool's description.
package server

import (
	"context"
	"sort"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"mcp-server-mac-os/internal/engine"
)

// PipelineStageArgs names one stage: a capability and its parameters.
type PipelineStageArgs struct {
	Capability string         `json:"capability" jsonschema:"required,Name of an eligible read-only capability to run as this stage; see this tool's description for the current eligible list."`
	Params     map[string]any `json:"params,omitempty" jsonschema:"Parameters for this stage's capability. For a stage after the first, omit its positional file/path parameter (where the capability allows it) to have it consume the previous stage's output instead of a named file."`
}

// PipelineArgs is the input to the pipeline tool: an ordered list of stages.
type PipelineArgs struct {
	Stages []PipelineStageArgs `json:"stages" jsonschema:"required,Ordered list of stages to run; each stage's output feeds the next stage's input."`
}

// pipelineToolDescription lists the capabilities currently eligible for
// pipeline use, computed live from the registry via the same
// engine.SupportsPipeline check Pipeline itself enforces — so the description
// can never drift from what a call will actually accept — and steers the
// model toward a single named operation first (see README.md's "Nudging the
// model toward named capabilities first").
func (s *Server) pipelineToolDescription() string {
	var eligible []string
	for _, c := range s.reg.All() {
		if s.eng.SupportsPipeline(c) {
			eligible = append(eligible, c.Name)
		}
	}
	sort.Strings(eligible)

	var b strings.Builder
	b.WriteString("Compose several read-only capabilities into one pipeline, piping each stage's output into the next " +
		"(the server-side equivalent of a unix \"a | b | c\" pipe — every stage is read-only, so there is no mutation " +
		"and nothing to undo).\n\n")
	b.WriteString("IMPORTANT: check the relevant domain tool's operation menu first and prefer a single named " +
		"operation whenever one already covers the request (for example, use filesystem's largest_files for " +
		"\"biggest files\" questions — do not build a pipeline for something a named operation already does). " +
		"Only use this tool to combine operations when no existing one already covers the request.\n\n")
	b.WriteString("Eligible capabilities (read-only; may appear as a stage): " + strings.Join(eligible, ", ") + ".\n\n")
	b.WriteString("For a stage after the first, omit its positional file/path parameter to have it consume the " +
		"previous stage's output instead of a named file (supported by: wc, grep, sort, head).")
	return b.String()
}

// Pipeline resolves each stage's named capability and runs the resolved
// stages through the engine. Every failure mode — an empty stage list, an
// unknown capability name, or a capability the engine would refuse (a builtin
// or a mutator) — is reported as a structured error naming the offending
// stage, never a panic; the engine itself re-validates eligibility
// defensively, but resolving here first gives a clearer error message.
func (s *Server) Pipeline(ctx context.Context, _ *mcp.CallToolRequest, in PipelineArgs) (*mcp.CallToolResult, any, error) {
	if len(in.Stages) == 0 {
		return errorResult("pipeline: 'stages' must contain at least one stage")
	}
	resolved := make([]engine.PipelineStage, len(in.Stages))
	for i, stage := range in.Stages {
		if stage.Capability == "" {
			return errorResult("pipeline: stage %d: 'capability' is required", i+1)
		}
		c, ok := s.reg.Lookup(stage.Capability)
		if !ok {
			return errorResult("pipeline: stage %d: unknown capability %q", i+1, stage.Capability)
		}
		if !s.eng.SupportsPipeline(c) {
			return errorResult("pipeline: stage %d: capability %q cannot be used in a pipeline (must be a read-only, binary-backed capability)", i+1, stage.Capability)
		}
		resolved[i] = engine.PipelineStage{Capability: c, Params: stage.Params}
	}

	out, err := s.eng.RunPipeline(ctx, resolved)
	if err != nil {
		return errorResult("pipeline: %v", err)
	}
	return textResult(out)
}
