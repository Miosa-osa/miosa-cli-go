package commands

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

// auditExportWindow is how far back an export reads when --since is not given.
// The organization audit stream keeps 90 days, so that is the useful default.
const auditExportWindow = 90 * 24 * time.Hour

// auditExportPageLimit is the server's page size ceiling.
const auditExportPageLimit = 100

type auditExportOptions struct {
	since        time.Duration
	until        time.Time
	eventType    string
	resourceType string
	resourceID   string
	workspaceID  string
}

func newAuditExportCmd() *cobra.Command {
	var (
		since, until            string
		eventType, resourceType string
		resourceID, workspaceID string
	)

	cmd := &cobra.Command{
		Use:   "export",
		Short: "Write the audit log as JSONL (90 days by default)",
		Long: `Write the organization's audit log to stdout as JSONL, one event per line,
exactly as the API returns it.

  miosa audit export > audit.jsonl
  miosa audit export --since 7d --resource-type sandbox > sandboxes.jsonl
  miosa audit export --since 2026-09-01 --until 2026-09-30 --type api_key.created

--since takes a duration (30m, 24h, 7d, 2w) or an RFC 3339 date; the default
is 90 days, which is how long the audit stream keeps events. Pages are read
newest first and the export stops as soon as an event is older than --since,
so a window costs only the pages it needs.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			opts := auditExportOptions{
				eventType:    eventType,
				resourceType: resourceType,
				resourceID:   resourceID,
				workspaceID:  workspaceID,
			}

			window, err := parseAuditStart(since)
			if err != nil {
				return usagef("--since: %v", err)
			}
			opts.since = window

			if strings.TrimSpace(until) != "" {
				end, err := parseAuditTime(until)
				if err != nil {
					return usagef("--until: %v", err)
				}
				opts.until = end
			}

			return runAuditExport(cmd, opts)
		},
	}

	cmd.Flags().StringVar(&since, "since", "90d", "How far back to read: a duration (7d) or an RFC 3339 date")
	cmd.Flags().StringVar(&until, "until", "", "Only events at or before this RFC 3339 date")
	cmd.Flags().StringVar(&eventType, "type", "", "Only this event type")
	cmd.Flags().StringVar(&resourceType, "resource-type", "", "Only events about this resource type")
	cmd.Flags().StringVar(&resourceID, "resource-id", "", "Only events about this resource id")
	cmd.Flags().StringVar(&workspaceID, "workspace-id", "", "Only this workspace")

	return cmd
}

func runAuditExport(cmd *cobra.Command, opts auditExportOptions) error {
	now := time.Now().UTC()
	since := now.Add(-opts.since)

	c, _, err := buildClient()
	if err != nil {
		return die(err)
	}

	written := 0
	cursor := ""

	for page := 0; page < 10_000; page++ {
		q := url.Values{}
		q.Set("limit", strconv.Itoa(auditExportPageLimit))
		if cursor != "" {
			q.Set("after", cursor)
		}
		if opts.eventType != "" {
			q.Set("type", opts.eventType)
		}
		if opts.resourceType != "" {
			q.Set("resource_type", opts.resourceType)
		}
		if opts.resourceID != "" {
			q.Set("resource_id", opts.resourceID)
		}
		if opts.workspaceID != "" {
			q.Set("workspace_id", opts.workspaceID)
		}

		var out struct {
			Data       []json.RawMessage `json:"data"`
			NextCursor string            `json:"next_cursor"`
		}
		if err := c.API.Get(cmd.Context(), "/audit-log", q, &out); err != nil {
			return die(fmt.Errorf("reading the audit log: %w", err))
		}

		reachedWindow := false
		for _, raw := range out.Data {
			var row struct {
				TS string `json:"ts"`
			}
			_ = json.Unmarshal(raw, &row)
			at, ok := parseAuditEventTime(row.TS)

			// Events are newest first: the first one older than the window
			// ends the export.
			if ok && at.Before(since) {
				reachedWindow = true
				break
			}
			if ok && !opts.until.IsZero() && at.After(opts.until) {
				continue
			}

			if _, err := fmt.Fprintln(cmd.OutOrStdout(), string(raw)); err != nil {
				return die(err)
			}
			written++
		}

		if reachedWindow || out.NextCursor == "" || len(out.Data) == 0 {
			break
		}
		cursor = out.NextCursor
	}

	fmt.Fprintf(cmd.ErrOrStderr(), "Exported %d event(s) since %s as JSONL.\n", written, since.Format(time.RFC3339))
	return nil
}

// parseAuditStart accepts a duration (with d and w, which time.ParseDuration
// does not) or an RFC 3339 timestamp.
func parseAuditStart(value string) (time.Duration, error) {
	if at, err := parseAuditTime(value); err == nil {
		return time.Since(at), nil
	}
	return parseAuditSpan(value)
}

// parseAuditSpan is parseAuditStart for a duration only.
func parseAuditSpan(value string) (time.Duration, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, fmt.Errorf("a duration like 7d or an RFC 3339 date is required")
	}

	if seconds, err := strconv.Atoi(value); err == nil {
		if seconds <= 0 {
			return 0, fmt.Errorf("%q is not a positive number of seconds", value)
		}
		return time.Duration(seconds) * time.Second, nil
	}

	unit := value[len(value)-1]
	if unit == 'd' || unit == 'w' {
		amount, err := strconv.Atoi(value[:len(value)-1])
		if err != nil || amount <= 0 {
			return 0, fmt.Errorf("%q is not a positive number of days or weeks", value)
		}
		if unit == 'd' {
			return time.Duration(amount) * 24 * time.Hour, nil
		}
		return time.Duration(amount) * 7 * 24 * time.Hour, nil
	}

	span, err := time.ParseDuration(value)
	if err != nil || span <= 0 {
		return 0, fmt.Errorf("%q is not a duration like 30m, 24h, 7d or an RFC 3339 date", value)
	}
	return span, nil
}

func parseAuditTime(value string) (time.Time, error) {
	value = strings.TrimSpace(value)
	if at, err := time.Parse(time.RFC3339, value); err == nil {
		return at, nil
	}
	// A bare date is midnight UTC, which is what a reader means by it.
	if at, err := time.Parse("2006-01-02", value); err == nil {
		return at, nil
	}
	return time.Time{}, fmt.Errorf("%q is not an RFC 3339 date", value)
}

// parseAuditEventTime reads the server's timestamp; a value it cannot read is
// not a reason to drop the event.
func parseAuditEventTime(value string) (time.Time, bool) {
	if value == "" {
		return time.Time{}, false
	}
	at, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}, false
	}
	return at, true
}
