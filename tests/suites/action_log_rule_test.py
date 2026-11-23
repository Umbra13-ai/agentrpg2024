# The action log has one reader (readActions, action_log_2024.go, Nov 15): GM narration, which has no character, and a row with no
# result are read whole in every view: the feed (and its ?since=), spectate, GM status, my-turn, the nudge's source, a character's
# profile, the web feed and the web log. Before, the feed showed narration as a blank record dated year 0001.
# Local: python3 action_log_rule_test.py   (safe on live too: it makes one campaign and two accounts, no database writes)
import json, random, time, urllib.request, ssl, os
from ah import call, register, build, BASE, _ctx
results = []
def check(name, ok, detail=""):
    results.append((name, bool(ok)))
    print(("PASS " if ok else "FAIL ") + name + ("" if ok else "  -> " + json.dumps(detail)[:600]), flush=True)
def page(path):
    ctx = _ctx
    return urllib.request.urlopen(BASE + path, context=ctx).read().decode()
gm = register("AlGM"); G = gm["token"]
P = register("AlP")["token"]
cid, f = build(P, "fighter", "soldier", "human", "AL"); assert cid, f
call("POST", "/api/characters/auto-equip", {"character_id": cid, "answer": "yes"}, P)
name = call("GET", f"/api/characters/{cid}", None, P)[1]["name"]
c, camp = call("POST", "/api/campaigns", {"name": "AL %d" % random.randint(1000, 99999), "setting": "t", "max_players": 4}, G); cmp = camp["campaign_id"]
call("POST", f"/api/campaigns/{cmp}/join", {"character_id": cid}, P)
call("POST", f"/api/campaigns/{cmp}/start", None, G)
c, j = call("POST", "/api/gm/narrate", {"campaign_id": cmp, "narration": "The lamps of the hall gutter as you enter."}, G)
check("the GM narrates", c == 200, j)
c, feed = call("GET", f"/api/campaigns/{cmp}/feed", None, P)
acts = feed.get("actions", [])
narr = [a for a in acts if a.get("type") == "narration"]
check("the feed returns the narration whole", narr and "lamps of the hall" in narr[-1].get("description", ""), acts[-3:])
check("no feed record is blank or dated year 0001", acts and all(a.get("type") and not a.get("created_at", "").startswith("0001") for a in acts), [a for a in acts if not a.get("type")][:3])
check("the narration's actor is the GM, not blank", narr and narr[-1].get("actor") not in ("", None) and narr[-1].get("character_id") == 0, narr[-1:])
# a player's action after the narration, then ?since= the narration's time
time.sleep(1.1)
c, j = call("POST", "/api/action", {"action": "search", "description": "I look for tracks in the dust"}, P)
check("the player searches", c == 200, j)
since = narr[-1]["created_at"] if narr else ""
c, later = call("GET", f"/api/campaigns/{cmp}/feed?since={since}", None, P)
la = later.get("actions", [])
check("?since= returns the later search and not the narration", any(a.get("type") == "search" for a in la) and not any("lamps of the hall" in a.get("description", "") for a in la), la)
from datetime import datetime
ts = lambda x: datetime.fromisoformat(x.replace("Z", "+00:00"))
check("every record after ?since= is later than since", since and all(ts(a["created_at"]) > ts(since) for a in la), la)
c, again = call("GET", f"/api/campaigns/{cmp}/feed?since={la[-1]['created_at'] if la else since}", None, P)
check("?since= the last record's created_at returns nothing new (no record comes back twice)", c == 200 and again.get("actions") == [], again.get("actions"))
c, j = call("GET", f"/api/campaigns/{cmp}/feed?since=yesterday", None, P)
check("a ?since= that isn't a time is refused (400 invalid_since)", c == 400 and j.get("error") == "invalid_since", j)
# the other views
c, sp = call("GET", f"/api/campaigns/{cmp}/spectate", None, None)
check("spectate shows the narration", "lamps of the hall" in json.dumps(sp.get("recent_actions", sp)), sp.get("recent_actions"))
c, st = call("GET", f"/api/gm/status?campaign_id={cmp}", None, G)
check("GM status names the last action (the player's search)", "search" in json.dumps(st.get("last_action")), st.get("last_action"))
c, mt = call("GET", "/api/my-turn", None, P)
check("my-turn's recent events show the narration", "lamps of the hall" in json.dumps(mt), {k: mt.get(k) for k in ("recent_events", "latest_narration") if k in mt})
c, pr = call("GET", f"/api/characters/{cid}/profile", None, P)
check("the character's profile lists its search", any(a.get("type") == "search" for a in pr.get("recent_actions", [])), pr.get("recent_actions"))
html = page(f"/campaign/{cmp}")
check("the Live Table is the story reader", 'id="story-reader"' in html)
check("the web story shows the narration", "lamps of the hall" in page(f"/campaign/{cmp}/story.json"))
log = page(f"/campaign/{cmp}/log")
check("the web log shows the narration with the narration style", "lamps of the hall" in log and 'class="narrate"' in log or "lamps of the hall" in log and "📖" in log)
ok = sum(1 for _, p in results if p)
print(f"{ok} of {len(results)} checks passed")
import sys
sys.exit(0 if ok == len(results) else 1)  # a failed check fails the run (audit AUD-48)
