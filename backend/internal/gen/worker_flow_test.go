package gen

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gokulkumar/exam-assistant/backend/internal/store"
)

// ---- helpers ----

var flowSeq atomic.Int64

// validOut returns in.Count valid, mutually distinct questions citing doc-1.
func validOut(in GenInput) GenOutput {
	out := GenOutput{StopReason: "end_turn", Usage: Usage{Input: 1000, Output: 100}}
	for i := 0; i < in.Count; i++ {
		n := flowSeq.Add(1)
		q := RawQuestion{
			Marks:  in.Marks,
			Text:   fmt.Sprintf("Explain topic%d with detail%d and aspect%d plus angle%d", n, n, n, n),
			Source: "doc-1",
		}
		if in.Type == "mcq" {
			zero := 0
			q.Options, q.CorrectIndex = []string{"a", "b", "c", "d"}, &zero
		} else {
			q.ModelAnswer = "answer"
		}
		out.Questions = append(out.Questions, q)
	}
	return out
}

// badQuestion is a question Validate drops (HTML) with a recognisable text.
func badQuestion(marks int, text string) RawQuestion {
	zero := 0
	return RawQuestion{Marks: marks, Text: text + " <b>bold</b>", Options: []string{"a", "b", "c", "d"}, CorrectIndex: &zero, Source: "doc-1"}
}

// seq returns a generator function that serves the given handlers in call order.
func seq(t *testing.T, fns ...func(in GenInput) (GenOutput, error)) func(context.Context, GenInput) (GenOutput, error) {
	var mu sync.Mutex
	n := 0
	return func(_ context.Context, in GenInput) (GenOutput, error) {
		mu.Lock()
		i := n
		n++
		mu.Unlock()
		if i >= len(fns) {
			t.Errorf("unexpected call %d (count %d marks %d)", i+1, in.Count, in.Marks)
			return GenOutput{}, errors.New("unexpected call")
		}
		return fns[i](in)
	}
}

func okFn(in GenInput) (GenOutput, error) { return validOut(in), nil }

type countGen struct {
	testGen
	tokens  int
	err     error
	counted atomic.Int32
}

func (c *countGen) CountTokens(_ context.Context, _ GenInput) (int, error) {
	c.counted.Add(1)
	return c.tokens, c.err
}

func (e *wEnv) withCounter(tokens int) *countGen {
	cg := &countGen{tokens: tokens}
	e.w.Gens = FixedFactory(cg)
	return cg
}

func haiku() Model { m, _ := LookupModel("claude-haiku-4-5"); return m }

func (e *wEnv) questions(t *testing.T) []store.Question {
	t.Helper()
	qs, err := e.raw.GetQuestions(context.Background(), e.jobID)
	if err != nil {
		t.Fatalf("GetQuestions: %v", err)
	}
	return qs
}

func (e *wEnv) noQuestions(t *testing.T) {
	t.Helper()
	if _, err := e.raw.GetQuestions(context.Background(), e.jobID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("questions were kept: %v", err)
	}
}

func (e *wEnv) wantFailed(t *testing.T, msg string) store.Job {
	t.Helper()
	j := e.job(t)
	if j.Status != store.JobFailed || j.Error != msg {
		t.Fatalf("job = %s %q, want failed %q", j.Status, j.Error, msg)
	}
	e.noQuestions(t)
	return j
}

func (e *wEnv) spendList() []int64 {
	e.jobs.mu.Lock()
	defer e.jobs.mu.Unlock()
	return slices.Clone(e.jobs.spend)
}

func counts(e *wEnv) []int {
	e.gen.mu.Lock()
	defer e.gen.mu.Unlock()
	var out []int
	for _, c := range e.gen.calls {
		out = append(out, c.Count)
	}
	return out
}

// ---- planning ----

func TestPlanCallsChunksOf15(t *testing.T) {
	got := PlanCalls(map[int]int{1: 30}, 15)
	if len(got) != 2 || got[0].Marks != 1 || got[0].Count != 15 || got[1].Count != 15 {
		t.Errorf("30 x 1-mark = %+v, want two calls of 15", got)
	}
	got = PlanCalls(map[int]int{1: 16, 2: 16, 3: 16, 4: 16, 5: 16}, 15)
	if len(got) != 10 {
		t.Fatalf("5 marks over 15 = %d calls, want 10", len(got))
	}
	for i, c := range got {
		wantMarks, wantCount := i/2+1, 15
		if i%2 == 1 {
			wantCount = 1
		}
		if c.Marks != wantMarks || c.Count != wantCount {
			t.Errorf("call %d = %+v, want marks %d count %d", i, c, wantMarks, wantCount)
		}
	}
	// Zero counts make no call; marks ascend whatever the map order; perCall 0 means 15.
	got = PlanCalls(map[int]int{5: 2, 3: 0, 1: 20}, 0)
	var shape []string
	for _, c := range got {
		shape = append(shape, fmt.Sprintf("%dx%d", c.Marks, c.Count))
	}
	if strings.Join(shape, " ") != "1x15 1x5 5x2" {
		t.Errorf("shape = %v", shape)
	}
}

func TestFirstCallRunsAloneThenAtMost3Concurrent(t *testing.T) {
	e := newWEnvShuffle(t, map[string]int{"1": 1, "2": 1, "3": 1, "4": 1, "5": 1}, true)
	var mu sync.Mutex
	var cur, peak, started int
	var events []string
	e.gen.fn = func(ctx context.Context, in GenInput) (GenOutput, error) {
		mu.Lock()
		started++
		n := started
		cur++
		peak = max(peak, cur)
		events = append(events, fmt.Sprintf("start%d", in.Marks))
		mu.Unlock()
		if n <= 4 { // calls 2 to 4 wait until three are in flight together
			deadline := time.Now().Add(2 * time.Second)
			for time.Now().Before(deadline) {
				mu.Lock()
				c := cur
				mu.Unlock()
				if c >= 3 || n == 1 {
					break
				}
				time.Sleep(time.Millisecond)
			}
		}
		time.Sleep(5 * time.Millisecond)
		mu.Lock()
		cur--
		events = append(events, fmt.Sprintf("end%d", in.Marks))
		mu.Unlock()
		return validOut(in), nil
	}
	e.run(t)
	if e.job(t).Status != store.JobDone {
		t.Fatalf("job = %+v", e.job(t))
	}
	if events[0] != "start1" || events[1] != "end1" {
		t.Errorf("first call was not alone: %v", events)
	}
	if peak != 3 {
		t.Errorf("max concurrency = %d, want 3", peak)
	}
	if len(e.questions(t)) != 5 {
		t.Errorf("questions = %d", len(e.questions(t)))
	}
}

// ---- caps ----

func TestCapsFailBeforeAnyCall(t *testing.T) {
	t.Run("bytes", func(t *testing.T) {
		e := newWEnv(t, map[string]int{"1": 2})
		e.w.Cfg.MaxTotalBytes = 30 // the two documents are 20 and 19 bytes
		e.run(t)
		e.wantFailed(t, TooLargeMessage)
		if e.gen.callCount() != 0 || len(e.spendList()) != 0 {
			t.Errorf("calls=%d spend=%v", e.gen.callCount(), e.spendList())
		}
	})
	t.Run("pages", func(t *testing.T) {
		e := newWEnv(t, map[string]int{"1": 2})
		for _, id := range []string{"d1", "d2"} {
			d, _ := e.docs.GetDocs(context.Background(), "u1", []string{id})
			d[0].Pages = 21
			if err := e.docs.PutDoc(context.Background(), d[0]); err != nil {
				t.Fatal(err)
			}
		}
		e.w.Cfg.MaxPDFPages = 40
		e.run(t)
		e.wantFailed(t, TooLargeMessage)
		if e.gen.callCount() != 0 {
			t.Errorf("calls = %d", e.gen.callCount())
		}
	})
	t.Run("tokens", func(t *testing.T) {
		e := newWEnv(t, map[string]int{"1": 2})
		cg := e.withCounter(80_001)
		e.run(t)
		e.wantFailed(t, TooLargeMessage)
		if cg.callCount() != 0 || cg.counted.Load() != 1 {
			t.Errorf("calls=%d counted=%d", cg.callCount(), cg.counted.Load())
		}
		if len(e.spendList()) != 0 {
			t.Errorf("counting is not spend: %v", e.spendList())
		}
	})
	t.Run("exactly at the token cap passes", func(t *testing.T) {
		e := newWEnv(t, map[string]int{"1": 2})
		e.withCounter(80_000)
		e.run(t)
		if e.job(t).Status != store.JobDone {
			t.Errorf("job = %+v", e.job(t))
		}
	})
	t.Run("a configured cap applies", func(t *testing.T) {
		e := newWEnv(t, map[string]int{"1": 2})
		e.w.Cfg.MaxInputTokens = 500
		e.withCounter(501)
		e.run(t)
		e.wantFailed(t, TooLargeMessage)
	})
}

// ---- shortfalls and retries ----

func TestShortBatchOneFollowUpWithAvoid(t *testing.T) {
	e := newWEnv(t, map[string]int{"1": 3})
	e.gen.fn = seq(t,
		func(in GenInput) (GenOutput, error) {
			out := validOut(GenInput{Type: in.Type, Marks: in.Marks, Count: 2})
			out.Questions = append(out.Questions, badQuestion(1, "Dropped question about volcano"))
			return out, nil
		},
		okFn,
	)
	e.run(t)
	j := e.job(t)
	if j.Status != store.JobDone || j.Done != 3 || len(e.questions(t)) != 3 {
		t.Fatalf("job = %s %q done=%d", j.Status, j.Error, j.Done)
	}
	if got := counts(e); !slices.Equal(got, []int{3, 1}) {
		t.Fatalf("call counts = %v, want [3 1] (follow-up asks for the missing one)", got)
	}
	avoid := e.gen.calls[1].Avoid
	var kept, droppedSeen int
	for _, a := range avoid {
		switch {
		case strings.HasPrefix(a, "Explain topic"):
			kept++
		case strings.HasPrefix(a, "Dropped question about volcano"):
			droppedSeen++
		}
	}
	if kept != 2 || droppedSeen != 1 {
		t.Errorf("follow-up Avoid = %q, want the 2 kept and the dropped text", avoid)
	}
	if j.Usage.Calls != 2 || j.Usage.Dropped != 1 || j.Usage.Kept != 3 {
		t.Errorf("usage = %+v", j.Usage)
	}
}

func TestSecondShortfallFails(t *testing.T) {
	e := newWEnv(t, map[string]int{"1": 3})
	short := func(in GenInput) (GenOutput, error) {
		out := validOut(GenInput{Type: in.Type, Marks: in.Marks, Count: 1})
		for i := 1; i < in.Count; i++ {
			out.Questions = append(out.Questions, badQuestion(1, fmt.Sprintf("Broken%d question", flowSeq.Add(1))))
		}
		return out, nil
	}
	e.gen.fn = seq(t, short, short)
	e.run(t)
	e.wantFailed(t, ShortfallMessage)
	if len(e.spendList()) != 2 {
		t.Errorf("spend = %v, want both calls recorded", e.spendList())
	}
}

func TestMaxTokensRetriesOnceWithHalfCount(t *testing.T) {
	for _, viaErr := range []bool{false, true} {
		t.Run(fmt.Sprintf("viaErr=%v", viaErr), func(t *testing.T) {
			e := newWEnv(t, map[string]int{"1": 4})
			cut := func(in GenInput) (GenOutput, error) {
				out := GenOutput{StopReason: "max_tokens", Usage: Usage{Input: 2000, Output: 300}}
				if viaErr {
					return out, fmt.Errorf("cut off: %w", ErrMaxTokens)
				}
				out.Questions = validOut(in).Questions[:1] // truncated output is not trusted
				return out, nil
			}
			e.gen.fn = seq(t, cut, okFn, okFn)
			e.run(t)
			if e.job(t).Status != store.JobDone || len(e.questions(t)) != 4 {
				t.Fatalf("job = %+v", e.job(t))
			}
			// 4 -> max_tokens -> 2 (half) -> the missing 2 by the shortfall follow-up.
			if got := counts(e); !slices.Equal(got, []int{4, 2, 2}) {
				t.Errorf("call counts = %v, want [4 2 2]", got)
			}
		})
	}
	t.Run("repeated fails", func(t *testing.T) {
		e := newWEnv(t, map[string]int{"1": 15})
		cut := func(in GenInput) (GenOutput, error) {
			return GenOutput{StopReason: "max_tokens", Usage: Usage{Input: 2000, Output: 300}}, nil
		}
		e.gen.fn = seq(t, cut, cut)
		e.run(t)
		e.wantFailed(t, GenerateFailedMessage)
		if got := counts(e); !slices.Equal(got, []int{15, 8}) {
			t.Errorf("call counts = %v, want [15 8]", got)
		}
		if len(e.spendList()) != 2 {
			t.Errorf("spend = %v", e.spendList())
		}
	})
}

func TestRefusalFailsBatchGenericMessage(t *testing.T) {
	for name, fn := range map[string]func(in GenInput) (GenOutput, error){
		"stop reason": func(in GenInput) (GenOutput, error) {
			return GenOutput{StopReason: "refusal", Usage: Usage{Input: 900}}, nil
		},
		"error": func(in GenInput) (GenOutput, error) {
			return GenOutput{Usage: Usage{Input: 900}}, fmt.Errorf("declined: %w", ErrRefusal)
		},
	} {
		t.Run(name, func(t *testing.T) {
			e := newWEnv(t, map[string]int{"1": 2, "2": 2})
			e.gen.fn = seq(t, fn)
			e.run(t)
			e.wantFailed(t, "We couldn't generate questions from these files.")
			if e.gen.callCount() != 1 {
				t.Errorf("calls = %d, the identical request must not be retried", e.gen.callCount())
			}
			if got := e.spendList(); len(got) != 1 || got[0] != CostMicros(haiku(), Usage{Input: 900}) {
				t.Errorf("spend = %v", got)
			}
		})
	}
}

func TestProvider429AfterRetriesMessage(t *testing.T) {
	e := newWEnv(t, map[string]int{"1": 2})
	e.gen.fn = seq(t, func(in GenInput) (GenOutput, error) {
		return GenOutput{}, fmt.Errorf("after retries: %w", ErrRateLimited)
	})
	e.run(t)
	e.wantFailed(t, "The AI service is busy. Try again in a minute.")
	if e.gen.callCount() != 1 {
		t.Errorf("calls = %d: the provider adapter owns 429 retries", e.gen.callCount())
	}
	if got := e.spendList(); len(got) != 0 {
		t.Errorf("a rejected request is not charged: %v", got)
	}
}

func TestCallTimeoutMessage(t *testing.T) {
	t.Run("provider timeout", func(t *testing.T) {
		e := newWEnv(t, map[string]int{"1": 2})
		e.gen.fn = seq(t, func(in GenInput) (GenOutput, error) {
			return GenOutput{}, fmt.Errorf("call: %w", ErrTimeout)
		})
		e.run(t)
		e.wantFailed(t, "That took too long. Try again.")
	})
	t.Run("per-call deadline", func(t *testing.T) {
		e := newWEnv(t, map[string]int{"1": 2})
		e.w.Cfg.CallTimeout = 20 * time.Millisecond
		e.gen.fn = func(ctx context.Context, in GenInput) (GenOutput, error) {
			<-ctx.Done()
			return GenOutput{}, ctx.Err()
		}
		e.run(t)
		e.wantFailed(t, "That took too long. Try again.")
		if len(e.spendList()) != 1 {
			t.Errorf("a timed-out call is charged for its input: %v", e.spendList())
		}
	})
}

func TestBadKey401GenericAndLoggedNotInJob(t *testing.T) {
	var buf bytes.Buffer
	captureLogs(t, &buf)
	e := newWEnv(t, map[string]int{"1": 2})
	e.gen.fn = seq(t, func(in GenInput) (GenOutput, error) {
		return GenOutput{}, fmt.Errorf("provider rejected the credentials (401): %w", ErrAuth)
	})
	e.run(t)
	j := e.wantFailed(t, GenerateFailedMessage)
	if strings.Contains(j.Error, "401") || strings.Contains(j.Error, "credentials") {
		t.Errorf("job error leaks details: %q", j.Error)
	}
	logs := buf.String()
	if !strings.Contains(logs, "level=ERROR") || !strings.Contains(logs, "401") {
		t.Errorf("a bad key must be logged loudly with details; logs:\n%s", logs)
	}
	if got := e.spendList(); len(got) != 0 {
		t.Errorf("a rejected request is not charged: %v", got)
	}
}

func TestInvalidJSONCountsAsMissing(t *testing.T) {
	invalid := func(in GenInput) (GenOutput, error) {
		return GenOutput{Usage: Usage{Input: 1000, Output: 50}}, fmt.Errorf("not json: %w", ErrInvalidOutput)
	}
	t.Run("follow-up recovers", func(t *testing.T) {
		e := newWEnv(t, map[string]int{"1": 3})
		e.gen.fn = seq(t, invalid, okFn)
		e.run(t)
		if e.job(t).Status != store.JobDone || len(e.questions(t)) != 3 {
			t.Fatalf("job = %+v", e.job(t))
		}
		if got := counts(e); !slices.Equal(got, []int{3, 3}) {
			t.Errorf("call counts = %v, want [3 3]", got)
		}
	})
	t.Run("second invalid is a shortfall", func(t *testing.T) {
		e := newWEnv(t, map[string]int{"1": 3})
		e.gen.fn = seq(t, invalid, invalid)
		e.run(t)
		e.wantFailed(t, ShortfallMessage)
	})
}

func TestLaterCallsReceiveEarlierQuestionsInAvoid(t *testing.T) {
	e := newWEnv(t, map[string]int{"1": 2, "2": 2})
	e.run(t)
	qs := e.questions(t)
	if len(qs) != 4 || e.gen.callCount() != 2 {
		t.Fatalf("qs=%d calls=%d", len(qs), e.gen.callCount())
	}
	first, second := e.gen.calls[0], e.gen.calls[1]
	if len(first.Avoid) != 0 || first.Marks != 1 {
		t.Errorf("first call = %+v", first)
	}
	if len(second.Avoid) != 2 {
		t.Fatalf("second call Avoid = %q, want the two 1-mark questions", second.Avoid)
	}
	for _, q := range qs {
		if q.Marks == 1 && !slices.Contains(second.Avoid, q.Text) {
			t.Errorf("question %q missing from Avoid", q.Text)
		}
	}
}

// ---- spend and budget ----

func TestSpendAddedAfterEveryCallIncludingRetries(t *testing.T) {
	e := newWEnv(t, map[string]int{"1": 4})
	u1 := Usage{Input: 2000, Output: 300}
	u2 := Usage{Input: 1100, Output: 90, CacheRead: 700}
	u3 := Usage{Input: 1500, Output: 200, CacheWrite: 300}
	seen := func(n int) { // spend of every earlier call is already added when the next starts
		if got := len(e.spendList()); got != n {
			t.Errorf("spend entries when call %d started = %d", n+1, got)
		}
	}
	e.gen.fn = seq(t,
		func(in GenInput) (GenOutput, error) {
			seen(0)
			return GenOutput{StopReason: "max_tokens", Usage: u1}, nil
		},
		func(in GenInput) (GenOutput, error) {
			seen(1)
			out := validOut(in)
			out.Usage = u2
			return out, nil
		},
		func(in GenInput) (GenOutput, error) {
			seen(2)
			out := validOut(in)
			out.Usage = u3
			return out, nil
		},
	)
	e.run(t)
	m := haiku()
	want := []int64{CostMicros(m, u1), CostMicros(m, u2), CostMicros(m, u3)}
	if got := e.spendList(); !slices.Equal(got, want) {
		t.Fatalf("spend = %v, want %v", got, want)
	}
	total := want[0] + want[1] + want[2]
	if spent, _ := e.raw.SpendThisMonth(context.Background(), "202610"); spent != total {
		t.Errorf("counter = %d, want %d", spent, total)
	}
	if j := e.job(t); j.Usage.CostMicros != total || j.Usage.Calls != 3 {
		t.Errorf("job usage = %+v", j.Usage)
	}
}

func TestFailedCallWithoutUsageChargesCountedInput(t *testing.T) {
	timeout := func(in GenInput) (GenOutput, error) { return GenOutput{}, fmt.Errorf("x: %w", ErrTimeout) }
	t.Run("counted by the provider", func(t *testing.T) {
		e := newWEnv(t, map[string]int{"1": 2})
		cg := e.withCounter(12_345)
		cg.fn = seq(t, timeout)
		e.run(t)
		e.wantFailed(t, TooLongMessage)
		want := CostMicros(haiku(), Usage{Input: 12_345})
		if got := e.spendList(); len(got) != 1 || got[0] != want {
			t.Errorf("spend = %v, want [%d]", got, want)
		}
		if j := e.job(t); j.Usage.CostMicros != want {
			t.Errorf("job cost = %d", j.Usage.CostMicros)
		}
	})
	t.Run("no counter estimates from the bytes sent", func(t *testing.T) {
		e := newWEnv(t, map[string]int{"1": 2})
		e.gen.fn = seq(t, timeout)
		e.run(t)
		// "content of notes.txt" + "content of scan.png" = 39 bytes, 4 bytes a token, rounded up.
		want := CostMicros(haiku(), Usage{Input: 10})
		if got := e.spendList(); len(got) != 1 || got[0] != want {
			t.Errorf("spend = %v, want [%d]", got, want)
		}
	})
	t.Run("usage reported with the error is used as it is", func(t *testing.T) {
		e := newWEnv(t, map[string]int{"1": 2})
		cg := e.withCounter(12_345)
		cg.fn = seq(t, func(in GenInput) (GenOutput, error) {
			return GenOutput{Usage: Usage{Input: 700}}, fmt.Errorf("x: %w", ErrTimeout)
		})
		e.run(t)
		if got := e.spendList(); len(got) != 1 || got[0] != CostMicros(haiku(), Usage{Input: 700}) {
			t.Errorf("spend = %v", got)
		}
	})
}

func TestBudgetExhaustedBeforeCallFailsJobWithBudgetMessage(t *testing.T) {
	t.Run("already used up", func(t *testing.T) {
		e := newWEnv(t, map[string]int{"1": 2})
		e.w.Cfg.BudgetMicros = 5_000
		if _, err := e.raw.AddSpend(context.Background(), "202610", 5_000); err != nil {
			t.Fatal(err)
		}
		e.run(t)
		e.wantFailed(t, BudgetMessage)
		if e.gen.callCount() != 0 {
			t.Errorf("calls = %d", e.gen.callCount())
		}
	})
	t.Run("used up mid-job by earlier calls", func(t *testing.T) {
		e := newWEnv(t, map[string]int{"1": 1, "2": 1, "3": 1})
		e.w.Cfg.BudgetMicros = CostMicros(haiku(), Usage{Input: 1000, Output: 100}) // one call's cost
		e.run(t)
		e.wantFailed(t, BudgetMessage)
		if e.gen.callCount() != 1 {
			t.Errorf("calls = %d, want 1", e.gen.callCount())
		}
		if got := e.spendList(); len(got) != 1 {
			t.Errorf("spend = %v", got)
		}
		if j := e.job(t); j.Usage.Calls != 1 || j.Usage.Requested != 3 {
			t.Errorf("usage = %+v", j.Usage)
		}
	})
	t.Run("zero budget refuses", func(t *testing.T) {
		e := newWEnv(t, map[string]int{"1": 1})
		e.w.Cfg.BudgetMicros = 0
		e.run(t)
		e.wantFailed(t, BudgetMessage)
	})
	t.Run("unreadable counter fails closed", func(t *testing.T) {
		e := newWEnv(t, map[string]int{"1": 1})
		e.jobs.spendErr = errors.New("dynamo down")
		e.run(t)
		e.wantFailed(t, GenerateFailedMessage)
		if e.gen.callCount() != 0 {
			t.Errorf("calls = %d", e.gen.callCount())
		}
	})
}

// ---- cancel and finish ----

func TestCancelBeforeFirstCallAndBetweenCalls(t *testing.T) {
	t.Run("before the first call", func(t *testing.T) {
		e := newWEnv(t, map[string]int{"1": 2})
		if err := e.raw.RequestCancel(context.Background(), "u1", e.jobID); err != nil {
			t.Fatal(err)
		}
		e.run(t)
		if j := e.job(t); j.Status != store.JobCancelled {
			t.Fatalf("status = %s", j.Status)
		}
		_, fin := e.jobs.snapshot()
		if e.gen.callCount() != 0 || !slices.Equal(fin, []string{store.JobCancelled}) {
			t.Errorf("calls=%d finishes=%v", e.gen.callCount(), fin)
		}
		e.noQuestions(t)
	})
	t.Run("between calls", func(t *testing.T) {
		e := newWEnv(t, map[string]int{"1": 1, "2": 1, "3": 1, "4": 1, "5": 1})
		e.gen.fn = func(ctx context.Context, in GenInput) (GenOutput, error) {
			_ = e.raw.RequestCancel(context.Background(), "u1", e.jobID)
			return validOut(in), nil
		}
		e.run(t)
		_, fin := e.jobs.snapshot()
		if e.job(t).Status != store.JobCancelled || e.gen.callCount() != 1 || slices.Contains(fin, store.JobDone) {
			t.Errorf("status=%s calls=%d finishes=%v", e.job(t).Status, e.gen.callCount(), fin)
		}
		e.noQuestions(t)
		if got := e.spendList(); len(got) != 1 {
			t.Errorf("the call that ran is still charged: %v", got)
		}
	})
}

func TestFinishWritesQuestionsItemBeforeDone(t *testing.T) {
	e := newWEnv(t, map[string]int{"1": 2})
	e.run(t)
	e.jobs.mu.Lock()
	defer e.jobs.mu.Unlock()
	if want := []string{"spend", "questions", "finish:done"}; !slices.Equal(e.jobs.events, want) {
		t.Errorf("events = %v, want %v", e.jobs.events, want)
	}
}

func TestDoneOnlyIfRunningAndNotCancelRequested(t *testing.T) {
	t.Run("cancel requested after the last check", func(t *testing.T) {
		e := newWEnv(t, map[string]int{"1": 2})
		e.jobs.afterPut = func() { _ = e.raw.RequestCancel(context.Background(), "u1", e.jobID) }
		e.run(t)
		if j := e.job(t); j.Status != store.JobCancelled {
			t.Fatalf("status = %s", j.Status)
		}
		_, fin := e.jobs.snapshot()
		if !slices.Equal(fin, []string{store.JobDone, store.JobCancelled}) {
			t.Errorf("finishes = %v", fin)
		}
	})
	t.Run("cancel seen before the questions are written", func(t *testing.T) {
		e := newWEnv(t, map[string]int{"1": 1})
		e.gen.fn = func(ctx context.Context, in GenInput) (GenOutput, error) {
			_ = e.raw.RequestCancel(context.Background(), "u1", e.jobID)
			return validOut(in), nil
		}
		e.run(t)
		_, fin := e.jobs.snapshot()
		if e.job(t).Status != store.JobCancelled || !slices.Equal(fin, []string{store.JobCancelled}) {
			t.Errorf("status=%s finishes=%v", e.job(t).Status, fin)
		}
		e.noQuestions(t)
	})
	t.Run("job failed by someone else meanwhile", func(t *testing.T) {
		e := newWEnv(t, map[string]int{"1": 2})
		e.jobs.afterPut = func() {
			_ = e.raw.MarkFailedIfQueuedOrRunning(context.Background(), e.jobID, "stale")
		}
		e.run(t)
		if j := e.job(t); j.Status != store.JobFailed || j.Error != "stale" {
			t.Errorf("job = %s %q: a failed job is never overwritten", j.Status, j.Error)
		}
	})
}

// ---- sources, records, logs, progress ----

func TestUnreadableSourcesLandInFailedSources(t *testing.T) {
	e := newWEnv(t, map[string]int{"1": 2})
	if err := e.docs.PutDoc(context.Background(), store.Document{
		ID: "d2", OwnerID: "u1", Name: "scan.png", Type: "TXT", ContentType: "text/plain", SizeBytes: 5,
		S3Key: "users/u1/d2/gone", Status: store.DocReady, CreatedAt: t0,
	}); err != nil {
		t.Fatal(err)
	}
	e.gen.fn = func(ctx context.Context, in GenInput) (GenOutput, error) {
		out := validOut(in)
		if len(in.Sources) != 1 {
			t.Errorf("sources = %d, want only the readable one", len(in.Sources))
		}
		out.UnreadableSources = []string{"doc-1"} // and the model could not use the one that was left
		return out, nil
	}
	e.run(t)
	j := e.job(t)
	if j.Status != store.JobDone {
		t.Fatalf("job = %s %q", j.Status, j.Error)
	}
	got := map[string]string{}
	for _, f := range j.FailedSources {
		got[f.DocID] = f.Reason
	}
	if len(got) != 2 || got["d2"] != unreadableFileReason || got["d1"] != modelSkippedReason {
		t.Errorf("failedSources = %+v", j.FailedSources)
	}
	if !j.Usage.AnySourceFailed {
		t.Error("AnySourceFailed not set")
	}
}

func TestJobRecordsPromptVersionModelEffortAndUsage(t *testing.T) {
	e := newWEnv(t, map[string]int{"1": 2, "2": 1})
	e.w.Cfg.Effort = "low" // Haiku rejects effort, so none is sent and none is recorded
	e.gen.fn = func(ctx context.Context, in GenInput) (GenOutput, error) {
		out := validOut(in)
		out.Usage = Usage{Input: 3000, Output: 400, CacheRead: 2000, CacheWrite: 500}
		return out, nil
	}
	e.run(t)
	j := e.job(t)
	m := haiku()
	one := CostMicros(m, Usage{Input: 3000, Output: 400, CacheRead: 2000, CacheWrite: 500})
	u := j.Usage
	if j.Model != "claude-haiku-4-5" || u.PromptVersion != "pv-test" || u.Effort != "" {
		t.Errorf("model=%q usage=%+v", j.Model, u)
	}
	if u.InputTokens != 6000 || u.OutputTokens != 800 || u.CacheReadTokens != 4000 || u.CacheWriteTokens != 1000 ||
		u.CostMicros != 2*one || u.Calls != 2 || u.Requested != 3 || u.Kept != 3 || u.Dropped != 0 || u.AnySourceFailed {
		t.Errorf("usage = %+v", u)
	}
	// A failed job records them too.
	e = newWEnv(t, map[string]int{"1": 1})
	e.gen.fn = seq(t, func(in GenInput) (GenOutput, error) { return GenOutput{}, ErrRateLimited })
	e.run(t)
	if u := e.job(t).Usage; u.PromptVersion != "pv-test" || u.Requested != 1 {
		t.Errorf("failed job usage = %+v", u)
	}
}

func TestProgressWrittenAfterEachCall(t *testing.T) {
	e := newWEnv(t, map[string]int{"1": 2, "2": 2}) // heartbeat interval 1 h: only call completion can write
	e.gen.fn = seq(t,
		okFn,
		func(in GenInput) (GenOutput, error) {
			waitFor(t, "progress after the first call", func() bool {
				e.jobs.mu.Lock()
				defer e.jobs.mu.Unlock()
				for _, p := range e.jobs.progress {
					if p.Done == 2 && p.Total == 4 && p.Usage.Calls == 1 && p.Usage.CostMicros > 0 {
						return true
					}
				}
				return false
			})
			return validOut(in), nil
		},
	)
	e.run(t)
	if e.job(t).Status != store.JobDone {
		t.Fatalf("job = %+v", e.job(t))
	}
}

func TestNoQuestionTextOrPromptInLogs(t *testing.T) {
	var buf bytes.Buffer
	captureLogs(t, &buf)
	e := newWEnv(t, map[string]int{"1": 3, "2": 1})
	e.setDoc(t, "d1", "notes.txt", "text/plain", []byte("PROMPT-SECRET-SOURCE-CONTENT about cells"))
	zebra := func(in GenInput, n int) GenOutput {
		out := validOut(GenInput{Type: in.Type, Marks: in.Marks, Count: n})
		for i := range out.Questions {
			out.Questions[i].Text = fmt.Sprintf("ZEBRA-QUESTION-TEXT %d about topic%d", i, flowSeq.Add(1))
			out.Questions[i].ModelAnswer = "ZEBRA-ANSWER"
		}
		return out
	}
	e.gen.fn = seq(t,
		func(in GenInput) (GenOutput, error) { // short: one dropped
			out := zebra(in, 2)
			out.Questions = append(out.Questions, badQuestion(1, "DROPPED-QUESTION-TEXT"))
			return out, nil
		},
		func(in GenInput) (GenOutput, error) { return zebra(in, in.Count), nil },
		func(in GenInput) (GenOutput, error) { return GenOutput{}, fmt.Errorf("call: %w", ErrRefusal) },
	)
	e.run(t)
	logs := buf.String()
	if !strings.Contains(logs, "job_id=job1") {
		t.Fatalf("expected worker logs, got:\n%s", logs)
	}
	for _, secret := range []string{"ZEBRA", "DROPPED-QUESTION", "PROMPT-SECRET", "ModelAnswer", "Explain topic"} {
		if strings.Contains(logs, secret) {
			t.Errorf("logs contain %q:\n%s", secret, logs)
		}
	}
	for _, field := range []string{"prompt_version=pv-test", "model=claude-haiku-4-5", "stop_reason=", "kept=", "dropped=", "latency_ms="} {
		if !strings.Contains(logs, field) {
			t.Errorf("per-call log lacks %s:\n%s", field, logs)
		}
	}
}

func captureLogs(t *testing.T, buf *bytes.Buffer) {
	t.Helper()
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(old) })
}

func TestPanicInConcurrentCallFailsJobAndKeepsSpend(t *testing.T) {
	e := newWEnv(t, map[string]int{"1": 1, "2": 1, "3": 1})
	e.gen.fn = func(ctx context.Context, in GenInput) (GenOutput, error) {
		if in.Marks == 3 {
			panic("boom in a call goroutine")
		}
		return validOut(in), nil
	}
	e.run(t) // a panic in a spawned goroutine would crash the test binary
	e.wantFailed(t, GenerateFailedMessage)
	if len(e.spendList()) < 1 {
		t.Errorf("spend of the finished calls was lost: %v", e.spendList())
	}
}

// ---- fix round 1 ----

func TestJobNeverExceeds20ProviderCalls(t *testing.T) {
	// 10 planned calls (marks 1 to 5, each 15 + 2). Every call except the very first hits
	// max_tokens on its first attempt, then needs a shortfall follow-up: 3 calls each, 28 in all.
	e := newWEnv(t, map[string]int{"1": 17, "2": 17, "3": 17, "4": 17, "5": 17})
	var mu sync.Mutex
	seen := map[[2]int]bool{}
	first := true
	e.gen.fn = func(ctx context.Context, in GenInput) (GenOutput, error) {
		mu.Lock()
		key := [2]int{in.Marks, in.Count}
		cut := (in.Count == 15 || in.Count == 2) && !seen[key] && !first
		seen[key] = true
		first = false
		mu.Unlock()
		if cut {
			return GenOutput{StopReason: "max_tokens", Usage: Usage{Input: 1000, Output: 100}}, nil
		}
		return validOut(in), nil
	}
	e.run(t)
	e.wantFailed(t, GenerateFailedMessage)
	n := e.gen.callCount()
	if n > 20 {
		t.Fatalf("Generate invoked %d times, the cap is 20", n)
	}
	if n < 20 {
		t.Errorf("Generate invoked %d times, expected the cap to be what stopped the job", n)
	}
	if got := len(e.spendList()); got != n {
		t.Errorf("spend entries = %d, want one per invocation (%d)", got, n)
	}
}

func TestTenCallJobWithoutRetriesIsUnaffectedByTheCap(t *testing.T) {
	e := newWEnv(t, map[string]int{"1": 16, "2": 16, "3": 16, "4": 16, "5": 16})
	e.gen.fn = func(ctx context.Context, in GenInput) (GenOutput, error) { return validOut(in), nil }
	e.run(t)
	if j := e.job(t); j.Status != store.JobDone || j.Done != 80 || e.gen.callCount() != 10 {
		t.Errorf("job = %s %q done=%d calls=%d", j.Status, j.Error, j.Done, e.gen.callCount())
	}
}

type panicReader struct{}

func (panicReader) Read([]byte) (int, error) { panic("entropy exploded") }

func TestPanicInsideValidationDoesNotHoldTheLock(t *testing.T) {
	e := newWEnv(t, map[string]int{"1": 1, "2": 1, "3": 1})
	e.w.idEntropy = panicReader{} // fires inside validate, in the locked section
	e.w.heartbeatInterval = 2 * time.Millisecond
	done := make(chan error, 1)
	go func() { done <- e.w.Run(context.Background(), e.jobID) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run hung: the state lock was left held by a panic")
	}
	e.wantFailed(t, GenerateFailedMessage)
	hb1, _ := e.jobs.snapshot()
	time.Sleep(20 * time.Millisecond)
	if hb2, _ := e.jobs.snapshot(); hb1 != hb2 {
		t.Errorf("heartbeat still running after Run: %d -> %d", hb1, hb2)
	}
}

func TestFakeGeneratorConcurrentCallsDoNotCollide(t *testing.T) {
	labels := map[string]store.Document{"doc-1": {ID: "d1", Name: "n"}}
	in := GenInput{Sources: []SourceFile{{Label: "doc-1"}}, Type: "mcq", Marks: 2, Count: 15, Avoid: make([]string, 15)}
	var mu sync.Mutex
	var all []RawQuestion
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			out, _ := FakeGenerator{}.Generate(context.Background(), in)
			mu.Lock()
			all = append(all, out.Questions...)
			mu.Unlock()
		}()
	}
	wg.Wait()
	kept, dropped, _ := Validate(all, CallSpec{Type: "mcq", Marks: 2}, labels, nil)
	if len(kept) != 120 || dropped != 0 {
		t.Errorf("kept=%d dropped=%d, want 120 distinct questions from 8 concurrent calls", len(kept), dropped)
	}
}

func TestInvalidOutputCallIsCharged(t *testing.T) {
	t.Run("reported usage", func(t *testing.T) {
		e := newWEnv(t, map[string]int{"1": 2})
		u := Usage{Input: 1500, Output: 40}
		e.gen.fn = seq(t,
			func(in GenInput) (GenOutput, error) { return GenOutput{Usage: u}, ErrInvalidOutput },
			okFn,
		)
		e.run(t)
		if got := e.spendList(); len(got) != 2 || got[0] != CostMicros(haiku(), u) {
			t.Errorf("spend = %v, want the invalid call charged its usage first", got)
		}
	})
	t.Run("no usage charges counted input", func(t *testing.T) {
		e := newWEnv(t, map[string]int{"1": 2})
		cg := e.withCounter(7_777)
		cg.fn = seq(t,
			func(in GenInput) (GenOutput, error) { return GenOutput{}, ErrInvalidOutput },
			okFn,
		)
		e.run(t)
		if got := e.spendList(); len(got) != 2 || got[0] != CostMicros(haiku(), Usage{Input: 7_777}) {
			t.Errorf("spend = %v", got)
		}
	})
}

func TestUnknownModelFailsBeforeAnyCall(t *testing.T) {
	var buf bytes.Buffer
	captureLogs(t, &buf)
	e := newWEnv(t, map[string]int{"1": 2})
	j := e.job(t)
	j.ID, j.Model, j.Status = "job-mystery", "mystery-model", store.JobQueued
	q := store.QuotaCheck{UserID: "u1", Day: "20261016", Month: "202610", MaxUser: 100, MaxRegen: 100, MaxGlobal: 100, BudgetMicros: 1 << 40}
	if err := e.raw.CreateJobWithQuota(context.Background(), j, q); err != nil {
		t.Fatal(err)
	}
	e.jobID = j.ID
	e.run(t)
	e.wantFailed(t, GenerateFailedMessage)
	if e.gen.callCount() != 0 {
		t.Errorf("calls = %d, a job with no price must not run", e.gen.callCount())
	}
	logs := buf.String()
	if n := strings.Count(logs, "mystery-model"); n != 1 || !strings.Contains(logs, "level=WARN msg=\"worker: model has no price") {
		t.Errorf("want one WARN naming the model, got %d mentions:\n%s", n, logs)
	}
}

func TestCountTokensHasTheCallTimeout(t *testing.T) {
	e := newWEnv(t, map[string]int{"1": 2})
	e.w.Cfg.CallTimeout = 20 * time.Millisecond
	cg := &blockingCounter{}
	e.w.Gens = FixedFactory(cg)
	e.run(t)
	e.wantFailed(t, TooLongMessage)
	if cg.callCount() != 0 {
		t.Errorf("calls = %d", cg.callCount())
	}
}

type blockingCounter struct{ testGen }

func (*blockingCounter) CountTokens(ctx context.Context, _ GenInput) (int, error) {
	<-ctx.Done()
	return 0, ctx.Err()
}

// modelOverride makes the worker see the job on another catalog model.
type modelOverride struct {
	store.JobStore
	model string
}

func (m modelOverride) GetJob(ctx context.Context, id string) (store.Job, error) {
	j, err := m.JobStore.GetJob(ctx, id)
	j.Model = m.model
	return j, err
}

func TestJobRecordsTheEffectiveEffortOfItsModel(t *testing.T) {
	for _, tc := range []struct{ model, want string }{
		{"claude-sonnet-5-5", "low"}, {"claude-opus-5-5", "low"}, {"claude-haiku-4-5", ""}, {"gemini-3.1-flash-lite", ""},
	} {
		e := newWEnv(t, map[string]int{"1": 1})
		e.w.Cfg.Effort = "low"
		e.w.Jobs = modelOverride{e.w.Jobs, tc.model}
		e.run(t)
		if got := e.job(t).Usage.Effort; got != tc.want {
			t.Errorf("%s: recorded effort %q, want %q", tc.model, got, tc.want)
		}
	}
}
