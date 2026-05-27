package miosa

import (
	"context"
	"fmt"
	"net/http"
)

// CustomDomainsService manages custom domain mappings for a computer.
// Accessed via Computer.Domains.
type CustomDomainsService struct {
	client     *Client
	computerID string
}

func (s *CustomDomainsService) base() string {
	return fmt.Sprintf("/computers/%s/domains", s.computerID)
}

// Register maps a custom FQDN to this computer.
// The returned domain starts in "pending" status. Use the instructions field
// to configure the required CNAME in your DNS registrar, then call Verify.
func (s *CustomDomainsService) Register(ctx context.Context, fqdn string) (*CustomDomain, error) {
	const op = "CustomDomainsService.Register"
	var out struct {
		Data CustomDomain `json:"data"`
	}
	if err := s.client.postJSON(ctx, s.base(), map[string]string{"fqdn": fqdn}, &out); err != nil {
		return nil, fmt.Errorf("%s: %w", op, err)
	}
	return &out.Data, nil
}

// List returns all custom domains registered for this computer.
func (s *CustomDomainsService) List(ctx context.Context) ([]CustomDomain, error) {
	const op = "CustomDomainsService.List"
	var out struct {
		Data []CustomDomain `json:"data"`
	}
	if err := s.client.getJSON(ctx, s.base(), &out); err != nil {
		return nil, fmt.Errorf("%s: %w", op, err)
	}
	return out.Data, nil
}

// Verify confirms DNS ownership of a registered domain.
// The control plane resolves the CNAME and confirms it points to
// verification_target. On success the domain transitions to "verified" and
// Caddy auto-issues a TLS certificate.
func (s *CustomDomainsService) Verify(ctx context.Context, id string) (*CustomDomain, error) {
	const op = "CustomDomainsService.Verify"
	var out struct {
		Data CustomDomain `json:"data"`
	}
	if err := s.client.postJSON(ctx, fmt.Sprintf("%s/%s/verify", s.base(), id), nil, &out); err != nil {
		return nil, fmt.Errorf("%s: %w", op, err)
	}
	return &out.Data, nil
}

// Delete removes a custom domain mapping.
// The domain is immediately removed from the routing cache.
func (s *CustomDomainsService) Delete(ctx context.Context, id string) error {
	const op = "CustomDomainsService.Delete"
	if err := s.client.sendJSON(ctx, http.MethodDelete, fmt.Sprintf("%s/%s", s.base(), id), nil, nil); err != nil {
		return fmt.Errorf("%s: %w", op, err)
	}
	return nil
}
