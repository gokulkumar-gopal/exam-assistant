// Package api holds routes, middleware and response helpers.
package api

import (
	"log/slog"
	"net/http"
	"slices"
	"strings"

	"github.com/gokulkumar/exam-assistant/backend/internal/apperr"
	"github.com/gokulkumar/exam-assistant/backend/internal/auth"
	"github.com/gokulkumar/exam-assistant/backend/internal/clock"
	"github.com/gokulkumar/exam-assistant/backend/internal/docs"
	"github.com/gokulkumar/exam-assistant/backend/internal/store"
)

// Deps is everything handlers need. Later tasks add services here.
type Deps struct {
	Logger *slog.Logger
	Clock  clock.Clock

	Users          store.UserStore
	Auth           *auth.Service
	Sessions       *auth.Sessions
	Docs           *docs.Service
	AllowedOrigins []string // CSRF allow-list for Origin
}

// routeTable registers routes on a mux and remembers which methods each path serves,
// so a wrong method gets a JSON 405 with an Allow header instead of Go's plain text.
type routeTable struct {
	mux     *http.ServeMux
	methods map[string][]string
}

func (rt *routeTable) handle(method, path string, h http.Handler) {
	rt.mux.Handle(method+" "+path, h)
	rt.methods[path] = append(rt.methods[path], method)
}

// matches reports whether path fits pattern, where "{name}" segments match any one non-empty segment.
func matches(pattern, path string) bool {
	ps, xs := strings.Split(pattern, "/"), strings.Split(path, "/")
	if len(ps) != len(xs) {
		return false
	}
	for i, p := range ps {
		if strings.HasPrefix(p, "{") && strings.HasSuffix(p, "}") {
			if xs[i] == "" {
				return false
			}
			continue
		}
		if p != xs[i] {
			return false
		}
	}
	return true
}

// methodsFor returns every method registered on any pattern that matches path.
func (rt *routeTable) methodsFor(path string) []string {
	var ms []string
	for pattern, m := range rt.methods {
		if matches(pattern, path) {
			ms = append(ms, m...)
		}
	}
	return ms
}

func (rt *routeTable) fallback(w http.ResponseWriter, r *http.Request) {
	if ms := rt.methodsFor(r.URL.Path); len(ms) > 0 {
		allow := slices.Compact(slices.Sorted(slices.Values(ms)))
		if slices.Contains(allow, http.MethodGet) {
			allow = append(allow, http.MethodHead)
		}
		slices.Sort(allow)
		w.Header().Set("Allow", strings.Join(allow, ", "))
		WriteError(w, r, apperr.MethodNotAllowed())
		return
	}
	WriteError(w, r, apperr.NotFound())
}

// NewRouter builds the handler tree. Routes have no /api prefix.
func NewRouter(deps Deps) http.Handler {
	rt := &routeTable{mux: http.NewServeMux(), methods: map[string][]string{}}
	rt.handle("GET", "/health", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	}))
	if deps.Auth != nil {
		registerAuthRoutes(rt, deps)
		if deps.Auth.GoogleEnabled() {
			registerGoogleRoutes(rt, deps)
		}
		if deps.Docs != nil {
			registerDocsRoutes(rt, deps)
		}
	}
	rt.mux.HandleFunc("/", rt.fallback)
	return wrap(deps, rt.mux)
}
