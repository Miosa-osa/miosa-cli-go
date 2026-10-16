package client

import (
	"context"
	"encoding/json"
)

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
	// Restart lets the server recreate the sandbox from its disk at the new
	// size when a change cannot apply live. The new sandbox has a new id and
	// the source stays paused.
	Restart bool `json:"restart,omitempty"`
}

// SandboxResizeOutcome is the body of a successful resize. applied_live and
// requires_restart describe the in-place (200) case; restarted marks the
// recreation (202) case, which also carries mode, new_sandbox and
// source_sandbox. Resources is the shape the sandbox has now, so after a
// requires_restart answer it is the OLD shape: nothing was changed.
type SandboxResizeOutcome struct {
	ID              string                         `json:"id"`
	AppliedLive     bool                           `json:"applied_live"`
	RequiresRestart bool                           `json:"requires_restart"`
	Changes         map[string]SandboxResizeChange `json:"changes"`
	Resources       SandboxResourceGeometry        `json:"resources"`
	Capabilities    map[string]any                 `json:"capabilities"`
	Billing         map[string]any                 `json:"billing,omitempty"`
	Restarted       bool                           `json:"restarted"`
	Mode            string                         `json:"mode,omitempty"`
	NewSandbox      *SandboxRef                    `json:"new_sandbox,omitempty"`
	SourceSandbox   *SandboxRef                    `json:"source_sandbox,omitempty"`
}

// SandboxResizeChange is what happened to one dimension (cpu, memory_mb):
// status is applied_live, requires_restart, unsupported, unchanged or
// recreated, and reason says why when it was not applied.
type SandboxResizeChange struct {
	From   int    `json:"from,omitempty"`
	To     int    `json:"to,omitempty"`
	Status string `json:"status"`
	Reason string `json:"reason,omitempty"`
}

// UnmarshalJSON also accepts a bare status string.
func (c *SandboxResizeChange) UnmarshalJSON(b []byte) error {
	var status string
	if json.Unmarshal(b, &status) == nil {
		*c = SandboxResizeChange{Status: status}
		return nil
	}
	type plain SandboxResizeChange
	return json.Unmarshal(b, (*plain)(c))
}

// SandboxRef names a sandbox in a resize answer: {"id", "state"}.
type SandboxRef struct {
	ID    string `json:"id"`
	State string `json:"state,omitempty"`
}

// UnmarshalJSON also accepts a bare id string.
func (r *SandboxRef) UnmarshalJSON(b []byte) error {
	var id string
	if json.Unmarshal(b, &id) == nil {
		*r = SandboxRef{ID: id}
		return nil
	}
	type plain SandboxRef
	return json.Unmarshal(b, (*plain)(r))
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
