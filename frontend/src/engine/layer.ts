// @ts-nocheck

// @generated-from docs/v1/tacet-prototype.html (script section: layer); maintained by hand after the port.
import { $, $$, I, rt } from './runtime';
import {  } from '../domain/data';
import { S } from './state';

export function closePop(){const p=$('#layer .pop');if(p)p.remove();if(rt.popClose){const f=rt.popClose;rt.popClose=null;f()}}
export function openPop(anchor,html,opt={}){
 closePop();
 const L=$('#layer'),w=$('#win').getBoundingClientRect();
 const el=document.createElement('div');el.className='pop '+(opt.cls||'');el.innerHTML=html;el.setAttribute('role',opt.role||'menu');
 L.appendChild(el);
 const r=anchor.getBoundingClientRect?anchor.getBoundingClientRect():anchor;
 const pw=el.offsetWidth,ph=el.offsetHeight;
 let x=opt.alignRight?r.right-w.left-pw:r.left-w.left;let y=r.bottom-w.top+6;
 if(y+ph>w.height-8)y=r.top-w.top-ph-6;
 x=Math.max(8,Math.min(x,w.width-pw-8));
 el.style.left=x+'px';el.style.top=y+'px';
 rt.popClose=opt.onClose||null;
 const f=el.querySelector('button,input');if(f&&opt.focus!==false)f.focus({preventScroll:true});
 return el;
}
export function menu(anchor,items,cb,opt={}){
 rt.menuCb=cb;
 const h=items.map((it,i)=>it==='-'?'<div class="msep"></div>':it.h?'<div class="mh">'+it.h+'</div>':
  '<button class="mi '+(it.d?'d':'')+'" data-a="menuPick" data-v="'+i+'" role="menuitem">'+(it.ic?I(it.ic,15):'')+'<span>'+it.l+'</span>'+(it.s?'<small>'+it.s+'</small>':'')+(it.on?'<span class="ck">'+I('check',14)+'</span>':'')+'</button>').join('');
 rt.menuCb.items=items;
 openPop(anchor,h,opt);
}
export function closeModal(){const history=$('#layer .history-modal');$$('#layer .scrim,#layer .modal,#layer .drawer').forEach(e=>e.remove());S.drawer=null;if(history&&rt.historyTrigger&&rt.historyTrigger.isConnected)rt.historyTrigger.focus();rt.historyTrigger=null}
export function modal(html){closePop();closeModal();const L=$('#layer');L.insertAdjacentHTML('beforeend','<div class="scrim" data-a="closeModal"></div><div class="modal" role="dialog" aria-modal="true">'+html+'</div>');const f=$('#layer .modal input,#layer .modal .btn-p');if(f)f.focus()}
export function drawer(html){closePop();$$('#layer .drawer,#layer .scrim').forEach(e=>e.remove());$('#layer').insertAdjacentHTML('beforeend','<div class="scrim" data-a="closeModal"></div><div class="drawer" role="dialog" aria-modal="true">'+html+'</div>')}
export function toast(msg,undo){
 const old=$('#layer .toast');if(old)old.remove();clearTimeout(rt.toastT);rt.toastUndo=undo||null;
 $('#layer').insertAdjacentHTML('beforeend','<div class="toast" role="status">'+msg+(undo?'<button data-a="toastUndo">撤销</button>':'')+'</div>');
 rt.toastT=setTimeout(()=>{const t=$('#layer .toast');if(t)t.remove();rt.toastUndo=null},undo?5000:2600);
}

