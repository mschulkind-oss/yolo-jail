---
title: "Emptying bundled_loopholes/ — the broker, the identity rule, and the proving ground"
date: 2026-08-15
status: in-review
stage: DESIGN
next: "Rule OQ-BP7, what main pins between releases; it gates §14.4 step 7 only, and no shipped manifest declares a binary yet"
tags: [loopholes, packs, broker, claude, identity, binaries]
summary: "How the Claude OAuth broker became a contribution of packs/claude and the bundled loophole channel was deleted (built 2026-08-19), the connection-preamble identity rule that replaced the per-jail relay, and the pack-shipped binary capability that was committed to the same sprint: its mechanism, a digest-pinned download cached with its exec bit, was built on 2026-09-30, and its release matrix, decided the same day, is built but for one step: the four platforms yolo ships to, the tag's own GitHub release, and a reproducible build whose digest a pin tool commits before the tag and every release gate checks. One question is open, what main pins between releases."
---

# Emptying `bundled_loopholes/` — the broker, the identity rule, and the proving ground

**Status:** 2026-09-30: one ruling owed, [OQ-BP7](#OQ-BP7). **The broker move is built** (2026-08-19; measured that day, and [§13](#13-what-empty-the-channel-actually-required--measured-2026-08-19) records what emptying the channel actually required), and **the pack-shipped-binary capability is built as a mechanism and not as a release** (2026-09-30, [BP-D1](#BP-D1) to [BP-D6](#BP-D6); [§3.1](#31-what-is-actually-unresolved-here)'s *Built* note says what): a loophole manifest declares a program's builds under `binaries`, `yolo pack install` fetches and verifies them, and the launch runs them from the cache. The **release matrix** [OQ-BP1](#decision-ledger) put on the critical path is decided ([§14](#14-the-release-matrix), [BP-D7](#BP-D7) to [BP-D9](#BP-D9)) and built but for its last step (2026-09-30, [BP-D10](#BP-D10) to [BP-D14](#BP-D14); [§14.4](#144-the-plan-in-order)'s *Built* note says what): which platforms yolo's own release builds for an official pack's binary, where it publishes them, and how each digest reaches the manifest embedded in the same release. No shipped manifest declares a binary yet (re-checked 2026-09-30). The first one is held to the matrix by a short-suite census, pinned by `just pin-pack-binaries`, and rebuilt and compared by `just release`, by the release before it uploads, and by PyPI's gate. What main pins between two releases, the last step, waits on [OQ-BP7](#OQ-BP7). ⚠ **[OQ-BP5](#OQ-BP5) and [OQ-BP6](#OQ-BP6) were written against a trust model that has since changed**: the fetched-pack approval prompt and every origin refusal were deleted on 2026-09-04 ([`OQ-TP9`](trust-paths.md#decision-ledger)), so [§3.1](#31-what-is-actually-unresolved-here)'s "two gates" and the `InstallerURL` precedent both questions lean on no longer exist in the tree — see the note under each. Both were settled on 2026-09-30.

**Needs your ruling:** [OQ-BP7](#OQ-BP7), what main's tree pins for an official binary between two releases. It blocks one step of [§14.4](#144-the-plan-in-order)'s plan, the last.

All five sequencing steps are in the tree and `bundled_loopholes/` is DELETED — the directory, its `embed.go`, `internal/loopholes/embedfallback.go`, and the `BundledLoopholesDir` / `SourceBundled` / `IncludeBundled` / `loadFromDir` vocabulary that read it. Step 5 landed as one commit: the manifest is now `packs/claude/loopholes/claude-oauth-broker/`, declared by `packs/claude/pack.json` as `{"kind": "loophole", "from": "loopholes/claude-oauth-broker"}` ([OQ-A10](../reference/loophole-system.md#why-its-this-way) — a contribution of the agent pack, not a pack of its own); `loopholes.ReservedLoopholeNames` is **deleted whole**, because the broker was the last name in it; `requires.command_on_path: "claude"` is deleted from the manifest (R3, free under R6); and `run.brokerLoopholeActive` gained the ORIGIN GATE it was allowed to skip only while the record was bundled. Three consequences worth carrying forward are in [§13](#13-what-empty-the-channel-actually-required--measured-2026-08-19). Code claims verified against the tree on 2026-08-15 unless dated otherwise.

**Four review rounds, same day.** Round 0: [§3.1](#31-what-is-actually-unresolved-here) grew from a deferral into a design — pack-shipped binaries are wanted as a general capability. Round 1 **retracted this doc's central claim** ([the retraction](#52a--retracted-the-front-never-parses-deliberately)): the front is *not* a component that never parses. Rounds 2 and 3 settled identity ([§5.5](#55-ruled--every-connection-is-raw-yolo-prepends-a-connection-preamble)) by rejecting, in turn, my payload stamp, my mandatory version of it, and my `framed`/`raw` compromise — arriving somewhere none of the three reached: **yolo never parses a daemon's payload at all.** The title has changed twice and the scope grew from one loophole to the whole channel.

**Three rulings, each of which overrules a leaning of mine:**

> 1. **The broker move and the pack-shipped binary capability ship TOGETHER**, not one then the other ([OQ-BP1](#decision-ledger)).
> 2. **`bundled_loopholes/` has no inhabitants at the end of this work sprint** ([OQ-BP4](#decision-ledger)) — the goal is the channel's retirement, not one fewer entry in it.
> 3. **Every connection stays raw and yolo prepends its own connection preamble** — default on, `preamble: false` for a dumb pipe ([OQ-BP2](#decision-ledger), [§5.5](#55-ruled--every-connection-is-raw-yolo-prepends-a-connection-preamble)). yolo never parses a daemon's payload. Breaking changes are in scope.

**One open question is left, and it gates one plan step.** [OQ-BP5](#OQ-BP5) and [OQ-BP6](#OQ-BP6) belonged to the binary capability, blocked nothing in [§12](#12-host-processes-as-the-proving-ground), and were settled on 2026-09-30. [OQ-BP7](#OQ-BP7) belongs to the release matrix and blocks only step 7 of [§14.4](#144-the-plan-in-order); steps 1 to 6 are the same under every answer.

**Scope note.** That second ruling makes this doc one of three conversions rather than a self-contained change, and the other two are not designed here: `host-processes` needs the same `publishes` change with none of the relay complexity, and `audio` cannot become a pack at all until **[OQ-LP14](../reference/loophole-system.md#oq-lp14)** is answered. [§11](#11-what-no-bundled-loopholes-additionally-requires) states what each needs and what is genuinely blocking; the work belongs to the sprint, not to this document.

**The short version.** [`pack-code-separation.md`](../reference/pack-system.md#what-a-pack-is-on-disk) [§4](../reference/pack-system.md#what-a-pack-is-on-disk) named two things that must be true before the `claude-oauth-broker` can ship as a pack: a **jail-side daemon shippable as a binary**, and the **per-jail relay** becoming expressible. The first is essentially already built — the container mount, the manifest token, the loader and the pack-shipped subset all already permit it, and nobody noticed because nothing has tried. The second reduces to three of four jobs the framework-owned front *already does*, plus one it does not: the relay decodes each request to stamp a host-asserted `jail_id` into it. **That job is now deleted rather than moved.** yolo prepends its own **connection preamble** to every authenticated connection and never inspects a payload byte again — which removes the relay's reason to exist, removes the framework's only obligation to re-serialize someone else's JSON byte-identically, and gives every daemon, in any language, one uniform thing to read. `internal/brokerrelay` goes; nothing replaces it.

**The most important section is [§5.5](#55-ruled--every-connection-is-raw-yolo-prepends-a-connection-preamble)** — everything else is inventory and consequence.

**Reads with:** [`trust-paths.md`](trust-paths.md#decision-ledger) ([`OQ-TP9`](trust-paths.md#decision-ledger), which deleted the approval and origin gates [§3.1](#31-what-is-actually-unresolved-here) designs around), [`pack-code-separation.md`](../reference/pack-system.md#what-a-pack-is-on-disk) (why this is being done at all; its [§4](../reference/pack-system.md#what-a-pack-is-on-disk) is what this doc corrects), [`loophole-system.md`](../reference/loophole-system.md) (the implementation authority: the `loophole` kind, the front, and [the pack-shipped subset](../reference/loophole-system.md#the-pack-shipped-subset) is the contract this leans on, with [OQ-LP11](../reference/loophole-system.md#why-its-this-way)/[OQ-LP14](../reference/loophole-system.md#why-its-this-way)), [`agent-credentials.md`](../reference/agent-credentials.md) ([§2.5](../reference/agent-credentials.md#the-claude-oauth-broker), why a broker exists at all), [`loophole-transport.md`](../reference/loophole-transport.md) (why loopback-TLS is the only hop).

---

## 1. Verdict up front

**Ship it as a pack, and expect the work to be smaller than [§4](#4-blocker-2--the-relay-and-how-little-of-it-is-irreducible) estimated — but concentrated somewhere [§4](#4-blocker-2--the-relay-and-how-little-of-it-is-irreducible) did not look.**

Three claims, each argued below:

- **P1. Jail-daemon-as-binary is not a missing mechanism.** `{jail_loophole_dir}` already resolves to a container path, the module dir is already bind-mounted there `:ro` **without `noexec`**, `nix-ld` already runs non-nix dynamically-linked binaries, and the pack-shipped subset does not restrict `jail_daemon` at all. What is missing is *selection, delivery and trust* for the binary — designed in [§3.1](#31-what-is-actually-unresolved-here) on review request, and **not on the broker's critical path**, because an official pack may keep a baked daemon. ([§3](#3-blocker-1-re-examined--the-jail-side-binary-is-already-expressible))
- **P2. The relay is 3/4 redundant with the front.** Per-connection upstream dial, TLS termination, endpoint publication, and layer-attributable failure are all in `svcendpoint` already. ([§4](#4-blocker-2--the-relay-and-how-little-of-it-is-irreducible))
- **P3. The relay's last job is replaced by something smaller than itself.** Ruled in [§5.5](#55-ruled--every-connection-is-raw-yolo-prepends-a-connection-preamble): yolo never parses a daemon's payload, and instead prepends one **connection preamble** to every authenticated connection — so `readFirstMessage`, `stampJailID` and their fallbacks are deleted rather than relocated, and the framework stops re-serializing someone else's JSON to keep a frozen wire contract. ([§5](#5-jail-identity--the-actual-design-decision))

**What I would not do:** treat this as a prerequisite for the rest of [`pack-code-separation.md`](../reference/pack-system.md#what-a-pack-is-on-disk). Its steps 1–3 are independent and should land first regardless of how this goes.

---

## 2. What the broker was before the move

> [!NOTE]
> **This section is the pre-move inventory, verified 2026-08-15, and it is kept as the argument's
> starting point rather than as a description of the tree.** The per-jail relay
> (`internal/brokerrelay`, `yolo internal daemon broker-relay`) was deleted on 2026-08-19
> ([§7](#7-what-this-deletes-what-it-costs-what-it-forecloses)); the manifest moved from
> `bundled_loopholes/` to `packs/claude/loopholes/claude-oauth-broker/`; and `yolo broker` is now
> an alias for `yolo host-daemon <verb> claude-oauth-broker`, the management verb over every
> host-wide daemon (2026-09-20). The line citations below are gone with the files they pointed
> into, so they are dropped rather than repointed.

Five hops, four processes, two of which are baked subcommands rather than binaries:

```
  IN THE JAIL                      │        ON THE HOST
                                   │
  claude ──(1)──► 127.0.0.1:443    │
                  yolo-jaild       │
                  oauth-terminator │
                       │           │
                       └──(2) loopback-TLS, per-jail bearer token ──►│
                                   │        yolo internal daemon broker-relay
                                   │        ├─ front: TLS, token check, publish
                                   │        │   endpoint file (0600, per jail)
                                   │        ├─ splice ──► its own host-only
                                   │        │   AF_UNIX socket in /tmp
                                   │        └─ stamp jail_id into frame #1 ──(3)──┐
                                   │                                              │
                                   │        yolo internal daemon claude-oauth-broker
                                   │        ├─ flock (serializes refreshes) ◄──(4)─┘
                                   │        ├─ upstream refresh + background tick
                                   │        └─ writes the SHARED creds file ──(5)──►
                                   │            ~/.claude-shared-credentials/
```

| Piece | Package | Runs as | Per |
|---|---|---|---|
| in-jail TLS terminator | `internal/oauthterminator` | `yolo-jaild oauth-terminator` | jail |
| per-jail relay (front + stamp + dial) | `internal/brokerrelay` | `yolo internal daemon broker-relay` | jail |
| host singleton | `internal/oauthbroker` | `yolo internal daemon claude-oauth-broker` | host |
| lifecycle + `yolo broker` CLI (now `yolo host-daemon`) | `internal/broker` | in-process | host |

**What the manifest already declares** (`bundled_loopholes/claude-oauth-broker/manifest.jsonc`, verified 2026-08-15): `serves`, `transport: loopback-tls`, `intercepts`, `broker_ip`, `ca_cert: {state}/ca.crt`, `state_files`, `requires.command_on_path`, `host_daemon.cmd`, `jail_daemon.cmd`, `doctor_cmd`. That is nearly the whole surface. **What it does not declare is the relay** — `relayEnsure` was called from the run pipeline (`internal/cli/run/loopholesruntime.go`), keyed per jail by a hash of the container name, with its own pid file, lock and socket. Nothing in any manifest describes it.

**P1 of the existing design still holds and is not reopened:** the broker exists because Anthropic mints single-use refresh tokens, so concurrent consumers must be serialized by a host-wide flock ([`agent-credentials.md`](../reference/agent-credentials.md) [§2.5](../reference/agent-credentials.md#the-claude-oauth-broker)).

---

## 3. Blocker 1 re-examined — the jail-side binary is already expressible

[`pack-code-separation.md`](../reference/pack-system.md#what-a-pack-is-on-disk) [§4](../reference/pack-system.md#what-a-pack-is-on-disk) says: *"A pack-shipped binary would have to be mounted `:ro` from the loophole module dir and be executable with a runtime the image bakes — which is the 'native binary' question [`loophole-packaging-overview.md`](../reference/loophole-system.md) [§3.3](../reference/loophole-system.md) names and does not design."*

Every clause of that is already satisfied. Evidence, verified 2026-08-15:

| What [§4](#4-blocker-2--the-relay-and-how-little-of-it-is-irreducible) says is needed | State in the tree |
|---|---|
| the module dir reachable from inside the container | **Built.** `-v <module>:/etc/yolo-jail/loopholes/<name>:ro` (`internal/loopholes/runtime.go`; the mount point is `loopholedecl.JailLoopholeDir`) |
| a way to *name* that path in the manifest | **Built.** `{jail_loophole_dir}` is legal in `jail_daemon.cmd` and refused in host fields, with the mount point as one constant (`loopholedecl.TokenJailLoopholeDir`, `refuseJailTokenInHostField`, in `internal/loopholedecl/tokens.go`) |
| the mount to permit execution | **Satisfied by omission.** The `-v` is `:ro` with **no `noexec`** |
| a runtime for a non-nix binary | **Shipped 2026-07-22.** `nix-ld` is the FHS interpreter; that is precisely its job ([`../reference/mise-node-dynamic-linking.md`](../reference/mise-node-dynamic-linking.md), the as-built reference) |
| the pack-shipped subset to permit a `jail_daemon` | **Never restricted it.** The subset constrains jail *env*, bind mounts, `ca_cert`, `requires` and `publishes` (`internal/loopholedecl/packshipped.go`) — there is no `jail_daemon` rule |
| a way to say "Linux/amd64 only" | **Landed 2026-08-14** as `platforms`, closed-list `<goos>[/<goarch>]` |
| the daemon to be spawned generically | **Built.** `jail_daemon` payloads become `YOLO_JAIL_DAEMONS` for `yolo-jaild supervise` with no per-loophole code (`loopholes.JailDaemonPayload`, `internal/loopholes/runtime.go`) |

So the honest statement is: **a pack can already ship a jail-side daemon binary; nothing has ever tried.** That is an unexercised path, not a missing one — the same shape of finding as agy's never-run credential hook in [`pack-code-separation.md`](../reference/pack-system.md#what-a-pack-is-on-disk) [§3.2](../reference/pack-system.md#what-a-pack-is-on-disk).

### 3.1 What is actually unresolved here

Not mechanism — **distribution**. *(Section rewritten after review: the maintainer wants pack-shipped binaries supported as a general capability, with per-arch selection and ideally dynamic download, so this is now a design rather than a deferral.)*

Three sub-problems that are easy to run together and should not be: **selection** (which file runs on this machine), **delivery** (how the file got there), and **trust** (what approving it means). Only the third is hard.

#### Selection — a convention, not a mechanism

`platforms` already declares *support*; it does not say which file to execute. The convention should be the one the repo already uses for its own cross-builds — `dist-go/<goos>-<goarch>/` from `just build-go`, and `bin/linux-<arch>` for a shipped bundle's prebuilt short-circuit:

```
loophole/broker/bin/<goos>-<goarch>/terminator      # e.g. bin/linux-amd64/terminator
```

`{jail_loophole_dir}` then resolves as it does today and the runtime substitutes the pair, so a manifest writes one path and gets the right file. A missing build for this machine is an **honest inert report** through the mechanism `platforms` already established (`loopholes.InertNote`), not an exec failure at daemon start. This part is a naming rule plus a lookup; it carries no trust weight.

*As built (2026-09-30, [BP-D3](#BP-D3)):* the file is a download rather than a path in the tree, so the pair selects a **build** instead of a directory: each token takes the build for where it runs, this machine's platform for a host reference and `linux/<arch>` for a jail one. A missing build is reported exactly as this paragraph asks, on the platform axis.

#### Delivery — three options, and the digest is what separates them

| | Option | Verdict |
|---|---|---|
| **A** | **Checked into the pack tree.** `bin/<goos>-<goarch>/…` committed alongside the manifest | ⚠️ **Half true — corrected 2026-09-17 by running it** ([§10](#10-sequencing) step two, `packs/hello-daemon`). It works for a pack **configured by path**: `packstage.copyFile` carries `0o111`. It cannot work for an **EMBEDDED** pack — the shape an official pack has, and the one the interim answer below leans on — because `embed.FS` cannot represent an exec bit at all: an embedded file reads back `0444` whatever its mode in the tree (measured), and yolo's own on-disk copies add none — `packload.copyEmbeddedTree` writes `0o644`, and the leased per-build embedded tree (2026-09-24) is made read-only `0o444`. The pinning half is also narrower than stated: `packsrc.LockEntry.Commit` pins a FETCHED pack, and an embedded pack has no lockfile. Cost is repo weight: every platform in every clone, forever |
| **B** | **Declared download.** the manifest names a URL per platform — a GitHub release asset — plus a **mandatory `sha256`**; yolo fetches at `pack install`, verifies, and caches by digest | ✅ **The right general mechanism**, and the one the comment asks for — with the digest as a hard requirement, for the reason below |
| **C** | **Declared build step.** the pack names a command that produces the binary at install time | ⚠️ **Not now, and possibly never on the host.** See below — it is a different risk class *and* it destroys the property B exists to preserve |

**Why the digest is not optional, stated as a requirement:**

> **P4. A pinned pack must pin everything that runs.** Today the lockfile's commit SHA pins the pack's whole tree — that is what "pinned" means here. A URL is not in the tree. A GitHub release asset can be deleted and re-uploaded, and a tag can be moved, both without changing any commit. So a manifest that names a URL *without* a digest silently converts "pinned pack" into "pinned manifest, unpinned payload", and the thing left unpinned is the executable. A `sha256` written **in the manifest** restores the property transitively: the commit pins the manifest, the manifest pins the bytes.

That also answers *when* the fetch happens: at **`pack install`**, never at launch. A launch-time fetch would mean no network is no jail, and would move the moment-of-trust from "when you approved this pack" to "every time you start one". Cache the verified artifact under yolo's state tree keyed by digest — not in the module dir, which is the git tree and is mounted `:ro`.

> [!NOTE]
> **Built 2026-09-30, as option B** ([BP-D1](#BP-D1) to [BP-D6](#BP-D6)). A loophole manifest
> declares each program under `binaries`, one build per `<goos>/<goarch>` with an https `url`
> and a mandatory `sha256` (`internal/loopholedecl`'s `binaries.go`). `{binary:<name>}` in
> `host_daemon.cmd` or `doctor_cmd` resolves to the cached build for this machine;
> `{jail_binary:<name>}` in `jail_daemon.cmd` resolves to
> `/etc/yolo-jail/loophole-binaries/<loophole>/<name>`, where the launch mounts the Linux build
> read-only from the cache (`internal/loopholes`' `binaries.go` and `runtime.go`). The cache is
> `~/.local/share/yolo-jail/pack-binaries/<sha256>/<name>`, mode `0555`, and only
> `yolo pack install` fills it (`internal/packbin`, `internal/cli/packbinaries.go`). The mounted
> file carries the exec bit an embedded pack's own tree cannot, which is the point. A build that
> is not fetched keeps the loophole off, and the launch says to run `yolo pack install`. A real
> podman jail running the mounted build is pinned by `TestAJailRunsAPackBinaryItDownloaded`
> (`integration/packbinary_test.go`); a real ELF under nix-ld, and every backend but podman on
> Linux, are not. The release matrix that builds an official pack's binary is
> [§14](#14-the-release-matrix)'s, built but for its last step, and no shipped pack declares a
> binary yet.

**Why the build step is a different question.** A build is arbitrary code execution, so it lands in the sharpest existing category rather than a new one, and the precedent is already in the schema: `packdecl.Install.InstallerURL` is *"a curl-piped installer … the sharpest thing a manifest can name: a URL whose contents run as a shell script"*, honored — when this was written — only under the origin rule: **a fetched pack could not introduce one**. ⚠ **That rule is gone**: [`OQ-TP9`](trust-paths.md#decision-ledger) deleted every origin refusal on 2026-09-04, and `packload.Pack.HonoredInstalls` now refuses nothing. The `InstallerURL` field's own doc comment in `packdecl.go` says the same: it is *"NOT gated on the pack's origin"*, and disclosure is what is left (checked 2026-09-25; this sentence used to say the comment still said otherwise). A build step is that, plus the loss of P4: builds are not bit-reproducible in general, so there is no digest to pin and no way to say what will run. My read is that B covers the real need and C should wait for a case B cannot serve. It is [OQ-BP5](#OQ-BP5) because the comment explicitly asks for it and because "both" is a coherent answer.

#### Trust — the split that matters, and it is not jail-vs-fetched

The comment's framing treats "shipping a binary" as one thing. It is two, and they sit on opposite sides of the boundary this whole design exists to defend:

| | Runs where | Risk if hostile | Existing category |
|---|---|---|---|
| **`jail_daemon` binary** | inside the sandbox | bounded by the jail — the same blast radius as any `npm install` the agent already does | comparable to `InstallerURL`, which is already origin-gated |
| **`host_daemon` binary** | on the real machine, as a daemon | unbounded — this is *the* thing the four gates exist for | must go through host-execution approval: enumerated claim, y/N at install, recorded in the lockfile |

So the mechanism should be one schema and **two gates**: shipping a jail-side binary is roughly as sharp as what a pack can already do, while shipping a host-side one is a host-execution grant and must be disclosed as such. That asymmetry is worth building in from the start, because a single "packs may ship binaries" switch would quietly grant the second while the reader is thinking about the first.

> [!WARNING]
> **Neither gate this table names exists any more.** On 2026-09-04 [`OQ-TP9`](trust-paths.md#decision-ledger)
> deleted the fetched-pack approval prompt — the enumerated claim, the y/N at install and the
> lockfile record — and with it every origin refusal, `InstallerURL`'s included: selecting a pack
> already requires writing user-scope config as the host user, which is more authority than the
> gate withheld. What a user gets instead is disclosure: `yolo pack footprint` before selecting a
> pack, and the launch's host-execution lines printed before any daemon spawns
> ([`loophole-system.md`](../reference/loophole-system.md#trust-what-is-gated-and-what-is-not)). So
> today a fetched pack's `host_daemon.cmd` runs on the host with no approval, disclosed. The
> asymmetry above is still the right thing to disclose; it is enforced by nothing, and
> [OQ-BP6](#OQ-BP6)'s answer (2026-09-30) is that a fetched pack's host-side binary is honored
> and disclosed like its `host_daemon.cmd`, with no new refusal source.

#### And the interim answer for *this* broker

None of the above blocks the broker move. `jail_daemon.cmd` may keep naming a baked subcommand for an **official** pack, because [`loophole-packaging-overview.md`](../reference/loophole-system.md) [§1.1](../reference/loophole-system.md) already rules that a baked client is fine for one. So the broker can become a pack **now**, on baked daemons, and adopt the binary mechanism when it exists — which is the sequencing [OQ-BP1](#decision-ledger) asks about, restated as "does the broker wait for the general capability?" rather than "does the capability exist?"

---

## 4. Blocker 2 — the relay, and how little of it is irreducible

The relay did four things (`internal/brokerrelay`, deleted 2026-08-19; its package comment and `handle`):

1. **Terminate loopback-TLS and publish the endpoint file** the jail reads (0600, per jail, carrying that jail's bearer token).
2. **Dial the broker's socket per connection**, so a restarted broker (new socket inode) is picked up on the next request rather than 502-ing the jail.
3. **Stamp `jail_id`** into the single framed JSON request, overriding any client-supplied value.
4. **Attribute failures to a layer** — endpoint missing / dial refused = relay layer; accepted-then-zero-frames = broker layer — including a bounded drain so a dial failure surfaces as clean EOF rather than `ECONNRESET`.

Three of those four are already the framework's, for every `publishes: "socket"` daemon:

| Relay job | Front equivalent | Evidence |
|---|---|---|
| TLS + token + publish endpoint | `listenWith(publishPath, advertiseHost, CrossingViaFront)` | `svcendpoint/front.go`, `listen.go` |
| per-connection upstream dial | `splice` dials inside the accept-loop goroutine | `svcendpoint/front.go` (`splice`) |
| layer-attributable failure | `CrossingUnreachable` + `CrossingReasonUpstreamDial` on the audit record | `svcendpoint/crossing.go`, `front.go` |
| **stamp `jail_id`** | **none today** — but see [the retraction](#52a--retracted-the-front-never-parses-deliberately), this is a gap in the code, not a principle | `front.go`: *"splice does not parse the stream"*; `crossing.go`: parsing the request "is both unavailable at the front" |

So the folding is not "reimplement the relay in the front". It is: **switch the broker's `host_daemon` to `publishes: "socket"`, let the existing front serve it, and delete `internal/brokerrelay` — except for job 3.**

---

## 5. Jail identity — the actual design decision

### 5.1 Why `jail_id` exists

It is an **audit attribution field**, not a routing or authorization field. `internal/hostservice` reads `Request["jail_id"]` (defaulting to `"unknown"`) and emits it as `jail=<id>` in the per-request record (`internal/hostservice/hostservice.go`). The security property was stated in `svcendpoint/crossing.go`: the per-jail token is what *makes the relay's host-side stamp trustworthy*. Compare `hostservice`'s tier-2 behaviour, where a client-supplied `jail_id` is recorded verbatim and is explicitly untrusted (`tiers_test.go`).

So the invariant to preserve is narrow and worth naming:

> **I1.** A `jail_id` in an audit record was asserted by the **host**, never by the jail — a jail cannot forge another jail's identity into the log.

### 5.2 What is actually on the wire — the concrete version

*Added after review: the sections above assume the vocabulary. This one does not.*

**Not startup metadata, and not argv.** That is the first thing to get out of the way, because it is the intuitive reading and it cannot work. The broker is **one host-wide process serving every jail on the machine** — that is its entire reason to exist, since the flock it holds is what stops two jails burning the same single-use refresh token. So there is no moment at which you could tell it "you are serving jail X": by the time it starts, it does not yet know which jails will connect, and while it runs, the answer changes from one connection to the next. The identity is a property of the **connection**, not of the process.

**What a request looks like.** The loophole protocol is one request per connection, client-first: a 4-byte big-endian length, then that many bytes of UTF-8 JSON.

```
  what the jail sends                what the broker should see
  ┌────┬──────────────────────┐      ┌────┬───────────────────────────────────────┐
  │ 00 │ {"action":"refresh"} │  ──► │ 00 │ {"action":"refresh",                  │
  │ 00 │                      │      │ 00 │  "jail_id":"yolo-yolo-jail-7f3a"}     │
  │ 00 │                      │      │ 00 │                                       │
  │ 14 │                      │      │ 2f │  ▲ inserted by the HOST, overriding   │
  └────┴──────────────────────┘      └────┴──  any jail_id the client sent ───────┘
   4-byte BE length + JSON body       length recomputed — the body grew
```

**That insertion is "the stamp".** One key, `jail_id`, whose value is the container name. It exists so the audit line the broker's request logging emits reads `jail=yolo-yolo-jail-7f3a` and that field can be *believed* — `internal/hostservice` records a client-supplied `jail_id` verbatim and treats it as untrusted (`tiers_test.go`), which is exactly what the host-side override upgrades.

**Who the three processes are, and what each one can see:**

| | Where | What it is | Sees the payload? |
|---|---|---|---|
| **terminator** | in the jail | pretends to be `platform.claude.com` on `127.0.0.1:443` so `claude`'s own HTTPS call is intercepted; forwards the result over loopback-TLS | yes — it builds the request |
| **front** | on the host | a ~90-line TLS listener (`svcendpoint/front.go`) that accepts the jail's connection, checks its bearer token, and then runs two `io.Copy` loops — client→daemon and daemon→client | **not today** — though it already frames-and-reads the token off the same stream ([the retraction](#52a--retracted-the-front-never-parses-deliberately)) |
| **daemon** | on the host | the broker singleton: flock, upstream refresh, writes the shared creds file | yes — it parses the request |

So "the front" is not a component with opinions; it is the framework's TLS front door, and its whole contract is *"authenticate the jail, then copy bytes."*

### 5.2a ⚠ Retracted: "the front never parses, deliberately"

**Raised in review round 1, and the objection is correct.** Both earlier versions of this doc leaned on the front being a component that decodes nothing — `front.go` ("splice does not parse the stream") and `crossing.go` were cited as if they stated a principle the design must protect. The reviewer's question — *"it never decodes anything, except when it decodes the jail's bearer token?"* — dissolves that, and checking the code confirms it.

**The front already reads a length-prefixed frame off the same stream, before any splicing.** `verifyTokenFrame` (`svcendpoint/token.go`) does:

```
read 4 bytes  →  BigEndian.Uint32  →  cap at tokenFrameMax (4096, "CAP BEFORE
ALLOCATING")  →  read N bytes  →  subtle.ConstantTimeCompare  →  write authAck
```

under a 5-second `handshakeTimeout`, which it then clears to preserve the no-session-deadline contract. Compare `brokerrelay.readFirstMessage`: read 4 bytes, `BigEndian.Uint32`, cap at `firstMsgMax`, read N bytes, under a 5-second `firstMsgTimeout`. **These are the same operation.** The framework is not protocol-blind; it is protocol-blind *after* its own handshake, which is a description of where the splice loop starts, not a property anyone designed to defend.

Two supporting arguments I also had backwards:

- **"The payload format belongs to the daemon."** It does not. `internal/frameproto` is yolo's own package and its doc comment calls it *"the frame protocol v1 spoken between a jail-side client and a host-side loophole daemon … a frozen interop contract."* yolo owns the transport **and** the payload framing. A front that reads frame #1 is reading its own format, not reaching into someone else's.
- **"No per-request audit tier exists for fronted connections."** True, and it is a *consequence* of the splice not parsing — not a reason for it. Citing it as justification was circular.

**What actually differs, and it is the honest residue:**

| | Token frame | The stamp |
|---|---|---|
| bytes are | **consumed and discarded** — never part of the payload | **transformed and forwarded** — must come out the other side |
| a malformed read means | drop the connection (it failed auth) | **forward the original bytes verbatim** and carry on — an unparseable request is not an error, it is a request |
| applies to | every fronted connection, by construction | only daemons speaking `frameproto` — so it must be declared, not unconditional |

That is a difference in **complexity, not in kind**: transform-and-forward needs the verbatim-fallback paths that read-and-discard does not, which is `stampJailID` plus its three fallbacks — order-preserving decode, byte-identical `jsonx` re-encode, recomputed length prefix. Roughly a hundred lines, all of which already exist and work in `internal/brokerrelay`.

**What this changes downstream:** option D in [§5.4](#54-options) gets substantially cheaper, and invariant **I2** stops being a warning about a slippery slope and becomes an ordinary scoping rule. It also moves the weight of [OQ-BP2](#decision-ledger) off "is parsing acceptable" and onto the question that was always the better one — **whether a daemon-visible `jail_id` earns any mechanism at all**, given that nothing in `internal/oauthbroker` reads it.

### 5.3 The pivot nobody has used yet

**The front already knows which jail it is talking to.** It validated a bearer token that was minted per jail and written 0600 into that jail's own directory. The identity is therefore available at the front *before any payload byte is read* — it simply has nowhere to go, because the front's contract is to splice opaque bytes.

That reframes the question from *"how does the front learn the jail?"* (it already has) to *"how does it tell the daemon, without parsing?"* — **and possibly to "does it need to tell the daemon at all?"**, which is [OQ-BP2](#decision-ledger) and is the cheaper answer if `jail_id` is only ever a log field.

### 5.4 Options

| # | Option | What it means | Verdict |
|---|---|---|---|
| A | **Per-jail upstream socket** | the front dials a different Unix path per jail; the daemon infers identity from which socket the connection arrived on | ❌ **Rejected.** The broker is a host singleton by design (one flock, one creds file); giving it N listeners re-creates per-jail state in the one component that must not have it |
| B | **Framework preamble frame** | the front writes a small, framework-owned metadata frame on the upstream Unix connection before splicing | ✅ **RULED — this one, and my hedge on it was wrong** ([§5.5](#55-ruled--every-connection-is-raw-yolo-prepends-a-connection-preamble)). I wrote *"only acceptable if gated by an opt-in manifest key"*, treating "every daemon's read path changes" as the disqualifying cost. It is the **entire point**: one contract, every daemon, no parsing. The gate it needed was not opt-in but an opt-**out** for a dumb pipe |
| C | **`SO_PEERCRED`-style side channel** | identity carried out-of-band on the socket itself | ❌ **Rejected.** Peer credentials identify the *front* process, which is the same process for every jail |
| D | **Declared protocol-aware stamp** | manifest opts in: `stamps: "jail_id"`; the front parses frame #1 for daemons that ask, and only for them | ✅ **Viable and cheap** — cheaper than the first two versions of this doc claimed, since the front already frames-and-reads for auth and yolo owns `frameproto` too ([the retraction](#52a--retracted-the-front-never-parses-deliberately)). Declared rather than unconditional, because not every fronted daemon speaks the framed protocol |
| E | **Drop the stamp; let the daemon self-report** | broker logs whatever the client says | ❌ **Rejected.** Violates I1 for a field whose entire value is that it is trustworthy |
| F | **Keep a relay-shaped shim in the pack** | the pack ships its own per-jail relay binary | ❌ **Rejected.** `host_daemon` is host-wide and keyed by loophole name; there is no per-jail daemon vocabulary, so this needs a *new* mechanism to avoid a smaller one |
| **G** | **Drop the daemon-visible field; yolo records the identity itself** | no stamp, no parse; the front writes `jail=<id>` into its own audit record from the token it already validated, and the daemon simply never sees a `jail_id` | ⚖️ **Not chosen, and it stays half-true.** Its audit half is kept unconditionally by [§5.5](#55-ruled--every-connection-is-raw-yolo-prepends-a-connection-preamble) — yolo's tier-1 record is host-derived whatever the daemon sees. Its *other* half, denying the daemon any identity, is what B delivers better: the daemon gets one, without yolo reading its bytes |

~~**Recommendation: G, with D as the fallback.**~~ **Superseded by [§5.5](#55-ruled--every-connection-is-raw-yolo-prepends-a-connection-preamble) — the answer is B.** Recording the path, because I recommended three different options across three rounds and each was rejected for the same reason: I kept optimizing for *not disturbing existing daemons*, and the ruling each time was that disturbing them uniformly is cheaper than any mechanism that avoids it. D and G both survive as descriptions of roads not taken; neither is the design.

**I2 survives the change and gets simpler:** a connection still emits exactly one connection-level audit record. The connection preamble is not a request and does not create a per-request tier — it is one frame at connection open, in the host→daemon direction only.

### 5.5 RULED — every connection is raw; yolo prepends a connection preamble

**The ruling, 2026-08-15, after two rejected drafts of mine.** Draft one: mandatory payload stamp. Draft two: a `framed`/`raw` declaration, stamping the framed case. Both were rejected on the same ground, and the ground is right:

> *"I don't even want framed in here. Just raw. We can still send a formatted preamble of JSON or whatever, even a frame — but then it's just raw, that's it. And you can turn off that frame if you need a dumb pipe implementation."*

**The design that falls out.** yolo **never decodes the daemon's bytes**. Every connection is an opaque stream. What yolo adds is one **connection preamble** of its own, ahead of the stream, and then it gets out of the way:

```
daemon reads:  [4B BE len][{"jail_id":"yolo-…-7f3a","service":"host-processes","v":1}]  ← yolo's, always first
               [ ...the client's bytes, byte-for-byte untouched, forever... ]           ← never inspected
```

The declaration is no longer about the payload's shape — yolo has no opinion on that — it is one bit about whether the frame is sent at all: **`preamble: true` (default) · `false` for a dumb pipe.**

**Why this is better than both drafts, concretely.** It is not a compromise; it is smaller:

| | payload stamp (rejected) | connection preamble (ruled) |
|---|---|---|
| yolo parses the daemon's protocol | yes, frame #1 | **never** |
| code needed in the framework | decode, insert, re-encode byte-identically (`jsonx` Python-parity), recompute length, plus verbatim fallbacks for oversize / timeout / non-object | **write N bytes, then splice** |
| works for audio, video, HTTP, a database socket | no | yes |
| what a third-party daemon must implement | all of `frameproto` | read one length-prefixed frame |
| `readFirstMessage` + `stampJailID` + their three fallbacks | move into the framework | **deleted** |

That last row is the one that decides it. The relay's protocol-aware trick does not get promoted into `svcendpoint` — it **stops existing**, and with it the only place in yolo that had to re-serialize someone else's JSON byte-identically to keep a frozen wire contract.

**Where it goes (P5, revised).** On the accepted-connection wrapper in `svcendpoint`, where `newCountingConn(conn, l.service, l.jail, …)` already attaches the host-derived identity. The wrapper now **prefixes the read stream** instead of transforming it, which is a smaller change than either draft: no parser, no writer, no fallbacks. One implementation covers both server shapes — a fronted daemon reads it through the front's `io.Copy`, an endpoint-publishing daemon reads it directly — so the Go server library and a third-party daemon do exactly the same thing, which is the property that keeps them from drifting.

**What it carries today** — `jail_id` (host-derived, the reason it exists), `service` (which loophole this listener is), and `v` (the envelope version). It is host→daemon only, exactly once, at connection open; it never appears in the response direction, and the jail-side client never sees it, so a client cannot forge, suppress, or even observe it.

**On the name.** It is *not* called an identity frame, deliberately: identity is what it carries first, not what it is. The frame is the framework's channel for anything it needs to tell a daemon **about the connection** before the connection's own bytes start — and naming it for today's single field would make the second field look like a violation instead of an addition. `v` is what keeps that honest.

**One disambiguation for whoever implements it:** `svcendpoint` now has two length-prefixed frames on one connection, and they are opposites. The **token frame** is client→server, pre-auth, consumed and discarded (`verifyTokenFrame`). The **preamble** is host→daemon, post-auth, and is the only thing yolo ever *adds* to a stream. Neither is part of the daemon's own protocol.

**What it costs, stated plainly.** Every existing daemon's read path changes once — that is the breaking change, and it is in scope. All three affected daemons are yolo's own (`hostservice`, the journal bridge, the broker), and `hostservice` reading it once covers every Go daemon written since. A daemon that declares `preamble: false` gets a genuinely dumb pipe and, in exchange, nothing the preamble carries — no identity today, and none of whatever it carries later. That is the trade, and it should be the reason to think twice before setting it.

**And the property that holds either way:** yolo's tier-1 connection record derives `jail=` from the published endpoint path (`crossingIdentity`, `crossing.go`) regardless of the declaration. Turning the frame off costs the daemon its identity, never yolo its audit trail — so `preamble: false` is not a privacy switch, and cannot be used as one.

**Downstream deletions this unlocks:** `frameproto`'s `jail_id` request field becomes vestigial (`hostservice.JailID` reads the frame instead of `Request["jail_id"]`), `yolo-ps` stops self-reporting one, and `hostservice`'s two-tier asymmetry loses its cause — tier 2's `jail=` becomes as host-asserted as tier 1's, which is what `hostservice.go`'s package comment on the two tiers had to warn readers about.

---

## 6. What the pack looks like

> [!IMPORTANT]
> **CORRECTED 2026-08-18 by [OQ-A10](../reference/loophole-system.md#why-its-this-way) in [`loophole-activation.md`](../reference/loophole-system.md#activation).** This
> section designed a separate `packs/claude-oauth-broker/`. That is **wrong**: the broker's loophole
> is a **contribution of `packs/claude`**, not a pack of its own. R6's whole argument is that the
> dependency is structural — the broker exists to serve claude — and a separate pack reinstates the
> second selection step R6 deletes. A Bedrock user's escape is `supersedes` on the
> `claude-oauth-refresh` capability, which is already built and already declared, not deselection.
> The layout below is amended accordingly.

```
packs/claude/                        # the EXISTING official pack, one more contribution
  pack.json                          # + { "kind": "loophole", "from": "loopholes/claude-oauth-broker" }
  loopholes/claude-oauth-broker/
    manifest.jsonc                   # from bundled_loopholes/, except:
                                     #   host_daemon.publishes: "socket"      (required of packs)
                                     #   requires.command_on_path: DELETED    (R3/R6 — selecting
                                     #     the pack IS the dependency the sniff approximated)
                                     # preamble defaults to true (§5.5) —
                                     # nothing to declare; only a dumb pipe writes false
```

Everything else in the manifest — `serves`, `intercepts`, `broker_ip`, `ca_cert`, `state_files`,
`doctor_cmd` — is already correct and moves unchanged. The `{state}` token is explicitly designed to
survive a restage (`loopholedecl.TokenState`'s doc comment), which is what makes a pack-shipped CA possible at
all.

### 6.1 ✅ RESOLVED 2026-08-19 — `host_daemon.scope` is the vocabulary the spawn path lacked

*The blocker below was found 2026-08-18 by implementing the sprint's other two conversions and then
attempting this one. It was never a decision to make; it was a mechanism that did not exist. It
exists now — here is what was built, then the original statement of the gap, kept because it is the
argument for the shape.*

**What shipped.** One manifest key, in the grammar `publishes` and `request_end` already use:

```jsonc
"host_daemon": {
  "cmd": ["yolo", "internal", "daemon", "claude-oauth-broker", "--socket", "{socket}"],
  "publishes": "socket",
  "scope": "host"          // ← ScopeJail (default) | ScopeHost
}
```

`scope` names the dimension — *what is this daemon shared across* — rather than today's only
interesting answer, for [§5.5](#55-ruled--every-connection-is-raw-yolo-prepends-a-connection-preamble)'s reason for calling the preamble a preamble: naming a key for its
current single use makes the second use look like a violation. `ScopeJail` is the default, so no
already-shipped manifest changes, and readers compare against `ScopeHost` so a dropped field costs
a spawn rather than a shared daemon nobody ensured.

Four consequences, each of which is where a defect would have gone:

- **The spawn path DISPATCHES on the record, not the name.** `startLoopholes`'
  `if name == broker.BrokerLoopholeName { o.brokerEnsure(); continue }` is gone; the branch reads
  `hd.Scope == ScopeHost` and calls `startHostSingleton`, which ENSURES the daemon (the existing
  `internal/broker` flock/pid/recheck engine, generalized by one `Deps.Argv` field so the argv
  comes from the manifest) and then fronts it with `svcendpoint.ServeFront`.
- **The framework owns the singleton's path.** `paths.HostSingletonSocket(name)` =
  `/tmp/yolo-<name>.sock`, derived from the loophole NAME and nothing else, because a singleton has
  no jail to be keyed by. For `claude-oauth-broker` that is byte-identical to the existing
  `broker.BrokerSingletonSocket`, which is why the move needed no migration — a test pins the three
  pairs equal, and it has to hold or `yolo broker status`, `yolo check` and the front would each
  reach a different file.
- **`scope: "host"` REQUIRES `publishes: "socket"`, refused at load.** An endpoint file carries one
  jail's bearer token, so a host-wide daemon publishing one would hand every jail the same
  credential. Under `socket` the daemon binds once and each jail gets its own front and its own
  token — the only shape in which "one daemon, N jails" and "one credential per jail" are both true.
- **A jail ending closes ITS FRONT and nothing else.** `startHostSingleton`'s `stop()` deliberately
  does not do what the spawned path's does (SIGKILL the process group, unlink the socket); doing so
  would cut every other live jail off its credential path.

**And the daemon had to learn to read the preamble in the same commit.** `internal/oauthbroker`
called `hostservice.ServeUnix`; behind a front that is the silent-CORRUPTION direction [§12](#12-host-processes-as-the-proving-ground) names —
the preamble is consumed AS the request and every refresh fails. It calls `ServeFrontedUnix` now,
which is also what makes its `jail_id` host-asserted again after the relay's stamp was deleted (I1).
The knock-on: `broker.BrokerPing` could no longer speak the protocol from the host side without
forging a jail identity, so liveness became a connect probe (`SingletonReachable`) — the same
predicate every other fronted daemon already uses. The protocol round trip survives in `yolo
check`'s per-jail probe, which goes through a real front and therefore sends a real preamble.

---

*The original statement of the gap follows.*

**The chain, each link verified in the tree:**

1. A pack-shipped loophole **must** declare `publishes: "socket"`. `LoadPackLoophole` applies
   `PackShippedProblems`, and `packPublishesProblems` refuses every other value **including the
   default** (`internal/loopholedecl/packshipped.go`). So the manifest cannot move without the flip.
2. `startLoopholes` builds its spawn list from `set.ManifestHostDaemonSpecs(discovered)` — every
   loophole that declares a `host_daemon`. Under `publishes: "socket"` it calls
   `startExternalService`, which **spawns a fresh daemon** and binds it at
   `frontSocketFile(frontShortHash(socketsDir), name)` — a path **keyed per jail**
   (`internal/cli/run/loopholesruntime.go`).
3. The broker is a **host-wide singleton by design** ([§5.2](#52-what-is-actually-on-the-wire--the-concrete-version): *"one host-wide process serving every
   jail on the machine — that is its entire reason to exist"*), reached at the fixed
   `broker.BrokerSingletonSocket`, with `yolo broker status`/`stop`, `yolo check`'s broker section
   and `brokerEndpointIsUnpublishable` all reading that one path.

**So the conversion as designed needs a fourth thing nobody has designed: ONE daemon behind N
per-jail fronts.** [§7](#7-what-this-deletes-what-it-costs-what-it-forecloses) says the broker's `--socket` "becomes a *fronted* socket rather than a
host-to-host one" and stops there. The spawn path has no vocabulary for *ensure this host-wide
daemon* as against *spawn one for this jail*, and inventing one in a sprint whose subject is
deleting keys is the wrong shape of answer.

**What that does NOT mean.** It is not an argument against the move, and it is not blocked on a
ruling — it is [§10](#10-sequencing) steps 3 and 4 (the preamble/stamp work and the `publishes` flip plus the relay
deletion) turning out to be a **hard prerequisite** for step 5 rather than merely earlier than it.
[§10](#10-sequencing) already says step 4 "must not be split — a half-flipped broker is a jail with no credential
path"; what is added here is that step 5 cannot precede it either.

> [!WARNING]
> **And the reservation is still the trap.** Deleting `bundled_loopholes/claude-oauth-broker/` does
> **not** free the name: `broker.BrokerLoopholeName` is appended to `ReservedLoopholeNames()`
> **unconditionally**, from the broker's own constant, not from the bundled directory. That is what
> makes this move different from the two that shipped on 2026-08-18 — `host-processes` and `audio`
> were reserved only as bundled DIRECTORY names, so `git mv` retired their reservations for free,
> and a reader generalizing from them would ship a commit that refuses every claude user's launch
> (`run.PackLoopholeNameConflicts` is fatal). The reservation, the `startLoopholes` name special-case
> and the contribution must land in ONE commit.

## 7. What this deletes, what it costs, what it forecloses

**Deletes:** `internal/brokerrelay` (~4 files plus its lifecycle in `loopholesruntime.go` — pid file, lock, socket path, reaping, the `relayKill` ordering comments) · one of the four bundled loopholes · the last consumer of the `relayEnsure` special case in the run pipeline.

**DELETED 2026-08-19, and the tally came out larger than this line predicted.** Also gone: the `broker-relay` entry in `yolo internal daemon`'s dispatch, `Options.RelayKillGrace`, the run pipeline's orphan-relay backstop reap and its piggyback on the live-container enumeration, the attach path's relay healing, the pre-loopback-TLS "spare a live legacy relay" upgrade decision, and `svcendpoint`'s only `NoPreamble` user. `internal/prune`'s `ReapRelayOrphans` is KEPT for one release and re-documented as a legacy sweep — a host upgrading has live relays in `/tmp` right now, and the run-path backstop that used to collect them went with the machinery.

**Costs:** the front gains a declared, opt-in parse ([§5.4](#54-options)) · the broker's `--socket` becomes a *fronted* socket rather than a host-to-host one, so its threat model changes from "nothing in a jail can reach this" to "the front is what stands in front of this" — the same position every other fronted daemon is already in · one more official pack in the embedded set the pack-loading tests walk.

**Forecloses:** nothing structural. If D proves wrong, the relay can come back as a `host_daemon` of a *different* loophole without touching the front.

---

## 8. Non-goals — what this does not license

- **Not** a general per-jail daemon mechanism. Option F is rejected precisely to avoid inventing one for a single consumer.
- **Not** a change to the transport. Loopback-TLS stays the only hop; this is about what sits behind the front.
- **Not** a widening of the pack-shipped subset *for the broker*. This loophole needs no new host-crossing vocabulary. **But the sprint goal does** — retiring the bundled channel means converting `audio` too, and that is blocked on **[OQ-LP14](../reference/loophole-system.md#oq-lp14)** ([§11](#11-what-no-bundled-loopholes-additionally-requires)). The distinction to hold: the broker does not depend on LP14; "no bundled loopholes" does.
- **Not** a fetched-pack broker. Everything about *this* loophole assumes an **official** pack. [§3.1](#31-what-is-actually-unresolved-here) designs the pack-shipped binary capability in general, but whether a **fetched** pack may ship a *host-side* binary is [OQ-BP6](#OQ-BP6)'s, answered 2026-09-30 (yes, disclosed), and is not needed here.
- **Not** a general artifact-caching or dependency system. [§3.1](#31-what-is-actually-unresolved-here)'s download is one verified file per platform per loophole, fetched at install and keyed by digest — it is not a package manager, and it should not grow into one.
- **Not** a change to how credentials are merged, harvested or written. That is [`pack-code-separation.md`](../reference/pack-system.md#what-a-pack-is-on-disk) [§5](../reference/pack-system.md#what-a-pack-is-on-disk), decided separately and landing first.

---

## 9. Risks

| Risk | Mitigation |
|---|---|
| The preamble accretes fields until it is a second protocol | It is **meant** to grow — that is why it is not named for today's contents — so the discipline is not "keep it empty" but "keep it a versioned envelope": `v` is mandatory, the key set is closed *per version* and reviewed as a schema change, and a daemon that does not recognize a version fails loudly rather than guessing. Growth is a decision each time, not a slope |
| `preamble: false` becomes the way to dodge auditing | It cannot: tier-1's `jail=` is derived from the published endpoint path regardless of the declaration ([§5.5](#55-ruled--every-connection-is-raw-yolo-prepends-a-connection-preamble)). Turning the frame off costs the daemon its identity, never yolo its audit trail |
| The frame silently breaks a daemon nobody remembered to update | It is a **breaking change on purpose** and the blast radius is three in-tree daemons; the migration test is that a daemon which does NOT read the frame fails loudly at its first request rather than misparsing one — the frame is length-prefixed, so a naive reader sees a length it cannot use, not a plausible-looking request |
| Deleting the relay loses the bounded-drain behaviour that makes a dial failure a clean EOF | It is not lost — `front.go`'s splice already distinguishes the case and marks `CrossingUnreachable`; the drain semantics must be **pinned by a test** before the relay is deleted, not after |
| A fronted broker socket is reachable by something on the host that the host-only relay socket excluded | The socket path stays where it is (`/tmp`, host-only, 0600); the front is an additional listener, not a relocation |
| The unexercised jail-binary path ([§3](#3-blocker-1-re-examined--the-jail-side-binary-is-already-expressible)) turns out to have a real defect once something uses it | Prove it with a throwaway pack shipping a two-line binary **before** committing to the broker move — it is the cheapest possible test of P1 |
| `platforms` forces the pack to enumerate arches yolo's own release does not build | Now on the critical path, since the two ship together ([OQ-BP1](#decision-ledger)): the release process must produce the matrix the manifest declares, or `platforms` must narrow to what it actually builds. Declaring more than you build is the failure mode — it turns "unsupported here" into "supported, missing". [BP-D7](#BP-D7) makes an official binary's builds exactly the release's platforms that its `platforms` admits, read from the release's own config by a census test ([§14.4](#144-the-plan-in-order), step 2): `TestEveryOfficialBinaryIsOnTheReleaseMatrix`, built 2026-09-30, which replaced a tripwire that refused any official `binaries` at all |
| **Shipping both at once means the sprint fails together** — a stalled binary capability holds a finished broker move hostage | Keep them separable *in the tree* even though they land in one sprint: the broker's manifest works on a baked daemon, so the binary work can slip to a follow-up commit without reverting anything. The ruling is about the sprint's end state, not about coupling the commits |
| A declared download turns `pack install` into a network-dependent step that can fail | Fetch at install only, never at launch ([§3.1](#31-what-is-actually-unresolved-here)), so a failure lands where the user is already waiting on the network — and a verified artifact is cached by digest, so a reinstall of the same pin is offline |
| A digest mismatch is treated as a transient error and retried past | It is an **integrity failure**, not a fetch failure: refuse the install, name both digests, and do not fall back to the cached copy — the whole point of P4 is that the bytes are the pin |

---

## 10. Sequencing

What I would build, in order.

**First, settle [OQ-BP1](#decision-ledger)**, because it decides whether [§3](#3-blocker-1-re-examined--the-jail-side-binary-is-already-expressible)'s distribution question exists at all. If the answer is "an official pack may keep baked daemons", steps 2 and 3 shrink to a manifest move.

~~**Second, prove P1 cheaply** — a throwaway local pack whose `jail_daemon.cmd` is `["{jail_loophole_dir}/bin/hello"]`, carrying a statically linked two-line binary. This is an afternoon, and it converts "the mechanism appears to exist" into "the mechanism works". Do this even if [OQ-BP1](#decision-ledger) says the broker keeps its baked daemon: the finding is worth having on its own.~~

**DONE 2026-09-17, and the finding was worth having: the mechanism works through one delivery route and cannot work through the other.** `packs/hello-daemon` is the throwaway, pinned by `internal/packload/packshippedjailbinary_test.go`. Everything DECLARATIVE held — `{jail_loophole_dir}` resolves in `jail_daemon.cmd`, the module dir mounts `:ro` with no `noexec`, a jail-daemon-only manifest is accepted, and such a loophole publishes no endpoint so the fatal reachability witness never sees it. **No schema gap.** The gap is entirely in delivery, and it is [§3.1](#31-what-is-actually-unresolved-here) Option A's, now corrected there.

**Three things this experiment establishes that reading could not:**

1. **The embedded route is a dead end for executables**, for a reason no copier can fix (`embed.FS` has no exec bit). An official pack cannot ship a program this way.
2. **The failure is SILENT, which is worse than the failure.** `supervisor.superviseOne` consults the restart policy only in `waitAndMaybeRestart`, reached after a *successful* start. A `permission denied` from `cmd.Start` took the spawn-failure arm, which **discarded the error**, slept, and retried on a 1s→30s backoff for the life of the jail — while `openLog` had already created an empty `<name>.log`. Empty log, no process, no diagnostic. **That was its own defect, and it is FIXED as of 2026-09-17**: `supervisor.superviseOne`'s spawn-failure arm now logs the error and obeys the restart policy (`"no"` gives up and says so; the others back off and say that).
3. **A shipped program is a new way in for the exec bit, and the disclosure reasoning has an exception now.** `packload.ExecutablesClaimKind` already enumerates shipped executables but is deliberately not `ReviewWorthy`, on the argument that *"a pack shipping a script is not a crossing — `bash file.sh` never needed the bit"*. A `jail_daemon` is: yolo itself spawns the file **by absolute path at boot**, no PATH involved, and the bit is load-bearing. The blast-radius argument still holds (it is inside the jail, this section's own ruling), but that sentence now has an exception.

What the experiment did NOT carry: a real ELF (the pack ships a `#!/bin/sh` program — byte-for-byte the same test for every property under test, since a shebang needs the exec bit exactly as an ELF does, but it does not exercise nix-ld), the live container (that the `:ro` bind permits exec and the supervisor spawns it — `packs/hello-daemon/README.md` carries the one-command recipe), and every non-podman backend.

**Third, the stamp.** Add the declared stamp to the front ([§5.4](#54-options)-D) with I1 and I2 as its tests, while the relay is still in place and still the thing running. Two implementations of the stamp can coexist for exactly one commit.

**Fourth, flip the broker to `publishes: "socket"`** and delete `internal/brokerrelay` plus its lifecycle in `loopholesruntime.go`. This is the step that must not be split — a half-flipped broker is a jail with no credential path. **SHIPPED 2026-08-19**, in one commit, and it needed [§6.1](#61--resolved-2026-08-19--host_daemonscope-is-the-vocabulary-the-spawn-path-lacked)'s `scope` vocabulary plus two things this sequencing did not name: the daemon moving to `ServeFrontedUnix` (or the front's preamble is eaten as the request), and the host-side liveness ping becoming a connect probe (or every healthy broker reads as dead and is respawned on every launch).

**Fifth, move the manifest into an official pack** — `packs/claude`, per [OQ-A10](../reference/loophole-system.md#why-its-this-way), not a pack of its own — and retire the bundled copy. This is also the step where [OQ-LP11](../reference/loophole-system.md#oq-lp11)'s consolidation finally gets one channel emptier, which it has been owed since 2026-08-14. **SHIPPED 2026-08-19**, in one commit as [§6.1](#61--resolved-2026-08-19--host_daemonscope-is-the-vocabulary-the-spawn-path-lacked)'s warning requires, and it turned out to be larger than "move the manifest and delete the reservation" — see [§13](#13-what-empty-the-channel-actually-required--measured-2026-08-19).

> **Step 5 CANNOT precede steps 3–4, measured 2026-08-18 ([§6.1](#61--resolved-2026-08-19--host_daemonscope-is-the-vocabulary-the-spawn-path-lacked)).** The pack-shipped subset requires `publishes: "socket"`, and yolo's spawn path answers that by spawning a daemon **per jail** at a per-jail socket — while the broker is a host-wide singleton by design. The ordering above already implies it; what was not stated is that it is a hard dependency rather than a preference, and that attempting 5 first produces a manifest yolo refuses to load rather than a broker that runs twice. **Both dependencies are discharged as of 2026-08-19**, and step 5 is unblocked.

**Sixth — and it landed FIRST, on 2026-08-18, because it is independent of all five:** gate `brokerEnsure` and `ensureBrokerRelay` on the loophole record ([OQ-A11](../reference/loophole-system.md#why-its-this-way)). Until then the singleton ran on every launch for every user with no lookup at all, while the jail was wired to it only when the loophole was Active. Doing it early matters for a reason the ruling names: after the move, a jail that does not select `packs: ["claude"]` has no broker in any surface, and yolo spawning the singleton anyway would be a daemon none of its own surfaces name.

**And the binary capability ([§3.1](#31-what-is-actually-unresolved-here)) runs alongside, not after** — *its mechanism is built (2026-09-30, [BP-D1](#BP-D1) to [BP-D6](#BP-D6)); the release matrix is decided in [§14](#14-the-release-matrix) and built but for its last step* — [OQ-BP1](#decision-ledger) was ruled *ship both at once*, overruling my "adopt it later". Its own order is unchanged: selection convention, then download-with-digest, then the two gates. What the ruling changes is that it must be *finished* in this sprint rather than queued behind a working broker, so its slowest piece — the release matrix producing per-platform artifacts — should start early rather than last. The one thing I would preserve from the rejected sequencing is **separability in the tree**: the broker's manifest is correct on a baked daemon, so the two can land as independent commits inside one sprint without either blocking the other's review.

---

## 11. What "no bundled loopholes" additionally requires

The second ruling is the larger one: `bundled_loopholes/` should be **empty** at the end of the sprint, not shorter. That is three conversions, and only one of them is this document.

| Loophole | What blocks it becoming a pack | Size |
|---|---|---|
| **claude-oauth-broker** | ~~`publishes` defaults to `endpoint`; the pack-shipped subset accepts **only `socket`**. Plus folding the relay away — and the fact that `publishes: "socket"` spawns a daemon PER JAIL while this one is a host-wide singleton ([§6.1](#61--resolved-2026-08-19--host_daemonscope-is-the-vocabulary-the-spawn-path-lacked))~~ | this doc · ✅ **SHIPPED 2026-08-19** as a contribution of `packs/claude`. The reservation was retired in the same commit — it had to be, and it took the whole reserved-name mechanism with it, the broker being its last entry ([§13](#13-what-empty-the-channel-actually-required--measured-2026-08-19)) |
| **host-processes** | The same `publishes` problem and nothing else: its `host_daemon.cmd` passes `{endpoint}` and publishes for itself, so it converts to `{socket}` + the framework front with no relay and no per-jail anything | ✅ **SHIPPED 2026-08-18** as `packs/host-processes`; the subset accepted the manifest unchanged |
| **audio** | ~~**[OQ-LP14](../reference/loophole-system.md#oq-lp14).**~~ Its `host_bind_mounts` and `requires.file_exists` both name `${XDG_RUNTIME_DIR}/pulse/native` and `pipewire-0`, which the pack-shipped path rule refused in every spelling | ✅ **SHIPPED 2026-08-18.** LP14 withdrew the rule rather than adding vocabulary; the two audio loopholes merged into `packs/audio` under the plain name, which deleting the bundled copy freed |

Three things follow, and the first is the one to notice:

- **[OQ-LP14](../reference/loophole-system.md#oq-lp14) stops being adjacent and becomes a hard dependency of the sprint goal.** I recommended the opposite one message ago ([OQ-BP3](#decision-ledger): "proceed beside it"), and that recommendation is **withdrawn for the sprint** while remaining correct for the broker in isolation. The roadmap already carries a leaning for LP14 — a closed, yolo-resolved list of runtime sockets — so this is a ruling to make, not a design to invent.
- **The `publishes` subset rule is the common blocker, and it is load-bearing rather than accidental.** It exists so a pack-shipped daemon cannot get TLS, token handling or endpoint permissions wrong; converting all three onto the framework front is the same work as honoring it. Worth stating because "all three bundled loopholes violate the pack-shipped subset" sounds like a rule that is too strict, and it is not — it is a rule they predate.
- **`host-processes` is the cheap one and should go first.** It exercises the whole conversion path — subset validation, official-pack staging, the front, `doctor_cmd` — with none of the broker's complexity. If something structural is wrong with converting a bundled loophole, it will show up there for a fraction of the cost.

**And one finding worth carrying into [§12](#12-host-processes-as-the-proving-ground):** `yolo-ps` sent its own `jail_id` from inside the jail, which `hostservice` recorded verbatim as untrusted. (It no longer does: `cmd/yolo-ps`'s header records that the client stopped naming its own jail once the preamble carried the host's assertion.) So the loophole chosen as the proving ground is *exactly* the one whose attribution the [§5.5](#55-ruled--every-connection-is-raw-yolo-prepends-a-connection-preamble) ruling fixes — it stops being a client's claim and becomes yolo's assertion.

---

## 12. `host-processes` as the proving ground

**Why this one.** It exercises the entire conversion path — pack-shipped subset validation, official-pack staging, `publishes: "socket"`, the framework front, `doctor_cmd`, and the new identity rule — while having none of the broker's complexity: no relay, no CA, no intercept, no credential file, no single-use token to burn if it goes wrong. If something structural is wrong with converting a bundled loophole, it surfaces here for a fraction of the cost. **Nothing about it is blocked**: it needs no answer from [OQ-LP14](../reference/loophole-system.md#oq-lp14) (no runtime-dir sockets), [OQ-BP5](#OQ-BP5) or [OQ-BP6](#OQ-BP6) (no shipped binary — `yolo-ps` stays baked, which an official pack may do).

**What it is today.** `bundled_loopholes/host-processes/manifest.jsonc` declares `requires.command_on_path: ps`, `transport: loopback-tls`, a `host_daemon` whose cmd is `["yolo","internal","daemon","host-processes","--endpoint","{endpoint}"]`, and a `doctor_cmd`. The daemon publishes its own endpoint via `hostservice.ServeEndpoint` → `svcendpoint.Listen`. The jail-side client is the baked `cmd/yolo-ps`, which reads `YOLO_SERVICE_HOST_PROCESSES_ENDPOINT` and self-reports a `jail_id` nobody trusts.

**The five changes, and what each one proves:**

| # | Change | What it proves |
|---|---|---|
| 1 | Daemon moves from `ServeEndpoint`/`{endpoint}` to `ServeUnix`/`{socket}`, with `publishes: "socket"` in the manifest | the framework front can carry a real daemon — the same flip the broker needs, without the relay |
| 2 | The connection preamble is prepended on the accepted connection (**P5**), `preamble` defaulting to true | the [§5.5](#55-ruled--every-connection-is-raw-yolo-prepends-a-connection-preamble) rule works for an endpoint-shaped daemon *and* a fronted one, from one implementation — and `hostservice` reading it once covers every Go daemon |
| 3 | `yolo-ps` stops self-reporting `jail_id` | the field's only source is now the host — and tier 2's `jail=` becomes as trustworthy as tier 1's |
| 4 | Manifest moves to `packs/host-processes/loopholes/host-processes/`, an official pack; bundled copy deleted | a bundled loophole can become a pack at all — staging, selection, exclusivity pre-flight, `doctor_cmd` |
| 5 | `requires.command_on_path: ps` and the workspace `host_processes.visible` list keep working | the pack-shipped subset's `requires` rule accepts a real manifest unchanged |

**Settled decisions this rests on**, so implementation does not have to re-litigate them:

- **The connection preamble's home is the accepted-connection wrapper**, not `ServeFront` ([§5.5](#55-ruled--every-connection-is-raw-yolo-prepends-a-connection-preamble), P5) — one implementation for both server shapes, and a prefix rather than a parse.
- **`preamble` defaults to true**, so no manifest declares anything to keep working. ~~`false` exists for a dumb pipe, of which there are none today.~~ **Half wrong, corrected 2026-08-15:** true of *manifests*, false of the **config** surface. Every `loopholes:` entry in a `yolo-jail.jsonc` that carries a `command` is given `publishes: socket` + loopback-TLS unconditionally (`synthesizeConfigLoopholes`, `internal/loopholes/discover.go`), and that code's own comment calls such a daemon *"a THIRD-PARTY PROGRAM yolo did not write"*. Those are dumb pipes **by construction** and there are real ones in the tree's own tests. So the default is **off for `Source == SourceConfig`**, with a `preamble` key added to the config spec to opt in — yolo declines to prepend bytes to a program whose protocol it has never seen and whose author never declared anything.
- **`publishes: "socket"` for every converted loophole**, because the pack-shipped subset requires it (`packPublishesProblems`, `packshipped.go`) — the three bundled loopholes predate that rule rather than disproving it ([§11](#11-what-no-bundled-loopholes-additionally-requires)).
- **A baked client binary is fine for an official pack** — `yolo-ps` does not become a shipped artifact, so [§3.1](#31-what-is-actually-unresolved-here)'s binary work stays off this critical path.
- ~~**`{endpoint}` survives** for yolo's own non-loophole services (the journal bridge still publishes its own, in `journaldcmd.go`).~~ **FALSE as of 2026-08-18**: the journal bridge became a pack-shipped loophole, so it had to take `publishes: "socket"` like everything else — `journald.ServeEndpoint` and the `--endpoint` flag are DELETED (the flag refuses, naming `--socket`). `publishes: "endpoint"` now has exactly ONE user left, this document's own subject, which makes the follow-on below smaller than it was rather than moot. Whether the *manifest key* `publishes: "endpoint"` should be retired once its last loophole user is gone is a genuine follow-on — it is not needed to finish the sprint, and ~~[OQ-BP4](#decision-ledger)'s end state makes it a two-line deletion~~ — **that last clause is REFUTED, measured 2026-09-14; see [§13](#13-what-empty-the-channel-actually-required--measured-2026-08-19) item 3.**

~~**The order to build it in:** change 2 first, while the relay still exists and still stamps. The two coexist without a flag-day because the connection preamble is **additive** — a daemon that has been taught to read it sees `[connection preamble][request]`, and the relay's redundant in-payload `jail_id` is simply ignored rather than conflicting.~~

> **⚠ Retracted, 2026-08-15 — "additive" is FALSE, and this was the most dangerous sentence in the document.** Found by implementation scouting, not by review. The relay did not merely carry a redundant field: `brokerrelay.readFirstMessage` read the **first length-prefixed frame** off its fronted connection and `stampJailID` rewrote it. Put a preamble in front of that and the relay stamps **the preamble**, writes it to the broker, and the terminator's real request sits unread behind it — **every Claude OAuth refresh in every jail fails.**
>
> The correction is one line of code and it must land in the *same* commit as the preamble: the relay's front set `NoPreamble: true`. The relay is the one deliberate opt-out in the tree, and it stays one until the broker conversion deletes it.
>
> It was at least a loud failure rather than a silent one: `TestRelayFrontPreservesJailIDStamp` failed under `just test-fast` if the opt-out was forgotten (both it and the relay are gone since 2026-08-19). That test was written for another reason and is now a tripwire on the credential path.

**The order to build it in:** change 2 first, with the relay opt-out in the same commit. Then 1 and 3 together, since 3 is only correct once 2 is in. Then 4, which is the part that either works immediately or teaches us something. Change 5 is a verification, not an edit.

**What "it worked" looks like:** `yolo-ps` returns the same output from inside a jail; the tier-1 connection record and the tier-2 request line agree on `jail=`; a client that sends a *spoofed* `jail_id` sees it overridden in both; `yolo check` still reports the loophole's doctor result; and `bundled_loopholes/` has one fewer directory with nothing in core mentioning `host-processes` by name.

---

## 13. What "empty the channel" actually required — measured 2026-08-19

[§11](#11-what-no-bundled-loopholes-additionally-requires) asked what "no bundled loopholes" needs beyond the three conversions and answered in
terms of the conversions themselves. Doing it turned up four more things, each of which
had to land in the SAME commit because the Go embed forces it: `//go:embed` cannot embed
nothing, so deleting the last directory deletes the package, which deletes every importer.
Recorded here rather than in a commit message because three of the four are decisions.

**1. The reserved-name MECHANISM is gone, not just the broker's entry.**
`loopholes.ReservedLoopholeNames()` composed two contributors — `broker.BrokerLoopholeName`
unconditionally, and the bundled directory names off the embed. Retiring the broker's
reservation emptied both at once, and a composed set with no contributors is the exact
shape this codebase keeps writing essays about (`journal` was "reserved in fact and
enforced nowhere"; an empty set is "enforced over nothing"). So `ReservedName`,
`ReservedLoopholeNames` and the pack-vs-reserved branch of `run.PackLoopholeNameConflicts`
are deleted, and the pre-flight keeps only its pack-vs-pack half.

What protects `claude-oauth-broker` — the one name yolo still reaches by literal, from
`yolo broker status`, `yolo check`'s broker section, `brokerEnsure` and the in-jail
terminator's endpoint variable — is now two things, and the second is item 2:

- `packs/claude` OCCUPIES the name, and loophole names are sole-owned across packs,
  fatally. A second claimant refuses the launch by name for everyone who selected claude.
- Without claude selected a pack MAY claim it, bounded by the origin gate. That is the
  same bound `cgroup-delegate` took when it retired its own reservation and the case [OQ-A3](../reference/loophole-system.md#why-its-this-way)
  already admits.

**2. `brokerLoopholeActive` had to become gate-aware, and the codebase had already said so.**
The comment beside `cgroupDelegateHonored` stated the conditional in as many words: that
predicate is `Honored`-shaped while the broker's "may stop at `Active()` because the
broker's record is BUNDLED — yolo's own manifest, in yolo's own tree, under a name no pack
may claim." All three clauses expired in this commit. It now asks
`lp.Active() && set.MayRunHostCode(lp)`. This is not cosmetic: the predicate switches the
in-jail TLS terminator, the CA mount, the endpoint variable, and — through `run.go` — the
host singleton spawn.

**3. The pack-shipped SUBSET is now universal, and two exemptions died with the label.**
`SourceBundled` meant "the yolo binary's own content", and two rules hung off it:
`publishes: "endpoint"` was available to a bundled manifest, and the [§4.3a](../reference/loophole-system.md#trust-what-is-gated-and-what-is-not) PLACEMENT rule
exempted a bundled module dir (the self-hosting case — yolo's own jail mounted
`bundled_loopholes/` `:rw`). Both are gone. The placement one needed no replacement: a
pack's module dir is its STAGED copy under `paths.AgentsDir()`, outside every workspace by
construction, so the collision is unrepresentable rather than exempted. The publishes one
leaves **`publishes: "endpoint"` undeclarable by any loophole a launch will honor** — [§12](#12-host-processes-as-the-proving-ground) called retiring
the key a "genuine follow-on… not needed to finish the sprint", and that is still true.

> [!WARNING]
> **~~It is now a two-line deletion of an unreachable enum member.~~ REFUTED, measured
> 2026-09-14 at `afa80cfe`.** `PublishesEndpoint` is the DECODER'S DEFAULT for an absent
> `publishes` (`parseHostDaemon`), not an inert value, and three refusals are expressed
> THROUGH that default rather than beside it: the pack-shipped subset's refusal of a manifest
> that says nothing about publication (`packPublishesProblems`, whose own comment calls
> refusing the default deliberate and is why it needs no declared-versus-defaulted bit), the
> `scope: "host"` credential rule, and the value the decoded record carries. Deleting the
> member forces the default to change, and the obvious replacement — defaulting to `socket` —
> turns all three into silent acceptances: that one-line change fails **ten** tests across
> `internal/loopholedecl` and `internal/loopholes`, and the classic self-publishing manifest
> (`cmd` naming `{endpoint}`, `publishes` omitted) flips from the subset's fix-naming refusal
> to a decoder error telling its author to write `{socket}` — the wrong fix, aimed at exactly
> the population the retirement is for. "No possible declarer" is also true of the LAUNCH PATH
> and not of the parser: the value still decodes through both unrestricted reads item 4 names.
> Retiring the KEY stays open; retiring the enum MEMBER is not the way to do it.

**4. The unrestricted loader survives with exactly one production caller.**
`LoadLoophole` (tolerant, full vocabulary) is what `run.resolveInertLoophole` uses for the
platform/inert report; `LoadPackLoophole` (tolerant + subset) is what every discovery read
goes through. Tests whose subject IS the wider vocabulary — `${VAR}` expansion in
`requires.file_exists`, the argv builder over `jail_env`/absolute `ca_cert` — were moved
onto `LoadLoophole` rather than onto a lenient discovery source, because there is no longer
a lenient discovery source and pretending otherwise would have turned "does the loader read
this key" into "does the subset permit it".

**What did NOT need changing, which is the part worth trusting:** the loophole's `{state}`
dir is keyed by NAME, so an upgrading host keeps its CA and every running jail keeps
trusting it; the endpoint file, its env var and the in-jail terminator are functions of the
name and the transport, so nothing jail-facing moved; and `flake.nix` listed
`bundled_loopholes` under `pathExists`, so the image build degraded silently — the comment
there had to be updated by hand or it would have been the only trace left.

---

## 14. The release matrix

The **release matrix** is what yolo's own release does for the binaries official packs declare:
which platforms it builds each one for, where it publishes the files, and how each file's
`sha256` gets into the manifest the same release embeds. [OQ-BP1](#decision-ledger) put it on the
critical path, and it is the part of the binary capability that [BP-D1](#BP-D1) to
[BP-D6](#BP-D6) left out.

Each of its three parts has one answer once you look at what the release already does, so each
is an implementation decision: [BP-D7](#BP-D7) (platforms), [BP-D8](#BP-D8) (publication) and
[BP-D9](#BP-D9) (the digest). What main's tree pins between two releases is a call about what a
build from source promises, filed as [OQ-BP7](#OQ-BP7).

**Steps 1 to 6 of [§14.4](#144-the-plan-in-order) are built (2026-09-30), over no binary
yet.** No official pack declares `binaries` (re-checked 2026-09-30), so each gate passes without
building anything, and the pin tool's tests run over a fixture checkout. The first `binaries`
entry is held to the matrix by the census the moment it is written, and step 7 waits on
[OQ-BP7](#OQ-BP7). The matrix covers programs yolo builds. An official pack that pins a program someone else
releases declares that upstream's builds and narrows `platforms` to them, and none of this
applies to it.

### 14.1 What the release already fixes

Verified 2026-09-30 at `f596a969`.

| The release today | Where | What it settles |
| :--- | :--- | :--- |
| `yolo` is built for `linux` and `darwin`, each on `amd64` and `arm64`, with cgo off, on one `ubuntu-latest` runner. The PyPI wheels cover the same four pairs | [`.goreleaser.yaml`](../../.goreleaser.yaml) `builds`; `platformKeys` in [`build-wheels`](../../tools/build-wheels/main.go) | the host platforms |
| The bundle cross-compiles the jail's binaries for `linux/amd64` and `linux/arm64` | [`stage-source-bundle.sh`](../../scripts/stage-source-bundle.sh) | the jail platforms |
| goreleaser uploads each platform's archive and `checksums.txt` to the tag's GitHub release. An asset URL there redirects over https to `release-assets.githubusercontent.com` (measured with `curl -I` on v0.11.0's `checksums.txt`) | [`release.yml`](../../.github/workflows/release.yml), the `goreleaser` job | where the files go |
| The Homebrew formula builds `yolo` from the tag's source tarball, and is pushed only after goreleaser succeeds | `release.yml`, the `update-homebrew` job | the digest has to be in the tagged tree |
| goreleaser refuses to release from a dirty tree | `release.yml`, beside the notes extraction | nothing may rewrite a manifest at release time |
| `just release` refuses a version whose changelog section [`changelog-section.sh`](../../scripts/changelog-section.sh) rejects, and both workflows run that script again | the `release` recipe in [`Justfile`](../../Justfile); both workflows | the shape of the gate |
| PyPI publishes off the same tag push, gated only by that changelog check, and not ordered after `release.yml` | [`publish.yml`](../../.github/workflows/publish.yml) | a window, named below |
| [`build-go.sh`](../../scripts/build-go.sh) stamps the version and the commit with `-X`, Go stamps VCS facts by default, and the release's Go is whatever `setup-go` reads from [`go.mod`](../../go.mod) (`go 1.26.0`, no `toolchain` line) | those files | why the build recipe is its own |

### 14.2 What was measured

On 2026-09-30, in this jail, with go1.26.7:

- **The checkout's path and the build cache do not move the bytes.** `yolo-ps` and `yolo-serial`
  were each built twice, once from the checkout and once from a `git archive` copy at another
  path, with separate build caches, using `-trimpath -buildvcs=false`, cgo off and no `-ldflags`.
  All eight pairs, over `linux/amd64`, `linux/arm64`, `darwin/amd64` and `darwin/arm64`, were
  byte-identical. The `darwin/arm64` builds carry the ad-hoc code signature Go's linker writes
  (an `LC_CODE_SIGNATURE` load command), inside those identical bytes.
- **Go's default VCS stamp does move them.** With `-buildvcs` left at its default, the checkout's
  build embeds `vcs.revision`, the commit, and the pair differs.
- **The toolchain is in the bytes** (`go version -m` prints `go1.26.7`). Go's own distribution of
  that version, fetched as the `golang.org/toolchain` module that `GOTOOLCHAIN` downloads, built
  the same bytes as the jail's mise-installed `go1.26.7`, on `linux/amd64` and `darwin/arm64`.
- **A manifest edit moves only a program that links the packs embed.** After an edit to an
  embedded loophole manifest, `yolo-ps` built the same bytes and `yolo-jaild` did not. Today
  `yolo`, `yolo-entrypoint` and `yolo-jaild` link the embed; `yolo-ps`, `yolo-serial`,
  `yolo-journalctl` and `yolo-cglimit` do not.
- **Unmeasured:** the same build on two machines, the maintainer's and a GitHub runner. The rebuild
  the release runs before it uploads anything ([§14.4](#144-the-plan-in-order), step 5) is the
  instrument. When it disagrees, the release is refused rather than published wrong.

### 14.3 What the release builds, where it puts it, and how the digest gets in

**Platforms** ([BP-D7](#BP-D7)). Each official binary has a build for exactly the platforms it
runs on among those the release ships `yolo` to, no more and no fewer:

- a `{binary:<name>}` reference gets one build for each `<goos>/<goarch>` of goreleaser's `yolo`
  build that the loophole's `platforms` admits;
- a `{jail_binary:<name>}` reference gets `linux/<arch>` for each architecture among those;
- nothing gets a `darwin` jail build, which the macos-user guest would decline
  ([BP-D6](#BP-D6)), or a build for a platform the release does not ship `yolo` to.

The program is Go from this module, built with cgo off, from `cmd/<name>`, where `<name>` is its
`binaries` key.

**Publication** ([BP-D8](#BP-D8)). The files go on the tag's own GitHub release, beside the
archives, one bare executable per build, each listed in `checksums.txt`:

```text
https://github.com/mschulkind-oss/yolo-jail/releases/download/v<version>/<name>_<version>_<goos>_<goarch>
```

Every release uploads every official build, whether its digest moved or not, so a version's
manifests name files on that version's release and no other.

**The digest** ([BP-D9](#BP-D9)). Each build's `url` and `sha256` are committed in the tree the
tag names. The **pin tool** *(coined here: the one program that builds every official build,
writes its `url` and `sha256` into the manifest, and checks them)* writes them before the tag.
Three gates rebuild and compare: `just release` before the tag, the release's goreleaser run
before it uploads, and `publish.yml` before PyPI. Every one of those builds uses one recipe:

- **the toolchain**: Go's own distribution of one pinned version, fetched as the
  `golang.org/toolchain` module, never whatever `go` is on the machine's PATH;
- **the flags**: `CGO_ENABLED=0`, `-trimpath`, `-buildvcs=false`, `-mod=vendor`, and no
  `-ldflags`, so nothing is stamped;
- **the environment**: `GOENV=off`, no `GOFLAGS` and no `GOEXPERIMENT`, and each architecture
  level at Go's default (`GOAMD64=v1`, `GOARM64=v8.0`), since a level changes the code the
  compiler emits.

A program whose imports reach the packs embed (`github.com/mschulkind-oss/yolo-jail/packs`) is
refused, naming the import: it would carry its own digest, and no build of it could match.

**What this costs:**

- **An upgrade can leave a loophole off until `yolo pack install`.** A launch never fetches
  ([BP-D5](#BP-D5)), so a release that moved a digest leaves that loophole inactive on every
  upgraded machine, and the launch names the command. Because nothing is stamped, a digest
  moves only when the program's own inputs do: its source, a package it imports, `vendor/`, or
  the pinned toolchain.
- **A refusal in CI spends the tag**, as a changelog refusal does. A tag is never moved, so the
  fix is the next version.
- **`just release` builds every official build**, and fetches the pinned toolchain the first
  time (the `linux/amd64` module zip is 71.7 MB, measured).
- **PyPI can be ahead of the files.** `publish.yml` runs beside `release.yml`, not after it, so
  for the minutes between PyPI publishing and goreleaser uploading, a pip-installed `yolo` of that
  version fails `yolo pack install` on a missing file, and succeeds when run again afterwards.
  Ordering PyPI after the release, through `workflow_run` as
  [`tap-install.yml`](../../.github/workflows/tap-install.yml) waits for it, would close the
  window; this plan leaves it open.

### 14.4 The plan, in order

Steps 1 to 6 land together, in the change that adds the first official `binaries` entry or
before it. Landed before it, they run over no binaries, so the pin tool's own tests carry a
fixture manifest. Step 7 waits on [OQ-BP7](#OQ-BP7).

1. **The pin tool**, a Go program under `tools/`, named by its builder. For every loophole
   manifest in the packs embed that declares `binaries`, it derives each binary's platforms
   ([BP-D7](#BP-D7)) and builds each one from `./cmd/<name>` with
   [§14.3](#143-what-the-release-builds-where-it-puts-it-and-how-the-digest-gets-in)'s recipe.
   The toolchain version is one constant in the tool, at or above `go.mod`'s `go` line, fetched
   through the module proxy and checked against the checksum database as `GOTOOLCHAIN` does, so
   moving it is a commit that re-pins every build. Three verbs:

   - `pin <version>` writes each build's `url` (in [BP-D8](#BP-D8)'s form, for `<version>`) and
     `sha256` into the manifest, editing those two string values in place so the file's comments
     survive;
   - `check <version>` builds and compares, writes nothing, and refuses on every disagreement it
     finds: a digest (naming the binary, the platform and both digests), a URL that does not name
     `<version>`, a platform outside [BP-D7](#BP-D7)'s set or missing from it, a program that
     links the packs embed, and a `binaries` key with no `cmd/<name>`;
   - `stage <version> <dir>` runs `check`, then writes each verified build into `<dir>` under its
     file name from [BP-D8](#BP-D8).

2. **The census test**, in the short suite, over the same manifests, rebuilding nothing: each
   binary's platforms are exactly [BP-D7](#BP-D7)'s set, read from `.goreleaser.yaml` rather
   than copied; each `url` has [BP-D8](#BP-D8)'s form; each program is at `cmd/<name>` and does
   not link the packs embed. It replaces `TestNoShippedManifestDeclaresABinaryYet`.
3. **The program's place in the tree.** Each `cmd/<name>` joins `shipSetExemptCmds` in
   [`shippedclients_test.go`](../../internal/entrypoint/shippedclients_test.go), beside
   `goprobe`, because it is downloaded and never mounted, and gets the root `.gitignore` line
   `rootbinaryignore_test.go` requires of every `cmd/` directory. The flake's source build and
   the bundle's cross-compile both compile every `cmd/` directory and copy only the shipped set
   into the prefix, so the program costs each of them one more compile and reaches no jail that
   way.
4. **Two recipes.** `just pin-pack-binaries <version>` runs `pin`, and `just release` runs
   `check <version>` after its changelog gate, refusing with "Nothing has been tagged." A
   release then goes: rename the changelog section, pin, commit both, `just release`.
5. **`release.yml`.** A goreleaser before-hook runs `stage <version>` into a git-ignored
   directory outside `dist/`, which goreleaser empties after the hooks (the reason the bundle
   stages under `bundle/`). goreleaser uploads what it staged as extra release files and lists
   them in `checksums.txt`. A refusal fails the hook, so nothing is published, and the Homebrew
   job, which needs goreleaser's success, does not run.
6. **`publish.yml`.** Its `release-notes` gate runs `check <version>` too, so PyPI never ships a
   `yolo` whose pins the release refused.
7. **What main pins between releases**, blocked on [OQ-BP7](#OQ-BP7):

   - **A** adds nothing.
   - **B** adds a digest-only `check` to `just check-ci`, in which a URL may name any earlier
     release, and a seeding step to `just install` and to the integration harness: build this
     machine's builds with the recipe and, where a digest equals the pin, admit the file to the
     cache through `internal/packbin`'s verified rename, saying which builds it seeded.
   - **C** adds B's steps, and a job on every push to main that uploads each new build to a
     standing prerelease under a name carrying its digest.

> [!NOTE]
> **Built 2026-09-30, steps 1 to 6** ([BP-D10](#BP-D10) to [BP-D14](#BP-D14)). Where each one is:
>
> 1. **The pin tool** is [`tools/pack-binaries`](../../tools/pack-binaries/main.go), and what it
>    shares with the census is [`internal/releasematrix`](../../internal/releasematrix/matrix.go):
>    the platform derivation, the release file's name and URL, and the import walk. The toolchain
>    is `Toolchain` in [`toolchain.go`](../../tools/pack-binaries/toolchain.go), `go1.26.7`, and
>    the recipe is [`recipe.go`](../../tools/pack-binaries/recipe.go). Its tests build a fixture
>    checkout with the go on the machine's PATH standing in for the toolchain, and a stub `go`
>    stands in for the download.
> 2. **The census** is `TestEveryOfficialBinaryIsOnTheReleaseMatrix`, in
>    [`releasematrix_test.go`](../../internal/loopholedecl/releasematrix_test.go), over every
>    loophole the embed ships.
> 3. **The program's place** is `TestAnOfficialPackBinaryIsDownloadedNeverMounted`, in
>    [`shippedclients_test.go`](../../internal/entrypoint/shippedclients_test.go): each official
>    binary must be in `shipSetExemptCmds` and in neither ship list. The flake and the bundle
>    compile every `cmd/` directory and copy only the shipped set, as step 3 says (checked
>    2026-09-30), so nothing else changed there.
> 4. **The recipes** are `pin-pack-binaries` and the check inside `release`, in the
>    [`Justfile`](../../Justfile).
> 5. **The release** stages into `bundle/pack-binaries` from a before-hook in
>    [`.goreleaser.yaml`](../../.goreleaser.yaml), which names that directory under both
>    `release.extra_files` and `checksum.extra_files`.
> 6. **PyPI's gate** is a step of [`publish.yml`](../../.github/workflows/publish.yml)'s
>    `release-notes` job.
>
> Each of 4 to 6 runs only when a release is cut, so
> [`callsites_test.go`](../../tools/pack-binaries/callsites_test.go) reads the files and fails
> when one of them stops running the tool. Not exercised: goreleaser itself, which is not
> installed in the dev jail, so the hook and the two `extra_files` globs were checked against
> goreleaser v2.18.2's source and not run; and a real toolchain download, which was run by hand
> on 2026-09-30 over a fixture checkout (download, pin, stage) and which no test runs.

**What done looks like**, checkable by a person:

- a release of a tree carrying one official binary shows one bare file per build beside the
  archives, each in `checksums.txt`, each matching the manifest the tag holds;
- a Homebrew-installed and a pip-installed `yolo` of that version fetch the same bytes on
  `yolo pack install`;
- editing the program after pinning makes `just release` refuse before tagging, naming the
  binary, the platform and both digests (the tool's half is pinned by
  `TestCheckRefusesAProgramEditedAfterThePin`);
- declaring a platform outside the matrix fails `just check-ci` (the census).

The first two need a release that carries an official binary, which no release has yet.

---

## Decision Ledger

Four rulings are settled in the body sections named below, and [OQ-BP5](#OQ-BP5) (as
[BP-D1](#BP-D1)) and [OQ-BP6](#OQ-BP6) on 2026-09-30, in **Open Questions** underneath, where
[OQ-BP7](#OQ-BP7) is still open. [BP-D1](#BP-D1) to [BP-D9](#BP-D9) are implementation decisions,
not rulings: BP-D2 to BP-D6 were made while building BP-D1 on 2026-09-30, BP-D7 to BP-D9
settle the release matrix the same day ([§14](#14-the-release-matrix)), and BP-D10 to BP-D14 were
made building its steps 1 to 6, also that day. The `Built` column was read from the tree on
2026-09-30.

| ID | Ruling / Decision | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| OQ-BP1 | Broker move and the pack-shipped binary capability ship **together**, as separate commits inside one sprint — overruling my "baked daemon first". Puts the release matrix on the critical path | 2026-08-15 | [§3.1](#31-what-is-actually-unresolved-here), [§9](#9-risks), [§10](#10-sequencing) | Partly: the broker move, the binary mechanism and the release matrix's steps 1 to 6 ([BP-D7](#BP-D7) to [BP-D14](#BP-D14)) are; step 7 waits on [OQ-BP7](#OQ-BP7) |
| OQ-BP2 | Every connection stays **raw** and yolo prepends its own connection preamble (`preamble: false` for a dumb pipe). yolo never parses a daemon's payload; the host-derived `jail=` lives in yolo's own connection record | 2026-08-15 | [§5.5](#55-ruled--every-connection-is-raw-yolo-prepends-a-connection-preamble) | ✅ |
| OQ-BP3 | Superseded by [OQ-BP4](#decision-ledger) — LP14 is a dependency of the **sprint**, not of this loophole | 2026-08-15 | [§11](#11-what-no-bundled-loopholes-additionally-requires) | Superseded; nothing to build |
| OQ-BP4 | **No inhabitants at sprint end** — retire the channel rather than shrink it. Done 2026-08-19: directory, embed and every reader deleted | 2026-08-15 | [§11](#11-what-no-bundled-loopholes-additionally-requires), [§13](#13-what-empty-the-channel-actually-required--measured-2026-08-19) | ✅ |
| <a id="BP-D1"></a>BP-D1 | *Implementation decision*, under [OQ-BP1](#decision-ledger). **A pack ships a binary as a declared download with a mandatory `sha256` (option B), and declares no build step.** yolo fetches it when the pack is installed, as [§3.1](#31-what-is-actually-unresolved-here) says, verifies the digest, and caches the file by digest under its state tree with the exec bit set, which also carries an executable for an embedded pack, whose `embed.FS` files read back `0444`. **Why:** the maintainer asked for per-arch selection and, ideally, dynamic download ([§3.1](#31-what-is-actually-unresolved-here)), which B is, and no pack needs a build. The digest is kept as integrity and reproducibility, the same bytes on every machine and the cache's key, not as a trust gate: [`OQ-TP9`](trust-paths.md#decision-ledger) deleted every origin gate, so the leaning's "origin-gated exactly as `InstallerURL` is" is dropped. If a pack later needs a built binary, it takes the source-build route [`forked-programs-as-packs.md`](forked-programs-as-packs.md) designs for programs, built in a throwaway capture jail, rather than a second build path here. Reversible: a build step can be added through that route without changing B ([OQ-BP5](#OQ-BP5)) | 2026-09-30 | [OQ-BP5](#OQ-BP5) | ✅ |
| <a id="BP-D2"></a>BP-D2 | *Implementation decision*, under [BP-D1](#BP-D1). **The download is declared in the LOOPHOLE manifest, as `binaries`, and named by two tokens: `{binary:<name>}` in `host_daemon.cmd` and `doctor_cmd`, `{jail_binary:<name>}` in `jail_daemon.cmd`.** Each token is refused in the other half and in every other field, must name a declared binary, and every declared binary must be named by one. **Why:** the argv that runs the program is in that manifest, so the leaf decoder can refuse a reference to nothing and a download nothing runs, and [§8](#8-non-goals--what-this-does-not-license) already scopes the download to "one verified file per platform per loophole". Two tokens for the module dir's reason (one token with two resolutions), and more so here, because host and jail differ in platform as well as in path. Reversible: a `pack.json` declaration can be added beside it if a binary is ever wanted outside a loophole | 2026-09-30 | [§3.1](#31-what-is-actually-unresolved-here) | ✅ |
| <a id="BP-D3"></a>BP-D3 | *Implementation decision*, under [BP-D1](#BP-D1). **Each reference takes the build for where it runs: this machine's `<goos>/<goarch>` for `{binary:}`, `linux/<goarch>` for `{jail_binary:}`. A build's platform key names both halves.** A binary with no build for where it runs makes the loophole unsupported on the **platform** axis, saying nothing can be installed, as [§3.1](#31-what-is-actually-unresolved-here)'s selection paragraph asks. **Why:** a compiled file runs on one architecture, and the jail image is Linux on this machine's architecture whatever the host OS is (`internal/cli/run/jailprefix.go` applies the same rule to yolo's own binaries) | 2026-09-30 | [§3.1](#31-what-is-actually-unresolved-here) | ✅ |
| <a id="BP-D4"></a>BP-D4 | *Implementation decision*, under [BP-D1](#BP-D1). **The cache is `~/.local/share/yolo-jail/pack-binaries/<sha256>/<name>`, each file mode `0555` and admitted only by renaming a verified download into place. A jail receives each build as ONE read-only file bind at `/etc/yolo-jail/loophole-binaries/<loophole>/<name>`.** A download must be https, redirects included (Go's client follows one to plain http unless told not to), carry no credentials in its URL, and stop at 512 MiB; a mismatch names both digests and caches nothing. **Why:** keyed by digest as BP-D1 says, with the declared name kept so a program reading its own argv[0] sees it; outside `cache/` and every jail mount because a host daemon's build runs from there with the user's authority (`paths.HostFloorDir`'s rule); a file bind, not the directory, so a jail sees only what its daemon runs; a root of its own because a file mount inside the module dir's read-only bind has no mountpoint to land on. The size bound exists because the digest can be checked only after the last byte | 2026-09-30 | [§3.1](#31-what-is-actually-unresolved-here), [§9](#9-risks) | ✅ |
| <a id="BP-D5"></a>BP-D5 | *Implementation decision*, under [BP-D1](#BP-D1). **`yolo pack install` is the only fetch** (and `yolo pack update`, which runs it). It walks every selected pack, embedded ones included, and fetches each build this machine needs whatever the loophole's switch says; a loophole whose `platforms` leaves this machine out needs none, and install says so. A launch never fetches: a build not in the cache keeps the loophole inactive, and the launch's inert report, `yolo loopholes list` and `yolo check` name `yolo pack install`. A reinstall re-hashes the cached copy and fetches again when it no longer matches, never using it. **Why:** [§3.1](#31-what-is-actually-unresolved-here) ("at `pack install`, never at launch") and [§9](#9-risks)'s offline argument; an embedded pack is the case BP-D1 exists for and has no fetch step of its own, so install has to cover it. The cost, named: a pack newly selected, or a fetched pack a launch fetched for the first time, runs its loophole only after `yolo pack install`, where every other part of such a pack arrives with the launch. Reversible | 2026-09-30 | [§3.1](#31-what-is-actually-unresolved-here), [§9](#9-risks) | ✅ |
| <a id="BP-D6"></a>BP-D6 | *Implementation decision*, under [BP-D1](#BP-D1). **Where a build cannot run, the part that would run it says so instead:** the macos-user guest declines a jail daemon whose argv names a jail binary's container path, as it declines `{jail_loophole_dir}` ([`OQ-DP8`](declaration-parity.md#OQ-DP8) leaves the argv as declared), while a host binary runs there; a `doctor_cmd` whose build is missing reports "not run" with the reason; inside a jail, a jail build counts once the outer launch mounted it, a nested launch binds that mounted copy on when its own cache lacks the build, and a host build is the host's business, as `requires` is answered there, in what the jail reports; a nested launch, which runs its host daemons in the jail from the jail's own cache, starts none whose build that cache lacks and says so, as the doctor does. Apple Container needs nothing new: every pack loophole is inert there already. **Why:** each is the existing rule for the thing next to it, applied to the new path, so no surface runs an argv naming a file it does not have | 2026-09-30 | [§3.1](#31-what-is-actually-unresolved-here) | ✅ |
| OQ-BP6 | **Yes: a fetched pack may ship a host-side daemon binary, honored and disclosed like its `host_daemon.cmd`, with no approval and no origin refusal.** Answered by [`OQ-TP9`](trust-paths.md#decision-ledger) (2026-09-04), which deleted the fetched-pack approval prompt and every origin refusal and kept the launch's disclosure. The maintainer's words in that review: *"they're all just as weak. anything can be an installer. we're ultimately extending trust somewhere."* A binary is no sharper than the arbitrary host argv `host_daemon.cmd` already names, and refusing it would be the only origin refusal in the tree | 2026-09-30 | [OQ-BP6](#OQ-BP6) | ✅ Adds no code |
| <a id="BP-D7"></a>BP-D7 | *Implementation decision*, under [OQ-BP1](#decision-ledger). **An official binary has a build for exactly the platforms it runs on among those the release ships `yolo` to:** a host reference gets one for each `<goos>/<goarch>` of goreleaser's `yolo` build that its loophole's `platforms` admits (`linux` and `darwin`, each on `amd64` and `arm64`, today), a jail reference gets `linux/<arch>` for each of those architectures, and nothing gets a `darwin` jail build. The program is Go from this module, cgo off, at `cmd/<name>` for the `binaries` key `<name>`. **Why:** a host build runs where the host `yolo` runs, and those four platforms are the only ones any channel ships `yolo` to (the archives, the wheels, the source-built formula), so one more would be fetched by nobody and one fewer would leave an official loophole unsupported where yolo itself works. The jail is Linux on the machine's architecture ([BP-D3](#BP-D3)), the pair the bundle already builds, and the macos-user guest declines the jail token ([BP-D6](#BP-D6)). *Exactly* makes [§9](#9-risks)'s failure mode unrepresentable, and reading the set from `.goreleaser.yaml` makes a platform the release drops fail the census instead of leaving a stale build. Pure Go because the release cross-compiles every platform on one Linux runner, where cgo for `darwin` cannot build; a program that needs more takes [BP-D1](#BP-D1)'s source-build route or an upstream's own release. `cmd/<name>` because every binary the release builds is named for its `cmd/` directory, and `build-go.sh` already builds a named subset. Reversible | 2026-09-30 | [§14.3](#143-what-the-release-builds-where-it-puts-it-and-how-the-digest-gets-in) | ✅ `releasematrix.Platforms`, read from `.goreleaser.yaml` by the census and the pin tool |
| <a id="BP-D8"></a>BP-D8 | *Implementation decision*, under [OQ-BP1](#decision-ledger). **Each official build is published on the tag's own GitHub release as a bare executable named `<name>_<version>_<goos>_<goarch>`, at `https://github.com/mschulkind-oss/yolo-jail/releases/download/v<version>/<file>`, and listed in `checksums.txt`. Every release uploads every official build, moved or not.** **Why:** the tag's release is the one place the release already publishes plain files over https, which is all `yolo pack install` reads; GHCR holds OCI images and Cachix holds nix store paths. Bare, because `internal/packbin` caches the bytes it downloads as the program and has no unpack step. The name follows the archives' `yolo-jail_<version>_<os>_<arch>`. Uploading an unchanged build again keeps each version self-contained, so a release deleted later breaks only its own installs. The asset URL redirects over https (measured, [§14.1](#141-what-the-release-already-fixes)), which [BP-D4](#BP-D4)'s redirect rule requires, and an asset re-uploaded under the same URL is refused by its digest (P4, [§3.1](#31-what-is-actually-unresolved-here)). Reversible | 2026-09-30 | [§14.3](#143-what-the-release-builds-where-it-puts-it-and-how-the-digest-gets-in) | ✅ `releasematrix.AssetURL`; the release uploads `bundle/pack-binaries` ([BP-D14](#BP-D14)) |
| <a id="BP-D9"></a>BP-D9 | *Implementation decision*, under [OQ-BP1](#decision-ledger). **Each build's `url` and `sha256` are committed in the tagged tree, written before the tag by the pin tool, and checked by rebuilding at `just release`, in the release's goreleaser run before it uploads, and in `publish.yml`'s gate. Every build uses one recipe: Go's own distribution of one pinned version, cgo off, `-trimpath -buildvcs=false -mod=vendor`, no `-ldflags`, `GOENV=off` and Go's default architecture levels. A program that links the packs embed is refused.** **Why:** the Homebrew formula builds `yolo` from the tag's source tarball, so the manifest a Homebrew `yolo` embeds is the tag's, and goreleaser refuses a dirty tree, so digests written at release time would give the archives and Homebrew different manifests. The digest has to be in the tagged tree, so the bytes have to be known before the tag, which a reproducible build of that tree gives ([§14.2](#142-what-was-measured)). Each recipe item removes one cause of drift: the VCS stamp and a commit stamp embed the commit that holds the digest, so no build could match; a version stamp would move every digest on every release, and so turn every upgraded machine's loophole off until `yolo pack install`; the toolchain's version is in the bytes, and nothing ties the maintainer's Go to the runner's; an architecture level changes the code emitted. A program that links the packs embed carries its own digest. The three gates are the changelog gate's shape: the same check refuses locally before the tag and again in CI. Rejected: pinning the build an earlier release published, which ships every change to the program one release late, and needs a release with nothing to pin before the first. Reversible | 2026-09-30 | [§14.3](#143-what-the-release-builds-where-it-puts-it-and-how-the-digest-gets-in) | ✅ `tools/pack-binaries`, at the three gates ([BP-D10](#BP-D10) to [BP-D14](#BP-D14)) |
| <a id="BP-D10"></a>BP-D10 | *Implementation decision*, under [BP-D9](#BP-D9). **The pin tool is `tools/pack-binaries`, a `package main` with the verbs `pin`, `check` and `stage`, and what it shares with the census is `internal/releasematrix`**: the platforms each binary is built for, the release file's name and URL, the manifests a packs tree carries, and the static checks. The census reads the packs embed, the tool the checkout's `packs/`, since it writes what it read, and a test holds the two to the same manifests. With no official binary declared, each verb says so and succeeds without fetching the toolchain. **Why:** under `tools/` a program reaches no jail's prefix and no image, as `build-wheels` does not; the census and the tool must not disagree about the matrix, and a test cannot import a `main` package, so the shared half is a library. Reversible | 2026-09-30 | [§14.4](#144-the-plan-in-order) | ✅ |
| <a id="BP-D11"></a>BP-D11 | *Implementation decision*, under [BP-D9](#BP-D9). **`pin` writes values, never keys.** It edits each build's `url` and `sha256` strings in place, found by `json5.Locate`, which reads with the decoder's own grammar and refuses a key written twice along the path, and reads the file back through the strict decoder before it is renamed into place. A build set that is not [BP-D7](#BP-D7)'s is refused before anything is built, naming each platform to add or drop, and the author writes the entry, with any url and a digest of zeros, for `pin` to fill. `check` and `stage` build only the builds BP-D7 wants: a declared build it does not want is the census's refusal, and the program may not build there at all. **Why:** [§14.4](#144-the-plan-in-order) asks for an edit of two string values so the file's comments survive; adding a key means choosing where and how to format it in a file a person wrote, and the entry is the author's declaration of what ships. Reversible | 2026-09-30 | [§14.4](#144-the-plan-in-order) | ✅ |
| <a id="BP-D12"></a>BP-D12 | *Implementation decision*, under [BP-D9](#BP-D9). **The toolchain is `go1.26.7`, fetched by `go mod download -json golang.org/toolchain@v0.0.1-go1.26.7.<goos>-<goarch>` run by the go on PATH from an empty directory, with `GOENV=off`, `GOTOOLCHAIN=local`, `GOWORK=off`, and `GOSUMDB=off` overridden**, then made runnable in the order the go command's own toolchain switch uses, and refused unless its `go version` names that version. **Why:** `go1.26.7` is the version [§14.2](#142-what-was-measured) measured reproducible. Setting `GOTOOLCHAIN=go1.26.7` would not do: the go command does not switch when it already is that version, so it would build with the PATH's go, which [BP-D9](#BP-D9) rules out. `GOENV=off` because an empty environment variable does not override the go env file, so it is the only way to know no `GONOSUMDB`, `GOPRIVATE`, `GOINSECURE` or `GOFLAGS` there skips the checksum database. `GOPROXY`, and a `GOSUMDB` naming another database, are honored. Reversible | 2026-09-30 | [§14.4](#144-the-plan-in-order) | ✅ |
| <a id="BP-D13"></a>BP-D13 | *Implementation decision*, under [BP-D9](#BP-D9). **The recipe's environment keeps every variable except the `GO*` and `CGO_*` ones, keeps of those only `GOCACHE`, `GOMODCACHE`, `GOPATH` and `GOTMPDIR`, and sets `GOENV=off`, `GOTOOLCHAIN=local`, `GOWORK=off`, `CGO_ENABLED=0`, the platform, and `GOAMD64=v1` or `GOARM64=v8.0`.** A build is written under the binary's own name. **Why:** a list of what to keep also covers a variable Go adds later, where a list of what to drop would miss it; `GOFIPS140`, which selects the cryptographic module a program links, is one the design did not name. The four kept say only where a cache or scratch directory is, which [§14.2](#142-what-was-measured) measured does not move the bytes. The explicit levels are the defaults (measured 2026-09-30: the same digest with and without them, and the same from the toolchain module and the jail's `go1.26.7`); the file name is the one the cache keeps. Reversible | 2026-09-30 | [§14.3](#143-what-the-release-builds-where-it-puts-it-and-how-the-digest-gets-in) | ✅ |
| <a id="BP-D14"></a>BP-D14 | *Implementation decision*, under [BP-D8](#BP-D8) and [BP-D9](#BP-D9). **`stage` writes into `bundle/pack-binaries`, refuses a directory that is not empty, and makes it even when there is nothing to stage; goreleaser names it under both `release.extra_files` and `checksum.extra_files`. The program check is a walk of this module's imports with `go/build`, under each platform's build constraints, test files excluded, compiling nothing.** **Why:** `bundle/` is already git-ignored and outside `dist/`; everything in the directory is uploaded, so a leftover would be published as if this run had built it. goreleaser v2.18.2 computes `checksums.txt` before it uploads and reads only its own `extra_files` there, so the release's list alone would leave the files out of it, and a glob with a wildcard that matches nothing is no error (read in its source and `fileglob` v1.4.1's). The walk lets the short suite ask the embed question of every platform; `TestEmbedChainAgreesWithTheMeasuredTree` holds it to [§14.2](#142-what-was-measured)'s measurement of which programs link the embed. Reversible | 2026-09-30 | [§14.4](#144-the-plan-in-order) | ✅ |

> [!WARNING]
> **[OQ-BP3](#decision-ledger) is superseded, not wrong, and it comes back if the goal shrinks.** The *broker* needs
> nothing from [OQ-LP14](../reference/loophole-system.md#oq-lp14); it was **"no bundled loopholes"** that did, through `audio` ([§11](#11-what-no-bundled-loopholes-additionally-requires)). Any future
> sprint goal smaller than emptying a channel gets the original answer — proceed beside LP14 — and
> should not inherit the coupling from this row.

> [!WARNING]
> **Do not read [OQ-BP1](#decision-ledger)'s ruling as "binaries are done".** It committed the *capability* to the same
> sprint as the move; what actually shipped on 2026-08-19 was the move, on baked daemons, which
> [§3.1](#31-what-is-actually-unresolved-here)'s design explicitly permits for an official pack. The mechanism followed on 2026-09-30
> ([BP-D1](#BP-D1) to [BP-D6](#BP-D6)). The release matrix that [OQ-BP1](#decision-ledger) put on the
> critical path is decided ([BP-D7](#BP-D7) to [BP-D9](#BP-D9)) and built but for its last step
> ([BP-D10](#BP-D10) to [BP-D14](#BP-D14)), and no official pack ships a binary yet. A `binaries`
> entry declaring builds the release does not produce is this ruling's named failure mode
> ([§9](#9-risks)): `yolo pack install` then fails on the missing file instead of the loophole
> reading as unsupported. `TestEveryOfficialBinaryIsOnTheReleaseMatrix` refuses such an entry in the
> short suite, and the pin tool refuses to pin it.

---

## Open Questions

All three belong to the **pack-shipped binary capability** ([§3.1](#31-what-is-actually-unresolved-here)), not to the broker: the broker shipped
on a baked daemon, which an official pack may do. The first two are settled. [OQ-BP7](#OQ-BP7), on
its release matrix ([§14](#14-the-release-matrix)), is open, and blocks one step of that plan and
nothing in the tree.

1. ✅ <a id="OQ-BP5"></a>**[OQ-BP5](#OQ-BP5) — download-with-digest only, or also a declared build step?**

   The review asks for both as candidates ([§3.1](#31-what-is-actually-unresolved-here)). They are not symmetric: a download can be pinned by `sha256` and therefore satisfies **P4** (a pinned pack pins everything that runs); a build step generally cannot, because builds are not bit-reproducible, so what runs is decided at install time by whatever toolchain the machine happens to have. A build step is also the same risk class as `packdecl.Install.InstallerURL`, which the schema calls *"the sharpest thing a manifest can name"* and which a fetched pack could not introduce until [`OQ-TP9`](trust-paths.md#decision-ledger) deleted the origin rule on 2026-09-04.

   <!-- vantage: oq id=OQ-BP5 -->

   _Leaning:_ **Download-with-digest now; no build step until something needs one B cannot serve.** If a build step is added later, it should be jail-side only and origin-gated exactly as `InstallerURL` is — and it should be honest that a built artifact is unpinned, rather than inheriting the word "pinned" from the commit that produced its recipe.

   ⚠ **The leaning's precedent no longer exists** (re-checked 2026-09-24): `InstallerURL` is not origin-gated any more — `packload.Pack.HonoredInstalls` refuses nothing since [`OQ-TP9`](trust-paths.md#decision-ledger) — so "origin-gated exactly as `InstallerURL` is" now means "not gated". A build step that should be origin-gated would need a refusal source of its own, and that code's own comment says any new one needs its own design ruling. The download half of the leaning is unaffected. Related: [`forked-programs-as-packs.md`](forked-programs-as-packs.md) designs source builds for `kind: "program"` — built in a throwaway capture jail, never on the host — which is the same "declared build step" question for a different artifact.

   **Answer:**
   > Decided as an implementation choice ([BP-D1](#BP-D1)), reversible: download-with-digest
   > only, and no declared build step. A pack that later needs a built binary takes the
   > source-build route [`forked-programs-as-packs.md`](forked-programs-as-packs.md) designs,
   > built in a throwaway capture jail, rather than a second build path. The origin gate the
   > leaning named is dropped, because [`OQ-TP9`](trust-paths.md#decision-ledger) deleted it.

2. ✅ <a id="OQ-BP6"></a>**[OQ-BP6](#OQ-BP6) — may a *fetched* pack ship a host-side daemon binary?**

   [§3.1](#31-what-is-actually-unresolved-here)'s two-gate split says a jail-side binary is roughly as sharp as what a pack can already do, while a host-side one is a host-execution grant. This asks whether the second is available to a fetched pack at all, or whether — like `InstallerURL` and `host_files` — it is refused by origin regardless of what the user would approve. Not needed for the broker, which is official; needed before anyone else ships one.

   <!-- vantage: oq id=OQ-BP6 -->

   _Leaning:_ **Allow it, gated by the existing host-execution approval rather than refused by origin.** A fetched pack can already declare a `host_daemon.cmd` naming an arbitrary host argv — verified 2026-09-02: the loophole claim producer (`loopholeClaims`, `internal/packload/loopholesource.go`) enumerates `host_daemon.cmd + doctor_cmd` as an **approvable** claim (*"host EXECUTION"*), distinct from the origin-refused fields (`reads-host`, `mount`, `InstallerURL`) — so refusing a *binary* while permitting an arbitrary *command* would repeat the halfway-measure shape [OQ-LP14](../reference/loophole-system.md#oq-lp14) already suffers from: blocking the declarative form of a capability while permitting the imperative one. But this genuinely is a widening and should be answered deliberately.

   ⚠ **Two facts under this leaning changed on 2026-09-04, two days after it was verified** ([`OQ-TP9`](trust-paths.md#decision-ledger)): there is no host-execution approval to gate on any more, and there are no origin-refused fields — `reads-host`, `mount` and `InstallerURL` are all honored for a fetched pack. What survives is disclosure: the claim is still enumerated, `yolo pack footprint` shows it, and the launch prints it before the daemon spawns. So a fetched pack's `host_daemon.cmd` runs on the host today with no approval, disclosed, and the halfway-measure argument is *stronger* — refusing the binary would be the only origin refusal in the tree. What is now open is whether that disclosure is enough for a shipped host binary, or whether it is the case that earns a new refusal source. **Not answered by [`OQ-TP9`](trust-paths.md#decision-ledger)**, which ruled on selection and approval rather than on shipped binaries, so this stays open.

   **Answer:**
   > Answered by [`OQ-TP9`](trust-paths.md#decision-ledger) (2026-09-04): yes, honored and
   > disclosed like the pack's `host_daemon.cmd`, with no approval and no origin refusal. TP9
   > deleted the fetched-pack approval prompt and every origin refusal on the maintainer's review
   > (*"they're all just as weak. anything can be an installer. we're ultimately extending trust
   > somewhere."*), and kept the launch's disclosure, so the host-execution lines print before the
   > daemon spawns and `yolo pack footprint` shows the claim. The 2026-09-29 ruling
   > [OQ-HP5](host-tool-provisioning.md#OQ-HP5) says the same of installs (*"You've acknowledged
   > this already. We don't have to ask again"*). No new refusal source is added for a binary.

3. 💬 <a id="OQ-BP7"></a>**[OQ-BP7](#OQ-BP7) — between two releases, does main pin the last release's build of an official binary, or its own?**

   Say `packs/claude`'s in-jail terminator becomes a downloaded program, `cmd/oauth-terminator`,
   pinned by v0.12.0. On main you change how it talks to the host broker, both halves in one
   commit, run `just install`, and start a jail. Everything else in that jail comes from your tree:
   the bundle's binaries are cross-compiled from it, and a launch refuses a host `yolo` built from
   an older tree than the flake it mounts ([`srcskew.go`](../../internal/version/srcskew.go)). The
   terminator would not come from your tree. If pins move only when a release is cut, the manifest
   your `yolo` embeds still names v0.12.0's build, and the jail runs the old terminator against the
   new broker. This decides what a build from source promises about an official binary, and it
   blocks step 7 of [§14.4](#144-the-plan-in-order) and nothing else.

   - **A — The last release's build.** Pins move only at `just release`, and nothing is added
     between releases. Every build of main installs, but a from-source jail runs the last released
     program until the next release, and a change to it can be tried only through a pack configured
     by path. It allows, for these programs alone, the skew `SourceSkew` refuses for every other
     binary yolo puts in a jail.
   - **B — The tree's own build, checked on every change.** `just check-ci` rebuilds every official
     build and refuses a digest the tree no longer reproduces, naming the pin command. `just install`
     builds the same bytes and seeds the cache with them, so a from-source jail runs the tree's
     program with no download. The URLs still name the last release until `just release`, so any
     other build of main fails `yolo pack install` for a changed program, with the integrity error
     naming both digests. Cost: a pin commit for every change that reaches a program's imports (its
     source, an internal package it uses, a vendor bump, the toolchain constant), and `just check-ci`
     cross-compiling every official build.
   - **C — The tree's own build, published on every push.** B, plus a CI job that uploads each new
     build to a standing prerelease under a name carrying its digest, so every build of main
     installs. Cost: a second publication channel, carrying programs no release vouched for, written
     by CI on every push to main.

   <!-- vantage: oq id=OQ-BP7 leaning="B: main pins its own build, kept current by a check-ci rebuild and seeded into the cache by just install, so a from-source jail runs the tree's program as it runs every other binary yolo puts in a jail, and nothing is published outside a release." -->

   _Leaning:_ **B.** It keeps the rule that a build from source runs the tree, which `SourceSkew`
   enforces for every other binary yolo puts in a jail, and it keeps publication at the release. A
   lets the one kind of program that shares a contract with the tree's host `yolo` lag it with
   nothing saying so; C publishes code no release vouched for.

   **Answer:**
   > _(empty — fill in when decided)_
