# miosa CLI

The official public command-line tool for [MIOSA](https://miosa.ai).

`miosa` covers sandboxes and computers, files and previews, agents and their runs, deployments, databases, storage, API keys, webhooks, billing and the organization you work in.
Internal OSA tooling such as `osagent` stays behind MIOSA and is not the public installation path.

The complete command reference is generated from the code: [docs/commands.md](docs/commands.md).
The API route behind each declarative command is in [docs/api-map.md](docs/api-map.md).
Both are regenerated with `make docs`.

## Install

**macOS (Homebrew)**
```sh
brew install --cask Miosa-osa/homebrew-tap/miosa
```

**Linux / macOS (install script)**
```sh
curl -fsSL https://raw.githubusercontent.com/Miosa-osa/miosa-cli-go/main/install.sh | sh
```

By default this installs to `~/.local/bin/miosa` for non-root users and `/usr/local/bin/miosa` for root.
Override with `INSTALL_DIR=/usr/local/bin`.

**Manual**: download from [GitHub Releases](https://github.com/Miosa-osa/miosa-cli-go/releases/latest) and place the binary in a directory on your `$PATH`.

**From source**
```sh
cd sdks/cli
make install   # builds and copies to ~/.local/bin/miosa
```

Update an installed binary with `miosa update`.
It verifies the SHA-256 of the download against the release `checksums.txt`, and refuses to replace a Homebrew-managed binary (use `brew upgrade` there).
`miosa update --check` only reports.
A one-line notice appears at most once a day when a newer version exists; silence it with `--no-update-notice` or `MIOSA_NO_UPDATE_NOTICE=1`.

Shell completion: `miosa completion zsh|bash|fish|powershell`.
Sandbox names, profiles, templates and agents complete from the live API.

## Quick start

```sh
miosa login                       # opens the browser, device-code sign in
miosa whoami                      # organization, plan, credits
miosa create my-box --wait        # provision a sandbox
miosa exec my-box -- echo hello
miosa console my-box              # interactive shell
miosa destroy my-box --yes
```

## Authentication and profiles

`miosa login` signs in through the browser (device code).
`miosa whoami` shows the user, organization, workspace, plan and the scopes the key holds.
`miosa login --with-key` asks for a key with hidden input, and `--key-stdin` reads it from stdin; a key is never accepted as an argument.
CI uses `MIOSA_API_KEY`.

Credentials resolve in this order: `--api-key`, `MIOSA_API_KEY`, the active profile in `~/.miosa/config.toml`.

A profile is a named sign-in with its own API URL, organization and defaults.

```sh
miosa login --profile work
miosa profile list
miosa profile use work
miosa --profile default list
```

The profile in effect is `--profile`, then `MIOSA_PROFILE`, then the one set with `profile use`, then `default`.
`miosa doctor` checks configuration, connectivity and authentication and prints a fix for each failure.
`miosa logout` removes the stored key of the active profile.

### One-shot runs, batches and service accounts

```sh
miosa run --rm -- python -c 'print(6 * 7)'          # fresh sandbox, run, print, destroy; exits with the command's status
miosa run --size small --set TOKEN=abc -- ./build.sh # without --rm the sandbox is kept
miosa batch new --count 50 --size small --name-prefix ci --wait
miosa batch status <batch>
miosa service-account new deploy-pipeline
miosa service-account key new <account> --preset ci --expires-in 90 --quiet-key
```

### Forge collaboration

```sh
miosa forge pr new platform --head fix-redirect --title "Fix redirect loop"
miosa forge pr review platform 12 --decision approved
miosa forge pr merge platform 12
miosa forge check report platform --commit <sha> --name unit-tests --conclusion passed --file evidence.json
miosa forge release new platform --tag v1.2.0
miosa forge policy set platform --branch main --approvals 2 --check unit-tests
```

### Your own hosts, cloud and the AI gateway

```sh
miosa host register studio --platform macos          # prints the host key once
miosa host list
miosa host app catalog
miosa cloud account new prod --provider aws          # bring your own cloud (private preview)
miosa cloud pool provision <pool> --count 1          # needs host provisioning activated for you
miosa gateway policy new fast --primary-model claude-haiku --alias fast
miosa gateway budget show
```

## Config file

`~/.miosa/config.toml` (mode 0600).
Read and change settings with `miosa config list|get|set|path`; the key is never printed.

```toml
api_url           = "https://api.miosa.ai/api/v1"
api_key           = "msk_u_..."
default_workspace = "default"
current_sandbox   = "my-box"
```

## Commands

Anywhere a sandbox is expected you may give its name, its id, `current` (set with `miosa use`) or `self` (inside a sandbox).

| Group | Commands |
|-------|----------|
| Account | `login`, `logout`, `whoami`, `org`, `limits`, `profile`, `config`, `doctor`, `open`, `update`, `version` |
| Lifecycle | `create`, `list`, `info`, `use`, `destroy`, `stop`, `pause`, `resume`, `fork`, `recover`, `extend`, `tag`, `wait`, `usage`, `computer` |
| Work inside | `exec`, `ssh`, `console`, `files`, `process` (`ps`), `services`, `logs`, `events`, `ports`, `preview`, `url`, `proxy`, `desktop` |
| State | `snapshot`, `checkpoint`, `restore`, `env`, `policy` |
| Agents | `prompt`, `run`, `chat`, `agent`, `connections` |
| Platform | `deploy`, `batch`, `host`, `db`, `storage`, `volume`, `workflow`, `schedule`, `function`, `template`, `project`, `workspace`, `forge`, `catalog`, `regions`, `egress`, `cloud`, `gateway` |
| Access and billing | `api-key`, `service-account`, `webhook`, `member`, `billing`, `audit`, `alerts`, `notifications` |
| Raw | `api <METHOD> <path>` with `-d` (`@file` or `-`) and `-H` |

### Agents

BYOK: agent runs use your own model-provider connections, never MIOSA platform keys.
Mint a key that can do this with `miosa api-key create NAME --preset cli`.
Add a connection with `miosa connections add models` (the key is read from a prompt or stdin, never from an argument).
A run without a usable connection fails with a message that names this command.

```sh
miosa prompt --sandbox my-box "fix the failing test"
miosa prompt --new-machine --harness codex "write a README"
miosa run list
miosa run events <id>
miosa run steer --sandbox my-box "also add tests"   # queue the next turn for the running agent
miosa run interrupt --sandbox my-box                # stop the agent attached to a machine
miosa chat list
miosa chat context <chat-id>                        # how full the context is
miosa chat compact <chat-id>                        # compact it so the chat can go on
miosa agent list
```

### Terminal and SSH

`miosa console` uses the miosa-terminal-v1 WebSocket relay with a real PTY, resize handling and the remote exit code.

`miosa ssh my-box` runs the system `ssh` through the authenticated tunnel with a one-time key and a short-lived certificate (15 minutes, valid for that sandbox only).
Where the server has no certificate authority it authorizes the one-time key in the sandbox instead.
The key, certificate and host file live in a private temporary directory removed when ssh ends.
Standard input is piped through, so `miosa ssh my-box -- bash -s < setup.sh` works; `--api` runs over the MIOSA API instead, and so does a machine without `ssh`.
`ssh user@host` and `scp` against a sandbox directly do not work: there is no public SSH endpoint.
`miosa proxy my-box 5432:5432` forwards local ports to sandbox ports over a WebSocket tunnel (any TCP service; the key needs `sandboxes:exec`).

## Environments

An environment is the template a new sandbox or computer starts from.
It decides which repositories are cloned, which variables and secret files are injected, and which of your credentials the machine may use.
Every workspace has one default environment named `base`.
A machine without `--env` uses the default of its workspace.

```sh
miosa env list
miosa env new staging
miosa env set --env staging STRIPE_KEY            # value is asked for, input hidden
miosa env set --env staging --from-file .env
miosa env file put --env staging hello-world/backend/.env ./backend.env
miosa env repo add --env staging octocat/hello-world --branch develop --setup-file ./repo-setup.sh --blocking
miosa env toggle --env staging github off
miosa env protect --env staging on
miosa create my-box --env staging
```

Commands that edit one environment take `--env <name>`, and edit the default environment when it is omitted.
`--workspace <slug>` scopes any `miosa env` command to one workspace.
Without it the API uses the workspace of a workspace-bound key, and the organization level otherwise.

`miosa env info <name>` masks variable values and never prints secret file contents.
Pass `--reveal` to print variable values; each reveal is written to the audit log.
A value is never accepted as an argument, because arguments end up in shell history: give the `NAME` and enter the value when asked, pipe it on stdin, or load a `.env` file with `--from-file`.

The four toggles are `github`, `secrets`, `machine-key` and `agent-connections`.
`protect on` passes nothing of yours, whatever the toggles say.
`miosa create --no-env` gives the same guarantee to one sandbox.

### Versions and upgrades

Every save that changes what a machine receives creates a new immutable version.
A machine pins the latest version when it starts and keeps it.
Saving never reaches into a running machine.

```sh
miosa env versions staging     # how many machines sit on each version
miosa info my-box              # shows "staging v1", and whether an upgrade is available
miosa env upgrade my-box       # move one machine to the latest version
miosa env upgrade --all staging
```

An upgrade is not reversible on the machine's disk.
A secret the new version withholds is deleted from the machine, so `miosa env upgrade` asks you to confirm unless you pass `--force`.

## Setup scripts and running commands

`--setup-file ./setup.sh` runs a script (UTF-8, at most 64KB) once, in the background, after the machine is ready.
It never delays `ready`.
Watch it with `miosa info`: the setup status is `pending`, `running`, `done` or `failed`, and a failed run shows its error.
The CLI checks the size and encoding before it sends anything.

```sh
miosa create my-box --env staging --setup-file ./setup.sh
miosa info my-box
```

A repository can carry its own setup script with `miosa env repo add ... --setup-file`.
Mark it `--blocking` when the machine is not usable until it finishes.

To run commands yourself:

```sh
miosa exec my-box --cwd my-repo --timeout 120 -- npm run build
miosa ssh my-box -- bash -s < ./setup.sh
```

`--cwd` is relative to the sandbox home directory.
The global `--timeout` also bounds the command, from 1 to 300 seconds.
`miosa exec` and `miosa ssh` exit with the remote command's exit code.
A sandbox that is still starting refuses commands, so both retry with backoff for up to 90 seconds.

`miosa ssh` runs the command over the MIOSA API and pipes your stdin to it, up to 80KB.
There is no interactive login shell yet.
With several words after `--`, each word is quoted, so `-- bash -lc "cd app && npm test"` reaches the sandbox as written.
A single word after `--` is a shell string.

## Product lanes

Use the CLI for the sandbox-first developer loop:

```sh
miosa create web-build
miosa files mkdir web-build:/workspace/app
miosa exec web-build -- npm create vite@latest /workspace/app -- --template react
miosa exec web-build -- npm --prefix /workspace/app run dev -- --host 0.0.0.0
miosa url web-build
```

Before choosing a template or size, ask the canonical catalog:

```sh
miosa catalog --product sandbox --template nextjs
miosa catalog --state fast_ready
miosa catalog --output json | jq '.data[] | select(.state == "fast_ready")'
```

For capabilities that are broader than the current high-level CLI commands,
use `miosa api` with the same authenticated config:

| Need | Use |
|---|---|
| Code/build/test/runtime workspace | `miosa create`, `miosa exec`, `miosa files`, `miosa services`, `miosa url` |
| Full GUI/browser computer | `miosa api /computers ...` or an SDK (`miosa.computers`) |
| Docker Deploy appliance | SDK/API Docker Deploy endpoints; publish from a sandbox to Docker Deploy |
| Product/template readiness | `miosa catalog` |

## Snapshots

`miosa snapshot` works on sandboxes and computers alike.
Every snapshot of a machine is its history.
A named snapshot is a restore point you pin on purpose: it outlives the machine and never appears in history.
Each organization keeps 10 named snapshots free.

| Command | Description |
|---------|-------------|
| `miosa snapshot list [machine]` | History across machines, or for one; `--search`, `--workspace` |
| `miosa snapshot named` | Named snapshots and the free allowance |
| `miosa snapshot save <machine> <name>` | Pin a machine (running: captured now, stopped: its newest snapshot) under a name |
| `miosa snapshot save --snapshot <id> <name>` | Pin one existing snapshot |
| `miosa snapshot rm <name>` | Remove a name |
| `miosa snapshot latest <machine>` | Newest snapshot of a machine |
| `miosa snapshot info <id>` | One snapshot and what depends on it |
| `miosa snapshot tree <id> [path]` | List files inside a snapshot, machine stopped or gone |
| `miosa snapshot pull <id> <path> -d ./out` | Download a file, or a folder (extracted from a tar), without overwriting |
| `miosa snapshot fork <id>` | New machine from a snapshot |
| `miosa snapshot deploy <name>` | New machine from a named snapshot |
| `miosa snapshot delete <id>... \| --all <machine>` | Delete what you name; anything still depended on is skipped and reported |

Deleting needs `--yes` when there is no terminal.
Use `--computer` when the machine argument is a computer.
See `docs/api/snapshots.md` in the compute repository for the HTTP API.

## Global flags

```
--api-key string      API key (overrides MIOSA_API_KEY and config)
--api-url string      API base URL (overrides MIOSA_BASE_URL and config)
--profile string      Config profile to use (MIOSA_PROFILE)
--org string          Organization to bill for this command (MIOSA_ORG)
--json                Machine-readable JSON (same as -o json)
-o, --output string   text (default) or json (MIOSA_OUTPUT)
-q, --quiet           Suppress informational output
--retries int         Retries on 429 and transient 5xx (default 3, MIOSA_RETRIES)
--timeout int         Request timeout in seconds; for exec and ssh the command timeout
--no-update-notice    Silence the new-version notice
```

Environment: `MIOSA_API_KEY`, `MIOSA_BASE_URL`, `MIOSA_PROFILE`, `MIOSA_ORG`, `MIOSA_OUTPUT`, `MIOSA_RETRIES`, `MIOSA_NO_UPDATE_NOTICE`.

## Exit codes

| Code | Meaning |
|------|---------|
| 0 | Success |
| 1 | Error |
| 2 | Usage |
| 3 | Not signed in, or forbidden |
| 4 | Not found |
| 5 | Conflict or invalid input |
| 6 | Rate limited (after retries) |
| 7 | Server or network error |

`exec` and `ssh` exit with the remote command's status.

## Scripting

`--json` (or `-o json`, or `MIOSA_OUTPUT=json`) prints machine-readable JSON on stdout.
Commands that stream print one JSON object per line.
Output is never switched to JSON automatically when piped.
Errors go to stderr with the API error code; with `--json` they are an `{"error":{...}}` object.
Destructive commands need `--yes` when there is no terminal.

```sh
miosa list --json | jq -r '.data[].name'
miosa create ci-box --wait --json | jq -r .id
miosa api GET /sandboxes
miosa api POST /webhooks -d @hook.json
```

Forge repository commands always return a `{ "data": ... }` success envelope.
Repository mutations accept `--idempotency-key`, and delete requires an interactive confirmation or `--yes`.
Forge clone is not exposed until MIOSA provides a credential-helper flow that does not place credentials in URLs, process arguments, shell history, logs, or persistent Git configuration.

## Building

```sh
make build        # current platform -> dist/miosa
make build-all    # darwin/linux x amd64/arm64
make test         # unit tests with -race
make lint         # go vet + staticcheck
make docs         # regenerate docs/commands.md and docs/api-map.md
make release-dry-run TAG=v2.0.0   # build the release archives locally, publish nothing
```

The SDK in `internal/miosa-sdk` is a separate module: `cd internal/miosa-sdk && go test ./...`.

## Distribution

The public CLI is distributed as a native `miosa` binary through GitHub Releases, Homebrew (cask), and the install script.
GoReleaser builds Linux and macOS archives for `amd64` and `arm64`.
It is not distributed through npm.
Python package distribution is reserved for the Python SDK (`pip install miosa`), not the CLI.
