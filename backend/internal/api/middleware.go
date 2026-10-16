package api

import (
	"context"
	"crypto/rand"
	"log/slog"
	"mime"
	"net/http"
	"time"

	"github.com/oklog/ulid/v2"

	"github.com/gokulkumar/exam-assistant/backend/internal/apperr"
)

type ctxKey int

const (
	ctxRequestID ctxKey = iota
	ctxLogger
)

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
// recovery, Cache-Control: no-store, and the 415 check for non-JSON bodies.
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
				"method", r.Method,
				"path", r.URL.Path,
				"status", rec.status,
				"duration_ms", float64(time.Since(start).Microseconds())/1000,
			)
		}()

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
