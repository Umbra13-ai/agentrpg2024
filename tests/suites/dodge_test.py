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
c,camp=call("POST","/api/campaigns",{"name":"DODGE Crypt %d"%random.randint(100,99999),"setting":"A crypt.","max_players":4},G,"pt")
cmp=camp.get("campaign_id") or camp.get("id")
for cls,cid in chars.items(): call("POST",f"/api/campaigns/{cmp}/join",{"character_id":cid},toks[cls],"pt")
c,j=call("POST",f"/api/campaigns/{cmp}/start",None,G,"pt")
B=f"/api/campaigns/{cmp}/combat"
c,j=call("POST",B+"/start",{},G,"pt"); check("combat start",c==200,j)
c,j=call("POST",B+"/add",{"combatants":[{"name":"Goblin A","monster_key":"goblin-warrior","initiative":-5,"hp":500},{"name":"Goblin B","monster_key":"goblin-warrior","initiative":-6,"hp":500}]},G,"pt"); check("add goblins",c==200,j)
B=f"/api/campaigns/{cmp}/combat"
names={cls:call("GET",f"/api/characters/{cid}",None,toks[cls],"d")[1]["name"] for cls,cid in chars.items()}
call("POST",B+"/start",{},G,"d")
c,j=call("POST",B+"/add",{"combatants":[{"name":"Gob","monster_key":"goblin-warrior","initiative":-5,"hp":300},{"name":"Mage","monster_key":"mage","initiative":-6,"hp":300}]},G,"d"); check("monsters added",c==200,j)
c,st=call("GET",B,None,G,"d"); order=st["combatants"]; by_id={cid:cls for cls,cid in chars.items()}; pcs=[by_id[e["id"]] for e in order if e["id"] in by_id]
mage=[e for e in order if e["name"]=="Mage"][0]; print("   mage actions:",[a["name"] for a in mage.get("stat_block",{}).get("actions",[])])
d=pcs[0]
for p in pcs: call("POST","/api/pending-turn",{"steps":[{"action":"dodge"}],"character_id":chars[p],"campaign_id":cmp},toks[p],"d")
c,rep=call("POST",B+"/resolve",{},G,"d"); check("everyone dodged; the walk is at the goblin",(rep.get("stopped_at") or {}).get("name")=="Gob",rep.get("stopped_at"))
# the Opportunity Attack below really hits: a big roll used to drop a level 1 dodger to 0 HP (Unconscious ends Dodge), so it gets
# Hit Points to spare (the GM may set them)
call("POST","/api/gm/update-character",{"character_id":chars[d],"set":{"hp":200,"max_hp":200}},G,"d")
def mon(m,a,t,extra=None):
    ma={"monster":m,"action":a,"target":t,"apply_damage":False}; ma.update(extra or {})
    return call("POST","/api/gm/narrate",{"campaign_id":cmp,"narration":"x","monster_action":ma},G,"d")[1]
print("--- one rule, every path")
j=mon("Gob","Scimitar",names[d]); t=j.get("monster_action_result",""); check("stat-block attack: Disadvantage (the target is Dodging)","with Disadvantage (the target is Dodging)" in t,t)
j=mon("Gob","Shortbow",names[d]); t=j.get("monster_action_result",""); check("ranged stat-block attack: Disadvantage","Disadvantage" in t and "Dodging" in t,t)
c,j=call("POST","/api/gm/opportunity-attack",{"campaign_id":cmp,"target_id":chars[d],"attacker_is_monster":True,"monster_name":"Gob","monster_key":"goblin-warrior"},G,"d"); t=json.dumps(j)
check("Opportunity Attack against the dodging character: Disadvantage","the target is Dodging" in t,t[:400])
call("POST",B+"/next",{},G,"d")
j=mon("Mage","Spellcasting",names[d],{"use":"Fire Bolt"}); t=j.get("monster_action_result","") or json.dumps(j)
print("   mage spell attack:",t[:260])
if "Spell attack" in t: check("a monster's spell attack: Disadvantage","with Disadvantage (the target is Dodging)" in t,t)
else:
    j=mon("Mage","Arcane Burst",names[d]); t=j.get("monster_action_result","") or json.dumps(j); print("   mage arcane burst:",t[:260])
    check("a monster's spell or magical attack: Disadvantage","Disadvantage" in t and "Dodging" in t,t)
c,j=call("POST","/api/gm/saving-throw",{"campaign_id":cmp,"character_id":chars[d],"ability":"dex","dc":12},G,"d"); t=json.dumps(j)
# the save's own lists, not a substring ("dvantage" also matched "Disadvantage"; audit AUD-48)
check("Dexterity save while dodging: Advantage",c==200 and "Dodge" in (j.get("advantage") or []) and not j.get("disadvantage"),t[:500])
c,j=call("POST","/api/gm/saving-throw",{"campaign_id":cmp,"character_id":chars[d],"ability":"wis","dc":12},G,"d"); t=json.dumps(j)
# a save was rolled (an error reply has no Dodge in it either; audit AUD-48) and Dodge gave it nothing
check("Wisdom save while dodging: no Advantage from Dodge",c==200 and j.get("outcome") and "Dodge" not in (j.get("advantage") or []) and "Dodge" not in t,t[:400])
print("--- Dodge ends at the start of the dodger's next turn")
c,j=call("POST",B+"/next",{},G,"d")   # mage's turn ends -> round 2, first PC's turn starts
c,st=call("GET",B,None,G,"d"); print("   current:",st.get("current_turn"),"| dodger:",names[d])
c,s=call("GET",f"/api/characters/{chars[d]}",None,toks[d],"d"); check("the marker is gone when the dodger's turn starts","dodging" not in (s.get("conditions") or []),s.get("conditions"))
other=pcs[1]; c,s=call("GET",f"/api/characters/{chars[other]}",None,toks[other],"d"); check("another character who dodged still has it until their own turn","dodging" in (s.get("conditions") or []),s.get("conditions"))
call("POST","/api/gm/conditions",{"campaign_id":cmp,"target_id":chars[other],"condition":"restrained"},G,"d")
j=mon("Gob","Scimitar",names[other]); t=j.get("monster_action_result",""); check("Restrained (Speed 0): Dodge gives nothing","Dodging" not in t,t)
print("FAILS:",fails)
import sys
sys.exit(1 if fails else 0)  # a failed check fails the run (audit AUD-48)
