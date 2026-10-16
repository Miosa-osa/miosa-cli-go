"use strict";

const fs = require("node:fs");
const path = require("node:path");
const { platformFor, platformKey } = require("./platforms");

const FALLBACK_DIR = path.join(__dirname, "..", "vendor");

// Finds the native binary for this machine. Order:
//   1. the platform package installed through optionalDependencies
//   2. the copy the postinstall fallback downloaded into ./vendor
// `deps` exists so tests can inject module resolution and the file system.
function resolveBinary({ platform, arch, deps = {} } = {}) {
  const resolve = deps.resolve || ((id) => require.resolve(id));
  const exists = deps.exists || fs.existsSync;
  platform = platform || process.platform;
  arch = arch || process.arch;

  const entry = platformFor(platform, arch);
  if (!entry) {
    throw new Error(
      `@miosa/cli has no prebuilt binary for ${platformKey(platform, arch)}. ` +
        "Install the CLI another way: https://miosa.ai/docs/cli",
    );
  }

  try {
    const manifest = resolve(`${entry.pkg}/package.json`);
    const bin = path.join(path.dirname(manifest), "bin", entry.bin);
    if (exists(bin)) return { path: bin, source: entry.pkg, entry };
  } catch (err) {
    if (err && err.code !== "MODULE_NOT_FOUND") throw err;
  }

  const vendored = path.join(deps.fallbackDir || FALLBACK_DIR, entry.bin);
  if (exists(vendored)) return { path: vendored, source: "vendor", entry };

  throw new Error(
    `The miosa binary for ${platformKey(platform, arch)} is missing (expected package ${entry.pkg}).\n` +
      "This usually means optional dependencies were skipped (--no-optional / --omit=optional) " +
      "or scripts were disabled (--ignore-scripts).\n" +
      "Reinstall with: npm install -g @miosa/cli\n" +
      "Or install the native CLI directly: https://miosa.ai/docs/cli",
  );
}

module.exports = { resolveBinary, FALLBACK_DIR };
