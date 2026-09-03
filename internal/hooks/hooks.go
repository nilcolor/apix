package hooks

import (
	"fmt"
	"strconv"

	"github.com/expr-lang/expr"

	"github.com/nilcolor/apix/internal/schema"
	"github.com/nilcolor/apix/internal/vars"
)

// Run evaluates a hook's expressions in document order and returns the resulting
// variables. Each result is visible to the expressions that follow it.
//
// Results are committed to the store only when every expression succeeds: a hook
// that fails halfway must not leave a stale value behind for a later step to use.
func Run(h *schema.Hook, env Env, store *vars.Store) (map[string]string, error) {
	if h == nil || len(h.Vars) == 0 {
		return nil, nil
	}

	base, err := env.build()
	if err != nil {
		return nil, err
	}
	scratch := make(map[string]string, len(h.Vars))

	for _, hv := range h.Vars {
		out, err := expr.Eval(hv.Expr, base)
		if err != nil {
			return nil, fmt.Errorf("hook %q: %w", hv.Name, err)
		}

		s, err := asResult(out)
		if err != nil {
			return nil, fmt.Errorf("hook %q: %w", hv.Name, err)
		}

		base[hv.Name] = s
		scratch[hv.Name] = s
	}

	for k, v := range scratch {
		store.Set(k, v)
	}
	return scratch, nil
}

// asResult enforces that a hook produces a string. Raw digest bytes are rejected
// here rather than stringified, because they would otherwise reach an HTTP header
// value unencoded. Numbers are formatted without an exponent: expr yields float64
// from any division, and %v would put scientific notation on the wire.
func asResult(v any) (string, error) {
	switch t := v.(type) {
	case string:
		return t, nil
	case []byte:
		return "", fmt.Errorf("expression produced raw bytes; wrap it in hex() or base64()")
	case bool:
		return strconv.FormatBool(t), nil
	case int:
		return strconv.Itoa(t), nil
	case int64:
		return strconv.FormatInt(t, 10), nil
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64), nil
	case nil:
		return "", fmt.Errorf("expression produced no value")
	default:
		return "", fmt.Errorf("expression produced %T; hook results must be strings", v)
	}
}
