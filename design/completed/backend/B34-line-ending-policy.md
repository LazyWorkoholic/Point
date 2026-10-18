# Episode B34 — A declared line-ending policy for this repo ✅

**Commit:** `c6a6b70` — "Add line-ending policy for cross-platform consistency (B34)"

**This repo had no `.gitattributes`, and `core.autocrlf` is `true` from the
SYSTEM git config on this machine — never a choice anyone made here. Every
blob was already pure LF, but nothing declared that, so `gofmt -l` could not
be trusted as a gate on a fresh Windows checkout and a fresh clone's working
tree disagreed with this machine's. `.gitattributes` now declares explicit,
per-type `text eol=lf` for every tracked extension, matching the blobs
exactly rather than converting anything.**

> Verified against committed HEAD (`c6a6b70`): zero tracked files are CRLF or
> MIXED in the working tree, `gofmt -l .` is silent, `go build ./...` and
> `go vet ./frontend/...` are clean, `go test ./frontend/...` is green, and
> both `gate_f13a.ps1` and `gate_f13b.ps1` still parse with zero errors.

---

## What this audit found (2026-10-18)

This completed doc did not exist even though `c6a6b70` had already landed
`.gitattributes` — the commit closed the policy decision but nothing
recorded the result. This audit measured the current state fresh rather
than assuming the task doc's original numbers still held:

```
.gitattributes                        present, committed in c6a6b70
core.autocrlf  local=unset  global=unset  system=true  -> effective true
core.eol                              unset

tracked files CRLF in the working tree    0   (was 45 when B34 was scoped)
tracked files MIXED in the working tree   0   (was 1 -- backend/runtime/errors.py)
```

**No renormalize commit was needed.** `git add --renormalize .` was run for
real (not `--dry-run`) against a clean working tree, and produced no staged
changes at all — `git status` and `git diff --cached --stat` both came back
empty. The working tree in this checkout was already byte-identical to the
LF blobs by the time this audit ran; `c6a6b70` itself only added
`.gitattributes` (24 insertions, no other file touched — confirmed via
`git show --stat`), so the 45+1 CRLF/MIXED files the task doc measured on
the machine that scoped this episode were not reintroduced here. The task
doc's own Part 2 instruction is explicit on this: no real changes means no
commit, not an empty one. `backend\runtime\errors.py`, the one file that was
MIXED (91 CRLF / 149 LF-only) when B34 was scoped, is confirmed pure LF now
(`CRLF=0`, 240 LF-terminated lines).

## Verification gate results

| check | result |
|---|---|
| `git ls-files` extensions all named in `.gitattributes` | yes — `.go .py .md .toml .json .mod .sum .ps1 .css .html .gitattributes .gitignore` plus the extension-less `VERSION`, `makefile`, and the dotfile `.python-version`, `uv.lock` are all listed explicitly |
| `git add --renormalize .` content change | none — confirmed via `git status` and `git diff --cached --stat`, both empty |
| tracked files CRLF or MIXED in working tree | 0 |
| `gofmt -l .` | silent |
| `go build ./...` | clean |
| `go vet ./frontend/...` | clean |
| `go test ./frontend/...` | green, all packages `ok` |
| `backend\runtime\errors.py` CRLF count | 0 (was MIXED 91/149) |
| `gate_f13a.ps1` / `gate_f13b.ps1` parse errors | 0 / 0 |

`gofmt -l .` was only run on this machine, not against a literal fresh
clone — there is no second Windows checkout available to this audit to
prove the fresh-clone claim directly. The `eol=lf` attribute is what makes
that claim hold on any machine (it governs checkout, not just comparison),
but it is recorded here as inferred from the attribute rather than as a
second measurement.

## Do NOT (unchanged, confirmed not violated)

- `core.autocrlf` was not touched — still `true` at the system level,
  unchanged, exactly as the task doc required.
- `.gitattributes` uses explicit per-type `text eol=lf`, not a bare
  `* text=auto`.
- No file whose blob was not already LF was normalized — the only files
  renormalize would have touched were the 45+1 already-LF-blob files, and
  in this checkout there was nothing left to touch at all.
- `describe_bytes`, `encode_for_write`, and F13b's C4 tiebreak were not
  touched — this episode is about Point's own repo, not about Point.

## What the next episode inherits

- `.gitattributes` lists itself (`.gitattributes  text eol=lf`), so the file
  that declares the policy is covered by it.
- `gofmt -l .` is now meaningful as a pre-commit gate on every machine —
  nothing in this episode wired it into one; B33's handoff flagged the same
  gap from the other side.
