// @ts-nocheck

// @generated-from docs/v1/tacet-prototype.html (script section: events); maintained by hand after the port.
import { $, $$ } from './runtime';
import {  } from '../domain/data';
import { fitCmp, doUndo, exportSingle, stepHit, setHit, setMode } from './single';
import { closePop, modal, closeModal } from './layer';
import { A } from './actions';
import { S } from './state';
import { takeFiles } from './home';
import { patchBatch } from './batch';
import { rvDecide, rvUndo, rvStep, rvCur, rvPreview } from './review';
import { render } from './shell';

export function installEvents(){
window.addEventListener('resize',fitCmp);
document.addEventListener('click',ev=>{
 const el=ev.target.closest('[data-a]');
 const sel=window.getSelection();const selecting=sel&&!sel.isCollapsed&&ev.target.closest('#doc');
 if(!selecting&&!ev.target.closest('#layer .pop')&&!(el&&['hit','imgMenu'].includes(el.dataset.a)))closePop();
 if(!el||el.disabled)return;
 const f=A[el.dataset.a];if(f)f(el,ev);
});
document.addEventListener('change',ev=>{
 if(ev.target.id==='sameAll'){S.single.sameAll=ev.target.checked}
 if(ev.target.id==='rvAll'){S.batch.rv.all=ev.target.checked}
 if(ev.target.id==='fileIn'){takeFiles([...ev.target.files].map(f=>({name:f.name,size:f.size})));ev.target.value=''}
});
document.addEventListener('input',ev=>{
 if(ev.target.dataset.in==='fq'){S.batch.q=ev.target.value.trim();S.batch.lim=60;patchBatch(true)}
});
document.addEventListener('keydown',ev=>{
 if(ev.key==='Tab'){const m=$('#layer .history-modal');if(m){const fs=$$('button:not([disabled])',m),first=fs[0],last=fs[fs.length-1];if(ev.shiftKey&&document.activeElement===first){ev.preventDefault();last.focus()}else if(!ev.shiftKey&&document.activeElement===last){ev.preventDefault();first.focus()}return}}
 if(ev.key==='Escape'){if($('#layer .pop')){closePop();return}if($('#layer .modal,#layer .drawer')){closeModal();return}if(S.single)S.single.focus=-1;}
 if(ev.target.id==='rvPwd'&&ev.key==='Enter'){ev.preventDefault();rvDecide('pwd');return}
 if((ev.key==='Enter'||ev.key===' ')&&ev.target.matches('div[role=button][data-a]')){ev.preventDefault();ev.target.click();return}
 const tag=(ev.target.tagName||'');if(tag==='INPUT'&&ev.target.type!=='checkbox')return;
 const B=S.batch;
 if(S.view==='batch'&&B&&B.phase==='review'&&!$('#layer .modal')){
  const k=ev.key.toLowerCase();const mod=ev.metaKey||ev.ctrlKey;
  if(mod&&k==='z'){ev.preventDefault();rvUndo();return}
  if(mod)return;
  if(k==='j'||ev.key==='ArrowDown'){ev.preventDefault();rvStep(1)}
  else if(k==='k'||ev.key==='ArrowUp'){ev.preventDefault();rvStep(-1)}
  else if(['1','2','a','b'].includes(k)){const it=rvCur();if(it){const a=B.groups[it.gi].acts['1a'.includes(k)?0:1];if(a)rvDecide(a.k)}}
  else if(k==='e'){S.docMode=(S.docMode||'orig')==='orig'?'fx':'orig';render()}
  else if(k==='c'){S.docMode=(S.docMode||'orig')==='cmp'?'orig':'cmp';render()}
  return;
 }
 const s=S.single;
 if(S.view==='single'&&s&&s.phase==='review'){
  const k=ev.key.toLowerCase();const mod=ev.metaKey||ev.ctrlKey;
  if(mod&&k==='z'){ev.preventDefault();doUndo();return}
  if(mod&&ev.key==='Enter'){ev.preventDefault();if(s.from!=='batch')exportSingle();return}
  if(mod)return;
  if(k==='j'||ev.key==='ArrowDown'){ev.preventDefault();stepHit(1)}
  else if(k==='k'||ev.key==='ArrowUp'){ev.preventDefault();stepHit(-1)}
  else if(k==='x'&&s.focus>=0){setHit(s.hits[s.focus].state==='keep'?'redact':'keep')}
  else if(k==='e'){closePop();setMode(s.mode==='orig'?'fx':'orig')}
  else if(k==='c'){closePop();setMode(s.mode==='cmp'?'orig':'cmp')}
 }
});
document.addEventListener('mouseover',e=>{if(S.view!=='batch'||!S.batch||S.batch.phase!=='review'||!S.batch.rv)return;const b=e.target.closest('.dopt');rvPreview(b?b.dataset.v:null)});
document.addEventListener('focusin',e=>{if(e.target.classList&&e.target.classList.contains('dopt'))rvPreview(e.target.dataset.v)});
document.addEventListener('dragover',e=>e.preventDefault());
document.addEventListener('drop',e=>{e.preventDefault();if(S.view==='home'&&!e.target.closest('#drop'))takeFiles([...e.dataTransfer.files].map(f=>({name:f.name,size:f.size})))});

}

