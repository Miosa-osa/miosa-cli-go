# miosa CLI

The official public command-line tool for [MIOSA](https://miosa.ai).

`miosa` is the user-facing CLI for auth, sandboxes, files, previews, deployments, computers, and OpenComputers hosts. Internal OSA tooling such as `osagent` stays behind MIOSA and is not the public installation path.

## Install

**macOS (Homebrew)**
```sh
brew install Miosa-osa/homebrew-tap/miosa
```

**Linux / macOS (install script)**
```sh
curl -fsSL https://install.miosa.ai | sh
```

By default this installs to `~/.local/bin/miosa` for non-root users and `/usr/local/bin/miosa` for root. Override with:

```sh
curl -fsSL https://install.miosa.ai | INSTALL_DIR=/usr/local/bin sh
```

**Manual** — download from [GitHub Releases](https://github.com/Miosa-osa/miosa-cli-go/releases/latest)
and place the binary in a directory on your `$PATH`.

**From source**
```sh
cd sdks/cli
make install   # builds and copies to ~/.local/bin/miosa
```

## Quick start

```sh
miosa login              # paste your msk_u_... key when prompted
miosa create my-box      # provision a sandbox
miosa exec my-box -- echo hello
miosa list               # see all sandboxes
miosa destroy my-box
```

## Authentication

Get an API key at https://miosa.ai/settings/api (format: `msk_u_...`).

Credentials resolve in this order:
1. `--api-key` flag
2. `MIOSA_API_KEY` environment variable
3. `~/.miosa/config.toml`

## Config file

`~/.miosa/config.toml`:
```toml
api_url           = "https://api.miosa.ai/api/v1"
api_key           = "msk_u_..."
default_workspace = "default"
current_sandbox   = "my-box"
```

## Commands

| Command | Description |
|---------|-------------|
| `miosa login` | Authenticate with an API key |
| `miosa logout` | Remove stored credentials |
| `miosa create [name]` | Create a sandbox |
| `miosa list` | List sandboxes |
| `miosa use <name>` | Set the current sandbox |
| `miosa destroy [name]` | Destroy a sandbox |
| `miosa exec [name] -- <cmd>` | Run a command |
| `miosa console [name]` | Interactive shell |
| `miosa proxy [name] <local>:<remote>` | Port forwarding |
| `miosa url [name]` | Print sandbox URL |
| `miosa files cp/ls/cat/rm/mkdir` | File operations |
| `miosa workspace create/list/delete` | Workspace management |
| `miosa checkpoint create/list/info/delete` | Checkpoints |
| `miosa restore [name] <checkpoint-id>` | Restore from checkpoint |
| `miosa services list/create/start/stop/...` | Services |
| `miosa policy show/set` | Network policy |
| `miosa catalog` | Product/template/size readiness |
| `miosa api <path>` | Raw authenticated API request |
| `miosa upgrade` | Upgrade the CLI |
| `miosa version` | Print version |

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

## Global flags

```
--api-key string    API key (overrides env and config)
--api-url string    API base URL
--output / -o       Output format: text (default) or json
--quiet / -q        Suppress informational output
--timeout int       Request timeout in seconds (default 60)
```

## Scripting

Every command supports `--output json` for machine-readable output:

```sh
miosa list --output json | jq '.data[].name'
miosa create my-box --output json | jq '.id'
```

## Building

```sh
make build        # current platform → dist/miosa
make build-all    # darwin/linux × amd64/arm64
make test         # unit tests with -race
make lint         # go vet + staticcheck
```

## Distribution

The public CLI is distributed as a native `miosa` binary through GitHub Releases, Homebrew, and the install script. GoReleaser builds Linux and macOS archives for `amd64` and `arm64`. It is not distributed through npm. Python package distribution is reserved for the Python SDK (`pip install miosa`), not the CLI.
