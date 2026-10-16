package docs

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/oklog/ulid/v2"

	"github.com/gokulkumar/exam-assistant/backend/internal/apperr"
	"github.com/gokulkumar/exam-assistant/backend/internal/clock"
	"github.com/gokulkumar/exam-assistant/backend/internal/store"
	"github.com/gokulkumar/exam-assistant/backend/internal/store/memory"
)

var t0 = time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)

// trackingDocs wraps the store and tracks which rows exist, so tests can see leaked pending rows.
type trackingDocs struct {
	store.DocStore
	live map[string]bool
}

func (t *trackingDocs) PutDoc(ctx context.Context, d store.Document) error {
	t.live[d.OwnerID+"/"+d.ID] = true
	return t.DocStore.PutDoc(ctx, d)
}

func (t *trackingDocs) DeleteDoc(ctx context.Context, owner, id string) error {
	delete(t.live, owner+"/"+id)
	return t.DocStore.DeleteDoc(ctx, owner, id)
}

type env struct {
	svc   *Service
	docs  *memory.DocStore
	track *trackingDocs
	blob  *MemBlob
	clk   *clock.Fake
	owner store.User
}

func newEnv() *env {
	clk := clock.NewFake(t0)
	docs := memory.NewDocStore(clk)
	blob := NewMemBlob()
	tr := &trackingDocs{DocStore: docs, live: map[string]bool{}}
	return &env{svc: NewService(tr, blob, clk), track: tr, docs: docs, blob: blob, clk: clk, owner: store.User{ID: "user-1"}}
}

func (e *env) upload(t *testing.T, in UploadInput) UploadOut {
	t.Helper()
	out, err := e.svc.UploadURL(context.Background(), e.owner, in)
	if err != nil {
		t.Fatalf("UploadURL(%+v): %v", in, err)
	}
	return out
}

var okIn = UploadInput{Name: "Biology.pdf", Type: "application/pdf", SizeBytes: 1000}

func TestUploadURLCreatesPendingRowWith24hTTL(t *testing.T) {
	e := newEnv()
	out := e.upload(t, okIn)
	d, err := e.docs.GetDoc(context.Background(), "user-1", out.DocID)
	if err != nil {
		t.Fatal(err)
	}
	if d.Status != store.DocPending {
		t.Errorf("status = %q", d.Status)
	}
	if want := t0.Add(24 * time.Hour).Unix(); d.TTL != want {
		t.Errorf("TTL = %d, want %d", d.TTL, want)
	}
	if d.Type != "PDF" || d.ContentType != "application/pdf" || d.SizeBytes != 1000 || d.OwnerID != "user-1" || d.Name != "Biology.pdf" {
		t.Errorf("doc = %+v", d)
	}
	if !d.CreatedAt.Equal(t0) {
		t.Errorf("CreatedAt = %v", d.CreatedAt)
	}
	id, err := ulid.ParseStrict(out.DocID)
	if err != nil {
		t.Fatalf("docId not a ULID: %v", err)
	}
	if id.Time() != ulid.Timestamp(t0) {
		t.Errorf("ULID time = %d, want clock time", id.Time())
	}
	if out.Headers["Content-Type"] != "application/pdf" || len(out.Headers) != 1 {
		t.Errorf("headers = %v", out.Headers)
	}
	if out.UploadURL == "" {
		t.Error("empty uploadUrl")
	}
}

func TestUploadURLKeyShape(t *testing.T) {
	e := newEnv()
	out := e.upload(t, UploadInput{Name: "../My Notes?.pdf", Type: "application/pdf", SizeBytes: 5})
	d, _ := e.docs.GetDoc(context.Background(), "user-1", out.DocID)
	want := "users/user-1/" + out.DocID + "/My Notes_.pdf"
	if d.S3Key != want {
		t.Errorf("S3Key = %q, want %q", d.S3Key, want)
	}
	if len(e.blob.Puts) != 1 || e.blob.Puts[0].Key != want {
		t.Errorf("presigned key = %+v", e.blob.Puts)
	}
}

func TestUploadURLTypes(t *testing.T) {
	for mime, typ := range map[string]string{
		"application/pdf": "PDF", "image/jpeg": "JPG", "image/png": "PNG", "text/plain": "TXT",
	} {
		e := newEnv()
		out := e.upload(t, UploadInput{Name: "f", Type: mime, SizeBytes: 1})
		d, _ := e.docs.GetDoc(context.Background(), "user-1", out.DocID)
		if d.Type != typ {
			t.Errorf("%s -> %q, want %q", mime, d.Type, typ)
		}
	}
}

func assertValidation(t *testing.T, err error, field string) {
	t.Helper()
	var ae *apperr.Error
	if !errors.As(err, &ae) || ae.Status != 422 || ae.Code != "validation" {
		t.Fatalf("err = %v, want 422 validation", err)
	}
	if _, ok := ae.Fields[field]; !ok {
		t.Fatalf("fields = %v, want key %q", ae.Fields, field)
	}
}

func TestUploadURLRejectsMime(t *testing.T) {
	for _, mime := range []string{"application/zip", "image/heic", "", "application/PDF", "application/pdf; charset=x"} {
		e := newEnv()
		_, err := e.svc.UploadURL(context.Background(), e.owner, UploadInput{Name: "f", Type: mime, SizeBytes: 10})
		assertValidation(t, err, "type")
		if len(e.blob.Puts) != 0 || len(e.track.live) != 0 {
			t.Errorf("%q: presigned or row left despite rejection", mime)
		}
	}
}

func TestUploadURLSize(t *testing.T) {
	for _, n := range []int64{0, -1, 20971521} {
		e := newEnv()
		_, err := e.svc.UploadURL(context.Background(), e.owner, UploadInput{Name: "f", Type: "text/plain", SizeBytes: n})
		assertValidation(t, err, "sizeBytes")
		if len(e.track.live) != 0 || len(e.blob.Puts) != 0 {
			t.Errorf("size %d: rows=%d puts=%d after rejection", n, len(e.track.live), len(e.blob.Puts))
		}
	}
	for _, n := range []int64{1, 20971520} {
		e := newEnv()
		e.upload(t, UploadInput{Name: "f", Type: "text/plain", SizeBytes: n})
		if len(e.track.live) != 1 {
			t.Errorf("size %d: rows = %d, want 1", n, len(e.track.live))
		}
	}
}

func TestPresignParams(t *testing.T) {
	e := newEnv()
	e.upload(t, UploadInput{Name: "p.png", Type: "image/png", SizeBytes: 4242})
	if len(e.blob.Puts) != 1 {
		t.Fatalf("puts = %d", len(e.blob.Puts))
	}
	p := e.blob.Puts[0]
	if p.TTL != 10*time.Minute || p.Size != 4242 || p.ContentType != "image/png" {
		t.Errorf("presign params = %+v", p)
	}
}

func TestSanitizeName(t *testing.T) {
	cases := []struct{ in, want string }{
		{"Biology.pdf", "Biology.pdf"},
		{"../../x.pdf", "x.pdf"},
		{`..\..\y.pdf`, "y.pdf"},
		{"", "file"},
		{"   ", "file"},
		{"...", "file"},
		{".hidden", "hidden"},
		{"rés umé.pdf", "r_s um_.pdf"},
		{"a\x00b\nc.txt", "a_b_c.txt"},
		{"my-file_v2.final.png", "my-file_v2.final.png"},
	}
	for _, c := range cases {
		if got := SanitizeName(c.in); got != c.want {
			t.Errorf("SanitizeName(%q) = %q, want %q", c.in, got, c.want)
		}
	}
	long := SanitizeName(strings.Repeat("a", 300) + ".pdf")
	if len(long) != 120 || !strings.HasSuffix(long, ".pdf") {
		t.Errorf("long name len=%d %q", len(long), long)
	}
	if got := SanitizeName(strings.Repeat("b", 300)); len(got) != 120 {
		t.Errorf("no-ext len = %d", len(got))
	}
	// Whatever goes in, only allowed characters come out.
	for _, in := range []string{"a/b", "\x7f\x1f", "名前.pdf", "x/../../etc/passwd", strings.Repeat("é", 200)} {
		got := SanitizeName(in)
		if got == "" || len(got) > 120 || strings.ContainsAny(got, "/\\") || strings.HasPrefix(got, ".") {
			t.Errorf("SanitizeName(%q) = %q", in, got)
		}
		for _, r := range got {
			ok := r == '.' || r == '-' || r == '_' || r == ' ' || r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z'
			if !ok {
				t.Errorf("SanitizeName(%q) = %q has disallowed %q", in, got, r)
			}
		}
	}
}

func TestMemBlobHead(t *testing.T) {
	b := NewMemBlob()
	if _, err := b.Head(context.Background(), "k"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("Head missing = %v", err)
	}
	b.Put("k", []byte("hello"))
	if n, err := b.Head(context.Background(), "k"); err != nil || n != 5 {
		t.Errorf("Head = %d, %v", n, err)
	}
	if _, err := b.Get(context.Background(), "k", 3); err == nil {
		t.Error("Get over max should fail")
	}
}

func TestUploadURLKeepsDisplayName(t *testing.T) {
	long := strings.Repeat("é", 200)
	for _, c := range []struct{ in, want, keyEnd string }{
		{"rés umé.pdf", "rés umé.pdf", "/r_s um_.pdf"},
		{"  a\x00b\nc.pdf\t", "abc.pdf", "/a_b_c.pdf_"},
		{" \t\n ", "file", "/__"},
		{long, strings.Repeat("é", 120), "/" + strings.Repeat("_", 120)},
	} {
		e := newEnv()
		out := e.upload(t, UploadInput{Name: c.in, Type: "text/plain", SizeBytes: 1})
		d, _ := e.docs.GetDoc(context.Background(), "user-1", out.DocID)
		if d.Name != c.want || !utf8.ValidString(d.Name) {
			t.Errorf("Name(%q) = %q, want %q", c.in, d.Name, c.want)
		}
		if !strings.HasSuffix(d.S3Key, c.keyEnd) {
			t.Errorf("S3Key(%q) = %q, want suffix %q", c.in, d.S3Key, c.keyEnd)
		}
	}
}
