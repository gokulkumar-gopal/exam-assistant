package api

import (
	"net/http"

	"github.com/gokulkumar/exam-assistant/backend/internal/gen"
)

const genBodyCap = 1 << 16

// regenBodyCap leaves room for 60 avoid strings of 600 characters, which can be 4 bytes each.
const regenBodyCap = 1 << 19

// registerGenRoutes serves the generation endpoints (backend-spec 8.4).
func registerGenRoutes(rt *routeTable, deps Deps) {
	rt.handle("POST", "/generate", RequireUser(deps, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var in gen.GenerateRequest
		if err := Decode(w, r, &in, genBodyCap); err != nil {
			WriteError(w, r, err)
			return
		}
		u, _ := UserFrom(r.Context())
		id, err := deps.Gen.Create(r.Context(), u, in)
		if err != nil {
			WriteError(w, r, err)
			return
		}
		w.Header().Set("Location", "/generate/"+id)
		WriteJSON(w, http.StatusAccepted, map[string]string{"jobId": id})
	})))

	rt.handle("POST", "/generate/regenerate", RequireUser(deps, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var in gen.RegenerateRequest
		if err := Decode(w, r, &in, regenBodyCap); err != nil {
			WriteError(w, r, err)
			return
		}
		u, _ := UserFrom(r.Context())
		id, err := deps.Gen.Regenerate(r.Context(), u, in)
		if err != nil {
			WriteError(w, r, err)
			return
		}
		w.Header().Set("Location", "/generate/"+id)
		WriteJSON(w, http.StatusAccepted, map[string]string{"jobId": id})
	})))

	rt.handle("GET", "/generate/{id}", RequireUser(deps, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, _ := UserFrom(r.Context())
		v, err := deps.Gen.Poll(r.Context(), u, r.PathValue("id"))
		if err != nil {
			WriteError(w, r, err)
			return
		}
		WriteJSON(w, http.StatusOK, v)
	})))

	rt.handle("DELETE", "/generate/{id}", RequireUser(deps, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, _ := UserFrom(r.Context())
		if err := deps.Gen.Cancel(r.Context(), u, r.PathValue("id")); err != nil {
			WriteError(w, r, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})))
}
