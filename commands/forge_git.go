package commands

import (
	"bufio"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"strings"

	"github.com/spf13/cobra"
)

const defaultForgeGitHost = "forge.miosa.ai"

// newForgeAuthCmd is `miosa forge auth`: git credentials for Forge over HTTPS,
// the same shape as `gh auth setup-git`. Git asks the helper for a password and
// gets this CLI's MIOSA API key, or, when the CLI signed in with a session
// token, a Forge-only key minted with it (forge_git_key.go).
func newForgeAuthCmd() *cobra.Command {
	auth := &cobra.Command{Use: "auth", Short: "Use your MIOSA API key for Forge git (clone, push and pull over HTTPS)"}
	var host string
	setup := &cobra.Command{
		Use:   "setup-git",
		Short: "Make git use your MIOSA login for Forge repositories",
		Long: `Configures git (globally, for the Forge host only) to ask this CLI for
your API key when it talks to Forge. After it, plain git works against Forge:

  git clone https://forge.miosa.ai/<org>/<repo>.git
  git push

The password git receives is the API key this CLI is signed in with
('miosa login'). A CLI signed in with a browser session token instead gets a
Forge-only key (forge:read, forge:write) minted once with that session and
kept in this profile; 'miosa logout' forgets it. Nothing is stored by git.`,
		Example: "  miosa forge auth setup-git\n  miosa forge repo clone my-repo",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, _, err := buildClient()
			if err != nil {
				return die(err)
			}
			_, minted, err := forgeGitKey(cmd.Context(), c)
			if err != nil {
				return die(err)
			}
			helper, onPath := persistentHelperCommand()
			key := "credential.https://" + host + ".helper"
			// An empty entry first resets helpers inherited from other config
			// (osxkeychain and friends) for this host only.
			for _, args := range [][]string{
				{"config", "--global", "--replace-all", key, ""},
				{"config", "--global", "--add", key, helper},
			} {
				if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
					return die(fmt.Errorf("git %s: %v %s", strings.Join(args, " "), err, strings.TrimSpace(string(out))))
				}
			}
			p := printerFor(cmd)
			p.Success("git now uses your MIOSA login for https://%s", host)
			if minted {
				p.Line("This CLI is signed in with a session token, so it minted a Forge key for git (forge:read, forge:write) and kept it in this profile.")
			}
			if !onPath {
				p.Line("'miosa' is not on your PATH, so git was given this binary's full path: %s", helper)
				p.Line("Put miosa on your PATH and run 'miosa forge auth setup-git' again, or git auth breaks when this file moves.")
			}
			return nil
		},
	}
	setup.Flags().StringVar(&host, "host", defaultForgeGitHost, "Forge git host")

	credential := &cobra.Command{
		Use:    "git-credential <get|store|erase>",
		Short:  "Git credential helper (called by git, not by people)",
		Hidden: true,
		Args:   cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if args[0] != "get" && args[0] != "erase" {
				return nil // store: nothing is kept
			}
			req := readCredentialRequest(cmd.InOrStdin())
			if req["protocol"] != "https" || !isForgeHost(req["host"]) {
				return nil
			}
			c, _, err := buildClient()
			if err != nil || c.Key == "" {
				return nil // git falls through to its next helper / prompt
			}
			if args[0] == "erase" {
				forgetForgeGitKey(cmd.Context(), c, req["password"])
				return nil
			}
			key, _, err := forgeGitKey(cmd.Context(), c)
			if err != nil {
				// git shows the helper's stderr; it then tries its next helper.
				fmt.Fprintf(cmd.ErrOrStderr(), "miosa: %s\n", friendlyMessage(err))
				return nil
			}
			fmt.Fprintf(cmd.OutOrStdout(), "username=miosa\npassword=%s\n", key)
			return nil
		},
	}
	auth.AddCommand(setup, credential)
	return auth
}

func readCredentialRequest(r io.Reader) map[string]string {
	out := map[string]string{}
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		line := sc.Text()
		if line == "" {
			break
		}
		if k, v, ok := strings.Cut(line, "="); ok {
			out[k] = v
		}
	}
	return out
}

func isForgeHost(host string) bool {
	host = strings.ToLower(host)
	if extra := os.Getenv("MIOSA_FORGE_GIT_HOST"); extra != "" && strings.EqualFold(host, extra) {
		return true
	}
	return host == defaultForgeGitHost
}

// persistentHelperCommand is the helper setup-git writes into the global git
// config. It names the CLI by PATH, the way gh does, so switching Node versions
// or reinstalling the CLI elsewhere keeps git working. Only when no miosa is on
// PATH does it fall back to this binary's full path (and onPath is false).
func persistentHelperCommand() (helper string, onPath bool) {
	if _, err := lookPath("miosa"); err == nil {
		return "!miosa forge auth git-credential", true
	}
	abs, err := forgeHelperCommand()
	if err != nil {
		return "!miosa forge auth git-credential", false
	}
	return abs, false
}

// lookPath is exec.LookPath, replaceable in tests.
var lookPath = exec.LookPath

// forgeHelperCommand names this exact binary, for a one-shot git command such
// as 'forge repo clone', where the path cannot go stale.
func forgeHelperCommand() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	return "!" + shellQuote(exe) + " forge auth git-credential", nil
}

func shellQuote(s string) string {
	if !strings.ContainsAny(s, " '\"\\$`") {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// newForgeRepoCloneCmd is `miosa forge repo clone <repo> [dir]`: git clone with
// this CLI as the credential helper for that one command, so it works before
// (and without) setup-git.
func newForgeRepoCloneCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "clone <repo> [directory]",
		Short:   "Clone a Forge repository with your MIOSA login",
		Example: "  miosa forge repo clone my-repo\n  miosa forge repo clone acme/my-repo ./src",
		Args:    cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, _, err := buildClient()
			if err != nil {
				return die(err)
			}
			var resp struct {
				Data struct {
					CloneURL *string `json:"clone_url"`
					Name     string  `json:"name"`
				} `json:"data"`
			}
			if err := c.API.Get(cmd.Context(), "/forge/repositories/"+url.PathEscape(args[0]), nil, &resp); err != nil {
				return die(fmt.Errorf("repository %s: %w", args[0], err))
			}
			if resp.Data.CloneURL == nil || *resp.Data.CloneURL == "" {
				return usagef("repository %s has no clone URL yet (it is still being created)", args[0])
			}
			u, err := url.Parse(*resp.Data.CloneURL)
			if err != nil {
				return die(err)
			}
			helper, err := forgeHelperCommand()
			if err != nil {
				return die(err)
			}
			gitArgs := []string{
				"-c", "credential.https://" + u.Host + ".helper=",
				"-c", "credential.https://" + u.Host + ".helper=" + helper,
				"clone", *resp.Data.CloneURL,
			}
			if len(args) == 2 {
				gitArgs = append(gitArgs, args[1])
			}
			git := exec.Command("git", gitArgs...)
			git.Stdout, git.Stderr, git.Stdin = cmd.OutOrStdout(), cmd.ErrOrStderr(), cmd.InOrStdin()
			git.Env = append(os.Environ(), "MIOSA_FORGE_GIT_HOST="+u.Host)
			if err := git.Run(); err != nil {
				return die(fmt.Errorf("git clone: %w", err))
			}
			return nil
		},
	}
}
