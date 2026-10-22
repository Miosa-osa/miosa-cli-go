package railway

import "time"

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
	LatestDeployment        *wireDeployment
}

type wireSource struct {
	Repo  string `json:"repo"`
	Image string `json:"image"`
}

type wireDeployment struct {
	ID     string `json:"id"`
	Status string `json:"status"`
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

// Service is one Railway service, normalized for the plan.
type Service struct {
	Name               string
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
	NumReplicas        int
	Region             string
	CronSchedule       string
	ServiceDomains     []string
	CustomDomains      []string
	Volumes            []VolumeRef
	Variables          []Variable
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
	Kind         string
	ProjectID    string
	ProjectName  string
	Environment  string
	Environments []string
	Services     []Service
	Plugins      []PluginRef
	FetchedAt    time.Time
}
