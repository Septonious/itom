"""Restore the exact public signed APK; contains no private signing material."""
import base64
import gzip
import hashlib
import json
from pathlib import Path
import sys

source = Path(sys.argv[1]).read_bytes()
patch = json.loads(gzip.decompress(base64.b64decode(
    Path(__file__).with_name('dev7-public-apk.patch.b64').read_text())))
assert hashlib.sha256(source).hexdigest() == patch['source_sha256']
parts = []
for operation in patch['operations']:
    if 'copy' in operation:
        offset, size = operation['copy']
        assert 0 <= offset <= offset + size <= len(source)
        parts.append(source[offset:offset + size])
    else:
        parts.append(base64.b64decode(operation['literal'], validate=True))
result = b''.join(parts)
assert len(result) == patch['result_bytes']
assert hashlib.sha256(result).hexdigest() == patch['result_sha256']
assert patch['result_sha256'] == 'bc3e0a4e78a36ecbd739d7f6e357fdd70af55d15a242b02acdf3efaa6d022984'
Path(sys.argv[2]).write_bytes(result)
print('Verified exact persistent-signed APK:', patch['result_sha256'])
