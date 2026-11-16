# The gold option and the starting shop: a Fighter takes gold for both kits, shops, equips; the shop closes when the campaign starts.
import json, random, base64
from ah import call as _call, register, png, D
OUT = open(D + "levelrun_shop.jsonl", "w")
def call(who, m, p, b=None, tok=None, note=""):
    c, j = _call(m, p, b, tok, "shop")
    OUT.write(json.dumps({"who": who, "method": m, "path": p, "body": b, "status": c, "reply": j, "note": note}) + "\n"); OUT.flush()
    return c, j
def mark(t): OUT.write(json.dumps({"section": t}) + "\n")
G = register("DM")["token"]; T = register("Shopper")["token"]
mark("Character creation: taking gold instead of equipment")
c, d = call("player", "POST", "/api/character-builder", {"name": f"Shopper {random.randint(100000,999999)}"}, T)
P = "/api/character-builder/%d" % d["draft_id"]
call("player", "POST", P, {"class": "fighter"}, T)
c, d = call("player", "GET", P, None, T)
sug = dict(d["current_step"]["suggestion"])
eqc = [c for c in d["current_step"]["choices"] if c["field"] == "starting_equipment"][0]
sug["starting_equipment"] = [o for o in eqc["options"] if "no equipment" in o or "no other equipment" in o][0].split(":")[0]  # the gold-only option
call("player", "POST", P, sug, T)
call("player", "POST", P, {"background": "soldier"}, T)
c, d = call("player", "GET", P, None, T)
sug = dict(d["current_step"]["suggestion"]); sug["background_equipment"] = "b"
call("player", "POST", P, sug, T)
call("player", "POST", P, {"species": "human"}, T)
for i in range(10):
    c, d = call("player", "GET", P, None, T)
    if "Every step is done" in (d.get("next") or ""): break
    cs = d["current_step"]; body = dict(cs.get("suggestion") or {})
    if cs["key"] == "details": body["alignment"] = "Lawful Good"
    if cs["key"] == "backstory": body = {"backstory": "A soldier who sold her kit to pay a debt.\n\nShe starts over with coin.\n\nNow she buys her own gear."}
    call("player", "POST", P, body, T)
c, f = call("player", "POST", P + "/finish", {}, T)
cid = f["character_id"]
mark("Setup: the shop step")
c, s = call("player", "GET", f"/api/characters/{cid}/setup", None, T)
mark("Looking at the shop")
call("player", "GET", f"/api/characters/{cid}/shop?category=armor", None, T)
call("player", "GET", f"/api/characters/{cid}/shop?category=weapons", None, T)
mark("Buying")
call("player", "POST", f"/api/characters/{cid}/shop", {"buy": [{"item": "Plate Armor", "quantity": 1}]}, T, note="More than the character can afford")
call("player", "POST", f"/api/characters/{cid}/shop", {"buy": [{"item": "Bag of Holding", "quantity": 1}]}, T, note="A magic item")
call("player", "POST", f"/api/characters/{cid}/shop", {"buy": [{"item": "Lightsaber", "quantity": 1}]}, T, note="Something not in the SRD tables")
call("player", "POST", f"/api/characters/{cid}/shop", {"buy": [{"item": "Rope", "quantity": 500}]}, T, note="Too many")
call("player", "POST", f"/api/characters/{cid}/shop", {"buy": [{"item": "Chain Mail", "quantity": 1}, {"item": "Greatsword", "quantity": 1}, {"item": "Javelin", "quantity": 4}, {"item": "Explorer's Pack", "quantity": 1}]}, T)
call("player", "POST", f"/api/characters/{cid}/shop", {"buy": [{"item": "Arrows", "quantity": 1}, {"item": "Shortbow", "quantity": 1}]}, T)
mark("Selling back to the starting shop (half price)")
c, sh = call("player", "GET", f"/api/characters/{cid}/shop?category=weapons", None, T)
bow = [o["item_id"] for o in sh.get("offers_for_your_items", []) if o["item"] == "Shortbow"]
call("player", "POST", f"/api/characters/{cid}/shop", {"sell": [{"item_id": bow[0] if bow else "none"}]}, T)
mark("Equipping what was bought")
call("player", "POST", "/api/characters/auto-equip", {"character_id": cid, "answer": "yes"}, T)
c, sh = call("player", "GET", f"/api/characters/{cid}", None, T)
mark("Finishing the rest of setup")
for rnd in range(6):
    c, s = call("player", "GET", f"/api/characters/{cid}/setup", None, T)
    op = [x for x in s.get("steps", []) if not x.get("done")]
    if not op: break
    for st in op:
        if st["key"] == "portrait": call("player", "POST", f"/api/characters/{cid}/portrait", {"image_base64": base64.b64encode(png(64, 80)).decode()}, T)
        elif st["key"] == "auto_equip": call("player", "POST", "/api/characters/auto-equip", {"character_id": cid, "answer": "yes"}, T)
        elif isinstance(st.get("request"), dict) and st["request"].get("method"):
            rq = st["request"]; call("player", rq["method"], rq["path"], rq.get("body"), T)
mark("The campaign starts: the shop closes")
c, camp = call("dm", "POST", "/api/campaigns", {"name": f"Shop Test {random.randint(1000,9999)}", "setting": "x", "max_players": 4}, G)
cmp = camp["campaign_id"]
call("player", "POST", f"/api/campaigns/{cmp}/join", {"character_id": cid}, T)
call("dm", "POST", f"/api/campaigns/{cmp}/start", None, G)
call("player", "POST", f"/api/characters/{cid}/shop", {"buy": [{"item": "Rope", "quantity": 1}]}, T, note="Buying after the campaign has started")
print("done", cid)
