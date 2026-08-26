r"""The backend's own command line, so every later episode can be
verified without Go (B6 14).

    uv run python cli.py doctor
    uv run python cli.py migrate
    uv run python cli.py session new [title]
    uv run python cli.py session list
    uv run python cli.py send "count to five" [--cancel-after 2]
    uv run python cli.py tasks list

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
import sys
import time
import uuid
from typing import Any, Callable, Optional

import appconfig
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
from providers.mock import provider as mock_provider
from providers.models import ProviderConfig, ProviderHealthStatus
from runtime.events import ENVELOPE_VERSION
from runtime.runtime import Runtime

_log = get_logger("cli")

# Terminal reply per request type: what `call()` waits for before it
# stops draining. Anything not listed falls back to the timeout.
_EXPECTED_REPLY: dict[str, set[str]] = {
    "CreateSession": {"SessionCreated"},
    "ListSessions": {"SessionList"},
    "GetSession": {"Session"},
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
            print(f"error: {envelope['payload'].get('error', 'unknown error')}", file=sys.stderr)


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

async def cmd_session_new(runtime: Runtime, args: argparse.Namespace) -> int:
    provider, model = args.provider, args.model
    if not provider:
        provider, model = await default_provider(runtime)
    elif not model:
        model = provider

    envelopes = await call(
        runtime, "CreateSession", verbose=args.verbose,
        title=args.title, provider=provider, model=model,
    )
    _print_errors(envelopes)
    if _failed(envelopes) or not envelopes:
        return EXIT_FAILED
    session = envelopes[-1]["payload"]["session"]
    print(f"{session['id']}  {session['title']}  [{session['provider']}/{session['model']}]")
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


async def cmd_send(runtime: Runtime, args: argparse.Namespace) -> int:
    session_id = args.session
    if not session_id:
        provider, model = await default_provider(runtime)
        created = await call(
            runtime, "CreateSession", verbose=args.verbose,
            title=args.text[:40] or "cli", provider=provider, model=model,
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
        elif kind == "task.cancel.accepted":
            print(f"\n[cancel accepted: {payload.get('accepted')}]")
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
        return envelope["type"] in _TASK_TERMINAL or envelope["type"] == "Error"

    envelopes = await call(
        runtime, "SendMessage", timeout=args.timeout, verbose=args.verbose,
        until=until, match=_first_ack_then(match), on_event=on_event,
        session=session_id, text=args.text,
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


async def cmd_tasks_list(runtime: Runtime, args: argparse.Namespace) -> int:
    """Reads the durable rows, not TaskManager's in-memory dict -- a
    fresh CLI process has no in-memory tasks, and the whole point of §3
    is that the answer outlives the process that produced it."""
    rows = await runtime.task_repo.list_recent(limit=args.limit)
    if not rows:
        print("(no tasks recorded)")
        return EXIT_OK
    print(f"{'id':<38}{'kind':<8}{'status':<11}{'error':<14}{'created':<20}finished")
    for row in rows:
        print(
            f"{row['id']:<38}{row['kind']:<8}{row['status']:<11}"
            f"{(row['error_code'] or '-'):<14}{_ts(row['created_at']):<20}"
            f"{_ts(row['finished_at'])}"
        )
    print(f"\n{len(rows)} task(s)")
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

def cmd_doctor(args: argparse.Namespace) -> int:
    """Must run and exit 0 on a machine with no config at all. Every line
    is either a fact or an explicit 'unknown' -- never a guess."""
    print("Point doctor")
    print("-" * 60)

    version_file = _repo_root() / "VERSION"
    version = version_file.read_text(encoding="utf-8").strip() if version_file.exists() else "(missing)"
    print(f"VERSION file        {version}  ({version_file})")
    print(f"backend version     {version}  (read from the same file)")
    print(f"go frontend         {_go_binary_status()}")
    print(f"protocol envelope   v{ENVELOPE_VERSION}")
    print(f"python              {sys.version.split()[0]}  ({sys.executable})")

    print("dependencies       ", _dependency_report())

    db_path = resolve_db_path()
    if db_path.exists():
        version_now = current_version(db_path)
        state = f"schema v{version_now} (expected v{SCHEMA_VERSION})"
        if version_now < SCHEMA_VERSION:
            state += "  -- run: uv run python cli.py migrate"
    else:
        state = "does not exist yet -- run: uv run python cli.py migrate"
    print(f"database            {db_path}")
    print(f"                    {state}")
    if LEGACY_DB_PATH.exists():
        print(f"                    legacy copy still present: {LEGACY_DB_PATH}")

    config = appconfig.config_path()
    creds = appconfig.credentials_path()
    print(f"config              {config}  {_exists(config)}")
    error = appconfig.config_error()
    if error:
        print(f"                    UNREADABLE: {error}")
    print(f"credentials         {creds}  {_exists(creds)}")
    if creds.exists() and sys.platform == "win32":
        print("                    permissions inherited from the user profile directory")
    print(f"state               {appconfig.state_path()}  {_exists(appconfig.state_path())}")
    print(f"logs                {appconfig.log_path()}  {_exists(appconfig.log_path())}")

    print("providers")
    configured = 0
    for name in appconfig.known_providers():
        source = appconfig.key_source(name)
        if source:
            configured += 1
            key = appconfig.resolve_api_key(name)
            print(f"  {name:<12}key from {source}  ({appconfig.mask_secret(key)})")
        else:
            print(f"  {name:<12}no credentials")
    print(f"  ollama      {_ollama_report()}")
    print(f"  mock        {'enabled (POINT_MOCK=1)' if mock_provider.is_enabled() else 'not enabled'}")
    if configured == 0:
        print("\nNo cloud provider is configured. Point still runs: use ollama, or")
        print("set a key in the environment or in credentials.toml above.")
    return EXIT_OK


def _repo_root():
    from pathlib import Path
    return Path(__file__).resolve().parent.parent


def _exists(path) -> str:
    return "(exists)" if path.exists() else "(missing)"


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


def _ollama_report() -> str:
    async def probe() -> str:
        from providers.ollama.provider import OllamaProvider
        provider = OllamaProvider(ProviderConfig.from_env("ollama"))
        health = await provider.health()
        if health.status != ProviderHealthStatus.HEALTHY:
            return f"not reachable ({health.detail or health.status.value})"
        models = await provider.list_models()
        names = ", ".join(m.id for m in models[:5]) or "no models pulled"
        return f"reachable, {len(models)} model(s): {names}"

    try:
        return asyncio.run(probe())
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

    sub.add_parser("doctor", help="report versions, paths, database, providers", parents=[common])
    sub.add_parser("migrate", help="bring the database up to the current schema version", parents=[common])

    session = sub.add_parser("session", help="session operations", parents=[common]).add_subparsers(
        dest="session_command", required=True
    )
    new = session.add_parser("new", help="create a session", parents=[common])
    new.add_argument("title", nargs="?", default="New Session")
    new.add_argument("--provider", default=None, help="default: ollama if reachable, else echo")
    new.add_argument("--model", default=None)
    listing = session.add_parser("list", help="list sessions", parents=[common])
    listing.add_argument("--all", action="store_true", help="include archived")

    send = sub.add_parser("send", help="send a message and stream the reply", parents=[common])
    send.add_argument("text")
    send.add_argument("--session", default=None, help="existing session id")
    send.add_argument("--cancel-after", type=float, default=None, metavar="SECONDS")
    send.add_argument("--timeout", type=float, default=180.0)

    tasks = sub.add_parser("tasks", help="task operations", parents=[common]).add_subparsers(
        dest="tasks_command", required=True
    )
    tasks_list = tasks.add_parser("list", help="list recorded tasks", parents=[common])
    tasks_list.add_argument("--limit", type=int, default=20)

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

    return parser


def main(argv: Optional[list[str]] = None) -> int:
    args = build_parser().parse_args(argv)
    configure_logging(verbose=args.verbose)

    if args.command == "doctor":
        return cmd_doctor(args)
    if args.command == "migrate":
        return cmd_migrate(args)

    handlers = {
        ("session", "new"): cmd_session_new,
        ("session", "list"): cmd_session_list,
        ("send", None): cmd_send,
        ("tasks", "list"): cmd_tasks_list,
        ("project", "init"): cmd_project_init,
        ("project", "resolve"): cmd_project_resolve,
        ("project", "list-files"): cmd_project_list_files,
        ("project", "read-file"): cmd_project_read_file,
        ("project", "check-stale"): cmd_project_check_stale,
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
