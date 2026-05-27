package miosa

import (
	"context"
	"fmt"
	"strconv"
	"time"
)

// ComputerVisibility controls who can access a computer's preview URLs.
type ComputerVisibility string

const (
	VisibilityPublic ComputerVisibility = "public"
	VisibilityTenant ComputerVisibility = "tenant"
	VisibilityKey    ComputerVisibility = "key"
)

// ComputersService provides CRUD operations on computers.
type ComputersService struct {
	client *Client
}

// Create provisions a new computer and returns the full resource.
func (s *ComputersService) Create(ctx context.Context, input CreateComputerInput) (*Computer, error) {
	var data ComputerData
	if err := s.client.postJSON(ctx, "/computers", input, &data); err != nil {
		return nil, err
	}
	return s.wrap(data), nil
}

// List returns a paginated list of computers.
// Pass a zero-value ListComputersInput to use API defaults.
func (s *ComputersService) List(ctx context.Context, input ListComputersInput) (*ComputerListResponse, error) {
	params := map[string]string{}
	if input.Page > 0 {
		params["page"] = strconv.Itoa(input.Page)
	}
	if input.PerPage > 0 {
		params["per_page"] = strconv.Itoa(input.PerPage)
	}
	if input.Status != "" {
		params["status"] = string(input.Status)
	}
	var out ComputerListResponse
	if err := s.client.getJSON(ctx, "/computers"+buildQuery(params), &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Get fetches a single computer by ID.
func (s *ComputersService) Get(ctx context.Context, id string) (*Computer, error) {
	var data ComputerData
	if err := s.client.getJSON(ctx, "/computers/"+id, &data); err != nil {
		return nil, err
	}
	return s.wrap(data), nil
}

// Delete permanently destroys a computer.
func (s *ComputersService) Delete(ctx context.Context, id string) error {
	return s.client.deleteJSON(ctx, "/computers/"+id, nil)
}

// wrap creates a Computer from raw data, wiring up all sub-services.
func (s *ComputersService) wrap(data ComputerData) *Computer {
	c := &Computer{
		ComputerData: data,
		client:       s.client,
	}
	c.Files = &FilesService{client: s.client, computerID: data.ID}
	c.Agent = &AgentService{client: s.client, computerID: data.ID}
	c.Exec = &ExecService{client: s.client, computerID: data.ID}
	c.Snapshots = &SnapshotsService{client: s.client, computerID: data.ID}
	c.Services = &ServicesService{client: s.client, computerID: data.ID}
	c.Domains = &CustomDomainsService{client: s.client, computerID: data.ID}
	c.Events = &EventsService{client: s.client, computerID: data.ID}
	c.NetworkPolicy = &NetworkPolicyService{client: s.client, computerID: data.ID}
	return c
}

// ─── Computer resource ────────────────────────────────────────────────────────

// Computer is a live handle to a specific computer resource.
// It embeds ComputerData and exposes action methods (Start, Stop, Click, …).
// Sub-services are scoped to this computer.
type Computer struct {
	ComputerData

	client        *Client
	Files         *FilesService
	Agent         *AgentService
	Exec          *ExecService
	Snapshots     *SnapshotsService
	Services      *ServicesService
	Domains       *CustomDomainsService
	Events        *EventsService
	NetworkPolicy *NetworkPolicyService
}

// slug returns the URL-safe slug, falling back to the raw ID.
func (c *Computer) slug() string {
	if c.Slug != "" {
		return c.Slug
	}
	return c.ID
}

// PreviewURL returns the public HTTPS URL that proxies to port inside the VM.
// path defaults to "/". Anyone with the URL can reach it — no auth required.
//
//	url := computer.PreviewURL(3000, "/")
//	// => https://3000-<slug>.sandbox.miosa.ai/
func (c *Computer) PreviewURL(port int, pathSegment string) string {
	if pathSegment == "" || !startsWith(pathSegment, "/") {
		pathSegment = "/" + pathSegment
	}
	return fmt.Sprintf("https://%d-%s.sandbox.miosa.ai%s", port, c.slug(), pathSegment)
}

// PublicURL returns the root preview URL for the computer's default app port.
func (c *Computer) PublicURL() string {
	return fmt.Sprintf("https://%s.sandbox.miosa.ai", c.slug())
}

func startsWith(s, prefix string) bool {
	return len(s) >= len(prefix) && s[:len(prefix)] == prefix
}

// Refresh fetches the latest state of this computer from the API.
func (c *Computer) Refresh(ctx context.Context) error {
	var data ComputerData
	if err := c.client.getJSON(ctx, "/computers/"+c.ID, &data); err != nil {
		return err
	}
	c.ComputerData = data
	return nil
}

// ─── Lifecycle ────────────────────────────────────────────────────────────────

// Start powers on a stopped computer.
func (c *Computer) Start(ctx context.Context) error {
	return c.client.postJSON(ctx, fmt.Sprintf("/computers/%s/start", c.ID), nil, nil)
}

// Stop gracefully shuts down a running computer.
func (c *Computer) Stop(ctx context.Context) error {
	return c.client.postJSON(ctx, fmt.Sprintf("/computers/%s/stop", c.ID), nil, nil)
}

// Restart reboots a running computer.
func (c *Computer) Restart(ctx context.Context) error {
	return c.client.postJSON(ctx, fmt.Sprintf("/computers/%s/restart", c.ID), nil, nil)
}

// Destroy permanently deletes this computer.
func (c *Computer) Destroy(ctx context.Context) error {
	return c.client.deleteJSON(ctx, "/computers/"+c.ID, nil)
}

// Wait polls until the computer reaches the target status or the context is
// cancelled. It refreshes every pollInterval.
func (c *Computer) Wait(ctx context.Context, target ComputerStatus, pollInterval ...time.Duration) error {
	interval := 2 * time.Second
	if len(pollInterval) > 0 && pollInterval[0] > 0 {
		interval = pollInterval[0]
	}
	for {
		if err := c.Refresh(ctx); err != nil {
			return err
		}
		if c.Status == target {
			return nil
		}
		if c.Status == StatusError || c.Status == StatusDestroyed {
			return fmt.Errorf("computer reached terminal state %q while waiting for %q", c.Status, target)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(interval):
		}
	}
}

// ─── Exec ────────────────────────────────────────────────────────────────────

// Bash runs a shell command on the computer and returns the result.
func (c *Computer) Bash(ctx context.Context, command string, timeoutSecs ...int) (*ExecResult, error) {
	input := ExecInput{Command: command}
	if len(timeoutSecs) > 0 {
		input.Timeout = timeoutSecs[0]
	}
	var out ExecResult
	if err := c.client.postJSON(ctx, fmt.Sprintf("/computers/%s/exec", c.ID), input, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Python runs Python code on the computer and returns the result.
func (c *Computer) Python(ctx context.Context, code string, timeoutSecs ...int) (*ExecResult, error) {
	input := ExecPythonInput{Code: code}
	if len(timeoutSecs) > 0 {
		input.Timeout = timeoutSecs[0]
	}
	var out ExecResult
	if err := c.client.postJSON(ctx, fmt.Sprintf("/computers/%s/exec/python", c.ID), input, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
