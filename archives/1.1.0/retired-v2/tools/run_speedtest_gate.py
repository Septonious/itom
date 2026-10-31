#!/usr/bin/env python3
"""Compatibility entry point for the strict 0.9.2-Rev1 transport laboratory gate.

Use --streams single|multi|both. SINGLE means exactly one bulk TCP stream per
 direction. A Go PASS is laboratory evidence; real-provider/device qualification
 remains a separate required measurement.
"""
from transport_qualification import main
if __name__=='__main__':raise SystemExit(main())
