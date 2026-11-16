# R3: one way to read a request. Every route that takes a body reads it with the same reader (readRequest / gmRead), so on
# every route: a field the request doesn't take is refused (unknown_field), malformed JSON, data after the JSON object and a
# list instead of an object are refused (invalid_json), an id that repeats the path's id is taken when it is the same and
# refused when it differs (id_mismatch), and every GM command takes campaign_id. The routes come from the server's own API
# reference (/docs/swagger.json), so a new route is tested without changing this file.
# Local: python3 request_rule_test.py   (it changes things: run it against the local server only)
import json, random, sys
from ah import call, register, build, BASE, only

only("local", "staging", why="it sends broken bodies to every route")
passed = failed = 0
fails, no_body, inconclusive = [], [], []
def check(name, ok, detail=None):
    global passed, failed
    if ok: passed += 1
    else:
        failed += 1; fails.append(name); print("FAIL", name, "|", json.dumps(detail)[:400])

G = register("ReqGM")["token"]
P = register("ReqP")["token"]
cid, _ = build(P, "fighter", "soldier", "human", "Req")
c, j = call("POST", "/api/campaigns", {"name": f"Req {random.randint(1000,9999)}", "setting": "t", "max_players": 4}, G)
camp = j["campaign_id"]
call("POST", f"/api/campaigns/{camp}/join", {"character_id": cid}, P)
call("POST", f"/api/campaigns/{camp}/start", {}, G)
c, d = call("POST", "/api/character-builder", {"name": f"Req Draft {random.randint(1000,9999)}"}, P)
draft = d.get("draft_id", 1)

c, spec = call("GET", "/docs/swagger.json")
ops = sorted((m.upper(), p) for p, v in spec["paths"].items() for m in v if m in ("post", "put", "patch", "delete"))
# routes that end or remove things go last, so the campaign and the character are there for the others
LAST = ("/end", "leave", "delete", "kick", "archive", "remove", "/finish", "recreate", "/combat/start")
ops.sort(key=lambda o: (any(x in o[1] for x in LAST), o[0] == "DELETE", o[1]))

def path_of(p):
    if p.startswith("/api/campaigns/{id}"): p = p.replace("{id}", str(camp), 1)
    elif p.startswith("/api/characters/{id}"): p = p.replace("{id}", str(cid), 1)
    elif p.startswith("/api/character-builder/{id}"): p = p.replace("{id}", str(draft), 1)
    import re
    return re.sub(r"\{[a-z_]+\}", "1", p)

def token_for(p):
    if p.startswith(("/api/gm/", "/api/campaigns/")) and "/join" not in p and "/observe" not in p: return G
    return P

def send(m, p, raw):
    tok = token_for(p)
    c, j = call(m, p, None, tok, "req_rule", raw=raw)
    if c in (401, 403, 404) and j.get("error") not in ("unknown_field", "invalid_json", "id_mismatch", "body_required", "wrong_type"):
        c2, j2 = call(m, p, None, P if tok == G else G, "req_rule", raw=raw)  # the other account may be the one allowed
        if c2 not in (401, 403, 404): return c2, j2
    return c, j

SKIP = ("/api/admin/", "/api/mod/")  # need the admin key or a moderator: refused before any body is read
for m, route in ops:
    if route.startswith(SKIP): continue
    p = path_of(route)
    label = f"{m} {route}"
    c, j = send(m, p, json.dumps({"zz_not_a_field": 1}).encode())
    err = j.get("error")
    if c in (401, 403, 404, 405) and err not in ("unknown_field",):
        inconclusive.append(f"{label}: {c} {err}"); continue
    if err != "unknown_field":
        no_body.append(f"{label}: {c} {err} {str(j.get('message',''))[:80]}"); continue
    check(f"{label}: unknown field refused, naming it, with the fields it takes", c == 400 and "zz_not_a_field" in (j.get("unknown") or []) and isinstance(j.get("fields"), list) and ("Nothing was" in j.get("message", "") or "No draft was started" in j.get("message", "")), (c, j))
    for what, raw in (("malformed JSON", b'{"a": '), ("data after the object", b'{} {"x": 1}'), ("a list", b'[1, 2]')):
        c, j = send(m, p, raw)
        check(f"{label}: {what} refused", c == 400 and j.get("error") == "invalid_json", (c, j))
    if route.startswith("/api/gm/"):
        c, j = send(m, p, json.dumps({"campaign_id": camp}).encode())
        check(f"{label}: campaign_id taken", j.get("error") not in ("unknown_field", "invalid_json"), (c, j))
        c, j = send(m, p, json.dumps({"campaign_id": camp + 100000}).encode())
        # every GM command picks its campaign with the shared rule (gm_campaign_2024.go)
        check(f"{label}: campaign_id of a campaign the GM doesn't run refused (not_gm)", c == 403 and j.get("error") == "not_gm", (c, j))
    for prefix, key, own in (("/api/characters/{id}", "character_id", cid), ("/api/campaigns/{id}", "campaign_id", camp)):
        if route.startswith(prefix + "/"):
            c, j = send(m, p, json.dumps({key: own}).encode())
            check(f"{label}: {key} repeating the path's taken", j.get("error") not in ("unknown_field", "id_mismatch"), (c, j))
            c, j = send(m, p, json.dumps({key: own + 100000}).encode())
            check(f"{label}: {key} other than the path's refused", j.get("error") in ("id_mismatch",) or (c == 400 and j.get("error") != "unknown_field" and key in str(j)), (c, j))

# field names are read as game.Key reads them, and a misspelling is answered with the closest field
c, j = call("POST", "/api/action", {"Action": "dodge", "Character-ID": cid}, P)
check("field names in any spelling (Action, Character-ID)", j.get("error") not in ("unknown_field", "invalid_json"), (c, j))
c, j = call("POST", "/api/action", {"action": "dodge", "targt_id": -1}, P)
check("a misspelled field is answered with the closest one", c == 400 and 'did you mean "target_id"' in j.get("message", ""), (c, j))
c, j = call("POST", "/api/action", {"action": "dodge", "target_id": "goblin"}, P)
check("a value of the wrong type is refused, naming the field", c == 400 and j.get("error") == "wrong_type" and "target_id" in j.get("message", ""), (c, j))

print()
print("Routes that answered something else first (game state checked before the body; check by hand that each reads its body):", len(no_body))
for x in no_body: print("  ", x)
print("Routes refused before the body (auth or not found):", len(inconclusive))
for x in inconclusive: print("  ", x)
print(f"\n{passed} of {passed + failed} checks passed")
sys.exit(1 if failed else 0)
