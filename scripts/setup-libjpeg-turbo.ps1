# Downloads libjpeg-turbo and builds its static libraries for Windows with
# MinGW-w64, the toolchain cgo links with, and with its SIMD code, which is
# where its speed comes from: CMake and NASM must be on PATH (the CI installs
# NASM; a developer gets it with `winget install NASM.NASM`).
# Output: third_party/libjpeg-turbo/{lib/libturbojpeg.a, include/turbojpeg.h}
param(
    [string]$Version = "3.2.0",
    [switch]$Force
)
$ErrorActionPreference = "Stop"
$root = Split-Path -Parent $PSScriptRoot
$third = Join-Path $root "third_party"
$srcDir = Join-Path $third "libjpeg-turbo-src"
$buildDir = Join-Path $third "libjpeg-turbo-build"
$outDir = Join-Path $third "libjpeg-turbo"
$libOut = Join-Path $outDir "lib\libturbojpeg.a"

if ((Test-Path $libOut) -and -not $Force) {
    Write-Host "libturbojpeg.a already present at $libOut (use -Force to rebuild)"
    exit 0
}

New-Item -ItemType Directory -Force $third | Out-Null

# --- Download & extract ---------------------------------------------------
$tarball = Join-Path $third "libjpeg-turbo-$Version.tar.gz"
if (-not (Test-Path $tarball)) {
    $url = "https://github.com/libjpeg-turbo/libjpeg-turbo/releases/download/$Version/libjpeg-turbo-$Version.tar.gz"
    Write-Host "Downloading $url"
    curl.exe -fL --retry 3 -o $tarball $url
    if ($LASTEXITCODE -ne 0) { throw "download failed" }
}
foreach ($d in $srcDir, $buildDir) { if (Test-Path $d) { Remove-Item -Recurse -Force $d } }
tar -xzf $tarball -C $third
if ($LASTEXITCODE -ne 0) { throw "extract failed" }
Rename-Item (Join-Path $third "libjpeg-turbo-$Version") $srcDir

# --- Build ----------------------------------------------------------------
# MinGW Makefiles with gcc: the libraries must be MinGW's for cgo to link
# them, not MSVC's.
Write-Host "Building libjpeg-turbo $Version..."
cmake -S $srcDir -B $buildDir -G "MinGW Makefiles" -DCMAKE_BUILD_TYPE=Release `
    -DCMAKE_C_COMPILER=gcc -DENABLE_SHARED=OFF -DENABLE_STATIC=ON -DWITH_TURBOJPEG=ON -DWITH_SIMD=ON `
    -DCMAKE_INSTALL_PREFIX="$outDir" -DCMAKE_INSTALL_LIBDIR=lib -DCMAKE_REQUIRE_FIND_PACKAGE_NASM=ON
if ($LASTEXITCODE -ne 0) { throw "cmake configure failed" }
cmake --build $buildDir -j ([Environment]::ProcessorCount)
if ($LASTEXITCODE -ne 0) { throw "build failed" }
cmake --install $buildDir | Out-Null
if ($LASTEXITCODE -ne 0) { throw "install failed" }

# --- Smoke check ------------------------------------------------------------
if (-not (Select-String -Quiet -Path (Join-Path $buildDir "CMakeCache.txt") -Pattern "WITH_SIMD:BOOL=ON")) {
    throw "built without SIMD"
}
if (-not (Test-Path $libOut)) { throw "no $libOut" }
Remove-Item -Recurse -Force $buildDir
Write-Host "OK: $libOut"
