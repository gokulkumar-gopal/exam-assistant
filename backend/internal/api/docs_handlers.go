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
}
