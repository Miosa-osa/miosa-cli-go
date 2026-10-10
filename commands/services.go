package commands

import (
	"fmt"
	"net/url"

	"github.com/spf13/cobra"

	"github.com/Miosa-osa/miosa-cli-go/internal/api"
)

// A sandbox "service" is a named, persistent process: it keeps its command id
// and logs after the CLI disconnects. start/stop/delete are built from the
// process routes since the service routes only create, list, show, restart and
// read logs.

var serviceCols = []Col{
	{Head: "NAME", Path: "name"}, {Head: "STATUS", Path: "status"}, {Head: "PID", Path: "pid"},
	{Head: "COMMAND", Path: "command"}, {Head: "ID", Path: "id", Fmt: "short"},
}

func newServicesCmd() *cobra.Command {
	sandbox := Arg{Name: "sandbox", Optional: true, Current: true, Complete: "sandbox"}
	cmd := group("services", "Run named long-lived services in a sandbox",
		`A service is a named background process that outlives your session: its status and
logs stay available after the CLI disconnects. The sandbox is optional everywhere
(the current one is used).

  miosa services create my-box --name web --command "python -m http.server 8080"
  miosa services list
  miosa services logs web --tail 50
  miosa services restart web`, []string{"service", "svc"},
		Op{
			Use: "list [sandbox]", Aliases: []string{"ls"}, Short: "List services",
			Method: "GET", Path: "/sandboxes/{0}/services", Args: []Arg{sandbox}, Cols: serviceCols,
		},
		Op{
			Use: "create [sandbox]", Short: "Start a service",
			Method: "POST", Path: "/sandboxes/{0}/services", Args: []Arg{sandbox},
			Flags: []Flag{
				{Name: "name", Usage: "Service name", Required: true},
				{Name: "command", Usage: "Command to run", Required: true},
				{Name: "cwd", Usage: "Working directory (default /workspace)"},
				{Name: "env", Usage: "Environment variable KEY=VALUE (repeatable)", Type: "kv"},
				{Name: "sudo", Usage: "Run as root", Type: "bool"},
			},
			Detail: serviceDetail, Done: "Started service",
		},
		Op{
			Use: "get [sandbox] <service>", Aliases: []string{"show"}, Short: "Show one service",
			Method: "GET", Path: "/sandboxes/{0}/services/{1}", Args: []Arg{sandbox, {Name: "service"}}, Detail: serviceDetail,
		},
		Op{
			Use: "restart [sandbox] <service>", Aliases: []string{"start"}, Short: "Restart a service (also starts a stopped one)",
			Method: "POST", Path: "/sandboxes/{0}/services/{1}/restart", Args: []Arg{sandbox, {Name: "service"}}, Detail: serviceDetail, Done: "Restarted {1}",
		},
		Op{
			Use: "logs [sandbox] <service>", Short: "Show a service's output",
			Method: "GET", Path: "/sandboxes/{0}/services/{1}/logs", Args: []Arg{sandbox, {Name: "service"}},
			Flags: []Flag{{Name: "tail", Short: "n", Type: "int", Usage: "Number of recent lines", Default: "100"}},
			Lines: "data.lines",
		},
	)
	cmd.AddCommand(newServicesStopCmd("stop", "Stop a service", []string{"delete", "rm"}))
	return cmd
}

var serviceDetail = []Col{
	{Head: "Name", Path: "name"}, {Head: "Status", Path: "status"}, {Head: "PID", Path: "pid"}, {Head: "Command", Path: "command"},
	{Head: "Directory", Path: "cwd"}, {Head: "Exit code", Path: "exit_code"}, {Head: "Started", Path: "started_at", Fmt: "age"}, {Head: "Id", Path: "id"},
}

func newServicesStopCmd(use, short string, aliases []string) *cobra.Command {
	return &cobra.Command{
		Use:     use + " [sandbox] <service>",
		Aliases: aliases,
		Short:   short,
		Long:    "Stop a service by killing its process. Start it again with 'services restart'.",
		Args:    cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			p := printerFor(cmd)
			ref, name := "current", args[0]
			if len(args) == 2 {
				ref, name = args[0], args[1]
			}
			c, _, err := buildClient()
			if err != nil {
				return die(err)
			}
			base := "/sandboxes/" + url.PathEscape(ref)
			var svc struct {
				Data struct {
					ID string `json:"id"`
				} `json:"data"`
			}
			if err := c.API.Get(cmd.Context(), base+"/services/"+url.PathEscape(name), nil, &svc); err != nil {
				return die(err)
			}
			if svc.Data.ID == "" {
				return die(&api.Error{Status: 404, Code: "NOT_FOUND", Message: fmt.Sprintf("no running service named %q", name)})
			}
			if err := c.API.Delete(cmd.Context(), base+"/processes/"+svc.Data.ID, nil, nil); err != nil {
				return die(err)
			}
			if isJSON() {
				return p.JSON(map[string]string{"service": name, "status": "stopped"})
			}
			p.Success("Stopped service %q", name)
			return nil
		},
	}
}
