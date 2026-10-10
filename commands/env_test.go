package commands_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/Miosa-osa/miosa-cli-go/commands"
)

// call is one request the fake API saw.
type call struct {
	Method, Path, Query string
	Body                map[string]interface{}
	Headers             http.Header
}

func (c call) is(method, path string) bool { return c.Method == method && c.Path == path }

// fakeAPI is a recording httptest server. reply decides each response.
type fakeAPI struct {
	mu    sync.Mutex
	calls []call
	srv   *httptest.Server
}

func newFakeAPI(t *testing.T, reply func(c call) (int, string)) *fakeAPI {
	t.Helper()
	f := &fakeAPI{}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c := call{Method: r.Method, Path: r.URL.Path, Query: r.URL.RawQuery, Headers: r.Header.Clone()}
		if b, _ := io.ReadAll(r.Body); len(b) > 0 {
			_ = json.Unmarshal(b, &c.Body)
		}
		f.mu.Lock()
		f.calls = append(f.calls, c)
		f.mu.Unlock()
		status, body := reply(c)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(f.srv.Close)
	setupEnv(t, f.srv)
	return f
}

func (f *fakeAPI) find(method, path string) []call {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []call
	for _, c := range f.calls {
		if c.is(method, path) {
			out = append(out, c)
		}
	}
	return out
}

func (f *fakeAPI) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

// runIn is run with a stdin.
func runIn(t *testing.T, stdin string, args ...string) (string, error) {
	t.Helper()
	commands.ResetForTest()
	root := commands.Root()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetIn(strings.NewReader(stdin))
	root.SetArgs(args)
	t.Cleanup(func() { root.SetIn(nil) })
	err := root.Execute()
	return buf.String(), err
}

const envJSON = `{"id":"e-base","name":"base","is_default":true,"latest_version":3,"safe_for_third_parties":false,
"pass_github":true,"pass_secrets":true,"pass_sandbox_credentials":false,"pass_agents_credentials":true,
"effective":{"pass_github":true,"pass_secrets":true,"pass_sandbox_credentials":false,"pass_agents_credentials":true},
"pinned_machine_count":5,"outdated_machine_count":2,
"variables":[{"name":"STRIPE_KEY","preview":"sk_liv...0123"},{"name":"EMPTY","preview":"*"}],
"secret_files":[{"path":"backend/.env","size_bytes":120}],
"repositories":[{"source":"github","repo":"octocat/hello-world","base_branch":"develop","setup_script":"npm ci","setup_blocking":true,"path":"hello-world"}]}`

const stagingJSON = `{"id":"e-stg","name":"staging","is_default":false,"latest_version":1,"variables":[],"secret_files":[],"repositories":[]}`

// envAPI serves the environments contract with the two fixtures above.
func envAPI(t *testing.T) *fakeAPI {
	return newFakeAPI(t, func(c call) (int, string) {
		switch {
		case c.is("GET", "/environments"):
			return 200, `{"environments":[` + envJSON + `,` + stagingJSON + `],"default_environment_id":"e-base"}`
		case c.is("GET", "/environments/base"), c.is("GET", "/environments/e-base"):
			return 200, `{"environment":` + envJSON + `}`
		case c.is("GET", "/environments/base/variables/STRIPE_KEY/reveal"), c.is("GET", "/environments/e-base/variables/STRIPE_KEY/reveal"):
			return 200, `{"name":"STRIPE_KEY","value":"sk_live_realvalue_0123"}`
		case c.is("GET", "/environments/base/variables/EMPTY/reveal"), c.is("GET", "/environments/e-base/variables/EMPTY/reveal"):
			return 200, `{"name":"EMPTY","value":""}`
		case c.is("GET", "/environments/base/versions"):
			return 200, `{"versions":[{"version":3,"pinned_machine_count":2,"variable_names":["A","B"],"secret_file_paths":["x"],"repositories":[],"created_at":"2026-10-09T12:30:00Z"},{"version":2,"pinned_machine_count":3,"variable_names":[],"secret_file_paths":[],"repositories":[],"safe_for_third_parties":true}]}`
		case c.Method == "POST" && c.Path == "/environments":
			return 201, `{"environment":{"id":"e-new","name":"` + c.Body["name"].(string) + `","latest_version":1}}`
		case c.Method == "POST" && strings.HasSuffix(c.Path, "/rename"):
			return 200, `{"environment":{"id":"e-stg","name":"prod","latest_version":1}}`
		case c.Method == "POST" && strings.HasSuffix(c.Path, "/default"):
			return 200, `{"environment":{"id":"e-stg","name":"staging","is_default":true,"latest_version":1}}`
		case c.Method == "PATCH":
			return 200, `{"environment":{"id":"e-base","name":"base","latest_version":4}}`
		case c.Method == "PUT", c.Method == "DELETE":
			return 200, `{"success":true}`
		case c.Method == "POST" && strings.HasSuffix(c.Path, "/repositories"):
			return 200, `{"success":true}`
		}
		return 404, `{"error":{"code":"NOT_FOUND","message":"no route"}}`
	})
}

func TestEnvList(t *testing.T) {
	envAPI(t)
	out, err := run(t, "env", "list")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"base *", "staging", "v3", "LATEST", "OUTDATED"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if strings.Contains(out, "sk_liv") {
		t.Errorf("list must not print any variable data:\n%s", out)
	}
}

func TestEnvListJSONHasCountsNotValues(t *testing.T) {
	envAPI(t)
	out, err := run(t, "env", "list", "-o", "json")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "sk_liv") || strings.Contains(out, "STRIPE_KEY") {
		t.Errorf("json list leaked variable data:\n%s", out)
	}
	var parsed struct {
		Environments []struct {
			Name      string `json:"name"`
			Variables int    `json:"variables"`
		} `json:"environments"`
	}
	if err := json.Unmarshal([]byte(out), &parsed); err != nil || len(parsed.Environments) != 2 || parsed.Environments[0].Variables != 2 {
		t.Fatalf("bad json: %v\n%s", err, out)
	}
}

func TestEnvInfoMasksValues(t *testing.T) {
	api := envAPI(t)
	for _, args := range [][]string{{"env", "info", "base"}, {"env", "get", "base"}, {"env", "info", "base", "-o", "json"}} {
		out, err := run(t, args...)
		if err != nil {
			t.Fatal(err)
		}
		for _, leak := range []string{"sk_liv", "0123", "realvalue"} {
			if strings.Contains(out, leak) {
				t.Errorf("%v leaked %q:\n%s", args, leak, out)
			}
		}
		if !strings.Contains(out, "STRIPE_KEY") || !strings.Contains(out, "********") {
			t.Errorf("%v should list names with masked values:\n%s", args, out)
		}
		if !strings.Contains(out, "backend/.env") || !strings.Contains(out, "octocat/hello-world") {
			t.Errorf("%v should list files and repos:\n%s", args, out)
		}
	}
	if n := len(api.find("GET", "/environments/e-base/variables/STRIPE_KEY/reveal")); n != 0 {
		t.Errorf("reveal endpoint must not be called without --reveal, got %d", n)
	}
}

func TestEnvInfoReveal(t *testing.T) {
	api := envAPI(t)
	out, err := run(t, "env", "info", "base", "--reveal")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "STRIPE_KEY=sk_live_realvalue_0123") {
		t.Errorf("--reveal should print the value:\n%s", out)
	}
	if len(api.find("GET", "/environments/e-base/variables/STRIPE_KEY/reveal")) != 1 {
		t.Error("expected one reveal call per variable")
	}
}

func TestEnvInfoEmptyValueNotConfusedWithMask(t *testing.T) {
	envAPI(t)
	out, err := run(t, "env", "info", "base")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "EMPTY=\n") {
		t.Errorf("an empty value must still be masked without --reveal:\n%s", out)
	}
}

func TestEnvCreateRenameDefaultDelete(t *testing.T) {
	api := envAPI(t)

	if _, err := run(t, "env", "create", "qa", "--safe-for-third-parties"); err != nil {
		t.Fatal(err)
	}
	got := api.find("POST", "/environments")
	if len(got) != 1 || got[0].Body["name"] != "qa" || got[0].Body["safe_for_third_parties"] != true {
		t.Fatalf("create body: %+v", got)
	}

	if _, err := run(t, "env", "create", "plain"); err != nil {
		t.Fatal(err)
	}
	if _, set := api.find("POST", "/environments")[1].Body["safe_for_third_parties"]; set {
		t.Error("safe_for_third_parties must be omitted unless the flag is passed")
	}

	if _, err := run(t, "env", "rename", "staging", "prod"); err != nil {
		t.Fatal(err)
	}
	if r := api.find("POST", "/environments/staging/rename"); len(r) != 1 || r[0].Body["name"] != "prod" {
		t.Fatalf("rename: %+v", r)
	}

	if _, err := run(t, "env", "default", "staging"); err != nil {
		t.Fatal(err)
	}
	if len(api.find("POST", "/environments/staging/default")) != 1 {
		t.Fatal("default not called")
	}

	if _, err := run(t, "env", "delete", "staging", "--force"); err != nil {
		t.Fatal(err)
	}
	if len(api.find("DELETE", "/environments/staging")) != 1 {
		t.Fatal("delete not called")
	}
}

func TestEnvDeleteConfirmation(t *testing.T) {
	api := envAPI(t)
	if _, err := runIn(t, "n\n", "env", "delete", "staging"); err == nil {
		t.Fatal("answering n must abort")
	}
	if _, err := runIn(t, "", "env", "delete", "staging"); err == nil {
		t.Fatal("EOF must abort")
	}
	if len(api.find("DELETE", "/environments/staging")) != 0 {
		t.Fatal("nothing may be deleted without a yes")
	}
	if _, err := runIn(t, "yes\n", "env", "rm", "staging"); err != nil {
		t.Fatal(err)
	}
	if len(api.find("DELETE", "/environments/staging")) != 1 {
		t.Fatal("yes should delete")
	}
}

func TestEnvSetVar(t *testing.T) {
	api := envAPI(t)
	out, err := runIn(t, "postgres://u:supersecretpw@h/db\n", "env", "set-var", "--env", "staging", "URL")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "supersecretpw") {
		t.Errorf("set-var must not echo values:\n%s", out)
	}
	u := api.find("PUT", "/environments/staging/variables/URL")
	if len(u) != 1 || u[0].Body["value"] != "postgres://u:supersecretpw@h/db" {
		t.Fatalf("calls: %+v", u)
	}
}

func TestEnvSetVarRefusesAValueArgument(t *testing.T) {
	api := envAPI(t)
	out, err := run(t, "env", "set", "--env", "staging", "STRIPE_KEY=sk_live_123")
	if err == nil {
		t.Fatal("a NAME=VALUE argument must be refused")
	}
	if strings.Contains(out+err.Error(), "sk_live_123") {
		t.Errorf("the refused value must not be echoed: %v", err)
	}
	if len(api.find("PUT", "/environments/staging/variables/STRIPE_KEY")) != 0 {
		t.Fatalf("nothing may be sent: %+v", api.calls)
	}
}

func TestEnvSetVarDefaultsToDefaultEnvironment(t *testing.T) {
	api := envAPI(t)
	if _, err := runIn(t, "v\n", "env", "set-var", "K"); err != nil {
		t.Fatal(err)
	}
	if len(api.find("GET", "/environments")) != 1 || len(api.find("PUT", "/environments/base/variables/K")) != 1 {
		t.Fatalf("should resolve the default (base) then set: %+v", api.calls)
	}
}

func TestEnvSetVarFromFile(t *testing.T) {
	api := envAPI(t)
	p := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(p, []byte("# c\nexport A=1\nB=\"two words\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// CLI args win over the file.
	if _, err := runIn(t, "override\n", "env", "set-var", "--env", "staging", "--from-file", p, "A"); err != nil {
		t.Fatal(err)
	}
	if c := api.find("PUT", "/environments/staging/variables/A"); len(c) != 1 || c[0].Body["value"] != "override" {
		t.Fatalf("A: %+v", c)
	}
	if c := api.find("PUT", "/environments/staging/variables/B"); len(c) != 1 || c[0].Body["value"] != "two words" {
		t.Fatalf("B: %+v", c)
	}
}

func TestEnvSetVarFromStdin(t *testing.T) {
	api := envAPI(t)
	if _, err := runIn(t, "TOKEN=abc\n", "env", "set-var", "--env", "staging", "--from-file", "-"); err != nil {
		t.Fatal(err)
	}
	if c := api.find("PUT", "/environments/staging/variables/TOKEN"); len(c) != 1 || c[0].Body["value"] != "abc" {
		t.Fatalf("TOKEN: %+v", c)
	}
}

func TestEnvSetVarValidationMakesNoRequest(t *testing.T) {
	api := envAPI(t)
	bad := [][]string{
		{"env", "set-var", "--env", "staging"},
		{"env", "set-var", "--env", "staging", "A=1"},
		{"env", "set-var", "--env", "staging", "bad-name"},
		{"env", "set-var", "--env", "staging", "1BAD"},
		{"env", "set-var", "--env", "staging", "--from-file", "/nonexistent/.env"},
		{"env", "unset-var", "--env", "staging", "bad-name"},
	}
	for _, args := range bad {
		if _, err := run(t, args...); err == nil {
			t.Errorf("%v: want error", args)
		}
	}
	if api.count() != 0 {
		t.Errorf("validation errors must not reach the API, saw %+v", api.calls)
	}
}

func TestEnvSetVarMalformedDotenvDoesNotEchoContent(t *testing.T) {
	envAPI(t)
	p := filepath.Join(t.TempDir(), ".env")
	_ = os.WriteFile(p, []byte("GOOD=1\nsuper-secret-garbage\n"), 0o600)
	out, err := run(t, "env", "set-var", "--env", "staging", "--from-file", p)
	if err == nil {
		t.Fatal("want error")
	}
	if strings.Contains(out, "super-secret-garbage") {
		t.Errorf("error leaked file content:\n%s", out)
	}
}

func TestEnvUnsetVar(t *testing.T) {
	api := envAPI(t)
	if _, err := run(t, "env", "unset-var", "--env", "staging", "A", "B"); err != nil {
		t.Fatal(err)
	}
	if len(api.find("DELETE", "/environments/staging/variables/A")) != 1 || len(api.find("DELETE", "/environments/staging/variables/B")) != 1 {
		t.Fatalf("calls: %+v", api.calls)
	}
}

func TestEnvAddAndRemoveFile(t *testing.T) {
	api := envAPI(t)
	p := filepath.Join(t.TempDir(), "local.env")
	_ = os.WriteFile(p, []byte("DB=postgres://x\n"), 0o600)
	out, err := run(t, "env", "add-file", "--env", "staging", "hello-world/backend/.env", p)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "postgres") {
		t.Errorf("add-file must not echo contents:\n%s", out)
	}
	c := api.find("PUT", "/environments/staging/files")
	if len(c) != 1 || c[0].Body["path"] != "hello-world/backend/.env" || c[0].Body["contents"] != "DB=postgres://x\n" {
		t.Fatalf("add-file: %+v", c)
	}

	if _, err := runIn(t, "FROM=stdin\n", "env", "add-file", "--env", "staging", ".env", "-"); err != nil {
		t.Fatal(err)
	}
	if c := api.find("PUT", "/environments/staging/files"); len(c) != 2 || c[1].Body["contents"] != "FROM=stdin\n" {
		t.Fatalf("stdin add-file: %+v", c)
	}

	if _, err := run(t, "env", "rm-file", "--env", "staging", "hello-world/backend/.env"); err != nil {
		t.Fatal(err)
	}
	d := api.find("DELETE", "/environments/staging/files")
	if len(d) != 1 || d[0].Query != "path=hello-world%2Fbackend%2F.env" {
		t.Fatalf("rm-file: %+v", d)
	}
}

func TestEnvAddFileValidation(t *testing.T) {
	api := envAPI(t)
	dir := t.TempDir()
	ok := filepath.Join(dir, "ok")
	_ = os.WriteFile(ok, []byte("x"), 0o600)
	big := filepath.Join(dir, "big")
	_ = os.WriteFile(big, bytes.Repeat([]byte("a"), 256*1024+1), 0o600)
	bad := [][]string{
		{"env", "add-file", "--env", "s", "/etc/passwd", ok},
		{"env", "add-file", "--env", "s", "../x", ok},
		{"env", "add-file", "--env", "s", "a//b", ok},
		{"env", "add-file", "--env", "s", "x", big},
		{"env", "add-file", "--env", "s", "x", filepath.Join(dir, "missing")},
		{"env", "rm-file", "--env", "s", "../x"},
	}
	for _, args := range bad {
		if _, err := run(t, args...); err == nil {
			t.Errorf("%v: want error", args)
		}
	}
	if api.count() != 0 {
		t.Errorf("validation errors must not reach the API: %+v", api.calls)
	}
}

func TestEnvRepoAdd(t *testing.T) {
	api := envAPI(t)
	script := filepath.Join(t.TempDir(), "setup.sh")
	_ = os.WriteFile(script, []byte("npm ci\n"), 0o600)

	if _, err := run(t, "env", "repo", "add", "--env", "staging", "octocat/hello-world", "--branch", "develop", "--setup-file", script, "--blocking"); err != nil {
		t.Fatal(err)
	}
	c := api.find("POST", "/environments/staging/repositories")
	if len(c) != 1 {
		t.Fatalf("calls: %+v", api.calls)
	}
	want := map[string]interface{}{"repo": "octocat/hello-world", "source": "github", "base_branch": "develop", "setup_script": "npm ci\n", "setup_blocking": true}
	for k, v := range want {
		if c[0].Body[k] != v {
			t.Errorf("%s: got %v want %v", k, c[0].Body[k], v)
		}
	}

	if _, err := run(t, "env", "repo", "add", "--env", "staging", "octocat/hello-world", "--setup-file", script, "--non-blocking"); err != nil {
		t.Fatal(err)
	}
	if got := api.find("POST", "/environments/staging/repositories")[1].Body["setup_blocking"]; got != false {
		t.Errorf("--non-blocking must send false, got %v", got)
	}

	if _, err := run(t, "env", "repo", "add", "--env", "staging", "octocat/hello-world"); err != nil {
		t.Fatal(err)
	}
	last := api.find("POST", "/environments/staging/repositories")[2].Body
	if _, set := last["setup_blocking"]; set {
		t.Error("setup_blocking must be omitted when neither flag is given")
	}
	if _, set := last["base_branch"]; set {
		t.Error("base_branch must be omitted when --branch is not given")
	}
}

func TestEnvRepoAddValidation(t *testing.T) {
	api := envAPI(t)
	dir := t.TempDir()
	script := filepath.Join(dir, "s.sh")
	_ = os.WriteFile(script, []byte("x"), 0o600)
	big := filepath.Join(dir, "big.sh")
	_ = os.WriteFile(big, bytes.Repeat([]byte("a"), 64*1024+1), 0o600)
	bin := filepath.Join(dir, "bin.sh")
	_ = os.WriteFile(bin, []byte{0xff, 0xfe}, 0o600)
	bad := map[string][]string{
		"blocking and non-blocking": {"env", "repo", "add", "--env", "s", "a/b", "--setup-file", script, "--blocking", "--non-blocking"},
		"blocking without script":   {"env", "repo", "add", "--env", "s", "a/b", "--blocking"},
		"bad repo":                  {"env", "repo", "add", "--env", "s", "justname"},
		"bad source":                {"env", "repo", "add", "--env", "s", "a/b", "--source", "gitlab"},
		"setup too big":             {"env", "repo", "add", "--env", "s", "a/b", "--setup-file", big},
		"setup not utf8":            {"env", "repo", "add", "--env", "s", "a/b", "--setup-file", bin},
		"setup missing":             {"env", "repo", "add", "--env", "s", "a/b", "--setup-file", filepath.Join(dir, "nope")},
		"rm bad repo":               {"env", "repo", "rm", "--env", "s", "justname"},
	}
	for name, args := range bad {
		if _, err := run(t, args...); err == nil {
			t.Errorf("%s: want error", name)
		}
	}
	if api.count() != 0 {
		t.Errorf("validation errors must not reach the API: %+v", api.calls)
	}
}

func TestEnvRepoRm(t *testing.T) {
	api := envAPI(t)
	if _, err := run(t, "env", "repo", "rm", "--env", "staging", "octocat/hello-world"); err != nil {
		t.Fatal(err)
	}
	d := api.find("DELETE", "/environments/staging/repositories")
	if len(d) != 1 || d[0].Query != "repo=octocat%2Fhello-world" {
		t.Fatalf("rm: %+v", d)
	}
}

func TestEnvToggle(t *testing.T) {
	api := envAPI(t)
	cases := []struct {
		section, state, field string
		want                  bool
	}{
		{"github", "off", "pass_github", false},
		{"secrets", "on", "pass_secrets", true},
		{"miosa-credentials", "on", "pass_sandbox_credentials", true},
		{"agents-credentials", "off", "pass_agents_credentials", false},
	}
	for i, tc := range cases {
		if _, err := run(t, "env", "toggle", "--env", "staging", tc.section, tc.state); err != nil {
			t.Fatalf("%s: %v", tc.section, err)
		}
		patches := api.find("PATCH", "/environments/staging")
		body := patches[i].Body
		if len(body) != 1 || body[tc.field] != tc.want {
			t.Errorf("%s %s: body %v want only %s=%v", tc.section, tc.state, body, tc.field, tc.want)
		}
	}
}

func TestEnvToggleAndSafeValidation(t *testing.T) {
	api := envAPI(t)
	bad := [][]string{
		{"env", "toggle", "--env", "s", "nope", "on"},
		{"env", "toggle", "--env", "s", "github", "maybe"},
		{"env", "toggle", "--env", "s", "github"},
		{"env", "safe-third-parties", "--env", "s", "perhaps"},
	}
	for _, args := range bad {
		if _, err := run(t, args...); err == nil {
			t.Errorf("%v: want error", args)
		}
	}
	if api.count() != 0 {
		t.Errorf("must not reach the API: %+v", api.calls)
	}
}

func TestEnvSafeThirdParties(t *testing.T) {
	api := envAPI(t)
	for i, tc := range []struct {
		arg  string
		want bool
	}{{"on", true}, {"off", false}} {
		if _, err := run(t, "env", "safe-third-parties", "--env", "staging", tc.arg); err != nil {
			t.Fatal(err)
		}
		if got := api.find("PATCH", "/environments/staging")[i].Body["safe_for_third_parties"]; got != tc.want {
			t.Errorf("%s: got %v", tc.arg, got)
		}
	}
}

func TestEnvVersions(t *testing.T) {
	envAPI(t)
	out, err := run(t, "env", "versions", "base")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"v3", "v2", "MACHINES", "2026-10-09"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q:\n%s", want, out)
		}
	}
}

func TestEnvServerErrorSurfaces(t *testing.T) {
	newFakeAPI(t, func(c call) (int, string) {
		return 409, `{"error":{"code":"ENVIRONMENT_IS_DEFAULT","message":"make another environment the default first"}}`
	})
	out, err := run(t, "env", "delete", "base", "--force")
	if err == nil {
		t.Fatal("want error")
	}
	_ = out
}

// ─── workspace scope ──────────────────────────────────────────────────────────

func TestEnvWorkspaceScope(t *testing.T) {
	api := newFakeAPI(t, func(c call) (int, string) {
		switch {
		case c.is("GET", "/workspaces"):
			return 200, `{"data":[{"id":"11111111-2222-3333-4444-555555555555","slug":"acme","name":"Acme"}]}`
		case c.is("GET", "/environments"):
			return 200, `{"environments":[` + envJSON + `],"default_environment_id":"e-base"}`
		case c.Method == "PUT":
			return 200, `{"success":true}`
		}
		return 404, `{}`
	})
	if _, err := run(t, "env", "list", "--workspace", "acme"); err != nil {
		t.Fatal(err)
	}
	if got := api.find("GET", "/environments"); len(got) != 1 || got[0].Query != "workspace_id=11111111-2222-3333-4444-555555555555" {
		t.Fatalf("list must carry the workspace id: %+v", got)
	}
	if _, err := runIn(t, "v\n", "env", "set-var", "--workspace", "acme", "K"); err != nil {
		t.Fatal(err)
	}
	put := api.find("PUT", "/environments/base/variables/K")
	if len(put) != 1 || put[0].Query != "workspace_id=11111111-2222-3333-4444-555555555555" {
		t.Fatalf("set-var must carry the workspace id: %+v", put)
	}
}

func TestEnvWorkspaceUUIDSkipsLookup(t *testing.T) {
	api := newFakeAPI(t, func(c call) (int, string) {
		return 200, `{"environments":[],"default_environment_id":""}`
	})
	id := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	if _, err := run(t, "env", "list", "--workspace", id); err != nil {
		t.Fatal(err)
	}
	if len(api.find("GET", "/workspaces")) != 0 {
		t.Error("a workspace id needs no lookup")
	}
	if got := api.find("GET", "/environments"); len(got) != 1 || got[0].Query != "workspace_id="+id {
		t.Fatalf("got %+v", got)
	}
}

func TestEnvWorkspaceUnknown(t *testing.T) {
	api := newFakeAPI(t, func(c call) (int, string) {
		if c.is("GET", "/workspaces") {
			return 200, `{"data":[{"id":"1","slug":"acme","name":"Acme"}]}`
		}
		return 500, `{}`
	})
	if _, err := run(t, "env", "list", "--workspace", "nope"); err == nil {
		t.Fatal("want error")
	}
	if len(api.find("GET", "/environments")) != 0 {
		t.Error("must not call environments for an unknown workspace")
	}
}

func TestEnvNoWorkspaceFlagSendsNoScope(t *testing.T) {
	api := envAPI(t)
	if _, err := run(t, "env", "list"); err != nil {
		t.Fatal(err)
	}
	if got := api.find("GET", "/environments"); got[0].Query != "" {
		t.Errorf("unscoped list must send no query: %q", got[0].Query)
	}
}
