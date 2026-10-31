"""Offline validation of the exact JSON Schema shipped from protocol/schema/v1.

The wheel includes repository schemas through build force-include; no second
hand-maintained message contract or remote schema resolution is used.
"""
from __future__ import annotations

import json
import re
from importlib.resources import files
from pathlib import Path

from jsonschema import Draft202012Validator, validators
from referencing import Registry, Resource
from referencing.exceptions import NoSuchResource

BASE = 'https://github.com/jorben/tacet/protocol/schema/v1/'


class SchemaError(ValueError):
    pass


def _pattern(validator, pattern, instance, schema):
    """ECMA-262 ``pattern``: ``$`` matches only at the very end of the string.

    Python's ``$`` also matches before a trailing newline, so a code, key or
    digest followed by "\\n" would pass here and fail in the Go host. Every
    shipped pattern is anchored; the trailing ``$`` is therefore made ``\\Z``.
    """
    if validator.is_type(instance, "string"):
        converted = pattern[:-1] + r"\Z" if pattern.endswith("$") and not pattern.endswith("\\$") else pattern
        if re.search(converted, instance) is None:
            yield validators.ValidationError(f"{instance!r} does not match {pattern!r}")


_Validator = validators.extend(Draft202012Validator, {"pattern": _pattern})


def _no_remote(uri):
    raise NoSuchResource(ref=uri)


class Schemas:
    def __init__(self):
        root = files('tacet_plugin_sdk').joinpath('schemas/v1')
        if not root.is_dir():
            # Editable development imports resolve to src/, while force-include
            # is materialized in the non-editable wheel. Use the same checked-
            # in contract only when the known repository structure is present.
            candidate = Path(__file__).resolve().parents[4] / 'protocol/schema/v1'
            if not candidate.is_dir():
                raise SchemaError('PROTOCOL_SCHEMAS_UNAVAILABLE')
            root = candidate
        self.documents = {}
        registry = Registry(retrieve=_no_remote)
        for path in root.iterdir():
            if path.name.endswith('.schema.json'):
                document = json.loads(path.read_text(encoding='utf-8'))
                Draft202012Validator.check_schema(document)
                self.documents[path.name] = document
                registry = registry.with_resource(BASE + path.name, Resource.from_contents(document))
        self.registry = registry
        self.validators = {}

    def compile(self, name, definition=None):
        if name not in self.documents or (definition is not None and
                definition not in self.documents[name].get('$defs', {})):
            raise SchemaError('PROTOCOL_SCHEMA_INVALID')
        ref = BASE + name + ('#/$defs/' + definition if definition is not None else '')
        self.validators[(name, definition)] = _Validator({'$ref': ref}, registry=self.registry)

    def validate(self, name, definition, value):
        validator = self.validators.get((name, definition))
        if validator is None:
            raise SchemaError('PROTOCOL_SCHEMA_INVALID')
        try:
            # Stop at the first failure; diagnostics may contain secret values
            # and must not be propagated or logged.
            if next(validator.iter_errors(value), None) is not None:
                raise SchemaError('PROTOCOL_SCHEMA_INVALID')
        except SchemaError:
            raise
        except Exception:
            raise SchemaError('PROTOCOL_SCHEMA_INVALID') from None
