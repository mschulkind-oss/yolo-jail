"""Registry of the coding agents yolo-jail can install into a jail.

This is the single source of truth that replaced the copilot/gemini/claude
triple that used to be hardcoded across ~5 subsystems (launcher install,
mise retirement, bashrc aliases, per-agent config writers, briefing +
skills staging, container overlays/mounts, and yolo-flag injection).

The module is **stdlib-only on purpose**.  ``src/entrypoint`` is baked into
the jail image as a standalone top-level package ``entrypoint`` (see
``flake.nix`` — only ``src/entrypoint/`` is copied and it runs as
``python3 -m entrypoint``), so anything it imports must resolve there too.
It also cannot import ``src.*``.  Host-side callers in ``src/cli`` import
this via a small dual-try shim (``from src.entrypoint.agent_registry …``
falling back to ``from entrypoint.agent_registry …``) because the test
suite imports the CLI under both the ``cli`` and ``src.cli`` identities.

The registry carries only data.  ``config_writer`` is the *name* of the
function in :mod:`entrypoint.agent_configs` (a string, not a callable) so
this module never imports the subprocess-heavy config code — the mapping
from name to callable is built locally in ``agent_configs``.
"""

from dataclasses import dataclass, field
from typing import Dict, List, Optional


@dataclass(frozen=True)
class InstallSpec:
    """How an agent CLI is installed and updated inside the jail.

    ``kind`` is ``"npm"`` (``npm install -g <package>``, binary lands at
    ``$NPM_CONFIG_PREFIX/bin/<bin>``) or ``"native"`` (a ``curl | bash``
    installer script, binary at ``real_bin``).  The lazy-update launchers
    in :func:`entrypoint.shims.generate_agent_launchers` build the right
    launcher body from ``kind``.
    """

    kind: str  # "npm" | "native"
    bin: str  # binary name on PATH (also the launcher/shim filename)
    package: Optional[str] = None  # npm package name (kind == "npm")
    install_flags: List[str] = field(default_factory=list)  # extra npm flags
    installer_url: Optional[str] = None  # curl installer (kind == "native")


@dataclass(frozen=True)
class BriefingSpec:
    """Where an agent's AGENTS.md/CLAUDE.md briefing is staged and mounted.

    ``staging`` is the filename written under the per-jail staging dir
    (``AGENTS_DIR/<cname>/``); ``mount`` is the HOME-relative path the
    agent actually reads inside the jail; ``host_source`` is the
    HOME-relative host file whose content is prepended to the
    jail-managed briefing (skipped when absent).
    """

    staging: str
    mount: str
    host_source: str


@dataclass(frozen=True)
class AgentSpec:
    name: str
    install: InstallSpec
    config_writer: str  # function name in entrypoint.agent_configs
    briefing: BriefingSpec
    # HOME-relative writable-overlay dirs this agent needs *beyond* the
    # dirs yolo-jail already overlays for everyone (.config/.local/.cache).
    overlay_dirs: List[str] = field(default_factory=list)
    # HOME-relative user-skills dir, or None if the agent has no such dir.
    # The staging dir is always ``skills-<name>``.
    skills: Optional[str] = None
    # Flags injected on the leading binary for ``yolo -- <bin>`` (YOLO mode).
    yolo_flags: List[str] = field(default_factory=list)
    # bashrc alias RHS (``alias <bin>='<rhs>'``), or None for no alias.
    alias: Optional[str] = None
    # mise tool-key token(s) to strip from mise config / uninstall so a
    # stale mise-cached copy never shadows the launcher-installed binary.
    # The tokens are kept verbatim (quoted for npm-style keys) to match
    # the pre-registry behavior in entrypoint.mise.
    mise_retire: List[str] = field(default_factory=list)

    @property
    def skills_staging(self) -> Optional[str]:
        """Staging dir name for this agent's skills, or None."""
        return f"skills-{self.name}" if self.skills else None


# ``--yolo`` and ``-y`` are the same switch (gemini); the injector must not
# add ``--yolo`` when the user already passed ``-y``.  Map a canonical flag
# to its accepted aliases so the dedup stays data-driven.
YOLO_FLAG_ALIASES: Dict[str, List[str]] = {"--yolo": ["-y"]}


_SPECS = [
    AgentSpec(
        name="claude",
        install=InstallSpec(
            kind="native",
            bin="claude",
            installer_url="https://claude.ai/install.sh",
        ),
        config_writer="configure_claude",
        briefing=BriefingSpec(
            staging="CLAUDE.md",
            mount=".claude/CLAUDE.md",
            host_source=".claude/CLAUDE.md",
        ),
        overlay_dirs=[".claude"],
        skills=".claude/skills",
        yolo_flags=["--dangerously-skip-permissions"],
        alias=None,  # flags injected via _inject_agent_yolo_flags, not an alias
        mise_retire=['"npm:@anthropic-ai/claude-code"'],
    ),
    AgentSpec(
        name="copilot",
        install=InstallSpec(kind="npm", bin="copilot", package="@github/copilot"),
        config_writer="configure_copilot",
        briefing=BriefingSpec(
            staging="AGENTS-copilot.md",
            mount=".copilot/AGENTS.md",
            host_source=".copilot/AGENTS.md",
        ),
        overlay_dirs=[".copilot"],
        skills=".copilot/skills",
        yolo_flags=["--yolo", "--no-auto-update"],
        alias="copilot --yolo --no-auto-update",
        mise_retire=['"npm:@github/copilot"'],
    ),
    AgentSpec(
        name="gemini",
        install=InstallSpec(kind="npm", bin="gemini", package="@google/gemini-cli"),
        config_writer="configure_gemini",
        briefing=BriefingSpec(
            staging="AGENTS-gemini.md",
            mount=".gemini/AGENTS.md",
            host_source=".gemini/AGENTS.md",
        ),
        overlay_dirs=[".gemini"],
        skills=".gemini/skills",
        yolo_flags=["--yolo"],
        alias="gemini --yolo",
        mise_retire=["gemini"],
    ),
    AgentSpec(
        name="opencode",
        install=InstallSpec(kind="npm", bin="opencode", package="opencode-ai"),
        config_writer="configure_opencode",
        # opencode's global config + rules live under ~/.config/opencode/,
        # and its auth under ~/.local/share/opencode/ — both already sit in
        # writable overlays, so opencode needs no extra overlay dir.
        briefing=BriefingSpec(
            staging="AGENTS-opencode.md",
            mount=".config/opencode/AGENTS.md",
            host_source=".config/opencode/AGENTS.md",
        ),
        overlay_dirs=[],
        skills=None,
        yolo_flags=[],  # auto-approve is written into opencode.json (permission: allow)
        alias=None,
        mise_retire=[],
    ),
    AgentSpec(
        name="pi",
        install=InstallSpec(
            kind="npm",
            bin="pi",
            package="@earendil-works/pi-coding-agent",
            # pi's own docs recommend --ignore-scripts (no install scripts needed).
            install_flags=["--ignore-scripts"],
        ),
        config_writer="configure_pi",
        briefing=BriefingSpec(
            staging="AGENTS-pi.md",
            mount=".pi/agent/AGENTS.md",
            host_source=".pi/agent/AGENTS.md",
        ),
        overlay_dirs=[".pi"],
        skills=None,
        yolo_flags=[],  # auto-approve via settings.json defaultProjectTrust: always
        alias=None,
        mise_retire=[],
    ),
    AgentSpec(
        name="codex",
        install=InstallSpec(kind="npm", bin="codex", package="@openai/codex"),
        config_writer="configure_codex",
        briefing=BriefingSpec(
            staging="AGENTS-codex.md",
            mount=".codex/AGENTS.md",
            host_source=".codex/AGENTS.md",
        ),
        overlay_dirs=[".codex"],
        skills=None,
        # --dangerously-bypass-approvals-and-sandbox disables BOTH Codex's
        # approval prompts and its own OS sandbox — the right choice since the
        # jail container is the security boundary and we don't want Codex
        # double-sandboxing.  config.toml (approval_policy/sandbox_mode) is
        # written too as belt-and-suspenders, mirroring claude's flag+settings.
        yolo_flags=["--dangerously-bypass-approvals-and-sandbox"],
        alias=None,
        mise_retire=[],
    ),
]

AGENTS: Dict[str, AgentSpec] = {spec.name: spec for spec in _SPECS}

# Default agent set when a config omits ``agents``.  Claude only — the
# lean, always-available default; other agents are opt-in for performance.
DEFAULT_AGENTS: List[str] = ["claude"]

VALID_AGENTS = frozenset(AGENTS)

# Every agent's mise-retire tokens, unioned.  ALL known agents stay retired
# from mise regardless of which are selected — a deselected agent must also
# never be mise-managed (a stale mise copy would shadow the launcher's).
ALL_MISE_RETIRE: List[str] = [token for spec in _SPECS for token in spec.mise_retire]

# Union of every known agent's overlay dirs — the base :ro mountpoints that
# ``ensure_global_storage`` must pre-create so a per-run selection change
# never targets a missing mountpoint.
ALL_OVERLAY_DIRS: List[str] = sorted({d for spec in _SPECS for d in spec.overlay_dirs})


def resolve_agents(names: Optional[List[str]]) -> List[AgentSpec]:
    """Return the :class:`AgentSpec` list for ``names`` (unknown names skipped).

    Order follows ``names``; used by every consumer to iterate the selected
    subset.  ``None`` falls back to :data:`DEFAULT_AGENTS`.
    """
    if names is None:
        names = DEFAULT_AGENTS
    return [AGENTS[n] for n in names if n in AGENTS]
