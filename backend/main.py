"""Entry point. One Runtime instance for the life of the process.

Two concurrent loops:
  - stdin reader:  Go -> Runtime  (never blocks Runtime)
  - outbox writer: Runtime -> Go  (streams events as they're produced)

Windows note: asyncio's loop.connect_read_pipe() does not reliably
support console stdin on the ProactorEventLoop (the default on
Windows, required for subprocess support). Instead we read stdin
synchronously in a background thread and hand lines to the event
loop through a thread-safe queue. This works the same way on
Windows, Linux, and macOS.

B6 changed two things here. Construction moved into bootstrap, so this
file is transport and nothing else. And EOF on stdin now ends the
process: `write_stdout_loop` was `while True` and was never cancelled,
so `asyncio.gather` waited forever for a writer that had nothing left to
write, the database was never closed, and the single-instance lock was
never released. Go closing the pipe means Go is gone; there is no one
left to talk to.
"""

from __future__ import annotations

import asyncio
import json
import sys
import threading

from bootstrap import AlreadyRunningError, build_runtime, shutdown_runtime
from logging_setup import get_logger
from runtime.runtime import Runtime

_log = get_logger("main")

# The protocol channel is UTF-8, and it has to be said out loud.
#
# On Windows, sys.stdin/sys.stdout over a PIPE default to the ANSI
# codepage (cp1252 on an en-GB/en-US install), not UTF-8 -- verified on
# this machine:  reads 'cp1252' when stdin is not a
# console. Replies happened to survive that by accident, because
# json.dumps defaults to ensure_ascii=True and so emits pure ASCII. The
# REQUEST direction did not: Go's encoding/json writes raw UTF-8, and
# cp1252 either mis-decodes it or cannot decode it at all.
#
# Measured end to end through a real pipe, before this line existed:
#
#   content with an em-dash (U+2014, e2 80 94)
#     -> every byte is defined in cp1252, so it decodes to mojibake
#     -> the write REPORTS ok: True and puts Ã¢â¬â
#        on disk. Silent corruption, reported as success. This is what
#        happened to design\GIT_STRATEGY.md.
#
#   content with U+240F (e2 90 8f), whose bytes 0x90 and 0x8f are
#   UNDEFINED in cp1252
#     -> UnicodeDecodeError inside the stdin reader thread
#     -> the line is dropped, no reply is ever emitted, the process
#        stays alive. Silent failure. This is why design\project_idea.md
#        would not save at all: it contains both of those bytes.
#
# So B32 made the byte layer exact and the transport threw it away one
# layer further out. errors="strict" is deliberate: a decode that
# cannot be trusted must raise where it can be logged, not quietly
# substitute U+FFFD into a payload that is about to be written to
# someone's file.
# Both halves, explicitly. stdout survived cp1252 only by luck:
# json.dumps defaults to ensure_ascii=True, so every reply happened to
# be pure ASCII. That is a property of a default somebody could change
# in one keystroke -- an ensure_ascii=False for readability, or a stray
# print of a non-ASCII path -- and it would reopen this exact bug on
# the other side of the pipe, where the symptom (Go seeing mojibake in
# a file it is about to show) is just as quiet.
#
# errors="strict" on both, deliberately: a decode or encode that cannot
# be trusted must raise where it can be logged, not quietly substitute
# U+FFFD into a payload that is about to become somebody's file.
#
# BE PRECISE ABOUT WHAT EACH OF THESE TWO LINES ACTUALLY DOES TODAY.
# Neither is what makes the regression test pass, and pretending
# otherwise is how a line survives a refactor that breaks it:
#
#   stdin  is superseded by _stdin_reader_thread, which reads
#          sys.stdin.BUFFER and owns the decode itself -- measured:
#          removing this line leaves tests\manual_transport_encoding.py
#          fully green. It stays because nothing should be able to read
#          this process's stdin as TEXT and get the locale codepage,
#          and because the reader's own decode is the thing under test
#          rather than a thing to be assumed.
#
#   stdout cannot be made to fail today at all, because json.dumps
#          defaults to ensure_ascii=True and so every reply is pure
#          ASCII. That accident IS the hazard: one ensure_ascii=False
#          for readability, or one print of a non-ASCII path, reopens
#          this bug on the other half of the pipe where the symptom is
#          just as quiet. This line is the reason that change would be
#          safe instead of a regression, which is worth a line of code
#          that no test can currently turn red.
sys.stdin.reconfigure(encoding="utf-8", errors="strict")
sys.stdout.reconfigure(encoding="utf-8", errors="strict")

# How long to keep working after stdin closes, so a request that arrived
# on the last line still gets its answer written out.
_DRAIN_TIMEOUT_S = 5.0


def _stdin_reader_thread(loop: asyncio.AbstractEventLoop, queue: asyncio.Queue) -> None:
    """Runs in a separate OS thread. Blocking reads are fine here.

    Reads BYTES and decodes each line itself, rather than iterating
    sys.stdin as text, so one undecodable line cannot take the reader
    down with it.

    Iterating the text wrapper raised the decode INSIDE this thread with
    nothing to catch it, and the consequences went well past losing the
    one request. Measured against this backend before the change, by
    feeding it a single line containing the bytes 0xff 0xfe:

        1. baseline request answered  : True
        2. reply to the malformed line: False
        3. request AFTER the bad line : False   <- the thread was dead
        4. clean shutdown on EOF      : False   <- hung forever

    Line 3 is a deaf backend: the thread had exited, so nothing was
    reading stdin any more and every later request was ignored in
    silence. Line 4 is worse, and is why this is a correctness fix
    rather than a robustness nicety -- the EOF signal below is the last
    statement in the function the exception escaped, so
    read_stdin_loop stayed blocked on `await queue.get()` for ever.
    This file's own header explains what that costs: EOF ending the
    process is what closes the database and releases the
    single-instance lock, so a bad byte left the lock held by a live
    pid and every later launch would refuse to start.

    A malformed line is logged and SKIPPED, exactly as read_stdin_loop
    already treats a malformed JSON line one layer down. There is
    deliberately no error reply: the bytes did not decode, so there is
    no envelope id to correlate one to, and an uncorrelated Error is
    something the frontend can only log as unroutable. Stderr is the
    honest destination -- stdout is the protocol channel (CLAUDE.md).
    """
    for raw in sys.stdin.buffer:
        try:
            line = raw.decode("utf-8").strip()
        except UnicodeDecodeError as exc:
            # Resuming the text wrapper after a failed decode is not
            # reliable -- the buffer sits mid-sequence and the
            # incremental decoder keeps state -- which is why this
            # reads bytes and owns the decode.
            _log.warning("Discarded undecodable line from stdin: %s", exc)
            continue
        if line:
            # put_nowait must happen on the event loop thread, so we
            # schedule it via call_soon_threadsafe rather than calling
            # queue.put_nowait directly from this worker thread.
            loop.call_soon_threadsafe(queue.put_nowait, line)
    # stdin closed (Go process exited or EOF) -> signal shutdown
    loop.call_soon_threadsafe(queue.put_nowait, None)


async def read_stdin_loop(runtime: Runtime) -> set[asyncio.Task]:
    """Returns the still-running receive tasks, so the caller can let
    them finish before tearing the runtime down."""
    loop = asyncio.get_running_loop()
    queue: asyncio.Queue[str | None] = asyncio.Queue()
    in_flight: set[asyncio.Task] = set()

    thread = threading.Thread(
        target=_stdin_reader_thread, args=(loop, queue), daemon=True
    )
    thread.start()

    while True:
        line = await queue.get()
        if line is None:  # stdin closed
            break
        try:
            raw = json.loads(line)
        except json.JSONDecodeError as exc:
            # Was a silent drop before B6. A malformed line is either a
            # bug in Go's encoder or something else writing to the pipe,
            # and both are worth a log entry.
            _log.warning("Discarded malformed line from stdin: %s", exc)
            continue
        # Fire-and-forget: receive() creates a Task and returns immediately.
        task = asyncio.create_task(runtime.receive(raw))
        in_flight.add(task)
        task.add_done_callback(in_flight.discard)

    return in_flight


async def write_stdout_loop(runtime: Runtime) -> None:
    """Returns -- never raises -- when stdout stops accepting writes.

    A dead stdout means the reader on the other end is gone, which is the
    same news as EOF on stdin arriving by a different door. Letting the
    OSError escape instead skips shutdown entirely, and skipping shutdown
    is not survivable here: aiosqlite's worker thread is not a daemon, so
    a connection that never gets closed hangs the process at exit
    forever -- and a hung process holds the single-instance lock, so
    every later start refuses with a pid that will never die.

    Windows raises OSError(EINVAL) rather than BrokenPipeError when the
    far end of a pipe closes, so both are caught.
    """
    while True:
        event = await runtime.outbox.get()
        try:
            sys.stdout.write(json.dumps(event.to_dict()) + "\n")
            sys.stdout.flush()
        except (BrokenPipeError, OSError, ValueError) as exc:
            _log.warning("stdout closed (%s); nothing left to write to", exc)
            return


async def _drain(runtime: Runtime, in_flight: set[asyncio.Task]) -> None:
    """Let in-flight handlers finish and the writer flush what they
    produced. Bounded: a hung handler must not stop the process from
    exiting, or the lock never gets released."""
    if in_flight:
        await asyncio.wait(set(in_flight), timeout=_DRAIN_TIMEOUT_S)
    deadline = asyncio.get_running_loop().time() + 1.0
    while not runtime.outbox.empty() and asyncio.get_running_loop().time() < deadline:
        await asyncio.sleep(0.01)


async def main(verbose: bool = False) -> None:
    runtime = await build_runtime(verbose=verbose)
    reader = asyncio.create_task(read_stdin_loop(runtime), name="stdin-reader")
    writer = asyncio.create_task(write_stdout_loop(runtime), name="stdout-writer")
    try:
        # Either end of the pipe closing ends the process. Waiting only on
        # the reader would leave a backend talking to a stdout nobody is
        # holding; waiting on both is the whole "Go is gone" condition.
        done, _ = await asyncio.wait({reader, writer}, return_when=asyncio.FIRST_COMPLETED)
        if reader in done and not reader.cancelled():
            await _drain(runtime, reader.result())
    finally:
        # Whatever happened above, teardown runs. shutdown_runtime is what
        # closes the database and releases the lock, and there is no state
        # this process can be in where skipping that is the better option.
        for task in (reader, writer):
            task.cancel()
        await asyncio.gather(reader, writer, return_exceptions=True)
        await shutdown_runtime(runtime)


if __name__ == "__main__":
    # No argparse: this process speaks a protocol, not a command line, and
    # the one flag it has must never be able to consume a protocol line.
    try:
        asyncio.run(main(verbose="--verbose" in sys.argv[1:]))
    except AlreadyRunningError as exc:
        print(str(exc), file=sys.stderr)
        sys.exit(1)
    except KeyboardInterrupt:
        pass  # clean exit, no traceback
