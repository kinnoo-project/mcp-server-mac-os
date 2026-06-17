// schema.go renders a capability's parameter list into a JSON Schema object.
//
// This is how describe_capability hands the model precise, per-capability
// parameter rigor on demand. The capability's ParamSpec list remains the single
// source of truth; this file is one of its renderings (the human-facing menu in
// menu.go is another). Because the schema is derived, not hand-maintained, it
// can never drift from the validation the engine actually enforces.
package server

import "mcp-server-mac-os/internal/registry"

// paramsSchema renders a capability's parameters as a JSON Schema "object". The
// result is a plain map so it serializes directly to JSON in describe_capability
// without coupling us to any schema library. additionalProperties is false to
// mirror the engine, which rejects unknown parameters.
func paramsSchema(c registry.Capability) map[string]any {
	properties := make(map[string]any, len(c.Params))
	var required []string

	for _, p := range c.Params {
		properties[p.Name] = propertySchema(p)
		if p.Required {
			required = append(required, p.Name)
		}
	}

	schema := map[string]any{
		"type":                 "object",
		"properties":           properties,
		"additionalProperties": false,
	}
	if len(required) > 0 {
		schema["required"] = required
	}
	return schema
}

// propertySchema renders a single parameter into its JSON Schema fragment,
// translating the capability ParamType into JSON Schema types and carrying
// across the description, enum constraint, and default when present.
func propertySchema(p registry.ParamSpec) map[string]any {
	prop := map[string]any{}
	if p.Description != "" {
		prop["description"] = p.Description
	}

	switch p.Type {
	case registry.TypeBool:
		prop["type"] = "boolean"
	case registry.TypeInt:
		prop["type"] = "integer"
	case registry.TypeString, registry.TypePath:
		prop["type"] = "string"
	case registry.TypeEnum:
		prop["type"] = "string"
		prop["enum"] = p.Enum
	case registry.TypeStringList, registry.TypePathList:
		prop["type"] = "array"
		prop["items"] = map[string]any{"type": "string"}
	}

	if p.Default != nil {
		prop["default"] = p.Default
	}
	return prop
}
