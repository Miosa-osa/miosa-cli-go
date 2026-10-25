package commands_test

import (
	"strings"
	"testing"
)

func TestShareCallsTheShareRoute(t *testing.T) {
	f := fakeRoutes(t, map[string]any{
		"POST /sandboxes/my-box/share": `{"data":{"sandbox_id":"s1","visibility":"shared","changed":true,"shared_at":"2026-10-24T00:00:00Z","credentials":"The creator's personal logins are withheld from the next start."}}`,
	})
	out, err := run(t, "share", "my-box")
	if err != nil || !strings.Contains(out, "shared") || !strings.Contains(out, "withheld") {
		t.Fatalf("%v\n%s", err, out)
	}
	if len(f.find("POST", "/sandboxes/my-box/share")) != 1 {
		t.Fatal("share route not called")
	}
}

func TestUnshareReportsRevokedKeys(t *testing.T) {
	fakeRoutes(t, map[string]any{
		"POST /sandboxes/my-box/unshare": `{"data":{"sandbox_id":"s1","visibility":"private","changed":true,"revoked_ssh_keys":2}}`,
	})
	out, err := run(t, "unshare", "my-box")
	if err != nil || !strings.Contains(out, "private") || !strings.Contains(out, "2") {
		t.Fatalf("%v\n%s", err, out)
	}
}

func TestShareRefusedIsAnError(t *testing.T) {
	fakeRoutes(t, map[string]any{
		"POST /sandboxes/my-box/share": resp{403, `{"error":{"code":"SANDBOX_PRIVATE","message":"This sandbox is private to its creator."}}`},
	})
	if out, err := run(t, "share", "my-box"); err == nil {
		t.Fatalf("a refused share must fail\n%s", out)
	}
}

func TestOrgDefaultVisibility(t *testing.T) {
	f := fakeRoutes(t, map[string]any{
		"GET /settings/sandbox-visibility": `{"data":{"default":"shared"}}`,
		"PUT /settings/sandbox-visibility": `{"data":{"default":"private"}}`,
	})
	out, err := run(t, "org", "default-visibility", "show")
	if err != nil || !strings.Contains(out, "shared") {
		t.Fatalf("%v\n%s", err, out)
	}
	if out, err := run(t, "org", "default-visibility", "set", "public"); err == nil || len(f.find("PUT", "/settings/sandbox-visibility")) != 0 {
		t.Fatalf("an invalid value must not call the API: %v\n%s", err, out)
	}
	if out, err := run(t, "org", "default-visibility", "set", "private"); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if body := f.find("PUT", "/settings/sandbox-visibility")[0].Body; body["default"] != "private" {
		t.Fatalf("body = %v", body)
	}
}

func TestCreateVisibility(t *testing.T) {
	f := fakeRoutes(t, map[string]any{
		"POST /sandboxes": resp{201, `{"id":"abc123","name":"my-box","state":"running","template_id":"miosa-sandbox","visibility":"private"}`},
	})
	if out, err := run(t, "create", "my-box", "--visibility", "private"); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if body := f.find("POST", "/sandboxes")[0].Body; body["visibility"] != "private" {
		t.Fatalf("body = %v", body)
	}

	if out, err := run(t, "create", "other", "--visibility", "secret"); err == nil {
		t.Fatalf("an invalid --visibility must be refused\n%s", out)
	}
	if out, err := run(t, "create", "--count", "2", "--visibility", "private"); err == nil {
		t.Fatalf("--visibility with --count must be refused\n%s", out)
	}
	if n := len(f.find("POST", "/sandboxes")); n != 1 {
		t.Fatalf("refused creates must not call the API: %d calls", n)
	}
}

func TestCreateWithoutVisibilitySendsNone(t *testing.T) {
	f := fakeRoutes(t, map[string]any{
		"POST /sandboxes": resp{201, `{"id":"abc123","name":"my-box","state":"running","template_id":"miosa-sandbox"}`},
	})
	if out, err := run(t, "create", "my-box"); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if _, ok := f.find("POST", "/sandboxes")[0].Body["visibility"]; ok {
		t.Fatal("no --visibility must leave the organization default to the server")
	}
}

func TestListShowsAccessOnlyWhenSomethingIsPrivate(t *testing.T) {
	row := func(name, visibility string) string {
		return `{"id":"8ba07e95-5085-4600-b758-60b6951c578` + name + `","name":"` + name + `","state":"running","template_id":"t","size":"small","created_at":"2026-10-09T00:00:00Z","visibility":"` + visibility + `"}`
	}
	fakeRoutes(t, map[string]any{"GET /sandboxes": `{"data":[` + row("a", "shared") + `,` + row("b", "shared") + `],"meta":{"total":2,"page":1}}`})
	out, err := run(t, "list")
	if err != nil || strings.Contains(out, "ACCESS") {
		t.Fatalf("an all-shared list must look as before: %v\n%s", err, out)
	}

	fakeRoutes(t, map[string]any{"GET /sandboxes": `{"data":[` + row("a", "shared") + `,` + row("b", "private") + `],"meta":{"total":2,"page":1}}`})
	out, err = run(t, "list")
	if err != nil || !strings.Contains(out, "ACCESS") || !strings.Contains(out, "private") {
		t.Fatalf("%v\n%s", err, out)
	}
}
