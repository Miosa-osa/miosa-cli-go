package commands_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	secretID = "8e4699ca-eee7-4987-b863-5e9329ae7fc6"
	shareWS  = "b89beea8-9094-43ef-9e00-477520c0932e"
)

// The vault CLI: lists never carry a value, a name resolves to an id before the
// call, and reveal is the only command that prints a value.
func TestVaultCommands(t *testing.T) {
	cases := []platformCase{
		{
			args:   []string{"secret", "ls"},
			method: "GET", path: "/secrets",
			reply:   `{"data":[{"id":"` + secretID + `","name":"GHL_TOKEN","scope":"org","current_version":2,"description":"crm","shared_with":[]}]}`,
			wantOut: "GHL_TOKEN",
		},
		{
			args:   []string{"secret", "set", "GHL_TOKEN", "--value", "s3cret", "--scope", "org"},
			method: "POST", path: "/secrets",
			reply:   `{"data":{"id":"` + secretID + `","name":"GHL_TOKEN","current_version":1}}`,
			body:    map[string]any{"name": "GHL_TOKEN", "value": "s3cret", "scope": "org"},
			wantOut: "GHL_TOKEN is at version 1",
		},
		{
			args:   []string{"secret", "get", secretID},
			method: "GET", path: "/secrets/" + secretID,
			reply:   `{"data":{"id":"` + secretID + `","name":"GHL_TOKEN","scope":"org","description":"crm","current_version":2}}`,
			wantOut: "crm",
		},
		{
			// A name is resolved through the list before the detail call.
			args:   []string{"secret", "versions", "GHL_TOKEN"},
			method: "GET", path: "/secrets/" + secretID + "/versions",
			reply:   `{"data":[{"version":2,"created_at":"2026-10-19T09:29:53Z"},{"version":1,"created_at":"2026-10-19T09:29:45Z"}]}`,
			wantOut: "2",
		},
		{
			args:   []string{"secret", "reveal", secretID},
			method: "POST", path: "/secrets/" + secretID + "/reveal",
			reply:   `{"data":{"value":"wo19-live-value-v2"}}`,
			wantOut: "wo19-live-value-v2",
		},
		{
			args:   []string{"secret", "share", secretID, "--workspace", shareWS},
			method: "POST", path: "/secrets/" + secretID + "/shares",
			reply:   `{"data":{"name":"GHL_TOKEN","shared_with":["` + shareWS + `"]}}`,
			body:    map[string]any{"workspace_ids": []any{shareWS}},
			wantOut: shareWS,
		},
		{
			args:   []string{"secret", "unshare", secretID, "--workspace", shareWS},
			method: "DELETE", path: "/secrets/" + secretID + "/shares/" + shareWS,
			reply:   `{"data":{"name":"GHL_TOKEN","shared_with":[]}}`,
			wantOut: "GHL_TOKEN",
		},
		{
			args:   []string{"secret", "rm", secretID, "-y"},
			method: "DELETE", path: "/secrets/" + secretID,
			reply:   `{}`,
			wantOut: "Deleted",
		},
	}

	listReply := `{"data":[{"id":"` + secretID + `","name":"GHL_TOKEN","scope":"org"}]}`

	for _, tc := range cases {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			// Name resolution reads the list first; every case may need it.
			routes := map[string]any{"GET /secrets": listReply}
			routes[tc.method+" "+tc.path] = tc.reply
			f := fakeRoutes(t, routes)

			out, err := run(t, tc.args...)
			if err != nil {
				t.Fatalf("%v\n%s", err, out)
			}

			calls := f.find(tc.method, tc.path)
			if len(calls) != 1 {
				t.Fatalf("want one %s %s, got calls %+v", tc.method, tc.path, f.calls)
			}
			for k, v := range tc.body {
				got, _ := json.Marshal(calls[0].Body[k])
				want, _ := json.Marshal(v)
				if string(got) != string(want) {
					t.Errorf("body[%s] = %s, want %s", k, got, want)
				}
			}
			if tc.wantOut != "" && !strings.Contains(out, tc.wantOut) {
				t.Errorf("output lacks %q:\n%s", tc.wantOut, out)
			}
		})
	}
}

// A dotenv import reads the file and never echoes the values.
func TestVaultImportReadsTheFileWithoutEchoingValues(t *testing.T) {
	dir := t.TempDir()
	body := filepath.Join(dir, "wo.env")
	if err := os.WriteFile(body, []byte("A=one\nB=\"two\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	routes := map[string]any{
		"POST /secrets/import": `{"data":{"names":["A","B"]}}`,
	}
	f := fakeRoutes(t, routes)

	out, err := run(t, "secret", "import", "--file", body, "--scope", "org")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}

	calls := f.find("POST", "/secrets/import")
	if len(calls) != 1 {
		t.Fatalf("want one import call, got %+v", f.calls)
	}
	content, _ := calls[0].Body["content"].(string)
	if !strings.Contains(content, "A=one") {
		t.Errorf("content did not carry the file: %q", content)
	}
	if strings.Contains(out, "one") || strings.Contains(out, "two") {
		t.Errorf("import echoed a value:\n%s", out)
	}
}

// Deleting without --yes must not send anything.
func TestVaultRemoveRefusesWithoutYes(t *testing.T) {
	f := fakeRoutes(t, map[string]any{"GET /secrets": `{"data":[]}`})

	if _, err := run(t, "secret", "rm", secretID); err == nil || !strings.Contains(err.Error(), "--yes") {
		t.Fatalf("must refuse without --yes, got %v", err)
	}
	if len(f.find("DELETE", "/secrets/"+secretID)) != 0 {
		t.Fatal("nothing may be sent before confirmation")
	}
}

// A workspace slug is what people type; the API needs its id. The share and
// unshare commands resolve it the same way every other --workspace flag does.
func TestVaultShareAcceptsAWorkspaceSlug(t *testing.T) {
	const slugWS = "w-1"

	routes := map[string]any{
		"GET /secrets":                                      `{"data":[{"id":"` + secretID + `","name":"GHL_TOKEN","scope":"org"}]}`,
		"GET /workspaces":                                   `{"data":[{"id":"` + slugWS + `","slug":"staging","name":"Staging"}]}`,
		"POST /secrets/" + secretID + "/shares":             `{"data":{"name":"GHL_TOKEN","shared_with":["` + slugWS + `"]}}`,
		"DELETE /secrets/" + secretID + "/shares/" + slugWS: `{"data":{"name":"GHL_TOKEN","shared_with":[]}}`,
	}

	t.Run("share", func(t *testing.T) {
		f := fakeRoutes(t, routes)

		out, err := run(t, "secret", "share", "GHL_TOKEN", "--workspace", "staging")
		if err != nil {
			t.Fatalf("%v\n%s", err, out)
		}

		calls := f.find("POST", "/secrets/"+secretID+"/shares")
		if len(calls) != 1 {
			t.Fatalf("want one POST, got %+v", f.calls)
		}
		got, _ := json.Marshal(calls[0].Body["workspace_ids"])
		if string(got) != `["`+slugWS+`"]` {
			t.Fatalf("workspace_ids = %s, want the resolved id", got)
		}
	})

	t.Run("unshare", func(t *testing.T) {
		f := fakeRoutes(t, routes)

		out, err := run(t, "secret", "unshare", "GHL_TOKEN", "--workspace", "staging")
		if err != nil {
			t.Fatalf("%v\n%s", err, out)
		}

		if calls := f.find("DELETE", "/secrets/"+secretID+"/shares/"+slugWS); len(calls) != 1 {
			t.Fatalf("want one DELETE by resolved id, got %+v", f.calls)
		}
	})
}

// A line the vault cannot take is named with its line number, not dropped in
// silence, and the rest of the file still imports.
func TestSecretImportWarnsOnADashedNameAndKeepsTheRest(t *testing.T) {
	dir := t.TempDir()
	body := filepath.Join(dir, "wo.env")
	if err := os.WriteFile(body, []byte("WO29_SECRET_fk-abc=one\nGOOD_NAME=two\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	f := fakeRoutes(t, map[string]any{
		"POST /secrets/import": `{"data":{"names":["GOOD_NAME"]}}`,
	})

	out, err := run(t, "secret", "import", "--file", body, "--scope", "org")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !strings.Contains(out, ":1:") || !strings.Contains(out, "invalid name") || !strings.Contains(out, "WO29_SECRET_fk-abc") {
		t.Fatalf("the warning must name line 1 and the name:\n%s", out)
	}
	if !strings.Contains(out, "Set 1 secrets") {
		t.Fatalf("the valid line must still import:\n%s", out)
	}
	if strings.Contains(out, "one") || strings.Contains(out, "two") {
		t.Fatalf("import echoed a value:\n%s", out)
	}
	if calls := f.find("POST", "/secrets/import"); len(calls) != 1 {
		t.Fatalf("want one import call, got %+v", f.calls)
	}
}

// When every line is unusable the command fails and sends nothing.
func TestSecretImportFailsWhenEveryLineIsSkipped(t *testing.T) {
	dir := t.TempDir()
	body := filepath.Join(dir, "wo.env")
	if err := os.WriteFile(body, []byte("A-B=1\n# comment\n\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	f := fakeRoutes(t, map[string]any{
		"POST /secrets/import": `{"data":{"names":[]}}`,
	})

	out, err := run(t, "secret", "import", "--file", body, "--scope", "org")
	if err == nil {
		t.Fatalf("an all-skipped file must fail:\n%s", out)
	}
	if !strings.Contains(err.Error(), "no lines could be imported") {
		t.Fatalf("error = %v", err)
	}
	if calls := f.find("POST", "/secrets/import"); len(calls) != 0 {
		t.Fatalf("nothing should be sent, got %+v", calls)
	}
}

// --strict refuses a partial import before anything is set.
func TestSecretImportStrictRefusesAnySkip(t *testing.T) {
	dir := t.TempDir()
	body := filepath.Join(dir, "wo.env")
	if err := os.WriteFile(body, []byte("GOOD_NAME=one\nBAD-NAME=two\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	f := fakeRoutes(t, map[string]any{
		"POST /secrets/import": `{"data":{"names":["GOOD_NAME"]}}`,
	})

	out, err := run(t, "secret", "import", "--file", body, "--scope", "org", "--strict")
	if err == nil {
		t.Fatalf("--strict must fail on a skip:\n%s", out)
	}
	if !strings.Contains(err.Error(), "--strict") {
		t.Fatalf("error = %v", err)
	}
	if calls := f.find("POST", "/secrets/import"); len(calls) != 0 {
		t.Fatalf("--strict sends nothing, got %+v", calls)
	}
}

func TestSecretImportWarnsOnAnEmptyName(t *testing.T) {
	dir := t.TempDir()
	body := filepath.Join(dir, "wo.env")
	if err := os.WriteFile(body, []byte("=value\nOK=1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	fakeRoutes(t, map[string]any{
		"POST /secrets/import": `{"data":{"names":["OK"]}}`,
	})

	out, err := run(t, "secret", "import", "--file", body, "--scope", "org")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !strings.Contains(out, ":1:") || !strings.Contains(out, "empty name") {
		t.Fatalf("the warning must name line 1 and the reason:\n%s", out)
	}
}

// Two spellings of one name are one secret: the later value wins, and the
// duplicate is reported rather than passed over.
func TestSecretImportWarnsOnADuplicate(t *testing.T) {
	dir := t.TempDir()
	body := filepath.Join(dir, "wo.env")
	if err := os.WriteFile(body, []byte("TOKEN=one\ntoken=two\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	fakeRoutes(t, map[string]any{
		"POST /secrets/import": `{"data":{"names":["TOKEN"]}}`,
	})

	out, err := run(t, "secret", "import", "--file", body, "--scope", "org")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !strings.Contains(out, ":2:") || !strings.Contains(out, "duplicate name TOKEN") || !strings.Contains(out, "line 1") {
		t.Fatalf("the warning must name line 2 and the first line:\n%s", out)
	}
	if strings.Contains(out, "one") || strings.Contains(out, "two") {
		t.Fatalf("import echoed a value:\n%s", out)
	}
}
