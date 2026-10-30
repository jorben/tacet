// @ts-nocheck

// @generated-from docs/v1/tacet-prototype.html (script section: home); maintained by hand after the port.
import { $, esc, fmt, pick, I, fileIcon, HOME_ICON, HOME_FLOW, rt } from './runtime';
import { buildDoc } from '../domain/data';
import { S } from './state';
import { batchStatus, vManifest, vResult, newBatch, settled, tick } from './batch';
import { extOf, singleManifest, vDone, newSingle } from './single';
import { render } from './shell';
import { homeModelGuide, presetModelId, needModel } from './models';
import { toast, modal, closeModal } from './layer';

export function liveHistory(){
 const out=[];const B=S.batch;const s=S.single&&S.single.from==='batch'?(S.behind&&S.behind.s):S.single;
 if(B){const[st,stl]=batchStatus();out.push({k:'batch',name:B.title,meta:fmt(B.n)+' 份',st,stl,when:'刚刚',live:'batch'})}
 if(s&&s.phase!=='done')out.push({k:'file',ext:s.ext,name:s.name,meta:s.pages.length+' 页',st:s.phase==='review'?'warn':'run',stl:s.phase==='review'?'待确认':'处理中',when:'刚刚',live:'single'});
 return out.concat(S.history);
}
/* Completed history is viewed separately so active tasks keep their slots and timers. */
export function openHistoryResult(x){
 let data=x.live==='batch'?S.batch:x.saved;
 if(!data){
  if(x.k==='file')data=Object.assign({name:x.name,ext:x.ext||extOf(x.name),out:'桌面 / 已脱敏',preset:'通用办公脱敏',phase:'done'},buildDoc());
  else{const n=parseInt(x.meta.replace(/,/g,''))||0;data={title:x.name,n,out:'桌面 / 已脱敏',phase:'result',files:Array.from({length:n},(_,i)=>({i,id:'T'+String(i+1).padStart(4,'0'),ext:'pdf',st:'done'}))}}
  x.saved=data;
 }
 S.historyResult={entry:x,data,phase:'result'};S.navStack=[];S.view='history';render();
}
export function vHistoryResult(){
 const r=S.historyResult;
 return r.phase==='manifest'?vManifest(r.entry.k==='file'?singleManifest(r.data):r.data):r.entry.k==='file'?vDone(r.data):vResult(r.data);
}
export function historyRows(h){
 return h.map((x,i)=>'<button class="row hrow" data-a="openHist" data-v="'+i+'">'+
  (x.k==='batch'?'<span class="ficon">'+I('layers',25)+'</span>':fileIcon(x.ext||'pdf'))+
  '<span class="hx"><span class="hn" style="display:block">'+esc(x.name)+'</span><span class="rs" style="display:block">'+esc(x.meta)+' · '+esc(x.when)+'</span></span>'+
  '<span class="pill '+({done:'ok',warn:'warn',run:'pri',keep:'ghost',idle:'pri',stop:'ghost',part:'ghost'}[x.st]||'ghost')+'">'+(x.st==='run'?'<span class="dot"></span>':'')+esc(x.stl)+'</span>'+I('chev-r',18).replace('class="i"','class="i history-chevron"')+'</button>').join('');
}
export function vHome(){
 if(!S.presets.some(p=>p.name===S.homePreset))S.homePreset=S.presets[0].name;
 const h=liveHistory();
 return '<div class="home-flow" aria-hidden="true"><img src="'+HOME_FLOW+'" width="2070" height="365" alt="" draggable="false"></div><div class="page home-page">'+
  '<aside class="home-note" aria-label="敏感信息，止于此处。本地处理，守护更安心。"><strong class="home-note-title">敏感信息，<br>止于此处。</strong><span class="home-note-caption">本地处理，守护更安心。</span></aside>'+
  '<div class="page-inner home-inner"><header class="home-brand"><img class="home-brand-icon" src="'+HOME_ICON+'" width="80" height="80" alt="Tacet 幼苗角色 Logo"><h1>Tacet</h1><p>该静默处，自有分寸</p></header>'+
  homeModelGuide()+'<div class="drop-wrap" id="drop"><button class="drop '+(h.length?'sm':'')+'" data-a="pick" aria-label="选择待脱敏文档，或将文档拖入此处"><span class="drop-g" aria-hidden="true">'+I('file',56,1.5)+'<span class="drop-plus">'+I('plus',23,1.75)+'</span></span><span class="drop-t">拖入文档</span><span class="drop-s"><span>或点击选择 ·</span><span class="drop-formats"><span>DOCX</span><span>PDF</span><span>TXT</span><span>CSV</span><span>JSONL</span></span></span></button>'+
  '<button class="sel drop-preset" data-a="homePreset" aria-label="脱敏方案：'+esc(S.homePreset)+'">'+I('layers',18)+'<span>'+esc(S.homePreset)+'</span>'+I('chev-d',15)+'</button></div>'+
  (h.length?'<section aria-labelledby="recentTitle"><div class="recent-head"><h2 id="recentTitle">最近</h2><button class="history-all" data-a="allHistory">查看全部 '+I('chev-r',16)+'</button></div><div class="list home-history">'+historyRows(h.slice(0,3))+'</div></section>':'')+
 '</div></div>';
}
export function bindDrop(){
 const d=$('#drop');if(!d)return;const z=d.querySelector('.drop');
 ['dragenter','dragover'].forEach(e=>d.addEventListener(e,ev=>{ev.preventDefault();z.classList.add('over')}));
 d.addEventListener('dragleave',ev=>{ev.preventDefault();if(!d.contains(ev.relatedTarget))z.classList.remove('over')});
 d.addEventListener('drop',ev=>{ev.preventDefault();z.classList.remove('over');takeFiles([...ev.dataTransfer.files].map(f=>({name:f.name,size:f.size})))});
}

export function takeFiles(fs){
 if(!fs.length)return;
 if(S.modelPending){toast('请先继续或取消已选择的操作，再选择其他文档');return}
 const preset=S.homePreset,modelId=presetModelId(preset);
 if(fs.length===1){const go=()=>needSlot('single',()=>newSingle(fs[0].name,fs[0].size,{preset,modelId}));if(needModel(preset,go,fs[0].name,modelId))go()}else needSlot('batch',()=>newBatch(fs.map(f=>f.name)));
}
/* one single + one batch at a time; an unfinished task is never replaced silently */
export function slotBusy(kind){
 if(kind==='single'){const s=S.single&&S.single.from==='batch'?(S.behind&&S.behind.s):S.single;return s&&s.phase!=='done'?s:null}
 const B=S.batch;return B&&!(['result','manifest'].includes(B.phase)&&settled())?B:null;
}
export function needSlot(kind,go){
 const cur=slotBusy(kind);
 if(!cur){if(kind==='batch'&&S.batch)archiveBatch();go();return}
 S.slotKind=kind;S.slotGo=go;
 if(kind==='single'){
  if(cur.phase!=='review'){toast('「'+esc(cur.name)+'」正在处理，请稍候');return}
  modal('<h3>「'+esc(cur.name)+'」还未导出</h3><div class="ms">保存到“最近”，稍后可继续确认</div><div class="mf"><button class="btn btn-g" data-a="closeModal" style="margin-right:auto">取消</button><button class="btn btn-s" data-a="slotView">返回该文件</button><button class="btn btn-p" data-a="slotPark">保存并继续</button></div>');
  return;
 }
 const pre=cur.phase==='precheck',d=keepDays(cur);
 modal('<h3>'+(pre?'当前批次还未开始':'当前批次还在进行')+'</h3><div class="ms">「'+esc(cur.title)+'」· '+batchStatus()[1]+'</div><div class="mb">'+(pre?'替换后，当前批次的设置不会保留。':'当前批次将暂停并加密保存 '+d+' 天，可在“最近”中继续。')+'</div><div class="mf"><button class="btn btn-g" data-a="closeModal" style="margin-right:auto">取消</button><button class="btn btn-s" data-a="slotView">查看当前批次</button><button class="btn btn-p" data-a="slotPark">'+(pre?'替换':'保存并继续')+'</button></div>');
}
export function keepDays(B){return B&&B.keep!=='0'?B.keep:'7'}
export function parkSingle(){const s=S.single;S.history.unshift({k:'file',ext:s.ext,name:s.name,meta:s.pages.length+' 页',st:'warn',stl:'待确认',when:'刚刚',saved:s,undo:S.undo,park:true});S.single=null;S.undo=[]}
export function parkBatch(d){const B=S.batch;clearInterval(rt.bT);B.paused=true;S.history.unshift({k:'batch',name:B.title,meta:fmt(B.n)+' 份',st:'keep',stl:'已保存 · '+d+' 天后清理',when:'刚刚',saved:B});S.batch=null}
export function archiveBatch(){const B=S.batch;const[st,stl]=batchStatus();clearInterval(rt.bT);S.history.unshift({k:'batch',name:B.title,meta:fmt(B.n)+' 份',st,stl,when:'刚刚',saved:B});S.batch=null}
export function resumeBatch(x){if(!needModel(x.saved.preset,()=>resumeBatch(x),x.name,x.saved.modelId))return;S.history=S.history.filter(h=>h!==x);S.batch=x.saved;const B=S.batch;B.paused=false;clearInterval(rt.bT);if(B.phase!=='precheck')rt.bT=setInterval(tick,240);S.view='batch';render()}

