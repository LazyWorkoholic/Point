# Shared helpers for this repo's PowerShell gate scripts.
#
# Dot-source it:  . (Join-Path $PSScriptRoot "gatelib.ps1")
#
# It exists because the same two PowerShell traps have now bitten three
# separate gate scripts, and fixing them at the call site each time is
# how a fourth one inherits them.
#
# ---------------------------------------------------------------------
# TRAP 1 -- a native command that writes NOTHING to stdout does not give
# you `$null`. It gives you AutomationNull, which `-eq $null` reports as
# true but which the COMPARISON OPERATORS treat as an empty COLLECTION:
#
#     $empty = & sqlite3 $db "select x from t where 1=0;"
#     $null -eq $empty          ->  True        (looks like null)
#     $empty.GetType()          ->  throws     (really is null-ish)
#     $empty -like  "*word01*"  ->  Object[]   <-- NOT a Boolean
#     $empty -match "word01"    ->  Object[]   <-- NOT a Boolean either
#
# Measured on this machine, PowerShell 5.1. That last value then hits a
# `[bool]`-typed parameter and throws:
#
#     Cannot convert value "System.Object[]" to type "System.Boolean"
#
# which is a CRASHED GATE instead of a reported failure -- and it fires
# only when the query returns nothing, i.e. exactly when a check was
# about to fail anyway. B15's crash gate hit this at the one moment it
# mattered: the assertion that would have said "no checkpoint row" threw
# instead, so the run reported a PowerShell type error rather than the
# product fact underneath it.
#
# Note it is NOT specific to `-like`. `-match`, `-eq` and `-ne` all take
# the collection-filtering path on an empty-collection left-hand side.
#
# TRAP 2 -- `sqlite3.exe` has NO BUSY TIMEOUT by default. Every
# connection Point itself opens sets `busy_timeout = 5000` (PRAGMAS in
# `database\migrations.py`), but a bare `sqlite3 $db "..."` in a gate
# script sets nothing, so a query that lands while the backend is
# mid-write fails INSTANTLY with:
#
#     Error: in prepare, database is locked (5)
#
# on stderr, nothing on stdout -- which is Trap 1's empty capture, with
# a cause that has nothing to do with the thing being tested. Measured
# while polling a live backend during B15's gate work.
#
# `Invoke-Sql` closes both: it passes a real timeout, keeps stderr out
# of the return value, distinguishes "the query failed" from "no rows",
# and always returns a SCALAR STRING.

# Deliberately no `Set-StrictMode` here. Dot-sourcing applies it to the
# CALLER's scope, so a helper file that turned it on would change the
# rules for every script that includes it -- which is how a shared
# helper breaks the scripts it was added to help.

# How long a gate query waits for a writer to finish before giving up.
# Matches PRAGMAS' busy_timeout in database\migrations.py on purpose: a
# check should be as patient as the product is.
$script:SqlBusyTimeoutMs = 5000

function Invoke-Sql {
    <#
    .SYNOPSIS
    Run one SQL statement against a SQLite file and return a scalar string.

    .DESCRIPTION
    Always returns a [string] -- "" when the query matched no rows, and
    the rows joined by newline when it matched several. Never
    AutomationNull, never an array, so the result is safe to compare
    with -like / -match / -eq (see Trap 1 at the top of this file).

    stderr is captured to a temp file rather than merged with `2>&1`,
    because merging wraps each stderr line in an ErrorRecord and puts it
    IN the return value -- turning a lock message into something a
    pattern match would happily succeed against.

    A genuine failure (bad SQL, locked past the timeout, missing file)
    is reported through -ErrorVariable style output parameters rather
    than by returning a misleading "": pass -FailedVar to find out.
    #>
    param(
        [Parameter(Mandatory = $true)][string]$Database,
        [Parameter(Mandatory = $true)][string]$Query,
        # Receives $true when sqlite3 itself failed, as distinct from a
        # query that legitimately returned no rows.
        [string]$FailedVar,
        # Receives sqlite3's stderr text, if any.
        [string]$ErrorTextVar,
        [switch]$Header
    )

    # TRAP 3, found while fixing the first two: in PowerShell 5.1 a
    # native command that writes to stderr while its stderr is
    # REDIRECTED produces a NativeCommandError ErrorRecord -- and under
    # `$ErrorActionPreference = "Stop"`, which gate scripts reasonably
    # set, that is TERMINATING. So `sqlite3 "select * from nope"` killed
    # the whole script instead of returning a failure this function
    # could report. Measured: it threw out of this very line.
    #
    # Set function-locally, so the caller's own preference is untouched
    # the moment this returns.
    #
    # `Continue`, not `SilentlyContinue`, and the difference was
    # measured: SilentlyContinue stops the throw but also DISCARDS the
    # stderr lines, so `-ErrorTextVar` came back empty on a failure it
    # had correctly detected -- detectable but unexplainable. `Continue`
    # keeps them, and because stderr is merged into the success stream
    # by the `2>&1` below they arrive in $raw rather than being printed
    # to the host.
    $ErrorActionPreference = "Continue"

    $sqlArgs = @("-cmd", ".timeout $script:SqlBusyTimeoutMs")
    if ($Header) { $sqlArgs += "-header" }
    $sqlArgs += @($Database, $Query)

    # `2>&1` here, and that is deliberate rather than careless. The PS
    # 5.1 behaviour everyone warns about -- each stderr line comes back
    # WRAPPED in an ErrorRecord -- is exactly what makes the two streams
    # separable afterwards by TYPE, with no temp file and no shell
    # quoting of the SQL through cmd.
    #
    # Redirecting to a file instead was tried first and does not work:
    # PowerShell intercepts the record rather than passing the handle
    # through, so the file came back empty while the exit code was
    # correctly non-zero -- a failure you can detect but not explain.
    # Saved and restored below. A gate script's normal shape is
    # `<command>; Step "name" $LASTEXITCODE`, and an Invoke-Sql landing
    # between those two lines would otherwise hand the Step sqlite3's
    # exit code instead of the command's -- a check reporting on the
    # wrong process, silently. Callers that want sqlite3's own result
    # ask for it with -FailedVar.
    $callerExitCode = $LASTEXITCODE

    $raw = @(& sqlite3 @sqlArgs 2>&1)
    $code = $LASTEXITCODE

    $errLines = @($raw | Where-Object { $_ -is [System.Management.Automation.ErrorRecord] })
    $outLines = @($raw | Where-Object { $_ -isnot [System.Management.Automation.ErrorRecord] })

    if ($FailedVar) {
        Set-Variable -Name $FailedVar -Scope 1 -Value ($code -ne 0)
    }
    if ($ErrorTextVar) {
        Set-Variable -Name $ErrorTextVar -Scope 1 `
            -Value ((($errLines | ForEach-Object { $_.ToString() }) -join "; ").Trim())
    }

    if ($null -ne $callerExitCode) {
        $global:LASTEXITCODE = $callerExitCode
    }

    # The coercion that closes Trap 1. `-join` over an empty array
    # yields "", over one row the row, over many the rows -- always a
    # real String, never AutomationNull and never a collection.
    return (($outLines -join "`n")).Trim()
}

function Test-Truthy {
    <#
    .SYNOPSIS
    Normalize anything a check expression might produce into one Boolean.

    .DESCRIPTION
    The second half of the Trap 1 fix, and the half that protects a
    future caller who builds a comparison by hand instead of going
    through Invoke-Sql. Rules, in order:

      $null / AutomationNull  -> $false
      an empty collection     -> $false   (nothing matched)
      a one-element collection-> that element's own truthiness
      a longer collection     -> $true    (the filter matched something)
      anything else           -> [bool] of it

    So a check can never crash the gate on a type transformation. It can
    still FAIL -- which is the point: a failing check must report the
    product fact, not a PowerShell error about the shape of the value
    that was going to report it.
    #>
    param([Parameter(Mandatory = $false, Position = 0)]$Value)

    if ($null -eq $Value) { return $false }
    if ($Value -is [string]) { return [bool]$Value }
    if ($Value -is [System.Collections.IEnumerable]) {
        $items = @($Value)
        if ($items.Count -eq 0) { return $false }
        if ($items.Count -eq 1) { return (Test-Truthy $items[0]) }
        return $true
    }
    return [bool]$Value
}

function Wait-ForSql {
    <#
    .SYNOPSIS
    Poll a query until its result satisfies a predicate. Returns the
    elapsed seconds, or $null if it never did.

    .DESCRIPTION
    This is what replaces a blind `Start-Sleep` before a timing-sensitive
    step. A sleep encodes a guess about how long something takes; this
    waits for the thing to have actually happened, and the bounded
    failure case is a real assertion rather than a silent race.

    B15's crash gate needed it: `Start-Sleep -Seconds 2` before the kill
    left ~0.35s of margin, because the first checkpoint lands at +0.90s
    on an idle machine and +1.65s on a loaded one (measured, four and two
    trials). Step 3 runs straight after a 20-cycle benchmark, so loaded
    is the normal case, and the gate failed on machine load rather than
    on anything about the code.
    #>
    param(
        [Parameter(Mandatory = $true)][string]$Database,
        [Parameter(Mandatory = $true)][string]$Query,
        [Parameter(Mandatory = $true)][scriptblock]$Until,
        [double]$TimeoutSeconds = 15.0,
        [int]$PollMs = 25
    )

    $started = Get-Date
    while (((Get-Date) - $started).TotalSeconds -lt $TimeoutSeconds) {
        $value = Invoke-Sql -Database $Database -Query $Query
        if (Test-Truthy (& $Until $value)) {
            return ((Get-Date) - $started).TotalSeconds
        }
        Start-Sleep -Milliseconds $PollMs
    }
    return $null
}
