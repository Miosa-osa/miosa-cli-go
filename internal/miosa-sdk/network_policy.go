package miosa

import (
	"context"
	"fmt"
	"net/http"
)

// NetworkPolicyService manages egress network policy for a computer.
// Rules are evaluated top-to-bottom with a default effect applied when none
// match. Accessed via Computer.NetworkPolicy.
//
// Block cloud IMDS while allowing all other egress:
//
//	err := computer.NetworkPolicy.Set(ctx, SetNetworkPolicyInput{
//	    DefaultEffect: NetworkEffectAllow,
//	    Rules: []NetworkPolicyRule{
//	        {Effect: NetworkEffectDeny, Destination: "169.254.169.254/32"},
//	    },
//	})
type NetworkPolicyService struct {
	client     *Client
	computerID string
}

func (s *NetworkPolicyService) base() string {
	return fmt.Sprintf("/computers/%s/network-policy", s.computerID)
}

// Get returns the current network policy.
// Returns an allow-all policy if none has been set.
func (s *NetworkPolicyService) Get(ctx context.Context) (*NetworkPolicy, error) {
	const op = "NetworkPolicyService.Get"
	var out struct {
		Data NetworkPolicy `json:"data"`
	}
	if err := s.client.getJSON(ctx, s.base(), &out); err != nil {
		return nil, fmt.Errorf("%s: %w", op, err)
	}
	return &out.Data, nil
}

// Set replaces the network policy entirely.
// The new rules take effect immediately — running VMs pick them up without a
// restart via a host-side nftables update.
func (s *NetworkPolicyService) Set(ctx context.Context, input SetNetworkPolicyInput) (*NetworkPolicy, error) {
	const op = "NetworkPolicyService.Set"
	var out struct {
		Data NetworkPolicy `json:"data"`
	}
	if err := s.client.putJSON(ctx, s.base(), input, &out); err != nil {
		return nil, fmt.Errorf("%s: %w", op, err)
	}
	return &out.Data, nil
}

// Reset reverts the policy to the default allow-all state. Idempotent.
func (s *NetworkPolicyService) Reset(ctx context.Context) error {
	const op = "NetworkPolicyService.Reset"
	if err := s.client.sendJSON(ctx, http.MethodDelete, s.base(), nil, nil); err != nil {
		return fmt.Errorf("%s: %w", op, err)
	}
	return nil
}
