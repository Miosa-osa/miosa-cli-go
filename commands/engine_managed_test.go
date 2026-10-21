package commands_test

import (
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
)

const (
	engineID     = "5dce0080-bc56-41a8-8596-74ed6a431b54"
	otherEngine  = "0a4d3936-bc6f-41b9-a425-6daece5602ff"
	engineListJS = `{"data":[{"id":"` + engineID + `","name":"qa-brain","engine":"optimal","engine_version":"0.3","state":"running","host":"172.16.128.18","port":4200}]}`
)

func TestEngineGetByNamesResolvesAndShowsTheRecord(t *testing.T) {
	f := fakeRoutes(t, map[string]any{
		"GET /databases":             engineListJS,
		"GET /databases/" + engineID: `{"id":"` + engineID + `","name":"qa-brain","engine":"optimal","engine_version":"0.3","state":"running","host":"172.16.128.18","port":4200,"workspace_id":"b89beea8"}`,
	})

	out, err := run(t, "engine", "get", "qa-brain")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	for _, want := range []string{"qa-brain", "optimal", "0.3", "running", "172.16.128.18"} {
		if !strings.Contains(out, want) {
			t.Fatalf("output is missing %q:\n%s", want, out)
		}
	}
	if len(f.find("GET", "/databases/"+engineID)) != 1 {
		t.Fatalf("engine get must read the resolved id: %v", f.find("GET", "/databases/"+engineID))
	}
}

func TestEngineListLeavesOtherDatabaseEnginesOut(t *testing.T) {
	f := fakeRoutes(t, map[string]any{
		"GET /databases": `{"data":[
			{"id":"` + engineID + `","name":"qa-brain","engine":"optimal","engine_version":"0.3","state":"running"},
			{"id":"` + otherEngine + `","name":"app-db","engine":"postgresql","engine_version":"16","state":"running"}]}`,
	})

	out, err := run(t, "engine", "list")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !strings.Contains(out, "qa-brain") {
		t.Fatalf("the Engine must be listed: %s", out)
	}
	if strings.Contains(out, "app-db") {
		t.Fatalf("a postgres database must not be listed as an Engine: %s", out)
	}
	if len(f.find("GET", "/databases")) != 1 {
		t.Fatalf("calls = %v", f.find("GET", "/databases"))
	}
}

func TestEngineCreateSendsTheManagedEngineBody(t *testing.T) {
	f := fakeRoutes(t, map[string]any{
		"GET /databases":  `{"data":[]}`,
		"POST /databases": resp{201, `{"id":"` + engineID + `","name":"qa-brain","engine":"optimal","engine_version":"0.3","state":"provisioning"}`},
	})

	out, err := run(t, "engine", "create", "qa-brain", "--workspace", "qa", "--size", "small")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !strings.Contains(out, "Created qa-brain") {
		t.Fatalf("output = %s", out)
	}
	posts := f.find("POST", "/databases")
	if len(posts) != 1 {
		t.Fatalf("posts = %d", len(posts))
	}
	body := posts[0].Body
	if body["engine"] != "optimal" || body["name"] != "qa-brain" || body["workspace_slug"] != "qa" || body["size"] != "small" {
		t.Fatalf("body = %v", body)
	}
}

func TestEngineCreateIsIdempotentByEngineName(t *testing.T) {
	f := fakeRoutes(t, map[string]any{"GET /databases": engineListJS})

	out, err := run(t, "engine", "create", "qa-brain")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !strings.Contains(out, "already exists") {
		t.Fatalf("output = %s", out)
	}
	if len(f.find("POST", "/databases")) != 0 {
		t.Fatalf("a second create must not create: %v", f.find("POST", "/databases"))
	}
}

func TestEngineCreateDoesNotAdoptAnotherEnginesDatabase(t *testing.T) {
	f := fakeRoutes(t, map[string]any{
		"GET /databases":  `{"data":[{"id":"` + otherEngine + `","name":"qa-brain","engine":"postgresql","state":"running"}]}`,
		"POST /databases": resp{201, `{"id":"` + engineID + `","name":"qa-brain","engine":"optimal","state":"provisioning"}`},
	})

	if _, err := run(t, "engine", "create", "qa-brain"); err != nil {
		t.Fatal(err)
	}
	if len(f.find("POST", "/databases")) != 1 {
		t.Fatalf("a same-named postgres database is not this Engine")
	}
}

func TestEngineCreateWaitFollowsTheStates(t *testing.T) {
	var reads int32
	f := newFakeAPI(t, func(c call) (int, string) {
		switch {
		case c.is("GET", "/databases"):
			return http.StatusOK, `{"data":[]}`
		case c.is("POST", "/databases"):
			return http.StatusCreated, `{"id":"` + engineID + `","name":"qa-brain","engine":"optimal","state":"provisioning"}`
		case c.is("GET", "/databases/"+engineID):
			if atomic.AddInt32(&reads, 1) == 1 {
				return http.StatusOK, `{"id":"` + engineID + `","name":"qa-brain","engine":"optimal","state":"provisioning"}`
			}
			return http.StatusOK, `{"id":"` + engineID + `","name":"qa-brain","engine":"optimal","state":"running"}`
		}
		return http.StatusNotFound, `{"error":{"code":"NOT_FOUND"}}`
	})

	out, err := run(t, "engine", "create", "qa-brain", "--wait", "--timeout", "10")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !strings.Contains(out, "qa-brain: running") || !strings.Contains(out, "qa-brain is running") {
		t.Fatalf("output = %s", out)
	}
	if atomic.LoadInt32(&reads) != 2 {
		t.Fatalf("wait read the record %d times, want 2", reads)
	}
	_ = f
}

func TestDBCreateAcceptsOptimal(t *testing.T) {
	f := fakeRoutes(t, map[string]any{
		"POST /databases": resp{201, `{"id":"` + engineID + `","name":"qa-brain","engine":"optimal","state":"provisioning"}`},
	})

	if _, err := run(t, "db", "create", "qa-brain", "--engine", "optimal"); err != nil {
		t.Fatalf("db create --engine optimal must be accepted: %v", err)
	}
	if body := f.find("POST", "/databases")[0].Body; body["engine"] != "optimal" {
		t.Fatalf("body = %v", body)
	}
}

// engineCatalogJS is the list response the size validation reads: an empty
// Engine list plus the published size ids.
const engineCatalogJS = `{"data":[],"meta":{"engines":{"catalog":{"optimal":{"sizes":[
{"id":"tiny"},{"id":"xs"},{"id":"small"},{"id":"medium"},{"id":"large"}]}}}}}`

func TestEngineCreateForPresetPicksSizeAndDisk(t *testing.T) {
	f := fakeRoutes(t, map[string]any{
		"GET /databases":  engineCatalogJS,
		"POST /databases": resp{201, `{"id":"` + engineID + `","name":"qa-brain","engine":"optimal","state":"provisioning"}`},
	})

	out, err := run(t, "engine", "create", "qa-brain", "--for", "workspace")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	body := f.find("POST", "/databases")[0].Body
	if body["size"] != "xs" {
		t.Errorf("size = %v, want xs for --for workspace", body["size"])
	}
	if body["storage_mb"] != float64(10*1024) && body["storage_mb"] != 10*1024 {
		t.Errorf("storage_mb = %v, want 10240 for --for workspace", body["storage_mb"])
	}
}

func TestEngineCreateSizeAndDiskOverrideFor(t *testing.T) {
	f := fakeRoutes(t, map[string]any{
		"GET /databases":  engineCatalogJS,
		"POST /databases": resp{201, `{"id":"` + engineID + `","name":"qa-brain","engine":"optimal","state":"provisioning"}`},
	})

	if _, err := run(t, "engine", "create", "qa-brain", "--for", "business", "--size", "medium", "--disk", "40"); err != nil {
		t.Fatal(err)
	}
	body := f.find("POST", "/databases")[0].Body
	if body["size"] != "medium" {
		t.Errorf("size = %v, --size must win over --for", body["size"])
	}
	if body["storage_mb"] != float64(40*1024) && body["storage_mb"] != 40*1024 {
		t.Errorf("storage_mb = %v, --disk must win over --for", body["storage_mb"])
	}
}

func TestEngineCreateRejectsUnknownFor(t *testing.T) {
	f := fakeRoutes(t, map[string]any{"GET /databases": engineCatalogJS})

	_, err := run(t, "engine", "create", "qa-brain", "--for", "laptop")
	if err == nil || !strings.Contains(err.Error(), "agent, workspace or business") {
		t.Fatalf("err = %v, want the accepted values", err)
	}
	if len(f.find("POST", "/databases")) != 0 {
		t.Fatalf("nothing may be created for an unknown --for")
	}
}

func TestEngineCreateRefusesDiskWithStorageMB(t *testing.T) {
	f := fakeRoutes(t, map[string]any{"GET /databases": engineCatalogJS})

	_, err := run(t, "engine", "create", "qa-brain", "--disk", "4", "--storage-mb", "1024")
	if err == nil || !strings.Contains(err.Error(), "not both") {
		t.Fatalf("err = %v, want a refusal naming both flags", err)
	}
	if len(f.find("POST", "/databases")) != 0 {
		t.Fatalf("nothing may be created when the disk is given twice")
	}
}

func TestEngineCreateChecksSizeAgainstTheDatabaseSizeList(t *testing.T) {
	f := fakeRoutes(t, map[string]any{"GET /databases": engineCatalogJS})

	_, err := run(t, "engine", "create", "qa-brain", "--size", "enormous")
	if err == nil || !strings.Contains(err.Error(), "tiny, xs, small, medium, large") {
		t.Fatalf("err = %v, want the published sizes", err)
	}
	if len(f.find("POST", "/databases")) != 0 {
		t.Fatalf("an unpublished size must not be sent")
	}
}

const engineUsageJS = `{"data":{"sampled_at":"2026-10-20T18:00:00Z","size":"small","limits":[
{"id":"vectors","label":"Vectors","used":1200,"limit":10000,"unit":"vectors"},
{"id":"disk","label":"Disk","used":1610612736,"limit":10737418240,"unit":"bytes"},
{"id":"memory","label":"Memory","used":104857600,"limit":null,"unit":"bytes"},
{"id":"write_rate","label":"Write rate","used":3.5,"limit":null,"unit":"per_second"}]}}`

func TestEngineGetShowsUsageGauges(t *testing.T) {
	f := fakeRoutes(t, map[string]any{
		"GET /databases":                               engineListJS,
		"GET /databases/" + engineID:                   `{"id":"` + engineID + `","name":"qa-brain","engine":"optimal","engine_version":"0.3","state":"running"}`,
		"GET /databases/" + engineID + "/engine-usage": engineUsageJS,
	})

	out, err := run(t, "engine", "get", "qa-brain")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	for _, want := range []string{"1200 / 10000 (12%)", "1.5 GB / 10.0 GB (15%)", "100.0 MB", "3.5/s"} {
		if !strings.Contains(out, want) {
			t.Fatalf("gauges are missing %q:\n%s", want, out)
		}
	}
	if len(f.find("GET", "/databases/"+engineID+"/engine-usage")) != 1 {
		t.Fatalf("engine get must read the usage route: %v", f.find("GET", "/databases/"+engineID+"/engine-usage"))
	}
}

func TestEngineGetCarriesUsageInJSON(t *testing.T) {
	fakeRoutes(t, map[string]any{
		"GET /databases":                               engineListJS,
		"GET /databases/" + engineID:                   `{"id":"` + engineID + `","name":"qa-brain","engine":"optimal","state":"running"}`,
		"GET /databases/" + engineID + "/engine-usage": engineUsageJS,
	})

	out, err := run(t, "engine", "get", "qa-brain", "--json")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	for _, want := range []string{`"usage"`, `"vectors"`, `"sampled_at"`} {
		if !strings.Contains(out, want) {
			t.Fatalf("--json is missing %s:\n%s", want, out)
		}
	}
}

func TestEngineGetStillPrintsWhenUsageIsUnavailable(t *testing.T) {
	fakeRoutes(t, map[string]any{
		"GET /databases":                               engineListJS,
		"GET /databases/" + engineID:                   `{"id":"` + engineID + `","name":"qa-brain","engine":"optimal","state":"running"}`,
		"GET /databases/" + engineID + "/engine-usage": resp{503, `{"error":{"code":"ENGINE_UNREACHABLE"}}`},
	})

	out, err := run(t, "engine", "get", "qa-brain")
	if err != nil {
		t.Fatalf("a down Engine must not fail the record read: %v\n%s", err, out)
	}
	if !strings.Contains(out, "qa-brain") || !strings.Contains(out, "running") {
		t.Fatalf("the record must print without gauges:\n%s", out)
	}
}
