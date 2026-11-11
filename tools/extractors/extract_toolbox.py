"""Extracts the SRD 5.2.1 "Gameplay Toolbox" items the GM commands use (contagions, environmental effects, fear and mental
stress, poisons, traps) into cmd/server/data/srd521_toolbox.json, word for word.

Run:  pdftotext -enc UTF-8 -f 193 -l 202 <SRD_CC_v5.2.1.pdf> tool.txt
      python tools/extractors/extract_toolbox.py tool.txt
Each item keeps its paragraphs; short table cells are left out (the tables are coded in cmd/server/toolbox_2024.go).
"""
import json
import os
import re
import sys

ROOT = os.path.dirname(os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
OUT = os.path.join(ROOT, "cmd", "server", "data", "srd521_toolbox.json")

SECTIONS = {
    "contagions": ["Cackle Fever", "Sewer Plague", "Sight Rot"],
    "environment": ["Deep Water", "Extreme Cold", "Extreme Heat", "Frigid Water", "Heavy Precipitation", "High Altitude",
                    "Slippery Ice", "Strong Wind", "Thin Ice"],
    "fear": ["Fear Effects", "Mental Stress Effects", "Prolonged Effects"],
    "poisons": ["Assassin's Blood", "Burnt Othur Fumes", "Crawler Mucus", "Essence of Ether", "Malice", "Midnight Tears",
                "Oil of Taggit", "Pale Tincture", "Purple Worm Poison", "Serpent Venom", "Spider's Sting", "Torpor",
                "Truth Serum", "Wyvern Poison"],
    "traps": ["Collapsing Roof", "Falling Net", "Fire-Casting Statue", "Hidden Pit", "Poisoned Darts", "Poisoned Needle",
              "Rolling Stone", "Spiked Pit"],
}
STOP = {"Combat Encounters", "Poison", "Traps", "Fear and Mental Stress", "Environmental Effects", "Sample Fear DCs",
        "Sample Mental Stress Effects", "Purchasing Poison", "Harvesting Poison", "Sample Poisons"}


def slug(name):
    return re.sub(r"[^a-z0-9]+", "_", name.lower().replace("'", "")).strip("_")


def main(path):
    lines = [l.rstrip("\n").replace("’", "'").replace("--", "—") for l in open(path, encoding="utf-8")]
    heads = {}
    for i, l in enumerate(lines):
        name = re.sub(r"\s*\([\d,]+ GP\)$", "", l.strip())
        heads.setdefault(name, i)
    all_names = {n for ns in SECTIONS.values() for n in ns} | STOP
    out = {}
    for sec, names in SECTIONS.items():
        out[sec] = {}
        for n in names:
            start = heads[n]
            m = re.search(r"\(([\d,]+) GP\)", lines[start])
            item = {"name": n, "paragraphs": []}
            if m:
                item["price_gp"] = int(m.group(1).replace(",", ""))
            j = start + 1
            while j < len(lines):
                t = lines[j].strip()
                bare = re.sub(r"\s*\([\d,]+ GP\)$", "", t)
                if bare in all_names and j != start:
                    break
                if re.match(r"^\d+\s*(System Reference Document.*)?$", t) or t.startswith("System Reference Document"):
                    j += 1
                    continue
                if re.match(r"^(Magical Contagion|Contact Poison|Ingested Poison|Inhaled Poison|Injury Poison)$", t):
                    item["kind"] = t
                elif re.match(r"^(Deadly|Nuisance) Trap", t):
                    item["severity"] = t
                elif len(t) > 55 or t.startswith("Trigger:") or t.startswith("Duration:"):
                    item["paragraphs"].append(t)
                j += 1
            out[sec][slug(n)] = item
    os.makedirs(os.path.dirname(OUT), exist_ok=True)
    json.dump(out, open(OUT, "w", encoding="utf-8"), indent=1, ensure_ascii=False)
    print({k: len(v) for k, v in out.items()})


if __name__ == "__main__":
    main(sys.argv[1])
