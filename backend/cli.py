r"""The backend's own command line, so every later episode can be
verified without Go (B6 14).

    uv run python cli.py doctor [--via-wire]
    uv run python cli.py behaviour set <key> <value>
    uv run python cli.py behaviour clear <key>
    uv run python cli.py migrate
    uv run python cli.py session new [title]
    uv run python cli.py session list
    uv run python cli.py send "count to five" [--cancel-after 2]
    uv run python cli.py send "@filler-explainer explain" --file <path> --select 12
    uv run python cli.py send "@research <question>" --wait
    uv run python cli.py tasks list [--scope all|active]
    uv run python cli.py task cancel <task-id> | --last
    uv run python cli.py context report [--session ID | --last] [--json]
    uv run python cli.py memory list --status pending
    uv run python cli.py attach <path> [--mode full|reference|detached] [--preview]
    uv run python cli.py detach <attachment-id>
    uv run python cli.py set-mode <attachment-id> reference
    uv run python cli.py attachments list
    uv run python cli.py agents list [--wire]
    uv run python cli.py project browse <path> [--project ID] [--cwd DIR]

Run from inside `backend\`, like everything else here.

**How a CLI talks to Runtime.** There is no request/response method:
`receive(raw)` returns None and every result leaves through
`runtime.outbox` as an envelope carrying `corr_id`. So one helper --
`call()` -- does all the work, and three constraints shape it:

  * Draining is mandatory. The outbox is bounded at 1000 and `_emit`
    *awaits* `.put()`, so a CLI that builds a Runtime and never drains
    eventually blocks the producer. Non-matching envelopes are discarded,
    or printed under --verbose.
  * `receive()` never raises to the caller. Errors come back as `Error`
    envelopes. Exit codes derive from the event type, never from a
    caught exception.
  * `send` is asynchronous: the handler returns after TaskStarted, and
    the reply arrives later as TextDelta* then TaskDone. So `send` keeps
    draining past its own ack until it sees the terminal event for that
    task_id -- which is 7's "exactly one terminal event" being cashed in.

This file never reads stdin and never writes JSON lines to stdout.
"""

from __future__ import annotations

import argparse
import asyncio
import json
import os
import sys
import time
import uuid
from pathlib import Path
from typing import Any, Callable, Optional

import appconfig
import appconfig_overrides
from bootstrap import (
    AlreadyRunningError,
    LEGACY_DB_PATH,
    SCHEMA_VERSION,
    build_runtime,
    current_version,
    resolve_db_path,
    run_migrations,
    shutdown_runtime,
)
from logging_setup import configure_logging, get_logger
from managers import context
from managers.editor_context import EditorContext, MODE_EXPLAIN, MODE_FILL
from managers.message import Message, MessageRole
from providers.mock import provider as mock_provider
from providers.models import ProviderConfig, ProviderHealthStatus
from runtime.errors import (
    AgentNotFoundError,
    BuiltinAgentImmutableError,
    ContextOverflowError,
    RuntimeException,
)
from runtime.events import ENVELOPE_VERSION
from runtime.runtime import Runtime

_log = get_logger("cli")

# Terminal reply per request type: what `call()` waits for before it
# stops draining. Anything not listed falls back to the timeout.
_EXPECTED_REPLY: dict[str, set[str]] = {
    "CreateSession": {"SessionCreated"},
    "ListSessions": {"SessionList"},
    "GetSession": {"Session"},
    "SessionMessages": {"SessionMessageList"},
    "ListTasks": {"TaskList"},
    "CancelTask": {"task.cancel.accepted"},
    "SetSessionProvider": {"SessionProviderUpdated"},
    "ProjectInit": {"Project"},
    "ProjectResolve": {"Project"},
    "ListProjectFiles": {"ProjectFileList"},
    "ReadProjectFile": {"ProjectFile"},
    "AcquireFileLock": {"FileLockResult"},
    "HeartbeatFileLock": {"FileLockResult"},
    "ReleaseFileLock": {"FileLockReleased"},
    "TakeOverFileLock": {"FileLockResult"},
    "CheckFileStale": {"FileStaleResult"},
    "AttachContext": {"Attachment"},
    "ResolveAttachment": {"AttachmentResolution"},
    "DetachContext": {"AttachmentDetached"},
    "SetAttachmentMode": {"Attachment"},
    "ListAttachments": {"AttachmentList"},
    "GetContextReport": {"ContextReport"},
    "ListAgents": {"AgentList"},
    "ListDirectory": {"DirectoryList"},
    "GetDoctorReport": {"DoctorReport"},
    "SetBehaviourOverride": {"BehaviourOverrideSet"},
}

_TASK_TERMINAL = {"TaskDone", "TaskFailed", "TaskCancelled"}

EXIT_OK = 0
EXIT_FAILED = 1


# ---------------------------------------------------------------------------
# The one helper
# ---------------------------------------------------------------------------

async def call(
    runtime: Runtime,
    type: str,
    timeout: float = 30.0,
    *,
    until: Optional[Callable[[dict[str, Any]], bool]] = None,
    match: Optional[Callable[[dict[str, Any]], bool]] = None,
    on_event: Optional[Callable[[dict[str, Any]], None]] = None,
    verbose: bool = False,
    **payload: Any,
) -> list[dict[str, Any]]:
    """Send one envelope, drain the outbox, return the envelopes that
    belonged to it."""
    request_id = str(uuid.uuid4())
    await runtime.receive({
        "v": ENVELOPE_VERSION,
        "id": request_id,
        "type": type,
        "payload": payload,
    })

    expected = _EXPECTED_REPLY.get(type, set()) | {"Error"}
    if until is None:
        def until(envelope: dict[str, Any]) -> bool:
            return envelope["type"] in expected
    if match is None:
        def match(envelope: dict[str, Any]) -> bool:
            return envelope.get("corr_id") == request_id

    collected: list[dict[str, Any]] = []
    loop = asyncio.get_running_loop()
    deadline = loop.time() + timeout

    while True:
        remaining = deadline - loop.time()
        if remaining <= 0:
            _log.warning("Timed out after %.1fs waiting for a reply to %s", timeout, type)
            break
        try:
            event = await asyncio.wait_for(runtime.outbox.get(), timeout=remaining)
        except asyncio.TimeoutError:
            _log.warning("Timed out after %.1fs waiting for a reply to %s", timeout, type)
            break

        envelope = event.to_dict()
        if not match(envelope):
            # Discarding is not optional: the producer awaits put() on a
            # bounded queue, so anything we refuse to take we eventually
            # block on.
            if verbose:
                print(f"  . {envelope['type']} (corr_id {envelope['corr_id']})", file=sys.stderr)
            continue

        collected.append(envelope)
        if on_event is not None:
            on_event(envelope)
        if until(envelope):
            break

    return collected


def _failed(envelopes: list[dict[str, Any]]) -> bool:
    return any(e["type"] in ("Error", "TaskFailed") for e in envelopes)


def _print_errors(envelopes: list[dict[str, Any]]) -> None:
    for envelope in envelopes:
        if envelope["type"] in ("Error", "TaskFailed"):
            payload = envelope["payload"]
            code = payload.get("code")
            tag = f"[{code}] " if code else ""
            print(f"error: {tag}{payload.get('error', 'unknown error')}", file=sys.stderr)


# ---------------------------------------------------------------------------
# Provider default for the CLI
# ---------------------------------------------------------------------------

async def default_provider(runtime: Runtime) -> tuple[str, str]:
    """ollama if it is registered *and* answering, else echo. Never
    claude: CreateSession's old default resolved through _PROVIDER_ALIASES
    to anthropic, which does not register at all on a keyless profile --
    exactly the profile these checks run on.

    `echo` is the existing no-network _demo_echo_body path and is
    deliberately not a registered provider, so it always works.
    """
    if "ollama" in runtime.providers.list():
        provider = runtime.providers.get("ollama")
        try:
            health = await provider.health()
            if health.status == ProviderHealthStatus.HEALTHY:
                models = await provider.list_models()
                if models:
                    return "ollama", models[0].id
        except Exception as exc:  # noqa: BLE001
            _log.debug("Ollama unavailable, falling back to echo: %s", exc)
    return "echo", "echo"


# ---------------------------------------------------------------------------
# Commands
# ---------------------------------------------------------------------------


# B10's `--script <name>`: a name -> fixture mapping over the env var
# MockProvider already reads (`POINT_MOCK_FIXTURE`). Deliberately not a
# new provider switch -- the mock is selected by POINT_MOCK=1 exactly as
# it was in B6, and this only chooses which script it replays.
#
# Named rather than free-path so the verification steps in the episode
# read as what they test, and so a fixture rename is one edit here rather
# than five in a document.
SCRIPT_DIR = Path(__file__).resolve().parent / "tests" / "fixtures"


def script_fixture(name: str) -> Path:
    return SCRIPT_DIR / f"{name}.json"


def available_scripts() -> list[str]:
    if not SCRIPT_DIR.is_dir():
        return []
    return sorted(p.stem for p in SCRIPT_DIR.glob("*.json"))


# ---------------------------------------------------------------------------
# EditorContext + @agent (B11) -- what stands in for a real editor/picker
# until F11/F8 exist. `send` and `context report` both accept the same
# --file/--select/--cursor/--mode flags for exactly that reason: an
# EditorContext is per-turn, not session state, so projecting "what
# would the next turn cost" needs it supplied again, not remembered.
# ---------------------------------------------------------------------------

# B12 deleted `_strip_agent_prefix`. The CLI used to pull `@agent` off
# the front of the text itself and send the remainder, which meant the
# ambiguity rule ("an exact agent name wins, otherwise it is a path")
# lived in two places -- here and, eventually, in F8's picker. It now
# lives in exactly one: `managers\\invocation.py`, called by
# `Runtime._handle_send_message`. The CLI sends what the user typed,
# verbatim, and lets the backend route it. `--agent` below is still
# accepted as an explicit override, and an explicit override still wins
# over anything parsed out of the text.


def _resolve_mode(args: argparse.Namespace) -> str:
    """explain if --select alone; fill if --cursor alone; --mode always
    wins. Both --select and --cursor with no --mode is refused rather
    than guessed -- see this episode's Handoff (B11-three-agents.md) for
    the exact rule, which F15 must read from there rather than
    reinventing its own when the real editor lands.
    """
    if args.mode:
        return args.mode
    if args.select is not None and args.cursor is not None:
        raise ValueError(
            "--select and --cursor were both given -- pass --mode explain|fill "
            "to say which one this is"
        )
    if args.select is not None:
        return MODE_EXPLAIN
    if args.cursor is not None:
        return MODE_FILL
    raise ValueError("--file needs --select, --cursor, or --mode to pick explain vs fill")


def _parse_line_range(value: str) -> tuple[int, int]:
    if "-" in value:
        start_s, end_s = value.split("-", 1)
        return int(start_s), int(end_s)
    line = int(value)
    return line, line


def _add_editor_flags(parser: argparse.ArgumentParser) -> None:
    """Shared by `send` and `context report` -- see the module note
    above on why both need the same flags rather than one remembering
    what the other sent."""
    parser.add_argument(
        "--file", default=None,
        help="EditorContext (B11): the file being explained/filled",
    )
    parser.add_argument(
        "--select", default=None, metavar="LINE|START-END",
        help="EditorContext: selected line(s), 1-based",
    )
    parser.add_argument(
        "--cursor", default=None, metavar="LINE",
        help="EditorContext: cursor line, 1-based",
    )
    parser.add_argument(
        "--mode", default=None, choices=[MODE_EXPLAIN, MODE_FILL],
        help="override the explain/fill inference from --select vs --cursor",
    )


def _build_editor_context(args: argparse.Namespace) -> Optional[EditorContext]:
    """None when --file was not given -- everything else here is only
    reachable through --file, so "no editor context" is the common case
    for `general`/`research` sends. Raises ValueError on an
    unresolvable mode; callers turn that into `error: ...` and exit 1,
    the same as every other CLI-side validation failure."""
    if not args.file:
        return None
    mode = _resolve_mode(args)
    selection = _parse_line_range(args.select) if args.select is not None else None
    cursor = int(args.cursor) if args.cursor is not None else None
    return EditorContext(file=args.file, mode=mode, cursor=cursor, selection=selection)


async def cmd_session_new(runtime: Runtime, args: argparse.Namespace) -> int:
    provider, model = args.provider, args.model
    if not provider:
        provider, model = await default_provider(runtime)
    elif not model:
        model = provider

    # --agent is stored in settings, exactly like the v0 stm_window/
    # system_prompt overrides B8 read from there before B9 existed --
    # no schema change needed for "which agent does this session use".
    # AgentRegistry.resolve_for_session() is what reads it back.
    if args.agent:
        try:
            await runtime.agents.get(args.agent)
        except AgentNotFoundError as exc:
            print(f"error: {exc}", file=sys.stderr)
            return EXIT_FAILED

    # B10: a session's project is what gives read_file/list_files a root
    # to be guarded against. CreateSession has carried `project_id` since
    # B7; nothing on the CLI could set it until a tool needed one.
    #
    # B12: and it is now resolved from the working directory by default,
    # rather than only when you happen to know the uuid. Every
    # attachment is guarded against `session.project_id`, so a session
    # created inside a project but not bound to it could not attach a
    # single file from that project -- the failure looked like "no
    # project is open" while standing in one. `--project` still wins;
    # `--no-project` opts out.
    project_id = args.project_id
    if project_id is None and not args.no_project:
        found = await runtime.projects.resolve(args.path)
        if found is not None:
            project_id = found.id

    envelopes = await call(
        runtime, "CreateSession", verbose=args.verbose,
        title=args.title, provider=provider, model=model,
        project_id=project_id,
        settings={"agent_id": args.agent} if args.agent else None,
    )
    _print_errors(envelopes)
    if _failed(envelopes) or not envelopes:
        return EXIT_FAILED
    session = envelopes[-1]["payload"]["session"]
    agent_note = f"  agent={args.agent}" if args.agent else ""
    project_note = f"  project={project_id}" if project_id else ""
    print(
        f"{session['id']}  {session['title']}  "
        f"[{session['provider']}/{session['model']}]{agent_note}{project_note}"
    )
    return EXIT_OK


async def cmd_session_list(runtime: Runtime, args: argparse.Namespace) -> int:
    envelopes = await call(
        runtime, "ListSessions", verbose=args.verbose,
        include_archived=args.all,
    )
    _print_errors(envelopes)
    if _failed(envelopes) or not envelopes:
        return EXIT_FAILED
    sessions = envelopes[-1]["payload"]["sessions"]
    if not sessions:
        print("(no sessions)")
        return EXIT_OK
    for session in sessions:
        provider = session["provider"] or "-"
        model = session["model"] or "-"
        print(
            f"{session['id']}  {_ts(session['updated_at'])}  "
            f"{provider}/{model:<24}  {session['title']}"
        )
    print(f"\n{len(sessions)} session(s)")
    return EXIT_OK


async def cmd_session_messages(runtime: Runtime, args: argparse.Namespace) -> int:
    """B18's keyset scrollback, exposed for verification with no Go
    attached (design/tasks/backend/B18-message-pagination.md). `--before`
    and `--after` are seq cursors, not row indices; neither given means
    "the newest --limit messages"."""
    if args.before is not None and args.after is not None:
        print("error: pass at most one of --before/--after", file=sys.stderr)
        return EXIT_FAILED

    envelopes = await call(
        runtime, "SessionMessages", verbose=args.verbose,
        session_id=args.session_id, before=args.before, after=args.after, limit=args.limit,
    )
    _print_errors(envelopes)
    if _failed(envelopes) or not envelopes:
        return EXIT_FAILED

    payload = envelopes[-1]["payload"]
    rows = payload["messages"]
    for m in rows:
        preview = (m.get("content") or {}).get("text") or ""
        preview = preview.replace("\n", " ")[:60]
        print(f"seq={m['seq']:<8}{m['role']:<10}{preview}")
    print(
        f"\n{len(rows)} message(s)  "
        f"has_more_before={payload['has_more_before']}  has_more_after={payload['has_more_after']}"
    )
    return EXIT_OK


async def cmd_seed_session(runtime: Runtime, args: argparse.Namespace) -> int:
    """F3's own fixture generator (frontend\\...\\F03-chat-viewport.md): a
    10,000-message session to open and scroll, without spending real
    provider calls or agent turns on it. Messages alternate user/assistant
    and are appended directly through SessionManager -- the same path
    real turns use, just without a Runtime dispatch or a provider in
    between.

    Not wired through call()/the outbox: this never needs to round-trip
    through an envelope, so it talks to the manager layer directly, same
    as `doctor`/`migrate`.
    """
    if args.session:
        session = await runtime.sessions.get(args.session)
    else:
        provider, model = await default_provider(runtime)
        session = await runtime.sessions.create(
            title=args.title or f"seed ({args.messages} messages)",
            provider=provider, model=model,
        )
        print(f"[session {session.id}  {provider}/{model}]")

    for i in range(args.messages):
        role = MessageRole.USER if i % 2 == 0 else MessageRole.ASSISTANT
        text = (
            f"seed message {i + 1} of {args.messages} -- {role.value} filler "
            "text for scrollback and render testing."
        )
        await runtime.sessions.append_message(Message.new(session.id, role, text=text))
        if (i + 1) % 1000 == 0:
            print(f"  {i + 1}/{args.messages}", file=sys.stderr)

    print(f"seeded {args.messages} message(s) into session {session.id}")
    return EXIT_OK


async def cmd_send(runtime: Runtime, args: argparse.Namespace) -> int:
    # Sent verbatim. `@research ...`, `@C:\\path\\file.py`, and
    # `--advanced` are all the backend's to parse and, in the last
    # case, to refuse -- see the note where `_strip_agent_prefix` used
    # to be.
    text = args.text
    agent_override = args.agent
    try:
        editor_context = _build_editor_context(args)
    except ValueError as exc:
        print(f"error: {exc}", file=sys.stderr)
        return EXIT_FAILED

    session_id = args.session
    if not session_id:
        provider, model = await default_provider(runtime)
        created = await call(
            runtime, "CreateSession", verbose=args.verbose,
            title=text[:40] or "cli", provider=provider, model=model,
        )
        _print_errors(created)
        if _failed(created) or not created:
            return EXIT_FAILED
        session_id = created[-1]["payload"]["session"]["id"]
        print(f"[session {session_id}  {provider}/{model}]")

    state: dict[str, Any] = {"task_id": None, "cancel_scheduled": False}

    def on_event(envelope: dict[str, Any]) -> None:
        kind = envelope["type"]
        payload = envelope["payload"]
        if kind == "TaskStarted":
            state["task_id"] = envelope["task_id"]
            state["background"] = bool(payload.get("background"))
            if state["background"] and not args.wait:
                print(
                    f"[task {envelope['task_id']} started -- background; "
                    "'tasks list' to check progress, or re-run with --wait to block]"
                )
            if args.cancel_after and not state["cancel_scheduled"]:
                state["cancel_scheduled"] = True
                asyncio.get_running_loop().create_task(
                    _cancel_after(runtime, envelope["task_id"], args.cancel_after)
                )
        elif kind == "TextDelta":
            sys.stdout.write(payload.get("data", {}).get("text", ""))
            sys.stdout.flush()
        elif kind == "TaskToken":
            sys.stdout.write(payload.get("text", ""))
            sys.stdout.flush()
        elif kind == "ProgressUpdated":
            print(f"\n[progress: {payload.get('data', {}).get('message', '')}]")
        elif kind == "ToolStarted":
            data = payload.get("data", {})
            print(f"\n[tool.started {data.get('tool')} {data.get('arguments', {})}]")
        elif kind == "ToolCompleted":
            data = payload.get("data", {})
            outcome = "ok" if data.get("ok") else f"failed: {data.get('error')}"
            print(f"[tool.completed {data.get('tool')} {data.get('duration_ms')}ms {outcome}]")
        elif kind == "task.cancel.accepted":
            print(f"\n[cancel accepted: {payload.get('accepted')}]")
        elif kind == "TaskFailed":
            code = payload.get("code") or "-"
            print(f"\n[TaskFailed code={code}] {payload.get('error', '')}")
        elif kind in _TASK_TERMINAL:
            print(f"\n[{kind}]")

    def match(envelope: dict[str, Any]) -> bool:
        # Past our own ack: anything belonging to our task, whoever asked
        # for it. The cancel ack is caused by a *different* request, and
        # not seeing it is how you end up unable to tell one terminal
        # event from two.
        return (
            envelope.get("task_id") is not None
            and envelope["task_id"] == state["task_id"]
        ) or envelope["type"] == "Error"

    def until(envelope: dict[str, Any]) -> bool:
        # B11: a background task's own TaskStarted carries
        # `background: true`. Without --wait, that alone is enough to
        # stop draining -- the send is fire-and-forget, and the caller
        # checks `tasks list` for progress. Absent/false (every inline
        # agent, unchanged) always falls through to the terminal check
        # below, exactly as before this episode.
        if (
            envelope["type"] == "TaskStarted"
            and not args.wait
            and bool(envelope["payload"].get("background"))
        ):
            return True
        return envelope["type"] in _TASK_TERMINAL or envelope["type"] == "Error"

    # BoundsOverride's wire form. Omitted entirely when no flag was
    # given, so a normal send carries no bounds key at all and the
    # agent's own config is the only thing that decides.
    bounds = {
        key: value
        for key, value in (
            ("max_steps", args.max_steps),
            ("max_tool_calls", args.max_tool_calls),
            ("timeout_s", args.max_seconds),
        )
        if value is not None
    }

    extra: dict[str, Any] = {}
    if bounds:
        extra["bounds"] = bounds
    if agent_override:
        extra["agent"] = agent_override
    if editor_context is not None:
        extra["editor_context"] = editor_context.to_dict()

    envelopes = await call(
        runtime, "SendMessage", timeout=args.timeout, verbose=args.verbose,
        until=until, match=_first_ack_then(match), on_event=on_event,
        session=session_id, text=text, **extra,
    )
    _print_errors(envelopes)
    return EXIT_FAILED if _failed(envelopes) else EXIT_OK


def _first_ack_then(match: Callable[[dict[str, Any]], bool]) -> Callable[[dict[str, Any]], bool]:
    """TaskStarted is what tells us our task_id, and it arrives before we
    know it -- so accept anything with a task_id until one is claimed."""
    claimed: dict[str, bool] = {"yes": False}

    def wrapped(envelope: dict[str, Any]) -> bool:
        if not claimed["yes"]:
            if envelope["type"] in ("TaskStarted", "Error"):
                claimed["yes"] = True
                return True
            return False
        return match(envelope)

    return wrapped


async def _cancel_after(runtime: Runtime, task_id: str, delay: float) -> None:
    """Fires CancelTask without draining: exactly one drain loop may run
    at a time, or two of them race for the same envelopes."""
    await asyncio.sleep(delay)
    await runtime.receive({
        "v": ENVELOPE_VERSION,
        "id": str(uuid.uuid4()),
        "type": "CancelTask",
        "payload": {"task_id": task_id},
    })


async def cmd_context_report(runtime: Runtime, args: argparse.Namespace) -> int:
    """The context breakdown for a session's *next* turn.

    Goes straight to the builder rather than through an event: this is a
    read-only projection with no task, no stream, and no terminal event
    to wait for, so wrapping it in the request/response machinery would
    buy nothing but latency.

    `--via-wire` (B20) takes the other path on purpose -- GetContextReport,
    drained through the same `call()` every other event-backed command
    uses -- so this command can also serve as the check that the wire
    reply and this command's own direct numbers agree field for field.
    """
    if args.via_wire:
        payload: dict[str, Any] = {}
        if args.session:
            payload["session_id"] = args.session
        envelopes = await call(runtime, "GetContextReport", **payload)
        _print_errors(envelopes)
        if _failed(envelopes):
            return EXIT_FAILED
        report_envelope = next(e for e in envelopes if e["type"] == "ContextReport")
        print(json.dumps(report_envelope["payload"], indent=2))
        return EXIT_OK

    try:
        editor_context = _build_editor_context(args)
    except ValueError as exc:
        print(f"error: {exc}", file=sys.stderr)
        return EXIT_FAILED

    session_id = args.session
    if not session_id:
        session = await runtime.memory.latest_session()
        if session is None:
            print("(no sessions)", file=sys.stderr)
            return EXIT_FAILED
        session_id = session.id
    else:
        session = await runtime.memory.session(session_id)
        if session is None:
            print(f"error: No session with id {session_id!r}", file=sys.stderr)
            return EXIT_FAILED

    # B9: the report is shaped by the session's own agent (its
    # stm_window, system_prompt, default_attach_mode), not a fixed
    # policy -- resolve_for_session() is the same lookup SendMessage
    # uses.
    agent = await runtime.agents.resolve_for_session(session)

    try:
        report = await context.report(
            session_id, runtime.memory, policy=agent.to_context_policy(),
            editor_context=editor_context,
        )
    except ContextOverflowError as exc:
        # Reachable without sending anything: a system prompt or an
        # attachment can exceed the budget on its own.
        print(str(exc), file=sys.stderr)
        return EXIT_FAILED
    except ValueError as exc:
        print(str(exc), file=sys.stderr)
        return EXIT_FAILED

    if args.json:
        print(json.dumps(report.to_dict(), indent=2))
    else:
        print(report.describe())
    return EXIT_OK


async def _session_for(runtime: Runtime, args: argparse.Namespace) -> Optional[str]:
    """The session an attachment command acts on: `--session`, else the
    most recently updated one.

    Defaulting rather than requiring the flag, because attachments are a
    thing you do to the session you are already talking to, and B2
    already orders `session list` by `updated_at DESC` for exactly this
    reason. Prints a clear error rather than creating a session: an
    `attach` that silently invents a session attaches the file to
    something the user will never look at.
    """
    if args.session:
        return args.session
    sessions = await runtime.sessions.list_all()
    if not sessions:
        print(
            "error: no sessions exist -- run 'session new' or pass --session",
            file=sys.stderr,
        )
        return None
    return sessions[0].id


async def cmd_attach(runtime: Runtime, args: argparse.Namespace) -> int:
    session_id = await _session_for(runtime, args)
    if session_id is None:
        return EXIT_FAILED

    if args.preview:
        # Making good on whatever the refusal offered: the first 200
        # lines of an oversized file, or a listing for a directory. A
        # refusal that offers a fallback nobody can accept is a refusal
        # with a decoration on it.
        #
        # Resolved under the session's own scope, the same as the attach
        # it is a fallback for -- previewing a file you would not be
        # allowed to attach would make this a way around the guard.
        project, allow_outside = await runtime._attachment_scope(session_id)
        try:
            print(runtime.attachments.preview(
                args.file, project=project, allow_outside_project=allow_outside,
            ))
        except (OSError, RuntimeException) as exc:
            print(f"error: {exc}", file=sys.stderr)
            return EXIT_FAILED
        return EXIT_OK

    extra: dict[str, Any] = {}
    if args.mode:
        extra["mode"] = args.mode
    envelopes = await call(
        runtime, "AttachContext", verbose=args.verbose,
        session=session_id, path=args.file, **extra,
    )
    _print_errors(envelopes)
    if _failed(envelopes) or not envelopes:
        _print_offer(envelopes)
        return EXIT_FAILED

    payload = envelopes[-1]["payload"]
    attachment = payload["attachment"]
    resolution = payload["resolution"]
    print(f"attached  {attachment['id']}")
    print(f"  path    {resolution['path']}")
    print(f"  mode    {attachment['mode']}")
    print(f"  size    {resolution['size']} bytes")
    print(f"  tokens  ~{resolution['tokens_estimate']} (estimate)")
    if resolution["outside_project"]:
        print("  note    outside the project root")
    return EXIT_OK


def _print_offer(envelopes: list[dict[str, Any]]) -> None:
    """A refusal names its fallback and how to take it.

    The text comes from the error itself, not from a sentence written
    here: the guard knows whether the fallback is "the first 200 lines",
    "a directory listing", or "bind this session to a project", and a
    hint hard-coded in the CLI drifts from it the moment a flag changes.
    """
    for envelope in envelopes:
        if envelope["type"] != "Error":
            continue
        payload = envelope["payload"]
        if payload.get("code") != "attachment_refused":
            continue
        offer = payload.get("offer")
        if offer:
            print(f"hint: {offer}", file=sys.stderr)
        if any(word in str(offer) for word in ("200 lines", "listing")):
            print("      take it with: attach <path> --preview", file=sys.stderr)


async def cmd_detach(runtime: Runtime, args: argparse.Namespace) -> int:
    session_id = await _session_for(runtime, args)
    if session_id is None:
        return EXIT_FAILED
    envelopes = await call(
        runtime, "DetachContext", verbose=args.verbose,
        session=session_id, attachment=args.attachment,
    )
    _print_errors(envelopes)
    if _failed(envelopes) or not envelopes:
        return EXIT_FAILED
    attachment = envelopes[-1]["payload"]["attachment"]
    print(f"detached  {attachment['id']}")
    print(f"  path    {attachment['source_id']}")
    print("  note    the binding is gone; the file is untouched")
    return EXIT_OK


async def cmd_set_mode(runtime: Runtime, args: argparse.Namespace) -> int:
    session_id = await _session_for(runtime, args)
    if session_id is None:
        return EXIT_FAILED
    envelopes = await call(
        runtime, "SetAttachmentMode", verbose=args.verbose,
        session=session_id, attachment=args.attachment, mode=args.mode,
    )
    _print_errors(envelopes)
    if _failed(envelopes) or not envelopes:
        return EXIT_FAILED
    attachment = envelopes[-1]["payload"]["attachment"]
    print(f"{attachment['id']}  mode={attachment['mode']}")
    return EXIT_OK


async def cmd_attachments_list(runtime: Runtime, args: argparse.Namespace) -> int:
    session_id = await _session_for(runtime, args)
    if session_id is None:
        return EXIT_FAILED
    envelopes = await call(
        runtime, "ListAttachments", verbose=args.verbose, session=session_id,
    )
    _print_errors(envelopes)
    if _failed(envelopes) or not envelopes:
        return EXIT_FAILED
    rows = envelopes[-1]["payload"]["attachments"]
    if not rows:
        print("(nothing attached)")
        return EXIT_OK
    print(f"{'id':<38}{'mode':<11}source")
    for row in rows:
        print(f"{row['id']:<38}{row['mode']:<11}{row['source_id']}")
    print(f"\n{len(rows)} attachment(s)")
    return EXIT_OK


async def cmd_task_cancel(runtime: Runtime, args: argparse.Namespace) -> int:
    """Cancel one task, or the most recent still-active one with
    `--last`.

    `--last` reads the live TaskManager rather than the `tasks` table:
    only a live object can be cancelled, and a row for a task belonging
    to some other process is not something this process can stop. It
    says so rather than reporting a cancel that never happened.
    """
    task_id = args.task_id
    if args.last:
        active = sorted(runtime.tasks.list_active(), key=lambda t: t.created_at)
        if not active:
            print(
                "error: no active task in this process to cancel "
                "(a task started by a different process cannot be cancelled from here)",
                file=sys.stderr,
            )
            return EXIT_FAILED
        task_id = active[-1].id
    if not task_id:
        print("error: pass a task id or --last", file=sys.stderr)
        return EXIT_FAILED

    envelopes = await call(runtime, "CancelTask", verbose=args.verbose, task_id=task_id)
    _print_errors(envelopes)
    if _failed(envelopes) or not envelopes:
        return EXIT_FAILED
    accepted = envelopes[-1]["payload"].get("accepted")
    print(f"cancel {task_id}: {'accepted' if accepted else 'not accepted (already finished?)'}")
    return EXIT_OK


async def cmd_tasks_list(runtime: Runtime, args: argparse.Namespace) -> int:
    """`tasks.list(scope)` -- running + queued + recent, across all
    sessions (B12).

    Goes through ListTasks rather than reading `task_repo` directly, as
    it did before B12. The handler merges the live TaskManager objects
    over the durable rows, and only the live objects know that a task is
    QUEUED rather than running -- the row is a mirror written a moment
    behind them. Reading the table alone is what would let this command
    report three tasks as running while the scheduler has one running
    and two waiting, which is the exact dishonesty B12 exists to remove.

    Caveat worth knowing when reading the output: a *different* process
    (another `cli.py`, or the backend) has its own TaskManager, and this
    process cannot see its live objects -- only its rows. Rows say
    `running` for anything that was running when they were last written.
    """
    envelopes = await call(
        runtime, "ListTasks", verbose=args.verbose,
        scope=args.scope, limit=args.limit,
    )
    _print_errors(envelopes)
    if _failed(envelopes) or not envelopes:
        return EXIT_FAILED
    rows = envelopes[-1]["payload"]["tasks"]
    if not rows:
        print("(no tasks recorded)")
        return EXIT_OK
    print(
        f"{'id':<38}{'kind':<8}{'state':<11}{'agent':<12}{'error':<14}"
        f"{'created':<20}finished"
    )
    for row in rows:
        print(
            f"{row['id']:<38}{row['kind']:<8}{row['state']:<11}"
            f"{(row['agent'] or '-'):<12}{(row['error_code'] or '-'):<14}"
            f"{_ts(row['created_at']):<20}{_ts(row['finished_at'])}"
        )
    running = sum(1 for r in rows if r["state"] == "running")
    queued = sum(1 for r in rows if r["state"] == "queued")
    print(f"\n{len(rows)} task(s) -- {running} running, {queued} queued")
    return EXIT_OK


# ---------------------------------------------------------------------------
# Proposals (B11) -- NOT B13's full `memory` surface
# ---------------------------------------------------------------------------

async def cmd_memory_list(runtime: Runtime, args: argparse.Namespace) -> int:
    """Lists `agent_proposals` (B11's stopgap). There is no
    accept/reject/edit/bind/forget/search here -- those are B13's,
    against `memory_records`, once real usage justifies building them."""
    status = args.status or None
    proposals = await runtime.proposals.list_by_status(status=status, limit=args.limit)
    if not proposals:
        print("(no proposals)")
        return EXIT_OK
    for p in proposals:
        print(
            f"{p.id}  {_ts(p.created_at)}  session={p.session_id[:8]}  "
            f"agent={p.agent_id or '-':<18}{p.status:<9}{p.content}"
        )
    print(f"\n{len(proposals)} proposal(s)")
    return EXIT_OK


# ---------------------------------------------------------------------------
# Agent commands (B9)
# ---------------------------------------------------------------------------

async def cmd_agents_list(runtime: Runtime, args: argparse.Namespace) -> int:
    if args.wire:
        # B23: exercises ListAgents/AgentList over the wire, the same
        # envelope F8's picker will call. The plain (non-wire) path
        # below calls runtime.agents.list() in-process and proves only
        # that the data exists, not that the wire event works.
        envelopes = await call(runtime, "ListAgents", verbose=args.verbose)
        _print_errors(envelopes)
        if _failed(envelopes) or not envelopes:
            return EXIT_FAILED
        agents = envelopes[-1]["payload"]["agents"]
        if not agents:
            print("(no agents)")
            return EXIT_OK
        for a in agents:
            print(f"{a['id']:<18}{a['name']:<20}{a['description']}")
        print(f"\n{len(agents)} agent(s)")
        return EXIT_OK

    configs = await runtime.agents.list()
    if not configs:
        print("(no agents)")
        return EXIT_OK
    for c in configs:
        kind = "builtin" if c.builtin else "user"
        tools = ",".join(c.tools) or "-"
        print(
            f"{c.id:<18}{c.name:<20}{kind:<8}tools={tools:<26}"
            f"reads(session={int(c.reads_session)},project={int(c.reads_project)},"
            f"global={int(c.reads_global)})  writes_proposals={c.writes_proposals}"
        )
    print(f"\n{len(configs)} agent(s)")
    return EXIT_OK


async def cmd_agents_show(runtime: Runtime, args: argparse.Namespace) -> int:
    try:
        c = await runtime.agents.get(args.agent_id)
    except AgentNotFoundError as exc:
        print(f"error: {exc}", file=sys.stderr)
        return EXIT_FAILED
    print(f"id                   {c.id}")
    print(f"name                 {c.name}")
    print(f"description          {c.description}")
    print(f"builtin              {c.builtin}")
    print(f"provider/model       {c.provider or '-'}/{c.model or '-'}  (effort={c.effort or '-'})")
    print(f"system_prompt        {c.system_prompt}")
    print(f"tools                {list(c.tools)}")
    print(f"reads_session        {c.reads_session}")
    print(f"reads_project        {c.reads_project}")
    print(f"reads_global         {c.reads_global}")
    print(f"writes_proposals     {c.writes_proposals}")
    print(f"stm_window           {c.stm_window}")
    print(f"max_steps            {c.max_steps}")
    print(f"max_tool_calls       {c.max_tool_calls}")
    print(f"timeout_s            {c.timeout_s}")
    print(f"default_attach_mode  {c.default_attach_mode}")
    return EXIT_OK


async def cmd_agents_set_model(runtime: Runtime, args: argparse.Namespace) -> int:
    """Every agent in v1 is builtin, so this always refuses -- that
    refusal, coming from the repository layer rather than a special
    case here, is B9's Done-when #2."""
    try:
        await runtime.agents.set_model(args.agent_id, args.model)
    except (AgentNotFoundError, BuiltinAgentImmutableError) as exc:
        print(f"error: {exc}", file=sys.stderr)
        return EXIT_FAILED
    print(f"{args.agent_id}: model set to {args.model}")
    return EXIT_OK


# ---------------------------------------------------------------------------
# Project commands (B7)
# ---------------------------------------------------------------------------
# Every project subcommand takes --path (default cwd) and re-resolves the
# project on every invocation -- there is no "current project" remembered
# between CLI processes, which matches the backend having no filesystem
# watcher and no session state of its own for this.

async def _resolve_project(
    runtime: Runtime, args: argparse.Namespace
) -> tuple[Optional[dict[str, Any]], list[dict[str, Any]]]:
    envelopes = await call(
        runtime, "ProjectResolve", verbose=args.verbose,
        path=args.path, project_id=getattr(args, "project_id", None),
    )
    if _failed(envelopes):
        return None, envelopes
    return envelopes[-1]["payload"]["project"], envelopes


async def _require_project(runtime: Runtime, args: argparse.Namespace) -> Optional[dict[str, Any]]:
    """Prints its own error and returns None on failure, so every caller
    can just check for None rather than re-printing errors."""
    project, envelopes = await _resolve_project(runtime, args)
    _print_errors(envelopes)
    if project is None and not _failed(envelopes):
        print(f"error: no project at or above {args.path!r} (run: project init --path <dir>)", file=sys.stderr)
    return project


async def cmd_project_init(runtime: Runtime, args: argparse.Namespace) -> int:
    envelopes = await call(runtime, "ProjectInit", verbose=args.verbose, path=args.path)
    _print_errors(envelopes)
    if _failed(envelopes) or not envelopes:
        return EXIT_FAILED
    project = envelopes[-1]["payload"]["project"]
    print(f"{project['id']}  {project['name']}  {project['root_path']}")
    return EXIT_OK


async def cmd_project_resolve(runtime: Runtime, args: argparse.Namespace) -> int:
    project, envelopes = await _resolve_project(runtime, args)
    _print_errors(envelopes)
    if _failed(envelopes):
        return EXIT_FAILED
    if project is None:
        print("project: null  (no .point\\ found)")
        return EXIT_OK
    print(f"{project['id']}  {project['name']}  {project['root_path']}")
    return EXIT_OK


async def cmd_project_list_files(runtime: Runtime, args: argparse.Namespace) -> int:
    project = await _require_project(runtime, args)
    if project is None:
        return EXIT_FAILED
    envelopes = await call(
        runtime, "ListProjectFiles", verbose=args.verbose,
        project_id=project["id"], prefix=args.prefix, limit=args.limit, refresh=args.refresh,
    )
    _print_errors(envelopes)
    if _failed(envelopes) or not envelopes:
        return EXIT_FAILED
    files = envelopes[-1]["payload"]["files"]
    for f in files:
        print(f)
    print(f"\n{len(files)} file(s)")
    return EXIT_OK


async def cmd_project_read_file(runtime: Runtime, args: argparse.Namespace) -> int:
    project = await _require_project(runtime, args)
    if project is None:
        return EXIT_FAILED
    envelopes = await call(
        runtime, "ReadProjectFile", verbose=args.verbose,
        project_id=project["id"], path=args.file, allow_outside_project=args.allow_outside,
    )
    _print_errors(envelopes)
    if _failed(envelopes) or not envelopes:
        return EXIT_FAILED
    f = envelopes[-1]["payload"]["file"]
    print(f"path:            {f['path']}")
    print(f"mtime:           {f['mtime']}")
    print(f"size:            {f['size']}")
    print(f"outside_project: {f['outside_project']}")
    print("---")
    print(f["content"])
    return EXIT_OK


async def cmd_project_browse(runtime: Runtime, args: argparse.Namespace) -> int:
    """B23: no _require_project / _resolve_project here on purpose --
    browsing is explicit-project-or-none, never an implicit resolve
    against --path the way every other `project` subcommand works. See
    Runtime._handle_list_directory's docstring for why."""
    envelopes = await call(
        runtime, "ListDirectory", verbose=args.verbose,
        path=args.target, project_id=args.project_id, cwd=args.cwd or os.getcwd(),
    )
    _print_errors(envelopes)
    if _failed(envelopes) or not envelopes:
        return EXIT_FAILED
    result = envelopes[-1]["payload"]
    print(f"path:            {result['path']}")
    print(f"outside_project: {result['outside_project']}")
    print("---")
    for e in result["entries"]:
        marker = "<DIR>" if e["is_dir"] else "     "
        print(f"{marker}  {e['name']}")
    print(f"\n{len(result['entries'])} entries")
    return EXIT_OK


_LOCK_TYPE_BY_COMMAND = {
    "acquire": "AcquireFileLock",
    "heartbeat": "HeartbeatFileLock",
    "release": "ReleaseFileLock",
    "take-over": "TakeOverFileLock",
}


async def cmd_project_lock(runtime: Runtime, args: argparse.Namespace) -> int:
    project = await _require_project(runtime, args)
    if project is None:
        return EXIT_FAILED
    envelopes = await call(
        runtime, _LOCK_TYPE_BY_COMMAND[args.lock_command], verbose=args.verbose,
        project_id=project["id"], path=args.file, holder=args.holder,
    )
    _print_errors(envelopes)
    if _failed(envelopes) or not envelopes:
        return EXIT_FAILED
    payload = envelopes[-1]["payload"]
    if args.lock_command == "release":
        print("released" if payload["released"] else f"not held by {args.holder}")
        return EXIT_OK
    if payload["ok"]:
        print("ok")
    else:
        print(f"held by {payload['holder']} ({int(payload['age_seconds'])}s)")
    return EXIT_OK


async def cmd_project_check_stale(runtime: Runtime, args: argparse.Namespace) -> int:
    project = await _require_project(runtime, args)
    if project is None:
        return EXIT_FAILED
    envelopes = await call(
        runtime, "CheckFileStale", verbose=args.verbose,
        project_id=project["id"], path=args.file, mtime=args.mtime,
        allow_outside_project=args.allow_outside,
    )
    _print_errors(envelopes)
    if _failed(envelopes) or not envelopes:
        return EXIT_FAILED
    print("changed" if envelopes[-1]["payload"]["changed"] else "unchanged")
    return EXIT_OK


def cmd_migrate(args: argparse.Namespace) -> int:
    """No Runtime: migrations run before one can safely open a database."""
    db_path = resolve_db_path()
    result = run_migrations(db_path, logger=_log)
    print(f"database: {db_path}")
    print(f"          {result.describe()}")
    for backup in result.backups:
        print(f"backup:   {backup}")
    return EXIT_OK


# ---------------------------------------------------------------------------
# doctor
# ---------------------------------------------------------------------------

async def build_doctor_report() -> dict[str, Any]:
    """Every fact `cmd_doctor` prints, as data. No `print()` calls live
    here -- `cmd_doctor` formats this dict to stdout, and the
    `GetDoctorReport` wire handler ships it verbatim; both read the same
    facts once.

    Must succeed on a machine with no config at all. Every field is
    either a fact or an explicit "unknown"/"missing" -- never a guess.
    """
    version_file = _repo_root() / "VERSION"
    version = version_file.read_text(encoding="utf-8").strip() if version_file.exists() else "(missing)"

    db_path = resolve_db_path()
    if db_path.exists():
        version_now = current_version(db_path)
        database_state = f"schema v{version_now} (expected v{SCHEMA_VERSION})"
        if version_now < SCHEMA_VERSION:
            database_state += "  -- run: uv run python cli.py migrate"
    else:
        database_state = "does not exist yet -- run: uv run python cli.py migrate"

    config = appconfig.config_path()
    creds = appconfig.credentials_path()

    providers = []
    configured = 0
    for name in appconfig.known_providers():
        source = appconfig.key_source(name)
        if source:
            configured += 1
            key = appconfig.resolve_api_key(name)
            providers.append({
                "name": name, "configured": True,
                "source": source, "masked_key": appconfig.mask_secret(key),
            })
        else:
            providers.append({"name": name, "configured": False})

    # Correction (B26): _ollama_report()'s asyncio.run() cannot be
    # reused here -- this coroutine runs inside the wire handler's
    # already-running event loop. _ollama_probe() is the same probe,
    # extracted; the exception handling _ollama_report() used to do
    # around asyncio.run() moves here, so both callers get identical
    # failure text.
    try:
        ollama_report = await _ollama_probe()
    except Exception as exc:  # noqa: BLE001
        ollama_report = f"not reachable ({exc})"

    return {
        "version_file": str(version_file),
        "version": version,
        "go_frontend_status": _go_binary_status(),
        "envelope_version": ENVELOPE_VERSION,
        "python_version": sys.version.split()[0],
        "python_executable": sys.executable,
        "dependency_report": _dependency_report(),
        "database_path": str(db_path),
        "database_state": database_state,
        "legacy_db_path": str(LEGACY_DB_PATH) if LEGACY_DB_PATH.exists() else None,
        "config_path": str(config),
        "config_exists": config.exists(),
        "config_error": appconfig.config_error(),
        "credentials_path": str(creds),
        "credentials_exists": creds.exists(),
        "credentials_permissions_inherited": creds.exists() and sys.platform == "win32",
        "state_path": str(appconfig.state_path()),
        "state_exists": appconfig.state_path().exists(),
        "log_path": str(appconfig.log_path()),
        "log_exists": appconfig.log_path().exists(),
        "providers": providers,
        "providers_configured_count": configured,
        "ollama_report": ollama_report,
        "mock_enabled": mock_provider.is_enabled(),
        "behaviour_overrides": appconfig_overrides.all_overrides(),
    }


def _print_doctor_report(report: dict[str, Any]) -> None:
    """Reproduces `cmd_doctor`'s exact pre-B26 stdout output, reading
    from the dict instead of calling each helper inline. Byte-identical
    on purpose -- see B26's Done-when #4."""
    print("Point doctor")
    print("-" * 60)
    print(f"VERSION file        {report['version']}  ({report['version_file']})")
    print(f"backend version     {report['version']}  (read from the same file)")
    print(f"go frontend         {report['go_frontend_status']}")
    print(f"protocol envelope   v{report['envelope_version']}")
    print(f"python              {report['python_version']}  ({report['python_executable']})")

    print("dependencies       ", report["dependency_report"])

    print(f"database            {report['database_path']}")
    print(f"                    {report['database_state']}")
    if report["legacy_db_path"]:
        print(f"                    legacy copy still present: {report['legacy_db_path']}")

    print(f"config              {report['config_path']}  {_exists_text(report['config_exists'])}")
    if report["config_error"]:
        print(f"                    UNREADABLE: {report['config_error']}")
    print(f"credentials         {report['credentials_path']}  {_exists_text(report['credentials_exists'])}")
    if report["credentials_permissions_inherited"]:
        print("                    permissions inherited from the user profile directory")
    print(f"state               {report['state_path']}  {_exists_text(report['state_exists'])}")
    print(f"logs                {report['log_path']}  {_exists_text(report['log_exists'])}")

    print("providers")
    for provider in report["providers"]:
        if provider["configured"]:
            print(f"  {provider['name']:<12}key from {provider['source']}  ({provider['masked_key']})")
        else:
            print(f"  {provider['name']:<12}no credentials")
    print(f"  ollama      {report['ollama_report']}")
    print(f"  mock        {'enabled (POINT_MOCK=1)' if report['mock_enabled'] else 'not enabled'}")
    if report["providers_configured_count"] == 0:
        print("\nNo cloud provider is configured. Point still runs: use ollama, or")
        print("set a key in the environment or in credentials.toml above.")


def cmd_doctor(args: argparse.Namespace) -> int:
    report = asyncio.run(build_doctor_report())
    _print_doctor_report(report)
    return EXIT_OK


async def cmd_doctor_via_wire(runtime: Runtime, args: argparse.Namespace) -> int:
    """B26 verification: drives GetDoctorReport through the same `call()`
    every other event-backed command uses, so the wire reply can be
    diffed against `cmd_doctor`'s own direct numbers."""
    envelopes = await call(runtime, "GetDoctorReport")
    _print_errors(envelopes)
    if _failed(envelopes):
        return EXIT_FAILED
    report_envelope = next(e for e in envelopes if e["type"] == "DoctorReport")
    print(json.dumps(report_envelope["payload"], indent=2))
    return EXIT_OK


async def cmd_behaviour_set(runtime: Runtime, args: argparse.Namespace) -> int:
    envelopes = await call(runtime, "SetBehaviourOverride", key=args.key, value=_parse_cli_value(args.value))
    _print_errors(envelopes)
    if _failed(envelopes):
        return EXIT_FAILED
    result = next(e for e in envelopes if e["type"] == "BehaviourOverrideSet")
    print(json.dumps(result["payload"], indent=2))
    return EXIT_OK


async def cmd_behaviour_clear(runtime: Runtime, args: argparse.Namespace) -> int:
    envelopes = await call(runtime, "SetBehaviourOverride", key=args.key)
    _print_errors(envelopes)
    if _failed(envelopes):
        return EXIT_FAILED
    result = next(e for e in envelopes if e["type"] == "BehaviourOverrideSet")
    print(json.dumps(result["payload"], indent=2))
    return EXIT_OK


def _parse_cli_value(raw: str) -> Any:
    """JSON when it parses (so `0`, `true`, `"a string"` all work as a
    shell would type them without a --json flag), else the raw string."""
    try:
        return json.loads(raw)
    except json.JSONDecodeError:
        return raw


def _repo_root():
    from pathlib import Path
    return Path(__file__).resolve().parent.parent


def _exists_text(exists: bool) -> str:
    return "(exists)" if exists else "(missing)"


def _go_binary_status() -> str:
    """Honest about what it cannot know: there is no built binary to ask
    for a version, so there is nothing to compare against yet."""
    for candidate in ("point.exe", "point"):
        binary = _repo_root() / candidate
        if binary.exists():
            return f"binary at {binary} -- version comparison not implemented"
    return "not built -- cannot compare versions"


def _dependency_report() -> str:
    import importlib
    import importlib.metadata as metadata
    import warnings

    parts = []
    for module, dist in (
        ("aiosqlite", "aiosqlite"), ("httpx", "httpx"), ("anthropic", "anthropic"),
        ("openai", "openai"), ("google.generativeai", "google-generativeai"),
    ):
        try:
            # Importing purely to answer "is it installed?" -- a package's
            # own deprecation notice is not this report's business, and
            # google-generativeai prints a nine-line one.
            with warnings.catch_warnings():
                warnings.simplefilter("ignore")
                importlib.import_module(module)
        except Exception:  # noqa: BLE001
            parts.append(f"{dist}: MISSING")
            continue
        try:
            parts.append(f"{dist} {metadata.version(dist)}")
        except metadata.PackageNotFoundError:
            parts.append(f"{dist} (version unknown)")
    return ", ".join(parts)


async def _ollama_probe() -> str:
    from providers.ollama.provider import OllamaProvider
    provider = OllamaProvider(ProviderConfig.from_env("ollama"))
    health = await provider.health()
    if health.status != ProviderHealthStatus.HEALTHY:
        return f"not reachable ({health.detail or health.status.value})"
    models = await provider.list_models()
    names = ", ".join(m.id for m in models[:5]) or "no models pulled"
    return f"reachable, {len(models)} model(s): {names}"


def _ollama_report() -> str:
    """CLI-only: owns the event loop, so it may create one. Not called
    from build_doctor_report() -- that awaits _ollama_probe() directly,
    since it already runs inside a running event loop."""
    try:
        return asyncio.run(_ollama_probe())
    except Exception as exc:  # noqa: BLE001
        return f"not reachable ({exc})"


# ---------------------------------------------------------------------------
# Wiring
# ---------------------------------------------------------------------------

def _ts(value: Optional[float]) -> str:
    if not value:
        return "-"
    return time.strftime("%Y-%m-%d %H:%M:%S", time.localtime(value))


async def _with_runtime(handler, args: argparse.Namespace) -> int:
    # single_instance=False: the CLI is a second process, not a second
    # instance of the app. WAL is what makes that safe.
    runtime = await build_runtime(
        single_instance=False, verbose=args.verbose, say_hello=False, heartbeat=False
    )
    try:
        return await handler(runtime, args)
    finally:
        await shutdown_runtime(runtime)


def build_parser() -> argparse.ArgumentParser:
    # --verbose is accepted before *and* after the subcommand, because
    # "cli.py send ... --verbose" is what anyone actually types. The
    # SUPPRESS default matters: without it the subparser's own default
    # would overwrite a --verbose given before the subcommand.
    common = argparse.ArgumentParser(add_help=False)
    common.add_argument(
        "--verbose", action="store_true", default=argparse.SUPPRESS,
        help="log to stderr, show discarded envelopes",
    )

    parser = argparse.ArgumentParser(
        prog="cli.py", description="Point backend CLI", parents=[common]
    )
    parser.set_defaults(verbose=False)
    sub = parser.add_subparsers(dest="command", required=True)

    doctor = sub.add_parser("doctor", help="report versions, paths, database, providers", parents=[common])
    doctor.add_argument(
        "--via-wire", action="store_true",
        help="drive GetDoctorReport over the event pipe instead of calling "
             "build_doctor_report() directly (B26 verification: proves the "
             "wire reply matches this command's own numbers). Always prints JSON.",
    )
    sub.add_parser("migrate", help="bring the database up to the current schema version", parents=[common])

    session = sub.add_parser("session", help="session operations", parents=[common]).add_subparsers(
        dest="session_command", required=True
    )
    new = session.add_parser("new", help="create a session", parents=[common])
    new.add_argument("title", nargs="?", default="New Session")
    new.add_argument("--provider", default=None, help="default: ollama if reachable, else echo")
    new.add_argument("--model", default=None)
    new.add_argument("--agent", default=None, help="agent id (default: general)")
    new.add_argument(
        "--project", dest="project_id", default=None,
        help="project id (from `project init`) -- the root file tools are guarded against",
    )
    new.add_argument(
        "--path", default=".",
        help="where to look for a project when --project is not given (default: cwd)",
    )
    new.add_argument(
        "--no-project", dest="no_project", action="store_true",
        help="create the session with no project, even inside one",
    )
    listing = session.add_parser("list", help="list sessions", parents=[common])
    listing.add_argument("--all", action="store_true", help="include archived")

    msgs = session.add_parser(
        "messages", help="paginate a session's messages by seq (B18)", parents=[common]
    )
    msgs.add_argument("session_id")
    msgs.add_argument(
        "--before", type=int, default=None, metavar="SEQ",
        help="messages with seq < SEQ, oldest-first",
    )
    msgs.add_argument(
        "--after", type=int, default=None, metavar="SEQ",
        help="messages with seq > SEQ, oldest-first",
    )
    msgs.add_argument(
        "--limit", type=int, default=50,
        help="page size; neither --before nor --after given means the newest --limit (default: 50)",
    )

    seed = sub.add_parser(
        "seed-session", help="generate a synthetic session for scrollback/render testing",
        parents=[common],
    )
    seed.add_argument("--messages", type=int, default=200, help="how many messages (default: 200)")
    seed.add_argument("--session", default=None, help="append to an existing session id")
    seed.add_argument("--title", default=None, help="title for a newly created session")

    send = sub.add_parser("send", help="send a message and stream the reply", parents=[common])
    send.add_argument("text")
    send.add_argument("--session", default=None, help="existing session id")
    send.add_argument(
        "--agent", default=None,
        help="explicit agent override; wins over an @agent parsed from the text",
    )
    send.add_argument("--cancel-after", type=float, default=None, metavar="SECONDS")
    send.add_argument("--timeout", type=float, default=180.0)
    send.add_argument(
        "--script", default=None, metavar="NAME",
        help="replay a mock fixture by name (requires POINT_MOCK=1); "
             "see tests\\fixtures\\",
    )
    # The BoundsOverride flags (B10). One send only -- they never touch
    # the agent's stored config, which is frozen and builtin-read-only.
    send.add_argument("--max-steps", type=int, default=None)
    send.add_argument("--max-tool-calls", type=int, default=None)
    send.add_argument(
        "--max-seconds", type=int, default=None, dest="max_seconds",
        help="override the agent's timeout_s for this send",
    )
    # B11: a background task (max_steps > 1 -- see agent config) returns
    # right after TaskStarted by default; --wait blocks to the terminal
    # event like every inline send always has.
    send.add_argument(
        "--wait", action="store_true",
        help="block until the task finishes, even if it runs in the background",
    )
    _add_editor_flags(send)

    ctx = sub.add_parser("context", help="context builder", parents=[common]).add_subparsers(
        dest="context_command", required=True
    )
    ctx_report = ctx.add_parser(
        "report", help="what the next turn's context would contain", parents=[common]
    )
    ctx_report.add_argument("--session", default=None, help="session id")
    ctx_report.add_argument(
        "--last", action="store_true",
        help="the most recently updated session (default when --session is omitted)",
    )
    ctx_report.add_argument("--json", action="store_true", help="machine-readable output")
    ctx_report.add_argument(
        "--via-wire", action="store_true",
        help="drive GetContextReport over the event pipe instead of calling "
             "context.report() directly (B20 verification: proves the wire "
             "reply matches this command's own numbers). Always prints JSON; "
             "editor context flags are ignored on this path, since "
             "GetContextReport's payload is session_id only.",
    )
    _add_editor_flags(ctx_report)

    tasks = sub.add_parser("tasks", help="task operations", parents=[common]).add_subparsers(
        dest="tasks_command", required=True
    )
    tasks_list = tasks.add_parser(
        "list", help="list running, queued and recent tasks", parents=[common]
    )
    tasks_list.add_argument("--limit", type=int, default=20)
    tasks_list.add_argument(
        "--scope", choices=("all", "active"), default="all",
        help="all (default) or active (running + queued only)",
    )

    task = sub.add_parser("task", help="one task", parents=[common]).add_subparsers(
        dest="task_command", required=True
    )
    task_cancel = task.add_parser("cancel", help="cancel a running or queued task", parents=[common])
    task_cancel.add_argument("task_id", nargs="?", default=None)
    task_cancel.add_argument(
        "--last", action="store_true",
        help="cancel the most recent active task in THIS process",
    )

    # Attachments (B12). `--session` defaults to the most recently
    # updated session, because an attachment is something you do to the
    # conversation you are already having.
    attach = sub.add_parser("attach", help="attach a file to a session", parents=[common])
    attach.add_argument("file", help="path to attach; \\ and / are both accepted")
    attach.add_argument("--session", default=None)
    attach.add_argument(
        "--mode", choices=("full", "reference", "detached"), default=None,
        help="default: the agent's default_attach_mode",
    )
    # No --allow-outside here. An attachment is guarded under the
    # SESSION's project and the config key `project.allow_outside_project`
    # -- the same two things the context builder will use at send time.
    # A per-command override would let `attach` report a cost the builder
    # then refuses to spend, which is the one thing the estimate exists
    # to prevent.
    attach.add_argument(
        "--preview", action="store_true",
        help="take the offered fallback: first 200 lines, or a directory listing",
    )

    detach = sub.add_parser(
        "detach", help="remove an attachment binding (never deletes the file)", parents=[common]
    )
    detach.add_argument("attachment", help="attachment id")
    detach.add_argument("--session", default=None)

    set_mode = sub.add_parser("set-mode", help="change an attachment's mode", parents=[common])
    set_mode.add_argument("attachment", help="attachment id")
    set_mode.add_argument("mode", choices=("full", "reference", "detached"))
    set_mode.add_argument("--session", default=None)

    attachments = sub.add_parser(
        "attachments", help="attachment operations", parents=[common]
    ).add_subparsers(dest="attachments_command", required=True)
    attachments_list = attachments.add_parser(
        "list", help="what is attached to a session", parents=[common]
    )
    attachments_list.add_argument("--session", default=None)

    # B11's agent_proposals stopgap -- not B13's eventual full `memory`
    # surface (accept/reject/edit/bind/unbind/forget/search).
    memory_sub = sub.add_parser(
        "memory", help="agent proposals (B11 stopgap)", parents=[common]
    ).add_subparsers(dest="memory_command", required=True)
    memory_list = memory_sub.add_parser("list", help="list agent proposals", parents=[common])
    memory_list.add_argument(
        "--status", default="pending", help="filter by status; pass '' for all (default: pending)",
    )
    memory_list.add_argument("--limit", type=int, default=50)

    agents_sub = sub.add_parser("agents", help="agent operations", parents=[common]).add_subparsers(
        dest="agents_command", required=True
    )
    agents_list_parser = agents_sub.add_parser(
        "list", help="list built-in and user agents", parents=[common]
    )
    agents_list_parser.add_argument(
        "--wire", action="store_true",
        help="go over the wire (ListAgents/AgentList) instead of the in-process registry",
    )
    agents_show = agents_sub.add_parser("show", help="show one agent's full config", parents=[common])
    agents_show.add_argument("agent_id")
    agents_set_model = agents_sub.add_parser(
        "set-model", help="set an agent's model (refused for builtins)", parents=[common]
    )
    agents_set_model.add_argument("agent_id")
    agents_set_model.add_argument("model")

    # project: --path resolves fresh on every call (default cwd) -- there
    # is no "current project" remembered between CLI processes.
    project = sub.add_parser("project", help="project operations", parents=[common]).add_subparsers(
        dest="project_command", required=True
    )
    p_init = project.add_parser("init", help="init or re-resolve a project", parents=[common])
    p_init.add_argument("--path", default=".", help="project root (default: cwd)")

    p_resolve = project.add_parser("resolve", help="resolve a project by path or id", parents=[common])
    p_resolve.add_argument("--path", default=".")
    p_resolve.add_argument("--project", dest="project_id", default=None, help="explicit project id")

    p_list = project.add_parser("list-files", help="list the project file index", parents=[common])
    p_list.add_argument("--path", default=".")
    p_list.add_argument("--prefix", default="")
    p_list.add_argument("--limit", type=int, default=200)
    p_list.add_argument("--refresh", action="store_true", help="rebuild the index instead of using the cache")

    p_read = project.add_parser("read-file", help="guarded, size-capped file read", parents=[common])
    p_read.add_argument("--path", default=".")
    p_read.add_argument("--file", required=True, help="relative or absolute path")
    p_read.add_argument("--allow-outside", dest="allow_outside", action="store_true")

    p_lock = project.add_parser("lock", help="soft file locks", parents=[common]).add_subparsers(
        dest="lock_command", required=True
    )
    for name, help_text in (
        ("acquire", "acquire a lock, or report who holds it"),
        ("heartbeat", "renew a lock this holder already has"),
        ("release", "release a lock this holder has"),
        ("take-over", "steal a stale or held lock unconditionally"),
    ):
        lock_sub = p_lock.add_parser(name, help=help_text, parents=[common])
        lock_sub.add_argument("--path", default=".")
        lock_sub.add_argument("--file", required=True)
        lock_sub.add_argument("--holder", required=True)

    p_stale = project.add_parser("check-stale", help="stale-buffer check for save", parents=[common])
    p_stale.add_argument("--path", default=".")
    p_stale.add_argument("--file", required=True)
    p_stale.add_argument("--mtime", type=float, required=True, help="the mtime read-file reported at open")
    p_stale.add_argument("--allow-outside", dest="allow_outside", action="store_true")

    # B23: plain, uncached directory listing -- no --path here on purpose.
    # Every other `project` subcommand resolves a project from --path
    # implicitly; browse takes an explicit --project instead (default:
    # none), since a browse target is meant to sweep across arbitrary,
    # often project-unrelated locations. See cmd_project_browse.
    p_browse = project.add_parser(
        "browse", help="list a directory's immediate children (no project required)", parents=[common]
    )
    p_browse.add_argument("target", help="absolute path, '.', '..', '~', or relative to --cwd")
    p_browse.add_argument("--project", dest="project_id", default=None, help="explicit project id")
    p_browse.add_argument(
        "--cwd", default=None,
        help="root for a relative TARGET when no project is given (default: this process's own cwd)",
    )

    # Behaviour overrides (B26). No non-wire path exists for this feature
    # at all (unlike doctor, which has one) -- this is the only CLI
    # surface, not a parity check.
    behaviour = sub.add_parser(
        "behaviour", help="runtime overrides for Settings-tab values", parents=[common]
    ).add_subparsers(dest="behaviour_command", required=True)
    behaviour_set = behaviour.add_parser("set", help="set an override", parents=[common])
    behaviour_set.add_argument("key")
    behaviour_set.add_argument("value", help="parsed as JSON if it parses, else a raw string")
    behaviour_clear = behaviour.add_parser("clear", help="clear an override", parents=[common])
    behaviour_clear.add_argument("key")

    return parser


def main(argv: Optional[list[str]] = None) -> int:
    args = build_parser().parse_args(argv)
    configure_logging(verbose=args.verbose)

    # Set before anything constructs a Runtime: MockProvider reads
    # POINT_MOCK_FIXTURE at construction, and the provider registry is
    # built inside Runtime.__init__.
    if getattr(args, "script", None):
        fixture = script_fixture(args.script)
        if not fixture.is_file():
            print(
                f"no such script: {args.script} "
                f"(available: {', '.join(available_scripts()) or 'none'})",
                file=sys.stderr,
            )
            return EXIT_FAILED
        if os.environ.get("POINT_MOCK") != "1":
            print("--script requires POINT_MOCK=1", file=sys.stderr)
            return EXIT_FAILED
        os.environ["POINT_MOCK_FIXTURE"] = str(fixture)

    if args.command == "doctor":
        if getattr(args, "via_wire", False):
            return asyncio.run(_with_runtime(cmd_doctor_via_wire, args))
        return cmd_doctor(args)
    if args.command == "migrate":
        return cmd_migrate(args)

    handlers = {
        ("session", "new"): cmd_session_new,
        ("session", "list"): cmd_session_list,
        ("session", "messages"): cmd_session_messages,
        ("seed-session", None): cmd_seed_session,
        ("send", None): cmd_send,
        ("context", "report"): cmd_context_report,
        ("tasks", "list"): cmd_tasks_list,
        ("memory", "list"): cmd_memory_list,
        ("agents", "list"): cmd_agents_list,
        ("agents", "show"): cmd_agents_show,
        ("agents", "set-model"): cmd_agents_set_model,
        ("project", "init"): cmd_project_init,
        ("project", "resolve"): cmd_project_resolve,
        ("project", "list-files"): cmd_project_list_files,
        ("project", "read-file"): cmd_project_read_file,
        ("project", "check-stale"): cmd_project_check_stale,
        ("project", "browse"): cmd_project_browse,
        # Attachments and single-task cancel (B12).
        ("attach", None): cmd_attach,
        ("detach", None): cmd_detach,
        ("set-mode", None): cmd_set_mode,
        ("attachments", "list"): cmd_attachments_list,
        ("task", "cancel"): cmd_task_cancel,
        ("behaviour", "set"): cmd_behaviour_set,
        ("behaviour", "clear"): cmd_behaviour_clear,
    }
    # "project lock <verb>" is a third level of subcommand, one deeper
    # than the (command, X_command) shape every other tree uses.
    if args.command == "project" and args.project_command == "lock":
        handler = cmd_project_lock
        key = ("project", "lock", args.lock_command)
    else:
        key = (args.command, getattr(args, f"{args.command}_command", None))
        handler = handlers.get(key)
    if handler is None:
        print(f"not implemented yet: {' '.join(k for k in key if k)}", file=sys.stderr)
        return EXIT_FAILED

    try:
        return asyncio.run(_with_runtime(handler, args))
    except AlreadyRunningError as exc:
        # The CLI does not take the lock, so this only happens if
        # something else does the taking. Report it rather than crash.
        print(str(exc), file=sys.stderr)
        return EXIT_FAILED


if __name__ == "__main__":
    try:
        sys.exit(main())
    except KeyboardInterrupt:
        sys.exit(130)
