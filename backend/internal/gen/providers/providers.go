// Package providers is the generator factory: it picks the provider adapter (Anthropic or
// Gemini) from a model's catalog entry and gives it the configuration, keys and the prompt
// package's System, Instruction and Schema. It cannot live in gen: config imports gen, and
// prompt imports gen, so a gen.NewFactory(config.Config) would be an import cycle.
package providers

import (
	"fmt"
	"slices"
	"time"

	"github.com/gokulkumar/exam-assistant/backend/internal/config"
	"github.com/gokulkumar/exam-assistant/backend/internal/gen"
	"github.com/gokulkumar/exam-assistant/backend/internal/gen/prompt"
)

// overrides point the adapters at test servers.
type overrides struct{ AnthropicBaseURL, GeminiBaseURL string }

// New returns the GeneratorFactory for the worker. A failure (unknown model, provider not
// enabled, key missing) is a deployment error: the error is a plain one, so the worker logs
// it and gives the job the generic message. It names the variable that is missing, never a
// value.
func New(cfg config.Config) gen.GeneratorFactory { return newFactory(cfg, overrides{}) }

func newFactory(cfg config.Config, o overrides) gen.GeneratorFactory {
	timeout := time.Duration(cfg.LLMTimeoutSeconds) * time.Second
	return func(modelID string) (gen.Generator, error) {
		m, ok := gen.LookupModel(modelID)
		if !ok {
			return nil, fmt.Errorf("generator factory: model %q is not in the catalog", modelID)
		}
		if !slices.Contains(cfg.Providers, m.Provider) {
			return nil, fmt.Errorf("generator factory: provider %q of model %q is not enabled", m.Provider, modelID)
		}
		switch m.Provider {
		case gen.ProviderAnthropic:
			if cfg.LLMAPIKey == "" {
				return nil, fmt.Errorf("generator factory: LLM_API_KEY is not set (model %q)", modelID)
			}
			return gen.NewAnthropic(cfg.LLMAPIKey, gen.AnthropicConfig{
				Effort: cfg.LLMEffort, Timeout: timeout, BaseURL: o.AnthropicBaseURL,
				System: prompt.System(), Instruction: prompt.Instruction, Schema: prompt.Schema,
			}), nil
		case gen.ProviderGemini:
			if cfg.GeminiAPIKey == "" {
				return nil, fmt.Errorf("generator factory: GEMINI_API_KEY is not set (model %q)", modelID)
			}
			g, err := gen.NewGemini(cfg.GeminiAPIKey, gen.GeminiConfig{
				Effort: cfg.LLMEffort, Timeout: timeout, BaseURL: o.GeminiBaseURL,
				System: prompt.System(), Instruction: prompt.Instruction, Schema: prompt.Schema,
			})
			if err != nil {
				return nil, fmt.Errorf("generator factory: %w", err)
			}
			return g, nil
		}
		return nil, fmt.Errorf("generator factory: no adapter for provider %q", m.Provider)
	}
}
