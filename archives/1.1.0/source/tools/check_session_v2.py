#!/usr/bin/env python3
"""Independent source checks. This command cannot qualify or package a release."""
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
    print('SOURCE CHECKS PASSED. Full transport qualification is incomplete; packaging remains disabled.')


if __name__ == '__main__':
    main()
