# Stages the hash-pinned restoration models the internal/infer tiled-inference
# tests measure against, into .devdata/models (gitignored).
#
# These are deliberately NOT in the production model registry: they are dev
# fixtures for throughput measurement, and internal/infer/registry.go stays
# empty of restoration models until the denoise feature unlocks. Because their
# ModelSpec carries no URL, download.go refuses to fetch them and the tests
# skip: this script is what turns the pins in design/ml-denoise.md from prose
# into something reproducible.
#
# Keep this file pure ASCII. Windows PowerShell 5.1 decodes a BOM-less script
# as ANSI, and a UTF-8 em dash then lands as a smart-quote lookalike, which the
# parser accepts as a string delimiter and silently mangles the rest of a line.
#
# Usage: powershell -NoProfile -ExecutionPolicy Bypass -File scripts/setup-devmodels.ps1
#        (add -Force to re-download)
param([switch]$Force)
$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'

$root = Split-Path -Parent $PSScriptRoot
$outDir = Join-Path $root '.devdata\models'

# File names are ModelSpec.FileName() = "<id>-<version>.onnx" for the ids the
# tests load ("scunet", "swin2sr") at Version "1".
$models = @(
    @{
        File = 'scunet-1.onnx'
        Url  = 'https://huggingface.co/deepghs/image_restoration/resolve/main/SCUNet-PSNR.onnx'
        Hash = 'b0f8c12f1575bb49e39a85924152f1c6d4b527a4aae0432c9e5c7397123465e3'
        Desc = 'SCUNet-PSNR blind real-photo denoise (MIT chain via deepghs, ~91 MB)'
    },
    @{
        File = 'swin2sr-1.onnx'
        Url  = 'https://huggingface.co/Xenova/swin2SR-classical-sr-x2-64/resolve/main/onnx/model.onnx'
        Hash = '6dde3fe2440543ccae7c40d175609f83c18aeaa3d8456745c4329ef97ae744bd'
        Desc = 'Swin2SR classical x2 super resolution (Apache-2.0, ~54 MB)'
    }
)

New-Item -ItemType Directory -Force $outDir | Out-Null

foreach ($m in $models) {
    $dest = Join-Path $outDir $m.File

    if ((Test-Path $dest) -and -not $Force) {
        $have = (Get-FileHash -Path $dest -Algorithm SHA256).Hash.ToLower()
        if ($have -eq $m.Hash) {
            Write-Host "OK: $($m.File) already present, hash verified"
            continue
        }
        Write-Warning "$($m.File) present but hash mismatch, re-downloading"
    }

    Write-Host "Downloading $($m.Desc)"
    # Download beside the target and rename only after the hash checks out, so
    # an interrupted run never leaves a truncated file that looks staged.
    $tmp = "$dest.part"
    if (Test-Path $tmp) { Remove-Item -Force $tmp }
    Invoke-WebRequest -Uri $m.Url -OutFile $tmp

    $got = (Get-FileHash -Path $tmp -Algorithm SHA256).Hash.ToLower()
    if ($got -ne $m.Hash) {
        Remove-Item -Force $tmp
        throw "$($m.File): SHA-256 mismatch`n  want $($m.Hash)`n  got  $got"
    }
    Move-Item -Force $tmp $dest
    $sizeMB = [math]::Round((Get-Item $dest).Length / 1MB, 1)
    Write-Host "OK: $dest ($sizeMB MB, hash verified)"
}

Write-Host ""
Write-Host "Models staged in $outDir"
Write-Host "Run the throughput tests with:"
Write-Host "  go test ./internal/infer -run 'TestRunTiled(Throughput|Soak)' -v -count=1 -timeout 60m"
