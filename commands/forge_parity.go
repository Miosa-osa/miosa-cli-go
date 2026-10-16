package commands

import (
	"encoding/base64"
	"fmt"
	"mime"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/Miosa-osa/miosa-cli-go/internal/api"
	"github.com/spf13/cobra"
)

// filterPullRequests applies 'pr list' --state, --author and --base to the rows
// the server returned. --state defaults to open, as in gh.
func filterPullRequests(cmd *cobra.Command, rows []any) []any {
	state, _ := cmd.Flags().GetString("state")
	author, _ := cmd.Flags().GetString("author")
	base, _ := cmd.Flags().GetString("base")
	state, author = strings.ToLower(state), strings.ToLower(author)
	out := rows[:0]
	for _, r := range rows {
		pr, _ := r.(map[string]any)
		if state != "" && state != "all" && !strings.EqualFold(fmt.Sprint(pr["state"]), state) {
			continue
		}
		if base != "" && fmt.Sprint(pr["base_branch"]) != base {
			continue
		}
		if author != "" && !authorMatches(pr, author) {
			continue
		}
		out = append(out, r)
	}
	return out
}

func authorMatches(pr map[string]any, want string) bool {
	if strings.EqualFold(fmt.Sprint(pr["author_user_id"]), want) {
		return true
	}
	a, _ := pr["author"].(map[string]any)
	for _, k := range []string{"name", "display_name", "email"} {
		if s, _ := a[k].(string); s != "" && strings.Contains(strings.ToLower(s), want) {
			return true
		}
	}
	return false
}

// getPullRequest reads one pull request from the repository's list (the API
// has no single-pull-request read besides the collaboration snapshot).
func getPullRequest(cmd *cobra.Command, c *api.Client, repo, number string) (map[string]any, error) {
	var resp struct {
		Data []map[string]any `json:"data"`
	}
	if err := c.Get(cmd.Context(), "/forge/repositories/"+url.PathEscape(repo)+"/pull-requests", nil, &resp); err != nil {
		return nil, fmt.Errorf("repository %s: %w", repo, err)
	}
	for _, pr := range resp.Data {
		if formatValue(pr["number"], "") == number {
			return pr, nil
		}
	}
	return nil, usagef("repository %s has no pull request #%s", repo, number)
}

func newForgePRDiffCmd() *cobra.Command {
	var nameOnly bool
	cmd := &cobra.Command{
		Use:     "diff <repo> <number>",
		Short:   "Show the changes a pull request makes",
		Example: "  miosa forge pr diff platform 12\n  miosa forge pr diff platform 12 --name-only",
		Args:    cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			bc, _, err := buildClient()
			if err != nil {
				return die(err)
			}
			pr, err := getPullRequest(cmd, bc.API, args[0], args[1])
			if err != nil {
				return die(err)
			}
			q := url.Values{"base": {fmt.Sprint(pr["base_sha"])}, "head": {fmt.Sprint(pr["head_sha"])}}
			var resp struct {
				Data map[string]any `json:"data"`
			}
			if err := bc.API.Get(cmd.Context(), "/forge/repositories/"+url.PathEscape(args[0])+"/compare", q, &resp); err != nil {
				return die(err)
			}
			p := printerFor(cmd)
			if isJSON() {
				return p.JSON(resp.Data)
			}
			files, _ := resp.Data["files"].([]any)
			for _, f := range files {
				file, _ := f.(map[string]any)
				if nameOnly {
					fmt.Fprintln(p.Writer(), file["path"])
					continue
				}
				if patch, _ := file["patch"].(string); patch != "" {
					fmt.Fprint(p.Writer(), patch)
					if !strings.HasSuffix(patch, "\n") {
						fmt.Fprintln(p.Writer())
					}
				} else if bin, _ := file["binary"].(bool); bin {
					fmt.Fprintf(p.Writer(), "Binary file %v %v\n", file["path"], file["status"])
				}
			}
			if cut, _ := resp.Data["truncated"].(map[string]any); cut["files"] == true || cut["patches"] == true {
				fmt.Fprintln(cmd.ErrOrStderr(), "miosa: the diff is too large to show in full; it was cut short")
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&nameOnly, "name-only", false, "Print only the names of the changed files")
	return cmd
}

func newForgePRCheckoutCmd() *cobra.Command {
	var branch string
	cmd := &cobra.Command{
		Use:   "checkout <repo> <number>",
		Short: "Check out a pull request's branch in this git repository",
		Long: `Fetches the pull request's head branch (from its fork, when it has one) and
checks it out as a local branch with the same name, or --branch. An existing
local branch is moved forward only; local commits are never thrown away.`,
		Example: "  miosa forge pr checkout platform 12",
		Args:    cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			bc, _, err := buildClient()
			if err != nil {
				return die(err)
			}
			pr, err := getPullRequest(cmd, bc.API, args[0], args[1])
			if err != nil {
				return die(err)
			}
			repoRef := args[0]
			if id, _ := pr["head_repository_id"].(string); id != "" && id != pr["repository_id"] {
				repoRef = id
			}
			var repo struct {
				Data struct {
					CloneURL string `json:"clone_url"`
				} `json:"data"`
			}
			if err := bc.API.Get(cmd.Context(), "/forge/repositories/"+url.PathEscape(repoRef), nil, &repo); err != nil {
				return die(fmt.Errorf("repository %s: %w", repoRef, err))
			}
			u, err := url.Parse(repo.Data.CloneURL)
			if err != nil || repo.Data.CloneURL == "" {
				return usagef("repository %s has no clone URL yet", repoRef)
			}
			head := fmt.Sprint(pr["head_branch"])
			if branch == "" {
				branch = head
			}
			helper, err := forgeHelperCommand()
			if err != nil {
				return die(err)
			}
			git := func(gitArgs ...string) error {
				full := append([]string{"-c", "credential.https://" + u.Host + ".helper=", "-c", "credential.https://" + u.Host + ".helper=" + helper}, gitArgs...)
				g := exec.Command("git", full...)
				g.Stdout, g.Stderr, g.Stdin = cmd.ErrOrStderr(), cmd.ErrOrStderr(), cmd.InOrStdin()
				g.Env = append(os.Environ(), "MIOSA_FORGE_GIT_HOST="+u.Host)
				if err := g.Run(); err != nil {
					return die(fmt.Errorf("git %s: %w", gitArgs[0], err))
				}
				return nil
			}
			if err := git("fetch", repo.Data.CloneURL, "refs/heads/"+head); err != nil {
				return err
			}
			if exec.Command("git", "rev-parse", "--verify", "--quiet", "refs/heads/"+branch).Run() == nil {
				if err := git("checkout", branch); err != nil {
					return err
				}
				if err := git("merge", "--ff-only", "FETCH_HEAD"); err != nil {
					return err
				}
			} else if err := git("checkout", "-b", branch, "FETCH_HEAD"); err != nil {
				return err
			}
			printerFor(cmd).Success("Checked out pull request #%s as branch %s", args[1], branch)
			return nil
		},
	}
	cmd.Flags().StringVar(&branch, "branch", "", "Local branch name (default: the pull request's head branch)")
	return cmd
}

func newForgeReleaseUploadCmd() *cobra.Command {
	var name, contentType, key string
	var sourceArchive bool
	cmd := &cobra.Command{
		Use:   "upload <repo> <tag> [file...]",
		Short: "Attach files to a release",
		Long: `Uploads each file as a release asset (up to 25 MB each). --source-archive
attaches a .tar.gz of the tagged commit instead. Repeating an upload with the
same --idempotency-key returns the first result.`,
		Example: "  miosa forge release upload platform v1.2.0 dist/app.tar.gz dist/checksums.txt\n  miosa forge release upload platform v1.2.0 --source-archive",
		Args:    cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			files := args[2:]
			if len(files) == 0 && !sourceArchive {
				return usagef("name at least one file, or pass --source-archive")
			}
			if name != "" && len(files) > 1 {
				return usagef("--name applies to one file; upload them one at a time")
			}
			bc, _, err := buildClient()
			if err != nil {
				return die(err)
			}
			path := "/forge/repositories/" + url.PathEscape(args[0]) + "/releases/" + url.PathEscape(args[1]) + "/assets"
			var rows [][]string
			var results []any
			send := func(body map[string]any, label string, n int) error {
				idem := key
				if idem == "" {
					idem = generatedKey()
				} else if n > 0 {
					idem = fmt.Sprintf("%s-%d", key, n)
				}
				resp, err := bc.API.JSONAny(cmd.Context(), api.Request{Method: "POST", Path: path, Body: body, IdempotencyKey: idem})
				if err != nil {
					return die(fmt.Errorf("%s: %w", label, err))
				}
				asset, _ := unwrapObject(resp).(map[string]any)
				results = append(results, asset)
				rows = append(rows, []string{formatValue(asset["name"], ""), formatValue(asset["byte_size"], "bytes"), formatValue(asset["sha256"], "sha")})
				return nil
			}
			if sourceArchive {
				if err := send(map[string]any{"kind": "source_archive"}, "source archive", 0); err != nil {
					return err
				}
			}
			for i, f := range files {
				raw, err := os.ReadFile(f)
				if err != nil {
					return die(err)
				}
				assetName := filepath.Base(f)
				if name != "" {
					assetName = name
				}
				ct := contentType
				if ct == "" {
					ct = mime.TypeByExtension(filepath.Ext(f))
				}
				body := map[string]any{"kind": "upload", "name": assetName, "content_base64": base64.StdEncoding.EncodeToString(raw)}
				if ct != "" {
					body["content_type"] = strings.SplitN(ct, ";", 2)[0]
				}
				if err := send(body, f, i+1); err != nil {
					return err
				}
			}
			p := printerFor(cmd)
			if isJSON() {
				return p.JSON(map[string]any{"data": results})
			}
			p.Table([]string{"ASSET", "SIZE", "SHA256"}, rows)
			return nil
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "Asset name (default: the file name)")
	cmd.Flags().StringVar(&contentType, "content-type", "", "Content type (default: from the file extension)")
	cmd.Flags().BoolVar(&sourceArchive, "source-archive", false, "Attach a .tar.gz of the tagged commit")
	cmd.Flags().StringVar(&key, "idempotency-key", "", "Replay-safe key: repeating it returns the first result (default: generated)")
	return cmd
}
