package api

import (
	"errors"

	"github.com/gokulkumar/exam-assistant/backend/internal/apperr"
	"github.com/gokulkumar/exam-assistant/backend/internal/gen"
	"github.com/gokulkumar/exam-assistant/backend/internal/store"
)

// MapQuotaError turns a store.QuotaError (from JobStore.CreateJobWithQuota) into the
// 429 rate_limited error with the end-user message for its kind. Any other error,
// including nil, is returned unchanged. The generate endpoint (B-2.3) uses it.
func MapQuotaError(err error) error {
	var qe *store.QuotaError
	if errors.As(err, &qe) {
		return apperr.RateLimited(gen.QuotaMessage(qe.Kind))
	}
	return err
}
