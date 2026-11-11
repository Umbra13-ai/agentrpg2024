"""Build cmd/server/data/monster_lore.json (slug -> {description, lore}) from the lore parts and check every SRD monster has an entry."""
import json, re, sys, os
sys.path.insert(0, os.path.dirname(__file__))
import part1, part2, part3, part4

root = r"<workspace>\agentrpg-src"
mons = json.load(open(root + r"\cmd\server\data\srd521_monsters.json", encoding="utf-8"))
lore = {}
for p in (part1, part2, part3):
    lore.update(p.LORE)
for k, (a, l) in part4.ANIMALS.items():
    lore[k] = (a, l)
for color, (look, tale) in part4.DRAGONS.items():
    for age, intro in part4.AGES.items():
        name = f"{color} Dragon Wyrmling" if age == "Wyrmling" else f"{age} {color} Dragon"
        lore[name] = (intro + look + ".", part4.AGE_LORE[age] + tale)


def slug(n):
    s = n.lower().replace("'", "").replace("\u2019", "")
    return re.sub(r"[^a-z0-9]+", "-", s).strip("-")


out, missing = {}, []
for m in mons:
    e = lore.get(m["name"])
    if not e or not e[0] or not e[1]:
        missing.append(m["name"])
        continue
    out[slug(m["name"])] = {"description": e[0], "lore": e[1]}
print(len(out), "entries;", len(missing), "missing:", missing)
extra = [k for k in lore if k not in {m["name"] for m in mons}]
print("not in SRD:", extra)
if not missing:
    json.dump(out, open(root + r"\cmd\server\data\monster_lore.json", "w", encoding="utf-8"), ensure_ascii=False, indent=1)
