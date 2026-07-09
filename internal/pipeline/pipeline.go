package pipeline

import (
	"bufio"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"os"
	"strings"
	"time"

	"github.com/nilcolor/apix/internal/assert"
	"github.com/nilcolor/apix/internal/extract"
	"github.com/nilcolor/apix/internal/output"
	"github.com/nilcolor/apix/internal/runner"
	"github.com/nilcolor/apix/internal/schema"
	"github.com/nilcolor/apix/internal/vars"
)

// Options controls pipeline execution behavior.
type Options struct {
	DryRun   bool
	FailFast bool
	Timeout  time.Duration
	Step     []string
	Skip     []string
	From     string
	// Stderr receives warning messages; defaults to os.Stderr when nil.
	Stderr io.Writer
	// Stdin supplies answers for ask: prompts; defaults to os.Stdin when nil.
	Stdin io.Reader
}

// Run executes steps in order, applying filtering, variable extraction, and assertions.
// The error return is reserved for fatal pre/post-execution failures; per-step errors
// (network, extraction) are recorded in StepResult.Error and follow on_error semantics.
func Run(steps []schema.Step, cfg *schema.Config, store *vars.Store, opts Options) ([]output.StepResult, output.Summary, error) {
	stderr := opts.Stderr
	if stderr == nil {
		stderr = os.Stderr
	}
	stdin := opts.Stdin
	if stdin == nil {
		stdin = os.Stdin
	}

	var jar http.CookieJar
	if cfg.UseCookieJar {
		jar, _ = cookiejar.New(nil)
	}

	start := time.Now()
	var results []output.StepResult
	fromReached := opts.From == ""

	for i := range steps {
		step := steps[i]

		if !shouldRun(step, opts, &fromReached) {
			continue
		}

		if step.Retry != nil {
			fmt.Fprintf(stderr, "warning: step %q has retry: set — retry execution is not yet supported\n", step.Name)
		}

		var result output.StepResult
		if opts.DryRun {
			result = dryRunStep(step, cfg, store)
		} else {
			result = executeStep(step, cfg, store, jar)
		}

		// Propagate extracted values into the store before print so that
		// {{ }} templates in print: can reference this step's own extracted vars.
		for k, v := range result.Extracted {
			store.Set(k, v)
		}

		// ask: only runs for steps that actually succeeded, and never during
		// --dry-run (no real request was made, so there's nothing to gate on).
		if len(step.Ask) > 0 && result.Error == "" && !opts.DryRun {
			asked, askErr := askStep(step.Ask, store, stdin, stderr)
			result.Asked = asked
			if askErr != nil {
				result.Error = askErr.Error()
			}
		}

		if step.Print != "" && result.Response != nil {
			var printed string
			var printErr error
			if strings.Contains(step.Print, "{{") {
				printed, printErr = vars.Interpolate(step.Print, store)
			} else {
				printed, printErr = extract.PrintSource(step.Print, result.Response)
			}
			if printErr != nil {
				fmt.Fprintf(stderr, "warning: print %q: %v\n", step.Print, printErr)
			} else {
				result.Printed = printed
			}
		}

		results = append(results, result)

		if shouldStop(result, step, opts) {
			break
		}
	}

	return results, buildSummary(results, time.Since(start)), nil
}

// shouldRun reports whether a step should be executed given the current filter options.
// Included-origin steps always run regardless of --step/--from/--skip.
func shouldRun(step schema.Step, opts Options, fromReached *bool) bool {
	if step.Origin == "included" {
		return true
	}

	// --skip takes priority over everything.
	for _, name := range opts.Skip {
		if name == step.Name {
			return false
		}
	}

	// --from: skip current steps until the named step is encountered.
	if opts.From != "" && !*fromReached {
		if step.Name != opts.From {
			return false
		}
		*fromReached = true
	}

	// --step whitelist: only run steps whose names are explicitly listed.
	if len(opts.Step) > 0 {
		for _, name := range opts.Step {
			if name == step.Name {
				return true
			}
		}
		return false
	}

	return true
}

// shouldStop returns true when the pipeline should halt after the given result.
func shouldStop(result output.StepResult, step schema.Step, opts Options) bool {
	if !stepFailed(result) {
		return false
	}
	return opts.FailFast || step.OnError != "continue"
}

func stepFailed(r output.StepResult) bool {
	if r.Error != "" {
		return true
	}
	for _, a := range r.Assertions {
		if !a.Passed {
			return true
		}
	}
	return false
}

// executeStep performs an HTTP request, extracts values, and evaluates assertions.
// Errors (network, extraction) are captured in StepResult.Error rather than returned.
func executeStep(step schema.Step, cfg *schema.Config, store *vars.Store, jar http.CookieJar) output.StepResult {
	resp, err := runner.Execute(&step, cfg, store, jar)
	if err != nil {
		return output.StepResult{
			Name:   step.Name,
			Method: methodOrDefault(step.Method),
			Error:  err.Error(),
		}
	}

	extracted, extErr := extract.Extract(step.Extract, resp)
	errStr := ""
	if extErr != nil {
		errStr = extErr.Error()
	}

	var assertions []assert.Result
	if extErr == nil {
		assertions = assert.Evaluate(step.Assert, resp, store)
	}

	snap := resp.Request
	return output.StepResult{
		Name:       step.Name,
		Method:     snap.Method,
		URL:        snap.URL,
		Status:     resp.Status,
		DurationMs: resp.Duration.Milliseconds(),
		Assertions: assertions,
		Extracted:  extracted,
		Request:    &snap,
		Response:   resp,
		Error:      errStr,
	}
}

// askStep prompts for each item not already present in the store, storing answers
// as variables. Already-set vars (e.g. via --var) are reported but never prompted.
// Errors reading stdin stop processing further items and are returned to the caller,
// which records them on StepResult.Error following the same on_error semantics as
// extraction errors.
func askStep(items []schema.AskItem, store *vars.Store, stdin io.Reader, stderr io.Writer) (map[string]string, error) {
	asked := make(map[string]string, len(items))
	reader := bufio.NewReader(stdin)

	for _, item := range items {
		if item.Var == "" {
			return asked, fmt.Errorf("ask: var must not be empty")
		}

		if v, ok := store.Get(item.Var); ok {
			asked[item.Var] = reportValue(item.Var, v)
			continue
		}

		prompt, err := vars.Interpolate(item.Prompt, store)
		if err != nil {
			prompt = item.Prompt
			fmt.Fprintf(stderr, "warning: ask %q prompt: %v\n", item.Var, err)
		}
		fmt.Fprintf(stderr, "%s ", prompt)

		line, readErr := reader.ReadString('\n')
		line = strings.TrimRight(line, "\r\n")
		if readErr != nil && line == "" {
			return asked, fmt.Errorf("ask %q: %w", item.Var, readErr)
		}

		store.Set(item.Var, line)
		asked[item.Var] = reportValue(item.Var, line)
	}

	return asked, nil
}

// reportValue masks the value for display when the var name matches the same
// sensitive-field heuristic used for request/response snapshot masking.
func reportValue(name, value string) string {
	if runner.IsSensitive(name) {
		return "***"
	}
	return value
}

// dryRunStep builds a StepResult from interpolated fields without sending an HTTP request.
func dryRunStep(step schema.Step, cfg *schema.Config, store *vars.Store) output.StepResult {
	method := methodOrDefault(step.Method)
	rawURL := resolveURL(step, cfg, store)
	return output.StepResult{
		Name:   step.Name,
		Method: method,
		URL:    rawURL,
		Request: &runner.RequestSnapshot{
			Method:  method,
			URL:     rawURL,
			Headers: http.Header{},
		},
	}
}

// resolveURL builds the final URL for a step by interpolating variables.
// Errors during interpolation are silently ignored (dry-run best-effort).
func resolveURL(step schema.Step, cfg *schema.Config, store *vars.Store) string {
	if step.URL != "" {
		u, _ := vars.Interpolate(step.URL, store)
		return u
	}
	base, _ := vars.Interpolate(cfg.BaseURL, store)
	path, _ := vars.Interpolate(step.Path, store)
	return strings.TrimRight(base, "/") + "/" + strings.TrimLeft(path, "/")
}

func methodOrDefault(m string) string {
	if m == "" {
		return http.MethodGet
	}
	return m
}

func buildSummary(results []output.StepResult, elapsed time.Duration) output.Summary {
	s := output.Summary{
		Total:      len(results),
		DurationMs: elapsed.Milliseconds(),
	}
	for i := range results {
		if stepFailed(results[i]) {
			s.Failed++
		} else {
			s.Passed++
		}
	}
	return s
}
