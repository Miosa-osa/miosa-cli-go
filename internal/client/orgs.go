package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// This file is the CLI's view of "which organization pays for new machines":
// GET/PUT/DELETE /bill-to, GET /limits, per-member caps, and the organization
// member and invite calls that `miosa org` needs. It talks to the REST client
// directly so API refusals keep their code and message instead of becoming a
// bare status line.

// APIError is a refusal from the MIOSA API with its machine-readable code.
type APIError struct {
	Status  int
	Code    string
	Message string
}

func (e *APIError) Error() string {
	switch {
	case e.Message != "" && e.Code != "":
		return fmt.Sprintf("%s (%s)", e.Message, e.Code)
	case e.Message != "":
		return e.Message
	case e.Code != "":
		return e.Code
	default:
		return fmt.Sprintf("request failed (%d)", e.Status)
	}
}

// BillToOrganization is one organization the caller may choose to bill.
type BillToOrganization struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Slug    string `json:"slug"`
	Type    string `json:"type"`
	Role    string `json:"role"`
	Viewing bool   `json:"viewing"`
	Billing bool   `json:"billing"`
	Pinned  bool   `json:"pinned"`
}

// BillTo is what a create would bill right now, and the choices.
type BillTo struct {
	Billing struct {
		ID   string `json:"id"`
		Name string `json:"name"`
		Type string `json:"type"`
		Role string `json:"role"`
	} `json:"billing"`
	Source        string               `json:"source"`
	SettingPinned bool                 `json:"setting_pinned"`
	SettingStale  bool                 `json:"setting_stale"`
	ViewingID     string               `json:"viewing_id"`
	CanChange     bool                 `json:"can_change"`
	Options       []BillToOrganization `json:"options"`
}

// Limits is the limits of the organization a create would bill.
type Limits struct {
	Organization struct {
		ID   string `json:"id"`
		Name string `json:"name"`
		Type string `json:"type"`
		Role string `json:"role"`
	} `json:"organization"`
	Source         string   `json:"source"`
	CanStart       bool     `json:"can_start"`
	BlockedReasons []string `json:"blocked_reasons"`
	Credits        struct {
		BalanceCents   *int64 `json:"balance_cents"`
		AvailableCents int64  `json:"available_cents"`
	} `json:"credits"`
	Plan struct {
		Name string `json:"name"`
	} `json:"plan"`
	Concurrency struct {
		Limit     *int64 `json:"limit"`
		Running   int64  `json:"running"`
		Remaining *int64 `json:"remaining"`
	} `json:"concurrency"`
	Spend struct {
		Mode           string `json:"mode"`
		CapCents       *int64 `json:"cap_cents"`
		AccountedCents int64  `json:"accounted_cents"`
		Status         string `json:"status"`
	} `json:"spend"`
	Member *struct {
		UsageCapCents          *int64 `json:"usage_cap_cents"`
		UsageCents             int64  `json:"usage_cents"`
		MaxConcurrentSandboxes *int64 `json:"max_concurrent_sandboxes"`
		RunningSandboxes       int64  `json:"running_sandboxes"`
	} `json:"member"`
}

// MemberCap is one member's caps and live usage inside an organization.
type MemberCap struct {
	UserID                 string `json:"user_id"`
	Email                  string `json:"email"`
	Name                   string `json:"name"`
	Role                   string `json:"role"`
	UsageCapCents          *int64 `json:"usage_cap_cents"`
	MaxConcurrentSandboxes *int64 `json:"max_concurrent_sandboxes"`
	UsageCents             int64  `json:"usage_cents"`
	RunningSandboxes       int64  `json:"running_sandboxes"`
}

// MemberCapUpdate carries only the fields to change. A nil pointer keeps the
// field; a pointer to a Clear value removes the cap.
type MemberCapUpdate struct {
	UsageCapCents          *int64
	ClearUsageCap          bool
	MaxConcurrentSandboxes *int64
	ClearConcurrency       bool
}

// OrgMember is one row of an organization's member list.
type OrgMember struct {
	UserID    string `json:"user_id"`
	UserName  string `json:"user_name"`
	UserEmail string `json:"user_email"`
	Role      string `json:"role"`
	Status    string `json:"status"`
}

// OrgInvite is the result of inviting someone.
type OrgInvite struct {
	InviteID  string `json:"invite_id"`
	Email     string `json:"email"`
	Role      string `json:"role"`
	ExpiresAt string `json:"expires_at"`
	InviteURL string `json:"invite_url"`
}

// call performs one request and decodes the JSON body into out (when non-nil).
// A non-2xx answer becomes an *APIError built from the platform's error shapes:
// {"error":{"code","message"}} and {"error":"code","message"}.
func (rc *restClient) call(ctx context.Context, method, path string, body, out interface{}) error {
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("marshal request: %w", err)
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, rc.baseURL+path, reader)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+rc.apiKey)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := rc.http.Do(req)
	if err != nil {
		return fmt.Errorf("cannot reach the MIOSA API: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return parseAPIError(resp.StatusCode, raw)
	}
	if out == nil || len(bytes.TrimSpace(raw)) == 0 {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("unexpected response from %s %s: %w", method, path, err)
	}
	return nil
}

func parseAPIError(status int, raw []byte) *APIError {
	apiErr := &APIError{Status: status}
	var envelope struct {
		Error   json.RawMessage `json:"error"`
		Message string          `json:"message"`
	}
	if json.Unmarshal(raw, &envelope) != nil {
		return apiErr
	}
	apiErr.Message = envelope.Message
	var nested struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}
	if json.Unmarshal(envelope.Error, &nested) == nil && (nested.Code != "" || nested.Message != "") {
		apiErr.Code = nested.Code
		if nested.Message != "" {
			apiErr.Message = nested.Message
		}
		return apiErr
	}
	var code string
	if json.Unmarshal(envelope.Error, &code) == nil {
		apiErr.Code = code
	}
	return apiErr
}

type dataEnvelope[T any] struct {
	Data T `json:"data"`
}

// GetBillTo reads the organization a create would bill, and the choices.
func (c *Client) GetBillTo(ctx context.Context) (*BillTo, error) {
	var out dataEnvelope[BillTo]
	if err := c.RC.call(ctx, http.MethodGet, "/bill-to", nil, &out); err != nil {
		return nil, err
	}
	return &out.Data, nil
}

// SetBillTo pins an organization (id, slug or name) account-wide.
func (c *Client) SetBillTo(ctx context.Context, org string) (*BillTo, error) {
	var out dataEnvelope[BillTo]
	body := map[string]string{"org": org}
	if err := c.RC.call(ctx, http.MethodPut, "/bill-to", body, &out); err != nil {
		return nil, err
	}
	return &out.Data, nil
}

// ClearBillTo goes back to billing the organization the credential is in.
func (c *Client) ClearBillTo(ctx context.Context) (*BillTo, error) {
	var out dataEnvelope[BillTo]
	if err := c.RC.call(ctx, http.MethodDelete, "/bill-to", nil, &out); err != nil {
		return nil, err
	}
	return &out.Data, nil
}

// GetLimits reads the limits of the organization a create would bill.
func (c *Client) GetLimits(ctx context.Context) (*Limits, error) {
	var out dataEnvelope[Limits]
	if err := c.RC.call(ctx, http.MethodGet, "/limits", nil, &out); err != nil {
		return nil, err
	}
	return &out.Data, nil
}

// ListMemberCaps lists every member of an organization with caps and usage.
func (c *Client) ListMemberCaps(ctx context.Context, tenantID string) ([]MemberCap, string, error) {
	var out dataEnvelope[struct {
		Members   []MemberCap `json:"members"`
		WindowEnd string      `json:"window_end"`
	}]
	path := "/tenants/" + url.PathEscape(tenantID) + "/member-caps"
	if err := c.RC.call(ctx, http.MethodGet, path, nil, &out); err != nil {
		return nil, "", err
	}
	return out.Data.Members, out.Data.WindowEnd, nil
}

// UpdateMemberCap changes the named fields of one member's caps.
func (c *Client) UpdateMemberCap(ctx context.Context, tenantID, userID string, update MemberCapUpdate) (*MemberCap, error) {
	body := map[string]interface{}{}
	switch {
	case update.ClearUsageCap:
		body["usage_cap_cents"] = nil
	case update.UsageCapCents != nil:
		body["usage_cap_cents"] = *update.UsageCapCents
	}
	switch {
	case update.ClearConcurrency:
		body["max_concurrent_sandboxes"] = nil
	case update.MaxConcurrentSandboxes != nil:
		body["max_concurrent_sandboxes"] = *update.MaxConcurrentSandboxes
	}
	var out dataEnvelope[MemberCap]
	path := "/tenants/" + url.PathEscape(tenantID) + "/member-caps/" + url.PathEscape(userID)
	if err := c.RC.call(ctx, http.MethodPut, path, body, &out); err != nil {
		return nil, err
	}
	return &out.Data, nil
}

// ClearMemberCap removes both of one member's caps.
func (c *Client) ClearMemberCap(ctx context.Context, tenantID, userID string) (*MemberCap, error) {
	var out dataEnvelope[MemberCap]
	path := "/tenants/" + url.PathEscape(tenantID) + "/member-caps/" + url.PathEscape(userID)
	if err := c.RC.call(ctx, http.MethodDelete, path, nil, &out); err != nil {
		return nil, err
	}
	return &out.Data, nil
}

// ListOrgMembers lists the members of an organization.
func (c *Client) ListOrgMembers(ctx context.Context, tenantID string) ([]OrgMember, error) {
	var out struct {
		Members []OrgMember `json:"members"`
	}
	path := "/tenants/" + url.PathEscape(tenantID) + "/members"
	if err := c.RC.call(ctx, http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}
	return out.Members, nil
}

// InviteOrgMember invites an email address to an organization.
func (c *Client) InviteOrgMember(ctx context.Context, tenantID, email, role string) (*OrgInvite, error) {
	body := map[string]string{"email": strings.TrimSpace(email)}
	if role != "" {
		body["role"] = role
	}
	var out dataEnvelope[OrgInvite]
	path := "/tenants/" + url.PathEscape(tenantID) + "/invites"
	if err := c.RC.call(ctx, http.MethodPost, path, body, &out); err != nil {
		return nil, err
	}
	return &out.Data, nil
}
