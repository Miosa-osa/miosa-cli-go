package commands_test

import (
	"strings"
	"testing"
)

// versionsAPI serves a deployment with two versions and its version list.
func versionsAPI(t *testing.T) *fakeAPI {
	t.Helper()
	return newFakeAPI(t, func(c call) (int, string) {
		switch {
		case c.is("GET", "/deployments/d3"):
			return 200, `{"data":{"id":"d3","name":"my-site","active_version_id":"v2"}}`
		case c.is("GET", "/deployments/d3/versions"):
			return 200, `{"data":[` +
				`{"id":"v2","version_number":2,"kind":"static","state":"ready","artifact_sha256":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",` +
				`"metadata":{"publish_source":"drop"},"artifact_manifest":{"file_count":2,"total_bytes":2048,"files":[]}},` +
				`{"id":"v1","version_number":1,"kind":"static","state":"ready","artifact_sha256":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",` +
				`"metadata":{"publish_source":"drop"},"artifact_manifest":{"file_count":1,"total_bytes":1024,"files":[]}}]}`
		}
		return 404, `{"error":{"code":"NOT_FOUND","message":"no route"}}`
	})
}

func TestDeployVersionsShowsSizeHashAndTheLiveOne(t *testing.T) {
	versionsAPI(t)

	out, err := run(t, "deploy", "versions", "d3")
	if err != nil {
		t.Fatalf("deploy versions: %v (out=%q)", err, out)
	}
	if !strings.Contains(out, "FILES") || !strings.Contains(out, "SIZE") || !strings.Contains(out, "SHA") {
		t.Fatalf("versions should carry size and hash columns: %q", out)
	}
	if !strings.Contains(out, "2.0 KB") || !strings.Contains(out, "1.0 KB") {
		t.Fatalf("sizes missing: %q", out)
	}
	if !strings.Contains(out, "bbbbbbbbbbbb") {
		t.Fatalf("the artifact sha should be shown: %q", out)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	live := 0
	for _, line := range lines[1:] {
		if strings.Contains(line, "live") {
			live++
			if !strings.Contains(line, "bbbbbbbb") {
				t.Fatalf("the live marker is on the wrong version: %q", line)
			}
		}
	}
	if live != 1 {
		t.Fatalf("exactly one version is live, got %d in %q", live, out)
	}
}

func deployDiffAPI(t *testing.T) *fakeAPI {
	t.Helper()
	return newFakeAPI(t, func(c call) (int, string) {
		switch c.Path {
		case "/deployments/d3/versions":
			return 200, `{"data":[{"id":"v1","version_number":1},{"id":"v2","version_number":2},` +
				`{"id":"v3","version_number":3},{"id":"90a9dee1-2222-4222-8222-222222222222","version_number":4}]}`
		case "/deployments/d3/versions/90a9dee1-2222-4222-8222-222222222222":
			return 200, `{"data":{"id":"90a9dee1-2222-4222-8222-222222222222","kind":"static","artifact_sha256":"dd",` +
				`"artifact_manifest":{"file_count":1,"total_bytes":12,"files":[` +
				`{"path":"index.html","sha256":"1111","size":12}]}}}`
		case "/deployments/d3/versions/v1":
			return 200, `{"data":{"id":"v1","kind":"static","artifact_sha256":"aa",` +
				`"artifact_manifest":{"file_count":2,"total_bytes":30,"files":[` +
				`{"path":"index.html","sha256":"1111","size":12},` +
				`{"path":"old.js","sha256":"2222","size":18}]}}}`
		case "/deployments/d3/versions/v2":
			return 200, `{"data":{"id":"v2","kind":"static","artifact_sha256":"bb",` +
				`"artifact_manifest":{"file_count":2,"total_bytes":40,"files":[` +
				`{"path":"index.html","sha256":"3333","size":22},` +
				`{"path":"new.js","sha256":"4444","size":18}]}}}`
		case "/deployments/d3/versions/v3":
			return 200, `{"data":{"id":"v3","kind":"dynamic","artifact_sha256":"cc","artifact_manifest":null}}`
		}
		return 404, `{"error":{"code":"NOT_FOUND","message":"no route"}}`
	})
}

func TestDeployDiffListsAddedChangedAndRemoved(t *testing.T) {
	deployDiffAPI(t)

	out, err := run(t, "deploy", "diff", "v1", "v2", "--deployment", "d3")
	if err != nil {
		t.Fatalf("deploy diff: %v (out=%q)", err, out)
	}
	for _, want := range []string{
		"+ new.js", "~ index.html", "- old.js",
		"2 files", "1 added", "1 changed", "1 removed", "0 unchanged",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("%q missing from %q", want, out)
		}
	}
}

func TestDeployDiffSaysWhenNothingChanged(t *testing.T) {
	f := deployDiffAPI(t)
	_ = f

	out, err := run(t, "deploy", "diff", "v1", "v1", "--deployment", "d3")
	if err != nil {
		t.Fatalf("deploy diff: %v (out=%q)", err, out)
	}
	if !strings.Contains(out, "same") {
		t.Fatalf("an unchanged diff should say so: %q", out)
	}
}

func TestDeployDiffWithoutAManifestFallsBackToHashes(t *testing.T) {
	deployDiffAPI(t)

	out, err := run(t, "deploy", "diff", "v1", "v3", "--deployment", "d3")
	if err != nil {
		t.Fatalf("deploy diff: %v (out=%q)", err, out)
	}
	if !strings.Contains(out, "no file manifest") {
		t.Fatalf("a version without a manifest should say so: %q", out)
	}
}

// The help's own example uses short ids; they resolve against the versions.
func TestDeployDiffTakesShortVersionIDsAndNumbers(t *testing.T) {
	deployDiffAPI(t)

	out, err := run(t, "deploy", "diff", "90a9dee1", "1", "--deployment", "d3")
	if err != nil {
		t.Fatalf("deploy diff with a short id and a number: %v (out=%q)", err, out)
	}
	if !strings.Contains(out, "+ old.js") {
		t.Fatalf("want old.js added going from 90a9dee1 to v1: %q", out)
	}
	if _, err := run(t, "deploy", "diff", "ffff0000", "1", "--deployment", "d3"); err == nil ||
		!strings.Contains(err.Error(), "version") || !strings.Contains(err.Error(), "ffff0000") {
		t.Fatalf("an unknown version must be named as a version: %v", err)
	}
}
