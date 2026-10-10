package commands_test

import (
	"strings"
	"testing"

	"github.com/Miosa-osa/miosa-cli-go/commands"
)

const boxID = "8ba07e95-5085-4600-b758-60b6951c5786"

const execOK = `{"data":{"stdout":"hi\n","stderr":"","exit_code":0}}`

func TestSandboxNameIsResolvedToItsID(t *testing.T) {
	f := fakeRoutes(t, map[string]any{
		"GET /sandboxes/by-name/my-box":      `{"id":"` + boxID + `","name":"my-box"}`,
		"POST /sandboxes/" + boxID + "/exec": execOK,
	})
	out, err := run(t, "exec", "my-box", "--", "echo", "hi")
	if err != nil {
		t.Fatalf("exec by name: %v\n%s", err, out)
	}
	if len(f.find("GET", "/sandboxes/by-name/my-box")) != 1 || len(f.find("POST", "/sandboxes/"+boxID+"/exec")) != 1 {
		t.Fatalf("calls = %+v", f.calls)
	}
}

func TestNameLookupIsCachedPerRun(t *testing.T) {
	f := fakeRoutes(t, map[string]any{
		"GET /sandboxes/by-name/my-box": `{"data":{"id":"` + boxID + `"}}`, // enveloped shape works too
		"GET /sandboxes/" + boxID:       `{"id":"` + boxID + `","name":"my-box","state":"running","size":"small","template_id":"miosa-sandbox"}`,
	})
	if _, err := run(t, "info", "my-box"); err != nil {
		t.Fatal(err)
	}
	if n := len(f.find("GET", "/sandboxes/by-name/my-box")); n != 1 {
		t.Fatalf("by-name called %d times", n)
	}
}

func TestUUIDSkipsTheNameLookup(t *testing.T) {
	f := fakeRoutes(t, map[string]any{"POST /sandboxes/" + boxID + "/exec": execOK})
	if _, err := run(t, "exec", boxID, "--", "echo", "hi"); err != nil {
		t.Fatal(err)
	}
	if len(f.find("GET", "/sandboxes/by-name/"+boxID)) != 0 {
		t.Fatal("a UUID must not be looked up by name")
	}
}

func TestCurrentAliasUsesTheConfiguredSandbox(t *testing.T) {
	f := fakeRoutes(t, map[string]any{
		"GET /sandboxes/by-name/my-box":      `{"id":"` + boxID + `"}`,
		"POST /sandboxes/" + boxID + "/exec": execOK,
	})
	writeConfigFile(t, "api_key = \"msk_u_test\"\ncurrent_sandbox = \"my-box\"\n")
	for _, ref := range []string{"current"} {
		if _, err := run(t, "exec", ref, "--", "echo", "hi"); err != nil {
			t.Fatalf("%s: %v", ref, err)
		}
	}
	if len(f.find("POST", "/sandboxes/"+boxID+"/exec")) != 1 {
		t.Fatalf("calls = %+v", f.calls)
	}
}

func TestSelfAliasUsesTheEnvironment(t *testing.T) {
	f := fakeRoutes(t, map[string]any{"POST /sandboxes/" + boxID + "/exec": execOK})
	t.Setenv("MIOSA_SANDBOX_ID", boxID)
	if _, err := run(t, "exec", "self", "--", "echo", "hi"); err != nil {
		t.Fatal(err)
	}
	if len(f.find("POST", "/sandboxes/"+boxID+"/exec")) != 1 {
		t.Fatalf("calls = %+v", f.calls)
	}
}

func TestUnknownNameGetsAnActionableError(t *testing.T) {
	fakeRoutes(t, map[string]any{
		"POST /sandboxes/nope/exec": resp{400, `{"error":{"code":"INVALID_ID"}}`},
	})
	_, err := run(t, "exec", "nope", "--", "echo", "hi")
	if err == nil || !strings.Contains(err.Error(), "no sandbox has that name") {
		t.Fatalf("err = %v", err)
	}
	if commands.ExitCode(err) != commands.ExitConflict {
		t.Fatalf("exit = %d", commands.ExitCode(err))
	}
}
