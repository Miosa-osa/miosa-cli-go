package commands_test

import (
	"strings"
	"testing"
)

const (
	volDataID   = "87d908f0-520a-4028-b2bc-000000000000"
	volMachine  = "c0ffee00-0000-4000-8000-0000000000a1"
	volMachine2 = "c0ffee00-0000-4000-8000-0000000000b2"
	volAttachID = "a7a7a7a7-0000-4000-8000-000000000001"
)

func volumeRoutes() map[string]any {
	return map[string]any{
		"GET /volumes":                                                `{"data":[{"id":"` + volDataID + `","name":"wo05-vol","status":"available","size_bytes":1073741824,"backend":"local"}]}`,
		"GET /sandboxes/by-name/wo05-a":                               `{"data":{"id":"` + volMachine + `"}}`,
		"GET /computers":                                              `{"computers":[{"id":"` + volMachine2 + `","name":"desk"}]}`,
		"POST /computers/" + volMachine2 + "/volumes":                 `{"data":{"id":"` + volAttachID + `","status":"attached"}}`,
		"POST /sandboxes/" + volMachine + "/volumes":                  `{"data":{"id":"` + volAttachID + `","status":"attached"}}`,
		"GET /volumes/" + volDataID + "/attachments":                  `{"data":[{"id":"` + volAttachID + `","computer_id":"` + volMachine + `","mount_path":"/data","read_only":false,"status":"attached"}]}`,
		"DELETE /sandboxes/" + volMachine + "/volumes/" + volAttachID: `{}`,
		"DELETE /volumes/" + volDataID:                                `{}`,
		"GET /volumes/" + volDataID:                                   `{"data":{"id":"` + volDataID + `","name":"wo05-vol","status":"available","size_bytes":1073741824,"backend":"local"}}`,
	}
}

// WO-05 (QA 2026-10-19): `volume create --size-gb 1` sent size_gb and no
// backend and the server answered "s3 backend is not yet supported".
func TestVolumeCreateSendsBytesAndTheLocalBackend(t *testing.T) {
	f := fakeRoutes(t, map[string]any{"POST /volumes": `{"data":{"id":"v1","name":"data","status":"available","size_bytes":1073741824,"backend":"local"}}`})
	if _, err := run(t, "volume", "create", "data", "--size-gb", "1"); err != nil {
		t.Fatal(err)
	}
	body := f.find("POST", "/volumes")[0].Body
	if body["size_bytes"] != float64(1<<30) || body["backend"] != "local" || body["name"] != "data" {
		t.Fatalf("body = %v", body)
	}
	if _, ok := body["size_gb"]; ok {
		t.Fatalf("the size_gb alias must not be sent: %v", body)
	}
}

func TestVolumeCreateReadsFractionsAndRefusesNonsense(t *testing.T) {
	f := fakeRoutes(t, map[string]any{"POST /volumes": `{"data":{"id":"v1"}}`})
	if _, err := run(t, "volume", "create", "half", "--size-gb", "0.5"); err != nil {
		t.Fatal(err)
	}
	if got := f.find("POST", "/volumes")[0].Body["size_bytes"]; got != float64(1<<29) {
		t.Fatalf("size_bytes = %v", got)
	}
	for _, bad := range []string{"0", "-1", "lots"} {
		if _, err := run(t, "volume", "create", "x", "--size-gb", bad); err == nil {
			t.Fatalf("--size-gb %s must be refused", bad)
		}
	}
}

func TestVolumeAttachByNamesToASandbox(t *testing.T) {
	f := fakeRoutes(t, volumeRoutes())
	out, err := run(t, "volume", "attach", "wo05-vol", "wo05-a", "--mount", "/data")
	if err != nil || !strings.Contains(out, "Attached wo05-vol to wo05-a") {
		t.Fatalf("%v\n%s", err, out)
	}
	body := f.find("POST", "/sandboxes/"+volMachine+"/volumes")[0].Body
	if body["volume_id"] != volDataID || body["mount_path"] != "/data" {
		t.Fatalf("body = %v", body)
	}
}

func TestVolumeAttachToAComputerAndReadOnly(t *testing.T) {
	f := fakeRoutes(t, volumeRoutes())
	if _, err := run(t, "volume", "attach", "wo05-vol", "desk", "--mount", "/mnt/x", "--read-only"); err != nil {
		t.Fatal(err)
	}
	body := f.find("POST", "/computers/"+volMachine2+"/volumes")[0].Body
	if body["read_only"] != true {
		t.Fatalf("body = %v", body)
	}
}

func TestVolumeAttachNeedsAMountPath(t *testing.T) {
	fakeRoutes(t, volumeRoutes())
	if _, err := run(t, "volume", "attach", "wo05-vol", "wo05-a"); err == nil {
		t.Fatal("--mount is required")
	}
}

func TestVolumeDetachFindsTheAttachment(t *testing.T) {
	f := fakeRoutes(t, volumeRoutes())
	out, err := run(t, "volume", "detach", "wo05-vol", "wo05-a")
	if err != nil || !strings.Contains(out, "Detached wo05-vol from wo05-a") {
		t.Fatalf("%v\n%s", err, out)
	}
	if len(f.find("DELETE", "/sandboxes/"+volMachine+"/volumes/"+volAttachID)) != 1 {
		t.Fatal("detach must address the attachment id")
	}
}

func TestVolumeDetachWhenNotAttachedSaysSo(t *testing.T) {
	routes := volumeRoutes()
	routes["GET /sandboxes/by-name/other"] = `{"data":{"id":"` + volMachine2 + `"}}`
	fakeRoutes(t, routes)
	_, err := run(t, "volume", "detach", "wo05-vol", "other")
	if err == nil || !strings.Contains(err.Error(), "not attached") {
		t.Fatalf("%v", err)
	}
}

func TestVolumeAttachmentsListsHolders(t *testing.T) {
	fakeRoutes(t, volumeRoutes())
	out, err := run(t, "volume", "attachments", "wo05-vol")
	if err != nil || !strings.Contains(out, "/data") || !strings.Contains(out, "attached") {
		t.Fatalf("%v\n%s", err, out)
	}
}

func TestVolumeListAsksForDeletedOnRequest(t *testing.T) {
	f := fakeRoutes(t, volumeRoutes())
	if _, err := run(t, "volume", "list"); err != nil {
		t.Fatal(err)
	}
	if _, err := run(t, "volume", "list", "--deleted"); err != nil {
		t.Fatal(err)
	}
	calls := f.find("GET", "/volumes")
	if calls[0].Query != "" || !strings.Contains(calls[1].Query, "deleted=true") {
		t.Fatalf("queries = %q, %q", calls[0].Query, calls[1].Query)
	}
}

func TestVolumeGetAndRmTakeANameAndAnUnknownNameIsRefused(t *testing.T) {
	f := fakeRoutes(t, volumeRoutes())
	if out, err := run(t, "volume", "get", "wo05-vol"); err != nil || !strings.Contains(out, "local") {
		t.Fatalf("%v\n%s", err, out)
	}
	if _, err := run(t, "volume", "rm", "wo05-vol", "-y"); err != nil {
		t.Fatal(err)
	}
	if len(f.find("DELETE", "/volumes/"+volDataID)) != 1 {
		t.Fatal("rm must delete by the resolved id")
	}
	if _, err := run(t, "volume", "get", "nope"); err == nil || !strings.Contains(err.Error(), "no volume named") {
		t.Fatalf("%v", err)
	}
}

const volSnapID = "5a5a5a5a-0000-4000-8000-000000000009"

func snapshotRoutes() map[string]any {
	routes := volumeRoutes()
	routes["POST /volumes/"+volDataID+"/snapshots"] = `{"data":{"id":"` + volSnapID + `","status":"ready","kind":"manual","size_bytes":1073741824}}`
	routes["GET /volumes/"+volDataID+"/snapshots"] = `{"data":[{"id":"` + volSnapID + `","status":"ready","kind":"scheduled","size_bytes":1073741824,"expires_at":"2026-10-26T03:30:00Z","created_at":"2026-10-19T03:30:00Z"}]}`
	routes["DELETE /volume-snapshots/"+volSnapID] = `{"data":{"id":"` + volSnapID + `","status":"deleted"}}`
	routes["POST /volume-snapshots/"+volSnapID+"/restore"] = `{"data":{"id":"` + volDataID + `","name":"wo05-vol-r","status":"available","size_bytes":1073741824}}`
	return routes
}

func TestVolumeSnapshotTakesOneByVolumeName(t *testing.T) {
	f := fakeRoutes(t, snapshotRoutes())
	out, err := run(t, "volume", "snapshot", "wo05-vol")
	if err != nil || !strings.Contains(out, "ready") {
		t.Fatalf("%v\n%s", err, out)
	}
	if len(f.find("POST", "/volumes/"+volDataID+"/snapshots")) != 1 {
		t.Fatal("the snapshot must be requested on the resolved volume id")
	}
}

func TestVolumeSnapshotsListsThemWithRetention(t *testing.T) {
	fakeRoutes(t, snapshotRoutes())
	out, err := run(t, "volume", "snapshots", "wo05-vol")
	if err != nil || !strings.Contains(out, "scheduled") || !strings.Contains(out, "2026-10-26") {
		t.Fatalf("%v\n%s", err, out)
	}
}

func TestVolumeRestoreToNewResolvesAShortSnapshotId(t *testing.T) {
	f := fakeRoutes(t, snapshotRoutes())
	out, err := run(t, "volume", "fork", volSnapID[:8], "--to-new", "wo05-vol-r")
	if err != nil || !strings.Contains(out, "wo05-vol-r") {
		t.Fatalf("%v\n%s", err, out)
	}
	body := f.find("POST", "/volume-snapshots/"+volSnapID+"/restore")[0].Body
	if body["name"] != "wo05-vol-r" {
		t.Fatalf("body = %v", body)
	}
	if _, err := run(t, "volume", "fork", volSnapID[:8]); err == nil {
		t.Fatal("--to-new is required")
	}
}

func TestVolumeSnapshotRmNeedsConfirmationAndAnUnknownIdIsRefused(t *testing.T) {
	f := fakeRoutes(t, snapshotRoutes())
	if _, err := run(t, "volume", "snapshot-rm", volSnapID); err == nil || !strings.Contains(err.Error(), "--yes") {
		t.Fatalf("must ask for --yes: %v", err)
	}
	if len(f.find("DELETE", "/volume-snapshots/"+volSnapID)) != 0 {
		t.Fatal("nothing may be sent before confirmation")
	}
	if _, err := run(t, "volume", "snapshot-rm", volSnapID, "-y"); err != nil {
		t.Fatal(err)
	}
	if _, err := run(t, "volume", "snapshot-rm", "ffff0000", "-y"); err == nil || !strings.Contains(err.Error(), "no snapshot starts with") {
		t.Fatalf("%v", err)
	}
}
