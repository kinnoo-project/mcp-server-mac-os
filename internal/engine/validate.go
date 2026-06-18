// validate.go turns the loosely-typed JSON parameter object that arrives from an
// MCP client into a strict, Go-typed parameter map the argument builders can
// trust. This is the engine's primary input guardrail: every value is checked
// against its ParamSpec (required-ness, type, enum membership) and every path is
// tilde-expanded here, before any argv token is assembled.
//
// Unknown parameters are rejected rather than ignored, so a typo or an attempt
// to smuggle an unexpected key surfaces as a clear error instead of silently
// doing the wrong thing.
package engine

import (
	"fmt"

	"mcp-server-mac-os/internal/registry"
)

// normalizeParams validates raw (the client-supplied params) against the
// capability's schema and returns a normalized map keyed by parameter name.
//
// Normalization rules per parameter:
//   - absent + required        -> error
//   - absent + has Default     -> the (coerced) default value
//   - absent + bool, no default -> false (so a flag is simply omitted)
//   - absent + optional, no default -> not present in the result
//   - present                  -> coerced/validated to its Go type
//
// Path and path-list values are tilde-expanded. Any key in raw that is not a
// declared parameter is an error.
func normalizeParams(c registry.Capability, raw map[string]any) (map[string]any, error) {
	known := make(map[string]bool, len(c.Params))
	for _, p := range c.Params {
		known[p.Name] = true
	}
	for k := range raw {
		if !known[k] {
			return nil, fmt.Errorf("%s: unknown parameter %q", c.Name, k)
		}
	}

	out := make(map[string]any, len(c.Params))
	for _, p := range c.Params {
		val, present := raw[p.Name]
		if !present {
			switch {
			case p.Required:
				return nil, fmt.Errorf("%s: missing required parameter %q", c.Name, p.Name)
			case p.Default != nil:
				val = p.Default
			case p.Type == registry.TypeBool:
				val = false
			default:
				continue // optional, no default: contribute nothing
			}
		}
		coerced, err := coerce(c.Name, p, val)
		if err != nil {
			return nil, err
		}
		out[p.Name] = coerced
	}
	return out, nil
}

// coerce converts a single JSON-decoded value into the Go type demanded by the
// parameter's ParamType, applying path expansion and enum checks. JSON numbers
// arrive as float64, so integer parameters are validated to be whole numbers.
func coerce(cap string, p registry.ParamSpec, val any) (any, error) {
	switch p.Type {
	case registry.TypeBool:
		b, ok := val.(bool)
		if !ok {
			return nil, typeErr(cap, p, val, "boolean")
		}
		return b, nil

	case registry.TypeString:
		s, ok := val.(string)
		if !ok {
			return nil, typeErr(cap, p, val, "string")
		}
		return s, nil

	case registry.TypeEnum:
		s, ok := val.(string)
		if !ok {
			return nil, typeErr(cap, p, val, "string")
		}
		for _, allowed := range p.Enum {
			if s == allowed {
				return s, nil
			}
		}
		return nil, fmt.Errorf("%s: parameter %q value %q not in allowed set %v", cap, p.Name, s, p.Enum)

	case registry.TypeInt:
		i, err := toInt(val)
		if err != nil {
			return nil, fmt.Errorf("%s: parameter %q: %w", cap, p.Name, err)
		}
		return i, nil

	case registry.TypePath:
		s, ok := val.(string)
		if !ok {
			return nil, typeErr(cap, p, val, "string path")
		}
		return expandUserPath(s)

	case registry.TypeStringList:
		return toStringList(cap, p, val, false)

	case registry.TypePathList:
		return toStringList(cap, p, val, true)

	default:
		// Unreachable: the registry validator rejects unknown types at load.
		return nil, fmt.Errorf("%s: parameter %q has unsupported type %q", cap, p.Name, p.Type)
	}
}

// toInt accepts the float64 that JSON numbers decode to (or a native int) and
// returns it as an int only if it represents a whole number.
func toInt(val any) (int, error) {
	switch n := val.(type) {
	case float64:
		if n != float64(int(n)) {
			return 0, fmt.Errorf("expected an integer, got %v", n)
		}
		return int(n), nil
	case int:
		return n, nil
	default:
		return 0, fmt.Errorf("expected an integer, got %T", val)
	}
}

// toStringList coerces a JSON array into []string, expanding tildes when the
// elements are paths.
func toStringList(cap string, p registry.ParamSpec, val any, isPath bool) (any, error) {
	arr, ok := val.([]any)
	if !ok {
		return nil, typeErr(cap, p, val, "array")
	}
	out := make([]string, 0, len(arr))
	for _, e := range arr {
		s, ok := e.(string)
		if !ok {
			return nil, fmt.Errorf("%s: parameter %q: array elements must be strings, got %T", cap, p.Name, e)
		}
		if isPath {
			expanded, err := expandUserPath(s)
			if err != nil {
				return nil, err
			}
			s = expanded
		}
		out = append(out, s)
	}
	return out, nil
}

// typeErr builds a consistent type-mismatch error.
func typeErr(cap string, p registry.ParamSpec, val any, want string) error {
	return fmt.Errorf("%s: parameter %q must be a %s, got %T", cap, p.Name, want, val)
}
