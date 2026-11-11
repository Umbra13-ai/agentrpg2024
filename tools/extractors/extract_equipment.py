"""Extract Tools, Adventuring Gear and Mounts and Vehicles of the SRD 5.2.1 (CC-BY-4.0) verbatim into equipment_srd.json.

Page 93 to 101 of the SRD PDF. Structure (fonts): chapter 18 pt, subsection 14 pt, item name 12 pt
(SemiBold), labels (Ability:, Weight:, Utilize:, Craft:) SemiBold 9.5 pt, table text GillSans 9.5 pt,
body Cambria 10 pt. Two columns per page.
"""
import json
import re
import sys
import warnings

warnings.filterwarnings("ignore")
import pymupdf

PDF = r"<workspace>\agentrpg-reference\SRD_CC_v5.2.1.pdf"
OUT = r"<workspace>\agentrpg-2024-research\items\equipment_srd.json"
doc = pymupdf.open(PDF)

LABELS = ("Ability:", "Weight:", "Utilize:", "Craft:")
rows = []  # reading-order lines
for pno in range(92, 101):
    p = doc[pno]
    cols = {0: [], 1: []}
    for b in p.get_text("dict")["blocks"]:
        if b.get("type") != 0:
            continue
        for l in b["lines"]:
            if l["bbox"][1] > 735:
                continue
            spans = l["spans"]
            f0, size0 = spans[0]["font"], round(spans[0]["size"], 1)
            text = "".join(s["text"] for s in spans)
            if "SemiBold" in f0 and size0 >= 17:
                kind = "chapter"
            elif "SemiBold" in f0 and size0 >= 13:
                kind = "section"
            elif "SemiBold" in f0 and size0 >= 11.5:
                kind = "name"
            elif "SemiBold" in f0 and any(text.strip().startswith(x) for x in LABELS):
                kind = "label"
            elif "GillSans" in f0 or "SemiBold" in f0:
                kind = "table"
            else:
                kind = "body"
            cols[0 if l["bbox"][0] < 300 else 1].append({"kind": kind, "text": text, "x": l["bbox"][0], "y": l["bbox"][1], "pno": pno + 1, "spans": spans, "col": 0 if l["bbox"][0] < 300 else 1})
    if pno + 1 == 95:
        continue  # the Adventuring Gear price table; every cost is also in the item headings
    for c in (0, 1):
        rows.extend(cols[c])


def clean(t):
    return t.replace("\t", " ").replace("\u00a0", " ").strip()


def join_lines(lines):
    out = ""
    for ln in lines:
        ln = clean(ln)
        if not ln:
            continue
        if out.endswith("-") and ln[:1].islower():
            out = out[:-1] + ln
        else:
            out = (out + " " + ln).strip()
    return out


result = {"tools": {"intro": [], "sections": [], "items": []}, "gear": {"items": []}, "mounts_vehicles": {"sections": []}}
chapter = None
section = None
item = None
label = None
para = []
table_rows = []


def flush_para(target):
    global para
    if para and target is not None:
        txt = join_lines(para)
        if txt:
            target.append(txt)
    para = []


def flush_table(target):
    global table_rows
    if table_rows and target is not None:
        grouped = {}
        for col, y, x, t in table_rows:
            grouped.setdefault((col, round(y / 4)), []).append((x, t))
        rows_ = [[clean(t) for x, t in sorted(cells)] for _, cells in sorted(grouped.items())]
        current = None
        for row in rows_:
            if len(row) == 1:  # a title row starts a new table
                current = {"title": row[0], "rows": []}
                target.append({"table": current})
            else:
                if current is None:
                    current = {"title": "", "rows": []}
                    target.append({"table": current})
                current["rows"].append(row)
    table_rows = []


def split_name(name):
    m = re.match(r"^(.*?)\s*\(([^()]*(?:\([^)]*\))?[^()]*)\)\s*$", name)
    if m:
        return m.group(1).strip(), m.group(2).strip()
    return name.strip(), ""


pending_name = None
for r in rows:
    k, t = r["kind"], r["text"]
    if k == "chapter":
        tgt_ = item["text"] if item is not None else (section["text"] if section is not None else result.setdefault("intros", {}).setdefault(chapter or "", []))
        flush_para(tgt_)
        flush_table(tgt_)
        chapter = clean(t)
        section = item = None
        continue
    if chapter not in ("Tools", "Adventuring Gear", "Mounts and Vehicles"):
        continue

    target = None
    if item is not None:
        target = item["text"]
    elif section is not None:
        target = section["text"]
    elif chapter == "Tools":
        target = result["tools"]["intro"]

    if k == "section":
        if chapter == "Tools" or chapter == "Mounts and Vehicles" or chapter == "Adventuring Gear":
            if item is not None:
                flush_para(item["text"]); flush_table(item["text"])
            elif section is not None:
                flush_para(section["text"]); flush_table(section["text"])
            else:
                tgt0 = target if target is not None else result.setdefault("intros", {}).setdefault(chapter or "", [])
                flush_para(tgt0); flush_table(tgt0)
            item = None
            section = {"title": clean(t), "text": []}
            if chapter == "Tools":
                result["tools"]["sections"].append(section)
            elif chapter == "Mounts and Vehicles":
                result["mounts_vehicles"]["sections"].append(section)
        continue
    if k == "name":
        if pending_name is not None:  # the name wrapped onto a second line
            pending_name["name"] += " " + clean(t)
            continue
        if item is not None:
            flush_para(item["text"]); flush_table(item["text"])
        elif section is not None:
            flush_para(section["text"]); flush_table(section["text"])
        else:
            intro_ = result.setdefault("intros", {}).setdefault(chapter or "", [])
            flush_para(intro_); flush_table(intro_)
        name, cost = split_name(clean(t))
        if chapter == "Mounts and Vehicles":
            # 12 pt headings in this chapter are sub-sections (Speed, Crew, ...), not items
            section = {"title": clean(t), "text": [], "level": 3}
            result["mounts_vehicles"]["sections"].append(section)
            item = None
            continue
        item = {"name": name, "cost": cost, "category": section["title"] if (section and chapter == "Tools") else "", "text": []}
        if chapter == "Tools":
            result["tools"]["items"].append(item)
        else:
            result["gear"]["items"].append(item)
        pending_name = item if (t.rstrip().endswith(";") or t.count("(") > t.count(")")) else None
        label = None
        continue
    pending_name = None
    if k == "label" and item is not None and chapter == "Tools":
        flush_para(item["text"]); flush_table(item["text"])
        # a line may hold two labels at the same height (Ability and Weight)
        txt = clean(t)
        for lab in LABELS:
            if txt.startswith(lab):
                key = lab.rstrip(":").lower()
                item[key] = txt[len(lab):].strip()
                label = key
        continue
    if k == "table" and label in ("utilize", "craft") and item is not None and chapter == "Tools":
        item[label] = (item[label] + " " + clean(t)).strip() if not item[label].endswith("-") else item[label][:-1] + clean(t)
        continue
    if k == "table":
        flush_para(target)
        table_rows.append((r["col"], r["y"], r["x"], t))
        continue
    # body text
    flush_table(target)
    if t.startswith("\t") or t.startswith(" \t"):
        flush_para(target)
    para.append(t)

# flush what is left
for tgt in ([item["text"]] if item else []) + ([section["text"]] if section else []):
    flush_para(tgt); flush_table(tgt)

for it in result["gear"]["items"] + result["tools"]["items"]:
    if "(" in it["name"] and not it["cost"]:
        m = re.match(r"^(.*?)\s*\((.*)\)\s*$", it["name"])
        if m:
            it["name"], it["cost"] = m.group(1).strip(), m.group(2).strip()
for it in result["tools"]["items"]:
    u = it.get("utilize", "")
    if " Variants: " in u:
        base, var = u.split(" Variants: ", 1)
        it["utilize"] = base.strip()
        it["variants"] = [v.strip() for v in re.split(r"\),\s*", var.rstrip(".")) if v.strip()]
        it["variants"] = [v if v.endswith(")") else v + ")" for v in it["variants"]]

json.dump(result, open(OUT, "w", encoding="utf-8"), indent=1, ensure_ascii=False)
print("tools:", len(result["tools"]["items"]), "gear:", len(result["gear"]["items"]), "mount sections:", len(result["mounts_vehicles"]["sections"]), file=sys.stderr)
