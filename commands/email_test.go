package commands_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEmailSendBuildsAResendShapedBody(t *testing.T) {
	f := fakeRoutes(t, map[string]any{
		"POST /email/send": `{"id":"msg-1","status":"sent"}`,
	})
	dir := t.TempDir()
	html := filepath.Join(dir, "body.html")
	attach := filepath.Join(dir, "report.pdf")
	if err := os.WriteFile(html, []byte("<p>hi</p>"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(attach, []byte("PDF"), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := run(t, "email", "send", "--from", "Hi <hi@mail.example.com>", "--to", "a@example.org,b@example.org",
		"--subject", "Report", "--html-file", html, "--attach", attach, "--tag", "kind=report", "--idempotency-key", "wo26-1", "--json")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	calls := f.find("POST", "/email/send")
	if len(calls) != 1 {
		t.Fatalf("calls = %+v", f.calls)
	}
	b := calls[0].Body
	to, _ := b["to"].([]any)
	atts, _ := b["attachments"].([]any)
	tags, _ := b["tags"].([]any)
	if b["from"] != "Hi <hi@mail.example.com>" || len(to) != 2 || b["html"] != "<p>hi</p>" || b["subject"] != "Report" {
		t.Fatalf("body = %v", b)
	}
	if len(atts) != 1 || atts[0].(map[string]any)["filename"] != "report.pdf" || atts[0].(map[string]any)["content"] != "UERG" {
		t.Fatalf("attachments = %v", atts)
	}
	if len(tags) != 1 || tags[0].(map[string]any)["name"] != "kind" {
		t.Fatalf("tags = %v", tags)
	}
	for _, leaked := range []string{"attach", "tag", "html_file", "text_file"} {
		if _, ok := b[leaked]; ok {
			t.Fatalf("%s must not be sent: %v", leaked, b)
		}
	}
	if got := calls[0].Headers.Get("Idempotency-Key"); got != "wo26-1" {
		t.Fatalf("Idempotency-Key = %q", got)
	}
}

func TestEmailSendGeneratesAnIdempotencyKey(t *testing.T) {
	f := fakeRoutes(t, map[string]any{"POST /email/send": `{"id":"msg-1","status":"sent"}`})
	if _, err := run(t, "email", "send", "--from", "a@mail.example.com", "--to", "b@example.org", "--subject", "s", "--text", "t"); err != nil {
		t.Fatal(err)
	}
	if got := f.find("POST", "/email/send")[0].Headers.Get("Idempotency-Key"); !strings.HasPrefix(got, "cli-") {
		t.Fatalf("Idempotency-Key = %q", got)
	}
}

func TestEmailDomainsAddPrintsTheRecordsToPublish(t *testing.T) {
	f := fakeRoutes(t, map[string]any{
		"POST /email/domains": resp{201, `{"data":{"id":"d1","name":"mail.example.com","status":"pending","records":[
			{"record":"SPF","type":"MX","name":"send","value":"feedback-smtp.example","status":"pending"},
			{"record":"DKIM","type":"TXT","name":"resend._domainkey","value":"p=MIG","status":"pending"},
			{"record":"DMARC","type":"TXT","name":"_dmarc.mail.example.com","value":"v=DMARC1; p=none","status":"pending"}]}}`},
	})
	out, err := run(t, "email", "domains", "add", "mail.example.com")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"SPF", "DKIM", "DMARC", "_dmarc.mail.example.com", "miosa email domains verify mail.example.com"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in:\n%s", want, out)
		}
	}
	if f.find("POST", "/email/domains")[0].Body["name"] != "mail.example.com" {
		t.Fatalf("body = %v", f.calls)
	}
}

func TestEmailDomainsVerifyAndMessagesUseTheDomainNameAndStatusFilter(t *testing.T) {
	f := fakeRoutes(t, map[string]any{
		"POST /email/domains/mail.example.com/verify": `{"data":{"id":"d1","name":"mail.example.com","status":"verified","verified_at":"2026-11-12T10:00:00Z","records":[]}}`,
		"GET /email/messages":                         `{"data":[{"id":"11111111-2222","status":"delivered","from":"a@mail.example.com","to":["b@example.org"],"subject":"s","created_at":"2026-11-12T10:00:00Z"}]}`,
	})
	out, err := run(t, "email", "domains", "verify", "mail.example.com")
	if err != nil || !strings.Contains(out, "verified") {
		t.Fatalf("err=%v out=%s", err, out)
	}
	out, err = run(t, "email", "messages", "--status", "delivered")
	if err != nil || !strings.Contains(out, "delivered") {
		t.Fatalf("err=%v out=%s", err, out)
	}
	if got := f.find("GET", "/email/messages")[0].Query; got != "status=delivered" {
		t.Fatalf("query = %q", got)
	}
}
