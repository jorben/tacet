#!/usr/bin/env python3
"""M0 Seatbelt candidate test on generated data, no personal files accessed.

A passing probe is narrower than platform sandbox admission. App Sandbox signed
helper comparison, native dependencies, resource limits, process-tree recovery,
Windows AppContainer and packaging remain separate acceptance requirements.
"""
from __future__ import annotations

import argparse
import json
import os
import platform
import subprocess
import sys
import tempfile
from pathlib import Path

CHILD = r'''
import json, os, socket, subprocess, sys
secret_path, environment_path, executable, packages = sys.argv[1:]
sys.path.insert(0, packages)
results = {}
def denied(name, action):
    try:
        action()
    except (PermissionError, OSError):
        results[name] = True
    else:
        results[name] = False
# Only generated test locations and loopback are probed.
def read_path():
    with open(secret_path, 'rb') as source:
        source.read()
denied('unauthorized_read_denied', read_path)
def write_environment():
    with open(environment_path, 'wb') as target:
        target.write(b'test')
denied('environment_write_denied', write_environment)
def network():
    with socket.socket() as connection:
        connection.connect(('127.0.0.1', 9))
# Distinguish denial from connection refused: only EPERM/EACCES prove sandbox.
try:
    network()
except OSError as error:
    results['network_denied'] = error.errno in (1, 13)
else:
    results['network_denied'] = False
denied('process_fork_denied', lambda: subprocess.run([executable, '-I', '-c', 'pass'], check=True))
with os.fdopen(3, 'rb') as source:
    results['inherited_input_readable'] = source.read() == b'tacet-fd-marker'
try:
    import lxml.etree
    import pypdfium2 as pdfium
    import onnxruntime
    import tokenizers
    document = pdfium.PdfDocument.new()
    document.close()
except Exception:
    results['native_imports_ready'] = False
else:
    results['native_imports_ready'] = True
print(json.dumps(results))
'''


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--report', type=Path, required=True)
    args = parser.parse_args()
    if sys.platform != 'darwin':
        raise SystemExit('M0_SEATBELT_PLATFORM_UNSUPPORTED')
    executable = Path(sys.executable).resolve()
    runtime = executable.parent.parent
    packages = Path(sys.prefix) / 'lib' / f'python{sys.version_info.major}.{sys.version_info.minor}' / 'site-packages'
    def quote(path):
        return json.dumps(str(path))
    profile = ('(version 1)\n(deny default)\n'
               f'(allow process-exec (literal {quote(executable)}))\n'
               f'(allow file-read* (subpath {quote(runtime)}) (subpath {quote(packages)}))\n'
               '(allow file-read* (literal "/") (subpath "/System/Library") (subpath "/usr/lib") '
               '(literal "/dev/null") (literal "/dev/urandom") (literal "/dev/random"))\n'
               '(allow sysctl-read)\n')
    with tempfile.TemporaryDirectory(prefix='tacet-seatbelt-generated-') as temp:
        root = Path(temp)
        input_path = root / 'input'
        input_path.write_bytes(b'tacet-fd-marker')
        denied_path = root / 'unauthorized'
        denied_path.write_bytes(b'only-generated-test-data')
        with input_path.open('rb') as source:
            # In this standalone process input is expected at fd3; fail rather
            # than silently handing the worker a different data channel.
            if source.fileno() != 3:
                raise SystemExit('M0_UNEXPECTED_FD_LAYOUT')
            result = subprocess.run(['/usr/bin/sandbox-exec', '-p', profile,
                                     str(executable), '-I', '-c', CHILD,
                                     str(denied_path), str(root / 'environment-write'), str(executable), str(packages)],
                                    pass_fds=(3,), capture_output=True, timeout=15, cwd='/')
        if result.returncode:
            # Interpreter errors can contain local paths. Keep the raw output
            # private and emit only a status in the tracked report.
            results = {'worker_started': False, 'worker_exit': result.returncode}
            diagnostic = result.stderr.decode(errors='replace').replace(str(runtime), '<runtime>').replace(str(root), '<generated>')
            print(diagnostic[-2000:], file=sys.stderr)
        else:
            results = json.loads(result.stdout)
            results['worker_started'] = True
    passed = all(value is True for value in results.values())
    report = {'probe': 'm0-macos-seatbelt-candidate', 'status': 'passed' if passed else 'failed',
              'platform': platform.platform(), 'checks': results,
              'limitations': ['generated files and loopback only', 'deprecated sandbox-exec candidate',
                              'not signed App Sandbox helper comparison', 'imports only, no full parser/model workload',
                              'no orphan tree/resource limit test', 'not platform admission']}
    args.report.parent.mkdir(parents=True, exist_ok=True)
    args.report.write_text(json.dumps(report, ensure_ascii=False, indent=2) + '\n')
    print(json.dumps(report))
    if not passed:
        raise SystemExit(1)


if __name__ == '__main__':
    main()
