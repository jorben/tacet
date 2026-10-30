// @ts-nocheck

// @generated-from docs/v1/tacet-prototype.html (script section: protoBoot); maintained by hand after the port.
import { $, I } from './runtime';
import { SCENES } from '../domain/data';
import { scene } from './proto';

export function installProto(){
$('#proto').innerHTML='<div class="pm"><div class="h">交互演示 · 下载与安装均为模拟</div>'+SCENES.map(([a,b])=>a==='h'?'<div class="h">'+b+'</div>':'<button data-scene="'+b+'">'+a+'</button>').join('')+'</div><button aria-label="原型场景">原型场景 '+I('chev-d',12)+'</button>';
$('#proto').addEventListener('click',e=>{e.stopPropagation();const b=e.target.closest('[data-scene]');if(b){$('#proto').classList.remove('open');scene(b.dataset.scene);return}if(e.target.closest('#proto>button'))$('#proto').classList.toggle('open')});
document.addEventListener('click',e=>{if(!e.target.closest('#proto'))$('#proto').classList.remove('open')});

}

