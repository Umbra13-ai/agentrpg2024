#!/usr/bin/env python3
"""Check a high-CR monster fixture against plain text extracted from the supplied Monster Manual chapter (HTML or DOCX).

Usage: verify_fixture_against_book.py fixture.json book_text.txt

Every trait/action/reaction/legendary text and the key stats must appear in the book text after dropping everything except
letters and digits (the reconstruction puts spaces before punctuation and splits cards over lines). Exit 1 on any mismatch.
This proves the fixture matches the supplied reconstruction text; it is not a check against a printed copy of the book.
"""
import json
import re
import sys


def norm(s):
    return re.sub(r"[^a-z0-9]", "", s.lower())


def main(fixture, book):
    m = json.load(open(fixture))["monsters"][0]
    text = norm(open(book, encoding="utf8").read())
    missing, checked = [], 0

    def need(label, s):
        nonlocal checked
        checked += 1
        if norm(str(s)) not in text:
            missing.append(f"{label}: {str(s)[:90]}")

    for sect in ("traits", "actions", "bonus_actions", "reactions", "legendary_actions"):
        for e in m.get(sect) or []:
            need(f"{sect}/{e['name']}", e["text"])
    for k in ("skills", "senses", "languages", "damage_immunities", "damage_resistances", "damage_vulnerabilities",
              "condition_immunities", "speed", "hit_points_formula"):
        if m.get(k):
            need(k, m[k])
    need("ac", f"AC {m['ac']}")
    need("hp", f"HP {m['hp']} ({m['hit_points_formula']})")
    need("cr", f"CR {m['cr']}")
    print(f"{m['name']}: checked {checked} items, {len(missing)} not found in the book text")
    for x in missing:
        print("  MISSING", x)
    return 1 if missing else 0


if __name__ == "__main__":
    sys.exit(main(*sys.argv[1:3]))
