package docs

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"unicode/utf8"

	"github.com/gokulkumar/exam-assistant/backend/internal/apperr"
	"github.com/gokulkumar/exam-assistant/backend/internal/store"
)

var pngBytes = append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, 32)...)
var jpegBytes = append([]byte{0xFF, 0xD8, 0xFF, 0xE0}, make([]byte, 32)...)

// buildPDF returns a valid PDF with n empty pages.
func buildPDF(n int) []byte {
	var b bytes.Buffer
	offs := []int{}
	obj := func(s string) {
		offs = append(offs, b.Len())
		fmt.Fprintf(&b, "%d 0 obj\n%s\nendobj\n", len(offs), s)
	}
	b.WriteString("%PDF-1.4\n")
	obj("<< /Type /Catalog /Pages 2 0 R >>")
	kids := ""
	for i := 0; i < n; i++ {
		kids += fmt.Sprintf("%d 0 R ", 3+i)
	}
	obj(fmt.Sprintf("<< /Type /Pages /Kids [%s] /Count %d >>", kids, n))
	for i := 0; i < n; i++ {
		obj("<< /Type /Page /Parent 2 0 R /MediaBox [0 0 200 200] >>")
	}
	x := b.Len()
	fmt.Fprintf(&b, "xref\n0 %d\n0000000000 65535 f \n", len(offs)+1)
	for _, o := range offs {
		fmt.Fprintf(&b, "%010d 00000 n \n", o)
	}
	fmt.Fprintf(&b, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(offs)+1, x)
	return b.Bytes()
}

// complete uploads (pending row) and, if data != nil, puts the object.
func (e *env) pending(t *testing.T, name, mime string, declared int64, data []byte) string {
	t.Helper()
	out := e.upload(t, UploadInput{Name: name, Type: mime, SizeBytes: declared})
	if data != nil {
		d, _ := e.docs.GetDoc(context.Background(), e.owner.ID, out.DocID)
		e.blob.Put(d.S3Key, data)
	}
	return out.DocID
}

func (e *env) assertGone(t *testing.T, id string) {
	t.Helper()
	if _, err := e.docs.GetDoc(context.Background(), e.owner.ID, id); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("row still present: %v", err)
	}
	if len(e.blob.objects) != 0 {
		t.Errorf("objects left: %d", len(e.blob.objects))
	}
}

func wantApp(t *testing.T, err error, status int, msg string) {
	t.Helper()
	var ae *apperr.Error
	if !errors.As(err, &ae) {
		t.Fatalf("err = %v, want *apperr.Error", err)
	}
	if ae.Status != status || (msg != "" && ae.Message != msg) {
		t.Fatalf("got %d %q, want %d %q", ae.Status, ae.Message, status, msg)
	}
}

func TestCompleteMissingObject409(t *testing.T) {
	e := newEnv()
	id := e.pending(t, "a.pdf", "application/pdf", 100, nil)
	_, err := e.svc.Complete(context.Background(), e.owner, id)
	wantApp(t, err, http.StatusConflict, "The upload didn't finish. Try again.")
	if _, err := e.docs.GetDoc(context.Background(), "user-1", id); err != nil {
		t.Errorf("row must be kept for retry: %v", err)
	}
	// retry after the upload finishes works
	d, _ := e.docs.GetDoc(context.Background(), "user-1", id)
	pdf := buildPDF(1)
	e.blob.Put(d.S3Key, pdf)
	d.SizeBytes = int64(len(pdf))
	_ = e.docs.PutDoc(context.Background(), d)
	if _, err := e.svc.Complete(context.Background(), e.owner, id); err != nil {
		t.Fatalf("retry: %v", err)
	}
}

func TestCompleteSizeMismatchDeletesObjectAndRow422(t *testing.T) {
	e := newEnv()
	pdf := buildPDF(1)
	id := e.pending(t, "a.pdf", "application/pdf", int64(len(pdf))+5, pdf)
	_, err := e.svc.Complete(context.Background(), e.owner, id)
	wantApp(t, err, http.StatusUnprocessableEntity, "")
	e.assertGone(t, id)
}

func TestCompleteMagicBytesMismatch422(t *testing.T) {
	cases := []struct {
		name, mime string
		body       []byte
	}{
		{"a.pdf", "image/png", buildPDF(1)},
		{"a.png", "application/pdf", pngBytes},
		{"a.jpg", "image/jpeg", pngBytes},
		{"a.txt", "text/plain", []byte{0xff, 0xfe, 0xfd}},
	}
	for _, c := range cases {
		e := newEnv()
		id := e.pending(t, c.name, c.mime, int64(len(c.body)), c.body)
		_, err := e.svc.Complete(context.Background(), e.owner, id)
		wantApp(t, err, http.StatusUnprocessableEntity, "That file doesn't match its type.")
		e.assertGone(t, id)
	}
}

func TestSniff(t *testing.T) {
	if !Sniff([]byte("%PDF-1.7"), "application/pdf") || Sniff([]byte("%PDX-"), "application/pdf") || Sniff(nil, "application/pdf") {
		t.Error("pdf")
	}
	if !Sniff(pngBytes, "image/png") || Sniff(jpegBytes, "image/png") {
		t.Error("png")
	}
	if !Sniff(jpegBytes, "image/jpeg") || Sniff(pngBytes, "image/jpeg") {
		t.Error("jpeg")
	}
	if Sniff([]byte("hello"), "application/zip") {
		t.Error("unknown mime must not sniff")
	}
}

func TestCompleteTXTMustBeUTF8First4KB(t *testing.T) {
	ok := []byte(strings.Repeat("a", 5000))
	if !Sniff(ok[:4096], "text/plain") {
		t.Error("ascii")
	}
	// invalid byte inside the first 4 KB
	bad := append([]byte(strings.Repeat("a", 100)), 0xff)
	bad = append(bad, []byte(strings.Repeat("a", 5000))...)
	if Sniff(bad, "text/plain") {
		t.Error("invalid utf-8 in first 4KB accepted")
	}
	// invalid byte after 4 KB is not inspected
	late := append([]byte(strings.Repeat("a", 4200)), 0xff)
	if !Sniff(late, "text/plain") {
		t.Error("only the first 4KB is checked")
	}
	// a 3-byte rune whose first byte is the 4096th byte (split at the boundary)
	for pad := 4094; pad <= 4095; pad++ {
		split := append([]byte(strings.Repeat("a", pad)), []byte(strings.Repeat("€", 50))...)
		if utf8.Valid(split[:4096]) {
			t.Fatalf("pad %d: fixture does not split a rune", pad)
		}
		if !Sniff(split, "text/plain") {
			t.Errorf("pad %d: valid file rejected because a rune straddles 4096", pad)
		}
	}
	// but a genuinely invalid tail inside the window is still rejected
	if Sniff(append([]byte(strings.Repeat("a", 4095)), 0xff, 'a', 'a'), "text/plain") {
		t.Error("0xff at 4096 accepted")
	}
	// short file with truncated rune is invalid
	if Sniff([]byte("ab\xe2\x82"), "text/plain") {
		t.Error("truncated rune in short file accepted")
	}
}

func TestCompleteTXTSplitRuneEndToEnd(t *testing.T) {
	e := newEnv()
	body := append([]byte(strings.Repeat("a", 4094)), []byte(strings.Repeat("€", 10))...)
	id := e.pending(t, "a.txt", "text/plain", int64(len(body)), body)
	d, err := e.svc.Complete(context.Background(), e.owner, id)
	if err != nil || d.Pages != 2 {
		t.Fatalf("d=%+v err=%v", d, err)
	}
}

func TestPagesPDF(t *testing.T) {
	for _, n := range []int{1, 3} {
		got, err := CountPages("application/pdf", buildPDF(n))
		if err != nil || got != n {
			t.Errorf("pages(%d) = %d, %v", n, got, err)
		}
	}
}

func TestPagesPDFBadInputNeverPanics(t *testing.T) {
	garbage := append([]byte("%PDF-1.4\n"), bytes.Repeat([]byte{0x00, 0xff, 'x'}, 500)...)
	zero := buildPDF(0)
	for name, in := range map[string][]byte{"garbage": garbage, "zero": zero, "header only": []byte("%PDF-"), "truncated": buildPDF(3)[:120]} {
		n, err := CountPages("application/pdf", in)
		if err == nil || n != 0 {
			t.Errorf("%s: n=%d err=%v, want error", name, n, err)
		}
	}
}

func TestCompleteCorruptPDF422DeletesObjectAndRow(t *testing.T) {
	e := newEnv()
	body := append([]byte("%PDF-1.4\n"), bytes.Repeat([]byte{0x00, 0xff, 'x'}, 500)...)
	id := e.pending(t, "a.pdf", "application/pdf", int64(len(body)), body)
	_, err := e.svc.Complete(context.Background(), e.owner, id)
	wantApp(t, err, http.StatusUnprocessableEntity, "We couldn't read that PDF. It may be damaged or password protected.")
	e.assertGone(t, id)
}

func TestPagesImage1(t *testing.T) {
	for _, m := range []string{"image/png", "image/jpeg"} {
		if n, err := CountPages(m, pngBytes); err != nil || n != 1 {
			t.Errorf("%s: %d %v", m, n, err)
		}
	}
}

func TestPagesTXT(t *testing.T) {
	for _, c := range []struct {
		chars, want int
	}{{0, 1}, {1, 1}, {3000, 1}, {3001, 2}, {6000, 2}, {6001, 3}} {
		got, err := CountPages("text/plain", []byte(strings.Repeat("é", c.chars)))
		if err != nil || got != c.want {
			t.Errorf("%d chars: %d, %v; want %d", c.chars, got, err, c.want)
		}
	}
}

func TestCompleteHappyPathAndClearsTTL(t *testing.T) {
	e := newEnv()
	pdf := buildPDF(3)
	id := e.pending(t, "Biology.pdf", "application/pdf", int64(len(pdf)), pdf)
	before, _ := e.docs.GetDoc(context.Background(), "user-1", id)
	if before.TTL == 0 {
		t.Fatal("precondition: pending row has TTL")
	}
	d, err := e.svc.Complete(context.Background(), e.owner, id)
	if err != nil {
		t.Fatal(err)
	}
	if d.Status != store.DocReady || d.Pages != 3 || d.TTL != 0 || d.Name != "Biology.pdf" || d.ID != id {
		t.Errorf("returned %+v", d)
	}
	got, _ := e.docs.GetDoc(context.Background(), "user-1", id)
	if got.Status != store.DocReady || got.Pages != 3 || got.TTL != 0 {
		t.Errorf("stored %+v", got)
	}
}

func TestCompleteClearsTTL(t *testing.T) {
	e := newEnv()
	id := e.pending(t, "a.png", "image/png", int64(len(pngBytes)), pngBytes)
	if _, err := e.svc.Complete(context.Background(), e.owner, id); err != nil {
		t.Fatal(err)
	}
	e.clk.Advance(1000 * 3600 * 1e9) // far past the 24h pending TTL
	d, err := e.docs.GetDoc(context.Background(), "user-1", id)
	if err != nil || d.TTL != 0 || d.Pages != 1 {
		t.Fatalf("d=%+v err=%v", d, err)
	}
}

type countingBlob struct {
	Blob
	gets, heads atomic.Int32
}

func (c *countingBlob) Get(ctx context.Context, k string, m int64) ([]byte, error) {
	c.gets.Add(1)
	return c.Blob.Get(ctx, k, m)
}
func (c *countingBlob) Head(ctx context.Context, k string) (int64, error) {
	c.heads.Add(1)
	return c.Blob.Head(ctx, k)
}

func TestCompleteIdempotent(t *testing.T) {
	e := newEnv()
	cb := &countingBlob{Blob: e.blob}
	e.svc.blob = cb
	id := e.pending(t, "a.png", "image/png", int64(len(pngBytes)), pngBytes)
	first, err := e.svc.Complete(context.Background(), e.owner, id)
	if err != nil {
		t.Fatal(err)
	}
	if cb.gets.Load() != 1 {
		t.Fatalf("gets after first = %d", cb.gets.Load())
	}
	second, err := e.svc.Complete(context.Background(), e.owner, id)
	if err != nil || second != first {
		t.Fatalf("second = %+v, %v; first %+v", second, err, first)
	}
	if cb.gets.Load() != 1 {
		t.Errorf("second call did a Get (%d)", cb.gets.Load())
	}
}

// racyDocs simulates a concurrent complete: the row becomes ready just before our MarkReady.
type racyDocs struct{ store.DocStore }

func (r racyDocs) MarkReady(ctx context.Context, o, id string, pages int) error {
	if err := r.DocStore.MarkReady(ctx, o, id, pages); err != nil {
		return err
	}
	return store.ErrPrecondition
}

func TestCompleteConcurrentReturnsReadyDoc(t *testing.T) {
	e := newEnv()
	e.svc.docs = racyDocs{e.track}
	id := e.pending(t, "a.png", "image/png", int64(len(pngBytes)), pngBytes)
	d, err := e.svc.Complete(context.Background(), e.owner, id)
	if err != nil || d.Status != store.DocReady || d.Pages != 1 {
		t.Fatalf("d=%+v err=%v", d, err)
	}
}

func TestCompleteOtherUsersDoc404(t *testing.T) {
	e := newEnv()
	id := e.pending(t, "a.png", "image/png", int64(len(pngBytes)), pngBytes)
	_, errOther := e.svc.Complete(context.Background(), store.User{ID: "user-2"}, id)
	_, errMissing := e.svc.Complete(context.Background(), e.owner, "nope")
	wantApp(t, errOther, http.StatusNotFound, "")
	wantApp(t, errMissing, http.StatusNotFound, "")
	if errOther.Error() != errMissing.Error() {
		t.Errorf("responses differ: %v vs %v", errOther, errMissing)
	}
	if d, _ := e.docs.GetDoc(context.Background(), "user-1", id); d.Status != store.DocPending {
		t.Error("foreign complete changed the row")
	}
}

func TestTrimPDFValidRange(t *testing.T) {
	for _, c := range []struct{ from, to, want int }{{1, 1, 1}, {3, 7, 5}, {1, 10, 10}, {10, 10, 1}} {
		out, err := TrimPDF(buildPDF(10), c.from, c.to)
		if err != nil {
			t.Fatalf("%d-%d: %v", c.from, c.to, err)
		}
		if n, err := CountPages("application/pdf", out); err != nil || n != c.want {
			t.Errorf("%d-%d: pages = %d, %v, want %d", c.from, c.to, n, err, c.want)
		}
	}
}

func TestTrimPDFOutOfRange(t *testing.T) {
	for name, r := range map[string][2]int{"past the end": {5, 11}, "from zero": {0, 3}, "reversed": {4, 2}, "negative": {-1, 2}, "all past": {11, 12}} {
		out, err := TrimPDF(buildPDF(10), r[0], r[1])
		if err == nil || !errors.Is(err, ErrUnreadable) {
			t.Errorf("%s: out=%d bytes err=%v, want ErrUnreadable", name, len(out), err)
		}
	}
}

func TestTrimPDFBadInputNeverPanics(t *testing.T) {
	garbage := append([]byte("%PDF-1.4\n"), bytes.Repeat([]byte{0x00, 0xff, 'x'}, 500)...)
	for name, in := range map[string][]byte{"garbage": garbage, "zero pages": buildPDF(0), "header only": []byte("%PDF-"), "truncated": buildPDF(3)[:120], "nil": nil} {
		out, err := TrimPDF(in, 1, 2)
		if err == nil || !errors.Is(err, ErrUnreadable) || out != nil {
			t.Errorf("%s: out=%d err=%v, want ErrUnreadable", name, len(out), err)
		}
	}
}
