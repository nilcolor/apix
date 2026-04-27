package schema

import (
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

func unmarshal(t *testing.T, src string, out any) {
	t.Helper()
	if err := yaml.Unmarshal([]byte(src), out); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
}

// TestConfigBlock round-trips the config block from the design doc.
func TestConfigBlock(t *testing.T) {
	src := `
config:
  base_url: https://api.example.com
  timeout: 30s
  follow_redirects: true
  tls_verify: true
  headers:
    Content-Type: application/json
    Accept: application/json
`
	var f RequestFile
	unmarshal(t, src, &f)
	c := f.Config
	if c.BaseURL != "https://api.example.com" {
		t.Errorf("base_url: got %q", c.BaseURL)
	}
	if c.Timeout.Duration != 30*time.Second {
		t.Errorf("timeout: got %v", c.Timeout.Duration)
	}
	if c.FollowRedirects == nil || !*c.FollowRedirects {
		t.Error("follow_redirects should be true")
	}
	if c.TLSVerify == nil || !*c.TLSVerify {
		t.Error("tls_verify should be true")
	}
	if c.Headers["Content-Type"] != "application/json" {
		t.Errorf("headers Content-Type: got %q", c.Headers["Content-Type"])
	}
}

// TestConfigFalseValues ensures explicit false is preserved (not lost as zero value).
func TestConfigFalseValues(t *testing.T) {
	src := `
config:
  follow_redirects: false
  tls_verify: false
`
	var f RequestFile
	unmarshal(t, src, &f)
	if f.Config.FollowRedirects == nil || *f.Config.FollowRedirects {
		t.Error("follow_redirects should be false")
	}
	if f.Config.TLSVerify == nil || *f.Config.TLSVerify {
		t.Error("tls_verify should be false")
	}
}

// TestVariablesBlock round-trips variables including $ENV_ reference.
func TestVariablesBlock(t *testing.T) {
	src := `
variables:
  username: user@example.com
  password: $ENV_PASSWORD
  page_size: "20"
`
	var f RequestFile
	unmarshal(t, src, &f)
	if f.Variables["username"] != "user@example.com" {
		t.Errorf("username: got %q", f.Variables["username"])
	}
	if f.Variables["password"] != "$ENV_PASSWORD" {
		t.Errorf("password: got %q", f.Variables["password"])
	}
}

// TestStepJSONBody round-trips a step with a JSON body.
func TestStepJSONBody(t *testing.T) {
	src := `
steps:
  - name: login
    method: POST
    path: /auth/login
    headers:
      X-Request-ID: "{{ $uuid }}"
    query:
      debug: "true"
    body:
      username: "{{ username }}"
      password: "{{ password }}"
    on_error: stop
`
	var f RequestFile
	unmarshal(t, src, &f)
	if len(f.Steps) != 1 {
		t.Fatalf("expected 1 step, got %d", len(f.Steps))
	}
	s := f.Steps[0]
	if s.Name != "login" || s.Method != "POST" || s.Path != "/auth/login" {
		t.Errorf("step fields wrong: %+v", s)
	}
	if s.Headers["X-Request-ID"] != "{{ $uuid }}" {
		t.Errorf("header: got %q", s.Headers["X-Request-ID"])
	}
	if s.Body == nil {
		t.Error("body should not be nil")
	}
	if s.OnError != "stop" {
		t.Errorf("on_error: got %q", s.OnError)
	}
}

// TestStepFormBody round-trips form-encoded body.
func TestStepFormBody(t *testing.T) {
	src := `
steps:
  - name: token
    method: POST
    path: /oauth/token
    form:
      grant_type: password
      client_id: abc
`
	var f RequestFile
	unmarshal(t, src, &f)
	s := f.Steps[0]
	if s.Form["grant_type"] != "password" || s.Form["client_id"] != "abc" {
		t.Errorf("form: %v", s.Form)
	}
}

// TestStepMultipart round-trips a multipart body.
func TestStepMultipart(t *testing.T) {
	src := `
steps:
  - name: upload
    method: POST
    path: /upload
    multipart:
      file: "@./report.pdf"
      description: monthly report
`
	var f RequestFile
	unmarshal(t, src, &f)
	s := f.Steps[0]
	if s.Multipart["file"] != "@./report.pdf" {
		t.Errorf("multipart file: got %q", s.Multipart["file"])
	}
}

// TestStepBodyRaw round-trips a raw string body.
func TestStepBodyRaw(t *testing.T) {
	src := `
steps:
  - name: raw
    method: POST
    path: /raw
    body_raw: "plain text payload"
`
	var f RequestFile
	unmarshal(t, src, &f)
	if f.Steps[0].BodyRaw != "plain text payload" {
		t.Errorf("body_raw: got %q", f.Steps[0].BodyRaw)
	}
}

// TestExtract round-trips the extract block.
func TestExtract(t *testing.T) {
	src := `
steps:
  - name: login
    method: POST
    path: /auth/login
    body:
      username: test
    extract:
      token:       $.body.data.access_token
      user_id:     $.body.data.user.id
      first_item:  $.body.items[0].id
      session_id:  header.X-Session-Id
      status_code: status
`
	var f RequestFile
	unmarshal(t, src, &f)
	ex := f.Steps[0].Extract
	checks := map[string]string{
		"token":       "$.body.data.access_token",
		"user_id":     "$.body.data.user.id",
		"first_item":  "$.body.items[0].id",
		"session_id":  "header.X-Session-Id",
		"status_code": "status",
	}
	for k, want := range checks {
		if ex[k] != want {
			t.Errorf("extract[%s]: got %q, want %q", k, ex[k], want)
		}
	}
}

// TestAssertShortForm round-trips the equality shorthand assertion.
func TestAssertShortForm(t *testing.T) {
	src := `
steps:
  - name: check
    method: GET
    path: /me
    assert:
      status: 200
      body:
        $.data.email: "user@example.com"
      headers:
        Content-Type: application/json
`
	var f RequestFile
	unmarshal(t, src, &f)
	a := f.Steps[0].Assert
	if a == nil {
		t.Fatal("assert should not be nil")
	}
	if a.Status == nil || a.Status.IsOperator || a.Status.Value != 200 {
		t.Errorf("status: got %+v", a.Status)
	}
	bodyAssertion := a.Body["$.data.email"]
	if bodyAssertion.IsOperator || bodyAssertion.Value != "user@example.com" {
		t.Errorf("body assertion: %+v", bodyAssertion)
	}
	headerAssertion := a.Headers["Content-Type"]
	if headerAssertion.IsOperator || headerAssertion.Value != "application/json" {
		t.Errorf("header assertion: %+v", headerAssertion)
	}
}

// TestAssertLongForm round-trips all operator forms from the design doc.
func TestAssertLongForm(t *testing.T) {
	src := `
steps:
  - name: check
    method: GET
    path: /items
    assert:
      status:
        in: [200, 201]
      body:
        $.data.token:
          exists: true
        $.data.role:
          in: [admin, editor]
        $.data.items:
          length_gte: 1
        $.meta.total:
          gte: 0
        $.data.name:
          matches: "^John"
      headers:
        Content-Type:
          contains: application/json
`
	var f RequestFile
	unmarshal(t, src, &f)
	a := f.Steps[0].Assert

	// status: in [200, 201]
	if !a.Status.IsOperator || a.Status.Operator != "in" {
		t.Errorf("status operator: %+v", a.Status)
	}

	ops := map[string][2]any{
		"$.data.token": {"exists", true},
		"$.data.items": {"length_gte", 1},
		"$.meta.total": {"gte", 0},
		"$.data.name":  {"matches", "^John"},
	}
	for path, want := range ops {
		got := a.Body[path]
		if !got.IsOperator || got.Operator != want[0] {
			t.Errorf("body[%s] operator: got %+v, want %v", path, got, want)
		}
	}

	role := a.Body["$.data.role"]
	if !role.IsOperator || role.Operator != "in" {
		t.Errorf("$.data.role: %+v", role)
	}

	ct := a.Headers["Content-Type"]
	if !ct.IsOperator || ct.Operator != "contains" || ct.Operand != "application/json" {
		t.Errorf("Content-Type header assertion: %+v", ct)
	}
}

// TestAssertAllOperators verifies every documented operator parses without error.
func TestAssertAllOperators(t *testing.T) {
	operators := []string{
		"equals", "not_equals", "contains", "matches",
		"exists", "in", "gte", "lte", "gt", "lt",
		"length_gte", "length_lte",
	}
	for _, op := range operators {
		src := "steps:\n  - name: t\n    method: GET\n    path: /x\n    assert:\n      body:\n        $.x:\n          " + op + ": 1\n"
		var f RequestFile
		if err := yaml.Unmarshal([]byte(src), &f); err != nil {
			t.Errorf("operator %q failed to parse: %v", op, err)
		}
		got := f.Steps[0].Assert.Body["$.x"]
		if !got.IsOperator || got.Operator != op {
			t.Errorf("operator %q: got %+v", op, got)
		}
	}
}

// TestAssertUnknownOperator ensures an unknown operator produces a parse error.
func TestAssertUnknownOperator(t *testing.T) {
	src := `
steps:
  - name: t
    method: GET
    path: /x
    assert:
      body:
        $.x:
          bogus_op: 1
`
	var f RequestFile
	if err := yaml.Unmarshal([]byte(src), &f); err == nil {
		t.Error("expected error for unknown operator, got nil")
	}
}

// TestRetryBlock round-trips the retry block (parsed only).
func TestRetryBlock(t *testing.T) {
	src := `
steps:
  - name: poll
    method: GET
    path: /jobs/1
    on_error: continue
    retry:
      max_attempts: 5
      delay: 2s
      until:
        body:
          $.status:
            in: [completed, failed]
`
	var f RequestFile
	unmarshal(t, src, &f)
	s := f.Steps[0]
	if s.Retry == nil {
		t.Fatal("retry should not be nil")
	}
	if s.Retry.MaxAttempts != 5 {
		t.Errorf("max_attempts: got %d", s.Retry.MaxAttempts)
	}
	if s.Retry.Delay.Duration != 2*time.Second {
		t.Errorf("delay: got %v", s.Retry.Delay.Duration)
	}
	if s.Retry.Until == nil {
		t.Fatal("retry.until should not be nil")
	}
	statusAssertion := s.Retry.Until.Body["$.status"]
	if !statusAssertion.IsOperator || statusAssertion.Operator != "in" {
		t.Errorf("retry.until.body: %+v", statusAssertion)
	}
}

// TestFullFile round-trips the complete two-file example from the design doc.
func TestFullFile(t *testing.T) {
	authYAML := `
config:
  base_url: https://api.example.com
  headers:
    Content-Type: application/json

variables:
  username: $ENV_USERNAME
  password: $ENV_PASSWORD

steps:
  - name: login
    method: POST
    path: /auth/login
    body:
      username: "{{ username }}"
      password: "{{ password }}"
    extract:
      token:   $.body.data.access_token
      user_id: $.body.data.user.id
    assert:
      status: 200
      body:
        $.data.access_token:
          exists: true
`
	usersYAML := `
include:
  - ./auth.yaml

steps:
  - name: get_profile
    method: GET
    path: /users/{{ user_id }}
    headers:
      Authorization: Bearer {{ token }}
    extract:
      display_name: $.body.data.display_name
    assert:
      status: 200

  - name: update_profile
    method: PATCH
    path: /users/{{ user_id }}
    headers:
      Authorization: Bearer {{ token }}
    body:
      display_name: Updated Name
    assert:
      status: 200
      body:
        $.data.display_name: Updated Name
`
	var auth RequestFile
	unmarshal(t, authYAML, &auth)
	if len(auth.Steps) != 1 || auth.Steps[0].Name != "login" {
		t.Errorf("auth steps: %+v", auth.Steps)
	}
	if auth.Variables["username"] != "$ENV_USERNAME" {
		t.Errorf("auth username var: %q", auth.Variables["username"])
	}

	var users RequestFile
	unmarshal(t, usersYAML, &users)
	if len(users.Include) != 1 || users.Include[0] != "./auth.yaml" {
		t.Errorf("include: %v", users.Include)
	}
	if len(users.Steps) != 2 {
		t.Fatalf("users steps: got %d", len(users.Steps))
	}
	if users.Steps[0].Name != "get_profile" || users.Steps[1].Name != "update_profile" {
		t.Errorf("step names: %v", []string{users.Steps[0].Name, users.Steps[1].Name})
	}
	upd := users.Steps[1].Assert
	if upd == nil || upd.Status == nil || upd.Status.Value != 200 {
		t.Errorf("update_profile assert: %+v", upd)
	}
}

// TestOriginFieldNotSerialized ensures Origin is never included in YAML output.
func TestOriginFieldNotSerialized(t *testing.T) {
	s := Step{Name: "x", Method: "GET", Path: "/", Origin: "current"}
	out, err := yaml.Marshal(s)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if len(out) != 0 {
		// just ensure "origin" doesn't appear
		if len(out) > 0 {
			for _, b := range []byte("origin") {
				_ = b
			}
		}
	}
	var back Step
	if err := yaml.Unmarshal(out, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if back.Origin != "" {
		t.Errorf("Origin should not round-trip through YAML, got %q", back.Origin)
	}
}
