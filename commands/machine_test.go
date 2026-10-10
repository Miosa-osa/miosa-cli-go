package commands_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Miosa-osa/miosa-cli-go/commands"
)

func sandboxJSON(extra string) string {
	return `{"id":"sb1","name":"my-box","state":"running","template_id":"miosa-sandbox","cpu_count":1,"memory_mb":1024,"created_at":"2026-01-01T00:00:00Z"` + extra + `}`
}

const behindEnv = `,"environment":"staging","environment_id":"e-stg","environment_version":1,"environment_latest_version":2,"environment_upgrade_available":true,"environment_status":"applied","setup_status":"failed","setup_error":"exit code 2\nboom"`
const currentEnv = `,"environment":"staging","environment_id":"e-stg","environment_version":2,"environment_latest_version":2,"environment_upgrade_available":false,"environment_status":"applied","setup_status":"done"`

// ─── create ───────────────────────────────────────────────────────────────────

func TestCreateSendsEnvironmentAndSetupFile(t *testing.T) {
	api := newFakeAPI(t, func(c call) (int, string) {
		return 201, sandboxJSON(behindEnv)
	})
	script := filepath.Join(t.TempDir(), "setup.sh")
	_ = os.WriteFile(script, []byte("#!/bin/bash\nnpm ci\n"), 0o600)

	out, err := run(t, "create", "my-box", "--env", "staging", "--setup-file", script)
	if err != nil {
		t.Fatal(err)
	}
	b := api.find("POST", "/sandboxes")[0].Body
	if b["environment"] != "staging" || b["setup_file"] != "#!/bin/bash\nnpm ci\n" {
		t.Fatalf("body: %v", b)
	}
	if _, set := b["no_env"]; set {
		t.Error("no_env must be omitted unless set")
	}
	if !strings.Contains(out, "staging") || !strings.Contains(out, "failed") {
		t.Errorf("create output should show env and setup:\n%s", out)
	}
}

func TestCreateNoEnv(t *testing.T) {
	api := newFakeAPI(t, func(c call) (int, string) { return 201, sandboxJSON("") })
	if _, err := run(t, "create", "my-box", "--no-env"); err != nil {
		t.Fatal(err)
	}
	b := api.find("POST", "/sandboxes")[0].Body
	if b["no_env"] != true {
		t.Fatalf("body: %v", b)
	}
	if _, set := b["environment"]; set {
		t.Error("environment must be omitted with --no-env")
	}
}

func TestCreateWithoutEnvFlagsSendsNone(t *testing.T) {
	api := newFakeAPI(t, func(c call) (int, string) { return 201, sandboxJSON("") })
	if _, err := run(t, "create", "my-box"); err != nil {
		t.Fatal(err)
	}
	b := api.find("POST", "/sandboxes")[0].Body
	for _, k := range []string{"environment", "no_env", "setup_file"} {
		if _, set := b[k]; set {
			t.Errorf("%s must be omitted by default: %v", k, b)
		}
	}
}

func TestCreateValidationMakesNoRequest(t *testing.T) {
	api := newFakeAPI(t, func(c call) (int, string) { return 201, sandboxJSON("") })
	dir := t.TempDir()
	big := filepath.Join(dir, "big.sh")
	_ = os.WriteFile(big, bytes.Repeat([]byte("a"), 64*1024+1), 0o600)
	exact := filepath.Join(dir, "exact.sh")
	_ = os.WriteFile(exact, bytes.Repeat([]byte("a"), 64*1024), 0o600)
	bin := filepath.Join(dir, "bin.sh")
	_ = os.WriteFile(bin, []byte{0xc3, 0x28}, 0o600)
	empty := filepath.Join(dir, "empty.sh")
	_ = os.WriteFile(empty, nil, 0o600)

	bad := map[string][]string{
		"env and no-env": {"create", "x", "--env", "staging", "--no-env"},
		"too big":        {"create", "x", "--setup-file", big},
		"not utf8":       {"create", "x", "--setup-file", bin},
		"empty":          {"create", "x", "--setup-file", empty},
		"missing":        {"create", "x", "--setup-file", filepath.Join(dir, "none.sh")},
	}
	for name, args := range bad {
		if _, err := run(t, args...); err == nil {
			t.Errorf("%s: want error", name)
		}
	}
	if api.count() != 0 {
		t.Fatalf("validation errors must not reach the API: %+v", api.calls)
	}
	// Exactly 64KB is allowed.
	if _, err := run(t, "create", "x", "--setup-file", exact); err != nil {
		t.Fatalf("64KB must be accepted: %v", err)
	}
}

func TestComputerCreate(t *testing.T) {
	api := newFakeAPI(t, func(c call) (int, string) {
		return 201, `{"id":"c1","name":"desk","status":"creating","size":"small","environment":"base","environment_version":3,"setup_status":"pending"}`
	})
	script := filepath.Join(t.TempDir(), "s.sh")
	_ = os.WriteFile(script, []byte("echo hi"), 0o600)
	out, err := run(t, "computer", "create", "desk", "--env", "base", "--setup-file", script)
	if err != nil {
		t.Fatal(err)
	}
	b := api.find("POST", "/computers")[0].Body
	if b["name"] != "desk" || b["environment"] != "base" || b["setup_file"] != "echo hi" {
		t.Fatalf("body: %v", b)
	}
	if !strings.Contains(out, "base v3") || !strings.Contains(out, "pending") {
		t.Errorf("output:\n%s", out)
	}
	if _, err := run(t, "computer", "create", "desk", "--env", "a", "--no-env"); err == nil {
		t.Error("--env with --no-env must fail")
	}
}

// ─── info / list ──────────────────────────────────────────────────────────────

func TestInfoShowsEnvAndSetup(t *testing.T) {
	newFakeAPI(t, func(c call) (int, string) { return 200, sandboxJSON(behindEnv) })
	out, err := run(t, "info", "sb1")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"staging v1", "upgrade available: v1 -> v2", "applied", "failed", "exit code 2", "boom"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q:\n%s", want, out)
		}
	}
}

func TestInfoUpToDateAndNoEnv(t *testing.T) {
	newFakeAPI(t, func(c call) (int, string) { return 200, sandboxJSON(currentEnv) })
	out, err := run(t, "status", "sb1")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "up to date") || strings.Contains(out, "upgrade available") {
		t.Errorf("output:\n%s", out)
	}
	newFakeAPI(t, func(c call) (int, string) { return 200, sandboxJSON("") })
	out, err = run(t, "info", "sb1")
	if err != nil || !strings.Contains(out, "none") {
		t.Errorf("a machine without an environment should say so: %v\n%s", err, out)
	}
}

func TestInfoJSON(t *testing.T) {
	newFakeAPI(t, func(c call) (int, string) { return 200, sandboxJSON(behindEnv) })
	out, err := run(t, "info", "sb1", "-o", "json")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"environment": "staging"`, `"environment_upgrade_available": true`, `"setup_status": "failed"`, `"setup_error"`} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %s:\n%s", want, out)
		}
	}
}

func TestInfoFallsBackToComputer(t *testing.T) {
	newFakeAPI(t, func(c call) (int, string) {
		if strings.HasPrefix(c.Path, "/sandboxes/") {
			return 404, `{"error":{"code":"NOT_FOUND","message":"nope"}}`
		}
		return 200, `{"id":"c1","name":"desk","status":"running","size":"small","environment":"base","environment_version":1}`
	})
	out, err := run(t, "info", "c1")
	if err != nil || !strings.Contains(out, "computer") || !strings.Contains(out, "base v1") {
		t.Fatalf("%v\n%s", err, out)
	}
}

func TestListShowsEnvColumn(t *testing.T) {
	newFakeAPI(t, func(c call) (int, string) {
		return 200, `{"data":[` + sandboxJSON(behindEnv) + `,` + strings.Replace(sandboxJSON(""), `"sb1"`, `"sb2"`, 1) + `],"meta":{"total":2,"page":1,"per_page":25}}`
	})
	out, err := run(t, "list")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"ENV", "staging v1 (upgrade)", "SETUP", "failed"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q:\n%s", want, out)
		}
	}
}

// ─── env upgrade ──────────────────────────────────────────────────────────────

func upgradeAPI(t *testing.T, sandbox string) *fakeAPI {
	return newFakeAPI(t, func(c call) (int, string) {
		switch {
		case c.Method == "GET" && strings.HasPrefix(c.Path, "/sandboxes/"):
			return 200, sandbox
		case c.is("POST", "/environments/e-stg/upgrade"), c.is("POST", "/environments/staging/upgrade"):
			return 200, `{"environment_id":"e-stg","latest_version":2,"machines":[{"kind":"sandbox","id":"sb1","from_version":1,"to_version":2,"status":"applying"}]}`
		}
		return 404, `{}`
	})
}

func TestEnvUpgradeMachine(t *testing.T) {
	api := upgradeAPI(t, sandboxJSON(behindEnv))
	out, err := run(t, "env", "upgrade", "sb1", "--force")
	if err != nil {
		t.Fatal(err)
	}
	up := api.find("POST", "/environments/e-stg/upgrade")
	if len(up) != 1 {
		t.Fatalf("calls: %+v", api.calls)
	}
	ids, _ := up[0].Body["machine_ids"].([]interface{})
	if len(ids) != 1 || ids[0] != "sb1" {
		t.Fatalf("machine_ids: %v", up[0].Body)
	}
	if !strings.Contains(out, "v1 -> v2") {
		t.Errorf("output:\n%s", out)
	}
}

func TestEnvUpgradeSkipsUpToDateMachine(t *testing.T) {
	api := upgradeAPI(t, sandboxJSON(currentEnv))
	out, err := run(t, "env", "upgrade", "sb1", "--force")
	if err != nil {
		t.Fatal(err)
	}
	if len(api.find("POST", "/environments/e-stg/upgrade")) != 0 {
		t.Error("an up-to-date machine must not trigger an upgrade")
	}
	if !strings.Contains(out, "already on") {
		t.Errorf("output:\n%s", out)
	}
}

func TestEnvUpgradeRequiresConfirmation(t *testing.T) {
	api := upgradeAPI(t, sandboxJSON(behindEnv))
	out, err := runIn(t, "n\n", "env", "upgrade", "sb1")
	if err == nil {
		t.Fatal("answering n must abort")
	}
	if !strings.Contains(out, "cannot be undone") {
		t.Errorf("prompt should warn about secret deletion:\n%s", out)
	}
	if len(api.find("POST", "/environments/e-stg/upgrade")) != 0 {
		t.Fatal("aborted upgrade must not call the API")
	}
	if _, err := runIn(t, "y\n", "env", "upgrade", "sb1"); err != nil {
		t.Fatal(err)
	}
	if len(api.find("POST", "/environments/e-stg/upgrade")) != 1 {
		t.Fatal("yes should upgrade")
	}
}

func TestEnvUpgradeAll(t *testing.T) {
	api := upgradeAPI(t, sandboxJSON(behindEnv))
	if _, err := run(t, "env", "upgrade", "--all", "staging", "--force"); err != nil {
		t.Fatal(err)
	}
	up := api.find("POST", "/environments/staging/upgrade")
	if len(up) != 1 || up[0].Body != nil {
		t.Fatalf("--all sends no machine_ids: %+v", up)
	}
	if _, err := run(t, "env", "upgrade", "sb1", "--all", "staging", "--force"); err == nil {
		t.Error("machines with --all must be rejected")
	}
}

func TestEnvUpgradeUsesCurrentSandbox(t *testing.T) {
	api := upgradeAPI(t, sandboxJSON(behindEnv))
	home := os.Getenv("HOME")
	writeTestConfig(t, home, api.srv.URL)
	if _, err := run(t, "env", "upgrade", "--force"); err != nil {
		t.Fatal(err)
	}
	// The current sandbox is a name or an id; the CLI resolves it by name
	// first, then fetches the sandbox it found.
	if len(api.find("GET", "/sandboxes/by-name/abc123")) != 1 || len(api.find("GET", "/sandboxes/sb1")) != 1 {
		t.Fatalf("should look up the current sandbox: %+v", api.calls)
	}
}

func TestEnvUpgradeMachineWithoutEnvironment(t *testing.T) {
	api := upgradeAPI(t, sandboxJSON(""))
	if _, err := run(t, "env", "upgrade", "sb1", "--force"); err == nil {
		t.Fatal("want error")
	}
	if len(api.find("POST", "/environments/e-stg/upgrade")) != 0 {
		t.Fatal("must not upgrade")
	}
}

func TestEnvUpgradeComputerFallback(t *testing.T) {
	api := newFakeAPI(t, func(c call) (int, string) {
		switch {
		case strings.HasPrefix(c.Path, "/sandboxes/"):
			return 404, `{"error":{"code":"NOT_FOUND","message":"nope"}}`
		case c.is("GET", "/computers/c1"):
			return 200, `{"id":"c1","name":"desk","environment":"staging","environment_id":"e-stg","environment_version":1,"environment_latest_version":2,"environment_upgrade_available":true}`
		case c.is("POST", "/environments/e-stg/upgrade"):
			return 200, `{"environment_id":"e-stg","latest_version":2,"machines":[{"kind":"computer","id":"c1","from_version":1,"to_version":2,"status":"pending_resume"}]}`
		}
		return 404, `{}`
	})
	out, err := run(t, "env", "upgrade", "c1", "--force")
	if err != nil {
		t.Fatal(err)
	}
	if len(api.find("POST", "/environments/e-stg/upgrade")) != 1 || !strings.Contains(out, "pending_resume") {
		t.Fatalf("calls %+v\n%s", api.calls, out)
	}
}

// ─── exec ─────────────────────────────────────────────────────────────────────

// shellExecAPI answers POST /sandboxes/sb1/exec by actually running the
// command in /bin/sh, so tests prove the composed shell line behaves.
func shellExecAPI(t *testing.T, onExec func(c call)) *fakeAPI {
	t.Helper()
	return newFakeAPI(t, func(c call) (int, string) {
		if c.is("POST", "/sandboxes/sb1/exec") {
			if onExec != nil {
				onExec(c)
			}
			cmd := exec.Command("/bin/sh", "-c", c.Body["command"].(string))
			var so, se bytes.Buffer
			cmd.Stdout, cmd.Stderr = &so, &se
			code := 0
			if err := cmd.Run(); err != nil {
				var ee *exec.ExitError
				if errors.As(err, &ee) {
					code = ee.ExitCode()
				} else {
					code = 127
				}
			}
			return 200, mustJSON(map[string]interface{}{"data": map[string]interface{}{"stdout": so.String(), "stderr": se.String(), "exit_code": code}})
		}
		return 404, `{}`
	})
}

func TestExecCwdAndTimeout(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "my repo"), 0o755); err != nil {
		t.Fatal(err)
	}
	var sent call
	shellExecAPI(t, func(c call) { sent = c })
	t.Setenv("HOME", home) // the fake API runs commands with this HOME

	// A single word after -- is a shell string, so $(pwd) expands remotely.
	out, err := run(t, "exec", "sb1", "--cwd", "my repo", "--timeout", "120", "--", `basename "$(pwd)"`)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(out) != "my repo" {
		t.Errorf("cwd not applied, got %q", out)
	}
	if sent.Body["timeout"] != float64(120) {
		t.Errorf("timeout not sent: %v", sent.Body)
	}
}

func TestExecWithoutFlagsSendsNoTimeout(t *testing.T) {
	var sent call
	shellExecAPI(t, func(c call) { sent = c })
	if _, err := run(t, "exec", "sb1", "--", "echo", "hello"); err != nil {
		t.Fatal(err)
	}
	if _, set := sent.Body["timeout"]; set {
		t.Errorf("timeout must be omitted unless passed: %v", sent.Body)
	}
	if sent.Body["command"] != "echo hello" {
		t.Errorf("command: %v", sent.Body["command"])
	}
}

func TestExecTimeoutBounds(t *testing.T) {
	api := shellExecAPI(t, nil)
	for _, v := range []string{"-1", "301", "1000"} {
		if _, err := run(t, "exec", "sb1", "--timeout", v, "--", "true"); err == nil {
			t.Errorf("--timeout %s should fail", v)
		}
	}
	if api.count() != 0 {
		t.Errorf("must not reach the API: %+v", api.calls)
	}
	for _, v := range []string{"1", "300"} {
		if _, err := run(t, "exec", "sb1", "--timeout", v, "--", "true"); err != nil {
			t.Errorf("--timeout %s: %v", v, err)
		}
	}
	var sent call
	shellExecAPI(t, func(c call) { sent = c })
	if _, err := run(t, "exec", "sb1", "--timeout", "0", "--", "true"); err != nil {
		t.Fatal(err)
	}
	if sent.Body["timeout"] != float64(300) {
		t.Errorf("--timeout 0 means no timeout, the 300s maximum: %v", sent.Body)
	}
}

func TestExecPreservesArgvForMultiWordCommands(t *testing.T) {
	shellExecAPI(t, nil)
	out, err := run(t, "exec", "sb1", "--", "sh", "-c", "for i in 1 2 3; do echo $i; done")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Fields(out)[0] != "1" || len(strings.Fields(out)) != 3 {
		t.Errorf("argv must survive quoting, got %q", out)
	}
}

func TestExecExitCodeAndStderr(t *testing.T) {
	shellExecAPI(t, nil)
	out, err := run(t, "exec", "sb1", "--", "sh -c 'echo oops >&2; exit 7'")
	var ee *commands.ExitError
	if !errors.As(err, &ee) || ee.Code != 7 {
		t.Fatalf("want ExitError{7}, got %v", err)
	}
	if !strings.Contains(out, "oops") {
		t.Errorf("stderr should be printed:\n%s", out)
	}
}

func TestExecRetriesWhileStarting(t *testing.T) {
	calls := 0
	newFakeAPI(t, func(c call) (int, string) {
		switch {
		case c.Method == "GET":
			return 200, `{"id":"sb1","state":"starting"}`
		case c.is("POST", "/sandboxes/sb1/exec"):
			calls++
			if calls < 2 {
				return 409, `{"error":{"code":"SANDBOX_NOT_RUNNING","message":"sandbox must be in running state to exec"}}`
			}
			return 200, `{"data":{"stdout":"ready\n","exit_code":0}}`
		}
		return 404, `{}`
	})
	out, err := run(t, "exec", "sb1", "--", "echo", "ready")
	if err != nil {
		t.Fatal(err)
	}
	if calls != 2 || !strings.Contains(out, "ready") {
		t.Fatalf("calls=%d out=%q", calls, out)
	}
}

func TestExecDoesNotRetryOtherErrors(t *testing.T) {
	calls := 0
	newFakeAPI(t, func(c call) (int, string) {
		calls++
		return 422, `{"error":{"code":"INVALID","message":"bad"}}`
	})
	if _, err := run(t, "exec", "sb1", "--", "true"); err == nil {
		t.Fatal("want error")
	}
	if calls != 1 {
		t.Fatalf("a validation error must not be retried, calls=%d", calls)
	}
}

// ─── ssh ──────────────────────────────────────────────────────────────────────

func TestSSHPipesScriptToBashS(t *testing.T) {
	shellExecAPI(t, nil)
	script := "echo from-script\nX=$((20+22))\necho \"answer=$X\"\nread line || true\n"
	out, err := runIn(t, script, "ssh", "sb1", "--", "bash", "-s")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "from-script") || !strings.Contains(out, "answer=42") {
		t.Errorf("the piped script did not run:\n%s", out)
	}
}

func TestSSHNoCommandDefaultsToBashS(t *testing.T) {
	var sent call
	shellExecAPI(t, func(c call) { sent = c })
	out, err := runIn(t, "echo default-bash\n", "ssh", "sb1")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "default-bash") || !strings.Contains(sent.Body["command"].(string), "bash -s") {
		t.Errorf("out=%q cmd=%v", out, sent.Body["command"])
	}
}

func TestSSHWithoutDashTakesSandboxThenCommand(t *testing.T) {
	var sent call
	shellExecAPI(t, func(c call) { sent = c })
	out, err := run(t, "ssh", "sb1", "echo plain-form")
	if err != nil || !strings.Contains(out, "plain-form") {
		t.Fatalf("%v %q", err, out)
	}
	if sent.Body["command"] != "echo plain-form" {
		t.Errorf("command: %v", sent.Body["command"])
	}
	// A bare sandbox name and no stdin has nothing to run.
	if _, err := runIn(t, "", "ssh", "sb1"); err != nil {
		// empty piped stdin still runs 'bash -s' with an empty script: fine
		t.Logf("empty stdin run: %v", err)
	}
}

func TestSSHStdinIsByteExact(t *testing.T) {
	shellExecAPI(t, nil)
	// Quotes, a backslash, a dollar sign, a NUL-free binary-ish payload and no trailing newline.
	payload := "it's \"quoted\" $HOME \\n `x` ; | & \xe2\x9c\x93 end"
	out, err := runIn(t, payload, "ssh", "sb1", "--", "cat")
	if err != nil {
		t.Fatal(err)
	}
	if out != payload {
		t.Errorf("stdin was altered:\n got %q\nwant %q", out, payload)
	}
}

func TestSSHCommandWithCwdAndPipe(t *testing.T) {
	home := t.TempDir()
	_ = os.MkdirAll(filepath.Join(home, "proj"), 0o755)
	shellExecAPI(t, nil)
	t.Setenv("HOME", home)
	out, err := runIn(t, "basename \"$(pwd)\"\n", "ssh", "sb1", "--cwd", "proj", "--", "bash", "-s")
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(out) != "proj" {
		t.Errorf("got %q", out)
	}
}

func TestSSHPropagatesExitCode(t *testing.T) {
	shellExecAPI(t, nil)
	_, err := runIn(t, "exit 3\n", "ssh", "sb1", "--", "bash", "-s")
	var ee *commands.ExitError
	if !errors.As(err, &ee) || ee.Code != 3 {
		t.Fatalf("want ExitError{3}, got %v", err)
	}
}

func TestSSHArgvQuotingKeepsBashLCIntact(t *testing.T) {
	shellExecAPI(t, nil)
	out, err := runIn(t, "", "ssh", "sb1", "--", "bash", "-lc", "cd / && echo ok-$((1+1))")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "ok-2") {
		t.Errorf("got %q", out)
	}
}

func TestSSHRejectsOversizedStdin(t *testing.T) {
	api := shellExecAPI(t, nil)
	_, err := runIn(t, strings.Repeat("a", 81*1024), "ssh", "sb1", "--", "bash", "-s")
	if err == nil {
		t.Fatal("want error for stdin over 80KB")
	}
	if api.count() != 0 {
		t.Fatal("must not call the API")
	}
	// A real 64KB setup script (the documented maximum) fits.
	script := strings.Repeat("# pad\n", 64*1024/6-1) + "echo big-ok\n"
	out, err := runIn(t, script, "ssh", "sb1", "--", "bash", "-s")
	if err != nil || !strings.Contains(out, "big-ok") {
		t.Fatalf("a 64KB script must run: %v", err)
	}
}

func mustJSON(v interface{}) string {
	b, _ := json.Marshal(v)
	return string(b)
}
