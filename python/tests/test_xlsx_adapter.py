import io
import unittest
import zipfile

from tacet_format_xlsx.adapter import open_adapter
from tacet_plugin_sdk import unitkit
from tacet_plugin_sdk.plan import Op
from tacet_plugin_sdk.schema import Schemas
from test_docx_extract import FakeTask
from test_xlsx_value_subset import book

NUM = '13812345678'
S1, S2 = 'xl/worksheets/sheet1.xml', 'xl/worksheets/sheet2.xml'
CORE = ('docProps/core.xml', 'application/vnd.openxmlformats-package.core-properties+xml')
CORE_REL = '<Relationship Id="rIdC" Type="http://schemas.openxmlformats.org/package/2006/relationships/metadata/core-properties" Target="docProps/core.xml"/>'


def schemas():
    s = Schemas()
    for d in ('ExtractItem', 'WriteItem', 'ValidateItem'):
        s.compile('format.schema.json', d)
    return s


def cell(ref, t=None, v=None, inline=None):
    t = 'inlineStr' if inline is not None else t
    attr = f' t="{t}"' if t else ''
    body = f'<is><t>{inline}</t></is>' if inline is not None else (f'<v>{v}</v>' if v is not None else '')
    return f'<c r="{ref}"{attr}>{body}</c>'


def wbook(props=False, strings=None, sheet2=False):
    rows = '<row r="1">' + cell('A1', 's', 0) + cell('B1', 's', 1) + cell('C1', inline=f'inline {NUM}') + cell('D1', v=NUM) + cell('E1', v=42) + '</row>' \
           + '<row r="2">' + cell('A2', 's', 0) + cell('B2') + '</row>'
    sheets = [('One', 'visible', rows)]
    if sheet2:
        sheets.append(('Two', 'hidden', '<row r="1">' + cell('A1', 's', 0) + '</row>'))
    extra = {CORE[0]: (b'<cp:coreProperties xmlns:cp="http://schemas.openxmlformats.org/package/2006/metadata/core-properties"/>', CORE[1])} if props else None
    return book(sheets, strings if strings is not None else [f'<t>shared {NUM}</t>', '<t>plain</t>'], extra=extra, root_rels=CORE_REL if props else '')


def units(raw):
    a = open_adapter(io.BytesIO(raw))
    return [(u.part, u.ordinal, u.text) for u in a.units(io.BytesIO(raw))]


def op(i, part, ordinal, start, end, repl='[号]'):
    return Op(f'op_{i}', 'text', part, ordinal, start, end, repl)


def write(raw, ops):
    a, sink, task = open_adapter(io.BytesIO(raw)), io.BytesIO(), FakeTask()
    unitkit.write(a, io.BytesIO(raw), ops, sink, task, schemas())
    return sink.getvalue(), task.items


def res(items):
    return {i['op']: (i['status'], i.get('reason')) for i in items if i['kind'] == 'op_result'}


def fates(items):
    return {i['part']: i['fate'] for i in items if i['kind'] == 'part_fate'}


def check(raw, cand, ops):
    task = FakeTask()
    unitkit.validate(open_adapter(io.BytesIO(raw)), io.BytesIO(raw), open_adapter, io.BytesIO(cand), ops, task, schemas())
    return {i['op']: (i['executed'], i['residual']) for i in task.items if i['kind'] == 'op_check'}, task.items


class UnitsTest(unittest.TestCase):
    def test_text_cells_are_units_numbered_by_cell_and_long_numbers_are_read_only_views(self):
        self.assertEqual(units(wbook()), [(S1, 1, f'shared {NUM}'), (S1, 2, 'plain'), (S1, 3, f'inline {NUM}'), (f'{S1}#numbers', 4, NUM), (S1, 6, f'shared {NUM}'),
                                          ])
        # B2 is an empty styled cell: it counts as a cell but holds no text

    def test_every_sheet_is_read_whatever_its_visibility(self):
        us = units(wbook(sheet2=True))
        self.assertIn((S2, 1, f'shared {NUM}'), us)

    def test_parts_are_the_real_package_parts_and_properties_are_not_inspected(self):
        raw = wbook(props=True)
        a = open_adapter(io.BytesIO(raw))
        parts = {p[0]: p[1:] for p in a.enumerate(io.BytesIO(raw))}
        self.assertEqual(parts[S1][1:], ('text', 'inspected'))
        self.assertEqual(parts['xl/sharedStrings.xml'][1:], ('text', 'inspected'))
        self.assertEqual(parts['xl/styles.xml'][1:], ('other', 'inspected'))
        self.assertEqual(parts['docProps/core.xml'][1:], ('unknown', 'not_inspected'))

    def test_what_the_value_subset_cannot_carry_is_not_claimed(self):
        formula = book([('One', 'visible', '<row r="1"><c r="A1"><f>1+1</f><v>2</v></c></row>')], [])
        comments = book([('One', 'visible', '<row r="1">' + cell('A1', inline='x') + '</row>')], [], extra={'xl/comments1.xml': (b'<comments/>', 'application/vnd.openxmlformats-officedocument.spreadsheetml.comments+xml')})
        external = book([('One', 'visible', '<row r="1">' + cell('A1', inline='x') + '</row>')], [],
                        rels_extra='<Relationship Id="rX" Type="http://x/hyperlink" Target="http://example.com" TargetMode="External"/>')
        for name, raw in (('formula', formula), ('comments', comments), ('external link', external), ('not a zip', b'PK\x03\x04junk'), ('plain text', b'hello'), ('empty', b'')):
            with self.subTest(name):
                self.assertIsNone(open_adapter(io.BytesIO(raw)))

    def test_probe(self):
        self.assertEqual(unitkit.probe(io.BytesIO(wbook()), open_adapter)['format'], 'xlsx')
        self.assertEqual(unitkit.probe(io.BytesIO(b'zzz'), open_adapter)['format'], 'unknown')


class WriteTest(unittest.TestCase):
    def test_editing_a_shared_string_cell_changes_that_cell_only(self):
        raw = wbook()
        out, items = write(raw, [op(1, S1, 1, 7, 18)])  # A1 only; A2 uses the same shared string
        self.assertEqual(res(items), {'op_1': ('executed', None)})
        got = units(out)
        self.assertIn((S1, 1, 'shared [号]'), got)
        self.assertIn((S1, 6, f'shared {NUM}'), got)  # the other cell that shares the string is untouched
        self.assertEqual(fates(items)['xl/sharedStrings.xml'], 'redacted')
        self.assertEqual(fates(items)[S1], 'redacted')

    def test_when_every_cell_of_a_shared_string_is_edited_the_orphaned_entry_is_emptied(self):
        raw = wbook()
        out, _ = write(raw, [op(1, S1, 1, 7, 18), op(2, S1, 6, 7, 18)])
        with zipfile.ZipFile(io.BytesIO(out)) as z:
            self.assertNotIn(NUM, z.read('xl/sharedStrings.xml').decode())
            for name in z.namelist():
                if name.endswith('.xml'):
                    self.assertNotIn(f'shared {NUM}', z.read(name).decode())

    def test_inline_cells_and_other_sheets(self):
        out, items = write(wbook(sheet2=True), [op(1, S1, 3, 7, 18, ''), op(2, S2, 1, 7, 18)])
        self.assertEqual({k: v[0] for k, v in res(items).items()}, {'op_1': 'executed', 'op_2': 'executed'})
        got = units(out)
        self.assertIn((S1, 3, 'inline '), got)
        self.assertIn((S2, 1, 'shared [号]'), got)

    def test_a_cell_emptied_by_the_edit_is_still_a_unit_so_validation_can_find_it(self):
        raw = book([('One', 'visible', '<row r="1">' + cell('A1', inline=NUM) + '</row>')], [])
        ops = [op(1, S1, 1, 0, 11, '')]
        out, items = write(raw, ops)
        self.assertEqual(res(items), {'op_1': ('executed', None)})
        self.assertEqual(check(raw, out, ops)[0], {'op_1': (True, False)})

    def test_numbers_cannot_be_turned_into_text_without_authorisation(self):
        raw = wbook()
        out, items = write(raw, [op(1, f'{S1}#numbers', 4, 0, 11)])
        self.assertEqual(res(items), {'op_1': ('not_executed', 'PART_OPERATION_UNSUPPORTED')})
        self.assertEqual(units(out), units(raw))

    def test_properties_are_removed_all_together_with_their_entries(self):
        raw = wbook(props=True)
        out, items = write(raw, [Op('op_p', 'part', 'docProps/core.xml')])
        self.assertEqual(res(items), {'op_p': ('executed', None)})
        self.assertEqual(fates(items)['docProps/core.xml'], 'removed')
        with zipfile.ZipFile(io.BytesIO(out)) as z:
            self.assertNotIn('docProps/core.xml', z.namelist())
            self.assertNotIn('core.xml', z.read('[Content_Types].xml').decode())
            self.assertNotIn('core.xml', z.read('_rels/.rels').decode())
        self.assertIsNotNone(open_adapter(io.BytesIO(out)))

    def test_properties_that_are_kept_stop_the_export_with_the_reason(self):
        raw = wbook(props=True)
        out, items = write(raw, [op(1, S1, 1, 7, 18)])
        self.assertEqual(res(items), {'op_1': ('not_executed', 'XLSX_UNINSPECTED_PART_POLICY_REQUIRED')})
        self.assertEqual({v for v in fates(items).values()}, {'kept'})

    def test_other_parts_cannot_be_removed(self):
        _, items = write(wbook(), [Op('op_1', 'part', 'xl/workbook.xml'), Op('op_2', 'part', 'nope.xml')])
        self.assertEqual(res(items), {'op_1': ('not_executed', 'PART_OPERATION_UNSUPPORTED'), 'op_2': ('not_executed', 'PART_UNKNOWN')})

    def test_bad_operations_say_why(self):
        _, items = write(wbook(), [op(1, S1, 99, 0, 1), op(2, S1, 1, 0, 999), op(3, 'xl/styles.xml', 1, 0, 1)])
        self.assertEqual(res(items), {'op_1': ('not_executed', 'UNIT_NOT_FOUND'), 'op_2': ('not_executed', 'RANGE_INVALID'), 'op_3': ('not_executed', 'PART_OPERATION_UNSUPPORTED')})

    def test_a_part_list_matches_between_the_input_and_the_candidate_for_reconciliation(self):
        raw = wbook(props=True)
        out, items = write(raw, [Op('op_p', 'part', 'docProps/core.xml'), op(1, S1, 1, 7, 18)])
        _, v = check(raw, out, [Op('op_p', 'part', 'docProps/core.xml'), op(1, S1, 1, 7, 18)])
        ins = {i['part']: i['content_type'] for i in v if i['kind'] == 'input_part'}
        outs = {i['part']: i['content_type'] for i in v if i['kind'] == 'output_part'}
        self.assertEqual(set(ins) - set(outs), {'docProps/core.xml'})
        self.assertEqual({k: ins[k] for k in outs}, outs)

    def test_removing_only_some_of_the_property_parts_fails_the_write(self):
        app = ('docProps/app.xml', 'application/vnd.openxmlformats-officedocument.extended-properties+xml')
        app_rel = '<Relationship Id="rIdA" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/extended-properties" Target="docProps/app.xml"/>'
        rows = '<row r="1">' + cell('A1', inline='x') + '</row>'
        raw = book([('One', 'visible', rows)], [], root_rels=CORE_REL + app_rel, extra={
            CORE[0]: (b'<cp:coreProperties xmlns:cp="http://schemas.openxmlformats.org/package/2006/metadata/core-properties"/>', CORE[1]),
            app[0]: (b'<Properties xmlns="http://schemas.openxmlformats.org/officeDocument/2006/extended-properties"/>', app[1])})
        _, items = write(raw, [Op('op_p', 'part', 'docProps/core.xml')])
        self.assertEqual(res(items), {'op_p': ('not_executed', 'PROPERTY_PARTS_ALL_OR_NONE')})
        out, items = write(raw, [Op('op_p', 'part', 'docProps/core.xml'), Op('op_q', 'part', 'docProps/app.xml')])
        self.assertEqual(res(items), {'op_p': ('executed', None), 'op_q': ('executed', None)})

    def test_the_text_held_in_memory_for_shared_strings_is_bounded(self):
        import tacet_format_xlsx.adapter as module
        old = module.MAX_STRING_CHARS
        module.MAX_STRING_CHARS = 10
        try:
            self.assertIsNone(open_adapter(io.BytesIO(wbook())))
        finally:
            module.MAX_STRING_CHARS = old
        self.assertIsNotNone(open_adapter(io.BytesIO(wbook())))

    def test_a_cell_pointing_past_the_shared_strings_is_not_claimed(self):
        raw = book([('One', 'visible', '<row r="1">' + cell('A1', 's', 5) + '</row>')], ['<t>only</t>'])
        self.assertIsNone(open_adapter(io.BytesIO(raw)))


class ValidateTest(unittest.TestCase):
    def test_correct_candidate_missing_edit_and_a_part_that_is_still_there(self):
        raw = wbook(props=True)
        ops = [op(1, S1, 1, 7, 18), Op('op_p', 'part', 'docProps/core.xml')]
        out, _ = write(raw, ops)
        self.assertEqual(check(raw, out, ops)[0], {'op_1': (True, False), 'op_p': (True, False)})
        self.assertEqual(check(raw, raw, ops)[0], {'op_1': (False, True), 'op_p': (False, True)})

    def test_a_part_that_exists_only_in_the_candidate_has_no_source(self):
        raw = wbook()
        out = io.BytesIO()
        with zipfile.ZipFile(io.BytesIO(raw)) as z, zipfile.ZipFile(out, 'w') as w:
            for n in z.namelist():
                w.writestr(n, z.read(n))
        # a candidate with an extra, unregistered part is not a workbook this adapter reopens
        with zipfile.ZipFile(out, 'a') as w:
            w.writestr('xl/extra.bin', b'x')
        _, items = check(raw, out.getvalue(), [op(1, S1, 1, 7, 18)])
        self.assertFalse(items[-1]['reopened'])

    def test_a_candidate_that_is_not_a_workbook_is_not_reopened(self):
        _, items = check(wbook(), b'PK\x03\x04junk', [op(1, S1, 1, 7, 18)])
        self.assertFalse(items[-1]['reopened'])


class ExtractTest(unittest.TestCase):
    def test_units_part_states_and_the_gap_for_properties(self):
        raw = wbook(props=True)
        task = FakeTask()
        unitkit.extract(open_adapter(io.BytesIO(raw)), io.BytesIO(raw), task, schemas())
        us = [i for i in task.items if i['kind'] == 'text_unit']
        self.assertEqual([(u['part'], u['ordinal']) for u in us], [(S1, 1), (S1, 2), (S1, 3), (f'{S1}#numbers', 4), (S1, 6)])
        states = {i['part']: (i['class'], i['state'], i.get('reason')) for i in task.items if i['kind'] == 'part_state'}
        self.assertEqual(states['docProps/core.xml'], ('unknown', 'not_inspected', 'PART_NOT_INSPECTED'))
        self.assertEqual(states[S1], ('text', 'inspected', None))
        self.assertEqual(task.items[-1]['parts'], len(states))


if __name__ == '__main__':
    unittest.main()
