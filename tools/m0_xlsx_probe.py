#!/usr/bin/env python3
"""Measure the XLSX value-subset rewrite on a generated workbook.

Run with python/.venv/bin/python in its own process so peak RSS is comparable.
The workbook is synthetic (shared strings with a fixed marker, inline strings,
numbers) and removed afterwards. Single run on the development machine: not
R1, not a distribution, not a format admission, no Excel/WPS opening check.
"""
from __future__ import annotations

import argparse
import faulthandler
import io
import json
import platform
import resource
import sys
import tempfile
import time
import zipfile
from pathlib import Path

from tacet_format_xlsx import workbook as wb
from tacet_format_xlsx.cells import CellChange, Edit
from tacet_format_xlsx.xmlunits import M, scan_units
from tacet_plugin_sdk.safezip import Archive, Limits

NS = 'http://schemas.openxmlformats.org/spreadsheetml/2006/main'
RNS = 'http://schemas.openxmlformats.org/officeDocument/2006/relationships'
PKG = 'http://schemas.openxmlformats.org/package/2006/relationships'
CT = 'http://schemas.openxmlformats.org/package/2006/content-types'
MARKER = 'TEST_SECRET_MARK'


def generate(path, target_bytes, unique):
    """Sheet XML of about ``target_bytes`` uncompressed; every 3rd unique string carries the marker."""
    row_template = ('<row r="{r}"><c r="A{r}" t="s"><v>{a}</v></c><c r="B{r}" s="1"><v>{r}{r}.125</v></c>'
                    '<c r="C{r}" t="inlineStr"><is><t>inline {m} {r}</t></is></c>'
                    '<c r="D{r}" t="s"><v>{b}</v></c></row>')
    with zipfile.ZipFile(path, 'w', zipfile.ZIP_DEFLATED) as z:
        z.writestr('[Content_Types].xml',
                   f'<Types xmlns="{CT}"><Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/>'
                   f'<Override PartName="/xl/workbook.xml" ContentType="{wb.WORKBOOK_TYPE}"/>'
                   f'<Override PartName="/xl/worksheets/sheet1.xml" ContentType="{wb.SHEET_TYPE}"/>'
                   f'<Override PartName="/xl/sharedStrings.xml" ContentType="{wb.STRINGS_TYPE}"/>'
                   '<Override PartName="/xl/styles.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.styles+xml"/></Types>')
        z.writestr('_rels/.rels', f'<Relationships xmlns="{PKG}"><Relationship Id="rId1" Type="{RNS}/officeDocument" Target="xl/workbook.xml"/></Relationships>')
        z.writestr('xl/workbook.xml', f'<workbook xmlns="{NS}" xmlns:r="{RNS}"><sheets><sheet name="Data" sheetId="1" r:id="rId1"/></sheets></workbook>')
        z.writestr('xl/_rels/workbook.xml.rels',
                   f'<Relationships xmlns="{PKG}"><Relationship Id="rId1" Type="{RNS}/worksheet" Target="worksheets/sheet1.xml"/>'
                   f'<Relationship Id="rId2" Type="{RNS}/sharedStrings" Target="sharedStrings.xml"/>'
                   f'<Relationship Id="rId3" Type="{RNS}/styles" Target="styles.xml"/></Relationships>')
        z.writestr('xl/styles.xml', f'<styleSheet xmlns="{NS}"><fonts count="1"><font/></fonts></styleSheet>')
        with z.open('xl/sharedStrings.xml', 'w', force_zip64=False) as out:
            out.write(f'<?xml version="1.0" encoding="UTF-8"?><sst xmlns="{NS}" count="0" uniqueCount="{unique}">'.encode())
            for i in range(unique):
                body = f'姓名 {i} {MARKER} 联系' if i % 3 == 0 else f'public 普通内容 {i}'
                out.write(f'<si><t>{body}</t></si>'.encode())
            out.write(b'</sst>')
        rows = 0
        with z.open('xl/worksheets/sheet1.xml', 'w', force_zip64=False) as out:
            out.write(f'<?xml version="1.0" encoding="UTF-8"?><worksheet xmlns="{NS}"><sheetData>'.encode())
            written = 0
            while written < target_bytes:
                rows += 1
                data = row_template.format(r=rows, a=(rows * 7) % unique, b=(rows * 13) % unique,
                                           m=MARKER if rows % 5 == 0 else 'x').encode()
                out.write(data)
                written += len(data)
            out.write(b'</sheetData></worksheet>')
    return rows


def main():
    faulthandler.dump_traceback_later(1500, exit=True)  # hard stop: never hang the caller
    parser = argparse.ArgumentParser()
    parser.add_argument('--mib', type=int, required=True)
    parser.add_argument('--unique', type=int, default=200_000)
    parser.add_argument('--report', required=True)
    args = parser.parse_args()
    result = {'requested_sheet_mib': args.mib, 'unique_strings': args.unique,
              'python': platform.python_version(), 'platform': platform.platform()}
    limits = Limits(expanded_bytes=16 * 1024**3, entry_bytes=8 * 1024**3, ratio=5000)
    with tempfile.TemporaryDirectory(prefix='tacet-xlsx-probe-') as work:
        source, output = Path(work, 'in.xlsx'), Path(work, 'out.xlsx')
        t0 = time.perf_counter()
        result['rows'] = generate(source, args.mib << 20, args.unique)
        result['source_bytes'] = source.stat().st_size
        result['generate_seconds'] = round(time.perf_counter() - t0, 3)

        def string_policy(entry):
            i = entry.text.find(MARKER)
            return [Edit(i, i + len(MARKER), '[MARK]')] if i >= 0 else None

        def cell_policy(sheet, cell):
            if cell.kind == 'inline' and MARKER in cell.text:
                return CellChange(cell.text.replace(MARKER, '[MARK]'))
            return None

        t0 = time.perf_counter()
        with open(source, 'rb') as src, open(output, 'wb') as out:
            report = wb.rewrite_value_subset(src, out, cell_policy, string_policy, limits)
        result['rewrite_seconds'] = round(time.perf_counter() - t0, 3)
        result['output_bytes'] = output.stat().st_size
        result['report'] = {'cells': report.cells, 'cells_changed': report.cells_changed,
                            'strings': report.strings, 'strings_edited': report.strings_edited,
                            'strings_blanked': report.strings_blanked}

        t0 = time.perf_counter()
        remaining = replaced = 0
        with open(output, 'rb') as handle:
            archive = Archive(handle, limits)
            try:
                def strings(element, _i):
                    nonlocal remaining, replaced
                    text = ''.join(t.text or '' for t in element.iter(M + 't'))
                    remaining += MARKER in text
                    replaced += '[MARK]' in text

                def cells(element, _i):
                    nonlocal remaining, replaced
                    text = ''.join(t.text or '' for t in element.iter(M + 't'))
                    remaining += MARKER in text
                    replaced += '[MARK]' in text

                with archive.open('xl/sharedStrings.xml') as s:
                    scan_units(s, M + 'si', wb.STRINGS_OUTER, strings)
                with archive.open('xl/worksheets/sheet1.xml') as s:
                    scan_units(s, M + 'c', wb.SHEET_OUTER, cells)
            finally:
                archive.close()
        result['verify_seconds'] = round(time.perf_counter() - t0, 3)
        result['remaining_marker_units'] = remaining
        result['replaced_marker_units'] = replaced
    peak = resource.getrusage(resource.RUSAGE_SELF).ru_maxrss
    result['peak_rss_bytes'] = peak if sys.platform == 'darwin' else peak * 1024
    result['note'] = 'single run; development machine; not R1; not a distribution; no Excel/WPS opening check'
    Path(args.report).write_text(json.dumps(result, ensure_ascii=False, indent=2) + '\n')
    print(json.dumps(result, ensure_ascii=False, indent=2))
    if remaining:
        raise SystemExit('markers remained in output')


if __name__ == '__main__':
    main()
