package dynamo

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"

	"github.com/gokulkumar/exam-assistant/backend/internal/store"
)

func d(owner, id, status string) store.Document {
	return store.Document{ID: id, OwnerID: owner, Name: id, Type: "PDF", ContentType: "application/pdf",
		SizeBytes: 1, S3Key: "k/" + id, Status: status, CreatedAt: time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)}
}

func TestGetDocUsesConsistentRead(t *testing.T) {
	st, f := newStores()
	_ = st.PutDoc(bg, d("o1", "d1", store.DocReady))
	if _, err := st.GetDoc(bg, "o1", "d1"); err != nil {
		t.Fatal(err)
	}
	if len(f.gets) != 1 || !aws.ToBool(f.gets[0].ConsistentRead) {
		t.Fatalf("GetItem must be consistent: %+v", f.gets)
	}
	if *f.gets[0].TableName != "test-documents" {
		t.Fatalf("table = %s", *f.gets[0].TableName)
	}
}

func TestPutDocOmitsTTLWhenZero(t *testing.T) {
	st, f := newStores()
	_ = st.PutDoc(bg, d("o1", "d1", store.DocReady))
	if _, has := f.puts[0].Item["ttl"]; has {
		t.Fatal("ttl must be omitted when 0")
	}
	p := d("o1", "d2", store.DocPending)
	p.TTL = 4102444800
	_ = st.PutDoc(bg, p)
	if _, has := f.puts[1].Item["ttl"]; !has {
		t.Fatal("ttl must be written when set")
	}
	_ = st.MarkReady(bg, "o1", "d2", 3)
	got := f.tables["test-documents"]["o1\x00d2"]
	if _, has := got["ttl"]; has {
		t.Fatal("MarkReady must remove ttl")
	}
}

func TestListReadyIsQueryNotScan(t *testing.T) {
	st, f := newStores()
	_ = st.PutDoc(bg, d("o1", "a", store.DocReady))
	if _, err := st.ListReady(bg, "o1", 10); err != nil {
		t.Fatal(err)
	}
	if f.saw("Scan") != 0 || f.saw("Query") == 0 {
		t.Fatalf("calls = %v", f.calls)
	}
	q := f.qrys[0]
	if q.ScanIndexForward == nil || *q.ScanIndexForward {
		t.Fatal("ScanIndexForward must be false")
	}
	if !aws.ToBool(q.ConsistentRead) {
		t.Fatal("ListReady Query must set ConsistentRead")
	}
	if v, ok := q.ExpressionAttributeValues[":o"]; !ok || s(v) != "o1" || *q.TableName != "test-documents" {
		t.Fatalf("query = %+v", q)
	}
}

func TestListReadyPaginatesPastPendingPages(t *testing.T) {
	st, f := newStores()
	// Newest 300 are pending; the 3 oldest are ready. Limit applies before the filter, so a
	// single page would return nothing.
	for i := 0; i < 303; i++ {
		status := store.DocPending
		if i < 3 {
			status = store.DocReady
		}
		_ = st.PutDoc(bg, d("o1", fmt.Sprintf("d%04d", i), status))
	}
	got, err := st.ListReady(bg, "o1", 3)
	if err != nil || len(got) != 3 {
		t.Fatalf("len=%d err=%v", len(got), err)
	}
	if got[0].ID != "d0002" || got[2].ID != "d0000" {
		t.Fatalf("order = %v %v %v", got[0].ID, got[1].ID, got[2].ID)
	}
	if f.saw("Query") < 2 {
		t.Fatalf("expected several pages, queries = %d", f.saw("Query"))
	}
}

func TestMarkReadyErrorKinds(t *testing.T) {
	st, _ := newStores()
	_ = st.PutDoc(bg, d("o1", "r", store.DocReady))
	if err := st.MarkReady(bg, "o1", "r", 1); !errors.Is(err, store.ErrPrecondition) {
		t.Fatalf("ready doc: %v", err)
	}
	if err := st.MarkReady(bg, "o1", "none", 1); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("missing: %v", err)
	}
}

func TestUnknownConditionalErrorsPropagate(t *testing.T) {
	// An error that is not a conditional-check failure must not be reported as NotFound.
	st := New(errAPI{}, "test")
	if err := st.DeleteDoc(bg, "o1", "x"); err == nil || errors.Is(err, store.ErrNotFound) {
		t.Fatalf("err = %v", err)
	}
}

func TestGetDocsChunksAndKeepsOrder(t *testing.T) {
	st, f := newStores()
	var ids []string
	for i := 0; i < 230; i++ {
		id := fmt.Sprintf("d%03d", i)
		_ = st.PutDoc(bg, d("o1", id, store.DocReady))
		ids = append(ids, id)
	}
	ids = append(ids, "missing", "d005")
	f.unprocessedOnce = true
	got, err := st.GetDocs(bg, "o1", ids)
	if err != nil || len(got) != 230 {
		t.Fatalf("len=%d err=%v", len(got), err)
	}
	for i, g := range got {
		if g.ID != fmt.Sprintf("d%03d", i) {
			t.Fatalf("order broken at %d: %s", i, g.ID)
		}
	}
	for _, b := range f.batch {
		for _, ka := range b.RequestItems {
			if len(ka.Keys) > 100 {
				t.Fatalf("batch of %d keys", len(ka.Keys))
			}
		}
	}
}

func TestExpiredPendingIsNotFound(t *testing.T) {
	st, _ := newStores()
	st.Now = func() time.Time { return time.Unix(2000, 0) }
	p := d("o1", "p", store.DocPending)
	p.TTL = 1999
	_ = st.PutDoc(bg, p)
	if _, err := st.GetDoc(bg, "o1", "p"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("expired: %v", err)
	}
	got, _ := st.GetDocs(bg, "o1", []string{"p"})
	if len(got) != 0 {
		t.Fatalf("GetDocs returned expired: %v", got)
	}
}

// errAPI fails DeleteItem with a plain error; every other method would panic (nil embed).
type errAPI struct{ DynamoAPI }

func (errAPI) DeleteItem(context.Context, *dynamodb.DeleteItemInput, ...func(*dynamodb.Options)) (*dynamodb.DeleteItemOutput, error) {
	return nil, errors.New("boom")
}
