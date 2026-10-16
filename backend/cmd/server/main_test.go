package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gokulkumar/exam-assistant/backend/internal/config"
	"github.com/gokulkumar/exam-assistant/backend/internal/docs"
)

func localCfg() config.Config {
	return config.Config{
		Env: "local", Stage: "local", S3Bucket: "local-bucket", Mode: "api",
		JWTSecret: strings.Repeat("s", 32), AllowedOrigins: []string{"http://localhost:3000"},
		Providers: []string{"anthropic"}, GenMaxFiles: 10, GenMaxTotalBytes: 20971520, GenMaxPDFPages: 40,
		GenMaxRunning: 2, GenMaxPerDay: 3, GenMaxRegenPerDay: 10, GenMaxGlobalPerDay: 10, GenMaxPerCall: 15,
		LLMMonthlyBudgetUSD: 15, LLMEffort: "medium",
	}
}

// startLocal serves buildHandler on a test server whose URL it advertises in presigned URLs.
func startLocal(t *testing.T, fakeLLM bool) (*httptest.Server, *http.Client) {
	t.Helper()
	srv := httptest.NewUnstartedServer(nil)
	h, err := buildHandler(localCfg(), slog.New(slog.NewJSONHandler(io.Discard, nil)), "http://"+srv.Listener.Addr().String(), fakeLLM)
	if err != nil {
		t.Fatal(err)
	}
	srv.Config.Handler = h
	srv.Start()
	t.Cleanup(srv.Close)
	jar, _ := cookiejar.New(nil)
	return srv, &http.Client{Jar: jar, Timeout: 5 * time.Second}
}

func do(t *testing.T, c *http.Client, method, url, ctype string, body []byte) (int, []byte) {
	t.Helper()
	req, err := http.NewRequest(method, url, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if ctype != "" {
		req.Header.Set("Content-Type", ctype)
	}
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, b
}

func TestLocalUploadAndFakeGenerationEndToEnd(t *testing.T) {
	srv, c := startLocal(t, true)

	code, body := do(t, c, "POST", srv.URL+"/auth/signup", "application/json",
		[]byte(`{"name":"Asha","email":"asha@example.com","password":"correct horse battery","role":"student"}`))
	if code != 201 {
		t.Fatalf("signup %d %s", code, body)
	}

	content := []byte("Photosynthesis turns light into chemical energy. Chlorophyll absorbs light.")
	code, body = do(t, c, "POST", srv.URL+"/documents/upload-url", "application/json",
		[]byte(`{"name":"notes.txt","type":"text/plain","sizeBytes":`+itoa(len(content))+`}`))
	if code != 201 {
		t.Fatalf("upload-url %d %s", code, body)
	}
	var up struct {
		DocID, UploadURL string
		Headers          map[string]string
	}
	var raw map[string]any
	_ = json.Unmarshal(body, &raw)
	up.DocID, _ = raw["docId"].(string)
	up.UploadURL, _ = raw["uploadUrl"].(string)
	if !strings.HasPrefix(up.UploadURL, srv.URL+"/local-blob/") {
		t.Fatalf("uploadUrl = %q, want it to point at the local blob endpoint", up.UploadURL)
	}
	if code, body = do(t, http.DefaultClient, "PUT", up.UploadURL, "text/plain", content); code != 200 {
		t.Fatalf("PUT %d %s", code, body)
	}
	if code, body = do(t, c, "POST", srv.URL+"/documents/"+up.DocID+"/complete", "", nil); code != 200 {
		t.Fatalf("complete %d %s", code, body)
	}

	code, body = do(t, c, "POST", srv.URL+"/generate", "application/json",
		[]byte(`{"sourceIds":["`+up.DocID+`"],"type":"mcq","counts":{"1":2,"2":1}}`))
	if code != 202 {
		t.Fatalf("generate %d %s", code, body)
	}
	var created struct {
		JobID string `json:"jobId"`
	}
	_ = json.Unmarshal(body, &created)

	var view struct {
		Status    string
		Questions []struct {
			Marks         int
			SourceDocName string
			Options       []string
		}
		Error *string
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		code, body = do(t, c, "GET", srv.URL+"/generate/"+created.JobID, "", nil)
		view.Status, view.Questions = "", nil
		_ = json.Unmarshal(body, &view)
		if view.Status == "done" || view.Status == "failed" || time.Now().After(deadline) {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if view.Status != "done" || len(view.Questions) != 3 {
		t.Fatalf("poll: %d %s", code, body)
	}
	if view.Questions[0].Marks != 1 || view.Questions[2].Marks != 2 || view.Questions[0].SourceDocName != "notes.txt" || len(view.Questions[0].Options) != 4 {
		t.Errorf("questions = %+v", view.Questions)
	}
}

func TestWithoutFakeLLMGenerationFailsCleanly(t *testing.T) {
	srv, c := startLocal(t, false)
	do(t, c, "POST", srv.URL+"/auth/signup", "application/json",
		[]byte(`{"name":"Asha","email":"asha@example.com","password":"correct horse battery","role":"student"}`))
	_, body := do(t, c, "POST", srv.URL+"/documents/upload-url", "application/json", []byte(`{"name":"n.txt","type":"text/plain","sizeBytes":5}`))
	var raw map[string]any
	_ = json.Unmarshal(body, &raw)
	do(t, http.DefaultClient, "PUT", raw["uploadUrl"].(string), "text/plain", []byte("hello"))
	do(t, c, "POST", srv.URL+"/documents/"+raw["docId"].(string)+"/complete", "", nil)
	_, body = do(t, c, "POST", srv.URL+"/generate", "application/json", []byte(`{"sourceIds":["`+raw["docId"].(string)+`"],"type":"free","counts":{"1":1}}`))
	var created struct {
		JobID string `json:"jobId"`
	}
	_ = json.Unmarshal(body, &created)
	deadline := time.Now().Add(5 * time.Second)
	var view struct {
		Status string
		Error  string
	}
	for time.Now().Before(deadline) {
		_, body = do(t, c, "GET", srv.URL+"/generate/"+created.JobID, "", nil)
		_ = json.Unmarshal(body, &view)
		if view.Status == "failed" {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if view.Status != "failed" || view.Error != "We couldn't generate questions. Try again." {
		t.Errorf("view = %+v", view)
	}
}

func TestLocalBlobEndpoint(t *testing.T) {
	srv, _ := startLocal(t, true)
	key := "users/u1/d1/a.txt"
	if code, _ := do(t, http.DefaultClient, "PUT", srv.URL+"/local-blob/"+key, "text/plain", []byte("abc")); code != 200 {
		t.Fatalf("PUT = %d", code)
	}
	if code, body := do(t, http.DefaultClient, "GET", srv.URL+"/local-blob/"+key, "", nil); code != 200 || string(body) != "abc" {
		t.Errorf("GET = %d %q", code, body)
	}
	if code, _ := do(t, http.DefaultClient, "GET", srv.URL+"/local-blob/users/u1/d1/missing.txt", "", nil); code != 404 {
		t.Errorf("missing GET = %d", code)
	}
	for _, bad := range []string{"/local-blob/", "/local-blob/etc/passwd", "/local-blob/users/../x", "/local-blob/users/u/%2e%2e/x"} {
		if code, _ := do(t, http.DefaultClient, "PUT", srv.URL+bad, "text/plain", []byte("x")); code != 400 && code != 404 && code != 301 {
			t.Errorf("PUT %s = %d, want it refused", bad, code)
		}
	}
	// Cross-origin uploads from the frontend (localhost:3000) need a preflight answer.
	req, _ := http.NewRequest("OPTIONS", srv.URL+"/local-blob/"+key, nil)
	req.Header.Set("Origin", "http://localhost:3000")
	req.Header.Set("Access-Control-Request-Method", "PUT")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode/100 != 2 || resp.Header.Get("Access-Control-Allow-Origin") == "" {
		t.Errorf("preflight = %d %v", resp.StatusCode, resp.Header)
	}
}

func TestLocalBlobOnlyInServerPackage(t *testing.T) {
	// The local blob lives in package main of cmd/server: PresignPut points at it.
	lb := newLocalBlob(docs.NewMemBlob(), "http://localhost:8080")
	u, err := lb.PresignPut(context.Background(), "users/u/d/f.txt", "text/plain", 3, time.Minute)
	if err != nil || u != "http://localhost:8080/local-blob/users/u/d/f.txt" {
		t.Errorf("url = %q %v", u, err)
	}
}

func TestBaseURLFromAddr(t *testing.T) {
	for addr, want := range map[string]string{
		":8080": "http://localhost:8080", "0.0.0.0:9000": "http://localhost:9000",
		"127.0.0.1:8081": "http://127.0.0.1:8081", "localhost:3001": "http://localhost:3001",
	} {
		if got := baseURLFromAddr(addr); got != want {
			t.Errorf("baseURLFromAddr(%q) = %q, want %q", addr, got, want)
		}
	}
}

func itoa(n int) string { b, _ := json.Marshal(n); return string(b) }
