"use strict";

const test = require("node:test");
const assert = require("node:assert/strict");
const path = require("node:path");
const fs = require("node:fs");
const http = require("node:http");
const { spawnSync, spawn } = require("node:child_process");
const { resolveBinary } = require("../cli/lib/resolve");
const { PLATFORMS } = require("../cli/lib/platforms");
const { tmpDir, fakeRelease } = require("./helpers.cjs");

const SHIM = path.join(__dirname, "..", "cli", "bin", "miosa.js");

function installedPackages(installed) {
  return {
    resolve: (id) => {
      const pkg = id.replace(/\/package\.json$/, "");
      if (installed.includes(pkg)) return `/nm/${pkg}/package.json`;
      const err = new Error(`Cannot find module '${id}'`);
      err.code = "MODULE_NOT_FOUND";
      throw err;
    },
    exists: (p) => p.startsWith("/nm/"),
    fallbackDir: "/nonexistent-vendor",
  };
}

test("resolves the matching platform package for every supported platform", () => {
  for (const [key, entry] of Object.entries(PLATFORMS)) {
    const got = resolveBinary({ platform: entry.os, arch: entry.cpu, deps: installedPackages(Object.values(PLATFORMS).map((p) => p.pkg)) });
    assert.equal(got.source, entry.pkg, key);
    assert.equal(got.path, path.join("/nm", entry.pkg, "bin", entry.bin), key);
  }
});

test("uses only the package for this platform even when others are installed", () => {
  const got = resolveBinary({ platform: "linux", arch: "arm64", deps: installedPackages(["@miosa/cli-linux-x64", "@miosa/cli-linux-arm64"]) });
  assert.equal(got.source, "@miosa/cli-linux-arm64");
});

test("rejects unsupported platforms with a pointer to other install methods", () => {
  assert.throws(() => resolveBinary({ platform: "freebsd", arch: "x64", deps: installedPackages([]) }), /no prebuilt binary for freebsd-x64/);
  assert.throws(() => resolveBinary({ platform: "linux", arch: "ia32", deps: installedPackages([]) }), /no prebuilt binary/);
});

test("falls back to the vendored download when the platform package is absent", () => {
  const vendor = tmpDir();
  fs.writeFileSync(path.join(vendor, "miosa"), "x");
  const got = resolveBinary({ platform: "darwin", arch: "arm64", deps: { ...installedPackages([]), exists: (p) => p.startsWith(vendor), fallbackDir: vendor } });
  assert.equal(got.source, "vendor");
});

test("explains how to recover when no binary can be found", () => {
  assert.throws(() => resolveBinary({ platform: "darwin", arch: "x64", deps: installedPackages([]) }), /@miosa\/cli-darwin-x64.*\n.*optional dependencies/s);
});

test("shim refuses `update` and names the npm command, without running anything", () => {
  for (const cmd of ["update", "upgrade", "self-update"]) {
    const r = spawnSync(process.execPath, [SHIM, cmd], { encoding: "utf8" });
    assert.equal(r.status, 1, cmd);
    assert.match(r.stderr, /npm install -g @miosa\/cli@latest/, cmd);
  }
});

// Copies the shim next to a fake vendored binary so no test touches the network.
function shimWithFakeBinary(script) {
  const entry = PLATFORMS[`${process.platform}-${process.arch}`];
  const cliDir = path.join(tmpDir(), "node_modules", "@miosa", "cli");
  fs.cpSync(path.join(__dirname, "..", "cli"), cliDir, { recursive: true });
  fs.mkdirSync(path.join(cliDir, "vendor"), { recursive: true });
  fs.writeFileSync(path.join(cliDir, "vendor", entry.bin), script, { mode: 0o755 });
  return path.join(cliDir, "bin", "miosa.js");
}

test("shim lets `update --check` through to the binary (read-only)", () => {
  if (process.platform === "win32") return;
  const shim = shimWithFakeBinary('#!/bin/sh\necho "argv=$*"\n');
  const r = spawnSync(process.execPath, [shim, "update", "--check"], { encoding: "utf8" });
  assert.equal(r.stdout.trim(), "argv=update --check");
});

test("shim forwards args, exit code and marks the install as npm-managed", () => {
  const root = tmpDir();
  const entry = PLATFORMS[`${process.platform}-${process.arch}`];
  if (!entry || process.platform === "win32") return;
  const pkgDir = path.join(root, "node_modules", "@miosa", path.basename(entry.pkg));
  fs.mkdirSync(path.join(pkgDir, "bin"), { recursive: true });
  fs.writeFileSync(path.join(pkgDir, "package.json"), JSON.stringify({ name: entry.pkg, version: "0.0.0" }));
  fs.writeFileSync(path.join(pkgDir, "bin", entry.bin), '#!/bin/sh\necho "args=$* via=$MIOSA_INSTALL_METHOD"\nexit 7\n', { mode: 0o755 });
  // copy the shim next to the fake platform package so require.resolve finds it
  const cliDir = path.join(root, "node_modules", "@miosa", "cli");
  fs.cpSync(path.join(__dirname, "..", "cli"), cliDir, { recursive: true });
  const r = spawnSync(process.execPath, [path.join(cliDir, "bin", "miosa.js"), "list", "--json"], { encoding: "utf8" });
  assert.equal(r.status, 7);
  assert.equal(r.stdout.trim(), "args=list --json via=npm");
});

test("shim maps a lone --version to the version command (binaries before 2.0.1)", () => {
  const entry = PLATFORMS[`${process.platform}-${process.arch}`];
  if (!entry || process.platform === "win32") return;
  const root = tmpDir();
  const cliDir = path.join(root, "node_modules", "@miosa", "cli");
  fs.cpSync(path.join(__dirname, "..", "cli"), cliDir, { recursive: true });
  const vendor = path.join(cliDir, "vendor");
  fs.mkdirSync(vendor, { recursive: true });
  fs.writeFileSync(path.join(vendor, entry.bin), '#!/bin/sh\necho "argv=$*"\n', { mode: 0o755 });
  const r = spawnSync(process.execPath, [path.join(cliDir, "bin", "miosa.js"), "--version"], { encoding: "utf8" });
  assert.equal(r.stdout.trim(), "argv=version");
});

test("shim downloads and verifies the binary on first use when install scripts never ran", async () => {
  const entry = PLATFORMS[`${process.platform}-${process.arch}`];
  if (!entry || process.platform === "win32") return;
  const version = require("../cli/package.json").version;
  const release = fakeRelease(version, [entry]);
  const server = http.createServer((req, res) => {
    const file = path.join(release, path.basename(req.url));
    if (!fs.existsSync(file)) return res.writeHead(404).end();
    res.end(fs.readFileSync(file));
  });
  await new Promise((r) => server.listen(0, "127.0.0.1", r));
  try {
    const cliDir = path.join(tmpDir(), "node_modules", "@miosa", "cli");
    fs.cpSync(path.join(__dirname, "..", "cli"), cliDir, { recursive: true });
    const run = () =>
      new Promise((resolve) => {
        const child = spawn(process.execPath, [path.join(cliDir, "bin", "miosa.js"), "hello"], {
          env: { ...process.env, MIOSA_CLI_DOWNLOAD_BASE: `http://127.0.0.1:${server.address().port}` },
        });
        let out = "";
        let err = "";
        child.stdout.on("data", (d) => (out += d));
        child.stderr.on("data", (d) => (err += d));
        child.on("close", (code) => resolve({ code, out, err }));
      });
    const first = await run();
    assert.equal(first.code, 0, first.err);
    assert.match(first.out, /fake miosa/);
    assert.match(first.err, /downloading/);
    assert.ok(fs.existsSync(path.join(cliDir, "vendor", entry.bin)));
  } finally {
    server.close();
  }
});
