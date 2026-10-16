package dynamo

import (
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
	return store.Attempt{ExamID: examID, UserID: userID, Status: store.AttemptNotStarted, CreatedAt: t0}
}

func TestExamTablesNamedByStage(t *testing.T) {
	st, f := newStores()
	_ = st.PutExam(bg, store.Exam{ID: "e1", CreatorID: "u1", CreatedAt: t0, ClosesAt: t0})
	_, _ = st.GetExam(bg, "e1")
	_, _ = st.PutAttemptIfAbsent(bg, newAttempt("e1", "u1"))
	if *f.puts[0].TableName != "test-exams" || *f.puts[1].TableName != "test-attempts" || *f.gets[0].TableName != "test-exams" {
		t.Fatalf("tables: %s %s %s", *f.puts[0].TableName, *f.puts[1].TableName, *f.gets[0].TableName)
	}
	if *f.puts[0].ConditionExpression != "attribute_not_exists(id)" || *f.puts[1].ConditionExpression != "attribute_not_exists(userId)" {
		t.Fatal("create conditions")
	}
}

func TestExamReadsAreConsistentExceptIndexes(t *testing.T) {
	st, f := newStores()
	_ = st.PutExam(bg, store.Exam{ID: "e1", CreatorID: "u1", CreatedAt: t0, ClosesAt: t0})
	_, _ = st.PutAttemptIfAbsent(bg, newAttempt("e1", "u1"))
	_, _ = st.GetExam(bg, "e1")
	_, _ = st.GetAttempt(bg, "e1", "u1")
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

func TestListByCreatorQueriesIndexNewestFirstLimit50(t *testing.T) {
	st, f := newStores()
	_ = st.PutExam(bg, store.Exam{ID: "e1", CreatorID: "u1", CreatedAt: t0, ClosesAt: t0})
	_, _ = st.ListByCreator(bg, "u1", 500)
	q := f.qrys[0]
	if aws.ToString(q.IndexName) != "creator-index" || aws.ToBool(q.ScanIndexForward) || aws.ToInt32(q.Limit) != 50 || f.saw("Scan") != 0 {
		t.Fatalf("query: %+v", q)
	}
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
		"user-index":    {"status", "scoreHalves", "scoreBeforePeeksHalves", "submittedAt"},
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
	_ = st.Save(bg, "e1", "u1", map[string]string{"q1": "secret answer"}, 2, t0.Add(time.Minute))
	// An index read returns only keys plus the INCLUDE list.
	res, err := f.Query(bg, userIndexQuery("u1"))
	if err != nil || len(res.Items) != 1 {
		t.Fatalf("%v %v", res, err)
	}
	for attr := range res.Items[0] {
		if !map[string]bool{"examId": true, "userId": true, "status": true, "scoreHalves": true, "scoreBeforePeeksHalves": true, "submittedAt": true}[attr] {
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
	_ = st.Save(bg, "e1", "u1", map[string]string{"q1": "a"}, 1, t0) // starts the attempt
	n := len(f.upds)
	for i := 0; i < 3; i++ {
		if err := st.Save(bg, "e1", "u1", map[string]string{"q1": "b"}, 2, t0.Add(time.Minute)); err != nil {
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
	it := f.tables["test-attempts"]["e1\x00u1"]
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

func TestNoBatchWriteItemUsed(t *testing.T) {
	st, f := newStores()
	for i := 0; i < 5; i++ {
		id := fmt.Sprintf("e%d", i)
		_ = st.PutExam(bg, store.Exam{ID: id, CreatorID: "u1", CreatedAt: t0, ClosesAt: t0})
		_, _ = st.PutAttemptIfAbsent(bg, newAttempt(id, "u1"))
		_ = st.Save(bg, id, "u1", nil, 0, t0)
		_, _ = st.Reveal(bg, id, "u1", "q1", 3)
		_ = st.Decline(bg, id, "u1")
		_ = st.Submit(bg, newAttempt(id, "u1"))
	}
	_, _ = st.GetExams(bg, []string{"e1", "e2"})
	_, _ = st.ListByUser(bg, "u1")
	_, _ = st.ListByCreator(bg, "u1", 5)
	if f.saw("BatchWriteItem") != 0 || f.saw("Scan") != 0 {
		t.Fatalf("calls: %v", f.calls)
	}
	for _, c := range f.calls {
		if strings.Contains(c, "BatchWrite") || c == "Scan" {
			t.Fatalf("forbidden call %s", c)
		}
	}
}

func TestRevealUsesConditionalUpdate(t *testing.T) {
	st, f := newStores()
	_, _ = st.PutAttemptIfAbsent(bg, newAttempt("e1", "u1"))
	if _, err := st.Reveal(bg, "e1", "u1", "q1", 3); err != nil {
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
