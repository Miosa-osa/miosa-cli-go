"use strict";

// Single source of truth for which platforms ship a native binary.
// `goos`/`goarch` are the names used in the miosa-cli-go release archives.
const RELEASE_REPO = "Miosa-osa/miosa-cli-go";

const PLATFORMS = {
  "darwin-arm64": { pkg: "@miosa/cli-darwin-arm64", os: "darwin", cpu: "arm64", goos: "darwin", goarch: "arm64", ext: "tar.gz", bin: "miosa" },
  "darwin-x64": { pkg: "@miosa/cli-darwin-x64", os: "darwin", cpu: "x64", goos: "darwin", goarch: "amd64", ext: "tar.gz", bin: "miosa" },
  "linux-arm64": { pkg: "@miosa/cli-linux-arm64", os: "linux", cpu: "arm64", goos: "linux", goarch: "arm64", ext: "tar.gz", bin: "miosa" },
  "linux-x64": { pkg: "@miosa/cli-linux-x64", os: "linux", cpu: "x64", goos: "linux", goarch: "amd64", ext: "tar.gz", bin: "miosa" },
  "win32-x64": { pkg: "@miosa/cli-win32-x64", os: "win32", cpu: "x64", goos: "windows", goarch: "amd64", ext: "zip", bin: "miosa.exe" },
};

function platformKey(platform, arch) {
  return `${platform}-${arch}`;
}

function platformFor(platform, arch) {
  return PLATFORMS[platformKey(platform, arch)] || null;
}

function archiveName(version, entry) {
  return `miosa_${version}_${entry.goos}_${entry.goarch}.${entry.ext}`;
}

function releaseBaseUrl(version, repo = RELEASE_REPO) {
  return `https://github.com/${repo}/releases/download/v${version}`;
}

module.exports = { RELEASE_REPO, PLATFORMS, platformKey, platformFor, archiveName, releaseBaseUrl };
