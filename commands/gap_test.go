package commands_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Miosa-osa/miosa-cli-go/commands"
)

func TestRunOneShotRemovesTheSandboxAndKeepsTheExitCode(t *testing.T) {
	f := fakeRoutes(t, map[string]any{
		"POST /sandboxes/run":        resp{201, `{"data":{"id":"` + boxID + `","name":"oneshot-1"},"exec":{"stdout":"42\n","stderr":"warn\n","exit_code":3}}`},
		"DELETE /sandboxes/" + boxID: resp{204, ``},
	})
	out, errOut, err := runSplit(t, "run", "--rm", "--size", "small", "--set", "A=1", "--", "python", "-c", "print(6*7)")
	if out != "42\n" || !strings.Contains(errOut, "warn") {
		t.Fatalf("out=%q err=%q", out, errOut)
	}
	if err == nil || commands.ExitCode(err) != 3 {
		t.Fatalf("the command's exit code must be miosa's: %v (%d)", err, commands.ExitCode(err))
	}
	b := f.find("POST", "/sandboxes/run")[0].Body
	if b["persistent"] != false || b["size"] != "small" || b["env"].(map[string]any)["A"] != "1" || !strings.Contains(b["command"].(string), "python") {
		t.Fatalf("body = %v", b)
	}
	if len(f.find("DELETE", "/sandboxes/"+boxID)) != 1 {
		t.Fatalf("--rm must destroy the sandbox: %+v", f.calls)
	}
}

func TestRunOneShotWithoutRmKeepsTheSandbox(t *testing.T) {
	f := fakeRoutes(t, map[string]any{
		"POST /sandboxes/run": resp{201, `{"data":{"id":"` + boxID + `","name":"oneshot-1"},"exec":{"stdout":"ok\n","exit_code":0}}`},
	})
	_, errOut, err := runSplit(t, "run", "--", "true")
	if err != nil || !strings.Contains(errOut, "kept sandbox oneshot-1") {
		t.Fatalf("err=%v %q", err, errOut)
	}
	if f.find("POST", "/sandboxes/run")[0].Body["persistent"] != true || len(f.find("DELETE", "/sandboxes/"+boxID)) != 0 {
		t.Fatalf("calls: %+v", f.calls)
	}
}

func TestBatchNewAndWatch(t *testing.T) {
	polls := 0
	f := fakeRoutes(t, map[string]any{
		"POST /sandboxes/batch": resp{202, `{"data":{"id":"b1","status":"queued","requested_count":3}}`},
	})
	if out, err := run(t, "batch", "new", "--count", "3", "--size", "small", "--name-prefix", "ci", "--ttl", "2h", "--tag", "team=infra"); err != nil || !strings.Contains(out, "batch b1") {
		t.Fatalf("%v\n%s", err, out)
	}
	b := f.find("POST", "/sandboxes/batch")[0].Body
	if b["count"] != float64(3) || b["name_prefix"] != "ci" || b["timeout_sec"] != float64(7200) || b["tags"].(map[string]any)["team"] != "infra" {
		t.Fatalf("body = %v", b)
	}
	if _, err := run(t, "batch", "new"); err == nil {
		t.Fatal("--count is required")
	}
	_ = polls
}

func TestBatchWatchSettlesAndReportsFailures(t *testing.T) {
	defer commands.SetPollIntervalForTest(1)()
	fakeRoutes(t, map[string]any{
		"GET /sandboxes/batches/b1": `{"data":{"id":"b1","status":"completed","requested":3,"pending":0,"live_count":2,"failed_total":1,"settled?":true}}`,
	})
	_, err := run(t, "batch", "watch", "b1")
	if err == nil || !strings.Contains(err.Error(), "could not create 1 sandbox(es)") {
		t.Fatalf("err = %v", err)
	}
	fakeRoutes(t, map[string]any{
		"GET /sandboxes/batches/b2": `{"data":{"id":"b2","status":"completed","requested":2,"pending":0,"live_count":2,"failed_total":0,"settled?":true}}`,
	})
	if out, err := run(t, "batch", "watch", "b2"); err != nil || !strings.Contains(out, "2 of 2 up") {
		t.Fatalf("%v\n%s", err, out)
	}
}

func TestServiceAccountKeyMintsOnTheAccount(t *testing.T) {
	f := fakeRoutes(t, map[string]any{
		"POST /service-accounts/sa1/keys": resp{201, `{"data":{"id":"k1","name":"deploy-pipeline key","key":"msk_u_SECRET","scopes":["a"]}}`},
	})
	out, err := run(t, "service-account", "key", "new", "sa1", "--preset", "ci", "--expires-in", "90")
	if err != nil || !strings.Contains(out, "msk_u_SECRET") {
		t.Fatalf("%v\n%s", err, out)
	}
	b := f.find("POST", "/service-accounts/sa1/keys")[0].Body
	got, _ := json.Marshal(b["scopes"])
	if !strings.Contains(string(got), "sandboxes:exec") || b["expires_in_days"] != float64(90) || b["key_type"] != nil {
		t.Fatalf("body = %v", b)
	}
	if q, _ := run(t, "service-account", "key", "new", "sa1", "--quiet-key"); !strings.HasPrefix(q, "msk_u_SECRET") {
		t.Fatalf("--quiet-key prints only the secret: %q", q)
	}
}

const forgeRepoID = "5d2a8b1c-7e4f-4a3b-9c60-1f2e3d4c5b6a"

func TestForgePullRequestCommands(t *testing.T) {
	f := fakeRoutes(t, map[string]any{
		"GET /forge/repositories":                                                    `{"data":[{"id":"` + forgeRepoID + `","name":"Platform","slug":"platform"}]}`,
		"GET /forge/repositories/" + forgeRepoID + "/pull-requests":                  `{"data":[{"number":12,"state":"open","title":"Fix redirect","head_branch":"fix-redirect","base_branch":"main","author":{"name":"Ada"}}]}`,
		"POST /forge/repositories/" + forgeRepoID + "/pull-requests":                 resp{201, `{"data":{"number":13,"title":"Fix redirect","state":"open"}}`},
		"GET /forge/repositories/" + forgeRepoID + "/pull-requests/12/collaboration": `{"data":{"mergeable":false,"reviews":[]}}`,
		"POST /forge/repositories/" + forgeRepoID + "/pull-requests/12/reviews":      resp{201, `{"data":{"decision":"approved"}}`},
		"POST /forge/repositories/" + forgeRepoID + "/pull-requests/12/check-runs":   resp{201, `{"data":{"name":"unit-tests"}}`},
		"POST /forge/repositories/" + forgeRepoID + "/pull-requests/12/merge":        `{"data":{"merged":true}}`,
		"POST /forge/repositories/" + forgeRepoID + "/pull-requests/12/close":        `{"data":{"number":12,"state":"closed"}}`,
		"POST /forge/repositories/" + forgeRepoID + "/pull-requests/12/comments":     resp{201, `{"data":{"id":"c1"}}`},
		"POST /forge/repositories/" + forgeRepoID + "/branch-policies":               resp{201, `{"data":{"version":2}}`},
		"POST /forge/repositories/" + forgeRepoID + "/releases":                      resp{201, `{"data":{"tag_name":"v1.2.0"}}`},
	})
	base := "/forge/repositories/" + forgeRepoID

	// A repository is addressed by slug; the id goes on the wire.
	if out, err := run(t, "forge", "pr", "list", "platform"); err != nil || !strings.Contains(out, "Fix redirect") {
		t.Fatalf("%v\n%s", err, out)
	}
	if _, err := run(t, "forge", "pr", "new", "platform", "--head", "fix-redirect", "--title", "Fix redirect", "--draft"); err != nil {
		t.Fatal(err)
	}
	call := f.find("POST", base+"/pull-requests")[0]
	if call.Body["head_branch"] != "fix-redirect" || call.Body["title"] != "Fix redirect" || call.Body["draft"] != true {
		t.Fatalf("body = %v", call.Body)
	}
	if !strings.HasPrefix(call.Headers.Get("Idempotency-Key"), "cli-") {
		t.Fatalf("the create must carry an Idempotency-Key, got %q", call.Headers.Get("Idempotency-Key"))
	}
	if _, err := run(t, "forge", "pr", "new", "platform", "--head", "a", "--title", "t", "--idempotency-key", "mine-1"); err != nil {
		t.Fatal(err)
	}
	if got := f.find("POST", base+"/pull-requests")[1].Headers.Get("Idempotency-Key"); got != "mine-1" {
		t.Fatalf("an explicit key must be used: %q", got)
	}

	for _, tc := range []struct {
		args []string
		path string
		body map[string]any
	}{
		{[]string{"forge", "pr", "review", "platform", "12", "--decision", "approved", "--body", "lgtm"}, base + "/pull-requests/12/reviews", map[string]any{"decision": "approved", "body": "lgtm"}},
		{[]string{"forge", "pr", "check", "platform", "12", "--head-sha", "abc", "--name", "unit-tests", "--conclusion", "passed"}, base + "/pull-requests/12/check-runs", map[string]any{"head_sha": "abc", "name": "unit-tests"}},
		{[]string{"forge", "pr", "merge", "platform", "12"}, base + "/pull-requests/12/merge", nil},
		{[]string{"forge", "pr", "close", "platform", "12"}, base + "/pull-requests/12/close", nil},
		{[]string{"forge", "pr", "comment", "platform", "12", "--body", "thanks"}, base + "/pull-requests/12/comments", map[string]any{"body": "thanks"}},
		{[]string{"forge", "policy", "set", "platform", "--approvals", "2", "--check", "unit-tests,lint"}, base + "/branch-policies", map[string]any{"required_approvals": float64(2)}},
		{[]string{"forge", "release", "new", "platform", "--tag", "v1.2.0", "--name", "1.2.0", "--prerelease"}, base + "/releases", map[string]any{"tag": "v1.2.0", "prerelease": true}},
	} {
		if out, err := run(t, tc.args...); err != nil {
			t.Fatalf("%v: %v\n%s", tc.args, err, out)
		}
		calls := f.find("POST", tc.path)
		if len(calls) == 0 {
			t.Fatalf("%v: no POST %s in %+v", tc.args, tc.path, f.calls)
		}
		for k, v := range tc.body {
			if calls[len(calls)-1].Body[k] != v {
				t.Errorf("%v: body[%s] = %v want %v", tc.args, k, calls[len(calls)-1].Body[k], v)
			}
		}
	}
	if _, err := run(t, "forge", "pr", "review", "platform", "12", "--decision", "maybe"); err == nil {
		t.Fatal("an unknown decision is a usage error")
	}
}

func TestForgeCheckReportReadsEvidenceFromAFile(t *testing.T) {
	f := fakeRoutes(t, map[string]any{
		"POST /forge/repositories/" + forgeRepoID + "/check-runs": resp{201, `{"data":{"id":"c1","name":"unit-tests","status":"completed"}}`},
	})
	out, err := runIn(t, `{"evidence":{"tests":42,"failed":0}}`, "forge", "check", "report", forgeRepoID, "--commit", "0123456789012345678901234567890123456789", "--name", "unit-tests", "--conclusion", "passed", "--file", "-")
	if err != nil || !strings.Contains(out, "unit-tests") {
		t.Fatalf("%v\n%s", err, out)
	}
	b := f.find("POST", "/forge/repositories/"+forgeRepoID+"/check-runs")[0].Body
	ev, _ := b["evidence"].(map[string]any)
	if ev["tests"] != float64(42) || b["conclusion"] != "passed" || b["commit_sha"] == nil {
		t.Fatalf("body = %v", b)
	}
}

func TestHostRegisterShowsTheKeyOnce(t *testing.T) {
	f := fakeRoutes(t, map[string]any{
		"POST /opencomputers/hosts": resp{201, `{"host":{"id":"h1","name":"studio"},"host_key":"hk_SECRET","control_url":"wss://control.example"}`},
	})
	out, err := run(t, "host", "register", "studio", "--platform", "macos")
	if err != nil || !strings.Contains(out, "hk_SECRET") || !strings.Contains(out, "wss://control.example") {
		t.Fatalf("%v\n%s", err, out)
	}
	if b := f.find("POST", "/opencomputers/hosts")[0].Body; b["name"] != "studio" || b["platform"] != "macos" {
		t.Fatalf("body = %v", b)
	}
	if q, _ := run(t, "host", "register", "studio", "--quiet-key"); strings.TrimSpace(q) != "hk_SECRET" {
		t.Fatalf("--quiet-key: %q", q)
	}
}

func TestHostCommandsHitTheRightRoutes(t *testing.T) {
	f := fakeRoutes(t, map[string]any{
		"GET /opencomputers/hosts":                        `{"hosts":[{"id":"h1","name":"studio","state":"online","connected":true,"os_kind":"macos"}]}`,
		"GET /opencomputers/hosts/h1":                     `{"host":{"id":"h1","name":"studio","state":"online"}}`,
		"POST /opencomputers/hosts/h1/tunnels":            resp{201, `{"tunnel":{"id":"t1"}}`},
		"POST /opencomputers/hosts/h1/secrets":            resp{201, `{"secret":{"id":"s1"}}`},
		"DELETE /opencomputers/hosts/h1":                  `{}`,
		"POST /opencomputers/hosts/h1/containers/c1/stop": `{}`,
	})
	if out, err := run(t, "host", "list", "--tag", "prod"); err != nil || !strings.Contains(out, "studio") {
		t.Fatalf("%v\n%s", err, out)
	}
	if q := f.find("GET", "/opencomputers/hosts")[0].Query; q != "tag=prod" {
		t.Fatalf("query = %q", q)
	}
	if out, err := run(t, "host", "get", "h1"); err != nil || !strings.Contains(out, "online") {
		t.Fatalf("a single object under its own key must be unwrapped: %v\n%s", err, out)
	}
	if _, err := run(t, "host", "tunnel", "new", "h1", "--port", "3000", "--access", "organization"); err != nil {
		t.Fatal(err)
	}
	if b := f.find("POST", "/opencomputers/hosts/h1/tunnels")[0].Body; b["target_port"] != float64(3000) || b["auth_mode"] != "tenant_only" {
		t.Fatalf("body = %v", b)
	}
	if _, err := run(t, "host", "container", "stop", "h1", "c1"); err != nil {
		t.Fatal(err)
	}
	if _, err := run(t, "host", "rm", "h1"); err == nil {
		t.Fatal("removing a host needs --yes off a terminal")
	}
	if _, err := run(t, "host", "rm", "h1", "--yes"); err != nil {
		t.Fatal(err)
	}
}

func TestHostSecretValueIsNeverAnArgument(t *testing.T) {
	f := fakeRoutes(t, map[string]any{"POST /opencomputers/hosts/h1/secrets": resp{201, `{"secret":{"id":"s1"}}`}})
	if _, err := runIn(t, "s3cr3t\n", "host", "secret", "set", "h1", "DB_URL", "--key-stdin"); err != nil {
		t.Fatal(err)
	}
	b := f.find("POST", "/opencomputers/hosts/h1/secrets")[0].Body
	if b["value"] != "s3cr3t" || b["name"] != "DB_URL" || b["scope"] != "host" {
		t.Fatalf("body = %v", b)
	}
	if _, err := run(t, "host", "secret", "set", "h1", "DB_URL", "s3cr3t"); err == nil {
		t.Fatal("a third positional (the value) must be refused")
	}
}

func TestCloudAccountAndPreflightWritesAreNotGated(t *testing.T) {
	f := fakeRoutes(t, map[string]any{
		"POST /cloud/accounts":             resp{201, `{"data":{"id":"a1","display_name":"prod","provider":"aws","status":"pending"}}`},
		"POST /cloud/accounts/a1/aws-role": `{"data":{"id":"a1","status":"ready"}}`,
		"POST /cloud/preflights":           resp{201, `{"data":{"id":"p1","status":"pass"}}`},
		"POST /cloud/regions":              resp{201, `{"data":{"id":"r1"}}`},
	})
	if _, err := run(t, "cloud", "account", "new", "prod", "--provider", "aws"); err != nil {
		t.Fatal(err)
	}
	b := f.find("POST", "/cloud/accounts")[0].Body
	if b["display_name"] != "prod" || b["provider"] != "aws" || b["mode"] != "customer_byoc" {
		t.Fatalf("body = %v", b)
	}
	if _, err := run(t, "cloud", "account", "attach-aws", "a1", "--role-arn", "arn:aws:iam::1:role/x", "--region", "us-east-1"); err != nil {
		t.Fatal(err)
	}
	if b := f.find("POST", "/cloud/accounts/a1/aws-role")[0].Body; b["role_arn"] != "arn:aws:iam::1:role/x" || b["default_region"] != "us-east-1" {
		t.Fatalf("body = %v", b)
	}
	if _, err := run(t, "cloud", "preflight", "run"); err == nil {
		t.Fatal("a preflight needs --account or --region")
	}
	if _, err := run(t, "cloud", "preflight", "run", "--account", "a1"); err != nil {
		t.Fatal(err)
	}
	if _, err := run(t, "cloud", "region", "new", "--account", "a1", "--name", "east", "--provider-region", "us-east-1", "--security-group", "sg-1,sg-2", "--meta", "host_image_id=ami-1"); err != nil {
		t.Fatal(err)
	}
	rb := f.find("POST", "/cloud/regions")[0].Body
	if rb["cloud_account_id"] != "a1" || rb["metadata"].(map[string]any)["host_image_id"] != "ami-1" || len(rb["security_group_refs"].([]any)) != 2 {
		t.Fatalf("body = %v", rb)
	}
	if len(f.find("GET", "/cloud/capabilities")) != 0 {
		t.Fatal("account, preflight and region writes do not wait on the provisioning gate")
	}
}

func TestCloudProvisioningFollowsTheConsolePreviewGate(t *testing.T) {
	f := fakeRoutes(t, map[string]any{
		"GET /cloud/capabilities":        `{"data":{"provisioning_enabled":false,"wireguard_configured":true}}`,
		"POST /cloud/pools":              resp{201, `{"data":{"id":"p1"}}`},
		"POST /cloud/pools/p1/provision": `{"data":{}}`,
	})
	_, err := run(t, "cloud", "pool", "new", "--region", "r1", "--instance-type", "m7i.8xlarge", "--target-nodes", "1", "--max-nodes", "4")
	if err == nil || !strings.Contains(err.Error(), "private preview") || !strings.Contains(err.Error(), "provisioning is not activated") {
		t.Fatalf("err = %v", err)
	}
	if len(f.find("POST", "/cloud/pools")) != 0 {
		t.Fatal("a gated command must send nothing")
	}

	f = fakeRoutes(t, map[string]any{
		"GET /cloud/capabilities":        `{"data":{"provisioning_enabled":true,"wireguard_configured":true}}`,
		"POST /cloud/pools":              resp{201, `{"data":{"id":"p1"}}`},
		"POST /cloud/pools/p1/provision": `{"data":{}}`,
	})
	if _, err := run(t, "cloud", "pool", "new", "--region", "r1", "--instance-type", "m7i.8xlarge", "--target-nodes", "1", "--max-nodes", "4", "--scope", "sandbox"); err != nil {
		t.Fatal(err)
	}
	b := f.find("POST", "/cloud/pools")[0].Body
	if b["pool_kind"] != "standing_byoc" || b["node_type"] != "byoc" || b["cloud_region_id"] != "r1" || b["max_nodes"] != float64(4) {
		t.Fatalf("body = %v", b)
	}
	if _, err := run(t, "cloud", "pool", "provision", "p1", "--count", "2"); err == nil {
		t.Fatal("starting billable hosts needs --yes off a terminal")
	}
	if _, err := run(t, "cloud", "pool", "provision", "p1", "--count", "2", "--yes"); err != nil {
		t.Fatal(err)
	}
	if b := f.find("POST", "/cloud/pools/p1/provision")[0].Body; b["count"] != float64(2) {
		t.Fatalf("body = %v", b)
	}
}
