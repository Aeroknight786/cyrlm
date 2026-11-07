# Scripted OpenAI chat-completions upstream: logs every request body and
# answers with a tool call for the first request of a conversation, text after.
import json, sys, http.server, os

LOG = ""
SCRIPT = os.environ.get("FAKE_TOOL", "shell")


def tool_call(tools, script):
    # Fail on tool/schema drift instead of calling an unrelated first tool and
    # treating its error as a successful round trip.
    functions = {t["function"]["name"]: t["function"] for t in tools if t.get("type") == "function"}
    candidates = ("shell", "exec_command", "shell_command") if script == "shell" else (script,)
    name = next((n for n in candidates if n in functions), None)
    if name is None:
        raise ValueError(f"expected tool {script!r}; advertised: {', '.join(functions)}")
    command = "echo tool-ok > tool.txt"
    if name in ("Bash", "bash"):
        args = {"command": command, "description": "write file"}
    elif name in ("run_shell_command", "shell_command"):
        args = {"command": command}
    elif name == "run_commands":
        args = {"commands": [command]}
    elif name == "Write":
        args = {"filepath": "tool.txt", "content": "tool-ok\n"}
    elif name == "shell":
        args = {"command": ["bash", "-lc", command]}
    elif name == "exec_command":
        args = {"cmd": command}
    else:
        raise ValueError(f"no fixture for tool {name!r}")
    schema = functions[name].get("parameters", {})
    properties = schema.get("properties", {})
    args = {k: v for k, v in args.items() if k in properties}
    missing = set(schema.get("required", [])) - args.keys()
    if missing:
        raise ValueError(f"{name} fixture missing required arguments: {', '.join(sorted(missing))}")
    return name, json.dumps(args)


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
        has_tool_result = any(m.get("role") == "tool" for m in msgs)
        greeting = "HARNESS_E2E_GREETING" in json.dumps(msgs)
        call = None
        if not has_tool_result and SCRIPT != "aider" and not greeting:
            try:
                call = tool_call(body.get("tools", []), SCRIPT)
            except ValueError as e:
                self.send_response(422); self.send_header("Content-Type", "application/json"); self.end_headers()
                self.wfile.write(json.dumps({"error": {"message": str(e)}}).encode())
                return
        self.send_response(200)
        self.send_header("Content-Type", "text/event-stream")
        self.end_headers()

        def send(d):
            self.wfile.write(b"data: " + json.dumps(d).encode() + b"\n\n")
            self.wfile.flush()

        base = {"id": "c1", "object": "chat.completion.chunk", "model": body.get("model")}
        if call:
            name, args = call
            send({**base, "choices": [{"index": 0, "delta": {"role": "assistant", "tool_calls": [{"index": 0, "id": "call_1", "type": "function", "function": {"name": name, "arguments": ""}}]}}]})
            send({**base, "choices": [{"index": 0, "delta": {"tool_calls": [{"index": 0, "function": {"arguments": args}}]}}]})
            send({**base, "choices": [{"index": 0, "delta": {}, "finish_reason": "tool_calls"}], "usage": {"prompt_tokens": 100, "completion_tokens": 20}})
        else:
            content = "Hello from the fake upstream." if greeting else "Done: the tool ran."
            if SCRIPT == "aider" and not has_tool_result and not greeting:
                content = "tool.txt\n```\ntool-ok\n```" if not any("FAIL: first run" in str(m) for m in msgs) else "Done: the tool ran."
            send({**base, "choices": [{"index": 0, "delta": {"role": "assistant", "content": content}}]})
            send({**base, "choices": [{"index": 0, "delta": {}, "finish_reason": "stop"}], "usage": {"prompt_tokens": 120, "completion_tokens": 6}})
        self.wfile.write(b"data: [DONE]\n\n")


if __name__ == "__main__":
    LOG = sys.argv[2]
    # Each invocation owns this log; reruns must not mix old tool results in.
    with open(LOG, "w"):
        pass
    http.server.ThreadingHTTPServer(("127.0.0.1", int(sys.argv[1])), H).serve_forever()
