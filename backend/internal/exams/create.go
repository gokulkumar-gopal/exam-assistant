package exams

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/gokulkumar/exam-assistant/backend/internal/apperr"
	"github.com/gokulkumar/exam-assistant/backend/internal/auth"
	"github.com/gokulkumar/exam-assistant/backend/internal/store"
)

// Limits (backend-spec 8.5, 9.1). The text caps are not in the spec; they match generation's
// own caps scaled up so that edited questions are never refused for length.
const (
	MinDeadline      = time.Minute
	MaxDeadline      = 30 * 24 * time.Hour
	MaxTitleRunes    = 120
	MaxQuestions     = 60
	MaxSourceRunes   = 120
	MaxTextRunes     = 2000
	MaxOptionRunes   = 500
	MaxAnswerRunes   = 4000
	MaxPeeksLimit    = 10
	DefaultPeeks     = 3
	TeacherParentCap = 50
	StudentCap       = 10
	MaxItemBytes     = 350 * 1024 // DynamoDB's item limit is 400 KB; keep a margin
	MaxAttemptWrites = 10         // concurrent attempt-row writes
	MaxEmailBytes    = 254        // the limit auth.validEmail applies
	mcqOptions       = 4
)

// CreateQuestion is one question as the client sends it. ID is accepted and ignored.
type CreateQuestion struct {
	ID            string   `json:"id"`
	Marks         int      `json:"marks"`
	Text          string   `json:"text"`
	Options       []string `json:"options"`
	CorrectIndex  *int     `json:"correctIndex"`
	ModelAnswer   string   `json:"modelAnswer"`
	SourceDocID   string   `json:"sourceDocId"`
	SourceDocName string   `json:"sourceDocName"`
}

// CreateInput is the body of POST /exams.
type CreateInput struct {
	Title           string           `json:"title"`
	Type            string           `json:"type"`
	ClosesAt        string           `json:"closesAt"`
	ClosesInSeconds *int64           `json:"closesInSeconds"`
	MaxPeeks        *int             `json:"maxPeeks"`
	AssigneeEmails  []string         `json:"assigneeEmails"`
	Questions       []CreateQuestion `json:"questions"`
}

// CreateResult is the outcome of Create. UnknownEmails is never nil.
type CreateResult struct {
	ID            string
	UnknownEmails []string
	Replay        bool
}

func validKey(k string) bool {
	if len(k) < 8 || len(k) > 64 {
		return false
	}
	for i := 0; i < len(k); i++ {
		c := k[i]
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-') {
			return false
		}
	}
	return true
}

func runes(s string) int { return utf8.RuneCountInString(s) }

// validated is a CreateInput that passed every check, in stored form.
type validated struct {
	title, typ string
	closesAt   time.Time
	maxPeeks   int
	emails     []string // distinct (by normalised form), as sent, trimmed
	questions  []store.Question
	totalMarks int
}

func (s *Service) validate(in CreateInput, user store.User) (validated, error) {
	f := map[string]string{}
	v := validated{title: strings.TrimSpace(in.Title), typ: in.Type}
	now := s.clk.Now()

	if v.title == "" || runes(v.title) > MaxTitleRunes {
		f["title"] = "Title must be 1 to 120 characters."
	}
	if v.typ != "mcq" && v.typ != "free" {
		f["type"] = "Type must be mcq or free."
	}

	switch {
	case (in.ClosesAt == "") == (in.ClosesInSeconds == nil):
		f["closesAt"] = "Send exactly one of closesAt or closesInSeconds."
	case in.ClosesInSeconds != nil:
		secs := *in.ClosesInSeconds
		if secs < int64(MinDeadline/time.Second) || secs > int64(MaxDeadline/time.Second) {
			f["closesInSeconds"] = "The deadline must be between 1 minute and 30 days from now."
		} else {
			v.closesAt = now.Add(time.Duration(secs) * time.Second).UTC()
		}
	default:
		t, err := time.Parse(time.RFC3339, in.ClosesAt)
		if err != nil {
			f["closesAt"] = "closesAt must be an ISO 8601 date and time."
		} else if d := t.Sub(now); d < MinDeadline || d > MaxDeadline {
			f["closesAt"] = "The deadline must be between 1 minute and 30 days from now."
		} else {
			v.closesAt = t.UTC()
		}
	}

	n := len(in.Questions)
	if n < 1 || n > MaxQuestions {
		f["questions"] = "An exam needs 1 to 60 questions."
	}
	if v.typ == "mcq" || v.typ == "free" {
		if n <= MaxQuestions {
			for i, q := range in.Questions {
				sq, msg := checkQuestion(q, v.typ)
				if msg != "" {
					f[fmt.Sprintf("questions[%d]", i)] = msg
					continue
				}
				v.questions = append(v.questions, sq)
				v.totalMarks += sq.Marks
			}
		}
	}

	emails, ef := checkEmails(in.AssigneeEmails, user.Role)
	if ef != "" {
		f["assigneeEmails"] = ef
	}
	v.emails = emails

	switch {
	case in.MaxPeeks == nil && len(emails) == 0:
		v.maxPeeks = DefaultPeeks
		if n >= 1 && n < DefaultPeeks {
			v.maxPeeks = n
		}
	case in.MaxPeeks == nil:
		v.maxPeeks = 0
	case *in.MaxPeeks < 0 || *in.MaxPeeks > MaxPeeksLimit || *in.MaxPeeks > n:
		f["maxPeeks"] = "Peeks must be 0 to 10 and no more than the number of questions."
	default:
		v.maxPeeks = *in.MaxPeeks
	}

	if len(f) > 0 {
		return validated{}, apperr.Validation("Some fields need attention.", f)
	}
	return v, nil
}

func checkQuestion(q CreateQuestion, typ string) (store.Question, string) {
	text := strings.TrimSpace(q.Text)
	out := store.Question{Marks: q.Marks, Text: text}
	if q.Marks < 1 || q.Marks > 5 {
		return out, "Marks must be 1 to 5."
	}
	if text == "" || runes(text) > MaxTextRunes {
		return out, "Question text must be 1 to 2000 characters."
	}
	if runes(q.SourceDocID) > MaxSourceRunes || runes(q.SourceDocName) > MaxSourceRunes {
		return out, "Source fields must be at most 120 characters."
	}
	out.SourceDocID, out.SourceDocName = q.SourceDocID, q.SourceDocName
	if typ == "mcq" {
		if q.ModelAnswer != "" {
			return out, "A multiple choice question has no model answer."
		}
		if len(q.Options) != mcqOptions {
			return out, "A multiple choice question needs exactly 4 options."
		}
		if q.CorrectIndex == nil || *q.CorrectIndex < 0 || *q.CorrectIndex >= mcqOptions {
			return out, "correctIndex must be 0 to 3."
		}
		seen := map[string]bool{}
		for _, o := range q.Options {
			o = strings.TrimSpace(o)
			if o == "" || runes(o) > MaxOptionRunes {
				return out, "Each option must be 1 to 500 characters."
			}
			k := strings.ToLower(o)
			if seen[k] {
				return out, "Options must be different from each other."
			}
			seen[k] = true
			out.Options = append(out.Options, o)
		}
		ci := *q.CorrectIndex
		out.CorrectIndex = &ci
		return out, ""
	}
	if len(q.Options) != 0 || q.CorrectIndex != nil {
		return out, "A free text question has no options or correctIndex."
	}
	ma := strings.TrimSpace(q.ModelAnswer)
	if ma == "" || runes(ma) > MaxAnswerRunes {
		return out, "A free text question needs a model answer of 1 to 4000 characters."
	}
	out.ModelAnswer = ma
	return out, ""
}

// checkEmails dedupes by normalised form (keeping the first spelling, trimmed) and applies the cap.
func checkEmails(in []string, role string) ([]string, string) {
	seen := map[string]bool{}
	out := []string{}
	for _, e := range in {
		t := strings.TrimSpace(e)
		if t == "" {
			return nil, "Assignee emails must not be empty."
		}
		if len(t) > MaxEmailBytes {
			return nil, "Assignee emails must be at most 254 characters."
		}
		k := auth.NormalizeEmail(t)
		if seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, t)
	}
	if capN := assigneeCap(role); len(out) > capN {
		return nil, capMessage(capN)
	}
	return out, ""
}

// resolve returns the user IDs allowed to hold an attempt row, in input order, and the emails
// reported as unknown. A known user the creator may not assign to is reported exactly like an
// unregistered email, so the two outputs are indistinguishable.
func (s *Service) resolve(ctx context.Context, creator store.User, emails []string) (ids, unknown []string, err error) {
	unknown = []string{}
	if len(emails) == 0 {
		return []string{creator.ID}, unknown, nil
	}
	norm := make([]string, len(emails))
	for i, e := range emails {
		norm[i] = auth.NormalizeEmail(e)
	}
	found, err := s.users.GetManyByEmail(ctx, norm)
	if err != nil {
		return nil, nil, apperr.Internal(err)
	}
	ids = []string{}
	for i, e := range emails {
		u, ok := found[norm[i]]
		if !ok || (creator.Role == "student" && u.Role != "student") {
			unknown = append(unknown, e)
			continue
		}
		ids = append(ids, u.ID)
	}
	return ids, unknown, nil
}

func itemSize(e store.Exam) (int, error) {
	b, err := json.Marshal(e)
	return len(b), err
}

// Create validates and stores an exam, then its attempt rows. idemKey, when not empty, becomes
// the exam ID. The exam item is written first; a crash before the rows are written is repaired
// by a replay with the same key (backend-spec 8.5). Without a key the server mints a ULID, so such
// a crash cannot be repaired; the frontend always sends a key.
func (s *Service) Create(ctx context.Context, user store.User, in CreateInput, idemKey string) (CreateResult, error) {
	id := idemKey
	if id == "" {
		id = newID(s.clk)
	} else if !validKey(id) {
		return CreateResult{}, apperr.Validation("Some fields need attention.", map[string]string{
			"Idempotency-Key": "Idempotency-Key must be 8 to 64 letters, digits, underscores or hyphens.",
		})
	}
	v, err := s.validate(in, user)
	if err != nil {
		return CreateResult{}, err
	}
	ids, unknown, err := s.resolve(ctx, user, v.emails)
	if err != nil {
		return CreateResult{}, err
	}

	now := s.clk.Now()
	exam := store.Exam{
		ID: id, CreatorID: user.ID, Title: v.title, Type: v.typ, TotalMarks: v.totalMarks,
		ClosesAt: v.closesAt, MaxPeeks: v.maxPeeks, CreatedAt: now,
	}
	for _, q := range v.questions {
		q.ID = newID(s.clk)
		exam.Questions = append(exam.Questions, q)
	}
	size, err := itemSize(exam)
	if err != nil {
		return CreateResult{}, apperr.Internal(err)
	}
	if size > MaxItemBytes {
		return CreateResult{}, apperr.Validation("Some fields need attention.", map[string]string{
			"questions": "The questions are too long in total. Shorten or remove some.",
		})
	}

	err = s.exams.PutExam(ctx, exam)
	switch {
	case err == nil:
		if err := s.writeRows(ctx, id, ids, now); err != nil {
			return CreateResult{}, err
		}
		return CreateResult{ID: id, UnknownEmails: unknown}, nil
	case errors.Is(err, store.ErrConflict):
	default:
		return CreateResult{}, apperr.Internal(err)
	}

	// The key is taken: a replay by its creator, or someone else's key.
	existing, err := s.exams.GetExam(ctx, id)
	if err != nil {
		return CreateResult{}, apperr.Internal(err)
	}
	if existing.CreatorID != user.ID {
		return CreateResult{}, apperr.Conflict("That request key is already in use.")
	}
	// Replay by the creator: the existing exam is never changed (edited content is ignored).
	// Rows are completed for the replay body's assignees, but the exam may never end up with more
	// assignees than the creator's cap, counting rows that already exist.
	rows, err := s.attempts.ListByExam(ctx, id)
	if err != nil {
		return CreateResult{}, apperr.Internal(err)
	}
	holders := map[string]bool{}
	for _, a := range rows {
		holders[a.UserID] = true
	}
	for _, uid := range ids {
		holders[uid] = true
	}
	if len(v.emails) == 0 {
		// Practice replay: the creator's own row is the only one wanted.
		holders = map[string]bool{}
	} else if !contains(ids, user.ID) {
		delete(holders, user.ID) // the creator's practice row does not count as an assignee
	}
	if n, capN := len(holders), assigneeCap(user.Role); n > capN {
		return CreateResult{}, apperr.Validation("Some fields need attention.", map[string]string{
			"assigneeEmails": capMessage(capN),
		})
	}
	if err := s.writeRows(ctx, id, ids, existing.CreatedAt); err != nil {
		return CreateResult{}, err
	}
	return CreateResult{ID: id, UnknownEmails: unknown, Replay: true}, nil
}

func contains(xs []string, x string) bool {
	for _, y := range xs {
		if y == x {
			return true
		}
	}
	return false
}

func assigneeCap(role string) int {
	if role == "student" {
		return StudentCap
	}
	return TeacherParentCap
}

func capMessage(n int) string { return fmt.Sprintf("You can assign an exam to at most %d people.", n) }

// writeRows creates one not_started row per user, at most MaxAttemptWrites at a time.
// A row that exists is left as it is.
func (s *Service) writeRows(ctx context.Context, examID string, userIDs []string, at time.Time) error {
	sem := make(chan struct{}, MaxAttemptWrites)
	var wg sync.WaitGroup
	var mu sync.Mutex
	var first error
	for _, uid := range userIDs {
		sem <- struct{}{}
		wg.Add(1)
		go func() {
			defer func() { <-sem; wg.Done() }()
			_, err := s.attempts.PutAttemptIfAbsent(ctx, store.Attempt{
				ExamID: examID, UserID: uid, Status: store.AttemptNotStarted,
				Answers: map[string]string{}, Revealed: []string{}, CreatedAt: at,
			})
			if err != nil {
				mu.Lock()
				if first == nil {
					first = err
				}
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if first != nil {
		// 503, not 500: the exam item exists and a retry with the same key repairs the rows.
		e := apperr.Upstream()
		e.Err = first
		return e
	}
	return nil
}
