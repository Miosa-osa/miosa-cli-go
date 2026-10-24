package commands

import (
	"fmt"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Miosa-osa/miosa-cli-go/internal/api"
)

func init() {
	rootCmd.AddCommand(newTelemetryCmd())
}

// telemetryPath is the organization's OTLP export (GET|PUT|DELETE).
const telemetryPath = "/settings/telemetry"

type telemetryDoc struct {
	Configured          bool     `json:"configured"`
	Endpoint            string   `json:"endpoint"`
	HeaderNames         []string `json:"header_names"`
	LastExportAt        string   `json:"last_export_at"`
	LastStatus          string   `json:"last_status"`
	LastError           string   `json:"last_error"`
	ConsecutiveFailures int      `json:"consecutive_failures"`
	Removed             bool     `json:"removed"`
	Test                *struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
	} `json:"test"`
}

func newTelemetryCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "telemetry",
		Short: "Export sandbox and account metrics and lifecycle events over OTLP",
		Long: `Send the organization's telemetry to an OpenTelemetry endpoint (OTLP/HTTP):
every minute the sandbox and account metrics (sandboxes by state, each running
sandbox, the credit balance), and every lifecycle event (create, pause, resume,
destroy, exec) since the last export, as log records.

With no subcommand it shows the export and how its last run went.

  miosa telemetry
  miosa telemetry set https://otlp.datadoghq.com -H dd-api-key=$DD_API_KEY
  miosa telemetry set https://api.honeycomb.io -H x-honeycomb-team=$HONEYCOMB_KEY
  miosa telemetry unset

The URL is the OTLP base: /v1/metrics and /v1/logs are added to it. Header
values are stored encrypted and never shown again, only their names. Setting
an export replaces the current one and sends a test export at once. Changing
it needs an owner or administrator.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, _, err := buildClient()
			if err != nil {
				return die(err)
			}
			var out struct {
				Data telemetryDoc `json:"data"`
			}
			if err := c.API.Get(cmd.Context(), telemetryPath, nil, &out); err != nil {
				return die(err)
			}
			return printTelemetry(cmd, out.Data)
		},
	}
	cmd.AddCommand(newTelemetrySetCmd(), newTelemetryUnsetCmd())
	return cmd
}

func newTelemetrySetCmd() *cobra.Command {
	var headers []string
	cmd := &cobra.Command{
		Use:   "set <url>",
		Short: "Replace the export and send a test export now",
		Long: `Replace the export with <url> (https, the OTLP base) and the given request
headers, then send a test export at once. The export is saved either way; when
the endpoint refuses the test, the command says what it answered and exits 1.

  miosa telemetry set https://otlp.datadoghq.com -H dd-api-key=$DD_API_KEY
  miosa telemetry set https://otel.example.com:4318 -H authorization="Bearer $TOKEN"`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if !strings.HasPrefix(args[0], "https://") {
				return usagef("the endpoint must be an https:// URL")
			}
			hdrs := map[string]string{}
			for _, h := range headers {
				k, v, ok := strings.Cut(h, "=")
				if !ok || strings.TrimSpace(k) == "" {
					return usagef("-H takes name=value (got %q)", h)
				}
				hdrs[strings.TrimSpace(k)] = v
			}
			c, _, err := buildClient()
			if err != nil {
				return die(err)
			}
			var out struct {
				Data telemetryDoc `json:"data"`
			}
			body := map[string]any{"endpoint": args[0], "headers": hdrs}
			if err := c.API.JSON(cmd.Context(), api.Request{Method: "PUT", Path: telemetryPath, Body: body}, &out); err != nil {
				return die(err)
			}
			if err := printTelemetry(cmd, out.Data); err != nil {
				return err
			}
			if out.Data.Test != nil && !out.Data.Test.OK {
				return die(fmt.Errorf("the export is saved, but the test export failed: %s", out.Data.Test.Error))
			}
			return nil
		},
	}
	cmd.Flags().StringArrayVarP(&headers, "header", "H", nil, "Request header sent with every export, name=value (repeatable)")
	return cmd
}

func newTelemetryUnsetCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "unset",
		Aliases: []string{"rm"},
		Short:   "Remove the export",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, _, err := buildClient()
			if err != nil {
				return die(err)
			}
			var out struct {
				Data telemetryDoc `json:"data"`
			}
			if err := c.API.JSON(cmd.Context(), api.Request{Method: "DELETE", Path: telemetryPath}, &out); err != nil {
				return die(err)
			}
			if isJSON() {
				return printerFor(cmd).JSON(out)
			}
			if out.Data.Removed {
				printerFor(cmd).Success("Telemetry export removed.")
			} else {
				printerFor(cmd).Line("No telemetry export was set.")
			}
			return nil
		},
	}
}

func printTelemetry(cmd *cobra.Command, d telemetryDoc) error {
	p := printerFor(cmd)
	if isJSON() {
		return p.JSON(map[string]any{"data": d})
	}
	if !d.Configured {
		p.Line("No telemetry export. Set one with: miosa telemetry set <url> -H name=value")
		return nil
	}
	names := append([]string(nil), d.HeaderNames...)
	sort.Strings(names)
	rows := [][2]string{
		{"Endpoint", d.Endpoint},
		{"Headers", orDash(strings.Join(names, ", "))},
		{"Last export", orDash(d.LastExportAt)},
		{"Last result", telemetryResult(d)},
	}
	if d.Test != nil {
		rows = append(rows, [2]string{"Test export", map[bool]string{true: "ok", false: "failed: " + d.Test.Error}[d.Test.OK]})
	}
	p.Fields(rows)
	return nil
}

func telemetryResult(d telemetryDoc) string {
	switch d.LastStatus {
	case "":
		return "-"
	case "ok":
		return "ok"
	}
	msg := d.LastStatus
	if d.LastError != "" {
		msg += ": " + d.LastError
	}
	if d.ConsecutiveFailures > 1 {
		msg += fmt.Sprintf(" (%d in a row)", d.ConsecutiveFailures)
	}
	return msg
}
