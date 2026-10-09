# F12 — Workspace Manager and Lifecycle: manual verification
#
# Run this from anywhere. It creates ONE scratch profile, points both
# APPDATA and LOCALAPPDATA at it for the whole script, and removes it at the
# end. Nothing under the real %APPDATA%\Point or %LOCALAPPDATA%\Point is
# ever named by this script, destructively or otherwise — CLAUDE.md's
# env-var-override pattern. The rename-aside pattern is NOT used.
#
#   powershell -ExecutionPolicy Bypass -File D:\Projects\Point\backend\tests\manual_f12_lifecycle.ps1
#
# It stops for you at each step. Read the EXPECT lines before pressing Enter.

$ErrorActionPreference = "Stop"

$scratch = Join-Path $env:TEMP "point-scratch-f12"
New-Item -ItemType Directory -Force $scratch | Out-Null

function Step($n, $title) {
    Write-Host ""
    Write-Host ("=" * 72) -ForegroundColor DarkGray
    Write-Host " STEP $n — $title" -ForegroundColor Cyan
    Write-Host ("=" * 72) -ForegroundColor DarkGray
}
function Expect($lines) {
    foreach ($l in $lines) { Write-Host "  EXPECT  $l" -ForegroundColor Green }
}
function Fails($lines) {
    foreach ($l in $lines) { Write-Host "  FAILS IF  $l" -ForegroundColor Yellow }
}
function Pause($msg) {
    Write-Host ""
    Read-Host "  $msg — press Enter when done"
}

try {
    $env:APPDATA      = $scratch   # config.toml, credentials.toml
    $env:LOCALAPPDATA = $scratch   # state.json, logs\, runtime.db

    Write-Host "scratch profile: $scratch" -ForegroundColor DarkGray

    # A fresh database. B29 removed the silent copy-into-any-fresh-profile
    # mechanism, so this is genuinely empty and nothing is inherited.
    Push-Location D:\Projects\Point\backend
    uv run python cli.py migrate
    Pop-Location

    # -----------------------------------------------------------------
    Step 1 "the chooser, not a workspace"
    # -----------------------------------------------------------------
    Write-Host "  A fresh profile has no provider, so F10's chooser opens FIRST."
    Write-Host "  Pick Demo (it needs no key and ships the sample project), then"
    Write-Host "  close Settings with esc."
    Expect @(
        "once Settings closes: a box titled 'Point' asking 'Resume previous session?'",
        "one button, [ Chat ] — Editor is F13's and is deliberately NOT registered",
        "'nothing opened here before' on a first run -- NOT 'last: nothing ...':",
        "  the 'last:' prefix labels a value, and on a first run there is none",
        "pressing Enter loads Chat: the View Port and Query Area appear",
        "the transcript and input are live — a session was created and opened"
    )
    Fails @(
        "a workspace renders before you choose one",
        "Chat loads but stays empty / 'loading' forever (the handshake replay is broken)"
    )
    Write-Host ""
    Write-Host "  Then press esc to leave the input, ^W then w to open the switcher."
    Expect @(
        "'Workspaces' popup, one row: 'Chat' with the note 'active'"
    )
    Write-Host ""
    Write-Host "  Launching. Quit with ^Q when you are done with this step."
    Push-Location D:\Projects\Point
    go run .\frontend\cmd\point
    Pop-Location

    # -----------------------------------------------------------------
    Step 2 "unload with a task running — THE DEMO BEAT"
    # -----------------------------------------------------------------
    Write-Host "  In the app: choose Chat, then send"
    Write-Host ""
    Write-Host "      @research tokio vs asyncio" -ForegroundColor White
    Write-Host ""
    Write-Host "  Wait until the Task card (right pane) shows it RUNNING, then"
    Write-Host "  press ^W then u to unload the view."
    Expect @(
        "the status line reads exactly:",
        "    unloading view - Research task continues in background",
        "  (with an em dash, not a hyphen)",
        "no confirmation dialog — a running task is NOT a reason to block",
        "the startup chooser comes back (nothing is loaded any more)",
        "F1 opens the task tray and the task is STILL THERE, still Running",
        "when it finishes, pressing o on its tray row still opens its result",
        "the notice disappears on its own after a few seconds"
    )
    Fails @(
        "the task shows Cancelled",
        "a dialog appears asking whether to unload",
        "the tray is empty, or the row is there but its result will not open",
        "the notice stays in the status bar indefinitely"
    )
    Write-Host ""
    Write-Host "  Leave the app running (do NOT quit) and come back here."
    Write-Host "  Launching."
    Push-Location D:\Projects\Point
    go run .\frontend\cmd\point
    Pop-Location

    Write-Host ""
    Write-Host "  Backend's own view of that task:" -ForegroundColor Cyan
    Push-Location D:\Projects\Point\backend
    uv run python cli.py tasks list
    Pop-Location
    Expect @("state RUNNING or COMPLETED — never CANCELLED")

    # -----------------------------------------------------------------
    Step 3 "idle auto-unload at one minute"
    # -----------------------------------------------------------------
    $cfg = Join-Path $scratch "Point\config.toml"
    Add-Content -LiteralPath $cfg -Encoding utf8 -Value @"

[workspace]
idle_unload_minutes = 1
"@
    Write-Host "  Wrote to the SCRATCH config.toml:"
    Get-Content -LiteralPath $cfg | Select-Object -Last 4 | ForEach-Object { Write-Host "    $_" -ForegroundColor DarkGray }
    Write-Host ""
    Write-Host "  There is only ONE workspace registered, and THE ACTIVE WORKSPACE"
    Write-Host "  NEVER UNLOADS — so idle unload cannot be triggered by hand until"
    Write-Host "  F13's Editor exists. What you CAN check by hand is the half that"
    Write-Host "  does not need a second workspace:"
    Expect @(
        "load Chat, sit in it for three minutes: it does NOT unload",
        "^W then w shows 'Chat - active' the whole time, never a countdown"
    )
    Write-Host ""
    Write-Host "  The rest of #4 and all of #4b (three workspaces, three different"
    Write-Host "  unload moments, per-workspace countdown, 30s grace warning, any-key-"
    Write-Host "  to-keep) are proven by test instead, on a substitutable clock:" -ForegroundColor Yellow
    Write-Host ""
    Write-Host "      go test ./frontend/internal/app/ -run 'Idle|Timers|Grace|AnyKey|Readout|Zero|Walk' -v" -ForegroundColor White
    Write-Host ""
    Write-Host "  Launching. Quit with ^Q when done."
    Push-Location D:\Projects\Point
    go run .\frontend\cmd\point
    Pop-Location

    # -----------------------------------------------------------------
    Step 4 "ctrl+q lists what it will cancel"
    # -----------------------------------------------------------------
    Write-Host "  Send another @research, and while it is RUNNING press ^Q."
    Expect @(
        "a dialog titled 'Quit Point?'",
        "it NAMES the task and its elapsed time, e.g. '* Research  0:12'",
        "it says 'Quitting will cancel them -- unloading a view would not'",
        "Cancel is focused by default, so a reflex Enter does not quit",
        "esc dismisses it and the task keeps running"
    )
    Write-Host ""
    Write-Host "  Then press ^Q again and confirm. The task dies here — that is the"
    Write-Host "  asymmetry with step 2, and it is the point."
    Fails @(
        "the dialog says only a COUNT with no task name (the off-screen snapshot path is broken)",
        "no dialog appears at all",
        "the wording does not distinguish quit from unload"
    )
    Write-Host ""
    Write-Host "  Launching."
    Push-Location D:\Projects\Point
    go run .\frontend\cmd\point
    Pop-Location

    Push-Location D:\Projects\Point\backend
    uv run python cli.py tasks list
    Pop-Location
    Expect @("the task you quit on is CANCELLED — unlike step 2's")

    # -----------------------------------------------------------------
    Step 5 "survive a kill"
    # -----------------------------------------------------------------
    Write-Host "  In the APP'S OWN WINDOW: load Chat, type a draft WITHOUT sending"
    Write-Host "  it, scroll up a few screens in the transcript, then come back to"
    Write-Host "  THIS window and leave the app running. Wait ~3 seconds after your"
    Write-Host "  last keystroke so the debounced save has fired."
    Write-Host ""

    # Built, not `go run`, and in its OWN WINDOW -- both deliberate.
    #
    # This step froze the app completely (unresponsive to every key,
    # including ^Q) on two separate verification runs, at this step and
    # nowhere else. It was launched with `Start-Process -NoNewWindow`,
    # which shares THIS console, and the script then called Read-Host on
    # the same stdin the TUI was reading. Two readers on one Windows
    # console input buffer means each keystroke goes to whichever wins the
    # race, so the app receives almost nothing and looks hung while being
    # perfectly healthy. Every other step here uses a plain foreground
    # `go run` and none of them froze -- which is the whole of the evidence
    # that this was the script and not the app.
    #
    # `go build` rather than `go run` because this step KILLS the process:
    # `go run` spawns the real binary as a child, so `Get-Process point`
    # was killing the child and leaving the parent, and nothing tied the
    # process being killed to the one that had been started. -PassThru
    # gives the exact pid instead of a name lookup that could match any
    # point.exe at all.
    Write-Host "  Building, then launching in a SEPARATE window."
    $exe = Join-Path $scratch "point-f12.exe"
    Push-Location D:\Projects\Point
    go build -o $exe .\frontend\cmd\point
    Pop-Location
    $app = Start-Process -FilePath $exe -WorkingDirectory D:\Projects\Point -PassThru
    Write-Host "  running as pid $($app.Id) -- it has its own window and its own stdin."

    Pause "set up the draft and scroll in THAT window, come back here, then"

    if (-not $app.HasExited) { Stop-Process -Id $app.Id -Force }
    Write-Host "  killed pid $($app.Id)."

    Write-Host ""
    Write-Host "  What landed on disk (SCRATCH state.json):" -ForegroundColor Cyan
    $st = Join-Path $scratch "Point\state.json"
    Get-Content -LiteralPath $st -Raw
    Expect @(
        "a 'workspace_state' object with a 'chat' key",
        "inside it, F18's shape: version 1 and an anchor with anchor_seq / row_offset / pinned_to_bottom",
        "a 'drafts' object with your unsent text — F4's, written by chat",
        "'last_workspace': 'chat'",
        "BOTH halves present at once — that is the two-owners rule holding"
    )
    Fails @(
        "workspace_state is there but drafts is gone, or the other way round",
        "no workspace_state at all (the position never survives a kill)"
    )
    Write-Host ""
    Write-Host "  Now restart and check it came back."
    Push-Location D:\Projects\Point
    go run .\frontend\cmd\point
    Pop-Location
    Expect @(
        "the chooser, NOT a workspace (a stored blob does not mean loaded)",
        "'last: Chat - position restored on open' on its last line",
        "choosing Chat restores the draft AND the scroll position",
        "the transcript came from the database, not from the blob"
    )

    # -----------------------------------------------------------------
    Step 6 "heap over 20 load/unload cycles"
    # -----------------------------------------------------------------
    # 6061, and the literal 127.0.0.1 -- NOT 6060 and not `localhost`.
    #
    # F12's own task doc asks for `localhost:6060` in its Done-when #7
    # command and this script copied it, which cost a verification round:
    # both ports were tried, both refused, and the conclusion was "pprof
    # never listens". It does. It has listened on 127.0.0.1:6061 since F3
    # (design\completed\frontend\F03-chat-viewport.md records that number),
    # 6060 has never been bound by anything here, and `localhost` can
    # resolve to ::1 while the server is bound to the v4 loopback only.
    Write-Host "  POINT_PPROF is already wired (frontend\cmd\point\diagnostics.go)."
    Write-Host "  In the app, do this twenty times: choose Chat from the chooser,"
    Write-Host "  then ^W u to unload it. Then, in ANOTHER PowerShell window:"
    Write-Host ""
    Write-Host "      go tool pprof -top http://127.0.0.1:6061/debug/pprof/heap" -ForegroundColor White
    Write-Host ""
    Write-Host "  127.0.0.1:6061 -- not 6060, and not 'localhost'. If it refuses,"
    Write-Host "  the app says what it bound (or why it could not) in:"
    Write-Host "      $(Join-Path $scratch 'Point\logs\frontend.log')"
    Write-Host "  grep it for 'pprof':" -ForegroundColor DarkGray
    Write-Host "      Select-String -Path '$(Join-Path $scratch 'Point\logs\frontend.log')' -Pattern pprof" -ForegroundColor White
    Write-Host ""
    Write-Host "  There is also a deterministic version of this check that needs no"
    Write-Host "  manual cycling at all, and it is the one that produced F12's"
    Write-Host "  recorded heap numbers:" -ForegroundColor Yellow
    Write-Host ""
    Write-Host "      go test ./frontend/internal/chat/ -run TestTwentyLoadUnloadCycles -v" -ForegroundColor White
    Write-Host ""
    Expect @(
        "inuse_space returns near its pre-load level after the twentieth cycle",
        "no chat.Workspace / historyWindow retained per cycle",
        "Go DOES release here, unlike CPython — so a real reduction is expected"
    )
    Fails @("heap climbs monotonically with the cycle count")
    Write-Host ""
    Write-Host "  Launching with pprof on."
    $env:POINT_PPROF = "1"
    Push-Location D:\Projects\Point
    go run .\frontend\cmd\point
    Pop-Location

    Write-Host ""
    Write-Host "all steps printed." -ForegroundColor Cyan
}
finally {
    # Nothing is killed by NAME here. This script starts exactly one
    # detached process (step 5) and kills that one by its own pid; a
    # name-matched kill could hit an unrelated point.exe the user started
    # themselves, which is the same class of mistake as naming a real
    # profile path in a destructive call.
    Remove-Item -LiteralPath $scratch -Recurse -Force
    Write-Host "scratch profile removed." -ForegroundColor DarkGray
}
