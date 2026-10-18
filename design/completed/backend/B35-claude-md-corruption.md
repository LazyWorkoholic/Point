# Episode B35 — Two artefacts B20 committed and nobody read ✅

**`CLAUDE.md` carried the same cp1252-pipe mojibake B33 fixed — 24 occurrences
of `â€”` where an em-dash belongs, plus a UTF-8 BOM that was not deliberate —
since `790e0c2` (B20, 2026-09-24), invisible to B33's own after-check because
that check compared the working tree against HEAD and the file was identical
in both. The same commit also added `backend\direct.json` and
`backend\wire.json`, two byte-identical UTF-16LE dumps of a `context.report`
payload that git stores as binary and that nothing in the repo reads —
B20's own verification output, captured with a PowerShell redirect and
staged by accident. Two unrelated root causes, one commit; both repaired
here as two separate changes.**

> Verified against the working tree with all five gate checks, run directly
> by Aryan: `CLAUDE.md` is 18,983 bytes, no BOM, 0 mojibake, 47 em-dashes;
> both `direct.json`/`wire.json` confirmed gone; `git grep` confirms no
> reference to either filename outside doc discussion;
> `backend\tests\manual_transport_encoding.py` is **14 passed, 0 failed**;
> `gofmt -l .`, `go build ./...`, `go vet ./frontend/...` and
> `go test -count=1 ./frontend/...` all clean. No product code changed —
> this episode repairs two artefacts and corrects one claim in a completed
> doc. **Changes are staged, not committed** — this episode's two commits
> (one per root cause, per its own task doc) are Aryan's to make.

---

## Why B33's after-check missed this

This is the episode's actual transferable finding, not a footnote.

B33's handoff states, of the seven files it restored during F13a/F13b
testing: *"verified afterwards that no mojibake remains anywhere in the
tree."* That sentence is true of what it measured and false as a claim about
the repo. The check it ran compared the **working tree against HEAD** — it
asked "did Point corrupt anything while I was testing it just now," which is
exactly the right question for files Point's own save path had just touched.
A file that was corrupted **in a commit thirteen episodes earlier** and never
touched since is, by definition, identical in the working tree and at HEAD.
A working-tree-vs-HEAD diff cannot distinguish "never corrupted" from
"corrupted a long time ago and nobody has saved it since" — both produce no
diff. The check was blind to `CLAUDE.md` by construction, not by oversight
in how carefully it was run.

The bug class B33 fixed (`backend\main.py` reading/writing the ANSI codepage
instead of UTF-8 on Windows) had been live since B6. Its victims were
therefore scattered across **history**, not only in the working tree at the
moment B33 happened to run, and only a scan that reads every tracked file's
committed blob — `git show HEAD:<path>` for every `git ls-files` entry,
compared at multiple revisions where useful — finds the ones that predate
the fix and were never resaved. "No mojibake in the tree" and "no mojibake in
the repo" are different claims, and B33's handoff made the second while
checking the first. Any future episode that fixes a long-lived
data-corruption bug has to ask the question this one didn't: *where would
the victims be, and is that where I'm looking?* For an encoding bug the
answer is "in history, not just in the working tree." For the next instance
of this pattern it will be a different storage layer, but the same question.

`F9a` makes the same point from a different angle: it rewrote the section of
`CLAUDE.md` containing 62 of the 85 originally-corrupted lines for its own
unrelated reasons, and that rewrite happened to repair them — without anyone
recording that it had. A partial, accidental repair looks exactly like a
file that was always fine unless corrupted and clean revisions are actually
compared against each other, which is why the 24 lines F9a's edit didn't
touch survived three more episodes undetected.

---

## `CLAUDE.md` — repaired at the byte level

The corruption was the UTF-8 bytes of an em-dash (`E2 80 94`) having been
decoded as cp1252 and re-encoded as UTF-8 at some point in B20's own save
path, producing the three-character mojibake run `â€”` — bytes
`C3 A2 E2 82 AC E2 80 9D`. A census of HEAD's blob before repair confirmed
this was the *only* damaged pattern: 170 correct `│`, 62 `─`, 41 `✅`, 26
`├`, 23 correct `—` (already repaired by F9a), 11 `·`, 5 `└`, 2 `→`, one `…`
and one BOM, all intact — and exactly 24 occurrences of the bad run,
nothing else non-ASCII touched.

The repair read the file as raw bytes, counted the pattern (asserted **24**
before touching anything — the script aborts rather than proceeding if the
count were anything else), replaced every occurrence of
`C3 A2 E2 82 AC E2 80 9D` with `E2 80 94`, counted again (asserted **0**
afterward), and wrote the result back as raw bytes — no decode-to-text/
re-encode-with-a-default-encoding step anywhere in the path, since that
shape is exactly the bug being fixed. 24 replacements, each turning 8 bytes
into 3, shrinks the file by `24 × 5 = 120` bytes on its own; see below for
the additional 3 from the BOM.

```
before: 19,106 bytes, BOM present, 24 mojibake runs,  23 em-dashes
after : 18,983 bytes, no BOM,       0 mojibake runs,  47 em-dashes
delta : -123 bytes  (120 text + 3 BOM)
```

`git diff --word-diff` on the result confirms the diff touches only those
24 tokens (plus the file's first word, `#`, which absorbed the BOM that used
to sit in front of it as part of the same "word" under word-diff's
tokenization — not a second content change). No prose line changed.

## The BOM decision

The BOM was **stripped**, and that is a decision separate from the em-dash
fix, made on its own terms rather than riding along unmentioned:

- It is **not deliberate**. It arrived in the same B20 commit as the
  corruption — `CLAUDE.md` had no BOM at B10 (`53f2e6d`) and had one at B20
  (`790e0c2`), with no documented reason for either change.
- `CLAUDE.md` was, at the time of this episode, **the only non-`.ps1` tracked
  file carrying a BOM**. The three `.ps1` verification gates keep theirs
  deliberately — PowerShell 5.1 needs one to read a non-ASCII script
  correctly — and `manual_f12_lifecycle.ps1` has one for the same reason.
  None of those four files were touched by this episode.
- `CLAUDE.md` is read by tooling (every session's first read, this one
  included), not by PowerShell, so the reason the `.ps1` files need a BOM
  does not apply to it. Stripping it means `CLAUDE.md` matches every other
  Markdown file in the repo and nothing has to explain why one doc is
  special.

## `backend\direct.json` and `backend\wire.json` — investigated, then deleted

Confirmed before deleting, not assumed:

- **Both files**: 2,900 bytes, byte-identical
  (sha256 `1dc80e6f480e9600785ba031795703e96694eec4c5ec01e7ecfa5705acaaf99c`),
  UTF-16LE with BOM (`ff fe 7b 00 0d 00 0a 00`), git classifies both as
  **binary** (`Bin 0 -> 2900 bytes`), first and only commit `790e0c2` (B20),
  never touched since, not gitignored.
- **`git grep -n -I -F -e "direct.json" -e "wire.json" HEAD`** over all
  tracked content: every hit is prose discussing them — this episode's own
  task doc, `design/WORKFLOW.md`'s session log, `design/tasks/backend/
  INDEX.md`'s episode summary — or an unrelated file
  (`B26-behaviour-overrides-doctor-wire.md`'s `doctor-wire.json`, a
  different name written to a scratch path, not these two). **No hit names
  either file as something opened, read, or written by code.**
- **`backend\cli.py`'s context-report paths**: both the direct path
  (`context.report(...)` then `print(json.dumps(report.to_dict(), ...))`)
  and `--via-wire` (`GetContextReport` then
  `print(json.dumps(report_envelope["payload"], ...))`) only ever
  `print()` to stdout. Neither writes to a fixed path anywhere in `cli.py`.
  `doctor` and `doctor --via-wire` are the same shape (`_print_doctor_report`
  prints; `GetDoctorReport`'s payload is also only printed).

With no code path writing either file and no reference to either filename
anywhere in tracked content outside documentation, the decoded content
confirms the theory: a `context.report` payload — `session_id`,
`provider: ollama`, `model: qwen2.5:1.5b-8k`, the `sections`/`counts`
breakdown, `included[]`, `next_evictions: []` — which is precisely B20's
subject matter, and the two files being byte-identical is precisely what
B20's own verification would have produced comparing the direct path against
`--via-wire`. UTF-16LE is the tell for *how*: PowerShell 5.1's `>` and bare
`Out-File` default to UTF-16LE, nothing in this repo writes UTF-16 on
purpose, and `cli.py` writes UTF-8 throughout. So the mechanism is a printed
verification command whose output was redirected to the repo root instead of
a scratch directory, then staged along with the rest of B20's real changes.
No credentials or personal data are in the content — a session UUID and a
local Ollama model name.

Both files were removed (`git rm`); no `.gitignore` entry was added, since
the investigation found nothing that regenerates them at those paths.

**The reusable lesson, not the two files themselves:** B20's own
verification wrote its redirect output into the repo root instead of
`$env:TEMP`. The next episode that prints a PowerShell redirect in a
verification step should send it to a scratch directory, exactly as
CLAUDE.md's env-var-override pattern already does for `APPDATA`/
`LOCALAPPDATA` — this is the same mistake one layer up, a stray output file
rather than a stray profile directory.

## B33's handoff — corrected, not rewritten

`design/completed/backend/B33-transport-utf8.md`'s "What the next episode
inherits" section states, of the check it ran after restoring seven files
Point had corrupted during its own testing: *"verified afterwards that no
mojibake remains anywhere in the tree."* That sentence is left exactly as
written — a completed doc records what was true when it was written, and
rewriting it hides the lesson, the same precedent F13b's doc set for its own
stale F19 reference. A blockquote was **appended** immediately after it,
pointing here and stating plainly that the claim was true of the check's
scope (working tree vs. HEAD) and false as a claim about the repo. B33 is
**not reopened**: its fix is correct, in place, and re-verified green
(14/14) by this episode's own gate run. Only the scope of that one sentence
in its completed doc was wrong.

## History-wide scan — the standing check, and what it actually finds

Two independent methods were run over every tracked file's **committed**
content (`git show HEAD:<path>` for each `git ls-files` entry, not the
working tree — the method the whole episode exists to use instead of a
working-tree diff):

1. the literal signatures `Ã¢â‚¬`, `â€`, `Â`, `Ã©`, `Ã¢`, `ï»¿`;
2. the round-trip test — decode the blob as UTF-8, re-encode each line as
   cp1252, decode *that* as UTF-8; if it succeeds and differs from the
   original line, the line is mojibake.

Both methods agree: **zero corruption remains** anywhere in tracked history
at HEAD after the repair. Both also surface hits beyond actual corruption —
every one of them deliberate prose discussing or quoting the bug, never a
damaged file:

```
backend/main.py:50                                        the c3 a2 tell, in a comment
design/completed/backend/B33-transport-utf8.md:89, 259     quotes the bug it fixed
design/tasks/backend/B33-transport-utf8.md:63, 268         same, in the task doc
design/tasks/backend/B35-claude-md-corruption.md (several) this episode's own task doc
design/WORKFLOW.md:2534                                     Aryan's own session-log entry for this episode
design/tasks/backend/INDEX.md:56                            this episode's one-line index summary
```

The last two were **not** in this episode's task doc's original whitelist of
four files — they were added to `WORKFLOW.md` and `INDEX.md` after that doc
was written, describing this same episode. Confirmed by reading both in
context: both are prose quoting the bug for documentation purposes, not
corrupted files. `WORKFLOW.md` was not edited to "fix" this — it is Aryan's
own operating manual, never edited except on explicit request, and there is
nothing in it to fix. **Recorded here so a future run of this scan is not
surprised by them**: the task doc's own point stands — the whitelist is "a
list of files allowed to discuss the bug," not a frozen set of paths or line
numbers, and it will keep growing as more docs (including this one) discuss
the bug by name.

This scan was **not** turned into an automated/committed check in this
episode — it was run ad hoc from a scratch script, not added under
`backend\tests\`. Whether it becomes a standing pre-commit gate (B34's
section 4 already proposes one for line endings; a mojibake check would sit
beside it) is left as an open call for whoever picks that up.

---

## Manual Test Log (Episode B35)

Run from `D:\Projects\Point` unless noted, against the repaired working
tree (staged, HEAD still `094ce5b`):

```
CLAUDE.md:   bytes=18983  BOM=False  mojibake=0  em-dashes=47
direct.json tracked: False
wire.json   tracked: False
git grep -n -I -F -e "direct.json" -e "wire.json" HEAD
  -> every hit is doc discussion (WORKFLOW.md, INDEX.md, this episode's
     task doc, B26's unrelated doctor-wire.json); no code reference
```

```
cd D:\Projects\Point\backend
uv run python tests\manual_transport_encoding.py
-> 14 passed, 0 failed
```

```
cd D:\Projects\Point
gofmt -l .                        -> (silent)
go build ./...                    -> (clean)
go vet ./frontend/...             -> (clean)
go test -count=1 ./frontend/...   -> ok, all 9 packages
```

History-wide scan (both methods, over `git show HEAD:<path>` for all 451
tracked files): 0 corruption, all hits deliberate prose (listed above).

---

## What the next episode inherits

- **`CLAUDE.md`'s highest-leverage property — being read at the start of
  every session — is also what let this survive thirteen episodes.** It was
  quietly corrupt and read every time; nothing about reading it ever
  surfaced the corruption, because reading a markdown file through normal
  tooling doesn't re-run a cp1252 round-trip. Whether `CLAUDE.md` deserves
  an automated check of its own, rather than relying on a general repo-wide
  scan being run at all, is still open.
- **The mojibake scan exists only as a scratch script**, not a committed
  test or pre-commit gate. If B34's line-ending gate (its section 4) gets
  built, a mojibake check belongs beside it — this episode deliberately left
  that call to whoever builds it.
- **Sequence B34 after this**, per B35's own task doc: B34's draft
  `.gitattributes` comment ("No binary entries yet") was wrong while
  `direct.json`/`wire.json` existed and git classified them as binary; it is
  true now that they are gone. `B34-line-ending-policy.md` itself was not
  touched by this episode.
- **Nothing in Point's product code is implicated by either finding.**
  B32's byte layer, B33's transport fix, and F13's save path are all
  correct and stay correct. This episode repaired two artefacts in the repo
  and sharpened one claim in a completed doc — no `.py` or `.go` file
  changed.
- **Changes remain staged, not committed**, at the request that created this
  doc. The task doc's own instruction is two separate commits, one per root
  cause (the `CLAUDE.md` byte repair + B33 correction as one; the two JSON
  deletions as the other) — not `git add --renormalize`, which is B34's
  operation and would pull unrelated line-ending churn into either commit.
