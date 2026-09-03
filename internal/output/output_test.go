package output

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/fatih/color"

	"github.com/nilcolor/apix/internal/assert"
	"github.com/nilcolor/apix/internal/runner"
)

// makeResult builds a StepResult for table tests.
func makeResult(name, method string, status int, passed bool) StepResult {
	assertion := assert.Result{
		Check:    "status",
		Expected: status,
		Actual:   status,
		Passed:   passed,
	}
	if !passed {
		assertion.Message = "expected 200, got 422"
	}
	return StepResult{
		Name:       name,
		Method:     method,
		URL:        "http://x",
		Status:     status,
		DurationMs: 100,
		Assertions: []assert.Result{assertion},
	}
}

// TestJSONOutputStructure verifies the JSON envelope schema matches the spec.
func TestJSONOutputStructure(t *testing.T) {
	results := []StepResult{
		{
			Name:       "login",
			Method:     "POST",
			URL:        "https://api.example.com/auth/login",
			Status:     200,
			DurationMs: 142,
			Assertions: []assert.Result{
				{Check: "status", Expected: 200, Actual: 200, Passed: true},
			},
			Extracted: map[string]string{
				"token":   "eyJ...",
				"user_id": "usr_123",
			},
		},
		{
			Name:       "update_profile",
			Method:     "PATCH",
			URL:        "https://api.example.com/users/usr_123",
			Status:     422,
			DurationMs: 201,
			Assertions: []assert.Result{
				{Check: "status", Expected: 200, Actual: 422, Passed: false, Message: "expected 200, got 422"},
			},
		},
	}
	summary := Summary{Total: 2, Passed: 1, Failed: 1, DurationMs: 343}

	var buf bytes.Buffer
	if err := JSON(results, summary, nil, &buf); err != nil {
		t.Fatalf("JSON: %v", err)
	}

	var out struct {
		Steps []struct {
			Name       string `json:"name"`
			Method     string `json:"method"`
			URL        string `json:"url"`
			Status     int    `json:"status"`
			DurationMs int64  `json:"duration_ms"`
			Assertions []struct {
				Check   string `json:"check"`
				Passed  bool   `json:"passed"`
				Message string `json:"message,omitempty"`
			} `json:"assertions"`
			Extracted map[string]string `json:"extracted"`
		} `json:"steps"`
		Summary struct {
			Total      int   `json:"total"`
			Passed     int   `json:"passed"`
			Failed     int   `json:"failed"`
			DurationMs int64 `json:"duration_ms"`
		} `json:"summary"`
	}
	if err := json.Unmarshal(buf.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if len(out.Steps) != 2 {
		t.Fatalf("steps: want 2, got %d", len(out.Steps))
	}

	s := out.Steps[0]
	if s.Name != "login" || s.Method != "POST" || s.Status != 200 || s.DurationMs != 142 {
		t.Errorf("step[0] fields wrong: %+v", s)
	}
	if s.Extracted["token"] != "***" || s.Extracted["user_id"] != "usr_123" {
		t.Errorf("step[0] extracted wrong: %v", s.Extracted)
	}
	if len(s.Assertions) != 1 || !s.Assertions[0].Passed {
		t.Errorf("step[0] assertion wrong: %+v", s.Assertions)
	}

	s2 := out.Steps[1]
	if len(s2.Assertions) == 0 || s2.Assertions[0].Passed {
		t.Errorf("step[1] should have failing assertion")
	}

	if out.Summary.Total != 2 || out.Summary.Passed != 1 || out.Summary.Failed != 1 || out.Summary.DurationMs != 343 {
		t.Errorf("summary wrong: %+v", out.Summary)
	}
}

// TestJSONEmptyResults verifies the JSON formatter handles empty input cleanly.
func TestJSONEmptyResults(t *testing.T) {
	var buf bytes.Buffer
	if err := JSON(nil, Summary{}, nil, &buf); err != nil {
		t.Fatalf("JSON: %v", err)
	}
	var out struct {
		Steps   []any `json:"steps"`
		Summary any   `json:"summary"`
	}
	if err := json.Unmarshal(buf.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(out.Steps) != 0 {
		t.Errorf("want empty steps, got %v", out.Steps)
	}
}

// TestPrettySummaryCounts checks the summary line reflects passed/failed correctly.
func TestPrettySummaryCounts(t *testing.T) {
	color.NoColor = true
	defer func() { color.NoColor = false }()

	results := []StepResult{
		makeResult("a", "GET", 200, true),
		makeResult("b", "POST", 422, false),
	}
	summary := Summary{Total: 2, Passed: 1, Failed: 1, DurationMs: 432}

	var buf bytes.Buffer
	Pretty(results, summary, nil, &buf, nil)
	out := buf.String()

	if !strings.Contains(out, "1 passed") {
		t.Errorf("want '1 passed' in output, got:\n%s", out)
	}
	if !strings.Contains(out, "1 failed") {
		t.Errorf("want '1 failed' in output, got:\n%s", out)
	}
	if !strings.Contains(out, "432ms total") {
		t.Errorf("want '432ms total' in output, got:\n%s", out)
	}
}

// TestPrettyPassFailIndicators checks ✓/✗ indicators appear on assertion lines.
func TestPrettyPassFailIndicators(t *testing.T) {
	color.NoColor = true
	defer func() { color.NoColor = false }()

	results := []StepResult{
		makeResult("pass_step", "GET", 200, true),
		makeResult("fail_step", "GET", 422, false),
	}
	var buf bytes.Buffer
	Pretty(results, Summary{Total: 2, Passed: 1, Failed: 1, DurationMs: 10}, nil, &buf, nil)
	out := buf.String()

	if !strings.Contains(out, "✓") {
		t.Errorf("want ✓ in output")
	}
	if !strings.Contains(out, "✗") {
		t.Errorf("want ✗ in output")
	}
}

// TestPrettyExtractedValues verifies extracted key=value lines appear.
func TestPrettyExtractedValues(t *testing.T) {
	color.NoColor = true
	defer func() { color.NoColor = false }()

	r := StepResult{
		Name:       "login",
		Method:     "POST",
		URL:        "http://x",
		Status:     200,
		DurationMs: 50,
		Extracted:  map[string]string{"session_id": "abc123"},
	}
	var buf bytes.Buffer
	Pretty([]StepResult{r}, Summary{Total: 1, Passed: 1}, nil, &buf, nil)
	out := buf.String()

	if !strings.Contains(out, "session_id") || !strings.Contains(out, "abc123") {
		t.Errorf("want extracted value in output, got:\n%s", out)
	}
}

// TestPrettyAssertionExpressionFormat verifies passing/failing assertion lines render
// as the resolved "source operator operand" expression, not the bare Check label.
func TestPrettyAssertionExpressionFormat(t *testing.T) {
	color.NoColor = true
	defer func() { color.NoColor = false }()

	r := StepResult{
		Name: "check", Method: "GET", URL: "http://x", Status: 200, DurationMs: 10,
		Assertions: []assert.Result{
			{Check: "status", Source: "status", Operator: "equals", Expected: 200, Actual: 200, Passed: true},
			{Check: "body $.body.role", Source: "$.body.role", Operator: "not_equals", Expected: "admin", Actual: "viewer", Passed: true},
			{Check: "body $.body.tags", Source: "$.body.tags", Operator: "in", Expected: []any{"a", "b"}, Actual: "a", Passed: true},
			{Check: "body $.body.token", Source: "$.body.token", Operator: "exists", Expected: true, Actual: true, Passed: true},
			{Check: "body $.body.age", Source: "$.body.age", Operator: "gte", Expected: 18, Actual: 4, Passed: false},
		},
	}
	var buf bytes.Buffer
	Pretty([]StepResult{r}, Summary{Total: 1, Passed: 0, Failed: 1}, nil, &buf, nil)
	out := buf.String()

	for _, want := range []string{
		"✓ status == 200",
		"✓ $.body.role != admin",
		"✓ $.body.tags in [a, b]",
		"✓ $.body.token exists true",
		"✗ $.body.age >= 18  (got 4)",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("want %q in output, got:\n%s", want, out)
		}
	}
}

// TestPrettyAssertionFallbackForExecutionErrors verifies assertions with no Operator
// (execution errors: missing path, invalid regexp, etc.) keep the old Check: Message form.
func TestPrettyAssertionFallbackForExecutionErrors(t *testing.T) {
	color.NoColor = true
	defer func() { color.NoColor = false }()

	r := StepResult{
		Name: "check", Method: "GET", URL: "http://x", Status: 200, DurationMs: 10,
		Assertions: []assert.Result{
			{Check: "body $.body.missing", Passed: false, Message: `JSONPath "$.body.missing" matched nothing`},
		},
	}
	var buf bytes.Buffer
	Pretty([]StepResult{r}, Summary{Total: 1, Passed: 0, Failed: 1}, nil, &buf, nil)
	out := buf.String()

	if !strings.Contains(out, `✗ body $.body.missing: JSONPath "$.body.missing" matched nothing`) {
		t.Errorf("want fallback Check: Message form, got:\n%s", out)
	}
}

// TestPrettyVerboseDump checks that request/response sections appear in verbose mode.
func TestPrettyVerboseDump(t *testing.T) {
	color.NoColor = true
	defer func() { color.NoColor = false }()

	r := StepResult{
		Name:       "login",
		Method:     "POST",
		URL:        "http://api.example.com/login",
		Status:     200,
		DurationMs: 50,
		Request: &runner.RequestSnapshot{
			Method:  "POST",
			URL:     "http://api.example.com/login",
			Headers: http.Header{"Content-Type": {"application/json"}},
			Body:    []byte(`{"username":"user","password":"***"}`),
		},
		Response: &runner.Response{
			Status:  200,
			Headers: http.Header{"Content-Type": {"application/json"}},
			Body:    []byte(`{"token":"eyJ..."}`),
		},
	}
	var buf bytes.Buffer
	PrettyVerbose([]StepResult{r}, Summary{Total: 1, Passed: 1}, nil, &buf, nil)
	out := buf.String()

	if !strings.Contains(out, "Request") {
		t.Errorf("want 'Request' section in verbose output")
	}
	if !strings.Contains(out, "Response") {
		t.Errorf("want 'Response' section in verbose output")
	}
	// Masking already applied by runner; verify the masked value passes through.
	if !strings.Contains(out, `"password":"***"`) {
		t.Errorf("want masked password in request dump, got:\n%s", out)
	}
}

// TestNoColorDisablesANSI verifies that color.NoColor = true removes escape codes.
func TestNoColorDisablesANSI(t *testing.T) {
	color.NoColor = true
	defer func() { color.NoColor = false }()

	r := makeResult("step", "GET", 200, true)
	var buf bytes.Buffer
	Pretty([]StepResult{r}, Summary{Total: 1, Passed: 1}, nil, &buf, nil)
	out := buf.String()

	if strings.Contains(out, "\x1b[") {
		t.Errorf("expected no ANSI escape codes when NoColor=true, got:\n%q", out)
	}
}

// TestPrettyPrintedGoesToPrintOut verifies print: value is written to printOut, not w.
func TestPrettyPrintedGoesToPrintOut(t *testing.T) {
	color.NoColor = true
	defer func() { color.NoColor = false }()

	r := StepResult{
		Name:       "get_body",
		Method:     "GET",
		URL:        "http://x",
		Status:     200,
		DurationMs: 10,
		Printed:    `{"key":"value"}`,
	}
	var pretty, printOut bytes.Buffer
	Pretty([]StepResult{r}, Summary{Total: 1, Passed: 1}, nil, &pretty, &printOut)

	if strings.Contains(pretty.String(), `"key"`) {
		t.Errorf("printed content should not appear in pretty writer, got:\n%s", pretty.String())
	}
	if !strings.Contains(printOut.String(), `"key"`) {
		t.Errorf("printed content should appear in printOut writer, got:\n%s", printOut.String())
	}
}

// TestPrettyEmptyResults verifies empty input produces only a summary line.
func TestPrettyEmptyResults(t *testing.T) {
	color.NoColor = true
	defer func() { color.NoColor = false }()

	var buf bytes.Buffer
	Pretty(nil, Summary{}, nil, &buf, nil)
	out := buf.String()

	if !strings.Contains(out, "passed") || !strings.Contains(out, "failed") {
		t.Errorf("want summary line even for empty results, got:\n%s", out)
	}
}

func TestExtractedSensitiveNameMasked(t *testing.T) {
	color.NoColor = true
	t.Cleanup(func() { color.NoColor = false })

	results := []StepResult{{
		Name:      "login",
		Method:    "POST",
		Status:    200,
		Extracted: map[string]string{"token": "sup3rs3cr3tvalue", "user_id": "42"},
	}}

	var buf bytes.Buffer
	Pretty(results, Summary{Total: 1, Passed: 1}, nil, &buf, nil)

	out := buf.String()
	if strings.Contains(out, "sup3rs3cr3tvalue") {
		t.Errorf("extracted token must be masked:\n%s", out)
	}
	if !strings.Contains(out, "42") {
		t.Errorf("non-sensitive extracted value should still print:\n%s", out)
	}
}

func TestHookVarCarryingSecretRedacted(t *testing.T) {
	color.NoColor = true
	t.Cleanup(func() { color.NoColor = false })

	const secret = "topsecretvalue"
	results := []StepResult{{
		Name:      "signed",
		Method:    "POST",
		Status:    200,
		Extracted: map[string]string{"api_secret": secret},
		HookVars: map[string]string{
			"canonical": "POST/orders" + secret,
			"sig":       "abc123",
		},
	}}

	var buf bytes.Buffer
	Pretty(results, Summary{Total: 1, Passed: 1}, nil, &buf, nil)

	out := buf.String()
	if strings.Contains(out, secret) {
		t.Errorf("secret leaked through a non-sensitive hook var name:\n%s", out)
	}
	if !strings.Contains(out, "abc123") {
		t.Errorf("non-sensitive hook var should still print:\n%s", out)
	}
}

func TestJSONRedactsHookVarsAndError(t *testing.T) {
	const secret = "topsecretvalue"
	results := []StepResult{{
		Name:      "signed",
		Method:    "POST",
		Status:    500,
		Extracted: map[string]string{"api_secret": secret},
		HookVars:  map[string]string{"canonical": "POST/orders" + secret},
		Error:     "hook \"sig\": bad input " + secret,
	}}

	var buf bytes.Buffer
	if err := JSON(results, Summary{Total: 1, Failed: 1}, nil, &buf); err != nil {
		t.Fatalf("JSON: %v", err)
	}
	if strings.Contains(buf.String(), secret) {
		t.Errorf("secret leaked into JSON output:\n%s", buf.String())
	}
}

func TestShortSensitiveValueDoesNotBlankOutput(t *testing.T) {
	color.NoColor = true
	t.Cleanup(func() { color.NoColor = false })

	results := []StepResult{{
		Name:      "step",
		Method:    "GET",
		Status:    200,
		Extracted: map[string]string{"token": "ab", "note": "abstract"},
	}}

	var buf bytes.Buffer
	Pretty(results, Summary{Total: 1, Passed: 1}, nil, &buf, nil)

	if !strings.Contains(buf.String(), "abstract") {
		t.Errorf("a 2-char secret must not scrub unrelated values:\n%s", buf.String())
	}
}

// The secret lives only in the variable store — not in Extracted or HookVars.
// An earlier version seeded the redactor from step results alone and leaked here.
func TestStoreSecretRedactedFromHookVar(t *testing.T) {
	color.NoColor = true
	t.Cleanup(func() { color.NoColor = false })

	const secret = "topsecretvalue"
	results := []StepResult{{
		Name:     "signed",
		Method:   "POST",
		Status:   200,
		HookVars: map[string]string{"canonical": "POST" + secret + "1788436825"},
	}}
	storeVars := map[string]string{"api_secret": secret, "base_url": "http://x"}

	var buf bytes.Buffer
	Pretty(results, Summary{Total: 1, Passed: 1}, storeVars, &buf, nil)
	if strings.Contains(buf.String(), secret) {
		t.Errorf("store secret leaked through hook var:\n%s", buf.String())
	}

	buf.Reset()
	if err := JSON(results, Summary{Total: 1, Passed: 1}, storeVars, &buf); err != nil {
		t.Fatalf("JSON: %v", err)
	}
	if strings.Contains(buf.String(), secret) {
		t.Errorf("store secret leaked into JSON:\n%s", buf.String())
	}
}

// Redaction must hold at the output boundary, not only on the fields someone
// remembered. The verbose request/response dumps were the omission.
func TestVerboseDumpsAreScrubbed(t *testing.T) {
	color.NoColor = true
	t.Cleanup(func() { color.NoColor = false })

	const secret = "topsecretvalue"
	results := []StepResult{{
		Name:   "leak",
		Method: "POST",
		URL:    "http://x/orders",
		Status: 200,
		Request: &runner.RequestSnapshot{
			Method:  "POST",
			URL:     "http://x/orders",
			Headers: http.Header{"X-Trace": []string{"trace-" + secret}},
			Body:    []byte(`{"note":"prefix-` + secret + `-suffix"}`),
		},
		Response: &runner.Response{
			Status:  200,
			Headers: http.Header{"X-Echo": []string{secret}},
			Body:    []byte(`{"echo":"` + secret + `"}`),
		},
	}}

	var buf bytes.Buffer
	PrettyVerbose(results, Summary{Total: 1, Passed: 1}, map[string]string{"api_secret": secret}, &buf, nil)

	if strings.Contains(buf.String(), secret) {
		t.Errorf("secret survived the verbose dumps:\n%s", buf.String())
	}
	if !strings.Contains(buf.String(), "***") {
		t.Errorf("expected redaction markers in the dump:\n%s", buf.String())
	}
}

func TestRedactWriterScrubs(t *testing.T) {
	const secret = "topsecretvalue"
	results := []StepResult{{Name: "s", Printed: "secret is " + secret}}

	var buf bytes.Buffer
	w := RedactWriter(results, map[string]string{"api_secret": secret}, &buf)
	if _, err := w.Write([]byte("secret is " + secret + "\n")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if strings.Contains(buf.String(), secret) {
		t.Errorf("RedactWriter did not scrub: %q", buf.String())
	}
}

// One sensitive value contained in another must not depend on map order.
func TestRedactionIsOrderStable(t *testing.T) {
	secrets := map[string]string{
		"api_secret":   "abcdefghijkl",
		"api_token":    "abcdef",
		"api_password": "abcdefghi",
	}
	for i := 0; i < 20; i++ {
		var buf bytes.Buffer
		w := RedactWriter(nil, secrets, &buf)
		_, _ = w.Write([]byte("value=abcdefghijkl"))
		if buf.String() != "value=***" {
			t.Fatalf("unstable redaction on run %d: %q", i, buf.String())
		}
	}
}

// A secret is escaped by the time it reaches the JSON writer, so scrubbing the
// plain form alone silently misses it. Covers every escape class, not just &<>.
func TestJSONEscapedSecretsAreScrubbed(t *testing.T) {
	secrets := []string{
		"TOP&SECRET&VALUE",
		`quote"inside`,
		`back\slash`,
		"tab\there",
		"angle<brackets>",
		"unicode—dash",
	}

	for _, secret := range secrets {
		results := []StepResult{{
			Name:     "leak",
			Method:   "POST",
			Status:   200,
			HookVars: map[string]string{"canonical": "POST" + secret},
		}}

		var buf bytes.Buffer
		if err := JSON(results, Summary{Total: 1, Passed: 1}, map[string]string{"api_secret": secret}, &buf); err != nil {
			t.Fatalf("JSON: %v", err)
		}

		var out struct {
			Steps []struct {
				HookVars map[string]string `json:"hook_vars"`
			} `json:"steps"`
		}
		if err := json.Unmarshal(buf.Bytes(), &out); err != nil {
			t.Fatalf("unmarshal %q: %v\n%s", secret, err, buf.String())
		}
		if strings.Contains(out.Steps[0].HookVars["canonical"], secret) {
			t.Errorf("secret %q survived JSON encoding: %q", secret, out.Steps[0].HookVars["canonical"])
		}
	}
}
