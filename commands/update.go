package commands

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/Miosa-osa/miosa-cli-go/internal/config"
)

const (
	releaseRepo = "Miosa-osa/miosa-cli-go"
	// The MIOSA download origin. It becomes the default ONLY after the
	// captain's acceptance curls pass; until then GitHub is the default and
	// this index is reachable through MIOSA_RELEASES_URL. To flip, set
	// defaultReleaseURL = miosaReleaseURL.
	miosaReleaseBase  = "https://dl.miosa.ai/cli"
	miosaReleaseURL   = miosaReleaseBase + "/latest.json"
	defaultReleaseURL = "https://api.github.com/repos/" + releaseRepo + "/releases/latest"
	installScriptURL  = "https://raw.githubusercontent.com/" + releaseRepo + "/main/install.sh"
	updateCheckEvery  = 24 * time.Hour
	maxBinaryBytes    = 200 << 20
)

// releasesURL is where the latest release is read from. MIOSA_RELEASES_URL
// overrides it (used by tests and by mirrors).
func releasesURL() string {
	if v := os.Getenv("MIOSA_RELEASES_URL"); v != "" {
		return v
	}
	return defaultReleaseURL
}

type releaseAsset struct {
	Name string `json:"name"`
	URL  string `json:"browser_download_url"`
}

type release struct {
	Tag    string         `json:"tag_name"`
	Assets []releaseAsset `json:"assets"`
	// Sums is the per-file SHA-256 from a latest.json index. Empty for the
	// GitHub shape, where checksums.txt is fetched instead.
	Sums map[string]string `json:"-"`
}

func (r release) Version() string { return strings.TrimPrefix(r.Tag, "v") }

func (r release) asset(name string) (releaseAsset, bool) {
	for _, a := range r.Assets {
		if a.Name == name {
			return a, true
		}
	}
	return releaseAsset{}, false
}

// sha returns the digest the index published for name, or "".
func (r release) sha(name string) string { return r.Sums[name] }

// releaseIndex is the MIOSA latest.json shape.
type releaseIndex struct {
	Version string `json:"version"`
	Assets  map[string]struct {
		File   string `json:"file"`
		SHA256 string `json:"sha256"`
		Size   int64  `json:"size"`
	} `json:"assets"`
}

// releaseFromIndex turns latest.json into the shape the rest of update uses.
func releaseFromIndex(url string, idx releaseIndex) (release, error) {
	version := strings.TrimPrefix(strings.TrimSpace(idx.Version), "v")
	if version == "" {
		return release{}, errors.New("release index has no version")
	}
	base := strings.TrimSuffix(url, "/latest.json")
	if base == url {
		base = strings.TrimSuffix(url, "/")
	}
	rel := release{Tag: "v" + version, Sums: map[string]string{}}
	for _, a := range idx.Assets {
		if a.File == "" {
			continue
		}
		rel.Assets = append(rel.Assets, releaseAsset{Name: a.File, URL: base + "/v" + version + "/" + a.File})
		if a.SHA256 != "" {
			rel.Sums[a.File] = strings.ToLower(a.SHA256)
		}
	}
	rel.Assets = append(rel.Assets, releaseAsset{Name: "checksums.txt", URL: base + "/v" + version + "/checksums.txt"})
	return rel, nil
}

// releaseForVersion builds a release for one version without an index, so
// `miosa update --version` still works when the index is unavailable.
func releaseForVersion(base, version string) (release, error) {
	v := strings.TrimPrefix(strings.TrimSpace(version), "v")
	if v == "" {
		return release{}, errors.New("no version given")
	}
	name := archiveName(v)
	return release{
		Tag: "v" + v,
		Assets: []releaseAsset{
			{Name: name, URL: base + "/v" + v + "/" + name},
			{Name: "checksums.txt", URL: base + "/v" + v + "/checksums.txt"},
		},
	}, nil
}

func fetchRelease(ctx context.Context, url string) (release, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return release{}, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "miosa-cli/"+cliVersion)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return release{}, fmt.Errorf("checking for a new release: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return release{}, fmt.Errorf("checking for a new release: %s", resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return release{}, fmt.Errorf("reading release metadata: %w", err)
	}

	// A latest.json index is tried first; a GitHub release response has no
	// "version" key and falls through to the original shape.
	var idx releaseIndex
	if json.Unmarshal(body, &idx) == nil && idx.Version != "" {
		return releaseFromIndex(url, idx)
	}
	var rel release
	if err := json.Unmarshal(body, &rel); err != nil {
		return release{}, fmt.Errorf("reading release metadata: %w", err)
	}
	if rel.Tag == "" {
		return release{}, errors.New("release metadata has no tag")
	}
	return rel, nil
}

// compareVersions compares dotted numeric versions ("1.2.10" > "1.2.9").
// A leading "v" and any "-suffix" are ignored. It returns -1, 0 or 1.
func compareVersions(a, b string) int {
	pa, pb := versionParts(a), versionParts(b)
	for i := 0; i < len(pa) || i < len(pb); i++ {
		var x, y int
		if i < len(pa) {
			x = pa[i]
		}
		if i < len(pb) {
			y = pb[i]
		}
		if x != y {
			if x < y {
				return -1
			}
			return 1
		}
	}
	return 0
}

func versionParts(v string) []int {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	if i := strings.IndexAny(v, "-+"); i >= 0 {
		v = v[:i]
	}
	var out []int
	for _, p := range strings.Split(v, ".") {
		n, err := strconv.Atoi(p)
		if err != nil {
			n = 0
		}
		out = append(out, n)
	}
	return out
}

func archiveName(version string) string {
	return fmt.Sprintf("miosa_%s_%s_%s.tar.gz", version, runtime.GOOS, runtime.GOARCH)
}

func download(ctx context.Context, url string, limit int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "miosa-cli/"+cliVersion)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download %s: %s", filepath.Base(url), resp.Status)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("download %s: larger than %d bytes", filepath.Base(url), limit)
	}
	return data, nil
}

// checksumFor finds name in a checksums.txt body ("<sha256>  <file>").
func checksumFor(sums []byte, name string) (string, bool) {
	for _, line := range strings.Split(string(sums), "\n") {
		f := strings.Fields(line)
		if len(f) == 2 && strings.TrimPrefix(f[1], "*") == name {
			return strings.ToLower(f[0]), true
		}
	}
	return "", false
}

// extractBinary returns the "miosa" file from a .tar.gz.
func extractBinary(archive []byte) ([]byte, error) {
	gz, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		return nil, fmt.Errorf("opening archive: %w", err)
	}
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil, errors.New("archive has no miosa binary")
		}
		if err != nil {
			return nil, fmt.Errorf("reading archive: %w", err)
		}
		if h.Typeflag == tar.TypeReg && filepath.Base(h.Name) == "miosa" {
			data, err := io.ReadAll(io.LimitReader(tr, maxBinaryBytes+1))
			if err != nil {
				return nil, err
			}
			if len(data) > maxBinaryBytes {
				return nil, errors.New("binary is unexpectedly large")
			}
			return data, nil
		}
	}
}

// selfPath is the running executable; tests replace it.
var selfPath = func() (string, error) {
	p, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(p)
}

// replaceBinary atomically swaps the file at path for data.
func replaceBinary(path string, data []byte) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".miosa-update-*")
	if err != nil {
		return fmt.Errorf("cannot write to %s (%v); re-run with permission or reinstall with INSTALL_DIR", dir, err)
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), 0o755); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// npmInstallEnv is set by the @miosa/cli npm shim when it launches the binary.
// It marks an npm-managed copy even when the package manager keeps the files
// somewhere the path check cannot recognise.
const npmInstallEnv = "MIOSA_INSTALL_METHOD"

// packageManaged reports whether this copy belongs to a package manager, which
// then owns upgrades. It returns the manager's name and the command to run.
func packageManaged(path string) (manager, hint string, managed bool) {
	slashed := filepath.ToSlash(path)
	for _, m := range []string{"/Cellar/", "/homebrew/", "/Caskroom/"} {
		if strings.Contains(slashed, m) {
			return "Homebrew", "brew upgrade miosa", true
		}
	}
	for _, m := range []string{"/node_modules/@miosa/cli/", "/node_modules/@miosa/cli-"} {
		if strings.Contains(slashed, m) {
			return "npm", "npm install -g @miosa/cli@latest", true
		}
	}
	if os.Getenv(npmInstallEnv) == "npm" {
		return "npm", "npm install -g @miosa/cli@latest", true
	}
	return "", "", false
}

func newUpdateCmd() *cobra.Command {
	var checkOnly bool
	var target string
	var force bool
	cmd := &cobra.Command{
		Use:     "update",
		Aliases: []string{"upgrade", "self-update"},
		Short:   "Update the miosa CLI to the latest release",
		Long: `Download the latest release for this platform, verify it against its SHA-256
checksum, and replace the running binary.

If miosa was installed with Homebrew or npm, run the package manager instead;
update prints the command.

  miosa update            # install the latest release
  miosa update --check    # only report whether a newer release exists
  miosa update --version 1.2.4

First install or a CI image: use the install script at ` + installScriptURL + `.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runUpdate(cmd, checkOnly, target, force)
		},
	}
	cmd.Flags().BoolVar(&checkOnly, "check", false, "Only check; do not install")
	cmd.Flags().StringVar(&target, "version", "", "Install this version instead of the latest")
	cmd.Flags().BoolVar(&force, "force", false, "Reinstall even when already up to date")
	return cmd
}

func runUpdate(cmd *cobra.Command, checkOnly bool, target string, force bool) error {
	p := printerFor(cmd)
	ctx, cancel := context.WithTimeout(cmd.Context(), 5*time.Minute)
	defer cancel()

	url := releasesURL()
	var rel release
	var err error
	switch {
	case target != "" && strings.HasSuffix(url, "/latest.json"):
		// Build the version's URLs directly; no index needed.
		rel, err = releaseForVersion(strings.TrimSuffix(url, "/latest.json"), target)
	case target != "":
		url = strings.Replace(url, "/releases/latest", "/releases/tags/v"+strings.TrimPrefix(target, "v"), 1)
		rel, err = fetchRelease(ctx, url)
	default:
		rel, err = fetchRelease(ctx, url)
		if err != nil && os.Getenv("MIOSA_RELEASES_URL") == "" && url == miosaReleaseURL {
			// GitHub is the fallback once MIOSA is the default.
			rel, err = fetchRelease(ctx, defaultReleaseURL)
		}
	}
	if err != nil {
		return die(err)
	}
	latest := rel.Version()
	newer := compareVersions(latest, cliVersion) > 0
	if cliVersion == "dev" {
		newer = true
	}
	status := map[string]any{
		"current": cliVersion, "latest": latest, "update_available": newer,
	}
	if checkOnly {
		if isJSON() {
			return p.JSON(status)
		}
		if newer {
			p.Line("miosa %s is available (you have %s). Run 'miosa update'.", latest, cliVersion)
		} else {
			p.Line("miosa %s is up to date.", cliVersion)
		}
		return nil
	}
	if !newer && !force && target == "" {
		if isJSON() {
			status["updated"] = false
			return p.JSON(status)
		}
		p.Line("miosa %s is up to date.", cliVersion)
		return nil
	}

	path, err := selfPath()
	if err != nil {
		return die(fmt.Errorf("locating the running binary: %w", err))
	}
	if manager, hint, ok := packageManaged(path); ok {
		return die(fmt.Errorf("this copy is managed by %s; run: %s", manager, hint))
	}

	name := archiveName(latest)
	asset, ok := rel.asset(name)
	if !ok {
		return die(fmt.Errorf("release %s has no build for %s/%s (%s)", rel.Tag, runtime.GOOS, runtime.GOARCH, name))
	}
	sumsAsset, ok := rel.asset("checksums.txt")
	if !ok {
		return die(errors.New("release has no checksums.txt; refusing to install an unverified binary"))
	}
	if !isJSON() {
		p.Line("Downloading miosa %s (%s/%s)...", latest, runtime.GOOS, runtime.GOARCH)
	}
	want := rel.sha(name)
	if want == "" {
		sums, err := download(ctx, sumsAsset.URL, 1<<20)
		if err != nil {
			return die(err)
		}
		var ok bool
		want, ok = checksumFor(sums, name)
		if !ok {
			return die(fmt.Errorf("checksums.txt has no entry for %s", name))
		}
	}
	archive, err := download(ctx, asset.URL, maxBinaryBytes)
	if err != nil {
		return die(err)
	}
	got := sha256.Sum256(archive)
	if hex.EncodeToString(got[:]) != want {
		return die(fmt.Errorf("checksum mismatch for %s; not installing", name))
	}
	bin, err := extractBinary(archive)
	if err != nil {
		return die(err)
	}
	if err := replaceBinary(path, bin); err != nil {
		return die(err)
	}
	status["updated"] = true
	status["path"] = path
	if isJSON() {
		return p.JSON(status)
	}
	p.Success("Updated miosa %s -> %s (%s)", cliVersion, latest, path)
	return nil
}

// ─── passive version notice ──────────────────────────────────────────────────

type updateCache struct {
	CheckedAt time.Time `json:"checked_at"`
	Latest    string    `json:"latest"`
}

func updateCachePath() (string, error) {
	dir, err := config.Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "cli-update-check.json"), nil
}

// maybeNotifyUpdate prints one line to w when a newer release exists. It is
// silent for dev builds, JSON output, non-terminals, CI, and when disabled by
// --no-update-check or MIOSA_NO_UPDATE_CHECK. The network is touched at most
// once per day and never for longer than two seconds.
func maybeNotifyUpdate(w io.Writer) {
	if cliVersion == "dev" || globalFlags.NoUpdateCheck || isJSON() || globalFlags.Quiet {
		return
	}
	if (os.Getenv("MIOSA_NO_UPDATE_NOTICE") != "" || os.Getenv("MIOSA_NO_UPDATE_CHECK") != "") || os.Getenv("CI") != "" {
		return
	}
	if f, ok := w.(*os.File); !ok || !term.IsTerminal(int(f.Fd())) {
		return
	}
	path, err := updateCachePath()
	if err != nil {
		return
	}
	var c updateCache
	if data, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(data, &c)
	}
	if time.Since(c.CheckedAt) > updateCheckEvery {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		rel, err := fetchRelease(ctx, releasesURL())
		if err != nil {
			c.CheckedAt = time.Now() // back off a day on failure too
		} else {
			c = updateCache{CheckedAt: time.Now(), Latest: rel.Version()}
		}
		if data, err := json.Marshal(c); err == nil {
			_ = os.MkdirAll(filepath.Dir(path), 0o700)
			_ = os.WriteFile(path, data, 0o600)
		}
	}
	if c.Latest != "" && compareVersions(c.Latest, cliVersion) > 0 {
		upgrade := "miosa update"
		if p, err := selfPath(); err == nil {
			if _, hint, ok := packageManaged(p); ok {
				upgrade = hint
			}
		}
		fmt.Fprintf(w, "\nA new miosa release is available: %s -> %s (run '%s')\n", cliVersion, c.Latest, upgrade)
	}
}
