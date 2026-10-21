package commands

import (
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

func init() { rootCmd.AddCommand(emailCmd()) }

var emailDomainCols = []Col{
	{Head: "ID", Path: "id", Fmt: "short"}, {Head: "DOMAIN", Path: "name"}, {Head: "STATUS", Path: "status"},
	{Head: "DMARC", Path: "dmarc_status"}, {Head: "CREATED", Path: "created_at", Fmt: "age"},
}

var emailDomainDetail = []Col{
	{Head: "Id", Path: "id"}, {Head: "Domain", Path: "name"}, {Head: "Status", Path: "status"},
	{Head: "DMARC", Path: "dmarc_status"}, {Head: "Verified", Path: "verified_at"},
}

var emailMessageCols = []Col{
	{Head: "ID", Path: "id", Fmt: "short"}, {Head: "STATUS", Path: "status"}, {Head: "FROM", Path: "from"},
	{Head: "TO", Path: "to", Fmt: "list"}, {Head: "SUBJECT", Path: "subject"}, {Head: "SENT", Path: "created_at", Fmt: "age"},
}

// emailCmd is the tenant email API: sending domains, sending, delivery state and the
// suppression list. Sending is refused from a domain that is not verified.
func emailCmd() *cobra.Command {
	cmd := group("email", "Send email from your apps through MIOSA",
		`Add a sending domain, publish the DNS records it returns (SPF, DKIM, DMARC),
verify it, then send from any address on it. Bounces and complaints add the
recipient to the organization's suppression list and are sent to your webhooks
in the email category (email.delivered, email.bounced, email.complained, ...).

  miosa email domains add mail.example.com
  miosa email domains verify mail.example.com
  miosa email send --from hello@mail.example.com --to you@example.org --subject Hi --text "Hello"
  miosa email messages

A key that may only send is minted with the email:send scope.`,
		[]string{"mail"},
		Op{
			Use: "send", Short: "Send an email from a verified domain", Method: "POST", Path: "/email/send",
			Idempotent: true, FileBody: true,
			Example: `  miosa email send --from hello@mail.example.com --to you@example.org --subject "Hi" --text "Hello"
  miosa email send --from hello@mail.example.com --to a@example.org,b@example.org --subject "Report" --html-file report.html --attach report.pdf
  miosa email send --file message.json --idempotency-key order-1042`,
			Flags: []Flag{
				{Name: "from", Usage: "Sender address on a verified domain, 'Name <addr>' allowed"},
				{Name: "to", Type: "strings", Usage: "Recipient (repeat or comma-separate)"},
				{Name: "cc", Type: "strings", Usage: "Cc recipient"},
				{Name: "bcc", Type: "strings", Usage: "Bcc recipient"},
				{Name: "reply-to", Type: "strings", Usage: "Reply-To address"},
				{Name: "subject", Usage: "Subject line"},
				{Name: "text", Usage: "Plain-text body"},
				{Name: "html", Usage: "HTML body"},
				{Name: "text-file", Usage: "Read the plain-text body from a file", In: "body", Field: "text_file"},
				{Name: "html-file", Usage: "Read the HTML body from a file", In: "body", Field: "html_file"},
				{Name: "attach", Type: "strings", Usage: "Attach a file (repeat; 10 MB in total)", In: "body", Field: "attach"},
				{Name: "tag", Type: "kv", Usage: "Tag as NAME=VALUE (repeat)", In: "body", Field: "tag"},
			},
			Body:   emailSendBody,
			Detail: []Col{{Head: "Id", Path: "id"}, {Head: "Status", Path: "status"}, {Head: "Replayed", Path: "replayed"}},
		},
		Op{Use: "messages", Aliases: []string{"ls"}, Short: "List sent messages and their delivery state", Method: "GET", Path: "/email/messages",
			Cols: emailMessageCols,
			Flags: []Flag{
				{Name: "status", Usage: "Only messages in this state: queued, sent, delivered, delayed, bounced, complained or failed"},
				{Name: "limit", Type: "int", Usage: "How many to return (default 50, at most 200)"},
			}},
		Op{Use: "message <id>", Aliases: []string{"show"}, Short: "Show one message with its delivery events", Method: "GET", Path: "/email/messages/{0}",
			Args: []Arg{{Name: "id"}}, Detail: []Col{
				{Head: "Id", Path: "id"}, {Head: "Status", Path: "status"}, {Head: "From", Path: "from"}, {Head: "To", Path: "to", Fmt: "list"},
				{Head: "Subject", Path: "subject"}, {Head: "Attachments", Path: "attachment_count"}, {Head: "Error", Path: "error"},
				{Head: "Events", Path: "events", Fmt: "json"},
			}},
		Op{Use: "limits", Short: "Daily cap, sent today and whether sending is paused", Method: "GET", Path: "/email/limits",
			Detail: []Col{
				{Head: "Daily cap", Path: "daily_cap"}, {Head: "Sent today", Path: "sent_today"},
				{Head: "Per minute", Path: "per_minute_cap"}, {Head: "Paused", Path: "paused"}, {Head: "Reason", Path: "paused_reason"},
			}},
		Op{Use: "suppressions", Aliases: []string{"suppressed"}, Short: "List addresses mail is not sent to", Method: "GET", Path: "/email/suppressions",
			Cols: []Col{{Head: "ID", Path: "id", Fmt: "short"}, {Head: "EMAIL", Path: "email"}, {Head: "REASON", Path: "reason"}, {Head: "ADDED", Path: "created_at", Fmt: "age"}}},
		Op{Use: "suppress <email>", Short: "Never send to an address", Method: "POST", Path: "/email/suppressions",
			Args: []Arg{{Name: "email"}},
			Body: func(args []string, body map[string]any) (map[string]any, error) {
				body["email"] = args[0]
				return body, nil
			},
			Detail: []Col{{Head: "Id", Path: "id"}, {Head: "Email", Path: "email"}, {Head: "Reason", Path: "reason"}}},
		Op{Use: "unsuppress <id>", Short: "Remove an address from the suppression list", Method: "DELETE", Path: "/email/suppressions/{0}",
			Args: []Arg{{Name: "id"}}, Done: "Removed {0}"},
	)
	cmd.AddCommand(emailDomainsCmd())
	return cmd
}

func emailDomainsCmd() *cobra.Command {
	return group("domains", "Sending domains and their DNS records",
		`'add' prints the SPF, DKIM and DMARC records to publish. After they resolve, 'verify'
checks them; mail can be sent only from a verified domain. <domain> is a domain name or an id.`,
		nil,
		Op{Use: "list", Aliases: []string{"ls"}, Short: "List sending domains", Method: "GET", Path: "/email/domains", Cols: emailDomainCols},
		Op{
			Use: "add <domain>", Short: "Add a sending domain and print the DNS records to publish", Method: "POST", Path: "/email/domains",
			Args: []Arg{{Name: "domain"}},
			Body: func(args []string, body map[string]any) (map[string]any, error) {
				body["name"] = args[0]
				return body, nil
			},
			Detail: emailDomainDetail,
			Human:  renderDomain,
		},
		Op{
			Use: "verify <domain>", Short: "Check the published DNS records", Method: "POST", Path: "/email/domains/{0}/verify",
			Args:   []Arg{{Name: "domain"}},
			Detail: emailDomainDetail,
			Human:  renderDomain,
		},
		Op{
			Use: "records <domain>", Short: "Show a domain's DNS records and their state", Method: "GET", Path: "/email/domains/{0}",
			Args: []Arg{{Name: "domain"}}, Detail: emailDomainDetail, Human: renderDomain,
		},
		Op{
			Use: "rm <domain>", Aliases: []string{"delete"}, Short: "Remove a sending domain", Method: "DELETE", Path: "/email/domains/{0}",
			Args:    []Arg{{Name: "domain"}},
			Confirm: "Remove sending domain {0}? Mail from it stops.", Done: "Removed {0}",
		},
	)
}

// emailSendBody turns the file and tag flags into the request's attachments, bodies and tags.
func emailSendBody(_ []string, body map[string]any) (map[string]any, error) {
	for _, pair := range [][2]string{{"text_file", "text"}, {"html_file", "html"}} {
		if path, ok := body[pair[0]].(string); ok {
			raw, err := os.ReadFile(path)
			if err != nil {
				return nil, fmt.Errorf("reading --%s: %w", strings.ReplaceAll(pair[0], "_", "-"), err)
			}
			body[pair[1]] = string(raw)
		}
		delete(body, pair[0])
	}
	if paths, ok := body["attach"].([]string); ok {
		attachments := make([]map[string]any, 0, len(paths))
		for _, path := range paths {
			raw, err := os.ReadFile(path)
			if err != nil {
				return nil, fmt.Errorf("reading --attach %s: %w", path, err)
			}
			attachments = append(attachments, map[string]any{
				"filename": filepath.Base(path),
				"content":  base64.StdEncoding.EncodeToString(raw),
			})
		}
		body["attachments"] = attachments
	}
	delete(body, "attach")
	if tags, ok := body["tag"].(map[string]any); ok {
		list := make([]map[string]any, 0, len(tags))
		for name, value := range tags {
			list = append(list, map[string]any{"name": name, "value": value})
		}
		body["tags"] = list
	}
	delete(body, "tag")
	return body, nil
}

// renderDomain prints a domain with the DNS records to publish and where each stands.
func renderDomain(cmd *cobra.Command, _ []string, resp any) error {
	domain, _ := unwrapObject(resp).(map[string]any)
	p := printerFor(cmd)
	if domain == nil {
		return p.JSON(resp)
	}
	p.Fields(nonEmpty([][2]string{
		{"Domain", formatValue(domain["name"], "")},
		{"Status", formatValue(domain["status"], "")},
		{"Verified", formatValue(domain["verified_at"], "")},
	}))
	records, _ := domain["records"].([]any)
	if len(records) == 0 {
		return nil
	}
	rows := make([][]string, 0, len(records))
	for _, r := range records {
		rec, _ := r.(map[string]any)
		rows = append(rows, []string{
			formatValue(rec["record"], ""), formatValue(rec["type"], ""), formatValue(rec["name"], ""),
			formatValue(rec["value"], ""), formatValue(rec["status"], ""),
		})
	}
	p.Line("")
	p.Table([]string{"RECORD", "TYPE", "NAME", "VALUE", "STATUS"}, rows)
	if domain["status"] != "verified" {
		p.Line("")
		p.Line("Publish these records, then run: miosa email domains verify %s", formatValue(domain["name"], ""))
	}
	return nil
}
