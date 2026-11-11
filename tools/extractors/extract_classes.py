"""Extract the Classes chapter of SRD 5.2.1 (CC-BY-4.0), word for word: pages 28 to 82.

Each class has: Core Traits (a label/value table), "Becoming a ..." bullets, class features ("Level N: Name"),
the class features table (a table as wide as the page), a spell list for the spellcasting classes, and a subclass
with its own features. Output: classes_srd.json = [{name, core_traits, sections:[{heading, level, blocks}]}].
Block kinds: paragraph (string), {"heading", "level"}, {"trait","text"} (run-in name), {"table"}, {"bullet"}.
Usage: python extract_classes.py [out.json]
"""
import json
import os
import re
import sys
import warnings

warnings.filterwarnings("ignore")
import pymupdf

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from srd_tables import blocks_of

PDF = r"<workspace>\agentrpg-reference\SRD_CC_v5.2.1.pdf"
OUT = sys.argv[1] if len(sys.argv) > 1 else "classes_srd.json"
FIRST, LAST = 28, 82
CLASSES = ["Barbarian", "Bard", "Cleric", "Druid", "Fighter", "Monk", "Paladin", "Ranger", "Rogue", "Sorcerer", "Warlock", "Wizard"]

doc = pymupdf.open(PDF)


def clean(t):
    return t.replace("\u200b", "").replace("\u00a0", " ")


stream = []  # entries: (kind, text, spans, x, y, col, size, bold, raw)
for pno in range(FIRST - 1, LAST):
    cols = {0: [], 1: []}
    for b in doc[pno].get_text("dict")["blocks"]:
        if b.get("type") != 0:
            continue
        for l in b["lines"]:
            if l["bbox"][1] > 735:
                continue
            spans = l["spans"]
            raw = clean("".join(s["text"] for s in spans))
            lead = [s for s in spans if s["text"].strip()]
            if not lead:
                continue
            f0, size = lead[0]["font"], round(lead[0]["size"], 1)
            t = raw.strip()
            if "SemiBold" in f0 and size >= 25:
                kind = "chapter"
            elif "SemiBold" in f0 and size >= 17:
                kind = "h1"
            elif "SemiBold" in f0 and size >= 13:
                kind = "h2"
            elif "SemiBold" in f0 and abs(size - 12.0) < 0.3:
                kind = "name"
            elif "GillSans" in f0 and size <= 10.6:
                kind = "table"
            elif "BoldItalic" in f0:
                kind = "trait"
            elif "Cambria" in f0 and "Bold" not in f0 and "Italic" not in f0:
                kind = "text"
            elif "Italic" in f0 and "Cambria" in f0:
                kind = "text"
            else:
                kind = "text"
            col = 0 if l["bbox"][0] < 300 else 1
            cols[col].append((kind, t, spans, l["bbox"][0], l["bbox"][1], col, size, "SemiBold" in f0, raw))
    # the class progression table ("Barbarian Features") is as wide as the page: its right half (x >= 300) was read as
    # the second column; put it back with the left half
    c0, c1 = cols[0], cols[1]
    for k, e in enumerate(list(c0)):
        if e[0] == "table" and e[7] and e[6] >= 10.4 and re.match(r"^[A-Za-z ]+ Features$", e[1]) and e in c0:
            idx = c0.index(e)
            last = idx
            while last + 1 < len(c0) and c0[last + 1][0] == "table":
                last += 1
            ty, ly = e[4], max(x[4] for x in c0[idx:last + 1])
            group = [g for g in c1 if g[0] == "table" and ty - 5 <= g[4] <= ly + 14]
            for g in reversed(group):
                c0.insert(last + 1, (g[0], g[1], g[2], g[3], g[4], 0, g[6], g[7], g[8]))
                c1.remove(g)
    deferred = []
    for c in (0, 1):
        col = cols[c]
        runs, rest, cur_run = [], [], []
        for e in col:
            if e[0] == "table":
                cur_run.append(e)
            else:
                if cur_run:
                    runs.append(cur_run)
                    cur_run = []
                rest.append(e)
        if cur_run:
            runs.append(cur_run)
        runs = deferred + runs if c == 1 else runs
        deferred = []
        for run in runs:
            ty = min(e[4] for e in run)
            at = next((k for k, e in enumerate(rest) if e[4] > ty + 4), len(rest))
            if c == 0 and at == len(rest) and any(e[0] != "table" and e[4] < ty for e in cols[1]):
                deferred.append(run)
                continue
            rest[at:at] = run
        cols[c] = rest
    for c in (0, 1):
        stream.extend(cols[c])


def join_lines(lines):
    out = ""
    for ln in lines:
        ln = ln.strip()
        if not ln:
            continue
        if out.endswith("-") and ln[:1].islower():
            out = out[:-1] + ln
        else:
            out = (out + " " + ln).strip()
    return out


def run_in(t, spans):
    head = ""
    for s in spans:
        if not s["text"].strip() and not head:
            continue
        if "BoldItalic" in s["font"] or ("Bold" in s["font"] and not head.endswith(".")):
            head += s["text"]
        else:
            break
    head = head.strip()
    return head.rstrip(".").strip(), (t[len(head):].strip() if t.startswith(head) else t)


def join_text(seg):
    """seg: list [kind, t, spans, raw] -> blocks (paragraph strings, bullets, run-in traits)."""
    out, cur = [], None
    for kind, t, spans, raw in seg:
        bullet = t.startswith("\u2022")
        starts = raw.startswith("\t") or raw.startswith(" \t") or bullet or kind == "trait"
        if kind == "trait":
            name, rest = run_in(t, spans)
            out.append({"kind": "trait", "name": name, "lines": [rest]})
            cur = out[-1]
        elif bullet:
            out.append({"kind": "bullet", "lines": [t.lstrip("\u2022").strip()]})
            cur = out[-1]
        elif starts or cur is None:
            out.append({"kind": "text", "lines": [t]})
            cur = out[-1]
        else:
            cur["lines"].append(t)
    res = []
    for o in out:
        text = join_lines(o["lines"])
        if o["kind"] == "trait":
            res.append({"trait": o["name"], "text": text})
        elif o["kind"] == "bullet":
            res.append({"bullet": text})
        else:
            res.append(text)
    return res


def core_traits(items):
    """The label/value table 'Core X Traits': bold labels at the left, values at x >= 160."""
    labels, values = [], []
    x0 = min((it[3] for it in items), default=0)  # the table may sit in either column
    for kind, t, spans, x, y, col, size, bold, raw in items:
        x -= x0 - 67
        bold_part = "".join(s["text"] for s in spans if "SemiBold" in s["font"]).strip()
        rest = "".join(s["text"] for s in spans if "SemiBold" not in s["font"]).strip()
        if x < 150 and bold_part:
            if rest and x < 150:
                labels.append({"label": bold_part, "y": y, "inline": rest})  # "Weapon Proficiencies Simple and Martial weapons"
            else:
                labels.append({"label": bold_part, "y": y, "inline": ""})
        elif x >= 150:
            values.append((y, t))
    rows = []
    for lab in labels:
        near = any(abs(y - lab["y"]) <= 3 for y, _ in values)
        if rows and not near and not lab["inline"] and lab["y"] - rows[-1]["y_last"] <= 12:
            rows[-1]["label"] += " " + lab["label"]
            rows[-1]["y_last"] = lab["y"]
        else:
            rows.append({"label": lab["label"], "y": lab["y"], "y_last": lab["y"], "text": lab["inline"], "values": []})
    for y, t in values:
        owner = None
        for r in rows:
            if r["y"] - 3 <= y:
                owner = r
        if owner is not None:
            owner["values"].append(t)
    return [{"label": r["label"], "text": join_lines([r["text"]] + r["values"])} for r in rows]


classes = []
cur_class = None
cur_section = None
pending = []  # stream entries of the current section


def paragraphs_from(items):
    lines = []
    for kind, t, spans, x, y, col, size, bold, raw in items:
        if kind == "table":
            lines.append(("T", x, y, col, t, size, bold))
        else:
            lines.append([kind, t, spans, raw])
    return blocks_of(lines, join_text)


def flush():
    global pending
    if cur_section is not None:
        if re.search(r"Spell List$", cur_section["heading"]):
            cur_section["spell_list"] = True
            pending = [p for p in pending if p[0] != "table"]  # the lists are the "classes" of each spell (the spells' own headers)
        if cur_section.get("core"):
            n = 0  # the core table: table lines up to the title of another table (or the first text)
            while n < len(pending) and pending[n][0] == "table" and not (pending[n][7] and pending[n][6] >= 10.4):
                n += 1
            cur_section["blocks"] = [{"keyvalue": core_traits(pending[:n])}] + paragraphs_from(pending[n:])
        else:
            cur_section["blocks"] = paragraphs_from(pending)
    pending = []


for entry in stream:
    kind, t, spans, x, y, col, size, bold, raw = entry
    if kind == "chapter":
        continue
    if kind == "h1" and t in CLASSES:
        flush()
        cur_class = {"name": t, "sections": []}
        classes.append(cur_class)
        cur_section = None
        continue
    if cur_class is None:
        continue
    if kind in ("h2", "name"):
        # a wrapped heading continues the previous one
        if pending == [] and cur_section is not None and cur_section["heading"] and cur_section.get("_y") is not None \
                and cur_section.get("_col") == col and 0 < y - cur_section["_y"] < 22 and cur_section.get("_kind") == kind and not cur_section["blocks"]:
            cur_section["heading"] += " " + t
            cur_section["_y"] = y
            continue
        flush()
        cur_section = {"heading": t, "level": 2 if kind == "h2" else 3, "blocks": [], "_y": y, "_col": col, "_kind": kind}
        cur_class["sections"].append(cur_section)
        continue
    if kind == "table" and cur_section is None:
        # the Core Traits table: its title is the first table line (SemiBold 10.5)
        pass
    if kind == "table" and bold and size >= 10.4 and re.match(r"^Core .* Traits$", t):
        flush()
        cur_section = {"heading": t, "level": 4, "blocks": [], "core": True, "_y": y, "_col": col, "_kind": "table"}
        cur_class["sections"].append(cur_section)
        continue
    if cur_section is None:
        cur_section = {"heading": "", "level": 0, "blocks": [], "_y": None, "_col": None, "_kind": None}
        cur_class["sections"].append(cur_section)
    pending.append(entry)
flush()
for c in classes:
    for s in c["sections"]:
        for k in ("_y", "_col", "_kind", "core"):
            s.pop(k, None)

SCHOOLS = {"Abjuration", "Conjuration", "Divination", "Enchantment", "Evocation", "Illusion", "Necromancy", "Transmutation"}
SPECIAL = re.compile(r"^(—|[CRM](, ?[CRM])*)$")


def is_spell_list_table(tb):
    """A piece of a class spell list (Spell | School | Special): the lists come from the spells' own class lists."""
    rows = tb["rows"]
    if rows and "Special" in rows[0] and "School" in rows[0]:
        return True
    hits = sum(1 for r in rows if len(r) >= 3 and SPECIAL.match(r[-1].strip()) and any(s in r[-2] for s in SCHOOLS))
    return hits and hits >= len(rows) - 1


for c in classes:
    for s in c["sections"]:
        out = []
        for b in s["blocks"]:
            if isinstance(b, dict) and "table" in b:
                tb = b["table"]
                if is_spell_list_table(tb):
                    s["spell_list"] = True
                    continue
                if len(tb["rows"][0]) == 1 and not tb["title"]:
                    out.append({"sidebar": [r[0] for r in tb["rows"]]})  # a boxed note
                    continue
            out.append(b)
        s["blocks"] = out
for c in classes:
    for s in c["sections"]:
        for b in s["blocks"]:
            if isinstance(b, dict) and "table" in b:
                hdr = b["table"]["rows"][0]
                for i, cell in enumerate(hdr):
                    m = re.match(r"^—+\s*(Spell Slots per Spell Level)\s*—+\s*(.*)$", cell)
                    if m:  # the banner spans this column and the ones after it
                        hdr[i] = m.group(2)
                        b["table"]["header_groups"] = [{"text": m.group(1), "from": i, "to": len(hdr) - 1}]


def text_of(b):
    if isinstance(b, str):
        return b
    if isinstance(b, dict):
        return b.get("text") if "trait" in b else b.get("bullet") if "bullet" in b else None
    return None


def set_text(b, text, blocks, i):
    if isinstance(b, str):
        blocks[i] = text
    elif "trait" in b:
        b["text"] = text
    else:
        b["bullet"] = text


def repair(cls):
    """A paragraph or bullet that continues in another column or after a table (it starts in lower case) is joined to the
    one it continues, wherever the page layout put it. The class features table then gets its own place."""
    prev = None  # (blocks, index) of the last paragraph-like block
    for s in cls["sections"]:
        i = 0
        while i < len(s["blocks"]):
            b = s["blocks"][i]
            tx = text_of(b)
            if tx is not None and tx[:1].islower() and prev is not None:
                pb, pi = prev
                joined = text_of(pb[pi])
                joined = joined[:-1] + tx if joined.endswith("-") else joined + " " + tx
                set_text(pb[pi], joined, pb, pi)
                del s["blocks"][i]
                continue
            if tx is not None:
                prev = (s["blocks"], i)
            i += 1
    table = None
    for s in cls["sections"]:
        for i, b in enumerate(s["blocks"]):
            if isinstance(b, dict) and "table" in b and b["table"]["title"].endswith("Features"):
                table = s["blocks"].pop(i)
                break
        if table:
            break
    if table:
        for s in cls["sections"]:
            if s["level"] == 2 and s["heading"].endswith("Class Features"):
                s["blocks"].append(table)
                break


for c in classes:
    repair(c)
json.dump(classes, open(OUT, "w", encoding="utf-8"), indent=1, ensure_ascii=False)
print(len(classes), "classes;", sum(len(c["sections"]) for c in classes), "sections", file=sys.stderr)
