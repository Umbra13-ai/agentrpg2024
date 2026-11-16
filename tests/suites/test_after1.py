# After creation, part 1: setup steps with wrong and refusing answers.
from ah import *
out=[]
def note(test,code,js,keys=None):
    r={"test":test,"http":code}
    for k in (keys or ["error","message","success","nothing_was_changed","now","needs_choices","answered_before","results"]):
        if isinstance(js,dict) and k in js: r[k]=js[k]
    out.append(r); return r
P=register("After"); T=P["token"]
rogue,f=build(T,"rogue","criminal","halfling","AfterA")
note("setup: rogue created",200,{"success":bool(rogue)})
c,s=call("GET",f"/api/characters/{rogue}/setup",None,T,"setup")
note("setup: open steps",c,{"message":[(x["key"],x.get("done")) for x in s.get("steps",[])]})
c,j=call("POST","/api/characters/auto-equip",{"character_id":rogue,"answer":"maybe"},T,"ae"); note("auto-equip: answer maybe",c,j)
c,j=call("GET",f"/api/characters/auto-equip?character_id={rogue}",None,T,"ae"); note("auto-equip: GET preview",c,j,["question","i_would_do","answer_with"])
c,j=call("POST","/api/characters/auto-equip",{"character_id":rogue,"answer":"yes","choices":{"main_hand":"i999","off_hand":"zzz"}},T,"ae"); note("auto-equip: yes with choices that are not options",c,j)
c,s=call("GET",f"/api/characters/{rogue}",None,T,"ae"); note("auto-equip: sheet after bad choices",c,{"now":s.get("equipment")})
c,j=call("POST","/api/characters/auto-equip",{"character_id":rogue,"answer":"no"},T,"ae"); note("auto-equip: answer no",c,j)
c,s=call("GET",f"/api/characters/{rogue}/setup",None,T,"ae"); note("auto-equip: setup after no",c,{"message":[(x["key"],x.get("done")) for x in s.get("steps",[])]})
c,sh=call("GET",f"/api/characters/{rogue}",None,T,"ae"); note("auto-equip: to_finish after no",c,{"message":sh.get("to_finish"),"now":sh.get("equipment")})
c,j=call("POST","/api/characters/auto-equip",{"character_id":rogue,"answer":"yes"},T,"ae"); note("auto-equip: yes after no",c,j)
# someone else's character
O=register("Other")["token"]
c,j=call("POST","/api/characters/auto-equip",{"character_id":rogue,"answer":"yes"},O,"ae"); note("auto-equip: another player's character",c,j)
c,j=call("POST",f"/api/characters/{rogue}/portrait",{"image_base64":base64.b64encode(png(64,80)).decode()},O,"portrait"); note("portrait: another player's character",c,j)
# portraits
for name,body,raw,ct in [
    ("portrait: 16 x 16 pixels",{"image_base64":base64.b64encode(png(16,16)).decode()},None,"application/json"),
    ("portrait: text, not an image",{"image_base64":base64.b64encode(b"hello world this is not an image").decode()},None,"application/json"),
    ("portrait: broken base64",{"image_base64":"!!!notbase64!!!"},None,"application/json"),
    ("portrait: over 5 MB",{"image_base64":base64.b64encode(b"\x89PNG\r\n\x1a\n"+b"\0"*(5*1024*1024+10)).decode()},None,"application/json"),
    ("portrait: square 512 x 512 (allowed, not 4:5)",{"image_base64":base64.b64encode(png(512,512)).decode()},None,"application/json"),
    ("portrait: raw PNG bytes",None,png(400,500),"image/png"),
    ("portrait: raw bytes with the wrong type",None,png(400,500),"image/gif"),
]:
    c,j=call("POST",f"/api/characters/{rogue}/portrait",body,T,"portrait",raw=raw,ctype=ct); note(name,c,j,["error","message","success","width","height","warning","note","allowed","max_bytes"])
# spells: a cleric with wrong picks
cler,f=build(T,"cleric","acolyte","dwarf","AfterC")
c,s=call("GET",f"/api/characters/{cler}/setup",None,T,"spells")
sp=[x for x in s.get("steps",[]) if x["key"]=="spells"]
note("spells: the step's request",c,{"message":sp[0].get("request") if sp else "no spells step"})
if sp and sp[0].get("request"):
    rq=sp[0]["request"]; path=rq["path"]
    for name,body in [("spells: a wizard spell (magic-missile)",{"spells":["magic-missile"]}),
                      ("spells: a level 3 spell (revivify)",{"spells":["revivify"]}),
                      ("spells: a made-up spell",{"spells":["super-heal"]}),
                      ("spells: too many (10)",{"spells":["bless","cure-wounds","healing-word","guiding-bolt","shield-of-faith","command","detect-magic","sanctuary","inflict-wounds","protection-from-evil-and-good"]}),
                      ("spells: the same spell twice",{"spells":["bless","bless"]})]:
        b=dict(rq.get("body") or {}); 
        key=[k for k in b if "spell" in k and isinstance(b[k],list)]
        k=key[0] if key else "spells"; b[k]=body["spells"]
        c,j=call(rq["method"],path,b,T,"spells"); note(name+f" [{k}]",c,j,["error","message","success","prepared","ignored_spells","problems","warnings"])
# backstory edits after creation
c,j=call("POST",f"/api/characters/{cler}/profile",{"backstory":"x"*4100},T,"profile"); note("profile: backstory over 4000",c,j,["error","message","success"])
c,j=call("POST",f"/api/characters/{cler}/profile",{"appearance":"A broad dwarf with a braided red beard."},T,"profile"); note("profile: appearance",c,j,["error","message","success"])
c,s=call("GET",f"/api/characters/{cler}/setup",None,T,"profile")
pp=[x for x in s.get("steps",[]) if x["key"]=="portrait"]
note("portrait prompt after appearance",c,{"message":(pp[0].get("choices") or {}).get("prompt_for_an_image_generator") if pp else None})
json.dump(out,open(D+"after1_results.json","w"),indent=1)
for r in out: print(json.dumps(r)[:700])
