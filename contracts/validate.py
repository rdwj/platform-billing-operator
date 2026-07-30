#!/usr/bin/env python3
"""Validate usage.v1 example events against the schema.

Usage:  python3 validate.py            # validates everything under examples/
        python3 validate.py f1.json…   # validates specific files

Requires: pip install jsonschema  (any 4.x)
Exit code 0 = all files behave as expected (files named invalid-* MUST fail).
Wire this into CI as the contract gate: the schema is the law; collectors and
ledger-svc both test against these same fixtures.
"""
import json
import pathlib
import sys

try:
    from jsonschema import Draft202012Validator
except ImportError:
    sys.exit("jsonschema not installed: pip install jsonschema")

HERE = pathlib.Path(__file__).parent
schema = json.loads((HERE / "usage.v1.schema.json").read_text())
Draft202012Validator.check_schema(schema)
validator = Draft202012Validator(schema)

files = [pathlib.Path(a) for a in sys.argv[1:]] or sorted((HERE / "examples").glob("*.json"))
failures = 0
for f in files:
    errors = list(validator.iter_errors(json.loads(f.read_text())))
    expect_invalid = f.name.startswith("invalid-")
    ok = bool(errors) == expect_invalid
    tag = "PASS" if ok else "FAIL"
    detail = f" ({len(errors)} schema errors)" if errors else ""
    print(f"[{tag}] {f.name}{detail}")
    if not ok:
        failures += 1
        for e in errors[:3]:
            print(f"       - {'/'.join(map(str, e.path)) or '<root>'}: {e.message}")
sys.exit(1 if failures else 0)
