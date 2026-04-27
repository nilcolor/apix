package runner

import (
	"encoding/json"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/nilcolor/apix/internal/schema"
	"github.com/nilcolor/apix/internal/vars"
)

func emptyCfg() *schema.Config {
	return &schema.Config{}
}

func storeWith(pairs ...string) *vars.Store {
	s := vars.NewStore()
	for i := 0; i+1 < len(pairs); i += 2 {
		s.Set(pairs[i], pairs[i+1])
	}
	return s
}

// echoServer returns a server that echos method, headers and body as JSON.
func echoServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		resp := map[string]any{
			"method":  r.Method,
			"path":    r.URL.Path,
			"query":   r.URL.RawQuery,
			"headers": r.Header,
			"body":    string(body),
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
}

func TestJSONBody(t *testing.T) {
	srv := echoServer(t)
	defer srv.Close()

	step := &schema.Step{
		Method: "POST",
		URL:    srv.URL + "/login",
		Body: map[string]any{
			"username": "alice",
			"password": "secret",
		},
	}
	resp, err := Execute(step, emptyCfg(), vars.NewStore())
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if resp.Status != 200 {
		t.Errorf("status: %d", resp.Status)
	}
	var echo map[string]any
	if err := json.Unmarshal(resp.Body, &echo); err != nil {
		t.Fatalf("parse echo: %v", err)
	}
	if echo["method"] != "POST" {
		t.Errorf("method: %v", echo["method"])
	}
}

func TestFormBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{
			"grant_type": r.FormValue("grant_type"),
			"client_id":  r.FormValue("client_id"),
		})
	}))
	defer srv.Close()

	step := &schema.Step{
		Method: "POST",
		URL:    srv.URL + "/token",
		Form: map[string]string{
			"grant_type": "password",
			"client_id":  "abc",
		},
	}
	resp, err := Execute(step, emptyCfg(), vars.NewStore())
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	var result map[string]string
	if err := json.Unmarshal(resp.Body, &result); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if result["grant_type"] != "password" || result["client_id"] != "abc" {
		t.Errorf("form values: %v", result)
	}
}

func TestQueryParams(t *testing.T) {
	srv := echoServer(t)
	defer srv.Close()

	step := &schema.Step{
		Method: "GET",
		URL:    srv.URL + "/items",
		Query:  map[string]string{"page": "2", "size": "10"},
	}
	resp, err := Execute(step, emptyCfg(), vars.NewStore())
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	var echo map[string]any
	_ = json.Unmarshal(resp.Body, &echo)
	rawQ := echo["query"].(string)
	vals, _ := url.ParseQuery(rawQ)
	if vals.Get("page") != "2" || vals.Get("size") != "10" {
		t.Errorf("query: %q", rawQ)
	}
}

func TestHeaderInterpolation(t *testing.T) {
	srv := echoServer(t)
	defer srv.Close()

	store := storeWith("my_token", "tok123")
	step := &schema.Step{
		Method:  "GET",
		URL:     srv.URL + "/me",
		Headers: map[string]string{"Authorization": "Bearer {{ my_token }}"},
	}
	resp, err := Execute(step, emptyCfg(), store)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	var echo map[string]any
	_ = json.Unmarshal(resp.Body, &echo)
	hdrs := echo["headers"].(map[string]any)
	authList := hdrs["Authorization"].([]any)
	if len(authList) == 0 || authList[0] != "Bearer tok123" {
		t.Errorf("Authorization header: %v", authList)
	}
}

func TestBaseURLAndPath(t *testing.T) {
	srv := echoServer(t)
	defer srv.Close()

	cfg := &schema.Config{BaseURL: srv.URL}
	step := &schema.Step{Method: "GET", Path: "/users"}
	resp, err := Execute(step, cfg, vars.NewStore())
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	var echo map[string]any
	_ = json.Unmarshal(resp.Body, &echo)
	if echo["path"] != "/users" {
		t.Errorf("path: %v", echo["path"])
	}
}

func TestPathInterpolation(t *testing.T) {
	srv := echoServer(t)
	defer srv.Close()

	store := storeWith("report_uuid", "abc-123")
	cfg := &schema.Config{BaseURL: srv.URL}
	step := &schema.Step{Method: "GET", Path: "/api/v3/reports/{{ report_uuid }}/report-details"}
	resp, err := Execute(step, cfg, store)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	var echo map[string]any
	_ = json.Unmarshal(resp.Body, &echo)
	if echo["path"] != "/api/v3/reports/abc-123/report-details" {
		t.Errorf("path: %v", echo["path"])
	}
}

func TestURLOverridesBaseURL(t *testing.T) {
	srv := echoServer(t)
	defer srv.Close()

	cfg := &schema.Config{BaseURL: "http://wrong.example.com"}
	step := &schema.Step{Method: "GET", URL: srv.URL + "/override"}
	resp, err := Execute(step, cfg, vars.NewStore())
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	var echo map[string]any
	_ = json.Unmarshal(resp.Body, &echo)
	if echo["path"] != "/override" {
		t.Errorf("path: %v", echo["path"])
	}
}

func TestTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
		w.WriteHeader(200)
	}))
	defer srv.Close()

	d := schema.Duration{}
	d.Duration = 50 * time.Millisecond
	cfg := &schema.Config{Timeout: d}
	step := &schema.Step{Method: "GET", URL: srv.URL + "/slow"}
	_, err := Execute(step, cfg, vars.NewStore())
	if err == nil {
		t.Fatal("expected timeout error")
	}
}

func redirectServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/redirect" {
			http.Redirect(w, r, "/dest", http.StatusFound)
			return
		}
		w.WriteHeader(200)
	}))
}

func TestNoFollowRedirectByDefault(t *testing.T) {
	srv := redirectServer(t)
	defer srv.Close()

	step := &schema.Step{Method: "GET", URL: srv.URL + "/redirect"}
	resp, err := Execute(step, emptyCfg(), vars.NewStore())
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if resp.Status != http.StatusFound {
		t.Errorf("expected 302 (no redirect), got %d", resp.Status)
	}
}

func TestFollowRedirectViaConfig(t *testing.T) {
	srv := redirectServer(t)
	defer srv.Close()

	follow := true
	cfg := &schema.Config{FollowRedirects: &follow}
	step := &schema.Step{Method: "GET", URL: srv.URL + "/redirect"}
	resp, err := Execute(step, cfg, vars.NewStore())
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if resp.Status != http.StatusOK {
		t.Errorf("expected 200 (followed redirect), got %d", resp.Status)
	}
}

func TestFollowRedirectViaStep(t *testing.T) {
	srv := redirectServer(t)
	defer srv.Close()

	follow := true
	step := &schema.Step{Method: "GET", URL: srv.URL + "/redirect", FollowRedirect: &follow}
	resp, err := Execute(step, emptyCfg(), vars.NewStore())
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if resp.Status != http.StatusOK {
		t.Errorf("expected 200 (followed redirect), got %d", resp.Status)
	}
}

func TestStepFollowRedirectOverridesConfig(t *testing.T) {
	srv := redirectServer(t)
	defer srv.Close()

	// Config says follow, step says don't.
	cfgFollow := true
	cfg := &schema.Config{FollowRedirects: &cfgFollow}
	stepNoFollow := false
	step := &schema.Step{Method: "GET", URL: srv.URL + "/redirect", FollowRedirect: &stepNoFollow}
	resp, err := Execute(step, cfg, vars.NewStore())
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if resp.Status != http.StatusFound {
		t.Errorf("expected 302 (step overrides config), got %d", resp.Status)
	}
}

func TestTLSVerifyOff(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))
	defer srv.Close()

	noVerify := false
	cfg := &schema.Config{TLSVerify: &noVerify}
	step := &schema.Step{Method: "GET", URL: srv.URL + "/"}
	resp, err := Execute(step, cfg, vars.NewStore())
	if err != nil {
		t.Fatalf("Execute (TLS verify off): %v", err)
	}
	if resp.Status != 200 {
		t.Errorf("status: %d", resp.Status)
	}
}

func TestMultipartWithFile(t *testing.T) {
	received := map[string]string{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ct := r.Header.Get("Content-Type")
		mediaType, params, _ := mime.ParseMediaType(ct)
		if !strings.HasPrefix(mediaType, "multipart/") {
			http.Error(w, "not multipart", 400)
			return
		}
		mr := multipart.NewReader(r.Body, params["boundary"])
		for {
			part, err := mr.NextPart()
			if err != nil {
				break
			}
			data, _ := io.ReadAll(part)
			received[part.FormName()] = string(data)
		}
		w.WriteHeader(200)
	}))
	defer srv.Close()

	// Create a temp file to upload.
	tmp, err := os.CreateTemp("", "apix-runner-test-*.txt")
	if err != nil {
		t.Fatalf("createtemp: %v", err)
	}
	defer os.Remove(tmp.Name())
	_, _ = tmp.WriteString("hello from file")
	tmp.Close()

	step := &schema.Step{
		Method: "POST",
		URL:    srv.URL + "/upload",
		Multipart: map[string]string{
			"file":        "@" + tmp.Name(),
			"description": "test upload",
		},
	}
	resp, err := Execute(step, emptyCfg(), vars.NewStore())
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if resp.Status != 200 {
		t.Errorf("status: %d", resp.Status)
	}
	if received["file"] != "hello from file" {
		t.Errorf("file content: %q", received["file"])
	}
	if received["description"] != "test upload" {
		t.Errorf("description: %q", received["description"])
	}
}

func TestMultipleBodyKindsError(t *testing.T) {
	step := &schema.Step{
		Method:  "POST",
		URL:     "http://localhost/",
		Body:    map[string]any{"x": 1},
		BodyRaw: "raw",
	}
	_, err := Execute(step, emptyCfg(), vars.NewStore())
	if err == nil {
		t.Fatal("expected error for multiple body kinds")
	}
}

func TestSensitiveHeaderMasking(t *testing.T) {
	srv := echoServer(t)
	defer srv.Close()

	step := &schema.Step{
		Method: "GET",
		URL:    srv.URL + "/me",
		Headers: map[string]string{
			"Authorization": "Bearer supersecret",
			"X-Normal":      "visible",
		},
	}
	resp, err := Execute(step, emptyCfg(), vars.NewStore())
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	snap := resp.Request
	if snap.Headers.Get("Authorization") != "***" {
		t.Errorf("Authorization not masked: %q", snap.Headers.Get("Authorization"))
	}
	if snap.Headers.Get("X-Normal") != "visible" {
		t.Errorf("X-Normal should not be masked: %q", snap.Headers.Get("X-Normal"))
	}
}

func TestSensitiveBodyMasking(t *testing.T) {
	srv := echoServer(t)
	defer srv.Close()

	step := &schema.Step{
		Method: "POST",
		URL:    srv.URL + "/login",
		Body: map[string]any{
			"username": "alice",
			"password": "s3cr3t",
			"token":    "tok123",
		},
	}
	resp, err := Execute(step, emptyCfg(), vars.NewStore())
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	snap := resp.Request
	var body map[string]any
	if err := json.Unmarshal(snap.Body, &body); err != nil {
		t.Fatalf("parse snapshot body: %v", err)
	}
	if body["password"] != "***" {
		t.Errorf("password not masked: %v", body["password"])
	}
	if body["token"] != "***" {
		t.Errorf("token not masked: %v", body["token"])
	}
	if body["username"] != "alice" {
		t.Errorf("username should not be masked: %v", body["username"])
	}
}

func TestBodyRaw(t *testing.T) {
	srv := echoServer(t)
	defer srv.Close()

	step := &schema.Step{
		Method:  "POST",
		URL:     srv.URL + "/raw",
		BodyRaw: "plain text payload",
	}
	resp, err := Execute(step, emptyCfg(), vars.NewStore())
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	var echo map[string]any
	_ = json.Unmarshal(resp.Body, &echo)
	if echo["body"] != "plain text payload" {
		t.Errorf("body: %v", echo["body"])
	}
}

func TestDurationCaptured(t *testing.T) {
	srv := echoServer(t)
	defer srv.Close()

	step := &schema.Step{Method: "GET", URL: srv.URL + "/"}
	resp, err := Execute(step, emptyCfg(), vars.NewStore())
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if resp.Duration <= 0 {
		t.Errorf("duration should be > 0, got %v", resp.Duration)
	}
}

func TestBodyFile(t *testing.T) {
	srv := echoServer(t)
	defer srv.Close()

	tmp, err := os.CreateTemp("", "apix-body-file-*.json")
	if err != nil {
		t.Fatalf("createtemp: %v", err)
	}
	defer os.Remove(tmp.Name())
	_, _ = tmp.WriteString(`{"user":"{{ username }}","role":"admin"}`)
	tmp.Close()

	store := storeWith("username", "alice")
	step := &schema.Step{
		Method:   "POST",
		URL:      srv.URL + "/submit",
		BodyFile: tmp.Name(),
	}
	resp, err := Execute(step, emptyCfg(), store)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if resp.Status != 200 {
		t.Errorf("status: %d", resp.Status)
	}
	var echo map[string]any
	_ = json.Unmarshal(resp.Body, &echo)
	if echo["body"] != `{"user":"alice","role":"admin"}` {
		t.Errorf("body: %v", echo["body"])
	}
}

func TestBodyFileContentType(t *testing.T) {
	var gotCT string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotCT = r.Header.Get("Content-Type")
		w.WriteHeader(200)
	}))
	defer srv.Close()

	tmp, err := os.CreateTemp("", "apix-body-file-ct-*.json")
	if err != nil {
		t.Fatalf("createtemp: %v", err)
	}
	defer os.Remove(tmp.Name())
	_, _ = tmp.WriteString(`{"ok":true}`)
	tmp.Close()

	step := &schema.Step{Method: "POST", URL: srv.URL + "/", BodyFile: tmp.Name()}
	_, err = Execute(step, emptyCfg(), vars.NewStore())
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.HasPrefix(gotCT, "application/json") {
		t.Errorf("Content-Type: %q", gotCT)
	}
}

func TestBodyFileMissing(t *testing.T) {
	step := &schema.Step{
		Method:   "POST",
		URL:      "http://localhost/",
		BodyFile: "/nonexistent/path/payload.json",
	}
	_, err := Execute(step, emptyCfg(), vars.NewStore())
	if err == nil {
		t.Fatal("expected error for missing body_file")
	}
	if !strings.Contains(err.Error(), "body_file read") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestBodyFileWithBodyError(t *testing.T) {
	step := &schema.Step{
		Method:   "POST",
		URL:      "http://localhost/",
		Body:     map[string]any{"x": 1},
		BodyFile: "/some/file.json",
	}
	_, err := Execute(step, emptyCfg(), vars.NewStore())
	if err == nil {
		t.Fatal("expected error for multiple body kinds")
	}
}
