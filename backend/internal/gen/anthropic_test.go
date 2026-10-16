package gen

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// The adapter is tested against an httptest server the SDK is pointed at: no network, no key.
// gen cannot import prompt (prompt imports gen), so the tests pass stand-ins for the system
// prompt, the instruction and the schema, exactly as B-2.10 passes the real ones.

const testSystem = "SYSTEM PROMPT FIXED"

func testInstruction(in GenInput) string {
	return "INSTRUCTION " + in.Type + " " + strings.Repeat("x", in.Count) + " avoid=" + strings.Join(in.Avoid, "|")
}

func testSchema(kind string) map[string]any {
	if kind != "mcq" && kind != "free" {
		return nil
	}
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"questions": map[string]any{"type": "array", "items": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"text":    map[string]any{"type": "string"},
					"options": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "minItems": 4, "maxItems": 4},
				},
				"required":             []any{"text"},
				"additionalProperties": false,
			}},
			"unreadableSources": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "maxItems": 10},
		},
		"required":             []any{"questions", "unreadableSources"},
		"additionalProperties": false,
	}
}

type recordedRequest struct {
	Path   string
	Header http.Header
	Raw    []byte
	Body   map[string]any
}

// fakeAPI records every request and answers with respond (default: a valid MCQ message).
type fakeAPI struct {
	srv     *httptest.Server
	mu      sync.Mutex
	reqs    []recordedRequest
	calls   atomic.Int64
	respond func(n int, w http.ResponseWriter, r *http.Request)
}

func newFakeAPI(t *testing.T, respond func(n int, w http.ResponseWriter, r *http.Request)) *fakeAPI {
	t.Helper()
	f := &fakeAPI{respond: respond}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(raw, &body)
		f.mu.Lock()
		f.reqs = append(f.reqs, recordedRequest{Path: r.URL.Path, Header: r.Header.Clone(), Raw: raw, Body: body})
		f.mu.Unlock()
		n := int(f.calls.Add(1))
		if f.respond == nil {
			writeMessage(w, messageJSON{Text: validMCQJSON(1), Stop: "end_turn", In: 100, Out: 50})
			return
		}
		f.respond(n, w, r)
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeAPI) requests() []recordedRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]recordedRequest(nil), f.reqs...)
}

func (f *fakeAPI) last(t *testing.T) recordedRequest {
	t.Helper()
	rs := f.requests()
	if len(rs) == 0 {
		t.Fatal("no request reached the server")
	}
	return rs[len(rs)-1]
}

type messageJSON struct {
	Text                          string
	Stop                          string
	Category                      string
	In, Out, CacheRead, CacheWrit int64
}

func writeMessage(w http.ResponseWriter, m messageJSON) {
	resp := map[string]any{
		"id": "msg_1", "type": "message", "role": "assistant", "model": "claude-sonnet-5-5",
		"content":       []any{map[string]any{"type": "text", "text": m.Text}},
		"stop_reason":   m.Stop,
		"stop_sequence": nil,
		"usage": map[string]any{
			"input_tokens": m.In, "output_tokens": m.Out,
			"cache_creation_input_tokens": m.CacheWrit, "cache_read_input_tokens": m.CacheRead,
		},
	}
	if m.Category != "" {
		resp["stop_details"] = map[string]any{"type": "refusal", "category": m.Category, "explanation": "SECRET EXPLANATION"}
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("request-id", "req_test123")
	_ = json.NewEncoder(w).Encode(resp)
}

func writeAPIError(w http.ResponseWriter, status int, typ, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("retry-after-ms", "1") // keeps the SDK's one retry fast
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"type": "error", "error": map[string]any{"type": typ, "message": msg}})
}

func validMCQJSON(n int) string {
	var qs []string
	for i := 0; i < n; i++ {
		qs = append(qs, `{"marks":2,"text":"What is topic`+string(rune('A'+i))+`?","options":["a","b","c","d"],"correctIndex":1,"source":"doc-1"}`)
	}
	return `{"questions":[` + strings.Join(qs, ",") + `],"unreadableSources":["doc-2"]}`
}

func testCfg(f *fakeAPI) AnthropicConfig {
	return AnthropicConfig{
		Effort: "medium", BaseURL: f.srv.URL,
		System: testSystem, Instruction: testInstruction, Schema: testSchema,
	}
}

func newTestGen(t *testing.T, f *fakeAPI, mod func(*AnthropicConfig)) Generator {
	t.Helper()
	cfg := testCfg(f)
	if mod != nil {
		mod(&cfg)
	}
	return NewAnthropic("sk-ant-test-KEY", cfg)
}

func mcqInput() GenInput {
	return GenInput{
		Sources: []SourceFile{{Label: "doc-1", ContentType: "text/plain", Bytes: []byte("Photosynthesis makes sugar.")}},
		Type:    "mcq", Marks: 2, Count: 3, Avoid: []string{"old question"}, Model: "claude-sonnet-5-5",
	}
}

func TestRequestOmitsSamplingThinkingPrefill(t *testing.T) {
	f := newFakeAPI(t, nil)
	g := newTestGen(t, f, nil)
	if _, err := g.Generate(context.Background(), mcqInput()); err != nil {
		t.Fatal(err)
	}
	r := f.last(t)
	if r.Path != "/v1/messages" {
		t.Fatalf("path %q", r.Path)
	}
	for _, k := range []string{"temperature", "top_p", "top_k", "thinking", "tool_choice", "tools"} {
		if _, ok := r.Body[k]; ok {
			t.Errorf("request carries %q", k)
		}
	}
	msgs := r.Body["messages"].([]any)
	if len(msgs) != 1 || msgs[len(msgs)-1].(map[string]any)["role"] != "user" {
		t.Fatalf("messages = %v, want exactly one user message (no prefill)", msgs)
	}
	if r.Body["model"] != "claude-sonnet-5-5" {
		t.Errorf("model = %v", r.Body["model"])
	}
	if r.Header.Get("X-Api-Key") != "sk-ant-test-KEY" {
		t.Errorf("api key header = %q", r.Header.Get("X-Api-Key"))
	}
}

func TestEffortSetFromConfigAndOmittedWhenEmpty(t *testing.T) {
	for _, tc := range []struct {
		effort string
		want   any
	}{{"medium", "medium"}, {"high", "high"}, {"", nil}} {
		f := newFakeAPI(t, nil)
		g := newTestGen(t, f, func(c *AnthropicConfig) { c.Effort = tc.effort })
		if _, err := g.Generate(context.Background(), mcqInput()); err != nil {
			t.Fatal(err)
		}
		oc, ok := f.last(t).Body["output_config"].(map[string]any)
		if !ok {
			t.Fatalf("effort %q: no output_config (the structured-output format must still be there)", tc.effort)
		}
		if got := oc["effort"]; got != tc.want {
			t.Errorf("effort %q: output_config.effort = %v, want %v", tc.effort, got, tc.want)
		}
	}
}

func TestEffortOnlyForModelsThatSupportIt(t *testing.T) {
	for _, tc := range []struct {
		model string
		want  any
	}{{"claude-sonnet-5-5", "medium"}, {"claude-opus-5-5", "medium"}, {"claude-haiku-4-5", nil}, {"not-in-catalog", nil}} {
		f := newFakeAPI(t, nil)
		g := newTestGen(t, f, nil)
		in := mcqInput()
		in.Model = tc.model
		if _, err := g.Generate(context.Background(), in); err != nil {
			t.Fatal(err)
		}
		if _, err := g.(TokenCounter).CountTokens(context.Background(), in); err != nil {
			t.Fatal(err)
		}
		reqs := f.requests()
		oc, ok := reqs[0].Body["output_config"].(map[string]any)
		if !ok || oc["format"] == nil {
			t.Fatalf("%s: the structured-output format is missing: %v", tc.model, reqs[0].Body["output_config"])
		}
		if got := oc["effort"]; got != tc.want {
			t.Errorf("%s: effort = %v, want %v", tc.model, got, tc.want)
		}
		if cc := reqs[1].Body["output_config"].(map[string]any); cc["effort"] != nil || cc["format"] == nil {
			t.Errorf("%s: count_tokens output_config = %v", tc.model, cc)
		}
	}
}

func TestBadRequestLogsProviderMessageButNoContent(t *testing.T) {
	f := newFakeAPI(t, func(n int, w http.ResponseWriter, r *http.Request) {
		writeAPIError(w, 400, "invalid_request_error", "output_config.effort: this model does not support effort")
	})
	var logs bytes.Buffer
	g := newTestGen(t, f, func(c *AnthropicConfig) {
		c.Logger = slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	})
	in := mcqInput()
	in.Sources[0].Bytes = []byte("DOCUMENT CONTENT PHOTOSYNTHESIS")
	_, err := g.Generate(context.Background(), in)
	if err == nil {
		t.Fatal("want an error")
	}
	for _, s := range []string{err.Error(), logs.String()} {
		if !strings.Contains(s, "this model does not support effort") {
			t.Errorf("provider message missing from %q", s)
		}
		for _, leak := range []string{"DOCUMENT CONTENT", "PHOTOSYNTHESIS", "old question", "SYSTEM PROMPT", "sk-ant-test-KEY", f.srv.URL} {
			if strings.Contains(s, leak) {
				t.Errorf("%q leaks %q", s, leak)
			}
		}
	}
	if !strings.Contains(logs.String(), "level=ERROR") && !strings.Contains(logs.String(), "level=WARN") {
		t.Errorf("no log line: %q", logs.String())
	}
}

func TestMaxTokens16000(t *testing.T) {
	f := newFakeAPI(t, nil)
	g := newTestGen(t, f, nil)
	if _, err := g.Generate(context.Background(), mcqInput()); err != nil {
		t.Fatal(err)
	}
	if got := f.last(t).Body["max_tokens"]; got != float64(16000) {
		t.Fatalf("max_tokens = %v", got)
	}
}

func TestSDKMaxRetries1AndTimeout120(t *testing.T) {
	// Defaults, read from the resolved configuration.
	f := newFakeAPI(t, nil)
	a := newTestGen(t, f, func(c *AnthropicConfig) { c.Timeout = 0 }).(*anthropicGen)
	if a.timeout != 120*time.Second {
		t.Errorf("default timeout = %v, want 120s", a.timeout)
	}
	// Behaviour: a 500 then a 200 succeeds on the single retry; two 500s stop after exactly 2 requests.
	f2 := newFakeAPI(t, func(n int, w http.ResponseWriter, r *http.Request) {
		if n == 1 {
			writeAPIError(w, 500, "api_error", "boom")
			return
		}
		writeMessage(w, messageJSON{Text: validMCQJSON(1), Stop: "end_turn", In: 1, Out: 1})
	})
	if _, err := newTestGen(t, f2, nil).Generate(context.Background(), mcqInput()); err != nil {
		t.Fatalf("one retry should recover: %v", err)
	}
	if n := f2.calls.Load(); n != 2 {
		t.Errorf("requests = %d, want 2", n)
	}
	f3 := newFakeAPI(t, func(n int, w http.ResponseWriter, r *http.Request) { writeAPIError(w, 500, "api_error", "boom") })
	_, err := newTestGen(t, f3, nil).Generate(context.Background(), mcqInput())
	if !errors.Is(err, ErrRateLimited) {
		t.Fatalf("err = %v, want ErrRateLimited after the retry", err)
	}
	if n := f3.calls.Load(); n != 2 {
		t.Errorf("requests = %d, want 2 (1 try + MaxRetries 1)", n)
	}
}

func TestTimeoutMapsToErrTimeoutAfterRetry(t *testing.T) {
	f := newFakeAPI(t, func(n int, w http.ResponseWriter, r *http.Request) { <-r.Context().Done() })
	g := newTestGen(t, f, func(c *AnthropicConfig) { c.Timeout = 60 * time.Millisecond })
	_, err := g.Generate(context.Background(), mcqInput())
	if !errors.Is(err, ErrTimeout) {
		t.Fatalf("err = %v, want ErrTimeout", err)
	}
	if n := f.calls.Load(); n != 2 {
		t.Errorf("requests = %d, want 2", n)
	}
}

func TestCallerCancelIsNotMapped(t *testing.T) {
	f := newFakeAPI(t, func(n int, w http.ResponseWriter, r *http.Request) { <-r.Context().Done() })
	g := newTestGen(t, f, nil)
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(50 * time.Millisecond); cancel() }()
	_, err := g.Generate(ctx, mcqInput())
	if !errors.Is(err, context.Canceled) || errors.Is(err, ErrTimeout) || errors.Is(err, ErrRateLimited) {
		t.Fatalf("err = %v, want context.Canceled only", err)
	}
}

func TestMessageLayoutSystemThenSourcesThenBreakpointThenInstruction(t *testing.T) {
	f := newFakeAPI(t, nil)
	g := newTestGen(t, f, nil)
	in := GenInput{
		Sources: []SourceFile{
			{Label: "doc-1", ContentType: "application/pdf", Bytes: []byte("%PDF-1 one")},
			{Label: "doc-2", ContentType: "image/png", Bytes: []byte("PNGBYTES")},
			{Label: "doc-3", ContentType: "text/plain", Bytes: []byte("plain notes")},
		},
		Type: "free", Marks: 3, Count: 4, Avoid: []string{"A", "B"}, Model: "claude-opus-5-5",
	}
	if _, err := g.Generate(context.Background(), in); err != nil {
		t.Fatal(err)
	}
	body := f.last(t).Body
	sys := body["system"].([]any)
	if len(sys) != 1 || sys[0].(map[string]any)["text"] != testSystem {
		t.Fatalf("system = %v", sys)
	}
	if _, ok := sys[0].(map[string]any)["cache_control"]; ok {
		t.Error("the system block carries cache_control; only the last source block may")
	}
	content := body["messages"].([]any)[0].(map[string]any)["content"].([]any)
	if len(content) != 7 { // 3 x (label, block) + instruction
		t.Fatalf("%d content parts, want 7: %v", len(content), content)
	}
	wantTypes := []string{"text", "document", "text", "image", "text", "text", "text"}
	for i, c := range content {
		if got := c.(map[string]any)["type"]; got != wantTypes[i] {
			t.Errorf("part %d type %v, want %s", i, got, wantTypes[i])
		}
	}
	for i, label := range map[int]string{0: "Source doc-1:", 2: "Source doc-2:", 4: "Source doc-3:"} {
		if got := content[i].(map[string]any)["text"]; got != label {
			t.Errorf("part %d = %v, want %q", i, got, label)
		}
		if _, ok := content[i].(map[string]any)["cache_control"]; ok {
			t.Errorf("label part %d carries cache_control", i)
		}
	}
	for i, c := range content {
		_, has := c.(map[string]any)["cache_control"]
		if i == 5 && !has {
			t.Error("the last source block (part 5) has no cache_control")
		}
		if i != 5 && has {
			t.Errorf("part %d carries cache_control; only the last source block may", i)
		}
	}
	cc := content[5].(map[string]any)["cache_control"].(map[string]any)
	if cc["type"] != "ephemeral" {
		t.Errorf("cache_control = %v", cc)
	}
	if _, ok := cc["ttl"]; ok {
		t.Errorf("cache_control sets a ttl (%v); the default 5 minutes is wanted", cc["ttl"])
	}
	want := testInstruction(in)
	if got := content[6].(map[string]any)["text"]; got != want {
		t.Errorf("instruction = %v, want %q", got, want)
	}
}

func TestSingleSourceBreakpointOnItsBlock(t *testing.T) {
	f := newFakeAPI(t, nil)
	g := newTestGen(t, f, nil)
	if _, err := g.Generate(context.Background(), mcqInput()); err != nil {
		t.Fatal(err)
	}
	content := f.last(t).Body["messages"].([]any)[0].(map[string]any)["content"].([]any)
	if len(content) != 3 {
		t.Fatalf("parts = %d, want 3", len(content))
	}
	if _, ok := content[1].(map[string]any)["cache_control"]; !ok {
		t.Error("the only source block must carry the breakpoint")
	}
}

func TestPDFAndImageAndTextBlocks(t *testing.T) {
	f := newFakeAPI(t, nil)
	g := newTestGen(t, f, nil)
	pdf, img := []byte("%PDF-1.7 data"), []byte{0xff, 0xd8, 0xff, 0x01}
	in := GenInput{
		Sources: []SourceFile{
			{Label: "doc-1", ContentType: "application/pdf", Bytes: pdf},
			{Label: "doc-2", ContentType: "image/jpeg", Bytes: img},
			{Label: "doc-3", ContentType: "image/png", Bytes: img},
			{Label: "doc-4", ContentType: "text/plain", Bytes: []byte("hello notes")},
		},
		Type: "mcq", Marks: 1, Count: 1, Model: "claude-sonnet-5-5",
	}
	if _, err := g.Generate(context.Background(), in); err != nil {
		t.Fatal(err)
	}
	c := f.last(t).Body["messages"].([]any)[0].(map[string]any)["content"].([]any)
	doc := c[1].(map[string]any)["source"].(map[string]any)
	if doc["type"] != "base64" || doc["media_type"] != "application/pdf" || doc["data"] != base64.StdEncoding.EncodeToString(pdf) {
		t.Errorf("pdf source = %v", doc)
	}
	for idx, mt := range map[int]string{3: "image/jpeg", 5: "image/png"} {
		src := c[idx].(map[string]any)["source"].(map[string]any)
		if src["type"] != "base64" || src["media_type"] != mt || src["data"] != base64.StdEncoding.EncodeToString(img) {
			t.Errorf("part %d image source = %v", idx, src)
		}
	}
	if txt := c[7].(map[string]any); txt["type"] != "text" || txt["text"] != "hello notes" {
		t.Errorf("text block = %v", txt)
	}
}

func TestUnsupportedSourceTypeFailsBeforeAnyRequest(t *testing.T) {
	f := newFakeAPI(t, nil)
	g := newTestGen(t, f, nil)
	in := mcqInput()
	in.Sources[0].ContentType = "application/zip"
	_, err := g.Generate(context.Background(), in)
	if err == nil || errors.Is(err, ErrRateLimited) || errors.Is(err, ErrAuth) {
		t.Fatalf("err = %v", err)
	}
	if f.calls.Load() != 0 {
		t.Error("a request was sent")
	}
	if _, err := g.(TokenCounter).CountTokens(context.Background(), in); err == nil {
		t.Error("CountTokens accepted an unsupported type")
	}
}

func TestUnknownQuestionTypeFails(t *testing.T) {
	f := newFakeAPI(t, nil)
	in := mcqInput()
	in.Type = "essay"
	if _, err := newTestGen(t, f, nil).Generate(context.Background(), in); err == nil {
		t.Fatal("want an error for an unknown type")
	}
	if f.calls.Load() != 0 {
		t.Error("a request was sent")
	}
}

func TestStructuredOutputSchemaSent(t *testing.T) {
	f := newFakeAPI(t, nil)
	g := newTestGen(t, f, nil)
	if _, err := g.Generate(context.Background(), mcqInput()); err != nil {
		t.Fatal(err)
	}
	oc := f.last(t).Body["output_config"].(map[string]any)
	format := oc["format"].(map[string]any)
	if format["type"] != "json_schema" {
		t.Fatalf("format = %v", format)
	}
	schema := format["schema"].(map[string]any)
	if schema["additionalProperties"] != false {
		t.Errorf("schema = %v", schema)
	}
	props := schema["properties"].(map[string]any)
	if _, ok := props["questions"]; !ok {
		t.Errorf("schema properties = %v", props)
	}
	if got := f.last(t).Body["output_config"].(map[string]any)["effort"]; got != "medium" {
		t.Errorf("effort = %v", got)
	}
}

func TestStructuredOutputSchemaDropsItemBounds(t *testing.T) {
	// The API rejects array bounds above minItems 1 and has no maxItems (the SDK's own schema
	// transform drops them); the four-option rule is Validate's. The caller's map is not mutated.
	src := testSchema("mcq")
	f := newFakeAPI(t, nil)
	g := newTestGen(t, f, func(c *AnthropicConfig) { c.Schema = func(string) map[string]any { return src } })
	if _, err := g.Generate(context.Background(), mcqInput()); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(f.last(t).Raw, []byte("minItems")) || bytes.Contains(f.last(t).Raw, []byte("maxItems")) {
		t.Errorf("request still carries minItems/maxItems: %s", f.last(t).Raw)
	}
	opts := src["properties"].(map[string]any)["questions"].(map[string]any)["items"].(map[string]any)["properties"].(map[string]any)["options"].(map[string]any)
	if opts["minItems"] != 4 || opts["maxItems"] != 4 {
		t.Error("the caller's schema was mutated")
	}
}

func TestParsesUsageIncludingCacheTokens(t *testing.T) {
	f := newFakeAPI(t, func(n int, w http.ResponseWriter, r *http.Request) {
		writeMessage(w, messageJSON{Text: validMCQJSON(2), Stop: "end_turn", In: 120, Out: 345, CacheRead: 6000, CacheWrit: 700})
	})
	out, err := newTestGen(t, f, nil).Generate(context.Background(), mcqInput())
	if err != nil {
		t.Fatal(err)
	}
	if want := (Usage{Input: 120, Output: 345, CacheRead: 6000, CacheWrite: 700}); out.Usage != want {
		t.Errorf("usage = %+v, want %+v", out.Usage, want)
	}
	if out.StopReason != "end_turn" || out.RequestID != "req_test123" {
		t.Errorf("stop %q request %q", out.StopReason, out.RequestID)
	}
	if len(out.Questions) != 2 || out.Questions[0].Text != "What is topicA?" || len(out.Questions[0].Options) != 4 ||
		out.Questions[0].CorrectIndex == nil || *out.Questions[0].CorrectIndex != 1 || out.Questions[0].Source != "doc-1" {
		t.Errorf("questions = %+v", out.Questions)
	}
	if len(out.UnreadableSources) != 1 || out.UnreadableSources[0] != "doc-2" {
		t.Errorf("unreadable = %v", out.UnreadableSources)
	}
}

func TestSkipsThinkingBlocksAndJoinsText(t *testing.T) {
	f := newFakeAPI(t, func(n int, w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "msg_2", "type": "message", "role": "assistant", "model": "claude-sonnet-5-5",
			"content": []any{
				map[string]any{"type": "thinking", "thinking": "hmm", "signature": "sig"},
				map[string]any{"type": "text", "text": validMCQJSON(1)},
			},
			"stop_reason": "end_turn", "stop_sequence": nil,
			"usage": map[string]any{"input_tokens": 5, "output_tokens": 9, "cache_creation_input_tokens": 0, "cache_read_input_tokens": 0},
		})
	})
	out, err := newTestGen(t, f, nil).Generate(context.Background(), mcqInput())
	if err != nil || len(out.Questions) != 1 {
		t.Fatalf("out %+v err %v", out, err)
	}
}

func TestFreeQuestionsParse(t *testing.T) {
	f := newFakeAPI(t, func(n int, w http.ResponseWriter, r *http.Request) {
		writeMessage(w, messageJSON{Text: `{"questions":[{"marks":3,"text":"Explain.","modelAnswer":"P1. P2.","source":"doc-1"}],"unreadableSources":[]}`, Stop: "end_turn", In: 1, Out: 1})
	})
	in := mcqInput()
	in.Type = "free"
	out, err := newTestGen(t, f, nil).Generate(context.Background(), in)
	if err != nil || len(out.Questions) != 1 || out.Questions[0].ModelAnswer != "P1. P2." || out.Questions[0].Options != nil {
		t.Fatalf("out %+v err %v", out, err)
	}
}

func TestStopReasonRefusalMaps(t *testing.T) {
	f := newFakeAPI(t, func(n int, w http.ResponseWriter, r *http.Request) {
		writeMessage(w, messageJSON{Text: "I can't help with that SECRETTEXT", Stop: "refusal", Category: "bio", In: 900, Out: 12, CacheRead: 500})
	})
	var logs bytes.Buffer
	g := newTestGen(t, f, func(c *AnthropicConfig) {
		c.Logger = slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	})
	out, err := g.Generate(context.Background(), mcqInput())
	if !errors.Is(err, ErrRefusal) {
		t.Fatalf("err = %v, want ErrRefusal", err)
	}
	if out.StopReason != StopRefusal {
		t.Errorf("stop = %q", out.StopReason)
	}
	if want := (Usage{Input: 900, Output: 12, CacheRead: 500}); out.Usage != want {
		t.Errorf("usage on a refusal = %+v, want %+v", out.Usage, want)
	}
	if out.RequestID != "req_test123" || len(out.Questions) != 0 {
		t.Errorf("out = %+v", out)
	}
	l := logs.String()
	if !strings.Contains(l, "level=WARN") || !strings.Contains(l, "category=bio") || !strings.Contains(l, "req_test123") {
		t.Errorf("refusal log = %q, want WARN with the category and request id", l)
	}
	for _, leak := range []string{"SECRETTEXT", "SECRET EXPLANATION", "Photosynthesis", "old question"} {
		if strings.Contains(l, leak) || strings.Contains(err.Error(), leak) {
			t.Errorf("log or error leaks %q", leak)
		}
	}
}

func TestRefusalWithoutCategoryStillMaps(t *testing.T) {
	f := newFakeAPI(t, func(n int, w http.ResponseWriter, r *http.Request) {
		writeMessage(w, messageJSON{Text: "no", Stop: "refusal", In: 10, Out: 1})
	})
	out, err := newTestGen(t, f, nil).Generate(context.Background(), mcqInput())
	if !errors.Is(err, ErrRefusal) || out.Usage.Input != 10 {
		t.Fatalf("out %+v err %v", out, err)
	}
}

func TestMaxTokensStopReasonMaps(t *testing.T) {
	f := newFakeAPI(t, func(n int, w http.ResponseWriter, r *http.Request) {
		writeMessage(w, messageJSON{Text: `{"questions":[{"marks":2,"te`, Stop: "max_tokens", In: 800, Out: 16000, CacheWrit: 300})
	})
	out, err := newTestGen(t, f, nil).Generate(context.Background(), mcqInput())
	if !errors.Is(err, ErrMaxTokens) {
		t.Fatalf("err = %v, want ErrMaxTokens", err)
	}
	if out.StopReason != StopMaxTokens || len(out.Questions) != 0 {
		t.Errorf("out = %+v", out)
	}
	if want := (Usage{Input: 800, Output: 16000, CacheWrite: 300}); out.Usage != want {
		t.Errorf("usage on max_tokens = %+v, want %+v", out.Usage, want)
	}
}

func TestContextWindowStopMapsToMaxTokens(t *testing.T) {
	f := newFakeAPI(t, func(n int, w http.ResponseWriter, r *http.Request) {
		writeMessage(w, messageJSON{Text: "", Stop: "model_context_window_exceeded", In: 9, Out: 1})
	})
	out, err := newTestGen(t, f, nil).Generate(context.Background(), mcqInput())
	if !errors.Is(err, ErrMaxTokens) || out.Usage.Input != 9 {
		t.Fatalf("out %+v err %v", out, err)
	}
}

func TestInvalidOutputReturnsUsage(t *testing.T) {
	for name, text := range map[string]string{
		"not json":     "Sure! Here are your questions",
		"wrong shape":  `{"questions":"nope","unreadableSources":[]}`,
		"empty":        "",
		"trailing":     validMCQJSON(1) + " trailing words",
		"array toplvl": `[1,2]`,
	} {
		t.Run(name, func(t *testing.T) {
			f := newFakeAPI(t, func(n int, w http.ResponseWriter, r *http.Request) {
				writeMessage(w, messageJSON{Text: text, Stop: "end_turn", In: 40, Out: 20, CacheRead: 7})
			})
			out, err := newTestGen(t, f, nil).Generate(context.Background(), mcqInput())
			if !errors.Is(err, ErrInvalidOutput) {
				t.Fatalf("err = %v, want ErrInvalidOutput", err)
			}
			if want := (Usage{Input: 40, Output: 20, CacheRead: 7}); out.Usage != want {
				t.Errorf("usage = %+v, want %+v", out.Usage, want)
			}
			if out.RequestID != "req_test123" || out.StopReason != "end_turn" {
				t.Errorf("out = %+v", out)
			}
			if strings.Contains(err.Error(), "Sure!") {
				t.Errorf("error echoes model output: %v", err)
			}
		})
	}
}

func TestPartiallyValidQuestionsAreReturnedWithErrInvalidOutput(t *testing.T) {
	f := newFakeAPI(t, func(n int, w http.ResponseWriter, r *http.Request) {
		writeMessage(w, messageJSON{
			Text: `{"questions":[{"marks":2,"text":"ok","options":["a","b","c","d"],"correctIndex":0,"source":"doc-1"},{"marks":"two","text":"bad"}],"unreadableSources":[]}`,
			Stop: "end_turn", In: 3, Out: 4,
		})
	})
	out, err := newTestGen(t, f, nil).Generate(context.Background(), mcqInput())
	if !errors.Is(err, ErrInvalidOutput) || len(out.Questions) != 1 || out.Questions[0].Text != "ok" || out.Usage.Output != 4 {
		t.Fatalf("out %+v err %v", out, err)
	}
}

func TestErrorMapping(t *testing.T) {
	for _, tc := range []struct {
		status  int
		typ     string
		want    error
		retries int64
	}{
		{401, "authentication_error", ErrAuth, 1},
		{403, "permission_error", ErrAuth, 1},
		{429, "rate_limit_error", ErrRateLimited, 2},
		{500, "api_error", ErrRateLimited, 2},
		{502, "api_error", ErrRateLimited, 2},
		{529, "overloaded_error", ErrRateLimited, 2},
	} {
		f := newFakeAPI(t, func(n int, w http.ResponseWriter, r *http.Request) {
			writeAPIError(w, tc.status, tc.typ, "PROVIDER BODY sk-ant-test-KEY secret prompt")
		})
		out, err := newTestGen(t, f, nil).Generate(context.Background(), mcqInput())
		if !errors.Is(err, tc.want) {
			t.Errorf("status %d: err = %v, want %v", tc.status, err, tc.want)
		}
		if n := f.calls.Load(); n != tc.retries {
			t.Errorf("status %d: %d requests, want %d", tc.status, n, tc.retries)
		}
		if out.Usage != (Usage{}) || out.Questions != nil {
			t.Errorf("status %d: out = %+v", tc.status, out)
		}
		for _, leak := range []string{"PROVIDER BODY", "sk-ant-test-KEY", "secret prompt", f.srv.URL} {
			if err != nil && strings.Contains(err.Error(), leak) {
				t.Errorf("status %d: error leaks %q: %v", tc.status, leak, err)
			}
		}
	}
}

func TestOtherClientErrorsAreNotSentinels(t *testing.T) {
	f := newFakeAPI(t, func(n int, w http.ResponseWriter, r *http.Request) {
		writeAPIError(w, 400, "invalid_request_error", "BADBODY")
	})
	_, err := newTestGen(t, f, nil).Generate(context.Background(), mcqInput())
	if err == nil {
		t.Fatal("want an error")
	}
	for _, s := range []error{ErrAuth, ErrRateLimited, ErrTimeout, ErrRefusal, ErrMaxTokens, ErrInvalidOutput} {
		if errors.Is(err, s) {
			t.Errorf("a 400 maps to %v", s)
		}
	}
	if !strings.Contains(err.Error(), "BADBODY") || strings.Contains(err.Error(), "invalid_request_error\"") {
		t.Errorf("want the provider message and not the raw body: %v", err)
	}
	if f.calls.Load() != 1 {
		t.Errorf("a 400 was retried: %d requests", f.calls.Load())
	}
}

func TestNoFileNamesInRequest(t *testing.T) {
	f := newFakeAPI(t, nil)
	g := newTestGen(t, f, nil)
	in := GenInput{
		Sources: []SourceFile{
			{Label: "doc-1", ContentType: "application/pdf", Bytes: []byte("%PDF body")},
			{Label: "doc-2", ContentType: "image/jpeg", Bytes: []byte{1, 2, 3}},
			{Label: "doc-3", ContentType: "text/plain", Bytes: []byte("notes")},
		},
		Type: "mcq", Marks: 1, Count: 1, Model: "claude-sonnet-5-5",
	}
	if _, err := g.Generate(context.Background(), in); err != nil {
		t.Fatal(err)
	}
	if _, err := g.(TokenCounter).CountTokens(context.Background(), in); err != nil {
		t.Fatal(err)
	}
	for _, r := range f.requests() {
		var walk func(v any)
		walk = func(v any) {
			switch x := v.(type) {
			case map[string]any:
				for k, c := range x {
					switch k {
					case "title", "context", "name", "filename", "file_name", "metadata", "user_id":
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

func TestCountTokens(t *testing.T) {
	f := newFakeAPI(t, func(n int, w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"input_tokens": 4321}`))
	})
	g := newTestGen(t, f, nil)
	tc, ok := g.(TokenCounter)
	if !ok {
		t.Fatal("the Anthropic generator must implement TokenCounter")
	}
	in := GenInput{
		Sources: []SourceFile{
			{Label: "doc-1", ContentType: "application/pdf", Bytes: []byte("%PDF x")},
			{Label: "doc-2", ContentType: "image/png", Bytes: []byte("img")},
		},
		Type: "mcq", Marks: 2, Count: 5, Model: "claude-haiku-4-5",
	}
	n, err := tc.CountTokens(context.Background(), in)
	if err != nil || n != 4321 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	r := f.last(t)
	if r.Path != "/v1/messages/count_tokens" {
		t.Fatalf("path %q", r.Path)
	}
	if r.Body["model"] != "claude-haiku-4-5" {
		t.Errorf("model = %v", r.Body["model"])
	}
	for _, k := range []string{"max_tokens", "temperature", "top_p", "top_k", "thinking"} {
		if _, bad := r.Body[k]; bad {
			t.Errorf("count_tokens request carries %q", k)
		}
	}
	sys := r.Body["system"].([]any)
	if sys[0].(map[string]any)["text"] != testSystem {
		t.Errorf("system = %v", sys)
	}
	c := r.Body["messages"].([]any)[0].(map[string]any)["content"].([]any)
	if len(c) != 5 || c[1].(map[string]any)["type"] != "document" || c[3].(map[string]any)["type"] != "image" {
		t.Fatalf("content = %v", c)
	}
	if _, ok := c[3].(map[string]any)["cache_control"]; !ok {
		t.Error("the last source block lost its breakpoint in the count request")
	}
	if _, bad := c[1].(map[string]any)["cache_control"]; bad {
		t.Error("a non-last source block has cache_control")
	}
	if c[4].(map[string]any)["text"] != testInstruction(in) {
		t.Errorf("instruction = %v", c[4])
	}
	if bytes.Contains(r.Raw, []byte("maxItems")) {
		t.Error("count request carries maxItems")
	}
}

func TestCountTokensErrorMapping(t *testing.T) {
	for _, tc := range []struct {
		status int
		typ    string
		want   error
	}{{401, "authentication_error", ErrAuth}, {500, "api_error", ErrRateLimited}, {429, "rate_limit_error", ErrRateLimited}} {
		f := newFakeAPI(t, func(n int, w http.ResponseWriter, r *http.Request) { writeAPIError(w, tc.status, tc.typ, "x") })
		_, err := newTestGen(t, f, nil).(TokenCounter).CountTokens(context.Background(), mcqInput())
		if !errors.Is(err, tc.want) {
			t.Errorf("status %d: err = %v, want %v", tc.status, err, tc.want)
		}
	}
	f := newFakeAPI(t, func(n int, w http.ResponseWriter, r *http.Request) { <-r.Context().Done() })
	g := newTestGen(t, f, func(c *AnthropicConfig) { c.Timeout = 50 * time.Millisecond })
	if _, err := g.(TokenCounter).CountTokens(context.Background(), mcqInput()); !errors.Is(err, ErrTimeout) {
		t.Errorf("timeout: err = %v", err)
	}
}

func TestGenerateIsSafeForThreeConcurrentCalls(t *testing.T) {
	f := newFakeAPI(t, func(n int, w http.ResponseWriter, r *http.Request) {
		writeMessage(w, messageJSON{Text: validMCQJSON(1), Stop: "end_turn", In: 10, Out: 5})
	})
	g := newTestGen(t, f, nil)
	var wg sync.WaitGroup
	errs := make(chan error, 6)
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			in := mcqInput()
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
	// Each request carried its own instruction: no shared buffer got mixed up.
	seen := map[string]int{}
	for _, r := range f.requests() {
		c := r.Body["messages"].([]any)[0].(map[string]any)["content"].([]any)
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

func TestNewAnthropicDefaultsAndInterfaces(t *testing.T) {
	g := NewAnthropic("k", AnthropicConfig{System: "s", Instruction: testInstruction, Schema: testSchema})
	var _ Generator = g
	if _, ok := g.(TokenCounter); !ok {
		t.Fatal("not a TokenCounter")
	}
	a := g.(*anthropicGen)
	if a.timeout != 120*time.Second {
		t.Errorf("default timeout %v", a.timeout)
	}
}
