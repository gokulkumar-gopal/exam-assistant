package exams_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"testing"
	"time"

	"github.com/gokulkumar/exam-assistant/backend/internal/apperr"
	"github.com/gokulkumar/exam-assistant/backend/internal/exams"
	"github.com/gokulkumar/exam-assistant/backend/internal/store"
)

func (e *env) status(t *testing.T, examID, uid string) string {
	t.Helper()
	a, err := e.attempts.GetAttempt(context.Background(), examID, uid)
	if err != nil {
		t.Fatal(err)
	}
	return a.Status
}

// assigned creates a teacher exam assigned to s1.
func (e *env) assigned(t *testing.T) string {
	in := input(1)
	in.AssigneeEmails = []string{"s1@x.com"}
	return e.create(t, e.teacher, in, "exam-0001").ID
}

func TestDeclineFromNotStarted204Idempotent(t *testing.T) {
	e := newEnv(t)
	id := e.assigned(t)
	for i := 0; i < 2; i++ {
		if err := e.svc.Decline(context.Background(), e.student, id); err != nil {
			t.Fatalf("decline %d: %v", i, err)
		}
	}
	if got := e.status(t, id, "s1"); got != store.AttemptDeclined {
		t.Fatalf("status %q", got)
	}
}

func TestDeclineInProgressOrSubmitted409(t *testing.T) {
	e := newEnv(t)
	id := e.assigned(t)
	ctx := context.Background()
	if err := e.attempts.Save(ctx, id, "s1", map[string]string{"a": "b"}, 0, t0); err != nil {
		t.Fatal(err)
	}
	wantStatus(t, e.svc.Decline(ctx, e.student, id), http.StatusConflict)
	if got := e.status(t, id, "s1"); got != store.AttemptInProgress {
		t.Fatalf("status %q", got)
	}
	s2 := e.addUser(t, "s2", "s2@x.com", "student")
	in := input(1)
	in.AssigneeEmails = []string{"s2@x.com"}
	id2 := e.create(t, e.teacher, in, "exam-0002").ID
	if err := e.attempts.Save(ctx, id2, "s2", map[string]string{}, 0, t0); err != nil {
		t.Fatal(err)
	}
	if err := e.attempts.Submit(ctx, store.Attempt{ExamID: id2, UserID: "s2", Status: store.AttemptSubmitted, Answers: map[string]string{}, Revealed: []string{}}); err != nil {
		t.Fatal(err)
	}
	wantStatus(t, e.svc.Decline(ctx, s2, id2), http.StatusConflict)
	if got := e.status(t, id2, "s2"); got != store.AttemptSubmitted {
		t.Fatalf("status %q", got)
	}
}

func TestDeclineByNonAssignee404(t *testing.T) {
	e := newEnv(t)
	id := e.assigned(t)
	ctx := context.Background()
	other := e.svc.Decline(ctx, e.parent, id)
	missing := e.svc.Decline(ctx, e.parent, "nope-nope-1")
	wantStatus(t, other, http.StatusNotFound)
	wantStatus(t, missing, http.StatusNotFound)
	var a, b *apperr.Error
	errors.As(other, &a)
	errors.As(missing, &b)
	if a.Message != b.Message || a.Code != b.Code {
		t.Fatal("foreign and missing differ")
	}
	// the creator has no row on an assigned exam: 404 too
	wantStatus(t, e.svc.Decline(ctx, e.teacher, id), http.StatusNotFound)
	if got := e.status(t, id, "s1"); got != store.AttemptNotStarted {
		t.Fatalf("status %q", got)
	}
}

// B-4.2 asserts the 404 on GET /exams/{id}; here the stored status it relies on is checked, and
// that the list sees nothing.
func TestDeclinedUserGets404OnGetExam(t *testing.T) {
	e := newEnv(t)
	id := e.assigned(t)
	ctx := context.Background()
	if err := e.svc.Decline(ctx, e.student, id); err != nil {
		t.Fatal(err)
	}
	if got := e.status(t, id, "s1"); got != store.AttemptDeclined {
		t.Fatalf("status %q", got)
	}
	items, err := e.svc.List(ctx, e.student, "open")
	if err != nil || len(items) != 0 {
		t.Fatalf("open list %v %v", items, err)
	}
}

func TestAssignCreatorOnly(t *testing.T) {
	e := newEnv(t)
	id := e.assigned(t)
	e.addUser(t, "s2", "s2@x.com", "student")
	ctx := context.Background()
	_, other := e.svc.Assign(ctx, e.student, id, []string{"s2@x.com"})
	_, missing := e.svc.Assign(ctx, e.student, "nope-nope-1", []string{"s2@x.com"})
	wantStatus(t, other, http.StatusNotFound)
	wantStatus(t, missing, http.StatusNotFound)
	var a, b *apperr.Error
	errors.As(other, &a)
	errors.As(missing, &b)
	if a.Message != b.Message || a.Code != b.Code {
		t.Fatal("foreign and missing differ")
	}
	if len(e.rows(t, id)) != 1 {
		t.Fatal("rows written")
	}
}

func TestAssignAfterAcceptingUntil409ExamClosed(t *testing.T) {
	e := newEnv(t)
	id := e.assigned(t)
	e.addUser(t, "s2", "s2@x.com", "student")
	x := e.exam(t, id)
	ctx := context.Background()
	e.clk.Set(x.ClosesAt.Add(60*time.Second - time.Nanosecond))
	if _, err := e.svc.Assign(ctx, e.teacher, id, []string{"s2@x.com"}); err != nil {
		t.Fatalf("inside grace: %v", err)
	}
	e.addUser(t, "s3", "s3@x.com", "student")
	e.clk.Set(x.ClosesAt.Add(60 * time.Second))
	_, err := e.svc.Assign(ctx, e.teacher, id, []string{"s3@x.com"})
	wantStatus(t, err, http.StatusConflict)
	var ae *apperr.Error
	errors.As(err, &ae)
	if ae.Code != "exam_closed" {
		t.Fatalf("code %q", ae.Code)
	}
	if len(e.rows(t, id)) != 2 {
		t.Fatal("rows written after close")
	}
}

func (e *env) students(t *testing.T, from, n int) []string {
	var out []string
	for i := from; i < from+n; i++ {
		em := fmt.Sprintf("st%d@x.com", i)
		e.addUser(t, fmt.Sprintf("st%d", i), em, "student")
		out = append(out, em)
	}
	return out
}

func TestAssignRoleRulesAndCapCountAllAttemptRows(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	// student creator: 9 rows (one declined) + 2 new = 11 -> 422; one new = 10 ok
	all := e.students(t, 0, 12)
	in := input(1)
	in.AssigneeEmails = all[:9]
	id := e.create(t, e.student, in, "exam-stud1").ID
	if err := e.svc.Decline(ctx, store.User{ID: "st0"}, id); err != nil {
		t.Fatal(err)
	}
	_, err := e.svc.Assign(ctx, e.student, id, all[9:11])
	wantField(t, err, "emails")
	if len(e.rows(t, id)) != 9 {
		t.Fatal("rows written on 422")
	}
	if r, err := e.svc.Assign(ctx, e.student, id, all[9:10]); err != nil || len(r.Assigned) != 1 {
		t.Fatalf("%+v %v", r, err)
	}
	_, err = e.svc.Assign(ctx, e.student, id, all[10:11])
	wantField(t, err, "emails")
	// the student creator's own practice row does not count
	in2 := input(1)
	id2 := e.create(t, e.student, in2, "exam-stud2").ID
	more := e.students(t, 100, 10)
	if r, err := e.svc.Assign(ctx, e.student, id2, more); err != nil || len(r.Assigned) != 10 {
		t.Fatalf("practice row counted: %+v %v", r, err)
	}
	// teacher at 50 (declined included)
	e2 := newEnv(t)
	ts := e2.students(t, 0, 52)
	tin := input(1)
	tin.AssigneeEmails = ts[:50]
	tid := e2.create(t, e2.teacher, tin, "exam-teach1").ID
	if err := e2.svc.Decline(ctx, store.User{ID: "st0"}, tid); err != nil {
		t.Fatal(err)
	}
	_, err = e2.svc.Assign(ctx, e2.teacher, tid, ts[50:51])
	wantField(t, err, "emails")
	if r, err := e2.svc.Assign(ctx, e2.teacher, tid, ts[:5]); err != nil || len(r.AlreadyAssigned) != 5 {
		t.Fatalf("existing re-listed at cap: %+v %v", r, err)
	}
	_, err = e2.svc.Assign(ctx, e2.teacher, tid, ts[:51])
	wantField(t, err, "emails")
	// input itself above the cap, empty and overlong
	_, err = e2.svc.Assign(ctx, e2.teacher, tid, ts)
	wantField(t, err, "emails")
	_, err = e2.svc.Assign(ctx, e2.teacher, tid, nil)
	wantField(t, err, "emails")
	_, err = e2.svc.Assign(ctx, e2.teacher, tid, []string{string(make([]byte, 255))})
	wantField(t, err, "emails")
	if len(e2.rows(t, tid)) != 50 {
		t.Fatal("rows changed")
	}
}

func TestAssignAlreadyAssignedListed(t *testing.T) {
	e := newEnv(t)
	id := e.assigned(t)
	e.addUser(t, "s2", "s2@x.com", "student")
	ctx := context.Background()
	if err := e.attempts.Save(ctx, id, "s1", map[string]string{"a": "b"}, 0, t0); err != nil {
		t.Fatal(err)
	}
	r, err := e.svc.Assign(ctx, e.teacher, id, []string{" S1@X.com ", "s2@x.com", "s2@X.COM", "ghost@x.com"})
	if err != nil {
		t.Fatal(err)
	}
	want := exams.AssignResult{Assigned: []string{"s2@x.com"}, AlreadyAssigned: []string{"S1@X.com"}, Unknown: []string{"ghost@x.com"}}
	if !reflect.DeepEqual(r, want) {
		t.Fatalf("%+v", r)
	}
	if got := e.status(t, id, "s1"); got != store.AttemptInProgress {
		t.Fatalf("existing row touched: %q", got)
	}
	r, err = e.svc.Assign(ctx, e.teacher, id, []string{"s2@x.com"})
	if err != nil || r.Assigned == nil || r.Unknown == nil || len(r.Assigned) != 0 || len(r.AlreadyAssigned) != 1 {
		t.Fatalf("%+v %v", r, err)
	}
}

func TestAssignUnknownIncludesNotAllowedForStudentCreator(t *testing.T) {
	e := newEnv(t)
	id := e.create(t, e.student, input(1), "exam-stud1").ID
	ctx := context.Background()
	r, err := e.svc.Assign(ctx, e.student, id, []string{"t1@x.com", "ghost@x.com", "p1@x.com"})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(r.Unknown, []string{"t1@x.com", "ghost@x.com", "p1@x.com"}) || len(r.Assigned) != 0 || r.AlreadyAssigned == nil {
		t.Fatalf("%+v", r)
	}
	// the same emails for a teacher creator are assignable
	tid := e.create(t, e.teacher, input(1), "exam-teach1").ID
	r, err = e.svc.Assign(ctx, e.teacher, tid, []string{"s1@x.com", "ghost@x.com"})
	if err != nil || len(r.Assigned) != 1 || !reflect.DeepEqual(r.Unknown, []string{"ghost@x.com"}) {
		t.Fatalf("%+v %v", r, err)
	}
}

func TestAssignAfterDeclineStaysDeclined(t *testing.T) {
	e := newEnv(t)
	id := e.assigned(t)
	ctx := context.Background()
	if err := e.svc.Decline(ctx, e.student, id); err != nil {
		t.Fatal(err)
	}
	r, err := e.svc.Assign(ctx, e.teacher, id, []string{"s1@x.com"})
	if err != nil || !reflect.DeepEqual(r.AlreadyAssigned, []string{"s1@x.com"}) || len(r.Assigned) != 0 {
		t.Fatalf("%+v %v", r, err)
	}
	if got := e.status(t, id, "s1"); got != store.AttemptDeclined {
		t.Fatalf("status %q", got)
	}
}

func TestAssignPartialFailure503ThenRetryCompletes(t *testing.T) {
	e := newEnv(t)
	id := e.assigned(t)
	ts := e.students(t, 0, 3)
	d := &dropRows{AttemptStore: e.attempts}
	svc := exams.NewService(e.users, e.exams, d, e.clk)
	d.fail.Store(true)
	_, err := svc.Assign(context.Background(), e.teacher, id, ts)
	wantStatus(t, err, http.StatusServiceUnavailable)
	d.fail.Store(false)
	r, err := svc.Assign(context.Background(), e.teacher, id, ts)
	if err != nil || len(r.Assigned)+len(r.AlreadyAssigned) != 3 {
		t.Fatalf("%+v %v", r, err)
	}
	if len(e.rows(t, id)) != 4 {
		t.Fatalf("rows %v", e.rows(t, id))
	}
}

func TestDeclineAfterDeadline(t *testing.T) {
	e := newEnv(t)
	id := e.assigned(t)
	in := input(1)
	in.AssigneeEmails = []string{"s1@x.com"}
	id2 := e.create(t, e.teacher, in, "exam-0002").ID
	ctx := context.Background()
	au := e.exam(t, id).ClosesAt.Add(60 * time.Second)

	// already declined stays an idempotent 204 after the deadline
	if err := e.svc.Decline(ctx, e.student, id2); err != nil {
		t.Fatal(err)
	}
	e.clk.Set(au.Add(-time.Nanosecond))
	if err := e.svc.Decline(ctx, e.student, id); err != nil {
		t.Fatalf("1ns before acceptingUntil: %v", err)
	}
	if got := e.status(t, id, "s1"); got != store.AttemptDeclined {
		t.Fatalf("status %q", got)
	}

	id3 := func() string {
		in := input(1)
		in.AssigneeEmails = []string{"s1@x.com"}
		e.clk.Set(t0)
		x := e.create(t, e.teacher, in, "exam-0003")
		return x.ID
	}()
	e.clk.Set(e.exam(t, id3).ClosesAt.Add(60 * time.Second))
	err := e.svc.Decline(ctx, e.student, id3)
	wantStatus(t, err, http.StatusConflict)
	var ae *apperr.Error
	errors.As(err, &ae)
	if ae.Code != "exam_closed" {
		t.Fatalf("code %q", ae.Code)
	}
	if got := e.status(t, id3, "s1"); got != store.AttemptNotStarted {
		t.Fatalf("missed attempt rewritten: %q", got)
	}
	e.clk.Set(au.Add(time.Hour))
	if err := e.svc.Decline(ctx, e.student, id2); err != nil {
		t.Fatalf("already declined after deadline: %v", err)
	}
	// missing and foreign stay identical after the deadline
	a := e.svc.Decline(ctx, e.parent, id3)
	b := e.svc.Decline(ctx, e.parent, "nope-nope-1")
	wantStatus(t, a, http.StatusNotFound)
	if a.Error() != b.Error() {
		t.Fatalf("%v vs %v", a, b)
	}
}

func TestAssignNonCreatorInvalidInputIs404(t *testing.T) {
	e := newEnv(t)
	id := e.assigned(t)
	_, err := e.svc.Assign(context.Background(), e.student, id, nil)
	wantStatus(t, err, http.StatusNotFound)
}

func TestAssignNilWritesNoRow(t *testing.T) {
	e := newEnv(t)
	id := e.assigned(t)
	_, err := e.svc.Assign(context.Background(), e.teacher, id, nil)
	wantField(t, err, "emails")
	if len(e.rows(t, id)) != 1 {
		t.Fatalf("rows %v", e.rows(t, id))
	}
}

func TestAssignedEmailsKeepCaseAndCreatorSelfAssign(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	id := e.assigned(t)
	e.addUser(t, "s2", "s2@x.com", "student")
	r, err := e.svc.Assign(ctx, e.teacher, id, []string{" S2@X.com "})
	if err != nil || !reflect.DeepEqual(r.Assigned, []string{"S2@X.com"}) {
		t.Fatalf("%+v %v", r, err)
	}
	// teacher assigns themself on an assigned exam: a new row, reported as assigned
	r, err = e.svc.Assign(ctx, e.teacher, id, []string{"T1@x.com"})
	if err != nil || !reflect.DeepEqual(r.Assigned, []string{"T1@x.com"}) {
		t.Fatalf("%+v %v", r, err)
	}
	// student creator, practice exam at 10 students: re-listing the creator and existing
	// assignees writes nothing and is not a cap error
	all := e.students(t, 0, 10)
	pid := e.create(t, e.student, input(1), "exam-stud9").ID
	if _, err := e.svc.Assign(ctx, e.student, pid, all); err != nil {
		t.Fatal(err)
	}
	r, err = e.svc.Assign(ctx, e.student, pid, append([]string{"s1@x.com"}, all[:3]...))
	if err != nil || len(r.Assigned) != 0 || len(r.AlreadyAssigned) != 4 {
		t.Fatalf("%+v %v", r, err)
	}
	// a creator adding themself counts when they have no row yet
	sid := e.create(t, e.student, func() exams.CreateInput {
		in := input(1)
		in.AssigneeEmails = all[:10]
		return in
	}(), "exam-stud8").ID
	_, err = e.svc.Assign(ctx, e.student, sid, []string{"s1@x.com"})
	wantField(t, err, "emails")
}
