# Open item 3 ("seen, not fixed") and its neighbours, Nov 15: Pack Tactics and Sneak Attack share one rule (an ally within
# 5 feet of the target: the battle map, or the attacker's ally_adjacent without one); the GM's stat block shows traits;
# Reckless Attack lasts until the Barbarian's next turn; a monster at 0 Hit Points takes no actions; death_save is refused in a
# pending turn; a GM-only search filter says when it isn't applied; error replies have an error status; spell list routes.
# Local: python3 seen_fixes_test.py
import json, random
from ah import call, register, build, BASE, only
only("local", "staging", why="it sets up fights and maps")
results = []
def check(name, ok, detail=""):
    results.append((name, bool(ok)))
    print(("PASS " if ok else "FAIL ") + name + ("" if ok else "  -> " + json.dumps(detail)[:500]), flush=True)
G = register("SfGM")["token"]
toks, chars, names = {}, {}, {}
for cls, bg, sp in (("barbarian", "soldier", "human"), ("rogue", "criminal", "halfling"), ("fighter", "soldier", "dwarf")):
    T = register("Sf" + cls)["token"]; toks[cls] = T
    cid, f = build(T, cls, bg, sp, "SF"); assert cid, f
    call("POST", "/api/characters/auto-equip", {"character_id": cid, "answer": "yes"}, T)
    chars[cls] = cid; names[cls] = call("GET", f"/api/characters/{cid}", None, T)[1]["name"]
c, camp = call("POST", "/api/campaigns", {"name": "SF %d" % random.randint(1000, 99999), "setting": "t", "max_players": 4}, G); cmp = camp["campaign_id"]
for cls, cid in chars.items(): call("POST", f"/api/campaigns/{cmp}/join", {"character_id": cid}, toks[cls])
call("POST", f"/api/campaigns/{cmp}/start", None, G)
call("POST", "/api/gm/starting-level", {"campaign_id": cmp, "character_id": chars["barbarian"], "level": 2, "gold": False}, G)  # Reckless Attack: level 2
B = f"/api/campaigns/{cmp}/combat"
call("POST", B + "/start", {}, G)
c, j = call("POST", B + "/add", {"combatants": [{"name": "Wolf A", "monster_key": "wolf", "initiative": -5, "hp": 300},
                                                {"name": "Wolf B", "monster_key": "wolf", "initiative": -6, "hp": 300}]}, G)
check("two wolves added", c == 200, j)
def mon(m, a, t, extra=None):
    ma = {"monster": m, "action": a, "target": t, "apply_damage": False}; ma.update(extra or {})
    return call("POST", "/api/gm/narrate", {"campaign_id": cmp, "narration": "x", "monster_action": ma}, G)[1]

# the GM's stat block shows the traits
c, st = call("GET", B, None, G)
wolf = next((e for e in st.get("combatants", []) if e.get("name") == "Wolf A"), {})
check("the GM's combat view shows the wolf's traits (Pack Tactics)", "Pack Tactics" in json.dumps(wolf.get("stat_block", {}).get("traits", [])), wolf.get("stat_block"))

# Pack Tactics: without a battle map only when the GM says an ally is next to the target
t1 = mon("Wolf A", "Bite", names["fighter"]).get("monster_action_result", "")
t2 = mon("Wolf A", "Bite", names["fighter"], {"ally_adjacent": True}).get("monster_action_result", "")
check("Pack Tactics without a map and without ally_adjacent: no Advantage from it", "Pack Tactics" not in t1, t1)
check("Pack Tactics with ally_adjacent: Advantage (Pack Tactics)", "Advantage" in t2 and "Pack Tactics" in t2, t2)

# Sneak Attack: without a map, the ally only counts when the player says so. Only attacks without Advantage count
# (Advantage, such as the Shortsword's Vex after a hit, allows Sneak Attack by itself).
def rogue_attack(extra):
    call("POST", "/api/gm/update-character", {"character_id": chars["rogue"], "set": {"action_used": False, "sneak_attack_used": False}}, G)
    body = {"character_id": chars["rogue"], "action": "attack", "target_id": -1, "description": "I stab the wolf"}; body.update(extra)
    return call("POST", "/api/action", body, toks["rogue"])[1].get("result", "")
call("POST", "/api/gm/update-character", {"character_id": chars["rogue"], "set": {"weapon_masteries": []}}, G)  # no Vex: plain attacks
plain_without, plain_with = [], []
for _ in range(10):
    r = rogue_attack({})
    if "Advantage" not in r and "HIT" in r: plain_without.append(r)
    r = rogue_attack({"ally_adjacent": True})
    if "Advantage" not in r and "HIT" in r: plain_with.append(r)
check("Sneak Attack on a hit without Advantage, without a map and without ally_adjacent: never given",
      plain_without and not any("Sneak Attack" in r for r in plain_without), plain_without[:2])
check("Sneak Attack on a hit without Advantage, with ally_adjacent: given", plain_with and all("Sneak Attack" in r for r in plain_with), plain_with[:2])

# Reckless Attack lasts until the start of the Barbarian's next turn, and ends with the fight
call("POST", "/api/gm/update-character", {"character_id": chars["barbarian"], "set": {"action_used": False, "conditions": []}}, G)
r = call("POST", "/api/action", {"character_id": chars["barbarian"], "action": "attack", "target_id": -2, "description": "reckless attack on the wolf"}, toks["barbarian"])[1]
conds = call("GET", f"/api/characters/{chars['barbarian']}", None, toks["barbarian"])[1].get("conditions") or []
check("after a Reckless Attack the Barbarian is marked reckless", "reckless" in json.dumps(conds), (conds, r.get("result")))
t = mon("Wolf B", "Bite", names["barbarian"]).get("monster_action_result", "")
check("while reckless, a wolf's attack has Advantage (the target attacked recklessly)", "recklessly" in t, t)
call("POST", B + "/end", {}, G)
conds = call("GET", f"/api/characters/{chars['barbarian']}", None, toks["barbarian"])[1].get("conditions") or []
check("the fight's end clears Reckless Attack and Dodge", "reckless" not in json.dumps(conds) and "dodging" not in json.dumps(conds), conds)

# a monster at 0 Hit Points takes no actions
call("POST", B + "/start", {}, G)
call("POST", B + "/add", {"combatants": [{"name": "Wolf C", "monster_key": "wolf", "initiative": -5, "hp": 1}]}, G)
c, st = call("GET", B, None, G); wc = next((e for e in st.get("combatants", []) if e.get("name") == "Wolf C"), {})
c, adj = call("POST", "/api/gm/adjust-monster", {"campaign_id": cmp, "target_id": wc.get("id"), "hp": 0}, G)
j = mon("Wolf C", "Bite", names["fighter"])
check("a monster at 0 Hit Points: 'takes no actions', not 'no action of that name'", "takes no actions" in json.dumps(j), (adj, j))

# death_save is refused in a pending turn (the server rolls it at a dying character's slot)
c, j = call("POST", "/api/pending-turn", {"character_id": chars["fighter"], "campaign_id": cmp, "steps": [{"action": "death_save"}]}, toks["fighter"])
check("death_save in a pending turn -> refused, saying the server rolls it", c == 400 and "rolls your Death Saving Throw" in json.dumps(j), (c, j))

# a GM-only filter says when it isn't applied; errors have an error status; spell list routes
c, j = call("GET", "/api/universe/monsters/search?cr=1/4", None, toks["fighter"])
check("a player's ?cr= is not applied and the reply says why", c == 200 and "cr was not applied" in j.get("note", ""), j)
c, j = call("GET", "/api/universe/monsters/search?cr=1/4", None, G)
check("a GM's ?cr=1/4 filters", c == 200 and j.get("monsters") and all(m.get("cr") == "1/4" for m in j["monsters"]), j)
c, j = call("GET", "/api/universe/class-spells/fighter")
check("a class with no spell list -> 404 (it was 200 with an error)", c == 404, (c, j))
c, j = call("GET", "/api/universe/spells?class=warlock&level=0")
check("the spell list filters by class and level (the Fighting Style refusal names a route that works)", c == 200 and "eldritch-blast" in j.get("spells", []) and "fire-bolt" not in j.get("spells", []), j)

print(f"\n{sum(ok for _, ok in results)} of {len(results)} checks passed")
import sys
sys.exit(0 if all(ok for _, ok in results) else 1)  # a failed check fails the run (audit AUD-48)
