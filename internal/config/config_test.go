package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Miosa-osa/miosa-cli-go/internal/config"
)

func TestRoundtrip(t *testing.T) {
	// Use a temp home dir so tests don't touch ~/.miosa.
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	want := config.Config{
		APIURL:           "https://custom.miosa.ai/api/v1",
		APIKey:           "msk_u_testkey",
		DefaultWorkspace: "myws",
		CurrentSandbox:   "my-box",
	}

	if err := config.Save(want); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, err := config.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if got != want {
		t.Errorf("roundtrip mismatch: got %+v, want %+v", got, want)
	}
}

func TestLoadMissingFile(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("expected no error for missing config, got %v", err)
	}
	if cfg.APIURL != config.DefaultBaseURL {
		t.Errorf("expected default APIURL, got %q", cfg.APIURL)
	}
}

func TestClear(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	// Write then clear.
	if err := config.Save(config.DefaultConfig()); err != nil {
		t.Fatalf("Save: %v", err)
	}

	path := filepath.Join(tmp, ".miosa", "config.toml")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("config file should exist after Save: %v", err)
	}

	if err := config.Clear(); err != nil {
		t.Fatalf("Clear: %v", err)
	}

	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("config file should be gone after Clear")
	}
}

func TestClearNoFile(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	// Clear on a missing file must not error.
	if err := config.Clear(); err != nil {
		t.Fatalf("Clear on missing file: %v", err)
	}
}

func TestFileMode(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	if err := config.Save(config.DefaultConfig()); err != nil {
		t.Fatalf("Save: %v", err)
	}

	path := filepath.Join(tmp, ".miosa", "config.toml")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Errorf("expected 0600 file mode, got %04o", mode)
	}
}
