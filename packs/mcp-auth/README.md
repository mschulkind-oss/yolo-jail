# MCP sign-ins for selected workspaces

The `mcp-auth` pack adds host-side sign-in sharing to the selected coding environment when a
pack needs it. Core reads declared stores and capabilities, not an agent registry. The host-only
canonical store is never mounted into the jail; conditional servers whose boot eligibility
cannot be proved are not shared.

The integrated one-sign-in path adopts a matching Claude native entry, refreshes it on the host
and writes access tokens back to its source. `yolo mcp-auth header SERVER` serves an access-token
header; pi's derive consumes it. This is a token-bearing delivery verb, not a status probe.
Claude and Codex helper rendering and the human `status` verb are not built.

The Codex store-sharing slice seeds an absent entry or replaces an exact managed copy. It cannot
prove a fresh native entry's header identity, so it neither adopts nor strips its refresh token;
unknown native entries are preserved. Fresh native harvest remains blocked, not waived.

Linux podman and Linux `yolo host` have resident one-sign-in adoption/refresh paths. Apple Container
and podman-on-Mac use launch/confirmed-exit boundary passes; macos-user has a session path. Native
backend and provider behavior still require their separate witnesses.

See the [sharing design](../../docs/design/mcp-auth-sharing.md) and
[plan](../../docs/design/mcp-auth-sharing-plan.md) for shipped slices and gates.
[Managed MCP sign-in](../../docs/design/managed-mcp-sign-in.md) owns the unbuilt first-login,
callback, delivery-repair, status and recovery revision. It uses ordinary sign-in inside a
managed agent, not a third yolo login ceremony, and remains under review pending engineering closure.
