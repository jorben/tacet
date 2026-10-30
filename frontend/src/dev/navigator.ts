// @ts-nocheck

// @generated-from docs/v1/tacet-prototype.html (dev-only scene navigator); maintained by hand after the port.
import { $, I } from '../engine/runtime';
import { scene } from './scenes';

export const SCENES=[
 ['h','入口'],['首页','home'],['首页 · 首次使用','fresh'],
 ['h','模型准备'],['下载中','model-download'],['下载已暂停','model-paused'],['断网 / 下载中断','model-offline'],['空间不足','model-disk'],['校验失败','model-integrity'],['加载与试运行','model-loading'],['启动失败','model-load'],['已选文件 · 等待模型','model-wait'],['批量 · 模型未就绪','model-batch'],['插件 · 模型待下载','model-plugins'],
 ['h','单份'],['检测中','s1'],['复核','s2'],['复核 · DOCX','s5'],['左右对比','s4'],['完成','s3'],
 ['h','批量'],['预检','b1'],['处理中','b2'],['人工复核','b5'],['完成','b3'],['无人值守 · 完成','b6'],['导出清单','b4'],
 ['h','其他'],['方案','presets'],['插件','plugins'],['设置','settings'],['关闭窗口','close'],
];

export function installProto(){
$('#proto').innerHTML='<div class="pm"><div class="h">交互演示 · 下载与安装均为模拟</div>'+SCENES.map(([a,b])=>a==='h'?'<div class="h">'+b+'</div>':'<button data-scene="'+b+'">'+a+'</button>').join('')+'</div><button aria-label="原型场景">原型场景 '+I('chev-d',12)+'</button>';
$('#proto').addEventListener('click',e=>{e.stopPropagation();const b=e.target.closest('[data-scene]');if(b){$('#proto').classList.remove('open');scene(b.dataset.scene);return}if(e.target.closest('#proto>button'))$('#proto').classList.toggle('open')});
document.addEventListener('click',e=>{if(!e.target.closest('#proto'))$('#proto').classList.remove('open')});

}

