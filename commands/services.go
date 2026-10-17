package commands

import (
	"fmt"
	"net/url"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Miosa-osa/miosa-cli-go/internal/api"
)

// A sandbox daemon is a named, persistent process: it keeps its command id and
// logs after the CLI disconnects. start/stop/delete are built from the process
// routes since the daemon routes (/sandboxes/:id/services) only create, list,
// show, restart and read logs.
//
// It was called `miosa services` (also service, svc) until 2.3.13. That name is
// moving to project Services (tasks/specs/cli-noun-plan-services-2026-10-16.md):
// 2.3.13 keeps the old names working with a stderr notice, the next release
// refuses them with the rewritten command, and the one after gives `service` to
// project Services.

var serviceCols = []Col{
	{Head: "NAME", Path: "name"}, {Head: "STATUS", Path: "status"}, {Head: "PID", Path: "pid"},
	{Head: "COMMAND", Path: "command"}, {Head: "ID", Path: "id", Fmt: "short"},
}

// noDeprecationNoticeEnv silences the notice for CI that fails on stderr.
const noDeprecationNoticeEnv = "MIOSA_NO_DEPRECATION_NOTICE"

func newDaemonCmd() *cobra.Command {
	return daemonGroup("daemon", "daemon", nil, `A daemon is a named background process in a sandbox that outlives your session: its
status and logs stay available after the CLI disconnects. The sandbox is optional
everywhere (the current one is used).

  miosa daemon create my-box --name web --command "python -m http.server 8080"
  miosa daemon list
  miosa daemon logs web --tail 50
  miosa daemon restart web`)
}

// newServicesCmd is the pre-2.3.13 name: hidden, same requests and output, and
// a stderr notice naming the command to use instead.
func newServicesCmd() *cobra.Command {
	cmd := daemonGroup("services", "service", []string{"service", "svc"}, `Renamed to 'miosa daemon'. This name keeps working in this release, with a notice
on standard error, and then stops working. Set MIOSA_NO_DEPRECATION_NOTICE=1 to
silence the notice.`)
	cmd.Hidden = true
	cmd.Short = "Renamed to 'miosa daemon'"
	warnRenamed(cmd)
	return cmd
}

// daemonGroup builds the command under `use`; noun is the word its messages use.
func daemonGroup(use, noun string, aliases []string, long string) *cobra.Command {
	sandbox := Arg{Name: "sandbox", Optional: true, Current: true, Complete: "sandbox"}
	item := Arg{Name: noun}
	cmd := group(use, "Run named long-lived processes (daemons) in a sandbox", long, aliases,
		Op{
			Use: "list [sandbox]", Aliases: []string{"ls"}, Short: "List " + noun + "s",
			Method: "GET", Path: "/sandboxes/{0}/services", Args: []Arg{sandbox}, Cols: serviceCols,
		},
		Op{
			Use: "create [sandbox]", Short: "Start a " + noun,
			Method: "POST", Path: "/sandboxes/{0}/services", Args: []Arg{sandbox},
			Flags: []Flag{
				{Name: "name", Usage: strings.ToUpper(noun[:1]) + noun[1:] + " name", Required: true},
				{Name: "command", Usage: "Command to run", Required: true},
				{Name: "cwd", Usage: "Working directory (default /workspace)"},
				{Name: "env", Usage: "Environment variable KEY=VALUE (repeatable)", Type: "kv"},
				{Name: "sudo", Usage: "Run as root", Type: "bool"},
			},
			Detail: serviceDetail, Done: "Started " + noun,
		},
		Op{
			Use: "get [sandbox] <" + noun + ">", Aliases: []string{"show"}, Short: "Show one " + noun,
			Method: "GET", Path: "/sandboxes/{0}/services/{1}", Args: []Arg{sandbox, item}, Detail: serviceDetail,
		},
		Op{
			Use: "restart [sandbox] <" + noun + ">", Aliases: []string{"start"}, Short: "Restart a " + noun + " (also starts a stopped one)",
			Method: "POST", Path: "/sandboxes/{0}/services/{1}/restart", Args: []Arg{sandbox, item}, Detail: serviceDetail, Done: "Restarted {1}",
		},
		Op{
			Use: "logs [sandbox] <" + noun + ">", Short: "Show a " + noun + "'s output",
			Method: "GET", Path: "/sandboxes/{0}/services/{1}/logs", Args: []Arg{sandbox, item},
			Flags: []Flag{{Name: "tail", Short: "n", Type: "int", Usage: "Number of recent lines", Default: "100"}},
			Lines: "data.lines",
		},
	)
	cmd.AddCommand(newServicesStopCmd(use, noun, "stop", "Stop a "+noun, []string{"delete", "rm"}))
	return cmd
}

// warnRenamed prints, before every subcommand of a renamed group runs, the
// command to use instead. Standard error only: stdout and --json are unchanged.
func warnRenamed(group *cobra.Command) {
	for _, sub := range group.Commands() {
		run := sub.RunE
		if run == nil {
			continue
		}
		sub.RunE = func(cmd *cobra.Command, args []string) error {
			if os.Getenv(noDeprecationNoticeEnv) == "" {
				fmt.Fprintf(cmd.ErrOrStderr(),
					"miosa: 'miosa %s' is now 'miosa daemon'; use 'miosa daemon %s'. The old name stops working in the next release.\n",
					group.Name(), strings.Join(append([]string{cmd.Name()}, args...), " "))
			}
			return run(cmd, args)
		}
	}
}

var serviceDetail = []Col{
	{Head: "Name", Path: "name"}, {Head: "Status", Path: "status"}, {Head: "PID", Path: "pid"}, {Head: "Command", Path: "command"},
	{Head: "Directory", Path: "cwd"}, {Head: "Exit code", Path: "exit_code"}, {Head: "Started", Path: "started_at", Fmt: "age"}, {Head: "Id", Path: "id"},
}

func newServicesStopCmd(group, noun, use, short string, aliases []string) *cobra.Command {
	return &cobra.Command{
		Use:     use + " [sandbox] <" + noun + ">",
		Aliases: aliases,
		Short:   short,
		Long:    "Stop a " + noun + " by killing its process. Start it again with '" + group + " restart'.",
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
				return die(&api.Error{Status: 404, Code: "NOT_FOUND", Message: fmt.Sprintf("no running %s named %q", noun, name)})
			}
			if err := c.API.Delete(cmd.Context(), base+"/processes/"+svc.Data.ID, nil, nil); err != nil {
				return die(err)
			}
			if isJSON() {
				return p.JSON(map[string]string{"service": name, "status": "stopped"})
			}
			p.Success("Stopped %s %q", noun, name)
			return nil
		},
	}
}
