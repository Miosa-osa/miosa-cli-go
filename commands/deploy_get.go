package commands

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/Miosa-osa/miosa-cli-go/internal/client"
	"github.com/spf13/cobra"
)

// newDeployGetCmd shows one deployment, including what it is actually running.
// The kind lives on the version, not on the deployment, so this reads the
// active version once and adds it to what the deployment carries.
func newDeployGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "get <deployment>",
		Aliases: []string{"show"},
		Short:   "Show a deployment",
		Long: `Show a deployment and what it is running.

  miosa deploy get my-app

<deployment> is a name, slug, hostname or id. Kind is the active version's
kind: static, or dynamic for a deployment that runs a server. A dynamic or
App Engine deployment also shows the image, command and port it runs.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDeployGet(cmd, args[0])
		},
	}
}

func runDeployGet(cmd *cobra.Command, ref string) error {
	p := printerFor(cmd)

	c, _, err := buildClientTimeout(0)
	if err != nil {
		return die(err)
	}

	var resp map[string]any
	if err := c.API.Get(cmd.Context(), "/deployments/"+url.PathEscape(ref), nil, &resp); err != nil {
		return die(err)
	}
	obj := unwrapObject(resp)

	data, _ := obj.(map[string]any)
	kind, runtime := activeVersionFacts(cmd, c, data)
	if kind != "" {
		data["kind"] = kind
	}
	if len(runtime) > 0 {
		data["runtime"] = runtime
	}

	if isJSON() {
		return p.JSON(resp)
	}

	rows := make([][2]string, 0, len(deployDetail)+4)
	for _, col := range deployDetail {
		rows = append(rows, [2]string{col.Head, formatValue(lookupPath(obj, col.Path), col.Fmt)})
	}
	if kind != "" {
		rows = append(rows, [2]string{"Kind", kind})
	}
	if image, _ := runtime["image"].(string); image != "" {
		rows = append(rows, [2]string{"Runtime image", image})
	}
	if command, _ := runtime["command"].(string); command != "" {
		rows = append(rows, [2]string{"Runtime command", command})
	}
	if port, ok := runtime["port"].(float64); ok && port > 0 {
		rows = append(rows, [2]string{"Runtime port", strconv.Itoa(int(port))})
	}
	p.Fields(rows)
	return nil
}

// activeVersionFacts reads the active version: its kind, and the image,
// command and port a non-static deployment runs. A version we cannot read (a
// deleted one, or a deployment that has never released) leaves the deployment
// answer intact rather than failing the whole command.
func activeVersionFacts(cmd *cobra.Command, c *client.Client, data map[string]any) (string, map[string]any) {
	if data == nil {
		return "", nil
	}
	id, _ := data["id"].(string)
	versionID, _ := data["active_version_id"].(string)
	if id == "" || versionID == "" {
		return "", nil
	}

	var resp struct {
		Data map[string]any `json:"data"`
	}
	path := fmt.Sprintf("/deployments/%s/versions/%s",
		url.PathEscape(id), url.PathEscape(versionID))
	if err := c.API.Get(cmd.Context(), path, nil, &resp); err != nil {
		return "", nil
	}

	kind, _ := resp.Data["kind"].(string)

	runtime := map[string]any{}
	for _, key := range []string{"runtime_image", "runtime_command"} {
		if v, ok := resp.Data[key].(string); ok && v != "" {
			runtime[strings.TrimPrefix(key, "runtime_")] = v
		}
	}
	if v := resp.Data["runtime_port"]; v != nil {
		runtime["port"] = v
	}
	if len(runtime) == 0 {
		runtime = nil
	}
	return kind, runtime
}
