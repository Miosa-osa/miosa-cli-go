package commands_test

import (
	"strings"
	"testing"
	"time"

	"github.com/Miosa-osa/miosa-cli-go/commands"
)

func TestCreateSendsPerSandboxVariables(t *testing.T) {
	f := newFakeAPI(t, func(c call) (int, string) {
		if c.is("POST", "/sandboxes") {
			return 201, `{"id":"` + boxID + `","name":"web","state":"running","ready":true}`
		}
		return 404, `{}`
	})
	if _, err := run(t, "create", "web", "-e", "API_URL=https://x.dev/a=b", "--var", "DEBUG=1"); err != nil {
		t.Fatal(err)
	}
	env, _ := f.find("POST", "/sandboxes")[0].Body["env"].(map[string]any)
	if env["API_URL"] != "https://x.dev/a=b" || env["DEBUG"] != "1" || len(env) != 2 {
		t.Fatalf("env = %v", env)
	}
}

func TestCreateRefusesABadVariable(t *testing.T) {
	f := newFakeAPI(t, func(call) (int, string) { return 404, `{}` })
	for _, bad := range []string{"NOVALUE", "=x", "K=a\nb"} {
		_, err := run(t, "create", "web", "-e", bad)
		if err == nil || commands.ExitCode(err) != commands.ExitUsage {
			t.Errorf("-e %q: err = %v", bad, err)
		}
	}
	if f.count() != 0 {
		t.Fatal("a refused flag reached the API")
	}
}

func TestCreateCountMakesOneBatch(t *testing.T) {
	polls := 0
	f := newFakeAPI(t, func(c call) (int, string) {
		switch {
		case c.is("POST", "/sandboxes/batch"):
			return 202, `{"data":{"id":"b1","status":"running"}}`
		case c.is("GET", "/sandboxes/batches/b1"):
			polls++
			if polls < 2 {
				return 200, `{"data":{"id":"b1","status":"running","requested":3,"live_count":1,"pending":2,"failed_total":0}}`
			}
			return 200, `{"data":{"id":"b1","status":"completed","requested":3,"live_count":3,"pending":0,"failed_total":0,"settled?":true}}`
		}
		return 404, `{}`
	})
	defer commands.SetPollIntervalForTest(time.Millisecond)()
	out, err := run(t, "create", "ci", "--count", "3", "--concurrency", "2", "--size", "quad", "--ttl", "1h", "-e", "A=1", "--wait")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	body := f.find("POST", "/sandboxes/batch")[0].Body
	if body["count"] != float64(3) || body["concurrency"] != float64(2) || body["name_prefix"] != "ci" || body["size"] != "quad" || body["timeout_sec"] != float64(3600) {
		t.Fatalf("body = %v", body)
	}
	if env, _ := body["env"].(map[string]any); env["A"] != "1" {
		t.Fatalf("env = %v", body["env"])
	}
	if len(f.find("POST", "/sandboxes")) != 0 {
		t.Fatal("--count must not create sandboxes one by one")
	}
	if !strings.Contains(out, "Created 3 sandboxes (batch b1)") {
		t.Fatalf("out = %s", out)
	}
}

func TestCreateCountFailsWhenASandboxFailed(t *testing.T) {
	newFakeAPI(t, func(c call) (int, string) {
		switch {
		case c.is("POST", "/sandboxes/batch"):
			return 202, `{"data":{"id":"b2"}}`
		case c.is("GET", "/sandboxes/batches/b2"):
			return 200, `{"data":{"id":"b2","status":"completed","requested":2,"live_count":1,"failed_total":1,"settled?":true}}`
		}
		return 404, `{}`
	})
	defer commands.SetPollIntervalForTest(time.Millisecond)()
	_, err := run(t, "create", "ci", "--count", "2", "--wait")
	if err == nil || !strings.Contains(err.Error(), "could not create 1 sandbox") {
		t.Fatalf("err = %v", err)
	}
}

func TestCreateCountRefusesSingleSandboxFlags(t *testing.T) {
	f := newFakeAPI(t, func(call) (int, string) { return 404, `{}` })
	for _, args := range [][]string{
		{"create", "ci", "--count", "2", "--runtime", "soma"},
		{"create", "ci", "--count", "2", "--meta", "a=b"},
		{"create", "ci", "--count", "2", "--no-env"},
		{"create", "ci", "--count", "0", "--concurrency", "2"},
	} {
		if _, err := run(t, args...); err == nil || commands.ExitCode(err) != commands.ExitUsage {
			t.Errorf("%v: err = %v", args, err)
		}
	}
	if len(f.find("POST", "/sandboxes/batch")) != 0 {
		t.Fatal("a refused batch reached the API")
	}
}
