package memory_test

import (
	"context"
	"testing"
	"time"

	"github.com/gokulkumar/exam-assistant/backend/internal/store"
	"github.com/gokulkumar/exam-assistant/backend/internal/store/memory"
)

// The memory stores must not alias caller data: a service mutating a returned exam or
// attempt must not change what is stored.
func TestMemoryStoresReturnCopies(t *testing.T) {
	ctx := context.Background()
	zero := 0
	es := memory.NewExamStore()
	_ = es.PutExam(ctx, store.Exam{ID: "e", CreatorID: "u", Questions: []store.Question{{ID: "q", Options: []string{"a"}, CorrectIndex: &zero}}})
	got, _ := es.GetExam(ctx, "e")
	got.Questions[0].Options[0] = "changed"
	*got.Questions[0].CorrectIndex = 9
	again, _ := es.GetExam(ctx, "e")
	if again.Questions[0].Options[0] != "a" || *again.Questions[0].CorrectIndex != 0 {
		t.Fatalf("exam aliased: %+v", again)
	}

	as := memory.NewAttemptStore()
	_, _ = as.PutAttemptIfAbsent(ctx, store.Attempt{ExamID: "e", UserID: "u"})
	answers := map[string]string{"q": "a"}
	_ = as.Save(ctx, "e", "u", 1, answers, 0, time.Now())
	answers["q"] = "mutated"
	a, _ := as.GetAttempt(ctx, "e", "u", 1)
	a.Answers["q"] = "mutated too"
	b, _ := as.GetAttempt(ctx, "e", "u", 1)
	if b.Answers["q"] != "a" {
		t.Fatalf("attempt aliased: %+v", b)
	}
}
