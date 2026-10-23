# Moving a Vercel project to MIOSA

`miosa import vercel` reads your `vercel.json` and writes a plan: the MIOSA
project, the `production` Deploy target, one service, and the environment
variable **names** it declares. Nothing is created until you apply the plan.

```bash
miosa import vercel --from-file vercel.json --name shop \
  --repo https://github.com/acme/shop --out plan.json
miosa import vercel --apply --plan plan.json
```

## What maps

| vercel.json | MIOSA |
|---|---|
| the app | one service, kind `server` |
| `regions` (a single region MIOSA has) | service `settings.region` |
| `env` keys | variable names, with no values |

## What does not, and says so

`rewrites`, `redirects`, `headers`, `builds`, `functions`, `cleanUrls`,
`trailingSlash`, `buildCommand`, `installCommand`, `outputDirectory` and
`crons` are listed in the plan with the reason, never dropped quietly. Crons
name the schedule to create a cron service with; rewrites and redirects belong
in the app or a proxy today.

## Values

No variable value is read from Vercel. Set them after the import:

```bash
miosa link shop
miosa variables set DATABASE_URL=... NEXT_PUBLIC_URL=...
```

## Rollback

`miosa import vercel --apply` records every created id in
`.miosa/import-vercel-<project>.json`. `--cleanup --state <file>` deletes
exactly those ids and nothing else.
