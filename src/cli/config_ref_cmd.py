"""``yolo config-ref`` — print the long-form configuration reference.

Mostly a single big rich-formatted string.  The Typer command is
registered in cli/__init__.py; this module just exports the function.
"""

from .console import console


def config_ref():
    """Show the full YOLO Jail configuration reference."""
    console.print("""[bold]YOLO Jail Configuration Reference[/bold]

[bold cyan]CONFIG FILE: yolo-jail.jsonc[/bold cyan]

  Location: Project root (per-workspace)
  Format:   JSON with comments (JSONC)
  User defaults: ~/.config/yolo-jail/config.jsonc

  Workspace config merges over user defaults.
  Lists are merged and deduplicated. Scalars override.

  [bold yellow]Rule:[/bold yellow] After [bold]EVERY[/bold] edit to `yolo-jail.jsonc` or
  `~/.config/yolo-jail/config.jsonc`, run `yolo check` before restarting or
  asking a human to restart the jail. Use `yolo check --no-build` inside a
  running jail for a faster preflight.

[bold cyan]FIELDS[/bold cyan]

  [bold]runtime[/bold] (string): Container runtime.
    Values: "podman" or "container"
    Override: YOLO_RUNTIME env var takes priority.
    Auto-detect: macOS prefers "container" then "podman"; Linux uses "podman".

  [bold]packages[/bold] (array): Extra nix packages baked into the image.
    Supports three formats:
    • String: package name from nixpkgs (latest from flake's pin)
      Example: "postgresql"
    • Object with nixpkgs: pinned to a specific nixpkgs commit
      Example: {"name": "freetype", "nixpkgs": "<commit-hash>"}
    • Object with version override: build from upstream source
      Uses the existing nix build recipe but swaps version+source.
      Example: {"name": "freetype", "version": "2.14.1",
                "url": "mirror://savannah/freetype/freetype-2.14.1.tar.xz",
                "hash": "sha256-MkJ+jEcawJWFMhKjeu+BbGC0IFLU2eSCMLqzvfKTbMw="}
      Get the hash: nix-prefetch-url <url>  (then convert with nix hash)
      Or set hash to "" and nix will tell you the correct one on build failure.
    Find nixpkgs commits per version: https://lazamar.co.uk/nix-versions/
    Search package names: https://search.nixos.org/packages
    Image rebuilds only when this list changes.
    Nix caches builds — identical configs across jails share cached results.

  [bold]host_claude_files[/bold] (array of strings): Host ~/.claude/ files to sync into the jail.
    Each entry is a filename (not a path) relative to ~/.claude/.
    Files are mounted read-only at /ctx/host-claude/ and copied into the jail's
    ~/.claude/ on startup. For settings.json, host settings are deep-merged with
    YOLO-required overrides (YOLO wins on conflicts).
    The fileSuggestion script referenced in host settings.json is auto-discovered
    and synced (if it lives under ~/.claude/) — no need to list it explicitly.
    Default: ["settings.json"]
    Set to [] to disable host claude file syncing.
    Example: ["settings.json", "keybindings.json"]

  [bold]host_services[/bold] (object): Host-side services exposed inside the jail via Unix sockets.
    Each key is a service name (must match ^[a-zA-Z][a-zA-Z0-9_-]{0,63}$).
    The name "cgroup-delegate" is reserved for the built-in cgroup daemon.

    Each value is an object with:
      "command" (array of strings, required): the command to launch on the host
        when the jail starts.  "{socket}" in any arg is substituted with the
        actual host-side socket path the service should bind.
      "env" (object, optional): extra env vars for the host daemon (NOT the jail).
      "jail_socket" (string, optional): override the jail-side socket path.
        Must start with /run/yolo-services/ and end in .sock.
        Default: /run/yolo-services/<name>.sock

    Each service gets:
      • Its socket bind-mounted into the jail at /run/yolo-services/<name>.sock
      • An env var YOLO_SERVICE_<NAME>_SOCKET injected into the container so
        agents can locate the socket without hard-coding paths.
      • A managed lifecycle: started before container start, SIGTERM + 5s grace +
        SIGKILL after the container exits.
      • stdout/stderr captured to ~/.local/share/yolo-jail/logs/host-service-<name>.log

    Use this to split the jail boundary cleanly: a host-side process can hold
    secrets, credentials, and access-control logic that the agent inside the
    jail can call but never sees.  See docs/USER_GUIDE.md § Host Services for
    a complete example.

    Example:
      "loopholes": {
        "auth-broker": {
          "command": ["~/code/auth-broker/serve.py", "--socket", "{socket}"],
          "env": {"KEYS_FILE": "~/secrets/keys.json"}
        }
      }

    Apple Container is unsupported (no Unix-socket bind-mount through virtiofs).

  [bold]journal[/bold] (string, default "off"): Enable the built-in journal bridge.
    Exposes [bold]yolo-journalctl[/bold] inside the jail, which forwards its args to
    [cyan]journalctl[/cyan] running on the host and streams stdout/stderr back to the
    terminal.  Useful when an agent needs to inspect systemd logs (e.g.
    the Claude token refresher's own output) without mounting the host
    journal rw into the jail.
    Values:
      • "off"  (default) — no daemon, no shim
      • "user" — daemon forces [cyan]--user[/cyan] on every invocation (recommended)
      • "full" — args pass through unchanged (requires host journal read access)
    Socket: /run/yolo-services/journal.sock
    Env var: YOLO_SERVICE_JOURNAL_SOCKET
    "journal" is reserved as a host_services name — you cannot shadow it.

  [bold]env_sources[/bold] (array): Environment variables set inside the jail.
    Ordered list; each entry is either:
      • a string — path to a KEY=VALUE dotenv file (# comments allowed,
        quoted values OK, `export` prefix tolerated)
      • an object — inline {"KEY": "VALUE"} map
    Later entries override earlier ones.  User-config list loads first,
    then workspace-config list.  File paths support ~ expansion,
    absolute paths, and workspace-relative paths.  Missing files warn
    and skip rather than failing the run — keep secrets in an unsynced
    file outside your dotfiles tree.
    Written to ~/.config/yolo-user-env.sh (sourced by .bashrc and entrypoint);
    can be overridden by mise .env or by editing that file inside the jail.
    Example: [
      "~/.config/yolo-jail/defaults.env",
      {"DEBUG": "1"},
      ".secrets/claude.env"
    ]

  [bold]mounts[/bold] (array of strings): Extra host paths mounted read-only.
    Simple path → mounted at /ctx/<basename>
    "host:container" → custom container path
    Example: ["/path/to/repo", "~/lib:/ctx/lib"]

  [bold]workspace_readonly[/bold] (array of strings): Workspace sub-paths to overlay as read-only.
    Each entry is a relative path inside the workspace (no leading /, no ..).
    Mounted on top of the writable /workspace volume so agents cannot modify
    those paths. Use this to protect host-executed code that lives in the
    workspace repo (e.g. the yolo-jail src/ directory itself).
    Example: ["src", "flake.nix", "Justfile"]

  [bold]network.mode[/bold] (string): Network isolation mode.
    "bridge" (default): Isolated. Use network.ports for access.
    "host": Share host network stack (localhost works directly).

  [bold]network.ports[/bold] (array of strings): Port mappings in bridge mode.
    Format: "host_port:container_port"
    Example: ["8000:8000", "3000:3000"]
    Makes container services reachable from the host.

  [bold]network.forward_host_ports[/bold] (array): Forward host ports into the jail.
    Makes host services appear on localhost inside the container, even if the
    host service only listens on 127.0.0.1 (like SSH -L port forwarding).
    Integer: same port on both sides (e.g., 5432)
    String "local:host": remap ports (e.g., "5432:3306")
    Example: [5432, 6379, "8080:9090"]
    Uses socat via Unix sockets; only active in bridge mode.
    Requires socat installed on the host.

  [bold]security.blocked_tools[/bold] (array): Tools to block inside the jail.
    Simple: ["curl", "wget"]
    Detailed: [{"name": "grep", "message": "Use rg", "suggestion": "rg <pattern>"}]
    Default: grep and find are blocked (rg/fd suggested instead).
      • grep is conditionally blocked — only recursive invocations
        (``-r``, ``-R``, ``--recursive``, or short-flag bundles like
        ``-rn``).  Pipe filters and single-file greps pass through.
      • find is unconditionally blocked.
    Conditional: add ``block_flags`` (array of shell case-glob patterns)
    to block only when argv contains a matching flag.  Absence means
    "always block" (find's default behavior).  Long options in
    block_flags match exactly; short patterns (starting with ``-``)
    match after any non-matching ``--*`` arg is skipped, so patterns
    like ``-*[rR]*`` catch ``-rn`` / ``-Rn`` without false-positive-ing
    ``--regex``.
    Example:
      "security": {
        "blocked_tools": [
          {
            "name": "grep",
            "message": "grep -r blocked; use rg",
            "suggestion": "rg <pattern>",
            "block_flags": ["-r", "-R", "--recursive", "-*[rR]*"]
          }
        ]
      }
    Bypass: Set YOLO_BYPASS_SHIMS=1 in scripts that need blocked tools.

  [bold]mise_tools[/bold] (object): Extra tools installed via mise in the jail.
    Keys are mise tool names, values are version strings.
    Default: {"neovim": "stable"}
    These are injected into the jail's global mise config (not workspace mise.toml).
    Deep-merged: user config adds tools, workspace config overrides versions.
    Example: {"neovim": "nightly", "typst": "latest"}

  [bold]lsp_servers[/bold] (object): Additional language servers for Copilot and Gemini (Claude uses its own tools).
    Default servers (always present): python (pyright), typescript, go (gopls).
    Workspace servers are merged with defaults — add new ones or override existing.
    Each key is a server name; value is an object with:
      • command (string, required): Binary name (on PATH) or absolute path.
      • args (array of strings): Args passed to the LSP binary. Default: [].
      • fileExtensions (object): Extension → language ID map (required for Copilot).
    The entrypoint translates these for each agent:
      • Copilot: written to ~/.copilot/lsp-config.json as native LSP servers.
      • Gemini: wrapped via mcp-language-server as MCP servers in settings.json.
    Example: {"rust": {"command": "rust-analyzer", "args": [],
              "fileExtensions": {".rs": "rust"}}}

  [bold]mcp_presets[/bold] (array of strings): Enable built-in MCP server presets by name.
    No presets are enabled by default. Available presets:
      • chrome-devtools: Headless Chromium automation via Chrome DevTools Protocol.
      • sequential-thinking: Chain-of-thought reasoning via MCP.
    Invalid: enabling a preset here and null-removing it in the same config file.
    Example: ["chrome-devtools", "sequential-thinking"]

  [bold]mcp_servers[/bold] (object): Custom MCP servers for Copilot, Gemini, and Claude.
    Add custom servers, or set a preset/inherited server to [bold]null[/bold] to disable it.
    Each key is a server name; value is an object with:
      • command (string, required): Binary name (on PATH) or absolute path.
      • args (array of strings): Args passed to the MCP server. Default: [].
      • env (object of string→string): Environment variables passed only to
        this MCP server's process. Use for per-server secrets and config so
        they don't leak into the jail-wide env. Default: {}.
        [bold]${VAR}[/bold] references are expanded against the jail's startup env
        (after [bold]env_sources[/bold] is loaded), so a secret can live in one
        unsynced file and be scoped to a single server. Undefined names
        are left as the literal [bold]${VAR}[/bold] and logged as a warning.
    The entrypoint translates these for each agent:
      • Copilot: written to a per-workspace overlay mounted at ~/.copilot/mcp-config.json.
      • Gemini: written to a per-workspace overlay mounted at ~/.gemini/settings.json.
      • Claude: written to a per-workspace overlay mounted at ~/.claude/settings.json.
    Example:
      {"cerebras-mcp": {
        "command": "npx", "args": ["-y", "cerebras-code-mcp"],
        "env": {"CEREBRAS_API_KEY": "csk-...", "CEREBRAS_MCP_IDE": "claude"}
      }}

  [bold]devices[/bold] (array): Host devices to pass through to the jail.
    Three formats supported:
    • USB by vendor:product ID (preferred — stable across reboots):
      {"usb": "0bda:2838", "description": "RTL-SDR Blog V4"}
      Resolved to /dev/bus/usb/... at startup via lsusb.
    • Raw device path (fragile — changes on replug):
      "/dev/bus/usb/001/004"
    • Cgroup rule (broad access):
      {"cgroup_rule": "c 189:* rwm"}
      Grants access to all devices matching the major number.
    Missing devices produce a warning, not an error — the jail still starts.
    Subject to config change safety (human approval required).

  [bold]gpu[/bold] (object): NVIDIA GPU passthrough configuration.
    Requires NVIDIA Container Toolkit on the host (podman + CDI).
    • [bold]enabled[/bold] (bool): Enable GPU passthrough. Default: false.
      If true but the host lacks drivers/CDI (e.g. laptop without an
      NVIDIA GPU), yolo prints a one-line warning and starts without
      GPU passthrough — so the same config can be committed and used
      on both a GPU box and a GPU-less machine.
    • [bold]devices[/bold] (string): Which GPUs to expose. Default: "all".
      Values: "all", "0", "0,1", or "GPU-<uuid>".  Mapped to CDI
      device entries (nvidia.com/gpu=...).
    • [bold]capabilities[/bold] (string): NVIDIA driver capabilities. Default: "compute,utility".
      Valid: compute, utility, graphics, video, display, compat32.
      "compute,utility" is sufficient for PyTorch/CUDA training.

    Host prerequisites (on the GPU machine):
      1. NVIDIA driver installed (nvidia-smi works)
      2. nvidia-container-toolkit installed
      3. sudo nvidia-ctk cdi generate --output=/etc/cdi/nvidia.yaml
    Run [bold]yolo check[/bold] to verify GPU readiness on a given host.
    Subject to config change safety (human approval required).

  [bold]kvm[/bold] (boolean, default false): Expose /dev/kvm inside the jail.
    When true, yolo adds [cyan]--device /dev/kvm[/cyan] to the container run
    command plus [cyan]--group-add keep-groups[/cyan] so the in-jail user
    inherits the host's kvm-group membership (podman).
    Enables nested hardware-accelerated VMs inside the jail (QEMU,
    firecracker, Android emulator, kernel dev workflows).  Runs full-speed
    virtualization via KVM instead of falling back to software emulation.
    Host prerequisites (verified by [bold]yolo check[/bold] when enabled):
      1. CPU virtualization extensions enabled in firmware (VT-x / AMD-V)
      2. kvm kernel module loaded ([cyan]modprobe kvm_intel[/cyan] or [cyan]kvm_amd[/cyan])
      3. Your host user is a member of the kvm group
    Not supported on macOS (Apple hosts use the VZ framework) or on the
    Apple Container runtime (no device passthrough).  Skipped with a warn
    when /dev/kvm is absent on a Linux host.
    [yellow]Security note:[/yellow] /dev/kvm is a kernel hypervisor interface.
    The attack surface is narrow — historical CVEs have mostly been
    guest-to-host escape bugs requiring attacker code in a KVM guest —
    but it is strictly larger than no-kvm.  Leave this off unless you
    actually need nested virtualization.

  [bold]resources[/bold] (object): Container resource limits.
    Sets hard cgroup constraints on the jail container via podman flags.
    These limits are enforced by the kernel — the jail cannot exceed them.
    • [bold]memory[/bold] (string): Maximum memory. Format: number + suffix (b/k/m/g).
      Examples: "8g" (8 GB), "512m" (512 MB), "2g".
      Maps to --memory flag. OOM-killed if exceeded.
    • [bold]cpus[/bold] (number|string): CPU limit as a decimal. Default: no limit.
      Examples: 4 (four cores), 2.5 (two and a half cores), "0.5" (half a core).
      Maps to --cpus flag (CFS quota).
    • [bold]pids_limit[/bold] (integer): Maximum number of processes. Default: 32768 (Podman's built-in default of 2048 is too low for agent workloads).
      Prevents fork bombs and runaway process creation.
      Maps to --pids-limit flag.

    [bold]In-jail sub-process limits (cgroup v2 delegation)[/bold]:
    A host-side cgroup delegate daemon runs alongside the container and
    performs all privileged cgroup operations on behalf of agents inside the
    jail.  No CAP_SYS_ADMIN or writable cgroup mount is needed inside the
    container — the daemon validates every request and operates securely on
    the host cgroup filesystem via a Unix socket.
    Use the [bold]yolo-cglimit[/bold] helper inside the jail:
      yolo-cglimit --cpu 75 -- python train.py           # 75% of all CPUs
      yolo-cglimit --cpu 50 --memory 2g -- make -j8      # 50% CPU + 2GB RAM
      yolo-cglimit --pids 100 -- ./script.sh             # Max 100 processes
    The daemon is started automatically by the yolo CLI.  Podman is the
    primary supported runtime.
    Falls back to nice/timeout/ulimit if delegation is unavailable.
    Subject to config change safety (human approval required).

  [bold]ephemeral_storage[/bold] (string, default "volume"): Backing for /tmp,
    /var/tmp, and /var/lib/containers inside the jail.
    The container rootfs is mounted [cyan]--read-only[/cyan], so these scratch
    paths need explicit writable mounts. Two modes:
      • "volume" (default) — anonymous podman volumes, disk-backed.
        Wiped automatically by [cyan]podman run --rm[/cyan] on container exit;
        doesn't compete with the jail's memory budget. Recommended for
        workloads that touch large temp files (builds, model downloads).
      • "tmpfs" — RAM-backed scratch. Faster reads/writes, but counts
        against host free memory and can OOM the jail under pressure.
    /run and /dev/shm always stay on tmpfs in either mode (small + shared
    memory, respectively). Apple Container always uses tmpfs (its volume
    syntax differs and isn't a drop-in replacement).

[bold cyan]EXAMPLE CONFIG[/bold cyan]

  {
    "runtime": "podman",
    "mise_tools": {"neovim": "nightly"},
    "mcp_presets": ["chrome-devtools"],
    "lsp_servers": {
      "rust": {"command": "rust-analyzer", "args": [],
               "fileExtensions": {".rs": "rust"}}
    },
    "packages": [
      "strace",
      "gtk4", "gtk4.dev",
      {"name": "gtk4-layer-shell", "outputs": ["out", "dev"]},
      {"name": "freetype", "nixpkgs": "e6f23dc0..."},
      {"name": "freetype", "version": "2.14.1",
       "url": "mirror://savannah/freetype/freetype-2.14.1.tar.xz",
       "hash": "sha256-MkJ+jEcawJWFMhKjeu+BbGC0IFLU2eSCMLqzvfKTbMw="}
    ],
    "env_sources": [{"DEBUG": "1"}, "~/.config/yolo-jail/secrets.env"],
    "mounts": ["/path/to/ref-repo"],
    "devices": [
      {"usb": "0bda:2838", "description": "RTL-SDR Blog V4"}
    ],
    "gpu": {
      "enabled": true,
      "devices": "all",
      "capabilities": "compute,utility"
    },
    "resources": {
      "memory": "8g",
      "cpus": 4,
      "pids_limit": 4096
    },
    "network": {
      "mode": "bridge",
      "ports": ["8000:8000"],
      "forward_host_ports": [5432]
    },
    "security": {
      "blocked_tools": [
        {"name": "grep", "message": "Use rg", "suggestion": "rg <pattern>"},
        "wget"
      ]
    }
  }

[bold cyan]ENVIRONMENT VARIABLES[/bold cyan]

  YOLO_RUNTIME          Override container runtime (podman/container)
  YOLO_BYPASS_SHIMS     Set to 1 to bypass blocked tool shims
  YOLO_EXTRA_PACKAGES   JSON array of extra nix packages (internal)

[bold cyan]CONFIG CHANGE SAFETY[/bold cyan]

  When yolo-jail.jsonc changes between jail startups, the CLI shows a
  diff of the normalized config and asks for y/N confirmation. This
  prevents agents from silently adding packages or mounts without the
  human operator noticing. Agents should still run `yolo check` after
  every config edit before asking for that restart.

  - First run: config is accepted and a snapshot saved.
  - Subsequent runs: changes require explicit y/N approval.
  - Non-interactive (piped input): accepted with a warning.

  Snapshot location: <workspace>/.yolo/config-snapshot.json

[bold cyan]AGENT PACKAGE WORKFLOW[/bold cyan]

  Agents inside the jail can request new packages:

  1. Agent edits /workspace/yolo-jail.jsonc, adds to "packages" array
  2. Agent ALWAYS runs `yolo check` after the edit (`--no-build` is okay inside a running jail)
  3. If the check passes, agent tells the human: "Please restart the jail for new packages"
  4. On next startup, human sees the config diff and approves (y/N)
  5. Image rebuilds with the new package
  6. Agent can use the package after restart

  This keeps the human in the loop for all environment changes.
  Do NOT install packages via apt, nix-env, or other package managers.

  [bold cyan]COMMANDS[/bold cyan]

  yolo                      Start interactive jail shell
  yolo -- <command>         Run a command inside the jail
  yolo --new -- <command>   Force a new container
  yolo check                Validate config and preflight the build
  yolo ps                   List running jail containers
  yolo init                 Create yolo-jail.jsonc in current directory
  yolo init-user-config     Create user-level defaults config
  yolo config-ref           Show this reference

[bold cyan]INSIDE THE JAIL[/bold cyan]

  [bold]Workspace[/bold]
    Your project is bind-mounted read-write at /workspace.
    Edits are visible on the host immediately — this is the SAME directory.
    The workspace path changes from the host path to /workspace.

  [bold]Networking[/bold]
    Full internet access is available. Bridge mode (default) isolates the
    container network but allows outbound connections. Use network.ports
    to publish container ports to the host. Host mode shares the host
    network stack directly.

  [bold]Home Directory (/home/agent)[/bold]
    A shared persistent home that is the SAME across ALL jail workspaces.
    Contains: auth tokens (gh, gemini, claude), tool caches, npm/go globals,
    nvim config, shell configs, mise tool data. All of this survives
    jail restarts and is shared between every project's jail.

  [bold]Per-Workspace State[/bold]
    Some state is isolated per-workspace (in <workspace>/.yolo/):
    SSH keys, bash history, copilot sessions, gemini history, claude projects.
    These are NOT shared across different project jails.

  [bold]Identity & Auth[/bold]
    Git/jj identity (name + email) is injected from the host automatically.
    GitHub CLI (gh) is pre-authenticated via the shared home.
    SSH keys are per-workspace — configure in <workspace>/.yolo/home/ssh/.

  [bold]Tools & Runtimes[/bold]
    Runtimes: Node.js 22, Python 3.13, Go (managed by mise)
    Editors:  nvim (version configurable via mise_tools config)
    CLI:      rg, fd, bat, jq, git, jj, gh, curl, strace, uv, tmux
    Agents:   copilot, gemini (--yolo auto-injected), claude (YOLO mode via settings.json)
    The 'yolo' command is available inside for nested jailing and help.

  [bold]Mise Tool Management[/bold]
    Mise manages all runtimes and supports thousands of tools from
    multiple registries:
    • aqua — pre-built binaries (kubectl, terraform, gh, etc.)
    • asdf — version-managed runtimes (python, node, ruby, etc.)
    • cargo — Rust crates (ripgrep, fd-find, bat, etc.)
    • go — Go modules (built from source)
    • npm — Node packages (installed globally)
    • pipx — Python CLI tools (isolated envs)
    • ubi — universal binary installer (GitHub releases)
    Run 'mise registry' to browse all available tools. Add tools via:
    • "mise_tools" in yolo-jail.jsonc (injected into jail global config)
    • /workspace/mise.toml (workspace-specific, checked into git)
    The host's mise data directory is shared with the jail, so tool
    installs are available in both environments.

  [bold]Blocked Tools[/bold]
    By default, grep is replaced by rg and find by fd. These are shims —
    set YOLO_BYPASS_SHIMS=1 in scripts that need the real commands.
    Configure via security.blocked_tools in yolo-jail.jsonc.

  [bold]Venvs & Python[/bold]
    The host's mise data directory is shared with the jail, so venvs
    created on the host resolve inside the jail (python binary paths
    match). The workspace path changes to /workspace though, so
    venv scripts with absolute shebangs may need fixing.

  [bold]Persistence Summary[/bold]
    Shared home:   /home/agent (same across all jails — auth, tools, caches)
    Workspace:     /workspace edits visible on host immediately
    Per-workspace: SSH keys, bash history, copilot/gemini sessions
    Ephemeral:     /tmp, container processes

[bold cyan]SPAWNING A NEW PROJECT[/bold cyan]

  When setting up a new project for jail use:

  1. Run 'yolo init' in the project root to create yolo-jail.jsonc
  2. Edit the config — add any nix packages or mise_tools needed
  3. Run 'yolo check' after EVERY config edit to validate the config before restarting
  4. Run 'yolo -- bash' to enter the jail interactively
  5. Start your agent: 'yolo -- copilot', 'yolo -- gemini', or 'yolo -- claude'

  [bold]For agents preparing to enter a jail:[/bold]
  Before asking the human to restart you inside the jail, ALWAYS run 'yolo check'
  and write a
  handoff document (e.g., scratch/jail-notes.md) with:
  • Current task state and what remains to be done
  • Decisions made and their rationale
  • Key files to examine first
  Your inner-jail self will be a fresh session without your context.
""")
