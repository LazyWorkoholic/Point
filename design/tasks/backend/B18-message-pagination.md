# Episode B18 — Message Pagination (keyset scrollback)

**A monotonic `seq` per message, an index that makes "the last N messages of
this session" an index seek instead of a full-session sort, and one keyset
query — `SessionMessages(session_id, before|after, limit)` — that F18 pages
against. `GetSession` keeps working untouched, so nothing breaks while F18 is
unbuilt.**

> Task doc — this describes **intent**. The completed doc
> (`design/completed/backend/B18-message-pagination.md`) is where reality and
> completion are claimed. If they disagree later, the code wins.

**Model:** Sonnet. This is one migration, one event type, one dispatcher
handler, one repository method, verifiable end to end through `cli.py` with no
Go attached. It is deliberately small.

**Depends on:** B6 (schema/migrations, envelope). Nothing else. Buildable at any
point — ideal filler between heavier frontend episodes.

**Consumed by:** F18 (windowed history) is the only consumer. B18 ships a query
surface F18 will page against; per `CLAUDE.md`, that is acceptable because B18 is
independently verifiable through the CLI — it does not depend on F18 existing.

---

## Why this episode exists

F3 assumed a `session.messages(before, limit)` backend query that never existed.
`GetSession` returns the **entire** unpaginated message list via
`SessionRepository.fetch_messages`. F3 shipped a windowed *renderer* as the
interim stopgap (rendering cost is O(rows), not O(messages)), but the **fetch**
is still O(all messages): the whole session is read, serialized, sent over
stdio, and parsed on the Go side at session open. On a large session and a
low-end device — the exact target — that one-shot fetch is the cost that hurts.

B18 removes that cost at the source. It does **not** touch the frontend; F18
does that. B18's entire job is to make "give me a bounded, ordered slice of a
session's history, from any point, cheaply" a real backend capability.

---

## The ordering problem — read this before writing the migration

Keyset pagination needs a cursor that is **unique and monotonic per session**.
`created_at` alone is neither:

- It is a `REAL` (float epoch). Two messages in one turn — a user message and
  the first persisted assistant row, or two rapid appends during `seed-session`
  — can share an identical timestamp. `WHERE created_at < ? ORDER BY created_at
  DESC LIMIT N` then **drops or repeats** rows at any page boundary a tie
  straddles. `seed-session` writes thousands of rows in a tight loop, so this
  surfaces immediately in testing, not months later.
- The `messages` table has `id TEXT PRIMARY KEY` (a uuid — random, not
  sequential) and indexes on `session_id` and `task_id` only. There is **no**
  column expressing insertion order, and no `(session_id, ordering)` composite
  index, so even a correct query would full-scan the session and sort.

**The fix is a real `seq` column, not a workaround.** A conversation has an
inherent sequence — turn 1, then turn 2 — and the schema currently has no column
that says so. Timestamps are a proxy that works until it doesn't, and F5's
streaming/queued sends are exactly when ties cluster. Add the column, index it,
and every ordering question becomes exact and cheap.

---

## Build

### 1. Migration v5 — `seq` column + composite index

- New column `messages.seq INTEGER NOT NULL DEFAULT 0`, monotonic **per
  session**.
- **Backfill** existing rows: for each session, assign `seq` in ascending
  `created_at, rowid` order (rowid breaks any `created_at` tie deterministically
  during backfill only). One pass. The migration system already takes an
  automatic backup before each step; this is a local, single-user database, so
  the backfill is safe and cheap.
- New index: `CREATE INDEX idx_messages_session_seq ON messages(session_id,
  seq)`. **This index is the point of the episode** — without it the keyset
  query degrades to a scan and buys nothing.
- `SCHEMA_VERSION` → 5.

### 2. Assign `seq` on every new message

- When `SessionManager.append_message` (or whatever B2/B3 path persists a
  message) writes a row, it sets `seq` to `MAX(seq) + 1` for that session,
  computed in the same transaction as the insert so two concurrent appends
  cannot collide on a value. (WAL + a single writer per session makes this
  straightforward; if the write path is async, ensure the max-and-insert is one
  atomic step, not read-then-write with a gap.)
- Confirm `seed-session` produces strictly increasing `seq` values — it is the
  fixture that F18 verifies against, so its ordering must be exact.

### 3. `GetSession` gains an optional `message_limit`

- New optional payload field `message_limit: int | null`. Omitted or null →
  **today's unbounded behaviour, unchanged** (this is what keeps F3 working
  while F18 is unbuilt). Present → return session metadata plus the newest
  `message_limit` messages, ordered oldest-first within that slice.
- Also return the session's **total message count** (cheap with the new index).
  F18 needs it for an honest scrollbar and "message 400 of 12,000".

### 4. New event: `SessionMessages` — the keyset query

- New `IncomingEventType: SESSION_MESSAGES`, new `OutgoingEventType:
  SESSION_MESSAGE_LIST`.
- Request payload: `{session_id, before: seq | null, after: seq | null, limit:
  int}`. Exactly one of `before`/`after` is set (both null = "newest `limit`",
  equivalent to the tail). `before: S` → the `limit` messages with `seq < S`,
  ordered oldest-first. `after: S` → the `limit` messages with `seq > S`,
  oldest-first.
- Response payload: `{session_id, messages: [...], has_more_before: bool,
  has_more_after: bool}`. The two `has_more_*` flags let F18 know whether an
  edge of the resident window is a true end of history or just the edge of the
  current batch — it must not have to issue a speculative extra fetch to find
  out.
- Repository method: one keyset query using `WHERE session_id = ? AND seq < ?
  ORDER BY seq DESC LIMIT ?` (then reverse for oldest-first), or the `> ? ORDER
  BY seq ASC` mirror. It **must** use the `(session_id, seq)` index — verify
  with `EXPLAIN QUERY PLAN` in the gate, not by eye.

### 5. `cli.py` — expose it for verification

- `session messages <id> [--before N] [--after N] [--limit N]` prints the
  returned slice and both `has_more` flags. This is how the entire episode is
  verified without Go.
- `session new`/`list`/`seed-session` unchanged except that `seed-session`'s
  rows now carry `seq` (falls out of step 2 automatically).

---

## Do NOT

- **Do not touch the frontend.** No Go changes. F18 owns every consumer-side
  concern (windowing, eviction, anchor, backpressure). If you find yourself
  reasoning about scroll position, you are in the wrong episode.
- **Do not remove or change `GetSession`'s existing unbounded behaviour.** The
  optional `message_limit` is additive. F3 relies on the unbounded path and must
  keep working until F18 replaces it.
- **Do not paginate on `created_at`.** The whole episode exists because that is
  not safe. Everything keys on `seq`.
- **Do not add `OFFSET`-based pagination.** Offset scans get slower as history
  grows — the opposite of a scale fix. Keyset only.
- **Do not implement archival, summarization, or eviction of old messages.**
  That is B13's territory and is lossy. B18 makes history *cheap to read in
  slices*, nothing more.
- **Do not collapse or normalize message content.** Return rows verbatim.

---

## Done when

1. `uv run python cli.py migrate` brings a v4 database to v5, backfilling `seq`
   per session in `created_at` order, with a backup taken first.
2. `seed-session --messages 10000` produces rows with strictly increasing `seq`
   within the session.
3. `GetSession` with no `message_limit` returns the full list exactly as before
   (F3's path is untouched); with `message_limit: N` returns the newest N plus a
   correct total count.
4. `SessionMessages` with `before`/`after`/`limit` returns correct, correctly
   ordered, non-overlapping, non-dropping slices across every page boundary —
   including boundaries where multiple messages once shared a `created_at`
   (proving `seq` fixed the tie problem).
5. `has_more_before` / `has_more_after` are correct at both true ends of history
   and mid-history.
6. `EXPLAIN QUERY PLAN` on the keyset query shows it using
   `idx_messages_session_seq`, not a scan.
7. `SCHEMA_VERSION == 5`; a fresh database and a migrated one end identical.

---

## Verification gate (PowerShell, scratch profile)

Run against a renamed-aside scratch profile as one script (the B7 lesson — env
vars do not persist across separate tool calls; a stray write must never hit the
real `%LOCALAPPDATA%\Point`).

```powershell
$scratch = "$env:TEMP\point-b18-verify"
Remove-Item -Recurse -Force $scratch -ErrorAction SilentlyContinue
New-Item -ItemType Directory -Force "$scratch\roaming","$scratch\local" | Out-Null
$env:APPDATA = "$scratch\roaming"; $env:LOCALAPPDATA = "$scratch\local"

cd D:\Projects\Point\backend
uv run python cli.py migrate           # 1 -> 5, backup taken
uv run python cli.py seed-session --messages 10000

# seq is strictly increasing, no gaps that break ordering
sqlite3 "$env:LOCALAPPDATA\Point\runtime.db" `
  "SELECT COUNT(*), MIN(seq), MAX(seq), COUNT(DISTINCT seq) FROM messages;"
# expect: 10000, 1, 10000, 10000  (unique, contiguous)

# the index is actually used
sqlite3 "$env:LOCALAPPDATA\Point\runtime.db" `
  "EXPLAIN QUERY PLAN SELECT * FROM messages WHERE session_id='<id>' AND seq < 5000 ORDER BY seq DESC LIMIT 50;"
# expect: USING INDEX idx_messages_session_seq

# keyset paging returns clean, non-overlapping slices
uv run python cli.py session messages <id> --limit 50               # newest 50
uv run python cli.py session messages <id> --before 9950 --limit 50 # the 50 before
uv run python cli.py session messages <id> --after  50   --limit 50 # the 50 after
# page the whole session end to end; assert no row appears twice and none is skipped

# has_more flags honest at the ends
uv run python cli.py session messages <id> --after 9999 --limit 50  # has_more_after = false
uv run python cli.py session messages <id> --before 2  --limit 50   # has_more_before = false
```

Migration commands touching `%LOCALAPPDATA%\Point` run as **one** script against
the scratch profile per `CLAUDE.md`'s hard rule.

---

## Handoff — what F18 inherits

- **The contract:** `SessionMessages(session_id, before|after, limit)` →
  `{messages, has_more_before, has_more_after}`, oldest-first within a slice,
  keyed on `seq`. `GetSession(message_limit)` for the initial tail + total
  count. These are the two calls F18 pages against — F18 adds no new backend
  surface.
- **`seq` is the cursor, not `created_at`.** F18 must page on `seq` and store
  `seq` (not a row index, not a timestamp) as its scroll anchor.
- **`has_more_*` means "true end of history," not "end of this batch."** F18 uses
  it to decide when to stop trying to fetch further in a direction.
- **Total count** comes from `GetSession(message_limit)` for the scrollbar and
  position readout.
- **Not built here, on purpose:** any eviction, windowing, or backpressure — all
  F18. Any archival/summarization — B13. Semantic retrieval — B13.
