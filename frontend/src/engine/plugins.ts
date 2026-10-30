// @ts-nocheck

// @generated-from docs/v1/tacet-prototype.html (script section: plugins); maintained by hand after the port.
import { esc, I } from './runtime';
import {  } from '../domain/data';
import { S } from './state';
import { hash } from './batch';
import { modelCard } from './models';
import { modal, closeModal } from './layer';

export function vPlugins(){
 const b=S.plugins.filter(p=>p.builtin),t=S.plugins.filter(p=>!p.builtin);
 const ic={格式:'file',模型:'cpu',规则:'hash'};
 const row=p=>'<div class="row"><span class="ficon" style="width:32px;height:32px">'+I(ic[p.role]||'puzzle',16)+'</span><div style="min-width:0"><div class="rl" style="display:flex;align-items:center;gap:8px">'+p.name+(p.builtin?'':'<span class="tag">'+p.role+'</span><span class="tag tp" data-tip="在系统沙箱中运行，无网络">沙箱</span>')+'</div><div class="rs">'+(p.fmt?p.fmt+' · ':'')+p.ver+(p.src?' · <span class="mono">'+p.src+' @ '+p.commit+'</span>':'')+'</div></div>'+
  '<span class="rv">'+(p.builtin?'':'<button class="ib" data-a="plmore" data-v="'+p.id+'" aria-label="更多">'+I('more',18)+'</button>')+'<button class="tg" role="switch" aria-checked="'+p.on+'" data-a="pltog" data-v="'+p.id+'" aria-label="启用 '+p.name+'" '+''+'></button>'+'</span></div>';
 return '<div class="page"><div class="page-inner">'+
  '<div class="phead"><div class="h1">插件</div><span class="sp"></span><button class="btn btn-p" data-a="plinstall">'+I('plus',15)+'安装</button></div>'+
  '<section id="modelPlugins" aria-label="模型"><div class="sec-t" style="margin-top:8px">模型<span class="model-section-note">按需下载，在方案中选择使用</span></div>'+S.models.map(m=>modelCard(true,m.id)).join('')+'</section>'+
  ['格式','规则'].map((r,i)=>{const xs=b.filter(p=>p.role===r);return xs.length?'<div class="sec-t" '+(i?'':'style="margin-top:8px"')+'>'+r+'</div><div class="list">'+xs.map(row).join('')+'</div>':''}).join('')+
  '<div class="sec-t">第三方</div><div class="list">'+(t.length?t.map(row).join(''):'<div class="empty" style="padding:28px">暂无</div>')+'</div>'+
 '</div></div>';
}
export function installModal(step,src){
 if(step!=='review'){
  modal('<h3>安装插件</h3><div class="mb"><div class="seg full"><button aria-pressed="'+(S.instKind!=='dir')+'" data-a="instKind" data-v="git">Git 仓库</button><button aria-pressed="'+(S.instKind==='dir')+'" data-a="instKind" data-v="dir">本地文件夹</button></div>'+
   (S.instKind==='dir'?'<button class="field" data-a="fakeDir" style="width:100%">'+I('folder',15)+'<span class="muted" id="dirv">选择文件夹…</span></button>':'<label class="field">'+I('git',15)+'<input id="instSrc" placeholder="https://github.com/…" value="https://github.com/mailtools/tacet-eml"></label>')+
   '</div><div class="mf"><button class="btn btn-g" data-a="closeModal">取消</button><button class="btn btn-p" data-a="instNext">下一步</button></div>');
  return;
 }
 const s=(src||'github.com/mailtools/tacet-eml').replace(/^https?:\/\//,'');
 modal('<div style="display:flex;gap:12px;align-items:center;margin-bottom:4px"><span class="ficon">'+I('file',17)+'</span><div><h3 style="margin:0">EML 邮件</h3><div class="ms">格式插件 · 0.2.1</div></div></div>'+
  '<div class="mb"><dl class="kv">'+
  '<dt>来源</dt><dd class="mono">'+esc(s)+'</dd>'+
  '<dt>版本</dt><dd class="mono">提交 8c41e07 · 固定</dd>'+
  '<dt>格式</dt><dd>.eml · 读取 · 回写 · 验证</dd>'+
  '<dt>权限</dt><dd>仅指定输入与临时区 · <b>无网络</b></dd>'+
  '<dt>资源</dt><dd>内存 ≤ 512 MiB</dd>'+
  '<dt>许可</dt><dd>MIT（作者声明）· 依赖 9</dd>'+
  '</dl></div>'+
  '<div class="warnline" style="justify-content:flex-start">'+I('alert',14)+'插件可读取文档原文</div>'+
  '<div class="mf"><button class="btn btn-g" data-a="closeModal">取消</button><button class="btn btn-p" data-a="instDo">安装</button></div>');
}

