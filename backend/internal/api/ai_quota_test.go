package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gokulkumar/exam-assistant/backend/internal/store"
)

func TestHandlerMessages(t *testing.T) {
	cases := map[string]string{
		store.QuotaBudget: "The monthly AI budget has been used. Try again next month.",
		store.QuotaUser:   "You've reached today's limit for generating questions. Try again tomorrow.",
		store.QuotaRegen:  "You've reached today's limit for regenerating questions. Try again tomorrow.",
		store.QuotaGlobal: "Question generation has reached its limit for today. Try again tomorrow.",
	}
	for kind, msg := range cases {
		t.Run(kind, func(t *testing.T) {
			w := httptest.NewRecorder()
			r := httptest.NewRequest("POST", "/generate", nil)
			WriteError(w, r, MapQuotaError(&store.QuotaError{Kind: kind}))
			if w.Code != http.StatusTooManyRequests {
				t.Fatalf("status = %d", w.Code)
			}
			var body struct {
				Error struct{ Code, Message string }
			}
			if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if body.Error.Code != "rate_limited" || body.Error.Message != msg {
				t.Fatalf("body = %s", w.Body.String())
			}
		})
	}
	// wrapped quota errors map too; anything else passes through unchanged
	wrapped := errors.Join(errors.New("ctx"), &store.QuotaError{Kind: store.QuotaUser})
	if MapQuotaError(wrapped).Error() == wrapped.Error() {
		t.Fatal("wrapped QuotaError not mapped")
	}
	other := errors.New("boom")
	if MapQuotaError(other) != other || MapQuotaError(nil) != nil {
		t.Fatal("non-quota errors must pass through")
	}
}
