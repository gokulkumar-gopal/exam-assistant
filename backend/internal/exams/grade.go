package exams

import "github.com/gokulkumar/exam-assistant/backend/internal/store"

// Grade scores an attempt in half-marks (backend-spec 9.3, 9.6). MCQ only: a question is correct
// when the stored answer equals options[correctIndex] byte for byte (no trimming, no case
// folding). A correct question earns 2*marks halves; if it was revealed it earns half of that,
// marks halves (integer arithmetic: a 1-mark revealed question is 1 half). beforeHalves is the
// same score as if nothing had been revealed. Free-text exams return nil, nil: they are not
// auto-graded. Unknown question ids in answers or revealed are ignored; a question without a
// usable correctIndex scores 0.
func Grade(exam store.Exam, answers map[string]string, revealed []string) (scoreHalves, beforeHalves *int) {
	if exam.Type != "mcq" {
		return nil, nil
	}
	peeked := make(map[string]bool, len(revealed))
	for _, id := range revealed {
		peeked[id] = true
	}
	score, before := 0, 0
	for _, q := range exam.Questions {
		ans, ok := answers[q.ID]
		if !ok || q.CorrectIndex == nil || *q.CorrectIndex < 0 || *q.CorrectIndex >= len(q.Options) {
			continue
		}
		if ans != q.Options[*q.CorrectIndex] {
			continue
		}
		before += 2 * q.Marks
		if peeked[q.ID] {
			score += q.Marks
		} else {
			score += 2 * q.Marks
		}
	}
	return &score, &before
}
