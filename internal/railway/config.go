package railway

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"
)

// FileConfig is Railway's config-as-code (railway.json or railway.toml).
// Only the keys we can carry are typed; everything else is reported by BuildFile.
type FileConfig struct {
	Schema string       `json:"$schema" toml:"-"`
	Build  BuildConfig  `json:"build" toml:"build"`
	Deploy DeployConfig `json:"deploy" toml:"deploy"`
}

// BuildConfig is the build half of a Railway config file.
type BuildConfig struct {
	Builder        string   `json:"builder" toml:"builder"`
	DockerfilePath string   `json:"dockerfilePath" toml:"dockerfilePath"`
	BuildCommand   string   `json:"buildCommand" toml:"buildCommand"`
	WatchPatterns  []string `json:"watchPatterns" toml:"watchPatterns"`
}

// DeployConfig is the deploy half of a Railway config file.
type DeployConfig struct {
	StartCommand            string   `json:"startCommand" toml:"startCommand"`
	HealthcheckPath         string   `json:"healthcheckPath" toml:"healthcheckPath"`
	HealthcheckTimeout      int      `json:"healthcheckTimeout" toml:"healthcheckTimeout"`
	RestartPolicyType       string   `json:"restartPolicyType" toml:"restartPolicyType"`
	RestartPolicyMaxRetries int      `json:"restartPolicyMaxRetries" toml:"restartPolicyMaxRetries"`
	NumReplicas             int      `json:"numReplicas" toml:"numReplicas"`
	Region                  string   `json:"region" toml:"region"`
	CronSchedule            string   `json:"cronSchedule" toml:"cronSchedule"`
	SleepApplication        bool     `json:"sleepApplication" toml:"sleepApplication"`
	PreDeployCommand        []string `json:"preDeployCommand" toml:"preDeployCommand"`
}

// ReadFile reads a railway.json or railway.toml.
func ReadFile(path string) (FileConfig, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return FileConfig{}, err
	}
	var cfg FileConfig
	switch strings.ToLower(filepath.Ext(path)) {
	case ".json":
		if err := json.Unmarshal(body, &cfg); err != nil {
			return FileConfig{}, fmt.Errorf("%s: %w", path, err)
		}
	case ".toml":
		if err := toml.Unmarshal(body, &cfg); err != nil {
			return FileConfig{}, fmt.Errorf("%s: %w", path, err)
		}
	default:
		return FileConfig{}, fmt.Errorf("%s: name the file railway.json or railway.toml", path)
	}
	return cfg, nil
}

// BuildFile turns a config file into a one-service plan. It invents no source:
// the user passes --repo or --image when applying.
func BuildFile(cfg FileConfig, serviceName string, opts Options) Plan {
	if serviceName == "" {
		serviceName = "app"
	}
	svc := Service{
		Name:         serviceName,
		StartCommand: cfg.Deploy.StartCommand,
		NumReplicas:  cfg.Deploy.NumReplicas,
		Region:       cfg.Deploy.Region,
		CronSchedule: cfg.Deploy.CronSchedule,
	}
	plan := Plan{
		Version: PlanVersion,
		Source: PlanSource{
			Kind:        "file",
			ProjectName: serviceName,
			FetchedAt:   "",
			Digest:      fileDigest(cfg),
		},
		Target: PlanTarget{
			WorkspaceID: opts.WorkspaceID,
			ProjectName: opts.ProjectName,
			Into:        opts.Into,
		},
		Warnings: []string{RotationWarning},
	}
	name := opts.ProjectName
	if name == "" {
		name = serviceName
	}
	plan.Target.ProjectName = name
	if opts.Into == "" {
		plan.Actions = append(plan.Actions, Action{Op: "project.create", Name: name})
	}
	address := true
	plan.Actions = append(plan.Actions, Action{
		Op:      "service.create",
		Name:    svc.Name,
		Kind:    kindOf(svc),
		Source:  nil,
		Build:   dockerfileSetting(cfg.Build.DockerfilePath),
		Address: &address,
		Settings: &ServiceSetting{
			StartCommand: svc.StartCommand,
			Replicas:     svc.NumReplicas,
			Region:       mapRegion(svc.Region),
			CronSchedule: svc.CronSchedule,
		},
	})
	plan.Actions = append(plan.Actions, unmappedServiceActions(svc)...)
	plan.Actions = append(plan.Actions, unmappedFileActions(cfg, serviceName)...)
	return plan
}

func kindOf(svc Service) string {
	kind, _ := serviceKind(svc)
	return kind
}

func dockerfileSetting(path string) *BuildSetting {
	if path == "" {
		return nil
	}
	return &BuildSetting{Dockerfile: path}
}

func unmappedFileActions(cfg FileConfig, service string) []Action {
	var actions []Action
	add := func(field, why, next string) {
		actions = append(actions, Action{Op: "unmapped", Name: service, Field: field, Why: why, Next: next})
	}
	if cfg.Build.Builder != "" {
		add("build.builder", "MIOSA builds from the Dockerfile or detects the stack; there is no builder choice", "none")
	}
	if cfg.Build.BuildCommand != "" {
		add("build.buildCommand", "MIOSA has no build command setting", "move it into the Dockerfile or a build script")
	}
	if len(cfg.Build.WatchPatterns) > 0 {
		add("build.watchPatterns", "MIOSA builds on every push", "none")
	}
	if cfg.Deploy.HealthcheckPath != "" {
		add("deploy.healthcheckPath", "MIOSA checks readiness on the service's port", "none")
	}
	if cfg.Deploy.RestartPolicyType != "" {
		add("deploy.restartPolicyType", "MIOSA restarts a failed release by policy", "none")
	}
	if cfg.Deploy.SleepApplication {
		add("deploy.sleepApplication", "MIOSA has no sleep-on-idle setting", "none")
	}
	if len(cfg.Deploy.PreDeployCommand) > 0 {
		add("deploy.preDeployCommand", "MIOSA has no pre-deploy hook yet",
			"run it from the start command or a release step")
	}
	if path := cfg.Deploy.Region; path != "" && mapRegion(path) == "" {
		add("deploy.region", fmt.Sprintf("Railway region %q has no MIOSA equivalent (us-east, us-west, us-mia)", path),
			"pick the nearest region after the import")
	}
	sortActions(actions)
	return actions
}

func sortActions(actions []Action) {
	sort.SliceStable(actions, func(i, j int) bool { return actions[i].Field < actions[j].Field })
}

func fileDigest(cfg FileConfig) string {
	body, _ := json.Marshal(cfg)
	sum := sha256.Sum256(body)
	return "sha256:" + hex.EncodeToString(sum[:])
}
