package commands

import (
	"encoding/json"
	"reflect"
	"testing"
)

// A Domain's crew (MIO-64): the API writes {"role", "capacity"}, older forge-local boards a string.
func TestDeliveryCrewUnmarshalsBothShapes(t *testing.T) {
	for _, tc := range []struct {
		name, raw string
		want      deliveryCrew
	}{
		{"string", `{"id":"console","crew":"frontend"}`, deliveryCrew{Role: "frontend"}},
		{"object", `{"id":"console","crew":{"role":"backend","capacity":{"backend":2}}}`, deliveryCrew{Role: "backend", Capacity: map[string]int{"backend": 2}}},
		{"object with empty capacity", `{"id":"console","crew":{"role":"backend","capacity":{}}}`, deliveryCrew{Role: "backend", Capacity: map[string]int{}}},
		{"object renamed to seats", `{"id":"console","crew":{"role":"backend","seats":{"backend":1}}}`, deliveryCrew{Role: "backend", Capacity: map[string]int{"backend": 1}}},
		{"empty string", `{"id":"console","crew":""}`, deliveryCrew{}},
		{"null", `{"id":"console","crew":null}`, deliveryCrew{}},
		{"missing", `{"id":"console"}`, deliveryCrew{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var domain deliveryDomain
			if err := json.Unmarshal([]byte(tc.raw), &domain); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if !reflect.DeepEqual(domain.Crew, tc.want) {
				t.Fatalf("crew = %#v, want %#v", domain.Crew, tc.want)
			}
		})
	}
	var domain deliveryDomain
	if err := json.Unmarshal([]byte(`{"crew":42}`), &domain); err == nil {
		t.Fatal("a number is neither shape and must be refused")
	}
}

func TestDeliveryCrewMarshalsTheAPIObject(t *testing.T) {
	for _, tc := range []struct {
		name string
		crew deliveryCrew
		want string
	}{
		{"with capacity", deliveryCrew{Role: "backend", Capacity: map[string]int{"backend": 2}}, `{"role":"backend","capacity":{"backend":2}}`},
		{"role only", deliveryCrew{Role: "frontend"}, `{"role":"frontend","capacity":{}}`},
		{"empty", deliveryCrew{}, `{"role":"","capacity":{}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, err := json.Marshal(tc.crew)
			if err != nil || string(out) != tc.want {
				t.Fatalf("marshal = %s %v, want %s", out, err, tc.want)
			}
			var back deliveryCrew
			if err := json.Unmarshal(out, &back); err != nil || back.Role != tc.crew.Role || len(back.Capacity) != len(tc.crew.Capacity) {
				t.Fatalf("round trip = %#v %v, want %#v", back, err, tc.crew)
			}
		})
	}
}
