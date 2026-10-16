package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gokulkumar/exam-assistant/backend/internal/clock"
	"github.com/gokulkumar/exam-assistant/backend/internal/gen"
	"github.com/gokulkumar/exam-assistant/backend/internal/store"
	"github.com/gokulkumar/exam-assistant/backend/internal/store/memory"
)

// withGen adds the generation dependencies (in-memory jobs, the given runner) to deps.
func withGen(deps Deps, clk clock.Clock, ds store.DocStore, runner gen.JobRunner) Deps {
	jobs := memory.NewJobStore(clk)
	lim := gen.Limits{MaxSources: 10, MaxTotalBytes: 20971520, MaxPDFPages: 40, MaxRunning: 2,
		Quota: gen.QuotaLimits{MaxUser: 50, MaxRegen: 10, MaxGlobal: 50, BudgetUSD: 15}}
	deps.Jobs, deps.Runner = jobs, runner
	deps.Gen = gen.NewService(jobs, ds, runner, clk, deps.Models, deps.DefaultModelID, lim)
	return deps
}

type okRunner struct{ started []string }

func (r *okRunner) Start(_ context.Context, id string) error {
	r.started = append(r.started, id)
	return nil
}

type genEnv struct {
	h      http.Handler
	cookie *http.Cookie
	other  *http.Cookie
	uid    string // the first user's ID
	docs   *memory.DocStore
	jobs   store.JobStore
	runner *okRunner
	clk    *clock.Fake
}

func newGenEnv(t *testing.T, runner gen.JobRunner) *genEnv {
	t.Helper()
	deps, clk := aiDeps(t, []string{"gemini", "anthropic"}, "")
	ds := memory.NewDocStore(clk)
	ok, _ := runner.(*okRunner)
	deps = withGen(deps, clk, ds, runner)
	h, err := NewHandler(deps)
	if err != nil {
		t.Fatal(err)
	}
	signup := func(email string) (*http.Cookie, string) {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, jsonReq("POST", "/auth/signup",
			`{"name":"A","email":"`+email+`","password":"correct horse","role":"student"}`))
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
		return c, out.User.ID
	}
	ca, uid := signup("a@example.com")
	cb, _ := signup("b@example.com")
	return &genEnv{h: h, cookie: ca, other: cb, uid: uid, docs: ds, jobs: deps.Jobs, runner: ok, clk: clk}
}

func (e *genEnv) doc(t *testing.T, owner, id string) {
	t.Helper()
	if err := e.docs.PutDoc(context.Background(), store.Document{ID: id, OwnerID: owner, Name: id + ".pdf", Type: "PDF",
		SizeBytes: 10, Pages: 2, Status: store.DocReady, CreatedAt: authT0}); err != nil {
		t.Fatal(err)
	}
}

func (e *genEnv) call(method, path, body string, c *http.Cookie) *httptest.ResponseRecorder {
	req := jsonReq(method, path, body)
	if c != nil {
		req.AddCookie(c)
	}
	if method != "GET" {
		req.Header.Set("Origin", "http://localhost:3000")
	}
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	return rec
}

const genBody = `{"sourceIds":["d1"],"type":"mcq","counts":{"1":4,"2":3},"shuffle":true}`

func TestCreateReturns202AndLocation(t *testing.T) {
	e := newGenEnv(t, &okRunner{})
	e.doc(t, e.uid, "d1")
	rec := e.call("POST", "/generate", genBody, e.cookie)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	var out map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil || len(out) != 1 || len(out["jobId"]) != 26 {
		t.Fatalf("body = %s", rec.Body)
	}
	if loc := rec.Header().Get("Location"); loc != "/generate/"+out["jobId"] {
		t.Errorf("Location = %q", loc)
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "no-store" {
		t.Errorf("Cache-Control = %q", cc)
	}
	if len(e.runner.started) != 1 || e.runner.started[0] != out["jobId"] {
		t.Errorf("started = %v", e.runner.started)
	}
}

func TestGenerateRequiresLogin(t *testing.T) {
	e := newGenEnv(t, &okRunner{})
	for _, c := range [][2]string{{"POST", "/generate"}, {"GET", "/generate/x"}, {"DELETE", "/generate/x"}} {
		if rec := e.call(c[0], c[1], "", nil); rec.Code != http.StatusUnauthorized {
			t.Errorf("%v: status %d", c, rec.Code)
		}
	}
}

func TestCreateValidationAndUnknownFields(t *testing.T) {
	e := newGenEnv(t, &okRunner{})
	e.doc(t, e.uid, "d1")
	for _, body := range []string{
		`{"sourceIds":["d1"],"type":"mcq","counts":{"1":4},"extra":1}`,
		`{"sourceIds":["d1"],"type":"mcq","counts":{"1":"four"}}`,
		`{"sourceIds":["d1"],"type":"mcq","counts":{"6":1}}`,
		`{"sourceIds":["d1"],"type":"mcq","counts":{"1":1},"model":"gpt-9"}`,
	} {
		if rec := e.call("POST", "/generate", body, e.cookie); rec.Code != 422 {
			t.Errorf("%s: status %d: %s", body, rec.Code, rec.Body)
		}
	}
	rec := e.call("POST", "/generate", `{"sourceIds":["d1"],"type":"mcq","counts":{"1":1},"model":"gpt-9"}`, e.cookie)
	var b errBody
	_ = json.Unmarshal(rec.Body.Bytes(), &b)
	if b.Error.Fields["model"] == "" {
		t.Errorf("body = %s", rec.Body)
	}
}

// The stub runner is what cmd/server and cmd/lambda wire until the worker task lands.
func TestStubRunnerYields503AndFailedJob(t *testing.T) {
	e := newGenEnv(t, gen.StubRunner{})
	uid := e.uid
	e.doc(t, uid, "d1")
	rec := e.call("POST", "/generate", genBody, e.cookie)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	var b errBody
	_ = json.Unmarshal(rec.Body.Bytes(), &b)
	if b.Error.Code != "upstream_unavailable" || b.Error.Message != "We couldn't start that. Try again." {
		t.Errorf("body = %s", rec.Body)
	}
	if strings.Contains(rec.Body.String(), "generation not available") {
		t.Error("runner error leaked to the client")
	}
	jobs, _ := e.jobs.ListOwnerJobs(context.Background(), uid)
	if len(jobs) != 1 || jobs[0].Status != store.JobFailed {
		t.Errorf("jobs = %+v", jobs)
	}
}

func TestPollAndCancelHTTP(t *testing.T) {
	e := newGenEnv(t, &okRunner{})
	e.doc(t, e.uid, "d1")
	var out map[string]string
	rec := e.call("POST", "/generate", genBody, e.cookie)
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	path := "/generate/" + out["jobId"]

	e.clk.Advance(time.Minute)
	rec = e.call("GET", path, "", e.cookie)
	if rec.Code != 200 {
		t.Fatalf("poll %d: %s", rec.Code, rec.Body)
	}
	var raw map[string]json.RawMessage
	_ = json.Unmarshal(rec.Body.Bytes(), &raw)
	for _, k := range []string{"status", "model", "progress", "questions", "failedSources", "error", "serverTime"} {
		if _, ok := raw[k]; !ok {
			t.Errorf("missing key %q in %s", k, rec.Body)
		}
	}
	if string(raw["questions"]) != "null" || string(raw["error"]) != "null" || string(raw["failedSources"]) != "[]" ||
		string(raw["progress"]) != `{"done":0,"total":7}` || string(raw["status"]) != `"queued"` ||
		string(raw["model"]) != `"gemini-3.1-flash-lite"` {
		t.Errorf("body = %s", rec.Body)
	}

	// Another user cannot read or cancel it, and cannot tell it from a missing job.
	for _, m := range []string{"GET", "DELETE"} {
		a, b := e.call(m, path, "", e.other), e.call(m, "/generate/missing", "", e.other)
		if a.Code != 404 || a.Body.String() != b.Body.String() {
			t.Errorf("%s foreign %d %s vs missing %d %s", m, a.Code, a.Body, b.Code, b.Body)
		}
	}
	for i := 0; i < 2; i++ {
		if rec := e.call("DELETE", path, "", e.cookie); rec.Code != 204 || rec.Body.Len() != 0 {
			t.Errorf("cancel %d: %d %s", i, rec.Code, rec.Body)
		}
	}
	if rec := e.call("DELETE", "/generate/missing", "", e.cookie); rec.Code != 404 {
		t.Errorf("cancel missing: %d", rec.Code)
	}
	j, _ := e.jobs.GetJob(context.Background(), out["jobId"])
	if !j.CancelRequested {
		t.Error("CancelRequested not set")
	}
}

func TestQuotaAndRunningLimitAre429(t *testing.T) {
	e := newGenEnv(t, &okRunner{})
	e.doc(t, e.uid, "d1")
	for i := 0; i < 2; i++ {
		if rec := e.call("POST", "/generate", genBody, e.cookie); rec.Code != 202 {
			t.Fatalf("create %d: %d %s", i, rec.Code, rec.Body)
		}
		e.clk.Advance(time.Second)
	}
	rec := e.call("POST", "/generate", genBody, e.cookie)
	var b errBody
	_ = json.Unmarshal(rec.Body.Bytes(), &b)
	if rec.Code != 429 || b.Error.Code != "rate_limited" {
		t.Errorf("status %d body %s", rec.Code, rec.Body)
	}
}

func TestNewHandlerRefusesNilGenDeps(t *testing.T) {
	good := func() Deps {
		deps, clk := aiDeps(t, []string{"anthropic"}, "")
		return withGen(deps, clk, memory.NewDocStore(clk), gen.StubRunner{})
	}
	if _, err := NewHandler(good()); err != nil {
		t.Fatalf("complete deps: %v", err)
	}
	for name, mut := range map[string]func(*Deps){
		"Jobs":   func(d *Deps) { d.Jobs = nil },
		"Gen":    func(d *Deps) { d.Gen = nil },
		"Runner": func(d *Deps) { d.Runner = nil },
	} {
		d := good()
		mut(&d)
		if h, err := NewHandler(d); err == nil || h != nil || !strings.Contains(err.Error(), name) {
			t.Errorf("nil %s: h=%v err=%v", name, h, err)
		}
	}
}

func TestGenerateWrongMethodIsJSON405(t *testing.T) {
	e := newGenEnv(t, &okRunner{})
	rec := e.call("PUT", "/generate/abc", "", e.cookie)
	if rec.Code != 405 || rec.Header().Get("Allow") != "DELETE, GET, HEAD" {
		t.Errorf("status %d allow %q body %s", rec.Code, rec.Header().Get("Allow"), rec.Body)
	}
}

const regenBody = `{"marks":2,"sourceIds":["d1"],"type":"mcq","avoid":["What is a cell?"]}`

func TestRegenerateReturns202AndLocation(t *testing.T) {
	e := newGenEnv(t, &okRunner{})
	e.doc(t, e.uid, "d1")
	rec := e.call("POST", "/generate/regenerate", regenBody, e.cookie)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	var out map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil || len(out) != 1 || len(out["jobId"]) != 26 {
		t.Fatalf("body = %s", rec.Body)
	}
	if loc := rec.Header().Get("Location"); loc != "/generate/"+out["jobId"] {
		t.Errorf("Location = %q", loc)
	}
	j, err := e.jobs.GetJob(context.Background(), out["jobId"])
	if err != nil || j.Kind != store.KindRegenerate || j.Total != 1 {
		t.Errorf("job = %+v, %v", j, err)
	}
	// The poll endpoint serves it like any other job (and is not shadowed by the static path).
	poll := e.call("GET", "/generate/"+out["jobId"], "", e.cookie)
	if poll.Code != 200 || !strings.Contains(poll.Body.String(), `"total":1`) {
		t.Errorf("poll %d: %s", poll.Code, poll.Body)
	}
}

func TestRegenerateRequiresLogin(t *testing.T) {
	e := newGenEnv(t, &okRunner{})
	e.doc(t, e.uid, "d1")
	if rec := e.call("POST", "/generate/regenerate", regenBody, nil); rec.Code != http.StatusUnauthorized {
		t.Errorf("status %d: %s", rec.Code, rec.Body)
	}
	if len(e.runner.started) != 0 {
		t.Error("a job was started without a session")
	}
}

func TestRegenerateValidationShape(t *testing.T) {
	e := newGenEnv(t, &okRunner{})
	e.doc(t, e.uid, "d1")
	avoid := func(n, size int) string {
		items := make([]string, n)
		for i := range items {
			items[i] = `"` + strings.Repeat("x", size) + `"`
		}
		return "[" + strings.Join(items, ",") + "]"
	}
	for _, tc := range []struct{ name, body, field string }{
		{"61 avoid", `{"marks":2,"sourceIds":["d1"],"type":"mcq","avoid":` + avoid(61, 1) + `}`, "avoid"},
		{"601 chars", `{"marks":2,"sourceIds":["d1"],"type":"mcq","avoid":` + avoid(1, 601) + `}`, "avoid"},
		{"marks 6", `{"marks":6,"sourceIds":["d1"],"type":"mcq","avoid":[]}`, "marks"},
		{"bad type", `{"marks":2,"sourceIds":["d1"],"type":"x","avoid":[]}`, "type"},
		{"no sources", `{"marks":2,"sourceIds":[],"type":"mcq","avoid":[]}`, "sourceIds"},
		{"unknown model", `{"marks":2,"sourceIds":["d1"],"type":"mcq","avoid":[],"model":"gpt-9"}`, "model"},
		{"foreign doc", `{"marks":2,"sourceIds":["zz"],"type":"mcq","avoid":[]}`, "sourceIds"},
	} {
		rec := e.call("POST", "/generate/regenerate", tc.body, e.cookie)
		var b errBody
		_ = json.Unmarshal(rec.Body.Bytes(), &b)
		if rec.Code != 422 || b.Error.Code != "validation" || b.Error.Fields[tc.field] == "" {
			t.Errorf("%s: status %d body %s", tc.name, rec.Code, rec.Body)
		}
	}
	for _, body := range []string{
		`{"marks":2,"sourceIds":["d1"],"type":"mcq","avoid":[],"extra":1}`,
		`{"marks":"two","sourceIds":["d1"],"type":"mcq"}`,
		`{"marks":2,"sourceIds":["d1"],"type":"mcq","avoid":[1]}`,
	} {
		if rec := e.call("POST", "/generate/regenerate", body, e.cookie); rec.Code != 422 {
			t.Errorf("%s: status %d", body, rec.Code)
		}
	}
	// 60 x 600 is accepted over HTTP too.
	rec := e.call("POST", "/generate/regenerate", `{"marks":2,"sourceIds":["d1"],"type":"mcq","avoid":`+avoid(60, 600)+`}`, e.cookie)
	if rec.Code != 202 {
		t.Errorf("60 x 600: status %d: %s", rec.Code, rec.Body)
	}
}

func TestRegenerateQuotaIs429AndWrongMethodIsJSON405(t *testing.T) {
	e := newGenEnv(t, &okRunner{})
	e.doc(t, e.uid, "d1")
	rec := e.call("PUT", "/generate/regenerate", "", e.cookie)
	if rec.Code != 405 || rec.Header().Get("Allow") != "DELETE, GET, HEAD, POST" ||
		!strings.Contains(rec.Header().Get("Content-Type"), "application/json") {
		t.Errorf("status %d allow %q body %s", rec.Code, rec.Header().Get("Allow"), rec.Body)
	}
	for i := 0; i < 10; i++ {
		if rec := e.call("POST", "/generate/regenerate", regenBody, e.cookie); rec.Code != 202 {
			t.Fatalf("regenerate %d: %d %s", i, rec.Code, rec.Body)
		}
		e.clk.Advance(7 * time.Minute) // past the running-jobs window
	}
	rec = e.call("POST", "/generate/regenerate", regenBody, e.cookie)
	var b errBody
	_ = json.Unmarshal(rec.Body.Bytes(), &b)
	if rec.Code != 429 || b.Error.Code != "rate_limited" || b.Error.Message != gen.QuotaMessage(store.QuotaRegen) {
		t.Errorf("status %d body %s", rec.Code, rec.Body)
	}
}
