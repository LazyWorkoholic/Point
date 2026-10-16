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

    Push-Location (Join-Path $repo "backend")
    uv run python cli.py project lock acquire `
        --path $repo --file "backend/tests/fixtures/f13b/locked.py" --holder $holderB | Out-Null
    $acqExit = $LASTEXITCODE
    Pop-Location
    Assert-That ($acqExit -eq 0) "cli.py took the lock as $holderB"

    $row = Sql "SELECT holder FROM file_locks WHERE rel_path LIKE '%locked.py';"
    Write-Host "  file_locks holder: $row" -ForegroundColor DarkGray
    Assert-That ($row -eq $holderB) "the lock row belongs to the other holder"

    Do-This @(
        "Open locked.py in the Editor. Try to edit it, then press ctrl+s.",
        "Then press ctrl+shift+l to try to take the lock over.",
        "Quit with ctrl+q."
    )
    Expect @(
        "the status bar names the holder and an age, as",
        "  'held by someone-else (other-id) (Ns ago)'",
        "F13's spec writes it 'held by aryan (12s ago)' -- same shape",
        "ctrl+s is REFUSED and says so; nothing is written",
        "ctrl+shift+l is refused too, saying the lock is STILL LIVE and that",
        "  take-over is offered once a lock goes 60s without a heartbeat"
    )
    Fails @(
        "the file saves anyway",
        "the holder is shown as a bare id with no username",
        "the whole 32-character id is printed instead of the first eight",
        "ctrl+shift+l steals a LIVE lock"
    )
    $lockedHash = Sha $locked
    Launch
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
    Sql "UPDATE file_locks SET heartbeat_at = heartbeat_at - 120 WHERE rel_path LIKE '%locked.py';" | Out-Null
    $age = Sql "SELECT CAST(strftime('%s','now') - heartbeat_at AS INT) FROM file_locks WHERE rel_path LIKE '%locked.py';"
    Write-Host "  backdated heartbeat age: ${age}s" -ForegroundColor DarkGray
    Assert-That ([int]$age -gt 60) "the other holder's lock is now STALE"

    Do-This @(
        "Open locked.py again. Read the lock segment, then press ctrl+shift+l.",
        "Then edit it and press ctrl+s.",
        "Quit with ctrl+q."
    )
    Expect @(
        "the lock segment now ENDS with 'ctrl+shift+l takes over' -- the offer",
        "  appears only now that it is stale",
        "ctrl+shift+l says 'took over the lock on locked.py'",
        "the segment becomes 'lock: you'",
        "ctrl+s then SAVES"
    )
    Fails @(
        "the offer was already showing in the previous step (a live lock)",
        "the take-over silently does nothing"
    )
    Launch
    $ours = Sql "SELECT holder FROM file_locks WHERE rel_path LIKE '%locked.py';"
    Write-Host "  file_locks holder now: $ours" -ForegroundColor DarkGray
    Assert-That ($ours -ne $holderB) "the stale lock was taken over (Done-when 7)"
    Assert-That ((Sha $locked) -ne $lockedHash) "and the file then saved"

    # -----------------------------------------------------------------
    Step 4 "Done-when 8 and C1/C2 -- external change, then save"
    # -----------------------------------------------------------------

    Do-This @(
        "Open external.py in the Editor and TYPE SOMETHING -- but do not save.",
        "Leave Point RUNNING and come back to this window."
    )
    Pause "open external.py, edit it, and leave Point running"

    Add-Content -LiteralPath $ext -Value "# changed by the gate"
    Write-Host "  external.py was modified on disk underneath the editor" -ForegroundColor DarkGray

    Do-This @(
        "Back in Point, press ctrl+s.",
        "Press d (or move to [diff] and press Enter).",
        "Read the diff, then press esc.",
        "Then choose [reload]. Then press ctrl+z.",
        "Quit with ctrl+q."
    )
    Expect @(
        "ctrl+s is REFUSED with a dialog offering [reload] [overwrite] [diff]",
        "  -- three buttons, which is why it is not a Confirm (C1)",
        "[reload] is focused by default, not [overwrite]",
        "the diff shows BOTH the gate's added line and your own edit",
        "esc from the diff returns to the THREE-BUTTON DIALOG, not to the",
        "  editor -- the save is still refused and still unresolved (C2)",
        "[reload] replaces the buffer with what is on disk and says so",
        "ctrl+z after the reload PUTS YOUR EDITS BACK -- one undo step"
    )
    Fails @(
        "the save goes through silently, overwriting the gate's line",
        "esc from the diff drops you back in the editor with nothing said",
        "the reload clears undo history and your edits are unrecoverable",
        "the dialog has only two buttons"
    )
    Launch

    $extText = Get-Content -Raw -LiteralPath $ext
    Assert-That ($extText -match "changed by the gate") `
        "the external change was NOT clobbered by the refused save (Done-when 8)"

    # -----------------------------------------------------------------
    Step 5 "Done-when 9 and C3 -- closing a dirty file"
    # -----------------------------------------------------------------

    Do-This @(
        "Open lf.py and type something. Do NOT wait for autosave -- within",
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
    Launch

    # -----------------------------------------------------------------
    Step 6 "Done-when 10 -- autosave fires ONCE, not thirty times"
    # -----------------------------------------------------------------
    #
    # Counted with a FileSystemWatcher rather than eyeballed. "One save at
    # the end, not thirty" is a claim about a count, and a count is the one
    # thing a human watching a terminal cannot produce.

    $watcher = New-Object IO.FileSystemWatcher $fixtures, "lf.py"
    $watcher.NotifyFilter = [IO.NotifyFilters]::LastWrite
    # $global:, NOT $script:. A Register-ObjectEvent -Action block runs in
    # its own isolated scope, so $script:saveCount++ inside it increments a
    # DIFFERENT variable and the caller always reads 0 -- which would make
    # the assertion below report a false FAIL on a working autosave.
    # Measured directly against this PowerShell before relying on it:
    # $script: saw 0 of 6 events, $global: saw 6 of 6.
    $global:saveCount = 0
    $sub = Register-ObjectEvent -InputObject $watcher -EventName Changed `
        -Action { $global:saveCount++ }
    $watcher.EnableRaisingEvents = $true

    Do-This @(
        "Open lf.py and TYPE CONTINUOUSLY for about 30 seconds -- do not",
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
    Launch

    $watcher.EnableRaisingEvents = $false
    Unregister-Event -SubscriptionId $sub.Id
    $watcher.Dispose()
    $observed = $global:saveCount
    Write-Host "  filesystem write events on lf.py: $observed" -ForegroundColor DarkGray
    # A single os.replace can raise more than one Changed event, so the
    # bound is generous -- what it rules out is the failure that matters,
    # one save per keystroke.
    Assert-That ($observed -ge 1) "autosave fired at least once after typing stopped"
    Assert-That ($observed -le 6) `
        "autosave fired a HANDFUL of times, not once per keystroke (Done-when 10)"

    # -----------------------------------------------------------------
    Step 7 "Done-when 11 -- autosave on a file held by someone else"
    # -----------------------------------------------------------------

    Push-Location (Join-Path $repo "backend")
    uv run python cli.py project lock acquire `
        --path $repo --file "backend/tests/fixtures/f13b/locked.py" --holder $holderB | Out-Null
    Pop-Location
    $heldHash = Sha $locked
    $logDir = Join-Path $scratch "Point\logs"

    Do-This @(
        "Open locked.py, type something, and WAIT about ten seconds without",
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
    Launch
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
        "Open lf.py. Type something, then STOP and wait for the autosave",
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
        "Open readonly.py. Try to type, then press ctrl+s.",
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
