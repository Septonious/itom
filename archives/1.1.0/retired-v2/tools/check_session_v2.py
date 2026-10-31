#!/usr/bin/env python3
"""Independent source checks, separate from performance and field qualification."""
from pathlib import Path
import argparse
import subprocess


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--go', default='go', help='path to the pinned Go toolchain')
    args = parser.parse_args()
    root = Path(__file__).resolve().parents[1]
    for module in ('core', 'native'):
        subprocess.run([args.go, 'test', '-race', './...'], cwd=root / module, check=True)
        subprocess.run([args.go, 'vet', './...'], cwd=root / module, check=True)
    print('SOURCE CHECKS PASSED. RC1 is experimental; full performance and field qualification remain incomplete.')


if __name__ == '__main__':
    main()
