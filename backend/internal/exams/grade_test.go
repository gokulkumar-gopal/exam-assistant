package exams_test

import (
	"testing"

	"github.com/gokulkumar/exam-assistant/backend/internal/exams"
	"github.com/gokulkumar/exam-assistant/backend/internal/store"
)

// gradeExam has questions q1 (1 mark, correct "A"), q2 (2 marks, correct "B"), q3 (3 marks, correct "c").
func gradeExam(typ string) store.Exam {
	mk := func(id string, marks, ci int) store.Question {
		q := store.Question{ID: id, Marks: marks, Text: id}
		if typ == "mcq" {
			c := ci
			q.Options, q.CorrectIndex = []string{"A", "B", "c", "D"}, &c
		} else {
			q.ModelAnswer = "model"
		}
		return q
	}
	return store.Exam{ID: "g", Type: typ, TotalMarks: 6, Questions: []store.Question{mk("q1", 1, 0), mk("q2", 2, 1), mk("q3", 3, 2)}}
}

func TestGradeMCQHalfMarks(t *testing.T) {
	for _, c := range []struct {
		name         string
		answers      map[string]string
		revealed     []string
		score, befor int
	}{
		{"all correct is 2x total", map[string]string{"q1": "A", "q2": "B", "q3": "c"}, nil, 12, 12},
		{"unanswered", map[string]string{}, nil, 0, 0},
		{"nil answers", nil, nil, 0, 0},
		{"wrong option", map[string]string{"q1": "B", "q2": "A", "q3": "D"}, nil, 0, 0},
		{"trailing space is wrong", map[string]string{"q1": "A ", "q2": "B", "q3": ""}, nil, 4, 4},
		{"different case is wrong", map[string]string{"q1": "a", "q2": "b", "q3": "C"}, nil, 0, 0},
		{"leading space is wrong", map[string]string{"q1": " A"}, nil, 0, 0},
		{"text not among options", map[string]string{"q1": "Z", "q2": "banana", "q3": "o2"}, nil, 0, 0},
		{"one correct", map[string]string{"q2": "B"}, nil, 4, 4},
		{"revealed 1-mark correct is 1 half", map[string]string{"q1": "A"}, []string{"q1"}, 1, 2},
		{"revealed 3-mark correct is 3 halves", map[string]string{"q3": "c"}, []string{"q3"}, 3, 6},
		{"revealed and wrong stays 0", map[string]string{"q2": "A"}, []string{"q2"}, 0, 0},
		{"revealed unanswered stays 0", map[string]string{}, []string{"q2"}, 0, 0},
		{"mixed", map[string]string{"q1": "A", "q2": "B", "q3": "c"}, []string{"q2"}, 12 - 2, 12},
		{"all revealed all correct is half", map[string]string{"q1": "A", "q2": "B", "q3": "c"}, []string{"q1", "q2", "q3"}, 6, 12},
		{"duplicate and unknown revealed ids", map[string]string{"q1": "A"}, []string{"q1", "q1", "nope"}, 1, 2},
		{"extra answer keys ignored", map[string]string{"q1": "A", "ghost": "A"}, nil, 2, 2},
	} {
		t.Run(c.name, func(t *testing.T) {
			s, b := exams.Grade(gradeExam("mcq"), c.answers, c.revealed)
			if s == nil || b == nil || *s != c.score || *b != c.befor {
				t.Fatalf("got %v/%v, want %d/%d", deref(s), deref(b), c.score, c.befor)
			}
		})
	}
}

func deref(p *int) any {
	if p == nil {
		return nil
	}
	return *p
}

func TestGradeMCQNeverPanicsOnBrokenQuestions(t *testing.T) {
	x := gradeExam("mcq")
	x.Questions[0].CorrectIndex = nil
	bad := 9
	x.Questions[1].CorrectIndex = &bad
	neg := -1
	x.Questions[2].CorrectIndex = &neg
	s, b := exams.Grade(x, map[string]string{"q1": "A", "q2": "B", "q3": "c"}, nil)
	if s == nil || b == nil || *s != 0 || *b != 0 {
		t.Fatalf("%v %v", deref(s), deref(b))
	}
	s, b = exams.Grade(store.Exam{Type: "mcq"}, map[string]string{"x": "y"}, []string{"z"})
	if s == nil || *s != 0 || b == nil || *b != 0 {
		t.Fatalf("empty exam: %v %v", deref(s), deref(b))
	}
}

func TestGradeFreeTextNil(t *testing.T) {
	s, b := exams.Grade(gradeExam("free"), map[string]string{"q1": "model", "q2": "x"}, []string{"q1"})
	if s != nil || b != nil {
		t.Fatalf("free text must not be scored: %v %v", deref(s), deref(b))
	}
}
