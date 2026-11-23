# One class from creation to level 20. Every exchange is saved with who sent it (player, DM, server) for the readable log.
import json, sys, random, base64
from ah import call as _call, register, png, D
# a step the server marks optional or asked (the starting level, the shop, the portrait) is left as it is unless this script follows it by name
# (the portrait: the upload is tested); "left open" counts only the required steps
FOLLOWED_OPTIONAL = {"portrait"}
def open_step(x): return not x.get("done") and ((not x.get("optional") and not x.get("asked")) or x.get("key") in FOLLOWED_OPTIONAL)

cls, bg, sp = sys.argv[1], sys.argv[2], sys.argv[3]
OUT = open(D + f"levelrun_{cls}.jsonl", "w")
# 2024 Player's Handbook options that are not in the SRD 5.2.1 (and not on this server)
PHB = {
 "backgrounds": ["farmer", "noble"], "species": ["aasimar"],
 "subclasses": {"barbarian": ["wild_heart", "world_tree", "zealot"], "bard": ["dance", "glamour", "valor"], "cleric": ["light", "trickery", "war"],
   "druid": ["moon", "sea", "stars"], "fighter": ["battle_master", "eldritch_knight", "psi_warrior"], "monk": ["mercy", "shadow", "elements"],
   "paladin": ["glory", "ancients", "vengeance"], "ranger": ["beast_master", "fey_wanderer", "gloom_stalker"], "rogue": ["arcane_trickster", "assassin", "soulknife"],
   "sorcerer": ["aberrant", "clockwork", "wild_magic"], "warlock": ["archfey", "celestial", "great_old_one"], "wizard": ["abjurer", "diviner", "illusionist"]},
 "feats": ["great_weapon_master", "tough", "sentinel", "war_caster", "polearm_master"], "epic_boons": ["boon_of_energy_resistance", "boon_of_recovery"],
 "fighting_styles": ["dueling", "protection", "blind_fighting"],
}
def call(who, m, p, b=None, tok=None, note=""):
    c, j = _call(m, p, b, tok, f"levelrun {cls}")
    rb = b
    if isinstance(b, dict) and "image_base64" in b: rb = {"image_base64": "(a 1024 x 1280 PNG image, base64 encoded)"}
    OUT.write(json.dumps({"who": who, "method": m, "path": p, "body": rb, "status": c, "reply": j, "note": note}) + "\n"); OUT.flush()
    return c, j
def mark(text):
    OUT.write(json.dumps({"section": text}) + "\n"); OUT.flush()
problems = []
TRIED = set()
G = register("DM")["token"]; T = register("AIPlayer")["token"]
mark("Character creation")
name = f"{cls.title()} Test {random.randint(100000,999999)}"
c, d = call("player", "POST", "/api/character-builder", {"name": name}, T)
P = "/api/character-builder/%d" % d["draft_id"]
first = {"class": {"class": cls}, "background": {"background": bg}, "species": {"species": sp}}
for i in range(25):
    cs = d.get("current_step") or {}
    key = cs.get("key")
    if "Every step is done" in (d.get("next") or ""): break
    if key == "human_feat_tried": key = None
    if key == "background" and "background" in first:
        for x in PHB["backgrounds"]:
            call("player", "POST", P, {"background": x}, T, note=f"A 2024 Player's Handbook background that is not in the SRD ({x})")
    if key == "species" and "species" in first:
        for x in PHB["species"]:
            call("player", "POST", P, {"species": x}, T, note=f"A 2024 Player's Handbook species that is not in the SRD ({x})")
    if key == "species" and "species" not in first and sp == "human" and "human_feat_tried" not in first:
        first["human_feat_tried"] = True
        call("player", "POST", P, {"species_choices": {"size": "medium", "skill": "perception", "feat": "tough"}}, T, note="A 2024 Player's Handbook Origin feat that is not in the SRD (Tough), as the Human's feat")
        c, d = call("player", "GET", P, None, T)
        cs = d.get("current_step") or {}; key = cs.get("key")
    if key in first: body = first.pop(key)
    elif key == "details": body = dict(cs.get("suggestion") or {}); body["alignment"] = "Neutral Good"
    elif key == "backstory": body = {"backstory": f"{name} grew up at the edge of the wild.\n\nA hard winter taught them to fight.\n\nNow they look for a place to belong."}
    else: body = cs.get("suggestion") or {}
    if not body: problems.append(f"creation step {key}: no suggestion to send"); break
    c, d = call("player", "POST", P, body, T)
    if d.get("error"): problems.append(f"creation step {key}: {d.get('error')} {d.get('message')}"); break
c, f = call("player", "POST", P + "/finish", {}, T)
cid = f.get("character_id")
if not cid: print("FINISH FAILED", json.dumps(f)[:500]); sys.exit(1)
def follow_setup(stage):
    for rnd in range(8):
        c, s = call("player", "GET", f"/api/characters/{cid}/setup", None, T)
        op = [x for x in s.get("steps", []) if open_step(x)]
        if not op: return []
        acted = False
        for st in op:
            k = st["key"]
            if k == "portrait":
                call("player", "POST", f"/api/characters/{cid}/portrait", {"image_base64": base64.b64encode(png(1024, 1280)).decode()}, T); acted = True
            elif k == "auto_equip":
                c, o = call("player", "POST", "/api/characters/auto-equip", {"character_id": cid, "answer": "yes"}, T)
                if o.get("needs_choices"):
                    call("player", "POST", "/api/characters/auto-equip", {"character_id": cid, "answer": "yes", "choices": {q["key"]: q["options"][0]["value"] for q in o["needs_choices"]}}, T)
                acted = True
            elif isinstance(st.get("request"), dict) and st["request"].get("method"):
                tried_key = (stage, k)
                if tried_key not in TRIED:
                    TRIED.add(tried_key)
                    if k == "subclass":
                        for x in PHB["subclasses"].get(cls, []):
                            call("player", "POST", "/api/characters/subclass", {"character_id": cid, "subclass": x}, T, note=f"A 2024 Player's Handbook subclass that is not in the SRD ({x})")
                    if k == "asi_or_feat":
                        lv = int(stage.split()[-1]) if stage.split()[-1].isdigit() else 0
                        pool = PHB["epic_boons"] if lv >= 19 else PHB["feats"]
                        x = pool[(lv // 4) % len(pool)]
                        call("player", "POST", f"/api/characters/{cid}/feat", {"feat": x}, T, note=f"A 2024 Player's Handbook feat that is not in the SRD ({x})")
                    if k == "spells" and stage in ("level 1", "level 13") and isinstance(st["request"].get("body"), dict):
                        b2 = json.loads(json.dumps(st["request"]["body"]))
                        for fk, fv in b2.items():
                            if isinstance(fv, list):
                                b2[fk] = fv + ["power-word-fortify"]
                        call("player", st["request"]["method"], st["request"]["path"], b2, T, note="A 2024 Player's Handbook spell that is not in the SRD (Power Word Fortify)")
                    if k == "fighting_style":
                        for x in PHB["fighting_styles"][:1]:
                            call("player", "POST", "/api/characters/fighting-style", {"character_id": cid, "style": x}, T, note=f"A 2024 Player's Handbook Fighting Style that is not in the SRD ({x})")
                rq = st["request"]; c, o = call("player", rq["method"], rq["path"], rq.get("body"), T); acted = True
                if c >= 400 or o.get("error"): problems.append(f"{stage}: step {k}: {o.get('error')} {o.get('message')}")
                if st.get("then"):
                    t = st["then"]["request"]; c, o = call("player", t["method"], t["path"], t.get("body"), T)
                    if c >= 400 or o.get("error"): problems.append(f"{stage}: step {k} (then): {o.get('error')} {o.get('message')}")
        if not acted: return [(x["key"], x.get("title")) for x in op]
    c, s = call("player", "GET", f"/api/characters/{cid}/setup", None, T)
    return [(x["key"], x.get("title")) for x in s.get("steps", []) if not x.get("done")]
mark("Finishing the new character (setup)")
left = follow_setup("level 1")
if left: problems.append(f"level 1: setup left open with no request to follow: {left}")
mark("Joining a campaign")
c, camp = call("dm", "POST", "/api/campaigns", {"name": f"{cls.title()} Levels {random.randint(1000,9999)}", "setting": "A test of every level.", "max_players": 4}, G)
cmp = camp["campaign_id"]
call("player", "POST", f"/api/campaigns/{cmp}/join", {"character_id": cid}, T)
call("dm", "POST", f"/api/campaigns/{cmp}/start", None, G)
for L in range(2, 21):
    mark(f"Level {L}")
    c, j = call("dm", "POST", "/api/gm/update-character", {"character_id": cid, "level": L}, G)
    if j.get("error"): problems.append(f"level {L}: DM set level: {j}"); break
    c, n = call("player", "GET", f"/api/characters/{cid}/level-up-notices", None, T)
    notices = n.get("notices") or []
    if not notices or notices[-1].get("level") != L: problems.append(f"level {L}: no level-up notice for this level")
    left = follow_setup(f"level {L}")
    if left: problems.append(f"level {L}: open with no request to follow: {left}")
    call("player", "POST", f"/api/characters/{cid}/level-up-notices", {"seen": True}, T)
    c, sh = call("player", "GET", f"/api/characters/{cid}", None, T)
    if not sh.get("character_finished"): problems.append(f"level {L}: not finished: {sh.get('to_finish')}")
    if sh.get("level") != L: problems.append(f"level {L}: sheet says level {sh.get('level')}")
json.dump({"class": cls, "character_id": cid, "problems": problems}, open(D + f"levelrun_{cls}_result.json", "w"), indent=1)
print("character", cid, "problems:", len(problems))
for p in problems: print(" -", p[:400])
import sys
sys.exit(1 if problems else 0)  # a problem fails the run (it exited 1 only when finish failed; audit AUD-48)
