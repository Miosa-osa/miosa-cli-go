package commands

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
)

type seen struct {
	Method, Path, Query string
	Body                map[string]any
	Auth                string
}

// opServer records requests and answers with a fixed body.
func opServer(t *testing.T, status int, body string) (*httptest.Server, func() []seen) {
	t.Helper()
	var mu sync.Mutex
	var reqs []seen
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s := seen{Method: r.Method, Path: r.URL.Path, Query: r.URL.RawQuery, Auth: r.Header.Get("Authorization")}
		if b, _ := io.ReadAll(r.Body); len(b) > 0 {
			_ = json.Unmarshal(b, &s.Body)
		}
		mu.Lock()
		reqs = append(reqs, s)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("MIOSA_API_KEY", "msk_u_test")
	t.Setenv("MIOSA_BASE_URL", srv.URL)
	return srv, func() []seen { mu.Lock(); defer mu.Unlock(); return append([]seen(nil), reqs...) }
}

func runOp(t *testing.T, op Op, stdin string, args ...string) (string, error) {
	t.Helper()
	ResetForTest()
	cmd := op.command("demo")
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetIn(strings.NewReader(stdin))
	cmd.SetArgs(args)
	root := Root()
	_ = root
	// Run through PersistentPreRun-less path: globals were reset above.
	err := cmd.Execute()
	return buf.String(), err
}

func TestOpBuildsPathQueryAndBodyFromArgsAndFlags(t *testing.T) {
	_, got := opServer(t, 200, `{"data":{"id":"x"}}`)
	op := Op{
		Use: "make <name> [env]", Method: "POST", Path: "/things/{0}/make/{1}",
		Args: []Arg{{Name: "name"}, {Name: "env", Optional: true}},
		Flags: []Flag{
			{Name: "size", Usage: "size", Default: "small", Enum: []string{"small", "large"}},
			{Name: "count", Type: "int", Usage: "n"},
			{Name: "dry-run", Type: "bool", Usage: "dry", In: "query"},
			{Name: "tag", Type: "strings", Usage: "tags"},
			{Name: "label", Type: "kv", Usage: "labels", Field: "metadata.labels"},
			{Name: "ttl", Type: "int", Field: "limits.ttl_seconds"},
		},
		Detail: []Col{{Head: "ID", Path: "id"}},
	}
	out, err := runOp(t, op, "", "my box", "--count", "3", "--dry-run", "--tag", "a,b", "--tag", "c", "--label", "k=v", "--label", "x=y", "--ttl", "60", "--size", "large")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	r := got()
	if len(r) != 1 {
		t.Fatalf("requests = %+v", r)
	}
	if r[0].Method != "POST" || r[0].Path != "/things/my box/make" {
		t.Fatalf("path = %q (optional arg should collapse)", r[0].Path)
	}
	if r[0].Query != "dry_run=true" {
		t.Fatalf("query = %q", r[0].Query)
	}
	b := r[0].Body
	if b["size"] != "large" || b["count"] != float64(3) {
		t.Fatalf("body = %+v", b)
	}
	tags, _ := b["tag"].([]any)
	if len(tags) != 3 || tags[0] != "a" || tags[2] != "c" {
		t.Fatalf("tags = %+v", b["tag"])
	}
	labels := b["metadata"].(map[string]any)["labels"].(map[string]any)
	if labels["k"] != "v" || labels["x"] != "y" {
		t.Fatalf("labels = %+v", labels)
	}
	if b["limits"].(map[string]any)["ttl_seconds"] != float64(60) {
		t.Fatalf("nested int = %+v", b["limits"])
	}
	if r[0].Auth != "Bearer msk_u_test" {
		t.Fatalf("auth = %q", r[0].Auth)
	}
	if !strings.Contains(out, "ID") || !strings.Contains(out, "x") {
		t.Fatalf("detail output = %q", out)
	}
}

func TestOpPathArgsAreEscaped(t *testing.T) {
	_, got := opServer(t, 200, `{}`)
	op := Op{Use: "get <id>", Method: "GET", Path: "/things/{0}", Args: []Arg{{Name: "id"}}}
	if _, err := runOp(t, op, "", "a/b c"); err != nil {
		t.Fatal(err)
	}
	if p := got()[0].Path; p != "/things/a/b c" {
		// The server sees the decoded path; the escaped form is %2F.
		t.Fatalf("path = %q", p)
	}
}

func TestOpGetSendsNoBodyAndFlagsGoToTheQuery(t *testing.T) {
	_, got := opServer(t, 200, `{"data":[]}`)
	op := Op{Use: "list", Method: "GET", Path: "/things", Flags: []Flag{{Name: "limit", Type: "int"}, {Name: "state"}}, Cols: []Col{{Head: "ID", Path: "id"}}}
	out, err := runOp(t, op, "", "--limit", "5", "--state", "running")
	if err != nil {
		t.Fatal(err)
	}
	r := got()[0]
	q, _ := url.ParseQuery(r.Query)
	if q.Get("limit") != "5" || q.Get("state") != "running" || r.Body != nil {
		t.Fatalf("req = %+v", r)
	}
	if !strings.Contains(out, "No results.") {
		t.Fatalf("empty list output = %q", out)
	}
}

func TestOpUnsetFlagsAreOmitted(t *testing.T) {
	_, got := opServer(t, 200, `{}`)
	op := Op{Use: "set", Method: "PATCH", Path: "/x", Flags: []Flag{{Name: "name"}, {Name: "enabled", Type: "bool"}, {Name: "n", Type: "int"}}}
	if _, err := runOp(t, op, "", "--name", "z"); err != nil {
		t.Fatal(err)
	}
	b := got()[0].Body
	if len(b) != 1 || b["name"] != "z" {
		t.Fatalf("body = %+v", b)
	}
}

func TestOpEnumAndRequiredFlags(t *testing.T) {
	opServer(t, 200, `{}`)
	op := Op{Use: "x", Method: "POST", Path: "/x", Flags: []Flag{{Name: "mode", Enum: []string{"a", "b"}}, {Name: "must", Required: true}}}
	if _, err := runOp(t, op, "", "--mode", "a"); err == nil || !strings.Contains(err.Error(), "must") {
		t.Fatalf("missing required flag: %v", err)
	}
	_, err := runOp(t, op, "", "--must", "1", "--mode", "zzz")
	if err == nil || !strings.Contains(err.Error(), "must be one of a, b") {
		t.Fatalf("bad enum: %v", err)
	}
	if ExitCode(err) != ExitUsage {
		t.Fatalf("exit = %d", ExitCode(err))
	}
}

func TestOpKVFlagRejectsMalformedPairs(t *testing.T) {
	opServer(t, 200, `{}`)
	op := Op{Use: "x", Method: "POST", Path: "/x", Flags: []Flag{{Name: "env", Type: "kv"}}}
	if _, err := runOp(t, op, "", "--env", "NOEQUALS"); err == nil || !strings.Contains(err.Error(), "KEY=VALUE") {
		t.Fatalf("err = %v", err)
	}
}

func TestOpConfirmRefusesWithoutYesOffATerminal(t *testing.T) {
	_, got := opServer(t, 200, `{}`)
	op := Op{Use: "rm <id>", Method: "DELETE", Path: "/things/{0}", Args: []Arg{{Name: "id"}}, Confirm: "Delete {0}?", Done: "Deleted {0}"}
	_, err := runOp(t, op, "y\n", "t1")
	if err == nil || !strings.Contains(err.Error(), "--yes") || ExitCode(err) != ExitUsage {
		t.Fatalf("err = %v", err)
	}
	if len(got()) != 0 {
		t.Fatal("nothing may be sent before confirmation")
	}
	out, err := runOp(t, op, "", "t1", "--yes")
	if err != nil || !strings.Contains(out, "Deleted t1") {
		t.Fatalf("%v %q", err, out)
	}
	if len(got()) != 1 || got()[0].Method != "DELETE" {
		t.Fatalf("requests = %+v", got())
	}
}

func TestOpRendersTableDetailDoneAndJSON(t *testing.T) {
	body := `{"data":[{"id":"8ba07e95-5085-4600-b758-60b6951c5786","name":"web","created_at":"2020-01-01T00:00:00Z","size":2048,"tags":["a","b"],"cost":250,"n":4124615}]}`
	opServer(t, 200, body)
	op := Op{Use: "list", Method: "GET", Path: "/things", Cols: []Col{
		{Head: "ID", Path: "id", Fmt: "short"}, {Head: "NAME", Path: "name"}, {Head: "AGE", Path: "created_at", Fmt: "age"},
		{Head: "SIZE", Path: "size", Fmt: "bytes"}, {Head: "TAGS", Path: "tags", Fmt: "list"}, {Head: "COST", Path: "cost", Fmt: "cents"}, {Head: "N", Path: "n"},
	}}
	out, err := runOp(t, op, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"ID", "8ba07e95", "web", "2.0 KB", "a,b", "$2.50", "4124615"} {
		if !strings.Contains(out, want) {
			t.Errorf("table lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "8ba07e95-5085") {
		t.Errorf("short id not applied:\n%s", out)
	}
	if strings.Contains(out, "e+") {
		t.Errorf("large numbers must not print in scientific notation:\n%s", out)
	}

	jop := Op{Use: "list", Method: "GET", Path: "/things", Cols: op.Cols}
	ResetForTest()
	jcmd := jop.command("demo")
	var buf bytes.Buffer
	jcmd.SetOut(&buf)
	jcmd.SetArgs(nil)
	// --output is a root flag; set the global directly as the root would.
	globalFlags.Output = "json"
	if err := jcmd.Execute(); err != nil {
		t.Fatal(err)
	}
	var parsed map[string]any
	if json.Unmarshal(buf.Bytes(), &parsed) != nil {
		t.Fatalf("not JSON: %s", buf.String())
	}
}

func TestOpErrorsCarryTheAPICode(t *testing.T) {
	opServer(t, 404, `{"error":{"code":"NOT_FOUND","message":"thing not found"}}`)
	op := Op{Use: "get <id>", Method: "GET", Path: "/things/{0}", Args: []Arg{{Name: "id"}}}
	_, err := runOp(t, op, "", "zzz")
	if err == nil || !strings.Contains(err.Error(), "thing not found [NOT_FOUND]") || ExitCode(err) != ExitNotFound {
		t.Fatalf("err = %v exit=%d", err, ExitCode(err))
	}
}

func TestOpNoAuthWorksSignedOut(t *testing.T) {
	_, got := opServer(t, 200, `{"ok":true}`)
	t.Setenv("MIOSA_API_KEY", "")
	op := Op{Use: "pub", Method: "GET", Path: "/public", NoAuth: true}
	if _, err := runOp(t, op, ""); err != nil {
		t.Fatal(err)
	}
	if got()[0].Auth != "" {
		t.Fatalf("auth = %q", got()[0].Auth)
	}
	authed := Op{Use: "priv", Method: "GET", Path: "/private"}
	if _, err := runOp(t, authed, ""); err == nil || ExitCode(err) != ExitAuth {
		t.Fatalf("err = %v", err)
	}
}

func TestOpBodyHookCanReshapeTheRequest(t *testing.T) {
	_, got := opServer(t, 200, `{}`)
	op := Op{Use: "x <a>", Method: "POST", Path: "/x", Args: []Arg{{Name: "a"}}, Flags: []Flag{{Name: "name"}},
		Body: func(args []string, body map[string]any) (map[string]any, error) {
			body["from_arg"] = args[0]
			return body, nil
		}}
	if _, err := runOp(t, op, "", "hello", "--name", "n"); err != nil {
		t.Fatal(err)
	}
	if b := got()[0].Body; b["from_arg"] != "hello" || b["name"] != "n" {
		t.Fatalf("body = %+v", b)
	}
}

func TestFindRowsAndLookup(t *testing.T) {
	var v any
	_ = json.Unmarshal([]byte(`{"items":[{"a":{"b":"deep"}}],"meta":{}}`), &v)
	rows, ok := findRows(v, "")
	if !ok || len(rows) != 1 {
		t.Fatalf("rows = %+v", rows)
	}
	if got := formatValue(lookupPath(rows[0], "a.b"), ""); got != "deep" {
		t.Fatalf("got %q", got)
	}
	if formatValue(lookupPath(rows[0], "a.missing"), "") != "" {
		t.Fatal("missing path must be empty")
	}
}
