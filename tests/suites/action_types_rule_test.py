# HANDOFF items 19 and 29: one definition of the action types a player can send. "other" is declare (Matt, Nov 16): the GM rules
# on it, it takes the action in a fight, and a declaration that casts a spell or uses a feature is refused.
# The server has one table of action types (cmd/server/action_types_2024.go); the request's check, the costs, my-turn's options, the
# refusal's list, /commands, the API reference and skill.md all read it. A word that is no action type is refused
# with 400 unknown_action and the actions the character can send now; nothing is spent. Something the server doesn't resolve is sent
# as the SRD action it is with a description (Utilize, Influence, speak) and the GM rules on it.
#   - an unknown word refused, with options, nothing spent: a direct action (out of and in a fight), a pending turn's step, a Ready, a
#     companion's action; an unknown word likewise; a spell's name points to cast; a feature's own request points to its route
#   - every type a level 1 character of each class has (the refusal's list) passes the check (probed with a target in another
#     campaign: the check comes first, then the campaign rule refuses with 404, so nothing happens)
#   - speak and interact are free, need a description and are logged as the character's words; speaking doesn't use the
#     exploration beat
#   - a level 2 Cleric has divine_spark and turn_undead (my-turn and the refusal's list); a level 1 Cleric doesn't
#   - /commands and /docs/swagger.json list the same types, divine_spark, speak, interact and declare among them
#   - Utilize and Magic are use_item and cast
# Requests only: local, or staging (ARPG_TARGET=staging python3 action_types_rule_test.py).
import json, random, re
from ah import call, register, build, only

only("local", "staging", why="it only sends requests")
passed = failed = 0
fails = []
def check(name, ok, detail=None):
    global passed, failed
    if ok:
        passed += 1; print("PASS", name)
    else:
        failed += 1; fails.append(name); print("FAIL", name, "|", json.dumps(detail)[:500])

def campaign(tok, name):
    c, j = call("POST", "/api/campaigns", {"name": f"{name} {random.randint(1000, 99999)}", "setting": "A test of action types.", "max_players": 6}, tok)
    return j["campaign_id"]

def join_start(camp, gm, members):
    for ch, tok in members:
        c, j = call("POST", f"/api/campaigns/{camp}/join", {"character_id": ch}, tok)
        assert c == 200, (c, j)
    c, j = call("POST", f"/api/campaigns/{camp}/start", {}, gm)
    assert c == 200, (c, j)

def act(tok, cid, body):
    return call("POST", "/api/action", dict(body, character_id=cid), tok, label="action types")

def names(opts):
    return [o.get("action") for o in (opts or [])]

def options_ok(j):
    opts = j.get("actions") or []
    return bool(opts) and all(isinstance(o.get("example"), dict) and o["example"] and o.get("send") and o.get("costs") for o in opts)

def feed(camp, tok):
    c, j = call("GET", f"/api/campaigns/{camp}/feed", None, tok)
    return j.get("actions") or j.get("feed") or j.get("entries") or []

# ---------------------------------------------------------------- a campaign: a level 2 Cleric and a Fighter; a victim elsewhere
GM, PC, PF, PV, GV = (register(n)["token"] for n in ("AtGm", "AtCleric", "AtFighter", "AtVictim", "AtGm2"))
cleric, _ = build(PC, "cleric", "acolyte", "dwarf", "AtCle")
fighter, _ = build(PF, "fighter", "soldier", "human", "AtFtr")
victim, _ = build(PV, "fighter", "soldier", "human", "AtVic")
camp = campaign(GM, "Action types")
join_start(camp, GM, [(cleric, PC), (fighter, PF)])
other_camp = campaign(GV, "Action types elsewhere")
join_start(other_camp, GV, [(victim, PV)])

# ---------------------------------------------------------------- level 1: no Channel Divinity yet
c, j = act(PC, cleric, {"action": "other", "description": "I cast Sacred Flame at the cultist"})
check(f"'other' (declare) that casts a spell is refused, pointing to cast ({c} {j.get('error')})", c == 400 and j.get("error") == "declaration_has_its_own_action" and '"cast"' in j.get("message", ""), j)
c, j = act(PC, cleric, {"action": "frobnicate", "description": "I cast Sacred Flame at the cultist"})
check(f"a word that is no action is refused out of a fight: 400 unknown_action ({c} {j.get('error')})", c == 400 and j.get("error") == "unknown_action", j)
check("the refusal lists the actions the character can send, each with an example request", options_ok(j) and "attack" in names(j.get("actions")) and "cast" in names(j.get("actions")), names(j.get("actions")))
check("the refusal says how to send what the server doesn't resolve (Utilize, Influence, speak)", all(w in j.get("message", "") for w in ("use_item", "influence", "speak")), j.get("message"))
check("a level 1 Cleric has no Divine Spark or Turn Undead yet", "divine_spark" not in names(j.get("actions")) and "turn_undead" not in names(j.get("actions")), names(j.get("actions")))
check("nothing was recorded for the refused requests", not any("Sacred Flame at the cultist" in json.dumps(e) for e in feed(camp, GM)), None)
c2, j2 = act(PC, cleric, {"action": "search", "description": "I look for tracks"})
check(f"after the refusal the exploration beat is still unused (search answers {c2})", c2 == 200, j2)

c, j = call("POST", "/api/gm/update-character", {"character_id": cleric, "level": 2}, GM)
check("the GM sets the Cleric to level 2", c == 200 and not j.get("error"), j)
call("POST", "/api/gm/narrate", {"campaign_id": camp, "narration": "The cultists turn toward you."}, GM)

c, j = act(PC, cleric, {"action": "frobnicate", "description": "Channel Divinity heal"})
opts = names(j.get("actions"))
check("a level 2 Cleric's refusal lists divine_spark and turn_undead", c == 400 and "divine_spark" in opts and "turn_undead" in opts, opts)
spark = next((o for o in j.get("actions", []) if o["action"] == "divine_spark"), {})
check("divine_spark is listed as an Action with its request", spark.get("costs") == "Action" and spark.get("send") == "POST /api/action" and spark.get("example", {}).get("action") == "divine_spark", spark)
c, mt = call("GET", f"/api/my-turn?character_id={cleric}", None, PC)
mine = (mt.get("your_options") or {}).get("actions") or []
check("my-turn lists Divine Spark and Turn Undead with the request to send", any(o.get("action") == "divine_spark" and "divine_spark" in o.get("use", "") for o in mine)
      and any(o.get("action") == "turn_undead" for o in mine), [o.get("action") for o in mine])
free = (mt.get("your_options") or {}).get("free_actions") or []
check("my-turn lists speak and interact among the things that take no action", {"speak", "interact"} <= {o.get("action") for o in free}, [o.get("action") for o in free])

for word in ("frobnicate", "Nonsense "):
    c, j = act(PC, cleric, {"action": word, "description": "x"})
    check(f"{word!r} is refused with unknown_action and the options ({c})", c == 400 and j.get("error") == "unknown_action" and options_ok(j), j.get("error"))
c, j = act(PC, cleric, {"action": "fireball"})
check(f"a spell's name as the action points to cast ({j.get('error')})", c == 400 and j.get("error") == "spell_as_action" and "spell_slug" in j.get("message", ""), j)
c, j = act(PC, cleric, {"action": "wholeness_of_body"})
check(f"a feature with its own request is pointed to it ({j.get('error')})", c == 400 and j.get("error") == "use_its_request" and "/api/characters/wholeness-of-body" in j.get("message", ""), j)
c, j = act(PC, cleric, {"action": "map_move"})
check(f"a pending turn's step sent alone is pointed to the battle map ({j.get('error')})", c == 400 and j.get("error") == "pending_turn_step", j)
c, j = act(PC, cleric, {"action": "frobnicate", "companion_id": 999999})
check(f"a companion's unknown word is refused as unknown_action ({j.get('error')})", c == 400 and j.get("error") == "unknown_action", j)
c, j = act(PC, cleric, {"action": "speak", "companion_id": 999999, "description": "hi"})
check(f"a companion can't be sent speak ({j.get('error')})", c == 400 and j.get("error") == "invalid_companion_action" and "attack" in j.get("message", ""), j)
c, j = act(PC, cleric, {"action": "breath", "target_ids": [-1]})
check(f"a companion's own action without companion_id is refused ({j.get('error')})", c == 400 and j.get("error") == "companion_id_required", j)

# ---------------------------------------------------------------- Ready must hold a real action
for desc in ("trigger: the door opens; action: other", "when the door opens I do something clever", ""):
    c, j = act(PC, cleric, {"action": "ready", "description": desc})
    check(f"Ready holding no action ({desc!r}) is refused: unknown_action with what it can hold ({c})", c == 400 and j.get("error") == "unknown_action" and "attack" in names(j.get("actions")) and "ready" not in names(j.get("actions")), j)

# ---------------------------------------------------------------- talking and one object: free, logged, need a description
c, j = act(PC, cleric, {"action": "speak"})
check(f"speak without a description is refused ({j.get('error')})", c == 400 and j.get("error") == "description_required", j)
line = f"Stay behind me, Aldren! {random.randint(1000, 9999)}"
c, j = act(PC, cleric, {"action": "speak", "description": line})
check(f"speak is accepted out of a fight ({c})", c == 200 and j.get("action") == "speak", j)
logged = [e for e in feed(camp, GM) if line in json.dumps(e)]
check("the words are logged as the character's (type speak)", any((e.get("action_type") or e.get("type")) == "speak" for e in logged), logged[:1])
c, j = act(PC, cleric, {"action": "influence", "description": "I urge the cultists to surrender"})
check(f"speaking didn't use the exploration beat: an Influence follows it ({c} {j.get('error')})", c == 200, j)
c, j = act(PF, fighter, {"action": "Utilize", "description": "I pull the rusted lever"})
check(f"Utilize is use_item, described for the GM ({c} {j.get('action')})", c == 200 and j.get("action") == "use_item", j)
call("POST", "/api/gm/narrate", {"campaign_id": camp, "narration": "The lever groans; a fight breaks out."}, GM)

# ---------------------------------------------------------------- in a fight: nothing spent on a refusal; speak and interact free
B = f"/api/campaigns/{camp}/combat"
call("POST", B + "/start", {}, GM)
c, j = call("POST", B + "/add", {"combatants": [{"name": "Cultist A", "monster_key": "cultist", "initiative": -5, "hp": 300}]}, GM)
check("a cultist joins the fight", c == 200, j)
c, st = call("GET", B, None, GM)
order = st.get("combatants") or st.get("turn_order") or []
cur = (order[st.get("current_turn_index", 0)] if order else {}).get("id")
for _ in range(4):
    if cur in (cleric, fighter):
        break
    call("POST", B + "/next", {}, GM)
    c, st = call("GET", B, None, GM)
    order = st.get("combatants") or st.get("turn_order") or []
    cur = (order[st.get("current_turn_index", 0)] if order else {}).get("id")
me, tok = (cleric, PC) if cur == cleric else (fighter, PF)
check("it is a character's turn", cur in (cleric, fighter), st)
c, j = act(tok, me, {"action": "frobnicate", "description": "I do something"})
check(f"a word that is no action is refused in a fight ({c} {j.get('error')})", c == 400 and j.get("error") == "unknown_action" and options_ok(j), j)
line = f"For the dawn! {random.randint(1000, 9999)}"
c, j = act(tok, me, {"action": "speak", "description": line})
check(f"speak in a fight is free ({j.get('resource_consumed')})", c == 200 and j.get("resource_consumed") == "free", j)
c, j = act(tok, me, {"action": "interact", "description": "I kick the fallen torch toward the cultist"})
check(f"interact in a fight is free ({j.get('resource_consumed')})", c == 200 and j.get("resource_consumed") == "free", j)
c, j = call("POST", "/api/pending-turn", {"steps": [{"action": "frobnicate", "description": "x"}], "character_id": me, "campaign_id": camp}, tok)
check(f"a pending turn with an unknown step is refused, with the options ({c} {j.get('error')})", c == 400 and j.get("error") == "invalid_pending_turn"
      and any("unknown_action" in p for p in j.get("problems", [])) and "attack" in names(j.get("actions")), j)
c, j = call("POST", "/api/pending-turn", {"steps": [{"action": "ready", "description": "trigger: x; action: other"}], "character_id": me, "campaign_id": camp}, tok)
check(f"a pending turn whose Ready holds no action is refused ({c})", c == 400 and any("unknown_action" in p for p in j.get("problems", [])), j)
c, j = act(tok, me, {"action": "ready", "description": "trigger: x; action: other"})
check(f"a Ready holding 'other' is refused in a fight ({j.get('error')})", c == 400 and j.get("error") == "unknown_action", j)
c, j = act(tok, me, {"action": "dodge"})
check(f"after every refusal and the free actions, the action is still there: Dodge works ({c} {j.get('resource_consumed')})", c == 200 and j.get("resource_consumed") == "action", j)
c, j = act(tok, me, {"action": "dash"})
check(f"and now the action is used ({c} {j.get('error')})", c == 409 and j.get("error") == "resource_exhausted", j)
c, j = act(tok, me, {"action": "other", "description": "I shove the table over between us"})
check(f"a declaration ('other') in a fight takes the action: with it used, it is refused, never free ({c} {j.get('error')})", c == 409 and j.get("error") == "resource_exhausted", j)
words = [e for e in feed(camp, GM) if line in json.dumps(e)]
check("the words said in the fight are in the log", bool(words), None)
call("POST", B + "/end", {"award_xp": False}, GM)

# ---------------------------------------------------------------- every class at level 1: each type it has passes the check
CLASSES = [("barbarian", "soldier", "human"), ("bard", "entertainer", "human"), ("cleric", "acolyte", "dwarf"), ("druid", "sage", "elf"),
           ("fighter", "soldier", "human"), ("monk", "hermit", "human"), ("paladin", "noble", "human"), ("ranger", "outlander", "elf"),
           ("rogue", "criminal", "halfling"), ("sorcerer", "sage", "human"), ("warlock", "charlatan", "tiefling"), ("wizard", "sage", "elf")]
EXPECT = {"barbarian": "rage", "bard": "bardic_inspiration", "fighter": "second_wind", "monk": "martial_arts", "paladin": "lay_on_hands", "sorcerer": "innate_sorcery"}
NOT_YET = {"cleric": "divine_spark", "druid": "wild_shape", "rogue": "cunning_action", "fighter": "action_surge", "warlock": "magical_cunning"}
GC = register("AtGmAll")["token"]
members = []  # 12 classes in two campaigns: a campaign has at most 6 seats (the turn schedule)
for cls, bg, sp in CLASSES:
    tok = register("At" + cls[:5].title())["token"]
    cid, _ = build(tok, cls, bg, sp, "AtAll")
    members.append((cid, tok, cls))
for half in (members[:6], members[6:]):
    join_start(campaign(GC, "Every class"), GC, [(cid, tok) for cid, tok, _ in half])
for cid, tok, cls in members:
    c, j = act(tok, cid, {"action": "frobnicate", "description": "x"})
    opts = j.get("actions") or []
    have = names(opts)
    check(f"{cls} 1: an unknown word refused with {len(have)} options", c == 400 and j.get("error") == "unknown_action" and len(have) >= 15, j.get("error"))
    if cls in EXPECT:
        check(f"{cls} 1: its feature {EXPECT[cls]} is listed", EXPECT[cls] in have, have)
    if cls in NOT_YET:
        check(f"{cls} 1: {NOT_YET[cls]} (a later level) is not listed", NOT_YET[cls] not in have, have)
    refused = []
    for o in opts:
        if not o.get("send", "").startswith("POST /api/action"):
            continue  # a feature's own request
        body = dict(o["example"])
        body.pop("companion_id", None)
        # a character of another campaign as the target: the type check comes first, then the campaign rule refuses (404)
        body.update({"target_character_id": victim})
        c2, j2 = act(tok, cid, body)
        if not (c2 == 404 and j2.get("error") == "character_not_found"):
            refused.append((o["action"], c2, j2.get("error")))
    check(f"{cls} 1: every listed type passes the type check ({len(opts)} probed, nothing done)", not refused, refused)
c, j = act(members[0][1], members[0][0], {"action": "Magic", "spell_slug": "fire-bolt", "target_character_id": victim})
check(f"Magic is cast (passes the check like cast: {c} {j.get('error')})", c == 404 and j.get("error") == "character_not_found", j)

# ---------------------------------------------------------------- the docs list the table's types
c, ref = call("GET", "/docs/swagger.json")
prop = (((ref["paths"]["/api/action"]["post"].get("requestBody") or {}).get("content") or {}).get("application/json") or {}).get("schema", {}).get("properties", {}).get("action", {})
enum = set(prop.get("enum") or [])
# the reference's enum is every word the request reads, the aliases too ("Also read under these names"): "other" and "free" are
# declare's aliases (Matt, Nov 16), so they are read as declare and never listed as an action of their own
check("the API reference's action types include divine_spark, turn_undead, speak, interact, utilize, magic and declare",
      {"divine_spark", "turn_undead", "speak", "interact", "utilize", "magic", "declare"} <= enum, sorted(enum))
import urllib.request
from ah import BASE, _ctx
html = urllib.request.urlopen(BASE + "/commands", context=_ctx, timeout=60).read().decode()
listed = set(re.findall(r'data-action="([^"]+)"', html))
aliases = {"magic", "utilize", "drink", "drink_potion", "quaff", "frenzy_attack", "breath_weapon", "other", "free"}
check("/commands lists every action type the reference does (and the routes and steps besides), never other",
      (enum - aliases) <= listed and "other" not in listed and {"divine_spark", "speak", "interact", "declare"} <= listed, sorted((enum - aliases) - listed))
c, md = 0, urllib.request.urlopen(BASE + "/skill.md", context=_ctx, timeout=60).read().decode()
check("skill.md lists divine_spark, speak and interact with examples", all(f'{{"action": "{w}"' in md for w in ("divine_spark", "speak", "interact")), None)

print(f"\n{passed} passed, {failed} failed")
print("FAILS:", fails)
raise SystemExit(1 if failed else 0)
