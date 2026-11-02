package commands_test

import (
	"strings"
	"testing"
	"time"
)

// DOMAIN (MIO-372), T-RESP (MIO-373) and M-HEALTH (MIO-370) in the CLI: Domains, the triage clock in
// the Inbox, the Mission's owner, update and computed health, and the Initiative's rollup.

func TestForgeDeliveryDomainNewSetShow(t *testing.T) {
	state := map[string]any{
		"domains": []map[string]any{{
			"id": "console", "name": "Console", "owner": "", "crew": "frontend", "triage": "Roberto",
			"sla":        map[string]int{"P0": 24, "P1": 168},
			"components": []map[string]any{{"repo": "MiosaOS", "trunk": "main", "releaseScheme": "calver"}},
			"standing":   "",
		}},
		"orders": []map[string]any{
			{"id": 4, "title": "Sidebar overlaps the modal", "owner": "frontend", "priority": "P1", "state": "todo", "domain": "console", "inMission": true, "triage": map[string]any{"person": "Roberto", "at": 1, "by": nil, "stoppedAt": nil}},
			{"id": 5, "title": "Shipped one", "owner": "frontend", "priority": "P2", "state": "shipped", "domain": "console", "inMission": true},
		},
	}
	calls, stop := fakeDelivery(t, map[string]any{"state": state, "createDomain": map[string]any{"id": "optimal-engine"}, "editDomain": map[string]any{"ok": true}})
	defer stop()

	out, err := runDelivery(t, "", "forge", "domain", "new", "Optimal", "Engine", "--crew", "platform", "--component", "optimal:main:semver", "--component", "optimal-docs", "--triage", "Roberto")
	if err != nil || !strings.Contains(out, "Domain optimal-engine") {
		t.Fatalf("new: %v %q", err, out)
	}
	body := (*calls)[0].body
	components := body["components"].([]any)
	if body["name"] != "Optimal Engine" || body["crew"] != "platform" || body["triage"] != "Roberto" || body["by"] != "claude-code" || len(components) != 2 {
		t.Fatalf("new payload %#v", body)
	}
	first := components[0].(map[string]any)
	second := components[1].(map[string]any)
	if first["repo"] != "optimal" || first["trunk"] != "main" || first["releaseScheme"] != "semver" || second["trunk"] != "main" || second["releaseScheme"] != "calver" {
		t.Fatalf("components %#v", components)
	}
	if _, err := runDelivery(t, "", "forge", "domain", "new", "Bad", "--component", "x:main:dates"); err == nil || !strings.Contains(err.Error(), "calver or semver") {
		t.Fatalf("a wrong release scheme should be refused: %v", err)
	}

	if out, err := runDelivery(t, "", "forge", "domain", "set", "console", "--owner", "Roberto"); err != nil || !strings.Contains(out, "Domain console updated") {
		t.Fatalf("set: %v %q", err, out)
	}
	set := (*calls)[1]
	if set.action != "editDomain" || set.body["id"] != "console" || set.body["owner"] != "Roberto" {
		t.Fatalf("set payload %#v", set)
	}
	if _, ok := set.body["triage"]; ok {
		t.Fatalf("a flag not given must be left out, so the service keeps the triage person: %#v", set.body)
	}

	out, err = runDelivery(t, "", "forge", "domain", "show", "console")
	if err != nil {
		t.Fatalf("show: %v", err)
	}
	for _, want := range []string{"console  Console", "1 open · 1 on the triage clock", "crew frontend · owner not set · triage Roberto · SLA P0 24h, P1 168h", "- MiosaOS → main (calver)", "MIO-4"} {
		if !strings.Contains(out, want) {
			t.Fatalf("show missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "MIO-5") {
		t.Fatalf("a shipped Issue is not in the Backlog:\n%s", out)
	}
	if _, err := runDelivery(t, "", "forge", "domain", "show", "nope"); err == nil || !strings.Contains(err.Error(), "no Domain nope") {
		t.Fatalf("show unknown: %v", err)
	}
}

// MIO-64: the Delivery API writes a Domain's crew as {"role", "capacity"}; every command that reads
// the board must still work, and the display prints the role.
func TestForgeDeliveryDomainCrewObjectFromTheAPI(t *testing.T) {
	domain := map[string]any{"id": "console", "name": "Console", "owner": "Roberto", "crew": map[string]any{"role": "backend", "capacity": map[string]int{"backend": 2}}, "triage": "", "sla": map[string]int{"P0": 24, "P1": 168}, "components": []any{}, "standing": ""}

	t.Run("forge-local", func(t *testing.T) {
		_, stop := fakeDelivery(t, map[string]any{"state": map[string]any{"domains": []any{domain}, "orders": []any{}}})
		defer stop()
		out, err := runDelivery(t, "", "forge", "domain", "show", "console")
		if err != nil || !strings.Contains(out, "crew backend · owner Roberto · triage nobody yet") {
			t.Fatalf("show: %v\n%s", err, out)
		}
		if _, err := runDelivery(t, "", "forge", "ls"); err != nil {
			t.Fatalf("ls must read a board with a Domain: %v", err)
		}
	})
	t.Run("api", func(t *testing.T) {
		fakeDeliveryAPI(t, map[string]any{"state": map[string]any{"live": true, "domains": []any{domain}, "issues": []any{}, "crew": []any{}}})
		out, err := runDelivery(t, "", "forge", "domain", "show", "console")
		if err != nil || !strings.Contains(out, "crew backend · owner Roberto") {
			t.Fatalf("show: %v\n%s", err, out)
		}
		if _, err := runDelivery(t, "", "forge", "ls"); err != nil {
			t.Fatalf("ls must read a board with a Domain: %v", err)
		}
	})
}

func TestForgeDeliveryAddAndPlaceSendTheDomain(t *testing.T) {
	calls, stop := fakeDelivery(t, map[string]any{"add": []any{map[string]any{"id": 9, "kind": "created", "summary": "Backend · P1"}}, "placeOrder": map[string]any{"where": "starts next"}})
	defer stop()
	if _, err := runDelivery(t, "", "forge", "add", "The", "invoice", "total", "is", "wrong", "--domain", "billing"); err != nil {
		t.Fatal(err)
	}
	if (*calls)[0].body["domain"] != "billing" {
		t.Fatalf("add payload %#v", (*calls)[0].body)
	}
	if _, err := runDelivery(t, "", "forge", "place", "MIO-9", "--mission", "m", "--domain", "security"); err != nil {
		t.Fatal(err)
	}
	if (*calls)[1].body["domain"] != "security" || (*calls)[1].body["mission"] != "m" {
		t.Fatalf("place payload %#v", (*calls)[1].body)
	}
}

func TestForgeDeliveryMissionOwnerAndUpdate(t *testing.T) {
	calls, stop := fakeDelivery(t, map[string]any{"missionOwner": map[string]any{"ok": true}, "missionUpdate": map[string]any{"at": 1, "by": "claude-code", "health": "at_risk", "text": "x"}, "createMission": map[string]any{"id": "m"}})
	defer stop()

	out, err := runDelivery(t, "", "forge", "mission", "owner", "release-pipeline-v2", "Roberto")
	if err != nil || !strings.Contains(out, "Roberto owns release-pipeline-v2") {
		t.Fatalf("owner: %v %q", err, out)
	}
	if (*calls)[0].action != "missionOwner" || (*calls)[0].body["owner"] != "Roberto" || (*calls)[0].body["id"] != "release-pipeline-v2" {
		t.Fatalf("owner payload %#v", (*calls)[0])
	}
	if _, err := runDelivery(t, "", "forge", "mission", "owner", "release-pipeline-v2"); err == nil {
		t.Fatal("owner without a person should be refused")
	}

	out, err = runDelivery(t, "", "forge", "mission", "update", "release-pipeline-v2", "Phase", "2", "merged;", "staging", "waits")
	if err != nil || !strings.Contains(out, "Update written for release-pipeline-v2 (at risk)") {
		t.Fatalf("update: %v %q", err, out)
	}
	if (*calls)[1].body["text"] != "Phase 2 merged; staging waits" || (*calls)[1].body["by"] != "claude-code" {
		t.Fatalf("update payload %#v", (*calls)[1].body)
	}
	if _, err := runDelivery(t, "From stdin\n", "forge", "mission", "update", "release-pipeline-v2", "--text", "-"); err != nil {
		t.Fatal(err)
	}
	if (*calls)[2].body["text"] != "From stdin" {
		t.Fatalf("update from stdin %#v", (*calls)[2].body)
	}

	if _, err := runDelivery(t, "", "forge", "mission", "new", "Owned", "--goal", "g", "--owner", "Dana", "--organization"); err != nil {
		t.Fatal(err)
	}
	if (*calls)[3].body["owner"] != "Dana" {
		t.Fatalf("mission new payload %#v", (*calls)[3].body)
	}
}

func TestForgeDeliveryStatusInboxAndInitiativeShowTheHealth(t *testing.T) {
	soon := time.Now().Add(90 * time.Minute).UnixMilli()
	late := time.Now().Add(-2 * time.Hour).UnixMilli()
	state := map[string]any{
		"missions": []map[string]any{
			{"id": "m1", "title": "Move the code onto Forge", "status": "active", "owner": "Roberto", "health": map[string]any{"state": "off_track", "reasons": []string{"P0 open: MIO-3 Production is down"}, "at": 1}},
			{"id": "m2", "title": "Forge Delivery v1", "status": "planned"},
		},
		"initiatives": []map[string]any{{"id": "one", "title": "One delivery system", "owner": "Roberto", "status": "active", "health": "red", "missions": []string{"m1", "m2"}, "counts": map[string]int{"planned": 2, "plannedShipped": 1}}},
		"orders": []map[string]any{
			{"id": 3, "title": "Production is down", "owner": "backend", "priority": "P0", "state": "todo", "domain": "production-upkeep", "inMission": true, "triage": map[string]any{"person": "", "at": 1, "by": soon, "stoppedAt": nil}},
			{"id": 4, "title": "Invoice wrong", "owner": "backend", "priority": "P1", "state": "todo", "domain": "billing", "inMission": true, "triage": map[string]any{"person": "Roberto", "at": 1, "by": late, "stoppedAt": nil}},
			{"id": 5, "title": "Placed already", "owner": "backend", "priority": "P1", "state": "todo", "domain": "billing", "inMission": true, "triage": map[string]any{"person": "Roberto", "at": 1, "by": late, "stoppedAt": 2}},
		},
	}
	_, stop := fakeDelivery(t, map[string]any{"state": state})
	defer stop()

	out, err := runDelivery(t, "", "forge", "status")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"off track", "owner Roberto", "! P0 open: MIO-3 Production is down"} {
		if !strings.Contains(out, want) {
			t.Fatalf("status missing %q:\n%s", want, out)
		}
	}

	out, err = runDelivery(t, "", "forge", "inbox")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"triage MIO-3 Production is down (production-upkeep) due in 1h30m0s", "triage MIO-4 Invoice wrong (billing) 2h0m0s overdue · Roberto on triage", "off track Move the code onto Forge: P0 open"} {
		if !strings.Contains(out, want) {
			t.Fatalf("inbox missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "MIO-5") || strings.Contains(out, "Nothing needs you") {
		t.Fatalf("a stopped clock is not in the Inbox:\n%s", out)
	}

	out, err = runDelivery(t, "", "forge", "initiative", "show", "one")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"active · health red · owner Roberto", "- m1 (active · off track) Move the code onto Forge", "- m2 (planned) Forge Delivery v1"} {
		if !strings.Contains(out, want) {
			t.Fatalf("initiative show missing %q:\n%s", want, out)
		}
	}
}
