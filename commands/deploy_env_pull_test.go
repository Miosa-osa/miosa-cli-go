package commands_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const revealJSON = `{"data":{"deployment_id":"` + depID + `","environment":"production","vars":[
  {"name":"DATABASE_URL","value":"postgres://u:p@db/app"},
  {"name":"API_TOKEN","value":"tok \"live\"\nline2"}]}}`

func envPullAPI(t *testing.T, status int, body string) *fakeAPI {
	t.Helper()
	routes := guardRoutes("")
	return newFakeAPI(t, func(c call) (int, string) {
		if c.is("GET", "/deployments/"+depID+"/env/reveal") {
			return status, body
		}
		return routeReply(routes)(c)
	})
}

func TestEnvPullWritesA0600FileAndPrintsNamesOnly(t *testing.T) {
	envPullAPI(t, 200, revealJSON)
	file := filepath.Join(t.TempDir(), ".env.local")

	out, err := run(t, "deploy", "env-pull", "my-app", "--file", file)
	if err != nil {
		t.Fatalf("env-pull: %v\n%s", err, out)
	}
	if strings.Contains(out, "tok") || strings.Contains(out, "postgres://") {
		t.Fatalf("a value was printed:\n%s", out)
	}
	if !strings.Contains(out, "API_TOKEN, DATABASE_URL") {
		t.Fatalf("names missing:\n%s", out)
	}
	info, err := os.Stat(file)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("file mode = %v (%v), want 0600", info.Mode().Perm(), err)
	}
	got, _ := os.ReadFile(file)
	for _, want := range []string{
		`API_TOKEN="tok \"live\"\nline2"`,
		`DATABASE_URL="postgres://u:p@db/app"`,
		"do not commit",
	} {
		if !strings.Contains(string(got), want) {
			t.Errorf("file missing %q:\n%s", want, got)
		}
	}

	jout, err := run(t, "deploy", "env-pull", "my-app", "--file", file, "--force", "--json")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(jout, "tok") || strings.Contains(jout, "postgres://") {
		t.Fatalf("--json printed a value:\n%s", jout)
	}
	var data map[string]any
	if err := json.Unmarshal(dataJSON(jout), &data); err != nil || data["environment"] != "production" {
		t.Fatalf("json = %s (%v)", jout, err)
	}
}

func TestEnvPullKeepsAnExistingFileWithoutForce(t *testing.T) {
	f := envPullAPI(t, 200, revealJSON)
	file := filepath.Join(t.TempDir(), ".env.local")
	if err := os.WriteFile(file, []byte("MINE=1\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := run(t, "deploy", "env-pull", "my-app", "--file", file); err == nil || !strings.Contains(err.Error(), "--force") {
		t.Fatalf("must refuse to overwrite: %v", err)
	}
	if got, _ := os.ReadFile(file); string(got) != "MINE=1\n" {
		t.Fatalf("file changed: %q", got)
	}
	if n := len(f.find("GET", "/deployments/"+depID+"/env/reveal")); n != 0 {
		t.Fatalf("refused before asking, but the server was asked %d time(s)", n)
	}

	if _, err := run(t, "deploy", "env-pull", "my-app", "--file", file, "--force"); err != nil {
		t.Fatal(err)
	}
	if info, _ := os.Stat(file); info.Mode().Perm() != 0o600 {
		t.Fatalf("an overwritten 0644 file must end up 0600, got %v", info.Mode().Perm())
	}
}

func TestEnvPullRefusedWritesNothing(t *testing.T) {
	envPullAPI(t, 403, `{"error":{"code":"OWNER_OR_ADMIN_REQUIRED","message":"Only an owner or admin of this organization can reveal environment variable values."}}`)
	file := filepath.Join(t.TempDir(), ".env.local")

	_, err := run(t, "deploy", "env-pull", "my-app", "--file", file)
	if err == nil || !strings.Contains(err.Error(), "owner or admin") {
		t.Fatalf("err = %v", err)
	}
	if _, statErr := os.Stat(file); !os.IsNotExist(statErr) {
		t.Fatal("a refused pull must not create the file")
	}
}

func TestEnvPullOnAServerWithoutRevealSaysSo(t *testing.T) {
	envPullAPI(t, 404, `{"error":{"code":"NOT_FOUND","message":"Not Found"}}`)
	file := filepath.Join(t.TempDir(), ".env.local")
	_, err := run(t, "deploy", "env-pull", "my-app", "--file", file)
	if err == nil || !strings.Contains(err.Error(), "does not support env-pull yet") {
		t.Fatalf("err = %v", err)
	}
	if _, statErr := os.Stat(file); !os.IsNotExist(statErr) {
		t.Fatal("nothing may be written")
	}
}
