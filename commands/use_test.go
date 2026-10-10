package commands_test

import (
	"strings"
	"testing"

	"github.com/Miosa-osa/miosa-cli-go/commands"
)

func TestUseResolvesAndStoresTheName(t *testing.T) {
	f := fakeRoutes(t, map[string]any{"GET /sandboxes/by-name/my-box": `{"id":"` + boxID + `"}`})
	out, err := run(t, "use", "my-box")
	if err != nil || !strings.Contains(out, `"my-box"`) {
		t.Fatalf("%v %s", err, out)
	}
	if !strings.Contains(readConfig(t), `current_sandbox = "my-box"`) {
		t.Fatal("name not stored")
	}
	if len(f.find("GET", "/sandboxes/by-name/my-box")) != 1 {
		t.Fatal("the sandbox must be checked")
	}
}

func TestUseRejectsAnUnknownName(t *testing.T) {
	fakeRoutes(t, map[string]any{})
	_, err := run(t, "use", "typo")
	if err == nil || !strings.Contains(err.Error(), `no sandbox named "typo"`) || commands.ExitCode(err) != commands.ExitNotFound {
		t.Fatalf("err = %v", err)
	}
}

func TestUseShowsAndClears(t *testing.T) {
	fakeRoutes(t, nil)
	writeConfigFile(t, "api_key = \"msk_u_test\"\ncurrent_sandbox = \"box\"\n")
	out, err := run(t, "use")
	if err != nil || strings.TrimSpace(out) != "box" {
		t.Fatalf("%v %q", err, out)
	}
	if _, err := run(t, "use", "--clear"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(readConfig(t), `current_sandbox = ""`) {
		t.Fatal("not cleared")
	}
	if out, _ := run(t, "use"); !strings.Contains(out, "No current sandbox") {
		t.Fatalf("out = %q", out)
	}
}

func TestUseCheckUUIDExists(t *testing.T) {
	fakeRoutes(t, map[string]any{"GET /sandboxes/" + boxID: `{"id":"` + boxID + `","name":"x","state":"running"}`})
	if _, err := run(t, "use", boxID); err != nil {
		t.Fatal(err)
	}
	if _, err := run(t, "use", "11111111-1111-1111-1111-111111111111"); err == nil {
		t.Fatal("an id that does not exist must be rejected")
	}
}
