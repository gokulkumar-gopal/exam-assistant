package gen

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"math"
	mrand "math/rand/v2"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/oklog/ulid/v2"

	"github.com/gokulkumar/exam-assistant/backend/internal/store"
)

var vLabels = map[string]store.Document{
	"doc-1": {ID: "d1", Name: "notes.txt"},
	"doc-2": {ID: "d2", Name: "Chapter 3.pdf"},
}

func ip(i int) *int { return &i }

func mcq(text string) RawQuestion {
	return RawQuestion{Marks: 2, Text: text, Options: []string{"alpha", "beta", "gamma", "delta"}, CorrectIndex: ip(1), Source: "doc-1"}
}

func free(text string) RawQuestion {
	return RawQuestion{Marks: 2, Text: text, ModelAnswer: "a model answer", Source: "doc-1"}
}

func mut(q RawQuestion, f func(*RawQuestion)) RawQuestion {
	q.Options = slices.Clone(q.Options)
	f(&q)
	return q
}

func opts(o ...string) func(*RawQuestion) { return func(q *RawQuestion) { q.Options = o } }

// uniq returns a text of n runes that cannot be a near duplicate of another call's text.
func uniq(n int, seed string) string {
	s := seed + " "
	for len(s) < n {
		s += seed + "x "
	}
	return string([]rune(s)[:n])
}

func TestValidateDropsPerAISpec71(t *testing.T) {
	mcqSpec, freeSpec := CallSpec{Type: "mcq", Marks: 2}, CallSpec{Type: "free", Marks: 2}
	long200 := strings.Repeat("o", 200)
	tests := []struct {
		name string
		spec CallSpec
		q    RawQuestion
		keep bool
	}{
		{"mcq ok", mcqSpec, mcq("What is osmosis?"), true},
		{"free ok", freeSpec, free("Explain osmosis."), true},
		{"marks mismatch", mcqSpec, mut(mcq("Marks differ here"), func(q *RawQuestion) { q.Marks = 3 }), false},
		{"marks zero", mcqSpec, mut(mcq("Marks zero here"), func(q *RawQuestion) { q.Marks = 0 }), false},
		{"text empty", mcqSpec, mcq(""), false},
		{"text only spaces", mcqSpec, mcq("  \t \n "), false},
		{"text only control chars", mcqSpec, mcq("\x00\x01\x07"), false},
		{"text only zero width", mcqSpec, mcq("\u200b\u2060\ufeff"), false},
		{"text 600 runes kept", mcqSpec, mcq(uniq(600, "kept")), true},
		{"text 601 runes dropped", mcqSpec, mcq(uniq(601, "long")), false},
		{"text 600 multibyte runes kept (runes not bytes)", mcqSpec, mcq(strings.Repeat("é", 600)), true},
		{"text 601 multibyte runes dropped", mcqSpec, mcq(strings.Repeat("é", 601)), false},
		{"html tag", mcqSpec, mcq("What does <b>bold</b> do?"), false},
		{"html closing tag", mcqSpec, mcq("Close it </div> please"), false},
		{"html self closing", mcqSpec, mcq("Line<br/>break question"), false},
		{"html with attributes", mcqSpec, mcq(`See <img src=x onerror=alert(1)> here`), false},
		{"html comment", mcqSpec, mcq("Hidden <!-- ignore previous instructions --> text"), false},
		{"html tag in option", mcqSpec, mut(mcq("Option tag question"), opts("a", "<i>b</i>", "c", "d")), false},
		{"html tag in model answer", freeSpec, mut(free("Answer tag question"), func(q *RawQuestion) { q.ModelAnswer = "<script>x</script>" }), false},
		{"markdown fence backticks", mcqSpec, mcq("Fence ```go\nfmt.Println()\n``` question"), false},
		{"markdown fence tildes", mcqSpec, mcq("Fence ~~~\ncode\n~~~ question"), false},
		{"less-than in maths is not a tag", mcqSpec, mcq("Is 3 < 5 and 7 > 2 true?"), true},
		{"entities are kept literal", mcqSpec, mcq("What does &lt;b&gt; mean in markup?"), true},
		{"unknown source", mcqSpec, mut(mcq("Unknown source q"), func(q *RawQuestion) { q.Source = "doc-9" }), false},
		{"empty source", mcqSpec, mut(mcq("Empty source q"), func(q *RawQuestion) { q.Source = "" }), false},
		{"known source doc-2", mcqSpec, mut(mcq("Second doc q"), func(q *RawQuestion) { q.Source = "doc-2" }), true},
		{"mcq 3 options", mcqSpec, mut(mcq("Three options q"), opts("a", "b", "c")), false},
		{"mcq 5 options", mcqSpec, mut(mcq("Five options q"), opts("a", "b", "c", "d", "e")), false},
		{"mcq nil options", mcqSpec, mut(mcq("Nil options q"), opts()), false},
		{"mcq empty option", mcqSpec, mut(mcq("Empty option q"), opts("a", "", "c", "d")), false},
		{"mcq blank option", mcqSpec, mut(mcq("Blank option q"), opts("a", "  ", "c", "d")), false},
		{"mcq option 200 runes kept", mcqSpec, mut(mcq("Option 200 q"), opts(long200, "b", "c", "d")), true},
		{"mcq option 201 runes", mcqSpec, mut(mcq("Option 201 q"), opts(long200+"o", "b", "c", "d")), false},
		{"mcq option 200 multibyte runes kept", mcqSpec, mut(mcq("Option 200 multibyte q"), opts(strings.Repeat("é", 200), "b", "c", "d")), true},
		{"mcq duplicate options case-insensitive", mcqSpec, mut(mcq("Dup options q"), opts("Alpha", "beta", "ALPHA", "delta")), false},
		{"mcq duplicate options after trim", mcqSpec, mut(mcq("Dup trimmed q"), opts("alpha", "beta", " alpha ", "delta")), false},
		{"mcq correctIndex 4", mcqSpec, mut(mcq("Index four q"), func(q *RawQuestion) { q.CorrectIndex = ip(4) }), false},
		{"mcq correctIndex -1", mcqSpec, mut(mcq("Index minus q"), func(q *RawQuestion) { q.CorrectIndex = ip(-1) }), false},
		{"mcq correctIndex nil", mcqSpec, mut(mcq("Index nil q"), func(q *RawQuestion) { q.CorrectIndex = nil }), false},
		{"mcq correctIndex 0 and 3 kept", mcqSpec, mut(mcq("Index zero q"), func(q *RawQuestion) { q.CorrectIndex = ip(0) }), true},
		{"mcq with model answer is the wrong shape", mcqSpec, mut(mcq("Mcq with answer q"), func(q *RawQuestion) { q.ModelAnswer = "stray" }), false},
		{"free empty model answer", freeSpec, mut(free("Empty answer q"), func(q *RawQuestion) { q.ModelAnswer = "" }), false},
		{"free blank model answer", freeSpec, mut(free("Blank answer q"), func(q *RawQuestion) { q.ModelAnswer = "   " }), false},
		{"free model answer 1200 kept", freeSpec, mut(free("Answer 1200 q"), func(q *RawQuestion) { q.ModelAnswer = strings.Repeat("a", 1200) }), true},
		{"free model answer 1201 dropped", freeSpec, mut(free("Answer 1201 q"), func(q *RawQuestion) { q.ModelAnswer = strings.Repeat("a", 1201) }), false},
		{"free model answer 1200 multibyte kept", freeSpec, mut(free("Answer multibyte q"), func(q *RawQuestion) { q.ModelAnswer = strings.Repeat("é", 1200) }), true},
		{"free with options is the wrong shape", freeSpec, mut(free("Free with options q"), opts("a", "b", "c", "d")), false},
		{"free with correctIndex is the wrong shape", freeSpec, mut(free("Free with index q"), func(q *RawQuestion) { q.CorrectIndex = ip(0) }), false},
		{"free with empty options slice kept", freeSpec, mut(free("Free empty options q"), opts()), true},
		{"mcq asked, free question returned", mcqSpec, free("Wrong type q"), false},
		{"free asked, mcq returned", freeSpec, mcq("Wrong type again q"), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			kept, dropped := Validate([]RawQuestion{tt.q}, tt.spec, vLabels, nil)
			if tt.keep && (len(kept) != 1 || dropped != 0) {
				t.Fatalf("kept=%d dropped=%d, want the question kept", len(kept), dropped)
			}
			if !tt.keep && (len(kept) != 0 || dropped != 1) {
				t.Fatalf("kept=%d dropped=%d, want the question dropped", len(kept), dropped)
			}
		})
	}
}

func TestValidateStripsControlAndInvisibleCharactersInsteadOfDropping(t *testing.T) {
	q := mcq("Wh\x00at\u202e is\u200b os\u2066mo\ufeffsis\x7f?\u0085")
	q.Options = []string{"al\x01pha", "be\u200bta", "gam\u202dma", "del\x08ta"}
	kept, dropped := Validate([]RawQuestion{q}, CallSpec{"mcq", 2}, vLabels, nil)
	if dropped != 0 || len(kept) != 1 {
		t.Fatalf("kept=%d dropped=%d", len(kept), dropped)
	}
	if kept[0].Text != "What is osmosis?" {
		t.Errorf("text = %q", kept[0].Text)
	}
	if want := []string{"alpha", "beta", "gamma", "delta"}; !slices.Equal(kept[0].Options, want) {
		t.Errorf("options = %q", kept[0].Options)
	}
	// Characters stripped before counting: 600 real runes plus junk is still 600.
	junk := strings.Repeat("a\u200b", 600)
	if k, d := Validate([]RawQuestion{mcq(junk)}, CallSpec{"mcq", 2}, vLabels, nil); len(k) != 1 || d != 0 {
		t.Errorf("stripped length not used: kept=%d dropped=%d", len(k), d)
	}
	// Distinct only after stripping is still a duplicate option.
	dup := mut(mcq("Stripped dup q"), opts("al\u200bpha", "alpha", "c", "d"))
	if k, d := Validate([]RawQuestion{dup}, CallSpec{"mcq", 2}, vLabels, nil); len(k) != 0 || d != 1 {
		t.Errorf("dup after strip: kept=%d dropped=%d", len(k), d)
	}
}

func TestValidateTrimsText(t *testing.T) {
	kept, _ := Validate([]RawQuestion{mcq("  padded question \n")}, CallSpec{"mcq", 2}, vLabels, nil)
	if len(kept) != 1 || kept[0].Text != "padded question" {
		t.Errorf("kept = %+v", kept)
	}
}

func TestValidateKeepsEntitiesLiteral(t *testing.T) {
	// AI-spec 7.1 says plain text only: tags are rejected but entities are not decoded.
	kept, _ := Validate([]RawQuestion{mcq("Why is &lt;script&gt; escaped as &amp;lt; in HTML?")}, CallSpec{"mcq", 2}, vLabels, nil)
	if len(kept) != 1 || kept[0].Text != "Why is &lt;script&gt; escaped as &amp;lt; in HTML?" {
		t.Errorf("kept = %+v", kept)
	}
}

func TestValidateMapsSourceToDocument(t *testing.T) {
	r2 := mut(mcq("From the second document"), func(q *RawQuestion) { q.Source = "doc-2" })
	kept, _ := Validate([]RawQuestion{mcq("From the first document"), r2}, CallSpec{"mcq", 2}, vLabels, nil)
	if len(kept) != 2 {
		t.Fatalf("kept = %d", len(kept))
	}
	if kept[0].SourceDocID != "d1" || kept[0].SourceDocName != "notes.txt" ||
		kept[1].SourceDocID != "d2" || kept[1].SourceDocName != "Chapter 3.pdf" {
		t.Errorf("sources = %+v %+v", kept[0], kept[1])
	}
	if kept[0].ID == "" || kept[0].ID == kept[1].ID {
		t.Errorf("ids = %q %q", kept[0].ID, kept[1].ID)
	}
}

func TestValidateKeepsInputOrderAndCountsEachDropOnce(t *testing.T) {
	raw := []RawQuestion{
		mcq("First kept question"),
		mut(mcq("Bad marks one"), func(q *RawQuestion) { q.Marks = 5 }),
		mcq("Second kept question"),
		// Several faults at once is still one drop.
		mut(mcq(""), func(q *RawQuestion) { q.Marks = 9; q.Source = "nope"; q.Options = nil; q.CorrectIndex = ip(7) }),
		mcq("Third kept question"),
	}
	kept, dropped := Validate(raw, CallSpec{"mcq", 2}, vLabels, nil)
	if dropped != 2 || len(kept) != 3 || kept[0].Text != "First kept question" ||
		kept[1].Text != "Second kept question" || kept[2].Text != "Third kept question" {
		t.Errorf("kept=%+v dropped=%d", kept, dropped)
	}
	if k, d := Validate(nil, CallSpec{"mcq", 2}, vLabels, nil); len(k) != 0 || d != 0 {
		t.Errorf("empty input: %v %d", k, d)
	}
}

func TestValidateDoesNotAliasInput(t *testing.T) {
	r := mcq("Alias check question")
	kept, _ := Validate([]RawQuestion{r}, CallSpec{"mcq", 2}, vLabels, nil)
	kept[0].Options[0] = "changed"
	*kept[0].CorrectIndex = 3
	if r.Options[0] != "alpha" || *r.CorrectIndex != 1 {
		t.Error("kept question aliases the raw one")
	}
}

func TestValidateDuplicates(t *testing.T) {
	spec := CallSpec{"mcq", 2}
	base := "Explain how photosynthesis converts light energy into chemical energy in plants"
	tests := []struct {
		name  string
		a, b  string
		avoid []string
		want  int // kept
	}{
		{"exact duplicate", base, base, nil, 1},
		{"case and punctuation only", base, strings.ToUpper(base) + "?!", nil, 1},
		{"extra whitespace", base, "  Explain   how photosynthesis converts light energy into chemical energy in plants ", nil, 1},
		{"0.8 similar (4 of 5 words)", "alpha beta gamma delta", "alpha beta gamma delta epsilon", nil, 1},
		{"0.79 similar kept", "w1 w2 w3 w4 w5 w6 w7 w8 w9 w10 w11 w12 w13 w14 w15", "w1 w2 w3 w4 w5 w6 w7 w8 w9 w10 w11 w12 w13 w14 w15 n1 n2 n3 n4", nil, 2},
		{"different", base, "Describe the structure of a plant cell wall and its role", nil, 2},
		{"duplicate of avoid", base, "", []string{base}, 0},
		{"near duplicate of avoid", base, "", []string{"alpha beta gamma delta epsilon"}, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var raw []RawQuestion
			switch {
			case tt.name == "duplicate of avoid":
				raw = []RawQuestion{mcq(tt.a)}
			case tt.name == "near duplicate of avoid":
				raw = []RawQuestion{mcq("alpha beta gamma delta")}
			default:
				raw = []RawQuestion{mcq(tt.a), mcq(tt.b)}
			}
			kept, dropped := Validate(raw, spec, vLabels, tt.avoid)
			if len(kept) != tt.want || dropped != len(raw)-tt.want {
				t.Errorf("kept=%d dropped=%d, want kept %d of %d", len(kept), dropped, tt.want, len(raw))
			}
		})
	}
}

func TestValidateDuplicateKeepsTheFirstOccurrence(t *testing.T) {
	kept, _ := Validate([]RawQuestion{mcq("Same words here"), mcq("same words here"), mcq("Another thing entirely")}, CallSpec{"mcq", 2}, vLabels, nil)
	if len(kept) != 2 || kept[0].Text != "Same words here" {
		t.Errorf("kept = %+v", kept)
	}
}

func TestNearDuplicateBoundary(t *testing.T) {
	words := func(n int, prefix string) string {
		var w []string
		for i := 1; i <= n; i++ {
			w = append(w, fmt.Sprintf("%s%d", prefix, i))
		}
		return strings.Join(w, " ")
	}
	tests := []struct {
		name string
		a, b string
		want bool
	}{
		{"jaccard is 0.8 exactly (4/5): duplicate", "a1 a2 a3 a4", "a1 a2 a3 a4 a5", true},
		{"jaccard 0.8 exactly (8/10)", words(8, "a"), words(8, "a") + " x1 x2", true},
		{"jaccard 15/19 = 0.789: kept", words(15, "a"), words(15, "a") + " x1 x2 x3 x4", false},
		{"jaccard 0.75 (3/4): kept", "a1 a2 a3", "a1 a2 a3 a4", false},
		{"normalised equal", "Hello, World!", "hello   world", true},
		{"word order does not matter (sets)", "red green blue yellow", "yellow blue green red", true},
		{"repeated words collapse", "go go go stop", "go stop", true},
		{"empty vs empty", "", "", true},
		{"empty vs text", "", "something", false},
		{"punctuation only, equal after lowering", "?!", "?!", true},
		{"punctuation only, different", "?!", "...", false},
		{"disjoint", "one two three", "four five six", false},
		{"unicode words", "Éléments chimiques légers", "éléments CHIMIQUES légers", true},
	}
	for _, tt := range tests {
		if got := NearDuplicate(tt.a, tt.b); got != tt.want {
			t.Errorf("%s: NearDuplicate = %v, want %v", tt.name, got, tt.want)
		}
		if got := NearDuplicate(tt.b, tt.a); got != tt.want {
			t.Errorf("%s (swapped): NearDuplicate = %v, want %v", tt.name, got, tt.want)
		}
	}
}

// The index used by Validate must agree with NearDuplicate.
func TestDedupeIndexAgreesWithNearDuplicate(t *testing.T) {
	r := mrand.New(mrand.NewPCG(1, 2))
	vocab := []string{"cell", "wall", "plant", "energy", "light", "water", "root", "leaf", "stem", "seed"}
	gen := func() string {
		n := 1 + r.IntN(6)
		var w []string
		for i := 0; i < n; i++ {
			w = append(w, vocab[r.IntN(len(vocab))])
		}
		return strings.Join(w, " ")
	}
	for round := 0; round < 200; round++ {
		var texts []string
		for i := 0; i < 12; i++ {
			texts = append(texts, gen())
		}
		var wantKept []string
		for _, s := range texts {
			dup := false
			for _, k := range wantKept {
				if NearDuplicate(s, k) {
					dup = true
					break
				}
			}
			if !dup {
				wantKept = append(wantKept, s)
			}
		}
		var raw []RawQuestion
		for _, s := range texts {
			raw = append(raw, free(s))
		}
		kept, _ := Validate(raw, CallSpec{"free", 2}, vLabels, nil)
		var got []string
		for _, q := range kept {
			got = append(got, q.Text)
		}
		if !slices.Equal(got, wantKept) {
			t.Fatalf("round %d: index kept %q, pairwise kept %q (input %q)", round, got, wantKept, texts)
		}
	}
}

func TestValidateBoundedTimeOnLargeInput(t *testing.T) {
	for _, n := range []int{1000, 10000} {
		var raw []RawQuestion
		var avoid []string
		for i := 0; i < n; i++ {
			// Many shared common words: the worst case for an inverted index.
			raw = append(raw, free(fmt.Sprintf("what is the role of the item number%d in the system part%d and area%d", i, i, i)))
			avoid = append(avoid, fmt.Sprintf("avoid text about topic%d section%d chapter%d", i, i, i))
		}
		start := time.Now()
		kept, dropped := Validate(raw, CallSpec{"free", 2}, vLabels, avoid)
		el := time.Since(start)
		if len(kept) != n || dropped != 0 {
			t.Fatalf("n=%d kept=%d dropped=%d", n, len(kept), dropped)
		}
		if el > 20*time.Second {
			t.Errorf("n=%d took %v", n, el)
		}
		t.Logf("n=%d validated in %v", n, el)
	}
}

func TestValidateAllDuplicatesLargeInput(t *testing.T) {
	var raw []RawQuestion
	for i := 0; i < 5000; i++ {
		raw = append(raw, free("the very same question text"))
	}
	start := time.Now()
	kept, dropped := Validate(raw, CallSpec{"free", 2}, vLabels, nil)
	if len(kept) != 1 || dropped != 4999 || time.Since(start) > 10*time.Second {
		t.Errorf("kept=%d dropped=%d in %v", len(kept), dropped, time.Since(start))
	}
}

// ---- shuffle ----

func newMCQ(correct int) store.Question {
	return store.Question{ID: "x", Marks: 1, Text: "t", Options: []string{"a", "b", "c", "d"}, CorrectIndex: ip(correct)}
}

type seeded struct{ r *mrand.Rand }

func (s seeded) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = byte(s.r.UintN(256))
	}
	return len(p), nil
}

func TestShuffleKeepsCorrectAnswer(t *testing.T) {
	for seed := uint64(0); seed < 200; seed++ {
		q := newMCQ(int(seed % 4))
		want := q.Options[*q.CorrectIndex]
		orig := slices.Clone(q.Options)
		ShuffleOptions(&q, seeded{mrand.New(mrand.NewPCG(seed, seed*7+1))})
		if q.Options[*q.CorrectIndex] != want {
			t.Fatalf("seed %d: option at CorrectIndex = %q, want %q", seed, q.Options[*q.CorrectIndex], want)
		}
		got := slices.Clone(q.Options)
		slices.Sort(got)
		if !slices.Equal(got, []string{"a", "b", "c", "d"}) {
			t.Fatalf("seed %d: options %v are not a permutation of %v", seed, q.Options, orig)
		}
	}
}

func TestShuffleActuallyMovesThings(t *testing.T) {
	moved := 0
	for seed := uint64(0); seed < 200; seed++ {
		q := newMCQ(0)
		ShuffleOptions(&q, seeded{mrand.New(mrand.NewPCG(seed, 99))})
		if *q.CorrectIndex != 0 {
			moved++
		}
	}
	if moved < 100 {
		t.Errorf("correct answer moved in only %d of 200 shuffles", moved)
	}
}

func TestShuffleUsesInjectedRandomSource(t *testing.T) {
	run := func(r io.Reader) store.Question {
		q := newMCQ(2)
		ShuffleOptions(&q, r)
		return q
	}
	a, b := run(seeded{mrand.New(mrand.NewPCG(5, 6))}), run(seeded{mrand.New(mrand.NewPCG(5, 6))})
	if !slices.Equal(a.Options, b.Options) || *a.CorrectIndex != *b.CorrectIndex {
		t.Errorf("same source, different result: %v vs %v", a.Options, b.Options)
	}
	// All-zero bytes: every draw is 0, so Fisher-Yates swaps index i with 0 for i = 3,2,1.
	z := run(zeroReader{})
	if want := []string{"b", "c", "d", "a"}; !slices.Equal(z.Options, want) || *z.CorrectIndex != 1 {
		t.Errorf("zero source: options=%v correct=%d, want %v correct=1", z.Options, *z.CorrectIndex, want)
	}
	// Different sources must be able to give different results.
	seen := map[string]bool{}
	for s := uint64(0); s < 50; s++ {
		seen[strings.Join(run(seeded{mrand.New(mrand.NewPCG(s, 1))}).Options, "")] = true
	}
	if len(seen) < 10 {
		t.Errorf("only %d distinct orders from 50 sources", len(seen))
	}
}

// zeroReader makes every randInt draw 0 for n up to 6: it yields one fixed 64-bit value that is
// a multiple of 60 and far above any rejection threshold.
type zeroReader struct{}

func (zeroReader) Read(p []byte) (int, error) {
	const v = ^uint64(0) - (^uint64(0))%60
	for i := range p {
		p[i] = byte(v >> (8 * (7 - i%8)))
	}
	return len(p), nil
}

func TestShuffleWorksWithCryptoRandAndSurvivesShortReader(t *testing.T) {
	q := newMCQ(3)
	ShuffleOptions(&q, rand.Reader)
	if q.Options[*q.CorrectIndex] != "d" {
		t.Error("crypto/rand shuffle lost the answer")
	}
	// A failing reader must not panic or lose the answer.
	q = newMCQ(3)
	ShuffleOptions(&q, bytes.NewReader(nil))
	if q.Options[*q.CorrectIndex] != "d" {
		t.Error("failing reader lost the answer")
	}
	ShuffleOptions(&q, nil) // nil reader means crypto/rand
	if q.Options[*q.CorrectIndex] != "d" {
		t.Error("nil reader lost the answer")
	}
}

func TestShuffleIgnoresQuestionsWithoutOptions(t *testing.T) {
	f := store.Question{Text: "free", ModelAnswer: "a"}
	ShuffleOptions(&f, zeroReader{})
	if f.Options != nil || f.CorrectIndex != nil {
		t.Errorf("free question changed: %+v", f)
	}
	bad := store.Question{Options: []string{"a", "b", "c", "d"}} // nil CorrectIndex
	ShuffleOptions(&bad, zeroReader{})
	oob := store.Question{Options: []string{"a", "b", "c", "d"}, CorrectIndex: ip(9)}
	ShuffleOptions(&oob, zeroReader{}) // must not panic
}

func TestShuffleIsUniformOverFourOptions(t *testing.T) {
	const runs = 24000
	pos := [4]int{}
	perms := map[string]int{}
	r := seeded{mrand.New(mrand.NewPCG(11, 13))}
	for i := 0; i < runs; i++ {
		q := newMCQ(0)
		ShuffleOptions(&q, r)
		pos[*q.CorrectIndex]++
		perms[strings.Join(q.Options, "")]++
	}
	for p, n := range pos {
		if math.Abs(float64(n)-runs/4) > 0.05*runs/4 {
			t.Errorf("answer landed at position %d %d times of %d (expected about %d)", p, n, runs, runs/4)
		}
	}
	if len(perms) != 24 {
		t.Errorf("saw %d of 24 permutations", len(perms))
	}
	for p, n := range perms {
		if math.Abs(float64(n)-runs/24) > 0.2*runs/24 {
			t.Errorf("permutation %s seen %d times (expected about %d)", p, n, runs/24)
		}
	}
}

// A source that is only biased on values the rejection loop must skip: with n = 3 over
// 64-bit draws, a plain modulo would favour 0. Feed draws just above the largest multiple.
func TestRandIntRejectsBiasedDraws(t *testing.T) {
	const n = 3
	// 2^64 mod 3 = 1, so the single draw 0 sits in the biased head and must be rejected.
	biased := bytes.Repeat([]byte{0}, 8)
	good := []byte{0, 0, 0, 0, 0, 0, 0, 5} // 5 mod 3 = 2
	got, err := randInt(bytes.NewReader(append(biased, good...)), n)
	if err != nil || got != 2 {
		t.Errorf("randInt = %d, %v; want 2 (biased draw skipped)", got, err)
	}
	if _, err := randInt(bytes.NewReader(nil), n); err == nil {
		t.Error("exhausted reader must be an error")
	}
	for _, bad := range []int{0, -1} {
		if _, err := randInt(zeroReader{}, bad); err == nil {
			t.Errorf("n=%d must be an error", bad)
		}
	}
}

// ---- ordering ----

func marksQ(id string, marks int) store.Question {
	return store.Question{ID: id, Marks: marks, Text: id}
}

func TestOrderAscendingByMarks(t *testing.T) {
	in := []store.Question{marksQ("a", 3), marksQ("b", 1), marksQ("c", 3), marksQ("d", 2), marksQ("e", 1), marksQ("f", 5), marksQ("g", 2)}
	orig := slices.Clone(in)
	got := OrderQuestions(in, false, zeroReader{})
	var ids []string
	for _, q := range got {
		ids = append(ids, q.ID)
	}
	// Stable: equal marks keep their input order.
	if want := "b e d g a c f"; strings.Join(ids, " ") != want {
		t.Errorf("order = %v, want %s", ids, want)
	}
	if !slices.EqualFunc(in, orig, func(a, b store.Question) bool { return a.ID == b.ID }) {
		t.Error("input slice was reordered")
	}
	if OrderQuestions(nil, false, nil) != nil && len(OrderQuestions(nil, false, nil)) != 0 {
		t.Error("nil input")
	}
}

func TestOrderRandomWhenShuffle(t *testing.T) {
	var in []store.Question
	for i := 0; i < 20; i++ {
		in = append(in, marksQ(fmt.Sprintf("q%02d", i), 1+i%5))
	}
	sorted := OrderQuestions(in, false, nil)
	got := OrderQuestions(in, true, seeded{mrand.New(mrand.NewPCG(3, 4))})
	if len(got) != len(in) {
		t.Fatalf("len = %d", len(got))
	}
	ids := func(qs []store.Question) []string {
		var s []string
		for _, q := range qs {
			s = append(s, q.ID)
		}
		return s
	}
	if slices.Equal(ids(got), ids(sorted)) || slices.Equal(ids(got), ids(in)) {
		t.Error("shuffle left the order unchanged")
	}
	a, b := ids(got), ids(in)
	slices.Sort(a)
	slices.Sort(b)
	if !slices.Equal(a, b) {
		t.Error("shuffle lost or duplicated questions")
	}
	again := OrderQuestions(in, true, seeded{mrand.New(mrand.NewPCG(3, 4))})
	if !slices.Equal(ids(got), ids(again)) {
		t.Error("same source, different order")
	}
	// Zero source: every draw is 0, so swap(i, 0) for i = n-1 down to 1.
	small := []store.Question{marksQ("a", 3), marksQ("b", 1), marksQ("c", 2)}
	z := OrderQuestions(small, true, zeroReader{})
	if g := strings.Join(ids(z), ""); g != "bca" {
		t.Errorf("zero source order = %s, want bca", g)
	}
	// A source that only yields a rejected value gives up instead of looping, and still returns every question.
	stuck := OrderQuestions(small, true, bytes.NewReader(bytes.Repeat([]byte{0}, 8*500)))
	if len(stuck) != 3 {
		t.Errorf("stuck source lost questions: %v", ids(stuck))
	}
}

// ---- ids ----

func TestNewIDsAssignedByWorkerNotModel(t *testing.T) {
	var rq RawQuestion
	js := `{"id":"MODEL-CHOSEN-ID","marks":2,"text":"Name a noble gas","options":["He","Fe","Na","Cu"],"correctIndex":0,"source":"doc-1"}`
	if err := json.Unmarshal([]byte(js), &rq); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 16, 9, 0, 0, 0, time.UTC)
	newID := newIDSource(func() time.Time { return now }, bytes.NewReader(bytes.Repeat([]byte{7}, 256)))
	kept, dropped := validate([]RawQuestion{rq, mcq("Another distinct prompt")}, CallSpec{"mcq", 2}, vLabels, nil, newID)
	if dropped != 0 || len(kept) != 2 {
		t.Fatalf("kept=%d dropped=%d", len(kept), dropped)
	}
	for _, q := range kept {
		if q.ID == "MODEL-CHOSEN-ID" {
			t.Fatal("model-supplied id was used")
		}
		id, err := ulid.ParseStrict(q.ID)
		if err != nil {
			t.Fatalf("id %q is not a ULID: %v", q.ID, err)
		}
		if id.Time() != ulid.Timestamp(now) {
			t.Errorf("id time = %d, want the injected clock's %d", id.Time(), ulid.Timestamp(now))
		}
	}
	if kept[0].ID == kept[1].ID {
		t.Error("ids not unique")
	}
	// Same clock and entropy, same ids: they come from the injected sources.
	again := newIDSource(func() time.Time { return now }, bytes.NewReader(bytes.Repeat([]byte{7}, 256)))
	k2, _ := validate([]RawQuestion{rq, mcq("Another distinct prompt")}, CallSpec{"mcq", 2}, vLabels, nil, again)
	if k2[0].ID != kept[0].ID || k2[1].ID != kept[1].ID {
		t.Error("ids are not deterministic for the same injected clock and entropy")
	}
	// Public Validate still yields valid ULIDs.
	pk, _ := Validate([]RawQuestion{rq}, CallSpec{"mcq", 2}, vLabels, nil)
	if _, err := ulid.ParseStrict(pk[0].ID); err != nil {
		t.Errorf("Validate id %q: %v", pk[0].ID, err)
	}
}
