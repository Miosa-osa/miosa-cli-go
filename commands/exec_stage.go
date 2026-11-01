package commands

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net/url"

	"github.com/Miosa-osa/miosa-cli-go/internal/api"
)

// execInlineLimit is the longest command sent inline. The machine's shell gets
// the command as one argv string, which Linux caps at 128 KiB; past that the
// run fails with "argument list too long". Half of it leaves room for the
// wrapper and the request's own framing.
const execInlineLimit = 64 * 1024

// stageLargeCommand keeps a long command off argv. Past execInlineLimit the
// command is written to a file through the files API (no length limit that
// matters) and the returned command runs that file, removes it, and exits with
// its status. A short command is returned unchanged. kind is "sandbox" or
// "computer".
func stageLargeCommand(ctx context.Context, c *api.Client, kind, id, command string) (string, error) {
	if len(command) <= execInlineLimit {
		return command, nil
	}
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	path := "/tmp/.miosa-exec-" + hex.EncodeToString(b[:]) + ".sh"
	body := map[string]string{"path": path, "content_base64": base64.StdEncoding.EncodeToString([]byte(command))}
	route := "/sandboxes/"
	if kind == "computer" {
		route = "/computers/"
	}
	if err := c.Post(ctx, route+url.PathEscape(id)+"/files/write", body, nil); err != nil {
		return "", fmt.Errorf("sending a %d byte command: %w", len(command), err)
	}
	return fmt.Sprintf("/bin/sh %[1]s; rc=$?; rm -f %[1]s; exit $rc", path), nil
}
