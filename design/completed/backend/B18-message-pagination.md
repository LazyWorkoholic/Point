# Episode 18 — Message Pagination (keyset scrollback) ✅

**A monotonic `seq` per message, a `(session_id, seq)` index that makes "the
last N messages of this session" an index seek instead of a full-session
sort, and one keyset query — `SessionMessages(session_id, before|after,
limit)` — that F18 pages against. `GetSession` keeps its old unbounded
behaviour untouched when `message_limit` is omitted, so F3 keeps working
while F18 is unbuilt.**

> Verified by Aryan on his own machine, fresh scratch profile, against
> `tasks/backend/B18-message-pagination.md`'s seven "Done when" criteria —
> including the specific row (`seq=9970`) his own manual `sqlite3` cross-check
> had flagged as suspect during verification. Confirmed present, correctly
> sequenced, and correctly returned by `session messages`. All gate checks
> pass; the full 1–10000 traversal reports no gaps.
>
> **No ambiguity in the episode doc required stopping before writing code.**
> Three things that looked like they might be repo-fact mismatches were
> checked and turned out fine, so the build proceeded: `SCHEMA_VERSION` was
> confirmed at 5 (B12's v5) before this episode touched it; `SessionManager
> .append_message` → `SessionRepository.insert_message` was confirmed as the
> **only** path anything persists a message through, so `seq` assignment
> belongs entirely in `insert_message`; and the installed SQLite (3.53.1 via
> Python, 3.51.1 via the `sqlite3` CLI) was confirmed to support the window
> function, `RETURNING`, and `UPDATE…FROM` syntax the migration and insert
> path use. A fourth check — whether adding a `seq` key to every
> `Message.to_dict()` output could break F3's Go decoding of the unbounded
> `GetSession` path — was resolved by reading `frontend\internal\chat
> \model.go`: no `DisallowUnknownFields` anywhere in the frontend, so an
> extra JSON key is silently ignored. Safe without touching Go.
>
> **The gate did not pass on the first report.** See "Bugs caught during
> verification" — both were in the throwaway verification script, not the
> product, but the second one is recorded in detail because of what kind of
> bug it is, not because of where it was.

---

## Where the code went

Backend only, as instructed. No frontend file touched.

```
backend\
├── database\
│   ├── migrations.py          v6: messages.seq + idx_messages_session_seq
│   └── session_repository.py  insert_message: atomic seq assignment;
│                               + fetch_message_count, fetch_messages_keyset,
│                               _seq_exists; _row_to_message reads seq
├── managers\
│   ├── message.py              + Message.seq (DB-assigned, not a new()
│   │                            param)
│   ├── session.py               + Session.total_message_count
│   └── session_manager.py       get(message_limit=...); + get_messages_page()
├── runtime\
│   ├── events.py                + SessionMessages / SessionMessageList
│   └── runtime.py                + _handle_session_messages;
│                                  _handle_get_session reads message_limit;
│                                  _session_to_dict adds total_message_count
│                                  only when present
└── cli.py                       + session messages <id> [--before N]
                                  [--after N] [--limit N]
```

`managers\tool.py` and `managers\provider.py` remain deleted; nothing here
touches them.

---

## Migration v6 — `seq` column + composite index

```sql
ALTER TABLE messages ADD COLUMN seq INTEGER NOT NULL DEFAULT 0;

UPDATE messages
SET seq = (
    SELECT ranked.rn
    FROM (
        SELECT id, ROW_NUMBER() OVER (
            PARTITION BY session_id ORDER BY created_at ASC, rowid ASC
        ) AS rn
        FROM messages
    ) AS ranked
    WHERE ranked.id = messages.id
);

CREATE INDEX IF NOT EXISTS idx_messages_session_seq ON messages(session_id, seq);
```

`SCHEMA_VERSION` 5 → 6, as B12's own handoff called it (B12 landed at v5 and
flagged that B18 would be v6; the task doc already said v6 by the time this
episode started, so no correction was needed here).

`rowid` breaks a `created_at` tie **only during this one-time backfill**.
Every row inserted afterward gets its `seq` from `insert_message`'s own
`MAX(seq)+1`, never from a timestamp. The index is created **after** the
backfill — building it against the pre-backfill default-0 values would be
discarded work.

Confirmed on a real backfill: `seed-session --messages 10000` twice into the
same scratch profile (2000 then +8000, exercising both the fresh-insert path
and a real backfilled tail) produced `COUNT(*), MIN(seq), MAX(seq),
COUNT(DISTINCT seq)` = `10000, 1, 10000, 10000` — unique, contiguous, no gaps.
`EXPLAIN QUERY PLAN` on the keyset query reports:

```
SEARCH messages USING INDEX idx_messages_session_seq (session_id=? AND seq<?)
```

not a scan — the point of the episode.

---

## `seq` assignment — one atomic statement, not read-then-write

`SessionRepository.insert_message` computes `seq` as a scalar subquery
**inline in the same `INSERT`**, not a separate `SELECT` beforehand:

```sql
INSERT INTO messages (..., seq)
VALUES (..., (SELECT COALESCE(MAX(seq), 0) + 1 FROM messages WHERE session_id = ?))
RETURNING seq
```

Two coroutines sharing the runtime's single connection can only interleave
*between* whole statements, never inside one, and a single statement is also
what SQLite's own write lock makes atomic across processes (a CLI invocation
running beside a live backend, both against the same WAL-mode file). A
read-then-write two-step would have a gap between the read and the write for
either kind of collision to land in; this has none. `RETURNING seq` hands the
assigned value back so `message.seq` on the in-memory object matches the row
that was actually written, rather than staying `None` until the next fetch.

---

## `GetSession(message_limit)` — additive, unbounded path untouched

`SessionManager.get()` gained `message_limit: Optional[int] = None`.
`None` (the default) is byte-for-byte the old `fetch_messages` path — no new
key appears in the payload at all. Given a value, it calls the same keyset
query as `SessionMessages` with `before=None, after=None` (tail semantics —
"both null means the newest `limit`" is one query, not two), and
`session.total_message_count` is populated from `fetch_message_count`.
`_session_to_dict` only adds the `total_message_count` key when it is not
`None`, so the unbounded response shape is unchanged for F3.

Verified directly (no CLI surface was asked for — see below):
`message_limit=25` against a 10000-message session returned 25 rows,
`total_message_count=10000`, and the newest-tail range `9976..10000`; a
follow-up call with no `message_limit` on the same session still returned all
10000 messages with no `total_message_count` key present.

---

## `SessionMessages` — the keyset query

Wire: `SessionMessages` → `SessionMessageList`.

Request: `{session_id, before: seq|null, after: seq|null, limit: int}`.
`before` and `after` together are refused (`InvalidEventError`) — at most one
may be non-null; both null means "the newest `limit`" (the tail).

Response: `{session_id, messages: [...], has_more_before, has_more_after}`,
always oldest-first within the slice.

One repository method, `SessionRepository.fetch_messages_keyset`, backs both
`SessionMessages` and `GetSession(message_limit)`. `has_more_before` /
`has_more_after` answer "does history exist beyond THIS returned slice" —
computed from the slice's own min/max `seq` (or, for an empty slice, from the
cursor that produced it), so a true end of history and the edge of an empty
batch are both reported honestly rather than requiring a speculative extra
fetch to tell them apart.

CLI: `session messages <id> [--before N] [--after N] [--limit N]` (default
`--limit 50`), printing each row's `seq`, role, and a truncated preview, plus
both `has_more_*` flags.

`GetSession(message_limit)` has **no CLI command** — the episode's Build
section only asked for `session messages`, and CLAUDE.md's rule against
building unlisted product surface applies to verification tooling too.
Verified instead with a scoped, one-off script that talks to `Runtime`
directly (see below), not a new permanent `session get` command.

---

## Manual Test Log (Episode 18)

`session messages` and a scoped one-off script (`fetch_messages_keyset` has
no dedicated CLI beyond it), run against a scratch profile with
`seed-session --messages 10000`.

| Done-when | Result |
|---|---|
| #1 v5 → v6 migration, backfilled, backup taken | ✅ `schema_migrations` reports `6`; `.bak-6` present |
| #2 `seed-session --messages 10000` produces strictly increasing `seq` | ✅ `10000, 1, 10000, 10000` (count, min, max, distinct) |
| #3 `GetSession` unbounded unchanged; `message_limit: N` returns newest N + total count | ✅ unbounded still 10000 rows, no `total_message_count` key; `message_limit=25` → 25 rows, `total_message_count=10000`, range `9976..10000` |
| #4 keyset slices correct, ordered, non-overlapping, non-dropping across every boundary — including former `created_at` ties | ✅ full 1–10000 traversal three independent ways (forward via `--after` from 0, backward via `--before` from the tail, and the plain tail slice) — zero gaps, zero dupes each time; the specific row Aryan's own cross-check flagged (`seq=9970`, range 9968–9972) confirmed present and correctly returned |
| #5 `has_more_before` / `has_more_after` correct at both true ends and mid-history | ✅ `--after 9999` → `has_more_after=False`; `--before 2` → `has_more_before=False`; mid-history slices report both `True` |
| #6 `EXPLAIN QUERY PLAN` shows `idx_messages_session_seq`, not a scan | ✅ `SEARCH messages USING INDEX idx_messages_session_seq (session_id=? AND seq<?)` |
| #7 `SCHEMA_VERSION == 6`; fresh and migrated databases end identical | ✅ confirmed via `schema_migrations` and the file-structure check above |

---

## Bugs caught during verification

Both bugs below were in the throwaway one-off verification script written to
exercise `GetSession(message_limit)` and a full page-through (`b18_check.py`,
kept outside the repo per CLAUDE.md's rule against building unlisted product
surface for a single verification step). **Neither was a defect in
`fetch_messages_keyset`, `_handle_session_messages`, or `cli.py session
messages`** — all three were reproduced clean, independently, before either
bug in the script was found.

**1. `ModuleNotFoundError: No module named 'bootstrap'`, regardless of
working directory.** Running `uv run python <path>` where `<path>` is
**outside** `backend\` sets `sys.path[0]` to the directory containing the
*script being run*, not the process's current working directory — `cd
backend` first does not change this. **Fix:** `sys.path.insert(0,
os.getcwd())` before importing `bootstrap`, so the script resolves `backend\`
imports the same way `cli.py` does when run from inside it.

**2. `cursor = None` seeds a page-through as the TAIL, not the start of
history — and the loop terminates "successfully" having walked almost
nothing.** The script's full-traversal loop called
`get_messages_page(before=None, after=cursor, limit=137)` starting with
`cursor = None`. Per this episode's own contract, `before=None, after=None`
together mean **"the newest `limit` messages"** — the tail — not "give me
everything, starting from the beginning." That first call correctly returned
the newest 137 messages and correctly reported `has_more_after=False` (there
genuinely is nothing newer than the tail), so the loop's own termination
condition fired immediately. The script then asserted the collected set
equalled `range(1, 10001)`, failed, and reported "gap" — Aryan's
report of a specific missing row was investigated on his own initiative,
correctly ruled out the migration and `insert_message`, and correctly
narrowed the search to this side of the contract, which is what turned up the
bug.

**This is worth carrying forward loudly, not just fixing quietly: it is the
failure mode where a test can look green while verifying almost nothing.**
Had the loop's assertion been looser (e.g. "no duplicates" without also
checking the full expected range), it would have printed `OK` after
confirming only the tail 137 rows are internally consistent — passing while
silently never exercising 98.6% of the session. **Fix:** seed the cursor as
`0` ("everything with `seq > 0`", i.e. the actual start of history), not
`None`. A page-through that means "from the beginning" must say so
explicitly with a real boundary value; relying on `None`'s meaning to happen
to be "start" is exactly the kind of assumption this episode's own contract
warns against — `None`/`None` is tail, on purpose, so that
`GetSession(message_limit)` and `SessionMessages` with no cursor can share
one query path.

**F18's own verification will page through history the same way.** Any test
that walks a session end-to-end must seed its first cursor as an explicit
boundary (`0` for `after`, or omit `after`/pass `before=None` deliberately
for "start from the tail instead") and assert the walked count against
`GetSession`'s `total_message_count` — not merely "the loop terminated
without an assertion failure."

---

## What the next episode inherits

- **The contract:** `SessionMessages(session_id, before|after, limit)` →
  `{messages, has_more_before, has_more_after}`, oldest-first within a
  slice, keyed on `seq`. `GetSession(message_limit)` for the initial tail +
  total count. These are the two calls F18 pages against; F18 adds no new
  backend surface.
- **`seq` is the cursor, not `created_at`.** F18 must page on `seq` and store
  `seq` — not a row index, not a timestamp — as its scroll anchor.
  `Message.seq` is present on every message dict now, including the
  unbounded `GetSession` path, so F18 does not need a separate call to learn
  a message's `seq`.
- **`has_more_*` means "true end of history," not "end of this batch."** F18
  uses it to decide when to stop trying to fetch further in a direction.
- **Total count** comes from `GetSession(message_limit)` for the scrollbar
  and position readout.
- **`None`/`None` on `before`/`after` means the tail, not "start of
  history."** Say this explicitly in F18's own test harness — see the bug
  above. A page-through that means "from the beginning" needs `after=0` (or
  the equivalent explicit boundary), never a bare `None` cursor.
- **`frontend\internal\chat\model.go`'s `wireMessage` comment claims it
  "mirrors `Message.to_dict()` exactly."** That is now slightly stale — a
  `seq` key exists on the wire that `wireMessage` does not declare. Harmless
  today (Go's `encoding/json` ignores unknown fields; no
  `DisallowUnknownFields` anywhere in the frontend, confirmed by search), but
  F18 is the episode that actually reads `seq` off the wire, so it is also
  the natural place to add the field to `wireMessage` and make the comment
  true again.
- **Not built here, on purpose:** any eviction, windowing, or backpressure —
  all F18's. Any archival/summarization, or semantic retrieval — B13's.

**No new dependencies.**
