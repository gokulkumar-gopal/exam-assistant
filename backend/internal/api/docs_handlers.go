package api

import (
	"net/http"
	"time"

	"github.com/gokulkumar/exam-assistant/backend/internal/docs"
)

const docsBodyCap = 1 << 16

func registerDocsRoutes(rt *routeTable, deps Deps) {
	rt.handle("POST", "/documents/upload-url", RequireUser(deps, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Name      string `json:"name"`
			Type      string `json:"type"`
			SizeBytes int64  `json:"sizeBytes"`
		}
		if err := Decode(w, r, &in, docsBodyCap); err != nil {
			WriteError(w, r, err)
			return
		}
		u, _ := UserFrom(r.Context())
		out, err := deps.Docs.UploadURL(r.Context(), u, docs.UploadInput{Name: in.Name, Type: in.Type, SizeBytes: in.SizeBytes})
		if err != nil {
			WriteError(w, r, err)
			return
		}
		WriteJSON(w, http.StatusCreated, map[string]any{
			"docId": out.DocID, "uploadUrl": out.UploadURL, "headers": out.Headers,
		})
	})))

	rt.handle("POST", "/documents/{id}/complete", RequireUser(deps, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, _ := UserFrom(r.Context())
		d, err := deps.Docs.Complete(r.Context(), u, r.PathValue("id"))
		if err != nil {
			WriteError(w, r, err)
			return
		}
		WriteJSON(w, http.StatusOK, map[string]any{
			"id": d.ID, "name": d.Name, "type": d.Type, "pages": d.Pages,
			"sizeBytes": d.SizeBytes, "createdAt": d.CreatedAt.UTC().Format(time.RFC3339),
		})
	})))

	rt.handle("GET", "/documents", RequireUser(deps, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, _ := UserFrom(r.Context())
		ds, err := deps.Docs.List(r.Context(), u)
		if err != nil {
			WriteError(w, r, err)
			return
		}
		out := make([]map[string]any, 0, len(ds))
		for _, d := range ds {
			out = append(out, map[string]any{
				"id": d.ID, "name": d.Name, "type": d.Type, "pages": d.Pages,
				"sizeBytes": d.SizeBytes, "createdAt": d.CreatedAt.UTC().Format(time.RFC3339),
			})
		}
		WriteJSON(w, http.StatusOK, map[string]any{
			"documents": out, "serverTime": deps.Clock.Now().UTC().Format(time.RFC3339),
		})
	})))

	rt.handle("GET", "/documents/{id}/file", RequireUser(deps, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, _ := UserFrom(r.Context())
		f, err := deps.Docs.FileURL(r.Context(), u, r.PathValue("id"))
		if err != nil {
			WriteError(w, r, err)
			return
		}
		WriteJSON(w, http.StatusOK, map[string]any{
			"url": f.URL, "expiresAt": f.ExpiresAt.UTC().Format(time.RFC3339),
		})
	})))

	rt.handle("DELETE", "/documents/{id}", RequireUser(deps, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, _ := UserFrom(r.Context())
		if err := deps.Docs.Delete(r.Context(), u, r.PathValue("id")); err != nil {
			WriteError(w, r, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})))
}
