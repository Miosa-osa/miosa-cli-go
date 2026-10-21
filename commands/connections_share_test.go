package commands_test

import (
	"strings"
	"testing"

	"github.com/Miosa-osa/miosa-cli-go/commands"
)

const (
	wsModels = "11111111-1111-1111-1111-111111111111"
	wsTeam   = "22222222-2222-2222-2222-222222222222"
	wsOps    = "33333333-3333-3333-3333-333333333333"
)

// shareAPI is a fake API with three workspaces; only `models` holds openrouter.
func shareAPI(t *testing.T, holders ...string) *fakeAPI {
	held := map[string]bool{}
	for _, h := range holders {
		held[h] = true
	}
	return newFakeAPI(t, func(c call) (int, string) {
		switch {
		case c.is("GET", "/workspaces"):
			return 200, `{"data":[{"id":"` + wsModels + `","slug":"models","name":"Models"},{"id":"` + wsTeam + `","slug":"team","name":"Team"},{"id":"` + wsOps + `","slug":"ops","name":"Ops"}]}`
		case c.is("GET", "/agents/credentials"):
			ws := strings.TrimPrefix(c.Query, "workspace_id=")
			if held[ws] {
				return 200, `{"data":[{"id":"openrouter","connected":true,"inherited":false,"scope":"workspace"},{"id":"anthropic","connected":false}]}`
			}
			return 200, `{"data":[{"id":"openrouter","connected":false}]}`
		case c.Method == "POST" && c.Path == "/agents/credentials/openrouter/shares":
			return 201, `{"data":[{"credential":"openrouter","target_workspace_id":"` + wsTeam + `"}],"total":1}`
		case c.Method == "DELETE" && c.Path == "/agents/credentials/openrouter/shares":
			return 200, `{"data":{"revoked":1}}`
		}
		return 404, `{}`
	})
}

func TestConnectionsShareFindsTheHoldingWorkspace(t *testing.T) {
	f := shareAPI(t, wsModels)
	out, err := run(t, "connections", "share", "openrouter", "--workspace", "team,ops")
	if err != nil || !strings.Contains(out, "Shared openrouter with 2 workspace(s)") {
		t.Fatalf("share: %v\n%s", err, out)
	}
	post := f.find("POST", "/agents/credentials/openrouter/shares")
	if len(post) != 1 || post[0].Body["workspace_id"] != wsModels {
		t.Fatalf("source = %+v", post)
	}
	ws, _ := post[0].Body["workspaces"].([]interface{})
	if len(ws) != 2 || ws[0] != wsTeam || ws[1] != wsOps {
		t.Fatalf("targets = %v", post[0].Body["workspaces"])
	}
}

func TestConnectionsShareAcceptsTheListRefAndFrom(t *testing.T) {
	f := shareAPI(t, wsModels, wsOps)
	// Two holders: --from is required.
	if _, err := run(t, "connections", "share", "agent_credential:openrouter", "--workspace", "team"); err == nil || !strings.Contains(err.Error(), "--from") {
		t.Fatalf("ambiguous holder: %v", err)
	}
	if _, err := run(t, "connections", "share", "agent_credential:openrouter", "--workspace", "team", "--from", "models"); err != nil {
		t.Fatal(err)
	}
	post := f.find("POST", "/agents/credentials/openrouter/shares")
	if len(post) != 1 || post[0].Body["workspace_id"] != wsModels {
		t.Fatalf("source = %+v", post)
	}
}

func TestConnectionsShareAllWorkspacesAndUnshare(t *testing.T) {
	f := shareAPI(t, wsModels)
	if _, err := run(t, "connections", "share", "openrouter", "--all-workspaces"); err != nil {
		t.Fatal(err)
	}
	post := f.find("POST", "/agents/credentials/openrouter/shares")
	if len(post) != 1 || post[0].Body["all_workspaces"] != true {
		t.Fatalf("all = %+v", post)
	}
	out, err := run(t, "connections", "unshare", "openrouter", "--workspace", "team")
	if err != nil || !strings.Contains(out, "Stopped sharing openrouter") {
		t.Fatalf("unshare: %v\n%s", err, out)
	}
	del := f.find("DELETE", "/agents/credentials/openrouter/shares")
	if len(del) != 1 || !strings.Contains(del[0].Query, "workspace_id="+wsModels) || !strings.Contains(del[0].Query, "workspaces="+wsTeam) {
		t.Fatalf("unshare query = %+v", del)
	}
}

func TestConnectionsShareRefusesWhatCannotWork(t *testing.T) {
	shareAPI(t) // nobody holds it
	if _, err := run(t, "connections", "share", "openrouter", "--workspace", "team"); err == nil || !strings.Contains(err.Error(), "nothing to share") {
		t.Fatalf("no holder: %v", err)
	}
	if _, err := run(t, "connections", "share", "openrouter"); err == nil || commands.ExitCode(err) != commands.ExitUsage {
		t.Fatalf("no target is a usage error: %v", err)
	}
	if _, err := run(t, "connections", "share", "openrouter", "--workspace", "team", "--all-workspaces"); err == nil || commands.ExitCode(err) != commands.ExitUsage {
		t.Fatalf("both targets is a usage error: %v", err)
	}
}
