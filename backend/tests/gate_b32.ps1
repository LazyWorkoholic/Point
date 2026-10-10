# B32 verification gate -- WriteProjectFile, the editor's save path.
#
#   powershell -NoProfile -ExecutionPolicy Bypass -File backend\tests\gate_b32.ps1
#
# Scratch profile, per CLAUDE.md: $env:APPDATA and $env:LOCALAPPDATA both
# point at a scratch directory for this script's duration, so Point's
# process never sees the real %APPDATA%\Point / %LOCALAPPDATA%\Point path
# at all, and the real profile is never named in any call, destructive or
# otherwise. $env:POINT_DB alone is not enough. (The rename-aside pattern
# is retired.) Every target file lives inside the scratch tree; nothing
# here touches the repo.
#
# ----------------------------------------------------------------------
# WHY $ErrorActionPreference IS "Continue" AND NOT "Stop"
# ----------------------------------------------------------------------
# Measured, not assumed. In Windows PowerShell 5.1 a native command that
# writes to stderr produces a NativeCommandError, which "Stop" promotes
# to terminating. The backend logs to stderr BY DESIGN (CLAUDE.md:
# "Never log to stdout in Python" -- stdout is the protocol channel), and
# this gate DELIBERATELY provokes stderr several times: every refusal
# logs, and check 12 expects an Error envelope. Under "Stop" the script
# aborted at the first one with FullyQualifiedErrorId:
# NativeCommandError, before a single assertion ran.
#
# Safety here does not come from "Stop". It comes from the two env-var
# overrides and from never naming the real Point folder.
#
# CORRECTNESS comes from Assert-That below, not from an ambient
# preference. "Continue" means an UNRELATED failure (a bad flag, a
# crash, a real regression) would otherwise print and fall through, so
# every check asserts its expected value programmatically instead of
# leaving it as an EXPECT comment for a human to eyeball. The three
# genuinely eyes-only items are listed at the end of the run.
# ----------------------------------------------------------------------

$ErrorActionPreference = "Continue"
$script:b32Failures = @()

function Assert-That {
    param([string]$Name, [bool]$Ok, [string]$Detail = "")
    if ($Ok) {
        Write-Host "PASS  $Name$(if ($Detail) { "  ($Detail)" })"
    } else {
        $script:b32Failures += $Name
        Write-Host "FAIL  $Name  $Detail"
    }
}

$scratch = Join-Path $env:TEMP "point-scratch-b32"
New-Item -ItemType Directory -Force $scratch | Out-Null
$realAppData      = $env:APPDATA
$realLocalAppData = $env:LOCALAPPDATA
try {
    $env:APPDATA      = $scratch
    $env:LOCALAPPDATA = $scratch
    Set-Location D:\Projects\Point\backend

    $db = Join-Path (Join-Path $scratch "Point") "runtime.db"

    # --- setup. Asserted because a failed setup makes every check after
    #     it meaningless, and under "Continue" it would not stop anything.
    $migrate = uv run python cli.py migrate
    Assert-That "setup: migrate" ($LASTEXITCODE -eq 0) "exit $LASTEXITCODE"

    $proj = Join-Path $scratch "work"
    New-Item -ItemType Directory -Force $proj | Out-Null
    $initOut = uv run python cli.py project init --path $proj
    Assert-That "setup: project init" ($LASTEXITCODE -eq 0 -and $initOut -match [regex]::Escape($proj)) `
        "exit $LASTEXITCODE"

    # --- 1: a CRLF + BOM file, written as exact bytes
    $crlf = Join-Path $proj "crlf_bom.py"
    $bom  = [byte[]](0xEF,0xBB,0xBF)
    $body = [System.Text.Encoding]::UTF8.GetBytes("def a():`r`n    return 1`r`n")
    [System.IO.File]::WriteAllBytes($crlf, [byte[]]($bom + $body))
    $before = (Get-FileHash -LiteralPath $crlf -Algorithm SHA256).Hash

    # --- 3: the new read-side metadata. ASSERTED, not eyeballed.
    $meta = uv run python cli.py project read-file --path $proj --file crlf_bom.py
    $metaText = $meta -join "`n"
    Assert-That "3: read-file reports line_ending crlf" ($metaText -match 'line_ending:\s+crlf')
    Assert-That "3: crlf_count 2"            ($metaText -match 'crlf_count:\s+2\b')
    Assert-That "3: lf_count 0"              ($metaText -match 'lf_count:\s+0\b')
    Assert-That "3: bom True"                ($metaText -match 'bom:\s+True')
    Assert-That "3: trailing_newline True"   ($metaText -match 'trailing_newline:\s+True')
    Assert-That "3: decode_replacements 0"   ($metaText -match 'decode_replacements:\s+0\b')
    Assert-That "3: writable True"           ($metaText -match 'writable:\s+True')
    # The BOM must be REPORTED, not left inside content. The first content
    # line comes straight after the --- separator.
    $sepIndex = [array]::IndexOf($meta, "---")
    $firstContentLine = if ($sepIndex -ge 0 -and $meta.Count -gt ($sepIndex + 1)) { $meta[$sepIndex + 1] } else { "" }
    Assert-That "3: content has no leading U+FEFF" `
        ($firstContentLine.Length -gt 0 -and [int][char]$firstContentLine[0] -ne 0xFEFF) `
        "first content char U+$(('{0:X4}' -f [int][char]$firstContentLine[0]))"

    # --- 1, 2, 9, 10, 11a, 12b, 13: the unit-level checks.
    #
    # A byte-exact round trip CANNOT be driven through a shell pipeline:
    # capturing stdout and writing it back is itself a line-ending
    # transformation, so a gate that tried would be testing PowerShell's
    # redirection rather than this episode.
    #
    # ITS EXIT CODE IS ASSERTED. Without this the script could print
    # "FAIL" for every row and the gate would carry on to report PASS
    # for everything after it -- the single worst consequence of running
    # under "Continue".
    uv run python tests\manual_b32_write_roundtrip.py --root $proj
    Assert-That "unit: manual_b32_write_roundtrip.py" ($LASTEXITCODE -eq 0) "exit $LASTEXITCODE"
    # Covers, at minimum: CRLF+BOM round trip, LF no-BOM round trip,
    # no-trailing-newline round trip, mixed endings reported as "mixed"
    # with both counts, a lock acquired on a non-existent path, create via
    # expected_mtime null, a second create refused "exists",
    # no_parent_dir creating no directory, a failed os.replace leaving the
    # original intact with no temp file behind, content above
    # editor.max_file_kb refused "too_large" with the file unchanged
    # (Done-when 11a), the read_file TOOL still refusing at
    # index.max_file_kb (the regression test for the shared-cap defect),
    # and an unresolvable project_id arriving as an Error (Done-when 12b).

    $after = (Get-FileHash -LiteralPath $crlf -Algorithm SHA256).Hash
    Assert-That "1: CRLF+BOM file byte-identical after the unit round trips" `
        ($before -eq $after) "$before"

    # --- 4, 5: staleness refusal, then the overwrite.
    #
    # The expected_mtime MUST come from read-file's own reported value,
    # not from PowerShell date arithmetic: Get-Date -UFormat %s truncates
    # to whole seconds, check_stale's tolerance is 1e-6, so a truncated
    # value is ALWAYS stale and check 4 would pass without testing
    # anything.
    $mtime = [double]((($meta | Select-String '^mtime:').Line) -replace '^mtime:\s*', '')
    Add-Content -LiteralPath $crlf -Value "# external"      # change it behind Point
    $src = Join-Path $scratch "payload.txt"
    "def a():`r`n    return 2`r`n" | Set-Content -LiteralPath $src -NoNewline
    $shaBeforeRefusal = (Get-FileHash -LiteralPath $crlf -Algorithm SHA256).Hash

    $staleOut = uv run python cli.py project write-file --path $proj --file crlf_bom.py `
        --content-from $src --holder "aryan@b32-test-id" `
        --expect-mtime $mtime --line-ending crlf --bom
    $staleText = $staleOut -join "`n"
    Assert-That "4: refused with reason stale" ($staleText -match 'refused\s+stale')
    $staleMtime = [double]((($staleOut | Select-String '^mtime:').Line) -replace '^mtime:\s*', '')
    # EYES-ON 1, also asserted: the reported mtime is the file's CURRENT
    # one, not the expected_mtime echoed back. A handler that echoed its
    # input would pass every other check in this gate.
    Assert-That "4: the stale reply reports the CURRENT mtime, not the input" `
        ([math]::Abs($staleMtime - $mtime) -gt 1e-6) `
        "reported $staleMtime vs sent $mtime"
    Assert-That "4: a refused write left the file byte-identical" `
        ((Get-FileHash -LiteralPath $crlf -Algorithm SHA256).Hash -eq $shaBeforeRefusal)

    $owOut = uv run python cli.py project write-file --path $proj --file crlf_bom.py `
        --content-from $src --holder "aryan@b32-test-id" `
        --overwrite --line-ending crlf --bom
    $owText = $owOut -join "`n"
    Assert-That "5: overwrite succeeded" ($LASTEXITCODE -eq 0 -and $owText -match '^ok\s') "exit $LASTEXITCODE"
    $owMtime = [double]((($owOut | Select-String '^mtime:').Line) -replace '^mtime:\s*', '')
    # "its reported mtime matches a fresh stat" -- compared against a
    # fresh read-file rather than a .NET timestamp, so both numbers come
    # from the same Python st_mtime and no epoch conversion can skew it.
    $reread = uv run python cli.py project read-file --path $proj --file crlf_bom.py
    $rereadMtime = [double]((($reread | Select-String '^mtime:').Line) -replace '^mtime:\s*', '')
    Assert-That "5: the write's reported mtime matches a fresh stat" `
        ([math]::Abs($owMtime - $rereadMtime) -lt 1e-6) `
        "write $owMtime vs fresh read $rereadMtime"
    Assert-That "5: the overwrite preserved CRLF and the BOM" `
        (($reread -join "`n") -match 'line_ending:\s+crlf' -and ($reread -join "`n") -match 'bom:\s+True')

    # --- 6, 7, 8: the lock interaction
    $other = "someone-else@other-id"
    uv run python cli.py project lock acquire --path $proj --file crlf_bom.py --holder $other | Out-Null
    Assert-That "6: setup -- a second holder took the lock" ($LASTEXITCODE -eq 0)
    $hbBefore = sqlite3 $db "SELECT heartbeat_at FROM file_locks;"
    $shaBeforeLocked = (Get-FileHash -LiteralPath $crlf -Algorithm SHA256).Hash

    $lockedOut = uv run python cli.py project write-file --path $proj --file crlf_bom.py `
        --content-from $src --holder "aryan@b32-test-id" --overwrite
    $lockedText = $lockedOut -join "`n"
    Assert-That "6: refused with reason locked" ($lockedText -match 'refused\s+locked')
    Assert-That "6: the refusal names the other holder" ($lockedText -match [regex]::Escape($other))
    Assert-That "6: the refusal reports an age" ($lockedText -match 'age_seconds:\s+[\d.]+')
    Assert-That "6: a locked refusal left the file byte-identical" `
        ((Get-FileHash -LiteralPath $crlf -Algorithm SHA256).Hash -eq $shaBeforeLocked)

    # EYES-ON 2, also asserted: a REFUSED write must not touch the
    # heartbeat. Touching it would extend a lock on behalf of a write
    # that never happened -- and here, one owned by somebody else.
    $hbAfter = sqlite3 $db "SELECT heartbeat_at FROM file_locks;"
    Assert-That "7: heartbeat_at UNCHANGED after a refused locked write" `
        ($hbAfter -eq $hbBefore) "before $hbBefore / after $hbAfter"

    uv run python cli.py project lock release --path $proj --file crlf_bom.py --holder $other | Out-Null
    $freeOut = uv run python cli.py project write-file --path $proj --file crlf_bom.py `
        --content-from $src --holder "aryan@b32-test-id" --overwrite
    Assert-That "6: the same write succeeds once the lock is released" `
        ($LASTEXITCODE -eq 0 -and ($freeOut -join "`n") -match '^ok\s')
    $lockRows = sqlite3 $db "SELECT count(*) FROM file_locks;"
    Assert-That "8: a write by an unlocked holder created NO lock row" `
        ($lockRows -eq "0") "file_locks rows: $lockRows"

    # --- 11, second clause: a file BETWEEN the two caps opens and saves.
    #     12000 rows, not 6000: 6000 is only ~80KB, which is UNDER
    #     index.max_file_kb and so proves nothing. Measured -- 12000 rows
    #     is 169,786 bytes.
    $big = Join-Path $proj "big.py"
    (1..12000 | ForEach-Object { "x$_ = $_" }) -join "`r`n" |
        Set-Content -LiteralPath $big -NoNewline
    $bigLen = (Get-Item -LiteralPath $big).Length
    Assert-That "11b: the fixture is between the two caps" `
        ($bigLen -gt 102400 -and $bigLen -lt 524288) "$bigLen bytes"

    $bigMeta = uv run python cli.py project read-file --path $proj --file big.py
    Assert-That "11b: a file over index.max_file_kb opens on the EDITOR path" `
        ($LASTEXITCODE -eq 0) "exit $LASTEXITCODE -- under the old 100KB cap this was refused outright"
    Assert-That "11b: and reports crlf_count 11999" `
        (($bigMeta -join "`n") -match 'crlf_count:\s+11999\b')

    $listed = uv run python cli.py project list-files --path $proj --limit 500
    Assert-That "11b: big.py is ABSENT from the file tree" `
        (-not (($listed -join "`n") -match '(?m)^big\.py$')) `
        "the accepted known limitation recorded in the Handoff, not a bug"

    # --- 11, first clause: ABOVE editor.max_file_kb is refused.
    #     Done-when 11 has two clauses and the second one (a file between
    #     the two caps opens and saves) does NOT exercise this one at all.
    $huge = Join-Path $scratch "huge_payload.txt"
    ("y" * 600000) | Set-Content -LiteralPath $huge -NoNewline
    $shaSmall = (Get-FileHash -LiteralPath $crlf -Algorithm SHA256).Hash
    $hugeOut = uv run python cli.py project write-file --path $proj --file crlf_bom.py `
        --content-from $huge --holder "aryan@b32-test-id" --overwrite
    $hugeText = $hugeOut -join "`n"
    Assert-That "11a: refused with reason too_large" ($hugeText -match 'refused\s+too_large')
    Assert-That "11a: the refusal reports size and limit_kb" `
        ($hugeText -match 'size:\s+\d+' -and $hugeText -match 'limit_kb:\s+512')
    Assert-That "11a: a too_large refusal left the file byte-identical" `
        ((Get-FileHash -LiteralPath $crlf -Algorithm SHA256).Hash -eq $shaSmall)

    # --- 12, first clause: outside the project root is an Error envelope.
    #
    # Error-vs-refusal is the distinction Done-when 12 is about, and the
    # two are told apart by STDOUT: a refusal prints "refused <slug>"
    # there, an Error prints nothing to stdout and goes to stderr. The
    # stderr text below is EXPECTED OUTPUT, not a failure.
    $outsideOut = uv run python cli.py project write-file --path $proj `
        --file (Join-Path $scratch "outside.py") `
        --content-from $src --holder "aryan@b32-test-id" --create
    $outsideCode = $LASTEXITCODE
    Assert-That "12a: an out-of-root path FAILS" ($outsideCode -ne 0) "exit $outsideCode"
    Assert-That "12a: and as an Error envelope, NOT an ok:false reason slug" `
        (-not (($outsideOut -join "`n") -match 'refused'))

    # --- 12, second clause: an UNRESOLVABLE project_id is also an Error.
    #     Two different raisers -- resolve_path vs _require_project -- and
    #     the first does not exercise the second. Unit-level because the
    #     CLI resolves its own project from --path and so cannot send a
    #     garbage project_id at all.
    uv run python tests\manual_b32_write_roundtrip.py --root $proj --case bad-project
    Assert-That "12b: unresolvable project_id raises (unit)" ($LASTEXITCODE -eq 0) "exit $LASTEXITCODE"

    # --- 14: the read_file TOOL still works after the BOM change.
    #
    # FOUR things all have to be right or this check silently tests
    # nothing:
    #   * --provider mock on the SESSION. POINT_MOCK=1 only REGISTERS the
    #     mock provider; `session new` otherwise picks ollama and the
    #     fixture is never replayed. (Measured: without it the reply was
    #     a real ollama answer and no tool ran at all.)
    #   * --agent research. The default `general` agent has tools=() and
    #     max_tool_calls=0, so the turn dies bounds_exceeded after 0
    #     calls. `research` is the builtin granted read_file/list_files.
    #   * --path on `session new`, which binds the project the tools are
    #     guarded against.
    #   * a real CLAUDE.md inside the scratch project -- the fixture reads
    #     that exact path, and without the file the tool returns
    #     "Not a file" and the read is never exercised. Give it a BOM, so
    #     this check also proves the BOM is stripped on the tool path.
    $bomClaude = Join-Path $proj "CLAUDE.md"
    $cbody = [System.Text.Encoding]::UTF8.GetBytes("# Point`r`nA BOM'd file.`r`n")
    [System.IO.File]::WriteAllBytes($bomClaude, [byte[]]($bom + $cbody))

    $env:POINT_MOCK = "1"
    $sid = (uv run python cli.py session new "b32" --path $proj --provider mock).Split(" ")[0]
    $sendOut = uv run python cli.py send "read something" --session $sid --agent research `
        --wait --script two-tool-calls
    Remove-Item Env:\POINT_MOCK
    $sendText = $sendOut -join "`n"
    Assert-That "14: the list_files tool completed ok" `
        ($sendText -match 'tool\.completed list_files[^\]]*ok\]')
    Assert-That "14: the read_file tool completed ok (not 'Not a file')" `
        ($sendText -match 'tool\.completed read_file[^\]]*ok\]')
    $toolRows = sqlite3 $db "SELECT tool || '|' || ok FROM tool_calls ORDER BY id;"
    $toolText = $toolRows -join "`n"
    Assert-That "14: tool_calls shows ok=1 on BOTH rows" `
        ($toolText -match 'list_files\|1' -and $toolText -match 'read_file\|1') "$toolText"

    # ------------------------------------------------------------------
    Write-Host ""
    if ($script:b32Failures.Count -eq 0) {
        Write-Host "B32 GATE: all assertions passed."
    } else {
        Write-Host "B32 GATE: $($script:b32Failures.Count) FAILED:"
        $script:b32Failures | ForEach-Object { Write-Host "  - $_" }
    }
    Write-Host ""
    Write-Host "Still eyes-on (asserted above, but worth reading the numbers):"
    Write-Host "  1. check 4's stale reply reports the file's CURRENT mtime, not the input."
    Write-Host "  2. check 7's heartbeat_at is identical before and after the refused locked write."
    Write-Host "  3. the unit block's 'failed-replace-leaves-original' prints temps=[]."
    Write-Host "     If it says SKIPPED instead, this host allowed the replace while a"
    Write-Host "     reader held the handle, and only the no-temp half actually ran."
    Write-Host ""
    Write-Host "Expected stderr, NOT failures: the 'WARNING point.project: write_file failed"
    Write-Host "for ...held_open.py' line (check 13's deliberate os.replace failure) and the"
    Write-Host "'error: ... outside project root' line (check 12a's Error envelope). PowerShell"
    Write-Host "5.1 renders both as red NativeCommandError blocks."

    if ($script:b32Failures.Count -gt 0) {
        throw "B32 gate failed: $($script:b32Failures -join ', ')"
    }
}
finally {
    $env:APPDATA      = $realAppData
    $env:LOCALAPPDATA = $realLocalAppData
    Remove-Item -LiteralPath $scratch -Recurse -Force -ErrorAction SilentlyContinue
}
