package commands

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// waitServer answers /domains/<host>/check from a script (one reply per call,
// the last repeats) and records the verify calls.
func waitServer(t *testing.T, checks []string) (verifies func() int) {
	t.Helper()
	var mu sync.Mutex
	call, verified := 0, 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/check"):
			i := call
			if i >= len(checks) {
				i = len(checks) - 1
			}
			call++
			fmt.Fprint(w, checks[i])
		case r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/verify"):
			verified++
			fmt.Fprint(w, `{"data":{"status":"verified"}}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("MIOSA_API_KEY", "msk_u_test")
	t.Setenv("MIOSA_BASE_URL", srv.URL)
	domainWaitInterval = time.Millisecond
	t.Cleanup(func() { domainWaitInterval = 10 * time.Second })
	return func() int { mu.Lock(); defer mu.Unlock(); return verified }
}

func stubLive(t *testing.T, code int, err error) {
	t.Helper()
	old := domainLiveProbe
	domainLiveProbe = func(context.Context, string) (int, error) { return code, err }
	t.Cleanup(func() { domainLiveProbe = old })
}

const (
	cnameMissing = `{"data":{"id":"d1","hostname":"www.example.com","status":"pending","verified":false,"ready":false,"stage":"dns","checks":[{"id":"cname","kind":"dns","type":"CNAME","name":"www.example.com","expected":["proxy.miosa.ai"],"observed":["parked.registrar.net"],"ok":false},{"id":"tls","kind":"tls","type":"TLS","name":"www.example.com","expected":["a certificate"],"observed":[],"ok":false,"note":"not checked until the DNS records above are correct"}]}}`
	tlsPending   = `{"data":{"id":"d1","hostname":"www.example.com","status":"pending","verified":false,"ready":false,"stage":"tls","checks":[{"id":"cname","kind":"dns","type":"CNAME","name":"www.example.com","expected":["proxy.miosa.ai"],"observed":["proxy.miosa.ai"],"ok":true},{"id":"tls","kind":"tls","type":"TLS","name":"www.example.com","expected":["a certificate"],"observed":["the edge presented no certificate yet"],"ok":false}]}}`
	allReady     = `{"data":{"id":"d1","hostname":"www.example.com","status":"active","verified":true,"ready":true,"stage":"ready","checks":[{"id":"cname","kind":"dns","type":"CNAME","name":"www.example.com","expected":["proxy.miosa.ai"],"observed":["proxy.miosa.ai"],"ok":true},{"id":"tls","kind":"tls","type":"TLS","name":"www.example.com","expected":["a certificate"],"observed":["certificate issued by R3"],"ok":true}]}}`
)

func TestDomainVerifyWaitPrintsTheMissingRecordThenGoesLive(t *testing.T) {
	verifies := waitServer(t, []string{cnameMissing, tlsPending, allReady})
	stubLive(t, 200, nil)

	out, err := runOp(t, deployDomainVerifyOp(t), "", "my-app", "www.example.com", "--wait", "--timeout", "30s")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	for _, want := range []string{
		"CNAME www.example.com expected proxy.miosa.ai, observed parked.registrar.net",
		"waiting on tls",
		"live https://www.example.com (HTTP 200)",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("output missing %q:\n%s", want, out)
		}
	}
	if verifies() != 1 {
		t.Fatalf("verify called %d times, want once DNS was right", verifies())
	}
}

func TestDomainVerifyWaitTimeoutNamesWhatIsStillMissing(t *testing.T) {
	waitServer(t, []string{cnameMissing})
	stubLive(t, 0, fmt.Errorf("unused"))

	out, err := runOp(t, deployDomainVerifyOp(t), "", "my-app", "www.example.com", "--wait", "--timeout", "50ms")
	if err == nil {
		t.Fatalf("expected a timeout error\n%s", out)
	}
	for _, want := range []string{"timed out after 50ms", "missing: CNAME www.example.com expected proxy.miosa.ai, observed parked.registrar.net"} {
		if !strings.Contains(out, want) {
			t.Fatalf("output missing %q:\n%s", want, out)
		}
	}
}

func TestDomainVerifyWaitRejectsABadTimeout(t *testing.T) {
	waitServer(t, []string{allReady})
	_, err := runOp(t, deployDomainVerifyOp(t), "", "my-app", "www.example.com", "--wait", "--timeout", "soon")
	if err == nil || !strings.Contains(err.Error(), "--timeout must be a duration") {
		t.Fatalf("err = %v", err)
	}
}

func TestDomainVerifyWithoutWaitStaysOneShot(t *testing.T) {
	_, got := opServer(t, 200, `{"data":{"status":"verified"}}`)
	if _, err := runOp(t, deployDomainVerifyOp(t), "", "6f1c1d0e-0000-4000-8000-000000000001", "d1"); err != nil {
		t.Fatal(err)
	}
	r := got()
	if len(r) != 1 || r[0].Method != "POST" || !strings.HasSuffix(r[0].Path, "/domains/d1/verify") {
		t.Fatalf("requests = %+v", r)
	}
	if _, has := r[0].Body["wait"]; has {
		t.Fatalf("client-side flag leaked into the body: %+v", r[0].Body)
	}
}

// deployDomainVerifyOp pulls the real domain-verify Op out of the deploy group.
func deployDomainVerifyOp(t *testing.T) Op {
	t.Helper()
	for _, o := range deployOps() {
		if strings.HasPrefix(o.Use, "domain-verify") {
			return o
		}
	}
	t.Fatal("domain-verify op not found")
	return Op{}
}
