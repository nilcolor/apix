package output

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/fatih/color"

	"github.com/nilcolor/apix/internal/assert"
	"github.com/nilcolor/apix/internal/runner"
)

// StepResult holds the result of executing a single step.
type StepResult struct {
	Name       string
	Method     string
	URL        string
	Status     int
	DurationMs int64
	Assertions []assert.Result
	Extracted  map[string]string
	Printed    string
	Request    *runner.RequestSnapshot
	Response   *runner.Response
	Error      string
}

// Summary aggregates results across all steps.
type Summary struct {
	Total      int
	Passed     int
	Failed     int
	DurationMs int64
}

var (
	colorStep    = color.New(color.FgCyan, color.Bold)
	colorPass    = color.New(color.FgGreen)
	colorFail    = color.New(color.FgRed)
	colorExtract = color.New(color.FgYellow)
	colorMeta    = color.New(color.FgHiBlack)
	colorHeader  = color.New(color.FgWhite, color.Bold)
)

// Pretty writes human-readable output for each step and a summary line to w.
// printOut receives print: values (one per step, after the step block); pass nil to suppress.
func Pretty(results []StepResult, summary Summary, w, printOut io.Writer) {
	for i := range results {
		if i > 0 {
			fmt.Fprintln(w)
		}
		printStepHeader(results[i], w)
		printStepStatus(results[i], w)
		for _, a := range results[i].Assertions {
			printAssertion(a, w)
		}
		if results[i].Error != "" {
			colorFail.Fprintf(w, "  ✗ error: %s\n", results[i].Error)
		}
		for k, v := range results[i].Extracted {
			colorExtract.Fprintf(w, "  → %-10s = %s\n", k, v)
		}
		if results[i].Printed != "" && printOut != nil {
			fmt.Fprintln(printOut, indentLines(results[i].Printed, "  "))
		}
	}
	printSummary(summary, w)
}

// PrettyVerbose writes Pretty output plus full request/response dumps per step.
// printOut receives print: values (one per step, after the step block); pass nil to suppress.
func PrettyVerbose(results []StepResult, summary Summary, w, printOut io.Writer) {
	for i := range results {
		if i > 0 {
			fmt.Fprintln(w)
		}
		printStepHeader(results[i], w)
		if results[i].Request != nil {
			printRequestDump(results[i].Request, w)
		}
		if results[i].Response != nil {
			printResponseDump(results[i].Response, w)
		}
		fmt.Fprintln(w)
		printStepStatus(results[i], w)
		for _, a := range results[i].Assertions {
			printAssertion(a, w)
		}
		if results[i].Error != "" {
			colorFail.Fprintf(w, "  ✗ error: %s\n", results[i].Error)
		}
		for k, v := range results[i].Extracted {
			colorExtract.Fprintf(w, "  → %-10s = %s\n", k, v)
		}
		if results[i].Printed != "" && printOut != nil {
			fmt.Fprintln(printOut, indentLines(results[i].Printed, "  "))
		}
	}
	printSummary(summary, w)
}

// JSON writes structured JSON output to w.
func JSON(results []StepResult, summary Summary, w io.Writer) error {
	type assertionJSON struct {
		Check    string `json:"check"`
		Expected any    `json:"expected"`
		Actual   any    `json:"actual"`
		Passed   bool   `json:"passed"`
	}
	type stepJSON struct {
		Name       string            `json:"name"`
		Method     string            `json:"method"`
		URL        string            `json:"url"`
		Status     int               `json:"status"`
		DurationMs int64             `json:"duration_ms"`
		Assertions []assertionJSON   `json:"assertions"`
		Extracted  map[string]string `json:"extracted,omitempty"`
		Printed    string            `json:"printed,omitempty"`
		Error      string            `json:"error,omitempty"`
	}
	type summaryJSON struct {
		Total      int   `json:"total"`
		Passed     int   `json:"passed"`
		Failed     int   `json:"failed"`
		DurationMs int64 `json:"duration_ms"`
	}
	type envelope struct {
		Steps   []stepJSON  `json:"steps"`
		Summary summaryJSON `json:"summary"`
	}

	steps := make([]stepJSON, len(results))
	for i := range results {
		assertions := make([]assertionJSON, len(results[i].Assertions))
		for j, a := range results[i].Assertions {
			assertions[j] = assertionJSON{
				Check:    a.Check,
				Expected: a.Expected,
				Actual:   a.Actual,
				Passed:   a.Passed,
			}
		}
		steps[i] = stepJSON{
			Name:       results[i].Name,
			Method:     results[i].Method,
			URL:        results[i].URL,
			Status:     results[i].Status,
			DurationMs: results[i].DurationMs,
			Assertions: assertions,
			Extracted:  results[i].Extracted,
			Printed:    results[i].Printed,
			Error:      results[i].Error,
		}
	}

	out := envelope{
		Steps: steps,
		Summary: summaryJSON(summary),
	}

	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}

// Silent is a no-op formatter; routing decisions are centralized here.
func Silent() {}

// --- internal helpers ---

func indentLines(s, prefix string) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		if l != "" {
			lines[i] = prefix + l
		}
	}
	return strings.Join(lines, "\n")
}

func printStepHeader(r StepResult, w io.Writer) {
	colorStep.Fprintf(w, "● %s", r.Name)
	fmt.Fprintf(w, "   %s %s\n", r.Method, r.URL)
}

func printStepStatus(r StepResult, w io.Writer) {
	if r.Status == 0 {
		return
	}
	statusText := http.StatusText(r.Status)
	if stepPassed(r) {
		colorPass.Fprintf(w, "  ✓ %d %s  (%dms)\n", r.Status, statusText, r.DurationMs)
	} else {
		colorFail.Fprintf(w, "  ✗ %d %s  (%dms)\n", r.Status, statusText, r.DurationMs)
	}
}

func stepPassed(r StepResult) bool {
	if r.Error != "" {
		return false
	}
	for _, a := range r.Assertions {
		if !a.Passed {
			return false
		}
	}
	return true
}

func printAssertion(a assert.Result, w io.Writer) {
	if a.Passed {
		colorPass.Fprintf(w, "  ✓ %s\n", a.Check)
	} else {
		msg := a.Message
		if msg == "" {
			msg = fmt.Sprintf("expected %v, got %v", a.Expected, a.Actual)
		}
		colorFail.Fprintf(w, "  ✗ %s: %s\n", a.Check, msg)
	}
}

func printSummary(summary Summary, w io.Writer) {
	fmt.Fprintln(w)
	colorMeta.Fprintln(w, strings.Repeat("─", 38))
	passedStr := colorPass.Sprintf("%d passed", summary.Passed)
	failedStr := colorFail.Sprintf("%d failed", summary.Failed)
	fmt.Fprintf(w, "  %s · %s · %dms total\n", passedStr, failedStr, summary.DurationMs)
}

func printRequestDump(snap *runner.RequestSnapshot, w io.Writer) {
	fmt.Fprintln(w)
	colorHeader.Fprintln(w, "  Request")
	u, _ := url.Parse(snap.URL)
	path := "/"
	if u != nil && u.RequestURI() != "" {
		path = u.RequestURI()
	}
	fmt.Fprintf(w, "    %s %s HTTP/1.1\n", snap.Method, path)
	if u != nil {
		fmt.Fprintf(w, "    Host: %s\n", u.Host)
	}
	for k, vs := range snap.Headers {
		fmt.Fprintf(w, "    %s: %s\n", k, strings.Join(vs, ", "))
	}
	if len(snap.Body) > 0 {
		fmt.Fprintln(w)
		fmt.Fprintf(w, "    %s\n", snap.Body)
	}
}

func printResponseDump(resp *runner.Response, w io.Writer) {
	fmt.Fprintln(w)
	colorHeader.Fprintln(w, "  Response")
	statusText := http.StatusText(resp.Status)
	fmt.Fprintf(w, "    HTTP/1.1 %d %s\n", resp.Status, statusText)
	for k, vs := range resp.Headers {
		fmt.Fprintf(w, "    %s: %s\n", k, strings.Join(vs, ", "))
	}
	if len(resp.Body) > 0 {
		fmt.Fprintln(w)
		fmt.Fprintf(w, "    %s\n", resp.Body)
	}
}
