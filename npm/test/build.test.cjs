"use strict";

const test = require("node:test");
const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const { buildPackages, dirAssets, parseArgs } = require("../scripts/build-packages.cjs");
const { install } = require("../cli/scripts/postinstall.js");
const { PLATFORMS } = require("../cli/lib/platforms");
const { fakeRelease, tmpDir } = require("./helpers.cjs");

const VERSION = require("../cli/package.json").version;
const UNIX = ["darwin-arm64", "darwin-x64", "linux-arm64", "linux-x64"];

test("source manifest pins every platform package to its own version", () => {
  const pkg = require("../cli/package.json");
  assert.deepEqual(Object.keys(pkg.optionalDependencies).sort(), Object.values(PLATFORMS).map((p) => p.pkg).sort());
  for (const v of Object.values(pkg.optionalDependencies)) assert.equal(v, pkg.version);
});

test("builds verified platform packages and a pinned @miosa/cli", async () => {
  const release = fakeRelease(VERSION, UNIX.map((k) => PLATFORMS[k]));
  const out = tmpDir();
  const { built } = await buildPackages({ version: VERSION, outDir: out, platforms: UNIX, getAsset: dirAssets(release) });
  assert.deepEqual(built, UNIX);
  const m = JSON.parse(fs.readFileSync(path.join(out, "darwin-arm64", "package.json"), "utf8"));
  assert.equal(m.name, "@miosa/cli-darwin-arm64");
  assert.deepEqual(m.os, ["darwin"]);
  assert.deepEqual(m.cpu, ["arm64"]);
  const bin = path.join(out, "darwin-arm64", "bin", "miosa");
  assert.equal(fs.statSync(bin).mode & 0o111, 0o111);
  assert.match(fs.readFileSync(bin, "utf8"), /fake miosa/);
  const cli = JSON.parse(fs.readFileSync(path.join(out, "cli", "package.json"), "utf8"));
  assert.deepEqual(Object.keys(cli.optionalDependencies).sort(), UNIX.map((k) => PLATFORMS[k].pkg).sort());
  assert.ok(fs.existsSync(path.join(out, "cli", "bin", "miosa.js")));
});

test("refuses to build when an archive does not match checksums.txt", async () => {
  const release = fakeRelease(VERSION, [PLATFORMS["linux-x64"]], { corrupt: true });
  await assert.rejects(
    buildPackages({ version: VERSION, outDir: tmpDir(), platforms: ["linux-x64"], getAsset: dirAssets(release) }),
    /checksum mismatch/,
  );
});

test("fails on a missing platform archive unless --allow-missing", async () => {
  const release = fakeRelease(VERSION, [PLATFORMS["linux-x64"]]);
  const args = { version: VERSION, outDir: tmpDir(), platforms: ["linux-x64", "win32-x64"], getAsset: dirAssets(release) };
  await assert.rejects(buildPackages(args), /no miosa_.*windows_amd64\.zip/);
  const { built } = await buildPackages({ ...args, allowMissing: true });
  assert.deepEqual(built, ["linux-x64"]);
});

test("builds the Windows package from a real zip (GNU tar cannot read zip)", async () => {
  const release = fakeRelease(VERSION, [PLATFORMS["win32-x64"]]);
  const out = tmpDir();
  const { built } = await buildPackages({ version: VERSION, outDir: out, platforms: ["win32-x64"], getAsset: dirAssets(release) });
  assert.deepEqual(built, ["win32-x64"]);
  const bin = path.join(out, "win32-x64", "bin", "miosa.exe");
  assert.match(fs.readFileSync(bin, "utf8"), /fake miosa/);
  const manifest = JSON.parse(fs.readFileSync(path.join(out, "win32-x64", "package.json"), "utf8"));
  assert.equal(manifest.name, "@miosa/cli-win32-x64");
  assert.deepEqual(manifest.os, ["win32"]);
  const cli = JSON.parse(fs.readFileSync(path.join(out, "cli", "package.json"), "utf8"));
  assert.deepEqual(Object.keys(cli.optionalDependencies), ["@miosa/cli-win32-x64"]);
});

test("builds every published platform against a fake release", async () => {
  const keys = Object.keys(PLATFORMS).sort();
  const release = fakeRelease(VERSION, keys.map((k) => PLATFORMS[k]));
  const out = tmpDir();
  const { built } = await buildPackages({ version: VERSION, outDir: out, platforms: keys, getAsset: dirAssets(release) });
  assert.deepEqual(built.sort(), keys);
  for (const key of keys) {
    const bin = path.join(out, key, "bin", PLATFORMS[key].bin);
    assert.ok(fs.existsSync(bin), `missing ${bin}`);
    assert.match(fs.readFileSync(bin, "utf8"), /fake miosa/);
  }
  const cli = JSON.parse(fs.readFileSync(path.join(out, "cli", "package.json"), "utf8"));
  assert.deepEqual(Object.keys(cli.optionalDependencies).sort(), keys.map((k) => PLATFORMS[k].pkg).sort());
});

test("refuses to build without checksums.txt", async () => {
  const release = fakeRelease(VERSION, [PLATFORMS["linux-x64"]]);
  fs.rmSync(path.join(release, "checksums.txt"));
  await assert.rejects(buildPackages({ version: VERSION, outDir: tmpDir(), platforms: ["linux-x64"], getAsset: dirAssets(release) }), /no checksums\.txt/);
});

test("refuses a --version that differs from the manifest", async () => {
  await assert.rejects(buildPackages({ version: "9.9.9", outDir: tmpDir(), platforms: ["linux-x64"], getAsset: async () => null }), /bump the manifest/);
});

test("parseArgs validates input", () => {
  assert.throws(() => parseArgs([]), /--version is required/);
  assert.throws(() => parseArgs(["--version", "1.0.0", "--bogus"]), /unknown argument/);
  assert.equal(parseArgs(["--version", "v2.0.0"]).version, "2.0.0");
});

function fetchFrom(dir, { tamper = false } = {}) {
  return async (url) => {
    const file = path.join(dir, path.basename(url));
    if (!fs.existsSync(file)) return { ok: false, status: 404, statusText: "Not Found" };
    let data = fs.readFileSync(file);
    if (tamper && !url.endsWith("checksums.txt")) data = Buffer.concat([data, Buffer.from("x")]);
    return { ok: true, status: 200, arrayBuffer: async () => data.buffer.slice(data.byteOffset, data.byteOffset + data.byteLength) };
  };
}

test("postinstall fallback downloads, verifies and installs the binary", async () => {
  const entry = PLATFORMS["linux-x64"];
  const release = fakeRelease(VERSION, [entry]);
  const vendor = tmpDir();
  const dest = await install({ version: VERSION, platform: "linux", arch: "x64", fetchImpl: fetchFrom(release), baseUrl: "https://example.test/dl", vendorDir: vendor });
  assert.equal(dest, path.join(vendor, "miosa"));
  assert.equal(fs.statSync(dest).mode & 0o111, 0o111);
});

test("postinstall fallback refuses a tampered download and installs nothing", async () => {
  const release = fakeRelease(VERSION, [PLATFORMS["linux-x64"]]);
  const vendor = tmpDir();
  await assert.rejects(
    install({ version: VERSION, platform: "linux", arch: "x64", fetchImpl: fetchFrom(release, { tamper: true }), baseUrl: "https://example.test/dl", vendorDir: vendor }),
    /checksum mismatch/,
  );
  assert.deepEqual(fs.readdirSync(vendor), []);
});

test("postinstall fallback surfaces HTTP failures", async () => {
  await assert.rejects(
    install({ version: VERSION, platform: "linux", arch: "x64", fetchImpl: fetchFrom(tmpDir()), baseUrl: "https://example.test/dl", vendorDir: tmpDir() }),
    /404/,
  );
});
