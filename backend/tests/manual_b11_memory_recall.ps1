# Decisive memory-first recall test for B11's Research agent.
#
# Unlike the earlier "Bloom filter" attempt, this asks about something
# ONLY findable by reading this project's own source (never in any
# model's pretraining, and no network/web_search flakiness to worry
# about) -- so a real answer on the first ask REQUIRES a tool call, and
# whether the second ask (after the first has scrolled out of
# stm_window, reworded so it can't just pattern-match raw history)
# reaches for load_task_result / re-reads the file / or answers directly
# from the reference index's own preview is a real, decisive signal.
#
# Runs entirely against a redirected scratch profile -- never the live
# one. `project init` against the real repo is expected and harmless
# (same side effect B10's own verification already documented: it adds
# `.point\` + a .gitignore line to D:\Projects\Point).

$ErrorActionPreference = "Continue"
[Console]::OutputEncoding = [Text.Encoding]::UTF8

$scratchRoot = Join-Path $env:TEMP ("point-b11-recall-" + (Get-Date -Format "yyyyMMdd-HHmmss"))
New-Item -ItemType Directory -Force -Path $scratchRoot | Out-Null
$env:APPDATA = Join-Path $scratchRoot "Roaming"
$env:LOCALAPPDATA = Join-Path $scratchRoot "Local"
Remove-Item Env:\POINT_MOCK -ErrorAction SilentlyContinue
Remove-Item Env:\POINT_DB -ErrorAction SilentlyContinue
Write-Host "scratch profile: $scratchRoot`n"

Set-Location "D:\Projects\Point\backend"
uv run python cli.py migrate

$projOut = uv run python cli.py project init --path "D:\Projects\Point"
Write-Host $projOut
$projectId = ($projOut -split '\s+')[0]

$sessOut = uv run python cli.py session new "clean recall test" --project $projectId
Write-Host $sessOut
$sessionId = ($sessOut -split '\s+')[0]

Write-Host "`n=== 1st ask -- must read the file to answer this correctly (--verbose shows live tool calls) ==="
# B11 fix: the model previously invented a leading '/' on the path from
# nothing (my prompt never had one), which the path guard correctly
# refused as outside the project root -- fail-safe, but useless for this
# test. Grounding the path in list_files' own output (copy, not compose)
# is far more reliable for a weak model than trusting it to spell one.
uv run python cli.py send "@research First call list_files with prefix backend/agents to find executor.py, then call read_file with that EXACT path list_files returned (do not modify it or add a leading slash). Then tell me: what regular expression pattern does _FILE_TOKEN_RE use in that file, and what is the minimum number of characters required before the dot for a token to count as a filename?" --session $sessionId --wait --verbose --max-seconds 60 --timeout 90

# --- push the first exchange out of stm_window (20) with 12 filler pairs,
# seeded directly (no LLM calls -- pure test scaffolding, not product code) ---
Write-Host "`n=== seeding 12 filler exchanges directly so the first answer scrolls out of history ==="
@'
import asyncio, os, sys
sys.path.insert(0, r"D:\Projects\Point\backend")
from database.connection import open_database
from managers.session_manager import SessionManager
from managers.message import UserMessage, AssistantMessage

async def main():
    conn = await open_database(os.environ["LOCALAPPDATA"] + r"\Point\runtime.db")
    sessions = SessionManager(conn)
    for i in range(12):
        await sessions.append_message(UserMessage(sys.argv[1], f"filler turn {i}: unrelated small talk."))
        await sessions.append_message(AssistantMessage(sys.argv[1], f"filler reply {i}: acknowledged."))
    await conn.close()
    print("seeded 12 filler exchange(s) (24 messages)")

asyncio.run(main())
'@ | Set-Content -Path (Join-Path $scratchRoot "seed_filler.py") -Encoding utf8
uv run python (Join-Path $scratchRoot "seed_filler.py") $sessionId

Write-Host "`n=== 2nd ask -- reworded, first exchange now out of history: watch for load_task_result vs. re-reading the file ==="
uv run python cli.py send "@research In this project's backend code, what's the minimum character count required before the dot for the Filler's one-file guard to treat something as a filename?" --session $sessionId --wait --verbose --max-seconds 60 --timeout 90

Write-Host "`n=== deterministic proof: the tool_calls row for each task, straight from the DB ==="
$db = Join-Path $env:LOCALAPPDATA "Point\runtime.db"
sqlite3 $db @"
.headers on
.mode column
select t.rowid, substr(t.id,1,8) as task, t.tool, t.ok, t.args_json
from tool_calls t
join tasks k on k.id = t.task_id
where k.session_id = '$sessionId'
order by t.rowid;
"@

Write-Host "`nEXPECT on the 1st task: a read_file call (real answer, grounded in the actual file)."
Write-Host "Then watch the 2nd task's row(s), if any:"
Write-Host "  - load_task_result only / no tool calls at all -> it used the reference index."
Write-Host "  - read_file again -> it re-did the work instead of using the index (a real prompt gap)."

Write-Host "`n=== the two final answers, for a human read ==="
sqlite3 $db @"
.headers on
.mode column
select substr(k.id,1,8) as task, m.content
from tasks k
join messages m on m.task_id = k.id and m.role = 'assistant'
where k.session_id = '$sessionId'
order by k.created_at;
"@

Write-Host "`n=== the reference-index log line (proves it was OFFERED, independent of whether it was used) ==="
Get-Content (Join-Path $env:LOCALAPPDATA "Point\logs\point.log") | Select-String "offered.*past result"

Write-Host "`n=== done. scratch profile: $scratchRoot ==="
