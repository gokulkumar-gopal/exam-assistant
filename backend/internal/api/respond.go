package api

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"

	"github.com/gokulkumar/exam-assistant/backend/internal/apperr"
)

// WriteJSON writes v as JSON with the given status. Every response is no-store.
func WriteJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Default().Error("encode response", "err", err)
	}
}

type errorEnvelope struct {
	Error errorBody `json:"error"`
}

type errorBody struct {
	Code    string            `json:"code"`
	Message string            `json:"message"`
	Fields  map[string]string `json:"fields,omitempty"`
}

// WriteError renders err as {"error":{code,message,fields?}}. Anything that is
// not an *apperr.Error becomes 500 internal; the cause is logged with the
// request ID and never rendered.
func WriteError(w http.ResponseWriter, r *http.Request, err error) {
	var ae *apperr.Error
	if !errors.As(err, &ae) {
		ae = apperr.Internal(err)
	}
	if ae.Status >= 500 && ae.Err != nil {
		loggerFrom(r.Context()).Error("request failed", "request_id", requestID(r.Context()), "err", ae.Err.Error())
	}
	WriteJSON(w, ae.Status, errorEnvelope{Error: errorBody{Code: ae.Code, Message: ae.Message, Fields: ae.Fields}})
}

// Decode reads one JSON value from the body into v, capped at maxBytes.
// Unknown fields, trailing data, malformed JSON and oversize bodies are 422 validation errors.
func Decode(w http.ResponseWriter, r *http.Request, v any, maxBytes int64) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return decodeError(err)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		if err == nil {
			return apperr.Validation("Request body must contain a single JSON value.", nil)
		}
		return decodeError(err)
	}
	return nil
}

func decodeError(err error) error {
	var tooBig *http.MaxBytesError
	if errors.As(err, &tooBig) {
		return apperr.Validation("Request body is too large.", nil)
	}
	return apperr.Validation("Request body is not valid JSON for this endpoint.", nil)
}
