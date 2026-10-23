package commands_test

import (
	"strings"
	"testing"
)

const billingSettings = `{"data":{
	"tenant_id":"t1",
	"auto_recharge_enabled":true,
	"auto_recharge_requested":true,
	"auto_recharge_payment_ready":true,
	"auto_recharge_threshold_credits":500,
	"auto_recharge_amount_credits":5000,
	"auto_recharge_monthly_cap_credits":20000,
	"auto_recharge_monthly_spent_credits":5000,
	"auto_recharge_monthly_remaining_credits":15000
}}`

func TestAutoTopupShowsTheState(t *testing.T) {
	fakeRoutes(t, map[string]any{"GET /billing/settings": billingSettings})

	out, err := run(t, "billing", "auto-reload")
	if err != nil {
		t.Fatalf("auto-reload: %v\n%s", err, out)
	}

	for _, want := range []string{"on", "500 credits", "50.00", "20000 credits"} {
		if !strings.Contains(out, want) {
			t.Errorf("output does not contain %q:\n%s", want, out)
		}
	}
}

func TestAutoTopupSetSendsTheAmounts(t *testing.T) {
	f := fakeRoutes(t, map[string]any{"PUT /billing/settings": billingSettings})

	if out, err := run(t, "billing", "auto-reload", "set", "--threshold", "500", "--amount", "5000", "--monthly-cap", "20000"); err != nil {
		t.Fatalf("auto-topup set: %v\n%s", err, out)
	}

	calls := f.find("PUT", "/billing/settings")
	if len(calls) != 1 {
		t.Fatalf("PUT calls = %d", len(calls))
	}
	body := calls[0].Body
	if body["auto_recharge_enabled"] != true {
		t.Fatalf("enabled = %v", body["auto_recharge_enabled"])
	}
	if body["auto_recharge_threshold_credits"] != float64(500) {
		t.Fatalf("threshold = %v", body["auto_recharge_threshold_credits"])
	}
}

func TestAutoTopupSetRefusesACapBelowTheAmount(t *testing.T) {
	fakeRoutes(t, map[string]any{})

	_, err := run(t, "billing", "auto-reload", "set", "--threshold", "500", "--amount", "5000", "--monthly-cap", "100")
	if err == nil || !strings.Contains(err.Error(), "monthly-cap") {
		t.Fatalf("err = %v", err)
	}
}

func TestAutoTopupOffSendsOnlyTheFlag(t *testing.T) {
	f := fakeRoutes(t, map[string]any{
		"PUT /billing/settings": `{"data":{"auto_recharge_enabled":false,"auto_recharge_payment_ready":false}}`,
	})

	out, err := run(t, "billing", "auto-reload", "off")
	if err != nil {
		t.Fatalf("auto-topup off: %v\n%s", err, out)
	}

	body := f.find("PUT", "/billing/settings")[0].Body
	if body["auto_recharge_enabled"] != false {
		t.Fatalf("enabled = %v", body["auto_recharge_enabled"])
	}
	if _, ok := body["auto_recharge_threshold_credits"]; ok {
		t.Fatal("off sent an amount; it must change only the flag")
	}
	if !strings.Contains(out, "No card is ready") {
		t.Fatalf("out = %s", out)
	}
}

func TestPaymentMethodsListAndDefault(t *testing.T) {
	methods := `{"data":[
		{"id":"pm_1","type":"card","brand":"visa","last4":"4242","exp_month":4,"exp_year":2030,"is_default":true},
		{"id":"pm_2","type":"card","brand":"mastercard","last4":"4444","exp_month":8,"exp_year":2031,"is_default":false}
	]}`

	f := fakeRoutes(t, map[string]any{
		"GET /billing/payment-methods":              methods,
		"PUT /billing/payment-methods/pm_2/default": methods,
	})

	out, err := run(t, "billing", "payment-methods", "list")
	if err != nil {
		t.Fatalf("payment-methods list: %v\n%s", err, out)
	}
	for _, want := range []string{"visa", "****4242", "2031"} {
		if !strings.Contains(out, want) {
			t.Errorf("output does not contain %q:\n%s", want, out)
		}
	}

	if out, err := run(t, "billing", "payment-methods", "default", "pm_2"); err != nil {
		t.Fatalf("payment-methods default: %v\n%s", err, out)
	}
	if len(f.find("PUT", "/billing/payment-methods/pm_2/default")) != 1 {
		t.Fatal("the default request was not sent")
	}
}
