package output

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestEnvelopeGivesOneDataKey(t *testing.T) {
	cases := []struct {
		name string
		in   any
		want string
	}{
		{"bare list", []any{1, 2}, `{"data":[1,2]}`},
		{"object without data", map[string]any{"summary": 1}, `{"data":{"summary":1}}`},
		{"same rows serialized differently", map[string]any{"data": []any{map[string]any{"id": "r", "n": 1}}, "releases": []any{map[string]any{"id": "r", "n": 1.0, "extra": true}}, "total": 1}, `{"data":[{"id":"r","n":1}]}`},
		{"duplicate rows and their count", map[string]any{"data": []any{"a"}, "deployments": []any{"a"}, "total": 1}, `{"data":["a"]}`},
		{"forge ok envelope", map[string]any{"ok": true, "data": map[string]any{"id": "r"}}, `{"data":{"id":"r"}}`},
		{"siblings move into an object", map[string]any{"data": map[string]any{"id": "x"}, "outcome": "revoked"}, `{"data":{"id":"x","outcome":"revoked"}}`},
		{"pagination stays beside a list", map[string]any{"data": []any{"a"}, "meta": map[string]any{"page": 1}}, `{"data":["a"],"meta":{"page":1}}`},
		{"error body unchanged", map[string]any{"error": map[string]any{"code": "X"}, "ok": false}, `{"error":{"code":"X"},"ok":false}`},
	}
	for _, c := range cases {
		got, _ := json.Marshal(Envelope(c.in))
		var a, b any
		_ = json.Unmarshal(got, &a)
		_ = json.Unmarshal([]byte(c.want), &b)
		if !reflect.DeepEqual(a, b) {
			t.Errorf("%s: got %s, want %s", c.name, got, c.want)
		}
	}
}
