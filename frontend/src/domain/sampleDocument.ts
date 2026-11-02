// The built-in sample document the prototype reviews, written as a compact markup and expanded by `buildDoc`.
import type { Block, BlockKind, CategoryId, DocContent, DocImage, Hit } from "./types";

type SourceBlock = [BlockKind | "img", string];

/**
 * Markup: `[[type|text]]` marks a hit, `[[type|text|l]]` a low-confidence one.
 * An `img` block carries the image kind (`photo`) in place of text.
 */
const PAGES: SourceBlock[][] = [
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

/** The markup's short type codes, as category ids. */
const TYPE: Record<string, CategoryId> = {
  org: "organization", name: "person", id: "id_number", addr: "address", card: "payment_card", date: "date", phone: "phone", email: "email",
};
/** What the demo says found each kind of hit. */
const HIT_SOURCE: Record<CategoryId, string> = {
  organization: "模型 · 词表", person: "模型", address: "模型", date: "模型", phone: "规则", email: "规则",
  id_number: "规则 · 校验", payment_card: "规则 · 校验",
};

const HIT_PATTERN = /\[\[(\w+)\|([^|\]]+)(?:\|(l))?\]\]/g;

/** Expands the sample document into pages of blocks, plus the hits and images found in it. */
export function buildDocument(): DocContent {
  const hits: Hit[] = [];
  const imgs: DocImage[] = [];
  const pages: Block[][] = [];
  let para = 0;
  let sec = "";
  PAGES.forEach((pg, page) => {
    const blocks = pg.map(([k, s]): Block => {
      para++;
      if (k === "h") sec = s;
      const at = { page, para, sec };
      if (k === "img") {
        imgs.push({ kind: s as "photo", label: "证件照", act: "remove", ...at });
        return { k, img: imgs.length - 1, at };
      }
      const block: Block = { k, segs: [], at };
      if (k === "sign") {
        imgs.push({ kind: "seal", label: "公章", act: "remove", ...at });
        block.img = imgs.length - 1;
      }
      let last = 0;
      let m: RegExpExecArray | null;
      const re = new RegExp(HIT_PATTERN);
      while ((m = re.exec(s))) {
        if (m.index > last) block.segs!.push({ t: s.slice(last, m.index) });
        const type = TYPE[m[1]!] ?? m[1]!;
        hits.push({
          id: hits.length, type, text: m[2]!, low: !!m[3], seen: false,
          src: m[3] ? "模型 · 低置信" : (HIT_SOURCE[type] ?? "规则"), state: "redact", ...at,
        });
        block.segs!.push({ h: hits.length - 1 });
        last = re.lastIndex;
      }
      if (last < s.length) block.segs!.push({ t: s.slice(last) });
      return block;
    });
    pages.push(blocks);
  });
  return { hits, imgs, pages };
}
