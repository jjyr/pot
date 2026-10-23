"""Check both local services without exposing or using browser credentials."""

import http.client
import json
import os
import subprocess
from urllib.parse import urlsplit

subprocess.run(
    ["/opt/pot/bin/pot", "status", "-root", "/home/pot/.pot"],
    check=True,
    timeout=4,
    stdout=subprocess.DEVNULL,
    stderr=subprocess.DEVNULL,
)
connection = http.client.HTTPConnection("127.0.0.1", 17703, timeout=4)
connection.request(
    "GET", "/access/status", headers={"Host": urlsplit(os.environ["POT_WEB_ORIGIN"]).netloc}
)
response = connection.getresponse()
assert response.status == 200 and json.load(response)["scope"] == "control"
connection.close()
