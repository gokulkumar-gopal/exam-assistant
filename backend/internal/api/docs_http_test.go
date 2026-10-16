package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/gokulkumar/exam-assistant/backend/internal/auth"
	"github.com/gokulkumar/exam-assistant/backend/internal/clock"
	"github.com/gokulkumar/exam-assistant/backend/internal/docs"
	"github.com/gokulkumar/exam-assistant/backend/internal/store"
	"github.com/gokulkumar/exam-assistant/backend/internal/store/memory"
)

type docsEnv struct {
	h      http.Handler
	cookie *http.Cookie
	blob   *docs.MemBlob
	docs   *memory.DocStore
}

func newDocsEnv(t *testing.T) *docsEnv {
	t.Helper()
	users := memory.NewUserStore()
	clk := clock.NewFake(authT0)
	ds := memory.NewDocStore(clk)
	blob := docs.NewMemBlob()
	svc := auth.NewService(users, auth.NewBcryptHasher(bcrypt.MinCost), clk)
	h := NewRouter(Deps{
		Logger: silentLogger(), Clock: clk, Users: users, Auth: svc,
		Sessions:       auth.NewSessions([]byte(authSecret), "local"),
		Docs:           docs.NewService(ds, blob, clk),
		AllowedOrigins: []string{"http://localhost:3000"},
	})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, jsonReq("POST", "/auth/signup", signupBody))
	c := sessionCookie(rec)
	if c == nil {
		t.Fatalf("signup failed: %s", rec.Body)
	}
	return &docsEnv{h: h, cookie: c, blob: blob, docs: ds}
}

func (e *docsEnv) post(body string, withCookie bool, origin string) *httptest.ResponseRecorder {
	req := jsonReq("POST", "/documents/upload-url", body)
	if withCookie {
		req.AddCookie(e.cookie)
	}
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	return rec
}

func TestUploadURLRoute201(t *testing.T) {
	e := newDocsEnv(t)
	rec := e.post(`{"name":"Biology.pdf","type":"application/pdf","sizeBytes":481234}`, true, "http://localhost:3000")
	if rec.Code != http.StatusCreated {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	var out struct {
		DocID     string            `json:"docId"`
		UploadURL string            `json:"uploadUrl"`
		Headers   map[string]string `json:"headers"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.DocID == "" || out.UploadURL == "" || out.Headers["Content-Type"] != "application/pdf" {
		t.Errorf("body = %s", rec.Body)
	}
	if len(e.blob.Puts) != 1 || e.blob.Puts[0].Size != 481234 {
		t.Errorf("puts = %+v", e.blob.Puts)
	}
}

func TestUploadURLRouteRequiresAuth(t *testing.T) {
	e := newDocsEnv(t)
	rec := e.post(`{"name":"a","type":"text/plain","sizeBytes":1}`, false, "")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status %d", rec.Code)
	}
}

func TestUploadURLRouteCSRF(t *testing.T) {
	e := newDocsEnv(t)
	rec := e.post(`{"name":"a","type":"text/plain","sizeBytes":1}`, true, "https://evil.example")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status %d", rec.Code)
	}
	if len(e.blob.Puts) != 0 {
		t.Error("presigned for forbidden origin")
	}
}

func TestUploadURLRouteValidation(t *testing.T) {
	e := newDocsEnv(t)
	for body, field := range map[string]string{
		`{"name":"a","type":"application/zip","sizeBytes":1}`:   "type",
		`{"name":"a","type":"text/plain","sizeBytes":0}`:        "sizeBytes",
		`{"name":"a","type":"text/plain","sizeBytes":20971521}`: "sizeBytes",
		`{"name":"a","type":"text/plain"}`:                      "sizeBytes",
	} {
		rec := e.post(body, true, "")
		if rec.Code != http.StatusUnprocessableEntity {
			t.Fatalf("%s: status %d", body, rec.Code)
		}
		var env struct {
			Error struct {
				Fields map[string]string `json:"fields"`
			} `json:"error"`
		}
		_ = json.Unmarshal(rec.Body.Bytes(), &env)
		if _, ok := env.Error.Fields[field]; !ok {
			t.Errorf("%s: fields = %v", body, env.Error.Fields)
		}
	}
	if rec := e.post(`{"name":"a","bogus":1}`, true, ""); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("unknown field status %d", rec.Code)
	}
}

func TestUploadURLRouteWrongMethodIsJSON405(t *testing.T) {
	e := newDocsEnv(t)
	req := httptest.NewRequest("GET", "/documents/upload-url", nil)
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	if rec.Code != http.StatusMethodNotAllowed || rec.Header().Get("Allow") != "DELETE, POST" || !strings.Contains(rec.Body.String(), "method_not_allowed") {
		t.Fatalf("status %d allow %q body %s", rec.Code, rec.Header().Get("Allow"), rec.Body)
	}
}

func TestCompleteRoute200AndErrors(t *testing.T) {
	e := newDocsEnv(t)
	rec := e.post(`{"name":"a.png","type":"image/png","sizeBytes":40}`, true, "")
	var up struct {
		DocID string `json:"docId"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &up); err != nil {
		t.Fatal(err)
	}
	complete := func(id string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("POST", "/documents/"+id+"/complete", nil)
		req.AddCookie(e.cookie)
		r := httptest.NewRecorder()
		e.h.ServeHTTP(r, req)
		return r
	}
	if r := complete(up.DocID); r.Code != http.StatusConflict {
		t.Fatalf("missing object: %d %s", r.Code, r.Body)
	}
	e.blob.Put(e.blob.Puts[0].Key, append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, 32)...))
	r := complete(up.DocID)
	if r.Code != http.StatusOK {
		t.Fatalf("complete: %d %s", r.Code, r.Body)
	}
	var doc map[string]any
	_ = json.Unmarshal(r.Body.Bytes(), &doc)
	if doc["id"] != up.DocID || doc["name"] != "a.png" || doc["type"] != "PNG" || doc["pages"] != float64(1) || doc["sizeBytes"] != float64(40) || doc["createdAt"] == nil {
		t.Errorf("doc = %v", doc)
	}
	if r := complete("missing"); r.Code != http.StatusNotFound {
		t.Errorf("missing doc: %d", r.Code)
	}
	req := httptest.NewRequest("POST", "/documents/"+up.DocID+"/complete", nil)
	r2 := httptest.NewRecorder()
	e.h.ServeHTTP(r2, req)
	if r2.Code != http.StatusUnauthorized {
		t.Errorf("no cookie: %d", r2.Code)
	}
}

func TestCompleteRouteWrongMethodIsJSON405(t *testing.T) {
	e := newDocsEnv(t)
	req := httptest.NewRequest("GET", "/documents/abc123/complete", nil)
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	if rec.Code != http.StatusMethodNotAllowed || rec.Header().Get("Allow") != "POST" || !strings.Contains(rec.Body.String(), "method_not_allowed") {
		t.Fatalf("status %d allow %q body %s", rec.Code, rec.Header().Get("Allow"), rec.Body)
	}
	req = httptest.NewRequest("GET", "/documents/abc123/nothing", nil)
	rec = httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("unknown path: %d", rec.Code)
	}
}

// --- list, file URL, delete (B-1.7) ---

// events records the order of side effects across the blob and the doc store.
type events struct {
	mu  sync.Mutex
	log []string
}

func (ev *events) add(s string) { ev.mu.Lock(); ev.log = append(ev.log, s); ev.mu.Unlock() }

type recBlob struct {
	*docs.MemBlob
	ev *events
}

func (b recBlob) Delete(ctx context.Context, key string) error {
	b.ev.add("blob.delete " + key)
	return b.MemBlob.Delete(ctx, key)
}

type recDocs struct {
	*memory.DocStore
	ev *events
}

func (d recDocs) DeleteDoc(ctx context.Context, owner, id string) error {
	d.ev.add("row.delete " + id)
	return d.DocStore.DeleteDoc(ctx, owner, id)
}

type docsEnv2 struct {
	h     http.Handler
	clk   *clock.Fake
	blob  *docs.MemBlob
	store *memory.DocStore
	ev    *events
	users *memory.UserStore
}

func newDocsEnv2(t *testing.T) *docsEnv2 {
	t.Helper()
	users := memory.NewUserStore()
	clk := clock.NewFake(authT0)
	ds := memory.NewDocStore(clk)
	blob := docs.NewMemBlob()
	ev := &events{}
	svc := auth.NewService(users, auth.NewBcryptHasher(bcrypt.MinCost), clk)
	h := NewRouter(Deps{
		Logger: silentLogger(), Clock: clk, Users: users, Auth: svc,
		Sessions:       auth.NewSessions([]byte(authSecret), "local"),
		Docs:           docs.NewService(recDocs{ds, ev}, recBlob{blob, ev}, clk),
		AllowedOrigins: []string{"http://localhost:3000"},
	})
	return &docsEnv2{h: h, clk: clk, blob: blob, store: ds, ev: ev, users: users}
}

// signup creates a user and returns their cookie and ID.
func (e *docsEnv2) signup(t *testing.T, email string) (*http.Cookie, string) {
	t.Helper()
	body := strings.Replace(signupBody, "asha@example.com", email, 1)
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, jsonReq("POST", "/auth/signup", body))
	c := sessionCookie(rec)
	if c == nil {
		t.Fatalf("signup: %s", rec.Body)
	}
	var out struct {
		User struct {
			ID string `json:"id"`
		} `json:"user"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if out.User.ID == "" {
		u, err := e.users.GetByEmail(context.Background(), email)
		if err != nil {
			t.Fatal(err)
		}
		out.User.ID = u.ID
	}
	return c, out.User.ID
}

func (e *docsEnv2) do(method, path string, c *http.Cookie) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, nil)
	if c != nil {
		req.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	return rec
}

func (e *docsEnv2) seed(t *testing.T, owner, id, name, status string, created time.Time) store.Document {
	t.Helper()
	d := store.Document{
		ID: id, OwnerID: owner, Name: name, Type: "PDF", ContentType: "application/pdf",
		SizeBytes: 10, Pages: 2, S3Key: "users/" + owner + "/" + id + "/" + name,
		Status: status, CreatedAt: created,
	}
	if status == store.DocPending {
		d.Pages = 0
		d.TTL = created.Add(time.Hour).Unix()
	}
	if err := e.store.PutDoc(context.Background(), d); err != nil {
		t.Fatal(err)
	}
	e.blob.Put(d.S3Key, []byte("x"))
	return d
}

func TestListDocsReadyOnlyNewestFirst(t *testing.T) {
	e := newDocsEnv2(t)
	c, uid := e.signup(t, "ada@example.com")
	_, other := e.signup(t, "bob@example.com")
	e.seed(t, uid, "01A", "old Ünï.pdf", store.DocReady, authT0.Add(-2*time.Hour))
	e.seed(t, uid, "01C", "new.pdf", store.DocReady, authT0.Add(-time.Hour))
	e.seed(t, uid, "01B", "pending.pdf", store.DocPending, authT0.Add(-90*time.Minute))
	e.seed(t, other, "01D", "theirs.pdf", store.DocReady, authT0)

	rec := e.do("GET", "/documents", c)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	var out struct {
		Documents  []map[string]any `json:"documents"`
		ServerTime string           `json:"serverTime"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Documents) != 2 || out.Documents[0]["id"] != "01C" || out.Documents[1]["id"] != "01A" {
		t.Fatalf("documents = %v", out.Documents)
	}
	if out.Documents[1]["name"] != "old Ünï.pdf" || out.Documents[1]["type"] != "PDF" || out.Documents[1]["pages"] != float64(2) ||
		out.Documents[1]["sizeBytes"] != float64(10) || out.Documents[1]["createdAt"] != "2026-10-16T10:00:00Z" {
		t.Errorf("doc json = %v", out.Documents[1])
	}
	if out.ServerTime != "2026-10-16T12:00:00Z" {
		t.Errorf("serverTime = %q", out.ServerTime)
	}
	if rec := e.do("GET", "/documents", nil); rec.Code != http.StatusUnauthorized {
		t.Errorf("no cookie: %d", rec.Code)
	}
	// An empty list is [] not null.
	c2, _ := e.signup(t, "cy@example.com")
	if rec := e.do("GET", "/documents", c2); !strings.Contains(rec.Body.String(), `"documents":[]`) {
		t.Errorf("empty = %s", rec.Body)
	}
}

func TestFileURL404ForOtherUser(t *testing.T) {
	e := newDocsEnv2(t)
	oc, owner := e.signup(t, "ada@example.com")
	c2, _ := e.signup(t, "bob@example.com")
	e.seed(t, owner, "01A", "a.pdf", store.DocReady, authT0)
	e.seed(t, owner, "01P", "p.pdf", store.DocPending, authT0)
	if rec := e.do("GET", "/documents/01A/file", c2); rec.Code != http.StatusNotFound {
		t.Fatalf("other user: %d %s", rec.Code, rec.Body)
	}
	if len(e.blob.Gets) != 0 {
		t.Error("presigned for another user's document")
	}
	for _, id := range []string{"missing", "01P"} {
		if rec := e.do("GET", "/documents/"+id+"/file", oc); rec.Code != http.StatusNotFound {
			t.Errorf("%s: %d", id, rec.Code)
		}
	}
	if len(e.blob.Gets) != 0 {
		t.Error("presigned for missing or pending document")
	}
}

func TestFileURLExpiresAt10Min(t *testing.T) {
	e := newDocsEnv2(t)
	c, uid := e.signup(t, "ada@example.com")
	d := e.seed(t, uid, "01A", "a.pdf", store.DocReady, authT0)
	rec := e.do("GET", "/documents/01A/file", c)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	var out struct{ URL, ExpiresAt string }
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if out.URL == "" || out.ExpiresAt != "2026-10-16T12:10:00Z" {
		t.Errorf("body = %s", rec.Body)
	}
	if len(e.blob.Gets) != 1 || e.blob.Gets[0] != (docs.GetCall{Key: d.S3Key, ContentType: "application/pdf", TTL: 10 * time.Minute}) {
		t.Errorf("gets = %+v", e.blob.Gets)
	}
	e.clk.Advance(time.Hour)
	rec = e.do("GET", "/documents/01A/file", c)
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if out.ExpiresAt != "2026-10-16T13:10:00Z" {
		t.Errorf("expiresAt after advance = %q", out.ExpiresAt)
	}
}

func TestDeleteRemovesObjectThenRow(t *testing.T) {
	e := newDocsEnv2(t)
	c, uid := e.signup(t, "ada@example.com")
	d := e.seed(t, uid, "01A", "a.pdf", store.DocReady, authT0)
	rec := e.do("DELETE", "/documents/01A", c)
	if rec.Code != http.StatusNoContent || rec.Body.Len() != 0 {
		t.Fatalf("status %d body %q", rec.Code, rec.Body)
	}
	want := []string{"blob.delete " + d.S3Key, "row.delete 01A"}
	if strings.Join(e.ev.log, "|") != strings.Join(want, "|") {
		t.Errorf("order = %v, want %v", e.ev.log, want)
	}
	if _, err := e.blob.Head(context.Background(), d.S3Key); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("object still there: %v", err)
	}
	if _, err := e.store.GetDoc(context.Background(), uid, "01A"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("row still there: %v", err)
	}
	if rec := e.do("DELETE", "/documents/01A", nil); rec.Code != http.StatusUnauthorized {
		t.Errorf("no cookie: %d", rec.Code)
	}
}

func TestDeleteIdempotent204(t *testing.T) {
	e := newDocsEnv2(t)
	c, uid := e.signup(t, "ada@example.com")
	_, other := e.signup(t, "bob@example.com")
	e.seed(t, uid, "01A", "a.pdf", store.DocReady, authT0)
	pend := e.seed(t, uid, "01P", "p.pdf", store.DocPending, authT0)
	theirs := e.seed(t, other, "01T", "t.pdf", store.DocReady, authT0)

	for i := 0; i < 2; i++ {
		if rec := e.do("DELETE", "/documents/01A", c); rec.Code != http.StatusNoContent {
			t.Fatalf("repeat %d: %d", i, rec.Code)
		}
	}
	if rec := e.do("DELETE", "/documents/nope", c); rec.Code != http.StatusNoContent {
		t.Errorf("missing: %d", rec.Code)
	}
	// A pending document of the caller is deleted (object and row).
	if rec := e.do("DELETE", "/documents/01P", c); rec.Code != http.StatusNoContent {
		t.Errorf("pending: %d", rec.Code)
	}
	if _, err := e.store.GetDoc(context.Background(), uid, "01P"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("pending row survived: %v", err)
	}
	if _, err := e.blob.Head(context.Background(), pend.S3Key); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("pending object survived: %v", err)
	}
	// Another user's document: 204 (indistinguishable from gone) with no effect.
	if rec := e.do("DELETE", "/documents/01T", c); rec.Code != http.StatusNoContent {
		t.Errorf("foreign: %d", rec.Code)
	}
	if _, err := e.store.GetDoc(context.Background(), other, "01T"); err != nil {
		t.Errorf("foreign row deleted: %v", err)
	}
	if _, err := e.blob.Head(context.Background(), theirs.S3Key); err != nil {
		t.Errorf("foreign object deleted: %v", err)
	}
}
