#!/usr/bin/env bash
# Downloads libjpeg-turbo and builds its static libraries for macOS/Linux (the
# Unix counterpart of setup-libjpeg-turbo.ps1), with its SIMD code, which is
# where its speed comes from: NASM is needed on x86-64 (Arm's Neon needs
# nothing more).
# Output: third_party/libjpeg-turbo/{lib/libturbojpeg.a, include/turbojpeg.h}
set -euo pipefail

VERSION="${LIBJPEG_TURBO_VERSION:-3.2.0}"
root="$(cd "$(dirname "$0")/.." && pwd)"
third="$root/third_party"
src_dir="$third/libjpeg-turbo-src"
build_dir="$third/libjpeg-turbo-build"
out_dir="$third/libjpeg-turbo"
lib_out="$out_dir/lib/libturbojpeg.a"

force=0
[ "${1:-}" = "--force" ] && force=1
if [ -f "$lib_out" ] && [ "$force" -eq 0 ]; then
    echo "libturbojpeg.a already present at $lib_out (use --force to rebuild)"
    exit 0
fi

mkdir -p "$third"

# --- Download & extract ---------------------------------------------------
tarball="$third/libjpeg-turbo-$VERSION.tar.gz"
if [ ! -f "$tarball" ]; then
    url="https://github.com/libjpeg-turbo/libjpeg-turbo/releases/download/$VERSION/libjpeg-turbo-$VERSION.tar.gz"
    echo "Downloading $url"
    curl -fL --retry 3 -o "$tarball" "$url"
fi
rm -rf "$src_dir" "$build_dir"
tar -xzf "$tarball" -C "$third"
mv "$third/libjpeg-turbo-$VERSION" "$src_dir"

# --- Build ----------------------------------------------------------------
# Static only, position-independent so cgo can link it into any binary, and
# with SIMD: without it libjpeg-turbo is no faster than Go's image/jpeg.
echo "Building libjpeg-turbo $VERSION..."
# A plain variable, not an array: macOS's bash 3.2 calls an empty one unbound.
nasm=""
case "$(uname -m)" in x86_64 | amd64) nasm="-DCMAKE_REQUIRE_FIND_PACKAGE_NASM=ON" ;; esac
cmake -S "$src_dir" -B "$build_dir" -DCMAKE_BUILD_TYPE=Release \
    -DENABLE_SHARED=OFF -DENABLE_STATIC=ON -DWITH_TURBOJPEG=ON -DWITH_SIMD=ON \
    -DCMAKE_POSITION_INDEPENDENT_CODE=ON -DCMAKE_INSTALL_PREFIX="$out_dir" \
    -DCMAKE_INSTALL_LIBDIR=lib ${nasm:+"$nasm"}
jobs="$(getconf _NPROCESSORS_ONLN 2>/dev/null || echo 4)"
cmake --build "$build_dir" -j"$jobs"
cmake --install "$build_dir" >/dev/null

# --- Smoke check ------------------------------------------------------------
grep -q "WITH_SIMD:BOOL=ON" "$build_dir/CMakeCache.txt" || { echo "built without SIMD"; exit 1; }
rm -rf "$build_dir"
echo "OK: $lib_out"
