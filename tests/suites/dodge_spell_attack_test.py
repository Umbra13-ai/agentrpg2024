import os, subprocess
os.environ.setdefault("TOK_BASE","0")
from ah import *
fails=[]
def check(label, cond, detail=""):
    print(("PASS " if cond else "FAIL ")+label+((" | "+str(detail)[:300]) if (detail and not cond) else ""))
    if not cond: fails.append(label)
G=register("GM")["token"]
chars={}; toks={}; names={}
for cls,bg,sp in [("fighter","soldier","human"),("cleric","acolyte","dwarf"),("wizard","sage","elf"),("rogue","criminal","halfling")]:
    P=register("P"+cls)["token"]; toks[cls]=P
    cid,f=build(P,cls,bg,sp,"PT")
    c,s=call("GET",f"/api/characters/{cid}/setup",None,P,"pt")
    for st in s.get("steps",[]):
        if st["key"]=="auto_equip":
            c,o=call("POST","/api/characters/auto-equip",{"character_id":cid,"answer":"yes"},P,"pt")
            if o.get("needs_choices"):
                call("POST","/api/characters/auto-equip",{"character_id":cid,"answer":"yes","choices":{q["key"]:q["options"][0]["value"] for q in o["needs_choices"]}},P,"pt")
        elif st["key"]=="portrait": call("POST",f"/api/characters/{cid}/portrait",{"skip":True},P,"pt")
        elif not st.get("done") and isinstance(st.get("request"),dict) and st["request"].get("method"):
            rq=st["request"]; call(rq["method"],rq["path"],rq.get("body"),P,"pt")
    chars[cls]=cid
c,camp=call("POST","/api/campaigns",{"name":"HAG Crypt %d"%random.randint(100,99999),"setting":"A crypt.","max_players":4},G,"pt")
cmp=camp.get("campaign_id") or camp.get("id")
for cls,cid in chars.items(): call("POST",f"/api/campaigns/{cmp}/join",{"character_id":cid},toks[cls],"pt")
c,j=call("POST",f"/api/campaigns/{cmp}/start",None,G,"pt")
B=f"/api/campaigns/{cmp}/combat"
c,j=call("POST",B+"/start",{},G,"pt"); check("combat start",c==200,j)
c,j=call("POST",B+"/add",{"combatants":[{"name":"Goblin A","monster_key":"goblin","initiative":-5,"hp":500},{"name":"Goblin B","monster_key":"goblin","initiative":-6,"hp":500}]},G,"pt"); check("add goblins",c==200,j)
B=f"/api/campaigns/{cmp}/combat"
names={cls:call("GET",f"/api/characters/{cid}",None,toks[cls],"d")[1]["name"] for cls,cid in chars.items()}
call("POST",B+"/start",{},G,"d")
c,j=call("POST",B+"/add",{"combatants":[{"name":"Hag","monster_key":"green-hag","initiative":-5,"hp":300}]},G,"d")
c,st=call("GET",B,None,G,"d"); hag=[e for e in st["combatants"] if e["name"]=="Hag"][0]; print("   hag actions:",[(a["name"],a.get("desc","")[:160]) for a in hag.get("stat_block",{}).get("actions",[])])
by_id={cid:cls for cls,cid in chars.items()}; pcs=[by_id[e["id"]] for e in st["combatants"] if e["id"] in by_id]; d=pcs[0]
for p in pcs: call("POST","/api/pending-turn",{"steps":[{"action":"dodge"}],"character_id":chars[p],"campaign_id":cmp},toks[p],"d")
call("POST",B+"/resolve",{},G,"d")
got=None
for use in ("Ray of Sickness","ray-of-sickness"):
    c,j=call("POST","/api/gm/narrate",{"campaign_id":cmp,"narration":"x","monster_action":{"monster":"Hag","action":"Spellcasting","target":names[d],"use":use,"apply_damage":False}},G,"d")
    t=j.get("monster_action_result","") or json.dumps(j); print("   ",use,"->",t[:300])
    if "Spell attack" in t: got=t; break
check("a monster's spell attack against a dodging character: Disadvantage",bool(got) and "with Disadvantage (the target is Dodging)" in got,got)
print("FAILS:",fails)
import sys
sys.exit(1 if fails else 0)  # a failed check fails the run (audit AUD-48)
