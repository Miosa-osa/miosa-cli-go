package commands

import (
	"fmt"
	"strings"
)

// Cells for list responses whose useful values sit in nested or differently
// named fields, so a table never prints "-" or a Go map for them.

// harnessModelsCell lists a harness's model ids: the first three, then a count.
// The API sends each model as an object, and a harness that runs on the AI
// Gateway (OSA, custom) sends none.
func harnessModelsCell(row map[string]any) string {
	models, _ := row["models"].([]any)
	if len(models) == 0 {
		return "-"
	}
	ids := make([]string, 0, len(models))
	for _, m := range models {
		switch v := m.(type) {
		case map[string]any:
			if id, _ := v["id"].(string); id != "" {
				ids = append(ids, id)
			}
		case string:
			ids = append(ids, v)
		}
	}
	if len(ids) <= 3 {
		return strings.Join(ids, ", ")
	}
	return fmt.Sprintf("%s +%d more", strings.Join(ids[:3], ", "), len(ids)-3)
}

// notificationTitleCell names what happened: the event type plus the payload's
// subject and reason ("database.stopped: osa-engine (host_maintenance)").
func notificationTitleCell(row map[string]any) string {
	if title, _ := row["title"].(string); title != "" {
		return title
	}
	kind, _ := row["type"].(string)
	return strings.TrimSpace(kind + subjectSuffix(row["payload"]))
}

// activityMessageCell does the same for a workspace activity entry, whose
// subject is in metadata.
func activityMessageCell(row map[string]any) string {
	if message, _ := row["message"].(string); message != "" {
		return message
	}
	return strings.TrimPrefix(subjectSuffix(row["metadata"]), ": ")
}

func subjectSuffix(raw any) string {
	fields, _ := raw.(map[string]any)
	subject, _ := fields["name"].(string)
	if subject == "" {
		subject, _ = fields["title"].(string)
	}
	reason, _ := fields["reason"].(string)
	out := ""
	if subject != "" {
		out = ": " + subject
	}
	if reason != "" {
		out += " (" + reason + ")"
	}
	return out
}

// readCell shows whether a notification has been read: the API sends read_at.
func readCell(row map[string]any) string {
	if at, _ := row["read_at"].(string); at != "" {
		return "yes"
	}
	return "no"
}
