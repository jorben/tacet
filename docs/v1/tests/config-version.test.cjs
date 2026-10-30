// Run: node docs/v1/tests/config-version.test.cjs (requires agent-browser).
const BEHAVIOR=`(() => {
const checks=[];const ok=(v,name)=>{if(!v)throw Error(name);checks.push(name)};
const click=(a,v)=>{const e=document.querySelector('[data-a="'+a+'"]'+(v!=null?'[data-v="'+v+'"]':''));ok(!!e,'入口存在 '+a);e.click()};
const pick=l=>{const e=[...document.querySelectorAll('.pop .mi')].find(x=>x.textContent.includes(l));ok(!!e,'菜单项 '+l);e.click()};
scene('fresh');openSubpage('presets');S.presetSel='cv';render();
const cv=getPreset('简历脱敏');ok(cv.ver==='v3'&&cv.hist.length===2,'示例方案带历史版本');
click('pcat','date|off');ok(cv.ver==='v3','内容未变不升版本');
click('pcat','date|mask');ok(cv.ver==='v4'&&cv.hist[0].ver==='v3'&&cv.hist[0].data.cats.date==='off','保存一次升一版并留存旧内容');
for(const k of ['ph','mask','ph'])click('pcat','phone|'+k);
ok(cv.ver==='v7'&&cv.hist.length===4&&cv.hist.map(h=>h.ver).join()==='v6,v5,v4,v3','只保留最近 5 个版本');
click('pmore');pick('历史版本');ok(document.querySelectorAll('[data-a="verBack"]').length===4,'历史列表可回滚');
document.querySelector('[data-a="verBack"][data-v="3"]').click();
ok(cv.ver==='v8'&&cv.cats.date==='off'&&cv.cats.phone==='mask'&&cv.hist[0].ver==='v7','回滚以历史内容另存为新版本');
ok(cv.name==='简历脱敏','回滚不改名称');
click('pcopy');const cp=curPreset();ok(cp.ver==='v1'&&!cp.hist.length&&cp.name==='简历脱敏 副本','复制从 v1 开始且不带历史');
click('pcat','date|mask');ok(cp.ver==='v2'&&cv.ver==='v8','副本与原方案独立');
S.presetSel='office';render();click('pmore');ok(![...document.querySelectorAll('.pop .mi')].some(e=>e.textContent.includes('历史版本')),'内建方案没有版本历史');closePop();
const n=S.presets.length;click('pimp');ok(document.querySelector('#impName')&&S.presets.length===n,'同名导入先提示冲突');
document.querySelector('#impName').value='简历脱敏';click('impGo');ok(document.querySelector('#impErr').textContent&&S.presets.length===n,'改名仍重名时不导入');
document.querySelector('#impName').value='简历脱敏 · 外部';click('impGo');ok(S.presets.length===n+1&&curPreset().ver==='v1'&&curPreset().name==='简历脱敏 · 外部','改名后作为 v1 导入');
const before=getPreset('通用办公脱敏');click('pimp');click('closeModal');ok(S.presets.length===n+1&&before.ver==='内建','取消导入不写入');
S.ptab='rules';S.rsSel='corp';render();const corp=curSet();ok(corp.ver==='v2','示例规则集 v2');
click('rtog','0');ok(corp.ver==='v3'&&corp.hist[0].data.rules[0].on===true,'规则集修改升版本');
click('redit','2');click('rdSave');ok(corp.ver==='v3','规则内容未变不升版本');
click('rsmore');pick('历史版本');[...document.querySelectorAll('[data-a="verBack"]')].pop().click();
ok(corp.ver==='v4'&&corp.rules.length===2,'规则集回滚另存为新版本');
S.rsSel='fin';render();click('rscopy');const fc=curSet();ok(fc.ver==='v1'&&!fc.plugin&&!fc.hist.length,'规则包复制后独立');
const m=S.rulesets.length;click('rsimp');click('closeModal');ok(S.rulesets.length===m,'规则集同名导入可取消');
return checks.length+' checks passed';
})()`;
const {spawnSync}=require('node:child_process');
const {pathToFileURL}=require('node:url');
const path=require('node:path');
const assert=require('node:assert/strict');
const session='tacet-config-version-'+process.pid;
function browser(...args){const r=spawnSync('agent-browser',['--session',session,...args],{encoding:'utf8',timeout:20000});if(r.error)throw r.error;assert.equal(r.status,0,r.stderr||r.stdout);return r.stdout}
try{
 browser('open',pathToFileURL(path.resolve(__dirname,'../tacet-prototype.html')).href+'#scene=fresh');
 console.log(browser('eval',BEHAVIOR).trim());
 const errors=browser('errors').trim();assert.ok(!errors,errors);console.log('No browser errors.');
}finally{browser('close')}
