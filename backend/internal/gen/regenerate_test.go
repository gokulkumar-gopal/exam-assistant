package gen

import (
	"context"
	"strings"
	"testing"

	"github.com/gokulkumar/exam-assistant/backend/internal/store"
)

func regenReq(ids ...string) RegenerateRequest {
	return RegenerateRequest{Marks: 2, SourceIDs: ids, Type: "mcq", Avoid: []string{"What is a cell?"}}
}

func TestRegenerateAvoidLimits(t *testing.T) {
	e := newEnv(t)
	e.ready(t, "d1")
	ctx := context.Background()

	r := regenReq("d1")
	r.Avoid = make([]string, 61)
	for i := range r.Avoid {
		r.Avoid[i] = "q"
	}
	ae := appErr(t, regenErr(t, e, r), 422)
	if ae.Fields["avoid"] == "" {
		t.Errorf("61 strings: fields = %v", ae.Fields)
	}

	r.Avoid = []string{strings.Repeat("x", 601)}
	ae = appErr(t, regenErr(t, e, r), 422)
	if ae.Fields["avoid"] == "" {
		t.Errorf("601 chars: fields = %v", ae.Fields)
	}
	// Characters, not bytes: 600 multi-byte characters are accepted, 601 are not.
	r.Avoid = []string{strings.Repeat("é", 600)}
	if _, err := e.svc.Regenerate(ctx, e.user, r); err != nil {
		t.Errorf("600 two-byte chars refused: %v", err)
	}
	e.clk.Advance(1)
	r.Avoid = []string{strings.Repeat("é", 601)}
	appErr(t, regenErr(t, e, r), 422)

	e2 := newEnv(t)
	e2.ready(t, "d1")
	r.Avoid = make([]string, 60)
	for i := range r.Avoid {
		r.Avoid[i] = strings.Repeat("x", 600)
	}
	if _, err := e2.svc.Regenerate(ctx, e2.user, r); err != nil {
		t.Errorf("60 x 600 refused: %v", err)
	}
	// No avoid list at all is fine too.
	r.Avoid = nil
	e2.clk.Advance(1)
	if _, err := e2.svc.Regenerate(ctx, e2.user, r); err != nil {
		t.Errorf("nil avoid refused: %v", err)
	}
}

func regenErr(t *testing.T, e *env, r RegenerateRequest) error {
	t.Helper()
	id, err := e.svc.Regenerate(context.Background(), e.user, r)
	if err == nil {
		t.Fatalf("accepted (job %s), want an error", id)
	}
	return err
}

func TestRegenerateShapeValidation(t *testing.T) {
	e := newEnv(t)
	e.ready(t, "d1")
	for name, mut := range map[string]func(*RegenerateRequest){
		"marks 0":   func(r *RegenerateRequest) { r.Marks = 0 },
		"marks 6":   func(r *RegenerateRequest) { r.Marks = 6 },
		"bad type":  func(r *RegenerateRequest) { r.Type = "essay" },
		"no source": func(r *RegenerateRequest) { r.SourceIDs = nil },
	} {
		r := regenReq("d1")
		mut(&r)
		_, err := e.svc.Regenerate(context.Background(), e.user, r)
		ae := appErr(t, err, 422)
		if len(ae.Fields) == 0 {
			t.Errorf("%s: no field errors", name)
		}
	}
}

// limitsEnv is newEnv with the given quota limits and no running-jobs limit.
func limitsEnv(t *testing.T, q QuotaLimits) *env {
	t.Helper()
	e := newEnv(t)
	l := testLimits()
	l.MaxRunning = 100
	l.Quota = q
	e.svc = NewService(e.jobs, e.docs, e.runner, e.clk, Enabled([]string{ProviderGemini, ProviderAnthropic}), "gemini-3.1-flash-lite", l)
	e.ready(t, "d1")
	return e
}

func TestRegenerateCountsAgainstRegenNotGenerate(t *testing.T) {
	ctx := context.Background()
	// Regenerate uses only the regen counter: 2 regens pass although the generate limit is 1, the 3rd is refused as a regen.
	e := limitsEnv(t, QuotaLimits{MaxUser: 1, MaxRegen: 2, MaxGlobal: 100, BudgetUSD: 15})
	for i := 0; i < 2; i++ {
		if _, err := e.svc.Regenerate(ctx, e.user, regenReq("d1")); err != nil {
			t.Fatalf("regenerate %d: %v", i, err)
		}
	}
	_, err := e.svc.Regenerate(ctx, e.user, regenReq("d1"))
	ae := appErr(t, err, 429)
	if ae.Message != QuotaMessage(store.QuotaRegen) {
		t.Errorf("message = %q, want the regen quota text", ae.Message)
	}
	// The generate counter is untouched: one generate still fits, the next is refused as a generate.
	if _, err := e.svc.Create(ctx, e.user, req("d1")); err != nil {
		t.Fatalf("generate after regens: %v", err)
	}
	_, err = e.svc.Create(ctx, e.user, req("d1"))
	if ae := appErr(t, err, 429); ae.Message != QuotaMessage(store.QuotaUser) {
		t.Errorf("message = %q, want the generate quota text", ae.Message)
	}

	// And the other way round: generating does not use regen quota.
	e = limitsEnv(t, QuotaLimits{MaxUser: 5, MaxRegen: 1, MaxGlobal: 100, BudgetUSD: 15})
	if _, err := e.svc.Create(ctx, e.user, req("d1")); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Regenerate(ctx, e.user, regenReq("d1")); err != nil {
		t.Fatalf("regenerate after a generate: %v", err)
	}
}

func TestRegenerateJobKindAndTotal1(t *testing.T) {
	e := newEnv(t)
	e.ready(t, "d1")
	r := regenReq("d1")
	r.Marks, r.Type, r.Avoid = 4, "free", []string{"a", "b"}
	id, err := e.svc.Regenerate(context.Background(), e.user, r)
	if err != nil {
		t.Fatal(err)
	}
	j, err := e.jobs.GetJob(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if j.Kind != store.KindRegenerate || j.Total != 1 || j.Status != store.JobQueued || j.OwnerID != e.user.ID {
		t.Errorf("job = %+v", j)
	}
	if j.Model != "gemini-3.1-flash-lite" {
		t.Errorf("model = %q, want the default", j.Model)
	}
	rq := j.Request
	if rq.Marks != 4 || rq.Type != "free" || len(rq.SourceIDs) != 1 || rq.SourceIDs[0] != "d1" ||
		len(rq.Avoid) != 2 || rq.Avoid[1] != "b" || len(rq.Counts) != 0 {
		t.Errorf("request = %+v", rq)
	}
	if len(e.runner.started) != 1 || e.runner.started[0] != id {
		t.Errorf("started = %v", e.runner.started)
	}
	v, err := e.svc.Poll(context.Background(), e.user, id)
	if err != nil || v.Progress.Total != 1 {
		t.Errorf("poll = %+v, %v", v, err)
	}
}

func TestRegenerateModelOptionalAndValidated(t *testing.T) {
	e := newEnv(t)
	e.ready(t, "d1")
	ctx := context.Background()
	r := regenReq("d1")
	r.Model = "claude-haiku-4-5"
	id, err := e.svc.Regenerate(ctx, e.user, r)
	if err != nil {
		t.Fatal(err)
	}
	if j, _ := e.jobs.GetJob(ctx, id); j.Model != "claude-haiku-4-5" {
		t.Errorf("model = %q", j.Model)
	}
	r.Model = "gpt-9"
	ae := appErr(t, regenErr(t, e, r), 422)
	if ae.Fields["model"] == "" {
		t.Errorf("fields = %v", ae.Fields)
	}
}

func TestRegenerateSameSourceCaps(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.putDoc(t, "u1", "big", "big.pdf", "PDF", 1000, 41, store.DocReady)
	e.putDoc(t, "u1", "pend", "p.pdf", "PDF", 1000, 2, store.DocPending)
	e.putDoc(t, "u2", "foreign", "f.pdf", "PDF", 1000, 2, store.DocReady)
	e.putDoc(t, "u1", "huge", "h.pdf", "PDF", 21<<20, 2, store.DocReady)
	e.ready(t, "d1")
	for name, ids := range map[string][]string{
		"unknown":        {"nope"},
		"pending":        {"pend"},
		"foreign":        {"foreign"},
		"too many pages": {"big"},
		"duplicate":      {"d1", "d1"},
	} {
		r := regenReq(ids...)
		ae := appErr(t, regenErr(t, e, r), 422)
		if ae.Fields["sourceIds"] == "" {
			t.Errorf("%s: fields = %v", name, ae.Fields)
		}
	}
	ae := appErr(t, regenErr(t, e, regenReq("huge")), 422)
	if ae.Message != TooLargeMessage {
		t.Errorf("total bytes: message = %q", ae.Message)
	}
	many := make([]string, 11)
	for i := range many {
		many[i] = "d" + string(rune('a'+i))
		e.ready(t, many[i])
	}
	ae = appErr(t, regenErr(t, e, regenReq(many...)), 422)
	if ae.Fields["sourceIds"] == "" {
		t.Errorf("11 sources: fields = %v", ae.Fields)
	}
	// A refused request leaves nothing behind.
	if jobs, _ := e.jobs.ListOwnerJobs(ctx, "u1"); len(jobs) != 0 {
		t.Errorf("jobs after refusals = %v", jobs)
	}
}

func TestRegenerateRunnerFailureIs503(t *testing.T) {
	e := newEnv(t)
	e.ready(t, "d1")
	e.runner.err = context.DeadlineExceeded
	_, err := e.svc.Regenerate(context.Background(), e.user, regenReq("d1"))
	ae := appErr(t, err, 503)
	if ae.Message != StartFailedMessage {
		t.Errorf("message = %q", ae.Message)
	}
}

func TestRegenerateProducesOneQuestionAndAvoidIsPassed(t *testing.T) {
	e := newWEnv(t, map[string]int{"1": 1})
	ctx := context.Background()
	avoid := []string{"What is osmosis in plants?", "Define diffusion"}
	job := store.Job{
		ID: "regen1", OwnerID: "u1", Kind: store.KindRegenerate, Model: "claude-haiku-4-5", Day: "20261016",
		Status: store.JobQueued, Total: 1, CreatedAt: t0, UpdatedAt: t0, TTL: t0.Add(store.JobItemTTL).Unix(),
		Request: store.GenerateRequest{SourceIDs: []string{"d1", "d2"}, Type: "free", Marks: 3, Avoid: avoid},
	}
	q := store.QuotaCheck{UserID: "u1", Day: "20261016", Month: "202610", Regen: true, MaxUser: 100, MaxRegen: 100, MaxGlobal: 100, BudgetMicros: 1 << 40}
	if err := e.raw.CreateJobWithQuota(ctx, job, q); err != nil {
		t.Fatal(err)
	}
	e.jobID = "regen1"
	e.run(t)

	j := e.job(t)
	if j.Status != store.JobDone || j.Done != 1 || j.Total != 1 {
		t.Fatalf("job = %+v", j)
	}
	qs, err := e.raw.GetQuestions(ctx, "regen1")
	if err != nil || len(qs) != 1 || qs[0].Marks != 3 || qs[0].ModelAnswer == "" || len(qs[0].Options) != 0 {
		t.Fatalf("questions = %+v, %v", qs, err)
	}
	if e.gen.callCount() != 1 {
		t.Fatalf("generate calls = %d, want 1", e.gen.callCount())
	}
	c := e.gen.calls[0]
	if c.Count != 1 || c.Marks != 3 || c.Type != "free" {
		t.Errorf("call = marks %d count %d type %s", c.Marks, c.Count, c.Type)
	}
	if len(c.Avoid) != 2 || c.Avoid[0] != avoid[0] || c.Avoid[1] != avoid[1] {
		t.Errorf("Avoid = %q, want the request's list", c.Avoid)
	}
}

func TestRegenerateAvoidTextsAreNotRepeated(t *testing.T) {
	e := newWEnv(t, map[string]int{"1": 1})
	ctx := context.Background()
	dup := "Explain photosynthesis in green leaves with sunlight water carbon"
	job := store.Job{
		ID: "regen2", OwnerID: "u1", Kind: store.KindRegenerate, Model: "claude-haiku-4-5", Day: "20261016",
		Status: store.JobQueued, Total: 1, CreatedAt: t0, UpdatedAt: t0, TTL: t0.Add(store.JobItemTTL).Unix(),
		Request: store.GenerateRequest{SourceIDs: []string{"d1"}, Type: "free", Marks: 2, Avoid: []string{dup}},
	}
	q := store.QuotaCheck{UserID: "u1", Day: "20261016", Month: "202610", Regen: true, MaxUser: 100, MaxRegen: 100, MaxGlobal: 100, BudgetMicros: 1 << 40}
	if err := e.raw.CreateJobWithQuota(ctx, job, q); err != nil {
		t.Fatal(err)
	}
	e.jobID = "regen2"
	n := 0
	e.gen.fn = func(_ context.Context, in GenInput) (GenOutput, error) {
		n++
		out := validOut(in)
		if n == 1 { // the model repeats an exam question; validation must drop it and the follow-up fetch a new one
			out.Questions[0].Text = dup
		}
		return out, nil
	}
	e.run(t)
	qs, err := e.raw.GetQuestions(ctx, "regen2")
	if err != nil || len(qs) != 1 || qs[0].Text == dup {
		t.Fatalf("questions = %+v, %v", qs, err)
	}
	if got := e.gen.calls[1].Avoid; !slicesContains(got, dup) {
		t.Errorf("follow-up Avoid = %q, want the exam question kept in it", got)
	}
}

func slicesContains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

func TestRegenerateCarriesPageRanges(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.putDoc(t, "u1", "big", "Book.pdf", "PDF", 50<<20, 300, store.DocReady)
	r := regenReq("big")
	if _, err := e.svc.Regenerate(ctx, e.user, r); err == nil {
		t.Fatal("whole 300-page file accepted")
	}
	r.PageRanges = map[string]PageRange{"big": {From: 41, To: 80}}
	id, err := e.svc.Regenerate(ctx, e.user, r)
	if err != nil {
		t.Fatal(err)
	}
	j, _ := e.jobs.GetJob(ctx, id)
	if j.Request.PageRanges["big"] != (PageRange{From: 41, To: 80}) {
		t.Errorf("stored ranges = %v", j.Request.PageRanges)
	}
	r.PageRanges = map[string]PageRange{"big": {From: 41, To: 301}}
	if ae := appErr(t, regenErr(t, e, r), 422); !strings.Contains(ae.Fields["pageRanges"], "Book.pdf") {
		t.Errorf("fields = %v", ae.Fields)
	}
}
