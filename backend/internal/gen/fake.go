package gen

import (
	"context"
	"fmt"
)

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
		n := len(in.Avoid) + i
		q := RawQuestion{
			Marks: in.Marks,
			// Index-specific words keep the questions apart under the 0.8 word-set duplicate check.
			Text: fmt.Sprintf("Sample %d-mark question %d (%d already asked): explain topic%d, detail%d, aspect%d and angle%d",
				in.Marks, i+1, len(in.Avoid), n, n, n, n),
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
