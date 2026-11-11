"""Extract the Magic Items A-Z section of SRD 5.2.1 (CC-BY-4.0) from the PDF into JSON.

Item names are GillSans-SemiBold 12 pt, the type line is Cambria-Italic, the
body is Cambria. Two columns per page (left x < 300, right x >= 300).
Output: magic_items.json (list of {name, type, rarity, rarity_text, requires_attunement, attunement, description}).
"""
import json
import os
import re
import sys
import warnings

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from srd_tables import blocks_of, flat_blocks, build_statblock

warnings.filterwarnings("ignore")
import pymupdf

PDF = r"<workspace>\agentrpg-reference\SRD_CC_v5.2.1.pdf"
OUT = sys.argv[1] if len(sys.argv) > 1 else r"<workspace>\agentrpg-2024-research\items\magic_items.json"

doc = pymupdf.open(PDF)

# find the page range
first = last = None
for i, p in enumerate(doc):
    t = p.get_text()
    if first is None and "Magic items are presented in alphabetical order" in t:
        first = i
    if first is not None and i > first and re.search(r"^\s*Monsters\s*$", t, re.M) and "Stat Block" in t[:3000]:
        last = i
        break
last = last or first + 50
print("pages", first + 1, "to", last, file=sys.stderr)

lines = []  # (kind, text)
for i in range(first, last):
    p = doc[i]
    cols = {0: [], 1: []}
    for b in p.get_text("dict")["blocks"]:
        if b.get("type") != 0:
            continue
        for l in b["lines"]:
            y = l["bbox"][1]
            if y > 735:  # the page footer; nothing sits above the text, and an item can start at the very top of a column
                continue
            col = 0 if l["bbox"][0] < 300 else 1
            spans = [s for s in l["spans"] if s["text"].strip()]
            if not spans:
                continue
            text = "".join(s["text"] for s in l["spans"]).rstrip()
            f = spans[0]["font"]
            size = spans[0]["size"]
            fonts_all = " ".join(s["font"] for s in l["spans"])
            if "Optima" in fonts_all or abs(size - 14.8) < 0.3 or abs(size - 9.8) < 0.15 or abs(size - 6.0) < 0.15 \
                    or (f.endswith("GillSans") and abs(size - 12.0) < 0.3 and text.strip() in ("Traits", "Actions", "Bonus Actions", "Reactions", "Legendary Actions")):
                kind = "stat"  # a stat block (Giant Fly, Avatar of Death)
            elif "SemiBold" in f and abs(size - 12.0) < 0.3:
                kind = "name"
            elif "GillSans" in f and size <= 10.6 and text.strip():
                kind = "table"  # title (SemiBold 10.5), header (SemiBold 9.2) and cells (GillSans 9.5)
            elif "Italic" in f and "Bold" not in f:
                kind = "italic"
            else:
                kind = "body"
            cols[col].append((y, l["bbox"][0], kind, text, round(size, 1), "SemiBold" in f, col, [(s["font"], round(s["size"], 1), s["text"]) for s in l["spans"]]))
    # a table as wide as the page: the cells right of x = 300 were read as the second column; put them back
    # beside the rest of the table (same header height, no title and no body text of the second column nearby)
    c0, c1 = cols[0], cols[1]
    i = 0
    while i < len(c1):
        head = c1[i]
        k0 = [k for k, e in enumerate(c0) if e[2] == "table" and e[5] and e[4] < 9.4 and abs(e[0] - head[0]) <= 3]
        if head[2] == "table" and head[5] and head[4] < 9.4 and k0:
            j = i + 1  # the right half runs until the next title or header
            while j < len(c1) and c1[j][2] == "table" and not (c1[j][5] and (c1[j][4] < 9.4 or c1[j][4] >= 10.4)):
                j += 1
            k = k0[0]
            while k + 1 < len(c0) and c0[k + 1][2] == "table":
                k += 1
            group = c1[i:j]
            for g in reversed(group):
                c0.insert(k + 1, (g[0], g[1], g[2], g[3], g[4], g[5], 0, g[7]))
            del c1[i:j]
            continue
        i += 1
    for c in (0, 1):
        for y, x, kind, text, size, bold, colno, spans_ in cols[c]:  # the PDF's own reading order keeps wrapped type lines together
            if kind == "table":
                lines.append((kind, ("T", x, y, colno, text.strip(), size, bold)))
            elif kind == "stat":
                lines.append((kind, (text, spans_, x, y)))
            else:
                lines.append((kind, text))

RARITY = re.compile(r"\b(Common|Uncommon|Rare|Very Rare|Legendary|Artifact|Rarity Varies)\b")
items = []
cur = None
started = False
for kind, text in lines:
    if kind == "table":
        if started and cur is not None:
            cur["body"].append(text)
        continue
    if kind == "stat":
        if started and cur is not None:
            cur.setdefault("stat", []).append(text)
        continue
    if kind == "name" and cur is not None and not cur["type_lines"] and not cur["body"]:
        cur["name"] += " " + text.strip()  # the name wrapped onto a second line
        continue
    if kind == "name":
        if text.strip() == "Adamantine Armor":
            started = True
        if not started:
            continue
        cur = {"name": text.strip(), "type_lines": [], "body": []}
        items.append(cur)
        continue
    if not started or cur is None:
        continue
    joined = " ".join(cur["type_lines"])
    type_done = bool(RARITY.search(joined)) and joined.count("(") == joined.count(")")
    if kind == "italic" and not cur["body"] and not type_done and len(cur["type_lines"]) < 4:
        cur["type_lines"].append(text.strip())
    else:
        cur["body"].append(text)


def join_list(lines_):
    out = []
    for t in lines_:
        t = t.replace("\t", "  ").rstrip()
        if out and out[-1].endswith("-") and not out[-1].endswith(" -") and t[:1].islower():
            out[-1] = out[-1][:-1] + t.lstrip()
        elif t.startswith("  ") or not out:
            out.append(t.strip())
        else:
            out[-1] = out[-1] + " " + t.strip()
    return out


def join(lines_):
    return "\n".join(join_list(lines_))


result = []
for it in items:
    typ = " ".join(it["type_lines"]).strip()
    if not typ:
        continue  # a table heading or sub-heading, not an item
    depth, cut = 0, -1
    for k, ch in enumerate(typ):
        depth += ch == "("
        depth -= ch == ")"
        if ch == "," and depth == 0:
            cut = k
            break
    category = typ[:cut].strip() if cut >= 0 else typ
    rest = typ[cut + 1:].strip() if cut >= 0 else ""
    attune = "Requires Attunement" in rest
    att_note = ""
    am = re.search(r"\(Requires Attunement( by [^)]*)?\)", rest)
    if am:
        att_note = (am.group(1) or "").strip()
    rarity_text = re.sub(r"\s*\(Requires Attunement[^)]*\)", "", rest).strip()
    rm = RARITY.search(rarity_text)
    rarity = rm.group(1) if rm else rarity_text
    result.append({
        "name": it["name"], "type": category, "rarity": rarity, "rarity_text": rarity_text,
        "requires_attunement": attune, "attunement": att_note, "type_line": typ,
        "description": "\n".join(flat_blocks(blocks_of(it["body"], join_list))),
        "description_blocks": blocks_of(it["body"], join_list),
        "statblock": build_statblock(it["stat"]) if it.get("stat") else None,
    })

json.dump(result, open(OUT, "w", encoding="utf-8"), indent=1, ensure_ascii=False)
print(len(result), "items", file=sys.stderr)
