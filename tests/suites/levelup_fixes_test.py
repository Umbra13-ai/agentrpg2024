# Local test of the level-up fixes (Nov 14, evening): every item on the setup checker's level-up list.
# Run against a local server on :8099 (ah.py). Prints PASS/FAIL per check and a total.
import json, random, sys
from ah import call, register, build

results = []
def check(name, ok, detail=""):
    results.append((name, bool(ok)))
    print(("PASS " if ok else "FAIL ") + name + ("" if ok else "  -> " + str(detail)[:600]))

import os
G = register("LvlFixGM")["token"]
T = register("LvlFixP")["token"]
# for cleanup afterwards: the ids, and the two tokens in a file outside the repository (never printed)
KEEP = os.environ.get("ARPG_SECRETS")
CREATED = {"chars": [], "campaigns": []}
def keep():
    if KEEP:
        os.makedirs(KEEP, exist_ok=True)
        p = KEEP + "/fixes_run.json"
        json.dump({"gm": G, "player": T, **CREATED}, open(p, "w")); os.chmod(p, 0o600)

def campaign():
    c, camp = call("POST", "/api/campaigns", {"name": "Level fixes %d" % random.randint(1000, 99999), "setting": "test", "max_players": 6}, G)
    CREATED["campaigns"].append(camp["campaign_id"]); keep()
    return camp["campaign_id"]

CMP = [campaign() for _ in range(10)]
nextc = iter(CMP)

def char_at(cls, bg, sp, level, extra=None):
    cid, f = build(T, cls, bg, sp, "LF", extra)
    assert cid, f
    CREATED["chars"].append({"id": cid, "name": f.get("name") or (f.get("character") or {}).get("name")}); keep()
    cmp = next(nextc)
    call("POST", f"/api/campaigns/{cmp}/join", {"character_id": cid}, T)
    call("POST", f"/api/campaigns/{cmp}/start", None, G)
    if level > 1:
        c, j = call("POST", "/api/gm/starting-level", {"campaign_id": cmp, "character_id": cid, "level": level, "gold": False}, G)
        assert c == 200, j
    return cid, cmp

def rec(cid):
    return call("GET", f"/api/characters/{cid}", None, T)[1]

def setup(cid):
    return call("GET", f"/api/characters/{cid}/setup?view=full", None, T)[1]

def step(cid, key):
    for s in setup(cid).get("steps") or []:
        if s.get("key") == key:
            return s
    return None

# 1. unknown fields refused (ASI, feat, level-up, subclass, metamagic, mystic arcanum, subclass-choice)
fid, fcmp = char_at("fighter", "soldier", "human", 4)
c, j = call("POST", f"/api/characters/{fid}/asi", {"abilty": "str", "points": 2}, T)
check("ASI: misspelled field refused (400 unknown_field, did you mean)", c == 400 and j.get("error") == "unknown_field" and "ability" in j.get("message", ""), (c, j))
c, j = call("POST", f"/api/characters/{fid}/feat", {"feat": "alert", "abilty_choice": "dex"}, T)
check("feat: misspelled field refused", c == 400 and j.get("error") == "unknown_field", (c, j))
c, j = call("POST", f"/api/characters/{fid}/level-up", {"clas": "fighter"}, T)
check("level-up: misspelled field refused", c == 400 and j.get("error") == "unknown_field", (c, j))
c, j = call("POST", f"/api/characters/{fid}/level-up", None, T)
check("level-up: an empty body is still read (not unknown_field/invalid_json)", j.get("error") not in ("unknown_field", "invalid_json"), (c, j))
r1 = rec(fid)
# 9. feat refusal list includes the Origin feats the request accepts
c, j = call("POST", f"/api/characters/{fid}/feat", {"feat": "not a feat"}, T)
af = j.get("available_feats") or []
check("feat refusal lists Origin feats (e.g. alert or lucky)", c == 400 and ("lucky" in af or "alert" in af or "magic_initiate" in af), (c, af[:40]))
check("feat refusal leaves out ability_score_improvement", "ability_score_improvement" not in af, af)
c, j = call("POST", f"/api/characters/{fid}/feat", {"feat": "Grappler", "ability_choice": "str"}, T)
check("feat: the displayed name 'Grappler' is accepted", c == 200, (c, j))

# 2/3. Wizard: Scholar skills; Evocation Savant spells must be Evocation
wid, wcmp = char_at("wizard", "sage", "elf", 3)
c, j = call("POST", "/api/characters/subclass", {"character_id": wid, "subclass": "evocation", "bonus": 1}, T)
check("subclass: misspelled field refused, subclass not saved", c == 400 and j.get("error") == "unknown_field" and not rec(wid).get("subclass"), (c, j))
c, j = call("POST", "/api/characters/subclass", {"character_id": wid, "subclass": "evocation"}, T)
check("wizard subclass evocation", c == 200, (c, j))
c, j = call("POST", f"/api/characters/{wid}/expertise", {"skills": ["perception"]}, T)
check("Scholar: a skill outside its six refused", c == 400 and j.get("error") in ("skill_not_allowed", "not_proficient"), (c, j))
held = [s.strip() for s in (rec(wid).get("skill_proficiencies") or "").split(",")] if isinstance(rec(wid).get("skill_proficiencies"), str) else rec(wid).get("skill_proficiencies") or []
nonsch = [s for s in held if s and s not in ("arcana", "history", "investigation", "medicine", "nature", "religion")]
if nonsch:
    c, j = call("POST", f"/api/characters/{wid}/expertise", {"skills": [nonsch[0]]}, T)
    check(f"Scholar: proficient but non-Scholar skill ({nonsch[0]}) refused", c == 400 and j.get("error") == "skill_not_allowed", (c, j))
else:
    print("NOTE  wizard holds no non-Scholar skill; that check was skipped")
es = step(wid, "expertise")
if es:
    opts = [o["skill"] for o in es["choices"]["options_your_skill_proficiencies"]]
    check("Scholar setup options are only the six skills", all(o in ("arcana", "history", "investigation", "medicine", "nature", "religion") for o in opts) and opts, opts)
    c, j = call(es["request"]["method"], es["request"]["path"], es["request"]["body"], T)
    check("Scholar: the setup suggestion is accepted", c == 200, (c, j))
sp = step(wid, "spells")
book = rec(wid).get("known_spells") or []
book = [b if isinstance(b, str) else b.get("slug") for b in book]
if sp:
    body = sp["request"]["body"]
    add = body.get("add") or []
    import urllib.request
    c, allsp = call("GET", "/api/universe/class-spells/wizard", None, T)
    print("INFO  suggested spellbook additions:", add)
    c, j = call(sp["request"]["method"], sp["request"]["path"], body, T)
    check("wizard: setup spellbook suggestion accepted (Evocation picks for Savant)", c == 200, (c, j))
# now the book is full: a non-Evocation spell beyond the free ones must be refused, as before; test on a fresh evoker instead
wid2, _ = char_at("wizard", "sage", "elf", 3)
call("POST", "/api/characters/subclass", {"character_id": wid2, "subclass": "evocation"}, T)
known = [k if isinstance(k, str) else k.get("slug") for k in (rec(wid2).get("known_spells") or [])]
c, lst = call("GET", "/api/universe/class-spells/wizard", None, T)
spells = lst.get("spells") or lst.get("class_spells") or []
def slug(x): return x if isinstance(x, str) else (x.get("slug") or x.get("spell"))
def lvl(x): return 0 if isinstance(x, str) else x.get("level", 0)
def school(x): return "" if isinstance(x, str) else (x.get("school") or "")
non_evo = [slug(x) for x in spells if 1 <= lvl(x) <= 2 and school(x).lower() != "evocation" and slug(x) not in known]
evo = [slug(x) for x in spells if 1 <= lvl(x) <= 2 and school(x).lower() == "evocation" and slug(x) not in known]
lv1 = [k for k in known if k]
print("INFO  evoker book before:", len(lv1), "spells")
# fill the base free spells (6 + 2*2 = 10) with non-Evocation, then try two more non-Evocation (Savant's) -> refused
c, j = call("GET", f"/api/characters/{wid2}/spells", None, T)
base_needed = 10
cur_free = len([k for k in known if k])  # approximate (cantrips included); the server decides
tries = []
for s in non_evo:
    c, j = call("PUT", f"/api/characters/{wid2}/spells", {"add": [s]}, T)
    tries.append((s, c, j.get("error")))
    if c != 200:
        break
last = tries[-1] if tries else None
check("Evocation Savant: a non-Evocation spell past the Spellbook's free spells refused (savant_spells_are_evocation)", last and last[2] == "savant_spells_are_evocation", tries[-3:])
c, j = call("PUT", f"/api/characters/{wid2}/spells", {"add": [evo[0]]}, T)
check(f"Evocation Savant: an Evocation spell ({evo[0]}) is still free", c == 200, (c, j))

# 4. level-up notes: no %!d(MISSING), no POST .../spells
rid, rcmp = char_at("ranger", "criminal", "human", 3)
r = rec(rid)
blob = json.dumps(r) + json.dumps(call("GET", f"/api/characters/{rid}/level-up-notices", None, T)[1]) + json.dumps(setup(rid))
check("ranger notes: no %!d(MISSING)", "%!d" not in blob, blob[blob.find("%!d") - 200: blob.find("%!d") + 50] if "%!d" in blob else "")
check("ranger notes: no 'POST /api/characters/N/spells'", f"POST /api/characters/{rid}/spells" not in blob)
# 6. Hunter's Prey by displayed names
c, j = call("POST", "/api/characters/subclass", {"character_id": rid, "subclass": "hunter"}, T)
c, j = call("POST", "/api/characters/subclass-choice", {"character_id": rid, "feature": "Hunter's Prey", "choice": "Colossus Slayer"}, T)
check("Hunter's Prey: displayed names accepted", c == 200, (c, j))
c, j = call("POST", "/api/characters/subclass-choice", {"character_id": rid, "feature": "hunters_prey", "choise": "horde_breaker"}, T)
check("subclass-choice: misspelled field refused", c == 400 and j.get("error") == "unknown_field", (c, j))
cc = rec(rid).get("class_choices") or []
check("record: class_choices shows Hunter's Prey: Colossus Slayer", any(x["feature"] == "Hunter's Prey" and x["choice"] == "Colossus Slayer" for x in cc), cc)

# 6. Metamagic by displayed name; unknown field
sid, scmp = char_at("sorcerer", "sage", "tiefling", 2)
c, j = call("POST", "/api/characters/metamagic", {"character_id": sid, "metamagik": "careful"}, T)
check("metamagic: misspelled field refused", c == 400 and j.get("error") == "unknown_field", (c, j))
c, j = call("POST", "/api/characters/metamagic", {"character_id": sid, "metamagic": "Careful Spell"}, T)
check("metamagic: displayed name 'Careful Spell' accepted", c == 200, (c, j))
cc = rec(sid).get("class_choices") or []
check("record: class_choices shows Metamagic", any(x["feature"] == "Metamagic" and "Careful Spell" in x["choice"] for x in cc), cc)

# 6. Mystic Arcanum by displayed name
kid, kcmp = char_at("warlock", "sage", "human", 11)
st = step(kid, "mystic_arcanum") or {}
c, j = call("GET", "/api/characters/mystic-arcanum?character_id=%d" % kid, None, T)
opts = json.dumps(st) + json.dumps(j)
name = "True Seeing" if "true-seeing" in opts or "True Seeing" in opts else "Mass Suggestion"
c, j = call("POST", "/api/characters/mystic-arcanum", {"character_id": kid, "spell_level": 6, "spell_slug": name}, T)
check(f"Mystic Arcanum: displayed name '{name}' accepted", c == 200, (c, j))
c, j = call("POST", "/api/characters/mystic-arcanum", {"character_id": kid, "spell_levl": 6, "spell_slug": name}, T)
check("mystic-arcanum: misspelled field refused", c == 400 and j.get("error") == "unknown_field", (c, j))

# 7/8. Lore Bard: Bonus Proficiencies duplicate; Magical Discoveries suggestion and refusal of a spell already had
bid, bcmp = char_at("bard", "acolyte", "human", 6)
c, j = call("POST", "/api/characters/subclass", {"character_id": bid, "subclass": "lore", "bonus_skills": ["arcana", "arcana", "nature"]}, T)
check("Lore subclass with a duplicate bonus skill: duplicate_skill", c == 400 and j.get("error") == "duplicate_skill", (c, j))
check("...and the subclass was NOT saved", not rec(bid).get("subclass"), rec(bid).get("subclass"))
c, j = call("POST", "/api/characters/subclass", {"character_id": bid, "subclass": "lore"}, T)
check("Lore subclass without bonus_skills accepted (setup asks later)", c == 200, (c, j))
bid2, _ = char_at("bard", "acolyte", "human", 3)
c, j = call("POST", "/api/characters/subclass", {"character_id": bid2, "subclass": "lore", "bonus_skills": ["acrobatics", "deception", "history"]}, T)
check("Lore subclass with 3 good bonus_skills: saved, and recorded as chosen", c == 200 and any(x["feature"] == "Bonus Proficiencies" for x in rec(bid2).get("class_choices") or []) and step(bid2, "bonus_proficiencies") is None, (c, j, rec(bid2).get("class_choices")))
c, j = call("POST", f"/api/characters/{bid}/class-options", {"bonus_proficiencies": ["arcana", "arcana", "nature"]}, T)
check("class-options bonus_proficiencies duplicate: duplicate_skill (not already_proficient)", c == 400 and j.get("error") == "duplicate_skill", (c, j))
bp = step(bid, "bonus_proficiencies")
if bp:
    c, j = call(bp["request"]["method"], bp["request"]["path"], bp["request"]["body"], T)
    check("Bonus Proficiencies: setup suggestion accepted", c == 200, (c, j))
r = rec(bid)
have = set()
for k in ("known_spells", "prepared_spells", "origin_spells"):
    for x in r.get(k) or []:
        have.add(x if isinstance(x, str) else (x.get("slug") or x.get("spell") or ""))
md = step(bid, "magical_discoveries")
check("Magical Discoveries step present", md is not None, [s.get("key") for s in setup(bid).get("steps") or []])
if md:
    pick = md["request"]["body"]["magical_discoveries"]
    optset = [o["spell"] for o in md["choices"]["options"]]
    print("INFO  bard already has:", sorted(have), " suggestion:", pick)
    check("Magical Discoveries suggestion names no spell the Bard has", not (set(pick) & have) and len(pick) == 2, (pick, sorted(have)))
    check("Magical Discoveries options leave out spells the Bard has", not (set(optset) & have), sorted(set(optset) & have))
    dup = [h for h in have if h and h in ("guidance", "sacred-flame", "bless", "cure-wounds", "healing-word", "spare-the-dying", "thaumaturgy", "light")]
    if dup:
        c, j = call("POST", f"/api/characters/{bid}/class-options", {"magical_discoveries": [dup[0], pick[1]]}, T)
        check(f"Magical Discoveries: a spell already had ({dup[0]}) refused", c >= 400, (c, j))
    c, j = call(md["request"]["method"], md["request"]["path"], md["request"]["body"], T)
    check("Magical Discoveries: suggestion accepted", c == 200, (c, j))
cc = rec(bid).get("class_choices") or []
check("record: class_choices shows Bonus Proficiencies and Magical Discoveries", any(x["feature"] == "Bonus Proficiencies" for x in cc) and any(x["feature"] == "Magical Discoveries" for x in cc), cc)

# 5. Wild Shape forms on the record
did, dcmp = char_at("druid", "sage", "elf", 2)
ws = step(did, "wild_shape_forms") or step(did, "wild_shape")
if ws:
    c, j = call(ws["request"]["method"], ws["request"]["path"], ws["request"]["body"], T)
    check("Wild Shape: setup suggestion accepted", c == 200, (c, j))
else:
    print("NOTE  no wild shape step key found:", [s.get("key") for s in setup(did).get("steps") or []])
cc = rec(did).get("class_choices") or []
check("record: class_choices shows Wild Shape forms", any(x["feature"] == "Wild Shape forms" for x in cc), cc)

# sheet page has the Choices section code
c, page = call("GET", f"/character/{did}", None, None)
print("INFO  sheet page fetched:", c)

n = sum(1 for _, ok in results if ok)
print(f"\n{n} of {len(results)} checks passed")
sys.exit(0 if n == len(results) else 1)
