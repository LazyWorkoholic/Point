# Git Strategy — Branching and Versioning

**This file is for you, not for Claude Code.** It may execute a branch, tag, or
merge command when you explicitly ask — it should never decide to create,
merge, or delete one on its own initiative.

---

## The one-paragraph version

**Commit straight to `main` after every episode, same as you already do.**
Branch only for the handful of episodes that might get *cut* before they're
merged, and for the future C++ track, which is a parallel universe that
shouldn't touch this one. Tag a version number only at four milestones your
own docs already name — nowhere else.

That's the whole system. Everything below is the reasoning and the exact commands.

---

## Why not full Gitflow

Gitflow (`develop`, per-feature branches, release branches, hotfix branches)
solves a problem you don't have: **multiple people, or multiple things in
flight at once, that need isolating from a stable line.** You're one person
doing one episode at a time, and every commit you make has already passed a
manual verification gate before it lands. The gate *is* the code review.
Branching every episode would add merge overhead for a risk that doesn't exist
here — there's nothing concurrent to isolate `main` from.

The two places branching earns its cost are named below. Everywhere else,
branching would be exactly the complexity you asked to avoid.

---

## Two numbers that are NOT the same thing

Easy to conflate, so stated plainly:

```
VERSION (repo root)        product/release version — what you bump per this doc
envelope "v" (B6's wire    protocol version — bumped only when the Go↔Python
protocol, currently 1)     wire format itself changes, which should be rare
                          after F2 lands
```

Shipping v0.4.0 of Point does not mean the envelope becomes `v: 4`. They move
on separate, unrelated schedules. Don't touch one when you meant the other.

---

## Versioning scheme

Stay in `0.x.y` for the entire build — SemVer's own convention is that `0.x.y`
means "anything can still change," which is exactly true right now. Bump
**MINOR** only at milestones your docs already name; don't bump per episode.

| Version | Milestone | Where it's already defined |
|---|---|---|
| `0.1.0` | current — design complete, build started | — |
| `0.2.0` | v1 backend complete | B12's own doc: *"this is the end of the minimum viable backend"* |
| `0.9.0` | v1 feature-complete | F15's own doc: *"v1 feature-complete"* |
| `1.0.0` | v1 real | `WORKFLOW.md`'s demo script runs end-to-end, no retake |
| `1.x.0` | each stretch episode that lands (B13, B14, B15, B16, F16, F17) | one MINOR bump per merged stretch feature — normal SemVer meaning of MINOR after 1.0 |
| `2.0.0` | Track 2 (Go+C++) reaches its own v1 | separate milestone, separate track |

Nothing invented here — every number above just labels a point your own docs
already call out as significant. If you want intermediate PATCH bumps between
these (`0.1.1`, `0.1.2`...) for your own bookkeeping, that's fine and optional;
they carry no meaning Claude Code or the docs rely on.

### How to bump and tag

```powershell
cd D:\Projects\Point
"0.2.0" | Set-Content VERSION -Encoding ascii -NoNewline
git add VERSION
git commit -m "version: 0.2.0 — v1 backend complete (post-B12)"
git tag -a v0.2.0 -m "v1 backend complete"
```

Do this yourself, by hand, at the four MINOR milestones above. It's a 30-second
manual act, not something to script or delegate — you're the one who decides
the project actually reached that point.

---

## Commits — unchanged, keep doing exactly this

You're already doing it right:

```
git commit -m "B7: project system"
git commit -m "session 0: doc repair after reality check"
```

One commit per episode, episode id first in the message. That's the real unit
of history in this project — versions are coarser labels on top of it, not a
replacement for it.

---

## Branching — exactly two cases, nothing else

### Case 1 — an episode that might get cut

Your own docs already mark several episodes as optional or droppable at the
checkpoint (B13's memory system, B16 SSH, F14 editor terminal, F17 web
preview). If one of these doesn't pan out, or you run out of time before
merging it, `main` should never have carried the half-finished attempt.

```powershell
git checkout -b episode/b16-ssh main
# ... run the whole session on this branch, commit as usual ...
git checkout main
git merge episode/b16-ssh          # only if you're keeping it
git branch -d episode/b16-ssh
```

If you decide to drop it instead:

```powershell
git checkout main
git branch -D episode/b16-ssh      # deleted, never touched main
```

`main` stays demo-ready at every point in time either way.

### Case 2 — the Track 2 (Go + C++) rewrite

This is a genuinely separate implementation of the same idea, built after v1
is done. It shouldn't share commits with the Go+Python line at all — cutting
it as a long-lived branch (or its own repo, if you'd rather) keeps the two
tracks from ever tangling.

```powershell
git checkout -b track-2-cpp main
```

Work there for the entire duration of Track 2. Never merge it back into
`main` — it's a different book, a different implementation, not a feature.

### Everything else stays on `main`

Every normal `[P1]` episode — B6 through F15, in order — commits straight to
`main`, exactly as you've already been doing since Session 0. No branch, no
merge, no extra step.

---

## Recovering from a bad commit

If an episode's commit turns out to be wrong after the fact (caught later,
not at its own gate):

```powershell
git revert <sha>
```

That's the whole recovery story at this stage. You don't need a hotfix-branch
workflow for a pre-1.0 solo project — a revert commit is simpler, and the
episode-per-commit history means you always know exactly which commit to
target.

---

## Optional: a CHANGELOG.md, nearly free

If you want one for the SRS/book later, this costs almost nothing because the
content already exists in `design/completed/**`:

```markdown
# Changelog

## 0.2.0 — v1 backend complete
B6 (schema v2, envelope v1, bootstrap), B7 (project system), B8 (context
builder), B9 (agent foundation), B10 (tool system), B11 (three agents),
B12 (routing, attachments, caps). See design/completed/backend/ for detail.

## 0.1.0 — design complete
All 46 design docs, decisions.md through D96, backend episodes B1–B5
verified against reality.
```

Write one entry **only at each MINOR bump**, not per episode — it's a summary
pointing at `completed/`, not a duplicate of it.

---

## What NOT to do

- Don't create a branch per episode. The verification gate is the review;
  branching adds merge steps for a risk that isn't there.
- Don't adopt `develop` as a permanent second branch. There's no team to
  protect `main` from.
- Don't bump `VERSION` per commit or per episode. Bump it at the four named
  milestones only.
- Don't confuse `VERSION` with the envelope's `v` field. They move independently.
- Don't build a hotfix-branch process this early. `git revert` is enough
  before 1.0.