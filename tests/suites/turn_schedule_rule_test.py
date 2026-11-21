# The campaign turn schedule, stages B and C (campaign-scheduling-directions.md; turn-schedule-design.md): seats and their
# check-in times from the seat capacity (1 to 6), the GM's check-ins, the final check-in, the creation and join replies, the
# one way into a seat (parallel joins for the last seat: exactly one wins; a seat freed by a kick goes to the next player),
# GET|PATCH /api/campaigns/{id}/schedule (capacity during a round waits for the next round; pause and resume with a fresh
# deadline; off), the round record (a fight's round, a noncombat round after it) and the read-only check_in status
# (not submitted with the seat's check-in, waiting on an earlier player, GM overdue, automatic death save, needs a stand-in only
# at the final check-in). Requests only. It needs the server's test clock (AGENTRPG_TEST_CLOCK=1) to play an hour in minutes.
# Stage D (Nov 16): round 1 is completed by every player submitting and the GM's resolve.
# Local:   BASE=http://localhost:8116 python3 turn_schedule_rule_test.py      Staging: ARPG_TARGET=staging python3 turn_schedule_rule_test.py
import json, random, sys, threading
from ah import call, register, build, only
import ah; ah.KEEP_SCHEDULE = True  # this suite sets the turn schedule itself

only("local", "staging", why="it sends refusals, parallel joins and test-clock moves")
passed = failed = 0
fails = []
def check(name, ok, detail=None):
    global passed, failed
    if ok:
        passed += 1; print("PASS", name)
    else:
        failed += 1; fails.append(name); print("FAIL", name, "|", json.dumps(detail)[:600])

def parallel(fns):
    out = [None] * len(fns)
    def run(i): out[i] = fns[i]()
    ts = [threading.Thread(target=run, args=(i,)) for i in range(len(fns))]
    [t.start() for t in ts]; [t.join() for t in ts]
    return out

DAY = "2026-11-16T"
G = register("TsGm")["token"]

# ---------------------------------------------------------------- creation: capacity 1 to 6, the schedule block
c, j = call("POST", "/api/campaigns", {"name": f"TsBad {random.randint(1000,9999)}", "setting": "t", "max_players": 7}, G)
check("a campaign of 7 seats is refused (invalid_capacity)", c == 400 and j.get("error") == "invalid_capacity", j)
c, j = call("POST", "/api/campaigns", {"name": f"TsC {random.randint(1000,9999)}", "setting": "t", "max_players": 4}, G)
C = j["campaign_id"]
s = j.get("schedule") or {}
check("the creation reply's schedule: state on, 4 seats at :00 :15 :30 :45",
      s.get("state") == "on" and [x.get("check_in") for x in s.get("seats", [])] == [":00", ":15", ":30", ":45"], s)
check("... the GM's check-ins :10 :25 :40 :55, the final :55, 4 an hour",
      s.get("gm_check_ins") == [":10", ":25", ":40", ":55"] and s.get("final_check_in") == ":55" and s.get("gm_check_ins_per_hour") == 4, s)
note = s.get("gm_note") or ""
check("... and the GM's note: fewer seats if the rate can't be kept, recurring checks with the human's permission",
      "fewer seats" in note and "human's permission" in note and f"/api/campaigns/{C}/schedule" in note, note)
c, j = call("POST", "/api/campaigns", {"name": f"TsDefault {random.randint(1000,9999)}", "setting": "t"}, G)
check("no max_players: 4 seats", c == 200 and (j.get("schedule") or {}).get("capacity") == 4, j.get("schedule"))
import urllib.request
from target import BASE, CTX
with urllib.request.urlopen(BASE + "/gm-guide.md", context=CTX) as x:
    raw = x.read().decode()
check("the GM guide shows the schedule table (every capacity)", "| 4 | :00 :15 :30 :45 | :10 :25 :40 :55 | 4 | :55 |" in raw and "| 6 |" in raw and "schedule table:" not in raw, raw[:200])

# ---------------------------------------------------------------- joining: seats in join order
players = []
for i, (cls, bg, sp) in enumerate([("fighter", "soldier", "human"), ("cleric", "acolyte", "dwarf"), ("rogue", "criminal", "halfling"), ("wizard", "sage", "elf")]):
    tok = register(f"TsP{i}")["token"]
    cid, _ = build(tok, cls, bg, sp, f"Ts{i}")
    players.append((cid, tok))
names = {}
for i, (cid, tok) in enumerate(players):
    c, j = call("POST", f"/api/campaigns/{C}/join", {"character_id": cid}, tok)
    seat = j.get("seat") or {}
    check(f"join {i+1}: seat {i+1} at {[':00', ':15', ':30', ':45'][i]}", c == 200 and seat.get("seat") == i + 1 and seat.get("check_in") == [":00", ":15", ":30", ":45"][i] and "human's permission" in (seat.get("note") or ""), j)
c, j = call("GET", f"/api/campaigns/{C}", None, G)
seats = (j.get("schedule") or {}).get("seats") or []
for x in seats:
    names[x.get("character_id")] = x.get("name")
check("GET /api/campaigns/{id} shows the schedule with who holds each seat", [x.get("character_id") for x in seats] == [p[0] for p in players], seats)
outsider = register("TsOut")["token"]
c, j = call("GET", f"/api/campaigns/{C}/schedule", None, outsider)
check("the schedule is for the GM and the players: someone else gets 403 not_in_campaign", c == 403 and j.get("error") == "not_in_campaign", j)
c, j = call("PATCH", f"/api/campaigns/{C}/schedule", {"capacity": 5}, players[0][1])
check("a player can't change it: 403 not_gm", c == 403 and j.get("error") == "not_gm", j)
for body, code, err in [({"capacity": 2}, 409, "capacity_below_players"), ({"capacity": 9}, 400, "invalid_capacity"), ({"capacity": 0}, 400, "invalid_capacity"),
                        ({}, 400, "schedule_change_required"), ({"state": "sometimes"}, 400, "invalid_state"), ({"seats": 4}, 400, "unknown_field")]:
    c, j = call("PATCH", f"/api/campaigns/{C}/schedule", body, G)
    check(f"PATCH {json.dumps(body)}: {code} {err}", c == code and j.get("error") == err, j)
c, j = call("GET", f"/api/campaigns/{C}/schedule", None, players[0][1])
check("before any round: check_in has no round", c == 200 and (j.get("check_in") or {}).get("round") is None and j.get("state") == "on", j.get("check_in"))
c, j = call("POST", f"/api/campaigns/{C}/test-clock", {"advance_minutes": 5}, players[0][1])
check("a player can't move the test clock: 403", c == 403, j)

# ---------------------------------------------------------------- a fight opens round 1 at 09:55: final check-in 10:55
c, j = call("POST", f"/api/campaigns/{C}/test-clock", {"at": DAY + "09:55:00Z"}, G)
if c == 404:
    sys.exit("This server has no test clock (start it with AGENTRPG_TEST_CLOCK=1): the timed checks can't run.")
check("the GM sets the campaign clock to 09:55", c == 200 and j.get("now", "").startswith(DAY + "09:5"), j)
call("POST", f"/api/campaigns/{C}/start", {}, G)
c, j = call("POST", f"/api/campaigns/{C}/combat/start", {}, G)
check("combat starts", c == 200, j)
def sched(tok=G):
    return call("GET", f"/api/campaigns/{C}/schedule", None, tok)[1]
def clock(hhmmss):
    c, j = call("POST", f"/api/campaigns/{C}/test-clock", {"at": DAY + hhmmss + "Z"}, G)
    assert c == 200, j
def status(ci, cid):
    return next((x for x in ci.get("characters", []) if x.get("character_id") == cid), {})
order = [e for e in (call("GET", f"/api/campaigns/{C}/combat", None, G)[1].get("turn_order") or [])]
order_ids = [e.get("id") for e in order]
first, second = order_ids[0], order_ids[1]
tok_of = {cid: tok for cid, tok in players}
j = sched()
ci = j.get("check_in") or {}
r = ci.get("round") or {}
check("round 1 (combat) is open, final check-in 10:55", r.get("number") == 1 and r.get("kind") == "combat" and (r.get("final_check_in") or "").startswith(DAY + "10:55"), r)
check("the round's GM check-ins: 10:10, 10:25, 10:40, 10:55", [t[11:16] for t in ci.get("gm_check_ins", [])] == ["10:10", "10:25", "10:40", "10:55"], ci.get("gm_check_ins"))
seat_time = {p[0]: ["10:00", "10:15", "10:30", "10:45"][i] for i, p in enumerate(players)}
ok = all(status(ci, cid).get("status") == "not_submitted" and (status(ci, cid).get("check_in_at") or "")[11:16] == seat_time[cid] for cid, _ in players)
check("every character: not submitted, with its seat's check-in this round", ok, ci.get("characters"))
check("whose action is due: the first in Initiative (a player)", (ci.get("due") or {}).get("kind") == "player" and (ci.get("due") or {}).get("characters", [{}])[0].get("character_id") == first, ci.get("due"))
check("nobody needs a stand-in and the GM isn't overdue", not any(x.get("status") == "needs_stand_in" for x in ci.get("characters", [])) and ci.get("gm_overdue") is None, ci)

# the second in Initiative submits: waiting on the first, never blocked or late
c, j = call("POST", "/api/pending-turn", {"steps": [{"action": "dodge"}]}, tok_of[second])
check("the second in Initiative submits a turn", c == 200, j)
clock("10:20:00")
before = call("GET", f"/api/campaigns/{C}/combat", None, G)[1]
ci = sched(tok_of[second]).get("check_in") or {}
st = status(ci, second)
check("it is waiting on the first, named, without blame", st.get("status") == "waiting" and (st.get("waiting_on") or {}).get("character_id") == first and "blocked" not in st.get("message", "").lower(), st)
check("a turn waiting on an earlier player is never GM-overdue (10:20)", ci.get("gm_overdue") is None, ci.get("gm_overdue"))
ci2 = sched().get("check_in") or {}
after = call("GET", f"/api/campaigns/{C}/combat", None, G)[1]
check("reading the status changes nothing (turn, round and queue the same)",
      (before.get("round"), before.get("current_turn_index"), json.dumps(before.get("turn_order"))) == (after.get("round"), after.get("current_turn_index"), json.dumps(after.get("turn_order"))) and
      [x.get("status") for x in ci.get("characters", [])] == [x.get("status") for x in ci2.get("characters", [])], [before.get("current_turn_index"), after.get("current_turn_index")])
# the first submits at 10:20: resolvable at the 10:25 check-in, GM-overdue from 10:30
call("POST", "/api/pending-turn", {"steps": [{"action": "dodge"}]}, tok_of[first])
ci = sched().get("check_in") or {}
check("the first submitted: it resolves at the GM's next check-in (10:25); the second no longer waits",
      status(ci, first).get("status") == "submitted" and (status(ci, first).get("resolves_at") or "")[11:16] == "10:25" and status(ci, second).get("status") == "submitted", [status(ci, first), status(ci, second)])
clock("10:29:00")
check("not GM-overdue at 10:29", (sched().get("check_in") or {}).get("gm_overdue") is None, None)
clock("10:30:00")
ov = (sched().get("check_in") or {}).get("gm_overdue") or {}
check("GM-overdue from 10:30: the turn could have resolved at 10:25 (the delay is the GM's)", (ov.get("check_in") or "")[11:16] == "10:25" and (ov.get("since") or "")[11:16] == "10:30" and ov.get("turn") == names.get(first), ov)
# a player who hasn't submitted: window, then passed, never missing before the final check-in
clock("10:50:00")
ci = sched().get("check_in") or {}
idle = [(i, cid) for i, (cid, _) in enumerate(players) if cid not in (first, second)]
for i, cid in idle:
    st = status(ci, cid)
    until = ["10:10", "10:25", "10:40", "10:55"][i]
    if until > "10:50":
        check(f"seat {i+1} at 10:50: not submitted, inside its window (until {until})", st.get("status") == "not_submitted" and (st.get("window_until") or "")[11:16] == until and "window is open" in st.get("message", ""), st)
    else:
        check(f"seat {i+1} at 10:50: its check-in has passed, it can still submit until 10:55 (not missing)", st.get("status") == "not_submitted" and "can still submit until the round's final check-in at 10:55" in st.get("message", ""), st)

# ---------------------------------------------------------------- pause and resume
c, j = call("PATCH", f"/api/campaigns/{C}/schedule", {"state": "paused"}, G)
check("the GM pauses: state paused, the round has no deadline", c == 200 and j.get("state") == "paused" and ((j.get("check_in") or {}).get("round") or {}).get("final_check_in") is None, j.get("check_in"))
clock("12:10:00")
ci = sched().get("check_in") or {}
check("paused at 12:10 (past the old 10:55): nobody needs a stand-in, the GM isn't overdue",
      not any(x.get("status") == "needs_stand_in" for x in ci.get("characters", [])) and ci.get("gm_overdue") is None, ci)
clock("12:30:00")
c, j = call("PATCH", f"/api/campaigns/{C}/schedule", {"state": "on"}, G)
r = ((j.get("check_in") or {}).get("round") or {})
check("resumed at 12:30: a fresh final check-in (13:55)", c == 200 and (r.get("final_check_in") or "")[11:16] == "13:55" and r.get("number") == 1, r)
st = status(j.get("check_in") or {}, players[0][0])
if st.get("status") == "not_submitted":
    check("... and a fresh check-in for seat 1 (13:00)", (st.get("check_in_at") or "")[11:16] == "13:00", st)

# ---------------------------------------------------------------- a capacity change waits for the next round
c, j = call("PATCH", f"/api/campaigns/{C}/schedule", {"capacity": 6}, G)
p = j.get("pending_capacity") or {}
check("capacity 6 during round 1: shown at once as pending, the current round keeps 4 seats",
      c == 200 and j.get("capacity") == 4 and p.get("capacity") == 6 and [x.get("check_in") for x in p.get("seats", [])] == [":00", ":10", ":20", ":30", ":40", ":50"] and "round 2" in " ".join(j.get("changed", [])), j)
# the fight's round goes up: round 2 opens with 6 seats, in join order. (Stage D: the GM's combat/next no longer ends a player's
# turn that was submitted or is still within the round's deadline, so every player submits and the GM resolves the round.)
for cid, tok in players:
    if cid not in (first, second):
        call("POST", "/api/pending-turn", {"steps": [{"action": "dodge"}]}, tok)
c, j = call("POST", f"/api/campaigns/{C}/combat/resolve", {}, G)
check("every player submitted: the GM's resolve ends round 1 ('Fight round 1 is complete; fight round 2 has begun')", "Fight round 1 is complete; fight round 2 has begun" in " ".join(j.get("round_complete") or []), j.get("round_complete"))
j = sched()
r = (j.get("check_in") or {}).get("round") or {}
check("round 2 opens with the fight's round, and the new capacity takes effect", r.get("number") == 2 and r.get("kind") == "combat" and j.get("capacity") == 6 and not j.get("pending_capacity"), [r, j.get("capacity")])
check("the seats keep their join order with the new times", [x.get("character_id") for x in j.get("seats", []) if x.get("character_id")] == [p[0] for p in players] and j["seats"][1].get("check_in") == ":10", j.get("seats"))
check("round 2 opened at 12:30 (mid-hour): its final check-in is 13:55", (r.get("final_check_in") or "")[11:16] == "13:55", r)

# ---------------------------------------------------------------- a dying character's turn is automatic; stand-ins only at the final check-in
victim = players[2][0]
sheet = call("GET", f"/api/characters/{victim}", None, G)[1]
c, j = call("POST", f"/api/characters/{victim}/damage", {"damage": sheet.get("hp", 1)}, G)
ci = sched().get("check_in") or {}
st = status(ci, victim)
check("at 0 Hit Points and dying: automatic (death save), not a missing submission", st.get("status") == "automatic" and st.get("automatic") == "death_save", st)
clock("13:54:00")
ci = sched().get("check_in") or {}
check("13:54: nobody needs a stand-in yet", not any(x.get("status") == "needs_stand_in" for x in ci.get("characters", [])), ci.get("characters"))
clock("13:55:00")
ci = sched().get("check_in") or {}
flagged = [x.get("character_id") for x in ci.get("characters", []) if x.get("status") == "needs_stand_in"]
want = [x.get("character_id") for x in ci.get("characters", []) if x.get("status") not in ("resolved", "submitted", "waiting", "automatic")]
check("13:55, the final check-in: only those still without a submission need a stand-in (never the dying one)",
      (ci.get("round") or {}).get("final_check_in_reached") is True and flagged and flagged == want and victim not in flagged, ci.get("characters"))

# ---------------------------------------------------------------- the fight ends: a noncombat round; off closes it
c, j = call("POST", f"/api/campaigns/{C}/combat/end", {"award_xp": False}, G)
r = ((sched().get("check_in") or {}).get("round") or {})
# the campaign's start opened campaign round 1 (scheduleCampaignStarts), so the round after the fight is campaign round 2
check("the fight ends: a noncombat round opens (campaign round 2: the start opened round 1)", c == 200 and r.get("kind") == "noncombat" and r.get("number") == 2 and r.get("name") == "campaign round 2", r)
c, j = call("PATCH", f"/api/campaigns/{C}/schedule", {"state": "off"}, G)
check("off: no round, and GET /api/campaigns/{id} says off", c == 200 and (j.get("check_in") or {}).get("round") is None and (call("GET", f"/api/campaigns/{C}", None, G)[1].get("schedule") or {}).get("state") == "off", j.get("check_in"))
c, j = call("PATCH", f"/api/campaigns/{C}/schedule", {"state": "on"}, G)
r = ((j.get("check_in") or {}).get("round") or {})
check("on again outside a fight: campaign round 3", c == 200 and r.get("kind") == "noncombat" and r.get("number") == 3, r)

# ---------------------------------------------------------------- the last seat: parallel joins, exactly one wins
c, j = call("POST", "/api/campaigns", {"name": f"TsRace {random.randint(1000,9999)}", "setting": "t", "max_players": 2}, G)
R = j["campaign_id"]
racers = []
for i in range(5):
    tok = register(f"TsR{i}")["token"]
    cid, _ = build(tok, "fighter", "soldier", "human", f"TsR{i}")
    racers.append((cid, tok))
c, j = call("POST", f"/api/campaigns/{R}/join", {"character_id": racers[0][0]}, racers[0][1])
res = parallel([lambda cid=cid, tok=tok: call("POST", f"/api/campaigns/{R}/join", {"character_id": cid}, tok) for cid, tok in racers[1:]])
won = [i for i, r in enumerate(res) if r[0] == 200]
check(f"4 joins at once for the last seat: exactly one wins, the rest campaign_full ({[r[0] for r in res]})",
      len(won) == 1 and all(r[1].get("error") == "campaign_full" for r in res if r[0] != 200), [(r[0], r[1].get("error")) for r in res])
held = [x for x in (call("GET", f"/api/campaigns/{R}/schedule", None, G)[1].get("seats") or []) if x.get("character_id")]
check("two seats held, seats 1 and 2", [x.get("seat") for x in held] == [1, 2], held)
winner = racers[1 + won[0]][0] if won else None
c, j = call("POST", "/api/gm/kick-character", {"character_id": winner, "campaign_id": R}, G)
loser = next(r for i, r in enumerate(racers[1:]) if i not in won)
c, j = call("POST", f"/api/campaigns/{R}/join", {"character_id": loser[0]}, loser[1])
check("a seat freed by a kick goes to the next player, with that seat's time", c == 200 and (j.get("seat") or {}).get("seat") == 2 and (j.get("seat") or {}).get("check_in") == ":30", j.get("seat"))

print(f"\n{passed} of {passed + failed} checks passed")
if fails:
    print("FAILED:", fails)
    sys.exit(1)
