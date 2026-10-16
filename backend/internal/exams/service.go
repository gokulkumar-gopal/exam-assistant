// Package exams implements exam creation and the rules around it (backend-spec 8.5, 9.1).
package exams

import (
	"crypto/rand"

	"github.com/oklog/ulid/v2"

	"github.com/gokulkumar/exam-assistant/backend/internal/clock"
	"github.com/gokulkumar/exam-assistant/backend/internal/store"
)

// Service holds the exam use cases.
type Service struct {
	users    store.UserStore
	exams    store.ExamStore
	attempts store.AttemptStore
	clk      clock.Clock
	fin      Finalizer // lazy finalize hook; nil means none (wired in B-4.4)
}

// NewService builds a Service.
func NewService(users store.UserStore, exams store.ExamStore, attempts store.AttemptStore, clk clock.Clock) *Service {
	return &Service{users: users, exams: exams, attempts: attempts, clk: clk}
}

func newID(clk clock.Clock) string {
	return ulid.MustNew(ulid.Timestamp(clk.Now()), rand.Reader).String()
}
