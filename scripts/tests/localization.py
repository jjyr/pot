#!/usr/bin/env python3
"""Check catalogue completeness, retained localized viewer and Sandbox pages. No models/VMs."""

import json
from pathlib import Path
import re
import shutil
import subprocess
import tempfile

ROOT = Path(__file__).resolve().parents[2]
en = json.loads((ROOT / "resources/app/locales/en.json").read_text())
catalogs = {
    path.stem: json.loads(path.read_text())
    for path in (ROOT / "resources/app/locales").glob("*.json")
}


def placeholders(text):
    return re.findall(r"(?<!\{)\{([^{}]*)\}(?!\})", text)


for locale, values in catalogs.items():
    assert en.keys() == values.keys(), f"Language keys differ: {locale}"
    for key in en:
        assert values[key], f"Empty translation: {locale}: {key}"
        assert placeholders(en[key]) == placeholders(values[key]), (
            f"Parameter mismatch: {locale}: {key}"
        )
for key in en:
    assert not re.search(r"[\u4e00-\u9fff]", en[key]), f"Chinese in English translation: {key}"
subprocess.run(["python3", ROOT / "scripts/localize-web", "--check"], check=True)
for name in [
    "plugins/podman/image/index.template.html",
    "desktop/index.template.html",
]:
    text = (ROOT / name).read_text()
    keys = re.findall(r'(?:data-pot-(?:i18n|title|aria-label)="|potT\(")([^"]+)', text)
    assert all(key in en for key in keys), f"Missing web translation: {name}"
with tempfile.TemporaryDirectory(prefix="pot-web-build-") as directory:
    temp = Path(directory)
    fixture = temp / "source"
    for name in ["resources/app/locales", "desktop", "plugins/podman/image"]:
        shutil.copytree(ROOT / name, fixture / name)
    third = dict(en, **{"language.name": "Français", "web.take-over": "Prendre le contrôle"})
    (fixture / "resources/app/locales/fr.json").write_text(json.dumps(third))
    output = temp / "assets"
    command = ["python3", ROOT / "scripts/localize-web", "--root", fixture, "--output", output]
    subprocess.run(command, check=True)
    for page in ["desktop", "sandbox"]:
        generated = json.loads((output / page / "locales.json").read_text())
        assert "fr" in generated
        assert "web.configure-token" not in generated["en"], (
            "Unrelated App keys leaked into desktop assets"
        )
        assert "potTranslations" not in (output / page / "index.html").read_text()
        subprocess.run(
            ["node", ROOT / "scripts/tests/localization-web.js", output / page], check=True
        )
    third["web.configured-memory"] = "invalid {wrong}"
    (fixture / "resources/app/locales/fr.json").write_text(json.dumps(third))
    result = subprocess.run(command, capture_output=True, text=True)
    assert result.returncode != 0 and "Parameter mismatch" in result.stderr
subprocess.run(["python3", ROOT / "scripts/tests/localization-broker.py"], check=True)
print(f"{len(en)} translation keys checked; retained web localization passed")
