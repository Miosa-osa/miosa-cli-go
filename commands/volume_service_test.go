package commands_test

import (
	"reflect"
	"strings"
	"testing"
)

const (
	vsProject = "11111111-0000-4000-8000-000000000001"
	vsService = "22222222-0000-4000-8000-000000000002"
)

func serviceVolumeRoutes(settings string) map[string]any {
	return map[string]any{
		"GET /projects": `{"data":[{"id":"` + vsProject + `","name":"Default","slug":"default","is_default":true}]}`,
		"GET /projects/" + vsProject + "/services":                `{"data":[{"id":"` + vsService + `","name":"web","slug":"web","lock_version":4,"settings":` + settings + `}]}`,
		"PATCH /projects/" + vsProject + "/services/" + vsService: `{"data":{"id":"` + vsService + `"}}`,
	}
}

func TestVolumeMountAddsToTheServiceSettingsWithItsLockVersion(t *testing.T) {
	f := fakeRoutes(t, serviceVolumeRoutes(`{"replicas":1,"volumes":[{"volume":"cache","mount_path":"/cache","read_only":true}]}`))
	out, err := run(t, "volume", "mount", "data", "web", "--mount", "/data")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "next deploy") {
		t.Fatalf("output = %q", out)
	}
	body := f.find("PATCH", "/projects/"+vsProject+"/services/"+vsService)[0].Body
	if body["lock_version"] != float64(4) {
		t.Fatalf("body = %v", body)
	}
	settings := body["settings"].(map[string]any)
	if settings["replicas"] != float64(1) {
		t.Fatalf("other settings must be sent back unchanged: %v", settings)
	}
	want := []any{
		map[string]any{"volume": "cache", "mount_path": "/cache", "read_only": true},
		map[string]any{"volume": "data", "mount_path": "/data"},
	}
	if !reflect.DeepEqual(settings["volumes"], want) {
		t.Fatalf("volumes = %v", settings["volumes"])
	}
}

func TestVolumeMountReadOnlyAndRefusesADuplicate(t *testing.T) {
	f := fakeRoutes(t, serviceVolumeRoutes(`{}`))
	if _, err := run(t, "volume", "mount", "data", "web", "--mount", "/data", "--read-only"); err != nil {
		t.Fatal(err)
	}
	body := f.find("PATCH", "/projects/"+vsProject+"/services/"+vsService)[0].Body
	vols := body["settings"].(map[string]any)["volumes"].([]any)
	if vols[0].(map[string]any)["read_only"] != true {
		t.Fatalf("volumes = %v", vols)
	}

	g := fakeRoutes(t, serviceVolumeRoutes(`{"volumes":[{"volume":"data","mount_path":"/data"}]}`))
	if _, err := run(t, "volume", "mount", "data", "web", "--mount", "/other"); err == nil {
		t.Fatal("a volume mounted twice must be refused")
	}
	if n := len(g.find("PATCH", "/projects/"+vsProject+"/services/"+vsService)); n != 0 {
		t.Fatalf("nothing may be sent, got %d PATCH", n)
	}
}

func TestVolumeUnmountRemovesItAndDropsTheKeyWhenEmpty(t *testing.T) {
	f := fakeRoutes(t, serviceVolumeRoutes(`{"replicas":1,"volumes":[{"volume":"data","mount_path":"/data"}]}`))
	if _, err := run(t, "volume", "unmount", "data", "web"); err != nil {
		t.Fatal(err)
	}
	settings := f.find("PATCH", "/projects/"+vsProject+"/services/"+vsService)[0].Body["settings"].(map[string]any)
	if _, ok := settings["volumes"]; ok || settings["replicas"] != float64(1) {
		t.Fatalf("settings = %v", settings)
	}
	if _, err := run(t, "volume", "unmount", "nope", "web"); err == nil {
		t.Fatal("unmounting a volume that is not mounted must fail")
	}
}
