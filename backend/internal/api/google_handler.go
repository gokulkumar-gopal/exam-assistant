package api

import (
	"net/http"

	"github.com/gokulkumar/exam-assistant/backend/internal/apperr"
	"github.com/gokulkumar/exam-assistant/backend/internal/auth"
)

// registerGoogleRoutes adds POST /auth/google. The caller registers it only when a
// verifier is configured; without one the path is the standard JSON 404.
func registerGoogleRoutes(rt *routeTable, deps Deps) {
	rt.handle("POST", "/auth/google", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// email and name are accepted so a client that sends them is not rejected, but they
		// are never read: identity comes only from the verified token.
		var in struct {
			Credential string `json:"credential"`
			Role       string `json:"role"`
			Email      string `json:"email"`
			Name       string `json:"name"`
		}
		if err := Decode(w, r, &in, authBodyCap); err != nil {
			WriteError(w, r, err)
			return
		}
		if in.Credential == "" {
			WriteError(w, r, apperr.Unauthenticated(auth.GoogleFailedMessage))
			return
		}
		u, created, err := deps.Auth.Google(r.Context(), in.Credential, in.Role)
		if err != nil {
			WriteError(w, r, err)
			return
		}
		http.SetCookie(w, deps.Sessions.Issue(u, deps.Clock.Now()))
		status := http.StatusOK
		if created {
			status = http.StatusCreated
		}
		WriteJSON(w, status, map[string]any{"user": toUserJSON(u)})
	}))
}
