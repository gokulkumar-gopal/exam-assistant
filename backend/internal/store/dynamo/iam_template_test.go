package dynamo

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
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
	"JobsTable.Arn":                         {"dynamodb:BatchGetItem", "dynamodb:GetItem", "dynamodb:PutItem", "dynamodb:UpdateItem"},
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

// plannedAPIGrants are API grants no store method uses yet. Each needs a spec reference; the
// test fails when a grant is neither derived from the code nor listed here.
var plannedAPIGrants = map[string]string{
	"JobsTable.Arn|dynamodb:BatchGetItem": "GET /ai/usage reads the month and day counter items (backend-spec 6.3, B-5a.2)",
}

// tableResource maps the Stores table fields to the template resources that own them.
var tableResource = map[string]string{
	"users": "UsersTable.Arn", "docs": "DocumentsTable.Arn", "jobs": "JobsTable.Arn",
	"exams": "ExamsTable.Arn", "attempts": "AttemptsTable.Arn",
}

// fnInfo is what one function in the package does to DynamoDB, from its source text.
type fnInfo struct {
	name    string
	file    string
	actions map[string]bool // IAM actions, e.g. dynamodb:GetItem
	tables  map[string]bool // Stores table fields it names (s.jobs ...)
	indexes []string        // index names it queries
	queries int             // calls to Query
	callees []string
}

var (
	reAPICall    = regexp.MustCompile(`\.api\.(\w+)\(`)
	reTable      = regexp.MustCompile(`\bs\.(users|docs|jobs|exams|attempts)\b`)
	reIndexName  = regexp.MustCompile(`IndexName:\s*ptr\(("[\w-]+"|\w+)\)`)
	reIndexField = regexp.MustCompile(`\bIndexName:`)
	reCall       = regexp.MustCompile(`\b(\w+)\(`)
	// Transaction items are authorised by the write they contain.
	txItem = map[string]string{"Put:": "dynamodb:PutItem", "Update:": "dynamodb:UpdateItem", "Delete:": "dynamodb:DeleteItem", "ConditionCheck:": "dynamodb:ConditionCheckItem"}
)

// scanFunctions parses EVERY non-test .go file in dir (a new file is picked up with no
// registration) and returns its functions plus the package's string constants.
func scanFunctions(t *testing.T, dir string) (map[string]*fnInfo, map[string]string) {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(dir, "*.go"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no go files in %s: %v", dir, err)
	}
	fns := map[string]*fnInfo{}
	consts := map[string]string{}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		fset := token.NewFileSet()
		af, err := parser.ParseFile(fset, f, src, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, d := range af.Decls {
			switch d := d.(type) {
			case *ast.GenDecl:
				for _, sp := range d.Specs {
					if vs, ok := sp.(*ast.ValueSpec); ok && d.Tok == token.CONST {
						for i, n := range vs.Names {
							if i < len(vs.Values) {
								if bl, ok := vs.Values[i].(*ast.BasicLit); ok && bl.Kind == token.STRING {
									consts[n.Name] = strings.Trim(bl.Value, "\"")
								}
							}
						}
					}
				}
			case *ast.FuncDecl:
				if d.Body == nil {
					continue
				}
				body := string(src[fset.Position(d.Body.Pos()).Offset:fset.Position(d.Body.End()).Offset])
				fi := &fnInfo{name: d.Name.Name, file: filepath.Base(f), actions: map[string]bool{}, tables: map[string]bool{}}
				for _, m := range reAPICall.FindAllStringSubmatch(body, -1) {
					if m[1] == "TransactWriteItems" {
						continue // authorised through the items' own actions
					}
					fi.actions["dynamodb:"+m[1]] = true
					if m[1] == "Query" {
						fi.queries++
					}
				}
				for tok, act := range txItem {
					if strings.Contains(body, tok+" &types.") {
						fi.actions[act] = true
					}
				}
				for _, m := range reTable.FindAllStringSubmatch(body, -1) {
					fi.tables[m[1]] = true
				}
				all := reIndexName.FindAllStringSubmatch(body, -1)
				if n := len(reIndexField.FindAllString(body, -1)); n != len(all) {
					t.Errorf("%s.%s: %d IndexName uses but only %d are ptr(<literal or constant>); write it so the test can resolve it", fi.file, fi.name, n, len(all))
				}
				for _, m := range all {
					fi.indexes = append(fi.indexes, m[1])
				}
				for _, m := range reCall.FindAllStringSubmatch(body, -1) {
					fi.callees = append(fi.callees, m[1])
				}
				if _, dup := fns[fi.name]; dup {
					t.Errorf("function name %s declared twice in the package; the IAM scan keys by name", fi.name)
				}
				fns[fi.name] = fi
			}
		}
	}
	return fns, consts
}

// need is a set of "<resource>|<action>" pairs.
type need map[string]bool

// derive returns, per function, the (resource, action) pairs it needs, following calls to other
// package functions. A function owns the tables and actions of everything it calls (so ClaimJob
// gets the jobs table through updateJob); a helper that names no table (getItem, batchGet,
// condUpdate) is attributed through its callers.
func derive(t *testing.T, fns map[string]*fnInfo, consts map[string]string) map[string]need {
	t.Helper()
	var tables map[string]bool
	var closure func(name string, seen map[string]bool) (actions map[string]bool, indexes []string, queries int)
	closure = func(name string, seen map[string]bool) (map[string]bool, []string, int) {
		acts := map[string]bool{}
		fi := fns[name]
		if fi == nil || seen[name] {
			return acts, nil, 0
		}
		seen[name] = true
		for a := range fi.actions {
			acts[a] = true
		}
		for tb := range fi.tables {
			tables[tb] = true
		}
		idx, q := append([]string(nil), fi.indexes...), fi.queries
		for _, c := range fi.callees {
			if c == name {
				continue
			}
			ca, ci, cq := closure(c, seen)
			for a := range ca {
				acts[a] = true
			}
			idx, q = append(idx, ci...), q+cq
		}
		return acts, idx, q
	}
	out := map[string]need{}
	covered := map[string]bool{}
	for name, fi := range fns {
		seen := map[string]bool{}
		tables = map[string]bool{}
		acts, idx, q := closure(name, seen)
		if len(tables) == 0 {
			continue
		}
		for n := range seen {
			covered[n] = true
		}
		n := need{}
		for tb := range tables {
			res := tableResource[tb]
			indexed := 0
			for _, ix := range idx {
				ixName := strings.Trim(ix, `"`)
				if !strings.HasPrefix(ix, `"`) {
					v, ok := consts[ix]
					if !ok {
						t.Errorf("%s.%s: index %s is not a string constant the test can resolve", fi.file, name, ix)
						continue
					}
					ixName = v
				}
				n["${"+res+"}/index/"+ixName+"|dynamodb:Query"] = true
				indexed++
			}
			for a := range acts {
				if a == "dynamodb:Query" && indexed > 0 && q <= indexed {
					continue // every Query in this function goes through an index
				}
				n[res+"|"+a] = true
			}
		}
		out[name] = n
	}
	for name, fi := range fns {
		if len(fi.actions) > 0 && len(fi.tables) == 0 && !covered[name] {
			t.Errorf("%s.%s calls DynamoDB but no function that names a table reaches it: its table cannot be attributed", fi.file, name)
		}
	}
	return out
}

func granted(pol map[string][]string, resAction string) bool {
	res, act, _ := strings.Cut(resAction, "|")
	has := func(r string) bool {
		for _, a := range pol[r] {
			if a == act {
				return true
			}
		}
		return false
	}
	if has(res) {
		return true
	}
	if i := strings.Index(res, "/index/"); i >= 0 { // a wildcard on the same table's indexes
		return has(res[:i] + "/index/*")
	}
	return false
}

func storeNeeds(t *testing.T) map[string]need {
	t.Helper()
	fns, consts := scanFunctions(t, ".")
	return derive(t, fns, consts)
}

func TestStoreCodeIAMDerivation(t *testing.T) {
	doc := loadTemplate(t)
	api := policy(t, doc, "ApiFunction")
	derived := storeNeeds(t)

	// Everything any store method needs is granted to the API, on the right table and index.
	used := map[string]bool{}
	for name, n := range derived {
		for ra := range n {
			used[ra] = true
			if !granted(api, ra) {
				t.Errorf("%s needs %s but the API policy does not grant it (update template.yaml and wantAPIPolicy together)", name, strings.Replace(ra, "|", " ", 1))
			}
		}
	}
	// Every API DynamoDB grant is used by code or explicitly planned.
	for res, acts := range api {
		if strings.HasPrefix(res, "${UploadsBucket") || strings.HasPrefix(res, "UploadsBucket") || strings.HasPrefix(res, "WorkerFunction") {
			continue
		}
		for _, a := range acts {
			ra := res + "|" + a
			ok := used[ra]
			if !ok && strings.HasSuffix(res, "/index/*") { // wildcard: used when any index of that table is
				for u := range used {
					if strings.HasPrefix(u, strings.TrimSuffix(res, "*")) && strings.HasSuffix(u, "|"+a) {
						ok = true
					}
				}
			}
			if _, planned := plannedAPIGrants[ra]; !ok && !planned {
				t.Errorf("API grants %s on %s that no store method uses; remove it or list it in plannedAPIGrants with a spec reference", a, res)
			}
		}
	}
	for ra := range plannedAPIGrants {
		if used[ra] {
			t.Errorf("plannedAPIGrants lists %s but code now uses it; delete the entry", ra)
		}
	}

	// Package-wide bans, including the interface (a new method means a new IAM action).
	files, _ := filepath.Glob("*.go")
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		b, _ := os.ReadFile(f)
		for _, banned := range []string{".Scan(", "BatchWriteItem", "ConditionCheck:", "types.ConditionCheck{", "ExecuteStatement", "ExecuteTransaction"} {
			if strings.Contains(string(b), banned) {
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
}

// workerStoreMethods lists the store methods the worker code path references: every
// <x>.Jobs.<Method> and <x>.Docs.<Method> in internal/gen (the Worker's JobStore and DocStore
// fields; the API-side Service uses lowercase fields, so it is not matched). Static analysis,
// because running the worker over a recording fake would need a generator, blob and documents
// and would only cover the branches a fixture reaches; the source scan covers every branch.
func workerStoreMethods(t *testing.T) map[string]string {
	t.Helper()
	files, err := filepath.Glob("../../gen/*.go")
	if err != nil || len(files) == 0 {
		t.Fatalf("no go files in internal/gen: %v", err)
	}
	re := regexp.MustCompile(`\.(Jobs|Docs)\.(\w+)`)
	out := map[string]string{}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		b, _ := os.ReadFile(f)
		for _, m := range re.FindAllStringSubmatch(string(b), -1) {
			out[m[2]] = fmt.Sprintf("%s.%s in %s", m[1], m[2], filepath.Base(f))
		}
	}
	return out
}

func TestWorkerPolicyMatchesWorkerCodePath(t *testing.T) {
	doc := loadTemplate(t)
	derived := storeNeeds(t)
	methods := workerStoreMethods(t)
	if len(methods) < 5 {
		t.Fatalf("found only %v worker store calls; the scan is broken", methods)
	}
	pol := policy(t, doc, "WorkerFunction")
	used := map[string]bool{}
	for m, where := range methods {
		n, ok := derived[m]
		if !ok {
			t.Errorf("worker calls %s (%s) but no such store function was found", m, where)
			continue
		}
		for ra := range n {
			used[ra] = true
			if !granted(pol, ra) {
				t.Errorf("worker calls %s (%s) which needs %s; the worker policy does not grant it", m, where, strings.Replace(ra, "|", " ", 1))
			}
		}
	}
	// The grant must also be no wider than the code path.
	for res, acts := range pol {
		if strings.HasPrefix(res, "${UploadsBucket") {
			continue
		}
		for _, a := range acts {
			if !used[res+"|"+a] {
				t.Errorf("worker policy grants %s on %s but no worker store call needs it", a, res)
			}
		}
	}
}
