package commands_test

import (
	"strings"
	"testing"
)

// RELATIONS (MIO-371) and CUSTOMERS (MIO-374): the verbs send what forge-local expects and print
// what came back.

func TestForgeDeliveryBlockSendsTheBlockerAndNeedsBoth(t *testing.T) {
	calls, stop := fakeDelivery(t, map[string]any{"block": map[string]any{"ok": true, "blockedBy": []any{9, 4}}})
	defer stop()

	if _, err := runDelivery(t, "", "forge", "block", "MIO-12"); err == nil || !strings.Contains(err.Error(), "--by") {
		t.Fatalf("block without --by: %v", err)
	}
	out, err := runDelivery(t, "", "forge", "block", "MIO-12", "--by", "MIO-9")
	if err != nil {
		t.Fatalf("block: %v %s", err, out)
	}
	if !strings.Contains(out, "MIO-12 waits on MIO-9, MIO-4.") {
		t.Fatalf("block printed %q", out)
	}
	call := (*calls)[0]
	if call.action != "block" || call.body["id"] != float64(12) || call.body["blocker"] != float64(9) || call.body["by"] != "claude-code" {
		t.Fatalf("block payload %#v", call.body)
	}
}

func TestForgeDeliveryDuplicateNamesTheCanonicalAndWhatMoved(t *testing.T) {
	calls, stop := fakeDelivery(t, map[string]any{"duplicate": map[string]any{"ok": true, "canonical": 12, "moved": map[string]any{"attachments": 2, "requests": 1}}})
	defer stop()

	out, err := runDelivery(t, "", "forge", "duplicate", "MIO-14", "--of", "MIO-12")
	if err != nil {
		t.Fatalf("duplicate: %v %s", err, out)
	}
	if !strings.Contains(out, "MIO-14 is a duplicate of MIO-12: moved 2 attachments and 1 request.") {
		t.Fatalf("duplicate printed %q", out)
	}
	if body := (*calls)[0].body; body["id"] != float64(14) || body["of"] != float64(12) {
		t.Fatalf("duplicate payload %#v", body)
	}
}

func TestForgeDeliverySplitSendsEveryChildTitleAndSaysWhatWasCreated(t *testing.T) {
	calls, stop := fakeDelivery(t, map[string]any{"split": map[string]any{"children": []any{20, 21}, "created": []any{21}}})
	defer stop()

	t.Setenv("FORGE_RUNNER", "orchestrator-2")
	out, err := runDelivery(t, "", "forge", "split", "MIO-12", "Retry on 429", "Retry on 5xx")
	if err != nil {
		t.Fatalf("split: %v %s", err, out)
	}
	if !strings.Contains(out, "Split MIO-12 into MIO-21.") || !strings.Contains(out, "MIO-12 ships when its last child does") {
		t.Fatalf("split printed %q", out)
	}
	body := (*calls)[0].body
	titles, _ := body["titles"].([]any)
	if len(titles) != 2 || titles[0] != "Retry on 429" || titles[1] != "Retry on 5xx" || body["by"] != "orchestrator-2" {
		t.Fatalf("split payload %#v", body)
	}
	if _, err := runDelivery(t, "", "forge", "split", "MIO-12"); err == nil {
		t.Fatal("split without titles must fail")
	}
}

func TestForgeDeliveryRequestAndAddCarryTheCustomer(t *testing.T) {
	calls, stop := fakeDelivery(t, map[string]any{
		"request": map[string]any{"added": true, "customer": "acme", "requests": 2},
		"add":     []any{map[string]any{"id": 81, "kind": "created", "summary": "Backend · P1"}},
	})
	defer stop()

	out, err := runDelivery(t, "", "forge", "request", "MIO-12", "--customer", "acme", "--important", "--source", "email", "--note", "blocks month end")
	if err != nil {
		t.Fatalf("request: %v %s", err, out)
	}
	if !strings.Contains(out, "acme asked for MIO-12: 2 requests on it.") {
		t.Fatalf("request printed %q", out)
	}
	body := (*calls)[0].body
	if body["customer"] != "acme" || body["important"] != true || body["source"] != "email" || body["note"] != "blocks month end" || body["by"] != "claude-code" {
		t.Fatalf("request payload %#v", body)
	}
	if _, err := runDelivery(t, "", "forge", "request", "MIO-12"); err == nil {
		t.Fatal("request without --customer must fail")
	}

	if _, err := runDelivery(t, "", "forge", "add", "Invoices", "show", "euros", "--customer", "globex", "--important"); err != nil {
		t.Fatalf("add: %v", err)
	}
	add := (*calls)[len(*calls)-1].body
	if add["customer"] != "globex" || add["important"] != true || add["text"] != "Invoices show euros" {
		t.Fatalf("add payload %#v", add)
	}
	if _, err := runDelivery(t, "", "forge", "add", "Plain", "work", "here"); err != nil {
		t.Fatalf("add: %v", err)
	}
	if plain := (*calls)[len(*calls)-1].body; plain["customer"] != nil {
		t.Fatalf("add without a customer sent %#v", plain)
	}
}

func TestForgeDeliveryCustomerEditsAndLists(t *testing.T) {
	calls, stop := fakeDelivery(t, map[string]any{
		"customer": map[string]any{"id": "acme", "name": "Acme Corp", "domain": "acme.com", "tier": "enterprise", "owner": "dana"},
		"state": map[string]any{
			"customers": []any{map[string]any{"id": "acme", "name": "Acme Corp", "domain": "acme.com", "tier": nil, "owner": nil}},
			"orders":    []any{map[string]any{"id": 12, "title": "Export fails", "state": "in_progress", "requests": []any{map[string]any{"customer": "acme", "important": true}}}},
		},
	})
	defer stop()

	out, err := runDelivery(t, "", "forge", "customer", "acme", "--name", "Acme Corp", "--tier", "enterprise")
	if err != nil {
		t.Fatalf("customer edit: %v %s", err, out)
	}
	if !strings.Contains(out, "acme  Acme Corp  acme.com  enterprise  dana") {
		t.Fatalf("customer edit printed %q", out)
	}
	body := (*calls)[0].body
	if body["id"] != "acme" || body["name"] != "Acme Corp" || body["tier"] != "enterprise" || body["domain"] != nil {
		t.Fatalf("customer payload %#v", body)
	}

	out, err = runDelivery(t, "", "forge", "customer")
	if err != nil {
		t.Fatalf("customer list: %v %s", err, out)
	}
	if !strings.Contains(out, "acme  Acme Corp  acme.com") || !strings.Contains(out, "MIO-12 Export fails (In progress) · important") {
		t.Fatalf("customer list printed %q", out)
	}
	if _, err := runDelivery(t, "", "forge", "customer", "nobody"); err == nil || !strings.Contains(err.Error(), "no customer nobody") {
		t.Fatalf("unknown customer: %v", err)
	}
}

func TestForgeDeliveryShowPrintsRelationsAndRequests(t *testing.T) {
	_, stop := fakeDelivery(t, map[string]any{"state": map[string]any{
		"customers": []any{map[string]any{"id": "acme", "name": "Acme Corp"}},
		"orders": []any{
			map[string]any{"id": 12, "title": "Parent", "state": "in_progress", "blockedBy": []any{9}, "related": []any{7}, "requests": []any{map[string]any{"customer": "acme", "important": true, "source": "email", "note": "soon"}}},
			map[string]any{"id": 20, "title": "Child one", "state": "shipped", "parent": 12},
			map[string]any{"id": 21, "title": "Child two", "state": "todo", "parent": 12},
			map[string]any{"id": 14, "title": "Again", "state": "todo", "dismissed": true, "duplicateOf": 12},
			map[string]any{"id": 30, "title": "After", "state": "todo", "blockedBy": []any{12}},
		},
	}})
	defer stop()

	out, err := runDelivery(t, "", "forge", "show", "MIO-12")
	if err != nil {
		t.Fatalf("show: %v %s", err, out)
	}
	for _, want := range []string{"blocked by MIO-9", "blocking MIO-30", "related MIO-7", "duplicates MIO-14", "children MIO-20, MIO-21 (1/2 shipped)", "Acme Corp · important · email · \"soon\""} {
		if !strings.Contains(out, want) {
			t.Errorf("show lacks %q in:\n%s", want, out)
		}
	}
}
