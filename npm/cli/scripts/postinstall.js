"use strict";

// Fallback for installs where the platform package did not arrive
// (--omit=optional, a mirror without the @miosa/cli-* packages, ...).
// Downloads the release archive from GitHub, verifies its SHA-256 against the
// release's checksums.txt, and stores the binary in ./vendor.
// A no-op whenever the platform package is present.
const fs = require("node:fs");
const path = require("node:path");
const { resolveBinary, FALLBACK_DIR } = require("../lib/resolve");
const { platformFor, platformKey, archiveName, releaseBaseUrl, githubReleaseBaseUrl } = require("../lib/platforms");
const { sha256Hex, verifyChecksum } = require("../lib/checksums");
const { extractBinary } = require("../lib/archive");

const MAX_BYTES = 200 << 20;

async function fetchBuffer(url, fetchImpl) {
  const res = await fetchImpl(url, { redirect: "follow", headers: { "user-agent": "miosa-cli-npm-postinstall" } });
  if (!res.ok) throw new Error(`GET ${url}: ${res.status} ${res.statusText}`);
  const buf = Buffer.from(await res.arrayBuffer());
  if (buf.length > MAX_BYTES) throw new Error(`GET ${url}: response too large`);
  return buf;
}

// embeddedSum returns the archive digest shipped inside this npm package by
// build-packages.cjs, if present. It lets the download be verified without
// trusting a checksums.txt fetched from the same origin as the archive.
function embeddedSum(name) {
  const file = path.join(__dirname, "..", "checksums.json");
  if (!fs.existsSync(file)) return null;
  try {
    const all = JSON.parse(fs.readFileSync(file, "utf8"));
    return all[name] || null;
  } catch {
    return null;
  }
}

async function installFrom(base, { version, entry, name, fetchImpl, vendorDir }) {
  const archive = await fetchBuffer(`${base}/${name}`, fetchImpl);
  const pinned = embeddedSum(name);
  if (pinned) {
    const got = sha256Hex(archive);
    if (got !== pinned) {
      throw new Error(`checksum mismatch for ${name}: expected ${pinned}, got ${got}`);
    }
  } else {
    const sums = (await fetchBuffer(`${base}/checksums.txt`, fetchImpl)).toString("utf8");
    verifyChecksum(archive, name, sums);
  }
  const bin = extractBinary(archive, entry.ext, entry.bin);
  fs.mkdirSync(vendorDir, { recursive: true });
  const dest = path.join(vendorDir, entry.bin);
  fs.writeFileSync(dest, bin, { mode: 0o755 });
  return dest;
}

async function install({ version, platform, arch, fetchImpl = fetch, baseUrl, vendorDir = FALLBACK_DIR }) {
  const entry = platformFor(platform, arch);
  if (!entry) throw new Error(`no prebuilt binary for ${platformKey(platform, arch)}`);
  const name = archiveName(version, entry);

  // An explicit base (env or argument) is used alone so tests and mirrors are
  // deterministic. Otherwise try the MIOSA origin first and GitHub as the
  // fallback while the migration is rolling out.
  const explicit = baseUrl || process.env.MIOSA_CLI_DOWNLOAD_BASE;
  const bases = explicit ? [explicit] : [...new Set([releaseBaseUrl(version), githubReleaseBaseUrl(version)])];

  let lastErr;
  for (const base of bases) {
    try {
      return await installFrom(base, { version, entry, name, fetchImpl, vendorDir });
    } catch (err) {
      lastErr = err;
    }
  }
  throw lastErr;
}

async function main() {
  const platform = process.platform;
  const arch = process.arch;
  if (!platformFor(platform, arch)) {
    console.warn(`@miosa/cli: no prebuilt binary for ${platformKey(platform, arch)}; see https://miosa.ai/docs/cli`);
    return 0;
  }
  try {
    resolveBinary({ platform, arch });
    return 0; // platform package (or an earlier download) is already in place
  } catch {
    // fall through to the download
  }
  const { version } = require("../package.json");
  console.log(`@miosa/cli: platform package missing, downloading miosa ${version}`);
  try {
    const dest = await install({ version, platform, arch });
    console.log(`@miosa/cli: installed ${dest} (checksum verified)`);
    return 0;
  } catch (err) {
    console.error(`@miosa/cli: could not install the native binary: ${err.message}`);
    return 1;
  }
}

module.exports = { install };

if (require.main === module) {
  main().then((code) => process.exit(code));
}
