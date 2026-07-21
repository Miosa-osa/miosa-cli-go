// Package config handles loading and saving ~/.miosa/config.toml.
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
)

const (
	DefaultBaseURL = "https://api.miosa.ai/api/v1"
	configDir      = ".miosa"
	configFile     = "config.toml"
)

// Config is the on-disk representation of ~/.miosa/config.toml.
type Config struct {
	APIURL           string `toml:"api_url"`
	APIKey           string `toml:"api_key"`
	DefaultWorkspace string `toml:"default_workspace"`
	CurrentSandbox   string `toml:"current_sandbox"`
}

// DefaultConfig returns a Config with only the default base URL set.
func DefaultConfig() Config {
	return Config{
		APIURL:           DefaultBaseURL,
		DefaultWorkspace: "default",
	}
}

// Path returns the absolute path to the config file.
func Path() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("cannot determine home directory: %w", err)
	}
	return filepath.Join(home, configDir, configFile), nil
}

// Load reads the config file. If the file does not exist a default Config is
// returned without error.
func Load() (Config, error) {
	path, err := Path()
	if err != nil {
		return DefaultConfig(), err
	}

	cfg := DefaultConfig()
	if _, err := toml.DecodeFile(path, &cfg); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return cfg, nil
		}
		return cfg, fmt.Errorf("reading config %s: %w", path, err)
	}
	return cfg, nil
}

// Save writes cfg to disk, creating the ~/.miosa directory if needed.
func Save(cfg Config) error {
	path, err := Path()
	if err != nil {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("creating config directory: %w", err)
	}

	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("opening config file for write: %w", err)
	}
	defer f.Close()

	enc := toml.NewEncoder(f)
	if err := enc.Encode(cfg); err != nil {
		return fmt.Errorf("encoding config: %w", err)
	}
	return nil
}

// Clear removes the config file (used by logout).
func Clear() error {
	path, err := Path()
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("removing config: %w", err)
	}
	return nil
}
