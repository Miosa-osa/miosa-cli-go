package commands

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

const proveHTTPTimeout = 10 * time.Second

// proveAfter adds the client-side HTTP check to the platform's proof, then
// applies the exit-status rule. The platform's checks are about its own rows and
// routes; only a request to the public URL shows what a visitor gets.
func proveAfter(cmd *cobra.Command, resp any) error {
	if proof, _ := unwrapObject(resp).(map[string]any); proof != nil {
		addPublicURLCheck(cmd, proof)
	}
	return proveExitStatus(cmd, resp)
}

// addPublicURLCheck requests the deployment's public URL and appends a
// public_url_http check: the status the visitor sees and, for https, that the
// certificate verified and how long it has left. A status below 500 counts as
// serving (an app behind a login answers 401 and is still up); a transport or
// TLS error, or a 5xx, fails the check and the whole proof.
func addPublicURLCheck(cmd *cobra.Command, proof map[string]any) {
	url, _ := proof["public_url"].(string)
	if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
		return
	}
	checks, _ := proof["checks"].([]any)
	for _, c := range checks {
		if check, _ := c.(map[string]any); check["id"] == "public_url_http" {
			return
		}
	}

	ok, message, details := probePublicURL(cmd, url)
	check := map[string]any{"id": "public_url_http", "ok": ok, "message": message, "details": details}
	proof["checks"] = append(checks, check)
	if !ok {
		proof["ok"] = false
		actions, _ := proof["next_actions"].([]any)
		proof["next_actions"] = append(actions, "Open "+url+" in a browser; check the deployment logs and its domain's certificate.")
	}
}

func probePublicURL(cmd *cobra.Command, url string) (bool, string, map[string]any) {
	details := map[string]any{"url": url}
	req, err := http.NewRequestWithContext(cmd.Context(), http.MethodGet, url, nil)
	if err != nil {
		return false, fmt.Sprintf("%s is not a valid URL: %v", url, err), details
	}
	req.Header.Set("User-Agent", "miosa-cli deploy prove")
	client := &http.Client{Timeout: proveHTTPTimeout}
	started := time.Now()
	res, err := client.Do(req)
	if err != nil {
		details["error"] = err.Error()
		return false, fmt.Sprintf("GET %s failed: %v", url, err), details
	}
	defer res.Body.Close()
	details["status"] = res.StatusCode
	details["ms"] = time.Since(started).Milliseconds()

	tlsNote := ""
	if res.TLS != nil && len(res.TLS.PeerCertificates) > 0 {
		expires := res.TLS.PeerCertificates[0].NotAfter
		days := int(time.Until(expires).Hours() / 24)
		details["tls_expires"] = expires.UTC().Format(time.RFC3339)
		tlsNote = fmt.Sprintf(", TLS valid for %d more days", days)
	}
	ok := res.StatusCode < 500
	return ok, fmt.Sprintf("GET %s answered %d in %d ms%s", url, res.StatusCode, details["ms"], tlsNote), details
}
