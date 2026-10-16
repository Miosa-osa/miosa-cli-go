package commands_test

import "encoding/json"

// dataJSON returns the "data" of a --json result (every result is one
// {"data": ...} envelope since 2.3.11), re-encoded for json.Unmarshal.
func dataJSON(out string) []byte {
	var env map[string]json.RawMessage
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		return []byte(out)
	}
	if d, ok := env["data"]; ok {
		return d
	}
	return []byte(out)
}
