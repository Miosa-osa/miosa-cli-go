package commands_test

import (
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/Miosa-osa/miosa-cli-go/commands"
)

const svcPath = "/projects/11111111-1111-1111-1111-111111111111/services/33333333-3333-3333-3333-333333333333"

// serviceAPI serves the project and service lookups and hands every other call
// to reply.
func serviceAPI(t *testing.T, reply func(c call) (int, string)) *fakeAPI {
	t.Helper()
	return newFakeAPI(t, func(c call) (int, string) {
		switch {
		case c.is("GET", "/projects"):
			return http.StatusOK, serviceProjectJSON
		case c.is("GET", "/projects/11111111-1111-1111-1111-111111111111/services"):
			return http.StatusOK, webServiceJSON
		}
		return reply(c)
	})
}

func TestServiceExecSendsTheArgvAndPrintsTheOutput(t *testing.T) {
	f := serviceAPI(t, func(c call) (int, string) {
		if c.is("POST", svcPath+"/exec") {
			return 200, `{"data":{"replica_id":"r1","deployment_id":"d1","exit_code":0,"stdout":"migrated 3\n","stderr":"warn: slow\n","stdout_truncated":false,"stderr_truncated":false,"duration_ms":1200}}`
		}
		return 404, `{"error":{"code":"NOT_FOUND"}}`
	})

	stdout, stderr, err := runSplit(t, "service", "exec", "cliniciq", "API", "--", "pnpm", "db:migrate")
	if err != nil {
		t.Fatal(err)
	}
	if stdout != "migrated 3\n" || stderr != "warn: slow\n" {
		t.Fatalf("stdout = %q, stderr = %q", stdout, stderr)
	}

	body := f.find("POST", svcPath+"/exec")[0].Body
	if !reflect.DeepEqual(body["command"], []any{"pnpm", "db:migrate"}) {
		t.Fatalf("command = %#v", body["command"])
	}
	if body["timeout"] != float64(60) || body["replica_id"] != nil {
		t.Fatalf("body = %#v", body)
	}
}

func TestServiceExecOneWordRunsInTheShellWithTheTimeoutAndReplica(t *testing.T) {
	f := serviceAPI(t, func(_ call) (int, string) {
		return 200, `{"data":{"replica_id":"r2","exit_code":0,"stdout":"","stderr":""}}`
	})

	_, err := run(t, "service", "exec", "cliniciq", "API", "--timeout", "240", "--replica", "r2", "--",
		"pnpm db:migrate && pnpm db:seed")
	if err != nil {
		t.Fatal(err)
	}
	body := f.find("POST", svcPath+"/exec")[0].Body
	if body["command"] != "pnpm db:migrate && pnpm db:seed" || body["timeout"] != float64(240) || body["replica_id"] != "r2" {
		t.Fatalf("body = %#v", body)
	}
}

func TestServiceExecExitsWithTheCommandsCode(t *testing.T) {
	serviceAPI(t, func(_ call) (int, string) {
		return 200, `{"data":{"replica_id":"r1","exit_code":3,"stdout":"","stderr":"PostgresError: denied\n","stderr_truncated":true}}`
	})

	_, stderr, err := runSplit(t, "service", "exec", "cliniciq", "API", "--", "pnpm", "db:migrate")
	if commands.ExitCode(err) != 3 {
		t.Fatalf("exit = %d (%v)", commands.ExitCode(err), err)
	}
	if !strings.Contains(stderr, "PostgresError: denied\n") || !strings.Contains(stderr, "kept only the first part") {
		t.Fatalf("stderr = %q", stderr)
	}
}

func TestServiceExecNeedsTheSeparatorAndATimeoutItCanKeep(t *testing.T) {
	serviceAPI(t, func(_ call) (int, string) { return 500, `{}` })

	for _, args := range [][]string{
		{"service", "exec", "cliniciq", "API", "pnpm", "db:migrate"},
		{"service", "exec", "cliniciq", "API", "--"},
		{"service", "exec", "cliniciq", "--", "ls"},
		{"service", "exec", "cliniciq", "API", "--timeout", "301", "--", "ls"},
		{"service", "exec", "cliniciq", "API", "--timeout", "0", "--", "ls"},
	} {
		if _, err := run(t, args...); commands.ExitCode(err) != commands.ExitUsage {
			t.Fatalf("%v: exit = %d (%v)", args, commands.ExitCode(err), err)
		}
	}
}

func TestServiceExecOnAStoppedServiceSaysHowToStartIt(t *testing.T) {
	serviceAPI(t, func(_ call) (int, string) {
		return 409, `{"error":{"code":"SERVICE_NOT_RUNNING","message":"the service has no running replica to run the command in: start or deploy it first"}}`
	})

	_, stderr, err := runSplit(t, "service", "exec", "cliniciq", "API", "--", "ls")
	if commands.ExitCode(err) != commands.ExitConflict {
		t.Fatalf("exit = %d (%v)", commands.ExitCode(err), err)
	}
	if !strings.Contains(err.Error()+stderr, "no running replica") {
		t.Fatalf("err = %v, stderr = %q", err, stderr)
	}
}

func TestServiceLogsPreDeployReadsTheNewestReleasesLog(t *testing.T) {
	f := serviceAPI(t, func(c call) (int, string) {
		if c.is("GET", svcPath+"/deployments/latest/pre-deploy/logs") {
			return 200, `{"data":{"deployment":{"id":"d9","number":9,"state":"failed"},"text":"-- stderr --\nPostgresError: denied\n","offset":40,"complete":true}}`
		}
		return 404, `{"error":{"code":"NOT_FOUND"}}`
	})

	stdout, stderr, err := runSplit(t, "service", "logs", "cliniciq", "API", "--pre-deploy", "-n", "50")
	if err != nil {
		t.Fatal(err)
	}
	if stdout != "-- stderr --\nPostgresError: denied\n" {
		t.Fatalf("stdout = %q", stdout)
	}
	if !strings.Contains(stderr, "Release 9 failed d9") {
		t.Fatalf("stderr = %q", stderr)
	}
	if q := f.find("GET", svcPath+"/deployments/latest/pre-deploy/logs")[0].Query; q != "tail=50" {
		t.Fatalf("query = %q", q)
	}
}

func TestServiceLogsPreDeployRefusesAnotherLogAtOnce(t *testing.T) {
	serviceAPI(t, func(_ call) (int, string) { return 500, `{}` })

	for _, flag := range []string{"--events", "--build"} {
		if _, err := run(t, "service", "logs", "cliniciq", "API", "--pre-deploy", flag); commands.ExitCode(err) != commands.ExitUsage {
			t.Fatalf("%s: exit = %d (%v)", flag, commands.ExitCode(err), err)
		}
	}
}

func TestServiceLogsPreDeployWithNoneSaysSo(t *testing.T) {
	serviceAPI(t, func(_ call) (int, string) {
		return 404, `{"error":{"code":"RESOURCE_NOT_FOUND","message":"resource not found"}}`
	})

	_, err := run(t, "service", "logs", "cliniciq", "API", "--pre-deploy")
	if commands.ExitCode(err) != commands.ExitNotFound || !strings.Contains(err.Error(), "no release of this service has a pre-deploy command") {
		t.Fatalf("exit = %d (%v)", commands.ExitCode(err), err)
	}
}
