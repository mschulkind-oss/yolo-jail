# The claude-shaped code still in core — and how packs become truly separate

**Status:** DIAGNOSIS SETTLED, DECISIONS TAKEN, NOTHING BUILT. 2026-08-15.

**All four open questions are answered** (see Open Questions), and the answers moved the design: the broker **becomes shippable** rather than staying bundled, the credential harvest is **deleted** rather than generalized, and `internal/agents` **is** renamed. Prose written under the old leanings is marked superseded in place rather than silently rewritten, so the argument that lost is still readable.

**Two annotation kinds below.** `> **REVIEW —**` blocks are a second agent's review comments (tags: **AGREE** · **DISAGREE** · **GAP** · **NIT**); `> **DECIDED —**` blocks are the maintainer's rulings from that review round, and they win over both the original prose and the review. Everything else is small. Verdict on the whole: the diagnosis is right and well-evidenced, §4 and OQ-1/OQ-4 should be signed as written, and §5 + OQ-2 + OQ-3 need the rework described below.

**The short version.** "Core does not know what an agent is" is true of the assembly layer — packs declare paths/kinds/hooks/surfaces and core renders them with no switch on a tool name. It is *not* true of a small stratum of imperative Go that is still claude-shaped: the OAuth broker's daemons are baked into `yolo`/`yolo-jaild`, the `shared_credentials` hook's harvest body hardcodes `claudeAiOauth`, and a few storage/migration helpers are named for and keyed on claude. This doc inventories that residue, proposes how each piece either becomes generic or moves into claude's own pack, and — for the broker — sketches the extension point that would let it ship externally.

**Reads with:** [`loophole-packaging-overview.md`](loophole-packaging-overview.md) (the `loophole` kind and the framework-owned wire — the extension point this doc leans on), [`loophole-packaging.md`](loophole-packaging.md) (the detailed design, OQ-LP10/OQ-LP11/OQ-LP14), [`pack-capabilities.md`](pack-capabilities.md) (the `serves`/`supersedes` vocabulary the broker already uses), [`agent-credentials.md`](agent-credentials.md) (what crosses the boundary today).

---

## 1. Verdict up front

The residue is real but smaller and more tractable than "there's claude everywhere" suggests. Three of the five categories are already generic *in body* and only claude-shaped *in name* (`internal/agents`, `hostclaude.go`, the host-file mounts). The two that are genuinely claude-shaped are:

1. **The OAuth broker subsystem** — `internal/oauthbroker`, `internal/oauthterminator`, `internal/broker`, `internal/brokerrelay`, plus three baked daemon subcommands. This is Claude-specific by *nature* (Anthropic mints single-use refresh tokens), and it is the one piece where "separate it" is a real architectural question, not a rename.
2. **The `shared_credentials` hook body** — `internal/entrypoint/claude.go`'s `harvestCredentialsFile` hardcodes `claudeAiOauth`. The hook is generically *named* but claude-shaped *in body*, and my agy change is the first time a second tool consumed it, which is exactly what exposed the seam.

My recommendation: **make `shared_credentials` genuinely generic** (symlink + copy-if-empty, no schema-specific merge), **move the claude-specific OAuth merge into the broker's own code**, and **treat the broker as the one remaining "extension point" question** — where the honest answer may be "keep it bundled, but document the seam" rather than "force it external."

> **DECIDED — the verdict after the review round.** The first clause holds; the other two do not. The hook becomes genuinely generic, but by **deleting** the merge rather than relocating it (§5) — so nothing moves into the broker's code, and OQ-2 dissolves. And the broker is **not** kept bundled: the extension point gets built and the broker ships as a pack (§4, OQ-1). Two more rulings the original verdict did not anticipate: `check`'s claude-specific freshness check moves behind the `doctor_cmd` extension point that already exists, and every remaining claude *name* in core is renamed out rather than tolerated (§3.3, §6). Net effect on the categories above: category 2 is deleted, category 1 leaves core, and the "generic in name only" three are renamed — which is the whole residue.

---

## 2. What "core doesn't know an agent" means — and its boundary

The claim is real and is the dominant design. Verified against the code 2026-08-15:

- `internal/packdecl` is the manifest schema; `internal/packload` reads it; `internal/entrypoint/packsurfaces.go` renders every pack in one loop with no switch on tool name (`AGENTS.md`: "the boot path renders every one in a single loop … with no switch on any tool name").
- Adding agy was a `pack.json` (`packs/agy/pack.json`), not a Go change. The `agentcfg` surfaces, the mount assembly (`internal/cli/run/assemble.go`), and the host-file/mount grants (`internal/cli/run/hostclaude.go` — see §7) all key on paths and kinds.
- `internal/agents/agents.go:9-18` states the registry is gone: "All of it is now pack DATA … Core does not know what an 'agent' is."

The boundary is the **imperative residue**: the things a pack needs done that are *not* surface content and therefore cannot be declared as layers. `internal/entrypoint/packhooks.go:3-26` is honest about this — it names three hooks and says "all currently claude's." The hooks are a **closed set** (`packdecl.KnownHooks`), and a third-party pack cannot ship a new side effect. That closed set is the design's own admission that "no agent in core" stops at the point where a side effect is tool-specific.

So the accurate framing is: **the assembly layer achieved it; the imperative layer has not, because only one tool ever needed those side effects.** The work is to finish the imperative layer — either by generalizing what is genuinely generic, or by relocating what is genuinely claude-specific.

> **REVIEW — NIT, and this doc's own thesis in miniature.** `packhooks.go:6` says the three hooks are "all currently claude's". That went stale one commit *before* this doc (ab39897): `packs/agy/pack.json:98` declares `shared_credentials`. The comment is quoted approvingly here rather than corrected. One line to fix, and it is the exact seam §3.2 is about.

---

## 3. Inventory: the claude-shaped code in core

Five categories, with evidence. The first two are substantive; the last three are mostly naming.

### 3.1 The OAuth broker subsystem (genuinely claude-specific)

The broker exists only because Anthropic's refresh token is single-use (`docs/design/agent-credentials.md` §2.5). It is *declared* as a loophole (`bundled_loopholes/claude-oauth-broker/manifest.jsonc`) but *implemented* in core:

| Piece | Where | Baked as |
|---|---|---|
| host singleton (flock + shared creds file) | `internal/oauthbroker` | `yolo internal daemon claude-oauth-broker` (`internal/cli/internal.go:122-123`) |
| per-jail relay | `internal/brokerrelay` | `yolo internal daemon broker-relay` (`internal/cli/internal.go:126-127`) |
| in-jail TLS terminator | `internal/oauthterminator` | `yolo-jaild oauth-terminator` (`cmd/yolo-jaild/main.go:32`) |
| lifecycle/`yolo broker` command group | `internal/broker` | `internal/broker/brokerlifecycle.go` |

The manifest's `host_daemon.cmd` is literally `["yolo", "internal", "daemon", "claude-oauth-broker", "--socket", "{socket}"]` and `jail_daemon.cmd` is `["yolo-jaild", "oauth-terminator"]` (`bundled_loopholes/claude-oauth-broker/manifest.jsonc`). So the loophole *declaration* is a pack, but the *daemon* is a core subcommand. This is the exact inversion the user is pointing at: "code that is only part of a loophole, namespaced along with the core go stuff."

`loophole-packaging-overview.md:86` already names the blocker, and it is the strongest one: **"the broker's manifest is not what runs — its real spawn is reconstructed in Go, and its per-jail relay has no manifest vocabulary at all."**

### 3.2 The `shared_credentials` hook body (generic name, claude body)

`internal/entrypoint/claude.go` holds `linkThroughShared` and `harvestCredentialsFile`. The *symlink* half is generic; the *harvest* half hardcodes `claudeAiOauth`:

- `oauthTokenKeys` / `oauthMetadataKeys` (`claude.go:20-21`) — `accessToken`/`refreshToken`/`expiresAt`, `scopes`/`subscriptionType`/`rateLimitTier`.
- `harvestCredentialsFile` (`claude.go:65-141`) merges the local file's `claudeAiOauth` dict into the shared one, newest-`expiresAt`-wins.

agy's token is `{"token":{access_token,refresh_token,expiry},"auth_method":"consumer"}` — no `claudeAiOauth`, so `harvestCredentialsFile` returns `false` and agy falls through to the generic copy-if-empty path in `linkThroughShared` (`claude.go:47-55`). That fallback is the *actually generic* behavior; the merge is the claude-specific part.

There is also `claude_plugins` (`packhooks.go:55`), which is *named* for the tool and shells out to `claude plugins install/uninstall` — the doc comment admits it deliberately (`packhooks.go:49-54`).

> **REVIEW — GAP.** The agy fall-through is described accurately, but its consequence is not named, and it is a defect rather than a curiosity. With `harvestCredentialsFile` returning false and the shared file already populated, `linkThroughShared` (`claude.go:46-58`) takes *neither* branch: it does not copy, then it **removes the local file** and symlinks to the shared one. So a fresh `agy` login performed in one jail works for that session and is silently reverted at the next boot, while claude's `expiresAt` merge saves the identical case.
>
> This is conditional on the same unverified premise §5 leans on — that a tool ever leaves a real file where the symlink was — which is *why* the observation in the §5 comment matters: one check settles both the cost of §5 and whether agy can lose logins.
>
> **Second, and worth stating plainly: the agy path has never run.** The pack staged into this jail (`/ctx/packs/_official/agy/pack.json`, from the baked binary's embed.FS) carries no `shared_credentials` hook — the commit that added it (ab39897, 21:39 today) postdates this jail's boot (14:52). Consistently, `~/.gemini-shared-credentials/` does not exist and no `antigravity-oauth-token` symlink was created, though the hook `MkdirAll`s both parents unconditionally (`packhooks.go:120-125`). So "a second tool consumed the hook" is a **code-reading** result, not an observed one, and the first agy login in a jail booted after ab39897 is the moment to watch. That is not a criticism of the analysis — it is right — but it means nothing has exercised the fall-through, and §5 would be rewriting a path with zero runtime evidence behind it.

### 3.3 `internal/agents` (already generic in body)

`internal/agents/agents.go` is now only skills staging, briefing composition, loophole descriptions, and the source-tree probe — "the stuff that was never per-agent." The claude references are in comments (`agentsmd.go:3`, `skills.go:52`) and tests (`skills_test.go`), not in a claude-specific switch. This is a **rename**, not a redesign.

> **REVIEW — NIT, in the doc's favour.** Weaker than stated. Non-test `internal/agents` has exactly **one** claude mention (`skills.go:52`); the other citation, `agentsmd.go:3`, is the filename `CLAUDE.md`.
>
> **Correction, and it cuts the other way.** I first called `CLAUDE.md` "a filename several tools read". That is wrong: `AGENTS.md` is the cross-tool convention, `CLAUDE.md` is Claude Code's own. And there is a mention this section misses entirely — `packdecl.DefaultBriefingFiles()` (`contributes.go:385`) hardcodes `["AGENTS.md", "CLAUDE.md"]` as the conventional briefing source pair that **every** pack's `briefing` contribution falls back to, read at `packload/briefingsource.go:144` and `packload/mergedest.go:223`. So core does name a claude file, in the schema package, for all packs. Its defence is in its own doc comment — *"both names are in the wild and a pack author should not have to know which one yolo happens to read"* — which is a real argument that the pair is a **convention**, not a claude special case. It is the one mention that needs a ruling rather than a rename.

> **DECIDED — rename them all out.** Every claude mention goes unless it is genuinely generic — and "copilot implements claude's plugin format" is the shape of the exception, which should live in a pack anyway, not in core. So: rename the package (§6), fix `skills.go:52`, and rule on `DefaultBriefingFiles` rather than leaving it unexamined.

### 3.4 Storage migration / back-compat (claude-keyed)

- `internal/storage/claudejson.go` — `SyncClaudeJSONSeed` back-propagates `oauthAccount`/`hasCompletedOnboarding` from a workspace `claude.json` into the `GLOBAL_HOME` seed. This is the one non-broker write-back-to-`GLOBAL_HOME` path (`agent-credentials.md` §4 note).
- `internal/storage/ensure.go:72-83` — migrates `.claude/.credentials.json` → `.claude-shared-credentials/.credentials.json`.
- `internal/cli/run/prepare.go:259-268` — `syncClaudeJSONSeed` + `migrateOldOverlay` (`claude-projects` → `claude/projects`, `claude-settings.json` → `claude/settings.json`).

These are **historical-layout migrations** for claude specifically. They are inherently tied to one tool's past file layout, not to "agents."

> **REVIEW — answering "is this the pre-packs builtin?"** Half. They *do* predate packs — all three arrived with the Go port (`326abbe`, Stage 14 storage slice, and `5e9b1c8`, Stage 16, both 2026-07-18), which ported them forward from the Python implementation, where claude was the only agent there was. But **none of them migrates a builtin→pack transition**, because the pack reform changed no on-disk path: it changed who *declares* a path, not where the bytes live. What they actually migrate is two earlier **storage-layout** changes — the shared-credentials dir (`~/.claude/.credentials.json` → `~/.claude-shared-credentials/`) and the per-workspace overlay reshuffle (`claude-projects` → `claude/projects`). So they are claude-shaped for the boring reason that claude was the only tool with state when the layout moved, and the delete-by date is "when no live workspace still carries the old layout", not "when packs land".

### 3.5 Host-side helpers (generic body, claude name)

`internal/cli/run/hostclaude.go` is named for claude but is fully generic — it reads `p.HonoredHostFiles()` / `p.HonoredMounts()` from pack declarations (`hostclaude.go:21-47`). The filename is a fossil. Same for `internal/cli/check/sections_misc.go:19-79` (`checkBrokerCredsFreshness`), which is claude-specific but *is* the broker's freshness check, so it belongs with §3.1, not here.

> **DECIDED — rename it.** `hostclaude.go` → `hostfiles.go`. Nothing about the file is claude-specific and the name is the only thing suggesting otherwise.

---

## 4. The broker — the extension point, and how to ship it externally

The `loophole` contribution kind already exists (`packdecl/kinds.go:135`, `KindLoophole`) and already lets a pack ship a loophole whose daemon is **any language** — the framework owns the wire, and a third-party daemon only binds a plain Unix socket (`loophole-packaging-overview.md` §3). So "ship a loophole externally" is *already* possible for the general case.

What is **not** possible is shipping the *broker* externally, for two reasons that are both about the daemon being a baked subcommand rather than a shippable binary:

1. **`jail_daemon` runs inside a read-only image.** `yolo-jaild oauth-terminator` works only because `oauth-terminator` is baked into the jail image. A pack-shipped binary would have to be mounted `:ro` from the loophole module dir and be executable with a runtime the image bakes — which is the "native binary" question `loophole-packaging-overview.md` §3.3 names and does not design.
2. **The per-jail relay has no manifest vocabulary.** The relay (`internal/brokerrelay`) is spawned by `loopholes_runtime._relay_ensure` (the Go port's `loopholesruntime.go`), not described by any manifest field. `loophole-packaging-overview.md:86` calls this out as the strongest reason the broker stays bundled.

The honest sketch of "shipped externally" for the broker is therefore:

```
acme-claude-broker/                    # a fetched or local pack
  pack.json                            # { "kind": "loophole", "from": "loophole/broker" }
  loophole/broker/
    manifest.jsonc                     # name, serves: ["claude-oauth-refresh"],
                                       # host_daemon.cmd → the pack's own binary,
                                       # jail_daemon.cmd → the pack's own binary,
                                       # publishes: "socket" (framework owns the wire)
    bin/
      brokerd                          # host singleton: flock + shared creds file
      terminator                       # in-jail: TLS front, dials the relay
```

Two things have to be true before that is more than a sketch, and both are the *extension point* the user asked to name:

- **A jail-side daemon must be shippable as a binary in the module dir**, mounted `:ro` and executable. This is `OQ-LP14`-adjacent (the "missing vocabulary" for a native transport) but narrower: it is not a new transport, just "the jail daemon may be a file in the module, not a baked subcommand."
- **The relay must either be expressible in the manifest or be folded into the framework-owned front.** The front (`loophole-packaging-overview.md` §3.1) already does the loopback-TLS termination the relay does; the relay's remaining job is the per-connection dial to the singleton. If the front can dial a `host_daemon` that `publishes: "socket"`, the relay disappears and the broker becomes an ordinary pack-shipped loophole.

> **DECIDED — build both.** This stops being a named-but-unbuilt extension point: **jail-daemon-as-binary** and the **relay/front folding** are the work, and the broker becomes an ordinary pack-shipped loophole on top of them. That answers OQ-1 *shippable* and settles OQ-4 with it (below).
>
> Note what this buys beyond the broker: the second bullet's folding **deletes** `internal/brokerrelay` rather than relocating it, because the framework-owned front already does the loopback-TLS half and the per-connection dial is the only remainder. So the shippable broker is a net *subtraction* from core, which is a better trade than "same code, new home".

~~The alternative — and my current leaning — is that **the broker stays bundled, but the seam is made explicit**: the claude-specific merge (§3.2) moves *into* `internal/oauthbroker` (where the shared creds file is already owned), and the generic `shared_credentials` hook keeps only the symlink+copy-if-empty. "Bundled" is not the sin; "claude-specific logic living in a generically-named hook" is.~~

> **SUPERSEDED by the decision above** (OQ-1 = shippable). Kept because its second half survives its first: the merge *is* being removed from the generically-named hook — it is simply being deleted outright rather than relocated (§5), so there is nothing left to move into `internal/oauthbroker`.

> **REVIEW — one addition, and I drew the wrong conclusion from it.** The observation stands: `check`'s `checkBrokerCredsFreshness` (`internal/cli/check/sections_misc.go:19-99`) parses `claudeAiOauth` *and* hardcodes the broker's own error strings, so it is a third copy of the schema knowledge and a claude-specific check sitting in core. I filed that as a third reason to keep the broker bundled. That was backwards, and the maintainer's correction is right —
>
> **DECIDED — this is proof that doctor additions want an extension point, not proof of bundling.** And the extension point **already exists**, which makes the residue worse than the review described rather than better: `doctor_cmd` is a manifest field (`loopholes/load.go:191-238`), `RunDoctorChecks` executes it behind the origin and placement gates (`loopholes/runtime.go:328-386`), `yolo check` already runs it (`check/sections_loopholes.go:60`), and **the broker already declares one** — `["yolo","internal","daemon","claude-oauth-broker","--self-check"]` (`manifest.jsonc:68`). So `checkBrokerCredsFreshness` is core re-implementing, in Go, a check the loophole could already deliver through its own manifest.
>
> Two consequences: the freshness grading moves behind the broker's `--self-check` (`oauthbrokercmd.go:33-53`), deleting a claude-specific section from `check`; and this **removes** an obstacle from the ship-externally list rather than adding one, since a pack-shipped broker inherits the same `doctor_cmd` seam every other loophole has. What is genuinely missing is only the richer *output* shape — `DoctorResult` grades pass/fail, while the current check emits a remaining-lifetime warning.

---

## 5. `shared_credentials` — generic, or moved

The hook's *contract* is generic and worth keeping: **"symlink this file into this machine-scoped dir, harvesting a real file first."** Both claude and agy need exactly that. What is not generic is the *harvest*: claude's merge is keyed on `claudeAiOauth` + `expiresAt`, agy's token is a different schema, and a future tool would be a third.

Proposal, in two moves:

1. **Make the hook generic.** `linkThroughShared` keeps: already-correct-symlink → done; real file + empty shared → copy local into shared; real file + populated shared → leave shared (no merge); then symlink. Delete `harvestCredentialsFile`'s `claudeAiOauth` merge from the generic path.
2. ~~**Relocate the merge.** The "newest-token-wins" merge of `claudeAiOauth` belongs with the thing that owns the shared creds file — `internal/oauthbroker`. It becomes a broker-internal concern (or a claude-specific hook if a second claude-specific side effect ever justifies one).~~ **Superseded — there is no merge to relocate; see the ruling below.**

> **DECIDED — the shared file always wins. Delete the harvest.** Losing one valid login in a migration is acceptable, so the rule needs no freshness comparison of any kind, in any schema:
>
> ```
> already the right symlink        → done
> real file + EMPTY shared         → copy local into shared, then symlink
> real file + POPULATED shared     → discard local, then symlink
> anything else                    → symlink
> ```
>
> That is move (1) exactly, minus the mtime rule the review proposed and minus the relocation in move (2). `harvestCredentialsFile` and `expiresAtMs` (`claude.go:63-199`, ~135 lines) are **deleted**, not moved, and with them the entrypoint's only `claudeAiOauth` knowledge and the one sanctioned tmp+rename in the boot path. The copy-if-empty branch stays because it is not a freshness rule — it is what makes a first login in a fresh install survive.
>
> **The one failure mode to accept knowingly**, since it is not the migration case: if the shared file holds a *revoked or expired* credential and a jail logs in again, that fresh login is discarded at the next boot and the jail is back on the dead token. Re-login fixes it until the next boot, so the exit is `rm` the shared file, not a code change. Worth a line in the hook's doc comment so the next person reads it as designed rather than broken.
>
> One thing must survive the deletion regardless — see §8: the **unconditional** `subscriptionType` / `rateLimitTier` preservation. Under "shared always wins" it survives for free (nothing overwrites those keys any more), but only if the broker keeps preserving them on refresh, which `NormalizeOAuth` does (`oauthbroker.go:196-200`, copy-previous-then-override). Pin it with a test rather than an argument.

The cost of (1): claude loses the boot-time merge of a *stale* local file into a *fresher* shared file. That case only arises when a jail holds a real (non-symlink) credential file while the shared file is already populated — a migration edge, not steady state, and the broker's flock already serializes the refresh that matters.

> **REVIEW — DISAGREE (BLOCKING) — RESOLVED by the ruling above; kept for its evidence.** The objection was that the cost estimate was unmeasured. The ruling moots the objection by accepting the cost outright, which is a stronger answer than measuring it. The measurement below stays because it is the only runtime evidence anyone has about this file, and §10 no longer needs it as a gate.
>
> That cost estimate rests on an empirical claim nobody has verified: *a real (non-symlink) file at `link` only appears on a migration edge.* Nothing in the repo establishes it. Every doc phrases it as a **pre-existing** regular file ([`jail-home.md`](jail-home.md) §4.2, [`agent-credentials.md`](agent-credentials.md)'s code map) — which is the first-boot story — but `jail-home.md` §4.1 also records that Claude atomic-renames `~/.claude.json`, and a rename over `~/.claude/.credentials.json` would replace the symlink with a real file **in steady state**. `docs/research/claude-oauth-refresh-mechanics.md:44` lists "Claude itself after a successful refresh" as a writer of that file and does not say which write mode it uses. Nobody has checked.
>
> **The observation that settles it, and the part of it I made.** Measured in this jail, 2026-08-15: `~/.claude/.credentials.json` is **still a symlink**, `lrwxrwxrwx … -> ../.claude-shared-credentials/.credentials.json`, with an mtime of **2026-08-05 21:39** — while its target was rewritten **today at 21:06** (555 bytes). So across ten days and many boots, with tokens visibly turning over, nothing has replaced the link with a regular file. That is real evidence **for** the author's position.
>
> **What it does not settle**, and this is the part left to check: with the broker active, the host daemon writes the *shared* path directly (`oauthbroker.WriteTokens` does tmp+rename on the shared file, never on the link), so this observation cannot isolate whether *Claude itself* ever rewrites the link. `claude-oauth-refresh-mechanics.md` §6.4 says Claude's own refresh path is deliberately retained. The case that matters is therefore a machine where the broker is **inactive** — its `requires.command_on_path: claude` is false, so a host without Claude Code installed runs no broker — or any refresh Claude performs itself. Watch the same `ls -l` there before deleting the harvest.
>
> ~~**And I would flip the leaning regardless of how it lands.** … a `local.mtime > shared.mtime → copy local` rule …~~ **Withdrawn.** The mtime rule was argued on the premise that discarding a fresh login is a defect worth five lines to avoid. The maintainer's ruling rejects that premise directly — the loss is acceptable — and with the premise gone the rule is just an extra branch that can be wrong in its own way (the broker rewrites the shared file on every background refresh, so its mtime is fresh even when its *account* is the stale one; mtime would then keep the wrong credential with more machinery than keeping it deliberately). The simpler rule is the better one here, and the review was arguing for a cost the maintainer had already priced.

---

## 6. `internal/agents` — what's actually left

Nothing claude-specific in *body* remains. The package is a home for "content agents read" (briefing, skills staging, loophole descriptions, source probe). The fix is cosmetic: the package name and a handful of comments/tests still say "claude" where they mean "the briefing/skills targets." This is a rename-and-comment pass, not a design decision — it does not need an open question, only a decision to do it.

> **REVIEW — NIT.** See the §3.3 comment: "a handful of comments" is one comment, and it is not this. ~~My read is that the right decision is *don't* — leave the package name alone.~~ **Overruled, and the overrule is right:** a name is what the next reader searches for, so "one mention" is an argument about cost, not about whether the name misleads. It does.
>
> **DECIDED — do the rename.** `internal/agents` loses the name, `skills.go:52` loses the comment, and `DefaultBriefingFiles`'s `CLAUDE.md` (§3.3) gets an explicit ruling instead of an implicit pass. The exception the maintainer named — something genuinely generic, e.g. copilot implementing claude's plugin format — belongs in a pack, not in core, so it does not license a claude name here either.

---

## 7. Storage migration and host helpers (#4/#5, clarified)

The user flagged these as "not totally sure what this is." Concretely:

- **#4 (storage migration)** is `claudejson.go` + `ensure.go`'s credential migration + `prepare.go`'s `syncClaudeJSONSeed`/`migrateOldOverlay`. These are **one-time historical-layout migrations** for claude's old file paths. They are not "claude in core" in the sense that matters — they are *migration debt*, and the right treatment is to keep them until the migration is complete, then delete them, not to "generalize" them (there is nothing generic to generalize *to*; a migration is by definition about one tool's past).
- **#5 (host helpers)** is `hostclaude.go`, which is **already generic** — it reads pack declarations. It is a stale filename, nothing more.

So #4 and #5 are the *least* interesting parts of the residue: one is migration debt with a delete-by date, the other is a rename.

> **REVIEW — NIT, and a reason to hurry.** This doc's own code references check out (`claude.go:20-21` and `65-141`, `hostclaude.go:21-47`, `internal.go:122-127`, `prepare.go:259-268` all verified 2026-08-15). The docs it inherits from do not: [`agent-credentials.md`](agent-credentials.md)'s code map points at `claude.go:161-209`, and [`jail-home.md`](jail-home.md) §4.2 at `claude.go:273-299` and `350-378` — in a file that is now 199 lines total. Whichever commit acts on §5 should fix those two references, since it will invalidate them again.

---

## 8. What this deletes, and what it costs

**Deletes / shrinks:**

- The `claudeAiOauth` schema knowledge leaves the generic hook and lives only in `internal/oauthbroker`.
- `internal/agents` stops being a name that implies an agent registry.
- `hostclaude.go` stops implying a claude-specific path.

**Costs:**

- Generalizing `shared_credentials` drops claude's boot-time merge of a stale local credential into a fresher shared one (a migration edge case; §5).
- ~~Making the broker externally shippable is a real piece of work … My leaning is to *name* the extension point and *not* build it until a second consumer appears.~~ **Superseded: it is being built** (OQ-1). The cost is real and unchanged — it touches the read-only image story and the transport — but it is now accepted rather than deferred, and it is partly repaid by deleting `internal/brokerrelay` (§4).

**Forecloses:** nothing irreversible. The hook generalization is reversible; the broker stays bundled either way until the extension point is built.

> **DECIDED — the ledger after this round.** Restating both lists against the answers, since the originals were written under the old leanings:
>
> **Deletes:** `harvestCredentialsFile` + `expiresAtMs` (~135 lines, and with them the entrypoint's only `claudeAiOauth` knowledge and its one sanctioned tmp+rename) · `internal/brokerrelay`, folded into the framework-owned front · `checkBrokerCredsFreshness` + `parseCredsExpiresAt`, moved behind the broker's existing `doctor_cmd` · the claude names in `internal/agents` and `hostclaude.go`. The `claudeAiOauth` schema ends up in **one** place — the broker — which is what OQ-2 was reaching for by a longer route.
>
> **Costs:** jail-daemon-as-binary + relay/front folding, as above · a fresh login discarded when the shared file is populated, accepted knowingly (§5) · a doctor surface that grades pass/fail where `check` currently prints remaining lifetime, unless `DoctorResult` grows a richer shape.

> **REVIEW — GAP.** One cost is missing, and it is not the merge. [`agent-auth-modes.md`](agent-auth-modes.md) §3 records that the repo **depends** on `oauthMetadataKeys` preserving `subscriptionType` / `rateLimitTier`, because Claude ≥ 2.1.200 treats a creds file carrying only the token trio as *not logged in*. In `claude.go:98-102` that metadata copy is **unconditional** — it runs on a different code path from the `expiresAt` comparison, so it fires even when the local token is older. Deleting `harvestCredentialsFile` from the generic path deletes it too. Whatever survives §5 must keep it; this section should say so, and a regression test should pin it.
>
> **Still live under "shared always wins", and cheaper than it looks:** nothing in the boot path rewrites those keys any more, so preservation reduces to "the broker must not drop them on refresh" — which `NormalizeOAuth` already gets right by construction. The regression test is the deliverable, not the code.

---

## 9. Risks

| Risk | Mitigation |
|---|---|
| Generalizing the hook breaks claude's credential migration | The broker's flock + the copy-if-empty fallback cover steady state; the dropped merge is a migration edge, and the migration itself (`ensure.go:72-83`) is untouched |
| Moving the merge into `oauthbroker` couples the hook and the broker | They are already coupled — the hook symlinks *into* the broker's shared dir; co-locating the merge with the file's owner is the *reduction* of coupling |
| Building the jail-daemon-as-binary extension point widens the native-binary attack surface | It is gated by the existing origin gate (`gateAdmitsCrossing`, `runtime.go:99-114`) — a fetched pack's daemon never runs without approval |
| The doc over-promises "ship externally" and the work stalls | The extension point is *named*, not designed, mirroring `loophole-packaging-overview.md` §3.3's "design the extension point, not the implementation" |

> **DECIDED — two rows change and one is added.** Row 2 (coupling the hook to the broker) is **void**: no merge moves, so nothing couples. Row 4's mitigation is **void as written** — the work is now committed, so "it is only named" no longer protects against the stall it describes; the real mitigation is that the folding *deletes* `internal/brokerrelay`, so a half-finished attempt leaves less code, not more. Row 3 (native-binary attack surface) is the one that gets sharper now that it is being built, and its mitigation holds: `gateAdmitsCrossing` (`runtime.go:99-114`) plus the placement rule (`loopholes/placement.go`) already gate the two other host-execution faces, and a jail-side binary is a third face of the same shape.
>
> **New row — the doctor move regresses `check`'s output.** Moving `checkBrokerCredsFreshness` behind `doctor_cmd` (§4) trades a remaining-lifetime warning for a pass/fail grade unless `DoctorResult` grows a richer shape. Mitigation: make that shape part of the move, or keep the freshness *reporting* in `check` and take only the `claudeAiOauth` parsing out.

---

## 10. Sequencing

1. **Generalize `shared_credentials`** (delete the `claudeAiOauth` merge from the generic path; keep symlink + copy-if-empty). This is the change my agy work already half-exposed, and it is small.
2. **Relocate the merge** into `internal/oauthbroker` (or leave it as a documented claude-specific helper beside the broker).
3. **Rename** `internal/agents` and `hostclaude.go` (cosmetic; no behavior).
4. **Leave the broker bundled**, but write the extension point down (§4) so a second consumer can pick it up. Do **not** build jail-daemon-as-binary now.

> ~~**REVIEW — DISAGREE.** … step 0 is the observation … my recommendation is mtime-newest-wins … leave the broker bundled.~~ **Superseded by the rulings; every premise it ordered around changed.** The observation is no longer a gate (the cost is accepted, §5), the mtime rule is withdrawn, and the broker is being shipped.

> **DECIDED — the sequence, replacing all four steps above.** Ordered so each step is independently landable and the two that delete code come before the one that adds any:
>
> 1. **Delete the harvest.** `harvestCredentialsFile` + `expiresAtMs` go; `linkThroughShared` keeps symlink → copy-if-empty → discard-local. Add the doc comment naming the accepted failure mode, and the regression test pinning `subscriptionType`/`rateLimitTier` survival across a broker refresh (§8). Smallest step, no dependencies, and it is the one the agy work exposed.
> 2. **Move the broker's freshness check behind its existing `doctor_cmd`** (§4), deleting `checkBrokerCredsFreshness` + `parseCredsExpiresAt` from `check`. Independent of step 1, and it settles the `DoctorResult` output shape *before* anything depends on it.
> 3. **The renames.** `internal/agents`, `hostclaude.go` → `hostfiles.go`, `skills.go:52`, and the `DefaultBriefingFiles` ruling (§3.3). Mechanical, no behavior, so it lands between the deletions and the build rather than competing with either. Fix the stale `claude.go` line refs in [`agent-credentials.md`](agent-credentials.md) and [`jail-home.md`](jail-home.md) in this step, since step 1 invalidated them (§7 comment).
> 4. **Build the extension point** — jail-daemon-as-binary, then fold the relay into the framework-owned front, then move the broker onto them. This is the large one and it is last because the first three shrink what it has to carry.
>
> After step 1 the entrypoint holds no `claudeAiOauth` knowledge; after step 2 neither does `check`; after step 4 the broker is a pack. That is the whole thesis of the doc, in the order that keeps each step reversible.

---

## Open Questions

1. **Should the broker become externally shippable now, or stay bundled with the seam documented?**

   This is the closure question for §4. Building jail-daemon-as-binary + relay/front folding is real work touching the read-only image and the transport; the alternative is to keep the broker bundled (it is yolo's own code, and `loophole-packaging-overview.md:87` already notes a baked client is fine for an official pack) and only relocate the claude-specific merge.

   _Leaning:_ Keep bundled, document the seam. There is exactly one consumer, and the "closed set until a second case" discipline has served this repo well. Build the extension point when a second tool needs a jail-side daemon.

   > ~~**REVIEW — AGREE.** Sign it as written.~~ **Wrong on both halves.** The doctor surface I offered as a third blocker turned out to be an *extension point that already exists* (§4), so it was never a reason to stay bundled — and the "one consumer, wait for a second" discipline argues for deferring the work, not for the architecture being right. The maintainer's answer is the load-bearing one.

   **Answer:**
   > **Shippable.** Build jail-daemon-as-binary and fold the relay into the framework-owned front (§4), then move the broker onto them. It stops being an exception to *"agents are packs"* and becomes an ordinary pack-shipped loophole — and because the folding deletes `internal/brokerrelay` rather than relocating it, core gets smaller in the process. Sequenced last (§10 step 4), after the deletions that shrink what it must carry.

2. **Does the `claudeAiOauth` merge move into `internal/oauthbroker`, or does it stay as a claude-specific hook?**

   Determines whether `shared_credentials` becomes fully generic or gains a claude-specific sibling. Moving it into `oauthbroker` co-locates it with the file's owner; keeping it as a hook preserves the "hooks are the imperative residue" shape but re-introduces a claude-named hook.

   _Leaning:_ Move into `internal/oauthbroker`. The merge is about the *shared creds file*, which the broker owns; a hook should not carry schema knowledge.

   > **REVIEW — DISAGREE (BLOCKING).** The second half of that leaning is right and the destination is wrong. `internal/oauthbroker` is **host-side only** — its non-test importers are `internal/cli/internal.go`, `internal/hostservice`, `internal/svcendpoint` and `internal/loopholes/runtime.go` — while `internal/entrypoint` is the **jail-side** binary (`cmd/yolo-entrypoint`). Moving the harvest there makes the jail entrypoint import the host daemon package, and the harvest cannot actually *run* there: it is jail-boot-time and per-jail, whereas the host broker only ever sees the shared file, never a given jail's local one.
   >
   > **Neither option offered is the right one; a third is.** Extract a leaf package — `internal/claudecreds` — holding the `claudeAiOauth` schema and the merge rules, imported by the hook path *and* the broker *and* `check`. That is worth doing on its own evidence: the tree already carries **three** copies of that schema knowledge — `claude.go:78-110`, `NormalizeOAuth` (`oauthbroker.go:186-224`, previous-preserving), and `parseCredsExpiresAt` (`check/sections_misc.go:81-99`) — which is exactly the duplication this doc set out to name. It satisfies "a hook should not carry schema knowledge" without inverting the layering.

   > **The question restated, because it was asked in jargon.** Claude's credential file has a particular JSON shape — a `claudeAiOauth` object holding `accessToken` / `refreshToken` / `expiresAt` and some entitlement metadata. Three places in core know that shape and would break if Anthropic changed it: the boot-time harvest, the broker's refresh, and `check`'s freshness warning. This question was only ever *"which of those three is allowed to know it?"* — the original framing ("does the merge move or stay a hook") assumed the answer had to be a new home for the harvest's copy.

   **Answer:**
   > **Obviated — nothing moves, and no `internal/claudecreds` is needed.** The other two rulings dissolved it: OQ-3 **deletes** the harvest's copy rather than relocating it (§5), and §4's doctor ruling **deletes** `check`'s copy by moving the grading behind the broker's existing `doctor_cmd`. What is left is one copy, in `internal/oauthbroker` — which is exactly where the original leaning wanted it, reached by deleting two things instead of building one. The broker is the only component that *must* understand the file, because it is the thing that refreshes the token.
   >
   > Re-open only if a third consumer appears that genuinely needs to parse the file; at that point the leaf package is the answer, and the review's argument for it applies unchanged.

3. **Is the dropped boot-time merge (§5) acceptable for claude, or must the generic hook preserve a "newest-wins" merge for any schema?**

   If the latter, the hook needs a schema-agnostic merge rule (e.g. "copy local over shared if local's mtime is newer"), which is weaker than claude's `expiresAt` comparison but generic.

   _Leaning:_ Accept the drop. The merge only fires on a migration edge, and the broker's flock already serializes the refresh that matters.

   > ~~**REVIEW — DISAGREE (BLOCKING).** … **Recommended answer:** mtime-newest-wins in the generic hook.~~ **Withdrawn** (reasoning in the §5 comment): the recommendation was buying protection against a loss the maintainer had already priced as acceptable, and mtime can keep the wrong credential anyway, since the broker refreshes the shared file's mtime on every tick regardless of which account it holds.

   **Answer:**
   > **Neither — the shared file always wins, and there is no merge in any schema.** `already-symlink → done` · `real file + empty shared → copy local` · `real file + populated shared → discard local` · `symlink`. Losing one valid login in a migration is acceptable, so no freshness comparison is needed and `harvestCredentialsFile` + `expiresAtMs` are deleted outright (§5). The copy-if-empty branch stays — it is not a freshness rule, it is what lets a first login survive.
   >
   > Accepted knowingly: if the shared file holds a revoked credential, a fresh login is discarded at the next boot; the exit is `rm` the shared file. Say so in the hook's doc comment.

4. **Is "bundled loophole + baked daemon" acceptable for yolo's *own* claude-specific code, as a standing principle?**

   This is the meta-question behind OQ-1. If the answer is "no, packs must be truly separate even for yolo's own code," then the broker *must* become shippable, and OQ-1 is decided the other way.

   _Leaning:_ Yes, acceptable for yolo's own code. The principle is about *third-party* packs not being able to smuggle host code; yolo's own code is already trusted.

   > ~~**REVIEW — AGREE.** Sign it.~~ **Moot:** OQ-1 was answered the other way, and this question's own text says that decides this one.

   **Answer:**
   > **No — not as a standing principle.** *(Derived from OQ-1 rather than answered directly; overrule this line if the intent was narrower.)* By this question's own wiring, choosing "shippable" for the broker means "yolo's own code is already trusted" does not license a baked daemon where a pack could carry one. The narrower claim that survives, and that [`loophole-packaging-overview.md`](loophole-packaging-overview.md) still supports, is about **trust**, not **packaging**: an official pack carries yolo's authority, so a *baked client binary* is fine for one. That is a statement about who is accountable for code, and it never implied that the code has to live in core.
