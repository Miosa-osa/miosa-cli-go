package commands

import (
	"fmt"
	"github.com/Miosa-osa/miosa-cli-go/internal/api"
	"net/url"
	"path/filepath"
	"sort"

	"github.com/Miosa-osa/miosa-cli-go/internal/client"
	"github.com/Miosa-osa/miosa-cli-go/internal/output"
	"github.com/spf13/cobra"
)

// deployVersionCols is one version per row: what it is, how big it was, and
// whether it is the one serving.
var deployVersionCols = []Col{
	{Head: "N", Path: "version_number"},
	{Head: "ID", Path: "id", Fmt: "short"},
	{Head: "KIND", Path: "kind"},
	{Head: "FILES", Path: "artifact_manifest.file_count"},
	{Head: "SIZE", Path: "artifact_manifest.total_bytes", Fmt: "bytes"},
	{Head: "SHA", Path: "artifact_sha256", Fmt: "sha"},
	{Head: "STATE", Path: "state"},
	{Head: "SOURCE", Path: "metadata.publish_source"},
	{Head: "CREATED", Path: "created_at", Fmt: "age"},
}

// newDeployVersionsCmd lists a deployment's versions with their size and
// artifact hash, and marks the live one. Which version is live is a fact about
// the deployment, so this reads it once as well as the versions.
func newDeployVersionsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "versions <deployment>",
		Short: "List versions, with what each one ships and which is live",
		Long: `List a deployment's versions.

  miosa deploy versions my-app

Each row carries the version number, its kind, how many files it ships and how
big they are, the artifact sha256, and "live" on the version that is serving.
` + deployDiffExample,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDeployVersions(cmd, args[0])
		},
	}
	cmd.AddCommand(newDeployVersionsRmCmd())
	return cmd
}

// newDeployVersionsRmCmd deletes one version. The live version and any version
// a release points at are refused (here for the live one, by the API for both).
func newDeployVersionsRmCmd() *cobra.Command {
	var deployment, dir string
	var yes bool
	cmd := &cobra.Command{
		Use:     "rm <version>",
		Aliases: []string{"archive"},
		Short:   "Archive a version that is not live (also: versions archive)",
		Long: `Archives one version (number, short id or id) of the deployment named by
--deployment, or of the deployment this folder is linked to. Archiving keeps the
row, its logs and its artifact for audit, takes it off the version list and out of
static storage, and it can no longer be promoted. A version id on its own finds
its deployment. The live version is refused, and so is any version the server
still needs, such as the rollback target.`,
		Example: "  miosa deploy versions rm 90a9dee1\n  miosa deploy versions rm 3 --deployment my-site --yes",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, _, err := buildClient()
			if err != nil {
				return die(err)
			}
			dep, err := deploymentForVersions(deployment, dir)
			if err != nil {
				owner, _, found := "", 0, false
				if deployment == "" && dir == "" && versionRefShape.MatchString(args[0]) {
					owner, _, found = findVersionOwner(cmd, c, args[0])
				}
				if !found {
					return err
				}
				dep = owner
			}
			var d struct {
				Data map[string]any `json:"data"`
			}
			if err := c.API.Get(cmd.Context(), "/deployments/"+url.PathEscape(dep), nil, &d); err != nil {
				return die(fmt.Errorf("deployment %s: %w", dep, err))
			}
			var vs struct {
				Data []map[string]any `json:"data"`
			}
			if err := c.API.Get(cmd.Context(), "/deployments/"+url.PathEscape(dep)+"/versions", nil, &vs); err != nil {
				return die(fmt.Errorf("deployment %s: %w", dep, err))
			}
			v, err := matchVersion(vs.Data, args[0])
			if err != nil {
				return usagef("deployment %s: version %v", dep, err)
			}
			vid, _ := v["id"].(string)
			if live, _ := d.Data["active_version_id"].(string); live == vid {
				return usagef("version %s is live on %s - make another version live first ('miosa deploy rollback %s --to <version>')", shortID(vid), dep, dep)
			}
			if !yes {
				if err := confirmDestructive(cmd, fmt.Sprintf("Delete version %s of %s?", describeVersion(v), dep)); err != nil {
					return err
				}
			}
			key, kerr := newForgeIdempotencyKey()
			if kerr != nil {
				return die(kerr)
			}
			req := api.Request{Method: "DELETE", Path: "/deployments/" + url.PathEscape(dep) + "/versions/" + url.PathEscape(vid), IdempotencyKey: key}
			if _, err := c.API.JSONAny(cmd.Context(), req); err != nil {
				if ae, ok := asAPIError(err); ok && ae.Status == 409 {
					return usagef("version %s of %s: %s", shortID(vid), dep, ae.Message)
				}
				return die(fmt.Errorf("version %s of %s: %w", shortID(vid), dep, err))
			}
			printerFor(cmd).Success("Deleted version %s of %s", describeVersion(v), dep)
			return nil
		},
	}
	cmd.Flags().StringVar(&deployment, "deployment", "", "Deployment the version belongs to (default: the folder's linked deployment)")
	cmd.Flags().StringVar(&dir, "dir", "", "Folder whose .miosa/deployment.json names the deployment")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "Delete without asking")
	return cmd
}

func runDeployVersions(cmd *cobra.Command, ref string) error {
	p := printerFor(cmd)

	c, _, err := buildClientTimeout(0)
	if err != nil {
		return die(err)
	}

	var dep struct {
		Data map[string]any `json:"data"`
	}
	if err := c.API.Get(cmd.Context(), "/deployments/"+url.PathEscape(ref), nil, &dep); err != nil {
		return die(err)
	}
	id, _ := dep.Data["id"].(string)
	active, _ := dep.Data["active_version_id"].(string)
	if id == "" {
		return fmt.Errorf("the server returned no deployment for %q", ref)
	}

	var resp struct {
		Data []map[string]any `json:"data"`
	}
	if err := c.API.Get(cmd.Context(), "/deployments/"+url.PathEscape(id)+"/versions", nil, &resp); err != nil {
		return die(err)
	}

	if isJSON() {
		for _, v := range resp.Data {
			if vid, _ := v["id"].(string); vid != "" && vid == active {
				v["live"] = true
			}
		}
		return p.JSON(map[string]any{"data": resp.Data})
	}
	if len(resp.Data) == 0 {
		p.Line("No results.")
		return nil
	}

	heads := make([]string, len(deployVersionCols)+1)
	table := make([][]string, 0, len(resp.Data))
	for i, col := range deployVersionCols {
		heads[i] = col.Head
	}
	heads[len(deployVersionCols)] = "LIVE"

	for _, v := range resp.Data {
		row := make([]string, 0, len(heads))
		for _, col := range deployVersionCols {
			row = append(row, formatValue(lookupPath(v, col.Path), col.Fmt))
		}
		if vid, _ := v["id"].(string); vid != "" && vid == active {
			row = append(row, "live")
		} else {
			row = append(row, "")
		}
		table = append(table, row)
	}
	p.Table(heads, table)
	return nil
}

const deployDiffExample = `
Compare two of them with:

  miosa deploy diff <version> <version>`

// ─── diff ─────────────────────────────────────────────────────────────────────

// newDeployDiffCmd compares two versions of one deployment file by file.
func newDeployDiffCmd() *cobra.Command {
	var deployment, dir string

	cmd := &cobra.Command{
		Use:   "diff <version> <version>",
		Short: "Compare two versions file by file",
		Long: `Compare what two versions of a deployment ship.

  miosa deploy diff 4bacc5df 7093400e                       # in a linked folder
  miosa deploy diff 4bacc5df 7093400e --deployment my-site  # from anywhere

Each file the second version adds, changes or drops is listed with its size, so
a version that went out bigger than expected can be traced to the files that
did it. The deployment comes from --deployment, or from the .miosa/deployment.json
a first deploy wrote in the folder (--dir, default the current directory).`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDeployDiff(cmd, args[0], args[1], deployment, dir)
		},
	}
	cmd.Flags().StringVar(&deployment, "deployment", "", "The deployment both versions belong to (default: the folder link)")
	cmd.Flags().StringVar(&dir, "dir", "", "Folder whose .miosa/deployment.json names the deployment (default: the current directory)")
	return cmd
}

type manifestFile struct {
	Path string  `json:"path"`
	Sha  string  `json:"sha256"`
	Size float64 `json:"size"`
}

func runDeployDiff(cmd *cobra.Command, fromRef, toRef, deployment, dir string) error {
	p := printerFor(cmd)

	deployment, err := deploymentForVersions(deployment, dir)
	if err != nil {
		return err
	}
	c, _, err := buildClientTimeout(0)
	if err != nil {
		return die(err)
	}

	// Version refs may be a number, a short id or the id: resolve both against
	// the deployment's versions so the help's own short-id example works.
	var vs struct {
		Data []map[string]any `json:"data"`
	}
	if err := c.API.Get(cmd.Context(), "/deployments/"+url.PathEscape(deployment)+"/versions", nil, &vs); err != nil {
		return die(fmt.Errorf("deployment %s: %w", deployment, err))
	}
	for _, ref := range []*string{&fromRef, &toRef} {
		v, err := matchVersion(vs.Data, *ref)
		if err != nil {
			return usagef("deployment %s: version %v", deployment, err)
		}
		*ref, _ = v["id"].(string)
	}

	from, err := fetchVersionManifest(cmd, c, deployment, fromRef)
	if err != nil {
		return die(err)
	}
	to, err := fetchVersionManifest(cmd, c, deployment, toRef)
	if err != nil {
		return die(err)
	}

	if from == nil || to == nil {
		reportHashOnlyDiff(p, fromRef, toRef)
		return nil
	}

	added, removed, changed, unchanged := diffManifests(from, to)

	if isJSON() {
		return p.JSON(map[string]any{
			"from":       fromRef,
			"to":         toRef,
			"added":      added,
			"removed":    removed,
			"changed":    changed,
			"unchanged":  unchanged,
			"from_bytes": manifestBytes(from),
			"to_bytes":   manifestBytes(to),
		})
	}

	if len(added)+len(removed)+len(changed) == 0 {
		p.Success("%s and %s ship the same %d files", shortRef(fromRef), shortRef(toRef), len(from))
		return nil
	}
	for _, f := range added {
		p.Line("+ %s  %s", f.Path, humanBytes(f.Size))
	}
	for _, ch := range changed {
		p.Line("~ %s  %s -> %s", ch.Path, humanBytes(ch.FromSize), humanBytes(ch.ToSize))
	}
	for _, f := range removed {
		p.Line("- %s  %s", f.Path, humanBytes(f.Size))
	}
	p.Line("")
	p.Success("%d files: %d added, %d changed, %d removed, %d unchanged (%s -> %s)",
		len(added)+len(changed)+unchanged,
		len(added), len(changed), len(removed), unchanged,
		humanBytes(manifestBytes(from)), humanBytes(manifestBytes(to)))
	return nil
}

type changedFile struct {
	Path     string
	FromSize float64
	ToSize   float64
}

func diffManifests(from, to map[string]manifestFile) (added, removed []manifestFile, changed []changedFile, unchanged int) {
	for path, tf := range to {
		ff, ok := from[path]
		switch {
		case !ok:
			added = append(added, tf)
		case ff.Sha != tf.Sha:
			changed = append(changed, changedFile{Path: path, FromSize: ff.Size, ToSize: tf.Size})
		default:
			unchanged++
		}
	}
	for path, ff := range from {
		if _, ok := to[path]; !ok {
			removed = append(removed, ff)
		}
	}
	sort.Slice(added, func(i, j int) bool { return added[i].Path < added[j].Path })
	sort.Slice(removed, func(i, j int) bool { return removed[i].Path < removed[j].Path })
	sort.Slice(changed, func(i, j int) bool { return changed[i].Path < changed[j].Path })
	return added, removed, changed, unchanged
}

func manifestBytes(files map[string]manifestFile) float64 {
	var total float64
	for _, f := range files {
		total += f.Size
	}
	return total
}

// fetchVersionManifest returns a version's files by path. A version whose
// artifact carries no manifest (a dynamic deployment) returns nil, and the
// caller falls back to comparing hashes.
func fetchVersionManifest(cmd *cobra.Command, c *client.Client, deployment, versionRef string) (map[string]manifestFile, error) {
	var resp struct {
		Data struct {
			ID               string `json:"id"`
			Kind             string `json:"kind"`
			ArtifactSha256   string `json:"artifact_sha256"`
			ArtifactManifest *struct {
				Files []manifestFile `json:"files"`
			} `json:"artifact_manifest"`
		} `json:"data"`
	}
	path := fmt.Sprintf("/deployments/%s/versions/%s",
		url.PathEscape(deployment), url.PathEscape(versionRef))
	if err := c.API.Get(cmd.Context(), path, nil, &resp); err != nil {
		return nil, err
	}
	if resp.Data.ArtifactManifest == nil || len(resp.Data.ArtifactManifest.Files) == 0 {
		return nil, nil
	}
	out := make(map[string]manifestFile, len(resp.Data.ArtifactManifest.Files))
	for _, f := range resp.Data.ArtifactManifest.Files {
		out[f.Path] = f
	}
	return out, nil
}

func reportHashOnlyDiff(p *output.Printer, fromRef, toRef string) {
	p.Line("One of %s and %s carries no file manifest: a dynamic deployment ships an "+
		"image, not files. Compare them with 'miosa deploy versions' instead.",
		shortRef(fromRef), shortRef(toRef))
}

// deploymentForVersions resolves the deployment a version command works on:
// --deployment when given, else the folder link written by a first deploy.
func deploymentForVersions(deployment, dir string) (string, error) {
	if deployment != "" {
		return deployment, nil
	}
	root := dir
	if root == "" {
		root = "."
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return "", usagef("--dir %s: %v", root, err)
	}
	link, ok := readDeploymentLink(abs)
	if !ok {
		return "", usagef("no deployment to work on: pass --deployment <deployment>, "+
			"or run this in a folder already deployed (no %s there)", deploymentLinkPath(root))
	}
	return link.DeploymentID, nil
}
