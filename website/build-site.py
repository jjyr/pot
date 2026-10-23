"""Generate six static language entry points and an offline preview. Python stdlib only."""

from pathlib import Path
import base64
import html
import json
import mimetypes
import re

ROOT = Path(__file__).resolve().parent
LANGUAGES = {
    "en": "English",
    "zh-CN": "简体中文",
    "zh-TW": "繁體中文",
    "ja": "日本語",
    "ko": "한국어",
    "es": "Español",
}
TEMPLATE = (ROOT / "templates/page.html").read_text()
CATALOGS = {code: json.loads((ROOT / "i18n" / f"{code}.json").read_text()) for code in LANGUAGES}
META_DESCRIPTION_KEY = html.unescape(
    re.search(r'<meta name="description" content="([^"]+)"', TEMPLATE)[1]
)
INSTALL = json.loads((ROOT / "install.json").read_text())
REFERENCE = CATALOGS["en"]
# Every meaningful template string must have an explicit catalog entry or be a brand.
BRANDS = {"Pot", "Codex", "Claude", "Grok bot", "Devin", "Agent Link", "Pot.", "Pot → Codex"}
for match in re.finditer(r'>([^<>]+)<|(?:aria-label|alt|content)="([^"]*)"', TEMPLATE):
    key = html.unescape((match[1] or match[2]).strip())
    if re.search(r"[A-Za-z\u3400-\u9fff]", key) and not key.startswith(("width=", "#")):
        assert key in REFERENCE["page"] or key in BRANDS, f"Uncatalogued template string: {key}"


def shape(value):
    if isinstance(value, dict):
        return {k: shape(v) for k, v in value.items()}
    if isinstance(value, list):
        return [shape(v) for v in value]
    return type(value).__name__


for code, catalog in CATALOGS.items():
    assert shape(catalog) == shape(REFERENCE), f"Incomplete translation structure: {code}"


def esc(value):
    return html.escape(str(value), quote=True)


def payload(value):
    return json.dumps(value, ensure_ascii=False, separators=(",", ":")).replace("<", "\\u003c")


def initial_demo(catalog, prefix):
    p = catalog["page"]
    d = catalog["demo"]
    a = d["agentTalk"][0]
    icon = f'<span class="agent-icon codex" aria-hidden="true"><img src="{prefix}assets/codex.png" alt=""></span>'
    conversation = f'<div class="conversation-user">{esc(a[0])}</div><div class="conversation-reply">{icon}<div><b>Codex</b><p>{esc(a[1])}</p></div></div><div class="conversation-result"><span>↳</span><div>{esc(a[2])}<small>{esc(d["directExample"])}</small></div></div>'
    number = d["flowNumber"].replace("{n}", "01")
    flow = f'<div class="flow-heading"><span>{esc(d["flowEyebrows"][0])}</span><span>{esc(number)}</span></div><div class="flow-user">{esc(d["flowUsers"][0])}</div><div class="flow-message"><img src="{prefix}assets/pot.png" alt="" aria-hidden="true"><div><b>Pot</b><p>{esc(d["flowReplies"][0])}</p></div></div><div class="flow-route"><b>{esc(d["flowRoute"][0])}</b><span>→</span><b>{esc(d["flowRoute"][1])}</b></div>'
    return conversation, flow


def render(code, prefix="../", root=False):
    catalog = CATALOGS[code]
    texts = catalog["page"]
    demo = catalog["demo"]
    install = INSTALL

    def node(m):
        raw = m[1]
        key = html.unescape(raw.strip())
        return ">" + raw.replace(raw.strip(), esc(texts[key])) + "<" if key in texts else m[0]

    page = re.sub(r">([^<>]+)<", node, TEMPLATE)
    if code in ("en", "es", "ko"):
        page = page.replace('<br class="desktop-break">', ' <br class="desktop-break">')

    def attribute(m):
        key = html.unescape(m[2])
        return m[1] + '="' + esc(texts.get(key, key)) + '"'

    page = re.sub(r'(aria-label|alt|content)="([^"]*)"', attribute, page)
    page = page.replace(
        '<html lang="zh-CN">',
        f'<html lang="{code}"' + (" data-detect-language" if root else "") + ">",
    )
    page = page.replace('href="assets/', 'href="' + prefix + "assets/").replace(
        'src="assets/', 'src="' + prefix + "assets/"
    )
    page = page.replace('href="style.css"', 'href="' + prefix + 'style.css"')
    options = "".join(
        f'<option value="{k}" lang="{k}"'
        + (" selected" if k == code else "")
        + ">"
        + esc(v)
        + "</option>"
        for k, v in LANGUAGES.items()
    )
    picker = f'<label class="language-picker"><span>{esc(demo["language"])}</span><select id="language-select" aria-label="{esc(demo["switchLanguage"])}">{options}</select></label>'
    page = page.replace("</nav>", "</nav>" + picker, 1)
    alternatives = (
        "".join(f'<link rel="alternate" hreflang="{k}" href="{prefix}{k}/">' for k in LANGUAGES)
        + f'<link rel="alternate" hreflang="x-default" href="{prefix}index.html">'
    )
    for marker, key in {
        "agent-prompt": "prompt",
        "brew-command": "brew",
        "docker-image": "image",
        "server-command": "server",
    }.items():
        page = page.replace("<!--" + marker + "-->", esc(install[key]))
    config = {**catalog, "install": install, "locale": code, "assetBase": prefix}
    scripts = f'<script src="{prefix}locale.js"></script><script id="pot-data" type="application/json">{payload(config)}</script><script src="{prefix}app.js" defer></script>'
    page = page.replace('<script src="app.js" defer></script>', alternatives + scripts)
    conversation, flow = initial_demo(catalog, prefix)
    page = page.replace(
        '<div id="agent-conversation" class="agent-conversation" aria-live="polite"></div>',
        '<div id="agent-conversation" class="agent-conversation" aria-live="polite">'
        + conversation
        + "</div>",
    )
    page = page.replace(
        '<div class="flow-panel" id="flow-panel" aria-live="polite"></div>',
        '<div class="flow-panel" id="flow-panel" aria-live="polite">' + flow + "</div>",
    )
    fallback = (
        '<noscript><nav class="language-fallback" aria-label="'
        + esc(demo["switchLanguage"])
        + '">'
        + "".join(f'<a href="{prefix}{k}/" lang="{k}">{esc(v)}</a>' for k, v in LANGUAGES.items())
        + "</nav></noscript>"
    )
    page = page.replace('<main id="main">', fallback + '<main id="main">')
    return page, config


def embedded_assets():
    result = {}
    for key, file in {
        "pot": "pot.png",
        "codex": "codex.png",
        "claude": "claude.svg",
        "grok": "grok.svg",
        "devin": "devin.png",
        "github": "github-invertocat-black.svg",
    }.items():
        path = ROOT / "assets" / file
        mime = mimetypes.guess_type(file)[0]
        result[key] = "data:" + mime + ";base64," + base64.b64encode(path.read_bytes()).decode()
    return result


def preview():
    assets = embedded_assets()
    bundle = {}
    for code in LANGUAGES:
        page, config = render(code, prefix="")
        body = re.search(r"<body>(.*?)</body>", page, re.S)[1]
        body = re.sub(r"<noscript>.*?</noscript>", "", body, flags=re.S)
        for key, file in {
            "pot": "pot.png",
            "codex": "codex.png",
            "claude": "claude.svg",
            "grok": "grok.svg",
            "devin": "devin.png",
            "github": "github-invertocat-black.svg",
        }.items():
            body = body.replace("assets/" + file, assets[key])
        config = {**config, "assets": assets}
        title = html.unescape(re.search(r"<title>(.*?)</title>", page, re.S)[1])
        description = config["page"][META_DESCRIPTION_KEY]
        bundle[code] = {"body": body, "data": config, "title": title, "description": description}
    first = bundle["en"]
    css = (ROOT / "style.css").read_text()
    locale = (ROOT / "locale.js").read_text()
    app = (ROOT / "app.js").read_text()
    result = (
        '<!doctype html><html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><meta name="description" content="'
        + esc(first["description"])
        + '"><title>'
        + esc(first["title"])
        + "</title><style>"
        + css
        + "</style></head><body>"
        + first["body"]
        + '<script id="pot-data" type="application/json">'
        + payload(first["data"])
        + "</script><script>window.POT_PREVIEW="
        + payload(bundle)
        + ";</script><script>"
        + locale
        + "</script><script>"
        + app
        + "</script></body></html>"
    )
    assert not re.search(r'(?:src=|<link\b[^>]*\bhref=)["\'](?:https?://|\.\./|assets/)', result), (
        "Preview contains a remote or missing resource"
    )
    (ROOT / "pot-preview.html").write_text(result)
    print("Generated offline preview:", len(result.encode()), "bytes")


def main():
    for code in LANGUAGES:
        target = ROOT / code
        target.mkdir(exist_ok=True)
        (target / "index.html").write_text(render(code)[0])
    (ROOT / "index.html").write_text(render("en", prefix="", root=True)[0])
    preview()
    print("Generated static entries:", ", ".join(LANGUAGES))


if __name__ == "__main__":
    main()
