package providers

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/gokulkumar/exam-assistant/backend/internal/config"
	"github.com/gokulkumar/exam-assistant/backend/internal/gen"
	"github.com/gokulkumar/exam-assistant/backend/internal/gen/prompt"
)

// recorder is an httptest server that answers like either provider and keeps the requests.
type recorder struct {
	srv  *httptest.Server
	mu   sync.Mutex
	reqs []rec
}

type rec struct {
	Path string
	Body map[string]any
}

const questionsJSON = `{"questions":[{"marks":2,"text":"Q?","options":["a","b","c","d"],"correctIndex":1,"source":"doc-1"}],"unreadableSources":[]}`

func newRecorder(t *testing.T) *recorder {
	t.Helper()
	r := &recorder{}
	r.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		raw, _ := io.ReadAll(req.Body)
		var body map[string]any
		_ = json.Unmarshal(raw, &body)
		r.mu.Lock()
		r.reqs = append(r.reqs, rec{req.URL.Path, body})
		r.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if strings.HasPrefix(req.URL.Path, "/v1/messages") {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id": "msg_1", "type": "message", "role": "assistant", "model": "claude-sonnet-5-5",
				"content":     []any{map[string]any{"type": "text", "text": questionsJSON}},
				"stop_reason": "end_turn",
				"usage":       map[string]any{"input_tokens": 1, "output_tokens": 1},
			})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"responseId":    "r1",
			"candidates":    []any{map[string]any{"finishReason": "STOP", "content": map[string]any{"role": "model", "parts": []any{map[string]any{"text": questionsJSON}}}}},
			"usageMetadata": map[string]any{"promptTokenCount": 1, "candidatesTokenCount": 1},
		})
	}))
	t.Cleanup(r.srv.Close)
	return r
}

func (r *recorder) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.reqs)
}

func (r *recorder) last(t *testing.T) rec {
	t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.reqs) == 0 {
		t.Fatal("no request")
	}
	return r.reqs[len(r.reqs)-1]
}

func bothCfg() config.Config {
	return config.Config{
		Providers: []string{"anthropic", "gemini"}, LLMAPIKey: "sk-ant-SECRET", GeminiAPIKey: "gem-SECRET",
		LLMEffort: "high", LLMTimeoutSeconds: 30,
	}
}

func input(model string) gen.GenInput {
	return gen.GenInput{
		Sources: []gen.SourceFile{{Label: "doc-1", ContentType: "text/plain", Bytes: []byte("Cells divide.")}},
		Type:    "mcq", Marks: 2, Count: 3, Avoid: []string{"an old question"}, Model: model,
	}
}

func TestFactoryPicksProviderFromModel(t *testing.T) {
	ant, gem := newRecorder(t), newRecorder(t)
	f := newFactory(bothCfg(), overrides{AnthropicBaseURL: ant.srv.URL, GeminiBaseURL: gem.srv.URL})
	for _, tc := range []struct {
		model string
		rec   *recorder
		path  string
	}{
		{"gemini-3.1-flash-lite", gem, "/v1beta/models/gemini-3.1-flash-lite:generateContent"},
		{"claude-haiku-4-5", ant, "/v1/messages"},
		{"claude-sonnet-5-5", ant, "/v1/messages"},
	} {
		g, err := f(tc.model)
		if err != nil {
			t.Fatalf("%s: %v", tc.model, err)
		}
		before := tc.rec.count()
		out, err := g.Generate(context.Background(), input(tc.model))
		if err != nil || len(out.Questions) != 1 {
			t.Fatalf("%s: out=%+v err=%v", tc.model, out, err)
		}
		if tc.rec.count() != before+1 || tc.rec.last(t).Path != tc.path {
			t.Errorf("%s: last request %v, want path %s", tc.model, tc.rec.last(t).Path, tc.path)
		}
	}
	if ant.count() != 2 || gem.count() != 1 {
		t.Errorf("requests anthropic=%d gemini=%d, want 2 and 1", ant.count(), gem.count())
	}
}

// The factory is where gen meets the prompt package (gen cannot import it): both adapters
// must carry prompt.System(), prompt.Instruction and prompt.Schema.
func TestFactoryInjectsPromptSystemInstructionAndSchema(t *testing.T) {
	ant, gem := newRecorder(t), newRecorder(t)
	f := newFactory(bothCfg(), overrides{AnthropicBaseURL: ant.srv.URL, GeminiBaseURL: gem.srv.URL})
	in := input("claude-sonnet-5-5")
	wantInstr := prompt.Instruction(in)

	ga, err := f("claude-sonnet-5-5")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ga.Generate(context.Background(), in); err != nil {
		t.Fatal(err)
	}
	a := ant.last(t).Body
	if got := a["system"].([]any)[0].(map[string]any)["text"]; got != prompt.System() {
		t.Errorf("anthropic system = %v", got)
	}
	ac := a["messages"].([]any)[0].(map[string]any)["content"].([]any)
	if got := ac[len(ac)-1].(map[string]any)["text"]; got != wantInstr {
		t.Errorf("anthropic instruction = %v", got)
	}
	if a["output_config"].(map[string]any)["effort"] != "high" {
		t.Errorf("anthropic effort = %v", a["output_config"])
	}
	props := a["output_config"].(map[string]any)["format"].(map[string]any)["schema"].(map[string]any)["properties"].(map[string]any)
	if _, ok := props["questions"]; !ok {
		t.Errorf("anthropic schema = %v", props)
	}

	in.Model = "gemini-3.1-flash-lite"
	wantInstr = prompt.Instruction(in)
	gg, err := f("gemini-3.1-flash-lite")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := gg.Generate(context.Background(), in); err != nil {
		t.Fatal(err)
	}
	g := gem.last(t).Body
	if got := g["systemInstruction"].(map[string]any)["parts"].([]any)[0].(map[string]any)["text"]; got != prompt.System() {
		t.Errorf("gemini system = %v", got)
	}
	gp := g["contents"].([]any)[0].(map[string]any)["parts"].([]any)
	if got := gp[len(gp)-1].(map[string]any)["text"]; got != wantInstr {
		t.Errorf("gemini instruction = %v", got)
	}
	gc := g["generationConfig"].(map[string]any)
	if gc["thinkingConfig"].(map[string]any)["thinkingLevel"] != "HIGH" {
		t.Errorf("gemini thinking = %v", gc["thinkingConfig"])
	}
	gprops := gc["responseJsonSchema"].(map[string]any)["properties"].(map[string]any)
	if _, ok := gprops["questions"]; !ok {
		t.Errorf("gemini schema = %v", gprops)
	}
}

func TestFactoryErrorsWhenKeyMissing(t *testing.T) {
	for _, tc := range []struct {
		name  string
		mod   func(*config.Config)
		model string
	}{
		{"gemini key", func(c *config.Config) { c.GeminiAPIKey = "" }, "gemini-3.1-flash-lite"},
		{"anthropic key", func(c *config.Config) { c.LLMAPIKey = "" }, "claude-sonnet-5-5"},
		{"unknown model", nil, "gpt-5"},
		{"provider not enabled", func(c *config.Config) { c.Providers = []string{"anthropic"} }, "gemini-3.1-flash-lite"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := bothCfg()
			if tc.mod != nil {
				tc.mod(&cfg)
			}
			g, err := New(cfg)(tc.model)
			if err == nil || g != nil {
				t.Fatalf("g=%v err=%v, want an error", g, err)
			}
			// The worker puts only a UserError's message in Job.Error; this must be a plain
			// error so the job gets the generic message and the detail is only logged
			// (see gen.TestFactoryErrorIsLoggedAndNeverPutInJobError).
			var ue *gen.UserError
			if errors.As(err, &ue) {
				t.Errorf("factory error is a UserError: %v", err)
			}
			for _, s := range []string{"sk-ant-SECRET", "gem-SECRET"} {
				if strings.Contains(err.Error(), s) {
					t.Errorf("error leaks a key: %v", err)
				}
			}
		})
	}
}

func TestFactoryGeneratorsAreTokenCounters(t *testing.T) {
	f := New(bothCfg())
	for _, m := range []string{"gemini-3.1-flash-lite", "claude-haiku-4-5"} {
		g, err := f(m)
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := g.(gen.TokenCounter); !ok {
			t.Errorf("%s generator is not a TokenCounter", m)
		}
	}
}

// Regenerate sends the exam's question texts as Avoid: the recorded request body of each
// adapter must carry them, cleaned and quoted under the Avoid heading, and nothing else of them.
func TestAvoidReachesBothAdaptersRequestBody(t *testing.T) {
	ant, gem := newRecorder(t), newRecorder(t)
	f := newFactory(bothCfg(), overrides{AnthropicBaseURL: ant.srv.URL, GeminiBaseURL: gem.srv.URL})
	avoid := []string{"What is osmosis?", "Define\ndiffusion in cells"}
	for _, tc := range []struct {
		model string
		rec   *recorder
	}{{"claude-sonnet-5-5", ant}, {"gemini-3.1-flash-lite", gem}} {
		g, err := f(tc.model)
		if err != nil {
			t.Fatal(err)
		}
		in := input(tc.model)
		in.Avoid = avoid
		if _, err := g.Generate(context.Background(), in); err != nil {
			t.Fatal(err)
		}
		raw, _ := json.Marshal(tc.rec.last(t).Body)
		body := string(raw)
		for _, want := range []string{"Avoid list", "What is osmosis?", "Define diffusion in cells"} {
			if !strings.Contains(body, want) {
				t.Errorf("%s: request body lacks %q", tc.model, want)
			}
		}
		if strings.Contains(body, "an old question") {
			t.Errorf("%s: body carries a stale avoid text", tc.model)
		}
	}
}
