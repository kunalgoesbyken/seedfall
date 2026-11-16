import json
import random
import threading
from collections import deque
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

import openai
import pytest

from harness.client import ChatClient
from harness.limits import Budget, Throttle
from harness.retry import RetryPolicy


class MockServer:
    def __init__(self):
        self.script = deque()
        self.requests = []
        self.httpd = ThreadingHTTPServer(("127.0.0.1", 0), self._handler())
        self.thread = threading.Thread(target=self.httpd.serve_forever, daemon=True)
        self.thread.start()

    @property
    def base_url(self) -> str:
        return f"http://127.0.0.1:{self.httpd.server_port}/v1"

    def push(self, message=None, *, status=200, headers=None, tokens=10):
        self.script.append((status, headers or {}, message, tokens))

    def push_call(self, name, arguments, call_id="call_1", tokens=10):
        call = {"id": call_id, "type": "function", "function": {"name": name, "arguments": json.dumps(arguments)}}
        self.push({"role": "assistant", "content": None, "tool_calls": [call]}, tokens=tokens)

    def push_text(self, text, tokens=10):
        self.push({"role": "assistant", "content": text}, tokens=tokens)

    def _handler(self):
        server = self

        class Handler(BaseHTTPRequestHandler):
            def log_message(self, *args):
                pass

            def do_POST(self):
                body = json.loads(self.rfile.read(int(self.headers["Content-Length"])))
                server.requests.append(body)
                status, headers, message, tokens = server.script.popleft() if server.script else (500, {}, None, 0)
                if status == 200:
                    payload = {
                        "id": "mock",
                        "object": "chat.completion",
                        "created": 0,
                        "model": body["model"],
                        "choices": [{"index": 0, "finish_reason": "stop", "message": message}],
                        "usage": {"prompt_tokens": tokens // 2, "completion_tokens": tokens - tokens // 2, "total_tokens": tokens},
                    }
                else:
                    payload = {"error": {"message": f"mock status {status}"}}
                raw = json.dumps(payload).encode()
                self.send_response(status)
                self.send_header("Content-Type", "application/json")
                self.send_header("Content-Length", str(len(raw)))
                for name, value in headers.items():
                    self.send_header(name, value)
                self.end_headers()
                self.wfile.write(raw)

        return Handler

    def close(self):
        self.httpd.shutdown()
        self.httpd.server_close()


@pytest.fixture
def server():
    mock = MockServer()
    yield mock
    mock.close()


class Sleeper:
    def __init__(self):
        self.calls = []

    def __call__(self, seconds):
        self.calls.append(seconds)


@pytest.fixture
def sleeper():
    return Sleeper()


@pytest.fixture
def make_client(server, sleeper, tmp_path):
    def build(daily=1000, policy=None, key="test-key"):
        transport = openai.OpenAI(base_url=server.base_url, api_key=key, max_retries=0)
        return ChatClient(
            transport,
            "mock-model",
            Throttle(10_000),
            Budget(tmp_path / "budget.json", daily),
            policy or RetryPolicy(max_attempts=4, base=1.0, cap=8.0),
            sleep=sleeper,
            rng=random.Random(0),
            redact=key,
        )

    return build
