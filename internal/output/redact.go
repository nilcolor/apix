package output

import (
	"encoding/json"
	"io"
	"sort"
	"strings"

	"github.com/nilcolor/apix/internal/runner"
)

// minRedactLength keeps a short or empty secret from blanking unrelated output.
const minRedactLength = 6

// redactor replaces the values of sensitive-named variables wherever they appear,
// catching values derived from a secret under a name that does not look sensitive.
// It is output hygiene, not a security control: everything it touches is printed
// to the terminal of the person who supplied the secret.
type redactor struct {
	values []string
}

// newRedactor collects sensitive values from every variable a run produced.
func newRedactor(results []StepResult, extra map[string]string) *redactor {
	seen := map[string]bool{}
	add := func(m map[string]string) {
		for k, v := range m {
			if runner.IsSensitive(k) && len(v) >= minRedactLength {
				seen[v] = true
			}
		}
	}
	add(extra)
	for i := range results {
		add(results[i].Extracted)
		add(results[i].HookVars)
	}

	r := &redactor{}
	for v := range seen {
		r.values = append(r.values, v)
		// A secret is escaped by the time it reaches a JSON writer, so the plain
		// form never matches. json.Marshal escapes exactly as json.Encoder does,
		// which gets every escape class without enumerating any of them.
		if esc := jsonForm(v); esc != "" && esc != v {
			r.values = append(r.values, esc)
		}
	}

	// Longest first, so a value that contains another is replaced whole; map order
	// would otherwise make the output vary between runs.
	sort.Slice(r.values, func(i, j int) bool {
		if len(r.values[i]) != len(r.values[j]) {
			return len(r.values[i]) > len(r.values[j])
		}
		return r.values[i] < r.values[j]
	})
	return r
}

// jsonForm returns v as it appears inside a JSON string, without the quotes.
func jsonForm(v string) string {
	b, err := json.Marshal(v)
	if err != nil || len(b) < 2 {
		return ""
	}
	return string(b[1 : len(b)-1])
}

// scrub replaces every known sensitive value found in s.
func (r *redactor) scrub(s string) string {
	if r == nil || len(r.values) == 0 || s == "" {
		return s
	}
	for _, v := range r.values {
		s = strings.ReplaceAll(s, v, "***")
	}
	return s
}

// report masks a variable by name. Values are scrubbed at the output boundary,
// not here.
func (r *redactor) report(name, value string) string {
	if runner.IsSensitive(name) {
		return "***"
	}
	return value
}

// writer wraps w so every write is scrubbed. Redaction belongs at the boundary:
// scrubbing chosen fields means a field added later is unprotected by default,
// and the verbose request/response dumps were exactly that omission.
func (r *redactor) writer(w io.Writer) io.Writer {
	if w == nil || r == nil || len(r.values) == 0 {
		return w
	}
	return &redactWriter{w: w, r: r}
}

type redactWriter struct {
	w io.Writer
	r *redactor
}

// Write scrubs each write in isolation. Callers here emit one line per call, so a
// value is never split across writes.
func (rw *redactWriter) Write(p []byte) (int, error) {
	scrubbed := rw.r.scrub(string(p))
	if _, err := rw.w.Write([]byte(scrubbed)); err != nil {
		return 0, err
	}
	return len(p), nil
}

// reportMap applies report to every entry.
func (r *redactor) reportMap(m map[string]string) map[string]string {
	if len(m) == 0 {
		return nil
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = r.report(k, v)
	}
	return out
}

// RedactWriter wraps w so writes are scrubbed of the run's sensitive values.
// Callers that emit step output without going through a formatter — silent mode's
// print: pass-through — must route through this, or redaction depends on which
// output mode the user picked.
func RedactWriter(results []StepResult, secrets map[string]string, w io.Writer) io.Writer {
	return newRedactor(results, secrets).writer(w)
}
