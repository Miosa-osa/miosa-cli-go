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
