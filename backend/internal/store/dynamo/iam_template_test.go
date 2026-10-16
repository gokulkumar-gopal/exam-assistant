package dynamo

import (
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// These tests tie backend/template.yaml (function scoping, IAM, async config) to the code.
// They parse the template with a real YAML parser; CloudFormation short tags (!Ref, !Sub,
// !GetAtt) decode as their plain scalar text, which is all the checks need.

func loadTemplate(t *testing.T) map[string]any {
	t.Helper()
	b, err := os.ReadFile(templatePath)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := yaml.Unmarshal(b, &doc); err != nil {
		t.Fatalf("template.yaml does not parse: %v", err)
	}
	return doc
}

func asMap(t *testing.T, v any, what string) map[string]any {
	t.Helper()
	m, ok := v.(map[string]any)
	if !ok {
		t.Fatalf("%s is not a mapping: %T", what, v)
	}
	return m
}

func fnProps(t *testing.T, doc map[string]any, name string) map[string]any {
	t.Helper()
	res := asMap(t, doc["Resources"], "Resources")
	return asMap(t, asMap(t, res[name], name)["Properties"], name+".Properties")
}

func strList(v any) []string {
	switch x := v.(type) {
	case string:
		return []string{x}
	case []any:
		var out []string
		for _, e := range x {
			out = append(out, strList(e)...)
		}
		return out
	}
	return nil
}

func envKeys(t *testing.T, props map[string]any) []string {
	t.Helper()
	env := asMap(t, props["Environment"], "Environment")
	vars := asMap(t, env["Variables"], "Environment.Variables")
	var out []string
	for k := range vars {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// effective is Globals plus the function's own variables, which is what Lambda gets.
func effectiveEnv(t *testing.T, doc map[string]any, fn string) map[string]bool {
	t.Helper()
	g := asMap(t, doc["Globals"], "Globals")
	gf := asMap(t, g["Function"], "Globals.Function")
	out := map[string]bool{}
	for _, k := range envKeys(t, gf) {
		out[k] = true
	}
	for _, k := range envKeys(t, fnProps(t, doc, fn)) {
		out[k] = true
	}
	return out
}

func TestTemplateSecretsScopedPerFunction(t *testing.T) {
	doc := loadTemplate(t)
	api, worker := effectiveEnv(t, doc, "ApiFunction"), effectiveEnv(t, doc, "WorkerFunction")
	for _, k := range []string{"JWT_SECRET", "GOOGLE_CLIENT_ID", "ALLOWED_ORIGINS", "WORKER_FUNCTION_NAME", "LLM_PROVIDERS", "LLM_MODEL", "MODE", "STAGE", "S3_BUCKET", "ENV"} {
		if !api[k] {
			t.Errorf("API function lacks %s", k)
		}
	}
	for _, k := range []string{"LLM_API_KEY", "GEMINI_API_KEY"} {
		if api[k] {
			t.Errorf("API function must not receive %s", k)
		}
	}
	for _, k := range []string{"LLM_API_KEY", "GEMINI_API_KEY", "LLM_PROVIDERS", "LLM_MODEL", "MODE", "STAGE", "S3_BUCKET", "ENV"} {
		if !worker[k] {
			t.Errorf("worker lacks %s", k)
		}
	}
	for _, k := range []string{"JWT_SECRET", "GOOGLE_CLIENT_ID", "ALLOWED_ORIGINS", "WORKER_FUNCTION_NAME"} {
		if worker[k] {
			t.Errorf("worker must not receive %s", k)
		}
	}
	// Secrets must be function-level, never Globals (Globals would leak them to both).
	gf := asMap(t, asMap(t, doc["Globals"], "Globals")["Function"], "Globals.Function")
	for _, k := range envKeys(t, gf) {
		if strings.Contains(k, "KEY") || strings.Contains(k, "SECRET") || k == "GOOGLE_CLIENT_ID" {
			t.Errorf("Globals sets %s; secrets belong on one function", k)
		}
	}
	// Both key parameters are NoEcho with an empty default.
	params := asMap(t, doc["Parameters"], "Parameters")
	for _, p := range []string{"LlmApiKey", "GeminiApiKey", "JwtSecret"} {
		pm := asMap(t, params[p], p)
		if pm["NoEcho"] != true {
			t.Errorf("parameter %s must be NoEcho", p)
		}
	}
	for _, p := range []string{"LlmApiKey", "GeminiApiKey", "LlmModel"} {
		if d, ok := asMap(t, params[p], p)["Default"]; !ok || d != "" {
			t.Errorf("parameter %s must default to the empty string, got %v", p, d)
		}
	}
}

func TestTemplateWorkerAsyncConfigAndLogging(t *testing.T) {
	doc := loadTemplate(t)
	w := fnProps(t, doc, "WorkerFunction")
	if w["Timeout"] != 600 {
		t.Errorf("worker Timeout = %v, want 600", w["Timeout"])
	}
	eic := asMap(t, w["EventInvokeConfig"], "EventInvokeConfig")
	if eic["MaximumRetryAttempts"] != 0 {
		t.Errorf("MaximumRetryAttempts = %v, want 0 (a retry would pay for the LLM calls twice)", eic["MaximumRetryAttempts"])
	}
	if eic["MaximumEventAgeInSeconds"] != 3600 {
		t.Errorf("MaximumEventAgeInSeconds = %v, want 3600", eic["MaximumEventAgeInSeconds"])
	}
	for fn, group := range map[string]string{"ApiFunction": "ApiLogs", "WorkerFunction": "WorkerLogs"} {
		lc := asMap(t, fnProps(t, doc, fn)["LoggingConfig"], fn+".LoggingConfig")
		if lc["LogGroup"] != group {
			t.Errorf("%s LoggingConfig.LogGroup = %v, want %s", fn, lc["LogGroup"], group)
		}
	}
	if _, ok := asMap(t, doc["Resources"], "Resources")["WorkerLogs"]; !ok {
		t.Error("WorkerLogs log group is missing")
	}
}

// policy flattens a function's statements to resource -> sorted actions.
func policy(t *testing.T, doc map[string]any, fn string) map[string][]string {
	t.Helper()
	pols, ok := fnProps(t, doc, fn)["Policies"].([]any)
	if !ok || len(pols) != 1 {
		t.Fatalf("%s: want exactly one inline Policies entry (no managed policies)", fn)
	}
	stmts, _ := asMap(t, pols[0], "policy")["Statement"].([]any)
	out := map[string][]string{}
	for _, s := range stmts {
		sm := asMap(t, s, "statement")
		if sm["Effect"] != "Allow" {
			t.Errorf("%s: statement effect %v", fn, sm["Effect"])
		}
		for _, a := range strList(sm["Action"]) {
			if strings.Contains(a, "*") {
				t.Errorf("%s: wildcard action %q", fn, a)
			}
			for _, r := range strList(sm["Resource"]) {
				out[r] = append(out[r], a)
			}
		}
	}
	for r := range out {
		sort.Strings(out[r])
	}
	return out
}

// Derivation: see task-D-2.1-report.md (function x table x action x code location).
var wantAPIPolicy = map[string][]string{
	"UsersTable.Arn":                        {"dynamodb:BatchGetItem", "dynamodb:GetItem", "dynamodb:PutItem", "dynamodb:UpdateItem"},
	"DocumentsTable.Arn":                    {"dynamodb:BatchGetItem", "dynamodb:DeleteItem", "dynamodb:GetItem", "dynamodb:PutItem", "dynamodb:Query", "dynamodb:UpdateItem"},
	"ExamsTable.Arn":                        {"dynamodb:BatchGetItem", "dynamodb:GetItem", "dynamodb:PutItem"},
	"${ExamsTable.Arn}/index/creator-index": {"dynamodb:Query"},
	"AttemptsTable.Arn":                     {"dynamodb:GetItem", "dynamodb:PutItem", "dynamodb:Query", "dynamodb:UpdateItem"},
	"${AttemptsTable.Arn}/index/user-index": {"dynamodb:Query"},
	"JobsTable.Arn":                         {"dynamodb:GetItem", "dynamodb:PutItem", "dynamodb:UpdateItem"},
	"${JobsTable.Arn}/index/*":              {"dynamodb:Query"},
	"${UploadsBucket.Arn}/*":                {"s3:DeleteObject", "s3:GetObject", "s3:PutObject"},
	"UploadsBucket.Arn":                     {"s3:ListBucket"},
	"WorkerFunction.Arn":                    {"lambda:InvokeFunction"},
}

var wantWorkerPolicy = map[string][]string{
	"DocumentsTable.Arn":     {"dynamodb:BatchGetItem"},
	"JobsTable.Arn":          {"dynamodb:GetItem", "dynamodb:PutItem", "dynamodb:UpdateItem"},
	"${UploadsBucket.Arn}/*": {"s3:GetObject"},
}

func TestIAMPoliciesMatchDerivation(t *testing.T) {
	doc := loadTemplate(t)
	for fn, want := range map[string]map[string][]string{"ApiFunction": wantAPIPolicy, "WorkerFunction": wantWorkerPolicy} {
		if got := policy(t, doc, fn); !reflect.DeepEqual(got, want) {
			t.Errorf("%s policy\n got  %v\n want %v", fn, got, want)
		}
	}
	// Only the API may invoke functions.
	if _, ok := policy(t, doc, "WorkerFunction")["WorkerFunction.Arn"]; ok {
		t.Error("worker must not invoke itself")
	}
}

// Which DynamoDB client methods each store file calls (directly or through getItem, batchGet
// and transact). If a file starts using another action, this fails: update the template
// policy (and wantAPIPolicy/wantWorkerPolicy) together with this table.
var wantStoreCalls = map[string][]string{
	"users.go":    {"BatchGetItem", "GetItem", "TransactWriteItems"},
	"docs.go":     {"DeleteItem", "GetItem", "BatchGetItem", "PutItem", "Query", "UpdateItem"},
	"exams.go":    {"BatchGetItem", "GetItem", "PutItem", "Query"},
	"attempts.go": {"GetItem", "PutItem", "Query", "UpdateItem"},
	"jobs.go":     {"GetItem", "PutItem", "Query", "TransactWriteItems", "UpdateItem"},
}

func TestStoreCodeUsesOnlyTemplateGrantedActions(t *testing.T) {
	call := regexp.MustCompile(`s\.api\.(\w+)\(`)
	helper := map[string]string{"s.getItem(": "GetItem", "s.batchGet(": "BatchGetItem", "s.transact(": "TransactWriteItems"}
	for file, want := range wantStoreCalls {
		b, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		src := string(b)
		set := map[string]bool{}
		for _, m := range call.FindAllStringSubmatch(src, -1) {
			set[m[1]] = true
		}
		for h, a := range helper {
			if strings.Contains(src, h) {
				set[a] = true
			}
		}
		var got []string
		for a := range set {
			got = append(got, a)
		}
		sort.Strings(got)
		w := append([]string(nil), want...)
		sort.Strings(w)
		if !reflect.DeepEqual(got, w) {
			t.Errorf("%s calls %v, expected %v (update the template IAM and this table together)", file, got, w)
		}
	}
	// Package-wide bans, including the interface (a new method means a new IAM action).
	files, _ := filepath.Glob("*.go")
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		b, _ := os.ReadFile(f)
		src := string(b)
		for _, banned := range []string{".Scan(", "BatchWriteItem", "ConditionCheck:", "types.ConditionCheck{", "ExecuteStatement", "ExecuteTransaction"} {
			if strings.Contains(src, banned) {
				t.Errorf("%s uses %s, which the template grants to nobody", f, banned)
			}
		}
	}
	b, _ := os.ReadFile("client.go")
	iface := regexp.MustCompile(`(?m)^\t(\w+)\(ctx context\.Context`).FindAllStringSubmatch(string(b[strings.Index(string(b), "type DynamoAPI interface"):]), -1)
	var methods []string
	for _, m := range iface {
		methods = append(methods, m[1])
	}
	sort.Strings(methods)
	wantM := []string{"BatchGetItem", "DeleteItem", "GetItem", "PutItem", "Query", "TransactWriteItems", "UpdateItem"}
	if !reflect.DeepEqual(methods, wantM) {
		t.Errorf("DynamoAPI methods %v, expected %v", methods, wantM)
	}
	// Indexes queried by name must be ones the API policy grants.
	idx := regexp.MustCompile(`IndexName:\s+ptr\(("[\w-]+"|ownerIndexName)\)`)
	for _, f := range []string{"exams.go", "attempts.go", "jobs.go"} {
		b, _ := os.ReadFile(f)
		for _, m := range idx.FindAllStringSubmatch(string(b), -1) {
			name := strings.Trim(m[1], `"`)
			if name == "ownerIndexName" {
				name = ownerIndexName
			}
			ok := false
			for r := range wantAPIPolicy {
				if strings.HasSuffix(r, "/index/"+name) || (strings.HasSuffix(r, "/index/*") && strings.HasPrefix(r, "${JobsTable")) {
					ok = true
				}
			}
			if !ok {
				t.Errorf("%s queries index %s with no IAM grant", f, name)
			}
		}
	}
}
