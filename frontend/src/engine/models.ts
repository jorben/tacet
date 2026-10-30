// @ts-nocheck

// @generated-from docs/v1/tacet-prototype.html (script section: models); maintained by hand after the port.
import { $, $$, esc, I } from './runtime';
import { MODEL_LABELS, MODEL_ERRORS } from '../domain/data';
import { S } from './state';
import { busy } from './batch';
import { A } from './actions';
import { toast } from './layer';
import { render, saveUi, restoreUi } from './shell';

export const modelTimers=new Map();
export function getPreset(name){return S.presets.find(p=>p.name===name)||S.presets[0]}
export function presetNeedsModel(name){return getPreset(name).det==='model'}
export function modelById(id){return S.models.find(m=>m.id===id)}
export function presetModelId(name){return presetNeedsModel(name)?getPreset(name).model:null}
export function contextModelId(){
 if(S.modelPending)return S.modelPending.modelId;
 if(S.view==='batch'&&S.batch)return S.batch.modelId||presetModelId(S.batch.preset);
 if(S.view==='single'&&S.single)return S.single.modelId||presetModelId(S.single.preset);
 return presetModelId(S.homePreset);
}
export function modelBusy(id){const xs=id?[modelById(id)]:S.models;return xs.some(m=>m&&['downloading','verifying','loading'].includes(m.status))}
export function stopModelTimers(){modelTimers.forEach(t=>clearInterval(t));modelTimers.clear()}
export function stopModelTimer(id){clearInterval(modelTimers.get(id));modelTimers.delete(id)}
export function resetModels(status='ready'){stopModelTimers();S.models.forEach(m=>Object.assign(m,{status,progress:status==='ready'?100:0,error:null}));S.modelPending=null}
export function documentBusy(){return !!(S.single&&['processing','exporting'].includes(S.single.phase))||!!(S.batch&&S.batch.phase!=='precheck'&&S.batch.files.some(f=>['proc','queue','ready'].includes(f.st)))}
export function patchModelShell(){
 const e=$('#modelShell');
 const show=S.models.length>0&&S.models.every(m=>m.status==='missing');
 e.hidden=!show;
 e.innerHTML=I('cpu',13)+'<span>模型待下载</span>';
 e.setAttribute('aria-label','模型待下载，查看模型插件');e.dataset.v='';
 const n=$('#networkState'),note=S.models.some(x=>x.status==='downloading')?'正在下载模型 · 文档留在本机':'本机处理 · 未联网';
 n.dataset.tip=note;n.setAttribute('aria-label',note);
}
export function homeModelGuide(){
 if(S.modelPending)return modelCard(false,S.modelPending.modelId);
 const ready=S.models.filter(m=>m.status==='ready').length,busy=S.models.filter(m=>modelBusy(m.id)),errors=S.models.filter(m=>m.status==='error'),paused=S.models.filter(m=>m.status==='paused');
 if(ready&&!busy.length&&!errors.length&&!paused.length)return '';
 const desc=busy.length?'正在准备识别模型，可先选择文档。':errors.length?'模型准备需要处理，请前往插件页查看。':paused.length?'模型下载已暂停，已下载的部分会保留。':'请先下载隐私识别模型，以便支持更优识别效果和本地离线使用';
 return '<section class="model-card home-model-notice '+(errors.length?'error':'')+'" data-home-guide aria-label="模型准备引导"><span class="model-icon" aria-hidden="true">'+I(errors.length?'alert':'cpu',16)+'</span><p class="model-desc" role="status" aria-live="polite" aria-atomic="true">'+desc+'</p><div class="model-actions"><button class="btn btn-p" data-a="modelManage">'+(busy.length?'查看进度':errors.length?'查看并处理':paused.length?'继续准备':'前往下载')+'</button></div></section>';
}
export function modelCard(manage=false,id=contextModelId()){
 // A rules-only task can still wait for an unrelated resource operation to finish.
 if(!id)id=S.models.find(m=>modelBusy(m.id))?.id;
 const m=modelById(id),p=S.modelPending?.modelId===id?S.modelPending:null;
 if(!m)return id?'<section class="model-card error" data-model="'+esc(id)+'" aria-label="模型不可用"><div class="model-title">所选模型不可用</div><p class="model-desc">请在插件页检查此模型是否仍受支持，原任务不会自动替换模型。</p><button class="btn btn-s" data-a="modelManage">查看模型</button></section>':'';
 const st=m.status,error=st==='error';
 if(st==='ready'&&!p&&!manage)return '';
 const detail=error?MODEL_ERRORS[m.error]||MODEL_ERRORS.offline:null;
 const title=manage?esc(m.name):detail?detail[0]:({missing:'此方案需要的模型尚未下载',downloading:'正在下载此方案需要的模型',paused:'模型下载已暂停',verifying:'下载完成，正在校验',loading:'正在准备本地识别',ready:'此方案需要的模型已就绪'})[st];
 const desc=detail?detail[1]:({missing:'下载后支持本地离线使用，文档始终留在本机。',downloading:'可以先选择文档、浏览方案。此模型准备好后再开始检测。',paused:'已下载的部分会保留，继续时无需从头下载。',verifying:'正在检查此模型的文件是否完整，完成后进行本地试运行。',loading:'正在加载并用内置样例试运行，不会读取你的文档。',ready:'已通过文件校验与本地试运行，支持本地离线使用。'})[st];
 const btn=(a,t,cls='btn-s')=>'<button class="btn '+cls+'" data-a="'+a+'" data-v="'+esc(m.id)+'">'+t+'</button>';
 let acts='';
 if(st==='missing'||st==='paused'||error){acts+=btn(error&&m.error==='load'?'modelPrepare':'modelDownload',detail?detail[2]:st==='paused'?'继续下载':'下载模型','btn-p');if(error&&m.error==='load')acts+=btn('modelDownload','重新下载','btn-g')}
 if(st==='downloading')acts+=btn('modelPause','暂停','btn-g');
 if(!manage)acts+=btn('modelManage','查看模型','btn-g');
 if(manage&&st==='ready')acts+='<span class="model-ready" role="status">已就绪</span>';
 const tags=manage&&!['missing','ready'].includes(st)?'<span class="pill '+(error?'warn':'ghost')+'">'+MODEL_LABELS[st]+'</span>':'';
 const progress=st==='downloading'||st==='paused'?'<div class="model-progress"><progress max="100" value="'+m.progress+'" aria-label="'+esc(m.name)+' 下载进度"></progress><div class="model-progress-label" style="justify-content:flex-end"><span class="num">'+m.progress+'%</span></div></div>':'';
 return '<section class="model-card '+(error?'error':st==='missing'||manage&&st==='ready'?'intro':'')+'" data-model="'+esc(m.id)+'" data-manage="'+manage+'" aria-label="'+esc(m.name)+' 模型准备"><div class="model-head"><span class="model-icon">'+I(manage?'cpu':error?'alert':st==='ready'?'check':'cpu',18)+'</span><div class="model-copy"><div class="model-title" role="status" aria-live="polite" aria-atomic="true">'+title+(tags?'<span class="model-tags">'+tags+'</span>':'')+'</div>'+(manage&&!error?'':'<p class="model-desc">'+(manage&&error?esc(detail[0])+' · ':'')+desc+'</p>')+'<div class="model-meta">'+(manage?'':esc(p?.preset||'本次任务')+' · '+esc(m.name)+' · ')+esc(m.version)+' · '+esc(m.variant)+' · '+esc(m.languages)+(st==='missing'&&m.weightMiB?' · 权重约 '+m.weightMiB+' MiB':'')+'</div>'+
 progress+(acts?'<div class="model-actions">'+acts+'</div>':'')+'</div></div>'+
 (p?'<div class="model-pending"><span>'+esc(p.label)+(st==='ready'?' · 准备好了，可以继续':' · 等待 '+esc(m.name)+' 准备，尚未开始检测')+'</span>'+btn('modelCancel','取消本次操作','btn-g')+'<button class="btn btn-p" data-a="modelContinue" '+(st==='ready'&&!modelBusy()?'':'disabled')+'>继续处理</button></div>':'')+'</section>';
}
export function needModel(preset,go,label,boundId){
 const id=boundId||presetModelId(preset),m=modelById(id);
 if((!id||m?.status==='ready')&&!modelBusy())return true;
 if(S.modelPending){toast('请先继续或取消上一次等待的操作');return false}
 const waitingId=id||S.models.find(x=>modelBusy(x.id))?.id;
 S.modelPending={go,label,preset,modelId:waitingId};render();
 const card=$('.model-card');if(card)card.scrollIntoView({block:'nearest'});
 return false;
}
export function modelRefresh(){
 const u=saveUi(),a=document.activeElement,action=a&&a.dataset.a,id=a&&a.dataset.v;
 if(S.view==='batch'&&S.batch?.phase==='precheck')render();
 else $$('.model-card').forEach(e=>{const h=e.hasAttribute('data-home-guide')?homeModelGuide():modelCard(e.dataset.manage==='true',e.dataset.model);if(h)e.outerHTML=h;else e.remove()});
 patchModelShell();restoreUi(u);
 if(action){const e=$$('[data-a="'+action+'"]').find(x=>x.dataset.v===id);if(e)e.focus({preventScroll:true});else if(!modelBusy())$('[data-a="modelContinue"],#modelShell')?.focus({preventScroll:true})}
}
export function modelTick(id=contextModelId()){
 const m=modelById(id);if(!m)return;
 if(m.status==='downloading'){m.progress=Math.min(100,m.progress+5);if(m.progress===100)m.status='verifying'}
 else if(m.status==='verifying')m.status='loading';
 else if(m.status==='loading'){m.status='ready';stopModelTimer(id);toast(esc(m.name)+' 已就绪'+(S.modelPending?.modelId===id?'，可以继续处理':'，可以离线使用'))}
 modelRefresh();
}
export function modelDownload(id=contextModelId()){
 const m=modelById(id);if(!m)return;
 if(documentBusy()){toast('请先完成或停止当前文档处理，再下载模型');return}
 if(modelBusy(id))return;
 if(m.error==='integrity'||m.error==='load')m.progress=0;
 m.error=null;m.status='downloading';stopModelTimer(id);render();modelTimers.set(id,setInterval(()=>modelTick(id),500));
}
export function modelPrepare(id=contextModelId()){
 const m=modelById(id);if(!m)return;
 if(documentBusy()){toast('请先完成或停止当前文档处理，再准备模型');return}
 stopModelTimer(id);m.error=null;m.status='loading';render();modelTimers.set(id,setInterval(()=>modelTick(id),900));
}

