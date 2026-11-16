# Who may act on a campaign or character named in the path (open item 15): one owner, campaign_access_2024.go.
# Every route of the API reference with {id} under /api/campaigns/ or /api/characters/ is sent, as a stranger (an account with
# its own character in another campaign) and with an id that doesn't exist: a stranger never gets 2xx on a write, nothing gets
# 5xx, and every refusal uses the owner's codes. Then the GM-only routes are checked as the GM, a player and a stranger.
# Local: python3 access_rule_test.py   (it refuses the live server: it sends writes to every route)
import json, random
from ah import call, register, build, BASE, only
only("local", "staging", why="it sends writes to every route")
results = []
def check(name, ok, detail=""):
    results.append((name, bool(ok)))
    print(("PASS " if ok else "FAIL ") + name + ("" if ok else "  -> " + json.dumps(detail)[:400]), flush=True)
OWNER_CODES = {"campaign_not_found", "character_not_found", "not_gm", "not_in_campaign", "not_your_character"}
OLD_CODES = {"gm_only", "not_your_character_or_campaign", "only_dm_can_start", "only_dm_can_end", "not_in_this_campaign",
             "only_the_gm_can_do_that"}

G = register("AcGM")["token"]; P = register("AcP")["token"]; S = register("AcS")["token"]; SG = register("AcSG")["token"]
pc, f = build(P, "fighter", "soldier", "human", "AcP"); assert pc, f
sc, f = build(S, "rogue", "criminal", "halfling", "AcS"); assert sc, f
c, camp = call("POST", "/api/campaigns", {"name": "Ac %d" % random.randint(1000, 99999), "setting": "t", "max_players": 4}, G); cmp = camp["campaign_id"]
c, camp2 = call("POST", "/api/campaigns", {"name": "Ac2 %d" % random.randint(1000, 99999), "setting": "t", "max_players": 4}, SG); cmp2 = camp2["campaign_id"]
call("POST", f"/api/campaigns/{cmp}/join", {"character_id": pc}, P); call("POST", f"/api/campaigns/{cmp2}/join", {"character_id": sc}, S)
call("POST", f"/api/campaigns/{cmp}/start", None, G); call("POST", f"/api/campaigns/{cmp2}/start", None, SG)

spec = call("GET", "/docs/swagger.json")[1]
routes = []
for path, ops in (spec.get("paths") or {}).items():
    if not (path.startswith("/api/campaigns/{") or path.startswith("/api/characters/{")):
        continue
    for m in ops:
        if m.upper() in ("POST", "PUT", "DELETE", "PATCH"):
            routes.append((m.upper(), path))
check(f"the API reference lists write routes with a campaign or character in the path ({len(routes)})", len(routes) > 30, len(routes))

def fill(path, cid, chid):
    import re
    out = path
    out = out.replace("{id}", str(cid if path.startswith("/api/campaigns/") else chid), 1)
    return re.sub(r"\{[^}]+\}", "1", out)

bad_stranger, five, old, notowner = [], [], [], []
for m, path in routes:
    p = fill(path, cmp, pc)
    c, j = call(m, p, {}, S, "access")
    code = j.get("error") if isinstance(j, dict) else None
    if 200 <= c < 300: bad_stranger.append((m, p, c))
    if c >= 500: five.append((m, p, c, code))
    if code in OLD_CODES: old.append((m, p, code))
    if c == 403 and code not in OWNER_CODES and code not in ("private_campaign",): notowner.append((m, p, code))
    for unknown in (fill(path, 9999999, 9999999),):
        c2, j2 = call(m, unknown, {}, G, "access")
        if c2 >= 500: five.append((m, unknown, c2, j2.get("error") if isinstance(j2, dict) else None))
check("a stranger never gets a 2xx on a write to another campaign's or player's route", not bad_stranger, bad_stranger)
check("no route answers 5xx to a stranger or to an id that doesn't exist", not five, five)
check("no route answers with an old access code", not old, old)
check("every 403 uses the owner's codes", not notowner, notowner)

# the GM-only routes: as the GM, a player of the campaign, a stranger, and for a campaign that doesn't exist
for name, m, path, body in [("start a fight", "POST", "/api/campaigns/{c}/combat/start", None),
                            ("end the fight", "POST", "/api/campaigns/{c}/combat/end", {}),
                            ("change the table rules", "POST", "/api/campaigns/{c}/table-rules", {"encumbrance": "ignored"}),
                            ("create a campaign item", "POST", "/api/campaigns/{c}/items", {"name": "Ac Token", "slug": "ac-token", "item_type": "item"})]:
    for who, tok, want in (("player", P, (403, "not_gm")), ("stranger", S, (403, "not_gm"))):
        c, j = call(m, path.replace("{c}", str(cmp)), body, tok)
        check(f"{name}: a {who} -> 403 not_gm, the message names the campaign", (c, j.get("error")) == want and str(cmp) in j.get("message", ""), (c, j))
    c, j = call(m, path.replace("{c}", "9999999"), body, G)
    check(f"{name}: a campaign that doesn't exist -> 404 campaign_not_found", (c, j.get("error")) == (404, "campaign_not_found"), (c, j))
    c, j = call(m, path.replace("{c}", str(cmp)), body, G)
    check(f"{name}: the GM -> 2xx", 200 <= c < 300, (c, j))
c, j = call("POST", f"/api/campaigns/{cmp}/combat/start", None)
check("not signed in -> 401", c == 401, (c, j))

# character routes: the owner, the GM of its campaign, a stranger, an id that doesn't exist
c, j = call("POST", f"/api/characters/{pc}/profile", {"appearance": "tall"}, S)
check("a stranger changing a character's profile -> 403 not_your_character", (c, j.get("error")) == (403, "not_your_character"), (c, j))
c, j = call("POST", f"/api/characters/9999999/profile", {"appearance": "tall"}, P)
check("a character that doesn't exist -> 404 character_not_found", (c, j.get("error")) == (404, "character_not_found"), (c, j))
c, j = call("POST", f"/api/characters/{pc}/profile", {"appearance": "tall"}, P)
check("the owner changes the profile", c == 200, (c, j))
c, j = call("POST", f"/api/characters/{pc}/class-options", {}, G)
c2, j2 = call("POST", f"/api/characters/{pc}/class-options", {}, S)
check("class options: the GM of its campaign gets past the access check, a stranger doesn't", c != 403 and c2 == 403 and j2.get("error") == "not_your_character", (c, j, c2, j2))
c, j = call("POST", "/api/characters/equip-armor", {"character_id": pc, "armor": "leather"}, S)
check("equipping armor on another player's character -> 403 not_your_character", (c, j.get("error")) == (403, "not_your_character"), (c, j))
c, j = call("POST", "/api/campaigns/messages", {"campaign_id": cmp, "message": "hi"}, S)
check("a stranger posting to a campaign's messages -> 403 not_in_campaign (was 401)", (c, j.get("error")) == (403, "not_in_campaign"), (c, j))

print(f"\n{sum(ok for _, ok in results)} of {len(results)} checks passed")
import sys
sys.exit(0 if all(ok for _, ok in results) else 1)  # a failed check fails the run (audit AUD-48)
