package store

import (
	"context"
	"fmt"
	"time"
)

// Exam is the logical exam entity (backend-spec 6.1). Questions are embedded.
type Exam struct {
	ID, CreatorID, Title string
	Type                 string // "mcq" | "free"
	TotalMarks           int
	ClosesAt             time.Time // acceptance ends at ClosesAt + 60 s (backend-spec 9.2)
	MaxPeeks             int       // 0..10; 0 turns peeking off
	Questions            []Question
	CreatedAt            time.Time
}

// ExamSummary is what the creator-index projects (backend-spec 6.2): never the questions
// and never MaxPeeks. Every field must be a key or an INCLUDE attribute of that index.
type ExamSummary struct {
	ID, CreatorID, Title string
	Type                 string
	TotalMarks           int
	ClosesAt, CreatedAt  time.Time
}

// Attempt statuses. "missed" is derived, never stored (backend-spec 6.1).
const (
	AttemptNotStarted = "not_started"
	AttemptInProgress = "in_progress"
	AttemptSubmitted  = "submitted"
	AttemptDeclined   = "declined"
)

// ErrSubmitted and ErrDeclined say why a write was refused; both satisfy
// errors.Is(err, ErrPrecondition), so callers that do not care keep working.
var (
	ErrSubmitted = fmt.Errorf("%w: attempt is submitted", ErrPrecondition)
	ErrDeclined  = fmt.Errorf("%w: attempt is declined", ErrPrecondition)
)

// MaxListByCreator caps ListByCreator results.
const MaxListByCreator = 50

// Attempt is one (exam, user) row: assignment and attempt in one (backend-spec 6.1).
// Scores are in half-marks.
type Attempt struct {
	ExamID, UserID         string
	Status                 string
	Answers                map[string]string // questionID -> option text or free text; never nil from a store
	LastQuestionIndex      int
	Revealed               []string // question IDs the taker peeked at; never nil from a store
	ScoreHalves            *int     // MCQ only
	ScoreBeforePeeksHalves *int
	StartedAt, SavedAt     *time.Time
	SubmittedAt            *time.Time
	AutoSubmitted          bool
	CreatedAt              time.Time
}

// AttemptSummary is what the user-index projects (backend-spec 6.2): never the answers.
// Every field must be a key or an INCLUDE attribute of that index.
type AttemptSummary struct {
	ExamID, UserID         string
	Status                 string
	ScoreHalves            *int
	ScoreBeforePeeksHalves *int
	SubmittedAt            *time.Time
}

// RevealResult is the outcome of a recorded peek. Revealed is the full list after the call;
// Charged is false when the question had already been revealed (idempotent repeat).
type RevealResult struct {
	Revealed []string
	Charged  bool
}

// ExamStore persists exams.
type ExamStore interface {
	// PutExam creates the exam; ErrConflict if the ID exists.
	PutExam(ctx context.Context, e Exam) error
	// GetExam returns ErrNotFound for a missing exam. The read is consistent.
	GetExam(ctx context.Context, id string) (Exam, error)
	// GetExams returns found exams keyed by ID; missing ones are omitted.
	GetExams(ctx context.Context, ids []string) (map[string]Exam, error)
	// ListByCreator returns the creator's exams newest first (CreatedAt descending, ties by ID
	// descending; IDs may be client-chosen UUIDs, so they do not order by time), at most
	// MaxListByCreator (limit <= 0 or above means the cap).
	ListByCreator(ctx context.Context, creatorID string, limit int) ([]ExamSummary, error)
}

// AttemptStore persists attempts. Writes never change index-projected attributes except
// where the status itself changes (start, submit, decline, first reveal).
type AttemptStore interface {
	// PutAttemptIfAbsent writes the row unless it exists; created=false (nil error) when it does.
	PutAttemptIfAbsent(ctx context.Context, a Attempt) (created bool, err error)
	// GetAttempt returns ErrNotFound for a missing row. The read is consistent.
	GetAttempt(ctx context.Context, examID, userID string) (Attempt, error)
	// ListByUser returns every attempt of the user, projected as the user-index holds it
	// (GSI reads are eventually consistent), ordered by exam ID.
	ListByUser(ctx context.Context, userID string) ([]AttemptSummary, error)
	// ListByExam returns every attempt of the exam, ordered by user ID.
	ListByExam(ctx context.Context, examID string) ([]Attempt, error)
	// Save stores answers and the last question index. The first save of a not_started attempt
	// starts it (status in_progress, StartedAt = now); later saves change only answers,
	// LastQuestionIndex and SavedAt. ErrNotFound if missing; ErrSubmitted or ErrDeclined (both
	// ErrPrecondition) if the attempt is submitted or declined.
	Save(ctx context.Context, examID, userID string, answers map[string]string, lastIdx int, now time.Time) error
	// Submit writes the final state of a (Status, Answers, LastQuestionIndex, scores,
	// SubmittedAt, SavedAt, AutoSubmitted; StartedAt only if the stored one is unset; never
	// Revealed). ErrNotFound if missing; ErrPrecondition if already submitted or declined.
	// If a.Revealed is non-nil it is the peeks the score was computed from: when the stored list
	// has a different length (a Reveal landed after the caller read the row) nothing is written
	// and the error is ErrConflict, so the caller re-reads and regrades. nil skips the check.
	Submit(ctx context.Context, a Attempt) error
	// Decline moves a not_started attempt to declined; idempotent when already declined.
	// ErrNotFound if missing; ErrPrecondition from in_progress or submitted.
	Decline(ctx context.Context, examID, userID string) error
	// Reveal atomically records questionID as revealed if it is not yet and fewer than max
	// are revealed, starting a not_started attempt. Already revealed: success, Charged=false,
	// even at the cap. ErrConflict when the cap is reached; ErrPrecondition when the attempt
	// is submitted (ErrSubmitted) or declined (ErrDeclined); ErrNotFound if missing. A reveal starts a
	// not_started attempt (StartedAt = now) but does not set SavedAt. Whether the exam is open, the
	// question exists and max > 0 are the caller's checks.
	Reveal(ctx context.Context, examID, userID, questionID string, max int, now time.Time) (RevealResult, error)
}
