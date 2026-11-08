#!/usr/bin/env python3
"""termshot: run a TUI in a pty, drive it with a key script, and render the
emulated screen to text, a style dump and a PNG after every `shot`.

It exists to check what a terminal UI really looks like (colours, borders,
alignment) instead of guessing from stripped output; swarmlens' layout was
compared against k9s with it.

    python3 -m venv .venv && .venv/bin/pip install pyte pillow
    .venv/bin/python scripts/termshot.py --cols 140 --rows 38 --out /tmp/shots \
        scripts/tour.script -- ./swarmlens --demo

Environment:
    TERMSHOT_FONT, TERMSHOT_FONT_BOLD   monospace fonts for the PNGs
    TERMSHOT_TERM, TERMSHOT_COLORTERM   what the app sees (default xterm-256color / truecolor;
                                        an empty COLORTERM tests a 256-colour terminal)

usage: termshot.py [--cols N] [--rows N] [--out DIR] [--bg HEX] SCRIPT -- CMD [ARGS...]

SCRIPT is a file of lines:
    wait <seconds>        let the app run / repaint
    shot <name>           write DIR/<name>.png and DIR/<name>.txt
    key <name>            enter esc tab up down left right pgup pgdn home end
                          space backspace ctrl-<x> alt-<x> shift-tab f1..f12
    type <text...>        literal characters (rest of the line, verbatim)
    resize <cols> <rows>  change the window size (SIGWINCH)
    # comment
"""
import fcntl
import os
import pty
import re
import select
import signal
import struct
import sys
import termios
import time

import pyte
from PIL import Image, ImageDraw, ImageFont

FONT_SIZE = 15
FONT_CANDIDATES = [
    ("/usr/share/fonts/TTF/JetBrainsMonoNerdFontMono-Regular.ttf", "/usr/share/fonts/TTF/JetBrainsMonoNerdFontMono-Bold.ttf"),
    ("/usr/share/fonts/TTF/DejaVuSansMono.ttf", "/usr/share/fonts/TTF/DejaVuSansMono-Bold.ttf"),
    ("/usr/share/fonts/truetype/dejavu/DejaVuSansMono.ttf", "/usr/share/fonts/truetype/dejavu/DejaVuSansMono-Bold.ttf"),
    ("/System/Library/Fonts/Menlo.ttc", "/System/Library/Fonts/Menlo.ttc"),
]


def find_fonts():
    env_regular, env_bold = os.environ.get("TERMSHOT_FONT"), os.environ.get("TERMSHOT_FONT_BOLD")
    if env_regular:
        return env_regular, env_bold or env_regular
    for regular, bold in FONT_CANDIDATES:
        if os.path.exists(regular):
            return regular, bold if os.path.exists(bold) else regular
    raise SystemExit("no monospace font found: set TERMSHOT_FONT")


FONT, FONT_B = find_fonts()

# Typical dark terminal theme for the 16 ANSI colours.
ANSI16 = {
    "black": "#1d1f21", "red": "#cc6666", "green": "#b5bd68", "brown": "#f0c674",
    "blue": "#81a2be", "magenta": "#b294bb", "cyan": "#8abeb7", "white": "#c5c8c6",
    "brightblack": "#666666", "brightred": "#d54e53", "brightgreen": "#b9ca4a",
    "brightbrown": "#e7c547", "brightyellow": "#e7c547", "brightblue": "#7aa6da",
    "brightmagenta": "#c397d8", "brightcyan": "#70c0b1", "brightwhite": "#eaeaea",
}

KEYS = {
    "enter": b"\r", "esc": b"\x1b", "tab": b"\t", "space": b" ",
    "backspace": b"\x7f", "up": b"\x1b[A", "down": b"\x1b[B",
    "right": b"\x1b[C", "left": b"\x1b[D", "home": b"\x1b[H", "end": b"\x1b[F",
    "pgup": b"\x1b[5~", "pgdn": b"\x1b[6~", "delete": b"\x1b[3~",
    "shift-tab": b"\x1b[Z",
}
for i in range(1, 13):
    KEYS[f"f{i}"] = [b"\x1bOP", b"\x1bOQ", b"\x1bOR", b"\x1bOS", b"\x1b[15~",
                     b"\x1b[17~", b"\x1b[18~", b"\x1b[19~", b"\x1b[20~",
                     b"\x1b[21~", b"\x1b[23~", b"\x1b[24~"][i - 1]


def key_bytes(name):
    if name in KEYS:
        return KEYS[name]
    m = re.fullmatch(r"ctrl-(.)", name)
    if m:
        return bytes([ord(m.group(1).lower()) & 0x1F])
    m = re.fullmatch(r"alt-(.)", name)
    if m:
        return b"\x1b" + m.group(1).encode()
    raise SystemExit(f"unknown key {name!r}")


class Term:
    def __init__(self, cols, rows, cmd, bg):
        self.cols, self.rows = cols, rows
        self.bg_reply = bg
        self.screen = pyte.Screen(cols, rows)
        self.stream = pyte.ByteStream(self.screen)
        env = dict(os.environ, TERM=os.environ.get("TERMSHOT_TERM", "xterm-256color"),
                   COLORTERM=os.environ.get("TERMSHOT_COLORTERM", "truecolor"),
                   LANG="en_US.UTF-8", COLUMNS=str(cols), LINES=str(rows))
        if not env["COLORTERM"]:
            del env["COLORTERM"]
        self.pid, self.fd = pty.fork()
        if self.pid == 0:
            fcntl.ioctl(0, termios.TIOCSWINSZ, struct.pack("HHHH", rows, cols, 0, 0))
            os.execvpe(cmd[0], cmd, env)
        fcntl.ioctl(self.fd, termios.TIOCSWINSZ, struct.pack("HHHH", rows, cols, 0, 0))
        self.tail = b""

    def _answer_queries(self, data):
        """Answer terminal queries (bubbletea/termenv ask for the background
        colour at start-up). Each query is answered exactly once, even when
        it straddles two reads."""
        old = self.tail
        buf = old + data
        r, g, b = (int(self.bg_reply[i:i + 2], 16) for i in (1, 3, 5))
        replies = [
            (b"\x1b]11;?", f"\x1b]11;rgb:{r:02x}{r:02x}/{g:02x}{g:02x}/{b:02x}{b:02x}\x1b\\".encode()),
            (b"\x1b]10;?", b"\x1b]10;rgb:cccc/cccc/cccc\x1b\\"),
            (b"\x1b[6n", b"\x1b[1;1R"),
            (b"\x1b[c", b"\x1b[?62;c"),
            (b"\x1b[0c", b"\x1b[?62;c"),
        ]
        for pat, reply in replies:
            pos = 0
            while True:
                i = buf.find(pat, pos)
                if i < 0:
                    break
                if i + len(pat) > len(old):  # completes in the new data
                    os.write(self.fd, reply)
                pos = i + 1
        self.tail = buf[-12:]

    def pump(self, seconds):
        end = time.time() + seconds
        while time.time() < end:
            r, _, _ = select.select([self.fd], [], [], 0.05)
            if self.fd in r:
                try:
                    data = os.read(self.fd, 65536)
                except OSError:
                    return False
                if not data:
                    return False
                self._answer_queries(data)
                self.stream.feed(data)
        return True

    def send(self, data):
        os.write(self.fd, data)

    def resize(self, cols, rows):
        self.cols, self.rows = cols, rows
        self.screen.resize(rows, cols)
        fcntl.ioctl(self.fd, termios.TIOCSWINSZ, struct.pack("HHHH", rows, cols, 0, 0))
        os.kill(self.pid, signal.SIGWINCH)

    def close(self):
        """Stop the app. The pty must keep being drained meanwhile: an app
        that repaints into a full pty blocks in write and never sees the
        signal."""
        try:
            os.kill(self.pid, signal.SIGTERM)
        except ProcessLookupError:
            return
        deadline = time.time() + 3
        while time.time() < deadline:
            try:
                pid, _ = os.waitpid(self.pid, os.WNOHANG)
            except ChildProcessError:
                return
            if pid:
                return
            self.pump(0.1)
        try:
            os.kill(self.pid, signal.SIGKILL)
            os.waitpid(self.pid, 0)
        except (ProcessLookupError, ChildProcessError):
            pass


def color(spec, default):
    if spec == "default":
        return default
    if spec in ANSI16:
        return ANSI16[spec]
    if re.fullmatch(r"[0-9a-fA-F]{6}", spec):
        return "#" + spec
    return default


def text_dump(screen):
    lines = []
    for y in range(screen.lines):
        row = screen.buffer[y]
        lines.append("".join(row[x].data or " " for x in range(screen.columns)).rstrip())
    while lines and not lines[-1]:
        lines.pop()
    return "\n".join(lines)


def attr_dump(screen):
    """Runs of identical styling per row: y x0-x1 fg bg flags 'text'."""
    out = []
    for y in range(screen.lines):
        row = screen.buffer[y]
        x = 0
        while x < screen.columns:
            c = row[x]
            key = (c.fg, c.bg, c.bold, c.reverse, c.italics, c.underscore)
            x1 = x
            text = ""
            while x1 < screen.columns:
                d = row[x1]
                if (d.fg, d.bg, d.bold, d.reverse, d.italics, d.underscore) != key:
                    break
                text += d.data or " "
                x1 += 1
            if text.strip():
                flags = "".join(f for f, on in (("B", c.bold), ("R", c.reverse), ("I", c.italics), ("U", c.underscore)) if on)
                out.append(f"y={y:<2} x={x:<3}-{x1 - 1:<3} fg={c.fg:<12} bg={c.bg:<8} {flags:<3} {text.strip()!r}")
            x = x1
    return "\n".join(out)


def render_png(screen, path, default_bg):
    font = ImageFont.truetype(FONT, FONT_SIZE)
    bold = ImageFont.truetype(FONT_B, FONT_SIZE)
    cw = round(font.getlength("M"))
    asc, desc = font.getmetrics()
    ch = asc + desc + 3
    img = Image.new("RGB", (cw * screen.columns, ch * screen.lines), default_bg)
    d = ImageDraw.Draw(img)
    default_fg = "#c5c8c6"
    for y in range(screen.lines):
        row = screen.buffer[y]
        x = 0
        while x < screen.columns:
            c = row[x]
            fg = color(c.fg, default_fg)
            bg = color(c.bg, default_bg)
            if c.reverse:
                fg, bg = bg, fg
            wide = (x + 1 < screen.columns and row[x + 1].data == "" and c.data != "")
            w = cw * (2 if wide else 1)
            if bg != default_bg:
                d.rectangle([x * cw, y * ch, x * cw + w - 1, (y + 1) * ch - 1], fill=bg)
            if c.data and c.data != " ":
                f = bold if c.bold else font
                d.text((x * cw, y * ch + 1), c.data, font=f, fill=fg)
            if c.underscore:
                d.line([x * cw, (y + 1) * ch - 2, x * cw + w - 1, (y + 1) * ch - 2], fill=fg)
            x += 2 if wide else 1
    img.save(path)


def main():
    argv = sys.argv[1:]
    cols, rows, out, bg = 140, 40, ".", "#1d1f21"
    while argv and argv[0].startswith("--") and argv[0] != "--":
        opt = argv.pop(0)
        val = argv.pop(0)
        if opt == "--cols":
            cols = int(val)
        elif opt == "--rows":
            rows = int(val)
        elif opt == "--out":
            out = val
        elif opt == "--bg":
            bg = val
        else:
            raise SystemExit(f"unknown option {opt}")
    script = argv.pop(0)
    if not argv or argv[0] != "--":
        raise SystemExit("missing -- CMD")
    cmd = argv[1:]
    os.makedirs(out, exist_ok=True)

    t = Term(cols, rows, cmd, bg)
    alive = t.pump(1.0)
    for raw in open(script, encoding="utf-8"):
        line = raw.rstrip("\n")
        if not line.strip() or line.lstrip().startswith("#"):
            continue
        op, _, arg = line.partition(" ")
        if op == "wait":
            alive = t.pump(float(arg))
        elif op == "key":
            for k in arg.split():
                t.send(key_bytes(k))
                alive = t.pump(0.15)
        elif op == "type":
            t.send(arg.encode())
            alive = t.pump(0.3)
        elif op == "resize":
            c, r = arg.split()
            t.resize(int(c), int(r))
            alive = t.pump(0.5)
        elif op == "shot":
            t.pump(0.4)
            name = arg.strip()
            render_png(t.screen, os.path.join(out, name + ".png"), bg)
            with open(os.path.join(out, name + ".txt"), "w", encoding="utf-8") as fh:
                fh.write(text_dump(t.screen) + "\n")
            with open(os.path.join(out, name + ".attrs.txt"), "w", encoding="utf-8") as fh:
                fh.write(attr_dump(t.screen) + "\n")
            print(f"shot {name} (alive={alive})")
        else:
            raise SystemExit(f"unknown op {op!r}")
    t.close()


if __name__ == "__main__":
    main()
