package gen

import (
	"crypto/rand"
	"encoding/binary"
	"errors"
	"io"
	"log/slog"
	mrand "math/rand/v2"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/oklog/ulid/v2"

	"github.com/gokulkumar/exam-assistant/backend/internal/store"
)

// This file checks and cleans what the model returned (AI-spec 7.1 and 7.3). It is pure:
// no I/O, and randomness and ids come from injected sources.

// Limits of AI-spec 7.1. They count characters (Unicode code points), not bytes.
const (
	maxTextRunes        = 600
	maxOptionRunes      = 200
	maxModelAnswerRunes = 1200
	mcqOptionCount      = 4

	// dupNum/dupDen is the word-set similarity at or above which two questions are the
	// same question (AI-spec 7.1): 4/5 = 0.8.
	dupNum, dupDen = 4, 5
)

// CallSpec is what one Generate call asked for.
type CallSpec struct {
	Type  string // "mcq" | "free"
	Marks int
}

var (
	// htmlTag matches an opening, closing or self-closing tag, or a comment or doctype:
	// "<b>", "</div>", "<br/>", `<img src=x>`, "<!-- x -->". A bare "<" in "3 < 5" is not one.
	htmlTag = regexp.MustCompile(`(?s)</?[A-Za-z][A-Za-z0-9:-]*(?:\s[^<>]*)?/?>|<![^>]*>|<\?[^>]*\?>`)
	// Entities such as &lt; are plain text and are kept as they are (AI-spec 7.1 asks only for
	// "no HTML tags or Markdown fences"); nothing here decodes them.
)

// plainOK reports whether s has no HTML tag and no Markdown code fence.
func plainOK(s string) bool {
	return !strings.Contains(s, "```") && !strings.Contains(s, "~~~") && !htmlTag.MatchString(s)
}

// cleanField strips control and invisible characters (CleanText) and trims.
func cleanField(s string) string { return strings.TrimSpace(CleanText(s)) }

// Validate applies the per-question checks of AI-spec 7.1 to one call's output and returns the
// questions that passed, in input order, with new ULIDs, SourceDocID and SourceDocName mapped
// from labels, and cleaned text. dropped is the number of questions removed (each counted
// once). avoid holds texts of questions already in the job or the exam: a question that
// duplicates one, or an earlier kept question of this call, is dropped (the first one stays).
//
// Control and invisible characters are stripped, not grounds for dropping. A question whose
// shape does not match req.Type (an MCQ with a model answer, free text with options or a
// correctIndex) fails the "correct type" check. The model's own ids are never read.
func Validate(raw []RawQuestion, req CallSpec, labels map[string]store.Document, avoid []string) (kept []store.Question, dropped int) {
	return validate(raw, req, labels, avoid, func() string { return ulid.Make().String() })
}

func validate(raw []RawQuestion, req CallSpec, labels map[string]store.Document, avoid []string, newID func() string) (kept []store.Question, dropped int) {
	idx := newDupIndex(len(avoid) + len(raw))
	for _, a := range avoid {
		idx.add(a)
	}
	for _, r := range raw {
		c, ok := checkQuestion(r, req, labels)
		if ok && idx.isDup(c.Text) {
			ok = false
		}
		if !ok {
			dropped++
			continue
		}
		idx.add(c.Text)
		q, _ := c.toQuestion(labels, newID) // the label was checked
		kept = append(kept, q)
	}
	return kept, dropped
}

// checkQuestion returns the cleaned question and whether it passes every check except
// duplicates.
func checkQuestion(r RawQuestion, req CallSpec, labels map[string]store.Document) (RawQuestion, bool) {
	if r.Marks != req.Marks {
		return r, false
	}
	if _, ok := labels[r.Source]; !ok {
		return r, false
	}
	c := RawQuestion{Marks: r.Marks, Source: r.Source, Text: cleanField(r.Text), ModelAnswer: cleanField(r.ModelAnswer)}
	if c.Text == "" || utf8.RuneCountInString(c.Text) > maxTextRunes || !plainOK(c.Text) {
		return r, false
	}
	switch req.Type {
	case "mcq":
		if c.ModelAnswer != "" || len(r.Options) != mcqOptionCount || r.CorrectIndex == nil ||
			*r.CorrectIndex < 0 || *r.CorrectIndex >= mcqOptionCount {
			return r, false
		}
		seen := map[string]bool{}
		for _, o := range r.Options {
			o = cleanField(o)
			key := strings.ToLower(o)
			if o == "" || utf8.RuneCountInString(o) > maxOptionRunes || !plainOK(o) || seen[key] {
				return r, false
			}
			seen[key] = true
			c.Options = append(c.Options, o)
		}
		c.CorrectIndex = r.CorrectIndex
	case "free":
		if len(r.Options) != 0 || r.CorrectIndex != nil {
			return r, false
		}
		if c.ModelAnswer == "" || utf8.RuneCountInString(c.ModelAnswer) > maxModelAnswerRunes || !plainOK(c.ModelAnswer) {
			return r, false
		}
	default:
		return r, false
	}
	return c, true
}

// newIDSource returns a generator of ULIDs from the given clock and entropy (crypto/rand when
// entropy is nil). It is safe for concurrent use and strictly increasing within a millisecond.
func newIDSource(now func() time.Time, entropy io.Reader) func() string {
	if entropy == nil {
		entropy = rand.Reader
	}
	mono := ulid.Monotonic(entropy, 0)
	var mu sync.Mutex
	return func() string {
		mu.Lock()
		defer mu.Unlock()
		id, err := ulid.New(ulid.Timestamp(now()), mono)
		if err != nil { // entropy failed or overflowed: fall back to the library default
			return ulid.Make().String()
		}
		return id.String()
	}
}

// ---- duplicates ----

// words returns the lowercase words of s (runs of letters and digits), sorted and unique.
func words(s string) []string {
	w := strings.FieldsFunc(strings.ToLower(s), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
	slices.Sort(w)
	return slices.Compact(w)
}

// normKey is the normalised form two texts must share to be exact duplicates: their words
// (lowercase, any punctuation and spacing ignored, order and repeats ignored). A text with
// no words at all falls back to its lowercase, whitespace-collapsed self.
func normKey(w []string, s string) string {
	if len(w) == 0 {
		return strings.Join(strings.Fields(strings.ToLower(s)), " ")
	}
	return strings.Join(w, " ")
}

// similar reports Jaccard similarity of two word sets >= 0.8, in integers: inter/union >= 4/5.
func similar(inter, a, b int) bool {
	union := a + b - inter
	return union > 0 && dupDen*inter >= dupNum*union
}

// NearDuplicate reports whether two question texts are the same question for AI-spec 7.1:
// their normalised texts are equal (lowercase, punctuation and spacing ignored; see words), or
// the Jaccard similarity of their lowercase word sets, |A and B| / |A or B|, is at least 0.8
// (inclusive: 4 shared words of 5 is a duplicate, 15 of 19 is not).
func NearDuplicate(a, b string) bool {
	wa, wb := words(a), words(b)
	if normKey(wa, a) == normKey(wb, b) {
		return true
	}
	return similar(interSize(wa, wb), len(wa), len(wb))
}

// dupIndex answers "is this text a NearDuplicate of one already added" without comparing
// every pair. Exact normalised matches are one map lookup. For similarity: a match B of a set
// A with Jaccard >= 0.8 must contain at least ceil(0.8*|A|) of A's words, so it must contain
// at least one word of any p = |A| - ceil(0.8*|A|) + 1 words of A. The index looks B up
// through the p words of A with the shortest posting lists (the rarest), then checks only
// those candidates exactly. Common words ("the", "of") are never the lookup key unless a
// question is made of nothing else.
type dupIndex struct {
	keys     map[string]struct{}
	sets     [][]string
	postings map[string][]int32
	seen     []bool
	cand     []int32
	order    []string
}

func newDupIndex(hint int) *dupIndex {
	return &dupIndex{keys: make(map[string]struct{}, hint), postings: map[string][]int32{}}
}

func (d *dupIndex) isDup(s string) bool {
	w := words(s)
	if _, ok := d.keys[normKey(w, s)]; ok {
		return true
	}
	if len(w) == 0 {
		return false
	}
	p := len(w) - (dupNum*len(w)+dupDen-1)/dupDen + 1
	d.order = append(d.order[:0], w...)
	slices.SortFunc(d.order, func(x, y string) int { return len(d.postings[x]) - len(d.postings[y]) })
	d.cand = d.cand[:0]
	for _, word := range d.order[:p] {
		for _, i := range d.postings[word] {
			if !d.seen[i] {
				d.seen[i] = true
				d.cand = append(d.cand, i)
			}
		}
	}
	dup := false
	for _, i := range d.cand {
		d.seen[i] = false
		if !dup && similar(interSize(w, d.sets[i]), len(w), len(d.sets[i])) {
			dup = true
		}
	}
	return dup
}

// interSize is the size of the intersection of two sorted, unique word lists.
func interSize(a, b []string) int {
	n := 0
	for i, j := 0, 0; i < len(a) && j < len(b); {
		switch c := strings.Compare(a[i], b[j]); {
		case c == 0:
			n++
			i++
			j++
		case c < 0:
			i++
		default:
			j++
		}
	}
	return n
}

func (d *dupIndex) add(s string) {
	w := words(s)
	d.keys[normKey(w, s)] = struct{}{}
	i := int32(len(d.sets))
	d.sets = append(d.sets, w)
	d.seen = append(d.seen, false)
	for _, word := range w {
		d.postings[word] = append(d.postings[word], i)
	}
}

// ---- shuffle and order ----

// maxRejections bounds the rejection loop of randInt so a broken source cannot spin forever.
const maxRejections = 128

// randInt returns a uniform integer in [0, n) read from r, without modulo bias: a 64-bit
// draw below 2^64 mod n is rejected and redrawn, so every residue is equally likely.
func randInt(r io.Reader, n int) (int, error) {
	if n <= 0 {
		return 0, errors.New("gen: randInt: n must be positive")
	}
	un := uint64(n)
	threshold := (-un) % un // 2^64 mod n
	var buf [8]byte
	for i := 0; i < maxRejections; i++ {
		if _, err := io.ReadFull(r, buf[:]); err != nil {
			return 0, err
		}
		if v := binary.BigEndian.Uint64(buf[:]); v >= threshold {
			return int(v % un), nil
		}
	}
	return 0, errors.New("gen: randInt: too many rejected draws")
}

// permute applies a Fisher-Yates shuffle over r through swap. If r fails midway it logs and
// finishes with math/rand, so a shuffle never fails and never drops anything.
func permute(r io.Reader, n int, swap func(i, j int)) {
	if r == nil {
		r = rand.Reader
	}
	failed := false
	for i := n - 1; i > 0; i-- {
		var j int
		if !failed {
			var err error
			if j, err = randInt(r, i+1); err != nil {
				slog.Error("gen: random source failed; using math/rand for the rest of this shuffle", "err", err)
				failed = true
			}
		}
		if failed {
			j = mrand.IntN(i + 1)
		}
		swap(i, j)
	}
}

// ShuffleOptions shuffles an MCQ's options with r (crypto/rand when r is nil) and updates
// CorrectIndex so it still points at the same option text (AI-spec 7.3). A question without
// options, or without a valid CorrectIndex, is left alone: the right answer cannot be tracked.
func ShuffleOptions(q *store.Question, r io.Reader) {
	n := len(q.Options)
	if n < 2 || q.CorrectIndex == nil || *q.CorrectIndex < 0 || *q.CorrectIndex >= n {
		return
	}
	perm := make([]int, n) // perm[i] is the old index of the option now at i
	for i := range perm {
		perm[i] = i
	}
	permute(r, n, func(i, j int) { perm[i], perm[j] = perm[j], perm[i] })
	opts := make([]string, n)
	correct := *q.CorrectIndex
	for i, old := range perm {
		opts[i] = q.Options[old]
		if old == *q.CorrectIndex {
			correct = i
		}
	}
	q.Options = opts
	q.CorrectIndex = &correct
}

// OrderQuestions returns a copy of qs in final order (AI-spec 7.3): a stable sort ascending by
// marks, or, when shuffle is true, a uniformly random order drawn from r (crypto/rand when nil).
func OrderQuestions(qs []store.Question, shuffle bool, r io.Reader) []store.Question {
	out := slices.Clone(qs)
	if shuffle {
		permute(r, len(out), func(i, j int) { out[i], out[j] = out[j], out[i] })
		return out
	}
	slices.SortStableFunc(out, func(a, b store.Question) int { return a.Marks - b.Marks })
	return out
}
