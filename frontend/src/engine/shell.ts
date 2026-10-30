// @ts-nocheck

// @generated-from docs/v1/tacet-prototype.html (script section: shell); maintained by hand after the port.
import { $, $$, esc, fmt, I, LOGO } from './runtime';
import {  } from '../domain/data';
import { S } from './state';
import { batchStatus, machineDone, vBatch } from './batch';
import { vHome, vHistoryResult, bindDrop } from './home';
import { vSingle, bindDoc } from './single';
import { vPresets } from './presets';
import { vPlugins } from './plugins';
import { vSettings } from './settings';
import { modelCard, patchModelShell } from './models';
import { bindRv } from './review';

export function railHtml(){
 const cur={home:'home',single:'home',batch:'home',presets:'presets',plugins:'plugins',settings:'settings',history:'home'}[S.view];
 const b=(v,ic,tip,extra='')=>'<button data-a="go" data-v="'+v+'" data-tip="'+tip+'" aria-label="'+tip+'" '+(cur===v?'aria-current="page"':'')+'>'+I(ic,19)+extra+'</button>';
 const pend=S.batch&&S.view!=='batch'&&batchStatus()[0]==='warn';
 return b('home','inbox','任务',pend?'<span class="dotb"></span>':'')+b('presets','layers','方案')+b('plugins','puzzle','插件')+'<span class="sp"></span>'+b('settings','gear','设置');
}
export function titleHtml(){
 const s=S.single,B=S.batch;
 if(S.view==='history')return '<span>'+esc(S.historyResult.phase==='manifest'?'导出清单':S.historyResult.entry.name)+'</span>';
 if(S.view==='single'&&s)return '<span>'+esc(s.phase==='done'&&s.resultPhase==='manifest'?'导出清单':s.name)+'</span>';
 if(S.view==='batch'&&B)return '<span>'+(B.phase==='manifest'?'导出清单':B.phase==='review'?'人工复核':B.phase==='result'?esc(B.title):'批次 · '+fmt(B.n)+' 份')+'</span>';
 return {presets:'方案',plugins:'插件',settings:'设置'}[S.view]||'<span style="display:flex;align-items:center;gap:8px">'+LOGO(16)+'Tacet</span>';
}
export function openSubpage(view,state={}){
 S.navStack.push({view:S.view,ptab:S.ptab,presetSel:S.presetSel,rsSel:S.rsSel});
 Object.assign(S,{view},state);render();
}
export function backHtml(){
 const back=(action,label,value='')=>'<button class="btn btn-g tb-back" data-a="'+action+'"'+(value?' data-v="'+value+'"':'')+' data-tip="'+esc(label)+'" aria-label="'+esc(label)+'">'+I('chev-l',18)+'返回</button>';
 if(S.view==='history')return S.historyResult.phase==='manifest'?back('historyPhase','返回结果','result'):back('go','返回任务','home');
 if(S.view==='single'&&S.single?.phase==='done'&&S.single.resultPhase==='manifest')return back('singlePhase','返回结果','result');
 if(S.navStack.length){const r=S.navStack[S.navStack.length-1];return back('ret','返回 '+(r.view==='single'&&S.single?S.single.name:{home:'任务',presets:r.ptab==='rules'?'规则':'方案',plugins:'插件',settings:'设置'}[r.view]||'上一页'))}
 if(S.view==='single'&&S.single&&S.single.from==='batch')return back('backBatch','返回批次');
 if(S.view==='batch'&&S.batch&&['manifest','review'].includes(S.batch.phase)){const r=machineDone();return back('bphase',r?'返回结果':'返回批次',r?'result':'run')}
 if(['single','batch'].includes(S.view))return back('go','返回任务','home');
 return '';
}
export function saveUi(){
 const a=document.activeElement;const st={};
 $$('[data-keep]').forEach(e=>st[e.id]=e.scrollTop);
 return{st,fid:a&&a.id,ss:a&&a.selectionStart};
}
export function restoreUi(u){
 Object.entries(u.st).forEach(([id,t])=>{const e=document.getElementById(id);if(e){e.style.scrollBehavior='auto';e.scrollTop=t;e.style.scrollBehavior=''}});
 if(u.fid){const e=document.getElementById(u.fid);if(e){e.focus();try{e.setSelectionRange(u.ss,u.ss)}catch(_){}}}
}
export function render(){
 const u=saveUi();
 document.documentElement.dataset.theme=S.theme;document.documentElement.dataset.redact=S.set.redact||'black';
 $('#rail').innerHTML=railHtml();
 $('#tbTitle').innerHTML=titleHtml();
 $('#tbBack').innerHTML=backHtml();
 const v=S.view;
 $('#main').innerHTML=v==='home'?vHome():v==='history'?vHistoryResult():v==='single'?vSingle():v==='batch'?vBatch():v==='presets'?vPresets():v==='plugins'?vPlugins():vSettings();
 if(S.modelPending&&v!=='home'&&v!=='plugins'&&!(v==='batch'&&S.batch?.phase==='precheck'))$('#main').insertAdjacentHTML('afterbegin','<div class="model-gate">'+modelCard()+'</div>');
 patchModelShell();
 restoreUi(u);
 if(v==='single'&&S.single&&S.single.phase==='review')bindDoc();
 if(v==='home')bindDrop();
 if(v==='batch'&&S.batch&&S.batch.phase==='review')bindRv();
}

