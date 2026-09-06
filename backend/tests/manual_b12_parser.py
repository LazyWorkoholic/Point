r"""B12 manual check: the input parser's rules, in isolation.

No database, no filesystem, no agent registry -- `parse_invocation()`
takes an `is_agent` predicate precisely so the routing rule can be
checked against a set literal. That is the point of the injection: a
rule that needs I/O to evaluate is a rule that can behave differently
under load, and the ambiguity rule ("an exact agent name wins,
otherwise it is a path") has to hold identically for the CLI, for the
backend, and for F8's picker.

Run from inside `backend\`:

    uv run python tests\manual_b12_parser.py

Exits non-zero if any rule broke, so the gate can branch on it.
"""

import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent.parent))

from managers.invocation import InvocationKind, parse_invocation  # noqa: E402

AGENTS = {"research", "general", "filler-explainer"}


def is_agent(token):
    return token in AGENTS


fails = []


def check(label, got, want):
    ok = got == want
    print(f"  [{'PASS' if ok else 'FAIL'}] {label}\n       got={got!r}\n       want={want!r}"
          if not ok else f"  [PASS] {label}  -> {got!r}")
    if not ok:
        fails.append(label)


def parse(text):
    return parse_invocation(text, is_agent=is_agent)


print("=== routing")
i = parse("what is a monad")
check("plain text is a TURN with no agent", (i.kind, i.agent, i.text),
      (InvocationKind.TURN, None, "what is a monad"))

i = parse("@research what is a monad")
check("@agent is a TASK, agent stripped from text", (i.kind, i.agent, i.text),
      (InvocationKind.TASK, "research", "what is a monad"))

i = parse("@filler-explainer explain this")
check("hyphenated agent name resolves", (i.kind, i.agent), (InvocationKind.TASK, "filler-explainer"))

print("\n=== ambiguity: an exact agent name wins, otherwise it is a path")
i = parse("@research")
check("bare @research is the agent, not a path", (i.agent, [a.raw for a in i.attachments]),
      ("research", []))

i = parse("@research.py explain")
check("@research.py is a PATH, not the research agent",
      (i.agent, [a.raw for a in i.attachments]), (None, ["research.py"]))

i = parse("@notanagent hello")
check("an unknown bare token is neither agent nor path",
      (i.agent, [a.raw for a in i.attachments]), (None, []))

print("\n=== both separators, both accepted")
i = parse("@src/main.py and @src\\other.py what changed")
check("forward and back slashes both parse as paths",
      [a.raw for a in i.attachments], ["src/main.py", "src\\other.py"])

print("\n=== filesystem-mode paths")
for raw, want in [
    ("@C:\\Users\\me\\x.py", True),
    ("@~\\notes.md", True),
    ("@..\\sibling\\a.py", True),
    ("@.\\here.py", True),
    ("@src/inside.py", False),
]:
    i = parse(raw + " look")
    got = i.attachments[0].filesystem if i.attachments else None
    check(f"{raw} filesystem={want}", got, want)

print("\n=== agent + attachment together")
i = parse("@research @src/main.py why is this slow")
check("agent claimed, path kept as an attachment",
      (i.agent, [a.raw for a in i.attachments]), ("research", ["src/main.py"]))
check("the attachment token stays in the text the model sees",
      "src/main.py" in i.text, True)

print("\n=== only the FIRST token can name the agent")
i = parse("@general ask @research about it")
check("a later @agent is prose, not a second route",
      (i.agent, [a.raw for a in i.attachments]), ("general", []))

print("\n=== --advanced")
i = parse("@research dig deeper --advanced")
check("advanced is recorded", i.advanced, True)
check("advanced is removed from the text", "--advanced" not in i.text, True)
check("the rest still routes normally", (i.agent, i.text), ("research", "dig deeper"))

i = parse("use the --advanced-mode flag")
check("--advanced-mode is not --advanced", i.advanced, False)

print("\n=== trailing punctuation")
i = parse("@research, and also this")
check("@research, names research", i.agent, "research")

i = parse("@src/main.py, check it")
check("a trailing comma is stripped from a path too",
      [a.raw for a in i.attachments], ["src/main.py"])

i = parse("@some/dir\\ what is in here")
check("a trailing separator survives (it means 'directory')",
      [a.raw for a in i.attachments], ["some/dir\\"])

print("\n" + ("ALL PARSER CHECKS PASSED" if not fails else "FAILED: " + "; ".join(fails)))
sys.exit(1 if fails else 0)
