package railway

import (
	"encoding/json"
	"sort"
	"strings"
)

// A Railway service's settings live in three places, and Railway itself merges
// them: the repository's config file (railway.json or railway.toml) wins over
// the service settings, and the latest deployment's service manifest is what
// actually ran. The importer takes each setting from the first place that has
// it, in that order, and records where it came from.
const (
	fromConfigFile = "config file"
	fromSettings   = "service settings"
	fromDeployment = "latest deployment"
)

// manifest is the build and deploy shape shared by Railway's config file and a
// deployment's service manifest. Only the keys the importer carries or reports
// are decoded.
type manifest struct {
	Build struct {
		Builder        string   `json:"builder"`
		DockerfilePath string   `json:"dockerfilePath"`
		BuildCommand   string   `json:"buildCommand"`
		WatchPatterns  []string `json:"watchPatterns"`
	} `json:"build"`
	Deploy struct {
		StartCommand            string                       `json:"startCommand"`
		PreDeployCommand        json.RawMessage              `json:"preDeployCommand"`
		NumReplicas             *int                         `json:"numReplicas"`
		HealthcheckPath         string                       `json:"healthcheckPath"`
		HealthcheckTimeout      *int                         `json:"healthcheckTimeout"`
		RestartPolicyType       string                       `json:"restartPolicyType"`
		RestartPolicyMaxRetries *int                         `json:"restartPolicyMaxRetries"`
		CronSchedule            string                       `json:"cronSchedule"`
		Region                  string                       `json:"region"`
		MultiRegionConfig       map[string]multiRegionConfig `json:"multiRegionConfig"`
	} `json:"deploy"`
}

type multiRegionConfig struct {
	NumReplicas *int `json:"numReplicas"`
}

// decodeManifest reads a manifest from a JSON object, or the zero manifest when
// there is none or it does not decode (a setting is then simply not found).
func decodeManifest(raw json.RawMessage) manifest {
	var m manifest
	if len(raw) == 0 {
		return m
	}
	_ = json.Unmarshal(raw, &m)
	return m
}

// deploymentManifest takes the service manifest out of a deployment's meta.
// Nothing else in meta is decoded or kept.
func deploymentManifest(d *wireDeployment) manifest {
	if d == nil || len(d.Meta) == 0 {
		return manifest{}
	}
	var meta struct {
		ServiceManifest json.RawMessage `json:"serviceManifest"`
	}
	if err := json.Unmarshal(d.Meta, &meta); err != nil {
		return manifest{}
	}
	return decodeManifest(meta.ServiceManifest)
}

// commandText turns Railway's pre-deploy command (a string or a list of
// commands) into one shell line.
func commandText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var one string
	if err := json.Unmarshal(raw, &one); err == nil {
		return strings.TrimSpace(one)
	}
	var many []string
	if err := json.Unmarshal(raw, &many); err == nil {
		parts := make([]string, 0, len(many))
		for _, c := range many {
			if c = strings.TrimSpace(c); c != "" {
				parts = append(parts, c)
			}
		}
		return strings.Join(parts, " && ")
	}
	return ""
}

// region is the manifest's region: the explicit one, else the first region of
// a multi-region config (sorted, so the read is stable).
func (m manifest) region() string {
	if m.Deploy.Region != "" {
		return m.Deploy.Region
	}
	keys := make([]string, 0, len(m.Deploy.MultiRegionConfig))
	for k := range m.Deploy.MultiRegionConfig {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	if len(keys) == 0 {
		return ""
	}
	return keys[0]
}

// replicas is the manifest's replica count: numReplicas, else the sum over a
// multi-region config.
func (m manifest) replicas() int {
	if m.Deploy.NumReplicas != nil {
		return *m.Deploy.NumReplicas
	}
	total := 0
	for _, r := range m.Deploy.MultiRegionConfig {
		if r.NumReplicas != nil {
			total += *r.NumReplicas
		}
	}
	return total
}

// resolveSettings fills the service from the config file, the instance and the
// latest deployment, first one that has a value wins, and records the source
// of every value it set.
func resolveSettings(svc *Service, inst wireInstance) {
	file := manifest{}
	if inst.ResolvedFileConfig != nil {
		file = decodeManifest(inst.ResolvedFileConfig.FileManifest)
		svc.ConfigFile = strings.TrimSpace(inst.ResolvedFileConfig.ConfigFile)
	}
	if svc.ConfigFile == "" {
		svc.ConfigFile = strings.TrimSpace(inst.RailwayConfigFile)
	}
	deployed := deploymentManifest(inst.LatestDeployment)
	fileLabel := fromConfigFile
	if svc.ConfigFile != "" {
		fileLabel = svc.ConfigFile
	}
	svc.Provenance = map[string]string{}

	pick := func(field string, target *string, file, setting, deployment string) {
		for _, c := range []struct{ value, from string }{
			{file, fileLabel}, {setting, fromSettings}, {deployment, fromDeployment},
		} {
			if v := strings.TrimSpace(c.value); v != "" {
				*target = v
				svc.Provenance[field] = c.from
				return
			}
		}
	}
	pickInt := func(field string, target *int, file *int, setting *int, deployment int) {
		switch {
		case file != nil && *file > 0:
			*target, svc.Provenance[field] = *file, fileLabel
		case setting != nil && *setting > 0:
			*target, svc.Provenance[field] = *setting, fromSettings
		case deployment > 0:
			*target, svc.Provenance[field] = deployment, fromDeployment
		}
	}

	pick("start_command", &svc.StartCommand, file.Deploy.StartCommand, inst.StartCommand, deployed.Deploy.StartCommand)
	pick("pre_deploy_command", &svc.PreDeployCommand,
		commandText(file.Deploy.PreDeployCommand), commandText(inst.PreDeployCommand), commandText(deployed.Deploy.PreDeployCommand))
	pick("build_command", &svc.BuildCommand, file.Build.BuildCommand, inst.BuildCommand, deployed.Build.BuildCommand)
	pick("dockerfile", &svc.DockerfilePath, file.Build.DockerfilePath, inst.DockerfilePath, deployed.Build.DockerfilePath)
	pick("builder", &svc.Builder, file.Build.Builder, "", deployed.Build.Builder)
	pick("healthcheck_path", &svc.HealthcheckPath, file.Deploy.HealthcheckPath, inst.HealthcheckPath, deployed.Deploy.HealthcheckPath)
	pick("restart_policy", &svc.RestartPolicy, file.Deploy.RestartPolicyType, inst.RestartPolicyType, deployed.Deploy.RestartPolicyType)
	pick("cron_schedule", &svc.CronSchedule, file.Deploy.CronSchedule, inst.CronSchedule, deployed.Deploy.CronSchedule)
	pick("region", &svc.Region, file.region(), inst.Region, deployed.region())
	pickInt("replicas", &svc.NumReplicas, nonZero(file.replicas()), inst.NumReplicas, deployed.replicas())
	pickInt("healthcheck_timeout_s", &svc.HealthcheckTimeout, file.Deploy.HealthcheckTimeout, inst.HealthcheckTimeout, intOf(deployed.Deploy.HealthcheckTimeout))
	pickInt("max_retries", &svc.RestartMaxRetries, file.Deploy.RestartPolicyMaxRetries, inst.RestartPolicyMaxRetries, intOf(deployed.Deploy.RestartPolicyMaxRetries))

	if len(svc.Provenance) == 0 {
		svc.Provenance = nil
	}
}

func nonZero(n int) *int {
	if n <= 0 {
		return nil
	}
	return &n
}

func intOf(p *int) int {
	if p == nil {
		return 0
	}
	return *p
}
