package commands

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/Miosa-osa/miosa-cli-go/internal/cdpforward"
	"github.com/Miosa-osa/miosa-cli-go/internal/client"
)

func newBrowserCmd() *cobra.Command {
	var sandbox string
	cmd := &cobra.Command{
		Use:   "browser",
		Short: "Drive a sandbox's live browser over the DevTools Protocol",
		Long: `The browser runs inside the sandbox. 'start' launches it lazily and 'open'
shows its live view; 'forward' and 'devtools' open it on your machine over the
DevTools Protocol, which is what Selenium and chromedp expect, without exposing
the plaintext debugging port. Actions (goto, click, type, key) drive it directly.

Every subcommand targets the current sandbox unless --sandbox names another.`,
	}
	cmd.PersistentFlags().StringVar(&sandbox, "sandbox", "", "Sandbox name or id (default: the current sandbox)")
	cmd.AddCommand(
		newBrowserForwardCmd(),
		newBrowserStartCmd(&sandbox),
		newBrowserStopCmd(&sandbox),
		newBrowserStatusCmd(&sandbox),
		newBrowserOpenCmd(&sandbox),
		newBrowserShareCmd(&sandbox),
		newBrowserRevokeCmd(&sandbox),
		newBrowserCDPCmd(&sandbox),
		newBrowserScreenshotCmd(&sandbox),
		newBrowserGotoCmd(&sandbox),
		newBrowserClickCmd(&sandbox),
		newBrowserTypeCmd(&sandbox),
		newBrowserKeyCmd(&sandbox),
		newBrowserDevtoolsCmd(&sandbox),
	)
	return cmd
}

func newBrowserForwardCmd() *cobra.Command {
	var port int
	var bind string
	cmd := &cobra.Command{
		Use:   "forward <sandbox_id>",
		Short: "Forward a sandbox's CDP endpoint to a local port",
		Long: `Mint a scoped, expiring CDP grant for the sandbox's live browser and serve it
on 127.0.0.1:<port>. It proxies CDP discovery (/json/*) to the cdp host and the
/devtools/* WebSocket the client then dials, rewriting webSocketDebuggerUrl to
the local listener. The grant stays in the host label, so no plaintext
debugging port is ever exposed.

Then point a tool at the local port:

  Selenium:  options.add_experimental_option("debuggerAddress", "127.0.0.1:9222")
  chromedp:  chromedp.NewRemoteAllocator(ctx, "http://127.0.0.1:9222")

  miosa browser forward my-box
  miosa browser forward my-box --port 9333`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error { return runBrowserForward(cmd, args[0], bind, port) },
	}
	cmd.Flags().IntVar(&port, "port", 9222, "Local port to listen on")
	cmd.Flags().StringVar(&bind, "bind", "127.0.0.1", "Local address to listen on")
	return cmd
}

func runBrowserForward(cmd *cobra.Command, ref, bind string, port int) error {
	c, cfg, err := buildClient()
	if err != nil {
		return die(err)
	}
	ref, err = requireSandbox(ref, cfg.CurrentSandbox)
	if err != nil {
		return usagef("%v", err)
	}

	ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	id, err := c.API.ResolveSandbox(ctx, ref)
	if err != nil {
		return die(err)
	}

	grant, err := c.BrowserCDPURL(ctx, id)
	if err != nil {
		return die(err)
	}
	if grant.CdpHostHTTPURL == "" || grant.CdpHostWSURL == "" {
		return die(fmt.Errorf("the control plane did not return a CDP host for %s (browser state: %s)", ref, grant.State))
	}

	p := printerFor(cmd)
	addr := net.JoinHostPort(bind, strconv.Itoa(port))
	p.Line("Forwarding CDP for %s to %s", ref, addr)
	p.Line("Selenium debuggerAddress: %s", addr)
	p.Line("chromedp base: http://%s", addr)
	if grant.CdpExpiresAt != "" {
		p.Line("Grant expires at %s; re-run to mint a new one.", grant.CdpExpiresAt)
	}
	p.Line("Press Ctrl-C to stop.")

	return cdpforward.Serve(ctx, cdpforward.Options{
		HTTPBase: grant.CdpHostHTTPURL,
		WSBase:   grant.CdpHostWSURL,
		Bind:     bind,
		Port:     port,
		Logf:     func(format string, a ...any) { p.Line(format, a...) },
	})
}

// browserSandbox builds an authenticated client and resolves the target
// sandbox from --sandbox, falling back to the current sandbox. ref is the name
// the user knows; id is the resolved id used in request paths.
func browserSandbox(cmd *cobra.Command, flagRef string) (c *client.Client, ref, id string, err error) {
	c, cfg, err := buildClient()
	if err != nil {
		return nil, "", "", die(err)
	}
	ref, err = requireSandbox(flagRef, cfg.CurrentSandbox)
	if err != nil {
		return nil, "", "", usagef("%v", err)
	}
	id, err = c.API.ResolveSandbox(cmd.Context(), ref)
	if err != nil {
		return nil, "", "", die(err)
	}
	return c, ref, id, nil
}

func viewportString(v client.BrowserViewport) string {
	if v.Width == 0 && v.Height == 0 {
		return ""
	}
	return fmt.Sprintf("%dx%d", v.Width, v.Height)
}

func externalLine(m map[string]any) string {
	b, err := json.Marshal(m)
	if err != nil {
		return fmt.Sprintf("%v", m)
	}
	return string(b)
}

func newBrowserStartCmd(sandbox *string) *cobra.Command {
	var ttl int
	cmd := &cobra.Command{
		Use:   "start",
		Short: "Start the sandbox's live browser",
		Long: `Start the sandbox's browser lazily and print its state and live view URL. A
stopped sandbox answers SANDBOX_NOT_RUNNING; resume it first. Needs the
sandboxes:write scope.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, ref, id, err := browserSandbox(cmd, *sandbox)
			if err != nil {
				return err
			}
			conn, err := c.BrowserStart(cmd.Context(), id, ttl)
			if err != nil {
				return die(err)
			}
			p := printerFor(cmd)
			if isJSON() {
				return p.JSON(conn)
			}
			p.Fields([][2]string{
				{"Sandbox", ref},
				{"State", conn.State},
				{"Epoch", strconv.Itoa(conn.Epoch)},
				{"Viewport", viewportString(conn.Viewport)},
				{"Live URL", conn.LiveURL},
			})
			return nil
		},
	}
	cmd.Flags().IntVar(&ttl, "ttl", 0, "Session lifetime in seconds (0 = server default)")
	return cmd
}

func newBrowserStopCmd(sandbox *string) *cobra.Command {
	return &cobra.Command{
		Use:   "stop",
		Short: "Stop the sandbox's live browser",
		Long: `Stop the sandbox's browser and print its state. Needs the sandboxes:write
scope.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, ref, id, err := browserSandbox(cmd, *sandbox)
			if err != nil {
				return err
			}
			st, err := c.BrowserStop(cmd.Context(), id)
			if err != nil {
				return die(err)
			}
			p := printerFor(cmd)
			if isJSON() {
				return p.JSON(st)
			}
			p.Fields([][2]string{{"Sandbox", ref}, {"State", st.State}})
			return nil
		},
	}
}

func newBrowserStatusCmd(sandbox *string) *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show the live browser state without starting it",
		Long: `Read the live browser state without starting anything. Prints the state, the
epoch, the viewport, and every external Chrome attached to the browser. Needs
the sandboxes:read scope.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, ref, id, err := browserSandbox(cmd, *sandbox)
			if err != nil {
				return err
			}
			st, err := c.BrowserStatus(cmd.Context(), id)
			if err != nil {
				return die(err)
			}
			p := printerFor(cmd)
			if isJSON() {
				return p.JSON(st)
			}
			p.Fields([][2]string{
				{"Sandbox", ref},
				{"State", st.State},
				{"Epoch", strconv.Itoa(st.Epoch)},
				{"Viewport", viewportString(st.Viewport)},
			})
			if st.State == "sandbox_not_running" {
				p.Line("The sandbox is not running; start it with 'miosa resume %s'.", ref)
			}
			if len(st.External) == 0 {
				p.Line("External: none")
			} else {
				p.Line("External:")
				for _, ext := range st.External {
					p.Line("  %s", externalLine(ext))
				}
			}
			return nil
		},
	}
}

func newBrowserOpenCmd(sandbox *string) *cobra.Command {
	var ttl int
	var printOnly bool
	cmd := &cobra.Command{
		Use:   "open",
		Short: "Start the live browser and open its view URL",
		Long: `Start the sandbox's browser lazily and open its live view URL in your browser.
Use --print to only print the URL. Needs the sandboxes:write scope.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, _, id, err := browserSandbox(cmd, *sandbox)
			if err != nil {
				return err
			}
			conn, err := c.BrowserStart(cmd.Context(), id, ttl)
			if err != nil {
				return die(err)
			}
			p := printerFor(cmd)
			if conn.LiveURL == "" {
				return die(fmt.Errorf("the control plane did not return a live URL (browser state: %s)", conn.State))
			}
			if isJSON() {
				return p.JSON(conn)
			}
			if printOnly {
				p.Line("%s", conn.LiveURL)
				return nil
			}
			if err := openBrowser(conn.LiveURL); err != nil {
				p.Line("%s", conn.LiveURL)
				return nil
			}
			p.Line("Opened %s", conn.LiveURL)
			return nil
		},
	}
	cmd.Flags().IntVar(&ttl, "ttl", 0, "Session lifetime in seconds (0 = server default)")
	cmd.Flags().BoolVar(&printOnly, "print", false, "Print the live URL instead of opening it")
	return cmd
}

func newBrowserShareCmd(sandbox *string) *cobra.Command {
	var ttl int
	var viewOnly bool
	cmd := &cobra.Command{
		Use:   "share",
		Short: "Create a view-only share link for the live browser",
		Long: `Create a view-only share link for the sandbox's live browser and print its
view URL, share id, and expiry. The server shares view-only mode; --view-only is
accepted because it is the only mode. Needs the sandboxes:write scope.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, ref, id, err := browserSandbox(cmd, *sandbox)
			if err != nil {
				return err
			}
			share, err := c.BrowserShare(cmd.Context(), id, ttl)
			if err != nil {
				return die(err)
			}
			p := printerFor(cmd)
			if isJSON() {
				return p.JSON(share)
			}
			p.Fields([][2]string{
				{"Sandbox", ref},
				{"View URL", share.ViewURL},
				{"Share ID", share.ShareID},
				{"Mode", share.Mode},
				{"Expires", share.ExpiresAt},
			})
			return nil
		},
	}
	cmd.Flags().IntVar(&ttl, "ttl", 0, "Share lifetime in seconds (0 = server default)")
	cmd.Flags().BoolVar(&viewOnly, "view-only", false, "View-only mode (the only mode the server shares)")
	return cmd
}

func newBrowserRevokeCmd(sandbox *string) *cobra.Command {
	var all bool
	var shareID string
	cmd := &cobra.Command{
		Use:   "revoke",
		Short: "Revoke share links for the live browser",
		Long: `Revoke either every share and CDP link (--all) or one share (--share-id ID).
Exactly one of the two is required. Needs the sandboxes:write scope.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if all == (shareID != "") {
				return usagef("exactly one of --all or --share-id is required")
			}
			c, _, id, err := browserSandbox(cmd, *sandbox)
			if err != nil {
				return err
			}
			p := printerFor(cmd)
			if all {
				res, err := c.BrowserRevokeAll(cmd.Context(), id)
				if err != nil {
					return die(err)
				}
				if isJSON() {
					return p.JSON(res)
				}
				p.Line("Revoked all shares and CDP links (epoch %d).", res.Epoch)
				return nil
			}
			res, err := c.BrowserRevokeOne(cmd.Context(), id, shareID)
			if err != nil {
				return die(err)
			}
			if isJSON() {
				return p.JSON(res)
			}
			p.Line("Revoked share %s.", res.ShareID)
			return nil
		},
	}
	cmd.Flags().BoolVar(&all, "all", false, "Revoke every share and CDP link")
	cmd.Flags().StringVar(&shareID, "share-id", "", "Revoke one share by id")
	return cmd
}

func newBrowserCDPCmd(sandbox *string) *cobra.Command {
	var ttl int
	cmd := &cobra.Command{
		Use:   "cdp",
		Short: "Mint a CDP grant for the live browser",
		Long: `Mint a scoped, expiring CDP grant and print its WebSocket URL and the cdp host
forms, plus Playwright and Puppeteer one-liners. Needs the sandboxes:write
scope. The cdp host carries the CDP token: do not log or share it.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, _, id, err := browserSandbox(cmd, *sandbox)
			if err != nil {
				return err
			}
			grant, err := c.BrowserCDPURLWithTTL(cmd.Context(), id, ttl)
			if err != nil {
				return die(err)
			}
			p := printerFor(cmd)
			if isJSON() {
				return p.JSON(grant)
			}
			if grant.CdpWsURL == "" && grant.CdpHostWSURL == "" {
				return die(fmt.Errorf("the control plane did not return a CDP endpoint (browser state: %s)", grant.State))
			}
			p.Fields([][2]string{
				{"State", grant.State},
				{"CDP WS URL", grant.CdpWsURL},
				{"CDP Host", grant.CdpHost},
				{"CDP Host HTTP", grant.CdpHostHTTPURL},
				{"CDP Host WS", grant.CdpHostWSURL},
				{"Expires", grant.CdpExpiresAt},
			})
			p.Line("")
			p.Line("Playwright: chromium.connectOverCDP(%q)", grant.CdpHostHTTPURL)
			p.Line("Puppeteer:  puppeteer.connect({ browserWSEndpoint: %q })", grant.CdpWsURL)
			p.Line("The cdp host carries the CDP token: do not log or share it.")
			return nil
		},
	}
	cmd.Flags().IntVar(&ttl, "ttl", 0, "Grant lifetime in seconds (0 = server default)")
	return cmd
}

func newBrowserScreenshotCmd(sandbox *string) *cobra.Command {
	var out string
	var format string
	var tab string
	cmd := &cobra.Command{
		Use:   "screenshot",
		Short: "Capture a screenshot of the live browser",
		Long: `Capture a screenshot of the sandbox's live browser. With -O the image is
written to a file (the path is required) and the path and size are printed;
without -O the raw image bytes go to stdout. The global -o selects the output
format, so this uses -O/--output-file. Needs the sandboxes:read scope.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, _, id, err := browserSandbox(cmd, *sandbox)
			if err != nil {
				return err
			}
			data, ctype, err := c.BrowserScreenshot(cmd.Context(), id, format, tab, "")
			if err != nil {
				return die(err)
			}
			if out == "" {
				_, werr := cmd.OutOrStdout().Write(data)
				return werr
			}
			path := out
			if filepath.Ext(path) == "" {
				path += imageExt(format, ctype)
			}
			if err := os.WriteFile(path, data, 0o644); err != nil {
				return die(err)
			}
			p := printerFor(cmd)
			p.Line("%s (%d bytes)", path, len(data))
			return nil
		},
	}
	cmd.Flags().StringVarP(&out, "output-file", "O", "", "Write the image to this file path")
	cmd.Flags().StringVar(&format, "format", "", "Image format: png or jpeg (default: server default)")
	cmd.Flags().StringVar(&tab, "tab", "", "Target tab id (default: the active tab)")
	return cmd
}

// imageExt picks a file extension from --format, falling back to the returned
// Content-Type, then png.
func imageExt(format, ctype string) string {
	switch strings.ToLower(format) {
	case "png":
		return ".png"
	case "jpeg", "jpg":
		return ".jpg"
	}
	lc := strings.ToLower(ctype)
	switch {
	case strings.Contains(lc, "png"):
		return ".png"
	case strings.Contains(lc, "jpeg"), strings.Contains(lc, "jpg"):
		return ".jpg"
	}
	return ".png"
}

func newBrowserGotoCmd(sandbox *string) *cobra.Command {
	return &cobra.Command{
		Use:   "goto <url>",
		Short: "Navigate the live browser to a URL",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runBrowserAction(cmd, *sandbox, map[string]any{"type": "navigate", "url": args[0]})
		},
	}
}

func newBrowserClickCmd(sandbox *string) *cobra.Command {
	return &cobra.Command{
		Use:   "click <x> <y>",
		Short: "Click at a point in the live browser",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			x, err := strconv.Atoi(args[0])
			if err != nil {
				return usagef("invalid x coordinate %q", args[0])
			}
			y, err := strconv.Atoi(args[1])
			if err != nil {
				return usagef("invalid y coordinate %q", args[1])
			}
			return runBrowserAction(cmd, *sandbox, map[string]any{"type": "click", "x": x, "y": y})
		},
	}
}

func newBrowserTypeCmd(sandbox *string) *cobra.Command {
	return &cobra.Command{
		Use:   "type <text>",
		Short: "Type text into the live browser",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runBrowserAction(cmd, *sandbox, map[string]any{"type": "type", "text": args[0]})
		},
	}
}

func newBrowserKeyCmd(sandbox *string) *cobra.Command {
	return &cobra.Command{
		Use:   "key <key>",
		Short: "Press a key in the live browser",
		Long: `Press a key or key combination, for example "Enter" or "ctrl+a". Needs the
sandboxes:write scope.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runBrowserAction(cmd, *sandbox, map[string]any{"type": "key", "key": args[0]})
		},
	}
}

// runBrowserAction sends one browser action and prints the daemon result.
func runBrowserAction(cmd *cobra.Command, sandbox string, action map[string]any) error {
	c, _, id, err := browserSandbox(cmd, sandbox)
	if err != nil {
		return err
	}
	res, err := c.BrowserAction(cmd.Context(), id, action)
	if err != nil {
		return die(err)
	}
	p := printerFor(cmd)
	if isJSON() {
		return p.JSON(res)
	}
	if res == nil {
		p.Line("ok")
		return nil
	}
	b, err := json.Marshal(res)
	if err != nil {
		p.Line("ok")
		return nil
	}
	p.Line("%s", b)
	return nil
}

func newBrowserDevtoolsCmd(sandbox *string) *cobra.Command {
	var port int
	var bind string
	cmd := &cobra.Command{
		Use:   "devtools",
		Short: "Forward CDP and open chrome://inspect",
		Long: `Forward the sandbox's CDP endpoint to a local port (default 9222, the port
chrome://inspect auto-discovers), open chrome://inspect/#devices, and block
until Ctrl-C. Needs the sandboxes:write scope.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return runBrowserDevtools(cmd, *sandbox, bind, port) },
	}
	cmd.Flags().IntVar(&port, "port", 9222, "Local port for the forwarded CDP endpoint")
	cmd.Flags().StringVar(&bind, "bind", "127.0.0.1", "Local address to listen on")
	return cmd
}

func runBrowserDevtools(cmd *cobra.Command, sandbox, bind string, port int) error {
	c, ref, id, err := browserSandbox(cmd, sandbox)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	grant, err := c.BrowserCDPURL(ctx, id)
	if err != nil {
		return die(err)
	}
	if grant.CdpHostHTTPURL == "" || grant.CdpHostWSURL == "" {
		return die(fmt.Errorf("the control plane did not return a CDP host for %s (browser state: %s)", ref, grant.State))
	}

	p := printerFor(cmd)
	addr := net.JoinHostPort(bind, strconv.Itoa(port))
	p.Line("Forwarding CDP for %s to %s", ref, addr)
	go func() {
		_ = cdpforward.Serve(ctx, cdpforward.Options{
			HTTPBase: grant.CdpHostHTTPURL,
			WSBase:   grant.CdpHostWSURL,
			Bind:     bind,
			Port:     port,
			Logf:     func(format string, a ...any) { p.Line(format, a...) },
		})
	}()

	target := "chrome://inspect/#devices"
	if err := openBrowser(target); err != nil {
		p.Line("Open %s in Chrome.", target)
	} else {
		p.Line("Opened %s.", target)
	}
	p.Line("chrome://inspect will list the forwarded targets on %s.", addr)
	p.Line("Press Ctrl-C to stop.")
	<-ctx.Done()
	return nil
}
