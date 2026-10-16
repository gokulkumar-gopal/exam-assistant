package exams_test

import (
	"context"
	"encoding/json"
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
type getRecAttempts struct {
	store.AttemptStore
	writes atomic.Int32
}

func (r *getRecAttempts) PutAttemptIfAbsent(ctx context.Context, a store.Attempt) (bool, error) {
	r.writes.Add(1)
	return r.AttemptStore.PutAttemptIfAbsent(ctx, a)
}
func (r *getRecAttempts) Save(ctx context.Context, e, u string, a map[string]string, i int, n time.Time) error {
	r.writes.Add(1)
	return r.AttemptStore.Save(ctx, e, u, a, i, n)
}
func (r *getRecAttempts) Submit(ctx context.Context, a store.Attempt) error {
	r.writes.Add(1)
	return r.AttemptStore.Submit(ctx, a)
}
func (r *getRecAttempts) Decline(ctx context.Context, e, u string) error {
	r.writes.Add(1)
	return r.AttemptStore.Decline(ctx, e, u)
}
func (r *getRecAttempts) Reveal(ctx context.Context, e, u, q string, m int, n time.Time) (store.RevealResult, error) {
	r.writes.Add(1)
	return r.AttemptStore.Reveal(ctx, e, u, q, m, n)
}

type getRecExams struct {
	store.ExamStore
	writes atomic.Int32
}

func (r *getRecExams) PutExam(ctx context.Context, x store.Exam) error {
	r.writes.Add(1)
	return r.ExamStore.PutExam(ctx, x)
}

func TestGetDoesNotStartAttemptOrChargePeek(t *testing.T) {
	e := newEnv(t)
	e.seedFull(t, "e1", "t1", "mcq")
	e.seedAttempt(t, "e1", "s1", store.AttemptNotStarted, nil)
	ra := &getRecAttempts{AttemptStore: e.attempts}
	rx := &getRecExams{ExamStore: e.exams}
	svc := exams.NewService(e.users, rx, ra, clock.NewFake(t0))
	ctx := context.Background()
	for _, u := range []store.User{e.student, e.teacher, e.student} {
		if _, err := svc.Get(ctx, u, "e1"); err != nil {
			t.Fatal(err)
		}
	}
	if ra.writes.Load() != 0 || rx.writes.Load() != 0 {
		t.Fatalf("writes: attempts %d exams %d", ra.writes.Load(), rx.writes.Load())
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
