package gen

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gokulkumar/exam-assistant/backend/internal/apperr"
	"github.com/gokulkumar/exam-assistant/backend/internal/clock"
	"github.com/gokulkumar/exam-assistant/backend/internal/store"
	"github.com/gokulkumar/exam-assistant/backend/internal/store/memory"
)

var t0 = time.Date(2026, 10, 16, 23, 30, 0, 0, time.UTC)

// countingJobs counts GetQuestions calls.
type countingJobs struct {
	store.JobStore
	mu        sync.Mutex
	qCalls    int
	listCalls int
}

func (c *countingJobs) GetQuestions(ctx context.Context, id string) ([]store.Question, error) {
	c.mu.Lock()
	c.qCalls++
	c.mu.Unlock()
	return c.JobStore.GetQuestions(ctx, id)
}

type fakeRunner struct {
	err     error
	started []string
}

func (f *fakeRunner) Start(_ context.Context, id string) error {
	f.started = append(f.started, id)
	return f.err
}

type env struct {
	svc    *Service
	clk    *clock.Fake
	jobs   *countingJobs
	docs   *memory.DocStore
	runner *fakeRunner
	user   store.User
}

func testLimits() Limits {
	return Limits{
		MaxSources: 10, MaxTotalBytes: 20971520, MaxPDFPages: 40, MaxRunning: 2,
		Quota: QuotaLimits{MaxUser: 100, MaxRegen: 10, MaxGlobal: 100, BudgetUSD: 15},
	}
}

func newEnv(t *testing.T) *env {
	t.Helper()
	clk := clock.NewFake(t0)
	jobs := &countingJobs{JobStore: memory.NewJobStore(clk)}
	docs := memory.NewDocStore(clk)
	runner := &fakeRunner{}
	models := Enabled([]string{ProviderGemini, ProviderAnthropic})
	// Only Gemini's models are enabled in the service below when a test narrows it.
	e := &env{clk: clk, jobs: jobs, docs: docs, runner: runner, user: store.User{ID: "u1"}}
	e.svc = NewService(jobs, docs, runner, clk, models, "gemini-3.1-flash-lite", testLimits())
	return e
}

func (e *env) putDoc(t *testing.T, owner, id, name, typ string, size int64, pages int, status string) {
	t.Helper()
	if err := e.docs.PutDoc(context.Background(), store.Document{
		ID: id, OwnerID: owner, Name: name, Type: typ, SizeBytes: size, Pages: pages, Status: status, CreatedAt: t0,
	}); err != nil {
		t.Fatal(err)
	}
}

func (e *env) ready(t *testing.T, id string) {
	t.Helper()
	e.putDoc(t, "u1", id, id+".pdf", "PDF", 1000, 5, store.DocReady)
}

func req(ids ...string) GenerateRequest {
	return GenerateRequest{SourceIDs: ids, Type: "mcq", Counts: map[string]int{"1": 4, "2": 3}}
}

func appErr(t *testing.T, err error, status int) *apperr.Error {
	t.Helper()
	var ae *apperr.Error
	if !errors.As(err, &ae) || ae.Status != status {
		t.Fatalf("err = %v, want apperr status %d", err, status)
	}
	return ae
}

func TestCountsValidation(t *testing.T) {
	e := newEnv(t)
	e.ready(t, "d1")
	bad := map[string]map[string]int{
		"key 6":        {"6": 1},
		"key 0":        {"0": 1},
		"key non-num":  {"x": 1},
		"key 01":       {"01": 1},
		"key space 1":  {" 1": 1},
		"key plus 1":   {"+1": 1},
		"key 1 space":  {"1 ": 1},
		"key arabic 3": {"\u0663": 1},
		"key empty":    {"": 1},
		"key 1.0":      {"1.0": 1},
		"count 31":     {"1": 31},
		"negative":     {"1": -1},
		"all zero":     {"1": 0, "2": 0},
		"61 total":     {"1": 30, "2": 30, "3": 1},
		"marks 206":    {"5": 30, "4": 14, "1": 0, "2": 0, "3": 0},
		"marks 202":    {"5": 30, "4": 13, "3": 0},
	}
	for name, counts := range bad {
		r := req("d1")
		r.Counts = counts
		_, err := e.svc.Create(context.Background(), e.user, r)
		ae := appErr(t, err, 422)
		if ae.Fields["counts"] == "" {
			t.Errorf("%s: fields = %v", name, ae.Fields)
		}
	}
	// Exactly 201: 30x5 + 12x4 + 1x3 = 150 + 48 + 3.
	r := req("d1")
	r.Counts = map[string]int{"5": 30, "4": 12, "3": 1}
	if _, err := e.svc.Create(context.Background(), e.user, r); err == nil {
		t.Error("marks 201 accepted")
	} else {
		appErr(t, err, 422)
	}
	// 60 questions, marks 5x30 + 2x30 = 210 > 200 would fail; 60 questions with marks <= 200 passes.
	r.Counts = map[string]int{"1": 30, "5": 30} // 60 questions, 180 marks
	if _, err := e.svc.Create(context.Background(), e.user, r); err != nil {
		t.Errorf("60 questions, 180 marks rejected: %v", err)
	}
}

func TestShapeValidation(t *testing.T) {
	e := newEnv(t)
	e.ready(t, "d1")
	for _, r := range map[string]GenerateRequest{
		"bad type":  {SourceIDs: []string{"d1"}, Type: "essay", Counts: map[string]int{"1": 1}},
		"no source": {Type: "mcq", Counts: map[string]int{"1": 1}},
		"no counts": {SourceIDs: []string{"d1"}, Type: "mcq"},
		"dup":       {SourceIDs: []string{"d1", "d1"}, Type: "mcq", Counts: map[string]int{"1": 1}},
	} {
		_, err := e.svc.Create(context.Background(), e.user, r)
		appErr(t, err, 422)
	}
}

func TestSourcesMustBeOwnedAndReady(t *testing.T) {
	e := newEnv(t)
	e.putDoc(t, "other", "theirs", "t.pdf", "PDF", 10, 1, store.DocReady)
	e.putDoc(t, "u1", "pending", "p.pdf", "PDF", 10, 0, store.DocPending)
	e.ready(t, "mine")
	for _, ids := range [][]string{{"theirs"}, {"pending"}, {"missing"}, {"mine", "theirs"}} {
		_, err := e.svc.Create(context.Background(), e.user, req(ids...))
		ae := appErr(t, err, 422)
		if ae.Fields["sourceIds"] == "" {
			t.Errorf("%v: fields = %v", ids, ae.Fields)
		}
	}
	if len(e.runner.started) != 0 {
		t.Error("runner started for an invalid request")
	}
}

func TestSourceCountOver10(t *testing.T) {
	e := newEnv(t)
	var ids []string
	for i := 0; i < 11; i++ {
		id := string(rune('a' + i))
		e.putDoc(t, "u1", id, id+".pdf", "PDF", 1000, 1, store.DocReady)
		ids = append(ids, id)
	}
	_, err := e.svc.Create(context.Background(), e.user, req(ids...))
	appErr(t, err, 422)
	if _, err := e.svc.Create(context.Background(), e.user, req(ids[:10]...)); err != nil {
		t.Errorf("10 sources rejected: %v", err)
	}
}

func TestTotalBytesOver20MB422Generic(t *testing.T) {
	e := newEnv(t)
	e.putDoc(t, "u1", "a", "a.txt", "TXT", 10*1024*1024, 1, store.DocReady)
	e.putDoc(t, "u1", "b", "b.txt", "TXT", 10*1024*1024+1, 1, store.DocReady)
	_, err := e.svc.Create(context.Background(), e.user, req("a", "b"))
	ae := appErr(t, err, 422)
	if ae.Message != "These files are too large to use together. Select fewer or smaller files." || len(ae.Fields) != 0 {
		t.Errorf("err = %+v", ae)
	}
	e.putDoc(t, "u1", "c", "c.txt", "TXT", 10*1024*1024, 1, store.DocReady)
	if _, err := e.svc.Create(context.Background(), e.user, req("a", "c")); err != nil {
		t.Errorf("exactly 20 MB rejected: %v", err)
	}
}

func TestSinglePDFOverPagesCapNamesFile(t *testing.T) {
	e := newEnv(t)
	e.ready(t, "small")
	e.putDoc(t, "u1", "big", "Biology.pdf", "PDF", 1000, 41, store.DocReady)
	_, err := e.svc.Create(context.Background(), e.user, req("small", "big"))
	ae := appErr(t, err, 422)
	if !strings.Contains(ae.Fields["sourceIds"], "Biology.pdf") {
		t.Errorf("fields = %v", ae.Fields)
	}
	e.putDoc(t, "u1", "ok", "Chem.pdf", "PDF", 1000, 40, store.DocReady)
	if _, err := e.svc.Create(context.Background(), e.user, req("ok")); err != nil {
		t.Errorf("40 pages rejected: %v", err)
	}
}

func TestPagesSumOver40(t *testing.T) {
	e := newEnv(t)
	e.putDoc(t, "u1", "a", "a.pdf", "PDF", 1000, 25, store.DocReady)
	e.putDoc(t, "u1", "b", "b.pdf", "PDF", 1000, 16, store.DocReady)
	e.putDoc(t, "u1", "c", "c.pdf", "PDF", 1000, 15, store.DocReady)
	e.putDoc(t, "u1", "t", "t.txt", "TXT", 1000, 99, store.DocReady) // non-PDF pages do not count
	_, err := e.svc.Create(context.Background(), e.user, req("a", "b"))
	ae := appErr(t, err, 422)
	if ae.Message != TooLargeMessage || len(ae.Fields) != 0 {
		t.Errorf("err = %+v", ae)
	}
	if _, err := e.svc.Create(context.Background(), e.user, req("a", "c", "t")); err != nil {
		t.Errorf("40 PDF pages plus a TXT rejected: %v", err)
	}
}

func TestModelDefaultsAndUnknown422(t *testing.T) {
	e := newEnv(t)
	e.ready(t, "d1")
	id, err := e.svc.Create(context.Background(), e.user, req("d1"))
	if err != nil {
		t.Fatal(err)
	}
	if j, _ := e.jobs.GetJob(context.Background(), id); j.Model != "gemini-3.1-flash-lite" {
		t.Errorf("default model = %q", j.Model)
	}
	r := req("d1")
	r.Model = "claude-sonnet-5-5"
	id, err = e.svc.Create(context.Background(), e.user, r)
	if err != nil {
		t.Fatal(err)
	}
	if j, _ := e.jobs.GetJob(context.Background(), id); j.Model != "claude-sonnet-5-5" {
		t.Errorf("model = %q", j.Model)
	}
	r.Model = "gpt-9"
	_, err = e.svc.Create(context.Background(), e.user, r)
	if ae := appErr(t, err, 422); ae.Fields["model"] == "" {
		t.Errorf("fields = %v", ae.Fields)
	}
	// In the catalog, but its provider is not enabled.
	gemOnly := NewService(e.jobs, e.docs, e.runner, e.clk, Enabled([]string{ProviderGemini}), "gemini-3.1-flash-lite", testLimits())
	r.Model = "claude-sonnet-5-5"
	_, err = gemOnly.Create(context.Background(), e.user, r)
	if ae := appErr(t, err, 422); ae.Fields["model"] == "" {
		t.Errorf("provider-disabled: fields = %v", ae.Fields)
	}
}

func TestValidationOrderDocumentsBeforeModel(t *testing.T) {
	e := newEnv(t)
	r := req("missing")
	r.Model = "gpt-9"
	_, err := e.svc.Create(context.Background(), e.user, r)
	ae := appErr(t, err, 422)
	if ae.Fields["sourceIds"] == "" || ae.Fields["model"] != "" {
		t.Errorf("fields = %v: documents must be checked before the model", ae.Fields)
	}
}

func TestCreateWritesQueuedJobFromOneNow(t *testing.T) {
	e := newEnv(t) // late evening UTC: the day key must come from the same now as everything else
	e.ready(t, "d1")
	id, err := e.svc.Create(context.Background(), e.user, req("d1"))
	if err != nil {
		t.Fatal(err)
	}
	j, err := e.jobs.GetJob(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if j.Status != store.JobQueued || j.Kind != store.KindGenerate || j.OwnerID != "u1" || j.Total != 7 || j.Done != 0 {
		t.Errorf("job = %+v", j)
	}
	if j.Day != "20261016" || !j.CreatedAt.Equal(t0) || !j.UpdatedAt.Equal(t0) {
		t.Errorf("day/times = %s %v %v", j.Day, j.CreatedAt, j.UpdatedAt)
	}
	if j.TTL != t0.Add(24*time.Hour).Unix() {
		t.Errorf("ttl = %d, want now+24h", j.TTL)
	}
	if len(e.runner.started) != 1 || e.runner.started[0] != id {
		t.Errorf("started = %v", e.runner.started)
	}
}

func TestQuotaRefusalIs429AndLeavesNoJob(t *testing.T) {
	e := newEnv(t)
	e.ready(t, "d1")
	lim := testLimits()
	lim.Quota.MaxUser = 1
	e.svc = NewService(e.jobs, e.docs, e.runner, e.clk, e.svc.models, e.svc.defModel, lim)
	if _, err := e.svc.Create(context.Background(), e.user, req("d1")); err != nil {
		t.Fatal(err)
	}
	e.clk.Advance(7 * time.Minute) // first job is stale: only the daily limit refuses
	_, err := e.svc.Create(context.Background(), e.user, req("d1"))
	ae := appErr(t, err, 429)
	if ae.Code != "rate_limited" || ae.Message != QuotaMessage(store.QuotaUser) {
		t.Errorf("err = %+v", ae)
	}
	if len(e.runner.started) != 1 {
		t.Errorf("started = %v", e.runner.started)
	}
}

func TestBudgetRefusalMessage(t *testing.T) {
	e := newEnv(t)
	e.ready(t, "d1")
	if _, err := e.jobs.AddSpend(context.Background(), MonthKey(t0), 15_000_000); err != nil {
		t.Fatal(err)
	}
	_, err := e.svc.Create(context.Background(), e.user, req("d1"))
	if ae := appErr(t, err, 429); ae.Message != BudgetMessage {
		t.Errorf("message = %q", ae.Message)
	}
}

func TestRunningLimitSoft2(t *testing.T) {
	e := newEnv(t)
	e.ready(t, "d1")
	ctx := context.Background()
	for i := 0; i < 2; i++ {
		if _, err := e.svc.Create(ctx, e.user, req("d1")); err != nil {
			t.Fatal(err)
		}
		e.clk.Advance(time.Second) // distinct ULID timestamps
	}
	_, err := e.svc.Create(ctx, e.user, req("d1"))
	if ae := appErr(t, err, 429); ae.Code != "rate_limited" || ae.Message != RunningMessage {
		t.Errorf("err = %+v", ae)
	}
	// Another user is unaffected.
	other := store.User{ID: "u2"}
	e.putDoc(t, "u2", "x", "x.pdf", "PDF", 10, 1, store.DocReady)
	if _, err := e.svc.Create(ctx, other, req("x")); err != nil {
		t.Errorf("other user blocked: %v", err)
	}
	// Both jobs were last written at t0 and t0+1s. At t0+6m exactly the first is not stale.
	e.clk.Set(t0.Add(6 * time.Minute))
	if _, err := e.svc.Create(ctx, e.user, req("d1")); err == nil {
		t.Error("a job exactly 6m old counted as stale")
	}
	// At t0+6m01s the first job is stale; the second (t0+1s) is 6m00s old and still counts.
	e.clk.Set(t0.Add(6*time.Minute + time.Second + time.Second))
	// Now both are stale (6m02s and 6m01s): the third is allowed.
	if _, err := e.svc.Create(ctx, e.user, req("d1")); err != nil {
		t.Errorf("stale jobs still counted: %v", err)
	}
}

func TestRunningLimitIgnoresFinishedJobs(t *testing.T) {
	e := newEnv(t)
	e.ready(t, "d1")
	ctx := context.Background()
	a, _ := e.svc.Create(ctx, e.user, req("d1"))
	e.clk.Advance(time.Second)
	b, _ := e.svc.Create(ctx, e.user, req("d1"))
	for _, id := range []string{a, b} {
		if err := e.jobs.MarkFailedIfQueuedOrRunning(ctx, id, "x"); err != nil {
			t.Fatal(err)
		}
	}
	e.clk.Advance(time.Second)
	if _, err := e.svc.Create(ctx, e.user, req("d1")); err != nil {
		t.Errorf("failed jobs counted as running: %v", err)
	}
}

func TestCreateReturnsIDAndRunnerGetsIt(t *testing.T) {
	e := newEnv(t)
	e.ready(t, "d1")
	id, err := e.svc.Create(context.Background(), e.user, req("d1"))
	if err != nil || len(id) != 26 {
		t.Fatalf("id %q err %v", id, err)
	}
}

func TestStartFailureMarksJobFailedAndReturns503(t *testing.T) {
	e := newEnv(t)
	e.ready(t, "d1")
	e.runner.err = errors.New("invoke refused")
	_, err := e.svc.Create(context.Background(), e.user, req("d1"))
	ae := appErr(t, err, 503)
	if ae.Code != "upstream_unavailable" || ae.Message != "We couldn't start that. Try again." {
		t.Errorf("err = %+v", ae)
	}
	jobs, _ := e.jobs.ListOwnerJobs(context.Background(), "u1")
	if len(jobs) != 1 {
		t.Fatalf("jobs = %v", jobs)
	}
	j, _ := e.jobs.GetJob(context.Background(), jobs[0].ID)
	if j.Status != store.JobFailed || j.Error != "We couldn't start that. Try again." {
		t.Errorf("job = %+v", j)
	}
}

func TestStubRunnerFails(t *testing.T) {
	if err := (StubRunner{}).Start(context.Background(), "x"); err == nil || err.Error() != "generation not available" {
		t.Errorf("err = %v", err)
	}
}

func seed(t *testing.T, e *env, id, owner, status string, updated time.Time) {
	t.Helper()
	j := store.Job{ID: id, OwnerID: owner, Kind: store.KindGenerate, Model: "gemini-3.1-flash-lite", Day: DayKey(t0),
		Status: status, Total: 12, Done: 3, CreatedAt: t0, UpdatedAt: updated, TTL: t0.Add(24 * time.Hour).Unix()}
	if err := e.jobs.CreateJobWithQuota(context.Background(), j, testLimits().Quota.Check(owner, t0, false)); err != nil {
		t.Fatal(err)
	}
}

func TestPollOwnerOnly404(t *testing.T) {
	e := newEnv(t)
	seed(t, e, "j1", "u1", store.JobRunning, t0)
	if _, err := e.svc.Poll(context.Background(), e.user, "j1"); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"j1", "nope"} {
		_, err := e.svc.Poll(context.Background(), store.User{ID: "u2"}, id)
		ae := appErr(t, err, 404)
		if ae.Code != "not_found" {
			t.Errorf("code = %q", ae.Code)
		}
	}
}

func TestPollDoneReadsQuestionsItemOnlyWhenDone(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	seed(t, e, "run", "u1", store.JobRunning, t0)
	seed(t, e, "bad", "u1", store.JobFailed, t0)
	seed(t, e, "ok", "u1", store.JobDone, t0)
	for _, id := range []string{"run", "bad"} {
		v, err := e.svc.Poll(ctx, e.user, id)
		if err != nil || v.Questions != nil {
			t.Fatalf("%s: %v %+v", id, err, v)
		}
	}
	if e.jobs.qCalls != 0 {
		t.Fatalf("GetQuestions called %d times for running/failed jobs", e.jobs.qCalls)
	}
	qs := []store.Question{{ID: "q1", Marks: 2, Text: "t", SourceDocID: "d", SourceDocName: "n"}}
	if err := e.jobs.PutQuestions(ctx, "ok", qs); err != nil {
		t.Fatal(err)
	}
	v, err := e.svc.Poll(ctx, e.user, "ok")
	if err != nil || len(v.Questions) != 1 || v.Questions[0].ID != "q1" || e.jobs.qCalls != 1 {
		t.Fatalf("v=%+v err=%v calls=%d", v, err, e.jobs.qCalls)
	}
}

func TestPollDoneWithoutQuestionsIsInternal(t *testing.T) {
	e := newEnv(t)
	seed(t, e, "ok", "u1", store.JobDone, t0)
	_, err := e.svc.Poll(context.Background(), e.user, "ok")
	appErr(t, err, 500)
}

func TestPollStaleReportsFailedTooLong(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	seed(t, e, "q", "u1", store.JobQueued, t0)
	seed(t, e, "r", "u1", store.JobRunning, t0)
	e.clk.Set(t0.Add(6 * time.Minute))
	if v, _ := e.svc.Poll(ctx, e.user, "r"); v.Status != store.JobRunning || v.Error != nil {
		t.Errorf("6m00s must not be stale: %+v", v)
	}
	e.clk.Set(t0.Add(6*time.Minute + time.Second))
	for _, id := range []string{"q", "r"} {
		v, err := e.svc.Poll(ctx, e.user, id)
		if err != nil || v.Status != "failed" || v.Error == nil || *v.Error != "That took too long. Try again." || v.Questions != nil {
			t.Errorf("%s: %+v %v", id, v, err)
		}
	}
	// Read only: the stored job is untouched.
	if j, _ := e.jobs.GetJob(ctx, "r"); j.Status != store.JobRunning || j.Error != "" {
		t.Errorf("poll wrote to the job: %+v", j)
	}
}

func TestPollIncludesModelAndProgress(t *testing.T) {
	e := newEnv(t)
	seed(t, e, "j", "u1", store.JobRunning, t0)
	e.clk.Advance(time.Minute)
	v, err := e.svc.Poll(context.Background(), e.user, "j")
	if err != nil {
		t.Fatal(err)
	}
	if v.Model != "gemini-3.1-flash-lite" || v.Progress != (Progress{Done: 3, Total: 12}) || v.Status != "running" {
		t.Errorf("v = %+v", v)
	}
	if v.ServerTime != t0.Add(time.Minute).Format(time.RFC3339) || v.FailedSources == nil || v.Error != nil {
		t.Errorf("v = %+v", v)
	}
}

func TestPollFailedShowsStoredError(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	seed(t, e, "j", "u1", store.JobQueued, t0)
	if err := e.jobs.MarkFailedIfQueuedOrRunning(ctx, "j", "We couldn't start that. Try again."); err != nil {
		t.Fatal(err)
	}
	v, _ := e.svc.Poll(ctx, e.user, "j")
	if v.Status != "failed" || v.Error == nil || *v.Error != "We couldn't start that. Try again." {
		t.Errorf("v = %+v", v)
	}
}

func TestCancelSetsFlagIdempotent204(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	seed(t, e, "j", "u1", store.JobRunning, t0)
	for i := 0; i < 2; i++ {
		if err := e.svc.Cancel(ctx, e.user, "j"); err != nil {
			t.Fatal(err)
		}
	}
	if j, _ := e.jobs.GetJob(ctx, "j"); !j.CancelRequested {
		t.Error("flag not set")
	}
	appErr(t, e.svc.Cancel(ctx, store.User{ID: "u2"}, "j"), 404)
	appErr(t, e.svc.Cancel(ctx, e.user, "missing"), 404)
}
