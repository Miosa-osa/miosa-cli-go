package commands

import (
	"bytes"
	"encoding/json"
	"strings"
)

// The Delivery API names things as the delivery glossary does, in snake_case: Issues, Phases,
// agent sessions, Domains, acceptance criteria. The commands read forge-local's names
// (Work Orders, milestones, runners, standing Missions, Done when), so an API answer is
// renamed to them before it is decoded. Requests need no renaming: the API accepts the old
// action and field names.

// deliveryRenamed are field names the API changed, after snake_case → camelCase.
var deliveryRenamed = map[string]string{
	"issues":             "workOrders",
	"issue":              "workOrder",
	"phases":             "milestones",
	"phase":              "milestone",
	"acceptanceCriteria": "doneWhen",
	"agentSession":       "runner",
	"capacity":           "seats",
	"deployed":           "shipped",
}

// deliveryBoardRenamed are the board's top-level collections the API renamed.
var deliveryBoardRenamed = map[string]string{
	"issues":        "orders",
	"phases":        "milestones",
	"agentSessions": "runners",
	"auditLog":      "records",
	"triage":        "intake",
}

// deliveryLegacyJSON renames an API answer to forge-local's names. board is true for the
// board itself (`/state`), whose top-level collections and Domains are folded back too.
func deliveryLegacyJSON(raw []byte, board bool) []byte {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return raw
	}
	if fields, ok := value.(map[string]any); ok && board {
		value = deliveryLegacyBoard(fields)
	} else {
		value = deliveryLegacyValue(value)
	}
	out, err := json.Marshal(value)
	if err != nil {
		return raw
	}
	return out
}

func deliveryLegacyValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(typed))
		for key, item := range typed {
			name := camelCase(key)
			if renamed, ok := deliveryRenamed[name]; ok {
				name = renamed
			}
			// An agent session's crew is the Mission (or Domain) it works in; the board's crew is its roles.
			if _, isID := item.(string); isID && name == "crew" {
				name = "mission"
			}
			out[name] = deliveryLegacyValue(item)
		}
		// Deployed and released are both "shipped" to the commands.
		if state, _ := out["state"].(string); state == "deployed" || state == "released" {
			out["state"] = "shipped"
		}
		return out
	case []any:
		for i, item := range typed {
			typed[i] = deliveryLegacyValue(item)
		}
	}
	return value
}

func deliveryLegacyBoard(board map[string]any) map[string]any {
	renamed := map[string]any{}
	for key, item := range board {
		name := camelCase(key)
		if legacy, ok := deliveryBoardRenamed[name]; ok {
			name = legacy
		}
		renamed[name] = item
	}
	out, _ := deliveryLegacyValue(renamed).(map[string]any)
	// A Domain is a standing Mission; its Issues are in it.
	missions, _ := out["missions"].([]any)
	for _, domain := range asList(out["domains"]) {
		if fields, ok := domain.(map[string]any); ok {
			fields["kind"], fields["status"] = "standing", "standing"
			missions = append(missions, fields)
		}
	}
	if missions != nil {
		out["missions"] = missions
	}
	for _, order := range asList(out["orders"]) {
		if fields, ok := order.(map[string]any); ok && fields["mission"] == nil && fields["domain"] != nil {
			fields["mission"] = fields["domain"]
		}
	}
	// One Charter: the organization's.
	for _, charter := range asList(out["charters"]) {
		if fields, ok := charter.(map[string]any); ok {
			if scope, _ := fields["scope"].(map[string]any); scope == nil || scope["kind"] == "organization" {
				out["charter"] = fields
			}
		}
	}
	return out
}

func asList(value any) []any {
	list, _ := value.([]any)
	return list
}

func camelCase(name string) string {
	if !strings.Contains(name, "_") {
		return name
	}
	parts := strings.Split(name, "_")
	for i := 1; i < len(parts); i++ {
		if parts[i] != "" {
			parts[i] = strings.ToUpper(parts[i][:1]) + parts[i][1:]
		}
	}
	return strings.Join(parts, "")
}
