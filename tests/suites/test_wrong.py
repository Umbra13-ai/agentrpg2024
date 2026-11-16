# Wrong answers at every builder step, one at a time, on top of a valid draft. Records what the server says.
import time
import json, urllib.request, random, copy
from target import BASE as SERVER, CTX  # where the test runs (target.py)
from target import LOG_DIR as D  # where the logs go (ARPG_LOG_DIR)
log=open(D+"wrong_tests.jsonl","w"); TOKEN=None
def call(m,p,b=None,label=""):
    h={"Content-Type":"application/json"}
    if TOKEN: h["Authorization"]="Bearer "+TOKEN
    r=urllib.request.Request(SERVER+p,data=json.dumps(b).encode() if b is not None else None,method=m,headers=h)
    try:
        with urllib.request.urlopen(r, context=CTX) as x: code,t=x.status,x.read().decode()
    except urllib.error.HTTPError as e: code,t=e.code,e.read().decode()
    try: js=json.loads(t)
    except Exception: js={"_raw":t[:300]}
    log.write(json.dumps({"label":label,"method":m,"path":p,"status":code,"request":b,"response":js})+"\n")
    return code,js
TOKEN=call("POST","/api/register",{"name":"Wrong%d"%random.randint(1000,9999),"password":"x"})[1]["token"]
BASE={"ability_increases":{"cha":1,"wis":2},"ability_method":"standard_array","alignment":"Neutral","background":"acolyte","background_equipment":"a",
 "background_feat_choices":{"cantrips":["guidance","sacred-flame"],"list":"cleric","spell":"bless","spellcasting_ability":"wis"},
 "backstory":"One.\n\nTwo.\n\nThree.","cha":12,"class":"cleric","class_options":{"divine_order":"thaumaturge"},"con":13,"dex":8,"extra_languages":["Gnomish","Giant"],
 "int":10,"skill_proficiencies":["medicine","persuasion"],"species":"tiefling","species_choices":{"legacy":"chthonic","size":"small","spellcasting_ability":"wis"},
 "starting_equipment":"a","str":14,"wis":15}
def draft(base,label):
    b=copy.deepcopy(base); b["name"]="Wrong Test %d"%(time.time_ns()%10**12)  # unique across runs: a random 5-digit name collided with an earlier run's
    c,d=call("POST","/api/character-builder",{"name":b["name"]},label)
    P="/api/character-builder/%d"%d["draft_id"]
    c,v=call("POST",P,b,label)
    return P,v
def find(v,field):
    out=[]
    for st in v.get("steps",[]) + ([v["current_step"]] if isinstance(v.get("current_step"),dict) else []):
        for c in st.get("choices",[]) or []:
            if c.get("field")==field or (field and c.get("field","").startswith(field)):
                out.append({"step":st.get("title"),"done":c.get("done"),"current":c.get("current"),"problem":c.get("problem")})
    return out[:1]
results=[]
def T(label,bad,field,base=BASE,note=""):
    P,v0=draft(base,label)
    code,v=call("POST",P,bad,label)
    g=call("GET",P,None,label)[1]
    cs=g.get("current_step") or {}
    r={"test":label,"sent":bad,"http":code,"error":v.get("error"),"message":v.get("message"),"choice":find(g,field),
       "current_step":cs.get("title"),"step_problems":cs.get("problems"),"next":g.get("next"),"choices_remaining":g.get("choices_remaining")}
    # can it still finish?
    c2,f=call("POST",P+"/finish",{},label)
    r["finish"]= "created" if f.get("character_id") else (f.get("error") or (f.get("draft") or {}).get("current_step",{}).get("problems") or f.get("message") or str(f)[:200])
    results.append(r); return P
ELF=dict(copy.deepcopy(BASE),species="elf",species_choices={"lineage":"high_elf","spellcasting_ability":"wis","skill":"perception"})
DRAGON=dict(copy.deepcopy(BASE),species="dragonborn",species_choices={"ancestry":"red"})
HUMAN=dict(copy.deepcopy(BASE),species="human",species_choices={"size":"medium","skill":"perception","feat":"skilled"},species_feat_choices={"proficiencies":["stealth","athletics","survival"]})
# the valid base first: it must finish
T("valid base draft (control)",{},"class")
T("class: not a class",{"class":"necromancer"},"class")
T("skills: only 1 of 2",{"skill_proficiencies":["medicine"]},"skill_proficiencies")
T("skills: 3 of 2",{"skill_proficiencies":["medicine","persuasion","history"]},"skill_proficiencies")
T("skills: not on the class list",{"skill_proficiencies":["stealth","medicine"]},"skill_proficiencies")
T("skills: same skill twice",{"skill_proficiencies":["medicine","medicine"]},"skill_proficiencies")
T("skills: one the background gives",{"skill_proficiencies":["insight","medicine"]},"skill_proficiencies")
T("divine order: not an order",{"class_options":{"divine_order":"wizardry"}},"class_options")
T("starting equipment: no option c",{"starting_equipment":"c"},"starting_equipment")
T("background: not a background",{"background":"pirate"},"background")
T("background equipment: no option z",{"background_equipment":"z"},"background_equipment")
T("Magic Initiate: spell not on the list",{"background_feat_choices":{"cantrips":["guidance","sacred-flame"],"list":"cleric","spell":"magic-missile","spellcasting_ability":"wis"}},"background_feat_choices")
T("Magic Initiate: one cantrip",{"background_feat_choices":{"cantrips":["guidance"],"list":"cleric","spell":"bless","spellcasting_ability":"wis"}},"background_feat_choices")
T("Magic Initiate: ability str",{"background_feat_choices":{"cantrips":["guidance","sacred-flame"],"list":"cleric","spell":"bless","spellcasting_ability":"str"}},"background_feat_choices")
SOLD=dict(copy.deepcopy(BASE),background="soldier",background_tool="dice set"); SOLD.pop("background_feat_choices")
SOLD["ability_increases"]={"con":1,"str":2}
T("Soldier gaming set: lute",{"background_tool":"lute"},"background_tool",SOLD)
T("species: aasimar (not on server)",{"species":"aasimar"},"species")
T("species: not a species",{"species":"robot"},"species")
T("tiefling legacy: not one",{"species_choices":{"legacy":"celestial","size":"small","spellcasting_ability":"wis"}},"species_choices.legacy")
T("tiefling size: huge",{"species_choices":{"legacy":"chthonic","size":"huge","spellcasting_ability":"wis"}},"species_choices.size")
T("tiefling spell ability: str",{"species_choices":{"legacy":"chthonic","size":"small","spellcasting_ability":"str"}},"species_choices.spellcasting_ability")
T("dragonborn ancestry: purple",{"species_choices":{"ancestry":"purple"}},"species_choices.ancestry",DRAGON)
T("elf lineage: dark",{"species_choices":{"lineage":"dark","spellcasting_ability":"wis","skill":"perception"}},"species_choices.lineage",ELF)
T("elf skill: not a Keen Senses skill",{"species_choices":{"lineage":"high_elf","spellcasting_ability":"wis","skill":"arcana"}},"species_choices.skill",ELF)
T("elf skill: one the class gives (medicine)",{"species_choices":{"lineage":"high_elf","spellcasting_ability":"wis","skill":"insight"}},"species_choices.skill",ELF)
T("human feat: not an Origin feat",{"species_choices":{"size":"medium","skill":"perception","feat":"great_weapon_master"}},"species_choices.feat",HUMAN)
T("human Skilled: 2 of 3",{"species_feat_choices":{"proficiencies":["stealth","athletics"]}},"species_feat_choices",HUMAN)
T("human Skilled: a skill already had",{"species_feat_choices":{"proficiencies":["medicine","athletics","survival"]}},"species_feat_choices",HUMAN)
T("scores: hand-picked Standard Array, different order (valid)",{"str":8,"dex":13,"con":14,"int":10,"wis":15,"cha":12},"str")
T("scores: an 18",{"str":18},"str")
T("scores: 15 twice",{"str":15,"wis":15},"str")
T("scores: point buy",{"ability_method":"point_buy"},"ability_method")
T("scores: random",{"ability_method":"random"},"ability_method")
T("increases: not a background ability",{"ability_increases":{"str":2,"wis":1}},"ability_increases")
T("increases: +3 on one",{"ability_increases":{"wis":3}},"ability_increases")
T("increases: +2 +2",{"ability_increases":{"wis":2,"cha":2}},"ability_increases")
T("increases: +1 +1 +1 (valid)",{"ability_increases":{"wis":1,"cha":1,"int":1}},"ability_increases")
T("name: empty",{"name":""},"name")
T("alignment: not an alignment",{"alignment":"Chaotic Stupid"},"alignment")
T("alignment: Chaotic Evil (allowed with a warning?)",{"alignment":"Chaotic Evil"},"alignment")
T("languages: not a language",{"extra_languages":["Klingon","Giant"]},"extra_languages")
T("languages: 3 of 2",{"extra_languages":["Gnomish","Giant","Orc"]},"extra_languages")
T("languages: Common (already known)",{"extra_languages":["Common","Giant"]},"extra_languages")
T("backstory: 1 paragraph",{"backstory":"Just one paragraph."},"backstory")
T("backstory: 8 paragraphs",{"backstory":"\n\n".join("P%d."%i for i in range(8))},"backstory")
T("backstory: over 4000 characters",{"backstory":"x"*4100},"backstory")
NOSTORY=copy.deepcopy(BASE); NOSTORY.pop("backstory")
T("backstory: skipped",{"backstory_skip":True},"backstory",NOSTORY)
# a taken name: create a character, then a new draft with the same name
P,v=draft(BASE,"name taken: setup"); c,f=call("POST",P+"/finish",{},"name taken: setup")
taken=(f.get("name") or (f.get("character") or {}).get("name"))
code,sheet=call("GET","/api/characters/%s"%f.get("character_id"),None,"name taken: setup")
taken=sheet.get("name") or taken
c,d=call("POST","/api/character-builder",{"name":taken},"name taken")
r={"test":"name taken: new draft with the same name","sent":{"name":taken},"http":c,"error":d.get("error"),"message":d.get("message"),"next":d.get("next")}
if d.get("draft_id"):
    P2="/api/character-builder/%d"%d["draft_id"]; b=copy.deepcopy(BASE); b["name"]=taken
    c,v=call("POST",P2,b,"name taken"); g=call("GET",P2,None,"name taken")[1]
    r["after_all_steps"]={"current_step":(g.get("current_step") or {}).get("title"),"problems":(g.get("current_step") or {}).get("problems"),"name_choice":find(g,"name")}
    c2,f2=call("POST",P2+"/finish",{},"name taken"); r["finish"]="created" if f2.get("character_id") else (f2.get("error") or str(f2)[:300])
results.append(r)
json.dump(results,open(D+"wrong_results.json","w"),indent=1)
for r in results:
    print("==",r["test"],"| http",r.get("http"),"|",r.get("error") or "", r.get("message") or "")
    print("   choice:",r.get("choice"),"| step:",r.get("current_step"),r.get("step_problems"),"| finish:",str(r.get("finish"))[:250])
