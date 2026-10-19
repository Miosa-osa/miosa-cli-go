package commands

import (
	"fmt"
	"strconv"

	"github.com/spf13/cobra"
)

func init() { rootCmd.AddCommand(deviceCmd()) }

var deviceCols = []Col{
	{Head: "NAME", Path: "name"}, {Head: "ID", Path: "id", Fmt: "short"}, {Head: "KIND", Path: "kind"},
	{Head: "STATE", Path: "state"}, {Head: "RUNNING", Path: "running"}, {Head: "TEMPLATE", Path: "template"},
	{Head: "IP", Path: "ip_address"},
}

var deviceDetail = []Col{
	{Head: "Name", Path: "name"}, {Head: "Id", Path: "id"}, {Head: "Kind", Path: "kind"}, {Head: "State", Path: "state"},
	{Head: "Running", Path: "running"}, {Head: "Terminal", Path: "terminal"}, {Head: "Template", Path: "template"},
	{Head: "IP", Path: "ip_address"}, {Head: "Node", Path: "node_id"}, {Head: "Slug", Path: "slug"},
}

// deviceCmd lists the compute you can run work on, whatever backs it: a
// sandbox, a computer or another runtime kind. It is a read-mostly view over
// the same machines the 'list' and 'computer' commands manage.
func deviceCmd() *cobra.Command {
	d := idArg("device")
	return group("device", "Every machine you can run work on, across runtime kinds",
		`A device is any runtime MIOSA can run a command on: a sandbox, a computer or
another kind. <device> is an id.

  miosa device list
  miosa device list --status running
  miosa device capabilities <device>
  miosa device exec <device> -- uname -a`, []string{"devices"},
		Op{
			Use: "list", Aliases: []string{"ls"}, Short: "List devices", Method: "GET", Path: "/devices", Cols: deviceCols,
			Flags: []Flag{
				{Name: "status", Usage: "Only this state"}, {Name: "name", Usage: "Only devices whose name contains this"},
				{Name: "limit", Short: "n", Type: "int", Usage: "How many"},
				{Name: "errored", Type: "bool", Field: "include_errored", Usage: "Include machines in the error state"},
			},
			HiddenCount: "meta.errored_hidden", HiddenHint: "--errored",
		},
		Op{Use: "get <device>", Aliases: []string{"show"}, Short: "Show one device", Method: "GET", Path: "/devices/{0}", Args: []Arg{d}, Detail: deviceDetail},
		Op{Use: "capabilities <device>", Aliases: []string{"caps"}, Short: "What this device can do", Method: "GET", Path: "/devices/{0}/capabilities", Args: []Arg{d}},
		Op{
			Use: "exec <device>", Short: "Run a command on a device", Method: "POST", Path: "/devices/{0}/exec", Args: []Arg{d},
			Flags: []Flag{{Name: "command", Short: "c", Usage: "Shell command to run", Required: true}, {Name: "timeout-ms", Type: "int", Usage: "Time limit in milliseconds"}},
		},
		Op{Use: "files <device>", Aliases: []string{"ls"}, Short: "List a directory on a device", Method: "GET", Path: "/devices/{0}/files", Args: []Arg{d}, Flags: []Flag{{Name: "path", Usage: "Directory (default /workspace)"}}},
		Op{Use: "read <device>", Short: "Read a file (base64) from a device", Method: "GET", Path: "/devices/{0}/files/read", Args: []Arg{d}, Flags: []Flag{{Name: "path", Usage: "Path to read", Required: true}}},
		Op{
			Use: "write <device>", Short: "Write a file on a device", Method: "POST", Path: "/devices/{0}/files/write", Args: []Arg{d}, FileBody: true,
			Flags: []Flag{{Name: "path", Usage: "Path to write", Required: true}, {Name: "content", Usage: "File contents (or pass a JSON body with --file)"}},
		},
		Op{
			Use: "expose <device> <port>", Short: "Publish a port of a device", Method: "POST", Path: "/devices/{0}/expose", Args: []Arg{d, idArg("port")},
			Body: func(args []string, body map[string]any) (map[string]any, error) {
				port, err := strconv.Atoi(args[1])
				if err != nil || port < 1 || port > 65535 {
					return nil, fmt.Errorf("port must be a whole number between 1 and 65535 (got %q)", args[1])
				}
				body["port"] = port
				return body, nil
			},
		},
		Op{Use: "browser <device>", Short: "Show the URL of a device's browser (computers)", Method: "GET", Path: "/devices/{0}/browser", Args: []Arg{d}},
		Op{Use: "pause <device>", Short: "Pause a device", Method: "POST", Path: "/devices/{0}/pause", Args: []Arg{d}, Done: "Paused {0}"},
		Op{Use: "stop <device>", Short: "Stop a device, keeping its disk", Method: "POST", Path: "/devices/{0}/stop", Args: []Arg{d}, Done: "Stopped {0}"},
		Op{Use: "resume <device>", Short: "Resume a paused device", Method: "POST", Path: "/devices/{0}/resume", Args: []Arg{d}, Done: "Resumed {0}"},
		Op{
			Use: "extend <device>", Short: "Replace a device's time to live", Method: "POST", Path: "/devices/{0}/extend", Args: []Arg{d},
			Flags: []Flag{{Name: "timeout", Usage: "New time to live, for example 30m or 2h", Required: true, Field: "timeout_sec"}},
			Body: func(args []string, body map[string]any) (map[string]any, error) {
				sec, err := parseSeconds(fmt.Sprint(body["timeout_sec"]))
				if err != nil {
					return nil, err
				}
				body["timeout_sec"] = sec
				return body, nil
			},
			Done: "Extended {0}",
		},
		Op{Use: "rm <device>", Aliases: []string{"delete", "destroy"}, Short: "Permanently destroy a device", Method: "DELETE", Path: "/devices/{0}", Args: []Arg{d}, Confirm: "Permanently destroy device {0}? This cannot be undone.", Done: "Destroyed {0}"},
	)
}
