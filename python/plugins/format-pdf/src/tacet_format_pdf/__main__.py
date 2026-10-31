"""Real protocol entry for the text-PDF plugin: probe, unlock, extract."""
import sys

import pypdfium2 as pdfium

from tacet_plugin_sdk.resources import inherited_reader, inherited_writer
from tacet_plugin_sdk.runtime import TaskError, Worker
from tacet_plugin_sdk.schema import Schemas

from .document import has_header, require_size
from .extract import extract
from .preview import preview
from .probe import probe
from .unlock import unlock
from .validate import validate
from .write import write
from tacet_plugin_sdk.plan import read_plan


def _schemas(*definitions):
    schemas = Schemas()
    for definition in definitions:
        schemas.compile('format.schema.json', definition)
    return schemas


def probe_handler(task, params):
    schemas = _schemas('ProbeParams', 'ProbeResult')
    schemas.validate('format.schema.json', 'ProbeParams', params)
    with inherited_reader(params['input']) as source:
        result = probe(source)
    schemas.validate('format.schema.json', 'ProbeResult', result)
    task.emit(result)


def unlock_handler(task, params):
    schemas = _schemas('UnlockParams', 'UnlockDone')
    schemas.validate('format.schema.json', 'UnlockParams', params)
    password = params.pop('password')
    with inherited_reader(params['input']) as source, inherited_writer(params['output'], 8 << 30) as sink:
        written = unlock(source, sink, password)
    task.emit({'kind': 'unlock_done', 'bytes': written})


def extract_handler(task, params):
    schemas = _schemas('ExtractParams', 'ExtractItem')
    schemas.validate('format.schema.json', 'ExtractParams', params)
    with inherited_reader(params['input']) as source:
        require_size(source)
        if not has_header(source):
            raise TaskError('INPUT_CORRUPT')
        try:
            document = pdfium.PdfDocument(source)
        except pdfium.PdfiumError:
            raise TaskError('INPUT_CORRUPT') from None
        try:
            extract(document, task, schemas)
        except pdfium.PdfiumError:
            raise TaskError('INPUT_CORRUPT') from None
        finally:
            document.close()


def preview_handler(task, params):
    schemas = _schemas('PreviewParams', 'PreviewItem')
    schemas.validate('format.schema.json', 'PreviewParams', params)
    with inherited_reader(params['input']) as source, inherited_writer(params['output'], 64 << 20) as sink:
        require_size(source)
        item = preview(source, sink, params['page'], params.get('dpi', 100), params.get('marks', []))
    schemas.validate('format.schema.json', 'PreviewItem', item)
    task.emit(item)


def write_handler(task, params):
    schemas = _schemas('WriteParams', 'WriteItem', 'PlanLine')
    schemas.validate('format.schema.json', 'WriteParams', params)
    with inherited_reader(params['plan']) as plan_source:
        ops = read_plan(plan_source, schemas)
    dpi, style = params.get('dpi', 200), params.get('style', 'black')
    with inherited_reader(params['input']) as source, inherited_writer(params['candidate'], 8 << 30) as sink:
        require_size(source)
        write(source, ops, sink, task, schemas, dpi, style)


def validate_handler(task, params):
    schemas = _schemas('ValidateParams', 'ValidateItem', 'PlanLine')
    schemas.validate('format.schema.json', 'ValidateParams', params)
    with inherited_reader(params['plan']) as plan_source:
        ops = read_plan(plan_source, schemas)
    dpi, style = params.get('dpi', 200), params.get('style', 'black')
    with inherited_reader(params['input']) as source, inherited_reader(params['candidate']) as candidate:
        require_size(source)
        validate(source, ops, candidate, task, schemas, dpi, style)


if __name__ == '__main__':
    raise SystemExit(Worker(sys.stdin.buffer, sys.stdout.buffer, 'tacet.pdf', '0.1.0', ['format'],
                            {'format.probe': probe_handler, 'format.unlock': unlock_handler,
                             'format.extract': extract_handler, 'format.preview': preview_handler,
                             'format.write': write_handler, 'format.validate': validate_handler}).run())
