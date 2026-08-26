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

# How long to keep working after stdin closes, so a request that arrived
# on the last line still gets its answer written out.
_DRAIN_TIMEOUT_S = 5.0


def _stdin_reader_thread(loop: asyncio.AbstractEventLoop, queue: asyncio.Queue) -> None:
    """Runs in a separate OS thread. Blocking reads are fine here."""
    for line in sys.stdin:
        line = line.strip()
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
