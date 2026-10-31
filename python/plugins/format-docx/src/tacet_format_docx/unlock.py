"""``format.unlock`` for password-protected DOCX (OOXML encrypted package).

The password arrives in the call message only. Python strings cannot be
zeroed; the reference is dropped as soon as the key is derived and no password
text is put in any error, which is the guarantee the architecture states
(4.6), not a stronger one. The decrypted copy is written to a host-issued,
encrypted, append-only resource, never to a path.
"""
from __future__ import annotations

import io
import zipfile

import msoffcrypto
from msoffcrypto.exceptions import InvalidKeyError

from tacet_plugin_sdk.runtime import TaskError


# msoffcrypto decrypts the whole package in memory (and writes it out before it
# checks that it is a package at all), so the encrypted input is bounded here
# and the result only reaches the sink after it has been validated. Large-file
# memory behaviour is an M0 item that this bound does not close.
MAX_ENCRYPTED_BYTES = 256 << 20


def unlock(source, sink, password: str) -> int:
    """Decrypt ``source`` into ``sink`` and return the number of plaintext bytes written.

    Nothing reaches ``sink`` unless the password verified and the result is a zip
    package, so a wrong password or damaged input leaves no partial copy.
    """
    source.seek(0, 2)
    if source.tell() > MAX_ENCRYPTED_BYTES:
        raise TaskError('RESOURCE_LIMIT')
    source.seek(0)
    try:
        office = msoffcrypto.OfficeFile(source)
        encrypted = office.is_encrypted()
    except Exception:  # the library's parsers raise expat, struct, zlib and olefile errors alike
        raise TaskError('ENCRYPTION_UNSUPPORTED') from None
    if not encrypted:
        raise TaskError('NOT_ENCRYPTED')
    plain = io.BytesIO()
    try:
        office.load_key(password=password, verify_password=True)
    except InvalidKeyError:
        raise TaskError('PASSWORD_INCORRECT') from None
    except Exception:
        raise TaskError('ENCRYPTION_UNSUPPORTED') from None
    finally:
        password = None  # release the reference as early as possible
    # The password verified, so a failure from here on is damage, not a typo:
    # reporting it as a wrong password would send the user back to retype it.
    try:
        office.decrypt(plain, verify_integrity=True)
    except Exception:
        raise TaskError('INPUT_CORRUPT') from None
    data = plain.getbuffer()
    if len(data) == 0 or not zipfile.is_zipfile(plain):
        raise TaskError('INPUT_CORRUPT')
    sink.write(data)
    return len(data)
