"""Restore the owner's encrypted ITOM signing backup using its private recovery key."""
import argparse
import base64
import getpass
import hashlib
import json
import os
from pathlib import Path

from cryptography.exceptions import InvalidTag
from cryptography.hazmat.primitives.ciphers.aead import AESGCM

parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument('encrypted_backup', type=Path)
parser.add_argument('--output', type=Path, default=Path('ITOM-signing-key-backup.zip'))
args = parser.parse_args()
data = json.loads(args.encrypted_backup.read_text())
assert data['format'] == 'ITOM-signing-backup-v1' and data['algorithm'] == 'AES-256-GCM'
encoded = getpass.getpass('Private recovery key: ').strip()
key = base64.urlsafe_b64decode(encoded + '=' * (-len(encoded) % 4))
assert len(key) == 32, 'Invalid recovery key length'
try:
    raw = AESGCM(key).decrypt(base64.b64decode(data['nonce_base64'], validate=True),
                              base64.b64decode(data['ciphertext_base64'], validate=True),
                              b'ITOM Android signing backup v1')
except InvalidTag:
    parser.exit(1, 'Recovery key does not match this backup, or the file was changed.\n')
assert hashlib.sha256(raw).hexdigest() == data['plaintext_sha256']
fd = os.open(args.output, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
with os.fdopen(fd, 'wb') as f:
    f.write(raw)
print('Restored private signing backup:', args.output)
