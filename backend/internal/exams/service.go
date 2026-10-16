// Package exams implements exam creation and the rules around it (backend-spec 8.5, 9.1).
package exams

import (
	"crypto/rand"
	"log/slog"

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
	log      *slog.Logger
	maxPend  int
	fin      Finalizer // lazy finalize hook (exams.NewFinalizer in cmd); nil means none
}

// NewService builds a Service.
func NewService(users store.UserStore, exams store.ExamStore, attempts store.AttemptStore, clk clock.Clock) *Service {
	return &Service{users: users, exams: exams, attempts: attempts, clk: clk, log: slog.Default(), maxPend: MaxPendingRows}
}

func newID(clk clock.Clock) string {
	return ulid.MustNew(ulid.Timestamp(clk.Now()), rand.Reader).String()
}

// HasFinalizer reports whether the lazy-finalize hook is wired (checked by the cmd wiring tests).
func (s *Service) HasFinalizer() bool { return s.fin != nil }
