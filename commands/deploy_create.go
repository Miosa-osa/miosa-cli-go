package commands

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/Miosa-osa/miosa-cli-go/internal/api"
	"github.com/Miosa-osa/miosa-cli-go/internal/client"
	"github.com/Miosa-osa/miosa-cli-go/internal/output"
)

// Creating a deployment is a multi-step operation that no single route
// expresses: publishing a sandbox, connecting a git repo and building it, or
// uploading a directory are three different API flows with three different
// result shapes. This command owns that logic outright instead of using the
// declarative Op layer.

// dropChunkBytes is the largest slice the Drop chunk route accepts (8 MiB).
const dropChunkBytes = 8 * 1024 * 1024

// dropSessionMaxBytes is the total a Drop session accepts (100 MiB).
const dropSessionMaxBytes = 100 * 1024 * 1024

// webhookDocsURL is where a repo deployment's webhook is configured to appear.
const webhookPath = "/api/v1/integrations/github/webhook"

// deployFlags are the flags shared by 'deploy' and 'deploy create'.
type deployFlags struct {
	name         string
	deployment   string // push to this existing deployment instead of creating
	fromSandbox  string
	git          string
	dir          string
	image        string
	branch       string
	port         int
	entrypoint   string
	deployType   string
	runCommand   string
	buildCommand string
	env          []string
	domain       string
	wait         bool // kept so existing scripts that pass --wait still parse; waiting is the default
	noWait       bool
	waitTimeout  time.Duration
	yes          bool
	noPromote    bool
	forge        string // Forge repository (id, org/name or name)
	project      string // project to link the Forge repository through
	noAuto       bool   // Forge: do not build on every push to the branch
	rootDir      string
	force        bool   // push even when nothing changed since the latest version
	aiAccess     string // allow | block: whether agents may read the published site
	workspace    string // --workspace: where a new deployment lands (not used with --from-sandbox)
	workspaceID  string // the resolved id, sent as workspace_id
}

// bindDeploySourceFlags binds the flags that shape the deployment. The bare
// 'deploy' shortcut only needs the git/upload ones, so createOnly adds the
// source selectors and the sandbox-specific ones.
func bindDeploySourceFlags(cmd *cobra.Command, f *deployFlags, createOnly bool) {
	fs := cmd.Flags()
	fs.StringVar(&f.name, "name", "", "Deployment name (default: the argument, the repo or the directory name)")
	fs.StringVar(&f.branch, "branch", "", "Git branch to build (default: the current one)")
	fs.StringArrayVar(&f.env, "env", nil, "Environment variable KEY=VALUE (repeatable)")
	fs.StringVar(&f.domain, "domain", "", "Custom domain to attach (then add the DNS record it shows)")
	fs.StringVar(&f.deployment, "deployment", "", "Push a new version to this existing deployment (id, name or hostname)")
	fs.BoolVar(&f.wait, "wait", false, "Wait until the deployment is live (the default; kept for scripts)")
	fs.BoolVar(&f.noWait, "no-wait", false, "Return as soon as the deployment is created instead of waiting until it is live")
	fs.DurationVar(&f.waitTimeout, "wait-timeout", 10*time.Minute, "How long to wait for the deployment to go live")
	fs.BoolVar(&f.noPromote, "no-promote", false, "Upload the version without making it live; prints the version to promote later")
	fs.BoolVar(&f.force, "force", false, "Upload even when the files match the latest version")
	fs.StringVar(&f.aiAccess, "ai-access", "", "Whether agents may read the published site: allow (the default) or block")
	if createOnly {
		fs.StringVar(&f.fromSandbox, "from-sandbox", "", "Publish this sandbox (name, id or 'current')")
		fs.StringVar(&f.git, "git", "", "Deploy this git repository URL")
		fs.StringVar(&f.dir, "dir", "", "With --from-sandbox: the app's path inside the sandbox. On its own: a local directory to upload")
		fs.StringVar(&f.image, "image", "", "App Engine container image (not exposed by the public API; see --type docker_deploy)")
		fs.IntVar(&f.port, "port", 0, "The port the app serves on (--from-sandbox, or --dir with --run-command)")
		fs.StringVar(&f.entrypoint, "entrypoint", "", "With --from-sandbox: the entry file for a static publish")
		fs.StringVar(&f.deployType, "type", "", "Runtime to publish as: static, dynamic or docker_deploy (default: static, or dynamic with --run-command)")
		fs.StringVar(&f.runCommand, "run-command", "", "Command the app starts with (makes the publish dynamic)")
		fs.StringVar(&f.buildCommand, "build-command", "", "Build command (git or Forge source, or a --from-sandbox server publish)")
		fs.StringVar(&f.forge, "forge", "", "Deploy this Forge repository (id, org/name or name); builds --branch and redeploys on every push")
		fs.StringVar(&f.project, "project", "", "Project to put the deployment in (name, slug or id); with --forge, the project to link the repository through (one per project)")
		fs.BoolVar(&f.noAuto, "no-auto", false, "With --forge: do not build automatically on pushes to the branch")
		fs.StringVar(&f.rootDir, "root-directory", "", "With --forge: the app's directory inside the repository")
		fs.StringVar(&f.workspace, "workspace", "", workspaceFlagUsage)
	}
}

func newDeployCreateCmd() *cobra.Command {
	f := &deployFlags{}
	cmd := &cobra.Command{
		Use:     "create [name]",
		Aliases: []string{"publish"},
		Short:   "Create and publish a deployment from a sandbox, a git repo or a directory",
		Long: `Create a new deployment and make it live.

Pick exactly one source:

  --from-sandbox <sandbox>   Publish a running sandbox's output.
  --git <url>                Connect a git repository and build it.
  --dir <path>               Upload a local directory: built static output, or a
                             server app with --run-command (and --port).

With --from-sandbox, --dir is where the app lives inside the sandbox (default
/workspace). On its own, --dir is a local directory to package and upload. A
sandbox publish is static by default; give --run-command (or --type dynamic)
to publish a running server, or --type docker_deploy for App Engine.

  miosa deploy create --from-sandbox my-box --wait
  miosa deploy create --from-sandbox my-box --port 3000 --run-command "npm start"
  miosa deploy create --git https://github.com/me/app --branch main --wait
  miosa deploy create --dir ./dist --name my-site

--env sets environment variables (repeatable, encrypted at rest). --domain
attaches a custom domain; 'miosa deploy domains <deployment>' shows the DNS
record to add. --wait blocks until the deployment is live and prints its URL.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDeployCreate(cmd, args, f)
		},
	}
	bindDeploySourceFlags(cmd, f, true)
	// Scripts pass --yes to every changing command; create asks nothing it
	// needs to skip today, but it must accept the flag instead of refusing.
	cmd.Flags().BoolVarP(&f.yes, "yes", "y", false, "Do not ask for confirmation")
	return cmd
}

func newDeployPushCmd() *cobra.Command {
	f := &deployFlags{}
	cmd := &cobra.Command{
		Use:   "push <deployment>",
		Short: "Upload a new version to an existing deployment",
		Long: `Push a new version of a local directory to an existing deployment.

  miosa deploy push my-site --dir ./dist
  miosa deploy push lunivate.com            # a hostname works too

The deployment keeps its id, URL and domains; only a new version is released.
This is what 'miosa deploy' does automatically once a folder is linked: every
deploy and push writes or refreshes .miosa/deployment.json in the folder (and in
the project folder when --dir points inside it). A push whose files match the
latest version uploads nothing; --force uploads anyway.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ff := *f
			ff.deployment = args[0]
			ff.name = ""
			if ff.dir == "" {
				ff.dir = "."
			}
			return runDeployCreate(cmd, nil, &ff)
		},
	}
	fs := cmd.Flags()
	fs.StringVar(&f.dir, "dir", "", "Local directory to upload (default: the current directory)")
	fs.StringArrayVar(&f.env, "env", nil, "Environment variable KEY=VALUE (repeatable)")
	fs.BoolVar(&f.wait, "wait", false, "Wait until the new version is live (the default; kept for scripts)")
	fs.BoolVar(&f.noWait, "no-wait", false, "Return as soon as the version is uploaded instead of waiting until it is live")
	fs.DurationVar(&f.waitTimeout, "wait-timeout", 10*time.Minute, "How long to wait for the version to go live")
	fs.BoolVar(&f.noPromote, "no-promote", false, "Upload the version without making it live; prints the version to promote later")
	fs.BoolVar(&f.force, "force", false, "Upload even when the files match the latest version")
	return cmd
}

// runDeployCreate chooses the source, runs the matching API flow and renders
// the result. It works on a copy of the flags: the flag struct is bound to the
// command for the life of the process, so mutating it would leak between runs.
func runDeployCreate(cmd *cobra.Command, args []string, flags *deployFlags) error {
	f := *flags
	if len(args) > 0 && f.name == "" {
		f.name = args[0]
	}
	if f.image != "" {
		return usagef("--image is not available: MIOSA exposes no public image-deploy route. " +
			"Publish from a sandbox instead: miosa deploy create --from-sandbox <sandbox> --type docker_deploy")
	}

	// --dir is the sandbox's output path when --from-sandbox is set, and a
	// local upload source otherwise, so only count it as a source on its own.
	localDir := f.dir != "" && f.fromSandbox == ""
	sources := 0
	for _, set := range []bool{f.fromSandbox != "", f.git != "", localDir, f.forge != ""} {
		if set {
			sources++
		}
	}
	switch {
	case sources == 0:
		return usagef("give a source: --from-sandbox <sandbox>, --git <url>, --forge <repo> or --dir <path> (see 'miosa deploy create --help')")
	case sources > 1:
		return usagef("give exactly one source of --from-sandbox, --git, --forge or --dir")
	}
	// A local folder publishes as a server only with the command it starts
	// with; --type dynamic alone used to reach the server and fail as
	// PUBLISH_VALIDATION_FAILED (customer write-up #4).
	if localDir && f.deployType == "dynamic" && f.runCommand == "" {
		return usagef("--type dynamic with --dir needs --run-command, the command the app starts with")
	}
	// A folder with no index.html but a server manifest is an app, not a site: published as the
	// default static site it failed on the server as a bare PUBLISH_VALIDATION_FAILED (QA 10-23).
	if localDir && f.deployType == "" && f.runCommand == "" {
		if manifest := serverManifest(f.dir); manifest != "" {
			return usagef("%s has %s and no index.html, so it is an app to run, not a site: add --run-command (the command it starts with, e.g. \"npm start\") and --port, or --type static to publish the files as they are", f.dir, manifest)
		}
	}
	if localDir && f.deployType == "docker_deploy" {
		return usagef("--type docker_deploy publishes from a sandbox: use --from-sandbox <sandbox>")
	}
	if f.port != 0 && f.fromSandbox == "" && (f.dir == "" || f.runCommand == "") {
		return usagef("--port only applies to --from-sandbox, or to --dir with --run-command")
	}
	// A sandbox publish only builds when it runs a server; a static publish
	// ships the folder as it is.
	if f.buildCommand != "" && f.fromSandbox != "" && f.runCommand == "" && f.deployType != "dynamic" {
		return usagef("--build-command with --from-sandbox needs --run-command or --type dynamic (a static publish ships the folder as it is)")
	}
	if f.noPromote && (f.fromSandbox != "" || f.git != "") {
		return usagef("--no-promote only applies to a local upload (--dir, or no source in a folder)")
	}

	env, err := deployEnvMap(f.env)
	if err != nil {
		return usagef("%v", err)
	}
	if err := checkAIAccess(f.aiAccess); err != nil {
		return err
	}

	var httpTimeout time.Duration
	if f.shouldWait() {
		httpTimeout = f.waitTimeout + 30*time.Second
	}
	c, _, err := buildClientTimeout(httpTimeout)
	if err != nil {
		return die(err)
	}
	if f.fromSandbox == "" {
		// A publish from a sandbox lands where the sandbox is; every other
		// source creates a deployment and honours --workspace and the default.
		if f.workspaceID, err = createWorkspaceID(cmd, c.API, f.workspace); err != nil {
			return die(err)
		}
	} else if f.workspace != "" {
		return usagef("--workspace does not apply with --from-sandbox: the deployment takes the sandbox's workspace")
	}
	p := printerFor(cmd)

	var out *deployOutcome
	switch {
	case f.fromSandbox != "":
		out, err = deployFromSandbox(cmd.Context(), c, p, &f, env)
	case f.forge != "":
		out, err = deployFromForge(cmd.Context(), c, p, &f, env)
	case f.git != "":
		out, err = deployFromGit(cmd.Context(), c, p, &f, env)
	default:
		out, err = deployFromDir(cmd.Context(), c, p, &f, env)
	}
	if nc, ok := err.(errNoChanges); ok {
		if isJSON() {
			return p.JSON(map[string]any{"status": "unchanged", "version_number": nc.version, "version_id": nc.id})
		}
		p.Line("No changes since version %d (%s) - nothing uploaded. Pass --force to push anyway.", nc.version, shortRef(nc.id))
		return nil
	}
	if err != nil {
		return die(err)
	}

	// Report where it actually landed: the create answer carries it on most
	// routes, and one GET settles the rest, including a fallback to the
	// organization's default workspace. A sandbox publish skips it: the
	// deployment takes the sandbox's workspace.
	if out.WorkspaceID == "" && out.Source != "sandbox" {
		out.WorkspaceID = deploymentWorkspace(cmd, c, out.ID)
	}

	if f.domain != "" && out.Source != "sandbox" {
		if err := addDeploymentDomain(cmd.Context(), c, out.ID, f.domain); err != nil {
			return die(err)
		}
		out.Domain = f.domain
	}

	// Set after the deployment exists, whatever the source: an upload creates it through a drop session
	// that has no such field. Before the wait, so the site is closed to agents as soon as it is live.
	if f.aiAccess != "" {
		if err := setAIAccess(cmd.Context(), c, out.ID, f.aiAccess); err != nil {
			return die(err)
		}
		out.AIAccess = f.aiAccess
	}

	if f.shouldWait() && out.Promoted != nil && !*out.Promoted {
		// An uploaded version that was not promoted never goes live: there is nothing to wait for.
	} else if f.shouldWait() {
		if !isJSON() {
			p.Line("Waiting for %s to go live (%s)...", orDefault(out.Name, "deployment"), out.ID)
		}
		state, url, err := pollDeployment(cmd.Context(), c, out.ID, f.waitTimeout, phasePrinter(p))
		out.State = state
		if url != "" {
			out.URL = url
		}
		if err != nil {
			return die(err)
		}
	}

	return renderDeploy(p, out)
}

// shouldWait is whether the command blocks until the deployment is live. It does by default: publishing takes
// time (a build, a runtime, the route, the HTTPS certificate), and returning earlier would hand back a URL that
// does not work yet. --no-wait skips it, and a version uploaded with --no-promote never goes live.
func (f *deployFlags) shouldWait() bool { return !f.noWait && !f.noPromote }

// deployOutcome is the shape the command prints, whatever the source.
type deployOutcome struct {
	ID            string
	Name          string
	Slug          string
	State         string
	URL           string
	Source        string
	Domain        string
	WebhookSecret string
	// VersionID and Promoted are set by the upload path only: a version pushed
	// with --no-promote is uploaded but not serving.
	VersionID string
	Promoted  *bool
	// AIAccess is set when --ai-access was given: allow or block.
	AIAccess string
	// Existing is true when this run pushed to a deployment that was already
	// there (a linked folder, or --deployment) instead of creating one.
	Existing bool
	// WorkspaceID is where the deployment actually landed, read back from the
	// API so a fallback to the default workspace is visible.
	WorkspaceID string
}

// deployResponse is the deployment object the API returns inside "data".
type deployResponse struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Slug        string `json:"slug"`
	State       string `json:"state"`
	PublicURL   string `json:"public_url"`
	WorkspaceID string `json:"workspace_id"`
	// Publish is how far the current publish has got; nil from a server that predates the phases.
	Publish *publishStatus `json:"publish"`
	// Failure is the coded reason a deployment failed or is failing
	// ({code, stage, message}); a timed-out --wait should show it.
	Failure *deployFailure `json:"failure"`
}

// deployFailure is the deployment document's failure object.
type deployFailure struct {
	Code    string `json:"code"`
	Stage   string `json:"stage"`
	Message string `json:"message"`
}

func (f *deployFailure) String() string {
	if f == nil {
		return ""
	}
	stage := orDefault(f.Stage, "deploy")
	if f.Message == "" {
		return fmt.Sprintf("%s at %s", orDefault(f.Code, "failure"), stage)
	}
	return fmt.Sprintf("%s at %s: %s", orDefault(f.Code, "failure"), stage, f.Message)
}

// ─── source: sandbox ──────────────────────────────────────────────────────────

type sandboxDeployResponse struct {
	DeploymentID string `json:"deployment_id"`
	URL          string `json:"url"`
	State        string `json:"state"`
	// EnvApplied is true when the server stored the env sent with the publish
	// (it froze into the first release). An older server leaves it false.
	EnvApplied bool `json:"env_applied"`
	Data       struct {
		Deployment deployResponse `json:"deployment"`
		URL        string         `json:"url"`
	} `json:"data"`
}

func deployFromSandbox(ctx context.Context, c *client.Client, p *output.Printer, f *deployFlags, env map[string]any) (*deployOutcome, error) {
	id, err := c.API.ResolveSandbox(ctx, f.fromSandbox)
	if err != nil {
		return nil, err
	}
	name := firstNonEmpty(f.name, f.fromSandbox)
	body := map[string]any{"name": name}
	// --deployment publishes a new version of that deployment. The sandbox route
	// takes an id only, and a publish that quietly fell back to a create left the
	// customer with a deployment they never asked for (FounderOS, 2026-10-20), so
	// the reference is resolved first and a miss is an error, never a create.
	if f.deployment != "" {
		depID, err := resolveDeploymentRef(ctx, c, f.deployment)
		if err != nil {
			return nil, err
		}
		body["deployment_id"] = depID
	}
	if f.dir != "" {
		body["output_path"] = f.dir
	}
	if f.entrypoint != "" {
		body["entrypoint"] = f.entrypoint
	}
	if f.port != 0 {
		body["port"] = f.port
	}
	if f.deployType != "" {
		body["deployment_type"] = f.deployType
	}
	if f.runCommand != "" {
		body["run_command"] = f.runCommand
	}
	if f.buildCommand != "" {
		body["build_command"] = f.buildCommand
	}
	if f.domain != "" {
		body["domain"] = f.domain
	}
	// The env goes with the publish so the first release carries it; setting it
	// afterwards cut a second release (QA 2026-10-16).
	if len(env) > 0 {
		body["env"] = env
	}
	if !isJSON() {
		p.Line("Publishing %s...", f.fromSandbox)
	}
	var resp sandboxDeployResponse
	if err := c.API.Post(ctx, "/sandboxes/"+id+"/deploy", body, &resp); err != nil {
		return nil, err
	}
	d := resp.Data.Deployment
	out := &deployOutcome{
		ID:          firstNonEmpty(d.ID, resp.DeploymentID),
		Name:        firstNonEmpty(d.Name, name),
		Slug:        d.Slug,
		State:       firstNonEmpty(d.State, resp.State),
		URL:         firstNonEmpty(d.PublicURL, resp.URL),
		Source:      "sandbox",
		Domain:      f.domain,
		WorkspaceID: d.WorkspaceID,
	}
	if out.ID == "" {
		return nil, fmt.Errorf("the server accepted the publish but returned no deployment id")
	}
	if len(env) > 0 && !resp.EnvApplied {
		// A server that predates env on the publish ignored it: set it as before.
		if err := setDeploymentEnv(ctx, c, out.ID, env); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// resolveDeploymentRef turns the --deployment argument (an id, name, slug or
// hostname) into the id the sandbox publish route takes. The route resolves ids
// only; every other form goes through the read route that already accepts names,
// so the behaviour matches 'deploy get'.
func resolveDeploymentRef(ctx context.Context, c *client.Client, ref string) (string, error) {
	var resp struct {
		Data deployResponse `json:"data"`
	}
	if err := c.API.Get(ctx, "/deployments/"+url.PathEscape(ref), nil, &resp); err != nil {
		return "", fmt.Errorf("--deployment %s: %w", ref, err)
	}
	if resp.Data.ID == "" {
		return "", fmt.Errorf("--deployment %s: the server returned no deployment id", ref)
	}
	return resp.Data.ID, nil
}

// ─── source: git ──────────────────────────────────────────────────────────────

type createDeployResponse struct {
	Data          deployResponse `json:"data"`
	WebhookSecret string         `json:"webhook_secret"`
}

func deployFromGit(ctx context.Context, c *client.Client, p *output.Printer, f *deployFlags, env map[string]any) (*deployOutcome, error) {
	if f.name == "" {
		name, err := repoNameFromURL(f.git)
		if err != nil {
			return nil, usageErr{err}
		}
		f.name = name
	}
	body := map[string]any{
		"name":        f.name,
		"repo_url":    f.git,
		"auto_deploy": true,
	}
	if f.branch != "" {
		body["branch"] = f.branch
	}
	if f.buildCommand != "" {
		body["build_command"] = f.buildCommand
	}
	if f.runCommand != "" {
		body["run_command"] = f.runCommand
	}
	if len(env) > 0 {
		body["env"] = env
	}
	if f.workspaceID != "" {
		body["workspace_id"] = f.workspaceID
	}
	// --project puts the deployment in a project, where it shows on the canvas like + Add > GitHub repository.
	if f.project != "" {
		projectID, err := resolveServiceProject(ctx, c.API, f.project)
		if err != nil {
			return nil, err
		}
		body["project_id"] = projectID
	}
	if id, name, found, err := existingDeploymentByName(ctx, c, f.name); err != nil {
		return nil, err
	} else if found {
		return nil, usagef("deployment %q (%s) already exists - rebuild it with 'miosa deploy redeploy %s', or pass a different name for a new deployment", name, shortRef(id), name)
	}
	if !isJSON() {
		p.Line("Creating deployment %q from %s...", f.name, f.git)
	}
	var resp createDeployResponse
	if err := c.API.Post(ctx, "/deployments", body, &resp); err != nil {
		return nil, err
	}
	out := &deployOutcome{
		ID:            resp.Data.ID,
		Name:          firstNonEmpty(resp.Data.Name, f.name),
		Slug:          resp.Data.Slug,
		State:         resp.Data.State,
		URL:           resp.Data.PublicURL,
		Source:        "git",
		WebhookSecret: resp.WebhookSecret,
		WorkspaceID:   firstNonEmpty(resp.Data.WorkspaceID, f.workspaceID),
	}
	if out.ID == "" {
		return nil, fmt.Errorf("the server created the deployment but returned no id")
	}
	// The server builds a connected repository on create and answers
	// "building". Only a server that left it "pending" (or did not say) needs
	// the first build queued here; redeploying anyway built the same head twice.
	if out.State == "" || out.State == "pending" {
		if err := c.API.Post(ctx, "/deployments/"+out.ID+"/redeploy", nil, nil); err != nil {
			return nil, err
		}
		out.State = "building"
	}
	return out, nil
}

// ─── source: local directory (Drop upload) ────────────────────────────────────

type dropSessionResponse struct {
	Data struct {
		SessionID    string `json:"session_id"`
		DeploymentID string `json:"deployment_id"`
	} `json:"data"`
}

type dropCompleteResponse struct {
	Data struct {
		VersionID    string   `json:"version_id"`
		DeploymentID string   `json:"deployment_id"`
		PublicURL    string   `json:"public_url"`
		Promoted     bool     `json:"promoted"`
		Kind         string   `json:"kind"`
		State        string   `json:"state"`
		SkippedPaths []string `json:"skipped_paths"`
	} `json:"data"`
}

// staleLink handles a folder linked to a deployment that is gone: with a name
// to create, it confirms (or takes --yes) and creates a new deployment, whose
// id then replaces the link; without one it explains instead of a bare 404.
func staleLink(p *output.Printer, f *deployFlags, link deploymentLink) error {
	what := fmt.Sprintf("This folder is linked to deployment %s, which no longer exists or isn't in this workspace.", orDefault(link.Name, shortRef(link.DeploymentID)))
	if f.name == "" {
		return &api.Error{Status: http.StatusNotFound, Code: "NOT_FOUND",
			Message: what + " Pass a name to create a new one: 'miosa deploy create <name>', or delete " + filepath.Join(deploymentLinkDir, deploymentLinkName) + "."}
	}
	if !f.yes {
		if err := confirmOnTerminal(what + fmt.Sprintf(" Create a new deployment %q and link this folder to it?", f.name)); err != nil {
			return err
		}
	} else if !isJSON() {
		p.Line("%s Creating %q and linking this folder to it.", what, f.name)
	}
	return nil
}

func deployFromDir(ctx context.Context, c *client.Client, p *output.Printer, f *deployFlags, env map[string]any) (*deployOutcome, error) {
	root, err := filepath.Abs(f.dir)
	if err != nil {
		return nil, usagef("--dir %s: %v", f.dir, err)
	}
	info, err := os.Stat(root)
	if err != nil {
		return nil, usagef("--dir %s: %v", f.dir, err)
	}
	if !info.IsDir() {
		return nil, usagef("--dir %s is not a directory", f.dir)
	}
	if f.name == "" {
		f.name = filepath.Base(root)
	}
	archive, skipped, err := tarGzDir(root)
	if err != nil {
		return nil, err
	}
	warnSkipped(os.Stderr, skipped)
	if len(archive) > dropSessionMaxBytes {
		return nil, usagef("--dir %s is %s packaged, over the 100 MiB upload limit", f.dir, humanBytes(float64(len(archive))))
	}

	// 1. Open an upload session. When this folder is already linked to a
	// deployment (or --deployment names one), push a new VERSION to that
	// deployment; otherwise create one and remember it.
	target := f.deployment
	linkedName := ""
	var fromLink *deploymentLink
	if target == "" {
		if link, ok := readDeploymentLink(root); ok {
			target = link.DeploymentID
			linkedName = link.Name
			fromLink = &link
		}
	}
	createNew := target == ""
	// An unlinked folder whose name matches an existing deployment is that
	// deployment: push a version to it, never create a second one beside it.
	if createNew && f.name != "" {
		if id, name, found, err := existingDeploymentByName(ctx, c, f.name); err != nil {
			return nil, err
		} else if found {
			target, linkedName, createNew = id, name, false
			if !isJSON() {
				p.Line("Deployment %q (%s) already exists: pushing a new version to it and linking this folder.", name, shortRef(id))
			}
		}
	}

	// Pushing to a deployment named by id, hostname or a stale link: print and
	// link it under its real name, not the reference that found it.
	if !createNew && linkedName == "" {
		var dep struct {
			Data map[string]any `json:"data"`
		}
		if err := c.API.Get(ctx, "/deployments/"+url.PathEscape(target), nil, &dep); err == nil {
			linkedName, _ = dep.Data["name"].(string)
		}
	}

	// An unchanged folder is not uploaded again: same files (path + sha256) as
	// the deployment's latest version means a new version would be identical.
	if !createNew && !f.force {
		if n, vid, same := unchangedSinceLatest(ctx, c, target, root); same {
			return nil, errNoChanges{version: n, id: vid}
		}
	}

	var sid, depID string
	if createNew {
		var session dropSessionResponse
		if err := c.API.Post(ctx, "/deployments/drop/sessions", dropSessionBody(f), &session); err != nil {
			return nil, err
		}
		sid, depID = session.Data.SessionID, session.Data.DeploymentID
	} else {
		depID = target
		var session dropSessionResponse
		err := c.API.Post(ctx, "/deployments/"+url.PathEscape(target)+"/drop/sessions", nil, &session)
		// The folder's link names a deployment that is gone: create the named
		// one instead (after confirmation), and the link is rewritten below.
		if fromLink != nil && api.IsStatus(err, http.StatusNotFound) {
			if err := staleLink(p, f, *fromLink); err != nil {
				return nil, err
			}
			err = c.API.Post(ctx, "/deployments/drop/sessions", dropSessionBody(f), &session)
			depID, target, linkedName, createNew = session.Data.DeploymentID, session.Data.DeploymentID, f.name, true
		}
		if err != nil {
			return nil, err
		}
		sid = session.Data.SessionID
	}
	if sid == "" || depID == "" {
		return nil, fmt.Errorf("the server opened no upload session")
	}
	if !isJSON() {
		p.Line("Uploading %s (%s)...", f.dir, humanBytes(float64(len(archive))))
	}

	// 2. Upload the archive as 8 MiB chunks of raw bytes.
	for i := 0; i < len(archive); i += dropChunkBytes {
		end := i + dropChunkBytes
		if end > len(archive) {
			end = len(archive)
		}
		req := api.Request{
			Method:      http.MethodPost,
			Path:        fmt.Sprintf("/deployments/%s/drop/sessions/%s/chunks", depID, sid),
			Body:        bytes.NewReader(archive[i:end]),
			ContentType: "application/octet-stream",
		}
		resp, err := c.API.Do(ctx, req)
		if err != nil {
			return nil, err
		}
		resp.Body.Close()
	}

	// 3. Complete: assemble, publish, and promote unless --no-promote asked for
	//    a version that is not live yet.
	promote := !f.noPromote
	// The env goes in BEFORE the version is published, so the first release carries it. Set afterwards it
	// cut a second release for the same version (WO-01: two releases per publish with --env).
	if len(env) > 0 {
		if err := setDeploymentEnv(ctx, c, depID, env); err != nil {
			return nil, err
		}
	}
	var done dropCompleteResponse
	completePath := fmt.Sprintf("/deployments/%s/drop/sessions/%s/complete", depID, sid)
	completeBody := map[string]any{"promote": promote}
	// A server app: the upload is published as a dynamic release that runs
	// this command (the server answers 202 and starts its runtime).
	if f.runCommand != "" {
		completeBody["start_command"] = f.runCommand
		if f.port != 0 {
			completeBody["port"] = f.port
		}
	}
	if err := c.API.Post(ctx, completePath, completeBody, &done); err != nil {
		return nil, err
	}
	if len(done.Data.SkippedPaths) > 0 && !isJSON() {
		p.Line("Not uploaded (keys and secret files are never published): %s", strings.Join(done.Data.SkippedPaths, ", "))
	}
	promoted := done.Data.Promoted || promote
	// The name printed is the deployment this run went to: the one the folder
	// is linked to, or the reference the caller gave. The folder name is only
	// a default for a deployment being created.
	name := f.name
	if !createNew {
		name = firstNonEmpty(linkedName, target, f.name)
	}
	out := &deployOutcome{
		ID:        firstNonEmpty(done.Data.DeploymentID, depID),
		Name:      name,
		State:     "running",
		URL:       done.Data.PublicURL,
		Source:    "upload",
		VersionID: done.Data.VersionID,
		Promoted:  &promoted,
		Existing:  !createNew,
	}
	if !promoted {
		out.State = "uploaded"
	}
	if done.Data.Kind == "dynamic" {
		out.State = firstNonEmpty(done.Data.State, "building")
	}
	// Remember (or refresh) the deployment so the next 'miosa deploy' in this
	// folder pushes a new version instead of creating a duplicate. Written on
	// every push too, so folders deployed before the link existed get one.
	link := deploymentLink{DeploymentID: out.ID, Name: firstNonEmpty(out.Name, linkedName, f.name)}
	_ = writeDeploymentLink(root, link)
	// `push --dir ./dist` from the project folder: the project folder is where
	// the next bare 'miosa deploy' looks, so link it too.
	if project := projectFolderFor(root); project != "" {
		_ = writeDeploymentLink(project, link)
	}
	return out, nil
}

// ─── shared helpers ───────────────────────────────────────────────────────────

// setDeploymentEnv sets environment variables after a create, for the sources
// whose create route takes no env map (sandbox and upload).
func setDeploymentEnv(ctx context.Context, c *client.Client, id string, env map[string]any) error {
	return c.API.Post(ctx, "/deployments/"+id+"/env", map[string]any{"env": env}, nil)
}

// addDeploymentDomain attaches a custom domain after a create.
func addDeploymentDomain(ctx context.Context, c *client.Client, id, hostname string) error {
	return c.API.Post(ctx, "/deployments/"+id+"/domains", map[string]any{"hostname": hostname}, nil)
}

// publishPhase and publishStatus are the `publish` object of GET /deployments/:id: the real phases between a
// build starting and the app answering over verified HTTPS.
type publishPhase struct {
	Name       string `json:"name"`
	State      string `json:"state"`
	StartedAt  string `json:"started_at"`
	FinishedAt string `json:"finished_at"`
}

type publishStatus struct {
	AttemptID  string         `json:"attempt_id"`
	Status     string         `json:"status"`
	Phase      string         `json:"phase"`
	Phases     []publishPhase `json:"phases"`
	WaitReason string         `json:"wait_reason"`
	Failed     *struct {
		Phase  string `json:"phase"`
		Reason string `json:"reason"`
	} `json:"failed"`
}

// phaseLabels are what each phase is called on the terminal.
var phaseLabels = map[string]string{
	"build":       "build",
	"runtime":     "runtime started",
	"route":       "route published",
	"certificate": "HTTPS certificate",
	"reachable":   "reachable",
	"live":        "live",
}

// phasePrinter prints each phase the moment it finishes, with how long it took, and the wait reason when it
// changes. Nothing is printed in JSON mode, which only wants the final result.
func phasePrinter(p *output.Printer) func(prev, cur *publishStatus, elapsed time.Duration) {
	return func(prev, cur *publishStatus, elapsed time.Duration) {
		if isJSON() || cur == nil {
			return
		}
		done := map[string]string{}
		if prev != nil {
			for _, ph := range prev.Phases {
				done[ph.Name] = ph.State
			}
		}
		for _, ph := range cur.Phases {
			if ph.State == done[ph.Name] || (ph.State != "done" && ph.State != "skipped" && ph.State != "failed") {
				continue
			}
			label := phaseLabels[ph.Name]
			switch ph.State {
			case "done":
				p.Line("  %-18s done      %s", label, phaseTook(ph))
			case "skipped":
				p.Line("  %-18s skipped", label)
			case "failed":
				p.Line("  %-18s FAILED    %s", label, phaseTook(ph))
			}
		}
		if cur.WaitReason != "" && (prev == nil || prev.WaitReason != cur.WaitReason) {
			p.Line("  ... %s (%s)", cur.WaitReason, elapsed.Round(time.Second))
		}
	}
}

// phaseTook is how long a phase ran, "-" when its timestamps are not both known.
func phaseTook(ph publishPhase) string {
	start, err1 := time.Parse(time.RFC3339Nano, ph.StartedAt)
	end, err2 := time.Parse(time.RFC3339Nano, ph.FinishedAt)
	if err1 != nil || err2 != nil || end.Before(start) {
		return "-"
	}
	return end.Sub(start).Round(100 * time.Millisecond).String()
}

// pollDeployment waits until a deployment is live and returns its final state and public URL.
//
// "Live" is the server's word: `publish.status == "live"` is set only after the control plane saw the app answer
// over verified HTTPS. A server that does not report `publish` yet (older than the phases) is judged by its
// state, as before. A failed publish returns an error naming the phase and the reason, so the command exits
// non-zero. onPhase, when set, is called after every poll with the previous and the current status.
func pollDeployment(ctx context.Context, c *client.Client, id string, timeout time.Duration, onPhase ...func(prev, cur *publishStatus, elapsed time.Duration)) (string, string, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	started := time.Now()
	var last deployResponse
	var prev *publishStatus
	for {
		var resp struct {
			Data deployResponse `json:"data"`
		}
		if err := c.API.Get(ctx, "/deployments/"+id, nil, &resp); err != nil {
			// The poll's own deadline is not a server error: report it as the timeout it is, with the
			// deployment id and whatever reason the last poll carried.
			if ctx.Err() != nil {
				return last.State, last.PublicURL, waitTimeoutError(id, last, timeout)
			}
			return last.State, last.PublicURL, err
		}
		last = resp.Data
		for _, fn := range onPhase {
			fn(prev, last.Publish, time.Since(started))
		}
		prev = last.Publish

		if pub := last.Publish; pub != nil {
			switch pub.Status {
			case "live":
				return last.State, last.PublicURL, nil
			case "failed":
				reason, phase := "unknown reason", "publish"
				if pub.Failed != nil {
					reason, phase = pub.Failed.Reason, pub.Failed.Phase
				}
				return last.State, last.PublicURL, fmt.Errorf("publishing failed at %s: %s (run 'miosa deploy logs %s' for the build and app output)", phaseLabels[phase], reason, orDefault(last.Name, id))
			}
		} else {
			switch last.State {
			case "running":
				return last.State, last.PublicURL, nil
			case "failed":
				// The document carries the reason; print it instead of sending the user to
				// the logs for a code the API already returned (FounderOS, 2026-10-20).
				if last.Failure != nil {
					return last.State, last.PublicURL, fmt.Errorf("deployment %s failed - %s; run 'miosa deploy logs %s' for the build and app output", id, last.Failure.String(), orDefault(last.Name, id))
				}
				return last.State, last.PublicURL, fmt.Errorf("deployment %s is in the failed state; run 'miosa deploy logs %s' to see why", id, orDefault(last.Name, id))
			}
		}
		select {
		case <-ctx.Done():
			return last.State, last.PublicURL, waitTimeoutError(id, last, timeout)
		case <-time.After(pollEvery):
		}
	}
}

// waitTimeoutError is the sentence a --wait run ends with when it gave up: the deployment, the
// state it was last in, and whatever reason the API had recorded. A timeout is not a failure, so it
// never claims one, and it always names the id the command printed.
func waitTimeoutError(id string, last deployResponse, timeout time.Duration) error {
	waiting := ""
	if last.Publish != nil && last.Publish.WaitReason != "" {
		waiting = ", " + last.Publish.WaitReason
	}

	failure := ""
	if last.Failure != nil {
		failure = ", failure: " + last.Failure.String()
	}

	return fmt.Errorf(
		"deployment %s timed out after %s waiting to go live (last state: %s%s%s); 'miosa deploy get %s' and 'miosa deploy logs %s' show what it is doing",
		id, timeout, orDash(last.State), waiting, failure, id, orDefault(last.Name, id),
	)
}

// deployEnvMap turns repeated KEY=VALUE flags into a map.
func deployEnvMap(pairs []string) (map[string]any, error) {
	if len(pairs) == 0 {
		return nil, nil
	}
	m := make(map[string]any, len(pairs))
	for _, kv := range pairs {
		k, v, ok := strings.Cut(kv, "=")
		if !ok || k == "" {
			return nil, fmt.Errorf("--env expects KEY=VALUE (got %q)", kv)
		}
		m[k] = v
	}
	return m, nil
}

// tarGzDir packages a directory into a gzip-compressed tar, leaving out .git,
// secrets and whatever .miosaignore lists (upload_excludes.go). The Drop route
// detects the gzip magic and unpacks it server-side.
func tarGzDir(root string) ([]byte, []skippedPath, error) {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	filter, err := newUploadFilter(root)
	if err != nil {
		return nil, nil, fmt.Errorf("reading %s: %w", miosaIgnoreFile, err)
	}
	var skipped []skippedPath

	walkErr := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		if skip, why := filter.skip(filepath.ToSlash(rel), d.IsDir()); skip {
			skipped = append(skipped, skippedPath{Path: filepath.ToSlash(rel), Why: why, Dir: d.IsDir()})
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		hdr, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}
		hdr.Name = filepath.ToSlash(rel)
		if d.IsDir() {
			hdr.Name += "/"
		}
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		defer f.Close()
		_, err = io.Copy(tw, f)
		return err
	})
	if walkErr != nil {
		return nil, nil, fmt.Errorf("packaging %s: %w", root, walkErr)
	}
	if err := tw.Close(); err != nil {
		return nil, nil, err
	}
	if err := gz.Close(); err != nil {
		return nil, nil, err
	}
	return buf.Bytes(), skipped, nil
}

// warnSkipped names each path an upload left out, except the ones nobody
// wants published (git metadata, CLI state, macOS files).
func warnSkipped(w io.Writer, skipped []skippedPath) {
	for _, s := range skipped {
		switch s.Why {
		case "git metadata", "MIOSA CLI state", "macOS metadata", "upload rules":
			continue
		}
		name := s.Path
		if s.Dir {
			name += "/"
		}
		fmt.Fprintf(w, "miosa: not uploading %s (%s); add !%s to %s to upload it\n", name, s.Why, s.Path, miosaIgnoreFile)
	}
}

// renderDeploy prints the outcome as fields or JSON.
func renderDeploy(p *output.Printer, out *deployOutcome) error {
	if isJSON() {
		m := map[string]any{
			"id":     out.ID,
			"name":   out.Name,
			"state":  out.State,
			"source": out.Source,
		}
		if out.VersionID != "" {
			m["version_id"] = out.VersionID
		}
		if out.Promoted != nil {
			m["promoted"] = *out.Promoted
		}
		if out.Slug != "" {
			m["slug"] = out.Slug
		}
		if out.URL != "" {
			m["url"] = out.URL
			m["public_url"] = out.URL
		}
		if out.Domain != "" {
			m["domain"] = out.Domain
		}
		if out.AIAccess != "" {
			m["ai_access"] = out.AIAccess
		}
		if out.WebhookSecret != "" {
			m["webhook_secret"] = out.WebhookSecret
		}
		if out.WorkspaceID != "" {
			m["workspace_id"] = out.WorkspaceID
		}
		return p.JSON(m)
	}
	if out.Promoted != nil && !*out.Promoted {
		return renderUnpromotedUpload(p, out)
	}
	if out.Existing {
		p.Success("Deployed %q (%s)", out.Name, out.ID)
	} else {
		p.Success("Created deployment %q (%s)", out.Name, out.ID)
	}
	rows := [][2]string{{"State", out.State}, {"Source", out.Source}}
	if out.WorkspaceID != "" {
		rows = append(rows, [2]string{"Workspace", out.WorkspaceID})
	}
	if out.Slug != "" {
		rows = append(rows, [2]string{"Slug", out.Slug})
	}
	if out.URL != "" {
		rows = append(rows, [2]string{"URL", out.URL})
	}
	if out.Domain != "" {
		rows = append(rows, [2]string{"Domain", out.Domain + " (run 'miosa deploy domains " + out.Name + "')"})
	}
	if out.AIAccess != "" {
		rows = append(rows, [2]string{"AI access", out.AIAccess})
	}
	p.Fields(rows)
	if out.WebhookSecret != "" {
		p.Line("")
		p.Line("Add this GitHub webhook so pushes redeploy (the secret is shown once):")
		p.Line("  URL:          https://api.miosa.ai%s", webhookPath)
		p.Line("  Content type: application/json")
		p.Line("  Secret:       %s", out.WebhookSecret)
	}
	return nil
}

// renderUnpromotedUpload renders a version that was uploaded but deliberately
// left off the air: the site still serves the previous version, and the id to
// promote is the one thing the caller needs.
func renderUnpromotedUpload(p *output.Printer, out *deployOutcome) error {
	p.Success("Uploaded version %s of %q (not live)", shortRef(out.VersionID), out.Name)
	rows := [][2]string{
		{"Deployment", out.ID},
		{"Version", out.VersionID},
	}
	if out.URL != "" {
		rows = append(rows, [2]string{"Still serving", out.URL})
	}
	p.Fields(rows)
	p.Line("")
	p.Line("Make it live with: miosa deploy promote %s", out.VersionID)
	return nil
}

// shortRef is the 8-character form a person reads and can type back.
func shortRef(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

// ─── bare 'miosa deploy': deploy the current directory ────────────────────────

// newDeployCmd builds the deploy group and wires the bare command to a
// deploy-the-current-directory shortcut: a git repository when the directory is
// one (the 1.x behaviour), otherwise a directory upload.
func newDeployCmd() *cobra.Command {
	f := &deployFlags{}
	cmd := group("deploy", "Create and operate deployments",
		`Deployments are the running, versioned apps published from sandboxes, git or
uploads. <deployment> is a name, slug or id.

  miosa deploy                          deploy the current directory
  miosa deploy create --from-sandbox my-box --wait
  miosa deploy create --git https://github.com/me/app
  miosa deploy list
  miosa deploy get my-app
  miosa deploy logs my-app -n 100
  miosa deploy rollback my-app --to <version-id>
  miosa deploy domains my-app`, []string{"deployments", "deployment"}, deployOps()...)
	bindDeploySourceFlags(cmd, f, false)
	cmd.Flags().BoolVar(&f.yes, "yes", false, "Do not ask for confirmation")
	cmd.Args = cobra.MaximumNArgs(1)
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		return runDeployCurrentDir(cmd, args, f)
	}
	cmd.AddCommand(newDeployCreateCmd())
	cmd.AddCommand(newDeployPushCmd())
	cmd.AddCommand(newDeployRedeployCmd())
	cmd.AddCommand(newDeployPromoteCmd())
	cmd.AddCommand(newDeployDomainUpdateCmd())
	cmd.AddCommand(newDeployUpdateCmd())
	cmd.AddCommand(newDeployGetCmd())
	cmd.AddCommand(newDeployVersionsCmd())
	cmd.AddCommand(newDeployDiffCmd())
	cmd.AddCommand(newDeployEnvPullCmd())
	return cmd
}

// runDeployCurrentDir deploys the working directory, choosing git when the
// directory is a repository and a directory upload otherwise.
func runDeployCurrentDir(cmd *cobra.Command, args []string, flags *deployFlags) error {
	f := *flags
	if len(args) > 0 && f.name == "" {
		f.name = args[0]
	}
	// An unlinked folder named like an existing deployment belongs to it: link
	// and push (an upload deployment) or point at redeploy (a repository one),
	// whether or not the folder is a git checkout. A new deployment needs a
	// different name on purpose.
	if handled, err := deployToExistingByName(cmd, &f); handled || err != nil {
		return err
	}
	if repo, branch := gitOrigin("."); repo != "" {
		f.git = repo
		if f.branch == "" {
			f.branch = branch
		}
	} else {
		f.dir = "."
		// A folder already deployed is pushed to, not created: name it after
		// the deployment it belongs to rather than after the folder, so the
		// prompt and the result line name what actually changed.
		if f.name == "" {
			if link, ok := readDeploymentLink("."); ok && link.Name != "" {
				f.name = link.Name
			}
		}
	}
	if f.name == "" {
		if wd, err := os.Getwd(); err == nil {
			f.name = filepath.Base(wd)
		}
	}
	if !f.yes {
		prompt := fmt.Sprintf("Deploy the current directory as a new deployment %q?", orDefault(f.name, "this directory"))
		if f.git == "" && f.deployment == "" && f.name != "" {
			if _, linked := readDeploymentLink("."); !linked {
				if c, _, err := buildClient(); err == nil {
					if id, name, found, _ := existingDeploymentByName(cmd.Context(), c, f.name); found {
						prompt = fmt.Sprintf("Deployment %q (%s) already exists. Push this directory as a new version of it and link this folder?", name, shortRef(id))
					}
				}
			} else {
				prompt = fmt.Sprintf("Push the current directory as a new version of %q?", f.name)
			}
		}
		if err := confirmDestructive(cmd, prompt); err != nil {
			return err
		}
	}
	return runDeployCreate(cmd, nil, &f)
}

// gitOrigin returns "origin"'s URL (normalised to HTTPS) and the current
// branch for the repository at dir, or empty strings when dir is not one.
func gitOrigin(dir string) (repoURL, branch string) {
	if _, err := runGit(dir, "rev-parse", "--git-dir"); err != nil {
		return "", ""
	}
	if out, err := runGit(dir, "remote", "get-url", "origin"); err == nil {
		repoURL = normalizeGitURL(strings.TrimSpace(out))
	}
	if out, err := runGit(dir, "rev-parse", "--abbrev-ref", "HEAD"); err == nil {
		branch = strings.TrimSpace(out)
	}
	return repoURL, branch
}

func runGit(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	return string(out), err
}

// repoNameFromURL derives a deployment name from a git URL's last path segment.
func repoNameFromURL(u string) (string, error) {
	trimmed := strings.TrimRight(normalizeGitURL(strings.TrimSpace(u)), "/")
	if trimmed == "" {
		return "", fmt.Errorf("--git expects a repository URL")
	}
	name := filepath.Base(trimmed)
	name = strings.TrimSuffix(name, ".git")
	if name == "" || name == "." || name == "/" {
		return "", fmt.Errorf("cannot derive a name from --git %q; pass --name", u)
	}
	return name, nil
}

func normalizeGitURL(u string) string {
	if u == "" {
		return ""
	}
	if rest, ok := strings.CutPrefix(u, "git@github.com:"); ok {
		u = "https://github.com/" + rest
	}
	return strings.TrimSuffix(u, ".git")
}

// existingDeploymentByName reports the deployment whose name (or slug) is
// exactly name. The lookup route also accepts short ids and hostnames, so a
// hit counts only when the returned name or slug is the one asked for.
func existingDeploymentByName(ctx context.Context, c *client.Client, name string) (id, found string, ok bool, err error) {
	id, found, _, ok, err = existingDeploymentByNameWithSource(ctx, c, name)
	return id, found, ok, err
}

// existingDeploymentByNameWithSource is existingDeploymentByName plus the
// deployment's source_type ("upload", "git", ...).
func existingDeploymentByNameWithSource(ctx context.Context, c *client.Client, name string) (id, found, source string, ok bool, err error) {
	var resp struct {
		Data map[string]any `json:"data"`
	}
	if gerr := c.API.Get(ctx, "/deployments/"+url.PathEscape(name), nil, &resp); gerr != nil {
		if ae, isAPI := asAPIError(gerr); isAPI && (ae.Status == 404 || ae.Status == 400) {
			return "", "", "", false, nil
		}
		if ae, isAPI := asAPIError(gerr); isAPI && strings.EqualFold(ae.Code, "AMBIGUOUS_DEPLOYMENT") {
			return "", "", "", false, fmt.Errorf("more than one deployment is named %q - pass --deployment <id>: %w", name, gerr)
		}
		return "", "", "", false, gerr
	}
	gotName, _ := resp.Data["name"].(string)
	gotSlug, _ := resp.Data["slug"].(string)
	if !strings.EqualFold(gotName, name) && !strings.EqualFold(gotSlug, name) {
		return "", "", "", false, nil
	}
	id, _ = resp.Data["id"].(string)
	source, _ = resp.Data["source_type"].(string)
	return id, firstNonEmpty(gotName, gotSlug, name), source, id != "", nil
}

// deployToExistingByName handles a bare 'miosa deploy' in an unlinked folder
// whose name matches an existing deployment. It reports handled=false when
// there is no such deployment (or the folder is already linked).
func deployToExistingByName(cmd *cobra.Command, f *deployFlags) (bool, error) {
	if f.deployment != "" {
		return false, nil
	}
	if _, linked := readDeploymentLink("."); linked {
		return false, nil
	}
	name := f.name
	if name == "" {
		wd, err := os.Getwd()
		if err != nil {
			return false, nil
		}
		name = filepath.Base(wd)
	}
	c, _, err := buildClient()
	if err != nil {
		return false, nil
	}
	id, found, source, ok, err := existingDeploymentByNameWithSource(cmd.Context(), c, name)
	if err != nil {
		return true, die(err)
	}
	if !ok {
		return false, nil
	}
	if strings.EqualFold(source, "git") {
		return true, usagef("deployment %q (%s) already exists and builds from its repository - rebuild it with 'miosa deploy redeploy %s', or pass --name for a new deployment", found, shortRef(id), found)
	}
	if !f.yes {
		if err := confirmDestructive(cmd, fmt.Sprintf("Deployment %q (%s) already exists. Link this folder and push it as a new version?", found, shortRef(id))); err != nil {
			return true, err
		}
	}
	f.git, f.dir, f.deployment, f.name = "", ".", id, found
	return true, runDeployCreate(cmd, nil, f)
}

// projectFolderFor is the current directory when the upload root sits inside
// it (push --dir ./dist), so the folder a person deploys from gets the link.
// Never the home directory: ~/.miosa is the CLI's own config.
func projectFolderFor(root string) string {
	wd, err := os.Getwd()
	if err != nil {
		return ""
	}
	wd, _ = filepath.Abs(wd)
	if home, herr := os.UserHomeDir(); herr == nil && filepath.Clean(home) == filepath.Clean(wd) {
		return ""
	}
	rel, err := filepath.Rel(wd, root)
	if err != nil || rel == "." || strings.HasPrefix(rel, "..") || filepath.IsAbs(rel) {
		return ""
	}
	return wd
}

// deployFromForge creates a deployment that builds a Forge repository branch
// (repo_provider "forge"). With auto (the default) every push to the branch
// builds and releases a new version.
func deployFromForge(ctx context.Context, c *client.Client, p *output.Printer, f *deployFlags, env map[string]any) (*deployOutcome, error) {
	if f.name == "" {
		f.name = forgeRepoShortName(f.forge)
	}
	body := map[string]any{
		"name":             f.name,
		"repo_provider":    "forge",
		"forge_repository": f.forge,
		"auto_deploy":      !f.noAuto,
	}
	if f.deployType != "" && f.deployType != "static" && f.deployType != "dynamic" {
		return nil, usagef("--type with --forge is static or dynamic")
	}
	// --type and --run-command used to be dropped here, so a repository with
	// a build script still came up dynamic under --type static.
	for k, v := range map[string]string{"branch": f.branch, "build_command": f.buildCommand, "root_directory": f.rootDir, "project_id": f.project, "deployment_type": f.deployType, "run_command": f.runCommand} {
		if v != "" {
			body[k] = v
		}
	}
	if len(env) > 0 {
		body["env"] = env
	}
	if f.workspaceID != "" {
		body["workspace_id"] = f.workspaceID
	}
	if !isJSON() {
		p.Line("Creating deployment %q from Forge repository %s...", f.name, f.forge)
	}
	var resp createDeployResponse
	if err := c.API.Post(ctx, "/deployments", body, &resp); err != nil {
		if ae, ok := asAPIError(err); ok && ae.Code == "PROJECT_LINKED_TO_OTHER_REPOSITORY" {
			return nil, usagef("%s - pass --project <id> of a project with no repository ('miosa project list')", ae.Message)
		}
		return nil, err
	}
	out := &deployOutcome{
		ID:          resp.Data.ID,
		Name:        firstNonEmpty(resp.Data.Name, f.name),
		Slug:        resp.Data.Slug,
		State:       firstNonEmpty(resp.Data.State, "building"),
		URL:         resp.Data.PublicURL,
		Source:      "forge",
		WorkspaceID: firstNonEmpty(resp.Data.WorkspaceID, f.workspaceID),
	}
	if out.ID == "" {
		return nil, fmt.Errorf("the server created the deployment but returned no id")
	}
	return out, nil
}

func forgeRepoShortName(ref string) string {
	if i := strings.LastIndex(ref, "/"); i >= 0 {
		return ref[i+1:]
	}
	return ref
}

type errNoChanges struct {
	version int
	id      string
}

func (e errNoChanges) Error() string { return fmt.Sprintf("no changes since version %d", e.version) }

// unchangedSinceLatest compares the folder's files with the latest version's
// manifest. Any error answers "changed", so a push is never skipped by mistake.
func unchangedSinceLatest(ctx context.Context, c *client.Client, deployment, root string) (int, string, bool) {
	var list struct {
		Data []map[string]any `json:"data"`
	}
	if err := c.API.Get(ctx, "/deployments/"+url.PathEscape(deployment)+"/versions", nil, &list); err != nil || len(list.Data) == 0 {
		return 0, "", false
	}
	latest := list.Data[0]
	for _, v := range list.Data {
		if n, _ := v["version_number"].(float64); n > latest["version_number"].(float64) {
			latest = v
		}
	}
	id, _ := latest["id"].(string)
	var one struct {
		Data struct {
			ArtifactManifest *struct {
				Files []manifestFile `json:"files"`
			} `json:"artifact_manifest"`
		} `json:"data"`
	}
	if id == "" || c.API.Get(ctx, "/deployments/"+url.PathEscape(deployment)+"/versions/"+url.PathEscape(id), nil, &one) != nil ||
		one.Data.ArtifactManifest == nil || len(one.Data.ArtifactManifest.Files) == 0 {
		return 0, "", false
	}
	local, err := localFileManifest(root)
	if err != nil || len(local) != len(one.Data.ArtifactManifest.Files) {
		return 0, "", false
	}
	for _, f := range one.Data.ArtifactManifest.Files {
		if local[f.Path] != f.Sha {
			return 0, "", false
		}
	}
	n, _ := latest["version_number"].(float64)
	return int(n), id, true
}

// localFileManifest hashes every regular file the upload would carry (same
// exclusions as tarGzDir: .git and .miosa).
func localFileManifest(root string) (map[string]string, error) {
	out := map[string]string{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if path != root && (d.Name() == ".git" || d.Name() == deploymentLinkDir) {
				return fs.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		defer f.Close()
		h := sha256.New()
		if _, err := io.Copy(h, f); err != nil {
			return err
		}
		out[filepath.ToSlash(rel)] = hex.EncodeToString(h.Sum(nil))
		return nil
	})
	return out, err
}

// dropSessionBody opens a new upload deployment: its name, and the workspace
// when one was chosen.
func dropSessionBody(f *deployFlags) map[string]any {
	body := map[string]any{"name": f.name}
	if f.workspaceID != "" {
		body["workspace_id"] = f.workspaceID
	}
	return body
}

// deploymentWorkspace reads back the workspace a deployment landed in. It is
// best effort: a create must not fail because the answer could not be read.
func deploymentWorkspace(cmd *cobra.Command, c *client.Client, id string) string {
	if id == "" {
		return ""
	}
	var resp struct {
		Data struct {
			WorkspaceID string `json:"workspace_id"`
		} `json:"data"`
	}
	if err := c.API.Get(cmd.Context(), "/deployments/"+url.PathEscape(id), nil, &resp); err != nil {
		return ""
	}
	return resp.Data.WorkspaceID
}

// deployBuildResponse is the build a redeploy starts (the API's build document).
type deployBuildResponse struct {
	ID           string `json:"id"`
	State        string `json:"state"`
	ErrorMessage string `json:"error_message"`
	LogURL       string `json:"log_url"`
}

// runDeployRedeploy starts a rebuild and prints the build it started.
//
// The old declarative form printed "Redeploying <name>" and nothing else: when the build then failed,
// the customer saw the previous version still serving and could not tell whether their redeploy had
// done anything (ClinicIQ v12, FounderOS batch F: "redeploy rebuilds v11"). The build id and state are
// printed, a build that is already failed is an error naming its reason, and the follow-up commands
// are named.
// redeployWaitTimeout is how long `deploy redeploy --wait` follows the deployment, the same default
// as `deploy create --wait-timeout`.
const redeployWaitTimeout = 10 * time.Minute

func newDeployRedeployCmd() *cobra.Command {
	var commit string
	var wait bool

	cmd := &cobra.Command{
		Use:   "redeploy <deployment>",
		Short: "Build and release again from the same source",
		Long: `Rebuilds the deployment from its configured source and releases the result.

The command prints the build it starts. A failed build marks the deployment failed and records the
reason: 'miosa deploy get <deployment>' reports it and 'miosa deploy builds <deployment>' lists the
run. A deployment published from a sandbox has no repository to rebuild, so it re-runs the release it
already has. An uploaded static site has nothing to rebuild: push a new version with
'miosa deploy push'.`,
		Example: "  miosa deploy redeploy my-app\n  miosa deploy redeploy my-app --commit 1a2b3c4d",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDeployRedeploy(cmd, args[0], commit, wait)
		},
	}

	fs := cmd.Flags()
	fs.StringVar(&commit, "commit", "", "Forge deployments: build this commit instead of the branch head")
	fs.BoolVar(&wait, "wait", false, "Wait until the redeployed version is live (10 minutes)")
	return cmd
}

func runDeployRedeploy(cmd *cobra.Command, ref, commit string, wait bool) error {
	c, _, err := buildClient()
	if err != nil {
		return die(err)
	}
	body := map[string]any{}
	if commit != "" {
		body["commit_sha"] = commit
	}

	var resp struct {
		Data deployBuildResponse `json:"data"`
	}
	if err := c.API.Post(cmd.Context(), "/deployments/"+url.PathEscape(ref)+"/redeploy", body, &resp); err != nil {
		return die(err)
	}

	b := resp.Data
	p := printerFor(cmd)
	if !isJSON() {
		p.Line("Rebuilding %s: build %s %s", ref, shortID(b.ID), orDash(b.State))
	}

	if b.State == "failed" || b.ErrorMessage != "" {
		if isJSON() {
			return p.JSON(map[string]any{"build": b, "deployment": ref})
		}
		return fmt.Errorf(
			"build %s failed: %s ('miosa deploy logs %s' has the output)",
			shortID(b.ID), orDefault(b.ErrorMessage, "no reason recorded"), ref,
		)
	}

	if wait {
		// --wait follows the deployment, not the build: the build id is not the deployment id, and
		// a redeploy of a sandbox artifact re-promotes an existing release, so the state is what
		// tells the caller the new version is actually serving.
		if !isJSON() {
			p.Line("Waiting for %s to go live...", ref)
		}
		if _, _, err := pollDeployment(cmd.Context(), c, ref, redeployWaitTimeout, phasePrinter(p)); err != nil {
			return die(err)
		}
	}

	if isJSON() {
		return p.JSON(map[string]any{"build": b, "deployment": ref})
	}
	if wait {
		p.Line("%s is live.", ref)
		return nil
	}
	p.Line("If it fails, 'miosa deploy get %s' reports the reason and 'miosa deploy builds %s' lists the run.", ref, ref)
	return nil
}

// serverManifest names the server-app manifest at the top of dir (package.json,
// requirements.txt, go.mod, ...) when the folder has no index.html to serve as
// a site, else "".
func serverManifest(dir string) string {
	if _, err := os.Stat(filepath.Join(dir, "index.html")); err == nil {
		return ""
	}
	for _, name := range []string{"package.json", "requirements.txt", "pyproject.toml", "go.mod", "Gemfile", "Cargo.toml", "Dockerfile"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err == nil {
			return name
		}
	}
	return ""
}
