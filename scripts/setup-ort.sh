#!/usr/bin/env bash
# Downloads the prebuilt ONNX Runtime shared library for macOS/Linux
# (the Unix counterpart of setup-ort.ps1).
# Output: third_party/onnxruntime/lib/libonnxruntime.{so,dylib}*
#
# The Go binding (github.com/yalue/onnxruntime_go) loads this library at
# runtime; internal/infer resolves it via MARRAW_ORT_LIB, the exe dir, or
# this path.
set -euo pipefail

VERSION="${ORT_VERSION:-1.27.1}"
root="$(cd "$(dirname "$0")/.." && pwd)"
third="$root/third_party"
out_dir="$third/onnxruntime"

case "$(uname -s)-$(uname -m)" in
    Linux-x86_64)  plat="linux-x64" ;;
    Darwin-arm64)  plat="osx-arm64" ;;
    Darwin-x86_64) plat="osx-x86_64" ;;
    *) echo "unsupported platform: $(uname -s)-$(uname -m)" >&2; exit 1 ;;
esac

force=0
[ "${1:-}" = "--force" ] && force=1
if compgen -G "$out_dir/lib/libonnxruntime.*" > /dev/null && [ "$force" -eq 0 ]; then
    echo "libonnxruntime already present in $out_dir/lib (use --force to re-download)"
    exit 0
fi

mkdir -p "$third"

name="onnxruntime-$plat-$VERSION"
tarball="$third/$name.tgz"
if [ ! -f "$tarball" ]; then
    url="https://github.com/microsoft/onnxruntime/releases/download/v$VERSION/$name.tgz"
    echo "Downloading $url"
    curl -fL --retry 3 -o "$tarball" "$url"
fi

extract="$third/$name-extract"
rm -rf "$extract"
mkdir -p "$extract"
tar -xzf "$tarball" -C "$extract"

rm -rf "$out_dir"
mkdir -p "$out_dir/lib"
cp -a "$extract/$name/lib/"libonnxruntime.* "$out_dir/lib/"
cp "$extract/$name/LICENSE" "$out_dir/" 2>/dev/null || true
echo "$VERSION" > "$out_dir/VERSION"
rm -rf "$extract"

echo "OK: $out_dir/lib (ONNX Runtime $VERSION)"
