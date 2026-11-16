# Audit Phase 1.4 (CODE_AUDIT_REPORT.md): who owns what, and where items come from.
#   E-3   a bag a player adds is a real container kind (name and capacity from containerKinds), labelled at most, and no shop pays
#         for it: no item comes from nothing.
#   E-11, D-3  one table says which character columns an editor may write (editorColumns): the GM can't write the soft-delete
#         record, who owns the character, its class levels or subclasses; and a level is taken only in the character's own class.
#   A-8   recreate-character only gives back a character to a player whose character in this campaign was deleted.
#   A-10  every read of a private campaign is private, not a list of subroutes: walked over every GET /api/campaigns/{id}/...
#         route of the server's own API reference.
# Local:   python3 ownership_rule_test.py      Staging: ARPG_TARGET=staging python3 ownership_rule_test.py
import json, random, sys
from ah import call, register, build, BASE, only

only("local", "staging", why="it makes campaigns, characters and refusals on many routes")
passed = failed = 0
fails = []
def check(name, ok, detail=None):
    global passed, failed
    if ok:
        passed += 1; print("PASS", name)
    else:
        failed += 1; fails.append(name); print("FAIL", name, "|", json.dumps(detail)[:400])

def campaign(tok, name):
    c, j = call("POST", "/api/campaigns", {"name": f"{name} {random.randint(1000, 9999)}", "setting": "t", "max_players": 6}, tok)
    return j["campaign_id"]

G = register("OwGm")["token"]
rP, rP2 = register("OwPl"), register("OwPl2")
P, P2, pid, p2 = rP["token"], rP2["token"], rP["agent_id"], rP2["agent_id"]
C = campaign(G, "OwC")
pc, _ = build(P, "fighter", "soldier", "human", "OwPc")
c, j = call("POST", f"/api/campaigns/{C}/join", {"character_id": pc}, P)
assert c == 200, j

# ---------------------------------------------------------------- E-3: bags
c, j = call("POST", f"/api/characters/{pc}/equipment/containers", {"kind": "sack", "name": "Bag of Holding", "location": "worn"}, P)
check(f"a bag named after a magic item is a Sack with that label ({c} {j.get('name')}/{j.get('label')})", c == 200 and j.get("name") == "Sack" and j.get("label") == "Bag of Holding", j)
bag = j.get("container_id")
c, j = call("POST", f"/api/characters/{pc}/equipment/containers", {"name": "Longsword +3"}, P)
check(f"a bag without a kind is refused ({c} {j.get('error')})", c == 400 and j.get("error") == "kind_required", j)
c, j = call("POST", f"/api/characters/{pc}/equipment/containers", {"kind": "chest", "capacity_lbs": 9999}, P)
check(f"a bag can't set its own capacity ({c} {j.get('error')})", c == 400 and j.get("error") == "unknown_field", j)
c, j = call("GET", f"/api/characters/{pc}/equipment", None, P)
names = json.dumps(j)
check("no item named Bag of Holding was made", '"name": "Bag of Holding"' not in names, None)
c, j = call("POST", f"/api/campaigns/{C}/start", {}, G)
c, j = call("POST", "/api/gm/shops", {"campaign_id": C, "open": "general_store", "name": "Ow Store"}, G)
shop = j.get("shop_id")
c, j = call("POST", f"/api/campaigns/{C}/shops/{shop}", {"character_id": pc, "sell": [{"item_id": bag, "quantity": 1}]}, P)
check(f"a shop doesn't pay for a bag the player added ({c} {j.get('error')})", c == 400 and j.get("error") == "cannot_sell", j)

# ---------------------------------------------------------------- E-11, D-3: the editor's columns, one class
for col, val in (("class_levels", {"fighter": 3, "wizard": 1}), ("subclasses", {"wizard": "evoker"}), ("deleted_at", "2026-01-01T00:00:00Z"),
                 ("deleted_owner", 1), ("agent_id", 1), ("class", "wizard")):
    c, j = call("POST", "/api/gm/update-character", {"character_id": pc, "set": {col: val}}, G)
    check(f"the GM can't set {col} ({c} {j.get('error')})", 400 <= c < 500, j)
c, j = call("POST", "/api/gm/update-character", {"character_id": pc, "set": {"gold": 50}}, G)
check(f"the GM can still set a game value such as gold ({c})", c == 200, j)
c, j = call("POST", "/api/gm/award-xp", {"character_ids": [pc], "xp": 300}, G)
c, j = call("POST", f"/api/characters/{pc}/level-up", {"class": "wizard"}, P)
check(f"a level in another class is refused ({c} {j.get('error')})", c == 400 and "multiclass" in (j.get("error") or ""), j)

# ---------------------------------------------------------------- A-8: recreate-character
R = campaign(G, "OwR")   # not started: a player may delete their own character here
gone, _ = build(P2, "rogue", "criminal", "halfling", "OwGone")
c, j = call("POST", f"/api/campaigns/{R}/join", {"character_id": gone}, P2)
c, j = call("POST", "/api/gm/recreate-character", {"campaign_id": R, "agent_id": p2, "name": f"Ow Back {random.randint(1000,9999)}", "class": "rogue"}, G)
check(f"recreate for a player whose character wasn't deleted is refused ({c} {j.get('error')})", c == 400 and j.get("error") == "nothing_to_recreate", j)
c, j = call("POST", "/api/gm/recreate-character", {"campaign_id": R, "agent_id": pid, "name": f"Ow X {random.randint(1000,9999)}", "class": "fighter"}, G)
check(f"recreate for an agent who never played here is refused ({c} {j.get('error')})", c == 400 and j.get("error") == "nothing_to_recreate", j)
c, sheet = call("GET", f"/api/characters/{gone}", None, P2)
c, j = call("DELETE", f"/api/characters/{gone}", {"confirm": sheet.get("name")}, P2)
check(f"the player deletes their character in the recruiting campaign ({c})", c == 200, j)
c, j = call("POST", "/api/gm/recreate-character", {"campaign_id": R, "agent_id": p2, "name": f"Ow Back {random.randint(1000,9999)}", "class": "wizardry"}, G)
check(f"recreate needs an SRD class ({c} {j.get('error')})", c == 400 and j.get("error") == "name_and_class_required", j)
c, j = call("POST", "/api/gm/recreate-character", {"campaign_id": R, "agent_id": p2, "name": f"Ow Back {random.randint(1000,9999)}", "class": "rogue"}, G)
check(f"recreate gives the deleted player a character back ({c})", c == 200 and j.get("character_id"), j)
c, j = call("GET", "/api/my-turn", None, P2)
check(f"that player's my-turn still answers ({c})", c < 500, j)

# ---------------------------------------------------------------- A-10: every read of a private campaign
c, j = call("POST", f"/api/campaigns/{C}/table-rules", {"visibility": "private", "watch_password": "ow-secret-pw"}, G)
check(f"the GM makes the campaign private ({c})", c == 200, j)
c, ref = call("GET", "/docs/swagger.json")
gets = sorted(p for p, ops in ref.get("paths", {}).items() if p.startswith("/api/campaigns/{id}") and "get" in ops and p.count("{") == 1)
check(f"the reference lists campaign read routes ({len(gets)})", len(gets) >= 10, gets)
leaks = []
for p in gets:
    path = p.replace("{id}", str(C))
    c, j = call("GET", path)                      # nobody signed in
    if c < 400 or (c in (401, 403) and j.get("error") != "private_campaign"):
        leaks.append((p, c, j.get("error")))
    c2, j2 = call("GET", path, None, P)           # a player of the campaign
    if j2.get("error") == "private_campaign":
        leaks.append((p, "player gated", c2))
check("every read of the private campaign asks a stranger for the watch password, and none asks its player", not leaks, leaks)

print(f"\n{passed} of {passed + failed} checks passed")
if fails:
    print("FAILED:", fails)
    sys.exit(1)
