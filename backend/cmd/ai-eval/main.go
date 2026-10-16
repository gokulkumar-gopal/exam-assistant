// Command ai-eval is the golden-set run of AI-spec 13.2: it generates questions from every
// file in a directory, for both question types and a standard marks breakdown, with the
// production worker (in-memory stores, the same source preparation, validation, retry and
// spend accounting), and prints one report row per file and type (AI-spec 13.3, 13.4).
//
// It calls the real provider API and spends money, so it runs only by hand:
// `make ai-eval MODEL=<catalog id>`, and only when AI_EVAL=1 is set.
package main

import (
	"context"
	"encoding/csv"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/gokulkumar/exam-assistant/backend/internal/clock"
	"github.com/gokulkumar/exam-assistant/backend/internal/config"
	"github.com/gokulkumar/exam-assistant/backend/internal/docs"
	"github.com/gokulkumar/exam-assistant/backend/internal/gen"
	"github.com/gokulkumar/exam-assistant/backend/internal/gen/prompt"
	"github.com/gokulkumar/exam-assistant/backend/internal/gen/providers"
	"github.com/gokulkumar/exam-assistant/backend/internal/store"
	"github.com/gokulkumar/exam-assistant/backend/internal/store/memory"
)

// standardCounts is the marks breakdown of every run: the example of AI-spec 1 (four
// 1-mark, three 2-mark, two 3-mark questions), 9 in all, one first call per mark value.
var standardCounts = map[string]int{"1": 4, "2": 3, "3": 2}

const (
	owner      = "ai-eval"
	wantEnvVar = "AI_EVAL"
	// defaultMaxUSD is the spend cap of one run. It covers every non-Opus model on 8 to 10 files
	// with margin; an Opus run says so with -max-usd. maxUSDLimit is the production monthly
	// budget (LLM_MONTHLY_BUDGET_USD default): a higher cap needs -allow-high-cap.
	defaultMaxUSD = 5.0
	maxUSDLimit   = 15.0
)

// deps are the parts a test replaces.
type deps struct {
	// newFactory returns the generator factory for the model. It must fail, naming the
	// missing variable (never its value), when the provider key is not set.
	newFactory func(modelID string, getenv func(string) string) (gen.GeneratorFactory, error)
}

func main() {
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn})))
	// Ctrl-C stops the run after the current call and still prints the rows already paid for.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := run(ctx, os.Args[1:], os.Getenv, os.Stdout, os.Stderr, deps{newFactory: providerFactory})
	stop()
	os.Exit(code)
}

// providerFactory is the production factory: the real adapters from the model's catalog entry.
func providerFactory(modelID string, getenv func(string) string) (gen.GeneratorFactory, error) {
	m, ok := gen.LookupModel(modelID)
	if !ok {
		return nil, fmt.Errorf("model %q is not in the catalog", modelID)
	}
	effort := effortFrom(getenv)
	f := providers.New(config.Config{
		Providers: []string{m.Provider}, LLMAPIKey: getenv("LLM_API_KEY"), GeminiAPIKey: getenv("GEMINI_API_KEY"),
		LLMEffort: effort, LLMTimeoutSeconds: 120,
	})
	// Build one generator now so a missing key is reported before any file is read.
	if _, err := f(modelID); err != nil {
		return nil, err
	}
	return f, nil
}

// effortFrom is LLM_EFFORT with the production default. The same value builds the providers
// and the worker, and is shown in the report.
func effortFrom(getenv func(string) string) string {
	if e := getenv("LLM_EFFORT"); e != "" {
		return e
	}
	return "medium"
}

func run(ctx context.Context, args []string, getenv func(string) string, stdout, stderr io.Writer, d deps) int {
	fs := flag.NewFlagSet("ai-eval", flag.ContinueOnError)
	fs.SetOutput(stderr)
	model := fs.String("model", "", "catalog model id to evaluate (required)")
	dir := fs.String("dir", "testdata/ai", "directory of golden-set files")
	types := fs.String("types", "mcq,free", "question types to generate")
	csvPath := fs.String("csv", "", "also write the report to this CSV file")
	maxUSD := fs.Float64("max-usd", defaultMaxUSD, "stop the run when its spend reaches this many USD")
	allowHigh := fs.Bool("allow-high-cap", false, fmt.Sprintf("allow -max-usd above %.0f (the production monthly budget)", maxUSDLimit))
	if err := fs.Parse(args); err != nil {
		return 2
	}
	// The gate comes first so that nothing below can run under `go test ./...` by accident.
	if getenv(wantEnvVar) != "1" {
		fmt.Fprintln(stderr, "ai-eval calls the real provider API and spends money. Set AI_EVAL=1 to run it (use `make ai-eval MODEL=<catalog id>`).")
		return 1
	}
	if *model == "" {
		fmt.Fprintln(stderr, "ai-eval: -model is required (a catalog id; the evaluation never picks a default model)")
		return 2
	}
	m, ok := gen.LookupModel(*model)
	if !ok {
		fmt.Fprintf(stderr, "ai-eval: model %q is not in the catalog\n", *model)
		return 2
	}
	var kinds []string
	for _, t := range strings.Split(*types, ",") {
		t = strings.TrimSpace(t)
		if t != "mcq" && t != "free" {
			fmt.Fprintf(stderr, "ai-eval: -types takes mcq and free, got %q\n", t)
			return 2
		}
		if !slices.Contains(kinds, t) {
			kinds = append(kinds, t)
		}
	}
	if *maxUSD <= 0 {
		fmt.Fprintln(stderr, "ai-eval: -max-usd must be above zero")
		return 2
	}
	if *maxUSD > maxUSDLimit && !*allowHigh {
		fmt.Fprintf(stderr, "ai-eval: -max-usd %.2f is above %.0f USD, the production monthly budget; pass -allow-high-cap to run with it anyway\n", *maxUSD, maxUSDLimit)
		return 2
	}
	files, err := goldenFiles(*dir)
	if err != nil {
		fmt.Fprintf(stderr, "ai-eval: %v\n", err)
		return 1
	}
	factory, err := d.newFactory(*model, getenv)
	if err != nil {
		fmt.Fprintf(stderr, "ai-eval: %v\n", err)
		return 1
	}

	effort := effortFrom(getenv)
	rows, stopped := evaluate(ctx, factory, m, files, kinds, *maxUSD, effort)
	// Whatever stopped the run, the rows already paid for are reported first.
	printReport(stdout, m, effort, *maxUSD, rows)
	code := 0
	if *csvPath != "" {
		if err := writeCSV(*csvPath, rows); err != nil {
			fmt.Fprintf(stderr, "ai-eval: %v\n", err)
			code = 1
		}
	}
	if stopped != "" {
		fmt.Fprintf(stderr, "ai-eval: %s after %d of %d files; the report above is partial\n", stopped, filesStarted(rows), len(files))
		code = 1
	}
	return code
}

func filesStarted(rows []Row) int {
	seen := map[string]bool{}
	for _, r := range rows {
		seen[r.File] = true
	}
	return len(seen)
}

type goldenFile struct {
	Name, ContentType, DocType string
	Data                       []byte
}

var extTypes = map[string][3]string{
	".pdf": {"application/pdf", "PDF"}, ".jpg": {"image/jpeg", "JPG"}, ".jpeg": {"image/jpeg", "JPG"},
	".png": {"image/png", "PNG"}, ".txt": {"text/plain", "TXT"},
}

// goldenFiles reads the supported files of dir (PDF, JPG, PNG, TXT) in name order; other
// files, such as the README, are not sources.
func goldenFiles(dir string) ([]goldenFile, error) {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("golden-set directory %q cannot be read: %w (see testdata/ai/README.md for the files to supply)", dir, err)
	}
	var out []goldenFile
	for _, e := range ents {
		t, ok := extTypes[strings.ToLower(filepath.Ext(e.Name()))]
		if !ok || e.IsDir() {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", e.Name(), err)
		}
		out = append(out, goldenFile{Name: e.Name(), ContentType: t[0], DocType: t[1], Data: data})
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("golden-set directory %q has no PDF, JPG, PNG or TXT files (see testdata/ai/README.md for the files to supply)", dir)
	}
	return out, nil
}

// Row is one report line: one file, one question type.
type Row struct {
	File, Type                    string
	Requested, Kept, FirstPass    int
	Dropped, Calls                int
	In, Out, CacheRead            int64
	CostMicros                    int64
	Seconds                       float64
	Refusals                      int
	PromptVersion, Status, Failed string
	Effort                        string
	FirstPassKnown                bool
}

func (r Row) firstPassPct() float64 {
	if r.Requested == 0 {
		return 0
	}
	return 100 * float64(r.FirstPass) / float64(r.Requested)
}

// firstPassText is "n/a" when the run ended before the first-pass figure could be known.
func (r Row) firstPassText(format string) string {
	if !r.FirstPassKnown {
		return "n/a"
	}
	return fmt.Sprintf(format, r.firstPassPct())
}

// evaluate runs every file and type through the production worker, one job at a time. It
// always returns the rows gathered so far; stopped says why it ended early ("" when it ran
// to the end): the spend cap, an interrupt, or an error.
func evaluate(ctx context.Context, factory gen.GeneratorFactory, m gen.Model, files []goldenFile, kinds []string, capUSD float64, effort string) (rows []Row, stopped string) {
	clk := clock.Real{}
	capMicros := gen.BudgetMicros(capUSD)
	jobs, docStore, blob := memory.NewJobStore(clk), memory.NewDocStore(clk), docs.NewMemBlob()
	var cur *tracker
	w := &gen.Worker{
		Jobs: jobs, Docs: docStore, Blob: blob, Clock: clk,
		Gens: func(id string) (gen.Generator, error) {
			g, err := factory(id)
			if err != nil {
				return nil, err
			}
			return cur.wrap(g), nil
		},
		Cfg: gen.WorkerConfig{
			Effort: effort, PromptVersion: prompt.PromptVersion, CallTimeout: 120 * time.Second,
			BudgetMicros: capMicros,
		},
	}
	var spent int64
	n := 0
	for i, f := range files {
		docID := fmt.Sprintf("doc%03d", i+1)
		key := "golden/" + docID
		blob.Put(key, f.Data)
		if err := docStore.PutDoc(ctx, store.Document{
			ID: docID, OwnerID: owner, Name: f.Name, Type: f.DocType, ContentType: f.ContentType,
			SizeBytes: int64(len(f.Data)), S3Key: key, Status: store.DocReady, CreatedAt: clk.Now(),
		}); err != nil {
			return rows, err.Error()
		}
		for _, kind := range kinds {
			if ctx.Err() != nil {
				return rows, "interrupted"
			}
			if spent >= capMicros {
				return rows, "stopped at cap"
			}
			n++
			job := store.Job{
				ID: fmt.Sprintf("eval%04d", n), OwnerID: owner, Kind: store.KindGenerate, Model: m.ID,
				Day: gen.DayKey(clk.Now()), Status: store.JobQueued, CreatedAt: clk.Now(),
				Request: store.GenerateRequest{SourceIDs: []string{docID}, Type: kind, Counts: standardCounts},
				Total:   total(standardCounts),
			}
			q := store.QuotaCheck{
				UserID: owner, Day: job.Day, Month: gen.MonthKey(clk.Now()),
				MaxUser: 1 << 20, MaxGlobal: 1 << 20, BudgetMicros: capMicros,
			}
			if err := jobs.CreateJobWithQuota(ctx, job, q); err != nil {
				var qe *store.QuotaError
				if errors.As(err, &qe) && qe.Kind == store.QuotaBudget {
					return rows, "stopped at cap"
				}
				return rows, err.Error()
			}
			cur = &tracker{}
			start := time.Now()
			runErr := w.Run(ctx, job.ID)
			secs := time.Since(start).Seconds()
			done, err := jobs.GetJob(context.WithoutCancel(ctx), job.ID)
			if err != nil {
				return rows, fmt.Sprintf("%s (%s): %v", f.Name, kind, err)
			}
			row := buildRow(f.Name, kind, done, cur, secs)
			rows = append(rows, row)
			spent += row.CostMicros
			switch {
			case ctx.Err() != nil:
				return rows, "interrupted"
			case runErr != nil:
				return rows, fmt.Sprintf("%s (%s): %v", f.Name, kind, runErr)
			case done.Error == gen.BudgetMessage:
				return rows, "stopped at cap"
			}
		}
	}
	return rows, ""
}

func total(c map[string]int) int {
	n := 0
	for _, v := range c {
		n += v
	}
	return n
}

func buildRow(file, kind string, j store.Job, t *tracker, secs float64) Row {
	u := j.Usage
	first, known := t.firstPassKept(standardCounts, j.Status == store.JobDone)
	r := Row{
		File: file, Type: kind, Requested: total(standardCounts), Kept: j.Done, Dropped: u.Dropped, Calls: u.Calls,
		In: u.InputTokens, Out: u.OutputTokens, CacheRead: u.CacheReadTokens, CostMicros: u.CostMicros,
		Seconds: secs, Refusals: t.refusals(), PromptVersion: u.PromptVersion, Status: j.Status,
		FirstPass: first, FirstPassKnown: known, Effort: u.Effort,
	}
	if j.Status != store.JobDone {
		r.Failed = j.Error
	}
	return r
}

// tracker observes the generator calls of one job. The worker already counts kept, dropped,
// calls, tokens and cost on the job record; what it does not record is how many questions the
// first call of each mark value delivered and whether the model refused, so those two are
// read from the calls themselves.
type tracker struct {
	mu    sync.Mutex
	byMk  map[int][]callObs
	refus int
}

type callObs struct {
	asked     int
	maxTokens bool
	failed    bool
}

func (t *tracker) wrap(g gen.Generator) gen.Generator {
	tg := &trackedGen{inner: g, t: t}
	if c, ok := g.(gen.TokenCounter); ok {
		return &trackedCounter{trackedGen: tg, c: c}
	}
	return tg
}

type trackedGen struct {
	inner gen.Generator
	t     *tracker
}

func (g *trackedGen) Generate(ctx context.Context, in gen.GenInput) (gen.GenOutput, error) {
	out, err := g.inner.Generate(ctx, in)
	g.t.mu.Lock()
	defer g.t.mu.Unlock()
	if g.t.byMk == nil {
		g.t.byMk = map[int][]callObs{}
	}
	g.t.byMk[in.Marks] = append(g.t.byMk[in.Marks], callObs{
		asked:     in.Count,
		maxTokens: out.StopReason == gen.StopMaxTokens || errors.Is(err, gen.ErrMaxTokens),
		failed:    err != nil && !errors.Is(err, gen.ErrInvalidOutput) || out.StopReason == gen.StopRefusal,
	})
	if out.StopReason == gen.StopRefusal || errors.Is(err, gen.ErrRefusal) {
		g.t.refus++
	}
	return out, err
}

type trackedCounter struct {
	*trackedGen
	c gen.TokenCounter
}

func (g *trackedCounter) CountTokens(ctx context.Context, in gen.GenInput) (int, error) {
	return g.c.CountTokens(ctx, in)
}

func (t *tracker) refusals() int {
	if t == nil {
		return 0
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.refus
}

// firstPassKept is the number of questions that passed every check in the first call of each
// mark value. A follow-up call asks for exactly what the first call left short (AI-spec 7.4),
// so the first call kept requested minus the follow-up's ask; a first call that was cut off,
// failed or refused kept none; with no follow-up it kept everything. known is false when the
// job did not finish and a mark's first call has no follow-up to read the shortfall from (it
// may have been short, with the follow-up stopped by the cap or a failure), or never ran.
// It assumes every count fits one call (at most 15, true of standardCounts): above that the
// worker plans several calls with the same Marks and calls[1] would be another chunk.
func (t *tracker) firstPassKept(counts map[string]int, jobDone bool) (kept int, known bool) {
	if t == nil {
		return 0, false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	known = true
	for k, want := range counts {
		mk, _ := strconv.Atoi(k)
		calls := t.byMk[mk]
		switch {
		case len(calls) == 0:
			known = known && jobDone
		case calls[0].maxTokens, calls[0].failed:
		case len(calls) > 1:
			kept += max(want-calls[1].asked, 0)
		default:
			kept += want
			known = known && jobDone
		}
	}
	return kept, known
}

func usd(micros int64) string { return fmt.Sprintf("%.4f", float64(micros)/1e6) }

func printReport(w io.Writer, m gen.Model, effort string, capUSD float64, rows []Row) {
	var spent int64
	for _, r := range rows {
		spent += r.CostMicros
	}
	sent := gen.EffortSent(m.ID, effort)
	if sent == "" {
		sent = "none"
	}
	fmt.Fprintf(w, "ai-eval golden set: model %s, effort %s, PromptVersion %s, %d runs, cap USD %.2f, spent USD %s\n\n",
		m.ID, sent, prompt.PromptVersion, len(rows), capUSD, usd(spent))
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "file\ttype\trequested\tkept after retry\tfirst-pass kept %\tdropped\tcalls\ttokens in/out/cacheRead\tcost USD\tseconds\trefusals\tPromptVersion\tresult")
	var cost int64
	for _, r := range rows {
		cost += r.CostMicros
		res := "ok"
		switch {
		case r.Refusals > 0:
			res = "refused"
		case r.Status != store.JobDone:
			res = "failed: " + r.Failed
		}
		fmt.Fprintf(tw, "%s\t%s\t%d\t%d\t%s\t%d\t%d\t%d/%d/%d\t%s\t%.1f\t%d\t%s\t%s\n",
			r.File, r.Type, r.Requested, r.Kept, r.firstPassText("%.0f%%"), r.Dropped, r.Calls,
			r.In, r.Out, r.CacheRead, usd(r.CostMicros), r.Seconds, r.Refusals, r.PromptVersion, res)
	}
	_ = tw.Flush()
	fmt.Fprintf(w, "\ntotal cost USD %s\n", usd(cost))
}

func writeCSV(path string, rows []Row) error {
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("write csv: %w", err)
	}
	defer f.Close()
	cw := csv.NewWriter(f)
	_ = cw.Write([]string{"file", "type", "requested", "kept_after_retry", "first_pass_kept_pct", "dropped", "calls",
		"tokens_in", "tokens_out", "tokens_cache_read", "cost_usd", "seconds", "refusals", "prompt_version", "effort", "status", "error"})
	for _, r := range rows {
		_ = cw.Write([]string{r.File, r.Type, strconv.Itoa(r.Requested), strconv.Itoa(r.Kept),
			r.firstPassText("%.1f"), strconv.Itoa(r.Dropped), strconv.Itoa(r.Calls),
			strconv.FormatInt(r.In, 10), strconv.FormatInt(r.Out, 10), strconv.FormatInt(r.CacheRead, 10),
			usd(r.CostMicros), fmt.Sprintf("%.1f", r.Seconds), strconv.Itoa(r.Refusals), r.PromptVersion, r.Effort, r.Status, r.Failed})
	}
	cw.Flush()
	if err := cw.Error(); err != nil {
		return fmt.Errorf("write csv: %w", err)
	}
	return f.Close()
}
