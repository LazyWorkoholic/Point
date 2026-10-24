# B15 Done-when #3: kill the Python process mid-stream, and the partial
# reply is still there when it comes back.
#
# This is the strongest beat in the demo, and it is the one check that
# cannot be faked in-process: the whole point is that no handler runs, no
# terminal event is emitted, and the accumulator dies with the process.
# What survives is whatever reached `message_checkpoints` before the
# kill, which startup then promotes into a real `messages` row marked
# interrupted.
#
# WHY THERE IS NO `Start-Sleep` BEFORE THE KILL ANY MORE
#
# There was, and it was the bug. `Start-Sleep -Seconds 2` had to cover
# interpreter start, bootstrap, the migrations check, agent seeding and
# the first text delta before a checkpoint could exist. Measured on this
# machine: the first checkpoint lands on disk at **+0.90s** idle (four
# trials, 0.90-0.91s) and at **+1.42-1.65s** with the machine busy. This
# script runs straight after `gate_b15.ps1`'s 20-cycle benchmark, so
# busy is the normal case and the margin was ~0.35s. The gate failed on
# machine load, not on anything about the product.
#
# So it waits for the precondition instead of guessing at it:
# `Wait-ForSql` polls until a checkpoint row is actually on disk AND has
# been updated at least once (which proves the 500ms cadence fired more
# than once), then kills immediately. That is strictly stronger than the
# sleep -- the wait expiring is itself a real assertion that
# checkpointing happened -- and it does not care how fast the machine is.
#
# Runs entirely inside a scratch profile. APPDATA and LOCALAPPDATA are
# both overridden for the script's duration, so nothing under the real
# Point folder is a target of anything here, including a failure --
# config.toml, credentials.toml, runtime.db, state.json and logs\ all
# resolve under $scratch. See CLAUDE.md on why this replaced the
# rename-aside pattern.
#
#     cd D:\Projects\Point\backend
#     powershell -ExecutionPolicy Bypass -File tests\manual_b15_crash.ps1
#
# Exits non-zero if any expectation failed.

$ErrorActionPreference = "Stop"

. (Join-Path $PSScriptRoot "gatelib.ps1")

$repo    = "D:\Projects\Point"
$backend = Join-Path $repo "backend"
$py      = Join-Path $repo ".venv\Scripts\python.exe"
$scratch = Join-Path $env:TEMP "point-scratch-b15-crash"

$script:failures = @()

function Check {
    # $ok is DELIBERATELY untyped. A `[bool]$ok` throws a parameter
    # transformation error when handed the empty collection that a
    # no-rows `sqlite3` capture produces through `-like`/`-match`, which
    # crashes the gate at precisely the moment a check was about to
    # fail. `Test-Truthy` normalizes instead, so a failing check reports
    # the product fact. See gatelib.ps1's Trap 1.
    param([string]$label, $ok, [string]$detail = "")
    if (Test-Truthy $ok) {
        Write-Host "  [PASS] $label" -ForegroundColor Green
    } else {
        Write-Host "  [FAIL] $label" -ForegroundColor Red
        if ($detail) { Write-Host "         $detail" -ForegroundColor DarkYellow }
        $script:failures += $label
    }
}

Remove-Item -LiteralPath $scratch -Recurse -Force -ErrorAction SilentlyContinue
New-Item -ItemType Directory -Force $scratch | Out-Null

try {
    $env:APPDATA      = $scratch
    $env:LOCALAPPDATA = $scratch
    $env:POINT_MOCK   = "1"
    Set-Location $backend

    $db = Join-Path $scratch "Point\runtime.db"

    Write-Host "`n[setup] migrate + a session on the mock slow fixture" -ForegroundColor Cyan
    & $py cli.py migrate | Out-Null
    $line = & $py cli.py session new "b15 crash" --provider mock --model mock-slow --workspace chat
    $sid = ($line -split '\s+')[0]
    Check "a session was created" ($sid -match '^[0-9a-f-]{36}$') "session line: $line"

    Write-Host "`n[1] start a slow reply, then kill the process mid-stream" -ForegroundColor Cyan

    # The exact kill, and why it is this and not a filter: the process is
    # started with -PassThru, so $proc.Id IS the python.exe running
    # Point and nothing has to be matched by name or command line. The
    # venv interpreter is invoked directly rather than through `uv run`
    # for the same reason -- `uv run` would be the process PowerShell
    # hands back, and the python it spawns underneath would be a child
    # this script never learns the pid of.
    $proc = Start-Process -FilePath $py `
        -ArgumentList @("cli.py", "send", "count", "--session", $sid,
                        "--script", "mock_slow", "--timeout", "60") `
        -WorkingDirectory $backend -PassThru -NoNewWindow
    Write-Host "       pid $($proc.Id), streaming..."

    # Wait for a checkpoint that has been WRITTEN AND UPDATED at least
    # once: `updated_at > created_at` only becomes true on the second
    # tick, so this also proves the 500ms cadence is running rather than
    # a single write having happened by luck. The fixture streams for
    # ~6.6s, so this leaves several seconds of stream still to come.
    $elapsed = Wait-ForSql -Database $db -TimeoutSeconds 20 `
        -Query "select count(*) from message_checkpoints where updated_at > created_at;" `
        -Until { param($v) $v -eq "1" }

    Check "a checkpoint was written and updated before the kill" ($null -ne $elapsed) `
        "nothing on disk within 20s -- if the stream never started this is a setup failure, and if it streamed without checkpointing this is the mechanism Done-when #3 rests on being broken"
    if ($null -ne $elapsed) {
        Write-Host ("       checkpoint on disk and updated at +{0:N2}s" -f $elapsed)
    }

    $taskBefore = Invoke-Sql -Database $db -Query `
        "select status from tasks where session_id='$sid' order by created_at desc limit 1;"
    Check "the task is still RUNNING at the moment of the kill" ($taskBefore -eq "running") `
        "status=[$taskBefore] -- killing a task that already finished would test nothing"

    Stop-Process -Id $proc.Id -Force
    $proc.WaitForExit(10000) | Out-Null
    Check "the process is gone" ($proc.HasExited) "pid $($proc.Id) still alive"

    Write-Host "`n[2] on disk, before anything restarts" -ForegroundColor Cyan
    $checkpoints = Invoke-Sql -Database $db -FailedVar sqlFailed -ErrorTextVar sqlError `
        -Query "select count(*) from message_checkpoints;"
    Check "the checkpoint query itself succeeded" (-not $sqlFailed) `
        "sqlite3 said: $sqlError -- a locked database or a bad path is not a product failure, and a gate that cannot tell the difference reports the wrong one"
    Check "a checkpoint survived the kill" ($checkpoints -eq "1") `
        "count=[$checkpoints] -- nothing ran on the way out, so this row is the only copy of that text"

    $partial = Invoke-Sql -Database $db -Query "select content from message_checkpoints limit 1;"
    Check "the checkpoint holds the text that was streamed" ($partial -like "*word01*") `
        "content=[$partial]"

    $assistantRows = Invoke-Sql -Database $db -Query `
        "select count(*) from messages where session_id='$sid' and role='assistant';"
    Check "no assistant row exists yet" ($assistantRows -eq "0") `
        "count=[$assistantRows] -- the terminal event never fired, so nothing promoted it yet"

    $taskState = Invoke-Sql -Database $db -Query `
        "select status from tasks where session_id='$sid' order by created_at desc limit 1;"
    Check "the task row still claims to be running" ($taskState -eq "running") `
        "status=[$taskState] -- a task cannot survive the process that ran it, and nothing has buried it yet"

    Write-Host "`n[3] restart: crash recovery runs" -ForegroundColor Cyan
    # Any entry point that reaches build_runtime runs recovery. `tasks
    # list` is the one that also shows the result.
    & $py cli.py tasks list --limit 5

    $taskState = Invoke-Sql -Database $db -Query `
        "select status from tasks where session_id='$sid' order by created_at desc limit 1;"
    $taskCode = Invoke-Sql -Database $db -Query `
        "select coalesce(error_code,'') from tasks where session_id='$sid' order by created_at desc limit 1;"
    Check "the previously-RUNNING task is FAILED" ($taskState -eq "failed") "status=[$taskState]"
    Check "with error_code backend_died" ($taskCode -eq "backend_died") "error_code=[$taskCode]"

    $row = Invoke-Sql -Database $db -Query `
        "select interrupted || '|' || coalesce(outcome,'') || '|' || coalesce(error_code,'') || '|' || length(content) from messages where session_id='$sid' and role='assistant' order by seq desc limit 1;"
    $parts = @($row -split '\|')
    Check "a partial assistant message now exists" ($parts.Count -eq 4) "row=[$row]"
    if ($parts.Count -eq 4) {
        Check "interrupted = 1" ($parts[0] -eq "1") "interrupted=[$($parts[0])]"
        Check "outcome = failed" ($parts[1] -eq "failed") "outcome=[$($parts[1])]"
        Check "error_code = backend_died" ($parts[2] -eq "backend_died") "error_code=[$($parts[2])]"
        Check "its content is non-empty" (($parts[3] -as [int]) -gt 0) "length=[$($parts[3])]"
    }

    $text = Invoke-Sql -Database $db -Query `
        "select content from messages where session_id='$sid' and role='assistant' order by seq desc limit 1;"
    Check "the content is the text that was actually streamed" ($text -like "*word01*") "content=[$text]"

    $checkpoints = Invoke-Sql -Database $db -Query "select count(*) from message_checkpoints;"
    Check "the checkpoint was consumed by the promotion" ($checkpoints -eq "0") `
        "count=[$checkpoints] -- a leftover would be promoted again on the next start"

    Write-Host "`n[4] tasks.list is correct immediately after the restart" -ForegroundColor Cyan
    $listed = (& $py cli.py tasks list --limit 5 | Out-String)
    Check "the buried task is reported as failed/backend_died" `
        ($listed -match "failed" -and $listed -match "backend_died") `
        "tasks list said:`n$listed"

    Write-Host "`n[5] the recovered turn is replayable" -ForegroundColor Cyan
    $replay = (& $py cli.py session replay --session $sid --since-seq 0 | Out-String)
    Check "replay shows the interrupted partial" `
        ($replay -match "interrupted" -and $replay -match "word01") "replay said:`n$replay"
}
finally {
    Remove-Item -LiteralPath $scratch -Recurse -Force -ErrorAction SilentlyContinue
}

if ($script:failures.Count -eq 0) {
    Write-Host "`nALL CHECKS PASSED" -ForegroundColor Green
    exit 0
} else {
    Write-Host "`nFAILED: $($script:failures -join '; ')" -ForegroundColor Red
    exit 1
}
