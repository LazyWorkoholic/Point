# Episode B33 — The protocol channel is UTF-8 ✅

**Commit:** `b5c4dc1` — "B33: the protocol channel is UTF-8"

**`backend\main.py` read stdin and wrote stdout with no encoding set, so on
Windows both were the ANSI codepage (cp1252 on this machine), never UTF-8.
Replies survived only by accident — `json.dumps` defaults to
`ensure_ascii=True`, so every reply happened to be pure ASCII — while
requests did not, because Go's `encoding/json` writes raw UTF-8. A payload
whose bytes cp1252 *can* map was silently mojibake'd and the write REPORTED
`ok: True`; one whose bytes it cannot was dropped with no reply at all.
Pinning stdin to `utf-8`/`strict` then MOVED the failure rather than fixing
it: an undecodable line raised inside the reader THREAD, killing it, so
every later request was ignored and the EOF signal after that loop never
ran — leaving the process hung with the single-instance lock held by a live
pid, which would make every later launch refuse to start. Both streams are
now pinned and the reader owns its decode, reading `sys.stdin.buffer` and
logging-and-skipping a line it cannot decode. Live since B6; neither B6 nor
B32 caused it and neither is reopened.**

> Verified fresh against committed HEAD (`6e28f6b`, with this episode at
> `b5c4dc1`): `backend\tests\manual_transport_encoding.py` reports
> **14 passed, 0 failed**, in a scratch `APPDATA`/`LOCALAPPDATA` profile per
> CLAUDE.md's env-var-override rule, with its own fixture under
> `backend\tests\fixtures\transport\` and both removed afterwards. It
> deliberately does **not** set `PYTHONIOENCODING` — the point is what Point
> does on its own. `go build ./...`, `gofmt -l .` and `go vet
> ./frontend/...` are clean and `go test ./frontend/...` is green
> (**1,251 tests, 0 failed**) — this episode touches no Go file, and that is
> the evidence for it.
>
> **Both fixes were shown to fail when reverted**, one of them with a result
> worth recording rather than hiding: reverting the reader's own decode goes
> red on all four invalid-UTF-8 checks, ending in `HAD TO BE KILLED`.
> Reverting the stdin `reconfigure` leaves the suite **fully green**,
> because the reader's explicit decode supersedes it. That is written into
> `main.py`'s own comment rather than left for someone to discover.

---

## Why this was invisible for twenty-seven episodes

The asymmetry is the whole answer, and it is worth stating precisely
because it explains why B32's byte-exactness work, F13a's metadata
carrying, and *both* F13 gates all passed while this was live:

- **Python → Go was safe by accident.** `json.dumps(event.to_dict())`
  defaults to `ensure_ascii=True`, so every reply is `\uXXXX`-escaped ASCII
  and the stdout codepage never mattered.
- **Go → Python was broken.** Go's `encoding/json` does not escape
  non-ASCII; it emits raw UTF-8, which cp1252 either mis-decodes or cannot
  decode.

So the read path always looked perfect — the file opened, the status bar
showed the right characters — and only the write was wrong.

And **nothing in the repo exercised the transport**. `cli.py` builds a
`Runtime` in its own process, and so does every `manual_*` script in
`backend\tests\`. Neither crosses the stdin/stdout `TextIOWrapper`. That
blind spot is the structural reason this survived from B6 to F13b, and
closing it is half of this episode.

### What the two failing files had in common, and it was not size

F13b's manual testing reported that `design\GIT_STRATEGY.md` and
`design\project_idea.md` would not save while `backend\runtime\runtime.py`
saved fine — which looked like a frontend bug in two specific files. Every
plausible explanation was measured and wrong:

| file | bytes | lines | `check-ignore` | tracked | read-only attr |
|---|---|---|---|---|---|
| `design/project_idea.md` | 22,766 | 546 | not ignored | yes | no |
| `design/GIT_STRATEGY.md` | 7,253 | 200 | not ignored | yes | no |
| `backend/runtime/runtime.py` | 90,803 | 1,866 | not ignored | yes | no |

All three inside the resolved project root, all under `index.max_file_kb`
(100) and `editor.max_file_kb` (512), none read-only, none gitignored, all
tracked. **The failing pair is the smallest of the three.**

What they actually had in common was their bytes:

```
GIT_STRATEGY.md    84 non-ASCII bytes, none undefined in cp1252
project_idea.md   283 non-ASCII bytes, INCLUDING 0x8f and 0x90
runtime.py          6 non-ASCII bytes, none undefined
```

`git status` is what gave it away. `design\GIT_STRATEGY.md` showed as
modified by nobody: its only diff was `—` → `â€”` on all 25 em-dash lines.
**A save had landed and corrupted it.** `project_idea.md` was byte-identical
to HEAD, so nothing had written it at all. Two different symptoms, one
cause. `runtime.py` "saved fine" because six characters of mojibake go
unnoticed.

---

## `backend\main.py` — both streams pinned

```python
sys.stdin.reconfigure(encoding="utf-8", errors="strict")
sys.stdout.reconfigure(encoding="utf-8", errors="strict")
```

`errors="strict"` on both, deliberately: a decode or encode that cannot be
trusted must raise where it can be logged, not quietly substitute U+FFFD
into a payload that is about to become somebody's file. `errors="replace"`
would reintroduce exactly what B32's `decode_replacements` exists to refuse,
one layer out.

**The comment in the file is precise about what each line does today**,
because neither is what makes the regression test pass and pretending
otherwise is how a line survives a refactor that breaks it:

- **stdin** is superseded by the reader below, which owns the decode.
  Measured: removing this `reconfigure` leaves the test fully green. It
  stays so nothing can read this process's stdin as *text* and get the
  locale codepage.
- **stdout** cannot be made to fail today at all, because
  `ensure_ascii=True` means every reply is ASCII. **That accident is the
  hazard**: one `ensure_ascii=False` for readability, or one `print` of a
  non-ASCII path, reopens the bug on the other half of the pipe where the
  symptom is just as quiet. This line is why such a change would be safe
  instead of a regression — a line of code no test can currently turn red,
  kept on purpose and labelled as such.

## `backend\main.py` — `_stdin_reader_thread` owns its decode

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
decode is not reliable** — the buffer sits mid-sequence and the incremental
decoder keeps state.

A malformed line is logged and SKIPPED, exactly as `read_stdin_loop` already
treats a malformed JSON line one layer down. There is deliberately **no
error reply**: the bytes did not decode, so there is no envelope id to
correlate one to, and an uncorrelated `Error` is something the frontend can
only log as unroutable. Stderr is the honest destination — stdout is the
protocol channel.

### Why the thread dying was worse than a crash

Measured against this backend by feeding it one line containing `\xff\xfe`,
which is invalid UTF-8 in any position:

```
1. baseline request answered  : True
2. reply to the malformed line: False
3. request AFTER the bad line : False   <- the reader thread was dead
4. clean shutdown on EOF      : False   <- HUNG FOREVER
```

Line 3 is a **deaf backend**: the thread had exited, so nothing was reading
stdin any more and every later request was ignored in silence. Line 4 is
why this is a correctness fix rather than a robustness nicety —
`loop.call_soon_threadsafe(queue.put_nowait, None)` is the **last statement
in the function the exception escaped**, so `read_stdin_loop` stayed blocked
on `await queue.get()` for ever. `main.py`'s own header explains the cost:
EOF ending the process is what closes the database and releases the
single-instance lock. One bad byte left the lock held by a live pid.

After the fix, the same input gives `3. True` and `4. True (exit 0)`, with
`WARNING point.main: Discarded undecodable line from stdin` on stderr and no
traceback.

## `backend\tests\manual_transport_encoding.py` (NEW)

The first test in this repo that **spawns `main.py` with pipes** instead of
importing the runtime. That shape is forced, not stylistic — see "Why this
was invisible" above.

It sends envelopes with `ensure_ascii=False` so the bytes on the wire are
raw UTF-8, the way Go's encoder produces them. Writing escaped ASCII would
test nothing.

Its stdout is drained on a thread. `readline()` on a live pipe with no data
blocks for ever, so a timeout checked only between lines never fires; **a
first version of the harness hung instead of reporting the failure it was
written to catch**, which is recorded in the file.

---

## Manual Test Log (Episode B33)

Run from inside `backend\`, against committed HEAD:

```powershell
cd D:\Projects\Point\backend
uv run python tests\manual_transport_encoding.py
```

```
PASS  the harness can talk to main.py at all
PASS  the repo resolves as a project
PASS  ReadProjectFile answers
PASS  an em-dash survives Python -> Go  (content='# heading \u2014 subtitle\n')
PASS  WriteProjectFile answers an em-dash payload
PASS  ...and reports ok
PASS  an em-dash file is BYTE-IDENTICAL after read -> write  (b'# heading \xe2\x80\x94 subtitle\n')
PASS  no mojibake on disk (c3 a2 is the cp1252 tell)
PASS  a payload with cp1252-UNDEFINED bytes is not dropped
PASS  ...and lands on disk intact
PASS  the backend STILL ANSWERS after an invalid-UTF-8 line
PASS  ...and says so on stderr (never stdout -- that is the protocol)
PASS  ...with no traceback escaping the reader thread
PASS  closing stdin still shuts the process down cleanly  (exit=0)

14 passed, 0 failed
```

Before/after, through the same harness:

```
em-dash (U+2014, e2 80 94)
  before -> reply ok:True, disk = c3 a2 e2 82 ac e2 80 9d   SILENT CORRUPTION
  after  -> byte-identical, no c3 a2 anywhere

U+240F (e2 90 8f; 0x90 and 0x8f undefined in cp1252)
  before -> reply NONE, request dropped, process alive      SILENT FAILURE
  after  -> reply ok:True, content intact on disk
```

---

## Bugs caught during this episode

- **The first fix introduced the second bug.** Pinning stdin to
  `utf-8`/`strict` turned two silent data problems into a process that went
  deaf and then hung. Only running an invalid-UTF-8 line — rather than a
  merely cp1252-undefined one — exposed it, which is why that case is its
  own permanent check.
- **The first harness hung instead of failing.** `readline()` on a live pipe
  blocks, so the timeout never fired. The shipped harness drains stdout on a
  thread, and says why.
- **A diagnostic script of this episode's own hit the same bug** while
  printing `✅` to a cp1252 stdout. Noted because it is the shortest
  possible demonstration that the default is the hazard, not the payload.

---

## What the next episode inherits

- **Point corrupted seven of its own tracked files while this was live**,
  in files opened and saved during F13a/F13b manual testing: `.gitignore`,
  `backend\cli.py`, `backend\managers\agent.py`, `backend\runtime\runtime.py`,
  `design\model_usage.md`, and `frontend\internal\config\{config,config_test,state}.go`.
  All were restored from HEAD before `b5c4dc1`; verified afterwards that
  **no mojibake remains anywhere in the tree**. `backend\cli.py`'s was not a
  comment — its corrupted line was `if content.startswith("ï»¿")`, a
  BOM-stripping check in the `project write-file` path that no longer
  matched a real BOM.

  > **Correction (B35):** "no mojibake remains anywhere in the tree" was true
  > of what this check measured — the working tree against HEAD — and false
  > as a claim about the repo. `CLAUDE.md` had been mojibake'd since `790e0c2`
  > (B20, thirteen episodes before this one) and was identical in the working
  > tree and at HEAD, so a working-tree check could not see it by
  > construction; only a scan of committed history across revisions found it.
  > This sentence is left as written — it records what was true of this
  > check's own scope at the time — see
  > `design/completed/backend/B35-claude-md-corruption.md` for the fix and
  > for why "no mojibake in the tree" and "no mojibake in the repo" are
  > different claims.
- **No `.gitattributes` exists and `core.autocrlf` is `true` from the SYSTEM
  config**, so 49 tracked files round-trip CRLF↔LF only by accident of that
  setting, and `gofmt -l` can never be a reliable gate on a fresh Windows
  checkout. **Scoped as its own episode — see
  `design/tasks/backend/B34-line-ending-policy.md`** — rather than left
  buried here.
- **`backend\tests\` now has one transport-level test and should keep
  exactly one.** Every other `manual_*` script imports the manager or
  runtime directly, which is right for a manager's own rules and is exactly
  the blind spot that let this live from B6 to F13b. A future transport
  change extends `manual_transport_encoding.py`.
- **`json.dumps`'s `ensure_ascii` is now safe to change.** It was load-bearing
  by accident before this episode and is not any more. Changing it is still
  nobody's current work.
