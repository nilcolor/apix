package assert

import (
	"fmt"
	"math"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/ohler55/ojg/jp"
	"github.com/ohler55/ojg/oj"

	"github.com/nilcolor/apix/internal/runner"
	"github.com/nilcolor/apix/internal/schema"
	"github.com/nilcolor/apix/internal/vars"
)

// Result holds the outcome of a single assertion check.
type Result struct {
	Check    string
	Source   string
	Operator string
	Expected any
	Actual   any
	Passed   bool
	Message  string
}

// Evaluate runs all assertions in asserts against resp and returns one Result per check.
// Assertion values/operands are interpolated against store before comparison, so
// "{{ name }}" templates may reference variables (including ones extracted by earlier
// steps) in either assertion form.
func Evaluate(asserts *schema.Assert, resp *runner.Response, store *vars.Store) []Result {
	if asserts == nil {
		return nil
	}
	var results []Result

	// Status assertion.
	if asserts.Status != nil {
		resolved, err := resolveAssertion(*asserts.Status, store)
		if err != nil {
			results = append(results, Result{Check: "status", Passed: false, Message: err.Error()})
		} else {
			results = append(results, check("status", "status", any(resp.Status), resolved))
		}
	}

	// Body assertions — each key is a JSONPath expression.
	for path, assertion := range asserts.Body {
		resolved, err := resolveAssertion(assertion, store)
		if err != nil {
			results = append(results, Result{Check: "body " + path, Passed: false, Message: err.Error()})
			continue
		}
		actual, err := bodyValue(path, resp.Body)
		if err != nil {
			// For "exists" operator, a missing path means exists=false.
			if resolved.IsOperator && resolved.Operator == "exists" {
				results = append(results, applyOperator("body "+path, path, nil, "exists", resolved.Operand))
			} else {
				results = append(results, Result{
					Check:   "body " + path,
					Passed:  false,
					Message: err.Error(),
				})
			}
			continue
		}
		results = append(results, check("body "+path, path, actual, resolved))
	}

	// Header assertions.
	for name, assertion := range asserts.Headers {
		resolved, err := resolveAssertion(assertion, store)
		if err != nil {
			results = append(results, Result{Check: "header " + name, Passed: false, Message: err.Error()})
			continue
		}
		actual, err := headerValue(name, resp.Headers)
		if err != nil {
			results = append(results, Result{
				Check:   "header " + name,
				Passed:  false,
				Message: err.Error(),
			})
			continue
		}
		results = append(results, check("header "+name, "header."+name, actual, resolved))
	}

	return results
}

// resolveAssertion interpolates "{{ }}" templates in an assertion's value/operand
// against store, coercing the result back to the Go type each operator expects:
// bool for "exists", []any for "in", string otherwise (the comparison helpers below
// already coerce numeric strings via toFloat64, so no further conversion is needed).
func resolveAssertion(a schema.Assertion, store *vars.Store) (schema.Assertion, error) {
	if !a.IsOperator {
		v, err := interpolateAny(a.Value, store)
		if err != nil {
			return a, err
		}
		a.Value = v
		return a, nil
	}

	var err error
	switch a.Operator {
	case "exists":
		a.Operand, err = interpolateBool(a.Operand, store)
	case "in":
		a.Operand, err = interpolateList(a.Operand, store)
	default:
		a.Operand, err = interpolateAny(a.Operand, store)
	}
	if err != nil {
		return a, err
	}
	return a, nil
}

// interpolateAny interpolates v if it's a string containing "{{"; other types (already
// native from YAML, e.g. a bool or number literal) pass through unchanged.
func interpolateAny(v any, store *vars.Store) (any, error) {
	s, ok := v.(string)
	if !ok || !strings.Contains(s, "{{") {
		return v, nil
	}
	return vars.Interpolate(s, store)
}

// interpolateBool interpolates a string operand and parses the result as a bool for the
// "exists" operator. A non-string operand (already a native YAML bool) passes through.
func interpolateBool(v any, store *vars.Store) (any, error) {
	s, ok := v.(string)
	if !ok {
		return v, nil
	}
	resolved, err := interpolateAny(s, store)
	if err != nil {
		return nil, err
	}
	str := resolved.(string)
	b, err := strconv.ParseBool(strings.TrimSpace(str))
	if err != nil {
		// Not a recognized boolean literal — let applyOperator's type assertion
		// report the mismatch clearly instead of failing silently here.
		return str, nil
	}
	return b, nil
}

// interpolateList interpolates each string element of a []any operand for the "in"
// operator. A non-list operand passes through unchanged.
func interpolateList(v any, store *vars.Store) (any, error) {
	list, ok := v.([]any)
	if !ok {
		return v, nil
	}
	out := make([]any, len(list))
	for i, item := range list {
		resolved, err := interpolateAny(item, store)
		if err != nil {
			return nil, err
		}
		out[i] = resolved
	}
	return out, nil
}

// check evaluates a single Assertion against actual and returns a Result.
func check(label, source string, actual any, assertion schema.Assertion) Result {
	if !assertion.IsOperator {
		// Scalar shorthand → equality.
		passed := equal(actual, assertion.Value)
		msg := ""
		if !passed {
			msg = fmt.Sprintf("expected %v, got %v", assertion.Value, actual)
		}
		return Result{
			Check:    label,
			Source:   source,
			Operator: "equals",
			Expected: assertion.Value,
			Actual:   actual,
			Passed:   passed,
			Message:  msg,
		}
	}
	return applyOperator(label, source, actual, assertion.Operator, assertion.Operand)
}

// applyOperator dispatches to the appropriate operator implementation.
func applyOperator(label, source string, actual any, op string, operand any) Result {
	r := Result{Check: label, Source: source, Operator: op, Expected: operand, Actual: actual}

	switch op {
	case "equals":
		r.Passed = equal(actual, operand)

	case "not_equals":
		r.Passed = !equal(actual, operand)

	case "contains":
		r.Passed = contains(actual, operand)

	case "matches":
		pattern, ok := operand.(string)
		if !ok {
			r.Passed = false
			r.Message = fmt.Sprintf("matches: pattern must be a string, got %T", operand)
			return r
		}
		rx, err := regexp.Compile(pattern)
		if err != nil {
			r.Passed = false
			r.Message = fmt.Sprintf("matches: invalid regexp %q: %v", pattern, err)
			return r
		}
		r.Passed = rx.MatchString(stringify(actual))

	case "exists":
		want, _ := operand.(bool)
		// actual is nil when the path was not found (caller passes nil for exists checks).
		exists := actual != nil
		r.Passed = exists == want
		r.Expected = want
		r.Actual = exists

	case "in":
		list, ok := toList(operand)
		if !ok {
			r.Passed = false
			r.Message = fmt.Sprintf("in: operand must be a list, got %T", operand)
			return r
		}
		r.Passed = inList(actual, list)

	case "gte", "lte", "gt", "lt":
		r.Passed, r.Message = numericCompare(op, actual, operand)

	case "length_gte", "length_lte":
		r.Passed, r.Message = lengthCompare(op, actual, operand)

	default:
		r.Passed = false
		r.Message = fmt.Sprintf("unknown operator %q", op)
	}

	if !r.Passed && r.Message == "" {
		r.Message = fmt.Sprintf("%s: expected %v, got %v", op, operand, actual)
	}
	return r
}

// --- Operator helpers ---

func equal(a, b any) bool {
	// Try numeric normalisation first so "200" == 200 and int/float match.
	af, aok := toFloat64(a)
	bf, bok := toFloat64(b)
	if aok && bok {
		return af == bf
	}
	return fmt.Sprintf("%v", a) == fmt.Sprintf("%v", b)
}

func contains(actual, operand any) bool {
	switch t := actual.(type) {
	case string:
		return strings.Contains(t, stringify(operand))
	case []any:
		for _, item := range t {
			if equal(item, operand) {
				return true
			}
		}
		return false
	default:
		return strings.Contains(stringify(actual), stringify(operand))
	}
}

func inList(actual any, list []any) bool {
	for _, item := range list {
		if equal(actual, item) {
			return true
		}
	}
	return false
}

func numericCompare(op string, actual, operand any) (ok bool, msg string) {
	af, aok := toFloat64(actual)
	bf, bok := toFloat64(operand)
	if !aok || !bok {
		return false, fmt.Sprintf("%s: both sides must be numeric (got %T and %T)", op, actual, operand)
	}
	switch op {
	case "gte":
		return af >= bf, ""
	case "lte":
		return af <= bf, ""
	case "gt":
		return af > bf, ""
	case "lt":
		return af < bf, ""
	}
	return false, "unknown numeric op"
}

func lengthCompare(op string, actual, operand any) (ok bool, msg string) {
	n, ok := toFloat64(operand)
	if !ok {
		return false, fmt.Sprintf("%s: operand must be numeric, got %T", op, operand)
	}
	threshold := int(math.Round(n))

	var length int
	switch t := actual.(type) {
	case string:
		length = len(t)
	case []any:
		length = len(t)
	default:
		return false, fmt.Sprintf("%s: actual must be a string or array, got %T", op, actual)
	}

	switch op {
	case "length_gte":
		return length >= threshold, ""
	case "length_lte":
		return length <= threshold, ""
	}
	return false, "unknown length op"
}

// --- Value resolution helpers ---

// bodyValue evaluates a JSONPath against the response body.
// For "exists" checks the caller inspects the error; for other checks the value
// is returned as the raw parsed type.
func bodyValue(path string, body []byte) (any, error) {
	if len(body) == 0 {
		return nil, fmt.Errorf("response body is empty")
	}
	exprStr := "$" + strings.TrimPrefix(path, "$.body")
	parsed, err := oj.Parse(body)
	if err != nil {
		return nil, fmt.Errorf("body is not valid JSON: %w", err)
	}
	expr, err := jp.ParseString(exprStr)
	if err != nil {
		return nil, fmt.Errorf("invalid JSONPath %q: %w", path, err)
	}
	matches := expr.Get(parsed)
	if len(matches) == 0 {
		return nil, fmt.Errorf("JSONPath %q matched nothing", path)
	}
	return matches[0], nil
}

// headerValue does a case-insensitive header lookup.
func headerValue(name string, headers http.Header) (string, error) {
	canonical := http.CanonicalHeaderKey(name)
	if vals, ok := headers[canonical]; ok && len(vals) > 0 {
		return vals[0], nil
	}
	for k, v := range headers {
		if strings.EqualFold(k, name) && len(v) > 0 {
			return v[0], nil
		}
	}
	return "", fmt.Errorf("header %q not found", name)
}

// --- Type conversion helpers ---

func toFloat64(v any) (float64, bool) {
	switch t := v.(type) {
	case int:
		return float64(t), true
	case int32:
		return float64(t), true
	case int64:
		return float64(t), true
	case float32:
		return float64(t), true
	case float64:
		return t, true
	case string:
		f, err := strconv.ParseFloat(t, 64)
		if err == nil {
			return f, true
		}
	}
	return 0, false
}

func toList(v any) ([]any, bool) {
	if t, ok := v.([]any); ok {
		return t, true
	}
	return nil, false
}

func stringify(v any) string {
	if v == nil {
		return ""
	}
	switch t := v.(type) {
	case string:
		return t
	case bool:
		if t {
			return "true"
		}
		return "false"
	case int64:
		return strconv.FormatInt(t, 10)
	case float64:
		if t == float64(int64(t)) {
			return strconv.FormatInt(int64(t), 10)
		}
		return strconv.FormatFloat(t, 'f', -1, 64)
	default:
		return fmt.Sprintf("%v", v)
	}
}
