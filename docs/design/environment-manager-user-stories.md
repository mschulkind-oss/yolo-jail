# User Stories: meeting yolo when it manages the environment, not just the box

**Status:** STORIES + OPEN QUESTIONS, written 2026-07-27; **re-verified against the tree
2026-08-23.** Most of the verbs the stories exercise **have since shipped** (`apply`, `apply
--host`, `apply --sealed`, `describe`, `check-deps`, the `confinement` key — env-manager plan
Phases 0–6, 8, 9), and **all three "live defects" (G1, G2, G3) are FIXED**. Every gap below now carries
a dated verdict; read those before hunting for a bug. **Eleven questions are still live**
(Q1 · Q1a · Q1b · Q2–Q9, below); **Q1 is the closure question and the biggest one in the
document**, and Q7 decides whether Linux `guest` is a promise or a hypothesis. IDs are cited from
[`../plans/roadmap.md`](../plans/roadmap.md) — do not renumber them.

> **Verification pass — 2026-08-23.** §1–§5 keep their original present tense: they describe the
> product **as it was on 2026-07-27**, which is what makes them readable as stories. What changed
> since is recorded as dated verdicts inline (each gap carries a `> [!NOTE]` or `> [!WARNING]`
> block) and summarised here. Nothing is deleted — a gap that closed is still the argument for
> the invariant that closed it.
>
> | Story defect | Verdict, verified 2026-08-23 | Evidence |
> |---|---|---|
> | **Story 1 Gap 1** — the `host` layer reads an undeclared file | **STILL LIVE**, and *worse* than the story says | `HostSource` at `internal/agentcfg/manifest/manifest.go:142`; nothing in `describe` names it, and `config ls`'s `host` column comes from a hardcoded 2-entry map (`internal/cli/configls.go:196-202`) |
> | **Story 1 Gap 2** — capture outranks the definition | **PARTLY LIVE**; the story overstates it, and `--sealed` now catches it | Real order at `internal/agentcfg/compose.go:357-379`: capture loses to `computed`/`transform`/`managed`. `applySealed` refuses on outstanding captures (`internal/cli/apply.go:617-651`) |
> | **Story 1 Gap 3** — `yolo-jail.local.jsonc` | **STILL LIVE** as an input; now refused by `--sealed` | `internal/config/config.go:38`, auto-merge at `internal/config/load.go:205-221`; refusal at `internal/cli/apply.go:623-630` |
> | **Story 2 step 3** — macos-user renders 0 surfaces (**G3**) | **FIXED 2026-08-12** (`a39628ad`) | packs staged *above* the backend dispatch at `internal/cli/run/run.go:103`; `YOLO_PACK_ROOT` set at `internal/macosuser/runplan.go:208-211`; tests `internal/cli/run/packstagedispatch_test.go:87,131` |
> | **Story 2 step 6** — host-side `config reset` data loss (**G1**) | **FIXED 2026-08-01** (`1220ac55`) | `refuseHostSideWrite` at `internal/cli/configdiff.go:84-93`, wired at `:645`; regression test `configdiff_test.go:172` |
> | `config capture` leaks host config into the workspace (**G2**) | **FIXED 2026-08-01** (`1220ac55`) | same guard, wired at `internal/cli/configdiff.go:848` |
>
> **The one thing that has NOT shipped and that the stories most depend on:** `yolo config
> promote` (plan Phase 5.3) does not exist — `internal/cli/config.go:33-60` lists `ls, render,
> diff, reset, capture, drift, dump` and no more. So `--sealed` can *refuse* on a capture but the
> user's only remedy is still "discard it." Q1's leaning is half-built.

Five worked stories of people (and one agent) encountering yolo as
[an environment manager whose confinement is a dial](yolo-as-environment-manager.md) —
`jail` (default), `guest`, `host` — rather than as a container product with config
composition bolted inside it. The lens is **the moment the reframing pays off, and the moment it
bites**: every story is written so the new verbs (`apply`, `describe`, `check --at`) get used in
anger, and three of the five hit defects that existed in the shipped code **when the stories were
written (2026-07-27)**. Two of those three are now fixed — see the verification table above and
the dated verdict at each gap.

Two things the stories argue for that the design doc does not yet say. **The goal is a binding
definition, not a comparison tool** — nix has no "diff my two machines" verb because it does not
need one, so the verb story 1 ends up needing is the one that *refuses* (`apply --sealed`), and
`describe --hash` is demoted to a cache key over a closed definition. And **an inert config key is
a handoff, not a verdict** — where yolo declines to manage something, it should verify whether the
dependency is there anyway and name the remedy, which is story 2's step 1.

Terminal output below is real where it can be (`yolo config ls`, `yolo config diff`, the launch
banner, `yolo pack ls`, the empty-packs notice) and follows the design doc's samples where the
feature does not exist yet.

**Reads with:** [yolo-as-environment-manager.md](yolo-as-environment-manager.md) (the design
these stories exercise), [host-render-target.md](host-render-target.md) (§6 is where the
failures in story 2 were probed), [../plans/BACKLOG.md](../plans/BACKLOG.md) Stage G.

---

## 1. Maya — Staff Engineer, Rust CLI, Wants a Guarantee, Not a Diff

**Context:** Maya maintains `sift`, a Rust CLI with 1,240 tests. Her `flake.nix` pins the
toolchain, and she has not wondered "does this machine have the right rustc" in two years. The
definition binds, so there is nothing to check. She wants the same property for the agent
environment: the `claude` pack plus a private `house-rules` pack (11 skills, an AGENTS.md fragment
about her error-handling conventions, and a `claude/settings.json` surface that disables two noisy
MCP servers). Her ask is one sentence: **"I want to define the agent environment functionally,
the way I define the toolchain, and have that definition be true."**

**What she is explicitly not asking for.** A way to compare her desk to her laptop. Nix has no
`nix diff-my-two-machines` because it does not need one — a comparison tool is what you build
when the definition does not bind. If yolo's answer to "is this the environment I declared" is
"diff it against another machine," the answer is no. So the interesting verb in this story is not
`diff`. It is the one that **refuses**.

**What happens:**

1. She writes the definition and applies it:

   ```
   $ yolo apply
   jail (podman)   image ✓ (cached)   packs claude,house-rules   surfaces 4 rendered
   ```

2. On the laptop: same definition, same command. The environment is the same **by
   construction** — nixpkgs pinned by `flake.lock`, packs pinned to commits by `yolo pack
   status`, composition deterministic and layered. She does not verify this, the same way she
   does not verify rustc. **That non-event is the product.**

3. Three weeks later the agent is noticeably better at her desk than on the laptop. So the
   definition did *not* bind, and the fact that she now has to investigate is itself the finding.

   Three inputs escaped the definition. All three exist in the shipped product today.

   **Gap 1 — the `host` layer reads a file that no declaration mentions.** `claude/settings`
   composes four layers, and one of them is literally named `host`:

   ```
   $ yolo config ls
   SURFACE          PATH                        CODEC  MODE     LAYERS                   OVERLAY
   claude/config    ~/.claude.json              json   rmw      defaults managed         –
   claude/settings  ~/.claude/settings.json     json   capture  host computed managed    3 keys ⚠
   mise/config      ~/.config/mise/config.toml  toml   capture  computed                 1 key ⚠
   house-rules/md   ~/.claude/CLAUDE.md         text   copy     defaults                 –
   ```

   That `host` layer is her own `~/.claude/settings.json`, mounted in from the host and recorded
   per-surface as `HostSource` (`internal/agentcfg/manifest/manifest.go:142` — the story said
   `:132`, which has drifted by ten lines). Her two machines' copies differ. This is nix's *impure
   derivation* — an input from outside the closure — and nix's rule for it is not prohibition, it
   is **declaration**. The `claude` pack does declare the grant; nothing surfaces that the
   resulting environment therefore has a machine-shaped input.

   > [!WARNING]
   > **Gap 1 is STILL LIVE, and the tree is worse than this story claims — verified 2026-08-23.**
   > `HostSource` is intact (`internal/agentcfg/manifest/manifest.go:142`, written at
   > `internal/packload/packload.go:89`, read by the boot render at
   > `internal/entrypoint/packsurfaces.go:328,331`). But the `LAYERS` column Maya reads above is
   > **not derived from it**: `config ls` gets `host` from a hand-maintained two-entry map,
   > `surfaceHasHostLayer` (`internal/cli/configls.go:196-202`), listing only `claude/settings`
   > and `pi/settings`. A **pack** surface with a non-empty `HostSource` reads machine state at
   > boot and `config ls` shows **no** `host` layer for it at all. `yolo describe` — which shipped
   > since (`internal/cli/describe.go`) — never mentions a host layer either; its only `host`
   > strings (`:129,214`) are the confinement *notch*, an unrelated concept. So the story's
   > "nothing surfaces that the environment has a machine-shaped input" is now literally true for
   > every pack surface, not just under-emphasised.
   >
   > Note also the standing decision this collides with: the design doc's §3.3 resolved **OQ-3 —
   > retire the read-in `host` layer entirely, express personal settings as a local pack**
   > (2026-08-01). That is decided and **not implemented**; until it is, the layer and the display
   > gap both persist.

   **Gap 2 — capture writes in-jail edits back, and they outrank the declaration.** This is the
   one that actually broke her:

   ```
   $ yolo config diff claude --surface settings
   # claude/settings → ~/.claude/settings.json
     enabledPlugins  {"gopls-lsp@claude-plugins-official": true, "pyright-lsp@claude-plugins-official": true} (was {})

   These values were captured from in-jail edits and outrank the host layer.
   Discard them with: yolo config reset claude
   ```

   She enabled `gopls` and `pyright` interactively in March, at her desk. The capture overlay
   promoted them to the winning layer. The laptop never had them, and **the agent really was
   better at her desk** — it had two language servers the definition never mentioned. In nix
   terms: a store path edited itself, and the edit outranks the derivation. There is no equivalent
   move in nix, which is the point.

   The launch banner does announce this, every single boot:

   ```
   ~/.config/mise/config.toml: 1 key from captured in-jail edits (yolo config diff mise)
   ~/.claude/settings.json: 3 keys from captured in-jail edits (yolo config diff claude)
   ```

   But by the time it prints, the undeclared value has already won. A notice is not a binding.

   > [!WARNING]
   > **Gap 2 is PARTLY LIVE, and this story overstates it — verified 2026-08-23.** The capture
   > overlay does **not** outrank *every* declared layer. The real ascending precedence
   > (`internal/agentcfg/compose.go:357-379`) is:
   >
   > `defaults` → `host` → `workspace` → `config-overlay:<pack>` → **`overlay` (capture)** →
   > `computed` → `transform` (Lua) → `managed` (enforced as a floor)
   >
   > So capture beats `defaults`/`host`/`workspace`/pack overlays and **loses** to `computed`,
   > `transform` and `managed` — deliberately, so a stale in-jail edit cannot defeat
   > regenerate-don't-reconcile (`compose.go:65-77`; pinned by
   > `internal/agentcfg/staterender_test.go:337`). Maya's actual example survives intact, because
   > `enabledPlugins` is a plain declared key rather than a `managed` one. The shipped user-facing
   > copy still says the strong thing — `internal/cli/configls.go:444` ("outrank the host layer"),
   > `internal/cli/apply.go:634` ("outranking the definition") — and is true only against the
   > lower half of the stack.
   >
   > **What HAS shipped:** `yolo apply --sealed` now refuses while any capture is outstanding
   > (`applySealed`, `internal/cli/apply.go:617-651`; tests `internal/cli/describe_test.go:264-287`).
   > **What has NOT:** `yolo config promote` (plan Phase 5.3). The refusal message at `apply.go:635`
   > tells the user to "promote them into a pack" — advice for a verb that does not exist. The only
   > shipped remedy is `yolo config reset`. That is precisely the half of Q1's leaning still owed.
   >
   > The boot banner quoted above is unchanged and still prints
   > (`internal/entrypoint/prism.go:322-330`).

   **Gap 3 — `yolo-jail.local.jsonc`.** An untracked, gitignored sibling that merges over the
   workspace config *automatically*, with no `include_if_found` entry. Hers drops two packages,
   written eight weeks ago while she was disk-starved and forgotten since. A deliberate feature,
   and a hole in the definition by design.

   > [!NOTE]
   > **Gap 3 is STILL LIVE as an input, and is now CATCHABLE — verified 2026-08-23.** The
   > auto-merge is unchanged: `config.WorkspaceLocalConfigName`
   > (`internal/config/config.go:38`) is loaded unconditionally and merged over the workspace
   > config, local winning, at `internal/config/load.go:205-221` — no `include_if_found` needed.
   > "Gitignored" remains a convention the guide asks for (`docs/guides/USER_GUIDE.md:276`), not
   > something code enforces. What changed is the refusal Maya asks for in step 4: `apply
   > --sealed` stats for the file and refuses when it is present
   > (`internal/cli/apply.go:623-630`). So step 4's first two refusal lines are **shipped**;
   > step 4's third line (the `✓ declared impurity` report of the `host` layer) is not — see
   > Gap 1's verdict.

4. What she wants is not a comparison. She wants the environment to tell her whether it is
   **closed**, and to refuse when it isn't:

   ```
   $ yolo apply --sealed
   ✗ refused: 3 keys captured in-jail outrank the definition
              claude/settings: enabledPlugins, extraKnownMarketplaces · mise/config: [tools]
              → promote them into a pack, or discard: yolo config reset claude
   ✗ refused: yolo-jail.local.jsonc is present and drops 2 packages (ripgrep, fd)
              → fold it into yolo-jail.jsonc, or pass --allow-local
   ✓ declared impurity: claude/settings ← ~/.claude/settings.json (granted by the claude pack)
   ```

   This is the nix model applied exactly: **impurity is not banned, it is declared.** A pack that
   says "I read the user's `~/.claude/settings.json`" is the fixed-output derivation — an impure
   input, named in the definition, so the closure stays honest and the value is still attributable.
   A captured in-jail edit is an impure input with *no declaration anywhere*, and that is the one
   that has to be an error rather than a banner line.

   **Gap:** nothing in the design decides whether `--sealed` is a flag or the default. As a flag,
   the property Maya thinks she bought is off until she knows to ask for it — and she came here
   from nix, where nobody opts into purity. As the default, capture (a genuinely good feature:
   agents and humans edit config in-jail, and silently discarding that is hostile) becomes an
   error on first use.

   > [!NOTE]
   > **Decided and shipped as a FLAG — verified 2026-08-23.** The design doc §3.3 rules
   > "`--sealed` is an opt-in flag, not the default, and the split is the point," and the code
   > agrees: `internal/cli/apply.go:69-70,101-102`. Maya's complaint therefore stands as written —
   > the guarantee is off until she asks. **Q1a is not closed by this**, because its leaning was a
   > *third* option (default-on in CI/non-TTY, flag-on interactively) and nothing in `applySealed`
   > consults TTY-ness.

5. She promotes the two plugins into `house-rules`, folds the local override into
   `yolo-jail.jsonc`, discards the captures, and re-applies:

   ```
   $ yolo apply --sealed
   jail (podman)   sealed ✓   image ✓ (cached)   packs claude,house-rules   surfaces 4 rendered
   ```

   She adds `yolo apply --sealed` to CI and **never runs `describe` again.** That is the correct
   end state: the success condition for a guarantee is that you stop looking at it.

   **Gap:** `describe --hash` still has a job, but not the one it was pitched with. A hash over a
   *sealed* definition is a cache key and a CI pin — a `flake.lock` rev. A hash over an unsealed
   environment is a number that changes for reasons the user cannot enumerate, which is worse than
   having no hash, because it looks authoritative. The hash must refuse to print, or print marked,
   when the closure is open.

**What would trip them up:**

- The word "reproducible" in yolo's own materials currently means "the same declaration produces
  the same jail," and Maya reads it as nix's meaning: "the declaration is the only input." Three
  inputs say otherwise.
- Capture and closure are in direct tension, and both are right. Nothing in the design says which
  wins by default, or gives a user a way to say "capture, but tell me I've gone impure."
- Sealing cannot mean "no host reads" — the `host` layer is load-bearing, it is how her real Claude
  settings reach the jail at all. It has to mean "no *undeclared* host reads," which is a subtler
  rule to explain and to implement.

**What makes this work:**

- The binding mostly exists already. Packages come from `flake.lock`, packs lock to commits,
  composition is deterministic and layer-ordered. What's missing is not machinery, it's a refusal.
- Every impurity is already *visible somewhere*: `config ls` marks captured surfaces with `⚠`,
  the boot banner prints them, and `HostSource` records where a host layer was read from. Closure
  is an aggregation of facts yolo already holds — which is why `--sealed` is a small feature and a
  large promise.

**The aha moment:** step 4 — when she stops asking "are my two machines the same?" and starts
asking "is this machine what I said?" The first question needs another machine and a diff tool.
The second needs only the definition. Nix taught her the difference, and she had been asking yolo
the weaker question because it was the only one yolo could answer.

---

## 2. Priya — Platform Engineer, Rolling yolo Out to a Mac Fleet, Hits the Broken Middle

**Context:** Priya supports 34 engineers, 28 of them on M-series MacBooks. She has been asked to
standardize agent config across the fleet: same skills, same approval posture, same MCP servers.
Containers on macOS mean a VM, the VM means 4 GB of RAM and a 20-second cold start, and her
developers will not accept that for a tool they invoke 40 times a day. The `guest` notch is
exactly what she needs and exactly why she's here: **a real home on the real filesystem, no VM.**

**First 30 minutes:**

1. She reads the confinement table, writes the user config, and checks it:

   ```jsonc
   // ~/.config/yolo-jail/config.jsonc
   {
     "confinement": "guest",
     "packs": ["claude", "git+ssh://git@github.com/acme/yolo-house-rules"]
   }
   ```

   ```
   $ yolo check --at guest
   ✓  packs, surfaces, skills, briefing, env_sources    apply here
   ✗  packages                    needs a jail (no image to bake)
   ✗  mounts, host_files          needs a mount namespace — refused, never emulated
   ✗  network.*, resources        nothing to confine
   !  security.blocked_tools      shims would land on the guest user's PATH — opt in explicitly
   ```

   `check --at` told her, by name, that her `packages: ["postgresql", "redis"]` line is inert
   here. Which is where a reporting tool stops and where she still has a rollout problem: 28
   machines need `postgresql` and `redis` and yolo has just declined to provide them.

   **Gap — an inert key is a handoff, not a verdict.** "yolo can't manage this" is one sentence
   short of useful. The next sentence is whether the dependency is *present anyway*, and the one
   after is how to get it. Both are knowable: `packages` names concrete binaries, and the notch
   knows which native package manager owns them. What she needs:

   ```
   $ yolo check --at guest
   ✗  packages          yolo does not manage packages at this notch (no image to bake)
                        yolo needs these; here is what this machine has:
                          postgresql   ✓ present   /opt/homebrew/bin/psql   16.4
                          redis        ✗ MISSING
                        remedy for the fleet:  brew install redis
                        yolo will re-check on every apply and refuse if it goes missing.
   ```

   Two properties matter more than the formatting. **It verifies rather than assuming** — the
   `✓ present` line is a probe, not an inference from the config, so a machine that lies gets
   caught. And **it hands off with momentum**: a copy-pasteable `brew install` line, ownership
   stated plainly (Homebrew's, not yolo's), and a promise about future applies. Compare the
   original output, which correctly reports scope and leaves her to work out the rest across 28
   machines.

   **Gap:** the remedy line is a per-notch, per-platform mapping (`brew`, `apt`, `dnf`, `pacman`,
   `port`) that yolo does not have and that goes stale. The honest version is a pack-declarable
   field — a pack that needs `redis` says how to get it outside a jail, once, in the manifest,
   rather than every yolo release guessing on every distro. That parallels the `install` rule
   exactly: **yolo names the remedy but never runs it below `jail`** (§4.1), so this is advice,
   not a second package manager.

   **Gap:** "verify system deps are present" needs a name for the dependency that is *not* the
   nixpkgs attribute. `packages: ["postgresql"]` is a nixpkgs attr; the Homebrew formula is
   `postgresql@16` and the binary is `psql`. Probing PATH for `postgresql` finds nothing on a
   machine that has it. So a real check needs the **binaries** a package provides, which nix can
   answer at jail level (the store path's `bin/`) and cannot answer at `guest`, where the package
   was never built. Most likely: packs and config declare `provides: ["psql"]` for the deps they
   care about, and unprobeable entries are reported as unprobeable rather than as present.

2. She applies:

   ```
   $ yolo apply
   guest (seatbelt)   user yolo-agent   packs claude,house-rules   surfaces 0 rendered
   ```

3. She launches, and Claude comes up. No MCP servers. No skills. No house rules. The agent
   greets her generically and immediately proposes a `git push --force`, which the house-rules
   pack exists specifically to forbid.

   **Gap — and this is the live defect, not a hypothetical.** `surfaces 0 rendered` was printed
   as *success*. On the `macos-user` backend the run path returns at `cli/run/run.go:73` before
   `stagePacks` ever runs, and `YOLO_PACK_ROOT` is never set, so
   `LoadJailPacks`/`ConfigurePackSurfaces`/`RunPackHooks` (`entrypoint/darwin.go:57-62`) loop
   over an empty list on every single launch. Eleven surfaces are declared; zero render; nothing
   errors. `docs/design/macos-user-nix-and-features.md:174` still claims pack selection works.

   The design doc's own §8 says it out loud: *"`guest` must actually work before any of this is
   honest. A three-notch story with a broken middle is worse than a one-notch story that
   works."* Priya is what that sentence looks like from the outside.

   > [!NOTE]
   > **G3 is FIXED — verified 2026-08-23. Do not go hunting for this bug.** The code fix is
   > commit **`a39628ad`** (2026-08-12, *"fix(run): stage packs BEFORE the backend dispatch —
   > macos-user rendered none"*); the doc correction is `2bb792ff` the same day. Every element of
   > the paragraph above is now stale:
   >
   > - Staging moved **above** the backend dispatch — `o.stageRunPacks(cname)` at
   >   `internal/cli/run/run.go:103`; the macos-user branch begins at `:112` and returns at
   >   `:155-156` **passing `staged.root` through**. The cited `run.go:73` is now inside a
   >   container-only repo-root gate that macos-user skips.
   > - `YOLO_PACK_ROOT` is set at `internal/macosuser/runplan.go:208-211`, and deliberately left
   >   unset when nothing was staged, so "no packs" is stated by absence.
   > - Three plan invariants at `internal/macosuser/runplan.go:302-320` refuse exactly the silent
   >   shapes this defect had (a root outside the state dir, a root nothing stages, a root never
   >   baked into the bootstrap argv).
   > - `internal/entrypoint/darwin.go:59-62` still holds the
   >   `LoadJailPacks`/`ConfigurePackSurfaces`/`RunPackHooks` sequence — the machinery was always
   >   real; it now receives a populated root (`internal/entrypoint/packsurfaces.go:87`).
   > - Pinned by `internal/cli/run/packstagedispatch_test.go:87` (the handler receives a pack root
   >   that **exists on disk** with `_official/claude/pack.json` in it) and `:131` (the empty-config
   >   half), plus `internal/macosuser/packroot_test.go`.
   > - `docs/design/macos-user-nix-and-features.md:174` no longer claims selection works: it now
   >   reads ⚠️ *"Wired 2026-08-12 (B-0); UNVERIFIED on a Mac"*, with a retained blockquote at
   >   `:178-195` recording that the old ✅ row had never been true.
   >
   > **The honest residue:** the row is ⚠️ rather than ✅ because no Mac has exercised the
   > `sudo -u _yolojail` staging step or the sandbox-uid read. That is a *verification* gap, not
   > this defect. Priya's story survives as the argument for the invariants at
   > `runplan.go:302-320` — which is what a fixed bug's story is for.

4. She only catches it because she compares the description to reality:

   ```
   $ yolo config ls
   SURFACE          PATH                                       CODEC  MODE     LAYERS
   claude/config    ~/.claude.json                             json   rmw      defaults managed
   claude/settings  ~/.claude/settings.json                    json   capture  host computed managed
   copilot/mcp      ~/.copilot/mcp-config.json                 json   copy     defaults computed
   mise/config      ~/.config/mise/config.toml                 toml   capture  computed
   … 7 more

   $ yolo diff
   config   11 surfaces declared, 0 present   ← the renderer did not run
   ```

   **Gap:** `yolo diff` is the only thing in the design that would have caught this, and it is
   the verb a new user is least likely to run. The 2026-07-26 ruling was **"config/pack generator
   failure is fatal: loud and halting, the jail does not start"** — but a generator that *never
   runs* isn't a failure, it's an absence, and absence slips past a fail-loud rule aimed at
   errors. `apply` needs to compare rendered-count against declared-count and refuse the launch
   on a shortfall, not report the shortfall as a number in a success line.

5. She needs to unblock two developers today, so she reaches for the escape valve:

   ```
   $ yolo apply --at host --dry-run
   host   surfaces 4 would render   3 refused
     ✗ install (claude)        packs never install software outside a jail
                               → install it yourself: npm i -g @anthropic-ai/claude-code
     ✗ mounts (house-rules)    needs a mount namespace — refused, never emulated
     ✗ retireMiseTools         no mise-managed toolchain here
   ```

   The `install` refusal is the design working exactly as specified — a pack's `installerUrl` is
   curl-to-shell, and running it against a developer's real machine is a different product with
   a different threat model. She writes the `npm i -g` line into her onboarding doc, which is the
   correct outcome: refused by name, with the manual alternative.

6. Then she loses data.

   Her fleet-wide `claude/settings` surface has captured edits she wants to discard, so she runs
   the command the diff output told her to run:

   ```
   $ yolo config reset mise
   ✓ mise/config reset to the pure render
   $ wc -c ~/.config/mise/config.toml
   1 /Users/priya/.config/mise/config.toml
   ```

   Her real `~/.config/mise/config.toml` — 20 bytes, a `[tools]` block pinning node and python
   for every non-agent project on her machine — is now a single newline.

   **Gap — probed, real, and filed as BACKLOG G1 (⚠ data loss).**
   `truncateSurfaceToPureRender` (`cli/configdiff.go:381`) resolves `~` through
   `paths.Home()`, which host-side is **the invoking human's home**, and it composes with no
   computed layer. `reset codex`/`reset opencode` replace real files with yolo's managed keys
   only; `reset claude` merges yolo's managed layer into the user's own file. The fix is a
   one-line predicate that already exists — `surfacesAreLocal()` (`configls.go:341`) — and is
   currently consulted only by `composedFileExists`. `configCapture`'s own docstring
   (`:415-419`) explains why a host-side re-render is wrong, one function away in the same file.

   This is the risk the confinement dial creates in general and this story in particular: **the
   moment a verb can run at `host`, every path that quietly assumed "my home is the jail's home"
   becomes a loaded gun.** Priya didn't opt into a host operation. She ran a command a jail told
   her to run.

   > [!NOTE]
   > **G1 is FIXED — verified 2026-08-23. So is its twin G2.** Commit **`1220ac55`** (2026-08-01,
   > *"feat(config): Phase 0 — refuse destructive host-side reset/capture"*), hardened by
   > `2b317dba` (2026-08-02).
   >
   > - The guard is `refuseHostSideWrite` (`internal/cli/configdiff.go:84-93`): it proceeds only
   >   when `surfacesAreLocal() || force`, otherwise it prints *"refusing — these surfaces resolve
   >   against a real home, not a jail's"* and aborts.
   > - It is wired at **both** destructive verbs, before any surface enumeration:
   >   `internal/cli/configdiff.go:645` (`configReset`) and `:848` (`configCapture` — that is
   >   **G2**, the privacy leak, closed by the same predicate the plan predicted).
   > - `truncateSurfaceToPureRender` still exists and still resolves `~` through `expandHome`
   >   (`internal/cli/configdiff.go:804-827`) — it has simply become **unreachable host-side**: its
   >   only caller sits at `:684`, downstream of the `:645` guard. Reachable via explicit
   >   `--force` only, which is the escape hatch plan §0.1 specified.
   > - `surfacesAreLocal()` moved from `configls.go:341` to `internal/cli/configls.go:385-393`,
   >   and `2b317dba` tightened it to require `workspaceRoot() == "/workspace"` — so a *different*
   >   workspace's surfaces inside a nested jail also count as non-local.
   > - Plan item 0.3's regression test exists:
   >   `internal/cli/configdiff_test.go:172` `TestResetCaptureRefuseHostSideWithoutForce`.
   > - The capture-on-terminate path added later (`internal/cli/configcapture.go:63`) runs on the
   >   host **by design** and avoids the leak structurally — it resolves through
   >   `jailHomeHostPath`, never `expandHome`, and the prohibition is documented at
   >   `configcapture.go:36-45`.
   >
   > The sentence in bold above is still the right lesson and is why the guard is a *predicate at
   > the verb*, not a fix to one function.

**What would trip them up:**

- Two silent-success failures in one sitting (`surfaces 0 rendered`, `reset` on a real file), and
  neither prints a warning.
- `yolo config reset` reads as scoped to yolo's own state. Nothing in its name or output says it
  can write outside the jail.
- She'd have shipped `guest` to 28 machines on the strength of step 1's clean `check --at`
  output. `check` validated the *description*; nothing validated that the notch renders.

**What makes this work — once it does:**

- `check --at guest` is genuinely the best thing in the design. Ten lines told her which of her
  ~25 config keys are inert on the notch she chose, before she distributed anything. It becomes
  the best thing *by a distance* if each inert key also probes and remedies (step 1): the
  difference between "yolo doesn't do this" and "yolo doesn't do this, you already have 1 of 2,
  run `brew install redis` for the other."
- Refusing `install` by name, with the manual command, converts a security rule into a
  documentation line instead of a mystery. The remedy lines generalize that pattern: **yolo names
  what it will not do, and hands you the next command anyway.**

**Technical reality check:** the three notches are not equally real. `jail` is production,
`host` is a design, `guest` renders zero surfaces per launch on the one platform it exists on.
The BACKLOG order (G1 → G2 → G4 → G3) puts the data-loss fix first and the silent-zero fix
fourth, behind the `internal/render` collapse that makes it a two-line change. That order is
right, and this story is the argument for not announcing three notches until G3 lands.

> [!NOTE]
> **Reality check, re-checked 2026-08-23.** The Stage-G order ran to completion: G1/G2 fixed
> 2026-08-01, G4/G5 shipped as `internal/render/` (`target.go`, `fieldset.go`, `modes.go`,
> `confinement.go`), G3 fixed 2026-08-12, G6 shipped as `apply --host`
> (`internal/cli/apply.go:63`). The notch scoreboard today:
>
> - **`jail`** — production, unchanged.
> - **`host`** — no longer "a design": `yolo host apply` renders the applicable kinds into the real
>   home under `observe`/`assert` postures, and an inapplicable kind is refused **by name**
>   (`internal/render/fieldset.go:36-63`). Its unbuilt half is the confirm-gated install (plan
>   4.3) — the shipped behaviour is a flat refusal plus a static note pointing at Phase 4.3
>   (`internal/cli/applyhostdeps.go:113-116`).
> - **`guest`** — no longer renders zero on macOS (G3), but Phase 7 is still unbuilt: its mode
>   policy is explicitly `UndecidedModes("the guest notch's mode policy is Phase 7's to state")`
>   at `internal/render/modes.go:185`, and Linux `guest` is a *profile constant*
>   (`render.GuestProfileLinux()`, `internal/render/confinement.go:132-136`) with **no bwrap or
>   Landlock execution code anywhere** — see Q7.
>
> Two of story 2's four verbs also shipped: `yolo apply --at host --dry-run` (step 5) is real
> (`internal/cli/apply.go:54,662`), and step 1's probe-and-remedy half landed as its **own verb**,
> `yolo check-deps` (`internal/cli/checkdeps.go`, over pack-declared `install_hints` in
> `internal/depcheck/`). What did **not** ship is `check --at <notch>` itself: `--at` is parsed
> only by `apply` (`internal/cli/apply.go:54`), and `yolo check` has no notch flag. So Priya's
> step 1 — the thing this story calls "genuinely the best thing in the design" — is still
> unavailable in the shape she uses it.

---

## 3. Derek — Solo Dev, Wants the Simple Version, Never Says the Word "Confinement"

**Context:** Derek has a Django side project, one repo, one laptop, no team. He heard yolo
described as "an agentic dev environment manager" and immediately assumed it was too much tool
for him. He does not want a dial. He wants Claude to not delete his home directory.

**First 10 minutes:**

1. ```
   $ cd ~/code/parkbench
   $ yolo init
   ✓ Config ready: /Users/derek/code/parkbench/yolo-jail.jsonc
   ```

2. ```
   $ yolo -- claude
   No packs are configured, so this jail has no coding agent.
   An agent arrives as a pack. The packs yolo ships are selected by name —
   add one to ~/.config/yolo-jail/config.jsonc:  "packs": ["claude"]
   ```

   Nothing is on by default, and the notice says the one thing he can act on. Six seconds of
   confusion, resolved by the confusion itself.

3. ```
   $ yolo init-user-config          # writes ~/.config/yolo-jail/config.jsonc
   $ # edit it: "packs": ["claude"]
   $ yolo -- claude
   yolo-jail 0.7.1+233.g1a2df8d | darwin/arm64 | container | yolo-parkbench-a1f9c204
   📦 Provisioning tools...
     ↳ bootstrap
   ⚡ Executing: claude
   ```

   Done. Derek's yolo career is now complete. He never types `--at`, never types `apply`, never
   reads the confinement table, and never learns that a dial exists.

**This is Layer 1.** Three commands, one of which is `cd`. Everything else in this document
builds on top of it, and none of it is required:

- **Layer 2 — a package.** `"packages": ["postgresql"]` in `yolo-jail.jsonc`. The agent gets
  `psql`; his laptop doesn't. First time he feels the jail as a *feature* rather than a fence.
- **Layer 3 — house rules.** A `file:///Users/derek/code/my-pack` pack with two skills and an
  AGENTS.md fragment. Now the agent follows his conventions in every project. This is the layer
  where "environment manager" starts to mean something to him, and he still hasn't touched
  confinement.
- **Layer 4 — `apply`.** He wants CI to pre-build the image at 6am so his morning launch is
  instant: `yolo apply` in a cron job, no agent, no exec. The first verb he learns that isn't
  "run."
- **Layer 5 — `apply --sealed`.** He buys a second laptop, and rather than compare the two he
  wants each one to be what he declared. Sealing is the layer where his definition starts binding
  the way his `requirements.txt` does (story 1).
- **Layer 6 — the dial.** One afternoon he needs the agent to fix his *global* git config, which
  lives on the host by definition. `yolo --at host -- claude`. He gets his pack's skills and
  house rules on his real machine for the first time, plus a list of what didn't come along.

Derek reaches Layer 3 in month two and Layer 6 possibly never. **The design's central claim is
that this ordering holds** — that a user can get all the value of the description while
believing yolo is just a box, and only meet the dial when a real need drags them to it.

**Gap:** nothing in the CLI teaches this ladder. `yolo --help` lists 12 subcommands in one flat
block, and the new design adds `apply`, `describe`, and `diff` to it. Derek's Layer-1 experience
survives because he types `yolo -- claude` and never reads `--help`; the moment he does read it,
he sees a platform tool. A `yolo --help` that leads with the three commands 90% of users need
(`yolo -- <cmd>`, `yolo init`, `yolo check`) and defers the rest behind `yolo help advanced`
would preserve the ladder in the interface, not just in the docs.

**What makes this work:**

- **The strongest notch is the default and is never inferred.** Derek gets full confinement by
  doing nothing, which is the only defensible default for someone who hasn't read anything.
- **The name still works.** "yolo jail" reads as "yolo, at the jail level," and Derek — who
  thinks it's a jail product — is not wrong. He's using the default notch.
- The empty-packs notice is doing real onboarding work. It is the only error in the product that
  a brand-new user is *supposed* to hit.

---

## 4. Ash — The Agent, First Run at `host`, Reading Its Own Briefing

**Context:** Ash is a Claude Code instance. Every previous run has been inside a jail: workspace
bind-mounted at `/workspace`, disposable home, no host credentials, `git push` impossible because
there is no SSH key to push with. Their briefing has told them this, in the same words, on every
boot. Today the human runs `yolo --at host -- claude` because a Homebrew-installed Python is
shadowing a pyenv shim and the problem does not exist inside a container.

**What happens today:**

Ash reads `AGENTS.md` and finds, generated by `internal/jailcontent/briefing.go`:

```markdown
# YOLO Jail Environment

You are running inside a YOLO Jail — a sandboxed container.
...
- **OS**: NixOS-based minimal container (no systemd, no sudo)
- **Workspace**: `/workspace` is the host directory ..., bind-mounted LIVE
```

Every load-bearing sentence is false. There is no container. There is `sudo`. `/workspace` does
not exist — the repo is at `/Users/derek/code/parkbench`. The built-in `jail-startup` skill fires
and its name is a lie.

Ash then does what a jailed agent reasonably does: to test the pyenv theory they run
`rm -rf ~/.pyenv && brew uninstall python@3.12`, because in a jail that is a free experiment you
undo by restarting the jail. **There is no jail to restart.** Ash has just uninstalled the
human's Python.

This is not a hypothetical failure mode. It is the specific reason §6 of the design exists: *an
agent that believes it is disposable when it is not will take a disposable agent's risks.*

**What happens with a self-describing briefing:**

1. Same command. Different first 40 lines of context:

   ```markdown
   ## Your environment
   Confinement: host — this is the human's real machine. Changes are not disposable.
   You have: their real credentials, their real dotfiles, no snapshot to fall back on.
   Absent: nothing is mounted read-only; there is no jail to restart.
   Workspace: /Users/derek/code/parkbench (this is also just a directory on their disk)
   ```

2. Ash's plan changes at the first destructive step. Instead of `rm -rf ~/.pyenv` they run
   `pyenv versions`, `brew --prefix python@3.12`, and `which -a python3`, then propose a
   `PATH` reorder in `~/.zshrc` and *ask before writing it*. Same model, same skills, same pack
   prose — one accurate paragraph changed the risk posture.

3. Ash needs a tool that isn't installed and reaches for the pack that would install it:

   ```
   $ yolo pack install
   ✗ refused: packs never install software outside a jail (confinement: host)
     claude declares install.installerUrl — running it here would execute a
     remote script against /Users/derek. Install it yourself, or re-run at
     confinement: jail.
   ```

   **What makes this good for an agent specifically:** the refusal names the field
   (`install.installerUrl`), the reason, and both remedies. An agent can act on that. A generic
   "not supported on this platform" would send Ash hunting for a workaround, which is the
   failure mode where agents do the most damage.

4. Ash finishes, and the human relaunches at `jail` an hour later for normal work.

   **Gap:** the briefing is a *file* — `AGENTS.md` in the workspace, plus `.claude/` skills —
   and at `host` it is rendered into a real directory that persists after the run ends. If the
   next launch is at a different notch, or if the human opens Claude Code directly without yolo,
   Ash reads a `host` briefing inside a jail (or worse, a `jail` briefing on the host). The
   briefing must either be stamped with the notch it was rendered for and *re-verified at
   startup*, or it must never be left behind at `host`. The design says the briefing states the
   level; it does not say what makes that statement stay true.

   Concretely, the check is cheap and belongs in the built-in `jail-startup` skill: if the
   briefing claims `confinement: jail` and `/workspace` does not exist, halt and say so.

   **Gap:** `jail-startup` is the wrong name for a skill that runs at three notches. It is
   injected into every environment (`internal/jailcontent/skills.go`) and reads `.yolo/handover.md`,
   neither of which is jail-specific.

   > [!NOTE]
   > **Overtaken by two separate shipments — verified 2026-08-23.**
   >
   > 1. **The briefing now states the notch (plan Phase 8.1, shipped).** `confinementHeader`
   >    (`internal/jailcontent/briefing.go:86-170`, called at `:287`) emits a per-notch opening
   >    block with a dedicated `host` body at `:124` (*"this is the human's REAL…"*) and a `guest`
   >    body at `:133`, plus a derived enforcement tail (`enforcementLines`, `:171`). Step 1's
   >    "different first 40 lines of context" is substantially real; the story's *"every
   >    load-bearing sentence is false"* describes 2026-07-27, not today.
   > 2. **There is no `jail-startup` skill to rename.** yolo's built-in suite is now exactly
   >    `configuring-the-jail`, `developing-yolo-jail`, `diagnosing-the-jail`
   >    (`internal/jailcontent/builtinskills/`). The startup-ritual skill (`n`) was **deleted**;
   >    the one-time handoff became a conditional **Handoff** section in the briefing consumed by
   >    the run pipeline (`internal/jailcontent/briefing.go:69`; see
   >    [host-to-jail-handoff.md](host-to-jail-handoff.md)). The `jail-startup` a reader may have
   >    on their machine is a *user-level* skill, not a yolo built-in.
   >
   > **What survives, and it is the load-bearing half:** nothing stamps a rendered briefing with
   > the notch it was made for, and nothing asserts that stamp against observable reality at
   > startup. The generator is per-notch; the *artifact* is still unlabelled. That is Q4, and it
   > is still open — but its "rename `jail-startup`" clause is now moot.

**What would trip them up:**

- An agent cannot verify its own confinement without being told. `uname` and `/proc` distinguish
  container from host on Linux; on macOS `guest` vs `host` is *invisible from inside* until you
  hit a Seatbelt denial. The briefing is not a convenience, it is the only channel.
- Ash has no way to ask. There is no `yolo describe` output in their context unless the human
  pastes it, and no reason for them to run it — the briefing is the thing they're trained to
  trust.

**What makes this work:**

- The briefing is **generated**, not written. Adding one accurate paragraph per notch costs one
  function and cannot drift from the config, because it *is* the config.
- Every refusal names a pack field. That turns the confinement dial from a mystery into an API an
  agent can reason about.

**The aha moment:** step 2, and it belongs to the human, not the agent. Derek watches Ash *ask*
before touching `~/.zshrc` and realizes the environment description reached the agent's judgment,
not just its `PATH`.

---

## 5. Lisa — Head of Engineering, Filling In a Security Questionnaire, Doesn't Read Code

**Context:** Lisa runs a 31-person engineering org. A customer's vendor-security review has
landed with question 14: *"Describe any AI coding tools with access to source code, and the
controls applied."* Nine of her engineers use yolo. She has never run it. She has 40 minutes
before the response is due and she is not going to read `docs/design/`.

**The QA moment:**

1. An engineer pairs with her and runs, in his repo:

   ```
   $ yolo describe
   environment  parkbench @ /Users/nikhil/code/parkbench   confinement  jail (container)
   tools        31 nix packages · flake.lock 8f2a1c…       mise: node@22.23.1
   agents       claude (pack, embedded)                    launcher --dangerously-skip-permissions
   knowledge    3 skill trees (built-in < house-rules < user) · AGENTS.md 4 sources
   config       11 composed surfaces                       2 with captured edits
   services     claude-oauth-broker, host-processes
   grants       /workspace rw · 2 host files ro · no network holes
   description  sha256:4c1f8ad2…
   ```

   Lisa reads exactly two lines of this: `confinement jail (container)` and `grants /workspace rw
   · 2 host files ro · no network holes`. She writes into the questionnaire: *"Agents run in a
   container with access only to the project directory and no network access."*

   **The second half of that sentence is wrong.** Bridge networking is the default. The agent has
   unrestricted outbound internet — that is how `npm install` and the model API work at all.
   `no network holes` means *no inbound port forwards*, which is a completely different claim,
   and the phrasing invites precisely the misreading Lisa just committed to a customer document.

   **Gap:** `describe` has no egress line, and its `grants` row uses a word (`holes`) whose
   direction is implicit to whoever wrote it and ambiguous to everyone else. What she needs:

   ```
   grants       /workspace rw · 2 host files ro
   network      outbound: unrestricted (bridge) · inbound: none · host ports: none
   credentials  omitted: ~/.ssh, ~/.gitconfig, ~/.aws, gh token
                present: Claude OAuth (brokered, host-held)
   ```

   The `credentials` row is the answer to question 14 and it is the one row `describe` doesn't
   print. The whole credential story in this product is **an omission** — `~/.ssh` is absent
   because nothing mounts it — and an omission is exactly the kind of fact that never shows up in
   output unless someone deliberately enumerates it.

2. She asks the question every non-technical stakeholder asks: *"What's the worst it can do?"*

   The engineer has no command for that. He has `yolo check`, which reports on health:

   ```
   $ yolo check
   ...
   Summary
     22 passed, 2 warnings
   ```

   **Gap:** `22 passed` reads to Lisa as a security audit result. It is a *readiness* result —
   runtime present, nix healthy, image built, no stale jails. Nothing in it is about exposure. A
   description-oriented product needs the exposure view to be a first-class output, not something
   a reader assembles from a health check and a grants line.

   The shape that would have answered her in one command:

   ```
   $ yolo describe --exposure
   confinement  jail (container) — strongest notch, default

   CAN REACH
     /Users/nikhil/code/parkbench      read-write   (the workspace, live — not a copy)
     ~/.claude/settings.json           read-only    (host file grant)
     ~/.gitconfig                      read-only    (composed identity)
     the internet                      outbound, unrestricted
     Claude API                        via host-held OAuth token (never copied in)

   CANNOT REACH
     everything else on this machine   no mount exists
     ~/.ssh, ~/.aws, gh/gcloud tokens  omitted by default
     inbound connections               none listening
     other yolo workspaces             separate homes

   ESCALATION AVAILABLE TO THE USER
     yolo --at host -- <cmd>           runs with the human's full identity — no confinement
   ```

3. That last block is the one she cares about most, and it is the honest cost of the dial. Her
   follow-up: *"So any engineer can turn this off?"*

   Yes. `--at host` is a flag, and the design has no org-level control over it.

   **Gap:** there is no way to say "this machine may not run at `host`," and no audit trail when
   someone does. The design's §8 admits `host` will be over-used and calls it a
   product-discipline risk with no technical fix. That's defensible for a solo user; it is not an
   answer for Lisa, who has to write something into a customer document. The minimum viable
   control is that the *user config* can pin `"maxConfinement": "jail"` — a floor she can
   distribute — and that `--at host` logs to the journal.

   Notably, `apply --sealed` *would* give her the fleet sentence she actually needs — not "all 31
   machines match each other" but "all 31 machines are what we declared, and the run fails
   otherwise." A vendor questionnaire wants a binding control, not a consistency observation, and
   sealing is the only thing in this design that is one. It just isn't reachable from anything she
   saw in 40 minutes.

**What would trip them up:**

- Every number in `describe` is a count, and Lisa needs nouns. `2 host files ro` is not an answer
  to "which files."
- `no network holes` is the single most dangerous string in the design: technically precise,
  directionally ambiguous, and sitting in the output a non-technical reader will quote.
- `yolo check`'s `22 passed` is read as an audit. It isn't one.

**What makes this work:**

- Confinement being an explicit, named, printed attribute — rather than something inferred from
  which backend happens to be installed — is what makes the first line of `describe` quotable at
  all. Today `runtime: "macos-user"` answers "how confined am I?" and "by what mechanism?" in one
  word, and `config-ref` has to explain in prose that one of the three values is *"a WEAKER
  isolation boundary than a container/VM."* Lisa could not have parsed that. `confinement:
  guest` she can.
- The credential boundary being **omission** rather than policy is genuinely the strongest answer
  to question 14 — there is no allowlist to misconfigure. It only needs to be *printed*.

---

## Technical Architecture

### Verbs × notches

| Verb | `jail` | `guest` | `host` |
|---|---|---|---|
| `yolo -- <cmd>` | launch container | launch as the guest identity | exec in place |
| `yolo apply` | build image, stage packs, render | render into the guest home | render applicable subset into real home |
| `yolo describe` | full | full, minus image/mounts | full, minus image/mounts/network/resources |
| `yolo config diff` | per-surface: captured vs rendered | same | vs real files (`rmw` + sidecar) |
| `yolo check --at <n>` | all keys apply | inert ones named, probed, remedied | same, plus the refused ones |
| `yolo apply --sealed` | refuse on any undeclared input | same | meaningless (the host *is* undeclared input) |
| `yolo pack install` | ✓ | ✓ | **refused, by field name** |
| `yolo config reset` | ✓ | ✓ | **must refuse unless `surfacesAreLocal()`** (G1) |

**Shipped-status of that table, verified 2026-08-23.** `yolo -- <cmd>`, `apply`, `apply --at host`,
`apply --sealed`, `describe`, `config diff` and `config reset`'s host refusal are all real
(`internal/cli/apply.go`, `describe.go`, `configdiff.go:84-93`). The last row is **done**: the
"must refuse" is now `refuseHostSideWrite`. `check --at <n>` is **not** built — its probe half
shipped as the separate verb `yolo check-deps`. `pack install`'s host refusal is expressed as a
`FieldSet` refusal naming `program` (`internal/render/fieldset.go:38`), which is the flat
"refused" of the original rule rather than the design doc §4.1 *confirm-gated* position — see
plan Phase 4.3, unbuilt.

### The closure: what is in the definition, and what escapes it

Story 1's finding, as a table. "Declared" is the nix test — is this input named in something the
user wrote or pinned?

| Input | Declared? | Binds today? |
|---|---|---|
| `packages[]` + `flake.lock` | ✓ | ✓ — the part that already works like nix |
| pack set + locked commits | ✓ | ✓ |
| `mise_tools` | ✓ | ✓ |
| composed surfaces (`defaults`, `managed`) | ✓ | ✓ |
| `computed` layer | ✓ (derived from jail paths) | ✓ — a function of the definition |
| **`host` layer** (`HostSource`) | ✓ **declared by the pack**, but its *content* is machine state | partly — the nix analogue is a fixed-output derivation |
| **capture overlay** | ✗ **nothing declares it, and it outranks everything** | ✗ — the closure-breaker |
| **`yolo-jail.local.jsonc`** | ✗ untracked, auto-merged, no `include_if_found` needed | ✗ by design |

`--sealed` is exactly: refuse on a row in the bottom two, report the `host` row as a declared
impurity. **Not** "no host reads" — the `host` layer is how a user's real Claude settings reach the
jail at all, and killing it would break the feature packs exist to provide.

> [!NOTE]
> **Half of this table is now enforced — verified 2026-08-23.** `applySealed`
> (`internal/cli/apply.go:617-651`) implements the *refusal* half exactly: it refuses on
> `yolo-jail.local.jsonc` (`:623-630`) and on any surface carrying outstanding overlay keys
> (`:631-638`). The *report* half — printing the `host` row as a declared impurity — is **not**
> implemented; `applySealed` prints only refusals or a bare `sealed` line, and `describe` never
> names a host layer at all (Gap 1's verdict). Two corrections to the table itself:
>
> - The **capture overlay** row's "outranks everything" is wrong as a claim about the compose
>   stack — it loses to `computed`/`transform`/`managed` (`internal/agentcfg/compose.go:357-379`).
>   It remains an **undeclared** input, which is what the row is actually for, so the tier is
>   right and the parenthetical is not.
> - The **`host` layer** row is under a standing decision to be *removed*, not reported: design
>   doc §3.3 / plan OQ-3 resolved 2026-08-01 to retire the read-in layer in favour of a local
>   pack. Unimplemented as of today.

### What goes into the description hash

```
resolved config      user + workspace (+ yolo-jail.local.jsonc, which is why sealing exists)
pack set             names + locked commits (yolo pack status)
tool set             flake.lock rev + packages[] + mise_tools[]
composed surfaces    the rendered bytes of every surface
captured edits       ??? — see open question 1
```

A hash over an *unsealed* environment is worse than no hash: it looks authoritative and moves for
reasons the user cannot enumerate. Under a sealed definition the hash is a `flake.lock` rev — a
cache key and a CI pin — which is the only job it should have.

### The failure that both story 2 and story 4 are instances of

```
  declared  ──render──▶  rendered  ──▶ used
     11                     0              ← story 2: shortfall reported as success
     11                    11              ← healthy
      ?                    11              ← story 4: rendered artifact outlives its notch
```

Two directions, one missing invariant: **nothing compares the declaration to the rendering, and
nothing stamps a rendering with the notch it was made for.**

---

## Open Questions

**All eleven are still live as of 2026-08-23** — none has been answered by a ruling. Several have
been *partly overtaken by shipped code*, and each of those now carries a `_Shipped since:_` line
saying what moved and what the question still decides. **IDs are cited from
[`../plans/roadmap.md`](../plans/roadmap.md) 💬 7 (Q1, Q7) — do not renumber, do not delete.**

1. 💬 **Q1 — whether the capture overlay may outrank the definition at all.**
   This is the closure question, and it is the biggest one in the document. Capture is a real
   feature — humans and agents edit config in-jail, and silently discarding those edits is hostile
   — but a captured value wins over every declared layer while nothing declares *it*. Story 1 is
   what that costs: two language servers in the winning layer that no config, pack, or lockfile
   mentions.

   _Leaning:_ capture stays, but becomes a **staging area rather than a layer**: captured values
   are recorded, reported, and *proposed* (`yolo config promote claude` writes them into a pack or
   the workspace config), and `apply --sealed` refuses while any are outstanding. That keeps the
   feature, keeps the definition binding, and makes "capture, and I meant it" expressible — it
   means "promote it." The alternative (capture keeps winning, sealing is opt-in) leaves the
   product unable to make nix's promise, which is the promise story 1 came for.

   _Shipped since (2026-08-23):_ **the leaning is exactly half-built, and the built half is the
   easy half.** `apply --sealed` refuses while any capture is outstanding
   (`internal/cli/apply.go:617-651`) — that is the "sealing refuses" clause. But **`yolo config
   promote` does not exist** (`internal/cli/config.go:33-60`: `ls, render, diff, reset, capture,
   drift, dump`), so capture is still a *winning layer* with no promotion path, and the refusal
   message at `apply.go:635` advises a verb that is not there. Two corrections to the question's
   premise, neither of which retires it: capture loses to `computed`/`transform`/`managed`
   (`internal/agentcfg/compose.go:357-379`), so "outranks every declared layer" is true only of
   the lower half of the stack; and the closure hole is now *detectable* even though it is not
   closed. **Q1 still decides the staging-area-vs-layer question**, which is the part nothing has
   built.

   **Answer:**
   > _(empty — fill in when decided)_

1a. 💬 **Q1a — whether `--sealed` is a flag or the default.**
   As a flag, the property is off until a user knows to ask, and nobody arriving from nix expects
   to opt into purity. As the default, capture and `yolo-jail.local.jsonc` — both deliberate,
   both shipped — become errors on first use.

   _Leaning:_ default-on for `apply` in CI (non-TTY), flag-on interactively, with the interactive
   path *printing* the open-closure summary every apply rather than refusing. Same information,
   two severities, chosen by whether a human is there to act on it. Pair with question 1's
   promote verb, or the interactive notice is just the boot banner again — visible and unbinding.

   _Shipped since (2026-08-23):_ **it shipped as a plain flag, defaulting off** — parsed at
   `internal/cli/apply.go:69-70`, dispatched at `:101-102`, and the design doc §3.3 argues for
   exactly that. The question is **not** thereby closed: its leaning proposed a third shape
   (default-on when non-TTY, flag-on interactively, with the interactive path *printing* the
   open-closure summary), and `applySealed` consults no TTY state at all. So today the flag is
   opt-in everywhere including CI, which is the one place the leaning wanted it mandatory.

   **Answer:**
   > _(empty — fill in when decided)_

1b. 💬 **Q1b — how far the "inert key" handoff goes.**
   Story 2 step 1 wants an inert `packages` to probe whether the dep is present anyway and print a
   remedy. That needs two things yolo lacks: a mapping from nixpkgs attr to native package manager
   formula, and the *binaries* a package provides (`packages: ["postgresql"]` → `psql`, not
   `postgresql`), which nix can answer at `jail` and cannot at `guest`.

   _Leaning:_ pack- and config-declarable `provides: ["psql"]` plus an optional per-notch
   `remedy` string, and report unprobeable entries as unprobeable rather than as present. yolo
   names the remedy and never runs it below `jail` — same rule as `install` (§4.1), so this is
   advice, not a second package manager. A built-in attr→brew/apt table is the tempting version
   and it goes stale on every distro release.

   _Shipped since (2026-08-23):_ **substantially built, and the leaning won** (plan Phase 6).
   Packs declare per-manager `install_hints` (`internal/depcheck/depcheck.go:48`,
   `internal/entrypoint/requires.go`), a shared probe reports present/missing/unprobeable
   (`internal/cli/applyhostdeps.go:168-191` distinguishes "no hints for this host's manager" from
   "no hints at all"), and the manifest emits as a `~/.config/yolo/Brewfile` and kin
   (`internal/cli/checkdeps.go:145-156`). What is left of the question is the **delivery shape**:
   the probe lives in `yolo check-deps` and in `yolo host apply`'s report, not in a `check --at
   <notch>` that names every inert key — and the *offer-to-run* half is explicitly deferred
   (`internal/cli/applyhostdeps.go:113-116`, `checkdeps.go:9-12`). The nixpkgs-attr-vs-binary
   naming problem is answered by hints, not by a `provides` field.

   **Answer:**
   > _(empty — fill in when decided)_

2. 💬 **Q2 — whether `apply` may report a shortfall as success.**
   `surfaces 0 rendered` is the shipped `macos-user` behavior (BACKLOG G3) and it printed as a
   normal success line in story 2. The 2026-07-26 ruling made generator *failure* fatal, but an
   absent generator isn't a failure.

   _Leaning:_ `apply` compares declared-count to rendered-count and is fatal on any shortfall not
   explained by a named refusal. "Refused, by name" is fine; "rendered fewer than declared, no
   reason given" is a halt. That makes G3 impossible to reintroduce rather than merely fixed.

   _Shipped since (2026-08-23):_ **G3 is fixed but the invariant is not** — which is exactly the
   distinction this question was raised to force, so it is *more* live now, not less. There is
   still **no declared-count-vs-rendered-count reconciliation** anywhere. What exists instead is
   three narrower guards: per-surface A12 fatality on a render *error*
   (`internal/entrypoint/packsurfaces.go:153-171`), a per-*kind* three-outcome census
   (refused / honored-but-unbuilt / rendered — `internal/cli/apply.go:311-350`), and a whole-pack
   inertness refusal when a pack contributes nothing (`internal/cli/apply.go:580-596`). The
   macos-user instance is closed by backend-specific plan invariants
   (`internal/macosuser/runplan.go:302-320`), not by a general rule. So "eleven declared, zero
   rendered, nothing errors" is still representable on any *future* target.

   **Answer:**
   > _(empty — fill in when decided)_

3. 💬 **Q3 — whether `diff` is a top-level verb at all.**
   The first draft of story 1 was built on comparing two machines, and rewriting it around closure
   removed the need: a binding definition makes "compare my environments" a question you stop
   asking, and nix ships no such verb. What survives is `yolo config diff`, which already exists
   and answers something narrower and real — *this surface's captured values vs. what yolo
   rendered* — i.e. it reports open closure on one file.

   _Leaning:_ don't promote `diff` to a top-level description-vs-description verb. Keep `config
   diff` as the per-surface impurity report, and let `apply --sealed` be the whole-environment
   answer. A cross-machine comparison is then a `describe --json` on each side and `diff(1)` — no
   verb, no description store, no cache invalidation.

   _Shipped since (2026-08-23):_ **the leaning is the built state, by omission.** No top-level
   `diff` was added (`internal/cli/dispatch.go`); `yolo config diff` keeps its per-surface job and
   `describe --json` exists (`internal/cli/describe.go`, help at `internal/cli/help.go:20`), which
   is the "diff(1) on each side" path. The question now decides only whether to *ratify* that or
   revisit it — the cheapest of the eleven to close.

   **Answer:**
   > _(empty — fill in when decided)_

4. 💬 **Q4 — whether a rendered briefing must be stamped with its notch.**
   Story 4's briefing is a real file in a real directory at `host`, and it outlives the run. A
   `host` briefing read inside a jail is merely confusing; a `jail` briefing read on the host is
   how someone loses `~/.pyenv`.

   _Leaning:_ stamp it (`confinement: host`, `rendered: <hash>`) and have the built-in startup
   skill assert the stamp against observable reality — if it claims `jail` and `/workspace` is
   missing, halt. Cheap, and it fails in the safe direction. Rename `jail-startup` while doing it.

   _Shipped since (2026-08-23):_ **the generator states the notch; the artifact still does not
   carry it.** `confinementHeader` (`internal/jailcontent/briefing.go:86-170`) emits a per-notch
   header with distinct `host` (`:124`) and `guest` (`:133`) bodies — plan Phase 8.1. But nothing
   stamps the rendered file with a hash or asserts it at startup, so a `host` briefing left on
   disk and read inside a jail is still unremarked. **The rename clause is moot:** there is no
   `jail-startup` built-in any more — yolo's suite is `configuring-the-jail`,
   `developing-yolo-jail`, `diagnosing-the-jail` (`internal/jailcontent/builtinskills/`), and the
   startup-ritual skill was deleted in favour of a Handoff section in the briefing
   ([host-to-jail-handoff.md](host-to-jail-handoff.md)). Which also removes the *place* the
   leaning proposed to put the assertion — so Q4 now has to name a new home for the check.

   **Answer:**
   > _(empty — fill in when decided)_

5. 💬 **Q5 — whether the user config can set a confinement floor.**
   Lisa needs to distribute "this machine may not run at `host`." §8 currently calls `host`
   over-use a product-discipline risk with no technical fix, which is true for one user and
   insufficient for 31.

   _Leaning:_ add `"maxConfinement"` at user scope only (never workspace — a workspace config is
   agent-editable, same reason `packs` is user-scoped), and journal every `--at` that lowers the
   notch. Not a security boundary — the user owns the machine and can edit their own config — but
   an honest answer to "is this the default here?"

   _Shipped since (2026-08-23):_ **nothing — and the surface it would bound is now real.** There
   is no `maxConfinement` key anywhere in `internal/config/`, and no journalling of a lowering
   `--at`. Meanwhile `--at` shipped on `apply` (`internal/cli/apply.go:54`) and the `confinement`
   key is live (`internal/config/confinement.go`), so Lisa's *"any engineer can turn this off?"*
   is now answerable with a concrete command rather than a design sketch. The question is
   unchanged and its stakes went up.

   **Answer:**
   > _(empty — fill in when decided)_

6. 💬 **Q6 — whether `describe` gets an exposure view, or exposure gets its own verb.**
   Story 5 wants nouns (which files, which credentials, which direction the network goes), and
   `describe`'s current shape is counts. Bolting `--exposure` onto `describe` keeps one verb;
   splitting it admits that "what is this environment" and "what can it reach" are different
   questions with different audiences.

   _Leaning:_ `describe --exposure` as a view, not a verb — same resolved description, different
   projection. But fix the `grants` line unconditionally: an explicit `network outbound:
   unrestricted` row belongs in the default output, because the current phrasing produced a wrong
   sentence in a customer document.

   _Shipped since (2026-08-23):_ **`describe` shipped without any of it — and, usefully, without
   the dangerous string either.** The real output is five rows (`internal/cli/describe.go:91-105`
   plus the confinement detail block at `:145-189`): `environment` + notch, `enforced by` (the
   composed primitives), `autonomy` ON/OFF, `packs`, `packages`, `description sha256:… (unsealed)`.
   There is **no `grants` row, no `network` row, no `credentials` row, and no `--exposure` flag**
   — so *"no network holes"* does not exist in the product and story 5's specific misreading is
   not reproducible today. That does not close Q6: it means the *whole* exposure view is unbuilt
   rather than half-built, and the row that would answer question 14 (`credentials`, the
   omission) is still the one row nothing prints.

   **Answer:**
   > _(empty — fill in when decided)_

7. 💬 **Q7 — whether Linux `guest` (bwrap + Landlock) is a promise or a hypothesis.**
   The design's three-row table lists it as the Linux mechanism for the middle notch, and that
   row is the evidence the dial is real rather than a story told about two macOS backends. No such
   code exists.

   _Leaning:_ mark it explicitly as unbuilt in the table until it runs, and do not ship the
   three-notch vocabulary in user-facing copy until `guest` renders surfaces on at least one
   platform (G3). Story 2 is what shipping it early costs.

   _Shipped since (2026-08-23):_ **still a hypothesis, and the vocabulary shipped anyway — so
   this is the question the tree has drifted furthest from.** There is **no bwrap and no Landlock
   execution code**: a repo-wide search for `bwrap`/`Landlock` in Go hits only a profile constant
   (`render.GuestProfileLinux()` = `PrimNamespaces | PrimLandlock`,
   `internal/render/confinement.go:132-136`), a primitive label (`:69`), a briefing test
   (`internal/jailcontent/briefingprofile_test.go:109-115`) and doc comments. The `guest` notch's
   render policy is explicitly unstated —
   `KindGuest: UndecidedModes("the guest notch's mode policy is Phase 7's to state")`
   (`internal/render/modes.go:185`). Meanwhile the three-notch vocabulary **is** user-facing:
   `confinement: jail|guest|host` validates (`internal/config/confinement.go:65-79`), `apply --at
   guest` parses, `describe` prints the notch, and the briefing has a `guest` body
   (`internal/jailcontent/briefing.go:133`). The leaning's precondition — *"do not ship the
   three-notch vocabulary until `guest` renders on at least one platform"* — was overtaken by G3
   landing on macOS 2026-08-12, but that platform's staging remains **UNVERIFIED on real hardware**
   (`docs/design/macos-user-nix-and-features.md:174`). So Q7's real question today is narrower and
   sharper: **does the Linux `guest` row stay in the table as a promise, given the vocabulary is
   already out?**

   **Answer:**
   > _(empty — fill in when decided)_

8. 💬 **Q8 — what `yolo --at host` prints as its banner.**
   Today launch prints `YOLO JAIL — AGENT BRIEFING` and a "WHAT YOU KEEP (shared with the host)"
   section built entirely around the container. At `host` every line of it is wrong, and the
   design's "the name stays" argument depends on `jail` reading as a *level* rather than the
   product.

   _Leaning:_ one banner, parameterized: `yolo — host level (no confinement)`. The name survives
   because the level is named in the same breath. Leaving the word "JAIL" in a `host` banner is
   the fastest way to make the reframing look like marketing.

   _Shipped since (2026-08-23):_ **unchanged and now inconsistent with its sibling.**
   `internal/cli/briefing.txt:5` still prints `YOLO JAIL — AGENT BRIEFING` and `:12` still prints
   `WHAT YOU KEEP (shared with the host)`, with no notch parameter. The *generated* briefing did
   get notch-awareness (Phase 8.1, `internal/jailcontent/briefing.go:110`), so the product now
   says two different things about the same run: the launch banner asserts a jail, the briefing
   names the actual notch. That divergence is new since the question was written and is the
   strongest argument for its leaning.

   **Answer:**
   > _(empty — fill in when decided)_

9. 💬 **Q9 — whether the notch names are defensible as terminology.**
   The middle notch was `sandbox` in the first draft of these stories, and it does not survive
   scrutiny (design doc §4.0): "sandbox" is the industry's *generic* term for the whole column —
   Kubernetes' `PodSandbox`, gVisor, Firecracker, Chrome's seccomp/Seatbelt renderer — so it names
   containers and VMs too. This codebase already spends the word on the jail three times
   (`internal/cli/help.go:39` "a sandboxed container jail", `internal/jailcontent/briefing.go`,
   `internal/macosuser/seatbelt.go`'s profile header). `jail` is fine: FreeBSD jails and chroot
   jails are OS-level partitioning of one kernel, so a container *is* a jail in the term's own
   lineage — nothing about the word implies a VM. Renamed to `guest` here.

   _Leaning:_ `jail` / `guest` / `host` — named for the agent's relationship to the machine, which
   is the only thing true on every platform, since the middle notch is a separate macOS user +
   Seatbelt on darwin but namespaces with **no separate user** under bwrap on Linux. Any name
   drawn from a mechanism describes one platform and lies about the other. Two residual worries:
   `guest` collides with "guest OS" in virtualization (where the guest is the *stronger*
   isolation, the reverse of our ordering), and a three-noun ladder invites reading the notches as
   absolute strengths when they are ordinal per platform — `jail` is podman on Linux and a
   per-container VM on Apple Container. `describe` prints the mechanism beside the notch for
   exactly that reason. `restricted` is the boring alternative, at the cost of mixing an adjective
   into a noun triple.

   _Shipped since (2026-08-23):_ **the leaning shipped as the config vocabulary, so this question
   is now a ratification rather than a choice.** `jail|guest|host` is what `confinement` validates
   (`internal/config/confinement.go:65-79`; unknown values fall back to `jail`, never `host` —
   `internal/config/validate_test.go:468-481`), and the mechanism *is* printed beside the notch as
   the leaning required (`enforced by <primitives>`, `internal/cli/describe.go:145-149`, sourced
   from `internal/render/confinement.go:53-69`). Renaming now costs a config migration it did not
   cost when the question was written.

   **Answer:**
   > _(empty — fill in when decided)_
