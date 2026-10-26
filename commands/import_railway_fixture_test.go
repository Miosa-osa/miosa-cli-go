package commands_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"testing"
)

// ClinicIQ-shaped Railway fixtures.
//
// The project, environment and service names, repositories, commands, config
// files, regions and variable NAMES follow ClinicIQ's Railway account as read
// on 2026-10-25 (tasks/cliniciq-railway-to-miosa-readiness-2026-10-25.md and
// the repositories' railway.json / railway.toml). No variable value was ever
// read: every literal here is the placeholder "set", and references are the
// ${{...}} templates, which hold no secret.

type rwVolume struct {
	MountPath string
	SizeMB    int
}

type rwInstance struct {
	ServiceID       string
	Name            string
	Repo            string
	Image           string
	Root            string
	Branch          string
	Start           string
	Build           string
	Dockerfile      string
	Health          string
	PreDeploy       any
	Replicas        *int
	Region          string
	ConfigFile      string
	FileManifest    string // JSON
	ServiceManifest string // JSON
	ServiceDomains  []string
	CustomDomains   []string
	DomainPort      int
	Vars            map[string]string
	Volumes         []rwVolume
}

type rwEnv struct {
	ID        string
	Name      string
	Instances []rwInstance
	Shared    map[string]string
}

type rwProject struct {
	ID   string
	Name string
	Envs []rwEnv
}

func intp(n int) *int { return &n }

// regionManifest is the service manifest of a deployment that ran in region.
func regionManifest(region string, replicas int, extra string) string {
	return fmt.Sprintf(`{"build":{%s},"deploy":{"multiRegionConfig":{%q:{"numReplicas":%d}}}}`, extra, region, replicas)
}

const literal = "set"

// clinicIQSystem is clinic-iq-system: production and development, a FastAPI
// backend with arq workers on one private repository, a Next.js frontend on
// another, Postgres 18 and Redis 8.2 as Railway template images, and mailhog.
func clinicIQSystem() rwProject {
	sg := func(replicas int) string {
		return regionManifest("asia-southeast1-eqsg3a", replicas, `"builder":"DOCKERFILE","dockerfilePath":"/Dockerfile"`)
	}
	eu := regionManifest("europe-west4-drams3a", 1, `"builder":"RAILPACK"`)
	redisParts := "redis://${{Redis.REDISUSER}}:${{Redis.REDISPASSWORD}}@${{Redis.REDISHOST}}:${{Redis.REDISPORT}}"
	pgParts := "postgresql+asyncpg://${{Postgres.PGUSER}}:${{Postgres.PGPASSWORD}}@${{Postgres.PGHOST}}:${{Postgres.PGPORT}}/${{Postgres.PGDATABASE}}"
	databases := func() []rwInstance {
		return []rwInstance{
			{ServiceID: "svc-postgres", Name: "Postgres", Image: "ghcr.io/railwayapp-templates/postgres-ssl:18",
				ServiceManifest: regionManifest("asia-southeast1-eqsg3a", 1, ""),
				Volumes:         []rwVolume{{"/var/lib/postgresql/data", 50000}},
				Vars:            map[string]string{"POSTGRES_PASSWORD": literal, "PGHOST": "${{RAILWAY_PRIVATE_DOMAIN}}"}},
			{ServiceID: "svc-redis", Name: "Redis", Image: "redis:8.2.9",
				Start:           `/bin/sh -c "rm -rf $RAILWAY_VOLUME_MOUNT_PATH/lost+found/ && exec docker-entrypoint.sh redis-server"`,
				ServiceManifest: regionManifest("asia-southeast1-eqsg3a", 1, ""),
				Volumes:         []rwVolume{{"/data", 50000}},
				Vars:            map[string]string{"REDIS_PASSWORD": literal}},
		}
	}
	families := []string{"deployments", "engines", "knowledge", "maintenance", "media", "notifications", "publishing"}

	prod := rwEnv{ID: "env-prod", Name: "production"}
	prod.Instances = append(databases(),
		rwInstance{ServiceID: "svc-backend", Name: "backend-cliniciq", Repo: "platform-cliniciq/backend-cliniciq", Branch: "main",
			Start: "uvicorn app.main:app --host 0.0.0.0 --port 8000", PreDeploy: []string{"alembic upgrade head"},
			Health: "/readyz", ServiceManifest: sg(1),
			ServiceDomains: []string{"backend-cliniciq-production.up.railway.app"}, CustomDomains: []string{"api.cliniciq.com"},
			// Production's API holds only Railway's own names today.
			Vars: map[string]string{"RAILWAY_PUBLIC_DOMAIN": literal, "RAILWAY_PRIVATE_DOMAIN": literal, "RAILWAY_SERVICE_NAME": literal}},
		rwInstance{ServiceID: "svc-workers", Name: "backend-cliniciq-workers", Repo: "platform-cliniciq/backend-cliniciq", Branch: "dev",
			Start: "arq app.worker.settings.WorkerSettings", ServiceManifest: sg(1),
			Vars: map[string]string{"DATABASE_URL": pgParts, "REDIS_URL": redisParts, "SECRET_KEY": literal,
				"OPENAI_API_KEY": "${{backend-cliniciq.OPENAI_API_KEY}}"}},
		rwInstance{ServiceID: "svc-frontend", Name: "clinic-iq-frontend", Repo: "platform-cliniciq/clinic-iq-frontend", Branch: "main",
			Health: "/login", ServiceManifest: sg(1),
			ServiceDomains: []string{"clinic-iq-frontend-production.up.railway.app"}, CustomDomains: []string{"app.cliniciq.com"},
			Vars: map[string]string{"NEXT_PUBLIC_API_URL": literal}},
		rwInstance{ServiceID: "svc-mailhog", Name: "mailhog", Image: "mailhog/mailhog", ServiceManifest: regionManifest("asia-southeast1-eqsg3a", 1, "")},
	)
	for _, family := range families {
		title := strings.ToUpper(family[:1]) + family[1:]
		prod.Instances = append(prod.Instances, rwInstance{
			ServiceID: "svc-worker-" + family, Name: "backend-cliniciq-worker-" + family,
			Repo: "platform-cliniciq/backend-cliniciq", Branch: "main",
			Start: "arq app.worker.settings." + title + "WorkerSettings", ServiceManifest: sg(1),
			Vars: map[string]string{"DATABASE_URL": literal, "PGCRYPTO_KEY": literal,
				"SECRET_KEY": "${{backend-cliniciq.SECRET_KEY}}", "REDIS_URL": "${{Redis.REDIS_URL}}"},
		})
	}

	dev := rwEnv{ID: "env-dev", Name: "development"}
	dev.Instances = append(databases(),
		rwInstance{ServiceID: "svc-backend", Name: "backend-cliniciq", Repo: "platform-cliniciq/backend-cliniciq", Branch: "dev",
			Start: "uvicorn app.main:app --host 0.0.0.0 --port 8000", PreDeploy: []string{"alembic upgrade head"},
			ServiceManifest: regionManifest("asia-southeast1-eqsg3a", 1, `"builder":"RAILPACK"`),
			ServiceDomains:  []string{"backend-cliniciq-development.up.railway.app"},
			Vars: map[string]string{"DATABASE_URL": pgParts, "REDIS_URL": redisParts, "SECRET_KEY": literal,
				"SMTP_HOST": "${{mailhog.RAILWAY_PRIVATE_DOMAIN}}"}},
		rwInstance{ServiceID: "svc-workers", Name: "backend-cliniciq-workers", Repo: "platform-cliniciq/backend-cliniciq", Branch: "dev",
			Start: "arq app.worker.settings.WorkerSettings", ServiceManifest: regionManifest("asia-southeast1-eqsg3a", 1, `"builder":"RAILPACK"`),
			Vars: map[string]string{"DATABASE_URL": pgParts, "REDIS_URL": redisParts, "SMTP_HOST": "${{mailhog.RAILWAY_PRIVATE_DOMAIN}}"}},
		rwInstance{ServiceID: "svc-frontend", Name: "clinic-iq-frontend", Repo: "platform-cliniciq/clinic-iq-frontend", Branch: "dev",
			ServiceManifest: regionManifest("asia-southeast1-eqsg3a", 1, `"builder":"RAILPACK"`),
			ServiceDomains:  []string{"clinic-iq-frontend-development.up.railway.app"},
			Vars:            map[string]string{"NEXT_PUBLIC_API_URL": literal}},
		rwInstance{ServiceID: "svc-mailhog", Name: "mailhog", Image: "mailhog/mailhog",
			ServiceManifest: regionManifest("asia-southeast1-eqsg3a", 1, ""),
			ServiceDomains:  []string{"mailhog-development-b4bf.up.railway.app"}},
	)
	for _, family := range families {
		dev.Instances = append(dev.Instances, rwInstance{
			ServiceID: "svc-worker-" + family + "-dev", Name: "backend-cliniciq-worker-" + family + "-dev",
			Repo: "platform-cliniciq/backend-cliniciq", Branch: "dev", ServiceManifest: eu,
			Vars: map[string]string{"CLINICIQ_WORKER_FAMILY": literal, "REDIS_URL": literal, "DATABASE_URL": literal},
		})
	}
	return rwProject{ID: "e7314324-b726-4ff7-aab6-6bab5a7b320d", Name: "clinic-iq-system", Envs: []rwEnv{prod, dev}}
}

// clinicIQBetaStaging is the staging environment of clinic-iq-beta-staging:
// a pnpm monorepo whose web service is configured by /railway.json, whose
// worker by /apps/worker/railway.toml (Dockerfile, 2 replicas), and whose
// programs app only by its service settings and its last deployment.
func clinicIQBetaStaging() rwProject {
	eu := `"multiRegionConfig":{"europe-west4-drams3a":{"numReplicas":%d}}`
	staging := rwEnv{ID: "env-staging", Name: "staging", Instances: []rwInstance{
		{ServiceID: "svc-web", Name: "clinic-iq-web", Repo: "platform-cliniciq/clinic-iq-beta", Branch: "staging",
			ConfigFile: "/railway.json",
			FileManifest: `{"build":{"builder":"NIXPACKS"},"deploy":{"preDeployCommand":"pnpm --filter @clinic-iq/db db:migrate",
				"startCommand":"pnpm --filter @clinic-iq/web start","restartPolicyType":"ON_FAILURE","restartPolicyMaxRetries":3}}`,
			ServiceManifest: `{"build":{"builder":"NIXPACKS"},"deploy":{` + fmt.Sprintf(eu, 1) + `}}`,
			ServiceDomains:  []string{"clinic-iq-web-staging.up.railway.app"},
			Vars: map[string]string{"DATABASE_URL": literal, "SUPABASE_SECRET_KEY": literal,
				"NEXT_PUBLIC_APP_URL":      "https://${{RAILWAY_PUBLIC_DOMAIN}}",
				"NEXT_PUBLIC_SUPABASE_URL": literal, "MIOSA_API_KEY": literal}},
		{ServiceID: "svc-worker", Name: "clinic-iq-worker", Repo: "platform-cliniciq/clinic-iq-beta", Branch: "staging",
			ConfigFile: "/apps/worker/railway.toml",
			FileManifest: `{"build":{"builder":"DOCKERFILE","dockerfilePath":"apps/worker/Dockerfile","watchPatterns":["/apps/worker/**"]},
				"deploy":{"numReplicas":2,"restartPolicyMaxRetries":10,"restartPolicyType":"ON_FAILURE","startCommand":"pnpm --filter @clinic-iq/worker start"}}`,
			ServiceManifest: `{"build":{"builder":"DOCKERFILE","dockerfilePath":"apps/worker/Dockerfile"},"deploy":{"numReplicas":2,` + fmt.Sprintf(eu, 2) + `}}`,
			Vars:            map[string]string{"DATABASE_URL": literal, "WEB_APP_URL": "${{clinic-iq-web.NEXT_PUBLIC_APP_URL}}"}},
		{ServiceID: "svc-programs", Name: "clinic-iq-programs", Repo: "platform-cliniciq/clinic-iq-beta", Branch: "staging",
			Start: "pnpm --filter @clinic-iq/programs-app start", PreDeploy: []string{"pnpm --filter @clinic-iq/programs db:migrate"},
			FileManifest:    `{}`,
			ServiceManifest: `{"build":{"builder":"NIXPACKS","buildCommand":"pnpm --filter @clinic-iq/programs-app build"},"deploy":{` + fmt.Sprintf(eu, 1) + `}}`,
			ServiceDomains:  []string{"clinic-iq-programs-staging.up.railway.app"},
			Vars:            map[string]string{"DATABASE_URL": literal}},
	}}
	return rwProject{ID: "8800f2fc-3599-43c1-962a-c702a24659a0", Name: "clinic-iq-beta-staging",
		Envs: []rwEnv{{ID: "env-beta-prod", Name: "production"}, staging}}
}

// clinicIQFreeAccount is cliniciq-free-account: a web service uploaded with the
// Railway CLI (no repository, no image) and its Postgres.
func clinicIQFreeAccount() rwProject {
	return rwProject{ID: "78d9f924-3066-4cf9-88f6-384459422640", Name: "cliniciq-free-account", Envs: []rwEnv{{
		ID: "env-free", Name: "production", Instances: []rwInstance{
			{ServiceID: "svc-free-pg", Name: "Postgres", Image: "ghcr.io/railwayapp-templates/postgres-ssl:17", Region: "us-east4"},
			{ServiceID: "svc-free-web", Name: "web", Start: "npm start", Region: "us-east4",
				CustomDomains: []string{"claim.cliniciq.com"},
				Vars:          map[string]string{"DATABASE_URL": "${{Postgres.DATABASE_URL}}", "GHL_API_KEY": literal}},
		},
		Shared: map[string]string{"APP_NAME": literal},
	}}}
}

// clinicIQProd is clinic-iq-prod: services named after their pnpm packages
// ("@clinic-iq/web"), one name too long for a MIOSA slug, and mailhog whose
// Railway domain routes to its web UI on 8025 (it listens for SMTP on 1025).
func clinicIQProd() rwProject {
	return rwProject{ID: "a138ff9d-ac39-43b6-b435-f4d4bc45ef09", Name: "clinic-iq-prod", Envs: []rwEnv{{
		ID: "env-iqprod", Name: "production", Instances: []rwInstance{
			{ServiceID: "svc-pkg-web", Name: "@clinic-iq/web", Repo: "platform-cliniciq/clinic-iq-beta", Branch: "main",
				Region: "us-east4", ServiceDomains: []string{"web-production.up.railway.app"}, CustomDomains: []string{"my.cliniciq.com"},
				DomainPort: 8080, Vars: map[string]string{"DATABASE_URL": literal}},
			{ServiceID: "svc-pkg-worker", Name: "@clinic-iq/worker", Repo: "platform-cliniciq/clinic-iq-beta", Branch: "main",
				Region: "us-east4", Vars: map[string]string{"WEB_URL": "${{@clinic-iq/web.APP_URL}}"}},
			{ServiceID: "svc-long", Name: "backend-cliniciq-worker-notifications-dev", Repo: "platform-cliniciq/backend-cliniciq",
				Branch: "main", Region: "us-east4"},
			{ServiceID: "svc-mail", Name: "mailhog", Image: "mailhog/mailhog", Region: "us-east4",
				ServiceDomains: []string{"mailhog-production.up.railway.app"}, DomainPort: 8025},
		},
	}}}
}

// railwayServer answers the GraphQL documents the importer sends, for the given
// projects, the way Railway's API does.
func railwayServer(t *testing.T, projects ...rwProject) *httptest.Server {
	t.Helper()
	findEnv := func(id string) (*rwEnv, bool) {
		for p := range projects {
			for e := range projects[p].Envs {
				if projects[p].Envs[e].ID == id {
					return &projects[p].Envs[e], true
				}
			}
		}
		return nil, false
	}
	findInstance := func(envID, serviceID string) (rwInstance, bool) {
		env, ok := findEnv(envID)
		if !ok {
			return rwInstance{}, false
		}
		for _, inst := range env.Instances {
			if inst.ServiceID == serviceID {
				return inst, true
			}
		}
		return rwInstance{}, false
	}
	write := func(w http.ResponseWriter, v any) {
		body, err := json.Marshal(map[string]any{"data": v})
		if err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write(body)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Query     string         `json:"query"`
			Variables map[string]any `json:"variables"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.Header().Set("content-type", "application/json")
		str := func(k string) string { s, _ := body.Variables[k].(string); return s }
		switch {
		case strings.Contains(body.Query, "query Project("):
			for _, p := range projects {
				if p.ID == str("id") {
					var envs []map[string]any
					for _, e := range p.Envs {
						envs = append(envs, map[string]any{"node": map[string]any{"id": e.ID, "name": e.Name}})
					}
					write(w, map[string]any{"project": map[string]any{"id": p.ID, "name": p.Name, "environments": map[string]any{"edges": envs}}})
					return
				}
			}
			write(w, map[string]any{"project": nil})
		case strings.Contains(body.Query, "query Environment"):
			env, _ := findEnv(str("id"))
			var instances, volumes []map[string]any
			for _, inst := range env.Instances {
				node := map[string]any{
					"serviceId": inst.ServiceID, "serviceName": inst.Name,
					"source":        map[string]any{"repo": nullable(inst.Repo), "image": nullable(inst.Image)},
					"rootDirectory": nullable(inst.Root), "startCommand": nullable(inst.Start),
					"buildCommand": nullable(inst.Build), "dockerfilePath": nullable(inst.Dockerfile),
					"healthcheckPath": nullable(inst.Health), "restartPolicyType": "ON_FAILURE",
					"restartPolicyMaxRetries": 10, "numReplicas": inst.Replicas, "region": nullable(inst.Region),
					"preDeployCommand": inst.PreDeploy, "railwayConfigFile": nullable(inst.ConfigFile),
				}
				if inst.FileManifest != "" {
					node["resolvedFileConfig"] = map[string]any{"configFile": nullable(inst.ConfigFile), "fileManifest": json.RawMessage(inst.FileManifest)}
				}
				if inst.ServiceManifest != "" {
					node["latestDeployment"] = map[string]any{"id": "dep-" + inst.ServiceID, "status": "SUCCESS",
						"meta": map[string]any{"serviceManifest": json.RawMessage(inst.ServiceManifest)}}
				}
				instances = append(instances, map[string]any{"node": node})
				for i, v := range inst.Volumes {
					volumes = append(volumes, map[string]any{"node": map[string]any{
						"id": fmt.Sprintf("vi-%s-%d", inst.ServiceID, i), "mountPath": v.MountPath, "sizeMB": v.SizeMB,
						"serviceId": inst.ServiceID, "volume": map[string]any{"id": "v", "name": "data"}}})
				}
			}
			write(w, map[string]any{"environment": map[string]any{"id": env.ID, "name": env.Name,
				"serviceInstances": map[string]any{"edges": instances}, "volumeInstances": map[string]any{"edges": volumes}}})
		case strings.Contains(body.Query, "query Variables"):
			if str("serviceId") == "" {
				env, _ := findEnv(str("environmentId"))
				write(w, map[string]any{"variables": nonNil(env.Shared)})
				return
			}
			inst, _ := findInstance(str("environmentId"), str("serviceId"))
			write(w, map[string]any{"variables": nonNil(inst.Vars)})
		case strings.Contains(body.Query, "query Domains"):
			inst, _ := findInstance(str("environmentId"), str("serviceId"))
			var service, custom []map[string]any
			port := any(nil)
			if inst.DomainPort > 0 {
				port = inst.DomainPort
			}
			for _, d := range inst.ServiceDomains {
				service = append(service, map[string]any{"id": d, "domain": d, "targetPort": port})
			}
			for _, d := range inst.CustomDomains {
				custom = append(custom, map[string]any{"id": d, "domain": d, "targetPort": port})
			}
			write(w, map[string]any{"domains": map[string]any{"serviceDomains": service, "customDomains": custom}})
		case strings.Contains(body.Query, "query Triggers"):
			inst, _ := findInstance(str("environmentId"), str("serviceId"))
			var edges []map[string]any
			if inst.Branch != "" {
				edges = append(edges, map[string]any{"node": map[string]any{"id": "t", "branch": inst.Branch, "repository": inst.Repo}})
			}
			write(w, map[string]any{"deploymentTriggers": map[string]any{"edges": edges}})
		case strings.Contains(body.Query, "query Plugins"):
			_, _ = w.Write([]byte(`{"errors":[{"message":"Plugins have been removed"}]}`))
		default:
			t.Errorf("unexpected Railway query: %s", body.Query)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func nonNil(m map[string]string) map[string]string {
	if m == nil {
		return map[string]string{}
	}
	return m
}

// fakeMiosa is a stateful MIOSA API for the applier: projects, Deploy targets,
// services per target, the GitHub link, variables, shared variables and the
// database change set. A second apply against the same fake sees what the
// first one made, which is what proves a re-run idempotent.
type fakeMiosa struct {
	mu           sync.Mutex
	sourceBuilds bool
	github       []map[string]any // repositories the connection can read; nil = not connected
	projects     []map[string]any
	envs         map[string][]map[string]any // project -> targets
	services     map[string]map[string]any   // service id -> row
	staged       map[string]map[string]any   // change id -> staged service row
	seq          int
}

func newFakeMiosa() *fakeMiosa {
	return &fakeMiosa{
		sourceBuilds: true,
		github: []map[string]any{
			{"id": 1237052916, "full_name": "platform-cliniciq/backend-cliniciq", "private": true},
			{"id": 1237052917, "full_name": "platform-cliniciq/clinic-iq-frontend", "private": true},
			{"id": 1237052918, "full_name": "platform-cliniciq/clinic-iq-beta", "private": true},
		},
		envs:     map[string][]map[string]any{},
		services: map[string]map[string]any{},
		staged:   map[string]map[string]any{},
	}
}

func (m *fakeMiosa) id(prefix string) string {
	m.seq++
	return fmt.Sprintf("%s-%03d", prefix, m.seq)
}

func jsonOf(v any) string {
	body, _ := json.Marshal(v)
	return string(body)
}

func (m *fakeMiosa) reply(c call) (int, string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	envOf := func(projectID string) string {
		q := c.Query
		if strings.HasPrefix(q, "env=") {
			return strings.TrimPrefix(q, "env=")
		}
		for _, e := range m.envs[projectID] {
			if e["is_default"] == true {
				return e["id"].(string)
			}
		}
		return ""
	}
	parts := strings.Split(strings.Trim(c.Path, "/"), "/")
	switch {
	case c.is("GET", "/workspaces"):
		return 200, `{"data":[{"id":"9c0ffee0-0000-4000-8000-000000000001","slug":"cliniciq-platform","name":"ClinicIQ Platform"}]}`
	case c.is("GET", "/project-capabilities"):
		return 200, jsonOf(map[string]any{"data": map[string]any{"services_api": true, "source_builds": m.sourceBuilds}})
	case c.is("GET", "/integrations/github/repos"):
		if m.github == nil {
			return 412, `{"error":"GitHub integration not connected"}`
		}
		return 200, jsonOf(map[string]any{"data": m.github})
	case c.is("GET", "/projects"):
		return 200, jsonOf(map[string]any{"data": nonNilRows(m.projects)})
	case c.is("POST", "/projects"):
		id := m.id("proj")
		m.projects = append(m.projects, map[string]any{"id": id, "name": c.Body["name"], "slug": c.Body["name"]})
		// The first read of a project makes its default target, production.
		m.envs[id] = []map[string]any{{"id": m.id("env"), "name": "production", "is_default": true}}
		return 201, jsonOf(map[string]any{"data": map[string]any{"id": id}})
	case len(parts) == 3 && parts[0] == "projects" && parts[2] == "environments":
		if c.Method == "GET" {
			return 200, jsonOf(map[string]any{"data": nonNilRows(m.envs[parts[1]])})
		}
		env := map[string]any{"id": m.id("env"), "name": c.Body["name"]}
		m.envs[parts[1]] = append(m.envs[parts[1]], env)
		return 201, jsonOf(map[string]any{"data": env})
	case len(parts) == 3 && parts[0] == "projects" && parts[2] == "changes" && c.Method == "GET":
		return 200, `{"data":[]}`
	case len(parts) == 3 && parts[0] == "projects" && parts[2] == "changes" && c.Method == "POST":
		payload, _ := c.Body["payload"].(map[string]any)
		change := m.id("chg")
		m.staged[change] = map[string]any{"project_id": parts[1], "environment_id": envOf(parts[1]),
			"name": payload["name"], "slug": payload["slug"], "kind": "database", "source": map[string]any{}}
		return 201, jsonOf(map[string]any{"data": map[string]any{"id": change}})
	case len(parts) == 4 && parts[2] == "changes" && parts[3] == "apply":
		// The applied set lists its changes, each with the service it made.
		var changes []map[string]any
		for change, row := range m.staged {
			id := m.id("svc")
			row["id"] = id
			m.services[id] = row
			changes = append(changes, map[string]any{"id": change, "result": map[string]any{"service_id": id}})
			delete(m.staged, change)
		}
		return 200, jsonOf(map[string]any{"data": map[string]any{"id": "set", "state": "applied", "changes": changes}})
	case len(parts) == 3 && parts[0] == "projects" && parts[2] == "services" && c.Method == "GET":
		env := envOf(parts[1])
		var rows []map[string]any
		for _, s := range m.services {
			if s["project_id"] == parts[1] && s["environment_id"] == env {
				rows = append(rows, s)
			}
		}
		return 200, jsonOf(map[string]any{"data": nonNilRows(rows)})
	case len(parts) == 3 && parts[0] == "projects" && parts[2] == "services" && c.Method == "POST":
		source, _ := c.Body["source"].(map[string]any)
		if t, _ := source["type"].(string); (t == "git" || t == "forge") && !m.sourceBuilds {
			return 422, `{"error":{"code":"SOURCE_BUILD_UNAVAILABLE","message":"no builds"}}`
		}
		id := m.id("svc")
		slug, _ := c.Body["slug"].(string)
		if !slugRule.MatchString(slug) {
			return 422, `{"error":{"code":"VALIDATION_FAILED","message":"slug must be lowercase a-z 0-9 - (1-40 chars)"}}`
		}
		row := map[string]any{"id": id, "project_id": parts[1], "environment_id": envOf(parts[1]),
			"name": c.Body["name"], "slug": slug, "kind": c.Body["kind"], "source": source, "settings": c.Body["settings"]}
		if row["source"] == nil {
			row["source"] = map[string]any{}
		}
		m.services[id] = row
		return 201, jsonOf(map[string]any{"data": row})
	case len(parts) == 4 && parts[2] == "services" && c.Method == "GET":
		return 200, jsonOf(map[string]any{"data": m.services[parts[3]]})
	case len(parts) == 4 && parts[2] == "services" && c.Method == "PATCH":
		row := m.services[parts[3]]
		if source, ok := c.Body["source"].(map[string]any); ok {
			row["source"] = source
		}
		return 200, jsonOf(map[string]any{"data": row})
	case len(parts) == 6 && parts[4] == "source" && parts[5] == "github":
		row := m.services[parts[3]]
		source := map[string]any{"type": "forge", "repository_id": "forge-" + fmt.Sprint(c.Body["github_repository_id"]),
			"branch": c.Body["branch"]}
		if dir, ok := c.Body["root_dir"]; ok {
			source["root_dir"] = dir
		}
		row["source"] = source
		return 202, jsonOf(map[string]any{"data": map[string]any{"service": row, "import": map[string]any{"id": "imp-" + parts[3], "status": "queued"}}})
	case strings.HasPrefix(c.Path, "/forge/github/imports/"):
		return 200, `{"data":{"source":{"status":"synced"}}}`
	case c.Method == "PUT" && (strings.HasSuffix(c.Path, "/variables") || strings.HasSuffix(c.Path, "/public-address") || strings.HasSuffix(c.Path, "/shared-variables")):
		return 200, `{"data":{}}`
	case c.Method == "POST" && strings.HasSuffix(c.Path, "/domains"):
		return 201, `{"data":{}}`
	case c.Method == "POST" && strings.HasSuffix(c.Path, "/deployments"):
		return 201, `{"data":{"id":"rel","state":"queued"}}`
	case c.is("GET", "/databases"):
		return 200, `{"data":[]}`
	}
	return 404, `{"error":{"code":"NOT_FOUND","message":"no route ` + c.Method + ` ` + c.Path + `"}}`
}

// slugRule is MIOSA's service slug rule.
var slugRule = regexp.MustCompile(`\A[a-z0-9]([a-z0-9-]{0,38}[a-z0-9])?\z`)

func nonNilRows(rows []map[string]any) []map[string]any {
	if rows == nil {
		return []map[string]any{}
	}
	return rows
}
