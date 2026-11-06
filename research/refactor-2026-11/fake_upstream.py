# Scripted OpenAI chat-completions upstream: logs every request body and
# answers with a tool call for the first request of a conversation, text after.
import json, sys, http.server, os

LOG = sys.argv[2]
SCRIPT = os.environ.get("FAKE_TOOL", "shell")


class H(http.server.BaseHTTPRequestHandler):
    def log_message(self, *a):
        pass

    def do_GET(self):
        out = {"data": [{"id": "qwen3.5:9b-cyrlm"}]} if self.path.startswith("/v1/models") else {"status": "ok"}
        if self.path.startswith("/props"):
            out = {"default_generation_settings": {"n_ctx": 65536}}
        b = json.dumps(out).encode()
        self.send_response(200); self.send_header("Content-Type", "application/json"); self.end_headers(); self.wfile.write(b)

    def do_POST(self):
        body = json.loads(self.rfile.read(int(self.headers["Content-Length"])))
        with open(LOG, "a") as f:
            f.write(json.dumps(body) + "\n")
        msgs = body.get("messages", [])
        # Strict Qwen-style template: one system message, first.
        roles = [m["role"] for m in msgs]
        if roles.count("system") > 1 or ("system" in roles and roles[0] != "system"):
            self.send_response(500); self.send_header("Content-Type", "application/json"); self.end_headers()
            self.wfile.write(b'{"error":{"code":500,"message":"Jinja Exception: System message must be at the beginning.","type":"server_error"}}')
            return
        tools = [t["function"]["name"] for t in body.get("tools", []) if t.get("type") == "function"]
        has_tool_result = any(m.get("role") == "tool" for m in msgs)
        self.send_response(200)
        self.send_header("Content-Type", "text/event-stream")
        self.end_headers()

        def send(d):
            self.wfile.write(b"data: " + json.dumps(d).encode() + b"\n\n")
            self.wfile.flush()

        base = {"id": "c1", "object": "chat.completion.chunk", "model": body.get("model")}
        if not has_tool_result and tools:
            name = SCRIPT if SCRIPT in tools else tools[0]
            if name == "apply_patch":
                args = json.dumps({"input": "*** Begin Patch\n*** Add File: hello.txt\n+hello from cyrlm\n*** End Patch\n"})
            elif name in ("shell", "exec_command", "shell_command"):
                args = json.dumps({"command": ["bash", "-lc", "echo tool-ok > tool.txt; ls"]} if name == "shell" else {"cmd": "echo tool-ok > tool.txt; ls", "command": "echo tool-ok > tool.txt; ls"})
            else:
                args = "{}"
            send({**base, "choices": [{"index": 0, "delta": {"role": "assistant", "tool_calls": [{"index": 0, "id": "call_1", "type": "function", "function": {"name": name, "arguments": ""}}]}}]})
            send({**base, "choices": [{"index": 0, "delta": {"tool_calls": [{"index": 0, "function": {"arguments": args}}]}}]})
            send({**base, "choices": [{"index": 0, "delta": {}, "finish_reason": "tool_calls"}], "usage": {"prompt_tokens": 100, "completion_tokens": 20}})
        else:
            send({**base, "choices": [{"index": 0, "delta": {"role": "assistant", "content": "Done: the tool ran."}}]})
            send({**base, "choices": [{"index": 0, "delta": {}, "finish_reason": "stop"}], "usage": {"prompt_tokens": 120, "completion_tokens": 6}})
        self.wfile.write(b"data: [DONE]\n\n")


http.server.ThreadingHTTPServer(("127.0.0.1", int(sys.argv[1])), H).serve_forever()
