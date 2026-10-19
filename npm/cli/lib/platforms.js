"use strict";

// Single source of truth for which platforms ship a native binary.
// `goos`/`goarch` are the names used in the miosa-cli-go release archives.
const RELEASE_REPO = "Miosa-osa/miosa-cli-go";

// The product download origin. GitHub Releases stay as a mirror and as the
// fallback while the MIOSA origin is rolled out.
const MIOSA_RELEASE_BASE = "https://dl.miosa.ai/cli";

// Rollout switch. Keep "github" until the captain's acceptance curls for
// dl.miosa.ai pass, then change this to "miosa". releaseBaseUrl() is the
// primary origin; githubReleaseBaseUrl() is always the fallback.
const DEFAULT_DOWNLOAD_ORIGIN = "github";

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

function miosaReleaseBaseUrl(version) {
  return `${MIOSA_RELEASE_BASE}/v${version}`;
}

function githubReleaseBaseUrl(version, repo = RELEASE_REPO) {
  return `https://github.com/${repo}/releases/download/v${version}`;
}

function releaseBaseUrl(version) {
  return DEFAULT_DOWNLOAD_ORIGIN === "miosa" ? miosaReleaseBaseUrl(version) : githubReleaseBaseUrl(version);
}

module.exports = { RELEASE_REPO, MIOSA_RELEASE_BASE, DEFAULT_DOWNLOAD_ORIGIN, PLATFORMS, platformKey, platformFor, archiveName, releaseBaseUrl, miosaReleaseBaseUrl, githubReleaseBaseUrl };
