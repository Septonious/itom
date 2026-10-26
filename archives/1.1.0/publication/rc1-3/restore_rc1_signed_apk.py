"""Restore a verified public APK signature; no private signing material."""
import base64,gzip,hashlib,json
from pathlib import Path
import sys
root=Path(__file__).resolve().parent
patch=json.loads(gzip.decompress(base64.b64decode((root/'rc1-public-apk.patch.b64').read_text().strip(),validate=True)))
source=Path(sys.argv[1]).read_bytes()
assert hashlib.sha256(source).hexdigest()==patch['source_sha256']
parts=[]
for op in patch['operations']:
    if 'copy' in op:
        offset,size=op['copy'];assert 0<=offset<=offset+size<=len(source)
        parts.append(source[offset:offset+size])
    else:parts.append(base64.b64decode(op['literal'],validate=True))
result=b''.join(parts)
metadata=json.loads((root/'RC1_BUILD.json').read_text())
assert len(result)==patch['result_bytes']
assert hashlib.sha256(result).hexdigest()==patch['result_sha256']==metadata['signed_apk_sha256']
Path(sys.argv[2]).write_bytes(result)
print('Verified persistent-signed APK',patch['result_sha256'])
