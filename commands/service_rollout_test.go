package commands_test

import (
	"strings"
	"testing"
)

const rolloutProject = "11111111-1111-4111-8111-111111111111"
const rolloutService = "33333333-3333-4333-8333-333333333333"

func rolloutRoutes() map[string]any {
	return map[string]any{
		"GET /projects": `{"data":[{"id":"` + rolloutProject + `","name":"shop","slug":"shop"}]}`,
		"GET /projects/" + rolloutProject + "/services": `{"data":[
			{"id":"` + rolloutService + `","name":"API","slug":"api","kind":"server","lock_version":3,"settings":{}}]}`,
		"PATCH /projects/" + rolloutProject + "/services/" + rolloutService: `{"data":{"id":"` + rolloutService + `","name":"API","slug":"api","kind":"server","lock_version":4,"settings":{}}}`,
	}
}

func TestServiceUpdateSendsSpreadAndStrategy(t *testing.T) {
	f := fakeRoutes(t, rolloutRoutes())

	out, err := run(t, "service", "update", "shop", "API", "--strategy", "rolling", "--spread", "single_host", "--rollout-step", "10", "--rollout-step", "50", "--rollout-step", "100", "--rollout-step-s", "30", "--terminate-grace", "20")
	if err != nil {
		t.Fatalf("service update: %v\n%s", err, out)
	}

	calls := f.find("PATCH", "/projects/"+rolloutProject+"/services/"+rolloutService)
	if len(calls) != 1 {
		t.Fatalf("PATCH calls = %d", len(calls))
	}
	settings, _ := calls[0].Body["settings"].(map[string]any)
	if settings["spread"] != "single_host" || settings["strategy"] != "rolling" {
		t.Fatalf("settings = %v", settings)
	}
	steps, _ := settings["rollout_steps"].([]any)
	if len(steps) != 3 || steps[2] != float64(100) {
		t.Fatalf("rollout_steps = %v", settings["rollout_steps"])
	}
	if settings["rollout_step_s"] != float64(30) || settings["terminate_grace_s"] != float64(20) {
		t.Fatalf("settings = %v", settings)
	}
}

func TestServiceUpdateRefusesStepsThatDoNotEndAt100(t *testing.T) {
	fakeRoutes(t, rolloutRoutes())

	_, err := run(t, "service", "update", "shop", "API", "--rollout-step", "10", "--rollout-step", "50")
	if err == nil || !strings.Contains(err.Error(), "end at 100") {
		t.Fatalf("err = %v", err)
	}
}

func TestServiceUpdateRefusesStepsOutOfOrder(t *testing.T) {
	fakeRoutes(t, rolloutRoutes())

	_, err := run(t, "service", "update", "shop", "API", "--rollout-step", "50", "--rollout-step", "25", "--rollout-step", "100")
	if err == nil || !strings.Contains(err.Error(), "ascend") {
		t.Fatalf("err = %v", err)
	}
}

func TestServiceUpdateSendsStaticBuildAndOutput(t *testing.T) {
	f := fakeRoutes(t, rolloutRoutes())

	if out, err := run(t, "service", "update", "shop", "API", "--build-command", "npm run build", "--output-dir", "dist"); err != nil {
		t.Fatalf("service update: %v\n%s", err, out)
	}

	settings, _ := f.find("PATCH", "/projects/"+rolloutProject+"/services/"+rolloutService)[0].Body["settings"].(map[string]any)
	if settings["build_command"] != "npm run build" || settings["output_dir"] != "dist" {
		t.Fatalf("settings = %v", settings)
	}
}
