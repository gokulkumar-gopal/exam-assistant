package exams

import (
	"sort"

	"github.com/gokulkumar/exam-assistant/backend/internal/store"
)

// userRun is one user's attempts on one exam, oldest first (backend-spec 6.1: one row per
// attempt). Only the latest attempt of a run can be unfinished: a retake starts only after the
// previous attempt is submitted (8.5, Retake).
type userRun []store.Attempt

// groupByUser splits every attempt row of an exam into one run per user, ordered by user ID,
// each run ordered by attempt number.
func groupByUser(rows []store.Attempt) []userRun {
	by := map[string]userRun{}
	var ids []string
	for _, a := range rows {
		if _, ok := by[a.UserID]; !ok {
			ids = append(ids, a.UserID)
		}
		by[a.UserID] = append(by[a.UserID], a)
	}
	sort.Strings(ids)
	out := make([]userRun, 0, len(ids))
	for _, id := range ids {
		r := by[id]
		sort.Slice(r, func(i, j int) bool { return r[i].AttemptNo < r[j].AttemptNo })
		out = append(out, r)
	}
	return out
}

func (r userRun) latest() store.Attempt { return r[len(r)-1] }

// best returns the submitted attempt with the highest score (the earliest on a tie). Without
// scores (a free-text exam) it is the latest submitted attempt. ok is false when none is submitted.
func (r userRun) best() (best store.Attempt, ok bool) {
	for _, a := range r {
		if a.Status != store.AttemptSubmitted {
			continue
		}
		switch {
		case !ok:
			best, ok = a, true
		case a.ScoreHalves == nil && best.ScoreHalves == nil:
			best = a // no scores: the latest submitted
		case a.ScoreHalves != nil && (best.ScoreHalves == nil || *a.ScoreHalves > *best.ScoreHalves):
			best = a
		}
	}
	return best, ok
}

// status is submitted if any attempt is submitted, otherwise the latest attempt's status.
func (r userRun) status() string {
	if _, ok := r.best(); ok {
		return store.AttemptSubmitted
	}
	return r.latest().Status
}
