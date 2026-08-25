#!/usr/bin/env python3
"""Drive an interactive program through a real PTY in headless CI.

Nushell asks the terminal for its cursor position before accepting each line.
The ordinary `script` utility allocates a PTY but never answers that request,
so a piped session hangs before its first command. This tiny driver supplies a
valid cursor response and sends one stdin line per prompt.
"""

from __future__ import annotations

import errno
import os
import pty
import select
import signal
import sys
import time


CURSOR_QUERY = b"\x1b[6n"
CURSOR_REPLY = b"\x1b[1;1R"
TIMEOUT_SECONDS = 30


def main() -> int:
    args = sys.argv[1:]
    immediate = args[:1] == ["--immediate"]
    if immediate:
        args = args[1:]
    if not args:
        print("usage: pty-session.py [--immediate] PROGRAM [ARG ...]", file=sys.stderr)
        return 2

    lines = [line.rstrip(b"\r\n") + b"\r" for line in sys.stdin.buffer.readlines()]
    commands = iter(lines)
    child, master = pty.fork()
    if child == 0:
        os.execvp(args[0], args)

    if immediate:
        os.write(master, b"".join(lines))
        commands = iter(())

    deadline = time.monotonic() + TIMEOUT_SECONDS
    try:
        while time.monotonic() < deadline:
            ready, _, _ = select.select([master], [], [], 0.1)
            if ready:
                try:
                    output = os.read(master, 65536)
                except OSError as exc:
                    if exc.errno == errno.EIO:
                        output = b""
                    else:
                        raise
                if output:
                    sys.stdout.buffer.write(output)
                    sys.stdout.buffer.flush()
                    for _ in range(output.count(CURSOR_QUERY)):
                        os.write(master, CURSOR_REPLY)
                        command = next(commands, None)
                        if command is not None:
                            os.write(master, command)

            done, status = os.waitpid(child, os.WNOHANG)
            if done:
                return os.waitstatus_to_exitcode(status)
    finally:
        try:
            os.close(master)
        except OSError:
            pass

    os.kill(child, signal.SIGTERM)
    os.waitpid(child, 0)
    print("interactive session timed out", file=sys.stderr)
    return 124


if __name__ == "__main__":
    raise SystemExit(main())
