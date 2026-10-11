package miosa

import "context"

// RuntimesService lists the sandbox runtimes the platform offers.
type RuntimesService struct {
	client *Client
}

// SandboxRuntime describes one selectable sandbox runtime.
type SandboxRuntime struct {
	ID             string `json:"id"`
	RuntimeProfile string `json:"runtime_profile"`
	Label          string `json:"label"`
	Description    string `json:"description"`
	// Default marks the runtime the platform uses when none is named.
	Default bool `json:"default"`
	// Available is the live gate state: false means the platform will not
	// schedule a sandbox on this runtime right now.
	Available bool `json:"available"`
	// UnavailableReason is a machine code ("GLOBAL_GATE_CLOSED",
	// "ORG_RESTRICTED") when Available is false, and nil otherwise.
	UnavailableReason *string                    `json:"unavailable_reason"`
	Capabilities      SandboxRuntimeCapabilities `json:"capabilities"`
}

// SandboxRuntimeCapabilities is what a runtime supports and its largest shape.
type SandboxRuntimeCapabilities struct {
	Browser       bool                `json:"browser"`
	NetworkEgress bool                `json:"network_egress"`
	Persistence   bool                `json:"persistence"`
	Pause         bool                `json:"pause"`
	GPU           bool                `json:"gpu"`
	MaxShape      SandboxRuntimeShape `json:"max_shape"`
}

// SandboxRuntimeShape is the largest sandbox a runtime can host.
type SandboxRuntimeShape struct {
	CPUCount   int `json:"cpu_count"`
	MemoryMB   int `json:"memory_mb"`
	DiskSizeMB int `json:"disk_size_mb"`
}

// SandboxRuntimeListResponse wraps the runtimes list envelope.
type SandboxRuntimeListResponse struct {
	Data []SandboxRuntime `json:"data"`
}

// List returns every sandbox runtime the platform offers, with the live gate
// state for each.
func (s *RuntimesService) List(ctx context.Context) ([]SandboxRuntime, error) {
	var out SandboxRuntimeListResponse
	if err := s.client.getJSON(ctx, "/sandboxes/runtimes", &out); err != nil {
		return nil, err
	}
	return out.Data, nil
}
