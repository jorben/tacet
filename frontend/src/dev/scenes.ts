// @ts-nocheck

// @generated-from docs/v1/tacet-prototype.html (dev-only scene fixtures); maintained by hand after the port.
import { $, rt } from '../engine/runtime';
import {  } from '../domain/data';
import { closeModal, closePop } from '../engine/layer';
import { resetModels, modelById, modelDownload, modelPrepare } from '../engine/models';
import { S } from '../engine/state';
import { newSingle, exportSingle } from '../engine/single';
import { rng, pick, ri } from '../engine/runtime';
import { SUR, GIV, KIND } from '../domain/data';
import { newBatch, startBatch, tick, patchBatch, gMembers, resolve } from '../engine/batch';
import { render } from '../engine/shell';
import { enterReview } from '../engine/review';
import { takeFiles } from '../engine/home';
import { A } from '../engine/actions';

export function demoNames(n){rt.R=rng(99);return Array.from({length:n},(_,i)=>{const[k,e]=pick(KIND);return k.replace('{n}',pick(SUR)+pick(GIV)).replace('{k}',String(100+i))+'.'+(i===37?'pages':e)})}

/** The prototype's preloaded "recent" list. Production starts empty; scenes seed it to keep parity with the prototype. */
export const DEMO_HISTORY=()=>[
  {k:'batch',name:'季度客户材料',meta:'48 份',st:'done',stl:'已导出 48',when:'3 天前'},
  {k:'file',name:'述职报告.docx',ext:'docx',meta:'12 页',st:'done',stl:'已导出',when:'昨天'},
  {k:'batch',name:'供应商合同',meta:'26 份',st:'keep',stl:'已保存 · 6 天后清理',when:'上周'},
];

export function scene(k){
 if(k==='blank'){closeModal();closePop();resetModels('ready');S.history=[];S.single=null;S.batch=null;S.navStack=[];S.view='home';render();return}
 if(!S.__demoSeeded){S.__demoSeeded=true;S.history=DEMO_HISTORY().concat(S.history)}
 closeModal();closePop();clearInterval(rt.bT);clearInterval(rt.procT);resetModels();S.behind=null;S.navStack=[];S.history=S.history.filter(h=>!h.park&&!h.saved);
 if(['s1','s2','s3','s4','s5'].includes(k)){newSingle(k==='s5'?'劳动合同.docx':'劳动合同.pdf',1887436,k==='s1'?{}:{phase:'review',mode:k==='s4'?'cmp':'orig'});if(k==='s3'){S.single.hits[13].state='keep';exportSingle()}return}
 if(k.startsWith('b')){S.batch=null;newBatch(demoNames(214),{title:'客户材料 · 10 月'});if(k==='b1')return;if(k==='b6'){S.batch.mode='auto';startBatch();for(let i=0;i<1500&&S.batch.phase!=='result';i++)tick();render();return}startBatch();
  if(k==='b5'){for(let i=0;i<60;i++)tick();enterReview(null);return}
  if(k==='b2'){for(let i=0;i<34;i++)tick();patchBatch(true);return}
  for(let r=0;r<3;r++){for(let i=0;i<120;i++)tick();S.batch.groups.forEach(g=>{if(gMembers(g).length)resolve(g.id,g.acts[0].k)})}for(let i=0;i<60;i++)tick();
  const t=$('#layer .toast');if(t)t.remove();
  if(k==='b4')S.batch.phase='manifest';render();return}
 if(k.startsWith('model-')){
  S.history=[];S.single=null;S.batch=null;S.homePreset='通用办公脱敏';S.view='home';const key=k.slice(6);resetModels('missing');
  if(key==='plugins'){S.view='plugins';render();return}
  if(key==='batch'){newBatch(demoNames(12),{title:'待处理材料'});return}
  if(key==='download'){modelById('astr').progress=35;modelDownload();return}
  if(key==='paused'){modelById('astr').status='paused';modelById('astr').progress=45}
  if(['offline','disk','integrity','load'].includes(key)){modelById('astr').status='error';modelById('astr').error=key;modelById('astr').progress=key==='offline'?45:key==='disk'?20:100}
  if(key==='loading'){modelPrepare();return}
  if(key==='wait'){takeFiles([{name:'劳动合同.pdf',size:1887436}]);return}
  render();return;
 }
 if(k==='fresh'){S.homePreset='通用办公脱敏';resetModels('missing');S.history=[];S.single=null;S.batch=null;S.view='home';render();return}
 if(k==='close'){A.closeWin();return}
 S.view=k;render();
}
