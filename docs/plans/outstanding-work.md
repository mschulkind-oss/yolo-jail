# Outstanding work

**What this is.** Everything still to do, and nothing else. Restructured 2026-08-12 around the
two threads the maintainer named; shipped items have moved out to
[`shipped-2026-08-pack-batch.md`](shipped-2026-08-pack-batch.md).

**Read this as today's forward plan.** Every claim below was checked against the code on the date
it carries, not against a status marker — five stale "still open" markers turned up in these docs
over the last fortnight, so verify-against-code is the house rule.

---

## Status key

Scan the first column. **One status per row**, plus 🐛 when the item is a live defect rather
than new work.

| | Means |
|---|---|
| 🟢 | **ready** — no decision needed. Say the word and it starts. |
| 🟡 | **waiting on you** — a decision or open question blocks it. Every one is listed in [Every decision waiting on you](#every-decision-waiting-on-you). |
| ⛔ | **blocked** — on something that is *not* a decision (a Mac, an upstream merge). |
| 🔄 | **in progress** |
| ⏸️ | **held** — deliberately not being built. |
| ✅ | **done** |
| 🐛 | *(flag, not a status)* a live defect rather than new work. |

---

## The three active threads

| | | Thread | First move | Blocked on |
|---|---|---|---|---|
| 🟢 | **C** | [Open PRs + issues on the public repo](#thread-c--the-open-prs-and-issues-on-the-public-repo) | **land #37** (certain, already-occurring bug in the verification tool); #33 is **fixed** and needs only a reply on the issue | nothing; #32 has a question awaiting your answer |
| 🟡 | **A** | [Claude auth as swappable packs](#thread-a--claude-auth-as-two-swappable-packs) | move `shared_credentials` off the base `claude` pack | nothing |
| ⛔ | **B** | [macos-user + non-container nix](#thread-b--macos-user-and-non-container-nix) | run B-0 once on a Mac (the wiring landed 2026-08-12; nothing else in the thread moves until a Mac confirms it) | a Mac to verify; N3 is your call |

Everything else is [below](#everything-else-still-open).

---

# Thread C — the open PRs and issues on the public repo

All on [mschulkind-oss/yolo-jail](https://github.com/mschulkind-oss/yolo-jail); `.env`'s
`GH_TOKEN` reads and pushes `origin`, so no extra credentials are needed. Reviewed 2026-08-12;
every premise re-verified against local code rather than taken from a PR body.

**Two of these are from outside contributors, both awaiting a first response.** Each filed an
issue *and* a PR fixing it. Neither has any review or comment on it yet, and **all three PRs are
`MERGEABLE`** (no conflicts). One carries a direct question for you (C-3).

| | PR | Title | Author | Size | CI | Verdict |
|---|---|---|---|---|---|---|
| 🟢🐛 | [#37](https://github.com/mschulkind-oss/yolo-jail/pull/37) | image staleness: compare against most-recently-loaded path (fixes [#35](https://github.com/mschulkind-oss/yolo-jail/issues/35)) | Georgi Popov, **external** | +88/−4 | none reported | **land first** — premise verified locally |
| 🟢 | [#34](https://github.com/mschulkind-oss/yolo-jail/pull/34) | weekly `flake.lock` bump | `github-actions` bot | +3/−3 | none reported | routine; inert until an image rebuild |
| 🟡 | [#32](https://github.com/mschulkind-oss/yolo-jail/pull/32) | macOS+podman broker transport (fixes [#31](https://github.com/mschulkind-oss/yolo-jail/issues/31)) | Dong Liu, **external** | +1064/−13 | **green** (integration + secrets-scan pass; check-macos skipped) | land, then **promote** — and it has a **question for you** |

| | Issue | Title | Author | State |
|---|---|---|---|---|
| 🟢 | [#35](https://github.com/mschulkind-oss/yolo-jail/issues/35) | Stale `:latest` reused after reverting config | Georgi Popov | fixed by #37 |
| ✅ | [#33](https://github.com/mschulkind-oss/yolo-jail/issues/33) | **`ca.key` is mounted into every jail** | Dong Liu | **fixed 2026-08-12** (C-4) — closable; still open upstream until someone replies on the issue |
| 🟢 | [#31](https://github.com/mschulkind-oss/yolo-jail/issues/31) | Broker relay socket unreachable on macOS+podman | Dong Liu | fixed by #32 |

## 🟢🐛 C-1. #37 — a silent stale-image bug in the tool you verify with

### What it is

Georgi Popov filed [#35](https://github.com/mschulkind-oss/yolo-jail/issues/35) — *"Stale `:latest`
image can be silently reused after reverting config to a previous value"* — and then #37 fixing it.
(He first fixed it against the pre-rewrite **Python** implementation in #36, noticed the project had
moved to Go, closed that, and redid it here. So he read the codebase rather than pattern-matching.)

### The mechanism

`AutoLoadImage` decides whether the loaded image is current. Today
`internal/image/autoload.go:232` asks `loadedPaths[currentPath]` — *is this store path anywhere in
the last-ten-loads sentinel history*, an **unordered set** from `ReadLoadedPaths`.

Nix builds are content-addressed, so **reverting a config change reproduces a store path you built
before** (remove a package, re-add it — same path). If that path is still in the ten-entry history
while a *different, newer* path is the runtime's actual `:latest`, the check answers "already
loaded", skips the reload, and `:latest` stays stale — **indefinitely, with no error or warning**.

### The fix, and why it is two lines

`internal/image/image.go:119` **already has `CurrentLoadedPath`**, which returns only the
single most-recent sentinel entry. Its doc comment explicitly distinguishes it from
`ReadLoadedPaths`'s *"whole LRU"*. **It has zero non-test call sites** — written for exactly this
and never wired up (verified locally 2026-08-12). #37 wires it in and adds a regression test that
loads path A, then B, then A again, and asserts the third call actually reloads.

### Why it outranks its +88/−4

It lands on this repo's **mandatory verification method**. `AGENTS.md` requires nested-jail
verification for every `cmd/`/`internal/` change, and already warns that *"a failed nix build does
not stop the jail… so a broken build looks like a working jail running stale code."* #37 is a
second, quieter route to the same false green — and unlike the build-failure route it is reached by
**ordinary edit-revert-re-edit iteration**, which is what developing looks like. A bug in the tool
you verify with costs more than its line count, because every result it produces is suspect.

### Before merging

No CI checks reported. Re-run against current `main` — `autoload.go` and `image.go` have not moved
locally, so it should apply cleanly.

## 🟢 C-2. #34 — real, correctly paced, and self-suppressing

Three questions worth answering, since this recurs weekly:

**Is it changing anything real?** Yes. The diff moves nixpkgs `643809054d…` → `f13ff45afd…`, a
`lastModified` gap of ~4.8 days of nixpkgs. It is not a no-op churn PR.

**Is it too frequent?** `cron: "0 6 * * 1"` — **weekly, Mondays 06:00 UTC**. For a lock that the
jail image's whole toolchain rides (mise, node, python, go), weekly is a reasonable floor; less
often means larger, harder-to-attribute jumps when something breaks.

**Does it drop itself when nothing changed?** Yes — `DeterminateSystems/update-flake-lock@v28` runs
the update and opens a PR only when the lock actually moves. There is no "empty bump" PR to
suppress; if nixpkgs had not moved, no PR would exist.

**The one real caveat:** merging changes nothing until an image rebuild (`just load && just install`
on the host), so it is safe to land and easy to forget. Worth pairing with one nested-jail run so
the new lock is actually exercised rather than merely merged.

### What the three changed lines actually are

The diff looks trivial because a lockfile is a **pointer**, not content. The four fields:

| Field | What it is | Is it a filter? |
|---|---|---|
| `rev` | the **git commit SHA** of nixpkgs. This is the whole input — it determines every package version in the image | no, it is the result |
| `narHash` | a content hash of the **fetched, unpacked tree** (NAR serialization). Nix verifies the download against it | no — integrity check |
| `lastModified` | the **commit timestamp** of that `rev`, Unix epoch. Descriptive metadata only | **no** — see below |
| `owner`/`repo`/`type` | where to fetch from | no |

**`lastModified` is not a version filter on a remote database.** Nothing queries by it. What
decides *what gets resolved* is the `original` ref in `flake.nix` —
`nixpkgs.url = "github:nixos/nixpkgs/nixos-unstable"`, i.e. **track the `nixos-unstable` branch
head**. `nix flake update` asks GitHub for that branch's current tip, writes the resulting `rev`,
and records `lastModified` because that is when the tip commit was authored. It is a "how stale is
this pin?" readout, not a constraint.

So the three lines say: *the nixos-unstable branch head moved from commit A to commit B, and here
is the hash proving we fetched B intact.*

### What it does NOT tell you, which is the real gap

**A `rev` bump signals nothing about the packages this image actually uses.** Between two
nixos-unstable tips, ~100k packages may have changed, of which the image consumes a few dozen. The
PR body says "picks up current nixpkgs on the next image rebuild" — true, and unfalsifiable by
reading it. A reviewer cannot tell an openssl CVE fix from a README typo in a package nobody here
installs.

**That is fixable and worth doing:** have the workflow build the image derivation on both the old
and new lock and post `nix store diff-closures` output into the PR body. That turns three opaque
lines into "go 1.25.1 → 1.25.3, openssl 3.5.2 → 3.5.4, +2 MiB" — which is a thing a human can
actually review. **Shipped — see D3 below.** What it diffs is not the image derivation but
`.#imageClosureRoot`, a new flake output whose closure is the image's contents *minus our own Go
build*: a lock bump cannot move our binaries, and excluding them keeps both sides of the diff a
download from cache.nixos.org rather than a build.

## 🟡 C-3. #32 — land it, then promote it; do NOT close it as subsumed

Full analysis: [`agent-auth-modes.md`](../design/agent-auth-modes.md) §12. The three points that
matter for prioritization:

1. **It is not subsumed by the auth work.** The broker exists for the OAuth refresh race, so a
   **Bedrock-mode jail never touches the path #31 breaks** — but Teams mode on macOS+podman still
   does. Auth modes make "Claude Code won't start on macOS" *conditional on the mode*, not fixed.
2. **Yes — this is a general loophole-transport problem, and it now has its own design doc.**
   The virtiofs socket problem is a property of the boundary, not of the broker: B1/B1b/B2 are all
   socket-reached host daemons and would each rediscover [#31](https://github.com/mschulkind-oss/yolo-jail/issues/31)
   on a Mac. **Read [`loophole-transport.md`](../design/loophole-transport.md) before implementing
   or standardizing anything here** — it leans heavily on #32 (which is the working implementation
   of most of it), covers why each piece of the design is load-bearing, proposes transport as a
   framework property rather than a `brokerrelay` one, and carries the `ca.key` fix (C-4) plus five
   open questions including #32's own.

   **Its recommendation is NOT to generalize first:** land #32 as scoped, and generalize when the
   second consumer appears. #32 is a working fix for a total outage on one platform and should not
   be held hostage to a refactor.
3. **Its per-jail bearer token is the client-auth upgrade the boundary work independently reached**
   — the third convergence on unYOLO's "named broker-client secret"
   ([`boundary-broker.md`](../design/boundary-broker.md) §10.3), against yolo's current *"the socket
   file is the authentication"*.

**It does not fix the CA key exposure** it works around — see C-4. Merging #32 must not be read as
closing that.

### The question #32 is waiting on you to answer

The PR ends with an explicit ask:

> *"I keep the terminator↔relay hop on a per-jail token + pinned host-only-key TLS (no client
> certs). Happy to switch to full mTLS if you'd prefer."*

**Nobody has answered it.** Worth deciding alongside OQ-8 (generalize the transport, or merge as
scoped), because if the transport becomes the loophole framework's, the answer applies to every
future host service rather than to one hop. A per-jail bearer token inside pinned TLS is already
strictly stronger than the current *"the socket file is the authentication"* posture, so "as
proposed" is a defensible answer — it just needs to be given.

## ✅🐛 C-4. Issue #33 — `ca.key` in every jail — **DONE 2026-08-12**

> **DONE.** The loophole manifest gained an optional `state_files` key naming the state-dir subset
> that crosses into a jail; the shipped broker declares `["ca.crt", "server.crt", "server.key"]`,
> each mounted as its own `:ro` file, and the state **directory** no longer crosses. Omit the key
> and the whole dir mounts as before, so no external manifest changed meaning
> (`internal/loopholes/{load,runtime}.go`).
>
> **Verified in a nested jail**, not inferred: `/var/lib/yolo-jail/loopholes/claude-oauth-broker/`
> holds exactly `ca.crt`, `server.crt`, `server.key`; `ca.key`, `ca.srl`, `leaf.cnf` and
> `refresh.lock` are absent; the in-jail terminator serves TLS with the mounted pair and `curl`
> verifies it against the mounted CA (`ssl_verify_result=0`). Pinned by
> `TestBundledBrokerNeverMountsItsPrivateKey`, which fakes a full state dir against the manifest
> that actually ships — so removing or widening `state_files` fails a test rather than a jail.
>
> Design note and the answer to OQ-T6 (narrow, but declared in the manifest rather than switched on
> the broker's name): [`loophole-transport.md`](../design/loophole-transport.md) §5.1.
>
> **Still worth reporting on #33 as a fix, not a dismissal** — the severity downgrade below stands,
> and the issue is closable.

> **Severity downgraded 2026-08-12 after review.** This section previously called it "the most
> serious item in this thread" and argued it should jump ahead of #37. **That was overstated.** It
> is **not an auth-escalation bug**: `ca.key` supplies a trusted certificate but not the ability to
> redirect a victim's traffic, and against the attacker's *own* jail it adds nothing a UID-0
> process cannot already do. Full worked analysis in
> [`loophole-transport.md`](../design/loophole-transport.md) §5.0.
>
> **Revised ranking: #37 first.** #37 is a certain, already-occurring bug in the verification tool;
> this is a conditional lateral-movement issue. Still worth fixing — it is free, it is
> least-privilege, and it is publicly filed — just not ahead of #37.

**Nobody is working on it.** Dong Liu
filed it separately while writing #32, whose body explains why it matters there: the broker's whole
state dir — **including `ca.key`** — is mounted `:ro` into every jail, so a malicious jail could
sign a `yolo-broker-relay` cert and MITM a sibling. That is why #32 pins an exact host-only-key
cert instead of trusting the broker CA: **verifying against that CA is worthless.**

**The fix is described in [`loophole-transport.md`](../design/loophole-transport.md) §5**, alongside
the transport design it is inseparable from — #32 pins its own cert *because* the broker CA cannot
be trusted, so a reader who merges #32 without this may reasonably conclude the CA problem was
handled. It landed **before** #32, as that argument requires.

This is the same defect `ROADMAP.md` §4d records from the internal audit — independently found and
now **publicly filed**. Combined with `NODE_EXTRA_CA_CERTS` trusting that CA inside the jail, a jail
process can mint a trusted leaf for *any* host.

### Why it was not acted on, and what else was not

Because **the audit produced findings, not work items.** `ROADMAP.md` §4d recorded four verified
defects around 2026-08-02 and **none was carried into this queue**, so every planning pass since
scoped from a list that did not contain them. The pack batch that followed was scoped from here.

**Re-checked 2026-08-12 — two of the four are now fixed.** Full table and evidence in
[`loophole-transport.md`](../design/loophole-transport.md) §5.2; summarized:

| §4d defect | State | Now tracked as |
|---|---|---|
| `ca.key` readable in-jail | ✅🐛 fixed 2026-08-12 | **C-4** (this row) — `state_files`, verified in a nested jail |
| Claude creds symlink dangles on macos-user | ⛔🐛 open | **B-1**, and it blocks Thread A's Teams mode on macOS |
| Config-approval snapshot is agent-writable | 🟡🐛 open | **D1** below — re-measured: `.yolo/config-snapshot.json` is mode `664` and writable in-jail |
| Two shipped docs contradict the code | ✅🐛 fixed 2026-08-12 | **D2** below — both refresher claims corrected |

**The process lesson matters more than the four items:** an audit whose output lives only in a
narrative doc is invisible to planning. Findings have to become queue rows the day they are found,
or they age quietly until an outside contributor files one as a public issue — which is exactly
what happened.

**What it looked like, measured from inside a live jail 2026-08-12** — not inferred from the audit:

```
$ ls -l /var/lib/yolo-jail/loopholes/claude-oauth-broker/
-rw-r--r--  ca.crt      -rw-------  ca.key      -rw-r--r--  ca.srl
-rw-r--r--  leaf.cnf    -rw-r--r--  refresh.lock
-rw-r--r--  server.crt  -rw-------  server.key
$ head -c 28 …/ca.key    →  -----BEGIN PRIVATE KEY-----      (3268 bytes, readable)
```

**The `0600` mode was not a mitigation.** A yolo jail runs its agent as UID 0 (Claude YOLO is
`--dangerously-skip-permissions` plus `IS_SANDBOX=1`, which exists precisely to bypass the UID-0
refusal), so owner-only permissions were no barrier — as the read above demonstrates.

**And after the fix**, from a nested jail on the built binary:

```
$ ls /var/lib/yolo-jail/loopholes/claude-oauth-broker/
ca.crt  server.crt  server.key
$ ls …/ca.key   →  No such file or directory
```

**One correction to the fix as it was scoped here:** this row said *"only `server.crt` /
`server.key` are needed in-jail"*. **`ca.crt` is needed too** — `NODE_EXTRA_CA_CERTS` points at it
and the entrypoint merges it into `$HOME/.yolo-ca-bundle.crt`, which `SSL_CERT_FILE`,
`CURL_CA_BUNDLE`, `GIT_SSL_CAINFO` and `REQUESTS_CA_BUNDLE` all reference. Dropping it would have
broken every in-jail TLS client. The line that matters is **public vs private**, not **ca vs
server**.

---

# Thread A — Claude auth as two swappable packs

**The ask:** two packs — one Teams, one Bedrock — cleanly shareable, toggled by swapping which is
selected. Manual swapping is fine for now.

**Design docs:** [`agent-auth-modes.md`](../design/agent-auth-modes.md) (the mode model),
[`pack-config-collaboration.md`](../design/pack-config-collaboration.md) (why `config-overlay`,
not `config`), [`boundary-broker.md`](../design/boundary-broker.md) §10 (prior art; declines this
problem).

## ℹ️ A0. The verdict: expressible today, with one move and one hazard

Checked against the kinds on 2026-08-12. **No new kind is needed.** A mode pack is:

| Piece | Kind | Notes |
|---|---|---|
| model IDs (`model`, `ANTHROPIC_DEFAULT_OPUS_MODEL`) | `config-overlay` → `claude/settings` | **not `config`** — the `claude` pack owns that surface, and two `config` owners is a loud collision by design |
| `CLAUDE_CODE_USE_BEDROCK=1`, `AWS_REGION` | `env` | literal, non-secret |
| the OAuth credential channel | `hook: shared_credentials` + machine-scope `state` | today these sit on the base `claude` pack |

**The secrets constraint is a feature, not an obstacle.** The `env` kind's contract is *"literal
strings only — no interpolation, no secrets, no host references"*. So a Bedrock pack carries the
**shape** (which backend, which region, which model namespace) and the user supplies AWS keys via
`env_sources`. That separation is exactly what makes the pack shareable — the maintainer's own
requirement, delivered by an existing constraint rather than by new work.

## 🟢 A1. The one structural move

**`shared_credentials` and the machine-scope `.claude-shared-credentials` state must move off the
`claude` pack onto `claude-teams`.** Today the base pack owns both, so "select `claude` alone"
implies subscription auth. For modes to be swappable, the base pack must be auth-neutral.

Consequence to accept deliberately: **selecting `claude` with no auth pack yields no credential
sharing.** That is correct — it makes the mode an explicit choice — but it changes the behavior of
a shipped pack, so it wants the render fingerprint gate run before and after.

## 🟡 A2. The hazard: nothing prevents selecting both

Two auth packs selected at once yields `CLAUDE_CODE_USE_BEDROCK=1` from one, the credentials hook
from the other, and model IDs from whichever overlay lands last. **That is precisely the silent
wrong-state the whole mode model exists to prevent** — and it is the failure the maintainer's own
manual switch already demonstrated once (see `agent-auth-modes.md` §2.3, where a hand-switch left a
Bedrock model pin behind).

Manual swapping is the agreed near-term answer, but the failure must be **loud**. Three options,
ascending:

1. **A `conflicts` field in the manifest** — a pack names packs it cannot coexist with; refuse at
   load. Most general, smallest concept.
2. **Reuse the S1 collision machinery** for `env` keys and overlay keys — two packs setting the
   same key refuse, naming both. Consistent with skills; may be too broad (legitimate overlays
   exist).
3. **Document it and accept it for now.** Cheapest, and the one that fails silently.

**Recommendation: (1).** It is the only one that expresses the actual invariant — these two packs
are alternatives — rather than catching a symptom.

## ✅ A3. Host support — SOLVED, and it no longer needs N3

An earlier version of this row said `env` is "refused at the host notch" and that Thread A was
therefore blocked on N3. **That was imprecise, and the correction removes the dependency.** Full
reasoning: [`agent-auth-modes.md`](../design/agent-auth-modes.md) §11.2.

`HostFields()` *includes* `KindEnv` — the notch can express it. What refuses it is
`hostUnimplemented`, whose recorded reason is scoped to the **command**: *"`apply --host` only
configures your tools — it never runs them."* The code comment says so deliberately, precisely so
`guest` does not inherit a refusal it could honor.

**The design answer is to not need the verb.** Claude Code reads an `env` block out of
`settings.json`, so a Bedrock pack puts `CLAUDE_CODE_USE_BEDROCK` and `AWS_REGION` there via
**`config-overlay`** — which renders at **both** notches — rather than via the `env` kind, which is
jail-only until yolo owns the launch. Auth-as-packs is host-complete now.

`hook`, by contrast, **is** refused at the host as policy, and that is correct: a host has one real
home and one credential file, so `shared_credentials` is meaningless there. The Teams pack is
near-empty at the host notch by design (§11.3).

## 🟡 A4. Composition — packs cannot depend on other packs

Verified 2026-08-12: **no pack→pack mechanism exists.** The `requires` kind takes a `bin`, not a
pack name — it asserts a binary is on `PATH` and carries install hints. See
[`agent-auth-modes.md`](../design/agent-auth-modes.md) §11.1.

So composition today is the flat, ordered, user-scope `packs` list:
`["claude", "claude-bedrock", "matt-bedrock-extras"]`. That is adequate for manual swapping — the
list *is* the mode selector — but nothing stops a personal pack being selected without the auth
pack it was written for.

**A `requires_pack` contribution is the minimal fix**, and it pairs with A2's `conflicts`: one
names packs that must be present, the other packs that must not be. Both are load-time checks over
a list yolo already has. **OQ-5.**

The Tavily case needs no new mechanism (§11.4): MCP servers are config under `mcpServers` in
`claude/config`, the delivery path already supports `requires_env` gating, so the personal pack is
a `config-overlay` plus a key in `env_sources`.

## ℹ️ A5. Order of work

1. **A1** — move the hook and state onto `claude-teams`; fingerprint before/after.
2. **Build `claude-bedrock`** — `config-overlay` for the env block *and* model IDs, no secrets, no
   `env` kind (A3).
3. **A2** — make double-selection loud.
4. **B4 below** — correct `agent-credentials.md` §3 while in the area; §11.2 shows it described the
   right mechanism before anything used it.
5. **A4 / OQ-5** — `requires_pack`, if the flat list proves insufficient.

**Decide before step 2: shipped or fetched?** Adding shipped auth packs breaks several tests that
hardcode the official six (`packload_test.go`, `packconfigexclusive_test.go`, the briefing/skills
source tests). A separate public pack repo matches "shareable" better and exercises the
fetched-pack approval path. **OQ-6; recommendation: fetched.**

**Still unrun and decisive for the ambitious version:** the `ANTHROPIC_BASE_URL` test
([`agent-auth-modes.md`](../design/agent-auth-modes.md) §6) — does Claude Code send a subscription
OAuth bearer to a non-Anthropic base URL? If yes, a proxy gives no-restart switching. If no,
pack-swapping is the ceiling. ~5 minutes; nothing downstream should be built before it.

---

# Thread B — macos-user and non-container nix

**Design docs:** [`handoff-guest-notch-macos.md`](handoff-guest-notch-macos.md) (the Mac work in
one place), [`noncontainer-nix-environment.md`](../design/noncontainer-nix-environment.md) (§7 is
load-bearing, §8 has the options),
[`macos-user-nix-and-features.md`](../design/macos-user-nix-and-features.md) (the backend — see the
correction below).

## ⛔🐛 B-0. macos-user renders ZERO pack surfaces — wired 2026-08-12, Mac-unverified

**The Go side is done; the status is ⛔ and not ✅ because no Mac has run it.** The defect was an
ORDERING one: `internal/cli/run/run.go`'s `rt == "macos-user"` branch returned at the
`o.MacosUserRun(...)` call, which sat **before `stagePacks`**, so `YOLO_PACK_ROOT` was never set and
`RunDarwinBootstrap`'s `LoadJailPacks` / `ConfigurePackSurfaces` / `RunPackHooks` loops each iterated
an empty list — a backend that looked provisioned and configured nothing, with no error and no
warning.

What changed:

- **Staging moved above the backend dispatch.** `Run()` now stages once, before choosing a backend,
  and hands the result to whichever arm runs (`stagedPacks`). No backend can be dispatched with an
  undecided pack set. Pinned by `TestPacksAreStagedBeforeBackendDispatch`, which asserts the handler
  receives a root that already holds the staged manifest — a path argument is easy to thread wrongly,
  so the test checks the tree, not the string.
- **The tree is staged root-owned.** `/var/yolo-jail/packs/<session>`, copied by sudo and made
  `a+rX` — the macos-user analogue of the container's `:ro` `/ctx/packs`, and for the same reason: a
  pack manifest is an INPUT to composition, so an agent that could rewrite one could grant its own
  pack a host file next launch. It deliberately does NOT point at the invoking user's
  `~/.local/share/yolo-jail`, which is the boundary this backend exists to enforce.
- **`YOLO_PACK_ROOT` is baked into the bootstrap argv**, and three `PlanInvariants` now refuse a plan
  that stages a tree nobody names, names a tree nobody stages, or puts the tree outside the
  root-owned dir.
- **`LoadJailPacks` no longer swallows every `ReadDir` error.** Absent still means "render nothing";
  anything else (a root that is a file, unreadable, or on a mount that did not appear) is now
  A12-fatal instead of an indistinguishable empty set — the same silence in the entrypoint that the
  pipeline had.

**What is verified, and from where.** The whole decision surface is unit-tested on Linux (the run
pipeline's ordering, the plan's stage commands + env, the invariants, the entrypoint split). The
render fingerprint gate is byte-identical to HEAD (10 files, hashes compared A/B in a HEAD worktree),
and a nested jail launched from the freshly built binary still stages packs and renders a real
surface (`~/.claude/settings.json`).

**What is NOT verified — this is the ⛔.** No Mac has executed the sudo stage commands, and nothing
outside a Mac can: whether `_yolojail` can read the staged tree, whether the bootstrap's surfaces
land in the sandbox home, whether the hooks run. **Do not mark this ✅ on the strength of the Linux
suite.** One `yolo run --dry-run` on a Mac shows the plan (it now prints the pack root, or
`none staged`); one real launch closes the row.

**Also still open on this backend:** skills and briefings do not reach a macos-user home at all —
they cross into a container as bind mounts, and this backend has none. That is a separate gap from
B-0, not a leftover of it, and it is unclaimed.

> `macos-user-nix-and-features.md`'s `packs` row is corrected to ⚠️ with the same caveat. (It
> previously read "`agents` selection ✅ — `YOLO_AGENTS` → per-agent config": a ✅ for a mechanism
> that no longer exists, on a backend that rendered nothing.)

The abstraction question this was meant to answer — can `render.Target` express a non-container
backend? — is answered **not yet, and it did not need to be**. macos-user renders at the JAIL notch
(`Env.renderTarget()` → `render.Jail`) with a real macOS home, which is what it did before this fix
and what it still does. Nothing here required a new Kind. B-3's `guest` notch is where a Target must
actually describe a non-container confinement, and it remains unstated.

## ⛔🐛 B-1. The other confirmed macos-user defects

From `ROADMAP.md` §4c, none fixed:

- **Claude's credentials symlink DANGLES.** `ensureCredentialsSymlink` runs unconditionally in
  `RunDarwinBootstrap`, but the target dir is provisioned only by `storage.EnsureGlobalStorage` +
  the container bind mount, neither of which runs there. **This blocks Thread A's Teams pack on
  macOS** — the subscription mode cannot work until it is fixed.
- **The OAuth broker is unwired**, by a decision that addressed sharing but not serialization.
  `BrokerSocketGrantCommands` exists with zero call sites.
- **Bedrock creds do not reach a macos-user jail** — the delivery path rides `/ctx/host-claude`,
  which does not exist there. **Also blocks Thread A**, on the other mode.
- **`env_sources` secrets are on the process argv** (`env -i K=V…`), visible in `ps` to every user
  on the Mac — and they reach the launch argv but not the bootstrap argv, so MCP `${VAR}` gating
  silently drops every secret-gated server.

## 🟡 B-2. N3 — the decision that is yours

Full study: [`noncontainer-nix-environment.md`](../design/noncontainer-nix-environment.md) §8.
N1 and N2 were Option 1 and are **done**.

- **Option 0** — stop here. `install_hints` keeps printing a remedy the user runs by hand.
- **Option 2** — `yolo --at host -- <cmd>`. `--at` is `apply`-only today
  (`internal/cli/apply.go:54`). Makes `env` and `launch` renderable at the host, because yolo would
  then be the process spawner. Against: "yolo launches your host agent" is a bigger product claim
  than "yolo configures it."
- **Option 3** — a yolo-owned `nix profile` as a confirm-gated install remedy.

**Recommendation: still Option 2, but it is no longer urgent.** A3's correction routes Thread A
around the `env` refusal entirely (via `config-overlay` into the `settings.json` env block), so
Option 2 is back to being about the two refused kinds and the `guest` notch — worth doing, not
blocking anything.

## ⛔ B-3. P7 — the `guest` notch

Not built; host/Mac-gated rather than design-blocked. `render.KindGuest` exists with no behavior;
`render.UndecidedModes(reason)` is its fail-closed mode census. Needs B-0 first — same abstraction.

## ⛔ B-4. Also Mac-gated, collected

| | Item | What is needed |
|---|---|---|
| ⛔ | **D4 Cachix** | one real download proof; everything else done 2026-07-22 |
| ⛔ | **E8's nightly** | the first nightly AFTER the next release (`publish.yml` is tag-triggered) |
| ⛔ | **`cache_relocations`** | one real cross-filesystem move as an acceptance step |

---

# Packs implementation — what is actually left

**Substantially done.** The ten-item batch plus S1–S3/C1–C3 shipped; the reform (14 kinds,
`yolo pack footprint`, host render, config collaboration) is complete. What remains is one live
gap, one decision the S4 audit surfaced, and a tail of small items.

| | # | Item | Kind | Blocked on |
|---|---|---|---|---|
| 🟡🐛 | **S5** | **A jail resolves a skill-name collision SILENTLY** — the notch S1 does not reach | live gap | nothing |
| 🟡 | **S4** | **AUDITED 2026-08-12 — the gate holds.** `into` does reach an unselected agent's dir, and that is the model, not a hole. What the probes DID find is a notch asymmetry in fan-out | audit done, one decision left | **your call** (OQ-S4) |
| 🟡 | **E1+E2** | `host_files` modes 4→3, `readonly` as a real `:ro` mount | behavior change on a shipped key | a design pass (E2 first) |
| 🟢 | **E3** | Capture on terminate (the `yolo config capture` half shipped) | small | nothing |
| 🟡 | **E4** | **`rmw` SHIPPED 2026-08-12; `computed` ruled out as vacuous; `stateful` is a different problem** — see below | one mode left, and it wants a decision | **your call** (OQ-E4) |
| ⏸️ | **E5** | `managed`/`defaults` array-append pinning | small | **do not build speculatively** |
| ✅ | **V2** | **SHIPPED 2026-08-12.** `apply --host` converges after ONE apply — a filled `defaults` key no longer relabels itself `host` on the second | was: pre-existing, in `config` | — |
| ✅ | **V3** | **SHIPPED 2026-08-12.** One archive bucket per kind (`skills`/`files`/`briefing`, plus `retired`); legacy `archive/skills` copies stay put and stay reclaimable | was: cosmetic | — |

## 🟡🐛 S5 — the detail

**Measured 2026-08-05**, same two-pack set `apply --host` refuses: the jail came up, `~/.codex/skills/mine`
held the local pack's copy, the other pack's skill was absent, nothing said so.

**Not a regression** — S1's ruling is explicit that the collision is fatal *at apply time*, and
`internal/agents/skills.go` has no collision concept. But it is the same silent loss the ruling
exists to remove, and it is now the **only** place it survives.

**Why it is a decision, not a port.** Refusing to START a jail is much heavier than refusing to
write a real home, and A12's fatal-generator policy would make it exactly that. Three options
ascending: a **warning** naming both packs at launch (cheap, closes the "nothing said so" half); a
**`yolo check` failure** (loud where the user is already asking, non-fatal at launch); a **boot
refusal** (consistent with the host, and the one that can strand someone mid-task). The cheap half
is worth doing regardless — the destinations and layers are already computed, and
`hostskills.Collisions` is a pure function of them.

## 🟡 S4 — the detail

**AUDITED 2026-08-12.** Both readings of the code were right about what it does. The conclusion
drawn from them — that the selection gate has a hole — is **wrong**, and this section now says so
in enough detail that nobody re-audits it. All three probes were run; probe 1 twice, the second
time in a real nested jail.

**Probe 1 — `into` is honored against no agent set. TRUE.** `packs: ["claude", <content pack
declaring `into: ".codex/skills"`>]`, nested jail, temp `HOME`:

```
=== .codex ===
/home/agent/.codex/skills:
configuring-the-jail  diagnosing-the-jail  jail-startup  rogueskill
```

The directory is created, populated, and bind-mounted, with no `codex` pack selected.

**Probe 2 — every pack's skills reach every destination. TRUE.** `packa` → `.alpha/skills` and
`packb` → `.beta/skills`, one distinct skill each. Both destinations staged **both** skills.

**Probe 3 — the notches disagree, and not only on collisions.** The same two-pack set through
`apply --host --assert` into a temp home:

```
skills  <home>/.alpha/skills composed from: packa
skills  <home>/.beta/skills  composed from: packb
```

One skill each. The jail delivers the full cross product; the host delivers only the declared
pairing.

### Why this is not a hole in the gate

**`into` is a PATH, and core has no agent to check it against.** That is the pack model's opening
premise (`internal/packdecl`'s package comment: *"the core deliberately does not know what an
'agent' is"*). There is no registry, so "an agent the user never selected" is not a thing the code
can express — and the `claude` pack reaches `.claude/skills` by exactly the same mechanism the
content pack reaches `.codex/skills`: it declared the path.

**The gate is on loading, and delivery IS a subset of it.** Every destination in the jail traces
to a `skills` contribution on a pack in `packs`, and `packs` is user-scope only — so a
repo-committed config still cannot add a destination, and neither can a fetched pack the user did
not name. The mise-trust shape was an enforcement point that a lower layer could bypass; nothing
bypasses anything here.

**Selecting a pack is consent to its declared destinations**, and they are inspectable before the
fact: `yolo pack footprint <dir>` prints the `skills` line with its `into`.

### What the probes DID find

**A pack's declaration understates where its content goes.** Probe 2 is the real result: `packa`
never named `.beta/skills`, and its skill landed there. So the reviewable artifact — the manifest,
and `yolo pack footprint` which reads it — is accurate about *destinations the pack creates* and
silent about *destinations the pack reaches*. For a fetched pack scoped by its author to its own
directory, "I installed it for tool X" is not what happens; its skills reach every selected
agent's skills dir.

**This is intended, and load-bearing.** `skills` is `CombineMerge` (pack-system.md §3) — a
zero-ceremony pack is *"a bare `skills/` dir, no contribution"* and delivers only because the merge
is global; the shipped packs *"carry no skills of their own (their contribution exists to NAME the
destination other packs merge into)"*. Narrowing the merge naively deletes the zero-ceremony
promise.

**But the host already refuses to do it**, and says why —
`packload.ResolveDestinations`' doc comment:

> That is narrower than the jail, deliberately: in a jail the skills source list is GLOBAL (every
> pack's skills reach every destination) […] Mirroring that here would mean an existing manifest
> suddenly writes into home directories its author never named, which is not a fix anyone asked for.

That reasoning is about a real `$HOME`. Whether it transfers to a container is the question this
audit leaves behind — **see OQ-S4**. Note the mechanism the host uses (`ResolveDestinations`, which
gives a *silent* pack borrowed destinations and leaves a *declaring* pack alone) is never called on
the jail path; the jail has its own ad-hoc global merge instead. That is the same one-inference-in-
two-places shape F1 closed for the host.

**Cheap half, no decision needed:** `internal/packload/footprint.go`'s `skills` line reads
`merged (built-in < pack < user)` beside a single `into`. Saying that the merge is into *every*
selected destination would make the footprint true without touching delivery.

**Pinned so this is not re-audited:** `internal/cli/run/packskillsdelivery_test.go` asserts all
three probe results, including the notch disagreement, so answering OQ-S4 either way moves a test
deliberately rather than rediscovering the behavior.

## 🟡 E4 — the detail: one mode shipped, one is vacuous, one is a different problem

**Shipped 2026-08-12** (`internal/entrypoint/tomltrivia.go`). The row said "small, decisions
made"; the decisions were in [`host-file-staging.md`](host-file-staging.md), which ranked five
options and put the *trivia* one — the only one that keeps a comment beside the key it explains —
third, as real work wanting a decision. **It was third for `stateful`. For `rmw` it is nearly
free, and the mode is the reason.**

Three of the four costs priced there are costs of CAPTURED STATE: widening the overlay envelope,
the sidecar migration, and finding somewhere for the staleness rule to live. An `rmw` surface has
no captured state, and its source and destination are the same file, read and rewritten in one
operation — so the option collapses to "scan the comments in, put them back out". No sidecar, no
`TriviaCodec` on the engine interface, and nothing touching the shared emitter that `stateful`
and `TestRenderFingerprintStable` ride (the fingerprint is byte-identical, checked).

| mode | ruling | why |
|---|---|---|
| `rmw` | **preserve — done** | its contract already says "preserve everything yolo does not declare"; comments are part of everything, so the mode was breaking its own promise |
| `computed` | **do not preserve, and that is correct** | yolo is the sole author. There is no user comment in the file to keep; a comment in that output would be one yolo *wrote*, which is a different feature |
| `stateful` | **open — OQ-E4** | the file is COMPOSED, so a comment can only come from the `host` layer and preserving it is a PROJECTION between two files, not an in-place edit |

**What `rmw` now does.** A comment survives iff the render did not change the value under it —
sub-question ①'s ruling ("better a missing explanation than a lying one"), translated to the mode
where the file *is* the layer the comment came from. Every drop is **reported by key** in
`apply --host`, in observe as well as assert; the doc had conceded the rule "silently drops the
user's comment", and it no longer does.

**The `json` half of the row is vacuous, and now provably so.** Strict JSON has no comment
syntax, so a commented `json` surface never decodes and the RMW path REFUSES it, byte-untouched.
That was an observation about today's `settings.json`; it is now pinned by a test, so "E4 on a
JSON surface" cannot quietly become a loss later.

**Still lost, deliberately, and named rather than hidden:** key ORDER (the emitter is canonical),
a comment block detached by a blank line anywhere but the file's top or bottom, a comment inside
a multi-line value, and anything under an `[[array of tables]]`.

---

# Everything else still open

| | # | Item | Kind | Blocked on |
|---|---|---|---|---|
| 🟢 | **T1** | **Unify the loophole transport on `loopback-tls`** ([loophole-transport.md](../design/loophole-transport.md) §7.4, decided 2026-08-13) — retire `unix-socket`. Port `host-processes` first (it is **D4**, broken on macOS today, and its failure is harmless), then the broker relay, then drop `unix-socket` from `validTransports`. Also needs the macOS-`guest` cross-uid grant on the token file and a correction to [loophole-protocol.md](../design/loophole-protocol.md) §Security posture | feature | nothing — but see **OQ-T8** for whether it lands with #32 or after |
| 🟢 | **B1** | Audit-only log of every jail↔host boundary crossing ([boundary-broker.md](../design/boundary-broker.md) step 1) | small, additive | nothing |
| 🟡 | **B1b** | **Credential-injecting proxy for git** — host injects after egress, jail holds nothing, no human. **Settled 2026-08-12: a BUILD, not an adoption.** unYOLO's `gh-broker` was read at source ([§10](../design/boundary-broker.md)) and the earlier "possibly an adoption" note is retired — it is Go not Python, but yolo **already ships this transport** (`claude-oauth-broker` *is* a credential-injecting TLS-interception proxy), and gh-broker wants a GitHub App, has bus factor 1 at 11 weeks old, and carries 73 modules against yolo's 3. Smaller build than the row implied. **Carries one decision — OQ-B1b** | new capability | **your call** (OQ-B1b) |
| 🟡 | **B2** | Approval-gated host credentials — one allowlisted verb, synchronous. Design validated by convergence with unYOLO. **Re-scoped 2026-08-12 from source** ([§10.6](../design/boundary-broker.md)): take the four-effect policy evaluation, code-owned `Grantable`, the operation registry, and two-bound **narrowing-only** grants; **defer** content-addressed plans, `expected_revision`, and decision tokens — each has a named trigger, and none has fired | new capability | N3/OQ-1 |
| 🟡🐛 | **D1** | **Config-approval snapshot is agent-writable** — `.yolo/config-snapshot.json` is mode `664` and writable in-jail (re-measured 2026-08-12). An agent that edits `yolo-jail.jsonc` **and** matches the snapshot makes the launch-time diff prompt vanish — the exact bypass [config-safety.md](../design/config-safety.md) exists to prevent, and it is undiscussed there. From `ROADMAP.md` §4d; never queued until now. **Has an open question — see OQ-D1** | security | **your call** (OQ-D1) |
| ✅ | **D2** | ~~**Two shipped docs contradict the code**~~ **DONE 2026-08-12** — `USER_GUIDE.md` and `bundled_loopholes/claude-oauth-broker/README.md` both claimed *"no background timer / no proactive refresh"* while `oauthbrokercmd.go:88` starts `RunBackgroundRefresher` by default. The code was right; both docs now describe the real behavior (tick 60 s, lead 300 s, 5 s fast retry ×12, `--no-background-refresh` to disable) and say why the loop is architectural: Claude Code has no proactive refresh of its own for Pro/Max tokens, so an idle jail or a suspended host would otherwise wake up logged out. The README's three dead `../../../docs/…` links were one level too high and are fixed in passing | — | — |
| 🟢🐛 | **D4** | **`host-processes` is silently broken on macOS + podman** — found 2026-08-12 while writing [loophole-transport.md](../design/loophole-transport.md) §2.1. Its manifest declares `"transport": "unix-socket"`, the *same* transport whose virtiofs failure is [#31](https://github.com/mschulkind-oss/yolo-jail/issues/31); `yolo-ps` fails identically. Unreported because a broken `yolo-ps` is quiet where a broken broker blocks startup. Means the loophole is Linux-only in practice while advertised as available. **Porting it is also the natural proof for the transport generalization** (§6 step 3) | bug + the generalization's test case | nothing |
| ✅ | **D3** | ~~**`flake.lock` bumps are unreviewable**~~ **DONE** — the weekly workflow now builds `.#imageClosureRoot` (new flake output: the nixpkgs half of the image's contents, 571 store paths / 3.1 GiB, our Go build excluded) against the old and the new lock and appends `nix store diff-closures` to the PR body. Rehearsed on the real 08-05 → 08-12 pair, which produces `chromium: 151.0.7922.71 → 151.0.7922.108`, `aardvark-dns: 2.0.0 → 2.1.0`, `7 of 570 store paths changed` — the path count is reported because a staging-next merge that rebuilds the world without moving a version is invisible to `diff-closures`. Runs *after* the PR exists and `continue-on-error`, so it can only add to a bump, never block one; a build failure degrades to a note in the body. **Unverified until the first Monday run: the `gh pr edit` half** (everything before it was run locally) | — | — |
| ✅ | **B4** | ~~Correct [agent-credentials.md](../design/agent-credentials.md) §3~~ **DONE** — it documented Bedrock keys arriving via the `env` block of host `settings.json`; that block is `{}` and the real path is `env_sources`. Corrected in place, with a note that the `env` block is nonetheless the right *target* design (§11.2) — it described the correct mechanism before anything used it | — | — |

---

# Every decision waiting on you

🟡 **Everything in this section is waiting on you.** The loophole-transport design was audited 2026-08-13 and **five of its seven questions resolved without a ruling** — they are recorded as settled with reasoning in [`loophole-transport.md`](../design/loophole-transport.md) §7.1, so nothing was quietly dropped. Only OQ-T1 and OQ-T8 survive. One index, because these are spread across four docs. ❓ marks the two where I have no recommendation; the rest carry one, so a bare "go with your read" clears them. **Nothing below is blocked on work — only
on an answer.** Where I have a recommendation it is stated; where I do not, it says so.

| # | Decision | Where | My read |
|---|---|---|---|
| **OQ-D1** | **How to fix the writable config snapshot** (D1) | below | make it host-owned and read-only in-jail |
| **OQ-S4** | **Should the jail narrow its skills fan-out to match the host?** (S4) | below | yes — run `ResolveDestinations` on the jail path too |
| **OQ-E4** | **Do `stateful` surfaces get comment preservation too?** (E4) | below | not yet — the cheap half already landed, and this half is a real engine change |
| **OQ-T1** | **Answer #32's author**: per-jail token + pinned cert as proposed, or full mTLS? *He asked when he opened the PR; still unanswered.* | [`loophole-transport.md`](../design/loophole-transport.md) §7.2 | **as proposed** — mTLS buys no identity gain and adds a second cert lifecycle plus a second CA, and #33 is a live lesson in what a CA costs |
| **OQ-T8** | **Does the transport generalization ship WITH #32 or AFTER it?** The live disagreement — you suggested replacing #32 with the open work | same §7.3 | **merge first** — #32 fixes a total outage and the churn is bounded; `host-processes` (D4) already is the second consumer, so generalizing can start immediately after |
| **N3** | Non-container nix: Option 0 / 2 / 3 | B-2 above | Option 2, no longer urgent |
| **OQ-1** | Is per-jail auth selection enough, or is dynamic switching required? | [`agent-auth-modes.md`](../design/agent-auth-modes.md) §10 | per-jail is probably enough |
| **OQ-2** | Bedrock bundle: stays in `env_sources`, or becomes a declared bundle? | same | declared |
| **OQ-3** | What happens on a mode switch mid-session? | same | require a restart; be honest about it |
| **OQ-4** | Should `check` verify the selected mode's credential is live? | same | yes |
| **OQ-5** | Should packs be able to require other packs (`requires_pack`)? | same §12.4 | yes, paired with `conflicts` |
| **OQ-6** | Auth packs **shipped or fetched**? *Gates building them.* | same | **fetched** |
| **OQ-7** ❓ | Does the Teams pack own the model IDs, or the base `claude` pack? | same | ❓ **no recommendation — genuinely your call** |
| **OQ-8** | Generalize #32's transport, or merge as scoped? | same | merge as scoped, generalize at B1 |
| **OQ-9** ❓ | Is `env_sources` still the right home for the AWS keys? | same | ❓ **no recommendation — genuinely your call** |
| **A2** | How loud should selecting both auth packs be? | Thread A | a `conflicts` manifest field |
| **S5** | Jail skill collision: warn, `check` failure, or boot refusal? | S5 above | warn now; decide the rest later |
| **OQ-B/E** | Approval grants: reusable? answered where? | [`boundary-broker.md`](../design/boundary-broker.md) §9 | §10 has worked answers |
| **OQ-B1b** | **Vendor unYOLO's policy engine, or re-derive the model?** *Adopt-vs-build is already settled (build); this is the one piece where copying may beat writing.* (B1b) | [`boundary-broker.md`](../design/boundary-broker.md) §10.6 | **vendor it** — MIT, stdlib-only, ~2,100 lines, zero new module deps, and safer than a module dep given upstream's no-compatibility policy |

## 🟡 OQ-D1 — how to fix the writable config snapshot

`.yolo/config-snapshot.json` lives in the **workspace**, which is bind-mounted read-write by
design — that is the whole point of the workspace. So "make it read-only" is not a one-liner, and
the options differ in what they cost:

1. **Move it out of the workspace** into host-side state (`paths.GlobalStorage()`), keyed by
   workspace path. The agent cannot reach it at all. **Cost:** the snapshot stops being visible
   next to the config it describes, and a workspace copied to another machine loses its approval
   state — which may be correct.
2. **Keep it in place, make it host-owned and read-only in-jail** (`0444`, owned by a uid the jail
   does not run as). **Cost:** the jail runs as UID 0, so file modes are not a barrier — this needs
   a mount-level `:ro`, which means a per-file mount inside a read-write tree.
3. **Sign it** — the snapshot carries an HMAC the host verifies, so tampering is detected rather
   than prevented. **Cost:** a key to manage, for a threat the other two options simply remove.
4. **Accept and document.** The threat is a *confused or prompt-injected* agent, not a malicious
   one, and an agent that wants to bypass the prompt can ask the user to approve instead.
   **Cost:** [`config-safety.md`](../design/config-safety.md) currently promises a guarantee it
   does not deliver, so at minimum the doc must change.

**My read: (2) if a per-file `:ro` mount inside the workspace is practical, else (1).** Both
remove the bypass rather than detecting it, and (1) is the honest fallback. **(4) is the one to
avoid quietly** — it is the current state, and it is only defensible if written down.

**This needs your call because it trades workspace-portability against a security property**, and
that is a product question rather than a technical one.

## 🟡 OQ-S4 — should the jail narrow its skills fan-out to match the host?

Measured in S4 above: in a jail **every** loaded pack's skills reach **every** declared
destination; at `apply --host` a pack's skills reach only the destinations that pack declared.
Two notches, two answers to "where does this pack's content go".

1. **Leave the jail as it is; make the reporting honest.** Say the fan-out out loud in
   pack-system.md's `skills` section and in `yolo pack footprint`. **Cost:** a manifest keeps
   understating delivery, so a pack scoped by its author to one directory still reaches every
   selected agent — the reviewable artifact and the behavior stay out of step.
2. **Run `packload.ResolveDestinations` on the jail path too.** A pack that DECLARES a destination
   delivers only there; a pack that declares nothing borrows every destination in the set. The
   zero-ceremony promise is preserved *by* the borrowing — that is what the function exists for.
   **Cost:** a behavior change on a shipped kind. No pack yolo ships is affected (none carries
   skills — checked), so it lands only on a third-party or local pack that both declares an `into`
   **and** ships skills; that pack narrows to what it declared.
3. **Widen the host to the jail's rule.** Symmetry in the other direction. **Rejected already**, by
   `ResolveDestinations`' own comment: a manifest would start writing into home directories its
   author never named.

**My read: (2).** It makes both notches answer from one inference instead of two, makes `into` mean
what it says, and makes `yolo pack footprint` true — the same argument F1 used to give the host the
jail's inference, applied in the other direction. **(1) is the honest fallback** and is worth doing
regardless of the answer, since even under (2) the footprint should say what a borrowed destination
is.

**The trade to weigh before agreeing.** Under (2) a content pack that declares a unique path (say
`into: ".acme/skills"`) delivers to `.acme/skills` and nowhere an agent reads — it becomes inert
where today it reaches everything. That is arguably correct (it is what the pack declared) and
arguably a regression, and pack-system.md's own advice pushed authors toward declaring rather than
staying silent. **Whether a declaration should NARROW delivery or only ADD to it is the actual
question**, and it is a product call about what `into` promises.

## 🟡 OQ-E4 — do `stateful` surfaces get comment preservation too?

`rmw` has it (E4 above). `computed` provably does not need it. `stateful` is the remaining mode,
and it is a **different problem wearing the same words**: the file is composed from layers, so a
comment can only come from the `host` layer, and putting it in the render is a PROJECTION out of
one file into another rather than an in-place edit. That is the case
[`host-file-staging.md`](host-file-staging.md) priced, and its price is mostly still there.

1. **Do it.** `Codec` grows an optional `TriviaCodec`; trivia rides the compose result; rule ① is
   keyed on `Result.Provenance` (emit a comment only where the winning layer is `host`), which the
   doc notes is already computed and one map lookup per key. **Cost:** it lands on the A12-fatal
   boot path, and it needs an answer for the Lua transform boundary — a hook returns a table, and
   trivia has to either survive that or be documented as dropped by any transform.
2. **Extend the yolo-authored header that is ALREADY THERE** to point at the `:ro` original — the
   doc's option 1, and half of it shipped without being recorded here. Measured in this jail
   2026-08-12: the composed `~/.codex/config.toml` opens with *"Generated by yolo-jail — composed
   at jail start; hand edits may be reverted or lost. Run `yolo config ls`…"*. So the header
   exists; what it does not carry is the `/ctx/host-user/<slug>` path to the untouched source.
   **Cost:** near nothing, and strict JSON still has nowhere to put a header line, so it serves
   `toml` only.
3. **Leave it, and say so.** `raw` already round-trips a hand-written file byte-exact, and
   `config-ref` already documents the structured-codec trade. **Cost:** none new; it is the status
   quo, now with one mode fewer in it.

**My read: (3) for now, then (1) when something needs it.** The reader this was for — an agent
reading config to learn *why* a value is what it is — is now served on the file the argument was
actually about (`~/.codex/config.toml` at the host notch, where a real user's real comments were
being destroyed on every apply). (2) is a one-line follow-up worth folding into whatever touches
that header next, not a reason to open this.

**What would change my read:** a `stateful` surface whose HOST source is a commented `.toml` that
a user actually maintains. Today there is none — the only shipped TOML surface is `codex/config`,
and the only shipped commented-config population is user-declared `host_files` entries, which get
`raw` by auto-detect and keep their bytes exactly.

---

# Recently shipped

Full reasoning and the defects that only surfaced by running things:
[`shipped-2026-08-pack-batch.md`](shipped-2026-08-pack-batch.md).

**2026-08-05:** S1 (skill collisions fatal, `3e0be7b`), S2 (`skills_tier`, `663cb29`/`0557c9e`/`ceb93b3`),
S3 (layer 4 deleted, `315c150`), C1 (`from` literals, `d342827`), C2 (`f2d0692`), C3 (`db695d8`),
N1 (gcroot, `23cee7a`), N2 (`yoloNoncontainerPackages`, `11f8bb7`), V1 (`8e7717f`).
**2026-08-12:** the auth-modes/broker doc split (`78eb3b5`), the unYOLO analysis (`1b4f7f9`).

## Two traps worth carrying forward

- **A retired manifest field is a VERSION-SKEW fact, not a structural one.** Refusing the retired
  per-contribution `tier` inside `Validate` reproduced the original `tier` incident in mirror image:
  `DecodeTolerant` runs `Validate`, so an OLDER baked entrypoint reading newly-staged manifests
  refused them and the jail would not start — no recovery route, since the offending manifest is one
  yolo ships. Found only by running a nested jail against the previous baked image. The refusal
  belongs in `Decode` alone.
- **The coarse fingerprint test cannot answer "did the bytes move?"** `TestRenderFingerprintStable`
  pins the file SET. A byte comparison needs a FIXED workspace path, or claude's
  `projects["${workspace}"]` differs between runs and hides the answer in noise.

## The local pack IS layer 4 — kept because the rationale is load-bearing

yolo now owns `~/.claude/skills` and `~/.claude/CLAUDE.md` wholesale, so **a user contribution has
nowhere else to live.** "Commit it to a repo pack" is not an answer for a half-baked skill, a
machine-specific one, or scratch space you do not want in git. The jail already had this slot —
layer 4, "the user's OWN skills tree, written last so a same-named local skill wins". The local pack
is that slot given a home yolo does not overwrite, which is why S3 was a defect rather than a design
choice. As an ordinary pack entry appended last by `config.LoadPacks` it already holds layer 4's
precedence, so the fix was to DELETE the fourth layer, not repoint it.
