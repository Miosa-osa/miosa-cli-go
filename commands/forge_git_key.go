package commands

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"

	"github.com/Miosa-osa/miosa-cli-go/internal/client"
	"github.com/Miosa-osa/miosa-cli-go/internal/config"
)

// Git credentials for a CLI signed in with a browser session token, the way gh
// hands git a token after `gh auth login`.
//
// The Forge git server takes an API key as the password, never a session
// token: every push records the key that made it. So a CLI holding a session
// token mints a Forge-only key with it once (forge:read, forge:write), keeps it
// in the profile bound to the user and organization that minted it, and hands
// git that. When git reports the key rejected (`erase`, for example after it
// was revoked) the profile forgets it and the next request mints a new one.

var forgeGitScopes = []string{"forge:read", "forge:write"}

// isAPIKey tells a MIOSA API key from a session token.
func isAPIKey(key string) bool { return strings.HasPrefix(key, "msk_") }

type gitIdentity struct{ user, org string }

func sessionIdentity(ctx context.Context, c *client.Client) (gitIdentity, error) {
	var who struct {
		User struct {
			ID string `json:"id"`
		} `json:"user"`
		Organization struct {
			ID string `json:"id"`
		} `json:"organization"`
	}
	if err := c.API.Get(ctx, "/whoami", nil, &who); err != nil {
		return gitIdentity{}, err
	}
	if who.User.ID == "" || who.Organization.ID == "" {
		return gitIdentity{}, errors.New("the server did not say who this session is")
	}
	return gitIdentity{user: who.User.ID, org: who.Organization.ID}, nil
}

// forgeGitKey is the password git gets for Forge: the CLI's own API key, or
// for a session token the profile's Forge key, minted on first use. minted
// reports a new key.
func forgeGitKey(ctx context.Context, c *client.Client) (key string, minted bool, err error) {
	if isAPIKey(c.Key) {
		return c.Key, false, nil
	}
	id, err := sessionIdentity(ctx, c)
	if err != nil {
		return "", false, err
	}
	if cfg, _ := config.Load(); cfg.ForgeKey != "" && cfg.ForgeKeyUser == id.user && cfg.ForgeKeyOrg == id.org {
		return cfg.ForgeKey, false, nil
	}
	host, _ := os.Hostname()
	var resp struct {
		Data struct {
			ID  string `json:"id"`
			Key string `json:"key"`
		} `json:"data"`
	}
	body := map[string]any{"name": "MIOSA git on " + orDefault(host, "this computer"), "scopes": forgeGitScopes}
	if err := c.API.Post(ctx, "/api-keys", body, &resp); err != nil {
		return "", false, fmt.Errorf("minting a Forge key for git: %w", err)
	}
	if !isAPIKey(resp.Data.Key) {
		return "", false, errors.New("minting a Forge key for git: the server returned no key")
	}
	err = updateProfile(func(p *config.Config) {
		p.ForgeKey, p.ForgeKeyID, p.ForgeKeyUser, p.ForgeKeyOrg = resp.Data.Key, resp.Data.ID, id.user, id.org
	})
	if err != nil {
		return "", false, fmt.Errorf("saving the Forge key for git: %w", err)
	}
	return resp.Data.Key, true, nil
}

// forgetForgeGitKey drops the profile's Forge key when it is the password git
// just reported rejected, and revokes it best-effort so it does not count
// against the organization's key limit.
func forgetForgeGitKey(ctx context.Context, c *client.Client, password string) {
	cfg, err := config.Load()
	if err != nil || cfg.ForgeKey == "" || cfg.ForgeKey != password {
		return
	}
	if cfg.ForgeKeyID != "" && c != nil && !isAPIKey(c.Key) {
		_ = c.API.Delete(ctx, "/api-keys/"+url.PathEscape(cfg.ForgeKeyID), nil, nil)
	}
	_ = updateProfile(func(p *config.Config) {
		p.ForgeKey, p.ForgeKeyID, p.ForgeKeyUser, p.ForgeKeyOrg = "", "", "", ""
	})
}

func updateProfile(change func(*config.Config)) error {
	f, err := config.LoadFile()
	if err != nil {
		return err
	}
	name := f.ActiveName()
	cur, _ := f.Get(name)
	change(&cur)
	f.Set(name, cur)
	return config.SaveFile(f)
}
