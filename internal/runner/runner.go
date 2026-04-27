package runner

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/nilcolor/apix/internal/schema"
	"github.com/nilcolor/apix/internal/vars"
)

// sensitiveKeys are field names (case-insensitive) whose values are masked in snapshots.
var sensitiveKeys = []string{"password", "secret", "token", "authorization"}

// Response holds the result of a single HTTP request.
type Response struct {
	Status   int
	Headers  http.Header
	Body     []byte
	Request  RequestSnapshot
	Duration time.Duration
}

// RequestSnapshot captures what was sent, with sensitive fields already masked.
type RequestSnapshot struct {
	Method  string
	URL     string
	Headers http.Header
	Body    []byte
}

// Runner executes HTTP requests using a configured http.Client.
type Runner struct {
	client *http.Client
}

// New creates a Runner. followRedirects controls whether the client follows
// HTTP redirects; call effectiveFollowRedirects to resolve step+config precedence.
func New(cfg *schema.Config, followRedirects bool) *Runner {
	timeout := cfg.Timeout.Duration
	if timeout == 0 {
		timeout = 30 * time.Second
	}

	transport := &http.Transport{
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: cfg.TLSVerify != nil && !*cfg.TLSVerify, //nolint:gosec // TLS verification is intentionally user-controlled via config
		},
	}

	redirectPolicy := func(req *http.Request, via []*http.Request) error {
		if !followRedirects {
			return http.ErrUseLastResponse
		}
		if len(via) >= 10 {
			return fmt.Errorf("runner: too many redirects")
		}
		return nil
	}

	return &Runner{
		client: &http.Client{
			Timeout:       timeout,
			Transport:     transport,
			CheckRedirect: redirectPolicy,
		},
	}
}

// effectiveFollowRedirects resolves redirect behavior with step > config > default (false).
func effectiveFollowRedirects(step *schema.Step, cfg *schema.Config) bool {
	if step.FollowRedirect != nil {
		return *step.FollowRedirect
	}
	if cfg.FollowRedirects != nil {
		return *cfg.FollowRedirects
	}
	return false
}

// Execute performs the HTTP request described by step, interpolating all fields
// from the variable store and applying config defaults.
func Execute(step *schema.Step, cfg *schema.Config, store *vars.Store) (*Response, error) {
	r := New(cfg, effectiveFollowRedirects(step, cfg))
	return r.execute(step, cfg, store)
}

func (r *Runner) execute(step *schema.Step, cfg *schema.Config, store *vars.Store) (*Response, error) {
	// --- Interpolate scalar fields ---
	rawURL, err := resolveURL(step, cfg, store)
	if err != nil {
		return nil, err
	}

	headers, err := interpolateMap(step.Headers, store)
	if err != nil {
		return nil, fmt.Errorf("runner: headers: %w", err)
	}

	// Merge config default headers (step headers win).
	for k, v := range cfg.Headers {
		if _, ok := headers[k]; !ok {
			headers[k] = v
		}
	}

	query, err := interpolateMap(step.Query, store)
	if err != nil {
		return nil, fmt.Errorf("runner: query: %w", err)
	}

	// Append query params to URL.
	if len(query) > 0 {
		u, err := url.Parse(rawURL)
		if err != nil {
			return nil, fmt.Errorf("runner: parse URL %q: %w", rawURL, err)
		}
		q := u.Query()
		for k, v := range query {
			q.Set(k, v)
		}
		u.RawQuery = q.Encode()
		rawURL = u.String()
	}

	// --- Build body ---
	bodyBytes, contentType, err := buildBody(step, store)
	if err != nil {
		return nil, err
	}

	// --- Build HTTP request ---
	method := step.Method
	if method == "" {
		method = http.MethodGet
	}

	req, err := http.NewRequest(method, rawURL, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("runner: build request: %w", err)
	}

	for k, v := range headers {
		req.Header.Set(k, v)
	}
	if contentType != "" && req.Header.Get("Content-Type") == "" {
		req.Header.Set("Content-Type", contentType)
	}

	// --- Capture snapshot with masking ---
	snap := RequestSnapshot{
		Method:  method,
		URL:     rawURL,
		Headers: maskHeaders(req.Header.Clone()),
		Body:    maskBody(bodyBytes, contentType),
	}

	// --- Execute ---
	start := time.Now()
	resp, err := r.client.Do(req)
	duration := time.Since(start)
	if err != nil {
		return nil, fmt.Errorf("runner: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("runner: read response body: %w", err)
	}

	return &Response{
		Status:   resp.StatusCode,
		Headers:  resp.Header,
		Body:     respBody,
		Request:  snap,
		Duration: duration,
	}, nil
}

// resolveURL builds the final request URL by interpolating step.URL or cfg.BaseURL+step.Path.
func resolveURL(step *schema.Step, cfg *schema.Config, store *vars.Store) (string, error) {
	if step.URL != "" {
		return vars.Interpolate(step.URL, store)
	}
	base, err := vars.Interpolate(cfg.BaseURL, store)
	if err != nil {
		return "", fmt.Errorf("runner: base_url: %w", err)
	}
	path, err := vars.Interpolate(step.Path, store)
	if err != nil {
		return "", fmt.Errorf("runner: path: %w", err)
	}
	return strings.TrimRight(base, "/") + "/" + strings.TrimLeft(path, "/"), nil
}

// buildBody selects and serializes the request body. At most one body variant may be set.
func buildBody(step *schema.Step, store *vars.Store) (body []byte, contentType string, err error) {
	count := 0
	if step.Body != nil {
		count++
	}
	if len(step.Form) > 0 {
		count++
	}
	if len(step.Multipart) > 0 {
		count++
	}
	if step.BodyRaw != "" {
		count++
	}
	if step.BodyFile != "" {
		count++
	}
	if count > 1 {
		return nil, "", fmt.Errorf("runner: at most one of body/form/multipart/body_raw/body_file may be set")
	}

	if step.Body != nil {
		// Interpolate JSON body values (works for string values; numeric/bool pass through).
		interpolated, err := interpolateAny(step.Body, store)
		if err != nil {
			return nil, "", fmt.Errorf("runner: body: %w", err)
		}
		b, err := json.Marshal(interpolated)
		if err != nil {
			return nil, "", fmt.Errorf("runner: marshal body: %w", err)
		}
		return b, "application/json", nil
	}

	if len(step.Form) > 0 {
		form, err := interpolateMap(step.Form, store)
		if err != nil {
			return nil, "", fmt.Errorf("runner: form: %w", err)
		}
		vals := url.Values{}
		for k, v := range form {
			vals.Set(k, v)
		}
		return []byte(vals.Encode()), "application/x-www-form-urlencoded", nil
	}

	if len(step.Multipart) > 0 {
		return buildMultipart(step.Multipart, store)
	}

	if step.BodyRaw != "" {
		raw, err := vars.Interpolate(step.BodyRaw, store)
		if err != nil {
			return nil, "", fmt.Errorf("runner: body_raw: %w", err)
		}
		return []byte(raw), "text/plain", nil
	}

	if step.BodyFile != "" {
		path, err := vars.Interpolate(step.BodyFile, store)
		if err != nil {
			return nil, "", fmt.Errorf("runner: body_file path: %w", err)
		}
		raw, err := os.ReadFile(path) //nolint:gosec // path is user-provided, intentional
		if err != nil {
			return nil, "", fmt.Errorf("runner: body_file read %q: %w", path, err)
		}
		interpolated, err := vars.Interpolate(string(raw), store)
		if err != nil {
			return nil, "", fmt.Errorf("runner: body_file interpolate: %w", err)
		}
		return []byte(interpolated), "application/json", nil
	}

	return nil, "", nil
}

// buildMultipart encodes a multipart/form-data body. Values starting with "@" are file paths.
func buildMultipart(fields map[string]string, store *vars.Store) (body []byte, contentType string, err error) {
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)

	for k, v := range fields {
		key, err := vars.Interpolate(k, store)
		if err != nil {
			return nil, "", fmt.Errorf("runner: multipart key: %w", err)
		}
		val, err := vars.Interpolate(v, store)
		if err != nil {
			return nil, "", fmt.Errorf("runner: multipart value: %w", err)
		}

		if strings.HasPrefix(val, "@") {
			path := strings.TrimPrefix(val, "@")
			fw, err := w.CreateFormFile(key, path)
			if err != nil {
				return nil, "", fmt.Errorf("runner: multipart create file field: %w", err)
			}
			f, err := os.Open(path) //nolint:gosec // path is user-provided multipart field value, intentional
			if err != nil {
				return nil, "", fmt.Errorf("runner: multipart open %q: %w", path, err)
			}
			_, copyErr := io.Copy(fw, f)
			_ = f.Close()
			if copyErr != nil {
				return nil, "", fmt.Errorf("runner: multipart copy %q: %w", path, copyErr)
			}
		} else {
			if err := w.WriteField(key, val); err != nil {
				return nil, "", fmt.Errorf("runner: multipart write field: %w", err)
			}
		}
	}

	if err := w.Close(); err != nil {
		return nil, "", fmt.Errorf("runner: multipart close: %w", err)
	}
	return buf.Bytes(), w.FormDataContentType(), nil
}

// interpolateMap applies Interpolate to every value in m.
func interpolateMap(m map[string]string, store *vars.Store) (map[string]string, error) {
	out := make(map[string]string, len(m))
	for k, v := range m {
		interp, err := vars.Interpolate(v, store)
		if err != nil {
			return nil, fmt.Errorf("key %q: %w", k, err)
		}
		out[k] = interp
	}
	return out, nil
}

// interpolateAny recursively interpolates string values in an arbitrary structure.
func interpolateAny(v any, store *vars.Store) (any, error) {
	switch t := v.(type) {
	case string:
		return vars.Interpolate(t, store)
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, val := range t {
			interp, err := interpolateAny(val, store)
			if err != nil {
				return nil, err
			}
			out[k] = interp
		}
		return out, nil
	case []any:
		out := make([]any, len(t))
		for i, val := range t {
			interp, err := interpolateAny(val, store)
			if err != nil {
				return nil, err
			}
			out[i] = interp
		}
		return out, nil
	default:
		return v, nil
	}
}

// isSensitive reports whether a field name is sensitive (case-insensitive match).
func isSensitive(name string) bool {
	lower := strings.ToLower(name)
	for _, k := range sensitiveKeys {
		if strings.Contains(lower, k) {
			return true
		}
	}
	return false
}

// maskHeaders returns a copy of headers with sensitive header values redacted.
func maskHeaders(h http.Header) http.Header {
	for name := range h {
		if isSensitive(name) {
			h[name] = []string{"***"}
		}
	}
	return h
}

// maskBody redacts sensitive keys in a JSON body. Non-JSON bodies are returned as-is.
func maskBody(body []byte, contentType string) []byte {
	if len(body) == 0 {
		return body
	}
	if !strings.Contains(contentType, "application/json") {
		return body
	}
	var obj map[string]any
	if err := json.Unmarshal(body, &obj); err != nil {
		return body
	}
	maskMap(obj)
	masked, err := json.Marshal(obj)
	if err != nil {
		return body
	}
	return masked
}

func maskMap(m map[string]any) {
	for k, v := range m {
		if isSensitive(k) {
			m[k] = "***"
			continue
		}
		if nested, ok := v.(map[string]any); ok {
			maskMap(nested)
		}
	}
}
