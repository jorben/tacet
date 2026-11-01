import io
import os
import subprocess
import sys
import tempfile
import unittest
import zipfile

from test_docx_extract import FakeTask, run
from tacet_format_docx.structure import inspect
from tacet_format_docx.validate import validate

HERE = os.path.dirname(os.path.abspath(__file__))


def wordlike(styles='', settings=''):
    with tempfile.TemporaryDirectory() as d:
        out = os.path.join(d, 'w.docx')
        subprocess.run([sys.executable, '-I', os.path.join(HERE, 'docx_wordlike.py'), out, styles, settings], check=True)
        return open(out, 'rb').read()


def states(raw):
    return {i['part']: (i['state'], i.get('reason')) for i in run(raw) if i['kind'] == 'part_state'}


STRUCTURE = ['word/styles.xml', 'word/settings.xml', 'word/fontTable.xml', 'word/webSettings.xml', 'word/theme/theme1.xml', 'word/numbering.xml']


class StructurePartsTest(unittest.TestCase):
    def test_the_structure_parts_of_a_word_document_are_inspected(self):
        got = states(wordlike())
        for name in STRUCTURE:
            self.assertEqual(got[name], ('inspected', None), name)

    def test_strings_of_the_author_keep_a_part_a_gap(self):
        cases = {
            'custom style name': ('<w:style w:type="paragraph" w:customStyle="1" w:styleId="x1"><w:name w:val="张三专用"/></w:style>', '', 'styles', 'DOCX_CUSTOM_STYLE_NAME'),
            'aliases': ('<w:style w:type="paragraph" w:styleId="x2"><w:name w:val="Quote"/><w:aliases w:val="机密"/></w:style>', '', 'styles', 'DOCX_STRUCTURE_PART_CONTENT'),
            'free string attribute': ('<w:style w:type="paragraph" w:styleId="x3"><w:name w:val="联系人 13812345678"/></w:style>', '', 'styles', 'DOCX_STRUCTURE_PART_STRING'),
            'document variable': ('', '<w:docVars><w:docVar w:name="client" w:val="13812345678"/></w:docVars>', 'settings', 'DOCX_STRUCTURE_PART_CONTENT'),
            'mail merge': ('', '<w:mailMerge><w:mainDocumentType w:val="formLetters"/></w:mailMerge>', 'settings', 'DOCX_STRUCTURE_PART_CONTENT'),
        }
        for name, (styles, settings, part, reason) in cases.items():
            with self.subTest(name):
                self.assertEqual(states(wordlike(styles, settings))['word/' + part + '.xml'], ('not_inspected', reason))

    def test_list_number_texts_pass_and_words_in_them_do_not(self):
        W = 'http://schemas.openxmlformats.org/wordprocessingml/2006/main'
        def numbering(level='%1.', name=''):
            return (f'<w:numbering xmlns:w="{W}"><w:abstractNum w:abstractNumId="0">{name}<w:lvl w:ilvl="0">'
                    f'<w:lvlText w:val="{level}"/></w:lvl></w:abstractNum></w:numbering>').encode()
        for ok in ('%1.', '第%1章', '（%1）', '%1.%2.%3', '\uf06c', '•', '■', '[%1]', '第%1条、'):
            with self.subTest(ok=ok):
                self.assertIsNone(inspect(io.BytesIO(numbering(ok))))
        for bad in ('张三%1', '%1 secret', 'x' * 40, '机密', '%1' * 17):  # the last: allowed signs, too long
            with self.subTest(bad=bad):
                self.assertEqual(inspect(io.BytesIO(numbering(bad))), 'DOCX_STRUCTURE_PART_STRING')
        self.assertEqual(inspect(io.BytesIO(numbering(name='<w:name w:val="List1"/>'))), 'DOCX_CUSTOM_STYLE_NAME')

    def test_braced_guids_pass_and_other_braced_strings_do_not(self):
        W = 'http://schemas.openxmlformats.org/wordprocessingml/2006/main'
        doc = lambda v: f'<w:settings xmlns:w="{W}"><w:docId w:val="{v}"/></w:settings>'.encode()  # noqa: E731
        self.assertIsNone(inspect(io.BytesIO(doc('{8F3A2C1D-0B4E-4F6A-9C2D-1E2F3A4B5C6D}'))))
        for bad in ('{王小明}', '{13812345678}', '{8F3A2C1D-0B4E-4F6A-9C2D-1E2F3A4B5C6D} x', '{8F3A2C1D-0B4E-4F6A-9C2D-1E2F3A4B5C6G}'):
            with self.subTest(bad=bad):
                self.assertEqual(inspect(io.BytesIO(doc(bad))), 'DOCX_STRUCTURE_PART_STRING')

    def test_text_comments_and_dtds_are_refused_by_the_inspector(self):
        W = 'http://schemas.openxmlformats.org/wordprocessingml/2006/main'
        for body, reason in ((f'<w:styles xmlns:w="{W}">secret</w:styles>', 'DOCX_STRUCTURE_PART_TEXT'),
                             (f'<w:styles xmlns:w="{W}"><w:style/>tail</w:styles>', 'DOCX_STRUCTURE_PART_TEXT'),
                             (f'<w:styles xmlns:w="{W}"><!-- 13812345678 --></w:styles>', 'DOCX_STRUCTURE_PART_STRING'),
                             (f'<!DOCTYPE x [<!ENTITY e "x">]><w:styles xmlns:w="{W}"/>', 'XML_DTD_FORBIDDEN'),
                             ('<w:styles', 'XML_INVALID')):
            with self.subTest(body=body):
                self.assertEqual(inspect(io.BytesIO(body.encode())), reason)
        self.assertIsNone(inspect(io.BytesIO(f'<w:styles xmlns:w="{W}"><w:style w:styleId="a"/></w:styles>'.encode())))

    def test_validation_inspects_the_candidates_structure_parts_itself(self):
        from test_docx_write import schemas
        raw = wordlike()
        tampered = io.BytesIO()
        with zipfile.ZipFile(io.BytesIO(raw)) as zin, zipfile.ZipFile(tampered, 'w') as zout:
            for info in zin.infolist():
                data = zin.read(info.filename)
                if info.filename == 'word/settings.xml':
                    data = data.replace(b'</w:settings>', b'<w:docVars><w:docVar w:name="a" w:val="b"/></w:docVars></w:settings>')
                zout.writestr(info.filename, data)
        for cand, want in ((raw, []), (tampered.getvalue(), ['word/settings.xml'])):
            task = FakeTask()
            validate(io.BytesIO(raw), [], io.BytesIO(cand), task, schemas())
            # docProps (author, company) is not a structure part: it stays unverified either way
            self.assertEqual([i['scope'] for i in task.items if i['kind'] == 'unverified' and i['scope'] in STRUCTURE], want)


if __name__ == '__main__':
    unittest.main()
