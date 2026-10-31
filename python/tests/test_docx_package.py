import io
import unittest
import zipfile

from tacet_plugin_sdk.safezip import Archive, inspect
from tacet_format_docx.package import PackageError, inventory, rewrite_text_subset
from tacet_format_docx.xmlparts import Edit, scan

CT = 'http://schemas.openxmlformats.org/package/2006/content-types'
REL = 'http://schemas.openxmlformats.org/package/2006/relationships'
W = 'http://schemas.openxmlformats.org/wordprocessingml/2006/main'
MAIN_TYPE = 'application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml'


def fixture(extra=(), relationships='', content_types=''):
    output = io.BytesIO()
    entries = [
        ('[Content_Types].xml', f'<Types xmlns="{CT}"><Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/><Override PartName="/word/document.xml" ContentType="{MAIN_TYPE}"/>{content_types}</Types>'),
        ('_rels/.rels', f'<Relationships xmlns="{REL}"><Relationship Id="main" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/></Relationships>'),
        ('word/document.xml', f'<w:document xmlns:w="{W}"><w:body><w:p><w:r><w:rPr><w:b/></w:rPr><w:t>联系人张</w:t></w:r><w:r><w:rPr><w:i/></w:rPr><w:t>三结束</w:t></w:r></w:p></w:body></w:document>'),
    ]
    if relationships:
        entries.append(('word/_rels/document.xml.rels', f'<Relationships xmlns="{REL}">{relationships}</Relationships>'))
    entries.extend(extra)
    with zipfile.ZipFile(output, 'w', compression=zipfile.ZIP_DEFLATED) as archive:
        for name, data in entries:
            archive.writestr(name, data)
    return output.getvalue()


class DOCXPackageTest(unittest.TestCase):
    def inventory_of(self, raw):
        archive = Archive(io.BytesIO(raw))
        try:
            return inventory(archive)
        finally:
            archive.close()

    def test_registers_unknown_unreferenced_parts_as_gaps(self):
        result = self.inventory_of(fixture([('custom/unknown.bin', b'original secret')]))
        part = next(p for p in result.parts if p.name == 'custom/unknown.bin')
        self.assertEqual(part.role, 'uninspected')
        self.assertFalse(part.referenced)
        self.assertTrue(result.complete)
        with self.assertRaisesRegex(PackageError, 'DOCX_UNINSPECTED_PART_POLICY_REQUIRED'):
            rewrite_text_subset(io.BytesIO(fixture([('custom/unknown.bin', b'secret')])),
                                io.BytesIO(), lambda name, p: [])

    def test_real_zip_rewrite_reopens_and_removes_cross_run_value(self):
        raw = fixture()
        output = io.BytesIO()
        before = rewrite_text_subset(io.BytesIO(raw), output,
                                     lambda name, p: [Edit(3, 5, '[人名]')])
        after = self.inventory_of(output.getvalue())
        self.assertEqual([p.name for p in before.parts], [p.name for p in after.parts])
        archive = Archive(io.BytesIO(output.getvalue()))
        try:
            paragraphs = []
            with archive.open('word/document.xml') as source:
                scan(source, paragraphs.append)
            self.assertEqual(paragraphs[0].text, '联系人[人名]结束')
        finally:
            archive.close()
        # Ensure metadata is copied exactly, rather than reconstructed from an
        # object model that might hide unknown container entries.
        with zipfile.ZipFile(io.BytesIO(raw)) as old, zipfile.ZipFile(io.BytesIO(output.getvalue())) as new:
            self.assertEqual(old.read('[Content_Types].xml'), new.read('[Content_Types].xml'))
            self.assertNotIn('张三'.encode(), new.read('word/document.xml'))

    def test_external_relationship_not_followed_or_silently_exported(self):
        raw = fixture(relationships='<Relationship Id="r1" Type="hyperlink" Target="https://example.invalid/private" TargetMode="External"/>')
        self.assertTrue(self.inventory_of(raw).relationships[-1].external)
        with self.assertRaisesRegex(PackageError, 'DOCX_EXTERNAL_RELATIONSHIP_POLICY_REQUIRED'):
            rewrite_text_subset(io.BytesIO(raw), io.BytesIO(), lambda name, p: [])

    def test_dangling_relationship_and_duplicate_id_rejected(self):
        bad = '<Relationship Id="r1" Type="image" Target="media/missing.png"/>'
        with self.assertRaisesRegex(PackageError, 'OPC_RELATIONSHIP_TARGET_MISSING'):
            self.inventory_of(fixture(relationships=bad))
        duplicate = '<Relationship Id="r1" Type="hyperlink" Target="https://example.invalid" TargetMode="External"/>' * 2
        with self.assertRaisesRegex(PackageError, 'OPC_RELATIONSHIP_DUPLICATE'):
            self.inventory_of(fixture(relationships=duplicate))

    def test_opc_uri_traversal_and_encoded_separator_rejected(self):
        for target in ('../../../outside', 'media%2fsecret', 'file:///outside', '//host/private'):
            with self.subTest(target=target), self.assertRaises(PackageError):
                self.inventory_of(fixture(relationships=f'<Relationship Id="r1" Type="image" Target="{target}"/>'))

    def test_missing_and_duplicate_content_type_override_rejected(self):
        override = f'<Override PartName="/word/document.xml" ContentType="{MAIN_TYPE}"/>'
        with self.assertRaisesRegex(PackageError, 'OPC_CONTENT_TYPES_DUPLICATE'):
            self.inventory_of(fixture(content_types=override))
        with self.assertRaisesRegex(PackageError, 'OPC_CONTENT_TYPE_TARGET_MISSING'):
            self.inventory_of(fixture(content_types='<Override PartName="/missing" ContentType="test"/>'))

    def test_all_parts_in_append_only_candidate_are_structurally_registered(self):
        class AppendOnly(io.BytesIO):
            def seek(self, *args):
                raise io.UnsupportedOperation()
        output = AppendOnly()
        rewrite_text_subset(io.BytesIO(fixture()), output, lambda name, p: [Edit(3, 5, '')])
        entries = inspect(io.BytesIO(output.getvalue()))
        self.assertEqual(len(entries), 3)
        self.assertTrue(all(entry.flags & 8 for entry in entries))


if __name__ == '__main__':
    unittest.main()
