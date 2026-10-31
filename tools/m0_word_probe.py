#!/usr/bin/env python3
"""M0 Word open probe on generated DOCX files (macOS, Microsoft Word installed).

Opens each file with `repair false`, reads paragraph text back and closes
without saving. A repair/error dialog, an AppleScript error or a timeout is a
failure. Only generated fixtures are used. Evidence for ONE Word build on this
machine; it is not WPS evidence and not format admission.
"""
import argparse
import io
import json
import platform
import subprocess
import sys
import tempfile
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(ROOT / 'python/tests'))

from test_docx_package import fixture  # noqa: E402
from tacet_format_docx.package import rewrite_text_subset  # noqa: E402
from tacet_format_docx.xmlparts import Edit  # noqa: E402

SCRIPT = '''
on run argv
  set p to item 1 of argv
  tell application "Microsoft Word"
    set d to open file name (POSIX file p) read only true repair false confirm conversions false add to recent files false
    set t to content of text object of d
    close d saving no
    return t
  end tell
end run
'''


def word_version():
    out = subprocess.run(['mdls', '-name', 'kMDItemVersion', '/Applications/Microsoft Word.app'],
                         capture_output=True, text=True)
    return out.stdout.strip().split('=')[-1].strip().strip('"')


def open_in_word(path, timeout):
    try:
        result = subprocess.run(['osascript', '-e', SCRIPT, str(path)], capture_output=True, text=True, timeout=timeout)
    except subprocess.TimeoutExpired:
        return {'opened': False, 'reason': 'timeout_or_blocking_dialog'}
    if result.returncode:
        return {'opened': False, 'reason': 'applescript_error', 'code': result.returncode}
    return {'opened': True, 'text': result.stdout.strip()}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--report', type=Path, required=True)
    parser.add_argument('--timeout', type=int, default=90)
    args = parser.parse_args()
    cases = {}
    with tempfile.TemporaryDirectory(prefix='tacet-word-generated-') as directory:
        root = Path(directory)
        source = fixture()
        candidate = io.BytesIO()
        rewrite_text_subset(io.BytesIO(source), candidate, lambda name, paragraph: [Edit(3, 5, '[人名]')])
        files = {'source_fixture': source, 'rewritten_candidate': candidate.getvalue(),
                 'truncated_negative_control': candidate.getvalue()[:-30]}
        try:
            for name, data in files.items():
                path = root / f'{name}.docx'
                path.write_bytes(data)
                cases[name] = open_in_word(path, args.timeout)
        finally:
            subprocess.run(['pkill', '-x', 'Microsoft Word'], capture_output=True)
    checks = {
        'source_opens': cases['source_fixture']['opened'],
        'candidate_opens_without_repair': cases['rewritten_candidate']['opened'],
        'candidate_text_matches': cases['rewritten_candidate'].get('text', '').replace('\r', '') == '联系人[人名]结束',
        'negative_control_rejected': not cases['truncated_negative_control']['opened'],
    }
    for case in cases.values():
        case.pop('text', None)
    report = {'probe': 'm0-word-open', 'status': 'passed' if all(checks.values()) else 'failed',
              'word_version': word_version(), 'platform': platform.platform(), 'checks': checks,
              'cases': cases,
              'limitations': ['generated text-only subset', 'one Word build on the development machine',
                              'no WPS, Windows Word, images, styles, tables or real office documents',
                              'not format admission']}
    args.report.parent.mkdir(parents=True, exist_ok=True)
    args.report.write_text(json.dumps(report, ensure_ascii=False, indent=2) + '\n')
    print(json.dumps(report, ensure_ascii=False))
    raise SystemExit(0 if report['status'] == 'passed' else 1)


if __name__ == '__main__':
    main()
