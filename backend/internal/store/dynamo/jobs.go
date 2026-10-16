package dynamo

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"github.com/gokulkumar/exam-assistant/backend/internal/store"
)

// Attributes the jobs table's GSIs project (INCLUDE) besides their keys. They mirror
// NonKeyAttributes in backend/template.yaml; TestIndexProjectionsMatchTemplate parses the
// template and fails if either side drifts, and checks every job item carries them.
var (
	ownerIndexAttrs = []string{"status", "createdAt", "updatedAt", "kind"}
	dayIndexAttrs   = []string{"status", "model", "kind", "costMicros", "calls", "inTok", "outTok", "cacheReadTok", "cacheWriteTok"}
)

const (
	questionsSuffix = "#questions"
	quotaPrefix     = "quota#"
	ownerIndexName  = "owner-index"
)

// jobItem is the job item. Only job items carry ownerId and day, so only they appear in the
// sparse owner-index and day-index. The usage counters are top-level attributes because the
// day-index projects them. Request and failed sources are JSON strings (counts are keyed by
// mark value and never queried).
type jobItem struct {
	ID              string `dynamodbav:"id"`
	OwnerID         string `dynamodbav:"ownerId"`
	Kind            string `dynamodbav:"kind"`
	Model           string `dynamodbav:"model"`
	Day             string `dynamodbav:"day"`
	Status          string `dynamodbav:"status"`
	Request         string `dynamodbav:"request"`
	Done            int    `dynamodbav:"done"`
	Total           int    `dynamodbav:"total"`
	FailedSources   string `dynamodbav:"failedSources,omitempty"`
	Error           string `dynamodbav:"error,omitempty"`
	CancelRequested bool   `dynamodbav:"cancelRequested"`
	PromptVersion   string `dynamodbav:"promptVersion,omitempty"`
	Effort          string `dynamodbav:"effort,omitempty"`
	InTok           int64  `dynamodbav:"inTok"`
	OutTok          int64  `dynamodbav:"outTok"`
	CacheReadTok    int64  `dynamodbav:"cacheReadTok"`
	CacheWriteTok   int64  `dynamodbav:"cacheWriteTok"`
	CostMicros      int64  `dynamodbav:"costMicros"`
	Calls           int    `dynamodbav:"calls"`
	Requested       int    `dynamodbav:"requested"`
	Kept            int    `dynamodbav:"kept"`
	Dropped         int    `dynamodbav:"dropped"`
	SourceFailed    bool   `dynamodbav:"sourceFailed"`
	CreatedAt       string `dynamodbav:"createdAt"`
	UpdatedAt       string `dynamodbav:"updatedAt"`
	TTL             int64  `dynamodbav:"ttl,omitempty"`
}

// questionsItem holds the generated questions as one JSON string. DynamoDB items are
// limited to 400 KB; 60 questions at the AI-spec 7.1 caps are about 160 KB (2-byte text; see the test for 3-byte text)
// (TestSixtyQuestionsStayUnderTheItemLimit estimates it), and the request limits (at most
// 60 questions) keep it there. Do not raise the question cap without re-checking.
type questionsItem struct {
	ID        string `dynamodbav:"id"`
	Questions string `dynamodbav:"questions"`
	TTL       int64  `dynamodbav:"ttl"`
}

func jobKey(id string) map[string]types.AttributeValue {
	return map[string]types.AttributeValue{"id": &types.AttributeValueMemberS{Value: id}}
}

func strAV(v string) types.AttributeValue { return &types.AttributeValueMemberS{Value: v} }
func numAV(v int64) types.AttributeValue {
	return &types.AttributeValueMemberN{Value: strconv.FormatInt(v, 10)}
}

func userDayID(user, day string) string { return quotaPrefix + user + "#" + day }
func globalID(period string) string     { return quotaPrefix + "global#" + period }

func marshalJob(j store.Job) (map[string]types.AttributeValue, error) {
	req, err := json.Marshal(j.Request)
	if err != nil {
		return nil, err
	}
	it := jobItem{ID: j.ID, OwnerID: j.OwnerID, Kind: j.Kind, Model: j.Model, Day: j.Day, Status: j.Status,
		Request: string(req), Done: j.Done, Total: j.Total, Error: j.Error, CancelRequested: j.CancelRequested,
		CreatedAt: j.CreatedAt.UTC().Format(timeLayout), UpdatedAt: j.UpdatedAt.UTC().Format(timeLayout), TTL: j.TTL}
	if len(j.FailedSources) > 0 {
		fs, err := json.Marshal(j.FailedSources)
		if err != nil {
			return nil, err
		}
		it.FailedSources = string(fs)
	}
	u := j.Usage
	it.PromptVersion, it.Effort = u.PromptVersion, u.Effort
	it.InTok, it.OutTok, it.CacheReadTok, it.CacheWriteTok = u.InputTokens, u.OutputTokens, u.CacheReadTokens, u.CacheWriteTokens
	it.CostMicros, it.Calls, it.Requested, it.Kept, it.Dropped, it.SourceFailed = u.CostMicros, u.Calls, u.Requested, u.Kept, u.Dropped, u.AnySourceFailed
	return attributevalue.MarshalMap(it)
}

func (s *Stores) decodeJob(m map[string]types.AttributeValue) (store.Job, error) {
	var it jobItem
	if err := attributevalue.UnmarshalMap(m, &it); err != nil {
		return store.Job{}, fmt.Errorf("dynamo: decode job: %w", err)
	}
	created, err := time.Parse(timeLayout, it.CreatedAt)
	if err != nil {
		return store.Job{}, fmt.Errorf("dynamo: job %s createdAt: %w", it.ID, err)
	}
	updated, err := time.Parse(timeLayout, it.UpdatedAt)
	if err != nil {
		return store.Job{}, fmt.Errorf("dynamo: job %s updatedAt: %w", it.ID, err)
	}
	j := store.Job{ID: it.ID, OwnerID: it.OwnerID, Kind: it.Kind, Model: it.Model, Day: it.Day, Status: it.Status,
		Done: it.Done, Total: it.Total, Error: it.Error, CancelRequested: it.CancelRequested,
		CreatedAt: created, UpdatedAt: updated, TTL: it.TTL,
		Usage: store.JobUsage{PromptVersion: it.PromptVersion, Effort: it.Effort, InputTokens: it.InTok, OutputTokens: it.OutTok,
			CacheReadTokens: it.CacheReadTok, CacheWriteTokens: it.CacheWriteTok, CostMicros: it.CostMicros, Calls: it.Calls,
			Requested: it.Requested, Kept: it.Kept, Dropped: it.Dropped, AnySourceFailed: it.SourceFailed}}
	if err := json.Unmarshal([]byte(it.Request), &j.Request); err != nil {
		return store.Job{}, fmt.Errorf("dynamo: job %s request: %w", it.ID, err)
	}
	if it.FailedSources != "" {
		if err := json.Unmarshal([]byte(it.FailedSources), &j.FailedSources); err != nil {
			return store.Job{}, fmt.Errorf("dynamo: job %s failedSources: %w", it.ID, err)
		}
	}
	return j, nil
}

func (s *Stores) ttlAt(d time.Duration) int64 { return s.Now().Add(d).Unix() }

// CreateJobWithQuota is one TransactWriteItems with four items; the position of a failed
// condition in CancellationReasons says what refused the job:
//
//	0 Put the job (attribute_not_exists(id))          -> store.ErrConflict
//	1 Update the user's day item (generate or regen)  -> QuotaError user | regen
//	2 Update quota#global#<yyyymmdd> (jobs)           -> QuotaError global
//	3 Update quota#global#<yyyymm> (spendMicros < budget) -> QuotaError budget
//
// Each quota item is created on first use and gets its ttl then (3 days for day items,
// 40 days for the month item); later writes never move it. A cancelled transaction writes
// nothing, so a refusal leaves no counter and no job behind.
func (s *Stores) CreateJobWithQuota(ctx context.Context, j store.Job, q store.QuotaCheck) error {
	if j.ID == "" || j.OwnerID == "" || j.Day == "" || strings.Contains(j.ID, "#") {
		return errors.New("dynamo: job needs an id without '#', an owner and a day")
	}
	if err := store.ValidateQuotaCheck(q); err != nil {
		return err
	}
	if j.Day != q.Day {
		return fmt.Errorf("dynamo: job day %q differs from quota day %q", j.Day, q.Day)
	}
	// A limit of zero or less refuses every job. The conditions below cannot express it for an
	// item that does not exist yet, so refuse before writing anything.
	userKind, userMax := store.QuotaUser, q.MaxUser
	if q.Regen {
		userKind, userMax = store.QuotaRegen, q.MaxRegen
	}
	switch {
	case userMax <= 0:
		return &store.QuotaError{Kind: userKind}
	case q.MaxGlobal <= 0:
		return &store.QuotaError{Kind: store.QuotaGlobal}
	case q.BudgetMicros <= 0:
		return &store.QuotaError{Kind: store.QuotaBudget}
	}
	av, err := marshalJob(j)
	if err != nil {
		return err
	}
	counter := "generate"
	if q.Regen {
		counter = "regen"
	}
	dayTTL, monthTTL := numAV(s.ttlAt(store.QuotaDayTTL)), numAV(s.ttlAt(store.QuotaMonthTTL))
	bump := func(id, attr string, max int, ttl types.AttributeValue) types.TransactWriteItem {
		return types.TransactWriteItem{Update: &types.Update{
			TableName:                &s.jobs,
			Key:                      jobKey(id),
			ConditionExpression:      ptr("attribute_not_exists(#c) OR #c < :max"),
			UpdateExpression:         ptr("ADD #c :one SET #ttl = if_not_exists(#ttl, :ttl)"),
			ExpressionAttributeNames: map[string]string{"#c": attr, "#ttl": "ttl"},
			ExpressionAttributeValues: map[string]types.AttributeValue{
				":max": numAV(int64(max)), ":one": numAV(1), ":ttl": ttl},
		}}
	}
	in := &dynamodb.TransactWriteItemsInput{TransactItems: []types.TransactWriteItem{
		{Put: &types.Put{TableName: &s.jobs, Item: av, ConditionExpression: ptr("attribute_not_exists(id)")}},
		bump(userDayID(q.UserID, q.Day), counter, userMax, dayTTL),
		bump(globalID(q.Day), "jobs", q.MaxGlobal, dayTTL),
		{Update: &types.Update{
			TableName:                 &s.jobs,
			Key:                       jobKey(globalID(q.Month)),
			ConditionExpression:       ptr("attribute_not_exists(spendMicros) OR spendMicros < :budget"),
			UpdateExpression:          ptr("SET #ttl = if_not_exists(#ttl, :ttl)"),
			ExpressionAttributeNames:  map[string]string{"#ttl": "ttl"},
			ExpressionAttributeValues: map[string]types.AttributeValue{":budget": numAV(q.BudgetMicros), ":ttl": monthTTL},
		}},
	}}
	err = s.transact(ctx, in)
	if err == nil {
		return nil
	}
	if rs, ok := cancelReasons(err); ok {
		for i := range rs {
			if !reasonIsCondFailed(rs, i) {
				continue
			}
			switch i {
			case 0:
				return fmt.Errorf("%w: job %s exists", store.ErrConflict, j.ID)
			case 1:
				return &store.QuotaError{Kind: userKind}
			case 2:
				return &store.QuotaError{Kind: store.QuotaGlobal}
			case 3:
				return &store.QuotaError{Kind: store.QuotaBudget}
			}
		}
	}
	return fmt.Errorf("dynamo: create job: %w", err)
}

func (s *Stores) GetJob(ctx context.Context, id string) (store.Job, error) {
	if id == "" || strings.Contains(id, "#") {
		return store.Job{}, store.ErrNotFound
	}
	m, err := s.getItem(ctx, s.jobs, jobKey(id))
	if err != nil {
		return store.Job{}, err
	}
	j, err := s.decodeJob(m)
	if err != nil {
		return store.Job{}, err
	}
	if j.TTL > 0 && j.TTL <= s.Now().Unix() { // TTL deletion is lazy
		return store.Job{}, store.ErrNotFound
	}
	return j, nil
}

// updateJob runs a conditional UpdateItem on the job. It returns applied=false when the
// condition failed, and the old item when the caller needs to tell "missing" from "wrong state".
func (s *Stores) updateJob(ctx context.Context, id, cond, update string, names map[string]string, vals map[string]types.AttributeValue) (applied bool, oldMissing bool, err error) {
	_, err = s.api.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName: &s.jobs, Key: jobKey(id),
		ConditionExpression: &cond, UpdateExpression: &update,
		ExpressionAttributeNames: names, ExpressionAttributeValues: vals,
		ReturnValuesOnConditionCheckFailure: types.ReturnValuesOnConditionCheckFailureAllOld,
	})
	if err == nil {
		return true, false, nil
	}
	if ce, ok := isCondFailed(err); ok {
		return false, len(ce.Item) == 0, nil
	}
	return false, false, err
}

func timeAV(t time.Time) types.AttributeValue { return strAV(t.UTC().Format(timeLayout)) }

// usageSet adds the usage attributes to a SET clause. Attribute names are all placeholders:
// several (status, error, day) are DynamoDB reserved words.
func usageSet(u store.JobUsage, sets *[]string, names map[string]string, vals map[string]types.AttributeValue) {
	add := func(attr string, v types.AttributeValue) {
		*sets = append(*sets, "#"+attr+" = :"+attr)
		names["#"+attr] = attr
		vals[":"+attr] = v
	}
	add("inTok", numAV(u.InputTokens))
	add("outTok", numAV(u.OutputTokens))
	add("cacheReadTok", numAV(u.CacheReadTokens))
	add("cacheWriteTok", numAV(u.CacheWriteTokens))
	add("costMicros", numAV(u.CostMicros))
	add("calls", numAV(int64(u.Calls)))
	add("requested", numAV(int64(u.Requested)))
	add("kept", numAV(int64(u.Kept)))
	add("dropped", numAV(int64(u.Dropped)))
	add("sourceFailed", &types.AttributeValueMemberBOOL{Value: u.AnySourceFailed})
	if u.PromptVersion != "" {
		add("promptVersion", strAV(u.PromptVersion))
	}
	if u.Effort != "" {
		add("effort", strAV(u.Effort))
	}
}

func (s *Stores) ClaimJob(ctx context.Context, id string, now time.Time) (bool, error) {
	if id == "" || strings.Contains(id, "#") {
		return false, nil
	}
	applied, _, err := s.updateJob(ctx, id, "#st = :queued", "SET #st = :running, #upd = :now",
		map[string]string{"#st": "status", "#upd": "updatedAt"},
		map[string]types.AttributeValue{":queued": strAV(store.JobQueued), ":running": strAV(store.JobRunning), ":now": timeAV(now)})
	if err != nil {
		return false, fmt.Errorf("dynamo: claim job: %w", err)
	}
	return applied, nil
}

func (s *Stores) Heartbeat(ctx context.Context, id string, now time.Time, p store.Progress) error {
	if id == "" || strings.Contains(id, "#") {
		return store.ErrNotFound
	}
	names := map[string]string{"#st": "status", "#upd": "updatedAt", "#done": "done", "#total": "total"}
	vals := map[string]types.AttributeValue{":running": strAV(store.JobRunning), ":now": timeAV(now),
		":done": numAV(int64(p.Done)), ":total": numAV(int64(p.Total))}
	sets := []string{"#upd = :now", "#done = :done", "#total = :total"}
	usageSet(p.Usage, &sets, names, vals)
	applied, missing, err := s.updateJob(ctx, id, "#st = :running", "SET "+strings.Join(sets, ", "), names, vals)
	if err != nil {
		return fmt.Errorf("dynamo: heartbeat: %w", err)
	}
	switch {
	case applied:
		return nil
	case missing:
		return store.ErrNotFound
	}
	return fmt.Errorf("%w: job is not running", store.ErrPrecondition)
}

func (s *Stores) FinishJob(ctx context.Context, id string, to string, upd store.JobFinish) (bool, error) {
	if to != store.JobDone && to != store.JobFailed && to != store.JobCancelled {
		return false, fmt.Errorf("dynamo: cannot finish a job as %q", to)
	}
	if id == "" || strings.Contains(id, "#") {
		return false, nil
	}
	now := upd.Now
	if now.IsZero() {
		now = s.Now()
	}
	names := map[string]string{"#st": "status", "#upd": "updatedAt", "#done": "done", "#err": "error", "#fs": "failedSources"}
	vals := map[string]types.AttributeValue{":running": strAV(store.JobRunning), ":to": strAV(to), ":now": timeAV(now),
		":done": numAV(int64(upd.Done)), ":err": strAV(upd.Error)}
	sets := []string{"#st = :to", "#upd = :now", "#done = :done", "#err = :err"}
	if len(upd.FailedSources) > 0 {
		fs, err := json.Marshal(upd.FailedSources)
		if err != nil {
			return false, err
		}
		vals[":fs"] = strAV(string(fs))
		sets = append(sets, "#fs = :fs")
	} else {
		delete(names, "#fs")
	}
	usageSet(upd.Usage, &sets, names, vals)
	cond := "#st = :running"
	if to == store.JobDone {
		cond += " AND cancelRequested = :false"
		vals[":false"] = &types.AttributeValueMemberBOOL{Value: false}
	}
	applied, _, err := s.updateJob(ctx, id, cond, "SET "+strings.Join(sets, ", "), names, vals)
	if err != nil {
		return false, fmt.Errorf("dynamo: finish job: %w", err)
	}
	return applied, nil
}

func (s *Stores) RequestCancel(ctx context.Context, ownerID, id string) error {
	if ownerID == "" || id == "" || strings.Contains(id, "#") {
		return store.ErrNotFound
	}
	applied, _, err := s.updateJob(ctx, id, "ownerId = :owner", "SET cancelRequested = :true", nil,
		map[string]types.AttributeValue{":owner": strAV(ownerID), ":true": &types.AttributeValueMemberBOOL{Value: true}})
	if err != nil {
		return fmt.Errorf("dynamo: request cancel: %w", err)
	}
	if !applied {
		return store.ErrNotFound // missing or someone else's: indistinguishable on purpose
	}
	return nil
}

func (s *Stores) MarkFailedIfQueuedOrRunning(ctx context.Context, id, msg string) error {
	if id == "" || strings.Contains(id, "#") {
		return store.ErrNotFound
	}
	applied, missing, err := s.updateJob(ctx, id, "#st = :queued OR #st = :running", "SET #st = :failed, #err = :err, #upd = :now",
		map[string]string{"#st": "status", "#err": "error", "#upd": "updatedAt"},
		map[string]types.AttributeValue{":queued": strAV(store.JobQueued), ":running": strAV(store.JobRunning),
			":failed": strAV(store.JobFailed), ":err": strAV(msg), ":now": timeAV(s.Now())})
	if err != nil {
		return fmt.Errorf("dynamo: mark job failed: %w", err)
	}
	if !applied && missing {
		return store.ErrNotFound
	}
	return nil // already finished: nothing to do
}

func (s *Stores) PutQuestions(ctx context.Context, jobID string, qs []store.Question) error {
	if jobID == "" || strings.Contains(jobID, "#") {
		return errors.New("dynamo: invalid job id")
	}
	// No HTML escaping: json.Marshal would turn each of < > & into 6 bytes.
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(qs); err != nil {
		return err
	}
	av, err := attributevalue.MarshalMap(questionsItem{ID: jobID + questionsSuffix, Questions: strings.TrimSuffix(buf.String(), "\n"), TTL: s.ttlAt(store.JobItemTTL)})
	if err != nil {
		return err
	}
	if _, err := s.api.PutItem(ctx, &dynamodb.PutItemInput{TableName: &s.jobs, Item: av}); err != nil {
		return fmt.Errorf("dynamo: put questions: %w", err)
	}
	return nil
}

func (s *Stores) GetQuestions(ctx context.Context, jobID string) ([]store.Question, error) {
	if jobID == "" || strings.Contains(jobID, "#") {
		return nil, store.ErrNotFound
	}
	m, err := s.getItem(ctx, s.jobs, jobKey(jobID+questionsSuffix))
	if err != nil {
		return nil, err
	}
	var it questionsItem
	if err := attributevalue.UnmarshalMap(m, &it); err != nil {
		return nil, fmt.Errorf("dynamo: decode questions: %w", err)
	}
	if it.TTL > 0 && it.TTL <= s.Now().Unix() {
		return nil, store.ErrNotFound
	}
	var qs []store.Question
	if err := json.Unmarshal([]byte(it.Questions), &qs); err != nil {
		return nil, fmt.Errorf("dynamo: decode questions: %w", err)
	}
	return qs, nil
}

// ListOwnerJobs queries the owner-index newest first (id, a ULID, is the index sort key),
// following LastEvaluatedKey to the end. It reads only what the index projects. GSIs do not
// support consistent reads, so a job created a moment ago may be missing.
func (s *Stores) ListOwnerJobs(ctx context.Context, ownerID string) ([]store.JobSummary, error) {
	if ownerID == "" {
		return nil, nil
	}
	type summary struct {
		ID        string `dynamodbav:"id"`
		Status    string `dynamodbav:"status"`
		Kind      string `dynamodbav:"kind"`
		CreatedAt string `dynamodbav:"createdAt"`
		UpdatedAt string `dynamodbav:"updatedAt"`
	}
	var out []store.JobSummary
	var start map[string]types.AttributeValue
	for {
		res, err := s.api.Query(ctx, &dynamodb.QueryInput{
			TableName:                 &s.jobs,
			IndexName:                 ptr(ownerIndexName),
			KeyConditionExpression:    ptr("ownerId = :o"),
			ExpressionAttributeValues: map[string]types.AttributeValue{":o": strAV(ownerID)},
			ScanIndexForward:          ptr(false),
			ExclusiveStartKey:         start,
		})
		if err != nil {
			return nil, fmt.Errorf("dynamo: list owner jobs: %w", err)
		}
		for _, m := range res.Items {
			var it summary
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
			out = append(out, store.JobSummary{ID: it.ID, Status: it.Status, Kind: it.Kind, CreatedAt: created, UpdatedAt: updated})
		}
		if len(res.LastEvaluatedKey) == 0 {
			return out, nil
		}
		start = res.LastEvaluatedKey
	}
}

// AddSpend is a single UpdateItem with ADD, so concurrent workers never lose an update, and
// it returns the new total. The 40-day ttl is set when the item is first created.
func (s *Stores) AddSpend(ctx context.Context, month string, micros int64) (int64, error) {
	if micros < 0 || len(month) != 6 {
		return 0, errors.New("dynamo: spend must be non-negative and the month yyyymm")
	}
	in := &dynamodb.UpdateItemInput{
		TableName:                &s.jobs,
		Key:                      jobKey(globalID(month)),
		UpdateExpression:         ptr("ADD spendMicros :n SET #ttl = if_not_exists(#ttl, :ttl)"),
		ExpressionAttributeNames: map[string]string{"#ttl": "ttl"},
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":n": numAV(micros), ":ttl": numAV(s.ttlAt(store.QuotaMonthTTL))},
		ReturnValues: types.ReturnValueUpdatedNew,
	}
	// CreateJobWithQuota writes this item in a transaction; a plain write that meets one in
	// flight fails with TransactionConflict and the SDK does not retry it. A conflicted
	// write applied nothing, so retrying cannot count the spend twice.
	var out *dynamodb.UpdateItemOutput
	err := s.retryTxnConflict(ctx, func() (err error) {
		out, err = s.api.UpdateItem(ctx, in)
		return err
	})
	if err != nil {
		return 0, fmt.Errorf("dynamo: add spend: %w", err)
	}
	n, ok := out.Attributes["spendMicros"].(*types.AttributeValueMemberN)
	if !ok {
		return 0, errors.New("dynamo: add spend: no total returned")
	}
	total, err := strconv.ParseInt(n.Value, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("dynamo: add spend: %w", err)
	}
	return total, nil
}

func (s *Stores) SpendThisMonth(ctx context.Context, month string) (int64, error) {
	m, err := s.getItem(ctx, s.jobs, jobKey(globalID(month)))
	if errors.Is(err, store.ErrNotFound) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	n, ok := m["spendMicros"].(*types.AttributeValueMemberN)
	if !ok {
		return 0, nil // the item exists (created with the budget check) but nothing was spent
	}
	return strconv.ParseInt(n.Value, 10, 64)
}
