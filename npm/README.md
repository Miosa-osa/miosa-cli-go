# @miosa/cli npm packaging

Layout:

- `cli/` - the `@miosa/cli` launcher package (published as is, with `optionalDependencies` pinned by the build script).
- `scripts/build-packages.cjs` - builds the `@miosa/cli-<platform>` packages and a pinned `@miosa/cli` from a miosa-cli-go release, verifying every archive against `checksums.txt`.
- `test/` - `npm test` (Node's built-in test runner, no dependencies, so there is no lockfile).

Build locally:

```sh
npm test
node scripts/build-packages.cjs --version 2.1.0 --platforms darwin-arm64 --out /tmp/miosa-npm
```

Release steps are in the pull request that introduced this directory and in `.github/workflows/publish-cli-npm.yml`.
When the Go CLI ships a new version, bump `version` and every `optionalDependencies` entry in `cli/package.json` (a test checks they agree), add `.github/release-notes/sdks/cli-npm/vX.Y.Z.md`, then tag `sdks/cli-npm/vX.Y.Z`.
