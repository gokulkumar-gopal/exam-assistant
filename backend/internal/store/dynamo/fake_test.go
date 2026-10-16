package dynamo

import (
	"context"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"sync"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

// fakeDynamo is an in-process DynamoAPI. It records every request and evaluates the small
// expression grammar the stores use: conditions made of "attribute_exists(x)",
// "attribute_not_exists(x)" and "x = :v" terms joined by AND; update expressions of the
// form "SET a = :a, b = :b REMOVE c, d"; key conditions "pk = :v". Anything else panics,
// so a new expression shape forces the fake to be extended.
type fakeDynamo struct {
	mu     sync.Mutex
	tables map[string]map[string]map[string]types.AttributeValue // table -> key -> item
	calls  []string
	gets   []*dynamodb.GetItemInput
	puts   []*dynamodb.PutItemInput
	txs    []*dynamodb.TransactWriteItemsInput
	qrys   []*dynamodb.QueryInput
	batch  []*dynamodb.BatchGetItemInput
	// unprocessedOnce, when set, makes each BatchGetItem leave the last key unprocessed once.
	unprocessedOnce bool
	pendingRetry    map[string][]map[string]types.AttributeValue
	failTx          error // returned by TransactWriteItems failTxCount times (once when 0)
	failTxCount     int
}

func newFake() *fakeDynamo {
	return &fakeDynamo{tables: map[string]map[string]map[string]types.AttributeValue{}}
}

func (f *fakeDynamo) saw(op string) int {
	n := 0
	for _, c := range f.calls {
		if c == op {
			n++
		}
	}
	return n
}

func keyNames(table string) (pk, sk string) {
	switch {
	case strings.HasSuffix(table, "-users"):
		return "id", ""
	case strings.HasSuffix(table, "-documents"):
		return "ownerId", "id"
	}
	panic("unknown table " + table)
}

func s(av types.AttributeValue) string {
	if v, ok := av.(*types.AttributeValueMemberS); ok {
		return v.Value
	}
	panic(fmt.Sprintf("not a string attribute: %#v", av))
}

func keyOf(table string, item map[string]types.AttributeValue) string {
	pk, sk := keyNames(table)
	k := s(item[pk])
	if sk != "" {
		k += "\x00" + s(item[sk])
	}
	return k
}

func (f *fakeDynamo) tbl(table string) map[string]map[string]types.AttributeValue {
	if f.tables[table] == nil {
		f.tables[table] = map[string]map[string]types.AttributeValue{}
	}
	return f.tables[table]
}

func clone(m map[string]types.AttributeValue) map[string]types.AttributeValue {
	if m == nil {
		return nil
	}
	out := make(map[string]types.AttributeValue, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

func resolve(name string, names map[string]string) string {
	if strings.HasPrefix(name, "#") {
		n, ok := names[name]
		if !ok {
			panic("unbound name " + name)
		}
		return n
	}
	return name
}

// condOK evaluates a condition expression against item (nil when absent).
func condOK(expr string, names map[string]string, vals map[string]types.AttributeValue, item map[string]types.AttributeValue) bool {
	if expr == "" {
		return true
	}
	for _, term := range strings.Split(expr, " AND ") {
		term = strings.TrimSpace(term)
		switch {
		case strings.HasPrefix(term, "attribute_not_exists("):
			a := resolve(strings.TrimSuffix(strings.TrimPrefix(term, "attribute_not_exists("), ")"), names)
			if _, ok := item[a]; ok {
				return false
			}
		case strings.HasPrefix(term, "attribute_exists("):
			a := resolve(strings.TrimSuffix(strings.TrimPrefix(term, "attribute_exists("), ")"), names)
			if _, ok := item[a]; !ok {
				return false
			}
		case strings.Contains(term, " = "):
			parts := strings.SplitN(term, " = ", 2)
			got, ok := item[resolve(strings.TrimSpace(parts[0]), names)]
			want, bound := vals[strings.TrimSpace(parts[1])]
			if !bound {
				panic("unbound value " + parts[1])
			}
			if !ok || !reflect.DeepEqual(got, want) {
				return false
			}
		default:
			panic("unsupported condition term: " + term)
		}
	}
	return true
}

func applyUpdate(expr string, names map[string]string, vals map[string]types.AttributeValue, item map[string]types.AttributeValue) {
	set, remove := expr, ""
	if i := strings.Index(expr, " REMOVE "); i >= 0 {
		set, remove = expr[:i], expr[i+len(" REMOVE "):]
	} else if strings.HasPrefix(expr, "REMOVE ") {
		set, remove = "", strings.TrimPrefix(expr, "REMOVE ")
	}
	if strings.HasPrefix(set, "SET ") {
		for _, a := range strings.Split(strings.TrimPrefix(set, "SET "), ",") {
			parts := strings.SplitN(a, " = ", 2)
			v, ok := vals[strings.TrimSpace(parts[1])]
			if !ok {
				panic("unbound value " + parts[1])
			}
			item[resolve(strings.TrimSpace(parts[0]), names)] = v
		}
	} else if set != "" {
		panic("unsupported update: " + expr)
	}
	if remove != "" {
		for _, a := range strings.Split(remove, ",") {
			delete(item, resolve(strings.TrimSpace(a), names))
		}
	}
}

func failedCondition(rv types.ReturnValuesOnConditionCheckFailure, old map[string]types.AttributeValue) *types.ConditionalCheckFailedException {
	e := &types.ConditionalCheckFailedException{Message: aws.String("The conditional request failed")}
	if rv == types.ReturnValuesOnConditionCheckFailureAllOld && old != nil {
		e.Item = clone(old)
	}
	return e
}

func (f *fakeDynamo) GetItem(_ context.Context, in *dynamodb.GetItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, "GetItem")
	f.gets = append(f.gets, in)
	return &dynamodb.GetItemOutput{Item: clone(f.tbl(*in.TableName)[keyOf(*in.TableName, in.Key)])}, nil
}

func (f *fakeDynamo) PutItem(_ context.Context, in *dynamodb.PutItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.PutItemOutput, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, "PutItem")
	f.puts = append(f.puts, in)
	t, k := f.tbl(*in.TableName), keyOf(*in.TableName, in.Item)
	if !condOK(aws.ToString(in.ConditionExpression), in.ExpressionAttributeNames, in.ExpressionAttributeValues, t[k]) {
		return nil, failedCondition(in.ReturnValuesOnConditionCheckFailure, t[k])
	}
	t[k] = clone(in.Item)
	return &dynamodb.PutItemOutput{}, nil
}

func (f *fakeDynamo) UpdateItem(_ context.Context, in *dynamodb.UpdateItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.UpdateItemOutput, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, "UpdateItem")
	t, k := f.tbl(*in.TableName), keyOf(*in.TableName, in.Key)
	if !condOK(aws.ToString(in.ConditionExpression), in.ExpressionAttributeNames, in.ExpressionAttributeValues, t[k]) {
		return nil, failedCondition(in.ReturnValuesOnConditionCheckFailure, t[k])
	}
	item := t[k]
	if item == nil {
		item = clone(in.Key)
	}
	applyUpdate(aws.ToString(in.UpdateExpression), in.ExpressionAttributeNames, in.ExpressionAttributeValues, item)
	t[k] = item
	return &dynamodb.UpdateItemOutput{}, nil
}

func (f *fakeDynamo) DeleteItem(_ context.Context, in *dynamodb.DeleteItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.DeleteItemOutput, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, "DeleteItem")
	t, k := f.tbl(*in.TableName), keyOf(*in.TableName, in.Key)
	if !condOK(aws.ToString(in.ConditionExpression), in.ExpressionAttributeNames, in.ExpressionAttributeValues, t[k]) {
		return nil, failedCondition(in.ReturnValuesOnConditionCheckFailure, t[k])
	}
	delete(t, k)
	return &dynamodb.DeleteItemOutput{}, nil
}

func (f *fakeDynamo) Query(_ context.Context, in *dynamodb.QueryInput, _ ...func(*dynamodb.Options)) (*dynamodb.QueryOutput, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, "Query")
	f.qrys = append(f.qrys, in)
	table := *in.TableName
	pk, sk := keyNames(table)
	kc := strings.SplitN(aws.ToString(in.KeyConditionExpression), " = ", 2)
	if len(kc) != 2 || resolve(strings.TrimSpace(kc[0]), in.ExpressionAttributeNames) != pk {
		panic("unsupported key condition: " + aws.ToString(in.KeyConditionExpression))
	}
	pkVal := in.ExpressionAttributeValues[strings.TrimSpace(kc[1])]
	var items []map[string]types.AttributeValue
	for _, it := range f.tbl(table) {
		if reflect.DeepEqual(it[pk], pkVal) {
			items = append(items, it)
		}
	}
	sort.Slice(items, func(i, j int) bool { return s(items[i][sk]) < s(items[j][sk]) })
	if in.ScanIndexForward != nil && !*in.ScanIndexForward {
		for i, j := 0, len(items)-1; i < j; i, j = i+1, j-1 {
			items[i], items[j] = items[j], items[i]
		}
	}
	if len(in.ExclusiveStartKey) > 0 {
		after := s(in.ExclusiveStartKey[sk])
		for len(items) > 0 {
			cur := s(items[0][sk])
			skip := cur <= after
			if in.ScanIndexForward != nil && !*in.ScanIndexForward {
				skip = cur >= after
			}
			if !skip {
				break
			}
			items = items[1:]
		}
	}
	out := &dynamodb.QueryOutput{}
	evaluated := 0
	for i, it := range items {
		if in.Limit != nil && evaluated >= int(*in.Limit) { // Limit applies before the filter
			out.LastEvaluatedKey = map[string]types.AttributeValue{pk: it[pk], sk: items[i-1][sk]}
			break
		}
		evaluated++
		if condOK(aws.ToString(in.FilterExpression), in.ExpressionAttributeNames, in.ExpressionAttributeValues, it) {
			out.Items = append(out.Items, clone(it))
		}
	}
	return out, nil
}

func (f *fakeDynamo) BatchGetItem(_ context.Context, in *dynamodb.BatchGetItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.BatchGetItemOutput, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, "BatchGetItem")
	f.batch = append(f.batch, in)
	out := &dynamodb.BatchGetItemOutput{Responses: map[string][]map[string]types.AttributeValue{}}
	total := 0
	for table, ka := range in.RequestItems {
		total += len(ka.Keys)
		seen := map[string]bool{}
		keys := ka.Keys
		for _, key := range keys {
			k := keyOf(table, key)
			if seen[k] {
				return nil, fmt.Errorf("ValidationException: provided list of item keys contains duplicates")
			}
			seen[k] = true
		}
		if f.unprocessedOnce && len(keys) > 1 && !f.retrying(table) {
			last := keys[len(keys)-1]
			keys = keys[:len(keys)-1]
			if out.UnprocessedKeys == nil {
				out.UnprocessedKeys = map[string]types.KeysAndAttributes{}
			}
			out.UnprocessedKeys[table] = types.KeysAndAttributes{Keys: []map[string]types.AttributeValue{last}, ConsistentRead: ka.ConsistentRead}
			f.markRetry(table)
		}
		for _, key := range keys {
			if it, ok := f.tbl(table)[keyOf(table, key)]; ok {
				out.Responses[table] = append(out.Responses[table], clone(it))
			}
		}
	}
	if total > 100 {
		return nil, fmt.Errorf("ValidationException: Too many items requested for the BatchGetItem call (%d)", total)
	}
	return out, nil
}

func (f *fakeDynamo) retrying(table string) bool { _, ok := f.pendingRetry[table]; return ok }
func (f *fakeDynamo) markRetry(table string) {
	if f.pendingRetry == nil {
		f.pendingRetry = map[string][]map[string]types.AttributeValue{}
	}
	f.pendingRetry[table] = nil
}

func (f *fakeDynamo) TransactWriteItems(_ context.Context, in *dynamodb.TransactWriteItemsInput, _ ...func(*dynamodb.Options)) (*dynamodb.TransactWriteItemsOutput, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, "TransactWriteItems")
	f.txs = append(f.txs, in)
	if f.failTx != nil {
		err := f.failTx
		if f.failTxCount > 1 {
			f.failTxCount--
		} else {
			f.failTx = nil
		}
		return nil, err
	}
	reasons := make([]types.CancellationReason, len(in.TransactItems))
	failed := false
	for i, ti := range in.TransactItems {
		reasons[i] = types.CancellationReason{Code: aws.String("None")}
		var table, cond string
		var names map[string]string
		var vals, cur map[string]types.AttributeValue
		var rv types.ReturnValuesOnConditionCheckFailure
		switch {
		case ti.Put != nil:
			table, cond, names, vals, rv = *ti.Put.TableName, aws.ToString(ti.Put.ConditionExpression), ti.Put.ExpressionAttributeNames, ti.Put.ExpressionAttributeValues, ti.Put.ReturnValuesOnConditionCheckFailure
			cur = f.tbl(table)[keyOf(table, ti.Put.Item)]
		case ti.Update != nil:
			table, cond, names, vals, rv = *ti.Update.TableName, aws.ToString(ti.Update.ConditionExpression), ti.Update.ExpressionAttributeNames, ti.Update.ExpressionAttributeValues, ti.Update.ReturnValuesOnConditionCheckFailure
			cur = f.tbl(table)[keyOf(table, ti.Update.Key)]
		default:
			panic("unsupported transact item")
		}
		if !condOK(cond, names, vals, cur) {
			failed = true
			reasons[i] = types.CancellationReason{Code: aws.String("ConditionalCheckFailed")}
			if rv == types.ReturnValuesOnConditionCheckFailureAllOld && cur != nil {
				reasons[i].Item = clone(cur)
			}
		}
	}
	if failed {
		return nil, &types.TransactionCanceledException{CancellationReasons: reasons, Message: aws.String("Transaction cancelled")}
	}
	for _, ti := range in.TransactItems {
		switch {
		case ti.Put != nil:
			f.tbl(*ti.Put.TableName)[keyOf(*ti.Put.TableName, ti.Put.Item)] = clone(ti.Put.Item)
		case ti.Update != nil:
			t, k := f.tbl(*ti.Update.TableName), keyOf(*ti.Update.TableName, ti.Update.Key)
			item := t[k]
			if item == nil {
				item = clone(ti.Update.Key)
			}
			applyUpdate(aws.ToString(ti.Update.UpdateExpression), ti.Update.ExpressionAttributeNames, ti.Update.ExpressionAttributeValues, item)
			t[k] = item
		}
	}
	return &dynamodb.TransactWriteItemsOutput{}, nil
}

var _ DynamoAPI = (*fakeDynamo)(nil)
