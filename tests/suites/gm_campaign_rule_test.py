# The GM-campaign rule (game repo cmd/server/gm_campaign_2024.go): every GM command finds its campaign the same way.
# The campaign is the one named (campaign_id), else the campaign of the characters the command acts on, else the GM's only
# campaign; it must be the GM's and active (set-up and repair tools: also not yet started); never an ended one. Every
# refusal has one code: not_gm, campaign_not_active, campaign_id_required, character_not_found, character_not_in_campaign,
# not_in_campaign, targets_in_different_campaigns.
# Every GM route that acts on characters is sent each case; the routes without a character, the GM status GET, a deadline
# and the character conditions route are checked too.
# Local: python3 gm_campaign_rule_test.py
import json, random, sys
from ah import call, register, build, BASE, only

only("local", "staging", why="it sends refusals to every GM route")
passed = failed = 0
fails = []
def check(name, ok, detail=None):
    global passed, failed
    if ok: passed += 1
    else:
        failed += 1; fails.append(name); print("FAIL", name, "|", json.dumps(detail)[:400])

CAMPAIGN_CODES = ("not_gm", "campaign_not_active", "campaign_id_required", "character_not_found", "character_not_in_campaign",
                  "not_in_campaign", "targets_in_different_campaigns", "campaign_lookup_failed")

G = register("GcGM")["token"]       # runs A and B (active), R (not started), E (ended)
O = register("GcOther")["token"]    # another GM: runs X
P = register("GcP")["token"]        # the player: one character in each, and one in none
P2 = register("GcP2")["token"]      # a second player in A

def campaign(tok, name):
    c, j = call("POST", "/api/campaigns", {"name": f"{name} {random.randint(1000,9999)}", "setting": "t", "max_players": 6}, tok)
    return j["campaign_id"]
def char(label, tok=None):
    cid, _ = build(tok or P, "fighter", "soldier", "human", label)
    return cid

A, B, R, E, X = campaign(G, "GcA"), campaign(G, "GcB"), campaign(G, "GcR"), campaign(G, "GcE"), campaign(O, "GcX")
a1, b1, r1, e1, x1, f1 = (char(n) for n in ("GcA1", "GcB1", "GcR1", "GcE1", "GcX1", "GcF1"))
a2 = char("GcA2", P2)
for camp, ch in ((A, a1), (A, a2), (B, b1), (R, r1), (E, e1), (X, x1)):
    c, j = call("POST", f"/api/campaigns/{camp}/join", {"character_id": ch}, P2 if ch == a2 else P)
    assert c == 200, (camp, ch, c, j)
for camp, tok in ((A, G), (B, G), (E, G), (X, O)):
    c, j = call("POST", f"/api/campaigns/{camp}/start", {}, tok)
    assert c == 200, (camp, c, j)
c, j = call("POST", f"/api/campaigns/{E}/end", {}, G)
assert c == 200, (c, j)

# route, the field(s) that name characters (a list field takes a list), works before the start
TARGET_ROUTES = [
    ("/api/gm/award-xp", "character_ids", True), ("/api/gm/gold", "character_ids", True), ("/api/gm/gold", "character_id", True),
    ("/api/gm/give-item", "character_id", True), ("/api/gm/update-character", "character_id", True),
    ("/api/gm/restore-action", "character_id", True), ("/api/gm/update-action-time", "character_id", True),
    ("/api/gm/nudge", "character_id", False), ("/api/gm/melee-range", "character_id", False),
    ("/api/gm/trigger-readied", "character_id", False), ("/api/gm/preserve-life", "caster_id", False),
    ("/api/gm/sacred-weapon", "paladin_id", False), ("/api/gm/cutting-words", "bard_id", False),
    ("/api/gm/dark-ones-luck", "character_id", False), ("/api/gm/indomitable", "character_id", False),
    ("/api/gm/stroke-of-luck", "character_id", False), ("/api/gm/falling-damage", "character_id", False),
    ("/api/gm/dispel-magic", "caster_id", False), ("/api/gm/inspiration", "character_id", False),
    ("/api/gm/inspiration", "give_to", False), ("/api/gm/recover-ammo", "character_id", False),
    ("/api/gm/skill-check", "character_id", False), ("/api/gm/tool-check", "character_id", False),
    ("/api/gm/saving-throw", "character_id", False), ("/api/gm/contested-check", "initiator_id", False),
    ("/api/gm/contested-check", "defender_id", False), ("/api/gm/forced-movement", "target_id", False),
    ("/api/gm/opportunity-attack", "target_id", False), ("/api/gm/opportunity-attack", "attacker_id", False),
    ("/api/gm/retaliation", "character_id", False), ("/api/gm/uncanny-dodge", "character_id", False),
    ("/api/gm/conditions", "target_id", False), ("/api/gm/spell-effect", "target_ids", False),
    ("/api/gm/spell-effect", "caster_id", False), ("/api/gm/aoe-cast", "target_ids", False),
    ("/api/gm/aoe-cast", "caster_id", False), ("/api/gm/milestone", "character_ids", False),
    ("/api/gm/grant-advantage", "character_id", False), ("/api/gm/intimidating-presence", "barbarian_id", False),
    ("/api/gm/turn-undead", "caster_id", False), ("/api/gm/tactical-mind", "character_id", False),
    ("/api/gm/group-check", "character_ids", False), ("/api/gm/overrule-hide", "character_id", False),
    ("/api/gm/deflect-attacks", "character_id", False), ("/api/gm/abjure-foes", "paladin_id", False),
    ("/api/gm/starting-level", "character_id", True),  # a party set up at its level before the start (playtest I-GM-3)
    ("/api/gm/kick-character", "character_id", True),  # its success cases use a2 and run at the end (it removes the character)
]

def body(field, ids, extra=None):
    b = {field: ids if field.endswith("_ids") else ids[0]}
    b.update(extra or {})
    return b

for route, field, before in TARGET_ROUTES:
    L = f"{route} {field}"
    def send(ids, extra=None, tok=G):
        return call("POST", route, body(field, ids, extra), tok, "gm_campaign_rule")
    c, j = send([x1]);              check(f"{L}: another GM's character -> not_gm", c == 403 and j.get("error") == "not_gm", (c, j))
    c, j = send([a1], {"campaign_id": B}); check(f"{L}: campaign_id other than the character's -> not_in_campaign", c == 400 and j.get("error") == "not_in_campaign", (c, j))
    c, j = send([e1]);              check(f"{L}: ended campaign -> campaign_not_active", c == 403 and j.get("error") == "campaign_not_active", (c, j))
    c, j = send([999999]);          check(f"{L}: no such character -> character_not_found", c == 404 and j.get("error") == "character_not_found", (c, j))
    c, j = send([f1]);              check(f"{L}: character in no campaign -> character_not_in_campaign", c == 400 and j.get("error") == "character_not_in_campaign", (c, j))
    c, j = send([r1])
    if before: check(f"{L}: not yet started: allowed", j.get("error") not in CAMPAIGN_CODES, (c, j))
    else:      check(f"{L}: not yet started -> campaign_not_active", c == 403 and j.get("error") == "campaign_not_active", (c, j))
    if field.endswith("_ids"):
        c, j = send([a1, b1]);      check(f"{L}: characters in two campaigns -> targets_in_different_campaigns", c == 400 and j.get("error") == "targets_in_different_campaigns", (c, j))
    if next(f for r, f, _ in TARGET_ROUTES if r == route) != field or route.endswith("kick-character"):
        continue  # a second field of a route already sent: its success case is the first field's
    # the GM runs two active campaigns: naming the character is enough (no campaign_id_required)
    c, j = send([a1]);              check(f"{L}: two campaigns, the character names the campaign", j.get("error") not in CAMPAIGN_CODES, (c, j))
    c, j = send([a1], {"campaign_id": A}); check(f"{L}: campaign_id that is the character's taken", j.get("error") not in CAMPAIGN_CODES, (c, j))

# commands that don't act on characters: the campaign is named or the GM's only one
NO_TARGET = [("/api/gm/set-lighting", {"lighting": "dim"}, True), ("/api/gm/underwater", {"underwater": False}, True),
             ("/api/gm/shops", {"close": 999999}, True), ("/api/gm/update-narration-time", {"text_match": "zz", "timestamp": "2026-01-01T00:00:00Z"}, True),
             ("/api/gm/pass-time", {}, False), ("/api/gm/travel", {}, False)]
for route, extra, before in NO_TARGET:
    def send(camp=None, tok=G):
        b = dict(extra)
        if camp: b["campaign_id"] = camp
        return call("POST", route, b, tok, "gm_campaign_rule")
    c, j = send();  check(f"{route}: two active campaigns, none named -> campaign_id_required", c == 400 and j.get("error") == "campaign_id_required", (c, j))
    c, j = send(X); check(f"{route}: another GM's campaign -> not_gm", c == 403 and j.get("error") == "not_gm", (c, j))
    c, j = send(E); check(f"{route}: ended campaign -> campaign_not_active", c == 403 and j.get("error") == "campaign_not_active", (c, j))
    c, j = send(R)
    if before: check(f"{route}: not yet started: allowed", j.get("error") not in CAMPAIGN_CODES, (c, j))
    else:      check(f"{route}: not yet started -> campaign_not_active", c == 403 and j.get("error") == "campaign_not_active", (c, j))
    c, j = send(A); check(f"{route}: own active campaign taken", j.get("error") not in CAMPAIGN_CODES, (c, j))

# the GM status GET and the shop list read ?campaign_id the same way
c, j = call("GET", "/api/gm/status", None, G)
check("GET /gm/status: two active campaigns, none named -> campaign_id_required, listing them", c == 400 and j.get("error") == "campaign_id_required" and str(A) in j.get("message", "") and str(B) in j.get("message", ""), (c, j))
c, j = call("GET", f"/api/gm/status?campaign_id={A}", None, G)
check("GET /gm/status?campaign_id=A answers for A", c == 200 and j.get("campaign_id", A) == A, (c, {k: j.get(k) for k in ("error", "campaign_id", "campaign_name")}))
c, j = call("GET", f"/api/gm/status?campaign_id={X}", None, G)
check("GET /gm/status: another GM's campaign -> not_gm", c == 403 and j.get("error") == "not_gm", (c, j))
c, j = call("GET", "/api/gm/status", None, O)
check("GET /gm/status: the GM's only campaign", c == 200, (c, j.get("error")))
c, j = call("GET", f"/api/gm/shops?campaign_id={X}", None, G)
check("GET /gm/shops: another GM's campaign -> not_gm", c == 403 and j.get("error") == "not_gm", (c, j))
c, j = call("GET", f"/api/gm/shops?campaign_id={R}", None, G)
check("GET /gm/shops: own campaign not yet started lists its shops", c == 200 and "open_shops" in j, (c, j))
c, j = call("GET", f"/api/gm/update-character?character_id={x1}", None, G)
check("GET /gm/update-character: another GM's character -> not_gm", c == 403 and j.get("error") == "not_gm", (c, j))
c, j = call("GET", f"/api/gm/update-character?character_id={a1}", None, G)
check("GET /gm/update-character: own character", c == 200 and j.get("character_id") == a1, (c, j.get("error")))

# a deadline: its campaign is the deadline's
c, j = call("POST", "/api/gm/deadline", {"campaign_id": A, "description": "Gc test", "deadline_at": "10h"}, G)
dl = j.get("deadline_id") or (j.get("deadline") or {}).get("id")
check("a deadline is set in A", c == 200 and dl, (c, j))
if dl:
    c, j = call("POST", f"/api/gm/deadline/{dl}", {"action": "cancel"}, O)
    check("deadline/{id}: another GM -> not_gm", c == 403 and j.get("error") == "not_gm", (c, j))
    c, j = call("POST", f"/api/gm/deadline/{dl}", {"action": "cancel", "campaign_id": B}, G)
    check("deadline/{id}: campaign_id other than the deadline's -> not_in_campaign", c == 400 and j.get("error") == "not_in_campaign", (c, j))
    c, j = call("POST", f"/api/gm/deadline/{dl}", {"action": "cancel"}, G)
    check("deadline/{id}: the GM cancels it", c == 200, (c, j))

# the character conditions route: the GM of the character's campaign only, with the player's way out in the refusal
c, j = call("POST", f"/api/characters/{a1}/conditions", {"condition": "prone"}, P)
check("POST /characters/{id}/conditions by the player -> not_gm, naming the escape route", c == 403 and j.get("error") == "not_gm" and "/api/conditions/escape" in j.get("message", ""), (c, j))
c, j = call("POST", f"/api/characters/{a1}/conditions", {"condition": "prone", "campaign_id": B}, G)
check("POST /characters/{id}/conditions: campaign_id other than the character's -> not_in_campaign", c == 400 and j.get("error") == "not_in_campaign", (c, j))
c, j = call("POST", f"/api/characters/{a1}/conditions", {"condition": "prone"}, G)
check("POST /characters/{id}/conditions by the GM", c == 200 and j.get("success"), (c, j))

# the kick: the GM runs two campaigns; naming the character is enough, and it leaves A but stays its player's
c, j = call("POST", "/api/gm/kick-character", {"character_id": a2, "campaign_id": A}, G)
check("kick: two campaigns, the character names the campaign; campaign_id that is the character's taken", c == 200 and j.get("success"), (c, j))
c, j = call("GET", f"/api/characters/{a2}", None, P2)
check("kick: the character left its campaign and is still its player's", c == 200 and not (j.get("campaign_id") or j.get("lobby_id")), (c, {k: j.get(k) for k in ("campaign_id", "lobby_id", "error")}))

print(f"\n{passed} of {passed + failed} checks passed")
sys.exit(1 if failed else 0)
