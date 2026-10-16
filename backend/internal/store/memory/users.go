// Package memory holds mutex-guarded in-memory fakes of the store interfaces. They
// mirror the conditional-write semantics of the DynamoDB implementation.
package memory

import (
	"context"
	"errors"
	"sync"

	"github.com/gokulkumar/exam-assistant/backend/internal/store"
)

// UserStore is an in-memory store.UserStore.
type UserStore struct {
	mu     sync.Mutex
	users  map[string]store.User // by ID
	emails map[string]string     // email lock -> userID
	google map[string]string     // google lock -> userID
}

var _ store.UserStore = (*UserStore)(nil)

// NewUserStore returns an empty UserStore.
func NewUserStore() *UserStore {
	return &UserStore{
		users:  map[string]store.User{},
		emails: map[string]string{},
		google: map[string]string{},
	}
}

func (s *UserStore) CreateUser(_ context.Context, u store.User) error {
	if u.ID == "" || u.Email == "" {
		return errors.New("memory: user needs ID and Email")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.users[u.ID]; ok {
		return store.ErrConflict
	}
	if _, ok := s.emails[u.Email]; ok {
		return store.ErrConflict
	}
	if u.GoogleSub != "" {
		if _, ok := s.google[u.GoogleSub]; ok {
			return store.ErrConflict
		}
		s.google[u.GoogleSub] = u.ID
	}
	s.emails[u.Email] = u.ID
	s.users[u.ID] = u
	return nil
}

func (s *UserStore) CreateGoogleUser(ctx context.Context, u store.User) error {
	if u.GoogleSub == "" {
		return errors.New("memory: google user needs GoogleSub")
	}
	return s.CreateUser(ctx, u)
}

func (s *UserStore) LinkGoogle(_ context.Context, userID, sub string, validAfter int64) error {
	if sub == "" {
		return errors.New("memory: empty google sub")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	u, ok := s.users[userID]
	if !ok {
		return store.ErrNotFound
	}
	if u.GoogleSub != "" {
		return store.ErrConflict
	}
	if _, taken := s.google[sub]; taken {
		return store.ErrConflict
	}
	s.google[sub] = userID
	u.GoogleSub = sub
	u.PasswordHash = ""
	u.SessionsValidAfter = validAfter
	s.users[userID] = u
	return nil
}

func (s *UserStore) GetByID(_ context.Context, id string) (store.User, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	u, ok := s.users[id]
	if !ok {
		return store.User{}, store.ErrNotFound
	}
	return u, nil
}

func (s *UserStore) GetByEmail(_ context.Context, email string) (store.User, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	id, ok := s.emails[email]
	if !ok {
		return store.User{}, store.ErrNotFound
	}
	return s.users[id], nil
}

func (s *UserStore) GetByGoogleSub(_ context.Context, sub string) (store.User, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	id, ok := s.google[sub]
	if !ok {
		return store.User{}, store.ErrNotFound
	}
	return s.users[id], nil
}

func (s *UserStore) GetManyByEmail(_ context.Context, emails []string) (map[string]store.User, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]store.User, len(emails))
	for _, e := range emails {
		if id, ok := s.emails[e]; ok {
			out[e] = s.users[id]
		}
	}
	return out, nil
}

func (s *UserStore) GetManyByID(_ context.Context, ids []string) (map[string]store.User, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]store.User, len(ids))
	for _, id := range ids {
		if u, ok := s.users[id]; ok {
			out[id] = u
		}
	}
	return out, nil
}
