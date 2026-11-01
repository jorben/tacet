import unittest

from test_docx_extract import package, run

# requirements 4.3 / acceptance "DOCX 定位": every unit can be located; the page is approximate and only
# given when the document was saved with page markers; the section is the nearest heading; table cells.


def p(text, ppr='', inner=''):
    return f'<w:p>{"<w:pPr>" + ppr + "</w:pPr>" if ppr else ""}{inner}<w:r><w:t>{text}</w:t></w:r></w:p>'


def where(body):
    return [(u['text'], u.get('location', {})) for u in run(package(body)) if u['kind'] == 'text_unit']


BREAK = '<w:r><w:br w:type="page"/></w:r>'
RENDERED = '<w:r><w:lastRenderedPageBreak/></w:r>'


class LocateTest(unittest.TestCase):
    def test_without_page_markers_there_is_no_page_but_the_paragraph_is_still_there(self):
        got = where(p('一') + p('二'))
        self.assertEqual(got, [('一', {}), ('二', {})])

    def test_pages_follow_the_saved_markers_and_a_break_with_its_rendered_marker_counts_once(self):
        body = (p('一') + f'<w:p>{BREAK}</w:p>' + f'<w:p>{RENDERED}<w:r><w:t>二</w:t></w:r></w:p>'
                + p('三', ppr='<w:pageBreakBefore/>') + p('四', ppr='<w:pageBreakBefore w:val="0"/>')
                + f'<w:p><w:r><w:t>五上</w:t></w:r>{RENDERED}<w:r><w:t>五下</w:t></w:r></w:p>' + p('六'))
        pages = [loc.get('page_hint') for _, loc in where(body)]
        self.assertEqual(pages, [1, 2, 3, 3, 3, 4])

    def test_a_break_at_the_end_of_a_paragraph_moves_the_next_one_on(self):
        # an explicit break with no rendered marker after it (a generated document)
        body = f'<w:p><w:r><w:t>一</w:t></w:r>{BREAK}</w:p>' + p('二') + p('三', inner=RENDERED)
        self.assertEqual([loc.get('page_hint') for _, loc in where(body)], [1, 2, 3])

    def test_a_section_break_starts_a_page_unless_it_is_continuous(self):
        body = (p('一', ppr='<w:sectPr><w:type w:val="continuous"/></w:sectPr>') + p('二', ppr='<w:sectPr/>') + p('三'))
        self.assertEqual([loc.get('page_hint') for _, loc in where(body)], [1, 1, 2])

    def test_the_section_is_the_nearest_heading_by_outline_level_or_style(self):
        body = (p('前言') + p('第一章', ppr='<w:outlineLvl w:val="0"/>') + p('甲') + p('1.1', ppr='<w:pStyle w:val="Heading2"/>')
                + p('乙') + p('正文样式', ppr='<w:pStyle w:val="BodyText"/>') + p('第二章', ppr='<w:pStyle w:val="1"/>') + p('丙'))
        got = {t: loc.get('section') for t, loc in where(body)}
        self.assertEqual(got, {'前言': None, '第一章': 2, '甲': 2, '1.1': 4, '乙': 4, '正文样式': 4, '第二章': 7, '丙': 7})

    def test_a_unit_in_a_table_names_its_cell_and_tables_are_numbered(self):
        cell = lambda t: f'<w:tc>{p(t)}</w:tc>'  # noqa: E731
        body = (p('前') + f'<w:tbl><w:tr>{cell("a")}{cell("b")}</w:tr><w:tr>{cell("c")}{cell("d")}</w:tr></w:tbl>'
                + p('中') + f'<w:tbl><w:tr>{cell("e")}</w:tr></w:tbl>' + p('后'))
        got = {t: loc.get('table') for t, loc in where(body)}
        self.assertEqual(got, {'前': None, 'a': {'index': 1, 'row': 1, 'col': 1}, 'b': {'index': 1, 'row': 1, 'col': 2},
                               'c': {'index': 1, 'row': 2, 'col': 1}, 'd': {'index': 1, 'row': 2, 'col': 2}, '中': None,
                               'e': {'index': 2, 'row': 1, 'col': 1}, '后': None})

    def test_a_location_holds_no_text(self):
        body = p('机密标题', ppr='<w:outlineLvl w:val="0"/>') + p('13812345678', inner=RENDERED)
        for text, loc in where(body):
            self.assertNotIn('机密', repr(loc))
            self.assertTrue(all(isinstance(v, (int, dict)) for v in loc.values()))


if __name__ == '__main__':
    unittest.main()
