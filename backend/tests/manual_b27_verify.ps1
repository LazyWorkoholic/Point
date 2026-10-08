# B27 verification gate. Never touches the real %APPDATA%\Point or
# %LOCALAPPDATA%\Point -- both env vars are pointed at a scratch dir for
# this script's duration only (CLAUDE.md's hard rule).
#
# Covers:
#   - Done-when #1-#4: backend\tests\manual_b27_doctor_resolved.py,
#     run three times against three config.toml starting states
#     (absent / empty-but-present / present-with-other-keys), since
#     "no config.toml value for any of the five keys" is the realistic
#     case in all three, not just the absent-file case.
#   - Done-when #5: `cli.py doctor` stdout captured to a file for a
#     byte-diff against a baseline YOU already captured from
#     unmodified code -- this script does not do that diff itself.
#
# Run from repo root:
#   powershell -File backend\tests\manual_b27_verify.ps1

$ErrorActionPreference = "Stop"
$realAppData = $env:APPDATA
$realLocalAppData = $env:LOCALAPPDATA
$scratch = Join-Path $env:TEMP "point-scratch-b27"

function Run-OneConfigCase($label, $configContent) {
    Write-Host "`n=== config.toml case: $label ===" -ForegroundColor Cyan
    New-Item -ItemType Directory -Force $scratch | Out-Null
    try {
        $env:APPDATA      = $scratch
        $env:LOCALAPPDATA = $scratch
        cd D:\Projects\Point\backend
        uv run python cli.py migrate

        if ($null -ne $configContent) {
            New-Item -ItemType Directory -Force $scratch\Point | Out-Null
            Set-Content -LiteralPath "$scratch\Point\config.toml" -Value $configContent -Encoding utf8
        }

        uv run python tests\manual_b27_doctor_resolved.py
        $exit = $LASTEXITCODE
        if ($exit -ne 0) {
            throw "manual_b27_doctor_resolved.py failed for case '$label' (exit $exit)"
        }
    }
    finally {
        $env:APPDATA      = $realAppData
        $env:LOCALAPPDATA = $realLocalAppData
        Remove-Item -LiteralPath $scratch -Recurse -Force -ErrorAction SilentlyContinue
    }
}

# Case 1: config.toml absent entirely.
Run-OneConfigCase "absent" $null

# Case 2: config.toml present but empty.
Run-OneConfigCase "empty-but-present" ""

# Case 3: config.toml present with other keys, none of the five.
#
# [ui] is a REAL top-level table (CLAUDE.md: "Go's config.Load() parses
# only the [ui] table"), but the Python backend never reads it (grep
# confirms no `appconfig.config_value("ui...` call site anywhere in
# backend\), so it is genuinely inert here -- unlike an earlier version
# of this case, which used `[database]\npath = "runtime.db"` as its
# filler. That accidentally set a REAL, interpreted key:
# `resolve_db_path()` (bootstrap.py) reads `database.path` from
# config.toml, and a RELATIVE value resolves against the calling
# process's cwd, not the scratch profile -- so instead of exercising
# "other keys", the first version of this case redirected the database
# itself to backend\runtime.db for the rest of this case's checks. Six
# backend\runtime.db / runtime.db.bak-N files were the result, diagnosed
# and cleaned up (never committed); see this episode's handoff notes.
# `resolve_db_path()` did exactly what its own docstring says
# (`POINT_DB -> config database.path -> %LOCALAPPDATA%\Point\runtime.db`)
# -- this was a test-script bug, not a production fallback.
Run-OneConfigCase "present-with-other-keys" "[ui]`ntheme = `"dark`"`n"

# Done-when #5: capture `cli.py doctor` stdout on a fresh scratch
# profile for YOUR OWN byte-diff against a baseline already captured
# from unmodified code.
Write-Host "`n=== Done-when #5: capturing cli.py doctor stdout ===" -ForegroundColor Cyan
New-Item -ItemType Directory -Force $scratch | Out-Null
try {
    $env:APPDATA      = $scratch
    $env:LOCALAPPDATA = $scratch
    cd D:\Projects\Point\backend
    uv run python cli.py migrate
    uv run python cli.py doctor | Out-File -Encoding utf8 "$scratch\doctor-after.txt"
    Copy-Item "$scratch\doctor-after.txt" "$env:TEMP\point-b27-doctor-after.txt" -Force
    Write-Host "Saved to $env:TEMP\point-b27-doctor-after.txt -- diff this yourself against your pre-episode capture."
}
finally {
    $env:APPDATA      = $realAppData
    $env:LOCALAPPDATA = $realLocalAppData
    Remove-Item -LiteralPath $scratch -Recurse -Force -ErrorAction SilentlyContinue
}

Write-Host "`nAll in-process checks (Done-when #1-#4) passed for all three config.toml cases." -ForegroundColor Green
Write-Host "Done-when #5 output is waiting at `$env:TEMP\point-b27-doctor-after.txt for your own diff." -ForegroundColor Green
