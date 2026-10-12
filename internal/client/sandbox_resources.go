package client

import "context"

// This file is the CLI's view of sandbox resource read and live resize:
// GET/PATCH /sandboxes/:id/resources. It talks to the typed API client so a
// refusal keeps its machine-readable code (RESIZE_REQUIRES_RESTART,
// PLAN_CAP_EXCEEDED, ...) and status instead of becoming a bare status line.

// SandboxResourceGeometry is a sandbox's vCPU and memory.
type SandboxResourceGeometry struct {
	CPU      int `json:"cpu"`
	MemoryMB int `json:"memory_mb"`
}

// SandboxResourceState is the body of GET /sandboxes/:id/resources: current
// geometry plus a live view of what can change without a restart. Every
// capabilities key is optional, so it is read as a map.
type SandboxResourceState struct {
	ID           string                  `json:"id"`
	Resources    SandboxResourceGeometry `json:"resources"`
	Capabilities map[string]any          `json:"capabilities"`
}

// SandboxResizeInput is the request body of PATCH /sandboxes/:id/resources.
// A nil field is left out, so only what the caller asked to change is sent.
type SandboxResizeInput struct {
	CPUCount *int `json:"cpu_count,omitempty"`
	MemoryMB *int `json:"memory_mb,omitempty"`
}

// SandboxResizeOutcome is the body of a successful resize. applied_live and
// requires_restart describe the in-place (200) case; restarted marks the
// recreation (202) case, which also carries mode, new_sandbox and
// source_sandbox.
type SandboxResizeOutcome struct {
	ID              string                  `json:"id"`
	AppliedLive     bool                    `json:"applied_live"`
	RequiresRestart bool                    `json:"requires_restart"`
	Changes         map[string]any          `json:"changes"`
	Resources       SandboxResourceGeometry `json:"resources"`
	Capabilities    map[string]any          `json:"capabilities"`
	Billing         map[string]any          `json:"billing,omitempty"`
	Restarted       bool                    `json:"restarted"`
	Mode            string                  `json:"mode,omitempty"`
	NewSandbox      string                  `json:"new_sandbox,omitempty"`
	SourceSandbox   string                  `json:"source_sandbox,omitempty"`
}

// SandboxResources reads a sandbox's vCPU and memory and what it can change
// on the running sandbox right now (GET /sandboxes/:id/resources). Reads only;
// changes nothing. sandboxID must be a resolved id.
func (c *Client) SandboxResources(ctx context.Context, sandboxID string) (*SandboxResourceState, error) {
	var out SandboxResourceState
	if err := c.API.Get(ctx, "/sandboxes/"+sandboxID+"/resources", nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ResizeSandbox changes a sandbox's vCPU and/or memory
// (PATCH /sandboxes/:id/resources). A refusal comes back as an *api.Error
// whose Code is one of the RESIZE_* codes, PLAN_CAP_EXCEEDED or INVALID_PARAM.
// sandboxID must be a resolved id.
func (c *Client) ResizeSandbox(ctx context.Context, sandboxID string, in SandboxResizeInput) (*SandboxResizeOutcome, error) {
	var out SandboxResizeOutcome
	if err := c.API.Patch(ctx, "/sandboxes/"+sandboxID+"/resources", in, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
