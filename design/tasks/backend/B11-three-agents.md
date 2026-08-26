# B11 — The Three Agents

**Phase:** P1 · **Batchable:** no · **Depends on:** B10

## Purpose

Behaviour for the three configs seeded in B9. They must differ **structurally**,
not by prompt — that difference is the proof the architecture does real work.

**Build order inside this episode: Explainer → General → Research.** Explainer
exercises the whole pipeline with the fewest moving parts; Research is the
integration test for everything.

## Build

### Filler-Explainer (one agent, two modes)

`EditorContext`, supplied by the frontend (F11) or synthesized by the CLI:
```python
EditorContext(file, language, cursor, selection, enclosing, imports)
```

**Enclosing-scope extraction** (backend helper, used when the frontend does not
supply it): nearest enclosing `def` / `class` / `func` / brace block by
indentation or brace matching. **No parser.** Cap at ~2k tokens; if the enclosing
scope is larger, fall back to selection ± 20 lines. One highlighted line is
unanswerable without its function.

**Explainer mode** — `reads_project=True`, no writes, no tools. Scope ladder:
selection → enclosing → block → file. Project-wide only on explicit request, and
then as a **file list**, never contents.

**Filler mode** — returns a **rewritten region**. Never a patch, never a file
write. Output contract:
```json
{ "file": "...", "start_line": 12, "end_line": 18,
  "replacement": "...", "note": "..." }
```
Refuses, with a message, if the request would need a second file, a new file, or
a command. That refusal is the boundary against the parked Coding Assistant.

### General Chat
The null agent: no tools, no project reads, no writes, STM only. Its purpose is
to be the daily driver **and the control case** — anything it does well comes
from the Context Builder, not from agent cleverness.

### Research
- `reads_project=True`, `writes_proposals=True`, tools: `web_search`,
  `read_file`, `list_files`, `load_task_result`.
- **Memory-first:** before searching, consult accepted memory records plus a
  reference index of past Research task results, **each with its age**. The model
  then decides: answer from memory, re-research because it is stale, or search.
  `stale_after_days` is config. In v1 memory is mostly empty, so this path is
  mainly exercised by past task results — that is fine and still worth building.
- Always background. Bounded, with `task.progress` carrying `step k/n`.
- Result persisted as a task result; `default_attach_mode = reference`.
- Emits **proposals** (`status=pending`) for facts worth keeping — at most 2–3
  per session, enforced by a counter, **never** by prompt instruction.

### Session title generation
On the first exchange in a session, one cheap call (small model where available)
produces a short title, stored on the session. **Failure is non-fatal** — an
untitled session is fine.

Add CLI support: `send` must accept `--file`, `--select`, `--cursor` so Explainer
and Filler are testable before any editor exists.

## Do NOT

- Give the Filler file-write or command capability, in any form.
- Implement retrieval ranking, summarization, or promotion logic (B13).
- Add Blog Writer, Trading, or Data agents.
- Build the result-card UI (F9) — this episode ends at events and stored results.

## Done when

1. Each agent produces a structurally different context for the same input,
   visible in the B8 log (tools present/absent, project reads, window size).
2. Explainer: a one-line selection inside a function yields a context containing
   the **whole enclosing function**.
3. Filler: returns a region whose line numbers map onto the real file, and
   **writes nothing** — proven by mtime.
4. Filler: a two-file request is refused with a message naming the limitation.
5. Research: runs in the background, emits `step k/n`, respects its bounds, and
   produces a persisted result plus **at most 3** pending proposals.
6. Research: on a repeat question, the log shows it consulted memory/past results
   before searching.
7. A new session gets a generated title; a killed title call leaves the session
   usable.

## Manual verification (PowerShell)

```powershell
cd D:\Projects\Point\backend

# set up a file to work on
@"
def parse_config(path):
    with open(path) as f:
        return json.load(f)
"@ | Set-Content "$env:TEMP\proj\main.py"
```

```powershell
# 2. Explainer sends the enclosing function, not one line
uv run python cli.py send "@filler-explainer explain" --file "$env:TEMP\proj\main.py" --select 2
uv run python cli.py context report --last
```
**Expect:** the context contains all three lines of `parse_config`, not just
line 2.

```powershell
# 3. Filler writes NOTHING
$before = (Get-Item "$env:TEMP\proj\main.py").LastWriteTime
uv run python cli.py send "@filler-explainer add error handling for a missing file" --file "$env:TEMP\proj\main.py" --cursor 1
$after = (Get-Item "$env:TEMP\proj\main.py").LastWriteTime
$before -eq $after
```
**Expect:** a JSON region in the response, and `True` printed.
**Fails if:** `False` — the agent wrote to disk, which must never happen.

```powershell
# 4. two-file refusal
uv run python cli.py send "@filler-explainer refactor this across main.py and utils.py" --file "$env:TEMP\proj\main.py"
```
**Expect:** an explicit refusal naming the one-file limit — as a normal response,
not an error.

```powershell
# 5. Research
uv run python cli.py send "@research what is a rope data structure" --wait
uv run python cli.py memory list --status pending
uv run python cli.py tasks list
```
**Expect:** a completed task with a stored result, `step k/n` in the event log,
and **≤3** pending proposals.

```powershell
# 6. memory-first on a repeat
uv run python cli.py send "@research what is a rope data structure" --wait
```
**Expect:** the log shows it consulted stored results first, and it either
answers from them or states they are stale.

```powershell
# 7. title
uv run python cli.py session list
```
**Expect:** the session has a short generated title, not a UUID.

## Handoff

State after this episode: three agents behave differently for structural reasons,
nothing writes to disk, and Research produces reviewable proposals that nothing
yet reviews (B13 + F11).

Record in `completed/`: the enclosing-scope heuristic's known failure cases —
you will hit them, and knowing them is worth more than pretending it is a parser.

Do not proceed until every check above passes.
