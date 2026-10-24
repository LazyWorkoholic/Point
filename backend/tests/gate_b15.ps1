# B15 verification gate -- lifecycle, resources, crash recovery, shutdown.
#
#   powershell -ExecutionPolicy Bypass -File D:\Projects\Point\backend\tests\gate_b15.ps1
#
# ONE script, because separate tool calls do not share shell state: the
# scratch-profile redirection has to hold for every command that follows
# it, and a per-command `$env:` patch would leave later commands writing
# to the LIVE %APPDATA%\Point and %LOCALAPPDATA%\Point (CLAUDE.md).
#
# Nothing here touches the real profile. APPDATA and LOCALAPPDATA are
# both overridden, so config.toml, credentials.toml, runtime.db,
# state.json and logs\ all resolve under $scratch -- and nothing under
# the real Point folder is named in a destructive call anywhere in this
# file, which is the rule, not merely the outcome.
#
# Every step is deterministic: the mock provider replays
# tests\fixtures\mock_slow.json (0.3s per chunk), so no Ollama, no key
# and no network. The one thing that is NOT automated is pressing Ctrl+C
# by hand -- step 6 sends the console signal programmatically instead,
# and says what that does and does not prove.
#
# READING THE OUTPUT: the backend logs refusals and warnings to stderr,
# and Windows PowerShell wraps a native command's stderr in a red
# NativeCommandError block. That decoration is PowerShell's, not a
# crash. Read the text, not the colour.

$ErrorActionPreference = "Continue"

. (Join-Path $PSScriptRoot "gatelib.ps1")

$repo    = "D:\Projects\Point"
$backend = Join-Path $repo "backend"
$scratch = Join-Path $env:TEMP "point-scratch-b15-gate"

$script:results = @()
function Step {
    # $code untyped and normalized, for the same reason manual_b15_crash's
    # `Check` is: `[int]$code` throws on the empty collection that a
    # no-output native capture produces, which would crash the summary
    # instead of recording a failure. See gatelib.ps1's Trap 1.
    param([string]$name, $code)
    $numeric = if ($null -eq $code) { 1 } else { @($code)[0] -as [int] }
    if ($null -eq $numeric) { $numeric = 1 }
    $ok = ($numeric -eq 0)
    $code = $numeric
    $script:results += [pscustomobject]@{ Step = $name; Exit = $code; OK = $ok }
    if ($ok) { Write-Host "`n>>> $name : PASS (exit 0)" -ForegroundColor Green }
    else     { Write-Host "`n>>> $name : FAIL (exit $code)" -ForegroundColor Red }
}

Remove-Item -LiteralPath $scratch -Recurse -Force -ErrorAction SilentlyContinue
New-Item -ItemType Directory -Force $scratch | Out-Null

try {
    $env:APPDATA      = $scratch
    $env:LOCALAPPDATA = $scratch
    $env:POINT_MOCK   = "1"
    Set-Location $backend

    $db = Join-Path $scratch "Point\runtime.db"
    Write-Host "scratch profile : $scratch"
    Write-Host "database        : $db"

    # -----------------------------------------------------------------
    # 0. Migration -- B15 is schema v7
    # -----------------------------------------------------------------
    Write-Host "`n=== 0. migrate to v7 (message_checkpoints)" -ForegroundColor Cyan
    uv run python cli.py migrate
    # Invoke-Sql, not a bare `& sqlite3`: it passes a busy timeout the
    # CLI otherwise does not have, keeps stderr out of the value, and
    # returns a scalar string so these comparisons cannot come back as
    # collections. gatelib.ps1 has the measurements.
    $version = Invoke-Sql -Database $db -Query "select max(version) from schema_migrations;"
    Write-Host "schema version: $version  (EXPECT 7)"
    $hasTable = Invoke-Sql -Database $db -Query "select name from sqlite_master where type='table' and name='message_checkpoints';"
    Write-Host "message_checkpoints present: '$hasTable'"
    Step "0. migrate to v7" $(if ($version -eq "7" -and $hasTable -eq "message_checkpoints") { 0 } else { 1 })

    Write-Host "`n=== 0b. the v6 -> v7 UPGRADE path, not just a fresh create" -ForegroundColor Cyan
    # Aryan's real database is at v6. A CREATE TABLE IF NOT EXISTS that
    # works on a fresh file says nothing about an existing one, so this
    # winds the scratch copy back to v6 and migrates it forward.
    Invoke-Sql -Database $db -Query "drop table message_checkpoints; delete from schema_migrations where version = 7;" | Out-Null
    $before = Invoke-Sql -Database $db -Query "select max(version) from schema_migrations;"
    uv run python cli.py migrate
    $after = Invoke-Sql -Database $db -Query "select max(version) from schema_migrations;"
    $hasTable = Invoke-Sql -Database $db -Query "select name from sqlite_master where type='table' and name='message_checkpoints';"
    Write-Host "wound back to v$before, migrated to v$after, table: '$hasTable'"
    Step "0b. v6 -> v7 upgrade" $(if ($before -eq "6" -and $after -eq "7" -and $hasTable) { 0 } else { 1 })

    # -----------------------------------------------------------------
    # 1, 2, 4, 5, 7 -- in-process, deterministic
    # -----------------------------------------------------------------
    Write-Host "`n=== 1/2/4/5/7. unload refusal, replay, checkpoints, bounded growth" -ForegroundColor Cyan
    Write-Host "NOTE: this runs through ONE runtime, and it has to."
    Write-Host "      B15's own suggested check (Start-Job a send, then"
    Write-Host "      ``cli.py workspace unload chat`` from the shell) CANNOT work:"
    Write-Host "      two cli.py invocations are two PROCESSES and therefore two"
    Write-Host "      TaskManagers, so the unload would find an empty live-reference"
    Write-Host "      registry, release everything, and prove nothing. Same reason"
    Write-Host "      B12's gate drives the concurrency cap through one backend."
    uv run python tests\manual_b15_lifecycle.py
    Step "1/2/4/5/7. lifecycle (in-process)" $LASTEXITCODE

    # -----------------------------------------------------------------
    # 5 -- the full harness at the episode's own N
    # -----------------------------------------------------------------
    Write-Host "`n=== 5. bench unload-cycles --n 20" -ForegroundColor Cyan
    Write-Host "EXPECT: a flat objects column and one value in each 'across cycles' set."
    Write-Host "FAILS IF: counts climb monotonically -- that is a real leak, and it is"
    Write-Host "          a different thing from RSS not shrinking. RSS is not reported."
    uv run python cli.py bench unload-cycles --n 20 --path $repo
    Step "5. bench unload-cycles" $LASTEXITCODE

    # -----------------------------------------------------------------
    # 3 -- the kill, in its own process so its scratch profile is its own
    # -----------------------------------------------------------------
    Write-Host "`n=== 3. kill the Python process mid-stream, then restart" -ForegroundColor Cyan
    powershell -ExecutionPolicy Bypass -File (Join-Path $backend "tests\manual_b15_crash.ps1")
    Step "3. crash recovery" $LASTEXITCODE

    # -----------------------------------------------------------------
    # 6 + 7 -- against a real main.py over real pipes
    # -----------------------------------------------------------------
    Write-Host "`n=== 6/7. Shutdown and Ctrl+C against a real main.py" -ForegroundColor Cyan
    Write-Host "NOTE: ``cli.py send --shutdown-after`` proves the ack and the persisted"
    Write-Host "      partial, and CANNOT prove the process exits -- the CLI exits when"
    Write-Host "      its command returns either way. This drives main.py, whose stdin"
    Write-Host "      stays open, so 'it exited' is a real observation."
    Write-Host "      Ctrl+C is sent as CTRL_BREAK_EVENT to a child in its own process"
    Write-Host "      group: the only way to deliver a genuine console interrupt to one"
    Write-Host "      process from a script on Windows. It exercises the same signal"
    Write-Host "      handler a real Ctrl+C does; pressing the keys by hand is still"
    Write-Host "      worth doing once, and step 6b below is that step."
    uv run python tests\manual_b15_shutdown_wire.py
    Step "6/7. shutdown + signal (real process)" $LASTEXITCODE

    # -----------------------------------------------------------------
    # 7 -- the CLI half, for the ack and the partial
    # -----------------------------------------------------------------
    Write-Host "`n=== 7b. send --shutdown-after 1.5, in-process" -ForegroundColor Cyan
    $sid = (uv run python cli.py session new "b15 gate" --provider mock --model mock-slow --workspace chat) -replace '\s.*',''
    uv run python cli.py send "count" --session $sid --script mock_slow --shutdown-after 1.5 --timeout 60
    $ack = $LASTEXITCODE
    Write-Host "`nthe row that Shutdown persisted:"
    Invoke-Sql -Database $db -Query "select seq, role, interrupted, coalesce(outcome,'-'), length(content) from messages where session_id='$sid' order by seq;"
    Step "7b. shutdown ack + partial (CLI)" $ack

    # -----------------------------------------------------------------
    # The heartbeat change (D98), visible
    # -----------------------------------------------------------------
    Write-Host "`n=== D98. the ping is unconditional -- visible during a task" -ForegroundColor Cyan
    Write-Host "EXPECT: ping envelopes with active_tasks > 0. Before B15 the loop"
    Write-Host "        skipped the ping whenever active_count() > 0, so a slow tool"
    Write-Host "        call looked exactly like a deadlocked process -- which is why"
    Write-Host "        Go carried busyGrace (60s), now deleted."
    uv run python tests\manual_b15_heartbeat.py
    Step "D98. unconditional ping" $LASTEXITCODE
}
finally {
    Remove-Item -LiteralPath $scratch -Recurse -Force -ErrorAction SilentlyContinue
}

# ---------------------------------------------------------------------
# The Go side
# ---------------------------------------------------------------------
Write-Host "`n=== Go: the replay seam, the marks, and the deleted busyGrace" -ForegroundColor Cyan
Set-Location $repo
go test ./frontend/...
Step "go test ./frontend/..." $LASTEXITCODE

# ---------------------------------------------------------------------
Write-Host "`n================ SUMMARY ================" -ForegroundColor Cyan
$script:results | Format-Table -AutoSize
$failed = @($script:results | Where-Object { -not $_.OK })
if ($failed.Count -eq 0) {
    Write-Host "ALL AUTOMATED STEPS PASSED" -ForegroundColor Green
} else {
    Write-Host "FAILED STEPS: $($failed.Step -join '; ')" -ForegroundColor Red
}

Write-Host @"

=== 6b. BY HAND, once -- the real keypress ===

    cd D:\Projects\Point\backend
    `$env:APPDATA = `$env:LOCALAPPDATA = "`$env:TEMP\point-manual"
    uv run python main.py
    (paste a CreateSession + SendMessage line, then press Ctrl+C mid-stream)

EXPECT: the process exits within a second or two.
FAILS IF: it hangs -- the stdin reader thread would not be a daemon.

Step 6 above sends CTRL_BREAK_EVENT, which reaches the same handler;
this one proves SIGINT itself. Delete the scratch folder afterwards.
"@ -ForegroundColor DarkCyan

if ($failed.Count -eq 0) { exit 0 } else { exit 1 }
