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
