"""The generated agent briefings must point every agent at the startup log."""

import sys
from pathlib import Path

REPO_ROOT = Path(__file__).parent.parent.resolve()

sys.path.insert(0, str(REPO_ROOT / "src"))


class TestAgentsMdStartupLog:
    """generate_agents_md emits the Startup Log section for all three agents."""

    def _generate(self, tmp_path, monkeypatch):
        from cli import generate_agents_md

        # Patch the module-local constant actually used by generate_agents_md
        # (cli.AGENTS_DIR is a re-export; patching it alone leaves the real
        # directory as the write target).
        monkeypatch.setattr("cli.agents_md.AGENTS_DIR", tmp_path / "agents")

        return generate_agents_md(
            "yolo-test",
            tmp_path / "workspace",
            [],
            [],
            net_mode="bridge",
            runtime="podman",
        )

    def test_startup_log_section_in_all_agent_files(self, tmp_path, monkeypatch):
        result = self._generate(tmp_path, monkeypatch)
        for name in ("AGENTS-copilot.md", "AGENTS-gemini.md", "CLAUDE.md"):
            content = (result / name).read_text()
            assert "## Startup Log" in content, name
            assert "/workspace/.yolo/startup.log" in content, name

    def test_startup_log_section_names_failure_marker(self, tmp_path, monkeypatch):
        result = self._generate(tmp_path, monkeypatch)
        for name in ("AGENTS-copilot.md", "AGENTS-gemini.md", "CLAUDE.md"):
            content = (result / name).read_text()
            assert "PROVISIONING FAILED" in content, name
            assert "mise install" in content, name


class TestBriefingSlimness:
    """The briefing carries only what agents can't discover natively:
    no MCP listing (agents read their own config), no handover section
    (the jail-startup skill's description drives invocation), and the
    yolo-jail dev-loop section only in yolo-jail source workspaces."""

    def _generate(self, tmp_path, monkeypatch, workspace=None):
        from cli import generate_agents_md

        monkeypatch.setattr("cli.agents_md.AGENTS_DIR", tmp_path / "agents")
        # Hermetic HOME: the real ~/.claude/CLAUDE.md gets prepended to the
        # briefing and would leak outer-jail text into the assertions.
        (tmp_path / "home").mkdir(exist_ok=True)
        monkeypatch.setenv("HOME", str(tmp_path / "home"))
        return generate_agents_md(
            "yolo-test", workspace or tmp_path / "workspace", [], []
        )

    def test_no_mcp_listing_or_handover_section(self, tmp_path, monkeypatch):
        content = (self._generate(tmp_path, monkeypatch) / "CLAUDE.md").read_text()
        assert "MCP Servers:" not in content
        assert "First Session" not in content

    def test_rg_replace_trap_warning_present(self, tmp_path, monkeypatch):
        """Agents habitually type grep's -rn into rg, where -r means
        --replace and corrupts search output (14 observed misfires) —
        the briefing must warn, since we deliberately don't shim rg."""
        content = (self._generate(tmp_path, monkeypatch) / "CLAUDE.md").read_text()
        assert "rg is recursive by default" in content
        assert "--replace" in content

    def test_skills_section_is_the_readonly_constraint_only(
        self, tmp_path, monkeypatch
    ):
        content = (self._generate(tmp_path, monkeypatch) / "CLAUDE.md").read_text()
        assert "## Skills" in content
        assert "read-only" in content
        assert "promote" in content

    def test_dev_loop_section_only_for_yolo_source_workspaces(
        self, tmp_path, monkeypatch
    ):
        plain = (self._generate(tmp_path, monkeypatch) / "CLAUDE.md").read_text()
        assert "Testing Changes to yolo-jail" not in plain

        ws = tmp_path / "yolo-src"
        (ws / "src" / "cli").mkdir(parents=True)
        (ws / "src" / "cli" / "__init__.py").touch()
        (ws / "pyproject.toml").write_text('[project]\nname = "yolo-jail"\n')
        dev = (
            self._generate(tmp_path, monkeypatch, workspace=ws) / "CLAUDE.md"
        ).read_text()
        assert "Testing Changes to yolo-jail" in dev
