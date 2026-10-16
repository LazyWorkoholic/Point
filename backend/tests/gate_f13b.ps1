# F13b (Session 18b) -- Editor: Save, Locks, Autosave: manual verification
#
#   powershell -NoProfile -ExecutionPolicy Bypass -File D:\Projects\Point\backend\tests\gate_f13b.ps1
#
# Run it from anywhere. It creates ONE scratch profile, points both APPDATA
# and LOCALAPPDATA at it for the whole script, generates its fixtures inside
# the repo, and removes both at the end. Nothing under the real
# %APPDATA%\Point or %LOCALAPPDATA%\Point is ever named by this script,
# destructively or otherwise -- CLAUDE.md's env-var-override pattern. The
# retired rename-aside pattern is NOT used.
#
# It stops at each step. Read the EXPECT lines before pressing Enter.
#
# ---------------------------------------------------------------------------
# What this gate covers
# ---------------------------------------------------------------------------
#
# F13's Done-when 2, 7, 8, 9, 10, 11, 12 -- the save half. Together with
# gate_f13a.ps1 (3, 4, 5, 6 and the open half of 1) that is all twelve, and
# a green run of BOTH is what closes F13.
#
# It also covers the save half of Done-when 1 and the five decisions this
# episode was given on top of its doc: C1 (the three-button overlay), C2
# (closing [diff] returns to it), C3 (the dirty-close Confirm), C4 (the
# mixed-ending write tiebreak) and C5 (the per-document autosave timer).
#
# ---------------------------------------------------------------------------
# Two things about this file's own bytes
# ---------------------------------------------------------------------------
#
# 1. It is written UTF-8 WITH a BOM, and its content is ASCII. gate_f13a's
#    first draft was UTF-8 without one, so PowerShell 5.1 decoded its
#    em-dashes as cp1252 and hit a stray quote character: ten parse errors
#    before a single line ran. Belt and braces -- the BOM makes the
#    encoding explicit and the ASCII means it would survive without it.
#
# 2. $ErrorActionPreference is Continue, not Stop. B32's finding: in
#    Windows PowerShell 5.1 a native command that writes to stderr
#    produces a NativeCommandError, which Stop promotes to terminating.
#    This script runs cli.py, which logs to stderr BY DESIGN (CLAUDE.md:
#    stdout is the protocol channel), and it deliberately provokes
#    refusals. Under Stop a routine log line aborts the run and it looks
#    like a crash. Safety comes from the two env-var overrides and from
#    never naming the real Point folder, not from this setting.
#
#    The cost of Continue is that an unrelated failure prints and falls
#    through, so every non-interactive step below checks its own exit code
#    or value through Assert-That.

$ErrorActionPreference = "Continue"

$repo     = "D:\Projects\Point"
$scratch  = Join-Path $env:TEMP "point-scratch-f13b"
$fixtures = Join-Path $repo "backend\tests\fixtures\f13b"
$holderB  = "someone-else@other-id"

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
function Fails($lines) {
    foreach ($l in $lines) { Write-Host "  FAILS IF  $l" -ForegroundColor Yellow }
}
function Do-This($lines) {
    foreach ($l in $lines) { Write-Host "  DO        $l" -ForegroundColor White }
}
function Pause($msg) {
    Write-Host ""
    Read-Host "  $msg -- press Enter when done"
}
function Launch() {
    Write-Host ""
    Write-Host "  launching Point (quit with ctrl+q when the step is done)" -ForegroundColor DarkGray
    Push-Location $repo
    go run .\frontend\cmd\point
    Pop-Location
}
# LaunchBg starts Point as a SEPARATE PROCESS and returns immediately, so
# this script keeps running while Point is still up.
#
# Used by STEP 4 ONLY. Every other step uses the blocking Launch above,
# which is the right shape for "do these things, then quit, then I check
# the result".
#
# Step 4 cannot be that shape, and the reason is worth stating because the
# obvious alternative does not work. Done-when 8 needs the file to change
# on disk WHILE a live session is holding the ORIGINAL mtime as its
# expected_mtime. Splitting the step into two blocking launches -- open and
# edit and quit, then modify the file, then reopen and save -- relocates
# the bug instead of fixing it: the SECOND open reads the file after the
# change has already landed, so its baseline is the post-change mtime,
# ctrl+s finds no divergence, and the stale refusal never fires. The gate
# would go green having tested nothing.
#
# The child inherits this process's environment block, which is what keeps
# the scratch-profile rule intact -- APPDATA and LOCALAPPDATA are already
# redirected here, so Point in that window reads the scratch profile and
# never the real one. Verified directly rather than assumed: a probe child
# reported both variables pointing at the scratch directory and its working
# directory as the repo.
#
# ArgumentList is ONE pre-quoted string, not an array. PowerShell 5.1 joins
# an array with spaces and does not reliably quote the elements, which would
# split "go run .\frontend\cmd\point" into three arguments.
function LaunchBg() {
    Write-Host ""
    Write-Host "  launching Point IN A SEPARATE WINDOW -- this one keeps going" -ForegroundColor DarkGray
    $argLine = '-NoProfile -ExecutionPolicy Bypass -Command "go run .\frontend\cmd\point"'
    return Start-Process -FilePath "powershell.exe" -ArgumentList $argLine `
        -WorkingDirectory $repo -PassThru
}
function Sha($path) {
    return (Get-FileHash -Algorithm SHA256 -LiteralPath $path).Hash
}
function Db() {
    return (Join-Path $scratch "Point\runtime.db")
}
# Sql runs one statement against the scratch database. sqlite3 is on PATH
# per CLAUDE.md. Used to READ lock rows and to BACKDATE a heartbeat, which
# is how the 60-second staleness window is exercised without a 60-second
# wait -- the alternative is a step whose instruction is "now wait a
# minute", which is the kind of step people skip.
function Sql($statement) {
    return (sqlite3 (Db) $statement)
}
# HoldLock keeps the OTHER holder's lock alive for as long as a step
# needs, and is the fix for the one defect that survived two attempts.
#
# B7's LOCK_STALE_AFTER_S is 60 and a cli.py lock is heartbeated by
# NOTHING -- that process exits immediately. So the row ages from the
# moment it is taken, and once it is past sixty seconds the editor's own
# AcquireFileLock takes it over BY DESIGN. Measured against this repo's
# backend:
#
#   fresh row, editor acquires -> "held by someone-else@other-id (1s)"
#   row aged 120s, same call   -> "ok", and the row becomes the editor's
#
# The first version of these steps acquired the lock and then ran a
# human-paced manual step against that window; the second acquired it
# later, which moved where the window starts and left the navigation gap
# inside it. Both lose the race whenever a person takes a minute to find
# a file in a 389-entry tree, and when they lose it the editor looks
# like it has stopped enforcing locks at all. It has not: it is taking
# over a lock that really is stale.
#
# So the lock is held OPEN by a background process that heartbeats it
# every 15 seconds -- four beats inside the window -- for the whole of
# the step. Nothing then depends on how fast the person is.
#
# cli.py deliberately does not take the single-instance lock (B32's
# checks 6-8 rely on that), so this can run alongside Point. It
# inherits this shell's APPDATA/LOCALAPPDATA, so it works on the same
# scratch runtime.db.
function HoldLock($relFile, $holder) {
    $script = @"
`$env:APPDATA = '$scratch'
`$env:LOCALAPPDATA = '$scratch'
Set-Location '$repo\backend'
while (`$true) {
    uv run python cli.py project lock heartbeat --path '$repo' --file '$relFile' --holder '$holder' | Out-Null
    Start-Sleep -Seconds 15
}
"@
    $enc = [Convert]::ToBase64String([Text.Encoding]::Unicode.GetBytes($script))
    Write-Host "  holding $holder's lock open (heartbeat every 15s)" -ForegroundColor DarkGray
    return Start-Process -FilePath "powershell.exe" `
        -ArgumentList "-NoProfile -ExecutionPolicy Bypass -EncodedCommand $enc" `
        -WindowStyle Hidden -PassThru
}
function StopHold($proc) {
    if ($proc -and -not $proc.HasExited) {
        Stop-Process -Id $proc.Id -Force -ErrorAction SilentlyContinue
        Write-Host "  stopped holding the lock" -ForegroundColor DarkGray
    }
}

New-Item -ItemType Directory -Force $scratch | Out-Null

try {
    $env:APPDATA      = $scratch   # config.toml, credentials.toml, state.json
    $env:LOCALAPPDATA = $scratch   # runtime.db, logs\

    Write-Host "scratch profile: $scratch" -ForegroundColor DarkGray
    Write-Host "fixtures:        $fixtures" -ForegroundColor DarkGray

    # -----------------------------------------------------------------
    Step 0 "setup -- a fresh database, config.toml, and the fixtures"
    # -----------------------------------------------------------------
    #
    # B32's audit finding, inherited: a failed setup made every later
    # check meaningless and stopped nothing. So the setup asserts.

    Push-Location (Join-Path $repo "backend")
    uv run python cli.py migrate | Out-Null
    $migrateExit = $LASTEXITCODE
    Pop-Location
    Assert-That ($migrateExit -eq 0) "cli.py migrate exited 0"
    Assert-That (Test-Path (Db)) "runtime.db is in the SCRATCH profile, not the real one"

    # The repo is itself a Point project (.point\project.json, gitignored),
    # so ProjectResolve from the working directory finds it, and B7's
    # _read_marker_and_resolve recreates the row from the marker in a fresh
    # database. No `project init`, and the real repo is never re-initialised.
    Assert-That (Test-Path (Join-Path $repo ".point\project.json")) `
        "the repo has a .point\project.json for ProjectResolve to find"

    # config.toml, in the scratch profile. [editor] is F13b's own table and
    # the frontend is its only reader; autosave_delay_ms is left at the
    # 1000ms default because Done-when 10's "~1s later" is the number being
    # verified.
    New-Item -ItemType Directory -Force (Join-Path $scratch "Point") | Out-Null
    $confPath = Join-Path $scratch "Point\config.toml"
    @'
[editor]
autosave = "after_delay"
autosave_delay_ms = 1000
'@ | Set-Content -LiteralPath $confPath -Encoding utf8
    Assert-That (Test-Path $confPath) "config.toml written to the scratch profile"

    New-Item -ItemType Directory -Force $fixtures | Out-Null
    $noBom = New-Object Text.UTF8Encoding $false
    $bom   = New-Object Text.UTF8Encoding $true

    # Done-when 2's fixture: CRLF *and* a BOM, which is the pair that
    # cannot survive a save that re-derives either. B32's own round-trip
    # fixture is this shape for the same reason.
    $crlfBom = Join-Path $fixtures "crlf_bom.py"
    [IO.File]::WriteAllText($crlfBom, "def a():`r`n    return 1`r`n", $bom)

    # Plain LF, for the save path's ordinary case.
    $lf = Join-Path $fixtures "lf.py"
    [IO.File]::WriteAllText($lf, "def b():`n    return 2`n", $noBom)

    # C4's fixture. THREE CRLF to TWO LF, so the majority is unambiguous
    # and a save must produce an all-CRLF file.
    $mixed = Join-Path $fixtures "mixed.py"
    [IO.File]::WriteAllText($mixed, "a = 1`r`nb = 2`nc = 3`r`nd = 4`ne = 5`r`n", $noBom)

    # C4's TIE fixture. Two CRLF to two LF -- the one input the read and
    # the write would each have to resolve alone, and the case the
    # tiebreak exists for. A save must produce an all-LF file.
    $tie = Join-Path $fixtures "tie.py"
    [IO.File]::WriteAllText($tie, "a = 1`r`nb = 2`nc = 3`r`nd = 4`n", $noBom)

    # No trailing newline: a one-byte diff every linter notices.
    $noEol = Join-Path $fixtures "no_eol.py"
    [IO.File]::WriteAllText($noEol, "x = 1", $noBom)

    # Done-when 8's victim, and Done-when 7's and 11's.
    $ext = Join-Path $fixtures "external.py"
    [IO.File]::WriteAllText($ext, "p = 1`r`nq = 2`r`n", $noBom)
    $locked = Join-Path $fixtures "locked.py"
    [IO.File]::WriteAllText($locked, "r = 1`r`n", $noBom)

    foreach ($f in @("crlf_bom.py","lf.py","mixed.py","tie.py","no_eol.py",
                     "external.py","locked.py")) {
        Assert-That (Test-Path (Join-Path $fixtures $f)) "fixture $f exists"
    }

    # The mixed fixtures are only meaningful at the counts they claim.
    # B32's gate defect 1 was a fixture too small to test anything whose
    # printed length contradicted its own comment, so these assert.
    $mixedBytes = [IO.File]::ReadAllBytes($mixed)
    $mixedText  = [Text.Encoding]::UTF8.GetString($mixedBytes)
    $mixedCrlf  = ([regex]::Matches($mixedText, "`r`n")).Count
    $mixedLf    = ([regex]::Matches($mixedText, "`n")).Count - $mixedCrlf
    Assert-That ($mixedCrlf -eq 3 -and $mixedLf -eq 2) `
        "mixed.py is 3 CRLF to 2 LF (an unambiguous CRLF majority)"

    $tieBytes = [IO.File]::ReadAllBytes($tie)
    $tieText  = [Text.Encoding]::UTF8.GetString($tieBytes)
    $tieCrlf  = ([regex]::Matches($tieText, "`r`n")).Count
    $tieLf    = ([regex]::Matches($tieText, "`n")).Count - $tieCrlf
    Assert-That ($tieCrlf -eq 2 -and $tieLf -eq 2) `
        "tie.py is an EXACT 2/2 tie (C4's one genuinely ambiguous input)"

    # -----------------------------------------------------------------
    Step "0b" "the Go test suite"
    # -----------------------------------------------------------------
    #
    # Everything assertable without a terminal already is: the write
    # payload, all seven refusal slugs, the lock lifecycle, the heartbeat
    # arithmetic, the per-document autosave timer (C5), the mixed-ending
    # tiebreak (C4) and the holder key's shape. The exit code is READ,
    # which B32's audit calls the worst gap to leave open -- a suite that
    # printed FAIL for every row while the gate carried on reporting PASS.
    Push-Location $repo
    go build ./... | Out-Null
    $buildExit = $LASTEXITCODE
    $fmt = (gofmt -l .\frontend\) -join ""
    go vet ./frontend/... | Out-Null
    $vetExit = $LASTEXITCODE
    go test ./frontend/... | Out-Null
    $testExit = $LASTEXITCODE
    Pop-Location
    Assert-That ($buildExit -eq 0) "go build ./... exited 0"
    Assert-That ($fmt -eq "")      "gofmt -l reports nothing"
    Assert-That ($vetExit -eq 0)   "go vet ./frontend/... exited 0"
    Assert-That ($testExit -eq 0)  "go test ./frontend/... exited 0"

    # -----------------------------------------------------------------
    Step "0c" "the holder key, and A2's erasure trap"
    # -----------------------------------------------------------------
    #
    # F13a's handoff recorded the trap precisely: config\state.go's
    # saveStateTo gives keep-what-is-on-disk treatment to a named set of
    # fields, and chat.Workspace writes the WHOLE struct from its startup
    # snapshot. A Holder generated after that snapshot is taken is erased
    # by chat's next debounced draft save -- ON THE LAUNCH THAT GENERATED
    # IT -- and Point then mints a new identity every time it starts,
    # unable to release or re-acquire its own locks.
    #
    # This step is what proves both halves of the fix are in place. One
    # alone still loses the value.

    $statePath = Join-Path $scratch "Point\state.json"
    Do-This @(
        "Point will launch. Choose the EDITOR (leader ctrl+w, then e).",
        "Open backend\tests\fixtures\f13b\lf.py from the tree.",
        "Then switch to CHAT (ctrl+w w) and TYPE A FEW CHARACTERS in the",
        "  query box -- do not send. That is what fires chat's draft save,",
        "  which is the write that used to erase the holder.",
        "Then quit with ctrl+q."
    )
    Expect @(
        "the status bar in the Editor shows 'lock: you' for lf.py"
    )
    Pause "step 0c"
    Launch
    Assert-That (Test-Path $statePath) "state.json was written"
    $state1 = Get-Content -Raw -LiteralPath $statePath | ConvertFrom-Json
    $holder1 = $state1.holder
    Write-Host "  holder: $holder1" -ForegroundColor DarkGray
    Assert-That (-not [string]::IsNullOrWhiteSpace($holder1)) `
        "state.json has a holder AFTER chat's draft save (A2's erasure trap)"
    Assert-That ($holder1 -match "^.+@[0-9a-f]{32}$") `
        "the holder is <username>@<32 hex> -- the WHOLE composed string"

    Do-This @("Launch again, open the Editor, then quit with ctrl+q.")
    Expect @("nothing visibly different -- this step is about the file, not the UI")
    Pause "step 0c, the relaunch"
    Launch
    $state2 = Get-Content -Raw -LiteralPath $statePath | ConvertFrom-Json
    Assert-That ($state2.holder -eq $holder1) `
        "the holder is the SAME across a relaunch -- never regenerated"

    # -----------------------------------------------------------------
    Step 1 "Done-when 2 -- a CRLF+BOM file round-trips BYTE-IDENTICAL"
    # -----------------------------------------------------------------
    #
    # The check that matters most on Windows, and the reason B32 rewrote
    # read_file to read bytes: Path.read_text() opens in universal-newline
    # mode and had already collapsed every \r\n before the frontend saw a
    # single byte.
    #
    # Hashed rather than diffed. A diff that reads correctly is not
    # evidence against byte loss -- git and every editor normalise line
    # endings on display, which is exactly how this class of bug survives
    # a code review.

    $before = Sha $crlfBom
    $beforeLen = (Get-Item $crlfBom).Length
    Write-Host "  crlf_bom.py: $beforeLen bytes, $before" -ForegroundColor DarkGray

    Do-This @(
        "Open backend\tests\fixtures\f13b\crlf_bom.py in the Editor.",
        "Press ctrl+s WITHOUT EDITING ANYTHING.",
        "Then open lf.py and no_eol.py and save each one unedited too.",
        "Quit with ctrl+q."
    )
    Expect @(
        "the status bar reads CRLF and BOM for crlf_bom.py",
        "'saved crlf_bom.py' on the status line",
        "no dirty marker afterwards"
    )
    Fails @(
        "the status bar says LF for a CRLF file",
        "the BOM segment is missing"
    )
    Pause "step 1"
    Launch

    $after = Sha $crlfBom
    Assert-That ($after -eq $before) `
        "crlf_bom.py is BYTE-IDENTICAL after open + save (Done-when 2)"
    Assert-That ((Get-Item $crlfBom).Length -eq $beforeLen) `
        "crlf_bom.py is the same LENGTH -- no BOM gained or lost"

    $crlfBytes = [IO.File]::ReadAllBytes($crlfBom)
    Assert-That ($crlfBytes[0] -eq 0xEF -and $crlfBytes[1] -eq 0xBB -and $crlfBytes[2] -eq 0xBF) `
        "the BOM is still the first three bytes"
    $crlfStr = [Text.Encoding]::UTF8.GetString($crlfBytes)
    Assert-That (([regex]::Matches($crlfStr, "`r`n")).Count -eq 2) `
        "both line endings are still CRLF"

    $noEolBytes = [IO.File]::ReadAllBytes($noEol)
    Assert-That ($noEolBytes[-1] -ne 0x0A) `
        "no_eol.py still has NO trailing newline -- a one-byte diff preserved"

    # And git agrees, which is Done-when 2's own wording.
    Push-Location $repo
    $dirty = (git status --short -- backend/tests/fixtures/f13b) -join ""
    Pop-Location
    Write-Host "  git status (fixtures): '$dirty'" -ForegroundColor DarkGray
    Assert-That ($dirty -eq "" -or $dirty -match "^\?\?") `
        "git sees no MODIFICATION to the saved fixtures (untracked is fine)"

    # -----------------------------------------------------------------
    Step 2 "C4 -- which ending a MIXED file is written with"
    # -----------------------------------------------------------------
    #
    # B32 reports crlf_count and lf_count and deliberately stops: a mixed
    # file is the one case that CANNOT round-trip byte-exact, because any
    # single ending on the way back rewrites the minority endings. So the
    # choice is which minority to rewrite. C4: the MAJORITY, and LF on an
    # exact tie.
    #
    # The tie half is the part worth verifying by hand. describe_bytes
    # resolves its own ambiguous input -- a file with no line breaks at
    # all -- to "lf", so LF-on-tie is what keeps the read and the write
    # answering the same way about the one input each resolves alone.
    # Writing CRLF on a tie would make the file flip its ending on every
    # single save.

    Do-This @(
        "Open mixed.py. Note the status bar, then press ctrl+s.",
        "Open tie.py. Note the status bar, then press ctrl+s.",
        "Quit with ctrl+q."
    )
    Expect @(
        "mixed.py's status bar reads 'MIXED 3/2' BEFORE the save",
        "tie.py's reads 'MIXED 2/2' before the save",
        "after each save the segment changes to CRLF (mixed) and LF (tie)",
        "it does NOT keep saying MIXED -- the file on disk is now uniform"
    )
    Fails @(
        "the status bar still claims MIXED after a save",
        "either save is refused with an Error (that would be 'mixed' reaching",
        "  the wire, which encode_for_write rejects outright)"
    )
    Pause "step 2"
    Launch

    $mixedAfter = [Text.Encoding]::UTF8.GetString([IO.File]::ReadAllBytes($mixed))
    $mCrlf = ([regex]::Matches($mixedAfter, "`r`n")).Count
    $mLf   = ([regex]::Matches($mixedAfter, "`n")).Count - $mCrlf
    Write-Host "  mixed.py after save: $mCrlf CRLF, $mLf LF" -ForegroundColor DarkGray
    Assert-That ($mLf -eq 0 -and $mCrlf -eq 5) `
        "mixed.py (3 CRLF vs 2 LF) was written ALL CRLF -- the majority"

    $tieAfter = [Text.Encoding]::UTF8.GetString([IO.File]::ReadAllBytes($tie))
    $tCrlf = ([regex]::Matches($tieAfter, "`r`n")).Count
    $tLf   = ([regex]::Matches($tieAfter, "`n")).Count - $tCrlf
    Write-Host "  tie.py after save:   $tCrlf CRLF, $tLf LF" -ForegroundColor DarkGray
    Assert-That ($tCrlf -eq 0 -and $tLf -eq 4) `
        "tie.py (an EXACT 2/2 tie) was written ALL LF -- C4's tiebreak"

    # And it is STABLE: saving again must not flip it back.
    $tieHash = Sha $tie
    Do-This @("Open tie.py again, press ctrl+s, quit.")
    Expect @("the status bar reads LF now, not MIXED")
    Pause "step 2, the second save"
    Launch
    Assert-That ((Sha $tie) -eq $tieHash) `
        "a second save of the resolved tie file changes NOTHING -- no ending flapping"

    # -----------------------------------------------------------------
    Step 3 "Done-when 7 -- a SECOND HOLDER, via cli.py"
    # -----------------------------------------------------------------
    #
    # F13's original step said "open the same file from a second Point
    # instance", and that is unachievable: backend\lockfile.py refuses a
    # second backend against the same profile, and two profiles have two
    # runtime.db files and therefore two separate file_locks tables, so
    # they cannot see each other's locks either. cli.py deliberately does
    # NOT take the single-instance lock, which is why it can be the second
    # holder. B32's own gate uses it this way in checks 6-8. Corrected in
    # B32's handoff before F13a started.

    # This step runs against a LIVE Point, like step 4, and the reason is
    # a defect in its own first version rather than anything about the
    # product.
    #
    # The first version did: cli.py acquires the lock as the other
    # holder -> Pause -> blocking Launch -> the human opens the Editor,
    # finds the file in the tree, opens it, edits it, presses ctrl+s. A
    # cli.py lock is HEARTBEATED BY NOTHING -- that process has already
    # exited -- so the row ages from the moment it is taken. `go run`
    # compiling plus a person navigating a tree is routinely more than
    # B7's LOCK_STALE_AFTER_S of 60 seconds, and once it is past that,
    # the editor's own AcquireFileLock takes the stale row over by
    # design. The status bar then reads 'lock: you', ctrl+s succeeds,
    # and the file is written -- every bit of it correct behaviour,
    # reported as three bugs.
    #
    # Measured rather than reasoned about, against this repo's backend:
    #
    #   fresh row, editor acquires  -> "held by someone-else@other-id (1s)"
    #   row aged 120s, same call    -> "ok", and the row's holder becomes
    #                                  the editor's
    #
    # So the lock is now freshened WHILE Point is up, moments before the
    # save, which removes the human from the timing entirely.
    $lockedRel = "backend/tests/fixtures/f13b/locked.py"
    $lockedHash = Sha $locked

    $proc = LaunchBg
    Do-This @(
        "In the window that just opened: choose the EDITOR.",
        "Do NOT open locked.py yet. Come back to THIS window."
    )
    Pause "step 3a -- Editor open, no file yet"

    # Freshen the other holder's lock now. Re-acquiring as the holder who
    # already owns the row is exactly a heartbeat (acquire_lock's
    # `row["holder"] == holder` branch calls touch_heartbeat), so this
    # resets the 60-second clock to zero with Point already running.
    Push-Location (Join-Path $repo "backend")
    uv run python cli.py project lock acquire `
        --path $repo --file $lockedRel --holder $holderB | Out-Null
    $acqExit = $LASTEXITCODE
    Pop-Location
    Assert-That ($acqExit -eq 0) "cli.py holds the lock as $holderB"

    # ...and KEEP it fresh for the rest of the step, however long the
    # person takes. See HoldLock.
    $hold = HoldLock $lockedRel $holderB

    $row = Sql "SELECT holder FROM file_locks WHERE rel_path LIKE '%locked.py';"
    $age = Sql "SELECT CAST(strftime('%s','now') - heartbeat_at AS INT) FROM file_locks WHERE rel_path LIKE '%locked.py';"
    Write-Host "  file_locks holder: $row (heartbeat ${age}s old)" -ForegroundColor DarkGray
    Assert-That ($row -eq $holderB) "the lock row belongs to the other holder"
    Assert-That ([int]$age -lt 30) `
        "the lock is FRESH -- so the editor cannot legitimately take it over"

    Do-This @(
        "Now open locked.py in the Editor, then press ctrl+o to put the",
        "  keyboard in the code pane. Try to edit it, then press ctrl+s.",
        "Then press f6 to try to take the lock over.",
        "Do not quit. Come back to THIS window."
    )
    Expect @(
        "the status bar names the holder and an age, as",
        "  'held by someone-else (other-id) (Ns ago)'",
        "F13's spec writes it 'held by aryan (12s ago)' -- same shape",
        "ctrl+s is REFUSED and says so; nothing is written",
        "f6 is refused too, saying the lock is STILL LIVE",
        "the segment does NOT end in 'f6 takes over' while it is live"
    )
    Fails @(
        "the file saves anyway",
        "the status bar reads 'lock: you' -- if it does, STOP: the lock aged",
        "  out before you opened the file and this step tested nothing",
        "the holder is shown as a bare id with no username",
        "the whole 32-character id is printed instead of the first eight",
        "f6 steals a LIVE lock"
    )
    Pause "step 3b -- the live-lock refusals seen; Point still running"

    # The heartbeat job's own proof. If this says the lock went stale
    # anyway, HoldLock is not running and every assertion below is about
    # the wrong thing -- so it is checked before them, not after.
    $ageNow = Sql "SELECT CAST(strftime('%s','now') - heartbeat_at AS INT) FROM file_locks WHERE rel_path LIKE '%locked.py';"
    Write-Host "  heartbeat age across the manual step: ${ageNow}s" -ForegroundColor DarkGray
    Assert-That ([int]$ageNow -lt 60) `
        "the other holder's lock stayed FRESH across the whole manual step (the heartbeat job is working)"

    Assert-That ((Sha $locked) -eq $lockedHash) `
        "locked.py is untouched on disk -- the refusal refused (Done-when 7)"
    $stillTheirs = Sql "SELECT holder FROM file_locks WHERE rel_path LIKE '%locked.py';"
    Assert-That ($stillTheirs -eq $holderB) "a LIVE lock was not stolen"

    # --- and now take-over, once it is stale -------------------------
    #
    # Backdated rather than waited out. B7's LOCK_STALE_AFTER_S is 60, and
    # a step whose instruction is "now wait a minute" is a step people
    # skip. The row is moved 120 seconds into the past, which is the same
    # state a crashed Point leaves behind -- the condition take-over
    # actually exists for.
    # Backdated rather than waited out, and done with Point STILL
    # RUNNING so the take-over is exercised in the same session that was
    # just refused. B7's LOCK_STALE_AFTER_S is 60; a step whose
    # instruction is "now wait a minute" is a step people skip.
    # A MARKER, not a hash comparison, and this is the fix for the one
    # assertion that failed on the last run.
    #
    # The original asserted `(Sha $locked) -ne $lockedHash` -- "the file
    # changed, so the save landed". It cannot carry that meaning. A
    # manual ctrl+s on a CLEAN buffer still sends a write, with content
    # identical to what is on disk; os.replace then writes the same
    # bytes and the hash does not move. So a save that landed perfectly
    # and a save that was refused are hash-indistinguishable, and a
    # keystroke that went anywhere other than the buffer produces the
    # failing case with nothing wrong at all.
    #
    # Traced in Go before changing anything, because the suspicion was
    # that the take-over left dirty-tracking stale. It does not: nothing
    # in the lock path touches d.dirty, saveDoc's only dirty-gated skip
    # is `reason == saveAuto`, and a test driving take-over -> edit ->
    # ctrl+s shows the write going out carrying the edit
    # (TestATakeOverLeavesDirtyTrackingIntactAndTheSaveCarriesTheEdit).
    # The product was right; the assertion was wrong.
    #
    # So the user types a KNOWN string and the gate looks for it. That
    # asserts what the step is actually about -- the edit reached the
    # buffer AND the save put it on disk -- and cannot pass on a
    # no-change write.
    $marker = "# taken over by the gate"

    # The hold has to stop BEFORE the backdate, or the job's next beat
    # (within 15s) resets heartbeat_at and the lock is live again by the
    # time the user presses f6 -- the take-over would then be refused
    # for a reason that looks exactly like the bug this step tests.
    StopHold $hold
    $hold = $null
    Sql "UPDATE file_locks SET heartbeat_at = heartbeat_at - 120 WHERE rel_path LIKE '%locked.py';" | Out-Null
    $age = Sql "SELECT CAST(strftime('%s','now') - heartbeat_at AS INT) FROM file_locks WHERE rel_path LIKE '%locked.py';"
    Write-Host "  backdated heartbeat age: ${age}s" -ForegroundColor DarkGray
    Assert-That ([int]$age -gt 60) "the other holder's lock is now STALE"

    Do-This @(
        "Back in Point, with locked.py still open, press f6.",
        "Do NOT press ctrl+o again -- step 3b already moved the keyboard",
        "  into the code pane and nothing since has moved it. ctrl+o is a",
        "  TOGGLE, so a second press here sends the marker to the tree and",
        "  this step fails with nothing wrong in the product. (That is what",
        "  the first run of this step did.)",
        "Go to the end of the file and type EXACTLY this line:",
        "    $marker",
        "Check the status bar shows the dirty marker, then press ctrl+s.",
        "Then quit with ctrl+q."
    )
    Expect @(
        "f6 says 'took over the lock on locked.py'",
        "the segment becomes 'lock: you'",
        "the dirty marker appears while you type and clears after ctrl+s",
        "ctrl+s says 'saved locked.py'"
    )
    Fails @(
        "f6 silently does nothing. It was first bound to ctrl+shift+l,",
        "  which Windows cannot deliver -- Ctrl+L and Ctrl+Shift+L produce",
        "  the same control character, so both arrive as ctrl+l, which is",
        "  app.redraw's chord and is consulted before this workspace. If f6",
        "  is dead too, STOP and say so.",
        "f6 is still refused as 'still live' -- the backend re-checks the",
        "  age on the request, so a backdated row must be stealable",
        "the dirty marker never appears -- then the keys are going to the",
        "  TREE, not the buffer; press ctrl+o and type again"
    )
    Pause "step 3c -- the take-over and the marker typed; quit Point when finished"

    Write-Host ""
    Write-Host "  waiting for Point to exit..." -ForegroundColor DarkGray
    $proc.WaitForExit()
    Write-Host "  Point exited (code $($proc.ExitCode))" -ForegroundColor DarkGray

    $ours = Sql "SELECT holder FROM file_locks WHERE rel_path LIKE '%locked.py';"
    Write-Host "  file_locks holder now: $ours" -ForegroundColor DarkGray
    Assert-That ($ours -ne $holderB) "the stale lock was taken over (Done-when 7)"

    # The marker, not the hash. See the comment above $marker.
    $lockedText = Get-Content -Raw -LiteralPath $locked
    Assert-That ($lockedText -match [regex]::Escape($marker)) `
        "the edit made after the take-over is ON DISK -- so the save landed AND the keystrokes reached the buffer"

    # -----------------------------------------------------------------
    Step 4 "Done-when 8 and C1/C2 -- external change, then save"
    # -----------------------------------------------------------------

    # This step runs against a LIVE Point (LaunchBg, not Launch) because the
    # external change has to land while a session is already holding the
    # ORIGINAL mtime -- see LaunchBg's own comment for why two blocking
    # launches cannot produce that state.
    #
    # It also has TWO checkpoints rather than one, and the second half
    # deliberately asserts nothing about the file. Reason: [reload] leaves
    # the document clean with the FRESH mtime, so the ctrl+z after it makes
    # the buffer dirty again against a correct baseline, and about a second
    # later autosave writes the restored edits over the gate's line -- a
    # correct save, not a bug. A disk assertion placed after that sequence
    # would report a false FAIL on working code. So the clobber assertion is
    # taken at the point the save was REFUSED, before any reload, which is
    # exactly where Done-when 8 makes its claim.
    $proc = LaunchBg

    Do-This @(
        "In the window that just opened: choose the EDITOR, open",
        "  backend\tests\fixtures\f13b\external.py, press ctrl+o, and",
        "  TYPE SOMETHING.",
        "Then stop typing and WAIT about two seconds for autosave to settle.",
        "Do NOT press ctrl+s and do NOT quit -- leave it exactly there.",
        "Come back to THIS window."
    )
    Expect @(
        "the status bar reads 'lock: you' for external.py",
        "the dirty marker appears while you type and CLEARS about a second",
        "  after you stop -- autosave is on in this gate's config.toml"
    )
    Fails @(
        "the marker never clears (autosave is not firing -- that is",
        "  Done-when 10, and step 6 covers it; stop here if so)"
    )
    # Letting autosave settle first is deliberate, not incidental. It makes
    # the baseline KNOWN: external.py's mtime is now whatever Point itself
    # last wrote, and the gate's change below moves it from there. It also
    # makes the next checkpoint a stronger claim than it would otherwise be
    # -- the buffer is CLEAN when ctrl+s is pressed, so a refusal proves the
    # precondition is driven by the mtime and not by dirtiness.
    Pause "step 4a -- external.py open, edited and autosaved; Point still running"

    # THE RACE. Point is up, holding the pre-change mtime as its
    # expected_mtime; the file moves underneath it now.
    Add-Content -LiteralPath $ext -Value "# changed by the gate"
    $extAfterChange = Sha $ext
    Write-Host "  external.py was modified on disk underneath the live editor" -ForegroundColor DarkGray
    Write-Host "  hash now: $extAfterChange" -ForegroundColor DarkGray

    Do-This @(
        "Back in Point, press ctrl+s.",
        "Read the dialog, then press esc to dismiss it WITHOUT choosing.",
        "Do not quit. Come back to THIS window."
    )
    Expect @(
        "ctrl+s is REFUSED with a dialog offering [reload] [overwrite] [diff]",
        "  -- three buttons, which is why it is not a Confirm (C1)",
        "[reload] is focused by default, not [overwrite]",
        "it is refused even though the buffer is CLEAN -- the precondition is",
        "  the recorded mtime, not whether you have unsaved edits",
        "after esc the status bar reads 'autosave stopped' for this file"
    )
    Fails @(
        "the save goes through silently and says 'saved external.py'",
        "the dialog has only two buttons",
        "nothing happens at all (a clean buffer being skipped -- a manual",
        "  save must still go out, which is what makes this case testable)",
        "'autosave stopped' never appears, or a refusal repeats once a second"
    )
    Pause "step 4b -- the refusal seen and dismissed, Point still running"

    # Done-when 8's actual assertion, taken while Point is still up and
    # before any reload: a refused save writes NOTHING.
    Assert-That ((Sha $ext) -eq $extAfterChange) `
        "the refused save wrote nothing -- the external change is intact (Done-when 8)"
    Assert-That ((Get-Content -Raw -LiteralPath $ext) -match "changed by the gate") `
        "the gate's own line is still in the file"

    Do-This @(
        "Now press ctrl+s again and work the dialog properly:",
        "  press d (or move to [diff] and press Enter),",
        "  read the diff, then press esc,",
        "  then choose [reload], then press ctrl+z.",
        "Then quit with ctrl+q."
    )
    Expect @(
        "the diff shows BOTH the gate's added line and your own edit",
        "esc from the diff returns to the THREE-BUTTON DIALOG, not to the",
        "  editor -- the save is still refused and still unresolved (C2)",
        "[reload] replaces the buffer with what is on disk and says so",
        "ctrl+z after the reload PUTS YOUR EDITS BACK -- one undo step"
    )
    Fails @(
        "esc from the diff drops you back in the editor with nothing said",
        "the reload clears undo history and your edits are unrecoverable"
    )
    Write-Host ""
    Write-Host "  NOTE: after the ctrl+z, autosave will write your restored edits" -ForegroundColor DarkGray
    Write-Host "  over the gate's line within about a second. That is CORRECT --" -ForegroundColor DarkGray
    Write-Host "  the reload gave the buffer a fresh, valid mtime. Nothing below" -ForegroundColor DarkGray
    Write-Host "  asserts on the file's contents after this point." -ForegroundColor DarkGray
    Pause "step 4c -- the dialog worked through; quit Point when done"

    # Wait for the process to ACTUALLY exit before moving on. Falling
    # through on the Pause alone would leave a Point running into the next
    # step, where it would hold locks step 9 counts and own a terminal the
    # next Launch wants.
    Write-Host ""
    Write-Host "  waiting for Point to exit..." -ForegroundColor DarkGray
    $proc.WaitForExit()
    Write-Host "  Point exited (code $($proc.ExitCode))" -ForegroundColor DarkGray
    Assert-That ($proc.HasExited) "the background Point process exited before step 5"

    # -----------------------------------------------------------------
    Step 5 "Done-when 9 and C3 -- closing a dirty file"
    # -----------------------------------------------------------------

    Do-This @(
        "Open lf.py, press ctrl+o, and type something. Do NOT wait for",
        "  autosave -- within",
        "  a second of typing, click the x on its tab (or use the tab strip).",
        "If autosave beats you to it, that is fine: edit again and click x",
        "  immediately. Press esc or choose Cancel on the dialog.",
        "Then click x again and choose Close.",
        "Quit with ctrl+q."
    )
    Expect @(
        "a Confirm appears: [ Close ]  [ Cancel ], with CANCEL focused (C3)",
        "Cancel/esc leaves the tab open and the edits intact",
        "Close discards and closes -- there is NO third 'Save and Close'",
        "  button, and Close does not quietly save first"
    )
    Fails @(
        "the tab closes with no dialog at all",
        "Close is focused by default (an Enter out of habit discards work)",
        "Close writes the file -- a button that sometimes saves and sometimes",
        "  does not, depending on state the dialog does not show"
    )
    Pause "step 5"
    Launch

    # -----------------------------------------------------------------
    Step 6 "Done-when 10 -- autosave fires ONCE, not thirty times"
    # -----------------------------------------------------------------
    #
    # Counted with a FileSystemWatcher rather than eyeballed. "One save at
    # the end, not thirty" is a claim about a count, and a count is the one
    # thing a human watching a terminal cannot produce.

    # RENAMED, not Changed -- and NotifyFilters.FileName, not LastWrite.
    #
    # The save is atomic: ProjectManager._atomic_write writes a temp file
    # in the same directory and ends in os.replace. On Windows that fires
    # Deleted + Renamed on the target NAME and NO Changed event at all,
    # so a watcher subscribed to Changed with NotifyFilter = LastWrite --
    # which is what this was -- could never see a save. The count was
    # structurally zero, and the first repair only corrected the
    # assertion's bounds, which turned an always-pass into an
    # always-fail without touching the cause.
    #
    # Measured directly against a real os.replace into a watched
    # directory, which is the only way to know:
    #
    #   NotifyFilter = LastWrite, Changed   -> NO EVENTS
    #   NotifyFilter = FileName             -> Deleted x1, Renamed x1
    #   LastWrite+FileName+Size+CreationTime-> Deleted x1, Renamed x1
    #
    # Renamed is counted and Deleted is not, because a replace fires
    # exactly one of each -- so one Renamed IS one save, which makes the
    # bound below a count of saves rather than a count of events.
    $watcher = New-Object IO.FileSystemWatcher $fixtures, "lf.py"
    $watcher.NotifyFilter = [IO.NotifyFilters]::FileName
    # $global:, NOT $script:. A Register-ObjectEvent -Action block runs in
    # its own isolated scope, so $script:saveCount++ inside it increments a
    # DIFFERENT variable and the caller always reads 0 -- which would make
    # the assertion below report a false FAIL on a working autosave.
    # Measured directly against this PowerShell before relying on it:
    # $script: saw 0 of 6 events, $global: saw 6 of 6.
    $global:saveCount = 0
    $sub = Register-ObjectEvent -InputObject $watcher -EventName Renamed `
        -Action { $global:saveCount++ }
    $watcher.EnableRaisingEvents = $true

    Do-This @(
        "Open lf.py, press ctrl+o, then TYPE CONTINUOUSLY for about 30",
        "  seconds -- do not",
        "  pause, and do NOT press ctrl+s at any point.",
        "Then STOP typing and watch the status bar for a couple of seconds.",
        "Quit with ctrl+q."
    )
    Expect @(
        "the dirty marker '. modified' is visible the whole time you type",
        "about a second after you STOP, the file saves and the marker clears",
        "nothing saves while you are still typing"
    )
    Fails @(
        "the marker clears and reappears repeatedly while typing (a save per",
        "  keystroke -- the debounce is not debouncing)",
        "nothing ever saves"
    )
    Pause "step 6"
    Launch

    $watcher.EnableRaisingEvents = $false
    Unregister-Event -SubscriptionId $sub.Id
    $watcher.Dispose()
    $observed = $global:saveCount
    Write-Host "  atomic replaces of lf.py observed: $observed" -ForegroundColor DarkGray
    # One Renamed per os.replace, so this is a count of SAVES. The upper
    # bound is still generous: a user who pauses mid-sentence legitimately
    # produces more than one.
    # ONE assertion with BOTH bounds, not two that can disagree.
    #
    # The first version had the upper bound only in the second call, and
    # the lower bound in a first call that was easy to read past -- so a
    # run where the watcher saw NOTHING at all satisfied "-le 6" and the
    # step went green having observed no save whatever. Both bounds in
    # one place is what makes "a handful, and at least one" a single
    # claim that cannot half-pass.
    Assert-That ($observed -ge 1 -and $observed -le 6) `
        "autosave fired between 1 and 6 times after typing stopped, not once per keystroke and not never (Done-when 10, observed $observed)"

    # -----------------------------------------------------------------
    Step 7 "Done-when 11 -- autosave on a file held by someone else"
    # -----------------------------------------------------------------

    Push-Location (Join-Path $repo "backend")
    uv run python cli.py project lock acquire `
        --path $repo --file $lockedRel --holder $holderB | Out-Null
    Pop-Location
    # Held open for the duration, for step 3's reason: this step has a
    # deliberate ten-second wait inside a human-paced manual step, which
    # is exactly the shape that loses the 60-second race.
    $hold7 = HoldLock $lockedRel $holderB
    $heldHash = Sha $locked
    $logDir = Join-Path $scratch "Point\logs"

    Do-This @(
        "Open locked.py, press ctrl+o, type something, and WAIT about ten",
        "  seconds without",
        "  pressing ctrl+s.",
        "Quit with ctrl+q."
    )
    Expect @(
        "the status bar names the other holder",
        "it also reads 'autosave stopped' -- said out loud, because the whole",
        "  promise of autosave is that unsaved work does not stay unsaved",
        "you see the refusal AT MOST ONCE, not once a second for ten seconds"
    )
    Fails @(
        "the file is written anyway",
        "a refusal message flickers repeatedly (Done-when 11's 'does not spam')"
    )
    Pause "step 7"
    Launch

    # Step 3's defect, in its latent form. This step has the same shape
    # -- cli.py takes an unheartbeated lock, then a human-paced manual
    # step runs against B7's 60-second window -- so if the row ages out
    # while the user is working, the editor takes it over legitimately,
    # autosave succeeds, and the file assertion below fails for a reason
    # that has nothing to do with Done-when 11. Checked FIRST so the run
    # says which of the two happened instead of blaming the product.
    StopHold $hold7
    $hold7 = $null

    $heldAfter = Sql "SELECT holder FROM file_locks WHERE rel_path LIKE '%locked.py';"
    Assert-That ($heldAfter -eq $holderB) `
        "the other holder STILL owns the lock. With HoldLock beating it every 15s this can no longer be the 60-second staleness race, so a failure here is the editor genuinely failing to respect a live lock"
    Assert-That ((Sha $locked) -eq $heldHash) `
        "locked.py untouched -- autosave did not fire on a held file (Done-when 11)"

    # The 'does not spam' half, counted rather than eyeballed. Every
    # refusal logs, so a storm leaves a trail.
    $refusals = 0
    if (Test-Path $logDir) {
        $refusals = (Select-String -Path (Join-Path $logDir "*.log") `
            -Pattern "locked" -AllMatches -ErrorAction SilentlyContinue |
            Measure-Object).Count
    }
    Write-Host "  'locked' lines in the backend log: $refusals" -ForegroundColor DarkGray
    Assert-That ($refusals -le 4) `
        "at most a handful of locked refusals, not one per second"

    Push-Location (Join-Path $repo "backend")
    uv run python cli.py project lock release `
        --path $repo --file "backend/tests/fixtures/f13b/locked.py" --holder $holderB | Out-Null
    Pop-Location

    # -----------------------------------------------------------------
    Step 8 "Done-when 12 -- Point does not trip its OWN staleness check"
    # -----------------------------------------------------------------
    #
    # The subtlest of the twelve, and the one a missing line of code
    # breaks silently. WriteProjectFile's reply carries the NEW mtime
    # (B32 built it that way for exactly this), and autosave rule 2 is
    # recording it. Without that, the next save sends the mtime from the
    # original READ, the file on disk has the one Point just wrote, and
    # the backend refuses `stale` against Point's own change.
    #
    # It is also why no follow-up CheckFileStale is needed anywhere.

    Do-This @(
        "Open lf.py and press ctrl+o. Type something, then STOP and wait",
        "  for the autosave",
        "  (the dirty marker clears).",
        "Now type something else and press ctrl+s IMMEDIATELY.",
        "Repeat that twice more: autosave, then a manual save.",
        "Quit with ctrl+q."
    )
    Expect @(
        "every manual save SUCCEEDS -- 'saved lf.py'",
        "NO 'changed on disk' dialog ever appears",
        "no [reload] [overwrite] [diff] at any point"
    )
    Fails @(
        "a stale dialog appears after Point's own autosave -- that is exactly",
        "  Done-when 12 failing, and it means the write reply's mtime is not",
        "  being recorded"
    )
    Pause "step 8"
    Launch
    Assert-That $true "Done-when 12 is eyes-on: no stale dialog after Point's own write"

    # -----------------------------------------------------------------
    Step 9 "locks are RELEASED on quit and on unload"
    # -----------------------------------------------------------------
    #
    # Not one of the twelve, and worth its own step: a lock that outlives
    # the editor makes Point wait out its OWN row on the next open, and
    # B7's 60-second staleness is supposed to be the backstop for a crash,
    # not the normal path.

    $rowsBefore = Sql "SELECT COUNT(*) FROM file_locks;"
    Write-Host "  file_locks rows before: $rowsBefore" -ForegroundColor DarkGray

    Do-This @(
        "Open THREE files in the Editor (lf.py, mixed.py, no_eol.py).",
        "Confirm each shows 'lock: you', then quit with ctrl+q."
    )
    Expect @("'lock: you' on each of the three")
    Pause "step 9"
    Launch

    $rowsAfter = Sql "SELECT COUNT(*) FROM file_locks;"
    Write-Host "  file_locks rows after quit: $rowsAfter" -ForegroundColor DarkGray
    Assert-That ([int]$rowsAfter -eq 0) `
        "every lock was released on quit -- no row survives the process"

    # -----------------------------------------------------------------
    Step 10 "the read-only paths still refuse, with the reason"
    # -----------------------------------------------------------------
    #
    # F13a shipped the read-only reasons and this episode has to keep
    # them true now that ctrl+s actually does something. A read-only file
    # must refuse BEFORE the wire, and say which of the three reasons.

    $roPath = Join-Path $fixtures "readonly.py"
    [IO.File]::WriteAllText($roPath, "z = 1`r`n", $noBom)
    Set-ItemProperty -LiteralPath $roPath -Name IsReadOnly -Value $true
    $roHash = Sha $roPath

    Do-This @(
        "Open readonly.py, press ctrl+o, then try to type and press ctrl+s.",
        "Quit with ctrl+q."
    )
    Expect @(
        "the status bar reads 'read-only (file is read-only)'",
        "ctrl+s says the file is read-only and names the reason",
        "there is NO lock segment problem -- a read-only FILE still takes a",
        "  lock, because the reason is the file's state, not a missing project"
    )
    Fails @(
        "ctrl+s reports a backend refusal instead of refusing locally",
        "the file changes on disk"
    )
    Pause "step 10"
    Launch
    Assert-That ((Sha $roPath) -eq $roHash) "readonly.py unchanged"

    # -----------------------------------------------------------------
    Write-Host ""
    Write-Host ("=" * 72) -ForegroundColor DarkGray
    if ($script:failures.Count -eq 0) {
        Write-Host " F13b GATE: all automated assertions passed." -ForegroundColor Green
        Write-Host " The eyes-on steps are yours to confirm." -ForegroundColor Green
    } else {
        Write-Host " F13b GATE: $($script:failures.Count) assertion(s) FAILED:" -ForegroundColor Red
        foreach ($f in $script:failures) { Write-Host "   - $f" -ForegroundColor Red }
    }
    Write-Host ("=" * 72) -ForegroundColor DarkGray
    Write-Host ""
    Write-Host " Covered here: F13's Done-when 2, 7, 8, 9, 10, 11, 12," -ForegroundColor Green
    Write-Host " the save half of 1, and decisions C1 C2 C3 C4 C5." -ForegroundColor Green
    Write-Host " gate_f13a.ps1 covers 3, 4, 5, 6 and the open half of 1." -ForegroundColor Green
    Write-Host " BOTH must be green to close F13." -ForegroundColor Green
    Write-Host ""
    Write-Host " Decisions this episode made that its doc did not specify:" -ForegroundColor Yellow
    Write-Host "   an AUTOSAVE that hits 'stale' does NOT open the modal. It says" -ForegroundColor Yellow
    Write-Host "   one line and stops autosaving that file until you act; ctrl+s" -ForegroundColor Yellow
    Write-Host "   then opens the dialog deliberately. A modal appearing a second" -ForegroundColor Yellow
    Write-Host "   after you stop typing, for someone else's change, is how people" -ForegroundColor Yellow
    Write-Host "   end up turning autosave off. Step 8's EXPECT lines assume this." -ForegroundColor Yellow
    Write-Host ""
    Write-Host "   on_focus_change reads 'focus' as the active DOCUMENT changing," -ForegroundColor Yellow
    Write-Host "   not the Chat/Editor workspace switch and not the buffer/tree" -ForegroundColor Yellow
    Write-Host "   pane toggle -- ctrl+o would otherwise be a save key." -ForegroundColor Yellow
    Write-Host ""
    Write-Host " Still owed after this gate:" -ForegroundColor DarkGray
    Write-Host "   the unified diff is prefix/suffix based, so two widely separated" -ForegroundColor DarkGray
    Write-Host "   edits report as ONE hunk spanning both. Never wrong, sometimes" -ForegroundColor DarkGray
    Write-Host "   coarse. F9b owns the real diff; this should call it once it" -ForegroundColor DarkGray
    Write-Host "   exists rather than keeping a second implementation." -ForegroundColor DarkGray
    Write-Host ""
    Write-Host "   lockStaleAfter is a second copy of B7's LOCK_STALE_AFTER_S. It" -ForegroundColor DarkGray
    Write-Host "   only decides whether to OFFER take-over; the backend is still" -ForegroundColor DarkGray
    Write-Host "   the only enforcer, so a drift shifts the offer, not the rule." -ForegroundColor DarkGray
    Write-Host ""
    Write-Host " A note on red text: cli.py logs to stderr BY DESIGN (stdout is the" -ForegroundColor DarkGray
    Write-Host " protocol channel), and this gate deliberately provokes refusals." -ForegroundColor DarkGray
    Write-Host " PowerShell 5.1 renders those as red NativeCommandError blocks." -ForegroundColor DarkGray
    Write-Host " Only 'FAIL' lines above are failures." -ForegroundColor DarkGray

    if ($script:failures.Count -ne 0) { throw "F13b gate: $($script:failures.Count) assertion(s) failed" }
}
finally {
    # Any lock-holding job first: it runs `uv run python` in a loop and
    # would outlive a Ctrl+C or a thrown assertion otherwise, writing to
    # a scratch database this block is about to delete.
    StopHold $hold
    StopHold $hold7

    # Both the scratch profile and the fixtures, whatever happened. The
    # read-only attribute has to come off first or Remove-Item refuses.
    if (Test-Path $fixtures) {
        Get-ChildItem $fixtures -Recurse -File | ForEach-Object { $_.IsReadOnly = $false }
        Remove-Item -LiteralPath $fixtures -Recurse -Force
    }
    if (Test-Path $scratch) {
        Remove-Item -LiteralPath $scratch -Recurse -Force
    }
    Write-Host ""
    Write-Host "cleaned up: $scratch and $fixtures" -ForegroundColor DarkGray
    Write-Host "git status should be clean -- every file this gate wrote was its own." -ForegroundColor DarkGray
}
