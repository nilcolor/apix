package hooks

import (
	"fmt"
	"sort"
	"strings"

	"github.com/nilcolor/apix/internal/vars"
)

// Reserved names cannot be used as store or hook variable names: they would
// shadow, or be shadowed by, the request projection and the function library.
func Reserved(name string) bool {
	switch name {
	case "request", "builtin":
		return true
	}
	_, ok := funcs()[name]
	return ok
}

// ReservedNames lists every reserved name, for error messages.
func ReservedNames() string {
	f := funcs()
	names := make([]string, 0, len(f)+2)
	names = append(names, "request", "builtin")
	for k := range f {
		names = append(names, k)
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}

// Request is the projection of the outgoing request a before_send hook can read.
// Headers are deliberately absent: they are rendered after the hook runs, so the
// hook can never observe the values that will actually be sent.
type Request struct {
	Method string
	URL    string
	Path   string
	Query  string
	Body   string
}

// Env is everything a hook expression can reference.
type Env struct {
	Request Request
	Store   *vars.Store
}

// build flattens Env into the map expr evaluates against. Store variables are
// exposed by bare name; frozen time built-ins appear under builtin.*, stripped of
// the "$" that identifies them in {{ }} syntax.
//
// A store variable named after a reserved name is an error rather than a silent
// shadowing in either direction.
func (e Env) build() (map[string]any, error) {
	m := map[string]any{
		"request": map[string]any{
			"method": e.Request.Method,
			"url":    e.Request.URL,
			"path":   e.Request.Path,
			"query":  e.Request.Query,
			"body":   e.Request.Body,
		},
	}

	builtin := map[string]any{}
	for name, v := range e.Store.FrozenBuiltins() {
		builtin[name[1:]] = v
	}
	m["builtin"] = builtin

	for k, v := range e.Store.All() {
		if Reserved(k) {
			return nil, fmt.Errorf("variable %q collides with a reserved hook name (%s)", k, ReservedNames())
		}
		m[k] = v
	}

	for name, fn := range funcs() {
		m[name] = fn
	}

	return m, nil
}
