package commands_test

import (
	"strings"
	"testing"
)

// The flat bodies GET/PUT /api/v1/billing/settings and GET /api/v1/billing/auto-topup
// return (captured from api.miosa.ai, 2026-10-23, QA account). The API sends no
// {"data": ...} envelope; the old fakes added one and hid that the CLI read nothing.
const billingSettings = `{
	"tenant_id":"t1",
	"updated_at":"2026-10-23T18:00:00Z",
	"auto_recharge_enabled":true,
	"auto_recharge_requested":true,
	"auto_recharge_payment_ready":true,
	"auto_recharge_not_ready_reasons":[],
	"auto_recharge_disabled_reason":null,
	"auto_recharge_threshold_credits":500,
	"auto_recharge_amount_credits":5000,
	"auto_recharge_monthly_cap_credits":20000,
	"cycle_spend_credits":0,
	"auto_recharge_monthly_spent_credits":5000,
	"auto_recharge_monthly_remaining_credits":15000,
	"auto_recharge_next_attempt_at":null
}`

const autoReloadNotReady = `{
	"enabled":false,"requested":false,"configured":true,
	"readiness":{"ready":false,"last_reconciled_at":null,"charge_consent":false,"reasons":["no_customer","no_account","no_payment_method"],"card":null},
	"next_action":"enroll","attempts":[],"amount_credits":1000,"threshold_credits":200,"monthly_cap_credits":2500
}`

func TestAutoReloadShowsTheState(t *testing.T) {
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

func TestAutoReloadSetSendsTheAmounts(t *testing.T) {
	f := fakeRoutes(t, map[string]any{"PUT /billing/settings": billingSettings})

	if out, err := run(t, "billing", "auto-reload", "set", "--threshold", "500", "--amount", "5000", "--monthly-cap", "20000"); err != nil {
		t.Fatalf("auto-reload set: %v\n%s", err, out)
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

func TestAutoReloadSetRefusesACapBelowTheAmount(t *testing.T) {
	fakeRoutes(t, map[string]any{})

	_, err := run(t, "billing", "auto-reload", "set", "--threshold", "500", "--amount", "5000", "--monthly-cap", "100")
	if err == nil || !strings.Contains(err.Error(), "monthly-cap") {
		t.Fatalf("err = %v", err)
	}
}

func TestAutoReloadOffSendsOnlyTheFlag(t *testing.T) {
	f := fakeRoutes(t, map[string]any{
		"PUT /billing/settings": `{"auto_recharge_enabled":false,"auto_recharge_payment_ready":false}`,
	})

	out, err := run(t, "billing", "auto-reload", "off")
	if err != nil {
		t.Fatalf("auto-reload off: %v\n%s", err, out)
	}

	body := f.find("PUT", "/billing/settings")[0].Body
	if body["auto_recharge_enabled"] != false {
		t.Fatalf("enabled = %v", body["auto_recharge_enabled"])
	}
	if _, ok := body["auto_recharge_threshold_credits"]; ok {
		t.Fatal("off sent an amount; it must change only the flag")
	}
	// Turning it off is not the moment to ask for a card.
	if !strings.Contains(out, "Auto-reload is off.") || strings.Contains(out, "No card is ready") {
		t.Fatalf("out = %s", out)
	}
}

func TestAutoReloadSetSaysWhenNoCardCanBeChargedYet(t *testing.T) {
	fakeRoutes(t, map[string]any{
		"PUT /billing/settings": `{"auto_recharge_enabled":false,"auto_recharge_requested":true,"auto_recharge_payment_ready":false}`,
	})

	out, err := run(t, "billing", "auto-reload", "set", "--threshold", "200", "--amount", "1000", "--monthly-cap", "2500")
	if err != nil {
		t.Fatalf("auto-reload set: %v\n%s", err, out)
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

func TestAutoReloadSaysWhatIsMissingBeforeACardIsSaved(t *testing.T) {
	notReady := strings.Replace(billingSettings, `"auto_recharge_enabled":true`, `"auto_recharge_enabled":false`, 1)
	notReady = strings.Replace(notReady, `"auto_recharge_payment_ready":true`, `"auto_recharge_payment_ready":false`, 1)
	fakeRoutes(t, map[string]any{
		"GET /billing/settings":   notReady,
		"GET /billing/auto-topup": autoReloadNotReady,
	})

	out, err := run(t, "billing", "auto-reload", "get")
	if err != nil {
		t.Fatalf("auto-reload get: %v\n%s", err, out)
	}
	for _, want := range []string{"off", "no_payment_method", "auto-reload setup checkout"} {
		if !strings.Contains(out, want) {
			t.Errorf("output does not contain %q:\n%s", want, out)
		}
	}
}

func TestAutoReloadStillReadsAnEnvelopedBody(t *testing.T) {
	fakeRoutes(t, map[string]any{"GET /billing/settings": `{"data":` + billingSettings + `}`})

	out, err := run(t, "billing", "auto-reload")
	if err != nil {
		t.Fatalf("auto-reload: %v\n%s", err, out)
	}
	if !strings.Contains(out, "20000 credits") {
		t.Fatalf("out = %s", out)
	}
}

func TestAutoReloadSetRefusesAmountsTheAPIRefuses(t *testing.T) {
	fakeRoutes(t, map[string]any{})

	cases := map[string][]string{
		"--amount":    {"--threshold", "500", "--amount", "100", "--monthly-cap", "20000"},
		"--threshold": {"--threshold", "50", "--amount", "5000", "--monthly-cap", "20000"},
	}
	for flag, args := range cases {
		_, err := run(t, append([]string{"billing", "auto-reload", "set"}, args...)...)
		if err == nil || !strings.Contains(err.Error(), flag) {
			t.Errorf("%s: err = %v", flag, err)
		}
	}
}
