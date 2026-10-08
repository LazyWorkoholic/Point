# F18 verification gate, part 1 of 4: build the scratch profile and both
# fixture sessions. Parts 2-4 are eyes-on app runs and cannot be a script --
# see design/tasks/frontend/F18-windowed-history.md's gate, or the handoff
# this episode printed.
#
# SAFETY. APPDATA and LOCALAPPDATA are overridden for this whole script, so
# Point's own process only ever resolves the scratch directory
# (backend\appconfig.py's config_dir()/state_dir(),
# frontend\internal\config\config.go's ConfigDir()/StateDir() -- the same two
# env vars on both sides, by design, so one override moves both). The real
# %APPDATA%\Point and %LOCALAPPDATA%\Point are never NAMED in any call here,
# destructive or otherwise. The only Remove-Item target is $scratch.
#
# The scratch is deliberately NOT removed in a finally block, because parts
# 2-4 run against it from their own shells (shell state, env vars included,
# does not survive between invocations -- which is why every part sets the
# two variables itself). Part 4 is the teardown.
#
# Usage, from anywhere:
#     powershell -ExecutionPolicy Bypass -File D:\Projects\Point\backend\tests\manual_f18_verify.ps1
#
# Add -Small to build a 2,000-message session instead of 50,000 when you just
# want to re-check the mechanics without waiting on the seed.

param(
    [switch] $Small
)

$ErrorActionPreference = "Stop"

$scratch = Join-Path $env:TEMP "point-scratch-f18"
$env:APPDATA      = Join-Path $scratch "roaming"
$env:LOCALAPPDATA = Join-Path $scratch "local"
$db = Join-Path $env:LOCALAPPDATA "Point\runtime.db"

Remove-Item -LiteralPath $scratch -Recurse -Force -ErrorAction SilentlyContinue
New-Item -ItemType Directory -Force $env:APPDATA, $env:LOCALAPPDATA | Out-Null

$messages = 50000
if ($Small) { $messages = 2000 }

Write-Host "scratch profile : $scratch"
Write-Host "seed size       : $messages messages"

Set-Location D:\Projects\Point\backend

uv run python cli.py migrate
if ($LASTEXITCODE -ne 0) { throw "migrate failed" }

# -------------------------------------------------------------------------
# Session A: the memory proof. Plain alternating rows, no task_id anywhere.
# -------------------------------------------------------------------------
Write-Host "`n=== session A: $messages seeded messages (the memory proof) ==="
$seedOut = uv run python cli.py seed-session --messages $messages --title "f18 memory proof"
$seedOut
if ($LASTEXITCODE -ne 0) { throw "seed-session failed" }
$sessA = ([regex]::Match(($seedOut -join "`n"), '[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}')).Value
if (-not $sessA) { throw "could not parse session A's id out of seed-session's output" }

# -------------------------------------------------------------------------
# Session B: the pairing-at-a-window-edge proof.
# -------------------------------------------------------------------------
Write-Host "`n=== session B: the pairing fixtures ==="
$newOut = uv run python cli.py session new "f18 pairing" --provider demo --model demo --no-project
$newOut
if ($LASTEXITCODE -ne 0) { throw "session new failed" }
$sessB = ([regex]::Match(($newOut -join "`n"), '[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}')).Value
if (-not $sessB) { throw "could not parse session B's id" }

# A REAL background research turn, so at least one collapsed slot in the
# transcript was produced by the live path rather than written by hand --
# real task_id on both rows, real invocation, real outcome. It lands at the
# very oldest end of this session, which also puts a decorated exchange at
# the one window edge that has no batch beyond it.
Write-Host "`n--- a real @research send against demo (waited out) ---"
uv run python cli.py send "@research what does this project do" --session $sessB --wait
Write-Host "send exit = $LASTEXITCODE"

# The three exchanges a real send cannot produce on demand. See the script's
# own docstring for what each one is for.
Write-Host "`n--- the hand-built exchanges ---"
uv run python tests\manual_f18_fixture.py $sessB
if ($LASTEXITCODE -ne 0) { throw "the fixture builder reported a failed self-check" }

# -------------------------------------------------------------------------
# The provider fix. seed-session and `session new` both resolve their
# provider through cli.py's default_provider(), which answers "ollama if it
# is registered AND ANSWERING, else echo" -- and `echo` is deliberately not a
# registered provider. chat\protocol.go's onSessionList skips any session
# pinned to a provider the backend did not register, and ollama/demo always
# register, so w.providers is never nil and an echo-pinned session is SKIPPED:
# the app would bootstrap a fresh empty session and this entire gate would
# measure nothing.
#
# Whether that bites depends on whether the Ollama daemon happens to be up
# when the seed runs, which is exactly the kind of thing that works until it
# does not. Pinning both sessions to `demo` makes the gate deterministic
# either way. seed-session itself is untouched.
# -------------------------------------------------------------------------
Write-Host "`n--- pinning both sessions to a provider the app will accept ---"
sqlite3 "$db" "UPDATE sessions SET provider='demo', model='demo';"
if ($LASTEXITCODE -ne 0) { throw "the provider update failed" }
sqlite3 -header "$db" "SELECT substr(id,1,8) AS id, provider, model, title FROM sessions;"

# -------------------------------------------------------------------------
# Self-checks on the fixtures themselves. A gate that trusts a fixture it
# never looked at is B18's own "green while verifying almost nothing",
# one layer down.
# -------------------------------------------------------------------------
Write-Host "`n--- fixture self-checks ---"
$failures = @()
function Check($ok, $what) {
    if ($ok) { Write-Host "  ok    $what" }
    else { Write-Host "  FAIL  $what"; $script:failures += $what }
}

$aStats = (sqlite3 "$db" "SELECT COUNT(*)||'|'||MIN(seq)||'|'||MAX(seq)||'|'||COUNT(DISTINCT seq) FROM messages WHERE session_id='$sessA';") -split '\|'
Check ([int]$aStats[0] -eq $messages) "session A holds $messages rows (got $($aStats[0]))"
Check ([int]$aStats[1] -eq 1 -and [int]$aStats[2] -eq $messages -and [int]$aStats[3] -eq $messages) `
      "session A's seqs are contiguous 1..$messages with no gaps or dupes"
$aTasks = sqlite3 "$db" "SELECT COUNT(*) FROM messages WHERE session_id='$sessA' AND task_id IS NOT NULL;"
Check ([int]$aTasks -eq 0) "session A has no task_id on any row (so it exercises the memory path only)"

$bOutcomes = sqlite3 "$db" "SELECT COUNT(*) FROM messages WHERE session_id='$sessB' AND outcome='failed';"
Check ([int]$bOutcomes -ge 1) "session B has a failed-after-partial-output assistant row"
$bOrphan = sqlite3 "$db" "SELECT COUNT(*) FROM tasks t WHERE t.status='cancelled' AND NOT EXISTS (SELECT 1 FROM messages m WHERE m.task_id=t.id AND m.role='assistant');"
Check ([int]$bOrphan -ge 1) "session B has a cancelled task with no assistant row at all"
# json_extract rather than a LIKE pattern: the stored shape is
# {"metadata": ..., "invocation": ...} (session_repository.py's
# insert_message), and a LIKE pattern for it needs embedded double quotes
# that PowerShell mangles on the way to sqlite3. Built as a single-quoted
# PowerShell string so neither $ nor a quote is touched.
$slotSql = "SELECT COUNT(*) FROM messages WHERE session_id='$sessB' AND role='assistant' AND json_extract(metadata, '" + '$.invocation.agent' + "') = 'research';"
$bSlots = sqlite3 "$db" $slotSql
Check ([int]$bSlots -ge 2) "session B has at least two research answers that must reload as collapsed slots"

if ($failures.Count -gt 0) {
    Write-Host "`nFIXTURES FAILED: $($failures -join '; ')"
    exit 1
}

Write-Host "`n================================================================"
Write-Host "FIXTURES OK"
Write-Host ""
Write-Host "session A (memory proof) : $sessA"
Write-Host "session B (pairing)      : $sessB"
Write-Host "scratch profile          : $scratch"
Write-Host "database                 : $db"
Write-Host ""
Write-Host "Session B is the most recently updated, so the app opens it."
Write-Host "To make the app open session A instead, run:"
Write-Host ""
Write-Host "  `$env:LOCALAPPDATA = `"$($env:LOCALAPPDATA)`"; sqlite3 `"$db`" `"UPDATE sessions SET updated_at = (SELECT MAX(updated_at)+1 FROM sessions) WHERE id='$sessA';`""
Write-Host ""
Write-Host "Now run part 2 (the memory proof). See the episode handoff."
Write-Host "================================================================"
exit 0
