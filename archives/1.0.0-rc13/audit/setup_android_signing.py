#!/usr/bin/env python3
"""Run locally with authenticated gh + keytool; never commit the private directory."""
import argparse
import base64
import json
import os
from pathlib import Path
import secrets
import subprocess

NAMES = ("ITOM_ANDROID_KEYSTORE_B64", "ITOM_ANDROID_STORE_PASSWORD", "ITOM_ANDROID_KEY_ALIAS", "ITOM_ANDROID_KEY_PASSWORD")


def set_secret(repository, name, value):
    # Secret values go through stdin, never the process command line or logs.
    subprocess.run(["gh", "secret", "set", name, "--repo", repository],
                   input=value, text=True, capture_output=True, check=True)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--repo", default="Septonious/itom")
    parser.add_argument("--private-dir", required=True, type=Path)
    args = parser.parse_args()
    private = args.private_dir.resolve()
    git = subprocess.run(["git", "rev-parse", "--show-toplevel"], capture_output=True, text=True)
    if git.returncode == 0 and private.is_relative_to(Path(git.stdout.strip()).resolve()):
        raise SystemExit("Choose a private directory OUTSIDE the Git repository")
    auth = subprocess.run(["gh", "auth", "status"], capture_output=True)
    if auth.returncode:
        raise SystemExit("Authenticate gh locally with repository-secrets write access, then rerun. No secrets were changed.")
    listed = subprocess.run(["gh", "secret", "list", "--repo", args.repo, "--json", "name"], capture_output=True, text=True, check=True)
    existing = {x["name"] for x in json.loads(listed.stdout)}
    if existing.intersection(NAMES):
        raise SystemExit("Existing signing secrets found; refusing to overwrite or rotate them. Keep the existing key.")
    private.mkdir(mode=0o700, parents=True, exist_ok=True)
    private.chmod(0o700)
    settings = private / "actions-secrets.json"
    keystore = private / "itom-signing.jks"
    if settings.exists():
        values = json.loads(settings.read_text())
        if set(values) != set(NAMES):
            raise SystemExit("Existing private settings are incomplete; preserved without changes")
    else:
        if keystore.exists():
            raise SystemExit("Existing keystore has no private settings; refusing to overwrite it")
        store_password, key_password = secrets.token_urlsafe(36), secrets.token_urlsafe(36)
        env = dict(os.environ, ITOM_NEW_STORE_PASS=store_password, ITOM_NEW_KEY_PASS=key_password)
        subprocess.run(["keytool", "-genkeypair", "-keystore", str(keystore), "-storetype", "JKS",
                        "-storepass:env", "ITOM_NEW_STORE_PASS", "-keypass:env", "ITOM_NEW_KEY_PASS",
                        "-alias", "itom", "-keyalg", "RSA", "-keysize", "3072", "-sigalg", "SHA256withRSA",
                        "-validity", "10000", "-dname", "CN=ITOM Android,O=ITOM", "-noprompt"],
                       env=env, capture_output=True, check=True)
        keystore.chmod(0o600)
        values = dict(zip(NAMES, (base64.b64encode(keystore.read_bytes()).decode(), store_password, "itom", key_password)))
        fd = os.open(settings, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
        with os.fdopen(fd, "w") as f:
            json.dump(values, f)
    for name in NAMES:
        set_secret(args.repo, name, values[name])
        print("Configured " + name)
    print("Signing configured. Keep the private directory backed up; never upload it as a public file/artifact.")
    print("A new key does not match an old temporary debug certificate. Keep RC12 Android for the RC13 server test.")


if __name__ == "__main__":
    try:
        main()
    except (subprocess.CalledProcessError, FileNotFoundError):
        raise SystemExit("Required command failed. Private files are preserved; credentials are not printed. If setup was partial, inspect secret NAMES before resuming.")
