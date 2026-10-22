// Package vercel reads a vercel.json and turns it into the same plan vocabulary
// the Railway importer uses, so one applier serves both.
package vercel

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/Miosa-osa/miosa-cli-go/internal/railway"
)

// Config is the part of vercel.json this importer understands. Everything it
// does not understand is reported as unmapped, never silently dropped.
type Config struct {
	Framework       string            `json:"framework"`
	BuildCommand    string            `json:"buildCommand"`
	InstallCommand  string            `json:"installCommand"`
	OutputDirectory string            `json:"outputDirectory"`
	Regions         []string          `json:"regions"`
	CleanUrls       bool              `json:"cleanUrls"`
	TrailingSlash   bool              `json:"trailingSlash"`
	Rewrites        []json.RawMessage `json:"rewrites"`
	Redirects       []json.RawMessage `json:"redirects"`
	Headers         []json.RawMessage `json:"headers"`
	Builds          []json.RawMessage `json:"builds"`
	Functions       json.RawMessage   `json:"functions"`
	Crons           []Cron            `json:"crons"`
	Env             map[string]any    `json:"env"`
}

// Cron is one scheduled path.
type Cron struct {
	Path     string `json:"path"`
	Schedule string `json:"schedule"`
}

// Options are the caller's choices.
type Options struct {
	ProjectName string // MIOSA project name
	ServiceName string // service inside the project (default: the project's)
	Repository  string // git URL for the service source, when known
	Region      string // us-east, us-west, us-mia; overrides the file
	WorkspaceID string
	FetchedAt   time.Time
}

// Parse reads a vercel.json body.
func Parse(data []byte) (Config, error) {
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return Config{}, fmt.Errorf("vercel.json is not valid JSON: %w", err)
	}
	return cfg, nil
}

// Plan turns a vercel.json into the shared plan vocabulary.
//
// What a Vercel project maps onto: one Services project, its `production`
// Deploy target, one service, and the environment variable names. What it does
// not: `rewrites`, `redirects`, `headers`, `builds`, the `functions` block,
// `cleanUrls` and `trailingSlash`, which have no Services equivalent today and
// are listed as unmapped with the reason.
func Plan(cfg Config, opts Options) railway.Plan {
	name := strings.TrimSpace(opts.ProjectName)
	if name == "" {
		name = "vercel-app"
	}
	service := strings.TrimSpace(opts.ServiceName)
	if service == "" {
		service = "web"
	}

	actions := []railway.Action{
		{Op: "target.create", Name: "production"},
		{
			Op:   "service.create",
			Name: service,
			Kind: "server",
			Source: &railway.ServiceSource{
				Type: "git",
				Ref:  opts.Repository,
			},
			Settings: settings(cfg, opts),
		},
	}

	names := make([]string, 0, len(cfg.Env))
	for key := range cfg.Env {
		names = append(names, key)
	}
	sort.Strings(names)
	for _, key := range names {
		actions = append(actions, railway.Action{
			Op:       "variable.set",
			Name:     service,
			Service:  service,
			Variable: &railway.Variable{Name: key},
		})
	}

	warnings := []string{}
	if opts.Repository == "" {
		warnings = append(warnings,
			"the service has no source yet: pass --repo <git url>, or set it on the service after the import")
	}

	return railway.Plan{
		Version: railway.PlanVersion,
		Source: railway.PlanSource{
			Kind:        "vercel",
			ProjectName: name,
			FetchedAt:   opts.FetchedAt.UTC().Format(time.RFC3339),
			Digest:      digest(cfg, opts),
		},
		Target: railway.PlanTarget{
			WorkspaceID: opts.WorkspaceID,
			ProjectName: name,
		},
		Actions:  append(actions, unmapped(cfg)...),
		Warnings: warnings,
	}
}

func settings(cfg Config, opts Options) *railway.ServiceSetting {
	out := &railway.ServiceSetting{}
	if region := strings.TrimSpace(opts.Region); region != "" {
		out.Region = region
	} else if len(cfg.Regions) == 1 {
		out.Region = cfg.Regions[0]
	}
	return out
}

func unmapped(cfg Config) []railway.Action {
	var out []railway.Action

	add := func(name, reason string) {
		out = append(out, railway.Action{Op: "unmapped", Field: name, Why: reason})
	}

	if len(cfg.Rewrites) > 0 {
		add("rewrites", "no path-rewrite rule on Services yet; handle it in the app or a proxy")
	}
	if len(cfg.Redirects) > 0 {
		add("redirects", "no redirect rule on Services yet; handle it in the app")
	}
	if len(cfg.Headers) > 0 {
		add("headers", "response headers are set by the app, not by a platform rule, today")
	}
	if len(cfg.Builds) > 0 {
		add("builds", "Services builds from a Dockerfile or a detected framework, not a builds list")
	}
	if len(cfg.Functions) > 0 && string(cfg.Functions) != "null" {
		add("functions", "per-function runtime config has no Services equivalent")
	}
	if cfg.CleanUrls {
		add("cleanUrls", "no URL normalisation rule yet")
	}
	if cfg.TrailingSlash {
		add("trailingSlash", "no URL normalisation rule yet")
	}
	if cfg.BuildCommand != "" {
		add("buildCommand", "the build runs from the Dockerfile; put the command there")
	}
	if cfg.InstallCommand != "" {
		add("installCommand", "dependency install happens inside the build")
	}
	if cfg.OutputDirectory != "" {
		add("outputDirectory", "a static site is served from the build output, not a directory setting")
	}
	for _, cron := range cfg.Crons {
		add("crons "+cron.Path,
			fmt.Sprintf("create a cron service with schedule %q that calls %s", cron.Schedule, cron.Path))
	}

	return out
}

func digest(cfg Config, opts Options) string {
	body, _ := json.Marshal(cfg)
	sum := sha256.Sum256(append(body, []byte(opts.ProjectName+opts.Repository)...))
	return hex.EncodeToString(sum[:8])
}
