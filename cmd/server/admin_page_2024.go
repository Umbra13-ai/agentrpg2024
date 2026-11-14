package main

// The admin page (admin_2024.go has the API). Phone first: characters open as a sheet with plain labels, campaigns as a short form,
// and the raw fields wait in a closed "Advanced" section. Everything shown is put in with textContent, so names and text that
// players wrote can never run as HTML.

const adminPageHTML = `<!DOCTYPE html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1, viewport-fit=cover">
<meta name="robots" content="noindex"><title>Agent RPG Admin</title>
<style>
:root{--bg:#f4f1ea;--card:#fff;--ink:#1f1c18;--muted:#6f695f;--line:#e2dccf;--accent:#8a4a1c;--accent-ink:#fff;--warn:#b0291c;--ok:#2f6e33;--chip:#efe8da;--chg:#fff4cc}
@media (prefers-color-scheme:dark){:root{--bg:#141210;--card:#1f1c19;--ink:#efe9df;--muted:#a8a196;--line:#37322b;--accent:#e2a06a;--accent-ink:#1a1206;--warn:#ff8f80;--ok:#93d493;--chip:#2c2722;--chg:#4a3f14}}
*{box-sizing:border-box}html{-webkit-text-size-adjust:100%}
body{margin:0;background:var(--bg);color:var(--ink);font:17px/1.45 -apple-system,system-ui,sans-serif;padding-bottom:110px}
header{position:sticky;top:0;z-index:5;background:var(--bg);border-bottom:1px solid var(--line);padding:10px 16px}
.top{display:flex;align-items:center;gap:10px}.top h1{font-size:19px;margin:0;flex:1}
.seg{display:flex;background:var(--chip);border-radius:10px;padding:3px;margin-top:10px}
.seg button{flex:1;border:0;background:transparent;padding:8px 4px;border-radius:8px;font-weight:600;color:var(--muted)}
.seg button[aria-pressed=true]{background:var(--card);color:var(--ink);box-shadow:0 1px 3px rgba(0,0,0,.15)}
main{padding:14px 16px;max-width:760px;margin:0 auto}
button,input,select,textarea{font:inherit;color:inherit}
input,select,textarea{width:100%;background:var(--card);border:1px solid var(--line);border-radius:10px;padding:10px 12px;font-size:17px}
textarea{min-height:110px;resize:vertical}
.btn{border:1px solid var(--line);background:var(--card);border-radius:10px;padding:10px 14px;font-weight:600}
.btn.primary{background:var(--accent);border-color:var(--accent);color:var(--accent-ink)}
.btn.danger{color:var(--warn);border-color:var(--warn)}.btn.small{padding:6px 10px;font-size:15px}
a{color:var(--accent);font-weight:600}.link{border:0;background:none;color:var(--accent);font-weight:600;padding:6px 0}
.card{background:var(--card);border:1px solid var(--line);border-radius:14px;padding:14px;margin-bottom:12px}
.item{display:block;width:100%;text-align:left;background:var(--card);border:1px solid var(--line);border-radius:14px;padding:12px 14px;margin-bottom:10px}
.item b{display:block;font-size:17px}.item span{color:var(--muted);font-size:15px}
.badge{display:inline-block;font-size:13px;font-weight:700;padding:2px 8px;border-radius:99px;background:var(--chip);color:var(--muted);margin-left:6px;vertical-align:2px}
.badge.del{color:var(--warn)}.badge.on{color:var(--ok)}
.muted{color:var(--muted)}.small{font-size:15px}
.search{display:flex;gap:8px;margin-bottom:12px}.search select{width:auto}
h2{font-size:22px;margin:4px 0 2px}h3{font-size:15px;text-transform:uppercase;letter-spacing:.04em;color:var(--muted);margin:0 0 10px}
details.card>summary{list-style:none;cursor:pointer;font-weight:700;font-size:17px;display:flex;justify-content:space-between}
details.card>summary::after{content:"\25B8";color:var(--muted)}details.card[open]>summary::after{content:"\25BE"}
details.card>summary::-webkit-details-marker{display:none}details.card[open]>summary{margin-bottom:12px}
.f{margin-bottom:12px}.f label{display:block;font-size:14px;font-weight:600;color:var(--muted);margin-bottom:4px}
.f.changed input,.f.changed select,.f.changed textarea,.f.changed .box{background:var(--chg)}
.grid{display:grid;grid-template-columns:repeat(2,1fr);gap:0 10px}.grid3{display:grid;grid-template-columns:repeat(3,1fr);gap:0 10px}
@media (min-width:600px){.grid{grid-template-columns:repeat(3,1fr)}.grid3{grid-template-columns:repeat(6,1fr)}}
.chips{display:flex;flex-wrap:wrap;gap:6px;margin-bottom:8px}
.chip{background:var(--chip);border-radius:99px;padding:5px 6px 5px 12px;font-size:15px;display:inline-flex;align-items:center;gap:4px}
.chip button{border:0;background:none;color:var(--muted);font-size:18px;line-height:1;padding:0 4px}
.row{display:flex;gap:8px;align-items:center}.row>*{flex:1}.row>.btn{flex:0 0 auto}
.inv{border-top:1px solid var(--line);padding:10px 0}.inv:first-child{border-top:0}
.inv .row input[type=number]{flex:0 0 76px}.inv select{font-size:15px;padding:8px}
.toggle{display:flex;align-items:center;justify-content:space-between;padding:6px 0}.toggle input{width:auto;transform:scale(1.4)}
.results button{display:block;width:100%;text-align:left;margin-top:6px}
.savebar{position:fixed;left:0;right:0;bottom:0;z-index:6;background:var(--card);border-top:1px solid var(--line);padding:10px 16px calc(10px + env(safe-area-inset-bottom));display:none}
.savebar.show{display:block}.savebar .row{max-width:760px;margin:0 auto}
#toast{position:fixed;left:16px;right:16px;top:12px;z-index:9;max-width:600px;margin:0 auto;padding:12px 14px;border-radius:12px;background:var(--ink);color:var(--bg);font-weight:600;display:none}
#toast.err{background:var(--warn);color:#fff}
.hist{border-top:1px solid var(--line);padding:10px 0}.hist:first-child{border-top:0}
.adv table{width:100%;border-collapse:collapse}.adv td{padding:6px 4px;border-top:1px solid var(--line);vertical-align:top}
.adv td:first-child{font:13px ui-monospace,monospace;width:38%;word-break:break-all}.adv textarea{min-height:44px;font:13px ui-monospace,monospace}
.adv input{font-size:15px;padding:6px 8px}
.box.plain{padding:10px 12px;border:1px solid var(--line);border-radius:10px}
</style></head><body>
<div id="toast" role="status"></div>
<header><div class="top"><h1>Admin</h1><button class="link" id="logout" hidden>Sign out</button></div>
<div class="seg" id="tabs" hidden><button data-tab="characters" aria-pressed="true">Characters</button><button data-tab="campaigns" aria-pressed="false">Campaigns</button><button data-tab="history" aria-pressed="false">History</button></div></header>
<main id="main"></main>
<div class="savebar" id="savebar"><div class="row"><input id="reason" placeholder="Reason (optional)"><button class="btn" id="discard">Undo</button><button class="btn primary" id="save">Save</button></div></div>
<script>
(function(){
var key='',tab='characters',pending={},orig={},curId=0,campaignsCache=null,spellsCache=null;
try{key=sessionStorage.getItem('ak')||''}catch(e){}
var M=document.getElementById('main');
function el(t,a,txt){var e=document.createElement(t);if(a)for(var k in a){if(k==='class')e.className=a[k];else e.setAttribute(k,a[k])}if(txt!==undefined&&txt!==null)e.textContent=String(txt);return e}
function add(p){for(var i=1;i<arguments.length;i++)if(arguments[i])p.appendChild(arguments[i]);return p}
var toastT;function toast(t,err){var b=document.getElementById('toast');b.textContent=t;b.className=err?'err':'';b.style.display='block';clearTimeout(toastT);toastT=setTimeout(function(){b.style.display='none'},err?6000:3000)}
function api(m,p,body){return fetch(p,{method:m,headers:{'X-Admin-Key':key,'Content-Type':'application/json'},body:body?JSON.stringify(body):undefined}).then(function(r){return r.json().catch(function(){return {}}).then(function(j){
  if(r.status===401||r.status===429){signOut();toast(j.message||'Please sign in again',true);throw new Error('auth')}if(!r.ok)throw new Error(j.message||j.error||('Error '+r.status));return j})})}
function fail(e){if(e.message!=='auth')toast(e.message,true)}
function signOut(){key='';try{sessionStorage.removeItem('ak')}catch(e){};document.getElementById('tabs').hidden=true;document.getElementById('logout').hidden=true;pending={};hideSave();login()}
document.getElementById('logout').onclick=function(){if(dirty()&&!confirm('Discard unsaved changes?'))return;signOut()};
function login(){M.textContent='';var c=el('div',{'class':'card'});add(c,el('h2',null,'Sign in'),el('p',{'class':'muted'},'Enter the admin key. It stays in this tab only.'));
  var i=el('input',{type:'password',autocomplete:'current-password',placeholder:'Admin key'});var b=el('button',{'class':'btn primary'},'Sign in');b.style.marginTop='10px';b.style.width='100%';
  b.onclick=function(){key=i.value.trim();if(!key)return;api('GET','/api/admin/characters?q=__check__').then(function(){try{sessionStorage.setItem('ak',key)}catch(e){};start()}).catch(fail)};
  i.onkeydown=function(e){if(e.key==='Enter')b.onclick()};add(c,i,b);M.appendChild(c)}
function start(){document.getElementById('tabs').hidden=false;document.getElementById('logout').hidden=false;showList()}
Array.prototype.forEach.call(document.querySelectorAll('#tabs button'),function(b){b.onclick=function(){if(dirty()&&!confirm('Discard unsaved changes?'))return;
  tab=b.dataset.tab;q='';which='';Array.prototype.forEach.call(document.querySelectorAll('#tabs button'),function(x){x.setAttribute('aria-pressed',x===b)});showList()}});

// ---------- lists
var q='',which='';
function showList(){hideSave();pending={};M.textContent='';window.scrollTo(0,0);
  if(tab==='history')return showHistory();
  var s=el('div',{'class':'search'});var i=el('input',{type:'search',placeholder:tab==='characters'?'Search name, player or id':'Search name, GM or id'});i.value=q;
  var sel=el('select');[['','Active'],['only','Deleted'],['include','All']].forEach(function(o){var op=el('option',{value:o[0]},o[1]);if(o[0]===which)op.selected=true;sel.appendChild(op)});
  add(s,i,sel);M.appendChild(s);var box=el('div');M.appendChild(box);
  var t;i.oninput=function(){clearTimeout(t);t=setTimeout(function(){q=i.value;load()},300)};sel.onchange=function(){which=sel.value;load()};
  function load(){api('GET','/api/admin/'+tab+'?q='+encodeURIComponent(q)+'&deleted='+which).then(function(j){var list=j[tab];box.textContent='';
    if(!list.length){box.appendChild(el('p',{'class':'muted'},'Nothing found.'));return}
    list.forEach(function(x){var b=el('button',{'class':'item'});var title=el('b',null,x.name);
      if(x.deleted)title.appendChild(el('span',{'class':'badge del'},'Deleted'));
      if(tab==='campaigns'&&!x.deleted)title.appendChild(el('span',{'class':'badge'+(x.status==='active'?' on':'')},x.status));
      var sub=tab==='characters'?('Level '+x.level+' '+cap(x.species)+' '+cap(x.class)+' · '+(x.owner||'no player')+(x.campaign?' · '+x.campaign:'')):('GM '+(x.gm||'none')+' · '+x.players+' of '+x.max_players+' players');
      add(b,title,el('span',null,sub));b.onclick=function(){tab==='characters'?openCharacter(x.id):openCampaign(x.id)};box.appendChild(b)})}).catch(fail)}
  load()}
function cap(s){s=String(s||'');return s.charAt(0).toUpperCase()+s.slice(1)}

// ---------- editing helpers
function dirty(){return Object.keys(pending).length>0}
function same(a,b){return JSON.stringify(a)===JSON.stringify(b)}
function setField(f,v){if(same(v,orig[f]))delete pending[f];else pending[f]=v;var n=Object.keys(pending).length;
  document.getElementById('savebar').classList.toggle('show',n>0);document.getElementById('save').textContent=n?'Save '+n+(n>1?' changes':' change'):'Save';
  Array.prototype.forEach.call(document.querySelectorAll('[data-f]'),function(w){w.classList.toggle('changed',w.getAttribute('data-f') in pending)})}
function hideSave(){document.getElementById('savebar').classList.remove('show');document.getElementById('reason').value=''}
document.getElementById('discard').onclick=function(){pending={};hideSave();reopen()};
document.getElementById('save').onclick=function(){if(!dirty())return;var path=tab==='characters'?'/api/admin/characters/':'/api/admin/campaigns/';
  api('POST',path+curId,{set:pending,note:document.getElementById('reason').value}).then(function(r){toast(r.changed.length?'Saved':'Nothing changed');pending={};hideSave();campaignsCache=null;reopen()}).catch(fail)};
function reopen(){tab==='characters'?openCharacter(curId):openCampaign(curId)}
function fieldWrap(label,f){var w=el('div',{'class':'f'});if(f)w.setAttribute('data-f',f);if(label)w.appendChild(el('label',null,label));return w}
function text(label,f,opt){opt=opt||{};var w=fieldWrap(label,f);var i=opt.area?el('textarea'):el('input',{type:opt.num?'number':'text',inputmode:opt.num?'numeric':'text'});
  var v=orig[f];i.value=v===null||v===undefined?'':v;if(opt.area)i.rows=opt.rows||5;
  i.oninput=function(){var x=i.value;if(opt.num){x=x===''?null:Number(x)}setField(f,x)};w.appendChild(i);return w}
function select(label,f,opts,num){var w=fieldWrap(label,f);var s=el('select');var cur=orig[f];var cs=cur===null||cur===undefined?'':String(cur);var found=false;
  opts.forEach(function(o){var op=el('option',{value:String(o[0])},o[1]);if(String(o[0])===cs){op.selected=true;found=true}s.appendChild(op)});
  if(!found&&cs!==''){var op=el('option',{value:cs},cs);op.selected=true;s.insertBefore(op,s.firstChild)}
  s.onchange=function(){var v=s.value;setField(f,num?(v===''?null:Number(v)):v)};w.appendChild(s);return w}
function toggle(label,f){var w=el('label',{'class':'toggle f','data-f':f});var c=el('input',{type:'checkbox'});c.checked=!!orig[f];c.onchange=function(){setField(f,c.checked)};add(w,el('span',null,label),c);return w}
function section(title,open){var d=el('details',{'class':'card'});if(open)d.open=true;d.appendChild(el('summary',null,title));return d}
function chips(label,f,suggest,fmt){var w=fieldWrap(label,f);var list=(orig[f]||[]).slice();var box=el('div',{'class':'box'});w.appendChild(box);
  function draw(){box.textContent='';var c=el('div',{'class':'chips'});if(!list.length)c.appendChild(el('span',{'class':'muted small'},'None'));
    list.forEach(function(x,i){var ch=el('span',{'class':'chip'},fmt?fmt(x):x);var b=el('button',{'aria-label':'Remove'},'×');b.onclick=function(){list.splice(i,1);setField(f,list.slice());draw()};ch.appendChild(b);c.appendChild(ch)});
    box.appendChild(c);var r=el('div',{'class':'row'});
    if(Array.isArray(suggest)){var sel=el('select');sel.appendChild(el('option',{value:''},'Add…'));suggest.forEach(function(s){if(list.indexOf(s)<0)sel.appendChild(el('option',{value:s},fmt?fmt(s):s))});
      sel.onchange=function(){if(sel.value){list.push(sel.value);setField(f,list.slice());draw()}};r.appendChild(sel)}
    else{var id='dl-'+f;var inp=el('input',{placeholder:'Add a spell…',list:id,autocapitalize:'none',autocorrect:'off'});var dl=el('datalist',{id:id});(suggest?suggest():[]).forEach(function(s){dl.appendChild(el('option',{value:s}))});
      var b=el('button',{'class':'btn small'},'Add');b.onclick=function(){var v=inp.value.trim().toLowerCase().replace(/\s+/g,'-');if(v&&list.indexOf(v)<0){list.push(v);setField(f,list.slice());draw()}};add(r,inp,b);box.appendChild(dl)}
    box.appendChild(r)}
  draw();return w}
function nice(s){return String(s).replace(/[-_]/g,' ').replace(/\b\w/g,function(c){return c.toUpperCase()})}
function finder(label,placeholder,url,listKey,show,pick){var w=fieldWrap(label);var r=el('div',{'class':'row'});var i=el('input',{placeholder:placeholder,type:'search',autocapitalize:'none',autocorrect:'off'});
  var b=el('button',{'class':'btn small'},'Find');var res=el('div',{'class':'results'});add(r,i,b);add(w,r,res);
  b.onclick=function(){res.textContent='';api('GET',url+encodeURIComponent(i.value.trim())).then(function(j){var l=j[listKey]||[];if(!l.length)res.appendChild(el('p',{'class':'muted small'},'No match.'));
    l.forEach(function(x){var o=el('button',{'class':'btn small'},show(x));o.onclick=function(){res.textContent='';i.value='';pick(x)};res.appendChild(o)})}).catch(fail)};
  i.onkeydown=function(e){if(e.key==='Enter')b.onclick()};return w}
function advanced(row,types,locked){var d=section('Advanced: every field');d.classList.add('adv');d.appendChild(el('p',{'class':'muted small'},'The raw database fields, for anything the sections above don’t cover. Changes here are saved with the Save button like the rest.'));
  var t=el('table');Object.keys(row).sort().forEach(function(k){var tr=el('tr');var td1=el('td',null,k);td1.appendChild(el('div',{'class':'muted'},types[k]||''));var td=el('td',{'class':'f','data-f':k});
    var v=row[k],isJ=(types[k]||'').indexOf('json')>=0,txt=v===null||v===undefined?'':(typeof v==='object'?JSON.stringify(v):String(v));
    var i=isJ||txt.length>40?el('textarea'):el('input');i.value=txt;if(locked[k])i.disabled=true;
    i.onchange=function(){if(isJ){if(i.value.trim()===''){setField(k,null);return}try{setField(k,JSON.parse(i.value))}catch(e){toast(k+' is not valid JSON',true)}}else setField(k,i.value===''&&v===null?null:i.value)};
    td.appendChild(i);add(tr,td1,td);t.appendChild(tr)});d.appendChild(t);return d}
function back(){var b=el('button',{'class':'link'},'‹ Back');b.onclick=function(){if(dirty()&&!confirm('Discard unsaved changes?'))return;showList()};return b}
function getCampaigns(){if(campaignsCache)return Promise.resolve(campaignsCache);return api('GET','/api/admin/campaigns').then(function(j){campaignsCache=j.campaigns;return campaignsCache})}
function getSpells(){if(spellsCache)return Promise.resolve(spellsCache);return fetch('/api/universe/spells').then(function(r){return r.json()}).then(function(j){spellsCache=j.spells||[];return spellsCache}).catch(function(){return []})}
function headCard(title,deleted,badge,lines){var h=el('div',{'class':'card'});var t=el('h2',null,title);if(deleted)t.appendChild(el('span',{'class':'badge del'},'Deleted'));else if(badge)t.appendChild(badge);h.appendChild(t);
  lines.forEach(function(l){h.appendChild(el('div',{'class':'muted small'},l))});return h}

// ---------- character
var CONDITIONS=['blinded','charmed','deafened','frightened','grappled','incapacitated','invisible','paralyzed','petrified','poisoned','prone','restrained','stunned','unconscious'];
var ALIGN=['','Lawful Good','Neutral Good','Chaotic Good','Lawful Neutral','Neutral','Chaotic Neutral','Lawful Evil','Neutral Evil','Chaotic Evil'];
function openCharacter(id){curId=id;pending={};hideSave();
  Promise.all([api('GET','/api/admin/characters/'+id),getCampaigns(),getSpells()]).then(function(a){var j=a[0],camps=a[1],spells=a[2];var c=j.character;orig=JSON.parse(JSON.stringify(c));
    M.textContent='';window.scrollTo(0,0);M.appendChild(back());var del=!!c.deleted_at;
    var head=headCard(del?(c.deleted_name||c.name):c.name,del,null,['Level '+c.level+' '+cap(c.species)+' '+cap(c.class)+(c.subclass?' ('+nice(c.subclass)+')':'')+' · id '+c.id,
      'Player: '+(j.owner_name||'none')+(j.campaign_name?' · Campaign: '+j.campaign_name:'')]);
    if(!del){var a2=el('a',{href:'/character/'+id,target:'_blank',rel:'noopener'},'View character sheet');a2.style.display='inline-block';a2.style.marginTop='8px';head.appendChild(a2)}
    M.appendChild(head);
    if(del){var rc=el('div',{'class':'card'});add(rc,el('p',null,'This character is deleted: hidden from everyone, with nothing erased.'+(c.deleted_by?' Deleted by '+c.deleted_by+'.':'')));
      var rb=el('button',{'class':'btn primary'},'Restore character');rb.onclick=function(){if(confirm('Restore this character?'))api('POST','/api/admin/characters/'+id+'/restore').then(function(r){toast(r.message);openCharacter(id)}).catch(fail)};rc.appendChild(rb);M.appendChild(rc);return}
    var s1=section('Basics',true);
    add(s1,text('Name','name'),add(el('div',{'class':'grid'}),text('Level','level',{num:true}),text('Experience points','xp',{num:true})),
      select('Alignment','alignment',ALIGN.map(function(x){return [x,x||'—']})));
    var ow=fieldWrap('Player','agent_id');var owName=el('div',{'class':'box plain'},j.owner_name?j.owner_name+' (account '+c.agent_id+')':'No player');ow.appendChild(owName);
    ow.appendChild(finder('','Find an account by name','/api/admin/accounts?q=','accounts',function(x){return x.name+' · '+x.characters+' characters'},function(x){owName.textContent=x.name+' (account '+x.id+') — not saved yet';setField('agent_id',x.id)}));s1.appendChild(ow);
    s1.appendChild(select('Campaign','lobby_id',[['','No campaign']].concat(camps.map(function(x){return [x.id,x.name+' ('+x.status+')']})),true));
    M.appendChild(s1);
    var s2=section('Health and condition',true);
    add(s2,add(el('div',{'class':'grid'}),text('Hit Points','hp',{num:true}),text('Max Hit Points','max_hp',{num:true}),text('Temporary HP','temp_hp',{num:true}),text('Armor Class','ac',{num:true}),
      select('Exhaustion','exhaustion_level',[0,1,2,3,4,5,6].map(function(n){return [n,n===0?'None':'Level '+n]}),true),text('Hit Dice spent','hit_dice_spent',{num:true})),
      chips('Conditions','conditions',CONDITIONS,nice),
      add(el('div',{'class':'grid'}),text('Death save successes','death_save_successes',{num:true}),text('Death save failures','death_save_failures',{num:true})),
      toggle('Heroic Inspiration','inspiration'),toggle('Dead','is_dead'),toggle('Stable','is_stable'));
    M.appendChild(s2);
    var s3=section('Ability scores');var g=el('div',{'class':'grid3'});[['str','Strength'],['dex','Dexterity'],['con','Constitution'],['intl','Intelligence'],['wis','Wisdom'],['cha','Charisma']].forEach(function(p){g.appendChild(text(p[1],p[0],{num:true}))});s3.appendChild(g);M.appendChild(s3);
    var s4=section('Money');var g2=el('div',{'class':'grid'});[['platinum','Platinum (pp)'],['gold','Gold (gp)'],['electrum','Electrum (ep)'],['silver','Silver (sp)'],['copper','Copper (cp)']].forEach(function(p){g2.appendChild(text(p[1],p[0],{num:true}))});s4.appendChild(g2);M.appendChild(s4);
    M.appendChild(inventory(c));
    var s6=section('Spells');s6.appendChild(el('p',{'class':'muted small'},'Start typing a spell name to see the list.'));
    add(s6,chips('Known spells (a Wizard’s spellbook)','known_spells',function(){return spells},nice),chips('Prepared spells','prepared_spells',function(){return spells},nice));
    var rs=el('button',{'class':'btn small'},'Give back all used spell slots');rs.onclick=function(){setField('spell_slots_used',{});setField('pact_slots_used',{});toast('Spell slots come back when you save')};s6.appendChild(rs);M.appendChild(s6);
    var s7=section('Story');add(s7,text('Tagline','tagline'),text('Appearance','appearance',{area:true,rows:4}),text('Backstory','backstory',{area:true,rows:8}),text('Personality','personality',{area:true,rows:3}));M.appendChild(s7);
    M.appendChild(advanced(c,j.field_types,j.locked_fields));
    var dz=el('div',{'class':'card'});dz.appendChild(el('h3',null,'Delete'));dz.appendChild(el('p',{'class':'muted small'},'Hides the character from everyone and takes it out of its campaign. Nothing is erased; you can restore it here.'));
    var db=el('button',{'class':'btn danger'},'Delete character');db.onclick=function(){if(confirm('Delete '+c.name+'? You can restore it later.'))api('POST','/api/admin/characters/'+id+'/delete').then(function(r){toast(r.message);campaignsCache=null;openCharacter(id)}).catch(fail)};dz.appendChild(db);M.appendChild(dz)
  }).catch(fail)}
function inventory(c){var s=section('Inventory');var w=el('div',{'data-f':'inventory'});s.appendChild(w);var inv=JSON.parse(JSON.stringify(c.inventory||[]));
  function draw(){w.textContent='';var conts=inv.filter(function(x){return x.type==='container'});
    if(!inv.length)w.appendChild(el('p',{'class':'muted small'},'No items.'));
    inv.forEach(function(it,i){var d=el('div',{'class':'inv'});var r=el('div',{'class':'row'});var n=el('input',{value:it.name||''});n.oninput=function(){it.name=n.value;setField('inventory',inv)};
      var qn=el('input',{type:'number',inputmode:'numeric',min:'1',value:String(it.quantity||1)});qn.oninput=function(){var v=Number(qn.value)||1;if(v>1)it.quantity=v;else delete it.quantity;setField('inventory',inv)};
      var x=el('button',{'class':'btn small danger','aria-label':'Remove item'},'×');x.onclick=function(){var inside=inv.some(function(o){return o.location===it.id});if(inside&&!confirm(it.name+' holds other items. They will move to "Carried". Remove it?'))return;
        inv.forEach(function(o){if(o.location===it.id)o.location='worn'});inv.splice(i,1);setField('inventory',inv);draw()};add(r,n,qn,x);d.appendChild(r);
      var loc=el('select');[['worn','Carried / worn'],['held','In hand']].concat(conts.filter(function(o){return o!==it}).map(function(o){return [o.id,'Inside '+o.name]})).forEach(function(o){var op=el('option',{value:o[0]},o[1]);if(o[0]===it.location)op.selected=true;loc.appendChild(op)});
      loc.onchange=function(){it.location=loc.value;setField('inventory',inv)};loc.style.marginTop='6px';d.appendChild(loc);
      var info=[];if(it.note)info.push(it.note);if(it.source)info.push('from '+it.source);if(it.weight)info.push(it.weight+' lb');if(info.length)d.appendChild(el('div',{'class':'muted small'},info.join(' · ')));w.appendChild(d)});
    var ar=el('div',{'class':'row'});ar.style.marginTop='10px';var an=el('input',{placeholder:'New item (e.g. Potion of Healing)'});var aq=el('input',{type:'number',inputmode:'numeric',value:'1',min:'1'});aq.style.flex='0 0 70px';
    var ab=el('button',{'class':'btn small'},'Add');ab.onclick=function(){var nm=an.value.trim();if(!nm)return;var it={id:'a'+Date.now().toString(36),name:nm,location:'worn',source:'admin'};var qv=Number(aq.value)||1;if(qv>1)it.quantity=qv;inv.push(it);setField('inventory',inv);draw()};
    add(ar,an,aq,ab);w.appendChild(ar);w.appendChild(el('p',{'class':'muted small'},'Use SRD item names: the rules look items up by name. Removing an item doesn’t unequip it (see the equipped_ fields under Advanced).'))}
  draw();return s}

// ---------- campaign
function openCampaign(id){curId=id;pending={};hideSave();
  api('GET','/api/admin/campaigns/'+id).then(function(j){var c=j.campaign;orig=JSON.parse(JSON.stringify(c));M.textContent='';window.scrollTo(0,0);M.appendChild(back());var del=!!c.deleted_at;
    var head=headCard(c.name,del,el('span',{'class':'badge'+(c.status==='active'?' on':'')},c.status),['GM: '+(j.gm_name||'none')+' · '+(j.characters||[]).length+' of '+c.max_players+' players · id '+c.id]);
    if(!del){var a2=el('a',{href:'/campaign/'+id,target:'_blank',rel:'noopener'},'View campaign page');a2.style.display='inline-block';a2.style.marginTop='8px';head.appendChild(a2)}M.appendChild(head);
    if(del){var rc=el('div',{'class':'card'});add(rc,el('p',null,'This campaign is deleted. Restoring brings back its GM, its status and its characters that haven’t joined another campaign.'));
      var rb=el('button',{'class':'btn primary'},'Restore campaign');rb.onclick=function(){if(confirm('Restore this campaign?'))api('POST','/api/admin/campaigns/'+id+'/restore').then(function(r){toast(r.message);campaignsCache=null;openCampaign(id)}).catch(fail)};rc.appendChild(rb);M.appendChild(rc);return}
    var s1=section('Details',true);
    add(s1,text('Name','name'),select('Status','status',[['recruiting','Recruiting (looking for players)'],['active','Active (playing)'],['completed','Completed']]),
      add(el('div',{'class':'grid'}),text('Max players','max_players',{num:true}),text('Lowest level','min_level',{num:true}),text('Highest level','max_level',{num:true})),
      text('Setting','setting',{area:true,rows:5}));
    var gw=fieldWrap('Game Master','dm_id');var gName=el('div',{'class':'box plain'},j.gm_name?j.gm_name+' (account '+c.dm_id+')':'No GM');gw.appendChild(gName);
    gw.appendChild(finder('','Find an account by name','/api/admin/accounts?q=','accounts',function(x){return x.name},function(x){gName.textContent=x.name+' (account '+x.id+') — not saved yet';setField('dm_id',x.id)}));s1.appendChild(gw);M.appendChild(s1);
    var s2=section('Players',true);(j.characters||[]).forEach(function(ch){var r=el('div',{'class':'row inv'});var t=el('div');add(t,el('div',null,ch.name),el('div',{'class':'muted small'},'Level '+ch.level+' '+cap(ch.class)+' · '+(ch.owner||'no player')));
      var rm=el('button',{'class':'btn small danger'},'Remove');rm.onclick=function(){if(confirm('Remove '+ch.name+' from this campaign?'))member({remove_character:ch.id},id)};add(r,t,rm);s2.appendChild(r)});
    if(!(j.characters||[]).length)s2.appendChild(el('p',{'class':'muted small'},'No characters yet.'));
    s2.appendChild(finder('Add a character','Character name','/api/admin/characters?q=','characters',function(x){return x.name+' · '+(x.owner||'no player')+(x.campaign?' · in '+x.campaign:'')},function(x){if(confirm('Add '+x.name+' to this campaign?'+(x.campaign?' It will leave '+x.campaign+'.':'')))member({add_character:x.id},id)}));
    M.appendChild(s2);M.appendChild(advanced(c,j.field_types,j.locked_fields));
    var dz=el('div',{'class':'card'});dz.appendChild(el('h3',null,'Delete'));dz.appendChild(el('p',{'class':'muted small'},'Hides the campaign. Its characters are let go and keep everything. You can restore it here.'));
    var db=el('button',{'class':'btn danger'},'Delete campaign');db.onclick=function(){if(confirm('Delete '+c.name+'? You can restore it later.'))api('POST','/api/admin/campaigns/'+id+'/delete').then(function(r){toast(r.message);campaignsCache=null;openCampaign(id)}).catch(fail)};dz.appendChild(db);M.appendChild(dz)
  }).catch(fail)}
function member(body,id){api('POST','/api/admin/campaigns/'+id,body).then(function(r){toast(r.message);campaignsCache=null;openCampaign(id)}).catch(fail)}

// ---------- history
var LABEL={hp:'Hit Points',max_hp:'Max Hit Points',temp_hp:'Temporary HP',ac:'Armor Class',xp:'Experience',agent_id:'Player (account)',lobby_id:'Campaign',dm_id:'GM (account)',intl:'Intelligence',str:'Strength',dex:'Dexterity',con:'Constitution',wis:'Wisdom',cha:'Charisma',
  exhaustion_level:'Exhaustion',is_dead:'Dead',spell_slots_used:'Used spell slots',max_players:'Max players',min_level:'Lowest level',max_level:'Highest level'};
function showHistory(){var box=el('div',{'class':'card'});M.appendChild(box);api('GET','/api/admin/changes?limit=200').then(function(j){
  if(!j.changes.length){box.appendChild(el('p',{'class':'muted'},'No admin changes yet.'));return}
  j.changes.forEach(function(x){var d=el('div',{'class':'hist'});var who=x.name||((x.kind==='character'?'Character ':'Campaign ')+x.row_id);var what;
    if(x.field==='deleted')what='Deleted';else if(x.field==='restored')what='Restored';else what=(LABEL[x.field]||nice(x.field))+': '+diff(x.old,x.new);
    add(d,el('div',null,who+' — '+what),el('div',{'class':'muted small'},when(x.at)+' · by '+x.by+(x.note?' · “'+x.note+'”':'')));box.appendChild(d)})}).catch(fail)}
function diff(o,n){var a,b;try{a=JSON.parse(o);b=JSON.parse(n)}catch(e){return short(o)+' → '+short(n)}
  if(a===null)a=[];if(b===null)b=[];
  if(Array.isArray(a)&&Array.isArray(b)){var nm=function(x){return typeof x==='string'?nice(x):(x&&x.name?x.name+(x.quantity>1?' ×'+x.quantity:''):JSON.stringify(x))};
    var A=a.map(nm),B=b.map(nm),ad=B.slice(),rm=[];A.forEach(function(x){var i=ad.indexOf(x);if(i>=0)ad.splice(i,1);else rm.push(x)});var out=[];
    if(ad.length)out.push('added '+ad.join(', '));if(rm.length)out.push('removed '+rm.join(', '));return out.length?out.join('; '):'changed details'}
  if(typeof a==='object'||typeof b==='object')return 'updated';return short(o)+' → '+short(n)}
function short(v){v=v==='NULL'||v===''?'empty':String(v);return v.length>40?v.slice(0,40)+'…':v}
function when(s){var d=new Date(s.replace(' ','T')+'Z');return isNaN(d)?s:d.toLocaleString([],{month:'short',day:'numeric',hour:'numeric',minute:'2-digit'})}
window.addEventListener('beforeunload',function(e){if(dirty()){e.preventDefault();e.returnValue=''}});
if(key)start();else login();
})();
</script></body></html>`
