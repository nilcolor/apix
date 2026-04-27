package vars

import (
	"crypto/rand"
	"fmt"
	"math/big"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/nilcolor/apix/internal/schema"
)

// Store is a flat key→value variable map.
type Store struct {
	m map[string]string
}

func NewStore() *Store {
	return &Store{m: make(map[string]string)}
}

func (s *Store) Set(key, value string) {
	s.m[key] = value
}

func (s *Store) Get(key string) (string, bool) {
	v, ok := s.m[key]
	return v, ok
}

func (s *Store) Has(key string) bool {
	_, ok := s.m[key]
	return ok
}

// Merge copies all entries from other into s. Existing keys in s are overwritten.
func (s *Store) Merge(other *Store) {
	for k, v := range other.m {
		s.m[k] = v
	}
}

// BuildStore builds a Store from a fully-resolved RequestFile and CLI overrides.
// Priority (highest wins): cliVars → file.Variables → included-file variables.
// $ENV_FOO references in Variables are resolved via os.Getenv; missing values error.
//
// The caller must invoke godotenv.Load before BuildStore so --env values are present
// in the OS environment.
func BuildStore(file *schema.RequestFile, cliVars map[string]string) (*Store, error) {
	s := NewStore()

	// Apply variables from the file (which already has included vars merged at lower
	// priority by the loader). The loader's merged file.Variables already reflects
	// priority: current-file vars override included vars.
	for k, v := range file.Variables {
		resolved, err := resolveEnvRef(k, v)
		if err != nil {
			return nil, err
		}
		s.Set(k, resolved)
	}

	// CLI vars win over everything.
	for k, v := range cliVars {
		s.Set(k, v)
	}

	return s, nil
}

// resolveEnvRef resolves $ENV_FOO references via os.Getenv.
func resolveEnvRef(key, value string) (string, error) {
	if !strings.HasPrefix(value, "$ENV_") {
		return value, nil
	}
	envName := strings.TrimPrefix(value, "$ENV_")
	envVal := os.Getenv(envName)
	if envVal == "" {
		return "", fmt.Errorf("vars: environment variable %q (referenced by %q) is not set or empty", envName, key)
	}
	return envVal, nil
}

// builtins returns a fresh value for known built-in names.
// Returns ("", false) if name is not a built-in.
func builtins(name string) (string, bool) {
	switch name {
	case "$uuid":
		return uuid.New().String(), true
	case "$timestamp":
		return fmt.Sprintf("%d", time.Now().Unix()), true
	case "$iso_date":
		return time.Now().UTC().Format(time.RFC3339), true
	case "$random_int":
		n, err := rand.Int(rand.Reader, big.NewInt(1<<31))
		if err != nil {
			return "0", true
		}
		return n.String(), true
	}
	return "", false
}

// Interpolate replaces every {{ name }} token in s with its value.
// Lookup order: store → built-in generators → error on unknown.
// Whitespace inside {{ }} is trimmed. Built-ins are never overridable by the store.
func Interpolate(s string, store *Store) (string, error) {
	var b strings.Builder
	rest := s
	for {
		open := strings.Index(rest, "{{")
		if open == -1 {
			b.WriteString(rest)
			break
		}
		closing := strings.Index(rest[open:], "}}")
		if closing == -1 {
			// No closing braces — write remainder as-is.
			b.WriteString(rest)
			break
		}
		closing += open // adjust to absolute index in rest

		b.WriteString(rest[:open])
		name := strings.TrimSpace(rest[open+2 : closing])
		rest = rest[closing+2:]

		// Built-ins take absolute priority — they cannot be shadowed by the store.
		if val, ok := builtins(name); ok {
			b.WriteString(val)
			continue
		}

		val, ok := store.Get(name)
		if !ok {
			return "", fmt.Errorf("vars: unknown variable %q", name)
		}
		b.WriteString(val)
	}
	return b.String(), nil
}
