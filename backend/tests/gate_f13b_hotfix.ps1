# F13b hotfix -- two save/autosave regressions: manual verification
#
#   powershell -NoProfile -ExecutionPolicy Bypass -File D:\Projects\Point\backend\tests\gate_f13b_hotfix.ps1
#
# Run it from anywhere. It creates ONE scratch profile, points both APPDATA
# and LOCALAPPDATA at it for the whole script, generates its two fixtures
# inside the repo, and removes both at the end. Nothing under the real
# %APPDATA%\Point or %LOCALAPPDATA%\Point is ever named by this script,
# destructively or otherwise -- CLAUDE.md's env-var-override pattern. The
# retired rename-aside pattern is NOT used.
#
# It stops at each step. Read the EXPECT lines before pressing Enter.
#
# ---------------------------------------------------------------------------
# What this gate covers, and what it deliberately does not
# ---------------------------------------------------------------------------
#
# TWO scenarios only. This is not a replacement for gate_f13b.ps1 and it
# re-verifies none of F13's Done-whens -- it exists because both bugs below
# are races against something the user does with the HANDS (a workspace
# switch, a dialog), and gate_f13b drives one thing at a time and waits,
# which is exactly why neither showed up in its run.
#
#   A  a save wedged across the idle-unload-skip path.
#      Autosave starts a write; the user switches to Chat inside the
#      one-second debounce; Root delivered EvProjectFileWritten only to
#      the ACTIVE workspace, so the Editor never saw its own reply.
#      doc.saving stayed true for ever: status bar stuck on "saving...",
#      dirty marker never cleared, and saveDoc's in-flight guard then
#      refused every later save -- autosave AND ctrl+s. The file became
#      permanently un-saveable and nothing said why.
#
#   B  autosave racing UI-13's unload confirm.
#      The dialog asks whether these buffers survive. A tick armed by the
#      keystroke that made the file dirty landed while the user was still
#      reading it and wrote the edits, so Unload discarded a buffer that
#      was already on disk and Cancel was indistinguishable from it.
#      The Editor's OWN close confirm already paused autosave for this
#      exact reason (gate_f13b step 5); this dialog is opened by Root and
#      had no way to ask for the same pause.
#
# ---------------------------------------------------------------------------
# Two things about this file's own bytes
# ---------------------------------------------------------------------------
#
# 1. UTF-8 WITH a BOM, content ASCII. gate_f13a's first draft was UTF-8
#    without one, so PowerShell 5.1 decoded its em-dashes as cp1252 and hit
#    a stray quote: ten parse errors before a line ran.
#
# 2. $ErrorActionPreference is Continue, not Stop. B32's finding: in
#    Windows PowerShell 5.1 a native command writing to stderr produces a
#    NativeCommandError, which Stop promotes to terminating. This script
#    runs cli.py, which logs to stderr BY DESIGN. Safety comes from the two
#    env-var overrides, not from this setting.

$ErrorActionPreference = "Continue"

$repo     = "D:\Projects\Point"
$scratch  = Join-Path $env:TEMP "point-scratch-f13b-hotfix"
$fixtures = Join-Path $repo "backend\tests\fixtures\f13b_hotfix"

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
function HashOf($p) {
    if (-not (Test-Path -LiteralPath $p)) { return "" }
    return (Get-FileHash -LiteralPath $p -Algorithm SHA256).Hash
}

New-Item -ItemType Directory -Force $scratch | Out-Null

try {
    $env:APPDATA      = $scratch   # config.toml, credentials.toml, state.json
    $env:LOCALAPPDATA = $scratch   # runtime.db, logs\

    Write-Host "scratch profile: $scratch" -ForegroundColor DarkGray
    Write-Host "fixtures:        $fixtures" -ForegroundColor DarkGray

    # -----------------------------------------------------------------
    Step 0 "setup -- scratch profile, idle_unload_minutes = 1, two fixtures"
    # -----------------------------------------------------------------
    #
    # A failed setup that stops nothing makes every later check
    # meaningless (B32's audit finding), so the setup asserts too.

    Push-Location (Join-Path $repo "backend")
    uv run python cli.py migrate | Out-Null
    $migrateExit = $LASTEXITCODE
    Pop-Location
    Assert-That ($migrateExit -eq 0) "cli.py migrate exited 0"
    Assert-That (Test-Path (Join-Path $scratch "Point\runtime.db")) `
        "runtime.db is in the SCRATCH profile, not the real one"
    Assert-That (Test-Path (Join-Path $repo ".point\project.json")) `
        "the repo has a .point\project.json for ProjectResolve to find"

    # idle_unload_minutes = 1 is the whole point of this config: it makes
    # the idle sweep reachable by a person waiting rather than by a person
    # waiting five minutes. autosave stays at the 1000ms default, because
    # the race being tested is a one-second debounce against a keystroke.
    New-Item -ItemType Directory -Force (Join-Path $scratch "Point") | Out-Null
    $confPath = Join-Path $scratch "Point\config.toml"
    @'
[workspace]
idle_unload_minutes = 1

[editor]
autosave = "after_delay"
autosave_delay_ms = 1000
'@ | Set-Content -LiteralPath $confPath -Encoding utf8
    Assert-That (Test-Path $confPath) "config.toml written to the scratch profile"
    Assert-That ((Get-Content -Raw -LiteralPath $confPath) -match "idle_unload_minutes = 1") `
        "idle_unload_minutes = 1 is set (the idle sweep is reachable by hand)"

    New-Item -ItemType Directory -Force $fixtures | Out-Null
    $fileA = Join-Path $fixtures "wedge.py"
    $fileB = Join-Path $fixtures "dialog.py"
    "print('a')`nprint('b')`n" | Set-Content -LiteralPath $fileA -Encoding utf8 -NoNewline
    "print('a')`nprint('b')`n" | Set-Content -LiteralPath $fileB -Encoding utf8 -NoNewline
    Assert-That (Test-Path $fileA) "fixture wedge.py exists"
    Assert-That (Test-Path $fileB) "fixture dialog.py exists"

    $relA = "backend\tests\fixtures\f13b_hotfix\wedge.py"
    $relB = "backend\tests\fixtures\f13b_hotfix\dialog.py"

    # -----------------------------------------------------------------
    Step "0b" "the regression tests for both bugs"
    # -----------------------------------------------------------------
    #
    # Eleven tests, and every one was shown to FAIL against its own
    # reverted fix before being accepted. They cover the mechanisms; the
    # two steps below cover the thing a person actually does.
    Push-Location $repo
    go build ./... | Out-Null
    $buildExit = $LASTEXITCODE
    $fmt = (gofmt -l .\frontend\) -join ""
    go vet ./frontend/... | Out-Null
    $vetExit = $LASTEXITCODE
    go test -count=1 ./frontend/internal/editor/ ./frontend/internal/app/ | Out-Null
    $testExit = $LASTEXITCODE
    Pop-Location
    Assert-That ($buildExit -eq 0) "go build ./... exited 0"
    Assert-That ($fmt -eq "")      "gofmt -l reports nothing"
    Assert-That ($vetExit -eq 0)   "go vet ./frontend/... exited 0"
    Assert-That ($testExit -eq 0)  "editor + app suites exited 0"

    # =================================================================
    Step "A" "BUG 1 -- a save must not wedge across the idle-unload skip"
    # =================================================================
    #
    # The skip itself is NOT the bug and is correct behaviour: F13a taught
    # the idle sweep to leave a dirty workspace loaded rather than discard
    # an unsaved buffer, and the notice is how that becomes visible. The
    # skip is in this repro because it is what keeps the Editor loaded and
    # backgrounded for long enough that the reply definitely arrives while
    # something else is active.
    #
    # Timing: idle_unload_minutes = 1, so stay in Chat for at least 70
    # seconds. The notice fires on the sweep tick after the window passes.

    $hashA0 = HashOf $fileA
    Write-Host "  wedge.py before: $hashA0" -ForegroundColor DarkGray

    Do-This @(
        "Point will launch. Go to the EDITOR (ctrl+w, then e).",
        "Open $relA from the tree.",
        "Press ctrl+o to put the keyboard in the buffer, then TYPE A FEW",
        "  CHARACTERS so the file is dirty (the bullet appears).",
        "IMMEDIATELY -- inside about a second, do not wait for the save --",
        "  switch to CHAT with ctrl+w then w. That is the race: the write",
        "  is in flight and the Editor stops being the active workspace.",
        "STAY IN CHAT for at least 70 seconds, past idle_unload_minutes = 1.",
        "Watch for: 'Editor stays loaded - N file(s) have unsaved changes'.",
        "Then switch BACK to the Editor with ctrl+w then e.",
        "LOOK AT THE STATUS BAR and the tab's dirty marker before quitting.",
        "Finally: press ctrl+s once, look again, then quit with ctrl+q."
    )
    Expect @(
        "EITHER the file is clean -- no bullet, no 'saving...' -- because the",
        "  autosave completed and its reply was accepted while backgrounded,",
        "OR it is cleanly dirty: the bullet is there, NO 'saving...' segment,",
        "  and ctrl+s then saves it and clears the bullet.",
        "the 'stays loaded' notice appears exactly once, not once per tick.",
        "after ctrl+s the file is saveable -- the bullet clears."
    )
    Fails @(
        "the status bar is stuck on 'saving...' after returning to the Editor",
        "the dirty bullet persists AND ctrl+s does nothing at all",
        "the file's on-disk hash never changes across the whole step",
        "  even though the notice said there were unsaved changes"
    )
    Pause "step A"
    Launch

    $hashA1 = HashOf $fileA
    Write-Host "  wedge.py after:  $hashA1" -ForegroundColor DarkGray
    Assert-That ($hashA1 -ne $hashA0) `
        "wedge.py CHANGED on disk -- a save actually landed (wedged = never writes)"
    Assert-That ($hashA1 -ne "") "wedge.py still exists"

    Write-Host ""
    Write-Host "  The two eyes-on answers this step needs from you:" -ForegroundColor DarkGray
    $stuck = Read-Host "  was the status bar STUCK on 'saving...' when you came back? (y/n)"
    Assert-That ($stuck -notmatch '^[Yy]') `
        "the status bar was NOT stuck on 'saving...' (bug 1's signature)"
    $saveable = Read-Host "  did ctrl+s clear the dirty bullet (or was it already clean)? (y/n)"
    Assert-That ($saveable -match '^[Yy]') `
        "the document was still saveable after the background round trip"

    # =================================================================
    Step "B" "BUG 2 -- autosave must pause while the unload confirm is open"
    # =================================================================
    #
    # Two halves, Cancel then Unload, and the hash is taken around EACH.
    # The Cancel half is the one with a definite answer: nothing may be
    # written while the dialog is up, so the hash must be unchanged at the
    # moment it is dismissed.
    #
    # ctrl+w u is leader then u. Cancel is focused by default (F07's rule:
    # an Enter pressed out of habit never fires the destructive path), so
    # Cancel is Enter and Unload needs an explicit left/right first.

    $hashB0 = HashOf $fileB
    Write-Host "  dialog.py before: $hashB0" -ForegroundColor DarkGray

    Do-This @(
        "Point will launch. Go to the EDITOR (ctrl+w, then e).",
        "Open $relB, press ctrl+o, and TYPE A FEW CHARACTERS (dirty).",
        "IMMEDIATELY press ctrl+w then u. The unload confirm appears:",
        "  [ Unload ]  [ Cancel ]   with Cancel focused.",
        "NOW WAIT at least 5 seconds with the dialog open -- five times the",
        "  autosave debounce, so a tick would certainly have fired.",
        "Press Cancel (Enter, since it is focused).",
        "Then QUIT with ctrl+q WITHOUT typing anything else and WITHOUT",
        "  pressing ctrl+s. Quitting is what lets this script read the hash."
    )
    Expect @(
        "no write happened while the popup was up -- the hash below is",
        "  UNCHANGED from the 'before' line above.",
        "the editor is still usable after Cancel: the buffer is intact and",
        "  still dirty."
    )
    Fails @(
        "the hash changed -- autosave wrote the buffer while the dialog",
        "  was asking whether to keep it",
        "the dialog closed by itself",
        "the buffer is empty or truncated after Cancel"
    )
    Pause "step B, the Cancel half"
    Launch

    $hashB1 = HashOf $fileB
    Write-Host "  dialog.py after Cancel: $hashB1" -ForegroundColor DarkGray
    Assert-That ($hashB1 -eq $hashB0) `
        "NO write landed while the unload confirm was open (Cancel half)"

    Write-Host ""
    $intact = Read-Host "  after Cancel, was the buffer intact and still dirty? (y/n)"
    Assert-That ($intact -match '^[Yy]') "Cancel left the buffer intact and dirty"

    Do-This @(
        "Point will launch again. Same file, same start:",
        "Go to the EDITOR, open $relB, ctrl+o, TYPE A FEW CHARACTERS.",
        "IMMEDIATELY press ctrl+w then u.",
        "WAIT at least 5 seconds with the dialog open.",
        "This time choose UNLOAD (arrow left to it, then Enter).",
        "Then quit with ctrl+q."
    )
    Expect @(
        "no race: the file is EITHER untouched (the unload superseded the",
        "  pending save -- this is the designed outcome) OR a single clean,",
        "  complete write landed.",
        "the file is never half-written: it still parses as Python and ends",
        "  with a complete line.",
        "the Editor unloads; the switcher shows it as not loaded."
    )
    Fails @(
        "the file is truncated, half-written, or has a torn final line",
        "Point panics or hangs on the unload",
        "the unload is refused with no message"
    )
    Pause "step B, the Unload half"
    Launch

    $hashB2 = HashOf $fileB
    Write-Host "  dialog.py after Unload: $hashB2" -ForegroundColor DarkGray

    # Not asserted equal: BOTH outcomes are correct by this fix's own
    # decision (unload supersedes the pending save, but a write that had
    # already gone out before the dialog opened is allowed to complete).
    # What is asserted is that whatever is on disk is a WHOLE file.
    $txt = Get-Content -Raw -LiteralPath $fileB
    Assert-That ($txt.Length -gt 0) "dialog.py is not empty after the Unload half"
    Assert-That ($txt.TrimEnd().EndsWith(")")) `
        "dialog.py ends with a complete line -- no torn or partial write"
    Assert-That (-not ($txt -match "\x00")) "dialog.py contains no NUL bytes"

    if ($hashB2 -eq $hashB1) {
        Write-Host "  (the unload superseded the pending save -- the designed outcome)" -ForegroundColor DarkGray
    } else {
        Write-Host "  (a complete write landed before the unload -- also acceptable)" -ForegroundColor DarkGray
    }

    # -----------------------------------------------------------------
    Write-Host ""
    Write-Host ("=" * 72) -ForegroundColor DarkGray
    if ($script:failures.Count -eq 0) {
        Write-Host " F13b HOTFIX GATE: all automated assertions passed." -ForegroundColor Green
        Write-Host " The eyes-on steps are yours to confirm." -ForegroundColor Green
    } else {
        Write-Host " F13b HOTFIX GATE: $($script:failures.Count) assertion(s) FAILED:" -ForegroundColor Red
        foreach ($f in $script:failures) { Write-Host "   - $f" -ForegroundColor Red }
    }
    Write-Host ("=" * 72) -ForegroundColor DarkGray
}
finally {
    Remove-Item -LiteralPath $scratch -Recurse -Force -ErrorAction SilentlyContinue
    Remove-Item -LiteralPath $fixtures -Recurse -Force -ErrorAction SilentlyContinue
    Write-Host ""
    Write-Host "scratch profile and fixtures removed." -ForegroundColor DarkGray
}
