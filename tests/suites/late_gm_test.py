# The turn schedule, stage D (campaign-scheduling-directions.md sections 3, 6, 8, 9 and 11.4; turn-schedule-design.md 1.2, 1.5-1.7):
# the late GM check. Before the round's final check-in a late GM resolves submitted turns only and stands in for no one (the
# stand-in, combat/skip, combat/next and a narration's advance are refused with stand_in_too_early and the time); the status
# names the earlier player a submitted turn waits on, says when the GM is overdue (from the first GM check-in at which a turn
# could resolve, plus 5 minutes), and never shows a player who submitted as missing; reading the status (the schedule and
# GET /api/gm/status) changes nothing; at the final check-in only the unsubmitted, unresolved character needs a stand-in; a
# second resolve re-runs nothing. Section 9: a round that every player submitted closes early; a round opened mid-hour has the
# next hour's :55 as its deadline; a submission for another round is refused and changes nothing. Pause: no stand-ins, the GM
# still resolves. A paused (needs_gm) submitted turn may be finished by the GM before the final check-in. And a campaign whose
# schedule is off plays as before (GET /api/gm/status resolves, a stand-in at once, a turn for the next round, the skip timeout).
# Requests only. It needs the server's test clock (AGENTRPG_TEST_CLOCK=1).
# Local:  BASE=http://localhost:8118 python3 late_gm_test.py      Staging: ARPG_TARGET=staging python3 late_gm_test.py
import json, random, sys
from ah import call, register, build, only
import ah; ah.KEEP_SCHEDULE = True  # this suite sets the turn schedule itself

only("local", "staging", why="it sends refusals and test-clock moves")
passed = failed = 0
fails = []
def check(name, ok, detail=None):
    global passed, failed
    if ok:
        passed += 1; print("PASS", name)
    else:
        failed += 1; fails.append(name); print("FAIL", name, "|", json.dumps(detail, default=str)[:700])

DAY = "2026-11-16T"
G = register("LgGm")["token"]

def setup(tok, cid):
    c, s = call("GET", f"/api/characters/{cid}/setup", None, tok)
    for st in s.get("steps", []):
        if st["key"] == "auto_equip":
            c, o = call("POST", "/api/characters/auto-equip", {"character_id": cid, "answer": "yes"}, tok)
            if o.get("needs_choices"):
                call("POST", "/api/characters/auto-equip", {"character_id": cid, "answer": "yes", "choices": {q["key"]: q["options"][0]["value"] for q in o["needs_choices"]}}, tok)
        elif st["key"] == "portrait":
            call("POST", f"/api/characters/{cid}/portrait", {"skip": True}, tok)

def campaign(n_players, label, max_players=4):
    c, j = call("POST", "/api/campaigns", {"name": f"{label} {random.randint(1000, 99999)}", "setting": "t", "max_players": max_players}, G)
    camp = j["campaign_id"]
    toks, names = {}, {}
    for i, (cls, bg, sp) in enumerate([("fighter", "soldier", "human"), ("cleric", "acolyte", "dwarf"), ("rogue", "criminal", "halfling"), ("wizard", "sage", "elf")][:n_players]):
        tok = register(f"{label}P{i}")["token"]
        cid, _ = build(tok, cls, bg, sp, f"{label}{i}")
        setup(tok, cid)
        c, j = call("POST", f"/api/campaigns/{camp}/join", {"character_id": cid}, tok)
        assert c == 200, j
        toks[cid] = tok
        names[cid] = call("GET", f"/api/characters/{cid}", None, tok)[1].get("name")
    return camp, toks, names

C, tok_of, name_of = campaign(4, "Lg")
B = f"/api/campaigns/{C}/combat"
def clock(hhmm, camp=None):
    c, j = call("POST", f"/api/campaigns/{camp or C}/test-clock", {"at": DAY + hhmm + ":00Z"}, G)
    if c == 404:
        sys.exit("This server has no test clock (start it with AGENTRPG_TEST_CLOCK=1).")
    assert c == 200, j
def sched(tok=G):
    return call("GET", f"/api/campaigns/{C}/schedule", None, tok)[1].get("check_in") or {}
def status(ci, cid):
    return next((x for x in ci.get("characters", []) if x.get("character_id") == cid), {})
def combat():
    return call("GET", B, None, G)[1]
def submit(cid, body, camp=None, toks=None):
    b = dict(body); b.update({"character_id": cid, "campaign_id": camp or C})
    return call("POST", "/api/pending-turn", b, (toks or tok_of)[cid])
def feed_len(camp=None):
    return len(call("GET", f"/api/campaigns/{camp or C}/feed", None, G)[1].get("actions") or [])
def snapshot():
    """everything a status check could change: the fight, the queue, the round's status (minus the clock), the log, the sheets"""
    cs = combat()
    ci = sched()
    ci.pop("now", None)
    q = call("GET", f"/api/pending-turn?campaign_id={C}", None, G)[1].get("queue")
    sheets = [{k: v for k, v in call("GET", f"/api/characters/{p}", None, G)[1].items() if k in ("hp", "status", "conditions", "death_saves")} for p in tok_of]
    return json.dumps([cs.get("round"), cs.get("current_turn_index"), cs.get("turn_order"), q, ci, feed_len(), sheets], sort_keys=True, default=str)
def refused_early(c, j, what):
    check(f"{what}: 409 stand_in_too_early with the final check-in (10:55)", c == 409 and j.get("error") == "stand_in_too_early" and (j.get("final_check_in") or "")[11:16] == "10:55", j)

clock("09:55")
call("POST", f"/api/campaigns/{C}/start", {}, G)
c, j = call("POST", B + "/start", {}, G); check("combat starts at 09:55: round 1, final check-in 10:55", c == 200 and ((sched().get("round") or {}).get("final_check_in") or "")[11:16] == "10:55", sched().get("round"))
c, j = call("POST", B + "/add", {"combatants": [{"name": "Boss", "monster_key": "goblin-warrior", "initiative": 100, "hp": 500},
                                                 {"name": "Tank", "monster_key": "goblin-warrior", "initiative": -20, "hp": 500}]}, G)
order = combat().get("turn_order") or []
P = [e["id"] for e in order if e["id"] in tok_of]
boss, tank = order[0]["id"], order[-1]["id"]
check("round 1 goes on from the first player (Boss joined before it); Tank last", combat().get("current_turn_index") == 1 and order[-1]["name"] == "Tank", [e["name"] for e in order])
p1, p2, p3, p4 = P

# ---------------------------------------------------------------- 10:00: three submit, the second in Initiative doesn't
clock("10:00")
for p in (p4, p3, p1):
    c, j = submit(p, {"steps": [{"action": "dodge"}]})
    check(f"{name_of[p]} submits", c == 200 and j.get("round") == 1, j)
clock("10:12")
ci = sched(tok_of[p3])
check("10:12: the first in Initiative has submitted: it resolves at the GM's next check-in (10:25); the GM isn't overdue yet",
      status(ci, p1).get("status") == "submitted" and (status(ci, p1).get("resolves_at") or "")[11:16] == "10:25" and ci.get("gm_overdue") is None, [status(ci, p1), ci.get("gm_overdue")])
for p in (p3, p4):
    st = status(ci, p)
    check(f"{name_of[p]}: submitted, waiting on {name_of[p2]} (named), never blocked or missing", st.get("status") == "waiting" and (st.get("waiting_on") or {}).get("character_id") == p2 and "blocked" not in st.get("message", "").lower(), st)
check(f"{name_of[p2]}: not submitted (never missing before the final check-in)", status(ci, p2).get("status") == "not_submitted", status(ci, p2))
clock("10:16")
ci = sched(tok_of[p1])
ov = ci.get("gm_overdue") or {}
check("10:16: the GM is overdue for the first player's turn: it could have resolved at 10:10, overdue since 10:15 (the delay is the GM's)",
      ov.get("turn") == name_of[p1] and (ov.get("check_in") or "")[11:16] == "10:10" and (ov.get("since") or "")[11:16] == "10:15" and "not a player's" in ov.get("message", ""), ov)
check("... and the waiting players are still waiting, not overdue or missing", [status(ci, p).get("status") for p in (p3, p4)] == ["waiting", "waiting"], [status(ci, p) for p in (p3, p4)])
# reading the status changes nothing (the schedule and GET /api/gm/status, twice)
s0 = snapshot()
c, gs = call("GET", f"/api/gm/status?campaign_id={C}", None, G)
check("GET /api/gm/status answers with the round's status (check_in) and the GM overdue task", c == 200 and (gs.get("check_in") or {}).get("gm_overdue") and any("overdue" in t for t in gs.get("gm_tasks", [])), [gs.get("gm_tasks"), (gs.get("check_in") or {}).get("gm_overdue")])
check("GET /api/gm/status resolves nothing (no pending_turns.resolved_now)", "resolved_now" not in (gs.get("pending_turns") or {}), gs.get("pending_turns"))
call("GET", f"/api/gm/status?campaign_id={C}", None, G)
sched(tok_of[p2])
s0b = snapshot()
check("reading the status (the schedule, gm/status twice) changed nothing: fight, queue, status, log and sheets the same", s0b == s0,
      [(i, x, y) for i, (x, y) in enumerate(zip(json.loads(s0), json.loads(s0b))) if x != y])
call("GET", "/api/my-turn?character_id=%d" % p1, None, tok_of[p1])
s0c = snapshot()
diff = [i for i, (x, y) in enumerate(zip(json.loads(s0b), json.loads(s0c))) if x != y]
check("a player's GET /api/my-turn changes nothing but its own check-in record in the log (poll)", diff in ([], [5]), diff)

# ---------------------------------------------------------------- 10:47: the late GM, before the final check-in
clock("10:47")
c, j = call("POST", B + "/stand-in", {"steps": [{"action": "dodge"}]}, G)
check("a stand-in for the first player (who submitted) is refused: its queued turn is used (pending_turn_waiting)", c == 409 and j.get("error") == "pending_turn_waiting", j)
c, rep = call("POST", B + "/resolve", {}, G)
done = rep.get("resolved_now") or []
st = rep.get("stopped_at") or {}
check("10:47 resolve: the first player's submitted turn resolves; the walk stops at the second (no submission) and stands in for no one",
      [d.get("character_id") for d in done] == [p1] and st.get("reason") == "awaiting_submission" and st.get("character_id") == p2 and (st.get("final_check_in") or "")[11:16] == "10:55", [done, st])
s1 = snapshot()
refused_early(*call("POST", B + "/stand-in", {"steps": [{"action": "dodge"}]}, G), "a stand-in for the second player before 10:55")
c, j = call("POST", B + "/skip", {}, G)
check("combat/skip of the second player's turn: a player's turn is never skipped with the schedule (Matt 11.11)", c == 409 and j.get("error") == "player_turn_not_skipped", j)
refused_early(*call("POST", B + "/next", {}, G), "combat/next on the second player's turn before 10:55")
c, j = call("POST", "/api/gm/narrate", {"campaign_id": C, "narration": "The fight goes on.", "advance_turn": True}, G)
check("a narration with advance_turn on that turn: 409 stand_in_too_early", c == 409 and j.get("error") == "stand_in_too_early", j)
s1b = snapshot()
check("the refusals changed nothing (but the narration's own record)", json.loads(s1)[:5] == json.loads(s1b)[:5], None)
log0 = feed_len()
clock("10:48")
c, j = call("POST", B + "/resolve", {}, G)
check("a second late resolve re-runs nothing", c == 200 and j.get("resolved_now") == [] and (j.get("stopped_at") or {}).get("reason") == "awaiting_submission", j.get("stopped_at"))
check("... and writes nothing to the log", feed_len() == log0, feed_len() - log0)
ci = sched()
check("10:48: first resolved (how own); second not submitted, can still submit until 10:55; third and fourth waiting on it; nobody needs a stand-in",
      status(ci, p1).get("how") == "own" and status(ci, p2).get("status") == "not_submitted" and "10:55" in status(ci, p2).get("message", "") and "submit" in status(ci, p2).get("message", "")
      and [status(ci, p).get("status") for p in (p3, p4)] == ["waiting", "waiting"] and not any(x.get("status") == "needs_stand_in" for x in ci.get("characters", [])), ci.get("characters"))
check("10:48: the GM isn't overdue (the turn due is waiting for a player's submission)", ci.get("gm_overdue") is None and (ci.get("due") or {}).get("kind") == "player", [ci.get("gm_overdue"), ci.get("due")])

# ---------------------------------------------------------------- 10:55, the final check-in
clock("10:55")
ci = sched()
flagged = [x.get("character_id") for x in ci.get("characters", []) if x.get("status") == "needs_stand_in"]
check("10:55: only the second player (unsubmitted, unresolved) needs a stand-in; the resolved and the submitted are never flagged", flagged == [p2], ci.get("characters"))
c, gs = call("GET", f"/api/gm/status?campaign_id={C}", None, G)
check("GET /api/gm/status asks for the stand-in", gs.get("stand_in_required") is True and gs.get("stand_in_player") == name_of[p2] and any("stand-in" in t for t in gs.get("gm_tasks", [])), [gs.get("stand_in_player"), gs.get("gm_tasks")])
c, rep = call("POST", B + "/resolve", {}, G)
check("resolve at 10:55: the walk stops at the second player for a stand-in", (rep.get("stopped_at") or {}).get("reason") == "stand_in" and (rep.get("stopped_at") or {}).get("character_id") == p2, rep.get("stopped_at"))
c, j = call("POST", B + "/stand-in", {"steps": [{"action": "dodge", "description": "Seeing the boss's reach, I keep my guard up."}]}, G)
done = (j.get("pending_turns") or {}).get("resolved_now") or []
check("the GM stands in for the second player; then the third and fourth resolve; Tank stops the walk",
      c == 200 and j.get("stand_in_for") == name_of[p2] and [d.get("character_id") for d in done] == [p3, p4] and ((j.get("pending_turns") or {}).get("stopped_at") or {}).get("name") == "Tank", [j.get("error"), j.get("message"), done])
ci = (j.get("pending_turns") or {}).get("check_in") or {}
check("status: own, stand_in, own, own", [status(ci, p).get("how") for p in P] == ["own", "stand_in", "own", "own"], [status(ci, p) for p in P])
c, j = call("POST", B + "/stand-in", {"steps": [{"action": "dodge"}]}, G)
check("a second stand-in now meets Tank's turn (the GM's): nobody gets a second action", c == 409 and j.get("error") == "gm_turn", j)
c, j = call("POST", B + "/next", {}, G)
check("Tank's turn ends at 10:55: 'Fight round 1 is complete; fight round 2 has begun (its final check-in is 11:55 UTC)'", c == 200 and "Fight round 1 is complete; fight round 2 has begun (its final check-in is 11:55 UTC)" in (j.get("round_complete") or ""), j.get("round_complete"))

# ---------------------------------------------------------------- section 9: early close, a mid-hour round, other rounds
clock("11:00")
for p in reversed(P):
    c, j = submit(p, {"steps": [{"action": "dodge"}]})
    check(f"round 2: {name_of[p]} submits", c == 200 and j.get("round") == 2, j)
clock("11:10")
c, j = call("POST", B + "/next", {}, G)
done = (j.get("pending_turns") or {}).get("resolved_now") or []
check("11:10: Boss's turn ends; every submitted turn resolves in order", [d.get("character_id") for d in done] == P, [(d.get("name"), d.get("used")) for d in done])
c, j = call("POST", B + "/next", {}, G)
check("Tank's turn ends: round 2 closes early (11:10, before 11:55); round 3 opens mid-hour with its final check-in at 12:55",
      "Fight round 2 is complete; fight round 3 has begun (its final check-in is 12:55 UTC)" in (j.get("round_complete") or ""), j.get("round_complete"))
r = sched().get("round") or {}
check("the status shows round 3, opened 11:10, final check-in 12:55", r.get("number") == 3 and (r.get("opened_at") or "")[11:16] == "11:10" and (r.get("final_check_in") or "")[11:16] == "12:55", r)
before = snapshot()
for rnd in (2, 4):
    c, j = submit(p1, {"steps": [{"action": "dodge"}], "round": rnd})
    check(f"a submission for round {rnd} while round 3 is open: 409 round_not_open", c == 409 and j.get("error") == "round_not_open" and j.get("open_round") == 3, j)
check("... and nothing changed", snapshot() == before, None)
c, j = call("DELETE", "/api/pending-turn", {"character_id": p1, "campaign_id": C, "round": 4}, tok_of[p1])
check("a withdrawal for another round: 409 round_not_open", c == 409 and j.get("error") == "round_not_open", j)

# ---------------------------------------------------------------- pause: no stand-ins, the GM still resolves
c, j = call("PATCH", f"/api/campaigns/{C}/schedule", {"state": "paused"}, G)
check("the GM pauses the schedule", c == 200 and j.get("state") == "paused", j.get("state"))
clock("13:30")
c, j = call("POST", B + "/next", {}, G)
st = (j.get("pending_turns") or {}).get("stopped_at") or {}
check("paused, 13:30 (past 12:55): Boss's turn ends; the first player hasn't submitted: the walk waits, no stand-in", c == 200 and st.get("reason") == "awaiting_submission" and "paused" in st.get("message", ""), st)
c, j = call("POST", B + "/stand-in", {"steps": [{"action": "dodge"}]}, G)
check("a stand-in while paused: 409 stand_in_too_early, no final check-in", c == 409 and j.get("error") == "stand_in_too_early" and j.get("final_check_in") is None, j)
check("nobody needs a stand-in while paused", not any(x.get("status") == "needs_stand_in" for x in sched().get("characters", [])), sched().get("characters"))
c, j = submit(p1, {"steps": [{"action": "dodge"}]})
c, rep = call("POST", B + "/resolve", {}, G)
check("paused: a submission is still saved and the GM still resolves it", [d.get("character_id") for d in rep.get("resolved_now") or []][:1] == [p1], rep.get("resolved_now"))
clock("13:40")
c, j = call("PATCH", f"/api/campaigns/{C}/schedule", {"state": "on"}, G)
check("resumed at 13:40: a fresh final check-in (14:55)", (((j.get("check_in") or {}).get("round") or {}).get("final_check_in") or "")[11:16] == "14:55", (j.get("check_in") or {}).get("round"))

# ---------------------------------------------------------------- the GM finishes a submitted turn that no longer works, at any check-in
c, j = submit(p2, {"steps": [{"action": "attack", "target_id": tank}], "fallbacks": [[{"action": "attack", "target_id": tank}]]})
check("the second player submits an attack on Tank", c == 200, j)
call("POST", "/api/gm/adjust-monster", {"campaign_id": C, "target_id": tank, "hp": 0}, G)
c, rep = call("POST", B + "/resolve", {}, G)
check("Tank falls before it resolves: the turn is paused for the GM (needs_gm)", (rep.get("stopped_at") or {}).get("reason") == "needs_gm" and (rep.get("stopped_at") or {}).get("character_id") == p2, rep.get("stopped_at"))
c, j = call("POST", B + "/stand-in", {"steps": [{"action": "dodge"}]}, G)
check("before the final check-in the GM may finish that submitted turn (it is the player's submission, not a missing one)", c == 200 and j.get("stand_in_for") == name_of[p2], j)

# ---------------------------------------------------------------- a campaign whose schedule is off plays as before
U, utoks, unames = campaign(2, "LgOff")
c, j = call("PATCH", f"/api/campaigns/{U}/schedule", {"state": "off"}, G)
check("another campaign: the GM turns the schedule off", c == 200 and j.get("state") == "off", j.get("state"))
UB = f"/api/campaigns/{U}/combat"
call("POST", f"/api/campaigns/{U}/start", {}, G)
call("POST", UB + "/start", {}, G)
call("POST", UB + "/add", {"combatants": [{"name": "Ogre", "monster_key": "ogre", "initiative": -20, "hp": 500}]}, G)
uorder = call("GET", UB, None, G)[1].get("turn_order") or []
up = [e["id"] for e in uorder if e["id"] in utoks]
c, j = submit(up[1], {"steps": [{"action": "dodge"}]}, U, utoks)
check("schedule off: the second player's turn is held for round 1", c == 200 and j.get("round") == 1, j)
c, gs = call("GET", f"/api/gm/status?campaign_id={U}", None, G)
check("schedule off: GET /api/gm/status runs the walk and asks for a stand-in at once (no deadline)", gs.get("stand_in_required") is True and ((gs.get("pending_turns") or {}).get("stopped_at") or {}).get("reason") == "stand_in" and "check_in" not in gs, gs.get("pending_turns"))
c, j = call("POST", UB + "/stand-in", {"steps": [{"action": "dodge"}]}, G)
done = (j.get("pending_turns") or {}).get("resolved_now") or []
check("schedule off: the stand-in is taken at once and the held turn resolves", c == 200 and [d.get("character_id") for d in done] == [up[1]], [j.get("error"), done])
c, j = submit(up[0], {"steps": [{"action": "dodge"}]}, U, utoks)
check("schedule off: a player whose slot passed sends a turn for the next round (round 2), as before", c == 200 and j.get("round") == 2, j)
c, j = call("POST", UB + "/skip", {}, G)
check("schedule off: combat/skip refuses a monster's turn (not_player_turn), as before", c == 409 and j.get("error") == "not_player_turn", j)

print(f"\n{passed} of {passed + failed} checks passed")
if fails:
    print("FAILED:", fails)
    sys.exit(1)
