# F9d (Session 19b) -- Apply-Time Guard Order: manual verification
#
#   powershell -NoProfile -ExecutionPolicy Bypass -File D:\Projects\Point\backend\tests\gate_f9d.ps1
#
# Run it from anywhere. It creates ONE scratch profile, points both APPDATA
# and LOCALAPPDATA at it for the whole script, copies the shipped sample
# project into it to work on, and removes the lot at the end. Nothing under
# the real %APPDATA%\Point or %LOCALAPPDATA%\Point is ever named by this
# script, destructively or otherwise -- CLAUDE.md's env-var-override
# pattern. The retired rename-aside pattern is NOT used.
#
# ---------------------------------------------------------------------------
# NO BACKTICK INSIDE A DOUBLE-QUOTED STRING IN THIS FILE. EVER.
# ---------------------------------------------------------------------------
#
# The first version of this gate did not parse, and the reason is worth
# recording because it is invisible when you read the line:
#
#     Expect @("ctrl+s reports `saved app.py`")
#
# In PowerShell the BACKTICK IS THE ESCAPE CHARACTER, so the trailing one
# escaped the closing quote. The string then ran on through the following
# lines until it found another quote, and the parser reported "Unexpected
# token 'editor:' in expression or statement" 4 lines later plus a missing
# string terminator 50 lines after that -- two errors, neither of them at
# the line that caused either, and both pointing at code that is correct.
#
# It was NOT a Unicode smart quote and NOT a write-mechanism corruption.
# Checked with Format-Hex and again by reading the raw bytes: the only
# non-ASCII byte in this file is the BOM at offset 0, which is deliberate
# (see below). The mistake was writing Markdown emphasis into a PowerShell
# string literal, which is a habit this file now has to not have.
#
# Quote a name inside a message with doubled double-quotes, or with single
# quotes, or with nothing at all. Backticks are fine in COMMENTS (there are
# several above) and at the END of a line as the continuation operator
# (there are several below). Only a backtick inside a string is the trap.
#
# ---------------------------------------------------------------------------
# What this episode changed, which is what there is to verify
# ---------------------------------------------------------------------------
#
# F9a's hazard 1: "resolve the SESSION's scope first, guard against that,
# and commit nothing until the guard passes." F15 added the first caller of
# WriteProjectFile from an agent-applied diff, which made the hazard live.
#
#   backend   _handle_write_project_file accepts `session_id` and resolves
#             the SESSION's own project (_attachment_scope). `project_id`
#             in the payload is ignored outright when it is present, and a
#             session with no project is REFUSED rather than downgraded to
#             the path walk-up.
#   frontend  applyNow records the proposal's session on the document
#             (doc.agentSession); saveDoc sends it as `session_id` and
#             sends no `project_id` beside it. Every other save is
#             unchanged.
#
# ---------------------------------------------------------------------------
# WHAT THIS GATE CANNOT COVER BY HAND, and where it is covered instead
# ---------------------------------------------------------------------------
#
# The drift itself -- a file whose own `.point\` belongs to a DIFFERENT
# project from the session that proposed the region -- has no path through
# the UI. The Editor's tree lists one project, and its session is bound to
# the project it resolved from the working directory, so there is no
# keystroke sequence that produces the mismatch. It is covered by
# backend\tests\manual_f9d_check.py (Step 1 below), which drives real
# WriteProjectFile events through a real Runtime against two real projects
# and asserts the wrong-scope write both ways: silently creating a file
# under the wrong project without a session, and answered about the right
# file with one.
#
# So Step 3 here is not the proof that the scope is CORRECT -- it is the
# proof that the real app, through a real apply and a real ctrl+s, sends
# the session scope at all rather than the path-resolved project. Those are
# two different claims and both are needed.
#
# Two things about this file's own bytes, both gate_f15's findings:
# UTF-8 WITH a BOM and ASCII content (PowerShell 5.1 decodes a BOM-less
# UTF-8 gate as cp1252 and produced ten parse errors before a line ran),
# and $ErrorActionPreference = Continue, because the backend logs to stderr
# by design and Stop would promote that to terminating. Safety comes from
# the two env-var overrides, not from the preference.

$ErrorActionPreference = "Continue"

$repo    = "D:\Projects\Point"
$scratch = Join-Path $env:TEMP "point-scratch-f9d-gate"
$work    = Join-Path $scratch "work"
$appPy   = Join-Path $work "app.py"
$exe     = Join-Path $scratch "point.exe"

$script:failures = @()

function Assert-That($ok, $what) {
    if ($ok) {
        Write-Host "  PASS  $what" -ForegroundColor Green
    } else {
        Write-Host "  FAIL  $what" -ForegroundColor Red
        $script:failures += $what
    }
}
function Step($n, $title) {
    Write-Host ""
    Write-Host ("=" * 72) -ForegroundColor DarkGray
    Write-Host " STEP $n -- $title" -ForegroundColor Cyan
    Write-Host ("=" * 72) -ForegroundColor DarkGray
}
function Expect($lines) {
    foreach ($l in $lines) { Write-Host "  EXPECT    $l" -ForegroundColor Green }
}
function Do-This($lines) {
    foreach ($l in $lines) { Write-Host "  DO        $l" -ForegroundColor White }
}
function Launch() {
    Write-Host ""
    Write-Host "  launching Point from $work (quit with ctrl+q or F3 when the step is done)" -ForegroundColor DarkGray
    Push-Location $work
    & $exe
    Pop-Location
}
function FrontendLog() {
    return (Join-Path $scratch "Point\logs\frontend.log")
}
function LogLines($pattern) {
    $p = FrontendLog
    if (-not (Test-Path $p)) { return @() }
    return (Select-String -LiteralPath $p -Pattern $pattern -SimpleMatch | ForEach-Object { $_.Line })
}

# ScopeOf pulls the scope VALUE out of one write log line, and it exists so
# that NO assertion below has to know how the logger quotes it.
#
# The first version of this gate had four different regexes for the same
# field and two of them were wrong in the same way:
#
#     scope=.*session .*agent-applied region      Step 3 -- passed
#     scope=.*(project |path walk-up)             Step 3 -- passed
#     scope=project                               Step 4 -- NEVER MATCHED
#     session                                     Step 4 -- far too loose
#
# All three scope values contain a space ("project <uuid>", "session <id>
# (agent-applied region)", "path walk-up"), so the Go logger quotes the
# value: the line reads scope="project 400f5426-...". The two Step 3
# patterns survived that only because their .* happened to swallow the
# opening quote -- tolerance by accident, not by design. Step 4's literal
# scope=project could never match a correct run, so it reported FAIL over
# behaviour that was right, which is the worst failure mode an assertion
# has. And Step 4's bare "session" would have matched the word anywhere on
# the line, including inside a file path.
#
# One parser, four comparisons against its result. A change to the logger's
# quoting breaks this function and nothing else.
function ScopeOf($line) {
    $m = [regex]::Match($line, 'scope=(?:"([^"]*)"|(\S+))')
    if (-not $m.Success) { return "" }
    if ($m.Groups[1].Success) { return $m.Groups[1].Value }
    return $m.Groups[2].Value
}

try {
    # -----------------------------------------------------------------
    Step 0 "the Go gate -- gofmt, vet, the whole frontend suite"
    # -----------------------------------------------------------------
    # frontend\internal\editor\guardorder_test.go is this episode's own
    # surface: which scope each of the three cases sends, that a refused
    # apply records nothing, that a reload drops it, that autosave carries
    # the same scope, and that a refusal is readable.
    Push-Location $repo
    $unformatted = (gofmt -l .\frontend\)
    Assert-That ([string]::IsNullOrWhiteSpace($unformatted)) "gofmt -l .\frontend\ is clean"
    if (-not [string]::IsNullOrWhiteSpace($unformatted)) { Write-Host $unformatted }

    go vet .\frontend\...
    Assert-That ($LASTEXITCODE -eq 0) "go vet .\frontend\... is clean"

    go test .\frontend\... -count=1
    Assert-That ($LASTEXITCODE -eq 0) "go test .\frontend\... -count=1 is green"
    Pop-Location

    # -----------------------------------------------------------------
    Step 1 "the backend half -- two real projects, real WriteProjectFile events"
    # -----------------------------------------------------------------
    New-Item -ItemType Directory -Force $scratch | Out-Null
    New-Item -ItemType Directory -Force $work | Out-Null
    $env:APPDATA      = $scratch
    $env:LOCALAPPDATA = $scratch

    Push-Location (Join-Path $repo "backend")
    uv run python cli.py migrate
    uv run python tests\manual_f9d_check.py
    $pyOK = ($LASTEXITCODE -eq 0)
    Pop-Location
    Assert-That $pyOK "manual_f9d_check.py -- all checks passed"

    # Also B36's own script, unchanged by this episode and re-run here
    # because this episode uses its primitive: if _attachment_scope's
    # resolution rule has moved, both events are wrong together.
    Push-Location (Join-Path $repo "backend")
    uv run python tests\manual_b36_check.py
    $b36OK = ($LASTEXITCODE -eq 0)
    Pop-Location
    Assert-That $b36OK "manual_b36_check.py still passes (B36's primitive is intact)"

    # -----------------------------------------------------------------
    Step 2 "the work copy and the build"
    # -----------------------------------------------------------------
    $env:POINT_BACKEND_VERBOSE = "1"
    # Demo mode is the only reliable producer of a valid proposal -- B24
    # measured the local model at 0/5, with a prompt deliberately
    # untouched since. gate_f15's header has the full account.
    #
    # POINT_BACKEND_DIR is stated rather than discovered: the upward walk
    # for backend\main.py cannot cross a volume root, and this gate's cwd
    # and exe both live under $env:TEMP, which need not be on the repo's
    # drive. gate_f15 lost a run to exactly that.
    $env:POINT_BACKEND_DIR = (Join-Path $repo "backend")

    Copy-Item (Join-Path $repo "backend\demo\sample_project\*") $work -Recurse -Force
    if (Test-Path (Join-Path $work ".point")) {
        Remove-Item -LiteralPath (Join-Path $work ".point") -Recurse -Force
    }
    Assert-That (Test-Path $appPy) "app.py copied into the work directory"

    # greet() must still be at lines 4-8, or B24 rejects the demo
    # fixture's proposal and there is no card to apply.
    $lines = Get-Content -LiteralPath $appPy
    Assert-That ($lines[3] -match '^def greet') "app.py line 4 still opens greet()"
    Assert-That ($lines[7] -match 'Hello') "app.py line 8 is still greet's last line"

    Push-Location (Join-Path $repo "backend")
    $projLine = (uv run python cli.py project init --path $work)
    Pop-Location
    Write-Host "  project: $projLine" -ForegroundColor DarkGray
    Assert-That (Test-Path (Join-Path $work ".point")) "the work copy is a registered project"

    Push-Location $repo
    go build -o $exe .\frontend\cmd\point
    Pop-Location
    Assert-That (Test-Path $exe) "point.exe built into the scratch directory"

    # -----------------------------------------------------------------
    Step 3 "the real app: an agent-applied region, saved"
    # -----------------------------------------------------------------
    Do-This @(
        "Settings -> Providers -> pick DEMO as the provider, then close Settings.",
        "Open the Editor workspace and open app.py from the tree.",
        "Put the caret on LINE 6 (inside greet).",
        "Press the leader (F2 or ctrl+w) then f, type anything, submit.",
        "Wait for the ghost, then the diff card. Press Enter to apply.",
        "Press ctrl+s to SAVE, then quit with F3 (or ctrl+q)."
    )
    Expect @(
        "the card applies into the buffer and closes; the status line names ctrl+z and ctrl+s",
        "ctrl+s reports: saved app.py"
    )
    Launch

    $writes = LogLines "editor: WriteProjectFile sent"
    Write-Host ""
    foreach ($l in $writes) { Write-Host "  $l" -ForegroundColor DarkGray }
    # The PARSED scope beside each raw line, so a failure below is readable
    # without re-deriving what the assertion saw.
    foreach ($l in $writes) { Write-Host ("  scope parsed as: [{0}]" -f (ScopeOf $l)) -ForegroundColor DarkGray }

    Assert-That ($writes.Count -ge 1) "a WriteProjectFile was actually sent"
    $scoped = @($writes | Where-Object { (ScopeOf $_) -like 'session *(agent-applied region)' })
    Assert-That ($scoped.Count -ge 1) `
        "the save after the apply was scoped to a SESSION, not to a path-resolved project"
    $pathScoped = @($writes | Where-Object { (ScopeOf $_) -like 'project *' -or (ScopeOf $_) -eq 'path walk-up' })
    if ($pathScoped.Count -ge 1) {
        Write-Host "  note: $($pathScoped.Count) write(s) used the ordinary project scope -- that is" -ForegroundColor DarkGray
        Write-Host "        correct for any save of a file no agent region was applied into." -ForegroundColor DarkGray
    }
    Assert-That ((Get-Content -LiteralPath $appPy -Raw) -notmatch '<path>') `
        "app.py does not contain the prompt's own placeholder (a bad proposal would show here)"

    # -----------------------------------------------------------------
    Step 4 "an ORDINARY save is still scoped by the project"
    # -----------------------------------------------------------------
    Do-This @(
        "Launch again. Open a DIFFERENT file from the tree (not app.py).",
        "Type one character, press ctrl+s, then quit."
    )
    Expect @("ctrl+s reports: saved <that file>")
    Launch

    $writes2 = LogLines "editor: WriteProjectFile sent"
    $new = @($writes2 | Select-Object -Skip $writes.Count)
    Write-Host ""
    foreach ($l in $new) { Write-Host "  $l" -ForegroundColor DarkGray }
    foreach ($l in $new) { Write-Host ("  scope parsed as: [{0}]" -f (ScopeOf $l)) -ForegroundColor DarkGray }
    Assert-That ($new.Count -ge 1) "the ordinary save reached the wire"
    Assert-That (@($new | Where-Object { (ScopeOf $_) -like 'project *' }).Count -ge 1) `
        "an ordinary save still names the PROJECT -- F9d did not re-scope every write"
    Assert-That (@($new | Where-Object { (ScopeOf $_) -like 'session *' }).Count -eq 0) `
        "and it sends no session at all"
}
finally {
    Write-Host ""
    Write-Host ("=" * 72) -ForegroundColor DarkGray
    if ($script:failures.Count -eq 0) {
        Write-Host " F9D GATE: every check passed" -ForegroundColor Green
    } else {
        Write-Host " F9D GATE: $($script:failures.Count) FAILED" -ForegroundColor Red
        foreach ($f in $script:failures) { Write-Host "   - $f" -ForegroundColor Red }
    }
    Write-Host ""
    Write-Host " NOT COVERED HERE (by design -- see this file's header):" -ForegroundColor Yellow
    Write-Host "   the cross-project mismatch itself has no path through the UI; it is" -ForegroundColor Yellow
    Write-Host "   Step 1's manual_f9d_check.py that drives it, both directions." -ForegroundColor Yellow
    Write-Host ("=" * 72) -ForegroundColor DarkGray

    if (Test-Path $scratch) {
        Remove-Item -LiteralPath $scratch -Recurse -Force
    }
}
