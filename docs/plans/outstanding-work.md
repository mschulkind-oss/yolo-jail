# Outstanding work

**What this is.** Everything still to do, and nothing else. Restructured 2026-08-12 around the
threads the maintainer named; refreshed 2026-08-13 after the loophole-transport design closed and
the first two PRs merged. **Completed work is REMOVED, not archived
here** — it moves to [`shipped-2026-08-12.md`](shipped-2026-08-12.md) (this fortnight) and
[`shipped-2026-08-pack-batch.md`](shipped-2026-08-pack-batch.md) (the earlier batch), which keep the
reasoning. **If a row is in this file, it is not done.**

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
| 🐛 | *(flag, not a status)* a live defect rather than new work. |

---

## The three active threads

| | | Thread | First move | Blocked on |
|---|---|---|---|---|
| 🟡 | **C** | [Open PRs + issues on the public repo](#thread-c--the-open-prs-and-issues-on-the-public-repo) | **two replies to send** — close #33 as fixed, and close #32 explaining it is superseded by T1. #37 and #34 are merged | nothing; both are messages, not work |
| 🟡 | **A** | [Claude auth as swappable packs](#thread-a--claude-auth-as-two-swappable-packs) | move `shared_credentials` off the base `claude` pack | nothing |
| ⛔ | **B** | [macos-user + non-container nix](#thread-b--macos-user-and-non-container-nix) | run B-0 once on a Mac (the wiring landed 2026-08-12; nothing else in the thread moves until a Mac confirms it) | a Mac to verify; N3 is your call |

Everything else is [below](#everything-else-still-open).

---

# Thread C — the open PRs and issues on the public repo

All on [mschulkind-oss/yolo-jail](https://github.com/mschulkind-oss/yolo-jail); `.env`'s
`GH_TOKEN` reads and pushes `origin`, so no extra credentials are needed. Reviewed 2026-08-12;
every premise re-verified against local code rather than taken from a PR body.

**What is left here is two messages, not code.** #37 and #34 merged 2026-08-13 and #35 auto-closed;
#33 is fixed and pushed. Both remaining items are replies owed to the same outside contributor —
see [`shipped-2026-08-12.md`](shipped-2026-08-12.md) for what shipped.

| | PR | Title | Author | Size | CI | Verdict |
|---|---|---|---|---|---|---|
| 🟡 | [#32](https://github.com/mschulkind-oss/yolo-jail/pull/32) | macOS+podman broker transport (fixes [#31](https://github.com/mschulkind-oss/yolo-jail/issues/31)) | Dong Liu, **external** | +1064/−13 | **green**; still `MERGEABLE` (re-checked 2026-08-13 against everything pushed) | **DECIDED: superseded by T1, not merged.** Needs a close comment — see C-3 |

| | Issue | Title | Author | State |
|---|---|---|---|---|
| 🟡 | [#33](https://github.com/mschulkind-oss/yolo-jail/issues/33) | **`ca.key` is mounted into every jail** | Dong Liu | **fixed and PUSHED** (C-4; `state_files` is on `origin/main`) — **still OPEN upstream.** Only a close comment is outstanding |
| ⛔ | [#31](https://github.com/mschulkind-oss/yolo-jail/issues/31) | Broker relay socket unreachable on macOS+podman | Dong Liu | **now fixed by T1, not #32.** Stays open until the unified transport ships — macOS+podman cannot run a jail meanwhile, a deliberate cost |

## 🟡 C-3. #32 — **superseded by T1, not merged.** One close comment outstanding

> **REVERSED 2026-08-13.** This section previously argued "land it, then promote it; do NOT close it
> as subsumed", and recommended against generalizing first. **The maintainer decided the opposite:
> we ship the unified transport (row T1) instead of merging #32.** The reasoning and the costs are in
> [`loophole-transport.md`](../design/loophole-transport.md) §7.3. The old argument is preserved
> there rather than here, because it is the road not taken and the doc records why.

**What the decision costs, so it is not later read as an oversight:**

1. **macOS + podman cannot run a jail until T1 ships.** Every `platform.claude.com` request 502s and
   Claude Code will not start ([#31](https://github.com/mschulkind-oss/yolo-jail/issues/31)). That
   window is now a deliberate choice.
2. **The transport is no longer free.** The earlier plan treated `loopback-tls` as already built —
   #32 *was* the implementation. T1 now covers **building** it as well as migrating both consumers.
3. **1064 tested lines are re-derived, not relocated.** Its test suite is the acceptance bar.

**Its own open question is settled and needs no ruling.** The author asked whether to keep the
terminator↔relay hop on a per-jail token + pinned host-only-key TLS, or switch to full mTLS.
**Answer: as proposed — and mTLS changes nothing**, because at **one relay per jail** the relay's
token *is* its jail's token, so a certificate's subject adds no identity a match does not already
establish ([§7.2](../design/loophole-transport.md)). Only a *shared* relay would reopen it.

**What the close comment has to say**, because closing a green, tested, conflict-free PR from an
outside contributor needs a real reason:

- his diagnosis of #31 was **correct** and is why `loophole-transport.md` exists;
- his transport design is **adopted almost wholesale** — §7.3 lists the seven mechanisms that must
  carry over verbatim, including the ones a naive reimplementation gets wrong (kernel-assigned port,
  key never persisted, re-read per dial, exact-cert pinning rather than CA trust);
- the reason it is not merged is **placement**: it lives in `brokerrelay`, and §3.3 argues that is
  exactly where the TCP path would drift from the one everybody uses. The framework has to own it —
  and `host-processes` (row **D4**) is broken on macOS for the identical reason his broker is;
- he also filed [#33](https://github.com/mschulkind-oss/yolo-jail/issues/33), which is **fixed and
  pushed**. That is a real contribution record even with the PR closed.

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

## 🟡 A2. The hazard: nothing prevents selecting both — and `provides` is the shape

Two auth packs selected at once yields `CLAUDE_CODE_USE_BEDROCK=1` from one, the credentials hook
from the other, and model IDs from whichever overlay lands last. **That is precisely the silent
wrong-state the whole mode model exists to prevent** — and it is the failure the maintainer's own
manual switch already demonstrated once (`agent-auth-modes.md` §2.3, where a hand-switch left a
Bedrock model pin behind).

**Ruled 2026-08-13: a `provides` capability, not a `conflicts` list.** An earlier draft recommended
`conflicts` — each pack naming the packs it cannot coexist with. The review's counter is better and
this row now carries it: **both packs should name a third thing rather than naming each other.**

```jsonc
// claude-teams/pack.json          // claude-bedrock/pack.json
{ "provides": ["claude-auth"] }    { "provides": ["claude-auth"] }
```

The invariant becomes *"at most one pack may provide `claude-auth`"*, checked at load.

**Why it beats `conflicts` on four counts:**

1. **No N² knowledge.** With `conflicts`, every auth pack must name every other one, so adding a
   third mode means editing the two that shipped. With `provides`, a third mode declares the same
   capability and the rule catches it for free.
2. **Third parties can participate.** A pack yolo does not ship cannot make itself an alternative
   under `conflicts` unless the shipped packs name it — which they cannot, because they predate it.
   Under `provides` it just declares the capability.
3. **It expresses the invariant rather than the symptom.** "These are alternatives for one job" is
   the actual fact; "A refuses B" is a consequence of it, restated once per pair.
4. **It subsumes A4/OQ-5.** `requires_pack` becomes `requires: ["claude-auth"]` — *"I need
   something that provides this"*, not *"I need `claude-teams` specifically"*. One mechanism, two
   open questions closed. See A4.

This is the ordinary package-manager shape (Debian `Provides`/`Conflicts`, RPM `Provides`, Arch
`provides`), which is a point in its favour — the semantics are well understood and users have met
them before.

**Who owns exclusivity — reviewed 2026-08-13, and my first answer was wrong.**

I offered three options and recommended (c), a separate `provides_exclusive` list. The review's
objection lands on (b) *and* (c) equally: **both are provider-owned, so nothing is definitive.** If
each provider asserts its own exclusivity there is no authority — two providers can disagree, and
the "lint error" I proposed for that is a tell that the model has no answer, not that it has one.
(c) was (b) with tidier syntax and the same hole.

**Corrected: the CONSUMER declares the slot; providers only fill it.** The `claude` pack knows
Claude Code has exactly one auth mode — that is a fact about the agent, and the agent's pack is the
right owner of it. Auth packs stay dumb.

```jsonc
// claude/pack.json — declares the slot and its arity
{ "capabilities": [ { "name": "claude-auth", "max": 1 } ] }

// claude-teams/pack.json        // claude-bedrock/pack.json
{ "provides": ["claude-auth"] }  { "provides": ["claude-auth"] }
```

**Why this is definitive where provider-owned was not:** one declarer, one statement, no
disagreement possible. A provider that names a capability nobody declared is a lint error with an
obvious fix, rather than an unresolvable conflict between equals.

**`max: 1`, not `exactly: 1` — the hazard is two, not zero.** A2's problem is selecting *both*
auth packs. Requiring *at least* one would newly break `packs: ["claude"]` on its own, which A1
deliberately makes legal (an auth-neutral base pack, credential sharing as an explicit choice). So
the arity to enforce is a ceiling, not an equality. If a floor is ever wanted it is a separate
decision with a separate cost.

**On the review's reservation** — *"all agents need auth, so being clear about where that auth
comes from isn't terrible, but still not great"* — the discomfort is fair but I think it resolves:
the claude pack is not learning about an auth-pack ecosystem, it is declaring **its own shape**
("I have one auth slot"). It names no pack and needs no edit when a third mode appears. That is the
same reason `into` is a path rather than an agent name: packs describe themselves, and core stays
ignorant.

**Could yolo own it instead?** Only for a capability that is *core's*, and `claude-auth` is not —
core deliberately does not know what an agent is (`internal/packdecl`'s opening premise), so it
cannot know that Claude Code has one auth slot. yolo owns the **mechanism** (the field, the arity
check, the error message); the pack owns the **fact**. A core-level registry of capability names
would rebuild the agent registry the pack system exists to avoid.

**What is left to decide** is narrower than it was: whether the slot declaration lives on a
`capabilities` field as above, or is folded into `requires` with an arity
(`requires: [{capability: "claude-auth", max: 1}]`). The first reads better for a slot nobody is
obliged to fill; the second avoids a fourth composition field. **See OQ-A2.**

**One thing to check before building:** a capability is just a string, so a typo silently creates a
new capability with one provider and no requirer. `yolo pack lint` should warn on that — it is the
same failure mode as a misspelled skill destination, and cheap to catch there.

## 🟡 A4. Composition — packs cannot depend on other packs

Verified 2026-08-12: **no pack→pack mechanism exists.** The `requires` kind takes a `bin`, not a
pack name — it asserts a binary is on `PATH` and carries install hints. See
[`agent-auth-modes.md`](../design/agent-auth-modes.md) §11.1.

So composition today is the flat, ordered, user-scope `packs` list:
`["claude", "claude-bedrock", "matt-bedrock-extras"]`. That is adequate for manual swapping — the
list *is* the mode selector — but nothing stops a personal pack being selected without the auth
pack it was written for.

**A4 is the same mechanism as A2, seen from the other side.** Rather than a separate
`requires_pack`, the composition primitive is `requires: ["<capability>"]` — *"I need something that
provides this"*. So `matt-bedrock-extras` requires the capability the Bedrock pack provides, and
never names a pack. `provides` + `requires` over one capability namespace closes A2 and A4 together;
see A2 for the shape and the remaining sub-question. **OQ-5 is therefore folded into OQ-A2.**

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
5. **A2/A4 together** — `provides` + `requires` over one capability namespace (**OQ-A2**).

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

---

# Everything else still open

| | # | Item | Kind | Blocked on |
|---|---|---|---|---|
| 🔄 | **T1** | **Build the unified `loopback-tls` transport, replacing PR #32** — **IN PROGRESS 2026-08-13** via an orchestrated build (survey → spec → four sequential stages → adversarial verification → completeness critique). This row and **D4** will be rewritten by that work; treat what follows as the brief, not the status. ([loophole-transport.md](../design/loophole-transport.md) §7.3, decided 2026-08-13) — #32 is CLOSED, not merged, so this covers **building** the transport as well as migrating both consumers. Its design is the spec and its test suite the acceptance bar (§7.3 lists what must carry over, incl. one relay per jail, which §7.2's token answer depends on). Port `host-processes` first (**D4**, broken on macOS today, harmless failure), then the broker relay, then drop `unix-socket` from `validTransports`. Also: the macOS-`guest` cross-uid grant, and correcting [loophole-protocol.md](../design/loophole-protocol.md) §Security posture. ⚠ **macOS + podman cannot run a jail until this ships** — a deliberate cost | feature | nothing |
| 🟢 | **B1** | Audit-only log of every jail↔host boundary crossing ([boundary-broker.md](../design/boundary-broker.md) step 1) | small, additive | nothing |
| 🟡 | **B1b** | **Credential-injecting proxy for git** — host injects after egress, jail holds nothing, no human. **Settled 2026-08-12: a BUILD, not an adoption.** unYOLO's `gh-broker` was read at source ([§10](../design/boundary-broker.md)) and the earlier "possibly an adoption" note is retired — it is Go not Python, but yolo **already ships this transport** (`claude-oauth-broker` *is* a credential-injecting TLS-interception proxy), and gh-broker wants a GitHub App, has bus factor 1 at 11 weeks old, and carries 73 modules against yolo's 3. Smaller build than the row implied. **Carries one decision — OQ-B1b** | new capability | **your call** (OQ-B1b) |
| 🟡 | **B2** | Approval-gated host credentials — one allowlisted verb, synchronous. Design validated by convergence with unYOLO. **Re-scoped 2026-08-12 from source** ([§10.6](../design/boundary-broker.md)): take the four-effect policy evaluation, code-owned `Grantable`, the operation registry, and two-bound **narrowing-only** grants; **defer** content-addressed plans, `expected_revision`, and decision tokens — each has a named trigger, and none has fired | new capability | N3/OQ-1 |
| 🟡🐛 | **D1** | **Config-approval snapshot is agent-writable** — `.yolo/config-snapshot.json` is mode `664` and writable in-jail (re-measured 2026-08-12). An agent that edits `yolo-jail.jsonc` **and** matches the snapshot makes the launch-time diff prompt vanish — the exact bypass [config-safety.md](../design/config-safety.md) exists to prevent, and it is undiscussed there. From `ROADMAP.md` §4d; never queued until now. **Has an open question — see OQ-D1** | security | **your call** (OQ-D1) |
| 🟢🐛 | **D4** | **`host-processes` is silently broken on macOS + podman** — found 2026-08-12 while writing [loophole-transport.md](../design/loophole-transport.md) §2.1. Its manifest declares `"transport": "unix-socket"`, the *same* transport whose virtiofs failure is [#31](https://github.com/mschulkind-oss/yolo-jail/issues/31); `yolo-ps` fails identically. Unreported because a broken `yolo-ps` is quiet where a broken broker blocks startup. Means the loophole is Linux-only in practice while advertised as available. **Porting it is also the natural proof for the transport generalization** (§6 step 3) | bug + the generalization's test case | nothing |

---

# Every decision waiting on you

🟡 **Everything in this section is waiting on you.** One index, because these are spread across
four docs. ❓ marks the two where I have no recommendation; the rest carry one, so a bare "go with
your read" clears them. **Nothing below is blocked on work — only on an answer.**

**The loophole-transport design is now fully settled — ZERO of its nine questions remain.** Five
resolved without needing a ruling (recorded with reasoning in
[`loophole-transport.md`](../design/loophole-transport.md) §7.1, so nothing was quietly dropped),
and four were decided by the maintainer on 2026-08-13: token in the endpoint file (OQ-T7), unify on
`loopback-tls` (OQ-T9), token not mTLS (OQ-T1 — which turned out to need no judgement once "one
relay per jail" was noticed), and ship T1 instead of #32 (OQ-T8). What is left there is execution.

| # | Decision | Read it in | My read |
|---|---|---|---|
| **OQ-A2** | **Where does the capability SLOT get declared?** `provides` is ruled, and provider-owned exclusivity is ruled OUT (nothing definitive). The consumer declares the slot and its arity — the open bit is whether that is a `capabilities` field or an arity on `requires`. **Folds in the old OQ-5** | **this file → Thread A → A2** (and A4 for the requires half) | a `capabilities` field, `max: 1` — a ceiling not an equality, so `packs: ["claude"]` alone stays legal |
| **OQ-D1** | **How to fix the writable config snapshot** — move it out of the workspace, per-file `:ro` mount, HMAC it, or accept-and-document | **this file → OQ-D1 below**; background in [`config-safety.md`](../design/config-safety.md) | (2) if a per-file `:ro` mount inside the workspace is practical, else (1). **(4) is the current state and only defensible if written down** |
| **OQ-S4** | **Should the jail narrow its skills fan-out to match the host?** Or: does a declaration NARROW delivery or only ADD to it? | **this file → OQ-S4 below**; the audit evidence is in [`shipped-2026-08-12.md`](shipped-2026-08-12.md) § S4; the mechanism is `packload.ResolveDestinations` | (2) — run `ResolveDestinations` on the jail path too. (1) is the honest fallback and worth doing either way |
| **OQ-E4** | **Do `stateful` surfaces get comment preservation too?** `rmw` shipped, `computed` is provably vacuous | **this file → OQ-E4 below**; the five costed options are in [`host-file-staging.md`](host-file-staging.md) | (3) for now, then (1) when something needs it — no shipped `stateful` surface has a commented host source |
| **S5** | **Jail skill collision: warn, `check` failure, or boot refusal?** | **this file → S5 above** | **warn now**; the cheap half is worth doing regardless of the rest |
| **N3** | **Non-container nix: Option 0 / 2 / 3** | [`noncontainer-nix-environment.md`](../design/noncontainer-nix-environment.md) §8; summarised in **this file → B-2** | Option 2, **no longer urgent** — A3's correction routed Thread A around it |
| **OQ-1** | Is per-jail auth selection enough, or is dynamic switching required? | [`agent-auth-modes.md`](../design/agent-auth-modes.md) §10, with the limits in §6 | per-jail is probably enough |
| **OQ-2** | Bedrock bundle: stays in `env_sources`, or becomes a declared bundle? | [`agent-auth-modes.md`](../design/agent-auth-modes.md) §10 | declared — it is what makes `describe`/`check` able to report the active mode |
| **OQ-3** | What happens on a mode switch mid-session? | [`agent-auth-modes.md`](../design/agent-auth-modes.md) §10 | require a restart, and say so — a half-applied switch is the dishonest option |
| **OQ-4** | Should `check` verify the selected mode's credential is live? | [`agent-auth-modes.md`](../design/agent-auth-modes.md) §10, cost in §7.2 | yes — refresh tokens hard-expire, so a stale overflow account fails exactly when needed |
| **OQ-6** | **Auth packs shipped or fetched?** *Gates building them* | [`agent-auth-modes.md`](../design/agent-auth-modes.md) §12.4, constraint in §11.5 | **fetched** — shipping breaks the "six official packs" tests and embeds a personal auth choice in the binary |
| **OQ-7** ❓ | Does the Teams pack own the model IDs, or the base `claude` pack? | [`agent-auth-modes.md`](../design/agent-auth-modes.md) §12.4 | ❓ **no recommendation — genuinely your call** |
| **OQ-9** ❓ | Is `env_sources` still the right home for the AWS keys? | [`agent-auth-modes.md`](../design/agent-auth-modes.md) §12.4; hygiene cost in §7.4 | ❓ **no recommendation — genuinely your call** |
| **OQ-B1b** | **Vendor unYOLO's policy engine, or re-derive the model?** Adopt-vs-build is settled (build); this is the one piece where copying may beat writing | [`boundary-broker.md`](../design/boundary-broker.md) §10.6 | **vendor at a pinned SHA** — MIT, verified stdlib-only, ~2,100 lines, zero new module deps |
| **OQ-B/E** | Approval grants: reusable, and where does the human answer? | [`boundary-broker.md`](../design/boundary-broker.md) §9, with worked answers in §10.2–§10.3 | §10 answers both — take unYOLO's two-bound narrowing-only grants and its separate operator listener |

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

Moved out so this stays a forward plan:

- **[`shipped-2026-08-12.md`](shipped-2026-08-12.md)** — this fortnight: #37/#34 merged, #33 fixed,
  V2, V3, D3, E4's `rmw` half, D2 + two more doc/code contradictions, B4, the S4 audit (**read that
  before re-auditing S4**), the auth/broker doc split, the unYOLO source evaluation, and
  `loophole-transport.md` settled — including three arguments of mine that review withdrew.
- **[`shipped-2026-08-pack-batch.md`](shipped-2026-08-pack-batch.md)** — the 2026-08-03/04 batch:
  S1–S3, C1–C3, N1, N2, V1, and the nine defects that surfaced only by running the lifecycle.

## The local pack IS layer 4 — kept because the rationale is load-bearing

yolo now owns `~/.claude/skills` and `~/.claude/CLAUDE.md` wholesale, so **a user contribution has
nowhere else to live.** "Commit it to a repo pack" is not an answer for a half-baked skill, a
machine-specific one, or scratch space you do not want in git. The jail already had this slot —
layer 4, "the user's OWN skills tree, written last so a same-named local skill wins". The local pack
is that slot given a home yolo does not overwrite, which is why S3 was a defect rather than a design
choice. As an ordinary pack entry appended last by `config.LoadPacks` it already holds layer 4's
precedence, so the fix was to DELETE the fourth layer, not repoint it.
