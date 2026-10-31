#!/usr/bin/env python3
"""Deterministic synthetic evaluation corpus for detection (M0 evidence).

Everything here is invented: names, numbers and addresses are generated from
small lists and checksum rules. The corpus is easier than real office material
and therefore says nothing about real-world quality; it exists to make
per-category, per-language, position-exact regression measurable.

Annotation policy: an entity is the sensitive value only, not its label
("电话：" is not part of the phone number). A phone number includes a country
prefix such as "+86 ". Dates are only those about a person (birth, document
validity, visit); log timestamps and publication dates are negatives.

Documents are built from one template family; families, not documents, are
split into dev / calibration / test, so near-duplicates never straddle splits.

Run: python3 -I tools/m0_eval_corpus.py OUT.jsonl
"""
import json
import random
import sys

SEED = 20261031

SURNAMES = list('王李张刘陈杨黄赵吴周徐孙马朱胡郭何高林罗郑梁谢宋唐许韩冯邓曹彭曾萧田董潘袁蔡蒋余于杜叶程魏苏吕丁')
RARE_SURNAMES = ['欧阳', '司马', '上官', '慕容', '诸葛', '尉迟', '皇甫', '公孙', '令狐', '东方']
GIVEN = ['伟', '芳', '娜', '敏', '静', '丽', '强', '磊', '军', '洋', '勇', '艳', '杰', '娟', '涛', '明', '超', '秀英', '华', '慧',
         '建国', '志强', '海燕', '晓东', '雪梅', '子轩', '梓涵', '浩然', '思远', '雨桐', '文博', '嘉怡', '俊杰', '美玲', '国栋']
EN_FIRST = ['James', 'Mary', 'Robert', 'Patricia', 'John', 'Jennifer', 'Michael', 'Linda', 'David', 'Elizabeth', 'William', 'Susan',
            'Daniel', 'Priya', 'Mateo', 'Fatima', 'Olivia', 'Noah', 'Sofia', 'Liam']
EN_LAST = ['Smith', 'Johnson', 'Williams', 'Brown', 'Jones', 'Garcia', 'Miller', 'Davis', 'Rodriguez', 'Martinez', 'Hernandez',
           'Lopez', 'Wilson', 'Anderson', 'Thomas', 'Taylor', 'Moore', "O'Brien", 'Nakamura', 'Okafor']
PROVINCES = [('北京市', ['海淀区', '朝阳区', '东城区']), ('上海市', ['浦东新区', '徐汇区', '静安区']), ('广东省广州市', ['天河区', '越秀区']),
             ('广东省深圳市', ['南山区', '福田区']), ('浙江省杭州市', ['西湖区', '余杭区']), ('四川省成都市', ['武侯区', '高新区']),
             ('江苏省南京市', ['鼓楼区', '江宁区']), ('湖北省武汉市', ['洪山区', '江汉区'])]
ROADS = ['中关村大街', '世纪大道', '天府大道', '解放路', '人民路', '科技园路', '文三路', '建国路', '长江路', '创新大道']
EN_STREETS = ['Baker Street', 'Maple Avenue', 'Oak Lane', 'Elm Street', 'Park Road', 'Lakeview Drive']
EN_CITIES = [('London', 'UK'), ('Austin', 'TX 78701'), ('Seattle', 'WA 98101'), ('Boston', 'MA 02108'), ('Denver', 'CO 80202')]
DOMAINS = ['example.com', 'corp.example.cn', 'mail.example.org', 'test.example.net', 'office.example.com.cn']
PROJECTS = ['曙光计划', '青云项目', '北斗二期', 'Project Falcon', 'Project Aurora', '蓝鲸专项']
PREFIX_MOBILE = ['130', '131', '132', '133', '135', '136', '137', '138', '139', '150', '151', '152', '155', '158', '159', '176', '177', '180', '182', '185', '186', '188', '189', '198']

rng = random.Random(SEED)


def digits(n):
    return ''.join(rng.choice('0123456789') for _ in range(n))


def luhn_fix(prefix15):
    total, double = 0, True
    for ch in reversed(prefix15):
        n = int(ch)
        if double:
            n *= 2
            if n > 9:
                n -= 9
        total += n
        double = not double
    return str((10 - total % 10) % 10)


def card(length=16, valid=True):
    body = rng.choice(['622202', '622848', '621700', '436742', '622588', '956033']) + digits(length - 7)
    check = luhn_fix(body)
    return body + (check if valid else str((int(check) + rng.randrange(1, 10)) % 10))


def resident_id(valid=True):
    area = rng.choice(['110105', '310115', '440106', '330106', '510107', '320106', '420111'])
    year = rng.randrange(1955, 2005)
    month = rng.randrange(1, 13)
    day = rng.randrange(1, 29)
    body = f'{area}{year:04d}{month:02d}{day:02d}{digits(3)}'
    weights = [7, 9, 10, 5, 8, 4, 2, 1, 6, 3, 7, 9, 10, 5, 8, 4, 2]
    check = '10X98765432'[sum(int(c) * w for c, w in zip(body, weights)) % 11]
    if not valid:
        check = rng.choice([c for c in '0123456789X' if c != check])
    return body + check


def zh_name():
    surname = rng.choice(RARE_SURNAMES) if rng.random() < 0.12 else rng.choice(SURNAMES)
    return surname + rng.choice(GIVEN)


def en_name():
    return f'{rng.choice(EN_FIRST)} {rng.choice(EN_LAST)}'


def zh_address():
    prov, districts = rng.choice(PROVINCES)
    s = f'{prov}{rng.choice(districts)}{rng.choice(ROADS)}{rng.randrange(1, 300)}号'
    if rng.random() < 0.5:
        s += f'{rng.randrange(1, 20)}栋{rng.randrange(1, 30)}0{rng.randrange(1, 9)}室'
    return s


def en_address():
    city, tail = rng.choice(EN_CITIES)
    return f'{rng.randrange(10, 9999)} {rng.choice(EN_STREETS)}, {city}, {tail}'


def mobile(variant=None):
    base = rng.choice(PREFIX_MOBILE) + digits(8)
    variant = variant or rng.choice(['plain', 'plain', 'dash', 'space', 'plus86'])
    if variant == 'dash':
        return f'{base[:3]}-{base[3:7]}-{base[7:]}', 'mobile_grouped'
    if variant == 'space':
        return f'{base[:3]} {base[3:7]} {base[7:]}', 'mobile_grouped'
    if variant == 'plus86':
        return f'+86 {base}', 'mobile_prefixed'
    return base, 'mobile_plain'


def landline():
    return f'{rng.choice(["010", "021", "020", "0755", "0571", "028", "025"])}-{digits(8) if rng.random() < .6 else digits(7)}'


def email():
    user = rng.choice(['zhang.wei', 'li_na', 'wang.fang88', 'chen-jie', 'liu.yang', 'jsmith', 'm.garcia', 'hr.office', 'finance2024'])
    return f'{user}@{rng.choice(DOMAINS)}'


def ipv4():
    return '.'.join(str(rng.randrange(1, 255)) for _ in range(4))


def url():
    return f'https://{rng.choice(["files", "wiki", "oa", "git"])}.{rng.choice(DOMAINS)}/{rng.choice(["doc", "share", "repo"])}/{digits(4)}?id={digits(6)}'


def api_key():
    kind = rng.choice(['sk', 'aws', 'gh'])
    alnum = 'abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789'
    if kind == 'sk':
        return 'sk-' + ''.join(rng.choice(alnum) for _ in range(32)), 'api_key_known_format'
    if kind == 'aws':
        return 'AKIA' + ''.join(rng.choice('ABCDEFGHIJKLMNOPQRSTUVWXYZ234567') for _ in range(16)), 'api_key_known_format'
    return 'ghp_' + ''.join(rng.choice(alnum) for _ in range(36)), 'api_key_known_format'


def password():
    chars = 'abcdefghijkmnpqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789!@#$%'
    return ''.join(rng.choice(chars) for _ in range(rng.randrange(8, 14)))


def account():
    kind = rng.choice(['user', 'staff', 'wx'])
    if kind == 'user':
        return rng.choice(['zhang_wei', 'lina', 'wangfang', 'chenjie', 'liuyang']) + digits(rng.randrange(2, 4)), '用户名', 'username'
    if kind == 'staff':
        return 'E' + digits(8), '工号', 'staff_id'
    return 'wxid_' + ''.join(rng.choice('abcdefghijklmnopqrstuvwxyz0123456789') for _ in range(10)), '微信号', 'wechat'


def person_date():
    kind = rng.choice(['birth', 'doc', 'visit'])
    y, m, d = rng.randrange(1960, 2005), rng.randrange(1, 13), rng.randrange(1, 29)
    if kind == 'birth':
        return rng.choice([f'{y}年{m}月{d}日', f'{y}-{m:02d}-{d:02d}']), '出生日期', 'birth_date'
    y = rng.randrange(2024, 2036) if kind == 'doc' else rng.randrange(2022, 2026)
    return rng.choice([f'{y}年{m}月{d}日', f'{y}-{m:02d}-{d:02d}']), ('身份证有效期至' if kind == 'doc' else '就诊日期'), kind + '_date'


class Doc:
    def __init__(self):
        self.text = ''
        self.entities = []

    def add(self, literal):
        self.text += literal

    def entity(self, category, subtype, value):
        start = len(self.text)
        self.text += value
        self.entities.append({'category': category, 'subtype': subtype, 'start': start, 'end': start + len(value), 'text_len': len(value)})


# Each segment template: name -> function(doc, lang). Segments are the units a family chooses from.
def seg_contact_zh(d):
    d.add('联系人：'); d.entity('person', 'zh_name', zh_name()); d.add('，手机：')
    v, sub = mobile(); d.entity('phone', sub, v); d.add('；邮箱：'); d.entity('email', 'email', email()); d.add('。\n')


def seg_contact_en(d):
    d.add('Please contact '); d.entity('person', 'en_name', en_name()); d.add(' at '); d.entity('email', 'email', email())
    d.add(' or call '); v, sub = mobile('plus86'); d.entity('phone', sub, v); d.add('.\n')


def seg_mixed_contact(d):
    d.add('客户 '); d.entity('person', 'en_name', en_name()); d.add(' 的邮箱是 '); d.entity('email', 'email', email())
    d.add('，座机 '); d.entity('phone', 'landline', landline()); d.add('。\n')


def seg_address_zh(d):
    d.add(rng.choice(['收货地址：', '公司位于', '住址：', '寄送至'])); d.entity('address', 'zh_address', zh_address()); d.add('。\n')


def seg_address_en(d):
    d.add(rng.choice(['Ship to: ', 'Office address: ', 'Home: '])); d.entity('address', 'en_address', en_address()); d.add('\n')


def seg_id_valid(d):
    d.add('姓名：'); d.entity('person', 'zh_name', zh_name()); d.add('，身份证号：'); d.entity('id_number', 'id_standard', resident_id(True)); d.add('\n')


def seg_id_suspect(d):
    d.add('证件号码（待核实）'); d.entity('id_number', 'id_suspect', resident_id(False)); d.add('\n')


def seg_card(d):
    d.add(rng.choice(['银行卡号：', '收款账号：', '工资卡 ']))
    n = rng.choice([16, 16, 19])
    value = card(n)
    if n == 16 and rng.random() < 0.4:
        value = ' '.join(value[i:i + 4] for i in range(0, 16, 4))
    d.entity('payment_card', 'card_declared_range', value); d.add('\n')


def seg_net(d):
    d.add('服务器 '); d.entity('ip_address', 'ipv4', ipv4()); d.add(' 管理页面 '); d.entity('url', 'url', url()); d.add('\n')


def seg_key(d):
    v, sub = api_key()
    d.add(rng.choice(['api_key = "', 'token: ', 'SECRET_KEY=']))
    d.entity('secret', sub, v); d.add(rng.choice(['"\n', '\n', '\n']))


def seg_password(d):
    d.add(rng.choice(['登录密码：', '初始密码为 ', 'password: ']))
    d.entity('secret', 'password_with_context', password()); d.add('\n')


def seg_account(d):
    v, label, sub = account()
    d.add(label + '：'); d.entity('account', sub, v); d.add('\n')


def seg_date(d):
    v, label, sub = person_date()
    d.add(label); d.add('：' if label != '身份证有效期至' else ''); d.entity('date', sub, v); d.add('\n')


def seg_project(d):
    d.add('本文件属于'); d.entity('project', 'codename', rng.choice(PROJECTS)); d.add('，请勿外传。\n')


def seg_neutral(d):
    d.add(rng.choice([
        '合同金额：128,000.00 元，分三期支付。\n', '版本 v2.3.1 已于近期发布，详见更新说明。\n',
        '订单号 ' + digits(18) + '，已发货。\n', '[2026-09-30 14:05:12] INFO request served in 35 ms\n',
        '文件校验值 3f9a' + digits(8) + 'c1e7，请核对。\n', 'The quarterly report is attached; totals are in Table 3.\n',
        '会议室 A302，下午 3 点开始，预计 90 分钟。\n', 'build #' + digits(5) + ' passed on branch main\n']))


SEGMENTS = {
    'contact_zh': seg_contact_zh, 'contact_en': seg_contact_en, 'contact_mixed': seg_mixed_contact,
    'address_zh': seg_address_zh, 'address_en': seg_address_en, 'id_valid': seg_id_valid, 'id_suspect': seg_id_suspect,
    'card': seg_card, 'net': seg_net, 'key': seg_key, 'password': seg_password, 'account': seg_account,
    'date': seg_date, 'project': seg_project, 'neutral': seg_neutral,
}
LANGUAGE = {'contact_zh': 'zh', 'contact_en': 'en', 'contact_mixed': 'mixed', 'address_en': 'en'}
FAMILY_KINDS = {
    'contract': ['contact_zh', 'address_zh', 'id_valid', 'project', 'neutral', 'date'],
    'resume': ['contact_zh', 'date', 'address_zh', 'account', 'neutral', 'id_valid'],
    'list': ['contact_zh', 'contact_mixed', 'account', 'card', 'neutral'],
    'finance': ['card', 'id_valid', 'contact_zh', 'neutral', 'date', 'project'],
    'log': ['net', 'key', 'password', 'neutral', 'account'],
    'english': ['contact_en', 'address_en', 'key', 'neutral', 'project'],
    'mixed': ['contact_mixed', 'address_zh', 'address_en', 'net', 'card', 'neutral'],
    'suspect': ['id_suspect', 'id_valid', 'contact_zh', 'neutral'],
}


def build():
    docs = []
    families = []
    for index in range(80):
        kind = rng.choice(sorted(FAMILY_KINDS))
        pool = FAMILY_KINDS[kind]
        chosen = rng.sample(pool, k=min(len(pool), rng.randrange(4, len(pool) + 1)))
        families.append((f'{kind}-{index:02d}', kind, chosen))
    order = list(range(len(families)))
    rng.shuffle(order)
    split_of = {}
    for rank, idx in enumerate(order):
        split_of[families[idx][0]] = 'dev' if rank < 40 else 'calibration' if rank < 56 else 'test'
    for family, kind, chosen in families:
        for n in range(16):
            d = Doc()
            for name in chosen:
                SEGMENTS[name](d)
            langs = {LANGUAGE.get(name, 'zh') for name in chosen}
            lang = 'mixed' if len(langs) > 1 or 'mixed' in langs else langs.pop()
            if kind == 'english':
                lang = 'en'
            docs.append({'id': f'{family}-{n:02d}', 'group': family, 'split': split_of[family], 'kind': kind, 'language': lang,
                         'text': d.text, 'entities': d.entities})
    # negatives: no sensitive content at all, with look-alike traps
    for n in range(60):
        d = Doc()
        for _ in range(rng.randrange(3, 7)):
            seg_neutral(d)
        docs.append({'id': f'negative-{n:02d}', 'group': f'negative-{n // 6}', 'split': 'test' if n % 3 == 0 else 'dev', 'kind': 'negative',
                     'language': 'mixed', 'text': d.text, 'entities': []})
    for doc in docs:
        for e in doc['entities']:
            assert e['end'] - e['start'] == e.pop('text_len')
            assert doc['text'][e['start']:e['end']].strip() == doc['text'][e['start']:e['end']]
        doc['entities'].sort(key=lambda e: (e['start'], e['end']))
    return docs


if __name__ == '__main__':
    out = sys.argv[1]
    documents = build()
    with open(out, 'w', encoding='utf-8') as handle:
        for doc in documents:
            handle.write(json.dumps(doc, ensure_ascii=False) + '\n')
    counts = {}
    for doc in documents:
        for e in doc['entities']:
            counts[e['category']] = counts.get(e['category'], 0) + 1
    print(len(documents), 'documents', sum(counts.values()), 'entities', dict(sorted(counts.items())))
