package docs

import (
	"context"
	"errors"

	"github.com/gokulkumar/exam-assistant/backend/internal/apperr"
	"github.com/gokulkumar/exam-assistant/backend/internal/store"
)

const (
	msgUploadUnfinished = "The upload didn't finish. Try again."
	msgTypeMismatch     = "That file doesn't match its type."
	msgSizeMismatch     = "The uploaded file isn't the size you declared. Upload it again."
	msgUnreadablePDF    = "We couldn't read that PDF. It may be damaged or password protected."
)

// Complete verifies an uploaded object and marks the document ready (backend-spec 8.3).
// It is idempotent: an already-ready document is returned as is, without touching the object store.
func (s *Service) Complete(ctx context.Context, user store.User, docID string) (store.Document, error) {
	d, err := s.docs.GetDoc(ctx, user.ID, docID)
	if errors.Is(err, store.ErrNotFound) {
		return store.Document{}, apperr.NotFound()
	}
	if err != nil {
		return store.Document{}, apperr.Internal(err)
	}
	if d.Status == store.DocReady {
		return d, nil
	}

	size, err := s.blob.Head(ctx, d.S3Key)
	if errors.Is(err, store.ErrNotFound) {
		return store.Document{}, apperr.Conflict(msgUploadUnfinished)
	}
	if err != nil {
		return store.Document{}, apperr.Upstream()
	}
	if size != d.SizeBytes {
		return store.Document{}, s.reject(ctx, d, msgSizeMismatch)
	}

	// The declared size is at most 20 MB and equals the real size, so this reads
	// at most 20 MB; the whole object is needed for PDF pages and TXT characters.
	data, err := s.blob.Get(ctx, d.S3Key, MaxUploadBytes)
	if errors.Is(err, store.ErrNotFound) {
		return store.Document{}, apperr.Conflict(msgUploadUnfinished)
	}
	if err != nil {
		return store.Document{}, apperr.Upstream()
	}
	if !Sniff(data, d.ContentType) {
		return store.Document{}, s.reject(ctx, d, msgTypeMismatch)
	}
	pages, err := CountPages(d.ContentType, data)
	if errors.Is(err, ErrUnreadable) {
		return store.Document{}, s.reject(ctx, d, msgUnreadablePDF)
	}
	if err != nil {
		return store.Document{}, apperr.Internal(err)
	}

	switch err := s.docs.MarkReady(ctx, user.ID, d.ID, pages); {
	case err == nil, errors.Is(err, store.ErrPrecondition):
		// ErrPrecondition: a concurrent complete won; return its result below.
	case errors.Is(err, store.ErrNotFound):
		return store.Document{}, apperr.NotFound()
	default:
		return store.Document{}, apperr.Internal(err)
	}
	out, err := s.docs.GetDoc(ctx, user.ID, d.ID)
	if errors.Is(err, store.ErrNotFound) {
		return store.Document{}, apperr.NotFound()
	}
	if err != nil {
		return store.Document{}, apperr.Internal(err)
	}
	if out.Status != store.DocReady {
		return store.Document{}, apperr.Internal(errors.New("docs: document not ready after MarkReady"))
	}
	return out, nil
}

// reject deletes the object and the row of a bad upload and returns the 422 to show.
// If the object cannot be deleted the row is kept so a later attempt can clean up.
func (s *Service) reject(ctx context.Context, d store.Document, msg string) error {
	if err := s.blob.Delete(ctx, d.S3Key); err != nil {
		return apperr.Upstream()
	}
	if err := s.docs.DeleteDoc(ctx, d.OwnerID, d.ID); err != nil && !errors.Is(err, store.ErrNotFound) {
		return apperr.Internal(err)
	}
	return apperr.Validation(msg, nil)
}
