# Outstanding work

**What this is.** Everything still to do, and nothing else. Restructured 2026-08-12 around the
two threads the maintainer named; shipped items have moved out to
[`shipped-2026-08-pack-batch.md`](shipped-2026-08-pack-batch.md).

**Read this as today's forward plan.** Every claim below was checked against the code on the date
it carries, not against a status marker — five stale "still open" markers turned up in these docs
over the last fortnight, so verify-against-code is the house rule.

---

## The three active threads

| | Thread | First move | Blocked on |
|---|---|---|---|
| **C** | [Open PRs + issues on the public repo](#thread-c--the-open-prs-and-issues-on-the-public-repo) | **fix issue #33** (`ca.key` in every jail — open, no PR), then land #37 | nothing; #32 has a question awaiting your answer |
| **A** | [Claude auth as swappable packs](#thread-a--claude-auth-as-two-swappable-packs) | move `shared_credentials` off the base `claude` pack | nothing |
| **B** | [macos-user + non-container nix](#thread-b--macos-user-and-non-container-nix) | fix macos-user rendering zero pack surfaces | a Mac to verify; N3 is your call |

Everything else is [below](#everything-else-still-open).

---

# Thread C — the open PRs and issues on the public repo

All on [mschulkind-oss/yolo-jail](https://github.com/mschulkind-oss/yolo-jail); `.env`'s
`GH_TOKEN` reads and pushes `origin`, so no extra credentials are needed. Reviewed 2026-08-12;
every premise re-verified against local code rather than taken from a PR body.

**Two of these are from outside contributors, both awaiting a first response.** Each filed an
issue *and* a PR fixing it. Neither has any review or comment on it yet, and **all three PRs are
`MERGEABLE`** (no conflicts). One carries a direct question for you (C-3).

| PR | Title | Author | Size | CI | Verdict |
|---|---|---|---|---|---|
| [#37](https://github.com/mschulkind-oss/yolo-jail/pull/37) | image staleness: compare against most-recently-loaded path (fixes [#35](https://github.com/mschulkind-oss/yolo-jail/issues/35)) | Georgi Popov, **external** | +88/−4 | none reported | **land first** — premise verified locally |
| [#34](https://github.com/mschulkind-oss/yolo-jail/pull/34) | weekly `flake.lock` bump | `github-actions` bot | +3/−3 | none reported | routine; inert until an image rebuild |
| [#32](https://github.com/mschulkind-oss/yolo-jail/pull/32) | macOS+podman broker transport (fixes [#31](https://github.com/mschulkind-oss/yolo-jail/issues/31)) | Dong Liu, **external** | +1064/−13 | **green** (integration + secrets-scan pass; check-macos skipped) | land, then **promote** — and it has a **question for you** |

| Issue | Title | Author | State |
|---|---|---|---|
| [#35](https://github.com/mschulkind-oss/yolo-jail/issues/35) | Stale `:latest` reused after reverting config | Georgi Popov | fixed by #37 |
| [#33](https://github.com/mschulkind-oss/yolo-jail/issues/33) | **`ca.key` is mounted into every jail** | Dong Liu | 🔴 **open, no PR** — see C-4 |
| [#31](https://github.com/mschulkind-oss/yolo-jail/issues/31) | Broker relay socket unreachable on macOS+podman | Dong Liu | fixed by #32 |

## C-1. #37 — a silent stale-image bug in the tool you verify with 🔴

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

## C-2. #34 — real, correctly paced, and self-suppressing

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
actually review. Recorded as **D3** below.

## C-3. #32 — land it, then promote it; do NOT close it as subsumed

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

## C-4. Issue #33 — `ca.key` in every jail 🔴 open, no PR, and it is ours

**The most serious item in this thread, and the only one with nobody working on it.** Dong Liu
filed it separately while writing #32, whose body explains why it matters there: the broker's whole
state dir — **including `ca.key`** — is mounted `:ro` into every jail, so a malicious jail could
sign a `yolo-broker-relay` cert and MITM a sibling. That is why #32 pins an exact host-only-key
cert instead of trusting the broker CA: **verifying against that CA is worthless.**

**The fix now lives in [`loophole-transport.md`](../design/loophole-transport.md) §5**, alongside
the transport design it is inseparable from — #32 pins its own cert *because* the broker CA cannot
be trusted, so a reader who merges #32 without this may reasonably conclude the CA problem was
handled. It should land **before or with** #32, not after.

This is the same defect `ROADMAP.md` §4d records from the internal audit — independently found and
now **publicly filed**. Combined with `NODE_EXTRA_CA_CERTS` trusting that CA inside the jail, a jail
process can mint a trusted leaf for *any* host.

### Why it was not acted on, and what else was not 🔴

Because **the audit produced findings, not work items.** `ROADMAP.md` §4d recorded four verified
defects around 2026-08-02 and **none was carried into this queue**, so every planning pass since
scoped from a list that did not contain them. The pack batch that followed was scoped from here.

**Re-checked 2026-08-12 — all four are still unfixed.** Full table and evidence in
[`loophole-transport.md`](../design/loophole-transport.md) §5.2; summarized:

| §4d defect | State | Now tracked as |
|---|---|---|
| `ca.key` readable in-jail | 🔴 open | **C-4** (this row) |
| Claude creds symlink dangles on macos-user | 🔴 open | **B-1**, and it blocks Thread A's Teams mode on macOS |
| Config-approval snapshot is agent-writable | 🔴 open | **D1** below — re-measured: `.yolo/config-snapshot.json` is mode `664` and writable in-jail |
| Two shipped docs contradict the code | 🟡 partly | **D2** below — the refresher contradiction survives; the `--host-creds-file` half was fixed |

**The process lesson matters more than the four items:** an audit whose output lives only in a
narrative doc is invisible to planning. Findings have to become queue rows the day they are found,
or they age quietly until an outside contributor files one as a public issue — which is exactly
what happened.

**Re-measured from inside a live jail, 2026-08-12** — not inferred from the audit:

```
$ ls -l /var/lib/yolo-jail/loopholes/claude-oauth-broker/
-rw-r--r--  ca.crt      -rw-------  ca.key      -rw-r--r--  ca.srl
-rw-r--r--  leaf.cnf    -rw-r--r--  refresh.lock
-rw-r--r--  server.crt  -rw-------  server.key
$ head -c 28 …/ca.key    →  -----BEGIN PRIVATE KEY-----      (3268 bytes, readable)
```

**The `0600` mode is not a mitigation here.** A yolo jail runs its agent as UID 0 (Claude YOLO is
`--dangerously-skip-permissions` plus `IS_SANDBOX=1`, which exists precisely to bypass the UID-0
refusal), so owner-only permissions are no barrier — as the read above demonstrates.

**The fix is narrow and known:** only `server.crt` / `server.key` are needed in-jail; `ca.key` is
used solely host-side by `cert.go`. Mount the two server files rather than the whole state dir.

**This should probably jump ahead of #37.** It is a live privilege boundary failure that an
outside contributor has now published, the remedy is a mount-scope change rather than a redesign,
and every day it stays open is a day the published issue has no response.

---

# Thread A — Claude auth as two swappable packs

**The ask:** two packs — one Teams, one Bedrock — cleanly shareable, toggled by swapping which is
selected. Manual swapping is fine for now.

**Design docs:** [`agent-auth-modes.md`](../design/agent-auth-modes.md) (the mode model),
[`pack-config-collaboration.md`](../design/pack-config-collaboration.md) (why `config-overlay`,
not `config`), [`boundary-broker.md`](../design/boundary-broker.md) §10 (prior art; declines this
problem).

## A0. The verdict: expressible today, with one move and one hazard

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

## A1. The one structural move 🔴

**`shared_credentials` and the machine-scope `.claude-shared-credentials` state must move off the
`claude` pack onto `claude-teams`.** Today the base pack owns both, so "select `claude` alone"
implies subscription auth. For modes to be swappable, the base pack must be auth-neutral.

Consequence to accept deliberately: **selecting `claude` with no auth pack yields no credential
sharing.** That is correct — it makes the mode an explicit choice — but it changes the behavior of
a shipped pack, so it wants the render fingerprint gate run before and after.

## A2. The hazard: nothing prevents selecting both 🔴

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

## A3. Host support — SOLVED, and it no longer needs N3 ✅

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

## A4. Composition — packs cannot depend on other packs 🔴

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

## A5. Order of work

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

## B-0. macos-user renders ZERO pack surfaces 🔴 — re-verified 2026-08-12

**Still live.** `internal/cli/run/run.go:60-76`: the `rt == "macos-user"` branch returns at the
`o.MacosUserRun(...)` call, which is **before `stagePacks`**. So `YOLO_PACK_ROOT` is never set, and
`RunDarwinBootstrap`'s `LoadJailPacks` / `ConfigurePackSurfaces` / `RunPackHooks` loops all iterate
an empty list. A backend that looks provisioned and configures nothing — no error, no warning.

> **`macos-user-nix-and-features.md:174` still claims pack selection works there. It does not.**
> Fix the doc when you fix the bug.

**Do this first.** It is the cheapest real test of whether `render.Target` can express a
non-container backend at all — and if it cannot express macos-user, it will not express `guest`.
This is plan item 1.4 in the handoff.

## B-1. The other confirmed macos-user defects

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

## B-2. N3 — the decision that is yours

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

## B-3. P7 — the `guest` notch

Not built; host/Mac-gated rather than design-blocked. `render.KindGuest` exists with no behavior;
`render.UndecidedModes(reason)` is its fail-closed mode census. Needs B-0 first — same abstraction.

## B-4. Also Mac-gated, collected

| Item | What is needed |
|---|---|
| **D4 Cachix** | one real download proof; everything else done 2026-07-22 |
| **E8's nightly** | the first nightly AFTER the next release (`publish.yml` is tag-triggered) |
| **`cache_relocations`** | one real cross-filesystem move as an acceptance step |

---

# Packs implementation — what is actually left

**Substantially done.** The ten-item batch plus S1–S3/C1–C3 shipped; the reform (14 kinds,
`yolo pack footprint`, host render, config collaboration) is complete. What remains is one live
gap, one audit, and a tail of small items.

| # | Item | Kind | Blocked on |
|---|---|---|---|
| **S5** 🔴 | **A jail resolves a skill-name collision SILENTLY** — the notch S1 does not reach | live gap | nothing |
| **S4** | **UNAUDITED:** can a pack's `into` deliver to an agent the user never selected? | audit | nothing |
| **E1+E2** | `host_files` modes 4→3, `readonly` as a real `:ro` mount | behavior change on a shipped key | a design pass (E2 first) |
| **E3** | Capture on terminate (the `yolo config capture` half shipped) | small | nothing |
| **E4** | Comment preservation on `json`/`toml` surfaces | small, decisions made | nothing |
| **E5** | `managed`/`defaults` array-append pinning | small | **do not build speculatively** |
| **V2** | `apply --host` is not whole-home idempotent until apply 3 | pre-existing, in `config` | nothing |
| **V3** | Pack-set-wide archives land under `archive/skills/` even for `files` | cosmetic | nothing |

## S5 — the detail

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

## S4 — the detail

Two readings of the code point the same way; neither has been probed.

1. `run.packSkillTargets` emits a target per `skills` contribution using `Dest: c.Into`. Nothing
   visible checks that the named agent is one the user selected.
2. `agents.PrepareSkills` copies EVERY pack's `skills/` into EVERY staging dir — `packSkillDirs` is
   a flat list with no per-destination filter. **S3 sharpened this:** that loop is now the last
   layer, so it alone decides a staged dir's contents.

**Why it matters beyond tidiness.** `packs` is the user's selection gate, USER-SCOPE ONLY precisely
so a repo-committed config cannot decide what enters the environment. If `into` can name any agent's
directory regardless of selection, the gate is on *loading* while the effect is on *delivery* — the
same shape as the mise-trust finding, where the enforcement point and the real boundary were
different layers.

**Three probes, none run:** (1) select only `claude`, add a pack with `into: ".codex/skills"` —
does the dir get created? (2) two packs, distinct skills, two destinations — does each reach both?
(3) do jail and host disagree? **Probe 3 is partly answered by S5: yes, on collisions.**

---

# Everything else still open

| # | Item | Kind | Blocked on |
|---|---|---|---|
| **B1** | Audit-only log of every jail↔host boundary crossing ([boundary-broker.md](../design/boundary-broker.md) step 1) | small, additive | nothing |
| **B1b** | **Credential-injecting proxy for git** — host injects after egress, jail holds nothing, no human. **Possibly an ADOPTION**: unYOLO's MIT `gh-broker` is this row's entire scope ([§10](../design/boundary-broker.md)) | new capability | nothing |
| **B2** | Approval-gated host credentials — one allowlisted verb, synchronous. Design validated by convergence with unYOLO; take its grant model, content-addressed plans, and `expected_revision` rather than re-deriving | new capability | N3/OQ-1 |
| **D1** 🔴 | **Config-approval snapshot is agent-writable** — `.yolo/config-snapshot.json` is mode `664` and writable in-jail (re-measured 2026-08-12). An agent that edits `yolo-jail.jsonc` **and** matches the snapshot makes the launch-time diff prompt vanish — the exact bypass [config-safety.md](../design/config-safety.md) exists to prevent, and it is undiscussed there. From `ROADMAP.md` §4d; never queued until now. **Has an open question — see OQ-D1** | security | **your call** (OQ-D1) |
| **D2** | **Two shipped docs contradict the code** — `USER_GUIDE.md:182` and `bundled_loopholes/claude-oauth-broker/README.md:59` both say *"no background timer / no proactive refresh"*, but `oauthbrokercmd.go:88` starts `RunBackgroundRefresher` by default — and that refresher **is** the architectural fix for all three logout paths. (The `--host-creds-file` half has since been fixed.) From `ROADMAP.md` §4d. **No OQ: the code is right and the docs are wrong**, so this is a doc edit, not a decision | doc defect | nothing |
| **D3** | **`flake.lock` bumps are unreviewable** — three opaque lines that could be a CVE fix or a README typo (C-2). Have the update workflow post `nix store diff-closures` between the old and new image derivation into the PR body, turning the bump into a readable package-version delta | small, additive | nothing |
| ✅ **B4** | ~~Correct [agent-credentials.md](../design/agent-credentials.md) §3~~ **DONE** — it documented Bedrock keys arriving via the `env` block of host `settings.json`; that block is `{}` and the real path is `env_sources`. Corrected in place, with a note that the `env` block is nonetheless the right *target* design (§11.2) — it described the correct mechanism before anything used it | — | — |

---

# Every decision waiting on you

One index, because these are spread across four docs. **Nothing below is blocked on work — only
on an answer.** Where I have a recommendation it is stated; where I do not, it says so.

| # | Decision | Where | My read |
|---|---|---|---|
| **OQ-D1** | **How to fix the writable config snapshot** (D1) | below | make it host-owned and read-only in-jail |
| **OQ-T1** | #32: per-jail token + pinned cert **as proposed**, or full mTLS? *The PR author asked this and is waiting.* | [`loophole-transport.md`](../design/loophole-transport.md) §7 | as proposed |
| **OQ-T2** | Transport selection: automatic by platform, configured, or both? | same | automatic + override |
| **OQ-T3** | Per-jail client secrets on the `unix` transport too, or only TCP? | same | yes, both |
| **OQ-T4** | Does `macos-user` make the transport problem moot? | same | no, not today |
| **OQ-T5** | Is the endpoint file jail-writable, and does it matter? | same | state it explicitly either way |
| **OQ-T6** | Per-file loophole mounts as a framework feature, or one-off for the broker? | same | narrow fix now, general form with §4 |
| **N3** | Non-container nix: Option 0 / 2 / 3 | B-2 above | Option 2, no longer urgent |
| **OQ-1** | Is per-jail auth selection enough, or is dynamic switching required? | [`agent-auth-modes.md`](../design/agent-auth-modes.md) §10 | per-jail is probably enough |
| **OQ-2** | Bedrock bundle: stays in `env_sources`, or becomes a declared bundle? | same | declared |
| **OQ-3** | What happens on a mode switch mid-session? | same | require a restart; be honest about it |
| **OQ-4** | Should `check` verify the selected mode's credential is live? | same | yes |
| **OQ-5** | Should packs be able to require other packs (`requires_pack`)? | same §12.4 | yes, paired with `conflicts` |
| **OQ-6** | Auth packs **shipped or fetched**? *Gates building them.* | same | **fetched** |
| **OQ-7** | Does the Teams pack own the model IDs, or the base `claude` pack? | same | no recommendation yet |
| **OQ-8** | Generalize #32's transport, or merge as scoped? | same | merge as scoped, generalize at B1 |
| **OQ-9** | Is `env_sources` still the right home for the AWS keys? | same | no recommendation yet |
| **A2** | How loud should selecting both auth packs be? | Thread A | a `conflicts` manifest field |
| **S5** | Jail skill collision: warn, `check` failure, or boot refusal? | S5 above | warn now; decide the rest later |
| **OQ-B/E** | Approval grants: reusable? answered where? | [`boundary-broker.md`](../design/boundary-broker.md) §9 | §10 has worked answers |

## OQ-D1 — how to fix the writable config snapshot

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
