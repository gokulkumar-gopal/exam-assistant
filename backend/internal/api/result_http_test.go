package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"
	"time"
)

func (e *declEnv) resultReq(c *http.Cookie) *httptest.ResponseRecorder {
	return e.do("GET", "/exams/as-key-1/result", "", "", c, "")
}

func TestResultOverHTTP(t *testing.T) {
	e, qid := asSetup(t)
	if rec := e.resultReq(nil); rec.Code != http.StatusUnauthorized {
		t.Errorf("no session: %d", rec.Code)
	}
	rec := e.resultReq(e.student)
	if rec.Code != http.StatusConflict || errCode(t, rec) != "conflict" || !strings.Contains(rec.Body.String(), "Submit the exam to see results.") {
		t.Fatalf("unfinished: %d %s", rec.Code, rec.Body)
	}
	if rec := e.submitReq(`{"answers":{"`+qid+`":"b"},"lastQuestionIndex":0}`, e.student); rec.Code != 200 {
		t.Fatal(rec.Body)
	}
	// submitted, exam still open: scores, no key, byte-level
	rec = e.resultReq(e.student)
	if rec.Code != http.StatusOK || rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("%d %v %s", rec.Code, rec.Header(), rec.Body)
	}
	for _, leak := range []string{"correct", "modelAnswer", "correctIndex"} {
		if strings.Contains(rec.Body.String(), leak) {
			t.Fatalf("hidden-key leak %q: %s", leak, rec.Body)
		}
	}
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	keys := func(m map[string]any) string {
		var ks []string
		for k := range m {
			ks = append(ks, k)
		}
		sort.Strings(ks)
		return strings.Join(ks, ",")
	}
	if got := keys(out); got != "exam,keyAvailableAt,keyVisible,maxScore,peeksUsed,questions,score,scoreBeforePeeks,serverTime,status" {
		t.Fatalf("top keys: %s", got)
	}
	if got := keys(out["exam"].(map[string]any)); got != "acceptingUntil,closesAt,id,maxPeeks,title,totalMarks,type" {
		t.Fatalf("exam keys: %s", got)
	}
	q := out["questions"].([]any)[0].(map[string]any)
	if got := keys(q); got != "id,marks,options,revealed,text,yourAnswer" {
		t.Fatalf("hidden question keys: %s", got)
	}
	au := authT0.Add(time.Hour + time.Minute).UTC().Format(time.RFC3339)
	if out["status"] != "submitted" || out["score"] != float64(1) || out["maxScore"] != float64(1) || out["keyVisible"] != false ||
		out["keyAvailableAt"] != au || out["serverTime"] != authT0.UTC().Format(time.RFC3339) || q["yourAnswer"] != "b" {
		t.Fatalf("%s", rec.Body)
	}
	// once over: key present
	e.clk.Set(authT0.Add(time.Hour + time.Minute))
	rec = e.resultReq(e.student)
	if rec.Code != 200 {
		t.Fatal(rec.Body)
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	q = out["questions"].([]any)[0].(map[string]any)
	if got := keys(q); got != "correct,correctAnswer,id,marks,modelAnswer,options,revealed,text,yourAnswer" ||
		q["correctAnswer"] != "b" || q["correct"] != true || out["keyVisible"] != true || out["keyAvailableAt"] != nil {
		t.Fatalf("visible: %s", rec.Body)
	}
}

func TestResult404sAreByteIdenticalOverHTTP(t *testing.T) {
	e, _ := asSetup(t)
	if rec := e.post("/exams/as-key-1/decline", "", e.student); rec.Code != http.StatusNoContent {
		t.Fatal(rec.Body)
	}
	declined := e.resultReq(e.student)
	creator := e.resultReq(e.teacher) // the creator has no attempt row of their own
	stranger := e.resultReq(e.other)
	missing := e.do("GET", "/exams/nope/result", "", "", e.student, "")
	for _, r := range []*httptest.ResponseRecorder{declined, creator, stranger, missing} {
		if r.Code != http.StatusNotFound || r.Body.String() != missing.Body.String() || r.Header().Get("Cache-Control") != "no-store" ||
			r.Header().Get("Content-Type") != missing.Header().Get("Content-Type") {
			t.Fatalf("%d %s vs %s", r.Code, r.Body, missing.Body)
		}
	}
}

func TestResultMethodNotAllowedOverHTTP(t *testing.T) {
	e, _ := asSetup(t)
	for _, m := range []string{"POST", "PUT", "DELETE"} {
		if rec := e.do(m, "/exams/as-key-1/result", "", "", e.student, ""); rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s: %d %s", m, rec.Code, rec.Body)
		}
	}
}

func TestResultLiteralAnswerOverHTTP(t *testing.T) {
	// the answer text is stored and returned as written (JSON-escaped on the wire, literal once decoded)
	e, qid := asSetup(t)
	lit := `<b>x</b>`
	if rec := e.submitReq(`{"answers":{"`+qid+`":"`+lit+`"},"lastQuestionIndex":0}`, e.student); rec.Code != 200 {
		t.Fatal(rec.Body)
	}
	var out struct{ Questions []struct{ YourAnswer string } }
	rec := e.resultReq(e.student)
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil || len(out.Questions) != 1 || out.Questions[0].YourAnswer != lit {
		t.Fatalf("%v %s", err, rec.Body)
	}
}
