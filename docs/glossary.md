# Glossary

Definitions used across design, research and companion seeds. These definitions describe the proposals;
they do not claim the mechanisms are built.

## Anchor (broker scope)

A literal prefix declared by a source using segment scope nesting; every scope of that source starts with
exactly one anchor. The anchor alone is a valid scope. Not an approved root or permission to access resources
outside approved roots.

**Origin:** Coined by the source-scope closure author in [scoped broker policy](design/scoped-broker-policy.md#38-scope-matching), 2026-11-17.

## Callback lease

A per-user, per-port record naming the launch that owns a host-loopback callback publication. Not a shared
router or permission to publish several jails on one port; acquisition is serialized across launches.

**Origin:** Defined in [managed MCP sign-in](design/managed-mcp-sign-in.md#5-the-callback-route), 2026-11-17.

## Callback route

The path by which the host browser's redirect to a fixed loopback port reaches the agent listening for it.
Not a general port publication or an outbound permission.

**Origin:** Coined in [managed MCP sign-in](design/managed-mcp-sign-in.md#2-vocabulary), 2026-11-17.

## Candidate cells

The finite set of cells an operation may turn out to be before a provider fact is read. Not permission to
run an unresolved refused candidate: no exact or timed answer overrides a hard deny.

**Origin:** Coined in [scoped broker policy](design/scoped-broker-policy.md#41-fact-reads), 2026-11-17.

## Capturable

A sign-in point is capturable in a launch when the agent writes sign-ins to a source store the broker adopts
from there. Bundle placement alone does not imply capture; credential view, alternate storage and Mac host
are outside the stated capture support.

**Origin:** Defined in [managed MCP sign-in](design/managed-mcp-sign-in.md#6-delivery-choice-including-the-source-agent), 2026-11-17.

## Execution context

The host-side environment a brokered call runs in, with fields and set/unset rules owned by the source's
declaration; for AWS, which host profile. Not permission, resource admission or a verified account binding,
and not a context the workspace or agent may select.

**Origin:** Defined in [scoped broker policy](design/scoped-broker-policy.md#7-execution-contexts-and-routing-proposed), 2026-11-17.

## Implicit read

The unnamed shipped read derived for a generic broker declaration with no read cells and an existing read
grant kind. It is decided by `read` lines without adding anything to the declaration hash or gate disclosure.
Not newly granted authority: with neither read cells nor a read grant kind the source has no reads.

**Origin:** Defined by the contract-repair author in [scoped broker policy](design/scoped-broker-policy.md#39-read-cells-zero-one-or-many), 2026-11-17.

## Managed sign-in point

A sign-in point yolo delivered through a selected pack. Not an externally installed, unmanaged bundle.

**Origin:** Coined in [managed MCP sign-in](design/managed-mcp-sign-in.md#2-vocabulary), 2026-11-17.

## On-switch

The shipped source-scope read grant without which an operation broker refuses every call, writes included.
The proposed independent read/write policy retires it only through a marked gate approval; a legacy record
keeps its old behavior. Not a new permission switch.

**Origin:** Coined in [scoped broker policy](design/scoped-broker-policy.md#37-read-and-write-are-independent), 2026-11-17.

## Outcome note

One host-only, token-free record per approved sign-in identity, written by an adopting or refreshing pass,
carrying the observed outcome code and its metadata. Not a token store, provider-response archive or status
side effect; status only reads it.

**Origin:** Defined in [managed MCP sign-in](design/managed-mcp-sign-in.md#7-token-free-status), 2026-11-17.

## Provisioning candidate

A shipped pack not selected in this launch whose declared sign-in bundle original could serve a
one-sign-in server with no sign-in point. Advisory only: not a sign-in point, scope authority, callback
lease, capture source, delivery route or rendered client identity.

**Origin:** Defined in [managed MCP sign-in](design/managed-mcp-sign-in.md#4-the-sign-in-point), 2026-11-17.

## Read word

The capability word a native manifest ships for its read (`fetch` for git); that word and literal `read`
are one internal read token in lists and ask keys at every scope. Not a new verb or a write capability;
writing both words for one scope in one layer is a load error unless baseline already accepts that pair,
in which case its baseline result is retained and only a pair involving `never` is an error ([V6 fallback](design/scoped-broker-policy.md#13-unverified-claims-this-design-relies-on)).

**Origin:** Coined by the native-cap closure author in [scoped broker policy](design/scoped-broker-policy.md#36-spelling), 2026-11-17.

## Route (broker policy)

A host-only line mapping a scope to an execution context. Not a permission rule or an MCP delivery route.

**Origin:** Defined in [scoped broker policy](design/scoped-broker-policy.md#7-execution-contexts-and-routing-proposed), 2026-11-17.

## Scope nesting

How a source's declaration says a scope string is matched: `exact` uses byte identity to a classifier chain
ID; `segment` uses byte prefixes on a declared separator under declared anchors. Not admission, normalization
or cross-source matching; only the approved-root proof admits an operation.

**Origin:** Coined by the source-scope closure author in [scoped broker policy](design/scoped-broker-policy.md#38-scope-matching), 2026-11-17.

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

## Timed limit

The smallest cap among all entries one timed answer would write: asking reads, asking writes and non-refused
candidates. Not a shortened duration or a change to a stored entry's expiry; an above-limit answer is refused,
subject to the write-only baseline fallback recorded with V5.

**Origin:** Coined by the native-cap closure author in [scoped broker policy](design/scoped-broker-policy.md#51-timed-caps), 2026-11-17.

## Usable record

A canonical record that is `live` and unexpired at the moment it is read. Not an expired or dead record.

**Origin:** Defined in [managed MCP sign-in](design/managed-mcp-sign-in.md#2-vocabulary), 2026-11-17.
