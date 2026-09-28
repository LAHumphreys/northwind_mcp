#!/usr/bin/env python3
"""Drive a Claude agent against the Northwind MCP server and dump everything.

Starts the server with NORTHWIND_TRACE_FILE set, runs each prompt as one turn of
a single headless `claude -p` conversation (Claude Code is the MCP client), and
renders a Markdown report that interleaves, per turn:

  * the agent transcript: assistant text, tool calls, tool results
  * every MCP request the server received and the response it returned
  * every SQL statement, its bound args, the command tag and the rows returned

Raw JSONL (agent stream per turn, server trace) is kept next to the report.

Usage:
    python3 scripts/agent_trace.py [--model claude-sonnet-5] [--out traces/...]
                                   [--prompts-file prompts.json] [--port 18080]
    python3 scripts/agent_trace.py --render traces/<run>    # re-render report.md from raw files

Requires: the `claude` CLI on PATH with working credentials, a reachable
Northwind database (NORTHWIND_DB_* env), and bin/mcp-northwind-server (make build).
"""

from __future__ import annotations

import argparse
import datetime as dt
import json
import os
import pathlib
import shutil
import subprocess
import sys
import textwrap
import time
import urllib.error
import urllib.request
import uuid

REPO = pathlib.Path(__file__).resolve().parent.parent

DEFAULT_PROMPTS = [
    textwrap.dedent(
        """\
        Return a product summary using the #northwind://get_products resource. List
        - The total number of active items (not discontinued)
        - The 5 items with the most stock
        - The 5 lowest-stocked items
        - The 5 items with the highest unit price"""
    ),
    "Limit this to products costing between $50 and $100",
    "Are any of these items amongst our top performers?",
]

MCP_SERVER_NAME = "northwind"
# Built-in tools the agent may use besides the MCP server's own tools.
BUILTIN_TOOLS = "ListMcpResourcesTool,ReadMcpResourceTool"


# --------------------------------------------------------------------------- server


def start_server(binary: pathlib.Path, port: int, trace_path: pathlib.Path, log_path: pathlib.Path):
    env = dict(os.environ, MCP_HTTP_ADDR=f":{port}", NORTHWIND_TRACE_FILE=str(trace_path))
    log = open(log_path, "wb")
    proc = subprocess.Popen([str(binary)], env=env, stdout=log, stderr=subprocess.STDOUT)
    return proc, log


def wait_for_server(url: str, timeout: float = 30.0) -> None:
    body = json.dumps(
        {
            "jsonrpc": "2.0",
            "id": 0,
            "method": "initialize",
            "params": {
                "protocolVersion": "2025-06-18",
                "capabilities": {},
                "clientInfo": {"name": "agent_trace-probe", "version": "0"},
            },
        }
    ).encode()
    deadline = time.time() + timeout
    last_err: Exception | None = None
    while time.time() < deadline:
        req = urllib.request.Request(
            url,
            data=body,
            headers={"Content-Type": "application/json", "Accept": "application/json, text/event-stream"},
        )
        try:
            with urllib.request.urlopen(req, timeout=2) as resp:
                if resp.status == 200:
                    return
        except (urllib.error.URLError, ConnectionError, TimeoutError) as e:
            last_err = e
        time.sleep(0.25)
    raise RuntimeError(f"MCP server at {url} did not become ready: {last_err}")


def count_lines(path: pathlib.Path) -> int:
    if not path.exists():
        return 0
    with open(path, "rb") as f:
        return sum(1 for _ in f)


# --------------------------------------------------------------------------- agent


def run_turn(
    prompt: str,
    *,
    session_id: str,
    first: bool,
    model: str,
    mcp_config: pathlib.Path,
    cwd: pathlib.Path,
    stream_path: pathlib.Path,
    stderr_path: pathlib.Path,
    max_turns: int,
) -> list[dict]:
    cmd = [
        "claude",
        "-p",
        prompt,
        "--model",
        model,
        "--output-format",
        "stream-json",
        "--verbose",
        "--mcp-config",
        str(mcp_config),
        "--strict-mcp-config",
        "--tools",
        BUILTIN_TOOLS,
        "--allowedTools",
        f"mcp__{MCP_SERVER_NAME}__*,{BUILTIN_TOOLS}",
        "--max-turns",
        str(max_turns),
    ]
    cmd += ["--session-id", session_id] if first else ["--resume", session_id]

    with open(stream_path, "wb") as out, open(stderr_path, "wb") as err:
        rc = subprocess.run(cmd, cwd=cwd, stdin=subprocess.DEVNULL, stdout=out, stderr=err).returncode
    events = []
    with open(stream_path, encoding="utf-8") as f:
        for line in f:
            line = line.strip()
            if line:
                try:
                    events.append(json.loads(line))
                except json.JSONDecodeError:
                    events.append({"type": "unparsed", "raw": line})
    if rc != 0:
        sys.stderr.write(f"warning: claude exited {rc}; see {stderr_path}\n")
    return events


# --------------------------------------------------------------------------- rendering


def fence(text: str, lang: str = "") -> str:
    return f"```{lang}\n{text}\n```"


def jfence(obj, *, max_inline_lines: int = 40, summary: str = "") -> str:
    text = json.dumps(obj, indent=2, ensure_ascii=False, default=str)
    block = fence(text, "json")
    n = text.count("\n") + 1
    if n > max_inline_lines:
        label = summary or f"{n} lines"
        return f"<details><summary>{label}</summary>\n\n{block}\n\n</details>"
    return block


def dedent_sql(sql: str) -> str:
    """Dedent SQL whose first line the tracer already trimmed."""
    first, _, rest = sql.partition("\n")
    return (first.strip() + "\n" + textwrap.dedent(rest)).strip()


def render_agent_events(events: list[dict]) -> tuple[str, dict]:
    out: list[str] = []
    stats = {"tool_calls": 0, "cost_usd": None, "duration_ms": None, "num_turns": None, "usage": None}
    for ev in events:
        t = ev.get("type")
        if t == "system" and ev.get("subtype") == "init":
            servers = ev.get("mcp_servers", [])
            tools = [n for n in ev.get("tools", []) if isinstance(n, str)]
            out.append(
                f"*Session init: model `{ev.get('model')}`, MCP servers {json.dumps(servers)}, "
                f"tools available: {', '.join(f'`{n}`' for n in tools) or 'none'}*"
            )
        elif t == "assistant":
            for block in ev.get("message", {}).get("content", []):
                bt = block.get("type")
                if bt == "text" and block.get("text", "").strip():
                    out.append("**Assistant:**\n\n" + textwrap.indent(block["text"].strip(), "> ", lambda _: True))
                elif bt == "tool_use":
                    stats["tool_calls"] += 1
                    out.append(f"**Tool call** `{block.get('name')}` (id `{block.get('id')}`)\n\n" + jfence(block.get("input", {})))
                elif bt == "thinking" and block.get("thinking", "").strip():
                    out.append("<details><summary>thinking</summary>\n\n" + fence(block["thinking"].strip()) + "\n\n</details>")
        elif t == "user":
            for block in ev.get("message", {}).get("content", []):
                if isinstance(block, dict) and block.get("type") == "tool_result":
                    content = block.get("content")
                    if isinstance(content, list):
                        parts = []
                        for c in content:
                            if isinstance(c, dict) and c.get("type") == "text":
                                parts.append(c.get("text", ""))
                            else:
                                parts.append(json.dumps(c, ensure_ascii=False))
                        content = "\n".join(parts)
                    content = str(content)
                    pretty = content
                    try:
                        pretty = json.dumps(json.loads(content), indent=2, ensure_ascii=False)
                    except (json.JSONDecodeError, TypeError):
                        pass
                    n = pretty.count("\n") + 1
                    flag = " (is_error)" if block.get("is_error") else ""
                    body = fence(pretty, "json" if pretty is not content else "")
                    if n > 30:
                        body = f"<details><summary>{n} lines</summary>\n\n{body}\n\n</details>"
                    out.append(f"**Tool result** for `{block.get('tool_use_id')}`{flag}\n\n{body}")
        elif t == "result":
            stats["cost_usd"] = ev.get("total_cost_usd")
            stats["duration_ms"] = ev.get("duration_ms")
            stats["num_turns"] = ev.get("num_turns")
            stats["usage"] = ev.get("usage")
            if ev.get("subtype") != "success":
                out.append(f"**Result: {ev.get('subtype')}** {ev.get('result') or ev.get('error') or ''}")
    return "\n\n".join(out), stats


def render_server_events(events: list[dict]) -> tuple[str, dict]:
    """Group a slice of the server trace by MCP request id and render it."""
    by_req: dict[int, list[dict]] = {}
    order: list[int] = []
    for ev in events:
        rid = ev.get("request_id", 0)
        if rid not in by_req:
            by_req[rid] = []
            order.append(rid)
        by_req[rid].append(ev)

    stats = {"mcp_requests": 0, "sql_statements": 0, "methods": []}
    out: list[str] = []
    for rid in order:
        evs = by_req[rid]
        req = next((e for e in evs if e["kind"] == "mcp.request"), None)
        resp = next((e for e in evs if e["kind"] == "mcp.response"), None)
        method = (req or resp or {}).get("method", "?")
        stats["mcp_requests"] += 1
        stats["methods"].append(method)
        out.append(f"#### MCP request #{rid}: `{method}`")
        if req is not None:
            out.append("**Message sent to the MCP server** (params):\n\n" + jfence(req.get("params")))
        for e in evs:
            k = e["kind"]
            if k == "sql.query":
                stats["sql_statements"] += 1
                out.append("**SQL query**\n\n" + fence(dedent_sql(e.get("sql", "")), "sql"))
                if e.get("args"):
                    out.append("Args:\n\n" + fence(json.dumps(e["args"], ensure_ascii=False), "json"))
            elif k == "sql.rows":
                rows = e.get("rows")
                n = len(rows) if isinstance(rows, list) else 1
                out.append(f"**SQL data returned** (`{e.get('label')}`, {n} row{'s' if n != 1 else ''}):\n\n" + jfence(rows, summary=f"{n} rows as JSON"))
            elif k == "sql.result":
                extra = f", error: `{e['error']}`" if e.get("error") else ""
                out.append(f"*Postgres: `{e.get('command_tag')}` in {e.get('duration_ms', 0):.1f} ms{extra}*")
        if resp is not None:
            if resp.get("error"):
                out.append(f"**Message returned by the MCP server**: error `{resp['error']}` ({resp.get('duration_ms', 0):.1f} ms)")
            else:
                out.append(
                    f"**Message returned by the MCP server** (result, {resp.get('duration_ms', 0):.1f} ms):\n\n"
                    + jfence(resp.get("result"))
                )
    return "\n\n".join(out), stats


def render_report(*, model: str, session_id: str, started: dt.datetime, turns: list[dict], out_dir: pathlib.Path) -> str:
    lines = [
        "# Agent trace: Northwind MCP",
        "",
        f"- Model: `{model}`",
        f"- Agent: Claude Code CLI headless (`claude -p`), one conversation resumed across turns, session `{session_id}`",
        f"- MCP client: Claude Code, Streamable HTTP to the Go server with `NORTHWIND_TRACE_FILE` set",
        f"- Started: {started.isoformat(timespec='seconds')}",
        f"- Raw files: `server_trace.jsonl`, `turn-N.stream.jsonl`, `turn-N.stderr.log`, `server.log`",
        "",
        "## Summary",
        "",
        "| Turn | Agent tool calls | MCP requests (server side) | SQL statements | Agent turns | Cost (USD) | Wall time |",
        "| --- | --- | --- | --- | --- | --- | --- |",
    ]
    for t in turns:
        a, s = t["agent_stats"], t["server_stats"]
        cost = f"{a['cost_usd']:.4f}" if a["cost_usd"] is not None else "?"
        dur = f"{a['duration_ms'] / 1000:.1f}s" if a["duration_ms"] is not None else "?"
        lines.append(f"| {t['n']} | {a['tool_calls']} | {s['mcp_requests']} | {s['sql_statements']} | {a['num_turns']} | {cost} | {dur} |")
    lines.append("")
    for t in turns:
        methods = ", ".join(f"`{m}`" for m in t["server_stats"]["methods"]) or "none"
        lines.append(f"Turn {t['n']} MCP methods, in order: {methods}")
        lines.append("")

    for t in turns:
        lines += [
            f"## Turn {t['n']}",
            "",
            "### Prompt",
            "",
            fence(t["prompt"]),
            "",
            "### Agent transcript",
            "",
            t["agent_md"] or "*(no events)*",
            "",
            "### Final answer",
            "",
            textwrap.indent(t["final"].strip() or "(none)", "> ", lambda _: True),
            "",
            "### MCP and SQL activity (server side)",
            "",
            t["server_md"] or "*(no MCP requests reached the server during this turn)*",
            "",
        ]
    return "\n".join(lines)


# --------------------------------------------------------------------------- main


def load_stream(path: pathlib.Path) -> list[dict]:
    events = []
    with open(path, encoding="utf-8") as f:
        for line in f:
            line = line.strip()
            if line:
                try:
                    events.append(json.loads(line))
                except json.JSONDecodeError:
                    events.append({"type": "unparsed", "raw": line})
    return events


def render_run(out_dir: pathlib.Path) -> pathlib.Path:
    """Build report.md from run.json plus the raw per-turn and server trace files."""
    run = json.loads((out_dir / "run.json").read_text(encoding="utf-8"))
    trace_lines = [ln for ln in (out_dir / "server_trace.jsonl").read_text(encoding="utf-8").splitlines()]
    turns = []
    for meta in run["turns"]:
        i = meta["n"]
        events = load_stream(out_dir / f"turn-{i}.stream.jsonl")
        server_events = [json.loads(ln) for ln in trace_lines[meta["trace_start_line"] : meta["trace_end_line"]] if ln.strip()]
        agent_md, agent_stats = render_agent_events(events)
        server_md, server_stats = render_server_events(server_events)
        final = next((e.get("result", "") for e in events if e.get("type") == "result"), "")
        turns.append(
            {
                "n": i,
                "prompt": meta["prompt"],
                "agent_md": agent_md,
                "agent_stats": agent_stats,
                "server_md": server_md,
                "server_stats": server_stats,
                "final": final or "",
            }
        )
    report = render_report(
        model=run["model"],
        session_id=run["session_id"],
        started=dt.datetime.fromisoformat(run["started"]),
        turns=turns,
        out_dir=out_dir,
    )
    path = out_dir / "report.md"
    path.write_text(report, encoding="utf-8")
    return path


def main() -> int:
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("--model", default="claude-sonnet-5")
    ap.add_argument("--out", type=pathlib.Path, help="output directory (default traces/<utc-timestamp>-<model>)")
    ap.add_argument("--prompts-file", type=pathlib.Path, help="JSON array of prompts, one per turn")
    ap.add_argument("--port", type=int, default=18080)
    ap.add_argument("--server-bin", type=pathlib.Path, default=REPO / "bin" / "mcp-northwind-server")
    ap.add_argument("--max-turns", type=int, default=20, help="agent loop cap per prompt")
    ap.add_argument("--render", type=pathlib.Path, metavar="RUN_DIR", help="only re-render report.md for an existing run")
    args = ap.parse_args()

    if args.render:
        print(f"[agent_trace] report: {render_run(args.render)}")
        return 0

    if shutil.which("claude") is None:
        sys.exit("claude CLI not found on PATH")
    if not args.server_bin.exists():
        sys.exit(f"server binary not found at {args.server_bin} (run `make build`)")

    prompts = DEFAULT_PROMPTS
    if args.prompts_file:
        prompts = json.loads(args.prompts_file.read_text(encoding="utf-8"))

    started = dt.datetime.now(dt.timezone.utc)
    out_dir = args.out or REPO / "traces" / f"{started.strftime('%Y%m%dT%H%M%SZ')}-{args.model}"
    out_dir.mkdir(parents=True, exist_ok=True)
    agent_cwd = out_dir / "agent-cwd"  # empty dir: no CLAUDE.md, hooks or project settings leak in
    agent_cwd.mkdir(exist_ok=True)

    trace_path = out_dir / "server_trace.jsonl"
    mcp_url = f"http://127.0.0.1:{args.port}/mcp"
    mcp_config = out_dir / "mcp.json"
    mcp_config.write_text(json.dumps({"mcpServers": {MCP_SERVER_NAME: {"type": "http", "url": mcp_url}}}, indent=2))

    session_id = str(uuid.uuid4())
    run = {"model": args.model, "session_id": session_id, "started": started.isoformat(timespec="seconds"), "turns": []}

    server, server_log = start_server(args.server_bin, args.port, trace_path, out_dir / "server.log")
    try:
        wait_for_server(mcp_url)
        for i, prompt in enumerate(prompts, start=1):
            print(f"[agent_trace] turn {i}/{len(prompts)}: {prompt.splitlines()[0][:70]}...", flush=True)
            # Skip the readiness probe's own initialize (turn 1) and anything logged before this turn.
            start_line = count_lines(trace_path)
            events = run_turn(
                prompt,
                session_id=session_id,
                first=(i == 1),
                model=args.model,
                mcp_config=mcp_config,
                cwd=agent_cwd,
                stream_path=out_dir / f"turn-{i}.stream.jsonl",
                stderr_path=out_dir / f"turn-{i}.stderr.log",
                max_turns=args.max_turns,
            )
            time.sleep(0.5)  # let the server flush trailing trace lines
            end_line = count_lines(trace_path)
            run["turns"].append({"n": i, "prompt": prompt, "trace_start_line": start_line, "trace_end_line": end_line})
            (out_dir / "run.json").write_text(json.dumps(run, indent=2), encoding="utf-8")
            result = next((e for e in events if e.get("type") == "result"), {})
            print(f"[agent_trace]   {result.get('num_turns')} agent turns, cost {result.get('total_cost_usd')}", flush=True)
    finally:
        server.terminate()
        try:
            server.wait(timeout=5)
        except subprocess.TimeoutExpired:
            server.kill()
        server_log.close()

    print(f"[agent_trace] report: {render_run(out_dir)}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
