"""Shared rich Console singleton.

A standalone module so any cli/* submodule can `from .console import console`
without having to import from cli/__init__.py (which would create a
circular-import hazard during package initialization).
"""

from rich.console import Console

console = Console()
