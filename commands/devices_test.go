package commands_test

import (
	"strings"
	"testing"
)

const deviceID = "9f1c2d3e-4a5b-4c6d-8e7f-0a1b2c3d4e5f"

func TestDeviceListAndGet(t *testing.T) {
	f := fakeRoutes(t, map[string]any{
		"GET /devices": `{"data":[{"id":"` + deviceID + `","name":"builder","kind":"sandbox","state":"running","running":true,"ip_address":"10.0.0.4"},
		                          {"id":"c0ffee00-0000-0000-0000-000000000000","name":"desk","kind":"computer","state":"paused","running":false}]}`,
		"GET /devices/" + deviceID: `{"data":{"id":"` + deviceID + `","name":"builder","kind":"sandbox","state":"running","running":true,"terminal":true,"template":"nextjs"}}`,
	})
	out, err := run(t, "device", "list")
	if err != nil || !strings.Contains(out, "builder") || !strings.Contains(out, "desk") {
		t.Fatalf("%v\n%s", err, out)
	}
	if q := f.find("GET", "/devices")[0].Query; q != "" {
		t.Fatalf("no filter was given, query should be empty: %q", q)
	}
	if out, err := run(t, "device", "list", "--status", "running", "--limit", "5"); err != nil || !strings.Contains(out, "builder") {
		t.Fatalf("%v\n%s", err, out)
	}
	if q := f.find("GET", "/devices")[1].Query; !strings.Contains(q, "status=running") || !strings.Contains(q, "limit=5") {
		t.Fatalf("query = %q", q)
	}
	if out, err := run(t, "device", "get", deviceID); err != nil || !strings.Contains(out, "nextjs") {
		t.Fatalf("a single object under data must be unwrapped: %v\n%s", err, out)
	}
}

func TestDeviceExecAndFiles(t *testing.T) {
	f := fakeRoutes(t, map[string]any{
		"POST /devices/" + deviceID + "/exec":        `{"data":{"stdout":"Linux\n","exit_code":0}}`,
		"GET /devices/" + deviceID + "/files":        `{"data":{"path":"/workspace","output":"app.go\n"}}`,
		"POST /devices/" + deviceID + "/files/write": `{"data":{"path":"/workspace/a.txt","bytes":3}}`,
	})
	if _, err := run(t, "device", "exec", deviceID, "--command", "uname -a", "--timeout-ms", "5000"); err != nil {
		t.Fatal(err)
	}
	b := f.find("POST", "/devices/"+deviceID+"/exec")[0].Body
	if b["command"] != "uname -a" || b["timeout_ms"] != float64(5000) {
		t.Fatalf("body = %v", b)
	}
	if _, err := run(t, "device", "exec", deviceID); err == nil {
		t.Fatal("--command is required")
	}
	if _, err := run(t, "device", "files", deviceID, "--path", "/workspace"); err != nil {
		t.Fatal(err)
	}
	if q := f.find("GET", "/devices/"+deviceID+"/files")[0].Query; q != "path=%2Fworkspace" {
		t.Fatalf("query = %q", q)
	}
	if _, err := run(t, "device", "write", deviceID, "--path", "/workspace/a.txt", "--content", "hi\n"); err != nil {
		t.Fatal(err)
	}
	if b := f.find("POST", "/devices/"+deviceID+"/files/write")[0].Body; b["path"] != "/workspace/a.txt" || b["content"] != "hi\n" {
		t.Fatalf("body = %v", b)
	}
}

func TestDeviceExposeConvertsThePort(t *testing.T) {
	f := fakeRoutes(t, map[string]any{"POST /devices/" + deviceID + "/expose": `{"data":{"url":"https://8080-builder.example"}}`})
	if _, err := run(t, "device", "expose", deviceID, "8080"); err != nil {
		t.Fatal(err)
	}
	if b := f.find("POST", "/devices/"+deviceID+"/expose")[0].Body; b["port"] != float64(8080) {
		t.Fatalf("the port must go on the wire as a number: %v", b)
	}
	if _, err := run(t, "device", "expose", deviceID, "http"); err == nil {
		t.Fatal("--port must be a number")
	}
	if f.count() != 1 {
		t.Fatalf("a rejected port must not reach the API: %+v", f.calls)
	}
}

func TestDeviceExtendReadsADuration(t *testing.T) {
	f := fakeRoutes(t, map[string]any{"POST /devices/" + deviceID + "/extend": `{"data":{"id":"` + deviceID + `","state":"running"}}`})
	if _, err := run(t, "device", "extend", deviceID, "--timeout", "90m"); err != nil {
		t.Fatal(err)
	}
	if b := f.find("POST", "/devices/"+deviceID+"/extend")[0].Body; b["timeout_sec"] != float64(5400) {
		t.Fatalf("90m must become 5400 seconds: %v", b)
	}
	if _, err := run(t, "device", "extend", deviceID, "--timeout", "soon"); err == nil {
		t.Fatal("an unparseable duration is a usage error")
	}
}

func TestDeviceDestroyNeedsConfirmation(t *testing.T) {
	f := fakeRoutes(t, map[string]any{"DELETE /devices/" + deviceID: `{"data":{"id":"` + deviceID + `","state":"destroyed"}}`})
	if _, err := run(t, "device", "rm", deviceID); err == nil {
		t.Fatal("destroying a device needs --yes off a terminal")
	}
	if f.count() != 0 {
		t.Fatal("a refused confirmation must send nothing")
	}
	if _, err := run(t, "device", "rm", deviceID, "--yes"); err != nil {
		t.Fatal(err)
	}
	if len(f.find("DELETE", "/devices/"+deviceID)) != 1 {
		t.Fatalf("calls = %+v", f.calls)
	}
}

func TestDeviceListSaysWhenErroredMachinesAreHidden(t *testing.T) {
	f := fakeRoutes(t, map[string]any{
		"GET /devices": `{"data":[{"id":"` + deviceID + `","name":"builder","kind":"sandbox","state":"running","running":true}],"meta":{"errored_hidden":2}}`,
	})
	out, err := run(t, "device", "list")
	if err != nil || !strings.Contains(out, "2 errored machines hidden, use --errored") {
		t.Fatalf("%v\n%s", err, out)
	}
	if _, err := run(t, "device", "list", "--errored"); err != nil {
		t.Fatal(err)
	}
	if q := f.find("GET", "/devices")[1].Query; !strings.Contains(q, "include_errored=true") {
		t.Fatalf("--errored must ask the server for them: %q", q)
	}
}
