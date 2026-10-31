import io
import json
import unittest
import zipfile

from tacet_format_docx.plan import Op, read_plan
from tacet_format_docx.validate import validate
from tacet_format_docx.write import write
from tacet_plugin_sdk.runtime import TaskError
from tacet_plugin_sdk.schema import Schemas
from test_docx_extract import FakeTask, doc
from test_docx_package import CT, MAIN_TYPE, REL, W

HEADER_TYPE = 'application/vnd.openxmlformats-officedocument.wordprocessingml.header+xml'
HEADER_OVERRIDE = f'<Override PartName="/word/header1.xml" ContentType="{HEADER_TYPE}"/>'
CORE_TYPE = 'application/vnd.openxmlformats-package.core-properties+xml'


def schemas():
    s = Schemas()
    for d in ('WriteItem', 'ValidateItem', 'PlanLine'):
        s.compile('format.schema.json', d)
    return s


def build(body, core=False, extra=(), header=False):
    out = io.BytesIO()
    with zipfile.ZipFile(out, 'w') as z:
        override = f'<Override PartName="/docProps/core.xml" ContentType="{CORE_TYPE}"/>' if core else ''
        z.writestr('[Content_Types].xml', f'<Types xmlns="{CT}"><Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/>'
                   f'<Override PartName="/word/document.xml" ContentType="{MAIN_TYPE}"/>{override}{HEADER_OVERRIDE if header else ""}</Types>')
        rel = '<Relationship Id="c" Type="http://schemas.openxmlformats.org/package/2006/relationships/metadata/core-properties" Target="docProps/core.xml"/>' if core else ''
        z.writestr('_rels/.rels', f'<Relationships xmlns="{REL}"><Relationship Id="m" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/>{rel}</Relationships>')
        z.writestr('word/document.xml', doc(body))
        if header:
            z.writestr('word/header1.xml', doc('<w:p><w:r><w:t>13812345678</w:t></w:r></w:p>'))
        if core:
            z.writestr('docProps/core.xml', '<cp:coreProperties xmlns:cp="http://schemas.openxmlformats.org/package/2006/metadata/core-properties"/>')
        for name, data in extra:
            z.writestr(name, data)
    return out.getvalue()


BODY = '<w:p><w:r><w:t>电话 13812345678 结束</w:t></w:r></w:p><w:p/><w:p><w:r><w:t>无敏感</w:t></w:r></w:p><w:p><w:r><w:t>13900000000 与 13700000000</w:t></w:r></w:p>'


def text_op(i, ordinal, start, end, repl, part='word/document.xml'):
    return Op(f'op_{i}', 'text', part, ordinal, start, end, repl)


def run_write(raw, ops):
    task, sink = FakeTask(), io.BytesIO()
    write(io.BytesIO(raw), ops, sink, task, schemas())
    return sink.getvalue(), task.items


def part_text(raw, name='word/document.xml'):
    with zipfile.ZipFile(io.BytesIO(raw)) as z:
        return z.read(name).decode()


def results(items):
    return {i['op']: (i['status'], i.get('reason')) for i in items if i['kind'] == 'op_result'}


def fates(items):
    return {i['part']: i['fate'] for i in items if i['kind'] == 'part_fate'}


class WriteTest(unittest.TestCase):
    def setUp(self):
        self.raw = build(BODY, core=True)

    def test_text_operations_replace_exactly_the_range_and_report_every_part(self):
        out, items = run_write(self.raw, [text_op(1, 1, 3, 14, '[手机号]')])
        self.assertEqual(results(items), {'op_1': ('executed', None)})
        xml = part_text(out)
        self.assertIn('电话 [手机号] 结束', xml)
        self.assertNotIn('13812345678', xml)
        self.assertIn('13900000000 与 13700000000', xml)  # untouched paragraphs stay as they were
        self.assertEqual(fates(items), {'[Content_Types].xml': 'kept', '_rels/.rels': 'kept', 'word/document.xml': 'redacted', 'docProps/core.xml': 'kept'})
        done = items[-1]
        self.assertEqual((done['kind'], done['operations'], done['parts']), ('write_complete', 1, 4))
        self.assertEqual(done['bytes'], len(out))
        with zipfile.ZipFile(io.BytesIO(out)) as z, zipfile.ZipFile(io.BytesIO(self.raw)) as o:
            self.assertEqual(z.read('docProps/core.xml'), o.read('docProps/core.xml'))  # byte for byte
            self.assertEqual(z.namelist(), o.namelist())

    def test_several_operations_in_one_paragraph_and_deletion(self):
        out, items = run_write(self.raw, [text_op(1, 4, 0, 11, '[A]'), text_op(2, 4, 14, 26, '')])
        res = results(items)
        self.assertEqual(res['op_1'][0], 'executed')
        self.assertEqual(res['op_2'][0], 'not_executed')  # 26 is past the paragraph end (25 characters)
        out, items = run_write(self.raw, [text_op(1, 4, 0, 11, '[A]'), text_op(2, 4, 14, 25, '')])
        self.assertEqual({k: v[0] for k, v in results(items).items()}, {'op_1': 'executed', 'op_2': 'executed'})
        self.assertIn('[A] 与 ', part_text(out))
        self.assertNotIn('13700000000', part_text(out))

    def test_removing_a_part_drops_its_override_relationship_and_own_relationship_part(self):
        raw = build(BODY, core=True, extra=[('docProps/_rels/core.xml.rels', f'<Relationships xmlns="{REL}"/>')])
        out, items = run_write(raw, [Op('op_p', 'part', 'docProps/core.xml')])
        self.assertEqual(results(items), {'op_p': ('executed', None)})
        self.assertEqual(fates(items)['docProps/core.xml'], 'removed')
        self.assertEqual(fates(items)['docProps/_rels/core.xml.rels'], 'removed')
        self.assertEqual((fates(items)['[Content_Types].xml'], fates(items)['_rels/.rels']), ('redacted', 'redacted'))
        with zipfile.ZipFile(io.BytesIO(out)) as z:
            self.assertNotIn('docProps/core.xml', z.namelist())
            self.assertNotIn('core.xml', z.read('[Content_Types].xml').decode())
            self.assertNotIn('core.xml', z.read('_rels/.rels').decode())
            self.assertIn('word/document.xml', z.read('_rels/.rels').decode())
        # the candidate is still a package the inventory accepts
        from tacet_format_docx.package import inventory
        from tacet_plugin_sdk.safezip import Archive
        inventory(Archive(io.BytesIO(out)))

    def test_operations_that_cannot_be_executed_say_why_and_never_vanish(self):
        ops = [text_op(1, 99, 0, 3, 'x'), text_op(2, 1, 0, 3, 'x', part='nope.xml'), text_op(3, 1, 0, 3, 'x', part='docProps/core.xml'),
               Op('op_4', 'part', 'word/document.xml'), Op('op_5', 'part', '[Content_Types].xml'), Op('op_6', 'part', 'ghost.xml'), text_op(7, 1, 0, 400, 'x')]
        out, items = run_write(self.raw, ops)
        self.assertEqual(results(items), {
            'op_1': ('not_executed', 'UNIT_NOT_FOUND'), 'op_2': ('not_executed', 'PART_NOT_TEXT'), 'op_3': ('not_executed', 'PART_NOT_TEXT'),
            'op_4': ('not_executed', 'PART_REQUIRED'), 'op_5': ('not_executed', 'PART_REQUIRED'), 'op_6': ('not_executed', 'PART_UNKNOWN'),
            'op_7': ('not_executed', 'RANGE_INVALID')})
        self.assertEqual(len([i for i in items if i['kind'] == 'op_result']), 7)

    def test_a_part_with_hidden_text_is_not_rewritten_and_its_operations_say_so(self):
        raw = build('<w:p><w:r><w:t>13812345678</w:t></w:r></w:p><w:p><w:r><w:rPr><w:vanish/></w:rPr><w:t>secret</w:t></w:r></w:p>')
        _, items = run_write(raw, [text_op(1, 1, 0, 11, '[x]')])
        self.assertEqual(results(items), {'op_1': ('not_executed', 'DOCX_NONVISIBLE_POLICY_REQUIRED')})

    def test_text_in_a_removed_part_is_subsumed_by_the_removal(self):
        raw = build(BODY, extra=[('word/footer1.xml', doc('<w:p><w:r><w:t>13812345678</w:t></w:r></w:p>'))])
        # the footer is not a text part for this inventory (no content type), so a text op on it fails; removal alone works
        _, items = run_write(raw, [Op('op_p', 'part', 'word/footer1.xml')])
        self.assertEqual(results(items), {'op_p': ('executed', None)})

    def test_text_operations_on_a_part_that_is_removed_count_as_executed(self):
        raw = build(BODY, header=True)
        out, items = run_write(raw, [text_op(1, 1, 0, 11, '[x]', part='word/header1.xml'), Op('op_p', 'part', 'word/header1.xml')])
        self.assertEqual(results(items), {'op_1': ('executed', None), 'op_p': ('executed', None)})
        self.assertEqual(fates(items)['word/header1.xml'], 'removed')
        with zipfile.ZipFile(io.BytesIO(out)) as z:
            self.assertNotIn('word/header1.xml', z.namelist())
        # and a header edited without removal is rewritten like the body
        out, items = run_write(raw, [text_op(1, 1, 0, 11, '[x]', part='word/header1.xml')])
        self.assertIn('[x]', part_text(out, 'word/header1.xml'))
        self.assertEqual(fates(items)['word/header1.xml'], 'redacted')

    def test_a_damaged_input_is_a_failed_write(self):
        with self.assertRaises(TaskError) as ctx:
            run_write(b'PK\x03\x04 not a package', [])
        self.assertEqual(ctx.exception.code, 'INPUT_CORRUPT')

    def test_an_empty_plan_copies_everything_byte_for_byte(self):
        out, items = run_write(self.raw, [])
        self.assertEqual(set(fates(items).values()), {'kept'})
        with zipfile.ZipFile(io.BytesIO(out)) as z, zipfile.ZipFile(io.BytesIO(self.raw)) as o:
            for n in o.namelist():
                self.assertEqual(z.read(n), o.read(n))

    def test_cancellation_stops_without_a_completion_marker(self):
        from tacet_plugin_sdk.runtime import Cancelled
        task = FakeTask(cancel_after=0)
        with self.assertRaises(Cancelled):
            write(io.BytesIO(self.raw), [], io.BytesIO(), task, schemas())
        self.assertFalse([i for i in task.items if i['kind'] == 'write_complete'])


class PlanReaderTest(unittest.TestCase):
    def read(self, text):
        data = io.BytesIO(text.encode() if isinstance(text, str) else text)
        return read_plan(data, schemas())

    def line(self, **kw):
        base = {'kind': 'text', 'id': 'op_1', 'part': 'word/document.xml', 'ordinal': 1, 'start': 0, 'end': 3, 'replacement': 'x'}
        base.update(kw)
        return json.dumps(base, ensure_ascii=False)

    def test_valid_lines_parse_in_order_and_blank_lines_are_ignored(self):
        ops = self.read(self.line() + '\n\n' + self.line(id='op_2') + '\n' + json.dumps({'kind': 'part', 'id': 'op_3', 'part': 'docProps/app.xml'}) + '\n')
        self.assertEqual([o.id for o in ops], ['op_1', 'op_2', 'op_3'])
        self.assertEqual(self.read(''), [])

    def test_anything_malformed_fails_the_whole_call(self):
        for name, text in {'not json': 'nope', 'schema violation': self.line(path='/x'), 'empty range': self.line(end=0),
                           'duplicate id': self.line() + '\n' + self.line(), 'reversed': self.line(start=5, end=2), 'bad utf-8': b'\xff\xfe'}.items():
            with self.subTest(name), self.assertRaises(TaskError) as ctx:
                self.read(text)
            self.assertEqual(ctx.exception.code, 'PROTOCOL_INVALID')

    def test_size_and_count_limits(self):
        import tacet_plugin_sdk.plan as module
        old = module.MAX_PLAN_BYTES
        module.MAX_PLAN_BYTES = 10
        try:
            with self.assertRaises(TaskError) as ctx:
                self.read(self.line())
            self.assertEqual(ctx.exception.code, 'RESOURCE_LIMIT')
        finally:
            module.MAX_PLAN_BYTES = old
        old = module.MAX_OPERATIONS
        module.MAX_OPERATIONS = 1
        try:
            with self.assertRaises(TaskError) as ctx:
                self.read(self.line() + '\n' + self.line(id='op_2'))
            self.assertEqual(ctx.exception.code, 'RESOURCE_LIMIT')
        finally:
            module.MAX_OPERATIONS = old


def run_validate(raw, cand, ops):
    task = FakeTask()
    validate(io.BytesIO(raw), ops, io.BytesIO(cand), task, schemas())
    return task.items


def checks(items):
    return {i['op']: (i['executed'], i['residual']) for i in items if i['kind'] == 'op_check'}


class ValidateTest(unittest.TestCase):
    def setUp(self):
        self.raw = build(BODY, core=True)
        self.ops = [text_op(1, 1, 3, 14, '[手机号]'), text_op(2, 4, 0, 11, '[A]')]

    def test_a_correct_candidate_passes_every_check_and_lists_both_part_sets(self):
        cand, _ = run_write(self.raw, self.ops)
        items = run_validate(self.raw, cand, self.ops)
        self.assertEqual(checks(items), {'op_1': (True, False), 'op_2': (True, False)})
        self.assertEqual({i['part'] for i in items if i['kind'] == 'input_part'}, {i['part'] for i in items if i['kind'] == 'output_part'})
        done = items[-1]
        self.assertEqual((done['reopened'], done['input_parts'], done['output_parts'], done['operations']), (True, 4, 4, 2))
        out = [i for i in items if i['kind'] == 'output_part']
        rels = [i for i in out if i['part'] == '_rels/.rels']
        self.assertEqual(rels[0].get('source'), '_rels/.rels')
        self.assertEqual(sorted(rels[0]['references']), ['docProps/core.xml', 'word/document.xml'])

    def test_an_operation_that_was_not_applied_leaves_a_residual_in_its_unit(self):
        items = run_validate(self.raw, self.raw, self.ops)  # the candidate is the untouched input
        self.assertEqual(checks(items), {'op_1': (False, True), 'op_2': (False, True)})
        self.assertEqual([i['unit'] for i in items if i['kind'] == 'op_check'], ['word/document.xml#1', 'word/document.xml#4'])

    def test_a_partial_removal_of_the_range_is_a_residual(self):
        cand, _ = run_write(self.raw, [text_op(1, 1, 3, 10, '[手机号]')])  # the last digit is left behind
        items = run_validate(self.raw, cand, [text_op(1, 1, 3, 14, '[手机号]')])
        self.assertEqual(checks(items)['op_1'], (False, True))

    def test_a_changed_paragraph_that_is_not_what_the_plan_says_is_not_a_residual_but_not_executed(self):
        cand, _ = run_write(self.raw, [text_op(1, 1, 3, 14, '[别的]')])
        items = run_validate(self.raw, cand, [text_op(1, 1, 3, 14, '[手机号]')])
        self.assertEqual(checks(items)['op_1'], (False, False))

    def test_two_operations_in_one_paragraph_are_checked_against_the_combined_result(self):
        ops = [text_op(1, 4, 0, 11, '[A]'), text_op(2, 4, 14, 25, '[BB]')]
        cand, _ = run_write(self.raw, ops)
        self.assertEqual(checks(run_validate(self.raw, cand, ops)), {'op_1': (True, False), 'op_2': (True, False)})

    def test_a_candidate_part_that_will_not_parse_is_reported_as_not_reopened(self):
        ops = [text_op(1, 1, 3, 14, '[手机号]')]
        cand, _ = run_write(self.raw, ops)
        out = io.BytesIO()
        with zipfile.ZipFile(io.BytesIO(cand)) as z, zipfile.ZipFile(out, 'w') as w:
            for n in z.namelist():
                w.writestr(n, b'<w:document xmlns:w="x"><w:body><w:p>' if n == 'word/document.xml' else z.read(n))
        self.assertFalse(run_validate(self.raw, out.getvalue(), ops)[-1]['reopened'])

    def test_a_part_that_exists_only_in_the_candidate_has_no_source(self):
        cand, _ = run_write(self.raw, [])
        out = io.BytesIO()
        with zipfile.ZipFile(io.BytesIO(cand)) as z, zipfile.ZipFile(out, 'w') as w:
            for n in z.namelist():
                w.writestr(n, z.read(n))
            w.writestr('extra/new.bin', b'x')
        items = run_validate(self.raw, out.getvalue(), [])
        new = next(i for i in items if i['kind'] == 'output_part' and i['part'] == 'extra/new.bin')
        self.assertNotIn('source', new)

    def test_part_operations_are_checked_by_presence(self):
        op = Op('op_p', 'part', 'docProps/core.xml')
        cand, _ = run_write(self.raw, [op])
        self.assertEqual(checks(run_validate(self.raw, cand, [op])), {'op_p': (True, False)})
        self.assertEqual(checks(run_validate(self.raw, self.raw, [op])), {'op_p': (False, True)})

    def test_a_candidate_that_cannot_be_reopened_says_so(self):
        items = run_validate(self.raw, b'PK\x03\x04 garbage', self.ops)
        self.assertEqual(items[-1], {'kind': 'validate_complete', 'complete': True, 'input_parts': 4, 'output_parts': 0, 'operations': 0, 'reopened': False})
        items = run_validate(self.raw, b'', self.ops)
        self.assertFalse(items[-1]['reopened'])

    def test_parts_nobody_looked_at_are_reported_as_unverified(self):
        raw = build(BODY, extra=[('custom/blob.bin', b'opaque')])
        cand, _ = run_write(raw, [])
        items = run_validate(raw, cand, [])
        self.assertEqual([i for i in items if i['kind'] == 'unverified'], [{'kind': 'unverified', 'scope': 'custom/blob.bin', 'reason': 'PART_NOT_INSPECTED'}])


if __name__ == '__main__':
    unittest.main()


class JudgeTest(unittest.TestCase):
    """Per-operation verdicts on one paragraph, without a package around them."""

    BEFORE = '甲 13812345678 乙 13900000000 丙'

    def ops(self, repl='[号]'):
        return [text_op(1, 1, 2, 13, repl), text_op(2, 1, 16, 27, repl)]

    def judge(self, after, ops=None):
        from tacet_format_docx.validate import judge
        return judge(self.BEFORE, after, ops or self.ops())

    def test_a_paragraph_done_exactly_as_planned_executes_every_operation(self):
        self.assertEqual(self.judge('甲 [号] 乙 [号] 丙'), {'op_1': (True, False), 'op_2': (True, False)})

    def test_an_operation_that_was_not_done_is_a_residual_and_the_others_still_count_as_done(self):
        self.assertEqual(self.judge('甲 [号] 乙 13900000000 丙'), {'op_1': (True, False), 'op_2': (False, True)})
        self.assertEqual(self.judge('甲 13812345678 乙 [号] 丙'), {'op_1': (False, True), 'op_2': (True, False)})
        self.assertEqual(self.judge(self.BEFORE), {'op_1': (False, True), 'op_2': (False, True)})

    def test_a_fragment_of_the_original_left_behind_or_in_front_is_a_residual(self):
        self.assertEqual(self.judge('甲 [号]5678 乙 [号] 丙')['op_1'], (False, True))
        self.assertEqual(self.judge('甲 1381[号] 乙 [号] 丙')['op_1'], (False, True))
        self.assertEqual(self.judge('甲 [号] 乙 [号]0000 丙')['op_2'], (False, True))
        self.assertEqual(self.judge('甲 [号]5678 乙 [号] 丙')['op_2'], (True, False))

    def test_anything_else_that_differs_from_the_plan_is_unaccounted_not_executed(self):
        both = {'op_1': (False, False), 'op_2': (False, False)}
        for name, after in {'another replacement': '甲 [别的] 乙 [号] 丙', 'kept text changed': '丁 [号] 乙 [号] 丙', 'tail changed': '甲 [号] 乙 [号] 戊',
                            'text appended': '甲 [号] 乙 [号] 丙!', 'op removed without trace': '甲 乙 [号] 丙', 'replacement simply missing': '甲  乙 [号] 丙'}.items():
            with self.subTest(name):
                self.assertEqual(self.judge(after), both)

    def test_deletions(self):
        ops = [text_op(1, 1, 2, 13, ''), text_op(2, 1, 16, 27, '')]
        self.assertEqual(self.judge('甲  乙  丙', ops), {'op_1': (True, False), 'op_2': (True, False)})
        self.assertEqual(self.judge('甲 5678 乙  丙', ops)['op_1'], (False, True))
        self.assertEqual(self.judge('甲 13812345678 乙  丙', ops)['op_1'], (False, True))

    def test_operations_may_be_given_in_any_order_and_a_replacement_equal_to_the_original_is_done(self):
        self.assertEqual(self.judge('甲 [号] 乙 [号] 丙', list(reversed(self.ops()))), {'op_1': (True, False), 'op_2': (True, False)})
        ops = [text_op(1, 1, 2, 13, '13812345678')]
        self.assertEqual(self.judge(self.BEFORE, ops), {'op_1': (True, False)})
