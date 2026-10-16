package dynamo

import (
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"

	"github.com/gokulkumar/exam-assistant/backend/internal/store"
)

const (
	usageMonthID = "usage#202610#claude-haiku-4-5"
	usageDayID   = "usage#20261016#claude-haiku-4-5"
)

var usageDelta = store.UsageDelta{Jobs: 1, Calls: 1, InTok: 100, OutTok: 20, CacheReadTok: 5, CacheWriteTok: 7, CostMicros: 900}

func addUsage(t *testing.T, st *Stores, d store.UsageDelta) {
	t.Helper()
	if err := st.AddUsage(bg, "202610", "20261016", "claude-haiku-4-5", d); err != nil {
		t.Fatal(err)
	}
}

func TestAddUsageWritesBothCountersInOneTransaction(t *testing.T) {
	st, f := jobStores()
	addUsage(t, st, usageDelta)
	if f.saw("TransactWriteItems") != 1 || f.saw("UpdateItem") != 0 || f.saw("PutItem") != 0 {
		t.Fatalf("calls = %v; both counters must be written together", f.calls)
	}
	for _, id := range []string{usageMonthID, usageDayID} {
		it := item(f, id)
		if it == nil {
			t.Fatalf("%s missing", id)
		}
		for attr, v := range map[string]int64{"jobs": 1, "calls": 1, "inTok": 100, "outTok": 20, "cacheReadTok": 5, "cacheWriteTok": 7, "costMicros": 900} {
			if got := n(t, it, attr); got != v {
				t.Errorf("%s.%s = %d, want %d", id, attr, got, v)
			}
		}
		for _, a := range []string{"ownerId", "day", "createdAt"} {
			if _, ok := it[a]; ok {
				t.Errorf("%s must not carry %s (it would join an index)", id, a)
			}
		}
	}
}

func TestCountersUseADDNotPut(t *testing.T) {
	st, f := jobStores()
	addUsage(t, st, usageDelta)
	addUsage(t, st, usageDelta)
	if f.saw("PutItem") != 0 {
		t.Fatal("a usage counter must never be written with PutItem")
	}
	for _, tx := range f.txs {
		if len(tx.TransactItems) != 2 {
			t.Fatalf("items = %d, want 2", len(tx.TransactItems))
		}
		for _, ti := range tx.TransactItems {
			if ti.Put != nil || ti.Update == nil {
				t.Fatalf("usage counters are written with Update only: %+v", ti)
			}
			expr := aws.ToString(ti.Update.UpdateExpression)
			if !strings.HasPrefix(expr, "ADD ") {
				t.Errorf("update expression %q does not start with ADD", expr)
			}
			if strings.Contains(strings.ReplaceAll(strings.SplitN(expr, " SET ", 2)[0], "ADD ", ""), " = ") {
				t.Errorf("update expression %q assigns a counter with SET", expr)
			}
		}
	}
	// Two adds sum; a PUT would have left the last value.
	if got := n(t, item(f, usageMonthID), "calls"); got != 2 {
		t.Fatalf("month calls = %d, want 2", got)
	}
	if got := n(t, item(f, usageDayID), "costMicros"); got != 1800 {
		t.Fatalf("day cost = %d, want 1800", got)
	}
}

func TestTTLs(t *testing.T) {
	st, f := jobStores()
	addUsage(t, st, usageDelta)
	if got, want := n(t, item(f, usageMonthID), "ttl"), t0.Add(40*24*time.Hour).Unix(); got != want {
		t.Errorf("month ttl = %d, want %d (40 days)", got, want)
	}
	if got, want := n(t, item(f, usageDayID), "ttl"), t0.Add(3*24*time.Hour).Unix(); got != want {
		t.Errorf("day ttl = %d, want %d (3 days)", got, want)
	}
	st.Now = func() time.Time { return t0.Add(time.Hour) }
	addUsage(t, st, usageDelta)
	if got, want := n(t, item(f, usageDayID), "ttl"), t0.Add(3*24*time.Hour).Unix(); got != want {
		t.Errorf("a later add moved the day ttl to %d, want %d", got, want)
	}
	// An expired counter that DynamoDB has not deleted yet reads as zero.
	st.Now = func() time.Time { return t0.Add(3*24*time.Hour + time.Second) }
	sn, err := st.GetUsage(bg, "202610", "20261016", []string{"claude-haiku-4-5"})
	if err != nil {
		t.Fatal(err)
	}
	if sn.Day["claude-haiku-4-5"] != (store.UsageDelta{}) || sn.Month["claude-haiku-4-5"].Calls != 2 {
		t.Errorf("after the day ttl: day %+v month %+v", sn.Day, sn.Month)
	}
}

func TestGetUsageIsOneBatchGetOnTheJobsTable(t *testing.T) {
	st, f := jobStores()
	addUsage(t, st, usageDelta)
	sn, err := st.GetUsage(bg, "202610", "20261016", []string{"claude-haiku-4-5", "gemini-3.1-flash-lite", "claude-haiku-4-5"})
	if err != nil {
		t.Fatal(err)
	}
	if f.saw("BatchGetItem") != 1 || f.saw("GetItem") != 0 || f.saw("Query") != 0 {
		t.Fatalf("calls = %v", f.calls)
	}
	ka := f.batch[0].RequestItems["test-jobs"]
	if len(f.batch[0].RequestItems) != 1 || len(ka.Keys) != 4 {
		t.Fatalf("request = %+v, want 4 keys (2 models x month, day) on test-jobs", f.batch[0].RequestItems)
	}
	if sn.Month["claude-haiku-4-5"] != usageDelta || sn.Day["claude-haiku-4-5"] != usageDelta {
		t.Fatalf("snapshot = %+v", sn)
	}
}

func TestJobItemCarriesDayAndModelAtCreation(t *testing.T) {
	st, f := jobStores()
	j := newJob("j1", "u1")
	if err := st.CreateJobWithQuota(bg, j, quotaFor("u1")); err != nil {
		t.Fatal(err)
	}
	it := item(f, "j1")
	if got := s(it["day"]); got != "20261016" {
		t.Errorf("day = %q", got)
	}
	if got := s(it["model"]); got != "gemini-3.1-flash-lite" {
		t.Errorf("model = %q", got)
	}
	// They are on the Put of the creating transaction, not added by a later write.
	put := f.txs[0].TransactItems[0].Put
	if s(put.Item["day"]) != "20261016" || s(put.Item["model"]) != "gemini-3.1-flash-lite" {
		t.Errorf("the create Put lacks day or model: %v", put.Item)
	}
	got, err := st.GetJob(bg, "j1")
	if err != nil || got.Day != "20261016" || got.Model != "gemini-3.1-flash-lite" {
		t.Errorf("GetJob = %+v %v", got, err)
	}
}

func TestRecentTodayQueriesDayIndexNewestFirstLimit50(t *testing.T) {
	st, f := jobStores()
	for i, id := range []string{"j1", "j2", "j3"} {
		j := newJob(id, "u1")
		j.CreatedAt = t0.Add(time.Duration(i) * time.Minute)
		j.UpdatedAt = j.CreatedAt
		if err := st.CreateJobWithQuota(bg, j, quotaFor("u"+id)); err != nil {
			t.Fatal(err)
		}
	}
	got, err := st.RecentToday(bg, "20261016", store.MaxRecentToday)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[0].ID != "j3" || got[2].ID != "j1" {
		t.Fatalf("got %+v", got)
	}
	if f.saw("Query") != 1 || f.saw("GetItem") != 0 || f.saw("BatchGetItem") != 0 {
		t.Fatalf("calls = %v; a single Query and nothing else", f.calls)
	}
	q := f.qrys[0]
	if aws.ToString(q.IndexName) != "day-index" || aws.ToString(q.TableName) != "test-jobs" {
		t.Errorf("index = %q table = %q", aws.ToString(q.IndexName), aws.ToString(q.TableName))
	}
	if q.ScanIndexForward == nil || *q.ScanIndexForward {
		t.Error("ScanIndexForward must be false (newest first)")
	}
	if q.Limit == nil || *q.Limit != 50 {
		t.Errorf("Limit = %v, want 50", q.Limit)
	}
	if q.ConsistentRead != nil && *q.ConsistentRead {
		t.Error("a GSI cannot be read consistently")
	}
	if aws.ToString(q.KeyConditionExpression) != "#d = :d" || q.ExpressionAttributeNames["#d"] != "day" ||
		s(q.ExpressionAttributeValues[":d"]) != "20261016" {
		t.Errorf("key condition = %q %v %v", aws.ToString(q.KeyConditionExpression), q.ExpressionAttributeNames, q.ExpressionAttributeValues)
	}
	// An oversize or non-positive limit is 50, never unbounded.
	for _, lim := range []int{0, -1, 500} {
		if _, err := st.RecentToday(bg, "20261016", lim); err != nil {
			t.Fatal(err)
		}
		if l := f.qrys[len(f.qrys)-1].Limit; l == nil || *l != 50 {
			t.Errorf("limit %d sent Limit %v, want 50", lim, l)
		}
	}
}

func TestDayIndexProjectionIsInclude(t *testing.T) {
	text := templateText()
	i := strings.Index(text, "IndexName: day-index")
	if i < 0 {
		t.Fatal("day-index missing from template.yaml")
	}
	block := text[i:]
	if j := strings.Index(block, "\nOutputs:"); j > 0 {
		block = block[:j]
	}
	if !strings.Contains(block, "ProjectionType: INCLUDE") || strings.Contains(block, "ProjectionType: ALL") {
		t.Errorf("day-index must project INCLUDE (the usage page never reads owners or requests): %s", block)
	}
	// What RecentToday reads must be keys or projected: id, day and createdAt are keys.
	need := []string{"status", "updatedAt", "kind", "model", "costMicros", "calls", "inTok", "outTok", "cacheReadTok", "cacheWriteTok"}
	have := map[string]bool{}
	for _, a := range listFrom(t, "day-index") {
		have[a] = true
	}
	for _, a := range need {
		if !have[a] {
			t.Errorf("day-index does not project %s", a)
		}
	}
	for _, banned := range []string{"ownerId", "request", "failedSources", "error"} {
		if have[banned] {
			t.Errorf("day-index must not project %s (aggregate only)", banned)
		}
	}
	// Seen through the fake, which projects per the template: the summary has the values.
	st, _ := jobStores()
	j := newJob("j1", "u1")
	j.Usage = store.JobUsage{InputTokens: 9, CostMicros: 4, Calls: 1}
	if err := st.CreateJobWithQuota(bg, j, quotaFor("u1")); err != nil {
		t.Fatal(err)
	}
	got, err := st.RecentToday(bg, "20261016", 50)
	if err != nil || len(got) != 1 {
		t.Fatalf("%v %v", got, err)
	}
	g := got[0]
	if g.Model != "gemini-3.1-flash-lite" || g.Usage.InputTokens != 9 || g.Usage.CostMicros != 4 || g.Usage.Calls != 1 ||
		!g.UpdatedAt.Equal(t0) || !g.CreatedAt.Equal(t0) || g.Status != store.JobQueued || g.Kind != store.KindGenerate {
		t.Errorf("summary = %+v", g)
	}
}
