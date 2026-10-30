// Run: node docs/v1/tests/model-guide.test.cjs (requires agent-browser).
const BEHAVIOR=`(() => {
const checks=[];const ok=(v,name)=>{if(!v)throw Error(name);checks.push(name)};
const click=a=>{const e=document.querySelector('[data-a="'+a+'"]');ok(!!e,'入口存在 '+a);e.click()};
scene('fresh');ok(modelById('astr').status==='missing'&&!S.history.length,'首次启动缺少模型');
ok(document.querySelector('.model-card')&&document.querySelector('#modelShell').textContent.includes('待下载'),'首页和顶栏状态一致');
takeFiles([{name:'合同.pdf',size:100}]);ok(!S.single&&S.modelPending.label==='合同.pdf','单份导入等待，不创建检测任务');
ok(document.querySelector('[data-a="modelContinue"]').disabled,'未就绪不可继续');
click('modelDownload');stopModelTimers();modelTick();ok(modelById('astr').progress===5,'下载按进度推进');
ok(document.querySelector('#networkState').dataset.tip.includes('下载'),'联网提示准确');
click('modelPause');ok(modelById('astr').status==='paused'&&modelById('astr').progress===5,'暂停保留进度');
click('modelDownload');stopModelTimers();ok(modelById('astr').progress===5,'继续不清空进度');
modelById('astr').progress=95;modelTick();ok(modelById('astr').status==='verifying'&&!S.single,'下载完成尚未就绪');
modelTick();ok(modelById('astr').status==='loading','校验后加载与试运行');
modelTick();ok(modelById('astr').status==='ready'&&!S.single,'就绪后等待用户继续');
click('modelContinue');clearInterval(procT);ok(S.single?.name==='合同.pdf'&&!S.modelPending,'明确继续后开始检测');
scene('fresh');takeFiles([{name:'保留.pdf'}]);click('modelCancel');ok(!S.modelPending&&!S.single,'取消不创建任务');
for(const kind of ['offline','disk','integrity','load']){scene('model-'+kind);ok(modelById('astr').status==='error'&&document.querySelector('.model-card.error'),'错误 '+kind+' 可见');A.modelManage();click(kind==='load'?'modelPrepare':'modelDownload');stopModelTimers();ok(modelById('astr').status===(kind==='load'?'loading':'downloading'),'错误 '+kind+' 可修复');if(kind==='offline')ok(modelById('astr').progress===45,'断网续传');if(kind==='integrity')ok(modelById('astr').progress===0,'校验失败重新下载')}
scene('model-batch');startBatch();ok(S.batch.phase==='precheck'&&S.modelPending,'批量未就绪阻止开始');ok(document.querySelector('[data-a="startBatch"]').disabled,'避免重复开始');click('modelDownload');stopModelTimers();modelById('astr').progress=95;modelTick();modelTick();modelTick();ok(S.batch.phase==='precheck','批量不自动启动');click('modelContinue');clearInterval(bT);ok(S.batch.phase==='run','批量明确继续后开始');
scene('model-batch');A.modelDownload();stopModelTimers();modelById('astr').progress=95;modelTick();modelTick();modelTick();ok(document.querySelector('.pc-foot').textContent.includes('AstrLink Guard 已就绪'),'批量准备状态实时更新');
scene('fresh');S.homePreset='日志清理';takeFiles([{name:'service.log'}]);clearInterval(procT);ok(S.single&&S.single.preset==='日志清理'&&!S.modelPending,'显式仅规则方案不需要模型');
scene('fresh');S.homePreset='日志清理';A.modelDownload({dataset:{v:'astr'}});stopModelTimers();takeFiles([{name:'service.log'}]);ok(!S.single&&S.modelPending,'下载与仅规则检测也互斥');
scene('s2');S.homePreset='通用办公脱敏';modelById('astr').status='missing';S.presetGo=()=>{};const before=S.single;takeFiles([{name:'另一份.pdf'}]);ok(S.single===before&&S.modelPending,'原任务保留，新操作等待');S.modelPending=null;render();
scene('b1');const saved=S.batch;S.batch=null;modelById('astr').status='missing';S.view='home';render();resumeBatch({name:'恢复任务',saved});ok(!S.batch&&S.modelPending,'恢复任务须核对模型');
scene('s1');clearInterval(procT);modelById('astr').status='missing';A.modelDownload();ok(modelById('astr').status==='missing','检测中禁止下载');
scene('fresh');ok(S.models.length===1&&S.models[0].id==='astr','首发仅提供一个模型');ok(!document.body.textContent.includes('离线包')&&!document.querySelector('[data-a="modelOffline"]'),'首页没有离线包入口');
click('modelManage');ok(S.view==='plugins','管理入口打开插件页');
const headings=[...document.querySelectorAll('.sec-t')].map(e=>e.textContent);ok(headings[0].startsWith('模型')&&headings[1]==='格式','模型区块位于格式上方');
ok(document.querySelectorAll('#modelPlugins [data-model]').length===1,'插件页按模型展示资源卡');
click('modelDownload');stopModelTimers();modelTick('astr');ok(modelById('astr').progress===5&&document.querySelector('#modelPlugins progress').value===5,'插件页直接下载并更新进度');
A.go({dataset:{v:'home'}});ok(document.querySelector('[data-home-guide]').textContent.includes('正在准备识别模型'),'首页仅显示准备汇总');click('modelManage');ok(document.querySelector('progress').value===5,'插件页保留共享进度');click('modelPause');ok(modelById('astr').status==='paused','首页暂停插件页启动的下载');
// Inject a second catalog entry only in the test: it is not offered in the release prototype.
scene('fresh');S.models.push({id:'test-second',name:'Second Model',version:'2.0',variant:'INT8',languages:'English',status:'missing',progress:0,error:null});
S.presets.push({id:'test-preset',name:'Second preset',det:'model',model:'test-second',rules:['cn'],cats:{},ver:'v1'});S.homePreset='Second preset';render();
ok(document.querySelector('[data-home-guide]')&&!document.querySelector('[data-home-guide]').textContent.includes('Second Model'),'首页不展示方案所选模型');const guide=document.querySelector('[data-home-guide]').textContent;S.homePreset='通用办公脱敏';render();ok(document.querySelector('[data-home-guide]').textContent===guide,'切换首页方案不改变通用准备引导');S.homePreset='Second preset';render();
takeFiles([{name:'second.pdf'}]);ok(S.modelPending.modelId==='test-second'&&!S.single,'等待操作绑定第二个模型');
A.modelManage();ok(!document.querySelector('#modelPlugins').textContent.includes('当前方案'),'插件页没有当前方案或全局选择');const card=document.querySelector('[data-model="test-second"]');card.querySelector('[data-a="modelDownload"]').click();stopModelTimers();ok(modelById('test-second').status==='downloading'&&modelById('astr').status==='missing','下载按钮仅操作所属模型');
modelById('test-second').progress=95;modelTick('test-second');modelTick('test-second');modelTick('test-second');ok(modelById('test-second').status==='ready'&&modelById('astr').status==='missing','各模型独立完成准备');
click('modelContinue');clearInterval(procT);ok(S.single.modelId==='test-second','新任务绑定所选模型');ok(getPreset('通用办公脱敏').model==='astr'&&getPreset('Second preset').model==='test-second','不同方案保留各自模型配置');
S.single=null;S.view='home';render();newBatch(['a.pdf','b.pdf']);ok(S.batch.modelId==='test-second','批次冻结所选模型');
getPreset('Second preset').model='astr';modelById('astr').status='ready';modelById('test-second').status='missing';startBatch();ok(S.batch.phase==='precheck'&&S.modelPending.modelId==='test-second','方案变化不替换批次绑定的模型');
S.modelPending=null;S.batch=null;S.view='presets';S.presetSel='test-preset';render();document.querySelector('[data-a="pmodel"]').click();ok(document.querySelector('.pop').textContent.includes('Second Model'),'方案选择器按目录列出模型');
const item=[...document.querySelectorAll('.pop .mi')].find(e=>e.textContent.includes('Second Model'));item.click();ok(getPreset('Second preset').model==='test-second','方案可选择第二个模型');
S.models=S.models.filter(m=>m.id!=='test-second');S.presets=S.presets.filter(p=>p.id!=='test-preset');
scene('fresh');modelById('astr').status='ready';S.view='home';render();ok(!document.querySelector('[data-home-guide]'),'已有可用模型时收起通用准备引导');scene('fresh');S.homePreset='通用办公脱敏';
scene('fresh');ok(!document.querySelector('#modelShell').hidden,'待下载时显示标题栏入口');
for(const status of ['downloading','paused','verifying','loading','ready','error']){modelById('astr').status=status;render();ok(document.querySelector('#modelShell').hidden&&getComputedStyle(document.querySelector('#modelShell')).display==='none','标题栏入口在 '+status+' 时隐藏')}
S.models.push({id:'test-missing',status:'missing'});modelById('astr').status='ready';render();ok(document.querySelector('#modelShell').hidden,'已有可用模型时不因新增未下载模型显示标题栏入口');S.models=S.models.filter(m=>m.id!=='test-missing');scene('fresh');
return {passed:checks.length,checks};
})()`;
const {spawnSync}=require('node:child_process');
const {pathToFileURL}=require('node:url');
const path=require('node:path');
const assert=require('node:assert/strict');
const session='tacet-model-guide-'+process.pid;
function browser(...args){const r=spawnSync('agent-browser',['--session',session,...args],{encoding:'utf8',timeout:20000});if(r.error)throw r.error;assert.equal(r.status,0,r.stderr||r.stdout);return r.stdout}
try{
 browser('open',pathToFileURL(path.resolve(__dirname,'../tacet-prototype.html')).href+'#scene=fresh');
 console.log(browser('eval',BEHAVIOR).trim());
 let layouts=0;
 for(const [w,h] of [[390,844],[768,900],[1440,1000]]){
  browser('set','viewport',String(w),String(h));
  for(const theme of ['light','dark']){
   for(const key of ['fresh','model-offline','model-wait','model-batch','model-plugins']){
    browser('eval','(()=>{scene('+JSON.stringify(key)+');S.theme='+JSON.stringify(theme)+';render();const cards=[...document.querySelectorAll(".model-card")];if(!cards.length)throw Error("missing model card");for(const c of cards){const r=c.getBoundingClientRect();if(r.left<0||r.right>innerWidth+1||c.scrollWidth>c.clientWidth+1)throw Error("model card overflow");for(const b of c.querySelectorAll("button")){const br=b.getBoundingClientRect();if(br.right>r.right+1)throw Error("button overflow")}}return true})()');
    layouts++;
   }
  }
 }
 console.log('Layout checks passed: '+layouts+' (3 viewport sizes, light/dark).');
 const errors=browser('errors').trim();assert.ok(!errors,errors);console.log('No browser errors.');
}finally{browser('close')}
