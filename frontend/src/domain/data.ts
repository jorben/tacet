// @ts-nocheck

// Domain constants and demo-data generators, copied from the prototype (docs/v1/tacet-prototype.html).
/* eslint-disable */
export const TYPES={org:{l:'公司名',ph:'公司'},name:{l:'人名',ph:'姓名'},phone:{l:'电话',ph:'电话'},email:{l:'邮箱',ph:'邮箱'},id:{l:'身份证',ph:'证件号'},addr:{l:'地址',ph:'地址'},card:{l:'银行卡',ph:'卡号'},date:{l:'日期',ph:'日期'},other:{l:'其他',ph:'已隐去'}};
export const SRC={org:'模型 · 词表',name:'模型',addr:'模型',date:'模型',phone:'规则',email:'规则',id:'规则 · 校验',card:'规则 · 校验',other:'人工'};
export const DOC_SRC=[
 [['title','劳 动 合 同'],['meta','合同编号：HT-2026-0417'],['img','photo'],
  ['p','甲方（用人单位）：[[org|杭州澄音科技有限公司]]'],['p','法定代表人：[[name|陈立群]]'],
  ['p','乙方（劳动者）：[[name|林晓雯]]'],['p','身份证号码：[[id|330106199203154827]]'],
  ['p','出生日期：[[date|1992 年 3 月 15 日]]'],['p','联系电话：[[phone|138 0571 6624]]'],
  ['p','电子邮箱：[[email|linxiaowen92@163.com]]'],
  ['p','现住址：[[addr|杭州市西湖区文三路 478 号华星时代广场 B 座 1203 室]]'],
  ['p','紧急联系人：[[name|林国栋]]（父亲），电话：[[phone|0571-8823 4410]]'],
  ['h','第一条　合同期限'],
  ['pi','本合同为固定期限劳动合同，自 2026 年 5 月 1 日起至 2029 年 4 月 30 日止，其中试用期三个月。试用期满经考核合格的，甲方以书面形式通知乙方转正。'],
  ['h','第二条　工作内容与地点'],
  ['pi','乙方同意在甲方产品研发部担任高级交互设计师岗位，工作地点为杭州市。甲方因生产经营需要调整乙方工作岗位的，应与乙方协商一致并以书面形式确认。']],
 [['h','第三条　劳动报酬'],
  ['pi','甲方于每月 10 日前以银行转账方式支付乙方上月工资。乙方工资账户：[[card|6222 0212 0300 4417 885]]，开户行：[[org|中国工商银行杭州文三路支行]]。'],
  ['pi','乙方税前月工资为人民币 28,000 元，试用期工资为正式工资的 90%。年终奖金依据甲方绩效管理制度另行核定，由甲方于次年一季度内发放。'],
  ['h','第四条　工作时间与休假'],
  ['pi','甲方实行标准工时制度，乙方每日工作 8 小时，每周工作 40 小时。乙方依法享有法定节假日、年休假、婚假、产假等假期。'],
  ['h','第五条　社会保险与福利'],
  ['pi','甲方依法为乙方缴纳养老、医疗、失业、工伤、生育保险及住房公积金。乙方个人应缴部分由甲方从工资中代扣代缴。'],
  ['h','第六条　保密义务'],
  ['pi','乙方在职期间及离职后两年内，对甲方的商业秘密负有保密义务。乙方的直属上级为[[name|周子航|l]]，保密事项的解释由甲方人力资源部负责。'],
  ['pi','人力资源联系人：[[name|何静]]，邮箱：[[email|hejing@chengyin.tech]]，电话：[[phone|0571-8796 2200|l]]。如乙方联系方式变更，应于七日内书面告知 [[name|何静]]。']],
 [['h','第七条　合同的变更与解除'],
  ['pi','经甲乙双方协商一致，可以变更本合同约定的内容。变更应当采用书面形式，变更后的文本由双方各执一份。'],
  ['pi','乙方提前三十日以书面形式通知甲方，可以解除本合同。乙方在试用期内提前三日通知甲方，可以解除本合同。'],
  ['h','第八条　争议解决'],
  ['pi','因履行本合同发生的争议，双方应协商解决；协商不成的，可向甲方所在地劳动争议仲裁委员会申请仲裁。本合同由[[org|安然律师事务所|l]]见证。'],
  ['h','第九条　其他'],
  ['pi','本合同一式两份，甲乙双方各执一份，自双方签字（盖章）之日起生效。本合同未尽事宜，按国家有关法律法规执行。'],
  ['sign','乙方（签字）：[[name|林晓雯]]']]
];
export function buildDoc(){
 const hits=[],imgs=[],pages=[];let para=0,sec='';
 DOC_SRC.forEach((pg,pi)=>{
  const blocks=pg.map(([k,s],bi)=>{
   para++;if(k==='h')sec=s;const at={page:pi,para,sec};
   if(k==='img'){imgs.push(Object.assign({kind:s,label:'证件照',act:'remove'},at));return{k,img:imgs.length-1,at}}
   const b={k,segs:[],at};
   if(k==='sign'){imgs.push(Object.assign({kind:'seal',label:'公章',act:'remove'},at));b.img=imgs.length-1}
   const re=/\[\[(\w+)\|([^|\]]+)(?:\|(l))?\]\]/g;let last=0,m;
   while((m=re.exec(s))){
    if(m.index>last)b.segs.push({t:s.slice(last,m.index)});
    hits.push({id:hits.length,type:m[1],text:m[2],low:!!m[3],seen:false,src:m[3]?'模型 · 低置信':SRC[m[1]],state:'redact',page:pi,para,sec});
    b.segs.push({h:hits.length-1});last=re.lastIndex;
   }
   if(last<s.length)b.segs.push({t:s.slice(last)});
   return b;
  });
  pages.push(blocks);
 });
 return{hits,imgs,pages};
}
/* DOCX has no fixed pages: the page comes from saved page-break hints (approximate), so headings and paragraph numbers carry the location */
export function locStr(ext,p,sec,para,short){
 if(ext!=='docx')return '第 '+p+' 页';
 return [p&&!short?'约第 '+p+' 页':'',sec?(short?sec.split(/\s/)[0]:sec):'','第 '+para+' 段'].filter(Boolean).join(' · ');
}
export const OFFICE_CATS=['org','name','phone','email','id','addr','card','date'];

export const MODEL_LABELS={missing:'待下载',downloading:'下载中',paused:'已暂停',verifying:'校验中',loading:'准备中',ready:'已就绪',error:'需处理'};
export const MODEL_ERRORS={
 offline:['下载已中断','网络连接不可用，已下载的部分会保留。连接网络后继续下载。','继续下载'],
 disk:['可用空间不足','请先释放磁盘空间，再重试。安装需要的空间包含下载文件、展开文件与临时副本。','检查空间并重试'],
 integrity:['模型文件校验未通过','文件不完整或与当前应用版本不匹配，尚未加载。请重新下载此模型。','重新下载'],
 load:['模型未能启动','文件校验已通过，但本地试运行失败。请关闭占用内存较多的应用后重试；仍失败时可重新下载。','重新准备']
};

export const SUR='王李张刘陈杨黄赵吴周徐孙马朱胡郭何高林罗'.split('');
export const GIV=['雨桐','子轩','思远','欣怡','浩然','佳琪','俊杰','梓涵','一鸣','诗雨','嘉怡','宇航','若溪','明哲'];
export const KIND=[['简历-{n}','pdf'],['简历-{n}','docx'],['劳动合同-{n}','docx'],['采购合同-2026-{k}','pdf'],['客户回访-{k}','docx'],['报价单-{k}','pdf'],['入职登记-{n}','docx'],['付款明细-{k}','pdf']];
export const GDEF=[
 {id:'pwd',ic:'lock',t:'需要密码',unit:'份',block:true,frac:.01,min:1,acts:[{l:'解锁',k:'pwd'},{l:'跳过此文件',k:'skip',n:'不导出'}]},
 {id:'fail',ic:'retry',t:'处理失败',unit:'份',block:true,after:true,acts:[{l:'重新处理',k:'redo'},{l:'跳过此文件',k:'skip',n:'不导出'}]},
 {id:'scan',ic:'scan',t:'扫描页，无文本层',unit:'份',ext:'pdf',frac:.014,min:1,gap:true,acts:[{l:'涂抹',k:'do',n:'整页'},{l:'保留',k:'keep',n:'部分结果'}]},
 {id:'shot',ic:'image',t:'截图中含文字',unit:'处',frac:.056,per:[2,5],gap:true,acts:[{l:'涂抹',k:'do',n:'整张截图'},{l:'保留',k:'keep',n:'部分结果'}]},
 {id:'idck',ic:'hash',t:'疑似身份证号 · 校验位不符',unit:'处',frac:.042,per:[1,4],post:.01,acts:[{l:'涂抹',k:'do',n:'整个号码'},{l:'保留',k:'keep'}]},
 {id:'embed',ic:'clip',t:'嵌入文件',unit:'个',frac:.028,per:[1,2],gap:true,acts:[{l:'涂抹',k:'do',n:'整个附件'},{l:'保留',k:'keep',n:'部分结果'}]},
 {id:'curve',ic:'pen',t:'疑似文字转曲',unit:'处',ext:'pdf',frac:.014,per:[1,3],acts:[{l:'涂抹',k:'do',n:'该区域'},{l:'保留',k:'keep'}]},
];

export const STL={queue:['排队',''],proc:['处理中','pri'],wait:['待你处理','warn'],held:['待你处理','warn'],ready:['待导出','pri'],done:['已导出','ok'],partial:['部分结果','ok'],bad:['无法处理','bad'],skipped:['已跳过','ghost']};
export const NODOC=['pwd','fail','strict'];
export const RVSRC={strict:'严格方式 · 每份由你确认后才导出',idck:'格式像身份证号，但末位校验码不符',shot:'截图 · 可能含文字',embed:'嵌入文件 · 内容未检查',curve:'路径图形 · 疑似文字',scan:'整页图像 · 无文本层',pwd:'文件已加密'};
export const DOCT={'简历':'个人简历','劳动合同':'劳动合同','采购合同':'采购合同','客户回访':'客户回访记录','报价单':'报价单','入职登记':'入职登记表','付款明细':'付款明细'};
export const FILL=['双方应本着诚实信用原则履行约定的各项义务，未尽事宜另行协商。','相关资料仅限本次业务使用，未经书面同意不得向第三方披露。','如有变更，应提前以书面形式通知对方，并经双方确认后生效。','附件与正文具有同等效力，二者不一致的以正文为准。','本记录由经办人整理，经复核后归档，保存期限按公司制度执行。'];
export const rdx=n=>'<span class="rd">'+'国'.repeat(n)+'</span>';
export const BD={digit:'数字',word:'字母或数字'};
export const SCENES=[
 ['h','入口'],['首页','home'],['首页 · 首次使用','fresh'],
 ['h','模型准备'],['下载中','model-download'],['下载已暂停','model-paused'],['断网 / 下载中断','model-offline'],['空间不足','model-disk'],['校验失败','model-integrity'],['加载与试运行','model-loading'],['启动失败','model-load'],['已选文件 · 等待模型','model-wait'],['批量 · 模型未就绪','model-batch'],['插件 · 模型待下载','model-plugins'],
 ['h','单份'],['检测中','s1'],['复核','s2'],['复核 · DOCX','s5'],['左右对比','s4'],['完成','s3'],
 ['h','批量'],['预检','b1'],['处理中','b2'],['人工复核','b5'],['完成','b3'],['无人值守 · 完成','b6'],['导出清单','b4'],
 ['h','其他'],['方案','presets'],['插件','plugins'],['设置','settings'],['关闭窗口','close'],
];
