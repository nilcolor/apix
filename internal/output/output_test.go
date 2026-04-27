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
	if err := JSON(results, summary, &buf); err != nil {
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
	if s.Extracted["token"] != "eyJ..." || s.Extracted["user_id"] != "usr_123" {
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
	if err := JSON(nil, Summary{}, &buf); err != nil {
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
	Pretty(results, summary, &buf, nil)
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
	Pretty(results, Summary{Total: 2, Passed: 1, Failed: 1, DurationMs: 10}, &buf, nil)
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
		Name:      "login",
		Method:    "POST",
		URL:       "http://x",
		Status:    200,
		DurationMs: 50,
		Extracted: map[string]string{"token": "abc123"},
	}
	var buf bytes.Buffer
	Pretty([]StepResult{r}, Summary{Total: 1, Passed: 1}, &buf, nil)
	out := buf.String()

	if !strings.Contains(out, "token") || !strings.Contains(out, "abc123") {
		t.Errorf("want extracted token in output, got:\n%s", out)
	}
}

// TestPrettyVerboseDump checks that request/response sections appear in verbose mode.
func TestPrettyVerboseDump(t *testing.T) {
	color.NoColor = true
	defer func() { color.NoColor = false }()

	r := StepResult{
		Name:      "login",
		Method:    "POST",
		URL:       "http://api.example.com/login",
		Status:    200,
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
	PrettyVerbose([]StepResult{r}, Summary{Total: 1, Passed: 1}, &buf, nil)
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
	Pretty([]StepResult{r}, Summary{Total: 1, Passed: 1}, &buf, nil)
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
	Pretty([]StepResult{r}, Summary{Total: 1, Passed: 1}, &pretty, &printOut)

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
	Pretty(nil, Summary{}, &buf, nil)
	out := buf.String()

	if !strings.Contains(out, "passed") || !strings.Contains(out, "failed") {
		t.Errorf("want summary line even for empty results, got:\n%s", out)
	}
}
