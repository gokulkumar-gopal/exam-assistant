package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gokulkumar/exam-assistant/backend/internal/apperr"
	"github.com/gokulkumar/exam-assistant/backend/internal/clock"
)

func silentLogger() *slog.Logger { return slog.New(slog.NewJSONHandler(io.Discard, nil)) }

func newTestRouter() http.Handler {
	return NewRouter(Deps{Logger: silentLogger(), Clock: clock.Real{}})
}

func do(h http.Handler, req *http.Request) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

type errBody struct {
	Error struct {
		Code    string            `json:"code"`
		Message string            `json:"message"`
		Fields  map[string]string `json:"fields"`
	} `json:"error"`
}

func TestHealth(t *testing.T) {
	rec := do(newTestRouter(), httptest.NewRequest(http.MethodGet, "/health", nil))
	if rec.Code != 200 {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if got := strings.TrimSpace(rec.Body.String()); got != `{"status":"ok"}` {
		t.Errorf("body = %s", got)
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", cc)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type = %q", ct)
	}
	if rec.Header().Get("X-Request-Id") == "" {
		t.Error("missing X-Request-Id")
	}
}

func TestRequestIDIsULIDAndUnique(t *testing.T) {
	h := newTestRouter()
	a := do(h, httptest.NewRequest(http.MethodGet, "/health", nil)).Header().Get("X-Request-Id")
	b := do(h, httptest.NewRequest(http.MethodGet, "/health", nil)).Header().Get("X-Request-Id")
	if len(a) != 26 || a == b {
		t.Errorf("request IDs %q, %q: want distinct 26-char ULIDs", a, b)
	}
}

func TestErrorShape(t *testing.T) {
	h := wrap(Deps{Logger: silentLogger()}, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		WriteError(w, r, apperr.Validation("Title is required.", map[string]string{"title": "Required"}))
	}))
	rec := do(h, httptest.NewRequest(http.MethodGet, "/x", nil))
	if rec.Code != 422 {
		t.Fatalf("status = %d, want 422", rec.Code)
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "no-store" {
		t.Errorf("Cache-Control = %q", cc)
	}
	var b errBody
	if err := json.Unmarshal(rec.Body.Bytes(), &b); err != nil {
		t.Fatal(err)
	}
	if b.Error.Code != "validation" || b.Error.Message != "Title is required." || b.Error.Fields["title"] != "Required" {
		t.Errorf("body = %s", rec.Body.String())
	}
}

func TestErrorShapeOmitsEmptyFields(t *testing.T) {
	rec := httptest.NewRecorder()
	WriteError(rec, httptest.NewRequest(http.MethodGet, "/", nil), apperr.NotFound())
	if rec.Code != 404 || strings.Contains(rec.Body.String(), "fields") {
		t.Errorf("status %d body %s", rec.Code, rec.Body.String())
	}
}

func TestInternalErrorHidesDetail(t *testing.T) {
	secret := "dynamo table prod-users password=hunter2"
	h := wrap(Deps{Logger: silentLogger()}, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		WriteError(w, r, apperr.Internal(errors.New(secret)))
	}))
	rec := do(h, httptest.NewRequest(http.MethodGet, "/x", nil))
	if rec.Code != 500 {
		t.Fatalf("status = %d", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "hunter2") || strings.Contains(rec.Body.String(), "dynamo") {
		t.Errorf("body leaks detail: %s", rec.Body.String())
	}
	var b errBody
	_ = json.Unmarshal(rec.Body.Bytes(), &b)
	if b.Error.Code != "internal" {
		t.Errorf("code = %q", b.Error.Code)
	}
}

func TestPlainErrorBecomesInternal(t *testing.T) {
	rec := httptest.NewRecorder()
	WriteError(rec, httptest.NewRequest(http.MethodGet, "/", nil), errors.New("boom secret"))
	if rec.Code != 500 || strings.Contains(rec.Body.String(), "secret") {
		t.Errorf("status %d body %s", rec.Code, rec.Body.String())
	}
}

func TestPanicRecovery(t *testing.T) {
	h := wrap(Deps{Logger: silentLogger()}, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("kaboom secret")
	}))
	rec := do(h, httptest.NewRequest(http.MethodGet, "/x", nil))
	if rec.Code != 500 || strings.Contains(rec.Body.String(), "kaboom") {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
	var b errBody
	_ = json.Unmarshal(rec.Body.Bytes(), &b)
	if b.Error.Code != "internal" {
		t.Errorf("code = %q", b.Error.Code)
	}
}

func TestUnknownRouteIsJSON404(t *testing.T) {
	rec := do(newTestRouter(), httptest.NewRequest(http.MethodGet, "/nope", nil))
	var b errBody
	if err := json.Unmarshal(rec.Body.Bytes(), &b); err != nil || rec.Code != 404 || b.Error.Code != "not_found" {
		t.Errorf("status %d body %s", rec.Code, rec.Body.String())
	}
}

func TestNonJSONBody415(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/health", strings.NewReader("hello"))
	req.Header.Set("Content-Type", "text/plain")
	rec := do(newTestRouter(), req)
	if rec.Code != 415 {
		t.Fatalf("status = %d, want 415", rec.Code)
	}
	var b errBody
	_ = json.Unmarshal(rec.Body.Bytes(), &b)
	if b.Error.Code != "unsupported_media_type" {
		t.Errorf("code = %q", b.Error.Code)
	}
}

func TestJSONContentTypeWithCharsetAccepted(t *testing.T) {
	h := wrap(Deps{Logger: silentLogger()}, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		WriteJSON(w, 200, map[string]string{"ok": "1"})
	}))
	req := httptest.NewRequest(http.MethodPost, "/x", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	if rec := do(h, req); rec.Code != 200 {
		t.Errorf("status = %d, want 200", rec.Code)
	}
}

func TestBodylessPostNotRejected(t *testing.T) {
	h := wrap(Deps{Logger: silentLogger()}, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		WriteJSON(w, 200, map[string]string{"ok": "1"})
	}))
	if rec := do(h, httptest.NewRequest(http.MethodPost, "/x", nil)); rec.Code != 200 {
		t.Errorf("status = %d, want 200", rec.Code)
	}
}

type payload struct {
	Name string `json:"name"`
}

func decodeHandler(max int64) http.Handler {
	return wrap(Deps{Logger: silentLogger()}, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var p payload
		if err := Decode(w, r, &p, max); err != nil {
			WriteError(w, r, err)
			return
		}
		WriteJSON(w, 200, p)
	}))
}

func post(h http.Handler, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/x", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	return do(h, req)
}

func TestBodyCap(t *testing.T) {
	const max = 1 << 20
	h := decodeHandler(max)

	// Valid JSON padded to exactly the cap passes; one more byte fails with 422.
	prefix := `{"name":"`
	suffix := `"}`
	pad := func(total int) string {
		return prefix + strings.Repeat("a", total-len(prefix)-len(suffix)) + suffix
	}
	if rec := post(h, pad(max)); rec.Code != 200 {
		t.Errorf("exactly 1 MB: status = %d, want 200", rec.Code)
	}
	rec := post(h, pad(max+1))
	if rec.Code != 422 {
		t.Fatalf("1 MB + 1: status = %d, want 422", rec.Code)
	}
	var b errBody
	_ = json.Unmarshal(rec.Body.Bytes(), &b)
	if b.Error.Code != "validation" {
		t.Errorf("code = %q", b.Error.Code)
	}
}

func TestDecodeRejectsBadInput(t *testing.T) {
	h := decodeHandler(1024)
	for name, body := range map[string]string{
		"unknown field": `{"name":"a","extra":1}`,
		"malformed":     `{"name":`,
		"empty":         ``,
		"trailing":      `{"name":"a"} {"name":"b"}`,
		"wrong type":    `{"name":5}`,
	} {
		if rec := post(h, body); rec.Code != 422 {
			t.Errorf("%s: status = %d, want 422", name, rec.Code)
		}
	}
	if rec := post(h, `{"name":"ok"}`); rec.Code != 200 || !bytes.Contains(rec.Body.Bytes(), []byte(`"ok"`)) {
		t.Errorf("valid: status %d body %s", rec.Code, rec.Body.String())
	}
}

func TestOneLogLinePerRequest(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))
	h := NewRouter(Deps{Logger: logger, Clock: clock.Real{}})
	rec := do(h, httptest.NewRequest(http.MethodGet, "/health", nil))
	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 1 {
		t.Fatalf("got %d log lines: %s", len(lines), buf.String())
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &m); err != nil {
		t.Fatal(err)
	}
	if m["request_id"] != rec.Header().Get("X-Request-Id") || m["method"] != "GET" || m["path"] != "/health" || m["status"] != float64(200) {
		t.Errorf("log line = %v", m)
	}
	if _, ok := m["duration_ms"]; !ok {
		t.Errorf("missing duration_ms: %v", m)
	}
}
