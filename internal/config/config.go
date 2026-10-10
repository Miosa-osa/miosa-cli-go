// Package config handles loading and saving ~/.miosa/config.toml.
//
// The file holds one default profile at the top level (the layout older CLI
// versions wrote) and any number of named profiles under [profiles.<name>].
// Load returns the effective Config of the selected profile and Save writes a
// Config back into the same profile, so commands never deal with the file
// layout.
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"
)

const (
	DefaultBaseURL = "https://api.miosa.ai/api/v1"
	configDir      = ".miosa"
	configFile     = "config.toml"

	// DefaultProfile is the name of the top-level profile.
	DefaultProfile = "default"
)

// Config is the effective settings of one profile.
type Config struct {
	APIURL           string `toml:"api_url"`
	APIKey           string `toml:"api_key"`
	DefaultWorkspace string `toml:"default_workspace"`
	CurrentSandbox   string `toml:"current_sandbox"`
	// Tenant is the organization slug the key belongs to. Informational: the
	// key itself decides the tenant.
	Tenant string `toml:"tenant,omitempty"`
}

// File is the on-disk layout.
type File struct {
	Config
	ActiveProfile string            `toml:"active_profile,omitempty"`
	Profiles      map[string]Config `toml:"profiles,omitempty"`
}

// DefaultConfig returns a Config with only the default base URL set.
func DefaultConfig() Config {
	return Config{
		APIURL:           DefaultBaseURL,
		DefaultWorkspace: "default",
	}
}

var selected string

// UseProfile selects the profile Load and Save operate on. An empty name means
// "the active profile from the file" (or the default profile).
func UseProfile(name string) { selected = strings.TrimSpace(name) }

// SelectedProfile returns the name given to UseProfile.
func SelectedProfile() string { return selected }

// Path returns the absolute path to the config file.
func Path() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("cannot determine home directory: %w", err)
	}
	return filepath.Join(home, configDir, configFile), nil
}

// Dir returns the config directory (~/.miosa).
func Dir() (string, error) {
	p, err := Path()
	if err != nil {
		return "", err
	}
	return filepath.Dir(p), nil
}

// LoadFile reads the whole file. A missing file yields defaults.
func LoadFile() (File, error) {
	f := File{Config: DefaultConfig()}
	path, err := Path()
	if err != nil {
		return f, err
	}
	if _, err := toml.DecodeFile(path, &f); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return f, nil
		}
		return f, fmt.Errorf("reading config %s: %w", path, err)
	}
	return f, nil
}

// SaveFile writes the whole file (mode 0600, directory 0700).
func SaveFile(f File) error {
	path, err := Path()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("creating config directory: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".config-*.toml")
	if err != nil {
		return fmt.Errorf("opening config file for write: %w", err)
	}
	defer os.Remove(tmp.Name())
	if err := os.Chmod(tmp.Name(), 0o600); err != nil {
		tmp.Close()
		return err
	}
	if err := toml.NewEncoder(tmp).Encode(f); err != nil {
		tmp.Close()
		return fmt.Errorf("encoding config: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("writing config: %w", err)
	}
	return nil
}

// ActiveName returns the profile name in effect for f: the explicit selection,
// MIOSA_PROFILE, the file's active_profile, or "default".
func (f File) ActiveName() string {
	for _, n := range []string{selected, os.Getenv("MIOSA_PROFILE"), f.ActiveProfile} {
		if n = strings.TrimSpace(n); n != "" {
			return n
		}
	}
	return DefaultProfile
}

// Get returns a profile's settings. The default profile is the top level.
func (f File) Get(name string) (Config, bool) {
	if name == "" || name == DefaultProfile {
		return f.Config, true
	}
	c, ok := f.Profiles[name]
	return c, ok
}

// Set stores a profile's settings.
func (f *File) Set(name string, c Config) {
	if name == "" || name == DefaultProfile {
		f.Config = c
		return
	}
	if f.Profiles == nil {
		f.Profiles = map[string]Config{}
	}
	f.Profiles[name] = c
}

// Names lists profile names, default first.
func (f File) Names() []string {
	names := make([]string, 0, len(f.Profiles)+1)
	names = append(names, DefaultProfile)
	rest := make([]string, 0, len(f.Profiles))
	for n := range f.Profiles {
		rest = append(rest, n)
	}
	sort.Strings(rest)
	return append(names, rest...)
}

// Load returns the effective Config of the selected profile. A missing file
// yields defaults. A selected profile that does not exist is an error, so a
// typo never falls back to another account's credentials.
func Load() (Config, error) {
	f, err := LoadFile()
	if err != nil {
		return DefaultConfig(), err
	}
	name := f.ActiveName()
	c, ok := f.Get(name)
	if !ok {
		return DefaultConfig(), fmt.Errorf("profile %q not found (see 'miosa profile list')", name)
	}
	if name != DefaultProfile {
		// A named profile inherits the API URL when it does not set one.
		if c.APIURL == "" {
			c.APIURL = f.Config.APIURL
		}
		if c.DefaultWorkspace == "" {
			c.DefaultWorkspace = "default"
		}
	}
	return c, nil
}

// Save writes cfg into the selected profile and keeps every other profile.
func Save(cfg Config) error {
	f, err := LoadFile()
	if err != nil {
		// A corrupt file must not be silently replaced.
		if _, statErr := os.Stat(mustPath()); statErr == nil {
			return err
		}
		f = File{Config: DefaultConfig()}
	}
	f.Set(f.ActiveName(), cfg)
	return SaveFile(f)
}

// Clear signs the selected profile out: its key and current sandbox are
// removed and everything else is kept. When the default profile is the only
// one, the file is deleted.
func Clear() error {
	f, err := LoadFile()
	if err != nil {
		return err
	}
	name := f.ActiveName()
	if name == DefaultProfile && len(f.Profiles) == 0 {
		return removeFile()
	}
	c, ok := f.Get(name)
	if !ok {
		return nil
	}
	c.APIKey, c.CurrentSandbox = "", ""
	f.Set(name, c)
	return SaveFile(f)
}

// ClearAll deletes the config file.
func ClearAll() error { return removeFile() }

func removeFile() error {
	path, err := Path()
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("removing config: %w", err)
	}
	return nil
}

func mustPath() string {
	p, _ := Path()
	return p
}
