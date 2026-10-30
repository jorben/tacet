// @ts-nocheck

// @generated-from docs/v1/tacet-prototype.html (script section: batch); maintained by hand after the port.
import { $, esc, fmt, ri, pick, rng, I, LOGO, rt } from './runtime';
import { TYPES, OFFICE_CATS, MODEL_LABELS, SUR, GIV, KIND, GDEF, STL } from '../domain/data';
import { extOf, newSingle } from './single';
import { S } from './state';
import { presetModelId, needModel, presetNeedsModel, modelBusy, modelCard, modelById, getPreset } from './models';
import { render, railHtml } from './shell';
import { patchRv, vRv, rvLoc } from './review';
import { closePop, toast, drawer, closeModal } from './layer';
import { vHome } from './home';
import { fmtConf, presetConf } from './presets';

export function newBatch(names,opt={}){
 rt.R=rng(names.length*7+3);
 const n=names.length;const OKX=['pdf','docx','txt','md','log','csv','jsonl','json'];const files=names.map((nm,i)=>{
  const ext=extOf(nm);
  return{st0:OKX.includes(ext)?'queue':'bad',i,id:'T'+String(i+1).padStart(4,'0'),name:nm,ext,pages:ri(1,36),h:{org:ri(1,12),name:ri(4,40),phone:ri(1,14),email:ri(0,6),id:ri(0,4),addr:ri(0,7),card:ri(0,2),date:ri(1,10)},imgs:ri(0,5),st:'queue',t:0,g:new Set(),late:new Set(),det:false,partial:false,man:false};
 });
 // precheck failures
 files.forEach(f=>{if(f.st0==='bad'){f.st='bad';f.reason='格式不支持'}});
 if(n>=20){const f=files[ri(0,n-1)];if(f.st!=='bad'){f.st='bad';f.reason='文件已损坏'}}
 // over the page limit: rejected at precheck with the actual count, no split in MVP
 if(n>=20){const c=files.filter(f=>f.st!=='bad'&&f.ext==='pdf');if(c.length){const f=c[ri(0,c.length-1)];f.pages=2340;f.st='bad';f.reason='超过 2,000 页 · 实际 2,340 页'}}
 const groups=[];
 const avail=files.filter(f=>f.st!=='bad');
 GDEF.forEach(d=>{
  if(d.after){groups.push(Object.assign({},d,{per:{},done:[]}));return}
  if(n<10&&!['shot','idck'].includes(d.id))return;
  const pool=d.ext?avail.filter(f=>f.ext===d.ext):avail;if(!pool.length)return;
  const k=Math.max(d.min||1,Math.round(n*d.frac));const mem={};
  for(let j=0;j<k&&j<avail.length*2;j++){const f=pick(pool);if(mem[f.i]!=null)continue;if(d.unit==='份'&&Object.values(mem).length&&groups.some(g=>g.unit==='份'&&g.per[f.i]!=null))continue;mem[f.i]=d.per?ri(d.per[0],d.per[1]):1}
  Object.keys(mem).forEach(fi=>{(d.late?files[fi].late:files[fi].g).add(d.id)});
  groups.push(Object.assign({},d,{per:mem,done:[]}));
 });
 // new hits found by output validation join their category group
 if(n>=10)groups.filter(g=>g.post).forEach(g=>{g.found={};const c=avail.filter(f=>g.per[f.i]==null&&!f.late.size);for(let j=0;j<Math.max(1,Math.round(n*g.post))&&c.length;j++){const f=c.splice(ri(0,c.length-1),1)[0];g.per[f.i]=1;g.found[f.i]=true;f.late.add(g.id)}});
 // validation outcomes: not completed → auto re-run; residue → auto widen; broken output → whole-file failure
 if(n>=20){const c=avail.filter(f=>!f.late.size);const take=fn=>{const xs=c.filter(fn);if(!xs.length)return null;const f=xs[ri(0,xs.length-1)];c.splice(c.indexOf(f),1);return f};
  [1,1,3].forEach(v=>{const f=take(f=>!f.g.size);if(f)f.vr=v});
  for(let j=0;j<2;j++){const f=take(()=>true);if(f){f.res=true;f.resN=ri(1,2)}}
  const b=take(f=>f.ext==='docx');if(b)b.brk=true}
 S.batch=Object.assign({title:opt.title||'新批次',n,files,groups,phase:'precheck',mode:'guided',preset:S.homePreset,modelId:presetModelId(S.homePreset),imgAct:'remove',out:'桌面 / 已脱敏',autoPub:true,keep:'7',paused:false,tab:'todo',ff:'all',q:'',dsel:new Set(),live:{}},opt);
 S.view='batch';render();
}
export function demoNames(n){rt.R=rng(99);return Array.from({length:n},(_,i)=>{const[k,e]=pick(KIND);return k.replace('{n}',pick(SUR)+pick(GIV)).replace('{k}',String(100+i))+'.'+(i===37?'pages':e)})}
export function counts(B=S.batch){
 const c={queue:0,proc:0,wait:0,held:0,ready:0,done:0,partial:0,bad:0,skip:0};
 B.files.forEach(f=>{c[{queue:'queue',proc:'proc',wait:'wait',held:'held',ready:'ready',done:'done',partial:'partial',bad:'bad',skipped:'skip'}[f.st]]++});
 return c;
}
export function gMembers(g,detOnly=true){return Object.keys(g.per).map(Number).filter(fi=>{const f=S.batch.files[fi];return f.g.has(g.id)&&f.st!=='skipped'&&(!detOnly||f.det)})}
export function openGroups(){if(!S.batch)return[];return S.batch.groups.filter(g=>gMembers(g).length)}
export function gTotal(g,mem){return mem.reduce((a,fi)=>a+g.per[fi],0)}
export function startBatch(){
 const B=S.batch;if(!needModel(B.preset,()=>startBatch(),B.title+' · '+B.n+' 份',B.modelId))return;B.phase='run';if(B.mode==='auto')B.autoPub=true;
 B.files.forEach(f=>{if(f.st==='bad')f.det=true});
 // strict: every file waits for an explicit per-file confirmation, never bulk-confirmable (BUX-08)
 if(B.mode==='strict'&&!B.groups.some(g=>g.id==='strict')){const g={id:'strict',ic:'eye',t:'逐份确认',unit:'份',noBulk:true,per:{},done:[],acts:[{l:'确认导出',k:'ok'},{l:'跳过此文件',k:'skip',n:'不导出'}]};B.files.forEach(f=>{if(f.st!=='bad'){g.per[f.i]=1;f.g.add('strict')}});B.groups.push(g)}
 render();clearInterval(rt.bT);rt.bT=setInterval(tick,240);
}
export function tick(){
 const B=S.batch;if(!B||B.phase==='precheck')return;
 B.files.forEach(f=>{
  if(f.st==='proc'){f.t--;if(f.t<=0)finishProc(f)}
  else if(f.st==='ready'&&B.autoPub&&f.rt!=null){f.rt--;if(f.rt<=0)f.st=f.partial?'partial':'done'}
 });
 if(!B.paused){let cap=10-B.files.filter(f=>f.st==='proc').length;for(const f of B.files){if(cap<=0)break;if(f.st==='queue'){f.st='proc';f.t=ri(2,6);cap--}}}
 if(['run','review'].includes(B.phase)&&settled()){
  // keep the review-complete card readable: at least 1.2 s, and 0.8 s after everything has finished
  const rv=B.rv;if(B.phase==='review'&&S.view==='batch'&&rv&&rv.fin){const now=Date.now();rv.setAt=rv.setAt||now;if(now-rv.finAt<1200||now-rv.setAt<800){patchRv();return}}
  B.phase='result';if(S.view==='batch'){closePop();render()}else $('#rail').innerHTML=railHtml();return}
 if(S.view==='batch'&&B.phase==='run')patchBatch();
 else if(S.view==='batch'&&B.phase==='review')patchRv();
 else if(S.view==='home'&&!$('#layer .pop')){const h=vHome();if(h!==S._lastHome){S._lastHome=h;render()}}
 else $('#rail').innerHTML=railHtml();
}
export function machineDone(){const c=counts();return !c.queue&&!c.proc&&!c.wait&&!c.ready}
export function settled(){const c=counts();return !c.queue&&!c.proc&&!c.wait&&!c.ready&&!(S.batch.phase==='review'&&c.held)}
export function pendItems(){return openGroups().reduce((a,g)=>a+gTotal(g,gMembers(g)),0)}
export function badgeHtml(){const p=pendItems();return p?'<span class="badge">'+fmt(p)+'</span>':''}
/* one status vocabulary for home, rail dot and guards: [kind, label] */
export function batchStatus(){
 const B=S.batch,c=counts(),dn=c.done+c.partial,N=fmt(B.n);
 if(B.phase==='precheck')return['idle','待开始'];
 if(!settled()){
  const p=pendItems();
  if(p&&B.mode!=='auto')return['warn','待你处理 '+fmt(p)+' 处'];
  if(c.queue+c.proc)return['run',(B.paused?'已暂停':'处理中')+' · '+fmt(dn)+' / '+N];
  if(c.ready&&!B.autoPub)return['warn','待导出 '+fmt(c.ready)+' 份'];
  if(p)return['warn','待你处理 '+fmt(p)+' 处'];
  return['run','处理中 · '+fmt(dn)+' / '+N];
 }
 if(c.held)return['warn','已导出 '+fmt(dn)+' / '+N+' · 待你处理 '+fmt(c.held)+' 份'];
 return dn===B.n?['done','已导出 '+N]:['part','已导出 '+fmt(dn)+' / '+N];
}
export function finishProc(f){
 const B=S.batch;f.det=true;if(f.tmp){f.reason=null;f.tmp=false}
 if(f.late.size&&!f.g.size){f.late.forEach(x=>f.g.add(x));f.late.clear()}
 if(f.g.size){f.st=B.mode==='auto'?'held':'wait';return}
 // validation not completed: re-run automatically within budget, never queued for review
 if(f.vr){f.vr--;f.vn=(f.vn||0)+1;if(f.vn>2){f.vr=0;toFail(f,'验证超时，已自动重试 2 次');return}busy(f,'重新验证');return}
 // residue under an approved 涂抹: widen within the same text unit, no human decision
 if(f.res){f.res=false;B.widened=(B.widened||0)+f.resN;busy(f,'扩大涂抹范围');return}
 // output cannot be reopened: whole-file failure → 重新处理 / 跳过; failing again → 无法处理
 if(f.brk){if(f.redo){f.st='bad';f.reason='重新处理后仍失败';return}toFail(f,'导出文件无法打开 · 页眉 2 结构无效');return}
 f.st='ready';f.rt=B.autoPub?ri(1,3):null;
}
export function busy(f,why){f.st='proc';f.t=ri(6,10);f.reason=why;f.tmp=true}
export function toFail(f,why){f.failWhy=why;S.batch.groups.find(g=>g.id==='fail').per[f.i]=1;f.g.add('fail');f.st=S.batch.mode==='auto'?'held':'wait'}
export function resolve(gid,k,only){
 const B=S.batch,g=B.groups.find(x=>x.id===gid);const mem=only||gMembers(g);if(!mem.length)return;
 const act=k==='pwd'?{l:'已解锁'}:(g.acts.find(a=>a.k===k)||{l:'处理'});
 const snap=mem.map(fi=>{const f=B.files[fi];return[fi,f.st,new Set(f.g),f.partial,f.t]});
 mem.forEach(fi=>{const f=B.files[fi];f.g.delete(gid);f.man=true;
  if(k==='skip'){f.st='skipped';f.g.clear();return}
  if(k==='keep'&&g.gap)f.partial=true;
  if(k==='redo')f.redo=true;
  if(!f.g.size){f.st='proc';f.t=ri(14,22)}});
 g.done.push({k,l:act.l,n:gTotal(g,mem),files:mem.length,mem});
 B.dsel.clear();
 const rec=g.done[g.done.length-1];
 patchBatch(true);
 toast(act.l+' · '+fmt(rec.n)+' '+g.unit,()=>{
  if(mem.some(fi=>['done','partial'].includes(B.files[fi].st))){toast('已导出的文件不能撤销');return}
  snap.forEach(([fi,st,gs,p,t])=>Object.assign(B.files[fi],{st,g:gs,partial:p,t}));
  g.done=g.done.filter(x=>x!==rec);patchBatch(true);toast('已撤销');
 });
}
export function etaStr(c){const r=c.queue+c.proc;if(!r)return'';const lo=Math.max(1,Math.round(r*.9/60)),hi=Math.max(lo+1,Math.round(r*1.6/60));return'剩余约 '+lo+'–'+hi+' 分钟'}
export function stats(){
 const B=S.batch,c=counts(),N=B.n,dn=c.done+c.partial;
 const seg=(n,col,lbl)=>n?'<i style="width:'+(n/N*100)+'%;background:'+col+'" title="'+lbl+' '+n+'"></i>':'';
 const lg=(n,col,l)=>n?'<span><span class="dot" style="color:'+col+'"></span>'+l+' <b class="num">'+fmt(n)+'</b></span>':'';
 return '<div class="bar" aria-label="进度">'+seg(c.done,'var(--ok)','已导出')+seg(c.partial,'color-mix(in srgb,var(--ok) 55%,transparent)','部分结果')+seg(c.ready,'#6C8FC0','待导出')+seg(c.proc,'var(--primary)','处理中')+seg(c.wait+c.held,'var(--warn)','待你处理')+seg(c.bad,'var(--bad)','无法处理')+seg(c.skip,'var(--faint)','已跳过')+'</div>'+
  '<div class="legend">'+lg(c.done,'var(--ok)','已导出')+lg(c.partial,'color-mix(in srgb,var(--ok) 55%,transparent)','部分结果')+lg(c.ready,'#6C8FC0','待导出')+lg(c.proc,'var(--primary)','处理中')+lg(c.queue,'var(--line)','排队')+lg(c.wait+c.held,'var(--warn)','待你处理')+lg(c.bad,'var(--bad)','无法处理')+lg(c.skip,'var(--faint)','已跳过')+'</div>';
}
export function bigHead(){
 const B=S.batch,c=counts(),dn=c.done+c.partial;
 return '<div><div class="big num">'+fmt(dn)+'<span class="of"> / '+fmt(B.n)+'</span></div><div class="lbl">已导出</div></div>';
}
export function vBatch(){
 const B=S.batch;
 if(B.phase==='precheck')return vPrecheck();
 if(B.phase==='manifest')return vManifest();
 if(B.phase==='result')return vResult();
 if(B.phase==='review')return vRv();
 const og=openGroups();
 return '<div class="page" id="bpage" data-keep><div class="page-inner">'+
  '<div class="bhead"><div id="bbig">'+bigHead()+'</div><span class="sp"></span><span class="eta" id="beta">'+etaStr(counts())+'</span>'+
   '<button class="ib" data-a="pauseBatch" data-tip="'+(B.paused?'继续':'暂停派发')+'" aria-label="'+(B.paused?'继续':'暂停')+'">'+I(B.paused?'play':'pause',17)+'</button>'+
   '<button class="ib" data-a="stopBatch" data-tip="停止" aria-label="停止">'+I('stop',17)+'</button>'+
   '</div>'+
  '<div id="bstats">'+stats()+'</div>'+
  '<div class="tabs" role="tablist">'+
   '<button class="tab" role="tab" data-a="btab" data-v="todo" aria-selected="'+(B.tab==='todo')+'">待你处理<span id="tbadge">'+badgeHtml()+'</span></button>'+
   '<button class="tab" role="tab" data-a="btab" data-v="auto" aria-selected="'+(B.tab==='auto')+'">自动处理</button>'+
   '<button class="tab" role="tab" data-a="btab" data-v="files" aria-selected="'+(B.tab==='files')+'">文件</button>'+
  '</div>'+
  (B.tab==='files'?'<div class="ftools"><label class="search">'+I('search',15)+'<input id="fq" placeholder="搜索" value="'+esc(B.q)+'" data-in="fq" aria-label="搜索文件"></label><span id="fchips"></span></div>':'')+
  '<div id="btab">'+tabLive()+'</div>'+
 '</div></div>';
}
export function patchBatch(force){
 const B=S.batch;if(S.view!=='batch'||B.phase!=='run')return;
 const set=(id,h)=>{const e=document.getElementById(id);if(e&&(force||B.live[id]!==h)){e.innerHTML=h;B.live[id]=h}};
 set('bbig',bigHead());set('bstats',stats());set('beta',etaStr(counts()));
 set('tbadge',badgeHtml());
 if(B.tab==='files')set('fchips',fileChips());
 set('btab',tabLive());
 $('#rail').innerHTML=railHtml();
}
export function tabLive(){const B=S.batch;return B.tab==='todo'?todoTab():B.tab==='auto'?autoTab():filesTab()}
export function todoTab(){
 const B=S.batch,c=counts();const og=openGroups();
 let h='';
 if(og.length){const pend=og.reduce((a,g)=>a+gTotal(g,gMembers(g)),0);const pf=new Set(og.flatMap(g=>gMembers(g))).size;
  h+='<div class="hero"><div><div class="hn num">'+fmt(pend)+'<small>处待你处理</small></div><div class="hs num">'+og.length+' 类 · '+fmt(pf)+' 份文件'+(B.mode==='auto'?' · 已搁置，不阻塞其他文件':'')+'</div></div><span class="sp"></span><button class="btn btn-p btn-lg" data-a="review">开始复核</button></div>'}
 if(!B.autoPub&&c.ready)h+='<div class="banner info"><div><div class="bt">待导出 '+fmt(c.ready)+' 份</div><div class="bs">已通过验证，确认后写入导出位置</div></div><span class="sp"></span><button class="btn btn-p" data-a="expReady">导出 '+fmt(c.ready)+' 份</button></div>';
 h+=og.map(g=>{const mem=gMembers(g);const tot=gTotal(g,mem);
  return '<div class="group go '+(g.block?'block':'')+'" data-a="review" data-v="'+g.id+'" role="button" tabindex="0"><div class="gi">'+I(g.ic,18)+'</div><div class="gx"><div class="gt">'+g.t+'</div><div class="gm num">'+(g.unit==='份'?fmt(mem.length)+' 份':fmt(tot)+' '+g.unit+' · '+fmt(mem.length)+' 份')+(g.found&&mem.some(fi=>g.found[fi])?' · 含导出后发现':'')+'</div></div>'+
   '<div class="ga">'+(g.noBulk?'':'<button class="ib" data-a="gMenu" data-v="'+g.id+'" data-tip="整组处理" data-tr aria-label="整组处理">'+I('more',18)+'</button>')+'<span class="chev">'+I('chev-r',18)+'</span></div></div>'}).join('');
 if(!og.length&&!(!B.autoPub&&c.ready))h+='<div class="empty"><div class="g">'+LOGO(36)+'</div>暂时没有需要你处理的</div>';
 const dg=B.groups.filter(g=>g.done.length);
 if(dg.length)h+='<div class="sec-t">已处理</div>'+dg.map(g=>{const m={};g.done.forEach(d=>m[d.l]=(m[d.l]||0)+d.n);
  return '<div class="group done"><div class="gi">'+I('check',15,2.2)+'</div><div class="gx"><div class="gt">'+g.t+'</div></div><div class="ga"><span class="muted sm num">'+Object.entries(m).map(([l,n])=>l+' '+fmt(n)).join(' · ')+' '+g.unit+'</span></div></div>'}).join('');
 return h;
}
export function autoTab(){
 const B=S.batch;const det=B.files.filter(f=>f.det&&f.st!=='bad'&&f.st!=='skipped');
 const tot={};OFFICE_CATS.forEach(t=>tot[t]=det.reduce((a,f)=>a+f.h[t],0));
 const im=det.reduce((a,f)=>a+f.imgs,0);
 return '<div class="list">'+OFFICE_CATS.map(t=>'<div class="row" style="--c:var(--t-'+t+')"><span class="sw"></span><span class="rl">'+TYPES[t].l+'</span><span class="rv"><span class="num" style="min-width:60px;text-align:right">'+fmt(tot[t])+'</span><span class="muted sm" style="width:40px">涂抹</span><button class="btn btn-g" data-a="sample" data-v="'+t+'">抽查</button></span></div>').join('')+
  '<div class="row"><span class="ri">'+I('image',15)+'</span><span class="rl">图像</span><span class="rv"><span class="num" style="min-width:60px;text-align:right">'+fmt(im)+'</span><span class="muted sm" style="width:40px">涂抹</span><button class="btn btn-g" data-a="sample" data-v="img">抽查</button></span></div>'+
  (B.widened?'<div class="row"><span class="ri">'+I('eye',15)+'</span><div><div class="rl">导出后残留</div><div class="rs">已自动扩大涂抹范围</div></div><span class="rv"><span class="num" style="min-width:60px;text-align:right">'+fmt(B.widened)+'</span><span class="muted sm" style="width:40px">处</span><button class="btn btn-g" style="visibility:hidden" tabindex="-1" aria-hidden="true">抽查</button></span></div>':'')+'</div>';
}
export function fileFilter(f){const B=S.batch;const ff=B.ff;const okq=!B.q||f.name.includes(B.q)||f.id.includes(B.q.toUpperCase());
 const m=ff==='all'||(ff==='done'&&['done','partial'].includes(f.st))||(ff==='wait'&&['wait','held'].includes(f.st))||(ff==='bad'&&['bad','skipped'].includes(f.st))||(ff==='run'&&['queue','proc','ready'].includes(f.st));return okq&&m}
export function fileChips(){
 const B=S.batch,F=B.files;const c=k=>F.filter(f=>{const o=B.ff;B.ff=k;const r=fileFilter(f);B.ff=o;return r}).length;
 return [['all','全部'],['done','已导出'],['run','进行中'],['wait','待你处理'],['bad','未导出']].map(([k,l])=>'<button class="fchip" data-a="ff" data-v="'+k+'" aria-pressed="'+(B.ff===k)+'">'+l+' <span class="num">'+fmt(c(k))+'</span></button>').join('');
}
export function filesTab(){
 const B=S.batch;const rows=B.files.filter(fileFilter);const show=rows.slice(0,B.lim||60);
 setTimeout(()=>{const e=$('#fchips');if(e&&!e.innerHTML)e.innerHTML=fileChips()});
 return '<table class="tbl"><thead><tr><th>编号</th><th>文件</th><th class="r">页</th><th class="r">命中</th><th>状态</th><th></th></tr></thead><tbody>'+
  show.map(f=>{const[l,c]=STL[f.st];const hits=Object.values(f.h).reduce((a,b)=>a+b,0);
   return '<tr class="cl" data-a="openFile" data-v="'+f.i+'"><td class="mono muted">'+f.id+'</td><td class="nm">'+esc(f.name)+'</td><td class="r num muted">'+fmt(f.pages)+'</td><td class="r num">'+(f.det&&f.st!=='bad'?fmt(hits):'<span class="muted">—</span>')+'</td><td><span class="pill '+c+'">'+(f.st==='proc'?'<span class="dot"></span>':'')+l+(f.reason?' · '+f.reason:'')+'</span></td><td class="r" style="color:var(--faint)">'+I('chev-r',15)+'</td></tr>'}).join('')+
  '</tbody></table>'+(rows.length>show.length?'<div class="more"><button class="link" data-a="moreFiles">再显示 '+Math.min(60,rows.length-show.length)+' · 共 '+fmt(rows.length)+'</button></div>':'')+(rows.length?'':'<div class="empty">没有匹配的文件</div>');
}
export function vPrecheck(){
 const B=S.batch;const fm={};B.files.forEach(f=>{if(f.st!=='bad')fm[f.ext]=(fm[f.ext]||0)+1});
 const bad=B.files.filter(f=>f.st==='bad');const imF=B.files.filter(f=>f.imgs).length,imN=B.files.reduce((a,f)=>a+f.imgs,0);
 const modeTip={guided:'自动处理，例外集中给你',auto:'例外先搁置，结束后集中处理',strict:'每份文件由你确认后才导出'};
 return '<div class="page"><div class="page-inner">'+
  '<div class="pc-head"><div class="big num">'+fmt(B.n)+'<span class="unit">份</span></div><div class="chips">'+Object.entries(fm).map(([k,v])=>'<span class="pill">'+k.toUpperCase()+' '+fmt(v)+'</span>').join('')+(bad.length?'<button class="pill bad" data-a="showBad">'+I('alert',12,2)+'无法处理 '+bad.length+'</button>':'')+'</div></div>'+
  '<div class="list">'+
   '<div class="row"><span class="ri">'+I('sliders',17)+'</span><span class="rl">方式</span><span class="rv"><div class="seg">'+[['guided','引导'],['auto','无人值守'],['strict','严格']].map(([k,l])=>'<button data-a="bmode" data-v="'+k+'" aria-pressed="'+(B.mode===k)+'" data-tip="'+modeTip[k]+'">'+l+'</button>').join('')+'</div></span></div>'+
   '<div class="row"><span class="ri">'+I('layers',17)+'</span><span class="rl">方案</span><span class="rv"><button class="sel" data-a="bpreset">'+B.preset+I('chev-d',14)+'</button></span></div>'+
   '<div class="row"><span class="ri">'+I('image',17)+'</span><div><div class="rl">图像与嵌入</div><div class="rs num">'+fmt(imF)+' 份 · '+fmt(imN)+' 个</div></div><span class="rv"><button class="sel" data-a="bimg">'+(B.imgAct==='remove'?'涂抹':'保留 · 部分结果')+I('chev-d',14)+'</button></span></div>'+
   '<div class="row"><span class="ri">'+I('folder',17)+'</span><div><div class="rl">导出位置</div><div class="rs mono">T0001.pdf · T0002.docx …</div></div><span class="rv"><button class="sel" data-a="bout">'+B.out+I('chev-d',14)+'</button></span></div>'+
   '<div class="row"><span class="ri">'+I('send',17)+'</span><span class="rl">验证后自动导出</span><span class="rv"><button class="tg" role="switch" aria-checked="'+(B.autoPub||B.mode==='auto')+'" data-a="bauto" aria-label="验证后自动导出" '+(B.mode==='auto'?'disabled':'')+'></button></span></div>'+
   '<div class="row"><span class="ri">'+I('clock',17)+'</span><span class="rl">中断后可恢复</span><span class="rv"><button class="sel" data-a="bkeep">'+(B.keep==='0'?'不保留':'保留 '+B.keep+' 天')+I('chev-d',14)+'</button></span></div>'+
  '</div>'+
  (presetNeedsModel(B.preset)||modelBusy()?modelCard(false,B.modelId||presetModelId(B.preset)):'')+'<div class="pc-foot"><span class="muted sm" style="display:flex;align-items:center;gap:6px">'+I(presetNeedsModel(B.preset)&&modelById(B.modelId||presetModelId(B.preset))?.status!=='ready'?'cpu':'check',13,2.2)+(presetNeedsModel(B.preset)?esc(modelById(B.modelId||presetModelId(B.preset))?.name||'所选模型')+' '+(MODEL_LABELS[modelById(B.modelId||presetModelId(B.preset))?.status]||'不可用'):'仅规则 · 无需模型')+(presetNeedsModel(B.preset)?' · 置信度 ≥ '+fmtConf(presetConf(getPreset(B.preset))):'')+' · 需 '+(B.n*0.015).toFixed(1)+' GiB / 可用 86 GiB</span><span class="sp"></span><button class="btn btn-g" data-a="cancelBatch">取消</button><button class="btn btn-p btn-lg" data-a="startBatch" '+(S.modelPending?'disabled':'')+'>'+(presetNeedsModel(B.preset)&&modelById(B.modelId||presetModelId(B.preset))?.status!=='ready'?'准备模型后处理':'开始处理')+'</button></div>'+
 '</div></div>';
}
export function vResult(B=S.batch){
 const c=counts(B),out=c.done+c.partial,miss=B.n-out,all=!miss&&!c.partial;
 const st=[[c.done,'已导出'],[c.partial,'部分结果'],[c.held,'待你处理'],[c.skip,'已跳过'],[c.bad,'无法处理']].filter(([v],i)=>v||!i);
 return '<div class="center"><div class="card donecard">'+
  '<div class="done-ic '+(all?'':'warn')+'">'+I(all?'check':'alert',28,2.2)+'</div>'+
  '<h2>'+(all?'已全部导出':'已导出 '+fmt(out)+' / '+fmt(B.n))+'</h2>'+
  '<div class="fn">'+I('folder',13)+esc(B.out)+'</div>'+
  '<div class="stats">'+st.map(([v,l])=>'<div><b class="num">'+fmt(v)+'</b><span>'+l+'</span></div>').join('')+'</div>'+
  (miss?'<div class="muted sm" style="margin:-10px 0 22px">未导出 '+fmt(miss)+' 份'+(c.held?'，其中 '+fmt(c.held)+' 份待你处理 <button class="link" data-a="review">去处理</button>':'，原因见清单')+'</div>':'')+
  '<div class="acts"><button class="btn btn-p" data-a="reveal">'+I('folder',15)+'打开导出文件夹</button><button class="btn btn-s" data-a="bphase" data-v="manifest">'+I('list',15)+'查看清单</button></div>'+
  '<div class="acts" style="margin-top:8px"><button class="link" data-a="go" data-v="home">再处理一份</button></div>'+
  '<div class="foot-note" data-tip="自动识别有局限，不代表文档已无隐私" data-tt>'+I('info',12)+'按策略处理，未声明零遗漏</div>'+
 '</div></div>';
}
export function vManifest(B=S.batch){
 const c=counts(B);const rows=B.files.filter(f=>{const k=B.mf||'all';return k==='all'||(k==='ok'&&['done','partial'].includes(f.st))||(k==='no'&&!['done','partial'].includes(f.st))});
 const show=rows.slice(0,B.mlim||80);
 const no=B.n-c.done-c.partial;
 return '<div class="page" id="mpage" data-keep><div class="page-inner">'+
  '<div class="bhead"><div><div class="big num">'+fmt(c.done+c.partial)+'<span class="of"> / '+fmt(B.n)+'</span></div><div class="lbl">已导出'+(c.partial?' · 含部分结果 '+c.partial:'')+'</div></div><span class="sp"></span>'+
   '<button class="btn btn-s" data-a="reveal">'+I('folder',15)+'导出文件夹</button><button class="btn btn-s" data-a="fakeSave" data-v="manifest.json" style="margin-left:6px">'+I('download',15)+'保存清单</button></div>'+
  '<div class="ftools" style="margin-top:22px">'+[['all','全部',B.n],['ok','已导出',c.done+c.partial],['no','未导出',no]].map(([k,l,n])=>'<button class="fchip" data-a="mf" data-v="'+k+'" aria-pressed="'+((B.mf||'all')===k)+'">'+l+' <span class="num">'+fmt(n)+'</span></button>').join('')+'</div>'+
  '<table class="tbl"><thead><tr><th>编号</th><th>导出</th><th>状态</th><th>处理</th><th>SHA-256</th></tr></thead><tbody>'+
  show.map(f=>{const[l,cl]=STL[f.st];const ok=['done','partial'].includes(f.st);
   return '<tr><td class="mono">'+f.id+'</td><td class="mono">'+(ok?(f.outputName?esc(f.outputName):f.id+'.'+(f.ext==='pages'?'pdf':f.ext))+(f.ver>1?' <span class="tag">v'+f.ver+' · 替代旧版</span>':''):'<span class="muted">—</span>')+'</td><td><span class="pill '+cl+'">'+l+(f.reason?' · '+f.reason:'')+(['wait','held'].includes(f.st)?' · '+[...f.g].map(x=>B.groups.find(g=>g.id===x).t).join('、'):'')+'</span></td><td class="muted">'+(ok?(f.man?'自动 + 人工':'自动'):'')+'</td><td class="mono muted">'+(ok?hash(f.i):'')+'</td></tr>'}).join('')+
  '</tbody></table>'+(rows.length>show.length?'<div class="more"><button class="link" data-a="moreMan">再显示 80 · 共 '+fmt(rows.length)+'</button></div>':'')+
 '</div></div>';
}
export function hash(i){let x=(i+1)*2654435761>>>0;let s='';for(let k=0;k<3;k++){x=(x*1103515245+12345)>>>0;s+=x.toString(16).padStart(8,'0').slice(0,4)}return s+'…'}

export function sampleDrawer(t){
 const B=S.batch;rt.R=rng(t.length*31);const fs=B.files.filter(f=>f.det&&f.st!=='bad');
 const vals={org:['杭州••••科技有限公司','上海••••贸易有限公司','••••律师事务所'],name:['张*桐','李*轩','周**','王*远'],phone:['138 •••• 6624','0571-•••• 2200','186 •••• 0931'],email:['li••••@163.com','hr••••@corp.cn'],id:['3301 •••• •••• ••4827','4403 •••• •••• ••112X'],addr:['杭州市西湖区••••','上海市浦东新区••••'],card:['6222 •••• •••• 885'],date:['1992 年 3 月 ••','2026 年 1 月 ••'],img:['证件照','公章','截图']};
 const ctx={org:'单位：',name:'联系人：',phone:'电话：',email:'邮箱：',id:'身份证号：',addr:'住址：',card:'账户：',date:'出生日期：',img:''};
 const rows=Array.from({length:8},(_,j)=>{const f=pick(fs);return{f,v:pick(vals[t]),p:ri(1,f.pages)}});
 S.drawer={k:'sample',t};
 drawer('<div class="dh"><div class="gi" style="background:var(--surface-2);color:var(--ink-2)">'+(t==='img'?I('image',17):'<span class="sw" style="--c:var(--t-'+t+');width:12px;height:12px"></span>')+'</div><div><div class="t">抽查 · '+(t==='img'?'图像':TYPES[t].l)+'</div><div class="sm muted">随机 8 处 · 跨文件</div></div><span class="sp"></span><button class="ib" data-a="sampleMore" data-v="'+t+'" data-tip="换一批" aria-label="换一批">'+I('retry',16)+'</button><button class="ib" data-a="closeModal" aria-label="关闭">'+I('x',18)+'</button></div>'+
  '<div class="dl">'+rows.map((r,j)=>'<div class="drow" style="cursor:default">'+(t==='img'?'<span class="dthumb"><i></i></span>':'')+'<span class="dx"><div class="dn">'+esc(r.f.name)+'</div><div class="dc">'+rvLoc(r.f,{p:r.p,j})+' · '+ctx[t]+'<span class="mask" data-a="peekS">'+I('eye',12)+r.v+'</span></div></span><button class="btn btn-g" data-a="sampleOpen" data-v="'+r.f.i+'">查看</button></div>').join('')+'</div>'+
  '<div class="df"><span class="muted sm">抽查不代表逐项审阅 · 误判请打开文件修改</span><span class="sp"></span><button class="btn btn-s" data-a="closeModal">完成</button></div>');
}
export function openFileFromBatch(fi){
 const B=S.batch,f=B.files[fi];closeModal();
 if(f.st==='bad'){toast('无法打开 · '+f.reason);return}
 if(f.st==='skipped'){toast('已跳过 · 不会导出');return}
 if(!f.det||f.st==='queue'||f.st==='proc'){toast('尚未检测完成');return}
 if(S.single&&S.single.from!=='batch'&&!S.behind)S.behind={s:S.single,undo:S.undo};
 const opt={phase:'review',from:'batch',bfi:fi,preset:B.preset};
 if(f.sv)Object.assign(opt,JSON.parse(f.sv));
 newSingle(f.name,f.sz||(f.sz=ri(200000,6000000)),opt);
 if(!f.sv){S.single.imgs.forEach(i=>i.act=B.imgAct==='keep'?'keep':'remove');render()}
}
/* edits inside a batch file are written back; an exported file is re-exported and replaces the old version */
export function commitBatchFile(){
 const s=S.single;if(!s||s.from!=='batch'||!S.batch||!S.undo.length)return null;
 const B=S.batch,f=B.files[s.bfi];
 f.sv=JSON.stringify({hits:s.hits,pages:s.pages,imgs:s.imgs,catAct:s.catAct});f.man=true;
 if(!['done','partial','ready'].includes(f.st))return 'saved';
 const was=f.st!=='ready';if(was)f.ver=(f.ver||1)+1;
 busy(f,'按新决定重新导出');if(['result','manifest'].includes(B.phase))B.phase='run';
 return was?'reexp':'saved';
}
export function leaveBatchFile(){
 const s=S.single;if(!s||s.from!=='batch')return null;
 const r=commitBatchFile();const b=S.behind;S.single=b?b.s:null;S.undo=b?b.undo:[];S.behind=null;return r;
}



