# Glossary

Definitions used across design, research and companion seeds. These definitions describe the proposals;
they do not claim the mechanisms are built.

## Callback lease

A per-user, per-port record naming the launch that owns a host-loopback callback publication. Not a shared
router or permission to publish several jails on one port; acquisition is serialized across launches.

**Origin:** Defined in [managed MCP sign-in](design/managed-mcp-sign-in.md#5-the-callback-route), 2026-11-17.

## Callback route

The path by which the host browser's redirect to a fixed loopback port reaches the agent listening for it.
Not a general port publication or an outbound permission.

**Origin:** Coined in [managed MCP sign-in](design/managed-mcp-sign-in.md#2-vocabulary), 2026-11-17.

## Capturable

A sign-in point is capturable in a launch when the agent writes sign-ins to a source store the broker adopts
from there. Bundle placement alone does not imply capture; credential view, alternate storage and Mac host
are outside the stated capture support.

**Origin:** Defined in [managed MCP sign-in](design/managed-mcp-sign-in.md#6-delivery-choice-including-the-source-agent), 2026-11-17.

## Execution context

The host-side environment a brokered call runs in; for AWS, which host profile. Not permission or resource
admission, and not a context the workspace or agent may select.

**Origin:** Defined in [scoped broker policy](design/scoped-broker-policy.md#7-execution-contexts-and-routing-proposed), 2026-11-17.

## Managed sign-in point

A sign-in point yolo delivered through a selected pack. Not an externally installed, unmanaged bundle.

**Origin:** Coined in [managed MCP sign-in](design/managed-mcp-sign-in.md#2-vocabulary), 2026-11-17.

## Outcome note

One host-only, token-free record per approved sign-in identity, written by an adopting or refreshing pass,
carrying the observed outcome code and its metadata. Not a token store, provider-response archive or status
side effect; status only reads it.

**Origin:** Defined in [managed MCP sign-in](design/managed-mcp-sign-in.md#7-token-free-status), 2026-11-17.

## Route (broker policy)

A host-only line mapping a scope to an execution context. Not a permission rule or an MCP delivery route.

**Origin:** Defined in [scoped broker policy](design/scoped-broker-policy.md#7-execution-contexts-and-routing-proposed), 2026-11-17.

## Sign-in bundle

A pack-delivered bundle whose pack declares it as a place to sign in. Not a contribution to `mcp_servers`
composition or general bundle-server sharing.

**Origin:** Defined in [managed MCP sign-in](design/managed-mcp-sign-in.md#4-the-sign-in-point), 2026-11-17.

## Sign-in point

The one native server entry inside one agent at which the user's ordinary sign-in for a one-sign-in server
can succeed. Not a yolo command or a delivery route.

**Origin:** Coined in [managed MCP sign-in](design/managed-mcp-sign-in.md#2-vocabulary), 2026-11-17.

## Source agent

An agent whose pack declares an adoptable store. Not a header-only consumer without that declaration.

**Origin:** Defined in [managed MCP sign-in](design/managed-mcp-sign-in.md#2-vocabulary), 2026-11-17.

## Usable record

A canonical record that is `live` and unexpired at the moment it is read. Not an expired or dead record.

**Origin:** Defined in [managed MCP sign-in](design/managed-mcp-sign-in.md#2-vocabulary), 2026-11-17.
