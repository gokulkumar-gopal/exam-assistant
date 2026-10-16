package gen

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/oklog/ulid/v2"

	"github.com/gokulkumar/exam-assistant/backend/internal/clock"
	"github.com/gokulkumar/exam-assistant/backend/internal/docs"
	"github.com/gokulkumar/exam-assistant/backend/internal/store"
	"github.com/gokulkumar/exam-assistant/backend/internal/store/memory"
)

// spyJobs wraps a JobStore to record heartbeats, spend and finishes and to inject errors.
type spyJobs struct {
	store.JobStore

	mu            sync.Mutex
	heartbeats    int
	hbInFlight    int32
	maxInFlight   int32
	preconditions int
	finishes      []string
	finished      bool
	hbAfterFinish int
	spend         []int64
	spendMonths   []string
	progress      []store.Progress

	putQuestionsErr error
	addSpendErr     error
	events          []string // spend, questions, finish:<status> in call order
	afterPut        func()   // runs after a successful PutQuestions
	spendErr        error    // returned by SpendThisMonth
}

func (s *spyJobs) event(e string) {
	s.mu.Lock()
	s.events = append(s.events, e)
	s.mu.Unlock()
}

func (s *spyJobs) Heartbeat(ctx context.Context, id string, now time.Time, p store.Progress) error {
	n := atomic.AddInt32(&s.hbInFlight, 1)
	defer atomic.AddInt32(&s.hbInFlight, -1)
	s.mu.Lock()
	if n > s.maxInFlight {
		s.maxInFlight = n
	}
	s.heartbeats++
	if s.finished {
		s.hbAfterFinish++
	}
	s.progress = append(s.progress, p)
	s.mu.Unlock()
	time.Sleep(time.Millisecond) // widen the window in which a second writer would overlap
	err := s.JobStore.Heartbeat(ctx, id, now, p)
	if errors.Is(err, store.ErrPrecondition) {
		s.mu.Lock()
		s.preconditions++
		s.mu.Unlock()
	}
	return err
}

func (s *spyJobs) FinishJob(ctx context.Context, id, to string, upd store.JobFinish) (bool, error) {
	if atomic.LoadInt32(&s.hbInFlight) != 0 {
		panic("FinishJob called while a heartbeat is in flight")
	}
	s.mu.Lock()
	s.finishes = append(s.finishes, to)
	s.finished = true
	s.mu.Unlock()
	ev := "finish:" + to
	if to == store.JobDone {
		if _, err := s.JobStore.GetQuestions(ctx, id); err != nil {
			ev = "finish:done-without-questions"
		}
	}
	s.event(ev)
	return s.JobStore.FinishJob(ctx, id, to, upd)
}

func (s *spyJobs) PutQuestions(ctx context.Context, id string, qs []store.Question) error {
	if s.putQuestionsErr != nil {
		return s.putQuestionsErr
	}
	if err := s.JobStore.PutQuestions(ctx, id, qs); err != nil {
		return err
	}
	s.event("questions")
	if s.afterPut != nil {
		s.afterPut()
	}
	return nil
}

func (s *spyJobs) AddSpend(ctx context.Context, month string, micros int64) (int64, error) {
	s.mu.Lock()
	s.spend = append(s.spend, micros)
	s.spendMonths = append(s.spendMonths, month)
	s.events = append(s.events, "spend")
	s.mu.Unlock()
	if s.addSpendErr != nil {
		return 0, s.addSpendErr
	}
	return s.JobStore.AddSpend(ctx, month, micros)
}

func (s *spyJobs) SpendThisMonth(ctx context.Context, month string) (int64, error) {
	if s.spendErr != nil {
		return 0, s.spendErr
	}
	return s.JobStore.SpendThisMonth(ctx, month)
}

func (s *spyJobs) snapshot() (hb int, finishes []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.heartbeats, append([]string(nil), s.finishes...)
}

// testGen is a configurable Generator.
type testGen struct {
	mu    sync.Mutex
	calls []GenInput
	fn    func(ctx context.Context, in GenInput) (GenOutput, error)
}

func (g *testGen) Generate(ctx context.Context, in GenInput) (GenOutput, error) {
	g.mu.Lock()
	g.calls = append(g.calls, in)
	fn := g.fn
	g.mu.Unlock()
	if fn != nil {
		return fn(ctx, in)
	}
	return defaultOut(in), nil
}

func (g *testGen) callCount() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return len(g.calls)
}

func defaultOut(in GenInput) GenOutput {
	var out GenOutput
	for i := 0; i < in.Count; i++ {
		q := RawQuestion{Marks: in.Marks, Text: fmt.Sprintf("q%d-%d-%d", in.Marks, len(in.Avoid), i), Source: "doc-1"}
		if in.Type == "mcq" {
			zero := 0
			q.Options, q.CorrectIndex = []string{"a", "b", "c", "d"}, &zero
		} else {
			q.ModelAnswer = "answer"
		}
		out.Questions = append(out.Questions, q)
	}
	out.Usage = Usage{Input: 1000, Output: 100}
	return out
}

type wEnv struct {
	w     *Worker
	clk   *clock.Fake
	jobs  *spyJobs
	raw   *memory.JobStore
	docs  *memory.DocStore
	blob  *docs.MemBlob
	gen   *testGen
	jobID string
}

func newWEnv(t *testing.T, counts map[string]int) *wEnv { return newWEnvShuffle(t, counts, false) }

func newWEnvShuffle(t *testing.T, counts map[string]int, shuffle bool) *wEnv {
	t.Helper()
	clk := clock.NewFake(t0)
	raw := memory.NewJobStore(clk)
	ds := memory.NewDocStore(clk)
	blob := docs.NewMemBlob()
	g := &testGen{}
	for i, name := range []string{"notes.txt", "scan.png"} {
		id := fmt.Sprintf("d%d", i+1)
		key := "users/u1/" + id + "/" + name
		blob.Put(key, []byte("content of "+name))
		if err := ds.PutDoc(context.Background(), store.Document{
			ID: id, OwnerID: "u1", Name: name, Type: "TXT", ContentType: "text/plain", SizeBytes: 10,
			S3Key: key, Status: store.DocReady, CreatedAt: t0,
		}); err != nil {
			t.Fatal(err)
		}
	}
	total := 0
	for _, n := range counts {
		total += n
	}
	job := store.Job{
		ID: "job1", OwnerID: "u1", Kind: store.KindGenerate, Model: "claude-haiku-4-5", Day: "20261016",
		Status: store.JobQueued, Total: total, CreatedAt: t0, UpdatedAt: t0, TTL: t0.Add(store.JobItemTTL).Unix(),
		Request: store.GenerateRequest{SourceIDs: []string{"d1", "d2"}, Type: "mcq", Counts: counts, Shuffle: shuffle},
	}
	q := store.QuotaCheck{UserID: "u1", Day: "20261016", Month: "202610", MaxUser: 100, MaxRegen: 100, MaxGlobal: 100, BudgetMicros: 1 << 40}
	if err := raw.CreateJobWithQuota(context.Background(), job, q); err != nil {
		t.Fatal(err)
	}
	spy := &spyJobs{JobStore: raw}
	e := &wEnv{clk: clk, jobs: spy, raw: raw, docs: ds, blob: blob, gen: g, jobID: "job1"}
	e.w = &Worker{
		Jobs: spy, Docs: ds, Blob: blob, Gens: FixedFactory(g), Clock: clk,
		Cfg:               WorkerConfig{BudgetMicros: 1 << 40, PromptVersion: "pv-test"},
		heartbeatInterval: time.Hour, jobDeadline: 10 * time.Second,
	}
	return e
}

func (e *wEnv) job(t *testing.T) store.Job {
	t.Helper()
	j, err := e.raw.GetJob(context.Background(), e.jobID)
	if err != nil {
		t.Fatal(err)
	}
	return j
}

func (e *wEnv) run(t *testing.T) {
	t.Helper()
	if err := e.w.Run(context.Background(), e.jobID); err != nil {
		t.Fatalf("Run: %v", err)
	}
}

// waitFor polls cond for up to two seconds of real time.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

func TestClaimOnlyWhenQueued(t *testing.T) {
	e := newWEnv(t, map[string]int{"1": 2})
	e.run(t)
	if e.gen.callCount() != 1 || e.job(t).Status != store.JobDone {
		t.Fatalf("first run: calls=%d status=%s", e.gen.callCount(), e.job(t).Status)
	}
	e.run(t) // duplicate delivery
	if e.gen.callCount() != 1 {
		t.Errorf("second Run called the generator again (%d calls)", e.gen.callCount())
	}
}

func TestDuplicateDeliveryAfterDoneExits(t *testing.T) {
	for _, to := range []string{store.JobDone, store.JobFailed, store.JobCancelled} {
		t.Run(to, func(t *testing.T) {
			e := newWEnv(t, map[string]int{"1": 2})
			ctx := context.Background()
			if ok, err := e.raw.ClaimJob(ctx, e.jobID, t0); !ok || err != nil {
				t.Fatal(ok, err)
			}
			if ok, err := e.raw.FinishJob(ctx, e.jobID, to, store.JobFinish{Now: t0, Error: "x"}); !ok || err != nil {
				t.Fatal(ok, err)
			}
			before := e.job(t)
			e.run(t)
			if e.gen.callCount() != 0 {
				t.Error("generator called for a finished job")
			}
			after := e.job(t)
			if after.Status != to || after.Error != before.Error || !after.UpdatedAt.Equal(before.UpdatedAt) {
				t.Errorf("job changed: %+v -> %+v", before, after)
			}
			if hb, fin := e.jobs.snapshot(); hb != 0 || len(fin) != 0 {
				t.Errorf("worker wrote: heartbeats=%d finishes=%v", hb, fin)
			}
		})
	}
}

func TestDeadlineFailsJobWithTooLongMessage(t *testing.T) {
	e := newWEnv(t, map[string]int{"1": 2})
	e.w.jobDeadline = 30 * time.Millisecond
	e.gen.fn = func(ctx context.Context, in GenInput) (GenOutput, error) {
		<-ctx.Done()
		return GenOutput{Usage: Usage{Input: 500}}, ctx.Err()
	}
	e.run(t)
	j := e.job(t)
	if j.Status != store.JobFailed || j.Error != "That took too long. Try again." {
		t.Fatalf("job = %s %q", j.Status, j.Error)
	}
	if _, err := e.raw.GetQuestions(context.Background(), e.jobID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("questions written for a failed job: %v", err)
	}
}

func TestHeartbeatWritesUpdatedAt(t *testing.T) {
	e := newWEnv(t, map[string]int{"1": 2, "2": 1})
	e.w.heartbeatInterval = 5 * time.Millisecond
	release := make(chan struct{})
	e.gen.fn = func(ctx context.Context, in GenInput) (GenOutput, error) {
		select {
		case <-release:
		case <-ctx.Done():
		}
		return defaultOut(in), nil
	}
	done := make(chan struct{})
	go func() { defer close(done); e.run(t) }()

	waitFor(t, "job running", func() bool { return e.job(t).Status == store.JobRunning })
	later := t0.Add(3 * time.Minute)
	e.clk.Set(later)
	waitFor(t, "heartbeat to advance UpdatedAt", func() bool { return e.job(t).UpdatedAt.Equal(later) })
	if j := e.job(t); j.Total != 3 {
		t.Errorf("heartbeat wrote Total=%d, want the 3 questions requested", j.Total)
	}
	close(release)
	<-done
	if e.job(t).Status != store.JobDone {
		t.Errorf("status = %s", e.job(t).Status)
	}
}

func TestHeartbeatStopsAndDoesNotTouchStatusAfterFinish(t *testing.T) {
	e := newWEnv(t, map[string]int{"1": 2})
	e.w.heartbeatInterval = 2 * time.Millisecond
	e.gen.fn = func(ctx context.Context, in GenInput) (GenOutput, error) {
		time.Sleep(15 * time.Millisecond)
		return defaultOut(in), nil
	}
	e.run(t)
	hb1, _ := e.jobs.snapshot()
	before := e.job(t)
	e.clk.Advance(time.Hour)
	time.Sleep(30 * time.Millisecond)
	hb2, _ := e.jobs.snapshot()
	after := e.job(t)
	if hb1 != hb2 {
		t.Errorf("heartbeats continued after Run returned: %d -> %d", hb1, hb2)
	}
	if e.jobs.hbAfterFinish != 0 {
		t.Errorf("%d heartbeat(s) after FinishJob", e.jobs.hbAfterFinish)
	}
	if after.Status != store.JobDone || !after.UpdatedAt.Equal(before.UpdatedAt) {
		t.Errorf("job touched after finish: %+v -> %+v", before, after)
	}
}

func TestLateHeartbeatWritesNothingOnFinishedJob(t *testing.T) {
	for _, to := range []string{store.JobDone, store.JobFailed, store.JobCancelled} {
		t.Run(to, func(t *testing.T) {
			e := newWEnv(t, map[string]int{"1": 2})
			ctx := context.Background()
			if ok, _ := e.raw.ClaimJob(ctx, e.jobID, t0); !ok {
				t.Fatal("claim")
			}
			if ok, err := e.raw.FinishJob(ctx, e.jobID, to, store.JobFinish{Now: t0, Done: 1}); !ok || err != nil {
				t.Fatal(ok, err)
			}
			before := e.job(t)
			err := e.raw.Heartbeat(ctx, e.jobID, t0.Add(time.Hour), store.Progress{Done: 2, Total: 9})
			if !errors.Is(err, store.ErrPrecondition) {
				t.Fatalf("err = %v", err)
			}
			if after := e.job(t); after.Status != to || after.Done != before.Done || after.Total != before.Total || !after.UpdatedAt.Equal(before.UpdatedAt) {
				t.Errorf("late heartbeat wrote: %+v -> %+v", before, after)
			}
		})
	}
}

func TestLateWorkerCannotResurrectStaleFailedJob(t *testing.T) {
	e := newWEnv(t, map[string]int{"1": 2})
	if err := e.raw.MarkFailedIfQueuedOrRunning(context.Background(), e.jobID, StaleMessage); err != nil {
		t.Fatal(err)
	}
	e.run(t)
	if e.gen.callCount() != 0 {
		t.Error("generator called for a failed job")
	}
	if j := e.job(t); j.Status != store.JobFailed || j.Error != StaleMessage {
		t.Errorf("job = %s %q", j.Status, j.Error)
	}
}

func TestStaleJobCannotLaterBecomeDone(t *testing.T) {
	e := newWEnv(t, map[string]int{"1": 2})
	e.gen.fn = func(ctx context.Context, in GenInput) (GenOutput, error) {
		// The job is persisted failed while the worker is mid-generation.
		if err := e.raw.MarkFailedIfQueuedOrRunning(context.Background(), e.jobID, StaleMessage); err != nil {
			t.Error(err)
		}
		return defaultOut(in), nil
	}
	e.run(t)
	j := e.job(t)
	if j.Status != store.JobFailed || j.Error != StaleMessage {
		t.Fatalf("job = %s %q, want failed with the stale message", j.Status, j.Error)
	}
}

func TestFinishRefusedBecauseCancelRequestedFinishesCancelled(t *testing.T) {
	e := newWEnv(t, map[string]int{"1": 2}) // heartbeat interval 1 h: only the final checks can notice
	e.gen.fn = func(ctx context.Context, in GenInput) (GenOutput, error) {
		if err := e.raw.RequestCancel(context.Background(), "u1", e.jobID); err != nil {
			t.Error(err)
		}
		return defaultOut(in), nil
	}
	e.run(t)
	if j := e.job(t); j.Status != store.JobCancelled {
		t.Fatalf("status = %s, want cancelled", j.Status)
	}
	// B-2.7: Run looks at the job before writing the questions item, so a cancel that arrived
	// during the last call finishes cancelled without a done attempt. The late cancel (after
	// that look) is TestDoneOnlyIfRunningAndNotCancelRequested.
	_, fin := e.jobs.snapshot()
	if len(fin) != 1 || fin[0] != store.JobCancelled {
		t.Errorf("finish calls = %v, want [cancelled]", fin)
	}
	e.noQuestionsItem(t)
}

func (e *wEnv) noQuestionsItem(t *testing.T) {
	t.Helper()
	if _, err := e.raw.GetQuestions(context.Background(), e.jobID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("questions item written for a cancelled job: %v", err)
	}
}

func TestCancelRequestedMidRunStopsGeneration(t *testing.T) {
	e := newWEnv(t, map[string]int{"1": 2})
	e.w.heartbeatInterval = 5 * time.Millisecond
	started := make(chan struct{})
	var ctxCancelled atomic.Bool
	e.gen.fn = func(ctx context.Context, in GenInput) (GenOutput, error) {
		close(started)
		<-ctx.Done()
		ctxCancelled.Store(true)
		return GenOutput{}, ctx.Err()
	}
	done := make(chan struct{})
	go func() { defer close(done); e.run(t) }()
	<-started
	if err := e.raw.RequestCancel(context.Background(), "u1", e.jobID); err != nil {
		t.Fatal(err)
	}
	<-done
	if !ctxCancelled.Load() {
		t.Error("generator context was not cancelled")
	}
	if j := e.job(t); j.Status != store.JobCancelled {
		t.Errorf("status = %s, want cancelled", j.Status)
	}
}

func TestCancelRequestedBetweenCallsStops(t *testing.T) {
	e := newWEnv(t, map[string]int{"1": 1, "2": 1, "3": 1}) // heartbeat never fires
	e.gen.fn = func(ctx context.Context, in GenInput) (GenOutput, error) {
		_ = e.raw.RequestCancel(context.Background(), "u1", e.jobID)
		return defaultOut(in), nil
	}
	e.run(t)
	if n := e.gen.callCount(); n != 1 {
		t.Errorf("calls = %d, want 1 (cancel seen between calls)", n)
	}
	if j := e.job(t); j.Status != store.JobCancelled {
		t.Errorf("status = %s", j.Status)
	}
}

func TestHeartbeatErrPreconditionStopsRun(t *testing.T) {
	e := newWEnv(t, map[string]int{"1": 2})
	e.w.heartbeatInterval = 3 * time.Millisecond
	started := make(chan struct{})
	var ctxCancelled atomic.Bool
	e.gen.fn = func(ctx context.Context, in GenInput) (GenOutput, error) {
		close(started)
		<-ctx.Done()
		ctxCancelled.Store(true)
		return GenOutput{}, ctx.Err()
	}
	done := make(chan struct{})
	go func() { defer close(done); e.run(t) }()
	<-started
	if err := e.raw.MarkFailedIfQueuedOrRunning(context.Background(), e.jobID, StaleMessage); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not stop after the heartbeat was refused")
	}
	if !ctxCancelled.Load() {
		t.Error("generator context was not cancelled")
	}
	time.Sleep(20 * time.Millisecond)
	e.jobs.mu.Lock()
	pre := e.jobs.preconditions
	e.jobs.mu.Unlock()
	if pre != 1 {
		t.Errorf("heartbeat kept going after ErrPrecondition: %d refusals", pre)
	}
	if _, fin := e.jobs.snapshot(); len(fin) != 0 {
		t.Errorf("FinishJob called for a job that is no longer running: %v", fin)
	}
	if j := e.job(t); j.Status != store.JobFailed || j.Error != StaleMessage {
		t.Errorf("job = %s %q", j.Status, j.Error)
	}
}

func TestHeartbeatSingleWriterNoRace(t *testing.T) {
	e := newWEnv(t, map[string]int{"1": 20, "2": 2, "3": 2})
	e.w.heartbeatInterval = time.Millisecond
	e.w.Cfg.MaxPerCall = 5
	e.gen.fn = func(ctx context.Context, in GenInput) (GenOutput, error) {
		time.Sleep(8 * time.Millisecond)
		return defaultOut(in), nil
	}
	e.run(t)
	hb, _ := e.jobs.snapshot()
	if hb < 3 {
		t.Fatalf("only %d heartbeats; the test needs overlap", hb)
	}
	if e.jobs.maxInFlight != 1 {
		t.Errorf("max concurrent Heartbeat calls = %d, want 1", e.jobs.maxInFlight)
	}
	// Progress is monotonic and Total never changes meaning.
	last := -1
	for _, p := range e.jobs.progress {
		if p.Total != 24 || p.Done < last {
			t.Fatalf("progress %+v after Done=%d", p, last)
		}
		last = p.Done
	}
}

func TestPutQuestionsFailureFailsJob(t *testing.T) {
	e := newWEnv(t, map[string]int{"1": 2})
	e.jobs.putQuestionsErr = errors.New("item too large: secret-detail")
	e.run(t)
	j := e.job(t)
	if j.Status != store.JobFailed || j.Error != SaveFailedMessage {
		t.Fatalf("job = %s %q", j.Status, j.Error)
	}
	if strings.Contains(j.Error, "secret-detail") {
		t.Error("raw error leaked")
	}
	if len(e.jobs.spend) != 1 { // the tokens were consumed even though the questions were lost
		t.Errorf("spend calls = %v", e.jobs.spend)
	}
}

func TestAddSpendCalledWithActualUsage(t *testing.T) {
	e := newWEnv(t, map[string]int{"1": 2, "2": 1})
	e.gen.fn = func(ctx context.Context, in GenInput) (GenOutput, error) {
		out := defaultOut(in)
		out.Usage = Usage{Input: 10_000 * int64(in.Marks), Output: 2_000, CacheRead: 5_000, CacheWrite: 1_000}
		return out, nil
	}
	e.run(t)
	m, _ := LookupModel("claude-haiku-4-5")
	// B-2.7: spend is added per call (AI-spec 10.4), each call rounded on its own, not once per job.
	c1 := CostMicros(m, Usage{Input: 10_000, Output: 2_000, CacheRead: 5_000, CacheWrite: 1_000})
	c2 := CostMicros(m, Usage{Input: 20_000, Output: 2_000, CacheRead: 5_000, CacheWrite: 1_000})
	total := c1 + c2
	if len(e.jobs.spend) != 2 || e.jobs.spend[0] != c1 || e.jobs.spend[1] != c2 || e.jobs.spendMonths[0] != "202610" {
		t.Fatalf("AddSpend = %v %v, want [%d %d] [202610 ...]", e.jobs.spend, e.jobs.spendMonths, c1, c2)
	}
	j := e.job(t)
	if j.Usage.CostMicros != total || j.Usage.InputTokens != 30_000 || j.Usage.OutputTokens != 4_000 ||
		j.Usage.CacheReadTokens != 10_000 || j.Usage.CacheWriteTokens != 2_000 || j.Usage.Calls != 2 || j.Usage.Requested != 3 {
		t.Errorf("job usage = %+v", j.Usage)
	}
	if spent, _ := e.raw.SpendThisMonth(context.Background(), "202610"); spent != total {
		t.Errorf("month spend = %d", spent)
	}
}

func TestAddSpendFailureDoesNotFailJob(t *testing.T) {
	e := newWEnv(t, map[string]int{"1": 2})
	e.jobs.addSpendErr = errors.New("dynamo down")
	e.run(t)
	if j := e.job(t); j.Status != store.JobDone {
		t.Fatalf("status = %s %q, want done (questions were saved)", j.Status, j.Error)
	}
}

func TestFailedJobStillRecordsSpend(t *testing.T) {
	e := newWEnv(t, map[string]int{"1": 2})
	e.gen.fn = func(ctx context.Context, in GenInput) (GenOutput, error) {
		return GenOutput{Usage: Usage{Input: 7_000}}, errors.New("boom")
	}
	e.run(t)
	m, _ := LookupModel("claude-haiku-4-5")
	if len(e.jobs.spend) != 1 || e.jobs.spend[0] != CostMicros(m, Usage{Input: 7_000}) {
		t.Errorf("spend = %v", e.jobs.spend)
	}
}

func TestGeneratorErrorFailsJobWithSafeMessage(t *testing.T) {
	e := newWEnv(t, map[string]int{"1": 2})
	e.gen.fn = func(ctx context.Context, in GenInput) (GenOutput, error) {
		return GenOutput{}, errors.New("401 invalid x-api-key sk-ant-SECRET")
	}
	e.run(t)
	j := e.job(t)
	if j.Status != store.JobFailed || j.Error != GenerateFailedMessage {
		t.Fatalf("job = %s %q", j.Status, j.Error)
	}
	if _, err := e.raw.GetQuestions(context.Background(), e.jobID); !errors.Is(err, store.ErrNotFound) {
		t.Error("questions written")
	}
}

func TestUserErrorMessageIsShown(t *testing.T) {
	e := newWEnv(t, map[string]int{"1": 2})
	e.gen.fn = func(ctx context.Context, in GenInput) (GenOutput, error) {
		return GenOutput{}, fmt.Errorf("wrapped: %w", &UserError{Message: "We couldn't read any of the selected files.", Err: errors.New("detail")})
	}
	e.run(t)
	if j := e.job(t); j.Error != "We couldn't read any of the selected files." {
		t.Errorf("error = %q", j.Error)
	}
}

func TestUnconfiguredFactoryFailsJobClearly(t *testing.T) {
	e := newWEnv(t, map[string]int{"1": 2})
	e.w.Gens = UnconfiguredFactory
	e.run(t)
	if j := e.job(t); j.Status != store.JobFailed || j.Error != GenerateFailedMessage {
		t.Errorf("job = %s %q", j.Status, j.Error)
	}
}

func TestSuccessSavesSortedQuestionsMappedToDocuments(t *testing.T) {
	e := newWEnv(t, map[string]int{"3": 1, "1": 2})
	e.gen.fn = func(ctx context.Context, in GenInput) (GenOutput, error) {
		out := defaultOut(in)
		for i := range out.Questions {
			out.Questions[i].Source = "doc-2"
		}
		out.Questions = append(out.Questions, RawQuestion{Marks: in.Marks, Text: "cites nothing real", Source: "doc-9"})
		out.UnreadableSources = []string{"doc-1"}
		return out, nil
	}
	e.run(t)
	j := e.job(t)
	if j.Status != store.JobDone || j.Done != 3 || j.Total != 3 {
		t.Fatalf("job = %s done=%d total=%d", j.Status, j.Done, j.Total)
	}
	qs, err := e.raw.GetQuestions(context.Background(), e.jobID)
	if err != nil || len(qs) != 3 {
		t.Fatalf("questions = %v %v", qs, err)
	}
	seen := map[string]bool{}
	for i, q := range qs {
		if i > 0 && q.Marks < qs[i-1].Marks {
			t.Errorf("not sorted by marks: %v", qs)
		}
		if q.ID == "" || seen[q.ID] {
			t.Errorf("bad id %q", q.ID)
		}
		seen[q.ID] = true
		if q.SourceDocID != "d2" || q.SourceDocName != "scan.png" {
			t.Errorf("source not mapped: %+v", q)
		}
	}
	if len(j.FailedSources) != 1 || j.FailedSources[0].DocID != "d1" || j.FailedSources[0].Name != "notes.txt" || j.FailedSources[0].Reason == "" {
		t.Errorf("failedSources = %+v", j.FailedSources)
	}
	if j.Usage.Dropped != 2 || j.Usage.Kept != 3 || !j.Usage.AnySourceFailed {
		t.Errorf("usage = %+v", j.Usage)
	}
}

func TestSourcesAreLabelledSortedAndOpaque(t *testing.T) {
	e := newWEnv(t, map[string]int{"1": 1})
	e.run(t)
	in := e.gen.calls[0]
	if len(in.Sources) != 2 || in.Sources[0].Label != "doc-1" || in.Sources[1].Label != "doc-2" ||
		string(in.Sources[0].Bytes) != "content of notes.txt" || in.Sources[0].ContentType != "text/plain" {
		t.Errorf("sources = %+v", in.Sources)
	}
	if in.Model != "claude-haiku-4-5" || in.Type != "mcq" || in.Marks != 1 || in.Count != 1 {
		t.Errorf("input = %+v", in)
	}
}

func TestUnreadableBlobGoesToFailedSources(t *testing.T) {
	e := newWEnv(t, map[string]int{"1": 1})
	if err := e.blob.Delete(context.Background(), "users/u1/d1/notes.txt"); err != nil {
		t.Fatal(err)
	}
	e.run(t)
	j := e.job(t)
	if j.Status != store.JobDone || len(j.FailedSources) != 1 || j.FailedSources[0].DocID != "d1" {
		t.Errorf("job = %s failed=%+v", j.Status, j.FailedSources)
	}
	if got := e.gen.calls[0].Sources; len(got) != 1 || got[0].Label != "doc-1" || string(got[0].Bytes) != "content of scan.png" {
		t.Errorf("sources = %+v", got)
	}
}

func TestNoReadableSourcesFailsWithoutCallingGenerator(t *testing.T) {
	e := newWEnv(t, map[string]int{"1": 1})
	_ = e.blob.Delete(context.Background(), "users/u1/d1/notes.txt")
	_ = e.blob.Delete(context.Background(), "users/u1/d2/scan.png")
	e.run(t)
	j := e.job(t)
	if e.gen.callCount() != 0 || j.Status != store.JobFailed || j.Error != NoSourcesMessage {
		t.Errorf("calls=%d job=%s %q", e.gen.callCount(), j.Status, j.Error)
	}
}

func TestCountsAreChunkedAndAvoidCarriesEarlierQuestions(t *testing.T) {
	e := newWEnv(t, map[string]int{"1": 20, "2": 1})
	e.run(t)
	// B-2.7: the first call runs alone, the other two run together, so the order of the last two
	// is not fixed: each sees the first call's 15 questions, and the one that starts after the
	// other has finished may see that one's too.
	var later []string
	for i, c := range e.gen.calls {
		if i == 0 {
			if c.Marks != 1 || c.Count != 15 || len(c.Avoid) != 0 {
				t.Errorf("first call = marks %d count %d avoid %d", c.Marks, c.Count, len(c.Avoid))
			}
			continue
		}
		if len(c.Avoid) < 15 {
			t.Errorf("call %d Avoid = %d, want at least the first call's 15", i+1, len(c.Avoid))
		}
		later = append(later, fmt.Sprintf("m%dx%d", c.Marks, c.Count))
	}
	slices.Sort(later)
	if got := strings.Join(later, " "); got != "m1x5 m2x1" {
		t.Errorf("later calls = %s", got)
	}
	if j := e.job(t); j.Done != 21 || j.Status != store.JobDone {
		t.Errorf("job = %s done=%d", j.Status, j.Done)
	}
}

func TestShuffleUsesInjectedRandomSourceAndKeepsEveryQuestion(t *testing.T) {
	e := newWEnvShuffle(t, map[string]int{"1": 4, "2": 4}, true)
	e.w.rnd = zeroReader{} // every draw is 0: swap(i, 0) for i = 7 down to 1
	e.run(t)
	qs, _ := e.raw.GetQuestions(context.Background(), e.jobID)
	if len(qs) != 8 {
		t.Fatalf("questions = %+v", qs)
	}
	// Generation order is the four 1-mark then the four 2-mark questions: q0..q3, p0..p3.
	// swap(i,0) for i = 7..1 rotates the list left by one: q1 q2 q3 p0 p1 p2 p3 q0.
	var marks []int
	for _, q := range qs {
		marks = append(marks, q.Marks)
	}
	if want := []int{1, 1, 1, 2, 2, 2, 2, 1}; !slices.Equal(marks, want) {
		t.Errorf("marks = %v, want %v (input order rotated by the injected source)", marks, want)
	}
}

func TestDefaultsApplyWhenFieldsAreZero(t *testing.T) {
	w := &Worker{}
	if w.heartbeatEvery() != 20*time.Second || w.deadline() != 8*time.Minute {
		t.Errorf("defaults = %v %v", w.heartbeatEvery(), w.deadline())
	}
}

func TestMissingJobIsNotAnError(t *testing.T) {
	e := newWEnv(t, map[string]int{"1": 1})
	if err := e.w.Run(context.Background(), "nope"); err != nil {
		t.Errorf("err = %v", err)
	}
	if e.gen.callCount() != 0 {
		t.Error("generator called")
	}
}

func TestRunRecoversGeneratorPanic(t *testing.T) {
	e := newWEnv(t, map[string]int{"1": 2})
	e.w.heartbeatInterval = 2 * time.Millisecond
	e.gen.fn = func(ctx context.Context, in GenInput) (GenOutput, error) {
		time.Sleep(10 * time.Millisecond) // let the heartbeat run
		panic("provider exploded: sk-ant-SECRET")
	}
	if err := e.w.Run(context.Background(), e.jobID); err != nil {
		t.Fatalf("Run returned %v, want nil", err)
	}
	j := e.job(t)
	if j.Status != store.JobFailed || j.Error != GenerateFailedMessage {
		t.Fatalf("job = %s %q", j.Status, j.Error)
	}
	if strings.Contains(j.Error, "SECRET") || strings.Contains(j.Error, "panic") {
		t.Error("panic value leaked into the job error")
	}
	if _, fin := e.jobs.snapshot(); len(fin) != 1 || fin[0] != store.JobFailed {
		t.Errorf("finish calls = %v", fin)
	}
	hb1, _ := e.jobs.snapshot()
	time.Sleep(20 * time.Millisecond)
	if hb2, _ := e.jobs.snapshot(); hb1 != hb2 || e.jobs.hbAfterFinish != 0 {
		t.Errorf("heartbeat still running after Run: %d -> %d (after finish: %d)", hb1, hb2, e.jobs.hbAfterFinish)
	}
	calls := e.gen.callCount()
	if err := e.w.Run(context.Background(), e.jobID); err != nil || e.gen.callCount() != calls {
		t.Errorf("duplicate Run did work: err=%v calls %d -> %d", err, calls, e.gen.callCount())
	}
}

func TestRunRecoversPanicWithUsage(t *testing.T) {
	e := newWEnv(t, map[string]int{"1": 1, "2": 1})
	e.gen.fn = func(ctx context.Context, in GenInput) (GenOutput, error) {
		if in.Marks == 2 {
			panic("boom")
		}
		out := defaultOut(in)
		out.Usage = Usage{Input: 4_000, Output: 500}
		return out, nil
	}
	e.run(t)
	m, _ := LookupModel("claude-haiku-4-5")
	want := CostMicros(m, Usage{Input: 4_000, Output: 500})
	if len(e.jobs.spend) != 1 || e.jobs.spend[0] != want {
		t.Fatalf("spend = %v, want [%d]", e.jobs.spend, want)
	}
	if j := e.job(t); j.Status != store.JobFailed || j.Usage.CostMicros != want || j.Usage.Calls != 1 {
		t.Errorf("job = %s usage=%+v", j.Status, j.Usage)
	}
}

// ---- B-2.6: sources are prepared and output is validated ----

// setDoc replaces document id with the given type and bytes.
func (e *wEnv) setDoc(t *testing.T, id, name, contentType string, data []byte) {
	t.Helper()
	key := "users/u1/" + id + "/" + name
	e.blob.Put(key, data)
	if err := e.docs.PutDoc(context.Background(), store.Document{
		ID: id, OwnerID: "u1", Name: name, Type: "TXT", ContentType: contentType, SizeBytes: int64(len(data)),
		S3Key: key, Status: store.DocReady, CreatedAt: t0,
	}); err != nil {
		t.Fatal(err)
	}
}

func TestWorkerSendsPreparedSources(t *testing.T) {
	e := newWEnv(t, map[string]int{"1": 1})
	pdf := []byte("%PDF-1.7 fake body")
	e.setDoc(t, "d1", "notes.txt", "text/plain", []byte("Line one\x00‮​\r\nLine two\x07"))
	e.setDoc(t, "d2", "paper.pdf", "application/pdf", pdf)
	e.run(t)
	got := e.gen.calls[0].Sources
	if len(got) != 2 {
		t.Fatalf("sources = %+v", got)
	}
	if string(got[0].Bytes) != "Line one\nLine two" { // control, bidi and zero-width removed, CRLF normalised
		t.Errorf("text source not cleaned: %q", got[0].Bytes)
	}
	if got[1].ContentType != "application/pdf" || !bytes.Equal(got[1].Bytes, pdf) {
		t.Errorf("pdf source = %q %q", got[1].ContentType, got[1].Bytes)
	}
}

func TestWorkerDoesNotSendCorruptSourceAndReportsIt(t *testing.T) {
	e := newWEnv(t, map[string]int{"1": 1})
	e.setDoc(t, "d2", "scan.png", "image/png", []byte("\x89PNG\r\n\x1a\nnot really a png"))
	e.run(t)
	j := e.job(t)
	if j.Status != store.JobDone {
		t.Fatalf("job = %s %q", j.Status, j.Error)
	}
	if got := e.gen.calls[0].Sources; len(got) != 1 || got[0].Label != "doc-1" || string(got[0].Bytes) != "content of notes.txt" {
		t.Errorf("sources = %+v", got)
	}
	if len(j.FailedSources) != 1 || j.FailedSources[0].DocID != "d2" || j.FailedSources[0].Name != "scan.png" ||
		j.FailedSources[0].Reason != unreadableFileReason || !j.Usage.AnySourceFailed {
		t.Errorf("failedSources = %+v", j.FailedSources)
	}
}

func TestWorkerFailsWhenNoSourceSurvivesPreparation(t *testing.T) {
	e := newWEnv(t, map[string]int{"1": 1})
	e.setDoc(t, "d1", "blank.txt", "text/plain", []byte(" ​ \n"))
	e.setDoc(t, "d2", "bad.pdf", "application/pdf", []byte("not a pdf"))
	e.run(t)
	j := e.job(t)
	if e.gen.callCount() != 0 || j.Status != store.JobFailed || j.Error != "We couldn't read any of the selected files." {
		t.Errorf("calls=%d job=%s %q", e.gen.callCount(), j.Status, j.Error)
	}
	if len(j.FailedSources) != 2 {
		t.Errorf("failedSources = %+v", j.FailedSources)
	}
	if _, err := e.raw.GetQuestions(context.Background(), e.jobID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("questions saved: %v", err)
	}
}

func TestWorkerSurfacesGeneratorUnreadableSources(t *testing.T) {
	e := newWEnv(t, map[string]int{"1": 1})
	e.gen.fn = func(ctx context.Context, in GenInput) (GenOutput, error) {
		out := defaultOut(in)
		out.UnreadableSources = []string{"doc-2", "doc-7"} // doc-7 is not a label we sent
		return out, nil
	}
	e.run(t)
	j := e.job(t)
	if len(j.FailedSources) != 1 || j.FailedSources[0].DocID != "d2" || j.FailedSources[0].Reason != modelSkippedReason {
		t.Errorf("failedSources = %+v", j.FailedSources)
	}
}

func TestWorkerValidatesOutputBeforeSaving(t *testing.T) {
	e := newWEnv(t, map[string]int{"2": 2})
	e.gen.fn = func(ctx context.Context, in GenInput) (GenOutput, error) {
		opts := []string{"a", "b", "c", "d"}
		out := GenOutput{Usage: Usage{Input: 10, Output: 10}, Questions: []RawQuestion{
			{Marks: 2, Text: "Good question number one about cells", Options: opts, CorrectIndex: ip(0), Source: "doc-1"},
			{Marks: 2, Text: "Bad <b>html</b> question", Options: opts, CorrectIndex: ip(0), Source: "doc-1"},
			{Marks: 3, Text: "Wrong marks question", Options: opts, CorrectIndex: ip(0), Source: "doc-1"},
			{Marks: 2, Text: "Good question number one about cells", Options: opts, CorrectIndex: ip(1), Source: "doc-1"}, // duplicate
			{Marks: 2, Text: "Good second question about mitochondria", Options: opts, CorrectIndex: ip(2), Source: "doc-2"},
			{Marks: 2, Text: "Third valid but one too many question on ribosomes", Options: opts, CorrectIndex: ip(3), Source: "doc-1"},
		}}
		return out, nil
	}
	e.run(t)
	j := e.job(t)
	qs, err := e.raw.GetQuestions(context.Background(), e.jobID)
	if err != nil || j.Status != store.JobDone || len(qs) != 2 {
		t.Fatalf("job=%s %q qs=%v err=%v", j.Status, j.Error, qs, err)
	}
	if qs[0].Text != "Good question number one about cells" || qs[1].Text != "Good second question about mitochondria" ||
		qs[1].SourceDocID != "d2" || qs[1].SourceDocName != "scan.png" {
		t.Errorf("questions = %+v", qs)
	}
	if j.Usage.Kept != 2 || j.Usage.Dropped != 4 { // html, marks, duplicate and the one past the count
		t.Errorf("usage = %+v", j.Usage)
	}
}

func TestWorkerFailsWithShortfallMessageWhenTooFewSurvive(t *testing.T) {
	e := newWEnv(t, map[string]int{"1": 3})
	e.gen.fn = func(ctx context.Context, in GenInput) (GenOutput, error) {
		out := defaultOut(in)
		for i := 1; i < len(out.Questions); i++ {
			out.Questions[i].Text = "" // invalid: only the first question of each call survives
		}
		return out, nil
	}
	e.run(t)
	j := e.job(t)
	if j.Status != store.JobFailed || j.Error != "We couldn't generate enough questions from these files." {
		t.Errorf("job = %s %q", j.Status, j.Error)
	}
	if _, err := e.raw.GetQuestions(context.Background(), e.jobID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("partial exam saved: %v", err)
	}
	// B-2.7: one follow-up call (AI-spec 7.4) for the 2 missing, then the failure; both calls are charged.
	if len(e.jobs.spend) != 2 || e.jobs.spend[0] <= 0 || e.jobs.spend[1] <= 0 {
		t.Errorf("spend = %v, want both calls' cost recorded", e.jobs.spend)
	}
	if j.Usage.Dropped != 3 || j.Usage.Kept != 2 {
		t.Errorf("usage = %+v", j.Usage)
	}
	if e.gen.callCount() != 2 {
		t.Errorf("calls = %d, want the call and one follow-up", e.gen.callCount())
	}
}

func TestWorkerShufflesMCQOptionsKeepingTheAnswer(t *testing.T) {
	e := newWEnv(t, map[string]int{"1": 4})
	e.w.rnd = zeroReader{} // n=4: swap(3,0), n=3: swap(2,0), n=2: swap(1,0)
	e.gen.fn = func(ctx context.Context, in GenInput) (GenOutput, error) {
		out := defaultOut(in)
		for i := range out.Questions {
			c := i % 4
			out.Questions[i].CorrectIndex = &c
			out.Questions[i].Text = fmt.Sprintf("Distinct prompt %c about %c", 'a'+i, 'q'+i)
		}
		return out, nil
	}
	e.run(t)
	qs, _ := e.raw.GetQuestions(context.Background(), e.jobID)
	if len(qs) != 4 {
		t.Fatalf("qs = %+v", qs)
	}
	letters := []string{"a", "b", "c", "d"}
	for i, q := range qs {
		if q.Options[0] == "a" && q.Options[3] == "d" {
			t.Errorf("question %d options not shuffled: %v", i, q.Options)
		}
		if q.Options[*q.CorrectIndex] != letters[i%4] {
			t.Errorf("question %d: answer %q at %d, want %q", i, q.Options[*q.CorrectIndex], *q.CorrectIndex, letters[i%4])
		}
	}
}

func TestWorkerAssignsIDsFromInjectedClockAndEntropy(t *testing.T) {
	e := newWEnv(t, map[string]int{"1": 3})
	e.w.idEntropy = bytes.NewReader(bytes.Repeat([]byte{9}, 1024))
	e.run(t)
	qs, _ := e.raw.GetQuestions(context.Background(), e.jobID)
	seen := map[string]bool{}
	for _, q := range qs {
		id, err := ulid.ParseStrict(q.ID)
		if err != nil || id.Time() != ulid.Timestamp(t0) || seen[q.ID] {
			t.Errorf("id %q: err=%v time=%d dup=%v", q.ID, err, id.Time(), seen[q.ID])
		}
		seen[q.ID] = true
	}
}

func TestFakeGeneratorOutputSurvivesValidation(t *testing.T) {
	// The local -fake-llm server relies on this: 15-question calls must pass the duplicate check.
	e := newWEnv(t, map[string]int{"1": 20, "2": 15, "5": 4})
	e.w.Gens = FixedFactory(FakeGenerator{})
	e.run(t)
	if j := e.job(t); j.Status != store.JobDone || j.Done != 39 {
		t.Errorf("mcq: job = %s %q done=%d", j.Status, j.Error, j.Done)
	}
	labels := map[string]store.Document{"doc-1": {ID: "d1", Name: "n"}, "doc-2": {ID: "d2", Name: "m"}}
	for _, typ := range []string{"mcq", "free"} {
		var avoid []string
		for call := 0; call < 3; call++ {
			out, _ := FakeGenerator{}.Generate(context.Background(), GenInput{
				Sources: []SourceFile{{Label: "doc-1"}, {Label: "doc-2"}}, Type: typ, Marks: 3, Count: 15, Avoid: avoid,
			})
			kept, dropped, _ := Validate(out.Questions, CallSpec{Type: typ, Marks: 3}, labels, avoid)
			if len(kept) != 15 || dropped != 0 {
				t.Fatalf("%s call %d: kept=%d dropped=%d", typ, call, len(kept), dropped)
			}
			for _, q := range kept {
				avoid = append(avoid, q.Text)
			}
		}
	}
}
