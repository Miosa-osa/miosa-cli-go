"use strict";

const fs = require("node:fs");
const os = require("node:os");
const path = require("node:path");
const zlib = require("node:zlib");
const { execFileSync } = require("node:child_process");

// Returns the binary named `binName` from a .tar.gz / .zip archive buffer.
// .tar.gz goes through the system `tar`, present on macOS, Linux and Windows
// 10+. .zip is read here in pure JavaScript: GNU tar on Linux cannot read a
// zip archive, and `unzip` is not installed everywhere.
function extractBinary(archive, ext, binName) {
  if (ext === "zip") return extractFromZip(archive, binName);
  return extractFromTarGz(archive, binName);
}

function extractFromTarGz(archive, binName) {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), "miosa-cli-"));
  try {
    const file = path.join(dir, "archive.tar.gz");
    fs.writeFileSync(file, archive);
    const out = path.join(dir, "out");
    fs.mkdirSync(out);
    execFileSync("tar", ["-xf", file, "-C", out], { stdio: "pipe" });
    const bin = path.join(out, binName);
    if (!fs.existsSync(bin)) throw new Error(`archive has no ${binName}`);
    return fs.readFileSync(bin);
  } finally {
    fs.rmSync(dir, { recursive: true, force: true });
  }
}

// extractFromZip reads one entry out of a zip buffer. The central directory is
// the authority for sizes and offsets, so an entry written with a trailing data
// descriptor still resolves.
function extractFromZip(archive, binName) {
  const entry = findZipEntry(archive, binName);
  if (!entry) throw new Error(`archive has no ${binName}`);
  if (archive.readUInt32LE(entry.offset) !== 0x04034b50) throw new Error("corrupt zip: bad local header");

  const nameLen = archive.readUInt16LE(entry.offset + 26);
  const extraLen = archive.readUInt16LE(entry.offset + 28);
  const start = entry.offset + 30 + nameLen + extraLen;
  const data = archive.subarray(start, start + entry.compressedSize);

  switch (entry.method) {
    case 0: // stored
      return Buffer.from(data);
    case 8: // deflate
      return zlib.inflateRawSync(data);
    default:
      throw new Error(`unsupported zip compression method ${entry.method}`);
  }
}

function findZipEntry(archive, binName) {
  const eocd = findEndOfCentralDirectory(archive);
  if (eocd < 0) throw new Error("not a zip archive");
  const count = archive.readUInt16LE(eocd + 10);
  let p = archive.readUInt32LE(eocd + 16);
  for (let i = 0; i < count; i++) {
    if (p + 46 > archive.length || archive.readUInt32LE(p) !== 0x02014b50) {
      throw new Error("corrupt zip: bad central directory");
    }
    const method = archive.readUInt16LE(p + 10);
    const compressedSize = archive.readUInt32LE(p + 20);
    const nameLen = archive.readUInt16LE(p + 28);
    const extraLen = archive.readUInt16LE(p + 30);
    const commentLen = archive.readUInt16LE(p + 32);
    const offset = archive.readUInt32LE(p + 42);
    const name = archive.toString("utf8", p + 46, p + 46 + nameLen);
    if (path.basename(name) === binName) return { method, compressedSize, offset };
    p += 46 + nameLen + extraLen + commentLen;
  }
  return null;
}

// findEndOfCentralDirectory scans back from the end, allowing for a comment.
function findEndOfCentralDirectory(archive) {
  const min = Math.max(0, archive.length - 22 - 0xffff);
  for (let i = archive.length - 22; i >= min; i--) {
    if (archive.readUInt32LE(i) === 0x06054b50) return i;
  }
  return -1;
}

module.exports = { extractBinary };
