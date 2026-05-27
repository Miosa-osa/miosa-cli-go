package miosa

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
)

// AdminService wraps the /api/v1/admin/* surface.
//
// Requires a msk_a_* / msk_p_* API key (or an admin JWT). Calls made
// with a user-role credential return 403 Forbidden.
//
// For endpoints not covered by the typed methods below, use Request
// to call any admin path directly.
type AdminService struct {
	client *Client
}

// Request is an escape hatch for admin endpoints that do not yet have a
// typed wrapper on AdminService. `body` may be nil; if non-nil it is
// marshalled as JSON. `out` receives the decoded response body and may be
// nil for calls that do not need a structured response.
func (s *AdminService) Request(ctx context.Context, method, path string, body, out interface{}) error {
	return s.client.sendJSON(ctx, method, path, body, out)
}

// ─── Overview ─────────────────────────────────────────────────────────────────

func (s *AdminService) Dashboard(ctx context.Context) (map[string]interface{}, error) {
	return s.getAny(ctx, "/admin/dashboard")
}

func (s *AdminService) Stats(ctx context.Context) (map[string]interface{}, error) {
	return s.getAny(ctx, "/admin/stats")
}

// AuditLogParams controls pagination of the audit log.
type AuditLogParams struct {
	Limit  int
	Cursor string
}

func (s *AdminService) AuditLog(ctx context.Context, p AuditLogParams) (map[string]interface{}, error) {
	q := map[string]string{}
	if p.Limit > 0 {
		q["limit"] = strconv.Itoa(p.Limit)
	}
	if p.Cursor != "" {
		q["cursor"] = p.Cursor
	}
	return s.getAny(ctx, "/admin/audit-log"+buildQuery(q))
}

func (s *AdminService) DetailedHealth(ctx context.Context) (map[string]interface{}, error) {
	return s.getAny(ctx, "/admin/health/detailed")
}

// ─── Credits ──────────────────────────────────────────────────────────────────

// GrantCreditsInput is the request body for granting credits to a tenant.
type GrantCreditsInput struct {
	TenantID    string `json:"tenant_id"`
	Amount      int    `json:"amount"`
	Description string `json:"description"`
	ExpiresAt   string `json:"expires_at,omitempty"`
}

func (s *AdminService) GrantCredits(ctx context.Context, input GrantCreditsInput) (map[string]interface{}, error) {
	var out map[string]interface{}
	if err := s.client.postJSON(ctx, "/admin/credits/grant", input, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// DeductCreditsInput is the request body for deducting credits.
type DeductCreditsInput struct {
	TenantID    string `json:"tenant_id"`
	Amount      int    `json:"amount"`
	Description string `json:"description"`
}

func (s *AdminService) DeductCredits(ctx context.Context, input DeductCreditsInput) (map[string]interface{}, error) {
	var out map[string]interface{}
	if err := s.client.postJSON(ctx, "/admin/credits/deduct", input, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// RefundCreditsInput is the request body for refunding credits.
type RefundCreditsInput struct {
	TenantID      string `json:"tenant_id"`
	Amount        int    `json:"amount"`
	Description   string `json:"description"`
	TransactionID string `json:"transaction_id,omitempty"`
}

func (s *AdminService) RefundCredits(ctx context.Context, input RefundCreditsInput) (map[string]interface{}, error) {
	var out map[string]interface{}
	if err := s.client.postJSON(ctx, "/admin/credits/refund", input, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func (s *AdminService) TenantBalance(ctx context.Context, tenantID string) (map[string]interface{}, error) {
	return s.getAny(ctx, fmt.Sprintf("/admin/credits/%s/balance", tenantID))
}

func (s *AdminService) TenantCreditHistory(ctx context.Context, tenantID string, p AuditLogParams) (map[string]interface{}, error) {
	q := map[string]string{}
	if p.Limit > 0 {
		q["limit"] = strconv.Itoa(p.Limit)
	}
	if p.Cursor != "" {
		q["cursor"] = p.Cursor
	}
	return s.getAny(ctx, fmt.Sprintf("/admin/credits/%s/history%s", tenantID, buildQuery(q)))
}

// ─── Users ────────────────────────────────────────────────────────────────────

// ListAdminUsersParams filters and paginates GET /admin/users.
type ListAdminUsersParams struct {
	Limit  int
	Cursor string
	Query  string
	Status string // "active" | "suspended" | "deleted"
}

func (s *AdminService) ListUsers(ctx context.Context, p ListAdminUsersParams) (map[string]interface{}, error) {
	q := map[string]string{}
	if p.Limit > 0 {
		q["limit"] = strconv.Itoa(p.Limit)
	}
	if p.Cursor != "" {
		q["cursor"] = p.Cursor
	}
	if p.Query != "" {
		q["q"] = p.Query
	}
	if p.Status != "" {
		q["status"] = p.Status
	}
	return s.getAny(ctx, "/admin/users"+buildQuery(q))
}

func (s *AdminService) GetUser(ctx context.Context, userID string) (map[string]interface{}, error) {
	return s.getAny(ctx, "/admin/users/"+userID)
}

// UpdateUser sends a PUT to /admin/users/:id with an arbitrary attribute map.
func (s *AdminService) UpdateUser(ctx context.Context, userID string, attrs map[string]interface{}) (map[string]interface{}, error) {
	var out map[string]interface{}
	if err := s.client.putJSON(ctx, "/admin/users/"+userID, attrs, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func (s *AdminService) DeleteUser(ctx context.Context, userID string) error {
	return s.client.deleteJSON(ctx, "/admin/users/"+userID, nil)
}

func (s *AdminService) ChangeUserRole(ctx context.Context, userID, role string) (map[string]interface{}, error) {
	var out map[string]interface{}
	if err := s.client.postJSON(ctx, "/admin/users/"+userID+"/role", map[string]string{"role": role}, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func (s *AdminService) ForceLogout(ctx context.Context, userID string) error {
	return s.client.postJSON(ctx, "/admin/users/"+userID+"/force-logout", nil, nil)
}

func (s *AdminService) SuspendUser(ctx context.Context, userID, reason string) error {
	var body interface{}
	if reason != "" {
		body = map[string]string{"reason": reason}
	}
	return s.client.postJSON(ctx, "/admin/users/"+userID+"/suspend", body, nil)
}

func (s *AdminService) UnsuspendUser(ctx context.Context, userID string) error {
	return s.client.postJSON(ctx, "/admin/users/"+userID+"/unsuspend", nil, nil)
}

// BanUserInput is the request body for banning a user.
type BanUserInput struct {
	Reason    string `json:"reason"`
	ExpiresAt string `json:"expires_at,omitempty"`
}

func (s *AdminService) BanUser(ctx context.Context, userID string, input BanUserInput) error {
	return s.client.postJSON(ctx, "/admin/users/"+userID+"/ban", input, nil)
}

func (s *AdminService) UnbanUser(ctx context.Context, userID string) error {
	return s.client.postJSON(ctx, "/admin/users/"+userID+"/unban", nil, nil)
}

// BulkUserActionInput is the request body for /admin/users/bulk.
type BulkUserActionInput struct {
	UserIDs []string               `json:"user_ids"`
	Action  string                 `json:"action"` // "suspend" | "unsuspend" | "delete" | "tag" | "notify"
	Params  map[string]interface{} `json:"params,omitempty"`
}

func (s *AdminService) BulkUserAction(ctx context.Context, input BulkUserActionInput) (map[string]interface{}, error) {
	var out map[string]interface{}
	if err := s.client.postJSON(ctx, "/admin/users/bulk", input, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// ─── Tenants ──────────────────────────────────────────────────────────────────

// ListAdminTenantsParams filters and paginates GET /admin/tenants.
type ListAdminTenantsParams struct {
	Limit  int
	Cursor string
	Query  string
}

func (s *AdminService) ListTenants(ctx context.Context, p ListAdminTenantsParams) (map[string]interface{}, error) {
	q := map[string]string{}
	if p.Limit > 0 {
		q["limit"] = strconv.Itoa(p.Limit)
	}
	if p.Cursor != "" {
		q["cursor"] = p.Cursor
	}
	if p.Query != "" {
		q["q"] = p.Query
	}
	return s.getAny(ctx, "/admin/tenants"+buildQuery(q))
}

func (s *AdminService) TenantDetail(ctx context.Context, tenantID string) (map[string]interface{}, error) {
	return s.getAny(ctx, "/admin/tenants/"+tenantID+"/detail")
}

func (s *AdminService) SuspendTenant(ctx context.Context, tenantID, reason string) error {
	var body interface{}
	if reason != "" {
		body = map[string]string{"reason": reason}
	}
	return s.client.postJSON(ctx, "/admin/tenants/"+tenantID+"/suspend", body, nil)
}

func (s *AdminService) UnsuspendTenant(ctx context.Context, tenantID string) error {
	return s.client.postJSON(ctx, "/admin/tenants/"+tenantID+"/unsuspend", nil, nil)
}

// ChangeTenantPlanInput is the request body for /admin/tenants/:id/plan.
type ChangeTenantPlanInput struct {
	Plan    string `json:"plan"`              // "free" | "starter" | "pro" | "scale"
	Prorate bool   `json:"prorate,omitempty"` // defaults to true server-side
}

func (s *AdminService) ChangeTenantPlan(ctx context.Context, tenantID string, input ChangeTenantPlanInput) (map[string]interface{}, error) {
	var out map[string]interface{}
	if err := s.client.postJSON(ctx, "/admin/tenants/"+tenantID+"/plan", input, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func (s *AdminService) DeleteTenant(ctx context.Context, tenantID string) error {
	return s.client.deleteJSON(ctx, "/admin/tenants/"+tenantID, nil)
}

// ─── Computers ────────────────────────────────────────────────────────────────

// ListAdminComputersParams filters and paginates GET /admin/computers.
type ListAdminComputersParams struct {
	Limit    int
	Cursor   string
	Status   string
	TenantID string
}

func (s *AdminService) ListComputers(ctx context.Context, p ListAdminComputersParams) (map[string]interface{}, error) {
	q := map[string]string{}
	if p.Limit > 0 {
		q["limit"] = strconv.Itoa(p.Limit)
	}
	if p.Cursor != "" {
		q["cursor"] = p.Cursor
	}
	if p.Status != "" {
		q["status"] = p.Status
	}
	if p.TenantID != "" {
		q["tenant_id"] = p.TenantID
	}
	return s.getAny(ctx, "/admin/computers"+buildQuery(q))
}

func (s *AdminService) DeleteComputer(ctx context.Context, computerID string) error {
	return s.client.deleteJSON(ctx, "/admin/computers/"+computerID, nil)
}

func (s *AdminService) SuspendComputer(ctx context.Context, computerID string) error {
	return s.client.postJSON(ctx, "/admin/computers/"+computerID+"/suspend", nil, nil)
}

func (s *AdminService) ResumeComputer(ctx context.Context, computerID string) error {
	return s.client.postJSON(ctx, "/admin/computers/"+computerID+"/resume", nil, nil)
}

func (s *AdminService) RestartComputer(ctx context.Context, computerID string) error {
	return s.client.postJSON(ctx, "/admin/computers/"+computerID+"/restart", nil, nil)
}

func (s *AdminService) PurgeStaleComputers(ctx context.Context) (map[string]interface{}, error) {
	var out map[string]interface{}
	if err := s.client.postJSON(ctx, "/admin/computers/purge-stale", nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// ─── API Keys ─────────────────────────────────────────────────────────────────

// ListAdminApiKeysParams filters and paginates GET /admin/api-keys.
type ListAdminApiKeysParams struct {
	Limit    int
	Cursor   string
	TenantID string
	Status   string
}

func (s *AdminService) ListApiKeys(ctx context.Context, p ListAdminApiKeysParams) (map[string]interface{}, error) {
	q := map[string]string{}
	if p.Limit > 0 {
		q["limit"] = strconv.Itoa(p.Limit)
	}
	if p.Cursor != "" {
		q["cursor"] = p.Cursor
	}
	if p.TenantID != "" {
		q["tenant_id"] = p.TenantID
	}
	if p.Status != "" {
		q["status"] = p.Status
	}
	return s.getAny(ctx, "/admin/api-keys"+buildQuery(q))
}

// CreateAdminApiKeyInput is the request body for POST /admin/api-keys.
type CreateAdminApiKeyInput struct {
	Name         string   `json:"name"`
	TenantID     string   `json:"tenant_id"`
	UserID       string   `json:"user_id"`
	KeyType      string   `json:"key_type"`          // "user" | "admin" | "platform"
	Purpose      string   `json:"purpose,omitempty"` // "api" | "optimal"
	RateLimitRpm int      `json:"rate_limit_rpm,omitempty"`
	ExpiresAt    string   `json:"expires_at,omitempty"`
	AllowedIPs   []string `json:"allowed_ips,omitempty"`
}

func (s *AdminService) CreateApiKey(ctx context.Context, input CreateAdminApiKeyInput) (map[string]interface{}, error) {
	if input.KeyType == "" {
		input.KeyType = "user"
	}
	if input.Purpose == "" {
		input.Purpose = "api"
	}
	var out map[string]interface{}
	if err := s.client.postJSON(ctx, "/admin/api-keys", input, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func (s *AdminService) ApiKeyStats(ctx context.Context) (map[string]interface{}, error) {
	return s.getAny(ctx, "/admin/api-keys/stats")
}

func (s *AdminService) BulkRevokeApiKeys(ctx context.Context, keyIDs []string) (map[string]interface{}, error) {
	var out map[string]interface{}
	if err := s.client.postJSON(ctx, "/admin/api-keys/bulk-revoke", map[string][]string{"key_ids": keyIDs}, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func (s *AdminService) RevokeApiKey(ctx context.Context, keyID string) error {
	return s.client.deleteJSON(ctx, "/admin/api-keys/"+keyID, nil)
}

// ─── Optimal ──────────────────────────────────────────────────────────────────

func (s *AdminService) OptimalStatus(ctx context.Context) (map[string]interface{}, error) {
	return s.getAny(ctx, "/admin/optimal/status")
}

func (s *AdminService) ListOptimalModels(ctx context.Context) (map[string]interface{}, error) {
	return s.getAny(ctx, "/admin/optimal/models")
}

func (s *AdminService) SwitchOptimalModel(ctx context.Context, modelID string) (map[string]interface{}, error) {
	var out map[string]interface{}
	if err := s.client.postJSON(ctx, "/admin/optimal/models/switch", map[string]string{"model_id": modelID}, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// ─── helpers ──────────────────────────────────────────────────────────────────

// getAny issues GET and decodes into a generic map.
func (s *AdminService) getAny(ctx context.Context, path string) (map[string]interface{}, error) {
	resp, err := s.client.do(ctx, "GET", path, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var out map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return out, nil
}
