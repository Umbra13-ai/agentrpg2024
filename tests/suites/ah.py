import json, urllib.request, random, base64, zlib, struct, os, ssl, re
# The request helpers of the suites in this folder: they only send requests (a suite that reads or sets the database stays
# outside this repository).
from target import BASE, TARGET, LOCAL, STAGING, LIVE, only, CTX as _ctx, LOG_DIR as D  # where the test runs: target.py (ARPG_TARGET=local|staging|live)
log=open(D+"after_tests.jsonl","a")
def _red(o):  # no tokens or passwords in the log
    if isinstance(o,dict): return {k:("(hidden)" if k in ("token","password","api_key") else _red(v)) for k,v in o.items()}
    if isinstance(o,list): return [_red(x) for x in o]
    return o
# A suite tests the server's mechanics without the turn schedule unless it tests the schedule itself: a new campaign uses the
# schedule from creation (outside a fight a game action is then saved for the GM, not applied), so call() has the GM turn it off
# right after the campaign is made. A schedule suite sets ah.KEEP_SCHEDULE = True before it makes its campaigns.
KEEP_SCHEDULE=False
# Starting a campaign waits on the GM and every player saying ready (POST /api/campaigns/{id}/ready). A suite that tests
# mechanics, not the start, has its players and GM say ready right before the start: call() remembers who made each campaign
# and who joined it with which character, and before POST /api/campaigns/{id}/start sends their ready; a player whose character
# can't be ready yet (an open setup step the suite leaves on purpose) is named in start_without. A suite that tests the start
# itself sets ah.KEEP_READINESS = True.
KEEP_READINESS=False
_campaign_gm={}       # campaign id -> the GM's token
_campaign_players={}  # campaign id -> {character id: the player's token}
_start_re=re.compile(r"^/api/campaigns/(\d+)/start(\?.*)?$")
_join_re=re.compile(r"^/api/campaigns/(\d+)/join(\?.*)?$")
def _ready_before_start(cid,b):
    without=[]
    for char,ptok in _campaign_players.get(cid,{}).items():
        c,_=call("POST","/api/campaigns/%d/ready"%cid,{"ready":True},ptok,"ready before the start (ah.KEEP_READINESS)")
        if c!=200: without.append(char)
    gtok=_campaign_gm.get(cid)
    if gtok: call("POST","/api/campaigns/%d/ready"%cid,{"ready":True},gtok,"GM ready before the start (ah.KEEP_READINESS)")
    if not without: return b
    b=dict(b or {})
    b["start_without"]=sorted(set((b.get("start_without") or [])+without))
    return b
def call(m,p,b=None,tok=None,label="",raw=None,ctype="application/json"):
    if m=="POST" and raw is None and not KEEP_READINESS:
        st=_start_re.match(p)
        if st and int(st.group(1)) in _campaign_gm: b=_ready_before_start(int(st.group(1)),b)
    h={"Content-Type":ctype,"User-Agent":"agentrpg-test"}
    if tok: h["Authorization"]="Bearer "+tok
    data=raw if raw is not None else (json.dumps(b).encode() if b is not None else None)
    r=urllib.request.Request(BASE+p,data=data,method=m,headers=h)
    try:
        with urllib.request.urlopen(r,context=_ctx,timeout=90) as x: code,t=x.status,x.read().decode()
    except urllib.error.HTTPError as e: code,t=e.code,e.read().decode()
    try: js=json.loads(t)
    except Exception: js={"_raw":t[:300]}
    rb=_red(b)
    if isinstance(b,dict) and "image_base64" in b: rb={"image_base64":"(%d chars)"%len(b["image_base64"])}
    log.write(json.dumps({"label":label,"method":m,"path":p,"status":code,"request":rb if raw is None else "(raw %d bytes)"%len(raw),"response":_red(js)})+"\n"); log.flush()
    _remember(m,p,js,tok)
    if m=="POST" and code==200 and tok and isinstance(js,dict):
        if p=="/api/campaigns" and js.get("campaign_id"): _campaign_gm[js["campaign_id"]]=tok
        jn=_join_re.match(p)
        if jn and isinstance(b,dict) and b.get("character_id"):
            ps=_campaign_players.setdefault(int(jn.group(1)),{})
            for k in [k for k,v in ps.items() if v==tok]: del ps[k] # joining with another character takes the old one's seat
            ps[int(b["character_id"])]=tok
    if m=="POST" and p=="/api/campaigns" and code==200 and isinstance(js,dict) and js.get("campaign_id") and tok and not KEEP_SCHEDULE:
        call("PATCH","/api/campaigns/%d/schedule"%js["campaign_id"],{"state":"off"},tok,"schedule off (ah.KEEP_SCHEDULE)")
    return code,js
# With ARPG_SECRETS set (a directory outside the repository), every character and campaign a test makes is written there
# with the token that owns it (created.jsonl, mode 600, never printed), so a clean-up script can delete them afterwards.
def _remember(m,p,js,tok):
    S=os.environ.get("ARPG_SECRETS")
    if not S or m!="POST" or not isinstance(js,dict) or not tok: return
    rec=None
    if p=="/api/campaigns" and js.get("campaign_id"): rec={"campaign_id":js["campaign_id"],"token":tok}
    elif (p=="/api/characters" or (p.startswith("/api/character-builder/") and p.endswith("/finish"))) and js.get("character_id"): rec={"character_id":js["character_id"],"token":tok}
    if rec:
        os.makedirs(S,exist_ok=True); f=S+"/created.jsonl"
        with open(f,"a") as o: o.write(json.dumps(rec)+"\n")
        os.chmod(f,0o600)
def register(prefix):
    return call("POST","/api/register",{"name":"%s%d"%(prefix,random.randint(10000,99999)),"password":"x" if LOCAL else "t-%d"%random.randint(10**9,10**10)})[1]
def png(w,h):
    raw=b"".join(b"\x00"+bytes([90,120,160])*w for _ in range(h))
    def ch(t,d):
        c=struct.pack(">I",len(d))+t+d; return c+struct.pack(">I",zlib.crc32(t+d)&0xffffffff)
    return b"\x89PNG\r\n\x1a\n"+ch(b"IHDR",struct.pack(">IIBBBBB",w,h,8,2,0,0,0))+ch(b"IDAT",zlib.compress(raw,9))+ch(b"IEND",b"")
_seq=[0]
def unique_suffix():
    # names are unique on a server, and a local database keeps every run's characters: time-based, so two runs never collide
    import time
    _seq[0]+=1
    return "%d%02d"%(int(time.time()*10)%10**8,_seq[0]%100)
def build(tok,cls,bg,sp,label,extra=None):
    c,d=call("POST","/api/character-builder",{"name":"%s %s %s"%(label,cls.title(),unique_suffix())},tok,label)
    P="/api/character-builder/%d"%d["draft_id"]
    for b in ({"class":cls},{"background":bg},{"species":sp}): call("POST",P,b,tok,label)
    if extra: call("POST",P,extra,tok,label)
    for i in range(20):
        c,v=call("POST",P,{"use_suggestion":True},tok,label)
        if "Every step is done" in (v.get("next") or ""): break
        k=(v.get("current_step") or {}).get("key")
        if k=="details": call("POST",P,{"alignment":"Neutral Good"},tok,label)
        if k=="backstory": call("POST",P,{"backstory":"One.\n\nTwo.\n\nThree."},tok,label)
    c,f=call("POST",P+"/finish",{},tok,label)
    return f.get("character_id"),f
