// @ts-nocheck

// @generated-from docs/v1/tacet-prototype.html (script section: presets); maintained by hand after the port.
import { $, esc, ri, I } from './runtime';
import { TYPES, OFFICE_CATS, MODEL_LABELS, BD } from '../domain/data';
import { S } from './state';
import { modelById, getPreset } from './models';
import { hash } from './batch';
import { modal, closeModal, toast } from './layer';
import { render } from './shell';

export function availSets(){return S.rulesets.filter(rs=>!rs.plugin||(S.plugins.find(p=>p.id===rs.plugin)||{}).on)}
export function ruleSum(r){return r.kind==='词表'?(r.n||r.body.split('\n').filter(x=>x.trim()).length)+' 条':r.body}
export function vPresets(){
 const tab=S.ptab||'scheme';
 return '<div class="page"><div class="page-inner"><div class="tabs" style="margin:0 0 22px" role="tablist">'+[['scheme','方案'],['rules','规则']].map(([k,l])=>'<button class="tab" role="tab" data-a="ptab" data-v="'+k+'" aria-selected="'+(tab===k)+'">'+l+'</button>').join('')+'</div>'+(tab==='rules'?vRules():vScheme())+'</div></div>';
}
// Model scores are not comparable across models: the default (calibrated) value and the floor come from the bound model
export function confOf(m){return{rec:m?.confRec??0.5,min:m?.confMin??0.3}}
export function fmtConf(v){return (+v).toFixed(2)}
export function presetConf(p){return p.conf??confOf(modelById(p.model)).rec}
// Shown wherever the scheme is summarised; rules-only schemes have no threshold
export function confSum(name){const p=getPreset(name);return p.det==='model'?'<span class="muted">置信度 ≥ '+fmtConf(presetConf(p))+'</span>':''}
export function confRow(p,lock){
 const c=confOf(modelById(p.model)),v=presetConf(p);
 return '<div class="row" style="flex-wrap:wrap;row-gap:8px"><span class="ri">'+I('sliders',17)+'</span><div style="min-width:0;flex:1 1 140px"><div class="rl">最低置信度</div><div class="rs">低于此值的模型命中不做涂抹</div></div><span class="rv">'+
  (v>c.rec?'<span class="pill warn" data-tip="高于推荐值，可能漏掉更多敏感信息；模型识别的类别不能无人值守处理" data-tr tabindex="0">'+I('alert',12)+'可能漏检</span>':'')+
  '<button class="sel" data-a="pconf" '+(lock?'disabled':'')+'>'+fmtConf(v)+'<span class="muted sm">'+(v===c.rec?'推荐':'推荐 '+fmtConf(c.rec))+'</span>'+(lock?'':I('chev-d',14))+'</button></span></div>';
}
export function vScheme(){
 const p=S.presets.find(x=>x.id===S.presetSel)||S.presets[0];
 const lock=p.builtin,dis=lock?'disabled':'';
 const lab={mask:'涂抹',ph:'占位',off:'关闭'};
 const sets=availSets().filter(rs=>p.rules.includes(rs.id));
 return '<div class="split">'+
  '<div><div class="plist">'+S.presets.map(x=>'<button class="pitem" data-a="psel" data-v="'+x.id+'" aria-current="'+(x.id===p.id)+'"><span class="ficon" style="width:30px;height:30px">'+I('layers',15)+'</span><span><div class="pn">'+x.name+'</div><div class="pv">'+x.ver+(x.edited?' · '+x.edited:'')+'</div></span></button>').join('')+
   '<button class="pitem" data-a="pnew" style="color:var(--muted)"><span class="ficon" style="width:30px;height:30px;background:none;box-shadow:inset 0 0 0 1px var(--line)">'+I('plus',15)+'</span>新建</button><button class="pitem" data-a="pimp" style="color:var(--muted)"><span class="ficon" style="width:30px;height:30px;background:none;box-shadow:inset 0 0 0 1px var(--line)">'+I('folder',15)+'</span>导入</button></div></div>'+
  '<div>'+
   '<div class="phead"><div class="h1">'+p.name+'</div><span class="tag '+(lock?'':'tp')+'">'+p.ver+'</span><span class="sp"></span>'+
    (lock?'<span class="ib" data-tip="内建方案，复制后可编辑" data-tr tabindex="0">'+I('lock',16)+'</span>':'')+
    '<button class="btn btn-s" data-a="pcopy">'+I('copy',15)+'复制</button><button class="ib" data-a="pmore" aria-label="更多">'+I('more',18)+'</button></div>'+
   '<div class="list" style="margin-bottom:14px">'+
    '<div class="row"><span class="ri">'+I('scan',17)+'</span><span class="rl">检测方式</span><span class="rv"><div class="seg">'+[['model','模型 + 规则'],['rules','仅规则']].map(([k,l])=>'<button data-a="pdet" data-v="'+k+'" aria-pressed="'+(p.det===k)+'" '+dis+'>'+l+'</button>').join('')+'</div></span></div>'+
    (p.det==='model'?'<div class="row"><span class="ri">'+I('cpu',17)+'</span><span class="rl">模型</span><span class="rv"><button class="sel" data-a="pmodel" '+dis+'>'+esc(modelById(p.model)?.name||'模型不可用')+'<span class="muted sm">'+esc(modelById(p.model)?.version||'')+' · '+(MODEL_LABELS[modelById(p.model)?.status]||'不可用')+'</span>'+(lock?'':I('chev-d',14))+'</button></span></div>':'')+
    (p.det==='model'?confRow(p,lock):'')+
    '<div class="row"><span class="ri">'+I('hash',17)+'</span><span class="rl">规则集</span><span class="rv"><button class="sel" data-a="prules" '+dis+'>'+(sets.length?sets.map(x=>x.name).join(' · '):'<span style="color:var(--bad)">未选择</span>')+(lock?'':I('chev-d',14))+'</button></span></div>'+
    '<div class="row"><span class="ri">'+I('image',17)+'</span><span class="rl">图像与嵌入</span><span class="rv"><span class="muted" style="padding-right:10px">涂抹</span></span></div>'+
   '</div>'+
   '<div class="list">'+OFFICE_CATS.map(t=>{const v=p.cats[t];return '<div class="row" style="--c:var(--t-'+t+')"><span class="sw"></span><span class="rl">'+TYPES[t].l+'</span><span class="rv">'+
    (v==='na'?'<span class="pill ghost" data-tip="仅规则模式无法检查此类" data-tr>'+I('alert',12)+'范围外</span>':'<div class="seg">'+['mask','ph','off'].map(k=>'<button data-a="pcat" data-v="'+t+'|'+k+'" aria-pressed="'+(v===k)+'" '+dis+'>'+lab[k]+'</button>').join('')+'</div>')+'</span></div>'}).join('')+'</div>'+
  '</div></div>';
}
export function vRules(){
 const sets=availSets();const r=sets.find(x=>x.id===S.rsSel)||sets[0];
 const lock=r.builtin||!!r.plugin;const pl=r.plugin&&S.plugins.find(p=>p.id===r.plugin);
 const used=S.presets.filter(p=>p.rules.includes(r.id)).map(p=>p.name);
 return '<div class="split">'+
  '<div><div class="plist">'+sets.map(x=>'<button class="pitem" data-a="rsel" data-v="'+x.id+'" aria-current="'+(x.id===r.id)+'"><span class="ficon" style="width:30px;height:30px">'+I('hash',15)+'</span><span><div class="pn">'+x.name+'</div><div class="pv">'+x.ver+(x.plugin?' · 插件':'')+' · '+x.rules.length+' 条规则</div></span></button>').join('')+
   '<button class="pitem" data-a="rsnew" style="color:var(--muted)"><span class="ficon" style="width:30px;height:30px;background:none;box-shadow:inset 0 0 0 1px var(--line)">'+I('plus',15)+'</span>新建</button>'+
   '<button class="pitem" data-a="rsimp" style="color:var(--muted)"><span class="ficon" style="width:30px;height:30px;background:none;box-shadow:inset 0 0 0 1px var(--line)">'+I('folder',15)+'</span>导入</button></div></div>'+
  '<div>'+
   '<div class="phead"><div style="min-width:0"><div style="display:flex;align-items:center;gap:10px"><div class="h1">'+r.name+'</div><span class="tag '+(lock?'':'tp')+'">'+r.ver+'</span></div><div class="muted sm" style="margin-top:4px">'+(used.length?'用于 '+used.join('、'):'未被方案使用')+'</div></div><span class="sp"></span>'+
    (lock?'<span class="ib" data-tip="'+(pl?'来自插件，复制后可编辑':'内建规则集，复制后可编辑')+'" data-tr tabindex="0">'+I('lock',16)+'</span>':'')+
    '<button class="btn btn-s" data-a="rscopy">'+I('copy',15)+'复制</button><button class="ib" data-a="rsmore" aria-label="更多">'+I('more',18)+'</button></div>'+
   '<div class="list">'+(r.rules.length?r.rules.map((x,i)=>'<div class="row" style="--c:var(--t-'+x.cat+')"><span class="sw"></span><div style="min-width:0"><div class="rl" style="display:flex;align-items:center;gap:8px">'+esc(x.name)+'<span class="tag">'+x.kind+'</span>'+(x.bd?'<span class="tag" data-tip="命中前后不能紧邻'+BD[x.bd]+'" data-tr>边界 · '+BD[x.bd]+'</span>':'')+'</div><div class="rs '+(x.kind==='正则'?'mono':'')+'" style="overflow:hidden;text-overflow:ellipsis;white-space:nowrap">'+esc(ruleSum(x))+'</div></div>'+
     '<span class="rv"><span class="muted sm">'+TYPES[x.cat].l+'</span>'+(lock?'':'<button class="ib" data-a="redit" data-v="'+i+'" aria-label="编辑">'+I('edit',15)+'</button>')+'<button class="tg" role="switch" aria-checked="'+x.on+'" data-a="rtog" data-v="'+i+'" aria-label="启用 '+esc(x.name)+'" '+(lock?'disabled':'')+'></button></span></div>').join(''):'<div class="empty" style="padding:36px 0">还没有规则</div>')+'</div>'+
   (lock?'':'<button class="btn btn-s" data-a="radd" style="margin-top:12px">'+I('plus',15)+'添加规则</button>')+
  '</div></div>';
}
export function ruleModal(){
 const d=S.rd;
 modal('<h3>'+(d.idx!=null?'编辑规则':'添加规则')+'</h3><div class="mb">'+
  '<label class="field"><input id="rdName" placeholder="名称" value="'+esc(d.name)+'" aria-label="名称"></label>'+
  '<div class="seg full" style="margin-top:12px">'+['词表','正则'].map(k=>'<button data-a="rdKind" data-v="'+k+'" aria-pressed="'+(d.kind===k)+'">'+k+'</button>').join('')+'</div>'+
  '<label class="field ta"><textarea id="rdBody" placeholder="'+(d.kind==='词表'?'每行一个词条':'正则表达式')+'" aria-label="内容" class="'+(d.kind==='正则'?'mono':'')+'">'+esc(d.body)+'</textarea></label>'+
  (d.kind==='正则'?'<div class="mh" style="padding:16px 0 6px">前后边界</div><div class="seg full">'+[['','不限'],['digit','不紧邻数字'],['word','不紧邻字母或数字']].map(([k,l])=>'<button data-a="rdBd" data-v="'+k+'" aria-pressed="'+((d.bd||'')===k)+'">'+l+'</button>').join('')+'</div>':'')+
  '<div class="mh" style="padding:16px 0 6px">命中归类</div><div class="typegrid" style="padding:0">'+Object.keys(TYPES).map(t=>'<button data-a="rdCat" data-v="'+t+'" aria-pressed="'+(d.cat===t)+'"><span class="sw" style="--c:var(--t-'+t+')"></span>'+TYPES[t].l+'</button>').join('')+'</div>'+
 '</div><div class="mf">'+(d.idx!=null?'<button class="btn btn-d" data-a="rdDel" style="margin-right:auto">删除</button>':'')+'<button class="btn btn-g" data-a="closeModal">取消</button><button class="btn btn-p" data-a="rdSave">保存</button></div>');
}
export function rdRead(){const d=S.rd,n=$('#rdName'),b=$('#rdBody');if(n)d.name=n.value;if(b)d.body=b.value}
export function curSet(){const sets=availSets();return sets.find(x=>x.id===S.rsSel)||sets[0]}
export function curPreset(){return S.presets.find(x=>x.id===S.presetSel)||S.presets[0]}
// Name is not versioned content; presets hold rule set ids, rule sets hold rules
export function cfgData(o){return JSON.parse(JSON.stringify(Object.fromEntries((o.cats?['det','model','conf','rules','cats','img']:['rules']).map(k=>[k,o[k]]))))}
// Call before each content change: the change is saved as v{n+1}; the latest 5 versions, current included, are kept
export function bump(o){o.hist=[{ver:o.ver,when:o.edited||'更早',data:cfgData(o)}].concat(o.hist||[]).slice(0,4);o.ver='v'+(parseInt(o.ver.slice(1),10)+1);o.edited='刚刚'}
export function verTarget(){return S.verKind==='r'?curSet():curPreset()}
export function verModal(){
 const o=verTarget();
 modal('<h3>历史版本</h3><div class="ms">'+esc(o.name)+' · 保留最近 5 个版本</div><div class="mb"><div class="list">'+
  '<div class="row"><span class="rl"><span class="tag tp">'+o.ver+'</span></span><span class="rv"><span class="muted sm">'+esc(o.edited||'')+' · 当前</span></span></div>'+
  (o.hist||[]).map((h,i)=>'<div class="row"><span class="rl"><span class="tag">'+h.ver+'</span></span><span class="rv"><span class="muted sm">'+esc(h.when)+'</span><button class="btn btn-s" data-a="verBack" data-v="'+i+'">回滚</button></span></div>').join('')+
  '</div><div class="muted sm" style="margin-top:10px">回滚会以所选版本的内容保存为新版本，其他版本保持不变</div></div><div class="mf"><button class="btn btn-g" data-a="closeModal">关闭</button></div>');
}
// Prototype: the chosen file holds a config named like an existing one, to show the conflict prompt
export function impModal(kind){
 const src=kind==='r'?{name:'企业词条',rules:[{name:'项目代号',kind:'词表',cat:'other',body:'',n:40,on:true}]}:{name:'简历脱敏',det:'model',model:'astr',conf:0.5,rules:['cn'],cats:Object.fromEntries(OFFICE_CATS.map(t=>[t,'mask'])),img:'remove'};
 S.imp={kind,src};
 if(!impClash(src.name)){impDo(src.name);return}
 modal('<h3>名称冲突</h3><div class="ms">已有同名'+(kind==='r'?'规则集':'方案')+'「'+esc(src.name)+'」，导入不会覆盖或合并</div><div class="mb"><label class="field"><input id="impName" value="'+esc(src.name)+' (导入)" aria-label="新名称"></label><div class="sm" id="impErr" style="margin-top:8px;color:var(--bad)"></div></div><div class="mf"><button class="btn btn-g" data-a="closeModal">取消导入</button><button class="btn btn-p" data-a="impGo">改名导入</button></div>');
}
export function impClash(name){return (S.imp.kind==='r'?availSets():S.presets).some(x=>x.name===name)}
export function impDo(name){
 const {kind,src}=S.imp;const o=Object.assign(JSON.parse(JSON.stringify(src)),{id:(kind==='r'?'rs':'p')+Date.now(),name,ver:'v1',edited:'刚刚',hist:[]});
 if(kind==='r'){S.rulesets.push(o);S.rsSel=o.id}else{S.presets.push(o);S.presetSel=o.id}
 S.imp=null;closeModal();render();toast('已导入「'+esc(name)+'」 · v1');
}

