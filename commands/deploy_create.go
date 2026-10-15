package commands

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
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
	wait         bool
	waitTimeout  time.Duration
	yes          bool
	noPromote    bool
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
	fs.BoolVar(&f.wait, "wait", false, "Wait until the deployment is live, then print its URL")
	fs.DurationVar(&f.waitTimeout, "wait-timeout", 10*time.Minute, "How long --wait may take")
	fs.BoolVar(&f.noPromote, "no-promote", false, "Upload the version without making it live; prints the version to promote later")
	if createOnly {
		fs.StringVar(&f.fromSandbox, "from-sandbox", "", "Publish this sandbox (name, id or 'current')")
		fs.StringVar(&f.git, "git", "", "Deploy this git repository URL")
		fs.StringVar(&f.dir, "dir", "", "With --from-sandbox: the app's path inside the sandbox. On its own: a local directory to upload")
		fs.StringVar(&f.image, "image", "", "App Engine container image (not exposed by the public API; see --type docker_deploy)")
		fs.IntVar(&f.port, "port", 0, "With --from-sandbox: the port the app serves on")
		fs.StringVar(&f.entrypoint, "entrypoint", "", "With --from-sandbox: the entry file for a static publish")
		fs.StringVar(&f.deployType, "type", "", "Runtime to publish as: static, dynamic or docker_deploy (default: static, or dynamic with --run-command)")
		fs.StringVar(&f.runCommand, "run-command", "", "Command the app starts with (makes the publish dynamic)")
		fs.StringVar(&f.buildCommand, "build-command", "", "Build command (git source only)")
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
  --dir <path>               Upload a local directory (built static output).

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
This is what 'miosa deploy' does automatically once a folder is linked
(.miosa/deployment.json is written on the first deploy).`,
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
	fs.BoolVar(&f.wait, "wait", false, "Wait until the new version is live, then print its URL")
	fs.DurationVar(&f.waitTimeout, "wait-timeout", 10*time.Minute, "How long --wait may take")
	fs.BoolVar(&f.noPromote, "no-promote", false, "Upload the version without making it live; prints the version to promote later")
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
	for _, set := range []bool{f.fromSandbox != "", f.git != "", localDir} {
		if set {
			sources++
		}
	}
	switch {
	case sources == 0:
		return usagef("give a source: --from-sandbox <sandbox>, --git <url> or --dir <path> (see 'miosa deploy create --help')")
	case sources > 1:
		return usagef("give exactly one source of --from-sandbox, --git or --dir")
	}
	if f.port != 0 && f.fromSandbox == "" {
		return usagef("--port only applies to --from-sandbox")
	}
	if f.buildCommand != "" && f.fromSandbox != "" {
		return usagef("--build-command does not apply to --from-sandbox")
	}
	if f.noPromote && (f.fromSandbox != "" || f.git != "") {
		return usagef("--no-promote only applies to a local upload (--dir, or no source in a folder)")
	}

	env, err := deployEnvMap(f.env)
	if err != nil {
		return usagef("%v", err)
	}

	var httpTimeout time.Duration
	if f.wait {
		httpTimeout = f.waitTimeout + 30*time.Second
	}
	c, _, err := buildClientTimeout(httpTimeout)
	if err != nil {
		return die(err)
	}
	p := printerFor(cmd)

	var out *deployOutcome
	switch {
	case f.fromSandbox != "":
		out, err = deployFromSandbox(cmd.Context(), c, p, &f, env)
	case f.git != "":
		out, err = deployFromGit(cmd.Context(), c, p, &f, env)
	default:
		out, err = deployFromDir(cmd.Context(), c, p, &f, env)
	}
	if err != nil {
		return die(err)
	}

	if f.domain != "" && out.Source != "sandbox" {
		if err := addDeploymentDomain(cmd.Context(), c, out.ID, f.domain); err != nil {
			return die(err)
		}
		out.Domain = f.domain
	}

	if f.wait {
		if !isJSON() {
			p.Line("Waiting for %s to go live...", orDefault(out.Name, out.ID))
		}
		state, url, err := pollDeployment(cmd.Context(), c, out.ID, f.waitTimeout)
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
	// Existing is true when this run pushed to a deployment that was already
	// there (a linked folder, or --deployment) instead of creating one.
	Existing bool
}

// deployResponse is the deployment object the API returns inside "data".
type deployResponse struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Slug      string `json:"slug"`
	State     string `json:"state"`
	PublicURL string `json:"public_url"`
}

// ─── source: sandbox ──────────────────────────────────────────────────────────

type sandboxDeployResponse struct {
	DeploymentID string `json:"deployment_id"`
	URL          string `json:"url"`
	State        string `json:"state"`
	Data         struct {
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
	if f.domain != "" {
		body["domain"] = f.domain
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
		ID:     firstNonEmpty(d.ID, resp.DeploymentID),
		Name:   firstNonEmpty(d.Name, name),
		Slug:   d.Slug,
		State:  firstNonEmpty(d.State, resp.State),
		URL:    firstNonEmpty(d.PublicURL, resp.URL),
		Source: "sandbox",
		Domain: f.domain,
	}
	if out.ID == "" {
		return nil, fmt.Errorf("the server accepted the publish but returned no deployment id")
	}
	if len(env) > 0 {
		if err := setDeploymentEnv(ctx, c, out.ID, env); err != nil {
			return nil, err
		}
	}
	return out, nil
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
	}
	if out.ID == "" {
		return nil, fmt.Errorf("the server created the deployment but returned no id")
	}
	// A repo deployment is created in "pending"; nothing builds until a push or
	// a redeploy, so queue the first build now (as the 1.x CLI did).
	if err := c.API.Post(ctx, "/deployments/"+out.ID+"/redeploy", nil, nil); err != nil {
		return nil, err
	}
	out.State = "building"
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
		VersionID    string `json:"version_id"`
		DeploymentID string `json:"deployment_id"`
		PublicURL    string `json:"public_url"`
		Promoted     bool   `json:"promoted"`
	} `json:"data"`
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
	archive, err := tarGzDir(root)
	if err != nil {
		return nil, err
	}
	if len(archive) > dropSessionMaxBytes {
		return nil, usagef("--dir %s is %s packaged, over the 100 MiB upload limit", f.dir, humanBytes(float64(len(archive))))
	}

	// 1. Open an upload session. When this folder is already linked to a
	// deployment (or --deployment names one), push a new VERSION to that
	// deployment; otherwise create one and remember it.
	target := f.deployment
	linkedName := ""
	if target == "" {
		if link, ok := readDeploymentLink(root); ok {
			target = link.DeploymentID
			linkedName = link.Name
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

	var sid, depID string
	if createNew {
		var session dropSessionResponse
		if err := c.API.Post(ctx, "/deployments/drop/sessions", map[string]any{"name": f.name}, &session); err != nil {
			return nil, err
		}
		sid, depID = session.Data.SessionID, session.Data.DeploymentID
	} else {
		depID = target
		var session dropSessionResponse
		if err := c.API.Post(ctx, "/deployments/"+url.PathEscape(target)+"/drop/sessions", nil, &session); err != nil {
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
	var done dropCompleteResponse
	completePath := fmt.Sprintf("/deployments/%s/drop/sessions/%s/complete", depID, sid)
	if err := c.API.Post(ctx, completePath, map[string]any{"promote": promote}, &done); err != nil {
		return nil, err
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
	if len(env) > 0 {
		if err := setDeploymentEnv(ctx, c, out.ID, env); err != nil {
			return nil, err
		}
	}
	// Remember (or refresh) the deployment so the next 'miosa deploy' in this
	// folder pushes a new version instead of creating a duplicate. Written on
	// every push too, so folders deployed before the link existed get one.
	_ = writeDeploymentLink(root, deploymentLink{DeploymentID: out.ID, Name: firstNonEmpty(out.Name, linkedName, f.name)})
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

// pollDeployment waits until a deployment is running (live) or failed and
// returns its final state and public URL.
func pollDeployment(ctx context.Context, c *client.Client, id string, timeout time.Duration) (string, string, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var last deployResponse
	for {
		var resp struct {
			Data deployResponse `json:"data"`
		}
		if err := c.API.Get(ctx, "/deployments/"+id, nil, &resp); err != nil {
			return last.State, last.PublicURL, err
		}
		last = resp.Data
		switch last.State {
		case "running":
			return last.State, last.PublicURL, nil
		case "failed":
			return last.State, last.PublicURL, fmt.Errorf("the deployment is in the failed state; run 'miosa deploy logs %s' to see why", orDefault(last.Name, id))
		}
		select {
		case <-ctx.Done():
			return last.State, last.PublicURL, fmt.Errorf("timed out after %s waiting for the deployment to go live (last state: %s)", timeout, orDash(last.State))
		case <-time.After(pollEvery):
		}
	}
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

// tarGzDir packages a directory into a gzip-compressed tar, skipping .git.
// The Drop route detects the gzip magic and unpacks it server-side.
func tarGzDir(root string) ([]byte, error) {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)

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
		if d.IsDir() && (filepath.Base(path) == ".git" || filepath.Base(path) == deploymentLinkDir) {
			return fs.SkipDir
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
		return nil, fmt.Errorf("packaging %s: %w", root, walkErr)
	}
	if err := tw.Close(); err != nil {
		return nil, err
	}
	if err := gz.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
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
		if out.WebhookSecret != "" {
			m["webhook_secret"] = out.WebhookSecret
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
	if out.Slug != "" {
		rows = append(rows, [2]string{"Slug", out.Slug})
	}
	if out.URL != "" {
		rows = append(rows, [2]string{"URL", out.URL})
	}
	if out.Domain != "" {
		rows = append(rows, [2]string{"Domain", out.Domain + " (run 'miosa deploy domains " + out.Name + "')"})
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
	cmd.AddCommand(newDeployPromoteCmd())
	cmd.AddCommand(newDeployGetCmd())
	cmd.AddCommand(newDeployVersionsCmd())
	cmd.AddCommand(newDeployDiffCmd())
	return cmd
}

// runDeployCurrentDir deploys the working directory, choosing git when the
// directory is a repository and a directory upload otherwise.
func runDeployCurrentDir(cmd *cobra.Command, args []string, flags *deployFlags) error {
	f := *flags
	if len(args) > 0 && f.name == "" {
		f.name = args[0]
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
	var resp struct {
		Data map[string]any `json:"data"`
	}
	if gerr := c.API.Get(ctx, "/deployments/"+url.PathEscape(name), nil, &resp); gerr != nil {
		if ae, isAPI := asAPIError(gerr); isAPI && (ae.Status == 404 || ae.Status == 400) {
			return "", "", false, nil
		}
		if ae, isAPI := asAPIError(gerr); isAPI && strings.EqualFold(ae.Code, "AMBIGUOUS_DEPLOYMENT") {
			return "", "", false, fmt.Errorf("more than one deployment is named %q - pass --deployment <id>: %w", name, gerr)
		}
		return "", "", false, gerr
	}
	gotName, _ := resp.Data["name"].(string)
	gotSlug, _ := resp.Data["slug"].(string)
	if !strings.EqualFold(gotName, name) && !strings.EqualFold(gotSlug, name) {
		return "", "", false, nil
	}
	id, _ = resp.Data["id"].(string)
	return id, firstNonEmpty(gotName, gotSlug, name), id != "", nil
}
