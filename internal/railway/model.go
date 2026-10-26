package railway

import (
	"encoding/json"
	"time"
)

// The wire types below mirror the GraphQL documents in queries.go field for
// field. Nothing outside this package sees them.

type connection[T any] struct {
	Edges []struct {
		Node T `json:"node"`
	} `json:"edges"`
}

func (c connection[T]) nodes() []T {
	out := make([]T, 0, len(c.Edges))
	for _, e := range c.Edges {
		out = append(out, e.Node)
	}
	return out
}

type wireProject struct {
	ID           string                 `json:"id"`
	Name         string                 `json:"name"`
	Description  string                 `json:"description"`
	Environments connection[wireRef]    `json:"environments"`
	Plugins      connection[wirePlugin] `json:"plugins"`
}

type wireRef struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type wireInstance struct {
	ServiceID               string `json:"serviceId"`
	ServiceName             string `json:"serviceName"`
	Source                  wireSource
	BuildCommand            string `json:"buildCommand"`
	StartCommand            string `json:"startCommand"`
	RootDirectory           string `json:"rootDirectory"`
	DockerfilePath          string `json:"dockerfilePath"`
	HealthcheckPath         string `json:"healthcheckPath"`
	HealthcheckTimeout      *int   `json:"healthcheckTimeout"`
	RestartPolicyType       string `json:"restartPolicyType"`
	RestartPolicyMaxRetries *int   `json:"restartPolicyMaxRetries"`
	NumReplicas             *int   `json:"numReplicas"`
	Region                  string `json:"region"`
	CronSchedule            string `json:"cronSchedule"`
	// PreDeployCommand is a string or a list of strings.
	PreDeployCommand   json.RawMessage `json:"preDeployCommand"`
	RailwayConfigFile  string          `json:"railwayConfigFile"`
	ResolvedFileConfig *wireFileConfig `json:"resolvedFileConfig"`
	LatestDeployment   *wireDeployment `json:"latestDeployment"`
}

// wireFileConfig is the repository's railway.json or railway.toml as Railway
// parsed it at the last deployment that read it. An empty manifest means the
// repository has no config file.
type wireFileConfig struct {
	ConfigFile   string          `json:"configFile"`
	FileManifest json.RawMessage `json:"fileManifest"`
}

type wireSource struct {
	Repo  string `json:"repo"`
	Image string `json:"image"`
}

type wireDeployment struct {
	ID     string `json:"id"`
	Status string `json:"status"`
	// Meta is a JSON object; only its serviceManifest is decoded (manifest), so
	// nothing else Railway puts there is kept.
	Meta json.RawMessage `json:"meta"`
}

type wireVolumeInstance struct {
	ID        string     `json:"id"`
	MountPath string     `json:"mountPath"`
	SizeMB    int        `json:"sizeMB"`
	ServiceID string     `json:"serviceId"`
	Volume    wireVolume `json:"volume"`
}

type wireVolume struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type wireDomains struct {
	ServiceDomains []wireDomain `json:"serviceDomains"`
	CustomDomains  []wireDomain `json:"customDomains"`
}

type wireDomain struct {
	ID         string `json:"id"`
	Domain     string `json:"domain"`
	Suffix     string `json:"suffix"`
	TargetPort *int   `json:"targetPort"`
}

type wireTrigger struct {
	ID         string `json:"id"`
	Branch     string `json:"branch"`
	Repository string `json:"repository"`
}

type wirePlugin struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	FriendlyName string `json:"friendlyName"`
}

// Service is one Railway service instance (a service in one environment),
// normalized for the plan.
type Service struct {
	Name string
	// RailwayName is the name on Railway when the import had to change it.
	RailwayName string
	// Port is the port Railway routed the service's domains to (targetPort).
	Port               int
	Environment        string
	Repo               string
	Image              string
	Branch             string
	RootDirectory      string
	DockerfilePath     string
	StartCommand       string
	BuildCommand       string
	HealthcheckPath    string
	HealthcheckTimeout int
	RestartPolicy      string
	RestartMaxRetries  int
	NumReplicas        int
	Region             string
	CronSchedule       string
	PreDeployCommand   string
	Builder            string
	// ConfigFile is the railway.json or railway.toml the settings were read
	// from, when the repository has one.
	ConfigFile string
	// Provenance says where each carried setting came from: the config file, the
	// service settings, or the latest deployment's manifest.
	Provenance     map[string]string
	ServiceDomains []string
	CustomDomains  []string
	Volumes        []VolumeRef
	Variables      []Variable
}

// Variable is one variable's name and, when it is not a secret, its value.
// HasValue is false when Railway returns no readable value (a sealed variable)
// or when the reader was told not to copy values.
type Variable struct {
	Name string `json:"name"`
	// Value is only set for a reference (${{...}}); a literal never leaves the
	// read unless --copy-variables was asked for.
	Value    string `json:"value,omitempty"`
	HasValue bool   `json:"has_value"`
}

// VolumeRef is a Railway volume attached to a service.
type VolumeRef struct {
	MountPath string
	SizeMB    int
}

// PluginRef is a legacy Railway plugin (a managed database).
type PluginRef struct {
	Name         string
	FriendlyName string
}

// Source is everything read from Railway, normalized. It is the input to the
// plan, and its digest guards an apply against a project that changed.
type Source struct {
	Kind        string
	ProjectID   string
	ProjectName string
	// Environment names the environments that were read, comma separated.
	Environment  string
	Environments []string
	Services     []Service
	// Shared is each read environment's shared variables.
	Shared    []SharedVariable
	Plugins   []PluginRef
	FetchedAt time.Time
}

// SharedVariable is one environment's shared variable (names only unless
// --copy-variables).
type SharedVariable struct {
	Environment string
	Variable    Variable
}
