import json
from pathlib import Path
import subprocess
import sys
import threading
import tempfile
import time
import unittest
import urllib.error
import urllib.request

import fake_upstream as fake


def tool(name, **properties):
    return {"type": "function", "function": {"name": name, "parameters": {
        "type": "object", "properties": properties, "required": list(properties),
    }}}


class ToolCallTests(unittest.TestCase):
    def test_restart_clears_old_requests(self):
        with tempfile.TemporaryDirectory() as root:
            log = Path(root) / "requests.jsonl"
            log.write_text("stale tool result\n")
            process = subprocess.Popen([sys.executable, fake.__file__, "0", str(log)])
            try:
                deadline = time.monotonic() + 5
                while log.read_text() and time.monotonic() < deadline:
                    time.sleep(0.01)
                self.assertEqual(log.read_text(), "")
            finally:
                process.terminate()
                process.wait(timeout=5)

    def test_continue_write_uses_advertised_filepath(self):
        tools = [tool("Read", filepath={"type": "string"}),
                 tool("Write", filepath={"type": "string"}, content={"type": "string"})]
        name, args = fake.tool_call(tools, "Write")
        self.assertEqual(name, "Write")
        self.assertEqual(json.loads(args), {"filepath": "tool.txt", "content": "tool-ok\n"})

    def test_codex_shell_variants(self):
        for name, key, kind in [("shell", "command", "array"),
                                ("exec_command", "cmd", "string"),
                                ("shell_command", "command", "string")]:
            with self.subTest(name=name):
                actual, args = fake.tool_call([tool(name, **{key: {"type": kind}})], "shell")
                self.assertEqual(actual, name)
                value = json.loads(args)[key]
                self.assertIsInstance(value, list if kind == "array" else str)
                self.assertIn("tool-ok > tool.txt", str(value))

    def test_missing_tool_does_not_fall_back_to_read(self):
        with self.assertRaisesRegex(ValueError, "expected tool 'Write'"):
            fake.tool_call([tool("Read", filepath={"type": "string"})], "Write")

    def test_changed_schema_is_rejected(self):
        with self.assertRaisesRegex(ValueError, "missing required arguments: new_path"):
            fake.tool_call([tool("Write", new_path={"type": "string"})], "Write")

    def test_http_missing_tool_returns_error_instead_of_done(self):
        with tempfile.NamedTemporaryFile() as log:
            fake.LOG = log.name
            fake.SCRIPT = "Write"
            server = fake.http.server.ThreadingHTTPServer(("127.0.0.1", 0), fake.H)
            thread = threading.Thread(target=server.serve_forever)
            thread.start()
            try:
                body = {"messages": [{"role": "user", "content": "create tool.txt"}], "tools": []}
                request = urllib.request.Request(
                    f"http://127.0.0.1:{server.server_port}/v1/chat/completions",
                    data=json.dumps(body).encode(), headers={"Content-Type": "application/json"})
                with self.assertRaises(urllib.error.HTTPError) as error:
                    urllib.request.urlopen(request)
                self.assertEqual(error.exception.code, 422)
                self.assertIn("expected tool 'Write'", error.exception.read().decode())
            finally:
                server.shutdown()
                server.server_close()
                thread.join()


if __name__ == "__main__":
    unittest.main()
