package commands

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/Miosa-osa/miosa-cli-go/internal/api"
)

// A link is the directory's answer to "which project, Deploy target and
// service am I working on", written once by `miosa link` and read by every
// command that takes a project or a service. It holds names and ids, never a
// key.
const linkFileName = "link.json"

type linkFile struct {
	Version       int    `json:"version"`
	Project       string `json:"project"`
	ProjectID     string `json:"project_id"`
	Environment   string `json:"environment,omitempty"`
	EnvironmentID string `json:"environment_id,omitempty"`
	Service       string `json:"service,omitempty"`
	ServiceID     string `json:"service_id,omitempty"`
	LinkedAt      string `json:"linked_at"`
}

// linkDir follows RAILWAY-style discovery: MIOSA_LINK points at the directory
// holding the link, else every directory from the current one up to the root
// is checked, then the home directory.
func linkDir() string {
	if dir := strings.TrimSpace(os.Getenv("MIOSA_LINK")); dir != "" {
		return dir
	}
	if wd, err := os.Getwd(); err == nil {
		for dir := wd; ; dir = filepath.Dir(dir) {
			if _, err := os.Stat(filepath.Join(dir, ".miosa", linkFileName)); err == nil {
				return dir
			}
			parent := filepath.Dir(dir)
			if parent == dir {
				break
			}
		}
	}
	if home, err := os.UserHomeDir(); err == nil {
		if _, err := os.Stat(filepath.Join(home, ".miosa", linkFileName)); err == nil {
			return home
		}
	}
	return ""
}

func writeLinkFile(link linkFile) (string, error) {
	link.Version = 1
	link.LinkedAt = time.Now().UTC().Format(time.RFC3339)
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	// An existing link (found upwards, or from MIOSA_LINK) is updated in place so
	// `miosa service select` in a subdirectory rewrites the repository's link.
	if found := linkDir(); found != "" {
		dir = found
	}
	path := filepath.Join(dir, ".miosa")
	if err := os.MkdirAll(path, 0o755); err != nil {
		return "", err
	}
	body, err := json.MarshalIndent(link, "", "  ")
	if err != nil {
		return "", err
	}
	file := filepath.Join(path, linkFileName)
	return file, os.WriteFile(file, append(body, '\n'), 0o644)
}

// readLink returns the link for the current directory, or an error that says how
// to make one.
func readLink() (linkFile, error) {
	dir := linkDir()
	if dir == "" {
		return linkFile{}, fmt.Errorf("this directory is not linked to a project; run `miosa link <project>` (or pass the project and service)")
	}
	body, err := os.ReadFile(filepath.Join(dir, ".miosa", linkFileName))
	if err != nil {
		return linkFile{}, err
	}
	var link linkFile
	if err := json.Unmarshal(body, &link); err != nil {
		return linkFile{}, fmt.Errorf("%s is not a link file: %w", filepath.Join(dir, ".miosa", linkFileName), err)
	}
	return link, nil
}

func newLinkCmd() *cobra.Command {
	var environment, serviceName string
	cmd := &cobra.Command{
		Use:   "link <project>",
		Short: "Attach this directory to a project, Deploy target and service",
		Long: `Write the project, Deploy target and service to .miosa/link.json so later
commands need no arguments.

  miosa link cliniciq
  miosa link cliniciq --environment staging --service API
  miosa status            # the linked project and service
  miosa unlink

The link holds names and ids, never a key.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			p := printerFor(cmd)
			c, _, err := buildClient()
			if err != nil {
				return die(err)
			}
			ctx := cmd.Context()
			projectID, err := resolveServiceProject(ctx, c.API, args[0])
			if err != nil {
				return err
			}
			link := linkFile{Project: args[0], ProjectID: projectID}

			if environment != "" {
				env, err := resolveServiceEnvironment(ctx, c.API, projectID, environment)
				if err != nil {
					return err
				}
				link.Environment, link.EnvironmentID = env.Name, env.ID
			} else if envs, err := listServiceEnvironments(ctx, c.API, projectID); err == nil {
				for _, env := range envs {
					if env.IsDefault {
						link.Environment, link.EnvironmentID = env.Name, env.ID
					}
				}
			}

			if serviceName != "" {
				services, err := listServices(ctx, c.API, projectID, link.Environment)
				if err != nil {
					return err
				}
				for _, s := range services {
					if strings.EqualFold(s.Name, serviceName) || strings.EqualFold(s.Slug, serviceName) || strings.EqualFold(s.ID, serviceName) {
						link.Service, link.ServiceID = s.Name, s.ID
					}
				}
				if link.Service == "" {
					return usagef("no service named %q in project %q", serviceName, args[0])
				}
			}

			file, err := writeLinkFile(link)
			if err != nil {
				return die(err)
			}
			if isJSON() {
				return p.JSON(link)
			}
			p.Success("Linked this directory to %s (%s)", link.Project, shortID(link.ProjectID))
			if link.Environment != "" {
				p.Line("  Deploy target: %s", link.Environment)
			}
			if link.Service != "" {
				p.Line("  Service:       %s", link.Service)
			}
			p.Line("  Written to %s", file)
			return nil
		},
	}
	fs := cmd.Flags()
	fs.StringVar(&environment, "environment", "", "Deploy target (default: the project's default)")
	fs.StringVar(&environment, "deploy-target", "", "Alias of --environment")
	fs.StringVar(&serviceName, "service", "", "Service to link")
	return cmd
}

func newUnlinkCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "unlink",
		Short: "Detach this directory from its project",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			p := printerFor(cmd)
			dir := linkDir()
			if dir == "" {
				return usagef("this directory is not linked")
			}
			file := filepath.Join(dir, ".miosa", linkFileName)
			if err := os.Remove(file); err != nil {
				return die(err)
			}
			p.Success("Unlinked (%s removed)", file)
			return nil
		},
	}
}

func printLinkedStatus(ctx context.Context, c *api.Client, link linkFile, p printerLike) error {
	project, err := getProject(ctx, c, link.ProjectID)
	if err != nil {
		return err
	}
	p.Line("Project  %s (%s)", project.Name, shortID(project.ID))
	if link.Environment != "" {
		p.Line("Target   %s", link.Environment)
	}
	if link.ServiceID == "" {
		return nil
	}
	svc, err := getService(ctx, c, link.ProjectID, link.ServiceID, link.Environment)
	if err != nil {
		return err
	}
	p.Line("Service  %s (%s, %s)", svc.Name, svc.Kind, shortID(svc.ID))
	if svc.PublicAddress != nil && svc.PublicAddress.URL != nil {
		p.Line("Address  %s", *svc.PublicAddress.URL)
	}
	releases, err := listReleases(ctx, c, link.ProjectID, svc.ID)
	if err == nil && len(releases) > 0 {
		r := releases[0]
		p.Line("Release  %d %s (%s)", r.Number, r.State, shortID(r.ID))
		if r.Reason != nil {
			p.Line("Reason   %s %s", r.Reason.Code, r.Reason.Detail)
		}
	}
	return nil
}

// getProject reads one project by id.
func getProject(ctx context.Context, c *api.Client, projectID string) (serviceProject, error) {
	var out struct {
		Data serviceProject `json:"data"`
	}
	if err := c.Get(ctx, "/projects/"+projectID, nil, &out); err != nil {
		return serviceProject{}, die(err)
	}
	return out.Data, nil
}

// getService reads one service by id.
func getService(ctx context.Context, c *api.Client, projectID, serviceID, env string) (projectServiceRow, error) {
	services, err := listServices(ctx, c, projectID, env)
	if err != nil {
		return projectServiceRow{}, err
	}
	for _, s := range services {
		if s.ID == serviceID {
			return s, nil
		}
	}
	return projectServiceRow{}, usagef("the linked service no longer exists; run `miosa link` again")
}

// linkedServiceLogs is `miosa logs` in a linked directory. The build log exists
// today; the runtime route is not live yet, so it falls back to the release
// events with a line that says which one answered.
func linkedServiceLogs(cmd *cobra.Command, c *api.Client, link linkFile, build, follow bool, lines int) error {
	if link.ServiceID == "" {
		return usagef("the link names no service; run `miosa link <project> --service <name>`")
	}
	if lines <= 0 {
		lines = 100
	}
	if build {
		return serviceBuildLogs(cmd.Context(), cmd, c, link.ProjectID, &projectServiceRow{ID: link.ServiceID, Name: link.Service}, "latest", lines, follow)
	}

	p := printerFor(cmd)
	var out struct {
		Data struct {
			Lines []string `json:"lines"`
		} `json:"data"`
	}
	q := url.Values{"tail": {fmt.Sprint(lines)}}
	err := c.Get(cmd.Context(), servicePath(link.ProjectID, link.ServiceID)+"/logs", q, &out)
	if err == nil {
		for _, line := range out.Data.Lines {
			p.Line("%s", line)
		}
		return nil
	}
	var ae *api.Error
	if !errors.As(err, &ae) || ae.Status != 404 {
		return die(err)
	}

	p.Line("Runtime logs are not live yet; the service's release events follow (--build shows the build log).")
	release, rerr := latestOrNamedRelease(cmd.Context(), c, link.ProjectID, link.ServiceID, "")
	if rerr != nil {
		return rerr
	}
	events, eerr := serviceEvents(cmd.Context(), c, link.ProjectID, link.ServiceID, release.ID)
	if eerr != nil {
		return eerr
	}
	p.Line("Release %d (%s) %s", release.Number, release.State, release.ID)
	for _, e := range events {
		from := "-"
		if e.FromState != nil {
			from = *e.FromState
		}
		p.Line("  %s  %s -> %s", e.At, from, e.ToState)
	}
	return nil
}
