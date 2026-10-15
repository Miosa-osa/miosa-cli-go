package commands

import (
	"strings"
	"testing"
)

func domainMoveOp(t *testing.T) Op {
	t.Helper()
	for _, o := range deployOps() {
		if strings.HasPrefix(o.Use, "domain-move") {
			return o
		}
	}
	t.Fatal("domain-move op not found")
	return Op{}
}

// The target is the second argument or --to; either way the request is the
// same, so the command reads the way a person types it.
func TestDomainMoveTakesTheTargetAsAnArgumentOrAFlag(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"argument", []string{"app.example.com", "other-app"}},
		{"flag", []string{"app.example.com", "--to", "other-app"}},
		{"both, the same", []string{"app.example.com", "other-app", "--to", "other-app"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, got := opServer(t, 200, `{"moved":true}`)
			if out, err := runOp(t, domainMoveOp(t), "", tc.args...); err != nil {
				t.Fatalf("%v\n%s", err, out)
			}
			calls := got()
			if len(calls) != 1 {
				t.Fatalf("want one call, got %d", len(calls))
			}
			if calls[0].Body["to"] != "other-app" {
				t.Fatalf("body = %+v", calls[0].Body)
			}
		})
	}
}

// Giving it twice with two different values is a mistake worth naming, not a
// guess: the command says which two values it saw.
func TestDomainMoveRefusesTwoDifferentTargets(t *testing.T) {
	_, got := opServer(t, 200, `{"moved":true}`)

	_, err := runOp(t, domainMoveOp(t), "", "app.example.com", "other-app", "--to", "third-app")
	if err == nil {
		t.Fatal("want an error")
	}
	for _, want := range []string{"other-app", "third-app", "differ"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("the error should name %q: %v", want, err)
		}
	}
	if len(got()) != 0 {
		t.Fatal("nothing may be sent when the target is ambiguous")
	}
}

func TestDomainMoveWantsATarget(t *testing.T) {
	_, got := opServer(t, 200, `{"moved":true}`)

	_, err := runOp(t, domainMoveOp(t), "", "app.example.com")
	if err == nil || !strings.Contains(err.Error(), "domain-move <domain> <target>") {
		t.Fatalf("err = %v", err)
	}
	if len(got()) != 0 {
		t.Fatal("nothing may be sent without a target")
	}
}
