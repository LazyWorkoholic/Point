# Episode B32 — `WriteProjectFile`: the Editor's Save Path

**Point cannot write a project file. There is no write event in the backend,
and the frontend writes nothing but `state.json` and its two log files — so
F13's save, autosave, lock-refusal and staleness-refusal paths are every one
of them specified against a path that does not exist. This episode builds the
write, in the backend, guarded by the same `resolve_path` every read already
goes through; and it fixes the read, which silently destroys the one fact the
save exists to preserve. Nothing else.**

> Task doc — describes **intent**. The completed doc
> (`design/completed/backend/B32-write-project-file.md`) claims reality and
> completion. Code wins on any disagreement.

**Model:** Sonnet high. One manager method, one event type, one reply type,
one additive change to an existing read, one CLI subcommand — over
`resolve_path`, `check_stale` and the four lock methods, all of which exist
and are tested (B7). Every design call is decided in this doc rather than left
to the session. **Escalate to Opus if** the atomic-replace path turns out to
interact with the lock/heartbeat ordering, or with a file held open by another
Windows process, in ways this doc did not anticipate.

What makes this episode unlike B20/B24/B25/B26 — all the same "plumbing over
something already tested" shape — is that its failure mode is **silent data
loss**, and a diff that reads correctly is not evidence against one. The gate
below is byte-level, and it has to be *run*, not reviewed.

**Depends on:** B7 (`pathguard.resolve_path`, `ProjectManager.read_file`,
`check_stale`, the four lock methods, the `file_locks` table), B6 (the
envelope, the dispatcher, `appconfig.config_value`).

**Consumed by:** F13 Editor Core — which was **stopped before any code**
by this gap when this doc was written, and has since **split in two**:
**F13a** (the editing half, WORKFLOW.md Session 18a) is built and
verified and deliberately ships NO save path, and **F13b** (Session 18b)
is the half that calls this event. F13a carries every field this episode
reports — `line_ending`, `crlf_count`/`lf_count`, `bom`,
`trailing_newline`, `writable` — through unchanged and un-re-derived, so
F13b has the values to write back without returning to the wire. Then
F9b's diff-card apply (18d) and F15's Filler integration (19) — both
land an agent-proposed region in a buffer that the user then saves
through this event.

---

## Why this episode exists

Session 18a (F13) was started against its full spec and stopped at its "settle
this before writing code" instruction, exactly as F9a (15), F10 (16) and F10c
(16d) were. Save ownership was the question it was told to settle. **Three
further findings came out of settling it**, two of them blockers F13 cannot
route around, and all three are recorded here because B32 is the episode that
clears them.

### Finding 1 — there is no write event, confirmed on both sides

`backend\runtime\events.py:78-83` and `:102` give the whole project surface as
`ReadProjectFile`, `AcquireFileLock`, `HeartbeatFileLock`, `ReleaseFileLock`,
`TakeOverFileLock`, `CheckFileStale`, `ListDirectory` — and no write.
`OutgoingEventType` (`:205-207`) has `FileLockResult`, `FileLockReleased` and
`FileStaleResult`, and no counterpart to `ProjectFile`. The Go side already
declares the matching command constants (`protocol\types.go:38-44`); only the
two listing ones have a caller.

Frontend disk writes, all of them: `config\state.go:274` (`state.json`, via
temp + rename) and two log files opened at `cmd\point\main.go:54` and `:102`.
Nothing else across ~110 Go files writes to disk. The only other writes
anywhere are `project init`'s own `.point\project.json` and its `.gitignore`
edit (`project_manager.py:71`, `:177-195`) — note **`.json`**, not `.toml`.

So `CheckFileStale` has existed since B7 as "the check a save-refusal is built
on" (`design/completed/backend/B07-project-system.md:82`) with no save to
refuse.

### Finding 2 — `ReadProjectFile` destroys CRLF, so F13 cannot detect line endings either

Not in Session 18a's brief, and the more consequential of the two blockers.

`ProjectManager.read_file` uses `resolved.read_text(encoding="utf-8",
errors="replace")` (`project_manager.py:269`). `read_text` opens in
**universal-newlines mode**, which collapses `\r\n` to `\n` before the
frontend is handed a single byte. Verified against this repo's own
interpreter, not reasoned about:

```
input    b"\xef\xbb\xbfline1\r\nline2\r\n"
output   '\ufeffline1\nline2\n'      # BOM survives as U+FEFF; CRLF does not
```

The BOM is therefore recoverable from the content the frontend already
receives, and **the line ending is not recoverable at all** — mixed endings
least of all. F13's status-bar `CRLF|LF` cell has nothing to display, and
F13's Done-when #2, which its own doc calls "the check that matters most on
Windows", cannot pass.

This is why "CRLF handling" was **not** separable as a frontend-only parcel to
build ahead of this episode. The frontend has nowhere to learn the answer
from.

### Finding 3 — the size cap makes F13's own Done-when #1 unopenable

`read_file` refuses anything above `index.max_file_kb` (default 100) at
`project_manager.py:263-266`, and `_build_index` skips the same files at
`:248-249`. F13's Done-when #1 is a **5,000-line file**; at ordinary source
density that is 150–200 KB, so such a file is both refused by the read and
invisible in the tree.

For scale: the largest file in this repo is `backend\runtime\runtime.py` at
88,425 bytes / 1,815 lines. There is no 5,000-line file here to test against
either, so F13's own gate will need a generated fixture. (F13's manual
verification also describes `runtime.py` as "21KB" — four times stale. Its
doc needs that number corrected when F13 runs.)

---

## The decision: the backend owns the write, and the read moves with it

Settled with Aryan during Session 18a, before any code was written. CLAUDE.md's
first hard rule bans writes from an **agent** path; a user saving their own
buffer is not that, so both answers were permitted and this one is chosen on
its merits.

1. **`pathguard.py`'s own docstring already names this consumer.** "Every
   `@file` attachment, every tool, **every editor open**, and the SSH jail all
   go through `resolve_path()`. There is exactly one implementation on purpose:
   two would drift, and the second one to drift is the one that lets something
   through." A Go-side write re-implements case-folded Windows containment and
   `..`-collapsing — the two independent steps `_contains`' own comment warns
   are easy to regress.
2. **`project_idea.md` §12 states editor-write permission as backend-enforced
   policy**: `editor write — yes | configurable, default read-only`, under "An
   SSH session is a **restricted** session, enforced in the backend." If Go
   writes the bytes, that row is undeliverable for a B16 guest.
3. **The lock key is backend-derived.** `_lock_key` is
   `resolve_path(...).relative_to(project.root_path).as_posix()`
   (`project_manager.py:378-381`). Go would have to reproduce that mapping to
   know which `file_locks` row its own write corresponds to. This is the
   B12-bug-#2 / B23-`ListDirectory` drift pattern, in the one place where the
   consequence is a write rather than a wrong number.
4. **Autosave rule 2 ("record Point's own write mtimes") needs the post-write
   mtime returned *with* the write.** A Go write followed by a separate
   `CheckFileStale` is two round trips with a stat race between them; one
   reply carrying the new mtime has no race to lose.
5. **A backend write path has to exist regardless.** D29 is already revised:
   the Coding Assistant (v2) "**writes files directly**", gated on a git repo
   and a checkpoint commit (D80). Building the guarded write once, here, is
   not duplicated work.
6. **A cap is only a cap if the backend enforces it.** The Go receive side
   caps a JSON line at 16 MiB (`transport.go:45`); the outbound side has no
   cap at all. A frontend-enforced size limit is a request the backend has no
   reason to trust.

**The read moves with the write.** Backend-read plus Go-write is the worst of
the three options: Go would be writing content it never saw in raw form, and
two places would each hold half of the line-ending knowledge.

**The honest cost:** every autosave ships the whole buffer over JSON lines. At
the cap set below, and at most one write per second, that is acceptable — and
this is claimed **for correctness**, not for performance. Nothing here is
measured, and no performance benefit is being asserted.

---

## Build

### 1. `ProjectManager.write_file()`

Lives beside `read_file` / `check_stale` / the four lock methods in
`managers\project_manager.py`. Not a new module — B7's shape already holds
every guarded filesystem operation, and a `managers\editor.py` would be the
second place that knows how to resolve a project path.

```python
async def write_file(
    self,
    project: Project,                 # REQUIRED, unlike read_file's Optional
    raw_path: str,
    content: str,
    *,
    holder: str,
    expected_mtime: Optional[float],  # None means "I expect no file here"
    overwrite: bool = False,
    line_ending: str = "lf",          # "lf" | "crlf"
    bom: bool = False,
    trailing_newline: bool = True,
) -> WriteResult:
```

**A project is required.** `read_file` takes `Optional[Project]` through
`_resolve_optional_project`; the write takes `_require_project`, exactly as
`AcquireFileLock` and its three siblings already do. This is Aryan's Session
18 decision — *no `.point\` above the file means read-only, not editable
without a lock* — expressed where it is enforceable rather than left as a
frontend convention.

**There is deliberately no `allow_outside_project` flag on the write.** An
out-of-project path cannot take a lock at all: `_lock_key` calls
`resolve_path` with the flag off, so there is no row to check and no holder to
compare against. Accepting the flag here would manufacture precisely the
writable-but-unlockable case the read-only decision rules out. An outside path
raises `PathDeniedError` from `resolve_path`, as it does everywhere else.

**Order of operations, and the order is the specification:**

```
1  resolve_path(raw_path, project)              -> PathDeniedError escapes as Error
2  parent directory exists?                     -> reason "no_parent_dir"
3  encoded size <= editor.max_file_kb?          -> reason "too_large"
4  lock: a DIFFERENT holder, heartbeat < 60s?   -> reason "locked"
5  precondition (see 3 below)                   -> reason "stale" | "exists"
6  target exists with read-only attribute set?  -> reason "read_only"
7  write temp in the SAME directory, os.replace -> reason "io_error"
8  touch_heartbeat for this holder              (see 4 below)
9  stat the result, return the new mtime + size
```

Steps 4 and 5 both precede step 7, and **nothing is written until every
precondition has passed** — the corrected guard order 374baa2 established for
`AttachContext`, and the one B24's handoff asks F9b's apply to follow.

**The write is atomic.** `tempfile.NamedTemporaryFile(dir=<the target's
parent>, delete=False)`, then `os.replace(tmp, target)` — the same
temp-plus-rename shape `config\state.go:274` already uses on the Go side. The
same directory is mandatory, because `os.replace` is only atomic within a
volume. On any `OSError`: delete the temp, return `io_error` with the message.
**Never leave a half-written target, and never leave a temp file behind.** On
Windows, `os.replace` raises `PermissionError` when the target is open by
another process — that is a real case, and it is `io_error`, not a traceback.

**Re-encoding, in this order:** join the content's `\n` as `line_ending`,
apply `trailing_newline`, prepend `codecs.BOM_UTF8` if `bom`, encode UTF-8.
`content` is LF-normalised on the wire in both directions — the frontend's
buffer is LF internally and the backend owns the translation at both ends, so
exactly one place in Point knows about `\r\n`.

### 2. `WriteProjectFile` → `ProjectFileWritten`

`IncomingEventType.WRITE_PROJECT_FILE = "WriteProjectFile"` in the B7 block of
`events.py`; `OutgoingEventType.PROJECT_FILE_WRITTEN = "ProjectFileWritten"`
beside `PROJECT_FILE`. Handler `_handle_write_project_file` next to
`_handle_read_project_file`, registered in the same `dispatcher.register`
block (`runtime.py:309-317`). `cli.py`'s `_REPLY_TYPES` gains
`"WriteProjectFile": {"ProjectFileWritten"}` (`:99`).

**A new reply type, not a reuse of `ProjectFile`.** The two payloads have
almost nothing in common, and B6's one-shape-one-decoder principle — stated in
`CONTEXT_REPORT`'s own comment — argues against overloading a type so that
both decoders have to branch on which keys arrived.

Request payload:

| field | type | meaning |
|---|---|---|
| `path` | str | required |
| `content` | str | required; LF-normalised, no BOM |
| `holder` | str | required; the lock holder key (see below) |
| `project_id` | str | optional, as on every project event |
| `expected_mtime` | float \| null | **null means "I expect no file here"** |
| `overwrite` | bool | default false; skips the precondition entirely |
| `line_ending` | `"lf"` \| `"crlf"` | default `"lf"` |
| `bom` | bool | default false |
| `trailing_newline` | bool | default true |

Success reply:

```json
{"ok": true, "path": "...", "mtime": 1760000000.123, "size": 412,
 "line_ending": "crlf", "bom": true, "trailing_newline": true}
```

It echoes back **what was actually written**, not what was asked for, so the
buffer records the truth rather than its own assumption. That is the whole of
autosave rule 2, and it is why the new mtime rides this reply instead of
needing a follow-up `CheckFileStale`.

Refusal reply — `ok: false` plus exactly one slug:

| `reason` | when | extra fields |
|---|---|---|
| `stale` | file exists, mtime ≠ `expected_mtime`, `overwrite` false | `mtime` (the current one) |
| `exists` | `expected_mtime` was null and the file exists | `mtime` |
| `locked` | a *different* holder holds a lock with `heartbeat_at` under 60s | `holder`, `age_seconds` |
| `read_only` | target exists with the read-only attribute set | — |
| `too_large` | encoded content exceeds `editor.max_file_kb` | `size`, `limit_kb` |
| `no_parent_dir` | the parent directory does not exist | — |
| `io_error` | the write or the replace failed | `message` |

**The one real design call in this episode: a refusal is a reply, not an
`Error`.** `Runtime.receive()` turns any `RuntimeException` into
`{"error": str(exc)}` generically (B7's `errors.py` note), so a path denial,
an unresolvable project and a malformed payload all keep arriving as `Error`
envelopes with no new code — correctly, because each of those means the editor
should never have offered the save in the first place. But **`stale` is a
normal outcome of a correct program**: F13 has to render three specific
buttons off it ("changed on disk since you opened it — [reload] [overwrite]
[diff]"), and `locked` has to name a holder and an age in the status bar.
Neither is possible from a prose string inside an `Error`.

There is no `denied` slug, for the same reason in reverse: `resolve_path`
already raises, and adding a slug would make two ways to report one condition.

### 3. The creation path — decided: supported, but never implicit

*(Addition 1 to this episode's scope, raised by Aryan.)*

**Yes, the write needs a creation path, and `expected_mtime: null` is it.**

`check_stale` returns `True` for a file that does not exist — "including if it
was deleted" (`project_manager.py:363-371`). So a create attempt carrying
*any* float `expected_mtime` reads as "changed on disk" and gets refused. If
creation had no explicit signal of its own, the only way to create a file
would be `overwrite: true` — which would make **"create a new file" and
"clobber a concurrent change by someone else" the same request.** That
conflation is how a silent data-loss bug gets written. So the three intentions
get three distinct encodings and none of them overloads another:

```
expected_mtime: <float>   "this file exists with this mtime"   -> mismatch = stale
expected_mtime: null      "this file does not exist"           -> it does   = exists
overwrite: true           "I have already decided"             -> no precondition
```

**Parent directories are not created.** `no_parent_dir` is a refusal. Writing
a file is one authority; materialising a directory tree is another. Nothing in
F13 asks for it — there is no "new folder" action anywhere in its spec — and
D31's "never creates" boundary for the Filler points the same way. A later
episode that genuinely needs it adds a flag then, with a reason.

**Does `AcquireFileLock` work before the file exists? Yes — verified by
reading the chain, not assumed:**

- `resolve_path` ends in `candidate.resolve(strict=False)` (`pathguard.py:68`,
  `:73`) — existence is not required.
- `_lock_key` then does a pure-string `relative_to(root).as_posix()`
  (`project_manager.py:378-381`) — no filesystem access.
- `get_lock` / `put_lock` are rows in `file_locks` keyed on `(project_id,
  rel_path)` (`migrations.py:234-241`) — no filesystem access.

So a lock on a not-yet-existing path acquires normally. **This must be pinned
by a test in the gate** (Done-when #9), because it is an emergent property of
three functions none of which was written with creation in mind: anyone adding
`strict=True`, or an `exists()` guard, anywhere along that chain would break
file creation with nothing failing nearby to say so.

### 4. Heartbeat on a successful write — decided: yes

*(Addition 2 to this episode's scope, raised by Aryan.)*

**Yes. On success, after the bytes land, and only then.**

- `LOCK_STALE_AFTER_S = 60.0`, and `acquire_lock` treats any lock older than
  that as stealable (`project_manager.py:38`, `:393-396`). A completed write is
  the least ambiguous evidence available that the holder is alive and actively
  using the file — strictly better evidence than a timer tick.
- **After, not before.** Touching the heartbeat first would extend a lock on
  behalf of a write that then failed, which is a lock held by a process that
  did nothing.
- **Only the holder, only on success.** A refused write touches nothing. And a
  write by a holder that holds no lock row must **not** create one — the write
  path never acquires a lock it was not handed. This case is reachable:
  nothing forces the editor to lock before writing, and `acquire` is F13's call
  to make at open.
- No new repository method. `touch_heartbeat(project_id, rel_key, now)`
  already exists, and both `acquire_lock` and `heartbeat_lock` already use it.

The boundary case this makes honest: if the heartbeat timer and a save race,
and the write arrives just after the lock went stale and was stolen, step 4
refuses it as `locked` — rather than writing the bytes and then cheerfully
refreshing a lock that now belongs to someone else.

### 5. Line-ending, BOM and encoding metadata on the read side

Additive keys on `read_file`'s return dict, and so on the `ProjectFile`
payload. `read_file` has exactly two callers — `runtime.py:752` (the wire
handler) and `tools\builtins\filesystem.py:62` (the `read_file` tool) — and
neither reads anything but `content`, `path` and `outside_project`, so new
keys are free.

`read_file` must read **bytes** and decode by hand; `read_text` is what loses
the information in the first place.

| new key | type | meaning |
|---|---|---|
| `line_ending` | `"lf"` \| `"crlf"` \| `"mixed"` | from the counts below |
| `crlf_count` | int | occurrences of `\r\n` |
| `lf_count` | int | occurrences of `\n` **not** preceded by `\r` |
| `bom` | bool | content began with `codecs.BOM_UTF8` |
| `trailing_newline` | bool | the last byte was a newline |
| `decode_replacements` | int | U+FFFD substitutions `errors="replace"` introduced |
| `writable` | bool | the read-only attribute is **not** set |

**The counts ship, and the mixed-file policy does not.** B32 reports
`crlf_count` and `lf_count` and leaves "what do we normalise a mixed file to,
and what does the status bar say about it" to F13. Counting bytes is
resolution; choosing what to do about a mixed file is a product decision. This
is the backend-resolves / frontend-renders line drawn where B20 and B23 drew
it, not one step past it.

**`decode_replacements` exists because `errors="replace"` is lossy.** A
non-UTF-8 file currently round-trips through Point as U+FFFD, and saving it
would corrupt it silently — the exact failure this episode is supposed to
prevent, arriving by the back door. The count is one `str.count` and it lets
F13 open such a file strictly read-only. **No encoding detection and no
second codec**: that is a larger decision than this episode should make, and
refusing to write a file Point cannot round-trip is the honest small answer.

**`writable` reflects the read-only *attribute*, not a full ACL evaluation** —
`os.access(path, os.W_OK)` does not evaluate Windows ACLs. So it is enough for
autosave rule 1 ("never autosave a read-only file") without a probe write, and
an ACL-denied write still surfaces as `io_error` at save time. F13 must handle
both; do not document `writable` as a guarantee.

**One non-additive change, called out deliberately: the BOM is stripped from
`content`.** Today it arrives as a literal U+FEFF inside the string, which
means every attachment of a BOM'd file feeds a zero-width character to the
model, and F13 would have to strip it itself — a second place knowing how to
handle a BOM. Strip it, report it in `bom`, write it back from the flag. Both
affected call sites (`runtime.py:752`, `filesystem.py:62`) are named above and
both are checked in the gate.

**Not changed:** `managers\context.py:605`, `:659` and `:720` read files
directly rather than through `ProjectManager.read_file`, and keep their
current `read_text` behaviour. Leave them alone. `:659`/`:720` are the
editor-context render whose line numbering B24's `shown_start`/`shown_end`
validation is built on, and that numbering is computed over LF-normalised
lines — **the same normalisation F13's buffer will use**, so the two agree
today. Changing the splitting there would break that agreement for no gain in
this episode.

### 6. `editor.max_file_kb`

A new plain config key, behind `appconfig.editor_max_file_kb()`. It governs
**the editor's read, the editor's write, and nothing else.**

**Which read, exactly.** `ProjectManager.read_file` is shared by two callers
with genuinely different needs, so `max_kb` is a **parameter** that defaults to
`index.max_file_kb`:

```
_handle_read_project_file (the editor)  -> passes editor_max_file_kb()   512
tools\builtins\filesystem.py read_file  -> takes the default             100
```

Defaulting the other way would quietly widen the **agent's** read fivefold,
which is the exact thing two separate numbers exist to prevent. The caller that
wants the bigger cap asks for it. The test pins both halves of this
(`tool-cap-still-narrow`), because one number serving both is a one-line
regression with no visible symptom until a 400KB file lands in a prompt.

```toml
[editor]
max_file_kb = 512
```

- **One number for read and write**, or a file you can open is a file you
  cannot save.
- **Separate from `index.max_file_kb`**, which must not move: it prices
  `@file` attachments against a token budget and bounds index build time. A
  400 KB file is reasonable to edit and unreasonable to attach.
- **512 is chosen, not measured.** It clears F13's 5,000-line Done-when with
  headroom. Do not describe it as a performance figure; if it needs to change
  later, it is one default.
- **Not added to `appconfig_overrides.ALLOWED_KEYS`.** That set is the five
  keys the Behaviour tab controls live, and expanding B26's surface is not
  this episode's business.

**Known limitation, accepted and recorded rather than fixed:** `_build_index`
still skips files over `index.max_file_kb`, so a file between the two limits is
openable **by path** but will not appear in F13's file tree. Acceptable for
F13 — its tree check opens `runtime.py` at 88 KB, and its 5,000-line fixture is
opened by path in its own gate. The fix is not trivial and does not belong
here: `_build_index`'s cache is keyed on `project.id` alone
(`project_manager.py:202-207`), so a per-call `max_kb` needs the cache key to
include it. See Handoff.

### 7. `cli.py project write-file`

Every file event has a CLI surface, and the gate below has no frontend.

```
project write-file --file <path> --content-from <file> --holder <h>
                   [--expect-mtime <float> | --create] [--overwrite]
                   [--line-ending crlf|lf] [--bom] [--no-trailing-newline]
                   [--path <project>]
```

**`--content-from <file>`, and deliberately not `--content <string>`.** The
thing under test is byte-exact line endings, and `\r\n` cannot be passed
safely through an argv string or a PowerShell quoted literal. Reading the
content from a file is the only way the CLI can exercise what this episode
builds.

`--create` sends `expected_mtime: null`; `--expect-mtime` sends the float. The
two are mutually exclusive, and **one of them or `--overwrite` is required** —
there is no default precondition, because defaulting to `overwrite` is how you
ship a clobber.

Also: extend `cmd_project_read_file`'s print block (`cli.py:1157-1162`) with
the seven new metadata fields, before the `---` separator.

### 8. The exact Go constants F13 will add

B32 is backend-only and adds no Go code. These names are fixed here so F13
does not invent different ones — the same service B24's doc did for Session
18d:

```go
CmdWriteProjectFile  = "WriteProjectFile"     // protocol/types.go, after CmdReadProjectFile
EvProjectFileWritten = "ProjectFileWritten"   // protocol/types.go, after EvProjectFile
```

`envelope_test.go:27` enumerates the known event types and must gain
`EvProjectFileWritten` when F13 adds it.

---

## The `holder` contract

Aryan's Session 18a decision, as amended by his Decision 1 before this episode
was built. Recorded here because the backend is where the value is compared,
and because F13 is written against it:

- **The whole composed string `<username>@<full-id>` is what is generated
  once and persisted in `state.json`, never regenerated on relaunch.** Not the
  id alone with the username re-read live and concatenated at each launch —
  **the full string is the one opaque holder value.** `file_locks.holder` is
  `TEXT NOT NULL` (`migrations.py:237`), so the backend needs no schema change
  and treats the value as fully opaque, exactly as it already does.
- **Why the whole string and not the id half:** a username read live at launch
  is not a stable input. It changes with a renamed Windows account, a different
  signed-in user on the same profile, or anything that alters
  `os.getlogin()`/`USERNAME` — and because comparison is whole-string equality,
  any of those would silently turn Point into a *different holder* and leave it
  unable to re-acquire its own lock after a restart. Composing once and
  persisting the result makes the holder value as stable as the id it contains.
- **Nothing else changes.** The wire format is still `<username>@<full-id>`;
  comparison is still whole-string equality; F13 still splits for display on
  the **last** `@`, not the first — a Windows username may contain one, and may
  contain spaces, and whole-string equality makes both harmless on the
  comparison side. There is no second field, no `holder_label` column, and no
  change to `FileLockResult`.
- This is also what resolves the ambiguity this section previously flagged.
  "The full id is the comparison key" left the backend with nothing
  human-readable to return, since `FileLockResult.holder` is what F13's "held
  by aryan (12s ago)" renders from. Making the username part *of* the persisted
  key is the answer: the key is self-describing, and the display is a local
  truncation of its id half.
- Stability across a restart is what makes `acquire_lock`'s same-holder branch
  (`project_manager.py:387-389`) re-acquire Point's own lock after a crash
  rather than seeing a stranger's. Distinctness *between instances* comes from
  the single-instance lock plus one `state.json` per profile, not from the id.
- **The backend does not generate, compose, or validate this value.** It is
  F13's to produce and persist; B32 only compares it. Nothing in this episode
  reads `USERNAME` or touches `state.json`.

---

## Do NOT

- **Do not register this as a tool.** `WriteProjectFile` is a runtime event
  handler, like `ProjectInit`. Nothing in `tools\` or `agents\` may reach it.
  CLAUDE.md's first hard rule is enforced structurally — by the absence of a
  tool — and this episode must not be the thing that quietly ends that.
- **Do not add a `WriteFile` tool, a Coding-Assistant write path, or a
  checkpoint commit.** That is D29/D80 and v2.
- **Do not build any frontend.** No buffer, no editor, no tab strip, no save
  key. F13 is Session 18a and it is a separate session.
- **Do not add a filesystem watcher.** F13's spec declines one explicitly; the
  save-time check is v1's answer.
- **Do not change `index.max_file_kb`**, or `_build_index`'s cache key, or
  `managers\context.py`'s three direct `read_text` calls.
- **Do not add encoding detection or a second codec.** `decode_replacements`
  is the whole answer this episode gives to non-UTF-8 files.
- **Do not add `editor.max_file_kb` to the Behaviour tab's `ALLOWED_KEYS`.**
- **Do not create parent directories**, and do not add a delete or rename
  event. One verb.
- **Do not make `overwrite` the default**, and do not let a missing
  precondition mean "write anyway".

---

## Done when

1. A UTF-8 file with **CRLF endings and a BOM** round-trips **byte-identical**
   through `read_file` → `write_file` using only the metadata the read
   reported. SHA-256 before and after are equal.
2. The same holds for an LF file with no BOM, and for a file with **no
   trailing newline**.
3. `ReadProjectFile`'s payload reports `line_ending`, `crlf_count`,
   `lf_count`, `bom`, `trailing_newline`, `decode_replacements` and `writable`,
   and `content` no longer carries a leading U+FEFF.
4. A write with a **stale** `expected_mtime` is refused `ok: false, reason:
   "stale"` and carries the file's **current** mtime. The file on disk is
   unchanged — check the hash, not just the reply.
5. The same write with `overwrite: true` succeeds, and its reply's `mtime`
   matches a fresh `stat` of the file.
6. A write while a **different holder** holds a fresh lock is refused
   `reason: "locked"` with that holder's name and age, and the file is
   unchanged. After the lock ages past 60s the same write succeeds.
7. A **successful** write refreshes the holder's own `heartbeat_at`; a
   **refused** write leaves it untouched. Read the two values out of
   `file_locks`.
8. A write by a holder with **no lock row** does not create one.
9. `AcquireFileLock` succeeds on a path that **does not exist yet**, and
   `write_file` with `expected_mtime: null` then creates it. A second
   `expected_mtime: null` write to the now-existing file is refused
   `reason: "exists"`.
10. A write whose parent directory does not exist is refused `no_parent_dir`,
    and **no directory is created**.
11. **Both clauses, separately exercised.** (a) Content above
    `editor.max_file_kb` is refused `too_large` and the file on disk is
    **unchanged** — check the hash. (b) A file between `index.max_file_kb` and
    `editor.max_file_kb` **opens and saves**. (a) does not follow from (b):
    they are a refusal path and a success path.
12. **Both clauses, separately exercised.** (a) A path outside the project root
    arrives as an **`Error` envelope**, not as a `reason` slug. (b) A write
    carrying an **unresolvable `project_id`** does too. These are two different
    raisers — `resolve_path` and `_require_project` — and (a) does not
    exercise (b).
13. A failed `os.replace` (target held open by another process) returns
    `io_error`, leaves the original intact, and **leaves no temp file** in the
    directory.
14. The `read_file` **tool** (`filesystem.py`) still returns usable content
    after the BOM change — exercise it through an agent turn or
    `send --script`, not by reading the diff.

Checks 1, 2, 9, 10, 13, and 12(b) are **unit-level**, in this episode's own
`backend\tests\manual_b32_write_roundtrip.py`; 11(a) is covered both there and
in the gate. 12(b) has to be unit-level because the CLI resolves its own
project from `--path` and so cannot send a garbage `project_id` at all. That is deliberate and it is
not a shortcut: **a byte-exact round trip cannot be driven through a shell
pipeline**, because capturing stdout and writing it back is itself a
line-ending transformation. A gate that tried would be testing PowerShell's
redirection, not this episode.

---

## Verification gate (PowerShell, scratch profile)

**The gate is a tracked file: `backend\tests\gate_b32.ps1`.** Run it with

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File D:\Projects\Point\backend\tests\gate_b32.ps1
```

It is deliberately **not** embedded here any more. A gate that has to be
extracted from markdown by hand on every run is a gate that gets run with
somebody's paraphrase of it, and two copies of a 190-line script drift. The
file is the script; this section is what the script is FOR. Its own header
comment carries the per-check reasoning, so nothing was lost in the move.

Per CLAUDE.md the script sets `$env:APPDATA` and `$env:LOCALAPPDATA` to a
scratch directory for its own duration, so Point's process never sees the real
`%APPDATA%\Point` / `%LOCALAPPDATA%\Point` path at all, and the real profile is
never named in any call, destructive or otherwise. `$env:POINT_DB` alone is not
enough. (The rename-aside pattern is retired.) Every target file lives inside
the scratch tree; nothing in the gate touches the repo.

### `$ErrorActionPreference` is `Continue`, not `Stop` — and the audit that follows from it

**Measured, not assumed.** In Windows PowerShell 5.1 a native command that
writes to stderr produces a `NativeCommandError`, which `Stop` promotes to
terminating. The backend logs to stderr **by design** (CLAUDE.md: "Never log to
stdout in Python" — stdout is the protocol channel), and this gate
*deliberately* provokes stderr several times: every refusal logs, and check 12
expects an `Error` envelope. Under `Stop` the script aborted at the first one
with `FullyQualifiedErrorId: NativeCommandError`, before a single assertion ran.

Safety does not come from `Stop` here. It comes from the two env-var overrides
and from never naming the real `Point` folder — unchanged.

**But `Continue` has a cost, and it was paid down rather than accepted.** With
`Continue` for the whole script, an *unrelated* failure — a bad flag, a crash, a
real regression — prints and falls through instead of stopping the run. So
**every check was audited for whether it asserted its expected value
programmatically or merely printed an `EXPECT` comment for a human to
eyeball**, and the ones that did not now do. The script accumulates failures
through one `Assert-That` helper, prints a summary, and `throw`s at the end so
the run's exit code is non-zero.

What the audit found — the four named, plus three more:

| check | before the audit | assertion added |
|---|---|---|
| setup (`migrate`, `project init`) | nothing | exit code, and that init echoes the scratch root. **A failed setup made every later check meaningless and stopped nothing.** |
| the unit script | **its exit code was never read** | exit code. **The worst gap: the script could print `FAIL` for every row and the gate would carry on reporting `PASS` for everything after it.** |
| **3** (read-side metadata) | `EXPECT` comment only | seven field assertions (`line_ending`, `crlf_count`, `lf_count`, `bom`, `trailing_newline`, `decode_replacements`, `writable`) plus "the first content character is not U+FEFF" |
| 4 (stale) | hash comparison only | the `refused stale` slug itself, **and that the reported mtime differs from the one sent** (eyes-on 1, now also asserted) |
| **5** (overwrite) | `EXPECT` comment only | exit code, `ok`, the reported mtime equals a *fresh read's* mtime, and CRLF+BOM survived |
| **6**, 7, 8 (locks) | `EXPECT` + a raw `sqlite3` dump | the `locked` slug, the other holder's name, an age, file byte-identical, **`heartbeat_at` identical before/after** (eyes-on 2, now also asserted), the post-release write succeeding, and `count(*) = 0` |
| **11**(a) | hash comparison only | the `too_large` slug and the reported `size`/`limit_kb` |
| **11**(b) | the band `throw` only | the editor-path read's exit code, `crlf_count 11999`, and `big.py` absent from the tree |
| 12(a) | `EXPECT` comment only | non-zero exit **and** no `refused` on stdout — which is exactly how an `Error` envelope is told apart from an `ok:false` slug |
| 14 | `EXPECT` + a raw `sqlite3` dump | both `tool.completed ... ok]` lines and `ok=1` on both `tool_calls` rows |

Checks 1, 2, 9, 10, 13 and 12(b) stay **unit-level**, in
`backend\tests\manual_b32_write_roundtrip.py`; 11(a) is covered both there and
in the gate. Two of those placements are forced, not stylistic: a byte-exact
round trip **cannot** be driven through a shell pipeline, because capturing
stdout and writing it back is itself a line-ending transformation; and 12(b)
cannot be driven from the CLI at all, because the CLI resolves its own project
from `--path` and so cannot send a garbage `project_id`.

**Three things are still worth reading with your own eyes**, even though the
script now asserts all three. The script prints this list at the end of a run:

1. **Check 4's `stale` reply reports the file's CURRENT mtime, not the input.**
   A handler that echoed `expected_mtime` back would pass every other check in
   this gate.
2. **Check 7's `heartbeat_at` is identical before and after the refused locked
   write.** If it grew, the write refreshed a lock it did not own.
3. **The unit block's `failed-replace-leaves-original` prints `temps=[]`.** If
   it says `SKIPPED` instead, this host allowed the replace while a reader held
   the handle, and only the no-temp half actually ran — which is still the half
   that regresses.

**Expected stderr, not failures:** the `WARNING point.project: write_file failed
for ...held_open.py` line (check 13's deliberate `os.replace` failure) and the
`error: ... outside project root` line (check 12(a)'s `Error` envelope).
PowerShell 5.1 renders both as red `NativeCommandError` blocks, which looks
alarming and is correct behaviour.

---


## Handoff

- **F13 (Session 18a) unblocks completely when this lands**, and its scope is
  unchanged otherwise. It gains: `line_ending`/`bom`/`trailing_newline` to
  display and preserve, `writable` for autosave rule 1, `decode_replacements`
  to open a non-round-trippable file read-only, `WriteProjectFile` for save and
  autosave, and the `stale`/`locked` slugs its two refusal UIs render from.
- **Four F13-doc corrections to make when Session 18a runs**, all found while
  settling this episode and none of them product work:
  1. Its manual verification calls `backend\runtime\runtime.py` "21KB"; it is
     88,425 bytes.
  2. Done-when #1's 5,000-line file does not exist in this repo and needs a
     generated fixture.
  3. **Done-when #7's "open the same file from a second Point instance" is
     unachievable as written.** `backend\lockfile.py` refuses a second backend
     against the same profile (`AlreadyRunningError`, naming the pid), and two
     instances on *different* profiles have different `runtime.db` files and so
     different `file_locks` tables — they cannot see each other's locks either.
     The correct second holder is `cli.py`, which deliberately does not take the
     single-instance lock ("the CLI is a second *process*, not a second instance
     of the app"): `project lock acquire --holder someone-else@other-id`. This
     gate's checks 6–8 already use exactly that.
  4. The Editor has no project id and the lock events require one. Aryan's
     decision — resolve from the opened file's own path by walking up for
     `.point\`, and treat "no `.point\` found" as read-only — is F13's to
     implement via `ProjectResolve`; the backend half is already enforced here
     by `write_file` requiring a `Project`.
- **F9b (18d) and F15 (19) inherit the write, and B24's handoff obligations
  are unchanged by it.** The apply path still owes: session-scoped guard
  resolution before anything commits, a `(mtime, size)` captured at region
  generation rather than re-derived at apply time, and a card state transition
  that follows the buffer edit. What changes is only that `overwrite: true`
  now exists, and **the diff card must never send it without the user having
  pressed `[overwrite]`** — it is the one field in this payload that can
  destroy someone else's work.
- **The file tree cannot see files between the two size limits**, and fixing
  it is a real episode, not a passing change. `_build_index` caches on
  `project.id` alone (`project_manager.py:202-207`), so threading a per-call
  `max_kb` through `list_files` means the cache key has to include it, or the
  first caller's limit silently decides every later caller's tree for 30
  seconds. Whoever picks this up should also decide whether the tree and the
  `@` picker genuinely want the same number (they probably do not — a 400 KB
  file is fine to edit and bad to attach).
- **`managers\context.py`'s three direct `read_text` calls still turn a BOM
  into a U+FEFF in the prompt.** B32 fixes this only on the
  `ProjectManager.read_file` path. The inconsistency is deliberate and bounded:
  `:659`/`:720` are the editor-context render whose LF-normalised line
  numbering B24's `shown_start`/`shown_end` validation depends on, and which
  F13's buffer will match. Do not "finish the job" here without re-verifying
  B24's range validation against it.
- **If `decode_replacements` turns out to be non-zero on real files in this
  repo, that is a finding to record, not a bug to fix in this session.** It
  would mean Point has been silently lossy on those files since B7, and the
  answer is an encoding episode, not a patch.
