#!/usr/bin/env python3
"""Build the single-file static demo of the harness console.

Why a build step at all: the demo must show the REAL front end, not a mock-up of
it. So instead of hand-writing a look-alike page (which silently drifts from the
product every time app.js changes), this script inlines the actual
server/console/app.css and server/console/app.js, and prepends a mock layer that
replaces only fetch() and EventSource.

    python3 docs/demo/build.py            # regenerate docs/demo/harness-console.html

Output properties that matter:
  · one file, no external requests (no CDN, no fonts, no fetch at runtime beyond
    the mocked one) → opens over file:// on a bare-metal box;
  · the UI code path is identical to production, so a demo defect is a real one.
"""
from __future__ import annotations

import pathlib
import re
import sys

HERE = pathlib.Path(__file__).resolve().parent
ROOT = HERE.parent.parent
CONSOLE = ROOT / "server" / "console"
OUT = HERE / "harness-console.html"


def main() -> int:
    index = (CONSOLE / "index.html").read_text(encoding="utf-8")
    css = (CONSOLE / "app.css").read_text(encoding="utf-8")
    app = (CONSOLE / "app.js").read_text(encoding="utf-8")
    mock = (HERE / "mock-harness.js").read_text(encoding="utf-8")

    for name, src in (("app.css", css), ("app.js", app), ("mock-harness.js", mock)):
        if "</style" in src or "</script" in src:
            print(f"refusing to inline {name}: it contains a closing tag literal", file=sys.stderr)
            return 2

    # 1) inline the stylesheet
    # NB: a lambda replacement — the CSS/JS contain backslash sequences that the
    # re module would otherwise try to interpret as group references.
    index, n = re.subn(
        r'<link rel="stylesheet" href="/console/app\.css" />',
        lambda _m: "<style>\n" + css + "\n</style>",
        index,
    )
    if n != 1:
        print("stylesheet link not found in index.html", file=sys.stderr)
        return 2

    # 2) inline the mock layer + the real app, in that order
    index, n = re.subn(
        r'<script src="/console/app\.js"></script>',
        lambda _m: "<script>\n" + mock + "\n</script>\n<script>\n" + app + "\n</script>",
        index,
    )
    if n != 1:
        print("app script tag not found in index.html", file=sys.stderr)
        return 2

    # 3) mark it as a demo in the title so a screenshot is never mistaken for live
    index = index.replace(
        "<title>artex-harness</title>",
        "<title>artex-harness · 静态演示</title>\n"
        "<!-- 由 docs/demo/build.py 生成：内联 server/console 的真实 CSS/JS + mock 数据层。\n"
        "     不要手改本文件，改源文件后重新生成。 -->",
    )

    OUT.write_text(index, encoding="utf-8")
    size = OUT.stat().st_size
    print(f"wrote {OUT.relative_to(ROOT)} ({size:,} bytes)")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
