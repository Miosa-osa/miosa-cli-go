<!-- version: 1 -->
# sdks/cli: agent instructions

The `miosa` CLI (Go, cobra), shipped as release binaries and as `@miosa/cli` on npm. The root AGENTS.md applies too.
The `miosa-cli` submodule at the repository root is an older, separate repository; do not change it for CLI work.

## Commands (from `sdks/cli`)

- Build, vet, test (what `Forge / CLI` runs): `go build ./... && go vet ./... && go test ./...`
- One test: `go test ./commands -run TestName`. npm wrapper: `cd npm && npm test`.
- Lint: `bash scripts/lint/hygiene.sh go` from the repository root (golangci-lint and deadcode, ratcheted).
- Command reference: `make docs` after any command, flag or help change.

## Generated files

| Path | Regenerate with | When |
|---|---|---|
| `docs/commands.md`, `docs/api-map.md` | `make docs` | a command, flag or help text changes |
| `testdata/product-language-baseline.json` | `go test ./commands -run TestProductLanguage -update-language-baseline` | only when a cleanup removes violations |

`docs/*.md` are blocked from hand edits [hook: protect-files], but no check compares them with the code. [review]

## Traps

- The CLI does not build against `sdks/go`. `go.mod` replaces `github.com/Miosa-osa/miosa-go` with its own copy in
  `internal/miosa-sdk`, which has drifted from `sdks/go`. A fix the CLI needs goes into that copy. [review]
- Exit codes, `--json` output and the `{ "data": ... }` envelope are documented in README.md and scripts depend on
  them. Changing one needs a compatibility decision in the PR. [review]
- Help text and error messages follow docs/product/product-language.md. [lint: go test ./commands -run TestProductLanguage]
- `testdata/forge-cli-command-contract.json` and `tests/fixtures/forge-cli-command-contract.json` (repository root)
  must stay identical. [lint: go test ./commands -run TestForgeCommandContractMatchesBothDistributions]
- New unreachable functions fail the lint check. [lint: deadcode]
- Never type a version: the binary gets it from the tag (`-ldflags`), `npm/cli/package.json` from the Release PR.
- `go.mod` pins the release toolchain (`toolchain go1.27.2`); `.goreleaser.yml` asserts it. Do not change it in a
  feature PR. [review]
