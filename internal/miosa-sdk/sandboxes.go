package miosa

import (
	"context"
	"fmt"
	"strconv"
	"time"
)

// SandboxTemplate is the default template slug for the lightweight code-exec
// sandbox rootfs.
const SandboxTemplate = "miosa-sandbox"

// SandboxesService provides CRUD operations on sandbox resources.
type SandboxesService struct {
	client *Client
}

// SandboxData is the API representation of a sandbox resource.
type SandboxData struct {
	ID             string            `json:"id"`
	TenantID       string            `json:"tenant_id"`
	OwnerID        string            `json:"owner_id"`
	Name           string            `json:"name"`
	State          ComputerStatus    `json:"state"`
	TemplateID     string            `json:"template_id"`
	ImageID        string            `json:"image_id"`
	CPUCount       int               `json:"cpu_count"`
	MemoryMB       int               `json:"memory_mb"`
	DiskSizeMB     int               `json:"disk_size_mb"`
	BootPath       string            `json:"boot_path"`
	BootMS         *int              `json:"boot_ms"`
	EnvdReadyMS    *int              `json:"envd_ready_ms"`
	TimeoutSec     int               `json:"timeout_sec"`
	IdleTimeoutSec int               `json:"idle_timeout_sec"`
	PreviewURL     string            `json:"preview_url"`
	Ready          bool              `json:"ready"`
	ReadyAt        string            `json:"ready_at"`
	ExitCode       *int              `json:"exit_code"`
	Tags           map[string]string `json:"tags"`
	Metadata       map[string]string `json:"metadata"`
	CreatedAt      string            `json:"created_at"`
	StartedAt      string            `json:"started_at"`
	DestroyedAt    string            `json:"destroyed_at"`
	LastActivityAt string            `json:"last_activity_at"`
	AgentSessionID string            `json:"agent_session_id"`

	// Compatibility fields used by older CLI table rendering.
	Size         ComputerSize   `json:"size,omitempty"`
	TemplateType string         `json:"template_type,omitempty"`
	Status       ComputerStatus `json:"status,omitempty"`
}

// CreateSandboxInput is the request body for POST /sandboxes.
type CreateSandboxInput struct {
	Name           string            `json:"name,omitempty"`
	TemplateID     string            `json:"template_id,omitempty"`
	Image          string            `json:"image,omitempty"`
	CPUCount       int               `json:"cpu_count,omitempty"`
	MemoryMB       int               `json:"memory_mb,omitempty"`
	DiskMB         int               `json:"disk_mb,omitempty"`
	TimeoutSec     int               `json:"timeout_sec,omitempty"`
	IdleTimeoutSec int               `json:"idle_timeout_sec,omitempty"`
	Metadata       map[string]string `json:"metadata,omitempty"`
}

// ListSandboxesInput are optional query parameters for GET /sandboxes.
type ListSandboxesInput struct {
	Page    int            `json:"-"`
	PerPage int            `json:"-"`
	State   ComputerStatus `json:"-"`
}

// SandboxListResponse wraps a paginated list of sandboxes.
type SandboxListResponse struct {
	Data []SandboxData `json:"data"`
	Meta struct {
		Total   int `json:"total"`
		Page    int `json:"page"`
		PerPage int `json:"per_page"`
	} `json:"meta"`
}

// Sandbox is a live handle to a specific sandbox resource.
type Sandbox struct {
	SandboxData

	client *Client
	Files  *FilesService
}

// Create provisions a sandbox through the native sandbox API.
func (s *SandboxesService) Create(ctx context.Context, input CreateSandboxInput) (*Sandbox, error) {
	if input.TemplateID == "" && input.Image == "" {
		input.TemplateID = SandboxTemplate
	}

	var data SandboxData
	if err := s.client.postJSON(ctx, "/sandboxes", input, &data); err != nil {
		return nil, err
	}
	return s.wrap(data), nil
}

// List returns a paginated list of sandboxes.
func (s *SandboxesService) List(ctx context.Context, input ListSandboxesInput) (*SandboxListResponse, error) {
	params := map[string]string{}
	if input.Page > 0 {
		params["page"] = strconv.Itoa(input.Page)
	}
	if input.PerPage > 0 {
		params["per_page"] = strconv.Itoa(input.PerPage)
	}
	if input.State != "" {
		params["state"] = string(input.State)
	}

	var out SandboxListResponse
	if err := s.client.getJSON(ctx, "/sandboxes"+buildQuery(params), &out); err != nil {
		return nil, err
	}
	if out.Meta.Total == 0 && len(out.Data) > 0 {
		out.Meta.Total = len(out.Data)
	}
	if out.Meta.Page == 0 {
		out.Meta.Page = 1
	}
	if out.Meta.PerPage == 0 {
		out.Meta.PerPage = len(out.Data)
	}
	return &out, nil
}

// Get fetches a sandbox by ID or slug.
func (s *SandboxesService) Get(ctx context.Context, id string) (*Sandbox, error) {
	var data SandboxData
	if err := s.client.getJSON(ctx, "/sandboxes/"+id, &data); err != nil {
		return nil, err
	}
	return s.wrap(data), nil
}

// Delete tears down a sandbox.
func (s *SandboxesService) Delete(ctx context.Context, id string) error {
	return s.client.deleteJSON(ctx, "/sandboxes/"+id, nil)
}

func (s *SandboxesService) wrap(data SandboxData) *Sandbox {
	sandbox := &Sandbox{
		SandboxData: normalizeSandboxData(data),
		client:      s.client,
	}
	sandbox.Files = &FilesService{client: s.client, computerID: sandbox.ID, resourceBase: "sandboxes"}
	return sandbox
}

func normalizeSandboxData(data SandboxData) SandboxData {
	if data.State == "" {
		data.State = data.Status
	}
	if data.Status == "" {
		data.Status = data.State
	}
	if data.TemplateID == "" {
		data.TemplateID = data.TemplateType
	}
	if data.TemplateType == "" {
		data.TemplateType = data.TemplateID
	}
	if data.Size == "" {
		data.Size = sandboxSize(data.CPUCount, data.MemoryMB)
	}
	if data.Metadata == nil {
		data.Metadata = map[string]string{}
	}
	if data.Tags == nil {
		data.Tags = map[string]string{}
	}
	return data
}

func sandboxSize(cpuCount, memoryMB int) ComputerSize {
	switch {
	case cpuCount >= 4 || memoryMB >= 8192:
		return SizeLarge
	case cpuCount >= 2 || memoryMB >= 2048:
		return SizeMedium
	default:
		return SizeSmall
	}
}

// PreviewURL returns the backend-issued sandbox preview URL when present.
func (s *Sandbox) PreviewURL(_ int, pathSegment string) string {
	if pathSegment == "" || !startsWith(pathSegment, "/") {
		pathSegment = "/" + pathSegment
	}
	if s.SandboxData.PreviewURL == "" {
		return ""
	}
	return fmt.Sprintf("%s%s", trimTrailingSlash(s.SandboxData.PreviewURL), pathSegment)
}

// PublicURL returns the root preview URL for the sandbox.
func (s *Sandbox) PublicURL() string {
	return s.SandboxData.PreviewURL
}

// Refresh fetches the latest state of this sandbox from the API.
func (s *Sandbox) Refresh(ctx context.Context) error {
	var data SandboxData
	if err := s.client.getJSON(ctx, "/sandboxes/"+s.ID, &data); err != nil {
		return err
	}
	s.SandboxData = normalizeSandboxData(data)
	return nil
}

// Destroy permanently deletes this sandbox.
func (s *Sandbox) Destroy(ctx context.Context) error {
	return s.client.deleteJSON(ctx, "/sandboxes/"+s.ID, nil)
}

// Wait polls until the sandbox reaches the target state or the context is
// cancelled. It refreshes every pollInterval.
func (s *Sandbox) Wait(ctx context.Context, target ComputerStatus, pollInterval ...time.Duration) error {
	interval := 2 * time.Second
	if len(pollInterval) > 0 && pollInterval[0] > 0 {
		interval = pollInterval[0]
	}
	for {
		if err := s.Refresh(ctx); err != nil {
			return err
		}
		if s.State == target {
			return nil
		}
		if s.State == StatusError || s.State == StatusDestroyed {
			return fmt.Errorf("sandbox reached terminal state %q while waiting for %q", s.State, target)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(interval):
		}
	}
}

func trimTrailingSlash(value string) string {
	if len(value) > 1 && value[len(value)-1:] == "/" {
		return value[:len(value)-1]
	}
	return value
}
