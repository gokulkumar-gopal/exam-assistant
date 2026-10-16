package api

import (
	"net/http"
	"time"

	"github.com/gokulkumar/exam-assistant/backend/internal/auth"
	"github.com/gokulkumar/exam-assistant/backend/internal/store"
)

const authBodyCap = 1 << 20

type userJSON struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Email string `json:"email"`
	Role  string `json:"role"`
}

func toUserJSON(u store.User) userJSON {
	return userJSON{ID: u.ID, Name: u.Name, Email: u.Email, Role: u.Role}
}

func registerAuthRoutes(rt *routeTable, deps Deps) {
	setSession := func(w http.ResponseWriter, u store.User) {
		http.SetCookie(w, deps.Sessions.Issue(u, deps.Clock.Now()))
	}

	rt.handle("POST", "/auth/signup", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Name     string `json:"name"`
			Email    string `json:"email"`
			Password string `json:"password"`
			Role     string `json:"role"`
		}
		if err := Decode(w, r, &in, authBodyCap); err != nil {
			WriteError(w, r, err)
			return
		}
		u, err := deps.Auth.Signup(r.Context(), auth.SignupInput{Name: in.Name, Email: in.Email, Password: in.Password, Role: in.Role})
		if err != nil {
			WriteError(w, r, err)
			return
		}
		setSession(w, u)
		WriteJSON(w, http.StatusCreated, map[string]any{"user": toUserJSON(u)})
	}))

	rt.handle("POST", "/auth/login", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Email    string `json:"email"`
			Password string `json:"password"`
		}
		if err := Decode(w, r, &in, authBodyCap); err != nil {
			WriteError(w, r, err)
			return
		}
		u, err := deps.Auth.Login(r.Context(), in.Email, in.Password)
		if err != nil {
			WriteError(w, r, err)
			return
		}
		setSession(w, u)
		WriteJSON(w, http.StatusOK, map[string]any{"user": toUserJSON(u)})
	}))

	rt.handle("POST", "/auth/logout", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.SetCookie(w, deps.Sessions.Clear())
		w.WriteHeader(http.StatusNoContent)
	}))

	rt.handle("GET", "/me", RequireUser(deps, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, _ := UserFrom(r.Context())
		WriteJSON(w, http.StatusOK, map[string]any{
			"user":       toUserJSON(u),
			"serverTime": deps.Clock.Now().UTC().Format(time.RFC3339),
		})
	})))
}
