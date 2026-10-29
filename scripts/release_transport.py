"""Bounded read retries and redacted diagnostics for release GitHub operations."""

import json
import re
import subprocess
import time

RETRY_STATUSES = {429, 500, 502, 503, 504}
NETWORK = re.compile(
    r"connection reset|connection refused|timed out|timeout|temporary failure|"
    r"network is unreachable|TLS handshake|unexpected EOF|remote end hung up|broken pipe",
    re.IGNORECASE,
)


def text(value):
    return value.decode("utf-8", "replace") if isinstance(value, bytes) else value or ""


def status(result):
    try:
        body = json.loads(text(result.stdout))
        if isinstance(body, dict) and str(body.get("status", "")).isdigit():
            return int(body["status"])
    except (ValueError, TypeError):
        pass
    match = re.search(r"\bHTTP(?:/\S+)?\s+(\d{3})\b", text(result.stderr))
    return int(match[1]) if match else None


def diagnostic(value):
    if isinstance(value, subprocess.TimeoutExpired):
        return f"request timed out after {value.timeout}s"
    if isinstance(value, OSError):
        return f"transport unavailable ({type(value).__name__}, errno={value.errno})"
    details = [text(value.stderr)]
    try:
        body = json.loads(text(value.stdout))
        if value.returncode and isinstance(body, dict):
            details.append(str(body.get("message", "")))
    except (ValueError, TypeError):
        pass
    cleaned = []
    for detail in details:
        detail = re.sub(r"(?im)^.*\bauthorization\s*:[^\r\n]*", "<redacted>", detail)
        detail = re.sub(r"(?i)\bbearer\s+\S+", "<redacted>", detail)
        detail = re.sub(r"https?://\S+", "<url>", detail)
        detail = re.sub(r"\b(?:gh[pousr]_[\w]+|github_pat_[\w]+)\b", "<redacted>", detail)
        detail = re.sub(
            r"(?i)\b(?:authorization|bearer|token|api[_-]?key|secret|password)\s*[:=]?\s*\S+",
            "<redacted>",
            detail,
        )
        cleaned.append(detail)
    detail = " ".join(" ".join(cleaned).split())[:800]
    return (
        f"exit={value.returncode}, HTTP={status(value) or 'unknown'}: {detail or 'no error detail'}"
    )


def run(command, *, read_only=False, **kwargs):
    """Retry only transient reads; callers reconcile writes without resubmitting."""
    attempts = 3 if read_only else 1
    # Download retries replace partial bytes rather than appending to them.
    output = kwargs.get("stdout")
    for attempt in range(attempts):
        if attempt and hasattr(output, "seek"):
            output.seek(0)
            output.truncate()
        try:
            result = subprocess.run(command, **kwargs)
        except subprocess.TimeoutExpired:
            if attempt + 1 == attempts:
                raise
        else:
            code = status(result) if result.returncode else None
            transient = code in RETRY_STATUSES or (
                code is None and NETWORK.search(text(result.stderr))
            )
            if not result.returncode or not transient or attempt + 1 == attempts:
                return result
        time.sleep(attempt + 1)
    raise AssertionError("unreachable")
