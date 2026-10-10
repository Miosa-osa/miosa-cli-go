package commands_test

import (
	"strings"
	"testing"
)

// The product names (new, rm, set, unset, file put/rm, protect) and the older
// spellings must reach the same endpoints.
func TestEnvCommandsAnswerToTheirProductNamesAndTheOldOnes(t *testing.T) {
	api := envAPI(t)

	for _, name := range []string{"new", "create"} {
		if _, err := run(t, "env", name, "q-"+name); err != nil {
			t.Fatalf("env %s: %v", name, err)
		}
	}
	if got := api.find("POST", "/environments"); len(got) != 2 || got[0].Body["name"] != "q-new" || got[1].Body["name"] != "q-create" {
		t.Fatalf("create/new: %+v", got)
	}

	for _, name := range []string{"set", "set-var"} {
		if _, err := runIn(t, "1\n", "env", name, "--env", "staging", "K_"+strings.ReplaceAll(name, "-", "_")); err != nil {
			t.Fatalf("env %s: %v", name, err)
		}
	}
	for _, name := range []string{"unset", "unset-var"} {
		if _, err := run(t, "env", name, "--env", "staging", "A"); err != nil {
			t.Fatalf("env %s: %v", name, err)
		}
	}
	if len(api.find("PUT", "/environments/staging/variables/K_set")) != 1 || len(api.find("PUT", "/environments/staging/variables/K_set_var")) != 1 {
		t.Fatalf("set/set-var: %+v", api.calls)
	}
	if len(api.find("DELETE", "/environments/staging/variables/A")) != 2 {
		t.Fatalf("unset/unset-var: %+v", api.calls)
	}

	if _, err := runIn(t, "X=1\n", "env", "file", "put", "--env", "staging", ".env", "-"); err != nil {
		t.Fatal(err)
	}
	if _, err := runIn(t, "X=2\n", "env", "add-file", "--env", "staging", ".env", "-"); err != nil {
		t.Fatalf("old add-file must keep working: %v", err)
	}
	if len(api.find("PUT", "/environments/staging/files")) != 2 {
		t.Fatalf("file put/add-file: %+v", api.calls)
	}
	if _, err := run(t, "env", "file", "rm", "--env", "staging", ".env"); err != nil {
		t.Fatal(err)
	}
	if _, err := run(t, "env", "rm-file", "--env", "staging", ".env"); err != nil {
		t.Fatalf("old rm-file must keep working: %v", err)
	}
	if len(api.find("DELETE", "/environments/staging/files")) != 2 {
		t.Fatalf("file rm/rm-file: %+v", api.calls)
	}

	if _, err := run(t, "env", "protect", "--env", "staging", "on"); err != nil {
		t.Fatal(err)
	}
	if _, err := run(t, "env", "safe-third-parties", "--env", "staging", "off"); err != nil {
		t.Fatalf("old safe-third-parties must keep working: %v", err)
	}
	p := api.find("PATCH", "/environments/staging")
	if len(p) != 2 || p[0].Body["safe_for_third_parties"] != true || p[1].Body["safe_for_third_parties"] != false {
		t.Fatalf("protect: %+v", p)
	}

	for _, name := range []string{"rm", "delete"} {
		if _, err := run(t, "env", name, "gone-"+name, "--yes"); err != nil {
			t.Fatalf("env %s: %v", name, err)
		}
	}
	if len(api.find("DELETE", "/environments/gone-rm")) != 1 || len(api.find("DELETE", "/environments/gone-delete")) != 1 {
		t.Fatalf("rm/delete: %+v", api.calls)
	}
}

func TestEnvHelpShowsProductNamesAndHidesRetiredOnes(t *testing.T) {
	envAPI(t)
	out, err := run(t, "env", "--help")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"new", "protect", "file", "set", "unset"} {
		if !strings.Contains(out, want) {
			t.Errorf("env help lacks %q:\n%s", want, out)
		}
	}
	for _, retired := range []string{"safe-third-parties", "add-file", "rm-file"} {
		if strings.Contains(out, retired) {
			t.Errorf("env help still lists the retired %q:\n%s", retired, out)
		}
	}
}
