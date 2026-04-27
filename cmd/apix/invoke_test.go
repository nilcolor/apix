package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fatih/color"
)

// writeFile writes content to a file in dir and returns its path.
func writeFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	return path
}

// makeCmd builds a InvokeCommand pointed at file, applying optional mutators.
func makeCmd(file string, mutators ...func(*InvokeCommand)) *InvokeCommand {
	r := &InvokeCommand{Output: "pretty"}
	r.Args.File = file
	for _, m := range mutators {
		m(r)
	}
	return r
}

// twoFileSetup writes auth.yaml (login step) + users.yaml (includes auth, lists users).
// Returns the path to users.yaml and a cleanup function.
func twoFileSetup(t *testing.T, serverURL string) string {
	t.Helper()
	dir := t.TempDir()

	authYAML := fmt.Sprintf(`
config:
  base_url: %s
steps:
  - name: login
    method: POST
    path: /auth/login
    extract:
      token: "$.body.access_token"
    assert:
      status: 200
`, serverURL)
	writeFile(t, dir, "auth.yaml", authYAML)

	usersYAML := `
include:
  - auth.yaml
steps:
  - name: list_users
    method: GET
    path: /users
    headers:
      Authorization: "Bearer {{ token }}"
    assert:
      status: 200
`
	return writeFile(t, dir, "users.yaml", usersYAML)
}

// TestRunCmdHappyPath: two-file setup, extraction flows from auth into users, all pass.
func TestRunCmdHappyPath(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/auth/login", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"access_token": "tok123"})
	})
	mux.HandleFunc("/users", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok123" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusOK)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	usersFile := twoFileSetup(t, srv.URL)

	var stdout, stderr bytes.Buffer
	code := invokeCmd(makeCmd(usersFile), &stdout, &stderr)
	if code != 0 {
		t.Fatalf("want exit 0, got %d\nstderr: %s", code, stderr.String())
	}
}

// TestRunCmdAssertionFailure: a failing assertion returns exit 1.
func TestRunCmdAssertionFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	dir := t.TempDir()
	yamlContent := fmt.Sprintf(`
config:
  base_url: %s
steps:
  - name: always_fails
    method: GET
    path: /
    assert:
      status: 200
`, srv.URL)
	f := writeFile(t, dir, "test.yaml", yamlContent)

	var stdout, stderr bytes.Buffer
	code := invokeCmd(makeCmd(f), &stdout, &stderr)
	if code != 1 {
		t.Fatalf("want exit 1 for assertion failure, got %d", code)
	}
}

// TestRunCmdFileNotFound: missing file returns exit 2.
func TestRunCmdFileNotFound(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := invokeCmd(makeCmd("/no/such/file.yaml"), &stdout, &stderr)
	if code != 2 {
		t.Fatalf("want exit 2 for missing file, got %d", code)
	}
}

// TestRunCmdMalformedVar: bad --var entry returns exit 2.
func TestRunCmdMalformedVar(t *testing.T) {
	dir := t.TempDir()
	f := writeFile(t, dir, "empty.yaml", "steps: []\n")

	var stdout, stderr bytes.Buffer
	code := invokeCmd(makeCmd(f, func(r *InvokeCommand) { r.Var = []string{"noequalssign"} }), &stdout, &stderr)
	if code != 2 {
		t.Fatalf("want exit 2 for malformed --var, got %d", code)
	}
}

// TestRunCmdBadTimeout: unparseable --timeout returns exit 2.
func TestRunCmdBadTimeout(t *testing.T) {
	dir := t.TempDir()
	f := writeFile(t, dir, "empty.yaml", "steps: []\n")

	var stdout, stderr bytes.Buffer
	code := invokeCmd(makeCmd(f, func(r *InvokeCommand) { r.Timeout = "notaduration" }), &stdout, &stderr)
	if code != 2 {
		t.Fatalf("want exit 2 for bad --timeout, got %d", code)
	}
}

// TestRunCmdFailFast: --fail-fast stops at first failure even with on_error: continue.
func TestRunCmdFailFast(t *testing.T) {
	var count int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count++
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	dir := t.TempDir()
	yamlContent := fmt.Sprintf(`
config:
  base_url: %s
steps:
  - name: step1
    method: GET
    path: /
    on_error: continue
    assert:
      status: 200
  - name: step2
    method: GET
    path: /
`, srv.URL)
	f := writeFile(t, dir, "test.yaml", yamlContent)

	var stdout, stderr bytes.Buffer
	code := invokeCmd(makeCmd(f, func(r *InvokeCommand) { r.FailFast = true }), &stdout, &stderr)
	if code != 1 {
		t.Fatalf("want exit 1, got %d", code)
	}
	if count != 1 {
		t.Errorf("--fail-fast should stop after first step, got %d HTTP calls", count)
	}
}

// TestRunCmdNoColor: --no-color removes ANSI escape sequences from pretty output.
func TestRunCmdNoColor(t *testing.T) {
	t.Cleanup(func() { color.NoColor = false })

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	dir := t.TempDir()
	yamlContent := fmt.Sprintf(`
config:
  base_url: %s
steps:
  - name: step1
    method: GET
    path: /
`, srv.URL)
	f := writeFile(t, dir, "test.yaml", yamlContent)

	var stdout, stderr bytes.Buffer
	code := invokeCmd(makeCmd(f, func(r *InvokeCommand) { r.NoColor = true }), &stdout, &stderr)
	if code != 0 {
		t.Fatalf("want exit 0, got %d\nstderr: %s", code, stderr.String())
	}
	if strings.Contains(stderr.String(), "\x1b[") {
		t.Errorf("expected no ANSI codes with --no-color, got:\n%q", stderr.String())
	}
}

// TestRunCmdOutputJSON: --output json writes valid JSON matching the spec to stdout.
func TestRunCmdOutputJSON(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/auth/login", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"access_token": "tok456"})
	})
	mux.HandleFunc("/users", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	usersFile := twoFileSetup(t, srv.URL)

	var stdout, stderr bytes.Buffer
	code := invokeCmd(makeCmd(usersFile, func(r *InvokeCommand) { r.Output = "json" }), &stdout, &stderr)
	if code != 0 {
		t.Fatalf("want exit 0, got %d\nstderr: %s", code, stderr.String())
	}

	var out struct {
		Steps []struct {
			Name       string `json:"name"`
			Method     string `json:"method"`
			Status     int    `json:"status"`
			DurationMs int64  `json:"duration_ms"`
		} `json:"steps"`
		Summary struct {
			Total  int `json:"total"`
			Passed int `json:"passed"`
			Failed int `json:"failed"`
		} `json:"summary"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal JSON output: %v\nraw: %s", err, stdout.String())
	}
	if len(out.Steps) != 2 {
		t.Errorf("want 2 steps, got %d", len(out.Steps))
	}
	if out.Summary.Total != 2 || out.Summary.Passed != 2 || out.Summary.Failed != 0 {
		t.Errorf("summary: %+v", out.Summary)
	}
	// JSON output goes to stdout; nothing significant on stderr.
	if strings.Contains(stderr.String(), "error") {
		t.Errorf("unexpected error on stderr: %s", stderr.String())
	}
}

// TestRunCmdTimeout: a valid --timeout is accepted and the run succeeds.
func TestRunCmdTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	dir := t.TempDir()
	yamlContent := fmt.Sprintf(`
config:
  base_url: %s
steps:
  - name: step1
    method: GET
    path: /
`, srv.URL)
	f := writeFile(t, dir, "test.yaml", yamlContent)

	var stdout, stderr bytes.Buffer
	code := invokeCmd(makeCmd(f, func(r *InvokeCommand) { r.Timeout = "30s" }), &stdout, &stderr)
	if code != 0 {
		t.Fatalf("want exit 0 with --timeout 30s, got %d\nstderr: %s", code, stderr.String())
	}
}

// TestRunCmdVarOverride: --var overrides a file-level variable.
func TestRunCmdVarOverride(t *testing.T) {
	var receivedPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedPath = r.URL.Path
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	dir := t.TempDir()
	yamlContent := fmt.Sprintf(`
config:
  base_url: %s
variables:
  user_id: "original"
steps:
  - name: step1
    method: GET
    path: "/users/{{ user_id }}"
`, srv.URL)
	f := writeFile(t, dir, "test.yaml", yamlContent)

	var stdout, stderr bytes.Buffer
	code := invokeCmd(makeCmd(f, func(r *InvokeCommand) { r.Var = []string{"user_id=overridden"} }), &stdout, &stderr)
	if code != 0 {
		t.Fatalf("want exit 0, got %d\nstderr: %s", code, stderr.String())
	}
	if receivedPath != "/users/overridden" {
		t.Errorf("want path /users/overridden, got %q", receivedPath)
	}
}

// TestRunCmdPrintBody: print: "$.body" writes pretty JSON to stdout.
func TestRunCmdPrintBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"token": "abc", "user": map[string]any{"id": 1}})
	}))
	defer srv.Close()

	dir := t.TempDir()
	yamlContent := fmt.Sprintf(`
config:
  base_url: %s
steps:
  - name: get_token
    method: POST
    path: /login
    print: "$.body"
`, srv.URL)
	f := writeFile(t, dir, "test.yaml", yamlContent)

	var stdout, stderr bytes.Buffer
	code := invokeCmd(makeCmd(f), &stdout, &stderr)
	if code != 0 {
		t.Fatalf("want exit 0, got %d\nstderr: %s", code, stderr.String())
	}
	out := stdout.String()
	if !strings.Contains(out, `"token"`) || !strings.Contains(out, `"abc"`) {
		t.Errorf("stdout should contain printed JSON body, got: %q", out)
	}
	// Must be indented (pretty-printed).
	if !strings.Contains(out, "\n") {
		t.Errorf("expected indented JSON on stdout, got: %q", out)
	}
}

// TestRunCmdPrintSubtree: print: "$.body.field" writes a JSON subtree to stdout.
func TestRunCmdPrintSubtree(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"role": "admin", "id": 7}})
	}))
	defer srv.Close()

	dir := t.TempDir()
	yamlContent := fmt.Sprintf(`
config:
  base_url: %s
steps:
  - name: get_role
    method: GET
    path: /me
    print: "$.body.data.role"
`, srv.URL)
	f := writeFile(t, dir, "test.yaml", yamlContent)

	var stdout, stderr bytes.Buffer
	code := invokeCmd(makeCmd(f), &stdout, &stderr)
	if code != 0 {
		t.Fatalf("want exit 0, got %d\nstderr: %s", code, stderr.String())
	}
	if strings.TrimSpace(stdout.String()) != "admin" {
		t.Errorf("expected 'admin' on stdout, got: %q", stdout.String())
	}
}

// TestRunCmdPrintJSONMode: print: value appears in JSON output, not separately on stdout.
func TestRunCmdPrintJSONMode(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"status": "ok"})
	}))
	defer srv.Close()

	dir := t.TempDir()
	yamlContent := fmt.Sprintf(`
config:
  base_url: %s
steps:
  - name: check
    method: GET
    path: /
    print: "$.body.status"
`, srv.URL)
	f := writeFile(t, dir, "test.yaml", yamlContent)

	var stdout, stderr bytes.Buffer
	code := invokeCmd(makeCmd(f, func(r *InvokeCommand) { r.Output = "json" }), &stdout, &stderr)
	if code != 0 {
		t.Fatalf("want exit 0, got %d\nstderr: %s", code, stderr.String())
	}
	var envelope struct {
		Steps []struct {
			Printed string `json:"printed"`
		} `json:"steps"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
		t.Fatalf("unmarshal JSON output: %v\nstdout: %s", err, stdout.String())
	}
	if len(envelope.Steps) == 0 {
		t.Fatal("expected at least one step in JSON output")
	}
	if envelope.Steps[0].Printed != "ok" {
		t.Errorf("expected printed=ok in JSON output, got: %q", envelope.Steps[0].Printed)
	}
}
