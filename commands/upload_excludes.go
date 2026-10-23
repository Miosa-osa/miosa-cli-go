package commands

import (
	"bufio"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// Uploads (`deploy create --dir`, `deploy push`) publish every file they
// package on a public URL. 2.3.9 shipped id_rsa, .npmrc (npm tokens) and
// .htpasswd that way (tester S1, 2026-10-16), so secrets are left out by
// default and each one is named on stderr. A `.miosaignore` at the upload root
// adds patterns (gitignore style: `#` comments, a trailing `/` for directories,
// `/` anchors to the root) and can put a default back with `!pattern`.

const miosaIgnoreFile = ".miosaignore"

// uploadRule is one exclude pattern; later rules win, as in .gitignore.
type uploadRule struct {
	pattern string
	dirOnly bool
	negate  bool
	why     string
}

var defaultUploadRules = []uploadRule{
	{pattern: ".git", dirOnly: true, why: "git metadata"},
	{pattern: deploymentLinkDir, dirOnly: true, why: "MIOSA CLI state"},
	{pattern: ".miosa", dirOnly: true, why: "MIOSA CLI state"},
	{pattern: ".ssh", dirOnly: true, why: "SSH keys"},
	{pattern: ".aws", dirOnly: true, why: "cloud credentials"},
	{pattern: ".gnupg", dirOnly: true, why: "GPG keys"},
	{pattern: "id_rsa*", why: "private key"},
	{pattern: "id_ed25519*", why: "private key"},
	{pattern: "id_ecdsa*", why: "private key"},
	{pattern: "id_dsa*", why: "private key"},
	{pattern: "*.pem", why: "private key or certificate"},
	{pattern: "*.key", why: "private key"},
	{pattern: "*.p12", why: "private key bundle"},
	{pattern: "*.pfx", why: "private key bundle"},
	{pattern: ".npmrc", why: "package registry token"},
	{pattern: ".pypirc", why: "package registry token"},
	{pattern: ".netrc", why: "credentials"},
	{pattern: ".htpasswd", why: "password file"},
	{pattern: ".env", why: "environment secrets"},
	{pattern: ".env.*", why: "environment secrets"},
	{pattern: ".DS_Store", why: "macOS metadata"},
	{pattern: miosaIgnoreFile, why: "upload rules"},
}

// uploadFilter decides which paths an upload leaves out.
type uploadFilter struct {
	rules []uploadRule
}

// newUploadFilter is the default rules plus the root's .miosaignore, if any.
func newUploadFilter(root string) (*uploadFilter, error) {
	extra, err := readIgnoreRules(filepath.Join(root, miosaIgnoreFile), miosaIgnoreFile)
	if err != nil {
		return nil, err
	}
	return &uploadFilter{rules: append(append([]uploadRule(nil), defaultUploadRules...), extra...)}, nil
}

// readIgnoreRules reads gitignore-style patterns from file (none when it does
// not exist): `#` comments, `!` to put a path back, a trailing `/` for
// directories, `/` anchoring to the root, and `**/` for any depth.
func readIgnoreRules(file, why string) ([]uploadRule, error) {
	f, err := os.Open(file)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var rules []uploadRule
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		rule := uploadRule{why: why}
		if strings.HasPrefix(line, "!") {
			rule.negate = true
			line = strings.TrimPrefix(line, "!")
		}
		if strings.HasSuffix(line, "/**") {
			rule.dirOnly = true
			line = strings.TrimSuffix(line, "/**")
		}
		if strings.HasSuffix(line, "/") {
			rule.dirOnly = true
			line = strings.TrimSuffix(line, "/")
		}
		// "**/x" matches x at any depth, which is what a pattern with no
		// inner slash already does.
		for strings.HasPrefix(line, "**/") {
			line = strings.TrimPrefix(line, "**/")
		}
		if line == "" {
			continue
		}
		rule.pattern = line
		rules = append(rules, rule)
	}
	return rules, sc.Err()
}

// newPushFilter is what `miosa push` leaves out: the .git directory and what
// the root's .gitignore and .miosaignore name. Secrets are not skipped by
// default as they are for a public upload: the sandbox is the user's own.
func newPushFilter(root string) (*uploadFilter, error) {
	rules := []uploadRule{{pattern: ".git", dirOnly: true, why: "git metadata"}}
	for _, name := range []string{".gitignore", miosaIgnoreFile} {
		extra, err := readIgnoreRules(filepath.Join(root, name), name)
		if err != nil {
			return nil, err
		}
		rules = append(rules, extra...)
	}
	return &uploadFilter{rules: rules}, nil
}

// skip reports whether rel (slash-separated, relative to the upload root) is
// left out, and why. The last matching rule wins.
func (u *uploadFilter) skip(rel string, isDir bool) (bool, string) {
	skipped, why := false, ""
	for _, r := range u.rules {
		if r.dirOnly && !isDir {
			continue
		}
		if !r.matches(rel) {
			continue
		}
		skipped, why = !r.negate, r.why
	}
	return skipped, why
}

func (r uploadRule) matches(rel string) bool {
	if strings.HasPrefix(r.pattern, "/") {
		ok, _ := path.Match(strings.TrimPrefix(r.pattern, "/"), rel)
		return ok
	}
	if strings.Contains(r.pattern, "/") {
		ok, _ := path.Match(r.pattern, rel)
		return ok
	}
	ok, _ := path.Match(r.pattern, path.Base(rel))
	return ok
}

// skippedPath is one path an upload left out.
type skippedPath struct {
	Path string
	Why  string
	Dir  bool
}
