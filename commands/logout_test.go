package commands_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Miosa-osa/miosa-cli-go/commands"
)

// optimal-qa (10-16): after 'miosa logout' the "MIOSA CLI" key still answered
// the API. Logout now revokes the stored credential on the server first.

func authOf(c call) string { return c.Headers.Get("Authorization") }

// configText is the config file, or "" once logout deleted it.
func configText(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(os.Getenv("HOME"), ".miosa", "config.toml"))
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	return string(b)
}

func TestLogoutRevokesTheStoredLoginKeyNotTheEnvKey(t *testing.T) {
	f := fakeRoutes(t, map[string]any{"DELETE /cli/session": `{"data":{"status":"revoked"}}`})
	// setupEnv exports MIOSA_API_KEY=msk_u_test: that key must never be revoked.
	writeConfigFile(t, "api_key = \"msk_u_storedloginkey1\"\n")

	out, err := run(t, "logout")
	if err != nil {
		t.Fatalf("logout: %v\n%s", err, out)
	}
	calls := f.find("DELETE", "/cli/session")
	if len(calls) != 1 || authOf(calls[0]) != "Bearer msk_u_storedloginkey1" {
		t.Fatalf("revoke calls = %+v", calls)
	}
	if !strings.Contains(out, "Revoked key") || strings.Contains(out, "storedloginkey1") {
		t.Fatalf("out = %s", out)
	}
	if strings.Contains(configText(t), "msk_u_storedloginkey1") {
		t.Fatal("the key must be removed locally")
	}
}

func TestLogoutWithNoStoredKeyCallsNothing(t *testing.T) {
	f := fakeRoutes(t, nil)
	writeConfigFile(t, "current_sandbox = \"box\"\n")
	if out, err := run(t, "logout"); err != nil {
		t.Fatalf("logout: %v\n%s", err, out)
	}
	if f.count() != 0 {
		t.Fatalf("an env-only key must not be revoked; calls = %d", f.count())
	}
}

func TestLogoutAllRevokesEveryProfile(t *testing.T) {
	f := fakeRoutes(t, map[string]any{"DELETE /cli/session": `{"data":{"status":"revoked"}}`})
	path := writeConfigFile(t, "api_key = \"msk_u_default1\"\n[profiles.work]\napi_key = \"msk_u_work1\"\n")
	if out, err := run(t, "logout", "--all"); err != nil {
		t.Fatalf("logout --all: %v\n%s", err, out)
	}
	got := map[string]bool{}
	for _, c := range f.find("DELETE", "/cli/session") {
		got[authOf(c)] = true
	}
	if !got["Bearer msk_u_default1"] || !got["Bearer msk_u_work1"] || len(got) != 2 {
		t.Fatalf("revoked = %v", got)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("config should be gone: %v", err)
	}
}

func TestLogoutLeavesAKeyItDidNotMintActive(t *testing.T) {
	fakeRoutes(t, map[string]any{"DELETE /cli/session": resp{409, `{"error":{"code":"NOT_A_CLI_LOGIN_KEY","message":"left active"}}`}})
	writeConfigFile(t, "api_key = \"msk_u_mineforci1\"\n")
	out, err := run(t, "logout")
	if err != nil {
		t.Fatalf("logout: %v\n%s", err, out)
	}
	if !strings.Contains(out, "was not created by 'miosa login', so it stays active") {
		t.Fatalf("out = %s", out)
	}
}

func TestLogoutAnAlreadyRevokedKeyIsFine(t *testing.T) {
	fakeRoutes(t, map[string]any{"DELETE /cli/session": resp{401, `{"error":{"code":"INVALID_API_KEY","message":"API key has been revoked"}}`}})
	writeConfigFile(t, "api_key = \"msk_u_oldkey1\"\n")
	if out, err := run(t, "logout"); err != nil || !strings.Contains(out, "already inactive") {
		t.Fatalf("logout: %v\n%s", err, out)
	}
}

func TestLogoutServerDownStillRemovesLocallyAndFails(t *testing.T) {
	fakeRoutes(t, map[string]any{"DELETE /cli/session": resp{503, `{"error":{"code":"UNAVAILABLE","message":"down"}}`}})
	writeConfigFile(t, "api_key = \"msk_u_stuck1\"\n")
	out, err := run(t, "--retries", "0", "logout")
	if err == nil {
		t.Fatalf("a key left active must fail the command\n%s", out)
	}
	if code := commands.ExitCode(err); code != 7 {
		t.Fatalf("exit = %d, want 7", code)
	}
	if !strings.Contains(out, "still active on the server") || strings.Contains(out, "Logged out of") {
		t.Fatalf("out = %s", out)
	}
	if strings.Contains(configText(t), "msk_u_stuck1") {
		t.Fatal("the key must still be removed locally")
	}
}

func TestLogoutOfASessionRevokesTheForgeKeyAndTheSession(t *testing.T) {
	f := fakeRoutes(t, map[string]any{
		"DELETE /api-keys/fk-1":   `{"data":{"status":"revoked"}}`,
		"POST /auth/token/revoke": `{"data":{"revoked":true}}`,
	})
	writeConfigFile(t, "api_key = \"eyJhbGciOi.session.token\"\nforge_key = \"msk_u_forge1\"\nforge_key_id = \"fk-1\"\n")
	out, err := run(t, "logout")
	if err != nil {
		t.Fatalf("logout: %v\n%s", err, out)
	}
	if len(f.find("DELETE", "/api-keys/fk-1")) != 1 || len(f.find("POST", "/auth/token/revoke")) != 1 {
		t.Fatalf("calls = %+v", f.calls)
	}
	if len(f.find("DELETE", "/cli/session")) != 0 {
		t.Fatal("a session token is not a login key")
	}
	if cfg := configText(t); strings.Contains(cfg, "msk_u_forge1") || strings.Contains(cfg, "session.token") {
		t.Fatalf("config = %s", cfg)
	}
}
