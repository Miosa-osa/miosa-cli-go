#!/usr/bin/env sh
set -eu

repo_root="$(CDPATH= cd -- "$(dirname "$0")/.." && pwd)"
test_root="$(mktemp -d)"
trap 'rm -rf "$test_root"' EXIT INT TERM

case "$(uname -s)" in
  Darwin) os=darwin ;;
  Linux) os=linux ;;
  *) exit 0 ;;
esac

case "$(uname -m)" in
  x86_64 | amd64) arch=amd64 ;;
  arm64 | aarch64) arch=arm64 ;;
  *) exit 0 ;;
esac

version=1.2.2
asset="miosa_${version}_${os}_${arch}.tar.gz"
mkdir -p "$test_root/archive" "$test_root/bin" "$test_root/install"
printf '#!/usr/bin/env sh\nprintf "miosa %%s\\n" "1.2.2"\n' >"$test_root/archive/miosa"
chmod +x "$test_root/archive/miosa"
tar -czf "$test_root/$asset" -C "$test_root/archive" miosa

if command -v sha256sum >/dev/null 2>&1; then
  checksum="$(sha256sum "$test_root/$asset" | awk '{print $1}')"
else
  checksum="$(shasum -a 256 "$test_root/$asset" | awk '{print $1}')"
fi
printf '%s  %s\n' "$checksum" "$asset" >"$test_root/checksums.txt"

cat >"$test_root/bin/curl" <<'MOCK_CURL'
#!/usr/bin/env sh
set -eu
url=''
out=''
while [ "$#" -gt 0 ]; do
  case "$1" in
    -o) out="$2"; shift 2 ;;
    -*) shift ;;
    *) url="$1"; shift ;;
  esac
done
printf '%s\n' "$url" >>"$INSTALL_TEST_LOG"
case "$url" in
  */checksums.txt) cp "$INSTALL_TEST_CHECKSUMS" "$out" ;;
  */miosa_*.tar.gz) cp "$INSTALL_TEST_ARCHIVE" "$out" ;;
  *) exit 64 ;;
esac
MOCK_CURL
chmod +x "$test_root/bin/curl"

PATH="$test_root/bin:$PATH" \
INSTALL_TEST_LOG="$test_root/urls.log" \
INSTALL_TEST_ARCHIVE="$test_root/$asset" \
INSTALL_TEST_CHECKSUMS="$test_root/checksums.txt" \
INSTALL_DIR="$test_root/install" \
MIOSA_CLI_VERSION="$version" \
sh "$repo_root/install.sh"

grep -Fq "/releases/download/v${version}/${asset}" "$test_root/urls.log"
grep -Fq "/releases/download/v${version}/checksums.txt" "$test_root/urls.log"
"$test_root/install/miosa" version | grep -Fq "miosa 1.2.2"

printf 'install script tests passed\n'
