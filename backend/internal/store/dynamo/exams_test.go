package dynamo

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"github.com/gokulkumar/exam-assistant/backend/internal/store"
)

func newAttempt(examID, userID string) store.Attempt {
	return store.Attempt{ExamID: examID, UserID: userID, AttemptNo: 1, Status: store.AttemptNotStarted, CreatedAt: t0}
}

func TestExamTablesNamedByStage(t *testing.T) {
	st, f := newStores()
	_ = st.PutExam(bg, store.Exam{ID: "e1", CreatorID: "u1", CreatedAt: t0, ClosesAt: t0})
	_, _ = st.GetExam(bg, "e1")
	_, _ = st.PutAttemptIfAbsent(bg, newAttempt("e1", "u1"))
	if *f.puts[0].TableName != "test-exams" || *f.puts[1].TableName != "test-attempts" || *f.gets[0].TableName != "test-exams" {
		t.Fatalf("tables: %s %s %s", *f.puts[0].TableName, *f.puts[1].TableName, *f.gets[0].TableName)
	}
	if *f.puts[0].ConditionExpression != "attribute_not_exists(id)" || *f.puts[1].ConditionExpression != "attribute_not_exists(sk)" {
		t.Fatal("create conditions")
	}
}

func TestExamReadsAreConsistentExceptIndexes(t *testing.T) {
	st, f := newStores()
	_ = st.PutExam(bg, store.Exam{ID: "e1", CreatorID: "u1", CreatedAt: t0, ClosesAt: t0})
	_, _ = st.PutAttemptIfAbsent(bg, newAttempt("e1", "u1"))
	_, _ = st.GetExam(bg, "e1")
	_, _ = st.GetAttempt(bg, "e1", "u1", 1)
	_, _ = st.GetExams(bg, []string{"e1"})
	_, _ = st.ListByExam(bg, "e1")
	_, _ = st.ListByCreator(bg, "u1", 10)
	_, _ = st.ListByUser(bg, "u1")
	for _, g := range f.gets {
		if !aws.ToBool(g.ConsistentRead) {
			t.Fatalf("GetItem not consistent: %v", *g.TableName)
		}
	}
	for _, b := range f.batch {
		for _, ka := range b.RequestItems {
			if !aws.ToBool(ka.ConsistentRead) {
				t.Fatal("BatchGetItem not consistent")
			}
		}
	}
	for _, q := range f.qrys {
		isIndex := q.IndexName != nil
		if isIndex == aws.ToBool(q.ConsistentRead) {
			t.Fatalf("query on %s index=%v consistent=%v", *q.TableName, isIndex, aws.ToBool(q.ConsistentRead))
		}
	}
}

func TestGetExamsChunksOf100AndRetriesUnprocessed(t *testing.T) {
	st, f := newStores()
	var ids []string
	for i := 0; i < 230; i++ {
		id := fmt.Sprintf("e%03d", i)
		_ = st.PutExam(bg, store.Exam{ID: id, CreatorID: "u", CreatedAt: t0, ClosesAt: t0})
		ids = append(ids, id)
	}
	f.unprocessedOnce = true
	got, err := st.GetExams(bg, ids)
	if err != nil || len(got) != 230 {
		t.Fatalf("got %d, %v", len(got), err)
	}
	if f.saw("BatchGetItem") < 4 {
		t.Fatalf("BatchGetItem calls = %d, want >= 3 chunks plus a retry", f.saw("BatchGetItem"))
	}
	for _, b := range f.batch {
		for _, ka := range b.RequestItems {
			if len(ka.Keys) > 100 {
				t.Fatalf("chunk of %d", len(ka.Keys))
			}
		}
	}
}

func TestGetExamsGivesUpOnPersistentUnprocessed(t *testing.T) {
	st, f := newStores()
	_ = st.PutExam(bg, store.Exam{ID: "a", CreatorID: "u", CreatedAt: t0, ClosesAt: t0})
	_ = st.PutExam(bg, store.Exam{ID: "b", CreatorID: "u", CreatedAt: t0, ClosesAt: t0})
	f.alwaysUnprocessed = true
	f.pendingRetry = nil
	if _, err := st.GetExams(bg, []string{"a", "b"}); err == nil {
		t.Fatal("persistently unprocessed keys must be an error, not an endless loop")
	}
	if f.saw("BatchGetItem") != maxBatchAttempts {
		t.Fatalf("attempts = %d", f.saw("BatchGetItem"))
	}
}

// The creator list reads the whole index partition (no Limit), page by page, so that sorting
// by createdAt sees every exam; the result is still capped at 50.
func TestListByCreatorFollowsPagesAndCaps(t *testing.T) {
	st, f := newStores()
	for i := 0; i < 70; i++ {
		_ = st.PutExam(bg, store.Exam{ID: fmt.Sprintf("id-%02d", 69-i), CreatorID: "u1", CreatedAt: t0.Add(time.Duration(i) * time.Minute), ClosesAt: t0})
	}
	f.queryPageSize = 7
	got, err := st.ListByCreator(bg, "u1", 500)
	if err != nil || len(got) != 50 || got[0].ID != "id-00" {
		t.Fatalf("len %d first %v err %v", len(got), got[0].ID, err)
	}
	if len(f.qrys) != 10 || f.saw("Scan") != 0 {
		t.Fatalf("queries = %d", len(f.qrys))
	}
	for _, q := range f.qrys {
		if aws.ToString(q.IndexName) != "creator-index" || q.Limit != nil {
			t.Fatalf("query: %+v", q)
		}
	}
}

func TestListByCreatorPageLoopIsBounded(t *testing.T) {
	st, f := newStores()
	_ = st.PutExam(bg, store.Exam{ID: "a", CreatorID: "u1", CreatedAt: t0, ClosesAt: t0})
	_ = st.PutExam(bg, store.Exam{ID: "b", CreatorID: "u1", CreatedAt: t0, ClosesAt: t0})
	f.queryPageSize = 1
	st2 := New(&pagingForever{f}, "test")
	if _, err := st2.ListByCreator(bg, "u1", 10); err == nil {
		t.Fatal("unbounded pagination must be an error")
	}
}

// pagingForever wraps the fake and always claims there is another page.
type pagingForever struct{ *fakeDynamo }

func (p *pagingForever) Query(ctx context.Context, in *dynamodb.QueryInput, o ...func(*dynamodb.Options)) (*dynamodb.QueryOutput, error) {
	out, err := p.fakeDynamo.Query(ctx, in, o...)
	if err == nil {
		out.LastEvaluatedKey = map[string]types.AttributeValue{"id": sv("x")}
	}
	return out, err
}

// parseIndexInclude returns the NonKeyAttributes of a GSI plus its key attributes and the
// base table keys, i.e. everything a query on that index can return.
func indexAttrs(index string, tableKeys ...string) map[string]bool {
	out := map[string]bool{}
	for _, a := range tableKeys {
		out[a] = true
	}
	for _, a := range templateIncludeList(index) {
		out[a] = true
	}
	return out
}

func tagNames(v any) []string {
	var out []string
	rt := reflect.TypeOf(v)
	for i := 0; i < rt.NumField(); i++ {
		tag := strings.Split(rt.Field(i).Tag.Get("dynamodbav"), ",")[0]
		if tag == "" || tag == "-" {
			t := rt.Field(i).Name
			tag = t
		}
		out = append(out, tag)
	}
	return out
}

// Every attribute the summary decoders read from an index must be a key or projected;
// otherwise the read silently returns the zero value.
func TestSummaryAttributesCoveredByTemplateIncludeLists(t *testing.T) {
	cases := []struct {
		name  string
		item  any
		attrs map[string]bool
	}{
		{"creator-index", examSummaryItem{}, indexAttrs("creator-index", "id", "creatorId")},
		{"user-index", attemptSummaryItem{}, indexAttrs("user-index", "examId", "userId")},
	}
	for _, c := range cases {
		for _, a := range tagNames(c.item) {
			if !c.attrs[a] {
				t.Errorf("%s: summary reads %q which the index does not project (keys+INCLUDE: %v)", c.name, a, c.attrs)
			}
		}
	}
	// The exact projected sets from backend-spec 6.2 (guards the template itself).
	want := map[string][]string{
		"creator-index": {"title", "type", "totalMarks", "closesAt", "createdAt"},
		"user-index":    {"status", "attemptNo", "scoreHalves", "scoreBeforePeeksHalves", "submittedAt"},
	}
	for idx, w := range want {
		if got := templateIncludeList(idx); !reflect.DeepEqual(got, w) {
			t.Errorf("%s INCLUDE = %v, want %v", idx, got, w)
		}
	}
	for _, bad := range []string{"questions", "answers", "revealed", "ALL"} {
		for _, idx := range []string{"creator-index", "user-index"} {
			for _, a := range templateIncludeList(idx) {
				if a == bad {
					t.Errorf("%s must never project %s", idx, bad)
				}
			}
		}
	}
	tpl := templateText()
	if !regexp.MustCompile(`IndexName: creator-index[^\n]*\n[^\n]*\n[^\n]*ProjectionType: INCLUDE`).MatchString(tpl) ||
		!regexp.MustCompile(`IndexName: user-index[^\n]*\n[^\n]*\n[^\n]*ProjectionType: INCLUDE`).MatchString(tpl) {
		t.Error("exam and attempt indexes must use ProjectionType INCLUDE")
	}
}

func TestUserIndexProjectionExcludesAnswers(t *testing.T) {
	st, f := newStores()
	a := newAttempt("e1", "u1")
	a.Answers = map[string]string{"q1": "secret answer"}
	_, _ = st.PutAttemptIfAbsent(bg, a)
	_ = st.Save(bg, "e1", "u1", 1, map[string]string{"q1": "secret answer"}, 2, t0.Add(time.Minute))
	// An index read returns only keys plus the INCLUDE list.
	res, err := f.Query(bg, userIndexQuery("u1"))
	if err != nil || len(res.Items) != 1 {
		t.Fatalf("%v %v", res, err)
	}
	for attr := range res.Items[0] {
		if !map[string]bool{"examId": true, "sk": true, "userId": true, "attemptNo": true, "status": true, "scoreHalves": true, "scoreBeforePeeksHalves": true, "submittedAt": true}[attr] {
			t.Fatalf("user-index returned %q", attr)
		}
	}
	sums, err := st.ListByUser(bg, "u1")
	if err != nil || len(sums) != 1 || sums[0].Status != store.AttemptInProgress {
		t.Fatalf("%+v %v", sums, err)
	}
}

// Autosave must not write any attribute the user-index projects, so autosaves never cost
// an index write. Only the first save of a not_started attempt changes status.
func TestSaveDoesNotTouchIndexedAttributes(t *testing.T) {
	st, f := newStores()
	_, _ = st.PutAttemptIfAbsent(bg, newAttempt("e1", "u1"))
	_ = st.Save(bg, "e1", "u1", 1, map[string]string{"q1": "a"}, 1, t0) // starts the attempt
	n := len(f.upds)
	for i := 0; i < 3; i++ {
		if err := st.Save(bg, "e1", "u1", 1, map[string]string{"q1": "b"}, 2, t0.Add(time.Minute)); err != nil {
			t.Fatal(err)
		}
	}
	indexed := templateIncludeList("user-index")
	for _, u := range f.upds[n:] {
		names := u.ExpressionAttributeNames
		expr := aws.ToString(u.UpdateExpression)
		for _, a := range indexed {
			if strings.Contains(expr, a) {
				t.Fatalf("autosave writes indexed attribute %s: %s", a, expr)
			}
			for ph, real := range names {
				if real == a && strings.Contains(expr, ph) {
					t.Fatalf("autosave writes indexed attribute %s: %s", a, expr)
				}
			}
		}
		if aws.ToString(u.ConditionExpression) == "" {
			t.Fatal("autosave must be conditional")
		}
	}
	// And the stored item's indexed attributes are untouched by a later autosave.
	it := f.tables["test-attempts"]["e1\x00u1#001"]
	if s(it["status"]) != store.AttemptInProgress {
		t.Fatalf("status = %v", it["status"])
	}
}

func TestPutAttemptOmitsAbsentScoresAndTimes(t *testing.T) {
	st, f := newStores()
	_, _ = st.PutAttemptIfAbsent(bg, newAttempt("e1", "u1"))
	it := f.puts[0].Item
	for _, a := range []string{"scoreHalves", "scoreBeforePeeksHalves", "submittedAt", "startedAt", "savedAt"} {
		if _, has := it[a]; has {
			t.Errorf("%s must be omitted when unset", a)
		}
	}
	if _, ok := it["revealed"].(*types.AttributeValueMemberL); !ok {
		t.Fatalf("revealed must be an (empty) list so size() works: %#v", it["revealed"])
	}
}

// DynamoAPI is the only way the stores reach DynamoDB, so the guard against BatchWriteItem
// and Scan is its method set. The recorder check below covers the fake's call log too.
func TestNoBatchWriteItemUsed(t *testing.T) {
	api := reflect.TypeOf((*DynamoAPI)(nil)).Elem()
	for i := 0; i < api.NumMethod(); i++ {
		if n := api.Method(i).Name; n == "BatchWriteItem" || n == "Scan" {
			t.Fatalf("DynamoAPI must not expose %s", n)
		}
	}
	st, f := newStores()
	for i := 0; i < 5; i++ {
		id := fmt.Sprintf("e%d", i)
		_ = st.PutExam(bg, store.Exam{ID: id, CreatorID: "u1", CreatedAt: t0, ClosesAt: t0})
		_, _ = st.PutAttemptIfAbsent(bg, newAttempt(id, "u1"))
		_ = st.Save(bg, id, "u1", 1, nil, 0, t0)
		_, _ = st.Reveal(bg, id, "u1", 1, "q1", 3, t0)
		_ = st.Decline(bg, id, "u1", 1)
		_ = st.Submit(bg, newAttempt(id, "u1"))
	}
	_, _ = st.GetExams(bg, []string{"e1", "e2"})
	_, _ = st.ListByUser(bg, "u1")
	_, _ = st.ListByCreator(bg, "u1", 5)
	for _, c := range f.calls {
		if strings.Contains(c, "BatchWrite") || c == "Scan" {
			t.Fatalf("forbidden call %s", c)
		}
	}
}

// A first save that loses the race to a Reveal (or another first save) between its two
// conditional writes must retry, not report the attempt as not writable.
func TestSaveRetriesWhenStartFormLosesRace(t *testing.T) {
	st, f := newStores()
	_, _ = st.PutAttemptIfAbsent(bg, newAttempt("e1", "u1"))
	raced := false
	f.beforeUpdate = func(f *fakeDynamo, in *dynamodb.UpdateItemInput) {
		if !raced && strings.Contains(aws.ToString(in.ConditionExpression), "#st = :ns") {
			raced = true // another writer starts the attempt just before our start form runs
			f.tables["test-attempts"]["e1\x00u1#001"]["status"] = sv(store.AttemptInProgress)
		}
	}
	if err := st.Save(bg, "e1", "u1", 1, map[string]string{"q1": "a"}, 1, t0); err != nil {
		t.Fatalf("save: %v", err)
	}
	if !raced {
		t.Fatal("hook did not run")
	}
	got, _ := st.GetAttempt(bg, "e1", "u1", 1)
	if got.Answers["q1"] != "a" || got.Status != store.AttemptInProgress {
		t.Fatalf("answers lost: %+v", got)
	}
}

func TestSaveGivesUpUnderEndlessContention(t *testing.T) {
	st, f := newStores()
	_, _ = st.PutAttemptIfAbsent(bg, newAttempt("e1", "u1"))
	// Every in_progress form sees not_started, every start form sees in_progress: a writer that
	// flips the row back and forth. The loop must stop, with a plain error.
	f.beforeUpdate = func(f *fakeDynamo, in *dynamodb.UpdateItemInput) {
		it := f.tables["test-attempts"]["e1\x00u1#001"]
		if strings.Contains(aws.ToString(in.ConditionExpression), "#st = :ns") {
			it["status"] = sv(store.AttemptInProgress)
		} else {
			it["status"] = sv(store.AttemptNotStarted)
		}
	}
	err := st.Save(bg, "e1", "u1", 1, nil, 0, t0)
	if err == nil || errors.Is(err, store.ErrPrecondition) {
		t.Fatalf("err = %v", err)
	}
	if len(f.upds) != 2*maxSaveAttempts {
		t.Fatalf("updates = %d", len(f.upds))
	}
}

// Reveal loses a race between its two writes: the row becomes in_progress just before the
// start form runs. The re-read shows a writable attempt, so a second round must succeed.
func TestRevealRetriesAfterLostRace(t *testing.T) {
	st, f := newStores()
	_, _ = st.PutAttemptIfAbsent(bg, newAttempt("e1", "u1"))
	raced := false
	f.beforeUpdate = func(f *fakeDynamo, in *dynamodb.UpdateItemInput) {
		if !raced && strings.Contains(aws.ToString(in.ConditionExpression), "#st = :ns") {
			raced = true
			f.tables["test-attempts"]["e1\x00u1#001"]["status"] = sv(store.AttemptInProgress)
		}
	}
	r, err := st.Reveal(bg, "e1", "u1", 1, "q1", 3, t0)
	if err != nil || !r.Charged || len(r.Revealed) != 1 {
		t.Fatalf("%+v %v", r, err)
	}
}

func TestRevealAndSubmitDoNotSetSavedAtOrTouchRevealed(t *testing.T) {
	st, f := newStores()
	_, _ = st.PutAttemptIfAbsent(bg, newAttempt("e1", "u1"))
	_, _ = st.Reveal(bg, "e1", "u1", 1, "q1", 3, t0)
	if _, has := f.tables["test-attempts"]["e1\x00u1#001"]["savedAt"]; has {
		t.Fatal("reveal must not set savedAt")
	}
	_ = st.Submit(bg, newAttempt("e1", "u1"))
	for _, u := range f.upds {
		if strings.Contains(aws.ToString(u.UpdateExpression), "#st = :sub") && strings.Contains(aws.ToString(u.UpdateExpression), "revealed") {
			t.Fatal("submit must not write revealed")
		}
	}
}

func TestRevealUsesConditionalUpdate(t *testing.T) {
	st, f := newStores()
	_, _ = st.PutAttemptIfAbsent(bg, newAttempt("e1", "u1"))
	if _, err := st.Reveal(bg, "e1", "u1", 1, "q1", 3, t0); err != nil {
		t.Fatal(err)
	}
	c := aws.ToString(f.upds[0].ConditionExpression)
	if !strings.Contains(c, "size(revealed) < :max") || !strings.Contains(c, "NOT contains(revealed, :q)") {
		t.Fatalf("condition = %s", c)
	}
}

func userIndexQuery(userID string) *dynamodb.QueryInput {
	return &dynamodb.QueryInput{
		TableName: aws.String("test-attempts"), IndexName: aws.String("user-index"),
		KeyConditionExpression:    aws.String("userId = :u"),
		ExpressionAttributeValues: map[string]types.AttributeValue{":u": &types.AttributeValueMemberS{Value: userID}},
	}
}

// One item per attempt: the range key is sk = "<userId>#<attemptNo, 3 digits>", the plain userId
// and attemptNo attributes stay on the item (user-index key and projection), and a retake is a
// conditional put on sk, never on userId (which would be absent only for a different user).
func TestAttemptItemKeyAndRetakePut(t *testing.T) {
	st, f := newStores()
	_, _ = st.PutAttemptIfAbsent(bg, newAttempt("e1", "u1"))
	ok, err := st.PutRetake(bg, "e1", "u1", 12, t0)
	if err != nil || !ok {
		t.Fatalf("%v %v", ok, err)
	}
	put := f.puts[len(f.puts)-1]
	it := f.tables["test-attempts"]["e1\x00u1#012"]
	if it == nil || s(it["sk"]) != "u1#012" || s(it["userId"]) != "u1" || num(it["attemptNo"]) != 12 || s(it["status"]) != store.AttemptNotStarted {
		t.Fatalf("item: %v", it)
	}
	if aws.ToString(put.ConditionExpression) != "attribute_not_exists(sk)" {
		t.Fatalf("condition: %s", aws.ToString(put.ConditionExpression))
	}
	if f.tables["test-attempts"]["e1\x00u1#001"] == nil {
		t.Fatal("attempt 1 must be stored as u1#001")
	}
	if ok, err := st.PutRetake(bg, "e1", "u1", 12, t0); err != nil || ok {
		t.Fatalf("second put: %v %v", ok, err)
	}
}

// ListAttempts and GetLatestAttempt are consistent base-table queries on the user's sk prefix;
// the latest is one item read newest first.
func TestUserAttemptQueriesUseBeginsWithOnTheBaseTable(t *testing.T) {
	st, f := newStores()
	_, _ = st.PutAttemptIfAbsent(bg, newAttempt("e1", "u1"))
	_, _ = st.PutRetake(bg, "e1", "u1", 2, t0)
	_, _ = st.PutRetake(bg, "e1", "u10", 2, t0)
	n := len(f.qrys)
	rows, err := st.ListAttempts(bg, "e1", "u1")
	if err != nil || len(rows) != 2 {
		t.Fatalf("%v %v", rows, err)
	}
	latest, err := st.GetLatestAttempt(bg, "e1", "u1")
	if err != nil || latest.AttemptNo != 2 {
		t.Fatalf("%+v %v", latest, err)
	}
	qs := f.qrys[n:]
	if len(qs) != 2 {
		t.Fatalf("%d queries", len(qs))
	}
	for _, q := range qs {
		if q.IndexName != nil || !aws.ToBool(q.ConsistentRead) ||
			aws.ToString(q.KeyConditionExpression) != "examId = :e AND begins_with(sk, :p)" ||
			s(q.ExpressionAttributeValues[":p"]) != "u1#" {
			t.Fatalf("query: %+v", q)
		}
	}
	if qs[0].Limit != nil || qs[0].ScanIndexForward != nil {
		t.Fatalf("ListAttempts reads every attempt in order: %+v", qs[0])
	}
	if qs[1].Limit == nil || *qs[1].Limit != 1 || aws.ToBool(qs[1].ScanIndexForward) {
		t.Fatalf("GetLatestAttempt reads one item, newest first: %+v", qs[1])
	}
}

// The user-index may hold several rows per (userId, examId); paging must not skip any of them.
func TestListByUserFollowsPagesAcrossSeveralAttemptsOfOneExam(t *testing.T) {
	st, f := newStores()
	_, _ = st.PutAttemptIfAbsent(bg, newAttempt("e1", "u1"))
	_, _ = st.PutAttemptIfAbsent(bg, newAttempt("e2", "u1"))
	for _, n := range []int{2, 3, 4} {
		_, _ = st.PutRetake(bg, "e1", "u1", n, t0)
	}
	f.queryPageSize = 1
	sums, err := st.ListByUser(bg, "u1")
	if err != nil || len(sums) != 5 {
		t.Fatalf("%d rows: %+v %v", len(sums), sums, err)
	}
	seen := map[string]bool{}
	for _, a := range sums {
		seen[fmt.Sprintf("%s/%d", a.ExamID, a.AttemptNo)] = true
	}
	if len(seen) != 5 || !seen["e1/4"] || !seen["e2/1"] {
		t.Fatalf("%v", seen)
	}
}
