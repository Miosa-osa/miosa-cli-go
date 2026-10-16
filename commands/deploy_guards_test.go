package commands_test

import (
	"strings"
	"testing"

	"github.com/Miosa-osa/miosa-cli-go/commands"
)

const (
	liveVer = "67cf439d-1111-4111-8111-111111111111"
	newVer  = "90a9dee1-2222-4222-8222-222222222222"
	oldVer  = "1234abcd-3333-4333-8333-333333333333"
)

func guardRoutes(domains string) map[string]any {
	return map[string]any{
		"GET /deployments":                         `{"data":[{"id":"` + depID + `","name":"My App","slug":"my-app"}]}`,
		"GET /deployments/" + depID:                `{"data":{"id":"` + depID + `","name":"My App","active_version_id":"` + liveVer + `"}}`,
		"GET /deployments/" + depID + "/domains":   `{"data":[` + domains + `]}`,
		"GET /deployments/" + depID + "/versions":  `{"data":[{"id":"` + newVer + `","version_number":3,"artifact_sha256":"aaaa1111bbbb"},{"id":"` + liveVer + `","version_number":2,"artifact_sha256":"cccc2222dddd"},{"id":"` + oldVer + `","version_number":1,"artifact_sha256":"eeee3333ffff"}]}`,
		"DELETE /deployments/" + depID:             `{}`,
		"POST /deployments/" + depID + "/rollback": `{}`,
	}
}

func TestDeployRmRefusesWhileCustomDomainsAreAttached(t *testing.T) {
	f := fakeRoutes(t, guardRoutes(`{"id":"d1","hostname":"lunivate.com"},{"id":"d2","hostname":"www.lunivate.com"}`))
	out, err := run(t, "deploy", "rm", "my-app", "-y")
	if err == nil {
		t.Fatalf("rm must refuse while domains are attached:\n%s", out)
	}
	for _, want := range []string{"lunivate.com", "www.lunivate.com", "domain-move", "--with-domains"} {
		if !strings.Contains(out+err.Error(), want) {
			t.Errorf("refusal must mention %q:\n%s\n%v", want, out, err)
		}
	}
	if len(f.find("DELETE", "/deployments/"+depID)) != 0 {
		t.Fatal("nothing may be deleted")
	}

	out, err = run(t, "deploy", "rm", "my-app", "--with-domains", "-y")
	if err != nil {
		t.Fatalf("--with-domains -y deletes: %v\n%s", err, out)
	}
	calls := f.find("DELETE", "/deployments/"+depID)
	if len(calls) != 1 || strings.Contains(calls[0].Query, "with_domains") {
		t.Fatalf("one DELETE without the client-only flag, got %+v", calls)
	}
}

func TestDeployRmWithoutDomainsStillConfirms(t *testing.T) {
	f := fakeRoutes(t, guardRoutes(""))
	if _, err := run(t, "deploy", "rm", "my-app"); err == nil || !strings.Contains(err.Error(), "--yes") {
		t.Fatalf("must refuse without --yes: %v", err)
	}
	if len(f.find("DELETE", "/deployments/"+depID)) != 0 {
		t.Fatal("nothing may be deleted before confirmation")
	}
	if out, err := run(t, "deploy", "rm", "my-app", "-y"); err != nil || !strings.Contains(out, "Deleted") {
		t.Fatalf("rm -y: %v\n%s", err, out)
	}
}

func TestDeployRollbackWithoutToListsVersionsAndChangesNothing(t *testing.T) {
	f := fakeRoutes(t, guardRoutes(""))
	out, err := run(t, "deploy", "rollback", "my-app", "-y")
	if err == nil || !strings.Contains(err.Error(), "--to") {
		t.Fatalf("rollback without --to must refuse: %v\n%s", err, out)
	}
	for _, want := range []string{"live now:", "v2", "67cf439d", "90a9dee1", "(live)"} {
		if !strings.Contains(out, want) {
			t.Errorf("listing must show %q:\n%s", want, out)
		}
	}
	if len(f.find("POST", "/deployments/"+depID+"/rollback")) != 0 {
		t.Fatal("nothing may be rolled back")
	}
}

func TestDeployRollbackNamesLiveAndTargetAndSendsTheFullID(t *testing.T) {
	f := fakeRoutes(t, guardRoutes(""))
	if _, err := run(t, "deploy", "rollback", "my-app", "--to", "1234abcd"); err == nil || !strings.Contains(err.Error(), "--yes") {
		t.Fatalf("must refuse without --yes: %v", err)
	}
	if len(f.find("POST", "/deployments/"+depID+"/rollback")) != 0 {
		t.Fatal("nothing may be sent before confirmation")
	}
	out, err := run(t, "deploy", "rollback", "my-app", "--to", "1234abcd", "-y")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !strings.Contains(out, "live now:") || !strings.Contains(out, "will switch to:") || !strings.Contains(out, "v1") {
		t.Errorf("must print live and target:\n%s", out)
	}
	calls := f.find("POST", "/deployments/"+depID+"/rollback")
	if len(calls) != 1 || calls[0].Body["version_id"] != oldVer {
		t.Fatalf("want version_id %s, got %+v", oldVer, calls)
	}
	if k := calls[0].Headers.Get("Idempotency-Key"); len(k) < 8 {
		t.Fatalf("the API refuses a rollback without an Idempotency-Key, got %q", k)
	}
	// By version number too.
	if _, err := run(t, "deploy", "rollback", "my-app", "--to", "1", "-y"); err != nil {
		t.Fatal(err)
	}
	// The live version is refused, not re-sent.
	if _, err := run(t, "deploy", "rollback", "my-app", "--to", "67cf439d", "-y"); err == nil || !strings.Contains(err.Error(), "already live") {
		t.Fatalf("rolling back to the live version must refuse: %v", err)
	}
}

// A cli-preset key on servers before 2.3.11 lacked domains:read: rm must say
// plainly that nothing was deleted and which scope is missing (exit 3).
func TestDeployRmWithoutDomainsReadSaysNothingWasDeleted(t *testing.T) {
	routes := guardRoutes("")
	f := newFakeAPI(t, func(c call) (int, string) {
		if c.is("GET", "/deployments/"+depID+"/domains") {
			return 403, `{"error":{"code":"FORBIDDEN","message":"insufficient scope - this action requires the 'domains:read' scope."}}`
		}
		return routeReply(routes)(c)
	})
	_, err := run(t, "deploy", "rm", "my-app", "-y")
	if err == nil || commands.ExitCode(err) != 3 {
		t.Fatalf("err = %v (exit %d), want exit 3", err, commands.ExitCode(err))
	}
	for _, want := range []string{"NOT deleted", "domains:read", "miosa login"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error must mention %q: %v", want, err)
		}
	}
	if len(f.find("DELETE", "/deployments/"+depID)) != 0 {
		t.Fatal("nothing may be deleted")
	}
}
