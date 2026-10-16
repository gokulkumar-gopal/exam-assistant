package gen

import (
	"context"
	"fmt"
	"sync/atomic"
)

// fakeSeq numbers fake questions across the whole process, so calls running at the same time
// (up to three) never produce the same words and Validate's duplicate check keeps them all.
var fakeSeq atomic.Int64

// FakeGenerator returns deterministic questions with no provider call. It backs worker
// tests and the local server's -fake-llm flag; cmd/lambda never references it.
type FakeGenerator struct{}

// Generate returns exactly in.Count questions of in.Marks, citing the sources in turn.
func (FakeGenerator) Generate(ctx context.Context, in GenInput) (GenOutput, error) {
	if err := ctx.Err(); err != nil {
		return GenOutput{}, err
	}
	out := GenOutput{StopReason: "end_turn", RequestID: "fake"}
	for i := 0; i < in.Count; i++ {
		n := fakeSeq.Add(1)
		q := RawQuestion{
			Marks: in.Marks,
			// Five words unique to the question keep any two apart under the 0.8 word-set duplicate check.
			Text: fmt.Sprintf("Sample %d-mark question: explain topic%d, detail%d, aspect%d, angle%d and idea%d", in.Marks, n, n, n, n, n),
		}
		if len(in.Sources) > 0 {
			q.Source = in.Sources[i%len(in.Sources)].Label
		}
		if in.Type == "mcq" {
			correct := i % 4
			q.Options = []string{"Option A", "Option B", "Option C", "Option D"}
			q.CorrectIndex = &correct
		} else {
			q.ModelAnswer = "A sample model answer."
		}
		out.Questions = append(out.Questions, q)
	}
	out.Usage = Usage{Input: 1000, Output: int64(100 * in.Count)}
	return out, nil
}

// CountTokens implements TokenCounter trivially: one token per four bytes of sources.
func (FakeGenerator) CountTokens(_ context.Context, in GenInput) (int, error) {
	n := 0
	for _, s := range in.Sources {
		n += len(s.Bytes)
	}
	return n / 4, nil
}
