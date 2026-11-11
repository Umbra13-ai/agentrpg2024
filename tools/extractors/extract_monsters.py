"""Extract the SRD 5.2.1 monster stat blocks (PDF pages 258-364: Monsters A-Z and Animals) into JSON.

Usage: python tools/extractors/extract_monsters.py OUT.json

The stat blocks are read by font: the name is GillSans-SemiBold 14.8, the line under it is italic (size, type,
alignment), AC/Initiative/HP/Speed lines have bold labels, the ability table is read by position (score, modifier and
saving throw bonus), then Skills / Resistances / Vulnerabilities / Immunities / Gear / Senses / Languages / CR, then the
sections Traits, Actions, Bonus Actions, Reactions and Legendary Actions whose entries start with a bold italic name.
The text of every entry is kept word for word (line-wrap hyphens removed).
"""
import json
import re
import sys
import warnings

import pymupdf

warnings.filterwarnings("ignore")
SRD = r"<workspace>\agentrpg-reference\SRD_CC_v5.2.1.pdf"
FIRST, LAST = 257, 363  # zero-based page indexes (PDF pages 258-364)
MINUS = "\u2212"
ABIL = ["str", "dex", "con", "int", "wis", "cha"]
SECTIONS = {"Traits": "traits", "Actions": "actions", "Bonus Actions": "bonus_actions", "Reactions": "reactions",
            "Legendary Actions": "legendary_actions"}
LABELS = ["Skills", "Resistances", "Vulnerabilities", "Immunities", "Gear", "Senses", "Languages", "CR"]


def clean(s):
    return (s.replace("\xa0", " ").replace("\u00ad", "").replace(MINUS, "-").replace("\u200b", ""))


class Line:
    def __init__(self, l, page):
        sp = l["spans"]
        self.x, self.y = sp[0]["bbox"][0], sp[0]["bbox"][1]
        self.text = "".join(s["text"] for s in sp)
        self.size = round(sp[0]["size"], 1)
        self.font = sp[0]["font"]
        self.fonts = {s["font"] for s in sp}
        self.page = page
        self.sp = sp
        self.bi = "BoldItalic" in sp[0]["font"]
        # the leading bold-italic run (entry name)
        run = ""
        for s in sp:
            if "BoldItalic" in s["font"]:
                run += s["text"]
            else:
                break
        self.name_run = run


def page_lines(doc, pn):
    out = []
    for b in doc[pn].get_text("dict")["blocks"]:
        for l in b.get("lines", []):
            ln = Line(l, pn + 1)
            if not ln.text.strip():
                continue
            if ln.y > 735:  # footer: page number and "System Reference Document"
                continue
            if ln.size == 26.0:  # part title
                continue
            out.append(ln)
    return out


def join_wrapped(lines):
    """Join wrapped lines of one entry: hyphenation at line ends is removed, a leading tab starts a new paragraph."""
    text = ""
    for t in lines:
        t = clean(t)
        para = t.startswith("\t")
        t = t.strip("\t").strip() if para else t.strip()
        if not text:
            text = t
        elif para:
            text += "\n" + t
        elif text.endswith("-") and t[:1].islower() and not text.endswith(" -"):
            text = text[:-1] + t
        else:
            text += " " + t
    return text


def parse_abilities(grid):
    """grid: list of Line between Speed and Skills/Senses. Returns {abbr: {score, mod, save}}."""
    res = {}
    labels = [ln for ln in grid if re.match(r"^(Str|Dex|Con|Int|Wis|Cha)\b", ln.text.strip())]
    others = [ln for ln in grid if ln not in labels and re.search(r"\d", ln.text) and not ln.text.startswith(("MOD", "SAVE"))]
    base = min(l.x for l in labels)
    colx = [base, base + 77, base + 154, base + 240]
    for lab in labels:
        key = lab.text.strip()[:3].lower()
        col = max(i for i in range(3) if lab.x >= colx[i] - 6)
        toks = re.findall(r"[+\-\u2212]?\d+", clean(lab.text[3:]))
        # the modifier and the saving throw bonus may sit on lines of their own at the same height
        for o in others:
            if abs(o.y - lab.y) < 6 and colx[col] - 6 <= o.x < colx[col + 1] - 6 and o is not lab:
                toks += re.findall(r"[+\-\u2212]?\d+", clean(o.text))
        toks = [t.replace(MINUS, "-") for t in toks]
        score = int(toks[0])
        mod = int(toks[1]) if len(toks) > 1 else None
        save = int(toks[2]) if len(toks) > 2 else None
        res[key] = {"score": score, "mod": mod, "save": save}
    return res


def split_types(s):
    """'Large Aberration, Lawful Evil' -> size, type (with tags), alignment."""
    head, _, align = s.partition(",")
    # alignment can contain a comma-less phrase; the type line is 'Size Type (tags)[, alignment]'
    m = re.match(r"^((?:Tiny|Small|Medium|Large|Huge|Gargantuan)(?: or (?:Tiny|Small|Medium|Large|Huge|Gargantuan))*)\s+(.*)$", head.strip())
    if not m:
        return "", head.strip(), align.strip()
    return m.group(1), m.group(2).strip(), align.strip()


def parse_block(lines):
    mon = {"name": clean(lines[0].text).strip()}
    i = 1
    first_text = clean(lines[i].text).strip()
    mon["size"], mon["type"], mon["alignment"] = split_types(first_text)
    mon["raw_type_line"] = first_text
    i += 1
    # AC / Initiative / HP / Speed lines (bold labels), possibly wrapped
    hdr = []
    while i < len(lines) and not lines[i].text.startswith(("MOD", "SAVE")):
        hdr.append(lines[i])
        i += 1
    htext = " ".join(clean(h.text).strip() for h in hdr)
    htext = re.sub(r"\s+", " ", htext)
    m = re.search(r"AC (\d+)", htext)
    mon["ac"] = int(m.group(1)) if m else None
    m = re.search(r"Initiative ([+-]?\d+) \((\d+)\)", clean(htext))
    mon["initiative"] = {"mod": int(m.group(1)), "score": int(m.group(2))} if m else None
    m = re.search(r"HP (\d+)(?: \(([^)]*)\))?", htext)
    mon["hp"] = int(m.group(1)) if m else None
    mon["hit_points_formula"] = m.group(2) if m and m.group(2) else ""
    m = re.search(r"Speed (.*)$", htext)
    mon["speed"] = m.group(1).strip() if m else ""
    # ability grid: until the first label line
    grid = []
    while i < len(lines) and not re.match(r"^(Skills|Resistances|Vulnerabilities|Immunities|Gear|Senses|Languages|CR)\b", lines[i].text.strip()):
        grid.append(lines[i])
        i += 1
    mon["abilities"] = parse_abilities(grid)
    # labelled lines
    labelled = {}
    cur = None
    while i < len(lines) and lines[i].text.strip() not in SECTIONS:
        t = clean(lines[i].text).strip()
        m = re.match(r"^(Skills|Resistances|Vulnerabilities|Immunities|Gear|Senses|Languages|CR)\b\s*(.*)$", t)
        if m and "Bold" in lines[i].font:
            cur = m.group(1)
            labelled[cur] = m.group(2)
        elif cur:
            labelled[cur] += " " + t
        i += 1
    for k, v in labelled.items():
        labelled[k] = re.sub(r"\s+", " ", v.replace("- ", "-") if v.endswith("-") else v).strip()
    mon["skills"] = labelled.get("Skills", "")
    mon["damage_vulnerabilities"] = labelled.get("Vulnerabilities", "")
    mon["damage_resistances"] = labelled.get("Resistances", "")
    imm = labelled.get("Immunities", "")
    dmg, _, cond = imm.partition(";")
    mon["damage_immunities"] = dmg.strip() if ";" in imm else (imm if not _looks_conditions(imm) else "")
    mon["condition_immunities"] = cond.strip() if ";" in imm else (imm if _looks_conditions(imm) else "")
    mon["gear"] = labelled.get("Gear", "")
    mon["senses"] = labelled.get("Senses", "")
    mon["languages"] = labelled.get("Languages", "")
    cr = labelled.get("CR", "")
    mon["cr_line"] = cr
    m = re.match(r"^([\d/]+) \((?:XP )?([\d,]+)(?: XP)?(?:, or ([\d,]+)(?: XP)? in lair)?; PB \+(\d+)\)", cr)
    if m:
        mon["cr"], mon["xp"] = m.group(1), int(m.group(2).replace(",", ""))
        mon["xp_lair"] = int(m.group(3).replace(",", "")) if m.group(3) else None
        mon["proficiency_bonus"] = int(m.group(4))
    else:
        mon["cr"] = cr
    # sections
    sections = {v: [] for v in SECTIONS.values()}
    mon["legendary_intro"] = ""
    sect = None
    entry = None
    while i < len(lines):
        ln = lines[i]
        t = ln.text.strip()
        if t in SECTIONS and ln.size == 12.0:
            sect = SECTIONS[t]
            entry = None
        elif sect is not None:
            nm = ln.name_run
            if nm:
                m = re.match(r"^(.*?\.)\s*$", nm) or re.match(r"^(.*)$", nm)
                name = clean(nm).strip().rstrip(".").strip()
                # the bold-italic run can end before ' (3/Day)' or the like: entry name stays as printed
                rest = clean(ln.text)[len(clean(nm)):]
                entry = {"name": name, "lines": [rest]}
                sections[sect].append(entry)
            elif entry is not None:
                entry["lines"].append(ln.text)
            elif sect == "legendary_actions":
                mon["legendary_intro"] = (mon["legendary_intro"] + " " + clean(ln.text)).strip()
        i += 1
    for k, v in sections.items():
        out = []
        for e in v:
            out.append({"name": e["name"], "text": join_wrapped(e["lines"]).lstrip(". ").strip()})
        mon[k] = out
    if mon["legendary_actions"]:
        pass
    return mon


def _looks_conditions(s):
    conds = ("Blinded", "Charmed", "Deafened", "Exhaustion", "Frightened", "Grappled", "Incapacitated", "Invisible", "Paralyzed",
             "Petrified", "Poisoned", "Prone", "Restrained", "Stunned", "Unconscious")
    parts = [p.strip() for p in s.split(",") if p.strip()]
    return bool(parts) and all(p in conds for p in parts)


def main(out_path):
    doc = pymupdf.open(SRD)
    lines = []
    for pn in range(FIRST, LAST + 1):
        lines += page_lines(doc, pn)
    blocks, cur, group = [], None, ""
    for ln in lines:
        if ln.size == 18.0 and "SemiBold" in ln.font:
            group = clean(ln.text).strip()
            continue
        if ln.size == 14.8 and "SemiBold" in ln.font:
            cur = [ln]
            blocks.append((group, cur))
        elif cur is not None:
            cur.append(ln)
    result = []
    for group, b in blocks:
        try:
            m = parse_block(b)
        except Exception as e:  # keep going, report
            print("FAILED", b[0].text, repr(e), file=sys.stderr)
            continue
        m["group"] = "Animals" if b[0].page >= 344 else group
        m["page"] = b[0].page
        result.append(m)
    json.dump(result, open(out_path, "w", encoding="utf-8"), ensure_ascii=False, indent=1)
    print(len(result), "monsters")


if __name__ == "__main__":
    main(sys.argv[1])
