package commands

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

const domainAddReply = `{"data":{"id":"d1","hostname":"www.example.com","dns_records":[
 {"type":"CNAME","name":"www","value":"proxy.miosa.ai","purpose":"traffic"},
 {"type":"TXT","name":"_miosa-verify","fqdn":"_miosa-verify.www.example.com","value":"miosa-domain-abc","purpose":"verification"}]}}`

const apexReply = `{"data":{"id":"d2","hostname":"example.com","dns_records":[
 {"type":"A","name":"@","value":"203.0.113.7","purpose":"traffic"},
 {"type":"ALIAS","name":"@","value":"proxy.miosa.ai","purpose":"traffic","optional":true},
 {"type":"TXT","name":"_miosa-verify","fqdn":"_miosa-verify.example.com","value":"tok","purpose":"verification"}]}}`

func TestZoneLinesAreWhatYouPaste(t *testing.T) {
	recs := parseDNSRecords(mustJSON(t, domainAddReply)["data"].(map[string]any)["dns_records"])
	got := zoneLines("www.example.com", recs)
	for _, want := range []string{
		"www.example.com. 300 IN CNAME proxy.miosa.ai.",
		`_miosa-verify.www.example.com. 300 IN TXT "miosa-domain-abc"`,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("zone output missing %q:\n%s", want, got)
		}
	}
}

func TestZoneLinesCommentOutAnAliasAndKeepAddresses(t *testing.T) {
	recs := parseDNSRecords(mustJSON(t, apexReply)["data"].(map[string]any)["dns_records"])
	got := zoneLines("example.com", recs)
	if !strings.Contains(got, "example.com. 300 IN A 203.0.113.7") {
		t.Fatalf("A line missing:\n%s", got)
	}
	if !strings.Contains(got, "; ALIAS example.com. -> proxy.miosa.ai") {
		t.Fatalf("ALIAS should be a comment:\n%s", got)
	}
}

func TestDomainAddDNSZonePrintsLinesAndSendsNoDNSField(t *testing.T) {
	_, got := opServer(t, 201, domainAddReply)
	out, err := runOp(t, domainAddOp(t), "", "6f1c1d0e-0000-4000-8000-000000000001", "www.example.com", "--dns", "zone")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !strings.Contains(out, "www.example.com. 300 IN CNAME proxy.miosa.ai.") {
		t.Fatalf("output:\n%s", out)
	}
	if _, leaked := got()[0].Body["dns"]; leaked {
		t.Fatalf("--dns leaked into the body: %+v", got()[0].Body)
	}
}

// fakeCloudflare keeps a zone's records in memory.
type fakeCloudflare struct {
	mu      sync.Mutex
	records []map[string]any
	calls   []string
}

func newFakeCloudflare(t *testing.T, zone string, existing ...map[string]any) *fakeCloudflare {
	t.Helper()
	f := &fakeCloudflare{records: existing}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.calls = append(f.calls, r.Method+" "+r.URL.Path)
		if r.Header.Get("Authorization") != "Bearer cf-test-token" {
			w.WriteHeader(403)
			fmt.Fprint(w, `{"success":false,"errors":[{"code":9109,"message":"Invalid access token"}]}`)
			return
		}
		reply := func(v any) { raw, _ := json.Marshal(v); fmt.Fprintf(w, `{"success":true,"result":%s}`, raw) }
		switch {
		case r.URL.Path == "/zones":
			if r.URL.Query().Get("name") == zone {
				reply([]map[string]string{{"id": "z1", "name": zone}})
			} else {
				reply([]any{})
			}
		case r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/dns_records"):
			var match []map[string]any
			for _, rec := range f.records {
				if rec["type"] == r.URL.Query().Get("type") && rec["name"] == r.URL.Query().Get("name") {
					match = append(match, rec)
				}
			}
			reply(match)
		case r.Method == "POST":
			var rec map[string]any
			b, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(b, &rec)
			rec["id"] = fmt.Sprintf("r%d", len(f.records)+1)
			f.records = append(f.records, rec)
			reply(rec)
		case r.Method == "PUT":
			var rec map[string]any
			b, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(b, &rec)
			id := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
			for i, old := range f.records {
				if old["id"] == id {
					rec["id"] = id
					f.records[i] = rec
				}
			}
			reply(rec)
		}
	}))
	t.Cleanup(srv.Close)
	old := cloudflareAPI
	cloudflareAPI = srv.URL
	t.Cleanup(func() { cloudflareAPI = old })
	t.Setenv("CLOUDFLARE_API_TOKEN", "cf-test-token")
	return f
}

func TestDomainAddDNSCloudflareCreatesTheRecordsInTheRightZone(t *testing.T) {
	cf := newFakeCloudflare(t, "example.com")
	opServer(t, 201, domainAddReply)

	out, err := runOp(t, domainAddOp(t), "", "6f1c1d0e-0000-4000-8000-000000000001", "www.example.com", "--dns", "cloudflare")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if len(cf.records) != 2 {
		t.Fatalf("records = %+v", cf.records)
	}
	byType := map[string]map[string]any{}
	for _, r := range cf.records {
		byType[r["type"].(string)] = r
	}
	if c := byType["CNAME"]; c["name"] != "www.example.com" || c["content"] != "proxy.miosa.ai" || c["proxied"] != false {
		t.Fatalf("CNAME = %+v", c)
	}
	if x := byType["TXT"]; x["name"] != "_miosa-verify.www.example.com" || x["content"] != "miosa-domain-abc" {
		t.Fatalf("TXT = %+v", x)
	}
	if !strings.Contains(out, "CNAME www.example.com created") {
		t.Fatalf("output:\n%s", out)
	}
}

func TestDomainAddDNSCloudflareIsIdempotentAndUpdatesAWrongCNAME(t *testing.T) {
	cf := newFakeCloudflare(t, "example.com",
		map[string]any{"id": "r1", "type": "CNAME", "name": "www.example.com", "content": "old.parked.net"},
		map[string]any{"id": "r2", "type": "TXT", "name": "_miosa-verify.www.example.com", "content": "miosa-domain-abc"},
	)
	opServer(t, 201, domainAddReply)

	out, err := runOp(t, domainAddOp(t), "", "6f1c1d0e-0000-4000-8000-000000000001", "www.example.com", "--dns", "cloudflare")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if len(cf.records) != 2 {
		t.Fatalf("expected no new records: %+v", cf.records)
	}
	if cf.records[0]["content"] != "proxy.miosa.ai" {
		t.Fatalf("wrong CNAME was not replaced: %+v", cf.records[0])
	}
	if !strings.Contains(out, "updated (was old.parked.net)") || !strings.Contains(out, "TXT _miosa-verify.www.example.com already set") {
		t.Fatalf("output:\n%s", out)
	}
}

func TestDomainAddDNSCloudflareApexUsesTheAddressNotTheAlias(t *testing.T) {
	cf := newFakeCloudflare(t, "example.com")
	opServer(t, 201, apexReply)

	if out, err := runOp(t, domainAddOp(t), "", "6f1c1d0e-0000-4000-8000-000000000001", "example.com", "--dns", "cloudflare"); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	types := []string{}
	for _, r := range cf.records {
		types = append(types, r["type"].(string))
	}
	if strings.Join(types, ",") != "A,TXT" {
		t.Fatalf("types = %v (no CNAME at the apex when an address is published)", types)
	}
}

func TestDomainAddDNSCloudflareNeedsATokenAndAZone(t *testing.T) {
	newFakeCloudflare(t, "other.org")
	opServer(t, 201, domainAddReply)

	t.Setenv("CLOUDFLARE_API_TOKEN", "")
	t.Setenv("CF_API_TOKEN", "")
	_, err := runOp(t, domainAddOp(t), "", "6f1c1d0e-0000-4000-8000-000000000001", "www.example.com", "--dns", "cloudflare")
	if err == nil || !strings.Contains(err.Error(), "CLOUDFLARE_API_TOKEN") {
		t.Fatalf("err = %v", err)
	}

	t.Setenv("CLOUDFLARE_API_TOKEN", "cf-test-token")
	_, err = runOp(t, domainAddOp(t), "", "6f1c1d0e-0000-4000-8000-000000000001", "www.example.com", "--dns", "cloudflare")
	if err == nil || !strings.Contains(err.Error(), "no Cloudflare zone for www.example.com") {
		t.Fatalf("err = %v", err)
	}
}

func TestDomainAddWithoutDNSFlagDoesNothingExtra(t *testing.T) {
	_, got := opServer(t, 201, domainAddReply)
	if _, err := runOp(t, domainAddOp(t), "", "6f1c1d0e-0000-4000-8000-000000000001", "www.example.com"); err != nil {
		t.Fatal(err)
	}
	if len(got()) != 1 {
		t.Fatalf("requests = %+v", got())
	}
}

func domainAddOp(t *testing.T) Op {
	t.Helper()
	for _, o := range deployOps() {
		if strings.HasPrefix(o.Use, "domain-add") {
			return o
		}
	}
	t.Fatal("domain-add op not found")
	return Op{}
}

func mustJSON(t *testing.T, s string) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestRedirectToMapsOntoThePolicyTheServerServes(t *testing.T) {
	for _, c := range []struct{ host, target, want string }{
		{"www.example.com", "example.com", "www_to_apex"},
		{"example.com", "www.example.com", "apex_to_www"},
		{"WWW.Example.com", "example.com.", "www_to_apex"},
	} {
		got, err := redirectPolicyFor(c.host, c.target)
		if err != nil || got != c.want {
			t.Fatalf("%s -> %s: %q, %v (want %q)", c.host, c.target, got, err, c.want)
		}
	}
	for _, c := range [][2]string{{"www.example.com", "other.org"}, {"blog.example.com", "example.com"}, {"example.com", "example.com"}} {
		if _, err := redirectPolicyFor(c[0], c[1]); err == nil {
			t.Fatalf("%v should be refused", c)
		}
	}
}

func TestDomainAddRedirectToSendsThePolicyAndNotTheFlag(t *testing.T) {
	_, got := opServer(t, 201, domainAddReply)
	if out, err := runOp(t, domainAddOp(t), "", "6f1c1d0e-0000-4000-8000-000000000001", "www.example.com", "--redirect-to", "example.com"); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	b := got()[0].Body
	if b["redirect_policy"] != "www_to_apex" || b["hostname"] != "www.example.com" {
		t.Fatalf("body = %+v", b)
	}
	if _, leaked := b["redirect_to"]; leaked {
		t.Fatalf("redirect_to leaked: %+v", b)
	}
}

func TestDomainAddRedirectToRefusesAnUnsupportedTargetBeforeAnyRequest(t *testing.T) {
	_, got := opServer(t, 201, domainAddReply)
	_, err := runOp(t, domainAddOp(t), "", "6f1c1d0e-0000-4000-8000-000000000001", "www.example.com", "--redirect-to", "other.org")
	if err == nil || !strings.Contains(err.Error(), "supports only example.com <-> www.example.com") {
		t.Fatalf("err = %v", err)
	}
	if len(got()) != 0 {
		t.Fatalf("a request was sent: %+v", got())
	}
}

// The API answers 201 for an already-attached hostname and returns the row it
// has, without applying the redirect_policy it was sent. The command has to say
// so: it used to look like it had set a redirect it had not.
func TestDomainAddWarnsWhenTheRedirectPolicyWasNotApplied(t *testing.T) {
	existing := `{"data":{"id":"d1","hostname":"www.example.com","redirect_policy":"none","dns_records":[]}}`
	_, _ = opServer(t, 201, existing)

	out, err := runOp(t, domainAddOp(t), "", "6f1c1d0e-0000-4000-8000-000000000001",
		"www.example.com", "--redirect-to", "example.com")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !strings.Contains(out, "warning:") ||
		!strings.Contains(out, "redirect_policy") ||
		!strings.Contains(out, "not applied") {
		t.Fatalf("the miss should be reported:\n%s", out)
	}
}

func TestDomainAddIsQuietWhenTheRedirectPolicyMatches(t *testing.T) {
	applied := `{"data":{"id":"d1","hostname":"www.example.com","redirect_policy":"www_to_apex","dns_records":[]}}`
	_, _ = opServer(t, 201, applied)

	out, err := runOp(t, domainAddOp(t), "", "6f1c1d0e-0000-4000-8000-000000000001",
		"www.example.com", "--redirect-to", "example.com")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if strings.Contains(out, "warning:") {
		t.Fatalf("nothing to warn about:\n%s", out)
	}
}

func TestDomainAddIsQuietWithoutTheRedirectFlag(t *testing.T) {
	_, _ = opServer(t, 201, domainAddReply)

	out, err := runOp(t, domainAddOp(t), "", "6f1c1d0e-0000-4000-8000-000000000001", "www.example.com")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if strings.Contains(out, "warning:") {
		t.Fatalf("no redirect was asked for:\n%s", out)
	}
}
