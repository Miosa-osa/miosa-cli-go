package railway

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// IsRailwayBuiltin reports a variable Railway injects or reads itself
// (RAILWAY_*). Such a variable is not imported: MIOSA injects its own
// (MIOSA_*), and a reference to one is rewritten by rewriteReferences.
func IsRailwayBuiltin(name string) bool {
	return strings.HasPrefix(strings.ToUpper(name), "RAILWAY_")
}

var referencePattern = regexp.MustCompile(`\$\{\{\s*([^{}]+?)\s*\}\}`)

// builtinNames maps the Railway built-ins MIOSA has an equivalent for. Any
// other RAILWAY_* reference (the public domain, TCP proxy, volume, git and
// replica names) has none and is reported.
var builtinNames = map[string]string{
	"RAILWAY_PRIVATE_DOMAIN":   "MIOSA_PRIVATE_DOMAIN",
	"RAILWAY_SERVICE_NAME":     "MIOSA_SERVICE_NAME",
	"RAILWAY_SERVICE_ID":       "MIOSA_SERVICE_ID",
	"RAILWAY_ENVIRONMENT":      "MIOSA_ENVIRONMENT_NAME",
	"RAILWAY_ENVIRONMENT_NAME": "MIOSA_ENVIRONMENT_NAME",
	"RAILWAY_ENVIRONMENT_ID":   "MIOSA_ENVIRONMENT_ID",
	"RAILWAY_PROJECT_ID":       "MIOSA_PROJECT_ID",
	"RAILWAY_PROJECT_NAME":     "MIOSA_PROJECT_NAME",
	"RAILWAY_REGION":           "MIOSA_REGION",
}

// databaseNames maps, per engine, the names a Railway database template
// exposes onto the names a MIOSA database service provides (DATABASE_URL,
// PGHOST..PGDATABASE for Postgres; REDIS_URL, REDIS_HOST, REDIS_PORT,
// REDIS_PASSWORD for Redis). A value starting with "=" is a literal that
// replaces the whole reference: MIOSA's Redis user is always "default".
// An empty value has no equivalent.
var databaseNames = map[string]map[string]string{
	"postgres": {
		"DATABASE_URL":           "DATABASE_URL",
		"PGHOST":                 "PGHOST",
		"PGPORT":                 "PGPORT",
		"PGUSER":                 "PGUSER",
		"PGPASSWORD":             "PGPASSWORD",
		"PGDATABASE":             "PGDATABASE",
		"POSTGRES_USER":          "PGUSER",
		"POSTGRES_PASSWORD":      "PGPASSWORD",
		"POSTGRES_DB":            "PGDATABASE",
		"RAILWAY_PRIVATE_DOMAIN": "PGHOST",
		"DATABASE_PUBLIC_URL":    "",
	},
	"redis": {
		"REDIS_URL":              "REDIS_URL",
		"REDIS_PASSWORD":         "REDIS_PASSWORD",
		"REDISHOST":              "REDIS_HOST",
		"REDISPORT":              "REDIS_PORT",
		"REDISPASSWORD":          "REDIS_PASSWORD",
		"REDISUSER":              "=default",
		"RAILWAY_PRIVATE_DOMAIN": "REDIS_HOST",
		"REDIS_PUBLIC_URL":       "",
	},
}

// refContext is what a reference rewrite needs to know about the plan: each
// database service's engine (by lower-case MIOSA name), and the services the
// import renamed (Railway name -> MIOSA name).
type refContext struct {
	engines map[string]string
	renames map[string]string
}

// rewriteReferences rewrites every ${{...}} in value onto MIOSA's names and
// renamed services. It answers the new value, each reference that has no MIOSA
// equivalent, and each service now reached by its private domain.
func rewriteReferences(value string, ctx refContext) (string, []string, []string) {
	var unmappable, private []string
	out := referencePattern.ReplaceAllStringFunc(value, func(token string) string {
		inner := strings.TrimSpace(referencePattern.FindStringSubmatch(token)[1])
		service, key, scoped := strings.Cut(inner, ".")
		if !scoped {
			key, service = inner, ""
		}
		if strings.EqualFold(service, "shared") {
			return token
		}
		if renamed, ok := ctx.renames[service]; ok {
			service = renamed
			token = "${{" + service + "." + key + "}}"
		}
		if engine := ctx.engines[strings.ToLower(service)]; engine != "" {
			if names, known := databaseNames[engine]; known {
				mapped, ok := names[key]
				switch {
				case ok && strings.HasPrefix(mapped, "="):
					return strings.TrimPrefix(mapped, "=")
				case ok && mapped != "":
					return "${{" + service + "." + mapped + "}}"
				case ok:
					unmappable = append(unmappable, token)
					return token
				}
			}
		}
		if !IsRailwayBuiltin(key) {
			return token
		}
		mapped := builtinNames[strings.ToUpper(key)]
		if mapped == "" {
			unmappable = append(unmappable, token)
			return token
		}
		if service == "" {
			return "${{" + mapped + "}}"
		}
		if mapped == "MIOSA_PRIVATE_DOMAIN" {
			private = append(private, service)
		}
		return "${{" + service + "." + mapped + "}}"
	})
	return out, unmappable, private
}

// carryVariable turns one Railway variable into what MIOSA gets: the
// reference rewritten onto MIOSA's names, or, when it references something
// MIOSA does not have, no value (it then needs one) and a report. A variable
// that now reaches another service by its private domain is reported too:
// private networking between services is off on MIOSA today.
func carryVariable(v Variable, owner, env string, ctx refContext) (Variable, []Action) {
	if !v.HasValue || !strings.Contains(v.Value, "${{") {
		return v, nil
	}
	rewritten, unmappable, private := rewriteReferences(v.Value, ctx)
	if len(unmappable) > 0 {
		sort.Strings(unmappable)
		return Variable{Name: v.Name}, []Action{{
			Op:    "unmapped",
			Env:   env,
			Name:  owner,
			Field: "variable " + v.Name,
			Why:   fmt.Sprintf("it references %s, which MIOSA has no equivalent for", strings.Join(unmappable, ", ")),
			Next:  "it is created without a value; set it after the import",
		}}
	}
	v.Value = rewritten
	var reports []Action
	for _, target := range private {
		next := "point it at an address MIOSA can reach"
		if strings.Contains(strings.ToUpper(v.Name), "SMTP") {
			next = "point SMTP at your provider"
		}
		reports = append(reports, Action{
			Op:    "unmapped",
			Env:   env,
			Name:  owner,
			Field: "variable " + v.Name,
			Why:   fmt.Sprintf("it reaches %s by its private domain, and %s is unreachable by name on MIOSA today", target, target),
			Next:  next,
		})
	}
	return v, reports
}
