# B14 verification -- usage capture, pricing, and the daily spending cap.
#
#   Run from anywhere:
#     powershell -ExecutionPolicy Bypass -File D:\Projects\Point\backend\tests\manual_b14_usage.ps1
#
# ONE script, because separate tool calls do not share shell state: the
# scratch-profile redirection below has to hold for every command that
# follows it, and a per-command `$env:` patch would leave later commands
# writing to the LIVE %APPDATA%\Point and %LOCALAPPDATA%\Point (CLAUDE.md).
#
# Nothing here touches the live profile or the live runtime.db. APPDATA
# and LOCALAPPDATA are both overridden to the same scratch directory, so
# config.toml and runtime.db both resolve under it -- and nothing under
# the real Point folder is named in a destructive call anywhere in this
# file. The scratch directory is printed at the end and is yours to
# delete.
#
# Deterministic throughout: POINT_MOCK=1 replays
# tests\fixtures\mock_stream.json (11 input / 5 output tokens, scripted),
# so no Ollama, no key and no network.
#
# READING THE OUTPUT: a refusal is written to stderr via cli.py's
# `_print_errors`, and Windows PowerShell wraps a native command's stderr
# in a red NativeCommandError block. That decoration is PowerShell's, not
# a crash -- the refusal text is the first line of the block, and seeing
# it for send #3 below is the check PASSING.

$ErrorActionPreference = "Continue"

$scratch = Join-Path $env:TEMP ("point-b14-gate-" + [guid]::NewGuid().ToString("N").Substring(0, 8))
New-Item -ItemType Directory -Force (Join-Path $scratch "Point") | Out-Null
$env:APPDATA      = $scratch
$env:LOCALAPPDATA = $scratch
$env:POINT_MOCK   = "1"

Set-Location D:\Projects\Point\backend
$db = Join-Path $scratch "Point\runtime.db"

# A priced mock model (so cost_micros is exercised) and a daily cap small
# enough that two 16-token mock sends (11 in + 5 out each, from
# tests\fixtures\mock_stream.json) cross it. config.toml is user-owned and
# never rewritten in the real profile (CLAUDE.md) -- this one lives only
# under $scratch.
$configToml = @'
[pricing.mock."mock-1"]
input_per_mtok  = 1.00
output_per_mtok = 2.00

[limits]
daily_token_cap = 30
'@
Set-Content -Path (Join-Path $scratch "Point\config.toml") -Value $configToml -Encoding utf8

uv run python cli.py migrate

Write-Host "`n=== 1: capture -- one send writes exactly one priced usage_records row" -ForegroundColor Cyan
$sid = (uv run python cli.py session new "b14 capture" --provider mock --model mock-1) -replace '\s.*', ''
uv run python cli.py send "hello" --session $sid --script mock_stream --wait --timeout 60 | Out-Null
sqlite3 $db "select provider, model, input_tokens, output_tokens, cost_micros, estimated from usage_records where session_id='$sid';"
Write-Host "EXPECT: mock|mock-1|11|5|21|0  (cost = 11*1.00 + 5*2.00 = 21 micros)"

Write-Host "`n=== 2: session totals equal the sum of that session's rows" -ForegroundColor Cyan
uv run python cli.py usage session --session $sid
sqlite3 $db "select sum(input_tokens), sum(output_tokens) from usage_records where session_id='$sid';"
Write-Host "EXPECT: the two agree exactly (11, 5)."

Write-Host "`n=== 3: unknown model -> NULL cost, no crash" -ForegroundColor Cyan
Write-Host "NOTE: this send goes through the real capture path end to end (executor -> runtime"
Write-Host "sink -> UsageManager.record) -- it is not a row inserted directly by this script."
Write-Host "'mock-slow' is just a model name absent from this scratch config's [pricing] table,"
Write-Host "the same way a real, newly-released model would be absent until priced by hand."
$sidU = (uv run python cli.py session new "b14 unpriced" --provider mock --model "mock-slow") -replace '\s.*', ''
uv run python cli.py send "hi" --session $sidU --script mock_stream --wait --timeout 60 | Out-Null
sqlite3 $db "select model, cost_micros from usage_records where session_id='$sidU' order by rowid desc limit 1;"
Write-Host "EXPECT: mock-slow| (empty/NULL cost_micros), nothing above crashed."

Write-Host "`n--- resetting the DB so the cap demo below starts at a known 0 ---" -ForegroundColor DarkGray
Remove-Item -LiteralPath $db -Force -ErrorAction SilentlyContinue
uv run python cli.py migrate | Out-Null

Write-Host "`n=== 4: the daily cap -- before vs after" -ForegroundColor Cyan
Write-Host "WHY:    the real cost of a call (tokens, let alone cost_micros) is not"
Write-Host "        knowable until the provider responds -- see managers\usage.py's"
Write-Host "        module docstring. So the cap can only check the running total"
Write-Host "        already on disk before a call starts; it cannot stop the call"
Write-Host "        that crosses it, because that call's own size is unknown until"
Write-Host "        it finishes. This section demonstrates both halves of that."
$sidCap = (uv run python cli.py session new "b14 cap" --provider mock --model mock-1) -replace '\s.*', ''

Write-Host "`nsend #1 (16 tok: 11 in + 5 out) -- today's total is 0 (< cap 30) before this call starts:"
uv run python cli.py usage today
uv run python cli.py send "one" --session $sidCap --script mock_stream --wait --timeout 60
Write-Host "`n--> AFTER #1 ('after' half): it completed normally -- nothing to refuse yet:"
uv run python cli.py usage today

Write-Host "`nsend #2 (16 tok) -- today's total is 16 (still < cap 30) before this call starts, so it"
Write-Host "is ALLOWED to run. This is the send whose OWN cost pushes the total past the cap --"
Write-Host "nothing could have blocked it in advance, because its cost was unknown until it returned:"
uv run python cli.py send "two" --session $sidCap --script mock_stream --wait --timeout 60
Write-Host "`n--> AFTER #2: today's total is now 32, over the cap of 30 -- #2 was NOT refused"
Write-Host "    (it is the request that crossed the cap; crossing is the thing this design cannot prevent):"
uv run python cli.py usage today

Write-Host "`nsend #3 ('before' half) -- BEFORE this call starts, today's total (32) already exceeds"
Write-Host "the cap (30). The refusal must happen before any provider call: no TaskStarted, no new"
Write-Host "usage_records row, and a non-zero exit code:"
$before = (sqlite3 $db "select count(*) from usage_records;")
$sw = [Diagnostics.Stopwatch]::StartNew()
uv run python cli.py send "three" --session $sidCap --script mock_stream --wait --timeout 60
$exitCode = $LASTEXITCODE
$sw.Stop()
$after = (sqlite3 $db "select count(*) from usage_records;")
Write-Host "`nexit code: $exitCode"
Write-Host "usage_records count before=$before after=$after"
Write-Host "elapsed: $($sw.Elapsed.TotalSeconds)s"
uv run python cli.py usage today
Write-Host "EXPECT: exit code non-zero; before == after (no row for the refused send); total"
Write-Host "        UNCHANGED at 32 (22 input + 10 output); the error text names both numbers"
Write-Host "        ('today's usage (32 tokens) has reached the daily cap (30 tokens)')."
Write-Host "FAILS IF: #3 produces a new usage_records row, or the total above moved -- that"
Write-Host "          would mean the send was aborted AFTER running, not refused before it started."

Write-Host "`n=== 5: daily_token_cap = 0 means unlimited, not a zero budget" -ForegroundColor Cyan
Write-Host "WHY:    an empty [limits] table must not silently block every request the moment"
Write-Host "        the section exists (see managers\usage.py's daily_cap_tokens()); 0 reads"
Write-Host "        the same way -- nobody configures a real cap of zero tokens."
$configZeroCap = @'
[pricing.mock."mock-1"]
input_per_mtok  = 1.00
output_per_mtok = 2.00

[limits]
daily_token_cap = 0
'@
Set-Content -Path (Join-Path $scratch "Point\config.toml") -Value $configZeroCap -Encoding utf8
Write-Host "today's total going in is already 32 (from section 4) -- with a real nonzero cap"
Write-Host "anywhere near that, this send would be refused like #3 was. With daily_token_cap = 0:"
uv run python cli.py send "four" --session $sidCap --script mock_stream --wait --timeout 60
$exitCodeZero = $LASTEXITCODE
Write-Host "`nexit code: $exitCodeZero"
uv run python cli.py usage today
Write-Host "EXPECT: exit code 0 (not refused); today's total moved from 32 to 48 (3rd successful"
Write-Host "        send's 16 tokens added) -- despite being far past what any nonzero cap in"
Write-Host "        this script would have allowed."
Write-Host "FAILS IF: this send is refused, or exits non-zero -- that would mean 0 is being read"
Write-Host "          as a real ceiling instead of 'no cap configured'."

Write-Host "`n=== done" -ForegroundColor Cyan
Write-Host "scratch profile left at: $scratch"
Write-Host "delete it with:  Remove-Item -Recurse -Force `"$scratch`""
