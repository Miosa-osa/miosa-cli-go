package commands

import (
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"regexp"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"
)

var forgeSecretName = regexp.MustCompile(`^[A-Z][A-Z0-9_]{0,63}$`)

// forgeSecretCmd manages a repository's secrets: write-only values a Forge
// Automation names in `secrets:` (for example a registry token for a publish
// workflow). Nothing reads a value back; the list shows names only.
func forgeSecretCmd() *cobra.Command {
	repo := Arg{Name: "repo"}
	cmd := group("secret", "Repository secrets for Forge Automations and deployments",
		`A secret is written once and never shown again. A Forge Automation receives the
ones it names in .miosa/automations.yml (secrets: [NPM_TOKEN]) on a tag or the
default branch only. Setting a secret needs the admin role on the repository.

  miosa forge secret set miosa NPM_TOKEN          (paste the value; input hidden)
  printf '%s' "$TOKEN" | miosa forge secret set miosa NPM_TOKEN
  miosa forge secret list miosa
  miosa forge secret delete miosa NPM_TOKEN`, []string{"secrets"},
		Op{Use: "list <repo>", Aliases: []string{"ls"}, Short: "List a repository's secret names", Method: "GET", Path: "/forge/repositories/{0}/secrets", Args: []Arg{repo},
			Cols: []Col{{Head: "NAME", Path: "name"}, {Head: "VALUE", Path: "preview"}, {Head: "UPDATED", Path: "updated_at", Fmt: "age"}, {Head: "LAST USED", Path: "last_used_at", Fmt: "age"}}},
		Op{Use: "delete <repo> <name>", Aliases: []string{"rm"}, Short: "Delete a secret", Method: "DELETE", Path: "/forge/repositories/{0}/secrets/{1}", Args: []Arg{repo, {Name: "name"}},
			Confirm: "Delete secret {1} from {0}?", Done: "Deleted {1}"},
	)
	cmd.AddCommand(newForgeSecretSetCmd())
	return cmd
}

// The value comes from stdin, never a flag or an argument: those land in shell
// history and in every other user's process list.
func newForgeSecretSetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "set <repo> <name>",
		Short: "Create or replace a secret; the value is read from stdin",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[1]
			if !forgeSecretName.MatchString(name) {
				return die(fmt.Errorf("secret name %q: use upper case letters, digits and _ (for example NPM_TOKEN)", name))
			}
			value, err := readSecretValue(cmd, name)
			if err != nil {
				return die(err)
			}
			bc, _, err := buildClient()
			if err != nil {
				return die(err)
			}
			body := map[string]any{"name": name, "value": value}
			if err := bc.API.Post(cmd.Context(), "/forge/repositories/"+url.PathEscape(args[0])+"/secrets", body, nil); err != nil {
				return die(err)
			}
			if isJSON() {
				return printerFor(cmd).JSON(map[string]any{"name": name, "repository": args[0]})
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Set %s on %s\n", name, args[0])
			return nil
		},
	}
}

func readSecretValue(cmd *cobra.Command, name string) (string, error) {
	in := cmd.InOrStdin()
	if f, ok := in.(*os.File); ok && term.IsTerminal(int(f.Fd())) {
		_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "Paste the value of %s (input hidden): ", name)
		raw, err := term.ReadPassword(int(f.Fd()))
		_, _ = fmt.Fprintln(cmd.ErrOrStderr())
		if err != nil {
			return "", fmt.Errorf("reading the value: %w", err)
		}
		return requireSecretValue(strings.TrimRight(string(raw), "\r\n"))
	}
	raw, err := io.ReadAll(io.LimitReader(in, 8193))
	if err != nil {
		return "", fmt.Errorf("reading the value from stdin: %w", err)
	}
	if len(raw) > 8192 {
		return "", errors.New("the value is longer than 8192 bytes")
	}
	// One trailing newline is what `echo` adds, not part of the value.
	return requireSecretValue(strings.TrimSuffix(strings.TrimSuffix(string(raw), "\n"), "\r"))
}

func requireSecretValue(value string) (string, error) {
	if value == "" {
		return "", errors.New("no value given")
	}
	return value, nil
}
