package gen

import (
	"context"
	"errors"

	"github.com/oklog/ulid/v2"

	"github.com/gokulkumar/exam-assistant/backend/internal/store"
)

// This file is the Generator contract of AI-spec 4 and backend-spec 7.1. The worker owns
// the job lifecycle (claim, heartbeat, deadline, finish) and calls Generate once per
// mark value and chunk; implementations (B-2.5 onward) only talk to one provider.

// SourceFile is one prepared source: a label the model sees ("doc-1", ...), the content
// type and the bytes, already preprocessed (AI-spec 5.1). It never carries a file name or
// one of our IDs (AI-spec 5.2).
type SourceFile struct {
	Label       string
	ContentType string
	Bytes       []byte
}

// GenInput is one Generate call (AI-spec 4).
type GenInput struct {
	Sources []SourceFile
	Type    string   // "mcq" | "free"
	Marks   int      // 1..5
	Count   int      // 1..15 (the worker chunks larger requests)
	Avoid   []string // texts of questions already produced or already in the exam
	Model   string   // catalog id; the factory picks the provider from it
}

// RawQuestion is one question as the model returned it (AI-spec 6.1): no ID, no file name,
// and Source is the label the model cited ("doc-1"), not a document ID. The worker turns
// it into a store.Question with ToQuestion and drops one with an unknown label (AI-spec 7).
type RawQuestion struct {
	Marks        int      `json:"marks"`
	Text         string   `json:"text"`
	Options      []string `json:"options,omitempty"`      // MCQ only
	CorrectIndex *int     `json:"correctIndex,omitempty"` // MCQ only; nil when absent
	ModelAnswer  string   `json:"modelAnswer,omitempty"`  // free text only
	Source       string   `json:"source"`
}

// ToQuestion builds the stored Question from a RawQuestion (AI-spec 6.2): it assigns a new
// ID and maps Source back to the document ID and a snapshot of its name. ok is false when
// the label is not in labelToDoc, and the caller drops the question. The Options slice is
// copied so the result never aliases the generator's output.
func (r RawQuestion) ToQuestion(labelToDoc map[string]store.Document) (q store.Question, ok bool) {
	return r.toQuestion(labelToDoc, func() string { return ulid.Make().String() })
}

// toQuestion is ToQuestion with the id source injected (the worker passes its clock and entropy).
func (r RawQuestion) toQuestion(labelToDoc map[string]store.Document, newID func() string) (q store.Question, ok bool) {
	d, ok := labelToDoc[r.Source]
	if !ok {
		return store.Question{}, false
	}
	q = store.Question{
		ID: newID(), Marks: r.Marks, Text: r.Text, ModelAnswer: r.ModelAnswer,
		SourceDocID: d.ID, SourceDocName: d.Name,
	}
	if r.Options != nil {
		q.Options = append([]string(nil), r.Options...)
	}
	if r.CorrectIndex != nil {
		c := *r.CorrectIndex
		q.CorrectIndex = &c
	}
	return q, true
}

// GenOutput is the result of one Generate call (AI-spec 4).
type GenOutput struct {
	Questions         []RawQuestion
	UnreadableSources []string // source labels the model could not read
	Usage             Usage    // tokens of this call, for spend accounting
	StopReason        string
	RequestID         string
}

// The generator error contract. Generate wraps one of these sentinels (fmt.Errorf("...: %w",
// ErrX) or errors.Join) so the worker can pick the user's message and the retry rule without
// knowing any provider SDK type. Provider adapters (B-2.8, B-2.9) map their errors as follows
// and do their own transport retries first (AI-spec 8: SDK max retries 1):
//
//	ErrRateLimited   HTTP 429, or 5xx / overloaded, still failing after the adapter's retries.
//	                 Job message "The AI service is busy. Try again in a minute."
//	ErrTimeout       the call exceeded its timeout (after the one retry). The worker also
//	                 applies WorkerConfig.CallTimeout through ctx and treats a call whose own
//	                 deadline passed as ErrTimeout. Job message "That took too long. Try again."
//	ErrAuth          HTTP 401 or 403 (bad or missing key): a deployment error. The job gets the
//	                 generic message; the details are logged at error level only.
//	ErrRefusal       stop reason "refusal" (the model's safety checks declined). Returning
//	                 GenOutput.StopReason == "refusal" with a nil error is equivalent. The
//	                 batch fails with "We couldn't generate questions from these files."
//	                 and is never retried.
//	ErrMaxTokens     stop reason "max_tokens" (output cut off); GenOutput.StopReason ==
//	                 "max_tokens" is equivalent. The worker retries that call once with half
//	                 the count and fails the job if it repeats. Any questions in the output
//	                 are ignored: a cut-off response is not trusted.
//	ErrInvalidOutput the response is not valid JSON or fails the schema. The questions count
//	                 as missing and the shortfall follow-up (AI-spec 7.4) applies. Return any
//	                 questions that did parse in GenOutput.Questions, or none.
//
// Any other error fails the job with the generic message and is logged.
var (
	ErrRateLimited   = errors.New("gen: provider is rate limited or unavailable")
	ErrTimeout       = errors.New("gen: provider call timed out")
	ErrAuth          = errors.New("gen: provider rejected the credentials")
	ErrRefusal       = errors.New("gen: model refused the request")
	ErrMaxTokens     = errors.New("gen: model output was cut off (max_tokens)")
	ErrInvalidOutput = errors.New("gen: model output is not valid")
)

// Stop reasons the worker acts on (GenOutput.StopReason).
const (
	StopRefusal   = "refusal"
	StopMaxTokens = "max_tokens"
)

// Generator produces questions with one provider. Implementations must honour ctx
// (the worker cancels it on the job deadline, on a cancel request, when the job is no
// longer running and after the per-call timeout), return the Usage actually consumed even
// alongside an error when they know it (the worker charges the counted input of a failed
// call that reports none, AI-spec 10.4), map provider failures onto the sentinel errors
// above, may be called from up to three goroutines at once, and must never put keys, prompts
// or raw provider responses in an error they return: the worker logs err, and shows only a
// fixed message to the user.
type Generator interface {
	Generate(ctx context.Context, in GenInput) (GenOutput, error)
}

// TokenCounter is optionally implemented by a Generator. CountTokens returns the input
// tokens the provider counts for in (the shared prefix: sources and instructions), without
// generating anything. The worker calls it once before the first call to enforce the
// input-token cap (AI-spec 10.2) and uses the count as the charge of a failed call that
// reports no usage (AI-spec 10.4). Without it the worker estimates from the bytes sent.
type TokenCounter interface {
	CountTokens(ctx context.Context, in GenInput) (int, error)
}

// GeneratorFactory returns the Generator for a catalog model id (by the model's provider).
type GeneratorFactory func(modelID string) (Generator, error)

// UserError is a generator failure whose Message is safe to show to the user (the
// AI-spec 8 messages). Any other error becomes the generic GenerateFailedMessage.
type UserError struct {
	Message string
	Err     error
}

func (e *UserError) Error() string {
	if e.Err != nil {
		return e.Message + ": " + e.Err.Error()
	}
	return e.Message
}

func (e *UserError) Unwrap() error { return e.Err }

// ErrGeneratorsNotConfigured is returned by UnconfiguredFactory.
var ErrGeneratorsNotConfigured = errors.New("generators not configured")

// UnconfiguredFactory is the placeholder factory until the provider generators are wired
// (B-2.8, B-2.9): every lookup fails with ErrGeneratorsNotConfigured.
func UnconfiguredFactory(string) (Generator, error) { return nil, ErrGeneratorsNotConfigured }

// FixedFactory returns a factory that gives the same Generator for every model. Tests and
// the local -fake-llm server use it.
func FixedFactory(g Generator) GeneratorFactory {
	return func(string) (Generator, error) { return g, nil }
}
