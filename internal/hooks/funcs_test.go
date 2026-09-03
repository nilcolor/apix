package hooks

import (
	"testing"

	"github.com/expr-lang/expr"

	"github.com/nilcolor/apix/internal/vars"
)

func eval(t *testing.T, src string, extra map[string]any) any {
	t.Helper()
	env := map[string]any{}
	for k, v := range funcs() {
		env[k] = v
	}
	for k, v := range extra {
		env[k] = v
	}
	out, err := expr.Eval(src, env)
	if err != nil {
		t.Fatalf("eval %q: %v", src, err)
	}
	return out
}

// RFC 4231 test case 1.
func TestHMACSHA256Vector(t *testing.T) {
	got := eval(t, `hex(hmac_sha256("Hi There", key))`, map[string]any{
		"key": string([]byte{0x0b, 0x0b, 0x0b, 0x0b, 0x0b, 0x0b, 0x0b, 0x0b, 0x0b, 0x0b, 0x0b, 0x0b, 0x0b, 0x0b, 0x0b, 0x0b, 0x0b, 0x0b, 0x0b, 0x0b}),
	})
	want := "b0344c61d8db38535ca8afceaf0bf12b881dc200c9833da726e9376c2e32cff7"
	if got != want {
		t.Errorf("hmac_sha256:\n got %v\nwant %v", got, want)
	}
}

// FIPS 180-2 / NIST single-block vector.
func TestSHA256Vector(t *testing.T) {
	got := eval(t, `hex(sha256("abc"))`, nil)
	want := "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad"
	if got != want {
		t.Errorf("sha256:\n got %v\nwant %v", got, want)
	}
}

// RFC 4648 section 10.
func TestBase64Vectors(t *testing.T) {
	cases := map[string]string{
		"":       "",
		"f":      "Zg==",
		"fo":     "Zm8=",
		"foo":    "Zm9v",
		"foob":   "Zm9vYg==",
		"fooba":  "Zm9vYmE=",
		"foobar": "Zm9vYmFy",
	}
	for in, want := range cases {
		got := eval(t, `base64(input)`, map[string]any{"input": in})
		if got != want {
			t.Errorf("base64(%q): got %v, want %v", in, got, want)
		}
	}
}

func TestHMACKeyAcceptsBytes(t *testing.T) {
	got := eval(t, `hex(hmac_sha256("msg", sha256("seed")))`, nil)
	if len(got.(string)) != 64 {
		t.Errorf("expected a 64-char hex digest, got %q", got)
	}
}

func TestJSONCompact(t *testing.T) {
	got := eval(t, `json(payload)`, map[string]any{
		"payload": map[string]any{"b": 2, "a": 1},
	})
	if got != `{"a":1,"b":2}` {
		t.Errorf("json: got %v", got)
	}
}

func TestFuncTypeError(t *testing.T) {
	env := map[string]any{}
	for k, v := range funcs() {
		env[k] = v
	}
	env["n"] = 42
	if _, err := expr.Eval(`hex(n)`, env); err == nil {
		t.Fatal("expected a type error passing an int to hex()")
	}
}

func TestStoreVarsAndBuiltinsInEnv(t *testing.T) {
	s := vars.NewStore()
	s.Set("api_secret", "shh")
	s.FreezeBuiltins()

	env, err := Env{Request: Request{Method: "POST", Path: "/orders"}, Store: s}.build()
	if err != nil {
		t.Fatalf("build: %v", err)
	}

	if env["api_secret"] != "shh" {
		t.Errorf("store var not exposed: %v", env["api_secret"])
	}
	b := env["builtin"].(map[string]any)
	if b["timestamp"] == nil || b["timestamp_ms"] == nil || b["iso_date"] == nil {
		t.Errorf("frozen builtins not exposed without $: %v", b)
	}
	if _, ok := b["uuid"]; ok {
		t.Error("$uuid must not be exposed to hooks; it is not frozen")
	}
	r := env["request"].(map[string]any)
	if _, ok := r["headers"]; ok {
		t.Error("request.headers must not be exposed; headers render after the hook")
	}
}
