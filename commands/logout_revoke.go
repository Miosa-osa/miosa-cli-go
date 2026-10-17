package commands

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"

	"github.com/Miosa-osa/miosa-cli-go/internal/api"
	"github.com/Miosa-osa/miosa-cli-go/internal/client"
	"github.com/Miosa-osa/miosa-cli-go/internal/config"
)

// revokeStored ends, on the server, the keys and session a profile holds: its
// `miosa login` key (DELETE /cli/session), or for a session token the Forge key
// it minted for git and the token itself. Only what is stored in the profile is
// revoked: a key given with --api-key or MIOSA_API_KEY is never touched.
//
// It returns one line per key or session for the user, and the first error that
// left one active (nil when everything stored is now dead).
func revokeStored(ctx context.Context, prof config.Config) (notes []string, err error) {
	if prof.APIKey == "" {
		return nil, nil
	}
	// The same URL order as every other command: flag, MIOSA_BASE_URL, profile.
	apiURL := firstNonEmpty(globalFlags.APIURL, os.Getenv("MIOSA_BASE_URL"), prof.APIURL)
	c, _, cerr := client.New(client.ResolveOptions{APIKey: prof.APIKey, APIURL: apiURL, Retries: globalFlags.Retries})
	if cerr != nil {
		return nil, cerr
	}

	if prof.ForgeKeyID != "" && !isAPIKey(prof.APIKey) {
		switch ferr := c.API.Delete(ctx, "/api-keys/"+prof.ForgeKeyID, nil, nil); {
		case ferr == nil || api.IsStatus(ferr, http.StatusNotFound) || api.IsStatus(ferr, http.StatusUnauthorized):
			notes = append(notes, "Revoked the Forge key it minted for git.")
		default:
			notes = append(notes, fmt.Sprintf("The Forge key for git (%s) could not be revoked: %v", prof.ForgeKeyID, ferr))
			err = ferr
		}
	}

	if !isAPIKey(prof.APIKey) {
		switch serr := c.API.Post(ctx, "/auth/token/revoke", map[string]any{}, nil); {
		case serr == nil || api.IsStatus(serr, http.StatusUnauthorized):
			notes = append(notes, "Ended the browser session on the server.")
		default:
			notes = append(notes, fmt.Sprintf("The session could not be ended on the server: %v", serr))
			err = errors.Join(err, serr)
		}
		return notes, err
	}

	switch kerr := c.API.Delete(ctx, "/cli/session", nil, nil); {
	case kerr == nil:
		notes = append(notes, fmt.Sprintf("Revoked key %s on the server.", maskKey(prof.APIKey)))
	case api.IsStatus(kerr, http.StatusUnauthorized):
		// Already revoked or expired: nothing is left to end.
		notes = append(notes, fmt.Sprintf("Key %s was already inactive on the server.", maskKey(prof.APIKey)))
	case api.IsCode(kerr, "NOT_A_CLI_LOGIN_KEY"):
		// A key the user created and stored themselves (miosa config set, an
		// old profile): it is theirs to keep or revoke.
		notes = append(notes, fmt.Sprintf("Key %s was not created by 'miosa login', so it stays active; revoke it with 'miosa api-key revoke' if you no longer need it.", maskKey(prof.APIKey)))
	case api.IsStatus(kerr, http.StatusNotFound):
		notes = append(notes, fmt.Sprintf("This server cannot revoke key %s from the CLI; revoke it under Settings > CLI sessions.", maskKey(prof.APIKey)))
		err = kerr
	default:
		notes = append(notes, fmt.Sprintf("Key %s could not be revoked on the server: %v", maskKey(prof.APIKey), kerr))
		err = kerr
	}
	return notes, err
}
