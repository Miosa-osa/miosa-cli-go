package vercel

import (
	"strings"
	"testing"
	"time"
)

const sample = `{
  "framework": "nextjs",
  "buildCommand": "next build",
  "regions": ["iad1"],
  "cleanUrls": true,
  "rewrites": [{"source": "/api/(.*)", "destination": "/api"}],
  "headers": [{"source": "/(.*)", "headers": [{"key": "X-Frame-Options", "value": "DENY"}]}],
  "crons": [{"path": "/api/nightly", "schedule": "0 3 * * *"}],
  "env": {"DATABASE_URL": "@db", "NEXT_PUBLIC_URL": "https://example.test"}
}`

func opts() Options {
	return Options{
		ProjectName: "shop",
		ServiceName: "web",
		Repository:  "https://github.com/acme/shop",
		Region:      "us-east",
		FetchedAt:   time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC),
	}
}

func TestPlanCarriesTheAppTheRegionAndTheVariableNames(t *testing.T) {
	cfg, err := Parse([]byte(sample))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	plan := Plan(cfg, opts())

	if plan.Source.Kind != "vercel" || plan.Target.ProjectName != "shop" {
		t.Fatalf("source/target = %+v / %+v", plan.Source, plan.Target)
	}
	if plan.Source.Digest == "" {
		t.Fatal("digest is empty")
	}

	var service bool
	var names []string
	for _, a := range plan.Actions {
		switch a.Op {
		case "service.create":
			service = true
			if a.Kind != "server" {
				t.Fatalf("kind = %s", a.Kind)
			}
			if a.Settings == nil || a.Settings.Region != "us-east" {
				t.Fatalf("settings = %+v", a.Settings)
			}
			if a.Source == nil || a.Source.Ref != "https://github.com/acme/shop" {
				t.Fatalf("source = %+v", a.Source)
			}
		case "variable.set":
			if a.Variable.HasValue {
				t.Fatalf("%s carries a value", a.Variable.Name)
			}
			names = append(names, a.Variable.Name)
		}
	}
	if !service {
		t.Fatal("no service.create")
	}
	if strings.Join(names, ",") != "DATABASE_URL,NEXT_PUBLIC_URL" {
		t.Fatalf("variable names = %v", names)
	}
}

func TestPlanReportsWhatItCannotCarry(t *testing.T) {
	cfg, _ := Parse([]byte(sample))
	plan := Plan(cfg, opts())

	fields := map[string]bool{}
	for _, a := range plan.Actions {
		if a.Op == "unmapped" {
			fields[a.Field] = true
			if a.Why == "" {
				t.Fatalf("%s has no reason", a.Field)
			}
		}
	}

	for _, want := range []string{"rewrites", "headers", "cleanUrls", "buildCommand", "crons /api/nightly"} {
		if !fields[want] {
			t.Errorf("%q is not reported as unmapped; got %v", want, fields)
		}
	}
}

func TestPlanWarnsWhenThereIsNoSource(t *testing.T) {
	cfg, _ := Parse([]byte(`{"env":{}}`))
	o := opts()
	o.Repository = ""

	plan := Plan(cfg, o)

	if len(plan.Warnings) == 0 || !strings.Contains(plan.Warnings[0], "--repo") {
		t.Fatalf("warnings = %v", plan.Warnings)
	}
}

func TestPlanIsStableForTheSameInput(t *testing.T) {
	cfg, _ := Parse([]byte(sample))
	if Plan(cfg, opts()).Source.Digest != Plan(cfg, opts()).Source.Digest {
		t.Fatal("digest changed for the same input")
	}
}
