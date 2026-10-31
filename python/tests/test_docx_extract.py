import io
import unittest
import zipfile

from tacet_plugin_sdk.runtime import Cancelled
from tacet_plugin_sdk.schema import Schemas
from tacet_format_docx.extract import extract
from test_docx_package import CT, MAIN_TYPE, REL, W, fixture


class FakeTask:
    def __init__(self, cancel_after=None):
        self.items = []
        self.cancel_after = cancel_after

    def check(self):
        if self.cancel_after is not None and len(self.items) >= self.cancel_after:
            raise Cancelled()

    def emit(self, item):
        self.items.append(item)


def schemas():
    s = Schemas()
    s.compile('format.schema.json', 'ExtractItem')
    return s


def run(raw, task=None):
    task = task or FakeTask()
    extract(io.BytesIO(raw), task, schemas())
    return task.items


def doc(body):
    return (f'<w:document xmlns:w="{W}"><w:body>{body}</w:body></w:document>').encode()


def package(body, extra=()):
    out = io.BytesIO()
    with zipfile.ZipFile(out, 'w') as z:
        z.writestr('[Content_Types].xml', f'<Types xmlns="{CT}"><Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/><Override PartName="/word/document.xml" ContentType="{MAIN_TYPE}"/></Types>')
        z.writestr('_rels/.rels', f'<Relationships xmlns="{REL}"><Relationship Id="m" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/></Relationships>')
        z.writestr('word/document.xml', doc(body))
        for name, data in extra:
            z.writestr(name, data)
    return out.getvalue()


class ExtractTest(unittest.TestCase):
    def test_units_part_states_and_a_completion_marker_with_counts(self):
        items = run(fixture())
        units = [i for i in items if i['kind'] == 'text_unit']
        self.assertEqual(len(units), 1)
        self.assertEqual((units[0]['text'], units[0]['part'], units[0]['ordinal'], units[0]['visibility']),
                         ('联系人张三结束', 'word/document.xml', 1, 'visible'))
        self.assertEqual(units[0]['source_map'], {'kind': 'paragraph_ordinal', 'part': 'word/document.xml', 'ordinal': 1})
        states = {i['part']: i['state'] for i in items if i['kind'] == 'part_state'}
        self.assertEqual(set(states), {'[Content_Types].xml', '_rels/.rels', 'word/document.xml'})
        self.assertEqual(set(states.values()), {'inspected'})
        classes = {i['part']: i['class'] for i in items if i['kind'] == 'part_state'}
        self.assertEqual(classes, {'[Content_Types].xml': 'other', '_rels/.rels': 'other', 'word/document.xml': 'text'})
        self.assertEqual(items[-1], {'kind': 'extract_complete', 'complete': True, 'units': 1, 'parts': 3})

    def test_every_item_is_valid_and_unit_ids_are_unique_and_ordered(self):
        body = ''.join(f'<w:p><w:r><w:t>段落{i}</w:t></w:r></w:p>' for i in range(50))
        items = run(package(body))
        ids = [i['unit_id'] for i in items if i['kind'] == 'text_unit']
        self.assertEqual(ids, [f'u{n}' for n in range(1, 51)])
        self.assertEqual([i['ordinal'] for i in items if i['kind'] == 'text_unit'], list(range(1, 51)))

    def test_empty_paragraphs_keep_their_ordinals(self):
        items = run(package('<w:p/><w:p><w:r><w:t>x</w:t></w:r></w:p><w:p/><w:p><w:r><w:t>y</w:t></w:r></w:p>'))
        self.assertEqual([(i['text'], i['ordinal']) for i in items if i['kind'] == 'text_unit'], [('x', 2), ('y', 4)])

    def test_hidden_and_revised_text_are_labelled_not_mixed_in(self):
        items = run(package('<w:p><w:r><w:t>可见</w:t></w:r></w:p>'
                            '<w:p><w:r><w:rPr><w:vanish/></w:rPr><w:t>隐藏</w:t></w:r></w:p>'
                            '<w:p><w:ins><w:r><w:t>修订</w:t></w:r></w:ins></w:p>'))
        self.assertEqual([(i['text'], i['visibility']) for i in items if i['kind'] == 'text_unit'],
                         [('可见', 'visible'), ('隐藏', 'hidden'), ('修订', 'revision')])

    def test_table_cells_are_extracted(self):
        items = run(package('<w:tbl><w:tr><w:tc><w:p><w:r><w:t>姓名</w:t></w:r></w:p></w:tc><w:tc><w:p><w:r><w:t>张三</w:t></w:r></w:p></w:tc></w:tr></w:tbl>'))
        self.assertEqual([i['text'] for i in items if i['kind'] == 'text_unit'], ['姓名', '张三'])

    def test_an_uninspected_part_is_reported_not_omitted(self):
        items = run(package('<w:p><w:r><w:t>x</w:t></w:r></w:p>', [('custom/unknown.bin', b'secret')]))
        state = [i for i in items if i['kind'] == 'part_state' and i['part'] == 'custom/unknown.bin']
        self.assertEqual([(s['state'], s['class'], s['reason']) for s in state], [('not_inspected', 'unknown', 'PART_NOT_INSPECTED')])

    def test_an_unreadable_part_is_a_gap_and_the_file_still_completes(self):
        # a nested paragraph (text box inside a paragraph) is outside the subset
        items = run(package('<w:p><w:r><w:t>前</w:t></w:r><w:p><w:r><w:t>内</w:t></w:r></w:p></w:p>'))
        failed = [i for i in items if i['kind'] == 'part_state' and i['state'] == 'failed']
        self.assertEqual([(f['part'], f['reason']) for f in failed], [('word/document.xml', 'PART_UNREADABLE')])
        self.assertEqual(items[-1]['kind'], 'extract_complete')
        self.assertFalse([i for i in items if i['kind'] == 'part_state' and i['part'] == 'word/document.xml' and i['state'] == 'inspected'])

    def test_cancellation_stops_the_stream_without_a_completion_marker(self):
        body = ''.join(f'<w:p><w:r><w:t>段落{i}</w:t></w:r></w:p>' for i in range(20))
        task = FakeTask(cancel_after=5)
        with self.assertRaises(Cancelled):
            run(package(body), task)
        self.assertNotIn('extract_complete', [i['kind'] for i in task.items])

    def test_garbage_is_rejected_not_reported_empty(self):
        with self.assertRaises(Exception):
            run(b'not a zip')

    def test_text_never_appears_in_part_states(self):
        for i in run(package('<w:p><w:r><w:t>TEST_SECRET</w:t></w:r></w:p>')):
            if i['kind'] != 'text_unit':
                self.assertNotIn('TEST_SECRET', str(i))


if __name__ == '__main__':
    unittest.main()
