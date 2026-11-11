"""Extract the Rules Glossary of the SRD 5.2.1 (CC-BY-4.0) verbatim into glossary_srd.json.

Entry names are 12 pt SemiBold ("Blinded [Condition]"), body is Cambria 10 pt, a leading tab starts a
new paragraph, bullet lines start with a bullet glyph. Two columns per page, read in the PDF's own order.
Usage: python extract_glossary.py [pdf] [out.json]
"""
import json
import re
import sys
import warnings

warnings.filterwarnings("ignore")
import pymupdf

PDF = sys.argv[1] if len(sys.argv) > 1 else r"<workspace>\agentrpg-reference\SRD_CC_v5.2.1.pdf"
OUT = sys.argv[2] if len(sys.argv) > 2 else "glossary_srd.json"
FIRST, LAST = 177, 200  # glossary pages; the last entry ends where the next chapter heading appears

doc = pymupdf.open(PDF)
lines = []
for pno in range(FIRST - 1, LAST):
    for b in doc[pno].get_text("dict")["blocks"]:
        if b.get("type") != 0:
            continue
        for l in b["lines"]:
            sp = l["spans"]
            text = "".join(s["text"] for s in sp)
            f, size = sp[0]["font"], round(sp[0]["size"], 1)
            if "GillSans" in f and size >= 11 and (text.strip().isdigit() or text.startswith("System Reference")):
                continue  # running header
            lines.append((f, size, text, l["bbox"]))

entries, cur, para = [], None, []


def clean(t):
    return t.replace("\u00a0", " ").replace("\t", " ").strip()


def join(ls):
    out = ""
    for ln in ls:
        ln = clean(ln)
        if not ln:
            continue
        if out.endswith("-") and ln[:1].islower():
            out = out[:-1] + ln
        else:
            out = (out + " " + ln).strip()
    return out


def flush():
    global para
    if para and cur is not None:
        cur["text"].append(join(para))
    para = []


for f, size, text, bbox in lines:
    if "SemiBold" in f and size >= 11.5:
        name = clean(text)
        if name in ("Rules Glossary", "Rules Definitions") or size > 13:
            flush()
            if size > 13 and cur is not None:
                break  # next chapter
            continue
        flush()
        m = re.match(r"^(.*?)\s*\[([^\]]+)\]\s*$", name)
        cur = {"name": m.group(1) if m else name, "kind": m.group(2) if m else "", "text": []}
        entries.append(cur)
        continue
    if cur is None:
        continue
    if text.startswith("\t") or text.startswith(" \t") or text.lstrip().startswith("\u2022"):
        flush()
    para.append(text)
flush()
json.dump(entries, open(OUT, "w", encoding="utf-8"), indent=1, ensure_ascii=False)
print(len(entries), "entries;", sum(1 for e in entries if e["kind"] == "Condition"), "conditions", file=sys.stderr)
