import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).parent.parent / "src"))
from cli import merge_config


def test_merge_config_lists_dedup_and_override_scalars():
    user = {
        "packages": ["sqlite", "postgresql"],
        "mounts": ["~/code/shared:/ctx/shared"],
        "network": {"mode": "bridge"},
        "security": {"blocked_tools": ["wget", {"name": "grep"}]},
    }
    workspace = {
        "packages": ["postgresql", "redis"],
        "mounts": ["~/code/extra:/ctx/extra"],
        "network": {"mode": "host"},
        "security": {"blocked_tools": [{"name": "grep"}, "curl"]},
    }

    merged = merge_config(user, workspace)

    assert merged["packages"] == ["sqlite", "postgresql", "redis"]
    assert merged["mounts"] == ["~/code/shared:/ctx/shared", "~/code/extra:/ctx/extra"]
    assert merged["network"]["mode"] == "host"
    assert merged["security"]["blocked_tools"] == ["wget", {"name": "grep"}, "curl"]
