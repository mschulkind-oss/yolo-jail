# MCP sign-ins for selected workspaces

The `mcp-auth` pack adds host-side sign-in sharing to the selected coding environment when a
pack needs it. Core reads declared stores and capabilities, not an agent registry. The host-only
canonical store is never mounted into the jail; conditional servers whose boot eligibility
cannot be proved are not shared.

The one-sign-in path adopts a matching native entry, refreshes it on the host and writes access
tokens back to its source. The retained managed synthetic candidate adds token-free
`yolo mcp-auth status [SERVER] [--json]`, native-name recovery, and bounded
`yolo mcp-auth header SERVER [--format bare|json]`. Header is token-bearing, never a status probe:
unexpired issuance does not redeem; expired jail requests wake the durable poll, while host
requests never refresh. pi keeps the bare helper; Claude derives JSON `headersHelper` only when
its source bootstrap or usable-record conditions hold. Native helper/reconnect behavior is unverified.

Select native sign-in content with `yolo pack add slack-mcp`, run `yolo config check`, then
relaunch. This adds no server or sharing authority: `sign_in = "once"` remains user-scope.
Status names the resolved native entry and full callback URL paste. An own-client choice uses
`yolo config set mcp_servers.<name>.oauth.client_id <id> --user`, then config check/relaunch;
no other consumer borrows that client identity. Placement is not plugin-loading proof.

The Codex store-sharing slice seeds an absent entry or replaces an exact managed copy. It cannot
prove a fresh native entry's header identity, so it neither adopts nor strips its refresh token;
unknown native entries are preserved. Fresh native harvest and Codex helper enablement remain blocked.

Linux podman and Linux `yolo host` have resident one-sign-in paths. Apple Container and
podman-on-Mac use launch/confirmed-exit passes; macos-user has a session path. The optional
callback convenience is IPv4 loopback TCP on qualifying Linux bridge podman only, per-port leased;
other backends, contention and unavailable publication retain the full callback paste route.

See [managed sign-in](../../docs/design/managed-mcp-sign-in.md), its
[synthetic coverage inventory](../../docs/design/managed-mcp-sign-in-coverage.md), and the
[sharing design](../../docs/design/mcp-auth-sharing.md). Independent review/landing and
native/rootless/Mac/provider/plugin-loading/reconnect/writeback/OAuth witnesses remain separate.
No yolo login/logout/revocation flow or native/provider success is claimed.
