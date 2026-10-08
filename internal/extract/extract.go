package extract

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/ohler55/ojg/jp"
	"github.com/ohler55/ojg/oj"

	"github.com/nilcolor/apix/internal/runner"
)

// Extract evaluates each entry in extracts against resp and returns a map of
// variable name → extracted string value.
//
// Supported source prefixes:
//   - "$.body.<jsonpath>" — JSONPath into parsed JSON body
//   - "header.<Name>"    — response header (case-insensitive)
//   - "status"           — HTTP status code as a string
func Extract(extracts map[string]string, resp *runner.Response) (map[string]string, error) {
	result := make(map[string]string, len(extracts))
	for varName, source := range extracts {
		val, err := extractOne(source, resp)
		if err != nil {
			return nil, fmt.Errorf("extract %q (%s): %w", varName, source, err)
		}
		result[varName] = val
	}
	return result, nil
}

// PrintSource extracts the value at source and formats it for human display:
// JSON objects and arrays are pretty-printed; scalars are returned as plain strings.
// Accepts the same source syntax as Extract ($.body[.path], header.Name, status).
func PrintSource(source string, resp *runner.Response) (string, error) {
	switch {
	case source == "status":
		return strconv.Itoa(resp.Status), nil
	case strings.HasPrefix(source, "header."):
		name := strings.TrimPrefix(source, "header.")
		return extractHeader(name, resp.Headers)
	case strings.HasPrefix(source, "$.body"):
		return extractJSONPathPretty(source, resp.Body)
	default:
		return "", fmt.Errorf("unknown print source %q (must start with $.body, header., or be \"status\")", source)
	}
}

const keysSuffix = ".keys()"

// Query evaluates a "$.body" source against a parsed JSON body and returns its matches.
// A trailing ".keys()" replaces the first matched object with its sorted key names.
func Query(source string, parsed any) ([]any, error) {
	path, wantKeys := strings.CutSuffix(source, keysSuffix)
	expr, err := jp.ParseString("$" + strings.TrimPrefix(path, "$.body"))
	if err != nil {
		return nil, fmt.Errorf("invalid JSONPath %q: %w", source, err)
	}
	matches := expr.Get(parsed)
	if !wantKeys || len(matches) == 0 {
		return matches, nil
	}
	obj, ok := matches[0].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%q: keys() needs an object, got %T", source, matches[0])
	}
	keys := make([]any, 0, len(obj))
	for _, k := range slices.Sorted(maps.Keys(obj)) {
		keys = append(keys, k)
	}
	return []any{keys}, nil
}

func extractJSONPathPretty(source string, body []byte) (string, error) {
	if len(body) == 0 {
		return "", fmt.Errorf("response body is empty, cannot apply %q", source)
	}
	parsed, err := oj.Parse(body)
	if err != nil {
		// Non-JSON body: only allowed when selecting the whole body.
		if source == "$.body" {
			return string(body), nil
		}
		return "", fmt.Errorf("response body is not valid JSON: %w", err)
	}
	matches, err := Query(source, parsed)
	if err != nil {
		return "", err
	}
	if len(matches) == 0 {
		return "", fmt.Errorf("JSONPath %q matched nothing in response body", source)
	}
	return stringifyPretty(matches[0]), nil
}

// stringifyPretty formats JSON objects/arrays with indentation, scalars as plain strings.
// SetEscapeHTML(false) prevents <, >, & from being replaced with < etc.
func stringifyPretty(v any) string {
	switch v.(type) {
	case map[string]any, []any:
		var buf bytes.Buffer
		enc := json.NewEncoder(&buf)
		enc.SetEscapeHTML(false)
		enc.SetIndent("", "  ")
		if err := enc.Encode(v); err != nil {
			return fmt.Sprintf("%v", v)
		}
		return strings.TrimRight(buf.String(), "\n")
	default:
		return stringify(v)
	}
}

func extractOne(source string, resp *runner.Response) (string, error) {
	switch {
	case source == "status":
		return strconv.Itoa(resp.Status), nil

	case strings.HasPrefix(source, "header."):
		name := strings.TrimPrefix(source, "header.")
		return extractHeader(name, resp.Headers)

	case strings.HasPrefix(source, "$.body"):
		return extractJSONPath(source, resp.Body)

	default:
		return "", fmt.Errorf("unknown extraction source %q (must start with $.body, header., or be \"status\")", source)
	}
}

// extractHeader does a case-insensitive header lookup.
func extractHeader(name string, headers http.Header) (string, error) {
	// http.Header is canonicalised, but the user may provide any casing.
	canonical := http.CanonicalHeaderKey(name)
	if vals, ok := headers[canonical]; ok && len(vals) > 0 {
		return vals[0], nil
	}
	// Fallback: linear scan for non-canonical names.
	for k, v := range headers {
		if strings.EqualFold(k, name) && len(v) > 0 {
			return v[0], nil
		}
	}
	return "", fmt.Errorf("header %q not found in response", name)
}

// extractJSONPath parses the body as JSON (regardless of Content-Type) and
// evaluates the JSONPath expression. The prefix "$.body" maps to the body root "$".
func extractJSONPath(source string, body []byte) (string, error) {
	if len(body) == 0 {
		return "", fmt.Errorf("response body is empty, cannot apply JSONPath %q", source)
	}

	parsed, err := oj.Parse(body)
	if err != nil {
		return "", fmt.Errorf("response body is not valid JSON: %w", err)
	}

	matches, err := Query(source, parsed)
	if err != nil {
		return "", err
	}
	if len(matches) == 0 {
		return "", fmt.Errorf("JSONPath %q matched nothing in response body", source)
	}

	return stringify(matches[0]), nil
}

// stringify converts a JSONPath match result to a string.
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
	case int:
		return strconv.Itoa(t)
	case int64:
		return strconv.FormatInt(t, 10)
	case float64:
		// Omit trailing ".0" for whole numbers.
		if t == float64(int64(t)) {
			return strconv.FormatInt(int64(t), 10)
		}
		return strconv.FormatFloat(t, 'f', -1, 64)
	default:
		return fmt.Sprintf("%v", v)
	}
}
