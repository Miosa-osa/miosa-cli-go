package commands

import (
	"fmt"
	"sort"
	"strings"

	"github.com/Miosa-osa/miosa-cli-go/internal/output"
)

// renderReadable is the text-mode answer for a command with no columns or
// renderer of its own: a table for a list, field rows for an object. Before
// it, such commands (forge check list, release tag, policy owners, ...) printed
// the raw JSON response without --json. --json is unchanged.
func renderReadable(p *output.Printer, resp any) error {
	resp = stripAvatars(resp)
	if rows, ok := findRows(resp, "data"); ok {
		return readableTable(p, rows)
	}
	switch v := unwrapObject(resp).(type) {
	case map[string]any:
		fields := flattenFields(v, "", 0)
		if len(fields) == 0 {
			p.Success("done")
			return nil
		}
		p.Fields(fields)
	case nil:
		p.Success("done")
	default:
		p.Line("%s", readableValue(v))
	}
	return nil
}

func readableTable(p *output.Printer, rows []any) error {
	if len(rows) == 0 {
		p.Line("No results.")
		return nil
	}
	objs := make([]map[string]any, 0, len(rows))
	for _, r := range rows {
		if m, ok := r.(map[string]any); ok {
			objs = append(objs, m)
		} else {
			p.Line("%s", readableValue(r))
		}
	}
	if len(objs) == 0 {
		return nil
	}
	keys := orderedKeys(objs)
	if len(keys) > 7 {
		keys = keys[:7]
	}
	heads := make([]string, len(keys))
	for i, k := range keys {
		heads[i] = strings.ToUpper(strings.ReplaceAll(k, "_", " "))
	}
	table := make([][]string, 0, len(objs))
	for _, o := range objs {
		line := make([]string, len(keys))
		for i, k := range keys {
			line[i] = readableValue(o[k])
		}
		table = append(table, line)
	}
	p.Table(heads, table)
	return nil
}

// preferred keys lead, in this order; the rest follow alphabetically. Only
// keys holding a scalar in some row are shown in a table.
var preferredKeys = []string{"id", "name", "slug", "tag", "number", "title", "status", "state", "conclusion", "role", "kind", "ref", "head_sha", "commit_sha", "created_at", "updated_at"}

func orderedKeys(objs []map[string]any) []string {
	seen := map[string]bool{}
	for _, o := range objs {
		for k, v := range o {
			switch v.(type) {
			case map[string]any, []any:
				if len(objs) > 1 {
					continue
				}
			}
			seen[k] = true
		}
	}
	var out []string
	for _, k := range preferredKeys {
		if seen[k] {
			out = append(out, k)
			delete(seen, k)
		}
	}
	rest := make([]string, 0, len(seen))
	for k := range seen {
		rest = append(rest, k)
	}
	sort.Strings(rest)
	return append(out, rest...)
}

// flattenFields lists an object's values as field rows, nested objects as
// dotted names (providers.aws.provisioning), three levels deep.
func flattenFields(obj map[string]any, prefix string, depth int) [][2]string {
	var fields [][2]string
	for _, k := range orderedKeys([]map[string]any{obj}) {
		name := k
		if prefix != "" {
			name = prefix + "." + k
		}
		if nested, ok := obj[k].(map[string]any); ok && len(nested) > 0 && depth < 3 {
			fields = append(fields, flattenFields(nested, name, depth+1)...)
			continue
		}
		fields = append(fields, [2]string{headOf(name), readableValue(obj[k])})
	}
	return fields
}

func headOf(k string) string {
	s := strings.ReplaceAll(k, "_", " ")
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

func readableValue(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case []any:
		parts := make([]string, 0, len(x))
		for _, e := range x {
			switch e.(type) {
			case map[string]any, []any:
				return fmt.Sprintf("%d items", len(x))
			}
			parts = append(parts, fmt.Sprint(e))
		}
		return strings.Join(parts, ", ")
	case map[string]any:
		if len(x) == 0 {
			return ""
		}
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		parts := make([]string, 0, len(keys))
		for _, k := range keys {
			switch x[k].(type) {
			case map[string]any, []any:
				parts = append(parts, k+"=...")
			default:
				parts = append(parts, k+"="+fmt.Sprint(x[k]))
			}
		}
		return strings.Join(parts, " ")
	case float64:
		if x == float64(int64(x)) {
			return fmt.Sprint(int64(x))
		}
		return fmt.Sprint(x)
	default:
		return fmt.Sprint(x)
	}
}
