import io
import os
import subprocess
import sys
import tempfile
import unittest
import zipfile

from test_docx_extract import FakeTask, run
from test_docx_write import schemas
from tacet_format_docx.write import write
from tacet_plugin_sdk.plan import Op

HERE = os.path.dirname(os.path.abspath(__file__))
SECRET = '客户 王小明 13812345678'


def with_custom_xml():
    """The Word-like fixture with a customXml item bound from the main document, as Word saves one."""
    with tempfile.TemporaryDirectory() as d:
        out = os.path.join(d, 'w.docx')
        subprocess.run([sys.executable, '-I', os.path.join(HERE, 'docx_wordlike.py'), out], check=True)
        src = open(out, 'rb').read()
    buf = io.BytesIO()
    with zipfile.ZipFile(io.BytesIO(src)) as zin, zipfile.ZipFile(buf, 'w') as zout:
        for info in zin.infolist():
            data = zin.read(info.filename)
            if info.filename == 'word/_rels/document.xml.rels':
                data = data.replace(b'</Relationships>', b'<Relationship Id="cx" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/customXml" Target="../customXml/item1.xml"/></Relationships>')
            zout.writestr(info.filename, data)
        zout.writestr('customXml/item1.xml', f'<data><client>{SECRET}</client></data>')
    return buf.getvalue()


class CustomXmlTest(unittest.TestCase):
    def test_custom_xml_is_a_gap_and_its_removal_takes_its_reference_along(self):
        raw = with_custom_xml()
        states = {i['part']: i['state'] for i in run(raw) if i['kind'] == 'part_state'}
        self.assertEqual(states.get('customXml/item1.xml'), 'not_inspected')
        task, sink = FakeTask(), io.BytesIO()
        write(io.BytesIO(raw), [Op('op_c', 'part', 'customXml/item1.xml')], sink, task, schemas())
        results = {i['op']: i['status'] for i in task.items if i['kind'] == 'op_result'}
        self.assertEqual(results, {'op_c': 'executed'})
        with zipfile.ZipFile(io.BytesIO(sink.getvalue())) as z:
            names = z.namelist()
            self.assertNotIn('customXml/item1.xml', names)
            self.assertNotIn(b'customXml', z.read('word/_rels/document.xml.rels'))
            self.assertIn(b'styles.xml', z.read('word/_rels/document.xml.rels'))
            for n in names:
                self.assertNotIn(SECRET.encode(), z.read(n), n)


if __name__ == '__main__':
    unittest.main()
