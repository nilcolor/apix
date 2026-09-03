package schema

import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// HookVar is one name→expression pair in a hook block.
type HookVar struct {
	Name string
	Expr string
}

// Hook is an ordered list of expressions evaluated at a fixed point in the request
// lifecycle. Order is preserved from the YAML document so a later expression can
// reference the result of an earlier one.
type Hook struct {
	Vars []HookVar
}

func (h *Hook) UnmarshalYAML(value *yaml.Node) error {
	if value.Kind != yaml.MappingNode {
		return fmt.Errorf("hook: must be a mapping of name to expression, got YAML node kind %v", value.Kind)
	}

	for i := 0; i+1 < len(value.Content); i += 2 {
		keyNode, valNode := value.Content[i], value.Content[i+1]

		var name string
		if err := keyNode.Decode(&name); err != nil {
			return fmt.Errorf("hook: variable name: %w", err)
		}
		if name == "" {
			return fmt.Errorf("hook: variable name must not be empty")
		}

		if valNode.Kind != yaml.ScalarNode {
			return fmt.Errorf("hook %q: expression must be a string, got YAML node kind %v", name, valNode.Kind)
		}
		var expr string
		if err := valNode.Decode(&expr); err != nil {
			return fmt.Errorf("hook %q: %w", name, err)
		}
		if strings.TrimSpace(expr) == "" {
			return fmt.Errorf("hook %q: expression must not be empty", name)
		}
		if reservedHookName(name) {
			return fmt.Errorf("hook %q: name is reserved; pick another", name)
		}
		if strings.Contains(expr, "{{") {
			return fmt.Errorf("hook %q: {{ }} is not allowed in a hook expression; reference variables by bare name", name)
		}

		h.Vars = append(h.Vars, HookVar{Name: name, Expr: expr})
	}

	return nil
}

// reservedHookName mirrors the names the evaluator binds in a hook's environment.
// Kept here so a collision is rejected when the file loads rather than when the
// request runs.
func reservedHookName(name string) bool {
	switch name {
	case "request", "builtin", "hmac_sha256", "sha256", "hex", "base64", "json":
		return true
	}
	return false
}
