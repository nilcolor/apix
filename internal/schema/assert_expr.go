package schema

import (
	"fmt"
	"strings"
)

// exprOperators maps expression-syntax operator tokens to internal operator names.
// Symbols are shorthand aliases for the six comparison operators that have natural
// notation; every other operator reuses its internal name verbatim as a bare keyword,
// so there is exactly one operator vocabulary rather than two to keep in sync.
var exprOperators = map[string]string{
	"==":         "equals",
	"equals":     "equals",
	"!=":         "not_equals",
	"not_equals": "not_equals",
	">=":         "gte",
	"gte":        "gte",
	"<=":         "lte",
	"lte":        "lte",
	">":          "gt",
	"gt":         "gt",
	"<":          "lt",
	"lt":         "lt",
	"contains":   "contains",
	"matches":    "matches",
	"exists":     "exists",
	"in":         "in",
	"length_gte": "length_gte",
	"length_lte": "length_lte",
}

// parseAssertExpr parses a single expression-form assertion string, e.g.:
//
//	"status == 200"
//	"$.body.age gte {{ min_age }}"
//	"$.body.roles in [admin, owner]"
//	"header.X-Request-Id == abc123"
//
// into the (target, key, Assertion) triple needed to populate an Assert struct.
// target is one of "status", "body", "header"; key is empty for "status".
//
// The operator must appear as its own whitespace-separated token. Operand values
// containing spaces must be wrapped in matching single or double quotes.
func parseAssertExpr(expr string) (target, key string, assertion Assertion, err error) {
	words := strings.Fields(expr)

	opIdx := -1
	var opToken string
	for i, w := range words {
		if _, ok := exprOperators[w]; ok {
			opIdx = i
			opToken = w
			break
		}
	}
	switch {
	case opIdx == -1:
		return "", "", Assertion{}, fmt.Errorf("assert expression %q: no recognized operator found", expr)
	case opIdx == 0:
		return "", "", Assertion{}, fmt.Errorf("assert expression %q: missing source before operator %q", expr, opToken)
	case opIdx == len(words)-1:
		return "", "", Assertion{}, fmt.Errorf("assert expression %q: missing operand after operator %q", expr, opToken)
	}

	source := strings.Join(words[:opIdx], " ")
	operand := dequote(strings.Join(words[opIdx+1:], " "))
	operator := exprOperators[opToken]

	switch {
	case source == "status":
		target = "status"
	case strings.HasPrefix(source, "$.body"):
		target = "body"
		key = source
	case strings.HasPrefix(source, "header."):
		target = "header"
		key = strings.TrimPrefix(source, "header.")
	default:
		return "", "", Assertion{}, fmt.Errorf("assert expression %q: unknown source %q (must be status, $.body.<path>, or header.<name>)", expr, source)
	}

	assertion = Assertion{IsOperator: true, Operator: operator}
	if operator == "in" {
		list, err := parseListLiteral(operand)
		if err != nil {
			return "", "", Assertion{}, fmt.Errorf("assert expression %q: %w", expr, err)
		}
		assertion.Operand = list
	} else {
		assertion.Operand = operand
	}

	return target, key, assertion, nil
}

// dequote strips one matching pair of leading/trailing single or double quotes, if present.
func dequote(s string) string {
	if len(s) >= 2 {
		first, last := s[0], s[len(s)-1]
		if (first == '\'' && last == '\'') || (first == '"' && last == '"') {
			return s[1 : len(s)-1]
		}
	}
	return s
}

// parseListLiteral parses a bracketed, comma-separated list literal such as
// "[admin, owner]" into a []any of strings. Elements may be individually quoted.
func parseListLiteral(s string) ([]any, error) {
	if !strings.HasPrefix(s, "[") || !strings.HasSuffix(s, "]") {
		return nil, fmt.Errorf("in: operand must be a bracketed list, e.g. [a, b], got %q", s)
	}
	inner := strings.TrimSpace(s[1 : len(s)-1])
	if inner == "" {
		return []any{}, nil
	}
	parts := strings.Split(inner, ",")
	list := make([]any, len(parts))
	for i, p := range parts {
		list[i] = dequote(strings.TrimSpace(p))
	}
	return list, nil
}
