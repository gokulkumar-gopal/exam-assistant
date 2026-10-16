package memory

import (
	"context"
	"slices"
	"sort"
	"sync"

	"github.com/gokulkumar/exam-assistant/backend/internal/store"
)

// ExamStore is an in-memory store.ExamStore.
type ExamStore struct {
	mu    sync.Mutex
	exams map[string]store.Exam
}

var _ store.ExamStore = (*ExamStore)(nil)

// NewExamStore returns an empty ExamStore.
func NewExamStore() *ExamStore { return &ExamStore{exams: map[string]store.Exam{}} }

func copyExam(e store.Exam) store.Exam {
	qs := make([]store.Question, len(e.Questions))
	for i, q := range e.Questions {
		q.Options = slices.Clone(q.Options)
		if q.CorrectIndex != nil {
			c := *q.CorrectIndex
			q.CorrectIndex = &c
		}
		qs[i] = q
	}
	if e.Questions == nil {
		qs = nil
	}
	e.Questions = qs
	return e
}

func (s *ExamStore) PutExam(_ context.Context, e store.Exam) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.exams[e.ID]; ok {
		return store.ErrConflict
	}
	s.exams[e.ID] = copyExam(e)
	return nil
}

func (s *ExamStore) GetExam(_ context.Context, id string) (store.Exam, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.exams[id]
	if !ok {
		return store.Exam{}, store.ErrNotFound
	}
	return copyExam(e), nil
}

func (s *ExamStore) GetExams(_ context.Context, ids []string) (map[string]store.Exam, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := map[string]store.Exam{}
	for _, id := range ids {
		if e, ok := s.exams[id]; ok {
			out[id] = copyExam(e)
		}
	}
	return out, nil
}

func (s *ExamStore) ListByCreator(_ context.Context, creatorID string, limit int) ([]store.ExamSummary, error) {
	if limit <= 0 || limit > store.MaxListByCreator {
		limit = store.MaxListByCreator
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []store.ExamSummary
	for _, e := range s.exams {
		if e.CreatorID == creatorID {
			out = append(out, store.ExamSummary{ID: e.ID, CreatorID: e.CreatorID, Title: e.Title, Type: e.Type,
				TotalMarks: e.TotalMarks, ClosesAt: e.ClosesAt, CreatedAt: e.CreatedAt})
		}
	}
	// ID descending, like the creator-index (sort key id; ULIDs are time-ordered).
	sort.Slice(out, func(i, j int) bool { return out[i].ID > out[j].ID })
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}
