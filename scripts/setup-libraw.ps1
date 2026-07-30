# Downloads LibRaw and builds a static, thread-safe libraw.a with MinGW-w64.
# Output: third_party/libraw/{lib/libraw.a, include/libraw/*.h}

# Runs on Windows PowerShell 5.1 as well as PowerShell 7: the parallel compile
# drives a pool of g++ processes directly rather than using the 7-only
# ForEach-Object -Parallel. Keep this file pure ASCII -- 5.1 decodes a BOM-less
# script as ANSI, and a UTF-8 em dash then lands as a smart-quote lookalike
# that the parser accepts as a string delimiter.

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
    $rules = @(Select-String -Path $mk -Pattern '^object/(\S+\.o): (src/\S+\.cpp)' | ForEach-Object {
        [pscustomobject]@{ obj = "object/$($_.Matches[0].Groups[1].Value)"; src = $_.Matches[0].Groups[2].Value }
    })
    Write-Host "Compiling $($rules.Count) files..."

    # Pool of concurrent g++ processes. Each compile is an external process
    # anyway, so this needs no PowerShell runspaces (and therefore no
    # ForEach-Object -Parallel, which is 7-only): keep $throttle children alive
    # and reap them as they exit.
    #
    # Uses System.Diagnostics.Process rather than Start-Process -PassThru
    # because under Windows PowerShell 5.1 the object Start-Process hands back
    # never populates ExitCode, even after WaitForExit -- so every compile would
    # look like a failure. Arguments are relative and the source dir is passed
    # as WorkingDirectory, so a checkout path containing spaces stays safe.
    $throttle = [Environment]::ProcessorCount
    $next = 0
    $running = New-Object System.Collections.ArrayList
    $failures = New-Object System.Collections.ArrayList
    while ($next -lt $rules.Count -or $running.Count -gt 0) {
        while ($running.Count -lt $throttle -and $next -lt $rules.Count) {
            $r = $rules[$next]
            $next++
            $gccArgs = @('-c') + $cflags + @('-o', $r.obj, $r.src)
            $psi = New-Object System.Diagnostics.ProcessStartInfo
            $psi.FileName = 'g++'
            $psi.WorkingDirectory = $srcDir
            $psi.Arguments = ($gccArgs | ForEach-Object {
                if ($_ -match '\s') { '"' + $_ + '"' } else { $_ }
            }) -join ' '
            $psi.UseShellExecute = $false
            $psi.RedirectStandardError = $true
            $proc = New-Object System.Diagnostics.Process
            $proc.StartInfo = $psi
            [void]$proc.Start()
            # Drain stderr asynchronously: a compile that emits more than the
            # pipe buffer would otherwise block forever waiting for a reader.
            [void]$running.Add([pscustomobject]@{
                proc = $proc; src = $r.src; err = $proc.StandardError.ReadToEndAsync()
            })
        }
        # Block on one child so the loop cannot spin, then reap everything that
        # has finished (children exit out of order).
        if ($running.Count -gt 0) { $running[0].proc.WaitForExit() }
        foreach ($d in @($running | Where-Object { $_.proc.HasExited })) {
            if ($d.proc.ExitCode -ne 0) {
                $why = $d.err.Result
                if ($why) { $why = $why.Trim() }
                [void]$failures.Add("$($d.src) (exit $($d.proc.ExitCode)): $why")
            }
            $d.proc.Dispose()
            $running.Remove($d)
        }
    }
    if ($failures.Count -gt 0) { throw "libraw compile failed:`n$($failures -join "`n")" }
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
