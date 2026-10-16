package exams

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/gokulkumar/exam-assistant/backend/internal/apperr"
	"github.com/gokulkumar/exam-assistant/backend/internal/store"
)

// ResultExam is the exam header of GET /exams/{id}/result (backend-spec 8.5).
type ResultExam struct {
	ID             string    `json:"id"`
	Title          string    `json:"title"`
	Type           string    `json:"type"`
	TotalMarks     int       `json:"totalMarks"`
	ClosesAt       time.Time `json:"closesAt"`
	AcceptingUntil time.Time `json:"acceptingUntil"`
	MaxPeeks       int       `json:"maxPeeks"`
}

// ResultQuestion is one question of a result. The key fields are set only by resultQuestion.
type ResultQuestion struct {
	ID            string          `json:"id"`
	Marks         int             `json:"marks"`
	Text          string          `json:"text"`
	Options       []string        `json:"options,omitempty"`
	YourAnswer    string          `json:"yourAnswer"`
	Correct       *bool           `json:"correct,omitempty"`
	CorrectAnswer *string         `json:"correctAnswer,omitempty"`
	ModelAnswer   json.RawMessage `json:"modelAnswer,omitempty"`
	Revealed      bool            `json:"revealed"`
}

// ResultView is the body of GET /exams/{id}/result, minus serverTime.
type ResultView struct {
	Exam             ResultExam       `json:"exam"`
	Status           string           `json:"status"`
	Score            *float64         `json:"score"`
	ScoreBeforePeeks *float64         `json:"scoreBeforePeeks"`
	MaxScore         int              `json:"maxScore"`
	PeeksUsed        int              `json:"peeksUsed"`
	KeyVisible       bool             `json:"keyVisible"`
	KeyAvailableAt   *time.Time       `json:"keyAvailableAt"`
	Questions        []ResultQuestion `json:"questions"`
}

// resultQuestion is the ONE place a stored question becomes a result question. The key fields
// (correct, correctAnswer, modelAnswer) are set only when withKey is true, so a hidden key is
// absent from the JSON. MCQ: correctAnswer is the option TEXT (options[correctIndex]), correct is
// a bool and modelAnswer is an explicit null (8.5 example). Free text: only modelAnswer; no
// correct, correctAnswer or options.
func resultQuestion(q store.Question, exam store.Exam, answer string, revealed, withKey bool) ResultQuestion {
	r := ResultQuestion{ID: q.ID, Marks: q.Marks, Text: q.Text, YourAnswer: answer, Revealed: revealed}
	if exam.Type == "mcq" {
		if len(q.Options) > 0 {
			r.Options = append([]string(nil), q.Options...)
		}
		if withKey && q.CorrectIndex != nil && *q.CorrectIndex >= 0 && *q.CorrectIndex < len(q.Options) {
			text := q.Options[*q.CorrectIndex]
			ok := answer != "" && answer == text
			r.Correct, r.CorrectAnswer = &ok, &text
			r.ModelAnswer = json.RawMessage("null")
		}
		return r
	}
	if withKey && q.ModelAnswer != "" {
		b, _ := json.Marshal(q.ModelAnswer)
		r.ModelAnswer = b
	}
	return r
}

func halvesToScore(h *int) *float64 {
	if h == nil {
		return nil
	}
	f := float64(*h) / 2
	return &f
}

// Result returns the caller's result (backend-spec 8.5, 9.3, 9.4). It needs a non-declined
// attempt row (a creator without one, a stranger, a declined user and a missing exam all get the
// same 404). An expired in_progress row is finalized first (the only write). Unsubmitted and not
// over is 409; a never-started attempt after the exam is over is "missed".
func (s *Service) Result(ctx context.Context, user store.User, examID string) (ResultView, error) {
	exam, err := s.exams.GetExam(ctx, examID)
	if errors.Is(err, store.ErrNotFound) {
		return ResultView{}, apperr.NotFound()
	}
	if err != nil {
		return ResultView{}, apperr.Internal(err)
	}
	att, err := s.attempts.GetAttempt(ctx, examID, user.ID)
	if errors.Is(err, store.ErrNotFound) {
		return ResultView{}, apperr.NotFound()
	}
	if err != nil {
		return ResultView{}, apperr.Internal(err)
	}
	if att.Status == store.AttemptDeclined {
		return ResultView{}, apperr.NotFound()
	}
	now := s.clk.Now()
	if s.fin != nil && att.Status == store.AttemptInProgress && IsOver(exam, now) {
		att, err = s.fin.FinalizeIfExpired(ctx, att, exam, now)
		if err != nil {
			return ResultView{}, apperr.Internal(err)
		}
	}
	status := att.Status
	if status != store.AttemptSubmitted {
		if !IsOver(exam, now) {
			return ResultView{}, apperr.Conflict("Submit the exam to see results.")
		}
		status = AttemptMissed
	}
	keyVisible := KeyVisible(exam, &att, user.ID, now)
	revealed := map[string]bool{}
	answers := att.Answers
	if status == AttemptMissed {
		answers = nil
	} else {
		for _, id := range att.Revealed {
			revealed[id] = true
		}
	}
	v := ResultView{
		Exam: ResultExam{ID: exam.ID, Title: exam.Title, Type: exam.Type, TotalMarks: exam.TotalMarks,
			ClosesAt: exam.ClosesAt.UTC(), AcceptingUntil: AcceptingUntil(exam).UTC(), MaxPeeks: exam.MaxPeeks},
		Status: status, MaxScore: exam.TotalMarks, PeeksUsed: len(revealed), KeyVisible: keyVisible,
		Questions: make([]ResultQuestion, 0, len(exam.Questions)),
	}
	if status == store.AttemptSubmitted && exam.Type == "mcq" {
		v.Score, v.ScoreBeforePeeks = halvesToScore(att.ScoreHalves), halvesToScore(att.ScoreBeforePeeksHalves)
	}
	if !keyVisible {
		t := AcceptingUntil(exam).UTC()
		v.KeyAvailableAt = &t
	}
	for _, q := range exam.Questions {
		v.Questions = append(v.Questions, resultQuestion(q, exam, answers[q.ID], revealed[q.ID], keyVisible || revealed[q.ID]))
	}
	return v, nil
}
