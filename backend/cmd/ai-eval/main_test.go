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
	if code == 0 || !strings.Contains(errOut, "-model") {
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
