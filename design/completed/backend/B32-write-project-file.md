# Episode B32 — `WriteProjectFile`: the Editor's Save Path ✅

**Point can write a project file. `WriteProjectFile` → `ProjectFileWritten` is
the first write path in the backend: one manager method, nine ordered steps,
atomic temp-plus-`os.replace`, seven refusal slugs, and a reply that echoes back
what was *actually* written including the new mtime — which is the whole of
F13's autosave rule 2 and why a save needs no follow-up `CheckFileStale`. The
read moved with it: `ProjectManager.read_file` now reads bytes instead of text,
because `Path.read_text()` opens in universal-newline mode and was destroying
the one fact a byte-exact save has to preserve. Seven additive metadata fields
(`line_ending`, `crlf_count`, `lf_count`, `bom`, `trailing_newline`,
`decode_replacements`, `writable`) plus one non-additive change — the BOM is
stripped from `content` and reported in `bom`. A CRLF+BOM file now round-trips
byte-identical through read → write using only the metadata the read reported.**

> Confirmed by Aryan on 2026-10-10 against
> `backend\tests\gate_b32.ps1`, in a scratch `%APPDATA%`/`%LOCALAPPDATA%`
> profile per CLAUDE.md's env-var-override pattern — the real profile was never
> named in any call, destructive or otherwise. Final line:
> **`B32 GATE: all assertions passed.`** The two unit invocations reported
> `22 passed, 0 failed` and `1 passed, 0 failed`. The only red text in the run
> was the two named expected-stderr cases.
>
> **All three eyes-on items were checked against real numbers, not just
> "something printed":**
>
> | eyes-on | Aryan's measured values | what it rules out |
> |---|---|---|
> | 1 — the `stale` reply reports the **current** mtime | reported `1791623520.08657` vs sent `1791623519.07509` | a handler echoing `expected_mtime` back, which would pass every other check in the gate |
> | 2 — a **refused** write leaves `heartbeat_at` alone | `1791623521.80366` before **and** after | a write refreshing a lock it does not own |
> | 3 — a failed `os.replace` leaves no temp | `temps=[]`, with a real `[WinError 5] Access is denied` in the log above it | the `SKIPPED` path — the refusal was genuinely exercised on this host, not stepped over |
>
> The two assertion mechanisms that could have passed vacuously both held on
> real output: the `---`-separator indexing used by check 3's U+FEFF test, and
> the `^mtime:` regex extractions used by checks 4 and 5.
>
> **Three of this episode's own scope decisions were made by Aryan before any
> code was written** (save ownership, the creation path, heartbeat-on-write),
> and **two more mid-episode** (holder persistence, and closing two gate gaps) —
> all recorded below. **One implementation defect and three gate defects were
> found by running the gate rather than reading it**, which is exactly why this
> episode's task doc insisted the gate be run and not reviewed.

---

## What was built

| file | change |
|---|---|
| `managers\project_manager.py` | `write_file()` (the nine steps), `WriteResult` + `WRITE_REASONS`, `describe_bytes()`, `encode_for_write()`, `_atomic_write()`, `_is_writable()`; `read_file()` rewritten to read bytes and take a `max_kb` parameter |
| `runtime\events.py` | `IncomingEventType.WRITE_PROJECT_FILE`, `OutgoingEventType.PROJECT_FILE_WRITTEN` |
| `runtime\runtime.py` | `_handle_write_project_file`, its `dispatcher.register` line, and `_handle_read_project_file` now passing `max_kb=appconfig.editor_max_file_kb()` |
| `appconfig.py` | `EDITOR_MAX_FILE_KB_DEFAULT = 512` and `editor_max_file_kb()` |
| `cli.py` | `project write-file` (+ its parser and dispatch entry), `_REPLY_TYPES` entry, seven new fields in `cmd_project_read_file`'s print block |
| `frontend\internal\protocol\types.go` | `CmdWriteProjectFile`, `EvProjectFileWritten` — fixed names for F13, **no Go caller**, by design |
| `backend\tests\manual_b32_write_roundtrip.py` | NEW — 23 unit checks, byte-layer round trips + the write path + `--case bad-project` |
| `backend\tests\gate_b32.ps1` | NEW — the gate as a tracked, runnable file. 325 lines, 40 `Assert-That` calls |

No new dependencies. `go build ./...` and `gofmt -l ./frontend/` clean;
`go test ./frontend/internal/protocol/` passes unmodified, confirming the two
new constants are additive.

### The nine steps, in order — and the order is the specification

```
1  resolve_path(raw_path, project)              -> PathDeniedError escapes as Error
2  parent directory exists?                     -> reason "no_parent_dir"
3  encoded size <= editor.max_file_kb?          -> reason "too_large"
4  lock: a DIFFERENT holder, heartbeat < 60s?   -> reason "locked"
5  precondition (stale / exists)                -> reason "stale" | "exists"
6  target exists with read-only attribute set?  -> reason "read_only"
7  write temp in the SAME directory, os.replace -> reason "io_error"
8  touch_heartbeat for this holder              (success only, after the bytes)
9  stat the result, return the new mtime + size
```

Steps 4 and 5 both precede step 7, and **nothing is written until every
precondition has passed** — the corrected guard order 374baa2 established for
`AttachContext`, and the one B24's handoff asks F9b's apply to follow.

### A refusal is a reply, not an exception

The one real design call, built as specified. `resolve_path` raising
`PathDeniedError` and `_require_project` raising `ProjectNotFoundError` both
keep arriving as `Error` envelopes through `Runtime.receive()`'s generic
`RuntimeException` handling, with **no new code** — correctly, because each
means the editor should never have offered the save. But `stale` is a normal
outcome of a correct program: F13 renders three buttons off it, and `locked`
names a holder and an age in the status bar. Neither is expressible as a prose
string inside an `Error`.

There is **no `denied` slug**, for the same reason in reverse: two ways to
report one condition is worse than one. Measured confirmation of the split, from
check 12(a) — stdout carried no `refused`, and the message went to stderr:

```
error: 'C:\...\point-scratch-b32\outside.py' resolves to
C:\...\point-scratch-b32\outside.py, outside project root
C:\...\point-scratch-b32\work
```

`WriteResult.to_dict()` emits only the fields that mean something for the
outcome it reports, so a consumer never has to guess whether a null `holder` on
a `stale` refusal is meaningful. `WriteResult.__post_init__` raises on an
unknown `reason`, so a typo'd slug fails at construction rather than reaching
the wire.

---

## Aryan's five decisions

### 1 — The backend owns the write, and the read moves with it (pre-code)

Settled during Session 18a (F13), which stopped before writing any code at its
own "settle this before writing code" instruction. Six reasons, recorded in the
task doc; the load-bearing two:

- `pathguard.py`'s own docstring already names the consumer — "every editor
  open … There is exactly one implementation on purpose: two would drift, and
  the second one to drift is the one that lets something through."
- `project_idea.md` §12 states editor-write permission as **backend-enforced**
  policy (`editor write — yes | configurable, default read-only`). A Go-side
  write makes that row undeliverable for a B16 guest.

CLAUDE.md's first hard rule bans writes from an **agent** path; a user saving
their own buffer is not that, so both answers were permitted and this one was
chosen on its merits. The structural guarantee is preserved by scope:
`WriteProjectFile` is a runtime event handler like `ProjectInit`, nothing in
`tools\` or `agents\` can reach it, and there is no `write_file` tool. **The
absence of the tool is how the hard rule is enforced — not a check inside one.**

### 2 — The creation path: supported, but never implicit (pre-code)

`expected_mtime: null` *means* "I expect no file here". Without that signal the
only way to create a file would be `overwrite: true`, which would make **"create
a new file" and "clobber someone else's concurrent change" the same request** —
the shape a silent data-loss bug gets written in. Three intentions, three
encodings, none overloading another:

```
expected_mtime: <float>   "exists with this mtime"   -> mismatch = stale
expected_mtime: null      "does not exist"           -> it does   = exists
overwrite: true           "I have already decided"   -> no precondition
```

Parent directories are **not** created (`no_parent_dir`). Verified by test that
no directory appears.

**`AcquireFileLock` does work before the file exists**, which this episode
depended on and pinned rather than assumed. It is an emergent property of three
functions none of which was written with creation in mind: `resolve_path` ends
in `candidate.resolve(strict=False)` (`pathguard.py:68`, `:73`), `_lock_key`
does a pure-string `relative_to(root).as_posix()`, and `get_lock`/`put_lock` are
rows in `file_locks` with no filesystem access. `lock-before-create` is the test
that keeps a later `strict=True` anywhere in that chain from breaking file
creation silently.

### 3 — A successful write refreshes the holder's heartbeat (pre-code)

Yes; after the bytes land, never before; only on success; only for a row this
holder owns. A completed write is better evidence of liveness than a timer tick.
Touching it first would extend a lock for a write that then failed; a stale row
belonging to someone else is not ours to refresh; and a write by a holder with
no lock row must not create one. No new repository method — `touch_heartbeat`
already existed.

Measured both directions: `success-refreshes-own-heartbeat` showed `+30.0s`
against a deliberately backdated row, and eyes-on 2 above confirmed a refused
write left `heartbeat_at` at `1791623521.80366` on both sides.

### 4 — Holder persistence: the WHOLE composed string (mid-episode correction)

**The whole composed string `<username>@<full-id>` is generated once and
persisted in `state.json` as one opaque value, never regenerated on relaunch.**
Not the id alone with the username re-read live and concatenated at each launch.

This corrected an ambiguity the task doc had flagged for Aryan. The reason it
matters is not cosmetic: a username read live is not a stable input. It changes
with a renamed Windows account, a different signed-in user on the same profile,
or anything that alters `USERNAME` — and because comparison is **whole-string
equality**, any of those would silently make Point a *different holder* and
leave it unable to re-acquire its own lock after a restart. Composing once and
persisting the result makes the holder value as stable as the id inside it.

Nothing else changed: the wire format is still `<username>@<full-id>`,
comparison is still whole-string equality, F13 still splits for display on the
**last** `@` (a Windows username may contain one, and may contain spaces).
`file_locks.holder` is `TEXT NOT NULL` (`migrations.py:237`), so **no schema
change, no second field, no `holder_label` column, no change to
`FileLockResult`.**

**The backend does not generate, compose, or validate this value.** It is F13's
to produce and persist; B32 only compares it. Nothing in this episode reads
`USERNAME` or touches `state.json`. The gate uses `aryan@b32-test-id` and
`someone-else@other-id` as opaque strings, and the `locked` refusal returned
`holder: someone-else@other-id` with `age_seconds: 0.4546988010406494`.

### 5 — Close two gate gaps before calling it done (mid-episode)

Done-when #11 and #12 each had two clauses, and in both cases the clause the
gate exercised did not imply the other. Both now have their own assertion, and
the Done-when text was rewritten to say so explicitly.

- **#11(a)** — content *above* `editor.max_file_kb` refused `too_large`, file on
  disk unchanged. The clause that was covered (#11(b): a file *between* the two
  caps opens and saves) is a success path and exercises none of it.
- **#12(b)** — a write carrying an unresolvable `project_id` arrives as an
  `Error`, not a slug. A **different raiser** from #12(a): `_require_project`
  rather than `resolve_path`.

12(b) is necessarily unit-level: the CLI resolves its own project from `--path`
and so cannot send a garbage `project_id` at all.

---

## `$ErrorActionPreference`: Stop → Continue, and the audit that followed

**This was measured, not assumed.** CLAUDE.md's canonical scratch-profile
snippet uses `$ErrorActionPreference = "Stop"`, and B24's gate does too. It is
wrong for this episode, and the failure is total rather than partial.

In Windows PowerShell 5.1 a native command that writes to stderr produces a
`NativeCommandError`, which `Stop` promotes to terminating. The backend logs to
stderr **by design** (CLAUDE.md: "Never log to stdout in Python" — stdout is the
protocol channel), and this gate *deliberately* provokes stderr several times:
every refusal logs, and check 12 expects an `Error` envelope. Probed directly
against this repo — the check-12 call aborted with
`FullyQualifiedErrorId: NativeCommandError` and **not one line after it
executed.** Under `Stop` the gate would have reported nothing and looked like a
crash.

Safety does not come from `Stop` here. It comes from the two env-var overrides
and from never naming the real `Point` folder — unchanged.

**But `Continue` has a cost, and it was paid down rather than accepted.** With
`Continue` for the whole script, an *unrelated* failure — a bad flag, a crash, a
real regression — prints and falls through instead of stopping the run. So every
check was audited for whether it asserted its expected value programmatically or
merely printed an `EXPECT` comment for a human to eyeball. The script now
accumulates failures through one `Assert-That` helper, prints a summary, and
`throw`s at the end so the run's exit code is non-zero.

**What the audit found — the four Aryan named, plus three more:**

| check | before the audit | assertion added |
|---|---|---|
| setup (`migrate`, `project init`) | nothing | exit code, and that init echoes the scratch root. **A failed setup made every later check meaningless and stopped nothing.** |
| the unit script | **its exit code was never read** | exit code. **The worst gap: the script could print `FAIL` for every row and the gate would carry on reporting `PASS` for everything after it.** |
| **3** (read-side metadata) | `EXPECT` comment only | seven field assertions plus "the first content character is not U+FEFF" |
| 4 (stale) | hash comparison only | the `refused stale` slug itself, **and that the reported mtime differs from the one sent** (eyes-on 1, now also asserted) |
| **5** (overwrite) | `EXPECT` comment only | exit code, `ok`, the reported mtime equals a *fresh read's* mtime, and CRLF+BOM survived |
| **6**, 7, 8 (locks) | `EXPECT` + a raw `sqlite3` dump | the `locked` slug, the other holder's name, an age, file byte-identical, **`heartbeat_at` identical before/after** (eyes-on 2, now also asserted), the post-release write succeeding, and `count(*) = 0` |
| **11**(a) | hash comparison only | the `too_large` slug and the reported `size`/`limit_kb` |
| **11**(b) | the band `throw` only | the editor-path read's exit code, `crlf_count 11999`, and `big.py` absent from the tree |
| 12(a) | `EXPECT` comment only | non-zero exit **and** no `refused` on stdout — exactly how an `Error` envelope is told apart from an `ok:false` slug |
| 14 | `EXPECT` + a raw `sqlite3` dump | both `tool.completed … ok]` lines and `ok=1` on both `tool_calls` rows |

Two check placements are forced, not stylistic. A **byte-exact round trip cannot
be driven through a shell pipeline** — capturing stdout and writing it back is
itself a line-ending transformation, so a gate that tried would be testing
PowerShell's redirection rather than this episode. And 12(b) cannot be driven
from the CLI at all, for the reason above.

**The gate is a tracked file**, `backend\tests\gate_b32.ps1`, not a block to be
extracted from markdown on every run. A gate that has to be hand-extracted is a
gate that gets run with somebody's paraphrase of it, and two copies of a 190-line
script drift. The task doc's gate section now holds the reasoning and points at
the file; the per-check reasoning moved into the file's own header comment, so
nothing was lost. Run it with:

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File D:\Projects\Point\backend\tests\gate_b32.ps1
```

---

## The implementation defect: the `read_file` TOOL's cap silently widened to 512KB

**Found by reasoning about the diff after it was written, before the gate run —
and it is the most consequential thing in this episode that was not in its own
spec.**

`editor.max_file_kb` (512) exists precisely because `index.max_file_kb` (100)
must not move: the index prices `@file` attachments against a token budget, and
a 400KB file is reasonable to **edit** and unreasonable to **attach**. The first
implementation pointed `ProjectManager.read_file` at `editor_max_file_kb()`
unconditionally — which is correct for the editor and quietly **widened the
agent's `read_file` tool fivefold**, because the tool calls the same manager
method. The two numbers existed and had collapsed into one anyway, by a route the
task doc had not anticipated.

The fix: `max_kb` is a **parameter** that defaults to `index.max_file_kb`.

```
_handle_read_project_file (the editor)  -> passes editor_max_file_kb()   512
tools\builtins\filesystem.py read_file  -> takes the default             100
```

Defaulting the other way is what caused the defect; the caller that wants the
bigger cap asks for it.

**Regression test: `tool-cap-still-narrow`**, in
`backend\tests\manual_b32_write_roundtrip.py`. It was itself strengthened after
Aryan pushed on whether it asserted what its name claimed — the first version
inherited the size band from an adjacent check via a shared variable and called
the manager method with a default argument rather than the tool. It now goes
through the **tool's own entry point** (`from tools.builtins.filesystem import
_read_file`, invoked with a stub ctx/services pair) and asserts four conjuncts,
each load-bearing:

| conjunct | what it rules out |
|---|---|
| `(index_kb * 1024) < size_bytes < (limit_kb * 1024)`, computed **in this check** | a fixture that drifted outside the band passing vacuously |
| `tool_result.ok is False` | the tool accepting the file at all |
| `"limit 100KB" in tool_result.error` | the refusal coming from the *editor's* 512 cap instead of the tool's 100 |
| `str(size_bytes) in tool_result.error` | the error being about some other file |

Measured: `169780B is in (100KB, 512KB) and the TOOL refused it:
File too large to read: 169780 bytes (limit 100KB)`.

---

## Three gate defects found by running it, not reading it

Each of these would have produced a green gate that tested less than it claimed.

**1 — Check 11(b)'s fixture was too small to test anything.** 6000 rows of
`x$_ = $_` is **81,784 bytes** — *under* `index.max_file_kb`, so "`big.py` is
ABSENT from the tree" was a false expectation and the printed length contradicted
its own comment. The run showed `big.py` present in `list-files`. Now 12000 rows
= **169,786 bytes**, with an assertion that fails if it ever drifts out of the
band. Verified absent from `list-files` (5 files listed, no `big.py`).

**2 — Check 14 never engaged the mock.** Four things had to be right; two were
wrong, and each failed differently:

- `POINT_MOCK=1` only **registers** the mock provider. `session new` otherwise
  picks ollama, so the fixture was never replayed — the run returned a real model
  answer (*"I'm sorry, but I need a specific piece of information or text to read
  for you."*) and **zero tool calls**. Fixed with `--provider mock` on the
  session.
- The default `general` agent has `tools=()` and `max_tool_calls=0`, so the turn
  died `error: [bounds_exceeded] stopped after 0 tool call(s): this agent's limit
  is 0`. Fixed with `--agent research`, the builtin granted
  `read_file`/`list_files`.

**3 — and then the read still failed.** With the mock engaged, the fixture reads
`CLAUDE.md`, which does not exist in a scratch project:
`[tool.completed read_file 0ms failed: Not a file: …\work\CLAUDE.md]`. The gate
now creates a **BOM'd** `CLAUDE.md` inside the scratch project, so check 14
proves both that the tool still works and that the BOM is stripped on the tool
path. Final measured result: `[tool.completed list_files 0ms ok]`,
`[tool.completed read_file 0ms ok]`, and `tool_calls` rows `list_files|1` and
`read_file|1`.

---

## Measured values from the verification run

Everything below is a number or string this episode actually produced, not an
expectation. Eyes-on values are from Aryan's confirming run; the rest are from
the development runs of the same script.

**Byte-exact round trip (Done-when 1).** `crlf_bom.py` — 27 bytes,
`b"\xef\xbb\xbfdef a():\r\n    return 1\r\n"` — SHA-256 identical before and
after a full read → write cycle using only the reported metadata:

```
B7A1E1059BA189B553E3B109B0293BC5874A359EB90B0E69A26D0E8F8E26CC13
```

**Read-side metadata (Done-when 3), the same file:**

```
line_ending:         crlf
crlf_count:          2
lf_count:            0
bom:                 True
trailing_newline:    True
decode_replacements: 0
writable:            True
```

…and `content` begins with `d`, not U+FEFF.

**The line-ending finding that forced the read change**, verified against this
repo's own interpreter:

```
input    b"\xef\xbb\xbfline1\r\nline2\r\n"
output   '\ufeffline1\nline2\n'      # the BOM survived; the CRLF did not
```

**Mixed endings** (`b"a\r\nb\nc\r\n"`) report `line_ending="mixed"` with
`crlf_count=2, lf_count=1`. B32 reports the counts and does **not** decide what
to do about a mixed file — counting bytes is resolution; choosing a
normalisation is a product decision, and that one is F13's.

**`decode_replacements`** on `b"\xff\xfe\x00bad utf8"` → **2** U+FFFD. This
exists because `errors="replace"` is lossy: a non-UTF-8 file round-trips as
U+FFFD and saving it would corrupt it silently — the exact failure this episode
exists to prevent, arriving by the back door. No encoding detection and no
second codec; refusing to write what Point cannot round-trip is the honest small
answer, and F13 opens such a file strictly read-only.

**`too_large` (Done-when 11a)**, both levels:

```
unit (589,825 bytes):   reason=too_large size=589825 limit_kb=512, file unchanged
CLI  (600,001 bytes):   refused too_large / size: 600001 / limit_kb: 512
```

**Between the two caps (Done-when 11b).** `big.py` at **169,786 bytes** —
`crlf_count: 11999` — reads on the editor path (under the old 100KB cap it was
refused outright) and is **absent** from `list-files`. `between_caps.py` at
**166KB** is byte-identical after a save.

**`io_error` (Done-when 13).** A real `os.replace` failure on Windows with a
reader holding the handle, and the temp cleaned up:

```
WARNING point.project: write_file failed for
C:\...\point-scratch-b32\work\held_open.py: [WinError 5] Access is denied:
'C:\...\point-scratch-b32\work\.point-write-e1ehobex.tmp' ->
'C:\...\point-scratch-b32\work\held_open.py'

PASS  failed-replace-leaves-original  (reason=io_error, original intact, temps=[])
```

**Expected stderr, not failures.** That `WARNING` line and check 12(a)'s
`error: … outside project root` line are both correct behaviour. PowerShell 5.1
renders both as red `NativeCommandError` blocks, which looks alarming; the gate
prints a note saying so at the end of every run.

**Regression surface.** `manual_b30_set_session_project.py`,
`manual_b31_validate_project_id.py` and `manual_b27_doctor_resolved.py` all
reported `ALL CHECKS PASSED` against the changed read path; `cli.py doctor` runs
unchanged.

---

## Handoff

- **F13 (Session 18a) is unblocked and its scope is otherwise unchanged.** It
  gains: `line_ending`/`crlf_count`/`lf_count`/`bom`/`trailing_newline` to
  display and preserve, `writable` for autosave rule 1,
  `decode_replacements` to open a non-round-trippable file read-only,
  `WriteProjectFile` for save and autosave, and the `stale`/`locked` slugs its
  two refusal UIs render from. The two Go constants are already in
  `protocol\types.go` with no caller; `envelope_test.go:27` must gain
  `EvProjectFileWritten` when F13 wires it.

- **F13 must pick the mixed-file policy.** B32 reports `crlf_count` and
  `lf_count` and deliberately stops there. A mixed file is the one case that
  cannot round-trip byte-exact — verified: the round-trip property holds for
  CRLF+BOM, LF, no-trailing-newline and BOM-only inputs, and necessarily fails
  for mixed, because any single `line_ending` on the way back rewrites the
  minority endings. Whatever F13 chooses, the status bar must not claim
  otherwise.

- **Four corrections to F13's own task doc, found while settling this episode
  and already in WORKFLOW.md's Session 18a paste:** `runtime.py` is 88,425 bytes
  not "21KB"; no 5,000-line file exists in this repo so Done-when #1 needs a
  generated fixture; **Done-when #7's "second Point instance" is unachievable as
  written** (`lockfile.py` refuses a second backend on the same profile, and two
  profiles have two `runtime.db` files and so two separate `file_locks` tables —
  use `cli.py project lock acquire --holder someone-else@other-id`, which
  deliberately does not take the single-instance lock, as this gate's checks 6–8
  do); and the Editor has no project id, so it must resolve one from the opened
  file's own path via `ProjectResolve`, treating "no `.point\`" as read-only.

- **F9b (18d) and F15 (19) inherit the write. `overwrite: true` is the one field
  in this payload that can destroy someone else's work, and the diff card must
  never send it without the user having pressed `[overwrite]`.** B24's handoff
  obligations are otherwise unchanged: session-scoped guard resolution before
  anything commits, a `(mtime, size)` captured at region generation rather than
  re-derived at apply time, and a card state transition that follows the buffer
  edit.

- **The file tree cannot see files between the two size limits**, and fixing it
  is a real episode, not a passing change. `_build_index` caches on
  `project.id` alone, so threading a per-call `max_kb` through `list_files`
  means the cache key has to include it, or the first caller's limit silently
  decides every later caller's tree for 30 seconds. Whoever picks it up should
  also decide whether the tree and the `@` picker really want the same number —
  they probably do not.

- **`managers\context.py`'s three direct `read_text` calls still turn a BOM into
  a U+FEFF in the prompt.** B32 fixed this only on the
  `ProjectManager.read_file` path, deliberately and boundedly: `:659`/`:720` are
  the editor-context render whose LF-normalised line numbering B24's
  `shown_start`/`shown_end` validation depends on, and which F13's buffer will
  match. **Do not "finish the job" there without re-verifying B24's range
  validation against it.**

- **`decode_replacements` came back 0 on every real file this episode touched.**
  If it is ever non-zero on a repo file, that is a finding about B7's lossy
  `errors="replace"` read — an encoding episode, not a patch.

- **CLAUDE.md's canonical `$ErrorActionPreference = "Stop"` is wrong for any
  gate that exercises a refusal path, and B24's gate carries the same latent
  problem.** Not changed here — B24 is closed and its gate passed — but the next
  episode whose gate provokes backend stderr should start from
  `gate_b32.ps1`'s `Continue` + `Assert-That` shape rather than from CLAUDE.md's
  snippet. The snippet is right about the *env-var overrides*, which are the part
  that protects the real profile; it is the preference line that does not
  survive contact with a backend that logs to stderr by design.
