# Repeatable Eldritch Invocations (open item 13): Agonizing Blast, Eldritch Spear and Repelling Blast may be taken again, each
# copy naming a different eligible cantrip; Lessons of the First Ones again for a different Origin feat. Every other invocation
# once. One table (game.InvocationRepeats) drives the request, its options, the sheet and the effects.
# Local: python3 invocation_repeat_test.py
import json, random
from ah import call, register, build, BASE
results = []
def check(name, ok, detail=""):
    results.append((name, bool(ok)))
    print(("PASS " if ok else "FAIL ") + name + ("" if ok else "  -> " + json.dumps(detail)[:500]), flush=True)
G = register("IrGM")["token"]; T = register("IrP")["token"]
def at(cls, bg, sp, level):
    cid, f = build(T, cls, bg, sp, "IR"); assert cid, f
    c, camp = call("POST", "/api/campaigns", {"name": "IR %d" % random.randint(1000, 99999), "setting": "t", "max_players": 4}, G)
    cmp = camp["campaign_id"]
    call("POST", f"/api/campaigns/{cmp}/join", {"character_id": cid}, T); call("POST", f"/api/campaigns/{cmp}/start", None, G)
    c, j = call("POST", "/api/gm/starting-level", {"campaign_id": cmp, "character_id": cid, "level": level, "gold": False}, G); assert c == 200, j
    for st in call("GET", f"/api/characters/{cid}/setup", None, T)[1].get("steps") or []:  # the class's own spells, as suggested
        if st.get("key") == "spells" and st.get("request"):
            rq = st["request"]; c, j = call(rq.get("method", "POST"), rq["path"], rq.get("body"), T, "spells")
    return cid
def opts(cid): return call("GET", f"/api/characters/invocations?character_id={cid}", None, T)[1]
def learn(cid, body): return call("POST", "/api/characters/invocations", {"character_id": cid, **body}, T, "invocation_repeat")
def option(o, slug): return next((x for x in o.get("available_to_learn") or [] if x["slug"] == slug), None)

wl = at("warlock", "criminal", "tiefling", 8)
o = opts(wl)
ab = option(o, "agonizing-blast")
check("options: Agonizing Blast is offered as repeatable, with the cantrips open for a copy",
      ab and ab.get("repeat", {}).get("repeatable") and ab["repeat"].get("cantrips_open"), ab)
open_ = (ab or {}).get("repeat", {}).get("cantrips_open") or []
known = {k["slug"] for k in o.get("invocations_known") or []}
check("options: a non-repeatable invocation the character has is not offered again",
      all(option(o, s) is None for s in known if s not in ("agonizing-blast", "eldritch-spear", "repelling-blast", "lessons-of-the-first-ones")), sorted(known))
has_ab = "agonizing-blast" in known

# a copy of Agonizing Blast on each eligible cantrip, then none left
first = open_[0] if open_ else None
c, j = learn(wl, {"invocation": "agonizing-blast", "cantrip": first})
check(f"Agonizing Blast on {first}: learned", c == 200, (c, j))
c, j = learn(wl, {"invocation": "agonizing-blast", "cantrip": first})
check("the same cantrip again -> cantrip_not_eligible, naming the other copy", c == 400 and j.get("error") == "cantrip_not_eligible" and "already on" in j.get("message", ""), (c, j))
c, j = learn(wl, {"invocation": "agonizing-blast", "cantrip": "light"})
check("a cantrip that deals no damage -> cantrip_not_eligible, with the choices", c == 400 and j.get("error") == "cantrip_not_eligible" and "Choose one of" in j.get("message", "") or "no cantrip left" in j.get("message", ""), (c, j))
o = opts(wl); ab = option(o, "agonizing-blast") or {}
left = ab.get("repeat", {}).get("cantrips_open") or []
if left:
    c, j = learn(wl, {"invocation": "agonizing-blast"})
    check(f"a second copy without a cantrip takes the next one left ({left[0]})", c == 200, (c, j))
o = opts(wl); ab = option(o, "agonizing-blast") or {}
check("with every eligible cantrip taken, Agonizing Blast is offered but not eligible, saying why",
      ab and ab.get("eligible") is False and "no copy" in ab.get("ineligible_reason", ""), ab)
c, j = learn(wl, {"invocation": "agonizing-blast"})
check("another copy with none left -> cantrip_not_eligible or at_capacity", c in (400, 409) and j.get("error") in ("cantrip_not_eligible", "at_capacity"), (c, j))

# the sheet and my-turn name each copy's cantrip, from one summary
sh = call("GET", f"/api/characters/{wl}", None, T)[1]
inv = json.dumps(sh)
copies = [e for e in (sh.get("eldritch_invocations") or {}).get("known", []) if e.get("slug") == "agonizing-blast"]
check("the sheet lists each copy of Agonizing Blast with its own cantrip", len(copies) >= 1 and all(e.get("cantrip") for e in copies) and len({e["cantrip"] for e in copies}) == len(copies), copies or inv[:300])

# the effect follows the copy's cantrip
c, cast = call("POST", "/api/action", {"character_id": wl, "action": "cast", "spell_slug": first, "description": "I cast it at the dummy"}, T)
check(f"casting {first}: the result adds Agonizing Blast", "Agonizing Blast" in json.dumps(cast), cast)

# rules that don't change
c, j = learn(wl, {"invocation": "devils-sight"})
c2, j2 = learn(wl, {"invocation": "devils-sight"})
check("a non-repeatable invocation twice -> already_known", c2 == 409 and j2.get("error") == "already_known", (c, j, c2, j2))
c, j = learn(wl, {"invocation": "mask-of-many-faces", "cantrip": "eldritch-blast"})
check("cantrip sent with an invocation that names none -> no_cantrip_choice", c == 400 and j.get("error") == "no_cantrip_choice", (c, j))
c, j = learn(wl, {"invocation": "repelling-blast", "cantrip": "minor-illusion"})
check("Repelling Blast on a cantrip without an attack roll -> refused", c == 400 and j.get("error") in ("cantrip_not_eligible", "at_capacity"), (c, j))

# Lessons of the First Ones twice: two Origin feats to choose
wl2 = at("warlock", "criminal", "tiefling", 8)
c1, j1 = learn(wl2, {"invocation": "lessons-of-the-first-ones"})
c2, j2 = learn(wl2, {"invocation": "lessons-of-the-first-ones"})
check("Lessons of the First Ones taken twice", c1 == 200 and c2 == 200, (c1, j1, c2, j2))
tf = call("GET", f"/api/characters/{wl2}", None, T)[1].get("to_finish") or []
check("to_finish asks for 2 Origin feats from Lessons of the First Ones", any("Lessons of the First Ones" in t and "2" in t for t in tf), tf)
c, j = call("POST", f"/api/characters/{wl2}/feat", {"feat": "Savage Attacker"}, T)
c2, j2 = call("POST", f"/api/characters/{wl2}/feat", {"feat": "Savage Attacker"}, T)
check("the second copy can't give the same Origin feat again", c == 200 and c2 in (400, 409), (c, j, c2, j2))

print(f"\n{sum(ok for _, ok in results)} of {len(results)} checks passed")
import sys
sys.exit(0 if all(ok for _, ok in results) else 1)  # a failed check fails the run (audit AUD-48)
