"""Shared helpers for the SRD extractors: rebuild tables from the lines of a PDF page.

A table line is a (x, y, text, size, bold) tuple. The helpers split a run of table lines into tables
(title, header, rows) and merge a table that continues in the next column.
"""

import re


def sort_lines(lines):
    """Sort (x, y, ...) lines by height, treating lines within 2.5 pt as one line (a cell may sit a point higher than its neighbour)."""
    lines = sorted(lines, key=lambda l: l[1])
    band, anchor = [], None
    for l in lines:
        if anchor is None or l[1] - anchor > 2.5:
            anchor = l[1]
        band.append(anchor)
    return [l for _, l in sorted(zip(band, lines), key=lambda z: (z[0], z[1][0]))]


def build_table(run):
    """run: table lines of one column as (x, y, text, size, bold) in reading order -> {"title", "rows"}.

    Title: bold 10.5 pt. Header: bold 9.2 pt lines; header lines close together in x form one column (a header
    cell can wrap onto two lines). Every other line goes to the column whose header is nearest to it. A line of
    the same height belongs to the same row; a line with no cell in the first column continues the row above
    (a cell that wraps), and so does a lone lower-case first-column line ("Viewed once or" / "described").
    """
    title = ""
    lines = list(run)
    while lines and lines[0][4] and lines[0][3] >= 10.4:
        title = (title + " " + lines[0][2]).strip()
        lines.pop(0)
    header = []
    while lines and lines[0][4] and lines[0][3] < 9.4:  # header cells are 9.2 pt; bold body cells are 9.5 pt
        header.append(lines.pop(0))
    if header:  # header cells of a table as wide as the page sit later in the list, inside the band of the header lines
        lo, hi = min(h[1] for h in header) - 8, max(h[1] for h in header) + 3
        rest = []
        for l in lines:
            (header if l[4] and l[3] < 9.4 and lo <= l[1] <= hi else rest).append(l)
        lines = rest
    if not lines and not header:
        return None
    lines = sort_lines(lines)

    def bands(ls):
        out, anchor = [], None
        for l in ls:
            if anchor is None or l[1] - anchor > 2.5:
                out.append([])
                anchor = l[1]
            out[-1].append(l)
        return out

    def cluster(xs, gap):
        out = []
        for x in sorted(xs):
            if out and x - out[-1][-1] <= gap:
                out[-1].append(x)
            else:
                out.append([x])
        return [min(c) for c in out]

    if header:
        anchors = cluster([h[0] for h in header], 14)
    else:
        widest = max(bands(lines), key=len)
        anchors = cluster([l[0] for l in widest], 6)
    col_of = lambda x: min(range(len(anchors)), key=lambda k: abs(anchors[k] - x))
    ncols = len(anchors)

    def put(cells, c, text):
        if cells[c]:
            cells[c] = cells[c][:-1] + text if cells[c].endswith("-") and text[:1].islower() else cells[c] + " " + text
        else:
            cells[c] = text

    rows = []  # each row: list of cell texts
    last_y = None
    for band in bands(lines):
        by_col = {}
        for x, y, text, size, bold in sorted(band, key=lambda l: l[0]):
            by_col.setdefault(col_of(x), []).append(text)
        y0 = band[0][1]
        starts = 0 in by_col
        lone_lower = starts and len(by_col) == 1 and (by_col[0][0][:1].islower() or by_col[0][0][:1] == "(") and rows and y0 - last_y <= 13.5
        if starts and not lone_lower:
            cells = [""] * ncols
            for c, texts in by_col.items():
                for tx in texts:
                    put(cells, c, tx)
            rows.append(cells)
        elif rows:
            for c, texts in by_col.items():
                for tx in texts:
                    put(rows[-1], c, tx)
        last_y = y0
    out_rows = []
    if header:
        hdr = [""] * ncols
        for x, y, text, size, bold in sorted(header, key=lambda h: (round(h[1] / 4), h[0])):
            put(hdr, col_of(x), text)
        out_rows.append([h.strip() for h in hdr])
    # lines below the last row that sit only in the first column are a note under the table (for example "*Stat blocks ...")
    notes = []
    while len(rows) > 1 and ncols > 1 and all(not c for c in rows[-1][1:]):
        notes.insert(0, rows.pop()[0])
    out_rows.extend(rows)
    return {"title": title, "rows": out_rows, "notes": [" ".join(notes)] if notes else []}


def blocks_of(lines, join_body):
    """Body lines -> paragraphs (strings) and tables ({"table": {...}}) in order."""
    blocks, seg = [], []

    def flush_text():
        nonlocal seg
        if seg:
            blocks.extend(join_body(seg))
            seg = []

    i = 0
    while i < len(lines):
        ln = lines[i]
        if isinstance(ln, tuple):
            flush_text()
            col = ln[3]
            run = []
            while i < len(lines) and isinstance(lines[i], tuple) and lines[i][3] == col:
                run.append((lines[i][1], lines[i][2], lines[i][4], lines[i][5], lines[i][6]))
                i += 1
            parts, cur_part, seen_data, head_ys = [], [], False, []
            for ln_ in run:  # (x, y, text, size, bold)
                is_title = ln_[4] and ln_[3] >= 10.4
                is_head = ln_[4] and ln_[3] < 9.4
                # a title, or a header lower than the table's own header, starts another table; header cells at the
                # same height belong to the same table (a table as wide as the page has cells in both columns)
                # (a header can have two lines, so "the same height" means inside the band of the header lines)
                if cur_part and seen_data and (is_title or (is_head and (not head_ys or not (min(head_ys) - 8 <= ln_[1] <= max(head_ys) + 3)))):
                    parts.append(cur_part)
                    cur_part, seen_data, head_ys = [], False, []
                if is_head and not seen_data:
                    head_ys.append(ln_[1])
                cur_part.append(ln_)
                if not ln_[4]:
                    seen_data = True
            if cur_part:
                parts.append(cur_part)
            for part in parts:
                tbl = build_table(part)
                if tbl:
                    prev = blocks[-1] if blocks else None
                    if not tbl["title"] and prev and isinstance(prev, dict) and "table" in prev and prev["table"]["rows"] and tbl["rows"] and tbl["rows"][0] == prev["table"]["rows"][0]:
                        prev["table"]["rows"].extend(tbl["rows"][1:])  # the table continues in the next column under its repeated header
                        blocks.extend(tbl.get("notes", []))
                    else:
                        notes = tbl.pop("notes", [])
                        blocks.append({"table": tbl})
                        blocks.extend(notes)
            continue
        seg.append(ln)
        i += 1
    flush_text()
    return blocks


def flat_blocks(blocks):
    """Blocks -> plain paragraphs; a table becomes its title and one line per row."""
    out = []
    for b in blocks:
        if isinstance(b, dict) and "table" in b:
            tb = b["table"]
            if tb["title"]:
                out.append(tb["title"])
            out.extend(" | ".join(r) for r in tb["rows"])
        else:
            out.append(b)
    return out


STAT_LABELS = ("AC", "HP", "Speed", "Initiative", "Skills", "Saving Throws", "Resistances", "Vulnerabilities", "Immunities",
               "Damage Immunities", "Condition Immunities", "Senses", "Languages", "CR", "Gear", "Proficiency Bonus")


def build_statblock(lines):
    """lines: (text, spans, x, y) of the stat block in reading order."""
    sb = {"name": "", "type": "", "stats": [], "abilities": [], "sections": []}
    grid = []
    section = None
    entry = None

    def add_text(e, text):
        e["text"] = e["text"][:-1] + text if e["text"].endswith("-") and text[:1].islower() else (e["text"] + " " + text).strip()

    for text, spans, x, y in lines:
        f0, size0 = spans[0][0], spans[0][1]
        txt = text.strip().replace(" ", " ")
        if abs(size0 - 14.8) < 0.3:
            sb["name"] = (sb["name"] + " " + txt).strip()
        elif "Italic" in f0 and "Bold" not in f0 and "Optima" in f0 and abs(size0 - 9.0) < 0.3 and not sb["stats"]:
            sb["type"] = (sb["type"] + " " + txt).strip()
        elif abs(size0 - 9.8) < 0.15 or abs(size0 - 6.0) < 0.15 or "SC700" in f0:
            if abs(size0 - 6.0) > 0.15:
                grid.append(txt)
        elif f0.endswith("GillSans") and abs(size0 - 12.0) < 0.3:
            section = {"title": txt, "entries": []}
            sb["sections"].append(section)
            entry = None
        elif "Optima-Bold" in f0 and "Italic" not in f0 and abs(size0 - 9.0) < 0.3:
            label = next((l for l in sorted(STAT_LABELS, key=len, reverse=True) if txt.startswith(l + " ") or txt == l), None)
            if label:
                sb["stats"].append({"label": label, "text": txt[len(label):].strip()})
            elif sb["stats"]:
                sb["stats"][-1]["text"] = (sb["stats"][-1]["text"] + " " + txt).strip()
        elif "BoldItalic" in f0 and section is not None:
            name = spans[0][2].strip()
            rest = "".join(s[2] for s in spans[1:]).strip()
            entry = {"name": name.rstrip("."), "text": rest}
            section["entries"].append(entry)
        elif section is None and sb["stats"] and "Optima" in f0:
            sb["stats"][-1]["text"] = (sb["stats"][-1]["text"] + " " + txt).strip()  # a wrapped stat line
        elif entry is not None:
            add_text(entry, txt)
    g = " ".join(grid).replace("\t", " ").replace("\u2212", "-")
    for m in re.finditer(r"(Str|Dex|Con|Int|Wis|Cha)\s+(\d+)\s+([+-]\d+)\s+([+-]\d+)", g):
        sb["abilities"].append({"ability": m.group(1), "score": int(m.group(2)), "mod": m.group(3).replace("-", "\u2212"), "save": m.group(4).replace("-", "\u2212")})
    return sb
