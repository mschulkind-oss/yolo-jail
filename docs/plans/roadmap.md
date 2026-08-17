# Ongoing work

**Status: 19 attention required · 3 ready · 1 in progress · 5 waiting on a Mac · 2 broken · 3 icebox.**

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

- ~~**My read, restored and sharpened: a CLOSED, yolo-resolved list of runtime sockets.**~~
  **WITHDRAWN — you asked me to convince you, and trying to is what changed my mind.** A closed list
  is an allowlist wearing an extension point's clothes: every new socket needs a yolo release, which
  is the opposite of what a manifest field is for. That objection lands, and it is the second time I
  have argued for this rule and had to retreat.

- **The argument that finishes it, and it is in this file already.** The rule permits everything
  under `$HOME` and refuses `${XDG_RUNTIME_DIR}/pulse/native`. So today it **admits `~/.ssh` and
  refuses a pulse socket** — it lets through the thing worth protecting and blocks the thing that is
  not. A gate with its two cases exactly inverted is not a weak gate; it is not a gate, and the
  `mount`-kind consistency argument I leaned on last round cannot rescue it. That analogy is false
  anyway: `mount` is relative-only because it stages **the pack's own content**, which has no
  business naming a host path at all. A loophole bind is the opposite kind of thing — reaching a
  host resource is its entire purpose.

- **What actually protects here is what always did:** total claim enumeration plus the approval. A
  bind emits an approvable string, and a fetched pack cannot cross without the user seeing it. The
  path rule sits on top of that and refuses what a user could knowingly approve — Test 1 of
  [gate-placement](../design/gate-placement-principle.md), aimed at an actor who already holds the
  authority it protects.

- **So: DROP the path rule. Keep one correctness property, which is not a gate.** The approved string
  must determine the bound path — normalize, resolve `..`, and refuse a declaration whose resolution
  is not stable between approval and launch. That is not "is this path allowed" (a judgement yolo
  cannot make for a user) but "does the thing you approved equal the thing I mount" (a property yolo
  must guarantee). `audio` then expresses itself as a pack with no new vocabulary at all, and
  OQ-LP14 stops being a missing feature.

- **It stopped being unblocking, 2026-08-15.** *"No bundled loopholes at the end of this sprint"*
  (OQ-BP4 below) cannot be reached without it: `audio` is the one bundled loophole that no pack can
  express, so the channel cannot be emptied until this is ruled. It was "nothing is blocked on this"
  when it was filed; it is now the gate on the sprint goal.

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

- **RULED 2026-08-17: yes, build it — and the shape is a yolo-owned lockfile pinning a commit.**
  The line you drew: a **local** pack you edit is your own business; a **fetched** git repo must not
  change under you without yolo noticing. Approving a config change approves *that pack at that
  commit*, and nothing after it.

  - **Half of this already exists**, which makes it much smaller than "a new mechanism":
    `packsrc.LockEntry` already records the resolved `Commit` alongside the ref
    (`internal/packsrc/lock.go`). The lockfile is there and it already pins.
  - **The missing half is the JOIN.** `HostAccessApproved` compares **claim strings only**, so a
    pack whose commit moves keeps an approval granted against different content — exactly the hole
    `ApprovedAt` pretended to cover and did not. The fix is to record the commit an approval was
    granted against and re-prompt when it moves. That is the whole of G2b, and it needs no content
    hashing: **the commit IS the content anchor** for a git-sourced pack.
  - **The installer line is already drawn, and further right than you feared.**
    `packdecl.Install.InstallerURL` — a URL whose contents run as a shell script — is honored only
    under the origin rule, so a **fetched pack cannot introduce one at all**. What remains is a local
    pack's own installer, which is the "your own files, your own fault" side of your line. So the
    install-time ack you asked for is only needed if we ever loosen that, and today the answer is
    stricter than an ack.
  - **What this leaves open** is narrow and worth naming: a `file://` pack has no commit to pin, so
    it gets no anchor by construction — which is the intended asymmetry, not a gap. And a fetched
    pack whose ref is a *branch* re-resolves on every install; the lockfile records where it landed,
    so "re-prompt when the commit moves" is well-defined even there.

  - **The same ruling one level down, and it found a live hole.** Asking "why can a fetched pack not
    ship an installer, when we are designing it to ship binaries?" exposed that the gate refuses
    `curl | sh` while permitting `npm install -g`, which runs postinstall scripts — both arbitrary
    code in the jail, decided oppositely. 📄
    [`pack-execution-trust.md`](../design/pack-execution-trust.md) replaces the mechanism list with
    one property (**pin what executes**), which closes the npm hole and opens the case you asked
    about. It also rules that approval prose must be readable by a new user, and carries the one
    genuinely open question (**OQ-X1**: does a digest-pinned installer script count, given its own
    fetches are not pinned?).

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
- **In a jail it is silently resolved.** `internal/jailcontent/skills.go` (was
  `internal/agents/skills.go`; renamed 2026-08-17, re-verified after) has **no collision concept at
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

### 💬 OQ-BP5/BP6 — emptying `bundled_loopholes` (broker included)

Decided *that* it ships ([`pack-code-separation.md`](../design/pack-code-separation.md) OQ-1);
[`broker-as-a-pack.md`](../design/broker-as-a-pack.md) designs *how*, and found the job smaller than
the parent doc estimated — jail-daemon-as-binary turns out to be already expressible and merely
unexercised.

**Three of the six are answered, and they set a sprint goal rather than a task:** the broker move and
the pack-shipped binary capability **ship together** (BP1); **`bundled_loopholes/` is empty at the
end of this sprint** (BP4); and **yolo prepends its own connection preamble to every
authenticated connection and never parses a daemon's payload** (BP2, design doc §5.5) —
`preamble: false` opts a dumb pipe out. I recommended against all
three; recorded here because the consequences need planning, not because they need re-arguing.

**BP4 is the one with a dependency: it makes OQ-LP14 above a hard blocker**, because `audio` cannot
be expressed as a pack until the runtime-socket vocabulary is ruled. Nothing else in the sprint waits
on it.

**What the sprint contains**, in the order I would run it:

1. **`host-processes` → pack** — 📦 below, ready to implement.
2. The identity rule (§5.5): one framework-owned frame prepended on the accepted connection,
   `preamble` defaulting to true. Built as part of step 1, which is what makes step 1 the
   proving ground. It **deletes** `readFirstMessage`/`stampJailID` rather than relocating them.
3. Rule OQ-LP14, then `audio` → pack, retiring the bundled copy the official pack sits beside.
4. Broker → pack: fold the relay, flip to `publishes: "socket"`.
5. The binary capability (design doc §3.1), whose slowest piece — a release matrix producing
   per-platform artifacts — should start early, not last.
6. Delete `bundled_loopholes/`.

**One rule worth stating up front:** all three bundled loopholes currently default to
`publishes: "endpoint"`, which the pack-shipped subset refuses. That is not a rule that is too
strict — it exists so a pack-shipped daemon cannot get TLS, tokens or endpoint permissions wrong —
it is a rule the three predate.

**Still open, and neither blocks anything above:**

- **OQ-BP5/BP6 — the pack-shipped binary capability**, now wanted as a general thing (per-arch
  selection, dynamic download from a release URL). **BP5:** download-with-digest only, or also a
  declared build step? *My read: download only* — a `sha256` in the manifest is what keeps "pinned
  pack" honest, since the lockfile's commit pins the tree and a release asset is not in the tree; a
  build step cannot be pinned at all and is the same risk class as `installerUrl`. **BP6:** may a
  *fetched* pack ship a **host-side** binary? *My read: allow it behind the existing host-execution
  approval* — a fetched pack can already declare an arbitrary `host_daemon.cmd`, so refusing the
  declarative form while permitting the imperative one repeats OQ-LP14's halfway shape. Neither
  blocks the broker.

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

- 📦 **`yolo loopholes enable|disable` writes nothing — the config-write is unbuilt.** *(The
  second half of OQ-LP10, which retired the directory that command served.)*

  `CmdSetEnabled` now PRINTS `loopholes.<name>.enabled`, the user config path and the value, and
  exits 1. Writing it means a read-modify-write of `~/.config/yolo-jail/config.jsonc` — a
  hand-written commented file nothing in yolo writes today — and the json5 → `jsonx.DumpsIndent`
  round trip drops every comment in it. That degradation was accepted for a yolo-GENERATED manifest
  and is a different proposition for the human's own file, so it is a **decision**, not typing. The
  obvious dodge is already refused: a conventionally-named auto-merged state file beside the config
  is withdrawn with cause in `internal/config/userlayer.go`'s header. Needs either a
  comment-preserving JSONC editor or a ruling that comment loss is acceptable here.
  📄 [`loophole-packaging.md`](../design/loophole-packaging.md) §5.2.

- ✅ **OQ-LP10 — the hand-placed loopholes dir is RETIRED.** *(Landed 2026-08-17.)*

  It was the one channel that started a host daemon with **no selection step**. `Discover` and
  `ValidateLoopholes` no longer read `~/.local/share/yolo-jail/loopholes`, the `SourceUser` label is
  deleted (ordering is bundled < pack < config), and `SetEnabled` — the manifest rewriter that
  existed only to serve it — is gone. A populated directory is **reported, never silently dropped**:
  one stderr notice per process from discovery plus a graded `yolo check` row, both naming every
  stranded module and the exact `mv` + `pack.json` for the implicitly-selected local pack. Two
  consequences worth knowing: `resolve()`'s default source label moved to the fail-safe `SourcePack`
  (no host code without a recorded gate, judged rather than exempted by the placement rule), and the
  module-dir face of the placement rule now applies to PACK loopholes only, since `user` was the one
  label that was both trusted and judged.

- 🏗️ **`host-processes` — steps 1-6 of 7 LANDED; the pack move is blocked.** *(Sprint step 1.)*

  Built 2026-08-15/16 via [`broker-as-a-pack.md`](../design/broker-as-a-pack.md) §12. What is in:
  the **connection preamble** end to end (codec, prefixed on the accepted connection where the
  host-derived identity already lives, read once by `hostservice` so every Go daemon inherits it),
  the `preamble` manifest key defaulting on, `ServeFrontedUnix`, host-processes running behind the
  framework front on `publishes: "socket"`, and `yolo-ps` no longer self-reporting a `jail_id`
  nobody trusted. `just done` green, tree clean.

  **What remains is step 7 — the official pack — and it is BLOCKED on the activation decisions**
  (OQ-A9's `enabled`/`default_enabled` collision decides what the pack's manifest even says, and
  OQ-A7 decides whether it needs selecting). Everything before it is independently shipped.

  **Three defects the adversarial pass caught, all fixed and pinned:**
  - The relay's `NoPreamble` opt-out — the guard standing between this work and every jail's Claude
    auth — had **no test that failed when it was removed**. The plan named an existing test as the
    tripwire; it passed without the opt-out. Now genuinely pinned (verified by mutation).
  - `preamble: "false"` (a quoted boolean) coerced to **true** through `Truthy` — the dumb-pipe
    opt-out silently did the opposite of what its author wrote. Now refused rather than coerced.
  - A connect-and-close probe against a fronted **framed** daemon leaked two goroutines and two fds
    **forever** and emitted no tier-1 record. `yolo check` does exactly that shape on every run.
    Fixed by signalling EOF upstream only when the client wrote zero payload bytes — the one
    condition that cannot cut a response short, and therefore cannot touch the relay.

  **One trap recorded for whoever writes step 7:** the plan's own recommended "mirror refusal"
  (refuse `{socket}` when `publishes` defaults to `endpoint`) would have refused the **shipped
  broker manifest** and taken down every jail's Claude auth. Correctly not implemented; it must stay
  that way or be written to exempt that shape.

- 📦 **Delete the credential harvest — the shared file always wins.** *(PS step 1; all four
  [`pack-code-separation.md`](../design/pack-code-separation.md) questions are answered.)*

  `harvestCredentialsFile` + `expiresAtMs` go (~135 lines), leaving `linkThroughShared` as
  symlink → copy-if-empty → discard-local. That removes the entrypoint's only `claudeAiOauth`
  knowledge and its one sanctioned tmp+rename. Two deliverables beyond the deletion: a doc comment
  naming the accepted failure mode (a revoked shared credential reverts a fresh login at the next
  boot; the exit is `rm`), and a regression test pinning `subscriptionType`/`rateLimitTier` survival
  across a broker refresh — [`agent-auth-modes.md`](../design/agent-auth-modes.md) §3 depends on it.

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

- 🛑 **Every loopback-TLS service is unreachable from every jail on this host — since 2026-08-13.**
  *Owned by me; three decisions below before I start.*

  `yolo-ps`, `yolo-journalctl` and the **Claude OAuth broker** all fail with
  `dial tcp 169.254.1.2:<port>: connect: connection refused`. The broker log carries **24** real
  refresh failures. Verified live: an AF_UNIX connect to `cgroup-delegate.sock` succeeds while both
  loopback-TLS endpoints *in the same directory* are refused — the only host service still working
  is the one that stayed on the retired transport.

  **Cause, verified rather than assumed.** `58ce9ee` (2026-08-13) hardcoded a `127.0.0.1` bind
  advertised as `host.containers.internal`, on the premise that the runtime forwards that name to the
  host's loopback. True for slirp4netns; **false for pasta**, which podman has defaulted to since 5.0.
  Differential probe settled where it actually forwards: the host's **global** address. **The
  in-flight preamble/pack sprint is innocent** — `git log -L` on the bind line returns `58ce9ee` plus
  the two commits that cancel out, and `ECONNREFUSED` at `connect(2)` means no preamble byte is ever
  written.

  **It fails CLOSED**, which is the one piece of good news and it is load-bearing: `/etc/hosts` pins
  `platform.claude.com` to `127.0.0.1`, so a jail cannot silently mint its own refresh instead. The
  single-use-token race stays prevented. Availability is lost, not safety.

  **Fix: change the runtime's forwarding, not the bind** — `--network=pasta:--map-host-loopback,<addr>`
  when podman reports `rootlessNetworkCmd=pasta`. `internal/svcendpoint` stays untouched, so the
  loopback bind, cert pinning, the per-jail token and the single-transport decision all survive.
  Note this means yolo starts emitting a network option on the **default** `bridge` path, where
  `assemble.go:252-259` deliberately emits nothing today — that is decision 1.

  **Two companion pieces, both non-optional:** `yolo check` reports **PASS during the outage**,
  because its probe substitutes `127.0.0.1` for the advertised host (`dial.go:15`), so it dials the
  one address a jail cannot use. And **nested-jail verification cannot reproduce this at all** —
  nested podman is forced onto `--net=host` — so AGENTS.md's verification instruction is misleading
  here and needs a carve-out, plus the integration coverage that does not exist.

  **All four design questions are answered (2026-08-17), so this is ready to build**, not to decide:
  emit the network option on the default path (OQ-R1); an enabled jail-facing service the jail cannot
  reach **fails the launch** (OQ-R2); and a passt supporting `--map-host-loopback` is a **hard
  requirement**, with no AF_UNIX revival (OQ-R3). Build the in-jail probe first and prove it in warn
  mode before making it fatal — a probe that misfires now costs a jail, not a log line.

  📄 [`loopback-tls-reachability.md`](../design/loopback-tls-reachability.md) — §2-§3 spell out every
  networking mode and where each actually delivers a packet; §5 prices the "bind globally" option
  that would work but trades a structural guarantee for LAN exposure.

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

### Claude-shaped code in core — all four questions answered, steps 1-3 shipped, one to design

[`pack-code-separation.md`](../design/pack-code-separation.md) inventories the imperative Go that is
still claude-shaped despite *"core does not know what an agent is"*. Reviewed and **decided**
2026-08-15; the doc carries the rulings inline as `> **DECIDED —**` blocks, with the superseded
leanings struck rather than deleted.

**Step 2 shipped 2026-08-17** — the freshness grading is `oauthbroker.gradeSharedCreds`, behind the
broker's own `--self-check`, and `check` no longer knows what `claudeAiOauth` is. Its one open
decision resolved *against* the shapes it offered: `DoctorResult` already carried `Output`, so
nothing needed enriching — the loss was that `check` discarded that output on the rc=0 path and
parsed only `FAIL:` lines on the other. `nixdiag.SplitSelfCheckLines` now reads the whole
`FAIL:`/`NOTE:`/`OK:` protocol, so the remaining-lifetime number arrives as text through the seam
every loophole already has (`pack-code-separation.md` §9, RESOLVED).

**Step 3 shipped 2026-08-17** — the renames, with one correction to the ruling. `internal/agents`
is **`internal/jailcontent`**: the name now says the output (content destined for a jail) rather
than the audience, and reads against its host-notch counterpart `internal/hostskills`.
`hostclaude.go` did **not** become `hostfiles.go` as §3.5 ruled — that filename was already taken,
by the *user's* `host_files` config key (`hostUserFileArgs`, `/ctx/host-user/<slug>`), which is a
different mechanism gated by config rather than by pack origin. Two host-file paths sharing one
filename is the confusion the rename was meant to end, so it is **`packhostgrants.go`**, matching
the `packhost*` prefix its sibling tests already use. `DefaultBriefingFiles()` returns
`["AGENTS.md"]`; the one test that pinned the CLAUDE.md fallback was inverted rather than deleted
(`TestJailBriefingDoesNotFallBackToClaudeMd`), and an absent `from: "CLAUDE.md"` now *reports* like
any other declared source instead of being silently conventional.

**Step 1's entry is still in 📦 above and is stale**: its code landed in `eb12125` + `cbf63d3`.
What remains beyond these is the fourth, which is a workstream, not a queue item:

- **Make the broker shippable** — now designed in
  [`broker-as-a-pack.md`](../design/broker-as-a-pack.md), and **the design shrank the job**. Two
  corrections to what §4 assumed: jail-daemon-as-binary is *already expressible* (the
  `{jail_loophole_dir}` token, an exec-capable `:ro` module-dir mount, and nix-ld all exist — the
  path is unexercised, not missing), and three of the relay's four jobs are already the
  framework-owned front's. The whole design reduces to one conflict: the relay stamps a trustworthy
  `jail_id` and the front is built never to parse the stream.

  It is still a **net subtraction** from core — `internal/brokerrelay` is deleted, not relocated.

  **Its three questions are in 💬 above.** And a correction to what this entry said yesterday:
  **do not pair it with OQ-LP14.** That one needs a new host-crossing claim class; this one needs
  none, so coupling them would delay the cheaper change behind the harder one.

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
