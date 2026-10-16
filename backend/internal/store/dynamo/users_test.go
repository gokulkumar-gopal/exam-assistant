package dynamo

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"github.com/gokulkumar/exam-assistant/backend/internal/store"
)

var bg = context.Background()

func u(id, email string) store.User {
	return store.User{ID: id, Name: "N", Email: email, PasswordHash: "h", Role: "student",
		CreatedAt: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)}
}

func TestCreateUserTransaction(t *testing.T) {
	st, f := newStores()
	if err := st.CreateUser(bg, u("u1", "a@x.com")); err != nil {
		t.Fatal(err)
	}
	if len(f.txs) != 1 || f.saw("TransactWriteItems") != 1 {
		t.Fatalf("want exactly one TransactWriteItems, calls = %v", f.calls)
	}
	items := f.txs[0].TransactItems
	if len(items) != 2 {
		t.Fatalf("items = %d, want user + email lock", len(items))
	}
	ids := []string{}
	for _, it := range items {
		if it.Put == nil || *it.Put.TableName != "test-users" || aws.ToString(it.Put.ConditionExpression) != "attribute_not_exists(id)" {
			t.Fatalf("bad item %+v", it.Put)
		}
		ids = append(ids, s(it.Put.Item["id"]))
	}
	if fmt.Sprint(ids) != "[u1 email#a@x.com]" {
		t.Fatalf("ids = %v", ids)
	}
	if s(items[1].Put.Item["userId"]) != "u1" {
		t.Fatalf("email lock must point at the user: %+v", items[1].Put.Item)
	}
}

func TestCreateGoogleUserHasThreeItems(t *testing.T) {
	st, f := newStores()
	g := u("g1", "g@x.com")
	g.PasswordHash, g.GoogleSub = "", "sub-1"
	if err := st.CreateGoogleUser(bg, g); err != nil {
		t.Fatal(err)
	}
	items := f.txs[0].TransactItems
	if len(items) != 3 {
		t.Fatalf("items = %d, want 3", len(items))
	}
	if s(items[2].Put.Item["id"]) != "google#sub-1" || s(items[2].Put.Item["userId"]) != "g1" {
		t.Fatalf("google lock = %+v", items[2].Put.Item)
	}
	if _, has := items[0].Put.Item["passwordHash"]; has {
		t.Fatal("empty passwordHash must be omitted")
	}
	for _, it := range items {
		if aws.ToString(it.Put.ConditionExpression) != "attribute_not_exists(id)" {
			t.Fatalf("missing condition on %v", it.Put.Item["id"])
		}
	}
}

func TestLinkGoogleCondition(t *testing.T) {
	st, f := newStores()
	if err := st.CreateUser(bg, u("u1", "a@x.com")); err != nil {
		t.Fatal(err)
	}
	if err := st.LinkGoogle(bg, "u1", "sub-1", 1700000000); err != nil {
		t.Fatal(err)
	}
	tx := f.txs[len(f.txs)-1]
	if len(tx.TransactItems) != 2 {
		t.Fatalf("items = %d", len(tx.TransactItems))
	}
	lock, upd := tx.TransactItems[0].Put, tx.TransactItems[1].Update
	if lock == nil || s(lock.Item["id"]) != "google#sub-1" || aws.ToString(lock.ConditionExpression) != "attribute_not_exists(id)" {
		t.Fatalf("lock = %+v", lock)
	}
	if upd == nil {
		t.Fatal("second item must be the user update")
	}
	cond := aws.ToString(upd.ConditionExpression)
	if cond != "attribute_exists(id) AND attribute_not_exists(googleSub)" {
		t.Fatalf("condition = %q", cond)
	}
	if ex := aws.ToString(upd.UpdateExpression); ex != "SET googleSub = :sub, sessionsValidAfter = :va REMOVE passwordHash" {
		t.Fatalf("update = %q", ex)
	}
	if upd.ReturnValuesOnConditionCheckFailure != types.ReturnValuesOnConditionCheckFailureAllOld {
		t.Fatal("update must request ALL_OLD to tell missing from already-linked")
	}
}

func TestCancelledTransactionIsConflict(t *testing.T) {
	st, _ := newStores()
	if err := st.CreateUser(bg, u("u1", "a@x.com")); err != nil {
		t.Fatal(err)
	}
	if err := st.CreateUser(bg, u("u2", "a@x.com")); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("err = %v, want ErrConflict", err)
	}
}

func TestOtherCancellationReasonsAreNotConflicts(t *testing.T) {
	for _, code := range []string{"TransactionConflict", "ThrottlingError", "ProvisionedThroughputExceeded", "ValidationError"} {
		st, f := newStores()
		st.sleep = noSleep
		f.failTx = &types.TransactionCanceledException{CancellationReasons: []types.CancellationReason{
			{Code: aws.String(code)}, {Code: aws.String("None")}}}
		f.failTxCount = 100 // persistent: retries must give up, not turn it into a conflict
		err := st.CreateUser(bg, u("u1", "a@x.com"))
		if err == nil || errors.Is(err, store.ErrConflict) || errors.Is(err, store.ErrPrecondition) || errors.Is(err, store.ErrNotFound) {
			t.Fatalf("%s: err = %v, want an infrastructure error", code, err)
		}
	}
}

func TestLinkGoogleDistinguishesMissingFromLinked(t *testing.T) {
	st, _ := newStores()
	if err := st.LinkGoogle(bg, "ghost", "sub-9", 1); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("missing user: %v", err)
	}
	_ = st.CreateUser(bg, u("u1", "a@x.com"))
	_ = st.CreateUser(bg, u("u2", "b@x.com"))
	_ = st.LinkGoogle(bg, "u1", "sub-1", 1)
	if err := st.LinkGoogle(bg, "u1", "sub-2", 2); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("already linked: %v", err)
	}
	if err := st.LinkGoogle(bg, "u2", "sub-1", 2); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("lock taken: %v", err)
	}
	// Missing user wins over a taken lock.
	if err := st.LinkGoogle(bg, "ghost", "sub-1", 2); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("missing user + taken lock: %v", err)
	}
}

func TestLockItemsAreNotUsers(t *testing.T) {
	st, _ := newStores()
	_ = st.CreateUser(bg, u("u1", "a@x.com"))
	if _, err := st.GetByID(bg, "email#a@x.com"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("lock item returned as user: %v", err)
	}
	m, err := st.GetManyByID(bg, []string{"email#a@x.com", "u1"})
	if err != nil || len(m) != 1 {
		t.Fatalf("m=%v err=%v", m, err)
	}
}

func TestGetManyChunksAndHandlesUnprocessed(t *testing.T) {
	st, f := newStores()
	var ids, emails []string
	for i := 0; i < 250; i++ {
		id, em := fmt.Sprintf("u%03d", i), fmt.Sprintf("e%03d@x.com", i)
		if err := st.CreateUser(bg, u(id, em)); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id, id) // duplicates must be collapsed
		emails = append(emails, em)
	}
	f.unprocessedOnce = true
	got, err := st.GetManyByID(bg, ids)
	if err != nil || len(got) != 250 {
		t.Fatalf("len=%d err=%v", len(got), err)
	}
	for _, b := range f.batch {
		for _, ka := range b.RequestItems {
			if len(ka.Keys) > 100 {
				t.Fatalf("batch of %d keys", len(ka.Keys))
			}
			if !aws.ToBool(ka.ConsistentRead) {
				t.Fatal("batch reads should be consistent")
			}
		}
	}
	f.batch, f.pendingRetry = nil, nil
	byEmail, err := st.GetManyByEmail(bg, emails)
	if err != nil || len(byEmail) != 250 || byEmail["e007@x.com"].ID != "u007" {
		t.Fatalf("len=%d err=%v", len(byEmail), err)
	}
}

func noSleep(context.Context, time.Duration) error { return nil }

func conflictErr() error {
	return &types.TransactionCanceledException{CancellationReasons: []types.CancellationReason{
		{Code: aws.String("TransactionConflict")}, {Code: aws.String("None")}}}
}

func TestTransactionConflictIsRetried(t *testing.T) {
	for name, e := range map[string]error{"cancelled": conflictErr(), "in-progress": &types.TransactionInProgressException{}} {
		st, f := newStores()
		var slept int
		st.sleep = func(context.Context, time.Duration) error { slept++; return nil }
		f.failTx, f.failTxCount = e, 2
		if err := st.CreateUser(bg, u("u1", "a@x.com")); err != nil {
			t.Fatalf("%s: err = %v", name, err)
		}
		if f.saw("TransactWriteItems") != 3 || slept != 2 {
			t.Fatalf("%s: tx calls = %d, sleeps = %d", name, f.saw("TransactWriteItems"), slept)
		}
		if _, err := st.GetByID(bg, "u1"); err != nil {
			t.Fatalf("%s: user missing after retry: %v", name, err)
		}
	}
}

func TestPersistentTransactionConflictGivesUp(t *testing.T) {
	st, f := newStores()
	st.sleep = noSleep
	f.failTx, f.failTxCount = conflictErr(), 100
	err := st.CreateUser(bg, u("u1", "a@x.com"))
	if err == nil || errors.Is(err, store.ErrConflict) {
		t.Fatalf("err = %v, want non-nil and not ErrConflict", err)
	}
	if f.saw("TransactWriteItems") != 3 {
		t.Fatalf("attempts = %d, want 3", f.saw("TransactWriteItems"))
	}
}

func TestRealConflictIsNotRetried(t *testing.T) {
	st, f := newStores()
	st.sleep = noSleep
	_ = st.CreateUser(bg, u("u1", "a@x.com"))
	before := f.saw("TransactWriteItems")
	if err := st.CreateUser(bg, u("u2", "a@x.com")); !errors.Is(err, store.ErrConflict) {
		t.Fatal(err)
	}
	if f.saw("TransactWriteItems")-before != 1 {
		t.Fatal("uniqueness conflict must not be retried")
	}
}
