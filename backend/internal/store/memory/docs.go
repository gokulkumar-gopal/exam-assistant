package memory

import (
	"context"
	"sort"
	"sync"

	"github.com/gokulkumar/exam-assistant/backend/internal/clock"
	"github.com/gokulkumar/exam-assistant/backend/internal/store"
)

type docKey struct{ owner, id string }

// DocStore is an in-memory store.DocStore. Pending documents whose TTL has passed
// (per the injected clock) behave as deleted, as DynamoDB TTL eventually makes them.
type DocStore struct {
	mu   sync.Mutex
	clk  clock.Clock
	docs map[docKey]store.Document
}

var _ store.DocStore = (*DocStore)(nil)

// NewDocStore returns an empty DocStore reading time from clk.
func NewDocStore(clk clock.Clock) *DocStore {
	return &DocStore{clk: clk, docs: map[docKey]store.Document{}}
}

// live returns the document if present and not expired. Caller holds the lock.
func (s *DocStore) live(owner, id string) (store.Document, bool) {
	d, ok := s.docs[docKey{owner, id}]
	if !ok {
		return store.Document{}, false
	}
	if d.TTL != 0 && s.clk.Now().Unix() >= d.TTL {
		delete(s.docs, docKey{owner, id})
		return store.Document{}, false
	}
	return d, true
}

func (s *DocStore) PutDoc(_ context.Context, d store.Document) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.docs[docKey{d.OwnerID, d.ID}] = d
	return nil
}

func (s *DocStore) GetDoc(_ context.Context, ownerID, id string) (store.Document, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, ok := s.live(ownerID, id)
	if !ok {
		return store.Document{}, store.ErrNotFound
	}
	return d, nil
}

func (s *DocStore) ListReady(_ context.Context, ownerID string, limit int) ([]store.Document, error) {
	if limit <= 0 || limit > store.MaxListReady {
		limit = store.MaxListReady
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []store.Document
	for k := range s.docs {
		if k.owner != ownerID {
			continue
		}
		if d, ok := s.live(k.owner, k.id); ok && d.Status == store.DocReady {
			out = append(out, d)
		}
	}
	// Newest first; for time-ordered ULIDs this equals id descending, and equal
	// CreatedAt ties break by id descending, as the store contract states.
	sort.Slice(out, func(i, j int) bool {
		if !out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].CreatedAt.After(out[j].CreatedAt)
		}
		return out[i].ID > out[j].ID
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (s *DocStore) GetDocs(_ context.Context, ownerID string, ids []string) ([]store.Document, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	seen := map[string]bool{}
	out := []store.Document{}
	for _, id := range ids {
		if seen[id] {
			continue
		}
		seen[id] = true
		if d, ok := s.live(ownerID, id); ok {
			out = append(out, d)
		}
	}
	return out, nil
}

func (s *DocStore) MarkReady(_ context.Context, ownerID, id string, pages int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, ok := s.live(ownerID, id)
	if !ok {
		return store.ErrNotFound
	}
	if d.Status != store.DocPending {
		return store.ErrPrecondition
	}
	d.Status = store.DocReady
	d.Pages = pages
	d.TTL = 0
	s.docs[docKey{ownerID, id}] = d
	return nil
}

func (s *DocStore) DeleteDoc(_ context.Context, ownerID, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.live(ownerID, id); !ok {
		return store.ErrNotFound
	}
	delete(s.docs, docKey{ownerID, id})
	return nil
}
