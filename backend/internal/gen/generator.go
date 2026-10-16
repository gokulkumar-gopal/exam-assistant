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

// Generator produces questions with one provider. Implementations must honour ctx
// (the worker cancels it on the job deadline, on a cancel request and when the job is no
// longer running), return the Usage actually consumed even alongside an error when they
// know it, and must never put keys or raw provider responses in an error they return:
// the worker logs err, and shows only a UserError message to the user.
type Generator interface {
	Generate(ctx context.Context, in GenInput) (GenOutput, error)
}

// GeneratorFactory returns the Generator for a catalog model id (by the model's provider).
type GeneratorFactory interface {
	For(modelID string) (Generator, error)
}

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

// UnconfiguredFactory is the placeholder factory until the provider generators exist
// (B-2.5 to B-2.7): every lookup fails with ErrGeneratorsNotConfigured.
type UnconfiguredFactory struct{}

// For always fails.
func (UnconfiguredFactory) For(string) (Generator, error) { return nil, ErrGeneratorsNotConfigured }

// FixedFactory returns the same Generator for every model. Tests and the local
// -fake-llm server use it.
type FixedFactory struct{ Generator Generator }

// For returns the fixed Generator.
func (f FixedFactory) For(string) (Generator, error) { return f.Generator, nil }
