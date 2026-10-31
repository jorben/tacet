"""Real protocol entry; currently exposes M0 inventory only, not full format admission."""
import sys

from tacet_plugin_sdk.resources import inherited_reader, inherited_writer
from tacet_plugin_sdk.runtime import Worker
from tacet_plugin_sdk.schema import Schemas
from tacet_plugin_sdk.safezip import Archive
from .extract import extract
from .package import inventory
from .probe import probe
from .unlock import unlock
from .plan import read_plan
from .write import write
from .validate import validate


def inventory_handler(task, params):
    schemas = Schemas()
    schemas.compile('format.schema.json', 'InventoryParams')
    schemas.compile('format.schema.json', 'InventoryItem')
    schemas.validate('format.schema.json', 'InventoryParams', params)
    with inherited_reader(params['input']) as source:
        archive = Archive(source)
        try:
            result = inventory(archive)
            for part in result.parts:
                task.check()
                item = {'kind': 'part', 'part_id': part.name, 'content_type': part.content_type,
                        'bytes': part.size, 'classification': part.role, 'referenced': part.referenced}
                schemas.validate('format.schema.json', 'InventoryItem', item)
                task.emit(item)
            for relation in result.relationships:
                task.check()
                item = {'kind': 'relation', 'source': relation.source, 'id': relation.identifier,
                        'type': relation.kind, 'target': relation.target, 'external': relation.external}
                schemas.validate('format.schema.json', 'InventoryItem', item)
                task.emit(item)
            item = {'kind': 'inventory_complete', 'complete': True,
                    'parts': len(result.parts), 'relations': len(result.relationships)}
            schemas.validate('format.schema.json', 'InventoryItem', item)
            task.emit(item)
        finally:
            archive.close()


def extract_handler(task, params):
    schemas = Schemas()
    for definition in ('ExtractParams', 'ExtractItem'):
        schemas.compile('format.schema.json', definition)
    schemas.validate('format.schema.json', 'ExtractParams', params)
    with inherited_reader(params['input']) as source:
        extract(source, task, schemas)


def probe_handler(task, params):
    schemas = Schemas()
    for definition in ('ProbeParams', 'ProbeResult'):
        schemas.compile('format.schema.json', definition)
    schemas.validate('format.schema.json', 'ProbeParams', params)
    with inherited_reader(params['input']) as source:
        result = probe(source)
    schemas.validate('format.schema.json', 'ProbeResult', result)
    task.emit(result)


def unlock_handler(task, params):
    schemas = Schemas()
    for definition in ('UnlockParams', 'UnlockDone'):
        schemas.compile('format.schema.json', definition)
    schemas.validate('format.schema.json', 'UnlockParams', params)
    password = params.pop('password')
    with inherited_reader(params['input']) as source, inherited_writer(params['output'], 8 << 30) as sink:
        written = unlock(source, sink, password)
    task.emit({'kind': 'unlock_done', 'bytes': written})


def write_handler(task, params):
    schemas = Schemas()
    for definition in ('WriteParams', 'WriteItem', 'PlanLine'):
        schemas.compile('format.schema.json', definition)
    schemas.validate('format.schema.json', 'WriteParams', params)
    with inherited_reader(params['plan']) as plan_source:
        ops = read_plan(plan_source, schemas)
    with inherited_reader(params['input']) as source, inherited_writer(params['candidate'], 8 << 30) as sink:
        write(source, ops, sink, task, schemas)


def validate_handler(task, params):
    schemas = Schemas()
    for definition in ('ValidateParams', 'ValidateItem', 'PlanLine'):
        schemas.compile('format.schema.json', definition)
    schemas.validate('format.schema.json', 'ValidateParams', params)
    with inherited_reader(params['plan']) as plan_source:
        ops = read_plan(plan_source, schemas)
    with inherited_reader(params['input']) as source, inherited_reader(params['candidate']) as candidate:
        validate(source, ops, candidate, task, schemas)


if __name__ == '__main__':
    raise SystemExit(Worker(sys.stdin.buffer, sys.stdout.buffer, 'tacet.docx', '0.1.0',
                           ['format'], {'format.inventory': inventory_handler, 'format.extract': extract_handler,
                                       'format.unlock': unlock_handler,
                                       'format.probe': probe_handler,
                                       'format.write': write_handler, 'format.validate': validate_handler}).run())
