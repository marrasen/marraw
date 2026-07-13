#!/usr/bin/env bash
# Downloads LibRaw and builds a static, thread-safe libraw.a for macOS/Linux
# (the Unix counterpart of setup-libraw.ps1).
# Output: third_party/libraw/{lib/libraw.a, include/libraw/*.h}
set -euo pipefail

VERSION="${LIBRAW_VERSION:-0.22.1}"
root="$(cd "$(dirname "$0")/.." && pwd)"
third="$root/third_party"
src_dir="$third/libraw-src"
out_dir="$third/libraw"
lib_out="$out_dir/lib/libraw.a"

force=0
[ "${1:-}" = "--force" ] && force=1
if [ -f "$lib_out" ] && [ "$force" -eq 0 ]; then
    echo "libraw.a already present at $lib_out (use --force to rebuild)"
    exit 0
fi

mkdir -p "$third"

# --- Download & extract ---------------------------------------------------
tarball="$third/LibRaw-$VERSION.tar.gz"
if [ ! -f "$tarball" ]; then
    url="https://www.libraw.org/data/LibRaw-$VERSION.tar.gz"
    echo "Downloading $url"
    curl -fL --retry 3 -o "$tarball" "$url"
fi
rm -rf "$src_dir"
tar -xzf "$tarball" -C "$third"
mv "$third/LibRaw-$VERSION" "$src_dir"

# --- Build ----------------------------------------------------------------
# Makefile.dist is LibRaw's autotools-free Unix build. It produces TWO
# archives: lib/libraw.a is compiled with -DLIBRAW_NOTHREADS (single-thread
# only) and lib/libraw_r.a is the reentrant build. marraw runs a pool of
# concurrent handles, so only the _r one is usable — install it under the
# plain name that the cgo -lraw link expects. It is also compiled with
# -DUSE_ZLIB (deflate DNG support), hence the -lz in the unix cgo LDFLAGS.
echo "Building libraw_r.a (this takes a few minutes)..."
jobs="$(getconf _NPROCESSORS_ONLN 2>/dev/null || echo 4)"
make -C "$src_dir" -f Makefile.dist -j"$jobs" library

# --- Install ----------------------------------------------------------------
mkdir -p "$out_dir/lib" "$out_dir/include/libraw"
cp "$src_dir/lib/libraw_r.a" "$out_dir/lib/libraw.a"
cp "$src_dir"/libraw/*.h "$out_dir/include/libraw/"

# --- Smoke check ------------------------------------------------------------
count="$(ar t "$lib_out" | wc -l)"
echo "libraw.a contains $count objects"
echo "OK: $lib_out"
