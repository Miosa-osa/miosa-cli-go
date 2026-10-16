package commands_test

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Miosa-osa/miosa-cli-go/commands"
)

// A CLI signed in with a browser session token: the Forge git server takes
// only API keys, so the helper mints a Forge-only key once and reuses it.

const sessionToken = "eyJhbGciOiJIUzI1NiJ9.session.sig"

func gitCredential(t *testing.T, op, input string) (stdout, stderr string) {
	t.Helper()
	commands.ResetForTest()
	root := commands.Root()
	var o, e bytes.Buffer
	root.SetOut(&o)
	root.SetErr(&e)
	root.SetIn(strings.NewReader(input))
	root.SetArgs([]string{"forge", "auth", "git-credential", op})
	t.Cleanup(func() { root.SetIn(nil) })
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	return o.String(), e.String()
}

const forgeRequest = "protocol=https\nhost=forge.miosa.ai\npath=acme/app.git\n\n"

func sessionForgeAPI(t *testing.T, user *string) *fakeAPI {
	t.Helper()
	minted := 0
	f := newFakeAPI(t, func(c call) (int, string) {
		switch c.Method + " " + c.Path {
		case "GET /whoami":
			return 200, `{"user":{"id":"` + *user + `"},"organization":{"id":"org-1","slug":"acme"}}`
		case "POST /api-keys":
			minted++
			n := string(rune('0' + minted))
			return 201, `{"data":{"id":"key-` + n + `","key":"msk_u_forge_` + n + `","scopes":["forge:read","forge:write"]}}`
		case "DELETE /api-keys/key-1":
			return 200, `{"data":{}}`
		}
		return 404, `{}`
	})
	t.Setenv("MIOSA_API_KEY", sessionToken)
	return f
}

func TestGitWithASessionTokenGetsAMintedForgeKeyOnce(t *testing.T) {
	user := "user-1"
	f := sessionForgeAPI(t, &user)

	out, _ := gitCredential(t, "get", forgeRequest)
	if out != "username=miosa\npassword=msk_u_forge_1\n" {
		t.Fatalf("first get = %q", out)
	}
	mint := f.find("POST", "/api-keys")
	if len(mint) != 1 {
		t.Fatalf("mints = %d", len(mint))
	}
	scopes := mint[0].Body["scopes"].([]any)
	if len(scopes) != 2 || scopes[0] != "forge:read" || scopes[1] != "forge:write" {
		t.Fatalf("minted scopes = %v, want forge:read and forge:write only", scopes)
	}
	if got := mint[0].Headers.Get("Authorization"); !strings.HasSuffix(got, sessionToken) {
		t.Fatalf("mint must use the session, got %q", got)
	}
	if strings.Contains(out, sessionToken) {
		t.Fatal("git must never receive the session token")
	}

	// The same session reuses the stored key; a different user mints its own.
	if out, _ := gitCredential(t, "get", forgeRequest); out != "username=miosa\npassword=msk_u_forge_1\n" {
		t.Fatalf("second get = %q", out)
	}
	if n := len(f.find("POST", "/api-keys")); n != 1 {
		t.Fatalf("the stored key was not reused: %d mints", n)
	}
	user = "user-2"
	if out, _ := gitCredential(t, "get", forgeRequest); out != "username=miosa\npassword=msk_u_forge_2\n" {
		t.Fatalf("another user's session got %q, want its own key", out)
	}
}

func TestGitEraseForgetsARejectedForgeKey(t *testing.T) {
	user := "user-1"
	f := sessionForgeAPI(t, &user)
	gitCredential(t, "get", forgeRequest)

	// Erase of some other password leaves the key alone.
	gitCredential(t, "erase", strings.Replace(forgeRequest, "\n\n", "\npassword=msk_other\n\n", 1))
	if !strings.Contains(readConfig(t), "msk_u_forge_1") {
		t.Fatal("an unrelated erase removed the key")
	}

	gitCredential(t, "erase", strings.Replace(forgeRequest, "\n\n", "\npassword=msk_u_forge_1\n\n", 1))
	if strings.Contains(readConfig(t), "forge_key") {
		t.Fatalf("rejected key still stored:\n%s", readConfig(t))
	}
	if len(f.find("DELETE", "/api-keys/key-1")) != 1 {
		t.Fatal("the rejected key was not revoked")
	}
	if out, _ := gitCredential(t, "get", forgeRequest); out != "username=miosa\npassword=msk_u_forge_2\n" {
		t.Fatalf("after erase, get = %q, want a fresh key", out)
	}
}

func TestGitHelperSaysWhyWhenItCannotMint(t *testing.T) {
	newFakeAPI(t, func(c call) (int, string) {
		if c.Path == "/whoami" {
			return 200, `{"user":{"id":"u"},"organization":{"id":"o"}}`
		}
		return 429, `{"error":"Maximum 25 active API keys per workspace"}`
	})
	t.Setenv("MIOSA_API_KEY", sessionToken)
	out, errOut := gitCredential(t, "get", forgeRequest)
	if out != "" || !strings.Contains(errOut, "minting a Forge key for git") {
		t.Fatalf("stdout %q stderr %q", out, errOut)
	}
}

func TestSetupGitWithASessionMintsTheForgeKeyUpFront(t *testing.T) {
	user := "user-1"
	f := sessionForgeAPI(t, &user)
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(t.TempDir(), "gitconfig"))
	defer commands.SetLookPathForTest(func(string) (string, error) { return "/usr/local/bin/miosa", nil })()

	out, err := run(t, "forge", "auth", "setup-git")
	if err != nil {
		t.Fatalf("setup-git: %v %s", err, out)
	}
	if len(f.find("POST", "/api-keys")) != 1 || !strings.Contains(out, "minted a Forge key for git") {
		t.Fatalf("setup-git did not mint: %s", out)
	}
	if strings.Contains(out, "msk_u_forge_1") {
		t.Fatal("setup-git printed the key")
	}

	// A named profile keeps its file on logout, so the key must go from it.
	if err := os.WriteFile(filepath.Join(os.Getenv("HOME"), ".miosa", "config.toml"),
		[]byte(readConfig(t)+"\n[profiles.other]\napi_key = \"msk_u_other\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := run(t, "logout"); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(readConfig(t), "forge_key") {
		t.Fatalf("logout kept the Forge key:\n%s", readConfig(t))
	}
}
