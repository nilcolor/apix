package assert

import (
	"net/http"
	"strings"
	"testing"

	"github.com/nilcolor/apix/internal/runner"
	"github.com/nilcolor/apix/internal/schema"
)

// helpers

func assertion(isOp bool, val any, op string, operand any) schema.Assertion {
	return schema.Assertion{IsOperator: isOp, Value: val, Operator: op, Operand: operand}
}

func scalar(v any) schema.Assertion { return assertion(false, v, "", nil) }
func op(name string, operand any) schema.Assertion {
	return assertion(true, nil, name, operand)
}

func makeResp(status int, body string, headers map[string]string) *runner.Response {
	h := http.Header{}
	for k, v := range headers {
		h.Set(k, v)
	}
	return &runner.Response{Status: status, Headers: h, Body: []byte(body)}
}

func mustPass(t *testing.T, results []Result) {
	t.Helper()
	for _, r := range results {
		if !r.Passed {
			t.Errorf("check %q failed: %s (expected %v, actual %v)", r.Check, r.Message, r.Expected, r.Actual)
		}
	}
}

func mustFail(t *testing.T, results []Result) {
	t.Helper()
	for _, r := range results {
		if r.Passed {
			t.Errorf("check %q should have failed", r.Check)
		}
	}
}

// --- Status assertions ---

func TestStatusScalarPass(t *testing.T) {
	r := makeResp(200, `{}`, nil)
	results := Evaluate(&schema.Assert{Status: ptr(scalar(200))}, r)
	mustPass(t, results)
}

func TestStatusScalarFail(t *testing.T) {
	r := makeResp(404, `{}`, nil)
	results := Evaluate(&schema.Assert{Status: ptr(scalar(200))}, r)
	mustFail(t, results)
	if !strings.Contains(results[0].Message, "404") {
		t.Errorf("message should mention actual: %q", results[0].Message)
	}
}

func TestStatusInPass(t *testing.T) {
	r := makeResp(201, `{}`, nil)
	results := Evaluate(&schema.Assert{Status: ptr(op("in", []any{200, 201}))}, r)
	mustPass(t, results)
}

func TestStatusInFail(t *testing.T) {
	r := makeResp(500, `{}`, nil)
	results := Evaluate(&schema.Assert{Status: ptr(op("in", []any{200, 201}))}, r)
	mustFail(t, results)
}

// --- Body operator tests (table-driven) ---

type opCase struct {
	name    string
	body    string
	path    string
	op      string
	operand any
	pass    bool
}

var operatorCases = []opCase{
	// equals
	{"equals pass", `{"x":"hello"}`, "$.body.x", "equals", "hello", true},
	{"equals fail", `{"x":"world"}`, "$.body.x", "equals", "hello", false},

	// not_equals
	{"not_equals pass", `{"x":"world"}`, "$.body.x", "not_equals", "hello", true},
	{"not_equals fail", `{"x":"hello"}`, "$.body.x", "not_equals", "hello", false},

	// contains (string)
	{"contains string pass", `{"x":"foobar"}`, "$.body.x", "contains", "oba", true},
	{"contains string fail", `{"x":"foobar"}`, "$.body.x", "contains", "xyz", false},

	// contains (array)
	{"contains array pass", `{"x":["a","b","c"]}`, "$.body.x", "contains", "b", true},
	{"contains array fail", `{"x":["a","b","c"]}`, "$.body.x", "contains", "z", false},

	// matches
	{"matches pass", `{"x":"John Doe"}`, "$.body.x", "matches", "^John", true},
	{"matches fail", `{"x":"Jane Doe"}`, "$.body.x", "matches", "^John", false},

	// exists
	{"exists true pass", `{"x":1}`, "$.body.x", "exists", true, true},
	{"exists true fail", `{"y":1}`, "$.body.x", "exists", true, false},
	{"exists false pass", `{"y":1}`, "$.body.x", "exists", false, true},
	{"exists false fail", `{"x":1}`, "$.body.x", "exists", false, false},

	// in
	{"in pass", `{"role":"admin"}`, "$.body.role", "in", []any{"admin", "editor"}, true},
	{"in fail", `{"role":"viewer"}`, "$.body.role", "in", []any{"admin", "editor"}, false},

	// gte
	{"gte pass equal", `{"n":5}`, "$.body.n", "gte", 5, true},
	{"gte pass greater", `{"n":6}`, "$.body.n", "gte", 5, true},
	{"gte fail", `{"n":4}`, "$.body.n", "gte", 5, false},

	// lte
	{"lte pass", `{"n":4}`, "$.body.n", "lte", 5, true},
	{"lte fail", `{"n":6}`, "$.body.n", "lte", 5, false},

	// gt
	{"gt pass", `{"n":6}`, "$.body.n", "gt", 5, true},
	{"gt fail equal", `{"n":5}`, "$.body.n", "gt", 5, false},

	// lt
	{"lt pass", `{"n":4}`, "$.body.n", "lt", 5, true},
	{"lt fail equal", `{"n":5}`, "$.body.n", "lt", 5, false},

	// length_gte
	{"length_gte string pass", `{"x":"hello"}`, "$.body.x", "length_gte", 5, true},
	{"length_gte string fail", `{"x":"hi"}`, "$.body.x", "length_gte", 5, false},
	{"length_gte array pass", `{"x":[1,2,3]}`, "$.body.x", "length_gte", 3, true},
	{"length_gte array fail", `{"x":[1]}`, "$.body.x", "length_gte", 3, false},

	// length_lte
	{"length_lte string pass", `{"x":"hi"}`, "$.body.x", "length_lte", 5, true},
	{"length_lte string fail", `{"x":"toolong!!"}`, "$.body.x", "length_lte", 5, false},
}

func TestOperators(t *testing.T) {
	for _, tc := range operatorCases {
		t.Run(tc.name, func(t *testing.T) {
			r := makeResp(200, tc.body, nil)
			asserts := &schema.Assert{
				Body: map[string]schema.Assertion{
					tc.path: op(tc.op, tc.operand),
				},
			}
			results := Evaluate(asserts, r)
			if len(results) != 1 {
				t.Fatalf("expected 1 result, got %d", len(results))
			}
			if results[0].Passed != tc.pass {
				t.Errorf("passed=%v, want %v; message: %q", results[0].Passed, tc.pass, results[0].Message)
			}
		})
	}
}

// --- Type mismatch cases ---

func TestNumericMismatch(t *testing.T) {
	r := makeResp(200, `{"x":"not-a-number"}`, nil)
	asserts := &schema.Assert{Body: map[string]schema.Assertion{"$.body.x": op("gte", 5)}}
	results := Evaluate(asserts, r)
	if results[0].Passed {
		t.Error("gte against non-numeric string should fail")
	}
}

func TestMatchesInvalidRegexp(t *testing.T) {
	r := makeResp(200, `{"x":"hello"}`, nil)
	asserts := &schema.Assert{Body: map[string]schema.Assertion{"$.body.x": op("matches", "[invalid")}}
	results := Evaluate(asserts, r)
	if results[0].Passed {
		t.Error("invalid regexp should produce a failing result")
	}
	if !strings.Contains(results[0].Message, "regexp") {
		t.Errorf("message should mention regexp: %q", results[0].Message)
	}
}

// --- Mixed status+body+headers ---

func TestMixedAssertions(t *testing.T) {
	r := makeResp(200,
		`{"data":{"email":"alice@example.com","items":[1,2,3]}}`,
		map[string]string{"Content-Type": "application/json"},
	)
	asserts := &schema.Assert{
		Status: ptr(scalar(200)),
		Body: map[string]schema.Assertion{
			"$.body.data.email":         scalar("alice@example.com"),
			"$.body.data.items":         op("length_gte", 1),
		},
		Headers: map[string]schema.Assertion{
			"Content-Type": op("contains", "application/json"),
		},
	}
	results := Evaluate(asserts, r)
	mustPass(t, results)
	if len(results) != 4 {
		t.Errorf("expected 4 results, got %d", len(results))
	}
}

// --- Failure messages include expected and actual ---

func TestFailureMessageContainsExpectedAndActual(t *testing.T) {
	r := makeResp(404, `{}`, nil)
	results := Evaluate(&schema.Assert{Status: ptr(scalar(200))}, r)
	msg := results[0].Message
	if !strings.Contains(msg, "200") {
		t.Errorf("message should include expected (200): %q", msg)
	}
	if !strings.Contains(msg, "404") {
		t.Errorf("message should include actual (404): %q", msg)
	}
}

// --- Header assertions ---

func TestHeaderScalarPass(t *testing.T) {
	r := makeResp(200, `{}`, map[string]string{"Content-Type": "application/json"})
	asserts := &schema.Assert{
		Headers: map[string]schema.Assertion{"Content-Type": scalar("application/json")},
	}
	mustPass(t, Evaluate(asserts, r))
}

func TestHeaderContains(t *testing.T) {
	r := makeResp(200, `{}`, map[string]string{"Content-Type": "application/json; charset=utf-8"})
	asserts := &schema.Assert{
		Headers: map[string]schema.Assertion{"Content-Type": op("contains", "application/json")},
	}
	mustPass(t, Evaluate(asserts, r))
}

func TestHeaderMissingFails(t *testing.T) {
	r := makeResp(200, `{}`, nil)
	asserts := &schema.Assert{
		Headers: map[string]schema.Assertion{"X-Missing": scalar("value")},
	}
	results := Evaluate(asserts, r)
	mustFail(t, results)
}

// --- NilAssert ---

func TestNilAssertReturnsEmpty(t *testing.T) {
	r := makeResp(200, `{}`, nil)
	results := Evaluate(nil, r)
	if len(results) != 0 {
		t.Errorf("nil assert should return empty results, got %d", len(results))
	}
}

// --- helpers ---

func ptr(a schema.Assertion) *schema.Assertion { return &a }
