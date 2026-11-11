"""Extract the Armor section (rules text and table) of SRD 5.2.1 (CC-BY-4.0), page 92, into armor_srd.json."""
import json
import re
import warnings

warnings.filterwarnings("ignore")
import pymupdf

PDF = r"<workspace>\agentrpg-reference\SRD_CC_v5.2.1.pdf"
OUT = r"<workspace>\agentrpg-2024-research\items\armor_srd.json"
doc = pymupdf.open(PDF)
page = None
for p in doc:
    t = p.get_text()
    if "Padded Armor" in t and "Chain Mail" in t and "Armor Training" in t:
        page = p
        break
lines = []
for b in page.get_text("dict")["blocks"]:
    if b.get("type") != 0:
        continue
    for l in b["lines"]:
        if l["bbox"][1] > 735:
            continue
        lines.append((l["bbox"][0], l["bbox"][1], l["spans"], "".join(s["text"] for s in l["spans"])))

# ---- rules text: the two text columns above the table (y < 315)
rules = []
cur = None


def add_text(txt):
    global cur
    txt = txt.strip()
    if not cur:
        return
    if cur["text"].endswith("-") and txt[:1].islower():
        cur["text"] = cur["text"][:-1] + txt
    else:
        cur["text"] = (cur["text"] + " " + txt).strip()


for col in (0, 1):
    for x, y, spans, text in [l for l in lines if l[1] < 315 and (l[0] < 300) == (col == 0) and l[1] > 30]:
        f0, size0 = spans[0]["font"], spans[0]["size"]
        if "SemiBold" in f0 and size0 > 9.9:
            if text.strip() == "Armor":
                continue
            cur = {"title": text.strip(), "text": ""}
            rules.append(cur)
        elif "Cambria-Bold" in f0 and len(spans) > 1:
            cur = {"title": spans[0]["text"].strip().rstrip("."), "text": "", "run_in": True}
            rules.append(cur)
            add_text("".join(s["text"] for s in spans[1:]))
        else:
            if cur is None:
                cur = {"title": "", "text": ""}
                rules.append(cur)
            add_text(text)

# ---- table rows
cols = [("name", 0, 150), ("ac", 150, 300), ("strength", 300, 370), ("stealth", 370, 440), ("weight", 440, 497), ("cost", 497, 700)]
rows = [l for l in lines if l[1] > 345]
groups = []
rowmap = {}
for x, y, spans, text in sorted(rows, key=lambda r: (round(r[1]), r[0])):
    key = round(y / 4)
    rowmap.setdefault(key, []).append((x, spans, text))
for key in sorted(rowmap):
    items = rowmap[key]
    first = items[0]
    if "Italic" in first[1][0]["font"] and len(items) == 1:
        title = first[2].strip()
        m = re.match(r"^(.*?)\s*\((.*)\)$", title)
        groups.append({"name": m.group(1) if m else title, "note": m.group(2) if m else "", "armor": []})
        continue
    rec = {c: "" for c, _, _ in cols}
    for x, spans, text in items:
        for c, lo, hi in cols:
            if lo <= x < hi:
                rec[c] = (rec[c] + " " + text.strip()).strip()
    if rec["name"] and groups:
        groups[-1]["armor"].append(rec)

json.dump({"rules": rules, "groups": groups}, open(OUT, "w", encoding="utf-8"), indent=1, ensure_ascii=False)
print(len(rules), "rule blocks;", sum(len(g["armor"]) for g in groups), "armors in", len(groups), "groups")
for g in groups:
    print(g["name"], "|", g["note"], "|", [a["name"] for a in g["armor"]])
for r in rules:
    print("##", r["title"], "::", r["text"][:90])
