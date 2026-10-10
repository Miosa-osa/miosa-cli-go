// Package envspec holds the client-side validation and parsing for
// environments and setup scripts. The API enforces the same rules; checking
// here gives a precise error before anything is sent.
package envspec

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
	"unicode/utf8"
)

const (
	// MaxSetupScriptBytes is the size limit for a setup script (64 KB).
	MaxSetupScriptBytes = 64 * 1024
	// MaxVarNameLen is the longest variable name.
	MaxVarNameLen = 128
	// MaxSecretFileBytes is the per-file limit for secret files (256 KB).
	MaxSecretFileBytes = 256 * 1024
)

var varNameRE = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// ValidateSetupScript checks a setup script: non-empty, at most 64 KB, valid
// UTF-8, no NUL bytes.
func ValidateSetupScript(b []byte) error {
	switch {
	case len(b) == 0:
		return errors.New("setup file is empty")
	case len(b) > MaxSetupScriptBytes:
		return fmt.Errorf("setup file is larger than %d bytes (64KB)", MaxSetupScriptBytes)
	case !utf8.Valid(b):
		return errors.New("setup file is not valid UTF-8")
	case strings.ContainsRune(string(b), 0):
		return errors.New("setup file contains a NUL byte")
	}
	return nil
}

// ReadSetupFile reads and validates a setup script from disk.
func ReadSetupFile(path string) (string, error) {
	b, err := readLimited(path, MaxSetupScriptBytes)
	if err != nil {
		return "", fmt.Errorf("reading setup file: %w", err)
	}
	if err := ValidateSetupScript(b); err != nil {
		return "", fmt.Errorf("%s: %w", path, err)
	}
	return string(b), nil
}

// ReadLimited reads a file, failing if it is larger than limit bytes. It reads
// at most limit+1 bytes so an oversized file is never loaded whole.
func ReadLimited(path string, limit int) ([]byte, error) { return readLimited(path, limit) }

func readLimited(path string, limit int) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, int64(limit)+1))
	if err != nil {
		return nil, err
	}
	return b, nil
}

// ValidateVarName checks an environment variable name.
func ValidateVarName(name string) error {
	if name == "" {
		return errors.New("variable name is empty")
	}
	if len(name) > MaxVarNameLen {
		return fmt.Errorf("variable name %q is longer than %d characters", clip(name), MaxVarNameLen)
	}
	if !varNameRE.MatchString(name) {
		return fmt.Errorf("invalid variable name %q: must match [A-Za-z_][A-Za-z0-9_]*", clip(name))
	}
	return nil
}

// ParseAssignment splits "KEY=VALUE" at the first "=". The value may be empty.
// The value is never echoed in errors.
func ParseAssignment(s string) (key, value string, err error) {
	i := strings.IndexByte(s, '=')
	if i < 0 {
		return "", "", fmt.Errorf("%q is not KEY=VALUE", clip(s))
	}
	key, value = s[:i], s[i+1:]
	if err := ValidateVarName(key); err != nil {
		return "", "", err
	}
	return key, value, nil
}

// Pair is one parsed variable.
type Pair struct{ Key, Value string }

// ParseDotenv parses .env-style content: KEY=VALUE lines, optional "export "
// prefix, blank lines and # comments ignored, one layer of matching single or
// double quotes removed from values. Later duplicates win. Errors name the
// line number only, never the content.
func ParseDotenv(r io.Reader) ([]Pair, error) {
	var out []Pair
	index := map[string]int{}
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), MaxSecretFileBytes)
	for n := 1; sc.Scan(); n++ {
		line := strings.TrimSpace(sc.Text())
		if n == 1 {
			line = strings.TrimPrefix(line, "\ufeff")
		}
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		i := strings.IndexByte(line, '=')
		if i < 0 {
			return nil, fmt.Errorf("line %d: expected KEY=VALUE", n)
		}
		key := strings.TrimSpace(line[:i])
		if err := ValidateVarName(key); err != nil {
			return nil, fmt.Errorf("line %d: %w", n, err)
		}
		val := unquote(strings.TrimSpace(line[i+1:]))
		if j, ok := index[key]; ok {
			out[j].Value = val
			continue
		}
		index[key] = len(out)
		out = append(out, Pair{key, val})
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func unquote(v string) string {
	if len(v) >= 2 {
		q := v[0]
		if (q == '"' || q == '\'') && v[len(v)-1] == q {
			return v[1 : len(v)-1]
		}
	}
	return v
}

// ValidateSecretPath checks a secret file path: relative to the machine home,
// no "..", no empty segments, no backslashes, no control characters.
func ValidateSecretPath(p string) error {
	if p == "" {
		return errors.New("secret file path is empty")
	}
	if strings.HasPrefix(p, "/") {
		return fmt.Errorf("secret file path %q must be relative to the home directory", p)
	}
	if strings.ContainsRune(p, '\\') {
		return fmt.Errorf("secret file path %q must not contain backslashes", p)
	}
	for _, r := range p {
		if r < 0x20 || r == 0x7f {
			return fmt.Errorf("secret file path %q contains a control character", p)
		}
	}
	for _, seg := range strings.Split(p, "/") {
		switch seg {
		case "":
			return fmt.Errorf("secret file path %q has an empty segment", p)
		case "..":
			return fmt.Errorf("secret file path %q must not contain '..'", p)
		}
	}
	return nil
}

// ValidateSecretContents checks a secret file's contents against the size cap.
func ValidateSecretContents(b []byte) error {
	if len(b) > MaxSecretFileBytes {
		return fmt.Errorf("secret file is larger than %d bytes (256KB)", MaxSecretFileBytes)
	}
	return nil
}

// ValidateRepo checks "owner/name".
func ValidateRepo(repo string) error {
	parts := strings.Split(repo, "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" || strings.ContainsAny(repo, " \t\\") {
		return fmt.Errorf("repository %q must look like owner/name", repo)
	}
	return nil
}

// ParseOnOff turns "on"/"off" (and true/false, 1/0, yes/no) into a bool.
func ParseOnOff(s string) (bool, error) {
	switch strings.ToLower(s) {
	case "on", "true", "1", "yes":
		return true, nil
	case "off", "false", "0", "no":
		return false, nil
	}
	return false, fmt.Errorf("expected on or off, got %q", clip(s))
}

// Mask hides a secret value completely; it never leaks any characters or the
// length.
func Mask(v string) string {
	if v == "" {
		return "(empty)"
	}
	return "********"
}

func clip(s string) string {
	if len(s) > 40 {
		return s[:40] + "..."
	}
	return s
}
