package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/gokulkumar/exam-assistant/backend/internal/gen"
	"github.com/gokulkumar/exam-assistant/backend/internal/gen/prompt"
)

var typesMu sync.Mutex

const testModel = "claude-haiku-4-5"

// fakeDeps never builds a provider: every model gets the offline fake generator.
func fakeDeps() deps {
	return deps{newFactory: func(string, func(string) string) (gen.GeneratorFactory, error) {
		return gen.FixedFactory(gen.FakeGenerator{}), nil
	}}
}

func goldenDir(t *testing.T, names ...string) string {
	t.Helper()
	dir := t.TempDir()
	for _, n := range names {
		if err := os.WriteFile(filepath.Join(dir, n), []byte("Photosynthesis turns light, water and carbon dioxide into glucose and oxygen in chloroplasts."), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func env(kv ...string) func(string) string {
	m := map[string]string{}
	for i := 0; i+1 < len(kv); i += 2 {
		m[kv[i]] = kv[i+1]
	}
	return func(k string) string { return m[k] }
}

func runEval(t *testing.T, d deps, getenv func(string) string, args ...string) (code int, out, errOut string) {
	t.Helper()
	var o, e bytes.Buffer
	code = run(context.Background(), args, getenv, &o, &e, d)
	return code, o.String(), e.String()
}

func TestReportContainsPerFileRows(t *testing.T) {
	dir := goldenDir(t, "bio.txt", "chem.txt")
	code, out, errOut := runEval(t, fakeDeps(), env("AI_EVAL", "1"), "-model", testModel, "-dir", dir)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	for _, col := range []string{"file", "type", "requested", "kept after retry", "first-pass kept %", "dropped", "calls", "tokens in/out/cacheRead", "cost USD", "seconds", "refusals", "PromptVersion"} {
		if !strings.Contains(out, col) {
			t.Errorf("report lacks column %q:\n%s", col, out)
		}
	}
	for _, f := range []string{"bio.txt", "chem.txt"} {
		if !strings.Contains(out, f) {
			t.Errorf("report lacks a row for %s", f)
		}
	}
	// The fake meets every count first time: 9 requested (4+3+2 marks), all kept, 100%.
	if !strings.Contains(out, "100%") || !strings.Contains(out, " 9 ") {
		t.Errorf("expected 9 requested and 100%% first pass:\n%s", out)
	}
}

func TestRefusesToRunWithoutExplicitModelFlag(t *testing.T) {
	dir := goldenDir(t, "a.txt")
	code, _, errOut := runEval(t, fakeDeps(), env("AI_EVAL", "1"), "-dir", dir)
	if code == 0 || !strings.Contains(errOut, "-model is required") {
		t.Fatalf("want a -model error, got exit %d: %q", code, errOut)
	}
	code, _, errOut = runEval(t, fakeDeps(), env("AI_EVAL", "1"), "-model", "no-such-model", "-dir", dir)
	if code == 0 || !strings.Contains(errOut, "catalog") {
		t.Fatalf("want a catalog error, got exit %d: %q", code, errOut)
	}
}

func TestRunsEveryFileForBothTypes(t *testing.T) {
	dir := goldenDir(t, "a.txt", "b.txt", "c.txt")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("not a source"), 0o600); err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int64
	types := map[string]int{}
	d := deps{newFactory: func(string, func(string) string) (gen.GeneratorFactory, error) {
		return gen.FixedFactory(countingGen{&calls, types}), nil
	}}
	code, out, errOut := runEval(t, d, env("AI_EVAL", "1"), "-model", testModel, "-dir", dir)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	rows := 0
	for _, l := range strings.Split(out, "\n") {
		if strings.Contains(l, ".txt") && (strings.Contains(l, " mcq ") || strings.Contains(l, " free ")) {
			rows++
		}
	}
	if rows != 6 {
		t.Errorf("want 6 rows (3 files x 2 types), got %d:\n%s", rows, out)
	}
	if types["mcq"] != 9 || types["free"] != 9 { // 3 mark values x 3 files
		t.Errorf("calls by type = %v", types)
	}
	if strings.Contains(out, "README") {
		t.Error("README.md must not be treated as a source")
	}

	// -types narrows the run.
	code, out, _ = runEval(t, fakeDeps(), env("AI_EVAL", "1"), "-model", testModel, "-dir", dir, "-types", "free")
	if code != 0 || strings.Contains(out, " mcq ") || !strings.Contains(out, " free ") {
		t.Errorf("-types free: exit %d\n%s", code, out)
	}
}

type countingGen struct {
	n     *atomic.Int64
	types map[string]int
}

func (g countingGen) Generate(ctx context.Context, in gen.GenInput) (gen.GenOutput, error) {
	g.n.Add(1)
	// The worker runs up to three calls at once: serialise the map write through the counter.
	typesMu.Lock()
	g.types[in.Type]++
	typesMu.Unlock()
	return gen.FakeGenerator{}.Generate(ctx, in)
}

func TestReportIncludesPromptVersion(t *testing.T) {
	dir := goldenDir(t, "a.txt")
	code, out, errOut := runEval(t, fakeDeps(), env("AI_EVAL", "1"), "-model", testModel, "-dir", dir)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	if !strings.Contains(out, prompt.PromptVersion) {
		t.Errorf("report lacks PromptVersion %q:\n%s", prompt.PromptVersion, out)
	}
}

func TestNeverRunsUnderGoTest(t *testing.T) {
	dir := goldenDir(t, "a.txt")
	var built atomic.Bool
	d := deps{newFactory: func(string, func(string) string) (gen.GeneratorFactory, error) {
		built.Store(true)
		return gen.FixedFactory(gen.FakeGenerator{}), nil
	}}
	for _, e := range []func(string) string{env(), env("AI_EVAL", "0"), env("AI_EVAL", "")} {
		code, out, errOut := runEval(t, d, e, "-model", testModel, "-dir", dir)
		if code == 0 || !strings.Contains(errOut, "AI_EVAL=1") || out != "" || built.Load() {
			t.Fatalf("must refuse without AI_EVAL=1: exit %d out=%q err=%q built=%v", code, out, errOut, built.Load())
		}
	}
}

func TestMissingOrEmptyGoldenDirIsAClearError(t *testing.T) {
	for _, dir := range []string{filepath.Join(t.TempDir(), "nope"), goldenDir(t)} {
		code, _, errOut := runEval(t, fakeDeps(), env("AI_EVAL", "1"), "-model", testModel, "-dir", dir)
		if code == 0 || !strings.Contains(errOut, "golden") {
			t.Errorf("dir %s: exit %d err %q", dir, code, errOut)
		}
	}
}

func TestShortfallAndRefusalAreMeasured(t *testing.T) {
	dir := goldenDir(t, "a.txt")
	d := deps{newFactory: func(string, func(string) string) (gen.GeneratorFactory, error) {
		return gen.FixedFactory(shortGen{&sync.Map{}}), nil
	}}
	code, out, errOut := runEval(t, d, env("AI_EVAL", "1"), "-model", testModel, "-dir", dir, "-types", "mcq")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	// First call of each mark value is one short, the follow-up completes it: 6/9 = 67% first pass, all kept.
	if !strings.Contains(out, "67%") {
		t.Errorf("want 67%% first pass:\n%s", out)
	}
	d = deps{newFactory: func(string, func(string) string) (gen.GeneratorFactory, error) {
		return gen.FixedFactory(refusingGen{}), nil
	}}
	code, out, _ = runEval(t, d, env("AI_EVAL", "1"), "-model", testModel, "-dir", dir, "-types", "mcq")
	if code != 0 || !strings.Contains(out, "refused") {
		t.Errorf("a refusal is reported, not fatal: exit %d\n%s", code, out)
	}
}

// shortGen returns one question too few on the first call of each mark value.
type shortGen struct{ seen *sync.Map }

func (g shortGen) Generate(ctx context.Context, in gen.GenInput) (gen.GenOutput, error) {
	if _, again := g.seen.LoadOrStore(in.Marks, true); !again && in.Count > 1 {
		in.Count--
	}
	return gen.FakeGenerator{}.Generate(ctx, in)
}

type refusingGen struct{}

func (refusingGen) Generate(context.Context, gen.GenInput) (gen.GenOutput, error) {
	return gen.GenOutput{StopReason: gen.StopRefusal}, nil
}

// bigGen reports a large usage per call so a few calls reach the spend cap.
type bigGen struct {
	in     int64
	onCall func()
}

func (g bigGen) Generate(ctx context.Context, in gen.GenInput) (gen.GenOutput, error) {
	if g.onCall != nil {
		g.onCall()
	}
	out, err := gen.FakeGenerator{}.Generate(ctx, in)
	out.Usage.Input = g.in
	return out, err
}

func depsOf(g gen.Generator) deps {
	return deps{newFactory: func(string, func(string) string) (gen.GeneratorFactory, error) {
		return gen.FixedFactory(g), nil
	}}
}

func TestMaxUSDDefaultsToFiveAndAboveFifteenNeedsOverride(t *testing.T) {
	dir := goldenDir(t, "a.txt")
	code, out, errOut := runEval(t, fakeDeps(), env("AI_EVAL", "1"), "-model", testModel, "-dir", dir)
	if code != 0 || !strings.Contains(out, "cap USD 5.00") {
		t.Fatalf("default cap must be 5: exit %d err %s\n%s", code, errOut, out)
	}
	code, out, errOut = runEval(t, fakeDeps(), env("AI_EVAL", "1"), "-model", testModel, "-dir", dir, "-max-usd", "16")
	if code == 0 || !strings.Contains(errOut, "-allow-high-cap") || out != "" {
		t.Fatalf("16 must be rejected: exit %d err %q out %q", code, errOut, out)
	}
	code, out, _ = runEval(t, fakeDeps(), env("AI_EVAL", "1"), "-model", testModel, "-dir", dir, "-max-usd", "16", "-allow-high-cap")
	if code != 0 || !strings.Contains(out, "cap USD 16.00") {
		t.Fatalf("override must allow 16: exit %d\n%s", code, out)
	}
	if code, _, _ = runEval(t, fakeDeps(), env("AI_EVAL", "1"), "-model", testModel, "-dir", dir, "-max-usd", "0"); code == 0 {
		t.Error("a zero cap must be rejected")
	}
}

func TestCapStopsRunAndKeepsPaidRows(t *testing.T) {
	dir := goldenDir(t, "a.txt", "b.txt", "c.txt", "d.txt")
	csvPath := filepath.Join(t.TempDir(), "out.csv")
	// 1M input tokens per call on haiku is 1 USD; a job is 3 calls. Cap 5: a.txt costs 3, b.txt hits the cap in its third call.
	d := depsOf(bigGen{in: 1_000_000})
	code, out, errOut := runEval(t, d, env("AI_EVAL", "1"), "-model", testModel, "-dir", dir, "-types", "mcq", "-csv", csvPath)
	if code == 0 {
		t.Fatalf("a stopped run must exit non-zero:\n%s", out)
	}
	for _, f := range []string{"a.txt", "b.txt"} {
		if !strings.Contains(out, f) {
			t.Errorf("paid row for %s missing:\n%s", f, out)
		}
	}
	for _, f := range []string{"c.txt", "d.txt"} {
		if strings.Contains(out, f) {
			t.Errorf("%s must not run after the cap:\n%s", f, out)
		}
	}
	if !strings.Contains(errOut, "stopped at cap after 2 of 4 files") {
		t.Errorf("stop line missing: %q", errOut)
	}
	raw, err := os.ReadFile(csvPath)
	if err != nil || !strings.Contains(string(raw), "a.txt") || !strings.Contains(string(raw), "b.txt") || strings.Contains(string(raw), "c.txt") {
		t.Errorf("CSV must hold the partial rows: %v\n%s", err, raw)
	}
}

func TestCancelledContextReturnsPartialRows(t *testing.T) {
	dir := goldenDir(t, "a.txt", "b.txt", "c.txt")
	files, err := goldenFiles(dir)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	var once sync.Once
	f := gen.FixedFactory(bigGen{in: 10, onCall: func() { once.Do(cancel) }})
	m, _ := gen.LookupModel(testModel)
	rows, stop := evaluate(ctx, f, m, files, []string{"mcq", "free"}, 5, "medium")
	if len(rows) == 0 || len(rows) >= 6 || stop == "" {
		t.Fatalf("want some but not all rows and a stop reason, got %d rows, stop %q", len(rows), stop)
	}
}

func TestFirstPassBranches(t *testing.T) {
	dir := goldenDir(t, "a.txt")
	run1 := func(g gen.Generator) string {
		code, out, errOut := runEval(t, depsOf(g), env("AI_EVAL", "1"), "-model", testModel, "-dir", dir, "-types", "mcq")
		if code != 0 {
			t.Fatalf("exit %d: %s", code, errOut)
		}
		return out
	}
	if out := run1(&flakyGen{mode: "max_tokens"}); !strings.Contains(out, " 0% ") {
		t.Errorf("a cut-off first call keeps nothing first pass:\n%s", out)
	}
	if out := run1(&flakyGen{mode: "invalid"}); !strings.Contains(out, " 0% ") {
		t.Errorf("invalid JSON on the first call keeps nothing first pass:\n%s", out)
	}
	if out := run1(&flakyGen{mode: "auth"}); !strings.Contains(out, "n/a") {
		t.Errorf("a failed job has an unknown first-pass figure:\n%s", out)
	}
}

// flakyGen fails the first call of each mark value in one way, then behaves.
type flakyGen struct {
	mode string
	seen sync.Map
}

func (g *flakyGen) Generate(ctx context.Context, in gen.GenInput) (gen.GenOutput, error) {
	if _, again := g.seen.LoadOrStore(in.Marks, true); !again {
		switch g.mode {
		case "max_tokens":
			return gen.GenOutput{StopReason: gen.StopMaxTokens}, nil
		case "invalid":
			return gen.GenOutput{}, gen.ErrInvalidOutput
		case "auth":
			return gen.GenOutput{}, gen.ErrAuth
		}
	}
	return gen.FakeGenerator{}.Generate(ctx, in)
}

func TestHeaderShowsEffortAndSpend(t *testing.T) {
	dir := goldenDir(t, "a.txt")
	_, out, _ := runEval(t, fakeDeps(), env("AI_EVAL", "1", "LLM_EFFORT", "high"), "-model", "claude-sonnet-5-5", "-dir", dir)
	if !strings.Contains(out, "effort high") || !strings.Contains(out, "spent USD") {
		t.Errorf("header lacks effort or spend:\n%s", out)
	}
}
