# R6 (the rest): a feat taken after creation takes its choices (game server: feats_2024.go, origins.go takeOriginFeat).
# Skilled and Magic Initiate take their choices in the feat request ("choices", the same object as background_feat_choices at
# creation), checked by the creation check (resolveFeat) against what the character already has, and recorded with the
# background's and the species' feats; a Fighting Style feat is one more Fighting Style. Paths: the feat request with an Ability
# Score Improvement (every class at level 8), the setup step's options, the Fighting Style count, and Lessons of the First Ones
# (its choice line, its setup step and the request without points).
# Local: python3 feat_choices_rule_test.py
import json, random, sys
from ah import call, register, build, BASE, only

only("local", "staging", why="it takes feats and levels on many characters")
results = []
def check(name, ok, detail=""):
    results.append((name, bool(ok)))
    print(("PASS " if ok else "FAIL ") + name + ("" if ok else "  -> " + json.dumps(detail)[:600]), flush=True)

G = register("FcGM")["token"]; T = register("FcP")["token"]
def at(cls, bg, sp, level):
    cid, f = build(T, cls, bg, sp, "FC"); assert cid, f
    c, camp = call("POST", "/api/campaigns", {"name": "FC %d" % random.randint(1000, 99999), "setting": "t", "max_players": 4}, G)
    cmp = camp["campaign_id"]
    call("POST", f"/api/campaigns/{cmp}/join", {"character_id": cid}, T); call("POST", f"/api/campaigns/{cmp}/start", None, G)
    c, j = call("POST", "/api/gm/starting-level", {"campaign_id": cmp, "character_id": cid, "level": level, "gold": False}, G); assert c == 200, j
    return cid
def feat(cid, body): return call("POST", f"/api/characters/{cid}/feat", body, T, "feat_choices")
def sheet(cid): return call("GET", f"/api/characters/{cid}", None, T)[1]
def setup_step(cid, key):
    for s in call("GET", f"/api/characters/{cid}/setup", None, T)[1].get("steps") or []:
        if s.get("key") == key: return s
c, allf = call("GET", "/api/universe/feats", None, T)
FS = sorted(f["slug"] for f in allf.get("feats") or [] if f.get("category") == "Fighting Style")
STYLE_CLASSES = ("fighter", "paladin", "ranger")
# SRD 5.2.1 spells (the universe list doesn't filter by class): Wizard cantrips and level 1 spells, and Cleric level 1 spells
# that aren't on the Wizard list
LISTS = {"wizard": {0: ["fire-bolt", "light", "mage-hand", "minor-illusion", "prestidigitation", "ray-of-frost", "shocking-grasp", "acid-splash"],
                    1: ["magic-missile", "shield", "sleep", "detect-magic", "burning-hands", "feather-fall"]},
         "cleric": {0: ["guidance", "light", "resistance", "sacred-flame", "spare-the-dying", "thaumaturgy", "word-of-radiance"],
                    1: ["bless", "command", "cure-wounds", "guiding-bolt", "healing-word", "sanctuary", "shield-of-faith"]},
         "druid": {0: ["druidcraft", "guidance", "produce-flame", "resistance", "shillelagh", "thorn-whip", "poison-spray"],
                   1: ["cure-wounds", "entangle", "faerie-fire", "goodberry", "healing-word", "thunderwave"]}}
OFF_LIST = {"wizard": "bless", "cleric": "magic-missile", "druid": "magic-missile"}
def spells_of(lst, level): return LISTS[lst][level]

CLASSES = [("barbarian", "soldier", "dwarf"), ("bard", "acolyte", "human"), ("cleric", "acolyte", "dwarf"), ("druid", "sage", "elf"),
           ("fighter", "soldier", "goliath"), ("monk", "criminal", "halfling"), ("paladin", "soldier", "dragonborn"), ("ranger", "criminal", "elf"),
           ("rogue", "criminal", "halfling"), ("sorcerer", "sage", "tiefling"), ("warlock", "criminal", "tiefling"), ("wizard", "sage", "gnome")]
for cls, bg, sp in CLASSES:
    tag = f"{cls} 8"
    cid = at(cls, bg, sp, 8)
    st = setup_step(cid, "asi_or_feat")
    opts = {o["feat"]: o for o in (st or {}).get("choices", {}).get("option_2_a_feat_you_qualify_for", [])}
    check(f"{tag}: the setup step offers Skilled and Magic Initiate, each with its choices in how_to_take",
          all(s in opts and '"choices"' in opts[s].get("how_to_take", "") for s in ("skilled", "magic_initiate")), list(opts))
    has_style = cls in STYLE_CLASSES
    check(f"{tag}: Fighting Style feats {'offered' if has_style else 'not offered'} (the Fighting Style feature)",
          any(s in opts for s in FS) == has_style, [s for s in opts if s in FS])
    # Skilled: refusals, then taken
    ch = sheet(cid)
    held = [s.lower() for s in ch.get("skill_proficiencies") or []]
    c, j = feat(cid, {"feat": "Skilled"})
    check(f"{tag}: Skilled without choices -> feat_choices_required, with how_to_take", c == 400 and j.get("error") == "feat_choices_required" and "choices" in j.get("how_to_take", ""), (c, j))
    c, j = feat(cid, {"feat": "Skilled", "choices": {"proficiencies": [held[0], "arcana", "history"]}})
    check(f"{tag}: Skilled with a skill already held -> proficiency_already_held", c == 400 and j.get("error") == "proficiency_already_held", (c, j))
    c, j = feat(cid, {"feat": "Skilled", "choices": {"proficiencies": ["not a skill", "arcana", "history"]}})
    check(f"{tag}: Skilled with something that isn't a skill or tool -> unknown_proficiency", c == 400 and j.get("error") == "unknown_proficiency", (c, j))
    new = [s for s in ("Arcana", "History", "Nature", "Religion", "Medicine", "Investigation", "Performance") if s.lower() not in held][:2] + ["Smith's Tools"]
    c, j = feat(cid, {"feat": "Skilled", "choices": {"proficiencies": new}})
    check(f"{tag}: Skilled taken with two skills and a tool", c == 200 and j.get("points_spent") == 2, (c, j))
    ch = sheet(cid)
    skills = [s.lower() for s in ch.get("skill_proficiencies") or []]
    tools = json.dumps(ch.get("tool_proficiencies")).lower()
    check(f"{tag}: the skills and the tool are on the sheet", all(s.lower() in skills for s in new[:2]) and "smith" in tools, (skills, tools))
    fe = [f for f in ch.get("feats") or [] if f.get("slug") == "skilled" and f.get("source") == "Ability Score Improvement"]
    check(f"{tag}: the sheet shows Skilled from the Ability Score Improvement, with its picks", fe and fe[0].get("proficiencies"), ch.get("feats"))
    plain = [f for f in ("alert", "savage_attacker") if f not in [x.get("slug") for x in ch.get("feats") or []]]
    if plain:
        c, j = feat(cid, {"feat": plain[0], "choices": {"proficiencies": ["arcana"]}})
        check(f"{tag}: choices sent with a feat that takes none ({plain[0]}) -> choices_not_used", c == 400 and j.get("error") == "choices_not_used", (c, j))
    # Magic Initiate: refusals, then taken
    slug_of = lambda s: s if isinstance(s, str) else (s.get("slug") or "")
    known = {slug_of(s) for s in (ch.get("known_spells") or []) + (ch.get("origin_spells") or [])}
    known |= {slug_of(s) for s in (ch.get("prepared_spells") or [])}
    used = [f.get("list") for f in ch.get("feats") or [] if f.get("slug") == "magic_initiate"]
    lst = [l for l in ("wizard", "cleric", "druid") if l not in used][0]
    if used:  # Magic Initiate from the background: the same list again is refused
        c, j = feat(cid, {"feat": "magic_initiate", "choices": {"list": used[0], "cantrips": spells_of(used[0], 0)[-2:], "spell": spells_of(used[0], 1)[-1], "spellcasting_ability": "wis"}})
        check(f"{tag}: Magic Initiate with its background's list ({used[0]}) -> spell_list_already_used", c == 400 and j.get("error") == "spell_list_already_used", (c, j))
    cantrips, level1 = spells_of(lst, 0), spells_of(lst, 1)
    two = [s for s in cantrips if s not in known][:2]
    spell = [s for s in level1 if s not in known][0]
    base = {"list": lst, "cantrips": two, "spell": spell, "spellcasting_ability": "int"}
    for what, change, code in (("a list that isn't Cleric, Druid or Wizard", {"list": "bard"}, "invalid_spell_list"),
                               ("one cantrip", {"cantrips": two[:1]}, "invalid_cantrips"),
                               ("a level 1 spell as a cantrip", {"cantrips": [two[0], spell]}, "wrong_spell_level"),
                               ("a spell of another list", {"spell": OFF_LIST[lst]}, "not_on_spell_list"),
                               ("an ability that isn't Int, Wis or Cha", {"spellcasting_ability": "str"}, "invalid_spellcasting_ability")):
        c, j = feat(cid, {"feat": "magic_initiate", "choices": dict(base, **change)})
        check(f"{tag}: Magic Initiate with {what} -> {code}", c == 400 and j.get("error") == code, (c, j))
    mine = [s for s in known if s in cantrips]
    if mine:  # a cantrip the character already has, from its class or its origin
        c, j = feat(cid, {"feat": "magic_initiate", "choices": dict(base, cantrips=[mine[0], two[0]])})
        check(f"{tag}: Magic Initiate with a cantrip the character already has ({mine[0]}) -> spell_already_known", c == 400 and j.get("error") == "spell_already_known", (c, j))
    c, j = feat(cid, {"feat": "Magic Initiate", "choices": base})
    if c == 400 and j.get("error") == "insufficient_asi":
        print(f"NOTE  {tag}: no points left for a second feat")
        continue
    check(f"{tag}: Magic Initiate ({lst}) taken", c == 200 and j.get("chosen", {}).get("list") == lst, (c, j))
    ch = sheet(cid)
    os_ = {s.get("slug"): s for s in ch.get("origin_spells") or []}
    check(f"{tag}: its cantrips and spell are origin spells, the spell with one free cast", all(s in os_ for s in two + [spell]) and os_[spell].get("free_casts_per_long_rest") == 1, list(os_))
    c, j = feat(cid, {"feat": "magic_initiate", "choices": dict(base, cantrips=[s for s in cantrips if s not in known and s not in two][:2], spell=[s for s in level1 if s != spell][0])})
    check(f"{tag}: Magic Initiate again with the same list -> spell_list_already_used (or no points left)", c == 400 and j.get("error") in ("spell_list_already_used", "insufficient_asi"), (c, j))

# Fighting Style feats: one more Fighting Style; a known one can't be taken again
cid = at("fighter", "soldier", "human", 8)
fs0 = call("GET", f"/api/characters/fighting-style?character_id={cid}", None, T)[1]
c, j = call("POST", "/api/characters/fighting-style", {"character_id": cid, "style": "defense"}, T)
if c != 200: print("INFO  class style:", c, j)
c, j = feat(cid, {"feat": "Defense"})
check("fighter: a Fighting Style feat it already has (its class style) -> already_has_feat", c == 409 and j.get("error") == "already_has_feat", (c, j))
c, j = feat(cid, {"feat": "Archery"})
check("fighter: Archery taken with the Ability Score Improvement", c == 200, (c, j))
fs = call("GET", f"/api/characters/fighting-style?character_id={cid}", None, T)[1]
check("fighter: its Fighting Styles are Defense and Archery, two of two", sorted(s["slug"] for s in fs.get("fighting_styles", [])) == ["archery", "defense"] and fs.get("max_styles") == fs0.get("max_styles", 1) + 1, fs)
check("fighter: no Fighting Style is left to choose", not any("Fighting Style" in t for t in sheet(cid).get("to_finish") or []), sheet(cid).get("to_finish"))
st = setup_step(cid, "asi_or_feat")
check("fighter: Defense and Archery are off the setup step's feats", st is None or not ({"defense", "archery"} & {o["feat"] for o in st["choices"]["option_2_a_feat_you_qualify_for"]}), st)
wiz = at("wizard", "sage", "elf", 8)
c, j = feat(wiz, {"feat": "Archery"})
check("wizard: a Fighting Style feat without the feature -> prerequisite_not_met", c == 400 and j.get("error") == "prerequisite_not_met", (c, j))

# Lessons of the First Ones: an Origin feat without points, with its choices; each copy a different feat
wl = at("warlock", "criminal", "tiefling", 8)
c, inv = call("GET", f"/api/characters/invocations?character_id={wl}", None, T)
# (the SRD makes the invocation repeatable, but the invocation request takes one copy: see ISSUES-FOUND; so one copy here)
c, j = call("POST", "/api/characters/invocations", {"character_id": wl, "invocation": "lessons-of-the-first-ones"}, T)
check("warlock: Lessons of the First Ones taken", c == 200, (c, j))
tf = sheet(wl).get("to_finish") or []
check("warlock: to_finish names the Origin feats to choose (Lessons of the First Ones)", any("Lessons of the First Ones" in t and "/feat" in t for t in tf), tf)
st = setup_step(wl, "lessons_feat")
names = [o["feat"] for o in (st or {}).get("choices", {}).get("origin_feats", [])]
check("warlock: the setup step lists the Origin feats, Skilled and Magic Initiate with their choices", st and {"skilled", "magic_initiate"} <= set(names) and all(o["feat"] in ("alert", "savage_attacker", "skilled", "magic_initiate") for o in st["choices"]["origin_feats"]), st)
before = sheet(wl).get("pending_asi")
ch = sheet(wl); held = [s.lower() for s in ch.get("skill_proficiencies") or []]
pick = [s for s in ("Arcana", "History", "Nature", "Religion", "Medicine", "Insight", "Survival") if s.lower() not in held]
c, j = feat(wl, {"feat": "Skilled", "choices": {"proficiencies": pick[:3]}})
check("warlock: Skilled by Lessons of the First Ones, no points spent", c == 200 and j.get("points_spent") == 0 and sheet(wl).get("pending_asi") == before, (c, j))
check("warlock: the sheet says it came from Lessons of the First Ones", any(f.get("slug") == "skilled" and f.get("source") == "Lessons of the First Ones" for f in sheet(wl).get("feats") or []), sheet(wl).get("feats"))
tf = sheet(wl).get("to_finish") or []
check("warlock: nothing left from Lessons of the First Ones", not any("Lessons of the First Ones" in t for t in tf), tf)

n = sum(1 for _, ok in results if ok)
print(f"\n{n} of {len(results)} checks passed")
sys.exit(0 if n == len(results) else 1)
