# The start (Matt, Nov 15): an agent given only the server's address, and perhaps "be the GM" or "find an open player seat",
# finds everything it needs, the same everywhere, from one owner (cmd/server/start_2024.go). Sign-in is by token only; email
# is optional and is the only way to recover a lost token.
# Local: python3 start_rule_test.py   Live (after a deploy): BASE=https://rpg.umbra13.dev ARPG_SECRETS=<dir> python3 start_rule_test.py
import json, random, re, urllib.request, ssl
from ah import call, build, BASE, LOCAL
from target import HOSTS, TARGET
SAME_SERVER = [BASE.split("//")[1]] + [h for h, t in HOSTS.items() if t == TARGET and t != "local"]  # a server may name its public address
results = []
def check(name, ok, detail=""):
    results.append((name, bool(ok)))
    print(("PASS " if ok else "FAIL ") + name + ("" if ok else "  -> " + json.dumps(detail)[:500]), flush=True)
from target import CTX as _ctx
def page(p):
    with urllib.request.urlopen(urllib.request.Request(BASE + p, headers={"User-Agent": "agentrpg-test"}), context=_ctx, timeout=60) as x:
        return x.read().decode()
import html as _h
def text(s): return _h.unescape(re.sub(r"\s+", " ", re.sub(r"<[^>]+>", " ", s)))

# 1. What an agent sees from the address alone: the home page, /llms.txt, GET /api/ and skill.md give the same start.
home, llms, skill = text(page("/")), page("/llms.txt"), page("/skill.md/raw")
c, api = call("GET", "/api/")
start = api.get("start") or {}
check("GET /api/ carries the start (register, auth, recovery, both paths, check-in)",
      c == 200 and all(start.get(k) for k in ("register", "auth", "recovery", "paths", "check_in")) and
      [p["key"] for p in start["paths"]] == ["player", "gm"], api)
for name, doc in (("home page", home), ("llms.txt", llms), ("skill.md", skill)):
    steps = [s["request"] for p in start.get("paths", []) for s in p["steps"]]
    missing = [s for s in steps + [start.get("register", "?")] if s.replace("&amp;", "&") not in doc.replace("&amp;", "&").replace("&#34;", '"').replace("&quot;", '"')]
    check(f"{name} shows every start step and the register request", not missing, missing)
    low = doc.lower()
    check(f"{name} says sign-in is a Bearer token and asks for no password", "authorization: bearer" in low and '"password"' not in low and "basic auth (" not in low and "base64(" not in low, name)
    check(f"{name} says email is optional and a lost token may be lost for good, and suggests asking the human for AgentMail",
          "optional" in low and ("lost for good" in low or "can't send email" in low) and ("agentmail" in low or "can't send email" in low), name)
check("skill.md's start is this server's (its address, not YOUR-SERVER)", "YOUR-SERVER" not in skill and any(h in skill for h in SAME_SERVER) or LOCAL, skill[:200])

# 2. Registration: a name is enough; the reply gives the token, recovery, both paths and the check-in; no password talk.
name = "Start%d" % random.randint(10000, 99999)
c, reg = call("POST", "/api/register", {"name": name})
tok = reg.get("token")
check("register with a name: 200, a token, and the start", c == 200 and tok and reg.get("paths") and reg.get("next_step") and reg.get("check_in") and reg.get("recovery"), reg)
check("register reply has no password or Basic auth instructions and no CRITICAL_ fields",
      not any(k.startswith("CRITICAL") or k.startswith("auth_") for k in reg) and "password" not in json.dumps({k: v for k, v in reg.items() if k != "token"}).lower(), list(reg))
check("register reply's next step offers both paths", "character-builder" in reg.get("next_step", "") and "/api/campaigns" in reg.get("next_step", ""), reg.get("next_step"))
c, reg2 = call("POST", "/api/register", {"name": name + "p", "password": "old-guide"})
check("a password sent by an old guide is ignored and the reply says so", c == 200 and reg2.get("token") and "token only" in reg2.get("password_ignored", ""), reg2)
# Test accounts never use email on live (Matt, Nov 15): the email registration runs only on a local server, which sends no
# mail (email_rule_test.py covers email with a mail stand-in).
reg3 = {}
if LOCAL:
    c, reg3 = call("POST", "/api/register", {"name": name + "e", "email": name.lower() + "@example.com"})
    check("register with an email: recovery says what happens next (a code to confirm, or the server can't send email)",
          c == 200 and ("POST /api/verify" in reg3.get("recovery", "") or "can't send email" in reg3.get("recovery", "")) and reg3.get("verification_sent") in (True, False), reg3)
c, em = call("GET", "/api/account/email", None, tok)
check("GET /api/account/email with no email: recovery note (lost for good, AgentMail) or the server can't send email",
      c == 200 and em.get("email") is None and ("lost for good" in em.get("recovery", "") or "can't send email" in em.get("recovery", "")), em)
c, bad = call("GET", "/api/my-turn")
help_ = json.dumps(bad.get("help", {}))
check("a request without a token: 401 that says Bearer, how to register, the server's own skill.md",
      c == 401 and "Bearer" in help_ and "/api/register" in help_ and "strangerloops" not in help_ and "password" not in help_.lower(), bad)
c, hb = call("GET", "/api/heartbeat", None, tok)
can_mail = "can't send email" not in start.get("recovery", "")
check("heartbeat of a new account: the start (both paths); no old /api/profile/email warning; the recovery note only if the server sends mail",
      c == 200 and (hb.get("start") or {}).get("paths") and "profile/email" not in json.dumps(hb) and bool(hb.get("recovery")) == can_mail, hb)
if not can_mail and LOCAL:
    c, rs = call("POST", "/api/account/email", None, reg3.get("token"))
    check("on a server that can't send mail, a resend is refused (email_not_configured)", c == 503 and rs.get("error") == "email_not_configured", (c, rs))

# 3. The GM path, then the player path, as the start describes them.
gm = call("POST", "/api/register", {"name": "StartGM%d" % random.randint(10000, 99999)})[1]["token"]
c, camp = call("POST", "/api/campaigns", {"name": "Start Camp %d" % random.randint(1000, 99999), "setting": "a mill in the rain", "max_players": 2}, gm)
cmp = camp.get("campaign_id")
check("GM path step 1: create the campaign", c in (200, 201) and cmp, camp)
c, cd = call("GET", "/api/campaigns/%s" % cmp, None, gm)
check("GM path step 2: GET /api/campaigns/{id} answers for the new campaign", c == 200, cd)
def listed(q):
    c, l = call("GET", "/api/campaigns" + q)
    return c, {x["id"]: x for x in l.get("campaigns", [])} if isinstance(l, dict) else {}, l
c, lst, raw = listed("?open=true&level=1")
check("player path step 3: the new campaign is listed with open=true&level=1, open_seats 2", c == 200 and lst.get(cmp, {}).get("open_seats") == 2, lst.get(cmp, raw))
c, lst, raw = listed("?level=5")
check("level=5 leaves out a level-1 campaign", c == 200 and cmp not in lst, lst.get(cmp))
for q in ("?open=maybe", "?level=0", "?level=x"):
    c, j = call("GET", "/api/campaigns" + q)
    check(f"GET /api/campaigns{q} is refused with invalid_query", c == 400 and j.get("error") == "invalid_query", (c, j))
cid, f = build(tok, "fighter", "soldier", "human", "Start")
check("player path step 1: a character from the builder", bool(cid), f)
c, s = call("GET", f"/api/characters/{cid}/setup", None, tok)
check("player path step 2: GET /api/characters/{id}/setup answers", c == 200, s)
c, j = call("POST", f"/api/campaigns/{cmp}/join", {"character_id": cid}, tok)
check("player path step 4: join; the reply has check_in and no CRITICAL_ field", c == 200 and j.get("check_in") and not any(k.startswith("CRITICAL") for k in j), j)
c, lst, raw = listed("?open=true")
check("after one join, open_seats is 1", lst.get(cmp, {}).get("open_seats") == 1, lst.get(cmp, raw))
tok2 = call("POST", "/api/register", {"name": "Start2%d" % random.randint(10000, 99999)})[1]["token"]
cid2, f2 = build(tok2, "wizard", "sage", "elf", "Start")
c, j = call("POST", f"/api/campaigns/{cmp}/join", {"character_id": cid2}, tok2)
c, lst, raw = listed("?open=true")
c_all, lst_all, _ = listed("")
check("a full campaign leaves the open list but stays in the full list with open_seats 0", cmp not in lst and lst_all.get(cmp, {}).get("open_seats") == 0, (lst.get(cmp), lst_all.get(cmp)))
tok3 = call("POST", "/api/register", {"name": "Start3%d" % random.randint(10000, 99999)})[1]["token"]
cid3, f3 = build(tok3, "rogue", "criminal", "halfling", "Start")
c, j = call("POST", f"/api/campaigns/{cmp}/join", {"character_id": cid3}, tok3)
check("joining the full campaign is refused (campaign_full), the same seat rule", c == 409 and j.get("error") == "campaign_full", (c, j))
c, j = call("GET", f"/api/campaigns/{cmp}", None, gm)
check("GM path step 2: the campaign lists both characters who joined", all(str(x) in json.dumps(j) for x in (cid, cid2)), j)
c, j = call("POST", f"/api/campaigns/{cmp}/start", None, gm)
check("GM path step 2: POST /api/campaigns/{id}/start", c == 200, (c, j))
c, gs = call("GET", "/api/gm/status?campaign_id=%s" % cmp, None, gm)
check("GM path step 3: GET /api/gm/status answers once the campaign has started", c == 200, (c, gs))
c, j = call("GET", "/api/my-turn", None, tok)
check("player path step 5: GET /api/my-turn answers", c == 200, (c, j))

print(f"\n{sum(ok for _, ok in results)} of {len(results)} checks passed")
import sys
sys.exit(0 if all(ok for _, ok in results) else 1)  # a failed check fails the run (audit AUD-48)
