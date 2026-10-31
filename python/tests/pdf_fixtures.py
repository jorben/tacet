"""Synthetic PDFs built by hand, independent of the code under test: text pages with
optional extras, and the standard security handler (RC4, 128 bit, revision 3)."""
import hashlib
import struct

PAD = bytes.fromhex('28BF4E5E4E758A4164004E56FFFA01082E2E00B6D0683E802F0CA9FE6453697A')


def rc4(key: bytes, data: bytes) -> bytes:
    s = list(range(256))
    j = 0
    for i in range(256):
        j = (j + s[i] + key[i % len(key)]) % 256
        s[i], s[j] = s[j], s[i]
    out = bytearray()
    i = j = 0
    for byte in data:
        i = (i + 1) % 256
        j = (j + s[i]) % 256
        s[i], s[j] = s[j], s[i]
        out.append(byte ^ s[(s[i] + s[j]) % 256])
    return bytes(out)


def _pad(password: bytes) -> bytes:
    return (password + PAD)[:32]


def security(user: bytes, owner: bytes, doc_id: bytes, permissions: int = -4):
    """Return (file key, O, U) of the revision 3, 128-bit standard security handler."""
    digest = hashlib.md5(_pad(owner)).digest()
    for _ in range(50):
        digest = hashlib.md5(digest).digest()
    okey = digest[:16]
    o = rc4(okey, _pad(user))
    for i in range(1, 20):
        o = rc4(bytes(b ^ i for b in okey), o)
    digest = hashlib.md5(_pad(user) + o + struct.pack('<i', permissions) + doc_id).digest()
    for _ in range(50):
        digest = hashlib.md5(digest[:16]).digest()
    key = digest[:16]
    u = rc4(key, hashlib.md5(PAD + doc_id).digest())
    for i in range(1, 20):
        u = rc4(bytes(b ^ i for b in key), u)
    return key, o, u + bytes(16)


def _object_key(key: bytes, number: int) -> bytes:
    return hashlib.md5(key + struct.pack('<I', number)[:3] + b'\x00\x00').digest()[:min(len(key) + 5, 16)]


def build(pages, user=None, owner=b'owner', permissions=-4, extra=None, page_extra='', catalog_extra='', info=None, resources=''):
    """pages: list of page texts. user=None builds an unencrypted file; user=b'' a
    permission-only one (opens without a password but carries restrictions)."""
    extra = dict(extra or {})
    doc_id = hashlib.md5(b'fixture' + repr(pages).encode()).digest()
    objects = {1: f'<< /Type /Catalog /Pages 2 0 R {catalog_extra} >>'.encode(),
               3: b'<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>'}
    kids, streams = [], {}
    for index, text in enumerate(pages):
        page_id, content_id = 10 + 2 * index, 11 + 2 * index
        kids.append(page_id)
        body = f'BT /F1 12 Tf 20 {100} Td ({text}) Tj ET'.encode()
        streams[content_id] = body
        objects[page_id] = (f'<< /Type /Page /Parent 2 0 R /MediaBox [0 0 300 200] /Resources << /Font << /F1 3 0 R >> {resources} >> '
                            f'/Contents {content_id} 0 R {page_extra} >>').encode()
    objects[2] = f'<< /Type /Pages /Count {len(pages)} /Kids [{" ".join(f"{k} 0 R" for k in kids)}] >>'.encode()
    for number, body in extra.items():
        if isinstance(body, tuple):  # (dictionary, stream bytes)
            streams[number] = body[1]
            objects[number] = body[0].encode()
        else:
            objects[number] = body
    if info:
        objects[4] = ('<< ' + ' '.join(f'/{k} ({v})' for k, v in info.items()) + ' >>').encode()
    key = None
    encrypt_id = max(max(objects), max(streams, default=0)) + 1
    if user is not None:
        key, o, u = security(user, owner, doc_id, permissions)
        objects[encrypt_id] = (b'<< /Filter /Standard /V 2 /R 3 /Length 128 /P ' + str(permissions).encode()
                               + b' /O <' + o.hex().encode() + b'> /U <' + u.hex().encode() + b'> >>')
    out, offsets = bytearray(b'%PDF-1.4\n%\xe2\xe3\xcf\xd3\n'), {}
    for number in sorted(set(objects) | set(streams)):
        offsets[number] = len(out)
        if number in streams:
            data = streams[number]
            if key is not None:
                data = rc4(_object_key(key, number), data)
            head = objects.get(number, b'<< >>')
            head = head.rstrip()
            head = head[:-2].rstrip() if head.endswith(b'>>') else head  # stream dictionaries are given open
            out += f'{number} 0 obj\n'.encode() + head + f' /Length {len(data)} >>\nstream\n'.encode() + data + b'\nendstream\nendobj\n'
        else:
            out += f'{number} 0 obj\n'.encode() + objects[number] + b'\nendobj\n'
    size = max(offsets) + 1
    xref = len(out)
    out += f'xref\n0 {size}\n0000000000 65535 f \n'.encode()
    for number in range(1, size):
        out += (f'{offsets[number]:010d} 00000 n \n' if number in offsets else '0000000000 65535 f \n').encode()
    trailer = f'<< /Size {size} /Root 1 0 R /ID [<{doc_id.hex()}> <{doc_id.hex()}>]'
    if info:
        trailer += ' /Info 4 0 R'
    if user is not None:
        trailer += f' /Encrypt {encrypt_id} 0 R'
    out += f'trailer\n{trailer} >>\nstartxref\n{xref}\n%%EOF\n'.encode()
    return bytes(out)
