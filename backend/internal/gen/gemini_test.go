package gen

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

// The Gemini adapter is tested against an httptest server the SDK (google.golang.org/genai)
// is pointed at: no network, no key. The fake server, the stand-in system prompt,
// instruction and schema come from anthropic_test.go.

const geminiModel = "gemini-3.1-flash-lite"

type geminiResp struct {
	Text      string
	Finish    string // candidate finishReason; "" omits the field
	NoCand    bool   // no candidates at all
	NoParts   bool   // a candidate without content parts
	Block     string // promptFeedback.blockReason
	Prompt    int64
	Cand      int64
	Thoughts  int64
	Cached    int64
	ThoughtTx string // text of a thought part placed before the answer
}

func writeGemini(w http.ResponseWriter, g geminiResp) {
	resp := map[string]any{
		"responseId":   "resp_gem1",
		"modelVersion": geminiModel,
		"usageMetadata": map[string]any{
			"promptTokenCount": g.Prompt, "candidatesTokenCount": g.Cand,
			"thoughtsTokenCount": g.Thoughts, "cachedContentTokenCount": g.Cached,
			"totalTokenCount": g.Prompt + g.Cand + g.Thoughts,
		},
	}
	if g.Block != "" {
		resp["promptFeedback"] = map[string]any{"blockReason": g.Block}
	}
	if !g.NoCand {
		parts := []any{}
		if !g.NoParts {
			if g.ThoughtTx != "" {
				parts = append(parts, map[string]any{"text": g.ThoughtTx, "thought": true})
			}
			parts = append(parts, map[string]any{"text": g.Text})
		}
		cand := map[string]any{"content": map[string]any{"role": "model", "parts": parts}, "index": 0}
		if g.NoParts {
			delete(cand, "content")
		}
		if g.Finish != "" {
			cand["finishReason"] = g.Finish
		}
		resp["candidates"] = []any{cand}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

func okGemini(w http.ResponseWriter) {
	writeGemini(w, geminiResp{Text: validMCQJSON(1), Finish: "STOP", Prompt: 100, Cand: 50})
}

func writeGeminiError(w http.ResponseWriter, status int, grpc, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"code": status, "message": msg, "status": grpc}})
}

func geminiCfg(f *fakeAPI) GeminiConfig {
	return GeminiConfig{
		Effort: "medium", BaseURL: f.srv.URL, RetryDelay: time.Millisecond,
		System: testSystem, Instruction: testInstruction, Schema: testSchema,
	}
}

func newGeminiGen(t *testing.T, f *fakeAPI, mod func(*GeminiConfig)) Generator {
	t.Helper()
	cfg := geminiCfg(f)
	if mod != nil {
		mod(&cfg)
	}
	g, err := NewGemini("gem-test-KEY", cfg)
	if err != nil {
		t.Fatal(err)
	}
	return g
}

func geminiInput() GenInput {
	in := mcqInput()
	in.Model = geminiModel
	return in
}

func defaultGeminiAPI(t *testing.T) *fakeAPI {
	return newFakeAPI(t, func(n int, w http.ResponseWriter, r *http.Request) { okGemini(w) })
}

func genConfigOf(t *testing.T, r recordedRequest) map[string]any {
	t.Helper()
	gc, _ := r.Body["generationConfig"].(map[string]any)
	if gc == nil {
		t.Fatalf("request has no generationConfig: %s", r.Raw)
	}
	return gc
}

func TestSameLabelledLayoutAsClaude(t *testing.T) {
	f := defaultGeminiAPI(t)
	g := newGeminiGen(t, f, nil)
	in := GenInput{
		Sources: []SourceFile{
			{Label: "doc-1", ContentType: "application/pdf", Bytes: []byte("%PDF-1.7 body")},
			{Label: "doc-2", ContentType: "image/jpeg", Bytes: []byte{0xff, 0xd8, 1}},
			{Label: "doc-3", ContentType: "image/png", Bytes: []byte{0x89, 'P', 'N', 'G'}},
			{Label: "doc-4", ContentType: "text/plain", Bytes: []byte("plain notes")},
		},
		Type: "mcq", Marks: 2, Count: 3, Avoid: []string{"old question"}, Model: geminiModel,
	}
	if _, err := g.Generate(context.Background(), in); err != nil {
		t.Fatal(err)
	}
	r := f.last(t)
	if r.Path != "/v1beta/models/"+geminiModel+":generateContent" {
		t.Fatalf("path %q", r.Path)
	}
	if r.Header.Get("X-Goog-Api-Key") != "gem-test-KEY" {
		t.Errorf("api key header = %q", r.Header.Get("X-Goog-Api-Key"))
	}
	if strings.Contains(r.Path, "KEY") {
		t.Error("the key is in the URL")
	}
	sys := r.Body["systemInstruction"].(map[string]any)["parts"].([]any)
	if len(sys) != 1 || sys[0].(map[string]any)["text"] != testSystem {
		t.Fatalf("systemInstruction = %v", sys)
	}
	contents := r.Body["contents"].([]any)
	if len(contents) != 1 || contents[0].(map[string]any)["role"] != "user" {
		t.Fatalf("contents = %v, want exactly one user message", contents)
	}
	parts := contents[0].(map[string]any)["parts"].([]any)
	if len(parts) != 9 {
		t.Fatalf("parts = %d, want 4 labels + 4 sources + instruction: %v", len(parts), parts)
	}
	inline := func(i int, mime string, data []byte) {
		t.Helper()
		id, ok := parts[i].(map[string]any)["inlineData"].(map[string]any)
		if !ok || id["mimeType"] != mime || id["data"] != base64.StdEncoding.EncodeToString(data) {
			t.Errorf("part %d = %v, want inline %s", i, parts[i], mime)
		}
	}
	for i, label := range []string{"doc-1", "doc-2", "doc-3", "doc-4"} {
		if got := parts[2*i].(map[string]any)["text"]; got != "Source "+label+":" {
			t.Errorf("part %d = %v, want the label", 2*i, got)
		}
	}
	inline(1, "application/pdf", in.Sources[0].Bytes)
	inline(3, "image/jpeg", in.Sources[1].Bytes)
	inline(5, "image/png", in.Sources[2].Bytes)
	if parts[7].(map[string]any)["text"] != "plain notes" {
		t.Errorf("text source part = %v", parts[7])
	}
	if parts[8].(map[string]any)["text"] != testInstruction(in) {
		t.Errorf("instruction part = %v", parts[8])
	}
	for _, p := range parts {
		for k := range p.(map[string]any) {
			if k != "text" && k != "inlineData" {
				t.Errorf("a part carries %q (file name, URI or metadata)", k)
			}
		}
	}
}

func TestUnsupportedSourceTypeAndUnknownQuestionTypeFailBeforeAnyRequest(t *testing.T) {
	f := defaultGeminiAPI(t)
	g := newGeminiGen(t, f, nil)
	in := geminiInput()
	in.Sources[0].ContentType = "video/mp4"
	if _, err := g.Generate(context.Background(), in); err == nil {
		t.Error("unsupported content type accepted")
	}
	in = geminiInput()
	in.Type = "essay"
	if _, err := g.Generate(context.Background(), in); err == nil {
		t.Error("unknown question type accepted")
	}
	if _, err := g.(TokenCounter).CountTokens(context.Background(), in); err == nil {
		t.Error("CountTokens accepted an unknown question type")
	}
	if n := f.calls.Load(); n != 0 {
		t.Errorf("%d requests sent", n)
	}
}

func TestResponseSchemaAndJSONMime(t *testing.T) {
	f := defaultGeminiAPI(t)
	if _, err := newGeminiGen(t, f, nil).Generate(context.Background(), geminiInput()); err != nil {
		t.Fatal(err)
	}
	gc := genConfigOf(t, f.last(t))
	if gc["responseMimeType"] != "application/json" {
		t.Errorf("responseMimeType = %v", gc["responseMimeType"])
	}
	if _, bad := gc["responseSchema"]; bad {
		t.Error("the OpenAPI responseSchema is set next to responseJsonSchema")
	}
	got, _ := json.Marshal(gc["responseJsonSchema"])
	want, _ := json.Marshal(testSchema("mcq"))
	if string(got) != string(want) {
		t.Errorf("responseJsonSchema = %s\nwant %s", got, want)
	}
	if gc["maxOutputTokens"] != float64(32000) {
		t.Errorf("maxOutputTokens = %v", gc["maxOutputTokens"])
	}
}

func TestThinkingMappedFromEffort(t *testing.T) {
	for _, tc := range []struct {
		effort string
		want   any
	}{{"low", "LOW"}, {"medium", "MEDIUM"}, {"high", "HIGH"}, {"", nil}} {
		f := defaultGeminiAPI(t)
		g := newGeminiGen(t, f, func(c *GeminiConfig) { c.Effort = tc.effort })
		if _, err := g.Generate(context.Background(), geminiInput()); err != nil {
			t.Fatal(err)
		}
		gc := genConfigOf(t, f.last(t))
		tcfg, has := gc["thinkingConfig"].(map[string]any)
		if tc.want == nil {
			if has {
				t.Errorf("effort %q: thinkingConfig = %v, want none", tc.effort, tcfg)
			}
			continue
		}
		if !has || tcfg["thinkingLevel"] != tc.want {
			t.Errorf("effort %q: thinkingConfig = %v, want level %v", tc.effort, gc["thinkingConfig"], tc.want)
		}
		if _, bad := tcfg["thinkingBudget"]; bad {
			t.Errorf("effort %q: a thinking budget is set next to the level", tc.effort)
		}
		if tcfg["includeThoughts"] == true {
			t.Errorf("effort %q: thoughts are requested back", tc.effort)
		}
	}
}

func TestNoSamplingParams(t *testing.T) {
	f := defaultGeminiAPI(t)
	if _, err := newGeminiGen(t, f, nil).Generate(context.Background(), geminiInput()); err != nil {
		t.Fatal(err)
	}
	r := f.last(t)
	gc := genConfigOf(t, r)
	for _, k := range []string{"temperature", "topP", "topK", "candidateCount", "seed", "presencePenalty", "frequencyPenalty", "stopSequences", "responseLogprobs"} {
		if _, bad := gc[k]; bad {
			t.Errorf("generationConfig carries %q", k)
		}
	}
	for _, k := range []string{"temperature", "topP", "topK", "tools", "toolConfig", "safetySettings"} {
		if _, bad := r.Body[k]; bad {
			t.Errorf("request carries %q", k)
		}
	}
}

func TestNoExplicitCacheCreated(t *testing.T) {
	f := defaultGeminiAPI(t)
	g := newGeminiGen(t, f, nil)
	in := geminiInput()
	for i := 0; i < 3; i++ {
		if _, err := g.Generate(context.Background(), in); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := g.(TokenCounter).CountTokens(context.Background(), in); err != nil {
		t.Fatal(err)
	}
	for _, r := range f.requests() {
		if !strings.HasSuffix(r.Path, ":generateContent") && !strings.HasSuffix(r.Path, ":countTokens") {
			t.Errorf("unexpected call to %s", r.Path)
		}
		if strings.Contains(strings.ToLower(r.Path), "cachedcontents") || bytes.Contains(r.Raw, []byte("cachedContent")) || bytes.Contains(r.Raw, []byte("cache_control")) {
			t.Errorf("%s asks for explicit caching: %s", r.Path, r.Raw)
		}
	}
}

func TestUsageIncludesThinkingInOutput(t *testing.T) {
	f := newFakeAPI(t, func(n int, w http.ResponseWriter, r *http.Request) {
		writeGemini(w, geminiResp{Text: validMCQJSON(1), Finish: "STOP", Prompt: 1000, Cand: 300, Thoughts: 700})
	})
	out, err := newGeminiGen(t, f, nil).Generate(context.Background(), geminiInput())
	if err != nil {
		t.Fatal(err)
	}
	if want := (Usage{Input: 1000, Output: 1000}); out.Usage != want {
		t.Errorf("usage = %+v, want %+v (thoughts counted as output)", out.Usage, want)
	}
	if out.RequestID != "resp_gem1" || out.StopReason != "STOP" || len(out.Questions) != 1 || len(out.UnreadableSources) != 1 {
		t.Errorf("out = %+v", out)
	}
}

func TestCachedTokensBilledAtCacheReadPrice(t *testing.T) {
	f := newFakeAPI(t, func(n int, w http.ResponseWriter, r *http.Request) {
		writeGemini(w, geminiResp{Text: validMCQJSON(1), Finish: "STOP", Prompt: 1_000_000, Cand: 100_000, Thoughts: 100_000, Cached: 800_000})
	})
	out, err := newGeminiGen(t, f, nil).Generate(context.Background(), geminiInput())
	if err != nil {
		t.Fatal(err)
	}
	// promptTokenCount includes the cached tokens: Input is the uncached part only.
	if want := (Usage{Input: 200_000, Output: 200_000, CacheRead: 800_000}); out.Usage != want {
		t.Fatalf("usage = %+v, want %+v", out.Usage, want)
	}
	m, _ := LookupModel(geminiModel)
	// 200k*$0.25 + 800k*$0.025 + 200k*$1.50 per MTok = 0.05 + 0.02 + 0.30 = $0.37
	if got := CostMicros(m, out.Usage); got != 370_000 {
		t.Errorf("cost = %d micros, want 370000", got)
	}
}

func TestCachedTokensNeverExceedPrompt(t *testing.T) {
	f := newFakeAPI(t, func(n int, w http.ResponseWriter, r *http.Request) {
		writeGemini(w, geminiResp{Text: validMCQJSON(1), Finish: "STOP", Prompt: 10, Cand: 1, Cached: 50})
	})
	out, err := newGeminiGen(t, f, nil).Generate(context.Background(), geminiInput())
	if err != nil {
		t.Fatal(err)
	}
	if out.Usage.Input != 0 || out.Usage.CacheRead != 50 {
		t.Errorf("usage = %+v, want input clamped at 0", out.Usage)
	}
}

func TestSafetyBlockMapsToRefusal(t *testing.T) {
	for _, tc := range []struct {
		name string
		resp geminiResp
		log  string
	}{
		{"candidate SAFETY", geminiResp{Text: "partial SECRETTEXT", Finish: "SAFETY", Prompt: 900, Cand: 12, Cached: 500}, "finish_reason=SAFETY"},
		{"candidate PROHIBITED_CONTENT", geminiResp{NoParts: true, Finish: "PROHIBITED_CONTENT", Prompt: 900, Cand: 12, Cached: 500}, "finish_reason=PROHIBITED_CONTENT"},
		{"candidate RECITATION", geminiResp{Text: "x", Finish: "RECITATION", Prompt: 900, Cand: 12, Cached: 500}, "finish_reason=RECITATION"},
		{"candidate BLOCKLIST", geminiResp{NoParts: true, Finish: "BLOCKLIST", Prompt: 900, Cand: 12, Cached: 500}, "finish_reason=BLOCKLIST"},
		{"blocked prompt", geminiResp{NoCand: true, Block: "SAFETY", Prompt: 900, Cached: 500}, "block_reason=SAFETY"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakeAPI(t, func(n int, w http.ResponseWriter, r *http.Request) { writeGemini(w, tc.resp) })
			var logs bytes.Buffer
			g := newGeminiGen(t, f, func(c *GeminiConfig) {
				c.Logger = slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
			})
			out, err := g.Generate(context.Background(), geminiInput())
			if !errors.Is(err, ErrRefusal) {
				t.Fatalf("err = %v, want ErrRefusal", err)
			}
			if out.StopReason != StopRefusal || len(out.Questions) != 0 {
				t.Errorf("out = %+v", out)
			}
			if out.Usage.Input != 400 || out.Usage.CacheRead != 500 || out.Usage.Output != tc.resp.Cand {
				t.Errorf("usage on a refusal = %+v", out.Usage)
			}
			if out.RequestID != "resp_gem1" {
				t.Errorf("request id = %q", out.RequestID)
			}
			l := logs.String()
			if !strings.Contains(l, "level=WARN") || !strings.Contains(l, tc.log) || !strings.Contains(l, "resp_gem1") {
				t.Errorf("log = %q, want WARN with %s and the response id", l, tc.log)
			}
			for _, leak := range []string{"SECRETTEXT", "Photosynthesis", "old question", "SYSTEM PROMPT", "gem-test-KEY"} {
				if strings.Contains(l, leak) || strings.Contains(err.Error(), leak) {
					t.Errorf("log or error leaks %q", leak)
				}
			}
		})
	}
}

func TestEmptyCandidateMapsToRefusal(t *testing.T) {
	for name, g := range map[string]geminiResp{
		"no candidates":      {NoCand: true, Prompt: 100, Cand: 0},
		"no parts":           {NoParts: true, Finish: "STOP", Prompt: 100},
		"empty text":         {Text: "", Finish: "STOP", Prompt: 100},
		"only a thought":     {NoParts: false, Text: "", ThoughtTx: "thinking...", Finish: "STOP", Prompt: 100, Thoughts: 40},
		"no finish reason":   {NoParts: true, Prompt: 100},
		"OTHER and no parts": {NoParts: true, Finish: "OTHER", Prompt: 100},
	} {
		t.Run(name, func(t *testing.T) {
			f := newFakeAPI(t, func(n int, w http.ResponseWriter, r *http.Request) { writeGemini(w, g) })
			out, err := newGeminiGen(t, f, nil).Generate(context.Background(), geminiInput())
			if !errors.Is(err, ErrRefusal) {
				t.Fatalf("err = %v, want ErrRefusal", err)
			}
			if out.StopReason != StopRefusal || out.Usage.Input != 100 || out.Usage.Output != g.Thoughts {
				t.Errorf("out = %+v", out)
			}
		})
	}
}

func TestMaxTokensFinishMaps(t *testing.T) {
	f := newFakeAPI(t, func(n int, w http.ResponseWriter, r *http.Request) {
		writeGemini(w, geminiResp{Text: `{"questions":[{"mar`, Finish: "MAX_TOKENS", Prompt: 500, Cand: 100, Thoughts: 31900})
	})
	out, err := newGeminiGen(t, f, nil).Generate(context.Background(), geminiInput())
	if !errors.Is(err, ErrMaxTokens) {
		t.Fatalf("err = %v, want ErrMaxTokens", err)
	}
	if out.StopReason != StopMaxTokens || out.Usage.Output != 32000 || len(out.Questions) != 0 {
		t.Errorf("out = %+v", out)
	}
}

func TestSkipsThoughtPartsAndJoinsText(t *testing.T) {
	f := newFakeAPI(t, func(n int, w http.ResponseWriter, r *http.Request) {
		writeGemini(w, geminiResp{Text: validMCQJSON(2), ThoughtTx: "not json at all", Finish: "STOP", Prompt: 1, Cand: 1})
	})
	out, err := newGeminiGen(t, f, nil).Generate(context.Background(), geminiInput())
	if err != nil || len(out.Questions) != 2 {
		t.Fatalf("out=%+v err=%v", out, err)
	}
}

func TestGeminiInvalidOutputReturnsUsage(t *testing.T) {
	for name, text := range map[string]string{
		"not json":   "Sorry, here you go: SECRETTEXT",
		"no array":   `{"foo":1}`,
		"trailing":   validMCQJSON(1) + " trailing",
		"bad member": `{"questions":[{"marks":"two"},{"marks":2,"text":"ok","source":"doc-1"}],"unreadableSources":[]}`,
	} {
		t.Run(name, func(t *testing.T) {
			f := newFakeAPI(t, func(n int, w http.ResponseWriter, r *http.Request) {
				writeGemini(w, geminiResp{Text: text, Finish: "STOP", Prompt: 70, Cand: 8})
			})
			out, err := newGeminiGen(t, f, nil).Generate(context.Background(), geminiInput())
			if !errors.Is(err, ErrInvalidOutput) {
				t.Fatalf("err = %v, want ErrInvalidOutput", err)
			}
			if out.Usage != (Usage{Input: 70, Output: 8}) {
				t.Errorf("usage = %+v", out.Usage)
			}
			if strings.Contains(err.Error(), "SECRETTEXT") {
				t.Error("the model text is in the error")
			}
			if name == "bad member" && len(out.Questions) != 1 {
				t.Errorf("valid questions dropped: %+v", out.Questions)
			}
		})
	}
}

func TestGeminiErrorMapping(t *testing.T) {
	for _, tc := range []struct {
		status  int
		grpc    string
		msg     string
		want    error
		retried bool
	}{
		{401, "UNAUTHENTICATED", "bad credentials", ErrAuth, false},
		{403, "PERMISSION_DENIED", "denied", ErrAuth, false},
		{400, "INVALID_ARGUMENT", "API key not valid. Please pass a valid API key.", ErrAuth, false},
		{429, "RESOURCE_EXHAUSTED", "quota", ErrRateLimited, true},
		{500, "INTERNAL", "boom", ErrRateLimited, true},
		{503, "UNAVAILABLE", "The model is overloaded. Please try again later.", ErrRateLimited, true},
		{504, "DEADLINE_EXCEEDED", "deadline", ErrRateLimited, true},
		{408, "DEADLINE_EXCEEDED", "slow", ErrTimeout, true},
	} {
		f := newFakeAPI(t, func(n int, w http.ResponseWriter, r *http.Request) { writeGeminiError(w, tc.status, tc.grpc, tc.msg) })
		_, err := newGeminiGen(t, f, nil).Generate(context.Background(), geminiInput())
		if !errors.Is(err, tc.want) {
			t.Errorf("HTTP %d: err = %v, want %v", tc.status, err, tc.want)
		}
		wantCalls := int64(1)
		if tc.retried {
			wantCalls = 2
		}
		if n := f.calls.Load(); n != wantCalls {
			t.Errorf("HTTP %d: requests = %d, want %d", tc.status, n, wantCalls)
		}
		if err != nil && (strings.Contains(err.Error(), f.srv.URL) || strings.Contains(err.Error(), "gem-test-KEY")) {
			t.Errorf("HTTP %d: error leaks the URL or key: %v", tc.status, err)
		}
	}
}

func TestOneRetryThenSuccess(t *testing.T) {
	f := newFakeAPI(t, func(n int, w http.ResponseWriter, r *http.Request) {
		if n == 1 {
			writeGeminiError(w, 503, "UNAVAILABLE", "overloaded")
			return
		}
		okGemini(w)
	})
	if _, err := newGeminiGen(t, f, nil).Generate(context.Background(), geminiInput()); err != nil {
		t.Fatalf("one retry should recover: %v", err)
	}
	if n := f.calls.Load(); n != 2 {
		t.Errorf("requests = %d, want 2", n)
	}
}

func TestOtherClientErrorsAreNotSentinelsAndLogProviderMessage(t *testing.T) {
	f := newFakeAPI(t, func(n int, w http.ResponseWriter, r *http.Request) {
		writeGeminiError(w, 400, "INVALID_ARGUMENT", "thinking_level   is not\nsupported by this model")
	})
	var logs bytes.Buffer
	g := newGeminiGen(t, f, func(c *GeminiConfig) {
		c.Logger = slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	})
	in := geminiInput()
	in.Sources[0].Bytes = []byte("DOCUMENT CONTENT PHOTOSYNTHESIS")
	_, err := g.Generate(context.Background(), in)
	if err == nil {
		t.Fatal("want an error")
	}
	for _, s := range []error{ErrAuth, ErrRateLimited, ErrTimeout, ErrRefusal, ErrMaxTokens, ErrInvalidOutput} {
		if errors.Is(err, s) {
			t.Errorf("a 400 maps to %v", s)
		}
	}
	for _, s := range []string{err.Error(), logs.String()} {
		if !strings.Contains(s, "thinking_level is not supported by this model") {
			t.Errorf("provider message missing from %q", s)
		}
		for _, leak := range []string{"DOCUMENT CONTENT", "PHOTOSYNTHESIS", "old question", "SYSTEM PROMPT", "gem-test-KEY", f.srv.URL} {
			if strings.Contains(s, leak) {
				t.Errorf("%q leaks %q", s, leak)
			}
		}
	}
	if !strings.Contains(logs.String(), "level=ERROR") {
		t.Errorf("no ERROR log line: %q", logs.String())
	}
}

func TestGeminiTimeoutMapsToErrTimeoutAfterRetry(t *testing.T) {
	f := newFakeAPI(t, func(n int, w http.ResponseWriter, r *http.Request) { <-r.Context().Done() })
	g := newGeminiGen(t, f, func(c *GeminiConfig) { c.Timeout = 60 * time.Millisecond })
	_, err := g.Generate(context.Background(), geminiInput())
	if !errors.Is(err, ErrTimeout) {
		t.Fatalf("err = %v, want ErrTimeout", err)
	}
	if n := f.calls.Load(); n != 2 {
		t.Errorf("requests = %d, want 2 (one try plus one retry)", n)
	}
}

func TestGeminiCallerCancelIsNotMapped(t *testing.T) {
	f := newFakeAPI(t, func(n int, w http.ResponseWriter, r *http.Request) { <-r.Context().Done() })
	g := newGeminiGen(t, f, nil)
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(50 * time.Millisecond); cancel() }()
	_, err := g.Generate(ctx, geminiInput())
	if !errors.Is(err, context.Canceled) || errors.Is(err, ErrTimeout) || errors.Is(err, ErrRateLimited) {
		t.Fatalf("err = %v, want context.Canceled only", err)
	}
}

func TestGeminiNoFileNamesInRequest(t *testing.T) {
	f := newFakeAPI(t, func(n int, w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, ":countTokens") {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"totalTokens": 7}`))
			return
		}
		okGemini(w)
	})
	g := newGeminiGen(t, f, nil)
	in := GenInput{
		Sources: []SourceFile{
			{Label: "doc-1", ContentType: "application/pdf", Bytes: []byte("%PDF body")},
			{Label: "doc-2", ContentType: "image/jpeg", Bytes: []byte{1, 2, 3}},
			{Label: "doc-3", ContentType: "text/plain", Bytes: []byte("notes")},
		},
		Type: "mcq", Marks: 1, Count: 1, Model: geminiModel,
	}
	if _, err := g.Generate(context.Background(), in); err != nil {
		t.Fatal(err)
	}
	if _, err := g.(TokenCounter).CountTokens(context.Background(), in); err != nil {
		t.Fatal(err)
	}
	if len(f.requests()) != 2 {
		t.Fatalf("requests = %d", len(f.requests()))
	}
	for _, r := range f.requests() {
		var walk func(v any)
		walk = func(v any) {
			switch x := v.(type) {
			case map[string]any:
				for k, c := range x {
					switch k {
					case "title", "context", "name", "displayName", "filename", "fileData", "fileUri", "metadata", "labels", "user_id":
						t.Errorf("%s request carries the key %q", r.Path, k)
					}
					walk(c)
				}
			case []any:
				for _, c := range x {
					walk(c)
				}
			}
		}
		walk(r.Body)
		for _, name := range []string{".pdf", ".jpg", ".png", ".txt"} {
			if strings.Contains(string(r.Raw), name) {
				t.Errorf("%s request contains %q", r.Path, name)
			}
		}
	}
}

func TestGeminiCountTokens(t *testing.T) {
	f := newFakeAPI(t, func(n int, w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"totalTokens": 4321}`))
	})
	g := newGeminiGen(t, f, nil)
	tc, ok := g.(TokenCounter)
	if !ok {
		t.Fatal("the Gemini generator must implement TokenCounter")
	}
	in := GenInput{
		Sources: []SourceFile{
			{Label: "doc-1", ContentType: "application/pdf", Bytes: []byte("%PDF x")},
			{Label: "doc-2", ContentType: "image/png", Bytes: []byte("img")},
		},
		Type: "mcq", Marks: 2, Count: 5, Model: geminiModel,
	}
	n, err := tc.CountTokens(context.Background(), in)
	if err != nil || n != 4321 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	r := f.last(t)
	if r.Path != "/v1beta/models/"+geminiModel+":countTokens" {
		t.Fatalf("path %q", r.Path)
	}
	for _, k := range []string{"generationConfig", "systemInstruction", "tools"} {
		if _, bad := r.Body[k]; bad {
			t.Errorf("countTokens request carries %q", k)
		}
	}
	// The Developer API rejects systemInstruction here, so the system prompt leads the user parts.
	contents := r.Body["contents"].([]any)
	if len(contents) != 1 {
		t.Fatalf("contents = %v", contents)
	}
	parts := contents[0].(map[string]any)["parts"].([]any)
	if len(parts) != 6 || parts[0].(map[string]any)["text"] != testSystem ||
		parts[1].(map[string]any)["text"] != "Source doc-1:" || parts[5].(map[string]any)["text"] != testInstruction(in) {
		t.Fatalf("parts = %v", parts)
	}
	if _, ok := parts[2].(map[string]any)["inlineData"]; !ok {
		t.Errorf("pdf part = %v", parts[2])
	}
}

func TestGeminiCountTokensErrorMapping(t *testing.T) {
	for _, tc := range []struct {
		status int
		want   error
	}{{401, ErrAuth}, {403, ErrAuth}, {429, ErrRateLimited}, {503, ErrRateLimited}} {
		f := newFakeAPI(t, func(n int, w http.ResponseWriter, r *http.Request) { writeGeminiError(w, tc.status, "X", "no") })
		_, err := newGeminiGen(t, f, nil).(TokenCounter).CountTokens(context.Background(), geminiInput())
		if !errors.Is(err, tc.want) {
			t.Errorf("HTTP %d: err = %v, want %v", tc.status, err, tc.want)
		}
	}
}

func TestGeminiGenerateIsSafeForThreeConcurrentCalls(t *testing.T) {
	f := newFakeAPI(t, func(n int, w http.ResponseWriter, r *http.Request) {
		writeGemini(w, geminiResp{Text: validMCQJSON(1), Finish: "STOP", Prompt: 10, Cand: 5})
	})
	g := newGeminiGen(t, f, nil)
	var wg sync.WaitGroup
	errs := make(chan error, 6)
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			in := geminiInput()
			in.Count = i + 1
			in.Avoid = []string{"avoid-" + string(rune('a'+i))}
			in.Sources = append([]SourceFile(nil), in.Sources...)
			for rep := 0; rep < 3; rep++ {
				out, err := g.Generate(context.Background(), in)
				if err != nil || len(out.Questions) != 1 || out.Usage.Input != 10 {
					errs <- errors.New("concurrent call failed")
					return
				}
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	seen := map[string]int{}
	for _, r := range f.requests() {
		c := r.Body["contents"].([]any)[0].(map[string]any)["parts"].([]any)
		seen[c[len(c)-1].(map[string]any)["text"].(string)]++
	}
	if len(seen) != 3 {
		t.Errorf("distinct instructions = %v, want 3", seen)
	}
	for k, v := range seen {
		if v != 3 || !strings.Contains(k, "avoid-") {
			t.Errorf("instruction %q seen %d times", k, v)
		}
	}
}

func TestNewGeminiDefaultsAndInterfaces(t *testing.T) {
	g, err := NewGemini("k", GeminiConfig{System: "s", Instruction: testInstruction, Schema: testSchema})
	if err != nil {
		t.Fatal(err)
	}
	var _ Generator = g
	if _, ok := g.(TokenCounter); !ok {
		t.Fatal("not a TokenCounter")
	}
	if a := g.(*geminiGen); a.timeout != 120*time.Second {
		t.Errorf("default timeout %v", a.timeout)
	}
	if _, err := NewGemini("", GeminiConfig{}); err == nil {
		t.Error("an empty key was accepted (the SDK would fall back to the environment)")
	}
}
