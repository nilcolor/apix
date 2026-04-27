package loader

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func td(name string) string {
	abs, _ := filepath.Abs("testdata/" + name)
	return abs
}

func TestSingleFile(t *testing.T) {
	f, err := Load(td("single.yaml"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if f.Config.BaseURL != "https://api.example.com" {
		t.Errorf("base_url: %q", f.Config.BaseURL)
	}
	if f.Config.Timeout.Duration != 10*time.Second {
		t.Errorf("timeout: %v", f.Config.Timeout.Duration)
	}
	if f.Variables["key"] != "value" {
		t.Errorf("variables: %v", f.Variables)
	}
	if len(f.Steps) != 1 || f.Steps[0].Name != "step1" {
		t.Errorf("steps: %v", f.Steps)
	}
}

func TestOriginTagging(t *testing.T) {
	f, err := Load(td("current.yaml"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	// base_step comes first (included), current_step comes last
	if len(f.Steps) != 2 {
		t.Fatalf("expected 2 steps, got %d", len(f.Steps))
	}
	if f.Steps[0].Name != "base_step" || f.Steps[0].Origin != "included" {
		t.Errorf("step[0]: name=%q origin=%q", f.Steps[0].Name, f.Steps[0].Origin)
	}
	if f.Steps[1].Name != "current_step" || f.Steps[1].Origin != "current" {
		t.Errorf("step[1]: name=%q origin=%q", f.Steps[1].Name, f.Steps[1].Origin)
	}
}

func TestConfigMergePrecedence(t *testing.T) {
	f, err := Load(td("current.yaml"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	// current overrides base_url; base provides timeout
	if f.Config.BaseURL != "https://current.example.com" {
		t.Errorf("base_url: %q", f.Config.BaseURL)
	}
	if f.Config.Timeout.Duration != 5*time.Second {
		t.Errorf("timeout from base: %v", f.Config.Timeout.Duration)
	}
}

func TestHeaderMerge(t *testing.T) {
	f, err := Load(td("current.yaml"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	// base provides Content-Type and X-Base; current adds X-Current
	if f.Config.Headers["Content-Type"] != "application/json" {
		t.Errorf("Content-Type: %q", f.Config.Headers["Content-Type"])
	}
	if f.Config.Headers["X-Base"] != "base" {
		t.Errorf("X-Base: %q", f.Config.Headers["X-Base"])
	}
	if f.Config.Headers["X-Current"] != "current" {
		t.Errorf("X-Current: %q", f.Config.Headers["X-Current"])
	}
}

func TestVariableMergePrecedence(t *testing.T) {
	f, err := Load(td("current.yaml"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	// current wins for "shared"; both base_var and current_var present
	if f.Variables["shared"] != "current_value" {
		t.Errorf("shared: %q", f.Variables["shared"])
	}
	if f.Variables["base_var"] != "from_base" {
		t.Errorf("base_var: %q", f.Variables["base_var"])
	}
	if f.Variables["current_var"] != "from_current" {
		t.Errorf("current_var: %q", f.Variables["current_var"])
	}
}

func TestExplicitFalseOverridesTrue(t *testing.T) {
	f, err := Load(td("false_override.yaml"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	// base has follow_redirects: true; false_override.yaml sets it false
	if f.Config.FollowRedirects == nil || *f.Config.FollowRedirects {
		t.Errorf("follow_redirects should be false, got %v", f.Config.FollowRedirects)
	}
	if f.Config.TLSVerify == nil || *f.Config.TLSVerify {
		t.Errorf("tls_verify should be false, got %v", f.Config.TLSVerify)
	}
}

func TestTwoLevelInclude(t *testing.T) {
	// top → mid → base: steps should be [base_step, mid_step, top_step]
	f, err := Load(td("top.yaml"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(f.Steps) != 3 {
		t.Fatalf("expected 3 steps, got %d: %v", len(f.Steps), f.Steps)
	}
	names := []string{f.Steps[0].Name, f.Steps[1].Name, f.Steps[2].Name}
	want := []string{"base_step", "mid_step", "top_step"}
	for i, w := range want {
		if names[i] != w {
			t.Errorf("step[%d]: got %q, want %q", i, names[i], w)
		}
	}
	// top_step is current; others are included
	if f.Steps[2].Origin != "current" {
		t.Errorf("top_step origin: %q", f.Steps[2].Origin)
	}
	if f.Steps[0].Origin != "included" || f.Steps[1].Origin != "included" {
		t.Errorf("included origins: %q %q", f.Steps[0].Origin, f.Steps[1].Origin)
	}
	// top base_url wins
	if f.Config.BaseURL != "https://top.example.com" {
		t.Errorf("base_url: %q", f.Config.BaseURL)
	}
	// mid timeout (20s) wins over base (5s) because mid is closer
	if f.Config.Timeout.Duration != 20*time.Second {
		t.Errorf("timeout: %v", f.Config.Timeout.Duration)
	}
}

func TestConfigOnlyInclude(t *testing.T) {
	f, err := Load(td("uses_config_only.yaml"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	// config_only provides base_url and Authorization header
	if f.Config.BaseURL != "https://shared.example.com" {
		t.Errorf("base_url: %q", f.Config.BaseURL)
	}
	if f.Config.Headers["Authorization"] != "Bearer shared_token" {
		t.Errorf("Authorization: %q", f.Config.Headers["Authorization"])
	}
	// only one step from current file
	if len(f.Steps) != 1 || f.Steps[0].Name != "uses_config" {
		t.Errorf("steps: %v", f.Steps)
	}
}

func TestCircularInclude(t *testing.T) {
	_, err := Load(td("circular_a.yaml"))
	if err == nil {
		t.Fatal("expected circular include error, got nil")
	}
	if !strings.Contains(err.Error(), "circular") {
		t.Errorf("error should mention circular: %v", err)
	}
}

func TestRelativeIncludePath(t *testing.T) {
	// Load current.yaml which includes ./base.yaml via relative path
	f, err := Load(td("current.yaml"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	// base_step should be present from the relative include
	if len(f.Steps) < 1 || f.Steps[0].Name != "base_step" {
		t.Errorf("relative include not resolved, steps: %v", f.Steps)
	}
}

func TestAbsoluteIncludePath(t *testing.T) {
	// Create a temp file that includes base.yaml by absolute path.
	absBase := td("base.yaml")
	content := "include:\n  - " + absBase + "\nsteps:\n  - name: abs_step\n    method: GET\n    path: /abs\n"
	tmp, err := os.CreateTemp("", "apix-loader-test-*.yaml")
	if err != nil {
		t.Fatalf("createtemp: %v", err)
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.WriteString(content); err != nil {
		t.Fatalf("write: %v", err)
	}
	tmp.Close()

	f, err := Load(tmp.Name())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(f.Steps) != 2 {
		t.Fatalf("expected 2 steps, got %d", len(f.Steps))
	}
	if f.Steps[0].Name != "base_step" || f.Steps[0].Origin != "included" {
		t.Errorf("step[0]: %+v", f.Steps[0])
	}
	if f.Steps[1].Name != "abs_step" || f.Steps[1].Origin != "current" {
		t.Errorf("step[1]: %+v", f.Steps[1])
	}
}

func TestErrorWrapsFilePath(t *testing.T) {
	_, err := Load("/nonexistent/path/file.yaml")
	if err == nil {
		t.Fatal("expected error for missing file")
	}
	if !strings.Contains(err.Error(), "nonexistent") {
		t.Errorf("error should include path: %v", err)
	}
}

func TestMissingFile(t *testing.T) {
	_, err := Load(td("does_not_exist.yaml"))
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}
