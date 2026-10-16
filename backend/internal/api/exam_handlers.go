package api

import (
	"bytes"
	"io"
	"net/http"
	"time"

	"github.com/gokulkumar/exam-assistant/backend/internal/exams"
)

// examBodyCap fits 60 questions at the service's own per-field caps (4 bytes per character)
// plus 50 assignee emails. The 350 KB item-size check is what bounds stored exams; this only
// stops a body from being read without limit.
const examBodyCap = 1 << 21

// autosaveBodyCap is the 400 KiB body limit of PUT /exams/{id}/attempt (backend-spec 8.5; the
// service also bounds the summed answer bytes). Decode turns an oversize body into the 422
// validation error of the 8.0 body rule.
const autosaveBodyCap = 400 << 10

// revealBodyCap bounds the one-field body of POST /exams/{id}/reveal.
const revealBodyCap = 4 << 10

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
	rt.handle("GET", "/exams/{id}", RequireUser(deps, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, _ := UserFrom(r.Context())
		v, err := deps.Exams.Get(r.Context(), u, r.PathValue("id"))
		if err != nil {
			WriteError(w, r, err)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		WriteJSON(w, http.StatusOK, map[string]any{
			"serverTime": deps.Clock.Now().UTC().Format(time.RFC3339),
			"exam":       v.Exam,
			"attempt":    v.Attempt,
			"keyVisible": v.KeyVisible,
		})
	})))
	rt.handle("POST", "/exams/{id}/decline", RequireUser(deps, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, _ := UserFrom(r.Context())
		if err := deps.Exams.Decline(r.Context(), u, r.PathValue("id")); err != nil {
			WriteError(w, r, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})))
	rt.handle("POST", "/exams/{id}/assign", RequireUser(deps, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Emails []string `json:"emails"`
		}
		if err := Decode(w, r, &in, examBodyCap); err != nil {
			WriteError(w, r, err)
			return
		}
		u, _ := UserFrom(r.Context())
		res, err := deps.Exams.Assign(r.Context(), u, r.PathValue("id"), in.Emails)
		if err != nil {
			WriteError(w, r, err)
			return
		}
		WriteJSON(w, http.StatusOK, res)
	})))
	rt.handle("GET", "/exams/{id}/status", RequireUser(deps, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, _ := UserFrom(r.Context())
		v, err := deps.Exams.Status(r.Context(), u, r.PathValue("id"))
		if err != nil {
			WriteError(w, r, err)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		WriteJSON(w, http.StatusOK, map[string]any{
			"serverTime": deps.Clock.Now().UTC().Format(time.RFC3339),
			"exam":       v.Exam,
			"counts":     v.Counts,
			"attempts":   v.Attempts,
		})
	})))
	rt.handle("PUT", "/exams/{id}/attempt", RequireUser(deps, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var in exams.AutosaveInput
		if err := Decode(w, r, &in, autosaveBodyCap); err != nil {
			WriteError(w, r, err)
			return
		}
		u, _ := UserFrom(r.Context())
		at, err := deps.Exams.Autosave(r.Context(), u, r.PathValue("id"), in)
		if err != nil {
			WriteError(w, r, err)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		WriteJSON(w, http.StatusOK, map[string]any{
			"savedAt":    at.UTC().Format(time.RFC3339),
			"serverTime": deps.Clock.Now().UTC().Format(time.RFC3339),
		})
	})))
	rt.handle("GET", "/exams/{id}/result", RequireUser(deps, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, _ := UserFrom(r.Context())
		res, err := deps.Exams.Result(r.Context(), u, r.PathValue("id"))
		if err != nil {
			WriteError(w, r, err)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		WriteJSON(w, http.StatusOK, struct {
			ServerTime string `json:"serverTime"`
			exams.ResultView
		}{deps.Clock.Now().UTC().Format(time.RFC3339), res})
	})))
	rt.handle("POST", "/exams/{id}/reveal", RequireUser(deps, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			QuestionID string `json:"questionId"`
		}
		if err := Decode(w, r, &in, revealBodyCap); err != nil {
			WriteError(w, r, err)
			return
		}
		u, _ := UserFrom(r.Context())
		res, err := deps.Exams.Reveal(r.Context(), u, r.PathValue("id"), in.QuestionID)
		if err != nil {
			WriteError(w, r, err)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		WriteJSON(w, http.StatusOK, struct {
			exams.RevealView
			ServerTime string `json:"serverTime"`
		}{res, deps.Clock.Now().UTC().Format(time.RFC3339)})
	})))
	rt.handle("POST", "/exams/{id}/submit", RequireUser(deps, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var in exams.SubmitInput
		if err := decodeOptional(w, r, &in, autosaveBodyCap); err != nil {
			WriteError(w, r, err)
			return
		}
		u, _ := UserFrom(r.Context())
		res, err := deps.Exams.Submit(r.Context(), u, r.PathValue("id"), &in)
		if err != nil {
			WriteError(w, r, err)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		WriteJSON(w, http.StatusOK, struct {
			exams.SubmitResult
			ServerTime string `json:"serverTime"`
		}{res, deps.Clock.Now().UTC().Format(time.RFC3339)})
	})))
}

// decodeOptional is Decode for a body that may be absent: an empty body leaves v untouched
// (submit with no answers uses the saved ones); anything else is decoded strictly.
func decodeOptional(w http.ResponseWriter, r *http.Request, v any, maxBytes int64) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
	data, err := io.ReadAll(r.Body)
	if err != nil {
		return decodeError(err)
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return nil
	}
	r.Body = io.NopCloser(bytes.NewReader(data))
	return Decode(w, r, v, maxBytes)
}
