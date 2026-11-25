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
        elif st["key"]=="portrait": call("POST",f"/api/characters/{cid}/portrait",{"my_human_declined":"test account: there is no human to ask"},P,"pt")
        elif not st.get("done") and isinstance(st.get("request"),dict) and st["request"].get("method"):
            rq=st["request"]; call(rq["method"],rq["path"],rq.get("body"),P,"pt")
    chars[cls]=cid
c,camp=call("POST","/api/campaigns",{"name":"KICK Crypt %d"%random.randint(100,99999),"setting":"A crypt.","max_players":4},G,"pt")
cmp=camp.get("campaign_id") or camp.get("id")
for cls,cid in chars.items(): call("POST",f"/api/campaigns/{cmp}/join",{"character_id":cid},toks[cls],"pt")
c,j=call("POST",f"/api/campaigns/{cmp}/start",None,G,"pt")
B=f"/api/campaigns/{cmp}/combat"
c,j=call("POST",B+"/start",{},G,"pt"); check("combat start",c==200,j)
c,j=call("POST",B+"/add",{"combatants":[{"name":"Goblin A","monster_key":"goblin-warrior","initiative":-5,"hp":500},{"name":"Goblin B","monster_key":"goblin-warrior","initiative":-6,"hp":500}]},G,"pt"); check("add goblins",c==200,j)
B=f"/api/campaigns/{cmp}/combat"
call("POST",B+"/start",{},G,"k"); call("POST",B+"/add",{"combatants":[{"name":"Gob","monster_key":"goblin-minion","initiative":-5}]},G,"k")
r=chars["rogue"]; nm=call("GET",f"/api/characters/{r}",None,toks["rogue"],"k")[1]["name"]
call("POST","/api/pending-turn",{"steps":[{"action":"dodge"}],"character_id":r,"campaign_id":cmp},toks["rogue"],"k")
c,j=call("POST","/api/gm/kick-character",{"campaign_id":cmp,"character_id":r},toks["fighter"],"k"); check("a player can't remove a character",c==403,j)
c,j=call("POST","/api/gm/kick-character",{"campaign_id":cmp,"character_id":r,"reason":"moved away"},G,"k"); check("GM removes the character",c==200 and "not erased" in j.get("message",""),j)
c,s=call("GET",f"/api/characters/{r}",None,toks["rogue"],"k"); check("the character still exists, with its sheet",c==200 and s.get("name")==nm and s.get("level")==1,(c,str(s)[:200]))
c,st=call("GET",B,None,G,"k"); check("it is out of the turn order",r not in [e["id"] for e in st.get("combatants",[])],[e["name"] for e in st.get("combatants",[])])
c,j=call("GET",f"/api/pending-turn?character_id={chars['fighter']}&campaign_id={cmp}",None,toks["fighter"],"k"); check("its pending turn is gone from the queue",all(q.get("character_id")!=r for q in j.get("queue",[])),j.get("queue"))
c,j=call("POST","/api/gm/kick-character",{"campaign_id":cmp,"character_id":r},G,"k"); check("removing it again: not in a campaign (character_not_in_campaign, gm_campaign_2024.go)",c==400 and j.get("error")=="character_not_in_campaign",j)
c,j=call("DELETE",f"/api/characters/{r}",{"confirm":nm},toks["rogue"],"k"); check("the player can now delete it",c==200,j)
c,s=call("GET",f"/api/characters/{chars['fighter']}",None,toks["fighter"],"k"); check("the others are untouched",c==200,s)
c,rep=call("POST",B+"/resolve",{},G,"k"); check("the fight goes on",c==200,rep)
print("FAILS:",fails)
import sys
sys.exit(1 if fails else 0)  # a failed check fails the run (audit AUD-48)
