package commands

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/Miosa-osa/miosa-cli-go/internal/config"
)

type check struct {
	Name   string `json:"name"`
	Status string `json:"status"` // ok, warn, fail
	Detail string `json:"detail"`
}

func newDoctorCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Diagnose configuration, connectivity and authentication",
		Long: `Run quick checks and say what to fix: CLI version, config file, the API endpoint,
your key, and clock skew (a skewed clock breaks signed webhooks and tokens).
Exit status is non-zero when a check fails.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			p := printerFor(cmd)
			checks := runDoctor(cmd.Context())
			failed := false
			for _, c := range checks {
				if c.Status == "fail" {
					failed = true
				}
			}
			if isJSON() {
				if err := p.JSON(map[string]any{"checks": checks, "ok": !failed}); err != nil {
					return err
				}
			} else {
				rows := make([][]string, 0, len(checks))
				for _, c := range checks {
					rows = append(rows, []string{c.Status, c.Name, c.Detail})
				}
				p.Table([]string{"", "CHECK", "DETAIL"}, rows)
			}
			if failed {
				return &ExitError{Code: ExitGeneral}
			}
			return nil
		},
	}
}

func runDoctor(ctx context.Context) []check {
	var out []check
	add := func(name, status, format string, args ...any) {
		out = append(out, check{Name: name, Status: status, Detail: fmt.Sprintf(format, args...)})
	}

	add("cli", "ok", "miosa %s (%s/%s)", cliVersion, runtime.GOOS, runtime.GOARCH)
	if cliVersion != "dev" && os.Getenv("MIOSA_RELEASES_URL") == "" {
		rctx, cancel := context.WithTimeout(ctx, 3*time.Second)
		if rel, err := fetchRelease(rctx, releasesURL()); err == nil && compareVersions(rel.Version(), cliVersion) > 0 {
			add("version", "warn", "%s is available; run 'miosa update'", rel.Version())
		} else if err == nil {
			add("version", "ok", "up to date")
		}
		cancel()
	}

	path, _ := config.Path()
	if fi, err := os.Stat(path); err == nil {
		if fi.Mode().Perm()&0o077 != 0 {
			add("config", "warn", "%s is readable by others (mode %o); run: chmod 600 %s", path, fi.Mode().Perm(), path)
		} else {
			add("config", "ok", "%s", path)
		}
	} else {
		add("config", "warn", "no config file at %s (run 'miosa login' or set MIOSA_API_KEY)", path)
	}

	c, cfg, err := buildPublicClient()
	if err != nil {
		add("profile", "fail", "%v", err)
		return out
	}
	add("profile", "ok", "%s", profileLabel())
	if os.Getenv("MIOSA_API_KEY") != "" {
		add("key source", "ok", "MIOSA_API_KEY (overrides the profile key)")
	}
	_ = cfg

	root := strings.TrimRight(c.BaseURL, "/")
	healthURL := root + "/health"
	if u, perr := url.Parse(c.BaseURL); perr == nil && u.Host != "" {
		healthURL = u.Scheme + "://" + u.Host + "/health" // health lives at the host root
	}
	start := time.Now()
	resp, err := httpGet(ctx, healthURL)
	if err != nil {
		add("api", "fail", "cannot reach %s: %v", root, err)
		return out
	}
	latency := time.Since(start).Round(time.Millisecond)
	if resp.StatusCode >= 500 {
		add("api", "fail", "%s answered %d", root, resp.StatusCode)
	} else {
		add("api", "ok", "%s (%s)", root, latency)
	}
	if d := resp.Header.Get("Date"); d != "" {
		if t, err := http.ParseTime(d); err == nil {
			skew := time.Since(t)
			if skew < 0 {
				skew = -skew
			}
			if skew > time.Minute {
				add("clock", "warn", "local clock differs from the server by %s", skew.Round(time.Second))
			} else {
				add("clock", "ok", "in sync")
			}
		}
	}

	if c.Key == "" {
		add("auth", "fail", "not signed in (run 'miosa login' or set MIOSA_API_KEY)")
		return out
	}
	var cur struct {
		Name string `json:"name"`
		Slug string `json:"slug"`
	}
	if err := c.API.Get(ctx, "/platform/tenants/current", nil, &cur); err != nil {
		detail := friendlyMessage(err)
		add("auth", "fail", "%s", detail)
		return out
	}
	add("auth", "ok", "%s (%s), key %s", cur.Name, cur.Slug, maskKey(c.Key))
	return out
}

func httpGet(ctx context.Context, target string) (*http.Response, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "miosa-cli/"+cliVersion)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	resp.Body.Close()
	return resp, nil
}
