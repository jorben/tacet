import io
import unittest
import zipfile

from lxml import etree
from tacet_plugin_sdk.opc import PackageError
from tacet_plugin_sdk.safezip import Archive

from tacet_format_xlsx import workbook as wb
from tacet_format_xlsx.cells import Cell, CellChange, Edit, SharedString, describe_cell, describe_string
from tacet_format_xlsx.xmlunits import M, XMLLimits, scan_units

NS = 'http://schemas.openxmlformats.org/spreadsheetml/2006/main'
RNS = 'http://schemas.openxmlformats.org/officeDocument/2006/relationships'
PKG = 'http://schemas.openxmlformats.org/package/2006/relationships'
CT = 'http://schemas.openxmlformats.org/package/2006/content-types'
SECRET = 'TEST_SECRET_MARK'


def book(sheets, strings=None, extra=None, workbook_extra='', sst_count=None, rels_extra='', root_rels=''):
    """sheets: [(name, state, sheetData inner xml)]; strings: [si inner xml]."""
    files, overrides, rels = {}, [], []
    entries = ''
    for i, (name, state, rows) in enumerate(sheets, 1):
        part = f'xl/worksheets/sheet{i}.xml'
        attr = '' if state == 'visible' else f' state="{state}"'
        entries += f'<sheet name="{name}" sheetId="{i}"{attr} r:id="rId{i}"/>'
        rels.append(f'<Relationship Id="rId{i}" Type="{RNS}/worksheet" Target="worksheets/sheet{i}.xml"/>')
        files[part] = (f'<?xml version="1.0" encoding="UTF-8"?><worksheet xmlns="{NS}"><dimension ref="A1"/>'
                       f'<sheetData>{rows}</sheetData><pageMargins left="1" right="1" top="1" bottom="1" header="0" footer="0"/>'
                       f'</worksheet>').encode()
        overrides.append((part, wb.SHEET_TYPE))
    files['xl/workbook.xml'] = (f'<?xml version="1.0" encoding="UTF-8"?><workbook xmlns="{NS}" xmlns:r="{RNS}">'
                                f'<sheets>{entries}</sheets>{workbook_extra}</workbook>').encode()
    overrides.append(('xl/workbook.xml', wb.WORKBOOK_TYPE))
    n = len(sheets)
    if strings is not None:
        count = sst_count if sst_count is not None else len(strings)
        files['xl/sharedStrings.xml'] = (f'<?xml version="1.0" encoding="UTF-8"?><sst xmlns="{NS}" count="{count}" '
                                         f'uniqueCount="{len(strings)}">' +
                                         ''.join(f'<si>{s}</si>' for s in strings) + '</sst>').encode()
        overrides.append(('xl/sharedStrings.xml', wb.STRINGS_TYPE))
        rels.append(f'<Relationship Id="rId{n + 1}" Type="{RNS}/sharedStrings" Target="sharedStrings.xml"/>')
    files['xl/styles.xml'] = f'<styleSheet xmlns="{NS}"><fonts count="1"><font/></fonts></styleSheet>'.encode()
    overrides.append(('xl/styles.xml', 'application/vnd.openxmlformats-officedocument.spreadsheetml.styles+xml'))
    rels.append(f'<Relationship Id="rId{n + 2}" Type="{RNS}/styles" Target="styles.xml"/>')
    files['xl/_rels/workbook.xml.rels'] = f'<Relationships xmlns="{PKG}">{"".join(rels)}{rels_extra}</Relationships>'.encode()
    files['_rels/.rels'] = (f'<Relationships xmlns="{PKG}"><Relationship Id="rId1" Type="{RNS}/officeDocument" '
                            f'Target="xl/workbook.xml"/>{root_rels}</Relationships>').encode()
    for name, (data, ctype) in (extra or {}).items():
        files[name] = data
        if ctype:
            overrides.append((name, ctype))
    files['[Content_Types].xml'] = (f'<Types xmlns="{CT}"><Default Extension="rels" ContentType="application/'
                                    f'vnd.openxmlformats-package.relationships+xml"/>' +
                                    ''.join(f'<Override PartName="/{p}" ContentType="{t}"/>' for p, t in overrides) +
                                    '</Types>').encode()
    out = io.BytesIO()
    with zipfile.ZipFile(out, 'w', zipfile.ZIP_DEFLATED) as z:
        z.writestr('[Content_Types].xml', files.pop('[Content_Types].xml'))
        for name, data in files.items():
            z.writestr(name, data)
    return out.getvalue()


def rewrite(data, cell_policy=lambda s, c: None, string_policy=None, **kwargs):
    out = io.BytesIO()
    report = wb.rewrite_value_subset(io.BytesIO(data), out, cell_policy, string_policy, **kwargs)
    return out.getvalue(), report


def cells_of(data):
    """{(sheet name, ref): Cell} of a workbook, read back through the scanner."""
    archive = Archive(io.BytesIO(data))
    try:
        inv = wb.inventory(archive)
        found = {}
        for sheet in wb.sheets(archive, inv):
            def take(element, _n, sheet=sheet):
                cell = describe_cell(element)
                found[(sheet.name, cell.ref)] = cell
            with archive.open(sheet.part) as stream:
                scan_units(stream, M + 'c', wb.SHEET_OUTER, take)
        return found
    finally:
        archive.close()


def strings_of(data):
    archive = Archive(io.BytesIO(data))
    try:
        out = []
        with archive.open('xl/sharedStrings.xml') as stream:
            scan_units(stream, M + 'si', wb.STRINGS_OUTER, lambda e, i: out.append(describe_string(e, i)))
        return out
    finally:
        archive.close()


def raw(data, name):
    with zipfile.ZipFile(io.BytesIO(data)) as z:
        return z.read(name).decode()


SAMPLE_ROWS = ('<row r="1"><c r="A1" t="s"><v>0</v></c><c r="B1" s="2"><v>1234567890123456789</v></c>'
               '<c r="C1" t="inlineStr"><is><t>inline ' + SECRET + '</t></is></c><c r="D1" t="b"><v>1</v></c></row>'
               '<row r="2"><c r="A2" t="s"><v>0</v></c><c r="B2" t="s"><v>1</v></c><c r="C2" t="e"><v>#N/A</v></c>'
               '<c r="D2" t="d"><v>2026-01-02T00:00:00</v></c><c r="E2" s="3"/></row>')
SAMPLE_STRINGS = ['<t>shared ' + SECRET + '</t>', '<r><rPr><b/></rPr><t>Zhang </t></r><r><t>San</t></r>',
                  '<t>orphan ' + SECRET + '</t>']


class ModelTests(unittest.TestCase):
    def test_cell_kinds(self):
        data = book([('S', 'visible', SAMPLE_ROWS)], SAMPLE_STRINGS)
        cells = cells_of(data)
        self.assertEqual((cells[('S', 'A1')].kind, cells[('S', 'A1')].shared_index), ('shared', 0))
        self.assertEqual((cells[('S', 'B1')].kind, cells[('S', 'B1')].text), ('number', '1234567890123456789'))
        self.assertEqual((cells[('S', 'C1')].kind, cells[('S', 'C1')].text), ('inline', 'inline ' + SECRET))
        self.assertEqual(cells[('S', 'D1')].kind, 'bool')
        self.assertEqual(cells[('S', 'C2')].kind, 'error')
        self.assertEqual(cells[('S', 'D2')].kind, 'date')
        self.assertEqual((cells[('S', 'E2')].kind, cells[('S', 'E2')].text), ('number', None))

    def test_rich_text_runs_are_concatenated_with_segments(self):
        entries = strings_of(book([('S', 'visible', SAMPLE_ROWS)], SAMPLE_STRINGS))
        self.assertEqual([e.text for e in entries], ['shared ' + SECRET, 'Zhang San', 'orphan ' + SECRET])
        self.assertEqual([(s.start, s.end) for s in entries[1].segments], [(0, 6), (6, 9)])

    def test_every_sheet_is_listed_with_its_state(self):
        data = book([('A', 'visible', ''), ('B', 'hidden', ''), ('C', 'veryHidden', '')])
        archive = Archive(io.BytesIO(data))
        listed = wb.sheets(archive, wb.inventory(archive))
        archive.close()
        self.assertEqual([(s.name, s.state) for s in listed], [('A', 'visible'), ('B', 'hidden'), ('C', 'veryHidden')])


class RewriteTests(unittest.TestCase):
    def test_no_change_keeps_values_and_structure(self):
        data = book([('S', 'visible', SAMPLE_ROWS)], SAMPLE_STRINGS[:2])
        out, report = rewrite(data)
        self.assertEqual({k: (c.kind, c.text, c.shared_index) for k, c in cells_of(out).items()},
                         {k: (c.kind, c.text, c.shared_index) for k, c in cells_of(data).items()})
        self.assertEqual([s.text for s in strings_of(out)], [s.text for s in strings_of(data)])
        self.assertEqual((report.cells, report.cells_changed, report.strings_blanked), (9, 0, 0))
        self.assertIn('<c r="B1" s="2">', raw(out, 'xl/worksheets/sheet1.xml'))
        self.assertRegex(raw(out, 'xl/sharedStrings.xml'), r'<rPr><b(/>|></b>)')  # run formatting kept
        self.assertEqual(raw(out, 'xl/styles.xml'), raw(data, 'xl/styles.xml'))

    def test_changed_shared_cell_becomes_inline_and_orphans_are_emptied(self):
        data = book([('S', 'visible', SAMPLE_ROWS)], SAMPLE_STRINGS, sst_count=3)
        changed = []

        def policy(sheet, cell):
            if cell.kind == 'shared' and cell.shared_index == 0:
                changed.append(cell.ref)
                return CellChange('[REDACTED]')

        out, report = rewrite(data, policy)
        self.assertEqual(changed, ['A1', 'A2'])
        cells = cells_of(out)
        self.assertEqual((cells[('S', 'A1')].kind, cells[('S', 'A1')].text), ('inline', '[REDACTED]'))
        self.assertEqual(cells[('S', 'B2')].shared_index, 1)  # untouched cells keep their index
        texts = [s.text for s in strings_of(out)]
        self.assertEqual(texts, ['', 'Zhang San', ''])        # entry 0 lost its last reference; 2 never had one
        self.assertEqual(len(texts), 3)                       # positions stay valid
        self.assertEqual(report.strings_blanked, 2)
        self.assertIn('count="1"', raw(out, 'xl/sharedStrings.xml'))
        self.assertNotIn(SECRET.encode(), b''.join(zipfile.ZipFile(io.BytesIO(out)).read(n) for n in
                                                  ('xl/sharedStrings.xml',)))

    def test_partially_changed_entry_survives_for_remaining_references(self):
        data = book([('S', 'visible', SAMPLE_ROWS)], SAMPLE_STRINGS[:2])
        out, _ = rewrite(data, lambda s, c: CellChange('x') if c.ref == 'A1' else None)
        self.assertEqual([s.text for s in strings_of(out)], ['shared ' + SECRET, 'Zhang San'])

    def test_shared_string_edit_applies_once_for_every_referencing_cell(self):
        data = book([('S', 'visible', SAMPLE_ROWS)], SAMPLE_STRINGS[:2])
        seen = []

        def edit(entry):
            seen.append(entry.index)
            i = entry.text.find(SECRET)
            return [Edit(i, i + len(SECRET), '[X]')] if i >= 0 else None

        out, report = rewrite(data, string_policy=edit)
        self.assertEqual(seen, [0, 1])
        self.assertEqual([s.text for s in strings_of(out)], ['shared [X]', 'Zhang San'])
        self.assertEqual(cells_of(out)[('S', 'A2')].shared_index, 0)
        self.assertEqual(report.strings_edited, 1)

    def test_edit_across_rich_runs_keeps_formatting_nodes(self):
        data = book([('S', 'visible', SAMPLE_ROWS)], SAMPLE_STRINGS[:2])
        out, _ = rewrite(data, string_policy=lambda e: [Edit(3, 8, '[N]')] if e.index == 1 else None)
        self.assertEqual(strings_of(out)[1].text, 'Zha[N]n')
        self.assertRegex(raw(out, 'xl/sharedStrings.xml'), r'<rPr><b(/>|></b>)')

    def test_inline_and_hidden_sheets_are_processed(self):
        rows = f'<row r="1"><c r="A1" t="inlineStr"><is><t>{SECRET}</t></is></c></row>'
        data = book([('V', 'visible', rows), ('H', 'hidden', rows), ('X', 'veryHidden', rows)])
        visited = []

        def policy(sheet, cell):
            visited.append(sheet.name)
            return CellChange('[X]')

        out, _ = rewrite(data, policy)
        self.assertEqual(visited, ['V', 'H', 'X'])
        self.assertEqual({k[0]: c.text for k, c in cells_of(out).items()}, {'V': '[X]', 'H': '[X]', 'X': '[X]'})

    def test_number_change_needs_explicit_authorization(self):
        data = book([('S', 'visible', SAMPLE_ROWS)], SAMPLE_STRINGS[:2])
        with self.assertRaisesRegex(PackageError, 'XLSX_TYPE_CHANGE_POLICY_REQUIRED'):
            rewrite(data, lambda s, c: CellChange('[N]') if c.ref == 'B1' else None)
        out, _ = rewrite(data, lambda s, c: CellChange('[N]', True) if c.ref == 'B1' else None)
        self.assertEqual((cells_of(out)[('S', 'B1')].kind, cells_of(out)[('S', 'B1')].text), ('inline', '[N]'))
        self.assertIn('r="B1" s="2"', raw(out, 'xl/worksheets/sheet1.xml'))  # reference and style survive

    def test_clearing_a_cell_removes_value_and_type(self):
        data = book([('S', 'visible', SAMPLE_ROWS)], SAMPLE_STRINGS[:2])
        out, _ = rewrite(data, lambda s, c: CellChange(None) if c.ref == 'C1' else None)
        cell = cells_of(out)[('S', 'C1')]
        self.assertEqual((cell.kind, cell.text), ('number', None))
        self.assertNotIn(SECRET, raw(out, 'xl/worksheets/sheet1.xml'))

    def test_bool_and_error_cells_cannot_be_replaced(self):
        data = book([('S', 'visible', SAMPLE_ROWS)], SAMPLE_STRINGS[:2])
        for ref in ('D1', 'C2'):
            with self.assertRaisesRegex(PackageError, 'XLSX_CELL_KIND_POLICY_REQUIRED'):
                rewrite(data, lambda s, c, ref=ref: CellChange('x', True) if c.ref == ref else None)

    def test_text_is_escaped_and_whitespace_preserved(self):
        rows = '<row r="1"><c r="A1" t="inlineStr"><is><t>x</t></is></c></row>'
        data = book([('S', 'visible', rows)])
        out, _ = rewrite(data, lambda s, c: CellChange(' <a&b> 中文 😀 \n'))
        self.assertEqual(cells_of(out)[('S', 'A1')].text, ' <a&b> 中文 😀 \n')
        self.assertIn('xml:space="preserve"', raw(out, 'xl/worksheets/sheet1.xml'))
        self.assertNotIn('xmlns:ns', raw(out, 'xl/worksheets/sheet1.xml'))  # xml namespace keeps its reserved prefix

    def test_invalid_replacements_are_rejected(self):
        rows = '<row r="1"><c r="A1" t="inlineStr"><is><t>x</t></is></c></row>'
        data = book([('S', 'visible', rows)])
        for bad in ('a\x00b', 'a\x01b', '\ud800', '_x0041_', 'a\ufffeb'):
            with self.subTest(bad=bad), self.assertRaises(PackageError):
                rewrite(data, lambda s, c, bad=bad: CellChange(bad))

    def test_candidate_is_a_complete_valid_package(self):
        data = book([('S', 'visible', SAMPLE_ROWS)], SAMPLE_STRINGS)
        out, _ = rewrite(data, lambda s, c: CellChange('[X]') if c.kind == 'shared' else None)
        archive = Archive(io.BytesIO(out))
        try:
            inv = wb.inventory(archive)
            self.assertEqual({p.name for p in inv.parts}, {p.name for p in wb.inventory(Archive(io.BytesIO(data))).parts})
        finally:
            archive.close()
        with zipfile.ZipFile(io.BytesIO(out)) as z:
            self.assertIsNone(z.testzip())
            for name in z.namelist():
                etree.fromstring(z.read(name))  # every part is well-formed XML

    def test_unreferenced_strings_are_emptied_even_with_no_policy(self):
        data = book([('S', 'visible', '<row r="1"><c r="A1" t="s"><v>0</v></c></row>')], SAMPLE_STRINGS)
        out, report = rewrite(data)
        self.assertEqual([s.text for s in strings_of(out)], ['shared ' + SECRET, '', ''])
        self.assertEqual(report.strings_blanked, 2)


CORE = 'application/vnd.openxmlformats-package.core-properties+xml'
APP = 'application/vnd.openxmlformats-officedocument.extended-properties+xml'
CUSTOM = 'application/vnd.openxmlformats-officedocument.custom-properties+xml'
PROPS = {'docProps/core.xml': (b'<cp>author Zhang San</cp>', CORE),
         'docProps/app.xml': (b'<Properties>Company ACME</Properties>', APP),
         'docProps/custom.xml': (b'<Properties>ProjectX</Properties>', CUSTOM)}
PROP_RELS = ('<Relationship Id="rId2" Type="http://schemas.openxmlformats.org/package/2006/relationships/metadata/core-properties" Target="docProps/core.xml"/>'
             f'<Relationship Id="rId3" Type="{RNS}/extended-properties" Target="docProps/app.xml"/>'
             f'<Relationship Id="rId4" Type="{RNS}/custom-properties" Target="docProps/custom.xml"/>')


class PropertyTests(unittest.TestCase):
    ROWS = '<row r="1"><c r="A1" t="inlineStr"><is><t>x</t></is></c></row>'

    def make(self, **kwargs):
        return book([('S', 'visible', self.ROWS)], extra=PROPS, root_rels=PROP_RELS, **kwargs)

    def test_properties_stop_export_unless_the_caller_authorizes_removal(self):
        with self.assertRaisesRegex(PackageError, 'XLSX_UNINSPECTED_PART_POLICY_REQUIRED'):
            rewrite(self.make())

    def test_authorized_removal_drops_parts_overrides_and_relationships(self):
        out, report = rewrite(self.make(), strip_properties=True)
        self.assertEqual(report.parts_removed, ('docProps/app.xml', 'docProps/core.xml', 'docProps/custom.xml'))
        with zipfile.ZipFile(io.BytesIO(out)) as z:
            names = z.namelist()
            blob = b''.join(z.read(n) for n in names)
            self.assertFalse([n for n in names if n.startswith('docProps')])
            self.assertNotIn(b'docProps', blob)          # no override, no relationship left
            self.assertNotIn(b'Zhang San', blob)
            self.assertNotIn(b'ACME', blob)
            self.assertIn(b'officeDocument', z.read('_rels/.rels'))  # the main relationship survives
        archive = Archive(io.BytesIO(out))
        try:
            inv = wb.inventory(archive)          # the candidate is a valid package
            self.assertFalse([p for p in inv.parts if p.role == 'uninspected'])
        finally:
            archive.close()

    def test_removal_does_not_hide_other_unknown_parts(self):
        data = book([('S', 'visible', self.ROWS)], root_rels=PROP_RELS,
                    extra={**PROPS, 'xl/comments1.xml': (b'<comments/>', 'application/vnd.openxmlformats-officedocument.spreadsheetml.comments+xml')})
        with self.assertRaisesRegex(PackageError, 'XLSX_UNINSPECTED_PART_POLICY_REQUIRED'):
            rewrite(data, strip_properties=True)

    def test_a_property_part_also_referenced_elsewhere_is_not_removed(self):
        shared = f'<Relationship Id="rId9" Type="{RNS}/image" Target="../docProps/core.xml"/>'
        data = book([('S', 'visible', self.ROWS)], extra=PROPS, root_rels=PROP_RELS, rels_extra=shared)
        with self.assertRaisesRegex(PackageError, 'XLSX_PROPERTY_PART_SHARED'):
            rewrite(data, strip_properties=True)

    def test_orphan_property_parts_without_a_root_relationship_still_stop_export(self):
        data = book([('S', 'visible', self.ROWS)], extra=PROPS)
        with self.assertRaisesRegex(PackageError, 'XLSX_UNINSPECTED_PART_POLICY_REQUIRED'):
            rewrite(data, strip_properties=True)

    def test_a_workbook_without_properties_is_unchanged_by_the_option(self):
        data = book([('S', 'visible', self.ROWS)])
        out, report = rewrite(data, strip_properties=True)
        self.assertEqual(report.parts_removed, ())
        with zipfile.ZipFile(io.BytesIO(out)) as z, zipfile.ZipFile(io.BytesIO(data)) as source:
            self.assertEqual(sorted(z.namelist()), sorted(source.namelist()))


class RefusalTests(unittest.TestCase):
    ROW = '<row r="1"><c r="A1" t="inlineStr"><is><t>x</t></is></c>%s</row>'

    def refuse(self, data, code, **kwargs):
        with self.assertRaisesRegex(PackageError, code):
            rewrite(data, **kwargs)

    def test_formulas_stop_export_even_when_untouched(self):
        for cell in ('<c r="B1"><f>A1&amp;"x"</f><v>0</v></c>', '<c r="B1" t="str"><f>"S"</f><v>S</v></c>'):
            data = book([('S', 'visible', self.ROW % cell)])
            self.refuse(data, 'XLSX_FORMULA_POLICY_REQUIRED')

    def test_unknown_parts_and_relationships_stop_export(self):
        sheet = [('S', 'visible', self.ROW % '')]
        self.refuse(book(sheet, extra={'docProps/core.xml': (b'<cp/>', 'application/vnd.openxmlformats-package.core-properties+xml')}),
                    'XLSX_UNINSPECTED_PART_POLICY_REQUIRED')
        self.refuse(book(sheet, extra={'xl/comments1.xml': (b'<comments/>', 'application/vnd.openxmlformats-officedocument.spreadsheetml.comments+xml')}),
                    'XLSX_UNINSPECTED_PART_POLICY_REQUIRED')
        self.refuse(book(sheet, extra={'orphan.bin': (b'secret', None)}), 'XLSX_UNINSPECTED_PART_POLICY_REQUIRED')
        ext = f'<Relationship Id="rId9" Type="{RNS}/hyperlink" Target="http://example.com" TargetMode="External"/>'
        self.refuse(book(sheet, rels_extra=ext), 'XLSX_EXTERNAL_RELATIONSHIP_POLICY_REQUIRED')

    def test_workbook_constructs_outside_the_subset_stop_export(self):
        sheet = [('S', 'visible', self.ROW % '')]
        for extra in ('<definedNames><definedName name="n">S!$A$1</definedName></definedNames>',
                      '<externalReferences/>', '<pivotCaches/>'):
            self.refuse(book(sheet, workbook_extra=extra), 'XLSX_WORKBOOK_POLICY_REQUIRED')

    def test_sheet_vocabulary_is_closed(self):
        for element in ('<hyperlinks/>', '<drawing r:id="rId1"/>', '<dataValidations/>', '<extLst/>'):
            data = book([('S', 'visible', self.ROW % '')])
            data = self.inject(data, element)
            self.refuse(data, 'XLSX_PART_SUBSET_UNSUPPORTED')

    @staticmethod
    def inject(data, element):
        out = io.BytesIO()
        with zipfile.ZipFile(io.BytesIO(data)) as source, zipfile.ZipFile(out, 'w') as target:
            for item in source.namelist():
                content = source.read(item)
                if item == 'xl/worksheets/sheet1.xml':
                    content = content.replace(b'</worksheet>', element.encode() + b'</worksheet>')
                target.writestr(item, content)
        return out.getvalue()

    def test_cells_outside_the_model_stop_export(self):
        for cell in ('<c r="B1" cm="1"><v>1</v></c>', '<c r="B1" t="n"><v>1</v><x/></c>', '<c t="n"><v>1</v></c>',
                     '<c r="B1" t="zz"><v>1</v></c>', '<c r="B1" t="s"><v>x</v></c>', '<c r="B1" t="inlineStr"/>',
                     '<c r="B1" t="inlineStr"><is><t>a</t></is><v>1</v></c>',
                     '<c r="B1" t="inlineStr"><is><rPh sb="0" eb="1"><t>reading</t></rPh><t>a</t></is></c>',
                     '<c r="B1" t="inlineStr"><is><t>_x0041_</t></is></c>'):
            with self.subTest(cell=cell):
                self.refuse(book([('S', 'visible', self.ROW % cell)], ['<t>s</t>']), 'XLSX_[A-Z_]+')

    def test_string_entries_outside_the_model_stop_export(self):
        sheet = [('S', 'visible', '<row r="1"><c r="A1" t="s"><v>0</v></c></row>')]
        for entry in ('<r><t>a</t></r><rPh sb="0" eb="1"><t>reading</t></rPh>', '<t>_x0041_</t>', '<unknown/>'):
            with self.subTest(entry=entry):
                self.refuse(book(sheet, [entry]), 'XLSX_[A-Z_]+')

    def test_shared_index_out_of_range(self):
        data = book([('S', 'visible', '<row r="1"><c r="A1" t="s"><v>5</v></c></row>')], ['<t>a</t>'])
        self.refuse(data, 'XLSX_SHARED_INDEX_INVALID')
        data = book([('S', 'visible', '<row r="1"><c r="A1" t="s"><v>0</v></c></row>')])
        self.refuse(data, 'XLSX_SHARED_INDEX_INVALID')  # no shared-strings part at all

    def test_sheet_table_must_be_consistent(self):
        data = book([('A', 'visible', ''), ('B', 'visible', '')])
        out = io.BytesIO()
        with zipfile.ZipFile(io.BytesIO(data)) as source, zipfile.ZipFile(out, 'w') as target:
            for item in source.namelist():
                content = source.read(item)
                if item == 'xl/workbook.xml':
                    content = content.replace(b'name="B"', b'name="A"')
                target.writestr(item, content)
        self.refuse(out.getvalue(), 'XLSX_SHEET_INVALID')
        out = io.BytesIO()
        with zipfile.ZipFile(io.BytesIO(data)) as source, zipfile.ZipFile(out, 'w') as target:
            for item in source.namelist():
                content = source.read(item)
                if item == 'xl/workbook.xml':
                    content = content.replace(b'<sheet name="B" sheetId="2" r:id="rId2"/>', b'')
                target.writestr(item, content)
        self.refuse(out.getvalue(), 'XLSX_ORPHAN_SHEET')  # a worksheet part no entry reaches

    def test_xml_hazards_are_rejected(self):
        sheet = [('S', 'visible', self.ROW % '')]
        for bad in (b'<!DOCTYPE x [<!ENTITY e "boom">]><worksheet xmlns="%s"><sheetData/></worksheet>' % NS.encode(),
                    b'<worksheet xmlns="%s"><!-- secret --><sheetData/></worksheet>' % NS.encode(),
                    b'<worksheet xmlns="%s"><?pi x?><sheetData/></worksheet>' % NS.encode(),
                    b'<worksheet xmlns="%s">stray text<sheetData/></worksheet>' % NS.encode(),
                    b'<worksheet xmlns="%s"><sheetData></worksheet>' % NS.encode()):
            data = book(sheet)
            out = io.BytesIO()
            with zipfile.ZipFile(io.BytesIO(data)) as source, zipfile.ZipFile(out, 'w') as target:
                for item in source.namelist():
                    target.writestr(item, bad if item == 'xl/worksheets/sheet1.xml' else source.read(item))
            with self.subTest(bad=bad[:30]):
                self.refuse(out.getvalue(), '(XML_|XLSX_)[A-Z_]+')

    def test_unit_size_limit(self):
        rows = '<row r="1"><c r="A1" t="inlineStr"><is><t>' + 'x' * 5000 + '</t></is></c></row>'
        self.refuse(book([('S', 'visible', rows)]), 'XLSX_UNIT_LIMIT', xml_limits=XMLLimits(unit_chars=1000))

    def test_not_an_xlsx(self):
        from tacet_plugin_sdk.safezip import ArchiveError
        with self.assertRaises(ArchiveError):
            rewrite(b'not a zip')


if __name__ == '__main__':
    unittest.main()
