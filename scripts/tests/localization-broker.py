#!/usr/bin/env python3
"""Exercise actual broker locale/auth handlers without booting its desktop service."""

import ast
import asyncio
from pathlib import Path
import secrets
from types import SimpleNamespace


class Forbidden(Exception):
    pass


class BadRequest(Exception):
    def __init__(self, *, text=""):
        super().__init__(text)


class Request:
    def __init__(self, method="GET", path="/api/locale", headers=None, body=None):
        self.method = method
        self.path = path
        self.headers = headers or {}
        self.body = body or {}
        self.query = {}
        self.host = "127.0.0.1:17681"

    async def json(self):
        return self.body


source = Path(__file__).resolve().parents[2] / "plugins/podman/image/broker.py"
module = ast.parse(source.read_text())
handlers = [
    node
    for node in module.body
    if isinstance(node, ast.AsyncFunctionDef) and node.name in ("auth", "locale")
]
web = SimpleNamespace(
    middleware=lambda function: function,
    HTTPForbidden=Forbidden,
    HTTPBadRequest=BadRequest,
    json_response=lambda data, **kwargs: (data, kwargs),
)
namespace = {
    "web": web,
    "secrets": secrets,
    "asyncio": asyncio,
    "TOKEN": "fixture-viewer-token",
    "locale_value": "en",
    "SUPPORTED_LOCALES": {"en": {}, "zh-CN": {}, "fr": {}},
}
exec(compile(ast.Module(body=handlers, type_ignores=[]), str(source), "exec"), namespace)


async def main():
    auth, locale = namespace["auth"], namespace["locale"]
    result, metadata = await auth(Request(headers={"Origin": "http://127.0.0.1:8081"}), locale)
    assert result == {"locale": "en"}
    assert metadata["headers"]["Access-Control-Allow-Origin"] == "http://127.0.0.1:8081"
    for request in [
        Request("POST", body={"locale": "zh-CN"}),
        Request(
            "POST",
            headers={"Origin": "https://other.example", "X-Pot-Token": namespace["TOKEN"]},
            body={"locale": "zh-CN"},
        ),
        Request(path="/api/status"),
    ]:
        try:
            await auth(request, locale)
        except Forbidden:
            pass
        else:
            raise AssertionError("Unauthorized request allowed")
    headers = {"Origin": "http://127.0.0.1:17681", "X-Pot-Token": namespace["TOKEN"]}
    result, _ = await auth(Request("POST", headers=headers, body={"locale": "zh-CN"}), locale)
    assert result == {"locale": "zh-CN"}
    try:
        await auth(Request("POST", headers=headers, body={"locale": "invalid"}), locale)
    except BadRequest:
        pass
    else:
        raise AssertionError("Unsupported locale allowed")
    assert namespace["locale_value"] == "zh-CN"
    result, _ = await auth(Request("POST", headers=headers, body={"locale": "fr"}), locale)
    assert result == {"locale": "fr"}
    print(
        "Broker localization: public locale-only read, authenticated same-origin writes and validation passed"
    )


asyncio.run(main())
