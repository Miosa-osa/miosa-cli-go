package commands_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	connectSecret = "oe_ck_connect_secret_value"
	connectKeyID  = "0190a1b2-0000-7000-8000-0000000000c1"
)

func clientKeyEnvelope() string {
	return `{"data":{"id":"` + connectKeyID + `","workspace_id":"` + attachWS + `","scopes":["read","write"],` +
		`"expires_at":"2026-10-21T12:00:00Z","key":"` + connectSecret + `"}}`
}

func connectRoutes(attached ...string) map[string]any {
	return map[string]any{
		"GET /workspaces": workspacesRoute(),
		"GET /databases":  `{"data":[` + engineJSON(attachEngA, "brain", "") + `]}`,
		"GET /databases/" + attachEngA + "/attachments":                    `{"data":[` + strings.Join(attached, ",") + `]}`,
		"POST /databases/" + attachEngA + "/client-keys":                   clientKeyEnvelope(),
		"DELETE /databases/" + attachEngA + "/client-keys/" + connectKeyID: `{"data":{}}`,
	}
}

func connectHome(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	return dir
}

func TestEngineConnectWritesAPrivateProfile(t *testing.T) {
	f := fakeRoutes(t, connectRoutes(attachmentBody(attachWS, attachEngA, "workspace")))
	home := connectHome(t)

	out, err := run(t, "engine", "connect", "brain")
	if err != nil {
		t.Fatalf("connect: %v (%s)", err, out)
	}
	if strings.Contains(out, connectSecret) {
		t.Fatalf("the key must never be printed: %q", out)
	}

	calls := f.find("POST", "/databases/"+attachEngA+"/client-keys")
	if len(calls) != 1 || calls[0].Body["workspace_id"] != attachWS || calls[0].Body["ttl_seconds"] != float64(86400) {
		t.Fatalf("mint call = %+v", calls)
	}

	path := filepath.Join(home, "optimal", "profile")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("profile mode = %v, want 0600", info.Mode().Perm())
	}
	body, _ := os.ReadFile(path)
	text := string(body)
	for _, want := range []string{
		"/engines/" + attachEngA + "/engine-api\n",
		"key=" + connectSecret + "\n",
		"workspace=kii\n",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("profile lacks %q:\n%s", want, text)
		}
	}
	if !strings.Contains(text, "url=http") {
		t.Fatalf("profile url: %s", text)
	}
}

func TestEngineConnectNamedProfileAndWorkspace(t *testing.T) {
	two := []string{
		attachmentBody(attachWS, attachEngA, "workspace"),
		strings.Replace(attachmentBody(attachWSOther, attachEngA, "workspace"), `"kii"`, `"peak"`, 1),
	}
	fakeRoutes(t, connectRoutes(two...))
	home := connectHome(t)

	if _, err := run(t, "engine", "connect", "brain"); err == nil {
		t.Fatal("two attached workspaces must need --workspace")
	}
	if _, err := run(t, "engine", "connect", "brain", "--workspace", "kii", "--profile", "kii"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(home, "optimal", "profiles", "kii")); err != nil {
		t.Fatalf("named profile: %v", err)
	}
}

func TestEngineConnectRefusesWhatItShould(t *testing.T) {
	f := fakeRoutes(t, connectRoutes(attachmentBody(attachWS, attachEngA, "workspace")))
	connectHome(t)

	for name, args := range map[string][]string{
		"ttl too long":    {"engine", "connect", "brain", "--ttl", "721h"},
		"ttl too short":   {"engine", "connect", "brain", "--ttl", "10s"},
		"profile as path": {"engine", "connect", "brain", "--profile", "../x"},
		"unattached ws":   {"engine", "connect", "brain", "--workspace", "peak"},
		"unknown engine":  {"engine", "connect", "nope"},
	} {
		if _, err := run(t, args...); err == nil {
			t.Errorf("%s: should fail", name)
		}
	}
	if calls := f.find("POST", "/databases/"+attachEngA+"/client-keys"); len(calls) != 0 {
		t.Fatalf("a refused connect must not mint: %+v", calls)
	}
}

func TestEngineConnectPrintWritesNothing(t *testing.T) {
	fakeRoutes(t, connectRoutes(attachmentBody(attachWS, attachEngA, "workspace")))
	home := connectHome(t)

	out, err := run(t, "engine", "connect", "brain", "--print")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"export OPTIMAL_ENGINE_URL='", "export OPTIMAL_ENGINE_API_KEY='" + connectSecret + "'", "export OPTIMAL_WORKSPACE='kii'"} {
		if !strings.Contains(out, want) {
			t.Fatalf("output lacks %q: %s", want, out)
		}
	}
	if _, err := os.Stat(filepath.Join(home, "optimal")); !os.IsNotExist(err) {
		t.Fatalf("--print must not write a profile: %v", err)
	}
}

func TestEngineConnectJSONNeverCarriesTheKey(t *testing.T) {
	fakeRoutes(t, connectRoutes(attachmentBody(attachWS, attachEngA, "workspace")))
	connectHome(t)

	out, err := run(t, "engine", "connect", "brain", "--json")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, connectSecret) || !strings.Contains(out, connectKeyID) {
		t.Fatalf("json output: %s", out)
	}
}

func TestEngineShellGivesTheChildTheKeyAndRevokesAfter(t *testing.T) {
	f := fakeRoutes(t, connectRoutes(attachmentBody(attachWS, attachEngA, "workspace")))
	dir := t.TempDir()
	seen := filepath.Join(dir, "env")

	_, err := run(t, "engine", "shell", "brain", "--",
		"sh", "-c", `printf '%s|%s' "$OPTIMAL_ENGINE_API_KEY" "$OPTIMAL_WORKSPACE" > `+seen)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(seen)
	if string(got) != connectSecret+"|kii" {
		t.Fatalf("child env = %q", got)
	}
	if calls := f.find("DELETE", "/databases/"+attachEngA+"/client-keys/"+connectKeyID); len(calls) != 1 {
		t.Fatalf("the key must be revoked after the child exits: %+v", f.calls)
	}
}

func TestEngineShellRevokesWhenTheChildFails(t *testing.T) {
	f := fakeRoutes(t, connectRoutes(attachmentBody(attachWS, attachEngA, "workspace")))

	if _, err := run(t, "engine", "shell", "brain", "--", "sh", "-c", "exit 3"); err == nil {
		t.Fatal("a failing child should fail the command")
	}
	if calls := f.find("DELETE", "/databases/"+attachEngA+"/client-keys/"+connectKeyID); len(calls) != 1 {
		t.Fatalf("the key must be revoked: %+v", f.calls)
	}
}
