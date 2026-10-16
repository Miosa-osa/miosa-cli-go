# @miosa/cli

The official MIOSA command-line tool.
`npm install -g @miosa/cli` installs the same native `miosa` binary as Homebrew and the install script.

```sh
npm install -g @miosa/cli
miosa login
miosa create my-box --wait
```

Supported: macOS (arm64, x64) and Linux (x64, arm64).
Elsewhere, see https://miosa.ai/docs/cli.

## How it works

This package is a small launcher.
The binary ships in one platform package, `@miosa/cli-<os>-<cpu>`, which npm installs through `optionalDependencies`.
If that package is missing (`--omit=optional`, a registry mirror, skipped install scripts), the launcher downloads the same release from github.com/Miosa-osa/miosa-cli-go and verifies its SHA-256 against the release `checksums.txt` before running it.

## Upgrading

```sh
npm install -g @miosa/cli@latest
```

`miosa update` does not replace an npm-managed binary; it prints this command.

## Coming from 1.x

1.x was a separate Node CLI.
2.x is the native CLI, so the command set is the 2.0 one.
Run `miosa --help` to see it, and `miosa <command> --help` for flags.
Sign in again with `miosa login`, or set `MIOSA_API_KEY`.
Node 18 or newer is required to run the launcher.
