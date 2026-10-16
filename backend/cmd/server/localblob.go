package main

// LOCAL ONLY. This file makes `make run` able to upload files without S3: the Blob
// behind the Docs service is the in-memory fake, and presigned URLs point at PUT/GET
// /local-blob/{key} on this same server. It is unauthenticated and keeps everything in
// memory; it belongs to cmd/server and must never be compiled into cmd/lambda.

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gokulkumar/exam-assistant/backend/internal/docs"
	"github.com/gokulkumar/exam-assistant/backend/internal/store"
)

const localBlobPrefix = "/local-blob/"

// localBlob is a docs.Blob whose presigned URLs address the local-blob endpoint.
type localBlob struct {
	*docs.MemBlob
	base string // scheme://host:port of this server
}

func newLocalBlob(m *docs.MemBlob, base string) *localBlob {
	return &localBlob{MemBlob: m, base: strings.TrimRight(base, "/")}
}

func (b *localBlob) PresignPut(_ context.Context, key, _ string, _ int64, _ time.Duration) (string, error) {
	return b.base + localBlobPrefix + key, nil
}

func (b *localBlob) PresignGet(_ context.Context, key, _ string, _ time.Duration) (string, error) {
	return b.base + localBlobPrefix + key, nil
}

// handler serves PUT (store), GET (read) and OPTIONS (CORS preflight for the browser app).
func (b *localBlob) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Access-Control-Allow-Origin", "*")
		h.Set("Access-Control-Allow-Methods", "GET, PUT, HEAD, OPTIONS")
		h.Set("Access-Control-Allow-Headers", "*")
		h.Set("Access-Control-Expose-Headers", "ETag")

		key, err := url.PathUnescape(strings.TrimPrefix(r.URL.EscapedPath(), localBlobPrefix))
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if err != nil || !strings.HasPrefix(key, "users/") || strings.Contains(key, "..") || strings.Contains(key, "//") {
			http.Error(w, "invalid key", http.StatusBadRequest)
			return
		}
		switch r.Method {
		case http.MethodPut:
			data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, docs.MaxUploadBytes))
			if err != nil {
				http.Error(w, "upload too large or interrupted", http.StatusRequestEntityTooLarge)
				return
			}
			b.Put(key, data)
			h.Set("ETag", `"local"`)
			w.WriteHeader(http.StatusOK)
		case http.MethodGet, http.MethodHead:
			data, err := b.Get(r.Context(), key, docs.MaxUploadBytes)
			if errors.Is(err, store.ErrNotFound) {
				http.NotFound(w, r)
				return
			}
			if err != nil {
				http.Error(w, "read failed", http.StatusInternalServerError)
				return
			}
			h.Set("Content-Type", http.DetectContentType(data))
			_, _ = w.Write(data)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})
}

// baseURLFromAddr turns a listen address into the URL a browser on this machine uses.
func baseURLFromAddr(addr string) string {
	host, port, ok := strings.Cut(addr, ":")
	if !ok {
		return "http://" + addr
	}
	if host == "" || host == "0.0.0.0" {
		host = "localhost"
	}
	return "http://" + host + ":" + port
}
