#!/usr/bin/env python3
"""Drive the swarmlens TUI inside a pty, exercising the interactive
features, and print the rendered frame after each step."""
import os, pty, select, struct, termios, time, sys, re

COLS, ROWS = 110, 32
ANSI = re.compile(r"\x1b\[[0-9;?]*[a-zA-Z]|\x1b\][^\x07]*\x07|\x1b[=78MDHE]")


def read_for(fd, dur):
    buf = b""
    end = time.time() + dur
    while time.time() < end:
        r, _, _ = select.select([fd], [], [], 0.1)
        if fd in r:
            try:
                data = os.read(fd, 65536)
            except OSError:
                break
            if not data:
                break
            buf += data
    return buf


def clean(raw):
    text = raw.decode("utf-8", "replace")
    text = ANSI.sub("", text)
    # bubbletea repaints: keep the last full frame
    frames = text.split("\x1b")  # already stripped; split on cursor moves lost above
    return text


def last_frame(raw):
    """Extract the last painted screen from a raw pty chunk."""
    text = ANSI.sub("", raw.decode("utf-8", "replace"))
    # Frames are separated by the cursor-home sequence which is stripped,
    # so fall back to splitting on \r and keeping the longest coherent block.
    parts = re.split(r"[\r\n]+", text)
    lines = [p for p in parts]
    # Reconstruct: take the tail of the buffer (last frame wins).
    return lines


def main():
    pid, fd = pty.fork()
    if pid == 0:
        import fcntl
        fcntl.ioctl(0, termios.TIOCSWINSZ, struct.pack("HHHH", ROWS, COLS, 0, 0))
        os.execvp("./swarmlens", ["./swarmlens"])

    import fcntl
    fcntl.ioctl(fd, termios.TIOCSWINSZ, struct.pack("HHHH", ROWS, COLS, 0, 0))

    steps = [
        ("initial render", None, 3.0),
        ("command palette", b":", 1.5),
        ("palette filter 'scale'", b"scale", 1.0),
        ("palette closed", b"\x1b", 1.0),
        ("search mode", b"/", 1.0),
        ("search 'demo'", b"demo", 1.5),
        ("search cleared", b"\x1b", 1.5),
        ("service detail", b"\r", 2.0),
        ("back to services", b"\x1b", 1.5),
        ("confirm modal (x)", b"x", 1.5),
        ("confirm cancelled (n)", b"n", 1.5),
        ("nodes view", b"n", 2.0),
        ("volumes view", b"v", 2.0),
        ("networks view", b"w", 2.0),
        ("services view", b"s", 1.5),
        ("quit", b"q", 1.5),
    ]

    for name, keys, wait in steps:
        if keys:
            os.write(fd, keys)
        raw = read_for(fd, wait)
        text = ANSI.sub("", raw.decode("utf-8", "replace"))
        # Show the meaningful tail of the painted output.
        lines = [l.rstrip() for l in re.split(r"[\r\n]+", text)]
        lines = [l for l in lines if l.strip()]
        print(f"\n===== {name} =====")
        for l in lines[-14:]:
            print("  " + l[:COLS])
        sys.stdout.flush()

    try:
        os.waitpid(pid, 0)
    except ChildProcessError:
        pass


main()
