# B12 verification gate -- routing, attachments, concurrency caps.
#
# ONE script, because separate tool calls do not share shell state: the
# scratch-profile redirection below has to hold for every command that
# follows it, and a per-command `$env:` patch would leave later commands
# writing to the LIVE %APPDATA%\Point and %LOCALAPPDATA%\Point.
#
#   Run from anywhere:
#     powershell -File D:\Projects\Point\backend\tests\manual_b12_verify.ps1
#
# Nothing here touches the live profile or the live runtime.db. The
# scratch directory is printed at the end and is yours to delete.
#
# Two checks need a real model: #1 and #2 use Ollama (qwen2.5:1.5b-8k).
# Everything else runs on the mock provider or the echo path, so the gate
# is deterministic apart from those two.
#
# READING THE OUTPUT: refusals are written to stderr, and Windows
# PowerShell wraps a native command's stderr in a red NativeCommandError
# block. That decoration is PowerShell's, not a crash -- the refusal text
# is the first line of each block, and a red block here is usually the
# check PASSING. Read the text, not the colour.

$ErrorActionPreference = "Continue"

# ---------------------------------------------------------------------------
# Scratch profile
# ---------------------------------------------------------------------------
$scratch = Join-Path $env:TEMP ("point-b12-gate-" + [guid]::NewGuid().ToString("N").Substring(0,8))
New-Item -ItemType Directory -Force -Path (Join-Path $scratch "roaming\Point") | Out-Null
New-Item -ItemType Directory -Force -Path (Join-Path $scratch "local")         | Out-Null
$env:APPDATA      = Join-Path $scratch "roaming"
$env:LOCALAPPDATA = Join-Path $scratch "local"

# POINT_DB points at a file that does not exist yet, so `migrate` creates
# it fresh. Without this, resolve_db_path() copies backend\runtime.db in
# (B6's one-time migration), and the gate would be reading rows written
# by earlier episodes.
$env:POINT_DB = Join-Path $scratch "local\runtime.db"
$db = $env:POINT_DB

@"
[limits]
max_concurrent_tasks = 3

[limits.ollama]
max_concurrent_tasks = 1

[chat]
queue_depth = 1
"@ | Set-Content (Join-Path $env:APPDATA "Point\config.toml") -Encoding utf8

Set-Location D:\Projects\Point\backend
Write-Host "scratch profile : $scratch"
Write-Host "database        : $db"
Write-Host ""

# ---------------------------------------------------------------------------
# 0. Migration -- B12 is v5
# ---------------------------------------------------------------------------
Write-Host "=== 0. migrate (expect 'migrated 0 -> 5')" -ForegroundColor Cyan
uv run python cli.py migrate
Write-Host "--- new columns on messages (expect interrupted, outcome, error_code):"
sqlite3 $db "select name from pragma_table_info('messages') where name in ('interrupted','outcome','error_code');"

# ---------------------------------------------------------------------------
# Parser rules (the ambiguity rule, both separators, --advanced)
# ---------------------------------------------------------------------------
Write-Host "`n=== parser rules" -ForegroundColor Cyan
uv run python tests\manual_b12_parser.py
Write-Host "parser exit code: $LASTEXITCODE  (0 = all rules held)"

# ---------------------------------------------------------------------------
# --advanced is parsed and REFUSED
# ---------------------------------------------------------------------------
Write-Host "`n=== --advanced is parsed and refused" -ForegroundColor Cyan
$env:POINT_MOCK = "1"
$sid = (uv run python cli.py session new "b12 gate" --provider mock --model mock-1) -replace '\s.*',''
Write-Host "session = $sid"
uv run python cli.py send "@research dig deeper --advanced" --session $sid
Write-Host "EXPECT: error: [advanced_unavailable] advanced capabilities are not available yet"
Write-Host "FAILS IF: the message is sent anyway, or the flag is silently ignored."
Remove-Item Env:\POINT_MOCK

# ---------------------------------------------------------------------------
# #2 + #3: concurrency cap and turn queue, through ONE backend process
# ---------------------------------------------------------------------------
Write-Host "`n=== #2 + #3. concurrency cap and turn queue (real Ollama)" -ForegroundColor Cyan
Write-Host "NOTE: this runs through ONE backend process, not three Start-Jobs."
Write-Host "      The cap is a per-process semaphore -- correct, because lockfile.py"
Write-Host "      makes the real backend single-instance. Three cli.py processes are"
Write-Host "      three independent runtimes and would truthfully show three RUNNING."
uv run python tests\manual_b12_concurrency.py
Write-Host "concurrency exit code: $LASTEXITCODE  (0 = all checks passed)"
Write-Host "EXPECT: one RUNNING, two QUEUED, and ListTasks saying exactly that."
Write-Host "FAILS IF: three RUNNING -- the UI would then be lying to the user."

# ---------------------------------------------------------------------------
# #1: an agent task and a plain turn are independent
# ---------------------------------------------------------------------------
Write-Host "`n=== #1. @research runs in the background while a plain turn streams inline" -ForegroundColor Cyan
$sid1 = (uv run python cli.py session new "b12 lanes" --provider ollama --model qwen2.5:1.5b-8k) -replace '\s.*',''
uv run python cli.py send "@research what is a monad" --session $sid1
Write-Host "EXPECT: '[task ... started -- background]' and an immediate return."
uv run python cli.py send "say hi in three words" --session $sid1 --timeout 120
Write-Host "EXPECT: the plain turn streams inline and finishes, even though research is still running."
Write-Host "FAILS IF: the plain turn is queued behind the research task -- agent tasks"
Write-Host "          must never enter the conversational turn queue."
uv run python cli.py tasks list --scope all
Write-Host "NOTE:   --scope all, not --scope active. 'active' means live objects in"
Write-Host "        THIS process, and each cli.py invocation is its own process with"
Write-Host "        its own TaskManager -- so from a fresh CLI it is always empty."
Write-Host "        The durable rows are what survive the process; inside the real"
Write-Host "        single backend (where F8 reads this list), 'active' is live and"
Write-Host "        correct. manual_b12_concurrency.py checks 'active' in-process."

# ---------------------------------------------------------------------------
# A project, and a session bound to it.
#
# The attachment guards need this: without a project, EVERY path is
# refused by B7's path guard first, and the size/binary/directory guards
# never get a turn. A refusal for the wrong reason is not the check
# passing.
#
# No hand-threading of ids. `session new --path <dir>` resolves the
# project the same way `project resolve` does and binds the session to
# it, because every attachment is guarded against `session.project_id`
# and a session created inside a project but not bound to it could not
# attach a single file from that project.
# ---------------------------------------------------------------------------
Write-Host "`n=== project setup (the guards need a project to be guarded against)" -ForegroundColor Cyan
$proj = Join-Path $scratch "proj"
New-Item -ItemType Directory -Force -Path $proj | Out-Null
$src = 1..60 | ForEach-Object { "def function_${_}(value):`n    return value * $_" }
$src -join "`n" | Set-Content (Join-Path $proj "main.py") -Encoding utf8
$projId = ((uv run python cli.py project init --path $proj) -split '\s+')[0]

$env:POINT_MOCK = "1"
$sid6 = (uv run python cli.py session new "b12 attachments" --provider mock --model mock-1 --path $proj) -replace '\s.*',''
Remove-Item Env:\POINT_MOCK
Write-Host "project = $projId"
Write-Host "session = $sid6"
Write-Host "bound to: $(sqlite3 $db "select coalesce(project_id,'NULL') from sessions where id='$sid6';")"
Write-Host "EXPECT: the session's project_id matches the project -- bound by --path,"
Write-Host "        with no uuid typed by hand."

# ---------------------------------------------------------------------------
# #4 + #5: attachment guards
# ---------------------------------------------------------------------------
Write-Host "`n=== #4 + #5. attachment guards" -ForegroundColor Cyan
$big = Join-Path $proj "big.log"
$sw = [System.IO.StreamWriter]::new($big)
$line = "x" * 1023
for ($i = 0; $i -lt 41000; $i++) { $sw.WriteLine($line) }
$sw.Close()
Write-Host "--- 40MB text file ($((Get-Item $big).Length) bytes):"
uv run python cli.py attach "big.log" --session $sid6

# A small binary, so the binary guard is what fires rather than the size
# guard. notepad.exe is 200KB and would be refused as too_large first --
# a correct refusal, but not the one this check is for.
$bin = Join-Path $proj "small.bin"
[System.IO.File]::WriteAllBytes($bin, [byte[]](0..255 + 0..255))
Write-Host "--- small binary ($((Get-Item $bin).Length) bytes):"
uv run python cli.py attach "small.bin" --session $sid6

Write-Host "--- a directory:"
uv run python cli.py attach $proj --session $sid6
Write-Host ""
Write-Host "EXPECT: three refusals, each naming its own reason --"
Write-Host "        [attachment_refused] ... exceeds the 100KB attachment limit"
Write-Host "        [attachment_refused] ... looks binary (null bytes in the first 8KB)"
Write-Host "        [attachment_refused] ... is a directory, not a file"
Write-Host "FAILS IF: any of them is a traceback, or the binary is refused as 'too large'"
Write-Host "          (the first-200-lines offer cannot be taken for a file with no lines)."

Write-Host "`n--- the offers are real, not decoration:"
Write-Host "    (a) the directory listing:"
uv run python cli.py attach $proj --session $sid6 --preview
Write-Host "    (b) the first 200 lines of the oversized file:"
uv run python cli.py attach "big.log" --session $sid6 --preview | Select-Object -First 3
Write-Host "EXPECT: a real listing, and three lines of x's -- both offers can be taken."

# ---------------------------------------------------------------------------
# #6: the token estimate matches what the builder spends
# ---------------------------------------------------------------------------
Write-Host "`n=== #6. token estimate vs. what the context builder actually spends" -ForegroundColor Cyan
Remove-Item $big, $bin -Force -ErrorAction SilentlyContinue
$env:POINT_MOCK = "1"
Write-Host "--- attach; note the estimate:"
uv run python cli.py attach "main.py" --session $sid6
Write-Host "--- what the builder will spend on the NEXT turn:"
uv run python cli.py context report --session $sid6
Write-Host ""
Write-Host "EXPECT: the 'attachments' line equals the estimate (within ~20%)."
Write-Host "NOTE:   run 'context report' BEFORE sending. After a send the attachment"
Write-Host "        correctly reports 0 -- 'unchanged since last sent' is B8 refusing"
Write-Host "        to pay for a file already in the history verbatim."

Write-Host "`n--- a path outside the project is refused at ATTACH time:"
$outside = Join-Path $scratch "outside.py"
"print('hello')" | Set-Content $outside -Encoding utf8
uv run python cli.py attach $outside --session $sid6
Write-Host "EXPECT: refused, naming the project root."
Write-Host "WHY:    the guard runs under the SAME project and the same"
Write-Host "        project.allow_outside_project config the builder will use at send"
Write-Host "        time. Anything else lets attach quote a cost the builder refuses"
Write-Host "        to spend -- an estimate for tokens that never enter the request."

# ---------------------------------------------------------------------------
# #7: detach removes the binding and nothing else
# ---------------------------------------------------------------------------
Write-Host "`n=== #7. detach removes the binding; file and task result untouched" -ForegroundColor Cyan
uv run python cli.py send "explain this file" --session $sid6 --script mock_stream --timeout 60 | Out-Null
$aid = (uv run python cli.py attachments list --session $sid6 | Select-String -Pattern '^([0-9a-f-]{36})' | ForEach-Object { $_.Matches[0].Groups[1].Value } | Select-Object -First 1)
Write-Host "before -- attachments: $(sqlite3 $db "select count(*) from context_attachments;")  messages: $(sqlite3 $db "select count(*) from messages;")  tasks: $(sqlite3 $db "select count(*) from tasks;")"
uv run python cli.py detach $aid --session $sid6
Write-Host "after  -- attachments: $(sqlite3 $db "select count(*) from context_attachments;")  messages: $(sqlite3 $db "select count(*) from messages;")  tasks: $(sqlite3 $db "select count(*) from tasks;")"
Write-Host "file still on disk: $(Test-Path (Join-Path $proj 'main.py'))  ($((Get-Item (Join-Path $proj 'main.py')).Length) bytes)"
Write-Host "EXPECT: attachments drops by one; messages and tasks unchanged; file intact."
Remove-Item Env:\POINT_MOCK

# ---------------------------------------------------------------------------
# #8 + #10: terminal outcomes
# ---------------------------------------------------------------------------
Write-Host "`n=== #8 + #10. every non-clean ending is recorded" -ForegroundColor Cyan
$env:POINT_MOCK = "1"

Write-Host "--- bounds_exceeded: a tool loop stopped at max_steps"
$sidB = (uv run python cli.py session new "b12 bounds" --provider mock --model mock-1) -replace '\s.*',''
uv run python cli.py send "@research list then read" --session $sidB --script two-tool-calls --max-steps 1 --wait --timeout 60

Write-Host "`n--- timed_out: the same error class, different bound"
$sidT = (uv run python cli.py session new "b12 timeout" --provider mock --model mock-slow) -replace '\s.*',''
uv run python cli.py send "@research slow" --session $sidT --script mock_slow --max-seconds 1 --wait --timeout 60

Write-Host "`n--- cancelled mid-stream"
$sidC = (uv run python cli.py session new "b12 cancel" --provider mock --model mock-slow) -replace '\s.*',''
uv run python cli.py send "slow please" --session $sidC --script mock_slow --cancel-after 1 --timeout 60
Remove-Item Env:\POINT_MOCK

Write-Host "`n--- the rows (role | task | outcome | interrupted | error_code | agent | length):"
sqlite3 $db "select role, substr(coalesce(task_id,'NULL'),1,8), coalesce(outcome,'NULL'), interrupted, coalesce(error_code,'-'), coalesce(json_extract(metadata,'$.invocation.agent'),'NULL'), length(content) from messages where outcome is not null order by rowid;"
Write-Host ""
Write-Host "EXPECT: one row each for bounds_exceeded, timed_out and cancelled --"
Write-Host "        every one with interrupted=1 and a NON-ZERO length. The partial"
Write-Host "        text survives; the marker only says how it ended."
Write-Host "FAILS IF: outcome is null or 'done' for any of them, or a row is missing."

Write-Host "`n--- exactly one terminal event per cancelled task:"
Write-Host "    (checked inside manual_b12_concurrency.py for the queued case; above,"
Write-Host "     the send prints '[TaskCancelled]' once and only once.)"

# ---------------------------------------------------------------------------
# #9: provenance survives persistence
# ---------------------------------------------------------------------------
Write-Host "`n=== #9. every assistant message names the agent that produced it" -ForegroundColor Cyan
$env:POINT_MOCK = "1"
$sid9 = (uv run python cli.py session new "b12 provenance" --provider mock --model mock-1) -replace '\s.*',''
uv run python cli.py send "@research something short" --session $sid9 --script mock_stream --wait --timeout 60 | Out-Null
uv run python cli.py send "a plain question" --session $sid9 --script mock_stream --timeout 60 | Out-Null
Remove-Item Env:\POINT_MOCK

Write-Host "--- role | agent (the gate's own query):"
sqlite3 $db "select role, coalesce(json_extract(metadata,'$.invocation.agent'),'NULL') from messages where session_id='$sid9' order by rowid;"
Write-Host ""
Write-Host "EXPECT: the research reply names 'research'; the plain reply names the"
Write-Host "        session's current agent ('general'). NOT null for either."
Write-Host "FAILS IF: either is null -- the frontend cannot rebuild a slot from a null."
Write-Host "NOTE:   invocation lives in the 'metadata' COLUMN, not inside 'content'."
Write-Host "        (session_repository.py stores it beside the free-form metadata dict,"
Write-Host "         so json_extract reads metadata, not content.)"

# ---------------------------------------------------------------------------
# Question-to-answer pairing
# ---------------------------------------------------------------------------
Write-Host "`n=== every user message carries the task_id of its own answer" -ForegroundColor Cyan
sqlite3 $db "select (select count(*) from messages where role='user' and task_id is null and outcome is null and session_id='$sid9') as user_without_task, (select count(*) from messages m1 join messages m2 on m1.task_id=m2.task_id and m1.role='user' and m2.role='assistant' where m1.session_id='$sid9') as paired;"
Write-Host "EXPECT: user_without_task = 0, paired = 2."
Write-Host "WHY:    executor.py used to build the UserMessage before the task_id"
Write-Host "        existed, so a question could not be paired with its answer on"
Write-Host "        reload and a retry could not be told from a genuine repeat."

# ---------------------------------------------------------------------------
Write-Host "`n=== done" -ForegroundColor Cyan
Write-Host "scratch profile left at: $scratch"
Write-Host "delete it with:  Remove-Item -Recurse -Force `"$scratch`""
