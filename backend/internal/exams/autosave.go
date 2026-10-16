package exams

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/gokulkumar/exam-assistant/backend/internal/apperr"
	"github.com/gokulkumar/exam-assistant/backend/internal/store"
)

// MaxAutosaveRunes is the longest autosaved answer (backend-spec 8.5: 5,000 characters, counted
// as characters like every other text limit in this package, not bytes).
const MaxAutosaveRunes = 5000

// MaxAutosaveBytes bounds the summed UTF-8 bytes of all answer values. It keeps the attempt item
// well under the 400 KB DynamoDB limit; the 400 KiB body cap (api package) is the outer bound.
const MaxAutosaveBytes = 350 << 10

// AutosaveInput is the body of PUT /exams/{id}/attempt. Both fields are required and every
// answer value must be a JSON string (null is rejected, never stored as an empty answer).
type AutosaveInput struct {
	Answers           map[string]string `json:"answers"`
	LastQuestionIndex *int              `json:"lastQuestionIndex"`
	nullAnswer        string            // an answer id whose JSON value was null or not a string
	badValue          bool
}

// UnmarshalJSON decodes strictly: unknown fields are errors (as Decode does elsewhere) and a
// non-string answer value is remembered so validation can answer 422 with a field error.
func (in *AutosaveInput) UnmarshalJSON(b []byte) error {
	var raw struct {
		Answers           map[string]json.RawMessage `json:"answers"`
		LastQuestionIndex *int                       `json:"lastQuestionIndex"`
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&raw); err != nil {
		return err
	}
	*in = AutosaveInput{LastQuestionIndex: raw.LastQuestionIndex}
	if raw.Answers == nil {
		return nil
	}
	in.Answers = make(map[string]string, len(raw.Answers))
	for id, v := range raw.Answers {
		var str string
		if bytes.Equal(bytes.TrimSpace(v), []byte("null")) || json.Unmarshal(v, &str) != nil {
			in.badValue, in.nullAnswer = true, id
			continue
		}
		in.Answers[id] = str
	}
	return nil
}

// Autosave replaces the caller's whole answers map (backend-spec 8.5, 9.2) and returns the
// server time of the save. Only a non-declined assignee with an attempt row may save: a creator
// without a row, a declined user, a stranger and a missing exam are the same 404.
//
// At or after acceptingUntil the answer is 409 exam_closed, after the caller's in_progress
// attempt has gone through the lazy-finalize hook. Before that, including the 60 s grace window,
// the save is accepted. MCQ answers are not checked against the options (they may be mid-edit
// text); only the question ids, the length and the index are validated.
//
// Known accepted gap (spec 8.5): the map has no sequence number, so a delayed older PUT that
// arrives after a newer one overwrites it. TestKnownGapDocumented pins this on purpose.
func (s *Service) Autosave(ctx context.Context, user store.User, examID string, in AutosaveInput) (time.Time, error) {
	a, err := s.attempts.GetAttempt(ctx, examID, user.ID)
	if errors.Is(err, store.ErrNotFound) {
		return time.Time{}, apperr.NotFound()
	}
	if err != nil {
		return time.Time{}, apperr.Internal(err)
	}
	if a.Status == store.AttemptDeclined {
		return time.Time{}, apperr.NotFound()
	}
	exam, err := s.exams.GetExam(ctx, examID)
	if errors.Is(err, store.ErrNotFound) {
		return time.Time{}, apperr.NotFound()
	}
	if err != nil {
		return time.Time{}, apperr.Internal(err)
	}
	now := s.clk.Now()
	if IsOver(exam, now) {
		if s.fin != nil && a.Status == store.AttemptInProgress {
			if _, err := s.fin.FinalizeIfExpired(ctx, a, exam, now); err != nil {
				s.log.Error("autosave: lazy finalize failed", "exam_id", examID, "err", err)
				return time.Time{}, apperr.Internal(err)
			}
		}
		return time.Time{}, apperr.ExamClosed()
	}
	if err := validateAutosave(exam, in); err != nil {
		return time.Time{}, err
	}
	err = s.attempts.Save(ctx, examID, user.ID, in.Answers, *in.LastQuestionIndex, now)
	switch {
	case err == nil:
		return now, nil
	case errors.Is(err, store.ErrSubmitted):
		return time.Time{}, apperr.Conflict("This exam has already been submitted.")
	case errors.Is(err, store.ErrDeclined), errors.Is(err, store.ErrNotFound):
		return time.Time{}, apperr.NotFound()
	}
	return time.Time{}, apperr.Internal(err)
}

func validateAutosave(exam store.Exam, in AutosaveInput) error {
	if in.Answers == nil {
		return apperr.Validation("Answers are required.", map[string]string{"answers": "Required"})
	}
	if in.badValue {
		return apperr.Validation("An answer must be text.", map[string]string{"answers": "Must be text"})
	}
	if in.LastQuestionIndex == nil {
		return apperr.Validation("The question position is required.",
			map[string]string{"lastQuestionIndex": "Required"})
	}
	if err := checkAnswers(exam, in.Answers); err != nil {
		return err
	}
	if i := *in.LastQuestionIndex; i < 0 || i >= len(exam.Questions) {
		return apperr.Validation("The question position is out of range.",
			map[string]string{"lastQuestionIndex": "Out of range"})
	}
	return nil
}

// checkAnswers validates an answers map against the exam: known question ids, 5,000 characters
// per answer and 350 KB in all. Shared by autosave and submit.
func checkAnswers(exam store.Exam, answers map[string]string) error {
	known := make(map[string]bool, len(exam.Questions))
	for _, q := range exam.Questions {
		known[q.ID] = true
	}
	total := 0
	for id, v := range answers {
		if !known[id] {
			return apperr.Validation("Answers refer to a question that is not in this exam.",
				map[string]string{"answers": "Unknown question"})
		}
		if runes(v) > MaxAutosaveRunes {
			return apperr.Validation("An answer is too long.",
				map[string]string{fmt.Sprintf("answers[%s]", id): "Too long"})
		}
		total += len(v)
	}
	if total > MaxAutosaveBytes {
		return apperr.Validation("Your answers are too long to save.", map[string]string{"answers": "Too long overall"})
	}
	return nil
}
