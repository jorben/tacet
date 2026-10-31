"""``format.extract`` for the DOCX text subset: text units, part states, a completion marker.

Every package part gets a state, so a part that was not looked at is reported as
such rather than silently missing. A part whose paragraphs cannot be read is
``failed`` (a coverage gap), not a failure of the whole file. Hidden text and
tracked changes are reported with their own visibility and never merged into
visible body text. Positions are code points of the unit text; the source map is
(part, paragraph ordinal), which the writer's ``Edit`` addresses after a re-scan
of the same part.
"""
from __future__ import annotations

from tacet_plugin_sdk.safezip import Archive

from .package import inventory
from .xmlparts import XMLPartError, scan

_REASON = {'uninspected': 'PART_NOT_INSPECTED'}


def extract(source, task, schemas) -> None:
    archive = Archive(source)
    try:
        result = inventory(archive)
        units = 0

        def emit(item, definition='ExtractItem'):
            schemas.validate('format.schema.json', definition, item)
            task.emit(item)

        for part in result.parts:
            task.check()
            if part.role == 'uninspected':
                emit({'kind': 'part_state', 'part': part.name, 'class': 'unknown', 'state': 'not_inspected', 'reason': _REASON['uninspected']})
                continue
            if part.role != 'text':
                # content types and relationships carry no document text; the inventory parser
                # already refused non-whitespace character data in them
                emit({'kind': 'part_state', 'part': part.name, 'class': 'other', 'state': 'inspected'})
                continue
            found = []

            def take(paragraph, part=part, found=found):
                task.check()
                if not paragraph.text:
                    return
                visibility = 'hidden' if paragraph.hidden else 'revision' if paragraph.revision else 'visible'
                found.append(None)
                nonlocal units
                units += 1
                emit({'kind': 'text_unit', 'unit_id': f'u{units}', 'part': part.name, 'ordinal': paragraph.ordinal,
                      'text': paragraph.text, 'visibility': visibility,
                      'source_map': {'kind': 'paragraph_ordinal', 'part': part.name, 'ordinal': paragraph.ordinal}})

            try:
                with archive.open(part.name) as stream:
                    scan(stream, take)
            except XMLPartError:
                # units already emitted for this part stay valid; the part is reported as a gap
                emit({'kind': 'part_state', 'part': part.name, 'class': 'text', 'state': 'failed', 'reason': 'PART_UNREADABLE'})
                continue
            emit({'kind': 'part_state', 'part': part.name, 'class': 'text', 'state': 'inspected'})
        emit({'kind': 'extract_complete', 'complete': True, 'units': units, 'parts': len(result.parts)})
    finally:
        archive.close()
