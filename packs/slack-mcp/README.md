# Slack native sign-in content

This integration pack carries the complete MIT-licensed Slack skills plugin original
at commit `5b8802105740b5c045c97247fded2e73b205e95e` (upstream version 1.8.0), from
<https://github.com/slackapi/slack-skills-plugin>. The retained license is
[`skills/slack/LICENSE`](skills/slack/LICENSE). All upstream files, including hidden
files and the three upstream executable scripts, are retained; yolo never runs them
to provision or inspect this pack.

Select the content with `yolo pack add slack-mcp`, run `yolo config check`, then
relaunch. It is delivered through the existing whole-plugin skills route. Ownership
marks and a configured namespace may change the delivered manifest name; `.mcp.json`
and the manifest's MCP server value may not change. Placement is not proof the agent
loaded it.

This pack installs no program, contributes no `mcp_servers` entry, and does not opt
any server into one sign-in. The user's `mcp_servers` configuration remains the
sharing authority. Public client identity and the fixed callback port remain only
in the upstream bundle content; they are not copied into the wrapping declaration.
The optional callback declaration permits a convenience route only where the
managed launch qualifies: IPv4 loopback TCP on Linux bridge podman, serialized per port.
Pending/unavailable publication and contention do not change sign-in authority. Status names
`plugin:<delivered-name>:slack`; paste the full callback URL in that native entry when needed.
Use `yolo mcp-auth status <configured-server> [--json]` for token-free recovery. The own-client
option is `yolo config set mcp_servers.<configured-server>.oauth.client_id <id> --user`, then
config check/relaunch; a mismatching bundle/native client is not borrowed by another agent.
Claude's duplicate entry derives a JSON `headersHelper` only under the managed source/record
conditions; pi's bare helper remains compatible. Placement is not a native helper witness.

The [accepted managed sign-in contract](../../docs/design/managed-mcp-sign-in.md)
separates synthetic placement/render tests from native agent, rootless callback,
Mac storage and provider verification. This packaging is not a claim of any native
or provider success.
