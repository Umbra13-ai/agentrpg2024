#!/usr/bin/env python3
"""Install and run Agent RPG 2024 on this computer.

    python install.py            set up everything (safe to run again)
    python install.py --start    start the server with the settings in .env
    python install.py --stop     stop a server started by this script
    python install.py --status   say whether the server is running

What setup does, in order:
  1. checks for Go 1.26+ and PostgreSQL, and says how to get them if they are missing;
  2. creates a database and a database user for the game (or reuses the ones in .env when they still work);
  3. writes .env (DATABASE_URL, PORT, ADMIN_KEY); passwords are never printed;
  4. builds the server (no C compiler needed);
  5. starts it once so it creates its tables and loads the SRD 5.2.1 data;
  6. can register a GM and a player and save their tokens in ~/.agentrpg/;
  7. starts the server on http://localhost:<port>, or leaves it stopped.

Windows first; Linux and macOS work too. Only the Python standard library is used (Python 3.8+).
Options: --yes (take every default, no questions), --reconfigure (make a new database setup even if .env works),
--port N, --db-name NAME, --db-user NAME. The PostgreSQL admin password can also come from the environment
variable AGENTRPG_DB_ADMIN_PASSWORD (useful with --yes).
"""

import argparse
import getpass
import glob
import json
import os
import platform
import re
import secrets
import shutil
import subprocess
import sys
import time
import urllib.error
import urllib.parse
import urllib.request

ROOT = os.path.dirname(os.path.abspath(__file__))
ENV_FILE = os.path.join(ROOT, ".env")
PID_FILE = os.path.join(ROOT, ".agentrpg.pid")
WINDOWS = platform.system() == "Windows"
MAC = platform.system() == "Darwin"
EXE = ".exe" if WINDOWS else ""
SERVER_BIN = os.path.join(ROOT, "agentrpg" + EXE)
CLI_BIN = os.path.join(ROOT, "arpg" + EXE)
TOKEN_DIR = os.path.join(os.path.expanduser("~"), ".agentrpg")
NAME_RE = re.compile(r"^[a-z_][a-z0-9_]{0,62}$")

ARGS = None


# ---------------------------------------------------------------- small helpers

def say(msg=""):
    print(msg, flush=True)


def step(msg):
    say("\n== " + msg)


def fail(msg):
    say("\nSTOPPED: " + msg)
    sys.exit(1)


def ask(question, default=""):
    if ARGS.yes:
        return default
    shown = " [%s]" % default if default else ""
    ans = input("%s%s: " % (question, shown)).strip()
    return ans or default


def ask_yes(question, default=True):
    if ARGS.yes:
        return default
    ans = input("%s [%s]: " % (question, "Y/n" if default else "y/N")).strip().lower()
    return default if not ans else ans.startswith("y")


def ask_secret(question, env_name=None):
    if env_name and os.environ.get(env_name):
        return os.environ[env_name]
    if ARGS.yes:
        return ""
    return getpass.getpass(question + " (not shown): ")


def run(cmd, env=None, input_text=None, cwd=ROOT):
    return subprocess.run(cmd, cwd=cwd, env=env, input=input_text, capture_output=True, text=True, encoding="utf-8", errors="replace")


# ---------------------------------------------------------------- .env

def read_env():
    vals = {}
    if os.path.exists(ENV_FILE):
        with open(ENV_FILE, encoding="utf-8") as fh:
            for line in fh:
                line = line.strip()
                if line and not line.startswith("#") and "=" in line:
                    k, v = line.split("=", 1)
                    vals[k.strip()] = v.strip().strip('"')
    return vals


def write_env(updates):
    """Update or add keys in .env, keeping every other line as it is."""
    lines = []
    if os.path.exists(ENV_FILE):
        with open(ENV_FILE, encoding="utf-8") as fh:
            lines = fh.read().splitlines()
    done = set()
    for i, line in enumerate(lines):
        key = line.split("=", 1)[0].strip()
        if key in updates and not line.lstrip().startswith("#"):
            lines[i] = "%s=%s" % (key, updates[key])
            done.add(key)
    if not lines:
        lines = ["# Agent RPG 2024 settings, written by install.py. DO NOT COMMIT this file."]
    for k, v in updates.items():
        if k not in done:
            lines.append("%s=%s" % (k, v))
    with open(ENV_FILE, "w", encoding="utf-8", newline="\n") as fh:
        fh.write("\n".join(lines) + "\n")
    if not WINDOWS:
        os.chmod(ENV_FILE, 0o600)


def parse_db_url(url):
    p = urllib.parse.urlparse(url)
    return {"user": urllib.parse.unquote(p.username or ""), "password": urllib.parse.unquote(p.password or ""),
            "host": p.hostname or "localhost", "port": str(p.port or 5432), "db": (p.path or "/").lstrip("/")}


def make_db_url(user, password, host, port, db):
    return "postgres://%s:%s@%s:%s/%s?sslmode=disable" % (urllib.parse.quote(user, safe=""), urllib.parse.quote(password, safe=""), host, port, db)


# ---------------------------------------------------------------- 1. tools

def find_go():
    cands = [shutil.which("go")]
    if WINDOWS:
        cands += [r"C:\Program Files\Go\bin\go.exe", os.path.expandvars(r"%USERPROFILE%\go\bin\go.exe")]
    else:
        cands += ["/usr/local/go/bin/go", "/opt/homebrew/bin/go", "/usr/lib/go/bin/go", os.path.expanduser("~/go/bin/go")]
    for c in cands:
        if c and os.path.exists(c):
            r = run([c, "version"])
            m = re.search(r"go(\d+)\.(\d+)", r.stdout)
            if m:
                return c, (int(m.group(1)), int(m.group(2))), r.stdout.strip()
    return None, None, None


def find_psql():
    cands = [shutil.which("psql")]
    if WINDOWS:
        cands += sorted(glob.glob(r"C:\Program Files\PostgreSQL\*\bin\psql.exe"), key=lambda p: int(re.search(r"PostgreSQL\\(\d+)", p).group(1)), reverse=True)
    elif MAC:
        cands += sorted(glob.glob("/opt/homebrew/opt/postgresql@*/bin/psql"), reverse=True) + ["/opt/homebrew/bin/psql", "/usr/local/bin/psql"]
        cands += sorted(glob.glob("/Applications/Postgres.app/Contents/Versions/*/bin/psql"), reverse=True)
    else:
        cands += sorted(glob.glob("/usr/lib/postgresql/*/bin/psql"), reverse=True)
    for c in cands:
        if c and os.path.exists(c):
            return c
    return None


def check_tools():
    step("1. Checking for Go and PostgreSQL")
    go, ver, text = find_go()
    if not go:
        say("Go was not found. Install Go 1.26 or newer, then run this script again:")
        if WINDOWS:
            say("  winget install GoLang.Go        (or download it from https://go.dev/dl/)")
        elif MAC:
            say("  brew install go                 (or download it from https://go.dev/dl/)")
        else:
            say("  sudo apt install golang-go      (Debian/Ubuntu; or download it from https://go.dev/dl/)")
        fail("Go is missing.")
    if ver < (1, 26):
        fail("%s is too old; Agent RPG 2024 needs Go 1.26 or newer (https://go.dev/dl/)." % text)
    say("Go: " + text)
    psql = find_psql()
    if not psql:
        say("PostgreSQL was not found. Install it (version 14 or newer), then run this script again:")
        if WINDOWS:
            say("  winget install PostgreSQL.PostgreSQL.17   (the installer asks you to choose a password for the 'postgres' user;")
            say("                                             remember it, this script asks for it)")
        elif MAC:
            say("  brew install postgresql@17 && brew services start postgresql@17")
        else:
            say("  sudo apt install postgresql               (Debian/Ubuntu; the service starts by itself)")
        fail("PostgreSQL is missing.")
    say("PostgreSQL client: " + psql)
    return go, psql


# ---------------------------------------------------------------- 2. database

class Admin:
    """Runs SQL as a PostgreSQL superuser, by password or (Linux/macOS) through `sudo -u postgres`."""

    def __init__(self, psql, host, port, user, password, use_sudo):
        self.psql, self.host, self.port, self.user, self.password, self.use_sudo = psql, host, port, user, password, use_sudo

    def sql(self, statement, db="postgres"):
        if self.use_sudo:
            cmd = ["sudo", "-u", self.user, self.psql, "-d", db]
            env = None
        else:
            cmd = [self.psql, "-h", self.host, "-p", self.port, "-U", self.user, "-d", db]
            env = dict(os.environ, PGPASSWORD=self.password)
        # SQL goes through stdin so passwords never appear in the process list.
        return run(cmd + ["-v", "ON_ERROR_STOP=1", "-t", "-A", "-q", "-f", "-"], env=env, input_text=statement)


def can_connect(psql, url):
    d = parse_db_url(url)
    r = run([psql, "-h", d["host"], "-p", d["port"], "-U", d["user"], "-d", d["db"], "-t", "-A", "-c", "SELECT 1"],
            env=dict(os.environ, PGPASSWORD=d["password"]))
    return r.returncode == 0 and r.stdout.strip() == "1", r.stderr.strip()


def lit(s):
    return "'" + s.replace("'", "''") + "'"


def setup_database(psql):
    step("2. Database")
    env = read_env()
    if env.get("DATABASE_URL") and not ARGS.reconfigure:
        ok, _ = can_connect(psql, env["DATABASE_URL"])
        if ok:
            d = parse_db_url(env["DATABASE_URL"])
            say("The database in .env works (database '%s', user '%s'); keeping it. Use --reconfigure to make a new one." % (d["db"], d["user"]))
            return env["DATABASE_URL"]
        say("The DATABASE_URL in .env does not connect; setting the database up again.")

    host = ask("PostgreSQL host", "localhost")
    port = ask("PostgreSQL port", "5432")
    admin_user = ask("PostgreSQL admin user", "postgres")
    use_sudo = False
    admin_pw = ask_secret("Password for the PostgreSQL user '%s'" % admin_user, "AGENTRPG_DB_ADMIN_PASSWORD")
    if not admin_pw and not WINDOWS and host in ("localhost", "127.0.0.1"):
        say("No password given: trying `sudo -u %s psql` (the usual setup on Linux and Homebrew)." % admin_user)
        use_sudo = True
    admin = Admin(psql, host, port, admin_user, admin_pw, use_sudo)
    r = admin.sql("SELECT split_part(current_setting('server_version'), ' ', 1)")
    if r.returncode != 0:
        say(r.stderr.strip())
        fail("Could not reach PostgreSQL as '%s' on %s:%s. Is the server running, and is the password right?" % (admin_user, host, port))
    say("Connected to PostgreSQL %s." % r.stdout.strip())

    db_name = ARGS.db_name or ask("Name of the game's database", "agentrpg")
    db_user = ARGS.db_user or ask("Name of the game's database user", "agentrpg")
    for label, val in (("database", db_name), ("user", db_user)):
        if not NAME_RE.match(val):
            fail("The %s name %r must be lower-case letters, digits and _ (starting with a letter or _)." % (label, val))

    if db_user == admin_user:
        db_pw = admin_pw
        if use_sudo:
            fail("With `sudo` sign-in the game needs its own database user with a password; choose a user other than '%s'." % admin_user)
    else:
        db_pw = secrets.token_urlsafe(24)  # stored in .env only, never printed
        exists = admin.sql("SELECT 1 FROM pg_roles WHERE rolname=%s" % lit(db_user)).stdout.strip() == "1"
        verb = "ALTER ROLE %s WITH LOGIN PASSWORD %s" if exists else "CREATE ROLE %s WITH LOGIN PASSWORD %s"
        r = admin.sql(verb % (db_user, lit(db_pw)))
        if r.returncode != 0:
            fail("Could not create the database user: " + r.stderr.strip())
        say(("Database user '%s' exists; gave it a new password." if exists else "Created the database user '%s'.") % db_user)

    owner = admin.sql("SELECT pg_get_userbyid(datdba) FROM pg_database WHERE datname=%s" % lit(db_name)).stdout.strip()
    if not owner:
        r = admin.sql("CREATE DATABASE %s OWNER %s ENCODING 'UTF8' TEMPLATE template0" % (db_name, db_user))
        if r.returncode != 0:
            fail("Could not create the database: " + r.stderr.strip())
        say("Created the database '%s'." % db_name)
    elif owner != db_user:
        n = admin.sql("SELECT count(*) FROM information_schema.tables WHERE table_schema='public'", db=db_name).stdout.strip()
        if n not in ("", "0"):
            fail("The database '%s' already exists, belongs to '%s' and has %s tables. Choose another database name (--db-name), "
                 "or use '%s' as the game's database user." % (db_name, owner, n, owner))
        admin.sql("ALTER DATABASE %s OWNER TO %s" % (db_name, db_user))
        say("The empty database '%s' now belongs to '%s'." % (db_name, db_user))
    else:
        say("The database '%s' already exists and belongs to '%s'; keeping its data." % (db_name, db_user))
    admin.sql("ALTER SCHEMA public OWNER TO %s" % db_user, db=db_name)

    url = make_db_url(db_user, db_pw, host, port, db_name)
    ok, err = can_connect(psql, url)
    if not ok:
        fail("The game's user cannot sign in to its database: " + err)
    return url


def write_settings(url):
    step("3. Writing .env")
    env = read_env()
    port = str(ARGS.port or env.get("PORT") or ask("Port for the game server", "8080"))
    updates = {"DATABASE_URL": url, "PORT": port}
    if not env.get("ADMIN_KEY"):
        updates["ADMIN_KEY"] = secrets.token_urlsafe(32)
    write_env(updates)
    say("Saved %s (database address, port %s%s). The file holds passwords: keep it private, never commit it."
        % (ENV_FILE, port, ", a new admin key" if "ADMIN_KEY" in updates else ""))
    return port


# ---------------------------------------------------------------- 4. build

def build(go):
    step("4. Building the server")
    stop_server(quiet=True)  # a running server.exe cannot be replaced on Windows
    env = dict(os.environ, CGO_ENABLED="0")
    for out, pkg in ((SERVER_BIN, "./cmd/server"), (CLI_BIN, "./cmd/arpg")):
        if not os.path.isdir(os.path.join(ROOT, pkg)):
            continue
        r = run([go, "build", "-o", out, pkg], env=env)
        if r.returncode != 0:
            say(r.stderr.strip()[-3000:])
            fail("The build of %s failed." % pkg)
        say("Built " + out)


# ---------------------------------------------------------------- server control

def server_env():
    env = dict(os.environ)
    vals = read_env()
    if not vals.get("DATABASE_URL"):
        fail("No DATABASE_URL in .env; run `python install.py` first.")
    env.update({k: v for k, v in vals.items() if k in ("DATABASE_URL", "PORT", "ADMIN_KEY", "RESEND_API_KEY", "RESEND_BASE_URL", "EMAIL_FROM", "PUBLIC_BASE_URL", "AGENTRPG_LOCAL_CONTENT")})
    return env, vals.get("PORT", "8080")


def health(port, timeout=2):
    try:
        with urllib.request.urlopen("http://localhost:%s/health" % port, timeout=timeout) as r:
            return r.status == 200
    except Exception:
        return False


def read_pid():
    try:
        with open(PID_FILE) as fh:
            return int(fh.read().strip())
    except Exception:
        return None


def pid_alive(pid):
    if not pid:
        return False
    if WINDOWS:
        r = run(["tasklist", "/FI", "PID eq %d" % pid, "/FO", "CSV", "/NH"])
        return str(pid) in r.stdout
    try:
        os.kill(pid, 0)
        return True
    except OSError:
        return False


def start_server(wait=240):
    env, port = server_env()
    if not os.path.exists(SERVER_BIN):
        fail("The server is not built yet; run `python install.py` first.")
    if health(port):
        say("Something is already answering on port %s (stop it first with `python install.py --stop`)." % port)
        return port
    out = open(os.path.join(ROOT, "server.log"), "ab")
    err = open(os.path.join(ROOT, "server.err"), "ab")
    kw = {}
    if WINDOWS:
        kw["creationflags"] = 0x00000008 | 0x00000200  # DETACHED_PROCESS | CREATE_NEW_PROCESS_GROUP
    else:
        kw["start_new_session"] = True
    p = subprocess.Popen([SERVER_BIN], cwd=ROOT, env=env, stdout=out, stderr=err, stdin=subprocess.DEVNULL, **kw)
    with open(PID_FILE, "w") as fh:
        fh.write(str(p.pid))
    say("Starting the server (the first start loads the SRD data and can take a minute or two)...")
    t0 = time.time()
    while time.time() - t0 < wait:
        if p.poll() is not None:
            fail("The server stopped by itself; see server.err and server.log in %s." % ROOT)
        if health(port):
            say("The server is running: http://localhost:%s  (process %d; logs in server.log and server.err)" % (port, p.pid))
            return port
        time.sleep(2)
    fail("The server did not answer within %d seconds; see server.err and server.log." % wait)


def stop_server(quiet=False):
    pid = read_pid()
    if pid_alive(pid):
        if WINDOWS:
            run(["taskkill", "/PID", str(pid), "/T", "/F"])
        else:
            import signal
            os.kill(pid, signal.SIGTERM)
        for _ in range(20):
            if not pid_alive(pid):
                break
            time.sleep(0.5)
        say("Stopped the server (process %d)." % pid)
    elif not quiet:
        say("No server started by this script is running.")
    if os.path.exists(PID_FILE):
        os.remove(PID_FILE)


def check_seeded(port, offsets):
    """Read what the server logged during this start and check the SRD data is really in the database."""
    text = ""
    for f, off in offsets.items():  # Go's log package writes to stderr
        if os.path.exists(f):
            with open(f, "rb") as fh:
                fh.seek(off)
                text += fh.read().decode("utf-8", errors="replace")
    if "Connected to Postgres" not in text:
        fail("The server started but did not connect to the database; see server.err.")
    problems = [l for l in text.splitlines() if "Schema error" in l or "pq:" in l or "failed" in l.lower()]
    if problems:
        say("The server logged %d database problem(s) while loading its data, for example:" % len(problems))
        for l in problems[:8]:
            say("  " + l[:240])
        fail("The data did not load cleanly; see server.err (and please report it).")
    counts = {}
    for section, minimum in (("spells", 300), ("monsters", 300), ("classes", 12)):
        try:
            with urllib.request.urlopen("http://localhost:%s/api/universe/%s" % (port, section), timeout=30) as r:
                body = json.load(r)
        except Exception as e:
            fail("Could not read /api/universe/%s: %s" % (section, e))
        items = body.get(section) if isinstance(body, dict) else body
        n = len(items) if isinstance(items, list) else int(body.get("count") or 0)
        counts[section] = n
        if n < minimum:
            fail("Only %d %s are in the database (expected at least %d); see server.err." % (n, section, minimum))
    say("Data loaded: %d spells, %d monsters, %d classes." % (counts["spells"], counts["monsters"], counts["classes"]))


# ---------------------------------------------------------------- 6. accounts

def register_accounts(port):
    step("6. Accounts (optional)")
    if not ask_yes("Register a GM account and a player account now?", default=False):
        say("Skipped. Agents can register themselves later with POST /api/register.")
        return
    os.makedirs(TOKEN_DIR, exist_ok=True)
    for role, default in (("GM", "my-gm"), ("player", "my-player")):
        name = ask("Name for the %s account" % role, default)
        req = urllib.request.Request("http://localhost:%s/api/register" % port, method="POST",
                                     data=json.dumps({"name": name}).encode(), headers={"Content-Type": "application/json"})
        try:
            with urllib.request.urlopen(req, timeout=30) as r:
                body = json.load(r)
        except urllib.error.HTTPError as e:
            say("Could not register %s: %s" % (name, e.read().decode(errors="replace")[:300]))
            continue
        token = body.get("token") or body.get("api_token")
        if not token:
            say("Registered %s, but no token came back: %s" % (name, str(body)[:300]))
            continue
        path = os.path.join(TOKEN_DIR, name + ".token")
        with open(path, "w", encoding="utf-8") as fh:
            fh.write(token)
        if not WINDOWS:
            os.chmod(path, 0o600)
        say("Registered the %s '%s'. Its token is saved in %s (shown nowhere else; send it as 'Authorization: Bearer <token>')." % (role, name, path))


# ---------------------------------------------------------------- main

def setup():
    say("Agent RPG 2024 installer (folder: %s)" % ROOT)
    go, psql = check_tools()
    url = setup_database(psql)
    port = write_settings(url)
    build(go)
    step("5. First start (creates the tables and loads the SRD 5.2.1 data)")
    logs = [os.path.join(ROOT, n) for n in ("server.log", "server.err")]
    offsets = {f: (os.path.getsize(f) if os.path.exists(f) else 0) for f in logs}
    start_server()
    check_seeded(port, offsets)
    register_accounts(port)
    step("7. Done")
    if ask_yes("Keep the server running?", default=True):
        say("Open http://localhost:%s in a browser; the API reference is at http://localhost:%s/docs" % (port, port))
        say("Later: `python install.py --stop` to stop it, `python install.py --start` to start it again.")
    else:
        stop_server()
        say("Start it any time with `python install.py --start`.")


def main():
    global ARGS
    ap = argparse.ArgumentParser(description="Install and run Agent RPG 2024.")
    ap.add_argument("--start", action="store_true", help="start the server with the settings in .env")
    ap.add_argument("--stop", action="store_true", help="stop a server started by this script")
    ap.add_argument("--status", action="store_true", help="say whether the server is running")
    ap.add_argument("--yes", action="store_true", help="take every default without asking")
    ap.add_argument("--reconfigure", action="store_true", help="set the database up again even if .env works")
    ap.add_argument("--port", type=int, help="port for the game server (default 8080)")
    ap.add_argument("--db-name", help="name of the game's database (default agentrpg)")
    ap.add_argument("--db-user", help="name of the game's database user (default agentrpg)")
    ARGS = ap.parse_args()
    if sys.version_info < (3, 8):
        fail("Python 3.8 or newer is needed.")
    try:
        if ARGS.stop:
            stop_server()
        elif ARGS.status:
            _, port = server_env()
            say(("Running on http://localhost:%s" if health(port) else "Not answering on port %s.") % port)
        elif ARGS.start:
            start_server()
        else:
            setup()
    except KeyboardInterrupt:
        fail("Interrupted.")


if __name__ == "__main__":
    main()
