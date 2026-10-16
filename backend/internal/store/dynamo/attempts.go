package dynamo

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"github.com/gokulkumar/exam-assistant/backend/internal/store"
)

type attemptItem struct {
	ExamID                 string            `dynamodbav:"examId"`
	UserID                 string            `dynamodbav:"userId"`
	Status                 string            `dynamodbav:"status"`
	Answers                map[string]string `dynamodbav:"answers"`
	LastIdx                int               `dynamodbav:"lastIdx"`
	Revealed               []string          `dynamodbav:"revealed"` // always a list (possibly empty) so size() works
	ScoreHalves            *int              `dynamodbav:"scoreHalves,omitempty"`
	ScoreBeforePeeksHalves *int              `dynamodbav:"scoreBeforePeeksHalves,omitempty"`
	StartedAt              string            `dynamodbav:"startedAt,omitempty"`
	SavedAt                string            `dynamodbav:"savedAt,omitempty"`
	SubmittedAt            string            `dynamodbav:"submittedAt,omitempty"`
	AutoSubmitted          bool              `dynamodbav:"autoSubmitted"`
	CreatedAt              string            `dynamodbav:"createdAt"`
}

// attemptSummaryItem is what a user-index query decodes: table key, index key and the
// INCLUDE list in template.yaml (see examSummaryItem).
type attemptSummaryItem struct {
	ExamID                 string `dynamodbav:"examId"`
	UserID                 string `dynamodbav:"userId"`
	Status                 string `dynamodbav:"status"`
	ScoreHalves            *int   `dynamodbav:"scoreHalves,omitempty"`
	ScoreBeforePeeksHalves *int   `dynamodbav:"scoreBeforePeeksHalves,omitempty"`
	SubmittedAt            string `dynamodbav:"submittedAt,omitempty"`
}

func attemptKey(examID, userID string) map[string]types.AttributeValue {
	return map[string]types.AttributeValue{
		"examId": &types.AttributeValueMemberS{Value: examID},
		"userId": &types.AttributeValueMemberS{Value: userID},
	}
}

func optTime(t *time.Time) string {
	if t == nil {
		return ""
	}
	return fmtTime(*t)
}

func parseOptTime(attr, v string) (*time.Time, error) {
	if v == "" {
		return nil, nil
	}
	t, err := parseTime(attr, v)
	if err != nil {
		return nil, err
	}
	return &t, nil
}

func sv(v string) types.AttributeValue { return &types.AttributeValueMemberS{Value: v} }

func (s *Stores) PutAttemptIfAbsent(ctx context.Context, a store.Attempt) (bool, error) {
	if a.ExamID == "" || a.UserID == "" {
		return false, errors.New("dynamo: attempt needs an exam and a user")
	}
	if a.Status == "" {
		a.Status = store.AttemptNotStarted
	}
	if a.Answers == nil {
		a.Answers = map[string]string{}
	}
	if a.Revealed == nil {
		a.Revealed = []string{}
	}
	av, err := attributevalue.MarshalMap(attemptItem{ExamID: a.ExamID, UserID: a.UserID, Status: a.Status, Answers: a.Answers,
		LastIdx: a.LastQuestionIndex, Revealed: a.Revealed, ScoreHalves: a.ScoreHalves, ScoreBeforePeeksHalves: a.ScoreBeforePeeksHalves,
		StartedAt: optTime(a.StartedAt), SavedAt: optTime(a.SavedAt), SubmittedAt: optTime(a.SubmittedAt),
		AutoSubmitted: a.AutoSubmitted, CreatedAt: fmtTime(a.CreatedAt)})
	if err != nil {
		return false, err
	}
	_, err = s.api.PutItem(ctx, &dynamodb.PutItemInput{TableName: &s.attempts, Item: av, ConditionExpression: ptr("attribute_not_exists(userId)")})
	if err == nil {
		return true, nil
	}
	if _, ok := isCondFailed(err); ok {
		return false, nil
	}
	return false, fmt.Errorf("dynamo: put attempt: %w", err)
}

func (s *Stores) GetAttempt(ctx context.Context, examID, userID string) (store.Attempt, error) {
	if examID == "" || userID == "" {
		return store.Attempt{}, store.ErrNotFound
	}
	m, err := s.getItem(ctx, s.attempts, attemptKey(examID, userID))
	if err != nil {
		return store.Attempt{}, err
	}
	return decodeAttempt(m)
}

func decodeAttempt(m map[string]types.AttributeValue) (store.Attempt, error) {
	var it attemptItem
	if err := attributevalue.UnmarshalMap(m, &it); err != nil {
		return store.Attempt{}, fmt.Errorf("dynamo: decode attempt: %w", err)
	}
	a := store.Attempt{ExamID: it.ExamID, UserID: it.UserID, Status: it.Status, Answers: it.Answers, LastQuestionIndex: it.LastIdx,
		Revealed: it.Revealed, ScoreHalves: it.ScoreHalves, ScoreBeforePeeksHalves: it.ScoreBeforePeeksHalves, AutoSubmitted: it.AutoSubmitted}
	if a.Answers == nil {
		a.Answers = map[string]string{}
	}
	if a.Revealed == nil {
		a.Revealed = []string{}
	}
	var err error
	if a.StartedAt, err = parseOptTime("startedAt", it.StartedAt); err != nil {
		return a, err
	}
	if a.SavedAt, err = parseOptTime("savedAt", it.SavedAt); err != nil {
		return a, err
	}
	if a.SubmittedAt, err = parseOptTime("submittedAt", it.SubmittedAt); err != nil {
		return a, err
	}
	if a.CreatedAt, err = parseTime("createdAt", it.CreatedAt); err != nil {
		return a, err
	}
	return a, nil
}

// queryAll runs q, following LastEvaluatedKey to the end, and returns every item.
func (s *Stores) queryAll(ctx context.Context, q dynamodb.QueryInput) ([]map[string]types.AttributeValue, error) {
	var out []map[string]types.AttributeValue
	for {
		res, err := s.api.Query(ctx, &q)
		if err != nil {
			return nil, err
		}
		out = append(out, res.Items...)
		if len(res.LastEvaluatedKey) == 0 {
			return out, nil
		}
		q.ExclusiveStartKey = res.LastEvaluatedKey
	}
}

// ListByUser queries user-index, which projects no answers. GSI reads are eventually consistent.
func (s *Stores) ListByUser(ctx context.Context, userID string) ([]store.AttemptSummary, error) {
	if userID == "" {
		return nil, nil
	}
	items, err := s.queryAll(ctx, dynamodb.QueryInput{
		TableName: &s.attempts, IndexName: ptr("user-index"),
		KeyConditionExpression:    ptr("userId = :u"),
		ExpressionAttributeValues: map[string]types.AttributeValue{":u": sv(userID)},
	})
	if err != nil {
		return nil, fmt.Errorf("dynamo: list attempts by user: %w", err)
	}
	var out []store.AttemptSummary
	for _, m := range items {
		var it attemptSummaryItem
		if err := attributevalue.UnmarshalMap(m, &it); err != nil {
			return nil, fmt.Errorf("dynamo: decode attempt summary: %w", err)
		}
		sub, err := parseOptTime("submittedAt", it.SubmittedAt)
		if err != nil {
			return nil, err
		}
		out = append(out, store.AttemptSummary{ExamID: it.ExamID, UserID: it.UserID, Status: it.Status,
			ScoreHalves: it.ScoreHalves, ScoreBeforePeeksHalves: it.ScoreBeforePeeksHalves, SubmittedAt: sub})
	}
	return out, nil
}

func (s *Stores) ListByExam(ctx context.Context, examID string) ([]store.Attempt, error) {
	if examID == "" {
		return nil, nil
	}
	items, err := s.queryAll(ctx, dynamodb.QueryInput{
		TableName:                 &s.attempts,
		KeyConditionExpression:    ptr("examId = :e"),
		ExpressionAttributeValues: map[string]types.AttributeValue{":e": sv(examID)},
		ConsistentRead:            ptr(true),
	})
	if err != nil {
		return nil, fmt.Errorf("dynamo: list attempts by exam: %w", err)
	}
	var out []store.Attempt
	for _, m := range items {
		a, err := decodeAttempt(m)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, nil
}

// condUpdate runs a conditional UpdateItem on an attempt. ok=false means the condition
// failed; old is then the item as it was (empty when the row is missing).
func (s *Stores) condUpdate(ctx context.Context, examID, userID, cond, update string, names map[string]string,
	vals map[string]types.AttributeValue, returnNew bool) (out map[string]types.AttributeValue, ok bool, old map[string]types.AttributeValue, err error) {
	in := &dynamodb.UpdateItemInput{
		TableName: &s.attempts, Key: attemptKey(examID, userID),
		ConditionExpression: &cond, UpdateExpression: &update,
		ExpressionAttributeNames: names, ExpressionAttributeValues: vals,
		// ALL_OLD tells a missing row (empty Item) from one in the wrong state.
		ReturnValuesOnConditionCheckFailure: types.ReturnValuesOnConditionCheckFailureAllOld,
	}
	if returnNew {
		in.ReturnValues = types.ReturnValueUpdatedNew
	}
	res, err := s.api.UpdateItem(ctx, in)
	if err == nil {
		return res.Attributes, true, nil, nil
	}
	if ce, isCF := isCondFailed(err); isCF {
		return nil, false, ce.Item, nil
	}
	return nil, false, nil, err
}

func statusOf(m map[string]types.AttributeValue) string {
	if v, ok := m["status"].(*types.AttributeValueMemberS); ok {
		return v.Value
	}
	return ""
}

var statusName = map[string]string{"#st": "status"}

// Save is up to two conditional writes. The common one (in_progress) sets answers, lastIdx
// and savedAt only, so it never writes to user-index. The first save of a not_started
// attempt falls through to the second form, which moves it to in_progress and sets
// startedAt (the only autosave write that changes the indexed status).
func (s *Stores) Save(ctx context.Context, examID, userID string, answers map[string]string, lastIdx int, now time.Time) error {
	if examID == "" || userID == "" {
		return store.ErrNotFound
	}
	if answers == nil {
		answers = map[string]string{}
	}
	ans, err := attributevalue.Marshal(answers)
	if err != nil {
		return err
	}
	idx, _ := attributevalue.Marshal(lastIdx)
	ts := sv(fmtTime(now))
	// Autosaves are the common case, so try the in_progress form first.
	_, ok, old, err := s.condUpdate(ctx, examID, userID, "#st = :ip",
		"SET savedAt = :t, answers = :a, lastIdx = :i, startedAt = if_not_exists(startedAt, :t)", statusName,
		map[string]types.AttributeValue{":ip": sv(store.AttemptInProgress), ":t": ts, ":a": ans, ":i": idx}, false)
	if err != nil {
		return fmt.Errorf("dynamo: save attempt: %w", err)
	}
	if ok {
		return nil
	}
	if len(old) == 0 {
		return store.ErrNotFound
	}
	_, ok, old, err = s.condUpdate(ctx, examID, userID, "#st = :ns",
		"SET #st = :ip, startedAt = :t, savedAt = :t, answers = :a, lastIdx = :i", statusName,
		map[string]types.AttributeValue{":ns": sv(store.AttemptNotStarted), ":ip": sv(store.AttemptInProgress), ":t": ts, ":a": ans, ":i": idx}, false)
	if err != nil {
		return fmt.Errorf("dynamo: save attempt: %w", err)
	}
	if ok {
		return nil
	}
	if len(old) == 0 {
		return store.ErrNotFound
	}
	return fmt.Errorf("%w: attempt is %s", store.ErrPrecondition, statusOf(old))
}

func (s *Stores) Submit(ctx context.Context, a store.Attempt) error {
	if a.ExamID == "" || a.UserID == "" {
		return store.ErrNotFound
	}
	if a.Answers == nil {
		a.Answers = map[string]string{}
	}
	ans, err := attributevalue.Marshal(a.Answers)
	if err != nil {
		return err
	}
	idx, _ := attributevalue.Marshal(a.LastQuestionIndex)
	vals := map[string]types.AttributeValue{":sub": sv(store.AttemptSubmitted), ":dec": sv(store.AttemptDeclined), ":a": ans, ":i": idx,
		":auto": &types.AttributeValueMemberBOOL{Value: a.AutoSubmitted}}
	set := "#st = :sub, answers = :a, lastIdx = :i, autoSubmitted = :auto"
	add := func(attr, ph string, v types.AttributeValue) { set += ", " + attr + " = " + ph; vals[ph] = v }
	if a.ScoreHalves != nil {
		v, _ := attributevalue.Marshal(*a.ScoreHalves)
		add("scoreHalves", ":sh", v)
	}
	if a.ScoreBeforePeeksHalves != nil {
		v, _ := attributevalue.Marshal(*a.ScoreBeforePeeksHalves)
		add("scoreBeforePeeksHalves", ":sb", v)
	}
	if a.SubmittedAt != nil {
		add("submittedAt", ":sat", sv(fmtTime(*a.SubmittedAt)))
	}
	if a.SavedAt != nil {
		add("savedAt", ":svd", sv(fmtTime(*a.SavedAt)))
	}
	if a.StartedAt != nil {
		add("startedAt", ":sta", sv(fmtTime(*a.StartedAt)))
		set = set[:len(set)-len(", startedAt = :sta")] + ", startedAt = if_not_exists(startedAt, :sta)"
	}
	_, ok, old, err := s.condUpdate(ctx, a.ExamID, a.UserID, "attribute_exists(examId) AND #st <> :sub AND #st <> :dec", "SET "+set, statusName, vals, false)
	if err != nil {
		return fmt.Errorf("dynamo: submit attempt: %w", err)
	}
	if ok {
		return nil
	}
	if len(old) == 0 {
		return store.ErrNotFound
	}
	return fmt.Errorf("%w: attempt is %s", store.ErrPrecondition, statusOf(old))
}

func (s *Stores) Decline(ctx context.Context, examID, userID string) error {
	if examID == "" || userID == "" {
		return store.ErrNotFound
	}
	_, ok, old, err := s.condUpdate(ctx, examID, userID, "#st = :ns", "SET #st = :dec", statusName,
		map[string]types.AttributeValue{":ns": sv(store.AttemptNotStarted), ":dec": sv(store.AttemptDeclined)}, false)
	if err != nil {
		return fmt.Errorf("dynamo: decline attempt: %w", err)
	}
	if ok {
		return nil
	}
	switch {
	case len(old) == 0:
		return store.ErrNotFound
	case statusOf(old) == store.AttemptDeclined:
		return nil
	}
	return fmt.Errorf("%w: attempt is %s", store.ErrPrecondition, statusOf(old))
}

const maxRevealAttempts = 3

// Reveal appends questionID to revealed with a conditional update (size(revealed) < max and
// the question not already in it). A not_started attempt is started by the same write. When
// the condition fails the row is re-read to say why; a lost race with another reveal loops
// (bounded) and is then judged on fresh state.
func (s *Stores) Reveal(ctx context.Context, examID, userID, questionID string, max int) (store.RevealResult, error) {
	if examID == "" || userID == "" || questionID == "" {
		return store.RevealResult{}, store.ErrNotFound
	}
	maxAV, _ := attributevalue.Marshal(max)
	qList := &types.AttributeValueMemberL{Value: []types.AttributeValue{sv(questionID)}}
	guard := " AND size(revealed) < :max AND NOT contains(revealed, :q)"
	for attempt := 0; attempt < maxRevealAttempts; attempt++ {
		// Not started: the same write starts the attempt.
		out, ok, old, err := s.condUpdate(ctx, examID, userID, "#st = :ns"+guard,
			"SET #st = :ip, startedAt = :t, savedAt = :t, revealed = list_append(revealed, :l)", statusName,
			map[string]types.AttributeValue{":ns": sv(store.AttemptNotStarted), ":ip": sv(store.AttemptInProgress), ":t": sv(fmtTime(s.Now())),
				":max": maxAV, ":q": sv(questionID), ":l": qList}, true)
		if err != nil {
			return store.RevealResult{}, fmt.Errorf("dynamo: reveal: %w", err)
		}
		if !ok && len(old) == 0 {
			return store.RevealResult{}, store.ErrNotFound
		}
		if !ok {
			out, ok, _, err = s.condUpdate(ctx, examID, userID, "#st = :ip"+guard,
				"SET revealed = list_append(revealed, :l)", statusName,
				map[string]types.AttributeValue{":ip": sv(store.AttemptInProgress), ":max": maxAV, ":q": sv(questionID), ":l": qList}, true)
			if err != nil {
				return store.RevealResult{}, fmt.Errorf("dynamo: reveal: %w", err)
			}
		}
		if ok {
			var it struct {
				Revealed []string `dynamodbav:"revealed"`
			}
			if err := attributevalue.UnmarshalMap(out, &it); err != nil {
				return store.RevealResult{}, fmt.Errorf("dynamo: decode reveal: %w", err)
			}
			return store.RevealResult{Revealed: it.Revealed, Charged: true}, nil
		}
		// Neither write applied: find out why.
		a, err := s.GetAttempt(ctx, examID, userID)
		if err != nil {
			return store.RevealResult{}, err
		}
		if slices.Contains(a.Revealed, questionID) {
			return store.RevealResult{Revealed: a.Revealed}, nil
		}
		if a.Status != store.AttemptNotStarted && a.Status != store.AttemptInProgress {
			return store.RevealResult{}, fmt.Errorf("%w: attempt is %s", store.ErrPrecondition, a.Status)
		}
		if len(a.Revealed) >= max {
			return store.RevealResult{}, fmt.Errorf("%w: no peeks left", store.ErrConflict)
		}
		// Otherwise another writer changed the row between our update and the read; retry.
	}
	return store.RevealResult{}, errors.New("dynamo: reveal: too much contention")
}
