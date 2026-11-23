# Wider build test: every class x every background, species rotating through all, sub-choices rotating
# through the options (first / last / middle), the other equipment options, then the same setup as test_builds.py.
import json, urllib.request, random, base64, sys
import os
# a step the server marks optional or asked (the starting level, the shop, the portrait) is left as it is unless this script follows it by name
# (the portrait: the upload is tested); "left open" counts only the required steps
FOLLOWED_OPTIONAL = {"portrait"}
def open_step(x): return not x.get("done") and ((not x.get("optional") and not x.get("asked")) or x.get("key") in FOLLOWED_OPTIONAL)

exec(open(os.path.join(os.path.dirname(os.path.abspath(__file__)), "test_builds.py")).read().split("# class, background, species")[0].replace('build_tests.jsonl','build_tests2.jsonl'))
def ov(o,f=None):
    if not isinstance(o,dict): return o
    return o.get(f) or o.get("value") or o.get("key") or o.get("id") or o.get("name")
SKIP={"class","background","species","ability_method","name","alignment","str, dex, con, int, wis, cha"}
r = call("POST", "/api/register", {"name": "Wide%d" % random.randint(1000, 9999), "password": "x"})[1]
TOKEN = r["token"]
# lists from the server
code,d=call("POST","/api/character-builder",{"name":"Probe %d"%random.randint(100,999)})
cs=d["current_step"]; classes=[ov(o,"class") for o in cs["choices"][0]["options"]]
P="/api/character-builder/%d"%d["draft_id"]; v=call("POST",P,{"class":classes[0]})[1]
v=call("POST",P,{"use_suggestion":True})[1]
v=call("GET",P)[1]
bgs=[ov(o,"background") for c in v["current_step"]["choices"] if c["field"]=="background" for o in c["options"]]
v=call("POST",P,{"background":bgs[0]})[1]; v=call("POST",P,{"use_suggestion":True})[1]; v=call("GET",P)[1]
sps=[ov(o,"species") for c in v["current_step"]["choices"] if c["field"]=="species" for o in c["options"]]
print("classes",classes,"\nbackgrounds",bgs,"\nspecies",sps)
results=[]; n=0
for cls in classes:
  for bg in bgs:
    sp=sps[n%len(sps)]; variant=n%3; n+=1
    tag=f"{cls}/{bg}/{sp}/v{variant}"; res={"build":tag,"problems":[],"picked":{}}
    code,d=call("POST","/api/character-builder",{"name":f"W {cls[:4]} {random.randint(100000,999999)}"},tag)
    P="/api/character-builder/%d"%d["draft_id"]
    for body in ({"class":cls},{"background":bg},{"species":sp}):
        x=call("POST",P,body,tag)[1]
        if x.get("error"): res["problems"].append(f"{body}: {x.get('error')} {x.get('message')}")
    last=None; tried=set()
    for i in range(30):
        code,v=call("GET",P,None,tag) if i else (200,x)
        if "Every step is done" in (v.get("next") or ""): break
        cs=v.get("current_step") or {}; key=cs.get("key")
        body={}
        if key not in tried:
            tried.add(key)
            for c in cs.get("choices",[]):
                f=c.get("field"); opts=[ov(o,f.split(".")[-1]) for o in (c.get("options") if isinstance(c.get("options"),list) else [])]
                if f in SKIP or f in ("backstory","appearance","backstory_skip") or not opts or not all(isinstance(p,str) for p in opts) or c.get("done") and f not in ("starting_equipment","background_equipment"): continue
                k=c.get("count") or 1
                if variant==0: pick=opts[:k]
                elif variant==1: pick=opts[-k:]
                else: m=max(0,len(opts)//2-k//2); pick=opts[m:m+k]
                if f=="starting_equipment" or f=="background_equipment": pick=[opts[-1 if variant==1 else 0]]
                val=[p.split(":")[0] if f in("starting_equipment",) else p for p in pick]
                val=val if k>1 or f=="tool_proficiencies" else val[0]   # a tool answer is a list even for one tool
                if f.startswith("species_feat_choices."):   # a feat's parts go only with that feat: proficiencies with Skilled, the rest with Magic Initiate
                    feat=(body.get("species_choices") or {}).get("feat")
                    if feat!=("skilled" if f.endswith(".proficiencies") else "magic_initiate"): continue
                if "." in f:
                    a,b=f.split(".",1); body.setdefault(a,{})[b]=val
                else: body[f]=val
            if key=="details": body["alignment"]=["Lawful Good","Chaotic Neutral","Neutral"][variant]
            if key=="backstory": body["backstory"]="One.\n\nTwo.\n\nThree.\n\nFour."
        if body:
            res["picked"][key]=body
            code,v=call("POST",P,body,tag)
            if v.get("error"): res["problems"].append(f"{key} {body}: {v.get('error')} {v.get('message')}")
        else:
            code,v=call("POST",P,{"use_suggestion":True},tag)
            if key==last and v.get("current_step",{}).get("key")==key:
                cs=v.get("current_step") or {}
                res["problems"].append(f"stuck at {cs.get('title')}: {cs.get('problems')} {[c.get('problem') for c in cs.get('choices',[]) if c.get('problem')]}"); break
        last=key
    code,f=call("POST",P+"/finish",{},tag)
    if not f.get("character_id"):
        res["problems"].append(f"finish failed: {json.dumps(f)[:400]}"); results.append(res); continue
    cid=f["character_id"]; res["hp"],res["ac"]=f.get("hp"),f.get("ac")
    for rnd in range(8):
        s=call("GET",f"/api/characters/{cid}/setup",None,tag)[1]
        op=[x for x in s.get("steps",[]) if open_step(x)]
        if not op: break
        acted=False
        for st in op:
            k=st["key"]
            if k=="portrait":
                o=call("POST",f"/api/characters/{cid}/portrait",{"image_base64":IMG},tag)[1]; acted=True
                if o.get("error"): res["problems"].append(f"portrait {o}")
            elif k=="auto_equip":
                o=call("POST","/api/characters/auto-equip",{"character_id":cid,"answer":"yes"},tag)[1]; ch={}
                for _ in range(3):
                    if not o.get("needs_choices"): break
                    for q in o["needs_choices"]: ch[q["key"]]=q["options"][-1 if variant==1 else 0]["value"]
                    o=call("POST","/api/characters/auto-equip",{"character_id":cid,"answer":"yes","choices":ch},tag)[1]
                if o.get("error") or not o.get("success") or [x for x in (o.get("results") or []) if not x.get("done")]:
                    res["problems"].append(f"auto-equip {json.dumps(o)[:400]}")
                res["equipped"]=o.get("now"); acted=True
            elif isinstance(st.get("request"),dict) and st["request"].get("method"):
                rq=st["request"]; c2,o=call(rq["method"],rq["path"],rq.get("body"),tag); acted=True
                if c2>=400 or o.get("error"): res["problems"].append(f"setup {k}: {o.get('error')} {o.get('message')}")
                if st.get("then"):
                    t=st["then"]["request"]; c3,o3=call(t["method"],t["path"],t.get("body"),tag)
                    if c3>=400 or o3.get("error"): res["problems"].append(f"setup {k} then: {o3.get('error')} {o3.get('message')}")
        if not acted: break
    s=call("GET",f"/api/characters/{cid}/setup",None,tag)[1]
    left=[x["key"] for x in s.get("steps",[]) if open_step(x)]
    if left: res["problems"].append(f"setup still open: {left}")
    sh=call("GET",f"/api/characters/{cid}",None,tag)[1]
    res["finished"]=sh.get("character_finished"); res["eq"]=sh.get("equipment")
    if not sh.get("character_finished"): res["problems"].append(f"not finished: {sh.get('to_finish')}")
    results.append(res)
json.dump(results,open(D+"build_results2.json","w"),indent=1)
ok=sum(1 for r in results if not r["problems"]); print(f"{ok}/{len(results)} OK")
for r in results:
    if r["problems"]:
        print("FAIL",r["build"]); [print("   -",p[:350]) for p in r["problems"]]
import sys
sys.exit(1 if any(r["problems"] for r in results) else 0)  # a failed build fails the run (audit AUD-48)
