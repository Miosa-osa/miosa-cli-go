package commands_test

import (
	"strings"
	"testing"
)

// The API refuses a trigger with no name, so `agent triggers add` without --name names it.
func TestAgentTriggersAddNamesATriggerAfterItsKind(t *testing.T) {
	f := fakeRoutes(t, map[string]any{
		"GET /agents":                         `{"data":[{"id":"` + boxID + `","name":"reviewer"}]}`,
		"POST /agents/" + boxID + "/triggers": resp{201, `{"data":{"id":"t2"}}`},
	})
	for _, tc := range []struct {
		args []string
		name string
	}{
		{[]string{"--kind", "schedule", "--schedule", "0 9 * * *"}, "schedule 0 9 * * *"},
		{[]string{"--kind", "webhook"}, "webhook"},
		{[]string{"--kind", "webhook", "--name", "deploys"}, "deploys"},
	} {
		args := append([]string{"agent", "triggers", "add", "reviewer"}, tc.args...)
		if out, err := run(t, args...); err != nil {
			t.Fatalf("%v: %v\n%s", tc.args, err, out)
		}
		calls := f.find("POST", "/agents/"+boxID+"/triggers")
		if got := calls[len(calls)-1].Body["name"]; got != tc.name {
			t.Fatalf("%v: name = %v, want %q", tc.args, got, tc.name)
		}
	}
}

// A refusal names the field and the reason the server gave, not only "The trigger is not valid".
func TestAgentTriggersAddShowsTheServersFieldErrors(t *testing.T) {
	fakeRoutes(t, map[string]any{
		"GET /agents": `{"data":[{"id":"` + boxID + `","name":"reviewer"}]}`,
		"POST /agents/" + boxID + "/triggers": resp{422, `{"error":{"code":"VALIDATION_FAILED","message":"The trigger is not valid.",` +
			`"details":{"config":["cron is not a valid expression"],"daily_run_limit":"must be positive"}}}`},
	})
	_, err := run(t, "agent", "triggers", "add", "reviewer", "--kind", "schedule", "--schedule", "nope")
	if err == nil {
		t.Fatal("a 422 must fail the command")
	}
	for _, want := range []string{"The trigger is not valid.", "config cron is not a valid expression", "daily_run_limit must be positive"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q lacks %q", err, want)
		}
	}
}
