package main

// The /admin page (admin_2024.go has the API). Everything shown is put in with textContent, so names and text that players wrote
// can never run as HTML.

const adminPageHTML = `<!DOCTYPE html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="robots" content="noindex"><title>Agent RPG Admin</title>
<style>
:root{--bg:#f6f4ef;--panel:#fff;--ink:#1d1b18;--muted:#6b665d;--line:#ddd7cc;--accent:#7a3b12;--warn:#a1261b;--ok:#2d6a2f;--chg:#fff3c4}
@media (prefers-color-scheme:dark){:root{--bg:#171512;--panel:#211f1b;--ink:#ece7de;--muted:#a59f94;--line:#3a352e;--accent:#e0a36b;--warn:#ff8a7a;--ok:#8fd18f;--chg:#4a3f14}}
*{box-sizing:border-box}body{margin:0;background:var(--bg);color:var(--ink);font:15px/1.45 system-ui,sans-serif}
header{padding:12px 16px;border-bottom:1px solid var(--line);display:flex;gap:12px;align-items:center;flex-wrap:wrap}
header h1{font-size:18px;margin:0;margin-right:auto}#nav{display:flex;gap:8px;flex-wrap:wrap}#nav[hidden]{display:none}main{padding:16px;max-width:1100px;margin:0 auto}
button,select,input,textarea{font:inherit;color:inherit;background:var(--panel);border:1px solid var(--line);border-radius:6px;padding:6px 10px}
button{cursor:pointer}button.primary{background:var(--accent);color:#fff;border-color:var(--accent)}button.danger{color:var(--warn);border-color:var(--warn)}
button.tab[aria-pressed=true]{background:var(--ink);color:var(--bg)}
.panel{background:var(--panel);border:1px solid var(--line);border-radius:8px;padding:14px;margin-bottom:14px}
table{width:100%;border-collapse:collapse}th,td{text-align:left;padding:6px 8px;border-bottom:1px solid var(--line);vertical-align:top}
tr.row{cursor:pointer}tr.row:hover{background:var(--bg)}.muted{color:var(--muted)}.del{color:var(--warn)}
.fields td:first-child{width:220px;font-family:ui-monospace,monospace;font-size:13px;word-break:break-all}
.fields input,.fields textarea{width:100%}.fields textarea{min-height:60px;font-family:ui-monospace,monospace;font-size:13px}
.changed{background:var(--chg)}.bar{display:flex;gap:8px;flex-wrap:wrap;align-items:center;margin-bottom:10px}
#msg{min-height:1.4em;margin:8px 0}#msg.err{color:var(--warn)}#msg.ok{color:var(--ok)}
.scroll{overflow-x:auto}.fields input[type=checkbox]{width:auto;margin-right:4px}.fields label{display:inline-block;margin-top:4px}@media (max-width:640px){.fields td:first-child{width:120px}}
</style></head><body>
<header><h1>Agent RPG Admin</h1><nav id="nav" hidden>
<button class="tab" data-tab="characters" aria-pressed="true">Characters</button>
<button class="tab" data-tab="campaigns" aria-pressed="false">Campaigns</button>
<button class="tab" data-tab="changes" aria-pressed="false">Change log</button>
<button id="logout">Sign out</button></nav></header>
<main>
<div id="login" class="panel" hidden>
  <p>Enter the server's <b>ADMIN_KEY</b>. It stays in this browser tab only and is sent with each request.</p>
  <div class="bar"><input id="key" type="password" autocomplete="off" placeholder="ADMIN_KEY" style="flex:1;min-width:200px"><button class="primary" id="signin">Sign in</button></div>
</div>
<div id="msg" role="status"></div>
<div id="app" hidden>
  <div id="detail" class="panel" hidden></div>
  <div id="list" class="panel">
    <div class="bar"><input id="q" placeholder="Search by name, owner or id" style="flex:1;min-width:180px">
      <select id="deleted"><option value="">Not deleted</option><option value="include">All</option><option value="only">Deleted only</option></select>
      <button id="search">Search</button></div>
    <div class="scroll"><table id="table"></table></div>
  </div>
</div>
</main>
<script>
(function(){
var tab='characters', key='';
try{key=sessionStorage.getItem('ak')||''}catch(e){}
var $=function(id){return document.getElementById(id)};
function el(tag,attrs,text){var e=document.createElement(tag);if(attrs)for(var k in attrs)e.setAttribute(k,attrs[k]);if(text!==undefined&&text!==null)e.textContent=String(text);return e}
function say(t,kind){var m=$('msg');m.textContent=t||'';m.className=kind||''}
function api(method,path,body){
  return fetch(path,{method:method,headers:{'X-Admin-Key':key,'Content-Type':'application/json'},body:body?JSON.stringify(body):undefined})
    .then(function(r){return r.json().catch(function(){return {}}).then(function(j){
      if(r.status===401||r.status===429){signOut();say(j.message||'Not signed in','err');throw new Error('auth')}
      if(!r.ok){throw new Error(j.message||j.error||('HTTP '+r.status))}return j})})}
function signOut(){key='';try{sessionStorage.removeItem('ak')}catch(e){};$('app').hidden=true;$('nav').hidden=true;$('login').hidden=false}
$('logout').onclick=function(){signOut();say('Signed out.')};
$('signin').onclick=function(){key=$('key').value.trim();api('GET','/api/admin/characters?q=__check__').then(function(){
  try{sessionStorage.setItem('ak',key)}catch(e){};$('key').value='';$('login').hidden=true;$('app').hidden=false;$('nav').hidden=false;say('');load()}).catch(function(e){if(e.message!=='auth')say(e.message,'err')})};
$('key').onkeydown=function(e){if(e.key==='Enter')$('signin').onclick()};
$('q').onkeydown=function(e){if(e.key==='Enter')load()};$('search').onclick=load;$('deleted').onchange=load;
Array.prototype.forEach.call(document.querySelectorAll('.tab'),function(b){b.onclick=function(){
  tab=b.dataset.tab;Array.prototype.forEach.call(document.querySelectorAll('.tab'),function(x){x.setAttribute('aria-pressed',x===b)});
  $('detail').hidden=true;say('');load()}});
function head(t,cols){var tr=el('tr');cols.forEach(function(c){tr.appendChild(el('th',null,c))});t.appendChild(tr)}
function load(){
  var t=$('table');t.textContent='';
  var bar=$('q').parentNode;bar.style.display=tab==='changes'?'none':'';
  if(tab==='changes'){api('GET','/api/admin/changes?limit=200').then(function(j){
    head(t,['When','What','Field','Old','New','By','Note']);
    j.changes.forEach(function(c){var tr=el('tr');[c.at,c.kind+' '+c.row_id,c.field,c.old,c.new,c.by,c.note].forEach(function(v){tr.appendChild(el('td',null,v))});t.appendChild(tr)});
    if(!j.changes.length)say('No changes yet.')}).catch(function(e){say(e.message,'err')});return}
  var path='/api/admin/'+tab+'?q='+encodeURIComponent($('q').value)+'&deleted='+$('deleted').value;
  api('GET',path).then(function(j){
    var list=j[tab];
    if(tab==='characters'){head(t,['Id','Name','Class','Level','Owner','Campaign',''])}else{head(t,['Id','Name','Status','GM','Players',''])}
    list.forEach(function(x){var tr=el('tr',{'class':'row'});
      var cells=tab==='characters'?[x.id,x.name,(x.species?x.species+' ':'')+x.class,x.level,x.owner,x.campaign||'—']:[x.id,x.name,x.status,x.gm||'—',x.players+' / '+x.max_players];
      cells.forEach(function(v){tr.appendChild(el('td',null,v))});tr.appendChild(el('td',{'class':'del'},x.deleted?'deleted':''));
      tr.onclick=function(){open(x.id)};t.appendChild(tr)});
    if(!list.length)say('Nothing found.')}).catch(function(e){if(e.message!=='auth')say(e.message,'err')})}
function kindOf(){return tab==='characters'?'character':'campaign'}
function show(v){if(v===null||v===undefined)return '';if(typeof v==='object')return JSON.stringify(v,null,1);return String(v)}
function open(id){
  var kind=kindOf();
  api('GET','/api/admin/'+tab+'/'+id).then(function(j){
    var row=j[kind],types=j.field_types,locked=j.locked_fields,d=$('detail');d.textContent='';d.hidden=false;
    var deleted=!!row.deleted_at;
    var title=el('h2',null,(kind==='character'?'Character ':'Campaign ')+id+': '+(deleted&&row.deleted_name?row.deleted_name:row.name));d.appendChild(title);
    var bar=el('div',{'class':'bar'});
    if(kind==='character'&&!deleted){var a=el('a',{href:'/character/'+id,target:'_blank',rel:'noopener'},'Open character sheet');bar.appendChild(a)}
    if(kind==='campaign'&&!deleted){var a2=el('a',{href:'/campaign/'+id,target:'_blank',rel:'noopener'},'Open campaign page');bar.appendChild(a2)}
    if(deleted){bar.appendChild(el('span',{'class':'del'},'Deleted '+row.deleted_at+(row.deleted_by?' by '+row.deleted_by:'')));
      var rb=el('button',{'class':'primary'},'Restore');rb.onclick=function(){if(confirm('Restore this '+kind+'?'))api('POST','/api/admin/'+tab+'/'+id+'/restore').then(function(r){say(r.message,'ok');open(id);load()}).catch(function(e){say(e.message,'err')})};bar.appendChild(rb)}
    else{var db=el('button',{'class':'danger'},'Delete');db.onclick=function(){
      var msg=kind==='character'?'Delete this character? It is hidden (not erased) and leaves its campaign. You can restore it.':'Delete this campaign? Its characters are let go (they keep everything). You can restore it.';
      if(confirm(msg))api('POST','/api/admin/'+tab+'/'+id+'/delete').then(function(r){say(r.message,'ok');open(id);load()}).catch(function(e){say(e.message,'err')})};bar.appendChild(db)}
    var cl=el('button',null,'Close');cl.onclick=function(){d.hidden=true};bar.appendChild(cl);d.appendChild(bar);
    if(kind==='campaign'&&!deleted){
      d.appendChild(el('h3',null,'Characters in this campaign'));var mt=el('table');head(mt,['Id','Name','Class','Level','Owner','']);
      (j.characters||[]).forEach(function(c){var tr=el('tr');[c.id,c.name,c.class,c.level,c.owner].forEach(function(v){tr.appendChild(el('td',null,v))});
        var td=el('td'),b=el('button',{'class':'danger'},'Remove');b.onclick=function(){if(confirm('Remove '+c.name+' from the campaign?'))member({remove_character:c.id},id)};td.appendChild(b);tr.appendChild(td);mt.appendChild(tr)});
      d.appendChild(mt);var ab=el('div',{'class':'bar'});var ai=el('input',{placeholder:'Character id to add',inputmode:'numeric'});var abtn=el('button',null,'Add character');
      abtn.onclick=function(){var n=parseInt(ai.value,10);if(n)member({add_character:n},id)};ab.appendChild(ai);ab.appendChild(abtn);d.appendChild(ab)}
    d.appendChild(el('h3',null,'Fields'));
    d.appendChild(el('p',{'class':'muted'},'Change any field, then Save. Only changed fields are sent; each change is logged. JSON fields must stay valid JSON. Clear a field and tick "null" to empty it.'));
    var flt=el('input',{placeholder:'Filter fields (e.g. hp, gold, spells)'});flt.style.width='100%';flt.style.marginBottom='8px';d.appendChild(flt);
    var ft=el('table',{'class':'fields'}),inputs={};
    flt.oninput=function(){var f=flt.value.toLowerCase();Array.prototype.forEach.call(ft.rows,function(tr){tr.style.display=(!f||tr.dataset.k.indexOf(f)>=0)?'':'none'})};
    Object.keys(row).sort().forEach(function(k){
      var tr=el('tr',{'data-k':k.toLowerCase()}),t=types[k]||'',v=row[k],td=el('td');tr.appendChild(el('td',null,k+'\n'));tr.firstChild.appendChild(el('div',{'class':'muted'},t));
      var isJSON=t.indexOf('json')>=0,txt=show(v),big=isJSON||txt.length>60||txt.indexOf('\n')>=0;
      var inp=big?el('textarea'):el('input');inp.value=txt;if(big)inp.rows=Math.min(12,Math.max(2,txt.split('\n').length));
      if(locked[k]||deleted){inp.disabled=true}
      var nul=el('input',{type:'checkbox',title:'set to null'});var lab=el('label',{'class':'muted'});lab.appendChild(nul);lab.appendChild(document.createTextNode(' null'));
      if(locked[k]||deleted)nul.disabled=true;
      function mark(){tr.className=(inp.value!==txt||nul.checked)?'changed':''}
      inp.oninput=mark;nul.onchange=mark;td.appendChild(inp);if(!locked[k]&&!deleted)td.appendChild(lab);tr.appendChild(td);ft.appendChild(tr);
      inputs[k]={inp:inp,nul:nul,orig:txt,json:isJSON}});
    d.appendChild(ft);
    if(!deleted){var sb=el('div',{'class':'bar'});var note=el('input',{placeholder:'Why (optional, goes in the log)'});note.style.flex='1';
      var save=el('button',{'class':'primary'},'Save changes');save.onclick=function(){
        var set={},bad='';Object.keys(inputs).forEach(function(k){var f=inputs[k];if(f.nul.checked){set[k]=null;return}if(f.inp.value===f.orig)return;
          if(f.json){try{set[k]=JSON.parse(f.inp.value)}catch(e){bad=k}}else set[k]=f.inp.value});
        if(bad){say(bad+' is not valid JSON. Nothing was saved.','err');return}
        if(!Object.keys(set).length){say('Nothing changed.');return}
        api('POST','/api/admin/'+tab+'/'+id,{set:set,note:note.value}).then(function(r){say('Saved: '+(r.changed.length?r.changed.join(', '):'no field changed'),'ok');open(id);load()}).catch(function(e){say(e.message,'err')})};
      sb.appendChild(note);sb.appendChild(save);d.appendChild(sb)}
    window.scrollTo(0,0)}).catch(function(e){if(e.message!=='auth')say(e.message,'err')})}
function member(body,id){api('POST','/api/admin/campaigns/'+id,body).then(function(r){say(r.message,'ok');open(id);load()}).catch(function(e){say(e.message,'err')})}
if(key){$('app').hidden=false;$('nav').hidden=false;load()}else{$('login').hidden=false}
})();
</script></body></html>`
