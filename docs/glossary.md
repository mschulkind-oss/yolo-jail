# Glossary

Definitions used across design, research and companion seeds. These definitions describe the proposals;
they do not claim the mechanisms are built.

## Callback route

The path by which the host browser's redirect to a fixed loopback port reaches the agent listening for it.
Not a general port publication or an outbound permission.

**Origin:** Coined in [managed MCP sign-in](design/managed-mcp-sign-in.md#2-vocabulary), 2026-11-17.

## Execution context

The host-side environment a brokered call runs in; for AWS, which host profile. Not permission or resource
admission, and not a context the workspace or agent may select.

**Origin:** Defined in [scoped broker policy](design/scoped-broker-policy.md#7-execution-contexts-and-routing-proposed), 2026-11-17.

## Managed sign-in point

A sign-in point yolo delivered through a selected pack. Not an externally installed, unmanaged bundle.

**Origin:** Coined in [managed MCP sign-in](design/managed-mcp-sign-in.md#2-vocabulary), 2026-11-17.

## Route (broker policy)

A host-only line mapping a scope to an execution context. Not a permission rule or an MCP delivery route.

**Origin:** Defined in [scoped broker policy](design/scoped-broker-policy.md#7-execution-contexts-and-routing-proposed), 2026-11-17.

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
