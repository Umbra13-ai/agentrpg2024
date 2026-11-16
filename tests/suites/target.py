"""Where a test runs: a local server, the staging server or the live server.

The one owner of that question for every suite here: the server address (BASE) and its kind (TARGET) come from here, and a
suite states where it may run with only(...). Pick the server with ARPG_TARGET=local|staging|live, or give its address with
BASE (BASE wins). Default: a local server on port 8099.

  local    http://localhost:8099 (any localhost port is local)   every suite in this folder
  staging  https://test.rpg.umbra13.dev                          every suite in this folder (they only send requests)
  live     https://rpg.umbra13.dev                               only the suites that say they are safe there, after a deploy

These suites only send requests; none reads or writes the database directly. The logs go to ARPG_LOG_DIR (default: a folder
in the system's temporary directory). HTTPS uses the system's certificates (SSL_CERT_FILE), or ARPG_CA_BUNDLE when set.
"""
import os
import ssl
import sys
import tempfile
from urllib.parse import urlparse

BASES = {
    "local": "http://localhost:8099",
    "staging": "https://test.rpg.umbra13.dev",
    "live": "https://rpg.umbra13.dev",
}
HOSTS = {
    "localhost": "local",
    "127.0.0.1": "local",
    "test.rpg.umbra13.dev": "staging",
    "rpg.umbra13.dev": "live",
}

_wanted = os.environ.get("ARPG_TARGET", "local")
if not os.environ.get("BASE") and _wanted not in BASES:
    sys.exit(f"ARPG_TARGET must be one of {', '.join(BASES)} (got {_wanted!r})")
BASE = (os.environ.get("BASE") or BASES[_wanted]).rstrip("/")
TARGET = HOSTS.get(urlparse(BASE).hostname or "", "unknown")
LOCAL, STAGING, LIVE = TARGET == "local", TARGET == "staging", TARGET == "live"

CTX = ssl.create_default_context(cafile=os.environ.get("ARPG_CA_BUNDLE") or None) if BASE.startswith("https") else None

LOG_DIR = os.path.join(os.environ.get("ARPG_LOG_DIR") or os.path.join(tempfile.gettempdir(), "arpg-suite-logs"), "")
os.makedirs(LOG_DIR, exist_ok=True)


def only(*allowed, why):
    """Stop the script unless it runs on one of the allowed targets (local, staging, live)."""
    if TARGET not in allowed:
        script = os.path.basename(sys.argv[0]) or "this test"
        sys.exit(f"{script} runs on {' or '.join(allowed)} only ({why}); BASE {BASE} is {TARGET}.")
