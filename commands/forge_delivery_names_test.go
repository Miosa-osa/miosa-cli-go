package commands

import (
	"encoding/json"
	"testing"
)

func TestDeliveryLegacyJSONRenamesTheAPIBoard(t *testing.T) {
	api := `{"live":true,"issues":[{"id":7,"key":"MIO-7","phase":"m:a","state":"deployed","spec_state":"approved","acceptance_criteria":"Green","domain":"ops","mission":null}],
	"phases":[{"id":"m:a","acceptance_criteria":"All green"}],
	"agent_sessions":[{"id":"platform-5","crew":"m","issue":7}],
	"crew":[{"id":"platform","deployed":3,"queue":[7]}],
	"domains":[{"id":"ops","title":"Ops","status":"open"}],
	"missions":[{"id":"m","crew":{"capacity":{"platform":2}}}],
	"audit_log":[{"id":1,"issues":[7]}],"triage":[{"id":"n1","issues":[7]}],
	"check_runs":[{"id":"c1","issue":7}],"activity":[{"id":1,"issue":7}],
	"charters":[{"text":"workspace","scope":{"kind":"workspace","id":"w"}},{"text":"org","scope":{"kind":"organization"}}],
	"settings":{"autonomy_minutes":30}}`
	var s deliveryState
	if err := json.Unmarshal(deliveryLegacyJSON([]byte(api), true), &s); err != nil {
		t.Fatal(err)
	}
	order := s.Orders[0]
	if order.ID != 7 || order.Milestone != "m:a" || order.State != "shipped" || order.SpecState == nil || *order.SpecState != "approved" || order.DoneWhen != "Green" || order.Mission != "ops" {
		t.Fatalf("issue → order: %+v", order)
	}
	if s.Milestones[0].ID != "m:a" || s.Runners[0].WorkOrder == nil || *s.Runners[0].WorkOrder != 7 {
		t.Fatalf("phases/agent sessions: %+v %+v", s.Milestones, s.Runners)
	}
	if len(s.Missions) != 2 || s.Missions[1].ID != "ops" || s.Missions[1].Status != "standing" {
		t.Fatalf("a Domain is a standing Mission: %+v", s.Missions)
	}
	if s.Charter == nil || s.Charter.Text != "org" {
		t.Fatalf("charter: %+v", s.Charter)
	}
	var raw map[string]any
	_ = json.Unmarshal(deliveryLegacyJSON([]byte(api), true), &raw)
	field := func(collection, name string) any { return raw[collection].([]any)[0].(map[string]any)[name] }
	for _, check := range []struct {
		collection, name string
		want             any
	}{{"runners", "mission", "m"}, {"activity", "workOrder", 7.0}, {"checkRuns", "workOrder", 7.0}, {"crew", "shipped", 3.0}} {
		if got := field(check.collection, check.name); got != check.want {
			t.Fatalf("%s.%s = %v, want %v", check.collection, check.name, got, check.want)
		}
	}
	if len(field("records", "workOrders").([]any)) != 1 || len(field("intake", "workOrders").([]any)) != 1 {
		t.Fatalf("audit log/triage: %v %v", raw["records"], raw["intake"])
	}
	seats := raw["missions"].([]any)[0].(map[string]any)["crew"].(map[string]any)["seats"]
	if seats == nil || raw["settings"].(map[string]any)["autonomyMinutes"] != 30.0 {
		t.Fatalf("capacity → seats, autonomy: %v %v", raw["missions"], raw["settings"])
	}
}
