# Outstanding work

**What this is.** Everything still to do, and nothing else. Written 2026-08-05, after the
ten-item pack batch shipped. If an item is here it is **not built**; if it was built, it is in
[`shipped-2026-08-pack-batch.md`](shipped-2026-08-pack-batch.md) with the reasoning and the
defects that only surfaced by running it.

**Read this as today's forward plan.** Every claim below was checked against the code on
2026-08-05, not against a status marker — three stale "still open" markers turned up in these
docs during the batch, so verify-against-code is the house rule now.

**Nothing here is blocked on a decision except where a row says so.**

---

## The short version

| # | Item | Kind of work | Blocked on |
|---|---|---|---|
| ✅ **S1** | ~~Skill collisions are SILENT at flat tier and produce two names at namespaced~~ **DONE** (`3e0be7b`) — unnamespaced by default, fatal at `apply --host`, naming both packs/paths/remedies. Verified end-to-end on the real `agent-standards` case, including that the message's own remedy resolves it. **See S5: the JAIL notch still resolves silently** | — | — |
| ✅ **S2** | ~~`tier` becomes an opt-in pack declaration~~ **DONE** (`663cb29`, `0557c9e`) — manifest-level `skills_tier`; the per-destination inheritance is gone, so one skill has ONE name everywhere. The retired per-contribution `tier` is refused BY NAME at authoring and TOLERATED at the version boundary — making it a `Validate` problem reproduced the original `tier` incident in mirror image and stopped jails booting (`ceb93b3`) | — | — |
| ✅ **S3** | ~~The jail's layer 4 reads the DESTINATION, which yolo now owns~~ **DONE** (`315c150`) — the layer is DELETED rather than repointed: the local pack is already layer 2 and composes last, so it holds exactly the precedence layer 4 provided. Repointing layer 4 at the local pack would have kept the double arrival S3 is about | — | — |
| **S5** | **A jail resolves a skill-name collision SILENTLY** — the notch S1 does not reach. Measured 2026-08-05 with the same two-pack set that `apply --host` refuses: the jail's flat merge (`agents.PrepareSkills`) picked the local pack's copy and printed nothing. S1's ruling says "fatal at apply time", so this is not a regression — but it is the same silent loss at the other notch, and it is now the ONLY place it survives | 🔴 live gap | nothing |
| **S4** | **UNAUDITED:** can a pack's `into` deliver to an agent the user never selected, and do all packs' skills reach all destinations? | audit, not yet done | nothing |
| ✅ **N1** | ~~nix profile has no gcroot~~ **DONE** (`23cee7a`) — rooted by the build's own `--out-link`, so rooting cannot fail separately from the build. Root is a SIBLING of `build/roots`, not in it: `prune.PruneOrphanImageRoots` reaps every symlink there that no live IMAGE needs, and would have unrooted this on a routine `yolo prune --apply` | — | — |
| ✅ **N2** | ~~Generalize `yoloDarwinPackages`~~ **DONE** (`11f8bb7`) — `yoloNoncontainerPackages`, `NativeSystem()`, and `describe`/`check` report the profile path. Named for the AXIS (no baked image) rather than either notch, since `guest` needs the same closure. Found a FOURTH E8 instance: the nix-probe *remedy string* said `aarch64-linux` while its detector matched any `<arch>-linux`, so an Intel Mac was told to delete a line it does not have | — | — |
| **N3** | Non-container nix: pick Option 0/2/3 beyond N2 | **your decision** | you |
| **P7** | The `guest` notch (env-manager Phase 7) | feature | a Mac |
| **B1** | Audit-only log of every jail↔host boundary crossing ([boundary-broker.md](../design/boundary-broker.md) step 1) | small, additive | nothing |
| **B1b** | **Credential-injecting proxy for git** — host injects the token after egress, jail holds nothing, no human. The tier [boundary-broker.md §5](../design/boundary-broker.md) was missing; it is what the motivating GitHub case actually wants, and unlike B2 it is **not** blocked on N3. **Possibly an ADOPTION, not a build**: unYOLO's MIT-licensed `gh-broker` is this row's entire scope ([§10](../design/boundary-broker.md)) — evaluate before writing code | new capability | nothing |
| **B2** | Approval-gated host credentials — one allowlisted verb, synchronous ([boundary-broker.md](../design/boundary-broker.md)). Design **validated by independent convergence** with unYOLO; take its grant model (duration + use budget, operator narrows at approval), content-addressed execution plans, and `expected_revision` rather than re-deriving them — see [§10](../design/boundary-broker.md) | new capability | N3/OQ-1 first (shape depends on it) |
| **B3** | **Claude auth MODES as config** ([agent-auth-modes.md](../design/agent-auth-modes.md)) — `claude_auth: subscription \| bedrock`, `autonomy`-shaped bundles. A mode is `{credential, env, MODEL IDs}`: the maintainer's own manual switch to Teams on 2026-08-05 moved the first two and **left a Bedrock-shaped model pin behind**, which is the bug in miniature. Credential half now verified end-to-end against a real `/login` | gap | nothing; separable from B2 |
| **B4** | Correct [agent-credentials.md](../design/agent-credentials.md) §3 — it documents Bedrock keys arriving via the `env` block of host `settings.json`; that block is `{}` and the real path is `env_sources` | doc defect | nothing |
| **E1+E2** | `host_files` modes 4→3, `readonly` as a real `:ro` mount | behavior change on a shipped key | a design pass (E2 first) |
| **E3** | Capture on terminate (the `yolo config capture` half SHIPPED) | small | nothing |
| **E4** | Comment preservation on `json`/`toml` surfaces | small, decisions already made | nothing |
| **E5** | `managed`/`defaults` array-append pinning | small | **do not build speculatively** |
| ✅ **V1** | ~~Reserved destinations miss symlink aliases~~ **DONE** (`8e7717f`). The three named aliases were ALREADY fixed (`99fabe6`, 2026-07-27) — a fourth stale marker in this doc. But a live one of the same shape remained and no literal list could cover it: a home-root dest stages at `.config/yolo-home/<slug>`, a slug derived from user input, so it is reserved as a SUBTREE | — | — |
| **V2** | `apply --host` is not whole-home idempotent until apply 3 | pre-existing, in `config` | nothing |
| **V3** | Pack-set-wide archives land under `archive/skills/` even for `files` | cosmetic, migration-shaped | nothing |
| ✅ **C1** | ~~Drop the now-redundant `"from"` literals from the six shipped packs~~ **DONE** (`d342827`) — the render fingerprint did NOT move: byte-identical across all 10 rendered files vs the pre-batch base, measured under a FIXED workspace path (`${workspace}` substitution otherwise masks the comparison, which is why the coarse `TestRenderFingerprintStable` cannot answer this on its own) | — | — |
| ✅ **C2** | ~~Briefing prose should read the Profile~~ **DONE** (`f2d0692`) — the enforcement vector and autonomy bit derive from `ProfileFor`; the payoff is the default branch, which used to tell an unenumerated notch it was in a container | — | — |
| ✅ **C3** | ~~`Collect`'s autonomy parameter~~ **DONE** (`db695d8`). Premise was stale: only ONE literal remained, not three | — | — |

---

## S1–S3 — SHIPPED 2026-08-05. What the ruling cost, and the one notch it did not reach

The full reasoning is in [`shipped-2026-08-pack-batch.md`](shipped-2026-08-pack-batch.md); the
design record is `docs/design/pack-system.md`'s §6a passage, marked superseded in place. Kept here
only for what a reader of THIS file still needs:

**S1 — a declared collision is fatal.** Unnamespaced by default; two packs claiming one entry name
at one destination refuses the whole `apply --host` before any destination is touched, naming both
packs, both source paths, and both remedies. The message is most of the value, because it fires on a
real case — verified end-to-end on exactly the predicted one (a personal pack and a shared pack both
shipping `agent-standards`), including that following the remedy the message prints resolves it.

**What it cost, deliberately.** §6a-5's flat-tier OVERRIDE is gone: the local pack no longer wins a
same-named skill, it refuses. An intentional override and an accidental clash are the same
declaration, so yolo cannot tell them apart and the ruling says the user should. §6a-5's actual fix
— precedence is LAYER ORDER, not a permission a record grants — is untouched and still exercised
wherever the local pack ships a name nobody else does.

**S2 — the tier is the PACK's.** `skills_tier` at the manifest level. The per-destination
inheritance was the defect's engine: a zero-ceremony pack borrowed two destinations and inherited a
tier from each, so the user's own local pack was namespaced in Claude and flat in codex, and one
skill had two invocation names it never chose.

**S3 — layer 4 is DELETED, not repointed.** The plan proposed pointing it at
`paths.LocalPackDir()/skills`. That would have kept the double arrival S3 is about: the local pack is
an ordinary pack entry, so it is ALREADY layer 2, and `config.LoadPacks` appends it last — which is
precisely the precedence layer 4 existed to provide. The layers are now disjoint with no fourth.

**Two traps worth carrying forward:**

- **A retired manifest field is a VERSION-SKEW fact, not a structural one.** Refusing the retired
  per-contribution `tier` inside `Validate` reproduced the original `tier` incident in mirror image:
  `DecodeTolerant` runs `Validate`, so an OLDER baked entrypoint reading the newly-staged manifests
  refused them and the jail would not start — no route to recovery, since the offending manifest is
  one yolo ships. Found only by running a nested jail against the previous baked image. The refusal
  belongs in `Decode` alone.
- **The coarse fingerprint test cannot answer "did the bytes move?"** `TestRenderFingerprintStable`
  pins the file SET. A byte comparison needs a FIXED workspace path, or claude's
  `projects["${workspace}"]` differs between two runs and hides the answer in noise.

## S5 — a JAIL resolves a skill-name collision silently 🔴

**Measured 2026-08-05**, with the same two-pack set `apply --host` now refuses: the jail came up, and
`~/.codex/skills/mine` held the local pack's copy. The other pack's skill was simply not there, and
nothing said so.

This is **not a regression** — S1's ruling is explicit that the collision is fatal *at apply time*,
and `internal/agents/skills.go` has no collision concept (nor any tier concept: the jail is
unnamespaced-only, which is now the same thing the default is everywhere). But it is the same silent
loss the ruling exists to remove, and after S1 it is the **only** place it survives.

**What makes it a real decision rather than a port.** A jail launch is not an apply: refusing to
START a jail over a skills collision is a much heavier consequence than refusing to write a real
home, and A12's fatal-generator policy would make it exactly that. Three options, in ascending
severity — a WARNING naming both packs at launch (cheap, and closes the "nothing said so" half); a
`yolo check` failure (loud where the user is already asking about config, and non-fatal at launch);
or a boot refusal (consistent with the host, and the one that can strand someone mid-task).

The cheap half is worth doing regardless of which: the destinations and their layers are already
computed, and `hostskills.Collisions` is a pure function of them.


## S4 — UNAUDITED: does `into` respect the selected agent set?

**Not investigated. Recorded so it is not mistaken for a settled question**, because two readings
of the code point the same way and neither has been probed.

The suspicion, from reading `run.packSkillTargets` (`internal/cli/run/prepare.go:304`) and
`agents.PrepareSkills` (`internal/agents/skills.go:86`) — named rather than line-pinned, because S3
moved both:

1. **`packSkillTargets` iterates `loadedPacks` and emits a target per `skills` contribution, using
   `Dest: c.Into`.** So the destination list comes from whatever packs are loaded — and `into`
   names a *specific agent's* directory. Nothing visible checks that the named agent is one the
   user selected. A pack declaring `into: ".codex/skills"` while only `claude` is in `packs`
   looks like it would still create and populate a codex dir.
2. **`PrepareSkills` copies EVERY pack's `skills/` into EVERY staging dir** (`packSkillDirs` is a
   flat list, `skills.go:111`, with no per-destination filter). So a codex-specific pack's skills
   look like they land in `.claude/skills` too. **S3 sharpened this rather than changing it:** that
   loop is now the LAST layer, so it is the only thing deciding a staged dir's contents.

**Why this matters beyond tidiness.** `packs` is the user's selection gate — it is USER-SCOPE ONLY
precisely so a repo-committed config cannot decide what content enters the environment. If `into`
can name any agent's directory regardless of selection, then the gate is on *loading* while the
effect is on *delivery*, and the second is not a subset of the first. That is the same shape as the
mise-trust finding: the enforcement point and the real boundary were different layers.

**What would settle it** — three probes, none run:

1. Select **only** `claude`; add a pack declaring `into: ".codex/skills"`. Does `.codex/skills`
   get created/mounted?
2. Two packs, each shipping a distinct skill, two destinations. Does each skill reach both?
3. Do the jail and host now **disagree**? Q6 changed the host to wholesale composition and left
   the jail's flat merge alone, so this may be a fourth notch-divergence on top of S1–S3.

**S1 is done, and probe 3 is now partly answered — see S5.** The jail and host DO disagree, on
collisions: the host refuses, the jail picks a winner silently. That was measured with a two-pack
set, not with the `into`-vs-selection question this item is about, so S4 itself is still unaudited —
but the "fourth notch-divergence" suspicion is confirmed rather than open, and S5 is the piece of it
that has been pinned down.

## The local pack IS layer 4 — the rationale, corrected

An earlier note in this file conceded that a local pack needing a manifest is "just a pack in an
awkward location." **The maintainer pushed back and was right:** *"we need somewhere for user
contributions to go, so where else could they go now that we control briefings and skills
completely?"*

The manifest-avoidance argument was the weak one. The load-bearing argument is that yolo now owns
`~/.claude/skills` and `~/.claude/CLAUDE.md` wholesale, so **a user contribution has nowhere else
to live.** "Commit it to a repo pack" is not an answer for a half-baked skill, a machine-specific
one, or scratch space you do not want in git.

The jail already had this slot — layer 4, "the user's OWN skills tree, written last so a
same-named local skill wins". The local pack is not a new concept; it is **that slot given a home
yolo does not overwrite.** Which is also why S3 was a defect and not a design choice: layer 4
pointing at the destination was how the slot worked before it had a home.

**SHIPPED, and the conclusion is stronger than "repoint layer 4".** The local pack does not just
FILL the slot, it IS the slot: as an ordinary pack entry appended last by `config.LoadPacks`, it
already sits in the pack layer with exactly the precedence layer 4 had. So the fix was to DELETE the
fourth layer, not repoint it — repointing would have left the local pack arriving twice, which is
the double arrival S3 was about. And S1/S2 removed the manifest need as predicted: no tier
declaration, no split-brain, no `pack.json`.

## N1 — the nix profile has no gcroot 🔴

**Verified 2026-08-05:** `rg -n 'gcroot' flake.nix internal/darwinpkg internal/macosuser` finds
nothing but the image's own `/nix/var/nix/gcroots` directory creation. So the `buildEnv` profile
that `packages:` materializes is **not rooted**, and `nix store gc` can collect it out from under
a launch that depends on it.

This is the only live defect in the non-container nix area, and it is independent of every option
in N3 — whichever provisioning story you pick, an unrooted profile is wrong. Fix is
`nix-store --add-root --indirect` (or the flake-era equivalent) at materialization time, plus
somewhere for the root to live under `paths.GlobalStorage()`.

**Do this one first**, because it is a real failure mode today and needs no ruling.

## N2 — the mechanism is already cross-platform; only the NAME is darwin

Checked all four sub-items on 2026-08-05:

| Sub-item | State |
|---|---|
| `yoloDarwinPackages` → system-neutral name | ❌ `flake.nix:994` |
| Un-hardcode `aarch64-darwin` in the caller | ❌ `internal/darwinpkg/darwinpkg.go:19` `DarwinSystem = "aarch64-darwin"` |
| Add a gcroot | ❌ (that is N1) |
| `describe` / `check --at host` report the resolved profile path | ❌ not reported |

The mechanism itself is fine: `flake.nix:303` confirms `lib.meta.availableOn` is a per-system
predicate, and the package set resolves for `x86_64-linux`. **The name is the lie, not the
mechanism.** The single caller is `internal/cli/commands.go:369`, reached only from
`macosUserRun`, which is why nothing else can use it today.

**This is a prerequisite for env-manager Phase 7.2, not an option** — `guest` is a real home with
no image, so it needs a tool closure for exactly the reason `host` does. See
[`handoff-guest-notch-macos.md`](handoff-guest-notch-macos.md) §5.

**Same bug class as BACKLOG E8**, which was fixed 2026-08-03 and turned out to have **three**
instances of one hardcoded-arch assumption rather than the one its entry named — found only by
grepping the literal instead of trusting the entry's scope. So: `rg -n 'aarch64' internal/ flake.nix`
before assuming `darwinpkg` is the only site.

## N3 — non-container nix: the part that is YOUR decision

Full study: [`../design/noncontainer-nix-environment.md`](../design/noncontainer-nix-environment.md).
Its §7 is the load-bearing finding — a tool environment is **not** orthogonal to confinement; it
is the missing provisioning primitive below the `jail` notch, and `guest` needs the identical
mechanism. Its §8 offers four options; N1 and N2 are Option 1 and are queued above regardless.

What is left to decide:

- **Option 0** — stop here. `install_hints` keeps printing a remedy the user runs by hand.
  Honest, and leaves `install_hints` covering 0–1 of six agent CLIs on a non-Arch Linux host.
- **Option 2** — `yolo --at host -- <cmd>`, the design's own §4.1 escape valve. Verified
  2026-08-05 that `--at` is `apply`-only (`internal/cli/apply.go:54`). This is the shape the
  codebase is already built for, and it would make `env` and `launch` renderable at the host
  immediately — see the D3 note in the shipped doc: those two kinds are refused for a reason
  that is true of `apply --host` and false of the notch. **Against:** "yolo launches your host
  agent" is a bigger product claim than "yolo configures it."
- **Option 3** — a yolo-owned `nix profile` as a confirm-gated Phase 4.3 install remedy, so the
  install story has a reproducible option beside `brew install`.

**My recommendation:** N1 + N2 now (they are not optional), then Option 2 — because it is the one
that unblocks two refused kinds rather than adding a mechanism. Not started; awaiting your call.

## P7 — the `guest` notch

Not built, host/Mac-gated rather than design-blocked. Everything the batch shipped was in service
of making this addable rather than bolted on:

- `render.KindGuest` exists with no behavior, so a `switch` on Kind must handle it.
- `render.UndecidedModes(reason)` is guest's mode census — fail-closed, with a test that tells
  Phase 7 to rewrite rather than delete it.
- `render.GuestProfileMacOS()` / `GuestProfileLinux()` are the primitive presets, and `describe`
  now prints the vector.

Full handoff, including the bug to fix first (macos-user renders ZERO pack surfaces, silently):
[`handoff-guest-notch-macos.md`](handoff-guest-notch-macos.md).

---

## E1 + E2 — the `host_files` mode collapse

Still four modes (`internal/config/hostfiles.go:59-61`: `readonly`, `copy`, …). The proposal
([composed-file-permissions.md §7.4](../design/composed-file-permissions.md)) collapses to three:
`copy` merges into `readonly`, and `readonly` becomes a real `:ro` mount rather than a `0o444`
chmod.

**Why `0o444` is the wrong mechanism:** it is **asymmetric**. Root ignores it; a non-root agent
gets `EACCES` — and the failure is silent, because the surface simply stops re-rendering. The same
declaration means "advisory" for one user and "broken" for another.

**E1 cannot land first.** Two reasons, the second being the real one:

1. `host_files` is a shipped key, so changing what a mode means changes existing configs.
2. **You cannot compose *into* a `:ro` mount.** Every current `readonly` user must be classified
   as either a surface yolo renders or a file yolo mounts read-only — a per-surface judgment call,
   not a coding problem. That is the E2 design pass.

Note `:ro` is only *non-persistence*, not confidentiality. Do not let the collapse imply a
security property it does not have.

## E3 — capture on terminate (half shipped)

`yolo config capture` **exists** (`internal/cli/config.go:112`) — that was the cheap half and the
one that names the mechanism to a user who did not know capture existed. What remains: capture in
the existing `onTerminate` hook, so the observability window closes without an explicit command.

**Nothing is lost today**, which is why this stays small: every surface sits under a host-backed
rw bind and its baseline lives in the workspace, so both survive `--rm` and the next boot captures
normally. What lags is *observability* — a host-side `yolo config diff` inside the window
under-reports.

**Explicitly rejected: an inotify watcher.** It solves staleness, not loss, at the price of
debounce plus a sidecar race, and the cost lands on the boot path.

## E4 — comment preservation

A `json`/`toml` surface is re-emitted canonically, so comments do not survive. Already *reported*
before the write as a `Formatting` line (`internal/entrypoint/hostrender.go:113`), so it is
visible rather than silent.

The three sub-questions — staleness, attachment, in-jail additions — are answered in
`host-file-staging.md`, so this starts from decisions. `raw` is already lossless. **The cheapest
useful step needs no comment parsing at all:** a yolo-authored header pointing at the `:ro`
original.

## E5 — array-append pinning

Object merge only; arrays replace wholesale, matching RFC-7386 and the render engine's `deepMerge`.
No user surface has needed it, and a config expecting append semantics fails **loudly** at
shape-check time, which is what makes waiting safe.

**The one item to name as do-not-build-speculatively.** An append mode is a second merge semantics
for the same field, and the layer model's legibility is its main asset.

---

## V1 — reserved destinations miss symlink aliases

`~/.config/git/config`, `~/.config/bashrc` and `~/.claude/claude.json` validate while their aliases
are rejected. `internal/config/helpers.go:87-95` resolves symlinks lexically with an
`EvalSymlinks` fallback, but the reserved-destination guard (`writablehome.go`) matches on the
declared path. Tracked at
[composed-file-permissions.md §4.5](../design/composed-file-permissions.md).

## V2 — `apply --host` is not whole-home idempotent until apply 3

Found while verifying Q6, and **pre-existing** — reproduced against a binary built from before that
work. A surface's provenance record classifies a key `default` on apply 1 and `host` on apply 2,
converging from apply 3. Not skills (skills and the local pack are byte-identical from apply 2);
it belongs to `config`. Harmless today, but it means "a second apply is a no-op" is not yet true
of the whole home, and any future test asserting that will be flaky.

## V3 — pack-set archives land under `archive/skills/`

`internal/cli/applyhostskills.go:78` puts every archive under
`<state>/archive/skills/<stamp>/<pack>/…`, including `files` and briefing content. The path implies
a skill was archived. Cosmetic, but renaming it orphans existing archives from `yolo prune`'s
sweep, so it needs a migration rather than a rename — which is why it is recorded rather than
already done.

---

## C1 — drop the redundant `"from"` literals from the shipped packs

`from` is now optional for `skills` and `briefing` (defaults `skills/`, then
`AGENTS.md`/`CLAUDE.md`), but all six shipped `pack.json` files still declare the literal the
resolver would have supplied. Removing them exercises the default and shortens the reference
examples. **Check the render fingerprint when you do** — it should not move, and if it does, the
default is not resolving the way the validator now permits.

## C2 — the briefing's per-notch prose should read the Profile — DONE

`internal/agents/agentsmd.go` switches on the notch NAME to pick its prose. Q10 deliberately left
it: the text genuinely differs per notch and a human reads it, so it is a legitimate boundary. But
it would be **better** reading the Profile — describing the primitives the notch composes plus the
autonomy bit — because then the prose is accurate for a notch nobody has enumerated yet. Same
argument that motivated `KindGuest`. Deferred at the time only because another agent held that
file.

**Split the way the item suggested:** the name still picks the title and the framing sentence
(that prose genuinely differs, and no generated line says "this is the human's REAL machine" as
usefully), while the two facts an agent most needs — the enforcement vector and the
`AgentAutonomy` bit — are derived from `render.ProfileFor`. The payoff is the DEFAULT branch: the
old name-switch fell through to the jail text, so any notch nobody enumerated was told it was in
"a sandboxed container" — for anything below jail, exactly the falsehood the notch line was added
to prevent. It now echoes the configured name and describes its real (fail-closed) vector.

**The fingerprint did NOT move, and the spec's premise about it was wrong.** The briefing is not
in the gate at all: `TestRenderFingerprintStable` covers the 10 files `ConfigurePackByName`
renders (config surfaces only), and mutating the jail's briefing line to a literal `MUTATED JAIL
BRIEFING LINE.` leaves the fingerprint test green — `internal/agents`'s own tests are what catch
it. The jail's bytes are unchanged regardless, pinned as a literal by
`TestBriefingJailHeaderIsUnchanged`, because every jail that boots renders that header.

**Vocabulary deduplicated rather than mirrored.** `primitiveOrder`/`primitiveDoes` moved from
`internal/cli/describe.go` into `internal/render` as `PrimitiveOrder()`/`PrimitiveDoes()`, with
`describe` and the briefing both reading them — two wordings for one primitive drift, and a reader
who hits the disagreement cannot tell which is current. The "every primitive is ordered and
described" invariant moved with the table.

**The `netMode == "host"` trap is pinned.** `TestBriefingNetModeHostIsNotTheHostNotch` asserts
both directions: a host-NETWORKED jail keeps the container framing, and a host-NOTCH environment
keeps bridge networking. Mutating `confinementHeader(in.Confinement)` to
`confinementHeader(netMode)` fails six tests.

## C3 — `packoverlay.Collect`'s autonomy parameter — DONE

Three of four autonomy literals are gone. `Collect` was already parameterized, and its three
callers each pass a literal — so converting it without deriving those callers' values from a
`Target` just moves the same literals one frame out. §6c's remaining half; small, and worth doing
when someone is next in those three callers.

**Only ONE literal was actually left** when this was picked up: `packsurfaces.go` already read
`e.renderTarget().Profile().AgentAutonomy` and `configdiff.go` already read
`render.ProfileFor(notch).AgentAutonomy`; the item's "three callers each pass a literal" was
stale. `apply.go` now derives from `render.Host(home, nil).Profile()`, so no autonomy literal
survives at any call site.

**The mutation SURVIVED at every caller, and that is the finding.** Inverting the argument
leaves the whole suite green — because the parameter has no observable effect on `Collect`'s
output at all: the posture fold (`packload.foldPostureManaged`) merges keys into the `Managed`
layer of surfaces already declared and ignores a patch naming no base surface, so both postures
yield the same surface-identity set, and identities are all `Collect` reads. So the survival is
a property, not absent coverage — but the two are indistinguishable without a test that says
which, and `internal/packoverlay/autonomyinert_test.go` now pins it: if a posture ever gains the
power to add or remove a surface identity, the parameter starts deciding which overlays find an
owner and that test fails at that moment. Verified by mutating the fold to promote patches to
declarations, which fails it. Behavior was probed in both directions: `apply --host --assert`
still writes the guarded posture (`defaultMode: "default"`, `additionalDirectories: []`,
`skipDangerousModePermissionPrompt: false`) and a jail render still writes `acceptEdits` /
`skipDangerousModePermissionPrompt: true`.

---

## Suggested order

1. ~~**S1**, **S2**, **S3**~~ — SHIPPED 2026-08-05, in that order (S1 first, which is what turned
   S3's double arrival from invisible into an error).
2. **S5** (a jail resolves a collision silently) — the notch S1 did not reach, and now the only
   place the silent loss survives. The warning half needs no ruling; the refusal half does.
3. **S4**'s audit. Cheaper now: S1 makes a delivery-vs-selection mismatch loud at the host, and S5
   already confirmed the notch-divergence half of probe 3.
4. **N1** (gcroot) — a live defect, no ruling needed.
5. **V1** (symlink aliases) and ~~**C1**~~/**C3** — small, independent, no decisions.
7. **N2** — mechanical, and unblocks P7.2.
8. **Your call on N3**, which decides whether Option 2 joins the list.
9. **E2's design pass**, then **E1**.
10. **E3**'s terminate half and **E4**'s header step.
11. **P7** when you are on a Mac. **V2/V3/C2** as anyone passes through.
12. **E5** only when a real surface needs it.
