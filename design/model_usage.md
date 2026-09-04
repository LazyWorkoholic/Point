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
11b      B18 message pagination           Sonnet high
12       F7  popup framework              Sonnet high   one widget, clear spec
13       F6  right pane                   Sonnet medium mostly wiring events to cards
14       F8  @ picker                     Sonnet high   modes + guards, well defined
15       F9  tray, cards, diff            Opus medium   diff computation + apply paths
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

Three rules that matter more than the table:

Fix-up rounds go a level up, not down. If a verification check fails, debugging is reasoning, not typing. A Sonnet episode that fails twice should be re-run on Opus rather than poked at repeatedly — three failed Sonnet rounds costs more than one Opus round.

Never drop below medium effort. Low effort on a spec'd episode produces code that looks right and skips the awkward cases — the guards, the empty states, the error paths. Those are exactly what your verification checks test, so you pay the saving back with interest.

Doc-only work is always Sonnet. Writing completed/ files, parking-lot entries, updating an INDEX — Sonnet high is more than enough, and those are frequent.

If your quota gets tight mid-project, the four to protect at Opus are B6, B8, F1+F2, and F13. Everything else has a spec detailed enough that Sonnet plus your verification gate will catch the difference.