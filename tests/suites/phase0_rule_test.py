# Phase 0 of the Nov 15 code audit (CODE_AUDIT_REPORT.md): the rules that closed the two Critical findings and two more.
#   AUD-01  every character a request names must be in the campaign it acts in: GM commands (gmReadAs reads every field of
#           characterIDFields, gm_campaign_2024.go) and players' actions (namedCharactersIn, campaign_access_2024.go).
#           Tested on every POST /api/gm/* route of the server's own API reference that takes a character-id field, so a
#           new route is covered without changing this file; the victim must be untouched afterwards.
#   AUD-02  a transaction reads through itself (sale, purchase): a sale while my-turn is polled can't freeze the server.
#   AUD-04  purchases sent together pay one after another (the wallet is read inside the purchase's transaction).
#   AUD-03  the sign-in throttle never refuses a valid token.
# Local:   python3 phase0_rule_test.py      Staging: ARPG_TARGET=staging python3 phase0_rule_test.py
import json, random, threading, time, sys, urllib.request
from ah import call, register, build, BASE, only, _ctx, LOCAL

only("local", "staging", why="it sends a GM command for another campaign's character to every GM route")
passed = failed = 0
fails = []
def check(name, ok, detail=None):
    global passed, failed
    if ok:
        passed += 1; print("PASS", name)
    else:
        failed += 1; fails.append(name); print("FAIL", name, "|", json.dumps(detail)[:400])

CAMPAIGN_CODES = ("not_gm", "campaign_not_active", "character_not_found", "character_not_in_campaign", "not_in_campaign",
                  "targets_in_different_campaigns")
ID_FIELDS = {"character_id", "character_ids", "target_id", "target_ids", "caster_id", "attacker_id", "defender_id", "initiator_id",
             "grappler_id", "combatant_id", "redirect_target_id", "sculpt_targets", "paladin_id", "bard_id", "barbarian_id",
             "owner_id", "give_to"}

def campaign(tok, name):
    c, j = call("POST", "/api/campaigns", {"name": f"{name} {random.randint(1000, 9999)}", "setting": "t", "max_players": 6}, tok)
    return j["campaign_id"]

# Two campaigns: A (the attacker runs it, with the attacker's own player) and B (another GM, with the victim).
GA, PA, PA2 = register("P0GmA")["token"], register("P0PlA")["token"], register("P0PlA2")["token"]
GB, PB = register("P0GmB")["token"], register("P0PlB")["token"]
A, B = campaign(GA, "P0A"), campaign(GB, "P0B")
own, _ = build(PA, "wizard", "sage", "elf", "P0Own")       # the attacker's player, a caster with Fire Bolt
ally, _ = build(PA2, "fighter", "soldier", "human", "P0Ally")  # one character per player in a campaign
victim, _ = build(PB, "fighter", "soldier", "human", "P0Vic")
for camp, ch, tok in ((A, own, PA), (A, ally, PA2), (B, victim, PB)):
    c, j = call("POST", f"/api/campaigns/{camp}/join", {"character_id": ch}, tok)
    assert c == 200, (camp, ch, c, j)
for camp, tok in ((A, GA), (B, GB)):
    c, j = call("POST", f"/api/campaigns/{camp}/start", {}, tok)
    assert c == 200, (camp, c, j)

def state(cid, tok):
    c, j = call("GET", f"/api/characters/{cid}", None, tok)
    keep = ("hp", "max_hp", "temp_hp", "conditions", "exhaustion_level", "gold", "is_dead", "xp", "level", "inventory")
    return {k: j.get(k) for k in keep}
before = state(victim, PB)

# ---------------------------------------------------------------- AUD-01, GM commands: every route of the reference
c, ref = call("GET", "/docs/swagger.json")
routes = []
for path, ops in ref.get("paths", {}).items():
    if not path.startswith("/api/gm/") or "post" not in ops or "{" in path:
        continue
    props = (((ops["post"].get("requestBody") or {}).get("content") or {}).get("application/json") or {}).get("schema", {}).get("properties", {})
    for f, sch in props.items():
        if f in ID_FIELDS:
            routes.append((path, f, sch.get("type") == "array"))
check("the reference lists GM routes with a character-id field (%d route/field pairs)" % len(routes), len(routes) >= 60, len(routes))
acted = []
for path, field, many in routes:
    c, j = call("POST", path, {field: [victim] if many else victim}, GA, label="aud01 " + path)
    if not (400 <= c < 500 and j.get("error") in CAMPAIGN_CODES):
        acted.append((path, field, c, j.get("error"), (j.get("message") or "")[:120]))
check("every GM route refuses another campaign's character with a campaign code", not acted, acted[:12])
# the three reproduced attacks, with full bodies
for path, body in (("/api/gm/trap", {"trap": "collapsing_roof", "target_ids": [victim], "level": 17}),
                   ("/api/gm/apply-poison", {"poison": "wyvern_poison", "target_id": victim}),
                   ("/api/gm/environmental-hazard", {"hazard": "dehydration", "character_id": victim})):
    c, j = call("POST", path, body, GA)
    check(f"{path} with a full body: refused for another campaign's character ({c} {j.get('error')})", 400 <= c < 500 and j.get("error") in CAMPAIGN_CODES, j)
# the same command still works in the GM's own campaign (no regression)
c, j = call("POST", "/api/gm/trap", {"trap": "collapsing_roof", "target_ids": [ally], "level": 1}, GA)
check(f"the trap still works on the GM's own campaign's character ({c})", c == 200, j)

# ---------------------------------------------------------------- AUD-01, players' actions
for body in ({"action": "cast", "spell_slug": "fire-bolt", "target_id": victim},
             {"action": "cast", "spell_slug": "fire-bolt", "target_ids": [victim]},
             {"action": "use_item", "item": "Potion of Healing", "target_character_id": victim}):
    c, j = call("POST", "/api/action", dict(body, character_id=own), PA)
    check(f"a player's {body['action']} naming another campaign's character: 404 character_not_found ({c} {j.get('error')})",
          c == 404 and j.get("error") == "character_not_found", j)
c, j = call("POST", "/api/action", {"action": "cast", "spell_slug": "fire-bolt", "target_id": ally, "character_id": own}, PA)
check(f"a cast at a character in the same campaign is not refused by the rule ({c} {j.get('error')})", j.get("error") != "character_not_found", j)

after = state(victim, PB)
check("the victim is untouched (HP, conditions, exhaustion, gold, inventory, XP)", before == after, {"before": before, "after": after})

# ---------------------------------------------------------------- AUD-04 and AUD-02: shops
c, j = call("POST", "/api/gm/shops", {"campaign_id": A, "open": "general_store", "name": "P0 Store"}, GA)
shop = j.get("shop_id")
check("the GM opens a shop", bool(shop), j)
call("POST", "/api/gm/gold", {"character_id": ally, "set": 8}, GA)
c, j = call("POST", "/api/gm/update-character", {"character_id": ally, "set": {"gold": 8, "silver": 0, "copper": 0, "electrum": 0, "platinum": 0}}, GA)
res = []
def buy():
    res.append(call("POST", f"/api/campaigns/{A}/shops/{shop}", {"character_id": ally, "buy": [{"item": "Rope", "quantity": 1}]}, PA2))
ts = [threading.Thread(target=buy) for _ in range(8)]
[t.start() for t in ts]; [t.join() for t in ts]
ok = [r for r in res if r[0] == 200]
c, j = call("GET", f"/api/characters/{ally}", None, PA2)
gold = (j.get("gold") or 0) * 100 + (j.get("silver") or 0) * 10 + (j.get("copper") or 0)
price = 800 - gold if len(ok) == 1 else None
check(f"8 purchases at once with 8 GP: each one paid ({len(ok)} bought, {gold} CP left)", len(ok) >= 1 and gold >= 0 and gold == 800 - len(ok) * ((800 - gold) // max(len(ok), 1)) and (800 - gold) % max(len(ok), 1) == 0 and (800 - gold) // max(len(ok), 1) > 0, [(r[0], r[1].get("paid") or r[1].get("error")) for r in res])

# AUD-02: sell one item at a time while four clients poll my-turn; every sale must answer within a few seconds
stop, slow = [False], []
def poll():
    while not stop[0]:
        call("GET", "/api/my-turn", None, PA2)
pollers = [threading.Thread(target=poll, daemon=True) for _ in range(4)]
[t.start() for t in pollers]
sold = 0
for i in range(12):
    c, j = call("POST", "/api/gm/give-item", {"character_id": ally, "item_name": "Rope", "quantity": 1}, GA)
    c, j = call("GET", f"/api/characters/{ally}", None, PA2)
    rope = next((x["id"] for x in j.get("inventory", []) if x.get("name") == "Rope"), None)
    t0 = time.time()
    c, j = call("POST", f"/api/campaigns/{A}/shops/{shop}", {"character_id": ally, "sell": [{"item_id": rope, "quantity": 1}]}, PA2)
    if time.time() - t0 > 15:
        slow.append(round(time.time() - t0, 1))
    sold += c == 200
stop[0] = True
check(f"12 sales while my-turn is polled: none hangs ({sold} sold, slow: {slow})", not slow and sold >= 10, slow)
c, j = call("GET", "/api/version")
check("the server still answers afterwards", c == 200, j)

# ---------------------------------------------------------------- AUD-03: a valid token is never throttled
for i in range(32):
    call("GET", "/api/my-turn", None, "arpg_wrong_" + str(i))
c, j = call("GET", "/api/my-turn", None, "arpg_wrong_last")
if LOCAL:  # on Render the address is the client's (CF-Connecting-IP), and this test's outgoing address changes per request
    check(f"after 32 wrong tokens, a wrong token is throttled ({c} {j.get('error')})", c == 429 or "too_many" in json.dumps(j), j)
c, j = call("GET", "/api/my-turn", None, PA)
check(f"after 32 wrong tokens from this address, a valid token still works ({c})", c == 200, j)

print(f"\n{passed} of {passed + failed} checks passed")
if fails:
    print("FAILED:", fails)
    sys.exit(1)
