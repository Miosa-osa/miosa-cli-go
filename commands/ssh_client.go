package commands

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/Miosa-osa/miosa-cli-go/internal/api"
	"github.com/Miosa-osa/miosa-cli-go/internal/terminal"
)

// lookSSH finds the OpenSSH client and key generator. Tests replace it.
var lookSSH = func() (ssh, keygen string, ok bool) {
	s, err1 := exec.LookPath("ssh")
	k, err2 := exec.LookPath("ssh-keygen")
	return s, k, err1 == nil && err2 == nil
}

func init() {
	rootCmd.AddCommand(&cobra.Command{
		Use:    "ssh-tunnel <url>",
		Hidden: true,
		Short:  "Bridge standard input and output to a sandbox's SSH tunnel (an ssh ProxyCommand)",
		Args:   cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, _, err := buildClient()
			if err != nil {
				return die(err)
			}
			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			conn, err := terminal.DialStream(ctx, args[0], c.Key)
			if err != nil {
				return die(err)
			}
			defer conn.Close()
			if err := terminal.BridgeStdio(ctx, conn, cmd.InOrStdin(), cmd.OutOrStdout()); err != nil {
				return die(err)
			}
			return nil
		},
	})
}

// sshCertificate is the data of POST /sandboxes/:id/ssh-certificates.
type sshCertificate struct {
	Certificate string `json:"certificate"`
	User        string `json:"user"`
	Principal   string `json:"principal"`
	TunnelURL   string `json:"tunnel_url"`
}

// runSSHClient opens a real ssh session to a sandbox. It makes an ephemeral
// ed25519 key, asks the API for a short-lived certificate for it (or, where the
// server has no certificate authority, installs the public key), and runs the
// system ssh through the authenticated tunnel. Nothing it creates outlives the
// command: the key, certificate and known_hosts live in a private temp dir.
func runSSHClient(cmd *cobra.Command, ref, command string, tty bool, sshBin, keygenBin string) error {
	c, cfg, err := buildClient()
	if err != nil {
		return die(err)
	}
	ref, err = requireSandbox(ref, cfg.CurrentSandbox)
	if err != nil {
		return usagef("%v", err)
	}
	id, err := c.API.ResolveSandbox(cmd.Context(), ref)
	if err != nil {
		return die(err)
	}

	dir, err := os.MkdirTemp("", "miosa-ssh-")
	if err != nil {
		return die(fmt.Errorf("creating a private directory for the ssh key: %w", err))
	}
	defer os.RemoveAll(dir)
	if err := os.Chmod(dir, 0o700); err != nil {
		return die(err)
	}
	keyPath := filepath.Join(dir, "id_ed25519")
	gen := exec.CommandContext(cmd.Context(), keygenBin, "-q", "-t", "ed25519", "-N", "", "-C", "miosa-ephemeral", "-f", keyPath)
	if out, err := gen.CombinedOutput(); err != nil {
		return die(fmt.Errorf("ssh-keygen failed: %v: %s", err, strings.TrimSpace(string(out))))
	}
	pubBytes, err := os.ReadFile(keyPath + ".pub")
	if err != nil {
		return die(fmt.Errorf("reading the public key: %w", err))
	}
	pub := strings.TrimSpace(string(pubBytes))

	tunnelURL, err := terminal.WebSocketURL(c.BaseURL, "/sandboxes/"+id+"/ssh-tunnel")
	if err != nil {
		return die(err)
	}
	user := "root"
	certFile := ""

	var cert struct {
		Data sshCertificate `json:"data"`
	}
	certErr := c.API.Post(cmd.Context(), "/sandboxes/"+id+"/ssh-certificates", map[string]string{"public_key": pub}, &cert)
	switch {
	case certErr == nil:
		if cert.Data.Certificate == "" {
			return die(errors.New("the server returned no certificate"))
		}
		certFile = keyPath + "-cert.pub"
		if err := os.WriteFile(certFile, []byte(strings.TrimSpace(cert.Data.Certificate)+"\n"), 0o600); err != nil {
			return die(err)
		}
		if cert.Data.TunnelURL != "" {
			tunnelURL = cert.Data.TunnelURL
		}
		if cert.Data.User != "" {
			user = cert.Data.User
		}
	case api.IsStatus(certErr, http.StatusServiceUnavailable), api.IsStatus(certErr, http.StatusNotFound), api.IsStatus(certErr, http.StatusMethodNotAllowed):
		// No certificate authority on this server (503), or an older server
		// without the route: authorize the key in the sandbox instead.
		if err := c.API.Post(cmd.Context(), "/sandboxes/"+id+"/ssh-keys", map[string]string{"public_key": pub}, nil); err != nil {
			return die(err)
		}
	default:
		return die(certErr)
	}

	self, err := selfPath()
	if err != nil {
		return die(fmt.Errorf("finding the miosa binary for the ssh tunnel: %w", err))
	}
	args := []string{
		"-i", keyPath,
		"-o", "IdentitiesOnly=yes",
		"-o", "BatchMode=yes",
		"-o", "StrictHostKeyChecking=no",
		"-o", "UserKnownHostsFile=" + filepath.Join(dir, "known_hosts"),
		"-o", "LogLevel=ERROR",
		"-o", "ProxyCommand=" + shellWord(self) + " ssh-tunnel " + shellWord(tunnelURL),
	}
	if certFile != "" {
		args = append(args, "-o", "CertificateFile="+certFile)
	}
	in := cmd.InOrStdin()
	switch {
	case tty:
		args = append(args, "-t")
	case !stdinIsTerminal(cmd):
		args = append(args, "-T") // piped input: no terminal on the far side
	}
	args = append(args, user+"@sandbox-"+id)
	if command != "" {
		args = append(args, command)
	}

	sshCmd := exec.Command(sshBin, args...)
	sshCmd.Stdin, sshCmd.Stdout, sshCmd.Stderr = in, cmd.OutOrStdout(), cmd.ErrOrStderr()
	// The tunnel helper reads the key and server from the environment, so the
	// secret never appears in a command line.
	sshCmd.Env = append(os.Environ(), "MIOSA_API_KEY="+c.Key, "MIOSA_BASE_URL="+c.BaseURL)

	sigs := make(chan os.Signal, 4)
	signal.Notify(sigs, os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	defer signal.Stop(sigs)
	if err := sshCmd.Start(); err != nil {
		return die(fmt.Errorf("starting ssh: %w", err))
	}
	done := make(chan error, 1)
	go func() { done <- sshCmd.Wait() }()
	for {
		select {
		case s := <-sigs:
			_ = sshCmd.Process.Signal(s) // ssh decides; we only make sure the temp dir is removed after it exits
		case err := <-done:
			var ee *exec.ExitError
			switch {
			case err == nil:
				return nil
			case errors.As(err, &ee):
				return &ExitError{Code: ee.ExitCode()}
			default:
				return die(err)
			}
		}
	}
}

// shellWord quotes one word for the shell that runs a ProxyCommand.
func shellWord(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
