package dynamo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"github.com/gokulkumar/exam-assistant/backend/internal/store"
)

// examItem is the exams table row. Questions are one JSON string attribute (like the job
// questions item), so CorrectIndex keeps its pointer semantics and nothing depends on how
// nested maps marshal.
type examItem struct {
	ID         string `dynamodbav:"id"`
	CreatorID  string `dynamodbav:"creatorId"`
	Title      string `dynamodbav:"title"`
	Type       string `dynamodbav:"type"`
	TotalMarks int    `dynamodbav:"totalMarks"`
	ClosesAt   string `dynamodbav:"closesAt"`
	MaxPeeks   int    `dynamodbav:"maxPeeks"`
	Questions  string `dynamodbav:"questions"`
	CreatedAt  string `dynamodbav:"createdAt"`
}

// examSummaryItem is what a creator-index query decodes: table key, index key and the
// INCLUDE list in template.yaml. TestSummaryAttributesCoveredByTemplateIncludeLists keeps
// these tags and the template in step.
type examSummaryItem struct {
	ID         string `dynamodbav:"id"`
	CreatorID  string `dynamodbav:"creatorId"`
	Title      string `dynamodbav:"title"`
	Type       string `dynamodbav:"type"`
	TotalMarks int    `dynamodbav:"totalMarks"`
	ClosesAt   string `dynamodbav:"closesAt"`
	CreatedAt  string `dynamodbav:"createdAt"`
}

func examKey(id string) map[string]types.AttributeValue {
	return map[string]types.AttributeValue{"id": &types.AttributeValueMemberS{Value: id}}
}

func parseTime(attr, s string) (time.Time, error) {
	t, err := time.Parse(timeLayout, s)
	if err != nil {
		return time.Time{}, fmt.Errorf("dynamo: %s %q: %w", attr, s, err)
	}
	return t, nil
}

func fmtTime(t time.Time) string { return t.UTC().Format(timeLayout) }

func (s *Stores) PutExam(ctx context.Context, e store.Exam) error {
	if e.ID == "" || e.CreatorID == "" {
		return errors.New("dynamo: exam needs an id and a creator")
	}
	qs, err := json.Marshal(e.Questions)
	if err != nil {
		return fmt.Errorf("dynamo: encode questions: %w", err)
	}
	av, err := attributevalue.MarshalMap(examItem{ID: e.ID, CreatorID: e.CreatorID, Title: e.Title, Type: e.Type,
		TotalMarks: e.TotalMarks, ClosesAt: fmtTime(e.ClosesAt), MaxPeeks: e.MaxPeeks, Questions: string(qs), CreatedAt: fmtTime(e.CreatedAt)})
	if err != nil {
		return err
	}
	_, err = s.api.PutItem(ctx, &dynamodb.PutItemInput{TableName: &s.exams, Item: av, ConditionExpression: ptr("attribute_not_exists(id)")})
	if err == nil {
		return nil
	}
	if _, ok := isCondFailed(err); ok {
		return store.ErrConflict
	}
	return fmt.Errorf("dynamo: put exam: %w", err)
}

func decodeExam(m map[string]types.AttributeValue) (store.Exam, error) {
	var it examItem
	if err := attributevalue.UnmarshalMap(m, &it); err != nil {
		return store.Exam{}, fmt.Errorf("dynamo: decode exam: %w", err)
	}
	closes, err := parseTime("closesAt", it.ClosesAt)
	if err != nil {
		return store.Exam{}, err
	}
	created, err := parseTime("createdAt", it.CreatedAt)
	if err != nil {
		return store.Exam{}, err
	}
	var qs []store.Question
	if err := json.Unmarshal([]byte(it.Questions), &qs); err != nil {
		return store.Exam{}, fmt.Errorf("dynamo: decode questions of exam %s: %w", it.ID, err)
	}
	return store.Exam{ID: it.ID, CreatorID: it.CreatorID, Title: it.Title, Type: it.Type, TotalMarks: it.TotalMarks,
		ClosesAt: closes, MaxPeeks: it.MaxPeeks, Questions: qs, CreatedAt: created}, nil
}

func (s *Stores) GetExam(ctx context.Context, id string) (store.Exam, error) {
	if id == "" {
		return store.Exam{}, store.ErrNotFound
	}
	m, err := s.getItem(ctx, s.exams, examKey(id))
	if err != nil {
		return store.Exam{}, err
	}
	return decodeExam(m)
}

func (s *Stores) GetExams(ctx context.Context, ids []string) (map[string]store.Exam, error) {
	ids = uniqueKeys(ids)
	out := make(map[string]store.Exam, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	keys := make([]map[string]types.AttributeValue, len(ids))
	for i, id := range ids {
		keys[i] = examKey(id)
	}
	items, err := s.batchGet(ctx, s.exams, keys)
	if err != nil {
		return nil, err
	}
	for _, m := range items {
		e, err := decodeExam(m)
		if err != nil {
			return nil, err
		}
		out[e.ID] = e
	}
	return out, nil
}

// maxCreatorPages bounds the creator-index pagination (about 5,000 exams per 1 MB page).
const maxCreatorPages = 20

// ListByCreator returns the creator's newest exams, CreatedAt descending with ID descending
// as the tie-break, at most limit (capped at 50).
//
// creator-index is sorted by id (spec 6.2), and exam IDs may be client-chosen UUIDs
// (Idempotency-Key), so id order is not creation order and a Limit-50 query could miss
// today's exams. This therefore reads the creator's whole index partition (following
// LastEvaluatedKey, bounded) and sorts in memory. Cost: one RCU per ~4 KB of summaries,
// so a creator with a few hundred exams costs a handful of RCUs per call; the list is
// small and rarely read. Making createdAt the index sort key would make Limit exact; that
// is a spec 6.2 change for the owner. GSIs do not support consistent reads, so an exam
// created a moment ago may be missing.
func (s *Stores) ListByCreator(ctx context.Context, creatorID string, limit int) ([]store.ExamSummary, error) {
	if limit <= 0 || limit > store.MaxListByCreator {
		limit = store.MaxListByCreator
	}
	if creatorID == "" {
		return nil, nil
	}
	q := dynamodb.QueryInput{
		TableName: &s.exams, IndexName: ptr("creator-index"),
		KeyConditionExpression:    ptr("creatorId = :c"),
		ExpressionAttributeValues: map[string]types.AttributeValue{":c": &types.AttributeValueMemberS{Value: creatorID}},
		ScanIndexForward:          ptr(false),
	}
	var out []store.ExamSummary
	for page := 0; ; page++ {
		if page >= maxCreatorPages {
			return nil, fmt.Errorf("dynamo: list exams by creator: more than %d index pages", maxCreatorPages)
		}
		res, err := s.api.Query(ctx, &q)
		if err != nil {
			return nil, fmt.Errorf("dynamo: list exams by creator: %w", err)
		}
		for _, m := range res.Items {
			var it examSummaryItem
			if err := attributevalue.UnmarshalMap(m, &it); err != nil {
				return nil, fmt.Errorf("dynamo: decode exam summary: %w", err)
			}
			closes, err := parseTime("closesAt", it.ClosesAt)
			if err != nil {
				return nil, err
			}
			created, err := parseTime("createdAt", it.CreatedAt)
			if err != nil {
				return nil, err
			}
			out = append(out, store.ExamSummary{ID: it.ID, CreatorID: it.CreatorID, Title: it.Title, Type: it.Type,
				TotalMarks: it.TotalMarks, ClosesAt: closes, CreatedAt: created})
		}
		if len(res.LastEvaluatedKey) == 0 {
			break
		}
		q.ExclusiveStartKey = res.LastEvaluatedKey
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].CreatedAt.After(out[j].CreatedAt)
		}
		return out[i].ID > out[j].ID
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}
