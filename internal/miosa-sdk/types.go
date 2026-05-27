package miosa

// ─── Computers ───────────────────────────────────────────────────────────────

// ComputerSize is the hardware tier for a computer.
type ComputerSize string

const (
	SizeSmall  ComputerSize = "small"
	SizeMedium ComputerSize = "medium"
	SizeLarge  ComputerSize = "large"
)

// ComputerStatus is the lifecycle state of a computer.
type ComputerStatus string

const (
	StatusCreating  ComputerStatus = "creating"
	StatusStarting  ComputerStatus = "starting"
	StatusRunning   ComputerStatus = "running"
	StatusStopping  ComputerStatus = "stopping"
	StatusStopped   ComputerStatus = "stopped"
	StatusError     ComputerStatus = "error"
	StatusDestroyed ComputerStatus = "destroyed"
)

// ComputerData is the API representation of a computer resource.
type ComputerData struct {
	ID           string             `json:"id"`
	Name         string             `json:"name"`
	Slug         string             `json:"slug"`
	Status       ComputerStatus     `json:"status"`
	Visibility   ComputerVisibility `json:"visibility"`
	TemplateType string             `json:"template_type"`
	Size         ComputerSize       `json:"size"`
	TenantID     string             `json:"tenant_id"`
	WorkspaceID  *string            `json:"workspace_id"`
	IPAddress    *string            `json:"ip_address"`
	Metadata     map[string]string  `json:"metadata"`
	CreatedAt    string             `json:"created_at"`
	UpdatedAt    string             `json:"updated_at"`
}

// CreateComputerInput is the request body for POST /computers.
type CreateComputerInput struct {
	Name         string            `json:"name"`
	TemplateType string            `json:"template_type,omitempty"`
	Size         ComputerSize      `json:"size,omitempty"`
	Metadata     map[string]string `json:"metadata,omitempty"`
}

// ListComputersInput are optional query parameters for GET /computers.
type ListComputersInput struct {
	Page    int            `json:"-"`
	PerPage int            `json:"-"`
	Status  ComputerStatus `json:"-"`
}

// ComputerListResponse wraps a paginated list of computers.
type ComputerListResponse struct {
	Data []ComputerData `json:"data"`
	Meta struct {
		Total   int `json:"total"`
		Page    int `json:"page"`
		PerPage int `json:"per_page"`
	} `json:"meta"`
}

// ─── Desktop ─────────────────────────────────────────────────────────────────

// MouseButton identifies which mouse button to use.
type MouseButton string

const (
	ButtonLeft   MouseButton = "left"
	ButtonRight  MouseButton = "right"
	ButtonMiddle MouseButton = "middle"
)

// ScrollDirection is the scroll axis and direction.
type ScrollDirection string

const (
	ScrollUp    ScrollDirection = "up"
	ScrollDown  ScrollDirection = "down"
	ScrollLeft  ScrollDirection = "left"
	ScrollRight ScrollDirection = "right"
)

// ClickInput is the request body for POST /desktop/click.
type ClickInput struct {
	X      int         `json:"x"`
	Y      int         `json:"y"`
	Button MouseButton `json:"button,omitempty"`
}

// DoubleClickInput is the request body for POST /desktop/double-click.
type DoubleClickInput struct {
	X int `json:"x"`
	Y int `json:"y"`
}

// TypeInput is the request body for POST /desktop/type.
type TypeInput struct {
	Text  string `json:"text"`
	Delay int    `json:"delay,omitempty"` // milliseconds between keystrokes
}

// KeyInput is the request body for POST /desktop/key.
type KeyInput struct {
	Key string `json:"key"`
}

// ScrollInput is the request body for POST /desktop/scroll.
type ScrollInput struct {
	X         *int            `json:"x,omitempty"`
	Y         *int            `json:"y,omitempty"`
	Direction ScrollDirection `json:"direction"`
	Clicks    int             `json:"clicks,omitempty"`
}

// DragInput is the request body for POST /desktop/drag.
type DragInput struct {
	FromX int `json:"from_x"`
	FromY int `json:"from_y"`
	ToX   int `json:"to_x"`
	ToY   int `json:"to_y"`
}

// WaitInput is the request body for POST /desktop/wait.
type WaitInput struct {
	Seconds float64 `json:"seconds"`
}

// WindowFocusInput is the request body for POST /desktop/window/focus.
type WindowFocusInput struct {
	WindowID string `json:"window_id"`
}

// LaunchInput is the request body for POST /desktop/launch.
type LaunchInput struct {
	AppName string `json:"app_name"`
}

// DesktopActionResult is the common response for mutating desktop actions.
type DesktopActionResult struct {
	Success bool `json:"success"`
}

// WindowInfo describes an open window on the desktop.
type WindowInfo struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	X         int    `json:"x"`
	Y         int    `json:"y"`
	Width     int    `json:"width"`
	Height    int    `json:"height"`
	IsFocused bool   `json:"is_focused"`
}

// CursorInfo reports the current cursor position.
type CursorInfo struct {
	X int `json:"x"`
	Y int `json:"y"`
}

// ─── Exec ────────────────────────────────────────────────────────────────────

// ExecInput is the request body for POST /computers/{id}/exec.
type ExecInput struct {
	Command string `json:"command"`
	Timeout int    `json:"timeout,omitempty"` // seconds
}

// ExecPythonInput is the request body for POST /computers/{id}/exec/python.
type ExecPythonInput struct {
	Code    string `json:"code"`
	Timeout int    `json:"timeout,omitempty"` // seconds
}

// ExecResult is the response for exec endpoints.
type ExecResult struct {
	Output   string `json:"output"`
	ExitCode int    `json:"exit_code"`
	Success  bool   `json:"success"`
}

// ─── Files ───────────────────────────────────────────────────────────────────

// FileEntry describes a single file or directory on the computer.
type FileEntry struct {
	Name       string `json:"name"`
	Path       string `json:"path"`
	Size       int64  `json:"size"`
	IsDir      bool   `json:"is_dir"`
	ModifiedAt string `json:"modified_at"`
}

// FileListResult is the response for GET /computers/{id}/files.
type FileListResult struct {
	Entries []FileEntry `json:"entries"`
	Path    string      `json:"path"`
}

// FileExportResult is the response for POST /computers/{id}/files/export.
type FileExportResult struct {
	URL       string `json:"url"`
	ExpiresAt string `json:"expires_at"`
}

// ─── Agent / CUA ─────────────────────────────────────────────────────────────

// AgentSessionStatus is the lifecycle state of an agent session.
type AgentSessionStatus string

const (
	AgentPending   AgentSessionStatus = "pending"
	AgentRunning   AgentSessionStatus = "running"
	AgentCompleted AgentSessionStatus = "completed"
	AgentFailed    AgentSessionStatus = "failed"
	AgentCancelled AgentSessionStatus = "cancelled"
)

// AgentEventType identifies the kind of SSE event emitted by an agent session.
type AgentEventType string

const (
	EventSessionStarted   AgentEventType = "session_started"
	EventTurnStarted      AgentEventType = "turn_started"
	EventThinking         AgentEventType = "thinking"
	EventToolCall         AgentEventType = "tool_call"
	EventToolResult       AgentEventType = "tool_result"
	EventStreamingToken   AgentEventType = "streaming_token"
	EventAgentResponse    AgentEventType = "agent_response"
	EventTurnCompleted    AgentEventType = "turn_completed"
	EventSessionCompleted AgentEventType = "session_completed"
	EventSessionFailed    AgentEventType = "session_failed"
	EventDone             AgentEventType = "done"
	EventError            AgentEventType = "error"
)

// RunAgentInput is the request body for POST /computers/{id}/cua/sessions.
type RunAgentInput struct {
	Goal     string `json:"goal"`
	ModelID  string `json:"model_id,omitempty"`
	MaxTurns int    `json:"max_turns,omitempty"`
}

// AgentSessionData is the API representation of an agent session.
type AgentSessionData struct {
	ID          string             `json:"id"`
	ComputerID  string             `json:"computer_id"`
	Goal        string             `json:"goal"`
	ModelID     string             `json:"model_id"`
	Status      AgentSessionStatus `json:"status"`
	MaxTurns    int                `json:"max_turns"`
	TurnsUsed   int                `json:"turns_used"`
	CreatedAt   string             `json:"created_at"`
	UpdatedAt   string             `json:"updated_at"`
	CompletedAt *string            `json:"completed_at"`
	Error       *string            `json:"error"`
}

// AgentSessionListResponse wraps a list of agent sessions.
type AgentSessionListResponse struct {
	Data []AgentSessionData `json:"data"`
}

// AgentEvent is a single SSE event from an agent session stream.
type AgentEvent struct {
	Type      AgentEventType `json:"type"`
	SessionID string         `json:"session_id"`
	Data      interface{}    `json:"data"`
	Timestamp string         `json:"timestamp"`
}

// ─── Credits ─────────────────────────────────────────────────────────────────

// CreditBalance is the current balance for the authenticated tenant.
type CreditBalance struct {
	Balance   int     `json:"balance"`
	ExpiresAt *string `json:"expires_at"`
}

// CreditTransaction is a single credit ledger entry.
type CreditTransaction struct {
	ID          string `json:"id"`
	Amount      int    `json:"amount"`
	Type        string `json:"type"` // "credit" | "debit"
	Description string `json:"description"`
	CreatedAt   string `json:"created_at"`
}

// CreditTransactionListResponse wraps a paginated list of transactions.
type CreditTransactionListResponse struct {
	Data []CreditTransaction `json:"data"`
	Meta struct {
		Total   int `json:"total"`
		Page    int `json:"page"`
		PerPage int `json:"per_page"`
	} `json:"meta"`
}

// CreditUsage summarises credit consumption for a billing period.
type CreditUsage struct {
	PeriodStart    string `json:"period_start"`
	PeriodEnd      string `json:"period_end"`
	ComputeCredits int    `json:"compute_credits"`
	AICredits      int    `json:"ai_credits"`
	TotalCredits   int    `json:"total_credits"`
}

// ─── Workspaces ───────────────────────────────────────────────────────────────

// Workspace groups related computers under a shared namespace.
type Workspace struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	TenantID    string            `json:"tenant_id"`
	Description string            `json:"description"`
	Metadata    map[string]string `json:"metadata"`
	CreatedAt   string            `json:"created_at"`
	UpdatedAt   string            `json:"updated_at"`
}

// CreateWorkspaceInput is the request body for POST /workspaces.
type CreateWorkspaceInput struct {
	Name        string            `json:"name"`
	Description string            `json:"description,omitempty"`
	Metadata    map[string]string `json:"metadata,omitempty"`
}

// UpdateWorkspaceInput is the request body for PATCH /workspaces/{id}.
type UpdateWorkspaceInput struct {
	Name        string            `json:"name,omitempty"`
	Description string            `json:"description,omitempty"`
	Metadata    map[string]string `json:"metadata,omitempty"`
}

// ListWorkspacesInput holds optional pagination parameters.
type ListWorkspacesInput struct {
	Page    int
	PerPage int
}

// WorkspaceListResponse wraps a paginated list of workspaces.
type WorkspaceListResponse struct {
	Data []Workspace `json:"data"`
	Meta struct {
		Total   int `json:"total"`
		Page    int `json:"page"`
		PerPage int `json:"per_page"`
	} `json:"meta"`
}

// ─── Snapshots ────────────────────────────────────────────────────────────────

// SnapshotStatus is the lifecycle state of a Firecracker snapshot.
type SnapshotStatus string

const (
	SnapshotCreating  SnapshotStatus = "creating"
	SnapshotUploading SnapshotStatus = "uploading"
	SnapshotReady     SnapshotStatus = "ready"
	SnapshotRestoring SnapshotStatus = "restoring"
	SnapshotFailed    SnapshotStatus = "failed"
	SnapshotDeleted   SnapshotStatus = "deleted"
)

// Snapshot is the API representation of a Firecracker checkpoint.
type Snapshot struct {
	ID                  string         `json:"id"`
	ComputerID          string         `json:"computer_id"`
	TenantID            string         `json:"tenant_id"`
	Comment             *string        `json:"comment"`
	Status              SnapshotStatus `json:"status"`
	StateSizeBytes      *int64         `json:"state_size_bytes"`
	MemorySizeBytes     *int64         `json:"memory_size_bytes"`
	RootfsSizeBytes     *int64         `json:"rootfs_size_bytes"`
	CompressedSizeBytes *int64         `json:"compressed_size_bytes"`
	S3Bucket            *string        `json:"s3_bucket"`
	S3Prefix            *string        `json:"s3_prefix"`
	ParentSnapshotID    *string        `json:"parent_snapshot_id"`
	Error               *string        `json:"error"`
	CreatedAt           string         `json:"created_at"`
	UpdatedAt           string         `json:"updated_at"`
}

// CreateSnapshotInput is the request body for POST /computers/{id}/snapshots.
type CreateSnapshotInput struct {
	Comment string `json:"comment,omitempty"`
}

// SnapshotProgressEvent is an SSE frame emitted during snapshot operations.
type SnapshotProgressEvent struct {
	Type       string `json:"type"`
	SnapshotID string `json:"snapshot_id"`
	Status     string `json:"status"`
	Step       string `json:"step,omitempty"`
	Progress   *int   `json:"progress,omitempty"`
	Error      string `json:"error,omitempty"`
}

// SnapshotStream is a live SSE channel for snapshot progress events.
// The underlying channel is closed when the stream ends.
type SnapshotStream struct {
	ch <-chan SnapshotProgressEvent
	// C is the read-only channel exposed to callers.
	C <-chan SnapshotProgressEvent
}

func (s *SnapshotStream) init() {
	s.C = s.ch
}

// ─── Services ─────────────────────────────────────────────────────────────────

// ServiceStatus is the runtime state of a managed service.
type ServiceStatus string

const (
	ServiceRunning  ServiceStatus = "running"
	ServiceStopped  ServiceStatus = "stopped"
	ServiceStarting ServiceStatus = "starting"
	ServiceFailed   ServiceStatus = "failed"
)

// Service is the API representation of a long-running process managed on a computer.
type Service struct {
	ID         string            `json:"id"`
	ComputerID string            `json:"computer_id"`
	Name       string            `json:"name"`
	Command    string            `json:"command"`
	Status     ServiceStatus     `json:"status"`
	Port       *int              `json:"port"`
	Env        map[string]string `json:"env"`
	CreatedAt  string            `json:"created_at"`
	UpdatedAt  string            `json:"updated_at"`
}

// CreateServiceInput is the request body for POST /computers/{id}/services.
type CreateServiceInput struct {
	Name    string            `json:"name"`
	Command string            `json:"command"`
	Port    *int              `json:"port,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
}

// ServiceLogEvent is a single log line from a running service.
type ServiceLogEvent struct {
	Timestamp string `json:"timestamp"`
	Stream    string `json:"stream"` // "stdout" | "stderr"
	Message   string `json:"message"`
}

// ServiceLogStream is a live channel of log events from a service.
type ServiceLogStream struct {
	ch <-chan ServiceLogEvent
	// C is the read-only channel exposed to callers.
	C <-chan ServiceLogEvent
}

func (s *ServiceLogStream) init() {
	s.C = s.ch
}

// ─── Custom Domains ───────────────────────────────────────────────────────────

// CustomDomainStatus is the lifecycle state of a custom domain mapping.
type CustomDomainStatus string

const (
	DomainPending  CustomDomainStatus = "pending"
	DomainVerified CustomDomainStatus = "verified"
	DomainActive   CustomDomainStatus = "active"
	DomainFailed   CustomDomainStatus = "failed"
	DomainRemoved  CustomDomainStatus = "removed"
)

// CustomDomain is the API representation of a custom domain mapping.
type CustomDomain struct {
	ID                 string             `json:"id"`
	ComputerID         string             `json:"computer_id"`
	TenantID           string             `json:"tenant_id"`
	FQDN               string             `json:"fqdn"`
	Status             CustomDomainStatus `json:"status"`
	VerificationTarget string             `json:"verification_target"`
	Instructions       string             `json:"instructions"`
	VerifiedAt         *string            `json:"verified_at"`
	TLSIssuedAt        *string            `json:"tls_issued_at"`
	CreatedAt          string             `json:"created_at"`
	UpdatedAt          string             `json:"updated_at"`
}

// ─── Network Policy ───────────────────────────────────────────────────────────

// NetworkEffect is the action applied to matching traffic.
type NetworkEffect string

const (
	NetworkEffectAllow NetworkEffect = "allow"
	NetworkEffectDeny  NetworkEffect = "deny"
)

// NetworkProtocol is the transport protocol for a policy rule.
type NetworkProtocol string

const (
	NetworkProtocolTCP NetworkProtocol = "tcp"
	NetworkProtocolUDP NetworkProtocol = "udp"
	NetworkProtocolAny NetworkProtocol = "any"
)

// NetworkPolicyRule is a single egress rule evaluated by the host firewall.
type NetworkPolicyRule struct {
	Effect      NetworkEffect   `json:"effect"`
	Destination string          `json:"destination"`
	Ports       string          `json:"ports,omitempty"`
	Protocol    NetworkProtocol `json:"protocol,omitempty"`
}

// NetworkPolicy is the full egress policy for a computer.
type NetworkPolicy struct {
	ComputerID    string              `json:"computer_id"`
	Rules         []NetworkPolicyRule `json:"rules"`
	DefaultEffect NetworkEffect       `json:"default_effect"`
}

// SetNetworkPolicyInput is the request body for PUT /computers/{id}/network-policy.
type SetNetworkPolicyInput struct {
	Rules         []NetworkPolicyRule `json:"rules"`
	DefaultEffect NetworkEffect       `json:"default_effect"`
}

// ─── Files (extended) ─────────────────────────────────────────────────────────

// FileStat contains metadata for a path inside the computer.
type FileStat struct {
	Path          string `json:"path"`
	Size          int64  `json:"size"`
	Mode          string `json:"mode"`
	IsDir         bool   `json:"is_dir"`
	IsSymlink     bool   `json:"is_symlink"`
	SymlinkTarget string `json:"symlink_target,omitempty"`
	ModifiedAt    string `json:"modified_at"`
}

// DirEntry is a single entry returned by Readdir.
type DirEntry struct {
	Name       string `json:"name"`
	IsDir      bool   `json:"is_dir"`
	IsSymlink  bool   `json:"is_symlink"`
	Size       int64  `json:"size"`
	ModifiedAt string `json:"modified_at"`
}

// MkdirOptions configures the Mkdir operation.
type MkdirOptions struct {
	// Recursive creates parent directories as needed (default true).
	Recursive bool
	// Mode is the octal permission string (default "0755").
	Mode string
}

// CopyOptions configures the Copy operation.
type CopyOptions struct {
	// Recursive is required when copying a directory tree.
	Recursive bool
}

// ─── Events ───────────────────────────────────────────────────────────────────

// EventProducer names the category of in-VM events to subscribe to.
type EventProducer string

const (
	ProducerWindow    EventProducer = "window"
	ProducerClipboard EventProducer = "clipboard"
	ProducerFile      EventProducer = "file"
	ProducerProcess   EventProducer = "process"
	ProducerIdle      EventProducer = "idle"
)

// EventSubscribeOptions configures an EventsService.Subscribe call.
type EventSubscribeOptions struct {
	// Subscribe lists the producers to enable. At least one is required.
	Subscribe []EventProducer
	// Paths are the filesystem paths to watch (file producer only).
	// Defaults to ["/home/user"] when empty.
	Paths []string
	// IdleThresholdSec is the inactivity threshold for the idle producer (default 30).
	IdleThresholdSec int
}

// EventType is the dot-separated event type string (e.g. "window.focus_changed").
type EventType string

// Event is a single typed event received from the EventStream.
type Event struct {
	Type      EventType `json:"type"`
	Timestamp string    `json:"timestamp"`
	// Payload is a typed struct (e.g. WindowFocusChangedPayload) for known types,
	// or json.RawMessage for unknown types.
	Payload interface{} `json:"payload"`
}

// ── Typed payload structs ─────────────────────────────────────────────────────

// WindowFocusChangedPayload is the payload for "window.focus_changed".
type WindowFocusChangedPayload struct {
	WindowID string `json:"window_id"`
	PID      string `json:"pid"`
	Title    string `json:"title"`
}

// WindowOpenedPayload is the payload for "window.opened".
type WindowOpenedPayload struct {
	WindowID string `json:"window_id"`
	PID      string `json:"pid"`
	Title    string `json:"title"`
}

// WindowClosedPayload is the payload for "window.closed".
type WindowClosedPayload struct {
	WindowID string `json:"window_id"`
	PID      string `json:"pid"`
	Title    string `json:"title"`
}

// ClipboardChangedPayload is the payload for "clipboard.changed".
type ClipboardChangedPayload struct {
	SizeBytes int `json:"size_bytes"`
}

// FileCreatedPayload is the payload for "file.created".
type FileCreatedPayload struct {
	Path string `json:"path"`
}

// FileModifiedPayload is the payload for "file.modified".
type FileModifiedPayload struct {
	Path string `json:"path"`
}

// FileDeletedPayload is the payload for "file.deleted".
type FileDeletedPayload struct {
	Path string `json:"path"`
}

// ProcessStartedPayload is the payload for "process.started".
type ProcessStartedPayload struct {
	PID  int    `json:"pid"`
	Cmd  string `json:"cmd"`
	PPID string `json:"ppid"`
}

// ProcessStoppedPayload is the payload for "process.stopped".
type ProcessStoppedPayload struct {
	PID int    `json:"pid"`
	Cmd string `json:"cmd"`
}

// IdleInactivePayload is the payload for "idle.inactive".
type IdleInactivePayload struct {
	IdleMs int `json:"idle_ms"`
}

// IdleActivePayload is the payload for "idle.active".
type IdleActivePayload struct {
	IdleMs int `json:"idle_ms"`
}

// ProducerUnavailablePayload is the payload for "producer.unavailable".
type ProducerUnavailablePayload struct {
	Producer EventProducer `json:"producer"`
	Reason   string        `json:"reason"`
}

// ─── ComputerStatus extras ────────────────────────────────────────────────────

const (
	// StatusProvisioning is the initial provisioning state.
	StatusProvisioning ComputerStatus = "provisioning"
	// StatusActive is an alias for StatusRunning for SDK parity.
	StatusActive ComputerStatus = "active"
	// StatusPaused is the paused/hibernated state.
	StatusPaused ComputerStatus = "paused"
)

// ─── Internal helpers ────────────────────────────────────────────────────────

// apiResponse is the generic envelope returned by non-data list endpoints.
type apiResponse[T any] struct {
	Data T `json:"data"`
}

// actionResponse is the envelope for simple action endpoints.
type actionResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message,omitempty"`
}
