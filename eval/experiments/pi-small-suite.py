#!/usr/bin/env python3
"""Direct Pi CLI baseline for a completed Pot small suite; not a Driver integration."""

import argparse
import copy
import hashlib
import json
import os
from pathlib import Path
import queue
import shutil
import signal
import subprocess
import threading
import time


def write_json(path, value):
    path.write_text(json.dumps(value, indent=2) + "\n")


def run_pi(executable, directory, workspace, config, scenario, key):
    directory.mkdir()
    config_dir = directory / "pi-config"
    config_dir.mkdir()
    write_json(config_dir / "models.json", config)
    write_json(
        config_dir / "settings.json",
        {"retry": {"enabled": False}, "quietStartup": True},
    )
    command = [
        str(executable),
        "--mode",
        "rpc",
        "--provider",
        "deepseek",
        "--model",
        "deepseek-v4-flash",
        "--thinking",
        "off",
        "--no-session",
        "--no-extensions",
        "--no-skills",
        "--no-context-files",
        "--no-prompt-templates",
        "--offline",
        "--system-prompt",
        scenario["profile"]["params"]["agent"]["system_prompt"],
    ]
    if not scenario["profile"]["params"]["agent"]["builtin_tools"]:
        command.append("--no-tools")
    write_json(
        directory / "manifest.json",
        {
            "command": command,
            "cwd": str(workspace),
            "scenario": scenario,
            "comparison": "native-policy CLI; Pi adds cwd and has native tool schemas",
            "model_output_cap": 64,
            "context": "fresh process; sequential turns retain the same session",
        },
    )
    env = os.environ.copy()
    env.update(PI_CODING_AGENT_DIR=str(config_dir), POT_EVAL_DEEPSEEK_KEY=key)
    events = queue.Queue()
    started = time.monotonic()
    turns = []
    with (
        (directory / "stderr.log").open("w") as stderr,
        (directory / "events.jsonl").open("w") as output,
    ):
        process = subprocess.Popen(
            command,
            cwd=workspace,
            env=env,
            stdin=subprocess.PIPE,
            stdout=subprocess.PIPE,
            stderr=stderr,
            text=True,
            start_new_session=True,
        )

        def reader():
            for line in process.stdout:
                events.put(line)
            events.put(None)

        thread = threading.Thread(target=reader, daemon=True)
        thread.start()
        try:
            for index, prompt in enumerate(scenario["turns"]):
                process.stdin.write(json.dumps({"type": "prompt", "message": prompt}) + "\n")
                process.stdin.flush()
                messages = []
                deadline = time.monotonic() + 45
                while True:
                    line = events.get(timeout=max(0.01, deadline - time.monotonic()))
                    if line is None:
                        raise RuntimeError("Pi exited before finishing the turn")
                    output.write(line)
                    output.flush()
                    event = json.loads(line)
                    if event.get("type") == "response" and event.get("success") is False:
                        raise RuntimeError("Pi RPC rejected the request; inspect records")
                    if event.get("type") == "tool_execution_start":
                        raise RuntimeError("Unexpected tool execution in short-output case")
                    if (
                        event.get("type") == "message_end"
                        and event.get("message", {}).get("role") == "assistant"
                    ):
                        messages.append(event["message"])
                    if event.get("type") == "agent_end":
                        break
                    if time.monotonic() >= deadline:
                        raise RuntimeError("Pi turn deadline exceeded")
                if len(messages) != 1:
                    raise RuntimeError("Expected exactly one request per turn")
                message = messages[0]
                usage = message["usage"]
                inputs = (
                    usage.get("input", 0) + usage.get("cacheRead", 0) + usage.get("cacheWrite", 0)
                )
                if inputs + usage["output"] != usage["totalTokens"]:
                    raise RuntimeError("Pi usage does not reconcile")
                answer = "".join(
                    part.get("text", "") for part in message["content"] if part["type"] == "text"
                )
                assertion = scenario["turn_assertions"][index]
                if assertion["kind"] == "json":
                    try:
                        accepted = json.loads(answer) == assertion["expected"]
                    except json.JSONDecodeError:
                        accepted = False
                else:
                    accepted = answer == assertion["expected"]
                turns.append(
                    {
                        "turn": index + 1,
                        "input_tokens": inputs,
                        "output_tokens": usage["output"],
                        "cache_read_tokens": usage.get("cacheRead"),
                        "answer": answer,
                        "success": accepted
                        and message.get("stopReason") not in ("error", "aborted"),
                    }
                )
        finally:
            os.killpg(process.pid, signal.SIGTERM) if process.poll() is None else None
            try:
                process.wait(timeout=5)
            except subprocess.TimeoutExpired:
                os.killpg(process.pid, signal.SIGKILL)
                process.wait()
            process.stdin.close()
            thread.join(timeout=5)
            process.stdout.close()
    report = {
        "success": all(turn["success"] for turn in turns),
        "turns": turns,
        "input_tokens": sum(turn["input_tokens"] for turn in turns),
        "output_tokens": sum(turn["output_tokens"] for turn in turns),
        "requests": len(turns),
        "elapsed_seconds": time.monotonic() - started,
    }
    write_json(directory / "report.json", report)
    return report


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("pot_run", type=Path)
    parser.add_argument("new_baseline_dir", type=Path)
    args = parser.parse_args()
    source = args.pot_run.resolve()
    directory = args.new_baseline_dir.resolve()
    executable = Path(shutil.which("pi") or "/Users/jjy/.cache/npm/global/bin/pi")
    provider = json.loads((Path.home() / ".pi/agent/models.json").read_text())["providers"][
        "deepseek"
    ]
    key = os.environ.get(provider["apiKey"], provider["apiKey"])
    model = copy.deepcopy(next(m for m in provider["models"] if m["id"] == "deepseek-v4-flash"))
    model["maxTokens"] = 64
    config = {
        "providers": {
            "deepseek": {
                "api": provider["api"],
                "baseUrl": provider["baseUrl"],
                "apiKey": "$POT_EVAL_DEEPSEEK_KEY",
                "models": [model],
            }
        }
    }
    directory.mkdir()
    workspace = directory / "workspace"
    workspace.mkdir()
    results = {}
    for name in ["memory", *[f"cache-{index}" for index in range(5)]]:
        scenario = json.loads((source / name / "scenario.json").read_text())
        results[name] = run_pi(executable, directory / name, workspace, config, scenario, key)
        write_json(directory / "results.json", results)
        print(name, json.dumps(results[name]), flush=True)
        if name.startswith("cache"):
            time.sleep(0.2)
    comparison = {
        "model": model["id"],
        "trials": 1,
        "pi_binary_sha256": hashlib.sha256(executable.resolve().read_bytes()).hexdigest(),
        "cache_condition": "uncontrolled; Pot suite ran before Pi baseline",
        "requests": sum(result["requests"] for result in results.values()),
        "results": results,
        "pot_summary": json.loads((source / "summary.json").read_text()),
        "measurement": "Pi message_end usage; cached inputs included once",
        "alignment": "identical user turns and requested settings; native tools/system serialization differ",
        "empirical_efficiency_claim": False,
    }
    write_json(directory / "summary.json", comparison)
    for path in directory.rglob("*"):
        if path.is_file() and key.encode() in path.read_bytes():
            raise RuntimeError("Credential found in recorded artifact")
    print("Credential scan passed", flush=True)


if __name__ == "__main__":
    main()
