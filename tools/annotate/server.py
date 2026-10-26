#!/usr/bin/env python3
"""tau annotate: serve an HTML file from the share dir with a comment overlay.

Comments made in the browser are pasted into a Pi tmux session in the tau
container. Runs on the host; stdlib only. Started by `tau annotate`.
"""

import argparse
import json
import mimetypes
import re
import secrets
import subprocess
import sys
import threading
import time
import webbrowser
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path
from urllib.parse import quote, unquote, urlsplit

HERE = Path(__file__).resolve().parent
MAX_BODY = 64 * 1024
MAX_QUOTE = 2000
MAX_COMMENT = 8000
MAX_LABEL = 300
# Everything except \t and \n. Strips ESC too, so a comment can't close the
# bracketed paste early and have its tail run as keystrokes.
CONTROL = re.compile(r"[\x00-\x08\x0b-\x1f\x7f-\x9f]")


def fail(message: str) -> None:
    print(f"tau: {message}", file=sys.stderr)
    sys.exit(1)


def clean(value: object, limit: int) -> str:
    text = value if isinstance(value, str) else ""
    text = text.replace("\r\n", "\n").replace("\r", "\n")
    return CONTROL.sub("", text).strip()[:limit]


def resolve_file(share: Path, arg: str) -> Path:
    """Map a user-supplied path to a real HTML file inside the share dir."""
    for prefix in ("~/share/", "/home/pi/share/"):
        if arg.startswith(prefix):
            arg = arg[len(prefix):]
            candidates = [share / arg]
            break
    else:
        candidates = [Path(arg).expanduser(), share / arg]
    for candidate in candidates:
        if not candidate.is_file():
            continue
        real = candidate.resolve()
        if share not in real.parents:
            fail(f"{arg} is outside the share dir ({share})")
        if real.suffix.lower() not in (".html", ".htm"):
            fail(f"{arg} is not an HTML file")
        return real
    fail(f"no such file in the share dir ({share}): {arg}")


class Annotator:
    def __init__(self, args: argparse.Namespace, page: Path, share: Path):
        self.session = args.session
        self.draft = args.draft
        self.tmux = args.tmux
        self.page = page
        self.root = share
        self.rel = page.relative_to(share).as_posix()
        self.token = secrets.token_urlsafe(24)
        self.overlay = (HERE / "overlay.js").read_text()
        self.lock = threading.Lock()
        self.port = 0

    def hosts(self) -> set:
        return {f"127.0.0.1:{self.port}", f"localhost:{self.port}"}

    def run_tmux(self, *argv: str) -> None:
        proc = subprocess.run(
            [*self.tmux, *argv], capture_output=True, text=True, timeout=15
        )
        if proc.returncode != 0:
            raise RuntimeError(proc.stderr.strip() or "tmux failed")

    def message(self, data: dict) -> str:
        label = clean(data.get("sectionPath"), MAX_LABEL)
        anchor = clean(data.get("sectionId"), 100)
        quote_text = clean(data.get("quote"), MAX_QUOTE)
        comment = clean(data.get("comment"), MAX_COMMENT)
        if not comment:
            raise ValueError("empty comment")
        where = f'section "{label}"' if label else "the whole document"
        if anchor:
            where += f" (#{anchor})"
        lines = [f"[annotate] Feedback on ~/share/{self.rel}, {where}"]
        if quote_text:
            lines += [f"> {line}" for line in quote_text.split("\n")]
        lines += ["", comment]
        return "\n".join(lines)

    def deliver(self, text: str) -> None:
        target = f"={self.session}:"
        buf = f"tau-annotate-{secrets.token_hex(4)}"
        with self.lock:
            try:
                self.run_tmux("has-session", "-t", f"={self.session}")
            except RuntimeError:
                raise RuntimeError(f"session '{self.session}' is gone") from None
            self.run_tmux("set-buffer", "-b", buf, "--", text)
            self.run_tmux("paste-buffer", "-p", "-d", "-b", buf, "-t", target)
            if not self.draft:
                time.sleep(0.15)
                self.run_tmux("send-keys", "-t", target, "Enter")


def make_handler(app: Annotator):
    class Handler(BaseHTTPRequestHandler):
        server_version = "tau-annotate"

        def log_message(self, *_args) -> None:
            pass

        def host_ok(self) -> bool:
            # Blocks DNS rebinding: only our own origin names are accepted.
            return self.headers.get("Host", "") in app.hosts()

        def reply(self, code: int, body: bytes, ctype: str) -> None:
            self.send_response(code)
            self.send_header("Content-Type", ctype)
            self.send_header("Content-Length", str(len(body)))
            self.send_header("Cache-Control", "no-store")
            self.send_header("X-Content-Type-Options", "nosniff")
            self.end_headers()
            self.wfile.write(body)

        def reply_json(self, code: int, payload: dict) -> None:
            self.reply(code, json.dumps(payload).encode(), "application/json")

        def do_GET(self) -> None:
            if not self.host_ok():
                return self.reply(403, b"forbidden", "text/plain")
            path = unquote(urlsplit(self.path).path)
            if path == "/":
                self.send_response(302)
                self.send_header("Location", "/" + quote(app.rel))
                self.end_headers()
                return
            target = (app.root / path.lstrip("/")).resolve()
            if app.root not in target.parents or not target.is_file():
                return self.reply(404, b"not found", "text/plain")
            body = target.read_bytes()
            if target == app.page:
                return self.reply(200, self.inject(body), "text/html; charset=utf-8")
            ctype = mimetypes.guess_type(target.name)[0] or "application/octet-stream"
            self.reply(200, body, ctype)

        def inject(self, body: bytes) -> bytes:
            config = json.dumps({"token": app.token, "session": app.session})
            config = config.replace("<", "\\u003c")
            tag = (
                f"<script>window.__TAU_ANNOTATE={config};</script>"
                f"<script>{app.overlay}</script>"
            ).encode()
            at = body.lower().rfind(b"</body>")
            if at == -1:
                return body + tag
            return body[:at] + tag + body[at:]

        def do_POST(self) -> None:
            if not self.host_ok() or urlsplit(self.path).path != "/__annotate/send":
                return self.reply(403, b"forbidden", "text/plain")
            origin = self.headers.get("Origin")
            if origin is not None and origin.removeprefix("http://") not in app.hosts():
                return self.reply(403, b"forbidden", "text/plain")
            if not secrets.compare_digest(
                self.headers.get("X-Annotate-Token", ""), app.token
            ):
                return self.reply(403, b"forbidden", "text/plain")
            try:
                length = int(self.headers.get("Content-Length", "0"))
                if not 0 < length <= MAX_BODY:
                    raise ValueError("bad request size")
                data = json.loads(self.rfile.read(length))
                if not isinstance(data, dict):
                    raise ValueError("bad request")
                app.deliver(app.message(data))
            except (ValueError, RuntimeError, subprocess.SubprocessError) as exc:
                return self.reply_json(400, {"ok": False, "error": str(exc)})
            self.reply_json(200, {"ok": True})

    return Handler


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("--share", required=True)
    parser.add_argument("--file", required=True)
    parser.add_argument("--session", required=True)
    parser.add_argument("--port", type=int, default=0)
    parser.add_argument("--draft", action="store_true")
    parser.add_argument("--no-open", action="store_true")
    parser.add_argument("tmux", nargs=argparse.REMAINDER)
    args = parser.parse_args()
    args.tmux = [a for a in args.tmux if a != "--"]
    if not args.tmux:
        fail("missing tmux command")

    share = Path(args.share).resolve()
    page = resolve_file(share, args.file)
    app = Annotator(args, page, share)
    server = ThreadingHTTPServer(("127.0.0.1", args.port), make_handler(app))
    app.port = server.server_address[1]
    url = f"http://127.0.0.1:{app.port}/{quote(app.rel)}"

    mode = "typed into the input, not submitted" if args.draft else "sent"
    print(f"annotating ~/share/{app.rel} -> session '{app.session}' ({mode})", flush=True)
    print(f"{url}\nCtrl-C to stop", flush=True)
    if not args.no_open:
        webbrowser.open(url)
    try:
        server.serve_forever()
    except KeyboardInterrupt:
        print()


if __name__ == "__main__":
    main()
