package commands

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Miosa-osa/miosa-cli-go/internal/api"
)

func init() {
	rootCmd.AddCommand(previewCmd(), processCmd(), desktopCmd(),
		sandboxVerb(Op{
			Use: "ps [sandbox]", Short: "List background processes (same as 'process list')",
			Method: "GET", Path: "/sandboxes/{0}/processes", Cols: processCols,
		}))
}

// sbx is the optional leading sandbox argument (default: the current one).
var sbx = Arg{Name: "sandbox", Optional: true, Current: true, Complete: "sandbox"}

// ─── preview ──────────────────────────────────────────────────────────────────

var previewCols = []Col{
	{Head: "ID", Path: "id", Fmt: "short"}, {Head: "NAME", Path: "name"}, {Head: "PORT", Path: "port"},
	{Head: "STATE", Path: "state"}, {Head: "VISIBILITY", Path: "visibility"}, {Head: "URL", Path: "url"},
}

var previewDetail = []Col{
	{Head: "URL", Path: "url"}, {Head: "Port", Path: "port"}, {Head: "Name", Path: "name"}, {Head: "State", Path: "state"},
	{Head: "Visibility", Path: "visibility"}, {Head: "Route", Path: "route_status"}, {Head: "Expires", Path: "expires_at"},
	{Head: "Share link until", Path: "share_token_expires_at"}, {Head: "Share token", Path: "share_token", Fmt: ""}, {Head: "Share token prefix", Path: "share_token_prefix"}, {Head: "Id", Path: "id"},
}

func previewCmd() *cobra.Command {
	return group("preview", "Publish a sandbox port at a public URL",
		`A preview exposes a port that a process in the sandbox is listening on. Private
previews need your key or a share link; 'preview share' makes one reachable by
anyone with the link for a limited time. The port must already be listening
(see 'miosa ports').

  miosa process start my-box -- python -m http.server 8080
  miosa preview create my-box 8080 --name web
  miosa preview share my-box <id> --ttl 1h`, []string{"previews"},
		Op{
			Use: "list [sandbox]", Aliases: []string{"ls"}, Short: "List previews",
			Method: "GET", Path: "/sandboxes/{0}/previews", Args: []Arg{sbx}, Cols: previewCols,
		},
		Op{
			Use: "create [sandbox] <port>", Short: "Expose a port",
			Method: "POST", Path: "/sandboxes/{0}/previews", Args: []Arg{sbx, {Name: "port"}},
			Flags: []Flag{
				{Name: "name", Usage: "A label for the preview"},
				{Name: "visibility", Usage: "Who can open it", Default: "private", Enum: []string{"private", "public"}},
				{Name: "ttl", Usage: "Remove it after this long: seconds, 30m, 2h", Field: "ttl_seconds"},
			},
			Body: func(args []string, body map[string]any) (map[string]any, error) {
				port, err := parsePort(args[1])
				if err != nil {
					return nil, err
				}
				body["port"] = port
				if v, ok := body["ttl_seconds"].(string); ok {
					n, err := parseSeconds(v)
					if err != nil {
						return nil, err
					}
					body["ttl_seconds"] = n
				}
				return body, nil
			},
			Detail: previewDetail,
		},
		Op{
			Use: "get [sandbox] <id>", Aliases: []string{"show"}, Short: "Show a preview",
			Method: "GET", Path: "/sandboxes/{0}/previews/{1}", Args: []Arg{sbx, {Name: "preview"}}, Detail: previewDetail,
		},
		Op{
			Use: "status [sandbox] <id>", Short: "Check that a preview answers (process, TLS, route)",
			Method: "GET", Path: "/sandboxes/{0}/previews/{1}/status", Args: []Arg{sbx, {Name: "preview"}},
			Detail: []Col{
				{Head: "Process", Path: "process.status"}, {Head: "HTTP status", Path: "process.http_status"}, {Head: "Latency (ms)", Path: "process.latency_ms"},
				{Head: "TLS", Path: "tls.status"}, {Head: "Sandbox", Path: "sandbox.state"},
			},
		},
		Op{
			Use: "rm [sandbox] <id>", Aliases: []string{"delete"}, Short: "Remove a preview",
			Method: "DELETE", Path: "/sandboxes/{0}/previews/{1}", Args: []Arg{sbx, {Name: "preview"}}, Done: "Removed preview {1}",
		},
		Op{
			Use: "share [sandbox] <id>", Short: "Make a preview reachable by anyone with its link",
			Method: "POST", Path: "/sandboxes/{0}/previews/{1}/share", Args: []Arg{sbx, {Name: "preview"}},
			Flags: []Flag{{Name: "ttl", Usage: "Link lifetime: seconds, 30m, 2h", Field: "ttl_seconds"}},
			Body: func(_ []string, body map[string]any) (map[string]any, error) {
				if v, ok := body["ttl_seconds"].(string); ok {
					n, err := parseSeconds(v)
					if err != nil {
						return nil, err
					}
					body["ttl_seconds"] = n
				}
				return body, nil
			},
			Detail: previewDetail,
		},
		Op{
			Use: "unshare [sandbox] <id>", Short: "Revoke a preview's public link",
			Method: "DELETE", Path: "/sandboxes/{0}/previews/{1}/share", Args: []Arg{sbx, {Name: "preview"}}, Done: "Revoked sharing for {1}",
		},
	)
}

func parsePort(s string) (int, error) {
	var n int
	if _, err := fmt.Sscanf(s, "%d", &n); err != nil || n < 1 || n > 65535 || fmt.Sprint(n) != s {
		return 0, fmt.Errorf("port must be a number from 1 to 65535 (got %q)", s)
	}
	return n, nil
}

// ─── process ──────────────────────────────────────────────────────────────────

var processCols = []Col{
	{Head: "ID", Path: "id", Fmt: "short"}, {Head: "PID", Path: "pid"}, {Head: "STATUS", Path: "status"},
	{Head: "STARTED", Path: "started_at", Fmt: "age"}, {Head: "NAME", Path: "name"}, {Head: "COMMAND", Path: "command"},
}

var processDetail = []Col{
	{Head: "Id", Path: "id"}, {Head: "PID", Path: "pid"}, {Head: "Status", Path: "status"}, {Head: "Exit code", Path: "exit_code"},
	{Head: "Command", Path: "command"}, {Head: "Directory", Path: "cwd"}, {Head: "Started", Path: "started_at", Fmt: "age"},
	{Head: "Stopped", Path: "stopped_at", Fmt: "age"}, {Head: "Log file", Path: "log_path"},
}

func processCmd() *cobra.Command {
	cmd := group("process", "Run and manage background processes in a sandbox",
		`A process keeps running after the CLI disconnects and its output is kept. Use it for
servers and long jobs; use 'exec' for a command whose result you wait for.

  miosa process start my-box --name api -- node server.js
  miosa process list
  miosa process logs <id>
  miosa process kill <id>`, []string{"proc"},
		Op{
			Use: "list [sandbox]", Aliases: []string{"ls"}, Short: "List processes",
			Method: "GET", Path: "/sandboxes/{0}/processes", Args: []Arg{sbx}, Cols: processCols,
		},
		Op{
			Use: "get [sandbox] <id>", Aliases: []string{"show"}, Short: "Show a process",
			Method: "GET", Path: "/sandboxes/{0}/processes/{1}", Args: []Arg{sbx, {Name: "process"}}, Detail: processDetail,
		},
		Op{
			Use: "logs [sandbox] <id>", Short: "Show a process's output",
			Method: "GET", Path: "/sandboxes/{0}/processes/{1}/logs", Args: []Arg{sbx, {Name: "process"}},
			Flags: []Flag{{Name: "tail", Short: "n", Type: "int", Usage: "Number of recent lines"}},
			Lines: "data.lines",
		},
		Op{
			Use: "kill [sandbox] <id>", Aliases: []string{"stop"}, Short: "Stop a process",
			Method: "DELETE", Path: "/sandboxes/{0}/processes/{1}", Args: []Arg{sbx, {Name: "process"}}, Detail: processDetail, Done: "Stopped {1}",
		},
	)
	cmd.AddCommand(newProcessStartCmd())
	return cmd
}

func newProcessStartCmd() *cobra.Command {
	var (
		name string
		cwd  string
		env  []string
		sudo bool
	)
	cmd := &cobra.Command{
		Use:   "start [sandbox] -- <command> [args...]",
		Short: "Start a background process",
		Long: `Start a process and return immediately with its id. The command goes after --.
A single word is a shell line; several words are quoted as an argv, like 'ssh'.

  miosa process start -- python -m http.server 8080
  miosa process start my-box --name worker --cwd /workspace -- ./run.sh`,
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			p := printerFor(cmd)
			dash := cmd.ArgsLenAtDash()
			if dash < 0 || dash == len(args) {
				return usagef("give the command after --: miosa process start [sandbox] -- <command>")
			}
			ref := "current"
			if dash > 0 {
				ref = args[0]
			}
			command := joinCommand(args[dash:])
			body := map[string]any{"command": command}
			if name != "" {
				body["name"] = name
			}
			if cwd != "" {
				body["cwd"] = cwd
			}
			if sudo {
				body["sudo"] = true
			}
			if len(env) > 0 {
				m := map[string]any{}
				for _, kv := range env {
					k, v, ok := strings.Cut(kv, "=")
					if !ok || k == "" {
						return usagef("--env expects KEY=VALUE (got %q)", kv)
					}
					m[k] = v
				}
				body["env"] = m
			}
			c, _, err := buildClient()
			if err != nil {
				return die(err)
			}
			resp, err := c.API.JSONAny(cmd.Context(), api.Request{Method: "POST", Path: "/sandboxes/" + url.PathEscape(ref) + "/processes", Body: body})
			if err != nil {
				return die(err)
			}
			if isJSON() {
				return p.JSON(resp)
			}
			obj, _ := unwrapObject(resp).(map[string]any)
			p.Success("Started process %s (pid %s)", formatValue(obj["id"], ""), formatValue(obj["pid"], ""))
			return nil
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "A name for the process")
	cmd.Flags().StringVar(&cwd, "cwd", "", "Working directory")
	cmd.Flags().StringArrayVar(&env, "env", nil, "Environment variable KEY=VALUE (repeatable)")
	cmd.Flags().BoolVar(&sudo, "sudo", false, "Run as root")
	return cmd
}

// ─── desktop ──────────────────────────────────────────────────────────────────

func desktopCmd() *cobra.Command {
	return group("desktop", "Start and open a sandbox's remote desktop",
		`A sandbox built from a desktop template can serve a graphical desktop. 'desktop
start' launches it and prints the stream URL; 'desktop url' prints it again. For
full computers see 'miosa computer urls'.

  miosa desktop start my-box
  miosa desktop url my-box`, nil,
		Op{
			Use: "start [sandbox]", Short: "Start the desktop and print its URL",
			Method: "POST", Path: "/sandboxes/{0}/desktop", Args: []Arg{sbx},
		},
		Op{
			Use: "url [sandbox]", Aliases: []string{"status", "get"}, Short: "Print the running desktop's URL",
			Method: "GET", Path: "/sandboxes/{0}/desktop", Args: []Arg{sbx},
		},
	)
}

// ─── computers ────────────────────────────────────────────────────────────────

var cptr = Arg{Name: "computer", Complete: ""}

var computerCols = []Col{
	{Head: "NAME", Path: "name"}, {Head: "ID", Path: "id"}, {Head: "STATUS", Path: "status"},
	{Head: "SIZE", Path: "size"}, {Head: "TEMPLATE", Path: "template_type"}, {Head: "CREATED", Path: "created_at", Fmt: "age"},
}

var computerDetail = []Col{
	{Head: "Name", Path: "name"}, {Head: "Id", Path: "id"}, {Head: "Status", Path: "status"}, {Head: "Size", Path: "size"},
	{Head: "Template", Path: "template_type"}, {Head: "Region", Path: "region"}, {Head: "Created", Path: "created_at", Fmt: "age"},
}

// computerOps are the lifecycle and access commands of 'miosa computer'.
// A computer is a full desktop VM; <computer> is a name or an id.
func computerOps() []*cobra.Command {
	ops := []Op{
		{
			Use: "list", Aliases: []string{"ls"}, Short: "List computers",
			Method: "GET", Path: "/computers", ListKey: "computers", Cols: computerCols,
		},
		{
			Use: "get <computer>", Aliases: []string{"info", "show"}, Short: "Show a computer",
			Method: "GET", Path: "/computers/{0}", Args: []Arg{cptr}, Detail: computerDetail,
		},
		{
			Use: "start <computer>", Short: "Start a stopped computer",
			Method: "POST", Path: "/computers/{0}/start", Args: []Arg{cptr}, Done: "Starting {0}",
		},
		{
			Use: "stop <computer>", Short: "Stop a computer (it keeps its disk)",
			Method: "POST", Path: "/computers/{0}/stop", Args: []Arg{cptr}, Done: "Stopping {0}",
		},
		{
			Use: "restart <computer>", Short: "Restart a computer",
			Method: "POST", Path: "/computers/{0}/restart", Args: []Arg{cptr}, Done: "Restarting {0}",
		},
		{
			Use: "rm <computer>", Aliases: []string{"destroy", "delete"}, Short: "Permanently delete a computer",
			Method: "DELETE", Path: "/computers/{0}", Args: []Arg{cptr}, Confirm: "Permanently delete computer {0}? This cannot be undone.",
			Done: "Deleted {0}",
		},
		{
			Use: "urls <computer>", Short: "Show the computer's desktop and service URLs",
			Long:   "Print the URLs for the computer's desktop stream and exposed services. They can carry a key: treat the output like a password.",
			Method: "GET", Path: "/computers/{0}/urls", Args: []Arg{cptr},
		},
		{
			Use: "auto-stop <computer>", Short: "Show when an idle computer stops itself",
			Method: "GET", Path: "/computers/{0}/auto-stop", Args: []Arg{cptr},
		},
		{
			Use: "logs <computer>", Short: "Show a computer's log",
			Method: "GET", Path: "/computers/{0}/logs", Args: []Arg{cptr}, Lines: "logs",
		},
		{
			Use: "ports <computer>", Short: "List ports exposed by a computer",
			Method: "GET", Path: "/computers/{0}/ports", Args: []Arg{cptr},
			Cols: []Col{{Head: "PORT", Path: "port"}, {Head: "PROTO", Path: "protocol"}, {Head: "VISIBILITY", Path: "visibility"}},
		},
	}
	cmds := make([]*cobra.Command, 0, len(ops))
	for _, o := range ops {
		cmds = append(cmds, o.command("computer"))
	}
	return cmds
}
