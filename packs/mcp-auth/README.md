# MCP sign-ins for selected workspaces

The `mcp-auth` pack adds a host-side sign-in broker to the environment when a selected agent
pack needs it. This detached, not-yet-integrated Linux podman candidate seeds a user-level
canonical entry into an absent Codex workspace entry, or replaces an exact copy it previously
wrote; it removes an exact managed stale copy after sign-out. It cannot prove the headers used
for a fresh native Codex sign-in, so it **does not adopt that sign-in** into user scope or strip
its refresh token. An unknown same-key native entry is left intact. The accepted Task 12
fresh-login harvest requirement therefore remains blocked, not waived. The canonical store
is never mounted into the jail. Conditional servers whose per-agent boot eligibility cannot be
proved are not shared.

This candidate is not live propagation or OAuth refresh. Use `codex mcp login <name>` in each
workspace for an unproved native login; inspect scope with `yolo config why mcp_servers.<name>`.
No `yolo mcp-auth` command exists yet. Apple Container, macos-user and `yolo host` do not share
MCP sign-ins in this slice. See [the implementation plan](../../docs/design/mcp-auth-sharing-plan.md#task-12).
