package gen

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
)

// The Anthropic adapter of AI-spec 3.3, 5.4 and 9.4 (B-2.8). It talks to the Messages API
// only through the official SDK (anthropic-sdk-go v1.82.0) and implements Generator and
// TokenCounter. It holds no mutable state after construction, so up to three Generate calls
// may run at once (every call builds its own params and response variables).
//
// Verified in the SDK source before coding (AI-spec 3.4):
//
//  1. structured output: MessageNewParams.OutputConfig.Format (JSONOutputFormatParam, type
//     json_schema) and OutputConfig.Effort exist;
//  2. images: NewImageBlockBase64 (JPEG and PNG), and documents: NewDocumentBlock with
//     Base64PDFSourceParam;
//  3. count_tokens: MessageService.CountTokens takes system, messages and output_config
//     (documents and images included);
//  4. server-side refusal fallbacks exist only in the beta namespace (client.Beta.Messages,
//     the server-side-fallback beta header, BetaMessageNewParams.Fallbacks), not on
//     MessageNewParams. They are not enabled here: that would move the whole call onto the
//     beta types and a platform-gated beta. A refusal is mapped to ErrRefusal, logged with
//     its category, and never retried by this adapter (AI-spec 8).

const (
	anthropicMaxTokens      = 16000
	anthropicMaxRetries     = 1
	anthropicDefaultTimeout = 120 * time.Second
)

// AnthropicConfig configures NewAnthropic.
//
// System, Instruction and Schema are the prompt package's System(), Instruction and Schema.
// They are injected because prompt imports gen, so gen cannot import prompt (B-2.10 passes
// them when it wires the factory (B-2.9).
type AnthropicConfig struct {
	Effort  string        // output_config.effort; empty sends none (LLM_EFFORT)
	Timeout time.Duration // per attempt; 0 means 120s (LLM_TIMEOUT_SECONDS)
	BaseURL string        // empty means the SDK default (api.anthropic.com); tests point it at httptest

	System      string                           // the fixed system prompt
	Instruction func(GenInput) string            // the per-call text after the sources
	Schema      func(kind string) map[string]any // the JSON schema for "mcq" or "free"
	Logger      *slog.Logger                     // nil means slog.Default()
}

type anthropicGen struct {
	client      anthropic.Client
	effort      string
	timeout     time.Duration
	system      string
	instruction func(GenInput) string
	schema      func(kind string) map[string]any
	log         *slog.Logger
}

// NewAnthropic returns a Generator (also a TokenCounter) for the Claude models. The SDK
// retries once (408, 409, 429, 5xx, connection errors, and its own per-attempt timeout) and
// this adapter adds no retry layer of its own.
func NewAnthropic(apiKey string, cfg AnthropicConfig) Generator {
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = anthropicDefaultTimeout
	}
	opts := []option.RequestOption{
		option.WithAPIKey(apiKey),
		option.WithMaxRetries(anthropicMaxRetries),
		option.WithRequestTimeout(timeout),
	}
	if cfg.BaseURL != "" {
		opts = append(opts, option.WithBaseURL(cfg.BaseURL))
	}
	log := cfg.Logger
	if log == nil {
		log = slog.Default()
	}
	return &anthropicGen{
		client: anthropic.NewClient(opts...), effort: cfg.Effort, timeout: timeout,
		system: cfg.System, instruction: cfg.Instruction, schema: cfg.Schema, log: log,
	}
}

// request is the part of a call shared by Generate and CountTokens.
type anthropicRequest struct {
	system   []anthropic.TextBlockParam
	messages []anthropic.MessageParam
	schema   map[string]any
}

// build lays the request out as AI-spec 5.4: the system prompt, then for each source a
// "Source doc-N:" text part and the block, a cache breakpoint on the last source block only,
// then the instruction. Nothing but the label identifies a source (AI-spec 9.2): no title,
// context, file name or metadata is ever set.
func (a *anthropicGen) build(in GenInput) (anthropicRequest, error) {
	if a.schema == nil || a.instruction == nil {
		return anthropicRequest{}, errors.New("anthropic: generator has no schema or instruction function")
	}
	schema := a.schema(in.Type)
	if schema == nil {
		return anthropicRequest{}, fmt.Errorf("anthropic: unknown question type %q", in.Type)
	}
	parts := make([]anthropic.ContentBlockParamUnion, 0, 2*len(in.Sources)+1)
	for i, s := range in.Sources {
		var block anthropic.ContentBlockParamUnion
		switch s.ContentType {
		case "application/pdf":
			block = anthropic.NewDocumentBlock(anthropic.Base64PDFSourceParam{Data: base64.StdEncoding.EncodeToString(s.Bytes)})
		case "image/jpeg", "image/png":
			block = anthropic.NewImageBlockBase64(s.ContentType, base64.StdEncoding.EncodeToString(s.Bytes))
		case "text/plain":
			block = anthropic.NewTextBlock(string(s.Bytes))
		default:
			return anthropicRequest{}, fmt.Errorf("anthropic: source %s has unsupported content type %q", s.Label, s.ContentType)
		}
		if i == len(in.Sources)-1 {
			setBreakpoint(&block)
		}
		parts = append(parts, anthropic.NewTextBlock("Source "+s.Label+":"), block)
	}
	parts = append(parts, anthropic.NewTextBlock(a.instruction(in)))
	return anthropicRequest{
		system:   []anthropic.TextBlockParam{{Text: a.system}},
		messages: []anthropic.MessageParam{anthropic.NewUserMessage(parts...)},
		schema:   stripItemBounds(schema),
	}, nil
}

// effortFor is the configured effort for models that accept it. Haiku 4.5 rejects
// output_config.effort with a 400 and an unknown model is not assumed to take it, so for
// those the field is left out (the structured-output format is still sent).
func (a *anthropicGen) effortFor(model string) anthropic.OutputConfigEffort {
	if m, ok := LookupModel(model); ok && m.SupportsEffort {
		return anthropic.OutputConfigEffort(a.effort)
	}
	return ""
}

// setBreakpoint puts the 5-minute cache breakpoint on block.
func setBreakpoint(b *anthropic.ContentBlockParamUnion) {
	cc := anthropic.NewCacheControlEphemeralParam()
	switch {
	case b.OfDocument != nil:
		b.OfDocument.CacheControl = cc
	case b.OfImage != nil:
		b.OfImage.CacheControl = cc
	case b.OfText != nil:
		b.OfText.CacheControl = cc
	}
}

// stripItemBounds returns a deep copy of a JSON schema without minItems and maxItems. The
// API accepts minItems of only 0 or 1 and has no maxItems in its supported subset (the SDK's
// own schema transform drops them too), so "exactly four options" is checked by Validate.
func stripItemBounds(v any) map[string]any {
	m, _ := stripBounds(v).(map[string]any)
	return m
}

func stripBounds(v any) any {
	switch x := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, c := range x {
			if k == "minItems" || k == "maxItems" {
				continue
			}
			out[k] = stripBounds(c)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, c := range x {
			out[i] = stripBounds(c)
		}
		return out
	default:
		return v
	}
}

// Generate makes one Messages call and maps the result onto GenOutput and the error
// contract of generator.go. Usage is returned with every error for which the provider
// reported it (invalid output, max_tokens, refusal).
func (a *anthropicGen) Generate(ctx context.Context, in GenInput) (GenOutput, error) {
	req, err := a.build(in)
	if err != nil {
		return GenOutput{}, err
	}
	params := anthropic.MessageNewParams{
		Model:     anthropic.Model(in.Model),
		MaxTokens: anthropicMaxTokens,
		System:    req.system,
		Messages:  req.messages,
		OutputConfig: anthropic.OutputConfigParam{
			Format: anthropic.JSONOutputFormatParam{Schema: req.schema},
			Effort: a.effortFor(in.Model),
		},
	}
	var httpRes *http.Response
	msg, err := a.client.Messages.New(ctx, params, option.WithResponseInto(&httpRes))
	if err != nil {
		return GenOutput{}, a.mapError(ctx, err)
	}
	requestID := msg.ID
	if httpRes != nil {
		if id := httpRes.Header.Get("request-id"); id != "" {
			requestID = id
		}
	}
	out := GenOutput{
		StopReason: string(msg.StopReason),
		RequestID:  requestID,
		Usage: Usage{
			Input: msg.Usage.InputTokens, Output: msg.Usage.OutputTokens,
			CacheRead: msg.Usage.CacheReadInputTokens, CacheWrite: msg.Usage.CacheCreationInputTokens,
		},
	}
	switch msg.StopReason {
	case anthropic.StopReasonRefusal:
		category := string(msg.StopDetails.Category)
		if category == "" {
			category = "unspecified"
		}
		// AI-spec 9.4: the category and ids only, never the text or the explanation.
		a.log.Warn("anthropic: the model refused the request", "category", category, "model", in.Model, "request_id", requestID)
		return out, fmt.Errorf("anthropic: stop reason refusal (category %s, request %s): %w", category, requestID, ErrRefusal)
	case anthropic.StopReasonMaxTokens, anthropic.StopReasonModelContextWindowExceeded:
		return out, fmt.Errorf("anthropic: stop reason max_tokens (request %s): %w", requestID, ErrMaxTokens)
	}
	var text strings.Builder
	for _, c := range msg.Content {
		if c.Type == "text" {
			text.WriteString(c.Text)
		}
	}
	qs, unreadable, err := parseAnthropicOutput(text.String())
	out.Questions, out.UnreadableSources = qs, unreadable
	if err != nil {
		return out, fmt.Errorf("anthropic: request %s: %w", requestID, err)
	}
	return out, nil
}

// parseAnthropicOutput decodes the model's JSON. Questions that decode are returned even when
// others do not; the error then wraps ErrInvalidOutput. The model's text is never put in the
// error.
func parseAnthropicOutput(text string) ([]RawQuestion, []string, error) {
	dec := json.NewDecoder(strings.NewReader(text))
	var top struct {
		Questions         *[]json.RawMessage `json:"questions"`
		UnreadableSources []string           `json:"unreadableSources"`
	}
	if err := dec.Decode(&top); err != nil || top.Questions == nil {
		return nil, nil, fmt.Errorf("response is not the expected JSON object: %w", ErrInvalidOutput)
	}
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return nil, nil, fmt.Errorf("response has text after the JSON object: %w", ErrInvalidOutput)
	}
	var qs []RawQuestion
	bad := 0
	for _, raw := range *top.Questions {
		var q RawQuestion
		if err := json.Unmarshal(raw, &q); err != nil {
			bad++
			continue
		}
		qs = append(qs, q)
	}
	if bad > 0 {
		return qs, top.UnreadableSources, fmt.Errorf("%d of %d questions do not match the schema: %w", bad, len(*top.Questions), ErrInvalidOutput)
	}
	return qs, top.UnreadableSources, nil
}

// CountTokens returns the input tokens of the shared prefix plus the instruction of in, using
// the count_tokens endpoint (documents and images included). It sends no max_tokens, sampling
// or thinking setting.
func (a *anthropicGen) CountTokens(ctx context.Context, in GenInput) (int, error) {
	req, err := a.build(in)
	if err != nil {
		return 0, err
	}
	res, err := a.client.Messages.CountTokens(ctx, anthropic.MessageCountTokensParams{
		Model:        anthropic.Model(in.Model),
		System:       anthropic.MessageCountTokensParamsSystemUnion{OfTextBlockArray: req.system},
		Messages:     req.messages,
		OutputConfig: anthropic.OutputConfigParam{Format: anthropic.JSONOutputFormatParam{Schema: req.schema}},
	})
	if err != nil {
		return 0, a.mapError(ctx, err)
	}
	return int(res.InputTokens), nil
}

// mapError turns an SDK error (after the SDK's own retry) into the error contract. It never
// wraps an API error: its text carries the request URL and the provider's whole response
// body. For a client error that maps to no sentinel (a 400, for example) it adds the
// provider's own error message, which names the offending parameter and never echoes the
// request content, so a configuration mistake can be diagnosed from the logs. A transport
// error is wrapped as it is (it can name the request URL, never a key or content).
func (a *anthropicGen) mapError(ctx context.Context, err error) error {
	if ctxErr := ctx.Err(); ctxErr != nil {
		return fmt.Errorf("anthropic: call stopped: %w", ctxErr)
	}
	var apiErr *anthropic.Error
	if errors.As(err, &apiErr) {
		desc := fmt.Sprintf("anthropic: HTTP %d (%s, request %s)", apiErr.StatusCode, apiErr.Type(), apiErr.RequestID)
		switch {
		case apiErr.StatusCode == http.StatusUnauthorized || apiErr.StatusCode == http.StatusForbidden:
			return fmt.Errorf("%s: %w", desc, ErrAuth)
		case apiErr.StatusCode == http.StatusTooManyRequests || apiErr.StatusCode >= http.StatusInternalServerError:
			return fmt.Errorf("%s: %w", desc, ErrRateLimited)
		case apiErr.StatusCode == http.StatusRequestTimeout:
			return fmt.Errorf("%s: %w", desc, ErrTimeout)
		}
		if msg := providerMessage(apiErr); msg != "" {
			a.log.Error("anthropic: request rejected", "status", apiErr.StatusCode, "type", apiErr.Type(), "request_id", apiErr.RequestID, "provider_message", msg)
			desc += ": " + msg
		}
		return errors.New(desc)
	}
	var ne net.Error
	if errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &ne) && ne.Timeout()) {
		return fmt.Errorf("anthropic: no response within %s: %w", a.timeout, ErrTimeout)
	}
	return fmt.Errorf("anthropic: transport error: %w", err)
}

// maxProviderMessage bounds the provider message kept in an error or log line.
const maxProviderMessage = 300

// providerMessage is error.message of the provider's error envelope on a single line, cut to
// maxProviderMessage runes, or "" when there is none.
func providerMessage(e *anthropic.Error) string {
	var env struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal([]byte(e.RawJSON()), &env) != nil {
		return ""
	}
	msg := strings.Join(strings.Fields(env.Error.Message), " ")
	if r := []rune(msg); len(r) > maxProviderMessage {
		msg = string(r[:maxProviderMessage])
	}
	return msg
}
