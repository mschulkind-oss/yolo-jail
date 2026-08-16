# The claude-shaped code still in core — and how packs become truly separate

**Status:** DESIGN SKETCH, 2026-08-15. Nothing built; this doc is the diagnosis and the open questions.

**The short version.** "Core does not know what an agent is" is true of the assembly layer — packs declare paths/kinds/hooks/surfaces and core renders them with no switch on a tool name. It is *not* true of a small stratum of imperative Go that is still claude-shaped: the OAuth broker's daemons are baked into `yolo`/`yolo-jaild`, the `shared_credentials` hook's harvest body hardcodes `claudeAiOauth`, and a few storage/migration helpers are named for and keyed on claude. This doc inventories that residue, proposes how each piece either becomes generic or moves into claude's own pack, and — for the broker — sketches the extension point that would let it ship externally.

**Reads with:** [`loophole-packaging-overview.md`](loophole-packaging-overview.md) (the `loophole` kind and the framework-owned wire — the extension point this doc leans on), [`loophole-packaging.md`](loophole-packaging.md) (the detailed design, OQ-LP10/OQ-LP11/OQ-LP14), [`pack-capabilities.md`](pack-capabilities.md) (the `serves`/`supersedes` vocabulary the broker already uses), [`agent-credentials.md`](agent-credentials.md) (what crosses the boundary today).

---

## 1. Verdict up front

The residue is real but smaller and more tractable than "there's claude everywhere" suggests. Three of the five categories are already generic *in body* and only claude-shaped *in name* (`internal/agents`, `hostclaude.go`, the host-file mounts). The two that are genuinely claude-shaped are:

1. **The OAuth broker subsystem** — `internal/oauthbroker`, `internal/oauthterminator`, `internal/broker`, `internal/brokerrelay`, plus three baked daemon subcommands. This is Claude-specific by *nature* (Anthropic mints single-use refresh tokens), and it is the one piece where "separate it" is a real architectural question, not a rename.
2. **The `shared_credentials` hook body** — `internal/entrypoint/claude.go`'s `harvestCredentialsFile` hardcodes `claudeAiOauth`. The hook is generically *named* but claude-shaped *in body*, and my agy change is the first time a second tool consumed it, which is exactly what exposed the seam.

My recommendation: **make `shared_credentials` genuinely generic** (symlink + copy-if-empty, no schema-specific merge), **move the claude-specific OAuth merge into the broker's own code**, and **treat the broker as the one remaining "extension point" question** — where the honest answer may be "keep it bundled, but document the seam" rather than "force it external."

---

## 2. What "core doesn't know an agent" means — and its boundary

The claim is real and is the dominant design. Verified against the code 2026-08-15:

- `internal/packdecl` is the manifest schema; `internal/packload` reads it; `internal/entrypoint/packsurfaces.go` renders every pack in one loop with no switch on tool name (`AGENTS.md`: "the boot path renders every one in a single loop … with no switch on any tool name").
- Adding agy was a `pack.json` (`packs/agy/pack.json`), not a Go change. The `agentcfg` surfaces, the mount assembly (`internal/cli/run/assemble.go`), and the host-file/mount grants (`internal/cli/run/hostclaude.go` — see §7) all key on paths and kinds.
- `internal/agents/agents.go:9-18` states the registry is gone: "All of it is now pack DATA … Core does not know what an 'agent' is."

The boundary is the **imperative residue**: the things a pack needs done that are *not* surface content and therefore cannot be declared as layers. `internal/entrypoint/packhooks.go:3-26` is honest about this — it names three hooks and says "all currently claude's." The hooks are a **closed set** (`packdecl.KnownHooks`), and a third-party pack cannot ship a new side effect. That closed set is the design's own admission that "no agent in core" stops at the point where a side effect is tool-specific.

So the accurate framing is: **the assembly layer achieved it; the imperative layer has not, because only one tool ever needed those side effects.** The work is to finish the imperative layer — either by generalizing what is genuinely generic, or by relocating what is genuinely claude-specific.

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

### 3.3 `internal/agents` (already generic in body)

`internal/agents/agents.go` is now only skills staging, briefing composition, loophole descriptions, and the source-tree probe — "the stuff that was never per-agent." The claude references are in comments (`agentsmd.go:3`, `skills.go:52`) and tests (`skills_test.go`), not in a claude-specific switch. This is a **rename**, not a redesign.

### 3.4 Storage migration / back-compat (claude-keyed)

- `internal/storage/claudejson.go` — `SyncClaudeJSONSeed` back-propagates `oauthAccount`/`hasCompletedOnboarding` from a workspace `claude.json` into the `GLOBAL_HOME` seed. This is the one non-broker write-back-to-`GLOBAL_HOME` path (`agent-credentials.md` §4 note).
- `internal/storage/ensure.go:72-83` — migrates `.claude/.credentials.json` → `.claude-shared-credentials/.credentials.json`.
- `internal/cli/run/prepare.go:259-268` — `syncClaudeJSONSeed` + `migrateOldOverlay` (`claude-projects` → `claude/projects`, `claude-settings.json` → `claude/settings.json`).

These are **historical-layout migrations** for claude specifically. They are inherently tied to one tool's past file layout, not to "agents."

### 3.5 Host-side helpers (generic body, claude name)

`internal/cli/run/hostclaude.go` is named for claude but is fully generic — it reads `p.HonoredHostFiles()` / `p.HonoredMounts()` from pack declarations (`hostclaude.go:21-47`). The filename is a fossil. Same for `internal/cli/check/sections_misc.go:19-79` (`checkBrokerCredsFreshness`), which is claude-specific but *is* the broker's freshness check, so it belongs with §3.1, not here.

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

The alternative — and my current leaning — is that **the broker stays bundled, but the seam is made explicit**: the claude-specific merge (§3.2) moves *into* `internal/oauthbroker` (where the shared creds file is already owned), and the generic `shared_credentials` hook keeps only the symlink+copy-if-empty. "Bundled" is not the sin; "claude-specific logic living in a generically-named hook" is.

---

## 5. `shared_credentials` — generic, or moved

The hook's *contract* is generic and worth keeping: **"symlink this file into this machine-scoped dir, harvesting a real file first."** Both claude and agy need exactly that. What is not generic is the *harvest*: claude's merge is keyed on `claudeAiOauth` + `expiresAt`, agy's token is a different schema, and a future tool would be a third.

Proposal, in two moves:

1. **Make the hook generic.** `linkThroughShared` keeps: already-correct-symlink → done; real file + empty shared → copy local into shared; real file + populated shared → leave shared (no merge); then symlink. Delete `harvestCredentialsFile`'s `claudeAiOauth` merge from the generic path.
2. **Relocate the merge.** The "newest-token-wins" merge of `claudeAiOauth` belongs with the thing that owns the shared creds file — `internal/oauthbroker`. It becomes a broker-internal concern (or a claude-specific hook if a second claude-specific side effect ever justifies one).

The cost of (1): claude loses the boot-time merge of a *stale* local file into a *fresher* shared file. That case only arises when a jail holds a real (non-symlink) credential file while the shared file is already populated — a migration edge, not steady state, and the broker's flock already serializes the refresh that matters.

---

## 6. `internal/agents` — what's actually left

Nothing claude-specific in *body* remains. The package is a home for "content agents read" (briefing, skills staging, loophole descriptions, source probe). The fix is cosmetic: the package name and a handful of comments/tests still say "claude" where they mean "the briefing/skills targets." This is a rename-and-comment pass, not a design decision — it does not need an open question, only a decision to do it.

---

## 7. Storage migration and host helpers (#4/#5, clarified)

The user flagged these as "not totally sure what this is." Concretely:

- **#4 (storage migration)** is `claudejson.go` + `ensure.go`'s credential migration + `prepare.go`'s `syncClaudeJSONSeed`/`migrateOldOverlay`. These are **one-time historical-layout migrations** for claude's old file paths. They are not "claude in core" in the sense that matters — they are *migration debt*, and the right treatment is to keep them until the migration is complete, then delete them, not to "generalize" them (there is nothing generic to generalize *to*; a migration is by definition about one tool's past).
- **#5 (host helpers)** is `hostclaude.go`, which is **already generic** — it reads pack declarations. It is a stale filename, nothing more.

So #4 and #5 are the *least* interesting parts of the residue: one is migration debt with a delete-by date, the other is a rename.

---

## 8. What this deletes, and what it costs

**Deletes / shrinks:**

- The `claudeAiOauth` schema knowledge leaves the generic hook and lives only in `internal/oauthbroker`.
- `internal/agents` stops being a name that implies an agent registry.
- `hostclaude.go` stops implying a claude-specific path.

**Costs:**

- Generalizing `shared_credentials` drops claude's boot-time merge of a stale local credential into a fresher shared one (a migration edge case; §5).
- Making the broker externally shippable is a real piece of work (jail-daemon-as-binary + relay/front folding), and it is **not** free: it touches the read-only image story and the transport. My leaning is to *name* the extension point and *not* build it until a second consumer appears — which is the same "closed set until a second case" discipline `packhooks.go` already states.

**Forecloses:** nothing irreversible. The hook generalization is reversible; the broker stays bundled either way until the extension point is built.

---

## 9. Risks

| Risk | Mitigation |
|---|---|
| Generalizing the hook breaks claude's credential migration | The broker's flock + the copy-if-empty fallback cover steady state; the dropped merge is a migration edge, and the migration itself (`ensure.go:72-83`) is untouched |
| Moving the merge into `oauthbroker` couples the hook and the broker | They are already coupled — the hook symlinks *into* the broker's shared dir; co-locating the merge with the file's owner is the *reduction* of coupling |
| Building the jail-daemon-as-binary extension point widens the native-binary attack surface | It is gated by the existing origin gate (`gateAdmitsCrossing`, `runtime.go:99-114`) — a fetched pack's daemon never runs without approval |
| The doc over-promises "ship externally" and the work stalls | The extension point is *named*, not designed, mirroring `loophole-packaging-overview.md` §3.3's "design the extension point, not the implementation" |

---

## 10. Sequencing

1. **Generalize `shared_credentials`** (delete the `claudeAiOauth` merge from the generic path; keep symlink + copy-if-empty). This is the change my agy work already half-exposed, and it is small.
2. **Relocate the merge** into `internal/oauthbroker` (or leave it as a documented claude-specific helper beside the broker).
3. **Rename** `internal/agents` and `hostclaude.go` (cosmetic; no behavior).
4. **Leave the broker bundled**, but write the extension point down (§4) so a second consumer can pick it up. Do **not** build jail-daemon-as-binary now.

---

## Open Questions

1. **Should the broker become externally shippable now, or stay bundled with the seam documented?**

   This is the closure question for §4. Building jail-daemon-as-binary + relay/front folding is real work touching the read-only image and the transport; the alternative is to keep the broker bundled (it is yolo's own code, and `loophole-packaging-overview.md:87` already notes a baked client is fine for an official pack) and only relocate the claude-specific merge.

   _Leaning:_ Keep bundled, document the seam. There is exactly one consumer, and the "closed set until a second case" discipline has served this repo well. Build the extension point when a second tool needs a jail-side daemon.

   **Answer:**
   > _(empty — fill in when decided)_

2. **Does the `claudeAiOauth` merge move into `internal/oauthbroker`, or does it stay as a claude-specific hook?**

   Determines whether `shared_credentials` becomes fully generic or gains a claude-specific sibling. Moving it into `oauthbroker` co-locates it with the file's owner; keeping it as a hook preserves the "hooks are the imperative residue" shape but re-introduces a claude-named hook.

   _Leaning:_ Move into `internal/oauthbroker`. The merge is about the *shared creds file*, which the broker owns; a hook should not carry schema knowledge.

   **Answer:**
   > _(empty — fill in when decided)_

3. **Is the dropped boot-time merge (§5) acceptable for claude, or must the generic hook preserve a "newest-wins" merge for any schema?**

   If the latter, the hook needs a schema-agnostic merge rule (e.g. "copy local over shared if local's mtime is newer"), which is weaker than claude's `expiresAt` comparison but generic.

   _Leaning:_ Accept the drop. The merge only fires on a migration edge, and the broker's flock already serializes the refresh that matters.

   **Answer:**
   > _(empty — fill in when decided)_

4. **Is "bundled loophole + baked daemon" acceptable for yolo's *own* claude-specific code, as a standing principle?**

   This is the meta-question behind OQ-1. If the answer is "no, packs must be truly separate even for yolo's own code," then the broker *must* become shippable, and OQ-1 is decided the other way.

   _Leaning:_ Yes, acceptable for yolo's own code. The principle is about *third-party* packs not being able to smuggle host code; yolo's own code is already trusted.

   **Answer:**
   > _(empty — fill in when decided)_
