import json, urllib.request, random, base64, zlib, struct, sys
from target import BASE as SERVER, CTX  # where the test runs (target.py)

from target import LOG_DIR as D  # where the logs go (ARPG_LOG_DIR)
# a step the server marks optional (the starting level, the shop) is left as it is unless this script follows it by name
# (the portrait: the upload is tested); "left open" counts only the required steps
FOLLOWED_OPTIONAL = {"portrait"}
def open_step(x): return not x.get("done") and (not x.get("optional") or x.get("key") in FOLLOWED_OPTIONAL)

log = open(D + "build_tests.jsonl", "w")
TOKEN = None


def call(m, p, b=None, label=""):
    h = {"Content-Type": "application/json"}
    if TOKEN:
        h["Authorization"] = "Bearer " + TOKEN
    r = urllib.request.Request(SERVER + p, data=json.dumps(b).encode() if b is not None else None, method=m, headers=h)
    try:
        with urllib.request.urlopen(r, context=CTX) as x:
            code, t = x.status, x.read().decode()
    except urllib.error.HTTPError as e:
        code, t = e.code, e.read().decode()
    try:
        js = json.loads(t)
    except Exception:
        js = {"_raw": t[:500]}
    log.write(json.dumps({"label": label, "method": m, "path": p, "status": code, "request": (b if not (isinstance(b, dict) and "image_base64" in b) else {"image_base64": "(hidden)"}), "response": js}) + "\n")
    return code, js


def png(w, h):
    raw = b"".join(b"\x00" + bytes([90, 120, 160]) * w for _ in range(h))
    def ch(t, d):
        c = struct.pack(">I", len(d)) + t + d
        return c + struct.pack(">I", zlib.crc32(t + d) & 0xffffffff)
    return b"\x89PNG\r\n\x1a\n" + ch(b"IHDR", struct.pack(">IIBBBBB", w, h, 8, 2, 0, 0, 0)) + ch(b"IDAT", zlib.compress(raw, 9)) + ch(b"IEND", b"")


IMG = base64.b64encode(png(1024, 1280)).decode()

# class, background, species: every class, every background and every species at least once
BUILDS = [
    ("barbarian", "soldier", "goliath"), ("bard", "criminal", "tiefling"), ("cleric", "acolyte", "dwarf"),
    ("druid", "sage", "elf"), ("fighter", "soldier", "human"), ("monk", "criminal", "halfling"),
    ("paladin", "acolyte", "dragonborn"), ("ranger", "sage", "orc"), ("rogue", "criminal", "gnome"),
    ("sorcerer", "sage", "dragonborn"), ("warlock", "criminal", "elf"), ("wizard", "sage", "gnome"),
]

r = call("POST", "/api/register", {"name": "BuildTest%d" % random.randint(1000, 9999), "password": "x"})[1]
TOKEN = r["token"]
results = []
for cls, bg, sp in BUILDS:
    tag = f"{cls}/{bg}/{sp}"
    res = {"build": tag, "problems": []}
    code, d = call("POST", "/api/character-builder", {"name": f"Test {cls.title()} {random.randint(100000, 999999)}"}, tag)
    P = "/api/character-builder/%d" % d["draft_id"]
    call("POST", P, {"class": cls}, tag)
    call("POST", P, {"background": bg}, tag)
    call("POST", P, {"species": sp}, tag)
    # let the server's suggestions fill every remaining choice, step by step
    last = None
    for i in range(20):
        code, v = call("POST", P, {"use_suggestion": True}, tag)
        if "Every step is done" in (v.get("next") or ""):
            break
        cs = v.get("current_step") or {}
        key = cs.get("key")
        if key == "details":
            call("POST", P, {"alignment": "Neutral Good"}, tag)
        if key == "backstory":
            call("POST", P, {"backstory": "Para one.\n\nPara two.\n\nPara three."}, tag)
        if key == last and key not in ("details", "backstory"):
            res["problems"].append(f"stuck at {cs.get('title')}: {cs.get('problems')} {[c.get('problem') for c in cs.get('choices', []) if c.get('problem')]}")
            break
        last = key
    else:
        res["problems"].append("builder did not finish in 20 rounds")
    code, f = call("POST", P + "/finish", {}, tag)
    if not f.get("character_id"):
        res["problems"].append(f"finish failed: {f.get('error')} {f.get('message')} {json.dumps(f)[:300]}")
        results.append(res)
        continue
    cid = f["character_id"]
    res["hp"], res["ac"], res["scores"] = f.get("hp"), f.get("ac"), f.get("ability_scores")
    # setup: follow each step's request
    for rnd in range(8):
        code, s = call("GET", f"/api/characters/{cid}/setup", None, tag)
        open_steps = [x for x in s.get("steps", []) if open_step(x)]
        if not open_steps:
            break
        acted = False
        for st in open_steps:
            k = st["key"]
            if k == "portrait":
                c2, o = call("POST", f"/api/characters/{cid}/portrait", {"image_base64": IMG}, tag)
                if c2 >= 400 or o.get("error"):
                    res["problems"].append(f"portrait: {o}")
                acted = True
            elif k == "auto_equip":
                c2, o = call("POST", "/api/characters/auto-equip", {"character_id": cid, "answer": "yes"}, tag)
                if o.get("needs_choices"):
                    ch = {q["key"]: q["options"][0]["value"] for q in o["needs_choices"]}
                    c2, o = call("POST", "/api/characters/auto-equip", {"character_id": cid, "answer": "yes", "choices": ch}, tag)
                    if o.get("needs_choices"):
                        ch.update({q["key"]: q["options"][0]["value"] for q in o["needs_choices"]})
                        c2, o = call("POST", "/api/characters/auto-equip", {"character_id": cid, "answer": "yes", "choices": ch}, tag)
                bad = [x for x in (o.get("results") or []) if not x.get("done")]
                if o.get("error") or bad or not o.get("success"):
                    res["problems"].append(f"auto-equip: {json.dumps(o)[:400]}")
                res["equipped"] = o.get("now")
                acted = True
            elif st.get("request") and isinstance(st["request"], dict) and st["request"].get("method"):
                rq = st["request"]
                c2, o = call(rq["method"], rq["path"], rq.get("body"), tag)
                if c2 >= 400 or o.get("error"):
                    res["problems"].append(f"setup step {k}: {o.get('error')} {o.get('message')}")
                if st.get("then"):
                    t = st["then"]["request"]
                    c3, o3 = call(t["method"], t["path"], t.get("body"), tag)
                    if c3 >= 400 or o3.get("error"):
                        res["problems"].append(f"setup step {k} (then): {o3.get('error')} {o3.get('message')}")
                acted = True
            elif k == "gear":
                pass  # reminders without a single request (handled by auto-equip)
        if not acted:
            break
    code, s = call("GET", f"/api/characters/{cid}/setup", None, tag)
    left = [(x["key"], x.get("title")) for x in s.get("steps", []) if open_step(x)]
    if left:
        res["problems"].append(f"setup still open: {left}")
    code, sheet = call("GET", f"/api/characters/{cid}", None, tag)
    res["finished"] = sheet.get("character_finished")
    res["alignment"] = sheet.get("alignment")
    res["prepared"] = sheet.get("prepared_count")
    if not sheet.get("character_finished"):
        code, f2 = call("GET", f"/api/characters/{cid}", None, tag)
        res["problems"].append(f"not finished: to_finish={sheet.get('to_finish')}")
    results.append(res)

json.dump(results, open(D + "build_results.json", "w"), indent=1)
for r in results:
    print(("OK  " if not r["problems"] else "FAIL"), r["build"], "| HP", r.get("hp"), "AC", r.get("ac"), "| equipped", r.get("equipped"), "| finished", r.get("finished"))
    for p in r["problems"]:
        print("     -", p[:400])
import sys
sys.exit(1 if any(r["problems"] for r in results) else 0)  # a failed build fails the run (audit AUD-48)
