# Controlled proof that timeout_s now interrupts a single long-running
# generation mid-stream, not just between steps (B11 fix).
$ErrorActionPreference = "Continue"
[Console]::OutputEncoding = [Text.Encoding]::UTF8
$scratchRoot = Join-Path $env:TEMP ("point-b11-deadline-" + (Get-Date -Format "yyyyMMdd-HHmmss"))
New-Item -ItemType Directory -Force -Path $scratchRoot | Out-Null
$env:APPDATA = Join-Path $scratchRoot "Roaming"
$env:LOCALAPPDATA = Join-Path $scratchRoot "Local"
Remove-Item Env:\POINT_MOCK -ErrorAction SilentlyContinue
Remove-Item Env:\POINT_DB -ErrorAction SilentlyContinue
Set-Location "D:\Projects\Point\backend"
uv run python cli.py migrate

$sessOut = uv run python cli.py session new "deadline check"
$sessionId = ($sessOut -split '\s+')[0]

Write-Host "`n=== a deliberately long single-step generation, capped to --max-seconds 8 ==="
$start = Get-Date
uv run python cli.py send "@research write an extremely long, detailed, exhaustive essay about the entire history of computer science, covering every decade from the 1800s to today in as much detail as possible, at least 3000 words" --session $sessionId --wait --max-seconds 8 --max-steps 1 --timeout 60
$elapsedSeconds = ((Get-Date) - $start).TotalSeconds
Write-Host "`nwall time for the whole CLI call: ${elapsedSeconds}s"
Write-Host "EXPECT: TaskFailed code=bounds_exceeded printed above, well under --timeout's 60s ceiling."
