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
)

// Result holds the outcome of a single assertion check.
type Result struct {
	Check    string
	Expected any
	Actual   any
	Passed   bool
	Message  string
}

// Evaluate runs all assertions in asserts against resp and returns one Result per check.
func Evaluate(asserts *schema.Assert, resp *runner.Response) []Result {
	if asserts == nil {
		return nil
	}
	var results []Result

	// Status assertion.
	if asserts.Status != nil {
		actual := resp.Status
		results = append(results, check("status", any(actual), *asserts.Status))
	}

	// Body assertions — each key is a JSONPath expression.
	for path, assertion := range asserts.Body {
		actual, err := bodyValue(path, resp.Body)
		if err != nil {
			// For "exists" operator, a missing path means exists=false.
			if assertion.IsOperator && assertion.Operator == "exists" {
				results = append(results, applyOperator("body "+path, nil, "exists", assertion.Operand))
			} else {
				results = append(results, Result{
					Check:   "body " + path,
					Passed:  false,
					Message: err.Error(),
				})
			}
			continue
		}
		results = append(results, check("body "+path, actual, assertion))
	}

	// Header assertions.
	for name, assertion := range asserts.Headers {
		actual, err := headerValue(name, resp.Headers)
		if err != nil {
			results = append(results, Result{
				Check:   "header " + name,
				Passed:  false,
				Message: err.Error(),
			})
			continue
		}
		results = append(results, check("header "+name, actual, assertion))
	}

	return results
}

// check evaluates a single Assertion against actual and returns a Result.
func check(label string, actual any, assertion schema.Assertion) Result {
	if !assertion.IsOperator {
		// Scalar shorthand → equality.
		passed := equal(actual, assertion.Value)
		msg := ""
		if !passed {
			msg = fmt.Sprintf("expected %v, got %v", assertion.Value, actual)
		}
		return Result{
			Check:    label,
			Expected: assertion.Value,
			Actual:   actual,
			Passed:   passed,
			Message:  msg,
		}
	}
	return applyOperator(label, actual, assertion.Operator, assertion.Operand)
}

// applyOperator dispatches to the appropriate operator implementation.
func applyOperator(label string, actual any, op string, operand any) Result {
	r := Result{Check: label, Expected: operand, Actual: actual}

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
