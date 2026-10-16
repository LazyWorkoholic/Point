# Episode B33 — The protocol channel is UTF-8

**`backend\main.py` read stdin and wrote stdout with no encoding set, so
on Windows both were the ANSI codepage (cp1252 here), not UTF-8. Replies
survived only by accident — `json.dumps` defaults to
`ensure_ascii=True`, so every reply happened to be pure ASCII — while
requests did not, because Go's `encoding/json` writes raw UTF-8. A
payload whose bytes cp1252 *can* map was silently mojibake'd and the
write REPORTED success; one whose bytes it cannot was dropped with no
reply at all. Pinning stdin to `utf-8`/`strict` then moved the failure
rather than fixing it: an undecodable line raised inside the reader
THREAD, which killed it, so every later request was ignored and the EOF
signal after that loop never ran — leaving the process hung with the
single-instance lock held by a live pid. This episode pins both streams
and makes the reader own its decode.**

> Task doc — describes **intent**. The completed doc
> (`design/completed/backend/B33-transport-utf8.md`, once it exists)
> claims reality and completion. Code wins on any disagreement.

**Model:** Sonnet. Two `reconfigure` calls and one loop that reads bytes
instead of text. No new wire event, no reply field, no schema change.
The diagnosis was the work; the change is eleven lines.

**Depends on:** B6 (`main.py` is that episode's transport split — stdin
reader thread, outbox writer, EOF-ends-the-process). Nothing else.

**Found during:** F13b's manual verification, from the outside in.
`design\GIT_STRATEGY.md` and `design\project_idea.md` would not save
from the Editor while `backend\runtime\runtime.py` saved fine, which
looked like a frontend bug in a specific file. It is neither — it is
this, one layer below both, and it had been live since B6. Does not
reopen B6 or B32.

---

## Why this episode exists

### What the two files had in common, and it was not size

Measured, because every plausible explanation was wrong:

| file | bytes | lines | `check-ignore` | tracked | read-only attr |
|---|---|---|---|---|---|
| `design/project_idea.md` | 22,766 | 546 | not ignored | yes | no |
| `design/GIT_STRATEGY.md` | 7,253 | 200 | not ignored | yes | no |
| `backend/runtime/runtime.py` | 90,803 | 1,866 | not ignored | yes | no |

All three inside the resolved project root, all under
`index.max_file_kb` (100) and `editor.max_file_kb` (512), none with the
Windows read-only attribute, none gitignored, all git-tracked. The
failing pair is the SMALLEST of the three.

What they actually have in common is their bytes:

```
GIT_STRATEGY.md    84 non-ASCII bytes, none undefined in cp1252
project_idea.md   283 non-ASCII bytes, INCLUDING 0x8f and 0x90
runtime.py          6 non-ASCII bytes, none undefined
```

`git status` is what gave it away. `design/GIT_STRATEGY.md` showed as
modified by nobody: its only diff was `—` → `â€”` on all 25 em-dash
lines. **A save had landed and corrupted it.** `project_idea.md` was
byte-identical to HEAD, so nothing had written it at all. Two different
symptoms, one cause.

### The asymmetry, and why it hid for so long

- **Python → Go is safe by accident.** `json.dumps(event.to_dict())`
  defaults to `ensure_ascii=True`, so every reply is `\uXXXX`-escaped
  ASCII and the stdout codepage never matters.
- **Go → Python is broken.** Go's `encoding/json` does not escape
  non-ASCII; it emits raw UTF-8. cp1252 then either mis-decodes those
  bytes (mojibake) or has no mapping for them (raises).

So the read path always looked perfect — the file opened, the status bar
showed the right characters — and only the write was wrong. That is why
B32's byte-exactness work, F13a's metadata carrying and both F13 gates
all passed while this was live: **`cli.py` builds a Runtime in its own
process and never crosses the stdin/stdout boundary**, and so does every
`manual_*` test in `backend\tests\`. Nothing in the repo exercised the
transport.

### Measured end to end, through a real pipe

Before:

```
em-dash (U+2014, e2 80 94)
  -> reply ok:True, disk = c3 a2 e2 82 ac e2 80 9d
     SILENT CORRUPTION, reported as success

U+240F (e2 90 8f; 0x90 and 0x8f are undefined in cp1252)
  -> reply NONE, request dropped, process alive
     SILENT FAILURE
```

After pinning stdin only, a line that is invalid UTF-8 in any encoding
(`\xff\xfe`):

```
1. baseline request answered  : True
2. reply to the malformed line: False
3. request AFTER the bad line : False   <- reader thread dead, deaf backend
4. clean shutdown on EOF      : False   <- HUNG FOREVER
```

Line 4 is why this is a correctness fix and not a robustness nicety.
`loop.call_soon_threadsafe(queue.put_nowait, None)` is the **last
statement in the function the exception escaped**, so `read_stdin_loop`
stayed blocked on `await queue.get()` for ever. `main.py`'s own header
explains what that costs: EOF ending the process is what closes the
database and releases the single-instance lock. One bad byte left the
lock held by a live pid, and every later launch would refuse to start.

---

## Build

### 1. Pin both streams

```python
sys.stdin.reconfigure(encoding="utf-8", errors="strict")
sys.stdout.reconfigure(encoding="utf-8", errors="strict")
```

`errors="strict"` on both, deliberately: a decode or encode that cannot
be trusted must raise where it can be logged, not quietly substitute
U+FFFD into a payload that is about to become somebody's file.

**Be precise in the comment about what each line does today**, because
neither is what makes the regression test pass and pretending otherwise
is how a line survives a refactor that breaks it:

- **stdin** is superseded by the reader below, which reads
  `sys.stdin.buffer` and owns the decode. Measured: removing the
  `reconfigure` leaves the new test fully green. It stays so that
  nothing can read this process's stdin as *text* and get the locale
  codepage.
- **stdout** cannot be made to fail today at all, because
  `ensure_ascii=True` means every reply is ASCII. That accident IS the
  hazard: one `ensure_ascii=False` for readability, or one `print` of a
  non-ASCII path, reopens the bug on the other half of the pipe where
  the symptom is just as quiet.

### 2. The reader owns its decode

`_stdin_reader_thread` iterates `sys.stdin.buffer` and decodes each line
itself, so one undecodable line cannot take the reader down with it:

```python
for raw in sys.stdin.buffer:
    try:
        line = raw.decode("utf-8").strip()
    except UnicodeDecodeError as exc:
        _log.warning("Discarded undecodable line from stdin: %s", exc)
        continue
    if line:
        loop.call_soon_threadsafe(queue.put_nowait, line)
loop.call_soon_threadsafe(queue.put_nowait, None)
```

Bytes rather than text because **resuming a text wrapper after a failed
decode is not reliable** — the buffer sits mid-sequence and the
incremental decoder keeps state.

A malformed line is logged and SKIPPED, exactly as `read_stdin_loop`
already treats a malformed JSON line one layer down. There is
deliberately **no error reply**: the bytes did not decode, so there is
no envelope id to correlate one to, and an uncorrelated `Error` is
something the frontend can only log as unroutable. Stderr is the honest
destination — stdout is the protocol channel (CLAUDE.md).

### 3. `backend\tests\manual_transport_encoding.py`

The first test in this repo that **spawns `main.py` with pipes** instead
of importing the runtime. That shape is forced, not stylistic: the
defect lives in the stdin/stdout `TextIOWrapper`, and an in-process call
never reaches it. It sends envelopes with `ensure_ascii=False` so the
bytes on the wire are raw UTF-8, the way Go's encoder produces them.

Three cases, one per failure above: the em-dash round trip, the
cp1252-undefined payload, and the invalid-UTF-8 line — the last
including the clean-shutdown assertion, because the fix for the first
two is what introduced it.

Its stdout is drained on a thread. `readline()` on a live pipe with no
data blocks for ever, so a timeout checked only between lines never
fires; a first version of the harness hung instead of reporting the
failure it was written to catch.

---

## Do NOT

- Add `PYTHONIOENCODING` to a launch script or the spawn environment
  instead. The frontend spawns the backend (`protocol\spawn*.go`) and so
  does `cli.py` and so does a developer running `uv run python main.py`
  by hand; a fix that lives in one caller's environment is absent from
  the other two.
- Use `errors="replace"` on stdin. It is what `describe_bytes` already
  counts and refuses to write (`decode_replacements`, B32's own
  handoff); substituting U+FFFD here would reintroduce exactly the
  corruption B32 exists to prevent, one layer out.
- Emit an `Error` envelope for an undecodable line. There is no id to
  correlate it to.
- Touch `json.dumps`'s `ensure_ascii`. Changing it is now *safe*, which
  is the point, but it is not this episode's work.
- Change `describe_bytes`, `encode_for_write`, or anything else in B32's
  byte layer. It was correct the whole time.

---

## Done when

1. An em-dash payload round-trips **byte-identical** through
   `ReadProjectFile` → `WriteProjectFile` over a real pipe, with no
   `c3 a2` on disk.
2. A payload containing bytes cp1252 cannot map (U+240F) is **not
   dropped** and lands on disk intact.
3. A line that is invalid UTF-8 in any encoding is logged to stderr and
   skipped, and **the backend still answers the next request**.
4. No traceback escapes the reader thread.
5. Closing stdin still shuts the process down cleanly (exit 0), so the
   single-instance lock is released.
6. `manual_transport_encoding.py` fails if either fix is reverted.

---

## Verification gate (PowerShell)

```powershell
cd D:\Projects\Point\backend
uv run python tests\manual_transport_encoding.py
```

**Expect:** `14 passed, 0 failed`. The script creates its own scratch
profile (`APPDATA`/`LOCALAPPDATA` overridden per CLAUDE.md) and its own
fixture under `backend\tests\fixtures\transport\`, and removes both.

It deliberately does **not** set `PYTHONIOENCODING` — the point is what
Point does on its own.

### Revert check

Both fixes must be shown to fail:

```powershell
# the reader fix: expect 4 FAILs, ending in "HAD TO BE KILLED"
# the stdin reconfigure: expect STILL GREEN -- it is superseded by the
#   reader's own decode, which is the honest result and is recorded in
#   main.py's comment rather than hidden
```

---

## Handoff

- **Point corrupted seven of its own tracked files** while this was
  live, in files opened and saved during F13a/F13b manual testing:
  `.gitignore`, `backend\cli.py`, `backend\managers\agent.py`,
  `backend\runtime\runtime.py`, `design\model_usage.md`,
  `frontend\internal\config\config.go`, `config_test.go` and `state.go`.
  Verified for all but `.gitignore` that undoing the mojibake reproduces
  the HEAD blob exactly, so `git checkout --` restores them losing
  nothing. **`backend\cli.py`'s is not a comment**: its corrupted line is
  `if content.startswith("ï»¿")`, a BOM-stripping check in the
  `project write-file` path that no longer matches a real BOM.
  `.gitignore` is corrupted *and* differs from HEAD for another reason,
  so it needs a look rather than a checkout.
- **No `.gitattributes` exists and `core.autocrlf` is `true` from the
  SYSTEM config.** HEAD blobs and the working tree are both LF, so
  commits are correct — but a fresh clone on Windows gets CRLF working
  files while this machine has LF. For a project whose central claim is
  byte-exact line endings, its own repo having no declared policy is
  worth an episode. Not this one's scope; recorded here so it is not
  rediscovered.
- **Nothing in `backend\tests\` exercised the transport before this.**
  Every other `manual_*` script imports the manager or runtime
  directly, which is right for a manager's rules and is exactly the
  blind spot that let this live from B6 to F13b. A future transport
  change should extend `manual_transport_encoding.py`.
