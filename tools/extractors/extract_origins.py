"""Extract the Character Origins chapter of SRD 5.2.1 (CC-BY-4.0): backgrounds and species, word for word.

Pages 83 to 86. Names are GillSans-SemiBold 12 pt, label lines ("Creature Type: Humanoid", "Ability Scores: ...") are
SemiBold 10 pt with wrapped values in GillSans 10 pt, traits start with a BoldItalic run-in heading (after a tab),
table text is GillSans 9.2 to 10.5 pt. Two columns per page, read in the PDF's own order.
Usage: python extract_origins.py [out.json]
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
OUT = sys.argv[1] if len(sys.argv) > 1 else "origins_srd.json"
FIRST, LAST = 83, 86

doc = pymupdf.open(PDF)
BG_LABELS = ("Ability Scores:", "Feat:", "Skill Proficiencies:", "Tool Proficiency:", "Equipment:")
SP_LABELS = ("Creature Type:", "Size:", "Speed:")

stream = []  # (kind, text, spans, x, y, col)
for pno in range(FIRST - 1, LAST):
    cols = {0: [], 1: []}
    for b in doc[pno].get_text("dict")["blocks"]:
        if b.get("type") != 0:
            continue
        for l in b["lines"]:
            if l["bbox"][1] > 735:
                continue  # the page footer; nothing sits above the text
            spans = l["spans"]
            text = "".join(s["text"] for s in spans).replace("\u200b", "").replace("\u00a0", " ")
            lead = [s for s in spans if s["text"].strip()]
            if not lead:
                continue
            f0, size = lead[0]["font"], round(lead[0]["size"], 1)  # the first span with text (a tab may come first)
            t = text.strip()
            if not t:
                continue
            if "SemiBold" in f0 and size >= 17:
                kind = "h1"
            elif "SemiBold" in f0 and size >= 13:
                kind = "h2"
            elif "SemiBold" in f0 and abs(size - 12.0) < 0.3:
                kind = "name"
            elif "SemiBold" in f0 and abs(size - 10.0) < 0.3 and any(t.startswith(x) for x in BG_LABELS + SP_LABELS):
                kind = "label"
            elif "GillSans" in f0 and abs(size - 10.0) < 0.3:
                kind = "labelcont"
            elif "GillSans" in f0 and size <= 10.6:
                kind = "table"
            elif "BoldItalic" in f0:
                kind = "trait"
            elif "Cambria-Bold" in f0 or "Bold" in f0 and "Cambria" in f0:
                kind = "entry"
            else:
                kind = "text"
            col = 0 if l["bbox"][0] < 300 else 1
            cols[col].append((kind, t, spans, l["bbox"][0], l["bbox"][1], col, size, "SemiBold" in f0, text))
    # a table as wide as the page: its right half (x >= 300) was read as the second column; put it back
    c0, c1 = cols[0], cols[1]
    i = 0
    while i < len(c1):
        head = c1[i]
        k0 = [k for k, e in enumerate(c0) if e[0] == "table" and e[7] and e[6] < 9.4 and abs(e[4] - head[4]) <= 3]
        if head[0] == "table" and head[7] and head[6] < 9.4 and k0:
            j = i + 1
            while j < len(c1) and c1[j][0] == "table" and not (c1[j][7] and (c1[j][6] < 9.4 or c1[j][6] >= 10.4)):
                j += 1
            k = k0[0]
            while k + 1 < len(c0) and c0[k + 1][0] == "table":
                k += 1
            for g in reversed(c1[i:j]):
                c0.insert(k + 1, (g[0], g[1], g[2], g[3], g[4], 0, g[6], g[7], g[8]))
            del c1[i:j]
            continue
        i += 1
    # tables go where they sit on the page: before the first line of text that lies below them. A table that
    # lies below all the text of the first column belongs after the text of the second column that is above it.
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


def trait_split(first_text, spans):
    """A run-in heading: the BoldItalic part before the first period."""
    head = ""
    for s in spans:
        if not s["text"].strip() and not head:
            continue
        if "BoldItalic" in s["font"] or ("Bold" in s["font"] and not head.endswith(".")):
            head += s["text"]
        else:
            break
    head = head.strip()
    return head.rstrip(".").strip(), first_text[len(head):].strip() if first_text.startswith(head) else first_text


def paragraphs_from(items):
    """items: stream entries of one section's body -> list of blocks."""
    lines = []
    for kind, t, spans, x, y, col, size, bold, raw in items:
        if kind == "table":
            lines.append(("T", x, y, col, t, size, bold))
        else:
            lines.append([kind, t, spans, raw])  # a list: tuples are table lines

    def join_text(seg):
        # seg: entries (kind, t, spans, raw); a trait or entry line starts a paragraph, so does a tab
        out = []
        cur = None
        for kind, t, spans, raw in seg:
            if kind in ("h2", "h3"):
                out.append({"kind": "heading", "lines": [t], "level": 2 if kind == "h2" else 3})
                cur = None
                continue
            starts = raw.startswith("\t") or raw.startswith(" \t") or kind in ("trait", "entry")
            if kind in ("trait", "entry"):
                name, rest = trait_split(t, spans)
                if name and rest is not None:
                    out.append({"kind": kind, "name": name, "lines": [rest]})
                    cur = out[-1]
                    continue
            if starts or cur is None:
                out.append({"kind": "text", "lines": [t]})
                cur = out[-1]
            else:
                cur["lines"].append(t)
        res = []
        for o in out:
            text = join_lines(o["lines"])
            if o["kind"] == "heading":
                res.append({"heading": text, "level": o["level"]})
            else:
                res.append(text if o["kind"] == "text" else {o["kind"]: o["name"], "text": text})
        return res

    return blocks_of(lines, join_text)


result = {"backgrounds": {"intro": [], "items": []}, "species": {"intro": [], "items": []}}
section = "bg_intro"
cur = None
body = []


def flush():
    global body
    if cur is None and section in ("bg_intro", "sp_intro"):
        target = result["backgrounds" if section == "bg_intro" else "species"]["intro"]
        target.extend(paragraphs_from(body))
    elif cur is not None:
        cur["blocks"] = paragraphs_from(body)
    body = []


for kind, t, spans, x, y, col, size, bold, raw in stream:
    if kind == "h1":
        if t == "Character Origins":
            continue
        flush()
        if t == "Character Backgrounds":
            section, cur = "bg_intro", None
        elif t == "Character Species":
            section, cur = "sp_intro", None
        else:
            raise SystemExit("unexpected heading " + t)
        continue
    if kind == "h2":
        if t in ("Background Descriptions",):
            flush()
            section, cur = "bg", None
            continue
        if t == "Species Descriptions":
            flush()
            section, cur = "sp", None
            continue
        body.append(("h2", t, spans, x, y, col, size, bold, raw))
        continue
    if kind == "name" and section in ("bg_intro", "bg", "sp_intro", "sp") and section in ("bg", "sp"):
        flush()
        cur = {"name": t, "fields": []}
        result["backgrounds" if section == "bg" else "species"]["items"].append(cur)
        continue
    if section in ("bg_intro", "sp_intro") and kind == "name":
        body.append(("h3", t, spans, x, y, col, size, bold, raw))
        continue
    if cur is not None and kind == "label":
        label = next(l for l in BG_LABELS + SP_LABELS if t.startswith(l))
        cur["fields"].append({"label": label.rstrip(":"), "text": t[len(label):].strip()})
        continue
    if cur is not None and kind == "labelcont" and cur["fields"] and not body:
        f = cur["fields"][-1]
        f["text"] = (f["text"][:-1] + t if f["text"].endswith("-") and t[:1].islower() else f["text"] + " " + t).strip()
        continue
    body.append((kind, t, spans, x, y, col, size, bold, raw))
flush()

# intro headings (h2, h3) become heading blocks: turn the placeholder entries into {"heading": text}
for key in ("backgrounds", "species"):
    result[key]["intro"] = [b for b in result[key]["intro"]]

json.dump(result, open(OUT, "w", encoding="utf-8"), indent=1, ensure_ascii=False)
print(len(result["backgrounds"]["items"]), "backgrounds,", len(result["species"]["items"]), "species", file=sys.stderr)
