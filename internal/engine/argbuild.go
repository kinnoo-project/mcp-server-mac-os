// argbuild.go assembles the argument vector for a capability from its already
// validated, normalized parameters.
//
// Most capabilities use buildGeneric, a fully declarative builder driven by each
// parameter's ArgRule. Capabilities whose command grammar is too irregular for
// that (for example find's OR-grouped name expression, or grep's -e/--include
// handling) register a named builder instead. The registry's Capability.Builder
// field selects which builder runs; an empty value means "generic".
//
// This "data-driven where it pays, code where the grammar demands it" split is
// what keeps the common case free of per-capability Go while still expressing
// the awkward utilities correctly (see docs/specs/capability-engine-readonly-spine.md).
package engine

import (
	"fmt"
	"strconv"

	"mcp-server-mac-os/internal/registry"
)

// ArgBuilder turns a capability plus its normalized parameter map into an argv
// suffix (everything after the binary name). Builders never run commands; they
// only assemble tokens, which keeps them pure and trivially testable.
type ArgBuilder func(c registry.Capability, in map[string]any) ([]string, error)

// builders is the registry of named argv builders. The empty string aliases the
// generic builder so a manifest may omit "builder" entirely. Named builders for
// irregular utilities (find, grep, pwd) are added alongside their capabilities.
var builders = map[string]ArgBuilder{
	"":        buildGeneric,
	"generic": buildGeneric,
}

// lookupBuilder returns the ArgBuilder for a capability, or false if its Builder
// name is not registered. Used both at request time and by ValidateBuilders for
// startup fail-fast.
func lookupBuilder(name string) (ArgBuilder, bool) {
	b, ok := builders[name]
	return b, ok
}

// buildGeneric maps parameters onto argv declaratively. Flag-style arguments are
// emitted in declaration order, then a "--" terminator (only if there are
// positionals) guards the remaining operands so that a value beginning with "-"
// can never be reinterpreted as a flag.
func buildGeneric(c registry.Capability, in map[string]any) ([]string, error) {
	var flags []string
	var positionals []string

	for _, p := range c.Params {
		val, present := in[p.Name]
		if !present {
			continue
		}
		switch p.Arg.Kind {
		case registry.ArgFlag:
			on, ok := val.(bool)
			if !ok {
				return nil, fmt.Errorf("%s: parameter %q must be boolean for a flag arg", c.Name, p.Name)
			}
			if on {
				flags = append(flags, p.Arg.Flag)
			}

		case registry.ArgValuedFlag:
			s, err := stringifyScalar(val)
			if err != nil {
				return nil, fmt.Errorf("%s: parameter %q: %w", c.Name, p.Name, err)
			}
			flags = append(flags, p.Arg.Flag, s)

		case registry.ArgPositional:
			vals, err := stringifyOperands(val)
			if err != nil {
				return nil, fmt.Errorf("%s: parameter %q: %w", c.Name, p.Name, err)
			}
			positionals = append(positionals, vals...)

		case registry.ArgNone, "":
			// Declared for validation/documentation only; emits nothing.
		default:
			return nil, fmt.Errorf("%s: parameter %q has unhandled arg kind %q", c.Name, p.Name, p.Arg.Kind)
		}
	}

	argv := flags
	if len(positionals) > 0 {
		argv = append(argv, "--")
		argv = append(argv, positionals...)
	}
	return argv, nil
}

// stringifyScalar renders a scalar parameter value (string or int) as a single
// argv token.
func stringifyScalar(val any) (string, error) {
	switch v := val.(type) {
	case string:
		return v, nil
	case int:
		return strconv.Itoa(v), nil
	default:
		return "", fmt.Errorf("expected a scalar value, got %T", val)
	}
}

// stringifyOperands renders a positional parameter, which may be a single
// string/int or a list of strings, into one or more argv tokens.
func stringifyOperands(val any) ([]string, error) {
	switch v := val.(type) {
	case string:
		return []string{v}, nil
	case int:
		return []string{strconv.Itoa(v)}, nil
	case []string:
		return v, nil
	default:
		return nil, fmt.Errorf("expected a string, integer, or string list, got %T", val)
	}
}
