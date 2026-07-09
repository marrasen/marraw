# Downloads LibRaw and builds a static, thread-safe libraw.a with MinGW-w64.
# Output: third_party/libraw/{lib/libraw.a, include/libraw/*.h}

# The parallel compile below needs ForEach-Object -Parallel, which Windows
# PowerShell 5.1 does not have.
#Requires -Version 7.0

param(
    [string]$Version = "0.22.1",
    [switch]$OpenMP,
    [switch]$Force
)
$ErrorActionPreference = "Stop"
$root = Split-Path -Parent $PSScriptRoot
$third = Join-Path $root "third_party"
$srcDir = Join-Path $third "libraw-src"
$outDir = Join-Path $third "libraw"
$libOut = Join-Path $outDir "lib\libraw.a"

if ((Test-Path $libOut) -and -not $Force) {
    Write-Host "libraw.a already present at $libOut (use -Force to rebuild)"
    exit 0
}

New-Item -ItemType Directory -Force $third | Out-Null

# --- Download & extract -------------------------------------------------
$tarball = Join-Path $third "LibRaw-$Version.tar.gz"
if (-not (Test-Path $tarball)) {
    $url = "https://www.libraw.org/data/LibRaw-$Version.tar.gz"
    Write-Host "Downloading $url"
    curl.exe -fL --retry 3 -o $tarball $url
    if ($LASTEXITCODE -ne 0) { throw "download failed" }
}
if (Test-Path $srcDir) { Remove-Item -Recurse -Force $srcDir }
tar -xzf $tarball -C $third
if ($LASTEXITCODE -ne 0) { throw "extract failed" }
Rename-Item (Join-Path $third "LibRaw-$Version") $srcDir

# --- Patch Makefile.mingw ------------------------------------------------
# 1. Remove -DLIBRAW_NOTHREADS: the stock mingw build is single-thread-only;
#    marraw runs a pool of concurrent handles.
# 2. Optionally enable OpenMP.
$mk = Join-Path $srcDir "Makefile.mingw"
$content = Get-Content $mk -Raw
$content = $content -replace '-DLIBRAW_NOTHREADS[ \t]*', ''
if ($OpenMP) {
    $content = $content -replace '(?m)^(CFLAGS\s*=\s*)', '$1-fopenmp '
    $content = $content -replace '(?m)^(LDADD\s*=\s*)', '$1-fopenmp '
}
Set-Content $mk $content -NoNewline

# --- Build ---------------------------------------------------------------
Write-Host "Building libraw.a (this takes a few minutes)..."
# Compile directly from PowerShell instead of make: the Makefile's recipes
# shell out to rm/cp which don't exist on plain Windows. The object rules
# are trivially parseable, so drive gcc ourselves, in parallel.
Push-Location $srcDir
try {
    $cflags = @("-O3", "-I.", "-w")
    if ($OpenMP) { $cflags += "-fopenmp" }
    $rules = Select-String -Path $mk -Pattern '^object/(\S+\.o): (src/\S+\.cpp)' | ForEach-Object {
        [pscustomobject]@{ obj = "object/$($_.Matches[0].Groups[1].Value)"; src = $_.Matches[0].Groups[2].Value }
    }
    Write-Host "Compiling $($rules.Count) files..."
    $failures = $rules | ForEach-Object -Parallel {
        Set-Location $using:PWD
        g++ -c @($using:cflags) -o $_.obj $_.src
        if ($LASTEXITCODE -ne 0) { $_.src }
    } -ThrottleLimit ([Environment]::ProcessorCount)
    if ($failures) { throw "libraw compile failed: $failures" }
    Remove-Item lib\libraw.a -ErrorAction SilentlyContinue
    ar crs lib\libraw.a (Get-ChildItem object\*.o).FullName
    if ($LASTEXITCODE -ne 0) { throw "ar failed" }
    ranlib lib\libraw.a
} finally { Pop-Location }

# --- Install -------------------------------------------------------------
New-Item -ItemType Directory -Force (Join-Path $outDir "lib") | Out-Null
New-Item -ItemType Directory -Force (Join-Path $outDir "include\libraw") | Out-Null
Copy-Item (Join-Path $srcDir "lib\libraw.a") (Join-Path $outDir "lib\libraw.a") -Force
Copy-Item (Join-Path $srcDir "libraw\*.h") (Join-Path $outDir "include\libraw\") -Force

# --- Smoke check ----------------------------------------------------------
$syms = ar t $libOut
Write-Host "libraw.a contains $($syms.Count) objects"
Write-Host "OK: $libOut"
