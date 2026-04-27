package vars

import (
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/nilcolor/apix/internal/schema"
)

// --- Store tests ---

func TestStoreSetGet(t *testing.T) {
	s := NewStore()
	s.Set("foo", "bar")
	v, ok := s.Get("foo")
	if !ok || v != "bar" {
		t.Errorf("Get: got %q %v", v, ok)
	}
}

func TestStoreHas(t *testing.T) {
	s := NewStore()
	if s.Has("x") {
		t.Error("Has should be false for missing key")
	}
	s.Set("x", "1")
	if !s.Has("x") {
		t.Error("Has should be true after Set")
	}
}

func TestStoreMerge(t *testing.T) {
	a := NewStore()
	a.Set("k1", "v1")
	a.Set("shared", "from_a")

	b := NewStore()
	b.Set("k2", "v2")
	b.Set("shared", "from_b")

	a.Merge(b)
	if v, _ := a.Get("k1"); v != "v1" {
		t.Errorf("k1: %q", v)
	}
	if v, _ := a.Get("k2"); v != "v2" {
		t.Errorf("k2: %q", v)
	}
	// b wins for shared
	if v, _ := a.Get("shared"); v != "from_b" {
		t.Errorf("shared: %q", v)
	}
}

// --- BuildStore priority tests ---

func makeFile(vars map[string]string) *schema.RequestFile {
	return &schema.RequestFile{Variables: vars}
}

func TestBuildStorePriorityOrder(t *testing.T) {
	// file has "key" = "from_file"; CLI overrides it
	f := makeFile(map[string]string{"key": "from_file", "file_only": "yes"})
	cli := map[string]string{"key": "from_cli", "cli_only": "yes"}

	s, err := BuildStore(f, cli)
	if err != nil {
		t.Fatalf("BuildStore: %v", err)
	}

	// CLI wins
	if v, _ := s.Get("key"); v != "from_cli" {
		t.Errorf("key: got %q, want from_cli", v)
	}
	if v, _ := s.Get("file_only"); v != "yes" {
		t.Errorf("file_only: %q", v)
	}
	if v, _ := s.Get("cli_only"); v != "yes" {
		t.Errorf("cli_only: %q", v)
	}
}

func TestBuildStoreEnvResolution(t *testing.T) {
	t.Setenv("MY_SECRET", "s3cr3t")
	f := makeFile(map[string]string{"pw": "$ENV_MY_SECRET"})
	s, err := BuildStore(f, nil)
	if err != nil {
		t.Fatalf("BuildStore: %v", err)
	}
	if v, _ := s.Get("pw"); v != "s3cr3t" {
		t.Errorf("pw: %q", v)
	}
}

func TestBuildStoreMissingEnvError(t *testing.T) {
	// ensure var is unset
	t.Setenv("APIX_TEST_MISSING_VAR", "")
	f := makeFile(map[string]string{"pw": "$ENV_APIX_TEST_MISSING_VAR"})
	_, err := BuildStore(f, nil)
	if err == nil {
		t.Fatal("expected error for missing env var")
	}
	if !strings.Contains(err.Error(), "APIX_TEST_MISSING_VAR") {
		t.Errorf("error should name the var: %v", err)
	}
}

// --- Interpolate tests ---

func store(pairs ...string) *Store {
	s := NewStore()
	for i := 0; i+1 < len(pairs); i += 2 {
		s.Set(pairs[i], pairs[i+1])
	}
	return s
}

func TestInterpolateSingleToken(t *testing.T) {
	s := store("name", "world")
	out, err := Interpolate("hello {{ name }}", s)
	if err != nil || out != "hello world" {
		t.Errorf("got %q %v", out, err)
	}
}

func TestInterpolateMultipleTokens(t *testing.T) {
	s := store("a", "1", "b", "2")
	out, err := Interpolate("{{ a }}-{{ b }}", s)
	if err != nil || out != "1-2" {
		t.Errorf("got %q %v", out, err)
	}
}

func TestInterpolateWhitespaceVariants(t *testing.T) {
	s := store("x", "val")
	cases := []string{"{{x}}", "{{ x }}", "{{  x  }}"}
	for _, c := range cases {
		out, err := Interpolate(c, s)
		if err != nil || out != "val" {
			t.Errorf("input %q: got %q %v", c, out, err)
		}
	}
}

func TestInterpolateNoTokens(t *testing.T) {
	s := store()
	out, err := Interpolate("plain string", s)
	if err != nil || out != "plain string" {
		t.Errorf("got %q %v", out, err)
	}
}

func TestInterpolateUnknownToken(t *testing.T) {
	s := store()
	_, err := Interpolate("{{ missing }}", s)
	if err == nil {
		t.Fatal("expected error for unknown token")
	}
	if !strings.Contains(err.Error(), "missing") {
		t.Errorf("error should name the token: %v", err)
	}
}

// --- Built-in tests ---

func TestBuiltinUUID(t *testing.T) {
	s := store()
	out, err := Interpolate("{{ $uuid }}", s)
	if err != nil {
		t.Fatalf("uuid: %v", err)
	}
	if len(out) != 36 {
		t.Errorf("uuid length: %d, val: %q", len(out), out)
	}
}

func TestBuiltinTimestamp(t *testing.T) {
	before := time.Now().Unix()
	s := store()
	out, err := Interpolate("{{ $timestamp }}", s)
	after := time.Now().Unix()
	if err != nil {
		t.Fatalf("timestamp: %v", err)
	}
	n, err2 := strconv.ParseInt(out, 10, 64)
	if err2 != nil {
		t.Fatalf("parse timestamp %q: %v", out, err2)
	}
	if n < before || n > after {
		t.Errorf("timestamp %d out of range [%d, %d]", n, before, after)
	}
}

func TestBuiltinISODate(t *testing.T) {
	s := store()
	out, err := Interpolate("{{ $iso_date }}", s)
	if err != nil {
		t.Fatalf("iso_date: %v", err)
	}
	if _, err := time.Parse(time.RFC3339, out); err != nil {
		t.Errorf("iso_date %q not RFC3339: %v", out, err)
	}
}

func TestBuiltinRandomInt(t *testing.T) {
	s := store()
	out, err := Interpolate("{{ $random_int }}", s)
	if err != nil {
		t.Fatalf("random_int: %v", err)
	}
	if _, err := strconv.ParseInt(out, 10, 64); err != nil {
		t.Errorf("random_int %q not an int: %v", out, err)
	}
}

func TestBuiltinFreshness(t *testing.T) {
	// Two calls should yield different UUIDs.
	s := store()
	a, _ := Interpolate("{{ $uuid }}", s)
	b, _ := Interpolate("{{ $uuid }}", s)
	if a == b {
		t.Errorf("expected different UUIDs, got same: %q", a)
	}
}

func TestBuiltinNotOverridableByStore(t *testing.T) {
	// Even if the store has a $uuid entry, the built-in generator must win.
	s := store("$uuid", "fixed-uuid")
	out, err := Interpolate("{{ $uuid }}", s)
	if err != nil {
		t.Fatalf("uuid: %v", err)
	}
	if out == "fixed-uuid" {
		t.Error("built-in $uuid was shadowed by store entry")
	}
	// It should still look like a real UUID
	if len(out) != 36 {
		t.Errorf("unexpected uuid value: %q", out)
	}
}
