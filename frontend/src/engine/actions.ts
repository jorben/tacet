// @ts-nocheck

// @generated-from docs/v1/tacet-prototype.html (script section: actions); maintained by hand after the port.
import { $, $$, esc, fmt, ri, pick, I, rt } from './runtime';
import { TYPES, buildDoc, locStr, OFFICE_CATS, MODEL_LABELS } from '../domain/data';
import { modelDownload, contextModelId, modelById, stopModelTimer, modelPrepare, modelBusy, needModel, presetModelId } from './models';
import { render, openSubpage } from './shell';
import { closeModal, closePop, toast, menu, modal, openPop } from './layer';
import { S } from './state';
import { leaveBatchFile, newBatch, demoNames, startBatch, gMembers, gTotal, resolve, openFileFromBatch, machineDone, counts, sampleDrawer, patchBatch, batchStatus } from './batch';
import { liveHistory, historyRows, openHistoryResult, needSlot, resumeBatch, parkSingle, keepDays, parkBatch, slotBusy } from './home';
import { setMode, hitEl, hitPop, setHit, orderedHits, focusHit, actOf, pushUndo, doUndo, outKind, runSingleProc, exportSingle, addManual, currentManifest } from './single';
import { enterReview, rvSync, rvDecide, rvStep, rvCur } from './review';
import { bump, confOf, presetConf, fmtConf, impModal, curSet, verModal, ruleModal, rdRead, verTarget, availSets, impClash, impDo, curPreset } from './presets';
import { installModal } from './plugins';

export const A={
 modelDownload(el){modelDownload(el?.dataset.v||contextModelId())},
 modelPause(el){const id=el?.dataset.v||contextModelId(),m=modelById(id);if(m?.status!=='downloading')return;stopModelTimer(id);m.status='paused';render()},
 modelPrepare(el){modelPrepare(el?.dataset.v||contextModelId())},
 modelManage(el){closeModal();closePop();if(S.view!=='plugins')openSubpage('plugins');else render();const id=el?.dataset.v;($$('.model-card').find(e=>id&&e.dataset.model===id)||$('#modelPlugins'))?.scrollIntoView({block:'center'})},
 modelCancel(){S.modelPending=null;render()},
 modelContinue(){const p=S.modelPending;if(!p||modelById(p.modelId)?.status!=='ready'||modelBusy())return;S.modelPending=null;render();p.go()},
 go(el){closeModal();closePop();const r=leaveBatchFile();S.navStack=[];S.view=el.dataset.v;render();if(r)toast(r==='reexp'?'已保存 · 该文件将重新导出':'已保存修改')},
 pick(){$('#fileIn').click()},
 homePreset(el){menu(el,[...S.presets.map(p=>({l:p.name,s:p.ver,k:p.name,on:S.homePreset===p.name})),'-',{l:'管理方案',ic:'sliders',k:'_m'}],it=>{if(it.k==='_m'){openSubpage('presets',{ptab:'scheme'});return}S.homePreset=it.k;render()},{alignRight:true})},
 allHistory(el){const h=liveHistory();modal('<h3 id="historyTitle">最近任务</h3><div class="ms">'+h.length+' 个任务</div><div class="list home-history">'+historyRows(h)+'</div><div class="mf"><button class="btn btn-g" data-a="closeModal">关闭</button></div>');const m=$('#layer .modal');m.classList.add('history-modal');m.setAttribute('aria-labelledby','historyTitle');rt.historyTrigger=el;m.querySelector('.hrow,.btn').focus();},
 openHist(el){closeModal();closePop();const x=liveHistory()[+el.dataset.v];
  if(['done','part'].includes(x.st)||x.stl.startsWith('已导出')){openHistoryResult(x);return}
  if(x.live){S.view=x.live;render();return}
  if(x.park){needSlot('single',()=>{S.history=S.history.filter(h=>h!==x);S.single=x.saved;S.undo=x.undo||[];S.view='single';render()});return}
  if(x.saved&&x.k==='file'){needSlot('single',()=>{S.single=x.saved;S.undo=[];S.view='single';render()});return}
  S.histX=x;
  if(x.st==='keep'){modal('<h3>'+esc(x.name)+'</h3><div class="ms">'+x.meta+' · '+x.stl+'</div><div class="mb">继续后从中断处处理，已提交的决定保留。</div><div class="mf"><button class="btn btn-d" data-a="histClean" style="margin-right:auto">停止并清理</button><button class="btn btn-g" data-a="closeModal">取消</button><button class="btn btn-p" data-a="histResume">继续处理</button></div>');return}
  if(x.saved&&x.k==='batch'){needSlot('batch',()=>resumeBatch(x));return}
  modal('<h3>'+esc(x.name)+'</h3><div class="ms">'+x.meta+' · '+x.when+'</div><div class="mb"><dl class="kv"><dt>状态</dt><dd>'+x.stl+'</dd><dt>位置</dt><dd>桌面 / 已脱敏</dd></dl></div><div class="mf"><button class="btn btn-g" data-a="closeModal">关闭</button><button class="btn btn-p" data-a="reveal">'+I('folder',15)+'打开导出文件夹</button></div>')},
 histResume(){const x=S.histX;closeModal();needSlot('batch',()=>{if(x.saved)resumeBatch(x);else{S.history=S.history.filter(h=>h!==x);newBatch(demoNames(parseInt(x.meta)||26),{title:x.name});startBatch()}toast('已恢复，继续处理')})},
 histClean(){const x=S.histX;closeModal();S.history=S.history.filter(h=>h!==x);render();toast('已清理「'+esc(x.name)+'」')},
 slotView(){closeModal();S.view=S.slotKind;render()},
 slotPark(){closeModal();const k=S.slotKind,go=S.slotGo;S.slotGo=null;
  if(k==='single'){parkSingle();toast('已保存到“最近”')}
  else if(S.batch.phase==='precheck')S.batch=null;
  else{const d=keepDays(S.batch);parkBatch(d);toast('当前批次已保存 '+d+' 天，可在“最近”中继续')}
  go&&go()},
 cancelSingle(){clearInterval(rt.procT);S.single=null;S.view='home';render();toast('已停止')},
 mode(el){closePop();setMode(el.dataset.v)},
 hit(el,ev){ev.stopPropagation();const id=+el.dataset.v;const s=S.single;s.focus=id;s.popFx=!!el.closest('.paper.fx');if(s.hits[id].low)s.hits[id].seen=true;render();const e=hitEl(id);if(e)openPop(e,hitPop(id),{role:'dialog',focus:false})},
 setHit(el){setHit(el.dataset.v)},
 nextLow(){const s=S.single;const ids=orderedHits().filter(i=>s.hits[i].low&&!s.hits[i].seen);if(ids.length)focusHit(ids[0])},
 focusType(el){const s=S.single;const t=el.dataset.v;const ids=orderedHits().filter(i=>s.hits[i].type===t);const cur=ids.indexOf(s.focus);focusHit(ids[(cur+1)%ids.length])},
 catMenu(el){const t=el.dataset.v;const s=S.single;menu(el,[{h:TYPES[t].l},{l:'涂抹',s:'■■■',k:'mask',on:actOf(t)==='mask'},{l:'占位符',s:'［'+TYPES[t].ph+'］',k:'ph',on:actOf(t)==='ph'},'-',{l:'全部保留',k:'keepAll'}],it=>{pushUndo();if(it.k==='keepAll'){s.hits.forEach(h=>{if(h.type===t){h.state='keep';h.seen=true}});render();toast('保留全部'+TYPES[t].l,doUndo)}else{s.catAct[t]=it.k;if(s.mode==='orig'&&it.k==='ph')s.mode='fx';render()}},{alignRight:true})},
 imgAllMenu(el){const s=S.single,v=outKind(s.ext).verb,k=s.imgs.filter(i=>i.act==='keep').length;menu(el,[{h:'全部 '+s.imgs.length+' 张图像'},{l:v+'全部',k:'remove',on:!k},{l:'保留全部',s:'部分结果',k:'keep',on:k===s.imgs.length}],it=>{pushUndo();s.imgs.forEach(i=>i.act=it.k);render()})},
 imgMenu(el,ev){ev.stopPropagation();const s=S.single,im=s.imgs[+el.dataset.v],v=outKind(s.ext).verb;menu(el,[{h:im.label+' · '+locStr(s.ext,im.page+1,im.sec,im.para)},{l:v,k:'remove',on:im.act!=='keep'},{l:'保留',s:'部分结果',k:'keep',on:im.act==='keep'}],it=>{if(im.act===it.k)return;pushUndo();im.act=it.k;render();toast((it.k==='keep'?'保留':v)+' · '+im.label,doUndo)})},
 presetMenu(el){const s=S.single;menu(el,[...S.presets.map(p=>({l:p.name,s:p.ver,k:p.name,on:s.preset===p.name})),'-',{l:'管理方案',ic:'sliders',k:'_m'}],it=>{
  if(it.k==='_m'){openSubpage('presets',{ptab:'scheme'});return}
  if(it.k===s.preset)return;
  S.presetGo=()=>{closeModal();if(!needModel(it.k,()=>S.presetGo(),s.name+' · 切换方案'))return;s.preset=it.k;s.modelId=presetModelId(it.k);Object.assign(s,buildDoc(),{catAct:{},focus:-1,phase:'processing',stage:0,sp:0});S.undo=[];render();runSingleProc();toast('已切换 · 重新检测')};
  if(S.undo.length)modal('<h3>切换到「'+it.k+'」</h3><div class="ms">将按新方案重新检测，你在此文件上的修改会失效</div><div class="mf"><button class="btn btn-g" data-a="closeModal">取消</button><button class="btn btn-p" data-a="presetGo">切换并重新检测</button></div>');else S.presetGo()})},
 presetGo(){S.presetGo&&S.presetGo()},
 ret(){closeModal();closePop();const r=S.navStack.pop();if(r)Object.assign(S,r);else S.view='home';render()},
 outMenu(el){const s=S.single;menu(el,[{l:'桌面 / 已脱敏',k:'桌面 / 已脱敏',on:s.out==='桌面 / 已脱敏'},{l:'与原文件相同位置',k:'原文件旁',on:s.out==='原文件旁'},'-',{l:'选择…',ic:'folder',k:'文稿 / 脱敏'}],it=>{s.out=it.k;render()})},
 exportSingle(){exportSingle()},
 undo(){doUndo()},
 keys(el){openPop(el,'<div class="keys"><kbd>J / ↓</kbd><span>下一处</span><kbd>K / ↑</kbd><span>上一处</span><kbd>X</kbd><span>保留 / 涂抹</span><kbd>E</kbd><span>原文 / 效果</span><kbd>C</kbd><span>左右对比</span><kbd>⌘ Z</kbd><span>撤销</span><kbd>⌘ ↩</kbd><span>导出</span></div>',{alignRight:true,focus:false})},
 toPage(el){const p=$('#pg'+el.dataset.v);const sc=$('#docScroll');sc.scrollTo({top:p.offsetTop-24,behavior:'smooth'})},
 addManual(el){addManual(el.dataset.v)},
 singlePhase(el){if(S.view==='history'){A.historyPhase(el);return}S.single.resultPhase=el.dataset.v;closeModal();render()},
 reveal(){closeModal();toast('已在访达中显示')},
 fakeSave(el){closeModal();toast('已保存 '+el.dataset.v)},
 closeModal(){closeModal()},
 menuPick(el){const it=rt.menuCb.items[+el.dataset.v];const cb=rt.menuCb;closePop();cb(it)},
 toastUndo(){const f=rt.toastUndo;const t=$('#layer .toast');if(t)t.remove();rt.toastUndo=null;f&&f()},
 backBatch(){closePop();const r=leaveBatchFile();S.view='batch';render();if(r)toast(r==='reexp'?'已保存 · 该文件将重新导出并替代旧版本':'已保存修改')},
 bfReview(){const fi=S.single.bfi;closePop();const r=leaveBatchFile();S.view='batch';enterReview(null,fi);if(r)toast('已保存修改')},
 /* batch */
 bmode(el){if(S.modelPending){toast('请先取消等待的操作再修改设置');return}S.batch.mode=el.dataset.v;render()},
 bpreset(el){if(S.modelPending){toast('请先取消等待的操作再修改方案');return}const B=S.batch;menu(el,S.presets.map(p=>({l:p.name,s:p.ver,k:p.name,on:B.preset===p.name})),it=>{B.preset=it.k;B.modelId=presetModelId(it.k);render()},{alignRight:true})},
 bimg(el){const B=S.batch;menu(el,[{l:'涂抹',k:'remove',on:B.imgAct==='remove'},{l:'保留',s:'部分结果',k:'keep',on:B.imgAct==='keep'}],it=>{B.imgAct=it.k;render()},{alignRight:true})},
 bout(el){const B=S.batch;menu(el,[{l:'桌面 / 已脱敏',k:'桌面 / 已脱敏',on:B.out==='桌面 / 已脱敏'},{l:'选择…',ic:'folder',k:'文稿 / 脱敏批次'}],it=>{B.out=it.k;render()},{alignRight:true})},
 bauto(){S.batch.autoPub=!S.batch.autoPub;render()},
 bkeep(el){const B=S.batch;menu(el,[{l:'保留 7 天',k:'7',on:B.keep==='7'},{l:'保留 30 天',k:'30',on:B.keep==='30'},{l:'不保留',k:'0',on:B.keep==='0'}],it=>{B.keep=it.k;render()},{alignRight:true})},
 showBad(el){const bad=S.batch.files.filter(f=>f.st==='bad');openPop(el,'<div class="mh">无法处理 · 不会导出</div>'+bad.map(f=>'<div class="mi ro"><span>'+esc(f.name)+'</span><small>'+f.reason+'</small></div>').join(''),{role:'dialog',focus:false})},
 cancelBatch(){S.modelPending=null;S.batch=null;S.view='home';render()},
 startBatch(){startBatch()},
 review(el){if(S.view==='history'){const go=()=>{S.view='batch';enterReview(el.dataset.v||null)};if(S.historyResult.data===S.batch)go();else needSlot('batch',()=>{resumeBatch(S.historyResult.entry);go()});return}enterReview(el.dataset.v||null)},
 gMenu(el){const g=S.batch.groups.find(x=>x.id===el.dataset.v);if(g.noBulk)return;const mem=gMembers(g),n=gTotal(g,mem);menu(el,[{h:'整组 · '+fmt(n)+' '+g.unit},...g.acts.map(a=>({l:a.k==='pwd'?'输入密码…':'全部'+a.l,s:a.k==='pwd'?'':fmt(n),k:a.k}))],it=>{
  if(it.k==='pwd'){pwdModal(g.id);return}
  if(it.k==='keep'||it.k==='skip'){const a=g.acts.find(x=>x.k===it.k);S.bulk={gid:g.id,k:it.k};
   modal('<h3>全部'+a.l+' · '+fmt(n)+' '+g.unit+'</h3><div class="ms">'+g.t+' · 涉及 '+fmt(mem.length)+' 份文件</div><div class="mb">'+(it.k==='skip'?'这些文件将不导出，计入未导出范围。':g.gap?'未检查的内容将原样保留，这 '+fmt(mem.length)+' 份将导出为部分结果。':'这些内容将原样保留在导出文件中。')+'<div class="muted sm" style="margin-top:8px">建议先在复核中查看样例</div></div><div class="mf"><button class="btn btn-g" data-a="closeModal" style="margin-right:auto">取消</button><button class="btn btn-s" data-a="bulkView">先去复核</button><button class="btn '+(it.k==='skip'?'btn-d':'btn-p')+'" data-a="bulkDo">全部'+a.l+'</button></div>');return}
  resolve(g.id,it.k)},{alignRight:true})},
 bulkDo(){const b=S.bulk;closeModal();resolve(b.gid,b.k)},
 bulkView(){const b=S.bulk;closeModal();enterReview(b.gid)},
 rvGo(el){const rv=S.batch.rv;rv.cur=el.dataset.v;rv.hov=null;rv.fin=false;render()},
 rvResume(){const rv=S.batch.rv;rv.fin=false;rv.hov=null;rvSync();render()},
 rvMode(el){S.docMode=el.dataset.v;render()},
 rvDo(el){rvDecide(el.dataset.v)},
 rvStep(el){rvStep(+el.dataset.v)},
 rvOpen(){const it=rvCur();if(it)openFileFromBatch(it.fi)},
 focusHit(el){focusHit(+el.dataset.v)},
 btab(el){S.batch.tab=el.dataset.v;render()},
 historyPhase(el){S.historyResult.phase=el.dataset.v;closeModal();render()},
 bphase(el){if(S.view==='history'){A.historyPhase(el);return}let v=el.dataset.v;if(v==='run'&&machineDone())v='result';S.batch.phase=v;closeModal();render()},
 pauseBatch(){const B=S.batch;B.paused=!B.paused;render();toast(B.paused?'已暂停派发，进行中的会完成':'已继续')},
 stopBatch(el){const B=S.batch,d=keepDays(B);menu(el,[{l:'停止并保留以便恢复',ic:'clock',k:'keep',s:d+' 天'},{l:'停止并清理',ic:'trash',k:'clean',d:true}],it=>{
  if(it.k==='keep'){parkBatch(d);toast('已保存 '+d+' 天，可在“最近”中继续')}
  else{clearInterval(rt.bT);const c=counts(),dn=c.done+c.partial;S.history.unshift({k:'batch',name:B.title,meta:fmt(B.n)+' 份',st:'stop',stl:'已停止 · 已导出 '+fmt(dn)+' / '+fmt(B.n),when:'刚刚'});S.batch=null;toast('已停止并清理；已导出的 '+fmt(dn)+' 份不受影响')}
  S.view='home';render()},{alignRight:true})},
 resolve(el){const[g,k]=el.dataset.v.split('|');if(k==='pwd'){pwdModal(g);return}resolve(g,k)},
 peek(el,ev){ev.preventDefault();ev.stopPropagation();el.innerHTML=I('eye',12)+'3301 0619 9203 15'+(40+(+el.dataset.v)%59);setTimeout(()=>{if(el.isConnected)el.innerHTML=I('eye',12)+'3301 •••• •••• ••'+(40+(+el.dataset.v)%59)},2500)},
 peekS(el){const o=el.innerHTML;el.innerHTML=I('eye',12)+el.textContent.replace(/•+/g,m=>'8'.repeat(m.length).replace(/8/g,()=>String(ri(0,9))));setTimeout(()=>{if(el.isConnected)el.innerHTML=o},2500)},
 sample(el){sampleDrawer(el.dataset.v)},
 sampleMore(el){sampleDrawer(el.dataset.v)},
 sampleOpen(el){openFileFromBatch(+el.dataset.v)},
 openFile(el,ev){ev.stopPropagation();ev.preventDefault();openFileFromBatch(+el.dataset.v)},
 ff(el){S.batch.ff=el.dataset.v;S.batch.lim=60;S.batch.live={};render()},
 moreFiles(){S.batch.lim=(S.batch.lim||60)+60;patchBatch(true)},
 mf(el){const B=currentManifest();B.mf=el.dataset.v;B.mlim=80;render()},
 moreMan(){const B=currentManifest();B.mlim=(B.mlim||80)+80;render()},
 expReady(){const B=S.batch,c=counts();const man=B.files.filter(f=>f.st==='ready'&&f.man).length;const part=B.files.filter(f=>f.st==='ready'&&f.partial).length;
  modal('<h3>导出 '+fmt(c.ready)+' 份</h3><div class="ms">到 '+B.out+'</div><div class="mb"><dl class="kv"><dt>自动</dt><dd>'+fmt(c.ready-man)+' 份</dd><dt>含人工决定</dt><dd>'+fmt(man)+' 份</dd><dt>部分结果</dt><dd>'+fmt(part)+' 份'+(part?' · 文件名带标记':'')+'</dd><dt>仍未导出</dt><dd>'+fmt(c.wait+c.held+c.bad+c.queue+c.proc)+' 份</dd></dl></div><div class="mf"><button class="btn btn-g" data-a="closeModal">取消</button><button class="btn btn-p" data-a="expDo">导出</button></div>')},
 expDo(){const n=counts().ready;S.batch.files.forEach(f=>{if(f.st==='ready')f.st=f.partial?'partial':'done'});closeModal();patchBatch(true);toast('已导出 '+fmt(n)+' 份')},
 /* presets */
 ptab(el){S.ptab=el.dataset.v;render()},
 pdet(el){const p=S.presets.find(x=>x.id===S.presetSel);if(p.det===el.dataset.v)return;bump(p);p.det=el.dataset.v;['org','name','addr'].forEach(t=>p.cats[t]=p.det==='rules'?'na':(p.cats[t]==='na'?'mask':p.cats[t]));if(p.det==='model'&&p.conf==null)p.conf=confOf(modelById(p.model)).rec;render()},
 pmodel(el){const p=S.presets.find(x=>x.id===S.presetSel)||S.presets[0];menu(el,[{h:'检测模型'},...S.models.map(m=>({l:esc(m.name),s:esc(m.languages)+' · '+MODEL_LABELS[m.status],k:m.id,on:p.model===m.id})),'-',{l:'管理模型插件',ic:'puzzle',k:'_p'}],it=>{if(it.k==='_p'){openSubpage('plugins');return}if(p.builtin||p.model===it.k)return;bump(p);p.model=it.k;
  // A threshold means nothing on another model's scale, so switching models resets it to the new default
  const was=presetConf(p),r=confOf(modelById(it.k)).rec;p.conf=r;render();if(was!==r)toast('最低置信度已重置为 '+fmtConf(r)+'（'+esc(modelById(it.k)?.name||'所选模型')+' 的推荐值）')},{alignRight:true})},
 prules(el){const p=S.presets.find(x=>x.id===S.presetSel);menu(el,[{h:'规则集 · 可多选'},...availSets().map(rs=>({l:rs.name,s:rs.ver,k:rs.id,on:p.rules.includes(rs.id)})),'-',{l:'管理规则',ic:'sliders',k:'_r'}],it=>{
  if(it.k==='_r'){openSubpage('presets',{ptab:'rules'});return}
  const has=p.rules.includes(it.k);if(has&&p.rules.length===1){toast('至少保留一个规则集');return}
  bump(p);p.rules=has?p.rules.filter(x=>x!==it.k):p.rules.concat(it.k);render();const b=$('[data-a=prules]');if(b)A.prules(b)},{alignRight:true})},
 rsel(el){S.rsSel=el.dataset.v;render()},
 rsnew(){const id='rs'+Date.now();S.rulesets.push({id,name:'新规则集',ver:'v1',rules:[]});S.rsSel=id;render()},
 rsimp(){impModal('r')},
 rscopy(){const r=curSet();const id='rs'+Date.now();S.rulesets.push(Object.assign(JSON.parse(JSON.stringify(r)),{id,name:r.name+' 副本',ver:'v1',builtin:false,plugin:null,edited:'刚刚',hist:[]}));S.rsSel=id;render();toast('已复制')},
 rsmore(el){const r=curSet();const lock=r.builtin||r.plugin;menu(el,[...(lock?[]:[{l:'历史版本',ic:'clock',k:'hist'}]),{l:'导出',ic:'download',k:'exp'},...(lock?[]:['-',{l:'删除',ic:'trash',k:'del',d:true}])],it=>{
  if(it.k==='hist'){S.verKind='r';verModal();return}
  if(it.k==='exp'){toast('已导出 '+r.name);return}
  const used=S.presets.filter(p=>p.rules.includes(r.id));if(used.length){toast('正在被「'+used[0].name+'」使用');return}
  S.rulesets=S.rulesets.filter(x=>x!==r);S.rsSel=null;render();toast('已删除')},{alignRight:true})},
 rtog(el){const r=curSet();bump(r);const x=r.rules[+el.dataset.v];x.on=!x.on;render()},
 radd(){S.rd={idx:null,name:'',kind:'词表',cat:'other',bd:'',body:''};ruleModal()},
 redit(el){const x=curSet().rules[+el.dataset.v];S.rd={idx:+el.dataset.v,name:x.name,kind:x.kind,cat:x.cat,bd:x.bd||'',body:x.kind==='词表'&&x.n&&!x.body?'':x.body};ruleModal()},
 rdKind(el){rdRead();S.rd.kind=el.dataset.v;ruleModal()},
 rdCat(el){rdRead();S.rd.cat=el.dataset.v;ruleModal()},
 rdBd(el){rdRead();S.rd.bd=el.dataset.v;ruleModal()},
 rdSave(){rdRead();const d=S.rd;if(!d.name.trim()){$('#rdName').focus();return}if(!d.body.trim()){$('#rdBody').focus();return}
  // Go regex (RE2) has no lookaround; boundaries are a declared attribute instead
  if(d.kind==='正则'&&/\(\?<?[=!]/.test(d.body)){toast('不支持前后断言（如 (?&lt;!…)），请改用“前后边界”');$('#rdBody').focus();return}
  const r=curSet();const x={name:d.name.trim(),kind:d.kind,cat:d.cat,bd:d.kind==='正则'?d.bd||'':'',body:d.body.trim(),on:true};
  const o=d.idx!=null&&r.rules[d.idx];if(o&&['name','kind','cat','bd','body'].every(k=>(o[k]||'')===x[k])){closeModal();toast('内容未变，版本不变');return}
  bump(r);
  if(d.idx!=null)r.rules[d.idx]=Object.assign(r.rules[d.idx],x,{n:0});else r.rules.push(x);
  closeModal();render();toast('已保存为 '+r.ver)},
 rdDel(){const r=curSet();bump(r);r.rules.splice(S.rd.idx,1);closeModal();render();toast('已删除 · 保存为 '+r.ver)},
 verBack(el){const o=verTarget(),h=o.hist[+el.dataset.v],from=h.ver,data=JSON.parse(JSON.stringify(h.data));
  // A rolled-back preset may name rule sets deleted since; drop them, keep the current ones if none remain
  if(o.cats){data.rules=data.rules.filter(id=>availSets().some(x=>x.id===id));if(!data.rules.length)delete data.rules}
  bump(o);Object.assign(o,data);closeModal();render();toast('已用 '+from+' 的内容保存为 '+o.ver)},
 impGo(){const i=$('#impName'),n=i.value.trim();if(!n){i.focus();return}if(impClash(n)){$('#impErr').textContent='仍与已有名称重复';i.focus();return}impDo(n)},
 psel(el){S.presetSel=el.dataset.v;render()},
 pnew(){const id='p'+Date.now();S.presets.push({id,name:'新方案',ver:'v1',det:'model',model:'astr',conf:0.5,rules:['cn'],cats:Object.fromEntries(OFFICE_CATS.map(t=>[t,'mask'])),img:'remove',edited:'刚刚'});S.presetSel=id;render()},
 pconf(el){const p=curPreset();if(p.builtin)return;const m=modelById(p.model),c=confOf(m),cur=presetConf(p),vals=[];for(let i=Math.round(c.min*10);i<=9;i++)vals.push(i/10);
  menu(el,[{h:'最低置信度 · 低于此值不涂抹'},...vals.map(v=>({l:fmtConf(v),s:v===c.rec?'推荐 · 按 '+esc(m?.name||'所选模型')+' 校准':v<c.rec?'命中更多，误报增加':'命中更少，可能漏检',k:v,on:cur===v}))],it=>{if(cur===it.k)return;bump(p);p.conf=it.k;render()},{alignRight:true})},
 pcopy(){const p=S.presets.find(x=>x.id===S.presetSel);const id='p'+Date.now();S.presets.push(Object.assign(JSON.parse(JSON.stringify(p)),{id,name:p.name+' 副本',ver:'v1',builtin:false,edited:'刚刚',hist:[]}));S.presetSel=id;render();toast('已复制')},
 pcat(el){const[t,k]=el.dataset.v.split('|');const p=S.presets.find(x=>x.id===S.presetSel);if(p.cats[t]===k)return;bump(p);p.cats[t]=k;render()},
 pimp(){impModal('p')},
 pmore(el){const p=S.presets.find(x=>x.id===S.presetSel);menu(el,[...(p.builtin?[]:[{l:'历史版本',ic:'clock',k:'hist'}]),{l:'导出',ic:'download',k:'exp'},...(p.builtin?[]:['-',{l:'删除',ic:'trash',k:'del',d:true}])],it=>{if(it.k==='hist'){S.verKind='p';verModal()}else if(it.k==='del'){S.presets=S.presets.filter(x=>x!==p);S.presetSel='office';render();toast('已删除')}else toast('已导出（不含模型）')},{alignRight:true})},
 /* plugins */
 pltog(el){const p=S.plugins.find(x=>x.id===el.dataset.v);p.on=!p.on;render();toast(p.on?'已启用':'已停用 · 进行中的任务不受影响')},
 plinstall(){S.instKind='git';installModal()},
 instKind(el){S.instKind=el.dataset.v;installModal()},
 instNext(){const v=$('#instSrc')?$('#instSrc').value:'local/tacet-eml';installModal('review',v)},
 instDo(){closeModal();S.plugins.splice(S.plugins.findIndex(p=>p.builtin),0,{id:'eml'+Date.now(),name:'EML 邮件',role:'格式',fmt:'.eml',ver:'0.2.1',src:'github.com/mailtools/tacet-eml',commit:'8c41e07',on:true,trusted:false,refs:0});S.plugins.sort((a,b)=>(a.builtin?1:0)-(b.builtin?1:0));render();toast('已安装 EML 邮件')},
 fakeDir(){const e=$('#dirv');if(e){e.textContent='~/plugins/tacet-eml';e.classList.remove('muted')}else toast('选择文件…')},
 plmore(el){const p=S.plugins.find(x=>x.id===el.dataset.v);menu(el,[{l:p.trusted?'取消完成判定授权':'授权完成判定',s:'当前版本',ic:'shield',k:'trust',on:p.trusted},{l:'升级…',ic:'download',k:'up'},{l:'回退版本',ic:'undo',k:'down'},'-',{l:'卸载',ic:'trash',k:'rm',d:true}],it=>{
  if(it.k==='trust'){p.trusted=!p.trusted;render();toast(p.trusted?'结果可标为“已按策略完成”':'已取消授权')}
  else if(it.k==='rm'){if(p.refs)modal('<h3>卸载 '+p.name+'</h3><div class="ms">有 '+p.refs+' 个可恢复任务在使用此版本</div><div class="mf"><button class="btn btn-g" data-a="closeModal">等待</button><button class="btn btn-d" data-a="plrm" data-v="'+p.id+'">作废任务并卸载</button></div>');else{S.plugins=S.plugins.filter(x=>x!==p);render();toast('已卸载')}}
  else toast(it.k==='up'?'没有新版本':'已是最早版本')},{alignRight:true})},
 plrm(el){S.plugins=S.plugins.filter(x=>x.id!==el.dataset.v);closeModal();render();toast('已卸载 · 已导出的文件不受影响')},
 /* settings */
 theme(el){S.themePref=el.dataset.v;S.theme=el.dataset.v==='system'?((matchMedia('(prefers-color-scheme: dark)').matches)?'dark':'light'):el.dataset.v;render()},
 tier(el){S.set.tier=el.dataset.v;render()},
 skeep(el){menu(el,[{l:'保留 7 天',k:'7',on:S.set.keep==='7'},{l:'保留 30 天',k:'30',on:S.set.keep==='30'},{l:'不保留',k:'0',on:S.set.keep==='0'}],it=>{S.set.keep=it.k;render()},{alignRight:true})},
 autoUpd(){S.set.autoUpd=!S.set.autoUpd;render()},
 // 版本检查和安装仅演示状态变化，不发起网络请求或修改本机应用。
 checkUpd(){
  const u=S.update;if(['checking','updating'].includes(u.status))return;
  u.status='checking';render();
  setTimeout(()=>{u.latest=u.current==='1.0.1'?null:'1.0.1';u.status=u.latest?'available':'latest';render();toast(u.latest?'（原型）发现新版本 '+u.latest:'（原型）已是最新版本')},900);
 },
 updateApp(){
  const u=S.update;if(u.status!=='available')return;
  const pend=[],sb=slotBusy('single'),bb=slotBusy('batch');
  if(sb)pend.push(esc(sb.name)+' · '+(sb.phase==='review'?'待确认':'处理中'));
  if(bb)pend.push(esc(bb.title)+' · '+batchStatus()[1]);
  S.history.filter(h=>h.park||h.st==='keep').forEach(h=>pend.push(esc(h.name)+' · '+esc(h.stl)));
  if(!pend.length){modal('<h3>更新至 '+u.latest+'</h3><div class="ms">原型演示 · 不会下载更新包或安装应用</div><div class="mb"><p>更新包校验签名后安装，完成后重新打开 Tacet。当前没有未完成的任务。</p></div><div class="mf"><button class="btn btn-g" data-a="closeModal">取消</button><button class="btn btn-p" data-a="updateDo">更新</button></div>');return}
  modal('<h3>更新至 '+u.latest+'</h3><div class="ms">'+pend.length+' 个任务未完成 · 原型演示，不会下载或安装</div><div class="mb">'+pend.map(x=>'<div class="sm" style="padding:3px 0;color:var(--ink-2)">'+x+'</div>').join('')+'<p class="muted sm" style="margin-top:12px">更新会替换检测模型、规则与格式插件。可以先完成这些任务；也可以作废它们，或在更新后按新版本重新执行受影响的步骤，已提交的决定核对一致后保留。已导出的文件不受影响。</p></div><div class="mf"><button class="btn btn-g" data-a="closeModal" style="margin-right:auto">先完成</button><button class="btn btn-d" data-a="updateDo" data-v="drop">作废并更新</button><button class="btn btn-p" data-a="updateDo" data-v="rerun">更新后重新执行</button></div>');
 },
 updateDo(el){
  const u=S.update;if(u.status!=='available')return;const v=el&&el.dataset.v;
  closeModal();
  if(v==='drop'){if(slotBusy('single')){clearInterval(rt.procT);S.single=null;S.undo=[]}if(slotBusy('batch')){clearInterval(rt.bT);S.batch=null}S.history=S.history.filter(h=>!(h.park||h.st==='keep'))}
  u.status='updating';render();
  setTimeout(()=>{u.current=u.latest;u.latest=null;u.status='latest';render();toast('（原型）更新演示完成'+(v==='drop'?' · 已作废未完成的任务':v==='rerun'?' · 未完成的任务将按新版本重新执行':'')+' · 未下载或安装')},1200);
 },
 aboutLink(el){
  const label={website:'官网',discord:'Discord 社区',github:'GitHub'}[el.dataset.v];
  modal('<h3>Tacet '+label+'</h3><div class="ms">原型入口 · 地址待配置</div><div class="mb"><p>配置正式'+label+'地址后，将在浏览器中打开。</p></div><div class="mf"><button class="btn btn-p" data-a="closeModal">知道了</button></div>');
 },
 licenses(){
  modal('<h3>开源许可</h3><div class="ms">Tacet · Apache License 2.0</div><div class="mb"><dl class="kv"><dt>应用主体</dt><dd>Apache-2.0</dd><dt>插件 SDK</dt><dd>Apache-2.0</dd><dt>协议 Schema</dt><dd>Apache-2.0</dd></dl><p style="margin-top:16px">第三方插件及依赖组件遵循各自的许可，具体声明以安装包中的许可证清单为准。</p></div><div class="mf"><button class="btn btn-g" data-a="closeModal">关闭</button><a class="btn btn-s" style="text-decoration:none" href="https://www.apache.org/licenses/LICENSE-2.0" target="_blank" rel="noopener noreferrer">查看许可原文 '+I('ext',14)+'</a></div>');
 },
 cleanWs(){modal('<h3>清理工作区</h3><div class="ms">删除中间数据与恢复数据，已导出的文件不受影响</div><div class="mb"><dl class="kv"><dt>可恢复</dt><dd>'+(S.history.filter(h=>h.st==='keep').map(h=>esc(h.name)+' · '+h.meta).join('<br>')||'无')+'</dd><dt>占用</dt><dd>1.4 GiB</dd></dl></div><div class="mf"><button class="btn btn-g" data-a="closeModal">取消</button><button class="btn btn-d" data-a="cleanDo">清理</button></div>')},
 cleanDo(){closeModal();S.history=S.history.filter(h=>h.st!=='keep');toast('已清理 · 1.4 GiB')},
 closeWin(){
  const s=S.single,B=S.batch;
  if(S.set.closeAct==='tray'){toast('（原型）已最小化到托盘'+(S.batch||S.single?'，任务继续':''));return}
  const items=[];const sb=slotBusy('single'),bb=slotBusy('batch');
  if(sb)items.push(esc(sb.name)+' · '+(sb.phase==='review'?'待确认':'处理中'));
  if(bb)items.push(esc(bb.title)+' · '+batchStatus()[1]);
  S.history.filter(h=>h.park).forEach(h=>items.push(esc(h.name)+' · 待确认'));
  if(!items.length){toast('（原型）程序已关闭');return}
  const d=bb&&bb.keep!=='0'?bb.keep:(S.set.keep!=='0'?S.set.keep:'7');
  modal('<h3>有未完成的任务</h3><div class="ms">保存后加密保留 '+d+' 天，下次打开继续；停止将清理中间数据，已导出的文件不受影响</div><div class="mb">'+items.map(x=>'<div class="sm" style="padding:3px 0;color:var(--ink-2)">'+x+'</div>').join('')+'</div><div class="mf" style="margin-top:22px"><button class="btn btn-d" data-a="closeClean" style="margin-right:auto">停止并清理</button><button class="btn btn-g" data-a="closeModal">取消</button><button class="btn btn-p" data-a="closeKeep" data-v="'+d+'">保存并关闭</button></div>')},
 redact(el){S.set.redact=el.dataset.v;render()},
 closeAct(el){S.set.closeAct=el.dataset.v;render()},
 closeKeep(el){closeModal();const B=S.batch;if(B)B.paused=true;render();toast('（原型）程序已关闭 · 已加密保存 '+el.dataset.v+' 天')},
 closeClean(){closeModal();
  if(S.single&&S.single.from==='batch')leaveBatchFile();
  if(slotBusy('single')){clearInterval(rt.procT);S.single=null;S.undo=[]}
  const bb=slotBusy('batch');if(bb){clearInterval(rt.bT);const c=counts();if(bb.phase!=='precheck')S.history.unshift({k:'batch',name:bb.title,meta:fmt(bb.n)+' 份',st:'stop',stl:'已停止 · 已导出 '+fmt(c.done+c.partial)+' / '+fmt(bb.n),when:'刚刚'});S.batch=null}
  S.history=S.history.filter(h=>!h.park);S.view='home';render();toast('（原型）已停止并清理')},
};
export function pwdModal(gid,only){
 const B=S.batch,g=B.groups.find(x=>x.id===gid);const mem=only||gMembers(g);
 S.pwdCtx={gid,mem};
 modal('<h3>输入密码</h3><div class="ms">仅本次使用，不会保存</div><div class="mb">'+mem.map(fi=>'<div class="sm" style="margin-top:12px;color:var(--ink-2)">'+esc(B.files[fi].name)+'</div><label class="field">'+I('lock',15)+'<input type="password" aria-label="密码"></label>').join('')+'</div><div class="mf"><button class="btn btn-g" data-a="closeModal">取消</button><button class="btn btn-p" data-a="pwdDo">解锁</button></div>');
}
A.pwdDo=()=>{const c=S.pwdCtx;closeModal();resolve(c.gid,'pwd',c.mem)};

