"use strict";

const test = require("node:test");
const assert = require("node:assert/strict");
const { parseChecksums, verifyChecksum, sha256Hex } = require("../cli/lib/checksums");

const GOOD = Buffer.from("hello");
const SUMS = `${sha256Hex(GOOD)}  miosa_2.0.0_linux_amd64.tar.gz\n${"a".repeat(64)}  miosa_2.0.0_darwin_arm64.tar.gz\n`;

test("parses goreleaser checksums.txt", () => {
  const m = parseChecksums(SUMS + "\nnot a checksum line\n");
  assert.equal(m.size, 2);
  assert.equal(m.get("miosa_2.0.0_linux_amd64.tar.gz"), sha256Hex(GOOD));
});

test("accepts a matching digest", () => {
  verifyChecksum(GOOD, "miosa_2.0.0_linux_amd64.tar.gz", SUMS);
});

test("rejects a tampered archive", () => {
  assert.throws(() => verifyChecksum(Buffer.from("hellO"), "miosa_2.0.0_linux_amd64.tar.gz", SUMS), /checksum mismatch/);
});

test("rejects an archive missing from checksums.txt", () => {
  assert.throws(() => verifyChecksum(GOOD, "miosa_2.0.0_linux_arm64.tar.gz", SUMS), /no entry/);
});

test("rejects empty checksums", () => {
  assert.throws(() => verifyChecksum(GOOD, "x.tar.gz", ""), /no entry/);
});
