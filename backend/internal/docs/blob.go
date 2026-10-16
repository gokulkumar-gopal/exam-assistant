package docs

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/gokulkumar/exam-assistant/backend/internal/store"
)

// Blob is the object store behind documents (backend-spec 12).
type Blob interface {
	// PresignPut returns a PUT URL that only accepts exactly size bytes of contentType.
	PresignPut(ctx context.Context, key, contentType string, size int64, ttl time.Duration) (url string, err error)
	PresignGet(ctx context.Context, key, contentType string, ttl time.Duration) (url string, err error)
	// Head returns the object size, or store.ErrNotFound if the key is missing.
	Head(ctx context.Context, key string) (size int64, err error)
	// Get reads the object; it fails if the object is larger than max bytes.
	Get(ctx context.Context, key string, max int64) ([]byte, error)
	Delete(ctx context.Context, key string) error
}

// PutCall records the parameters of one PresignPut call.
type PutCall struct {
	Key, ContentType string
	Size             int64
	TTL              time.Duration
}

// MemBlob is an in-memory Blob for tests. Puts records every PresignPut call.
type MemBlob struct {
	mu      sync.Mutex
	objects map[string][]byte
	Puts    []PutCall
	Gets    []GetCall
}

// GetCall records the parameters of one PresignGet call.
type GetCall struct {
	Key, ContentType string
	TTL              time.Duration
}

var _ Blob = (*MemBlob)(nil)

// NewMemBlob returns an empty MemBlob.
func NewMemBlob() *MemBlob { return &MemBlob{objects: map[string][]byte{}} }

// Put stores an object directly, simulating a finished browser upload.
func (m *MemBlob) Put(key string, data []byte) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.objects[key] = append([]byte(nil), data...)
}

func (m *MemBlob) PresignPut(_ context.Context, key, contentType string, size int64, ttl time.Duration) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Puts = append(m.Puts, PutCall{Key: key, ContentType: contentType, Size: size, TTL: ttl})
	return "https://mem.test/put/" + key, nil
}

func (m *MemBlob) PresignGet(_ context.Context, key, contentType string, ttl time.Duration) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Gets = append(m.Gets, GetCall{Key: key, ContentType: contentType, TTL: ttl})
	return "https://mem.test/get/" + key, nil
}

func (m *MemBlob) Head(_ context.Context, key string) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	o, ok := m.objects[key]
	if !ok {
		return 0, store.ErrNotFound
	}
	return int64(len(o)), nil
}

func (m *MemBlob) Get(_ context.Context, key string, max int64) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	o, ok := m.objects[key]
	if !ok {
		return nil, store.ErrNotFound
	}
	if int64(len(o)) > max {
		return nil, fmt.Errorf("docs: object %q is larger than %d bytes", key, max)
	}
	return append([]byte(nil), o...), nil
}

func (m *MemBlob) Delete(_ context.Context, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.objects, key)
	return nil
}
