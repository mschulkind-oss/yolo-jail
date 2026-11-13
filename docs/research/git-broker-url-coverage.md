---
title: "Git broker URL coverage: supported, direct, and refused forms"
date: 2026-10-29
tags: [research, git, broker, ssh, urls]
summary: "Grounded audit and bounded repair of Git URL forms crossing the host broker, with exact-scope and no-network regression evidence."
---

# Git broker URL coverage

**Checked against:** `5594d2d68` at task start; the broker, protocol, remote helper, pack and existing broker authorities had no intervening changes from that audit base. The confirmed bracketed-IPv6 scp defect and two Git SSH aliases matched the audit packet.

**Finding at that audit:** `git@[::1]:o/r.git` was rewritten by the shipped `git@` rule, but the broker split at the first colon inside `[::1]` and refused it. Git's `git+ssh://` and `ssh+git://` URL schemes are valid SSH aliases, but neither was rewritten or accepted. At that audit the owner's reported SSH failure was unidentifiable because no URL or error had been supplied. The later owner-provided approved/request URL pair identified a separate literal-scope mismatch; ITEM18's narrow endpoint matcher is recorded below, without changing the earlier parser RED or QA history.

**Boundary:** SSH requests still need a live approved remote endpoint and read grant. Exact URLs match as before; additionally, Git's broker accepts SSH scheme aliases only when parsed destination, explicit port and effective path match, and the exact `git@github.com` no-port two-segment relative scp path may match the corresponding absolute SSH URL path, and for that same GitHub user and no-port form a single trailing `.git` on the repository segment is equivalent in either spelling. Host case/aliases, user, port, generic relative/absolute paths, other suffixes and HTTPS remain distinct. The original request URL remains in the remote helper, upstream SSH argv and audit. Repository identity groups grants; it does not itself approve another spelling. The existing anonymous public HTTPS fetch exception remains unchanged. This document supplements [`git-broker.md`](../reference/git-broker.md) and [`git-broker.md`](../reference/git-broker.md#the-builds-implementation-decisions); it does not widen their trust boundary.

## URL coverage

“Routed” means the pack rewrites the URL into `git-remote-yolo`; SSH and credentialed HTTPS still pass broker policy. “Direct” means the pack does not intercept the remote, so jail Git's normal behavior applies and host credentials do not cross by this broker.

| Input | Result | Scope and safety |
| :--- | :--- | :--- |
| `ssh://[user@]host[:port]/path`, including bracketed IPv6 address authorities and a `/~/` path | Routed to SSH broker | Exact approval plus read grant, with only bounded SSH endpoint equivalence: same parsed destination/explicit port/path across SSH schemes, or GitHub's two-segment relative/absolute exception. |
| `git@host:path`, including `git@[IPv6]:path` | Routed to SSH broker | Exact approval plus read grant; only `git@github.com:owner/repo` has the corresponding absolute GitHub SSH URL path equivalence. |
| `git+ssh://…`, `ssh+git://…` | Newly rewritten SSH aliases | Match the same SSH endpoint when destination, explicit port and effective path are identical; original request URL is retained. |
| `https://host[:port]/path` | Routed to HTTPS broker | Credentialed access requires exact URL approval and read grant. Public fetch may retain the existing anonymous out-of-scope path. URL user-info remains excluded from scope and is not a credential hint. |
| Valid scp forms such as `host:path` or `alice@host:path` | Direct in ordinary Git: no pack rewrite | A direct call does not reach the broker. If explicitly sent through the broker helper, scp-style parsing is available, but exact URL scope and read-grant checks still apply. |
| `http://`, `git://`, `file://`, local paths, or Windows-shaped local paths | Direct, not brokered | No host-file forwarding or new transport is introduced. A URL manually presented to the broker is refused unless it is one of the supported SSH/HTTPS forms. |
| `ext::`, `fd::`, other `<transport>::` helpers, option-shaped remotes, malformed URL/path forms, query/fragment, or SSH percent escapes | Refused if submitted to the broker | The broker does not execute host helpers, accept host options, or relax path parsing. Percent-escaped SSH input remains refused because Git's decoding and broker identity/argv interpretations can differ. |

Git itself may use SSH URL percent escapes differently from the old parser comment's claim; the comment now explains why the broker still refuses them. No blanket percent-decoding or identity canonicalization was added.

## ITEM18 — narrow SSH endpoint equivalence

The earlier repair above fixed parser and transport coverage while retaining literal scope checks. A later owner-provided request demonstrated a distinct mismatch: approval `git@github.com:mschulkind/dotfiles` and request `ssh://git@github.com/mschulkind/dotfiles` should reach the same GitHub endpoint. The Git broker now uses its existing SSH parser at the scope boundary; generic scope consumers still default to exact matching. The matcher adds only identical parsed SSH destinations, explicit ports and paths across `ssh`, `git+ssh` and `ssh+git`, plus GitHub's exact `git@github.com`, no-port, two-segment relative-scp/absolute-URL path pair. It drops one trailing `.git` only for that GitHub form (`git@github.com` with no port, two segments, `o/r.git` ≡ `o/r`, refusing `o/r.git.git` and `o/.git`); it never drops username, host case, port, trailing slash, `~/` or escapes, and generic scp-relative versus URL-absolute paths remain distinct. HTTPS remains literal.

Permanent real-Git tests cover successful `ls-remote` and clone through guarded local fake SSH/bare repositories, original URL audit retention, routed negative endpoints with no SSH dispatch or approval request, and live scope/read-grant revocation. URL comparison tests cover HTTPS, aliases, path and encoding boundaries without making public network requests. These results belong to this candidate only, not Main acceptance.

## Bounded implementation plan and result

1. Preserve the defect as a real-Git RED before production changes: run a real `git ls-remote` through the fixture's `git@` rewrite rules, remote-helper process, and broker fixture for `git@[::1]:o/r.git`; at that stage, the fixture's rewrite rules were hard-coded rather than loaded from the production manifest. Require the fixture's `main` ref and SSH dispatch. **Observed RED:** broker rejected host `[` before SSH ran. The output is preserved with the untracked durable run evidence, not in the source tree.
2. Parse bracketed scp IPv6 only when a closing bracket is followed by the host/path delimiter; validate it as an IP literal, preserve the bracketed host in repository identity, and pass the unbracketed host to SSH as real Git does. Keep exact URL scope matching.
3. Add the two aliases only because network-free real Git exercises confirm its `insteadOf` rewrite and SSH dispatch semantics. Add them to the pack and broker URL parser, not to scope matching. Drive the unit fixture from the shipped `packs/git/pack.json` environment contribution so tests fail if the production rewrite disappears.
4. Cover real Git fetch/`ls-remote` through helper and broker for ordinary SSH URL, both aliases, bracketed scp IPv6, and IPv6 URL with port; assert the audit contains the original exact URL. Also prove valid-but-direct scp, HTTP, git, file, local and Windows-shaped inputs remain unrevised using Git's no-network `ls-remote --get-url`; cover an unapproved alias with the same repository identity and a refused alias push whose upstream receives only the empty command list. Keep existing HTTPS user-info/credential tests, helper/file refusals, option-injection checks, and SSH command quoting tests.

Changed implementation seams: `internal/gitproto.Identity` now recognizes only the two explicit SSH aliases and bracketed scp IPv6; `internal/gitbroker.transportOf`/SSH parsing mirrors those schemes and parses the scp delimiter safely; `packs/git/pack.json` adds only the two alias rewrites; the packload and integration assertions follow the manifest's five rewrite rules. `packs/git/briefing/git.md` and the URL statements in the existing broker design/plan now describe the implemented surface without implying all Git URL syntaxes are intercepted.

## QA record

- **RED:** `go test ./internal/gitbroker -run '^TestRealGitRoutesBracketedIPv6ScpThroughBroker$' -count=1` failed before the fix with the parser error `host "[" has a character a host name cannot`; the failure is preserved in the untracked durable run evidence.
- **GREEN, focused:** `go test ./internal/gitproto ./internal/gitbroker -run 'Test(GitPackLeavesOtherValidGitURLsDirect|GitPackRewritesSSHFormsThroughTheBroker|SameIdentityDoesNotApproveAnotherSSHURL|SSHAliasPushStillRefusesAnUncoveredRef)$' -count=1 -timeout=60s` passed; the real Git tests use the production pack rules and local fixtures.
- **GREEN, package:** `go test ./internal/gitproto ./internal/gitbroker ./internal/packload -count=1 -timeout=150s` passed. Existing HTTPS tests also use real Git through the fixture helper and production pack rewrite, against a local TLS server.
- **Harness incident:** one earlier pre-guard broad test invocation timed out while exercising aliases; it showed no successful remote operation, but whether a system SSH connection was attempted cannot be proven. It is not accepted validation. The fixture now sets a fail-closed caller-side `GIT_SSH_COMMAND`, and all accepted caller evidence is from guarded reruns with local fake transports. Full history is preserved outside the source tree.
- **Whole-tree gate:** `just check-ci` passed after the source changes. Its complete output is saved in the untracked durable evidence bundle.
- **Nested bundle verification:** `just build-dev-bundle` passed. The emitted launcher, invoked by absolute path from an isolated workspace and a fresh no-packs `HOME`, completed an actual nested podman jail run and printed `nested-no-packs-ok` and the in-jail Git version. This verifies the Linux nested path, not rootless-only behavior; inherited unknown-setting warnings did not prevent launch.
- **Matching integration:** `go test -v -run '^TestGitBrokerFetchAndRefusal$' ./integration -count=1 -timeout=0` passed; verbose output confirms the test ran, not skipped.
- **Full integration:** `go test -count=1 -timeout=0 ./integration` passed (773.163s) on clean source commit `8fa41b4e5d87fa5ff0b6c0a6408d2ebf83ee37c7`; the command's recorded `HEAD` and clean status match that source commit. It includes the updated main ancestry through `727b94848` and hatch `99ba81be2`.
- **Completion:** `just done` passed on clean docs-only QA commit `3da31238a0b33c26fb593340f0a3281bd1fbfa9b`. The final handoff is another docs-only descendant of source commit `8fa41b4`; no Go, pack, manifest, or integration-test source changed after that fully tested source commit. This keeps the full-suite proof code-equivalent while the final handoff SHA is carried in the acceptance report.
- **Side-effect boundary:** the accepted broker-call tests are guarded and use fake local SSH/bare repositories or fixture TLS. The earlier pre-guard timeout showed no successful remote operation, but an outbound SSH attempt cannot be excluded. No authorized live credentials, grants, production deploy, or API/remote operation was part of the test plan.

## Sources

- [`git-broker.md`](../reference/git-broker.md) — exact URL scope, credential boundary, and SSH/HTTPS transport policy.
- [`git-broker.md`](../reference/git-broker.md#the-builds-implementation-decisions) — pack rewrite and broker implementation choices.
- [`packs/git/pack.toml`](../../packs/git/pack.toml) — shipped URL rewrite rules; the real-Git test reads this manifest contribution directly.
- `internal/gitproto.Identity`, `internal/gitbroker.transportOf` — implementation and refusal seams verified by the tests named above.
