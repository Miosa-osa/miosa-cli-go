# Import a Railway project

`miosa import railway` reads a Railway project through Railway's public API and
writes a plan: the MIOSA project, Deploy targets, services, databases and
variable names it would create, and everything it cannot carry across.

Nothing is created until you review the plan and apply it.

## The three steps

```bash
export RAILWAY_TOKEN=...                    # a Railway account, workspace or project token
miosa import railway --project acme-api     # review
miosa import railway --project acme-api --out plan.json
miosa import railway --apply --plan plan.json
```

The token is read from `RAILWAY_TOKEN`, `--token-file`, or a prompt on a TTY.
It is never written to the profile, the plan, the state file or a log line.

## What the review looks like

```
acme-api (production) -> MIOSA project acme-api

  create project acme-api
  create database Postgres (postgres)
  create server api from https://github.com/acme/api (main)
  create cron worker from ghcr.io/acme/worker:1

Variables carried:
  api.DATABASE_URL (reference, kept)
  api.LOG_LEVEL (value)

Variables that need a value (1):
  api.STRIPE_KEY

Finish by hand:
  api address: api was api-production.up.railway.app; MIOSA assigns a new <label>.<org>.miosa.app
  api buildCommand: MIOSA builds from the Dockerfile or detects the stack; there is no build command setting -> move it into the Dockerfile or a build script
  api volume /data: the Services API has no service volumes; Railway mounted 5000 MB at /data -> miosa volume create api-data --size-gb 5, then attach it where the data is needed

warning: Railway disclosed on 2026-10-21 that its secrets were exposed. Rotate every secret you rely on; no literal secret is copied unless you pass --copy-variables.
```

Every field that cannot be carried is listed under "Finish by hand" with the
MIOSA command that does the equivalent. Nothing is dropped silently.

## Variables and secrets

Names are carried by default, and references such as
`${{Postgres.DATABASE_URL}}` are carried as references because a reference is
not a secret. MIOSA uses the same syntax, so a reference keeps working when the
service names match.

`--copy-variables` copies literal values too, and prints a warning: Railway
disclosed on 2026-10-21 that its secrets were exposed, so rotate every one you
rely on.

An apply refuses to run while a variable has no value. Fill the values into the
plan, or pass `--allow-missing-values` to create them empty and fill them in
afterwards.

## Applying

`--apply` runs the plan in order: project, Deploy targets, databases, services,
variables. Databases come first so a reference resolves when the first release
builds.

Names are the idempotency key: a project, target, service or database that
already exists with that name is reused and reported, never duplicated.

Every created id is appended to the state file (`.miosa/import-railway-<project>.json`
by default) as soon as it exists:

```bash
miosa import railway --apply --plan plan.json --resume    # continue after a stop
miosa import railway --cleanup --state .miosa/import-railway-acme-api.json --yes
```

`--cleanup` prints exactly the ids it will delete before deleting them.

## From a config file, without a token

```bash
miosa import railway --from-file ./railway.toml --service-name api
```

Reads Railway's `railway.json` or `railway.toml` locally and produces a
one-service plan. No token and no network call. The plan has no source: pass
`--repo` or `--image` at apply time, or edit the plan.

## What is not carried

| Railway | why |
|---|---|
| `buildCommand` | MIOSA builds from the Dockerfile or detects the stack |
| `healthcheckPath`, `restartPolicyType`, `sleepApplication`, `watchPatterns`, `preDeployCommand` | no service setting yet |
| service volumes | the Services API has no service volumes; MIOSA volumes attach to machines |
| `*.up.railway.app` domains | Railway-owned DNS; MIOSA assigns its own |
| custom domains | DNS must point at MIOSA first |
| MongoDB plugins | no managed MongoDB |
| non-production environments | reported unless `--include-targets` |
