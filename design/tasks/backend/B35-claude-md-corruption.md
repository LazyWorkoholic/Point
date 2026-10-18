# Episode B35 — Two artefacts B20 committed and nobody read

**`CLAUDE.md` — the file every session is told to read first — has been
mojibake'd since `790e0c2` (B20, 2026-09-24). Twenty-four of its lines carry
`â€”` where an em-dash belongs, and it gained a UTF-8 BOM in the same commit,
unlike the three `.ps1` gates whose BOMs are deliberate. This is the cp1252
pipe bug B33 fixed, but a *pre-existing instance of it* that B33's own
"verified afterwards that no mojibake remains anywhere in the tree" check
never looked at: that check covered the WORKING TREE at one moment, never the
committed history. The same commit also added `backend\direct.json` and
`backend\wire.json` — two byte-identical UTF-16LE dumps of a `context.report`
payload, which git stores as BINARY, which nothing in the repo reads, and
which are almost certainly B20's own verification output captured with a
PowerShell redirect and staged by accident. Two unrelated root causes, one
commit, both found by the same repo-wide pass.**

> Task doc — describes **intent**. The completed doc
> (`design/completed/backend/B35-claude-md-corruption.md`, once it exists)
> claims reality and completion. Code wins on any disagreement.

**Model:** Sonnet. No product code changes at all — one documentation file
repaired byte-for-byte, two stray files investigated and then deleted or
ignored. The care is in the method, not the volume.

**Depends on:** nothing in code. B33 is the explanation for finding 1 and is
**not reopened** — its fix is correct, in place, and re-verified green
(14/14) at the time this was scoped. What B33 got wrong was the *scope of its
own after-check*, which is a claim in a completed doc, not a line of code.

**Found during:** a verification pass over B33/F13b against HEAD `fdc6cf7`,
which ran the first repo-wide scan of **committed content** rather than the
working tree — 448 tracked files, each read with `git show HEAD:<path>`.

**Do not fold this into B34.** B34 is about line endings and a
`.gitattributes` policy; its root cause is `core.autocrlf` being a per-machine
setting. Neither finding here has anything to do with that. They are listed
together only because the same scan surfaced them. See "Interaction with B34"
below for the one place they touch.

---

## Why this episode exists

### Finding 1 — `CLAUDE.md`, measured across its own history

Every number below is from reading the committed blob at each revision, not
the working tree.

| commit | date | episode | mojibake lines | correct `—` | BOM | bytes |
|---|---|---|---|---|---|---|
| `6fd020b` | 2026-08-27 | B9 | 0 | — | no | 9,799 |
| `53f2e6d` | 2026-08-27 | B10 | **0** | 23 | no | 11,649 |
| `790e0c2` | 2026-09-24 | **B20** | **85** of 261 | **0** | **yes** | 15,358 |
| `96feddc` | 2026-10-02 | F9a | 23 of 316 | 22 | yes | 18,608 |
| `b270433` | 2026-10-04 | B29 | 23 of 322 | 23 | yes | 19,106 |
| `fdc6cf7` | 2026-10-17 | HEAD | **23** of 322 | 23 | yes | 19,106 |

**Read the B20 row carefully — it is the whole story.** At `790e0c2` the
*entire file* went through the bad pipe: 85 of 261 lines round-trip as
mojibake, every one of the 23 em-dashes was destroyed, and so were all 130
box-drawing characters and all 28 `✅` marks (U+2502 → `â"‚`, U+2705 →
`âœ…`). It was not a partial hit. The file also acquired a BOM at exactly
that moment.

Then F9a **partially repaired it without anyone saying so**: 62 of the 85
lines came back correct, the box-drawing tree and the checkmarks were
rewritten intact, and 23 lines were left behind. B29 changed nothing. Those
23 lines (24 by a literal `â€` match — line 1 differs only because the BOM
sits in front of it) are still there today.

**The surviving corruption is exactly one pattern and nothing else.** A
character census of HEAD's blob:

```
U+2502 │  x170      U+2014 —  x23    (correct em-dashes, coexisting)
U+2500 ─  x62       U+00B7 ·  x11
U+2705 ✅ x41       U+2514 └  x5
U+251C ├  x26       U+2192 →  x2
U+00E2 â  x24  }                     U+FEFF BOM x1
U+20AC € x24   } ← "â€”", the ONLY    U+2026 …  x1
U+201D " x24   }   mojibake run present
```

So the repair is unambiguous: replace the 24 occurrences of the three-character
run `â€”` (U+00E2 U+20AC U+201D) with `—` (U+2014). Nothing else in the file
is damaged. The affected lines at HEAD:

```
1, 10, 28, 32, 48, 49, 62, 71, 79, 97, 103, 105, 127, 132, 133,
141, 148, 149, 150, 158, 168, 173, 174, 175
```

### Why B33's after-check did not catch this

B33's handoff says, of the seven files it restored:

> "All were restored from HEAD before `b5c4dc1`; verified afterwards that
> **no mojibake remains anywhere in the tree**."

That sentence is true of what it measured and false as written. The check
compared the working tree against HEAD — it found files Point had corrupted
*during F13a/F13b's testing*, which is exactly what it was looking for. A
file corrupted **in a commit thirteen episodes earlier** is identical to
HEAD, so a working-tree check is blind to it by construction. The bug class
B33 identified was live from B6; its *victims* were therefore in history, not
only in the working tree, and only history-wide scanning finds them.

**This is the finding with the longest reach, and it must be written into the
completed doc as a method, not an anecdote:** "no mojibake in the tree" and
"no mojibake in the repo" are different claims, and B33 made the second while
checking the first.

### Finding 2 — `backend\direct.json` and `backend\wire.json`

Measured, not assumed:

```
both files            2,900 bytes, byte-IDENTICAL
                      sha256 1DC80E6F480E9600785BA031795703E96694EEC4C5EC01E7ECFA5705ACAAF99C
encoding              UTF-16LE with BOM  (first bytes: ff fe 7b 00 0d 00 0a 00)
git's own view        "Bin 0 -> 2900 bytes" -- git classifies them as BINARY
first commit          790e0c2  (B20)  -- the same commit as finding 1
commits since         0   (never touched again)
referenced in code    NONE -- `git grep` over all tracked content finds no
                      reference to either filename anywhere
gitignored            no
```

The decoded content is a `context.report` payload: `session_id`,
`provider: ollama`, `model: qwen2.5:1.5b-8k`, the `sections`/`counts` token
breakdown, `included[]`, `next_evictions: []`. That is precisely B20's
subject matter, and the two files being **byte-identical** is precisely what
B20's verification set out to prove — that the payload delivered over the
wire matches the one produced directly. So these are B20's own evidence,
captured to disk and then staged along with the real changes.

**UTF-16LE is the tell for how.** PowerShell 5.1's `>` redirect and bare
`Out-File` default to UTF-16LE; nothing in this repo writes UTF-16 on
purpose, and `cli.py` writes UTF-8. So the mechanism is a printed
verification command whose output was redirected to a file in the repo root
rather than to a scratch directory. **That is a different root cause from
finding 1** — it is not an encoding bug in Point, it is a PowerShell default
plus a `git add` that was not reviewed.

No credentials or personal data are in the content — a session UUID and a
local Ollama model name. This is cleanup, not an incident.

---

## Build

### 1. Repair `CLAUDE.md`, by bytes, and prove the repair

Do the substitution at the byte level and assert the result, rather than
opening the file in anything that might re-encode it on the way out.

The operation is: in the UTF-8 bytes of `CLAUDE.md`, replace every
`C3 A2 E2 82 AC E2 80 9D` — the UTF-8 encoding of `â€”` — with `E2 80 94`.
Twenty-four replacements, and the file should shrink by exactly
`24 × (8 − 3) = 120` bytes.

**Assert the count before and after.** A repair that silently replaces 23 or
25 is a different change from the one scoped here, and should stop the work.

**Do not use a regex over decoded text with a default-encoding write.** That
is the same shape as the bug being fixed. Read bytes, write bytes.

### 2. Decide the BOM, explicitly

The BOM is **not** deliberate here and arrived with the corruption, but
removing it is a separate decision from fixing the em-dashes and should be
made on its own terms:

- The three `.ps1` gates' BOMs **are** deliberate — PowerShell 5.1 needs one
  to read a non-ASCII script correctly, and B34's Done-when 6 asserts they
  stay. Nothing here touches those.
- `manual_f12_lifecycle.ps1` also has one, for the same reason.
- `CLAUDE.md` is the **only non-`.ps1` tracked file with a BOM**, and it is
  read by tooling, not by PowerShell.

**Recommendation: strip it**, so the file matches every other Markdown file
in the repo and nothing has to explain why one doc is special. But decide it
and record the decision — do not let it ride along unmentioned inside the
em-dash commit.

### 3. Rescan, history-wide, and make that the standing check

After the repair, re-run a **committed-content** scan over every tracked
file, not a working-tree diff. The two methods that found this should both be
used, because they catch different things:

- the literal signatures `Ã¢â‚¬`, `â€`, `Â`, `Ã©`, `Ã¢`, `ï»¿`;
- the round-trip test: decode the blob as UTF-8, re-encode the line as
  cp1252, decode *that* as UTF-8 — if it succeeds and differs, the line is
  mojibake.

Expect only hits that are **deliberate prose about the bug and must not be
"fixed"**. As scoped, that is these four files:

```
backend/main.py:50                                  the c3 a2 tell, in a comment
design/completed/backend/B33-transport-utf8.md:89, 259
design/tasks/backend/B33-transport-utf8.md:63, 268
design/tasks/backend/B35-claude-md-corruption.md:5, 62, 290   <- this file
```

**This file is itself on that list, and so will the completed doc be.** A doc
that explains a mojibake bug has to quote the mojibake, exactly as B33's two
docs do. Whatever form the check takes, the whitelist is a *list of files that
are allowed to discuss the bug*, not a frozen set of line numbers — line
numbers move the first time either doc is edited, and a check that pins them
will go red for the wrong reason.

Whether this becomes an automated check is this episode's call. B34's section
4 already proposes a pre-commit gate for line endings; if one is built, a
mojibake check belongs beside it. **If it is not automated, say so in the
completed doc** rather than leaving a reader to assume it was.

### 4. Investigate, then remove, the two dumps

Confirm — do not assume — that nothing consumes them:

- `git grep` for both filenames across all tracked content (expect: nothing);
- grep `backend\` for any open/read of a `.json` at the backend root;
- confirm `cli.py doctor` / `context` write to a path the caller gives them,
  never to a fixed file in the repo.

Then **delete both**, since they are one-off evidence from a closed episode
and a `.gitignore` entry would preserve two stale files for no reason. Add a
`.gitignore` entry only if the investigation shows something regenerates them
at those exact paths — which is not expected.

Either way, **note in the completed doc that B20's verification wrote into
the repo root**, so the next episode printing a redirect in a verification
step sends it to `$env:TEMP` instead. That is the reusable lesson; the two
files are not.

### 5. Correct B33's handoff sentence

B33's completed doc states that no mojibake remains anywhere in the tree.
Leave the sentence and **append** the correction with a pointer here, the way
F13b's doc handles its own obsolete F19 reference — a completed doc records
what was true when it was written, and rewriting it hides the lesson. Do not
reopen B33 otherwise.

---

## Interaction with B34

One place only, and it is a correction to B34's *starting point*, not a
reason to merge the episodes:

B34's draft `.gitattributes` carries the comment

> `# No binary entries yet: git ls-files currently lists no binary file.`

**That is wrong at the time of writing.** `direct.json` and `wire.json` are
UTF-16LE and git already classifies both as binary (`git show --stat` reports
`Bin 0 -> 2900 bytes`). They also quietly skew B34's headline measurement:
UTF-16LE encodes CRLF as `0d 00 0a 00`, so `0d` and `0a` are never adjacent
and a byte-pair CRLF scan does not see them — which is part of why B34 reads
"zero tracked blobs contain CRLF".

**If B35 lands first, the problem disappears** and B34's comment becomes true.
If B34 lands first, it must add a binary entry for these two. Sequencing them
B35 → B34 is simpler and is the recommendation. **Do not edit `B34-line-ending-policy.md`
from this episode** — note the dependency and let whoever runs B34 read it.

---

## Do NOT

- **Reopen B33.** Its fix is correct and re-verified (14/14 at HEAD
  `fdc6cf7`). Only the scope of one claim in its completed doc is wrong, and
  section 5 handles that by appending, not rewriting.
- **Fix the four deliberate mojibake references** in `main.py` and the two
  B33 docs. They are documentation of the bug and a scan that flags them is
  working correctly. Any automated check added here must whitelist them by
  path and line, with the reason written next to the whitelist.
- **Repair `CLAUDE.md` by decoding to text and writing with a default
  encoding.** Byte-level in, byte-level out, assert the count.
- **Rewrite the 23 correct em-dashes, the 232 box-drawing characters, the 41
  `✅` marks, or anything else non-ASCII.** Exactly 24 three-character runs
  change. A diff showing more than that is a different change.
- **Touch `B34-line-ending-policy.md`**, or treat either finding as a line-ending
  problem. Separate root causes.
- **Combine the `CLAUDE.md` repair and the two-file deletion in one commit.**
  Two unrelated causes; two commits, each reviewable on its own.
- **Run `git add --renormalize`** — that is B34's operation, and doing it here
  would mix 46 files of line-ending churn into a 24-character fix.
- **Assume the two JSON files are disposable.** Section 4 says confirm first.
  They almost certainly are; the episode still has to show it.

---

## Done when

1. `CLAUDE.md` contains **zero** occurrences of `â€”`, and exactly **47**
   correct em-dashes (the 23 already correct plus the 24 repaired).
2. The repair diff touches **only** those 24 runs — asserted by inspecting
   the diff, not assumed. The file shrinks by exactly 120 bytes, less the BOM
   if it is stripped.
3. The BOM decision is made, applied, and stated in the completed doc with
   its reasoning — including why the three `.ps1` gates keep theirs.
4. A history-wide scan of all tracked files' committed content returns only
   known-deliberate hits — `backend\main.py`, the two B33 docs, and this
   episode's own task and completed docs — and nothing else.
5. `backend\direct.json` and `backend\wire.json` are gone (or ignored, if the
   investigation found a reason), and the completed doc records that nothing
   referenced them and how they got there.
6. The completed doc states **why B33's after-check missed this** — working
   tree versus committed history — in its own section, not as a footnote.
7. `go build ./...`, `go vet ./frontend/...` and `go test ./frontend/...`
   unchanged and green; `backend\tests\manual_transport_encoding.py` still
   14/14. None of them should be affected, which is the point of asserting it.

---

## Verification gate (PowerShell)

Every command below is read-only except where it says otherwise. Nothing here
touches `%APPDATA%\Point` or `%LOCALAPPDATA%\Point`, so no scratch profile is
needed — but note that if any step is ever added that runs Point itself, it
must use CLAUDE.md's env-var-override pattern.

```powershell
# 1. CLAUDE.md is repaired, and ONLY in the way scoped
cd D:\Projects\Point
$b = [IO.File]::ReadAllBytes("CLAUDE.md")
$t = [Text.Encoding]::UTF8.GetString($b)
"bytes      : $($b.Length)"
"BOM        : $($b[0] -eq 0xEF -and $b[1] -eq 0xBB -and $b[2] -eq 0xBF)"
"mojibake   : $(([regex]::Matches($t, [char]0x00E2 + [string][char]0x20AC + [char]0x201D)).Count)"
"em-dashes  : $(([regex]::Matches($t, [string][char]0x2014)).Count)"
```
**Expect:** `mojibake : 0` and `em-dashes : 47`.
**Fails if:** the em-dash count is anything but 47 — fewer means a run was
deleted rather than replaced, more means something else was rewritten.

```powershell
# 2. the repair diff is 24 runs and nothing else
cd D:\Projects\Point
git diff --stat HEAD~1 -- CLAUDE.md
git diff --word-diff=porcelain HEAD~1 -- CLAUDE.md | Select-String '^\+' | Measure-Object
```
**Expect:** one file, and the changed words are em-dashes only.
**Fails if:** any line's prose changed.

```powershell
# 3. the two dumps are gone
cd D:\Projects\Point
"direct.json tracked: $((git ls-files backend/direct.json) -ne $null)"
"wire.json   tracked: $((git ls-files backend/wire.json) -ne $null)"
```
**Expect:** both `False`.

```powershell
# 4. nothing referenced them (run BEFORE deleting, record the output)
cd D:\Projects\Point
git grep -n -I -F -e "direct.json" -e "wire.json" HEAD
```
**Expect:** no hit naming either file. (`B26-behaviour-overrides-doctor-wire.md`
mentions `doctor-wire.json`, a different name in a scratch path — not a
reference to these.)

```powershell
# 5. the transport regression suite is untouched by any of this
cd D:\Projects\Point\backend
uv run python tests\manual_transport_encoding.py
```
**Expect:** `14 passed, 0 failed`.

```powershell
# 6. the Go side is unaffected
cd D:\Projects\Point
gofmt -l . ; go build ./... ; go vet ./frontend/... ; go test -count=1 ./frontend/...
```
**Expect:** gofmt silent, build and vet clean, suite green.

The history-wide scan itself is not a one-liner and should be written as a
small script under `backend\tests\` if it is kept. If it is written, it runs
over `git show HEAD:<path>` for every `git ls-files` entry — **not** over the
working tree, which is the mistake this episode exists to correct.

---

## Handoff

- **The transferable finding is about verification scope, not about
  em-dashes.** B33 fixed a bug that had been live for 27 episodes and then
  checked for victims in the working tree only. Any future episode that fixes
  a long-lived data-corruption bug has to ask the same question B33 did not:
  *where would the victims be, and is that where I am looking?* The completed
  doc should say this plainly, because the next instance of it will not
  involve encodings.
- **`CLAUDE.md` is the highest-leverage file in the repo to get wrong.** It is
  read at the start of every session, and it was quietly corrupt for thirteen
  episodes while being read every time. Whether that is worth an automated
  check of its own — rather than a general mojibake scan — is a judgement for
  this episode.
- **F9a repaired 62 of the 85 corrupted lines without recording that it had
  done so.** Not a criticism of F9a, which was rewriting that section for its
  own reasons and had no way to know — but it is why the corruption survived:
  a partial repair looks exactly like a file that was always fine, unless you
  compare against history.
- **B34 should run after this**, so its "no binary entries yet" comment is
  true when written. See "Interaction with B34".
- **Nothing in Point's product code is implicated by either finding.** B32's
  byte layer, B33's transport, and F13's save path are all correct and stay
  correct. This episode repairs two artefacts in the repo and sharpens one
  claim in a completed doc.
