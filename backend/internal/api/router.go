// Package api holds routes, middleware and response helpers.
package api

import (
	"log/slog"
	"net/http"

	"github.com/gokulkumar/exam-assistant/backend/internal/apperr"
	"github.com/gokulkumar/exam-assistant/backend/internal/clock"
)

// Deps is everything handlers need. Later tasks add services here.
type Deps struct {
	Logger *slog.Logger
	Clock  clock.Clock
}

// NewRouter builds the handler tree. Routes have no /api prefix.
func NewRouter(deps Deps) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		WriteError(w, r, apperr.NotFound())
	})
	return wrap(deps, mux)
}
