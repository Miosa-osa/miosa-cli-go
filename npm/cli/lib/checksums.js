"use strict";

const crypto = require("node:crypto");

function sha256Hex(buf) {
  return crypto.createHash("sha256").update(buf).digest("hex");
}

// Parses a goreleaser checksums.txt body ("<sha256>  <file>") into a Map.
function parseChecksums(text) {
  const sums = new Map();
  for (const line of String(text).split("\n")) {
    const m = /^([0-9a-fA-F]{64})\s+\*?(\S+)\s*$/.exec(line.trim());
    if (m) sums.set(m[2], m[1].toLowerCase());
  }
  return sums;
}

// Throws unless buf's SHA-256 equals the checksums.txt entry for name.
function verifyChecksum(buf, name, checksumsText) {
  const want = parseChecksums(checksumsText).get(name);
  if (!want) throw new Error(`checksums.txt has no entry for ${name}`);
  const got = sha256Hex(buf);
  if (got !== want) {
    throw new Error(`checksum mismatch for ${name}: expected ${want}, got ${got}`);
  }
}

module.exports = { sha256Hex, parseChecksums, verifyChecksum };
