# Downloads the prebuilt ONNX Runtime shared library for Windows x64
# (the Windows counterpart of setup-ort.sh).
# Output: third_party/onnxruntime/lib/onnxruntime.dll (+ providers_shared dll)
#
# With -DirectML it instead stages the DirectML-enabled build into a SEPARATE
# tree, third_party/onnxruntime-directml/lib/, so both runtimes can coexist and
# switching execution provider costs one env var (MARRAW_ORT_LIB). Used to
# measure the GPU path: see design/ml-denoise.md.
#
# Keep this file pure ASCII. Windows PowerShell 5.1 decodes a BOM-less script
# as ANSI, so a UTF-8 em dash lands as a smart-quote lookalike that the parser
# accepts as a string delimiter.
#
# The Go binding (github.com/yalue/onnxruntime_go) loads this DLL at runtime;
# internal/infer resolves it via MARRAW_ORT_LIB, the exe dir, or this path.
param(
    [switch]$Force,
    [switch]$DirectML,
    [switch]$CUDA,
    # Newest DirectML ONNX Runtime published. Microsoft ships no DirectML build
    # on the GitHub releases page at all and the NuGet package stops here, so
    # this is not a lagging pin we can bump: it is the ceiling. The binding
    # (onnxruntime_go v1.27.0, ORT API 24) loads it because 1.24 is API 24.
    [string]$DmlVersion = '1.24.4',
    [string]$AiDmlVersion = '1.15.4'
)
$ErrorActionPreference = 'Stop'
# PowerShell 5.1 renders a progress bar per response chunk, which makes
# Invoke-WebRequest roughly an order of magnitude slower on 100 MB downloads.
$ProgressPreference = 'SilentlyContinue'

$Version = if ($env:ORT_VERSION) { $env:ORT_VERSION } else { '1.27.1' }
$root = Split-Path -Parent $PSScriptRoot
$third = Join-Path $root 'third_party'
$outDir = Join-Path $third 'onnxruntime'
$libOut = Join-Path $outDir 'lib\onnxruntime.dll'

if ($DirectML -and $CUDA) { throw "-DirectML and -CUDA are mutually exclusive" }
if ($DirectML) {
    $outDir = Join-Path $third 'onnxruntime-directml'
    $libOut = Join-Path $outDir 'lib\onnxruntime.dll'
}
if ($CUDA) {
    $outDir = Join-Path $third 'onnxruntime-cuda'
    $libOut = Join-Path $outDir 'lib\onnxruntime.dll'
}

if ((Test-Path $libOut) -and -not $Force) {
    Write-Host "onnxruntime.dll already present at $libOut (use -Force to re-download)"
    exit 0
}

New-Item -ItemType Directory -Force $third | Out-Null

# Downloads a zip-shaped archive (.zip, .nupkg, .whl are all zips), caching it
# in third_party, and returns the extract dir.
function Expand-RemoteZip($url, $name) {
    $zip = Join-Path $third "$name.zip"
    if (-not (Test-Path $zip)) {
        Write-Host "Downloading $url"
        Invoke-WebRequest -Uri $url -OutFile $zip
    }
    $ex = Join-Path $third "$name-extract"
    if (Test-Path $ex) { Remove-Item -Recurse -Force $ex }
    Expand-Archive -Path $zip -DestinationPath $ex
    return $ex
}

# NuGet is the only channel that ships a DirectML-enabled ORT.
function Expand-Nupkg($id, $ver) {
    $lower = $id.ToLower()
    return Expand-RemoteZip "https://api.nuget.org/v3-flatcontainer/$lower/$ver/$lower.$ver.nupkg" "$lower.$ver"
}

# Resolves the newest win_amd64 wheel for a PyPI package and extracts it.
# NVIDIA publishes the CUDA runtime and cuDNN as wheels, which is how we avoid
# requiring a multi-GB CUDA toolkit installer and an NVIDIA account for cuDNN.
function Expand-PyPiWheel($pkg) {
    $meta = Invoke-RestMethod "https://pypi.org/pypi/$pkg/json"
    $ver = $meta.info.version
    $wheel = $meta.releases.$ver | Where-Object { $_.filename -like '*win_amd64*' } | Select-Object -First 1
    if (-not $wheel) { throw "$pkg $ver has no win_amd64 wheel" }
    Write-Host ("  {0} {1} ({2:N0} MB)" -f $pkg, $ver, ($wheel.size / 1MB))
    return Expand-RemoteZip $wheel.url "$pkg-$ver"
}

# Locates one DLL inside an extracted package. Searched rather than hardcoded
# because NuGet native layouts drift between versions; fails loudly with the
# archive contents so a moved path is obvious instead of silently absent.
function Find-NativeDll($extract, $file, $archHint) {
    $hits = @(Get-ChildItem -Path $extract -Recurse -Filter $file -File |
        Where-Object { $_.FullName -like "*$archHint*" })
    if ($hits.Count -eq 0) {
        Write-Host "Package contents:"
        Get-ChildItem -Path $extract -Recurse -Filter '*.dll' -File |
            ForEach-Object { Write-Host "  $($_.FullName.Substring($extract.Length))" }
        throw "$file (arch '$archHint') not found in $extract"
    }
    return $hits[0].FullName
}

if ($DirectML) {
    $ortEx = Expand-Nupkg 'Microsoft.ML.OnnxRuntime.DirectML' $DmlVersion
    $dmlEx = Expand-Nupkg 'Microsoft.AI.DirectML' $AiDmlVersion

    $ortDll = Find-NativeDll $ortEx 'onnxruntime.dll' 'win-x64'
    # DirectML.dll must sit in the SAME directory as onnxruntime.dll: the
    # binding loads ORT by absolute path, and Windows resolves a dependent DLL
    # from the loading module's own directory. Shipping our own copy rather
    # than relying on the System32 one keeps the measurement reproducible.
    $dmlDll = Find-NativeDll $dmlEx 'DirectML.dll' 'x64'

    if (Test-Path $outDir) { Remove-Item -Recurse -Force $outDir }
    $libDir = Join-Path $outDir 'lib'
    New-Item -ItemType Directory -Force $libDir | Out-Null
    Copy-Item $ortDll $libDir
    Copy-Item $dmlDll $libDir
    $shared = @(Get-ChildItem -Path (Split-Path -Parent $ortDll) -Filter 'onnxruntime_providers_*.dll' -File)
    foreach ($s in $shared) { Copy-Item $s.FullName $libDir }
    Copy-Item (Join-Path $ortEx 'LICENSE.txt') $outDir -ErrorAction SilentlyContinue
    Set-Content (Join-Path $outDir 'VERSION') "$DmlVersion (DirectML $AiDmlVersion)"
    Remove-Item -Recurse -Force $ortEx, $dmlEx

    Write-Host ""
    Write-Host "OK: $libOut (ONNX Runtime $DmlVersion, DirectML $AiDmlVersion)"
    Get-ChildItem $libDir | ForEach-Object { Write-Host ("  {0,-38} {1,8:N0} KB" -f $_.Name, ($_.Length / 1KB)) }
    Write-Host ""
    Write-Host "To use the GPU path:"
    Write-Host "  `$env:MARRAW_ORT_LIB=`"$libOut`""
    Write-Host "  `$env:MARRAW_TEST_GPU=`"1`""
    exit 0
}

if ($CUDA) {
    # ORT's gpu_cuda12 release ships the provider DLLs but NOT the CUDA runtime
    # or cuDNN, which it dynamically links (verified with objdump: cudart64_12,
    # cublas64_12, cublasLt64_12, cudnn64_9, cufft64_11). Those come from
    # NVIDIA's PyPI wheels rather than the CUDA toolkit installer: same
    # binaries, no installer, no NVIDIA account for cuDNN, and pinned by the
    # wheel version. MSVCP140/VCRUNTIME140 are expected from the system VC++
    # redistributable.
    $ortEx = Expand-RemoteZip `
        "https://github.com/microsoft/onnxruntime/releases/download/v$Version/onnxruntime-win-x64-gpu_cuda12-$Version.zip" `
        "onnxruntime-win-x64-gpu_cuda12-$Version"

    if (Test-Path $outDir) { Remove-Item -Recurse -Force $outDir }
    $libDir = Join-Path $outDir 'lib'
    New-Item -ItemType Directory -Force $libDir | Out-Null

    $ortLib = Split-Path -Parent (Find-NativeDll $ortEx 'onnxruntime.dll' 'lib')
    Copy-Item (Join-Path $ortLib '*.dll') $libDir
    Copy-Item (Join-Path $ortEx 'LICENSE') $outDir -ErrorAction SilentlyContinue

    Write-Host "Fetching CUDA runtime + cuDNN from NVIDIA's PyPI wheels:"
    $wheels = @('nvidia-cuda-runtime-cu12', 'nvidia-cublas-cu12', 'nvidia-cudnn-cu12', 'nvidia-cufft-cu12')
    $wheelVers = @()
    foreach ($w in $wheels) {
        $ex = Expand-PyPiWheel $w
        # Wheels lay the DLLs out under nvidia/<component>/bin. Copy every DLL
        # there rather than a hand-listed set: cuDNN 9 is split across a dozen
        # sub-libraries that cudnn64_9.dll loads itself.
        $bins = @(Get-ChildItem -Path $ex -Recurse -Directory -Filter 'bin')
        foreach ($b in $bins) { Copy-Item (Join-Path $b.FullName '*.dll') $libDir -ErrorAction SilentlyContinue }
        $wheelVers += (Split-Path -Leaf $ex) -replace '-extract$', ''
        Remove-Item -Recurse -Force $ex
    }
    Remove-Item -Recurse -Force $ortEx

    Set-Content (Join-Path $outDir 'VERSION') "$Version cuda12`n$($wheelVers -join "`n")"
    $dlls = @(Get-ChildItem $libDir -Filter '*.dll')
    Write-Host ""
    Write-Host "OK: $libOut (ONNX Runtime $Version CUDA 12, $($dlls.Count) DLLs, $([math]::Round((($dlls | Measure-Object -Sum Length).Sum)/1GB,2)) GB)"
    Write-Host ""
    Write-Host "To use the CUDA path:"
    Write-Host "  `$env:MARRAW_ORT_LIB=`"$libOut`""
    Write-Host "  `$env:MARRAW_TEST_GPU=`"1`"; `$env:MARRAW_GPU_EP=`"cuda`""
    exit 0
}

$name = "onnxruntime-win-x64-$Version"
$zip = Join-Path $third "$name.zip"
if (-not (Test-Path $zip)) {
    $url = "https://github.com/microsoft/onnxruntime/releases/download/v$Version/$name.zip"
    Write-Host "Downloading $url"
    Invoke-WebRequest -Uri $url -OutFile $zip
}

$extract = Join-Path $third "$name-extract"
if (Test-Path $extract) { Remove-Item -Recurse -Force $extract }
Expand-Archive -Path $zip -DestinationPath $extract

if (Test-Path $outDir) { Remove-Item -Recurse -Force $outDir }
New-Item -ItemType Directory -Force (Join-Path $outDir 'lib') | Out-Null
Copy-Item (Join-Path $extract "$name\lib\*.dll") (Join-Path $outDir 'lib')
Copy-Item (Join-Path $extract "$name\LICENSE") $outDir -ErrorAction SilentlyContinue
Set-Content (Join-Path $outDir 'VERSION') $Version
Remove-Item -Recurse -Force $extract

Write-Host "OK: $libOut (ONNX Runtime $Version)"
