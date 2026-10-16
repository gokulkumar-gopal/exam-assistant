package api

import (
	"net/http"
	"time"

	"github.com/gokulkumar/exam-assistant/backend/internal/exams"
)

// examBodyCap fits 60 questions at the service's own per-field caps (4 bytes per character)
// plus 50 assignee emails. The 350 KB item-size check is what bounds stored exams; this only
// stops a body from being read without limit.
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
		} else {
			w.Header().Set("Location", "/exams/"+res.ID)
		}
		WriteJSON(w, status, map[string]any{
			"id": res.ID, "unknownEmails": res.UnknownEmails,
			"serverTime": deps.Clock.Now().UTC().Format(time.RFC3339),
		})
	})))
	rt.handle("GET", "/exams", RequireUser(deps, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, _ := UserFrom(r.Context())
		items, err := deps.Exams.List(r.Context(), u, r.URL.Query().Get("scope"))
		if err != nil {
			WriteError(w, r, err)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		WriteJSON(w, http.StatusOK, map[string]any{
			"serverTime": deps.Clock.Now().UTC().Format(time.RFC3339),
			"exams":      items,
		})
	})))
}
