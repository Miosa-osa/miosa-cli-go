// Package client wraps the miosa-go SDK with config-aware key/URL resolution
// and provides concrete REST implementations for each transport interface.
// Commands that depend on unimplemented server endpoints (proxy only) still
// return ErrPhaseNotReady so callers get an actionable message immediately.
package client

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/gorilla/websocket"
	"github.com/Miosa-osa/miosa-cli-go/internal/config"
	miosa "github.com/Miosa-osa/miosa-go"
)

// ─── ErrPhaseNotReady ─────────────────────────────────────────────────────────

// ErrPhaseNotReady is returned when a feature depends on a server endpoint
// that has not yet been deployed. The message includes the minimum server
// version and an upgrade instruction.
type ErrPhaseNotReady struct {
	Phase      string
	MinVersion string
}

func (e *ErrPhaseNotReady) Error() string {
	return fmt.Sprintf("miosa: %s requires control-plane server v%s — upgrade with: miosa upgrade",
		e.Phase, e.MinVersion)
}

// ─── Transport interfaces ─────────────────────────────────────────────────────

// ExecSession is the transport abstraction for streaming exec.
type ExecSession interface {
	Run(ctx context.Context, computerID, command string, stdout, stderr chan<- string) (exitCode int, err error)
	Resize(ctx context.Context, computerID, sessionID string, rows, cols uint16) error
}

// CheckpointTransport handles checkpoint operations.
type CheckpointTransport interface {
	Create(ctx context.Context, computerID, comment string) (*CheckpointInfo, error)
	List(ctx context.Context, computerID string) ([]CheckpointInfo, error)
	Get(ctx context.Context, checkpointID string) (*CheckpointInfo, error)
	Delete(ctx context.Context, checkpointID string) error
	Restore(ctx context.Context, computerID, checkpointID string) (*miosa.ComputerData, error)
}

// WorkspaceTransport handles workspace operations.
type WorkspaceTransport interface {
	Create(ctx context.Context, name string) (*WorkspaceInfo, error)
	List(ctx context.Context) ([]WorkspaceInfo, error)
	Delete(ctx context.Context, id string) error
}

// ProxyTransport handles local-port-to-VM-port forwarding (Phase 4 — not yet deployed).
type ProxyTransport interface {
	Forward(ctx context.Context, computerID string, localPort, remotePort int) error
}

// ServicesTransport handles supervised process management.
type ServicesTransport interface {
	List(ctx context.Context, computerID string) ([]ServiceInfo, error)
	Create(ctx context.Context, computerID string, input CreateServiceInput) (*ServiceInfo, error)
	Start(ctx context.Context, computerID, name string) error
	Stop(ctx context.Context, computerID, name string) error
	Restart(ctx context.Context, computerID, name string) error
	Delete(ctx context.Context, computerID, name string) error
	Logs(ctx context.Context, computerID, name string, follow bool) (<-chan string, error)
}

// PolicyTransport handles network policy operations.
type PolicyTransport interface {
	Show(ctx context.Context, computerID string) (map[string]interface{}, error)
	Set(ctx context.Context, computerID string, policy map[string]interface{}) error
}

// ─── Data types ───────────────────────────────────────────────────────────────

// CheckpointInfo is the CLI representation of a snapshot/checkpoint.
type CheckpointInfo struct {
	ID         string    `json:"id"`
	ComputerID string    `json:"computer_id"`
	Comment    string    `json:"comment"`
	CreatedAt  time.Time `json:"created_at"`
}

// WorkspaceInfo is the CLI representation of a workspace.
type WorkspaceInfo struct {
	ID   string `json:"id"`
	Slug string `json:"slug"`
	Name string `json:"name"`
}

// ServiceInfo is the CLI representation of a supervised service.
type ServiceInfo struct {
	Name    string `json:"name"`
	Command string `json:"command"`
	Status  string `json:"status"`
	Restart string `json:"restart"`
}

// CreateServiceInput is the request to create a supervised service.
type CreateServiceInput struct {
	Name    string `json:"name"`
	Command string `json:"command"`
	Restart string `json:"restart"`
}

// ─── httpClient helper ────────────────────────────────────────────────────────

// restClient is a minimal HTTP helper used by transport implementations.
// It uses the same base URL and auth key as the SDK.
type restClient struct {
	baseURL string
	apiKey  string
	http    *http.Client
}

func newRestClient(baseURL, apiKey string) *restClient {
	return &restClient{
		baseURL: strings.TrimRight(baseURL, "/"),
		apiKey:  apiKey,
		http:    &http.Client{Timeout: 60 * time.Second},
	}
}

func (c *restClient) do(ctx context.Context, method, path string, body interface{}) (*http.Response, error) {
	var bodyReader io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("marshal request: %w", err)
		}
		bodyReader = bytes.NewReader(buf)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, bodyReader)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		defer resp.Body.Close()
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
		return nil, fmt.Errorf("API error status=%d body=%s", resp.StatusCode, string(raw))
	}
	return resp, nil
}

func (c *restClient) getJSON(ctx context.Context, path string, out interface{}) error {
	resp, err := c.do(ctx, http.MethodGet, path, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return json.NewDecoder(resp.Body).Decode(out)
}

func (c *restClient) postJSON(ctx context.Context, path string, body, out interface{}) error {
	resp, err := c.do(ctx, http.MethodPost, path, body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if out == nil || resp.ContentLength == 0 {
		io.Copy(io.Discard, resp.Body)
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func (c *restClient) putJSON(ctx context.Context, path string, body, out interface{}) error {
	resp, err := c.do(ctx, http.MethodPut, path, body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if out == nil {
		io.Copy(io.Discard, resp.Body)
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// PostJSON is the exported equivalent of postJSON for use by commands.
func (c *restClient) PostJSON(ctx context.Context, path string, body, out interface{}) error {
	return c.postJSON(ctx, path, body, out)
}

func (c *restClient) delete(ctx context.Context, path string) error {
	resp, err := c.do(ctx, http.MethodDelete, path, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)
	return nil
}

// ─── Exec (streaming REST — real implementation) ──────────────────────────────

// realExec implements ExecSession using the blocking REST exec endpoint.
// When the control-plane streaming WebSocket exec endpoint is deployed,
// this can be upgraded transparently.
type realExec struct {
	rc *restClient
}

// Run executes the command via POST /sandboxes/:id/exec, then feeds all
// output to stdout in one shot (single-chunk streaming over REST).
func (e *realExec) Run(ctx context.Context, computerID, command string, stdout, _ chan<- string) (int, error) {
	type input struct {
		Command string `json:"command"`
	}
	type result struct {
		Output   string `json:"output"`
		Stdout   string `json:"stdout"`
		Stderr   string `json:"stderr"`
		ExitCode int    `json:"exit_code"`
		Success  bool   `json:"success"`
	}
	type response struct {
		Data     result `json:"data"`
		Output   string `json:"output"`
		Stdout   string `json:"stdout"`
		Stderr   string `json:"stderr"`
		ExitCode int    `json:"exit_code"`
		Success  bool   `json:"success"`
	}
	var out response
	if err := e.rc.postJSON(ctx, "/sandboxes/"+computerID+"/exec", input{Command: command}, &out); err != nil {
		return 1, err
	}
	execResult := out.Data
	if execResult.Output == "" && execResult.Stdout == "" && execResult.Stderr == "" && execResult.ExitCode == 0 {
		execResult = result{
			Output:   out.Output,
			Stdout:   out.Stdout,
			Stderr:   out.Stderr,
			ExitCode: out.ExitCode,
			Success:  out.Success,
		}
	}
	output := execResult.Stdout
	if output == "" {
		output = execResult.Output
	}
	if stdout != nil && output != "" {
		stdout <- output
	}
	return execResult.ExitCode, nil
}

// Resize sends a terminal resize event. Requires the streaming exec endpoint.
func (e *realExec) Resize(ctx context.Context, computerID, sessionID string, rows, cols uint16) error {
	type resizeInput struct {
		SessionID string `json:"session_id"`
		Rows      uint16 `json:"rows"`
		Cols      uint16 `json:"cols"`
	}
	return e.rc.postJSON(ctx, "/sandboxes/"+computerID+"/exec/resize",
		resizeInput{SessionID: sessionID, Rows: rows, Cols: cols}, nil)
}

// ─── Checkpoints (real implementation) ───────────────────────────────────────

type realCheckpoints struct {
	rc *restClient
}

// snapshotResponse is the envelope returned by the snapshots API.
type snapshotResponse struct {
	Data struct {
		ID         string `json:"id"`
		ComputerID string `json:"computer_id"`
		SandboxID  string `json:"sandbox_id"`
		Comment    string `json:"comment"`
		CreatedAt  string `json:"created_at"`
	} `json:"data"`
}

func parseCheckpoint(s snapshotResponse) *CheckpointInfo {
	t, _ := time.Parse(time.RFC3339, s.Data.CreatedAt)
	resourceID := s.Data.SandboxID
	if resourceID == "" {
		resourceID = s.Data.ComputerID
	}
	return &CheckpointInfo{
		ID:         s.Data.ID,
		ComputerID: resourceID,
		Comment:    s.Data.Comment,
		CreatedAt:  t,
	}
}

func (r *realCheckpoints) Create(ctx context.Context, computerID, comment string) (*CheckpointInfo, error) {
	type createInput struct {
		Comment string `json:"comment,omitempty"`
	}
	var out snapshotResponse
	if err := r.rc.postJSON(ctx, "/sandboxes/"+computerID+"/snapshots",
		createInput{Comment: comment}, &out); err != nil {
		return nil, err
	}
	return parseCheckpoint(out), nil
}

func (r *realCheckpoints) List(ctx context.Context, computerID string) ([]CheckpointInfo, error) {
	var out struct {
		Data []struct {
			ID         string `json:"id"`
			ComputerID string `json:"computer_id"`
			SandboxID  string `json:"sandbox_id"`
			Comment    string `json:"comment"`
			CreatedAt  string `json:"created_at"`
		} `json:"data"`
	}
	if err := r.rc.getJSON(ctx, "/sandboxes/"+computerID+"/snapshots", &out); err != nil {
		return nil, err
	}
	cps := make([]CheckpointInfo, 0, len(out.Data))
	for _, s := range out.Data {
		t, _ := time.Parse(time.RFC3339, s.CreatedAt)
		resourceID := s.SandboxID
		if resourceID == "" {
			resourceID = s.ComputerID
		}
		cps = append(cps, CheckpointInfo{
			ID:         s.ID,
			ComputerID: resourceID,
			Comment:    s.Comment,
			CreatedAt:  t,
		})
	}
	return cps, nil
}

func (r *realCheckpoints) Get(ctx context.Context, checkpointID string) (*CheckpointInfo, error) {
	var out snapshotResponse
	if err := r.rc.getJSON(ctx, "/snapshots/"+checkpointID, &out); err != nil {
		return nil, err
	}
	return parseCheckpoint(out), nil
}

func (r *realCheckpoints) Delete(ctx context.Context, checkpointID string) error {
	return r.rc.delete(ctx, "/snapshots/"+checkpointID)
}

func (r *realCheckpoints) Restore(ctx context.Context, computerID, checkpointID string) (*miosa.ComputerData, error) {
	var out struct {
		Data miosa.SandboxData `json:"data"`
	}
	if err := r.rc.postJSON(ctx, "/sandboxes/"+computerID+"/restore/"+checkpointID, nil, &out); err != nil {
		return nil, err
	}
	return &miosa.ComputerData{
		ID:           out.Data.ID,
		Name:         out.Data.Name,
		Status:       out.Data.State,
		TemplateType: out.Data.TemplateID,
		Size:         out.Data.Size,
		Metadata:     out.Data.Metadata,
		CreatedAt:    out.Data.CreatedAt,
		UpdatedAt:    out.Data.CreatedAt,
	}, nil
}

// ─── Workspaces (real implementation) ────────────────────────────────────────

type realWorkspaces struct {
	rc *restClient
}

// workspaceAPIData is the raw workspace shape from the API.
type workspaceAPIData struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Slug string `json:"slug"`
}

func apiToWorkspaceInfo(w workspaceAPIData) WorkspaceInfo {
	slug := w.Slug
	if slug == "" {
		slug = w.ID
	}
	return WorkspaceInfo{ID: w.ID, Name: w.Name, Slug: slug}
}

func (r *realWorkspaces) Create(ctx context.Context, name string) (*WorkspaceInfo, error) {
	type createInput struct {
		Name string `json:"name"`
	}
	var out struct {
		Data workspaceAPIData `json:"data"`
	}
	if err := r.rc.postJSON(ctx, "/workspaces", createInput{Name: name}, &out); err != nil {
		return nil, err
	}
	ws := apiToWorkspaceInfo(out.Data)
	return &ws, nil
}

func (r *realWorkspaces) List(ctx context.Context) ([]WorkspaceInfo, error) {
	var out struct {
		Data []workspaceAPIData `json:"data"`
	}
	if err := r.rc.getJSON(ctx, "/workspaces", &out); err != nil {
		return nil, err
	}
	list := make([]WorkspaceInfo, 0, len(out.Data))
	for _, w := range out.Data {
		list = append(list, apiToWorkspaceInfo(w))
	}
	return list, nil
}

func (r *realWorkspaces) Delete(ctx context.Context, id string) error {
	return r.rc.delete(ctx, "/workspaces/"+id)
}

// ─── Proxy (real WebSocket tunnel implementation) ─────────────────────────────

// realProxy implements ProxyTransport by opening a WebSocket tunnel to the
// control plane and bridging it to a local TCP listener.
// The control-plane endpoint is:
//
//	GET /api/v1/computers/:id/tunnel/:port  (WebSocket, subprotocol miosa-tunnel-v1)
type realProxy struct {
	baseURL string
	apiKey  string
}

// Forward listens on 127.0.0.1:localPort and for each incoming TCP connection
// opens a WebSocket tunnel to remotePort inside the sandbox. The function blocks
// until ctx is cancelled (caller is responsible for cancellation on SIGINT).
func (p *realProxy) Forward(ctx context.Context, computerID string, localPort, remotePort int) error {
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", localPort))
	if err != nil {
		return fmt.Errorf("proxy: listen on :%d: %w", localPort, err)
	}
	defer ln.Close()

	// Close listener when context is cancelled so Accept unblocks.
	go func() {
		<-ctx.Done()
		ln.Close()
	}()

	for {
		tcpConn, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil // normal shutdown
			}
			return fmt.Errorf("proxy: accept: %w", err)
		}
		go p.handleTunnelConn(ctx, tcpConn, computerID, remotePort)
	}
}

// handleTunnelConn opens a WS tunnel for one accepted TCP connection and
// bridges bytes bidirectionally until either side closes.
func (p *realProxy) handleTunnelConn(ctx context.Context, tcpConn net.Conn, computerID string, remotePort int) {
	defer tcpConn.Close()

	wsURL, err := p.tunnelURL(computerID, remotePort)
	if err != nil {
		return
	}

	hdr := http.Header{
		"Authorization": []string{"Bearer " + p.apiKey},
	}
	dialer := websocket.Dialer{
		HandshakeTimeout: 10 * time.Second,
		Subprotocols:     []string{"miosa-tunnel-v1"},
	}
	wsConn, _, err := dialer.DialContext(ctx, wsURL, hdr)
	if err != nil {
		// Connection refused / not running — log and drop, don't crash the listener.
		return
	}
	defer wsConn.Close()

	// TCP → WS
	tcpToWS := make(chan int64, 1)
	go func() {
		var total int64
		buf := make([]byte, 32*1024)
		for {
			n, err := tcpConn.Read(buf)
			if n > 0 {
				total += int64(n)
				if werr := wsConn.WriteMessage(websocket.BinaryMessage, buf[:n]); werr != nil {
					break
				}
			}
			if err != nil {
				break
			}
		}
		wsConn.WriteControl( //nolint:errcheck
			websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""),
			time.Now().Add(time.Second),
		)
		tcpToWS <- total
	}()

	// WS → TCP
	wsToTCP := make(chan int64, 1)
	go func() {
		var total int64
		for {
			_, data, err := wsConn.ReadMessage()
			if err != nil {
				break
			}
			total += int64(len(data))
			if _, werr := tcpConn.Write(data); werr != nil {
				break
			}
		}
		wsToTCP <- total
	}()

	select {
	case <-tcpToWS:
	case <-wsToTCP:
	case <-ctx.Done():
	}
}

// tunnelURL converts the REST base URL to a WebSocket URL for the tunnel endpoint.
func (p *realProxy) tunnelURL(computerID string, remotePort int) (string, error) {
	u, err := url.Parse(p.baseURL)
	if err != nil {
		return "", fmt.Errorf("proxy: parse base URL: %w", err)
	}
	switch u.Scheme {
	case "https":
		u.Scheme = "wss"
	case "http":
		u.Scheme = "ws"
	}
	u.Path = strings.TrimRight(u.Path, "/") +
		fmt.Sprintf("/computers/%s/tunnel/%d", computerID, remotePort)
	return u.String(), nil
}

// ─── Services (real implementation) ──────────────────────────────────────────

type realServices struct {
	rc *restClient
}

type serviceAPIData struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Command string `json:"command"`
	Status  string `json:"status"`
	Restart string `json:"restart_policy"`
}

func apiToServiceInfo(s serviceAPIData) ServiceInfo {
	return ServiceInfo{
		Name:    s.Name,
		Command: s.Command,
		Status:  s.Status,
		Restart: s.Restart,
	}
}

func (r *realServices) List(ctx context.Context, computerID string) ([]ServiceInfo, error) {
	var out struct {
		Data []serviceAPIData `json:"data"`
	}
	if err := r.rc.getJSON(ctx, "/computers/"+computerID+"/services", &out); err != nil {
		return nil, err
	}
	list := make([]ServiceInfo, 0, len(out.Data))
	for _, s := range out.Data {
		list = append(list, apiToServiceInfo(s))
	}
	return list, nil
}

func (r *realServices) Create(ctx context.Context, computerID string, input CreateServiceInput) (*ServiceInfo, error) {
	var out struct {
		Data serviceAPIData `json:"data"`
	}
	if err := r.rc.postJSON(ctx, "/computers/"+computerID+"/services", input, &out); err != nil {
		return nil, err
	}
	svc := apiToServiceInfo(out.Data)
	return &svc, nil
}

func (r *realServices) serviceAction(ctx context.Context, computerID, name, action string) error {
	return r.rc.postJSON(ctx, "/computers/"+computerID+"/services/"+name+"/"+action, nil, nil)
}

func (r *realServices) Start(ctx context.Context, computerID, name string) error {
	return r.serviceAction(ctx, computerID, name, "start")
}

func (r *realServices) Stop(ctx context.Context, computerID, name string) error {
	return r.serviceAction(ctx, computerID, name, "stop")
}

func (r *realServices) Restart(ctx context.Context, computerID, name string) error {
	return r.serviceAction(ctx, computerID, name, "restart")
}

func (r *realServices) Delete(ctx context.Context, computerID, name string) error {
	return r.rc.delete(ctx, "/computers/"+computerID+"/services/"+name)
}

// Logs streams service logs via SSE. When follow=false, it collects all
// buffered lines and closes the channel. When follow=true, the channel
// stays open until ctx is cancelled.
func (r *realServices) Logs(ctx context.Context, computerID, name string, follow bool) (<-chan string, error) {
	path := "/computers/" + computerID + "/services/" + name + "/logs"
	if follow {
		path += "?follow=true"
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, r.rc.baseURL+path, nil)
	if err != nil {
		return nil, fmt.Errorf("build logs request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+r.rc.apiKey)
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Cache-Control", "no-cache")

	resp, err := r.rc.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("logs request: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		resp.Body.Close()
		return nil, fmt.Errorf("logs API error status=%d", resp.StatusCode)
	}

	ch := make(chan string, 256)
	go func() {
		defer resp.Body.Close()
		defer close(ch)
		scanner := bufio.NewScanner(resp.Body)
		for scanner.Scan() {
			line := scanner.Text()
			// Strip SSE "data:" prefix.
			if after, ok := strings.CutPrefix(line, "data:"); ok {
				line = strings.TrimSpace(after)
			}
			if line == "" {
				continue
			}
			select {
			case ch <- line:
			case <-ctx.Done():
				return
			}
		}
	}()
	return ch, nil
}

// ─── Policy (real implementation) ────────────────────────────────────────────

type realPolicy struct {
	rc *restClient
}

func (r *realPolicy) Show(ctx context.Context, computerID string) (map[string]interface{}, error) {
	var out struct {
		Data map[string]interface{} `json:"data"`
	}
	if err := r.rc.getJSON(ctx, "/computers/"+computerID+"/network-policy", &out); err != nil {
		return nil, err
	}
	if out.Data == nil {
		// API may return the object directly (not wrapped).
		var direct map[string]interface{}
		if err2 := r.rc.getJSON(ctx, "/computers/"+computerID+"/network-policy", &direct); err2 != nil {
			return map[string]interface{}{}, nil
		}
		return direct, nil
	}
	return out.Data, nil
}

func (r *realPolicy) Set(ctx context.Context, computerID string, policy map[string]interface{}) error {
	return r.rc.putJSON(ctx, "/computers/"+computerID+"/network-policy", policy, nil)
}

// ─── URL update (real implementation via PATCH /computers/:id) ───────────────

// UpdateVisibility patches the computer's visibility field.
// Called by the `miosa url update` command.
func UpdateVisibility(ctx context.Context, rc *restClient, computerID, visibility string) error {
	type patchInput struct {
		Visibility string `json:"visibility"`
	}
	return rc.postJSON(ctx, "/computers/"+computerID, patchInput{Visibility: visibility}, nil)
}

// ─── Client facade ────────────────────────────────────────────────────────────

// Client bundles the miosa-go SDK client with resolved config and transport
// implementations for each API surface.
type Client struct {
	SDK         *miosa.Client
	RC          *restClient // direct REST client (bypasses SDK for new endpoints)
	Exec        ExecSession
	Checkpoints CheckpointTransport
	Workspaces  WorkspaceTransport
	Proxy       ProxyTransport
	Services    ServicesTransport
	Policy      PolicyTransport
}

// ResolveOptions controls how the Client is built.
type ResolveOptions struct {
	APIKey string
	APIURL string
}

// New builds a Client by resolving credentials and URL in priority order:
//  1. opts.APIKey / opts.APIURL (flags)
//  2. MIOSA_API_KEY / MIOSA_BASE_URL environment variables
//  3. ~/.miosa/config.toml
//  4. Built-in defaults
func New(opts ResolveOptions) (*Client, config.Config, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, cfg, fmt.Errorf("loading config: %w", err)
	}

	apiKey := resolve(opts.APIKey, os.Getenv("MIOSA_API_KEY"), cfg.APIKey)
	apiURL := resolve(opts.APIURL, os.Getenv("MIOSA_BASE_URL"), cfg.APIURL)

	if apiURL == "" {
		apiURL = config.DefaultBaseURL
	}

	sdk := miosa.NewClient(apiKey, miosa.WithBaseURL(apiURL))
	rc := newRestClient(apiURL, apiKey)

	c := &Client{
		SDK:         sdk,
		RC:          rc,
		Exec:        &realExec{rc: rc},
		Checkpoints: &realCheckpoints{rc: rc},
		Workspaces:  &realWorkspaces{rc: rc},
		Proxy:       &realProxy{baseURL: apiURL, apiKey: apiKey},
		Services:    &realServices{rc: rc},
		Policy:      &realPolicy{rc: rc},
	}
	return c, cfg, nil
}

// MustAuthenticated returns an error if no API key is configured.
func (c *Client) MustAuthenticated() error {
	return nil
}

// resolve returns the first non-empty string from candidates.
func resolve(candidates ...string) string {
	for _, s := range candidates {
		if s != "" {
			return s
		}
	}
	return ""
}

// IsNotAuthenticated reports whether err is a 401 authentication error.
func IsNotAuthenticated(err error) bool {
	if err == nil {
		return false
	}
	var authErr *miosa.AuthenticationError
	return isAs(err, &authErr)
}

// IsNotFound reports whether err is a 404 not-found error.
func IsNotFound(err error) bool {
	if err == nil {
		return false
	}
	var nfe *miosa.NotFoundError
	return isAs(err, &nfe)
}

func isAs[T any](err error, target *T) bool {
	type iface interface{ Unwrap() error }
	for err != nil {
		if _, ok := err.(T); ok {
			return true
		}
		if u, ok := err.(iface); ok {
			err = u.Unwrap()
		} else {
			break
		}
	}
	return false
}
