// Package dynamo implements store.UserStore and store.DocStore on DynamoDB
// (backend-spec 6.2). It depends on a narrow DynamoAPI so tests run against an
// in-process fake and no AWS access is needed.
package dynamo

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"github.com/gokulkumar/exam-assistant/backend/internal/store"
)

// DynamoAPI is the subset of *dynamodb.Client the stores use.
type DynamoAPI interface {
	GetItem(ctx context.Context, in *dynamodb.GetItemInput, optFns ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error)
	PutItem(ctx context.Context, in *dynamodb.PutItemInput, optFns ...func(*dynamodb.Options)) (*dynamodb.PutItemOutput, error)
	UpdateItem(ctx context.Context, in *dynamodb.UpdateItemInput, optFns ...func(*dynamodb.Options)) (*dynamodb.UpdateItemOutput, error)
	DeleteItem(ctx context.Context, in *dynamodb.DeleteItemInput, optFns ...func(*dynamodb.Options)) (*dynamodb.DeleteItemOutput, error)
	Query(ctx context.Context, in *dynamodb.QueryInput, optFns ...func(*dynamodb.Options)) (*dynamodb.QueryOutput, error)
	BatchGetItem(ctx context.Context, in *dynamodb.BatchGetItemInput, optFns ...func(*dynamodb.Options)) (*dynamodb.BatchGetItemOutput, error)
	TransactWriteItems(ctx context.Context, in *dynamodb.TransactWriteItemsInput, optFns ...func(*dynamodb.Options)) (*dynamodb.TransactWriteItemsOutput, error)
}

var _ DynamoAPI = (*dynamodb.Client)(nil)

const (
	batchGetLimit    = 100 // BatchGetItem accepts at most 100 keys per call
	maxBatchAttempts = 8   // rounds of UnprocessedKeys retries before giving up
	condFailedCode   = "ConditionalCheckFailed"
	emailLockPrefix  = "email#"
	googleLockPrefix = "google#"
	timeLayout       = time.RFC3339Nano
)

// Stores implements store.UserStore and store.DocStore.
type Stores struct {
	api   DynamoAPI
	users string // table names: <STAGE>-users, <STAGE>-documents
	docs  string
	// Now reports the current time; it decides whether a pending document's ttl has passed
	// (DynamoDB deletes expired items lazily, so reads must filter them).
	Now func() time.Time
}

var (
	_ store.UserStore = (*Stores)(nil)
	_ store.DocStore  = (*Stores)(nil)
)

// New returns Stores over api using tables "<stage>-users" and "<stage>-documents".
func New(api DynamoAPI, stage string) *Stores {
	return &Stores{api: api, users: stage + "-users", docs: stage + "-documents", Now: time.Now}
}

// isCondFailed reports whether err is a single-item conditional check failure and returns it.
func isCondFailed(err error) (*types.ConditionalCheckFailedException, bool) {
	var ce *types.ConditionalCheckFailedException
	if errors.As(err, &ce) {
		return ce, true
	}
	return nil, false
}

// cancelReasons returns the per-item cancellation reasons of a cancelled transaction.
func cancelReasons(err error) ([]types.CancellationReason, bool) {
	var tc *types.TransactionCanceledException
	if errors.As(err, &tc) {
		return tc.CancellationReasons, true
	}
	return nil, false
}

func reasonIsCondFailed(rs []types.CancellationReason, i int) bool {
	return i < len(rs) && rs[i].Code != nil && *rs[i].Code == condFailedCode
}

// batchGet reads keys from table in chunks of 100 with consistent reads, retrying
// UnprocessedKeys with a short backoff. Keys must be unique.
func (s *Stores) batchGet(ctx context.Context, table string, keys []map[string]types.AttributeValue) ([]map[string]types.AttributeValue, error) {
	var out []map[string]types.AttributeValue
	for start := 0; start < len(keys); start += batchGetLimit {
		end := min(start+batchGetLimit, len(keys))
		pending := map[string]types.KeysAndAttributes{
			table: {Keys: keys[start:end], ConsistentRead: ptr(true)},
		}
		for attempt := 0; len(pending) > 0; attempt++ {
			if attempt >= maxBatchAttempts {
				return nil, fmt.Errorf("dynamo: batch get on %s: unprocessed keys remain after %d attempts", table, attempt)
			}
			if attempt > 0 {
				select {
				case <-ctx.Done():
					return nil, ctx.Err()
				case <-time.After(time.Duration(5<<attempt) * time.Millisecond):
				}
			}
			res, err := s.api.BatchGetItem(ctx, &dynamodb.BatchGetItemInput{RequestItems: pending})
			if err != nil {
				return nil, fmt.Errorf("dynamo: batch get on %s: %w", table, err)
			}
			out = append(out, res.Responses[table]...)
			pending = res.UnprocessedKeys
		}
	}
	return out, nil
}

func ptr[T any](v T) *T { return &v }

func isLockID(id string) bool {
	return strings.HasPrefix(id, emailLockPrefix) || strings.HasPrefix(id, googleLockPrefix)
}
