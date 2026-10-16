package gen

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	"google.golang.org/genai"
)

// The Gemini adapter of AI-spec 3.5, 5.4 and 9.2 (B-2.9). It talks to the Gemini Developer
// API only through the official SDK (google.golang.org/genai v1.75.0) and implements Generator
// and TokenCounter. It holds no mutable state after construction, so up to three Generate
// calls may run at once.
//
// Verified in the SDK source before coding (AI-spec 3.5):
//
//  1. structured output: GenerateContentConfig.ResponseMIMEType ("application/json") and
//     ResponseJsonSchema (any; plain JSON Schema, the alternative to the OpenAPI-style
//     ResponseSchema);
//  2. inputs: Content{Role, Parts}, NewPartFromText and NewPartFromBytes (an inline blob with
//     a MIME type; the bytes are base64 encoded on the wire), GenerateContentConfig.SystemInstruction;
//  3. thinking: ThinkingConfig{ThinkingLevel} with ThinkingLevelLow, ThinkingLevelMedium and
//     ThinkingLevelHigh (Gemini 3 models take a level, not a budget; IncludeThoughts false);
//  4. usage: GenerateContentResponseUsageMetadata.PromptTokenCount (which INCLUDES the cached
//     tokens), CachedContentTokenCount, CandidatesTokenCount (which EXCLUDES thinking) and
//     ThoughtsTokenCount;
//  5. blocks: Candidate.FinishReason (FinishReasonSafety and the other block reasons),
//     GenerateContentResponsePromptFeedback.BlockReason, and an empty Candidates list;
//  6. token counting: Models.CountTokens(ctx, model, contents, config). On the Developer API
//     its config rejects SystemInstruction, Tools and GenerationConfig, so CountTokens sends
//     the system prompt as the first text part of the user content instead;
//  7. retries: HTTPOptions.RetryOptions{Attempts}. With nil RetryOptions the SDK makes ONE
//     attempt; Attempts counts the original request, so one retry is Attempts 2. The SDK
//     retries 408, 429, 5xx and transport errors. HTTPOptions.Timeout would cover all attempts
//     together, so the per-attempt timeout is set on the http.Client instead;
//  8. errors: the SDK returns genai.APIError by value (Code, Message, Status, Details).
//
// No cached-content request is ever made (AI-spec 3.5.6): the adapter never touches Client.Caches
// and never sets GenerateContentConfig.CachedContent, so only implicit caching can apply.

const (
	// geminiMaxOutputTokens leaves room for thinking, which counts against the output limit.
	geminiMaxOutputTokens = 32000
	geminiMaxAttempts     = 2 // the original request plus one retry (AI-spec 3.5.10)
	geminiDefaultTimeout  = 120 * time.Second
)

// GeminiConfig configures NewGemini.
//
// System, Instruction and Schema are the prompt package's System(), Instruction and Schema,
// injected for the same reason as in AnthropicConfig (prompt imports gen); the provider
// factory passes them.
type GeminiConfig struct {
	Effort  string        // LLM_EFFORT, mapped to the thinking level; empty leaves the model default
	Timeout time.Duration // per attempt; 0 means 120s (LLM_TIMEOUT_SECONDS)
	BaseURL string        // empty means the SDK default (generativelanguage.googleapis.com); tests point it at httptest

	System      string                           // the fixed system prompt
	Instruction func(GenInput) string            // the per-call text after the sources
	Schema      func(kind string) map[string]any // the JSON schema for "mcq" or "free"
	Logger      *slog.Logger                     // nil means slog.Default()
	RetryDelay  time.Duration                    // initial wait before the retry; 0 means the SDK's 1s (tests shorten it)
}

type geminiGen struct {
	client      *genai.Client
	thinking    *genai.ThinkingConfig
	timeout     time.Duration
	system      string
	instruction func(GenInput) string
	schema      func(kind string) map[string]any
	log         *slog.Logger
}

// NewGemini returns a Generator (also a TokenCounter) for the Gemini models. The SDK retries
// once (408, 429, 5xx and transport errors, including this adapter's own per-attempt timeout)
// and this adapter adds no retry layer of its own. An empty apiKey is an error: the SDK would
// otherwise fall back to the GOOGLE_API_KEY and GEMINI_API_KEY environment variables.
func NewGemini(apiKey string, cfg GeminiConfig) (Generator, error) {
	if apiKey == "" {
		return nil, errors.New("gemini: no API key")
	}
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = geminiDefaultTimeout
	}
	retry := &genai.HTTPRetryOptions{Attempts: genai.Ptr(int32(geminiMaxAttempts))}
	if cfg.RetryDelay > 0 {
		retry.InitialDelay = genai.Ptr(cfg.RetryDelay.Seconds())
		retry.MaxDelay = genai.Ptr(cfg.RetryDelay.Seconds())
	}
	client, err := genai.NewClient(context.Background(), &genai.ClientConfig{
		APIKey:      apiKey,
		Backend:     genai.BackendGeminiAPI,
		HTTPClient:  &http.Client{Timeout: timeout},
		HTTPOptions: genai.HTTPOptions{BaseURL: cfg.BaseURL, RetryOptions: retry},
	})
	if err != nil {
		return nil, fmt.Errorf("gemini: create client: %w", err)
	}
	log := cfg.Logger
	if log == nil {
		log = slog.Default()
	}
	return &geminiGen{
		client: client, thinking: thinkingFor(cfg.Effort), timeout: timeout,
		system: cfg.System, instruction: cfg.Instruction, schema: cfg.Schema, log: log,
	}, nil
}

// thinkingFor maps LLM_EFFORT onto the thinking level. Empty (or anything else) sets nothing,
// so the model's default applies. Thoughts are billed as output but not returned.
func thinkingFor(effort string) *genai.ThinkingConfig {
	switch effort {
	case "low":
		return &genai.ThinkingConfig{ThinkingLevel: genai.ThinkingLevelLow}
	case "medium":
		return &genai.ThinkingConfig{ThinkingLevel: genai.ThinkingLevelMedium}
	case "high":
		return &genai.ThinkingConfig{ThinkingLevel: genai.ThinkingLevelHigh}
	}
	return nil
}

// build lays the user message out as AI-spec 5.4: for each source a "Source doc-N:" text part
// and the inline part, then the instruction. There is no cache breakpoint (Gemini has none
// without explicit caching). Nothing but the label identifies a source (AI-spec 9.2).
func (g *geminiGen) build(in GenInput) (parts []*genai.Part, schema map[string]any, err error) {
	if g.schema == nil || g.instruction == nil {
		return nil, nil, errors.New("gemini: generator has no schema or instruction function")
	}
	schema = g.schema(in.Type)
	if schema == nil {
		return nil, nil, fmt.Errorf("gemini: unknown question type %q", in.Type)
	}
	parts = make([]*genai.Part, 0, 2*len(in.Sources)+1)
	for _, s := range in.Sources {
		var p *genai.Part
		switch s.ContentType {
		case "application/pdf", "image/jpeg", "image/png":
			p = genai.NewPartFromBytes(s.Bytes, s.ContentType)
		case "text/plain":
			p = genai.NewPartFromText(string(s.Bytes))
		default:
			return nil, nil, fmt.Errorf("gemini: source %s has unsupported content type %q", s.Label, s.ContentType)
		}
		parts = append(parts, genai.NewPartFromText("Source "+s.Label+":"), p)
	}
	parts = append(parts, genai.NewPartFromText(g.instruction(in)))
	return parts, schema, nil
}

// Generate makes one generateContent call and maps the result onto GenOutput and the error
// contract of generator.go. Usage is returned with every error for which the provider
// reported it (invalid output, max tokens, refusal).
func (g *geminiGen) Generate(ctx context.Context, in GenInput) (GenOutput, error) {
	parts, schema, err := g.build(in)
	if err != nil {
		return GenOutput{}, err
	}
	cfg := &genai.GenerateContentConfig{
		SystemInstruction:  &genai.Content{Parts: []*genai.Part{genai.NewPartFromText(g.system)}},
		MaxOutputTokens:    geminiMaxOutputTokens,
		ResponseMIMEType:   "application/json",
		ResponseJsonSchema: schema,
		ThinkingConfig:     g.thinking,
	}
	resp, err := g.client.Models.GenerateContent(ctx, in.Model, []*genai.Content{genai.NewContentFromParts(parts, genai.RoleUser)}, cfg)
	if err != nil {
		return GenOutput{}, g.mapError(ctx, err)
	}
	out := GenOutput{RequestID: resp.ResponseID, Usage: geminiUsage(resp.UsageMetadata)}

	var cand *genai.Candidate
	if len(resp.Candidates) > 0 {
		cand = resp.Candidates[0]
	}
	var block genai.BlockedReason
	if resp.PromptFeedback != nil {
		block = resp.PromptFeedback.BlockReason
	}
	var finish genai.FinishReason
	if cand != nil {
		finish = cand.FinishReason
	}
	out.StopReason = string(finish)

	var text strings.Builder
	if cand != nil && cand.Content != nil {
		for _, p := range cand.Content.Parts {
			if p != nil && !p.Thought {
				text.WriteString(p.Text)
			}
		}
	}

	refusal := ""
	switch {
	case block != "" && block != genai.BlockedReasonUnspecified:
		refusal = "prompt blocked"
	case cand == nil:
		refusal = "no candidate"
	case geminiBlockFinish(finish):
		refusal = "response blocked"
	case finish == genai.FinishReasonMaxTokens:
		return withStop(out, StopMaxTokens), fmt.Errorf("gemini: finish reason MAX_TOKENS (request %s): %w", resp.ResponseID, ErrMaxTokens)
	case strings.TrimSpace(text.String()) == "":
		refusal = "empty candidate"
	}
	if refusal != "" {
		out.StopReason = StopRefusal
		// AI-spec 9.4: the reasons and ids only, never the text.
		g.log.Warn("gemini: the model produced no usable answer", "reason", refusal,
			"finish_reason", string(finish), "block_reason", string(block), "model", in.Model, "request_id", resp.ResponseID)
		return out, fmt.Errorf("gemini: %s (finish %q, block %q, request %s): %w", refusal, finish, block, resp.ResponseID, ErrRefusal)
	}

	qs, unreadable, err := parseAnthropicOutput(text.String())
	out.Questions, out.UnreadableSources = qs, unreadable
	if err != nil {
		return out, fmt.Errorf("gemini: request %s: %w", resp.ResponseID, err)
	}
	return out, nil
}

func withStop(o GenOutput, reason string) GenOutput {
	o.StopReason = reason
	return o
}

// geminiBlockFinish reports the finish reasons that mean a safety or content filter stopped
// the answer (AI-spec 3.5.7).
func geminiBlockFinish(r genai.FinishReason) bool {
	switch r {
	case genai.FinishReasonSafety, genai.FinishReasonRecitation, genai.FinishReasonBlocklist,
		genai.FinishReasonProhibitedContent, genai.FinishReasonSPII, genai.FinishReasonImageSafety,
		genai.FinishReasonImageProhibitedContent, genai.FinishReasonImageRecitation:
		return true
	}
	return false
}

// geminiUsage converts the response usage. PromptTokenCount includes the cached tokens, so
// Input is the uncached part and CacheRead the cached part (CostMicros charges them
// separately). Output is the candidates plus the thinking tokens, which are billed as output.
func geminiUsage(u *genai.GenerateContentResponseUsageMetadata) Usage {
	if u == nil {
		return Usage{}
	}
	cached := int64(max(u.CachedContentTokenCount, 0))
	return Usage{
		Input:     max(int64(u.PromptTokenCount)-cached, 0),
		Output:    int64(max(u.CandidatesTokenCount, 0)) + int64(max(u.ThoughtsTokenCount, 0)),
		CacheRead: cached,
	}
}

// CountTokens returns the input tokens of the shared prefix plus the instruction of in, using
// the countTokens endpoint. The Developer API does not accept a system instruction there, so
// the system prompt is counted as the first text part of the user content.
func (g *geminiGen) CountTokens(ctx context.Context, in GenInput) (int, error) {
	parts, _, err := g.build(in)
	if err != nil {
		return 0, err
	}
	parts = append([]*genai.Part{genai.NewPartFromText(g.system)}, parts...)
	res, err := g.client.Models.CountTokens(ctx, in.Model, []*genai.Content{genai.NewContentFromParts(parts, genai.RoleUser)}, nil)
	if err != nil {
		return 0, g.mapError(ctx, err)
	}
	return int(res.TotalTokens), nil
}

// mapError turns an SDK error (after the SDK's own retry) into the error contract. It never
// wraps an APIError: its text carries the provider's whole response body. For a client error
// that maps to no sentinel it adds the provider's own error message, which names the offending
// parameter and never echoes the request content. A transport error is wrapped as it is.
func (g *geminiGen) mapError(ctx context.Context, err error) error {
	if ctxErr := ctx.Err(); ctxErr != nil {
		return fmt.Errorf("gemini: call stopped: %w", ctxErr)
	}
	var apiErr genai.APIError
	var apiPtr *genai.APIError
	switch {
	case errors.As(err, &apiErr):
	case errors.As(err, &apiPtr) && apiPtr != nil:
		apiErr = *apiPtr
	default:
		var ne net.Error
		if errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &ne) && ne.Timeout()) {
			return fmt.Errorf("gemini: no response within %s: %w", g.timeout, ErrTimeout)
		}
		return fmt.Errorf("gemini: transport error: %w", err)
	}
	desc := fmt.Sprintf("gemini: HTTP %d (%s)", apiErr.Code, apiErr.Status)
	switch {
	case apiErr.Code == http.StatusUnauthorized || apiErr.Code == http.StatusForbidden || invalidKey(apiErr):
		return fmt.Errorf("%s: %w", desc, ErrAuth)
	case apiErr.Code == http.StatusTooManyRequests || apiErr.Code >= http.StatusInternalServerError:
		return fmt.Errorf("%s: %w", desc, ErrRateLimited)
	case apiErr.Code == http.StatusRequestTimeout:
		return fmt.Errorf("%s: %w", desc, ErrTimeout)
	}
	if msg := cut(apiErr.Message); msg != "" {
		g.log.Error("gemini: request rejected", "status", apiErr.Code, "grpc_status", apiErr.Status, "provider_message", msg)
		desc += ": " + msg
	}
	return errors.New(desc)
}

// invalidKey is true for the 400 the Gemini API answers to an unknown key (reason
// API_KEY_INVALID, message "API key not valid"): a deployment error, like a 401.
func invalidKey(e genai.APIError) bool {
	if e.Code != http.StatusBadRequest {
		return false
	}
	if strings.Contains(e.Message, "API key not valid") {
		return true
	}
	for _, d := range e.Details {
		if d["reason"] == "API_KEY_INVALID" {
			return true
		}
	}
	return false
}

// cut puts s on a single line and cuts it to maxProviderMessage runes.
func cut(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > maxProviderMessage {
		s = string(r[:maxProviderMessage])
	}
	return s
}
