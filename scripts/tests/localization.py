#!/usr/bin/env python3
"""Check catalogue completeness, native switching, and embedded web assets. No models/VMs."""

import json
from pathlib import Path
import re
import subprocess
import tempfile

ROOT = Path(__file__).resolve().parents[2]
en = json.loads((ROOT / "app/locales/en.json").read_text())
zh = json.loads((ROOT / "app/locales/zh-CN.json").read_text())


def placeholders(text):
    return re.findall(r"(?<!\{)\{([^{}]*)\}(?!\})", text)


assert en.keys() == zh.keys(), "Language keys differ"
for key in en:
    assert en[key] and zh[key], f"Empty translation: {key}"
    assert placeholders(en[key]) == placeholders(zh[key]), f"Parameter mismatch: {key}"
    assert not re.search(r"[\u4e00-\u9fff]", en[key]), f"Chinese in English translation: {key}"
for path in [*(ROOT / "app/src").rglob("*.rs"), *(ROOT / "ui/src").rglob("*.rs")]:
    source = path.read_text()
    keys = re.findall(r'(?:i18n::text|i18n::tr!)\(\s*"([^"]+)"', source)
    keys += re.findall(r'(?:catalog\.(?:text|format)\(|tr!\(\s*catalog,)\s*"([^"]+)"', source)
    keys += re.findall(r'rejection_detail\([^,]+,\s*"([^"]+)"', source)
    for key in keys:
        assert key in en, f"Missing Rust translation: {key}"
for path in (ROOT / "app/swift").glob("*.swift"):
    for key in re.findall(r'L\.(?:text|format)\(\s*"([^"]+)"', path.read_text()):
        if key.endswith("."):
            continue
        assert key in en, f"Missing native translation: {key}"
subprocess.run(["python3", ROOT / "scripts/localize-web", "--check"], check=True)
for name in ["sandbox-images/linux/default/index.html", "desktop/index.html"]:
    text = (ROOT / name).read_text()
    keys = re.findall(r'(?:data-pot-(?:i18n|title|aria-label)="|potT\(")([^"]+)', text)
    assert all(key in en for key in keys), f"Missing web translation: {name}"
with tempfile.TemporaryDirectory(prefix="pot-localization-") as directory:
    temp = Path(directory)
    resources = "enum TranslationResources {\nstatic let values: [String: [String: String]] = [\n"
    for locale, values in [("en", en), ("zh-CN", zh)]:
        resources += json.dumps(locale) + ": [\n"
        for key, value in values.items():
            resources += (
                json.dumps(key, ensure_ascii=False)
                + ": "
                + json.dumps(value, ensure_ascii=False)
                + ",\n"
            )
        resources += "],\n"
    resources += "]\n}\n"
    (temp / "Translations.swift").write_text(resources)
    subprocess.run(
        [
            "xcrun",
            "swiftc",
            "-module-cache-path",
            temp / "modules",
            temp / "Translations.swift",
            ROOT / "app/swift/Localization.swift",
            ROOT / "app/tests/language-preferences.swift",
            "-o",
            temp / "test",
        ],
        check=True,
    )
    subprocess.run([temp / "test"], check=True)
subprocess.run(
    [
        "node",
        ROOT / "scripts/tests/localization-web.js",
        ROOT / "sandbox-images/linux/default/index.html",
    ],
    check=True,
)
subprocess.run(["python3", ROOT / "scripts/tests/localization-broker.py"], check=True)
print(f"{len(en)} translation keys checked; native and embedded web localization passed")
