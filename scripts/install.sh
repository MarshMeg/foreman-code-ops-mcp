#!/bin/sh
# Installs the foreman-code-ops binary from GitHub Releases.
#
#   curl -fsSL https://raw.githubusercontent.com/MarshMeg/foreman-code-ops-mcp/main/scripts/install.sh | sh
#
# Environment:
#   FOREMAN_VERSION   version to install, e.g. 0.1.0 (default: the latest release)
#   FOREMAN_BIN_DIR   install directory (default: ~/.local/bin)
set -eu

repo="MarshMeg/foreman-code-ops-mcp"
bin_dir="${FOREMAN_BIN_DIR:-$HOME/.local/bin}"

case "$(uname -s)" in
  Linux) os=linux ;;
  Darwin) os=darwin ;;
  *) echo "foreman-code-ops: unsupported OS $(uname -s); build from source with go install" >&2; exit 1 ;;
esac
case "$(uname -m)" in
  x86_64 | amd64) arch=amd64 ;;
  aarch64 | arm64) arch=arm64 ;;
  *) echo "foreman-code-ops: unsupported architecture $(uname -m)" >&2; exit 1 ;;
esac

version="${FOREMAN_VERSION:-}"
if [ -z "$version" ]; then
  # /releases/latest redirects to /releases/tag/vX.Y.Z.
  url=$(curl -fsSLI -o /dev/null -w '%{url_effective}' "https://github.com/$repo/releases/latest")
  version="${url##*/v}"
  if [ -z "$version" ] || [ "$version" = "$url" ]; then
    echo "foreman-code-ops: could not find the latest release" >&2
    exit 1
  fi
fi
version="${version#v}"

name="foreman-code-ops_${version}_${os}_${arch}"
base="https://github.com/$repo/releases/download/v$version"
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

echo "foreman-code-ops: downloading $name.tar.gz"
curl -fsSL -o "$tmp/$name.tar.gz" "$base/$name.tar.gz"
curl -fsSL -o "$tmp/checksums.txt" "$base/checksums.txt"
(
  cd "$tmp"
  grep " $name.tar.gz\$" checksums.txt > expected.txt || { echo "foreman-code-ops: $name.tar.gz is not in checksums.txt" >&2; exit 1; }
  if command -v sha256sum >/dev/null 2>&1; then sha256sum -c expected.txt >/dev/null; else shasum -a 256 -c expected.txt >/dev/null; fi
)
tar -C "$tmp" -xzf "$tmp/$name.tar.gz"
mkdir -p "$bin_dir"
install -m 0755 "$tmp/$name/foreman-code-ops" "$bin_dir/foreman-code-ops"
echo "foreman-code-ops: installed $("$bin_dir/foreman-code-ops" version) to $bin_dir"
case ":$PATH:" in
  *":$bin_dir:"*) ;;
  *) echo "foreman-code-ops: add $bin_dir to PATH" ;;
esac
