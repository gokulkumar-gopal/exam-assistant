package dynamo

import (
	"context"
	"errors"
	"fmt"
	"os"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"github.com/gokulkumar/exam-assistant/backend/internal/store"
	"github.com/gokulkumar/exam-assistant/backend/internal/store/storetest"
)

var t0 = storetest.T0

func jobStores() (*Stores, *fakeDynamo) {
	s, f := newStores()
	s.Now = func() time.Time { return t0 }
	s.sleep = func(_ context.Context, _ time.Duration) error { return nil }
	return s, f
}

func newJob(id, owner string) store.Job {
	return store.Job{ID: id, OwnerID: owner, Kind: store.KindGenerate, Model: "gemini-3.1-flash-lite", Day: "20261016",
		Status: store.JobQueued, Request: store.GenerateRequest{SourceIDs: []string{"d1"}, Type: "free", Counts: map[string]int{"2": 3}},
		Total: 3, CreatedAt: t0, UpdatedAt: t0, TTL: t0.Add(24 * time.Hour).Unix()}
}

func quotaFor(user string) store.QuotaCheck {
	return store.QuotaCheck{UserID: user, Day: "20261016", Month: "202610", MaxUser: 3, MaxRegen: 10, MaxGlobal: 10, BudgetMicros: 15_000_000}
}

func item(f *fakeDynamo, id string) map[string]types.AttributeValue { return f.tables["test-jobs"][id] }

func n(t *testing.T, it map[string]types.AttributeValue, attr string) int64 {
	t.Helper()
	av, ok := it[attr]
	if !ok {
		t.Fatalf("attribute %s missing in %v", attr, it)
	}
	return num(av)
}

func TestCreateJobIncrementsCountersAtomically(t *testing.T) {
	st, f := jobStores()
	if err := st.CreateJobWithQuota(bg, newJob("j1", "u1"), quotaFor("u1")); err != nil {
		t.Fatal(err)
	}
	if f.saw("TransactWriteItems") != 1 || len(f.txs[0].TransactItems) != 4 || f.saw("PutItem") != 0 || f.saw("UpdateItem") != 0 {
		t.Fatalf("calls = %v", f.calls)
	}
	ti := f.txs[0].TransactItems
	if ti[0].Put == nil || aws.ToString(ti[0].Put.ConditionExpression) != "attribute_not_exists(id)" {
		t.Fatalf("item 0 must be the conditional job put: %+v", ti[0])
	}
	for i := 1; i < 4; i++ {
		if ti[i].Update == nil || *ti[i].Update.TableName != "test-jobs" || aws.ToString(ti[i].Update.ConditionExpression) == "" {
			t.Fatalf("item %d must be a conditional update on test-jobs: %+v", i, ti[i])
		}
	}
	if n(t, item(f, "quota#u1#20261016"), "generate") != 1 || n(t, item(f, "quota#global#20261016"), "jobs") != 1 {
		t.Fatalf("counters: %v %v", item(f, "quota#u1#20261016"), item(f, "quota#global#20261016"))
	}
	if _, has := item(f, "quota#u1#20261016")["regen"]; has {
		t.Fatal("generate must not touch regen")
	}
	if _, has := item(f, "quota#global#202610")["spendMicros"]; has {
		t.Fatal("job creation must not add spend")
	}
	if item(f, "j1") == nil {
		t.Fatal("job missing")
	}
	// regenerate uses the regen attribute of the same item
	q := quotaFor("u1")
	q.Regen = true
	if err := st.CreateJobWithQuota(bg, newJob("j2", "u1"), q); err != nil {
		t.Fatal(err)
	}
	it := item(f, "quota#u1#20261016")
	if n(t, it, "generate") != 1 || n(t, it, "regen") != 1 || n(t, item(f, "quota#global#20261016"), "jobs") != 2 {
		t.Fatalf("counters: %v", it)
	}
	if f.saw("Scan") != 0 {
		t.Fatal("Scan used")
	}
}

func cancelled(codes ...string) error {
	rs := make([]types.CancellationReason, len(codes))
	for i, c := range codes {
		rs[i] = types.CancellationReason{Code: aws.String(c)}
	}
	return &types.TransactionCanceledException{CancellationReasons: rs}
}

func TestQuotaErrorKindMapsFromCancellationReasons(t *testing.T) {
	const cf = "ConditionalCheckFailed"
	cases := []struct {
		name  string
		regen bool
		err   error
		want  string // quota kind; "conflict" for ErrConflict
	}{
		{"user", false, cancelled("None", cf, "None", "None"), store.QuotaUser},
		{"regen", true, cancelled("None", cf, "None", "None"), store.QuotaRegen},
		{"global", false, cancelled("None", "None", cf, "None"), store.QuotaGlobal},
		{"budget", false, cancelled("None", "None", "None", cf), store.QuotaBudget},
		{"first failure wins", false, cancelled("None", cf, cf, cf), store.QuotaUser},
		{"job exists", false, cancelled(cf, "None", "None", "None"), "conflict"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			st, f := jobStores()
			f.failTx = c.err
			q := quotaFor("u1")
			q.Regen = c.regen
			err := st.CreateJobWithQuota(bg, newJob("j1", "u1"), q)
			if c.want == "conflict" {
				if !errors.Is(err, store.ErrConflict) {
					t.Fatalf("err = %v", err)
				}
				return
			}
			var qe *store.QuotaError
			if !errors.As(err, &qe) || qe.Kind != c.want {
				t.Fatalf("err = %v, want kind %s", err, c.want)
			}
		})
	}
	// an unrelated cancellation is neither a quota error nor a conflict
	st, f := jobStores()
	f.failTx = cancelled("None", "ValidationError", "None", "None")
	err := st.CreateJobWithQuota(bg, newJob("j1", "u1"), quotaFor("u1"))
	var qe *store.QuotaError
	if err == nil || errors.As(err, &qe) || errors.Is(err, store.ErrConflict) {
		t.Fatalf("err = %v", err)
	}
}

func TestCreateJobRetriesTransactionConflict(t *testing.T) {
	st, f := jobStores()
	f.failTx, f.failTxCount = cancelled("None", "TransactionConflict", "None", "None"), 2
	if err := st.CreateJobWithQuota(bg, newJob("j1", "u1"), quotaFor("u1")); err != nil {
		t.Fatal(err)
	}
	if f.saw("TransactWriteItems") != 3 {
		t.Fatalf("attempts = %d", f.saw("TransactWriteItems"))
	}
}

func TestBudgetCondition(t *testing.T) {
	st, f := jobStores()
	_ = st.CreateJobWithQuota(bg, newJob("j1", "u1"), quotaFor("u1"))
	u := f.txs[0].TransactItems[3].Update
	cond := aws.ToString(u.ConditionExpression)
	if cond != "attribute_not_exists(spendMicros) OR spendMicros < :budget" {
		t.Fatalf("budget condition = %q", cond)
	}
	if got := u.ExpressionAttributeValues[":budget"].(*types.AttributeValueMemberN).Value; got != "15000000" {
		t.Fatalf(":budget = %s", got)
	}
}

func TestQuotaItemsHaveTTLs(t *testing.T) {
	st, f := jobStores()
	if err := st.CreateJobWithQuota(bg, newJob("j1", "u1"), quotaFor("u1")); err != nil {
		t.Fatal(err)
	}
	day, mon := t0.Add(3*24*time.Hour).Unix(), t0.Add(40*24*time.Hour).Unix()
	if got := n(t, item(f, "quota#u1#20261016"), "ttl"); got != day {
		t.Fatalf("user day ttl = %d, want %d", got, day)
	}
	if got := n(t, item(f, "quota#global#20261016"), "ttl"); got != day {
		t.Fatalf("global day ttl = %d, want %d", got, day)
	}
	if got := n(t, item(f, "quota#global#202610"), "ttl"); got != mon {
		t.Fatalf("month ttl = %d, want %d", got, mon)
	}
	// AddSpend on a fresh month also sets the 40-day ttl
	if _, err := st.AddSpend(bg, "202611", 5); err != nil {
		t.Fatal(err)
	}
	if got := n(t, item(f, "quota#global#202611"), "ttl"); got != mon {
		t.Fatalf("AddSpend ttl = %d, want %d", got, mon)
	}
	// a later write must not push the ttl out
	st.Now = func() time.Time { return t0.Add(24 * time.Hour) }
	_, _ = st.AddSpend(bg, "202611", 5)
	if got := n(t, item(f, "quota#global#202611"), "ttl"); got != mon {
		t.Fatalf("ttl moved to %d", got)
	}
}

func TestAddSpendIsADD(t *testing.T) {
	st, f := jobStores()
	total, err := st.AddSpend(bg, "202610", 1234)
	if err != nil || total != 1234 {
		t.Fatalf("total = %d, err = %v", total, err)
	}
	if f.saw("UpdateItem") != 1 || f.saw("GetItem") != 0 || f.saw("PutItem") != 0 {
		t.Fatalf("AddSpend must be one UpdateItem, no read-modify-write: %v", f.calls)
	}
	u := f.upds[0]
	if !strings.Contains(aws.ToString(u.UpdateExpression), "ADD spendMicros :n") {
		t.Fatalf("expression = %q", aws.ToString(u.UpdateExpression))
	}
	if aws.ToString(u.TableName) != "test-jobs" || s(u.Key["id"]) != "quota#global#202610" || u.ReturnValues != types.ReturnValueUpdatedNew {
		t.Fatalf("input = %+v", u)
	}
}

func TestJobReadsAreConsistentAndOwnerListIsAnIndexQuery(t *testing.T) {
	st, f := jobStores()
	_ = st.CreateJobWithQuota(bg, newJob("j1", "u1"), quotaFor("u1"))
	_ = st.PutQuestions(bg, "j1", nil)
	_, _ = st.GetJob(bg, "j1")
	_, _ = st.GetQuestions(bg, "j1")
	_, _ = st.SpendThisMonth(bg, "202610")
	if len(f.gets) != 3 {
		t.Fatalf("gets = %d", len(f.gets))
	}
	for _, g := range f.gets {
		if !aws.ToBool(g.ConsistentRead) || *g.TableName != "test-jobs" {
			t.Fatalf("GetItem not consistent on test-jobs: %+v", g)
		}
	}
	if _, err := st.ListOwnerJobs(bg, "u1"); err != nil {
		t.Fatal(err)
	}
	q := f.qrys[0]
	if aws.ToString(q.IndexName) != "owner-index" || aws.ToBool(q.ConsistentRead) || f.saw("Scan") != 0 {
		t.Fatalf("query = %+v", q)
	}
}

func TestListOwnerJobsPaginatesAndOrdersBySortKey(t *testing.T) {
	st, f := jobStores()
	q := quotaFor("u1")
	q.MaxUser, q.MaxGlobal = 100, 100
	for i := 0; i < 7; i++ {
		if err := st.CreateJobWithQuota(bg, newJob(fmt.Sprintf("01J%02d", i), "u1"), q); err != nil {
			t.Fatal(err)
		}
	}
	f.queryPageSize = 2
	got, err := st.ListOwnerJobs(bg, "u1")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 7 || f.saw("Query") < 4 {
		t.Fatalf("%d jobs in %d queries", len(got), f.saw("Query"))
	}
	for i, g := range got {
		if g.ID != fmt.Sprintf("01J%02d", 6-i) {
			t.Fatalf("order: %+v", got)
		}
	}
	if sf := f.qrys[0].ScanIndexForward; sf == nil || *sf {
		t.Fatal("must query newest first (ScanIndexForward=false)")
	}
}

func TestJobItemOmitsTTLWhenZero(t *testing.T) {
	st, f := jobStores()
	j := newJob("j1", "u1")
	j.TTL = 0
	_ = st.CreateJobWithQuota(bg, j, quotaFor("u1"))
	if _, has := item(f, "j1")["ttl"]; has {
		t.Fatal("ttl must be omitted when 0")
	}
	_ = st.CreateJobWithQuota(bg, newJob("j2", "u1"), quotaFor("u1"))
	if _, has := item(f, "j2")["ttl"]; !has {
		t.Fatal("ttl must be written when set")
	}
}

func TestExpiredJobReadsAsMissing(t *testing.T) {
	st, _ := jobStores()
	_ = st.CreateJobWithQuota(bg, newJob("j1", "u1"), quotaFor("u1"))
	_ = st.PutQuestions(bg, "j1", []store.Question{{ID: "q"}})
	st.Now = func() time.Time { return t0.Add(25 * time.Hour) }
	if _, err := st.GetJob(bg, "j1"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("err = %v", err)
	}
	if _, err := st.GetQuestions(bg, "j1"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("err = %v", err)
	}
}

func TestStatusChangesAreConditional(t *testing.T) {
	st, f := jobStores()
	_ = st.CreateJobWithQuota(bg, newJob("j1", "u1"), quotaFor("u1"))
	_, _ = st.ClaimJob(bg, "j1", t0)
	_ = st.Heartbeat(bg, "j1", t0, store.Progress{})
	_, _ = st.FinishJob(bg, "j1", store.JobDone, store.JobFinish{Now: t0})
	_ = st.RequestCancel(bg, "u1", "j1")
	_ = st.MarkFailedIfQueuedOrRunning(bg, "j1", "x")
	if len(f.upds) != 5 {
		t.Fatalf("updates = %d", len(f.upds))
	}
	want := []string{"#st = :queued", "#st = :running", "#st = :running AND cancelRequested = :false", "ownerId = :owner", "#st = :queued OR #st = :running"}
	for i, u := range f.upds {
		if got := aws.ToString(u.ConditionExpression); got != want[i] {
			t.Errorf("update %d condition = %q, want %q", i, got, want[i])
		}
	}
	if f.saw("PutItem") != 0 || f.saw("DeleteItem") != 0 {
		t.Fatalf("calls = %v", f.calls)
	}
}

// itemSize estimates the stored size the way DynamoDB counts it: attribute name bytes plus value bytes.
func itemSize(t *testing.T, it map[string]types.AttributeValue) int {
	t.Helper()
	size := 0
	for k, v := range it {
		size += len(k)
		switch x := v.(type) {
		case *types.AttributeValueMemberS:
			size += len(x.Value)
		case *types.AttributeValueMemberN:
			size += len(x.Value)
		default:
			t.Fatalf("unexpected attribute type %T", v)
		}
	}
	return size
}

// DynamoDB items are limited to 400 KB. The questions item holds up to 60 questions at the
// AI-spec 7.1 caps: text 600 chars; MCQ four options of 200 chars; free text a model answer
// of 1,200 chars; each question has options OR an answer, never both. Two sets are measured:
// realistic English (1-byte characters) and the worst case (3-byte characters, and text made
// of < > & to prove the encoder does not HTML-escape them to 6 bytes each).
func TestSixtyQuestionsStayUnderTheItemLimit(t *testing.T) {
	const limit = 400 * 1024
	set := func(ch string, mcq bool) []store.Question {
		one := 2
		var qs []store.Question
		for i := 0; i < 60; i++ {
			q := store.Question{ID: "01J00000000000000000000000", Marks: 5, Text: strings.Repeat(ch, 600),
				SourceDocID: "01J00000000000000000000001", SourceDocName: strings.Repeat(ch, 120)}
			if mcq {
				q.Options = []string{strings.Repeat(ch, 200), strings.Repeat(ch, 200), strings.Repeat(ch, 200), strings.Repeat(ch, 200)}
				q.CorrectIndex = &one
			} else {
				q.ModelAnswer = strings.Repeat(ch, 1200)
			}
			qs = append(qs, q)
		}
		return qs
	}
	cases := []struct {
		name string
		ch   string
		mcq  bool
	}{
		{"realistic MCQ", "a", true}, {"realistic free text", "a", false},
		{"worst MCQ 3-byte", "語", true}, {"worst free text 3-byte", "語", false},
		{"worst MCQ html chars", "<", true}, {"worst free text html chars", "&", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			st, f := jobStores()
			if err := st.PutQuestions(bg, "j1", set(c.ch, c.mcq)); err != nil {
				t.Fatal(err)
			}
			size := itemSize(t, item(f, "j1#questions"))
			t.Logf("%s: %d bytes (%.0f%% of the %d byte limit)", c.name, size, 100*float64(size)/limit, limit)
			if size >= limit {
				t.Fatalf("item size estimate %d reaches the %d byte limit", size, limit)
			}
			got, err := st.GetQuestions(bg, "j1")
			if err != nil || len(got) != 60 || got[0].Text != strings.Repeat(c.ch, 600) {
				t.Fatalf("round trip failed: %v", err)
			}
		})
	}
}

func TestCreateJobRejectsDayMismatch(t *testing.T) {
	st, f := jobStores()
	j := newJob("j1", "u1")
	j.Day = "20261015"
	err := st.CreateJobWithQuota(bg, j, quotaFor("u1"))
	var qe *store.QuotaError
	if err == nil || errors.As(err, &qe) || errors.Is(err, store.ErrConflict) || len(f.calls) != 0 {
		t.Fatalf("err = %v calls = %v", err, f.calls)
	}
}

func TestAddSpendRetriesTransactionConflict(t *testing.T) {
	st, f := jobStores()
	if _, err := st.AddSpend(bg, "202610", 100); err != nil {
		t.Fatal(err)
	}
	f.failUpdate, f.failUpdateCount = &types.TransactionConflictException{}, 2
	total, err := st.AddSpend(bg, "202610", 50)
	if err != nil || total != 150 {
		t.Fatalf("total = %d, err = %v", total, err)
	}
	if got := n(t, item(f, "quota#global#202610"), "spendMicros"); got != 150 {
		t.Fatalf("stored spend = %d, want 150 (counted exactly once)", got)
	}
	if f.saw("UpdateItem") != 4 { // 1 + 3 attempts
		t.Fatalf("UpdateItem calls = %d", f.saw("UpdateItem"))
	}
}

func TestAddSpendPersistentConflictReturnsError(t *testing.T) {
	st, f := jobStores()
	f.failUpdate, f.failUpdateCount = &types.TransactionConflictException{}, 100
	_, err := st.AddSpend(bg, "202610", 50)
	var tc *types.TransactionConflictException
	if err == nil || !errors.As(err, &tc) {
		t.Fatalf("err = %v, want the raw conflict", err)
	}
	if f.saw("UpdateItem") != maxTxAttempts {
		t.Fatalf("attempts = %d, want %d", f.saw("UpdateItem"), maxTxAttempts)
	}
	if item(f, "quota#global#202610") != nil {
		t.Fatal("a failed AddSpend wrote something")
	}
}

// --- template.yaml drift ----------------------------------------------------------------

func listFrom(t *testing.T, index string) []string {
	t.Helper()
	b, err := os.ReadFile(templatePath)
	if err != nil {
		t.Fatal(err)
	}
	re := regexp.MustCompile(`IndexName: ` + regexp.QuoteMeta(index) + `\b[\s\S]*?NonKeyAttributes: \[([^\]]*)\]`)
	m := re.FindStringSubmatch(string(b))
	if m == nil {
		t.Fatalf("index %s not found in template.yaml", index)
	}
	var out []string
	for _, a := range strings.Split(m[1], ",") {
		out = append(out, strings.TrimSpace(a))
	}
	return out
}

// The store's mirrored constants must equal the INCLUDE lists in template.yaml (parsed here),
// and a stored job item must actually carry every projected attribute.
func TestIndexProjectionsMatchTemplate(t *testing.T) {
	if got := listFrom(t, "owner-index"); !slices.Equal(got, ownerIndexAttrs) {
		t.Errorf("owner-index: template %v, store %v", got, ownerIndexAttrs)
	}
	if got := listFrom(t, "day-index"); !slices.Equal(got, dayIndexAttrs) {
		t.Errorf("day-index: template %v, store %v", got, dayIndexAttrs)
	}
	st, f := jobStores()
	_ = st.CreateJobWithQuota(bg, newJob("j1", "u1"), quotaFor("u1"))
	it := item(f, "j1")
	for _, a := range slices.Concat([]string{"ownerId", "id", "day", "createdAt"}, ownerIndexAttrs, dayIndexAttrs) {
		if _, ok := it[a]; !ok {
			t.Errorf("job item does not write index attribute %q (has %v)", a, keys(it))
		}
	}
	for _, id := range []string{"quota#u1#20261016", "quota#global#20261016", "quota#global#202610"} {
		if _, ok := item(f, id)["ownerId"]; ok {
			t.Errorf("%s must not be in owner-index", id)
		}
		if _, ok := item(f, id)["day"]; ok {
			t.Errorf("%s must not be in day-index", id)
		}
	}
	_ = st.PutQuestions(bg, "j1", nil)
	for _, a := range []string{"ownerId", "day"} {
		if _, ok := item(f, "j1#questions")[a]; ok {
			t.Errorf("questions item must not carry %s", a)
		}
	}
}

func keys(m map[string]types.AttributeValue) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

// The summary comes from what the owner-index projects (the fake projects per template.yaml),
// so a store that read an unprojected attribute would see the zero value here.
func TestOwnerSummaryUsesOnlyProjectedAttributes(t *testing.T) {
	st, _ := jobStores()
	_ = st.CreateJobWithQuota(bg, newJob("j1", "u1"), quotaFor("u1"))
	got, err := st.ListOwnerJobs(bg, "u1")
	if err != nil || len(got) != 1 {
		t.Fatalf("%v %v", got, err)
	}
	want := store.JobSummary{ID: "j1", Status: "queued", Kind: "generate", CreatedAt: t0, UpdatedAt: t0}
	if !reflect.DeepEqual(got[0], want) {
		t.Fatalf("got %+v want %+v", got[0], want)
	}
}

func TestJobsTableName(t *testing.T) {
	st, f := newStores()
	_, _ = st.SpendThisMonth(bg, "202610")
	if *f.gets[0].TableName != "test-jobs" {
		t.Fatalf("table = %s", *f.gets[0].TableName)
	}
}

func TestInvalidQuotaCheckIsRejectedBeforeAnyWrite(t *testing.T) {
	st, f := jobStores()
	q := quotaFor("u1")
	q.Day = "2026-10-16"
	if err := st.CreateJobWithQuota(bg, newJob("j1", "u1"), q); err == nil || len(f.calls) != 0 {
		t.Fatalf("err = %v calls = %v", err, f.calls)
	}
}
