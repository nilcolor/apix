package schema

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestHookPreservesDocumentOrder(t *testing.T) {
	var step Step
	src := `
before_send:
  ts: "builtin.timestamp"
  canonical: "request.method + request.path"
  sig: "hex(hmac_sha256(canonical + ts, api_secret))"
`
	if err := yaml.Unmarshal([]byte(src), &step); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if step.BeforeSend == nil {
		t.Fatal("before_send not decoded")
	}

	want := []string{"ts", "canonical", "sig"}
	if len(step.BeforeSend.Vars) != len(want) {
		t.Fatalf("got %d vars, want %d", len(step.BeforeSend.Vars), len(want))
	}
	for i, name := range want {
		if step.BeforeSend.Vars[i].Name != name {
			t.Errorf("position %d: got %q, want %q", i, step.BeforeSend.Vars[i].Name, name)
		}
	}
	if step.BeforeSend.Vars[0].Expr != "builtin.timestamp" {
		t.Errorf("expression not captured: %q", step.BeforeSend.Vars[0].Expr)
	}
}

func TestHookOnConfig(t *testing.T) {
	var cfg Config
	src := `
before_send:
  sig: "hex(hmac_sha256(request.body, api_secret))"
`
	if err := yaml.Unmarshal([]byte(src), &cfg); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if cfg.BeforeSend == nil || len(cfg.BeforeSend.Vars) != 1 {
		t.Fatalf("config before_send not decoded: %+v", cfg.BeforeSend)
	}
}

func TestHookRejectsTemplateSyntax(t *testing.T) {
	var step Step
	src := `
before_send:
  sig: "hmac_sha256(request.body, {{ api_secret }})"
`
	err := yaml.Unmarshal([]byte(src), &step)
	if err == nil {
		t.Fatal("expected error for {{ }} inside a hook expression")
	}
	if !strings.Contains(err.Error(), "sig") {
		t.Errorf("error should name the variable: %v", err)
	}
}

func TestHookRejectsNonScalarExpression(t *testing.T) {
	var step Step
	src := `
before_send:
  sig:
    nested: value
`
	if err := yaml.Unmarshal([]byte(src), &step); err == nil {
		t.Fatal("expected error for non-scalar hook expression")
	}
}

func TestHookRejectsEmptyExpression(t *testing.T) {
	var step Step
	src := `
before_send:
  sig: "  "
`
	if err := yaml.Unmarshal([]byte(src), &step); err == nil {
		t.Fatal("expected error for empty hook expression")
	}
}

func TestHookRejectsSequenceForm(t *testing.T) {
	var step Step
	src := `
before_send:
  - "ts = builtin.timestamp"
`
	if err := yaml.Unmarshal([]byte(src), &step); err == nil {
		t.Fatal("expected error for sequence-form hook")
	}
}

func TestHookAbsentIsNil(t *testing.T) {
	var step Step
	if err := yaml.Unmarshal([]byte("name: plain\nmethod: GET\n"), &step); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if step.BeforeSend != nil {
		t.Errorf("before_send should be nil when absent, got %+v", step.BeforeSend)
	}
}

func TestHookRejectsReservedVariableName(t *testing.T) {
	for _, name := range []string{"request", "builtin", "hex", "json", "hmac_sha256"} {
		var step Step
		src := "before_send:\n  " + name + ": \"request.method\"\n"
		if err := yaml.Unmarshal([]byte(src), &step); err == nil {
			t.Errorf("%q should be rejected as a hook variable name", name)
		}
	}
}
