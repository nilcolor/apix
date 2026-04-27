package schema

import (
	"fmt"
	"time"

	"gopkg.in/yaml.v3"
)

// RequestFile is the top-level structure of an apix YAML file.
type RequestFile struct {
	Include   []string          `yaml:"include"`
	Config    Config            `yaml:"config"`
	Variables map[string]string `yaml:"variables"`
	Steps     []Step            `yaml:"steps"`
}

// Config holds HTTP client settings that apply to every step in the file.
type Config struct {
	BaseURL         string            `yaml:"base_url"`
	Timeout         Duration          `yaml:"timeout"`
	FollowRedirects *bool             `yaml:"follow_redirects"`
	TLSVerify       *bool             `yaml:"tls_verify"`
	Headers         map[string]string `yaml:"headers"`
}

// Step represents a single HTTP request.
type Step struct {
	Name      string            `yaml:"name"`
	Method    string            `yaml:"method"`
	Path      string            `yaml:"path"`
	URL       string            `yaml:"url"`
	Headers   map[string]string `yaml:"headers"`
	Query     map[string]string `yaml:"query"`
	Body           any               `yaml:"body"`
	Form           map[string]string `yaml:"form"`
	Multipart      map[string]string `yaml:"multipart"`
	BodyRaw        string            `yaml:"body_raw"`
	BodyFile       string            `yaml:"body_file"`
	FollowRedirect *bool             `yaml:"follow_redirect"`
	Extract   map[string]string `yaml:"extract"`
	Print     string            `yaml:"print"`
	Assert    *Assert           `yaml:"assert"`
	OnError   string            `yaml:"on_error"`
	Retry     *Retry            `yaml:"retry"`

	// Origin is set by the loader ("included" or "current"); never serialized.
	Origin string `yaml:"-"`
}

// Assert holds the assertions for a step, keyed by target (status, body path, header name).
type Assert struct {
	Status  *Assertion            `yaml:"status"`
	Body    map[string]Assertion  `yaml:"body"`
	Headers map[string]Assertion  `yaml:"headers"`
}

// validOperators is the set of recognized operator keys in the long-form assertion.
var validOperators = map[string]bool{
	"equals": true, "not_equals": true, "contains": true, "matches": true,
	"exists": true, "in": true, "gte": true, "lte": true, "gt": true, "lt": true,
	"length_gte": true, "length_lte": true,
}

// Assertion holds either a scalar equality check or an operator+operand check.
// YAML scalars → equality; YAML mappings with a single operator key → operator form.
type Assertion struct {
	// IsOperator is true when this is an operator assertion.
	IsOperator bool
	// Value holds the scalar value when IsOperator is false.
	Value any
	// Operator is the operator name when IsOperator is true.
	Operator string
	// Operand is the operand value when IsOperator is true.
	Operand any
}

func (a *Assertion) UnmarshalYAML(value *yaml.Node) error {
	switch value.Kind {
	case yaml.ScalarNode:
		var v any
		if err := value.Decode(&v); err != nil {
			return err
		}
		a.IsOperator = false
		a.Value = v
		return nil

	case yaml.MappingNode:
		if len(value.Content)%2 != 0 {
			return fmt.Errorf("assertion: malformed mapping node")
		}
		if len(value.Content) != 2 {
			return fmt.Errorf("assertion: operator mapping must have exactly one key, got %d", len(value.Content)/2)
		}
		key := value.Content[0].Value
		if !validOperators[key] {
			return fmt.Errorf("assertion: unknown operator %q", key)
		}
		var operand any
		if err := value.Content[1].Decode(&operand); err != nil {
			return err
		}
		a.IsOperator = true
		a.Operator = key
		a.Operand = operand
		return nil

	default:
		return fmt.Errorf("assertion: unexpected YAML node kind %v", value.Kind)
	}
}

// Retry holds retry configuration for a step (parsed but not executed in v1).
type Retry struct {
	MaxAttempts int     `yaml:"max_attempts"`
	Delay       Duration `yaml:"delay"`
	Until       *Assert  `yaml:"until"`
}

// Duration wraps time.Duration to support YAML unmarshalling from strings like "30s".
type Duration struct {
	time.Duration
}

func (d *Duration) UnmarshalYAML(value *yaml.Node) error {
	var s string
	if err := value.Decode(&s); err != nil {
		return fmt.Errorf("duration: expected string, got %v: %w", value.Tag, err)
	}
	parsed, err := time.ParseDuration(s)
	if err != nil {
		return fmt.Errorf("duration: %w", err)
	}
	d.Duration = parsed
	return nil
}
