package exams_test

import (
	"testing"
	"time"

	"github.com/gokulkumar/exam-assistant/backend/internal/exams"
	"github.com/gokulkumar/exam-assistant/backend/internal/store"
)

func TestKeyVisibleTable(t *testing.T) {
	x := store.Exam{ID: "e1", CreatorID: "t1", ClosesAt: closes}
	au := closes.Add(time.Minute) // acceptingUntil
	before := au.Add(-time.Nanosecond)
	at := func(s string) *store.Attempt {
		return &store.Attempt{AttemptNo: 1, ExamID: "e1", UserID: "x", Status: s}
	}
	tests := []struct {
		name string
		a    *store.Attempt
		user string
		now  time.Time
		want bool
	}{
		{"assignee in_progress before acceptingUntil", at(store.AttemptInProgress), "s1", before, false},
		{"assignee not_started before acceptingUntil", at(store.AttemptNotStarted), "s1", before, false},
		{"assignee submitted before acceptingUntil", at(store.AttemptSubmitted), "s1", before, false},
		{"assignee declined before acceptingUntil", at(store.AttemptDeclined), "s1", before, false},
		{"assignee nil attempt before acceptingUntil", nil, "s1", before, false},
		{"assignee at acceptingUntil", at(store.AttemptInProgress), "s1", au, true},
		{"assignee after over", at(store.AttemptNotStarted), "s1", au.Add(time.Hour), true},
		{"assignee submitted after over", at(store.AttemptSubmitted), "s1", au.Add(time.Hour), true},
		{"creator no attempt row", nil, "t1", t0, true},
		{"creator no attempt row at before", nil, "t1", before, true},
		{"creator submitted own attempt", at(store.AttemptSubmitted), "t1", before, true},
		{"creator in_progress", at(store.AttemptInProgress), "t1", before, false},
		{"creator not_started", at(store.AttemptNotStarted), "t1", before, false},
		{"creator in_progress at acceptingUntil", at(store.AttemptInProgress), "t1", au, true},
		{"creator not_started after over", at(store.AttemptNotStarted), "t1", au.Add(time.Hour), true},
		{"creator declined own practice row", at(store.AttemptDeclined), "t1", before, true},
		{"stranger-like nil before", nil, "z9", before, false},
		{"stranger-like nil over", nil, "z9", au, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := exams.KeyVisible(x, tc.a, tc.user, tc.now); got != tc.want {
				t.Fatalf("KeyVisible = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestIsOverBoundary(t *testing.T) {
	x := store.Exam{ClosesAt: closes}
	au := closes.Add(60 * time.Second)
	if exams.IsOver(x, au.Add(-time.Nanosecond)) || !exams.IsOver(x, au) || !exams.IsOver(x, au.Add(time.Nanosecond)) {
		t.Fatal("IsOver boundary")
	}
}
