package dynamo

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"github.com/gokulkumar/exam-assistant/backend/internal/store"
)

type docItem struct {
	OwnerID     string `dynamodbav:"ownerId"`
	ID          string `dynamodbav:"id"`
	Name        string `dynamodbav:"name"`
	Type        string `dynamodbav:"type"`
	ContentType string `dynamodbav:"contentType"`
	SizeBytes   int64  `dynamodbav:"sizeBytes"`
	Pages       int    `dynamodbav:"pages,omitempty"`
	S3Key       string `dynamodbav:"s3Key"`
	Status      string `dynamodbav:"status"`
	CreatedAt   string `dynamodbav:"createdAt"`
	TTL         int64  `dynamodbav:"ttl,omitempty"` // omitted (not 0) so DynamoDB TTL never sees it
}

func docKey(owner, id string) map[string]types.AttributeValue {
	return map[string]types.AttributeValue{
		"ownerId": &types.AttributeValueMemberS{Value: owner},
		"id":      &types.AttributeValueMemberS{Value: id},
	}
}

func (s *Stores) expired(it docItem) bool {
	return it.TTL > 0 && it.TTL <= s.Now().Unix()
}

func (s *Stores) decodeDoc(m map[string]types.AttributeValue) (store.Document, bool, error) {
	var it docItem
	if err := attributevalue.UnmarshalMap(m, &it); err != nil {
		return store.Document{}, false, fmt.Errorf("dynamo: decode document: %w", err)
	}
	t, err := time.Parse(timeLayout, it.CreatedAt)
	if err != nil {
		return store.Document{}, false, fmt.Errorf("dynamo: document %s createdAt: %w", it.ID, err)
	}
	return store.Document{ID: it.ID, OwnerID: it.OwnerID, Name: it.Name, Type: it.Type, ContentType: it.ContentType,
		SizeBytes: it.SizeBytes, Pages: it.Pages, S3Key: it.S3Key, Status: it.Status, CreatedAt: t, TTL: it.TTL}, s.expired(it), nil
}

func (s *Stores) PutDoc(ctx context.Context, d store.Document) error {
	if d.ID == "" || d.OwnerID == "" {
		return errors.New("dynamo: document needs an id and an owner")
	}
	av, err := attributevalue.MarshalMap(docItem{OwnerID: d.OwnerID, ID: d.ID, Name: d.Name, Type: d.Type,
		ContentType: d.ContentType, SizeBytes: d.SizeBytes, Pages: d.Pages, S3Key: d.S3Key, Status: d.Status,
		CreatedAt: d.CreatedAt.UTC().Format(timeLayout), TTL: d.TTL})
	if err != nil {
		return err
	}
	if _, err := s.api.PutItem(ctx, &dynamodb.PutItemInput{TableName: &s.docs, Item: av}); err != nil {
		return fmt.Errorf("dynamo: put document: %w", err)
	}
	return nil
}

func (s *Stores) GetDoc(ctx context.Context, ownerID, id string) (store.Document, error) {
	if ownerID == "" || id == "" {
		return store.Document{}, store.ErrNotFound
	}
	m, err := s.getItem(ctx, s.docs, docKey(ownerID, id))
	if err != nil {
		return store.Document{}, err
	}
	d, expired, err := s.decodeDoc(m)
	if err != nil {
		return store.Document{}, err
	}
	if expired {
		return store.Document{}, store.ErrNotFound
	}
	return d, nil
}

// ListReady queries the owner's partition by id descending (a ULID sort key, so newest
// first). The status filter runs after Limit, so one page can hold few or no ready
// documents; it pages until the requested count is reached or the partition is exhausted.
func (s *Stores) ListReady(ctx context.Context, ownerID string, limit int) ([]store.Document, error) {
	if limit <= 0 || limit > store.MaxListReady {
		limit = store.MaxListReady
	}
	if ownerID == "" {
		return nil, nil
	}
	var out []store.Document
	var start map[string]types.AttributeValue
	for len(out) < limit {
		res, err := s.api.Query(ctx, &dynamodb.QueryInput{
			TableName:                 &s.docs,
			KeyConditionExpression:    ptr("ownerId = :o"),
			FilterExpression:          ptr("#st = :ready"),
			ExpressionAttributeNames:  map[string]string{"#st": "status"},
			ExpressionAttributeValues: map[string]types.AttributeValue{":o": &types.AttributeValueMemberS{Value: ownerID}, ":ready": &types.AttributeValueMemberS{Value: store.DocReady}},
			ScanIndexForward:          ptr(false),
			Limit:                     ptr(int32(max(limit-len(out), 50))),
			ExclusiveStartKey:         start,
		})
		if err != nil {
			return nil, fmt.Errorf("dynamo: list ready: %w", err)
		}
		for _, m := range res.Items {
			d, _, err := s.decodeDoc(m)
			if err != nil {
				return nil, err
			}
			out = append(out, d)
		}
		if len(res.LastEvaluatedKey) == 0 {
			break
		}
		start = res.LastEvaluatedKey
	}
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (s *Stores) GetDocs(ctx context.Context, ownerID string, ids []string) ([]store.Document, error) {
	ids = uniqueKeys(ids)
	if ownerID == "" || len(ids) == 0 {
		return nil, nil
	}
	keys := make([]map[string]types.AttributeValue, len(ids))
	for i, id := range ids {
		keys[i] = docKey(ownerID, id)
	}
	items, err := s.batchGet(ctx, s.docs, keys)
	if err != nil {
		return nil, err
	}
	byID := make(map[string]store.Document, len(items))
	for _, m := range items {
		d, expired, err := s.decodeDoc(m)
		if err != nil {
			return nil, err
		}
		if !expired {
			byID[d.ID] = d
		}
	}
	var out []store.Document
	for _, id := range ids {
		if d, ok := byID[id]; ok {
			out = append(out, d)
		}
	}
	return out, nil
}

func (s *Stores) MarkReady(ctx context.Context, ownerID, id string, pages int) error {
	if ownerID == "" || id == "" {
		return store.ErrNotFound
	}
	pagesAV, _ := attributevalue.Marshal(pages)
	_, err := s.api.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName:                 &s.docs,
		Key:                       docKey(ownerID, id),
		ConditionExpression:       ptr("attribute_exists(id) AND #st = :pending"),
		UpdateExpression:          ptr("SET #st = :ready, pages = :pages REMOVE #ttl"),
		ExpressionAttributeNames:  map[string]string{"#st": "status", "#ttl": "ttl"},
		ExpressionAttributeValues: map[string]types.AttributeValue{":pending": &types.AttributeValueMemberS{Value: store.DocPending}, ":ready": &types.AttributeValueMemberS{Value: store.DocReady}, ":pages": pagesAV},
		// ALL_OLD distinguishes a missing item (empty Item) from one that is not pending.
		ReturnValuesOnConditionCheckFailure: types.ReturnValuesOnConditionCheckFailureAllOld,
	})
	if err == nil {
		return nil
	}
	if ce, ok := isCondFailed(err); ok {
		if len(ce.Item) == 0 {
			return store.ErrNotFound
		}
		return fmt.Errorf("%w: document is not pending", store.ErrPrecondition)
	}
	return fmt.Errorf("dynamo: mark ready: %w", err)
}

func (s *Stores) DeleteDoc(ctx context.Context, ownerID, id string) error {
	if ownerID == "" || id == "" {
		return store.ErrNotFound
	}
	_, err := s.api.DeleteItem(ctx, &dynamodb.DeleteItemInput{
		TableName: &s.docs, Key: docKey(ownerID, id), ConditionExpression: ptr("attribute_exists(id)"),
	})
	if err == nil {
		return nil
	}
	if _, ok := isCondFailed(err); ok {
		return store.ErrNotFound
	}
	return fmt.Errorf("dynamo: delete document: %w", err)
}
