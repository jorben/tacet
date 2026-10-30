// @ts-nocheck

// @generated-from docs/v1/tacet-prototype.html (script section: single); maintained by hand after the port.
import { $, $$, esc, I, fileIcon, rt } from './runtime';
import { TYPES, buildDoc, locStr } from '../domain/data';
import { S } from './state';
import { presetModelId, needModel } from './models';
import { render } from './shell';
import { vManifest, stats } from './batch';
import { confSum } from './presets';
import { openPop, closePop, toast } from './layer';

export function extOf(n){const m=/\.([a-z0-9]+)$/i.exec(n);return m?m[1].toLowerCase():'pdf'}
export function sizeStr(b){return b>1048576?(b/1048576).toFixed(1)+' MB':Math.max(1,Math.round(b/1024))+' KB'}
export function outKind(ext){return ext==='pdf'?{l:'PDF · 栅格化',tip:'不可搜索 · 无链接与表单 · 200 DPI',verb:'涂抹'}:ext==='docx'?{l:'DOCX · 保留版式',tip:'图片换为同尺寸占位图',verb:'涂抹'}:{l:ext.toUpperCase()+' · 同格式',tip:'保持编码与换行',verb:'涂抹'}}
export function newSingle(name,size,opt={}){
 const preset=opt.preset||S.homePreset;opt={modelId:presetModelId(preset),...opt};
 if((!opt.phase||opt.phase==='processing')&&!needModel(preset,()=>newSingle(name,size,opt),name,opt.modelId))return;
 const ext=extOf(name);
 S.single=Object.assign({name,ext,size:sizeStr(size||1887436),phase:'processing',stage:0,sp:0,mode:S.docMode||'orig',focus:-1,catAct:{},preset:S.homePreset,out:'桌面 / 已脱敏',sameAll:true,from:null,page:0},buildDoc(),opt);
 S.undo=[];S.view='single';render();
 if(S.single.phase==='processing')runSingleProc();
}
export function runSingleProc(){
 clearInterval(rt.procT);const s=S.single;
 rt.procT=setInterval(()=>{
  if(S.single!==s&&!(S.behind&&S.behind.s===s)){clearInterval(rt.procT);return}
  s.sp+=s.stage===0?9:4;
  if(s.sp>=100){s.sp=0;s.stage++;if(s.stage>=2){clearInterval(rt.procT);s.phase='review';if(S.view==='single')render();return}}
  if(S.view==='single')patchProc();
 },70);
}
export function stepper(labels,stage,sp){
 return '<div class="steps">'+labels.map((l,i)=>'<div class="step"><i style="width:'+(i<stage?100:i===stage?sp:0)+'%"></i></div>').join('')+'</div>'+
  '<div class="steps-l">'+labels.map((l,i)=>'<span class="'+(i===stage?'on':i<stage?'done':'')+'">'+l+'</span>').join('')+'</div>';
}
export function patchProc(){const s=S.single,e=$('#procSteps');if(e)e.innerHTML=s.phase==='exporting'?stepper(['导出','验证'],s.stage,s.sp):stepper(['读取','检测'],s.stage,s.sp)}
export function vSingle(){
 const s=S.single;
 if(s.phase==='processing')return '<div class="center"><div class="card proc"><div class="proc-f">'+fileIcon(s.ext)+'<div style="min-width:0"><div class="fn">'+esc(s.name)+'</div><div class="fm">'+s.size+' · '+s.pages.length+' 页</div></div><button class="ib" data-a="cancelSingle" data-tip="停止" aria-label="停止">'+I('x',18)+'</button></div><div id="procSteps">'+stepper(['读取','检测'],s.stage,s.sp)+'</div></div></div>';
 if(s.phase==='done')return s.resultPhase==='manifest'?vManifest(singleManifest(s)):vDone();
 return vReview();
}
export const actOf=t=>S.single.catAct[t]||'mask';
export function hitHtml(h){
 const s=S.single,fx=rt.RFX==null?s.mode==='fx':rt.RFX,ph=actOf(h.type)==='ph';
 const txt=(fx&&h.state==='redact'&&ph)?'［'+TYPES[h.type].ph+'］':esc(h.text);
 const c=['hit',h.state==='keep'?'keep':'',h.low&&!h.seen&&h.state==='redact'?'low':'',ph?'ph':'',s.focus===h.id?'focus':''].join(' ');
 return '<span class="'+c+'" style="--c:var(--t-'+h.type+')" data-a="hit" data-v="'+h.id+'"'+(rt.RNOID?'':' id="h'+h.id+'"')+'>'+txt+'</span>';
}
export function segsHtml(b,pi,bi){return (b.segs||[]).map((g,si)=>g.h!=null?hitHtml(S.single.hits[g.h]):'<span class="tx" data-k="'+pi+'.'+bi+'.'+si+'">'+esc(g.t)+'</span>').join('')}
export const PHOTO='<svg viewBox="0 0 104 132" width="104" height="132" aria-hidden="true"><rect width="104" height="132" fill="#dce6f2"/><circle cx="52" cy="54" r="21" fill="#9fb0c4"/><path d="M12 132c4-30 21-43 40-43s36 13 40 43z" fill="#9fb0c4"/></svg>';
export const SEAL='<svg viewBox="0 0 120 120" width="120" height="120" aria-hidden="true"><defs><path id="sa" d="M17,62 a43,43 0 1,1 86,0"/></defs><circle cx="60" cy="60" r="55" fill="none" stroke="#c8102e" stroke-width="3.2"/><text font-size="11.5" fill="#c8102e" font-family="Songti SC,SimSun,serif" letter-spacing="1.6"><textPath href="#sa" startOffset="50%" text-anchor="middle">杭州澄音科技有限公司</textPath></text><text x="60" y="73" text-anchor="middle" font-size="26" fill="#c8102e">★</text><text x="60" y="97" text-anchor="middle" font-size="10" fill="#c8102e" font-family="Songti SC,SimSun,serif">合同专用章</text></svg>';
export function imgHtml(ix){
 const s=S.single,im=s.imgs[ix],rm=im.act!=='keep',gone=rm&&(rt.RFX==null?s.mode==='fx':rt.RFX),inB=s.from==='batch';
 const cls=(im.kind==='photo'?'d-photo':'d-seal')+(gone?' gone':rm?' imgm':'');
 return '<div class="'+cls+'"'+(inB?' style="cursor:default"':' data-a="imgMenu"')+' data-v="'+ix+'" data-tip="'+im.label+' · '+(rm?outKind(s.ext).verb:'保留')+(inB?' · 按批次设置':'')+'">'+(gone?'图像已涂抹':im.kind==='photo'?PHOTO:SEAL)+'</div>';
}
export function blockHtml(b,pi,bi){
 const t=segsHtml(b,pi,bi);
 switch(b.k){
  case 'title':return '<div class="d-title">'+t+'</div>';
  case 'meta':return '<div class="d-meta">'+t+'</div>';
  case 'h':return '<h4>'+t+'</h4>';
  case 'pi':return '<p class="ind">'+t+'</p>';
  case 'img':return imgHtml(b.img);
  case 'sign':return '<div class="d-sign"><div><p>甲方（盖章）：</p>'+imgHtml(b.img)+'</div><div><p>'+t+'</p><p>日期：2026 年 4 月 17 日</p></div></div>';
  default:return '<p>'+t+'</p>';
 }
}
export function thumbsHtml(){
 const s=S.single;
 return s.pages.map((pg,pi)=>{
  let marks='';const n=pg.length;
  pg.forEach((b,bi)=>{
   if(b.k==='img')marks+='<i class="im" style="right:14%;top:12%"></i>';
   if(b.k==='sign')marks+='<i class="im" style="left:16%;top:80%;border-radius:50%;width:22%;height:14%"></i>';
   (b.segs||[]).forEach((g,si)=>{if(g.h!=null){const h=s.hits[g.h];if(h.state==='redact')marks+='<i style="--c:var(--t-'+h.type+');top:'+(10+bi/n*78).toFixed(1)+'%;left:'+(18+(si%3)*18)+'%"></i>'}})
  });
  return '<button class="thumb '+(s.page===pi?'on':'')+'" data-a="toPage" data-v="'+pi+'" aria-label="'+(s.ext==='docx'?'约':'')+'第 '+(pi+1)+' 页">'+marks+'<b>'+(pi+1)+'</b></button>';
 }).join('');
}
export function lowList(){
 const s=S.single;const ids=orderedHits().filter(i=>s.hits[i].low);const left=ids.filter(i=>!s.hits[i].seen).length;
 if(!left)return '';
 return '<div class="cats-h"><span class="dot" style="color:#D18A0F;width:6px;height:6px"></span><span>待看</span><span class="num" style="color:var(--ink-2)">'+left+'</span><span class="sp"></span><span class="muted" style="font-weight:400" data-tip="置信度较低，已默认涂抹" data-tr tabindex="0">'+I('info',13)+'</span></div>'+
  ids.map(i=>{const h=s.hits[i];return '<button class="lowrow '+(h.seen?'seen':'')+'" data-a="focusHit" data-v="'+i+'" style="--c:var(--t-'+h.type+')"><span class="sw"></span><span class="v">'+esc(h.text)+'</span><span class="muted sm">'+(h.seen?(h.state==='keep'?'保留':'涂抹'):locStr(s.ext,h.page+1,h.sec,h.para,true))+'</span>'+(h.seen?I('check',13,2.4):'')+'</button>'}).join('');
}
export function catsHead(){const s=S.single;const low=s.hits.some(h=>h.low&&!h.seen);return '<div class="cats-h"><span>命中</span><span class="num" style="color:var(--ink-2)">'+s.hits.length+'</span><span class="sp"></span>'+(low?'':'<span class="pill ok">'+I('check',12,2.4)+'已看完</span>')+'</div>'}
export function catStats(){
 const s=S.single,m={};
 s.hits.forEach(h=>{const o=m[h.type]||(m[h.type]={r:0,k:0,low:0});h.state==='redact'?o.r++:o.k++;if(h.low&&!h.seen)o.low++});
 return m;
}
export function vReview(){
 const s=S.single,ok=outKind(s.ext),st=catStats();
 const order=['org','name','phone','email','id','addr','card','date','other'].filter(t=>st[t]);
 const low=s.hits.filter(h=>h.low&&!h.seen).length;
 const redN=s.hits.filter(h=>h.state==='redact').length;
 const partial=s.imgs.some(i=>i.act==='keep');
 const inBatch=s.from==='batch';
 const bf=inBatch&&S.batch?S.batch.files[s.bfi]:null,bOut=!!bf&&['done','partial'].includes(bf.st);
 const bPend=bf?[...bf.g].reduce((a,gid)=>a+(S.batch.groups.find(g=>g.id===gid).per[bf.i]||0),0):0;
 return '<div class="review '+(s.mode==='cmp'?'cmp':'')+'">'+
  '<div class="thumbs" id="thumbs" data-keep>'+thumbsHtml()+'</div>'+
  '<div class="docwrap">'+
   '<div class="doc-tools">'+
    '<div class="seg" role="group" aria-label="视图"><button data-a="mode" data-v="orig" aria-pressed="'+(s.mode==='orig')+'">原文</button><button data-a="mode" data-v="fx" aria-pressed="'+(s.mode==='fx')+'">效果</button><button data-a="mode" data-v="cmp" aria-pressed="'+(s.mode==='cmp')+'" data-tip="左右对比 C">'+I('columns',15)+'对比</button></div>'+
    '<span class="sp"></span>'+
    (s.mode==='orig'?'<span class="tool-hint">'+I('mark',14)+'选中文字可补充涂抹</span>':'<span class="tool-hint" data-tip="按当前决定模拟，导出后以验证结果为准" data-tr tabindex="0">'+I('info',14)+'效果为预览</span>')+
    '<button class="ib" data-a="undo" data-tip="撤销 ⌘Z" data-tr aria-label="撤销" '+(S.undo.length?'':'disabled style="opacity:.35"')+'>'+I('undo',17)+'</button>'+
    '<button class="ib" data-a="keys" data-tip="快捷键" data-tr aria-label="快捷键">'+I('kbd',18)+'</button>'+
   '</div>'+
   '<div class="doc-scroll '+(s.mode==='cmp'?'cmpv':'')+'" id="docScroll" data-keep>'+docHtml()+'</div>'+
  '</div>'+
  '<aside class="side">'+
   '<div class="side-head">'+fileIcon(s.ext)+'<div style="min-width:0"><div class="fn">'+esc(s.name)+'</div><div class="fm">'+s.pages.length+' 页 · '+s.size+'</div></div></div>'+
   '<div class="sum">'+
    (inBatch?'<div class="sum-row ro" tabindex="0" data-tip="批次方案 · 开始后不可更改">'+I('layers',16)+'<span class="v">'+s.preset+'</span>'+confSum(s.preset)+'<span class="sp"></span>'+I('lock',14)+'</div>':'<button class="sum-row" data-a="presetMenu">'+I('layers',16)+'<span class="v">'+s.preset+'</span>'+confSum(s.preset)+'<span class="sp"></span>'+I('chev-d',14)+'</button>')+
    '<div class="sum-row ro" tabindex="0" data-tip="'+ok.tip+'">'+I('fileout',16)+'<span class="v">'+ok.l+'</span><span class="sp"></span>'+I('info',14)+'</div>'+
    (inBatch?'<div class="sum-row ro" tabindex="0" data-tip="按批次设置">'+I('image',16)+'<span class="v">图像 '+s.imgs.length+'</span><span class="muted">'+imgSum(s,ok)+'</span><span class="sp"></span>'+I('lock',14)+'</div>':'<button class="sum-row" data-a="imgAllMenu">'+I('image',16)+'<span class="v">图像 '+s.imgs.length+'</span><span class="muted">'+imgSum(s,ok)+'</span><span class="sp"></span>'+(partial?'<span class="pill warn">部分结果</span>':'')+I('chev-d',14)+'</button>')+
    (bPend?'<button class="sum-row" data-a="bfReview">'+I('alert',16)+'<span class="v">还有 '+bPend+' 处待你处理</span><span class="sp"></span>'+I('chev-r',14)+'</button>':'')+
   '</div>'+
   '<div class="cats" id="cats" data-keep>'+lowList()+catsHead()+order.map(t=>{const o=st[t];return '<div class="cat" style="--c:var(--t-'+t+')"><button class="cat-main" data-a="focusType" data-v="'+t+'"><span class="sw"></span><span>'+TYPES[t].l+'</span>'+(o.low?'<span class="dot" style="color:#D18A0F;width:6px;height:6px"></span>':'')+'<span class="n num">'+o.r+'</span>'+(o.k?'<span class="kept">保留 '+o.k+'</span>':'')+'</button><button class="sel sm" data-a="catMenu" data-v="'+t+'" aria-label="'+TYPES[t].l+' 处理方式">'+(actOf(t)==='ph'?'占位':'涂抹')+I('chev-d',12)+'</button></div>'}).join('')+'</div>'+
   '<div class="side-foot">'+
    (inBatch?(bOut?'<div class="side-note">'+I('info',13)+'已导出 · 修改后将重新导出并替代旧版本</div>':'')+'<button class="btn btn-p btn-lg wide" data-a="backBatch">'+(bOut&&S.undo.length?'完成并重新导出':'完成')+'</button>':
    '<button class="btn btn-p btn-lg wide" data-a="exportSingle">'+(partial?'导出部分结果':'确认并导出')+'</button>'+
    '<button class="out" data-a="outMenu" data-tip="导出位置" data-tt aria-label="导出位置">'+I('folder',13)+'<span>'+s.out+'</span>'+I('chev-d',12)+'</button>')+
   '</div>'+
  '</aside>'+
 '</div>'+(s.phase==='exporting'?'<div class="overlay"><div class="card proc"><div class="proc-f">'+fileIcon(s.ext)+'<div><div class="fn">'+esc(outName())+'</div><div class="fm">涂抹 '+redN+' 处</div></div></div><div id="procSteps">'+stepper(['导出','验证'],s.stage,s.sp)+'</div></div></div>':'');
}
export function imgSum(s,ok){const n=s.imgs.length,k=s.imgs.filter(i=>i.act==='keep').length;return !k?ok.verb:k===n?'保留':'保留 '+k+' · '+ok.verb+' '+(n-k)}
export function outName(s=S.single){const base=s.name.replace(/\.[^.]+$/,'');const ext=['pdf','docx'].includes(s.ext)?s.ext:s.ext;return base+'_已脱敏.'+ext}
export function docHtml(){
 const s=S.single;
 const paper=(pg,pi,cls,id)=>'<div class="paper '+cls+'"'+(id?' id="pg'+pi+'" data-pg':'')+'>'+pg.map((b,bi)=>blockHtml(b,pi,bi)).join('')+'<div class="pgno">'+(s.ext==='docx'?'约 ':'')+(pi+1)+' / '+s.pages.length+'</div></div>';
 let h;
 if(s.mode==='cmp'){
  h='<div class="doc" id="doc"><div class="cmp-h"><span>原文</span><span>效果 · 预览</span></div>'+s.pages.map((pg,pi)=>{
   rt.RFX=false;rt.RNOID=false;const a=paper(pg,pi,'',false);
   rt.RFX=true;rt.RNOID=true;const b=paper(pg,pi,'fx',false);
   return '<div class="cmp-row" id="pg'+pi+'" data-pg>'+a+b+'</div>'}).join('')+'</div>';
 }else h='<div class="doc '+(s.mode==='fx'?'fx':'')+'" id="doc">'+s.pages.map((pg,pi)=>paper(pg,pi,'',true)).join('')+'</div>';
 rt.RFX=null;rt.RNOID=false;
 return h;
}
export function fitCmp(){$$('.doc-scroll.cmpv').forEach(sc=>sc.style.setProperty('--z',Math.min(1,Math.max(.4,(sc.clientWidth-40-16)/2/640)).toFixed(3)))}
export function setMode(m){
 const s=S.single;if(!s||s.mode===m)return;
 const sc=$('#docScroll'),pg=$('#pg'+s.page);let frac=0;
 if(sc&&pg){const a=sc.getBoundingClientRect(),b=pg.getBoundingClientRect();frac=(a.top-b.top)/b.height}
 s.mode=S.docMode=m;render();
 const sc2=$('#docScroll'),p2=$('#pg'+s.page);
 if(sc2&&p2){sc2.style.scrollBehavior='auto';const a=sc2.getBoundingClientRect(),b=p2.getBoundingClientRect();sc2.scrollTop+=b.top-a.top+Math.max(0,frac)*b.height;sc2.style.scrollBehavior=''}
}
export function hitEl(id){const s=S.single;return (s.popFx&&s.mode==='cmp'&&$('.paper.fx .hit[data-v="'+id+'"]'))||$('#h'+id)}
export function bindDoc(){
 const sc=$('#docScroll');if(!sc)return;
 fitCmp();
 sc.addEventListener('mouseover',e=>{const h=e.target.closest('.hit');const v=h&&S.single.mode==='cmp'?h.dataset.v:null;if(v===sc._tw)return;sc._tw=v;$$('.hit.twin',sc).forEach(x=>x.classList.remove('twin'));if(v)$$('.hit[data-v="'+v+'"]',sc).forEach(x=>x.classList.add('twin'))});
 sc.addEventListener('scroll',()=>{
  const s=S.single;const top=sc.scrollTop+sc.clientHeight*.35;let p=0;
  $$('[data-pg]',sc).forEach((e,i)=>{if(e.offsetTop<=top)p=i});
  if(p!==s.page){s.page=p;$$('.thumb').forEach((t,i)=>t.classList.toggle('on',i===p))}
 },{passive:true});
 sc.addEventListener('mouseup',onSelect);
}
export function onSelect(){
 const sel=window.getSelection();if(!sel||sel.isCollapsed)return;
 const r=sel.getRangeAt(0);const a=r.startContainer,b=r.endContainer;
 if(a!==b||a.nodeType!==3)return;
 const sp=a.parentElement;if(!sp||!sp.classList.contains('tx'))return;
 const st=Math.min(r.startOffset,r.endOffset),en=Math.max(r.startOffset,r.endOffset);
 const text=a.data.slice(st,en);if(!text.trim())return;
 S.pendSel={k:sp.dataset.k,st,en,text};
 const types=['org','name','phone','id','addr','other'];
 openPop(r.getBoundingClientRect(),'<div class="mh">涂抹「'+esc(text.length>14?text.slice(0,14)+'…':text)+'」</div><div class="typegrid">'+types.map(t=>'<button data-a="addManual" data-v="'+t+'"><span class="sw" style="--c:var(--t-'+t+')"></span>'+TYPES[t].l+'</button>').join('')+'</div>',{focus:false});
}
export function pushUndo(){S.undo.push(JSON.stringify({hits:S.single.hits,pages:S.single.pages,imgs:S.single.imgs,catAct:S.single.catAct}));if(S.undo.length>50)S.undo.shift()}
export function doUndo(){const u=S.undo.pop();if(!u)return;Object.assign(S.single,JSON.parse(u));closePop();render();toast('已撤销')}
export function addManual(type){
 const p=S.pendSel;if(!p)return;pushUndo();
 const s=S.single;const[pi,bi,si]=p.k.split('.').map(Number);const b=s.pages[pi][bi];const g=b.segs[si];
 const h={id:s.hits.length,type,text:p.text,low:false,seen:true,src:'人工添加',state:'redact',page:pi,para:b.at&&b.at.para,sec:b.at&&b.at.sec,manual:true};s.hits.push(h);
 const parts=[];if(p.st>0)parts.push({t:g.t.slice(0,p.st)});parts.push({h:h.id});if(p.en<g.t.length)parts.push({t:g.t.slice(p.en)});
 b.segs.splice(si,1,...parts);
 window.getSelection().removeAllRanges();closePop();S.pendSel=null;render();
 toast('已涂抹 · '+TYPES[type].l,doUndo);
}
export function hitPop(id){
 const s=S.single,h=s.hits[id];
 const same=s.hits.filter(x=>x.text===h.text&&x.type===h.type);
 return '<div class="hp"><div class="hp-h"><span class="sw" style="--c:var(--t-'+h.type+')"></span>'+TYPES[h.type].l+'<span class="muted" style="font-weight:400">· '+h.src+'</span></div>'+
  '<div class="hp-v">'+esc(h.text)+'</div>'+
  '<div class="muted sm" style="margin:-6px 0 12px">'+locStr(s.ext,h.page+1,h.sec,h.para)+'</div>'+
  '<div class="seg full"><button data-a="setHit" data-v="keep" aria-pressed="'+(h.state==='keep')+'">保留</button><button data-a="setHit" data-v="redact" aria-pressed="'+(h.state==='redact')+'">涂抹</button></div>'+
  (same.length>1?'<label class="hp-same"><input type="checkbox" id="sameAll" '+(s.sameAll?'checked':'')+'> 同值 '+same.length+' 处一起</label>':'')+
 '</div>';
}
export function focusHit(id,open=true){
 const s=S.single;s.focus=id;s.popFx=false;const h=s.hits[id];if(h.low)h.seen=true;
 render();
 const el=$('#h'+id);if(!el)return;
 el.scrollIntoView({block:'center',behavior:'smooth'});
 if(open)setTimeout(()=>{const e=$('#h'+id);if(e)openPop(e,hitPop(id),{role:'dialog',focus:false})},260);
}
export function setHit(state){
 const s=S.single,h=s.hits[s.focus];if(!h)return;pushUndo();
 const targets=s.sameAll?s.hits.filter(x=>x.text===h.text&&x.type===h.type):[h];
 targets.forEach(x=>{x.state=state;x.seen=true});
 closePop();render();
 const el=hitEl(h.id);if(el)openPop(el,hitPop(h.id),{role:'dialog',focus:false});
 toast((state==='keep'?'保留':'涂抹')+(targets.length>1?' '+targets.length+' 处':''),doUndo);
}
export function stepHit(d){
 const s=S.single;const n=s.hits.length;if(!n)return;
 const ordered=orderedHits();let i=ordered.indexOf(s.focus);
 i=i<0?(d>0?0:n-1):(i+d+n)%n;focusHit(ordered[i]);
}
export function orderedHits(){const ids=[];S.single.pages.forEach(pg=>pg.forEach(b=>(b.segs||[]).forEach(g=>{if(g.h!=null)ids.push(g.h)})));return ids}
export function exportSingle(){
 const s=S.single;closePop();s.phase='exporting';s.stage=0;s.sp=0;render();
 clearInterval(rt.procT);
 rt.procT=setInterval(()=>{
  s.sp+=7;if(s.sp>=100){s.sp=0;s.stage++;if(s.stage>=2){clearInterval(rt.procT);s.phase='done';
   const red=s.hits.filter(h=>h.state==='redact').length;
   const pt=s.imgs.some(i=>i.act==='keep');S.history.unshift({k:'file',ext:s.ext,name:s.name,meta:s.pages.length+' 页',st:pt?'part':'done',stl:pt?'部分结果':'已导出',when:'刚刚',saved:s});
   if(S.view==='single')render();return}}
  if(S.view==='single')patchProc();
 },60);
}
export function vDone(s=S.single){
 const red=s.hits.filter(h=>h.state==='redact').length,keep=s.hits.length-red,kImg=s.imgs.filter(i=>i.act==='keep').length,partial=kImg>0;
 return '<div class="center"><div class="card donecard">'+
  '<div class="done-ic '+(partial?'warn':'')+'">'+I(partial?'alert':'check',28,2.2)+'</div>'+
  '<h2>'+(partial?'已导出部分结果':'已导出')+'</h2>'+
  '<div class="fn">'+I('folder',13)+esc(s.out)+' / '+esc(outName(s))+'</div>'+
  '<div class="stats"><div><b class="num">'+red+'</b><span>涂抹</span></div><div><b class="num">'+keep+'</b><span>保留</span></div><div><b class="num">'+(s.imgs.length-kImg)+'</b><span>涂抹图像</span></div>'+(partial?'<div><b class="num" style="color:var(--warn)">'+kImg+'</b><span>未检查图像</span></div>':'')+'</div>'+
  '<div class="acts"><button class="btn btn-p" data-a="reveal">'+I('folder',15)+'打开所在位置</button><button class="btn btn-s" data-a="singlePhase" data-v="manifest">'+I('list',15)+'查看清单</button></div>'+
  '<div class="acts" style="margin-top:8px"><button class="link" data-a="go" data-v="home">再处理一份</button></div>'+
  '<div class="foot-note" data-tip="自动识别有局限，不代表文档已无隐私" data-tt>'+I('info',12)+'按策略处理，未声明零遗漏</div>'+
 '</div></div>';
}
export function singleManifest(s){
 const partial=s.imgs.some(i=>i.act==='keep');
 const man=s.hits.some(h=>h.manual||h.state==='keep')||partial||Object.keys(s.catAct||{}).length>0;
 const B=s.manifest||(s.manifest={});
 return Object.assign(B,{n:1,out:s.out,files:[{i:0,id:'T0001',ext:s.ext,outputName:outName(s),st:partial?'partial':'done',man}]});
}
export function currentManifest(){
 if(S.view==='history'){const r=S.historyResult;return r.entry.k==='file'?singleManifest(r.data):r.data}
 return S.view==='single'?singleManifest(S.single):S.batch;
}


