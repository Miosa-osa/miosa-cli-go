package commands_test

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gorilla/websocket"

	"github.com/Miosa-osa/miosa-cli-go/commands"
)

const fakeKeygen = `#!/bin/bash
# ssh-keygen -q -t ed25519 -N "" -C label -f PATH
while [ $# -gt 0 ]; do [ "$1" = "-f" ] && f="$2"; shift; done
echo "PRIVATE-KEY" > "$f"; chmod 600 "$f"
echo "ssh-ed25519 AAAAFAKEKEY miosa-ephemeral" > "$f.pub"
`

// The fake ssh records how it was called and what was on its files, because the
// temp dir is gone once miosa returns.
const fakeSSH = `#!/bin/bash
log="$FAKE_SSH_LOG"
printf '%s\n' "$@" > "$log.args"
cat > "$log.stdin"
key=""; cert=""
while [ $# -gt 0 ]; do
  case "$1" in
    -i) key="$2"; shift;;
    -o) case "$2" in CertificateFile=*) cert="${2#CertificateFile=}";; esac; shift;;
  esac
  shift
done
echo "$key" > "$log.key"
stat -f %Lp "$key" > "$log.mode" 2>/dev/null || stat -c %a "$key" > "$log.mode"
[ -n "$cert" ] && cp "$cert" "$log.cert"
echo "hello from ssh"
exit ${FAKE_SSH_EXIT:-0}
`

func installFakeSSH(t *testing.T) (log string) {
	t.Helper()
	dir := t.TempDir()
	sshPath, keygenPath := filepath.Join(dir, "ssh"), filepath.Join(dir, "ssh-keygen")
	for p, body := range map[string]string{sshPath: fakeSSH, keygenPath: fakeKeygen} {
		if err := os.WriteFile(p, []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	log = filepath.Join(dir, "log")
	t.Setenv("FAKE_SSH_LOG", log)
	t.Cleanup(commands.SetSSHToolsForTest(sshPath, keygenPath))
	t.Cleanup(commands.SetSelfPathForTest("/opt/my tools/miosa"))
	return log
}

func readFile(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("%v", err)
	}
	return string(b)
}

func TestSSHUsesAShortLivedCertificateAndKeepsStdinWorking(t *testing.T) {
	log := installFakeSSH(t)
	f := fakeRoutes(t, map[string]any{
		"POST /sandboxes/" + boxID + "/ssh-certificates": resp{201, `{"data":{"certificate":"ssh-ed25519-cert-v01@openssh.com AAAACERT","user":"root","principal":"sandbox-` + boxID + `","tunnel_url":"wss://api.example/api/v1/sandboxes/` + boxID + `/ssh-tunnel"}}`},
	})
	out, errOut, err := runSplitIn(t, "echo hi\n", "ssh", boxID, "--", "bash", "-s")
	if err != nil || !strings.Contains(out, "hello from ssh") {
		t.Fatalf("err=%v out=%q err=%q", err, out, errOut)
	}
	b := f.find("POST", "/sandboxes/"+boxID+"/ssh-certificates")[0].Body
	if b["public_key"] != "ssh-ed25519 AAAAFAKEKEY miosa-ephemeral" {
		t.Fatalf("body = %v", b)
	}
	if len(f.find("POST", "/sandboxes/"+boxID+"/ssh-keys")) != 0 {
		t.Fatal("a certificate must not also install a key")
	}
	args := readFile(t, log+".args")
	for _, want := range []string{
		"-T", "IdentitiesOnly=yes", "BatchMode=yes", "StrictHostKeyChecking=no", "root@sandbox-" + boxID,
		"CertificateFile=", "ProxyCommand='/opt/my tools/miosa' ssh-tunnel 'wss://api.example/api/v1/sandboxes/" + boxID + "/ssh-tunnel'",
	} {
		if !strings.Contains(args, want) {
			t.Errorf("ssh args lack %q:\n%s", want, args)
		}
	}
	if !strings.HasSuffix(strings.TrimSpace(args), "bash -s") {
		t.Errorf("the command is the last argument:\n%s", args)
	}
	if got := readFile(t, log+".stdin"); got != "echo hi\n" {
		t.Errorf("stdin must reach ssh unchanged: %q", got)
	}
	if got := strings.TrimSpace(readFile(t, log+".mode")); got != "600" {
		t.Errorf("the private key must be 0600, was %s", got)
	}
	if got := readFile(t, log+".cert"); got != "ssh-ed25519-cert-v01@openssh.com AAAACERT\n" {
		t.Errorf("certificate file = %q", got)
	}
	key := strings.TrimSpace(readFile(t, log+".key"))
	if _, err := os.Stat(filepath.Dir(key)); !os.IsNotExist(err) {
		t.Errorf("the temp dir must be removed after ssh ends: %v", err)
	}
}

func TestSSHFallsBackToInstallingTheKeyWhenThereIsNoCA(t *testing.T) {
	log := installFakeSSH(t)
	f := fakeRoutes(t, map[string]any{
		"POST /sandboxes/" + boxID + "/ssh-certificates": resp{503, `{"error":{"code":"SSH_CA_UNAVAILABLE","message":"SSH certificates are not available right now"}}`},
		"POST /sandboxes/" + boxID + "/ssh-keys":         resp{204, ``},
	})
	if _, _, err := runSplitIn(t, "", "ssh", boxID, "--", "uname"); err != nil {
		t.Fatal(err)
	}
	keys := f.find("POST", "/sandboxes/"+boxID+"/ssh-keys")
	if len(keys) != 1 || keys[0].Body["public_key"] != "ssh-ed25519 AAAAFAKEKEY miosa-ephemeral" {
		t.Fatalf("the public key must be installed: %+v", f.calls)
	}
	args := readFile(t, log+".args")
	if strings.Contains(args, "CertificateFile") {
		t.Errorf("no certificate was issued:\n%s", args)
	}
	if !strings.Contains(args, "ssh-tunnel 'ws") || !strings.Contains(args, "/sandboxes/"+boxID+"/ssh-tunnel'") {
		t.Errorf("the tunnel URL is derived from the API URL:\n%s", args)
	}
}

func TestSSHOtherCertificateErrorsAreReportedNotHidden(t *testing.T) {
	installFakeSSH(t)
	f := fakeRoutes(t, map[string]any{
		"POST /sandboxes/" + boxID + "/ssh-certificates": resp{409, `{"error":{"code":"SANDBOX_NOT_RUNNING","message":"sandbox must be running"}}`},
	})
	_, _, err := runSplitIn(t, "", "ssh", boxID, "--", "uname")
	if err == nil || !strings.Contains(err.Error(), "SANDBOX_NOT_RUNNING") {
		t.Fatalf("err = %v", err)
	}
	if len(f.find("POST", "/sandboxes/"+boxID+"/ssh-keys")) != 0 {
		t.Fatal("only a missing CA falls back to installing a key")
	}
}

func TestSSHExitsWithTheRemoteStatus(t *testing.T) {
	installFakeSSH(t)
	t.Setenv("FAKE_SSH_EXIT", "7")
	fakeRoutes(t, map[string]any{
		"POST /sandboxes/" + boxID + "/ssh-certificates": resp{201, `{"data":{"certificate":"ssh-ed25519-cert-v01@openssh.com AAAACERT"}}`},
	})
	_, _, err := runSplitIn(t, "", "ssh", boxID, "--", "false")
	if err == nil || commands.ExitCode(err) != 7 {
		t.Fatalf("err = %v exit = %d", err, commands.ExitCode(err))
	}
}

func TestSSHAPIFlagKeepsTheOldBehaviour(t *testing.T) {
	installFakeSSH(t)
	f := fakeRoutes(t, map[string]any{
		"POST /sandboxes/" + boxID + "/exec": `{"stdout":"from api\n","exit_code":0}`,
	})
	out, err := runIn(t, "echo hi\n", "ssh", boxID, "--api", "--", "bash", "-s")
	if err != nil || !strings.Contains(out, "from api") {
		t.Fatalf("err=%v out=%q calls=%+v", err, out, f.calls)
	}
	if len(f.find("POST", "/sandboxes/"+boxID+"/ssh-certificates")) != 0 {
		t.Fatal("--api must not touch certificates")
	}
}

func TestSSHTunnelBridgesStdioToTheWebSocket(t *testing.T) {
	up := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	var auth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		conn, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		for {
			mt, msg, err := conn.ReadMessage()
			if err != nil {
				return
			}
			if err := conn.WriteMessage(mt, append([]byte("echo:"), msg...)); err != nil {
				return
			}
		}
	}))
	defer srv.Close()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("MIOSA_API_KEY", "msk_u_tunnelkey")
	t.Setenv("MIOSA_BASE_URL", srv.URL)
	out, err := runIn(t, "SSH-2.0-test\n", "ssh-tunnel", "ws"+strings.TrimPrefix(srv.URL, "http")+"/api/v1/sandboxes/x/ssh-tunnel")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !strings.Contains(out, "echo:SSH-2.0-test") {
		t.Fatalf("out = %q", out)
	}
	if auth != "Bearer msk_u_tunnelkey" {
		t.Fatalf("the key goes in the Authorization header, got %q", auth)
	}
}
