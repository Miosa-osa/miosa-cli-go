package commands

import (
	"fmt"

	"github.com/spf13/cobra"
)

func init() { rootCmd.AddCommand(hostCmd()) }

var hostCols = []Col{
	{Head: "ID", Path: "id"}, {Head: "NAME", Path: "name"}, {Head: "STATE", Path: "state"}, {Head: "CONNECTED", Path: "connected"},
	{Head: "OS", Path: "os_kind"}, {Head: "ARCH", Path: "arch"}, {Head: "TAGS", Path: "tags", Fmt: "list"}, {Head: "SEEN", Path: "last_heartbeat", Fmt: "age"},
}

var hostDetail = []Col{
	{Head: "Id", Path: "id"}, {Head: "Name", Path: "name"}, {Head: "State", Path: "state"}, {Head: "Connected", Path: "connected"},
	{Head: "OS", Path: "os_kind"}, {Head: "Version", Path: "os_version"}, {Head: "Arch", Path: "arch"}, {Head: "Region", Path: "region"},
	{Head: "Agent", Path: "agent_version"}, {Head: "Update pending", Path: "pending_update_version"}, {Head: "Tags", Path: "tags", Fmt: "list"},
	{Head: "Last seen", Path: "last_heartbeat", Fmt: "age"}, {Head: "Created", Path: "created_at", Fmt: "age"},
}

// hostCmd manages your own hosts: machines you own (a Mac, a Linux server, a
// workstation) enrolled in your organization so MIOSA can run work on them.
func hostCmd() *cobra.Command {
	host := Arg{Name: "host", Complete: ""}
	cmd := group("host", "Your own machines enrolled in your organization (bring your own host)",
		`A host is a machine you own, with the MIOSA host agent installed, that your
organization can run work on: commands, containers, apps, tunnels and agents.
'host register' prints the host key once; give it to the agent on the machine.
<host> is an id.

  miosa host register studio --platform macos
  miosa host list
  miosa host apps catalog
  miosa host container list <host>
  miosa host tunnel list <host>`, []string{"hosts", "opencomputers"},
		Op{Use: "list", Aliases: []string{"ls"}, Short: "List hosts", Method: "GET", Path: "/opencomputers/hosts", Cols: hostCols,
			Flags: []Flag{{Name: "tag", Usage: "Only hosts with this tag"}}},
		Op{Use: "get <host>", Aliases: []string{"show"}, Short: "Show one host", Method: "GET", Path: "/opencomputers/hosts/{0}", Args: []Arg{host}, Detail: hostDetail},
		Op{
			Use: "rm <host>", Aliases: []string{"delete"}, Short: "Remove a host from the organization", Method: "DELETE", Path: "/opencomputers/hosts/{0}",
			Args: []Arg{host}, Confirm: "Remove host {0}? Its agent is disconnected.", Done: "Removed {0}",
		},
		Op{Use: "metrics <host>", Short: "Show CPU, memory and disk of a host", Method: "GET", Path: "/opencomputers/hosts/{0}/metrics", Args: []Arg{host}},
		Op{Use: "jobs <host>", Short: "List recent jobs on a host", Method: "GET", Path: "/opencomputers/hosts/{0}/jobs", Args: []Arg{host},
			Flags: []Flag{{Name: "limit", Type: "int", Usage: "How many jobs"}}},
		Op{Use: "runtime <host>", Short: "Show the container runtime of a host", Method: "GET", Path: "/opencomputers/hosts/{0}/runtime", Args: []Arg{host}},
		Op{Use: "services <host>", Short: "List the services running on a host", Method: "GET", Path: "/opencomputers/hosts/{0}/services", Args: []Arg{host}},
	)
	cmd.AddCommand(newHostRegisterCmd())

	cmd.AddCommand(
		group("tag", "Tags that group hosts", "Tags let you find hosts and run work on groups.", []string{"tags"},
			Op{Use: "list", Aliases: []string{"ls"}, Short: "List every tag in use", Method: "GET", Path: "/opencomputers/tags"},
			Op{Use: "add <host> <tag>", Short: "Add a tag to a host", Method: "POST", Path: "/opencomputers/hosts/{0}/tags/{1}", Args: []Arg{host, {Name: "tag"}}, Done: "Tagged {0} with {1}"},
			Op{Use: "rm <host> <tag>", Aliases: []string{"remove"}, Short: "Remove a tag from a host", Method: "DELETE", Path: "/opencomputers/hosts/{0}/tags/{1}", Args: []Arg{host, {Name: "tag"}}, Done: "Removed {1} from {0}"},
		),
		group("group", "Named sets of hosts", "A group runs a command or installs an app on every member.", []string{"groups"},
			Op{Use: "list", Aliases: []string{"ls"}, Short: "List groups", Method: "GET", Path: "/opencomputers/groups"},
			Op{Use: "get <group>", Short: "Show a group and its members", Method: "GET", Path: "/opencomputers/groups/{0}", Args: []Arg{{Name: "group"}}},
			Op{
				Use: "new <name>", Aliases: []string{"create"}, Short: "Create a group", Method: "POST", Path: "/opencomputers/groups", Args: []Arg{{Name: "name"}},
				Flags: []Flag{{Name: "description", Usage: "What the group is for"}},
				Body: func(args []string, body map[string]any) (map[string]any, error) {
					body["name"] = args[0]
					return body, nil
				},
			},
			Op{Use: "add <group> <host>", Short: "Add a host to a group", Method: "POST", Path: "/opencomputers/groups/{0}/members/{1}", Args: []Arg{{Name: "group"}, host}, Done: "Added {1} to {0}"},
			Op{Use: "remove <group> <host>", Short: "Remove a host from a group", Method: "DELETE", Path: "/opencomputers/groups/{0}/members/{1}", Args: []Arg{{Name: "group"}, host}, Done: "Removed {1} from {0}"},
			Op{
				Use: "exec <group>", Short: "Run a command on every host of a group", Method: "POST", Path: "/opencomputers/groups/{0}/exec", Args: []Arg{{Name: "group"}},
				Flags: []Flag{{Name: "cmd", Usage: "Program to run", Required: true}, {Name: "arg", Usage: "An argument (repeatable)", Type: "strings", Field: "args"}, {Name: "cwd", Usage: "Working directory"}, {Name: "timeout-ms", Usage: "Time limit in milliseconds", Type: "int"}},
			},
			Op{Use: "rm <group>", Aliases: []string{"delete"}, Short: "Delete a group (its hosts stay)", Method: "DELETE", Path: "/opencomputers/groups/{0}", Args: []Arg{{Name: "group"}}, Confirm: "Delete group {0}?", Done: "Deleted {0}"},
		),
		group("job", "Commands dispatched to a host", "", []string{"jobs", "exec"},
			Op{Use: "list <host>", Aliases: []string{"ls"}, Short: "List jobs", Method: "GET", Path: "/opencomputers/hosts/{0}/exec", Args: []Arg{host}},
			Op{Use: "get <host> <job>", Short: "Show one job and its output", Method: "GET", Path: "/opencomputers/hosts/{0}/exec/{1}", Args: []Arg{host, {Name: "job"}}},
			Op{Use: "cancel <host> <job>", Short: "Cancel a running job", Method: "DELETE", Path: "/opencomputers/hosts/{0}/exec/{1}", Args: []Arg{host, {Name: "job"}}, Done: "Canceled {1}"},
		),
		group("fs", "Files on a host", "Paths are on the host's own file system.", nil,
			Op{Use: "ls <host>", Aliases: []string{"list"}, Short: "List a directory", Method: "GET", Path: "/opencomputers/hosts/{0}/fs", Args: []Arg{host}, Flags: []Flag{{Name: "path", Usage: "Directory (default: the home directory)"}}},
			Op{Use: "stat <host>", Short: "Show a file's size and mode", Method: "GET", Path: "/opencomputers/hosts/{0}/fs/stat", Args: []Arg{host}, Flags: []Flag{{Name: "path", Usage: "Path to inspect", Required: true}}},
			Op{Use: "mkdir <host>", Short: "Create a directory", Method: "POST", Path: "/opencomputers/hosts/{0}/fs/mkdir", Args: []Arg{host}, Flags: []Flag{{Name: "path", Usage: "Directory to create", Required: true}}, Done: "Created the directory"},
			Op{Use: "rm <host>", Aliases: []string{"delete"}, Short: "Delete a file or directory", Method: "DELETE", Path: "/opencomputers/hosts/{0}/fs", Args: []Arg{host}, Flags: []Flag{{Name: "path", Usage: "Path to delete", Required: true}}, Confirm: "Delete that path on host {0}?", Done: "Deleted"},
		),
		group("app", "Apps you can install on a host", "The catalog lists what MIOSA can install; install puts one on a host.", []string{"apps"},
			Op{Use: "catalog", Short: "List installable apps", Method: "GET", Path: "/opencomputers/apps"},
			Op{Use: "list <host>", Aliases: []string{"ls"}, Short: "List the apps installed on a host", Method: "GET", Path: "/opencomputers/hosts/{0}/apps", Args: []Arg{host}},
			Op{Use: "install <host> <app>", Short: "Install an app on a host", Method: "POST", Path: "/opencomputers/hosts/{0}/apps/{1}/install", Args: []Arg{host, {Name: "app"}}, FileBody: true},
			Op{Use: "start <host> <app>", Short: "Start an installed app", Method: "POST", Path: "/opencomputers/hosts/{0}/apps/{1}/start", Args: []Arg{host, {Name: "app"}}, Done: "Started {1}"},
			Op{Use: "health <host> <app>", Short: "Check that an installed app is healthy", Method: "POST", Path: "/opencomputers/hosts/{0}/apps/{1}/health", Args: []Arg{host, {Name: "app"}}},
			Op{Use: "uninstall <host> <app>", Aliases: []string{"rm"}, Short: "Uninstall an app", Method: "DELETE", Path: "/opencomputers/hosts/{0}/apps/{1}", Args: []Arg{host, {Name: "app"}}, Confirm: "Uninstall {1} from host {0}?", Done: "Uninstalled {1}"},
		),
		group("container", "Containers on a host", "", []string{"containers"},
			Op{Use: "list <host>", Aliases: []string{"ls"}, Short: "List containers", Method: "GET", Path: "/opencomputers/hosts/{0}/containers", Args: []Arg{host}},
			Op{Use: "get <host> <container>", Short: "Show one container", Method: "GET", Path: "/opencomputers/hosts/{0}/containers/{1}", Args: []Arg{host, {Name: "container"}}},
			Op{
				Use: "run <host>", Aliases: []string{"create"}, Short: "Create a container", Method: "POST", Path: "/opencomputers/hosts/{0}/containers", Args: []Arg{host}, FileBody: true,
				Flags: []Flag{
					{Name: "image", Usage: "Image to run", Required: true}, {Name: "name", Usage: "Container name"},
					{Name: "cmd", Usage: "Command and arguments (repeatable)", Type: "strings", Field: "command"},
					{Name: "set", Usage: "Environment variable KEY=VALUE (repeatable)", Type: "kv", Field: "env"},
					{Name: "restart", Usage: "Restart policy", Field: "restart_policy"},
				},
			},
			Op{Use: "start <host> <container>", Short: "Start a container", Method: "POST", Path: "/opencomputers/hosts/{0}/containers/{1}/start", Args: []Arg{host, {Name: "container"}}, Done: "Started {1}"},
			Op{Use: "stop <host> <container>", Short: "Stop a container", Method: "POST", Path: "/opencomputers/hosts/{0}/containers/{1}/stop", Args: []Arg{host, {Name: "container"}}, Done: "Stopped {1}"},
			Op{Use: "restart <host> <container>", Short: "Restart a container", Method: "POST", Path: "/opencomputers/hosts/{0}/containers/{1}/restart", Args: []Arg{host, {Name: "container"}}, Done: "Restarted {1}"},
			Op{
				Use: "rm <host> <container>", Aliases: []string{"delete"}, Short: "Delete a container", Method: "DELETE", Path: "/opencomputers/hosts/{0}/containers/{1}", Args: []Arg{host, {Name: "container"}},
				Flags: []Flag{{Name: "force", Type: "bool", Usage: "Delete it even if it is running"}}, Confirm: "Delete container {1} on host {0}?", Done: "Deleted {1}",
			},
		),
		group("compose", "Compose projects on a host", "A compose project is a docker-compose file the host keeps running.", nil,
			Op{Use: "list <host>", Aliases: []string{"ls"}, Short: "List projects", Method: "GET", Path: "/opencomputers/hosts/{0}/compose-projects", Args: []Arg{host}},
			Op{Use: "up <host> <project>", Short: "Start a project", Method: "POST", Path: "/opencomputers/hosts/{0}/compose-projects/{1}/up", Args: []Arg{host, {Name: "project"}}, Done: "Started {1}",
				Flags: []Flag{{Name: "build", Type: "bool", Usage: "Build images first"}, {Name: "pull", Type: "bool", Usage: "Pull images first"}}},
			Op{Use: "down <host> <project>", Short: "Stop a project", Method: "POST", Path: "/opencomputers/hosts/{0}/compose-projects/{1}/down", Args: []Arg{host, {Name: "project"}}, Done: "Stopped {1}",
				Flags: []Flag{{Name: "remove-volumes", Type: "bool", Usage: "Also remove its volumes", Field: "remove_volumes"}}},
			Op{Use: "rm <host> <project>", Aliases: []string{"delete"}, Short: "Delete a project", Method: "DELETE", Path: "/opencomputers/hosts/{0}/compose-projects/{1}", Args: []Arg{host, {Name: "project"}}, Confirm: "Delete project {1} on host {0}?", Done: "Deleted {1}"},
		),
		group("tunnel", "Public addresses that reach a service on a host", "", []string{"tunnels"},
			Op{Use: "list <host>", Aliases: []string{"ls"}, Short: "List tunnels", Method: "GET", Path: "/opencomputers/hosts/{0}/tunnels", Args: []Arg{host}},
			Op{Use: "get <host> <tunnel>", Short: "Show one tunnel", Method: "GET", Path: "/opencomputers/hosts/{0}/tunnels/{1}", Args: []Arg{host, {Name: "tunnel"}}},
			Op{
				Use: "new <host>", Aliases: []string{"create"}, Short: "Expose a port of a host at a public address", Method: "POST", Path: "/opencomputers/hosts/{0}/tunnels", Args: []Arg{host},
				Flags: []Flag{
					{Name: "port", Usage: "Port on the host", Type: "int", Field: "target_port", Required: true},
					{Name: "slug", Usage: "Address name (default: generated)"},
					{Name: "access", Usage: "Who may open it: organization (default) or public", Field: "auth_mode", Enum: []string{"organization", "public"}},
				},
				Body: func(args []string, body map[string]any) (map[string]any, error) {
					if body["auth_mode"] == "organization" {
						body["auth_mode"] = "tenant_only"
					}
					return body, nil
				},
			},
			Op{Use: "rm <host> <tunnel>", Aliases: []string{"delete"}, Short: "Delete a tunnel", Method: "DELETE", Path: "/opencomputers/hosts/{0}/tunnels/{1}", Args: []Arg{host, {Name: "tunnel"}}, Confirm: "Delete tunnel {1}?", Done: "Deleted {1}"},
		),
		group("ssh-key", "SSH keys allowed to sign in to a host", "Only public keys are handled here.", []string{"ssh-keys"},
			Op{Use: "list <host>", Aliases: []string{"ls"}, Short: "List keys", Method: "GET", Path: "/opencomputers/hosts/{0}/ssh-keys", Args: []Arg{host}},
			Op{
				Use: "add <host>", Short: "Allow a public key", Method: "POST", Path: "/opencomputers/hosts/{0}/ssh-keys", Args: []Arg{host},
				Flags: []Flag{{Name: "name", Usage: "A label for the key"}, {Name: "public-key", Usage: "The public key line, for example ssh-ed25519 AAAA", Field: "pubkey", Required: true}},
			},
			Op{
				Use: "import <host> <github-user>", Short: "Allow the public keys a GitHub user publishes", Method: "POST", Path: "/opencomputers/hosts/{0}/ssh-keys/import/github", Args: []Arg{host, {Name: "github-user"}},
				Body: func(args []string, body map[string]any) (map[string]any, error) {
					body["github_username"] = args[1]
					return body, nil
				},
			},
			Op{Use: "rm <host> <key>", Aliases: []string{"delete"}, Short: "Remove a key", Method: "DELETE", Path: "/opencomputers/hosts/{0}/ssh-keys/{1}", Args: []Arg{host, {Name: "key"}}, Confirm: "Remove SSH key {1}?", Done: "Removed {1}"},
		),
		group("cluster", "Groups of hosts that serve a model together", "", []string{"clusters"},
			Op{Use: "list", Aliases: []string{"ls"}, Short: "List clusters", Method: "GET", Path: "/opencomputers/clusters"},
			Op{Use: "get <cluster>", Short: "Show one cluster", Method: "GET", Path: "/opencomputers/clusters/{0}", Args: []Arg{{Name: "cluster"}}},
			Op{Use: "start <cluster>", Short: "Start a cluster", Method: "POST", Path: "/opencomputers/clusters/{0}/start", Args: []Arg{{Name: "cluster"}}, Done: "Started {0}"},
			Op{Use: "stop <cluster>", Short: "Stop a cluster", Method: "POST", Path: "/opencomputers/clusters/{0}/stop", Args: []Arg{{Name: "cluster"}}, Done: "Stopped {0}"},
			Op{Use: "rm <cluster>", Aliases: []string{"delete"}, Short: "Delete a cluster", Method: "DELETE", Path: "/opencomputers/clusters/{0}", Args: []Arg{{Name: "cluster"}}, Confirm: "Delete cluster {0}?", Done: "Deleted {0}"},
		),
		group("agent", "Agents running on a host", "", nil,
			Op{Use: "runs <host>", Short: "List agent runs on a host", Method: "GET", Path: "/opencomputers/hosts/{0}/agent/sessions", Args: []Arg{host}},
			Op{Use: "run <host> <run>", Short: "Show one agent run on a host", Method: "GET", Path: "/opencomputers/hosts/{0}/agent/sessions/{1}", Args: []Arg{host, {Name: "run"}}},
			Op{Use: "cancel <host> <run>", Short: "Cancel an agent run on a host", Method: "DELETE", Path: "/opencomputers/hosts/{0}/agent/sessions/{1}", Args: []Arg{host, {Name: "run"}}, Done: "Canceled {1}"},
		),
		hostSecretGroup(host),
	)
	return cmd
}

func newHostRegisterCmd() *cobra.Command {
	var platform, region string
	cmd := &cobra.Command{
		Use:   "register <name>",
		Short: "Enroll a new host and print its host key",
		Long: `Creates a host record and prints the host key once. Install the host agent on
the machine and give it the key and the control URL. The key is not shown again:
store it right away. --quiet-key prints only the key.

  miosa host register studio --platform macos`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, _, err := buildClient()
			if err != nil {
				return die(err)
			}
			body := map[string]any{"name": args[0]}
			if platform != "" {
				body["platform"] = platform
			}
			if region != "" {
				body["region"] = region
			}
			resp, err := c.API.JSONAny(cmd.Context(), reqPost("/opencomputers/hosts", body))
			if err != nil {
				return die(err)
			}
			m, _ := resp.(map[string]any)
			hostObj, _ := m["host"].(map[string]any)
			key := fmt.Sprint(orEmpty(m["host_key"], ""))
			quiet, _ := cmd.Flags().GetBool("quiet-key")
			p := printerFor(cmd)
			switch {
			case quiet:
				fmt.Fprintln(cmd.OutOrStdout(), key)
			case isJSON():
				return p.JSON(resp)
			default:
				p.Success("Registered host %q. The host key is shown once - store it now:", args[0])
				fmt.Fprintf(p.Writer(), "\n  %s\n\n", key)
				p.Fields([][2]string{{"Id", formatValue(hostObj["id"], "")}, {"Control URL", formatValue(m["control_url"], "")}})
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&platform, "platform", "", "Operating system: macos, linux or windows")
	cmd.Flags().StringVar(&region, "region", "", "Region")
	cmd.Flags().Bool("quiet-key", false, "Print only the host key")
	return cmd
}

// hostSecretSetCmd stores a secret for a host. The value is never an argument.
func hostSecretSetCmd() *cobra.Command {
	var description string
	var fromStdin bool
	cmd := &cobra.Command{
		Use:   "set <host> <name>",
		Short: "Store a secret for a host (the value is asked for, or read from stdin)",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			value, err := readSecret(cmd, "Value for "+args[1]+" (input hidden): ", fromStdin)
			if err != nil {
				return die(err)
			}
			if value == "" {
				return usagef("no value: enter it when asked or pipe it on standard input")
			}
			c, _, err := buildClient()
			if err != nil {
				return die(err)
			}
			body := map[string]any{"name": args[1], "value": value, "scope": "host"}
			if description != "" {
				body["description"] = description
			}
			if _, err := c.API.JSONAny(cmd.Context(), reqPost("/opencomputers/hosts/"+args[0]+"/secrets", body)); err != nil {
				return die(err)
			}
			printerFor(cmd).Success("Stored secret %s for host %s", args[1], args[0])
			return nil
		},
	}
	cmd.Flags().StringVar(&description, "description", "", "What the secret is for")
	cmd.Flags().BoolVar(&fromStdin, "key-stdin", false, "Read the value from standard input")
	return cmd
}

func hostSecretGroup(host Arg) *cobra.Command {
	g := group("secret", "Secrets handed to a host", "Secret values are never shown by list; they go in through 'secret set' on stdin or a prompt.", []string{"secrets"},
		Op{Use: "list <host>", Aliases: []string{"ls"}, Short: "List the secrets of a host (names only)", Method: "GET", Path: "/opencomputers/hosts/{0}/secrets", Args: []Arg{host}},
		Op{Use: "rm <host> <name>", Aliases: []string{"delete"}, Short: "Delete a secret", Method: "DELETE", Path: "/opencomputers/hosts/{0}/secrets/{1}", Args: []Arg{host, {Name: "name"}}, Confirm: "Delete secret {1}?", Done: "Deleted {1}"},
	)
	g.AddCommand(hostSecretSetCmd())
	return g
}
