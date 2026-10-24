# Episode B40 — The Durable Event Log, and Real `events_since` ✅

**This doc claims reality.** `../../tasks/backend/B40-durable-event-log.md`
describes intent. On any disagreement, **the code wins** — report the
mismatch, do not silently edit either side.

Migration **v8** adds an `events` table. Every session-scoped envelope
that leaves `Runtime._emit` is written to it, carrying a durable
per-session cursor that rides back out on the envelope as `event_seq`.
`EventsSince` reads it. **B15's `SessionReplaySince` is deleted**, from
`events.py`, `cli.py`, `types.go` and `client.go`, and
`Client.afterReconnect` calls the new command.

What this buys over the interim, in one sentence: B15's message-level
replay could answer *what does this transcript look like now*; this
answers *what happened while I was gone* — the tool call that started
**and** completed in the gap, progress steps, usage updates, a
`FillProposal` pushed after a terminal event. None of those are
messages, so none of them were expressible before.

---

## Where the code went

```
backend\
├── database\
│   ├── migrations.py          SCHEMA_VERSION 7 -> 8, _V8, Migration(8, ...)
│   └── event_repository.py    NEW — EventRepository: append (one statement,
│                              RETURNING the assigned cursor), events_since,
│                              max_seq, count, sweep, maybe_sweep. Holds the
│                              policy for what is NOT logged: VOLATILE_TYPES
│                              (the token stream) and REPLAY_TYPES
├── runtime\
│   ├── events.py              + OutgoingEvent.event_seq and its wire key.
│   │                          IncomingEventType.EVENTS_SINCE and
│   │                          OutgoingEventType.SESSION_EVENTS replace
│   │                          SESSION_REPLAY_SINCE / SESSION_REPLAY
│   └── runtime.py             self.events = EventRepository(db_conn);
│                              _emit persists then emits; _handle_events_since
│                              replaces _handle_session_replay_since;
│                              _heartbeat_loop calls events.maybe_sweep()
├── cli.py                     `session events-since` replaces `session
│                              replay`; `bench event-log` is new
└── tests\
    ├── manual_b40_event_log.py        NEW — 26 checks
    ├── manual_b15_lifecycle.py        [4] block rewritten (see below)
    └── fixtures\mock_dense.json       NEW — 2000 deltas, zero delay

frontend\internal\protocol\
├── envelope.go                + Envelope.EventSeq and EventSeqOr()
├── types.go                   CmdEventsSince / EvSessionEvents replace
│                              CmdSessionReplaySince / EvSessionReplay
├── client.go                  afterReconnect sends CmdEventsSince;
│                              lastSeq and MarkSeen re-documented as the
│                              event cursor; deliver() routes
│                              EvSessionEvents as RouteReplay
└── client_test.go             the two replay tests follow the rename
```

---

## Key decisions

### 1. Option (c): lifecycle events are logged, the token stream is not (D99)

`TextDelta`, `ThinkingDelta` and `TaskToken` are excluded.
**Chosen on a measurement, not a preference** — the task doc was
explicit that it owed numbers here, because a write per envelope lands
on the streaming hot path through B22's `SerializedConnection`, which
takes a connection-wide `asyncio.Lock` and commits per statement.

`cli.py bench event-log` runs three arms over one fixture: the log
disabled (what `_emit` cost before this episode), option (c), and option
(a) (every session-scoped envelope, deltas included). Four runs,
`mock_dense.json`, 2,000 deltas per turn:

| arm | wall_s — 4 runs | rows written | vs. baseline |
|---|---|---|---|
| none — no log | 0.190 / 0.253 / 0.262 / 0.228 | 0 | — |
| **(c) — what ships** | 0.217 / 0.280 / 0.276 / 0.267 | 4 | within noise |
| (a) — every envelope | 4.266 / 4.243 / 4.062 / 3.558 | 2,004 | **15–20×** |

**(a) costs roughly 2 ms per envelope**, so 2,004 writes add ~3.8 s to a
turn that otherwise takes ~0.23 s.

**(c)'s own cost is NOT claimed to be zero.** Its measured delta against
the baseline ranged from **−14% to +47%** across those four runs — a
spread that straddles zero, which is the honest reading of a sub-300 ms
measurement and not evidence of a speedup. What the numbers support is
narrower and sufficient: four writes a turn are not detectable, and two
thousand are.

#### The fixture had to be built, and that is the finding

The gate as originally drafted named `mock_slow` as the measurement
fixture. It is 20 chunks at `delay: 0.3` — six seconds of
`asyncio.sleep` in the mock provider — so the provider's sleeps swamp
any per-write cost and all three arms land within ~1.5% of each other:

| arm | `mock_slow` wall_s | rows written |
|---|---|---|
| none | 6.411 | 0 |
| (c) | 6.435 | 4 |
| (a) | 6.510 | 24 |

Those three numbers look like evidence and are not. `mock_dense.json`
(2,000 deltas, zero delay) was added so wall-clock is dominated by the
emit path rather than the provider, and it is `bench event-log`'s
default. **`--script mock_slow` is kept and still run** as the
realistic-cadence arm — the table above is its result, and it is worth
having precisely because it shows the realistic case is not where the
cost lives.

#### Why (c) is also correct, not merely cheap

Those three types are exactly `IsDroppable` in
`frontend\internal\protocol\types.go`: the live path may already discard
them under backpressure, because *"the accumulated string re-renders
from the next token anyway"*. An event the live path is permitted to
drop is not one a replay can be expected to hold. And the text is not
lost by its absence — it rebuilds from `messages` for a finished reply,
or from B15's `message_checkpoints` for one the process died mid-way
through. Logging the deltas would be a third copy of the same string,
written per delta.

So a replay reconstructs the **structure** of a turn, with the prose
coming from the tables that already own prose.

**Option (b) (buffer and flush) was not built and not measured.** (c)
made it unnecessary rather than preferable: the volume (b) exists to
batch is the volume (c) does not write, so (b) would have bought a flush
timer, a bounded buffer and a lost-window-on-crash caveat in exchange
for batching four writes a turn.

### 2. `event_seq` is a new envelope field; `seq` keeps its meaning (D100)

The task doc leaned additive; **this confirms it rather than overriding
it**, and the reason is a count rather than a principle: **four
consumers already decode `seq`** — `StreamAccumulator`, F5's stream
ordering, F18's pagination, and B24's `FillProposal` note. Redefining it
would be one field instead of two, bought with a contract change
arriving disguised as the same field: the kind a frontend episode finds
six weeks later, because nothing fails at the boundary.

There are now **three** distinct per-something counters here, and
conflating any two is a bug:

| field | what it counts | lifetime |
|---|---|---|
| envelope `seq` / `events.stream_seq` | per-TASK stream position | in-memory, dropped at `close_stream()`, null on every lifecycle event |
| `messages.seq` (v6/B18) | per-session MESSAGE insertion order | durable |
| `event_seq` / `events.session_seq` (v8/B40) | per-session EVENT order | durable |

`event_seq` is null on any envelope the log does not hold — one with no
session, and the three token types. A client marks the non-null ones;
the nulls are not gaps.

B24's note that *"`seq` is null on BOTH envelopes and cannot order
them"* becomes **incomplete rather than wrong**, which is the safe
direction: `seq` is still null there, and `event_seq` is now what can
order them.

`MarkSeen` stays the **consumer's** to call, even though `event_seq` is
a routing field the protocol package now parses and could derive
itself. The mark means "has been rendered", not "has arrived":
auto-marking in `deliver()` would advance it for events still sitting in
a channel, and a reconnect would then skip exactly the events the UI
never drew.

### 3. Retention: 7 days OR 2,000 rows per session (D102)

Decided in-episode as the task doc required, rather than deferred to
B17 — *a table with no retention policy is a disk-full bug with a long
fuse.*

- **Age: 7 days.** The cap that matches what replay is *for*: a client
  away for a week restarted, it is not reconnecting. Seven days still
  leaves the log usable as a "what did the backend send yesterday"
  diagnostic.
- **Rows: 2,000 per session.** Age alone does not bound one very busy
  session inside the window. Per-session rather than global, so a
  chatty session cannot evict a quiet one's replayable history; 2,000
  is an order of magnitude above `client.go`'s `replayLimit` of 200.
- **Run from `_heartbeat_loop`**, beside `TaskManager.sweep_finished`
  and for the same reason, throttled to every 60th beat (~5 min) by
  `RETENTION_EVERY_N_BEATS`. Never on a request path.

**A sweep does not renumber.** `session_seq` has gaps afterwards and
that is correct: the cursor is a high-water mark, `events_since` asks
`> seq` rather than for a dense run, and renumbering would invalidate
every client's existing mark. A client whose mark was swept past gets
the oldest surviving events plus `has_more` — the most a bounded log can
honestly offer. Asserted directly (`[5]`, five checks).

### 4. The cursor is assigned in one statement

```sql
INSERT INTO events (...) VALUES (
    ?, ?, ?, ?, ?,
    (SELECT COALESCE(MAX(session_seq), 0) + 1 FROM events WHERE session_id = ?),
    ?, ?, ?
)
RETURNING session_seq
```

`insert_message`'s inline-subquery pattern, plus `RETURNING` so the
assigned value comes back without a second statement to interleave
with. B18's note is why this is sufficient: *two coroutines on this
connection can only interleave between whole statements, never inside
one*, and `SerializedConnection` drains the cursor before releasing its
lock. SQLite here is 3.53.1; `RETURNING` needs 3.35+.

The `(session_id, session_seq)` index is **UNIQUE**, which puts
Done-when #1's monotonic cursor under the database rather than under a
convention: a bug handing out a cursor twice fails the INSERT instead of
producing a replay with a duplicate.

---

## The schema deviates from the task doc, deliberately

Section 1 of the task doc drafted `session_id` **nullable**, reasoning
that `hello` and `ping` have no session and *"an event log that cannot
hold them is not a log of what went over the wire"*.

**It is `NOT NULL REFERENCES sessions(id) ON DELETE CASCADE` in the
built schema**, because that draft contradicted its own next column.
`session_seq` is a **per-session** cursor, so an envelope with no session
has no position in any cursor space for a `NOT NULL` column to take, and
the single query against this table — `WHERE session_id = ? AND
session_seq > ?` — can never return such a row. The contradiction
resolves in favour of the cursor, because the cursor is the only reason
the table exists.

This is wider than `hello`/`ping`, which is the part worth recording.
`_fill_envelope` backfills `session_id` only when the *request* carried
one, so **eleven** further types emit session-less: `SessionList`,
`ProviderList`, `ProviderModelList`, `AgentList`, `DirectoryList`,
`DoctorReport`, `BehaviourOverrideSet`, `TaskList`,
`shutdown.complete`, and any `Error` raised before a session is known.
Logging them would be unreplayable rows — at one `ping` every five
seconds forever, given B15's unconditional heartbeat (D98).

So **connection-level and session-less envelopes are not written at
all** (section 1's explicit decision point, answered *no*), and the
claim this table makes is the narrower, true one: it is a log of what
went over the wire **for a session**. Recorded as D101. Raised with
Aryan and confirmed before the schema was written.

Wire-completeness as a property would need a second global cursor
column and a nullable `session_id`, buying a `ping` row every five
seconds that nothing reads.

---

## Two defects found while building, neither reasoned about in advance

Both were caught by `manual_b40_event_log.py`, not by reading the code.

### 1. The replay reply was being logged

`_emit` is the one persistence boundary, and the reply to `EventsSince`
goes out through `_emit` like every other envelope — so the first
version of this episode **logged it**, appending a row whose payload was
a verbatim copy of a page of the log.

Three consequences, all observed rather than predicted:

- the log grew **super-linearly in replays** — a page of N events
  becomes one row containing N events, which the next replay returns and
  copies again;
- `events_since(session, max_seq)` **never settled**, because asking
  "anything after the end?" itself created something after the end. This
  is how it was caught: the check *"a cursor at the end returns nothing
  and says so"* came back holding a `SessionEvents` row at
  `event_seq 12`;
- a reconnecting client would have **replayed its own previous replies
  back to itself**.

Fixed with `REPLAY_TYPES`, excluded **unconditionally** —
`persist_volatile` does not reach it, because this is a defect and not a
cost trade like the deltas. The general rule, stated in the module so
the next addition inherits it: **an envelope that CARRIES log contents
is not log content.** Anything later carrying `replay: true` belongs in
that set.

Two sibling checks failed from the same cause and now pass: the
per-session cursor test (*"the first session's cursor was not moved by
the second's traffic"* — it had moved, by the replies to my own
queries) and *"the cursor does NOT restart after a sweep"*.

### 2. `_emit` trusted a guarantee made one layer down

`EventRepository.append` catches operational failures itself (a locked
database, a full disk) and returns `None`, and `_emit`'s docstring said
so — *"`append()` never raises"*. But the guarantee belonged to the
layer that **depends** on it, not the layer that makes it: any other
defect in the log path propagated out of `_emit` and killed the
request. A reply the user is waiting for, lost to a recovery aid, is a
far worse outcome than a degraded replay.

`_emit` now guards the call as well. The repository's internal catch is
the first line and this is the second; they are not redundant, because
they cover different failures.

**Evidence is an induced failure, not an argument.**
`manual_b40_event_log.py`'s `[1c]` substitutes a repository whose
`append` raises `RuntimeError` and delegates everything else, then
asserts the envelope still goes out. Run against the pre-fix code it
produced an uncaught `RuntimeError: disk on fire` out of
`runtime.receive`; it passes now. The stub raises on demand because the
real trigger — a locked database or a full disk — is not reproducible on
demand, and a guarantee of this shape is worth testing rather than
asserting.

---

## B15's Done-when #4, re-run against the new mechanism

The task doc required a **re-test, not a doc edit**, and that is what
happened.

**Run unchanged against the new code**, `manual_b15_lifecycle.py`'s
`[4]` block failed on four assertions:

```
[4] replay from a seq cursor
  [FAIL] a SessionReplay reply arrived
  [FAIL] exactly the messages after the mark, in order  -- replayed [] from a session holding [1, 2]
  [PASS] no gaps: consecutive seqs
  [FAIL] the mechanism is stated on the wire, not assumed  -- mechanism=None replay=None
  [FAIL] the task's state rides along so an UNKNOWN task can be resolved  -- tasks=[]
```

The reply never arrived at all, because `SessionReplaySince` is no
longer a known event type, and `mechanism` came back `None` — the
failure the task doc predicted. **Every other block in the file
passed**, which is what localised the change to #4 and is itself the
useful signal: the event log did not disturb the unload refusal rule,
the checkpoint behaviour, bounded growth, or the `Shutdown` path.

The block was rewritten to assert `mechanism == "events.session_seq"`
and the event cursor, with the ordering and no-gaps checks intact, and
extended with three checks the new mechanism makes possible: that every
replayed event is flagged `replay: true`, that the replay carries
non-message events, and that the token stream is **not** in it. The
whole file now passes (`ALL CHECKS PASSED`, exit 0), re-confirmed on a
final run.

### One forward-looking note: a check that passed on no data

Worth recording because it generalises, **not** as a re-litigation of
B15's gate, which passed on its own terms against the mechanism that
then existed.

In the failing run above, `[PASS] no gaps: consecutive seqs` sits in the
middle of three siblings reporting that the reply had not arrived. Its
condition was:

```python
all(b - a == 1 for a, b in zip(seqs, seqs[1:]))
```

which is **vacuously true on an empty list**. It passed because there
was nothing to check, beside assertions saying there was nothing to
check. The rewrite guards it with `bool(seqs)`, and
`manual_b40_event_log.py` guards every equivalent check the same way.

The generalisation, for whoever writes the next gate: **an
emptiness-tolerant predicate over a collection that may legitimately be
empty is not an assertion.** `all()`, `any()` over a negation, and
`== sorted(...)` all pass on `[]`. The fix is a non-emptiness guard
beside the property, and `manual_b40_event_log.py` additionally pairs
several checks with a guard check that the *input* was non-trivial —
*"deltas went over the wire in this turn"* before *"and none of them are
in the log"*, and *"the second turn really did produce a tool pair"*
before asserting the pair replays. The second of those caught two real
harness mistakes during this build (a `tool_not_granted` failure from
using the default `general` agent, which grants no tools, and a
tool-free turn from the mock provider's turn cursor clamping to the last
turn of a multi-turn fixture) that would otherwise have been a silent
vacuous pass.

---

## Manual Test Log (Episode B40)

Gate green end to end, confirmed by Aryan. Schema v8.

| # | Done-when | Result |
|---|---|---|
| 1 | Every persisted envelope appears in `events` with its own id, type, correlation fields and a monotonic per-session cursor | **PASS** — `manual_b40_event_log.py` `[1]`, 5 checks, including that each logged id is the id that actually went over the wire. Gate: `TaskStarted\|1 … ContextReport\|5` |
| 2 | `events_since` returns exactly the events after the cursor, in order, no gaps — **and B15's #4 re-run against it** | **PASS** — `[6]` and `[2b]`; B15 `[4]` rewritten and passing (above) |
| 3 | The measured cost of the write recorded with fixture and numbers | **PASS** — four tables above; `bench event-log` on `mock_dense` and `mock_slow` |
| 4 | `SessionReplaySince` gone from `events.py`, `cli.py`, `types.go`, `client.go`; `afterReconnect` calls the new command | **PASS** — **4** surviving grep hits, one per file, every one prose inside a comment explaining what replaced it; no enum member, constant, handler or CLI subcommand remains. Count verified by re-running the grep, not from memory — an earlier draft of the task doc's own gate note said "two" |
| 5 | Retention implemented and its policy stated | **PASS** — `[5]`, 5 checks: a no-op sweep deletes nothing, the row cap keeps the newest, a swept log still replays, the cursor does not restart, the age cap clears |
| 6 | A reconnect mid-stream rebuilds the UI from the log: the tool call that started and completed while the client was away is visible | **PASS** — `[6]`, 7 checks, over `two-tool-calls.json` with the `research` agent |

Additional, beyond the Done-when list:

- `[1b]` the cursor is **per-session**, not global — a second session's
  log starts at 1 while the first has passed 11, replay does not leak
  across sessions, and one session's traffic does not move another's
  cursor (3 checks).
- `[2]` the token stream is absent and `event_seq` is present exactly
  where the log holds a row (4 checks).
- `[1c]` an induced write failure does not fail the emit (1 check).
- Gate assertions: `select count(*) from events where type in
  ('TextDelta','ThinkingDelta','TaskToken')` → **0**;
  `where session_id is null` → **0**.
- `manual_b15_lifecycle.py`, `manual_concurrent_completion.py`,
  `manual_prompt_isolation.py` — all `ALL CHECKS PASSED`. The second
  matters: B22's race is the reason `SerializedConnection` exists, and
  this episode added a write per emit through it.
- `go build ./...` clean; `go test ./...` clean, including the two
  renamed replay tests in `frontend/internal/protocol`.

---

## What the next episode inherits

### F2's ladder now calls what F2 was specified against

`Client.afterReconnect` sends `CmdEventsSince` and the reply arrives as
`EvSessionEvents`, routed `RouteReplay`. **The consumer side has not
been rewritten**, and this is the one real handoff: the reply's payload
key changed from `messages` to `events`, and whoever decodes it must
now merge events by `event_seq` and id rather than merging messages by
`messages.seq`. `MarkSeen` must be fed `Envelope.EventSeq`
(`EventSeqOr(0)` makes a null a no-op) and **not** a message payload's
`seq`. Nothing in `internal/chat` reads the old reply today, so nothing
is broken; the work is to make a reconnect visibly rebuild a view, which
is a frontend episode's.

### B16 (SSH Serving)

The log is keyed by session, not by connection. `connection_id` is on
the envelope and is **not** a column in `events` — per-connection
replay, if B16 wants it, is a schema addition and should be decided
there rather than assumed from this table.

### B17 (Hardening)

Retention is implemented, so B17 inherits a bounded table rather than an
open question. Two things it may want: the caps are module constants,
not config (deliberately — nothing has needed to tune them), and
`sweep()` runs two statements under the heartbeat rather than one
planned query, which is a readability-over-cleverness call documented in
place.

A3 from B15's appendix (`os._exit` at interpreter shutdown) is
**untouched and still B17's**. This episode added nothing that must run
at interpreter shutdown.

### B13 (Memory System)

Unaffected. `memory_records`/`memory_scopes` remain in the schema and
unused; this episode added a table beside them and touched neither.

### Not touched, on purpose

`message_checkpoints` (B15). The task doc's Do-NOT list forbids it, the
two mechanisms are different — the checkpoint preserves the in-flight
reply **text**, the log preserves event **structure** — and they are
complementary rather than overlapping: option (c) is only correct
*because* the checkpoint already holds the text across the same crash
window. B15's Done-when #3 therefore stands exactly as recorded.
