# Manual verification for B11 -- The Three Agents.
# Run from anywhere; it cd's itself. Runs entirely against a redirected
# scratch profile (APPDATA/LOCALAPPDATA), never the live one -- per
# CLAUDE.md's hard rule, both env vars are set in this one script, not
# per-call. Needs Ollama reachable locally (the same dependency every
# prior episode's real-model checks have had).
#
# Corrected from the original task doc's literal commands where the
# mechanism does not match (documented inline at each divergence, same
# as B8/B10's own corrections):
#   - explainer/filler/refusal sends have no --provider/--model flags on
#     `send` (only `session new` has those) -- default_provider() picks
#     ollama automatically.
#   - `context report` needs the SAME --file/--select back: EditorContext
#     is per-turn, never remembered on the session (see cmd_context_report
#     in cli.py's own docstring addition).
#   - the "repeat" check for Research needs the SAME session id both
#     times (@research alone always starts a fresh session) and both
#     sends use --wait, so the first one is genuinely `completed` (not
#     merely started) before the second one asks again -- otherwise
#     there is no completed prior task to reference yet.

$ErrorActionPreference = "Continue"
[Console]::OutputEncoding = [Text.Encoding]::UTF8

$scratchRoot = Join-Path $env:TEMP ("point-b11-verify-" + (Get-Date -Format "yyyyMMdd-HHmmss"))
New-Item -ItemType Directory -Force -Path $scratchRoot | Out-Null
$env:APPDATA = Join-Path $scratchRoot "Roaming"
$env:LOCALAPPDATA = Join-Path $scratchRoot "Local"
Remove-Item Env:\POINT_MOCK -ErrorAction SilentlyContinue
Remove-Item Env:\POINT_DB -ErrorAction SilentlyContinue
Write-Host "scratch profile: $scratchRoot`n"

Set-Location "D:\Projects\Point\backend"
uv run python cli.py migrate

# --- fixture ------------------------------------------------------------
$projDir = Join-Path $scratchRoot "proj"
New-Item -ItemType Directory -Force -Path $projDir | Out-Null
$mainPy = Join-Path $projDir "main.py"
# -Encoding utf8 on Windows PowerShell 5.1 writes a BOM -- deliberately
# left in, because the real editor case does too, and the backend now
# strips it (see B11's Handoff for why that fix exists).
Set-Content -Path $mainPy -Encoding utf8 -Value "def parse_config(path):`n    with open(path) as f:`n        return json.load(f)`n"

# --- 2. Explainer sends the enclosing function, not one line -------------
Write-Host "`n=== 2. explainer scope ladder ==="
uv run python cli.py send "@filler-explainer explain" --file $mainPy --select 2 --timeout 120
Write-Host "`n--- context report (same --file/--select: per-turn, not remembered) ---"
uv run python cli.py context report --last --file $mainPy --select 2 --json
Write-Host "`nEXPECT: task_state.detail above reads 'lines 1-3' (the whole function), not just line 2."

# --- 3. Filler writes NOTHING ---------------------------------------------
Write-Host "`n=== 3. filler writes nothing ==="
$before = (Get-Item $mainPy).LastWriteTime
uv run python cli.py send "@filler-explainer add error handling for a missing file" --file $mainPy --cursor 1 --timeout 120
$after = (Get-Item $mainPy).LastWriteTime
Write-Host "`nEXPECT a JSON region above, and UNCHANGED = True below."
Write-Host "UNCHANGED: $($before -eq $after)"

# --- 4. two-file refusal ----------------------------------------------------
Write-Host "`n=== 4. two-file refusal ==="
uv run python cli.py send "@filler-explainer refactor this across main.py and utils.py" --file $mainPy --cursor 1 --timeout 60
Write-Host "`nEXPECT a plain-text refusal naming 'utils.py' and 'main.py', TaskDone, no error/code."

# --- 5/6. Research: first ask, then a genuine repeat in the SAME session ---
Write-Host "`n=== 5/6. research ==="
$sessOut = uv run python cli.py session new "research verify"
Write-Host $sessOut
$sessionId = ($sessOut -split '\s+')[0]

uv run python cli.py send "@research what is a rope data structure" --session $sessionId --wait --timeout 240
Write-Host "`n--- pending proposals (<= 3) ---"
uv run python cli.py memory list --status pending
Write-Host "`n--- tasks ---"
uv run python cli.py tasks list --limit 3

Write-Host "`n--- repeat, same session, --wait ---"
uv run python cli.py send "@research what is a rope data structure" --session $sessionId --wait --timeout 240
Write-Host "`n--- pending proposals again (still <= 3 total for the session) ---"
uv run python cli.py memory list --status pending

Write-Host "`n--- deterministic memory-first evidence (code-driven, not model-dependent) ---"
Get-Content (Join-Path $env:LOCALAPPDATA "Point\logs\point.log") | Select-String "offered.*past result"
Write-Host "EXPECT one line above: '...agent research offered 1 past result(s) before this turn'."

# --- bonus: background early-return (--wait is optional; the CLI process
# still exits shortly after -- see B11's Handoff on why this cancels the
# task rather than truly keeping it running past this command) ---------
Write-Host "`n=== bonus: background early-return, --wait omitted ==="
$sessOut2 = uv run python cli.py session new "research background demo"
$sessionId2 = ($sessOut2 -split '\s+')[0]
$elapsed = Measure-Command {
    uv run python cli.py send "@research what is a skip list" --session $sessionId2 --timeout 240
}
Write-Host "wall time for the CLI call: $($elapsed.TotalSeconds)s -- EXPECT well under a full generation's worth"

# --- 7. title ---------------------------------------------------------------
Write-Host "`n=== 7. session titles ==="
uv run python cli.py session list
Write-Host "`nEXPECT every session above to have a short generated title, not its literal first message or 'New Session'."

Write-Host "`n=== done. scratch profile left at: $scratchRoot (safe to delete) ==="
