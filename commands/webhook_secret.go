package commands

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"

	"github.com/spf13/cobra"
)

// webhookRotateSecretOp replaces a webhook's signing secret with a new random
// one (PATCH /webhooks/:id {secret}) and prints it once: the API never returns
// a stored secret. Deliveries are signed with the new secret from then on.
func webhookRotateSecretOp(hook Arg) Op {
	var secret string
	return Op{
		Use: "rotate-secret <id>", Short: "Replace the signing secret and print the new one once",
		Long: `Replace the webhook's signing secret with a new random one and print it once.
Every delivery after this is signed with the new secret, so update your
endpoint's verification first or accept both while you switch.`,
		Method: "PATCH", Path: "/webhooks/{0}", Args: []Arg{hook},
		Confirm: "Replace the signing secret of webhook {0}? Deliveries are signed with the new one at once.",
		Body: func(_ []string, body map[string]any) (map[string]any, error) {
			buf := make([]byte, 32)
			if _, err := rand.Read(buf); err != nil {
				return nil, fmt.Errorf("generating a secret: %w", err)
			}
			secret = "whsec_" + hex.EncodeToString(buf)
			body["secret"] = secret
			return body, nil
		},
		After: func(cmd *cobra.Command, resp any) error {
			if isJSON() {
				if obj, ok := unwrapObject(resp).(map[string]any); ok {
					obj["secret"] = secret
				}
				return nil
			}
			p := printerFor(cmd)
			p.Success("New signing secret. It is shown once - store it now:")
			fmt.Fprintf(p.Writer(), "\n  %s\n\n", secret)
			return nil
		},
		Detail: webhookDetail,
	}
}
