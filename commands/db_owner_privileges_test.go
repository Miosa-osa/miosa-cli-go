package commands_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Miosa-osa/miosa-cli-go/commands"
)

// The API takes and answers owner_privileges as a boolean; the CLI's words
// standard and elevated are its false and true.

func TestDBCreateSendsOwnerPrivilegesAsABooleanOnlyWhenGiven(t *testing.T) {
	f := fakeRoutes(t, map[string]any{
		"POST /databases": resp{201, `{"id":"` + dbID + `","name":"app-db","engine":"postgresql","engine_version":"16","state":"provisioning","owner_privileges":true}`},
	})

	for _, args := range [][]string{
		{"db", "create", "app-db", "--version", "16"},
		{"db", "create", "app-db", "--version", "16", "--owner-privileges", "elevated"},
		{"db", "create", "app-db", "--version", "16", "--owner-privileges", "standard"},
	} {
		if out, err := run(t, args...); err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
	}

	calls := f.find("POST", "/databases")
	if len(calls) != 3 {
		t.Fatalf("POST /databases calls = %d, want 3", len(calls))
	}
	if _, sent := calls[0].Body["owner_privileges"]; sent {
		t.Errorf("owner_privileges must not be sent without the flag: %v", calls[0].Body)
	}
	if got := calls[1].Body["owner_privileges"]; got != true {
		t.Errorf("elevated sent owner_privileges = %#v, want true", got)
	}
	if got := calls[2].Body["owner_privileges"]; got != false {
		t.Errorf("standard sent owner_privileges = %#v, want false", got)
	}
}

func TestDBOwnerPrivilegesRefusesAnyOtherValueBeforeSending(t *testing.T) {
	routes := dbRoutes()
	routes["POST /databases"] = `{"id":"` + dbID + `"}`
	routes["PATCH /databases/"+dbID] = `{"id":"` + dbID + `"}`
	f := fakeRoutes(t, routes)

	for _, args := range [][]string{
		{"db", "create", "app-db", "--owner-privileges", "superuser"},
		{"db", "update", "app-db", "--owner-privileges", "true"},
		{"db", "update", "app-db", "--owner-privileges", ""},
	} {
		_, err := run(t, args...)
		if err == nil || commands.ExitCode(err) != commands.ExitUsage {
			t.Fatalf("%v: want a usage error, got %v", args, err)
		}
		if !strings.Contains(err.Error(), "--owner-privileges must be one of standard, elevated") {
			t.Errorf("%v: the error does not name the allowed values: %v", args, err)
		}
	}
	if n := f.count(); n != 0 {
		t.Fatalf("nothing may be sent for an invalid value, got %d calls", n)
	}
}

func TestDBUpdateNeedsTheFlag(t *testing.T) {
	f := fakeRoutes(t, dbRoutes())

	_, err := run(t, "db", "update", "app-db")
	if err == nil || commands.ExitCode(err) != commands.ExitUsage {
		t.Fatalf("want a usage error, got %v", err)
	}
	if n := len(f.find("PATCH", "/databases/"+dbID)); n != 0 {
		t.Fatalf("nothing may be sent without a setting, got %d PATCH calls", n)
	}
}

func TestDBUpdateSendsOnlyOwnerPrivileges(t *testing.T) {
	routes := dbRoutes()
	routes["PATCH /databases/"+dbID] = `{"id":"` + dbID + `","name":"app-db","engine":"postgresql","engine_version":"16","state":"running","owner_privileges":true}`
	f := fakeRoutes(t, routes)

	out, err := run(t, "db", "update", "app-db", "--owner-privileges", "elevated")
	if err != nil {
		t.Fatalf("db update: %v\n%s", err, out)
	}
	calls := f.find("PATCH", "/databases/"+dbID)
	if len(calls) != 1 {
		t.Fatalf("PATCH calls = %d, want 1", len(calls))
	}
	if b, _ := json.Marshal(calls[0].Body); string(b) != `{"owner_privileges":true}` {
		t.Errorf("body = %s, want only owner_privileges true", b)
	}
	if !strings.Contains(out, "Owner privileges") || !strings.Contains(out, "elevated") {
		t.Errorf("output does not show the owner privileges:\n%s", out)
	}
	if strings.Contains(out, "next starts") {
		t.Errorf("a running database must not be told the change waits:\n%s", out)
	}
}

func TestDBUpdateOnAStoppedDatabaseSaysWhenItApplies(t *testing.T) {
	routes := dbRoutes()
	routes["PATCH /databases/"+dbID] = `{"id":"` + dbID + `","name":"app-db","engine":"postgresql","state":"stopped","owner_privileges":false}`
	fakeRoutes(t, routes)

	out, err := run(t, "db", "update", "app-db", "--owner-privileges", "standard")
	if err != nil {
		t.Fatalf("db update: %v\n%s", err, out)
	}
	if !strings.Contains(out, "standard") || !strings.Contains(out, "applies when it next starts") {
		t.Errorf("output does not say the change waits for the next start:\n%s", out)
	}
}

func TestDBGetShowsOwnerPrivileges(t *testing.T) {
	for _, tc := range []struct {
		value bool
		want  string
	}{{true, "elevated"}, {false, "standard"}} {
		routes := dbRoutes()
		routes["GET /databases/"+dbID] = `{"id":"` + dbID + `","name":"app-db","engine":"postgresql","state":"running","owner_privileges":` + map[bool]string{true: "true", false: "false"}[tc.value] + `}`
		fakeRoutes(t, routes)

		out, err := run(t, "db", "get", "app-db")
		if err != nil {
			t.Fatalf("db get: %v\n%s", err, out)
		}
		if !strings.Contains(out, "Owner privileges") || !strings.Contains(out, tc.want) {
			t.Errorf("owner_privileges %v: output lacks %q:\n%s", tc.value, tc.want, out)
		}
	}
}

func TestDBGetLeavesOwnerPrivilegesOutForOtherEngines(t *testing.T) {
	routes := dbRoutes()
	routes["GET /databases/"+dbID] = `{"id":"` + dbID + `","name":"cache","engine":"redis","state":"running","owner_privileges":false}`
	fakeRoutes(t, routes)

	out, err := run(t, "db", "get", "app-db")
	if err != nil {
		t.Fatalf("db get: %v\n%s", err, out)
	}
	if strings.Contains(out, "standard") || strings.Contains(out, "elevated") {
		t.Errorf("a redis database has no owner privileges to show:\n%s", out)
	}
}

func TestDBOwnerPrivilegesRefusalsNameTheNextStep(t *testing.T) {
	t.Setenv("MIOSA_RETRIES", "0")
	update := []string{"db", "update", "app-db", "--owner-privileges", "elevated"}
	cases := []struct {
		code   string
		status int
		args   []string
		want   string
		exit   int
	}{
		{"OWNER_PRIVILEGES_UNSUPPORTED", 422, update, "PostgreSQL 16 or newer", commands.ExitConflict},
		{"OWNER_PRIVILEGES_UNSUPPORTED", 422, []string{"db", "create", "cache", "--engine", "redis", "--owner-privileges", "elevated"}, "PostgreSQL 16 or newer", commands.ExitConflict},
		{"PLAN_LIMIT_EXCEEDED", 402, update, "needs a paid plan", commands.ExitConflict},
		{"PLAN_LIMIT_EXCEEDED", 402, []string{"db", "create", "app-db", "--owner-privileges", "elevated"}, "miosa billing plan", commands.ExitConflict},
		{"OWNER_PRIVILEGES_NOT_APPLIED", 503, update, "nothing changed", commands.ExitServer},
	}
	for _, tc := range cases {
		t.Run(tc.code+" "+tc.args[1], func(t *testing.T) {
			routes := dbRoutes()
			reply := resp{tc.status, `{"error":{"code":"` + tc.code + `","message":"refused"}}`}
			routes["PATCH /databases/"+dbID] = reply
			routes["POST /databases"] = reply
			fakeRoutes(t, routes)

			_, err := run(t, tc.args...)
			if err == nil {
				t.Fatal("a refusal must be an error")
			}
			text := commands.ErrorText(err, false)
			if !strings.Contains(text, tc.code) || !strings.Contains(text, tc.want) {
				t.Errorf("error text lacks %s or %q:\n%s", tc.code, tc.want, text)
			}
			if got := commands.ExitCode(err); got != tc.exit {
				t.Errorf("exit code = %d, want %d", got, tc.exit)
			}
		})
	}
}

func TestAPlanLimitPointsAtThePlanNotAtCredits(t *testing.T) {
	t.Setenv("MIOSA_RETRIES", "0")
	fakeRoutes(t, map[string]any{
		"POST /databases": resp{402, `{"error":{"code":"PLAN_LIMIT_EXCEEDED","message":"Your Free plan allows 1 database"}}`},
	})

	_, err := run(t, "db", "create", "app-db")
	if err == nil {
		t.Fatal("a refusal must be an error")
	}
	text := commands.ErrorText(err, false)
	if strings.Contains(text, "owner-privileges") {
		t.Errorf("a plan limit unrelated to owner privileges must not mention them:\n%s", text)
	}
	if !strings.Contains(text, "upgrade your plan") || strings.Contains(text, "add credits") {
		t.Errorf("a plan limit must point at the plan, not at credits:\n%s", text)
	}
}
