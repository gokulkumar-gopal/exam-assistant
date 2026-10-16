package exams_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/gokulkumar/exam-assistant/backend/internal/exams"
	"github.com/gokulkumar/exam-assistant/backend/internal/store"
)

// seedRes stores an attempt row with answers, revealed ids and optional stored scores.
func (e *env) seedRes(t *testing.T, examID, userID, status string, answers map[string]string, revealed []string, halves, before *int) {
	t.Helper()
	if answers == nil {
		answers = map[string]string{}
	}
	if revealed == nil {
		revealed = []string{}
	}
	a := store.Attempt{ExamID: examID, UserID: userID, Status: status, Answers: answers, Revealed: revealed,
		ScoreHalves: halves, ScoreBeforePeeksHalves: before, CreatedAt: t0}
	if status == store.AttemptSubmitted {
		s := t0.Add(time.Hour)
		a.SubmittedAt = &s
	}
	if _, err := e.attempts.PutAttemptIfAbsent(context.Background(), a); err != nil {
		t.Fatal(err)
	}
}

func (e *env) result(t *testing.T, u store.User, id string) (exams.ResultView, map[string]any) {
	t.Helper()
	v, err := e.svc.Result(context.Background(), u, id)
	if err != nil {
		t.Fatalf("Result: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(marshal(t, v)), &m); err != nil {
		t.Fatal(err)
	}
	return v, m
}

func resQs(t *testing.T, m map[string]any) []map[string]any {
	t.Helper()
	var out []map[string]any
	list, ok := m["questions"].([]any)
	if !ok || len(list) != 3 {
		t.Fatalf("questions: %v", m["questions"])
	}
	for _, q := range list {
		out = append(out, q.(map[string]any))
	}
	return out
}

func resEnv(t *testing.T, typ string) *env {
	t.Helper()
	e := newEnv(t)
	e.seedFull(t, "e1", "t1", typ)
	e.svc.SetFinalizer(exams.NewFinalizer(e.attempts))
	return e
}

func TestResultBeforeSubmitAndNotOver409(t *testing.T) {
	for _, st := range []string{store.AttemptNotStarted, store.AttemptInProgress} {
		e := resEnv(t, "mcq")
		e.seedRes(t, "e1", "s1", st, nil, nil, nil, nil)
		e.clk.Set(t0.Add(time.Hour))
		_, err := e.svc.Result(context.Background(), e.student, "e1")
		wantStatus(t, err, 409)
		if err == nil || !strings.Contains(err.Error(), "Submit the exam to see results.") {
			t.Fatalf("%s: message: %v", st, err)
		}
	}
}

func TestResultAfterOverFinalizesFirst(t *testing.T) {
	e := resEnv(t, "mcq")
	e.seedRes(t, "e1", "s1", store.AttemptInProgress, map[string]string{"q3": "o0", "q1": "o9"}, nil, nil, nil)
	e.clk.Set(closes.Add(60 * time.Second))
	v, m := e.result(t, e.student, "e1")
	if v.Status != "submitted" || m["score"] != float64(3) || m["scoreBeforePeeks"] != float64(3) {
		t.Fatalf("%s", marshal(t, v))
	}
	row, _ := e.attempts.GetAttempt(context.Background(), "e1", "s1")
	if row.Status != store.AttemptSubmitted || !row.AutoSubmitted || row.ScoreHalves == nil || *row.ScoreHalves != 6 {
		t.Fatalf("row not finalized: %+v", row)
	}
}

func TestResultHidesCorrectnessWhenKeyHidden(t *testing.T) {
	for _, typ := range []string{"mcq", "free_text"} {
		e := resEnv(t, typ)
		e.seedRes(t, "e1", "s1", store.AttemptSubmitted, map[string]string{"q3": "o0", "q1": "my free"}, nil, ip(6), ip(6))
		e.clk.Set(t0.Add(time.Hour))
		v, m := e.result(t, e.student, "e1")
		raw := marshal(t, v)
		for _, leak := range []string{`"correct"`, `"correctAnswer"`, `"modelAnswer"`, secretModel, `"correctIndex"`} {
			if strings.Contains(raw, leak) {
				t.Fatalf("%s leak %s in %s", typ, leak, raw)
			}
		}
		if m["keyVisible"] != false || m["keyAvailableAt"] != closes.Add(time.Minute).UTC().Format(time.RFC3339) {
			t.Fatalf("%s: %s", typ, raw)
		}
		if typ == "mcq" && (m["score"] != float64(3) || m["maxScore"] != float64(6)) {
			t.Fatalf("scores must still be returned: %s", raw)
		}
	}
}

func TestResultShowsKeyWhenOver(t *testing.T) {
	e := resEnv(t, "mcq")
	e.seedRes(t, "e1", "s1", store.AttemptSubmitted, map[string]string{"q3": "o0", "q1": "o3"}, nil, ip(6), ip(6))
	e.clk.Set(closes.Add(time.Minute))
	_, m := e.result(t, e.student, "e1")
	if m["keyVisible"] != true || m["keyAvailableAt"] != nil {
		t.Fatalf("%v", m)
	}
	qs := resQs(t, m)
	if qs[0]["correct"] != true || qs[0]["correctAnswer"] != "o0" || qs[1]["correct"] != false || qs[1]["correctAnswer"] != "o1" ||
		qs[2]["correct"] != false || qs[2]["yourAnswer"] != "" {
		t.Fatalf("%v", qs)
	}
	if v, ok := qs[0]["modelAnswer"]; !ok || v != nil {
		t.Fatalf("MCQ with key carries modelAnswer:null (8.5 example): %v", qs[0])
	}
}

func TestResultMissedStatusNoScoreEmptyAnswers(t *testing.T) {
	e := resEnv(t, "mcq")
	e.seedRes(t, "e1", "s1", store.AttemptNotStarted, nil, nil, nil, nil)
	e.clk.Set(closes.Add(time.Minute))
	v, m := e.result(t, e.student, "e1")
	if v.Status != "missed" || m["score"] != nil || m["scoreBeforePeeks"] != nil || m["keyVisible"] != true || m["maxScore"] != float64(6) {
		t.Fatalf("%s", marshal(t, v))
	}
	if len(resQs(t, m)) != 3 {
		t.Fatal("questions must still be listed")
	}
	for _, q := range resQs(t, m) {
		if q["yourAnswer"] != "" {
			t.Fatalf("%v", q)
		}
	}
	row, _ := e.attempts.GetAttempt(context.Background(), "e1", "s1")
	if row.Status != store.AttemptNotStarted {
		t.Fatalf("missed is derived, never stored: %s", row.Status)
	}
}

func TestResultDeclined404(t *testing.T) {
	e := resEnv(t, "mcq")
	e.seedRes(t, "e1", "s1", store.AttemptDeclined, nil, nil, nil, nil)
	e.clk.Set(closes.Add(time.Hour))
	other := e.addUser(t, "s2", "s2@x.com", "student")
	var errs []string
	for _, c := range []struct {
		u  store.User
		id string
	}{{e.student, "e1"}, {other, "e1"}, {e.student, "nope"}, {e.teacher, "e1"}} { // declined, stranger, missing, creator without a row
		_, err := e.svc.Result(context.Background(), c.u, c.id)
		wantStatus(t, err, 404)
		errs = append(errs, err.Error())
	}
	for _, s := range errs[1:] {
		if s != errs[0] {
			t.Fatalf("404s differ: %q vs %q", s, errs[0])
		}
	}
}

func TestResultScoreHalfMarksRendered(t *testing.T) {
	e := resEnv(t, "mcq")
	x := e.exam(t, "e1")
	x.ID, x.TotalMarks = "e40", 40
	if err := e.exams.PutExam(context.Background(), x); err != nil {
		t.Fatal(err)
	}
	e.seedRes(t, "e40", "s1", store.AttemptSubmitted, nil, []string{"q3"}, ip(69), ip(70))
	e.clk.Set(t0.Add(time.Hour))
	v, m := e.result(t, e.student, "e40")
	raw := marshal(t, v)
	if m["score"] != 34.5 || m["scoreBeforePeeks"] != float64(35) || m["maxScore"] != float64(40) || m["peeksUsed"] != float64(1) {
		t.Fatalf("%s", raw)
	}
	if !strings.Contains(raw, `"score":34.5`) || !strings.Contains(raw, `"scoreBeforePeeks":35,`) || !strings.Contains(raw, `"maxScore":40,`) ||
		m["exam"].(map[string]any)["maxPeeks"] != float64(3) {
		t.Fatalf("number rendering: %s", raw)
	}
}

func TestResultRevealedQuestionsKeepKeyWhenHidden(t *testing.T) {
	e := resEnv(t, "mcq")
	e.seedRes(t, "e1", "s1", store.AttemptSubmitted, map[string]string{"q3": "o0", "q1": "o1"}, []string{"q1"}, ip(5), ip(6))
	e.clk.Set(t0.Add(time.Hour))
	_, m := e.result(t, e.student, "e1")
	if m["keyVisible"] != false {
		t.Fatal("key must stay hidden")
	}
	qs := resQs(t, m)
	if qs[1]["id"] != "q1" || qs[1]["revealed"] != true || qs[1]["correctAnswer"] != "o1" || qs[1]["correct"] != true {
		t.Fatalf("revealed q1: %v", qs[1])
	}
	for _, i := range []int{0, 2} {
		for _, k := range []string{"correct", "correctAnswer", "modelAnswer"} {
			if _, ok := qs[i][k]; ok {
				t.Fatalf("unrevealed %v leaks %s", qs[i]["id"], k)
			}
		}
		if qs[i]["revealed"] != false {
			t.Fatalf("revealed flag must be present and false: %v", qs[i])
		}
	}
}

func TestResultFreeTextScoresNull(t *testing.T) {
	e := resEnv(t, "free_text")
	e.seedRes(t, "e1", "s1", store.AttemptSubmitted, map[string]string{"q3": "<b>x</b> & \"y\""}, nil, nil, nil)
	e.clk.Set(closes.Add(time.Minute))
	v, m := e.result(t, e.student, "e1")
	raw := marshal(t, v)
	if score, ok := m["score"]; !ok || score != nil || m["scoreBeforePeeks"] != nil {
		t.Fatalf("%s", raw)
	}
	for _, q := range resQs(t, m) {
		for _, k := range []string{"correct", "correctAnswer", "options"} {
			if _, ok := q[k]; ok {
				t.Fatalf("free text carries %s: %v", k, q)
			}
		}
		if q["modelAnswer"] != secretModel+"-"+q["id"].(string) {
			t.Fatalf("model answer: %v", q)
		}
	}
	if resQs(t, m)[0]["yourAnswer"] != `<b>x</b> & "y"` {
		t.Fatalf("answer altered: %v", resQs(t, m)[0]["yourAnswer"])
	}
}

func TestResultCreatorCanReadOwnWhenKeyVisible(t *testing.T) {
	e := resEnv(t, "mcq")
	e.seedRes(t, "e1", "t1", store.AttemptInProgress, nil, nil, nil, nil)
	e.clk.Set(t0.Add(time.Hour))
	_, err := e.svc.Result(context.Background(), e.teacher, "e1")
	wantStatus(t, err, 409)
	if err := e.attempts.Submit(context.Background(), store.Attempt{ExamID: "e1", UserID: "t1", Status: store.AttemptSubmitted,
		Answers: map[string]string{"q3": "o0"}, Revealed: []string{}, ScoreHalves: ip(6), ScoreBeforePeeksHalves: ip(6),
		SubmittedAt: &t0, CreatedAt: t0}); err != nil {
		t.Fatal(err)
	}
	_, m := e.result(t, e.teacher, "e1")
	qs := resQs(t, m)
	if m["keyVisible"] != true || m["score"] != float64(3) || qs[0]["correct"] != true || qs[0]["correctAnswer"] != "o0" {
		t.Fatalf("%v", m)
	}
}

func TestResultCorrectAnswerIsOptionTextAndStoredOrder(t *testing.T) {
	e := resEnv(t, "mcq")
	e.seedRes(t, "e1", "s1", store.AttemptSubmitted, map[string]string{"q1": "o1"}, nil, ip(4), ip(4))
	e.clk.Set(closes.Add(time.Hour))
	_, m := e.result(t, e.student, "e1")
	x := e.exam(t, "e1")
	qs := resQs(t, m)
	for i, q := range x.Questions {
		if qs[i]["id"] != q.ID || qs[i]["correctAnswer"] != q.Options[*q.CorrectIndex] || qs[i]["text"] != q.Text || qs[i]["marks"] != float64(q.Marks) {
			t.Fatalf("question %d: %v", i, qs[i])
		}
		if _, isIdx := qs[i]["correctIndex"]; isIdx {
			t.Fatal("correctIndex must not be sent")
		}
	}
	if qs[1]["yourAnswer"] != "o1" || qs[1]["correct"] != true {
		t.Fatalf("%v", qs[1])
	}
}

func TestResultIsReadOnlyForSubmittedAttempt(t *testing.T) {
	e := resEnv(t, "mcq")
	e.seedRes(t, "e1", "s1", store.AttemptSubmitted, map[string]string{"q3": "o0"}, []string{"q2"}, ip(3), ip(6))
	e.clk.Set(closes.Add(time.Hour))
	before, _ := e.attempts.GetAttempt(context.Background(), "e1", "s1")
	e.result(t, e.student, "e1")
	after, _ := e.attempts.GetAttempt(context.Background(), "e1", "s1")
	if marshal(t, before) != marshal(t, after) {
		t.Fatalf("row changed: %s vs %s", marshal(t, before), marshal(t, after))
	}
}
