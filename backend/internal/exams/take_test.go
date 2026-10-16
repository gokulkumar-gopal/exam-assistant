package exams_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gokulkumar/exam-assistant/backend/internal/apperr"
	"github.com/gokulkumar/exam-assistant/backend/internal/clock"
	"github.com/gokulkumar/exam-assistant/backend/internal/exams"
	"github.com/gokulkumar/exam-assistant/backend/internal/store"
)

const secretModel = "SECRET-MODEL-TEXT"

// seedFull stores an exam with an MCQ or free-text question set (stored order q3, q1, q2).
func (e *env) seedFull(t *testing.T, id, creator, typ string) store.Exam {
	t.Helper()
	var qs []store.Question
	for i, qid := range []string{"q3", "q1", "q2"} {
		q := store.Question{ID: qid, Marks: 3 - i, Text: "text " + qid, SourceDocID: "d", SourceDocName: "B.pdf"}
		if typ == "mcq" {
			ci := i
			q.Options, q.CorrectIndex = []string{"o0", "o1", "o2", "o3"}, &ci
		} else {
			q.ModelAnswer = secretModel + "-" + qid
		}
		qs = append(qs, q)
	}
	x := store.Exam{ID: id, CreatorID: creator, Title: "T-" + id, Type: typ, TotalMarks: 6, ClosesAt: closes,
		MaxPeeks: 3, Questions: qs, CreatedAt: t0}
	if err := e.exams.PutExam(context.Background(), x); err != nil {
		t.Fatal(err)
	}
	return x
}

func (e *env) get(t *testing.T, u store.User, id string) exams.GetView {
	t.Helper()
	v, err := e.svc.Get(context.Background(), u, id)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	return v
}

func marshal(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// keyNames collects every object key at any depth, lowercased.
func keyNames(v any, out *[]string) {
	switch x := v.(type) {
	case map[string]any:
		for k, c := range x {
			*out = append(*out, strings.ToLower(k))
			keyNames(c, out)
		}
	case []any:
		for _, c := range x {
			keyNames(c, out)
		}
	}
}

func TestGetVisibleToCreatorAndNonDeclinedAssignee(t *testing.T) {
	e := newEnv(t)
	e.seedFull(t, "e1", "t1", "mcq")
	e.seedAttempt(t, "e1", "s1", store.AttemptNotStarted, nil)
	e.seedAttempt(t, "e1", "p1", store.AttemptDeclined, nil)
	if v := e.get(t, e.teacher, "e1"); v.Exam.ID != "e1" || len(v.Exam.Questions) != 3 {
		t.Fatalf("creator: %+v", v)
	}
	v := e.get(t, e.student, "e1")
	if v.Exam.ID != "e1" || len(v.Exam.Questions) != 3 || v.Attempt == nil || v.Attempt.Status != store.AttemptNotStarted {
		t.Fatalf("assignee: %+v", v)
	}
}

func TestStrangerAndDeclinedUserGet404(t *testing.T) {
	e := newEnv(t)
	e.seedFull(t, "e1", "t1", "mcq")
	e.seedAttempt(t, "e1", "p1", store.AttemptDeclined, nil)
	stranger := e.addUser(t, "o1", "o1@x.com", "student")
	ctx := context.Background()
	_, a := e.svc.Get(ctx, stranger, "e1")
	_, b := e.svc.Get(ctx, stranger, "nope-nope-1")
	_, c := e.svc.Get(ctx, e.parent, "e1")
	for _, err := range []error{a, b, c} {
		if err == nil {
			t.Fatal("want 404")
		}
		wantStatus(t, err, http.StatusNotFound)
	}
	ja, _ := json.Marshal(a.(*apperr.Error))
	jb, _ := json.Marshal(b.(*apperr.Error))
	jc, _ := json.Marshal(c.(*apperr.Error))
	if string(ja) != string(jb) || string(ja) != string(jc) || a.Error() != b.Error() || a.Error() != c.Error() {
		t.Fatalf("%s %s %s", ja, jb, jc)
	}
}

func TestAttemptNilForCreatorWithoutRow(t *testing.T) {
	e := newEnv(t)
	e.seedFull(t, "e1", "t1", "mcq")
	v := e.get(t, e.teacher, "e1")
	if v.Attempt != nil {
		t.Fatalf("attempt = %+v", v.Attempt)
	}
	if !strings.Contains(marshal(t, v), `"attempt":null`) {
		t.Fatal("attempt must marshal as null")
	}
}

func TestKeysAbsentWhileHidden(t *testing.T) {
	for _, typ := range []string{"mcq", "free"} {
		t.Run(typ, func(t *testing.T) {
			e := newEnv(t)
			e.seedFull(t, "e1", "t1", typ)
			e.seedAttempt(t, "e1", "s1", store.AttemptInProgress, nil)
			e.seedAttempt(t, "e1", "t1", store.AttemptInProgress, nil) // creator taking own exam
			for _, u := range []store.User{e.student, e.teacher} {
				v := e.get(t, u, "e1")
				if v.KeyVisible {
					t.Fatalf("%s: keyVisible", u.ID)
				}
				body := marshal(t, v)
				var generic any
				if err := json.Unmarshal([]byte(body), &generic); err != nil {
					t.Fatal(err)
				}
				var names []string
				keyNames(generic, &names)
				for _, n := range names {
					for _, bad := range []string{"correct", "model", "explanation", "solution", "isanswer"} {
						if strings.Contains(n, bad) {
							t.Fatalf("%s: key %q in %s", u.ID, n, body)
						}
					}
				}
				if strings.Contains(strings.ToLower(body), "correctindex") || strings.Contains(strings.ToLower(body), "modelanswer") ||
					strings.Contains(body, secretModel) {
					t.Fatalf("%s: leak in %s", u.ID, body)
				}
			}
		})
	}
}

func TestQuestionViewIsNotTheStoredType(t *testing.T) {
	rt := reflect.TypeOf(exams.QuestionView{})
	for i := 0; i < rt.NumField(); i++ {
		f := rt.Field(i)
		if f.Anonymous || f.Type == reflect.TypeOf(store.Question{}) {
			t.Fatalf("field %s embeds or holds the stored question", f.Name)
		}
	}
}

func TestKeysPresentWhenKeyVisible(t *testing.T) {
	e := newEnv(t)
	e.seedFull(t, "m1", "t1", "mcq")
	e.seedFull(t, "f1", "t1", "free")
	// creator with no row: key visible
	vm := e.get(t, e.teacher, "m1")
	vf := e.get(t, e.teacher, "f1")
	if !vm.KeyVisible || !vf.KeyVisible {
		t.Fatal("creator without row must see the key")
	}
	if !strings.Contains(marshal(t, vm), `"correctIndex":0`) { // q3 is first, index 0: a zero must still be present
		t.Fatalf("mcq key missing/zero dropped: %s", marshal(t, vm))
	}
	for i, q := range vm.Exam.Questions {
		if q.CorrectIndex == nil || *q.CorrectIndex != i {
			t.Fatalf("q%d: %+v", i, q)
		}
	}
	if !strings.Contains(marshal(t, vf), `"modelAnswer":"`+secretModel+`-q3"`) {
		t.Fatalf("free key missing: %s", marshal(t, vf))
	}
	// assignee after over
	e.seedAttempt(t, "m1", "s1", store.AttemptNotStarted, nil)
	if e.get(t, e.student, "m1").KeyVisible {
		t.Fatal("assignee before over")
	}
	e.clk.Set(closes.Add(time.Minute))
	v := e.get(t, e.student, "m1")
	if !v.KeyVisible || v.Exam.Questions[0].CorrectIndex == nil {
		t.Fatalf("assignee after over: %+v", v)
	}
}

func TestRevealedQuestionKeysIncludedWhileHidden(t *testing.T) {
	e := newEnv(t)
	e.seedFull(t, "m1", "t1", "mcq")
	e.seedFull(t, "f1", "t1", "free")
	for _, id := range []string{"m1", "f1"} {
		e.seedAttempt(t, id, "s1", store.AttemptInProgress, nil)
		if _, err := e.attempts.Reveal(context.Background(), id, "s1", "q1", 3, t0); err != nil {
			t.Fatal(err)
		}
	}
	v := e.get(t, e.student, "m1")
	if v.KeyVisible {
		t.Fatal("hidden")
	}
	for _, q := range v.Exam.Questions {
		if q.ID == "q1" {
			if q.CorrectIndex == nil || *q.CorrectIndex != 1 {
				t.Fatalf("revealed q1: %+v", q)
			}
		} else if q.CorrectIndex != nil || q.ModelAnswer != nil {
			t.Fatalf("unrevealed %s leaks: %+v", q.ID, q)
		}
	}
	if len(v.Attempt.Revealed) != 1 || v.Attempt.Revealed[0].QuestionID != "q1" || *v.Attempt.Revealed[0].CorrectIndex != 1 || v.Attempt.PeeksLeft != 2 {
		t.Fatalf("attempt.revealed %+v peeksLeft %d", v.Attempt.Revealed, v.Attempt.PeeksLeft)
	}
	body := marshal(t, e.get(t, e.student, "f1"))
	if !strings.Contains(body, secretModel+"-q1") || strings.Contains(body, secretModel+"-q2") || strings.Contains(body, secretModel+"-q3") {
		t.Fatalf("free reveal: %s", body)
	}
}

func TestResumeReturnsAnswersLastIndexSavedAt(t *testing.T) {
	e := newEnv(t)
	e.seedFull(t, "e1", "t1", "mcq")
	e.seedAttempt(t, "e1", "s1", store.AttemptNotStarted, nil)
	saved := t0.Add(5 * time.Minute)
	if err := e.attempts.Save(context.Background(), "e1", "s1", map[string]string{"q1": "o1", "q2": "o2"}, 2, saved); err != nil {
		t.Fatal(err)
	}
	a := e.get(t, e.student, "e1").Attempt
	if a == nil || a.Status != store.AttemptInProgress || a.Answers["q1"] != "o1" || a.Answers["q2"] != "o2" ||
		a.LastQuestionIndex != 2 || a.SavedAt == nil || !a.SavedAt.Equal(saved) {
		t.Fatalf("%+v", a)
	}
	e.seedAttempt(t, "e1", "p1", store.AttemptNotStarted, nil)
	fresh := e.get(t, e.parent, "e1").Attempt
	if fresh.Answers == nil || fresh.Revealed == nil || fresh.SavedAt != nil {
		t.Fatalf("fresh attempt: %+v", fresh)
	}
	body := marshal(t, e.get(t, e.parent, "e1"))
	if !strings.Contains(body, `"answers":{}`) || !strings.Contains(body, `"revealed":[]`) {
		t.Fatalf("empty collections must marshal as {} and []: %s", body)
	}
}

func TestGetIncludesAcceptingUntilMaxPeeksCreatedBy(t *testing.T) {
	e := newEnv(t)
	e.seedFull(t, "e1", "t1", "mcq")
	e.seedAttempt(t, "e1", "s1", store.AttemptNotStarted, nil)
	v := e.get(t, e.student, "e1")
	x := v.Exam
	if !x.AcceptingUntil.Equal(closes.Add(time.Minute)) || !x.ClosesAt.Equal(closes) || x.MaxPeeks != 3 ||
		x.CreatedBy != (exams.UserRef{ID: "t1", Name: "t1"}) || x.TotalMarks != 6 || x.Title != "T-e1" || x.Type != "mcq" {
		t.Fatalf("%+v", x)
	}
	if strings.Contains(marshal(t, v), "t1@x.com") {
		t.Fatal("creator email leaked")
	}
	if q := x.Questions[0]; q.ID != "q3" || q.Marks != 3 || q.Text != "text q3" || len(q.Options) != 4 {
		t.Fatalf("%+v", q)
	}
}

// Backend-spec 9.5: reading an exam, as the print view does, starts nothing and charges nothing.
// The recorders implement every store method by hand (no embedding): reads are listed in an
// explicit allow-list, every other method counts as a write, and a method added to the interface
// later fails to compile here until it is classified.
type getRecAttempts struct {
	in     store.AttemptStore
	writes atomic.Int32
	names  []string
}

func (r *getRecAttempts) w(name string) { r.writes.Add(1); r.names = append(r.names, name) }
func (r *getRecAttempts) PutAttemptIfAbsent(ctx context.Context, a store.Attempt) (bool, error) {
	r.w("PutAttemptIfAbsent")
	return r.in.PutAttemptIfAbsent(ctx, a)
}
func (r *getRecAttempts) GetAttempt(ctx context.Context, e, u string) (store.Attempt, error) {
	return r.in.GetAttempt(ctx, e, u) // read
}
func (r *getRecAttempts) ListByUser(ctx context.Context, u string) ([]store.AttemptSummary, error) {
	return r.in.ListByUser(ctx, u) // read
}
func (r *getRecAttempts) ListByExam(ctx context.Context, e string) ([]store.Attempt, error) {
	return r.in.ListByExam(ctx, e) // read
}
func (r *getRecAttempts) Save(ctx context.Context, e, u string, a map[string]string, i int, n time.Time) error {
	r.w("Save")
	return r.in.Save(ctx, e, u, a, i, n)
}
func (r *getRecAttempts) Submit(ctx context.Context, a store.Attempt) error {
	r.w("Submit")
	return r.in.Submit(ctx, a)
}
func (r *getRecAttempts) Decline(ctx context.Context, e, u string) error {
	r.w("Decline")
	return r.in.Decline(ctx, e, u)
}
func (r *getRecAttempts) Reveal(ctx context.Context, e, u, q string, m int, n time.Time) (store.RevealResult, error) {
	r.w("Reveal")
	return r.in.Reveal(ctx, e, u, q, m, n)
}

type getRecExams struct {
	in     store.ExamStore
	writes atomic.Int32
}

func (r *getRecExams) PutExam(ctx context.Context, x store.Exam) error {
	r.writes.Add(1)
	return r.in.PutExam(ctx, x)
}
func (r *getRecExams) GetExam(ctx context.Context, id string) (store.Exam, error) {
	return r.in.GetExam(ctx, id) // read
}
func (r *getRecExams) GetExams(ctx context.Context, ids []string) (map[string]store.Exam, error) {
	return r.in.GetExams(ctx, ids) // read
}
func (r *getRecExams) ListByCreator(ctx context.Context, c string, n int) ([]store.ExamSummary, error) {
	return r.in.ListByCreator(ctx, c, n) // read
}

// noopFin is the carve-out marker: lazy finalize is the one write a GET may make (spec 9.2), and
// it goes through the Finalizer, not through Get. A no-op finalizer isolates what Get itself writes.
type noopFin struct{ calls atomic.Int32 }

func (f *noopFin) FinalizeIfExpired(_ context.Context, a store.Attempt, _ store.Exam, _ time.Time) (store.Attempt, error) {
	f.calls.Add(1)
	return a, nil
}

func TestGetDoesNotStartAttemptOrChargePeek(t *testing.T) {
	e := newEnv(t)
	e.seedFull(t, "e1", "t1", "mcq")
	e.seedAttempt(t, "e1", "s1", store.AttemptNotStarted, nil)
	ra := &getRecAttempts{in: e.attempts}
	rx := &getRecExams{in: e.exams}
	clk := clock.NewFake(t0)
	svc := exams.NewService(e.users, rx, ra, clk)
	svc.SetFinalizer(&noopFin{})
	ctx := context.Background()
	for _, u := range []store.User{e.student, e.teacher, e.student} {
		if _, err := svc.Get(ctx, u, "e1"); err != nil {
			t.Fatal(err)
		}
	}
	if ra.writes.Load() != 0 || rx.writes.Load() != 0 {
		t.Fatalf("writes: attempts %d %v exams %d", ra.writes.Load(), ra.names, rx.writes.Load())
	}
	// after the exam is over, with an in_progress row and a creator row-less read: still no Get writes
	e.seedAttempt(t, "e1", "p1", store.AttemptInProgress, nil)
	clk.Set(closes.Add(time.Hour))
	for _, u := range []store.User{e.student, e.teacher, e.parent} {
		if _, err := svc.Get(ctx, u, "e1"); err != nil {
			t.Fatal(err)
		}
	}
	if ra.writes.Load() != 0 || rx.writes.Load() != 0 {
		t.Fatalf("post-over writes: attempts %d %v exams %d", ra.writes.Load(), ra.names, rx.writes.Load())
	}
	a, err := e.attempts.GetAttempt(ctx, "e1", "s1")
	if err != nil || a.Status != store.AttemptNotStarted || a.StartedAt != nil || len(a.Revealed) != 0 {
		t.Fatalf("attempt changed: %+v %v", a, err)
	}
	if _, err := e.attempts.GetAttempt(ctx, "e1", "t1"); err != store.ErrNotFound {
		t.Fatalf("creator row created: %v", err)
	}
}

func TestClosedExamStillReturnsQuestionsToCreatorAndAssignees(t *testing.T) {
	e := newEnv(t)
	e.seedFull(t, "e1", "t1", "mcq")
	e.seedAttempt(t, "e1", "s1", store.AttemptNotStarted, nil)
	e.seedAttempt(t, "e1", "p1", store.AttemptDeclined, nil)
	e.clk.Set(closes.Add(24 * time.Hour))
	for _, u := range []store.User{e.teacher, e.student} {
		v := e.get(t, u, "e1")
		if len(v.Exam.Questions) != 3 || !v.KeyVisible {
			t.Fatalf("%s: %+v", u.ID, v)
		}
	}
	if _, err := e.svc.Get(context.Background(), e.parent, "e1"); err == nil {
		t.Fatal("declined user must still get 404 after close")
	}
}

func TestQuestionsInStoredOrder(t *testing.T) {
	e := newEnv(t)
	e.seedFull(t, "e1", "t1", "mcq")
	var got []string
	for _, q := range e.get(t, e.teacher, "e1").Exam.Questions {
		got = append(got, q.ID)
	}
	if strings.Join(got, ",") != "q3,q1,q2" {
		t.Fatalf("order %v", got)
	}
}

func TestCreatorWhoIsAssigneeFollowsOwnAttempt(t *testing.T) {
	e := newEnv(t)
	e.seedFull(t, "e1", "t1", "mcq")
	e.seedAttempt(t, "e1", "t1", store.AttemptInProgress, nil)
	v := e.get(t, e.teacher, "e1")
	if v.KeyVisible || v.Attempt == nil || v.Exam.Questions[0].CorrectIndex != nil {
		t.Fatalf("%+v", v)
	}
}

func TestGetRunsLazyFinalizeOnOwnExpiredAttempt(t *testing.T) {
	e := newEnv(t)
	e.seedFull(t, "e1", "t1", "mcq")
	e.seedAttempt(t, "e1", "s1", store.AttemptInProgress, nil)
	f := &fakeFin{}
	e.svc.SetFinalizer(f)
	// before acceptingUntil: not called
	e.clk.Set(closes.Add(time.Minute - time.Nanosecond))
	if v := e.get(t, e.student, "e1"); atomic.LoadInt32(&f.calls) != 0 || v.Attempt.Status != store.AttemptInProgress {
		t.Fatalf("early: calls %d %+v", f.calls, v.Attempt)
	}
	e.clk.Set(closes.Add(time.Minute))
	v := e.get(t, e.student, "e1")
	if atomic.LoadInt32(&f.calls) != 1 || v.Attempt.Status != store.AttemptSubmitted {
		t.Fatalf("calls %d attempt %+v", f.calls, v.Attempt)
	}
	// not_started and submitted rows are not finalized
	e.seedAttempt(t, "e1", "p1", store.AttemptNotStarted, nil)
	e.get(t, e.parent, "e1")
	if atomic.LoadInt32(&f.calls) != 1 {
		t.Fatalf("not_started finalized: %d", f.calls)
	}
	// creator without a row: not called, attempt nil
	if v := e.get(t, e.teacher, "e1"); v.Attempt != nil || atomic.LoadInt32(&f.calls) != 1 {
		t.Fatalf("creator: %+v calls %d", v.Attempt, f.calls)
	}
}

func TestGetFinalizeErrorIs500AndNilFinalizerIsAsStored(t *testing.T) {
	e := newEnv(t)
	e.seedFull(t, "e1", "t1", "mcq")
	e.seedAttempt(t, "e1", "s1", store.AttemptInProgress, nil)
	e.clk.Set(closes.Add(time.Hour))
	if v := e.get(t, e.student, "e1"); v.Attempt.Status != store.AttemptInProgress {
		t.Fatalf("nil finalizer: %+v", v.Attempt)
	}
	e.svc.SetFinalizer(&fakeFin{err: errors.New("boom")})
	_, err := e.svc.Get(context.Background(), e.student, "e1")
	wantStatus(t, err, http.StatusInternalServerError)
}

func TestGetByCreatorWithDeclinedOwnRowIs200KeyVisible(t *testing.T) {
	e := newEnv(t)
	e.seedFull(t, "e1", "t1", "mcq")
	e.seedAttempt(t, "e1", "t1", store.AttemptDeclined, nil)
	v := e.get(t, e.teacher, "e1")
	if !v.KeyVisible || v.Attempt == nil || v.Attempt.Status != store.AttemptDeclined || v.Exam.Questions[0].CorrectIndex == nil {
		t.Fatalf("%+v", v)
	}
}

// Visible keys are pinned by JSON key set: an MCQ question never carries modelAnswer, a free-text
// question never carries correctIndex or options.
func TestVisibleQuestionKeySets(t *testing.T) {
	e := newEnv(t)
	e.seedFull(t, "m1", "t1", "mcq")
	e.seedFull(t, "f1", "t1", "free")
	keys := func(id string) []map[string]any {
		var out struct {
			Exam struct{ Questions []map[string]any }
		}
		if err := json.Unmarshal([]byte(marshal(t, e.get(t, e.teacher, id))), &out); err != nil {
			t.Fatal(err)
		}
		return out.Exam.Questions
	}
	for _, q := range keys("m1") {
		if len(q) != 5 || q["correctIndex"] == nil || q["options"] == nil || q["id"] == nil || q["marks"] == nil || q["text"] == nil {
			t.Fatalf("mcq keys %v", q)
		}
	}
	for _, q := range keys("f1") {
		if len(q) != 4 || q["modelAnswer"] == nil || q["id"] == nil || q["marks"] == nil || q["text"] == nil {
			t.Fatalf("free keys %v", q)
		}
	}
}
