// Package apperr defines typed errors that carry an HTTP status, a stable
// code and an end-user message. Services return these; the api package renders them.
package apperr

import "net/http"

// Error is an application error with the shape backend-spec 8.1 renders.
type Error struct {
	Status  int
	Code    string
	Message string
	Fields  map[string]string
	// Err is the underlying cause. It is logged, never rendered.
	Err error
}

func (e *Error) Error() string {
	if e.Err != nil {
		return e.Code + ": " + e.Message + ": " + e.Err.Error()
	}
	return e.Code + ": " + e.Message
}

// Unwrap returns the underlying cause, if any.
func (e *Error) Unwrap() error { return e.Err }

// NotFound is a missing resource, or one not visible to the caller.
func NotFound() *Error {
	return &Error{Status: http.StatusNotFound, Code: "not_found", Message: "Not found."}
}

// Forbidden is an authenticated caller who is not allowed.
func Forbidden(msg string) *Error {
	return &Error{Status: http.StatusForbidden, Code: "forbidden", Message: msg}
}

// Conflict is a state clash (duplicate, already submitted, ...).
func Conflict(msg string) *Error {
	return &Error{Status: http.StatusConflict, Code: "conflict", Message: msg}
}

// ExamClosed is a write after the deadline.
func ExamClosed() *Error {
	return &Error{Status: http.StatusConflict, Code: "exam_closed", Message: "This exam is closed."}
}

// Validation is bad input. fields may be nil.
func Validation(msg string, fields map[string]string) *Error {
	return &Error{Status: http.StatusUnprocessableEntity, Code: "validation", Message: msg, Fields: fields}
}

// RateLimited is a quota or rate refusal.
func RateLimited(msg string) *Error {
	return &Error{Status: http.StatusTooManyRequests, Code: "rate_limited", Message: msg}
}

// Unauthenticated is a missing or invalid session.
func Unauthenticated(msg string) *Error {
	return &Error{Status: http.StatusUnauthorized, Code: "unauthenticated", Message: msg}
}

// UnsupportedMediaType is a request body that is not application/json.
func UnsupportedMediaType() *Error {
	return &Error{Status: http.StatusUnsupportedMediaType, Code: "unsupported_media_type", Message: "Request body must be application/json."}
}

// Upstream is a failed LLM or AWS dependency.
func Upstream() *Error {
	return &Error{Status: http.StatusServiceUnavailable, Code: "upstream_unavailable", Message: "A service we depend on is unavailable. Please try again."}
}

// Internal wraps an unexpected error. The cause is kept for logging and never rendered.
func Internal(err error) *Error {
	return &Error{Status: http.StatusInternalServerError, Code: "internal", Message: "Something went wrong.", Err: err}
}

// MethodNotAllowed is a known route called with the wrong method. The caller sets the Allow header.
func MethodNotAllowed() *Error {
	return &Error{Status: http.StatusMethodNotAllowed, Code: "method_not_allowed", Message: "That method is not allowed for this endpoint."}
}

// RoleRequired is a first Google sign-in for a new account that sent no valid role.
func RoleRequired() *Error {
	return &Error{Status: http.StatusUnprocessableEntity, Code: "role_required", Message: "Choose a role to finish creating your account."}
}
