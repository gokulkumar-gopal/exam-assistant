package exams_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gokulkumar/exam-assistant/backend/internal/apperr"
	"github.com/gokulkumar/exam-assistant/backend/internal/clock"
	"github.com/gokulkumar/exam-assistant/backend/internal/docs"
	"github.com/gokulkumar/exam-assistant/backend/internal/exams"
	"github.com/gokulkumar/exam-assistant/backend/internal/store"
	"github.com/gokulkumar/exam-assistant/backend/internal/store/memory"
)

var t0 = time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)

type env struct {
	svc      *exams.Service
	users    *memory.UserStore
	exams    *memory.ExamStore
	attempts *memory.AttemptStore
	clk      *clock.Fake
	teacher  store.User
	student  store.User
	parent   store.User
}

func (e *env) addUser(t *testing.T, id, email, role string) store.User {
	t.Helper()
	u := store.User{ID: id, Name: id, Email: email, Role: role, CreatedAt: t0}
	if err := e.users.CreateUser(context.Background(), u); err != nil {
		t.Fatal(err)
	}
	return u
}

func newEnv(t *testing.T) *env {
	t.Helper()
	e := &env{users: memory.NewUserStore(), exams: memory.NewExamStore(), attempts: memory.NewAttemptStore(), clk: clock.NewFake(t0)}
	e.svc = exams.NewService(e.users, e.exams, e.attempts, e.clk)
	e.teacher = e.addUser(t, "t1", "t1@x.com", "teacher")
	e.student = e.addUser(t, "s1", "s1@x.com", "student")
	e.parent = e.addUser(t, "p1", "p1@x.com", "parent")
	return e
}

func mcq(text string) exams.CreateQuestion {
	ci := 2
	return exams.CreateQuestion{Marks: 1, Text: text, Options: []string{"a", "b", "c", "d"}, CorrectIndex: &ci, SourceDocID: "d1", SourceDocName: "Bio.pdf"}
}

func input(n int) exams.CreateInput {
	secs := int64(172800)
	in := exams.CreateInput{Title: "Cells", Type: "mcq", ClosesInSeconds: &secs}
	for i := 0; i < n; i++ {
		in.Questions = append(in.Questions, mcq(fmt.Sprintf("q%d", i)))
	}
	return in
}

func wantField(t *testing.T, err error, field string) {
	t.Helper()
	var ae *apperr.Error
	if !errors.As(err, &ae) || ae.Status != http.StatusUnprocessableEntity {
		t.Fatalf("want 422, got %v", err)
	}
	if _, ok := ae.Fields[field]; !ok {
		t.Fatalf("want field %q in %v", field, ae.Fields)
	}
}

func wantStatus(t *testing.T, err error, status int) {
	t.Helper()
	var ae *apperr.Error
	if !errors.As(err, &ae) || ae.Status != status {
		t.Fatalf("want %d, got %v", status, err)
	}
}

func (e *env) create(t *testing.T, u store.User, in exams.CreateInput, key string) exams.CreateResult {
	t.Helper()
	r, err := e.svc.Create(context.Background(), u, in, key)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	return r
}

func (e *env) exam(t *testing.T, id string) store.Exam {
	t.Helper()
	x, err := e.exams.GetExam(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return x
}

func (e *env) rows(t *testing.T, id string) []string {
	t.Helper()
	as, err := e.attempts.ListByExam(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, a := range as {
		out = append(out, a.UserID)
	}
	return out
}

func TestTotalMarksComputedFromQuestions(t *testing.T) {
	e := newEnv(t)
	in := input(3)
	in.Questions[0].Marks, in.Questions[1].Marks, in.Questions[2].Marks = 2, 3, 5
	r := e.create(t, e.teacher, in, "")
	if got := e.exam(t, r.ID).TotalMarks; got != 10 {
		t.Fatalf("total %d", got)
	}
}

func TestDeadlineExactlyOneOfClosesAtAndClosesInSeconds(t *testing.T) {
	e := newEnv(t)
	both := input(1)
	both.ClosesAt = t0.Add(time.Hour).Format(time.RFC3339)
	_, err := e.svc.Create(context.Background(), e.teacher, both, "")
	wantField(t, err, "closesAt")
	none := input(1)
	none.ClosesInSeconds = nil
	_, err = e.svc.Create(context.Background(), e.teacher, none, "")
	wantField(t, err, "closesAt")
	abs := input(1)
	abs.ClosesInSeconds = nil
	abs.ClosesAt = t0.Add(time.Hour).Format(time.RFC3339)
	r := e.create(t, e.teacher, abs, "")
	if !e.exam(t, r.ID).ClosesAt.Equal(t0.Add(time.Hour)) {
		t.Fatal("closesAt not kept")
	}
	bad := abs
	bad.ClosesAt = "tomorrow"
	_, err = e.svc.Create(context.Background(), e.teacher, bad, "")
	wantField(t, err, "closesAt")
}

func TestDeadlineBounds(t *testing.T) {
	e := newEnv(t)
	for _, tc := range []struct {
		secs int64
		ok   bool
	}{{59, false}, {60, true}, {30 * 24 * 3600, true}, {30*24*3600 + 1, false}} {
		in := input(1)
		in.ClosesInSeconds = &tc.secs
		_, err := e.svc.Create(context.Background(), e.teacher, in, "")
		if tc.ok && err != nil {
			t.Fatalf("%d: %v", tc.secs, err)
		}
		if !tc.ok {
			wantField(t, err, "closesInSeconds")
		}
	}
	// the absolute form has the same bounds
	for _, tc := range []struct {
		d  time.Duration
		ok bool
	}{{59 * time.Second, false}, {time.Minute, true}, {30 * 24 * time.Hour, true}, {30*24*time.Hour + time.Second, false}} {
		in := input(1)
		in.ClosesInSeconds = nil
		in.ClosesAt = t0.Add(tc.d).Format(time.RFC3339)
		_, err := e.svc.Create(context.Background(), e.teacher, in, "")
		if tc.ok && err != nil {
			t.Fatalf("%v: %v", tc.d, err)
		}
		if !tc.ok {
			wantField(t, err, "closesAt")
		}
	}
}

func TestServerClockUsedForClosesInSeconds(t *testing.T) {
	e := newEnv(t)
	e.clk.Set(t0.Add(5 * time.Hour))
	in := input(1)
	secs := int64(3600)
	in.ClosesInSeconds = &secs
	r := e.create(t, e.teacher, in, "")
	x := e.exam(t, r.ID)
	if !x.ClosesAt.Equal(t0.Add(6*time.Hour)) || !x.CreatedAt.Equal(t0.Add(5*time.Hour)) {
		t.Fatalf("closesAt %v createdAt %v", x.ClosesAt, x.CreatedAt)
	}
}

func TestTitleAndQuestionCountBounds(t *testing.T) {
	e := newEnv(t)
	in := input(1)
	in.Title = strings.Repeat("x", 121)
	_, err := e.svc.Create(context.Background(), e.teacher, in, "")
	wantField(t, err, "title")
	in.Title = strings.Repeat("é", 120)
	e.create(t, e.teacher, in, "")
	in.Title = "   "
	_, err = e.svc.Create(context.Background(), e.teacher, in, "")
	wantField(t, err, "title")
	_, err = e.svc.Create(context.Background(), e.teacher, input(0), "")
	wantField(t, err, "questions")
	_, err = e.svc.Create(context.Background(), e.teacher, input(61), "")
	wantField(t, err, "questions")
	e.create(t, e.teacher, input(60), "")
}

func TestMCQShape(t *testing.T) {
	e := newEnv(t)
	bad := func(name string, mut func(q *exams.CreateQuestion)) {
		t.Helper()
		in := input(1)
		mut(&in.Questions[0])
		_, err := e.svc.Create(context.Background(), e.teacher, in, "")
		if err == nil {
			t.Fatalf("%s accepted", name)
		}
		wantField(t, err, "questions[0]")
	}
	bad("3 options", func(q *exams.CreateQuestion) { q.Options = q.Options[:3] })
	bad("empty option", func(q *exams.CreateQuestion) { q.Options[1] = "  " })
	bad("correctIndex 4", func(q *exams.CreateQuestion) { i := 4; q.CorrectIndex = &i })
	bad("correctIndex -1", func(q *exams.CreateQuestion) { i := -1; q.CorrectIndex = &i })
	bad("no correctIndex", func(q *exams.CreateQuestion) { q.CorrectIndex = nil })
	bad("duplicate after trim/lowercase", func(q *exams.CreateQuestion) { q.Options = []string{"Paris", " paris ", "c", "d"} })
	bad("model answer on mcq", func(q *exams.CreateQuestion) { q.ModelAnswer = "x" })
	bad("marks 0", func(q *exams.CreateQuestion) { q.Marks = 0 })
	bad("marks 6", func(q *exams.CreateQuestion) { q.Marks = 6 })
	bad("empty text", func(q *exams.CreateQuestion) { q.Text = " " })

	free := input(1)
	free.Type = "free"
	free.Questions[0] = exams.CreateQuestion{Marks: 2, Text: "Why?", Options: []string{"a", "b", "c", "d"}, ModelAnswer: "Because"}
	_, err := e.svc.Create(context.Background(), e.teacher, free, "")
	wantField(t, err, "questions[0]")
	free.Questions[0] = exams.CreateQuestion{Marks: 2, Text: "Why?"}
	_, err = e.svc.Create(context.Background(), e.teacher, free, "")
	wantField(t, err, "questions[0]")
	free.Questions[0].ModelAnswer = "Because"
	e.create(t, e.teacher, free, "")
}

func TestTypeMustMatchEveryQuestion(t *testing.T) {
	e := newEnv(t)
	in := input(3)
	in.Questions[2] = exams.CreateQuestion{Marks: 1, Text: "free", ModelAnswer: "a"}
	_, err := e.svc.Create(context.Background(), e.teacher, in, "")
	wantField(t, err, "questions[2]")
	in = input(1)
	in.Type = "essay"
	_, err = e.svc.Create(context.Background(), e.teacher, in, "")
	wantField(t, err, "type")
}

func TestServerMintsQuestionIDsIgnoringClientIDs(t *testing.T) {
	e := newEnv(t)
	in := input(3)
	for i := range in.Questions {
		in.Questions[i].ID = "client-id"
	}
	r := e.create(t, e.teacher, in, "")
	seen := map[string]bool{}
	for _, q := range e.exam(t, r.ID).Questions {
		if q.ID == "" || q.ID == "client-id" || seen[q.ID] {
			t.Fatalf("bad id %q", q.ID)
		}
		seen[q.ID] = true
	}
}

// Ruling: over-long source fields are rejected (422), not truncated; they are display-only and
// never looked up or compared with anything.
func TestSourceFieldsTruncatedOrRejectedOver120AndNeverAuthorise(t *testing.T) {
	e := newEnv(t)
	in := input(1)
	in.Questions[0].SourceDocName = strings.Repeat("n", 121)
	_, err := e.svc.Create(context.Background(), e.teacher, in, "")
	wantField(t, err, "questions[0]")
	in = input(1)
	in.Questions[0].SourceDocID = strings.Repeat("i", 121)
	_, err = e.svc.Create(context.Background(), e.teacher, in, "")
	wantField(t, err, "questions[0]")
	// exactly 120 is kept verbatim
	in = input(1)
	in.Questions[0].SourceDocName = strings.Repeat("é", 120)
	r := e.create(t, e.teacher, in, "")
	if got := e.exam(t, r.ID).Questions[0].SourceDocName; got != strings.Repeat("é", 120) {
		t.Fatal("source name changed")
	}
	// A source doc id that belongs to nobody (or to another user) changes nothing: no lookup is made.
	in = input(1)
	in.Questions[0].SourceDocID = "someone-elses-doc"
	e.create(t, e.student, in, "")
}

func TestMaxPeeksDefaults(t *testing.T) {
	e := newEnv(t)
	r := e.create(t, e.teacher, input(5), "")
	if got := e.exam(t, r.ID).MaxPeeks; got != 3 {
		t.Fatalf("practice default %d", got)
	}
	in := input(5)
	in.AssigneeEmails = []string{"s1@x.com"}
	r = e.create(t, e.teacher, in, "")
	if got := e.exam(t, r.ID).MaxPeeks; got != 0 {
		t.Fatalf("assigned default %d", got)
	}
	// a practice exam with fewer than 3 questions cannot default above its question count
	r = e.create(t, e.teacher, input(2), "")
	if got := e.exam(t, r.ID).MaxPeeks; got != 2 {
		t.Fatalf("2-question default %d", got)
	}
	for _, tc := range []struct {
		n, peeks int
		ok       bool
	}{{10, 10, true}, {10, 11, false}, {4, 5, false}, {4, 4, true}, {4, 0, true}, {4, -1, false}} {
		in := input(tc.n)
		p := tc.peeks
		in.MaxPeeks = &p
		_, err := e.svc.Create(context.Background(), e.teacher, in, "")
		if tc.ok && err != nil {
			t.Fatalf("%+v: %v", tc, err)
		}
		if !tc.ok {
			wantField(t, err, "maxPeeks")
		}
	}
}

func TestPracticeExamCreatesCreatorAttemptRow(t *testing.T) {
	e := newEnv(t)
	r := e.create(t, e.student, input(2), "")
	rows := e.rows(t, r.ID)
	if len(rows) != 1 || rows[0] != "s1" {
		t.Fatalf("rows %v", rows)
	}
	a, _ := e.attempts.GetAttempt(context.Background(), r.ID, "s1", 1)
	if a.Status != store.AttemptNotStarted || !a.CreatedAt.Equal(t0) {
		t.Fatalf("%+v", a)
	}
	if r.UnknownEmails == nil || len(r.UnknownEmails) != 0 || r.Replay {
		t.Fatalf("%+v", r)
	}
}

func TestAssigneeRowsOnlyNotCreator(t *testing.T) {
	e := newEnv(t)
	e.addUser(t, "s2", "s2@x.com", "student")
	in := input(2)
	in.AssigneeEmails = []string{"s1@x.com", " S2@X.com "}
	r := e.create(t, e.teacher, in, "")
	if rows := e.rows(t, r.ID); len(rows) != 2 || rows[0] != "s1" || rows[1] != "s2" {
		t.Fatalf("rows %v", rows)
	}
}

func TestCreatorCanAddSelfByEmail(t *testing.T) {
	e := newEnv(t)
	in := input(2)
	in.AssigneeEmails = []string{"t1@x.com", "s1@x.com"}
	r := e.create(t, e.teacher, in, "")
	if rows := e.rows(t, r.ID); len(rows) != 2 || rows[0] != "s1" || rows[1] != "t1" {
		t.Fatalf("rows %v", rows)
	}
	in.AssigneeEmails = []string{"s1@x.com"}
	r = e.create(t, e.student, in, "")
	if rows := e.rows(t, r.ID); len(rows) != 1 || rows[0] != "s1" {
		t.Fatalf("student self rows %v", rows)
	}
}

func emails(n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = fmt.Sprintf("nobody%d@x.com", i)
	}
	return out
}

func TestTeacherCap50And51Is422(t *testing.T) {
	e := newEnv(t)
	in := input(1)
	in.AssigneeEmails = emails(50)
	r := e.create(t, e.teacher, in, "")
	if len(r.UnknownEmails) != 50 {
		t.Fatalf("unknown %d", len(r.UnknownEmails))
	}
	in.AssigneeEmails = emails(51)
	_, err := e.svc.Create(context.Background(), e.teacher, in, "")
	wantField(t, err, "assigneeEmails")
	_, err = e.svc.Create(context.Background(), e.parent, in, "")
	wantField(t, err, "assigneeEmails")
	// duplicates count once
	in.AssigneeEmails = append(emails(50), "NOBODY0@x.com")
	e.create(t, e.teacher, in, "")
}

func TestStudentCap10(t *testing.T) {
	e := newEnv(t)
	in := input(1)
	in.AssigneeEmails = emails(10)
	e.create(t, e.student, in, "")
	in.AssigneeEmails = emails(11)
	_, err := e.svc.Create(context.Background(), e.student, in, "")
	wantField(t, err, "assigneeEmails")
}

func TestStudentAssigningToTeacherReportedAsUnknownIdenticalToUnregistered(t *testing.T) {
	e := newEnv(t)
	run := func(email string) []byte {
		in := input(1)
		in.AssigneeEmails = []string{email}
		r := e.create(t, e.student, in, "")
		if len(e.rows(t, r.ID)) != 0 {
			t.Fatalf("rows written for %s", email)
		}
		b, _ := json.Marshal(map[string]any{"id": "X", "unknownEmails": r.UnknownEmails, "replay": r.Replay})
		return b
	}
	// same-length addresses so the echoed text is the only difference-free comparison
	teacher := run("t1@x.com")
	parent := run("p1@x.com")
	ghost := run("zz@x.com")
	if string(teacher) != `{"id":"X","replay":false,"unknownEmails":["t1@x.com"]}` {
		t.Fatalf("%s", teacher)
	}
	if len(teacher) != len(ghost) || len(parent) != len(ghost) ||
		strings.ReplaceAll(string(teacher), "t1@x.com", "zz@x.com") != string(ghost) ||
		strings.ReplaceAll(string(parent), "p1@x.com", "zz@x.com") != string(ghost) {
		t.Fatalf("shapes differ:\n%s\n%s\n%s", teacher, parent, ghost)
	}
}

func TestUnknownEmailsReturnedNotError(t *testing.T) {
	e := newEnv(t)
	in := input(1)
	in.AssigneeEmails = []string{"s1@x.com", "ghost@x.com", "not-an-email", "ghost@x.com"}
	r := e.create(t, e.teacher, in, "")
	if len(r.UnknownEmails) != 2 || r.UnknownEmails[0] != "ghost@x.com" || r.UnknownEmails[1] != "not-an-email" {
		t.Fatalf("%v", r.UnknownEmails)
	}
	if rows := e.rows(t, r.ID); len(rows) != 1 || rows[0] != "s1" {
		t.Fatalf("rows %v", rows)
	}
	in.AssigneeEmails = []string{"  "}
	_, err := e.svc.Create(context.Background(), e.teacher, in, "")
	wantField(t, err, "assigneeEmails")
}

func TestIdempotencyKeyBecomesExamID(t *testing.T) {
	e := newEnv(t)
	r := e.create(t, e.teacher, input(1), "3f2b8c1e-aaaa-4bbb-8ccc-0123456789ab")
	if r.ID != "3f2b8c1e-aaaa-4bbb-8ccc-0123456789ab" || r.Replay {
		t.Fatalf("%+v", r)
	}
	e.exam(t, r.ID)
	r2 := e.create(t, e.teacher, input(1), "")
	r3 := e.create(t, e.teacher, input(1), "")
	if len(r2.ID) != 26 || r2.ID == r3.ID {
		t.Fatalf("minted ids %q %q", r2.ID, r3.ID)
	}
}

func TestIdempotencyKeyFormat(t *testing.T) {
	e := newEnv(t)
	for _, k := range []string{"1234567", strings.Repeat("a", 65), "a b", "abcd efgh", "abcdefgh!", "abcdéfgh"} {
		_, err := e.svc.Create(context.Background(), e.teacher, input(1), k)
		wantField(t, err, "Idempotency-Key")
	}
	for _, k := range []string{"12345678", strings.Repeat("a", 64), "A_b-9_Z-"} {
		e.create(t, e.teacher, input(1), k)
	}
}

// dropRows wraps an AttemptStore and can be made to fail, to simulate a crash after the exam item.
type dropRows struct {
	store.AttemptStore
	fail atomic.Bool
}

func (d *dropRows) PutAttemptIfAbsent(ctx context.Context, a store.Attempt) (bool, error) {
	if d.fail.Load() {
		return false, errors.New("crash")
	}
	return d.AttemptStore.PutAttemptIfAbsent(ctx, a)
}

func TestReplayBySameCreatorCompletesMissingRowsAnd200(t *testing.T) {
	e := newEnv(t)
	d := &dropRows{AttemptStore: e.attempts}
	d.fail.Store(true)
	e.svc = exams.NewService(e.users, e.exams, d, e.clk)
	in := input(2)
	in.AssigneeEmails = []string{"s1@x.com", "ghost@x.com"}
	_, err := e.svc.Create(context.Background(), e.teacher, in, "key-12345")
	wantStatus(t, err, http.StatusServiceUnavailable)
	if len(e.rows(t, "key-12345")) != 0 {
		t.Fatal("rows written")
	}
	first := e.exam(t, "key-12345")
	d.fail.Store(false)
	e.clk.Advance(time.Minute)
	r := e.create(t, e.teacher, in, "key-12345")
	if !r.Replay || r.ID != "key-12345" || len(r.UnknownEmails) != 1 {
		t.Fatalf("%+v", r)
	}
	if rows := e.rows(t, "key-12345"); len(rows) != 1 || rows[0] != "s1" {
		t.Fatalf("rows %v", rows)
	}
	again := e.exam(t, "key-12345")
	if again.ClosesAt != first.ClosesAt || again.Questions[0].ID != first.Questions[0].ID || !again.CreatedAt.Equal(first.CreatedAt) {
		t.Fatal("exam changed on replay")
	}
	a, _ := e.attempts.GetAttempt(context.Background(), "key-12345", "s1", 1)
	if !a.CreatedAt.Equal(first.CreatedAt) {
		t.Fatal("row createdAt")
	}
	// a third replay creates nothing new and keeps a started row
	if err := e.attempts.Save(context.Background(), "key-12345", "s1", 1, map[string]string{"x": "y"}, 0, t0); err != nil {
		t.Fatal(err)
	}
	r = e.create(t, e.teacher, in, "key-12345")
	a, _ = e.attempts.GetAttempt(context.Background(), "key-12345", "s1", 1)
	if !r.Replay || a.Status != store.AttemptInProgress || a.Answers["x"] != "y" {
		t.Fatalf("%+v %+v", r, a)
	}
}

func TestReplayWithDifferentBodyReturnsExistingAndDoesNotDuplicate(t *testing.T) {
	e := newEnv(t)
	e.addUser(t, "s2", "s2@x.com", "student")
	in := input(2)
	in.AssigneeEmails = []string{"s1@x.com"}
	e.create(t, e.teacher, in, "key-12345")
	orig := e.exam(t, "key-12345")

	other := input(3)
	other.Title = "Other"
	other.AssigneeEmails = []string{"s2@x.com", "ghost@x.com"}
	r := e.create(t, e.teacher, other, "key-12345")
	if !r.Replay || r.ID != "key-12345" || len(r.UnknownEmails) != 1 || r.UnknownEmails[0] != "ghost@x.com" {
		t.Fatalf("%+v", r)
	}
	got := e.exam(t, "key-12345")
	if got.Title != orig.Title || len(got.Questions) != 2 || got.TotalMarks != orig.TotalMarks {
		t.Fatalf("exam changed: %+v", got)
	}
	if rows := e.rows(t, "key-12345"); len(rows) != 2 {
		t.Fatalf("rows %v", rows)
	}
	if list, _ := e.exams.ListByCreator(context.Background(), "t1", 50); len(list) != 1 {
		t.Fatalf("%d exams", len(list))
	}
}

// Replays may not grow the assignee set past the creator's cap (existing rows count).
func TestReplayCannotBypassAssigneeCap(t *testing.T) {
	e := newEnv(t)
	var first, second []string
	for i := 0; i < 10; i++ {
		a, b := fmt.Sprintf("a%d", i), fmt.Sprintf("b%d", i)
		e.addUser(t, a, a+"@x.com", "student")
		e.addUser(t, b, b+"@x.com", "student")
		first, second = append(first, a+"@x.com"), append(second, b+"@x.com")
	}
	in := input(1)
	in.AssigneeEmails = first
	e.create(t, e.student, in, "student-key-1")
	in.AssigneeEmails = second
	_, err := e.svc.Create(context.Background(), e.student, in, "student-key-1")
	wantField(t, err, "assigneeEmails")
	if rows := e.rows(t, "student-key-1"); len(rows) != 10 {
		t.Fatalf("rows %d", len(rows))
	}
	// the same set again is fine, and so is adding one more within the cap
	in.AssigneeEmails = first
	if r := e.create(t, e.student, in, "student-key-1"); !r.Replay {
		t.Fatal("no replay")
	}
	// teacher: 50 existing + one more user is over the cap
	for i := 0; i < 51; i++ {
		e.addUser(t, fmt.Sprintf("m%d", i), fmt.Sprintf("m%d@x.com", i), "student")
	}
	var fifty []string
	for i := 0; i < 50; i++ {
		fifty = append(fifty, fmt.Sprintf("m%d@x.com", i))
	}
	tin := input(1)
	tin.AssigneeEmails = fifty
	e.create(t, e.teacher, tin, "teacher-key-1")
	tin.AssigneeEmails = []string{"m50@x.com"}
	_, err = e.svc.Create(context.Background(), e.teacher, tin, "teacher-key-1")
	wantField(t, err, "assigneeEmails")
	if len(e.rows(t, "teacher-key-1")) != 50 {
		t.Fatal("rows added")
	}
}

// A practice exam replayed with assignees: the creator's own row does not count towards the cap
// and the assignees are added (the replay body names them; the cap still applies).
func TestPracticeReplayWithAssigneesCountsOnlyAssignees(t *testing.T) {
	e := newEnv(t)
	e.create(t, e.student, input(1), "practice-key-1")
	in := input(1)
	in.AssigneeEmails = []string{"s1@x.com"}
	r := e.create(t, e.student, in, "practice-key-1")
	if !r.Replay || len(e.rows(t, "practice-key-1")) != 1 {
		t.Fatalf("%+v %v", r, e.rows(t, "practice-key-1"))
	}
}

func TestReplayWithEditedQuestionCompletesRowsKeepsOriginal(t *testing.T) {
	e := newEnv(t)
	d := &dropRows{AttemptStore: e.attempts}
	d.fail.Store(true)
	e.svc = exams.NewService(e.users, e.exams, d, e.clk)
	in := input(2)
	in.AssigneeEmails = []string{"s1@x.com", "ghost@x.com"}
	_, err := e.svc.Create(context.Background(), e.teacher, in, "key-12345")
	wantStatus(t, err, http.StatusServiceUnavailable)
	var ae *apperr.Error
	errors.As(err, &ae)
	if ae.Code != "upstream_unavailable" || ae.Err == nil {
		t.Fatalf("%+v", ae)
	}
	d.fail.Store(false)
	edited := in
	edited.Questions = append([]exams.CreateQuestion(nil), in.Questions...)
	edited.Questions[0].Text = "EDITED"
	r := e.create(t, e.teacher, edited, "key-12345")
	if !r.Replay || len(r.UnknownEmails) != 1 || r.UnknownEmails[0] != "ghost@x.com" {
		t.Fatalf("%+v", r)
	}
	if rows := e.rows(t, "key-12345"); len(rows) != 1 || rows[0] != "s1" {
		t.Fatalf("rows %v", rows)
	}
	if got := e.exam(t, "key-12345").Questions[0].Text; got != "q0" {
		t.Fatalf("question text %q", got)
	}
}

func TestPeeksCapIsTenIndependentOfQuestionCount(t *testing.T) {
	e := newEnv(t)
	for _, tc := range []struct {
		peeks int
		ok    bool
	}{{10, true}, {11, false}, {12, false}} {
		in := input(12)
		p := tc.peeks
		in.MaxPeeks = &p
		_, err := e.svc.Create(context.Background(), e.teacher, in, "")
		if tc.ok && err != nil {
			t.Fatal(err)
		}
		if !tc.ok {
			wantField(t, err, "maxPeeks")
		}
	}
}

func TestParentCapIs50NotStudentCap(t *testing.T) {
	e := newEnv(t)
	in := input(1)
	in.AssigneeEmails = emails(11)
	e.create(t, e.parent, in, "")
	in.AssigneeEmails = emails(50)
	e.create(t, e.parent, in, "")
}

func TestAssigneeEmailLengthCapped(t *testing.T) {
	e := newEnv(t)
	in := input(1)
	in.AssigneeEmails = []string{strings.Repeat("a", 250) + "@x.com"}
	_, err := e.svc.Create(context.Background(), e.teacher, in, "")
	wantField(t, err, "assigneeEmails")
}

func TestQuestionFieldBoundariesAndStoredForm(t *testing.T) {
	e := newEnv(t)
	in := input(1)
	in.Questions[0].Options = []string{"  " + strings.Repeat("o", 500) + " ", "b", "c", "d"}
	in.Questions[0].Text = "  <b>x</b> & y  "
	r := e.create(t, e.teacher, in, "")
	q := e.exam(t, r.ID).Questions[0]
	if q.Options[0] != strings.Repeat("o", 500) || q.Text != "<b>x</b> & y" {
		t.Fatalf("stored %q %q", q.Options[0], q.Text)
	}
	in.Questions[0].Options[0] = strings.Repeat("o", 501)
	_, err := e.svc.Create(context.Background(), e.teacher, in, "")
	wantField(t, err, "questions[0]")

	free := input(1)
	free.Type = "free"
	free.Questions[0] = exams.CreateQuestion{Marks: 1, Text: "q", ModelAnswer: strings.Repeat("m", 4000)}
	e.create(t, e.teacher, free, "")
	free.Questions[0].ModelAnswer = strings.Repeat("m", 4001)
	_, err = e.svc.Create(context.Background(), e.teacher, free, "")
	wantField(t, err, "questions[0]")
	ci := 1
	free.Questions[0] = exams.CreateQuestion{Marks: 1, Text: "q", ModelAnswer: "a", CorrectIndex: &ci}
	_, err = e.svc.Create(context.Background(), e.teacher, free, "")
	wantField(t, err, "questions[0]")
}

// The echoed text is the same whether the address is unregistered or a not-allowed account,
// including its case and surrounding spaces.
func TestMixedCaseEchoIdenticalForUnknownAndNotAllowed(t *testing.T) {
	e := newEnv(t)
	run := func(email string) []string {
		in := input(1)
		in.AssigneeEmails = []string{email}
		return e.create(t, e.student, in, "").UnknownEmails
	}
	a, b := run(" T1@X.com "), run(" Zz@X.com ")
	if len(a) != 1 || len(b) != 1 || a[0] != "T1@X.com" || b[0] != "Zz@X.com" {
		t.Fatalf("%q %q", a, b)
	}
}

func TestOtherCreatorsKey409(t *testing.T) {
	e := newEnv(t)
	in := input(2)
	in.Title = "Secret title"
	e.create(t, e.teacher, in, "key-12345")
	_, err := e.svc.Create(context.Background(), e.student, input(1), "key-12345")
	wantStatus(t, err, http.StatusConflict)
	var ae *apperr.Error
	errors.As(err, &ae)
	if ae.Code != "conflict" || ae.Fields != nil || strings.Contains(ae.Message+ae.Error(), "Secret") {
		t.Fatalf("%+v", ae)
	}
	if rows := e.rows(t, "key-12345"); len(rows) != 1 || rows[0] != "t1" {
		t.Fatalf("rows %v", rows)
	}
	if e.exam(t, "key-12345").CreatorID != "t1" {
		t.Fatal("exam changed")
	}
}

type orderLog struct {
	mu      sync.Mutex
	events  []string
	cur     int
	maxSeen int
}

func (o *orderLog) add(s string) { o.mu.Lock(); o.events = append(o.events, s); o.mu.Unlock() }

type spyExams struct {
	store.ExamStore
	log *orderLog
}

func (s spyExams) PutExam(ctx context.Context, e store.Exam) error {
	err := s.ExamStore.PutExam(ctx, e)
	s.log.add("exam")
	return err
}

type spyAttempts struct {
	store.AttemptStore
	log *orderLog
}

func (s spyAttempts) PutAttemptIfAbsent(ctx context.Context, a store.Attempt) (bool, error) {
	s.log.mu.Lock()
	s.log.cur++
	if s.log.cur > s.log.maxSeen {
		s.log.maxSeen = s.log.cur
	}
	s.log.mu.Unlock()
	time.Sleep(5 * time.Millisecond)
	ok, err := s.AttemptStore.PutAttemptIfAbsent(ctx, a)
	s.log.mu.Lock()
	s.log.cur--
	s.log.events = append(s.log.events, "row")
	s.log.mu.Unlock()
	return ok, err
}

func TestAttemptRowsWrittenAfterExamItem(t *testing.T) {
	e := newEnv(t)
	log := &orderLog{}
	e.svc = exams.NewService(e.users, spyExams{e.exams, log}, spyAttempts{e.attempts, log}, e.clk)
	in := input(1)
	for i := 0; i < 40; i++ {
		id := fmt.Sprintf("u%d", i)
		e.addUser(t, id, id+"@x.com", "student")
		in.AssigneeEmails = append(in.AssigneeEmails, id+"@x.com")
	}
	e.create(t, e.teacher, in, "")
	if len(log.events) != 41 || log.events[0] != "exam" {
		t.Fatalf("events %v", log.events)
	}
	for _, ev := range log.events[1:] {
		if ev != "row" {
			t.Fatalf("events %v", log.events)
		}
	}
	if log.maxSeen > 10 || log.maxSeen < 2 {
		t.Fatalf("max concurrency %d (want 2..10)", log.maxSeen)
	}
}

func TestAnyRoleMayCreate(t *testing.T) {
	e := newEnv(t)
	for _, u := range []store.User{e.student, e.parent, e.teacher} {
		e.create(t, u, input(1), "")
	}
}

func TestDeletedDocumentDoesNotAffectExam(t *testing.T) {
	e := newEnv(t)
	ds := memory.NewDocStore(e.clk)
	blob := docs.NewMemBlob()
	dsvc := docs.NewService(ds, blob, e.clk)
	out, err := dsvc.UploadURL(context.Background(), e.teacher, docs.UploadInput{Name: "Bio.pdf", Type: "application/pdf", SizeBytes: 1000})
	if err != nil {
		t.Fatal(err)
	}
	in := input(1)
	in.Questions[0].SourceDocID, in.Questions[0].SourceDocName = out.DocID, "Bio.pdf"
	r := e.create(t, e.teacher, in, "")
	if err := dsvc.Delete(context.Background(), e.teacher, out.DocID); err != nil {
		t.Fatal(err)
	}
	q := e.exam(t, r.ID).Questions[0]
	if q.SourceDocID != out.DocID || q.SourceDocName != "Bio.pdf" {
		t.Fatalf("%+v", q)
	}
	// and a replay after the delete still works
	if rr := e.create(t, e.teacher, in, r.ID); !rr.Replay {
		t.Fatal("no replay")
	}
}

// Size check: 60 questions of 4000-rune multi-byte model answers are far over the DynamoDB limit
// while every individual field is within its cap.
func TestOversizeItemIs422NotInternal(t *testing.T) {
	e := newEnv(t)
	in := input(60)
	in.Type = "free"
	for i := range in.Questions {
		in.Questions[i] = exams.CreateQuestion{Marks: 1, Text: strings.Repeat("é", 2000), ModelAnswer: strings.Repeat("語", 4000)}
	}
	_, err := e.svc.Create(context.Background(), e.teacher, in, "")
	wantField(t, err, "questions")
	if list, _ := e.exams.ListByCreator(context.Background(), "t1", 50); len(list) != 0 {
		t.Fatal("exam written")
	}
	// a field over its own cap is also 422
	in = input(1)
	in.Questions[0].Text = strings.Repeat("x", 2001)
	_, err = e.svc.Create(context.Background(), e.teacher, in, "")
	wantField(t, err, "questions[0]")
}
