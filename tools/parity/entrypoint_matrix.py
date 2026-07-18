#!/usr/bin/env python3
"""Loads the committed Stage 9 env matrix (entrypoint_matrix.json).

The matrix is stored as JSON so BOTH the Python oracle (entrypoint_oracle.py)
and the Go parity test (internal/entrypoint/entrypoint_parity_test.go) read the
exact same scenarios from one source of truth. This module just exposes it as
``SCENARIOS`` (name -> spec) with the shared ``home_token`` folded into each
spec so the oracle doesn't need to special-case it.
"""

from __future__ import annotations

import json
from pathlib import Path

_MATRIX_PATH = Path(__file__).resolve().parent / "entrypoint_matrix.json"
_doc = json.loads(_MATRIX_PATH.read_text())
_home_token = _doc.get("home_token", "@HOME@")

SCENARIOS: dict[str, dict] = {}
for _name, _spec in _doc["scenarios"].items():
    _spec = dict(_spec)
    _spec.setdefault("home_token", _home_token)
    SCENARIOS[_name] = _spec
