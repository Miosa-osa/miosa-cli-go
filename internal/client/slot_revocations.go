package client

import (
	"context"
	"net/http"
	"strings"
)

// SlotRevocation is one revoked producer bake revision.
//
// A revocation is the supported replacement for a manual sweep after a bad
// bake: every compute host refuses to acquire or warm-claim a slot whose
// manifest names a revoked revision, and a reaper retires the idle slots cut
// from it.
type SlotRevocation struct {
	ID               string `json:"id"`
	Revision         string `json:"revision"`
	Reason           string `json:"reason,omitempty"`
	RevokedByUserID  string `json:"revoked_by_user_id,omitempty"`
	InsertedAt       string `json:"inserted_at,omitempty"`
	AlreadyRevokedAt string `json:"-"`
}

// ListSlotRevocations reads every revoked producer revision.
func (c *Client) ListSlotRevocations(ctx context.Context) ([]SlotRevocation, error) {
	var out struct {
		Data  []SlotRevocation `json:"data"`
		Total int              `json:"total"`
	}

	if err := c.RC.call(ctx, http.MethodGet, "/admin/slot-producer-revocations", nil, &out); err != nil {
		return nil, err
	}

	return out.Data, nil
}

// RevokeSlotRevision revokes one producer revision. It returns the stored
// revocation and the outcome: "revoked" for a new one, "already_revoked" when
// it was already in the ledger, which is not an error.
func (c *Client) RevokeSlotRevision(ctx context.Context, revision, reason string) (*SlotRevocation, string, error) {
	body := map[string]string{"revision": strings.TrimSpace(revision)}
	if trimmed := strings.TrimSpace(reason); trimmed != "" {
		body["reason"] = trimmed
	}

	var out struct {
		Data    SlotRevocation `json:"data"`
		Outcome string         `json:"outcome"`
	}

	if err := c.RC.call(ctx, http.MethodPost, "/admin/slot-producer-revocations", body, &out); err != nil {
		return nil, "", err
	}

	return &out.Data, out.Outcome, nil
}
