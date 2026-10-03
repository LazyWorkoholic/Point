SESSION  EPISODE                          MODEL         WHY
1        B6  schema + envelope v1         Opus high     defines the wire contract
                                                        + schema everything sits on
2        B7  project system               Sonnet high   spec is precise; path guard
                                                        is the only subtle part
3        B8  context builder              Opus high     highest-leverage function
                                                        in the backend
4        B9  agent foundation             Sonnet high   mostly plumbing a dataclass
5        B10 tool system                  Opus medium   the loop + bounds need care
6        B11 three agents                 Sonnet high   behaviour, well specced
7        B12 routing, caps                Opus medium   concurrency honesty is subtle
8        F1+F2 bootstrap + client         Opus high     layout engine, registry and
                                                        protocol — all load-bearing
9        F3  chat viewport                Sonnet high   rendering, spec is exact
10       F4  query input                  Sonnet high   fiddly but mechanical
11       F5  streaming, queue, cancel     Opus medium   async state machine
11b      F5 re-verifying after B12        Opus medium
11c      B18 message pagination           Sonnet high
12       F7  popup framework              Sonnet high   one widget, clear spec
13       F6  right pane                   Sonnet medium mostly wiring events to cards
                                                        -- ESTIMATE WAS WRONG, see below
13b      B21 orphaned message context     Sonnet high   one filter, but in the
                                                        highest-blast-radius function
                                                        in the backend
13c      B20 context.report on the wire   Sonnet high   transport over a calculation
                                                        that already exists
13d      S13d wire the context card       Sonnet 5, switched to Opus 5.5 partway
13f      B23 agent + directory listing   Sonnet high   plumbing over an existing,
         on the wire                                   tested registry.list() --
                                                        one real open decision
                                                        (the outside-project gate
                                                        for a listing vs. a read)
14       F8  @ picker                     Sonnet high   modes + guards, well defined
                                                        -- now reads B23's events
15       F9a tray + result cards         Opus 5        SPLIT before writing code --
                                                       four of F9's eight Done-whens
                                                       assume things this repo does
                                                       not have (no Editor
                                                       workspace, source_type is
                                                       file-only, nothing writes
                                                       memory_records, ListTasks
                                                       carries no topic or token
                                                       count). Built #1 and #8 only.
                                                       The "Opus medium / diff
                                                       computation + apply paths"
                                                       estimate this row replaces
                                                       was for the WHOLE episode and
                                                       was never spent on it.
15b      B19 Silence-Based Agent Timeout (done)
15c      B25 persist provider key         Sonnet high   SPLIT OUT of session 16 --
                                                        stopped before writing code
                                                        once F10's "backend writes
                                                        credentials.toml" turned out
                                                        to have no caller anywhere.
                                                        One call site over a writer
                                                        that already exists and is
                                                        already correct; the real
                                                        work is the restart-across-
                                                        process verification, not
                                                        the diff.
16       F10 settings, first run          Sonnet high   forms and flows --
                                                        NARROWED, same shape as F9a.
                                                        Stopped once before writing
                                                        code: credential persistence
                                                        (now B25/15c), the doctor
                                                        action, Behaviour's live
                                                        effect, and demo mode all
                                                        assumed backend surface this
                                                        repo does not have. This
                                                        session builds the provider
                                                        chooser, credential entry,
                                                        no-provider state, and the
                                                        Providers+Appearance tabs
                                                        only.
16a      B26 behaviour overrides +         Sonnet high   plumbing over seven existing
         doctor wire (backend)                          config_value() call sites and
                                                        cmd_doctor's own print
                                                        sequence -- same shape as
                                                        B20/B25. The one real catch
                                                        (asyncio.run() inside the wire
                                                        handler's already-running
                                                        event loop) is already found
                                                        and documented in the task
                                                        doc, not something to
                                                        discover mid-session.
16c      F10b behaviour overrides +       Sonnet high   plumbing now that B26 (16a)
         doctor in About (done)                         has landed -- same shape as
                                                        B20/S13d. Escalate only if
                                                        the runtime override layer
                                                        turns out to interact with
                                                        the scheduler's concurrency
                                                        gate.
                                                        -- IT DID, and the estimate's
                                                        own escalation trigger was
                                                        never needed: four live-
                                                        verification rounds surfaced
                                                        a real correctness bug
                                                        (provider re-expand silently
                                                        reverting a specific model
                                                        choice), a genuine
                                                        chat.queue_depth/scheduler
                                                        interaction (the frontend's
                                                        OWN unrelated local queue, not
                                                        the override layer, gates
                                                        every plain-chat send to one
                                                        slot regardless of configured
                                                        depth -- found by direct
                                                        file:line trace, not guessed),
                                                        a self-caught correction on
                                                        this episode's own first
                                                        write-up of that finding
                                                        (claimed "@research proves it
                                                        works" without checking --
                                                        @research reads a DIFFERENT
                                                        key entirely), and three
                                                        rendering bugs in one new row
                                                        -- all handled on Sonnet high,
                                                        no escalation. The pattern each
                                                        time: render/trace the REAL
                                                        thing (Popup.View at real
                                                        width, the actual call chain
                                                        with line numbers) rather than
                                                        reasoning from a mental model
                                                        of it -- every wrong claim in
                                                        this session was a mental
                                                        shortcut standing in for that.
16d      F10c demo mode                   Opus medium   fixture design + timed
                                                        replay is product judgment,
                                                        not plumbing -- and the
                                                        filler-explainer fixture is
                                                        blocked on F9b/F13, so this
                                                        row may need re-splitting
                                                        again once scoped.
16e      B27 doctor resolved behaviour     Sonnet high   plumbing over two functions
         values                                         that already exist
                                                        (config_value,
                                                        get_override) -- same shape
                                                        as B20/B25/B26. The one real
                                                        discipline is Done-when #4:
                                                        resolved must stay the
                                                        config.toml/fallback value
                                                        even while an override is
                                                        active, or the field is just
                                                        config_value_live() renamed
                                                        -- test that explicitly,
                                                        don't assume it from the
                                                        diff.
16b      F18 windowed history             Opus high
17       F12 workspace lifecycle          Opus high     Suspend/Resume + timers;
                                                        wrong here breaks silently
17b      B24 filler proposal on wire     Sonnet high   transport + validation over a
                                                       prompt that already asks for
                                                       the shape -- the B20 pattern
                                                       again. Escalate only if the
                                                       shown-region validation turns
                                                       out to interact with
                                                       resolve_scope.
18       F13 editor core                  Opus high     largest, subtlest — CRLF,
                                                        undo grouping, wide chars
18b      F9b diff card                   Opus high     was Opus MEDIUM as part of
                                                       F9; raised. The apply path is
                                                       destructive, and its three
                                                       real hazards are each ones
                                                       another episode already paid
                                                       for: guard ORDER (B12 bug 2),
                                                       staleness re-read (B12
                                                       set_mode, F8 chips), and the
                                                       card's state following the
                                                       buffer edit (F8 chips again).
                                                       Debugging those is reasoning,
                                                       not typing.
19       F15 explainer integration        Opus medium   ghost text + apply semantics
20       B15 lifecycle + crash recovery   Opus high     demo-critical, easy to fake
21       B14 usage + limits               Sonnet medium SQL and arithmetic
22       F14 editor terminal              Sonnet high   ExecProcess is a known pattern
23       B13 memory system                Opus high     the design you deferred on
                                                        purpose — decide well
23b      F9c result card attach + save   Sonnet high   two axes over B13's and the
                                                       attachment extension's own
                                                       events, once both exist
24       B16 SSH                          Opus medium   permissions must be right
25       F16 polish                       Sonnet medium audits and checklists
26       F17 web preview                  Sonnet high   small, well specced
27       B17 hardening                    Sonnet high   tests are mechanical

F6 is the cautionary entry. "Mostly wiring events to cards" was a fair reading
of the spec and wrong about the work: the build was a day, the verification gate
was seven rounds. What ran long was not complexity in the cards but three defects
whose causes were each in a different component than the symptom -- a Bubble Tea
cmd-ordering race, a visibility flag that three of five call sites forgot to
update, and a backend context bug that F6 only surfaced. None was predictable
from the spec.

The lesson is not "estimate F6-like episodes higher". It is that **an episode
that touches a shared contract -- the message bus, the layout engine, the
context builder -- carries risk its own Build section cannot show you**, because
the risk lives in what else already depends on that contract. Wiring is cheap;
wiring into something six episodes already rely on is not.

Four rules that matter more than the table:

Fix-up rounds go a level up, not down. If a verification check fails, debugging is reasoning, not typing. A Sonnet episode that fails twice should be re-run on Opus rather than poked at repeatedly — three failed Sonnet rounds costs more than one Opus round.

Never drop below medium effort. Low effort on a spec'd episode produces code that looks right and skips the awkward cases — the guards, the empty states, the error paths. Those are exactly what your verification checks test, so you pay the saving back with interest.

A bug that survives two failed reproductions is a model decision, not just a
debugging one. Two carefully-traced wrong hypotheses means the fault is probably
not where either of you is looking, and more Sonnet rounds in the same place
compound the error. Escalate, or stop theorising and instrument -- see
WORKFLOW.md's rule 7.

Doc-only work is always Sonnet. Writing completed/ files, parking-lot entries, updating an INDEX — Sonnet high is more than enough, and those are frequent.

If your quota gets tight mid-project, the four to protect at Opus are B6, B8, F1+F2, and F13. Everything else has a spec detailed enough that Sonnet plus your verification gate will catch the difference.