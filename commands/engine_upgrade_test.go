package commands_test

import (
	"strings"
	"testing"
)

const upgradePlanJS = `{"data":{"from":"0.3.1","to":"0.3.2","git_sha":"c13e13da175b7614399989a45b2fd1c1ceae60b1","sha256":"25b33ca6","expected_migration":70,"release_channel":"beta","safe":true,"current_state":"running","estimated_downtime_seconds":90,"backup":{"id":"523e62ce-43a2-4bb2-9562-87f03f81ef87"}}}`

const upgradeResultJS = `{"data":{"action":"upgrade","from":"0.3.1","to":"0.3.2","state":"running","backup_id":"523e62ce-43a2-4bb2-9562-87f03f81ef87","rolled_back":false}}`

const rollbackResultJS = `{"data":{"action":"rollback","from":"0.3.2","to":"0.3.1","state":"running","backup_id":"523e62ce-43a2-4bb2-9562-87f03f81ef87","rolled_back":false}}`

func TestEngineUpgradeResolvesTheNameAndSendsTheVersion(t *testing.T) {
	f := fakeRoutes(t, map[string]any{
		"GET /databases": engineListJS,
		"POST /databases/" + engineID + "/upgrade": resp{200, upgradeResultJS},
	})

	out, err := run(t, "engine", "upgrade", "qa-brain", "--to", "0.3.2")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	for _, want := range []string{"qa-brain upgraded", "0.3.1 -> 0.3.2", "running"} {
		if !strings.Contains(out, want) {
			t.Fatalf("output is missing %q:\n%s", want, out)
		}
	}
	posts := f.find("POST", "/databases/"+engineID+"/upgrade")
	if len(posts) != 1 {
		t.Fatalf("posts = %v", f.find("POST", "/databases/"+engineID+"/upgrade"))
	}
	if posts[0].Body["version"] != "0.3.2" {
		t.Fatalf("body = %v", posts[0].Body)
	}
	if _, ok := posts[0].Body["force"]; ok {
		t.Fatalf("force must not be sent unless asked: %v", posts[0].Body)
	}
}

func TestEngineUpgradeDryRunAsksForThePlanOnly(t *testing.T) {
	f := fakeRoutes(t, map[string]any{
		"GET /databases": engineListJS,
		"POST /databases/" + engineID + "/upgrade": resp{200, upgradePlanJS},
	})

	out, err := run(t, "engine", "upgrade", "qa-brain", "--to", "0.3.2", "--dry-run")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	for _, want := range []string{"0.3.1 -> 0.3.2", "c13e13da", "migration 70", "safe to upgrade"} {
		if !strings.Contains(out, want) {
			t.Fatalf("output is missing %q:\n%s", want, out)
		}
	}
	if posts := f.find("POST", "/databases/"+engineID+"/upgrade"); len(posts) != 1 || posts[0].Body["dry_run"] != true {
		t.Fatalf("dry run body = %v", posts)
	}
}

func TestEngineUpgradeForceIsSentWhenAsked(t *testing.T) {
	f := fakeRoutes(t, map[string]any{
		"GET /databases": engineListJS,
		"POST /databases/" + engineID + "/upgrade": resp{200, upgradeResultJS},
	})

	if _, err := run(t, "engine", "upgrade", "qa-brain", "--to", "0.3.2", "--force"); err != nil {
		t.Fatalf("%v", err)
	}
	posts := f.find("POST", "/databases/"+engineID+"/upgrade")
	if len(posts) != 1 || posts[0].Body["force"] != true {
		t.Fatalf("force body = %v", posts)
	}
}

func TestEngineUpgradeReportsTheAPIRefusal(t *testing.T) {
	fakeRoutes(t, map[string]any{
		"GET /databases": engineListJS,
		"POST /databases/" + engineID + "/upgrade": resp{422, `{"error":{"code":"VERSION_NOT_STAGED","message":"that version is not staged on the fleet; see the engine-versions route"}}`},
	})

	out, err := run(t, "engine", "upgrade", "qa-brain", "--to", "0.3.3")
	if err == nil {
		t.Fatalf("an unstaged version must fail:\n%s", out)
	}
	if !strings.Contains(err.Error(), "not staged") {
		t.Fatalf("error = %v", err)
	}
}

func TestEngineRollbackReturnsToTheRecordedVersion(t *testing.T) {
	f := fakeRoutes(t, map[string]any{
		"GET /databases": engineListJS,
		"POST /databases/" + engineID + "/rollback": resp{200, rollbackResultJS},
	})

	out, err := run(t, "engine", "rollback", "qa-brain")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !strings.Contains(out, "qa-brain rolled back 0.3.2 -> 0.3.1") {
		t.Fatalf("output = %s", out)
	}
	if posts := f.find("POST", "/databases/"+engineID+"/rollback"); len(posts) != 1 {
		t.Fatalf("posts = %v", posts)
	}
}

func TestEngineUpgradeRefusesADatabaseThatIsNotAnEngine(t *testing.T) {
	fakeRoutes(t, map[string]any{
		"GET /databases/" + otherEngine: `{"id":"` + otherEngine + `","name":"app-db","engine":"postgresql","engine_version":"16","state":"running"}`,
	})

	out, err := run(t, "engine", "upgrade", otherEngine, "--to", "0.3.2")
	if err == nil {
		t.Fatalf("a postgres database must not be upgradeable as an Engine:\n%s", out)
	}
	if !strings.Contains(err.Error(), "not a managed Optimal Engine") {
		t.Fatalf("error = %v", err)
	}
}
