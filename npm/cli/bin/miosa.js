#!/usr/bin/env node
"use strict";

// Thin launcher: finds the native miosa binary for this platform and runs it.
const { spawnSync } = require("node:child_process");
const { resolveBinary } = require("../lib/resolve");
const { platformFor } = require("../lib/platforms");

const UPDATE_COMMANDS = new Set(["update", "upgrade", "self-update"]);
const UPGRADE_HINT = "npm install -g @miosa/cli@latest";

// Binaries older than 2.0.1 do not know they are npm-managed and have no
// --version flag. These two adjustments keep them safe; they are inert for
// newer binaries, which handle both natively.
function adjustArgs(argv) {
  if (argv.length === 1 && argv[0] === "--version") return ["version"];
  return argv;
}

function refusesUpdate(argv) {
  return UPDATE_COMMANDS.has(argv[0]) && !argv.some((a) => a === "--check" || a === "-h" || a === "--help");
}

// The platform package (an optionalDependency) carries the binary. When it did
// not arrive (--omit=optional, a mirror without it) do the checksum-verified
// download once, on first use. There is no install script: npm 11+ warns about
// every package that has one and tells users to rerun with --allow-scripts.
async function locateBinary() {
  try {
    return resolveBinary();
  } catch (err) {
    const { version } = require("../package.json");
    if (!platformFor(process.platform, process.arch)) throw err;
    process.stderr.write("miosa: native binary not installed; downloading it from GitHub Releases (checksum verified)...\n");
    const { install } = require("../scripts/postinstall");
    try {
      await install({ version, platform: process.platform, arch: process.arch });
    } catch (dlErr) {
      throw new Error(`${err.message}\nDownload failed: ${dlErr.message}`);
    }
    return resolveBinary();
  }
}

async function main(argv) {
  if (refusesUpdate(argv)) {
    process.stderr.write(`miosa: this copy was installed with npm; run: ${UPGRADE_HINT}\n`);
    return 1;
  }

  let target;
  try {
    target = await locateBinary();
  } catch (err) {
    process.stderr.write(`miosa: ${err.message}\n`);
    return 1;
  }

  const result = spawnSync(target.path, adjustArgs(argv), {
    stdio: "inherit",
    env: { ...process.env, MIOSA_INSTALL_METHOD: "npm" },
  });
  if (result.error) {
    process.stderr.write(`miosa: cannot run ${target.path}: ${result.error.message}\n`);
    return 1;
  }
  if (result.signal) {
    process.kill(process.pid, result.signal);
    return 1;
  }
  return result.status === null ? 1 : result.status;
}

main(process.argv.slice(2)).then((code) => {
  process.exitCode = code;
});
