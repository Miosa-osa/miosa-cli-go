package commands_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Miosa-osa/miosa-cli-go/commands"
)

const (
	wo16Trigger  = "a1a1a1a1-0000-4000-8000-000000000001"
	wo16Policy   = "b2b2b2b2-0000-4000-8000-000000000002"
	wo16Computer = "c3c3c3c3-0000-4000-8000-000000000003"
)

// An agent schedule: --agent and --task become agent_id and task, the timeout goes along,
// --on names the machine, and the created schedule is shown with how to run it now.
func TestScheduleCreateWithAnAgent(t *testing.T) {
	f := fakeRoutes(t, map[string]any{
		"GET /agents":                    `{"data":[{"id":"` + boxID + `","name":"drafter"}]}`,
		"GET /computers/" + wo16Computer: `{"data":{"id":"` + wo16Computer + `"}}`,
		"POST /cron-jobs": resp{201, `{"schedule":{"id":"` + wo16Trigger + `","type":"agent","name":"drafts","agent_id":"` + boxID +
			`","schedule":"0 9 * * 1-5","timezone":"UTC","timeout_sec":1800,"task":"Draft notes","state":"active"},"trigger":{"id":"` + wo16Trigger + `","kind":"schedule"}}`},
	})
	out, err := run(t, "schedule", "create", "--name", "drafts", "--cron", "0 9 * * 1-5", "--agent", "drafter",
		"--task", "Draft notes", "--timeout", "1800", "--on", wo16Computer)
	if err != nil {
		t.Fatalf("create: %v\n%s", err, out)
	}
	b := f.find("POST", "/cron-jobs")[0].Body
	machine, _ := b["machine"].(map[string]any)
	if b["agent_id"] != boxID || b["task"] != "Draft notes" || b["timeout_sec"] != float64(1800) ||
		b["agent"] != nil || b["command"] != nil || b["computer_id"] != nil ||
		machine["target"] != "computer" || machine["id"] != wo16Computer {
		t.Fatalf("body = %v", b)
	}
	for _, want := range []string{"drafts", "1800", "Draft notes", "miosa schedule run " + wo16Trigger} {
		if !strings.Contains(out, want) {
			t.Fatalf("output lacks %q:\n%s", want, out)
		}
	}
}

func TestScheduleCreateWithAnAgentRefusesWhatItCannotHonour(t *testing.T) {
	fakeRoutes(t, map[string]any{"POST /cron-jobs": `{}`})
	base := []string{"schedule", "create", "--name", "a", "--cron", "* * * * *"}
	for _, extra := range [][]string{
		{"--agent", "bot"},
		{"--agent", "bot", "--task", "t", "--command", "x"},
		{"--agent", "bot", "--task", "t", "--env", "A=1"},
		{"--agent", "bot", "--task", "t", "--overlap", "skip"},
		{"--agent", "bot", "--task", "t", "--timeout", "30"},
		{"--agent", "bot", "--task", "t", "--timeout", "7200"},
		{"--command", "x", "--task", "t"},
	} {
		args := append(append([]string{}, base...), extra...)
		if out, err := run(t, args...); err == nil || commands.ExitCode(err) != commands.ExitUsage {
			t.Fatalf("%v must be a usage error: %v\n%s", extra, err, out)
		}
	}
}

// 'schedule run' runs an agent schedule through the schedule routes; with --agent it fires
// the trigger directly, and a 404 without --agent names that flag.
func TestScheduleRunAnAgentSchedule(t *testing.T) {
	f := fakeRoutes(t, map[string]any{
		"GET /agents":                        `{"data":[{"id":"` + boxID + `","name":"drafter"}]}`,
		"GET /agents/" + boxID + "/triggers": `{"data":[{"id":"` + wo16Trigger + `","name":"drafts","kind":"schedule"}]}`,
		"POST /agents/" + boxID + "/triggers/" + wo16Trigger + "/fire": resp{202, `{"status":"accepted","firing_id":"f1"}`},
		"POST /cron-jobs/" + wo16Trigger + "/run-now":                  resp{404, `{"error":{"code":"NOT_FOUND","message":"cron job not found"}}`},
		"POST /cron-jobs/drafts/run-now":                               resp{202, `{"status":"accepted","firing_id":"f2","type":"agent","schedule_id":"` + wo16Trigger + `"}`},
	})
	// The schedule routes run an agent schedule by name.
	if out, err := run(t, "schedule", "run", "drafts"); err != nil || !strings.Contains(out, "accepted") {
		t.Fatalf("run: %v\n%s", err, out)
	}
	if out, err := run(t, "schedule", "run", "drafts", "--agent", "drafter"); err != nil || !strings.Contains(out, "accepted") {
		t.Fatalf("run --agent: %v\n%s", err, out)
	}
	if len(f.find("POST", "/agents/"+boxID+"/triggers/"+wo16Trigger+"/fire")) != 1 || len(f.find("POST", "/cron-jobs/"+wo16Trigger+"/run-now")) != 0 {
		t.Fatalf("run --agent must fire the trigger, never the cron-job route")
	}
	_, err := run(t, "schedule", "run", wo16Trigger)
	if err == nil || !strings.Contains(commands.ErrorText(err, false), "--agent") {
		t.Fatalf("a missing schedule must point at --agent: %v", err)
	}
}

func TestAuthorityRulesCreateListGetRm(t *testing.T) {
	policy := `{"id":"` + wo16Policy + `","name":"drafts","scope":"agent","agent_id":"` + boxID + `","status":"active",` +
		`"rules":[{"effect":"allow","class":"internal_write","resource":"storage"},{"effect":"ask","class":"external_write","resource":"email"}],"budget_run_credits":50}`
	f := fakeRoutes(t, map[string]any{
		"GET /agents":                           `{"data":[{"id":"` + boxID + `","name":"drafter"}]}`,
		"POST /agents/policies":                 resp{201, `{"data":` + policy + `}`},
		"GET /agents/policies":                  `{"data":[` + policy + `]}`,
		"GET /agents/policies/" + wo16Policy:    `{"data":` + policy + `}`,
		"DELETE /agents/policies/" + wo16Policy: `{"data":` + policy + `}`,
	})
	out, err := run(t, "agent", "authority", "rules", "create", "--name", "drafts", "--agent", "drafter",
		"--allow", "internal_write:storage", "--ask", "external_write:email", "--deny", "publish", "--budget-run", "50")
	if err != nil || !strings.Contains(out, "allow internal_write:storage") {
		t.Fatalf("create: %v\n%s", err, out)
	}
	b := f.find("POST", "/agents/policies")[0].Body
	rules, _ := b["rules"].([]any)
	if b["agent_id"] != boxID || b["budget_run_credits"] != float64(50) || len(rules) != 3 || b["allow"] != nil {
		t.Fatalf("body = %v", b)
	}
	if last := rules[2].(map[string]any); last["effect"] != "deny" || last["class"] != "publish" || last["resource"] != "*" {
		t.Fatalf("a rule without a resource covers every resource: %v", last)
	}

	// Rules with a time window come from --file; flags add to them.
	file := filepath.Join(t.TempDir(), "policy.json")
	os.WriteFile(file, []byte(`{"rules":[{"effect":"allow","class":"read","resource":"*","window":{"days":[1],"from":"09:00","to":"17:00","tz":"UTC"}}]}`), 0o600)
	if _, err := run(t, "agent", "authority", "rules", "create", "--name", "w", "--file", file, "--ask", "*"); err != nil {
		t.Fatal(err)
	}
	if rules := f.find("POST", "/agents/policies")[1].Body["rules"].([]any); len(rules) != 2 || rules[0].(map[string]any)["window"] == nil {
		t.Fatalf("file rules = %v", rules)
	}

	// /agents/policies is the policy collection, never an agent named "policies".
	if out, err := run(t, "agent", "authority", "rules", "list", "--agent", "drafter"); err != nil || !strings.Contains(out, "drafts") {
		t.Fatalf("list: %v\n%s", err, out)
	}
	if q := f.find("GET", "/agents/policies"); len(q) == 0 || !strings.Contains(q[len(q)-1].Query, "agent_id="+boxID) {
		t.Fatalf("list query = %+v", q)
	}
	if out, err := run(t, "agent", "authority", "rules", "get", "drafts"); err != nil || !strings.Contains(out, "50") {
		t.Fatalf("get: %v\n%s", err, out)
	}
	// The work order's spelling works too.
	if _, err := run(t, "agent", "policy", "create", "--name", "wo", "--allow", "internal_write:storage", "--ask", "external_write:*", "--budget-run", "50", "--budget-day", "200", "--json"); err != nil {
		t.Fatalf("agent policy create: %v", err)
	}
	if b := f.find("POST", "/agents/policies")[2].Body; b["budget_day_credits"] != float64(200) {
		t.Fatalf("agent policy body = %v", b)
	}
	if _, err := run(t, "agent", "authority", "rules", "rm", "b2b2", "-y"); err != nil || len(f.find("DELETE", "/agents/policies/"+wo16Policy)) != 1 {
		t.Fatalf("rm by short id: %v", err)
	}
}

func TestAuthorityRulesCreateRefusesBadRules(t *testing.T) {
	fakeRoutes(t, map[string]any{"POST /agents/policies": `{}`})
	for _, extra := range [][]string{
		{},
		{"--allow", "writes:storage"},
		{"--allow", "read", "--budget-day", "0"},
	} {
		args := append([]string{"agent", "authority", "rules", "create", "--name", "p"}, extra...)
		if out, err := run(t, args...); err == nil || commands.ExitCode(err) != commands.ExitUsage {
			t.Fatalf("%v must be a usage error: %v\n%s", extra, err, out)
		}
	}
}
