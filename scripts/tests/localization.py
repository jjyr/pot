#!/usr/bin/env python3
"""Check catalogue completeness, native switching, and embedded web assets. No models/VMs."""

import json
from pathlib import Path
import re
import shutil
import subprocess
import tempfile

ROOT = Path(__file__).resolve().parents[2]
en = json.loads((ROOT / "app/locales/en.json").read_text())
catalogs = {
    path.stem: json.loads(path.read_text()) for path in (ROOT / "app/locales").glob("*.json")
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
for name in ["sandbox-images/linux/default/index.template.html", "desktop/index.template.html"]:
    text = (ROOT / name).read_text()
    keys = re.findall(r'(?:data-pot-(?:i18n|title|aria-label)="|potT\(")([^"]+)', text)
    assert all(key in en for key in keys), f"Missing web translation: {name}"
with tempfile.TemporaryDirectory(prefix="pot-localization-") as directory:
    temp = Path(directory)
    resources = "enum TranslationResources {\nstatic let values: [String: [String: String]] = [\n"
    for locale, values in catalogs.items():
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
    third = dict(en, **{"language.name": "Français", "ui.retry": "Réessayer"})
    french = (
        '"fr": [\n'
        + "".join(
            json.dumps(key, ensure_ascii=False)
            + ": "
            + json.dumps(value, ensure_ascii=False)
            + ",\n"
            for key, value in third.items()
        )
        + "],\n"
    )
    (temp / "Translations.swift").write_text(resources.replace("= [\n", "= [\n" + french, 1))
    (temp / "ThirdLanguage.swift").write_text("""import Foundation
@main struct ThirdLanguage {
    static func main() {
        let previous = UserDefaults.standard.string(forKey: "language")
        defer { if let previous { UserDefaults.standard.set(previous, forKey: "language") } else { UserDefaults.standard.removeObject(forKey: "language") } }
        assert(L.resolve(["fr-CA", "en"]) == "fr")
        assert(L.resolve(saved: "fr", languages: ["zh-CN"]) == "fr")
        _ = L.select("en")
        assert(L.select("fr"))
        assert(L.text("ui.retry") == "Réessayer")
        print("Native third-language matching, selection and rendering passed")
    }
}
""")
    subprocess.run(
        [
            "xcrun",
            "swiftc",
            "-module-cache-path",
            temp / "modules",
            temp / "Translations.swift",
            ROOT / "app/swift/Localization.swift",
            temp / "ThirdLanguage.swift",
            "-o",
            temp / "third-test",
        ],
        check=True,
    )
    subprocess.run([temp / "third-test"], check=True)
with tempfile.TemporaryDirectory(prefix="pot-web-build-") as directory:
    temp = Path(directory)
    fixture = temp / "source"
    for name in ["app/locales", "desktop", "sandbox-images/linux/default"]:
        shutil.copytree(ROOT / name, fixture / name)
    third = dict(en, **{"language.name": "Français", "web.take-over": "Prendre le contrôle"})
    (fixture / "app/locales/fr.json").write_text(json.dumps(third))
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
    (fixture / "app/locales/fr.json").write_text(json.dumps(third))
    result = subprocess.run(command, capture_output=True, text=True)
    assert result.returncode != 0 and "Parameter mismatch" in result.stderr
subprocess.run(["python3", ROOT / "scripts/tests/localization-broker.py"], check=True)
print(f"{len(en)} translation keys checked; native and embedded web localization passed")
