package docs

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"

	"github.com/gokulkumar/exam-assistant/backend/internal/store"
)

type recordingHTTP struct {
	reqs   []*http.Request
	status int
}

func (r *recordingHTTP) Do(req *http.Request) (*http.Response, error) {
	r.reqs = append(r.reqs, req)
	return &http.Response{StatusCode: r.status, Status: http.StatusText(r.status), Header: http.Header{}, Body: io.NopCloser(strings.NewReader("")), Request: req}, nil
}

func testS3(t *testing.T, status int) (*S3Blob, *recordingHTTP) {
	t.Helper()
	rec := &recordingHTTP{status: status}
	cfg := aws.Config{
		Region: "us-east-1",
		Credentials: aws.CredentialsProviderFunc(func(context.Context) (aws.Credentials, error) {
			return aws.Credentials{AccessKeyID: "AKIDTEST", SecretAccessKey: "secrettest"}, nil
		}),
		HTTPClient: rec,
	}
	return NewS3Blob(cfg, "test-bucket"), rec
}

func TestS3PresignSignsContentLength(t *testing.T) {
	b, rec := testS3(t, 200)
	raw, err := b.PresignPut(context.Background(), "users/u1/d1/a b.pdf", "application/pdf", 4242, 10*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if len(rec.reqs) != 0 {
		t.Fatalf("presigning made %d network requests", len(rec.reqs))
	}
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	if q.Get("X-Amz-Expires") != "600" {
		t.Errorf("X-Amz-Expires = %q, want 600", q.Get("X-Amz-Expires"))
	}
	signed := strings.Split(q.Get("X-Amz-SignedHeaders"), ";")
	for _, h := range []string{"content-length", "content-type", "host"} {
		found := false
		for _, s := range signed {
			found = found || s == h
		}
		if !found {
			t.Errorf("signed headers %v missing %q", signed, h)
		}
	}
	if !strings.Contains(u.Host, "test-bucket") || !strings.Contains(u.EscapedPath(), "users/u1/d1/a%20b.pdf") {
		t.Errorf("url = %s", raw)
	}
	if q.Get("X-Amz-Signature") == "" {
		t.Error("no signature")
	}
	// A browser sends no checksum header, so none may be required by the signature.
	for k := range q {
		if strings.Contains(strings.ToLower(k), "checksum") {
			t.Errorf("unexpected checksum param %q", k)
		}
	}
	for _, s := range signed {
		if strings.HasPrefix(s, "x-amz-checksum") || s == "x-amz-sdk-checksum-algorithm" {
			t.Errorf("signed checksum header %q", s)
		}
	}
	// The signed length is the exact size: a different size yields a different signature.
	raw2, _ := b.PresignPut(context.Background(), "users/u1/d1/a b.pdf", "application/pdf", 4243, 10*time.Minute)
	u2, _ := url.Parse(raw2)
	if u2.Query().Get("X-Amz-Signature") == q.Get("X-Amz-Signature") {
		t.Error("signature does not depend on size")
	}
	raw3, _ := b.PresignPut(context.Background(), "users/u1/d1/a b.pdf", "image/png", 4242, 10*time.Minute)
	u3, _ := url.Parse(raw3)
	if u3.Query().Get("X-Amz-Signature") == q.Get("X-Amz-Signature") {
		t.Error("signature does not depend on content type")
	}
}

func TestS3PresignGet(t *testing.T) {
	b, _ := testS3(t, 200)
	raw, err := b.PresignGet(context.Background(), "users/u1/d1/x.pdf", "application/pdf", 10*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(raw)
	if u.Query().Get("X-Amz-Expires") != "600" || u.Query().Get("X-Amz-Signature") == "" {
		t.Errorf("url = %s", raw)
	}
}

func TestS3HeadMissingIsNotFound(t *testing.T) {
	b, rec := testS3(t, 404)
	_, err := b.Head(context.Background(), "k")
	if !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("Head 404 = %v, want ErrNotFound", err)
	}
	if len(rec.reqs) != 1 || rec.reqs[0].Method != http.MethodHead {
		t.Errorf("requests = %v", rec.reqs)
	}
}
