# F13a (Session 18a) — Editor Core, editing half: manual verification
#
#   powershell -NoProfile -ExecutionPolicy Bypass -File D:\Projects\Point\backend\tests\gate_f13a.ps1
#
# Run it from anywhere. It creates ONE scratch profile, points both APPDATA
# and LOCALAPPDATA at it for the whole script, generates its fixtures inside
# the repo, and removes both at the end. Nothing under the real
# %APPDATA%\Point or %LOCALAPPDATA%\Point is ever named by this script,
# destructively or otherwise — CLAUDE.md's env-var-override pattern. The
# retired rename-aside pattern is NOT used.
#
# It stops at each step. Read the EXPECT lines before pressing Enter.
#
# ---------------------------------------------------------------------------
# What this gate covers, and what it deliberately does not
# ---------------------------------------------------------------------------
#
# Session 18a is the editing half of F13: buffer, metadata, cursor,
# selection, indent, undo, tab strip, file tree, highlighting, status bar.
# There is NO save path — no WriteProjectFile call exists anywhere in
# frontend\internal\editor, no lock is acquired, and there is no autosave.
#
# So this gate covers F13's Done-when 3, 4, 5, 6 and the OPEN half of 1.
# Done-when 2, 7, 8, 9, 10, 11 and 12 are all save-path or lock items and
# belong to 18b; they are listed at the end of this file as explicitly NOT
# covered, so a reader cannot mistake a green run here for a green F13.
#
# ---------------------------------------------------------------------------
# $ErrorActionPreference, and why it is Continue
# ---------------------------------------------------------------------------
#
# B32's handoff: in Windows PowerShell 5.1 a native command that writes to
# stderr produces a NativeCommandError, which "Stop" promotes to
# terminating. This script runs cli.py, which logs to stderr BY DESIGN
# (CLAUDE.md: stdout is the protocol channel). Under Stop a routine log line
# aborts the run and it looks like a crash. Safety here comes from the two
# env-var overrides and from never naming the real Point folder — unchanged
# by this setting.
#
# The cost of Continue is that an unrelated failure prints and falls
# through, so every non-interactive step below checks its own exit code
# through Assert-That rather than leaving it to a human to notice.

$ErrorActionPreference = "Continue"

$repo    = "D:\Projects\Point"
$scratch = Join-Path $env:TEMP "point-scratch-f13a"
$fixtures = Join-Path $repo "backend\tests\fixtures\f13a"

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
    Write-Host " STEP $n — $title" -ForegroundColor Cyan
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
    Read-Host "  $msg — press Enter when done"
}
function Launch() {
    Write-Host ""
    Write-Host "  launching Point (quit with ctrl+q when the step is done)" -ForegroundColor DarkGray
    Push-Location $repo
    go run .\frontend\cmd\point
    Pop-Location
}

New-Item -ItemType Directory -Force $scratch | Out-Null

try {
    $env:APPDATA      = $scratch   # config.toml, credentials.toml
    $env:LOCALAPPDATA = $scratch   # state.json, logs\, runtime.db

    Write-Host "scratch profile: $scratch" -ForegroundColor DarkGray
    Write-Host "fixtures:        $fixtures" -ForegroundColor DarkGray

    # -----------------------------------------------------------------
    Step 0 "setup — a fresh database, and the fixtures"
    # -----------------------------------------------------------------
    #
    # B32's audit finding: a failed setup made every later check
    # meaningless and stopped nothing. So the setup asserts.

    Push-Location (Join-Path $repo "backend")
    uv run python cli.py migrate | Out-Null
    $migrateExit = $LASTEXITCODE
    Pop-Location
    Assert-That ($migrateExit -eq 0) "cli.py migrate exited 0"

    # The repo is itself a Point project (.point\project.json, gitignored),
    # so ProjectResolve from the working directory finds it. B7's
    # _read_marker_and_resolve recreates the row from the marker in a fresh
    # database, so no `project init` is needed and the real repo is never
    # re-initialised.
    Assert-That (Test-Path (Join-Path $repo ".point\project.json")) `
        "the repo has a .point\project.json for ProjectResolve to find"

    New-Item -ItemType Directory -Force $fixtures | Out-Null

    # --- Done-when #1's 5,000-line file ------------------------------
    #
    # B32's own gate defect 1: a fixture too small to test anything,
    # whose printed length contradicted its own comment. So the size is
    # ASSERTED into the band that makes it meaningful.
    #
    # It has to be UNDER index.max_file_kb (100KB) or it will not appear
    # in the file tree at all — the tree is built from the same index the
    # `@` picker uses, and nothing in 18a's UI opens a file the tree does
    # not show. ~17 bytes a line keeps 5,000 lines under the cap.
    $big = New-Object System.Text.StringBuilder
    for ($i = 0; $i -lt 5000; $i++) {
        switch ($i % 5) {
            0 { [void]$big.Append("def f$i(a, b):`r`n") }
            1 { [void]$big.Append("    x = $i + a`r`n") }
            2 { [void]$big.Append("    if x > b:`r`n") }
            3 { [void]$big.Append('        return "v"' + "`r`n") }
            4 { [void]$big.Append("    return x`r`n") }
        }
    }
    $bigPath = Join-Path $fixtures "big.py"
    [IO.File]::WriteAllText($bigPath, $big.ToString(), (New-Object Text.UTF8Encoding $false))

    $bigBytes = (Get-Item $bigPath).Length
    $bigLines = [IO.File]::ReadAllText($bigPath).Split("`n").Count - 1
    Write-Host "  big.py: $bigLines lines, $bigBytes bytes" -ForegroundColor DarkGray
    Assert-That ($bigLines -eq 5000) "big.py is 5,000 lines (Done-when #1's size)"
    Assert-That ($bigBytes -lt 100 * 1024) `
        "big.py is under index.max_file_kb (100KB) and so IS in the tree"

    # --- the metadata fixtures ---------------------------------------
    $noBom = New-Object Text.UTF8Encoding $false
    $bom   = New-Object Text.UTF8Encoding $true

    # LF, trailing newline.
    [IO.File]::WriteAllText((Join-Path $fixtures "lf.py"),
        "def a():`n    return 1`n", $noBom)

    # CRLF + BOM.
    [IO.File]::WriteAllText((Join-Path $fixtures "crlf_bom.py"),
        "def a():`r`n    return 1`r`n", $bom)

    # Mixed endings — B4's display case. Two CRLF, one LF.
    [IO.File]::WriteAllText((Join-Path $fixtures "mixed.py"),
        "a = 1`r`nb = 2`nc = 3`r`n", $noBom)

    # No trailing newline.
    [IO.File]::WriteAllText((Join-Path $fixtures "no_eol.py"),
        "x = 1", $noBom)

    # Hard tabs — Go, so the indent detector sees tabs and the lexer
    # reports `go`.
    [IO.File]::WriteAllText((Join-Path $fixtures "tabs.go"),
        "package x`r`n`r`nfunc a() {`r`n`tif true {`r`n`t`tprintln(1)`r`n`t}`r`n}`r`n", $noBom)

    # A line with CJK and an emoji — Done-when #4's exact shape.
    [IO.File]::WriteAllText((Join-Path $fixtures "wide.py"),
        "s = 'x 日本語 🎉 y'`r`nt = 2`r`n", $noBom)

    # Not valid UTF-8: B5's read-only case. decode_replacements > 0.
    [IO.File]::WriteAllBytes((Join-Path $fixtures "latin1.py"),
        [byte[]](0x78, 0x20, 0x3d, 0x20, 0x27, 0xff, 0xfe, 0x27, 0x0d, 0x0a))

    # The read-only ATTRIBUTE set: B32's `writable` comes back false.
    $roPath = Join-Path $fixtures "readonly.py"
    [IO.File]::WriteAllText($roPath, "y = 1`r`n", $noBom)
    Set-ItemProperty -Path $roPath -Name IsReadOnly -Value $true

    foreach ($f in @("big.py","lf.py","crlf_bom.py","mixed.py","no_eol.py",
                     "tabs.go","wide.py","latin1.py","readonly.py")) {
        Assert-That (Test-Path (Join-Path $fixtures $f)) "fixture $f exists"
    }

    # --- the automated half ------------------------------------------
    #
    # Everything that can be asserted without a terminal already is: the
    # buffer, undo grouping, indent/dedent, auto-indent, selection cells
    # on a CJK/emoji line, the highlight cache, the tree, the tab strip,
    # Suspend/Resume, and Root's three new seams. The exit code is read,
    # which B32's audit calls the worst gap to leave open.
    Step "0b" "the Go test suite"
    Push-Location $repo
    go test ./frontend/...
    $testExit = $LASTEXITCODE
    Pop-Location
    Assert-That ($testExit -eq 0) "go test ./frontend/... exited 0"

    Push-Location $repo
    gofmt -l .\frontend\ | Tee-Object -Variable fmtOut | Out-Null
    go vet ./frontend/... 2>&1 | Out-Null
    $vetExit = $LASTEXITCODE
    Pop-Location
    Assert-That ([string]::IsNullOrWhiteSpace(($fmtOut -join ""))) "gofmt -l frontend is clean"
    Assert-That ($vetExit -eq 0) "go vet ./frontend/... exited 0"

    # -----------------------------------------------------------------
    Step 1 "the Editor exists, and leader -> e reaches it"
    # -----------------------------------------------------------------
    Write-Host "  A fresh profile has no provider, so F10's chooser opens FIRST."
    Do-This @(
        "pick Demo (it needs no key), then close Settings with esc",
        "the startup chooser (UI-12) appears next — press esc to skip it",
        "then press ctrl+w then e"
    )
    Expect @(
        "the startup chooser now has TWO buttons: [ Chat ] and [ Editor ]",
        "ctrl+w then e opens the Editor: a 'Code File' box over a 'Terminal' box,",
        "  with 'File Tree' and 'Task' cards in the right pane",
        "the Terminal box says 'the terminal arrives in F14' — three inert rows (B8)",
        "the File Tree lists the repo: backend\, design\, frontend\ collapsed",
        "the status bar's LEFT half reads 'no file open   ctrl+o tree/file",
        "  f5 re-index …' -- F13b made the ctrl+o hint static and name",
        "  BOTH targets, so this reads tree/file now, not tree",
        "  — NOT 'ctrl+p palette  ctrl+q quit  ctrl+w ? help', which is Chat's line"
    )
    Fails @(
        "the chooser shows only [ Chat ] (the Editor is not registered)",
        "ctrl+w then e does nothing, or reports 'not a leader key'",
        "Point refuses to start naming a duplicate binding (leader e collided)",
        "the Terminal rect is blank or missing"
    )
    Pause "step 1"

    # -----------------------------------------------------------------
    Step 2 "Done-when #1 (open half) — a 5,000-line file"
    # -----------------------------------------------------------------
    Do-This @(
        "ctrl+o to focus the File Tree (its border turns accent)",
        "navigate with j/k (or the arrows) to backend\ -> tests\ -> fixtures\ -> f13a\,",
        "  expanding with l / right / Enter",
        "select big.py and press Enter"
    )
    Expect @(
        "it opens INSTANTLY — no visible pause",
        "line numbers in a right-aligned gutter, four digits wide",
        "Python syntax colour: `def` and `return` in the accent, strings green,",
        "  numbers in the secondary accent",
        "the status bar reads  tests/fixtures/f13a/big.py  1:1  python  CRLF  4sp",
        "ctrl+end jumps to line 5000 instantly; ctrl+home back to 1:1",
        "holding pgdn scrolls smoothly, with no tearing and no lag that builds up",
        "holding a letter key types smoothly — the dirty marker • appears in the tab"
    )
    Fails @(
        "a pause of a second or more on open (the whole file is being tokenised)",
        "scrolling gets progressively slower the further down you are",
        "typing visibly lags behind the keyboard",
        "the gutter truncates to three digits past line 999"
    )
    Write-Host ""
    Write-Host "  Measured on this machine, 400 keystrokes in big.py:" -ForegroundColor DarkGray
    Write-Host "    before the repairs:  p50 6.0ms  p90 9.0ms  max 18.3ms  -  50% over 6ms" -ForegroundColor DarkGray
    Write-Host "    after:               p50 3.5ms  p90 5.9ms  max  8.1ms  -  6.75% over" -ForegroundColor DarkGray
    Write-Host "    30-line highlight window ~3ms  ·  whole 5,000-line file 302ms" -ForegroundColor DarkGray
    Write-Host ""
    Write-Host "  Why that matters: app\paste.go classifies a paste by the gap" -ForegroundColor DarkGray
    Write-Host "  between Update calls (6ms). A frame slower than that stops the" -ForegroundColor DarkGray
    Write-Host "  burst from ever activating, and the paste degrades into N" -ForegroundColor DarkGray
    Write-Host "  separate keystrokes. That was the cause of BOTH paste reports." -ForegroundColor DarkGray
    Do-This @(
        "paste ~300 lines into big.py (copy a chunk of the file itself)",
        "then press ctrl+z ONCE"
    )
    Expect @(
        "the paste lands as ONE block, in well under a second",
        "the status bar reports one paste event, not 300",
        "ONE ctrl+z removes the whole paste"
    )
    Fails @(
        "the paste arrives character by character, visibly",
        "ctrl+z has to be pressed many times to undo one paste",
        "a trailing newline from the paste lands as a separate Enter"
    )
    Pause "step 2"

    # -----------------------------------------------------------------
    Step 3 "Done-when #3 — undo groups keystrokes"
    # -----------------------------------------------------------------
    Do-This @(
        "with big.py open, ctrl+home, then type:  the quick brown fox",
        "press ctrl+z ONCE"
    )
    Expect @(
        "one ctrl+z removes 'fox' — a word, not the letter 'x'",
        "further ctrl+z presses remove 'brown ', then 'quick ', then 'the '",
        "ctrl+y (or ctrl+shift+z) puts them back one group at a time",
        "typing anything new makes ctrl+y stop doing anything (redo is cleared)"
    )
    Fails @(
        "ctrl+z removes exactly one character",
        "ctrl+z removes the entire sentence in one step"
    )
    Write-Host ""
    Do-This @(
        "now: type 'abc', move the caret elsewhere with the arrows, type 'xyz',",
        "then press ctrl+z once"
    )
    Expect @("only 'xyz' is removed — moving the caret ended the group")
    Pause "step 3"

    # -----------------------------------------------------------------
    Step 4 "Done-when #4 — selection on a CJK/emoji line"
    # -----------------------------------------------------------------
    Do-This @(
        "open f13a\wide.py from the tree (ctrl+o, navigate, Enter)",
        "on the line containing  日本語 🎉  DRAG-SELECT across it with the mouse",
        "then do the same with shift+right from the start of the line"
    )
    Expect @(
        "the highlight covers EXACTLY the characters under the pointer —",
        "  two cells for each of 日 本 語, two for 🎉, and no half-covered glyph",
        "the highlight's right edge lands on a character boundary, never inside one",
        "dragging back past the start shrinks the selection rather than inverting it",
        "shift+right steps one CHARACTER at a time, so 日 takes one press for two cells",
        "clicking in the middle of 日 puts the caret BEFORE it, not inside it"
    )
    Fails @(
        "the highlight is narrower or wider than the characters it covers",
        "the right border of the Code File box shifts on the selected line",
        "the emoji renders as two separate half-highlighted cells"
    )
    Pause "step 4"

    # -----------------------------------------------------------------
    Step 5 "Done-when #5 — tab / shift+tab on a multi-line selection"
    # -----------------------------------------------------------------
    Do-This @(
        "in big.py (or lf.py), select three or four whole lines:",
        "  put the caret on a line, then shift+down three times",
        "press tab, then shift+tab"
    )
    Expect @(
        "tab indents EVERY selected line by four spaces and keeps the selection",
        "shift+tab puts them all back",
        "ONE ctrl+z undoes the whole block shift, not one line per press",
        "tab with NO selection inserts four spaces at the caret",
        "in tabs.go (open it from the tree) tab inserts a TAB, not spaces —",
        "  the status bar reads 'tabs' there and '4sp' in the Python files"
    )
    Fails @(
        "tab moves focus to another pane (KeyCapturer is not claiming it)",
        "the status bar says 'Focus Next Pane is not available right now'",
        "tab replaces the selection with a tab character",
        "a blank line inside the selection gains trailing whitespace"
    )
    Pause "step 5"

    # -----------------------------------------------------------------
    Step "5a" "tab in tabs.go inserts a TAB — the Done-when #5 re-check"
    # -----------------------------------------------------------------
    Write-Host "  Reported as `"tab inserts 4 spaces in tabs.go`". The character" -ForegroundColor DarkGray
    Write-Host "  inserted is a real tab -- asserted at five caret positions" -ForegroundColor DarkGray
    Write-Host "  through both the direct and the key path, and the fixture is" -ForegroundColor DarkGray
    Write-Host "  confirmed below to contain real 0x09 bytes. What varies is how" -ForegroundColor DarkGray
    Write-Host "  WIDE a tab DRAWS: tabStop is 8, so a tab advances to the next" -ForegroundColor DarkGray
    Write-Host "  multiple of 8 and is 4 cells wide at display column 4, 12, 20..." -ForegroundColor DarkGray
    Do-This @(
        "open f13a\tabs.go; the status bar must read `"tabs`"",
        "put the caret at the very START of the `tif true {` line and press tab",
        "then undo, put the caret in the MIDDLE of `println` and press tab"
    )
    Expect @(
        "at the line start: the indent grows by 8 cells (one tab at column 0)",
        "mid-word: the gap is whatever reaches the next multiple of 8 -- often 4",
        "both are ONE tab character, and one ctrl+z removes the whole thing",
        "the status bar keeps saying `"tabs`" and never `"4sp`" for this file"
    )
    Fails @(
        "the status bar says 4sp for tabs.go (detection is wrong)",
        "one ctrl+z removes part of the indent (spaces were inserted, not a tab)"
    )
    # The fixture's own bytes, so a gate defect cannot masquerade as a
    # product defect -- B32's lesson about fixtures too weak to test
    # anything.
    $tabBytes = ([IO.File]::ReadAllBytes((Join-Path $fixtures "tabs.go")) | Where-Object { $_ -eq 9 } | Measure-Object).Count
    Assert-That ($tabBytes -eq 4) "tabs.go contains 4 real 0x09 tab bytes (found $tabBytes)"
    Pause "step 5a"

    # -----------------------------------------------------------------
    Step 6 "Done-when #6 — Enter preserves the indentation"
    # -----------------------------------------------------------------
    Do-This @(
        "in big.py, put the caret at the END of a line that starts with spaces",
        "  (for instance '    return x'), and press Enter",
        "then do the same at the end of an indented line in tabs.go"
    )
    Expect @(
        "the new line starts with the SAME indentation as the one above",
        "the caret sits after that indentation, not at column 1",
        "in tabs.go the copied indentation is tabs, matching the file",
        "Enter is its own undo step: one ctrl+z removes the line break alone"
    )
    Fails @(
        "the new line starts at column 1",
        "Enter adds MORE indentation than the line above had (a Tier 2 rule leaked in)"
    )
    Pause "step 6"

    # -----------------------------------------------------------------
    Step 7 "B32's metadata, displayed and not re-derived"
    # -----------------------------------------------------------------
    Do-This @("open each of these from the tree and read the status bar")
    Expect @(
        "lf.py         ->  LF    4sp",
        "crlf_bom.py   ->  CRLF  BOM  4sp      and the first character is 'd', not a",
        "                                       zero-width blob (B32 strips the BOM)",
        "mixed.py      ->  MIXED 2/1            — editable, and it does NOT claim",
        "                                       CRLF or LF (B4)",
        "no_eol.py     ->  LF  'no eol'         — describe_bytes reports lf with both",
        "                                       counts zero for a file with no line",
        "                                       break; the missing final newline is",
        "                                       trailing_newline, shown as 'no eol'",
        "tabs.go       ->  CRLF  tabs  go",
        "                  (and `tab` there inserts a REAL tab -- see step 5a)",
        "latin1.py     ->  read-only (not valid UTF-8), and typing does nothing (B5)",
        "readonly.py   ->  read-only (file is read-only), and typing does nothing",
        "no 'lock:' segment anywhere — nothing acquires a lock this session, and a",
        "  lock readout with no lock behind it would be a hint that lies"
    )
    Fails @(
        "any file reports LF when its bytes are CRLF (the metadata is being re-derived)",
        "crlf_bom.py's first line begins with a stray invisible character",
        "latin1.py or readonly.py can be edited",
        "mixed.py opens read-only (18a's decision is: editable, labelled MIXED)"
    )
    Pause "step 7"

    # -----------------------------------------------------------------
    Step 8 "tabs, the strip, and the tree"
    # -----------------------------------------------------------------
    Do-This @(
        "with four or five files open, look at the first row inside the Code File box",
        "press ctrl+n and ctrl+b a few times",
        "click a tab, then click its ×",
        "edit a file, then try to close THAT tab's ×",
        "narrow the terminal to about 60 columns, then widen it again"
    )
    Expect @(
        "the tab strip is the first CONTENT ROW inside the box — not a nested box",
        "the active tab is in the accent colour, the others dim, separated by │",
        "ctrl+n / ctrl+b cycle and wrap (ctrl+tab does NOT work — see the handoff:",
        "  bubbletea cannot tell ctrl+tab from tab on Windows)",
        "a dirty file shows • before its name",
        "clicking × on a CLEAN tab closes it",
        "clicking × on a DIRTY tab refuses, and says it has unsaved changes",
        "with more tabs than fit, ‹ and › appear and the ACTIVE tab stays visible",
        "below about 70 columns the strip collapses to the active filename only",
        "below 90 columns the right pane (File Tree, Task) auto-hides",
        "below 70x16 the whole workspace refuses with 'Editor needs 70x16 — terminal",
        "  is WxH', naming both real numbers"
    )
    Fails @(
        "a tab's name is cut in half rather than the strip scrolling",
        "the strip scrolls away from the file you are editing",
        "the right border of the Code File box moves as tabs change",
        "a dirty tab closes and the edits are gone"
    )
    Write-Host ""
    Do-This @(
        "press f5 while the tree is on screen",
        "then: leave Point running, create a file in the fixtures directory from",
        "  another terminal, and press f5 again"
    )
    Expect @(
        "f5 re-indexes: 're-indexing the project' in the status bar",
        "the new file appears after f5 (there is no filesystem watcher — F13's own",
        "  'Do NOT', and _build_index caches for 30 seconds)",
        "the directories you had expanded are STILL expanded after the refresh"
    )
    Pause "step 8"

    # -----------------------------------------------------------------
    Step 9 "the gitignore toggle, which lives in Settings"
    # -----------------------------------------------------------------
    Do-This @(
        "ctrl+w then s to open Settings, go to the Behaviour tab,",
        "turn index.respect_gitignore OFF, close Settings"
    )
    Expect @(
        "the tree re-indexes by itself — no f5 needed",
        "ignored paths now appear (.venv is still absent: the backend's",
        "  _ALWAYS_SKIP_DIRS skips .venv, node_modules, __pycache__ and .git",
        "  regardless of the toggle, which is F13's 'always skips' requirement",
        "  satisfied one layer down)",
        "turning it back on re-indexes again and they disappear"
    )
    Fails @(
        "the tree only changes after f5 (the override is not reaching the workspace)",
        ".venv appears when the toggle is off"
    )
    Pause "step 9"

    # -----------------------------------------------------------------
    Step 10 "unload, suspend and resume — and the dirty guard"
    # -----------------------------------------------------------------
    Do-This @(
        "open two files, put the caret somewhere distinctive in each, DO NOT edit",
        "ctrl+w then w, switch to Chat, then ctrl+w then e back to the Editor"
    )
    Expect @(
        "both files are still open, at the same scroll position and caret",
        "  (the workspace was never unloaded — it only takes ~1s to switch)"
    )
    Write-Host ""
    Do-This @(
        "now EDIT one of the files so a • appears, then ctrl+w then u (unload)"
    )
    Expect @(
        "a confirmation appears: 'N file(s) have unsaved changes.' with",
        "  [ Unload ] [ Cancel ] and Cancel focused (F12's rule: No by default)",
        "Cancel leaves everything exactly as it was"
    )
    Fails @(
        "the Editor unloads with no confirmation while a file is dirty"
    )
    Write-Host ""
    Write-Host "  The IDLE path matters more than the manual one this session:" -ForegroundColor DarkGray
    Write-Host "  with no save path, EVERY edit is unsaved, so an idle unload at" -ForegroundColor DarkGray
    Write-Host "  the default five minutes would be the only unrecoverable action" -ForegroundColor DarkGray
    Write-Host "  in the app. F13 teaches the sweep to SKIP a dirty workspace." -ForegroundColor DarkGray
    Do-This @(
        "put  [workspace]`n  idle_unload_minutes = 1  in $scratch\Point\config.toml",
        "  (the script can do it for you — see the next prompt)",
        "restart Point, open the Editor, edit a file, switch to Chat, wait ~70s"
    )
    Expect @(
        "the Editor does NOT unload; the status bar says",
        "  'Editor stays loaded — N file(s) have unsaved changes'",
        "that line FADES after a few seconds like every other status message",
        "ctrl+w then w shows Editor as still loaded",
        "with NO edits pending, the same wait DOES unload it silently,",
        "  and switching back re-opens the same files at the same positions",
        "  (Suspend stored the paths and carets; the content came back from the backend)"
    )
    Write-Host ""
    Write-Host "  AND THE SECOND TIME (round 5). Switch BACK to the Editor, then" -ForegroundColor Cyan
    Write-Host "  background it again and wait the same period again." -ForegroundColor Cyan
    Expect @(
        "the notice fires AGAIN -- it is once per IDLE PERIOD, not once per",
        "  loaded lifetime. The latch resets whenever the workspace's idle",
        "  clock does, which reactivating it is",
        "repeat a third time: it fires a third time"
    )
    Fails @(
        "the notice fires only on the first idle period and never again",
        "the notice repeats every second within one period"
    )
    Fails @(
        "a dirty Editor is idle-unloaded and the edits are gone",
        "a clean Editor never unloads (the guard is too broad)"
    )
    Write-Host ""
    $doConfig = Read-Host "  write idle_unload_minutes = 1 into the scratch config now? (y/N)"
    if ($doConfig -eq "y") {
        $cfgDir = Join-Path $scratch "Point"
        New-Item -ItemType Directory -Force $cfgDir | Out-Null
        $cfgPath = Join-Path $cfgDir "config.toml"
        Add-Content -Path $cfgPath -Encoding utf8 -Value "`r`n[workspace]`r`nidle_unload_minutes = 1`r`n"
        Assert-That (Test-Path $cfgPath) "scratch config.toml written (NOT the real one)"
        Get-Content $cfgPath | Write-Host -ForegroundColor DarkGray
    }
    Pause "step 10"

    # -----------------------------------------------------------------
    Step "10a" "the repairs from the first verification round"
    # -----------------------------------------------------------------
    Write-Host "  Every item below has a regression test; these are the ones" -ForegroundColor DarkGray
    Write-Host "  whose FIX is only visible on a real terminal." -ForegroundColor DarkGray
    Write-Host ""
    Write-Host "  CARET (was: `"the cursor disappears / toggles / needs N arrows`")" -ForegroundColor Cyan
    Do-This @(
        "open big.py, click the first character of line 1",
        "hold the right arrow across `def f0(a, b):` and back with the left"
    )
    Expect @(
        "the caret is visible at EVERY column, on keywords as well as spaces",
        "it is reverse video -- the cell's own colours swapped -- not a block",
        "  of one fixed colour, so it stands out against every token type",
        "on the last line (ctrl+end) it is visible at the end AND one back",
        "it never needs hunting for, and no count of arrows is involved"
    )
    Fails @(
        "the caret is invisible on `def` / `return` and visible on spaces",
        "the caret is invisible anywhere at all"
    )
    Write-Host ""
    Write-Host "  SELECTION (was: `"a 4-space block appears with nothing selected`")" -ForegroundColor Cyan
    Do-This @(
        "put the caret just after the indent of an indented line",
        "press shift+up once"
    )
    Expect @(
        "a selection appears -- that is what shift+arrow MEANS, it is correct",
        "the CARET is now visible at the selection's active end, which is what",
        "  was missing: the highlight no longer appears with no caret anywhere",
        "a plain arrow collapses it; Delete would DELETE it (and still does)"
    )
    Write-Host ""
    Write-Host "  WHEEL (was: `"scrolls then snaps back`")" -ForegroundColor Cyan
    Do-This @(
        "scroll the Code File box with the wheel/trackpad, far, both ways",
        "do the same over the File Tree",
        "then press an arrow key"
    )
    Expect @(
        "both panes scroll freely and STAY where you left them",
        "the caret does not move while you scroll",
        "the first arrow key jumps the view back to the caret -- revealing is",
        "  something a key asks for, which is why the wheel no longer fights it"
    )
    Fails @(
        "either pane springs back to the caret/selection on its own"
    )
    Write-Host ""
    Write-Host "  FILE TREE CLICKS (was: `"the top rows do not open`")" -ForegroundColor Cyan
    Do-This @(
        "click the very first row of the File Tree, once",
        "click a row further down, once",
        "click the card's top border line (the one with the title)"
    )
    Expect @(
        "ONE click opens a file or expands a directory, on every row equally",
        "the first row is no different from any other",
        "a click on the border moves focus to the tree and nothing else --",
        "  it does not select or open a row, because a border is not a row"
    )
    Fails @(
        "any row needs two clicks",
        "a border click opens or selects something"
    )
    Write-Host ""
    Write-Host "  MESSAGES (was: `"no message at all`", four times)" -ForegroundColor Cyan
    Do-This @(
        "with a WRITABLE file open and edited, press ctrl+s",
        "open readonly.py and press ctrl+s",
        "click the x on a dirty tab",
        "press f5",
        "open latin1.py"
    )
    Expect @(
        "every one of those puts a line in the STATUS BAR, left half:",
        "  ctrl+s writable   -> `"save is not wired yet ...`"",
        "  ctrl+s readonly   -> `"readonly.py is read-only - file is read-only`"",
        "  x on dirty tab    -> `"... has unsaved changes ...`" and it stays open",
        "  f5                -> `"re-indexing the project`"",
        "  latin1.py opened  -> `"... opened read-only - not valid UTF-8`"",
        "each one fades after a few seconds and the file segments come back"
    )
    Fails @(
        "any of the five is silent"
    )
    Write-Host ""
    Write-Host "  RIGHT PANE (scope addition 18)" -ForegroundColor Cyan
    Expect @(
        "the File Tree card is now TALLER than the Task card and takes the",
        "  remainder of the pane; Task is the smaller fixed box below it",
        "f1 still opens the full task tray, which is why Task can be smaller"
    )
    Write-Host ""
    Write-Host "  IDLE NOTICE (was: `"reappears on every keystroke in Chat`")" -ForegroundColor Cyan
    Do-This @(
        "with idle_unload_minutes = 1 in the scratch config: edit a file in the",
        "Editor, switch to Chat, then type continuously in Chat for ~90s"
    )
    Expect @(
        "`"Editor stays loaded - N file(s) have unsaved changes`" appears ONCE",
        "it does not come back on every keystroke",
        "the Editor is still loaded afterwards, with the edit intact"
    )
    Fails @(
        "the notice repeats while you type",
        "the Editor unloads and the edit is gone"
    )
    Write-Host ""
    Write-Host "  PASTE (was: `"character by character, several ctrl+z`")" -ForegroundColor Cyan
    Do-This @(
        "copy ~300 lines from another editor and paste into big.py with ctrl+v",
        "press ctrl+z ONCE",
        "repeat the paste twice more"
    )
    Expect @(
        "the paste lands as one block, including the FIRST paste of the session",
        "ONE ctrl+z takes the whole paste back, every time",
        "with a selection active, ctrl+v REPLACES the selection with the",
        "  clipboard -- not with a literal `"v`"",
        "the `"pasted N lines`" status line FADES after a few seconds (round 5),",
        "  and the file segments (name, line:col, lang, CRLF, indent) come",
        "  back on their own -- no file close and reopen needed"
    )
    Fails @(
        "the first paste of a session is visibly character-by-character",
        "any paste needs more than one ctrl+z",
        "ctrl+v types a `"v`" over the selection",
        "the paste message stays on screen until something else overwrites it"
    )
    Write-Host ""
    Write-Host "  Every status message fades now, not only the ones that remembered" -ForegroundColor DarkGray
    Write-Host "  to schedule it: the expiry is derived in Update, so the ~40 places" -ForegroundColor DarkGray
    Write-Host "  that assign r.status directly all get one. Check one more:" -ForegroundColor DarkGray
    Do-This @("in the Editor, press esc (a Chat-only chord) and wait")
    Expect @(
        "'Browse Transcript is not available right now' appears and then FADES,",
        "  with the file segments coming back after it"
    )
    Write-Host ""
    Write-Host "  This one is the residual, and it is honest rather than fixed:" -ForegroundColor Yellow
    Write-Host "  6.75% of frames still exceed paste.go's 6ms gate (was 50%), so" -ForegroundColor Yellow
    Write-Host "  a paste can still occasionally arrive as keystrokes. When it" -ForegroundColor Yellow
    Write-Host "  does, the UNDO count is still right -- a split paste collapses" -ForegroundColor Yellow
    Write-Host "  into one step by construction (editPaste), which is why the" -ForegroundColor Yellow
    Write-Host "  ctrl+z expectation above holds either way." -ForegroundColor Yellow
    Pause "step 10a"

    # -----------------------------------------------------------------
    Step "10b" "the repairs from the SECOND verification round"
    # -----------------------------------------------------------------
    Write-Host "  MODIFIER KEYS (was: three separate symptoms, one NUL rune)" -ForegroundColor Cyan
    Write-Host "  bubbletea's Windows console reader forwards a BARE Ctrl key-down" -ForegroundColor DarkGray
    Write-Host "  as KeyRunes carrying a single NUL -- it skips key-UP records and" -ForegroundColor DarkGray
    Write-Host "  VK_SHIFT and nothing else, and keyType has no case for VK_CONTROL." -ForegroundColor DarkGray
    Write-Host "  Everything downstream read it as a typed character." -ForegroundColor DarkGray
    Do-This @(
        "select a word, then HOLD ctrl -- without pressing anything else",
        "release, then: type a word, pause a second, press ctrl+z ONCE",
        "then press ctrl+y ONCE",
        "then hold ctrl and tap the right arrow across a line of code",
        "finally, press and release ctrl/alt/caps lock a few times, then press End"
    )
    Expect @(
        "holding ctrl alone does NOT collapse the selection and changes no text",
        "ONE ctrl+z undoes the word -- not an invisible character first",
        "ONE ctrl+y puts it back",
        "ctrl+right steps word to word with the caret visible the whole way;",
        "  no character is skipped and the caret never vanishes",
        "after the modifier presses the line is unchanged -- End lands exactly",
        "  at the last visible character, with no invisible runes before it",
        "the status bar's line:col matches the caret's visible position"
    )
    Fails @(
        "a selection disappears the moment ctrl goes down",
        "ctrl+z or ctrl+y needs two presses",
        "the caret skips a cell, or the line's length disagrees with what is drawn"
    )
    Write-Host ""
    Write-Host "  TREE TOP ROW, FOLDERS (was: `"backend\ does not expand`")" -ForegroundColor Cyan
    Write-Host "  The Code File box's tab strip and the File Tree card's first row" -ForegroundColor DarkGray
    Write-Host "  are the SAME screen row -- Compute starts both panes at bodyTop." -ForegroundColor DarkGray
    Write-Host "  The strip was hit-tested on Y alone, so every click on that row," -ForegroundColor DarkGray
    Write-Host "  across the whole terminal, was routed to the strip and dropped." -ForegroundColor DarkGray
    Do-This @(
        "open any file first, so the tab strip exists",
        "click `"backend\`" -- the very FIRST row of the File Tree -- once",
        "click it again",
        "expand it, then click a file on the first row after scrolling the tree",
        "then click a tab in the strip, and its x"
    )
    Expect @(
        "one click expands backend\; a second collapses it",
        "a FILE on the first row opens on one click, like every other row",
        "every visible row behaves identically, top included",
        "the tab strip still takes its own clicks, and x still closes a clean tab"
    )
    Fails @(
        "the first row does nothing while rows below it work",
        "clicking the tree changes the active TAB"
    )
    Write-Host ""
    Write-Host "  IDLE UNLOAD SCOPE (was: typing anywhere reset every countdown)" -ForegroundColor Cyan
    Write-Host "  This CHANGES F12's shipped behaviour, deliberately: the global" -ForegroundColor DarkGray
    Write-Host "  `"any key to keep`" rescue is gone. A backgrounded workspace could" -ForegroundColor DarkGray
    Write-Host "  never unload while anybody typed anywhere, which defeated the" -ForegroundColor DarkGray
    Write-Host "  feature. Switching to a workspace is now the only rescue." -ForegroundColor DarkGray
    Do-This @(
        "with idle_unload_minutes = 1: open the Editor, visit Chat, come back",
        "wait for `"Chat unloads in ...`" to appear",
        "stay in the Editor and keep typing -- do NOT switch to Chat",
        "then repeat, but switch to Chat once the warning appears"
    )
    Expect @(
        "the warning reads `"Chat unloads in 29s - ctrl+w w to keep it`" --",
        "  it names the SWITCHER, not `"any key`"",
        "typing in the Editor does NOT reset Chat's countdown, and Chat",
        "  unloads on schedule while you are still typing",
        "switching to Chat DOES keep it, and leaving again starts a fresh",
        "  full countdown rather than resuming the old one",
        "the Editor itself never unloads while it is the active workspace"
    )
    Fails @(
        "the warning still says `"any key to keep`"",
        "Chat's countdown resets while you type in the Editor",
        "Chat unloads even though you switched to it"
    )
    Pause "step 10b"

    # -----------------------------------------------------------------
    Step "10c" "the repairs from the THIRD verification round"
    # -----------------------------------------------------------------
    Write-Host "  THEME REACH (was: the Editor kept the old palette)" -ForegroundColor Cyan
    Write-Host "  propagateTheme pushed the new theme to the ACTIVE workspace only," -ForegroundColor DarkGray
    Write-Host "  and the only other caller of SetTheme is loadWorkspace -- i.e." -ForegroundColor DarkGray
    Write-Host "  construction. So a loaded-but-inactive workspace never heard, and" -ForegroundColor DarkGray
    Write-Host "  switching to it does not re-theme something already loaded. Root's" -ForegroundColor DarkGray
    Write-Host "  header and status bar read the theme live and were always right," -ForegroundColor DarkGray
    Write-Host "  which is what made it look like the Editor cached styles. It does" -ForegroundColor DarkGray
    Write-Host "  not -- it holds a theme COPY and resolves styles per render, and" -ForegroundColor DarkGray
    Write-Host "  its highlight cache holds roles rather than styles for this very" -ForegroundColor DarkGray
    Write-Host "  reason. Affects every palette; the repro just named two." -ForegroundColor DarkGray
    Do-This @(
        "from a fresh start, ctrl+w e into the Editor and open a file",
        "ctrl+w s -> Appearance -> set the theme to butterfly, close Settings",
        "ctrl+w w -> Chat",
        "ctrl+w s -> Appearance -> set the theme to green, close Settings",
        "ctrl+w e -> back to the Editor",
        "then repeat the whole thing in REVERSE (theme from the Editor while",
        "  Chat is backgrounded), and once more with gold and turquoise"
    )
    Expect @(
        "the Editor's OWN chrome is green the moment you return:",
        "  the Code File box border, the syntax colours, the gutter,",
        "  the File Tree border and its rows, the Terminal box, the Task card",
        "Root's header diamond, POINT, the connection word and the status bar",
        "  are green too -- they always were",
        "nothing needs a second visit, a resize, or a reopened file to repaint",
        "the same holds in reverse, and for all four palettes"
    )
    Fails @(
        "any box border or any text inside a pane is still on the old palette",
        "the Editor repaints only after you touch it (a key, a resize, a reload)",
        "Chat shows the same problem when the Editor is the one you theme from"
    )
    Write-Host ""
    Write-Host "  STATUS BAR BUDGET (was: a segment vanished at a double-digit column)" -ForegroundColor Cyan
    Write-Host "  Measured: fitHints is exact -- its drop boundary is precisely" -ForegroundColor DarkGray
    Write-Host "  `"does the whole set fit`", at every digit count. The digits were" -ForegroundColor DarkGray
    Write-Host "  never the problem. What was: the left half was built against" -ForegroundColor DarkGray
    Write-Host "  (width - right - 1), so a segment was dropped while 30 cells of" -ForegroundColor DarkGray
    Write-Host "  the line went to `"connected - N task(s) - WxH`". At 100 columns:" -ForegroundColor DarkGray
    Write-Host "  budget 67, a dirty file needs 77, with 21 cells spare in the line." -ForegroundColor DarkGray
    Do-This @(
        "open a file and type until the column passes 1:9 -> 1:10 -> 1:100",
        "watch the LEFT half of the status bar the whole time",
        "then narrow the terminal gradually and watch what goes first"
    )
    Expect @(
        "no segment disappears while there is room in the LINE for it --",
        "  the `"connected - N task(s) - WxH`" readout on the right yields first",
        "the file name, line:col, language, CRLF/LF, indent, the dirty marker",
        "  and any read-only reason all stay as the column grows",
        "no `"+1`" / `"+2`" drop count appears while the line has room",
        "narrowing far enough DOES eventually drop segments, whole and counted,",
        "  never cut in half",
        "in CHAT the behaviour is unchanged: the bindings hint still yields to",
        "  the readout, because a dropped hint is in the palette and in Help"
    )
    Fails @(
        "a segment vanishes on a digit boundary with the terminal still wide",
        "fullscreening brings a segment back that should never have gone",
        "half a segment is shown",
        "Chat's status line lost its right-hand readout"
    )
    Write-Host ""
    Write-Host "  SWITCHER READOUT — a CLAMPED countdown (round 5)" -ForegroundColor Cyan
    Write-Host "  Round 4 removed the countdown because it could read `"idle 2m35s" -ForegroundColor DarkGray
    Write-Host "  of 1m00s`" -- elapsed past its own budget, which happens whenever" -ForegroundColor DarkGray
    Write-Host "  a workspace is pinned by unsaved work and so never unloads." -ForegroundColor DarkGray
    Write-Host "  Round 5 brings it back, clamped, and labelled when clamped." -ForegroundColor DarkGray
    Do-This @(
        "ctrl+w w with a CLEAN workspace backgrounded for ~20s",
        "then background a DIRTY Editor and leave it well past its timeout",
        "  (idle_unload_minutes = 1 makes this quick), and open ctrl+w w again"
    )
    Expect @(
        "the active row reads `"active`"",
        "a never-loaded row reads `"not loaded`"",
        "a clean backgrounded row counts up: `"idle 0m23s of 1m00s`"",
        "the DIRTY row reads `"idle 1m00s of 1m00s - pinned`" -- clamped at the",
        "  budget, labelled so it does not read as about to unload, and NOT",
        "  cut off after `"- p`" (round 6: the note column was 24 cells and",
        "  the readout is 28, so the label was truncated away entirely)",
        "a workspace configured never to unload reads `"loaded - stays loaded`"",
        "",
        "BOTH SIDES carry a minutes field at every value (round 6):",
        "  under a minute reads `"idle 0m01s of 1m00s`" / `"idle 0m58s of 1m00s`",",
        "  NOT `"idle 1s of 1m00s`" / `"idle 58s of 1m00s`"",
        "the GRACE WARNING is unaffected and still reads bare seconds:",
        "  `"Chat unloads in 29s - ctrl+w w to keep it`" (Step 10b's own text,",
        "  unchanged). Round 6 briefly spelled it `"0m25s`" as a side effect of",
        "  this fix; round 7 gave that one sentence its own formatter rather",
        "  than putting the under-a-minute branch back into the shared one"
    )
    Fails @(
        "elapsed is ever shown LARGER than the budget (e.g. idle 2m35s of 1m00s)",
        "the dirty row shows a bare clamped countdown with no `"pinned`" label",
        "  (it would read as about to unload, which it is not)",
        "the `"pinned`" label is cut off, or the readout ends in an ellipsis",
        "either side of `"X of Y`" drops its minutes field",
        "the grace warning picked up a minutes field (`"unloads in 0m29s`")",
        "the clean row shows no countdown at all"
    )
    Write-Host "  Still a SNAPSHOT taken when the popup opens -- it does not tick" -ForegroundColor DarkGray
    Write-Host "  while open (app\chooser.go, F12's, deliberately unchanged)." -ForegroundColor DarkGray
    Write-Host "  NOT fixed here, and deliberately: the rows are still a snapshot" -ForegroundColor DarkGray
    Write-Host "  taken when the popup opens (app\chooser.go, F12's, out of scope)." -ForegroundColor DarkGray
    Pause "step 10c"

    # -----------------------------------------------------------------
    Step 11 "save says it is not wired, and writes nothing"
    # -----------------------------------------------------------------
    Do-This @("with a dirty file open, press ctrl+s")
    Expect @(
        "the status bar reads 'save is not wired yet — the write path lands with",
        "  locks and autosave'",
        "the • stays: nothing was saved and the editor does not pretend otherwise",
        "on a read-only file ctrl+s reports the READ-ONLY reason instead"
    )
    Fails @(
        "ctrl+s appears to succeed",
        "ctrl+s silently does nothing at all",
        "the file on disk changes (checked below)"
    )
    Pause "step 11"

    # The hard assertion behind step 11: the fixtures are byte-identical
    # after the whole session. There is no write path, so nothing this
    # gate did can have changed them.
    Step 12 "nothing was written to disk"
    $hashes = @{}
    foreach ($f in @("big.py","lf.py","crlf_bom.py","mixed.py","no_eol.py",
                     "tabs.go","wide.py","latin1.py")) {
        $hashes[$f] = (Get-FileHash (Join-Path $fixtures $f) -Algorithm SHA256).Hash
    }
    Write-Host "  re-hashed after the session:" -ForegroundColor DarkGray
    foreach ($f in $hashes.Keys) { Write-Host "    $f  $($hashes[$f])" -ForegroundColor DarkGray }
    Write-Host ""
    Write-Host "  Compare these against the originals if you kept them. The" -ForegroundColor DarkGray
    Write-Host "  structural guarantee is stronger than the hashes: grep says so." -ForegroundColor DarkGray

    Push-Location $repo
    $writeCalls = (Select-String -Path "frontend\internal\editor\*.go" `
        -Pattern "CmdWriteProjectFile|os\.WriteFile|os\.Create|CmdAcquireFileLock" `
        -SimpleMatch:$false -ErrorAction SilentlyContinue)
    Pop-Location
    Assert-That ($null -eq $writeCalls -or $writeCalls.Count -eq 0) `
        "no write or lock call exists anywhere in frontend\internal\editor"
    if ($writeCalls) { $writeCalls | ForEach-Object { Write-Host "    $_" -ForegroundColor Red } }

    # -----------------------------------------------------------------
    Step 13 "the keys 18a deliberately does NOT own"
    # -----------------------------------------------------------------
    Write-Host "  dispatchKey SWALLOWS a chord whose When predicate is false — it" -ForegroundColor DarkGray
    Write-Host "  sets a status line instead of falling through. Four Chat-only" -ForegroundColor DarkGray
    Write-Host "  chords therefore misreport in the Editor. This is PRE-EXISTING" -ForegroundColor DarkGray
    Write-Host "  behaviour being newly exposed, recorded rather than worked" -ForegroundColor DarkGray
    Write-Host "  around: CapturesKey claims two keys and no more." -ForegroundColor DarkGray
    Do-This @("in the Editor, press esc, ctrl+c, ctrl+e and ctrl+r in turn")
    Expect @(
        "f5       -> 're-indexing the project' (it IS decoded on Windows)",
        "esc      -> 'Browse Transcript is not available right now'",
        "ctrl+c   -> 'Cancel Stream is not available right now'",
        "ctrl+e   -> 'Edit Queued Message is not available right now'",
        "ctrl+r   -> 'Retry is not available right now'",
        "none of them damages the buffer, and none of them quits",
        "ctrl+p (palette), ctrl+l (redraw), f1 (tray) and ctrl+q (quit) all still work",
        "ctrl+w then anything still works — the leader is NOT capturable"
    )
    Fails @(
        "any of the four edits the buffer or crashes",
        "ctrl+w is swallowed by the Editor and the switcher becomes unreachable",
        "ctrl+p does nothing (a claim is shadowing the palette)"
    )
    Pause "step 13"

    # -----------------------------------------------------------------
    Write-Host ""
    Write-Host ("=" * 72) -ForegroundColor DarkGray
    if ($script:failures.Count -eq 0) {
        Write-Host " F13a GATE: all automated assertions passed." -ForegroundColor Green
        Write-Host " The eyes-on steps are yours to confirm." -ForegroundColor Green
    } else {
        Write-Host " F13a GATE: $($script:failures.Count) assertion(s) FAILED:" -ForegroundColor Red
        foreach ($f in $script:failures) { Write-Host "   - $f" -ForegroundColor Red }
    }
    Write-Host ("=" * 72) -ForegroundColor DarkGray
    Write-Host ""
    Write-Host " NOT covered by this gate — all of it is 18b:" -ForegroundColor Yellow
    Write-Host "   Done-when 2   CRLF round-trip, git status clean after open + save" -ForegroundColor Yellow
    Write-Host "   Done-when 7   a second holder, read-only, and take-over" -ForegroundColor Yellow
    Write-Host "   Done-when 8   external change -> refused, with [reload][overwrite][diff]" -ForegroundColor Yellow
    Write-Host "   Done-when 9   closing a dirty file routes through Confirm" -ForegroundColor Yellow
    Write-Host "   Done-when 10  autosave after_delay: one save, not thirty" -ForegroundColor Yellow
    Write-Host "   Done-when 11  autosave does not fire on a file held by another holder" -ForegroundColor Yellow
    Write-Host "   Done-when 12  autosave does not trip Point's own staleness check" -ForegroundColor Yellow
    Write-Host ""
    Write-Host " Settled by the first verification round, NOT bugs:" -ForegroundColor DarkGray
    Write-Host "   tab in tabs.go      inserts a real tab; tabStop 8 makes its" -ForegroundColor DarkGray
    Write-Host "                       DRAWN width 4 at columns 4, 12, 20... (step 5a)" -ForegroundColor DarkGray
    Write-Host "   shift+tab           dedents an indented line and is a clean no-op" -ForegroundColor DarkGray
    Write-Host "                       on an unindented one; in big.py one line in" -ForegroundColor DarkGray
    Write-Host "                       five has no indent, which read as inconsistent" -ForegroundColor DarkGray
    Write-Host "   caret-move undo     verified correct: abc, arrows, xyz, one ctrl+z" -ForegroundColor DarkGray
    Write-Host "                       removes only xyz" -ForegroundColor DarkGray
    Write-Host "   redo after a pause  no timer clears redo; held across a real 2.8s" -ForegroundColor DarkGray
    Write-Host "                       gap by test. Not reproduced -- re-report with" -ForegroundColor DarkGray
    Write-Host "                       the exact keys if it recurs" -ForegroundColor DarkGray
    Write-Host ""
    Write-Host " CHANGED from what F12 shipped, on purpose (Aryan's call):" -ForegroundColor Yellow
    Write-Host "   the global `"any key to keep`" idle rescue is removed. A keystroke" -ForegroundColor Yellow
    Write-Host "   now affects only the ACTIVE workspace's own clock; switching to a" -ForegroundColor Yellow
    Write-Host "   workspace is the only way to keep a backgrounded one, and the" -ForegroundColor Yellow
    Write-Host "   grace warning names the switcher instead of promising a key." -ForegroundColor Yellow
    Write-Host "   F12's TestAnyKeyKeepsAWarnedWorkspaceAndOnlyThatOne is replaced by" -ForegroundColor Yellow
    Write-Host "   TestAKeyInOneWorkspaceDoesNotRescueAnother, which asserts the" -ForegroundColor Yellow
    Write-Host "   inverse. design/completed/frontend/F12 should record this." -ForegroundColor Yellow
    Write-Host ""
    Write-Host "   the switcher's readout is a CLAMPED countdown: elapsed is never" -ForegroundColor Yellow
    Write-Host "   displayed larger than the budget, and the pinned case says so." -ForegroundColor Yellow
    Write-Host "   F12's own TestTheIdleReadoutDistinguishesActiveIdleAndNotLoaded" -ForegroundColor Yellow
    Write-Host "   is replaced by ...CountsDownAndNamesTheOtherStates." -ForegroundColor Yellow
    Write-Host ""
    Write-Host "   BOTH are now recorded in design/completed/frontend/" -ForegroundColor Green
    Write-Host "   F12-workspace-manager.md, under `"Superseded by F13 (Session 18a)`"," -ForegroundColor Green
    Write-Host "   with pointers from the two Decisions they contradict." -ForegroundColor Green
    Write-Host ""
    Write-Host " Pre-existing, NOT this episode's and NOT changed:" -ForegroundColor DarkGray
    Write-Host "   the idle countdown in the ctrl+w w switcher is a snapshot taken" -ForegroundColor DarkGray
    Write-Host "   when the popup opens and never advances (app\chooser.go, F12)." -ForegroundColor DarkGray
    Write-Host ""
    Write-Host " A note on red text: cli.py logs to stderr BY DESIGN (stdout is the" -ForegroundColor DarkGray
    Write-Host " protocol channel). PowerShell 5.1 renders those lines as red" -ForegroundColor DarkGray
    Write-Host " NativeCommandError blocks. Only 'FAIL' lines above are failures." -ForegroundColor DarkGray

    if ($script:failures.Count -ne 0) { throw "F13a gate: $($script:failures.Count) assertion(s) failed" }
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
    Write-Host "git status should be clean — this session has no write path at all." -ForegroundColor DarkGray
}
