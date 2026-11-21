#!/usr/bin/env python3
"""Parse stat blocks out of text extracted from the supplied 2024 Monster Manual reconstruction (HTML/DOCX chapters) into the
compendium import format (POST /api/gm/compendium/monsters).

Usage: parse_mm_statblocks.py out_dir min_cr chapter.txt [chapter.txt ...]

Writes one <slug>.json per monster with CR >= min_cr. Nothing here names a monster: the layout is the book's own
("AC 20 Initiative +12 (22)", "HP 225 (18d12 + 108)", ability rows, Traits/Actions/Bonus Actions/Reactions/Legendary Actions).
A block that does not parse completely is listed on stderr and skipped, never guessed.
"""
import json
import os
import re
import sys
from fractions import Fraction

SIZES = r"(Tiny|Small|Medium|Large|Huge|Gargantuan)"
SMALL_WORDS = {"of", "the", "in", "and", "or", "to", "a", "on", "with", "for", "at", "as", "by"}


def slug(name):
    return re.sub(r"[^a-z0-9]+", "-", name.lower()).strip("-")


def is_entry_name(n):
    if len(n) > 90 or not n:
        return False
    for w in re.split(r"[ /]", n):
        w2 = w.strip("(),")
        if not w2 or w2.lower() in SMALL_WORDS or re.match(r"^[0-9+\u2013\-]+$", w2) or w2.endswith("/Day") or w2.lower() == "lair":
            continue
        if not w2[0].isupper():
            return False
    return True


def split_entries(lines):
    entries = []
    for ln in lines:
        m = re.match(r"^(.{1,90}?)\. (.*)$", ln)
        if m and is_entry_name(m.group(1)) and (not entries or True):
            entries.append({"name": m.group(1).strip(), "text": m.group(2).strip()})
        elif entries:
            entries[-1]["text"] += " " + ln.strip()
        else:
            entries.append({"name": "", "text": ln.strip()})
    return entries


def merge_after_colon(entries):
    """An entry that ends with ':' and says "roll 1dN" introduces exactly N listed effects ("Clinging Flames. ..."): those N entries
    are that action's table, not separate actions. Without a die the entries stay separate."""
    for i, e in enumerate(entries):
        d = re.search(r"roll 1d(\d+)", e["text"]) if e["name"] and e["text"].rstrip().endswith(":") else None
        if d and i + int(d.group(1)) < len(entries) + 0:
            n = int(d.group(1))
            rest = " ".join((x["name"] + ". " if x["name"] else "") + x["text"] for x in entries[i + 1:i + 1 + n])
            return entries[:i] + [{"name": e["name"], "text": e["text"] + " " + rest}] + entries[i + 1 + n:]
    return entries


def clean(s):
    s = s.replace("\u00a0", " ")
    s = re.sub(r"\s+([,.;:)])", r"\1", s)
    s = re.sub(r"\(\s+", "(", s)
    return re.sub(r"\s+", " ", s).strip()


def parse_block(lines, name, typeline, word_text=False):
    m = {"name": name}
    # a stat block that runs onto the next page repeats its heading ("Adult Gold Dragon (continued)") in the middle of a sentence
    lines = [re.sub(r"\s*" + re.escape(name) + r" \(continued\)\s*", " ", l) for l in lines]
    t = re.match(SIZES + r"(?: or \w+)? ([^,]+), (.+)$", typeline)
    if not t:
        raise ValueError("type line")
    m["size"], m["type"], m["alignment"] = t.group(1), t.group(2).strip(), t.group(3).strip()
    body = [l.strip() for l in lines if l.strip() not in ("Ability", "Score", "Mod", "Save")]
    # Word tables put each ability cell on its own line: "Str", "24", "+7", "+7"
    j = 0
    while j < len(body) - 3:
        if body[j] in ("Str", "Dex", "Con", "Int", "Wis", "Cha") and all(re.match(r"^[+\-\u2212]?\d+$", x) for x in body[j + 1:j + 4]):
            body[j:j + 4] = [" ".join(body[j:j + 4])]
        j += 1
    ab = {}
    sect = None
    sections = {"Traits": [], "Actions": [], "Bonus Actions": [], "Reactions": [], "Legendary Actions": []}
    for ln in body:
        if not ln:
            continue
        if ln in sections:
            sect = ln
            continue
        if sect:
            sections[sect].append(ln)
            continue
        if (a := re.match(r"^AC (\d+) Initiative ([+\-\u2212]\d+) \((\d+)\)", ln)):
            m["ac"] = int(a.group(1))
            m["initiative"] = {"Mod": int(a.group(2).replace("\u2212", "-")), "Score": int(a.group(3))}
        elif (a := re.match(r"^HP (\d+) \(([^)]+)\)", ln)):
            m["hp"], m["hit_points_formula"] = int(a.group(1)), clean(a.group(2))
        elif ln.startswith("Speed "):
            m["speed"] = clean(ln[6:])
        elif (a := re.match(r"^(Str|Dex|Con|Int|Wis|Cha) (\d+) ([+\-\u2212]\d+) ([+\-\u2212]\d+)", ln)):
            ab[a.group(1).lower()] = {"score": int(a.group(2)), "save": int(a.group(4).replace("\u2212", "-"))}
        elif ln.startswith("Skills "):
            m["skills"] = clean(ln[7:])
        elif ln.startswith("Vulnerabilities "):
            m["damage_vulnerabilities"] = clean(ln[16:])
        elif ln.startswith("Resistances "):
            m["damage_resistances"] = clean(ln[12:])
        elif ln.startswith("Immunities "):
            parts = clean(ln[11:]).split(";", 1)
            conds = {"blinded", "charmed", "deafened", "exhaustion", "frightened", "grappled", "incapacitated", "invisible", "paralyzed", "petrified", "poisoned", "prone", "restrained", "stunned", "unconscious"}
            first = [x.strip() for x in parts[0].split(",")]
            dmg = [x for x in first if x.lower() not in conds]
            cond = [x for x in first if x.lower() in conds]
            if len(parts) > 1:
                cond += [x.strip() for x in parts[1].split(",")]
            # "Poisoned" is a damage type only as "Poison"; a list of conditions without ';' is the condition list
            if dmg:
                m["damage_immunities"] = ", ".join(dmg)
            if cond:
                m["condition_immunities"] = ", ".join(cond)
        elif ln.startswith("Gear "):
            m["gear"] = clean(ln[5:])
        elif ln.startswith("Senses "):
            m["senses"] = clean(ln[7:])
        elif ln.startswith("Languages "):
            m["languages"] = clean(ln[10:])
        elif (a := re.match(r"^CR ([0-9/]+) \(XP ([\d,]+)(?:, or ([\d,]+) in lair)?; PB \+(\d+)\)", ln)):
            m["cr"] = a.group(1)
            m["xp"] = int(a.group(2).replace(",", ""))
            m["xp_lair"] = int(a.group(3).replace(",", "")) if a.group(3) else 0
            m["proficiency_bonus"] = int(a.group(4))
    last = [k for k in ("Legendary Actions", "Reactions", "Bonus Actions", "Actions", "Traits") if sections[k]]
    if last and word_text:
        # The Word text runs on past the block's last entry into artist credits, the next monster's
        # lore header and lore paragraphs, none of which have entry names. Unnamed lines between
        # named entries are continuations of the previous entry (split_entries folds them in); junk
        # starts at the first short title-case line (a credit or lore header), a Habitat line or a
        # bare dice-table line, and everything from there on is lore. A last entry's own trailing
        # continuation line sits before the first junk line and survives.
        def named(l):
            mm = re.match(r"^(.{1,90}?)\. ", l)
            return bool(mm) and is_entry_name(mm.group(1))
        connectors = {"of", "the", "and", "a", "an", "in", "on", "or", "by", "for", "to", "with", "from"}
        def is_junk(l):
            if l.startswith("Habitat:") or re.match(r"^\d*d\d+\s*$", l):
                return True
            if l.startswith("\u2014") or re.search(r"[.!?]\s*\u2014[A-Z\u2018]", l):
                return True  # an epigraph's attribution, or a quote carrying one on the same line
            words = l.split()
            return (len(l) <= 50 and ". " not in l and not any(ch.isdigit() for ch in l)
                    and 1 <= len(words) <= 5
                    and all(w[:1].isupper() or w.lower() in connectors for w in words))
        out, junk_seen, attribution_seen = [], False, False
        for l in sections[last[0]]:
            if named(l):
                out.append(l)
            elif not junk_seen and not is_junk(l):
                out.append(l)
            else:
                junk_seen = junk_seen or is_junk(l)
                attribution_seen = attribution_seen or l.startswith("\u2014")
        if attribution_seen:
            # An attribution only ever closes a flavor quote: the quote line(s) it signs are the
            # unnamed line(s) right before it - drop them back to the last named entry.
            while out and not named(out[-1]):
                out.pop()
        sections[last[0]] = out
    need = ("ac", "initiative", "hp", "speed", "cr")
    if any(k not in m for k in need) or len(ab) != 6:
        raise ValueError("missing header fields: " + ",".join(k for k in need if k not in m) + f" abilities={len(ab)}")
    m["abilities"] = ab
    for key, label in (("traits", "Traits"), ("actions", "Actions"), ("bonus_actions", "Bonus Actions"), ("reactions", "Reactions")):
        m[key] = [{"name": clean(e["name"]), "text": clean(e["text"])} for e in merge_after_colon(split_entries(sections[label]))]
        if any(not e["name"] for e in m[key]):
            raise ValueError("unnamed entry in " + label)
    leg = split_entries(sections["Legendary Actions"])
    if leg and leg[0]["name"] == "":
        m["legendary_intro"] = clean(leg[0]["text"])
        leg = leg[1:]
    elif leg and leg[0]["name"].startswith("Legendary Action Uses"):
        m["legendary_intro"] = clean(leg[0]["name"] + ". " + leg[0]["text"])
        leg = leg[1:]
    m["legendary_actions"] = [{"name": clean(e["name"]), "text": clean(e["text"])} for e in leg]
    return m


def cr_value(cr):
    return float(Fraction(cr))


def main(out, min_cr, files):
    os.makedirs(out, exist_ok=True)
    ok = skipped = 0
    for f in files:
        L = [re.sub(r"\s+", " ", l).strip() for l in open(f, encoding="utf8").read().replace("\u00a0", " ").split("\n")]
        word_text = "  " not in open(f, encoding="utf8").read()[:20000]  # Word text (no HTML spacing): no page breaks, lore runs on
        starts = [i for i, l in enumerate(L) if re.match(r"^\s*AC \d+ Initiative", l)]
        for n, i in enumerate(starts):
            name, typeline = L[i - 2].strip(), L[i - 1].strip()
            end = starts[n + 1] - 2 if n + 1 < len(starts) else len(L)
            blk, blank, seen_cr = [], 0, False
            for l in L[i:end]:
                if not l.strip():
                    blank += 1
                    # A blank run ends the block only after the CR line: before it, big gaps are
                    # verticalized ability-table layout (the two halves of the table are separated
                    # by blank runs in some Word exports), not the next monster's lore.
                    if blank >= 3 and seen_cr:
                        break
                    continue
                blank = 0
                if l.startswith("CR "):
                    seen_cr = True
                if seen_cr and (re.match(r"^\s*\d{1,3}\s*$", l) or (re.match(r"^\s*[A-Z][A-Z' \-]{3,}\s*$", l) and l.strip() != "SCORE MOD SAVE")):
                    break
                blk.append(l)
            try:
                m = parse_block(blk, name, typeline, word_text)
            except Exception as e:  # never guess a partial stat block
                print(f"SKIP {os.path.basename(f)}: {name!r}: {e}", file=sys.stderr)
                skipped += 1
                continue
            if cr_value(m["cr"]) >= float(min_cr):
                m["source_note"] = f"2024 Monster Manual, supplied reconstruction text ({os.path.basename(f)}); parsed by tools/parse_mm_statblocks.py"
                json.dump({"source_note": m.pop("source_note"), "monsters": [m]}, open(os.path.join(out, slug(name) + ".json"), "w"), indent=1)
                ok += 1
    print(f"parsed {ok} monsters with CR >= {min_cr}; skipped (did not parse) {skipped}", file=sys.stderr)


if __name__ == "__main__":
    main(sys.argv[1], sys.argv[2], sys.argv[3:])
