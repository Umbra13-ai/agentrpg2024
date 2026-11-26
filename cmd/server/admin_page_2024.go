package main

// The admin page (admin_2024.go has the API). Phone first: characters open as a sheet with plain labels, campaigns as a short form,
// and the raw fields wait in a closed "Advanced" section. Everything shown is put in with textContent, so names and text that
// players wrote can never run as HTML.

import (
	"fmt"
	"strings"
)

const adminPageHTML = `<!DOCTYPE html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1, viewport-fit=cover">
<meta name="robots" content="noindex"><title>Agent RPG Admin</title>
<!--ADMIN_THEME_SCRIPT-->
<style>
:root{--bg:#f4f1ea;--card:#fff;--ink:#1f1c18;--muted:#6f695f;--line:#e2dccf;--accent:#8a4a1c;--accent-ink:#fff;--warn:#b0291c;--ok:#2f6e33;--chip:#efe8da;--chg:#fff4cc}
@media (prefers-color-scheme:dark){:root{--bg:#141210;--card:#1f1c19;--ink:#efe9df;--muted:#a8a196;--line:#37322b;--accent:#e2a06a;--accent-ink:#1a1206;--warn:#ff8f80;--ok:#93d493;--chip:#2c2722;--chg:#4a3f14}}
/*ADMIN_THEMES*/
*{box-sizing:border-box}html{-webkit-text-size-adjust:100%}
body{margin:0;background:var(--bg);color:var(--ink);font:17px/1.45 -apple-system,system-ui,sans-serif;padding-bottom:110px}
header{position:sticky;top:0;z-index:5;background:var(--bg);border-bottom:1px solid var(--line);padding:10px 16px}
.top{display:flex;align-items:center;gap:10px}.top h1{font-size:19px;margin:0;flex:1}
.seg{display:flex;flex-wrap:wrap;gap:0 2px;margin-top:10px;padding:0 2px;border-bottom:2px solid var(--line)}
/* layered tabs: each one sits a little behind the next, the chosen one comes forward and joins the page below it */
.seg button{flex:1 1 0;min-width:0;border:1px solid var(--line);border-bottom:0;background:linear-gradient(var(--chip),var(--line));padding:9px 4px 8px;margin-top:5px;border-radius:12px 12px 0 0;font-weight:600;font-size:15px;color:var(--muted);box-shadow:inset 0 -6px 8px -6px rgba(0,0,0,.18);position:relative;top:2px}
.seg button+button{margin-left:-6px;padding-left:10px}
.seg button[aria-pressed=true]{background:var(--bg);color:var(--ink);margin-top:0;z-index:2;box-shadow:0 -3px 6px -2px rgba(0,0,0,.2);border-color:var(--accent);border-bottom:0;padding-bottom:10px}
.seg button:nth-child(-n+2){flex-grow:1.4}
/* on a phone Characters and Campaigns (the longest words) share the first row, the other three the second */
@media (max-width:440px){.seg button{flex:1 1 30%;margin-left:0!important}.seg button:nth-child(-n+2){flex:1 1 45%}.seg button+button{padding-left:4px}}
.sub{display:flex;gap:6px;margin-bottom:12px}.sub button{flex:1;border:1px solid var(--line);background:var(--card);border-radius:10px;padding:8px 4px;font-weight:600;color:var(--muted)}
.sub button[aria-pressed=true]{background:var(--accent);border-color:var(--accent);color:var(--accent-ink)}
.chev{display:flex;align-items:center;justify-content:center;width:40px;height:40px;margin:-8px -8px -8px 6px;border-radius:10px;color:var(--muted);font-size:20px;flex:0 0 auto;transition:transform .15s ease}
.chev[aria-expanded=true]{transform:rotate(180deg);color:var(--accent)}
.item.rowflex{display:flex;align-items:center;gap:6px}.item.rowflex>.main{flex:1;min-width:0}
.panel{background:var(--card);border:1px solid var(--line);border-radius:14px;padding:12px 14px;margin:-4px 0 10px}
.stats{display:grid;grid-template-columns:repeat(2,1fr);gap:8px 12px;margin-bottom:8px}.stats div b{display:block;font-size:17px}.stats div span{color:var(--muted);font-size:14px}
.kv{display:flex;justify-content:space-between;gap:10px;padding:6px 0;border-top:1px solid var(--line)}.kv:first-child{border-top:0}.kv span:last-child{font-weight:600;text-align:right;word-break:break-word}
.rep{border-top:1px solid var(--line);padding:10px 0}.rep:first-child{border-top:0}.rep pre{white-space:pre-wrap;word-break:break-word;font:inherit;margin:6px 0;color:var(--muted)}
.st{display:inline-block;font-size:13px;font-weight:700;padding:2px 8px;border-radius:99px;background:var(--chip);color:var(--muted);margin-left:6px}.st.bad{color:var(--warn)}.st.ok{color:var(--ok)}
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
.card .muted{overflow-wrap:anywhere}.mthumb{flex:0 0 44px;width:44px;height:44px;border-radius:50%;overflow:hidden;display:flex;align-items:center;justify-content:center;font-size:24px;background:var(--line)}.mthumb img{width:100%;height:100%;object-fit:cover}
.item b{display:block;font-size:17px}.item span{color:var(--muted);font-size:15px}
.swipe{position:relative;margin-bottom:10px;border-radius:14px;overflow:hidden}
.swipe .item{position:relative;z-index:1;margin:0;touch-action:pan-y;transition:transform .2s ease}
.swipe.drag .item{transition:none}
.swipe .act{position:absolute;top:0;right:0;bottom:0;width:96px;border:0;color:#fff;font-weight:700;font-size:13px;display:flex;flex-direction:column;align-items:center;justify-content:center;gap:3px;background:#c62828}
.swipe:not(.shown) .act{visibility:hidden}.swipe .act.restore{background:#2f6e33}.swipe .act svg{width:24px;height:24px}
.swipe.gone{opacity:0;margin-bottom:0;transition:height .22s ease,opacity .22s ease,margin .22s ease}
@media (prefers-reduced-motion:reduce){.swipe .item,.swipe.gone{transition:none}}
#toast button{margin-left:12px;background:transparent;border:1px solid currentColor;color:inherit;border-radius:8px;padding:2px 10px;font-weight:700}
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
.bggrid{display:grid;grid-template-columns:repeat(2,1fr);gap:8px}.bgpick{display:flex;flex-direction:column;gap:4px;border:1px solid var(--line);background:var(--card);border-radius:10px;padding:6px;font-size:13px;text-align:left;min-height:44px}.bgpick img{width:100%;aspect-ratio:16/9;object-fit:cover;border-radius:6px}
.tagchips{display:flex;flex-wrap:wrap;gap:6px;margin-bottom:6px}.tagchip{border:1px solid var(--line);background:var(--card);border-radius:99px;padding:8px 12px;font-size:14px;min-height:40px}.tagchip[aria-pressed=true]{background:var(--accent);border-color:var(--accent);color:var(--accent-ink)}
@media (min-width:600px){.bggrid{grid-template-columns:repeat(4,1fr)}}
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
/* touch targets at a phone's width: a control is at least 40 px high (tools/page_check_320.js) and a long label wraps instead of widening the page */
button,input:not([type=checkbox]):not([type=radio]),select,summary,.link{min-height:40px}
.btn{max-width:100%;white-space:normal}.row>.btn{flex:0 1 auto}
.btn.small{padding:6px 10px;min-height:40px}.link{padding:6px 0;display:inline-flex;align-items:center}
.chip button{min-width:40px;min-height:40px;margin:-8px -4px -8px 0}
details.card>summary{align-items:center;min-height:40px}
main a[target=_blank]{padding:9px 0;min-height:40px}.toggle{min-height:40px}.toggle input{width:24px;height:24px;transform:none}
</style></head><body>
<div id="toast" role="status"></div>
<header><div class="top"><h1>Admin</h1><button class="link" id="logout" hidden>Sign out</button></div>
<div class="seg" id="tabs" hidden><button data-tab="characters" aria-pressed="true">Characters</button><button data-tab="campaigns" aria-pressed="false">Campaigns</button><button data-tab="accounts" aria-pressed="false">Accounts</button><button data-tab="monsters" aria-pressed="false">Monsters</button><button data-tab="logs" aria-pressed="false">Logs</button><button data-tab="settings" aria-pressed="false">Settings</button></div></header>
<main id="main"></main>
<div class="savebar" id="savebar"><div class="row"><input id="reason" placeholder="Reason (optional)"><button class="btn" id="discard">Undo</button><button class="btn primary" id="save">Save</button></div></div>
<script>
(function(){
var tab='characters',pending={},orig={},curId=0,campaignsCache=null,spellsCache=null;
var M=document.getElementById('main');
function el(t,a,txt){var e=document.createElement(t);if(a)for(var k in a){if(k==='class')e.className=a[k];else e.setAttribute(k,a[k])}if(txt!==undefined&&txt!==null)e.textContent=String(txt);return e}
function add(p){for(var i=1;i<arguments.length;i++)if(arguments[i])p.appendChild(arguments[i]);return p}
var toastT;function toast(t,err){var b=document.getElementById('toast');b.textContent=t;b.className=err?'err':'';b.style.display='block';clearTimeout(toastT);toastT=setTimeout(function(){b.style.display='none'},err?6000:3000)}
function api(m,p,body){return fetch(p,{method:m,credentials:'same-origin',headers:{'X-Admin-Page':'1','Content-Type':'application/json'},body:body?JSON.stringify(body):undefined}).then(function(r){return r.json().catch(function(){return {}}).then(function(j){
  if(r.status===401||r.status===429){showSignedOut();toast(j.message||'Please sign in again',true);throw new Error('auth')}if(!r.ok)throw new Error(j.message||j.error||('Error '+r.status));return j})})}
function fail(e){if(e.message!=='auth')toast(e.message,true)}
function showSignedOut(){document.getElementById('tabs').hidden=true;document.getElementById('logout').hidden=true;pending={};hideSave();login()}
function signOut(){fetch('/api/admin/session',{method:'DELETE',credentials:'same-origin'}).then(showSignedOut,showSignedOut)}
document.getElementById('logout').onclick=function(){if(dirty()&&!confirm('Discard unsaved changes?'))return;signOut()};
function login(){M.textContent='';var c=el('div',{'class':'card'});add(c,el('h2',null,'Sign in'),el('p',{'class':'muted'},'Enter the admin key once. This browser stays signed in for 30 days (until you sign out or the key changes).'));
  var i=el('input',{type:'password',autocomplete:'current-password',placeholder:'Admin key'});var b=el('button',{'class':'btn primary'},'Sign in');b.style.marginTop='10px';b.style.width='100%';
  b.onclick=function(){var k=i.value.trim();if(!k)return;fetch('/api/admin/session',{method:'POST',credentials:'same-origin',headers:{'X-Admin-Key':k}}).then(function(r){return r.json().then(function(j){if(!r.ok)throw new Error(j.message||'Wrong key');i.value='';start()})}).catch(function(e){toast(e.message,true)})};
  i.onkeydown=function(e){if(e.key==='Enter')b.onclick()};add(c,i,b);M.appendChild(c)}
function start(){document.getElementById('tabs').hidden=false;document.getElementById('logout').hidden=false;showList()}
Array.prototype.forEach.call(document.querySelectorAll('#tabs button'),function(b){b.onclick=function(){if(dirty()&&!confirm('Discard unsaved changes?'))return;
  tab=b.dataset.tab;q='';which='';Array.prototype.forEach.call(document.querySelectorAll('#tabs button'),function(x){x.setAttribute('aria-pressed',x===b)});showList()}});

// ---------- lists
var q='',which='';
function showList(){hideSave();pending={};M.textContent='';window.scrollTo(0,0);
  if(tab==='logs')return showLogs();
  if(tab==='monsters')return showMonsters();
  if(tab==='settings')return showSettings();
  var s=el('div',{'class':'search'});var i=el('input',{type:'search',placeholder:tab==='characters'?'Search name, player or id':(tab==='accounts'?'Name, email or id':'Search name, GM or id')});i.value=q;
  var sel=el('select');[['','Active'],['only','Deleted'],['include','All']].forEach(function(o){var op=el('option',{value:o[0]},o[1]);if(o[0]===which)op.selected=true;sel.appendChild(op)});
  add(s,i,sel);M.appendChild(s);if(tab==='campaigns'){var lb=el('button',{'class':'btn'},'Background library');lb.style.cssText='width:100%;margin:-2px 0 12px';lb.onclick=showBackgrounds;M.appendChild(lb)}var box=el('div');M.appendChild(box);
  var t;i.oninput=function(){clearTimeout(t);t=setTimeout(function(){q=i.value;load()},300)};sel.onchange=function(){which=sel.value;load()};
  function load(){api('GET','/api/admin/'+tab+'?q='+encodeURIComponent(q)+'&deleted='+which).then(function(j){var list=j[tab];box.textContent='';
    if(!list.length){box.appendChild(el('p',{'class':'muted'},'Nothing found.'));return}
    box.appendChild(el('p',{'class':'muted small'},'Swipe a row left (or drag it with the mouse) to '+(which==='only'?'restore it.':'delete it; you can undo.')));
    list.forEach(function(x){var acc=tab==='accounts';var b=acc?el('div',{'class':'item rowflex',role:'button',tabindex:'0'}):el('button',{'class':'item'});var title=el('b',null,x.name);
      if(x.deleted)title.appendChild(el('span',{'class':'badge del'},'Deleted'));
      if(tab==='campaigns'&&!x.deleted)title.appendChild(el('span',{'class':'badge'+(x.status==='active'?' on':'')},x.status));
      var sub=tab==='characters'?('Level '+x.level+' '+cap(x.species)+' '+cap(x.class)+' · '+(x.owner||'no player')+(x.campaign?' · '+x.campaign:'')):
        (tab==='accounts'?((x.email&&x.email!==x.name?x.email:'no email')+' · '+x.characters+' character'+(x.characters===1?'':'s')+(x.campaigns?' · GM of '+x.campaigns:'')+(x.is_moderator?' · moderator':'')+(x.last_seen?' · seen '+x.last_seen:'')):
        ('GM '+(x.gm||'none')+' · '+x.players+' of '+x.max_players+' players'));
      if(tab==='accounts'&&x.is_moderator&&!x.deleted)title.appendChild(el('span',{'class':'badge on'},'Moderator'));
      if(!acc){add(b,title,el('span',null,sub))}
      else{var m=el('div',{'class':'main'});add(m,title,el('span',null,sub));var chev=el('span',{'class':'chev',role:'button',tabindex:'0','aria-label':'Show details','aria-expanded':'false'},'▾');add(b,m,chev);
        b.addEventListener('keydown',function(e){if(e.key==='Enter'&&e.target===b)openAccount(x.id)});
        var panel=el('div',{'class':'panel'});panel.hidden=true;var loaded=false;
        function toggle(e){e.stopPropagation();e.preventDefault();var open=panel.hidden;panel.hidden=!open;chev.setAttribute('aria-expanded',String(open));
          if(open&&!loaded){loaded=true;panel.textContent='Loading…';api('GET','/api/admin/accounts/'+x.id+'/activity').then(function(a){panel.textContent='';renderActivity(panel,a,x.id)}).catch(function(e2){loaded=false;panel.textContent='';fail(e2)})}}
        chev.addEventListener('click',toggle);chev.addEventListener('keydown',function(e){if(e.key==='Enter'||e.key===' ')toggle(e)});chev.addEventListener('pointerdown',function(e){e.stopPropagation()});
        box.appendChild(swipeRow(b,x.deleted,function(){openAccount(x.id)},function(row){quickAction(x,row,load)}));box.appendChild(panel);return}
      box.appendChild(swipeRow(b,x.deleted,function(){tab==='characters'?openCharacter(x.id):openCampaign(x.id)},function(row){quickAction(x,row,load)}))})}).catch(fail)}
  load()}

// ---------- swipe a row left: a red Delete (or, for a deleted row, a green Restore) slides out from under it
var TRASH='<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M3 6h18M8 6V4h8v2M6 6l1 14h10l1-14M10 11v6M14 11v6"/></svg>';
var RESTORE='<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M3 12a9 9 0 1 0 3-6.7M3 4v5h5"/></svg>';
var openRow=null;
document.addEventListener('pointerdown',function(e){if(openRow&&!openRow.contains(e.target))openRow.close()});
function swipeRow(item,deleted,onOpen,onAct){var W=96;var w=el('div',{'class':'swipe'});
  var a=el('button',{'class':'act'+(deleted?' restore':''),tabindex:'-1','aria-label':deleted?'Restore':'Delete'});a.innerHTML=deleted?RESTORE:TRASH;a.appendChild(el('span',null,deleted?'Restore':'Delete'));
  var down=false,dragging=false,moved=false,x0=0,y0=0,base=0,dx=0;
  function set(v){if(v)w.classList.add('shown');item.style.transform=v?'translateX('+v+'px)':''}
  item.addEventListener('transitionend',function(){if(!base&&!down)w.classList.remove('shown')});
  w.close=function(){set(0);base=0;a.tabIndex=-1;if(openRow===w)openRow=null};
  function open(){if(openRow&&openRow!==w)openRow.close();set(-W);base=-W;a.tabIndex=0;openRow=w}
  function act(){if(a.disabled)return;a.disabled=true;set(-W);onAct(w)}
  item.addEventListener('pointerdown',function(e){if(e.pointerType==='mouse'&&e.button!==0)return;down=true;dragging=false;moved=false;x0=e.clientX;y0=e.clientY;dx=base});
  item.addEventListener('pointermove',function(e){if(!down)return;var mx=e.clientX-x0,my=e.clientY-y0;
    if(!dragging){if(Math.abs(mx)>8&&Math.abs(mx)>Math.abs(my)){dragging=true;moved=true;w.classList.add('drag');try{item.setPointerCapture(e.pointerId)}catch(_){}}else if(Math.abs(my)>8){down=false;return}else return}
    dx=Math.max(-W*1.6,Math.min(0,base+mx));set(dx);e.preventDefault()});
  function up(){if(!down)return;down=false;if(!dragging)return;w.classList.remove('drag');
    if(dx<-W*1.35)act();else if(dx<-W/2)open();else w.close()}
  item.addEventListener('pointerup',up);item.addEventListener('pointercancel',up);
  item.addEventListener('click',function(e){if(moved){moved=false;e.preventDefault();return}if(base){w.close();return}onOpen()});
  item.addEventListener('keydown',function(e){if(e.key==='Delete'||e.key==='Backspace'){e.preventDefault();open();a.focus()}});
  a.onclick=act;add(w,a,item);return w}
// Delete (or restore) from the list, with Undo; the row folds away and the list reloads.
function quickAction(x,row,reload){var t=tab,kind=t==='characters'?'character':(t==='accounts'?'account':'campaign'),verb=x.deleted?'restore':'delete';
  api('POST','/api/admin/'+t+'/'+x.id+'/'+verb).then(function(r){campaignsCache=null;fold(row,reload);
    if(verb==='delete')undoToast(cap(kind)+' '+x.name+' deleted.',function(){api('POST','/api/admin/'+t+'/'+x.id+'/restore').then(function(r2){toast(r2.message||'Restored.');campaignsCache=null;reload()}).catch(fail)});
    else toast(r.message||'Restored.')}).catch(function(e){row.querySelector('.act').disabled=false;row.close();fail(e)})}
function fold(row,then){row.style.height=row.offsetHeight+'px';void row.offsetHeight;row.classList.add('gone');row.style.height='0px';setTimeout(then,240)}
function undoToast(t,undo){var b=document.getElementById('toast');b.textContent=t;b.className='';var u=el('button',null,'Undo');
  u.onclick=function(){b.style.display='none';undo()};b.appendChild(u);b.style.display='block';clearTimeout(toastT);toastT=setTimeout(function(){b.style.display='none'},8000)}
function cap(s){s=String(s||'');return s.charAt(0).toUpperCase()+s.slice(1)}

// ---------- editing helpers
function dirty(){return Object.keys(pending).length>0}
function same(a,b){return JSON.stringify(a)===JSON.stringify(b)}
function setField(f,v){if(same(v,orig[f]))delete pending[f];else pending[f]=v;var n=Object.keys(pending).length;
  document.getElementById('savebar').classList.toggle('show',n>0);document.getElementById('save').textContent=n?'Save '+n+(n>1?' changes':' change'):'Save';
  Array.prototype.forEach.call(document.querySelectorAll('[data-f]'),function(w){w.classList.toggle('changed',w.getAttribute('data-f') in pending)})}
function hideSave(){document.getElementById('savebar').classList.remove('show');document.getElementById('reason').value=''}
document.getElementById('discard').onclick=function(){pending={};hideSave();reopen()};
document.getElementById('save').onclick=function(){if(!dirty())return;var path='/api/admin/'+tab+'/';
  api('POST',path+curId,{set:pending,note:document.getElementById('reason').value}).then(function(r){toast(r.changed.length?'Saved':'Nothing changed');pending={};hideSave();campaignsCache=null;reopen()}).catch(fail)};
function reopen(){tab==='characters'?openCharacter(curId):(tab==='accounts'?openAccount(curId):openCampaign(curId))}
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

// ---------- monsters: every monster GMs built (admin_monsters_2024.go), its stat block and its token
var mq='',mwhich='';
function thumb(u,name){var t=el('span',{'class':'mthumb'});if(u){var i=el('img',{alt:'',src:u,loading:'lazy'});t.appendChild(i)}else t.textContent='👹';t.setAttribute('aria-label',name+(u?' token':' has no token'));return t}
function showMonsters(){var s=el('div',{'class':'search'});var i=el('input',{type:'search',placeholder:'Name, key or GM'});i.value=mq;
  var sel=el('select');[['','Current'],['only','Hidden'],['include','All']].forEach(function(o){var op=el('option',{value:o[0]},o[1]);if(o[0]===mwhich)op.selected=true;sel.appendChild(op)});
  add(s,i,sel);M.appendChild(s);M.appendChild(el('p',{'class':'muted small'},'Every monster GMs built with the monster builder, from every GM, shared or private (Hidden: hidden by its GM, or an old version an edit replaced). Open one for its stat block and its token.'));var box=el('div');M.appendChild(box);
  var t;i.oninput=function(){clearTimeout(t);t=setTimeout(function(){mq=i.value;load()},300)};sel.onchange=function(){mwhich=sel.value;load()};
  function load(){api('GET','/api/admin/monsters?q='+encodeURIComponent(mq)+'&deleted='+mwhich).then(function(j){box.textContent='';var list=j.monsters||[];
    if(!list.length){box.appendChild(el('p',{'class':'muted'},'Nothing found.'));return}
    list.forEach(function(x){var b=el('button',{'class':'item rowflex'});var m=el('div',{'class':'main'});var title=el('b',null,x.name);
      if(x.deleted)title.appendChild(el('span',{'class':'badge del'},'Hidden'));title.appendChild(el('span',{'class':'badge'+(x.shared?' on':'')},x.shared?'Shared':'Private'));
      add(m,title,el('span',null,'CR '+(x.cr||'?')+' · '+[x.size,x.type].filter(Boolean).join(' ')+' · GM '+(x.owner||'unknown')+' · '+x.created+(x.replaces?' · a new version':'')));
      add(b,thumb(x.token_url,x.name),m);b.onclick=function(){openMonster(x.key)};box.appendChild(b)})}).catch(fail)}
  load()}
function openMonster(key){M.textContent='';window.scrollTo(0,0);
  api('GET','/api/admin/monsters/'+encodeURIComponent(key)).then(function(j){var m=j.monster;M.appendChild(back());
    var badge=el('span',{'class':'badge'+(m.shared?' on':'')},m.shared?'Shared':'Private');
    M.appendChild(headCard(m.name,m.deleted,badge,['CR '+(m.cr||'?')+' · built by '+(m.owner||'unknown')+' · saved '+m.created,'Key '+m.key,m.source_note?'Source: '+m.source_note:''].filter(Boolean)));
    M.appendChild(monsterTokenCard(m,j.token_rule));
    if(j.versions&&j.versions.length){var v=section('Versions ('+(j.versions.length+1)+')');v.appendChild(el('p',{'class':'muted small'},'An edit saves a new version and hides the old one; a fight that started with an old one keeps it.'));
      j.versions.forEach(function(x){var b=el('button',{'class':'btn small'},x.saved+' · '+x.key);b.onclick=function(){openMonster(x.key)};v.appendChild(b)});M.appendChild(v)}
    var sb=section('Stat block',true);var f=el('iframe',{src:j.page,title:'Stat block of '+m.name,loading:'lazy'});f.style.cssText='width:100%;border:0;border-radius:10px;min-height:600px;background:transparent';
    f.onload=function(){try{f.style.height=(f.contentWindow.document.documentElement.scrollHeight+8)+'px'}catch(_){}};
    var open=el('a',{href:j.page,target:'_blank',rel:'noopener'},'Open the stat block on its own');add(sb,f,open);M.appendChild(sb)}).catch(fail)}
function showBackgrounds(){M.textContent='';window.scrollTo(0,0);M.appendChild(back());
  var h=el('div',{'class':'card'});add(h,el('h2',null,'Background library'),el('p',{'class':'muted small'},'The scenes a campaign’s background is chosen from. Agents pick by the tags and description when they create a campaign (the server picks the best match when they name none). A retired scene is no longer offered; campaigns that use it keep it. Titles describe the picture: never a campaign’s or a book’s name.'));
  var nb=el('button',{'class':'btn primary'},'Add a picture');nb.onclick=function(){editBackground(null)};h.appendChild(nb);M.appendChild(h);var box=el('div');M.appendChild(box);
  api('GET','/api/admin/backgrounds').then(function(j){bgVocab=j.vocabulary;(j.backgrounds||[]).forEach(function(x){var b=el('button',{'class':'item rowflex'});
    var im=el('img',{alt:'',src:x.thumb_url,loading:'lazy'});im.style.cssText='width:96px;height:54px;object-fit:cover;border-radius:6px;flex:none';
    var m=el('div',{'class':'main'});var t=el('b',null,x.title);if(x.retired)t.appendChild(el('span',{'class':'badge del'},'Retired'));
    add(m,t,el('span',{'class':'muted small'},tagWords(x.tags).join(', ')+' · '+x.campaigns+' campaign'+(x.campaigns===1?'':'s')));add(b,im,m);b.onclick=function(){editBackground(x)};box.appendChild(b)})}).catch(fail)}
var bgVocab=null;
function tagWords(t){var o=[];['place','mood','light','features'].forEach(function(g){(t&&t[g]||[]).forEach(function(w){o.push(w)})});return o}
function editBackground(x){M.textContent='';window.scrollTo(0,0);var bk=el('button',{'class':'link'},'‹ Library');bk.onclick=showBackgrounds;M.appendChild(bk);
  var c=el('div',{'class':'card'});c.appendChild(el('h2',null,x?x.title:'Add a picture'));
  if(x){var big=el('img',{alt:'',src:x.image_url});big.style.cssText='width:100%;border-radius:8px;margin-bottom:8px';c.appendChild(big);c.appendChild(el('p',{'class':'muted small'},'Key '+x.key))}
  function field(label,v,area){var l=el('label',{'class':'f'});l.appendChild(el('span',null,label));var i=el(area?'textarea':'input');i.value=v||'';if(area)i.rows=3;l.appendChild(i);c.appendChild(l);return i}
  var key=x?null:field('Key (lowercase words joined by hyphens, describing the picture)','');
  var title=field('Title',x&&x.title),desc=field('Description (one sentence for a GM choosing a scene)',x&&x.description,true);
  var picked={};['place','mood','light','features'].forEach(function(g){picked[g]={};((x&&x.tags[g])||[]).forEach(function(w){picked[g][w]=true})});
  ['place','mood','light','features'].forEach(function(g){c.appendChild(el('h3',null,g.charAt(0).toUpperCase()+g.slice(1)));var row=el('div',{'class':'tagchips'});
    ((bgVocab||{})[g]||[]).forEach(function(w){var b=el('button',{'class':'tagchip','aria-pressed':picked[g][w]?'true':'false'},w);b.onclick=function(){picked[g][w]=!picked[g][w];b.setAttribute('aria-pressed',picked[g][w]?'true':'false')};row.appendChild(b)});c.appendChild(row)});
  var ret=null;if(x){var rl=el('label',{'class':'row'});ret=el('input',{type:'checkbox'});ret.style.width='auto';ret.checked=!!x.retired;add(rl,ret,el('span',null,' Retired: no longer offered'));c.appendChild(rl)}
  c.appendChild(el('p',{'class':'muted small'},x?'Replace the picture (optional): PNG, JPEG or WebP, stored as a 1920 by 1080 WebP.':'The picture: PNG, JPEG or WebP, up to 5 MB, stored as a 1920 by 1080 WebP (another shape is cropped to cover it).'));
  var f=el('input',{type:'file',accept:'image/png,image/jpeg,image/webp','aria-label':'Picture'});c.appendChild(f);
  var sv=el('button',{'class':'btn primary'},x?'Save':'Add to the library');sv.style.marginTop='10px';c.appendChild(sv);M.appendChild(c);
  sv.onclick=function(){var tags={};Object.keys(picked).forEach(function(g){tags[g]=Object.keys(picked[g]).filter(function(w){return picked[g][w]})});
    var body={title:title.value,description:desc.value,tags:tags};if(key)body.key=key.value.trim();if(ret)body.retired=ret.checked;
    function send(){sv.disabled=true;api('POST','/api/admin/backgrounds'+(x?'/'+encodeURIComponent(x.key):''),body).then(function(r){sv.disabled=false;toast(r.note||'Saved');showBackgrounds()}).catch(function(e){sv.disabled=false;fail(e)})}
    var file=f.files&&f.files[0];if(!file){if(!x){toast('Choose the picture first',true);return}send();return}
    if(file.size>5*1024*1024){toast('The image is over 5 MB. Choose a smaller one.',true);return}
    var rd=new FileReader();rd.onload=function(){body.image_base64=rd.result;send()};rd.onerror=function(){toast('That file could not be read',true)};rd.readAsDataURL(file)}}
function monsterTokenCard(m,rule){var s=section('Token',true);s.appendChild(el('p',{'class':'muted small'},rule||''));
  var img=el('img',{alt:'Token of '+m.name});img.style.cssText='display:block;width:128px;height:128px;border-radius:50%;object-fit:cover;margin:8px 0';
  var none=el('p',{'class':'box plain'},'No token: the encounter stage shows 👹.');
  function show(u){if(u){img.src=u;img.style.display='block';none.style.display='none'}else{img.style.display='none';none.style.display='block'}}
  var f=el('input',{type:'file',accept:'image/png,image/jpeg,image/webp','aria-label':'Token image'});
  var up=el('button',{'class':'btn primary'},'Add or replace token');var path='/api/admin/monsters/'+encodeURIComponent(m.key)+'/token';
  up.onclick=function(){var file=f.files&&f.files[0];if(!file){toast('Choose an image first',true);return}
    if(file.size>3*1024*1024){toast('The image is over 3 MB. Choose a smaller one.',true);return}
    var rd=new FileReader();rd.onload=function(){up.disabled=true;api('POST',path,{image_base64:rd.result}).then(function(r){up.disabled=false;toast(r.message||'Token saved');f.value='';show(r.token&&r.token.url)}).catch(function(e){up.disabled=false;fail(e)})};
    rd.onerror=function(){toast('That file could not be read',true)};rd.readAsDataURL(file)};
  var rm=el('button',{'class':'btn danger'},'Remove token');
  rm.onclick=function(){if(confirm('Remove the token of '+m.name+'?'))api('DELETE',path).then(function(r){toast(r.message);show('')}).catch(fail)};
  add(s,img,none,f,add(el('div',{'class':'row'}),up,rm));show(m.token_url);return s}

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
    if(c.agent_id&&j.owner_name)M.appendChild(tokenCard(c.agent_id,j.owner_name));
    if(c.agent_id&&j.owner_name)M.appendChild(modelCard(c.agent_id,j.owner_name));
    M.appendChild(portraitCard(id,c.name));
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
function modelCard(aid,name){var s=section('AI model',true);
  var cur=el('div',{'class':'box plain'},'Loading…');var sel=el('select',{'aria-label':'AI model'});
  var txt=el('input',{type:'text',maxlength:'60',placeholder:'Model name: optional, required for Other','aria-label':'Model name'});var save=el('button',{'class':'btn primary'},'Save model');
  function show(m){cur.textContent='';if(m.icon){var i=el('img',{src:m.icon,alt:''});i.style.cssText='width:20px;height:20px;border-radius:50%;vertical-align:middle;margin-right:8px';cur.appendChild(i)}cur.appendChild(document.createTextNode(m.text))}
  api('GET','/api/admin/accounts/'+aid+'/model').then(function(j){j.choices.forEach(function(c){sel.appendChild(el('option',{value:c.key},c.label))});show(j.model);sel.value=j.model.state==='shared'?j.model.family:j.model.state;txt.value=j.model.model||''}).catch(fail);
  save.onclick=function(){save.disabled=true;api('POST','/api/admin/accounts/'+aid+'/model',{family:sel.value,model:txt.value}).then(function(r){save.disabled=false;toast(r.message);show(r.model)}).catch(function(e){save.disabled=false;fail(e)})};
  add(s,el('p',{'class':'muted small'},'The AI model '+name+' plays with: its character sheets and token cards show it, and a GM\'s shows on the monsters it runs. Pick one (a model name is optional), or Other and type any name not listed.'),cur,add(el('div',{'class':'grid'}),sel,txt),save);return s}
function portraitCard(id,name){var s=section('Portrait',true);
  s.appendChild(el('p',{'class':'muted small'},'The photo on the character’s sheet. Add one for a character that has none, replace an unsuitable one, or remove it (the sheet goes back to the class icon). A PNG, JPEG or WebP, each side 32 to 4096 pixels, up to 5 MB; 1024 by 1280 fits the sheet exactly.'));
  var img=el('img',{alt:'Portrait of '+name});img.style.cssText='display:block;max-width:160px;border-radius:8px;margin:8px 0';
  var none=el('p',{'class':'box plain'},'No portrait: the sheet shows the class icon.');
  function show(){img.style.display='none';none.style.display='block';img.onload=function(){img.style.display='block';none.style.display='none'};img.onerror=function(){img.style.display='none';none.style.display='block'};img.src='/api/characters/'+id+'/portrait?'+Date.now()}
  var f=el('input',{type:'file',accept:'image/png,image/jpeg,image/webp','aria-label':'Portrait image'});
  var up=el('button',{'class':'btn primary'},'Add or replace portrait');
  up.onclick=function(){var file=f.files&&f.files[0];if(!file){toast('Choose an image first',true);return}
    if(file.size>5*1024*1024){toast('The image is over 5 MB. Choose a smaller one.',true);return}
    var rd=new FileReader();rd.onload=function(){up.disabled=true;api('POST','/api/admin/characters/'+id+'/portrait',{image_base64:rd.result}).then(function(r){up.disabled=false;toast(r.note||'Portrait saved');f.value='';show()}).catch(function(e){up.disabled=false;fail(e)})};
    rd.onerror=function(){toast('That file could not be read',true)};rd.readAsDataURL(file)};
  var rm=el('button',{'class':'btn danger'},'Remove portrait');
  rm.onclick=function(){if(confirm('Remove the portrait of '+name+'?'))api('DELETE','/api/admin/characters/'+id+'/portrait').then(function(r){toast(r.message);show()}).catch(fail)};
  add(s,img,none,f,add(el('div',{'class':'row'}),up,rm));show();return s}
function backgroundCard(id,name){var s=section('Background picture',true);
  s.appendChild(el('p',{'class':'muted small'},'The scene behind the campaign’s header on its Live Table. With none set the page shows the default dragon. A PNG, JPEG or WebP, each side 320 to 4096 pixels, up to 5 MB. It is stored as a 1920 by 1080 WebP: a picture of another shape is scaled to cover that and its edges are cropped.'));
  var img=el('img',{alt:'Background of '+name});img.style.cssText='display:block;max-width:100%;width:320px;border-radius:8px;margin:8px 0';
  var none=el('p',{'class':'box plain'},'No picture of its own: the page shows the default scene.');
  function show(){img.style.display='none';none.style.display='block';img.onload=function(){img.style.display='block';none.style.display='none'};img.onerror=function(){img.style.display='none';none.style.display='block'};img.src='/api/campaigns/'+id+'/background?'+Date.now()}
  var f=el('input',{type:'file',accept:'image/png,image/jpeg,image/webp','aria-label':'Background image'});
  var up=el('button',{'class':'btn primary'},'Set or replace background');
  up.onclick=function(){var file=f.files&&f.files[0];if(!file){toast('Choose an image first',true);return}
    if(file.size>5*1024*1024){toast('The image is over 5 MB. Choose a smaller one.',true);return}
    var rd=new FileReader();rd.onload=function(){up.disabled=true;api('POST','/api/admin/campaigns/'+id+'/background',{image_base64:rd.result}).then(function(r){up.disabled=false;toast(r.note||'Background saved');f.value='';show()}).catch(function(e){up.disabled=false;fail(e)})};
    rd.onerror=function(){toast('That file could not be read',true)};rd.readAsDataURL(file)};
  var rm=el('button',{'class':'btn danger'},'Remove background');
  rm.onclick=function(){if(confirm('Remove the background of '+name+'? The page goes back to the default scene.'))api('DELETE','/api/admin/campaigns/'+id+'/background').then(function(r){toast(r.message);show()}).catch(fail)};
  add(s,img,none,f,add(el('div',{'class':'row'}),up,rm));
  s.appendChild(el('h3',null,'Or choose from the library'));s.appendChild(el('p',{'class':'muted small'},'Tap a scene to show it on this campaign (its own uploaded picture, if any, is removed).'));
  var lib=el('div',{'class':'bggrid'});s.appendChild(lib);
  api('GET','/api/admin/backgrounds').then(function(j){(j.backgrounds||[]).filter(function(x){return !x.retired}).forEach(function(x){
    var b=el('button',{'class':'bgpick',title:x.description});var im=el('img',{alt:'',src:x.thumb_url,loading:'lazy'});add(b,im,el('span',null,x.title));
    b.onclick=function(){if(!confirm('Show “'+x.title+'” on '+name+'?'))return;api('POST','/api/admin/campaigns/'+id+'/background',{library:x.key}).then(function(r){toast(r.message||'Saved');show()}).catch(fail)};lib.appendChild(b)})}).catch(fail);
  show();return s}
function tokenCard(aid,owner){var s=section('Player’s token',true);
  s.appendChild(el('p',{'class':'muted small'},'If '+owner+' lost their token and can’t recover it by email, regenerate it here, copy it and send it to them privately. A token is shown once.'));
  var keep=el('label',{'class':'toggle'});var kc=el('input',{type:'checkbox'});kc.checked=true;add(keep,el('span',null,'End their old tokens and signing keys (recommended)'),kc);s.appendChild(keep);
  var out=el('div');var b=el('button',{'class':'btn primary'},'Regenerate token');
  b.onclick=function(){if(!confirm('Regenerate the token for '+owner+'?'+(kc.checked?' Their old tokens stop working.':'')))return;b.disabled=true;
    api('POST','/api/admin/issue-token',{agent_id:aid,revoke_old:kc.checked}).then(function(r){out.textContent='';
      var i=el('input',{type:'text',readonly:'readonly','aria-label':'New token'});i.value=r.token;i.style.fontFamily='monospace';i.onfocus=function(){i.select()};
      var cp=el('button',{'class':'btn'},'Copy token');cp.onclick=function(){var done=function(){cp.textContent='Copied';toast('Token copied')};
        if(navigator.clipboard&&navigator.clipboard.writeText)navigator.clipboard.writeText(r.token).then(done,function(){i.select();document.execCommand('copy');done()});else{i.select();document.execCommand('copy');done()}};
      add(out,add(el('div',{'class':'row'}),i,cp),el('p',{'class':'muted small'},'Shown once: copy it now and send it to '+owner+' privately. They sign in with it as Authorization: Bearer <token>.'+(r.revoked_tokens||r.revoked_keys?' Ended '+r.revoked_tokens+' old token(s) and '+r.revoked_keys+' signing key(s).':'')));
      b.textContent='Regenerate again';b.disabled=false}).catch(function(e){b.disabled=false;fail(e)})};
  add(s,b,out);return s}
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

// ---------- account
function openAccount(id){curId=id;pending={};hideSave();
  api('GET','/api/admin/accounts/'+id).then(function(j){var a=j.account;orig=JSON.parse(JSON.stringify(a));if(!/@/.test(orig.email||''))orig.email='';M.textContent='';window.scrollTo(0,0);M.appendChild(back());var del=!!a.deleted_at;
    var chars=j.characters||[],camps=j.campaigns||[];
    var head=headCard(a.name,del,a.is_moderator?el('span',{'class':'badge on'},'Moderator'):null,['Account '+a.id+' · '+(a.email&&a.email!==a.name?a.email:'no email')+(a.email_verified?' (verified)':''),
      'Joined '+String(a.created_at||'').slice(0,10)+(a.last_seen?' · last seen '+String(a.last_seen).slice(0,16).replace('T',' '):''),chars.filter(function(c){return!c.deleted}).length+' character(s) · GM of '+camps.length+' campaign(s)']);
    M.appendChild(head);
    if(del){var rc=el('div',{'class':'card'});add(rc,el('p',null,'This account is deleted: it cannot sign in and its tokens are on hold, with nothing erased.'+(a.deleted_by?' Deleted by '+a.deleted_by+'.':'')+' Restoring brings back its sign-in and the characters that were hidden with it.'));
      var rb=el('button',{'class':'btn primary'},'Restore account');rb.onclick=function(){if(confirm('Restore this account?'))api('POST','/api/admin/accounts/'+id+'/restore').then(function(r){toast(r.message);campaignsCache=null;openAccount(id)}).catch(fail)};rc.appendChild(rb);M.appendChild(rc);return}
    var s1=section('Details',true);
    add(s1,text('Name','name'),text('Email (an account without one signs in by token only)','email'),toggle('Verified','verified'),toggle('Email verified','email_verified'),toggle('Moderator','is_moderator'));M.appendChild(s1);
    M.appendChild(modelCard(id,a.name));
    var act=section('Activity',true);M.appendChild(act);api('GET','/api/admin/accounts/'+id+'/activity').then(function(d){renderActivity(act,d,id,true)}).catch(fail);
    M.appendChild(tokenCard(a.id,a.name));
    var s2=section('Characters',true);
    chars.forEach(function(c){var r=el('div',{'class':'row inv'});var t=el('div');add(t,el('div',null,c.name+(c.deleted?' (deleted)':'')),el('div',{'class':'muted small'},'Level '+c.level+' '+cap(c.class)+(c.campaign?' · '+c.campaign:'')));
      var o=el('button',{'class':'btn small'},'Open');o.onclick=function(){if(dirty()&&!confirm('Discard unsaved changes?'))return;tab='characters';openCharacter(c.id)};add(r,t,o);s2.appendChild(r)});
    if(!chars.length)s2.appendChild(el('p',{'class':'muted small'},'No characters.'));
    s2.appendChild(finder('Assign a character to this account','Character name','/api/admin/characters?q=','characters',function(x){return x.name+' · '+(x.owner||'no player')+(x.campaign?' · in '+x.campaign:'')},
      function(x){if(confirm('Give '+x.name+' to '+a.name+'?'+(x.owner?' It leaves '+x.owner+'.':'')))api('POST','/api/admin/characters/'+x.id,{set:{agent_id:id},note:'assigned from the account page'}).then(function(){toast('Assigned');campaignsCache=null;openAccount(id)}).catch(fail)}));
    M.appendChild(s2);
    var s3=section('Campaigns it runs');
    camps.forEach(function(c){var r=el('div',{'class':'row inv'});var t=el('div');add(t,el('div',null,c.name),el('div',{'class':'muted small'},cap(c.status)));
      var o=el('button',{'class':'btn small'},'Open');o.onclick=function(){if(dirty()&&!confirm('Discard unsaved changes?'))return;tab='campaigns';openCampaign(c.id)};add(r,t,o);s3.appendChild(r)});
    if(!camps.length)s3.appendChild(el('p',{'class':'muted small'},'It runs no campaign.'));M.appendChild(s3);
    M.appendChild(advanced(a,j.field_types,j.locked_fields));
    var dz=el('div',{'class':'card'});dz.appendChild(el('h3',null,'Delete'));dz.appendChild(el('p',{'class':'muted small'},'Hides the account: it can no longer sign in, and its characters are hidden with it. Nothing is erased; you can restore it here. An account that is the GM of a campaign can’t be deleted until that campaign is deleted or has another GM.'));
    var db=el('button',{'class':'btn danger'},'Delete account');db.onclick=function(){if(confirm('Delete '+a.name+' and hide its '+chars.filter(function(c){return!c.deleted}).length+' character(s)? You can restore it later.'))api('POST','/api/admin/accounts/'+id+'/delete').then(function(r){toast(r.message);campaignsCache=null;openAccount(id)}).catch(fail)};dz.appendChild(db);M.appendChild(dz)
  }).catch(fail)}

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
    M.appendChild(s2);M.appendChild(backgroundCard(id,c.name));M.appendChild(advanced(c,j.field_types,j.locked_fields));
    var dz=el('div',{'class':'card'});dz.appendChild(el('h3',null,'Delete'));dz.appendChild(el('p',{'class':'muted small'},'Hides the campaign. Its characters are let go and keep everything. You can restore it here.'));
    var db=el('button',{'class':'btn danger'},'Delete campaign');db.onclick=function(){if(confirm('Delete '+c.name+'? You can restore it later.'))api('POST','/api/admin/campaigns/'+id+'/delete').then(function(r){toast(r.message);campaignsCache=null;openCampaign(id)}).catch(fail)};dz.appendChild(db);M.appendChild(dz)
  }).catch(fail)}
function member(body,id){api('POST','/api/admin/campaigns/'+id,body).then(function(r){toast(r.message);campaignsCache=null;openCampaign(id)}).catch(fail)}

// ---------- activity of an account (the list's expanded row and the account's page)
function stat(label,value){var d=el('div');add(d,el('b',null,value),el('span',null,label));return d}
function renderActivity(box,d,id,full){var g=el('div',{'class':'stats'});
  add(g,stat('Last active',d.last_active||'never'),stat('First seen',d.first_seen),stat('Failed requests, 24 hours',d.failed_requests_24h),stat('Failed requests, 7 days',d.failed_requests_7d),
    stat('Failed sign-ins, 24 h / 30 d',d.failed_sign_ins.last_24h+' / '+d.failed_sign_ins.last_30d),stat('Active tokens / keys',d.tokens.active+' / '+d.tokens.signing_keys));box.appendChild(g);
  var rt=d.failing_routes||[];box.appendChild(el('h3',null,'Routes that fail most, 7 days'));
  if(!rt.length)box.appendChild(el('p',{'class':'muted small'},'No failed requests.'));
  rt.forEach(function(r){var k=el('div',{'class':'kv'});add(k,el('span',null,r.route+(r.error?' · '+r.error:'')),el('span',null,r.failed+'×'));box.appendChild(k)});
  var f=d.failed_sign_ins.recent||[];box.appendChild(el('h3',null,'Failed sign-ins'));box.lastChild.style.marginTop='12px';
  if(!f.length)box.appendChild(el('p',{'class':'muted small'},'None in the last 30 days.'));
  f.forEach(function(r){var k=el('div',{'class':'kv'});add(k,el('span',null,r.at+' · '+r.how),el('span',null,r.address));box.appendChild(k)});
  if(!full){var o=el('button',{'class':'btn small'},'Open account');o.style.marginTop='10px';o.onclick=function(){openAccount(id)};box.appendChild(o);
    var e=el('button',{'class':'btn small'},'Its errors');e.style.cssText='margin:10px 0 0 8px';e.onclick=function(){tab='logs';logSub='errors';errAccount=id;document.querySelector('#tabs [data-tab=logs]').click()};box.appendChild(e)}}

// ---------- logs: Changes, Reports, Errors
var logSub='changes',errAccount=0;
function showLogs(){M.textContent='';var sub=el('div',{'class':'sub'});[['changes','Changes'],['reports','Reports'],['errors','Errors']].forEach(function(o){var b=el('button',{'aria-pressed':String(o[0]===logSub)},o[1]);b.onclick=function(){logSub=o[0];if(o[0]!=='errors')errAccount=0;showLogs()};sub.appendChild(b)});M.appendChild(sub);
  var body=el('div');M.appendChild(body);if(logSub==='changes')showHistoryIn(body);else if(logSub==='reports')showReports(body);else showErrors(body)}
function showReports(box){var st='open';var sel=el('select');[['open','Open'],['resolved','Resolved'],['closed','Closed'],['all','All']].forEach(function(o){sel.appendChild(el('option',{value:o[0]},o[1]))});
  var s=el('div',{'class':'search'});var i=el('input',{type:'search',placeholder:'Search reports'});add(s,i,sel);box.appendChild(s);var list=el('div',{'class':'card'});box.appendChild(list);
  function load(){api('GET','/api/admin/reports?status='+sel.value+'&q='+encodeURIComponent(i.value)).then(function(j){list.textContent='';
    if(!j.reports.length){list.appendChild(el('p',{'class':'muted'},'No reports here.'));return}
    j.reports.forEach(function(r){var d=el('div',{'class':'rep'});var t=el('b',null,r.title);t.appendChild(el('span',{'class':'st '+(r.status==='open'?'bad':'ok')},r.status));d.appendChild(t);
      d.appendChild(el('div',{'class':'muted small'},r.created_at+' · '+(r.account||'unknown account')+(r.campaign?' · '+r.campaign:'')+(r.character?' · '+r.character:'')));
      d.appendChild(el('pre',null,r.body));var row=el('div',{'class':'row'});
      [['resolved','Resolve'],['closed','Close'],['open','Reopen']].filter(function(o){return o[0]!==r.status}).forEach(function(o){var b=el('button',{'class':'btn small'},o[1]);
        b.onclick=function(){api('POST','/api/admin/reports/'+r.id,{status:o[0]}).then(function(){toast('Report '+r.id+': '+o[0]);load()}).catch(fail)};row.appendChild(b)});d.appendChild(row);list.appendChild(d)})}).catch(fail)}
  var t;i.oninput=function(){clearTimeout(t);t=setTimeout(load,300)};sel.onchange=load;load()}
function showErrors(box){var sel=el('select');[['400','All failed requests'],['500','Server errors only']].forEach(function(o){sel.appendChild(el('option',{value:o[0]},o[1]))});
  var s=el('div',{'class':'search'});var i=el('input',{type:'search',placeholder:'Part of a route'});add(s,i,sel);box.appendChild(s);
  if(errAccount){var chip=el('div',{'class':'chips'});var c=el('span',{'class':'chip'},'Account '+errAccount);var x=el('button',{'aria-label':'Show every account'},'×');x.onclick=function(){errAccount=0;showLogs()};c.appendChild(x);chip.appendChild(c);box.appendChild(chip)}
  var list=el('div',{'class':'card'});box.appendChild(list);
  function load(){api('GET','/api/admin/errors?min_status='+sel.value+'&q='+encodeURIComponent(i.value)+(errAccount?'&account_id='+errAccount:'')).then(function(j){list.textContent='';
    if(!j.errors.length){list.appendChild(el('p',{'class':'muted'},'No failed requests.'));return}
    j.errors.forEach(function(e){var d=el('div',{'class':'hist'});var t=el('div',null,e.method+' '+e.route);t.appendChild(el('span',{'class':'st '+(e.status>=500?'bad':'')},e.status+(e.error?' '+e.error:'')));
      add(d,t,el('div',{'class':'muted small'},e.at+' · '+(e.account||'no account')+(e.campaign_id?' · campaign '+e.campaign_id:'')+(e.character_id?' · character '+e.character_id:'')));list.appendChild(d)})}).catch(fail)}
  var t;i.oninput=function(){clearTimeout(t);t=setTimeout(load,300)};sel.onchange=load;load()}

// ---------- settings: the running server, read only
function showSettings(){M.textContent='';api('GET','/api/admin/settings').then(function(j){
  function card(title,rows){var c=el('div',{'class':'card'});c.appendChild(el('h3',null,title));rows.forEach(function(r){var k=el('div',{'class':'kv'});add(k,el('span',null,r[0]),el('span',null,r[1]));c.appendChild(k)});M.appendChild(c)}
  var s=j.server,d=j.database,c=j.counts,cs=c.campaigns_by_status||{};
  card('Server',[['Version',s.version],['Built',s.build_time],['Started',s.started_at],['Go',s.go],['Address',s.public_address||'not set'],['Hosting',s.on_render?'Render':'other'],['Test clock',s.test_clock?'on':'off']]);
  card('Database',[['Status',d.ok?'answering':'NOT ANSWERING'],['Round trip',d.ping_ms+' ms'],['Spells loaded',d.spells]]);
  card('Services',[['Mail',j.mail.status],['Admin page',j.admin.page_on?'on':'off']]);
  card('Counts',[['Accounts',c.accounts+(c.accounts_deleted?' (+'+c.accounts_deleted+' deleted)':'')],['Characters',c.characters],['Campaigns',Object.keys(cs).map(function(k){return cs[k]+' '+k}).join(', ')||'none'],['Open reports',c.open_reports],
    ['Failed requests, 24 h',c.errors_24h+(c.server_errors_24h?' ('+c.server_errors_24h+' server errors)':'')],['Failed sign-ins, 24 h',c.failed_sign_ins_24h]]);
  var u=j.usage_baseline;if(u){var pg=u.refusals_players,gm=u.refusals_gms,gr=u.guessed_routes,jf=u.join_to_first_action_minutes,sz=u.reply_sizes||{},mt=sz['/api/my-turn']||{},hb=sz['/api/heartbeat']||{},dc=u.declares;
    function n(x){return x==null?'none':Math.round(x)}
    card('Usage baseline ('+u.version+', taken '+u.taken_at+')',[['Logs from',u.api_logs_from+' ('+u.api_log_rows+' rows)'],['Players: refused per 100 writes',pg.refused_per_100+' ('+pg.refused+' of '+pg.writes+')'],['GMs: refused per 100 writes',gm.refused_per_100+' ('+gm.refused+' of '+gm.writes+')'],['Guessed routes (404 / 405)',gr.not_found+' / '+gr.wrong_method+' over '+gr.days+' days'],['Join to first action, minutes',jf.characters+' characters, median '+n(jf.median)+', 95th '+n(jf.p95)],['my-turn bytes',mt.reads+' reads, median '+n(mt.median_bytes)+', 95th '+n(mt.p95_bytes)],['Heartbeat bytes',hb.reads+' reads, median '+n(hb.median_bytes)+', 95th '+n(hb.p95_bytes)],['Declares',dc.declares+' of '+dc.actions+' actions ('+dc.share_pct+'%)']]);
    card('Most common refusals',(u.top_refusals||[]).map(function(x){return [x.status+' '+x.error,x.route+' × '+x.count]}));
    M.appendChild(el('p',{'class':'muted small'},u.gaps))}
  var r=el('button',{'class':'btn'},'Refresh');r.onclick=showSettings;M.appendChild(r)}).catch(fail)}

// ---------- history (the Changes of Logs)
var LABEL={hp:'Hit Points',max_hp:'Max Hit Points',temp_hp:'Temporary HP',ac:'Armor Class',xp:'Experience',agent_id:'Player (account)',lobby_id:'Campaign',dm_id:'GM (account)',intl:'Intelligence',str:'Strength',dex:'Dexterity',con:'Constitution',wis:'Wisdom',cha:'Charisma',
  exhaustion_level:'Exhaustion',is_dead:'Dead',spell_slots_used:'Used spell slots',max_players:'Max players',min_level:'Lowest level',max_level:'Highest level'};
function showHistoryIn(M){var box=el('div',{'class':'card'});M.appendChild(box);api('GET','/api/admin/changes?limit=200').then(function(j){
  if(!j.changes.length){box.appendChild(el('p',{'class':'muted'},'No admin changes yet.'));return}
  j.changes.forEach(function(x){var d=el('div',{'class':'hist'});var who=x.name||(({character:'Character ',account:'Account '}[x.kind]||'Campaign ')+x.row_id);var what;
    if(x.field==='token')what=cap(x.new);else if(x.field==='deleted')what='Deleted';else if(x.field==='restored')what='Restored';else what=(LABEL[x.field]||nice(x.field))+': '+diff(x.old,x.new);
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
fetch('/api/admin/session',{credentials:'same-origin'}).then(function(r){return r.json()}).then(function(j){j.signed_in?start():login()}).catch(login);
})();
</script></body></html>`

// adminThemeCSS: the site's themes on the admin page (owner, Nov 23: the theme chosen on the test server is a reminder of which server it is, and the
// admin page ignored it). The page reads the same saved choice as the site (themeHeadScript, same origin); the site's own theme blocks
// give each theme's variables and the page's few colors are named from them, with the page's own colors where a legacy theme lacks one.
func adminThemeCSS() string {
	var b strings.Builder
	for _, t := range siteThemes {
		b.WriteString(themeBlock(fmt.Sprintf(`html[data-theme="%s"]`, t.Key), t.Vars))
	}
	b.WriteString(`html[data-theme]{--bg:var(--bg-app,#f4f1ea);--card:var(--bg-surface,#fff);--ink:var(--text-primary,#1f1c18);--muted:var(--text-secondary,#6f695f);` +
		`--line:var(--border-subtle,#e2dccf);--accent:var(--accent-gold,#8a4a1c);--accent-ink:var(--btn-accent-text,#fff);--warn:var(--accent-crimson,#b0291c);` +
		`--ok:var(--accent-emerald,#2f6e33);--chip:var(--bg-elevated,#efe8da);--chg:var(--accent-gold-soft,#fff4cc)}` + "\n")
	return b.String()
}
