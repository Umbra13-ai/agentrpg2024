"""Extract the Spell Descriptions of SRD 5.2.1 (CC-BY-4.0) from the PDF into spells.json.

Structure used: name = GillSans-SemiBold 12 pt; header = Cambria-Italic
("Level 3 Evocation (Wizard)" or "Evocation Cantrip (Wizard)"); then the labeled lines
Casting Time / Range / Components / Duration (GillSans 9.5); then the body (Cambria 10).
"""
import json
import os
import re
import sys
import warnings

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from srd_tables import blocks_of as _blocks_of, build_statblock, flat_blocks

warnings.filterwarnings("ignore")
import pymupdf

PDF = r"<workspace>\agentrpg-reference\SRD_CC_v5.2.1.pdf"
OUT = sys.argv[1] if len(sys.argv) > 1 else r"<workspace>\agentrpg-2024-research\items\spells.json"

doc = pymupdf.open(PDF)
HEADER = re.compile(r"^(?:Level (\d+) ([A-Z][a-z]+)|([A-Z][a-z]+) Cantrip) \(([^)]*)\)\s*$")
LABELS = ("Casting Time:", "Range:", "Components:", "Component:", "Duration:")

# page range: the first spell header to the page that starts the next chapter
first = last = None
for i, p in enumerate(doc):
    t = p.get_text()
    if first is None and "Acid Arrow" in t and "Casting Time:" in t:
        first = i
    if first is not None and i > first and "Zone of Truth" in t and "Casting Time:" in t:
        last = i
        break
print("spell pages", first + 1, "to", last + 1, file=sys.stderr)

stream = []  # (kind, text) in reading order, column by column
for i in range(first, last + 1):
    p = doc[i]
    cols = {0: [], 1: []}
    for b in p.get_text("dict")["blocks"]:
        if b.get("type") != 0:
            continue
        for l in b["lines"]:
            y = l["bbox"][1]
            if y > 735:  # the page footer; nothing is above the text, and a spell can start at the very top of a column
                continue
            spans = [s for s in l["spans"] if s["text"].strip() or s["text"] in ("\t", " ")]
            if not l["spans"]:
                continue
            f0 = l["spans"][0]["font"]
            size0 = l["spans"][0]["size"]
            text = "".join(s["text"] for s in l["spans"])
            col = 0 if l["bbox"][0] < 300 else 1
            fonts_all = " ".join(s["font"] for s in l["spans"])
            if "Optima" in fonts_all or abs(size0 - 14.8) < 0.3 or abs(size0 - 9.8) < 0.15 or abs(size0 - 6.0) < 0.15 \
                    or (f0.endswith("GillSans") and abs(size0 - 12.0) < 0.3 and text.strip() in ("Traits", "Actions", "Bonus Actions", "Reactions", "Legendary Actions")):
                kind = "stat"
            elif "SemiBold" in f0 and abs(size0 - 12.0) < 0.3:
                kind = "name"
            elif l["spans"][0]["text"].strip() in LABELS:
                kind = "label"  # the font of the label varies (SemiBold, Bold)
            elif "SemiBold" in f0 and size0 > 13:
                kind = "chapter"
            elif "Italic" in f0 and "Bold" not in f0:
                kind = "italic"
            else:
                kind = "text"
            cols[col].append((kind, l["spans"], text, l["bbox"], col))
    for c in (0, 1):
        for kind, spans, text, bbox, colno in cols[c]:
            stream.append((kind, spans, text, bbox, colno))

def header_start(txt):
    return bool(re.match(r"^(Level \d+ [A-Z][a-z]+|[A-Z][a-z]+ Cantrip)\b", txt.strip()))


for idx in range(len(stream)):
    k, sp_, tx, bb_, cl_ = stream[idx]
    if k == "name":
        nxt = stream[idx + 1] if idx + 1 < len(stream) else None
        nxt2 = stream[idx + 2] if idx + 2 < len(stream) else None
        real = (nxt and nxt[0] == "italic" and header_start(nxt[2])) or (nxt and nxt[0] == "name" and nxt2 and nxt2[0] == "italic" and header_start(nxt2[2]))
        if not real:
            stream[idx] = ("text", sp_, tx, bb_, cl_)  # inline bold text inside a description, not a spell name

spells = []
cur = None
pending_name = None
hdr_buf = ""
state = None
for kind, spans, text, bbox, colno in stream:
    t = text.strip()
    if kind == "name":
        if pending_name is not None and cur is None:
            pending_name += " " + t
        else:
            pending_name = t
        continue
    if kind == "italic" and pending_name is not None:
        hdr_buf = (hdr_buf + " " + t).strip()
        if ")" not in hdr_buf:
            continue  # the class list wraps onto the next line
        t = hdr_buf
        hdr_buf = ""
    if kind == "italic" and pending_name is not None and HEADER.match(t):
        m = HEADER.match(t)
        level = int(m.group(1)) if m.group(1) else 0
        school = m.group(2) or m.group(3)
        classes = [c.strip() for c in m.group(4).split(",") if c.strip()]
        cur = {"name": pending_name, "level": level, "school": school, "classes": classes,
               "casting_time": "", "range": "", "components": "", "duration": "", "body": []}
        spells.append(cur)
        pending_name = None
        state = "labels"
        continue
    pending_name = None if kind != "name" else pending_name
    if cur is None:
        continue
    if kind == "chapter":
        if cur["name"] == "Zone of Truth":
            break
        continue
    if kind == "label":
        label = spans[0]["text"].strip()
        value = "".join(s["text"] for s in spans[1:]).strip()
        key = {"Casting Time:": "casting_time", "Range:": "range", "Components:": "components", "Component:": "components", "Duration:": "duration"}[label]
        cur[key] = value
        state = key
        continue
    # continuation of a wrapped label value (still GillSans 9.5, before the Cambria body starts)
    if state in ("casting_time", "range", "components", "duration") and spans and (
            cur[state] == "" or cur[state].count("(") > cur[state].count(")") or
            ("Cambria" not in spans[0]["font"] and "SemiBold" not in spans[0]["font"] and "Bold" not in spans[0]["font"])):
        if cur[state].endswith("-") and t[:1].islower():
            cur[state] = cur[state][:-1] + t  # a word broken at the end of the line ("immedi-" "ately")
        else:
            cur[state] = (cur[state] + " " + t).strip()
        continue
    state = "body"
    f0_ = spans[0]["font"] if spans else ""
    if kind == "stat":
        cur.setdefault("stat", []).append((t, [(s["font"], round(s["size"], 1), s["text"]) for s in spans], bbox[0], bbox[1]))
        continue
    if kind in ("text", "italic") and "GillSans" in f0_ and spans[0]["size"] <= 10.6 and t:
        # a table line: title (SemiBold 10.5), header (SemiBold 9.2), cells (GillSans 9.5)
        cur["body"].append(("T", bbox[0], bbox[1], colno, t, round(spans[0]["size"], 1), "SemiBold" in f0_))
        continue
    # paragraph starts are marked by a tab
    cur["body"].append(("\t" if text.startswith("\t") or text.startswith(" \t") else "") + t + ("" if t else ""))


def blocks_of(lines):
    return _blocks_of(lines, join_body)


def join_body(lines):
    out = []
    for raw in lines:
        starts = raw.startswith("\t")
        t = raw.strip()
        if not t:
            continue
        if out and out[-1].endswith("-") and not starts and t[:1].islower():
            out[-1] = out[-1][:-1] + t
        elif starts or not out:
            out.append(t)
        else:
            out[-1] = out[-1] + " " + t
    return out


ABIL = {"Strength": "STR", "Dexterity": "DEX", "Constitution": "CON", "Intelligence": "INT", "Wisdom": "WIS", "Charisma": "CHA"}
TYPES = "Acid|Bludgeoning|Cold|Fire|Force|Lightning|Necrotic|Piercing|Poison|Psychic|Radiant|Slashing|Thunder"
ORD = {1: "st", 2: "nd", 3: "rd"}


def slug(name):
    n = name.lower().replace("’", "").replace("'", "")
    return re.sub(r"[^a-z0-9]+", "-", n).strip("-")


def add_dice(base, inc, times):
    """base and inc like '8d6' and '1d6'; returns base plus times*inc when the die size matches."""
    bm, im = re.match(r"(\d+)d(\d+)$", base), re.match(r"(\d+)d(\d+)$", inc)
    if not bm or not im or bm.group(2) != im.group(2):
        return None
    return "%dd%s" % (int(bm.group(1)) + int(im.group(1)) * times, bm.group(2))


result = []
for sp in spells:
    blocks = blocks_of(sp["body"])
    higher, upgrade, main, main_blocks = "", "", [], []
    for blk in blocks:
        if isinstance(blk, dict):
            main_blocks.append(blk)
            main.extend(flat_blocks([blk]))
            continue
        para = blk
        if para.startswith("Using a Higher-Level Spell Slot"):
            higher = re.sub(r"^Using a Higher-Level Spell Slot\.?\s*", "", para)
        elif para.startswith("Cantrip Upgrade"):
            upgrade = re.sub(r"^Cantrip Upgrade\.?\s*", "", para)
        else:
            main.append(para)
            main_blocks.append(para)
    desc = "\n".join(main)
    statblock = build_statblock(sp["stat"]) if sp.get("stat") else None
    full = desc + " " + higher + " " + upgrade
    s = {"slug": slug(sp["name"]), "name": sp["name"], "level": sp["level"], "school": sp["school"], "classes": sp["classes"],
         "casting_time": sp["casting_time"], "range": sp["range"], "components": sp["components"], "duration": sp["duration"],
         "description": desc, "description_blocks": main_blocks, "statblock": statblock, "higher_level": higher or upgrade, "ritual": "Ritual" in sp["casting_time"],
         "concentration": sp["duration"].startswith("Concentration")}
    comp = sp["components"]
    s["components_short"] = ", ".join(c for c in ("V", "S", "M") if re.search(r"\b%s\b" % c, comp.split("(")[0]))
    mm = re.search(r"\((.*)\)", comp)
    material = mm.group(1) if mm else ""
    s["material"] = material
    cost = re.search(r"(\d[\d,]*)\+?\s*GP", material)
    s["material_cost"] = int(cost.group(1).replace(",", "")) if cost else 0
    s["material_consumed"] = bool(re.search(r"consum", material, re.I)) or bool(re.search(r"spell consumes", desc, re.I))
    sv = re.search(r"\b(Strength|Dexterity|Constitution|Intelligence|Wisdom|Charisma) saving throw", full)
    s["saving_throw"] = ABIL[sv.group(1)] if sv else ""
    lm = re.search(r"(\d+)-foot-long[^.]*?\bLine\b", sp["range"] + " " + desc)
    area = re.search(r"(\d+)-foot(?:-radius| radius)?\s+(?:-?\w+\s+)?(Cone|Cube|Cylinder|Emanation|Line|Sphere)\b", sp["range"] + " " + desc)
    s["aoe_shape"] = "line" if lm else (area.group(2).lower() if area else "")
    s["aoe_size"] = int(lm.group(1)) if lm else (int(area.group(1)) if area else 0)
    # damage
    cands = []
    for pat, di, ti in ((r"(\d+d\d+)(?:\s*\+\s*\d+)?\s+(%s) damage" % TYPES, 1, 2),
                        (r"(%s) damage equal to (\d+d\d+)" % TYPES, 2, 1),
                        (r"base damage is (\d+d\d+)", 1, 0),
                        (r"(\d+d\d+) damage", 1, 0),
                        (r"extra (\d+d\d+)", 1, 0)):
        mm_ = re.search(pat, desc)
        if mm_:
            cands.append((mm_.start(), mm_.group(di), (mm_.group(ti).lower() if ti else "")))
    if cands:
        cands.sort()
        _, dice_, type_ = cands[0]
        if not type_:
            tm = re.search(r"(%s) damage" % TYPES, desc)
            type_ = tm.group(1).lower() if tm else ""
        s["damage_dice"], s["damage_type"] = dice_, type_
    else:
        s["damage_dice"], s["damage_type"] = "", ""
    s["damage_at_slot_level"], s["damage_at_character_level"], s["heal_at_slot_level"] = {}, {}, {}
    # healing
    hm = re.search(r"(?:regains?|restores?)[^.]*?(\d+d\d+)(?: plus| \+) your spellcasting (?:ability )?modifier", desc) or \
        re.search(r"(?:regains?|restores?)[^.]*?(\d+d\d+)", desc)
    s["healing"] = hm.group(1) if hm else ""
    fm = re.search(r"(?:restoring|restores?|regains?)\s+(\d+)\s+Hit Points", desc)
    if fm and not hm:
        s["healing"] = fm.group(1)
    s["heal_uses_modifier"] = bool(hm and re.search(r"spellcasting (?:ability )?modifier", desc[hm.start():hm.end() + 60]))
    # cantrip scaling: "increases by 1d10 when you reach levels 5 (2d10), 11 (3d10), and 17 (4d10)"
    if sp["level"] == 0 and s["damage_dice"]:
        pairs = re.findall(r"(\d+)\s*\((\d+d\d+)\)", upgrade)
        if pairs:
            s["damage_at_character_level"] = {"1": s["damage_dice"]}
            for lvl, dice in pairs:
                s["damage_at_character_level"][lvl] = dice
    # slot scaling
    inc = re.search(r"(damage|healing)[^.]*?increases? by (\d+d\d+)[^.]*?(?:slot level|spell slot level|level) above (\d+)", higher)
    if inc and sp["level"] > 0:
        kind, step, above = inc.group(1), inc.group(2), int(inc.group(3))
        base = s["damage_dice"] if kind == "damage" else s["healing"]
        if base:
            table = {}
            for slot in range(sp["level"], 10):
                d = add_dice(base, step, slot - above)
                if d:
                    table[str(slot)] = d + (" + MOD" if kind == "healing" and s["heal_uses_modifier"] else "")
            if kind == "damage":
                s["damage_at_slot_level"] = table
            else:
                s["heal_at_slot_level"] = table
    flat = re.search(r"healing increases by (\d+) for each spell slot level above (\d+)", higher)
    if flat and s["healing"].isdigit():
        s["heal_at_slot_level"] = {str(sl): str(int(s["healing"]) + int(flat.group(1)) * (sl - int(flat.group(2)))) for sl in range(sp["level"], 10)}
    result.append(s)

# The SRD PDF has a few flaws. Soft hyphens are dropped, and two typos are corrected (the extra "s" in Animal Friendship, the
# missing "a" in Freezing Sphere). The text is otherwise the SRD's, word for word: wording from other books (the Player's
# Handbook's fuller Telekinesis sentence, its Animal Shapes rule, Flaming Sphere's school, Conjure Minor Elementals' 2d8) is not
# part of this data; a private server can add it as local content (AGENTRPG_LOCAL_CONTENT, spells.json).
for r_ in result:
    for k_ in ("description", "higher_level", "material", "components", "casting_time", "range", "duration"):
        r_[k_] = r_[k_].replace("\u00ad ", "").replace("\u00ad", "")
    r_["description_blocks"] = [b_.replace("\u00ad ", "").replace("\u00ad", "") if isinstance(b_, str) else b_ for b_ in r_["description_blocks"]]
FIXES = {
    "animal-friendship": ("the spells ends.", "the spell ends."),
    "freezing-sphere": ("10d6 Cold damage on failed save", "10d6 Cold damage on a failed save"),
}
for r_ in result:
    if r_["slug"] in FIXES:
        old_, new_ = FIXES[r_["slug"]]
        assert old_ in r_["description"], r_["slug"]
        r_["description"] = r_["description"].replace(old_, new_)
        r_["description_blocks"] = [b_.replace(old_, new_) if isinstance(b_, str) else b_ for b_ in r_["description_blocks"]]

# spells whose damage comes in several missiles: total dice per slot level
by = {r["slug"]: r for r in result}
if "magic-missile" in by:  # three darts, each 1d4 + 1 Force; one more dart per slot level above 1
    m = by["magic-missile"]
    m["damage_dice"], m["damage_type"] = "3d4+3", "force"
    m["damage_at_slot_level"] = {str(s_): "%dd4+%d" % (s_ + 2, s_ + 2) for s_ in range(1, 10)}
if "scorching-ray" in by:  # three rays, each 2d6 Fire; one more ray per slot level above 2
    m = by["scorching-ray"]
    m["damage_dice"], m["damage_type"] = "6d6", "fire"
    m["damage_at_slot_level"] = {str(s_): "%dd6" % (2 * (s_ + 1)) for s_ in range(2, 10)}
if "eldritch-blast" in by:  # one beam, more at levels 5, 11 and 17: 1d10 each
    m = by["eldritch-blast"]
    m["damage_dice"], m["damage_type"] = "1d10", "force"
    m["damage_at_character_level"] = {"1": "1d10", "5": "2d10", "11": "3d10", "17": "4d10"}

if "aid" in by:  # each target's Hit Point maximum and current Hit Points increase by 5, plus 5 per slot level above 2
    by["aid"]["healing"] = "5"
    by["aid"]["heal_at_slot_level"] = {str(s_): str(5 * (s_ - 1)) for s_ in range(2, 10)}
if "glyph-of-warding" in by:  # explosive rune: 5d8 damage of a type you choose, plus 1d8 per slot level above 3
    by["glyph-of-warding"]["damage_dice"] = "5d8"
    by["glyph-of-warding"]["damage_at_slot_level"] = {str(s_): "%dd8" % (5 + s_ - 3) for s_ in range(3, 10)}
if "shillelagh" in by:  # the weapon's damage die becomes d8, d10 at level 5, d12 at 11, 2d6 at 17
    by["shillelagh"]["damage_dice"] = "1d8"
    by["shillelagh"]["damage_at_character_level"] = {"1": "1d8", "5": "1d10", "11": "1d12", "17": "2d6"}
# damage or healing with a flat bonus ("10d6 + 40 Force damage"): the general pattern above keeps only the dice
if "disintegrate" in by:  # 10d6 + 40 Force; 3d6 more per slot level above 6
    by["disintegrate"]["damage_dice"] = "10d6+40"
    by["disintegrate"]["damage_at_slot_level"] = {str(s_): "%dd6+40" % (10 + 3 * (s_ - 6)) for s_ in range(6, 10)}
if "finger-of-death" in by:  # 7d8 + 30 Necrotic
    by["finger-of-death"]["damage_dice"] = "7d8+30"
if "regenerate" in by:  # the target regains 4d8 + 15 Hit Points
    by["regenerate"]["healing"] = "4d8+15"
if "true-strike" in by:  # extra Radiant damage at levels 5, 11 and 17
    by["true-strike"]["damage_type"] = "radiant"
    by["true-strike"]["damage_at_character_level"] = {"5": "1d6", "11": "2d6", "17": "3d6"}


# Spells that deal damage in more than one part (two types, or a second part later). Each part: name, dice, type,
# timing (on_cast, on_hit, end_of_next_turn, ...), save and on_save (half or none) and at_slot_level where it scales.
# Checked against the spell text (see the changelog of 2026-11-06). The first part is the one in damage_dice.
def tbl(base, inc, base_level, extra=""):
    return {str(s_): add_dice(base, inc, s_ - base_level) + extra for s_ in range(base_level, 10)}


def part(name, dice, dtype, timing="on_cast", save="", on_save="", at_slot=None, note=""):
    d_ = {"name": name, "dice": dice, "type": dtype, "timing": timing}
    if save:
        d_["save"] = save
        d_["on_save"] = on_save
    if at_slot:
        d_["at_slot_level"] = at_slot
    if note:
        d_["note"] = note
    return d_


PARTS = {
    "acid-arrow": [part("Initial damage", "4d4", "acid", "on_hit", at_slot=tbl("4d4", "1d4", 2), note="half as much on a miss"),
                   part("Later damage", "2d4", "acid", "end_of_next_turn", at_slot=tbl("2d4", "1d4", 2))],
    "flame-strike": [part("Fire", "5d6", "fire", save="DEX", on_save="half", at_slot=tbl("5d6", "1d6", 5)),
                     part("Radiant", "5d6", "radiant", save="DEX", on_save="half", at_slot=tbl("5d6", "1d6", 5))],
    "ice-knife": [part("On a hit", "1d10", "piercing", "on_hit"),
                  part("Explosion", "2d6", "cold", save="DEX", on_save="none", at_slot=tbl("2d6", "1d6", 1), note="hit or miss; the target and each creature within 5 feet of it")],
    "ice-storm": [part("Bludgeoning", "2d10", "bludgeoning", save="DEX", on_save="half", at_slot=tbl("2d10", "1d10", 4)),
                  part("Cold", "4d6", "cold", save="DEX", on_save="half")],
    "meteor-swarm": [part("Fire", "20d6", "fire", save="DEX", on_save="half", note="each of four meteors"),
                     part("Bludgeoning", "20d6", "bludgeoning", save="DEX", on_save="half", note="each of four meteors")],
    "vitriolic-sphere": [part("Initial damage", "10d4", "acid", save="DEX", on_save="half", at_slot=tbl("10d4", "2d4", 4)),
                         part("Later damage", "5d4", "acid", "end_of_next_turn", save="DEX", on_save="none", note="only on a failed save")],
    "wall-of-ice": [part("When the wall appears", "10d6", "cold", save="DEX", on_save="half", at_slot=tbl("10d6", "2d6", 6)),
                    part("Passing through the frigid air", "5d6", "cold", "when_entering", save="CON", on_save="half", at_slot=tbl("5d6", "1d6", 6))],
    "weird": [part("Initial damage", "10d10", "psychic", save="WIS", on_save="half"),
              part("Each of its turns", "5d10", "psychic", "end_of_each_turn", save="WIS", on_save="none", note="only on a failed save")],
    "tsunami": [part("When the wall appears", "6d10", "bludgeoning", save="STR", on_save="half"),
                part("Inside the wall or in its path", "5d10", "bludgeoning", "each_round", save="STR", on_save="none", note="once per round; reduced by 1d10 on each later round")],
    "wall-of-thorns": [part("When the wall appears", "7d8", "piercing", save="DEX", on_save="half", at_slot=tbl("7d8", "1d8", 6)),
                       part("Entering the wall or ending a turn in it", "7d8", "slashing", "when_entering", save="DEX", on_save="half", at_slot=tbl("7d8", "1d8", 6))],
    "storm-of-vengeance": [part("Turn 1", "2d6", "thunder", save="CON", on_save="none"),
                           part("Turn 2: acidic rain", "4d6", "acid", "turn_2"),
                           part("Turn 3: six bolts of lightning", "10d6", "lightning", "turn_3", save="DEX", on_save="half"),
                           part("Turn 4: hailstones", "2d6", "bludgeoning", "turn_4"),
                           part("Turns 5-10: freezing rain", "1d6", "cold", "turns_5_to_10")],
    "animate-objects": [part("Slam, Medium or smaller", "1d4+3", "force", "on_hit", at_slot={str(s_): add_dice("1d4", "1d4", s_ - 5) + "+3" for s_ in range(5, 10)}),
                        part("Slam, Large", "2d6+3", "force", "on_hit", at_slot={str(s_): add_dice("2d6", "1d6", s_ - 5) + "+3" for s_ in range(5, 10)}, note="plus your spellcasting ability modifier"),
                        part("Slam, Huge", "2d12+3", "force", "on_hit", at_slot={str(s_): add_dice("2d12", "1d12", s_ - 5) + "+3" for s_ in range(5, 10)}, note="plus your spellcasting ability modifier")],
}
for r_ in result:
    r_["damage_parts"] = PARTS.get(r_["slug"], [])
    if r_["damage_parts"] and r_["slug"] != "animate-objects":
        first = r_["damage_parts"][0]
        assert first["dice"] == r_["damage_dice"] and first["type"] == r_["damage_type"], (r_["slug"], first["dice"], r_["damage_dice"], first["type"], r_["damage_type"])

json.dump(result, open(OUT, "w", encoding="utf-8"), indent=1, ensure_ascii=False)
print(len(result), "spells", file=sys.stderr)
