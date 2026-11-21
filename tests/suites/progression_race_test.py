# Audit Phase 1.1, stage A (CODE_AUDIT_REPORT.md AUD-07: D-2, B-18): a progression request claims its step with one guarded
# update and adds its effects to the values as they are then, so requests sent at the same time (agents retry and run in
# parallel) can't take a level twice, spend points twice, lose a feat, apply a subclass's Hit Points twice, lose the CON bonus,
# or lose XP. Each case sends parallel requests and then reads the character.
# Local:   python3 progression_race_test.py      Staging: ARPG_TARGET=staging python3 progression_race_test.py
import json, random, sys, threading
from ah import call, register, build, BASE, only

only("local", "staging", why="it sends many requests at once")
passed = failed = 0
fails = []
def check(name, ok, detail=None):
    global passed, failed
    if ok:
        passed += 1; print("PASS", name)
    else:
        failed += 1; fails.append(name); print("FAIL", name, "|", json.dumps(detail)[:400])

def parallel(n, fn):
    out = [None] * n
    def run(i): out[i] = fn()
    ts = [threading.Thread(target=run, args=(i,)) for i in range(n)]
    [t.start() for t in ts]; [t.join() for t in ts]
    return out

G = register("PrGm")["token"]
c, j = call("POST", "/api/campaigns", {"name": f"PrC {random.randint(1000,9999)}", "setting": "t", "max_players": 6}, G)
C = j["campaign_id"]
def pc(cls, bg, sp, label):
    tok = register("PrPl")["token"]
    cid, _ = build(tok, cls, bg, sp, label)
    c, j = call("POST", f"/api/campaigns/{C}/join", {"character_id": cid}, tok)
    assert c == 200, j
    return cid, tok
fighter, ftok = pc("fighter", "soldier", "human", "PrFi")
sorc, stok = pc("sorcerer", "sage", "tiefling", "PrSo")
xpman, xtok = pc("rogue", "criminal", "halfling", "PrXp")
conman, ctok = pc("fighter", "soldier", "dwarf", "PrCon")
c, j = call("POST", f"/api/campaigns/{C}/start", {}, G)
assert c == 200, j

def sheet(cid, tok):
    c, j = call("GET", f"/api/characters/{cid}", None, tok)
    return j
def bank(cid, xp):
    c, j = call("POST", "/api/gm/award-xp", {"character_ids": [cid], "xp": xp, "auto_level": False}, G)
    assert c == 200, j

# ---------------------------------------------------------------- level-up five times at once
bank(fighter, 300)  # exactly one level's worth (level 2), banked
s0 = sheet(fighter, ftok)
res = parallel(5, lambda: call("POST", f"/api/characters/{fighter}/level-up", {"class": "fighter"}, ftok))
ok = [r for r in res if r[0] == 200]
s1 = sheet(fighter, ftok)
check(f"5 level-ups at once: one level ({s0.get('level')} -> {s1.get('level')}, {len(ok)} answered 200; the rest refused: level_changed or no_level_available)",
      s1.get("level") == s0.get("level") + 1 and len(ok) == 1 and all(r[1].get("error") in ("level_changed", "no_level_available") for r in res if r[0] != 200), [(r[0], r[1].get("error")) for r in res])
gain = ok[0][1].get("hp_gained") if ok else None
check(f"its Hit Points are added once (+{gain})", gain is not None and s1.get("max_hp") == s0.get("max_hp") + gain, {"before": s0.get("max_hp"), "after": s1.get("max_hp")})
# up to level 4 one at a time, then two feats at once with 2 points
bank(fighter, 2400)  # to 2700: levels 3 and 4
for _ in range(3):
    if sheet(fighter, ftok).get("level", 0) >= 4:
        break
    call("POST", f"/api/characters/{fighter}/level-up", {"class": "fighter"}, ftok)
s2 = sheet(fighter, ftok)
pending = s2.get("pending_asi") or (s2.get("ability_score_improvement") or {}).get("points") or 0
out = []
def feat(body):
    return lambda: call("POST", f"/api/characters/{fighter}/feat", body, ftok)
t = [threading.Thread(target=lambda b=b: out.append(feat(b)())) for b in ({"feat": "grappler", "ability_choice": "str"}, {"feat": "alert"})]
[x.start() for x in t]; [x.join() for x in t]
s3 = sheet(fighter, ftok)
won = [o for o in out if o[0] == 200]
feats3 = json.dumps(s3.get("feats"))
check(f"two feats at once with {pending} points: one taken, the other 409 feats_changed ({[o[0] for o in out]})",
      len(won) == 1 and all(o[1].get("error") == "feats_changed" for o in out if o[0] != 200), [(o[0], o[1].get("error")) for o in out])
check("the feat taken is recorded and the points never go below 0",
      ("grappler" in feats3.lower() or "alert" in feats3.lower()) and (s3.get("pending_asi") or 0) >= 0, {"feats": s3.get("feats"), "pending_asi": s3.get("pending_asi")})

# ---------------------------------------------------------------- subclass four times at once (Draconic Resilience: +1 HP per level)
bank(sorc, 900)
call("POST", f"/api/characters/{sorc}/level-up", {"class": "sorcerer"}, stok)
call("POST", f"/api/characters/{sorc}/level-up", {"class": "sorcerer"}, stok)
s4 = sheet(sorc, stok)
res = parallel(4, lambda: call("POST", "/api/characters/subclass", {"character_id": sorc, "subclass": "draconic"}, stok))
s5 = sheet(sorc, stok)
ok = [r for r in res if r[0] == 200]
check(f"4 subclass picks at once: one ({[r[0] for r in res]})", len(ok) == 1, [(r[0], r[1].get("error")) for r in res])
check(f"Draconic Resilience's Hit Points once (max HP {s4.get('max_hp')} -> {s5.get('max_hp')}, level {s5.get('level')})",
      s5.get("max_hp") == s4.get("max_hp") + s5.get("level", 0), None)

# ---------------------------------------------------------------- four CON improvements of 1 point at once
bank(conman, 2700)
for _ in range(3):
    call("POST", f"/api/characters/{conman}/level-up", {"class": "fighter"}, ctok)
k0 = sheet(conman, ctok)
con0 = (k0.get("stats") or {}).get("con")
res = parallel(4, lambda: call("POST", f"/api/characters/{conman}/asi", {"ability": "con", "points": 1}, ctok))
k1 = sheet(conman, ctok)
con1 = (k1.get("stats") or {}).get("con")
mod = lambda v: (v - 10) // 2
want = k0.get("max_hp") + k1.get("level", 0) * (mod(con1) - mod(con0))
check(f"4 CON improvements at once with 2 points: 2 applied (CON {con0} -> {con1}; {[r[0] for r in res]})", con1 == con0 + 2, None)
check(f"max HP follows the CON modifier exactly ({k0.get('max_hp')} -> {k1.get('max_hp')}, want {want})", k1.get("max_hp") == want, None)

# ---------------------------------------------------------------- XP awards at once
# One command resolves at a time in a campaign (combat_pause_2024.go): an award sent while another is resolving is refused with
# 409 combat_operation_in_progress and changes nothing, so the GM sends it again. Every award answered 200 is counted exactly
# once, none is lost or counted twice; the refused ones are then sent again until all six are in.
x0 = sheet(xpman, xtok)
res = parallel(6, lambda: call("POST", "/api/gm/award-xp", {"character_ids": [xpman], "xp": 100}, G))
codes = [r[0] for r in res]
check(f"6 awards of 100 XP at once: each answered 200 or refused while another resolves ({codes})",
      all(r[0] == 200 or (r[0] == 409 and r[1].get("error") == "combat_operation_in_progress") for r in res), [(r[0], r[1].get("error")) for r in res])
mid = sheet(xpman, xtok)
check(f"every award answered 200 is counted once, a refused one not at all ({x0.get('xp')} -> {mid.get('xp')}, {codes.count(200)} counted)",
      mid.get("xp") == (x0.get("xp") or 0) + 100 * codes.count(200), None)
for _ in range(codes.count(409)):
    for attempt in range(20):
        c, j = call("POST", "/api/gm/award-xp", {"character_ids": [xpman], "xp": 100}, G)
        if c == 200: break
x1 = sheet(xpman, xtok)
check(f"the six awards sent again where refused: all counted ({x0.get('xp')} -> {x1.get('xp')})", x1.get("xp") == (x0.get("xp") or 0) + 600, None)
check(f"the level they reach is gained once (level {x1.get('level')})", x1.get("level") == 2, None)
lvl2_hp = x1.get("max_hp") - x0.get("max_hp")
check(f"that level's Hit Points once (+{lvl2_hp})", 1 <= lvl2_hp <= 10, None)

print(f"\n{passed} of {passed + failed} checks passed")
if fails:
    print("FAILED:", fails)
    sys.exit(1)
