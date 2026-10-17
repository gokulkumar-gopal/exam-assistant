package gen

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/lambda"
	"github.com/aws/aws-sdk-go-v2/service/lambda/types"

	"github.com/gokulkumar/exam-assistant/backend/internal/store"
)

func TestInProcessRunnerStartReturnsImmediatelyAndRunsJob(t *testing.T) {
	e := newWEnv(t, map[string]int{"1": 2})
	release := make(chan struct{})
	e.gen.fn = func(ctx context.Context, in GenInput) (GenOutput, error) {
		<-release
		return defaultOut(in), nil
	}
	r := &InProcessRunner{Worker: e.w}
	if err := r.Start(context.Background(), e.jobID); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "job running", func() bool { return e.job(t).Status == store.JobRunning })
	close(release)
	r.Wait()
	if e.job(t).Status != store.JobDone {
		t.Errorf("status = %s", e.job(t).Status)
	}
}

func TestInProcessRunnerSurvivesRequestContextCancel(t *testing.T) {
	e := newWEnv(t, map[string]int{"1": 2})
	var seen error
	e.gen.fn = func(ctx context.Context, in GenInput) (GenOutput, error) {
		time.Sleep(20 * time.Millisecond)
		seen = ctx.Err()
		return defaultOut(in), nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	r := &InProcessRunner{Worker: e.w}
	if err := r.Start(ctx, e.jobID); err != nil {
		t.Fatal(err)
	}
	cancel() // the HTTP request ends right after 202
	r.Wait()
	if seen != nil || e.job(t).Status != store.JobDone {
		t.Errorf("ctx err = %v, status = %s", seen, e.job(t).Status)
	}
}

func TestInProcessRunnerPanicFailsJob(t *testing.T) {
	e := newWEnv(t, map[string]int{"1": 2})
	e.gen.fn = func(ctx context.Context, in GenInput) (GenOutput, error) { panic("boom") }
	r := &InProcessRunner{Worker: e.w}
	if err := r.Start(context.Background(), e.jobID); err != nil {
		t.Fatal(err)
	}
	r.Wait()
	if j := e.job(t); j.Status != store.JobFailed || j.Error != GenerateFailedMessage {
		t.Errorf("job = %s %q", j.Status, j.Error)
	}
}

type fakeLambda struct {
	in  *lambda.InvokeInput
	out *lambda.InvokeOutput
	err error
}

func (f *fakeLambda) Invoke(_ context.Context, in *lambda.InvokeInput, _ ...func(*lambda.Options)) (*lambda.InvokeOutput, error) {
	f.in = in
	if f.err != nil {
		return nil, f.err
	}
	if f.out != nil {
		return f.out, nil
	}
	return &lambda.InvokeOutput{StatusCode: 202}, nil
}

func TestLambdaRunnerInvokesAsyncWithJobID(t *testing.T) {
	c := &fakeLambda{}
	r := LambdaRunner{Client: c, FunctionName: "exam-assistant-dev-worker"}
	if err := r.Start(context.Background(), "01JOB"); err != nil {
		t.Fatal(err)
	}
	if c.in == nil || *c.in.FunctionName != "exam-assistant-dev-worker" || c.in.InvocationType != types.InvocationTypeEvent {
		t.Fatalf("input = %+v", c.in)
	}
	var ev map[string]string
	if err := json.Unmarshal(c.in.Payload, &ev); err != nil || len(ev) != 1 || ev["jobId"] != "01JOB" {
		t.Errorf("payload = %s (%v)", c.in.Payload, err)
	}
}

func TestLambdaRunnerErrorPropagates(t *testing.T) {
	boom := errors.New("AccessDenied")
	r := LambdaRunner{Client: &fakeLambda{err: boom}, FunctionName: "f"}
	if err := r.Start(context.Background(), "j"); !errors.Is(err, boom) {
		t.Errorf("err = %v", err)
	}
}

func TestLambdaRunnerRejectsUnexpectedStatus(t *testing.T) {
	r := LambdaRunner{Client: &fakeLambda{out: &lambda.InvokeOutput{StatusCode: 200}}, FunctionName: "f"}
	if err := r.Start(context.Background(), "j"); err == nil {
		t.Error("a non-202 answer to an Event invoke must be an error")
	}
}

func TestLambdaRunnerNeedsFunctionName(t *testing.T) {
	if err := (LambdaRunner{Client: &fakeLambda{}}).Start(context.Background(), "j"); err == nil {
		t.Error("expected error for empty function name")
	}
}

func TestFakeGeneratorHonoursCountsAndNeverRepeatsATextAcrossCalls(t *testing.T) {
	g := FakeGenerator{}
	in := GenInput{Type: "mcq", Marks: 2, Count: 3, Sources: []SourceFile{{Label: "doc-1"}, {Label: "doc-2"}}}
	a, err := g.Generate(context.Background(), in)
	if err != nil || len(a.Questions) != 3 {
		t.Fatal(a, err)
	}
	b, _ := g.Generate(context.Background(), in)
	for i, q := range a.Questions {
		// Fix round 1: texts are numbered process-wide so concurrent calls never collide, which
		// means the same input no longer gives the same text.
		if q.Text == b.Questions[i].Text || q.Marks != 2 || len(q.Options) != 4 || q.CorrectIndex == nil || q.Source == "" {
			t.Errorf("question %d = %+v", i, q)
		}
	}
	f, _ := g.Generate(context.Background(), GenInput{Type: "free", Marks: 4, Count: 1, Sources: in.Sources})
	if len(f.Questions) != 1 || f.Questions[0].ModelAnswer == "" || len(f.Questions[0].Options) != 0 {
		t.Errorf("free = %+v", f.Questions)
	}
}

func TestFakeCountTokensCountsPDFByPagesNotBytes(t *testing.T) {
	g := FakeGenerator{}
	count := func(srcs ...SourceFile) int {
		n, err := g.CountTokens(context.Background(), GenInput{Sources: srcs})
		if err != nil {
			t.Fatal(err)
		}
		return n
	}
	pdf := testPDF(50)
	if got, want := count(SourceFile{ContentType: "application/pdf", Bytes: pdf}), 50*fakeTokensPerPDFPage; got != want {
		t.Errorf("50-page PDF = %d tokens, want %d", got, want)
	}
	// Many large PDFs by size would pass 200,000 tokens at one token per four bytes; by pages they do not.
	big := SourceFile{ContentType: "application/pdf", Bytes: append(append([]byte{}, pdf...), make([]byte, 4<<20)...)}
	if got := count(big); got > 100*fakeTokensPerPDFPage {
		t.Errorf("padded PDF = %d tokens, want a page-based count", got)
	}
	if got := count(SourceFile{ContentType: "image/png", Bytes: make([]byte, 3<<20)}); got != fakeTokensPerImage {
		t.Errorf("image = %d tokens, want %d", got, fakeTokensPerImage)
	}
	if got := count(SourceFile{ContentType: "text/plain", Bytes: make([]byte, 400)}); got != 100 {
		t.Errorf("400 bytes of text = %d tokens, want 100", got)
	}
	if got := count(SourceFile{ContentType: "application/pdf", Bytes: []byte("not a pdf at all..")}); got != 4 {
		t.Errorf("unreadable PDF = %d tokens, want bytes/4 = 4", got)
	}
}
