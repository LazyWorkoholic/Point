SESSION  EPISODE                                                     MODEL                                   STATUS
1        B6  schema + envelope v1                                    Opus high                               done
2        B7  project system                                          Sonnet high                             done
3        B8  context builder                                         Opus high                               done
4        B9  agent foundation                                        Sonnet high                             done
5        B10 tool system                                             Opus medium                             done
6        B11 three agents                                            Sonnet high                             done
7        B12 routing, caps                                           Opus medium                             done
8        F1+F2 bootstrap + client                                    Opus high                               done
9        F3  chat viewport                                           Sonnet high                             done
10       F4  query input                                             Sonnet high                             done
11       F5  streaming, queue, cancel                                Opus medium                             done
11b      F5 re-verifying after B12                                   Opus medium                             done
11c      B18 message pagination                                      Sonnet high                             done
12       F7  popup framework                                         Sonnet high                             done
13       F6  right pane                                              Sonnet medium                           done
13b      B21 orphaned message context                                Sonnet high                             done
13c      B20 context.report on the wire                              Sonnet high                             done
13d      S13d wire the context card                                  Sonnet 5, switched to Opus 5.5 partway  done
13f      B23 agent + directory listing on the wire                   Sonnet high                             done
14       F8  @ picker                                                Sonnet high                             done
15       F9a tray + result cards                                     Opus 5                                  done
15b      B19 Silence-Based Agent Timeout                             (not recorded)                          done
15c      B25 persist provider key                                    Sonnet high                             done
16       F10 settings, first run                                     Sonnet high                             done
16a      B26 behaviour overrides + doctor wire (backend)             Sonnet high                             done
16c      F10b behaviour overrides + doctor in About                  Sonnet high                             done
16f      B28 demo mode provider + fixtures (backend)                 Sonnet high                             done
16d      F10c demo mode                                              Sonnet high                             done
--       B29 remove legacy runtime.db copy-forward                   Sonnet high                             done
16g      B30 set session project (via SetSessionProvider)            Sonnet high                             done
16h      B31 validate project_id (CreateSession/SetSessionProvider)  Sonnet high                             done
16e      B27 doctor resolved behaviour values                        Sonnet high                             done
16b      F18 windowed history                                        Opus high                               done
17       F12 workspace lifecycle                                     Opus high                               done
17b      B24 filler proposal on wire                                 Sonnet high                             done
17c      B32 WriteProjectFile (the editor's save path)               Sonnet high                             done
18a      F13a editor core: editing half (F13, SPLIT)                 Opus high                               done
18d      F9b diff card                                               Opus high                               done
18d      (F9b, as first planned)                                     Opus high                               superseded
18b      F13b editor save + locks                                    Opus high                               done
18b      (F13b, as first planned) (F13, SPLIT)                       Opus high                               superseded
(18)     (F13, as first planned)                                     Opus high                               superseded
18e      B33 transport is UTF-8                                      Sonnet high                             done
18g      B35 CLAUDE.md corruption + two stray UTF-16LE files         Sonnet high                             done
18f      B34 line-ending policy                                      Sonnet high                             done
19a      B36 generation-time staleness                               Sonnet high                             done
19       F15 explainer integration                                   Opus medium                             done
19b      F9d apply-time guard order                                  Opus medium                             done
20       (B15, as first estimated)                                   Opus high                               superseded
20       B15 lifecycle + crash recovery                              Opus 5                                  done
20b      B40 durable event log                                       Opus medium                             done
21       B14 usage + limits                                          Sonnet medium                           not started
22       F14 editor terminal                                         Sonnet high                             not started
22b      F20 live stale detection                                    Opus medium                             not started
23       B13 memory system                                           Opus high                               not started
23b      F9c result card attach + save                               Sonnet high                             not started
24       B16 SSH                                                     Opus medium                             not started
25       F16 polish                                                  Sonnet medium                           not started
26       F17 web preview                                             Sonnet high                             not started
27       B17 hardening                                               Sonnet high                             not started
28       F21 multi-line fill prompt                                  Sonnet high                             not started
29       F22 explain as conversation                                 Opus medium                             not started
30       F23 fill on a blank line                                    Opus high                               not started

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

**A split episode gets one row per half, not one row with two estimates.** F13
is the worked example: `18a` spent Opus high on F13a and `18b` carries its own
Opus high for F13b, because the two halves have different risk (editing
correctness versus bytes on disk and another process's lock) and the spend on
one says nothing about the other. The row for the original combined episode is
kept below them, labelled `(18)` because the plain number is retired, rather
than deleted — it records the pass that stopped before
writing code, which is itself a data point about estimating. See WORKFLOW.md's
"Splitting an episode".

**An episode that turns out to be two polish items does not need a
number.** F19 was written up as its own episode (keymap docs, a one-key
close, a scroll bound), and retired three prompts later: two items went
into `F16-polish.md` where small polish items already live, and the
third was reverted and deferred there. The cost of the detour was two
docs and two INDEX rows, which is cheap -- but the lesson is that
"found during testing, not on the Done-when list" argues for the
polish bucket first and a new number only if the work is structural.
B33, by contrast, earned its number: a transport fix with its own
permanent test is not polish.

If your quota gets tight mid-project, the four to protect at Opus are B6, B8, F1+F2, and F13. Everything else has a spec detailed enough that Sonnet plus your verification gate will catch the difference.