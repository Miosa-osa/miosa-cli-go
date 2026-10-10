package config_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Miosa-osa/miosa-cli-go/internal/config"
)

func home(t *testing.T) string {
	t.Helper()
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("MIOSA_PROFILE", "")
	config.UseProfile("")
	t.Cleanup(func() { config.UseProfile("") })
	return tmp
}

func TestLegacyTopLevelFileIsTheDefaultProfile(t *testing.T) {
	tmp := home(t)
	dir := filepath.Join(tmp, ".miosa")
	os.MkdirAll(dir, 0o700)
	os.WriteFile(filepath.Join(dir, "config.toml"), []byte("api_url = \"https://x/api/v1\"\napi_key = \"msk_u_old\"\ncurrent_sandbox = \"box\"\n"), 0o600)

	got, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if got.APIKey != "msk_u_old" || got.CurrentSandbox != "box" || got.APIURL != "https://x/api/v1" {
		t.Fatalf("got %+v", got)
	}
}

func TestNamedProfilesAreIsolatedAndSaveKeepsOthers(t *testing.T) {
	home(t)
	if err := config.Save(config.Config{APIURL: "https://a/api/v1", APIKey: "msk_u_default"}); err != nil {
		t.Fatal(err)
	}
	config.UseProfile("work")
	f, _ := config.LoadFile()
	f.Set("work", config.Config{APIKey: "msk_u_work", Tenant: "acme"})
	if err := config.SaveFile(f); err != nil {
		t.Fatal(err)
	}

	got, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if got.APIKey != "msk_u_work" || got.Tenant != "acme" {
		t.Fatalf("work profile: %+v", got)
	}
	if got.APIURL != "https://a/api/v1" {
		t.Fatalf("profile should inherit the default api_url, got %q", got.APIURL)
	}

	got.CurrentSandbox = "w-box"
	if err := config.Save(got); err != nil {
		t.Fatal(err)
	}

	config.UseProfile("")
	def, _ := config.Load()
	if def.APIKey != "msk_u_default" || def.CurrentSandbox != "" {
		t.Fatalf("default profile was clobbered: %+v", def)
	}
	config.UseProfile("work")
	w, _ := config.Load()
	if w.CurrentSandbox != "w-box" || w.APIKey != "msk_u_work" {
		t.Fatalf("work profile lost state: %+v", w)
	}
}

func TestUnknownProfileIsAnErrorNotAFallback(t *testing.T) {
	home(t)
	config.Save(config.Config{APIKey: "msk_u_default"})
	config.UseProfile("typo")
	if _, err := config.Load(); err == nil || !strings.Contains(err.Error(), `profile "typo" not found`) {
		t.Fatalf("err = %v", err)
	}
}

func TestSelectionOrder(t *testing.T) {
	home(t)
	f := config.File{Config: config.DefaultConfig(), ActiveProfile: "fromfile"}
	f.Set("fromfile", config.Config{APIKey: "k-file"})
	f.Set("fromenv", config.Config{APIKey: "k-env"})
	f.Set("fromflag", config.Config{APIKey: "k-flag"})
	if err := config.SaveFile(f); err != nil {
		t.Fatal(err)
	}
	if c, _ := config.Load(); c.APIKey != "k-file" {
		t.Fatalf("active_profile: %+v", c)
	}
	t.Setenv("MIOSA_PROFILE", "fromenv")
	if c, _ := config.Load(); c.APIKey != "k-env" {
		t.Fatalf("env: %+v", c)
	}
	config.UseProfile("fromflag")
	if c, _ := config.Load(); c.APIKey != "k-flag" {
		t.Fatalf("flag: %+v", c)
	}
}

func TestClearKeepsOtherProfilesAndURL(t *testing.T) {
	tmp := home(t)
	f := config.File{Config: config.Config{APIURL: "https://a/api/v1", APIKey: "d"}}
	f.Set("work", config.Config{APIURL: "https://w/api/v1", APIKey: "w", CurrentSandbox: "box"})
	config.SaveFile(f)

	config.UseProfile("work")
	if err := config.Clear(); err != nil {
		t.Fatal(err)
	}
	f2, _ := config.LoadFile()
	w, ok := f2.Get("work")
	if !ok || w.APIKey != "" || w.CurrentSandbox != "" || w.APIURL != "https://w/api/v1" {
		t.Fatalf("work after logout: %+v ok=%v", w, ok)
	}
	if f2.Config.APIKey != "d" {
		t.Fatal("default profile must keep its key")
	}

	// The default profile alone: logout deletes the file, as before.
	config.UseProfile("")
	os.Remove(filepath.Join(tmp, ".miosa", "config.toml"))
	config.Save(config.Config{APIKey: "only"})
	if err := config.Clear(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(tmp, ".miosa", "config.toml")); !os.IsNotExist(err) {
		t.Fatalf("file should be gone, stat err = %v", err)
	}
}

func TestSavedFileIsPrivate(t *testing.T) {
	tmp := home(t)
	config.Save(config.Config{APIKey: "secret"})
	fi, err := os.Stat(filepath.Join(tmp, ".miosa", "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %v, want 0600", fi.Mode().Perm())
	}
}

func TestSaveRefusesToReplaceACorruptFile(t *testing.T) {
	tmp := home(t)
	dir := filepath.Join(tmp, ".miosa")
	os.MkdirAll(dir, 0o700)
	os.WriteFile(filepath.Join(dir, "config.toml"), []byte("this is = = not toml"), 0o600)
	if err := config.Save(config.Config{APIKey: "x"}); err == nil {
		t.Fatal("expected an error instead of overwriting a corrupt config")
	}
}

func TestNamesListsDefaultFirst(t *testing.T) {
	f := config.File{}
	f.Set("zeta", config.Config{})
	f.Set("alpha", config.Config{})
	got := strings.Join(f.Names(), ",")
	if got != "default,alpha,zeta" {
		t.Fatalf("names = %s", got)
	}
}
