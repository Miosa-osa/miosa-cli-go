package commands_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// entriesOf reads the API's entry-list variables body ({name, value, build}).
func entriesOf(t *testing.T, body map[string]any) map[string]map[string]any {
	t.Helper()
	list, ok := body["variables"].([]any)
	if !ok {
		t.Fatalf("want the entry list (the only shape that sets build), got %#v", body["variables"])
	}
	out := map[string]map[string]any{}
	for _, e := range list {
		entry := e.(map[string]any)
		out[entry["name"].(string)] = entry
	}
	return out
}

func TestServiceVarsSetBuildMarksVariablesBuildVisible(t *testing.T) {
	f := servicesAPI(t)
	out, err := run(t, "service", "vars", "set", "cliniciq", "API", "NEXT_PUBLIC_API_URL=https://api.example.com", "A=1", "--build")
	if err != nil {
		t.Fatalf("vars set --build: %v", err)
	}
	entries := entriesOf(t, f.find("PUT", flagsService+"/variables")[0].Body)
	if e := entries["NEXT_PUBLIC_API_URL"]; e["build"] != true || e["value"] != "https://api.example.com" || entries["A"]["build"] != true {
		t.Fatalf("entries = %#v", entries)
	}
	if !strings.Contains(out, "build-visible") {
		t.Fatalf("output:\n%s", out)
	}
}

func TestServiceVarsSetNoBuildMakesThemRuntimeOnly(t *testing.T) {
	f := servicesAPI(t)
	if _, err := run(t, "service", "vars", "set", "cliniciq", "API", "A=1", "--no-build"); err != nil {
		t.Fatalf("vars set --no-build: %v", err)
	}
	if e := entriesOf(t, f.find("PUT", flagsService+"/variables")[0].Body)["A"]; e["build"] != false {
		t.Fatalf("entry = %#v", e)
	}
}

func TestServiceVarsSetWithoutAFlagKeepsTheMapShape(t *testing.T) {
	f := servicesAPI(t)
	if _, err := run(t, "service", "vars", "set", "cliniciq", "API", "A=1"); err != nil {
		t.Fatal(err)
	}
	if vars, ok := f.find("PUT", flagsService+"/variables")[0].Body["variables"].(map[string]any); !ok || vars["A"] != "1" {
		t.Fatalf("without --build the build setting is left as it is: %#v", f.find("PUT", flagsService+"/variables")[0].Body)
	}
}

func TestServiceVarsSetBuildRefusesAnEmptyValueAndBothFlags(t *testing.T) {
	f := servicesAPI(t)
	if _, err := run(t, "service", "vars", "set", "cliniciq", "API", "EMPTY=", "--build"); err == nil || !strings.Contains(err.Error(), "needs one") {
		t.Fatalf("want an empty build variable refused, got %v", err)
	}
	if _, err := run(t, "service", "vars", "set", "cliniciq", "API", "A=1", "--build", "--no-build"); err == nil {
		t.Fatal("--build and --no-build together must be refused")
	}
	if n := len(f.find("PUT", flagsService+"/variables")); n != 0 {
		t.Fatalf("nothing may be written on a refusal, got %d writes", n)
	}
}

func TestProjectVarsSetBuildMarksSharedVariables(t *testing.T) {
	f := servicesAPI(t)
	if _, err := run(t, "project", "vars", "set", "cliniciq", "API_BASE=https://api.example.com", "--build"); err != nil {
		t.Fatalf("project vars set --build: %v", err)
	}
	puts := f.find("PUT", flagsProject+"/shared-variables")
	if len(puts) != 1 || entriesOf(t, puts[0].Body)["API_BASE"]["build"] != true {
		t.Fatalf("shared write = %#v", puts)
	}
}

func TestVariablesSetAndImportTakeBuild(t *testing.T) {
	t.Chdir(t.TempDir())
	f := fakeRoutes(t, linkedRoutes())
	setupEnv(t, f.srv)
	if _, err := run(t, "link", "cliniciq", "--service", "API"); err != nil {
		t.Fatal(err)
	}
	if _, err := run(t, "variables", "set", "NEXT_PUBLIC_X=1", "--build"); err != nil {
		t.Fatalf("variables set --build: %v", err)
	}
	path := filepath.Join(t.TempDir(), "build.env")
	if err := os.WriteFile(path, []byte("NEXT_PUBLIC_A=a\nNEXT_PUBLIC_B=b\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := run(t, "variables", "import", path, "--build"); err != nil {
		t.Fatalf("variables import --build: %v", err)
	}
	puts := f.find("PUT", "/projects/"+linkProjectID+"/services/"+linkServiceID+"/variables")
	if len(puts) != 2 || entriesOf(t, puts[0].Body)["NEXT_PUBLIC_X"]["build"] != true {
		t.Fatalf("set write = %#v", puts)
	}
	imported := entriesOf(t, puts[1].Body)
	if imported["NEXT_PUBLIC_A"]["build"] != true || imported["NEXT_PUBLIC_B"]["build"] != true {
		t.Fatalf("import write = %#v", imported)
	}
}
