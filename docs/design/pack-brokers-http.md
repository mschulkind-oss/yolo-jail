---
title: "Pack broker HTTP calls stay within the selected host decision"
date: 2026-11-06
status: in-review
stage: DESIGN
next: "Build plan Task 3 slice 3.4 (the selected launch starts and seals the front); keep publication off"
tags: [design, broker, packs, http]
summary: "G3 core is partially integrated; private delivery is proposed and unverified; G4 remains unactivated."
---

# Pack broker HTTP calls stay within the selected host decision

**Status:** 2026-11-06. G3 core is partial; the G4 front is built but no launch starts it yet. The [build plan](pack-brokers-http-plan.md) has Task 1 and the Task 2 semver slice built and reviewed, and Task 3 slices 3.1 to 3.3 built (seal, local TLS and authentication, withdrawal before the final send). Nothing publishes a URL, token or trust. [OQ-HTTP1](#OQ-HTTP1) is ruled: a custom signer returns only header material, and its wire is not designed. [OQ-HTTP2](#OQ-HTTP2) is withdrawn until a client is shown incompatible. Live grants, native rootless and macOS proof are not established. The broader [pack broker design](pack-brokers.md) refuses permanent Add ([OQ-BX7](pack-brokers.md#OQ-BX7)) and caps a timed answer by the smallest `max_for` ([BX-D17](pack-brokers.md#BX-D17)).

> **In short.** An HTTP front carries a call, not authority: only the selected host broker binds its destination and credential, classifies exact bytes and fresh facts, decides, audits and executes once.

**Start at [HTTP binding](#http-binding-and-executor).** Build order is in the [current plan](pack-brokers-http-plan.md).

**Reads with:** [`pack-brokers-http-plan.md`](pack-brokers-http-plan.md) (the current build
hand-off); [`pack-brokers.md`](pack-brokers.md) (shared terms, decisions, classifier and
executor rules); [`access-introspection.md`](access-introspection.md) (the shared decision
pipeline).

This scope excludes mixed-cell timed approvals until
[BX-D17](pack-brokers.md#BX-D17) and [BX-D18](pack-brokers.md#BX-D18) are built (there is no Add, per [OQ-BX7](pack-brokers.md#OQ-BX7)), MCP sign-in, actual CircleCI/cloud auth, and publication.
No new credential channel, grant, approval RPC or relaxed CLI limit is authorized.

**Needs your ruling:** nothing. [OQ-HTTP1](#OQ-HTTP1) is ruled (a pack signer returns only a header; core sends the request), and [OQ-HTTP2](#OQ-HTTP2) is withdrawn until a client is shown incompatible.

## HTTP pending custody and revalidation

For HTTP, retain only nonsecret intent and canonical digests, recorded cells, native IDs,
and request metadata—not an original or canonical HTTP body, replayable HTTP call, or
audit payload. At act time, the client must resubmit the original body. Core runs the
currently selected classifier again and obtains fresh native leaf-to-parent facts, then
compares original intent, canonical form, declaration, ancestry, ceiling, selection, grant,
and standing before one dispatch. A changed binding refuses or asks again; a
missing HTTP body cannot be recovered from an archive. Preserve the existing one-use,
15-minute approval and required-audit/spend rules. This HTTP custody rule does not
change the accepted private `OriginalArgv` storage and canonical replay for CLI approval,
or impose an HTTP body-privacy rule on CLI stdin.

## HTTP read lookups and native parents

For HTTP, both original and canonical method, path, query,
headers, and body must equal the instantiated request. A fixture, template check, or
current read grant cannot substitute for this runtime check. No write verdict or recursive
lookup may dispatch; detect cycles and cap native-fact resolution at three rounds.

HTTP native-fact mappings are minimal and typed: a resource and native-ID input selects a
returned native ID and, where applicable, a known parent relation. Require the input ID
to equal the returned ID before deriving a parent. Selectors are JSON Pointers to single
JSON strings as defined by [RFC 6901](https://www.rfc-editor.org/rfc/rfc6901); a
classifier's own assertion or a previously stored chain is not proof of current root
membership. Missing or conflicting results, unknown mappings, cycles, or withdrawn
standing or read grants refuse and identify the next host step. Resolve resource parents
fresh when acting. Read standing must be checked for every runtime-instantiated lookup and again at act.
A failed lookup remains a fact supplied to the classifier, not permission to guess.

## HTTP binding and executor

The declaration is `http { origin, credential { source, key, header, prefix }, request_headers }`.
Pin an HTTPS DNS origin, with optional port and no userinfo, path, query, fragment, wildcard,
or runtime-selected host. Existing private launch-input bindings, declaration hash, native
jail/program checks, current scope, leases, cells, and read standing remain applicable.

For `source: host_env`, the existing selected-pack config gate must display and freeze the
exact variable name together with origin, credential header, source, and prefix, within the
current host user's ceiling and selection. `hostEnv` presence or `EnvFromHost` alone confers
no authority. Check that approved binding at each dispatch; do not create a second
credential channel or grant. The token value never enters classifier input or classifier
environment, jail input, storage, or audit. For `source: fenced_file`, require an absolute
path inside approved credential paths, actual host-user ownership, a nonsymlink regular
file with mode `0600`, and at most 8 KiB read; refuse empty, oversized, or
control-containing values. Prefix values are at most 128 bytes and contain no controls.
Require one valid header field; prohibit Host, connection, proxy, cookie, `Yolo-*`, and
hop-by-hop fields. Case-insensitively validate and deduplicate
allowlisted client headers; clients cannot supply authorization or override the credential.
Test TLS origin/trust overrides are test-only, not a production insecure mode or trust path.

The selected HTTP classifier receives the original request body under existing
[parent classifier contract](pack-brokers.md#72-the-classifier) rules; this does not
promise that inline secrets are hidden from it. Its subprocess stdin remains one JSON value, not a length-framed stream. HTTP input may
use base64 body encoding within the bounded JSON input budget below; its complete answer
remains limited to 64 KiB and two seconds. Raising the HTTP input bound to accommodate a
3 MiB body must leave the existing CLI 64 KiB stdin and answer behavior unchanged. A
canonical body is either the one original body reference, with verified digest and
presence explicitly endorsed by the classifier, or a complete inline base64 rewrite inside
the answer. Refuse an oversized or partial rewrite: no blob side channel or implicit jail
forwarding. Core resolves actual bytes and independently hashes original intent and
canonical method, path, query, headers, body presence and bytes, ordered write cells, and
native chains. Hash components require
unambiguous length delimiting; that does not add a subprocess wire prefix or new RPC.
Distinguish absent from empty bodies and reject duplicate fields.

Ingress limits precede allocation. On authenticated host transport, read only its fixed
length prefix first; reject a declared JSON frame above 4,456,448 bytes before allocating
or decoding it. That cap budgets 4,194,304 bytes of base64 for a 3 MiB body, 196,608 bytes
for worst-case JSON escaping of two raw aggregates, each capped at 16 KiB: path plus query,
and allowed header values. It leaves 65,536 bytes for bounded names, keys, and other
metadata. Apply that bounded JSON encoding budget to HTTP classifier input without adding
a subprocess length prefix. Bound header count to 64 and each name to 256 bytes within the
metadata budget. Separately reject original or canonical bodies above 3 MiB and either raw
aggregate above 16 KiB. Caller fields cannot raise limits; spilling or unbounded streaming
cannot satisfy the ingress bound. The classifier answer's 64 KiB limit is separate from
the HTTP input budget. These HTTP changes are requirements, not claims about the currently
wired input limit.

The method floor. Only POST may be a non-safe `read`, and only on a declared, gate-shown
`read_over_post` path. Before accepting a `read` verdict, check both original and resolved
canonical method and path against this rule. DELETE and PUT are never read exceptions
merely because their path is listed.

After the real decision and required audit/spend, the core-owned transport sends the
canonical request to the pinned origin. Send no cookie or jar; never follow a redirect or
make its second request.
Reject a known credential in a URL or query in either direction, including `Location`.
Stream the response with a 32 MiB cap; suppress `Set-Cookie` and credential headers and
redact known exact credential bytes across body chunks and headers. Do not promise
redaction of encoded or transformed secrets. A custom signer runs only after decision; it
receives neither classifier environment nor host credential before then.

## HTTP custom-executor boundary

The parent design permits a selected [pack executor](pack-brokers.md#83-the-pack-executor)
for a call core cannot apply, and says it runs only after the real allow decision. Its
[trust boundary](pack-brokers.md#9-the-trust-boundary) does not defend against malicious
pack programs running as the host user. Core's origin pinning, redirect refusal, response
cap and redaction therefore describe only requests core itself sends. They are not an OS
sandbox or a restriction on independent host-program network activity.

The HTTP contract specifies a core-owned pinned transport path and requires custom signing
to occur only after decision. It does not say whether an HTTP pack executor only supplies
authentication material or conducts the request. [OQ-HTTP1](#OQ-HTTP1) rules it: the
program returns only header material and core sends the request. The signer's wire is not
designed yet, and no expansion of credential channels, grants, or approval behavior is accepted here.

## HTTP front, refusal and retries

### Shared and different surfaces

| | CLI front | HTTP front |
| :--- | :--- | :--- |
| In the jail | `intercept` puts a forwarder named for the program ahead of it on PATH | A loopback HTTP listener run by `yolo-jaild`; the pack's `env` names its URL in the variable the client reads, such as a base-URL setting |
| Identity, scope, decision, store, approvals, audit, dry run | Shared | Shared |
| What the classifier reads | argv and the stdin digest | method, path, query, allowlisted headers, body |
| Canonical form | argv | request bytes |
| Lookups | argv templates | request templates |
| Credential | The host program's own login | The core-owned transport path builds a header from a declared source; a custom signer returns only header material ([OQ-HTTP1](#OQ-HTTP1)) |
| Executor hazards | Host file writes by flag, a server or token flag, spawned helper programs, git pushes | Redirects, cookies, credentials in URLs, response size, non-safe methods that read |
| A refusal reaches the agent as | Exit 64 and the refusal text on stderr, as `gh`'s | Status 403 with headers `Yolo-Verdict` and `Yolo-Request`, and a body in the error shape the block declares (GraphQL's `errors` array for the local HTTP proof), whose message is the refusal text |
| An ask reaches the agent as | Exit 75 and the menu | Status 403, `Yolo-Verdict: asks`, the menu in the message |

### Shared-loopback caller boundary

The selected pack's client-facing base URL is a local HTTP route, not the host TLS
endpoint file or a host credential. The TLS preamble identifies the front's configured
`FrontJailID` on its host connection; it does not identify which jail dialed that front over
a shared loopback. If jail B can reach jail A's front, A's request could therefore arrive at
the host with A's identity and use A's selected binding and grant. This is an architectural
confused-deputy path, not a measured two-jail runtime result; [OQ-HTTP2](#OQ-HTTP2)
records the caller boundary.

The existing `{caller_token}` is defined by
[`loopholedecl.TokenCallerToken`](../../internal/loopholedecl/tokens.go) and resolved by the
selected [`CredentialScope`](../../internal/packload/credentialscope.go), but this proves
selection-aware composition, not cross-jail secrecy. Source review found token records in the
per-entry channel, selected-agent environment files, and session snapshots under
[`paths.WorkspaceHomeState`](../../internal/paths/paths.go); the workspace is also exposed
to each jail. A same-owner peer jail sharing the workspace can therefore read those
workspace aliases, regardless of a `0600` mode or the read-only home overlay. No-follow and
`os.Root` protections prevent path escape; they do not hide legitimate files in the shared
workspace. The current token-bearing consumer startup chain is workspace-backed too. This is
a source-based finding, not a reproduced two-jail exploit or runtime verification. Private
delivery and consumer integrity are engineering prerequisites before reusing this composition
for cross-jail isolation; the following proposal describes that work without claiming it is
implemented.

### Proposed private ordinary-client delivery

**Design proposal only.** The owner prefers a fresh jail-scoped replacement token in the
selected client's ordinary API-token field, consumed by the front while the host independently
injects the real origin credential. That preference does not settle custody, local wire,
client compatibility or backend support. A client shown incompatible holds only its own cell, and its fallback is asked
then ([OQ-HTTP2](#OQ-HTTP2)); it does not hold compatible cells whose own gates pass.

#### Private payload and consumer integrity

Mint 256 fresh random bits for each selected front in each live jail generation. Use
[`svcendpoint.NewToken`](../../internal/svcendpoint/token.go#L64) for entropy only; never reuse a service token, a vendor
credential or the CLI TLS bearer. Keep the token, the front's server certificate/key, per-client TLS trust material, selected-client
environment/settings and immutable session snapshots in a private per-jail-generation payload
outside the workspace, shared caches, shared program prefixes and every peer-visible bind. An
outside-workspace host-state path (for example, a dedicated per-jail child beneath
[`paths.AgentsDir`](../../internal/paths/paths.go#L1110)) is a candidate, subject to lifecycle
and native mount review; the existing [`jailgrant` vehicle](../../internal/cli/run/jailgrant.go#L186-L227)
is only a source of mount-shape evidence, not grant policy or proof of this boundary. Publish
files atomically and use owner-only modes, but rely on mount or session-sandbox exclusion for
same-owner peers: `0700` and `0600` alone are not that boundary.

Public state contains only front name, address, generation and readiness/failure reason, never
the token, its hash or private key. Launch and valid attach read the private record directly;
there is no workspace replica or fallback to the existing channel. A missing or changed record
refuses attach and requires a fresh generation. A zero-front selection creates no token or
trust payload.

Associate delivery with the selected program, not a global agent/profile variable. The proposed generic `env` contribution adds `bin`, paired with `served_by` naming the same selected front: for example, `[[env]]` with `bin = "fixture-http-client"`, `served_by = "fixture-http"`, and ordinary `vars` that resolve `{caller_token}`, `https://{listen}` and the proposed `{client_trust}` path. The token remains the complete value of the client's ordinary variable; the client supplies the approved header and prefix. `env.bin` and `served_by` do not create a provider, login path, global API variable, agent registry or new front eligibility. Resolve only one selected program recipe (or asserted need) and an enabled, selected, approved HTTP binding; reject missing, ambiguous, colliding or unresolved targets, conflicting managed fields and unknown substitutions. Keep existing profile/platform gates for unrelated service contributions unchanged. Private `env.bin` declarations are excluded from all ordinary projections, including `EnvServedBy`, before value or service-pointer folding; their selected-client plan is separate. A private same-key declaration cannot retag an ordinary value, and unrelated profile/platform results remain unchanged. No targeted client/front means no generated material. Read-only/check composition validates and predicts without minting, staging, installing or executing pack derives.

The selected launch-input producer is the sole owner of an immutable, nonsecret **PRE-LEASE prepared** record, published before the host daemon starts. Its version and `prepared` phase bind a fresh generation to the container and producer-issued `FrontJailID`, runtime/setup/staged pack tree frozen from the selected launch, host scope `LaunchID`, selected loophole/front/pack/source/declaration hash, approved roots/program identities, and client-plan identities and variable names. It is a placement snapshot, not a lease, readiness, authentication or authority; identity never comes from workspace or jail-name reconstruction. Hash declarations, never secret values.

Omit `BrokerStart`/PID, lease/path/live binding, actual listen address, certificate/key or their identities, trust path/bytes/identity, and listener/readiness results from the prepared record. They are absent, not null, zero, placeholders or values from a previous generation. The optional host-input `HTTPFrontGeneration` is a nonempty generation reference only; omit it when no applicable selected client/front exists and for old nonclient G3 inputs. Strict prepared-record validation rejects duplicate, unknown or mistyped fields and identity mismatches. The pre-lease host constructor validates that private record and independently available selected-scope identities only: it must not require a live lease or treat prepared state as request admission. Existing G3 startup still performs `NewStartID` and `BindLease` in its existing order. The HTTP request-frame `front_generation` wire remains Task 3, not this host-input reference.

Under the existing launch lock, publish the prepared record outside the workspace with private atomic/no-follow ownership. Attach adopts the generation held by the launch, not a restaged record, and verifies its generation, file identity/inode, container, producer/front and host-launch identities, declaration/program/root bindings, and frozen runtime/setup/pack-tree context. A changed or replaced record refuses adoption; prepared ownership is not established by live-front readiness. Cleanup follows confirmed container absence and removes only the still-matching inode and generation it owns, never a replacement. A later ready attach additionally needs the separate host seal and its live binding.

Only after the existing G3 `BindLease` succeeds and Task 3 has bound the actual listener/address and satisfied local TLS/readiness checks does the host lifecycle publish a separate immutable host seal. It binds the prepared generation to the actual host scope, broker start/lifetime, endpoint and certificate/trust identities. Never mutate or promote the prepared record, move G3 lease creation into launch-input staging, or reuse a previous lease. The seal is not authority; client URL/token/trust environment, managed startup and public READY state require the matching prepared identity, separate seal, complete payloads, actual live lease and required readiness to agree. Task 1 publishes none of these ready forms.

The existing caller-token channel remains separate and cannot be used as the private mechanism or as peer-secrecy evidence. A generated consumer startup chain may be built only from a fully sealed ready generation; Task 1 prepares identity and ownership, not an authenticated or published client.

The generated consumer startup chain must use this generation's managed scripts and private
settings, not workspace `.bashrc`, shell init, `agent-env` files or launcher aliases. Start it
through an absolute shipped bootstrap without user shell hooks, and parse export records as
data rather than executable shell. The selected program, interpreter, libraries and automatic
startup configuration must resolve to the image/read-only bundle or a private install prefix
that peer B cannot see; do not use peer-writable pack copies or cache aliases. Stage updates
before atomically replacing a complete consumer version, and never rewrite a live generation's
snapshot. This boundary covers automatic managed startup, not user code deliberately executed
from the shared project: that code runs with the user's authority and may use the token.

#### Local authentication and request path

The client-facing URL is HTTPS, separate from the host's authenticated TLS endpoint file and
preamble. Give each front a separate certificate/key for its actual loopback name and a private
per-client trust configuration. Do not install a host trust root, accept insecure TLS, use a
client certificate as a new grant, or put a bearer on plaintext shared loopback. Podman currently adds `NET_RAW` in its nesting and host branches, and nested launches share
the parent network namespace; this is source evidence to avoid assuming plaintext secrecy, not
a packet-capture result or a universal escape claim ([`assemble_parts.go`](../../internal/cli/run/assemble_parts.go#L252-L304), [`assemble.go`](../../internal/cli/run/assemble.go#L350-L355)).

The selected client must send the local token through its ordinary field. Candidate local
header/prefix validation reuses the selected HTTP credential's declared header and prefix only
when the actual client emits them; verify that behavior against the real client before
publication. Require exactly one well-formed credential field and reject duplicates or conflicts.
A missing or wrong local token returns **401 before reading the body or contacting the host**;
the handler, receiver and spend counters remain zero. A valid local token may proceed to the host
broker, where ASK returns **403** with `Yolo-Verdict: asks`, an opaque `Yolo-Request` and the
client's normal error shape (GraphQL `errors` for the fixture); this enters the host handler once,
while receiver and spend remain zero. Compare the local token in constant time; do not install
a global trust override or allow a missing-TLS fallback. Keep forged-origin-authorization
stripping in G3's host-input test; do not bypass local authentication to manufacture the ASK case.

Strip local authorization and all caller-supplied authorization before host input. Refuse the
exact local token bytes anywhere outside the designated credential field; they must not reach
the classifier, pending store, audit, launch log or dry run. Preserve G3's existing approved `host_env`/`fenced_file` origin-credential source and post-decision load path in [`loadHTTPCredential`](../../internal/callbroker/http_executor.go#L240-L263) unchanged. The
origin credential never enters the client environment or local front. No actual vendor or
transport credential is used in tests.

#### Generation, readiness and retirement

Reserve a nonzero address by selected front name before readiness; do not reuse a shared port.
Publish the URL, token and private trust settings together only after the listener is bound and
the selected host endpoint/generation is ready. A valid attach adopts that generation's token,
address and snapshots; it does not remint. Changed selection/binding or missing private state
requires a fresh launch, not opportunistic migration of workspace token records.

Retire by withdrawing admission and the host generation first, then canceling and draining
in-flight work before releasing the address and private files. Serialize withdrawal against the
host's final send admission. A request withdrawn before send has zero effects; a request that
may already have been sent is not refunded and is never automatically retried. An old client
reaching a reused address must meet a different certificate and token, not renewed authority.

#### Setup boundary and verification

Keep every intended setup in scope. The rows below name candidate mechanisms and required
proof, not verified support:

| Setup | Candidate vehicle and required proof |
| :--- | :--- |
| Podman self: rootful/rootless, private/shared network, nested | An outside-workspace per-jail payload and private install prefix. Verify peer mount exclusion, actual user/network namespace and capability effects, client traffic and cleanup in each intended mode. |
| Apple Container self | A separate per-jail payload/install bind or copy to a private destination outside workspace home. Never copy the token or managed startup into the workspace-backed jail home. Do not depend on an unsupported read-only suffix; verify the actual peer boundary and process separation natively. |
| Seatbelt self | Exact session-private payload, startup and install paths in staged reads/writes. Verify sibling-path, filesystem-alias and process-environment denial natively. |
| Seatbelt yolo account | The same per-session path restriction; account ownership alone does not isolate peer sessions. Verify exact reads, denies and cleanup natively. |
| Host | Keep private staging to avoid accidental workspace disclosure; unconfined same-owner host processes are trusted, not a malicious-peer boundary. Do not claim a host-process sandbox. |

A source review, synthetic helper test, shared Go cache, workspace alias, file mode, rootful
nested test or certificate construction alone proves neither custody nor native isolation. For
confined setups, the actual selected-client path must pass same-workspace A/B acquisition,
startup-tampering, replay and counted-effect tests, then each intended native setup's
mount/process/network checks. **Host acceptance is narrower but not excluded:** verify normal
selected-client and TLS behavior and absence of accidental workspace disclosure, but do not gate
host on hostile same-owner process acquisition or claim a host-process sandbox; same-owner host
processes are trusted by this boundary. Keep every intended setup in scope. No such native proof
exists in this draft.

### HTTP custody and retries

The jail-front loopback request carries a jail-identity preamble authenticated by host TLS,
not client-controlled `Yolo-*` authority. Check the selected binding; an unbound request
refuses with 403. An ask returns 403 with `Yolo-Verdict: asks`, a menu, and an opaque
`Yolo-Request` correlator that grants nothing.

Only a proven failure before any request byte was sent means the request was not run;
spending does not refund. If transmission was possible, record `ran` as an attempted transmission that may have
reached the remote host, not as confirmed completion or effect. Spending alone is not
evidence of `ran`. Cancellation, response cap,
and unusable responses never trigger an automatic retry or new grant: the client must
inspect host state. The existing CLI rule for an actually completed child process is
separate; not every HTTP transport error proves transmission.

The selected in-jail front and launch publication are not wired in the checked primary tree; these requirements are not runtime proof. No token store, keychain, privilege, global mount, parallel approval RPC, channel, grant, or source-specific registry is added.

## Decision Ledger

| ID | Ruling / Decision | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| <a id="OQ-HTTP1"></a>[OQ-HTTP1](#OQ-HTTP1) | **A pack's program that signs HTTP requests only returns the authentication header; core sends the request.** Core keeps enforcing the pinned origin, the redirect refusal and its response rules. The other option, a pack program that sends the request itself, is rejected: it would hold the credential and the network at once with core only auditing, which defeats the broker. The owner (2026-11-06): *"how is B even an option? you're just saying give them everything? then what's the point of the loophole?"* | 2026-11-06 | the owner's answer in chat; [custom-executor boundary](#http-custom-executor-boundary) | — |
| <a id="OQ-HTTP2"></a>[OQ-HTTP2](#OQ-HTTP2) | **Withdrawn: there is no question until an agent is shown unable to use its ordinary token field.** Until then the ordinary-field path proceeds. If one fails verification, only that client and setup is held, and its fallback is asked then, with the evidence. The owner agreed (2026-11-06) | 2026-11-06 | the owner's answer in chat | — |
