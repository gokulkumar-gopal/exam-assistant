// Package storetest is the shared contract suite for store implementations. The
// memory fake and the DynamoDB implementation must both pass it.
package storetest

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/gokulkumar/exam-assistant/backend/internal/store"
)

// UserFactory returns a fresh, empty UserStore.
type UserFactory func(t *testing.T) store.UserStore

// DocFactory returns a fresh, empty DocStore.
type DocFactory func(t *testing.T) store.DocStore

var ctx = context.Background()

func user(id, email string) store.User {
	return store.User{ID: id, Name: "N " + id, Email: email, PasswordHash: "hash", Role: "student",
		CreatedAt: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)}
}

func is(t *testing.T, got, want error) {
	t.Helper()
	if !errors.Is(got, want) {
		t.Fatalf("error = %v, want %v", got, want)
	}
}

func noErr(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// RunUserStoreSuite runs every UserStore contract test.
func RunUserStoreSuite(t *testing.T, f UserFactory) {
	t.Run("CreateUserEmailUnique", func(t *testing.T) { CreateUserEmailUnique(t, f) })
	t.Run("ConcurrentCreateSameEmail", func(t *testing.T) { ConcurrentCreateSameEmail(t, f) })
	t.Run("GoogleUserLocks", func(t *testing.T) { GoogleUserLocks(t, f) })
	t.Run("LinkGoogleAtomic", func(t *testing.T) { LinkGoogleAtomic(t, f) })
	t.Run("LinkGoogleErrors", func(t *testing.T) { LinkGoogleErrors(t, f) })
	t.Run("GetMany", func(t *testing.T) { GetMany(t, f) })
}

// RunDocStoreSuite runs every DocStore contract test.
func RunDocStoreSuite(t *testing.T, f DocFactory) {
	t.Run("ListReadyNewestFirstExcludesPending", func(t *testing.T) { ListReadyNewestFirstExcludesPending(t, f) })
	t.Run("ListReadyLimitAndCap", func(t *testing.T) { ListReadyLimitAndCap(t, f) })
	t.Run("DocsAreOwnerScoped", func(t *testing.T) { DocsAreOwnerScoped(t, f) })
	t.Run("MarkReady", func(t *testing.T) { MarkReady(t, f) })
	t.Run("GetDocsAndDelete", func(t *testing.T) { GetDocsAndDelete(t, f) })
}

func CreateUserEmailUnique(t *testing.T, f UserFactory) {
	s := f(t)
	noErr(t, s.CreateUser(ctx, user("u1", "a@x.com")))
	is(t, s.CreateUser(ctx, user("u2", "a@x.com")), store.ErrConflict)
	is(t, s.CreateUser(ctx, user("u1", "b@x.com")), store.ErrConflict)
	if _, err := s.GetByID(ctx, "u2"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("losing user must not exist, got %v", err)
	}
	got, err := s.GetByEmail(ctx, "a@x.com")
	noErr(t, err)
	if got.ID != "u1" || got.PasswordHash != "hash" || !got.CreatedAt.Equal(user("u1", "").CreatedAt) {
		t.Fatalf("unexpected user %+v", got)
	}
	_, err = s.GetByEmail(ctx, "nobody@x.com")
	is(t, err, store.ErrNotFound)
	_, err = s.GetByID(ctx, "nope")
	is(t, err, store.ErrNotFound)
}

func ConcurrentCreateSameEmail(t *testing.T, f UserFactory) {
	s := f(t)
	const n = 40
	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			errs[i] = s.CreateUser(ctx, user(fmt.Sprintf("u%d", i), "race@x.com"))
		}(i)
	}
	wg.Wait()
	wins := 0
	for _, err := range errs {
		switch {
		case err == nil:
			wins++
		case !errors.Is(err, store.ErrConflict):
			t.Fatalf("unexpected error %v", err)
		}
	}
	if wins != 1 {
		t.Fatalf("winners = %d, want exactly 1", wins)
	}
}

func GoogleUserLocks(t *testing.T, f UserFactory) {
	s := f(t)
	g := user("g1", "g@x.com")
	g.PasswordHash, g.GoogleSub = "", "sub-1"
	noErr(t, s.CreateGoogleUser(ctx, g))

	got, err := s.GetByGoogleSub(ctx, "sub-1")
	noErr(t, err)
	if got.ID != "g1" {
		t.Fatalf("got %+v", got)
	}
	// Same sub, different email: google lock clash.
	g2 := user("g2", "other@x.com")
	g2.GoogleSub = "sub-1"
	is(t, s.CreateGoogleUser(ctx, g2), store.ErrConflict)
	if _, err := s.GetByEmail(ctx, "other@x.com"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("a failed create must leave no email lock, got %v", err)
	}
	// Same email, new sub: email lock clash, and the new sub must stay free.
	g3 := user("g3", "g@x.com")
	g3.GoogleSub = "sub-3"
	is(t, s.CreateGoogleUser(ctx, g3), store.ErrConflict)
	_, err = s.GetByGoogleSub(ctx, "sub-3")
	is(t, err, store.ErrNotFound)
	// A Google user needs a sub.
	if err := s.CreateGoogleUser(ctx, user("g4", "g4@x.com")); err == nil {
		t.Fatal("CreateGoogleUser without GoogleSub must fail")
	}
}

func LinkGoogleAtomic(t *testing.T, f UserFactory) {
	s := f(t)
	noErr(t, s.CreateUser(ctx, user("u1", "a@x.com")))
	noErr(t, s.LinkGoogle(ctx, "u1", "sub-1", 1700000000))
	got, err := s.GetByID(ctx, "u1")
	noErr(t, err)
	if got.PasswordHash != "" || got.GoogleSub != "sub-1" || got.SessionsValidAfter != 1700000000 {
		t.Fatalf("after link: %+v", got)
	}
	bySub, err := s.GetByGoogleSub(ctx, "sub-1")
	noErr(t, err)
	if bySub.ID != "u1" {
		t.Fatalf("lock points at %q", bySub.ID)
	}
	is(t, s.LinkGoogle(ctx, "u1", "sub-2", 1700000001), store.ErrConflict)
	again, _ := s.GetByID(ctx, "u1")
	if again.GoogleSub != "sub-1" || again.SessionsValidAfter != 1700000000 {
		t.Fatalf("second link must change nothing: %+v", again)
	}
	_, err = s.GetByGoogleSub(ctx, "sub-2")
	is(t, err, store.ErrNotFound)
}

func LinkGoogleErrors(t *testing.T, f UserFactory) {
	s := f(t)
	is(t, s.LinkGoogle(ctx, "missing", "sub-1", 1), store.ErrNotFound)
	noErr(t, s.CreateUser(ctx, user("u1", "a@x.com")))
	noErr(t, s.CreateUser(ctx, user("u2", "b@x.com")))
	noErr(t, s.LinkGoogle(ctx, "u1", "sub-1", 1))
	// Same Google account onto a different user: lock clash, nothing changes.
	is(t, s.LinkGoogle(ctx, "u2", "sub-1", 2), store.ErrConflict)
	u2, _ := s.GetByID(ctx, "u2")
	if u2.GoogleSub != "" || u2.PasswordHash != "hash" || u2.SessionsValidAfter != 0 {
		t.Fatalf("failed link modified user: %+v", u2)
	}
}

func GetMany(t *testing.T, f UserFactory) {
	s := f(t)
	noErr(t, s.CreateUser(ctx, user("u1", "a@x.com")))
	noErr(t, s.CreateUser(ctx, user("u2", "b@x.com")))
	byEmail, err := s.GetManyByEmail(ctx, []string{"a@x.com", "b@x.com", "zz@x.com"})
	noErr(t, err)
	if len(byEmail) != 2 || byEmail["a@x.com"].ID != "u1" || byEmail["b@x.com"].ID != "u2" {
		t.Fatalf("byEmail = %+v", byEmail)
	}
	byID, err := s.GetManyByID(ctx, []string{"u2", "nope"})
	noErr(t, err)
	if len(byID) != 1 || byID["u2"].Email != "b@x.com" {
		t.Fatalf("byID = %+v", byID)
	}
	empty, err := s.GetManyByID(ctx, nil)
	noErr(t, err)
	if len(empty) != 0 {
		t.Fatalf("empty = %+v", empty)
	}
}

var base = time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)

func doc(owner, id, status string, age time.Duration) store.Document {
	return store.Document{ID: id, OwnerID: owner, Name: id + ".pdf", Type: "PDF", ContentType: "application/pdf",
		SizeBytes: 100, S3Key: "users/" + owner + "/" + id + "/" + id + ".pdf", Status: status,
		CreatedAt: base.Add(-age)}
}

func ListReadyNewestFirstExcludesPending(t *testing.T, f DocFactory) {
	s := f(t)
	noErr(t, s.PutDoc(ctx, doc("o1", "old", store.DocReady, 3*time.Hour)))
	noErr(t, s.PutDoc(ctx, doc("o1", "new", store.DocReady, time.Hour)))
	noErr(t, s.PutDoc(ctx, doc("o1", "mid", store.DocReady, 2*time.Hour)))
	p := doc("o1", "pend", store.DocPending, 0)
	p.TTL = base.Add(24 * time.Hour).Unix()
	noErr(t, s.PutDoc(ctx, p))
	noErr(t, s.PutDoc(ctx, doc("o2", "theirs", store.DocReady, 0)))

	got, err := s.ListReady(ctx, "o1", 0)
	noErr(t, err)
	var ids []string
	for _, d := range got {
		ids = append(ids, d.ID)
	}
	if fmt.Sprint(ids) != "[new mid old]" {
		t.Fatalf("ids = %v, want [new mid old]", ids)
	}
	none, err := s.ListReady(ctx, "nobody", 10)
	noErr(t, err)
	if len(none) != 0 {
		t.Fatalf("unexpected %v", none)
	}
}

func ListReadyLimitAndCap(t *testing.T, f DocFactory) {
	s := f(t)
	for i := 0; i < store.MaxListReady+5; i++ {
		noErr(t, s.PutDoc(ctx, doc("o1", fmt.Sprintf("d%04d", i), store.DocReady, time.Duration(i)*time.Second)))
	}
	two, err := s.ListReady(ctx, "o1", 2)
	noErr(t, err)
	if len(two) != 2 || two[0].ID != "d0000" || two[1].ID != "d0001" {
		t.Fatalf("limit 2 = %+v", two)
	}
	all, err := s.ListReady(ctx, "o1", 100000)
	noErr(t, err)
	if len(all) != store.MaxListReady {
		t.Fatalf("len = %d, want cap %d", len(all), store.MaxListReady)
	}
}

func DocsAreOwnerScoped(t *testing.T, f DocFactory) {
	s := f(t)
	noErr(t, s.PutDoc(ctx, doc("o1", "d1", store.DocReady, 0)))
	got, err := s.GetDoc(ctx, "o1", "d1")
	noErr(t, err)
	if got.OwnerID != "o1" || got.S3Key == "" {
		t.Fatalf("got %+v", got)
	}
	_, err = s.GetDoc(ctx, "o2", "d1")
	is(t, err, store.ErrNotFound)
	is(t, s.MarkReady(ctx, "o2", "d1", 3), store.ErrNotFound)
	is(t, s.DeleteDoc(ctx, "o2", "d1"), store.ErrNotFound)
	docs, err := s.GetDocs(ctx, "o2", []string{"d1"})
	noErr(t, err)
	if len(docs) != 0 {
		t.Fatalf("foreign GetDocs = %+v", docs)
	}
	if _, err := s.GetDoc(ctx, "o1", "d1"); err != nil {
		t.Fatalf("owner's document must be untouched: %v", err)
	}
}

func MarkReady(t *testing.T, f DocFactory) {
	s := f(t)
	p := doc("o1", "d1", store.DocPending, 0)
	p.TTL = base.Add(24 * time.Hour).Unix()
	noErr(t, s.PutDoc(ctx, p))
	noErr(t, s.MarkReady(ctx, "o1", "d1", 7))
	got, err := s.GetDoc(ctx, "o1", "d1")
	noErr(t, err)
	if got.Status != store.DocReady || got.Pages != 7 || got.TTL != 0 {
		t.Fatalf("got %+v", got)
	}
	is(t, s.MarkReady(ctx, "o1", "d1", 9), store.ErrPrecondition)
	is(t, s.MarkReady(ctx, "o1", "missing", 1), store.ErrNotFound)
	ready, _ := s.ListReady(ctx, "o1", 10)
	if len(ready) != 1 {
		t.Fatalf("ready = %+v", ready)
	}
}

func GetDocsAndDelete(t *testing.T, f DocFactory) {
	s := f(t)
	noErr(t, s.PutDoc(ctx, doc("o1", "a", store.DocReady, 0)))
	noErr(t, s.PutDoc(ctx, doc("o1", "b", store.DocPending, 0)))
	got, err := s.GetDocs(ctx, "o1", []string{"b", "zz", "a", "b"})
	noErr(t, err)
	if len(got) != 2 || got[0].ID != "b" || got[1].ID != "a" {
		t.Fatalf("GetDocs = %+v", got)
	}
	noErr(t, s.DeleteDoc(ctx, "o1", "a"))
	_, err = s.GetDoc(ctx, "o1", "a")
	is(t, err, store.ErrNotFound)
	is(t, s.DeleteDoc(ctx, "o1", "a"), store.ErrNotFound)
}
