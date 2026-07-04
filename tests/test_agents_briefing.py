"""Generated agent-briefing content: minimal by design.

The briefing carries only what an agent cannot discover natively, and
conditional sections appear only when their data exists (enabled
loopholes, configured resource limits, a failed provisioning run, a
yolo-jail dev workspace).
"""

import sys
from pathlib import Path

REPO_ROOT = Path(__file__).parent.parent.resolve()

sys.path.insert(0, str(REPO_ROOT / "src"))


def _generate(tmp_path, monkeypatch, workspace=None, **kwargs):
    from cli import generate_agents_md

    monkeypatch.setattr("cli.agents_md.AGENTS_DIR", tmp_path / "agents")
    # Hermetic HOME: the real ~/.claude/CLAUDE.md gets prepended to the
    # briefing and would leak outer-jail text into the assertions.
    (tmp_path / "home").mkdir(exist_ok=True)
    monkeypatch.setenv("HOME", str(tmp_path / "home"))
    return generate_agents_md(
        "yolo-test", workspace or tmp_path / "workspace", [], [], **kwargs
    )


class TestProvisioningFailureSection:
    """Mentioned only when the last boot actually failed — never as a
    generic 'go check the log' instruction."""

    def test_absent_without_a_log(self, tmp_path, monkeypatch):
        content = (_generate(tmp_path, monkeypatch) / "CLAUDE.md").read_text()
        assert "Provisioning failed" not in content
        assert "startup.log" not in content

    def test_absent_when_log_is_clean(self, tmp_path, monkeypatch):
        ws = tmp_path / "workspace"
        (ws / ".yolo").mkdir(parents=True)
        (ws / ".yolo" / "startup.log").write_text("=== yolo provisioning ===\nok\n")
        content = (
            _generate(tmp_path, monkeypatch, workspace=ws) / "CLAUDE.md"
        ).read_text()
        assert "Provisioning failed" not in content

    def test_present_when_last_boot_failed(self, tmp_path, monkeypatch):
        ws = tmp_path / "workspace"
        (ws / ".yolo").mkdir(parents=True)
        (ws / ".yolo" / "startup.log").write_text(
            "  ↳ mise install\nPROVISIONING FAILED (exit 1)\n"
        )
        for name in ("AGENTS-copilot.md", "AGENTS-gemini.md", "CLAUDE.md"):
            content = (
                _generate(tmp_path, monkeypatch, workspace=ws) / name
            ).read_text()
            assert "## ⚠ Provisioning failed" in content, name
            assert "/workspace/.yolo/startup.log" in content, name
            assert "mise install" in content, name


class TestConditionalSections:
    def test_loopholes_listed_only_when_enabled(self, tmp_path, monkeypatch):
        plain = (_generate(tmp_path, monkeypatch) / "CLAUDE.md").read_text()
        assert "Loopholes" not in plain

        content = (
            _generate(
                tmp_path,
                monkeypatch,
                loopholes=[
                    ("audio", "PipeWire/PulseAudio socket pass-through. More prose."),
                    ("host-processes", ""),
                ],
            )
            / "CLAUDE.md"
        ).read_text()
        assert "## Loopholes" in content
        assert "- **audio**: PipeWire/PulseAudio socket pass-through" in content
        assert "More prose" not in content  # first sentence only
        assert "- **host-processes**" in content
        assert "yolo loopholes list" in content

    def test_resources_listed_only_when_set(self, tmp_path, monkeypatch):
        plain = (_generate(tmp_path, monkeypatch) / "CLAUDE.md").read_text()
        assert "Resource limits" not in plain
        assert "yolo-cglimit" not in plain

        content = (
            _generate(tmp_path, monkeypatch, resources={"memory": "8g", "cpus": 4})
            / "CLAUDE.md"
        ).read_text()
        assert "cpus=4, memory=8g" in content
        assert "yolo-cglimit" in content

    def test_dev_loop_section_only_for_yolo_source_workspaces(
        self, tmp_path, monkeypatch
    ):
        plain = (_generate(tmp_path, monkeypatch) / "CLAUDE.md").read_text()
        assert "Testing Changes to yolo-jail" not in plain

        ws = tmp_path / "yolo-src"
        (ws / "src" / "cli").mkdir(parents=True)
        (ws / "src" / "cli" / "__init__.py").touch()
        (ws / "pyproject.toml").write_text('[project]\nname = "yolo-jail"\n')
        dev = (_generate(tmp_path, monkeypatch, workspace=ws) / "CLAUDE.md").read_text()
        assert "Testing Changes to yolo-jail" in dev


class TestBriefingSlimness:
    """No inline manuals, no tool inventories, no agent-discoverable data."""

    def test_no_tool_inventory_or_inline_help(self, tmp_path, monkeypatch):
        content = (_generate(tmp_path, monkeypatch) / "CLAUDE.md").read_text()
        assert "Standard CLI tools" not in content
        assert "MCP Servers:" not in content
        assert "First Session" not in content
        assert "PAGER" not in content
        assert "nixpkgs commit" not in content  # packages manual → yolo config-ref
        assert "nice -n" not in content  # resource manual → yolo-cglimit --help
        assert "yolo --help" in content
        assert "yolo config-ref" in content

    def test_config_edit_discipline_survives(self, tmp_path, monkeypatch):
        content = (_generate(tmp_path, monkeypatch) / "CLAUDE.md").read_text()
        assert "ALWAYS run `yolo check` after every config edit" in content

    def test_workspace_is_described_as_live_shared_dir(self, tmp_path, monkeypatch):
        """Agents routinely assume the host copy of /workspace needs a git
        pull to see jail-side edits (or vice versa). The briefing must say
        it's the same live bind-mounted directory, no sync step ever."""
        content = (_generate(tmp_path, monkeypatch) / "CLAUDE.md").read_text()
        assert "bind-mounted LIVE" in content
        assert "not a copy" in content
        assert "never a git" in content

    def test_rg_replace_trap_warning_present(self, tmp_path, monkeypatch):
        """Agents habitually type grep's -rn into rg, where -r means
        --replace and corrupts search output (14 observed misfires) —
        the briefing must warn, since we deliberately don't shim rg."""
        content = (_generate(tmp_path, monkeypatch) / "CLAUDE.md").read_text()
        assert "rg is recursive by default" in content
        assert "--replace" in content

    def test_skills_section_is_the_readonly_constraint_only(
        self, tmp_path, monkeypatch
    ):
        content = (_generate(tmp_path, monkeypatch) / "CLAUDE.md").read_text()
        assert "## Skills" in content
        assert "read-only" in content
        assert "promote" in content
