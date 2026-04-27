package pipeline

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

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
