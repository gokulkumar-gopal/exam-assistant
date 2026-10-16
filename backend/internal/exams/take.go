package exams

import (
	"context"
	"errors"
	"time"

	"github.com/gokulkumar/exam-assistant/backend/internal/apperr"
	"github.com/gokulkumar/exam-assistant/backend/internal/store"
)

// QuestionView is the question as a client sees it. It is a separate type from store.Question on
// purpose (no embedding, no shared fields by default): the key fields are pointers that only
// questionView ever sets, so a hidden key is absent from the JSON, never a zero value.
type QuestionView struct {
	ID           string   `json:"id"`
	Marks        int      `json:"marks"`
	Text         string   `json:"text"`
	Options      []string `json:"options,omitempty"`
	CorrectIndex *int     `json:"correctIndex,omitempty"`
	ModelAnswer  *string  `json:"modelAnswer,omitempty"`
}

// ExamView is the exam of GET /exams/{id} (backend-spec 8.5).
type ExamView struct {
	ID             string         `json:"id"`
	Title          string         `json:"title"`
	Type           string         `json:"type"`
	TotalMarks     int            `json:"totalMarks"`
	ClosesAt       time.Time      `json:"closesAt"`
	AcceptingUntil time.Time      `json:"acceptingUntil"`
	MaxPeeks       int            `json:"maxPeeks"`
	CreatedBy      UserRef        `json:"createdBy"`
	Questions      []QuestionView `json:"questions"`
}

// RevealedKey is the key of one question the caller peeked at (backend-spec 8.5, 9.6).
type RevealedKey struct {
	QuestionID   string  `json:"questionId"`
	CorrectIndex *int    `json:"correctIndex,omitempty"`
	ModelAnswer  *string `json:"modelAnswer,omitempty"`
}

// AttemptView is the caller's own attempt row. It never carries scores.
type AttemptView struct {
	Status            string            `json:"status"`
	Answers           map[string]string `json:"answers"`
	LastQuestionIndex int               `json:"lastQuestionIndex"`
	SavedAt           *time.Time        `json:"savedAt"`
	Revealed          []RevealedKey     `json:"revealed"`
	PeeksLeft         int               `json:"peeksLeft"`
}

// GetView is the response body of GET /exams/{id}, minus serverTime.
type GetView struct {
	Exam       ExamView     `json:"exam"`
	Attempt    *AttemptView `json:"attempt"`
	KeyVisible bool         `json:"keyVisible"`
}

// questionView is the ONE place a stored question becomes a client question. The key fields are
// set only when withKey is true; nothing else in this package copies them.
func questionView(q store.Question, withKey bool) QuestionView {
	v := QuestionView{ID: q.ID, Marks: q.Marks, Text: q.Text}
	if len(q.Options) > 0 {
		v.Options = append([]string(nil), q.Options...)
	}
	if withKey {
		if q.CorrectIndex != nil {
			ci := *q.CorrectIndex
			v.CorrectIndex = &ci
		}
		if q.ModelAnswer != "" {
			ma := q.ModelAnswer
			v.ModelAnswer = &ma
		}
	}
	return v
}

// revealedKey is the key of a peeked question, built from the same questionView.
func revealedKey(q store.Question) RevealedKey {
	kv := questionView(q, true)
	return RevealedKey{QuestionID: q.ID, CorrectIndex: kv.CorrectIndex, ModelAnswer: kv.ModelAnswer}
}

// Get returns the exam to take or view (backend-spec 8.5, 9.4, 9.5). It is read-only: it never
// creates or updates an attempt row and never charges a peek, so printing an exam is free.
// Visible to the creator and to a caller with a non-declined attempt row; a missing exam, a
// stranger and a declined user all get the same 404. The caller's own expired in_progress
// attempt is lazily finalized first, and the finalized row is what the view shows.
func (s *Service) Get(ctx context.Context, user store.User, examID string) (GetView, error) {
	exam, err := s.exams.GetExam(ctx, examID)
	if errors.Is(err, store.ErrNotFound) {
		return GetView{}, apperr.NotFound()
	}
	if err != nil {
		return GetView{}, apperr.Internal(err)
	}
	var att *store.Attempt
	row, err := s.attempts.GetAttempt(ctx, examID, user.ID)
	switch {
	case err == nil:
		att = &row
	case !errors.Is(err, store.ErrNotFound):
		return GetView{}, apperr.Internal(err)
	}
	isCreator := exam.CreatorID == user.ID
	if !isCreator && (att == nil || att.Status == store.AttemptDeclined) {
		return GetView{}, apperr.NotFound()
	}
	by := UserRef{ID: exam.CreatorID}
	if isCreator {
		by.Name = user.Name
	} else {
		u, err := s.users.GetByID(ctx, exam.CreatorID)
		if err != nil && !errors.Is(err, store.ErrNotFound) {
			return GetView{}, apperr.Internal(err)
		}
		by.Name = u.Name // a deleted creator keeps an empty name
	}
	now := s.clk.Now()
	// Lazy finalize (backend-spec 9.2) is the one write a GET may make; it runs through the
	// Finalizer for the caller's own expired in_progress row only. Get itself never creates a
	// row and never charges a peek (9.5).
	if s.fin != nil && att != nil && att.Status == store.AttemptInProgress && IsOver(exam, now) {
		fin, err := s.fin.FinalizeIfExpired(ctx, *att, exam, now)
		if err != nil {
			return GetView{}, apperr.Internal(err)
		}
		att = &fin
	}
	keyVisible := KeyVisible(exam, att, user.ID, now)
	revealed := map[string]bool{}
	if att != nil {
		for _, id := range att.Revealed {
			revealed[id] = true
		}
	}
	v := GetView{KeyVisible: keyVisible, Exam: ExamView{
		ID: exam.ID, Title: exam.Title, Type: exam.Type, TotalMarks: exam.TotalMarks,
		ClosesAt: exam.ClosesAt.UTC(), AcceptingUntil: AcceptingUntil(exam).UTC(),
		MaxPeeks: exam.MaxPeeks, CreatedBy: by, Questions: make([]QuestionView, 0, len(exam.Questions)),
	}}
	for _, q := range exam.Questions {
		v.Exam.Questions = append(v.Exam.Questions, questionView(q, keyVisible || revealed[q.ID]))
	}
	if att != nil {
		av := AttemptView{
			Status: att.Status, Answers: map[string]string{}, LastQuestionIndex: att.LastQuestionIndex,
			Revealed: []RevealedKey{}, PeeksLeft: max(0, exam.MaxPeeks-len(att.Revealed)),
		}
		for k, a := range att.Answers {
			av.Answers[k] = a
		}
		if att.SavedAt != nil {
			t := att.SavedAt.UTC()
			av.SavedAt = &t
		}
		for _, q := range exam.Questions { // stored order, existing questions only
			if revealed[q.ID] {
				av.Revealed = append(av.Revealed, revealedKey(q))
			}
		}
		v.Attempt = &av
	}
	return v, nil
}
