// @ts-nocheck

// @generated-from docs/v1/tacet-prototype.html (script section: settings); maintained by hand after the port.
import { ri, I } from './runtime';
import {  } from '../domain/data';
import { S } from './state';
import { busy } from './batch';

export function vSettings(){
 const st=S.set,u=S.update;
 const busy=['checking','updating'].includes(u.status);
 const updateNote={idle:'',checking:'',available:'发现新版本 '+u.latest,updating:'正在更新至 '+u.latest+'…',latest:'已是最新版本'}[u.status];
 return '<div class="page" id="settingsPage" data-keep><div class="page-inner"><div class="h1" style="margin-bottom:18px">设置</div>'+
  '<div class="list">'+
   '<div class="row"><span class="ri">'+I('moon',17)+'</span><span class="rl">外观</span><span class="rv"><div class="seg">'+[['light','浅色'],['dark','深色'],['system','跟随系统']].map(([k,l])=>'<button data-a="theme" data-v="'+k+'" aria-pressed="'+(S.themePref===k)+'">'+l+'</button>').join('')+'</div></span></div>'+
   '<div class="row"><span class="ri">'+I('x',17)+'</span><div><div class="rl">点击关闭按钮</div><div class="rs">'+(st.closeAct==='tray'?'任务在后台继续':'进行中的任务会暂停')+'</div></div><span class="rv"><div class="seg">'+[['tray','最小化到托盘'],['quit','关闭程序']].map(([k,l])=>'<button data-a="closeAct" data-v="'+k+'" aria-pressed="'+(st.closeAct===k)+'">'+l+'</button>').join('')+'</div></span></div>'+
   '<div class="row"><span class="ri">'+I('cpu',17)+'</span><div><div class="rl">资源档位</div><div class="rs">'+(st.tier==='comfort'?'CPU ≤ 50% · 内存 2 GiB':'内存 4 GiB · 更快')+'</div></div><span class="rv"><div class="seg">'+[['comfort','舒适'],['std','标准']].map(([k,l])=>'<button data-a="tier" data-v="'+k+'" aria-pressed="'+(st.tier===k)+'">'+l+'</button>').join('')+'</div></span></div>'+
   '<div class="row"><span class="ri">'+I('clock',17)+'</span><span class="rl">中断后可恢复</span><span class="rv"><button class="sel" data-a="skeep">'+(st.keep==='0'?'不保留':'保留 '+st.keep+' 天')+I('chev-d',14)+'</button></span></div>'+
  '</div>'+
  '<div class="sec-t">涂抹样式</div><div class="list rgal">'+[['经典',[['black','纯黑'],['gray','深灰'],['mosaic','马赛克'],['stripe','斜纹']]],['趣味',[['heart','粉色爱心'],['paw','小猫脚印'],['snow','雪花'],['seal','奉旨回避'],['star','星空'],['dot','柠檬波点'],['rainbow','彩虹'],['plaid','薄荷格纹']]]].map(([h,xs])=>'<div class="rg-h">'+h+'</div><div class="rgrid" role="radiogroup" aria-label="涂抹样式 · '+h+'">'+xs.map(([v,l])=>{const on=(st.redact||'black')===v;return '<button class="rtile" role="radio" data-a="redact" data-v="'+v+'" aria-checked="'+on+'"><span class="pp">姓名：<span class="rsw" data-redact="'+v+'"></span></span><span class="lb">'+(on?I('check',12,2.6):'')+l+'</span></button>'}).join('')+'</div>').join('')+'</div>'+
  '<div class="sec-t">存储</div><div class="list"><div class="row"><span class="ri">'+I('hdd',17)+'</span><div><div class="rl">工作区</div><div class="rs">加密 · 1.4 GiB · '+S.history.filter(h=>h.st==='keep').length+' 个可恢复任务</div></div><span class="rv"><button class="btn btn-s" data-a="cleanWs">清理…</button></span></div></div>'+
  '<div class="sec-t">版本</div><div class="list">'+
   '<div class="row"><span class="ri">'+I('info',17)+'</span><div><div class="rl">当前版本</div><div class="rs" id="updateStatus" role="status" aria-live="polite" aria-atomic="true">'+updateNote+'</div></div><span class="rv version-controls"><button class="text-link" id="checkUpdate" data-a="checkUpd" '+(busy?'disabled':'')+'>'+(u.status==='checking'?'检查中…':'检查更新')+'</button><span class="mono" id="currentVersion">'+u.current+'</span>'+(['available','updating'].includes(u.status)?'<button class="btn btn-p" id="updateApp" data-a="updateApp" '+(u.status==='updating'?'disabled':'')+'>'+(u.status==='updating'?'更新中…':'更新')+'</button>':'')+'</span></div>'+
   '<div class="row"><span class="ri">'+I('clock',17)+'</span><div><div class="rl">自动检查更新</div><div class="rs">开启后联网检查 · 仅发送版本号、平台与更新通道</div></div><span class="rv"><button class="tg" id="autoUpdate" role="switch" aria-checked="'+st.autoUpd+'" data-a="autoUpd" aria-label="自动检查更新"></button></span></div>'+
  '</div>'+
  '<div class="sec-t">关于</div><div class="list">'+
   '<button class="row" data-a="aboutLink" data-v="website"><span class="ri">'+I('globe',17)+'</span><span class="rl">官网</span><span class="rv">'+I('ext',15)+'</span></button>'+
   '<button class="row" data-a="licenses"><span class="ri">'+I('scale',17)+'</span><span class="rl">开源许可</span><span class="rv"><span class="rs">Apache-2.0</span>'+I('chev-r',15)+'</span></button>'+
   '<div class="row"><span class="ri">'+I('users',17)+'</span><span class="rl">社区</span><span class="rv"><button class="pill ghost community-link" data-a="aboutLink" data-v="discord">'+I('discord',15)+'加入 Discord</button><button class="pill ghost community-link" data-a="aboutLink" data-v="github">'+I('github',15)+'GitHub</button></span></div>'+
  '</div>'+
 '</div></div>';
}

