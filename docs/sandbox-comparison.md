# Sandbox Comparison: Claude Code Built-in vs. yolo-jail

A developer-experience comparison of the two sandboxing approaches available
when running Claude Code: the **built-in Claude Code sandbox** (bubblewrap on
Linux, Seatbelt on macOS) and the **yolo-jail** OCI container approach.

Both aim to let an AI agent work autonomously without destroying the host
machine. They are architecturally very different — and that difference comes
down to a single, consequential design choice about **where the default sits**.

---

## The Core Difference: Default-Deny at the Right Layer

Claude Code's sandbox and yolo-jail both claim to be "default deny." But they
default-deny *different things*, and that distinction has cascading consequences
for security and developer productivity.

**Claude Code sandbox default posture:**

- Default-deny specific *operations* (file writes outside the project, network
  requests to unlisted domains).
- Default-allow everything else on the host: all files are readable, all
  processes are visible, all host credentials (SSH keys, cloud tokens, cookies)
  are reachable unless you have explicitly named and denied each one.
- Every new tool, service, domain, or permission the agent needs requires the
  developer to audit it and add an allow rule.

This is the SELinux / AppArmor model applied to AI agents. It sounds secure
in theory. In practice, developers faced with an agent that can't reach
`api.openai.com` or write to `~/.kube` do what developers always do with
mandatory-access-control systems: they open everything up. Claude Code
explicitly acknowledges this in its own docs, naming "approval fatigue" as a
primary motivation for building the sandbox. But the sandbox doesn't fix the
fatigue — it just moves the auditing from per-command prompts to per-domain
allow rules. The outcome is the same: a long, brittle list of manual
exceptions that nobody fully understands.

**yolo-jail default posture:**

- Default-deny *host access* structurally — the agent runs in a container that
  has no visibility into the host at all.
- Default-allow everything *inside* the jail: the agent has unfettered
  privileges within the container — it can run as root, mount tmpfs volumes,
  start containers, bind ports, kill processes, and do anything a developer
  would do locally.
- What the agent can reach outside the jail is determined by *explicit
  plumbing* (loopholes, port publishes, bind mounts) — not by a deny list
  that must enumerate every sensitive path.

This inverts the auditing burden. Instead of "enumerate everything the agent
must not touch," you configure "enumerate exactly what the agent needs from the
host." That list is short, purpose-built, and doesn't grow as the agent learns
new tricks. The agent gets a full development environment with zero permission
prompts; the host gets structural isolation that requires no ongoing vigilance.

**The security argument:** Any system that generates permission prompts is
insecure in practice. The human reviewing "do you want Claude to access
`npmjs.org`?" at 3pm on a Tuesday after 47 previous approvals is not making
a security decision — they are clicking OK. Prompt-based security degrades
toward rubber-stamping. The Claude Code docs acknowledge this: it is literally
the stated motivation for building the sandbox. But the sandbox doesn't
eliminate approvals for anything outside its configured boundaries — and those
boundaries need constant extension as real projects evolve.

yolo-jail eliminates approvals for the entire development workflow by moving
the boundary to the container wall, where a one-time configuration decision
replaces an endless series of runtime decisions.

---

---

## How Each Works

### Claude Code built-in sandbox

Claude Code's `/sandbox` command enables OS-level isolation around **bash
subprocess commands only**. The Read, Edit, and Write file tools are not
sandboxed — they use the permission system.

| Platform | Technology | Filesystem isolation | Network isolation |
|---|---|---|---|
| macOS | Apple Seatbelt (`sandbox-exec`) | ✅ | ✅ via proxy |
| Linux / WSL2 | bubblewrap (`bwrap`) | ✅ | ✅ via proxy |
| WSL1 | Not supported | — | — |
| Native Windows | Not supported (planned) | — | — |

The network side uses a proxy running **outside** the sandbox. It enforces a
domain allowlist but does not terminate or inspect TLS — meaning domain
fronting is a known bypass path. The filesystem side is enforced by the OS
kernel (Seatbelt / user namespaces via bwrap).

The agent still runs on the **host machine** (or the host user's account).
There is no container, no separate kernel, and no NixOS environment. The
sandbox wraps individual bash invocations; everything else — your home
directory, your SSH keys, your cloud credentials, your running processes — is
visible to Claude unless explicitly denied.

### yolo-jail

yolo-jail puts the AI agent inside a **full OCI container** (Podman rootless
on Linux; Podman Machine or Apple Container on macOS). The container runs a
NixOS-based minimal image. The agent executes as a mapped non-root user with
no visibility into the host.

```
Host (Linux/macOS)
  └─ Podman / Apple Container
       └─ NixOS container
            └─ Claude Code / Gemini / Copilot agent
                 └─ /workspace (bind mount)
                 └─ /home/agent (persistent volume)
                 └─ /ctx/ (read-only context mounts)
                 └─ loophole sockets → host daemons
```

The boundary is a container/VM wall rather than per-syscall filtering. The
agent cannot see host processes, host credentials, host ports, or host
filesystems unless explicitly passed through via a loophole.

---

## Feature Comparison

### Isolation model

| Property | Claude Code sandbox | yolo-jail |
|---|---|---|
| Agent runs on host | ✅ (same user, same kernel) | ❌ (separate container/VM) |
| Separate kernel | ❌ | ✅ on Apple Container; shared on Podman Linux |
| Host process visibility | ✅ full (ps, /proc, etc.) | ❌ (blocked by container ns) |
| Host credential visibility | ✅ unless explicitly denied | ❌ (nothing mounted by default) |
| SSH keys exposed | Unless `denyRead` configured | ❌ (not mounted) |
| Sandbox covers Read/Edit/Write tools | ❌ (only Bash subprocesses) | ✅ (all agent I/O goes through container) |
| Sandbox covers MCP servers | ❌ | ✅ (MCP runs inside container) |
| Portable environment | ❌ (host's tool versions) | ✅ (NixOS, declarative packages) |
| Reproducible across machines | ❌ | ✅ |

**Honest note:** The Claude Code sandbox is a meaningful improvement over no
isolation, but it is not a container. A compromised agent or a prompt injection
attack that exfiltrates `~/.aws/credentials` is prevented only if you have
explicitly configured `sandbox.filesystem.denyRead`. The default sandbox
posture is write-restricted to the project directory but read-open everywhere.

### Running complex apps with multiple services

This is where the gap is most visible.

#### Claude Code built-in sandbox

The sandbox wraps individual bash commands. If you want to run a multi-service
application (e.g., a web app + database + worker), you need to:

1. Start each process as a separate bash command, or
2. Use a docker-compose / podman-compose file and run `docker compose up`

**Problem:** `docker` and `podman` are **explicitly incompatible** with the
bubblewrap sandbox. The Claude Code docs state:

> `docker` is incompatible with running in the sandbox. Consider specifying
> `docker *` in `excludedCommands` to force it to run outside of the sandbox.

`excludedCommands` means those commands bypass isolation entirely and run on
the bare host. So to use Docker Compose inside the Claude Code sandbox, you
must poke a hole in the sandbox. Similarly, anything requiring `/dev/fuse`,
privileged mounts, or user namespaces will fail or need exclusion.

There is `enableWeakerNestedSandbox` mode for running inside Docker, but the
docs warn this "considerably weakens security."

For non-containerised multi-service apps (multiple processes, port bindings,
shared sockets), the sandbox has no coordination layer — the agent just manages
the processes itself. There is no service mesh, health check system, or process
supervision beyond what the agent orchestrates manually via bash.

#### yolo-jail

The agent is already in a container with a full Linux user environment. Running
multiple services is a normal development workflow:

- **Start processes directly** — `mise` manages runtimes, `just` runs recipes,
  processes can fork and background normally.
- **Run nested Podman containers** — `podman` is available inside the jail on
  all platforms including Apple Container (which gets `/dev/fuse` and its own
  kernel). The agent can pull images, build containers, and run `podman compose`.
- **Docker Compose / Podman Compose** — works natively; the agent runs it like
  any CLI tool.
- **Port forwarding** — `network.ports` in `yolo-jail.jsonc` publishes
  container ports to the host. The agent can start a web server on :8080 and
  the developer accesses it at `localhost:8080`.
- **Unix sockets** — on Linux and Apple Container, loophole sockets bridge the
  container to host-side daemons. On Podman-macOS, TCP gateway fallback handles
  the socket bridging automatically.

**Example:** an agent developing a SaaS app can run `podman compose up`
(Postgres + Redis + app server), `mise run dev` (Node/Python/Go dev server),
and Chrome DevTools MCP (browser automation) simultaneously, all within the
jail. The developer sees ports on localhost; the agent sees container-internal
hostnames.

### Running podman/docker containers inside the sandbox

| Scenario | Claude Code sandbox | yolo-jail |
|---|---|---|
| Run `podman pull / run` | ❌ (incompatible with bwrap; must use `excludedCommands`) | ✅ |
| Run `docker compose up` | ❌ (must exclude docker from sandbox) | ✅ |
| Nested containers with full isolation | ❌ | ✅ |
| `/dev/fuse` available | ❌ (blocked by bwrap) | ✅ (Linux + Apple Container) |
| Run sandbox-inside-sandbox | ❌ (kernel ns conflicts) | ✅ (`yolo -- bash` launches nested jail) |
| Build OCI images | Requires excluding from sandbox | ✅ |

When you run `docker *` outside the Claude Code sandbox via `excludedCommands`,
those docker commands execute as the host user with full host access. This
reduces the sandbox to a partial measure — anything Claude can reach via docker
escapes the filesystem and network controls entirely.

### Permission model

| Mechanism | Claude Code sandbox | yolo-jail |
|---|---|---|
| Per-tool allow/deny rules | ✅ rich (Bash, Read, Edit, WebFetch, MCP) | ✅ (loophole-level; commands are free inside jail) |
| Filesystem write boundary | Configurable via `allowWrite`/`denyWrite` | Container root is read-only; only `/workspace`, `/home/agent`, `/tmp` are writable |
| Filesystem read boundary | Default: read-open; `denyRead` to restrict | Default: closed; only mounted paths visible |
| Network allowlist | Domain allowlist via proxy | Bridge mode default; loopholes are the only host-side egress |
| Credential access | Explicit `denyRead` required to block `~/.aws`, `~/.ssh` | Not mounted; no host credentials by default |
| Managed policy (enterprise) | ✅ `/etc/claude-code/managed-settings.json` | ✅ `yolo-jail.jsonc` checked into repo |
| Per-command prompting | ✅ (bypassed in sandbox auto-allow mode) | ❌ (YOLO by default inside jail) |
| YOLO / bypass mode | `--dangerously-skip-permissions` (disable-able) | Default mode (container is the boundary) |

The Claude Code sandbox and the yolo-jail represent two philosophies:

- **Sandbox philosophy:** Agents run on the host with user permissions; reduce
  blast radius by filtering individual syscalls and network connections.
- **Jail philosophy:** Agents run in a container; the host is simply not
  reachable. What gets in is what was mounted; what gets out is what was
  published.

### Resource limits

| Feature | Claude Code sandbox | yolo-jail |
|---|---|---|
| CPU limits | ❌ | ✅ (cgroup delegation on Linux; `--cpus` on Apple Container) |
| Memory limits | ❌ | ✅ (`--memory` flag; kernel-enforced OOM) |
| PID limits | ❌ | ✅ (`pids_limit` in config) |
| In-session `yolo-cglimit` per-job | N/A | ✅ (Linux + Apple Container) |
| Limits survive agent bypass | N/A | ✅ (kernel-enforced) |

The Claude Code sandbox has no resource throttling. An agent running a
runaway build or training loop will consume all available host resources. In
yolo-jail, memory limits are kernel-enforced — the OOM killer terminates
the runaway process, not the host.

### Developer experience: multi-service app workflow

A concrete workflow to illustrate the difference:

**Scenario:** Claude Code is developing a web app (FastAPI + Postgres + Redis +
a Celery worker). The agent needs to start all services, run migrations, run
tests, and have a browser available.

#### Claude Code sandbox (Linux)

1. Install bubblewrap: `sudo apt install bubblewrap socat`
2. Run `/sandbox` to enable.
3. Add `docker *` or `podman *` to `excludedCommands` (or avoid containers
   entirely and run services as bare processes).
4. Configure `sandbox.filesystem.allowWrite` for any paths services need to
   write outside the project dir.
5. Configure `sandbox.network.allowedDomains` for each PyPI, npm, apt mirror,
   and external API endpoint.
6. Agent starts Postgres, Redis, and Celery as bare host processes. These run
   with host-level network and filesystem access (no sandbox).
7. No browser automation available unless the agent installs a browser and
   configures it; no MCP for Chrome DevTools.
8. No resource limits — a bad migration can eat all RAM.

**macOS:** Same flow, but Seatbelt replaces bubblewrap. Docker Desktop or
Podman Machine is required; those daemons run outside the sandbox.

#### yolo-jail

1. Developer writes `yolo-jail.jsonc` once (checked into repo).
2. Agent runs `podman compose up -d` to start Postgres + Redis.
3. Agent runs `uvicorn app:app --reload` in background.
4. Agent uses `mcp__chrome-devtools__navigate_page` to open the app in a
   browser (Chrome DevTools MCP is preinstalled).
5. Agent uses `yolo-cglimit --memory 1g -- pytest` to run tests with a memory
   cap.
6. Agent runs `podman exec postgres psql` to inspect DB state.
7. Everything is isolated; stopping the jail cleans everything up.

### Tool-level agent restrictions: blocking slow or dangerous commands

An underappreciated dimension of sandbox design is the ability to prevent the
agent from using specific tools that are slow, unsafe, or produce noisy output.

**Claude Code sandbox:** The `deny` permission list can block tool categories
(`Bash`, `WebFetch`, etc.) but not easily specific subcommands within Bash.
You can deny `Bash(find *)` to block the `find` command specifically, but
this must be configured per-project and is not enforced structurally — the
agent can still try alternatives, and the deny list is agent-visible (the
agent receives an error message that tells it what was blocked).

**yolo-jail:** The CLAUDE.md file loaded into every session includes a
`## Blocked Tools` section that informs the agent at the prompt level:

```
- `grep`: grep's recursive mode is blocked. Use ripgrep (rg) instead.
- `find`: find is blocked to prevent unintended recursive searches. Use fd.
```

This is enforced via shell shims: `grep -r` and `find` are wrapped in scripts
that either block or redirect the call. The agent sees these as non-functional
and learns to use `rg` and `fd` — which are faster, respect `.gitignore`, and
don't accidentally traverse `/proc` or network filesystems.

The effect is twofold:

1. **Performance:** An agent that uses `rg` instead of `grep -r` finishes
   searches in milliseconds instead of minutes on large codebases.
2. **Safety:** Blocking `find /` prevents the agent from accidentally scanning
   the entire container filesystem (or, on a misconfigured system, the host).

This kind of tool shaping is impossible in the Claude Code sandbox because the
sandbox operates at the syscall / network layer, not the command layer. You
can deny `find` via the permission system, but you cannot redirect it to `fd`
with a helpful error message that trains the agent toward better behavior.

### Loopholes vs. excludedCommands

Both systems need a way to escape isolation for legitimate host access. The
mechanisms are very different.

**Claude Code `excludedCommands`:** Commands listed here run on the **bare
host** with no sandbox wrapping. It is a list of patterns to opt out of
isolation. There are no guardrails on what those commands can do once excluded.

**yolo-jail loopholes:** A loophole is a narrow, purpose-built Unix socket or
TCP bridge from inside the jail to a specific host-side daemon. Each loophole:
- Does exactly one thing (OAuth token refresh, process list, cgroup delegate, etc.)
- Is explicitly listed in `yolo-jail.jsonc`
- Cannot be used to run arbitrary host commands

```sh
yolo loopholes list    # see what's wired up
yolo loopholes status  # health-check each one
```

The OAuth broker loophole, for example, lets the agent call `claude` inside
the jail without mounting host API keys. The cgroup delegate loophole lets
`yolo-cglimit` set kernel resource limits without privileged access. Neither
loophole gives the agent shell access to the host.

### Environment reproducibility

| | Claude Code sandbox | yolo-jail |
|---|---|---|
| Package manager | Host's (apt, brew, etc.) | Nix (declarative, pinned) |
| "Works on my machine" risk | High (host varies) | Low (image is the same everywhere) |
| Add a tool | Install on host | Edit `packages` in `yolo-jail.jsonc`, `yolo check` |
| Pin tool version | Manual | Nixpkgs commit hash or upstream `url`+`hash` |
| Environment diffs across team | ✅ (common source of bugs) | ❌ (all agents see the same image) |
| Rebuild from scratch | Not applicable | `yolo clean && yolo` |

---

## When to use each

### Use the Claude Code built-in sandbox when:

- You are doing routine coding tasks on a trusted, single-service project.
- You want reduced permission prompts with minimal setup (install bubblewrap,
  run `/sandbox`).
- You are on a machine where Docker/Podman is not available.
- You are on Windows (yolo-jail has no native Windows support).
- The task does not involve running untrusted code, containers, or external
  services.

**Be honest about the tradeoffs:** The sandbox offers real improvement over
unsandboxed execution. But if you are configuring `denyRead` rules for
`~/.aws`, `~/.ssh`, and `~/.config/gcloud` and maintaining an `allowedDomains`
list for every npm mirror and API your project touches, you are doing ongoing
security maintenance work that is easy to get wrong. Most developers don't do
this — they configure the minimum needed to make the agent work, then forget
about it.

### Use yolo-jail when:

- You want the agent to have a **full, unrestricted development environment**
  without ongoing permission maintenance.
- You want **structural** isolation: host credentials physically absent, not
  just deny-listed.
- The task involves running services, databases, web servers, or containers
  (especially nested Podman/Docker).
- You need resource limits the agent cannot bypass (memory, CPU, PID count).
- You need a reproducible environment across machines and team members.
- You are doing security research, adversarial prompting, or CI automation.
- You need browser automation (Chrome DevTools MCP is preinstalled).
- You want to eliminate permission prompts entirely — not reduce them.

---

## Honest limitations of yolo-jail

- **More setup:** Requires Podman (Linux) or Podman Machine / Apple Container
  (macOS). The Claude Code sandbox requires only bubblewrap.
- **Startup latency:** ~1s on Linux, ~2-3s on macOS. The Claude Code sandbox
  has near-zero overhead.
- **No native Windows support:** yolo-jail requires Linux or macOS (via WSL is
  theoretically possible but untested).
- **Max bind mounts on Apple Container:** ~22 (Virtualization.framework limit).
  Large projects with many context mounts may hit this.
- **No GPU passthrough on macOS:** Neither the Claude Code sandbox nor
  yolo-jail supports NVIDIA GPU passthrough on macOS. Linux + NVIDIA drivers
  is required for GPU work.
- **No `--network host` on Apple Container:** The container always uses bridge
  mode on Apple Container.
- **Nix store not shared on macOS:** Each jail build downloads packages from
  the binary cache. First build takes 5-10 minutes; subsequent builds are
  instant.
- **Loophole setup required for host tools:** If the agent needs to call a
  host-side CLI (e.g., `aws`, `gh`, `kubectl` against a real cluster), a
  loophole must be configured. The Claude Code sandbox gives host CLI access
  for free (which is also a risk).

---

## Summary

The fundamental difference is not feature count — it is where you put the
default and who has to do work to change it.

**Claude Code sandbox:** Default-deny specific operations; default-allow host
access. The developer must continuously audit and extend an allow list. This
degrades toward approval fatigue and eventually toward rubber-stamping, which
is the same as no security. Every new project dependency, new API endpoint,
or new tool the agent needs adds to the maintenance burden.

**yolo-jail:** Default-deny host access at the container wall; default-allow
everything inside. The developer configures a short list of explicit host
access (ports, loopholes, bind mounts) once, at setup time, checked into
version control. The agent has a full development environment, no permission
prompts, no fatigue, and no path to the host that wasn't deliberately opened.

The productivity argument and the security argument point in the same
direction: eliminating approvals is better than reducing them, provided the
boundary is structurally enforced. A container wall is structurally enforced.
A domain allowlist maintained under deadline pressure is not.

---

## References

- Claude Code sandboxing docs: https://code.claude.com/docs/en/sandboxing
- Claude Code devcontainer docs: https://code.claude.com/docs/en/devcontainer
- Claude Code security docs: https://code.claude.com/docs/en/security
- Claude Code settings reference: https://code.claude.com/docs/en/settings
- bubblewrap: https://github.com/containers/bubblewrap
- sandbox-runtime (open source): https://github.com/anthropic-experimental/sandbox-runtime
- yolo-jail platform comparison: [docs/platform-comparison.md](platform-comparison.md)
