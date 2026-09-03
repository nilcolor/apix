package pipeline

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/nilcolor/apix/internal/schema"
	"github.com/nilcolor/apix/internal/vars"
)

// --- helpers ---

func newCfg(baseURL string) *schema.Config { return &schema.Config{BaseURL: baseURL} }

func scalarAssertion() *schema.Assertion {
	return &schema.Assertion{Value: 200}
}

func currentStep(name, method, path string) schema.Step {
	return schema.Step{Name: name, Method: method, Path: path, Origin: "current"}
}

func includedStep(name, method, path string) schema.Step {
	return schema.Step{Name: name, Method: method, Path: path, Origin: "included"}
}

// --- tests ---

// TestRunHappyPathExtraction: two steps where step 2 uses a value extracted from step 1.
func TestRunHappyPathExtraction(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/login", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"token": "abc123"})
	})
	mux.HandleFunc("/profile", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer abc123" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusOK)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	steps := []schema.Step{
		{
			Name:    "login",
			Method:  "POST",
			Path:    "/login",
			Origin:  "current",
			Extract: map[string]string{"token": "$.body.token"},
			Assert:  &schema.Assert{Status: scalarAssertion()},
		},
		{
			Name:    "get_profile",
			Method:  "GET",
			Path:    "/profile",
			Origin:  "current",
			Headers: map[string]string{"Authorization": "Bearer {{ token }}"},
			Assert:  &schema.Assert{Status: scalarAssertion()},
		},
	}

	results, summary, err := Run(steps, newCfg(srv.URL), vars.NewStore(), Options{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("want 2 results, got %d", len(results))
	}
	if summary.Total != 2 || summary.Passed != 2 || summary.Failed != 0 {
		t.Errorf("summary: %+v", summary)
	}
	if results[0].Extracted["token"] != "abc123" {
		t.Errorf("want token=abc123, got %q", results[0].Extracted["token"])
	}
	for _, r := range results {
		for _, a := range r.Assertions {
			if !a.Passed {
				t.Errorf("step %q: assertion %q failed: %s", r.Name, a.Check, a.Message)
			}
		}
	}
}

// TestRunExpressionAssertCrossStepVariable exercises the motivating end-to-end case:
// a list-form assert whose operand is a variable extracted by an earlier step.
func TestRunExpressionAssertCrossStepVariable(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/build", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"clearance_id": "abc123"})
	})
	mux.HandleFunc("/final", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"clearance_id": "abc123"})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	var assertYAML schema.Assert
	if err := yaml.Unmarshal([]byte(`
- "status == 200"
- "$.body.clearance_id == {{ a_clearance_id }}"
`), &assertYAML); err != nil {
		t.Fatalf("unmarshal assert: %v", err)
	}

	steps := []schema.Step{
		{
			Name:    "build_clearance",
			Method:  "POST",
			Path:    "/build",
			Origin:  "current",
			Extract: map[string]string{"a_clearance_id": "$.body.clearance_id"},
			Assert:  &schema.Assert{Status: scalarAssertion()},
		},
		{
			Name:   "check_final_clearance",
			Method: "GET",
			Path:   "/final",
			Origin: "current",
			Assert: &assertYAML,
		},
	}

	results, summary, err := Run(steps, newCfg(srv.URL), vars.NewStore(), Options{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if summary.Total != 2 || summary.Passed != 2 || summary.Failed != 0 {
		t.Errorf("summary: %+v", summary)
	}
	for _, r := range results {
		for _, a := range r.Assertions {
			if !a.Passed {
				t.Errorf("step %q: assertion %q failed: %s", r.Name, a.Check, a.Message)
			}
		}
	}
}

// TestRunOnErrorContinue: a failing step with on_error=continue should not stop the pipeline.
func TestRunOnErrorContinue(t *testing.T) {
	var called int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&called, 1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	steps := []schema.Step{
		{
			Name:    "fail_step",
			Method:  "GET",
			Path:    "/",
			Origin:  "current",
			OnError: "continue",
			Assert:  &schema.Assert{Status: scalarAssertion()},
		},
		{
			Name:   "next_step",
			Method: "GET",
			Path:   "/",
			Origin: "current",
		},
	}

	results, summary, err := Run(steps, newCfg(srv.URL), vars.NewStore(), Options{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("want 2 results (pipeline continued), got %d", len(results))
	}
	if summary.Failed != 1 || summary.Passed != 1 {
		t.Errorf("summary: %+v", summary)
	}
	if atomic.LoadInt32(&called) != 2 {
		t.Errorf("want 2 HTTP calls, got %d", called)
	}
}

// TestRunOnErrorStop: the default on_error=stop behavior stops after the first failure.
func TestRunOnErrorStop(t *testing.T) {
	var called int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&called, 1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	steps := []schema.Step{
		{
			Name:   "fail_step",
			Method: "GET",
			Path:   "/",
			Origin: "current",
			Assert: &schema.Assert{Status: scalarAssertion()},
		},
		{
			Name:   "next_step",
			Method: "GET",
			Path:   "/",
			Origin: "current",
		},
	}

	results, _, err := Run(steps, newCfg(srv.URL), vars.NewStore(), Options{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("want 1 result (pipeline stopped), got %d", len(results))
	}
	if atomic.LoadInt32(&called) != 1 {
		t.Errorf("want 1 HTTP call, got %d", called)
	}
}

// TestRunFailFastOverride: --fail-fast overrides on_error=continue.
func TestRunFailFastOverride(t *testing.T) {
	var called int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&called, 1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	steps := []schema.Step{
		{
			Name:    "fail_step",
			Method:  "GET",
			Path:    "/",
			Origin:  "current",
			OnError: "continue",
			Assert:  &schema.Assert{Status: scalarAssertion()},
		},
		{
			Name:   "next_step",
			Method: "GET",
			Path:   "/",
			Origin: "current",
		},
	}

	results, _, err := Run(steps, newCfg(srv.URL), vars.NewStore(), Options{FailFast: true})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("want 1 result (fail-fast stopped pipeline), got %d", len(results))
	}
	if atomic.LoadInt32(&called) != 1 {
		t.Errorf("want 1 HTTP call (fail-fast), got %d", called)
	}
}

// TestRunStepFiltering: --step runs only the named current step; included steps always run.
func TestRunStepFiltering(t *testing.T) {
	hits := map[string]*int32{
		"/included1": new(int32),
		"/current1":  new(int32),
		"/current2":  new(int32),
		"/current3":  new(int32),
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if c, ok := hits[r.URL.Path]; ok {
			atomic.AddInt32(c, 1)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	steps := []schema.Step{
		includedStep("included1", "GET", "/included1"),
		currentStep("current1", "GET", "/current1"),
		currentStep("current2", "GET", "/current2"),
		currentStep("current3", "GET", "/current3"),
	}

	results, _, err := Run(steps, newCfg(srv.URL), vars.NewStore(), Options{Step: []string{"current2"}})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if len(results) != 2 {
		t.Fatalf("want 2 results (included1 + current2), got %d", len(results))
	}
	names := []string{results[0].Name, results[1].Name}
	if names[0] != "included1" || names[1] != "current2" {
		t.Errorf("want [included1, current2], got %v", names)
	}
	if atomic.LoadInt32(hits["/included1"]) != 1 {
		t.Error("included1 should have run")
	}
	if atomic.LoadInt32(hits["/current1"]) != 0 {
		t.Error("current1 should have been skipped")
	}
	if atomic.LoadInt32(hits["/current2"]) != 1 {
		t.Error("current2 should have run")
	}
	if atomic.LoadInt32(hits["/current3"]) != 0 {
		t.Error("current3 should have been skipped")
	}
}

// TestRunFromFiltering: --from skips current steps before the named one; included always run.
func TestRunFromFiltering(t *testing.T) {
	hits := map[string]*int32{
		"/included1": new(int32),
		"/current1":  new(int32),
		"/current2":  new(int32),
		"/current3":  new(int32),
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if c, ok := hits[r.URL.Path]; ok {
			atomic.AddInt32(c, 1)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	steps := []schema.Step{
		includedStep("included1", "GET", "/included1"),
		currentStep("current1", "GET", "/current1"),
		currentStep("current2", "GET", "/current2"),
		currentStep("current3", "GET", "/current3"),
	}

	results, _, err := Run(steps, newCfg(srv.URL), vars.NewStore(), Options{From: "current2"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	// Expect: included1, current2, current3 (current1 skipped).
	if len(results) != 3 {
		names := make([]string, len(results))
		for i, r := range results {
			names[i] = r.Name
		}
		t.Fatalf("want 3 results, got %d: %v", len(results), names)
	}
	if atomic.LoadInt32(hits["/current1"]) != 0 {
		t.Error("current1 should have been skipped (before --from)")
	}
	if atomic.LoadInt32(hits["/current2"]) != 1 {
		t.Error("current2 should have run (--from start)")
	}
	if atomic.LoadInt32(hits["/current3"]) != 1 {
		t.Error("current3 should have run (after --from)")
	}
}

// TestRunSkipFiltering: --skip excludes the named current steps; included always run.
func TestRunSkipFiltering(t *testing.T) {
	hits := map[string]*int32{
		"/included1": new(int32),
		"/current1":  new(int32),
		"/current2":  new(int32),
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if c, ok := hits[r.URL.Path]; ok {
			atomic.AddInt32(c, 1)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	steps := []schema.Step{
		includedStep("included1", "GET", "/included1"),
		currentStep("current1", "GET", "/current1"),
		currentStep("current2", "GET", "/current2"),
	}

	results, _, err := Run(steps, newCfg(srv.URL), vars.NewStore(), Options{Skip: []string{"current1"}})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if len(results) != 2 {
		t.Fatalf("want 2 results (included1 + current2), got %d", len(results))
	}
	if atomic.LoadInt32(hits["/current1"]) != 0 {
		t.Error("current1 should have been skipped")
	}
	if atomic.LoadInt32(hits["/current2"]) != 1 {
		t.Error("current2 should have run")
	}
}

// TestRunDryRun: dry-run resolves URL and method but makes no HTTP calls.
func TestRunDryRun(t *testing.T) {
	var called int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&called, 1)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	steps := []schema.Step{
		currentStep("post_step", "POST", "/api/resource"),
	}

	results, _, err := Run(steps, newCfg(srv.URL), vars.NewStore(), Options{DryRun: true})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if atomic.LoadInt32(&called) != 0 {
		t.Errorf("dry-run should make no HTTP calls, got %d", called)
	}
	if len(results) != 1 {
		t.Fatalf("want 1 result, got %d", len(results))
	}
	r := results[0]
	if r.Method != "POST" {
		t.Errorf("want method POST, got %q", r.Method)
	}
	if !strings.Contains(r.URL, "/api/resource") {
		t.Errorf("want URL containing /api/resource, got %q", r.URL)
	}
	if len(r.Assertions) != 0 {
		t.Errorf("dry-run should have zero assertions, got %d", len(r.Assertions))
	}
}

// TestRunRetryWarning: a step with retry: emits a warning to Stderr exactly once.
func TestRunRetryWarning(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	retryDelay := schema.Duration{}
	step := schema.Step{
		Name:   "retry_step",
		Method: "GET",
		Path:   "/",
		Origin: "current",
		Retry:  &schema.Retry{MaxAttempts: 3, Delay: retryDelay},
	}

	var warnBuf bytes.Buffer
	_, _, err := Run([]schema.Step{step}, newCfg(srv.URL), vars.NewStore(), Options{Stderr: &warnBuf})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	out := warnBuf.String()
	if !strings.Contains(out, "retry") {
		t.Errorf("expected retry warning, got: %q", out)
	}
	if count := strings.Count(out, "warning"); count != 1 {
		t.Errorf("expected exactly 1 warning, got %d in: %q", count, out)
	}
}

// TestRunAskPrompt: an ask: step reads a value from stdin, echoes the prompt to
// stderr, and makes the value available to later steps via the store.
func TestRunAskPrompt(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/request-code", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/submit-code", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-OTP") != "123456" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusOK)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	steps := []schema.Step{
		{
			Name:   "request_code",
			Method: "POST",
			Path:   "/request-code",
			Origin: "current",
			Ask:    []schema.AskItem{{Var: "otp_code", Prompt: "Enter OTP:"}},
			Assert: &schema.Assert{Status: scalarAssertion()},
		},
		{
			Name:    "submit_code",
			Method:  "POST",
			Path:    "/submit-code",
			Origin:  "current",
			Headers: map[string]string{"X-OTP": "{{ otp_code }}"},
			Assert:  &schema.Assert{Status: scalarAssertion()},
		},
	}

	var stderrBuf bytes.Buffer
	results, summary, err := Run(steps, newCfg(srv.URL), vars.NewStore(), Options{
		Stdin:  strings.NewReader("123456\n"),
		Stderr: &stderrBuf,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if summary.Failed != 0 {
		t.Fatalf("want 0 failed, got %d (results: %+v)", summary.Failed, results)
	}
	if !strings.Contains(stderrBuf.String(), "Enter OTP:") {
		t.Errorf("want prompt text on stderr, got: %q", stderrBuf.String())
	}
	if results[0].Asked["otp_code"] != "123456" {
		t.Errorf("want Asked[otp_code]=123456, got %q", results[0].Asked["otp_code"])
	}
}

// TestRunAskSkipsWhenPreset: a var already in the store (e.g. via --var) is
// never prompted for, and its value is still reported in Asked.
func TestRunAskSkipsWhenPreset(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	step := currentStep("ask_step", "GET", "/")
	step.Ask = []schema.AskItem{{Var: "otp_code", Prompt: "Enter OTP:"}}

	store := vars.NewStore()
	store.Set("otp_code", "999999") // simulates --var otp_code=999999

	var stderrBuf bytes.Buffer
	results, _, err := Run([]schema.Step{step}, newCfg(srv.URL), store, Options{
		Stdin:  strings.NewReader(""), // would error if actually read from
		Stderr: &stderrBuf,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if strings.Contains(stderrBuf.String(), "Enter OTP") {
		t.Errorf("should not prompt when var is already set, got stderr: %q", stderrBuf.String())
	}
	if results[0].Error != "" {
		t.Errorf("want no error, got %q", results[0].Error)
	}
	if results[0].Asked["otp_code"] != "999999" {
		t.Errorf("want Asked[otp_code]=999999 (preset), got %q", results[0].Asked["otp_code"])
	}
}

// TestRunAskDryRunSkip: --dry-run never prompts and never touches stdin.
func TestRunAskDryRunSkip(t *testing.T) {
	var called int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&called, 1)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	step := currentStep("ask_step", "GET", "/")
	step.Ask = []schema.AskItem{{Var: "otp_code", Prompt: "Enter OTP:"}}

	results, _, err := Run([]schema.Step{step}, newCfg(srv.URL), vars.NewStore(), Options{
		DryRun: true,
		Stdin:  strings.NewReader(""), // must never be read
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if atomic.LoadInt32(&called) != 0 {
		t.Errorf("dry-run should make no HTTP calls, got %d", called)
	}
	if len(results[0].Asked) != 0 {
		t.Errorf("dry-run should skip ask:, got Asked=%v", results[0].Asked)
	}
}

// TestRunAskMasksSensitiveVarName: a var name matching the sensitive-field
// heuristic is masked in the reported Asked map, but the real value still
// flows into later steps' interpolation.
func TestRunAskMasksSensitiveVarName(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/request", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/use", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer secret-value" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusOK)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	steps := []schema.Step{
		{
			Name:   "request",
			Method: "GET",
			Path:   "/request",
			Origin: "current",
			Ask:    []schema.AskItem{{Var: "auth_token", Prompt: "Enter token:"}},
		},
		{
			Name:    "use",
			Method:  "GET",
			Path:    "/use",
			Origin:  "current",
			Headers: map[string]string{"Authorization": "Bearer {{ auth_token }}"},
			Assert:  &schema.Assert{Status: scalarAssertion()},
		},
	}

	results, summary, err := Run(steps, newCfg(srv.URL), vars.NewStore(), Options{
		Stdin: strings.NewReader("secret-value\n"),
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if summary.Failed != 0 {
		t.Fatalf("want 0 failed, got %d (results: %+v)", summary.Failed, results)
	}
	if results[0].Asked["auth_token"] != "***" {
		t.Errorf("want masked Asked[auth_token]=***, got %q", results[0].Asked["auth_token"])
	}
}

// TestRunAskEOFError: an empty/closed stdin surfaces as a step error, subject
// to the same on_error/fail-fast handling as any other execution error.
func TestRunAskEOFError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	step := currentStep("ask_step", "GET", "/")
	step.Ask = []schema.AskItem{{Var: "otp_code", Prompt: "Enter OTP:"}}

	results, summary, err := Run([]schema.Step{step}, newCfg(srv.URL), vars.NewStore(), Options{
		Stdin: strings.NewReader(""),
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if summary.Failed != 1 {
		t.Errorf("want 1 failed step on EOF, got %d", summary.Failed)
	}
	if results[0].Error == "" {
		t.Errorf("want step error on stdin EOF, got none")
	}
}

// TestTimeBuiltinsAgreeWithinStep: the property signing depends on — a body and a
// header referencing {{ $timestamp }} in the same step must resolve to one instant.
// Deterministic proof that freezing works lives in vars; this covers the wiring.
func TestTimeBuiltinsAgreeWithinStep(t *testing.T) {
	var headerTS, bodyTS []string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		headerTS = append(headerTS, r.Header.Get("X-Timestamp"))
		bodyTS = append(bodyTS, body["ts"].(string))
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	step := func(name string) schema.Step {
		return schema.Step{
			Name:    name,
			Method:  "POST",
			Path:    "/sign",
			Origin:  "current",
			Headers: map[string]string{"X-Timestamp": "{{ $timestamp_ms }}"},
			Body:    map[string]any{"ts": "{{ $timestamp_ms }}"},
		}
	}

	_, _, err := Run([]schema.Step{step("one"), step("two")}, newCfg(srv.URL), vars.NewStore(), Options{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if len(headerTS) != 2 {
		t.Fatalf("expected 2 requests, got %d", len(headerTS))
	}
	for i := range headerTS {
		if headerTS[i] != bodyTS[i] {
			t.Errorf("step %d: header %q and body %q must match", i, headerTS[i], bodyTS[i])
		}
	}
}

// TestBeforeSendSignsRequest: the motivating case. The handler recomputes the HMAC
// from what it received and compares, so a mismatch between what the hook signed
// and what was sent fails the test rather than being asserted around.
func TestBeforeSendSignsRequest(t *testing.T) {
	const secret = "topsecret"
	var verified, mismatch int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		ts := r.Header.Get("X-Timestamp")

		mac := hmac.New(sha256.New, []byte(secret))
		mac.Write([]byte(r.Method + r.URL.Path + string(body) + ts))
		want := hex.EncodeToString(mac.Sum(nil))

		if hmac.Equal([]byte(want), []byte(r.Header.Get("X-Signature"))) {
			atomic.AddInt32(&verified, 1)
		} else {
			atomic.AddInt32(&mismatch, 1)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	store := vars.NewStore()
	store.Set("api_secret", secret)

	steps := []schema.Step{{
		Name:   "signed",
		Method: "POST",
		Path:   "/orders",
		Origin: "current",
		Body:   map[string]any{"amount": 100},
		BeforeSend: &schema.Hook{Vars: []schema.HookVar{
			{Name: "ts", Expr: "builtin.timestamp"},
			{Name: "canonical", Expr: "request.method + request.path + request.body + ts"},
			{Name: "sig", Expr: "hex(hmac_sha256(canonical, api_secret))"},
		}},
		Headers: map[string]string{
			"X-Timestamp": "{{ ts }}",
			"X-Signature": "{{ sig }}",
		},
	}}

	results, _, err := Run(steps, newCfg(srv.URL), store, Options{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if results[0].Error != "" {
		t.Fatalf("step error: %s", results[0].Error)
	}
	if atomic.LoadInt32(&verified) != 1 || atomic.LoadInt32(&mismatch) != 0 {
		t.Errorf("signature not verified by server: verified=%d mismatch=%d",
			atomic.LoadInt32(&verified), atomic.LoadInt32(&mismatch))
	}
	if len(results[0].HookVars["sig"]) != 64 {
		t.Errorf("hook vars not reported on the result: %+v", results[0].HookVars)
	}
}

// TestConfigBeforeSendAppliesToEveryStep: the hook lives in shared config, not per step.
func TestConfigBeforeSendAppliesToEveryStep(t *testing.T) {
	var signed int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if len(r.Header.Get("X-Signature")) == 64 {
			atomic.AddInt32(&signed, 1)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	store := vars.NewStore()
	store.Set("api_secret", "s3cr3t")

	cfg := newCfg(srv.URL)
	cfg.BeforeSend = &schema.Hook{Vars: []schema.HookVar{
		{Name: "sig", Expr: "hex(hmac_sha256(request.method + request.path, api_secret))"},
	}}
	cfg.Headers = map[string]string{"X-Signature": "{{ sig }}"}

	steps := []schema.Step{
		currentStep("one", "GET", "/a"),
		currentStep("two", "GET", "/b"),
	}

	if _, _, err := Run(steps, cfg, store, Options{}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if atomic.LoadInt32(&signed) != 2 {
		t.Errorf("expected both steps signed, got %d", atomic.LoadInt32(&signed))
	}
}

// TestHookErrorLeavesNoStaleVariable: a hook failing partway must not leave its
// earlier results behind for the next step to sign with.
func TestHookErrorLeavesNoStaleVariable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	store := vars.NewStore()

	steps := []schema.Step{{
		Name:    "broken",
		Method:  "GET",
		Path:    "/a",
		Origin:  "current",
		OnError: "continue",
		BeforeSend: &schema.Hook{Vars: []schema.HookVar{
			{Name: "ts", Expr: "builtin.timestamp"},
			{Name: "sig", Expr: "no_such_function(ts)"},
		}},
	}}

	results, _, err := Run(steps, newCfg(srv.URL), store, Options{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if results[0].Error == "" {
		t.Fatal("expected the hook error on the step result")
	}
	if !strings.Contains(results[0].Error, "sig") {
		t.Errorf("error should name the failing variable: %s", results[0].Error)
	}
	if _, ok := store.Get("ts"); ok {
		t.Error("ts must not be committed when a later expression fails")
	}
}

func TestDryRunDoesNotEvaluateHooks(t *testing.T) {
	store := vars.NewStore()

	steps := []schema.Step{{
		Name:   "signed",
		Method: "POST",
		Path:   "/orders",
		Origin: "current",
		BeforeSend: &schema.Hook{Vars: []schema.HookVar{
			{Name: "sig", Expr: "no_such_function(1)"},
		}},
	}}

	results, _, err := Run(steps, newCfg("https://example.test"), store, Options{DryRun: true})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if results[0].Error != "" {
		t.Errorf("dry-run must not evaluate hooks, got error: %s", results[0].Error)
	}
	if _, ok := store.Get("sig"); ok {
		t.Error("dry-run must not commit hook variables")
	}
}
