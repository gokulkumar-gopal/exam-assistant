package dynamo

import (
	"context"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"github.com/gokulkumar/exam-assistant/backend/internal/store"
)

var _ store.UsageStore = (*Stores)(nil)

const usagePrefix = "usage#"

func usageID(period, model string) string { return usagePrefix + period + "#" + model }

// usageAttrs are the counter attributes of a usage item, in the order the update writes them.
var usageAttrs = []string{"jobs", "calls", "inTok", "outTok", "cacheReadTok", "cacheWriteTok", "costMicros"}

func usageValues(d store.UsageDelta) []int64 {
	return []int64{d.Jobs, d.Calls, d.InTok, d.OutTok, d.CacheReadTok, d.CacheWriteTok, d.CostMicros}
}

// AddUsage is one TransactWriteItems with two Updates, the month item and the day item of the
// model, so a retry after a failure cannot count one and not the other. Each is
//
//	ADD #jobs :jobs, ... SET #ttl = if_not_exists(#ttl, :ttl)
//
// ADD, never SET or Put: concurrent workers add into the same item without losing an update.
// The ttl is set when the item is created (40 days for the month, 3 for the day) and later
// adds never move it. The items carry neither ownerId nor day, so no index holds them.
func (s *Stores) AddUsage(ctx context.Context, month, day, model string, d store.UsageDelta) error {
	if err := store.ValidateUsageAdd(month, day, model, d); err != nil {
		return err
	}
	vals := usageValues(d)
	update := func(period string, life time.Duration) types.TransactWriteItem {
		names := map[string]string{"#ttl": "ttl"}
		values := map[string]types.AttributeValue{":ttl": numAV(s.ttlAt(life))}
		expr := "ADD "
		for i, a := range usageAttrs {
			if i > 0 {
				expr += ", "
			}
			expr += "#" + a + " :" + a
			names["#"+a] = a
			values[":"+a] = numAV(vals[i])
		}
		expr += " SET #ttl = if_not_exists(#ttl, :ttl)"
		return types.TransactWriteItem{Update: &types.Update{
			TableName: &s.jobs, Key: jobKey(usageID(period, model)), UpdateExpression: &expr,
			ExpressionAttributeNames: names, ExpressionAttributeValues: values,
		}}
	}
	in := &dynamodb.TransactWriteItemsInput{TransactItems: []types.TransactWriteItem{
		update(month, store.QuotaMonthTTL),
		update(day, store.QuotaDayTTL),
	}}
	if err := s.transact(ctx, in); err != nil {
		return fmt.Errorf("dynamo: add usage: %w", err)
	}
	return nil
}

// GetUsage is one BatchGetItem (consistent) of the month and day items of each distinct model.
// A missing item, or one whose ttl has passed but DynamoDB has not deleted yet, reads as zero.
func (s *Stores) GetUsage(ctx context.Context, month, day string, models []string) (store.UsageSnapshot, error) {
	sn := store.UsageSnapshot{Month: map[string]store.UsageDelta{}, Day: map[string]store.UsageDelta{}}
	var keys []map[string]types.AttributeValue
	for _, m := range models {
		if err := store.ValidateUsageModel(m); err != nil {
			return store.UsageSnapshot{}, err
		}
		if _, dup := sn.Month[m]; dup {
			continue
		}
		sn.Month[m], sn.Day[m] = store.UsageDelta{}, store.UsageDelta{}
		keys = append(keys, jobKey(usageID(month, m)), jobKey(usageID(day, m)))
	}
	if len(keys) == 0 {
		return sn, nil
	}
	items, err := s.batchGet(ctx, s.jobs, keys)
	if err != nil {
		return store.UsageSnapshot{}, err
	}
	now := s.Now().Unix()
	for _, it := range items {
		id, ok := it["id"].(*types.AttributeValueMemberS)
		if !ok {
			continue
		}
		var c struct {
			Jobs, Calls, InTok, OutTok, CacheReadTok, CacheWriteTok, CostMicros int64
			TTL                                                                 int64 `dynamodbav:"ttl"`
		}
		if err := attributevalue.UnmarshalMap(it, &c); err != nil {
			return store.UsageSnapshot{}, fmt.Errorf("dynamo: decode usage %s: %w", id.Value, err)
		}
		if c.TTL > 0 && c.TTL <= now {
			continue
		}
		d := store.UsageDelta{Jobs: c.Jobs, Calls: c.Calls, InTok: c.InTok, OutTok: c.OutTok,
			CacheReadTok: c.CacheReadTok, CacheWriteTok: c.CacheWriteTok, CostMicros: c.CostMicros}
		for _, m := range models {
			switch id.Value {
			case usageID(month, m):
				sn.Month[m] = d
			case usageID(day, m):
				sn.Day[m] = d
			}
		}
	}
	return sn, nil
}

// RecentToday is one Query on the day-index: day = :d, newest first, Limit = limit (at most
// MaxRecentToday; the page is the whole answer, so there is no pagination). It reads only
// what the index projects. Never a Scan. A GSI cannot be read consistently.
func (s *Stores) RecentToday(ctx context.Context, day string, limit int) ([]store.JobSummary, error) {
	if day == "" {
		return nil, nil
	}
	if limit <= 0 || limit > store.MaxRecentToday {
		limit = store.MaxRecentToday
	}
	res, err := s.api.Query(ctx, &dynamodb.QueryInput{
		TableName:                 &s.jobs,
		IndexName:                 ptr(dayIndexName),
		KeyConditionExpression:    ptr("#d = :d"),
		ExpressionAttributeNames:  map[string]string{"#d": "day"},
		ExpressionAttributeValues: map[string]types.AttributeValue{":d": strAV(day)},
		ScanIndexForward:          ptr(false),
		Limit:                     ptr(int32(limit)),
	})
	if err != nil {
		return nil, fmt.Errorf("dynamo: recent jobs today: %w", err)
	}
	out := make([]store.JobSummary, 0, len(res.Items))
	for _, m := range res.Items {
		var it struct {
			ID            string `dynamodbav:"id"`
			Status        string `dynamodbav:"status"`
			Kind          string `dynamodbav:"kind"`
			Model         string `dynamodbav:"model"`
			CreatedAt     string `dynamodbav:"createdAt"`
			UpdatedAt     string `dynamodbav:"updatedAt"`
			CostMicros    int64  `dynamodbav:"costMicros"`
			Calls         int    `dynamodbav:"calls"`
			InTok         int64  `dynamodbav:"inTok"`
			OutTok        int64  `dynamodbav:"outTok"`
			CacheReadTok  int64  `dynamodbav:"cacheReadTok"`
			CacheWriteTok int64  `dynamodbav:"cacheWriteTok"`
		}
		if err := attributevalue.UnmarshalMap(m, &it); err != nil {
			return nil, fmt.Errorf("dynamo: decode job summary: %w", err)
		}
		created, err := time.Parse(timeLayout, it.CreatedAt)
		if err != nil {
			return nil, fmt.Errorf("dynamo: job %s createdAt: %w", it.ID, err)
		}
		updated, err := time.Parse(timeLayout, it.UpdatedAt)
		if err != nil {
			return nil, fmt.Errorf("dynamo: job %s updatedAt: %w", it.ID, err)
		}
		out = append(out, store.JobSummary{ID: it.ID, Status: it.Status, Kind: it.Kind, CreatedAt: created, UpdatedAt: updated,
			Model: it.Model, Usage: store.JobUsage{InputTokens: it.InTok, OutputTokens: it.OutTok, CacheReadTokens: it.CacheReadTok,
				CacheWriteTokens: it.CacheWriteTok, CostMicros: it.CostMicros, Calls: it.Calls}})
	}
	return out, nil
}
