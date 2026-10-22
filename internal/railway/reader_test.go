package railway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeGraphQL answers the documents Read sends, in the shape Railway answers.
//
// Every body here is synthetic: written from Railway's public schema and docs,
// never recorded from a Railway account.
// It records the variables of the variables() read so the test can prove the
// read is unrendered.
func fakeGraphQL(t *testing.T) (*httptest.Server, *map[string]any, *string) {
	t.Helper()
	lastVars := map[string]any{}
	rendered := ""
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("authorization"); got != "Bearer rail_test" {
			t.Errorf("authorization = %q", got)
		}
		var body struct {
			Query     string         `json:"query"`
			Variables map[string]any `json:"variables"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode: %v", err)
		}
		w.Header().Set("content-type", "application/json")
		switch {
		case strings.Contains(body.Query, "query Projects"):
			_, _ = w.Write([]byte(`{"data":{"projects":{"edges":[{"node":{"id":"11111111-1111-1111-1111-111111111111","name":"acme-api"}}]}}}`))
		case strings.Contains(body.Query, "query Project("):
			_, _ = w.Write([]byte(`{"data":{"project":{"id":"11111111-1111-1111-1111-111111111111","name":"acme-api","environments":{"edges":[{"node":{"id":"e1","name":"production"}},{"node":{"id":"e2","name":"staging"}}]}}}}`))
		case strings.Contains(body.Query, "query Environment"):
			_, _ = w.Write([]byte(`{"data":{"environment":{"id":"e1","name":"production",
				"serviceInstances":{"edges":[{"node":{
					"serviceId":"s1","serviceName":"api",
					"source":{"repo":"acme/api","image":null},
					"buildCommand":"npm run build","startCommand":"npm start",
					"rootDirectory":"/app","dockerfilePath":"Dockerfile",
					"healthcheckPath":"/health","restartPolicyType":"ON_FAILURE",
					"numReplicas":2,"region":"us-east4","cronSchedule":null
				}}]},
				"volumeInstances":{"edges":[{"node":{"id":"v1","mountPath":"/data","sizeMB":5000,"serviceId":"s1","volume":{"id":"vol1","name":"data"}}}]}}}}`))
		case strings.Contains(body.Query, "query Variables"):
			lastVars = body.Variables
			rendered = body.Query
			_, _ = w.Write([]byte(`{"data":{"variables":{"DATABASE_URL":"${{Postgres.DATABASE_URL}}","LOG_LEVEL":"info","STRIPE_KEY":"sk_live_123"}}}`))
		case strings.Contains(body.Query, "query Domains"):
			_, _ = w.Write([]byte(`{"data":{"domains":{"serviceDomains":[{"id":"d1","domain":"api-production.up.railway.app"}],"customDomains":[{"id":"d2","domain":"api.acme.com"}]}}}`))
		case strings.Contains(body.Query, "query Triggers"):
			_, _ = w.Write([]byte(`{"data":{"deploymentTriggers":{"edges":[{"node":{"id":"t1","branch":"main","repository":"acme/api"}}]}}}`))
		case strings.Contains(body.Query, "query Plugins"):
			// Railway removed plugins: an error here must not fail the read.
			_, _ = w.Write([]byte(`{"errors":[{"message":"Plugins have been removed"}]}`))
		default:
			t.Errorf("unexpected query: %s", body.Query)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, &lastVars, &rendered
}

func TestReaderReadsAProjectUnrendered(t *testing.T) {
	srv, lastVars, variablesQuery := fakeGraphQL(t)
	client := NewClient("rail_test")
	client.Endpoint = srv.URL

	src, err := (Reader{Client: client}).Read(context.Background(), "acme-api", "production")
	if err != nil {
		t.Fatal(err)
	}
	if src.ProjectID == "" || src.ProjectName != "acme-api" || src.Environment != "production" {
		t.Fatalf("source = %+v", src)
	}
	if len(src.Services) != 1 {
		t.Fatalf("services = %+v", src.Services)
	}
	svc := src.Services[0]
	if svc.Repo != "acme/api" || svc.Branch != "main" || svc.Region != "us-east4" || svc.NumReplicas != 2 {
		t.Fatalf("service = %+v", svc)
	}
	if len(svc.Volumes) != 1 || svc.Volumes[0].MountPath != "/data" || svc.Volumes[0].SizeMB != 5000 {
		t.Fatalf("volumes = %+v", svc.Volumes)
	}
	if len(svc.CustomDomains) != 1 || svc.CustomDomains[0] != "api.acme.com" {
		t.Fatalf("domains = %+v", svc.CustomDomains)
	}
	if !strings.Contains(*variablesQuery, "unrendered: true") {
		t.Fatalf("variables must be read unrendered, got %s", *variablesQuery)
	}
	if (*lastVars)["serviceId"] != "s1" {
		t.Fatalf("variables must be read for the service, got %v", *lastVars)
	}
	if len(src.Plugins) != 0 {
		t.Fatalf("a removed plugin field must not fail or invent plugins: %+v", src.Plugins)
	}

	byName := map[string]Variable{}
	for _, v := range svc.Variables {
		byName[v.Name] = v
	}
	if got := byName["DATABASE_URL"]; !got.HasValue || got.Value != "${{Postgres.DATABASE_URL}}" {
		t.Fatalf("a reference is not a secret and must be kept: %+v", got)
	}
	if got := byName["STRIPE_KEY"]; got.HasValue {
		t.Fatalf("a literal must not be copied by default: %+v", got)
	}
}

func TestReaderCopiesLiteralsOnlyWhenAsked(t *testing.T) {
	srv, _, _ := fakeGraphQL(t)
	client := NewClient("rail_test")
	client.Endpoint = srv.URL

	src, err := (Reader{Client: client, CopyVariables: true}).Read(context.Background(), "acme-api", "production")
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]Variable{}
	for _, v := range src.Services[0].Variables {
		byName[v.Name] = v
	}
	if got := byName["STRIPE_KEY"]; !got.HasValue || got.Value != "sk_live_123" {
		t.Fatalf("--copy-variables must carry literals: %+v", got)
	}
}

func TestReaderRefusesAnUnknownEnvironment(t *testing.T) {
	srv, _, _ := fakeGraphQL(t)
	client := NewClient("rail_test")
	client.Endpoint = srv.URL

	_, err := (Reader{Client: client}).Read(context.Background(), "acme-api", "qa")
	if err == nil || !strings.Contains(err.Error(), "no Railway environment named") {
		t.Fatalf("want an environment error, got %v", err)
	}
}

func TestReaderReportsARejectedToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"errors":[{"message":"Unauthorized"}]}`))
	}))
	t.Cleanup(srv.Close)
	client := NewClient("rail_test")
	client.Endpoint = srv.URL
	client.MaxRetries = 0

	_, err := (Reader{Client: client}).Read(context.Background(), "11111111-1111-1111-1111-111111111111", "production")
	if err == nil || !strings.Contains(err.Error(), "Unauthorized") {
		t.Fatalf("want the API's message, got %v", err)
	}
}

func TestReadFileParsesBothFormats(t *testing.T) {
	dir := t.TempDir()
	jsonPath := filepath.Join(dir, "railway.json")
	tomlPath := filepath.Join(dir, "railway.toml")
	if err := os.WriteFile(jsonPath, []byte(`{
		"$schema":"https://railway.com/railway.schema.json",
		"build":{"builder":"DOCKERFILE","dockerfilePath":"Dockerfile.prod","buildCommand":"npm run build"},
		"deploy":{"startCommand":"npm start","numReplicas":2,"region":"us-east4","cronSchedule":"0 3 * * *"}
	}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(tomlPath, []byte(`
[build]
builder = "DOCKERFILE"
dockerfilePath = "Dockerfile.prod"

[deploy]
startCommand = "npm start"
numReplicas = 3
region = "us-west1"
preDeployCommand = ["npm run migrate"]
`), 0o644); err != nil {
		t.Fatal(err)
	}

	fromJSON, err := ReadFile(jsonPath)
	if err != nil {
		t.Fatal(err)
	}
	if fromJSON.Build.DockerfilePath != "Dockerfile.prod" || fromJSON.Deploy.NumReplicas != 2 || fromJSON.Deploy.CronSchedule != "0 3 * * *" {
		t.Fatalf("json = %+v", fromJSON)
	}

	fromTOML, err := ReadFile(tomlPath)
	if err != nil {
		t.Fatal(err)
	}
	if fromTOML.Deploy.NumReplicas != 3 || fromTOML.Deploy.Region != "us-west1" || len(fromTOML.Deploy.PreDeployCommand) != 1 {
		t.Fatalf("toml = %+v", fromTOML)
	}

	if _, err := ReadFile(filepath.Join(dir, "railway.yaml")); err == nil {
		t.Fatal("a name that is neither json nor toml must be refused")
	}
}
