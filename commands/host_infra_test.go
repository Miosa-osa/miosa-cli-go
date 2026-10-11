package commands_test

import (
	"strings"
	"testing"

	"github.com/Miosa-osa/miosa-cli-go/commands"
)

const (
	ocHost = "h-1"
	ocRun  = "r-1"
	ocMesh = "m-1"
	ocCfg  = "c-1"
	ocSnap = "s-1"
)

func TestHostGhaRunnerCommands(t *testing.T) {
	f := fakeRoutes(t, map[string]any{
		"GET /opencomputers/hosts/" + ocHost + "/gha-runners":                        `{"runners":[{"id":"` + ocRun + `","name":"ci-1","state":"running","repo_url":"https://github.com/acme/app","labels":["self-hosted","linux"],"ephemeral":true}]}`,
		"POST /opencomputers/hosts/" + ocHost + "/gha-runners":                       `{"runner":{"id":"` + ocRun + `","name":"ci-2","state":"provisioning"}}`,
		"GET /opencomputers/hosts/" + ocHost + "/gha-runners/" + ocRun:               `{"runner":{"id":"` + ocRun + `","name":"ci-1","state":"running","gh_runner_id":"77"}}`,
		"POST /opencomputers/hosts/" + ocHost + "/gha-runners/" + ocRun + "/refresh": `{"runner":{"id":"` + ocRun + `","state":"provisioning"}}`,
		"DELETE /opencomputers/hosts/" + ocHost + "/gha-runners/" + ocRun:            `{"ok":true}`,
	})
	if out, err := run(t, "host", "gha-runner", "list", ocHost); err != nil || !strings.Contains(out, "ci-1") {
		t.Fatalf("%v\n%s", err, out)
	}
	if _, err := run(t, "host", "gha-runner", "new", ocHost, "--repo", "https://github.com/acme/app", "--label", "linux", "--label", "x64", "--ephemeral", "--token", "ghp_x"); err != nil {
		t.Fatal(err)
	}
	b := f.find("POST", "/opencomputers/hosts/"+ocHost+"/gha-runners")[0].Body
	if b["repo_url"] != "https://github.com/acme/app" || b["ephemeral"] != true || b["pat_value"] != "ghp_x" {
		t.Fatalf("body = %v", b)
	}
	if labels, ok := b["labels"].([]any); !ok || len(labels) != 2 {
		t.Fatalf("labels = %v", b["labels"])
	}
	if _, err := run(t, "host", "gha-runner", "new", ocHost); err == nil {
		t.Fatal("--repo is required")
	}
	if out, err := run(t, "host", "gha-runner", "get", ocHost, ocRun); err != nil || !strings.Contains(out, "77") {
		t.Fatalf("%v\n%s", err, out)
	}
	if _, err := run(t, "host", "gha-runner", "refresh", ocHost, ocRun); err != nil {
		t.Fatal(err)
	}
	if _, err := run(t, "host", "gha-runner", "rm", ocHost, ocRun); err == nil {
		t.Fatal("deregistering a runner needs --yes off a terminal")
	}
	if _, err := run(t, "host", "gha-runner", "rm", ocHost, ocRun, "--yes"); err != nil {
		t.Fatal(err)
	}
}

func TestHostMeshCommands(t *testing.T) {
	f := fakeRoutes(t, map[string]any{
		"GET /opencomputers/meshes":                             `{"meshes":[{"id":"` + ocMesh + `","name":"lab","state":"active","cidr":"10.44.0.0/24","listen_port_base":51820}]}`,
		"POST /opencomputers/meshes":                            `{"mesh":{"id":"` + ocMesh + `","name":"lab","state":"provisioning"}}`,
		"GET /opencomputers/meshes/" + ocMesh:                   `{"mesh":{"id":"` + ocMesh + `","name":"lab","state":"active","peers":[{"id":"p1","host_id":"h-2","assigned_ip":"10.44.0.2"}]}}`,
		"POST /opencomputers/meshes/" + ocMesh + "/hosts/h-2":   `{"mesh":{"id":"` + ocMesh + `"}}`,
		"DELETE /opencomputers/meshes/" + ocMesh + "/hosts/h-2": `{"mesh":{"id":"` + ocMesh + `"}}`,
		"DELETE /opencomputers/meshes/" + ocMesh:                `{"ok":true}`,
	})
	if out, err := run(t, "host", "mesh", "list"); err != nil || !strings.Contains(out, "lab") {
		t.Fatalf("%v\n%s", err, out)
	}
	if _, err := run(t, "host", "mesh", "new", "lab", "--host", "h-2", "--host", "h-3"); err != nil {
		t.Fatal(err)
	}
	b := f.find("POST", "/opencomputers/meshes")[0].Body
	if b["name"] != "lab" {
		t.Fatalf("body = %v", b)
	}
	if hosts, ok := b["host_ids"].([]any); !ok || len(hosts) != 2 {
		t.Fatalf("host_ids = %v", b["host_ids"])
	}
	if out, err := run(t, "host", "mesh", "get", ocMesh); err != nil || !strings.Contains(out, "active") {
		t.Fatalf("%v\n%s", err, out)
	}
	if _, err := run(t, "host", "mesh", "add-host", ocMesh, "h-2"); err != nil {
		t.Fatal(err)
	}
	if len(f.find("POST", "/opencomputers/meshes/"+ocMesh+"/hosts/h-2")) != 1 {
		t.Fatalf("calls = %+v", f.calls)
	}
	if _, err := run(t, "host", "mesh", "remove-host", ocMesh, "h-2", "--yes"); err != nil {
		t.Fatal(err)
	}
	if _, err := run(t, "host", "mesh", "rm", ocMesh, "--yes"); err != nil {
		t.Fatal(err)
	}
}

func TestHostBackupCommands(t *testing.T) {
	f := fakeRoutes(t, map[string]any{
		"GET /opencomputers/hosts/" + ocHost + "/backup-configs":                         `{"backup_configs":[{"id":"` + ocCfg + `","name":"nightly","path":"/data","state":"active","schedule_cron":"0 3 * * *","retention_count":7}]}`,
		"POST /opencomputers/hosts/" + ocHost + "/backup-configs":                        `{"backup_config":{"id":"` + ocCfg + `","name":"nightly","state":"active"}}`,
		"PATCH /opencomputers/hosts/" + ocHost + "/backup-configs/" + ocCfg:              `{"backup_config":{"id":"` + ocCfg + `","name":"nightly","retention_count":14}}`,
		"POST /opencomputers/hosts/" + ocHost + "/backup-configs/" + ocCfg + "/trigger":  `{"snapshot":{"id":"` + ocSnap + `","state":"pending"}}`,
		"POST /opencomputers/hosts/" + ocHost + "/backup-configs/" + ocCfg + "/pause":    `{"backup_config":{"id":"` + ocCfg + `","state":"paused"}}`,
		"GET /opencomputers/hosts/" + ocHost + "/backup-configs/" + ocCfg + "/snapshots": `{"snapshots":[{"id":"` + ocSnap + `","state":"complete","compressed_bytes":2048,"chunk_count":3}]}`,
		"GET /opencomputers/snapshots/" + ocSnap:                                         `{"snapshot":{"id":"` + ocSnap + `","state":"complete","total_bytes":4096}}`,
		"POST /opencomputers/snapshots/" + ocSnap + "/restore":                           `{"snapshot":{"id":"` + ocSnap + `","state":"restoring"}}`,
		"DELETE /opencomputers/snapshots/" + ocSnap:                                      `{"ok":true}`,
	})
	if out, err := run(t, "host", "backup", "list", ocHost); err != nil || !strings.Contains(out, "nightly") {
		t.Fatalf("%v\n%s", err, out)
	}
	if _, err := run(t, "host", "backup", "new", ocHost, "--name", "nightly", "--path", "/data", "--schedule", "0 3 * * *", "--keep", "7"); err != nil {
		t.Fatal(err)
	}
	b := f.find("POST", "/opencomputers/hosts/"+ocHost+"/backup-configs")[0].Body
	if b["name"] != "nightly" || b["path"] != "/data" || b["schedule_cron"] != "0 3 * * *" || b["retention_count"] != float64(7) {
		t.Fatalf("body = %v", b)
	}
	if _, err := run(t, "host", "backup", "update", ocHost, ocCfg, "--keep", "14"); err != nil {
		t.Fatal(err)
	}
	if b := f.find("PATCH", "/opencomputers/hosts/"+ocHost+"/backup-configs/"+ocCfg)[0].Body; b["retention_count"] != float64(14) {
		t.Fatalf("body = %v", b)
	}
	if _, err := run(t, "host", "backup", "trigger", ocHost, ocCfg); err != nil {
		t.Fatal(err)
	}
	if _, err := run(t, "host", "backup", "snapshot", "list", ocHost, ocCfg); err != nil {
		t.Fatal(err)
	}
	if _, err := run(t, "host", "backup", "snapshot", "get", ocSnap); err != nil {
		t.Fatal(err)
	}
	if _, err := run(t, "host", "backup", "snapshot", "fork", ocSnap, "--to-host", "h-9", "--overwrite", "--yes"); err != nil {
		t.Fatal(err)
	}
	rb := f.find("POST", "/opencomputers/snapshots/"+ocSnap+"/restore")[0].Body
	if rb["target_host_id"] != "h-9" || rb["overwrite"] != true {
		t.Fatalf("body = %v", rb)
	}
	if _, err := run(t, "host", "backup", "snapshot", "fork", ocSnap); err == nil {
		t.Fatal("forking onto a host needs --yes off a terminal")
	}
	// The retired 1.x name still works, hidden.
	if _, err := run(t, "host", "backup", "snapshot", "restore", ocSnap, "--yes"); err != nil {
		t.Fatalf("the deprecated restore name must keep working: %v", err)
	}
	if _, err := run(t, "host", "backup", "snapshot", "rm", ocSnap, "--yes"); err != nil {
		t.Fatal(err)
	}
}

func TestLegacySnapshotRestoreIsHidden(t *testing.T) {
	fakeRoutes(t, nil)
	root := commands.Root()
	legacy, _, err := root.Find([]string{"host", "backup", "snapshot", "restore"})
	if err != nil || legacy.Name() != "restore" {
		t.Fatalf("restore must still resolve: %v", err)
	}
	if !legacy.Hidden || legacy.Deprecated == "" {
		t.Fatal("the retired restore name must be hidden and deprecated")
	}
	// It must not appear in the generated reference.
	out, err := run(t, "__docs")
	if err != nil || strings.Contains(out, "backup snapshot restore") {
		t.Fatalf("the hidden command leaked into the docs: %v", err)
	}
}
