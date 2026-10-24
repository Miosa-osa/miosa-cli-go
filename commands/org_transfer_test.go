package commands_test

import (
	"strings"
	"testing"

	"github.com/Miosa-osa/miosa-cli-go/commands"
)

func orgTransferRoutes(t *testing.T) *fakeAPI {
	return fakeRoutes(t, map[string]any{
		"GET /tenant/members":             `{"data":[{"id":"m1","user_id":"u-owner","email":"me@acme.dev","role":"owner"},{"id":"m2","user_id":"u-dana","email":"Dana@Acme.dev","role":"member"}]}`,
		"GET /whoami":                     `{"organization":{"slug":"acme","name":"Acme"}}`,
		"POST /tenant/transfer-ownership": `{"data":{"tenant_id":"t-1","new_owner_user_id":"u-dana"}}`,
	})
}

func TestOrgTransferByEmailWithYes(t *testing.T) {
	f := orgTransferRoutes(t)
	out, err := run(t, "org", "transfer", "dana@acme.dev", "--yes")
	if err != nil || !strings.Contains(out, "Dana@Acme.dev is now the owner of acme") {
		t.Fatalf("%v\n%s", err, out)
	}
	if b := f.find("POST", "/tenant/transfer-ownership")[0].Body; b["new_owner_user_id"] != "u-dana" {
		t.Fatalf("body = %v", b)
	}
}

func TestOrgTransferNeedsConfirmationOffATerminal(t *testing.T) {
	f := orgTransferRoutes(t)
	if _, err := runIn(t, "acme\n", "org", "transfer", "u-dana"); commands.ExitCode(err) != commands.ExitUsage {
		t.Fatalf("without --yes off a terminal: %v", err)
	}
	if len(f.find("POST", "/tenant/transfer-ownership")) != 0 {
		t.Fatal("nothing may be transferred without confirmation")
	}
}

func TestOrgTransferUnknownMember(t *testing.T) {
	f := orgTransferRoutes(t)
	if _, err := run(t, "org", "transfer", "nobody@acme.dev", "--yes"); commands.ExitCode(err) != commands.ExitUsage {
		t.Fatalf("unknown member: %v", err)
	}
	if len(f.find("POST", "/tenant/transfer-ownership")) != 0 {
		t.Fatal("nothing may be transferred to a non-member")
	}
}
