# F15 (Session 19) -- Filler-Explainer Integration: manual verification
#
#   powershell -NoProfile -ExecutionPolicy Bypass -File D:\Projects\Point\backend\tests\gate_f15.ps1
#
# Run it from anywhere. It creates ONE scratch profile, points both APPDATA
# and LOCALAPPDATA at it for the whole script, copies the shipped sample
# project into it to work on, and removes the lot at the end. Nothing under
# the real %APPDATA%\Point or %LOCALAPPDATA%\Point is ever named by this
# script, destructively or otherwise -- CLAUDE.md's env-var-override
# pattern. The retired rename-aside pattern is NOT used.
#
# It stops at each step. Read the EXPECT lines before pressing Enter.
#
# ---------------------------------------------------------------------------
# Why this gate runs in DEMO MODE, against a COPY of the sample project
# ---------------------------------------------------------------------------
#
# Two separate reasons, both measured rather than assumed.
#
# 1. The real local model does not produce a valid proposal. B24 measured
#    qwen2.5:3b at 0/5 across two runs with one consistent root cause (it
#    reports end_line as the length of its own replacement rather than the
#    span of the shown region), and that prompt is deliberately untouched
#    since. So against ollama there is no diff card at all, and Done-when
#    3, 4, 7 and 7b cannot be exercised.
#
#    Demo mode's filler_fill.json (B28) is the one reliable producer. Its
#    JSON is hardcoded to file "app.py", start_line 4, end_line 8 -- and
#    B24 REJECTS a proposal whose range falls outside the region the model
#    was shown. The shipped sample project's app.py has greet() at exactly
#    lines 4-8, which is why the caret goes on line 6 and nowhere else.
#    Confirmed end to end while building this episode: one real send
#    through the demo provider returned that proposal with mtime
#    1791061494.9591148 and size 374 attached.
#
# 2. Done-when 7b modifies the file ON DISK. Doing that to the shipped
#    backend\demo\sample_project\app.py would dirty the repo, so the gate
#    works on a copy inside the scratch directory and registers that copy
#    as its own project in the scratch database.
#
# The Editor resolves its project from the WORKING DIRECTORY (see
# frontend\internal\editor\protocol.go), so Point is launched from that
# copy.
#
# POINT_BACKEND_DIR is therefore SET EXPLICITLY in Step 1, and the first
# version of this gate was wrong not to. It claimed "FindBackendDir still
# walks up from the cwd and finds the repo's own backend\main.py --
# verified, not assumed", which was verified on the machine that wrote it
# and is NOT drive-independent: the walk stops at the volume root
# (searchUpward in frontend\internal\protocol\spawn.go), and this gate's
# working directory lives under $env:TEMP. On a machine where TEMP is on
# C: and the repo is on D: -- the real case this failed on -- no number of
# upward steps from the scratch copy can reach D:\Projects\Pointackend,
# so the backend never started and the app sat on "backend starting"
# forever. The error it logged literally suggested setting this variable.
#
# Nothing else in the chain rescues it: the exe is built INTO the scratch
# directory, so the walk from the executable's own directory (which is what
# a packaged install relies on) has no backend\ above it either.
#
# ---------------------------------------------------------------------------
# TWO KEYS FOR THE LEADER, AND TWO FOR QUIT
# ---------------------------------------------------------------------------
#
# F2 / ctrl+w   both start a leader chord
# F3 / ctrl+q   both quit
#
# Neither is a fallback and neither is going away. Use whichever you like;
# the steps below name both at every point they ask for one.
#
# Why there are two, since it affects what you will see in the log: on
# Windows, bubbletea's key reader returns a plain RUNE when a CTRL bit is
# set but neither the control character nor the virtual-key code matches
# its tables (v1 key_windows.go's final "return KeyRunes"), and its KeyMsg
# has no Ctrl field to carry the modifier. Ctrl+Shift+W reports the
# character 'W'. So the chord can arrive as an ordinary letter -- and in a
# text editor an ordinary letter is typed INTO THE FILE. Earlier runs of
# this gate logged exactly that: cause="key w" on fast repeats,
# cause="key W" right after a workspace load, cause="key q" at the quit
# press, and a doubled "ww" when the chord was pressed immediately after
# submitting the fill prompt (the mangled first key leaves nothing
# pending, so the second lands as text too).
#
# F2 and F3 cannot do this: a virtual-key code is resolved before the
# ctrl/rune branch and carries no rune to insert. They are the reliable
# habit. ctrl+w and ctrl+q stay fully wired because removing them would
# make every muscle-memory press a guaranteed stray letter instead of an
# occasional one.
#
# If a stray letter does appear, the log names it -- see DirtyCauses below.
#
# ---------------------------------------------------------------------------
# Two things about this file's own bytes
# ---------------------------------------------------------------------------
#
# 1. UTF-8 WITH a BOM and ASCII content, for gate_f13a's reason: PowerShell
#    5.1 decoded a BOM-less UTF-8 gate as cp1252 and produced ten parse
#    errors before a line ran.
#
# 2. $ErrorActionPreference is Continue, not Stop -- B32's finding. A
#    native command writing to stderr raises NativeCommandError, which Stop
#    promotes to terminating, and the backend logs to stderr BY DESIGN.
#    Safety comes from the two env-var overrides, not from this setting.

$ErrorActionPreference = "Continue"

$repo    = "D:\Projects\Point"
$scratch = Join-Path $env:TEMP "point-scratch-f15"
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
function Ask($question) {
    $a = Read-Host "  $question [y/n]"
    return ($a -match '^(y|Y)')
}
# Launch blocks until Point exits (ctrl+q). The built EXE rather than
# "go run", and from the WORK directory -- that cwd is what the Editor
# resolves its project from.
function Launch() {
    Write-Host ""
    Write-Host "  launching Point from $work (quit with ctrl+q when the step is done)" -ForegroundColor DarkGray
    Push-Location $work
    & $exe
    Pop-Location
}
# LaunchBg starts Point in its own window and returns immediately, so this
# script can modify app.py on disk WHILE a diff card is open. Step 6 is the
# only step that needs it and it cannot be any other shape: the whole
# question is what an apply does when the file moved under a card that is
# already on screen. Splitting it into two launches would relocate the
# question instead of answering it, which is the trap gate_f13b's own
# step 4 records.
#
# The child inherits this process's environment, so APPDATA and
# LOCALAPPDATA still point at the scratch profile.
function LaunchBg() {
    Write-Host ""
    Write-Host "  launching Point IN A SEPARATE WINDOW -- this one keeps going" -ForegroundColor DarkGray
    return Start-Process -FilePath $exe -WorkingDirectory $work -PassThru
}
function FrontendLog() {
    return (Join-Path $scratch "Point\logs\frontend.log")
}
# WroteAnything is the write detector, and it greps for the string the
# write path ACTUALLY logs.
#
# It used to be LogHas "WriteProjectFile", which could never match: the
# editor logged nothing at all on a successful write (doSend logs only on a
# marshal or send failure), so the assertion passed in every run -- including
# the runs where the mtime check in the same step FAILED, which is a flat
# contradiction that went unread for three rounds. save.go now logs
# "editor: WriteProjectFile sent" with the reason and the byte count, and
# this is the pattern that matches it.
#
# Proven to catch a real write rather than assumed: see step 1b.
function WroteAnything() {
    return (LogHas "editor: WriteProjectFile sent")
}

# DirtyCauses lists every "the buffer became dirty" line, which names the
# input that did it -- a key, a paste, the mouse. That is the line that
# answers "what wrote app.py", because in a TEXT EDITOR any key no surface
# claimed is a character typed into the file, and autosave then persists it
# entirely correctly. A write with no visible cause reads as the agent path
# misbehaving when it is usually a stray keystroke.
function DirtyCauses() {
    return (LogLines "editor: buffer became dirty")
}

function LogHas($pattern) {
    $p = FrontendLog
    if (-not (Test-Path $p)) { return $false }
    return ((Select-String -LiteralPath $p -Pattern $pattern -SimpleMatch -Quiet) -eq $true)
}
function LogLines($pattern) {
    $p = FrontendLog
    if (-not (Test-Path $p)) { return @() }
    return (Select-String -LiteralPath $p -Pattern $pattern -SimpleMatch | ForEach-Object { $_.Line })
}
function MtimeOf($path) {
    return (Get-Item -LiteralPath $path).LastWriteTime.Ticks
}

try {
    # -----------------------------------------------------------------
    Step 0 "the Go gate -- gofmt, vet, the whole frontend suite"
    # -----------------------------------------------------------------
    #
    # MOST OF THIS GATE IS NOW AUTOMATED, and the suite below runs it.
    # frontend\internal\editor\gateauto_test.go drives steps 2, 3, 4, 5,
    # 6, 7 and the automatable half of 8 through the real Root -- the real
    # key events, the real envelope path, the real overlay stack -- and
    # asserts what the Ask prompts below used to ask a human to eyeball.
    #
    # The manual pass is kept for what a machine cannot judge: whether
    # something LOOKS right (alignment, the accent frame, colour), whether
    # scrolling FEELS smooth, whether the demo fixture's prose reads
    # sensibly, and the one instrument the harness does not have -- the
    # real filesystem's own mtime.
    # -----------------------------------------------------------------
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
    Step 1 "the scratch profile, the work copy, and the build"
    # -----------------------------------------------------------------
    New-Item -ItemType Directory -Force $scratch | Out-Null
    New-Item -ItemType Directory -Force $work | Out-Null
    $env:APPDATA      = $scratch
    $env:LOCALAPPDATA = $scratch
    # The backend's own DEBUG lines, so a failure has something to read.
    $env:POINT_BACKEND_VERBOSE = "1"
    # A LONG fill proposal, for the steps that need one.
    #
    # Demo's default fixture proposes five lines, which is the right size
    # for demonstrating the feature and the wrong size for testing the
    # Editor's ghost rendering: scrolling, click-to-line mapping and
    # arrow movement through ghost rows only misbehave visibly once the
    # region is taller than the pane. POINT_DEMO_LONG_FILL=1 swaps in a
    # deterministic 46-line proposal against the same app.py lines 4-8,
    # so B24 validates it identically and nothing else changes.
    #
    # Set for the whole run: every fill step below benefits from the
    # bigger region, and a step that wants the short one can unset it.
    $env:POINT_DEMO_LONG_FILL = "1"

    # Two pacing overrides, off unless you set them. The long fixture
    # still streams in under two seconds, which is faster than a human
    # can watch the ghost scroll or click a line mid-stream. Uncomment
    # either to slow the part you want to look at:
    #
    #   STREAM_DELAY_MS replaces each chunk's own delay (it does NOT add
    #   to it) and is PER CHUNK. The long fixture arrives in 604
    #   token-sized pieces with no baked-in delay, so unset it streams in
    #   about 0.06s; 10 gives a smooth ~9s typewriter over its 46 lines
    #   and 40 gives ~28s. Both measured. Values that look sane for the
    #   SHORT fixtures (which have 2-4 chunks of 500-700ms) are wildly
    #   too slow here -- 1500 would be fifteen minutes.
    #
    #   CARD_DELAY_MS holds the DONE chunk back, which holds back
    #   TaskDone and therefore B24's FillProposal -- so it is the gap
    #   between "the ghost stopped growing" and "the diff card opens".
    #   That is the only window in which the finished ghost can be
    #   inspected with no card over it, which is where Step 6's
    #   arrow-movement and click-to-line checks actually want to be.
    #
    # Both are Demo-mode only (they live in providers\demo\provider.py,
    # which loads for no other provider) and are read once per stream, so
    # they can be changed between launches of the same scratch profile.
    #
    # $env:POINT_DEMO_STREAM_DELAY_MS = "1500"
    # $env:POINT_DEMO_CARD_DELAY_MS   = "4000"

    # Where backend\main.py is, stated rather than discovered. See the
    # header: the upward walk cannot cross a volume root, and this gate's
    # cwd and exe both live under $env:TEMP, which is not necessarily on
    # the same drive as the repo.
    $env:POINT_BACKEND_DIR = (Join-Path $repo "backend")

    # The sample project's FILES, not its .point directory: the project is
    # registered below against the scratch database, and a copied .point
    # would name a project id that database has never heard of.
    Copy-Item (Join-Path $repo "backend\demo\sample_project\*") $work -Recurse -Force
    if (Test-Path (Join-Path $work ".point")) {
        Remove-Item -LiteralPath (Join-Path $work ".point") -Recurse -Force
    }
    Assert-That (Test-Path $appPy) "app.py copied into the work directory"

    # greet() must still be at lines 4-8, or B24 rejects the demo
    # fixture's proposal and there is no card to review.
    $lines = Get-Content -LiteralPath $appPy
    Assert-That ($lines[3] -match '^def greet') "app.py line 4 still opens greet()"
    Assert-That ($lines[7] -match 'Hello') "app.py line 8 is still greet's last line"

    Push-Location (Join-Path $repo "backend")
    uv run python cli.py migrate
    $projLine = (uv run python cli.py project init --path $work)
    Pop-Location
    Write-Host "  project: $projLine" -ForegroundColor DarkGray
    Assert-That (Test-Path (Join-Path $work ".point")) "the work copy is a registered project"

    Push-Location $repo
    go build -o $exe .\frontend\cmd\point
    Pop-Location
    Assert-That (Test-Path $exe) "point.exe built into the scratch directory"

    $mtimeAtStart = MtimeOf $appPy
    Write-Host "  app.py LastWriteTime.Ticks at start: $mtimeAtStart" -ForegroundColor DarkGray

    # -----------------------------------------------------------------
    Step 2 "Done-when 1 and 2 -- explain, in a panel beside the code"
    # -----------------------------------------------------------------
    Do-This @(
        "the first-run chooser opens: pick DEMO as the provider",
        "F2 then e  (or ctrl+w then e)  switches to the Editor",
        "the file tree has the keyboard; open app.py with Enter",
        "ctrl+o focuses the buffer; put the caret on LINE 12 (inside add())",
        "shift+end to select just that one line if you like -- either works",
        "F2 then a  (or ctrl+w then a)  asks for an explanation",
        "read it, then press c to send it to Chat (it switches workspaces)",
        "come back with F2 then e, ask again, and press esc instead",
        "ctrl+q to quit when you are done"
    )
    Write-Host "  BEFORE YOU PRESS A BARE LETTER (c, d, e), CHECK THE SURFACE IS UP." -ForegroundColor Yellow
    Write-Host "  This editor is a text editor: a key no surface claims is a character" -ForegroundColor Yellow
    Write-Host "  typed INTO app.py, and autosave then persists it one second later --" -ForegroundColor Yellow
    Write-Host "  entirely correctly. That is where this gate's disk-write failures came" -ForegroundColor Yellow
    Write-Host "  from, not from the fill path: 'press c to send to chat' with the panel" -ForegroundColor Yellow
    Write-Host "  already dismissed, or 'press d to discard' a card that has not opened" -ForegroundColor Yellow
    Write-Host "  yet, is an edit. The log now names the cause of every such edit" -ForegroundColor Yellow
    Write-Host "  ('editor: buffer became dirty ... cause=\"key c\"'), and this script" -ForegroundColor Yellow
    Write-Host "  prints those lines beside each mtime check." -ForegroundColor Yellow
    Write-Host "  The caret goes in add() for THIS step and greet() for the fill steps," -ForegroundColor DarkGray
    Write-Host "  and the asymmetry is the fixtures', not an inconsistency here:" -ForegroundColor DarkGray
    Write-Host "  filler_explain.json's TEXT describes add(a, b) -- B28's own check #7" -ForegroundColor DarkGray
    Write-Host "  asserts exactly that, paired with lines 11-13 -- while" -ForegroundColor DarkGray
    Write-Host "  filler_fill.json's JSON is hardcoded to lines 4-8, which is greet()." -ForegroundColor DarkGray
    Write-Host "  Asking about greet() here streams a canned answer about a DIFFERENT" -ForegroundColor DarkGray
    Write-Host "  function and reads as broken region detection when the region sent" -ForegroundColor DarkGray
    Write-Host "  was correct -- which is exactly how the first run of this gate was" -ForegroundColor DarkGray
    Write-Host "  misread. Updating the fixture instead would break B28's check." -ForegroundColor DarkGray
    Expect @(
        "a panel opens on the RIGHT of the Code File pane, titled 'Explaining - app.py'",
        "the code is STILL VISIBLE beside it -- the panel does not cover it",
        "the explanation streams in, and is about the add FUNCTION, not one line",
        "the question at the top of the panel WRAPS onto as many rows as it needs",
        "c switches to Chat with the exchange quoted in the INPUT, not sent",
        "Chat's TRANSCRIPT is empty of it -- the Editor's turns live in the",
        "   Editor's own session and must never render as Chat's history",
        "esc dismisses the panel and the keyboard returns to the buffer"
    )
    Fails @(
        "the panel covers the code, or opens centred over the whole pane",
        "the explanation is about one line alone with no function around it",
        "c sends a message instead of filling the input",
        "the exchange appears in Chat's TRANSCRIPT (as 'User: ... / Point: ...')",
        "   rather than only in its input box -- that is the session leak the",
        "   first run of this gate found, and it made a correct prefill look",
        "   like a sent message",
        "the question line at the top of the panel is cut off at the border",
        "esc prints 'Browse is not available right now'"
    )
    Launch
    Assert-That (Ask "did the panel open beside the code, stream, and esc dismiss it?") "Done-when 2 -- the explanation streams into the panel and esc dismisses it"
    Assert-That (Ask "did c switch to Chat with the exchange in the INPUT, unsent?") "Done-when 2 -- send to chat prefills Chat's input"
    Assert-That (Ask "was Chat's TRANSCRIPT free of the exchange (input only)?") "the Editor's session does not leak into Chat's transcript"
    Assert-That (Ask "did the panel's question line wrap rather than being cut off?") "the panel's question wraps inside its own width"

    # The sent region, from the frontend's own log rather than from a
    # claim: the Editor records the shown range at the moment it invokes.
    Write-Host "  log:" -ForegroundColor DarkGray
    foreach ($l in (LogLines "editor: invoked the filler-explainer")) {
        Write-Host "    $l" -ForegroundColor DarkGray
    }
    Assert-That (LogHas 'mode=explain') "an explain invocation reached the wire"
    Assert-That (LogHas 'shown="[11 13]"') "Done-when 1 -- the WHOLE enclosing function (lines 11-13) was sent, not the one line"

    # -----------------------------------------------------------------
    Step 3 "Done-when 3, 3b, 3c -- ghost text, a workspace switch, and esc"
    # -----------------------------------------------------------------
    Do-This @(
        "F2 then e, open app.py, caret on LINE 6 again",
        "F2 then f  (or ctrl+w then f)  opens a one-line prompt AT THAT ROW",
        "type:  add a type check     and press Enter",
        "   (PASTING it is fine now and worth doing once on purpose: a paste",
        "   while this prompt is open used to land in app.py itself, which is",
        "   where this gate's two disk-write failures came from -- the pasted",
        "   text was a real edit and autosave persisted it, correctly.)",
        "watch the region arrive as dim ghost rows under line 6 -- 46 of them,",
        "   so they overflow the pane and the view follows the stream down",
        "IMMEDIATELY switch away: F2 then w opens the workspace switcher,",
        "   pick Chat and press Enter. (F2 then c does NOT go to Chat: c",
        "   is the right-pane toggle and Chat has no leader key of its own;",
        "   main.go says so in words. The first version of this gate told you to",
        "   press it, which toggled a pane instead of switching workspace.)",
        "then F2 then e to come back",
        "a DIFF CARD opens once the stream finishes -- press d to DISCARD it.",
        "   This step is about the ghost; applying is step 4's job",
        "",
        "NOW THE ONE THAT WAS BROKEN TWICE -- do it deliberately:",
        "ask again (F2 then f, Enter), switch away IMMEDIATELY as above, and",
        "   WAIT a few seconds in Chat for the task to finish there",
        "then come back with F2 then e",
        "",
        "finally: ask once more and press esc WHILE the ghost is still growing",
        "F3 (or ctrl+q) to quit"
    )
    Expect @(
        "the prompt reads 'fill - after line 6 - python' and nothing is sent until Enter",
        "ghost rows appear UNDER line 6, dim, marked with a plus, growing as tokens arrive",
        "coming back from Chat shows everything that arrived while away",
        "a fill that FINISHED while you were in Chat opens its diff card the",
        "   moment you come back -- not over Chat, and not never. The ghost is",
        "   gone, replaced by the card",
        "esc mid-stream removes the ghost entirely and says the buffer was not touched",
        "after esc the status bar shows NO modified marker"
    )
    Fails @(
        "the prompt sends on the first keystroke",
        "the ghost is frozen at whatever it held when you left for Chat",
        "a fill that completed while you were away leaves a frozen ghost and NO",
        "   card, with ctrl+z doing nothing (nothing was ever spliced) -- that is",
        "   the exact pair of faults this round fixed, so it is the one to watch",
        "the diff card pops up over CHAT rather than waiting for the Editor",
        "esc leaves the ghost on screen, or leaves the buffer dirty",
        "the ghost text lands in the real buffer at any point"
    )
    Launch
    Assert-That (Ask "did the ghost stream in under line 6, dim and marked?") "Done-when 3 -- the region streams as ghost text at the caret"
    Assert-That (Ask "did the view FOLLOW the stream down as it overflowed the pane?") "a growing region pulls the viewport along, as Chat's transcript does"
    Assert-That (Ask "scrolling up then back down: was every ghost row reachable, and did the follow resume at the bottom?") "the whole of a long region is reachable, and the live edge re-pins"
    Assert-That (Ask "clicking a REAL line below the ghost: did the caret land on the line you clicked?") "the click mapping accounts for the ghost rows above it"
    Assert-That (Ask "arrow up/down through the ghost: did the caret step one real line at a time?") "arrow movement steps lines, not ghost rows"
    Assert-That (Ask "after Chat and back, did the ghost show everything that arrived?") "Done-when 3b -- the ghost rebuilds from the accumulated events, not a snapshot"
    Assert-That (Ask "did a fill that FINISHED while you were in Chat open its card on return?") "a backgrounded fill's proposal is delivered and its card opens on return"
    Assert-That (Ask "did that card appear only AFTER you came back, never over Chat?") "a card is not opened over the workspace it has nothing to do with"
    Assert-That (Ask "did esc mid-stream clear the ghost AND leave the buffer clean?") "Done-when 3c -- cancelling discards the ghost and touches nothing"

    Assert-That ((MtimeOf $appPy) -eq $mtimeAtStart) "nothing in the ghost path wrote app.py (LastWriteTime unchanged)"
    Assert-That (-not (WroteAnything)) "no write was even attempted during the ghost steps"
    foreach ($c in (DirtyCauses)) { Write-Host "    dirtied: $c" -ForegroundColor Yellow }

    # -----------------------------------------------------------------
    Step 4 "Done-when 4 and 5 -- edit the proposal, apply it, undo it once"
    # -----------------------------------------------------------------
    Write-Host "  This is WORKFLOW 18d's 'You do:' step 1, owed by this session." -ForegroundColor DarkGray
    Do-This @(
        "F2 then e, open app.py, caret on LINE 6",
        "F2 then f, type:  add a type check     Enter",
        "let it finish -- a DIFF CARD opens over the pane",
        "press e, change one word of the proposed result, press esc to review",
        "press Enter to apply",
        "then press ctrl+z ONCE",
        "WAIT three seconds before quitting -- the autosave debounce is one",
        "   second, so quitting immediately would skip the window this check",
        "   is about",
        "F3 (or ctrl+q) to quit -- do NOT press ctrl+s at any point"
    )
    Expect @(
        "the card is titled 'Proposed change - app.py', with line numbers 4-8 and +/- counts",
        "e opens the proposed RESULT (not a patch), and the diff below re-renders as you type",
        "Enter applies YOUR EDITED text and the card CLOSES ITSELF, leaving the",
        "   result in the buffer and a status line naming both ctrl+z and ctrl+s.",
        "   (It used to stay open reading 'Applied - app.py'. An open overlay",
        "   takes every key, and review mode binds no ctrl+z -- so the undo the",
        "   card promised could not reach the buffer until you closed it first.)",
        "the status bar shows the modified marker -- the buffer is dirty, the file is not written",
        "ONE ctrl+z removes the whole insertion, with no need to close anything",
        "and app.py on disk is untouched a few seconds AFTER the undo as well --",
        "   the undo arms the autosave timer (it is a splice like any other), and",
        "   what stops it writing is that the buffer then matches the file again"
    )
    Fails @(
        "the original proposal lands instead of your edit",
        "undo takes several presses, or does nothing until you dismiss the card",
        "the file is written at any point without ctrl+s",
        "   -- including one second AFTER the ctrl+z, which is what the first",
        "   fix for this missed: the apply was stopped from arming autosave and",
        "   the UNDO armed it instead"
    )
    Launch
    Assert-That (Ask "did Enter apply your EDITED text, and one ctrl+z undo all of it?") "Done-when 4 -- the edited proposal applies as one undo step (WORKFLOW 18d step 1)"

    $mtimeAfterApply = MtimeOf $appPy
    Write-Host "  app.py LastWriteTime.Ticks now: $mtimeAfterApply" -ForegroundColor DarkGray
    Assert-That ($mtimeAfterApply -eq $mtimeAtStart) "Done-when 5 -- app.py is UNCHANGED on disk across apply-then-undo"
    Assert-That (-not (WroteAnything)) "Done-when 5 -- no write was even attempted"
    foreach ($c in (DirtyCauses)) { Write-Host "    dirtied: $c" -ForegroundColor Yellow }

    # -----------------------------------------------------------------
    Step 5 "WORKFLOW 18d step 3 -- a refused apply shows ON THE CARD"
    # -----------------------------------------------------------------
    Write-Host "  18d's step 3: force an apply to fail and watch the CARD, not the log." -ForegroundColor DarkGray
    Write-Host "  The reachable version is a READ-ONLY target: the card's guard 3." -ForegroundColor DarkGray
    Do-This @(
        "quit Point if it is running; this script makes app.py read-only first",
        "then: F2 then e, open app.py -- it opens read-only and says so",
        "F2 then f is REFUSED up front, naming read-only, with no model call",
        "F3 (or ctrl+q) to quit"
    )
    Expect @(
        "opening app.py says it is read-only and why",
        "F2 then f refuses immediately -- a proposal that could never be applied",
        "   is not worth a model call",
        "no diff card, no ghost, nothing written"
    )
    Set-ItemProperty -LiteralPath $appPy -Name IsReadOnly -Value $true
    Launch
    Set-ItemProperty -LiteralPath $appPy -Name IsReadOnly -Value $false
    Assert-That (Ask "was fill refused up front on a read-only file, in the status line?") "the apply guards refuse on the surface the user is looking at"

    Write-Host ""
    Write-Host "  NOTE on Done-when 7 (the BUFFER moved since generation):" -ForegroundColor Yellow
    Write-Host "  it is NOT reachable through the UI in this build, and that is a" -ForegroundColor Yellow
    Write-Host "  fact about the card rather than a gap in the guard. The card is an" -ForegroundColor Yellow
    Write-Host "  overlay and Root gives an open overlay every key, so while it is on" -ForegroundColor Yellow
    Write-Host "  screen there is no way to type into the buffer behind it -- and" -ForegroundColor Yellow
    Write-Host "  dismissing it to type discards the card. The guard itself is" -ForegroundColor Yellow
    Write-Host "  F9b's and is covered by TestApplyIsRefusedWhenTheBufferMovedSince" -ForegroundColor Yellow
    Write-Host "  TheProposal, which drives exactly that state. Recorded in F15's" -ForegroundColor Yellow
    Write-Host "  completed doc rather than claimed as verified by hand." -ForegroundColor Yellow

    # -----------------------------------------------------------------
    Step 6 "Done-when 7b -- the FILE changed ON DISK (F9's own #7, finally)"
    # -----------------------------------------------------------------
    Write-Host "  The one F9b deferred as its Hazard 2 and B36 unblocked." -ForegroundColor DarkGray
    Write-Host "  THIS SCRIPT modifies app.py from outside Point while your card is open." -ForegroundColor DarkGray
    Do-This @(
        "a SEPARATE Point window is about to open",
        "in it: F2 then e, open app.py, caret on LINE 6",
        "F2 then f, type:  add a type check     Enter",
        "LEAVE THE CARD OPEN, come back to THIS window and press Enter here",
        "after this script appends a line to app.py, go back and press Enter ON THE CARD"
    )
    $p = LaunchBg
    Pause "get the card open in the other window, then come back"
    Add-Content -LiteralPath $appPy -Value "# external"
    Write-Host "  appended an external line to app.py from outside Point" -ForegroundColor Yellow
    Expect @(
        "pressing Enter on the card REFUSES, with the reason ON THE CARD",
        "the reason names app.py changing ON DISK -- not the buffer",
        "the card still reads 'Proposed change - app.py', never 'Applied'",
        "the footer offers re-generating"
    )
    Fails @(
        "it applies",
        "the refusal appears only in the log",
        "the card flips to Applied",
        "the refusal talks about the BUFFER changing -- that is Done-when 7, and a disk",
        "   check that never ran would look exactly like this"
    )
    Pause "press Enter on the card in the other window, then come back here"
    Assert-That (Ask "was the apply REFUSED, with a reason ON THE CARD naming the file on disk?") "Done-when 7b -- a file changed on disk since generation refuses the apply"
    Assert-That (Ask "did the card stay 'Proposed change' and never read 'Applied'?") "Done-when 7b -- the card does not claim an apply that did not happen"
    Write-Host "  quit the other Point window (ctrl+q) before continuing" -ForegroundColor DarkGray
    Pause "quit the other window"
    if ($p -and -not $p.HasExited) {
        Write-Host "  it is still running -- quit it" -ForegroundColor Yellow
        Pause "quit it"
    }

    foreach ($l in (LogLines "apply refused, file changed on disk")) {
        Write-Host "    $l" -ForegroundColor DarkGray
    }
    Assert-That ((LogLines "apply refused, file changed on disk").Count -ge 1) "the refusal is recorded with the pair it compared"
    Assert-That (LogHas "recorded_mtime=") "the generation-time mtime reached the guard"

    # -----------------------------------------------------------------
    Step 7 "Done-when 6 -- a non-proposal reply is a plain response"
    # -----------------------------------------------------------------
    Do-This @(
        "F2 then e, open app.py, caret inside a function",
        "F2 then f, type:  refactor this across app.py and utils.py     Enter",
        "F3 (or ctrl+q) to quit"
    )
    Expect @(
        "whatever comes back that is NOT a proposal is readable text, shown as a response",
        "no diff card, no error styling, no failed task",
        "the status line says no change was proposed"
    )
    Fails @(
        "it is rendered in the error role, or as a failed task",
        "nothing appears at all"
    )
    Write-Host "  NOTE: Demo mode replays a fixture and will propose a change whatever" -ForegroundColor Yellow
    Write-Host "  you type, so this step usually produces a CARD rather than a refusal." -ForegroundColor Yellow
    Write-Host "  The agent's one-file rule is B11's prompt, not this UI. The refusal" -ForegroundColor Yellow
    Write-Host "  PRESENTATION is covered by TestARefusalSettlesIntoThePanelNotAnError" -ForegroundColor Yellow
    Write-Host "  State and TestANonProposalStreamsAsLiteralText. Answer n and note it" -ForegroundColor Yellow
    Write-Host "  if the fixture proposes anyway -- that is the fixture, not the UI." -ForegroundColor Yellow
    Launch
    Assert-That (Ask "pass-or-note: was a non-proposal reply shown as a normal response?") "Done-when 6 -- a refusal renders as a response, not an error state"

    # -----------------------------------------------------------------
    Step 8 "Done-when 8 -- the same agent from CHAT is NOT an inline diff"
    # -----------------------------------------------------------------
    Write-Host "  Done-when 8's ORIGINAL wording ('produces a result card instead of" -ForegroundColor Yellow
    Write-Host "  an inline diff') asked for something this build deliberately does" -ForegroundColor Yellow
    Write-Host "  not do, and the first run of this gate was answered pass against it" -ForegroundColor Yellow
    Write-Host "  although no tray entry ever appeared. THE TRAY IS BACKGROUND WORK" -ForegroundColor Yellow
    Write-Host "  ONLY: app\tasks.go's builtinAgentBackground, trayRows filters on it," -ForegroundColor Yellow
    Write-Host "  and its own empty state says 'no background tasks ... an inline" -ForegroundColor Yellow
    Write-Host "  turn's reply is in the transcript, not here'. filler-explainer is" -ForegroundColor Yellow
    Write-Host "  max_steps=1, so from Chat it is an inline turn and correctly has no" -ForegroundColor Yellow
    Write-Host "  tray row. The criterion's real content is the half that belongs to" -ForegroundColor Yellow
    Write-Host "  this episode: no inline diff card from a Chat invocation. The task" -ForegroundColor Yellow
    Write-Host "  doc's wording was corrected to match; the tray was not changed." -ForegroundColor Yellow
    Do-This @(
        "get to Chat with F2 then w and the switcher (not F2 then c, which",
        "   is the right-pane toggle)",
        "type @ and pick filler-explainer, then @ again and attach app.py",
        "add 'explain this file', press Enter, let it finish",
        "the right pane's Task card lists EVERY task, inline ones included --",
        "   look at the agent name there",
        "open the tray with f1 for comparison",
        "F3 (or ctrl+q) to quit"
    )
    Expect @(
        "the reply streams into the TRANSCRIPT as an ordinary assistant message",
        "NO inline diff card appears anywhere, in either workspace",
        "the Task card shows the finished task, its agent name either in full",
        "   or cut WITH an ellipsis -- never silently one character short",
        "the f1 tray does NOT list it, and says 'no background tasks'"
    )
    Fails @(
        "a diff card opens over Chat",
        "a diff card appears in the Editor for a task Chat started",
        "the Task card reads 'filler-explaine' with no ellipsis"
    )
    Launch
    Assert-That (Ask "did Chat stream a transcript reply with NO diff card anywhere?") "Done-when 8 -- the invocation site decides the presentation"
    Assert-That (Ask "was the agent name in the Task card either full or ellipsised?") "a truncated agent name says that it was truncated"

    Write-Host ""
    Write-Host "  app.py was modified once, by THIS SCRIPT, in step 6." -ForegroundColor DarkGray
    Write-Host "  start=$mtimeAtStart  final=$(MtimeOf $appPy)" -ForegroundColor DarkGray

    Write-Host ""
    if ($script:failures.Count -eq 0) {
        Write-Host " ALL CHECKS PASSED" -ForegroundColor Green
    } else {
        Write-Host " FAILURES:" -ForegroundColor Red
        foreach ($f in $script:failures) { Write-Host "   - $f" -ForegroundColor Red }
    }
    Write-Host ""
    Write-Host " A note on red text: the backend logs to stderr BY DESIGN (stdout is" -ForegroundColor DarkGray
    Write-Host " the protocol channel). PowerShell 5.1 renders that as red" -ForegroundColor DarkGray
    Write-Host " NativeCommandError blocks. Only FAIL lines are failures." -ForegroundColor DarkGray

    if ($script:failures.Count -ne 0) { throw "F15 gate: $($script:failures.Count) assertion(s) failed" }
}
finally {
    if (Test-Path $scratch) {
        Get-ChildItem $scratch -Recurse -File -ErrorAction SilentlyContinue | ForEach-Object { $_.IsReadOnly = $false }
        Remove-Item -LiteralPath $scratch -Recurse -Force
    }
    Write-Host ""
    Write-Host "cleaned up: $scratch" -ForegroundColor DarkGray
    Write-Host "git status should be clean -- the only app.py this gate touched was its own copy." -ForegroundColor DarkGray
}
