# expected_turn (Matt, Nov 16): combat/next, combat/skip and narrate's advance_turn may name the turn they mean to end
# ({"round": R, "turn_index": I}, as GET /api/campaigns/{id}/combat shows it). A retry that arrives after the first change
# finished is refused (409 turn_already_moved, with the turn it is now) and changes nothing; overlapping retries change the turn
# once; without the field a request behaves as before. With the turn schedule on and off. The check is in the one turn owner
# (advanceTurn, cmd/server/turn_advance_2024.go); the Go test TestExpectedTurnInTheTurnOwner covers the same against Postgres.
# Requests only.  Local: BASE=http://localhost:8099 python3 expected_turn_test.py   Staging: ARPG_TARGET=staging python3 ...
import json, sys, threading
from ah import call, register, build, only
import ah; ah.KEEP_SCHEDULE = True  # this suite sets the turn schedule itself

only("local", "staging", why="it sends refusals and parallel turn changes")
passed = failed = 0
fails = []
def check(name, ok, detail=None):
    global passed, failed
    if ok:
        passed += 1; print("PASS", name)
    else:
        failed += 1; fails.append(name); print("FAIL", name, "|", json.dumps(detail, default=str)[:700])

G = register("EtGm")["token"]
P = register("EtPl")["token"]
cid, f = build(P, "fighter", "soldier", "human", "Et")
check("a character is built", bool(cid), f)
c, s = call("GET", f"/api/characters/{cid}/setup", None, P)
for st in s.get("steps", []):
    if st.get("key") == "portrait":
        call("POST", f"/api/characters/{cid}/portrait", {"skip": True}, P)
c, camp = call("POST", "/api/campaigns", {"name": "Expected Turn %s" % cid, "setting": "A ford.", "max_players": 4}, G)
C = camp.get("campaign_id")
call("POST", f"/api/campaigns/{C}/join", {"character_id": cid}, P)
c, j = call("POST", f"/api/campaigns/{C}/start", None, G)
check("the campaign starts", c == 200, j)
B = f"/api/campaigns/{C}/combat"

def now():
    st = call("GET", B, None, G)[1]
    return st.get("round"), st.get("current_turn_index"), st.get("current_turn")

def turn(r, i):
    return {"round": r, "turn_index": i}

def fight():
    call("POST", B + "/end", {"award_xp": False}, G)
    c, j = call("POST", B + "/start", {}, G)
    c2, j2 = call("POST", B + "/add", {"combatants": [{"name": n, "monster_key": "goblin", "initiative": 40 - k, "hp": 50} for k, n in enumerate(["Gob A", "Gob B", "Gob C", "Gob D"])]}, G)
    # the fight started on the character's turn (the goblins joined ahead of it): the character dodges, the GM ends the turn
    c3, j3 = call("POST", "/api/action", {"character_id": cid, "action": "dodge"}, P)
    c4, j4 = call("POST", B + "/next", {}, G)
    return c == 200 and c2 == 200 and c3 == 200 and c4 == 200, (j, j2, j3, j4)

for state in ("on", "off"):
    c, j = call("PATCH", f"/api/campaigns/{C}/schedule", {"state": state}, G)
    check(f"[{state}] the schedule is {state}", c == 200 and j.get("state") == state, j)
    ok, d = fight()
    check(f"[{state}] a fight with four goblins first", ok and now()[2] == "Gob A", [d, now()])
    r, i, _ = now()
    # next with the turn it is: the turn ends
    c, j = call("POST", B + "/next", {"expected_turn": turn(r, i)}, G)
    check(f"[{state}] next with the current turn ends it", c == 200 and j.get("current_turn") == "Gob B" and j.get("turn_index") == i + 1, j)
    after = now()
    # a retry of the same request after it finished: refused, nothing changed, the reply says whose turn it is
    c, j = call("POST", B + "/next", {"expected_turn": turn(r, i)}, G)
    check(f"[{state}] a retried next (the old expected_turn): 409 turn_already_moved with the turn it is now",
          c == 409 and j.get("error") == "turn_already_moved" and j.get("current_turn") == "Gob B" and j.get("turn_index") == i + 1 and j.get("round") == r
          and f'"turn_index": {i + 1}' in j.get("message", ""), j)
    check(f"[{state}] ... and the turn is unchanged", now() == after, [now(), after])
    c, j = call("POST", B + "/skip", {"expected_turn": turn(r, i)}, G)
    check(f"[{state}] a retried skip: 409 turn_already_moved, the turn unchanged", c == 409 and j.get("error") == "turn_already_moved" and now() == after, j)
    narr = f"The goblins hesitate ({state}, retry)."
    c, j = call("POST", "/api/gm/narrate", {"campaign_id": C, "narration": narr, "advance_turn": True, "expected_turn": turn(r, i)}, G)
    feed = json.dumps(call("GET", f"/api/campaigns/{C}/feed", None, G)[1])
    check(f"[{state}] a retried narrate with advance_turn: 409 turn_already_moved, the narration not recorded, the turn unchanged",
          c == 409 and j.get("error") == "turn_already_moved" and j.get("narration_recorded") is False and narr not in feed and now() == after, j)
    # overlapping retries with the turn it is now: the turn moves once
    r, i, _ = now()
    res = []
    def go():
        res.append(call("POST", B + "/next", {"expected_turn": turn(r, i)}, G))
    ts = [threading.Thread(target=go) for _ in range(5)]
    [t.start() for t in ts]; [t.join() for t in ts]
    ok = [x for x in res if x[0] == 200]
    refused = [x[1].get("error") for x in res if x[0] != 200]
    check(f"[{state}] 5 overlapping nexts naming one turn: one ends it, the others are refused",
          len(ok) == 1 and all(e in ("turn_change_in_progress", "turn_already_moved") for e in refused), [(x[0], x[1].get("error")) for x in res])
    check(f"[{state}] ... the turn moved one place", now()[1] == i + 1 and now()[2] == "Gob C", now())
    # narrate with the turn it is now ends it
    r, i, _ = now()
    c, j = call("POST", "/api/gm/narrate", {"campaign_id": C, "narration": f"Gob C lunges ({state}).", "advance_turn": True, "expected_turn": turn(r, i)}, G)
    check(f"[{state}] narrate with the current turn ends it", c == 200 and j.get("turn_advanced") and j.get("current_turn") == "Gob D", j)
    # without expected_turn: as before
    c, j = call("POST", B + "/next", {}, G)
    check(f"[{state}] next without expected_turn ends the current turn as before", c == 200 and now()[2] != "Gob D", [j.get("error"), now()])
    # an incomplete expected_turn, or one without advance_turn, is refused with nothing changed
    before = now()
    for body in ({"expected_turn": {"round": before[0]}}, {"expected_turn": turn(0, 0)}, {"expected_turn": turn(1, -1)}, {"expected_turn": {"round": 1, "turn_index": 0, "id": 3}}):
        c, j = call("POST", B + "/next", body, G)
        check(f"[{state}] next with {json.dumps(body)} is refused (400)", c == 400 and j.get("error") in ("invalid_expected_turn", "unknown_field"), j)
    c, j = call("POST", "/api/gm/narrate", {"campaign_id": C, "narration": "x", "expected_turn": turn(*before[:2])}, G)
    check(f"[{state}] expected_turn without advance_turn is refused (400)", c == 400 and j.get("error") == "invalid_expected_turn", j)
    check(f"[{state}] ... and nothing moved", now() == before, [now(), before])

print(f"\n{passed} of {passed + failed} checks passed")
if fails:
    print("FAILED:", fails)
    sys.exit(1)
