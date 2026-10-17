package exams_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gokulkumar/exam-assistant/backend/internal/exams"
	"github.com/gokulkumar/exam-assistant/backend/internal/store"
)

var closes = t0.Add(48 * time.Hour)

func (e *env) seedExam(t *testing.T, id, creator, typ string, closesAt, createdAt time.Time) store.Exam {
	t.Helper()
	ci := 1
	q := store.Question{ID: "q1", Marks: 2, Text: "Q?", SourceDocID: "d", SourceDocName: "B.pdf"}
	if typ == "mcq" {
		q.Options, q.CorrectIndex = []string{"a", "b", "c", "d"}, &ci
	} else {
		q.ModelAnswer = "m"
	}
	x := store.Exam{ID: id, CreatorID: creator, Title: "T-" + id, Type: typ, TotalMarks: 30, ClosesAt: closesAt,
		Questions: []store.Question{q}, CreatedAt: createdAt}
	if err := e.exams.PutExam(context.Background(), x); err != nil {
		t.Fatal(err)
	}
	return x
}

func (e *env) seedAttempt(t *testing.T, examID, userID, status string, halves *int) {
	t.Helper()
	a := store.Attempt{AttemptNo: 1, ExamID: examID, UserID: userID, Status: status, Answers: map[string]string{}, Revealed: []string{},
		ScoreHalves: halves, CreatedAt: t0}
	if status == store.AttemptSubmitted {
		s := t0
		a.SubmittedAt = &s
	}
	if _, err := e.attempts.PutAttemptIfAbsent(context.Background(), a); err != nil {
		t.Fatal(err)
	}
}

func ip(i int) *int { return &i }

func (e *env) list(t *testing.T, u store.User, scope string) []exams.ListItem {
	t.Helper()
	out, err := e.svc.List(context.Background(), u, scope)
	if err != nil {
		t.Fatalf("List(%s): %v", scope, err)
	}
	return out
}

func ids(items []exams.ListItem) []string {
	out := []string{}
	for _, i := range items {
		out = append(out, i.ID)
	}
	return out
}

func eqIDs(t *testing.T, got []exams.ListItem, want ...string) {
	t.Helper()
	g := ids(got)
	if fmt.Sprint(g) != fmt.Sprint(want) {
		t.Fatalf("ids = %v, want %v", g, want)
	}
}

func TestOpenIncludesNotStartedAndInProgressBeforeAcceptingUntil(t *testing.T) {
	e := newEnv(t)
	e.seedExam(t, "e1", "t1", "mcq", closes, t0)
	e.seedExam(t, "e2", "t1", "mcq", closes, t0)
	e.seedAttempt(t, "e1", "s1", store.AttemptNotStarted, nil)
	e.seedAttempt(t, "e2", "s1", store.AttemptInProgress, nil)
	e.clk.Set(closes.Add(-time.Second))
	got := e.list(t, e.student, "open")
	eqIDs(t, got, "e1", "e2")
	if got[0].AttemptStatus != "not_started" || got[1].AttemptStatus != "in_progress" {
		t.Fatalf("statuses %+v", got)
	}
	if got[0].AcceptingUntil != closes.Add(time.Minute) || got[0].CreatedBy.Name != "t1" || got[0].CreatedBy.ID != "t1" {
		t.Fatalf("item %+v", got[0])
	}
	if len(e.list(t, e.student, "closed")) != 0 {
		t.Fatal("nothing should be closed")
	}
}

func TestGraceWindowStillOpen(t *testing.T) {
	e := newEnv(t)
	e.seedExam(t, "e1", "t1", "mcq", closes, t0)
	e.seedAttempt(t, "e1", "s1", store.AttemptInProgress, nil)
	e.clk.Set(closes.Add(30 * time.Second))
	eqIDs(t, e.list(t, e.student, "open"), "e1")
	eqIDs(t, e.list(t, e.student, "closed"))
}

func TestBoundaryAtAcceptingUntilIsClosed(t *testing.T) {
	e := newEnv(t)
	e.seedExam(t, "e1", "t1", "mcq", closes, t0)
	e.seedAttempt(t, "e1", "s1", store.AttemptNotStarted, nil)
	e.clk.Set(closes.Add(time.Minute - time.Nanosecond))
	eqIDs(t, e.list(t, e.student, "open"), "e1")
	e.clk.Set(closes.Add(time.Minute))
	eqIDs(t, e.list(t, e.student, "open"))
	got := e.list(t, e.student, "closed")
	eqIDs(t, got, "e1")
	if got[0].AttemptStatus != "missed" {
		t.Fatalf("status %q", got[0].AttemptStatus)
	}
	x := store.Exam{ClosesAt: closes}
	if exams.Classify(x, store.Attempt{Status: "in_progress"}, closes.Add(time.Minute)) != "closed" ||
		exams.Classify(x, store.Attempt{Status: "in_progress"}, closes.Add(time.Minute-1)) != "open" {
		t.Fatal("Classify boundary")
	}
}

func TestClosedHasSubmittedAndMissed(t *testing.T) {
	e := newEnv(t)
	e.seedExam(t, "e1", "t1", "mcq", closes, t0)
	e.seedExam(t, "e2", "t1", "mcq", closes, t0)
	e.seedExam(t, "e3", "t1", "mcq", closes, t0)
	e.seedAttempt(t, "e1", "s1", store.AttemptSubmitted, ip(4))
	e.seedAttempt(t, "e2", "s1", store.AttemptNotStarted, nil)
	e.seedAttempt(t, "e3", "s1", store.AttemptInProgress, nil) // nil Finalizer: derived missed
	e.clk.Set(closes.Add(time.Hour))
	got := e.list(t, e.student, "closed")
	eqIDs(t, got, "e1", "e2", "e3")
	if got[0].AttemptStatus != "submitted" || got[1].AttemptStatus != "missed" || got[2].AttemptStatus != "missed" {
		t.Fatalf("%+v", got)
	}
	if exams.DerivedStatus(store.Exam{ClosesAt: closes}, store.Attempt{Status: "not_started"}, closes.Add(time.Minute)) != "missed" {
		t.Fatal("DerivedStatus")
	}
}

func TestSubmittedBeforeCloseIsClosedImmediately(t *testing.T) {
	e := newEnv(t)
	e.seedExam(t, "e1", "t1", "mcq", closes, t0)
	e.seedAttempt(t, "e1", "s1", store.AttemptSubmitted, ip(2))
	e.clk.Set(closes.Add(-24 * time.Hour))
	eqIDs(t, e.list(t, e.student, "open"))
	eqIDs(t, e.list(t, e.student, "closed"), "e1")
}

func TestDeclinedInNeitherList(t *testing.T) {
	e := newEnv(t)
	e.seedExam(t, "e1", "t1", "mcq", closes, t0)
	e.seedAttempt(t, "e1", "s1", store.AttemptDeclined, nil)
	for _, now := range []time.Time{closes.Add(-time.Hour), closes.Add(time.Hour)} {
		e.clk.Set(now)
		eqIDs(t, e.list(t, e.student, "open"))
		eqIDs(t, e.list(t, e.student, "closed"))
	}
}

func TestCreatedCap50NewestFirstWithCountsAndNoAttemptFields(t *testing.T) {
	e := newEnv(t)
	for i := 0; i < 55; i++ {
		e.seedExam(t, fmt.Sprintf("e%02d", i), "t1", "mcq", closes, t0.Add(time.Duration(i)*time.Minute))
	}
	e.seedExam(t, "other", "p1", "mcq", closes, t0)
	e.seedAttempt(t, "e54", "s1", store.AttemptSubmitted, ip(1))
	e.seedAttempt(t, "e54", "p1", store.AttemptInProgress, nil)
	e.seedAttempt(t, "e54", "t1", store.AttemptNotStarted, nil)
	got := e.list(t, e.teacher, "created")
	if len(got) != 50 || got[0].ID != "e54" || got[49].ID != "e05" {
		t.Fatalf("len %d first %s last %s", len(got), got[0].ID, got[len(got)-1].ID)
	}
	c := got[0].Counts
	if c == nil || *c != (exams.Counts{Submitted: 1, InProgress: 1, NotStarted: 1}) {
		t.Fatalf("counts %+v", c)
	}
	if got[1].Counts == nil || *got[1].Counts != (exams.Counts{}) {
		t.Fatalf("zero counts must be present: %+v", got[1].Counts)
	}
	b, _ := json.Marshal(got[0])
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	for _, k := range []string{"attemptStatus", "score", "maxScore"} {
		if _, ok := m[k]; ok {
			t.Errorf("created item must omit %s: %s", k, b)
		}
	}
	if m["counts"] == nil || m["createdBy"] == nil || m["acceptingUntil"] == nil {
		t.Fatalf("%s", b)
	}
	if got[0].CreatedBy.ID != "t1" {
		t.Fatalf("createdBy %+v", got[0].CreatedBy)
	}
}

func TestCountsIncludeDeclined(t *testing.T) {
	e := newEnv(t)
	e.seedExam(t, "e1", "t1", "mcq", closes, t0)
	e.seedAttempt(t, "e1", "s1", store.AttemptDeclined, nil)
	e.seedAttempt(t, "e1", "p1", store.AttemptSubmitted, ip(2))
	got := e.list(t, e.teacher, "created")
	if got[0].Counts.Declined != 1 || got[0].Counts.Submitted != 1 {
		t.Fatalf("%+v", got[0].Counts)
	}
	b, _ := json.Marshal(got[0].Counts)
	if string(b) != `{"submitted":1,"inProgress":0,"notStarted":0,"declined":1}` {
		t.Fatalf("json %s", b)
	}
}

// recAttempts records calls and the concurrency of ListByExam.
type recAttempts struct {
	store.AttemptStore
	mu                       sync.Mutex
	cur, max, byUser, byExam int
	getAttempt               int
	failByExam               error
}

func (r *recAttempts) ListByExam(ctx context.Context, id string) ([]store.Attempt, error) {
	r.mu.Lock()
	r.cur++
	r.byExam++
	if r.cur > r.max {
		r.max = r.cur
	}
	r.mu.Unlock()
	time.Sleep(5 * time.Millisecond)
	if r.failByExam != nil {
		defer func() { r.mu.Lock(); r.cur--; r.mu.Unlock() }()
		return nil, r.failByExam
	}
	defer func() { r.mu.Lock(); r.cur--; r.mu.Unlock() }()
	return r.AttemptStore.ListByExam(ctx, id)
}

func (r *recAttempts) ListByUser(ctx context.Context, id string) ([]store.AttemptSummary, error) {
	r.mu.Lock()
	r.byUser++
	r.mu.Unlock()
	return r.AttemptStore.ListByUser(ctx, id)
}

type recExams struct {
	store.ExamStore
	getExam, getExams, byCreator int32
	mu                           sync.Mutex
	loaded                       []string // every id passed to GetExams
	failGetExams                 error
}

func (r *recExams) GetExam(ctx context.Context, id string) (store.Exam, error) {
	atomic.AddInt32(&r.getExam, 1)
	return r.ExamStore.GetExam(ctx, id)
}
func (r *recExams) GetExams(ctx context.Context, ids []string) (map[string]store.Exam, error) {
	atomic.AddInt32(&r.getExams, 1)
	r.mu.Lock()
	r.loaded = append(r.loaded, ids...)
	r.mu.Unlock()
	if r.failGetExams != nil {
		return nil, r.failGetExams
	}
	return r.ExamStore.GetExams(ctx, ids)
}
func (r *recExams) ListByCreator(ctx context.Context, c string, n int) ([]store.ExamSummary, error) {
	atomic.AddInt32(&r.byCreator, 1)
	return r.ExamStore.ListByCreator(ctx, c, n)
}

func TestCountsQueriesRun10AtATime(t *testing.T) {
	e := newEnv(t)
	for i := 0; i < 50; i++ {
		e.seedExam(t, fmt.Sprintf("e%02d", i), "t1", "mcq", closes, t0.Add(time.Duration(i)*time.Second))
		e.seedAttempt(t, fmt.Sprintf("e%02d", i), "s1", store.AttemptNotStarted, nil)
	}
	rec := &recAttempts{AttemptStore: e.attempts}
	svc := exams.NewService(e.users, e.exams, rec, e.clk)
	got, err := svc.List(context.Background(), e.teacher, "created")
	if err != nil || len(got) != 50 {
		t.Fatalf("%d %v", len(got), err)
	}
	if rec.byExam != 50 {
		t.Fatalf("ListByExam calls %d", rec.byExam)
	}
	if rec.max > 10 || rec.max < 2 {
		t.Fatalf("max concurrency %d, want 2..10", rec.max)
	}
}

func TestScoreOnlyForSubmittedMCQ(t *testing.T) {
	e := newEnv(t)
	e.seedExam(t, "m1", "t1", "mcq", closes, t0)
	e.seedExam(t, "m2", "t1", "mcq", closes, t0)
	e.seedExam(t, "f1", "t1", "free", closes, t0)
	e.seedAttempt(t, "m1", "s1", store.AttemptSubmitted, ip(55)) // 27.5
	e.seedAttempt(t, "m2", "s1", store.AttemptInProgress, nil)
	e.seedAttempt(t, "f1", "s1", store.AttemptSubmitted, nil)
	e.clk.Set(closes.Add(-time.Hour))
	closed := e.list(t, e.student, "closed")
	eqIDs(t, closed, "f1", "m1")
	if closed[1].Score == nil || *closed[1].Score != 27.5 || closed[1].MaxScore == nil || *closed[1].MaxScore != 30 {
		t.Fatalf("%+v", closed[1])
	}
	if closed[0].Score != nil {
		t.Fatal("free exam must have no score")
	}
	b, _ := json.Marshal(closed[1])
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	if m["score"] != 27.5 || m["maxScore"] != float64(30) {
		t.Fatalf("%s", b)
	}
	if s := string(b); !contains(s, `"maxScore":30`) {
		t.Fatalf("maxScore must be an integer: %s", s)
	}
	open := e.list(t, e.student, "open")
	eqIDs(t, open, "m2")
	if open[0].Score != nil {
		t.Fatal("open item has no score")
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func TestListUsesGSIThenBatchGet(t *testing.T) {
	e := newEnv(t)
	for i := 0; i < 3; i++ {
		e.seedExam(t, fmt.Sprintf("e%d", i), "t1", "mcq", closes, t0)
		e.seedAttempt(t, fmt.Sprintf("e%d", i), "s1", store.AttemptNotStarted, nil)
	}
	ra, re := &recAttempts{AttemptStore: e.attempts}, &recExams{ExamStore: e.exams}
	svc := exams.NewService(e.users, re, ra, e.clk)
	e.clk.Set(closes.Add(-time.Hour))
	got, err := svc.List(context.Background(), e.student, "open")
	if err != nil || len(got) != 3 {
		t.Fatalf("%d %v", len(got), err)
	}
	if ra.byUser != 1 || re.getExams != 1 || re.getExam != 0 || ra.byExam != 0 || re.byCreator != 0 {
		t.Fatalf("byUser=%d getExams=%d getExam=%d byExam=%d byCreator=%d", ra.byUser, re.getExams, re.getExam, ra.byExam, re.byCreator)
	}
	if _, err := svc.List(context.Background(), e.teacher, "created"); err != nil || re.byCreator != 1 {
		t.Fatalf("created must use the creator index: %d %v", re.byCreator, err)
	}
}

func TestBadScope422(t *testing.T) {
	e := newEnv(t)
	for _, s := range []string{"", "all", "OPEN", "open,closed"} {
		_, err := e.svc.List(context.Background(), e.student, s)
		wantField(t, err, "scope")
	}
}

func TestCreatedByShownForAssignee(t *testing.T) {
	e := newEnv(t)
	e.seedExam(t, "e1", "t1", "mcq", closes, t0)
	e.seedAttempt(t, "e1", "s1", store.AttemptNotStarted, nil)
	e.clk.Set(closes.Add(-time.Hour))
	got := e.list(t, e.student, "open")
	b, _ := json.Marshal(got[0].CreatedBy)
	if string(b) != `{"id":"t1","name":"t1"}` {
		t.Fatalf("createdBy %s (id and name only, no email)", b)
	}
}

func TestMissingExamRowIsSkipped(t *testing.T) {
	e := newEnv(t)
	e.seedExam(t, "e1", "t1", "mcq", closes, t0)
	e.seedAttempt(t, "e1", "s1", store.AttemptNotStarted, nil)
	e.seedAttempt(t, "ghost", "s1", store.AttemptNotStarted, nil)
	e.clk.Set(closes.Add(-time.Hour))
	eqIDs(t, e.list(t, e.student, "open"), "e1")
}

type fakeFin struct {
	calls int32
	err   error
}

func (f *fakeFin) FinalizeIfExpired(_ context.Context, a store.Attempt, x store.Exam, now time.Time) (store.Attempt, error) {
	atomic.AddInt32(&f.calls, 1)
	if f.err != nil {
		return a, f.err
	}
	a.Status, a.AutoSubmitted, a.ScoreHalves = store.AttemptSubmitted, true, ip(3)
	return a, nil
}

func TestFinalizerCalledOnlyForExpiredInProgress(t *testing.T) {
	e := newEnv(t)
	f := &fakeFin{}
	e.svc.SetFinalizer(f)
	e.seedExam(t, "e1", "t1", "mcq", closes, t0)
	e.seedExam(t, "e2", "t1", "mcq", closes, t0)
	e.seedAttempt(t, "e1", "s1", store.AttemptInProgress, nil)
	e.seedAttempt(t, "e2", "s1", store.AttemptNotStarted, nil)
	e.clk.Set(closes.Add(30 * time.Second)) // grace: not expired
	e.list(t, e.student, "open")
	if f.calls != 0 {
		t.Fatalf("finalizer called inside grace window")
	}
	e.clk.Set(closes.Add(time.Minute))
	got := e.list(t, e.student, "closed")
	eqIDs(t, got, "e1", "e2")
	if f.calls != 1 || got[0].AttemptStatus != "submitted" || got[0].Score == nil || *got[0].Score != 1.5 {
		t.Fatalf("calls %d %+v", f.calls, got[0])
	}
}

func (e *env) seedSubmittedAt(t *testing.T, examID, userID string, at time.Time) {
	t.Helper()
	a := store.Attempt{AttemptNo: 1, ExamID: examID, UserID: userID, Status: store.AttemptSubmitted, Answers: map[string]string{},
		Revealed: []string{}, ScoreHalves: ip(2), SubmittedAt: &at, CreatedAt: t0}
	if _, err := e.attempts.PutAttemptIfAbsent(context.Background(), a); err != nil {
		t.Fatal(err)
	}
}

func TestOpenNotHiddenByLargeHistoryWithLowestID(t *testing.T) {
	e := newEnv(t)
	e.seedExam(t, "0000-open", "t1", "mcq", closes, t0)
	e.seedAttempt(t, "0000-open", "s1", store.AttemptNotStarted, nil)
	for i := 0; i < 600; i++ {
		id := fmt.Sprintf("f%04d", i)
		e.seedExam(t, id, "t1", "mcq", closes.Add(-time.Hour), t0)
		e.seedSubmittedAt(t, id, "s1", t0.Add(time.Duration(i)*time.Second))
	}
	re := &recExams{ExamStore: e.exams}
	svc := exams.NewService(e.users, re, e.attempts, e.clk)
	e.clk.Set(closes.Add(-time.Minute))
	got, err := svc.List(context.Background(), e.student, "open")
	if err != nil {
		t.Fatal(err)
	}
	eqIDs(t, got, "0000-open")
	if len(re.loaded) != 1 {
		t.Fatalf("open must load only unfinished rows' exams, loaded %d", len(re.loaded))
	}
}

func TestClosedHistoryCapsAt200NewestSubmittedWithoutLoadingMore(t *testing.T) {
	e := newEnv(t)
	for i := 0; i < 250; i++ {
		id := fmt.Sprintf("h%03d", (i*7)%250) // ids unrelated to submittedAt order
		e.seedExam(t, id, "t1", "mcq", closes, t0)
		e.seedSubmittedAt(t, id, "s1", t0.Add(time.Duration(i)*time.Second))
	}
	re := &recExams{ExamStore: e.exams}
	svc := exams.NewService(e.users, re, e.attempts, e.clk)
	got, err := svc.List(context.Background(), e.student, "closed")
	if err != nil || len(got) != 200 {
		t.Fatalf("len %d err %v", len(got), err)
	}
	if len(re.loaded) != 200 {
		t.Fatalf("loaded %d exams, want 200", len(re.loaded))
	}
	have := map[string]bool{}
	for _, it := range got {
		have[it.ID] = true
	}
	for i := 0; i < 250; i++ {
		id := fmt.Sprintf("h%03d", (i*7)%250)
		if (i >= 50) != have[id] {
			t.Fatalf("submission %d (%s): in result = %v", i, id, have[id])
		}
	}
}

type logSink struct {
	mu   sync.Mutex
	msgs []string
}

func (l *logSink) Write(p []byte) (int, error) {
	l.mu.Lock()
	l.msgs = append(l.msgs, string(p))
	l.mu.Unlock()
	return len(p), nil
}

func TestPendingSafetyBoundLogsWarnNotSilent(t *testing.T) {
	e := newEnv(t)
	for i := 0; i < 8; i++ {
		id := fmt.Sprintf("p%d", i)
		e.seedExam(t, id, "t1", "mcq", closes, t0)
		e.seedAttempt(t, id, "s1", store.AttemptNotStarted, nil)
	}
	sink := &logSink{}
	e.svc.SetLogger(slog.New(slog.NewTextHandler(sink, nil)))
	e.svc.SetMaxPending(5)
	e.clk.Set(closes.Add(-time.Minute))
	if got := e.list(t, e.student, "open"); len(got) != 5 {
		t.Fatalf("len %d", len(got))
	}
	if len(sink.msgs) == 0 || !strings.Contains(sink.msgs[0], "level=WARN") {
		t.Fatalf("expected a WARN log, got %v", sink.msgs)
	}
	sink.msgs = nil
	e.svc.SetMaxPending(8)
	e.list(t, e.student, "open")
	if len(sink.msgs) != 0 {
		t.Fatalf("no warning expected at the bound: %v", sink.msgs)
	}
}

func TestDeclinedGuardsEachAlone(t *testing.T) {
	x := store.Exam{ClosesAt: closes}
	for _, now := range []time.Time{closes.Add(-time.Hour), closes.Add(time.Hour)} {
		if c := exams.Classify(x, store.Attempt{Status: store.AttemptDeclined}, now); c != "" {
			t.Fatalf("Classify must not list declined, got %q", c)
		}
	}
	e := newEnv(t)
	e.seedExam(t, "e1", "t1", "mcq", closes, t0)
	e.seedAttempt(t, "e1", "s1", store.AttemptDeclined, nil)
	re := &recExams{ExamStore: e.exams}
	svc := exams.NewService(e.users, re, e.attempts, e.clk)
	if _, err := svc.List(context.Background(), e.student, "open"); err != nil {
		t.Fatal(err)
	}
	if len(re.loaded) != 0 {
		t.Fatalf("declined rows must be dropped at the index, loaded %v", re.loaded)
	}
}

func TestScoreGuardsDistinguishMCQAndSubmitted(t *testing.T) {
	e := newEnv(t)
	e.seedExam(t, "free", "t1", "free", closes, t0)
	e.seedExam(t, "prog", "t1", "mcq", closes, t0)
	e.seedAttempt(t, "free", "s1", store.AttemptSubmitted, ip(10))
	e.seedAttempt(t, "prog", "s1", store.AttemptInProgress, ip(10))
	e.clk.Set(closes.Add(-time.Hour))
	for _, it := range append(e.list(t, e.student, "open"), e.list(t, e.student, "closed")...) {
		if it.Score != nil {
			t.Fatalf("%s must have no score: %v", it.ID, *it.Score)
		}
	}
}

func TestOpenSortSoonestFirstClosedLatestFirst(t *testing.T) {
	e := newEnv(t)
	for i, id := range []string{"b", "a", "c"} {
		e.seedExam(t, id, "t1", "mcq", closes.Add(time.Duration(i)*time.Hour), t0)
		e.seedAttempt(t, id, "s1", store.AttemptNotStarted, nil)
	}
	e.clk.Set(closes.Add(-time.Minute))
	eqIDs(t, e.list(t, e.student, "open"), "b", "a", "c")
	e.clk.Set(closes.Add(24 * time.Hour))
	eqIDs(t, e.list(t, e.student, "closed"), "c", "a", "b")
}

func TestMissingExamRowSkippedInClosed(t *testing.T) {
	e := newEnv(t)
	e.seedExam(t, "e1", "t1", "mcq", closes, t0)
	e.seedAttempt(t, "e1", "s1", store.AttemptNotStarted, nil)
	e.seedAttempt(t, "ghost", "s1", store.AttemptNotStarted, nil)
	e.seedSubmittedAt(t, "ghost2", "s1", t0)
	e.clk.Set(closes.Add(time.Hour))
	eqIDs(t, e.list(t, e.student, "closed"), "e1")
}

func TestCreatedFinalizePathAndNames(t *testing.T) {
	e := newEnv(t)
	f := &fakeFin{}
	e.svc.SetFinalizer(f)
	e.seedExam(t, "e1", "t1", "mcq", closes, t0)
	e.seedAttempt(t, "e1", "s1", store.AttemptInProgress, nil)
	e.seedAttempt(t, "e1", "p1", store.AttemptInProgress, nil)
	e.seedAttempt(t, "e1", "t1", store.AttemptNotStarted, nil)
	e.clk.Set(closes.Add(30 * time.Second))
	got := e.list(t, e.teacher, "created")
	if f.calls != 0 || got[0].Counts.InProgress != 2 || got[0].CreatedBy.Name != "t1" {
		t.Fatalf("grace: calls %d %+v", f.calls, got[0])
	}
	e.clk.Set(closes.Add(time.Minute))
	got = e.list(t, e.teacher, "created")
	if f.calls != 2 || got[0].Counts.Submitted != 2 || got[0].Counts.InProgress != 0 || got[0].Counts.NotStarted != 1 {
		t.Fatalf("calls %d %+v", f.calls, got[0].Counts)
	}
}

func TestListErrorsSurface(t *testing.T) {
	boom := errors.New("boom")
	e := newEnv(t)
	e.seedExam(t, "e1", "t1", "mcq", closes, t0)
	e.seedAttempt(t, "e1", "s1", store.AttemptInProgress, nil)
	e.seedAttempt(t, "e1", "t1", store.AttemptInProgress, nil)
	e.clk.Set(closes.Add(time.Minute))

	f := &fakeFin{err: boom}
	e.svc.SetFinalizer(f)
	if _, err := e.svc.List(context.Background(), e.student, "closed"); err == nil {
		t.Error("finalize error must surface (closed)")
	}
	if _, err := e.svc.List(context.Background(), e.teacher, "created"); err == nil {
		t.Error("finalize error must surface (created)")
	}
	ra := &recAttempts{AttemptStore: e.attempts, failByExam: boom}
	svc := exams.NewService(e.users, e.exams, ra, e.clk)
	if _, err := svc.List(context.Background(), e.teacher, "created"); err == nil {
		t.Error("ListByExam error must surface")
	}
	re := &recExams{ExamStore: e.exams, failGetExams: boom}
	svc = exams.NewService(e.users, re, e.attempts, e.clk)
	if _, err := svc.List(context.Background(), e.student, "open"); err == nil {
		t.Error("GetExams error must surface")
	}
}

func TestFinalizerGetsListNow(t *testing.T) {
	e := newEnv(t)
	var gotNow time.Time
	e.svc.SetFinalizer(nowFin{&gotNow})
	e.seedExam(t, "e1", "t1", "mcq", closes, t0)
	e.seedAttempt(t, "e1", "s1", store.AttemptInProgress, nil)
	e.clk.Set(closes.Add(time.Minute))
	e.list(t, e.student, "closed")
	if !gotNow.Equal(closes.Add(time.Minute)) {
		t.Fatalf("now %v", gotNow)
	}
}

type nowFin struct{ now *time.Time }

func (n nowFin) FinalizeIfExpired(_ context.Context, a store.Attempt, _ store.Exam, now time.Time) (store.Attempt, error) {
	*n.now = now
	return a, nil
}
