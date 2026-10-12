#!/usr/bin/env node
"use strict";

// Builds the npm packages for the native miosa CLI from a miosa-cli-go release.
//
//   node scripts/build-packages.cjs --version 2.0.0 [--out dist]
//        [--platforms darwin-arm64,linux-x64,...] [--allow-missing]
//        [--assets-dir DIR | --base-url URL]
//
// For each platform it downloads miosa_<version>_<os>_<arch>.<ext> and
// checksums.txt, verifies the SHA-256, and lays out one @miosa/cli-<platform>
// package. It also writes the @miosa/cli package, pinned to exactly the
// platform packages that were built.
const fs = require("node:fs");
const path = require("node:path");
const { PLATFORMS, archiveName, releaseBaseUrl } = require("../cli/lib/platforms");
const { verifyChecksum } = require("../cli/lib/checksums");
const { extractBinary } = require("../cli/lib/archive");

const CLI_DIR = path.join(__dirname, "..", "cli");

function dirAssets(dir) {
  return async (name) => {
    const file = path.join(dir, name);
    return fs.existsSync(file) ? fs.readFileSync(file) : null;
  };
}

function urlAssets(base) {
  return async (name) => {
    const res = await fetch(`${base}/${name}`, { redirect: "follow" });
    if (res.status === 404) return null;
    if (!res.ok) throw new Error(`GET ${base}/${name}: ${res.status} ${res.statusText}`);
    return Buffer.from(await res.arrayBuffer());
  };
}

function copyDir(src, dest) {
  fs.mkdirSync(dest, { recursive: true });
  for (const e of fs.readdirSync(src, { withFileTypes: true })) {
    if (e.name === "vendor" || e.name === "node_modules") continue;
    const s = path.join(src, e.name);
    const d = path.join(dest, e.name);
    if (e.isDirectory()) copyDir(s, d);
    else fs.copyFileSync(s, d);
  }
}

function platformManifest(key, entry, version) {
  return {
    name: entry.pkg,
    version,
    description: `The MIOSA CLI native binary for ${key}. Install @miosa/cli instead of this package.`,
    os: [entry.os],
    cpu: [entry.cpu],
    files: ["bin", "README.md"],
    license: "MIT",
    homepage: "https://docs.miosa.ai/cli",
    repository: { type: "git", url: "git+https://github.com/Miosa-osa/miosa.git", directory: "sdks/cli/npm" },
    publishConfig: { access: "public" },
  };
}

async function buildPackages({ version, outDir, platforms, getAsset, allowMissing = false, log = () => {} }) {
  const source = JSON.parse(fs.readFileSync(path.join(CLI_DIR, "package.json"), "utf8"));
  if (source.version !== version) {
    throw new Error(`sdks/cli/npm/cli/package.json is ${source.version} but --version is ${version}; bump the manifest first`);
  }
  const unknown = platforms.filter((k) => !PLATFORMS[k]);
  if (unknown.length) throw new Error(`unknown platform(s): ${unknown.join(", ")}`);

  const sumsBuf = await getAsset("checksums.txt");
  if (!sumsBuf) throw new Error(`release v${version} has no checksums.txt; refusing to build unverified packages`);
  const sums = sumsBuf.toString("utf8");

  fs.rmSync(outDir, { recursive: true, force: true });
  fs.mkdirSync(outDir, { recursive: true });

  const built = [];
  for (const key of platforms) {
    const entry = PLATFORMS[key];
    const name = archiveName(version, entry);
    const archive = await getAsset(name);
    if (!archive) {
      if (allowMissing) {
        log(`skip ${key}: release has no ${name}`);
        continue;
      }
      throw new Error(`release v${version} has no ${name} (use --platforms or --allow-missing to skip ${key})`);
    }
    verifyChecksum(archive, name, sums);
    const bin = extractBinary(archive, entry.ext, entry.bin);
    const dir = path.join(outDir, key);
    fs.mkdirSync(path.join(dir, "bin"), { recursive: true });
    fs.writeFileSync(path.join(dir, "bin", entry.bin), bin, { mode: 0o755 });
    fs.writeFileSync(path.join(dir, "package.json"), JSON.stringify(platformManifest(key, entry, version), null, 2) + "\n");
    fs.writeFileSync(
      path.join(dir, "README.md"),
      `# ${entry.pkg}\n\nNative miosa ${version} binary for ${key}.\nThis package is installed automatically by \`@miosa/cli\`; do not depend on it directly.\n`,
    );
    log(`built ${entry.pkg}@${version} (sha256 verified)`);
    built.push(key);
  }
  if (built.length === 0) throw new Error("no platform packages were built");

  const cliOut = path.join(outDir, "cli");
  copyDir(CLI_DIR, cliOut);
  const manifest = { ...source, optionalDependencies: {} };
  for (const key of built.slice().sort()) manifest.optionalDependencies[PLATFORMS[key].pkg] = version;
  fs.writeFileSync(path.join(cliOut, "package.json"), JSON.stringify(manifest, null, 2) + "\n");
  log(`built @miosa/cli@${version} with optionalDependencies on ${built.length} platform package(s)`);
  return { built, outDir };
}

function parseArgs(argv) {
  const args = { platforms: Object.keys(PLATFORMS), out: path.join(__dirname, "..", "dist"), allowMissing: false };
  for (let i = 0; i < argv.length; i++) {
    const a = argv[i];
    const next = () => {
      if (i + 1 >= argv.length) throw new Error(`${a} needs a value`);
      return argv[++i];
    };
    if (a === "--version") args.version = next().replace(/^v/, "");
    else if (a === "--out") args.out = path.resolve(next());
    else if (a === "--platforms") args.platforms = next().split(",").map((s) => s.trim()).filter(Boolean);
    else if (a === "--assets-dir") args.assetsDir = path.resolve(next());
    else if (a === "--base-url") args.baseUrl = next().replace(/\/$/, "");
    else if (a === "--allow-missing") args.allowMissing = true;
    else throw new Error(`unknown argument: ${a}`);
  }
  if (!args.version) throw new Error("--version is required (for example --version 2.0.0)");
  return args;
}

async function main() {
  const args = parseArgs(process.argv.slice(2));
  const getAsset = args.assetsDir ? dirAssets(args.assetsDir) : urlAssets(args.baseUrl || releaseBaseUrl(args.version));
  await buildPackages({
    version: args.version,
    outDir: args.out,
    platforms: args.platforms,
    getAsset,
    allowMissing: args.allowMissing,
    log: (m) => console.log(m),
  });
  console.log(`packages are in ${args.out}`);
}

module.exports = { buildPackages, parseArgs, dirAssets };

if (require.main === module) {
  main().catch((err) => {
    console.error(`build-packages: ${err.message}`);
    process.exit(1);
  });
}
