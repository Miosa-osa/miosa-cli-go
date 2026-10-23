package commands_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/Miosa-osa/miosa-cli-go/commands"
)

func tarball(t *testing.T, name string, data []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	tw.WriteHeader(&tar.Header{Name: "README.md", Mode: 0o644, Size: 4, Typeflag: tar.TypeReg})
	tw.Write([]byte("docs"))
	tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(data)), Typeflag: tar.TypeReg})
	tw.Write(data)
	tw.Close()
	gz.Close()
	return buf.Bytes()
}

// releaseServer serves a GitHub-shaped latest release with one archive for
// this platform. corrupt makes the checksum disagree with the archive.
func releaseServer(t *testing.T, tag string, binary []byte, corrupt bool) string {
	t.Helper()
	version := strings.TrimPrefix(tag, "v")
	archiveName := fmt.Sprintf("miosa_%s_%s_%s.tar.gz", version, runtime.GOOS, runtime.GOARCH)
	archive := tarball(t, "miosa", binary)
	sum := sha256.Sum256(archive)
	hexSum := hex.EncodeToString(sum[:])
	if corrupt {
		hexSum = strings.Repeat("0", 64)
	}
	var base string
	srv := plainServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/releases/latest", "/releases/tags/" + tag:
			json.NewEncoder(w).Encode(map[string]any{"tag_name": tag, "assets": []map[string]string{
				{"name": archiveName, "browser_download_url": base + "/dl/" + archiveName},
				{"name": "checksums.txt", "browser_download_url": base + "/dl/checksums.txt"},
			}})
		case "/dl/" + archiveName:
			w.Write(archive)
		case "/dl/checksums.txt":
			fmt.Fprintf(w, "%s  %s\n%s  other.tar.gz\n", hexSum, archiveName, strings.Repeat("a", 64))
		default:
			http.NotFound(w, r)
		}
	})
	base = srv.URL
	t.Setenv("MIOSA_RELEASES_URL", srv.URL+"/releases/latest")
	return srv.URL
}

func TestUpdateCheckReportsNewerRelease(t *testing.T) {
	releaseServer(t, "v1.3.0", []byte("x"), false)
	defer commands.SetVersionForTest("1.2.4")()
	out, err := run(t, "update", "--check")
	if err != nil || !strings.Contains(out, "1.3.0 is available") {
		t.Fatalf("%v %s", err, out)
	}
	jout, _ := run(t, "update", "--check", "--json")
	var st map[string]any
	if json.Unmarshal(dataJSON(jout), &st) != nil || st["update_available"] != true || st["latest"] != "1.3.0" {
		t.Fatalf("json = %s", jout)
	}
}

func TestUpdateUpToDate(t *testing.T) {
	releaseServer(t, "v1.2.4", []byte("x"), false)
	defer commands.SetVersionForTest("1.2.4")()
	out, err := run(t, "update")
	if err != nil || !strings.Contains(out, "up to date") {
		t.Fatalf("%v %s", err, out)
	}
}

func TestUpdateInstallsVerifiedBinary(t *testing.T) {
	releaseServer(t, "v1.3.0", []byte("NEW-BINARY"), false)
	defer commands.SetVersionForTest("1.2.4")()
	target := filepath.Join(t.TempDir(), "miosa")
	os.WriteFile(target, []byte("OLD"), 0o755)
	defer commands.SetSelfPathForTest(target)()

	out, err := run(t, "update")
	if err != nil {
		t.Fatalf("update: %v\n%s", err, out)
	}
	got, _ := os.ReadFile(target)
	if string(got) != "NEW-BINARY" {
		t.Fatalf("binary = %q", got)
	}
	fi, _ := os.Stat(target)
	if fi.Mode().Perm()&0o100 == 0 {
		t.Fatal("installed binary must be executable")
	}
	left, _ := filepath.Glob(filepath.Join(filepath.Dir(target), ".miosa-update-*"))
	if len(left) != 0 {
		t.Fatalf("temp files left behind: %v", left)
	}
}

func TestUpdateRefusesOnChecksumMismatch(t *testing.T) {
	releaseServer(t, "v1.3.0", []byte("EVIL"), true)
	defer commands.SetVersionForTest("1.2.4")()
	target := filepath.Join(t.TempDir(), "miosa")
	os.WriteFile(target, []byte("OLD"), 0o755)
	defer commands.SetSelfPathForTest(target)()

	_, err := run(t, "update")
	if err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("err = %v", err)
	}
	if got, _ := os.ReadFile(target); string(got) != "OLD" {
		t.Fatalf("binary was replaced despite a bad checksum: %q", got)
	}
}

func TestUpdateRefusesHomebrewManagedCopy(t *testing.T) {
	releaseServer(t, "v1.3.0", []byte("x"), false)
	defer commands.SetVersionForTest("1.2.4")()
	defer commands.SetSelfPathForTest("/opt/homebrew/Cellar/miosa/1.2.4/bin/miosa")()
	_, err := run(t, "update")
	if err == nil || !strings.Contains(err.Error(), "brew upgrade miosa") {
		t.Fatalf("err = %v", err)
	}
}

func TestUpdateRefusesNpmManagedCopy(t *testing.T) {
	for _, path := range []string{
		"/usr/lib/node_modules/@miosa/cli-linux-x64/bin/miosa",
		"/home/u/.pnpm/node_modules/@miosa/cli/vendor/miosa",
	} {
		releaseServer(t, "v1.3.0", []byte("x"), false)
		restoreV := commands.SetVersionForTest("1.2.4")
		restoreP := commands.SetSelfPathForTest(path)
		_, err := run(t, "update")
		restoreP()
		restoreV()
		if err == nil || !strings.Contains(err.Error(), "npm install -g @miosa/cli@latest") {
			t.Fatalf("%s: err = %v", path, err)
		}
	}
}

func TestUpdateRefusesWhenShimMarksNpmInstall(t *testing.T) {
	releaseServer(t, "v1.3.0", []byte("x"), false)
	t.Setenv("MIOSA_INSTALL_METHOD", "npm")
	defer commands.SetVersionForTest("1.2.4")()
	defer commands.SetSelfPathForTest("/somewhere/else/miosa")()
	_, err := run(t, "update")
	if err == nil || !strings.Contains(err.Error(), "managed by npm") {
		t.Fatalf("err = %v", err)
	}
}

func TestUpdateRefusesWithoutChecksums(t *testing.T) {
	var base string
	srv := plainServer(t, func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"tag_name": "v9.9.9", "assets": []map[string]string{
			{"name": fmt.Sprintf("miosa_9.9.9_%s_%s.tar.gz", runtime.GOOS, runtime.GOARCH), "browser_download_url": base + "/x"},
		}})
	})
	base = srv.URL
	t.Setenv("MIOSA_RELEASES_URL", srv.URL+"/releases/latest")
	defer commands.SetVersionForTest("1.0.0")()
	defer commands.SetSelfPathForTest(filepath.Join(t.TempDir(), "miosa"))()
	_, err := run(t, "update")
	if err == nil || !strings.Contains(err.Error(), "checksums.txt") {
		t.Fatalf("err = %v", err)
	}
}

func TestUpgradeIsAnAliasOfUpdate(t *testing.T) {
	releaseServer(t, "v1.2.4", []byte("x"), false)
	defer commands.SetVersionForTest("1.2.4")()
	if out, err := run(t, "upgrade"); err != nil || !strings.Contains(out, "up to date") {
		t.Fatalf("%v %s", err, out)
	}
}

func TestCompareVersions(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"1.2.10", "1.2.9", 1}, {"1.2.4", "1.2.4", 0}, {"v1.3.0", "1.2.9", 1},
		{"1.2", "1.2.0", 0}, {"2.0.0-rc1", "1.9.9", 1}, {"1.0.0", "1.0.1", -1},
	}
	for _, tc := range cases {
		if got := commands.CompareVersionsForTest(tc.a, tc.b); got != tc.want {
			t.Errorf("compare(%s,%s) = %d, want %d", tc.a, tc.b, got, tc.want)
		}
	}
}

func TestVersionNoticeSilentWhenNotATerminal(t *testing.T) {
	releaseServer(t, "v9.0.0", []byte("x"), false)
	defer commands.SetVersionForTest("1.0.0")()
	var buf bytes.Buffer
	commands.MaybeNotifyUpdateForTest(&buf)
	if buf.Len() != 0 {
		t.Fatalf("notice must not print to a non-terminal: %q", buf.String())
	}
}

func TestInstallScriptURLUsesPublishedInstaller(t *testing.T) {
	const want = "https://raw.githubusercontent.com/Miosa-osa/miosa-cli-go/main/install.sh"
	if commands.InstallScriptURL != want {
		t.Fatalf("install script URL = %q, want %q", commands.InstallScriptURL, want)
	}
}

func TestVersionFlagPrintsVersion(t *testing.T) {
	out, err := run(t, "--version")
	if err != nil || !strings.HasPrefix(out, "miosa version ") {
		t.Fatalf("out = %q err = %v", out, err)
	}
}

// The MIOSA release index carries the version and a per-file SHA-256. Update
// must read it, build the asset URLs from it, and verify against the index
// digest without depending on checksums.txt.
func TestUpdateReadsTheMiosaIndex(t *testing.T) {
	version := "9.9.9"
	name := fmt.Sprintf("miosa_%s_%s_%s.tar.gz", version, runtime.GOOS, runtime.GOARCH)
	archive := tarball(t, "miosa", []byte("NEW-FROM-MIOSA"))
	sum := sha256.Sum256(archive)
	hexSum := hex.EncodeToString(sum[:])
	srv := plainServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/latest.json":
			json.NewEncoder(w).Encode(map[string]any{
				"version": version,
				"assets": map[string]any{
					"current": map[string]any{"file": name, "sha256": hexSum, "size": len(archive)},
				},
			})
		case "/v" + version + "/" + name:
			w.Write(archive)
		case "/v" + version + "/checksums.txt":
			// Deliberately absent: the index digest is enough.
			http.NotFound(w, r)
		default:
			http.NotFound(w, r)
		}
	})
	t.Setenv("MIOSA_RELEASES_URL", srv.URL+"/latest.json")
	defer commands.SetVersionForTest("1.2.4")()

	out, err := run(t, "update", "--check")
	if err != nil || !strings.Contains(out, version+" is available") {
		t.Fatalf("check: %v %s", err, out)
	}

	target := filepath.Join(t.TempDir(), "miosa")
	os.WriteFile(target, []byte("OLD"), 0o755)
	defer commands.SetSelfPathForTest(target)()
	if _, err := run(t, "update"); err != nil {
		t.Fatalf("update: %v", err)
	}
	got, _ := os.ReadFile(target)
	if string(got) != "NEW-FROM-MIOSA" {
		t.Fatalf("binary = %q", got)
	}
}

func TestUpdateRefusesATamperedIndexDigest(t *testing.T) {
	version := "9.9.8"
	name := fmt.Sprintf("miosa_%s_%s_%s.tar.gz", version, runtime.GOOS, runtime.GOARCH)
	archive := tarball(t, "miosa", []byte("TAMPERED"))
	srv := plainServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/latest.json":
			json.NewEncoder(w).Encode(map[string]any{
				"version": version,
				"assets": map[string]any{
					"current": map[string]any{"file": name, "sha256": strings.Repeat("0", 64)},
				},
			})
		case "/v" + version + "/" + name:
			w.Write(archive)
		default:
			http.NotFound(w, r)
		}
	})
	t.Setenv("MIOSA_RELEASES_URL", srv.URL+"/latest.json")
	defer commands.SetVersionForTest("1.2.4")()
	target := filepath.Join(t.TempDir(), "miosa")
	os.WriteFile(target, []byte("OLD"), 0o755)
	defer commands.SetSelfPathForTest(target)()
	if _, err := run(t, "update"); err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("err = %v", err)
	}
	got, _ := os.ReadFile(target)
	if string(got) != "OLD" {
		t.Fatalf("binary was replaced: %q", got)
	}
}

// The MIOSA origin is not the default until the captain's acceptance curls
// pass. Flip this test together with defaultReleaseURL.
func TestReleaseDefaultStaysOnGitHubUntilTheOriginIsAccepted(t *testing.T) {
	if !strings.HasPrefix(commands.DefaultReleaseURL, "https://api.github.com/") {
		t.Fatalf("default release URL = %q; do not flip it before acceptance", commands.DefaultReleaseURL)
	}
}

func TestAutoUpdateInstallsOnceEverySixHoursWhenOptedIn(t *testing.T) {
	releaseServer(t, "v1.3.0", []byte("NEW-BINARY"), false)
	defer commands.SetVersionForTest("1.2.4")()
	target := filepath.Join(t.TempDir(), "miosa")
	os.WriteFile(target, []byte("OLD"), 0o755)
	defer commands.SetSelfPathForTest(target)()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("CI", "")

	var out bytes.Buffer
	commands.AutoUpdateForTest(&out)
	if got, _ := os.ReadFile(target); string(got) != "OLD" {
		t.Fatal("updated without opting in")
	}

	t.Setenv("MIOSA_AUTO_UPDATE", "1")
	commands.AutoUpdateForTest(&out)
	if got, _ := os.ReadFile(target); string(got) != "NEW-BINARY" {
		t.Fatalf("binary = %q", got)
	}
	if !strings.Contains(out.String(), "updated itself: 1.2.4 -> 1.3.0") {
		t.Fatalf("out = %q", out.String())
	}

	os.WriteFile(target, []byte("OLD"), 0o755)
	commands.AutoUpdateForTest(&out)
	if got, _ := os.ReadFile(target); string(got) != "OLD" {
		t.Fatal("tried again within six hours")
	}
}

func TestAutoUpdateOffForNoUpdateAndPackageManagers(t *testing.T) {
	releaseServer(t, "v1.3.0", []byte("NEW-BINARY"), false)
	defer commands.SetVersionForTest("1.2.4")()
	t.Setenv("MIOSA_AUTO_UPDATE", "1")
	t.Setenv("CI", "")

	target := filepath.Join(t.TempDir(), "miosa")
	os.WriteFile(target, []byte("OLD"), 0o755)
	defer commands.SetSelfPathForTest(target)()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("MIOSA_NO_UPDATE", "1")
	commands.AutoUpdateForTest(&bytes.Buffer{})
	if got, _ := os.ReadFile(target); string(got) != "OLD" {
		t.Fatal("MIOSA_NO_UPDATE did not stop the update")
	}
	t.Setenv("MIOSA_NO_UPDATE", "")

	t.Setenv("HOME", t.TempDir())
	brew := "/opt/homebrew/Cellar/miosa/1.2.4/bin/miosa"
	defer commands.SetSelfPathForTest(brew)()
	var out bytes.Buffer
	commands.AutoUpdateForTest(&out)
	if out.Len() != 0 {
		t.Fatalf("a Homebrew copy must be left to brew: %q", out.String())
	}
}

func TestConfigAutoUpdateKey(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if _, err := run(t, "config", "set", "auto_update", "on"); err != nil {
		t.Fatal(err)
	}
	out, err := run(t, "config", "get", "auto_update")
	if err != nil || strings.TrimSpace(out) != "on" {
		t.Fatalf("%q %v", out, err)
	}
}
