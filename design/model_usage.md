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
14       F8  @ picker                     Sonnet high   modes + guards, well defined
15       F9  tray, cards, diff            Opus medium   diff computation + apply paths
15b      B19 Silence-Based Agent Timeout
16       F10 settings, first run          Sonnet high   forms and flows
16b      F18 windowed history             Opus high
17       F12 workspace lifecycle          Opus high     Suspend/Resume + timers;
                                                        wrong here breaks silently
18       F13 editor core                  Opus high     largest, subtlest — CRLF,
                                                        undo grouping, wide chars
19       F15 explainer integration        Opus medium   ghost text + apply semantics
20       B15 lifecycle + crash recovery   Opus high     demo-critical, easy to fake
21       B14 usage + limits               Sonnet medium SQL and arithmetic
22       F14 editor terminal              Sonnet high   ExecProcess is a known pattern
23       B13 memory system                Opus high     the design you deferred on
                                                        purpose — decide well
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