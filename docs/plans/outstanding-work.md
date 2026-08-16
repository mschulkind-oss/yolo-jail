# Ongoing work

**Status: 18 attention required · 6 ready · 0 in progress · 5 waiting on a Mac · 1 broken · 3 icebox.**

Last updated 2026-08-15. Counts tallied from this file, not asserted.

**What this is.** The forward plan and nothing else. **If it is in this file, it is not done.**

Work that shipped leaves immediately — the record is the commit history and
[`shipped-2026-08-12.md`](shipped-2026-08-12.md). Decisions to *not* build something leave too, to
[`retired-decisions.md`](../design/retired-decisions.md), because a rejected design with no record
gets re-proposed.

| | Means |
|---|---|
| 💬 | **needs you** — a decision or an answer. Nothing here is blocked on work. |
| 📦 | **ready to build** — designed, questions answered, no blockers. |
| 🏗️ | **in progress** |
| 🔒 | **waiting** — on a machine or an external dependency. Code is done or paused, not broken. |
| 🛑 | **broken** — actively failing, needs investigation. |
| 🧊 | **icebox** — acknowledged, deliberately not this cycle. |

---

# 💬 Attention required

If you read one section, read this one. Each carries my recommendation, so a bare *"go with your
read"* clears it. **💬🤷 marks the two where I genuinely have none** — subjective product calls where
manufacturing a recommendation would be worse than admitting the gap.

## The one that decides a design, not a task

### 💬 OQ-LP14 — the pack bind-host path rule is a halfway measure

**Reframed on review.** This was filed as *"the vocabulary is missing"*. The sharper question is
whether the rule should exist in its current form at all — and by this repo's own
[gate-placement principle](../design/gate-placement-principle.md), it mostly should not.

- **What the rule does.** A pack-shipped loophole's `host_bind_mounts[].host` must sit inside its own
  module dir or under `$HOME`. `${XDG_RUNTIME_DIR}/pulse/native` is neither in any spelling, so the
  socket half of `audio` is inexpressible for a pack.

- **Why it is halfway — measured.** The pack-shipped subset does **not** refuse `host_daemon`; it
  constrains only its `publishes` value. So the same pack may declare an arbitrary host argv that
  opens the very socket the bind rule refused. The rule blocks the **declarative** form of a
  capability while permitting the **imperative** form of it.

- **Test 1 says that is theatre.** The gate is aimed at an actor who already has the authority it
  protects. And the effect is worse than neutral: it pushes an author toward **arbitrary host
  execution** to obtain a read-only bind — the sharpest capability in the system to get the mildest.

- **The real gate is already there, and it is the claim.** Enumeration is total, so a bind emits an
  approvable string and a fetched pack cannot cross without the user seeing it. The path rule sits
  *on top of* that and refuses what a user could knowingly approve.

- **It also misfires on origin.** The subset applies to every pack-shipped loophole, so a `file://`
  pack — content in a directory you control, trusted unconditionally everywhere else — is refused a
  bind it could trivially write into the config block instead.

- **What it does buy, honestly:** a claim string must be machine-independent (G2a). A literal
  `/run/user/1000/…` hardcodes a uid. But `${XDG_RUNTIME_DIR}` is *more* portable as a raw string,
  and the rule refuses both — a portability rule that refuses the portable spelling.

- **I over-corrected last round, and the correction is withdrawn.** I wrote "drop the path rule and
  let the approval do its job". Two facts checked since say that is wrong:

  - **The `mount` kind refuses absolute paths too** (`appendPathProblems`: *"must be relative, not
    absolute"*). So the loophole bind rule is CONSISTENT with the existing declarative kind, not a
    patch over an inconsistency. Dropping it would make loophole binds the one declarative kind that
    can name any host path.
  - The inconsistency that remains is narrower and real: **declarative binds are confined to `$HOME`,
    while the `host_daemon` the same subset permits is not confined at all.** The rule can never be a
    boundary while that is true — but it is still the right DEFAULT, because most packs declare no
    daemon, and for those it holds.

- **Answering the specific proposal — "disallow hardcoded, require the var":** that is the version
  with false positives, and it is the one to avoid. `${HOME}/.ssh` is also a `$VAR`, so a
  *require-interpolation* rule admits exactly what the path rule exists to refuse. Interpolation is
  not evidence of anything; it is a spelling.

- **My read, restored and sharpened: a CLOSED, yolo-resolved list of runtime sockets.** The manifest
  names a member of the list (`pulse`, `pipewire`, …) rather than a path; yolo resolves the session
  runtime dir; the claim is emitted in the host-IPC class that already exists.

  - **No false positives by construction** — the list is closed, so nothing unintended is admitted.
  - Its failure mode is a false *negative* (a runtime socket nobody enumerated), which is fixed by
    adding a name, not by loosening a rule.
  - The approval string stays machine-independent, because what is approved is the declaration, not
    the resolved path.

- 📄 [`loophole-packaging.md`](../design/loophole-packaging.md) §3.1, §7 ·
  [overview](../design/loophole-packaging-overview.md) §5.1 ·
  [`packs/audio/README.md`](../../packs/audio/README.md) has the measurements

## Security and correctness

### 💬 OQ-LP8 / G2b — `ApprovedAt` names a guarantee it does not deliver

**Reframed on review: the question is not "should we build content-anchoring" but "why is the
half-built artifact still there".**

- **Measured.** `ApprovedAt` is written into the lockfile by `internal/cli/pack.go` as
  `"approvedAt"`, and read by **nothing** in production. `HostAccessApproved` compares claim strings
  only.

- **Why that is worse than absent.** It is a persisted, user-visible field in a *trust* file whose
  name states that the approval is anchored to a commit. Anyone reading that lockfile — or writing
  the next gate against it — would reasonably believe it is. That is the artifact form of the thing
  [gate-placement](../design/gate-placement-principle.md) Test 1 forbids: *it looks like protection
  while the real gap stays open, and a reviewer sees it and stops looking.*

- **YAGNI applies, and the extension-point exception does not rescue it.**
  [`extension-point-principle.md`](../design/extension-point-principle.md) protects designed
  extension *surfaces* from YAGNI. This is not one — it is a half-finished implementation with no
  reader, no documented contract for third parties, and no caller waiting on it.

- **DONE 2026-08-15: removed.** The field is gone and the gap is recorded as an assertion
  (`TestHostAccessApprovedComparesClaimStringsOnly`). The gap it gestures at is real but is already
  pinned by a test (`TestHostAccessApprovedIgnoresApprovedAtToday`), which is the honest way to
  record a known hole — a failing-if-it-changes assertion rather than a field that implies it is
  handled. If a fetched exec-bearing pack ever ships, decide content-anchoring then, on evidence.

- **What still needs you:** whether to build content-anchoring *at all*, later. Removing the field
  does not answer that; it only stops the codebase claiming an answer it does not have.

### 💬 OQ-D1 — the config-approval snapshot is agent-writable

- **The bypass.** `.yolo/config-snapshot.json` is mode `664` in a `:rw` workspace. An agent that
  edits `yolo-jail.jsonc` **and** matches the snapshot makes the launch-time diff prompt vanish —
  the exact bypass [`config-safety.md`](../design/config-safety.md) exists to prevent, and
  undiscussed there.
- **Four options:** move it to host state · per-file `:ro` mount · HMAC it · accept and document.
- **My read, corrected on review: MOVE IT to host state.** The `:ro` mount was my first choice and it
  is **podman-only** — Apple Container cannot bind-mount a single file (hence the `acMaterialize`
  workaround), and `macos-user` has no bind mounts at all. A security property that holds on one of
  three backends is not a security property; it is a coverage gap with a good story. Moving the
  snapshot into `paths.GlobalStorage()`, keyed by workspace path, removes the bypass on every
  backend and needs no mount at all.
- **What moving it costs, so the trade is visible:** the snapshot stops sitting next to the config it
  describes, and a workspace copied to another machine loses its approval state — which is arguably
  correct, since approval was granted on the machine that granted it.
- **Accept-and-document is the current state and is only defensible written down.**

### 💬 S5 — a jail resolves a skill-name collision silently

- **What happens elsewhere, measured.** At `apply --host` a collision is **refused by name and
  nothing is composed** — `reportSkillCollisions` prints one red line per collision and the whole
  skills surface is skipped. Deliberately a refusal rather than a crash: its own comment says *"a
  fatal error the user cannot act on is worse than the silent loss it replaces."*
- **In a jail it is silently resolved.** `internal/agents/skills.go` has **no collision concept at
  all** — no mention of the word — so the packs are copied in order and the last writer wins.
- **Is it an accident? Half.** S1's ruling was explicit that the collision is fatal *at apply time*,
  and the jail path was simply never given the concept. So it is a scope boundary that hardened into
  an inconsistency — not an oversight, but never a decision either. That is why it is a question
  rather than a bug.
- **Three options ascending:** a launch **warning** naming both packs · a **`yolo check` failure** ·
  a **boot refusal**. Note the apply-side precedent argues against the third: refusing to START a
  jail is much heavier than refusing to write a real home, and the existing comment already rejects
  "fatal the user cannot act on" for the lighter case.
- **My read:** warn now. The cheap half is worth doing regardless — the destinations are already
  computed and `hostskills.Collisions` is a pure function of them.

### 💬 OQ-S4 — does a skills `into` declaration NARROW delivery, or only ADD to it?

- **Measured asymmetry.** In a jail, every loaded pack's skills reach every declared destination.
  At `apply --host`, a pack reaches only the destinations it declared.
- **My read:** run `ResolveDestinations` on the jail path too, so both notches answer from one
  inference and `into` means what it says.
- **The trade to weigh:** under that rule a content pack declaring a unique path becomes inert where
  today it reaches everything. Arguably correct, arguably a regression.
- **Worth doing either way:** say the fan-out out loud in `yolo pack footprint`.

### 💬 OQ-CO — should two packs writing the same `config-overlay` key be LOUD?

- Silent last-one-wins today.
- **My read:** refuse and name both packs — the same shape as the `config` exclusivity collision
  that already ships.
- **Blocks nothing:** with one auth pack there is no second writer.
- 📄 [`pack-config-collaboration.md`](../design/pack-config-collaboration.md)

## Scope and direction

### 💬 N3 — non-container nix

- **Options:** stop here (`install_hints` prints a remedy) · `yolo --at host -- <cmd>` · a
  yolo-owned `nix profile` as a confirm-gated remedy.
- **My read:** option 2, and **no longer urgent** — the auth thread routed around the `env` refusal
  that motivated it.
- 📄 [`noncontainer-nix-environment.md`](../design/noncontainer-nix-environment.md) §8

### 💬 OQ-B1b — vendor unYOLO's policy engine, or re-derive it?

- Adopt-vs-build is settled (**build**); this is the one piece where copying may beat writing.
- **My read:** vendor at a pinned SHA — MIT, verified stdlib-only, ~2,100 lines, zero new module
  dependencies.
- 📄 [`boundary-broker.md`](../design/boundary-broker.md) §10.6

### 💬 OQ-B/E — approval grants: reusable, and where does the human answer?

- **My read:** take the two-bound **narrowing-only** grants and the separate operator listener.
- 📄 [`boundary-broker.md`](../design/boundary-broker.md) §9–§10.3

### 💬 OQ-E4 — do `stateful` surfaces get comment preservation?

- `rmw` shipped. `computed` is provably vacuous. `stateful` is the remaining mode, and it is a
  different problem wearing the same words: the file is composed from layers, so a comment can only
  come from the `host` layer and emitting it is a *projection*, not an in-place edit.
- **My read:** leave it, then build it when something needs it.
- **What would change that:** a `stateful` surface whose host source is a commented `.toml` a user
  actually maintains. Today there is none.

## Auth mode

All from [`agent-auth-modes.md`](../design/agent-auth-modes.md) §10, §12.4.

- 💬 **OQ-6 — auth packs shipped or fetched?** *Gates building `claude-bedrock`.*
  → **fetched**; shipping breaks the "six official packs" tests and embeds a personal auth choice in
  the binary.

- 💬 **OQ-2 — does the Bedrock bundle stay in `env_sources`, or become declared?**
  → **declared**; it is what lets `describe`/`check` report the active mode.

- 💬 **OQ-4 — should `check` verify the selected mode's credential is live?**
  → **yes**; refresh tokens hard-expire, so a stale overflow account fails exactly when needed.

- 💬 **OQ-3 — what happens on a mode switch mid-session?**
  → **require a restart and say so**; a half-applied switch is the dishonest option.

- 💬 **OQ-1 — is per-jail auth selection enough, or is dynamic switching required?**
  → per-jail is probably enough.

- 💬🤷 **OQ-7 — does the Teams pack own the model IDs, or the base `claude` pack?**
  → no recommendation; this is a preference about which pack owns a surface.

- 💬🤷 **OQ-9 — is `env_sources` still the right home for the AWS keys?**
  → no recommendation; it trades hygiene against convenience and both readings are defensible.

---

# 📦 Up next

Nothing here needs an answer first.

**Ordered by:** what unblocks something else in this file · then descending value-for-cost. So the
swallowed-build-failure fix leads — the 🛑 nightly below cannot be diagnosed without it, and
[`image-staging-vs-baking.md`](../design/image-staging-vs-baking.md) ranks it ahead of every
staging change it proposes — and the small independent items trail.

- 📦 **Pass `--accept-flake-config` on the three image `nix` invocations.**

  `autoload.go:319-323`, `build.go:44-49`, `check/sections_nix.go:19-20` do not pass it, so nix
  prints *"ignoring untrusted flake configuration setting 'extra-substituters'"* and never consults
  `yolo-jail.cachix.org` — the exact failure `darwinpkg.go:80-87` documents and guards against on
  the *other* nix path. Two-character change; makes a "build failed" on macOS mean something.
  📄 [`image-staging-vs-baking.md`](../design/image-staging-vs-baking.md) §6.

- 📦 **OQ-LP10 — retire the hand-placed loopholes dir.** Ruled yes, unblocked, not carried out.

  It is the one channel that starts a host daemon with **no selection step**, and retiring it forces
  `loopholes enable/disable` off its single special case — it serves only that dir today — into
  config state for every source.

- 📦 **Delete the credential harvest — the shared file always wins.** *(PS step 1; all four
  [`pack-code-separation.md`](../design/pack-code-separation.md) questions are answered.)*

  `harvestCredentialsFile` + `expiresAtMs` go (~135 lines), leaving `linkThroughShared` as
  symlink → copy-if-empty → discard-local. That removes the entrypoint's only `claudeAiOauth`
  knowledge and its one sanctioned tmp+rename. Two deliverables beyond the deletion: a doc comment
  naming the accepted failure mode (a revoked shared credential reverts a fresh login at the next
  boot; the exit is `rm`), and a regression test pinning `subscriptionType`/`rateLimitTier` survival
  across a broker refresh — [`agent-auth-modes.md`](../design/agent-auth-modes.md) §3 depends on it.

- 📦 **Move the broker's freshness check behind the `doctor_cmd` it already declares.** *(PS step 2.)*

  `check`'s `checkBrokerCredsFreshness` + `parseCredsExpiresAt` re-implement in Go a check the
  loophole extension point already covers: `doctor_cmd` is a manifest field, `RunDoctorChecks` runs
  it behind the origin and placement gates, `yolo check` already calls it
  (`check/sections_loopholes.go:60`), and the broker declares
  `["yolo","internal","daemon","claude-oauth-broker","--self-check"]`. Deletes a claude-specific
  section from `check`. **One decision inside it:** `DoctorResult` grades pass/fail while the current
  check prints remaining lifetime — either enrich the result shape or leave the reporting in `check`
  and move only the parsing.

- 📦 **Rename the claude names out of core.** *(PS step 3; mechanical, no behavior.)*

  `internal/agents` (the package name), `hostclaude.go` → `hostfiles.go`, the `skills.go:52` comment,
  and a ruling on `packdecl.DefaultBriefingFiles()` — which hardcodes `["AGENTS.md","CLAUDE.md"]` as
  the fallback pair for *every* pack's briefing. Fold in the stale `claude.go` line refs in
  [`agent-credentials.md`](../design/agent-credentials.md) and
  [`jail-home.md`](../design/jail-home.md) §4.2, which point past the end of a 199-line file and
  which step 1 invalidates again.

- 💬 **T2 is shipped except for one service that cannot follow it — and the blocker is not a client.**

  **Shipped.** Both generated-Python clients are gone: `cmd/yolo-cglimit` and `cmd/yolo-journalctl`
  are Go binaries baked into the image, the generator is retired *and unlinks what it already wrote*
  (`~/.local/bin` PRECEDES `/bin` on PATH, so deleting the generator alone would have left the
  scripts shadowing the binaries forever), and the journal bridge is on `loopback-tls` end to end.
  `unix-socket` is now unreachable from the run pipeline as well as unwritable in a manifest — the
  private `transportLegacySocket` constant is deleted.

  **The `cgroup-delegate` cannot move at all, for a reason no port fixes.** Its security model is
  kernel-attested identity: `create_and_join` writes the peer's HOST-NAMESPACE pid, read off the
  connection by `SO_PEERCRED`, into `cgroup.procs`, and *that write* is what moves the caller into
  the cgroup ([`security-shim.md`](../design/security-shim.md) §2). TCP carries no peer credential,
  and a loopback-TLS **front** is worse than nothing — `SO_PEERCRED` on the upstream socket would
  attest *yolo's own* pid, so the delegate would move the `yolo run` process into the jail's job
  cgroup. A client-supplied PID fails twice over: caller-asserted where the current value is
  kernel-attested, and namespaced to the container where the host needs one in its own namespace.

  **The cost of leaving it:** the delegate stays broken on macOS + podman, for exactly the virtiofs
  reason the unification exists to fix.

  **My read:** don't bundle this into a transport row. It is a *credential* question — can the
  transport carry a kernel-attested caller identity (NSpid translation host-side, or an
  `SCM_CREDENTIALS`-equivalent), or does the delegate stay a Linux-local AF_UNIX service by
  design? Either answer is defensible; picking one sizes the work. *"Leave it AF_UNIX, per-job
  cgroup limits are a Linux-host feature" is coherent and is what ships today.*

  *Trap, confirmed the hard way: the ship set is spelled TWICE — `flake.nix`'s `shippedBinaries` and
  `scripts/stage-source-bundle.sh`'s `SHIPPED_BINARIES`. A binary missing from the first vanishes
  from a source-built image; missing from the second it vanishes only from a SHIPPED-bundle image,
  which no dev jail ever builds. `internal/entrypoint/shippedclients_test.go` pins both to `cmd/*`.*

- 📦 **`--help` is unanswered on eight more subcommands, and one of them writes a file.**

  Surveyed while fixing `run`: `pack`, `config`, `apply`, `describe` and `check-deps` answer
  before config load. **`check`, `prune`, `ps`, `init`, `init-user-config`, `config-ref` and the
  `macos-*` commands do the work instead** — `check --help` runs a full check including a nix
  build, `prune --help` prints a full disk report, and **`init --help` scaffolds a
  `yolo-jail.jsonc`**. `loopholes` and `broker` do print usage, but to stderr with exit 1.

  Deliberately not fixed with `run`: the same-shape fix needs a usage const per command, which is
  new CLI text rather than the one-line change `run` needed. **`init --help` writing a file is the
  one worth doing first** — asking a command what it does should never change your project.


---

# 🔒 Waiting

Code is done or paused. Nothing here is broken; each needs a machine.

### On a Mac

- 🔒 **B-0 — macos-user renders ZERO pack surfaces.**

  The Go fix landed 2026-08-12: staging moved above backend dispatch, the tree is root-owned at
  `/var/yolo-jail/packs/<session>`, and three `PlanInvariants` refuse a plan that stages a tree
  nobody names. The whole decision surface is unit-tested on Linux.

  **No Mac has executed the sudo stage commands**, so whether `_yolojail` can read the staged tree,
  whether surfaces land in the sandbox home, and whether hooks run are all unknown. **Do not mark
  this done on the strength of the Linux suite.** One `--dry-run` shows the plan; one real launch
  closes it.

  *Why 🔒 and not 📦, since the code is written:* the status is genuinely ambiguous here, so it takes
  the reading that fails safe. Calling built-but-unverified work "ready" is how a Linux suite gets
  mistaken for a confirmation on the platform it never ran.

  *Separately unclaimed:* skills and briefings do not reach a macos-user home at all — they cross
  into a container as bind mounts, and this backend has none.

- 🔒 **B-1 — four more macos-user defects, none fixed.**

  - Claude's credentials symlink **dangles** — blocks the auth thread's Teams half on macOS.
  - The OAuth broker is **unwired**; `EndpointGrantCommands` has zero call sites.
  - Bedrock creds ride `/ctx/host-claude`, which does not exist there — blocks the other half.
  - `env_sources` secrets sit on the process argv, visible in `ps` to every user on the Mac — and
    they reach the launch argv but not the bootstrap argv, so MCP `${VAR}` gating silently drops
    every secret-gated server.

- 🔒 **T1 + D4 + #31 — confirmation only.** The unified `loopback-tls` transport and the
  `host-processes` fix both shipped and are green on Linux. **macOS + podman, Apple Container and
  macos-user were never executed** — and that is the platform the headline claim is about.

- 🔒 **B-3 — the `guest` notch.** `render.KindGuest` exists with no behaviour. Needs B-0 first, same
  abstraction.

- 🔒 **B-4 — three one-shot confirmations:** Cachix (one real download proof) · the first nightly
  after a release · one real cross-filesystem `cache_relocations` move.

---

# 🛑 Broken

- 🛑 **The macOS nightly cannot build an image.**

  `TestImageSkewOracleAnswers` fails on `nix eval .#installPrefix failed: exit status 1`, and the two
  lib-farm tests fail because the build failed and the run **silently fell back to a stale image**.

  **Plausibly one root cause, not three:** nix is broken on that runner. Not in our tree, so a
  re-trigger reproduces it. The next useful step is the swallowed `nix build` stderr, not another
  run. 📄 [`image-staging-vs-baking.md`](../design/image-staging-vs-baking.md) §7 traces the
  fallback exactly and names the missing call site; note the second 📦 item above — the runner may
  simply never have been allowed to reach the binary cache.

  *The multi-arch builder-index theory was tested and **refuted** — the arch warning is gone and the
  failures are identical. Recorded so it is not retried.*

---

# 🧊 Icebox

- 🧊 **E5 — `managed`/`defaults` array-append pinning.** Do not build speculatively.
- 🧊 **`requires_pack` / pack→pack composition.** No demonstrated need left.
  📄 [`retired-decisions.md`](../design/retired-decisions.md)
- 🧊 **E1+E2 — `host_files` modes 4→3, `readonly` as a real `:ro` mount.** A behaviour change on a
  shipped key; wants a design pass before it is worth queuing.

---

# Open threads

### Loophole packaging — shipped, one question open

The 15th contribution kind and everything in its landing order landed 2026-08-13/14, verified with
`just done`, `build-go` and a nested-jail launch.

**What remains:** OQ-LP14 (a design gap) and OQ-LP8/G2b (a decision), both above.

**Two things are unexercised rather than unbuilt**, and both are fixtures rather than design gaps:

- no *fetched* pack's loophole has been approved at a real prompt and spawned as a host daemon — the
  `audio` pack declares no daemon, and an embedded pack's approval is true by construction;
- the bundled→packs consolidation is blocked on OQ-LP14.

📄 [`loophole-packaging-overview.md`](../design/loophole-packaging-overview.md) — its status header
is the ledger · [`loophole-packaging.md`](../design/loophole-packaging.md) is the implementation
authority, with dated markers per section.

### Claude auth — one pack left to build

**`claude-bedrock`**: a `config-overlay` carrying `CLAUDE_CODE_USE_BEDROCK`, `AWS_REGION` and the
Bedrock-shaped model IDs, with AWS keys in `env_sources` so it stays secret-free and shareable.
**Gated on OQ-6.** Then `check`/`describe` should report the *effective* auth mode, which is OQ-2's
payoff.

There is deliberately **no** Teams pack — the base `claude` pack already is one
([why](../design/retired-decisions.md)).

**Still unrun and decisive for the ambitious version:** does Claude Code send a subscription OAuth
bearer to a non-Anthropic `ANTHROPIC_BASE_URL`? If yes, a proxy gives no-restart switching; if no,
pack-swapping is the ceiling. ~5 minutes
([`agent-auth-modes.md`](../design/agent-auth-modes.md) §6).

### Claude-shaped code in core — all four questions answered, three steps queued, one to design

[`pack-code-separation.md`](../design/pack-code-separation.md) inventories the imperative Go that is
still claude-shaped despite *"core does not know what an agent is"*. Reviewed and **decided**
2026-08-15; the doc carries the rulings inline as `> **DECIDED —**` blocks, with the superseded
leanings struck rather than deleted.

**The three landable steps are in 📦 above.** What remains here is the fourth, which is a
workstream, not a queue item:

- **Make the broker shippable** — build jail-daemon-as-binary, fold `internal/brokerrelay` into
  the framework-owned front, then move the broker onto both. This was the "leave it bundled" option
  in the design; it was decided the other way, so the broker stops being the standing exception to
  *agents are packs*. It is a **net subtraction** from core — the folding deletes the relay rather
  than relocating it.

  **Next step is design, not code:** §4 names the two vocabulary additions but does not design
  either, and the first one lands right next to **OQ-LP14** at the top of this file — both are about
  what a pack-shipped loophole may declare. Design them together, or the second will re-open the
  first.

**Two things the review settled without needing a decision**, small enough to fold into any commit
that touches the area: `packhooks.go:6` still says the three hooks are *"all currently claude's"*
(stale since ab39897), and the storage migrations (§3.4) predate packs but migrate **storage
layouts**, not a builtin→pack transition — the pack reform changed no on-disk path, so their
delete-by date is "no live workspace carries the old layout".

**Runtime evidence gathered during the review, so it is not re-derived:**
`~/.claude/.credentials.json` was still a symlink after ten days and many boots while its target
turned over the same day. Untested: a host where the broker is inactive, and the agy path, which has
**never run** — the pack staged into a live jail predates ab39897.

### Boundary broker — designed, not started

**B1b** (credential-injecting git proxy — host injects after egress, jail holds nothing) and **B2**
(approval-gated host credentials, one allowlisted verb, synchronous) are both settled as *builds*
rather than adoptions.

B1b carries OQ-B1b; B2 waits on N3 and OQ-1.
📄 [`boundary-broker.md`](../design/boundary-broker.md) §10.
