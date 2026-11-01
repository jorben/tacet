import io
import unittest

from lxml import etree
from tacet_format_docx.xmlparts import Edit, XMLLimits, XMLPartError, W, rewrite, scan


PREFIX = '<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body>'
SUFFIX = '</w:body></w:document>'


def document(paragraphs):
    return (PREFIX + paragraphs + SUFFIX).encode()


class DOCXXMLTest(unittest.TestCase):
    def test_cross_run_keeps_unmodified_text_and_format(self):
        source = document('<w:p><w:r><w:rPr><w:b/></w:rPr><w:t>前缀张</w:t></w:r>'
                          '<w:r><w:rPr><w:i/></w:rPr><w:t>三后缀</w:t></w:r></w:p>')
        output = io.BytesIO()
        count = rewrite(io.BytesIO(source), output, lambda p: [Edit(2, 4, '[人名]')])
        root = etree.fromstring(output.getvalue())
        texts = [n.text or '' for n in root.iter(W + 't')]
        self.assertEqual(texts, ['前缀[人名]', '后缀'])
        self.assertEqual(len(list(root.iter(W + 'b'))), 1)
        self.assertEqual(len(list(root.iter(W + 'i'))), 1)
        self.assertEqual(count, 1)

    def test_unicode_offsets_are_code_points_and_edits_bind_original(self):
        source = document('<w:p><w:r><w:t>A😀张</w:t></w:r><w:r><w:t>三与李四Z</w:t></w:r></w:p>')
        result = []
        scan(io.BytesIO(source), result.append)
        self.assertEqual(result[0].text, 'A😀张三与李四Z')
        self.assertEqual([(s.start, s.end) for s in result[0].segments], [(0, 3), (3, 8)])
        output = io.BytesIO()
        rewrite(io.BytesIO(source), output, lambda p: [Edit(2, 4, '甲'), Edit(5, 7, '很长的占位符')])
        actual = []
        scan(io.BytesIO(output.getvalue()), actual.append)
        self.assertEqual(actual[0].text, 'A😀甲与很长的占位符Z')

    def test_many_paragraphs_stream_in_order(self):
        source = document(''.join(f'<w:p><w:r><w:t>第{i}段</w:t></w:r></w:p>' for i in range(1500)))
        expected = 0
        def consume(p):
            nonlocal expected
            expected += 1
            self.assertEqual(p.ordinal, expected)
            self.assertEqual(p.text, f'第{expected-1}段')
        self.assertEqual(scan(io.BytesIO(source), consume), 1500)

    def test_rejects_dtd_even_without_entity_expansion(self):
        source = b'<!DOCTYPE x [<!ENTITY a SYSTEM "file:///etc/passwd">]>' + document('<w:p><w:r><w:t>&a;</w:t></w:r></w:p>')
        with self.assertRaisesRegex(XMLPartError, 'XML_DTD_FORBIDDEN'):
            scan(io.BytesIO(source), lambda p: None)

    def test_xml_depth_node_character_and_size_limits(self):
        source = document('<w:p><w:r><w:t>abcdef</w:t></w:r></w:p>')
        for limits, code in [(XMLLimits(depth=2), 'XML_STRUCTURE_LIMIT'),
                             (XMLLimits(nodes=2), 'XML_STRUCTURE_LIMIT'),
                             (XMLLimits(paragraph_nodes=2), 'DOCX_PARAGRAPH_LIMIT'),
                             (XMLLimits(paragraph_chars=5), 'DOCX_PARAGRAPH_LIMIT'),
                             (XMLLimits(total_bytes=10), 'XML_SIZE_LIMIT')]:
            with self.subTest(limits=limits), self.assertRaisesRegex(XMLPartError, code):
                scan(io.BytesIO(source), lambda p: None, limits)

    def test_hidden_and_revised_content_cannot_be_silently_rewritten(self):
        for inner in ('<w:r><w:rPr><w:vanish/></w:rPr><w:t>secret</w:t></w:r>',
                      '<w:ins><w:r><w:t>secret</w:t></w:r></w:ins>'):
            with self.subTest(inner=inner), self.assertRaisesRegex(XMLPartError, 'DOCX_NONVISIBLE_POLICY_REQUIRED'):
                rewrite(io.BytesIO(document('<w:p>' + inner + '</w:p>')), io.BytesIO(),
                        lambda p: [Edit(0, 6, 'x')])

    def test_hidden_content_cannot_pass_through_when_no_edits_are_requested(self):
        source = document('<w:p><w:r><w:rPr><w:vanish/></w:rPr><w:t>secret</w:t></w:r></w:p>')
        with self.assertRaisesRegex(XMLPartError, 'DOCX_NONVISIBLE_POLICY_REQUIRED'):
            rewrite(io.BytesIO(source), io.BytesIO(), lambda p: [])

    def test_truncated_xml_is_rejected_by_parser(self):
        with self.assertRaisesRegex(XMLPartError, 'XML_INVALID'):
            scan(io.BytesIO(document('<w:p/>')[:-8]), lambda p: None)

    def test_unsupported_semantic_boundaries_fail_instead_of_guessing(self):
        source = document('<w:p><w:r><w:t>张</w:t><w:tab/><w:t>三</w:t></w:r></w:p>')
        with self.assertRaisesRegex(XMLPartError, 'DOCX_COMPLEX_PARAGRAPH_UNSUPPORTED'):
            rewrite(io.BytesIO(source), io.BytesIO(), lambda p: [Edit(0, 2, 'x')])

    def test_unknown_text_and_attributes_cannot_bypass_mapping(self):
        for content in ('<w:p><w:unknown>secret</w:unknown></w:p>',
                        '<w:p><w:r private="secret"><w:t>public</w:t></w:r></w:p>',
                        '<w:p><w:r><w:t>public</w:t></w:r>secret</w:p>'):
            with self.subTest(content=content), self.assertRaises(XMLPartError):
                rewrite(io.BytesIO(document(content)), io.BytesIO(), lambda p: [])

    def test_overlapping_and_out_of_bounds_edits_fail(self):
        source = document('<w:p><w:r><w:t>abcdef</w:t></w:r></w:p>')
        for edits, code in [([Edit(0, 4, 'x'), Edit(3, 5, 'y')], 'DOCX_OVERLAPPING_EDITS'),
                            ([Edit(1, 7, 'x')], 'DOCX_INVALID_SPAN')]:
            with self.subTest(code=code), self.assertRaisesRegex(XMLPartError, code):
                rewrite(io.BytesIO(source), io.BytesIO(), lambda p: edits)

    def test_unmapped_comments_and_processing_instructions_refused(self):
        for content, code in [('<w:p><!--sensitive--></w:p>', 'DOCX_XML_COMMENT_POLICY_REQUIRED'),
                              ('<?unsafe data?><w:p/>', 'XML_PROCESSING_INSTRUCTION_FORBIDDEN')]:
            with self.subTest(code=code), self.assertRaisesRegex(XMLPartError, code):
                scan(io.BytesIO(document(content)), lambda p: None)


TABLE = ('<w:tbl><w:tblPr><w:tblW w:w="5000" w:type="dxa"/></w:tblPr><w:tblGrid><w:gridCol w:w="2500"/><w:gridCol w:w="2500"/></w:tblGrid>'
         '<w:tr><w:tc><w:tcPr><w:tcW w:w="2500" w:type="dxa"/></w:tcPr><w:p><w:pPr><w:jc w:val="center"/></w:pPr><w:r><w:t>姓名 张三</w:t></w:r></w:p></w:tc>'
         '<w:tc><w:p><w:r><w:t>电话 13800000000</w:t></w:r></w:p></w:tc></w:tr></w:tbl>')
SECTION = '<w:sectPr><w:pgSz w:w="11906" w:h="16838"/><w:pgMar w:top="1440" w:right="1800" w:bottom="1440" w:left="1800" w:header="851" w:footer="992" w:gutter="0"/></w:sectPr>'


class DOCXLayoutTest(unittest.TestCase):
    def test_table_cells_are_rewritten_and_layout_is_preserved(self):
        source = document(TABLE + '<w:p><w:r><w:t>尾段</w:t></w:r></w:p>' + SECTION)
        output = io.BytesIO()

        def decide(p):
            i = p.text.find('张三')
            return [Edit(i, i + 2, '[人名]')] if i >= 0 else []

        self.assertEqual(rewrite(io.BytesIO(source), output, decide), 3)
        root = etree.fromstring(output.getvalue())
        self.assertEqual([n.text for n in root.iter(W + 't')], ['姓名 [人名]', '电话 13800000000', '尾段'])
        for tag, count in (('tbl', 1), ('tr', 1), ('tc', 2), ('gridCol', 2), ('jc', 1), ('sectPr', 1), ('pgMar', 1)):
            self.assertEqual(len(list(root.iter(W + tag))), count, tag)
        self.assertEqual(root.find(f'.//{W}pgMar').get(W + 'left'), '1800')
        self.assertEqual(root.find(f'.//{W}gridCol').get(W + 'w'), '2500')

    def test_layout_attributes_must_be_well_formed(self):
        for bad in ('<w:tbl><w:tblPr><w:tblW w:w="5000x" w:type="dxa"/></w:tblPr></w:tbl>',
                    '<w:tbl><w:tblPr><w:tblW w:w="5" w:type="weird"/></w:tblPr></w:tbl>',
                    '<w:tbl w:secret="x"/>', '<w:sectPr><w:pgSz w:w="1" w:h="2" w:secret="3"/></w:sectPr>',
                    '<w:p><w:pPr><w:jc w:val="secret"/></w:pPr></w:p>'):
            with self.subTest(bad=bad), self.assertRaisesRegex(XMLPartError, 'DOCX_TEXT_SUBSET_UNSUPPORTED'):
                rewrite(io.BytesIO(document(bad)), io.BytesIO(), lambda p: [])

    def test_text_in_layout_elements_and_unknown_layout_children_are_refused(self):
        for bad in ('<w:tbl>secret</w:tbl>', '<w:tbl><w:tblPr><w:tblStyle w:val="x"/></w:tblPr></w:tbl>',
                    '<w:tbl><w:tr><w:tc>secret<w:p/></w:tc></w:tr></w:tbl>', '<w:sectPr><w:headerReference w:id="r1"/></w:sectPr>'):
            with self.subTest(bad=bad), self.assertRaises(XMLPartError):
                rewrite(io.BytesIO(document(bad)), io.BytesIO(), lambda p: [])

    def test_hidden_text_inside_a_table_cell_is_still_refused(self):
        cell = '<w:tbl><w:tr><w:tc><w:p><w:r><w:rPr><w:vanish/></w:rPr><w:t>secret</w:t></w:r></w:p></w:tc></w:tr></w:tbl>'
        with self.assertRaisesRegex(XMLPartError, 'DOCX_NONVISIBLE_POLICY_REQUIRED'):
            rewrite(io.BytesIO(document(cell)), io.BytesIO(), lambda p: [])

    def test_paragraph_inside_paragraph_is_refused_even_in_a_cell(self):
        with self.assertRaisesRegex(XMLPartError, 'DOCX_NESTED_PARAGRAPH_UNSUPPORTED'):
            rewrite(io.BytesIO(document('<w:tbl><w:tr><w:tc><w:p><w:p/></w:p></w:tc></w:tr></w:tbl>')), io.BytesIO(), lambda p: [])


if __name__ == '__main__':
    unittest.main()


class PageMarkersTest(unittest.TestCase):
    """Page markers and paragraph styles (what review locations come from) are kept by the rewriter."""

    def run_rewrite(self, body, decide):
        out = io.BytesIO()
        rewrite(io.BytesIO(document(body)), out, decide)
        return etree.fromstring(out.getvalue())

    def test_markers_and_styles_are_kept_and_the_text_is_edited(self):
        body = ('<w:p><w:pPr><w:pStyle w:val="Heading1"/><w:outlineLvl w:val="0"/></w:pPr><w:r><w:t>第一章</w:t></w:r></w:p>'
                '<w:p><w:pPr><w:pageBreakBefore/></w:pPr><w:r><w:lastRenderedPageBreak/><w:t>电话 13812345678</w:t></w:r></w:p>'
                '<w:p><w:r><w:t>前</w:t><w:br w:type="page"/><w:t>后 张三</w:t></w:r></w:p>'
                '<w:p><w:pPr><w:sectPr><w:type w:val="nextPage"/></w:sectPr></w:pPr><w:r><w:t>末</w:t></w:r></w:p>')

        def decide(p):
            for word in ('13812345678', '张三'):
                i = p.text.find(word)
                if i >= 0:
                    return [Edit(i, i + len(word), '[x]')]
            return []
        root = self.run_rewrite(body, decide)
        self.assertEqual([n.text for n in root.iter(W + 't')], ['第一章', '电话 [x]', '前', '后 [x]', '末'])
        for tag in ('pStyle', 'outlineLvl', 'pageBreakBefore', 'lastRenderedPageBreak', 'br', 'type'):
            self.assertEqual(len(list(root.iter(W + tag))), 1, tag)

    def test_an_edit_in_a_paragraph_with_a_line_break_is_refused_as_complex(self):
        body = '<w:p><w:r><w:t>张</w:t><w:br/><w:t>三</w:t></w:r></w:p>'
        with self.assertRaisesRegex(XMLPartError, 'DOCX_COMPLEX_PARAGRAPH_UNSUPPORTED'):
            self.run_rewrite(body, lambda p: [Edit(0, 2, 'x')])

    def test_an_edit_across_a_page_break_is_refused(self):
        body = '<w:p><w:r><w:t>138123</w:t><w:br w:type="page"/><w:t>45678</w:t></w:r></w:p>'
        with self.assertRaisesRegex(XMLPartError, 'DOCX_EDIT_ACROSS_PAGE_BREAK'):
            self.run_rewrite(body, lambda p: [Edit(0, 11, '[x]')])
        root = self.run_rewrite(body, lambda p: [Edit(0, 6, '[x]')])  # up to the break is fine
        self.assertEqual([n.text for n in root.iter(W + 't')], ['[x]', '45678'])

    def test_other_breaks_and_unknown_style_values_are_still_refused(self):
        for bad, code in (('<w:p><w:r><w:t>a</w:t><w:br/><w:t>b</w:t></w:r></w:p>', 'DOCX_TEXT_SUBSET_UNSUPPORTED'),
                          ('<w:p><w:r><w:t>a</w:t><w:br w:type="textWrapping"/></w:r></w:p>', 'DOCX_TEXT_SUBSET_UNSUPPORTED'),
                          ('<w:p><w:pPr><w:pStyle w:val="机密 标题"/></w:pPr><w:r><w:t>a</w:t></w:r></w:p>', 'DOCX_TEXT_SUBSET_UNSUPPORTED'),
                          ('<w:p><w:pPr><w:outlineLvl w:val="x"/></w:pPr><w:r><w:t>a</w:t></w:r></w:p>', 'DOCX_TEXT_SUBSET_UNSUPPORTED'),
                          ('<w:p><w:pPr><w:pStyle w:val="A" w:secret="b"/></w:pPr><w:r><w:t>a</w:t></w:r></w:p>', 'DOCX_TEXT_SUBSET_UNSUPPORTED'),
                          ('<w:p><w:pPr><w:sectPr><w:type w:val="weird"/></w:sectPr></w:pPr><w:r><w:t>a</w:t></w:r></w:p>', 'DOCX_TEXT_SUBSET_UNSUPPORTED')):
            with self.subTest(bad=bad), self.assertRaisesRegex(XMLPartError, code):
                self.run_rewrite(bad, lambda p: [])


class ListItemsTest(unittest.TestCase):
    def test_a_list_item_is_rewritten_with_its_numbering(self):
        body = '<w:p><w:pPr><w:numPr><w:ilvl w:val="0"/><w:numId w:val="3"/></w:numPr></w:pPr><w:r><w:t>张三</w:t></w:r></w:p>'
        out = io.BytesIO()
        rewrite(io.BytesIO(document(body)), out, lambda p: [Edit(0, 2, '[人名]')])
        root = etree.fromstring(out.getvalue())
        self.assertEqual([n.text for n in root.iter(W + 't')], ['[人名]'])
        self.assertEqual(root.find(f'.//{W}numId').get(W + 'val'), '3')
        with self.assertRaisesRegex(XMLPartError, 'DOCX_TEXT_SUBSET_UNSUPPORTED'):
            rewrite(io.BytesIO(document(body.replace('w:val="3"', 'w:val="x"'))), io.BytesIO(), lambda p: [])


class WordFormattingTest(unittest.TestCase):
    """What a real Word file's body carries besides text (seen in a supplied sample): ids, run formatting,
    table geometry and shading. Kept as it is when the text is rewritten; any value of another shape refused."""
    W14 = 'http://schemas.microsoft.com/office/word/2010/wordml'
    MC = 'http://schemas.openxmlformats.org/markup-compatibility/2006'

    def doc(self, body, root_attrs=''):
        return (f'<w:document xmlns:w="{W[1:-1]}" xmlns:w14="{self.W14}" xmlns:mc="{self.MC}" {root_attrs}>'
                f'<w:body>{body}</w:body></w:document>').encode()

    def body(self, cell_run='<w:rPr><w:rFonts w:hint="eastAsia"/><w:color w:val="FF0000"/><w:sz w:val="21"/><w:szCs w:val="21"/></w:rPr>'):
        return ('<w:tbl><w:tblPr><w:tblW w:w="0" w:type="auto"/><w:tblInd w:w="-108" w:type="dxa"/><w:tblLayout w:type="fixed"/>'
                '<w:tblCellMar><w:top w:w="0" w:type="dxa"/><w:left w:w="108" w:type="dxa"/><w:bottom w:w="0" w:type="dxa"/><w:right w:w="108" w:type="dxa"/></w:tblCellMar>'
                '<w:tblLook w:val="04A0" w:firstRow="1" w:lastRow="0" w:firstColumn="1" w:lastColumn="0" w:noHBand="0" w:noVBand="1"/></w:tblPr>'
                '<w:tr w:rsidR="00A1B2C3" w14:paraId="1A2B3C4D" w14:textId="77777777"><w:tblPrEx/><w:trPr><w:trHeight w:val="567" w:hRule="exact"/></w:trPr>'
                '<w:tc><w:tcPr><w:tcW w:w="4000" w:type="dxa"/><w:shd w:val="clear" w:color="auto" w:fill="FFC000"/><w:vAlign w:val="center"/></w:tcPr>'
                f'<w:p w:rsidR="00A1B2C3" w:rsidRDefault="00A1B2C3" w14:paraId="2B3C4D5E" w14:textId="77777777"><w:r>{cell_run}<w:t>证件 110101199003074519</w:t></w:r></w:p>'
                '</w:tc></w:tr></w:tbl><w:sectPr w:rsidR="00A1B2C3"><w:cols w:space="425"/></w:sectPr>')

    def rewrite_doc(self, data):
        out = io.BytesIO()
        rewrite(io.BytesIO(data), out, lambda p: [Edit(p.text.find('110'), p.text.find('110') + 18, '[证件号]')] if '110' in p.text else [])
        return etree.fromstring(out.getvalue())

    def test_word_formatting_is_kept_and_the_text_is_rewritten(self):
        root = self.rewrite_doc(self.doc(self.body(), 'mc:Ignorable="w14"'))
        self.assertEqual([n.text for n in root.iter(W + 't')], ['证件 [证件号]'])
        for tag in ('shd', 'color', 'sz', 'szCs', 'rFonts', 'vAlign', 'tblCellMar', 'tblInd', 'tblLayout', 'tblLook', 'trHeight', 'cols', 'tblPrEx', 'trPr'):
            self.assertEqual(len(list(root.iter(W + tag))), 1, tag)
        self.assertEqual(root.find(f'.//{W}shd').get(W + 'fill'), 'FFC000')
        self.assertEqual(root.find(f'.//{W}p').get('{%s}paraId' % self.W14), '2B3C4D5E')

    def test_values_of_another_shape_are_refused(self):
        good = self.body()
        cases = {
            'colour as text': good.replace('w:fill="FFC000"', 'w:fill="王小明"'),
            'colour not hex': good.replace('w:val="FF0000"', 'w:val="red"'),
            'size not digits': good.replace('<w:sz w:val="21"/>', '<w:sz w:val="21pt"/>'),
            'unknown hint': good.replace('w:hint="eastAsia"', 'w:hint="secret"'),
            'rsid not hex8': good.replace('<w:tr w:rsidR="00A1B2C3"', '<w:tr w:rsidR="张三"'),
            'paraId too long': good.replace('w14:paraId="1A2B3C4D"', 'w14:paraId="1A2B3C4D5"'),
            'unknown attribute on p': good.replace('<w:p w:rsidR=', '<w:p w:secret="x" w:rsidR='),
            'indent not integer': good.replace('w:w="-108"', 'w:w="-1x"'),
            'shading pattern unknown': good.replace('w:val="clear"', 'w:val="horzStripe"'),
            'look not hex4': good.replace('w:val="04A0"', 'w:val="04A0F"'),
            'row height rule unknown': good.replace('w:hRule="exact"', 'w:hRule="maybe"'),
            'border under the margin tag': good.replace('<w:top w:w="0" w:type="dxa"/>', '<w:top w:val="single" w:sz="4"/>'),
        }
        for name, body in cases.items():
            with self.subTest(name), self.assertRaisesRegex(XMLPartError, 'DOCX_TEXT_SUBSET_UNSUPPORTED'):
                self.rewrite_doc(self.doc(body))
        for name, attrs in {'ignorable with a free string': 'mc:Ignorable="w14 王小明"', 'id on the root': 'w:rsidR="00A1B2C3x"'}.items():
            with self.subTest(name), self.assertRaisesRegex(XMLPartError, 'DOCX_TEXT_SUBSET_UNSUPPORTED'):
                self.rewrite_doc(self.doc(good, attrs))
