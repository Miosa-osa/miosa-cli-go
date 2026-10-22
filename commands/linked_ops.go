package commands

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/Miosa-osa/miosa-cli-go/internal/api"
)

// newUpCmd is `miosa up`: deploy the current directory to the linked service.
func newUpCmd() *cobra.Command {
	var (
		dir      string
		message  string
		noWait   bool
		timeout  time.Duration
		withLogs bool
	)
	cmd := &cobra.Command{
		Use:   "up [dir]",
		Short: "Deploy this directory to the linked service",
		Long: `Upload the directory (the current one by default) as the linked service's source,
queue a release, and wait for it. Then print the build's log.

  miosa link cliniciq --service API
  miosa up
  miosa up ./dist --message "hotfix" --no-wait

Needs a linked directory: ` + "`miosa link <project> --service <name>`" + `.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			p := printerFor(cmd)
			if len(args) > 0 {
				dir = args[0]
			}
			if dir == "" {
				dir = "."
			}
			if info, err := os.Stat(dir); err != nil || !info.IsDir() {
				return usagef("%s is not a directory", dir)
			}
			c, _, err := buildClient()
			if err != nil {
				return die(err)
			}
			link, svc, err := linkedService(cmd.Context(), c.API)
			if err != nil {
				return err
			}
			ctx := cmd.Context()
			source, err := uploadServiceSource(ctx, c.API, svc.Name, dir)
			if err != nil {
				return err
			}
			body := map[string]any{"source": source}
			if message != "" {
				body["message"] = message
			}
			release, err := queueRelease(ctx, c.API, link.ProjectID, svc.ID, body)
			if err != nil {
				return err
			}
			p.Success("Uploaded %s; release %d queued (%s)", dir, release.Number, shortID(release.ID))
			if !noWait {
				release, err = waitForRelease(ctx, c.API, link.ProjectID, svc.ID, release.ID, timeout, cmd)
				if err != nil {
					return err
				}
				if renderErr := renderServiceRelease(p, release); renderErr != nil {
					return renderErr
				}
			}
			if withLogs {
				p.Line("")
				if err := serviceBuildLogs(cmd.Context(), cmd, c.API, link.ProjectID, &projectServiceRow{ID: link.ServiceID, Name: link.Service}, "latest", 200, false); err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "note: the build log was not available: %v\n", err)
				}
			}
			return nil
		},
	}
	fs := cmd.Flags()
	fs.StringVar(&message, "message", "", "Release message")
	fs.BoolVar(&noWait, "no-wait", false, "Queue the release and return")
	fs.DurationVar(&timeout, "timeout", 10*time.Minute, "How long to wait for the release")
	fs.BoolVar(&withLogs, "logs", true, "Print the build's log after the release")
	return cmd
}

// newRedeployCmd is `miosa redeploy`: rebuild the linked service from its own source.
func newRedeployCmd() *cobra.Command {
	var noWait bool
	var timeout time.Duration
	cmd := &cobra.Command{
		Use:   "redeploy",
		Short: "Build and release the linked service again",
		Long: `Queue a release from the linked service's own source and wait for it.

  miosa link cliniciq --service API
  miosa redeploy`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			p := printerFor(cmd)
			c, _, err := buildClient()
			if err != nil {
				return die(err)
			}
			link, svc, err := linkedService(cmd.Context(), c.API)
			if err != nil {
				return err
			}
			release, err := queueRelease(cmd.Context(), c.API, link.ProjectID, svc.ID, map[string]any{})
			if err != nil {
				return err
			}
			p.Success("Release %d queued (%s)", release.Number, shortID(release.ID))
			if noWait {
				return nil
			}
			release, err = waitForRelease(cmd.Context(), c.API, link.ProjectID, svc.ID, release.ID, timeout, cmd)
			if err != nil {
				return err
			}
			return renderServiceRelease(p, release)
		},
	}
	cmd.Flags().BoolVar(&noWait, "no-wait", false, "Queue the release and return")
	cmd.Flags().DurationVar(&timeout, "timeout", 10*time.Minute, "How long to wait for the release")
	return cmd
}

// newRollbackCmd is `miosa rollback`: serve the release before the active one.
func newRollbackCmd() *cobra.Command {
	var to string
	var noWait bool
	var timeout time.Duration
	cmd := &cobra.Command{
		Use:   "rollback",
		Short: "Serve the linked service's previous release",
		Long: `Queue a release that serves an earlier build with its frozen source and settings.
The previous release is the newest one that is not the active one; --to names one
(number, unique id prefix or id).

  miosa rollback
  miosa rollback --to 42`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			p := printerFor(cmd)
			c, _, err := buildClient()
			if err != nil {
				return die(err)
			}
			link, svc, err := linkedService(cmd.Context(), c.API)
			if err != nil {
				return err
			}
			target, err := rollbackTarget(cmd.Context(), c.API, link.ProjectID, svc.ID, to)
			if err != nil {
				return err
			}
			var queued struct {
				Data serviceRelease `json:"data"`
			}
			path := "/service-deployments/" + url.PathEscape(target.ID) + "/rollback"
			if err := c.API.Post(cmd.Context(), path, map[string]any{}, &queued); err != nil {
				return die(err)
			}
			release := queued.Data
			p.Success("Rolled back to release %d (%s)", target.Number, shortID(target.ID))
			if noWait {
				return nil
			}
			release, err = waitForRelease(cmd.Context(), c.API, link.ProjectID, svc.ID, release.ID, timeout, cmd)
			if err != nil {
				return err
			}
			return renderServiceRelease(p, release)
		},
	}
	cmd.Flags().StringVar(&to, "to", "", "Release number, id or unique id prefix (default: the previous one)")
	cmd.Flags().BoolVar(&noWait, "no-wait", false, "Queue the rollback and return")
	cmd.Flags().DurationVar(&timeout, "timeout", 10*time.Minute, "How long to wait for the release")
	return cmd
}

// linkedService resolves the link and the service row it names.
func linkedService(ctx context.Context, c *api.Client) (linkFile, projectServiceRow, error) {
	link, err := readLink()
	if err != nil {
		return linkFile{}, projectServiceRow{}, usagef("%v", err)
	}
	svc, err := getService(ctx, c, link.ProjectID, link.ServiceID, link.Environment)
	if err != nil {
		return linkFile{}, projectServiceRow{}, err
	}
	return link, svc, nil
}

func queueRelease(ctx context.Context, c *api.Client, projectID, serviceID string, body map[string]any) (serviceRelease, error) {
	var queued struct {
		Data serviceRelease `json:"data"`
	}
	if err := c.Post(ctx, servicePath(projectID, serviceID)+"/deployments", body, &queued); err != nil {
		return serviceRelease{}, die(err)
	}
	return queued.Data, nil
}

// rollbackTarget picks what `--to` names, or the newest release that is not the
// active one.
func rollbackTarget(ctx context.Context, c *api.Client, projectID, serviceID, ref string) (serviceRelease, error) {
	if strings.TrimSpace(ref) != "" {
		release, err := latestOrNamedRelease(ctx, c, projectID, serviceID, ref)
		if err != nil {
			return serviceRelease{}, err
		}
		return *release, nil
	}
	releases, err := listReleases(ctx, c, projectID, serviceID)
	if err != nil {
		return serviceRelease{}, err
	}
	var active string
	for _, r := range releases {
		if r.State == "active" {
			active = r.ID
			break
		}
	}
	for _, r := range releases {
		if r.ID == active || r.State == "stopped" || r.State == "removed" {
			continue
		}
		return r, nil
	}
	return serviceRelease{}, usagef("this service has no earlier release to serve")
}
