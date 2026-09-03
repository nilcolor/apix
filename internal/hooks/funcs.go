package hooks

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
)

// toBytes accepts the string and []byte forms that flow between the digest and
// encoding functions, so hmac keys and encoder arguments compose either way.
func toBytes(v any, fn string) ([]byte, error) {
	switch t := v.(type) {
	case []byte:
		return t, nil
	case string:
		return []byte(t), nil
	default:
		return nil, fmt.Errorf("%s: expected a string or bytes, got %T", fn, v)
	}
}

func funcs() map[string]any {
	return map[string]any{
		"hmac_sha256": func(data, key any) ([]byte, error) {
			d, err := toBytes(data, "hmac_sha256")
			if err != nil {
				return nil, err
			}
			k, err := toBytes(key, "hmac_sha256")
			if err != nil {
				return nil, err
			}
			m := hmac.New(sha256.New, k)
			m.Write(d)
			return m.Sum(nil), nil
		},

		"sha256": func(data any) ([]byte, error) {
			d, err := toBytes(data, "sha256")
			if err != nil {
				return nil, err
			}
			sum := sha256.Sum256(d)
			return sum[:], nil
		},

		"hex": func(data any) (string, error) {
			d, err := toBytes(data, "hex")
			if err != nil {
				return "", err
			}
			return hex.EncodeToString(d), nil
		},

		"base64": func(data any) (string, error) {
			d, err := toBytes(data, "base64")
			if err != nil {
				return "", err
			}
			return base64.StdEncoding.EncodeToString(d), nil
		},

		"json": func(v any) (string, error) {
			b, err := json.Marshal(v)
			if err != nil {
				return "", fmt.Errorf("json: %w", err)
			}
			return string(b), nil
		},
	}
}
