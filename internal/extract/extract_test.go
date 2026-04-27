package extract

import (
	"net/http"
	"strings"
	"testing"

	"github.com/nilcolor/apix/internal/runner"
)

func resp(status int, body string, headers map[string]string) *runner.Response {
	h := http.Header{}
	for k, v := range headers {
		h.Set(k, v)
	}
	return &runner.Response{
		Status:  status,
		Headers: h,
		Body:    []byte(body),
	}
}

func TestStatus(t *testing.T) {
	r := resp(201, `{}`, nil)
	out, err := Extract(map[string]string{"code": "status"}, r)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if out["code"] != "201" {
		t.Errorf("status: %q", out["code"])
	}
}

func TestJSONPathScalar(t *testing.T) {
	r := resp(200, `{"data":{"email":"alice@example.com"}}`, nil)
	out, err := Extract(map[string]string{"email": "$.body.data.email"}, r)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if out["email"] != "alice@example.com" {
		t.Errorf("email: %q", out["email"])
	}
}

func TestJSONPathNested(t *testing.T) {
	r := resp(200, `{"data":{"user":{"id":42}}}`, nil)
	out, err := Extract(map[string]string{"uid": "$.body.data.user.id"}, r)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if out["uid"] != "42" {
		t.Errorf("uid: %q", out["uid"])
	}
}

func TestJSONPathArrayIndex(t *testing.T) {
	r := resp(200, `{"items":[{"id":"first"},{"id":"second"}]}`, nil)
	out, err := Extract(map[string]string{"first": "$.body.items[0].id"}, r)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if out["first"] != "first" {
		t.Errorf("first: %q", out["first"])
	}
}

func TestMultipleExtracts(t *testing.T) {
	r := resp(200, `{"token":"tok123","user_id":7}`, map[string]string{
		"X-Session-Id": "sess-abc",
	})
	extracts := map[string]string{
		"token":   "$.body.token",
		"user_id": "$.body.user_id",
		"session": "header.X-Session-Id",
		"code":    "status",
	}
	out, err := Extract(extracts, r)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if out["token"] != "tok123" {
		t.Errorf("token: %q", out["token"])
	}
	if out["user_id"] != "7" {
		t.Errorf("user_id: %q", out["user_id"])
	}
	if out["session"] != "sess-abc" {
		t.Errorf("session: %q", out["session"])
	}
	if out["code"] != "200" {
		t.Errorf("code: %q", out["code"])
	}
}

func TestHeaderFound(t *testing.T) {
	r := resp(200, `{}`, map[string]string{"X-Rate-Limit": "100"})
	out, err := Extract(map[string]string{"limit": "header.X-Rate-Limit"}, r)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if out["limit"] != "100" {
		t.Errorf("limit: %q", out["limit"])
	}
}

func TestHeaderCaseInsensitive(t *testing.T) {
	r := resp(200, `{}`, map[string]string{"Content-Type": "application/json"})
	// Lookup with different casing.
	out, err := Extract(map[string]string{"ct": "header.content-type"}, r)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if out["ct"] != "application/json" {
		t.Errorf("ct: %q", out["ct"])
	}
}

func TestHeaderMissing(t *testing.T) {
	r := resp(200, `{}`, nil)
	_, err := Extract(map[string]string{"h": "header.X-Missing"}, r)
	if err == nil {
		t.Fatal("expected error for missing header")
	}
	if !strings.Contains(err.Error(), "X-Missing") {
		t.Errorf("error should name the header: %v", err)
	}
}

func TestNonJSONBody(t *testing.T) {
	r := resp(200, `not json at all`, nil)
	_, err := Extract(map[string]string{"x": "$.body.foo"}, r)
	if err == nil {
		t.Fatal("expected error for non-JSON body")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "json") {
		t.Errorf("error should mention JSON: %v", err)
	}
}

func TestMissingJSONPath(t *testing.T) {
	r := resp(200, `{"a":1}`, nil)
	_, err := Extract(map[string]string{"x": "$.body.b.c.d"}, r)
	if err == nil {
		t.Fatal("expected error for missing path")
	}
	if !strings.Contains(err.Error(), "matched nothing") {
		t.Errorf("error should say matched nothing: %v", err)
	}
}

func TestJSONBodyWithContentTypeHeader(t *testing.T) {
	// application/json Content-Type round-trip.
	r := resp(200, `{"access_token":"tok-abc"}`, map[string]string{
		"Content-Type": "application/json",
	})
	out, err := Extract(map[string]string{"tok": "$.body.access_token"}, r)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if out["tok"] != "tok-abc" {
		t.Errorf("tok: %q", out["tok"])
	}
}

func TestJSONBodyWithoutContentType(t *testing.T) {
	// Body parses as JSON regardless of absent Content-Type.
	r := resp(200, `{"id":99}`, nil)
	out, err := Extract(map[string]string{"id": "$.body.id"}, r)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if out["id"] != "99" {
		t.Errorf("id: %q", out["id"])
	}
}

func TestUnknownSource(t *testing.T) {
	r := resp(200, `{}`, nil)
	_, err := Extract(map[string]string{"x": "body.foo"}, r)
	if err == nil {
		t.Fatal("expected error for unknown source prefix")
	}
}

func TestBooleanValue(t *testing.T) {
	r := resp(200, `{"active":true}`, nil)
	out, err := Extract(map[string]string{"active": "$.body.active"}, r)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if out["active"] != "true" {
		t.Errorf("active: %q", out["active"])
	}
}

func TestPrintSourceWholeBody(t *testing.T) {
	r := resp(200, `{"token":"abc","user":{"id":1}}`, nil)
	got, err := PrintSource("$.body", r)
	if err != nil {
		t.Fatalf("PrintSource: %v", err)
	}
	// Should be indented JSON.
	if !strings.Contains(got, "\n") {
		t.Errorf("expected indented JSON, got: %q", got)
	}
	if !strings.Contains(got, `"token"`) {
		t.Errorf("expected token key in output: %q", got)
	}
}

func TestPrintSourceSubtree(t *testing.T) {
	r := resp(200, `{"user":{"id":42,"name":"alice"}}`, nil)
	got, err := PrintSource("$.body.user", r)
	if err != nil {
		t.Fatalf("PrintSource: %v", err)
	}
	if !strings.Contains(got, `"name"`) || !strings.Contains(got, "alice") {
		t.Errorf("expected user object in output: %q", got)
	}
}

func TestPrintSourceScalar(t *testing.T) {
	r := resp(200, `{"token":"tok-xyz"}`, nil)
	got, err := PrintSource("$.body.token", r)
	if err != nil {
		t.Fatalf("PrintSource: %v", err)
	}
	if got != "tok-xyz" {
		t.Errorf("expected plain scalar, got: %q", got)
	}
}

func TestPrintSourceNonJSON(t *testing.T) {
	r := resp(200, "plain text response", nil)
	got, err := PrintSource("$.body", r)
	if err != nil {
		t.Fatalf("PrintSource non-JSON body: %v", err)
	}
	if got != "plain text response" {
		t.Errorf("expected raw body, got: %q", got)
	}
}

func TestPrintSourceStatus(t *testing.T) {
	r := resp(204, `{}`, nil)
	got, err := PrintSource("status", r)
	if err != nil {
		t.Fatalf("PrintSource status: %v", err)
	}
	if got != "204" {
		t.Errorf("expected 204, got: %q", got)
	}
}
