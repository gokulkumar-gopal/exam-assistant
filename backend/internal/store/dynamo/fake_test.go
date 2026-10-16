package dynamo

import (
	"context"
	"fmt"
	"os"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

// fakeDynamo is an in-process DynamoAPI. It records every request and evaluates the small
// expression grammar the stores use: conditions made of "attribute_exists(x)",
// "attribute_not_exists(x)", "x = :v" and "x < :v" terms joined by AND, groups of those
// joined by OR (OR binds loosest, no parentheses); update expressions made of SET (values
// ":v" or "if_not_exists(a, :v)"), ADD (numbers) and REMOVE clauses; key conditions
// "pk = :v" on the table or on a GSI (projected per the INCLUDE lists in template.yaml).
// Also: "x <> :v", "size(x) < :v", "NOT contains(x, :v)" and SET list_append(x, :v).
// Anything else panics,
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
	// alwaysUnprocessed leaves the last key of every BatchGetItem unprocessed, forever.
	alwaysUnprocessed bool
	pendingRetry      map[string][]map[string]types.AttributeValue
	failTx            error // returned by TransactWriteItems failTxCount times (once when 0)
	failTxCount       int
	upds              []*dynamodb.UpdateItemInput
	// beforeUpdate, when set, runs at the start of every UpdateItem with the fake's lock held
	// (it may edit f.tables directly), to inject a concurrent writer between two calls.
	beforeUpdate    func(f *fakeDynamo, in *dynamodb.UpdateItemInput)
	failUpdate      error // returned by UpdateItem failUpdateCount times (once when 0), before any write
	failUpdateCount int
	// queryPageSize, when > 0, caps the items one Query evaluates, to exercise pagination.
	queryPageSize int
	indexes       map[string]indexDef
}

// indexDef is a GSI: its keys and the non-key attributes it projects (INCLUDE).
type indexDef struct {
	pk, sk string
	proj   []string
}

func newFake() *fakeDynamo {
	return &fakeDynamo{tables: map[string]map[string]map[string]types.AttributeValue{}, indexes: map[string]indexDef{
		"owner-index":   {pk: "ownerId", sk: "id", proj: templateIncludeList("owner-index")},
		"day-index":     {pk: "day", sk: "createdAt", proj: templateIncludeList("day-index")},
		"creator-index": {pk: "creatorId", sk: "id", proj: templateIncludeList("creator-index")},
		"user-index":    {pk: "userId", sk: "examId", proj: templateIncludeList("user-index")},
	}}
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
	case strings.HasSuffix(table, "-jobs"), strings.HasSuffix(table, "-exams"):
		return "id", ""
	case strings.HasSuffix(table, "-attempts"):
		return "examId", "userId"
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
	for _, group := range strings.Split(expr, " OR ") {
		ok := true
		for _, term := range strings.Split(group, " AND ") {
			if !termOK(strings.TrimSpace(term), names, vals, item) {
				ok = false
				break
			}
		}
		if ok {
			return true
		}
	}
	return false
}

func num(av types.AttributeValue) int64 {
	n, ok := av.(*types.AttributeValueMemberN)
	if !ok {
		panic(fmt.Sprintf("not a number attribute: %#v", av))
	}
	v, err := strconv.ParseInt(n.Value, 10, 64)
	if err != nil {
		panic(err)
	}
	return v
}

// listLen returns the length of a list attribute; ok=false when absent or not a list.
func listLen(av types.AttributeValue, ok bool) (int64, bool) {
	l, isList := av.(*types.AttributeValueMemberL)
	if !ok || !isList {
		return 0, false
	}
	return int64(len(l.Value)), true
}

func termOK(term string, names map[string]string, vals map[string]types.AttributeValue, item map[string]types.AttributeValue) bool {
	switch {
	case strings.HasPrefix(term, "NOT contains("):
		args := strings.Split(strings.TrimSuffix(strings.TrimPrefix(term, "NOT contains("), ")"), ",")
		l, isList := item[resolve(strings.TrimSpace(args[0]), names)].(*types.AttributeValueMemberL)
		want, bound := vals[strings.TrimSpace(args[1])]
		if !bound {
			panic("unbound value " + args[1])
		}
		if isList {
			for _, e := range l.Value {
				if reflect.DeepEqual(e, want) {
					return false
				}
			}
		}
		return true
	case strings.HasPrefix(term, "size(") && strings.Contains(term, ") < "):
		parts := strings.SplitN(term, ") < ", 2)
		a := resolve(strings.TrimPrefix(parts[0], "size("), names)
		n, ok := listLen(item[a], item[a] != nil)
		want, bound := vals[strings.TrimSpace(parts[1])]
		if !bound {
			panic("unbound value " + parts[1])
		}
		return ok && n < num(want)
	case strings.Contains(term, " <> "):
		parts := strings.SplitN(term, " <> ", 2)
		got, ok := item[resolve(strings.TrimSpace(parts[0]), names)]
		want, bound := vals[strings.TrimSpace(parts[1])]
		if !bound {
			panic("unbound value " + parts[1])
		}
		return ok && !reflect.DeepEqual(got, want)
	case strings.HasPrefix(term, "attribute_not_exists("):
		a := resolve(strings.TrimSuffix(strings.TrimPrefix(term, "attribute_not_exists("), ")"), names)
		_, ok := item[a]
		return !ok
	case strings.HasPrefix(term, "attribute_exists("):
		a := resolve(strings.TrimSuffix(strings.TrimPrefix(term, "attribute_exists("), ")"), names)
		_, ok := item[a]
		return ok
	case strings.Contains(term, " < "):
		parts := strings.SplitN(term, " < ", 2)
		got, ok := item[resolve(strings.TrimSpace(parts[0]), names)]
		want, bound := vals[strings.TrimSpace(parts[1])]
		if !bound {
			panic("unbound value " + parts[1])
		}
		return ok && num(got) < num(want)
	case strings.Contains(term, " = "):
		parts := strings.SplitN(term, " = ", 2)
		got, ok := item[resolve(strings.TrimSpace(parts[0]), names)]
		want, bound := vals[strings.TrimSpace(parts[1])]
		if !bound {
			panic("unbound value " + parts[1])
		}
		return ok && reflect.DeepEqual(got, want)
	}
	panic("unsupported condition term: " + term)
}

// splitTop splits on commas outside parentheses.
func splitTop(s string) []string {
	var out []string
	depth, start := 0, 0
	for i, c := range s {
		switch c {
		case '(':
			depth++
		case ')':
			depth--
		case ',':
			if depth == 0 {
				out = append(out, s[start:i])
				start = i + 1
			}
		}
	}
	return append(out, s[start:])
}

// applyUpdate applies SET / ADD / REMOVE clauses and returns the attributes it touched.
func applyUpdate(expr string, names map[string]string, vals map[string]types.AttributeValue, item map[string]types.AttributeValue) []string {
	var touched []string
	re := regexp.MustCompile(`(?:^|\s)(SET|ADD|REMOVE)\s`)
	locs := re.FindAllStringSubmatchIndex(expr, -1)
	if len(locs) == 0 || locs[0][0] != 0 {
		panic("unsupported update: " + expr)
	}
	val := func(tok string) types.AttributeValue {
		v, ok := vals[strings.TrimSpace(tok)]
		if !ok {
			panic("unbound value " + tok)
		}
		return v
	}
	for i, loc := range locs {
		end := len(expr)
		if i+1 < len(locs) {
			end = locs[i+1][0]
		}
		body := strings.TrimSpace(expr[loc[1]:end])
		for _, part := range splitTop(body) {
			part = strings.TrimSpace(part)
			switch expr[loc[2]:loc[3]] {
			case "SET":
				kv := strings.SplitN(part, " = ", 2)
				attr, rhs := resolve(strings.TrimSpace(kv[0]), names), strings.TrimSpace(kv[1])
				if strings.HasPrefix(rhs, "if_not_exists(") {
					args := strings.Split(strings.TrimSuffix(strings.TrimPrefix(rhs, "if_not_exists("), ")"), ",")
					if resolve(strings.TrimSpace(args[0]), names) != attr {
						panic("unsupported if_not_exists on another attribute: " + rhs)
					}
					if _, has := item[attr]; has {
						continue
					}
					rhs = strings.TrimSpace(args[1])
				}
				if strings.HasPrefix(rhs, "list_append(") {
					args := strings.Split(strings.TrimSuffix(strings.TrimPrefix(rhs, "list_append("), ")"), ",")
					if resolve(strings.TrimSpace(args[0]), names) != attr {
						panic("unsupported list_append on another attribute: " + rhs)
					}
					cur, _ := item[attr].(*types.AttributeValueMemberL)
					add, _ := val(args[1]).(*types.AttributeValueMemberL)
					merged := &types.AttributeValueMemberL{}
					if cur != nil {
						merged.Value = append(merged.Value, cur.Value...)
					}
					merged.Value = append(merged.Value, add.Value...)
					item[attr] = merged
					touched = append(touched, attr)
					continue
				}
				item[attr] = val(rhs)
				touched = append(touched, attr)
			case "ADD":
				kv := strings.Fields(part)
				attr := resolve(kv[0], names)
				cur := int64(0)
				if old, ok := item[attr]; ok {
					cur = num(old)
				}
				item[attr] = &types.AttributeValueMemberN{Value: strconv.FormatInt(cur+num(val(kv[1])), 10)}
				touched = append(touched, attr)
			case "REMOVE":
				attr := resolve(part, names)
				delete(item, attr)
			}
		}
	}
	return touched
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
	f.upds = append(f.upds, in)
	if f.beforeUpdate != nil {
		f.beforeUpdate(f, in)
	}
	if f.failUpdate != nil {
		err := f.failUpdate
		if f.failUpdateCount > 1 {
			f.failUpdateCount--
		} else {
			f.failUpdate = nil
		}
		return nil, err
	}
	t, k := f.tbl(*in.TableName), keyOf(*in.TableName, in.Key)
	if !condOK(aws.ToString(in.ConditionExpression), in.ExpressionAttributeNames, in.ExpressionAttributeValues, t[k]) {
		return nil, failedCondition(in.ReturnValuesOnConditionCheckFailure, t[k])
	}
	item := t[k]
	if item == nil {
		item = clone(in.Key)
	}
	touched := applyUpdate(aws.ToString(in.UpdateExpression), in.ExpressionAttributeNames, in.ExpressionAttributeValues, item)
	t[k] = item
	out := &dynamodb.UpdateItemOutput{}
	if in.ReturnValues == types.ReturnValueUpdatedNew {
		out.Attributes = map[string]types.AttributeValue{}
		for _, a := range touched {
			out.Attributes[a] = item[a]
		}
	}
	return out, nil
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
	var idx *indexDef
	if in.IndexName != nil {
		d, ok := f.indexes[*in.IndexName]
		if !ok {
			panic("unknown index " + *in.IndexName)
		}
		if aws.ToBool(in.ConsistentRead) {
			return nil, fmt.Errorf("ValidationException: Consistent reads are not supported on global secondary indexes")
		}
		idx = &d
		pk, sk = d.pk, d.sk
	}
	kc := strings.SplitN(aws.ToString(in.KeyConditionExpression), " = ", 2)
	if len(kc) != 2 || resolve(strings.TrimSpace(kc[0]), in.ExpressionAttributeNames) != pk {
		panic("unsupported key condition: " + aws.ToString(in.KeyConditionExpression))
	}
	pkVal := in.ExpressionAttributeValues[strings.TrimSpace(kc[1])]
	var items []map[string]types.AttributeValue
	for _, it := range f.tbl(table) {
		if reflect.DeepEqual(it[pk], pkVal) {
			if idx != nil {
				it = project(table, it, *idx)
			}
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
		limit := 0
		if in.Limit != nil {
			limit = int(*in.Limit)
		}
		if f.queryPageSize > 0 && (limit == 0 || f.queryPageSize < limit) {
			limit = f.queryPageSize
		}
		if limit > 0 && evaluated >= limit { // Limit applies before the filter
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

// project returns what a GSI holds for item: the table key, the index keys and the INCLUDE
// attributes. Items without the index partition key never reach the index (the caller's
// DeepEqual on a missing attribute already excludes them).
func project(table string, item map[string]types.AttributeValue, idx indexDef) map[string]types.AttributeValue {
	pk, sk := keyNames(table)
	out := map[string]types.AttributeValue{}
	for _, a := range append([]string{pk, sk, idx.pk, idx.sk}, idx.proj...) {
		if v, ok := item[a]; ok && a != "" {
			out[a] = v
		}
	}
	return out
}

// templateIncludeList reads the NonKeyAttributes of a GSI from backend/template.yaml, so the
// fake projects exactly what the deployed index would.
func templateIncludeList(index string) []string {
	b := []byte(templateText())
	re := regexp.MustCompile(`IndexName: ` + regexp.QuoteMeta(index) + `\b[\s\S]*?NonKeyAttributes: \[([^\]]*)\]`)
	m := re.FindStringSubmatch(string(b))
	if m == nil {
		panic("index " + index + " not found in template.yaml")
	}
	var out []string
	for _, a := range strings.Split(m[1], ",") {
		out = append(out, strings.TrimSpace(a))
	}
	return out
}

func templateText() string {
	b, err := os.ReadFile(templatePath)
	if err != nil {
		panic(err)
	}
	return string(b)
}

const templatePath = "../../../template.yaml" // backend/template.yaml, relative to this package

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
		if (f.alwaysUnprocessed && len(keys) > 0) || (f.unprocessedOnce && len(keys) > 1 && !f.retrying(table)) {
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
	seenTargets := map[string]bool{}
	for _, ti := range in.TransactItems {
		var t, k string
		switch {
		case ti.Put != nil:
			t, k = *ti.Put.TableName, keyOf(*ti.Put.TableName, ti.Put.Item)
		case ti.Update != nil:
			t, k = *ti.Update.TableName, keyOf(*ti.Update.TableName, ti.Update.Key)
		}
		if seenTargets[t+"/"+k] {
			return nil, fmt.Errorf("ValidationException: Transaction request cannot include multiple operations on one item")
		}
		seenTargets[t+"/"+k] = true
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
