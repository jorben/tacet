// @ts-nocheck

// @generated-from docs/v1/tacet-prototype.html (script section: review); maintained by hand after the port.
import { $, esc, fmt, ri, rng, I, fileIcon } from './runtime';
import { locStr, SUR, GIV, NODOC, RVSRC, DOCT, FILL } from '../domain/data';
import { S } from './state';
import { gMembers, stats, counts, busy, settled } from './batch';
import { closePop, toast } from './layer';
import { render, railHtml } from './shell';
import { A } from './actions';
import { fitCmp } from './single';

export const decLabel=(g,k)=>k==='pwd'?'已解锁':(g.acts.find(a=>a.k===k)||{l:''}).l;
export function rvSync(){
 const B=S.batch;const rv=B.rv||(B.rv={items:[],cur:null,all:false,undo:[]});
 const want=new Map();
 B.groups.forEach((g,gi)=>gMembers(g).forEach(fi=>{for(let j=0;j<g.per[fi];j++)want.set(g.id+'|'+fi+'|'+j,{gid:g.id,gi,fi,j})}));
 const items=rv.items.filter(it=>it.dec||want.has(it.key));
 const have=new Set(items.map(it=>it.key));
 want.forEach((v,k)=>{if(!have.has(k))items.push(Object.assign({key:k,dec:null,p:1+(v.fi*7+v.j*5)%Math.max(1,B.files[v.fi].pages)},v))});
 items.sort((a,b)=>a.gi-b.gi||a.fi-b.fi||a.p-b.p||a.j-b.j);
 const sig=items.map(it=>it.key+':'+(it.dec||'')).join(',');
 const changed=sig!==rv.sig;rv.items=items;rv.sig=sig;
 // once the review is finished, new exceptions are announced on the card instead of replacing it
 if(rv.cur==null?!rv.fin:!items.some(it=>it.key===rv.cur)){const n=items.find(it=>!it.dec);rv.cur=n?n.key:null}
 return changed;
}
export function rvCur(){const rv=S.batch.rv;return rv?rv.items.find(x=>x.key===rv.cur):null}
export function enterReview(gid,fi){
 const B=S.batch;if(B.rv)B.rv.fin=false;rvSync();const rv=B.rv;
 const it=rv.items.find(x=>!x.dec&&(!gid||x.gid===gid)&&(fi==null||x.fi===fi));if(it)rv.cur=it.key;
 B.phase='review';closePop();render();
}
export function rvNextPending(key){const its=S.batch.rv.items;const i=its.findIndex(x=>x.key===key);for(let k=1;k<=its.length;k++){const x=its[(i+k)%its.length];if(!x.dec)return x}return null}
export function rvStep(d){const rv=S.batch.rv;rv.hov=null;rv.fin=false;const n=rv.items.length;if(!n)return;let i=rv.items.findIndex(x=>x.key===rv.cur);i=i<0?0:(i+d+n)%n;rv.cur=rv.items[i].key;render()}
export function rvSnap(items){const B=S.batch,rv=B.rv;const fis=[...new Set(items.map(t=>t.fi))];return{cur:rv.cur,decs:items.map(t=>[t.key,t.dec]),files:fis.map(fi=>{const f=B.files[fi];return[fi,f.st,new Set(f.g),f.partial,f.t,f.man]}),done:B.groups.map(x=>x.done.map(d=>Object.assign({},d)))}}
export function rvAddRec(g,k,n){let rec=g.done.find(d=>d.rv&&d.k===k);if(!rec){rec={k,l:decLabel(g,k),n:0,rv:true};g.done.push(rec)}rec.n+=n}
export function rvDecide(k){
 const B=S.batch,rv=B.rv,cur=rvCur();if(!cur||cur.dec===k)return;
 if(k==='pwd'){const p=$('#rvPwd');if(p&&!p.value){p.focus();return}}
 const g=B.groups[cur.gi];
 if(cur.dec){
  const f=B.files[cur.fi];if(['done','partial'].includes(f.st)){toast('已导出，不能更改');return}
  rv.undo.push(rvSnap([cur]));
  const o=g.done.find(d=>d.rv&&d.k===cur.dec);if(o){o.n--;if(!o.n)g.done.splice(g.done.indexOf(o),1)}
  rvAddRec(g,k,1);cur.dec=k;if(k==='redo')f.redo=true;
  const mine=rv.items.filter(x=>x.fi===cur.fi&&x.dec);
  f.partial=mine.some(x=>B.groups[x.gi].gap&&x.dec==='keep');
  if(mine.some(x=>x.dec==='skip'))f.st='skipped';
  else if(f.st==='skipped'){if(f.g.size)f.st=B.mode==='auto'?'held':'wait';else{f.st='proc';f.t=ri(8,14)}}
  rv.hov=null;rvSync();render();toast('已改为 '+decLabel(g,k),rvUndo);return;
 }
 const targets=rv.all&&k!=='pwd'&&!g.noBulk?rv.items.filter(x=>!x.dec&&x.gid===cur.gid):[cur];
 const fis=[...new Set(targets.map(t=>t.fi))];
 rv.undo.push(rvSnap(targets));
 targets.forEach(t=>t.dec=k);
 rvAddRec(g,k,targets.length);
 fis.forEach(fi=>{
  const its=rv.items.filter(x=>x.gid===g.id&&x.fi===fi);if(its.some(x=>!x.dec))return;
  const f=B.files[fi];f.g.delete(g.id);f.man=true;
  if(its.some(x=>x.dec==='skip')){f.st='skipped';return}
  if(g.gap&&its.some(x=>x.dec==='keep'))f.partial=true;
  if(k==='redo')f.redo=true;
  if(!f.g.size){f.st='proc';f.t=ri(8,14)}
 });
 rv.all=false;rv.hov=null;
 const nx=rvNextPending(cur.key);rvSync();rv.cur=nx&&!nx.dec&&rv.items.includes(nx)?nx.key:((rv.items.find(x=>!x.dec)||{}).key||null);
 render();
 toast(decLabel(g,k)+(targets.length>1?' '+targets.length+' '+g.unit:''),rvUndo);
}
export function rvUndo(){
 const B=S.batch,rv=B.rv;const s=rv&&rv.undo.pop();if(!s)return;
 if(s.files.some(([fi])=>['done','partial'].includes(B.files[fi].st))){toast('已导出的文件不能撤销');return}
 s.files.forEach(([fi,st,gs,p,t,man])=>Object.assign(B.files[fi],{st,g:gs,partial:p,t,man}));
 B.groups.forEach((g,i)=>g.done=s.done[i]);
 const m=new Map(s.decs);rv.items.forEach(it=>{if(m.has(it.key))it.dec=m.get(it.key)});
 rv.cur=s.cur;rv.fin=false;rv.hov=null;rvSync();render();toast('已撤销');
}
/* demo: contracts have numbered clauses as headings; every fourth DOCX was saved without page-break hints */
export function rvLoc(f,it){
 if(f.ext!=='docx')return '第 '+it.p+' 页';
 const sec=/^(劳动|采购)合同/.test(f.name)?'第'+'一二三四五六七八九'[(it.p+it.j)%9]+'条':'';
 return locStr('docx',f.i%4?it.p:null,sec,3+(it.p*7+it.j*5)%40);
}
export function rvWhere(g,f,it){return g.id==='pwd'?'已加密':g.id==='fail'?'整份文件':g.id==='scan'?'全部 '+f.pages+' 页':g.id==='strict'?'命中 '+fmt(Object.values(f.h).reduce((a,b)=>a+b,0))+' 处':rvLoc(f,it)+(g.per[it.fi]>1?' · 第 '+(it.j+1)+' '+g.unit:'')}
export function vRv(){
 rvSync();const it=rvCur();const rv=S.batch.rv;
 if(!it&&!rv.fin){rv.fin=true;rv.finAt=Date.now();rv.setAt=0}
 return '<div class="rvw '+(it?'':'fin')+(it&&(S.docMode||'orig')==='cmp'&&!NODOC.includes(it.gid)?' cmp':'')+'"><nav class="rvq" id="rvq" data-keep aria-label="复核队列">'+rvQueue()+'</nav>'+(it?rvCenter(it)+rvPanel(it):rvDone())+'</div>';
}
export function rvQueue(){
 const B=S.batch,rv=B.rv;let h='',last=null,lastF=null;
 rv.items.forEach(it=>{
  const g=B.groups[it.gi],f=B.files[it.fi];const sub=it.gid===last&&it.fi===lastF;lastF=it.fi;
  if(it.gid!==last){last=it.gid;const left=rv.items.filter(x=>x.gid===it.gid&&!x.dec).length;
   h+='<div class="rvg">'+I(g.ic,14)+'<span>'+g.t+'</span><span class="n num">'+(left||I('check',13,2.4))+'</span></div>'}
  h+='<button class="rvi '+(it.dec?'dn ':'')+(sub?'sub':'')+'" data-a="rvGo" data-v="'+it.key+'" aria-current="'+(it.key===rv.cur)+'"><span class="st '+(it.dec?'ok':'')+'">'+(it.dec?I('check',10,3):'')+'</span><span class="x">'+(sub?'':'<div class="fn">'+esc(f.name)+'</div>')+'<div class="fm">'+rvWhere(g,f,it)+(g.found&&g.found[it.fi]?' <span class="tag">导出后发现</span>':'')+'</div></span>'+(it.dec?'<span class="dl">'+decLabel(g,it.dec)+'</span>':'')+'</button>';
 });
 return h||'<div class="empty" style="padding:48px 0">—</div>';
}
export function rvData(f,it){
 if(it._d)return it._d;
 const kind=Object.keys(DOCT).find(k=>f.name.startsWith(k))||'客户回访';
 const r=rng(it.fi*31+it.j*7+3);const ix=a=>a[Math.floor(r()*a.length)];const dg=n=>Array.from({length:n},()=>Math.floor(r()*10)).join('');
 const id='330106'+(1978+it.fi%25)+'0'+(1+it.fi%9)+(10+it.j%18)+dg(3)+'X';
 return it._d={kind,id,org:'杭州'+ix(['澄音','启明','云栖','望湖'])+ix(['科技','贸易','咨询'])+'有限公司',name:ix(SUR)+ix(GIV),phone:'1'+ix(['38','39','86','58'])+' '+dg(4)+' '+dg(4),email:ix(['li','wang','zhang','chen'])+dg(3)+'@'+ix(['163.com','qq.com','corp.cn']),addr:'杭州市'+ix(['西湖区','滨江区','拱墅区'])+ix(['文三路','江南大道','莫干山路'])+dg(3)+' 号',callee:ix(SUR)+'先生',cphone:'139 '+dg(4)+' '+dg(4),att:['报价单.xlsx','签字页.pdf','名单.csv'][(it.fi+it.j)%3],kb:12+it.fi%80,paras:Array.from({length:6},()=>ix(FILL)+ix(FILL)),scan:Array.from({length:22},()=>62+Math.floor(r()*36))};
}
export const CURVE_SVG='<svg width="150" height="30" aria-hidden="true"><text x="0" y="25" font-size="25" font-weight="700" font-family="Songti SC,SimSun,serif" fill="none" stroke="#2b2b2b" stroke-width=".8">澄音科技</text></svg>';
export function rvPaper(g,f,it,view,k){
 const d=rvData(f,it),o=view==='orig',act=o?null:k;
 const fld=(t,v)=>o?'<span class="hit" style="--c:var(--t-'+t+')">'+esc(v)+'</span>':'<span class="rd" style="--c:var(--t-'+t+')">'+esc(v)+'</span>';
 const kv=(a,v)=>'<p>'+a+'：'+v+'</p>';
 const para=(a,n)=>d.paras.slice(a,a+n).map(x=>'<p class="ind">'+x+'</p>').join('');
 const head='<div class="d-title" style="font-size:20px;letter-spacing:.25em">'+DOCT[d.kind]+'</div><div class="d-meta">编号：'+f.id+'-'+(100+it.j)+'</div>';
 const base=kv('单位',fld('org',d.org))+kv('姓名',fld('name',d.name))+kv('联系电话',fld('phone',d.phone))+kv('电子邮箱',fld('email',d.email));
 let body='',cls='';
 switch(g.id){
  case 'idck':body=head+base+kv('证件号码',o?'<span class="hit tgt" style="--c:var(--t-id)">'+d.id+'</span>':act==='do'?'<span class="rd tgt" style="--c:var(--t-id)">'+d.id+'</span>':'<span class="tgt keepv">'+d.id+'</span>')+kv('现住址',fld('addr',d.addr))+para(0,3);break;
  case 'shot':body=head+base+para(0,1)+(act==='do'?'<div class="shot-gone tgt">截图已涂抹</div>':'<div class="shot tgt"><div class="bar"><i></i><i></i><i></i></div><div class="bd"><span class="bb">您好，我是'+d.callee+'，回访电话 '+d.cphone+'</span><span class="bb me">收到，合同已发您邮箱</span><span class="bb">好的，身份证复印件稍后补</span></div></div>')+'<div class="cap">图 '+(it.j+1)+'　沟通记录截图</div>'+para(1,2);break;
  case 'embed':body=head+base+para(0,2)+(act==='do'?'<div class="att gone2 tgt">'+I('clip',18)+'<span>附件已涂抹</span></div>':'<div class="att tgt">'+I('clip',18)+'<span>'+d.att+'<small>'+d.kb+' KB · 嵌入对象</small></span></div>')+para(2,2);break;
  case 'curve':body='<div class="lh tgt">'+(act==='do'?'<span class="rd" style="display:inline-block;width:150px;height:30px"></span>':CURVE_SVG)+'<span style="font-size:11px;color:#888">内部资料</span></div>'+head+base+para(0,3);break;
  case 'scan':cls='scan tgt';body=d.scan.map((w,i)=>'<div class="scanl" style="width:'+(i%6===0?40:w)+'%;'+(i===0?'margin:6px auto 26px;height:14px':'')+'"></div>').join('')+(act==='do'?'<div class="scan-gone">整页已涂抹</div>':'');break;
 }
 return '<div class="paper pv '+cls+'">'+body+'<div class="pgno">'+(f.ext!=='docx'?(g.id==='scan'?1:it.p)+' / '+f.pages:f.i%4?'约 '+it.p+' / '+f.pages:'')+'</div></div>';
}
export function rvFxK(it){const rv=S.batch.rv;return rv.hov||it.dec||S.batch.groups[it.gi].acts[0].k}
export function rvPreview(k){
 const rv=S.batch.rv;if(!rv||rv.hov===k)return;rv.hov=k;
 const it=rvCur(),fx=$('#rvFx');if(!it||!fx)return;
 const B=S.batch,g=B.groups[it.gi],f=B.files[it.fi],kk=rvFxK(it);
 fx.classList.add('still');fx.innerHTML=rvPaper(g,f,it,'fx',kk);
 const l=$('#rvFxL');if(l)l.textContent=(l.dataset.p||'')+decLabel(g,kk);
}
export function rvCenter(it){
 const B=S.batch,g=B.groups[it.gi],f=B.files[it.fi];const m=S.docMode||'orig';const doc=!NODOC.includes(g.id);
 let main;
 if(g.id==='pwd')main='<div class="rvbox"><div class="card" style="width:340px;padding:36px 28px;text-align:center"><div class="done-ic" style="background:var(--surface-2);color:var(--ink-2)">'+I('lock',26)+'</div><div style="font-weight:600">'+esc(f.name)+'</div><div class="muted sm" style="margin-top:4px">已加密</div></div></div>';
 else if(g.id==='fail')main='<div class="rvbox"><div class="card" style="width:440px;padding:30px 28px"><div style="display:flex;gap:12px;align-items:center">'+fileIcon(f.ext)+'<div><div style="font-weight:600">'+esc(f.name)+'</div><div class="muted sm">整份文件处理失败</div></div></div><dl class="kv" style="margin-top:20px"><dt>原因</dt><dd>'+esc(f.failWhy||'')+'</dd><dt>结果</dt><dd><span class="pill bad">未导出</span></dd></dl><div class="muted sm" style="margin-top:16px">重新处理会从失败的步骤再执行一次；仍失败将标记为无法处理</div></div></div>';
 else if(g.id==='strict')main='<div class="rvbox"><div class="card" style="width:440px;padding:30px 28px"><div style="display:flex;gap:12px;align-items:center">'+fileIcon(f.ext)+'<div><div style="font-weight:600">'+esc(f.name)+'</div><div class="muted sm num">'+fmt(f.pages)+' 页 · '+rvWhere(g,f,it)+'</div></div></div><div class="muted sm" style="margin-top:16px">逐处查看后再确认导出</div><button class="btn btn-s" data-a="rvOpen" style="margin-top:14px">'+I('ext',15)+'打开逐处复核</button></div></div>';
 else{const k=rvFxK(it),fxp='<div id="rvFx">'+rvPaper(g,f,it,'fx',k)+'</div>';
  main='<div class="doc-scroll '+(m==='cmp'?'cmpv':'')+'" id="rvDoc">'+(m==='cmp'?'<div class="cmp-h"><span>原文</span><span id="rvFxL" data-p="效果预览 · ">效果预览 · '+decLabel(g,k)+'</span></div><div class="cmp-row">'+rvPaper(g,f,it,'orig')+fxp+'</div>':m==='fx'?fxp:rvPaper(g,f,it,'orig'))+'</div>'}
 const seg=doc?'<div class="seg" role="group" aria-label="视图">'+[['orig','原文'],['fx','效果'],['cmp','对比']].map(([v,l])=>'<button data-a="rvMode" data-v="'+v+'" aria-pressed="'+(m===v)+'"'+(v==='cmp'?' data-tip="左右对比 C" data-tr':'')+'>'+(v==='cmp'?I('columns',15):'')+l+'</button>').join('')+'</div>':'';
 return '<div class="rvc"><div class="doc-tools"><span class="rvt">'+esc(f.name)+'</span><span class="muted sm" style="white-space:nowrap">'+rvWhere(g,f,it)+'</span>'+(doc&&m==='fx'?'<span class="pill pri" id="rvFxL" data-p="预览 · " style="margin-left:6px">预览 · '+decLabel(g,rvFxK(it))+'</span>':'')+'<span class="sp"></span>'+seg+(doc?'<button class="ib" data-a="rvOpen" data-tip="打开整份文件" data-tr aria-label="打开整份文件" style="margin-left:4px">'+I('ext',16)+'</button>':'')+'</div>'+main+'</div>';
}
export function rvFoot(){
 const rv=S.batch.rv,tot=rv.items.length,dn=rv.items.filter(x=>x.dec).length;
 return '<div class="sm" style="display:flex"><span class="muted">已决定</span><span class="sp"></span><span class="num">'+dn+' / '+tot+'</span></div><div class="mini"><i style="width:'+(tot?dn/tot*100:0)+'%"></i></div>';
}
export function rvPanel(it){
 const B=S.batch,rv=B.rv,g=B.groups[it.gi],f=B.files[it.fi];
 const its=rv.items.filter(x=>x.gid===it.gid);const rest=its.filter(x=>!x.dec&&x!==it).length;
 const opt=(a,i)=>{const sel=it.dec===a.k;return '<button class="dopt '+(sel?'on':'')+'" data-a="rvDo" data-v="'+a.k+'" aria-pressed="'+sel+'"><span class="key">'+'AB'[i]+'</span><span>'+a.l+'</span>'+(a.n?'<span class="dn2">'+a.n+'</span>':'')+(sel?'<span class="ck">'+I('check',16,2.4)+'</span>':'')+'</button>'};
 let acts='<div class="ask">'+(it.dec?'已决策 · 可更改':'请决策')+'</div>';
 if(g.id==='pwd'&&it.dec!=='pwd')acts+='<label class="field" style="margin:0 0 8px">'+I('lock',15)+'<input type="password" id="rvPwd" placeholder="输入密码后选 A" aria-label="密码"></label>';
 acts+='<div class="acts-v">'+g.acts.map(opt).join('')+'</div>';
 if(!it.dec&&rest&&g.id!=='pwd'&&!g.noBulk)acts+='<label class="rest"><input type="checkbox" id="rvAll" '+(rv.all?'checked':'')+'> 本组其余 <span id="rvRest" class="num">'+rest+'</span> '+g.unit+'一起</label>';
 acts+='<div class="navs"><button class="btn btn-s" data-a="rvStep" data-v="-1" data-tip="↑" data-tt>'+I('chev-l',15)+'上一条</button><button class="btn btn-s" data-a="rvStep" data-v="1" data-tip="↓" data-tt>下一条'+I('chev-r',15)+'</button></div>';
 return '<aside class="rvp">'+
  '<div class="rvp-h"><div class="gi2 '+(g.block?'block':'')+'">'+I(g.ic,17)+'</div><div style="min-width:0;flex:1"><div class="t">'+g.t+'</div><div class="sm muted num" id="rvIdx">'+(its.indexOf(it)+1)+' / '+its.length+'</div></div></div>'+
  '<div class="rvp-b"><dl class="kv"><dt>文件</dt><dd>'+esc(f.name)+'</dd><dt>位置</dt><dd>'+rvWhere(g,f,it)+'</dd><dt>依据</dt><dd>'+(g.id==='fail'?esc(f.failWhy||''):RVSRC[g.id])+(g.found&&g.found[it.fi]?' · 导出后验证发现':'')+'</dd></dl>'+acts+'</div>'+
  '<div class="rvp-f"><div id="rvpf">'+rvFoot()+'</div></div>'+
 '</aside>';
}
export function rvDone(){
 const B=S.batch,rv=B.rv;const by={};
 rv.items.forEach(x=>{if(!x.dec)return;const l=decLabel(B.groups[x.gi],x.dec);by[l]=(by[l]||0)+1});
 return '<div class="rvbox"><div class="card donecard"><div class="done-ic">'+I('check',28,2.2)+'</div><h2>复核完成</h2>'+
  '<div class="stats">'+Object.entries(by).map(([l,v])=>'<div><b class="num">'+v+'</b><span>'+l+'</span></div>').join('')+'</div>'+
  '<div id="rvExp">'+rvExpHtml()+'</div></div></div>';
}
/* the reviewed files are exported in the background after each decision; show that like the single-file export */
export function rvExpHtml(){
 const B=S.batch,rv=B.rv,c=counts(),ap=B.autoPub;
 const mine=[...new Set(rv.items.filter(x=>x.dec&&x.dec!=='skip').map(x=>x.fi))].filter(fi=>B.files[fi].st!=='skipped');
 const n=k=>mine.filter(fi=>k.includes(B.files[fi].st)).length;
 const N=mine.length,out=n(['done','partial']),rd=n(['ready']),run=n(['queue','proc']),bad=n(['bad']);
 const ok=ap?out:out+rd,busy=c.queue+c.proc,other=busy-run,pend=rv.items.filter(x=>!x.dec).length;
 let h='';
 if(N){
  const seg=(v,col)=>v?'<i style="width:'+(v/N*100)+'%;background:'+col+'"></i>':'';
  const verb=ap?'导出':'验证',left=N-ok-bad-n(['wait','held'])>0;
  h+='<div class="rvx"><div class="rvx-h"><span>'+(left?'正在'+verb+'你复核的 '+fmt(N)+' 份':ok===N?'你复核的 '+fmt(N)+' 份'+(ap?'已导出':'已通过验证'):'你复核的 '+fmt(N)+' 份中 '+fmt(N-ok)+' 份'+(ap?'未导出':'未通过验证'))+'</span><span class="num">'+(ap?'已导出 ':'已验证 ')+fmt(ok)+' / '+fmt(N)+'</span></div>'+
   '<div class="bar">'+seg(ok,'var(--ok)')+(ap?seg(rd,'#6C8FC0'):'')+seg(bad,'var(--bad)')+'</div>'+
   '<div class="rvx-s">'+(ap?'每份导出后重新打开验证，通过后写入 '+esc(B.out):'验证通过后由你确认导出')+(bad?' · 无法处理 '+fmt(bad)+' 份':'')+'</div></div>';
 }
 if(pend)h+='<div class="rvx-warn">'+I('alert',14)+'验证中发现 '+fmt(pend)+' 处需要你处理</div>';
 const note=[];
 if(other)note.push('另有 '+fmt(other)+' 份其他文件处理中');
 if(!pend){
  if(settled())note.push('即将显示结果');
  else if(!ap&&!busy)note.push('<span style="color:var(--warn)">'+fmt(c.ready)+' 份已通过验证，待你确认导出</span>');
  else if(ap)note.push('完成后自动显示结果');
 }
 const exp=!pend&&!ap&&c.ready&&!busy;
 const btn=pend?'<button class="btn btn-s" data-a="bphase" data-v="run">返回批次</button><button class="btn btn-p" data-a="rvResume">继续复核</button>'
  :exp?'<button class="btn btn-p" data-a="bphase" data-v="run">返回批次导出</button>'
  :'<button class="btn btn-s" data-a="bphase" data-v="run">返回批次</button>';
 return h+(note.length?'<div class="muted sm rvx-n">'+note.join('<br>')+'</div>':'')+'<div class="acts">'+btn+'</div>';
}
export function bindRv(){
 fitCmp();
 const sc=$('#rvDoc'),t=sc&&sc.querySelector('.tgt');
 if(t)sc.scrollTop=Math.max(0,t.getBoundingClientRect().top-sc.getBoundingClientRect().top-sc.clientHeight*.32);
 const q=$('#rvq'),c=q&&q.querySelector('[aria-current=true]');
 if(c){const a=c.getBoundingClientRect(),b=q.getBoundingClientRect();if(a.top<b.top+40||a.bottom>b.bottom-40)q.scrollTop+=a.top-b.top-b.height*.4}
 const p=$('#rvPwd');if(p)p.focus({preventScroll:true});
}
export function patchRv(){
 const rv=S.batch.rv;const had=rv&&rv.cur;
 const ch=rvSync();
 const ex=$('#rvExp');if(ex&&!rv.cur){const h=rvExpHtml();if(ex._h!==h){ex.innerHTML=h;ex._h=h}}
 if(!ch)return;
 if(rv.cur!==had){render();return}
 const q=$('#rvq');if(q){const c0=q.querySelector('[aria-current=true]'),y0=c0&&c0.getBoundingClientRect().top;const st=q.scrollTop;q.innerHTML=rvQueue();q.scrollTop=st;const c1=q.querySelector('[aria-current=true]');if(c0&&c1)q.scrollTop+=c1.getBoundingClientRect().top-y0}
 const pf=$('#rvpf');if(pf)pf.innerHTML=rvFoot();
 const it=rvCur(),rs=$('#rvRest'),ix=$('#rvIdx');if(it){const its=rv.items.filter(x=>x.gid===it.gid);if(rs)rs.textContent=its.filter(x=>!x.dec&&x!==it).length;if(ix)ix.textContent=(its.indexOf(it)+1)+' / '+its.length}
 $('#rail').innerHTML=railHtml();
}

