# Downloads the prebuilt ONNX Runtime shared library for Windows x64
# (the Windows counterpart of setup-ort.sh).
# Output: third_party/onnxruntime/lib/onnxruntime.dll (+ providers_shared dll)
#
# The Go binding (github.com/yalue/onnxruntime_go) loads this DLL at runtime;
# internal/infer resolves it via MARRAW_ORT_LIB, the exe dir, or this path.
param([switch]$Force)
$ErrorActionPreference = 'Stop'

$Version = if ($env:ORT_VERSION) { $env:ORT_VERSION } else { '1.27.1' }
$root = Split-Path -Parent $PSScriptRoot
$third = Join-Path $root 'third_party'
$outDir = Join-Path $third 'onnxruntime'
$libOut = Join-Path $outDir 'lib\onnxruntime.dll'

if ((Test-Path $libOut) -and -not $Force) {
    Write-Host "onnxruntime.dll already present at $libOut (use -Force to re-download)"
    exit 0
}

New-Item -ItemType Directory -Force $third | Out-Null

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
