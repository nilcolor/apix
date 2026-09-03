package hooks

import (
	"strings"
	"testing"

	"github.com/nilcolor/apix/internal/schema"
	"github.com/nilcolor/apix/internal/vars"
)

func hook(pairs ...string) *schema.Hook {
	h := &schema.Hook{}
	for i := 0; i+1 < len(pairs); i += 2 {
		h.Vars = append(h.Vars, schema.HookVar{Name: pairs[i], Expr: pairs[i+1]})
	}
	return h
}

func envFor(s *vars.Store) Env {
	return Env{
		Request: Request{Method: "POST", URL: "https://api.example.com/orders", Path: "/orders", Body: `{"amount":100}`},
		Store:   s,
	}
}

func TestRunLaterExpressionSeesEarlierResult(t *testing.T) {
	s := vars.NewStore()
	s.Set("api_secret", "topsecret")
	s.FreezeBuiltins()

	h := hook(
		"canonical", `request.method + request.path + request.body`,
		"sig", `hex(hmac_sha256(canonical, api_secret))`,
	)

	got, err := Run(h, envFor(s), s)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(got["sig"]) != 64 {
		t.Errorf("sig should be a 64-char hex digest, got %q", got["sig"])
	}
	if v, _ := s.Get("sig"); v != got["sig"] {
		t.Errorf("sig not committed to the store: %q", v)
	}
	if got["canonical"] != `POST/orders{"amount":100}` {
		t.Errorf("canonical: %q", got["canonical"])
	}
}

func TestRunCommitsNothingWhenALaterExpressionFails(t *testing.T) {
	s := vars.NewStore()
	s.FreezeBuiltins()

	h := hook(
		"ts", `builtin.timestamp`,
		"sig", `no_such_function(ts)`,
	)

	if _, err := Run(h, envFor(s), s); err == nil {
		t.Fatal("expected an error for an unknown function")
	}
	if _, ok := s.Get("ts"); ok {
		t.Error("ts must not be committed when a later expression fails")
	}
}

func TestRunErrorNamesTheVariable(t *testing.T) {
	s := vars.NewStore()
	h := hook("sig", `request.method + 1`)

	_, err := Run(h, envFor(s), s)
	if err == nil {
		t.Fatal("expected a type error")
	}
	if !strings.Contains(err.Error(), "sig") {
		t.Errorf("error should name the failing variable: %v", err)
	}
}

func TestRunRejectsRawDigestBytes(t *testing.T) {
	s := vars.NewStore()
	s.Set("api_secret", "topsecret")

	h := hook("sig", `hmac_sha256(request.body, api_secret)`)

	_, err := Run(h, envFor(s), s)
	if err == nil {
		t.Fatal("expected raw digest bytes to be rejected as a hook result")
	}
	if !strings.Contains(err.Error(), "hex()") {
		t.Errorf("error should suggest an encoder: %v", err)
	}
}

func TestRunStringifiesScalars(t *testing.T) {
	s := vars.NewStore()
	got, err := Run(hook("n", `len(request.path)`), envFor(s), s)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got["n"] != "7" {
		t.Errorf("expected len(\"/orders\") == 7, got %q", got["n"])
	}
}

func TestRunNilHook(t *testing.T) {
	s := vars.NewStore()
	got, err := Run(nil, envFor(s), s)
	if err != nil || got != nil {
		t.Errorf("nil hook should be a no-op, got %v / %v", got, err)
	}
}

func TestRunFrozenBuiltinMatchesStore(t *testing.T) {
	s := vars.NewStore()
	s.FreezeBuiltins()

	got, err := Run(hook("ts", `builtin.timestamp`), envFor(s), s)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	rendered, err := vars.Interpolate("{{ $timestamp }}", s)
	if err != nil {
		t.Fatalf("Interpolate: %v", err)
	}
	if got["ts"] != rendered {
		t.Errorf("hook saw %q but {{ $timestamp }} rendered %q", got["ts"], rendered)
	}
}

func TestNumericResultsAvoidScientificNotation(t *testing.T) {
	s := vars.NewStore()
	s.FreezeBuiltins()

	got, err := Run(hook("exp", `int(builtin.timestamp_ms) / 1000`), envFor(s), s)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if strings.ContainsAny(got["exp"], "eE") {
		t.Errorf("float result must not use an exponent on the wire: %q", got["exp"])
	}
	if !strings.HasPrefix(got["exp"], "17") {
		t.Errorf("expected a plain decimal seconds value, got %q", got["exp"])
	}

	intResult, err := Run(hook("exp", `int(int(builtin.timestamp_ms) / 1000)`), envFor(s), s)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if strings.ContainsAny(intResult["exp"], ".eE") {
		t.Errorf("an int() result must be a plain integer: %q", intResult["exp"])
	}
}

func TestNumericResultFormats(t *testing.T) {
	s := vars.NewStore()
	cases := map[string]string{
		`1 + 1`:             "2",
		`10 / 4`:            "2.5",
		`1000000 * 1000000`: "1000000000000",
		`true`:              "true",
	}
	for src, want := range cases {
		got, err := Run(hook("v", src), envFor(s), s)
		if err != nil {
			t.Fatalf("Run %q: %v", src, err)
		}
		if got["v"] != want {
			t.Errorf("%q: got %q, want %q", src, got["v"], want)
		}
	}
}

func TestStoreVariableCollidingWithReservedNameErrors(t *testing.T) {
	s := vars.NewStore()
	s.Set("hex", "not-the-function")

	_, err := Run(hook("v", `request.method`), envFor(s), s)
	if err == nil {
		t.Fatal("expected an error for a store variable named after a function")
	}
	if !strings.Contains(err.Error(), "hex") {
		t.Errorf("error should name the collision: %v", err)
	}
}
