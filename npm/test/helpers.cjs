"use strict";

const fs = require("node:fs");
const os = require("node:os");
const path = require("node:path");
const { execFileSync } = require("node:child_process");
const { sha256Hex } = require("../cli/lib/checksums");

// Builds a fake release directory: one tar.gz (and, for win32, one real zip)
// per platform containing an executable named `miosa`, plus checksums.txt.
function fakeRelease(version, entries, { corrupt = false } = {}) {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), "miosa-release-"));
  const lines = [];
  for (const entry of entries) {
    const stage = fs.mkdtempSync(path.join(os.tmpdir(), "miosa-stage-"));
    fs.writeFileSync(path.join(stage, entry.bin), fakeBinary(version, entry), { mode: 0o755 });
    fs.writeFileSync(path.join(stage, "README.md"), "docs");
    const name = `miosa_${version}_${entry.goos}_${entry.goarch}.${entry.ext}`;
    const file = path.join(dir, name);
    if (entry.ext === "zip") {
      // A real zip, so the same code path runs on macOS and Linux.
      fs.writeFileSync(
        file,
        makeZip([
          [entry.bin, fakeBinary(version, entry)],
          ["README.md", "docs"],
        ]),
      );
    } else {
      execFileSync("tar", ["-czf", file, "-C", stage, entry.bin, "README.md"]);
    }
    const sum = corrupt ? "0".repeat(64) : sha256Hex(fs.readFileSync(file));
    lines.push(`${sum}  ${name}`);
    fs.rmSync(stage, { recursive: true, force: true });
  }
  fs.writeFileSync(path.join(dir, "checksums.txt"), lines.join("\n") + "\n");
  return dir;
}

function fakeBinary(version, entry) {
  return `#!/bin/sh\necho "fake miosa ${version} ${entry.goos}/${entry.goarch}"\n`;
}

// ─── a minimal zip writer (stored entries), for tests only ───────────────────

const CRC_TABLE = (() => {
  const table = new Int32Array(256);
  for (let n = 0; n < 256; n++) {
    let c = n;
    for (let k = 0; k < 8; k++) c = c & 1 ? 0xedb88320 ^ (c >>> 1) : c >>> 1;
    table[n] = c;
  }
  return table;
})();

function crc32(buf) {
  let c = 0xffffffff;
  for (let i = 0; i < buf.length; i++) c = CRC_TABLE[(c ^ buf[i]) & 0xff] ^ (c >>> 8);
  return (c ^ 0xffffffff) >>> 0;
}

function makeZip(files) {
  const parts = [];
  const central = [];
  let offset = 0;
  for (const [name, content] of files) {
    const data = Buffer.isBuffer(content) ? content : Buffer.from(content);
    const nameBuf = Buffer.from(name, "utf8");
    const crc = crc32(data);

    const local = Buffer.alloc(30 + nameBuf.length);
    local.writeUInt32LE(0x04034b50, 0);
    local.writeUInt16LE(20, 4);
    local.writeUInt32LE(crc, 14);
    local.writeUInt32LE(data.length, 18);
    local.writeUInt32LE(data.length, 22);
    local.writeUInt16LE(nameBuf.length, 26);
    nameBuf.copy(local, 30);

    const entry = Buffer.alloc(46 + nameBuf.length);
    entry.writeUInt32LE(0x02014b50, 0);
    entry.writeUInt16LE(20, 4);
    entry.writeUInt16LE(20, 6);
    entry.writeUInt32LE(crc, 16);
    entry.writeUInt32LE(data.length, 20);
    entry.writeUInt32LE(data.length, 24);
    entry.writeUInt16LE(nameBuf.length, 28);
    entry.writeUInt32LE(offset, 42);
    nameBuf.copy(entry, 46);

    parts.push(local, data);
    central.push(entry);
    offset += local.length + data.length;
  }

  const centralBuf = Buffer.concat(central);
  const eocd = Buffer.alloc(22);
  eocd.writeUInt32LE(0x06054b50, 0);
  eocd.writeUInt16LE(files.length, 8);
  eocd.writeUInt16LE(files.length, 10);
  eocd.writeUInt32LE(centralBuf.length, 12);
  eocd.writeUInt32LE(offset, 16);
  return Buffer.concat([...parts, centralBuf, eocd]);
}

function tmpDir() {
  return fs.mkdtempSync(path.join(os.tmpdir(), "miosa-test-"));
}

module.exports = { fakeRelease, tmpDir, makeZip };
