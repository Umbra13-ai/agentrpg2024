# The Expertise rule, through every path that uses it (game server: game/expertise.go, cmd/server/expertise_2024.go).
# One table says which class gets Expertise, at which level, in how many skills and in which skills. This test goes through:
#   1. the builder's step 2, for all 12 classes (the choice is there only for a class with Expertise at level 1);
#   2. the one-request creation route (POST /api/characters, dry_run), same refusals;
#   3. for every class with Expertise, at every level it gains it: the level-up note, the to-finish line, GET and POST
#      /api/characters/{id}/expertise (count, duplicate, not proficient, not a skill, a skill the feature doesn't allow),
#      the setup step (features, options, suggestion accepted), the level-up notice;
#   4. a class with no Expertise;
#   5. Expertise is permanent once the campaign has started, and free to correct before;
#   6. Expertise from another source is kept, isn't an option and isn't counted as a class pick.
# Local: python3 expertise_rule_test.py      Live: BASE=https://rpg.umbra13.dev ARPG_SECRETS=<dir outside the repo> python3 ...
import json, os, random, sys
from ah import call, register, build

results = []
def check(name, ok, detail=""):
    results.append((name, bool(ok)))
    print(("PASS " if ok else "FAIL ") + name + ("" if ok else "  -> " + str(detail)[:700]), flush=True)

G = register("ExpGM")["token"]
T = register("ExpP")["token"]
KEEP = os.environ.get("ARPG_SECRETS")
CREATED = {"chars": [], "campaigns": []}
def keep():
    if KEEP:
        os.makedirs(KEEP, exist_ok=True)
        p = KEEP + "/expertise_run.json"
        json.dump({"gm": G, "player": T, **CREATED}, open(p, "w")); os.chmod(p, 0o600)

SCHOLAR = ["arcana", "history", "investigation", "medicine", "nature", "religion"]
# the SRD's Expertise features: class -> [(level, feature, count, allowed skills or None)]
SRD = {"bard": [(2, "Expertise", 2, None), (9, "Expertise", 2, None)],
       "ranger": [(2, "Deft Explorer", 1, None), (9, "Expertise", 2, None)],
       "rogue": [(1, "Expertise", 2, None), (6, "Expertise", 2, None)],
       "wizard": [(2, "Scholar", 1, SCHOLAR)]}
CLASSES = ["barbarian", "bard", "cleric", "druid", "fighter", "monk", "paladin", "ranger", "rogue", "sorcerer", "warlock", "wizard"]

def rec(cid): return call("GET", f"/api/characters/{cid}", None, T)[1]
def setup_step(cid, key):
    for s in call("GET", f"/api/characters/{cid}/setup?view=full", None, T)[1].get("steps") or []:
        if s.get("key") == key and not s.get("done"):
            return s
    return None
def skills_of(cid):
    v = rec(cid).get("skill_proficiencies") or []
    if isinstance(v, str): v = [x.strip() for x in v.split(",")]
    return [x.lower().replace("_", " ") for x in v if x]
def exp_line(cid):
    return [t for t in rec(cid).get("to_finish") or [] if t.startswith("Expertise")]

# ---------------------------------------------------------------- 1. builder step 2, every class
print("--- builder step 2, every class")
for cls in CLASSES:
    c, d = call("POST", "/api/character-builder", {"name": "Exp %s %d" % (cls, random.randint(10000, 99999))}, T)
    P = "/api/character-builder/%d" % d["draft_id"]
    c, v = call("POST", P, {"class": cls}, T)
    c, v = call("GET", P, None, T)
    row = [ch for ch in (v.get("current_step") or {}).get("choices", []) if ch.get("field") == "expertise"]
    want = sum(n for (lvl, _, n, _) in SRD.get(cls, []) if lvl == 1)
    check(f"builder: {cls} step 2 {'asks for Expertise in %d skills' % want if want else 'has no Expertise choice'}",
          (len(row) == 1 and row[0].get("count") == want) if want else not row, row)
    if not want:
        c, v = call("POST", P, {"expertise": ["stealth", "perception"]}, T)
        blob = json.dumps(v)
        check(f"builder: {cls} refuses an Expertise answer and says when classes get it", c >= 400 and "has no Expertise to choose" in blob and "Rogue at levels 1 and 6" in blob, (c, blob[:400]))
    else:
        call("POST", P, {"skill_proficiencies": ["acrobatics", "stealth", "deception", "perception"]}, T)
        for bad, why in ((["stealth", "stealth"], "twice"), (["stealth", "arcana"], "proficient"), (["stealth", "flying"], "isn't a skill")):
            c, v = call("POST", P, {"expertise": bad}, T)
            check(f"builder: {cls} Expertise {bad} refused ({why})", c >= 400 and why in json.dumps(v), (c, json.dumps(v)[:400]))
        c, v = call("POST", P, {"expertise": ["Stealth", "sleight_of_hand"]}, T)
        check(f"builder: {cls} Expertise in a skill it isn't proficient in (another spelling) refused", c >= 400, (c, json.dumps(v)[:300]))
        c, v = call("POST", P, {"expertise": ["Stealth", "Perception"]}, T)
        row = [ch for ch in (v.get("current_step") or {}).get("choices", []) if ch.get("field") == "expertise"]
        check(f"builder: {cls} Expertise in two proficient skills saved and done", c == 200 and row and row[0].get("done") and row[0].get("current") == ["stealth", "perception"], (c, row))
    call("DELETE", P, None, T)

# ---------------------------------------------------------------- 2. one-request creation
print("--- one-request creation (dry run)")
ROGUE = {"class": "rogue", "background": "soldier", "species": "dwarf", "ability_method": "standard_array", "str": 12, "dex": 15, "con": 13, "int": 14, "wis": 10, "cha": 8,
         "ability_increases": {"dex": 2, "con": 1}, "skill_proficiencies": ["acrobatics", "stealth", "deception", "perception"], "weapon_masteries": ["dagger", "shortbow"],
         "background_tool": "dice set", "background_equipment": "a", "starting_equipment": "a", "extra_languages": ["Elvish", "Goblin"], "alignment": "Neutral",
         "backstory": "One.\n\nTwo.\n\nThree.", "dry_run": True}
def create(extra):
    b = dict(ROGUE); b.update(extra); b["name"] = "Exp One %d" % random.randint(10000, 99999)
    return call("POST", "/api/characters", b, T)
c, j = create({})
check("creation: the Rogue body is valid without Expertise (setup asks later)", c == 200, (c, j))
for bad, code in ((["stealth", "stealth"], "duplicate_skill"), (["stealth"], "invalid_count"), (["stealth", "arcana"], "not_proficient"), (["stealth", "flying"], "invalid_skill"),
                  (["stealth", "perception", "acrobatics"], "invalid_count")):
    c, j = create({"expertise": bad})
    check(f"creation: Rogue Expertise {bad} -> {code}, with the options", c == 400 and j.get("error") == code and "Your options:" in j.get("message", ""), (c, j))
c, j = create({"expertise": ["Stealth", "Athletics"]})
check("creation: Rogue Expertise in a class skill and a background skill accepted", c == 200, (c, j))
c, j = create({"class": "wizard", "skill_proficiencies": ["arcana", "history"], "weapon_masteries": None, "expertise": ["arcana"]})
check("creation: a class with no Expertise at level 1 -> expertise_not_available, says when classes get it", c == 400 and j.get("error") == "expertise_not_available" and "Wizard at level 2 (Scholar)" in j.get("message", ""), (c, j))

# ---------------------------------------------------------------- 3. every class with Expertise, every level it gains it
def campaign():
    c, camp = call("POST", "/api/campaigns", {"name": "Expertise %d" % random.randint(1000, 99999), "setting": "test", "max_players": 4}, G)
    CREATED["campaigns"].append(camp["campaign_id"]); keep()
    return camp["campaign_id"]

def make(cls, bg, sp):
    cid, f = build(T, cls, bg, sp, "EX")
    assert cid, f
    CREATED["chars"].append({"id": cid}); keep()
    return cid

def level_to(cmp, cid, level):
    c, j = call("POST", "/api/gm/starting-level", {"campaign_id": cmp, "character_id": cid, "level": level, "gold": False}, G)
    assert c == 200, j
    return j.get("note") or ""

BUILDS = {"bard": ("acolyte", "human"), "ranger": ("criminal", "orc"), "rogue": ("criminal", "halfling"), "wizard": ("sage", "elf")}
for cls, grants in SRD.items():
    print(f"--- {cls}")
    cid = make(cls, *BUILDS[cls])
    cmp = None
    total = 0
    for (lvl, feature, count, allowed) in grants:
        before = total
        total += count
        note = ""
        if lvl == 1:
            g = call("GET", f"/api/characters/{cid}/expertise", None, T)[1]
            check(f"{cls} 1: built with its {count} Expertise skills (the builder's suggestion), nothing open", len(g.get("chosen") or []) == count and g.get("still_to_choose") == 0 and not exp_line(cid), g)
            # 5. before the campaign starts the picks can be corrected
            mine = skills_of(cid)
            other = [s for s in mine if s not in g["chosen"]][:count]
            c, j = call("POST", f"/api/characters/{cid}/expertise", {"skills": other}, T)
            check(f"{cls} 1: before the campaign starts the picks can be corrected", c == 200 and len(other) == count, (c, j, other))
        else:
            if cmp is None:
                cmp = campaign()
                call("POST", f"/api/campaigns/{cmp}/join", {"character_id": cid}, T)
                call("POST", f"/api/campaigns/{cmp}/start", None, G)
            if lvl > 2:  # nothing open on the level before
                level_to(cmp, cid, lvl - 1)
                check(f"{cls} {lvl - 1}: no Expertise open the level before", not exp_line(cid) or before > len(call("GET", f"/api/characters/{cid}/expertise", None, T)[1].get("chosen") or []), exp_line(cid))
            note = level_to(cmp, cid, lvl)
            words = f"{feature}: Expertise in {count} " + ("skill you" if count == 1 and not allowed else "skills you" if not allowed else "of Arcana, History, Investigation, Medicine, Nature or Religion")
            check(f"{cls} {lvl}: the level-up note names {feature} and the request", words in note and f"/api/characters/{cid}/expertise" in note, note[-600:])
            n = call("GET", f"/api/characters/{cid}/level-up-notices", None, T)[1]
            check(f"{cls} {lvl}: the level-up notice names the feature {feature}", f'"name": "{feature}"' in json.dumps(n), json.dumps(n)[:300])
        g = call("GET", f"/api/characters/{cid}/expertise", None, T)[1]
        feats = g.get("your_features") or []
        check(f"{cls} {lvl}: GET lists {len([x for x in SRD[cls] if x[0] <= lvl])} feature(s), {total} skills in all", g.get("allowed") == total and len(feats) == len([x for x in SRD[cls] if x[0] <= lvl]) and feats[-1]["feature"] == feature and feats[-1]["choose"] == count, g)
        if lvl == 1:
            continue
        chosen = g.get("chosen") or []
        line = exp_line(cid)
        check(f"{cls} {lvl}: to-finish says {before} of {total} chosen", line and f"{before} of {total}" in line[0], (line, chosen))
        st = setup_step(cid, "expertise")
        check(f"{cls} {lvl}: setup step present with features and options", st and st["choices"]["expertise_in_all"] == total and st["choices"]["your_features"], st)
        if not st:
            continue
        opts = [o["skill"] for o in st["choices"]["options_your_skill_proficiencies"]]
        held = skills_of(cid)
        if allowed:
            check(f"{cls} {lvl}: options are only {feature}'s skills the character is proficient in", opts and all(o in allowed and o in held for o in opts), (opts, held))
            off = [s for s in held if s not in allowed]
            if off:
                c, j = call("POST", f"/api/characters/{cid}/expertise", {"skills": chosen + [off[0]]}, T)
                check(f"{cls} {lvl}: a proficient skill {feature} doesn't allow ({off[0]}) -> skill_not_allowed", c == 400 and j.get("error") == "skill_not_allowed" and "Your options:" in j.get("message", ""), (c, j))
            else:
                print(f"NOTE  {cls}: holds no skill outside {feature}'s list; that refusal was not exercised")
        else:
            check(f"{cls} {lvl}: options are the character's skill proficiencies", sorted(opts) == sorted(held), (opts, held))
        sug = st["request"]["body"]["skills"]
        new = [s for s in sug if s not in chosen]
        check(f"{cls} {lvl}: the suggestion keeps the {before} chosen and adds {count}", len(sug) == total and all(s in sug for s in chosen) and len(new) == count, (sug, chosen))
        for bad, code in ((chosen + new[:1] * count if count > 1 else None, "duplicate_skill"), (chosen + new[:count - 1], "invalid_count"), (chosen + new[:count - 1] + ["flying"], "invalid_skill")):
            if bad is None:
                continue
            c, j = call("POST", f"/api/characters/{cid}/expertise", {"skills": bad}, T)
            check(f"{cls} {lvl}: {code}", c == 400 and j.get("error") == code, (c, j, bad))
        not_held = [s for s in (allowed or ["athletics", "arcana", "medicine", "survival", "history", "performance"]) if s not in held]
        if not_held:
            c, j = call("POST", f"/api/characters/{cid}/expertise", {"skills": chosen + new[:count - 1] + [not_held[0]]}, T)
            check(f"{cls} {lvl}: a skill it isn't proficient in ({not_held[0]}) -> not_proficient", c == 400 and j.get("error") == "not_proficient", (c, j))
        c, j = call("POST", f"/api/characters/{cid}/expertise", {"skils": sug}, T)
        check(f"{cls} {lvl}: a misspelled field refused", c == 400 and j.get("error") == "unknown_field", (c, j))
        if chosen:  # 5. permanent once the campaign has started
            repl = [s for s in opts if s not in sug]
            if len(repl) >= len(chosen):
                c, j = call("POST", f"/api/characters/{cid}/expertise", {"skills": repl[:len(chosen)] + new}, T)
                check(f"{cls} {lvl}: dropping an earlier pick after the campaign started -> expertise_is_permanent", c == 409 and j.get("error") == "expertise_is_permanent", (c, j))
            else:
                print(f"NOTE  {cls} {lvl}: not enough other skills to try replacing the earlier picks")
        c, j = call(st["request"]["method"], st["request"]["path"], st["request"]["body"], T)
        check(f"{cls} {lvl}: the setup suggestion is accepted", c == 200, (c, j))
        g = call("GET", f"/api/characters/{cid}/expertise", None, T)[1]
        check(f"{cls} {lvl}: all {total} chosen, nothing open, no setup step", len(g.get("chosen") or []) == total and g.get("still_to_choose") == 0 and not exp_line(cid) and setup_step(cid, "expertise") is None, g)
        sheet = rec(cid).get("expertise") or []
        check(f"{cls} {lvl}: the sheet lists the {total} Expertise skills", len(sheet) == total, sheet)
    if cmp is not None and grants[-1][0] < 20:
        level_to(cmp, cid, 20)
        check(f"{cls} 20: still {total} skills, nothing open", call("GET", f"/api/characters/{cid}/expertise", None, T)[1].get("allowed") == total and not exp_line(cid))

# ---------------------------------------------------------------- 4. a class with no Expertise
print("--- fighter (no Expertise)")
fid = make("fighter", "soldier", "human")
cmp = campaign(); call("POST", f"/api/campaigns/{cmp}/join", {"character_id": fid}, T); call("POST", f"/api/campaigns/{cmp}/start", None, G)
note = level_to(cmp, fid, 9)
g = call("GET", f"/api/characters/{fid}/expertise", None, T)[1]
check("fighter 9: no Expertise feature, nothing open, no note line, no setup step", g.get("allowed") == 0 and not g.get("your_features") and not exp_line(fid) and "Expertise" not in note and setup_step(fid, "expertise") is None, (g, note[-300:]))
c, j = call("POST", f"/api/characters/{fid}/expertise", {"skills": ["athletics"]}, T)
check("fighter 9: POST -> no_expertise, says when classes get it", c == 400 and j.get("error") == "no_expertise" and "Bard at levels 2 and 9" in j.get("message", ""), (c, j))

# ---------------------------------------------------------------- 6. Expertise from another source
print("--- Expertise from another source (a rogue; the GM records it as a magic item's gift would)")
rid = make("rogue", "criminal", "halfling")
cmp = campaign(); call("POST", f"/api/campaigns/{cmp}/join", {"character_id": rid}, T); call("POST", f"/api/campaigns/{cmp}/start", None, G)
g0 = call("GET", f"/api/characters/{rid}/expertise", None, T)[1]
mine = skills_of(rid)
gift = [s for s in mine if s not in g0["chosen"]][0]
c, j = call("POST", "/api/gm/update-character", {"campaign_id": cmp, "character_id": rid, "set": {"expertise_other": gift, "expertise": ", ".join(g0["chosen"] + [gift])}}, G)
check("GM records Expertise from another source", c == 200, (c, j))
g = call("GET", f"/api/characters/{rid}/expertise", None, T)[1]
check("other source: the class picks are still 2 of 2, the gift isn't an option", g.get("chosen") == g0["chosen"] and g.get("still_to_choose") == 0 and gift not in (g.get("options") or []) and not exp_line(rid), g)
level_to(cmp, rid, 6)
st = setup_step(rid, "expertise")
check("other source: at level 6 the step asks for 4, the gift is not suggested or offered", st and st["choices"]["expertise_in_all"] == 4 and gift not in st["request"]["body"]["skills"] and gift not in [o["skill"] for o in st["choices"]["options_your_skill_proficiencies"]], st)
if st:
    c, j = call("POST", f"/api/characters/{rid}/expertise", {"skills": g0["chosen"] + [gift] + [s for s in st["request"]["body"]["skills"] if s not in g0["chosen"]][:1]}, T)
    check("other source: picking the gifted skill as a class pick -> already_expertise", c == 400 and j.get("error") == "already_expertise", (c, j))
    c, j = call(st["request"]["method"], st["request"]["path"], st["request"]["body"], T)
    sheet = rec(rid).get("expertise") or []
    check("other source: after the class picks are sent the sheet has 5 (4 class + the gift)", c == 200 and len(sheet) == 5 and gift in [s.lower() for s in sheet], (c, j, sheet))

n = sum(1 for _, ok in results if ok)
print(f"\n{n} of {len(results)} checks passed")
sys.exit(0 if n == len(results) else 1)
