# Episode B34 — A declared line-ending policy for this repo

**This repo has no `.gitattributes`, and `core.autocrlf` is `true` from the
SYSTEM git config — the Git-for-Windows installer default, never a choice
anyone made here. So every blob is pure LF (verified: zero tracked blobs
contain CRLF) while 45 tracked files are CRLF in the working tree, and one
— `backend\runtime\errors.py` — is MIXED. Commits stay byte-correct only
because autocrlf's clean filter happens to normalise on the way in. Nothing
declares that, nothing enforces it, and `gofmt -l` cannot be used as a gate
on a fresh Windows checkout because gofmt demands LF while git hands out
CRLF. For a project whose central guarantee is byte-exact line endings
(B32, F13's Done-when 2), its own source tree having no declared policy is
the gap this episode closes.**

> Task doc — describes **intent**. The completed doc
> (`design/completed/backend/B34-line-ending-policy.md`, once it exists)
> claims reality and completion. Code wins on any disagreement.

**Model:** Sonnet. One new file, one `renormalize` pass, and a decision
about what to declare. The decision is the work; the file is twelve lines.

**Depends on:** nothing in code. It must be read **against** B32
(`describe_bytes`/`encode_for_write` — the byte layer this policy has to
agree with) and F13b's C4 (the mixed-file write tiebreak), because a policy
that contradicts either would make the editor and git disagree about the
same file.

**Found during:** the B33 review, while answering why `git` emitted
`LF will be replaced by CRLF the next time Git touches it` on every file in
that commit's diff. That warning turned out to be benign — those files are LF on
both sides and the commit is correct — but the investigation found the
45 files and the MIXED one, which are not. Does not reopen B32, B33 or F13.

---

## Why this episode exists

### Measured state, not assumed

```
.gitattributes                        absent
core.autocrlf  local=unset  global=unset  system=true  -> effective true
core.eol                              unset

tracked blobs containing CRLF         0        (every blob is pure LF)
tracked files CRLF in the working tree against an LF blob   45
  .py        32
  .md         7
  .json       4
  .gitignore  2
tracked files MIXED in the working tree                      1
  backend/runtime/errors.py   CRLF=91  LF-only=149
```

### What is and is not broken

**Not broken: the commits.** Every blob is LF, and `autocrlf=true`'s clean
filter converts CRLF→LF on `git add`, so content committed from a CRLF
working tree lands as LF. both commits are byte-correct.

**Broken: everything that reads the working tree.**

1. **`gofmt -l` is not usable as a gate.** gofmt normalises to LF, so any
   CRLF `.go` file is reported as unformatted. This bit during the B33
   review: three `config` files came back CRLF from a `git checkout` and
   `gofmt -l` flagged them, which read as a code defect and was not one.
   No `.go` file is currently CRLF, but that is luck — the next checkout of
   one reintroduces it.
2. **`backend\runtime\errors.py` is MIXED**, which is precisely the one case
   B32 states cannot round-trip byte-exact: "any single `line_ending` on the
   way back rewrites the minority endings". Open it in Point today and the
   status bar reads `MIXED 91/149`; save it and F13b's C4 tiebreak writes
   the majority — all LF — silently normalising a file nobody asked to
   change. **That is correct behaviour producing a surprising diff**, and
   the surprise exists only because the working tree was allowed into that
   state.
3. **A fresh clone disagrees with this machine.** Anyone cloning on Windows
   gets 45 CRLF files; anyone on Linux/macOS gets LF. Two developers run
   the same `gofmt -l` and get different answers.

### Why a policy rather than turning autocrlf off

`core.autocrlf` is a **per-machine** setting in the system config. Changing
it fixes one machine and is invisible to the next clone, which is the same
failure mode as fixing the UTF-8 transport in one caller's environment
(B33's Do-NOT). `.gitattributes` is committed, so it travels with the repo
and applies to every clone and every tool that honours it.

---

## Build

### 1. Decide what to declare — and it should be LF, explicitly

Three candidate shapes, and the reasoning matters more than the file:

| shape | what it does | verdict |
|---|---|---|
| `* text=auto` | git guesses which files are text; working tree follows `core.autocrlf`/`core.eol` | **no** — still per-machine, still CRLF on Windows |
| `* text=auto eol=lf` | same detection, but the working tree is LF everywhere | plausible |
| explicit per-type `text eol=lf` | named types are LF in the working tree; everything else untouched | **recommended** |

**Recommended: explicit, per-type, `eol=lf`.** Reasons, in order:

- **It matches the blobs.** Every blob is already LF; declaring `eol=lf`
  makes the working tree agree rather than converting anything.
- **It makes `gofmt -l` and `go vet` usable as gates** on every machine,
  which is the concrete thing currently impossible.
- **It does not guess.** `text=auto` applies git's own heuristic to files
  like `uv.lock`, `backend\demo\fixtures\*.json` and the `.ps1` gates;
  naming types keeps a binary misdetection from ever rewriting one.
- **PowerShell is the one honest exception.** CLAUDE.md requires every
  printed command to be PowerShell, and the `.ps1` gates are run by
  `powershell -File`. PowerShell 5.1 reads LF scripts correctly, so `eol=lf`
  is safe — but this is the one entry that should be written down as a
  deliberate decision rather than inherited from a wildcard. **Decide it
  explicitly; do not let `text=auto` decide it.**

A starting point, to be reviewed rather than pasted:

```gitattributes
# Every blob in this repo is LF and must stay LF. The working tree is LF
# too, so gofmt, go vet and the byte-exactness B32/F13 guarantee all see
# the same bytes git stores -- on every machine, regardless of
# core.autocrlf.
*.go    text eol=lf
*.py    text eol=lf
*.md    text eol=lf
*.toml  text eol=lf
*.json  text eol=lf
*.mod   text eol=lf
*.sum   text eol=lf
*.ps1   text eol=lf
.gitignore      text eol=lf
.gitattributes  text eol=lf
.python-version text eol=lf
uv.lock text eol=lf

# No binary entries yet: `git ls-files` currently lists no binary file.
# Add one explicitly the first time a font, image or fixture blob arrives,
# rather than relying on detection.
```

**Check `git ls-files` for unlisted extensions before committing this** —
an extension that exists and is not named keeps the current per-machine
behaviour, which is the bug.

### 2. Renormalise the working tree, once

```powershell
cd D:\Projects\Point
git add --renormalize .
git status          # expect: the 45 + 1 files, content-identical, endings normalised
```

This must be a **separate commit from the `.gitattributes` itself**, or the
policy change and a 46-file whitespace churn land together and neither is
reviewable. Expect `git diff --stat` to show line-ending-only changes and
**no content change** — assert that, do not assume it.

### 3. Fix `backend\runtime\errors.py` deliberately

It is the only MIXED file and the only one where "normalise" is a content
decision rather than a formality. Confirm its blob is pure LF (it is), then
let the renormalise take the working copy to LF, and **state in the
completed doc that the file was mixed and is now not** — so a later reader
does not find a one-line whitespace commit with no explanation.

### 4. Make the gate real

Add `gofmt -l .` and a CRLF check to whatever runs before a commit. The
point of the policy is that these become reliable; if nothing checks them,
the next CRLF file arrives unnoticed.

---

## Do NOT

- Set `core.autocrlf=false` and call it done. It fixes one machine and does
  not travel — the same mistake as fixing an encoding in one caller's
  environment (B33).
- Use a bare `* text=auto`. It leaves the working-tree ending per-machine,
  which is the actual problem.
- Combine the `.gitattributes` commit with the renormalise commit.
- Normalise anything whose blob is not already LF. Nothing currently
  qualifies — verified, zero tracked blobs contain CRLF — so a file that
  *does* appear during this episode is new information and should stop the
  work, not be converted.
- Touch `describe_bytes`, `encode_for_write`, or F13b's C4 tiebreak. This
  episode changes what is on disk in a working tree; it changes nothing
  about how Point reads or writes a user's project files, which is a
  different repo entirely.
- Convert `design\**` prose on the assumption it does not matter. It does:
  `gate_f13a.ps1` and `gate_f13b.ps1` are parsed by PowerShell and must
  stay UTF-8-with-BOM, LF, ASCII-only. Verify both still parse with zero
  errors afterwards.

---

## Done when

1. `.gitattributes` exists, is committed, and names every extension
   `git ls-files` currently reports.
2. `git add --renormalize .` produces **no content change** — asserted by
   inspecting the diff, not assumed.
3. No tracked file is CRLF or MIXED in the working tree:
   the 45 + 1 count above goes to zero.
4. `gofmt -l .` is clean on a **fresh clone**, not just on this machine.
5. `go build ./...`, `go vet ./frontend/...` and `go test ./frontend/...`
   unchanged and green.
6. Both PowerShell gates still parse with zero errors and still report
   BOM present, ASCII-only, zero CRLF.
7. `backend\runtime\errors.py` is pure LF, and the completed doc says it was
   MIXED and why that mattered.

---

## Verification gate (PowerShell)

```powershell
cd D:\Projects\Point
git ls-files | ForEach-Object { [IO.Path]::GetExtension($_) } | Sort-Object -Unique
```
**Expect:** every extension listed appears in `.gitattributes`.

```powershell
cd D:\Projects\Point
git add --renormalize . ; git diff --cached --stat
```
**Expect:** files listed, insertions ≈ deletions, and
`git diff --cached -w --stat` **empty** — whitespace-only.
**Fails if:** any file shows a real content change.

```powershell
cd D:\Projects\Point
gofmt -l . ; go build ./... ; go vet ./frontend/... ; go test ./frontend/...
```
**Expect:** gofmt silent, build and vet clean, suite green.

```powershell
# the one file that was MIXED
cd D:\Projects\Point
$b = [IO.File]::ReadAllBytes("backend\runtime\errors.py")
$crlf = 0; for ($i=0; $i -lt $b.Length-1; $i++) { if ($b[$i] -eq 13 -and $b[$i+1] -eq 10) { $crlf++ } }
"CRLF=$crlf"
```
**Expect:** `CRLF=0`.

Both gates must still parse:

```powershell
foreach ($f in 'gate_f13a.ps1','gate_f13b.ps1') {
    $errs = $null
    $null = [System.Management.Automation.Language.Parser]::ParseFile(
        "D:\Projects\Point\backend\tests\$f", [ref]$null, [ref]$errs)
    "$f errors: $($errs.Count)"
}
```
**Expect:** `0` for both.

---

## Handoff

- **This episode is about Point's own repo, not about Point.** Nothing here
  changes how the editor reads or writes a user's files; B32 owns that and
  is correct. The confusion is worth pre-empting in the completed doc,
  because "line endings" in this project means two unrelated things.
- `.gitattributes` itself should be listed in `.gitattributes`, so the file
  that declares the policy is covered by it.
- Once this lands, `gofmt -l .` becomes meaningful enough to belong in a
  pre-commit check. B33's handoff notes the same thing from the other side:
  until now a CRLF checkout made that gate unusable.
