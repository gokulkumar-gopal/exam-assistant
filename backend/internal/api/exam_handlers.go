package api

import (
	"net/http"
	"time"

	"github.com/gokulkumar/exam-assistant/backend/internal/exams"
)

// examBodyCap fits 60 questions at the service's own text caps (4 bytes per character) plus
// 50 assignee emails.
const examBodyCap = 1 << 21

// registerExamRoutes serves the exam endpoints (backend-spec 8.5).
func registerExamRoutes(rt *routeTable, deps Deps) {
	rt.handle("POST", "/exams", RequireUser(deps, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var in exams.CreateInput
		if err := Decode(w, r, &in, examBodyCap); err != nil {
			WriteError(w, r, err)
			return
		}
		u, _ := UserFrom(r.Context())
		res, err := deps.Exams.Create(r.Context(), u, in, r.Header.Get("Idempotency-Key"))
		if err != nil {
			WriteError(w, r, err)
			return
		}
		status := http.StatusCreated
		if res.Replay {
			status = http.StatusOK
		}
		WriteJSON(w, status, map[string]any{
			"id": res.ID, "unknownEmails": res.UnknownEmails,
			"serverTime": deps.Clock.Now().UTC().Format(time.RFC3339),
		})
	})))
}
