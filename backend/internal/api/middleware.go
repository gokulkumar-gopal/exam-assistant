package api

import (
	"context"
	"crypto/rand"
	"errors"
	"log/slog"
	"mime"
	"net/http"
	"slices"
	"time"

	"github.com/oklog/ulid/v2"

	"github.com/gokulkumar/exam-assistant/backend/internal/apperr"
	"github.com/gokulkumar/exam-assistant/backend/internal/auth"
	"github.com/gokulkumar/exam-assistant/backend/internal/store"
)

type ctxKey int

const (
	ctxRequestID ctxKey = iota
	ctxLogger
	ctxReqInfo
	ctxUser
)

// reqInfo is filled in by inner middleware so the request log line can report the user.
type reqInfo struct{ userID string }

func requestID(ctx context.Context) string {
	id, _ := ctx.Value(ctxRequestID).(string)
	return id
}

func loggerFrom(ctx context.Context) *slog.Logger {
	if l, ok := ctx.Value(ctxLogger).(*slog.Logger); ok {
		return l
	}
	return slog.Default()
}

type statusRecorder struct {
	http.ResponseWriter
	status int
	wrote  bool
}

func (s *statusRecorder) WriteHeader(code int) {
	if !s.wrote {
		s.status, s.wrote = code, true
	}
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Write(b []byte) (int, error) {
	if !s.wrote {
		s.status, s.wrote = http.StatusOK, true
	}
	return s.ResponseWriter.Write(b)
}

func (s *statusRecorder) Unwrap() http.ResponseWriter { return s.ResponseWriter }

// wrap applies, outermost first: request ID, one log line per request, panic
// recovery, Cache-Control: no-store, the CSRF origin check, and the 415 check for non-JSON bodies.
func wrap(deps Deps, next http.Handler) http.Handler {
	logger := deps.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		id := ulid.MustNew(ulid.Timestamp(start), rand.Reader).String()
		ctx := context.WithValue(r.Context(), ctxRequestID, id)
		ctx = context.WithValue(ctx, ctxLogger, logger)
		info := &reqInfo{}
		ctx = context.WithValue(ctx, ctxReqInfo, info)
		r = r.WithContext(ctx)

		w.Header().Set("X-Request-Id", id)
		w.Header().Set("Cache-Control", "no-store")
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}

		defer func() {
			if p := recover(); p != nil {
				if p == http.ErrAbortHandler {
					panic(p)
				}
				// The panic value is not logged: it may carry request data.
				logger.Error("panic recovered", "request_id", id)
				WriteError(rec, r, apperr.Internal(nil))
			}
			logger.Info("request",
				"request_id", id,
				"user_id", info.userID,
				"method", r.Method,
				"path", r.URL.Path,
				"status", rec.status,
				"duration_ms", float64(time.Since(start).Microseconds())/1000,
			)
		}()

		if !originAllowed(r, deps.AllowedOrigins) {
			WriteError(rec, r, apperr.Forbidden("Request origin is not allowed."))
			return
		}
		if hasBody(r) && bodyMethod(r.Method) && !isJSON(r.Header.Get("Content-Type")) {
			WriteError(rec, r, apperr.UnsupportedMediaType())
			return
		}
		next.ServeHTTP(rec, r)
	})
}

func bodyMethod(m string) bool {
	return m == http.MethodPost || m == http.MethodPut || m == http.MethodPatch
}

// hasBody is true when the request declares or streams a body (ContentLength -1 is chunked).
func hasBody(r *http.Request) bool { return r.ContentLength != 0 }

func isJSON(contentType string) bool {
	mt, _, err := mime.ParseMediaType(contentType)
	return err == nil && mt == "application/json"
}

// originAllowed is the CSRF check (backend-spec 8.2): for state-changing methods a
// present Origin must be in the allow-list; an absent Origin is allowed.
func originAllowed(r *http.Request, allowed []string) bool {
	switch r.Method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
	default:
		return true
	}
	origin := r.Header.Get("Origin")
	return origin == "" || slices.Contains(allowed, origin)
}

// UserFrom returns the authenticated user RequireUser put in the context.
func UserFrom(ctx context.Context) (store.User, bool) {
	u, ok := ctx.Value(ctxUser).(store.User)
	return u, ok
}

// RequireUser authenticates the session cookie, loads the user from the store on every
// request, and rejects sessions issued before the user's SessionsValidAfter. Every failure
// is the same 401.
func RequireUser(deps Deps, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		unauth := apperr.Unauthenticated("Sign in to continue.")
		cookie, err := r.Cookie(auth.CookieName)
		if err != nil || cookie.Value == "" {
			WriteError(w, r, unauth)
			return
		}
		claims, err := deps.Sessions.Parse(cookie.Value, deps.Clock.Now())
		if err != nil {
			WriteError(w, r, unauth)
			return
		}
		u, err := deps.Users.GetByID(r.Context(), claims.Sub)
		if errors.Is(err, store.ErrNotFound) {
			WriteError(w, r, unauth)
			return
		}
		if err != nil {
			WriteError(w, r, apperr.Internal(err))
			return
		}
		if claims.Iat < u.SessionsValidAfter {
			WriteError(w, r, unauth)
			return
		}
		ctx := context.WithValue(r.Context(), ctxUser, u)
		if info, ok := ctx.Value(ctxReqInfo).(*reqInfo); ok {
			info.userID = u.ID
		}
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
