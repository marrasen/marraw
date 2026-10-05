# Runs the marraw gunim test build's measurements on this machine, and packs
# the results into results.zip, next to this script.
#
#   powershell -ExecutionPolicy Bypass -File .\run-tests.ps1
#
# It uses a data folder of its own in %TEMP%, so it leaves no marraw library
# behind, and starts each folder cold: nothing rendered yet.

$ErrorActionPreference = 'Stop'
$here = Split-Path -Parent $MyInvocation.MyCommand.Path
$exe = Join-Path $here 'bin\marraw-cull.exe'
$out = Join-Path $here 'results'
$data = Join-Path $env:TEMP 'marraw-cull-test-data'
if (-not (Test-Path $exe)) { throw "No ${exe}: unpack the whole kit." }
Remove-Item -Recurse -Force $out, $data -ErrorAction SilentlyContinue
New-Item -ItemType Directory $out | Out-Null
$env:GUNIM_DEBUG_FRAMES = '1'

# What this machine is, for reading the numbers.
Get-CimInstance Win32_VideoController | Select-Object Name, DriverVersion, CurrentHorizontalResolution, CurrentVerticalResolution, CurrentRefreshRate |
    Format-List | Out-File (Join-Path $out 'machine.txt')
Get-CimInstance Win32_Processor | Select-Object Name, NumberOfCores | Format-List | Out-File -Append (Join-Path $out 'machine.txt')

function Run($name, $folder, $steps, $every, $extra = '') {
    Write-Host "== $name"
    $log = Join-Path $out "$name.log"
    $shot = Join-Path $out "$name.png"
    # Start-Process rather than & and 2>: Windows PowerShell takes a native
    # program's stderr as errors, and would stop at its first log line.
    $argLine = "-folder `"$folder`" -data-dir `"$data`" -skim $steps -every $every -shot `"$shot`" $extra"
    Start-Process -FilePath $exe -ArgumentList $argLine -RedirectStandardError $log -NoNewWindow -Wait
    Get-Content $log | Select-String '^(steps|zoom): ' | ForEach-Object { Write-Host "   $_" }
}

foreach ($f in Get-ChildItem (Join-Path $here 'photos') -Directory) {
    $n = ($f.Name -replace '[^A-Za-z0-9]+', '-').Trim('-')
    $count = (Get-ChildItem $f.FullName -File).Count
    # Skimming fast through photos never seen: what shows, and how soon.
    Run "$n-1-cold-fast" $f.FullName ($count - 1) '150ms'
    # Slower, so the real renders after a pause get their chance.
    Run "$n-2-slow" $f.FullName ([Math]::Min(20, $count - 1)) '700ms'
    # Give the backend a minute to render ahead, then skim the warm folder.
    Write-Host '   (a minute for the backend to render ahead)'
    Start-Sleep -Seconds 60
    Run "$n-3-warm-fast" $f.FullName ($count - 1) '150ms'
    # Zoom to one to one on a photo, and wait for its full resolution.
    Run "$n-4-zoom" $f.FullName 2 '700ms' '-zoom'
}

Compress-Archive -Force -Path (Join-Path $out '*') -DestinationPath (Join-Path $here 'results.zip')
Write-Host ''
Write-Host "Done: $(Join-Path $here 'results.zip'). Now try it by hand too (see README.txt)."
