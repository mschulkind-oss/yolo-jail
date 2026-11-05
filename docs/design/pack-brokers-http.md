---
title: "Pack broker HTTP calls stay within the selected host decision"
date: 2026-11-05
status: in-review
stage: DESIGN
next: "Rule [OQ-HTTP1](#OQ-HTTP1) before specifying the HTTP pack-executor wire"
tags: [design, broker, packs, http]
summary: "Settled HTTP call safeguards with the custom-executor boundary open."
---

# Pack broker HTTP calls stay within the selected host decision

**Status:** 2026-11-06. The HTTP binding, bounded ingress/classifier and core decision
semantics are settled; the custom-executor boundary remains open. The primary source at `a1626df`
integrates G1/G2, but HTTP native lookups are still refused by
[`ClassifyHTTP`](../../internal/callbroker/http.go#L168-L170); G3 host decision/execution,
G4 front wiring and runtime proof remain outstanding. No external authentication or real
grants are established. This does not accept the broader [pack broker design](pack-brokers.md),
whose [OQ-BX7](pack-brokers.md#OQ-BX7) still governs permanent Add and mixed-cell timed
answers.

> **In short.** An HTTP front carries a call, not authority: only the selected host broker binds its destination and credential, classifies exact bytes and fresh facts, decides, audits and executes once.

**Start at [HTTP binding](#http-binding-and-executor).** Build order is in the [current plan](pack-brokers-http-plan.md).

**Reads with:** [`pack-brokers-http-plan.md`](pack-brokers-http-plan.md) (the current build
hand-off); [`pack-brokers.md`](pack-brokers.md) (shared terms, decisions, classifier and
executor rules); [`access-introspection.md`](access-introspection.md) (the shared decision
pipeline).

This scope excludes For/Add/Remember and mixed-cell timed approvals pending
[OQ-BX7](pack-brokers.md#OQ-BX7), MCP sign-in, actual CircleCI/cloud auth, and publication.
No new credential channel, grant, approval RPC or relaxed CLI limit is authorized.

**Needs your ruling:** [OQ-HTTP1](#OQ-HTTP1).

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
authentication material or conducts the request. That distinction is open in
[OQ-HTTP1](#OQ-HTTP1); no signer wire or expansion of credential channels, grants, or
approval behavior is accepted here.

## HTTP front, refusal and retries

### Shared and different surfaces

| | CLI front | HTTP front |
| :--- | :--- | :--- |
| In the jail | `intercept` puts a forwarder named for the program ahead of it on PATH | A loopback HTTP listener run by `yolo-jaild`; the pack's `env` names its URL in the variable the client reads, such as a base-URL setting |
| Identity, scope, decision, store, approvals, audit, dry run | Shared | Shared |
| What the classifier reads | argv and the stdin digest | method, path, query, allowlisted headers, body |
| Canonical form | argv | request bytes |
| Lookups | argv templates | request templates |
| Credential | The host program's own login | The core-owned transport path builds a header from a declared source; custom executor authority is [open](#OQ-HTTP1) |
| Executor hazards | Host file writes by flag, a server or token flag, spawned helper programs, git pushes | Redirects, cookies, credentials in URLs, response size, non-safe methods that read |
| A refusal reaches the agent as | Exit 64 and the refusal text on stderr, as `gh`'s | Status 403 with headers `Yolo-Verdict` and `Yolo-Request`, and a body in the error shape the block declares (GraphQL's `errors` array for the local HTTP proof), whose message is the refusal text |
| An ask reaches the agent as | Exit 75 and the menu | Status 403, `Yolo-Verdict: asks`, the menu in the message |

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

The HTTP front and executor are not wired at the checked source tree; these requirements are not runtime proof. No token store, keychain, privilege, global mount, parallel approval RPC, channel, grant, or source-specific registry is added.

## Open Questions

1. 💬 **OQ-HTTP1: Should an HTTP pack executor sign a request or conduct it?**

   Priya selects an HTTP pack that needs a body signature or per-call token, then approves
   a request. The parent design allows a pack executor after that decision; HTTP specifies
   core's pinned transport and a post-decision signer, but not whether the signer can send the
   request.

   - **A — Header-only signer.** The program returns authentication-header material; core
     sends the canonical request and enforces its origin, redirect and response rules.
   - **B — Full HTTP executor.** The program conducts the approved canonical request; core
     audits its result but cannot claim its own transport checks.

   <!-- vantage: question id=OQ-HTTP1 leaning="A — header-only signer. Keep broker-mediated transport checks in core; no HTTP pack requirement for executor-owned transport is established." -->

   _Leaning:_ A — header-only signer. Keep broker-mediated transport checks in core; no HTTP pack requirement for executor-owned transport is established.

   **Answer:**

   > _(empty — fill in when decided)_
