#!/usr/bin/env python3
"""Check the shared product translation catalog used by App and browser."""

import json
from pathlib import Path
import re
import subprocess

ROOT = Path(__file__).resolve().parents[2]
source = (
    "import {messages} from './web/src/locales/messages.mjs'; console.log(JSON.stringify(messages))"
)
catalogs = json.loads(
    subprocess.check_output(["node", "--input-type=module", "-e", source], cwd=ROOT, text=True)
)
en = catalogs["en"]
for locale, values in catalogs.items():
    assert en.keys() == values.keys(), f"Language keys differ: {locale}"
    for key in en:
        assert values[key].strip(), f"Empty translation: {locale}: {key}"
        assert re.findall(r"\{([^{}]*)\}", en[key]) == re.findall(r"\{([^{}]*)\}", values[key]), (
            f"Parameter mismatch: {locale}: {key}"
        )
print(f"{len(en)} shared product translation keys checked")
