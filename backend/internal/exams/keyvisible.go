package exams

import (
	"time"

	"github.com/gokulkumar/exam-assistant/backend/internal/store"
)

// IsOver reports whether the exam no longer accepts work: now >= closesAt+60s (backend-spec 9.2).
func IsOver(exam store.Exam, now time.Time) bool { return over(exam, now) }

// KeyVisible is the one answer-key rule (backend-spec 9.4). a is the caller's own attempt row, or
// nil if there is none. It is a pure function of its arguments and the caller must already have
// access to the exam.
func KeyVisible(exam store.Exam, a *store.Attempt, userID string, now time.Time) bool {
	if IsOver(exam, now) {
		return true
	}
	if exam.CreatorID != userID {
		return false
	}
	// The creator sees the key unless they are taking their own exam and have not finished: no
	// row, or a submitted row. A declined row means they are not taking it (an author has the key).
	return a == nil || a.Status == store.AttemptSubmitted || a.Status == store.AttemptDeclined
}
