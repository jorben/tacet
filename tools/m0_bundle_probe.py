#!/usr/bin/env python3
"""M0 relocatable offline runtime probe (macOS).

Build phase (may use the network/uv cache): copy the managed CPython, install
locked third-party wheels with hashes and the workspace packages as built
non-editable wheels. Run phase: move the tree to a new path, clear the
environment, deny network with Seatbelt, and start the real DOCX plugin
through the protocol. Evidence for one machine; not a signed/notarized
distribution and not a clean-OS test.
"""
import argparse
import json
import os
import platform
import re
import shutil
import subprocess
import sys
import tempfile
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
PY = ROOT / 'python'
sys.path.insert(0, str(PY / 'sdk/src'))
from tacet_plugin_sdk import framing  # noqa: E402

HANDSHAKE = r'''
import io, json, sys
from tacet_plugin_sdk import framing
import lxml.etree, pypdfium2, onnxruntime, tokenizers, numpy, cryptography
import tacet_format_docx.__main__ as docx
import PIL.Image, tacet_format_pdf.raster as raster
import pypdfium2 as pdfium
document = pdfium.PdfDocument.new()
document.new_page(100, 100)
bitmap = document[0].render(scale=1)  # PDFium and Pillow work from the relocated tree
bitmap.to_pil().convert("RGB").close()
from tacet_plugin_sdk.schema import Schemas
Schemas().compile("rpc.schema.json", "Message")  # schemas shipped inside the wheel
sys.stdout.write(json.dumps({"imports": True, "prefix": sys.prefix}))
'''


def run(cmd, **kw):
    return subprocess.run(cmd, check=True, capture_output=True, text=True, **kw)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--report', type=Path, required=True)
    args = parser.parse_args()
    base = Path(run([str(PY / '.venv/bin/python'), '-c', 'import sys;print(sys.base_prefix)']).stdout.strip())
    with tempfile.TemporaryDirectory(prefix='tacet-bundle-build-') as build_dir, \
            tempfile.TemporaryDirectory(prefix='tacet-bundle-run-') as run_dir:
        build, target = Path(build_dir), Path(run_dir) / 'relocated' / 'Tacet Runtime'
        staged = build / 'runtime'
        shutil.copytree(base, staged, symlinks=True)
        interpreter = staged / 'bin' / 'python3'
        wheels = build / 'wheels'
        run(['uv', 'build', '--all-packages', '--wheel', '--out-dir', str(wheels)], cwd=PY)
        requirements = build / 'requirements.txt'
        exported = run(['uv', 'export', '--locked', '--no-dev', '--no-emit-workspace', '--no-hashes',
                        '--format', 'requirements-txt'], cwd=PY).stdout
        exported_hashed = run(['uv', 'export', '--locked', '--no-dev', '--no-emit-workspace',
                               '--format', 'requirements-txt'], cwd=PY).stdout
        requirements.write_text(exported_hashed)
        run(['uv', 'pip', 'install', '--python', str(interpreter), '--break-system-packages', '--require-hashes',
             '--no-build', '-r', str(requirements)])
        run(['uv', 'pip', 'install', '--python', str(interpreter), '--break-system-packages', '--no-build',
             '--no-index', '--no-deps', '--find-links', str(wheels), 'tacet-plugin-sdk', 'tacet-format-docx',
             'tacet-format-pdf', 'tacet-detect-astrlink'])
        target.parent.mkdir(parents=True)
        shutil.move(str(staged), str(target))  # relocation: new path, with a space in it
        python = target / 'bin' / 'python3'
        site = next((target / 'lib').glob('python3*/site-packages'))
        # Native libraries must not reference anything outside system dirs or the bundle.
        stray = []
        for lib in list(site.rglob('*.so')) + list(site.rglob('*.dylib')):
            skip_id = False
            for line in run(['otool', '-L', str(lib)]).stdout.splitlines():
                if line.endswith(':') and not line.startswith('\t'):
                    skip_id = lib.suffix == '.dylib'  # each architecture slice starts with the dylib's own id
                    continue
                match = re.match(r'\s*(.*?) \(compatibility', line)
                if match is None:
                    raise RuntimeError('UNPARSED_OTOOL_LINE')  # never silently skip an unknown shape
                if skip_id:
                    skip_id = False
                    continue
                dep = match.group(1)
                if dep.startswith('/') and not dep.startswith(('/usr/lib/', '/System/Library/')):
                    stray.append(dep)
        # The probe must not find uv, Homebrew or the project venv.
        env = {'PATH': '/usr/bin:/bin'}
        profile = ('(version 1)\n(deny default)\n'
                   f'(allow process-exec (literal {json.dumps(str(python.resolve()))}))\n'
                   f'(allow file-read* (literal "/") (subpath {json.dumps(str(target.resolve()))}) '
                   '(subpath "/System/Library") (subpath "/usr/lib") (literal "/dev/null") '
                   '(literal "/dev/urandom") (literal "/dev/random"))\n(allow sysctl-read)\n')
        imports = subprocess.run(['/usr/bin/sandbox-exec', '-p', profile, str(python.resolve()), '-I', '-c', HANDSHAKE],
                                 capture_output=True, text=True, env=env, cwd='/', timeout=60)
        imported = imports.returncode == 0 and json.loads(imports.stdout).get('imports') is True
        init = {'jsonrpc': '2.0', 'id': 1, 'method': 'initialize', 'params': {
            'protocol': {'min': '1.0', 'max': '1.0'}, 'owner_generation': 1,
            'limits': {'max_message_bytes': framing.MAX_MESSAGE_BYTES}}}
        worker = subprocess.run(['/usr/bin/sandbox-exec', '-p', profile, str(python.resolve()), '-I', '-m', 'tacet_format_docx'],
                                input=framing.encode(init), capture_output=True, env=env, cwd='/', timeout=60)
        import io
        reply = framing.read(io.BytesIO(worker.stdout))
        handshake = bool(reply) and reply['result']['plugin']['id'] == 'tacet.docx'
        size = sum(f.stat().st_size for f in target.rglob('*') if f.is_file())
        uv_present = any('uv' == p.name for p in (target / 'bin').iterdir())
        pth = [p.name for p in site.glob('*.pth')]
    checks = {'imports_in_relocated_offline_sandbox': imported, 'real_plugin_handshake': handshake,
              'no_stray_native_dependencies': not stray, 'no_editable_or_source_tree_references': not any('editable' in n for n in pth),
              'no_uv_in_runtime': not uv_present}
    report = {'probe': 'm0-relocatable-runtime', 'status': 'passed' if all(checks.values()) else 'failed',
              'platform': platform.platform(), 'python': platform.python_version(), 'checks': checks,
              'runtime_bytes': size, 'stray_dependencies': sorted(set(stray))[:20], 'pth_files': pth,
              'limitations': ['same machine and user, not a clean OS image', 'not code-signed, hardened or notarized',
                              'no Windows runtime', 'Seatbelt only; no hard resource limits',
                              'packages installed with uv in the build phase; runtime phase has no installer',
                              'astrlink model weights are a separate downloaded resource']}
    args.report.parent.mkdir(parents=True, exist_ok=True)
    args.report.write_text(json.dumps(report, ensure_ascii=False, indent=2) + '\n')
    print(json.dumps(report, ensure_ascii=False))
    raise SystemExit(0 if report['status'] == 'passed' else 1)


if __name__ == '__main__':
    main()
