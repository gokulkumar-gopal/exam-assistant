package api

import (
	"net/http"
	"time"

	"github.com/gokulkumar/exam-assistant/backend/internal/gen"
)

// registerAIRoutes serves GET /ai/models (backend-spec 8.6): the enabled models, cheapest
// first, with exactly one marked default. The list never changes while the process runs,
// so approximate job costs are computed once.
func registerAIRoutes(rt *routeTable, deps Deps) {
	type jobCost struct {
		Small float64 `json:"small"`
		Heavy float64 `json:"heavy"`
	}
	type modelOut struct {
		ID            string  `json:"id"`
		Name          string  `json:"name"`
		Provider      string  `json:"provider"`
		Tier          string  `json:"tier"`
		PriceIn       float64 `json:"priceInPerMTok"`
		PriceOut      float64 `json:"priceOutPerMTok"`
		ApproxJobCost jobCost `json:"approxJobCost"`
		Default       bool    `json:"default"`
	}
	models := make([]modelOut, 0, len(deps.Models))
	for _, m := range deps.Models {
		small, heavy := gen.ApproxJobCost(m)
		models = append(models, modelOut{
			ID: m.ID, Name: m.Name, Provider: m.Provider, Tier: m.Tier,
			PriceIn: m.InPerMTok, PriceOut: m.OutPerMTok,
			ApproxJobCost: jobCost{Small: small, Heavy: heavy},
			Default:       m.ID == deps.DefaultModelID,
		})
	}
	rt.handle("GET", "/ai/models", RequireUser(deps, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		WriteJSON(w, http.StatusOK, map[string]any{
			"models":     models,
			"serverTime": deps.Clock.Now().UTC().Format(time.RFC3339),
		})
	})))
	if deps.Usage == nil {
		return
	}
	// Aggregate counters only: nothing about users, documents or questions is read or returned.
	rt.handle("GET", "/ai/usage", RequireUser(deps, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		v, err := deps.Usage.View(r.Context(), deps.Clock.Now())
		if err != nil {
			WriteError(w, r, err)
			return
		}
		WriteJSON(w, http.StatusOK, v)
	})))
}
