# The feat rule, through every path that uses it (game server: feats_2024.go featRefusal / featsOpenTo).
# One rule decides which feats a character can take with an Ability Score Improvement. This test checks, for several classes at
# level 8 and 19, that the setup step's options and the list in the request's refusal are the same list, that a listed feat is
# accepted and an unlisted one refused with the reason. Skilled and Magic Initiate are listed and need their choices (taking them:
# feat_choices_rule_test.py); a Fighting Style feat is listed for a class with the Fighting Style feature.
import json, random, sys
from ah import call, register, build

results = []
def check(name, ok, detail=""):
    results.append((name, bool(ok)))
    print(("PASS " if ok else "FAIL ") + name + ("" if ok else "  -> " + str(detail)[:700]), flush=True)

G = register("FeatGM")["token"]; T = register("FeatP")["token"]
def at(cls, bg, sp, level):
    cid, f = build(T, cls, bg, sp, "FT"); assert cid, f
    c, camp = call("POST", "/api/campaigns", {"name": "Feats %d" % random.randint(1000, 99999), "setting": "test", "max_players": 4}, G)
    cmp = camp["campaign_id"]
    call("POST", f"/api/campaigns/{cmp}/join", {"character_id": cid}, T); call("POST", f"/api/campaigns/{cmp}/start", None, G)
    c, j = call("POST", "/api/gm/starting-level", {"campaign_id": cmp, "character_id": cid, "level": level, "gold": False}, G); assert c == 200, j
    return cid
def step_feats(cid):
    for s in call("GET", f"/api/characters/{cid}/setup?view=full", None, T)[1].get("steps") or []:
        if s.get("key") == "asi_or_feat":
            return [o["feat"] for o in s["choices"]["option_2_a_feat_you_qualify_for"]], s
    return None, None
def refusal_feats(cid):
    c, j = call("POST", f"/api/characters/{cid}/feat", {"feat": "no such feat"}, T)
    return sorted(j.get("available_feats") or []), c, j
c, allf = call("GET", "/api/universe/feats", None, T)
FEATS = {f["slug"]: f for f in (allf.get("feats") or [])}
FS = sorted(s for s, f in FEATS.items() if f.get("category") == "Fighting Style")
BOONS = sorted(s for s, f in FEATS.items() if f.get("category") == "Epic Boon")
print("INFO  feats on the server:", len(FEATS), "| Fighting Style:", FS, "| Epic Boons:", len(BOONS))

for cls, bg, sp, level in (("fighter", "soldier", "dwarf", 8), ("wizard", "sage", "elf", 8), ("paladin", "acolyte", "human", 19), ("fighter", "criminal", "orc", 19), ("rogue", "criminal", "halfling", 8)):
    tag = f"{cls} {level}"
    cid = at(cls, bg, sp, level)
    opts, st = step_feats(cid)
    ref, c, j = refusal_feats(cid)
    check(f"{tag}: the setup step's feats and the refusal's list are the same list", opts is not None and sorted(opts) == ref and ref, (opts, ref))
    have = [f if isinstance(f, str) else (f.get("slug") or f.get("feat") or "") for f in call("GET", f"/api/characters/{cid}", None, T)[1].get("feats") or []]
    have = [nm.lower().replace(" ", "_") for nm in have]
    styles = cls in ("fighter", "paladin", "ranger")
    bad = [s for s in ref if s == "ability_score_improvement" or (s in FS and not styles) or (s in BOONS and level < 19)]
    check(f"{tag}: the list has no feat it can't take (a Fighting Style without the feature, an Epic Boon before 19, the ASI itself)", not bad, bad)
    check(f"{tag}: Skilled and Magic Initiate are listed", {"skilled", "magic_initiate"} <= set(ref), ref)
    check(f"{tag}: Fighting Style feats {'are' if styles else 'are not'} listed", any(s in FS for s in ref) == styles, ref)
    check(f"{tag}: {'Epic Boons are listed' if level >= 19 else 'no Epic Boon is listed'}", any(s in BOONS for s in ref) == (level >= 19), ref)
    for name in ("Skilled", "magic_initiate"):
        c, j = call("POST", f"/api/characters/{cid}/feat", {"feat": name}, T)
        check(f"{tag}: {name} without its choices -> feat_choices_required, with how_to_take", c == 400 and j.get("error") == "feat_choices_required" and "choices" in j.get("how_to_take", ""), (c, j))
    c, j = call("POST", f"/api/characters/{cid}/feat", {"feat": "Ability Score Improvement"}, T)
    check(f"{tag}: Ability Score Improvement -> use_asi, with the feats it can take", c == 400 and j.get("error") == "use_asi" and sorted(j.get("available_feats") or []) == ref, (c, j))
    if not styles:
        c, j = call("POST", f"/api/characters/{cid}/feat", {"feat": FS[0]}, T)
        check(f"{tag}: a Fighting Style feat ({FS[0]}) without the feature -> prerequisite_not_met", c == 400 and j.get("error") == "prerequisite_not_met", (c, j))
    if level < 19:
        c, j = call("POST", f"/api/characters/{cid}/feat", {"feat": BOONS[0]}, T)
        check(f"{tag}: an Epic Boon before level 19 -> prerequisite_not_met", c == 400 and j.get("error") == "prerequisite_not_met", (c, j))
    if "boon_of_spell_recall" in FEATS and level >= 19:
        check(f"{tag}: Boon of Spell Recall (needs Spellcasting) is {'listed' if cls == 'paladin' else 'not listed'}", ("boon_of_spell_recall" in ref) == (cls == "paladin"), ref)
    # a listed feat is accepted (by its displayed name), then is no longer listed unless it is repeatable
    pick = [s for s in ref if not FEATS[s].get("repeatable") and not FEATS[s].get("choices_when_taken")][0]
    body = {"feat": FEATS[pick]["name"]}
    if FEATS[pick].get("ability_choices"):
        body["ability_choice"] = FEATS[pick]["ability_choices"][0]
    c, j = call("POST", f"/api/characters/{cid}/feat", body, T)
    if j.get("error") == "ability_choice_required":  # the feat raises a score: name one of the abilities the reply lists
        body["ability_choice"] = j["message"].split("one of:")[1].strip(" .").split(",")[0].strip()
        c, j = call("POST", f"/api/characters/{cid}/feat", body, T)
    check(f"{tag}: a listed feat ({FEATS[pick]['name']}) is accepted", c == 200, (c, j))
    ref2, c, j = refusal_feats(cid)
    opts2, _ = step_feats(cid)
    if j.get("error") == "insufficient_asi":
        print(f"NOTE  {tag}: no points left after one feat; the already-has check was not run")
    else:
        check(f"{tag}: once taken it is off both lists", pick not in ref2 and (opts2 is None or pick not in opts2), (ref2, opts2))
        c, j = call("POST", f"/api/characters/{cid}/feat", body, T)
        check(f"{tag}: taking it again -> already_has_feat", c == 409 and j.get("error") == "already_has_feat", (c, j))
n = sum(1 for _, ok in results if ok)
print(f"\n{n} of {len(results)} checks passed")
sys.exit(0 if n == len(results) else 1)
