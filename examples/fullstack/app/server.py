import http.server
import os
import socket


class Handler(http.server.BaseHTTPRequestHandler):
    def do_GET(self):
        peer = os.environ.get("PEER", "db")
        try:
            addr = socket.gethostbyname(peer)
        except OSError as exc:
            addr = f"unresolved ({exc})"
        secret = "missing"
        try:
            with open("/run/secrets/api_token") as fh:
                secret = fh.read().strip()
        except OSError:
            pass
        body = f"banner={os.environ.get('BANNER')} peer={peer}={addr} secret={secret}\n"
        self.send_response(200)
        self.send_header("Content-Type", "text/plain")
        self.end_headers()
        self.wfile.write(body.encode())

    def log_message(self, fmt, *args):
        print(self.address_string(), fmt % args, flush=True)


http.server.ThreadingHTTPServer(("0.0.0.0", 8000), Handler).serve_forever()
