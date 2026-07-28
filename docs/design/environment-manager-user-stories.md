# User Stories: meeting yolo when it manages the environment, not just the box

Five worked stories of people (and one agent) encountering yolo as
[an environment manager whose confinement is a dial](yolo-as-environment-manager.md) —
`jail` (default), `guest`, `host` — rather than as a container product with config
composition bolted inside it. The lens is **the moment the reframing pays off, and the moment it
bites**: every story is written so the new verbs (`apply`, `describe`, `diff`, `check --at`) get
used in anger, and three of the five hit defects that exist in the shipped code today.

Terminal output below is real where it can be (`yolo config ls`, `yolo config diff`, the launch
banner, `yolo pack ls`, the empty-packs notice) and follows the design doc's samples where the
feature does not exist yet.

**Reads with:** [yolo-as-environment-manager.md](yolo-as-environment-manager.md) (the design
these stories exercise), [host-render-target.md](host-render-target.md) (§6 is where the
failures in story 2 were probed), [../plans/BACKLOG.md](../plans/BACKLOG.md) Stage G.

---

## 1. Maya — Staff Engineer, Rust CLI, Two Machines That Should Be the Same

**Context:** Maya maintains `sift`, a Rust CLI with 1,240 tests and a 6-minute release build.
She works on a Linux desktop at her desk and a MacBook on the train. Both run yolo with the same
user config: the `claude` pack plus a private `house-rules` pack (11 skills, an AGENTS.md
fragment about her error-handling conventions, and a `claude/settings.json` surface that turns
off two MCP servers she finds noisy). Her complaint for the last three months: **the agent is
subtly better at her desk than on the train**, and she has never been able to say why.

**What happens today:**

She has no way to ask the question. `yolo config ls` tells her which files are composed, `yolo
check` tells her the runtime is healthy, and neither of them compares two machines. She has
twice resorted to `diff`ing `~/.claude/settings.json` between hosts over SSH, which does not
work, because the file she cares about is composed *inside* the jail from four layers and never
exists on the host in that form.

**What happens with a description:**

1. At her desk, she asks what she's actually running:

   ```
   $ yolo describe
   environment  sift @ /home/maya/code/sift         confinement  jail (podman)
   tools        31 nix packages · flake.lock 8f2a1c…   mise: node@22.23.1
   agents       claude (pack, embedded)               launcher --dangerously-skip-permissions
   knowledge    3 skill trees (built-in < house-rules < user) · AGENTS.md 4 sources
   config       11 composed surfaces                  2 with captured edits
   services     claude-oauth-broker, host-processes
   grants       /workspace rw · 2 host files ro · no network holes
   description  sha256:4c1f8ad2…   ← same hash, same environment
   ```

2. On the train, same command:

   ```
   $ yolo describe --hash
   sha256:b7e0119c…
   ```

   Two hashes. Three months of vague intuition resolves into a 12-character mismatch in under a
   second, which is the entire argument for `describe` existing.

3. She asks what differs:

   ```
   $ yolo diff --against sha256:4c1f8ad2…
   tools     nix packages 31 → 29        (-ripgrep, -fd)
   tools     mise node@22.23.1 → (none)
   config    claude/settings  managed layer differs: 2 keys
   knowledge house-rules skills 11 → 11  (identical)
   ```

   **Gap:** `--against <hash>` implies yolo can resolve a hash back into a description, which
   means descriptions have to be *stored* somewhere, keyed by hash. Nothing in the design says
   where. The honest minimum is `yolo describe --json > desc.json` on machine A, scp, then
   `yolo diff --against ./desc.json` — a file, not a hash. The hash is for *detecting* drift;
   a file is for *explaining* it. The design conflates them.

4. The package difference is easy: her MacBook has a `yolo-jail.local.jsonc` next to the
   workspace config, written eight weeks ago to drop two packages while she was disk-starved.
   She'd forgotten it existed. It is deliberately gitignored, which is why no amount of `git
   diff` ever found it.

   **Gap:** `describe` printed `31 nix packages` but not *where the number came from*. A
   description assembled from user config + workspace config + an untracked local override
   should say so, or the hash becomes a mystery generator. She wants:

   ```
   $ yolo describe --sources
   packages   29   user:2  workspace:29  local:-2   ← yolo-jail.local.jsonc
   packs       2   user:2
   mounts      0
   ```

5. The `claude/settings` difference is the interesting one:

   ```
   $ yolo config diff claude --surface settings
   # claude/settings → ~/.claude/settings.json
     enabledPlugins  {"gopls-lsp@claude-plugins-official": true, "pyright-lsp@claude-plugins-official": true} (was {})
     extraKnownMarketplaces  {"subdir-mk": {...}} (same as yolo's last render — redundant capture)

   These values were captured from in-jail edits and outrank the host layer.
   Discard them with: yolo config reset claude
   ```

   Her desk jail captured two LSP plugins she enabled interactively in March. They outrank the
   pack. The train jail never had them. **The agent really was better at her desk** — it had
   `gopls` and `pyright` and the train agent didn't.

   **Gap:** a captured in-jail edit is a *fourth* source of truth that no pack, no config file,
   and no lockfile mentions. It legitimately changes behavior and it legitimately changes the
   description hash — but `describe`'s `2 with captured edits` is a count, not a warning, and
   nothing about it says "this environment has diverged from what you declared." Compare the
   launch banner, which does say it, every single boot:

   ```
   ~/.config/mise/config.toml: 1 key from captured in-jail edits (yolo config diff mise)
   ~/.claude/settings.json: 3 keys from captured in-jail edits (yolo config diff claude)
   ```

   The information exists. It just isn't in the artifact that claims to be the description.

6. She promotes the two plugins into `house-rules` (where they belong), runs `yolo config reset
   claude` in both jails, and re-checks:

   ```
   $ yolo describe --hash
   sha256:9d3c02f1…
   ```

   Both machines. Same hash. She adds `yolo describe --hash` to her release checklist so a
   release never builds from an environment she can't name.

**What would trip them up:**

- `--against <hash>` cannot work without a description store; the first thing she tries is the
  thing that doesn't exist.
- A gitignored `yolo-jail.local.jsonc` is invisible to every workflow she already trusts. The
  hash catches it, but only if she thinks to compare hashes — which requires already suspecting
  drift.
- Captured in-jail edits are legitimate (that's the feature) *and* a drift source. There is no
  vocabulary yet for "captured, and I meant it" vs "captured, and I forgot."

**What makes this work:**

- The hash converts an unfalsifiable feeling ("it's better at my desk") into a comparison. That
  is the only thing in the design that SandVault, devcontainers, and "whatever's on the host"
  structurally cannot offer.
- `yolo config diff` already exists and already prints the right thing. `describe` doesn't have
  to reinvent it — it has to *point at* it.

**The aha moment:** step 5. Not the mismatch — the *cause*. She'd assumed the difference was
model routing or network latency. It was two LSP plugins she enabled by hand five months ago and
never wrote down. The environment had state she didn't know she owned.

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

   This is a good half hour. `check --at` told her, by name, that her `packages: ["postgresql",
   "redis"]` line is inert here and her developers will need those from Homebrew. That's a
   rollout decision she can make on the spot instead of discovering it in a support ticket.

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

**What would trip them up:**

- Two silent-success failures in one sitting (`surfaces 0 rendered`, `reset` on a real file), and
  neither prints a warning.
- `yolo config reset` reads as scoped to yolo's own state. Nothing in its name or output says it
  can write outside the jail.
- She'd have shipped `guest` to 28 machines on the strength of step 1's clean `check --at`
  output. `check` validated the *description*; nothing validated that the notch renders.

**What makes this work — once it does:**

- `check --at guest` is genuinely the best thing in the design. Ten lines told her which of her
  ~25 config keys are inert on the notch she chose, before she distributed anything.
- Refusing `install` by name, with the manual command, converts a security rule into a
  documentation line instead of a mystery.

**Technical reality check:** the three notches are not equally real. `jail` is production,
`host` is a design, `guest` renders zero surfaces per launch on the one platform it exists on.
The BACKLOG order (G1 → G2 → G4 → G3) puts the data-loss fix first and the silent-zero fix
fourth, behind the `internal/render` collapse that makes it a two-line change. That order is
right, and this story is the argument for not announcing three notches until G3 lands.

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
- **Layer 5 — `describe --hash`.** He buys a second laptop. Now he cares whether they match
  (story 1).
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

Ash reads `AGENTS.md` and finds, generated by `internal/agents/agentsmd.go:114`:

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
   injected into every environment (`internal/agents/skills.go`) and reads `.yolo/handover.md`,
   neither of which is jail-specific.

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

   Notably, `describe --hash` *would* let her verify a fleet: 31 machines, one hash, one
   sentence. That is a stronger compliance story than any of her other vendors can tell. It just
   isn't reachable from anything she saw in 40 minutes.

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
| `yolo diff` | declared vs rendered vs captured | same | declared vs real files (`rmw` + sidecar) |
| `yolo check --at <n>` | all keys apply | names the inert ones | names the inert ones + the refused ones |
| `yolo pack install` | ✓ | ✓ | **refused, by field name** |
| `yolo config reset` | ✓ | ✓ | **must refuse unless `surfacesAreLocal()`** (G1) |

### What goes into the description hash

The stories put four sources under the hash, and one of them is contentious:

```
resolved config      user + workspace + yolo-jail.local.jsonc     ← story 1, step 4
pack set             names + locked commits (yolo pack status)
tool set             flake.lock rev + packages[] + mise_tools[]
composed surfaces    the rendered bytes of every surface
captured edits       ??? — story 1, step 5; see open question 1
```

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

1. **Whether captured in-jail edits belong in the description hash.**
   Story 1's whole plot is two machines whose declared config matched and whose *captured* config
   didn't. If captures are excluded, the hash says "same environment" when the agent demonstrably
   behaves differently. If they're included, the hash changes whenever a user enables an LSP
   plugin interactively, and `describe --hash` in CI becomes noise.

   _Leaning:_ include them, and print two hashes — `declared` and `effective`. CI pins
   `declared`; story 1's mystery is solved by `effective`. One hash cannot serve both, and
   picking one silently makes the other lie.

   **Answer:**
   > _(empty — fill in when decided)_

2. **Whether `apply` may report a shortfall as success.**
   `surfaces 0 rendered` is the shipped `macos-user` behavior (BACKLOG G3) and it printed as a
   normal success line in story 2. The 2026-07-26 ruling made generator *failure* fatal, but an
   absent generator isn't a failure.

   _Leaning:_ `apply` compares declared-count to rendered-count and is fatal on any shortfall not
   explained by a named refusal. "Refused, by name" is fine; "rendered fewer than declared, no
   reason given" is a halt. That makes G3 impossible to reintroduce rather than merely fixed.

   **Answer:**
   > _(empty — fill in when decided)_

3. **Where `describe --against` resolves a hash from.**
   Story 1 assumes a hash can be turned back into a description. That needs a store — per
   workspace under `.yolo/`, machine-global under `~/.local/share/yolo-jail/`, or nowhere.

   _Leaning:_ nowhere. `--hash` detects drift; `describe --json` to a file plus `diff --against
   ./file.json` explains it. A description store is a cache-invalidation problem in exchange for
   saving one `scp`.

   **Answer:**
   > _(empty — fill in when decided)_

4. **Whether a rendered briefing must be stamped with its notch.**
   Story 4's briefing is a real file in a real directory at `host`, and it outlives the run. A
   `host` briefing read inside a jail is merely confusing; a `jail` briefing read on the host is
   how someone loses `~/.pyenv`.

   _Leaning:_ stamp it (`confinement: host`, `rendered: <hash>`) and have the built-in startup
   skill assert the stamp against observable reality — if it claims `jail` and `/workspace` is
   missing, halt. Cheap, and it fails in the safe direction. Rename `jail-startup` while doing it.

   **Answer:**
   > _(empty — fill in when decided)_

5. **Whether the user config can set a confinement floor.**
   Lisa needs to distribute "this machine may not run at `host`." §8 currently calls `host`
   over-use a product-discipline risk with no technical fix, which is true for one user and
   insufficient for 31.

   _Leaning:_ add `"maxConfinement"` at user scope only (never workspace — a workspace config is
   agent-editable, same reason `packs` is user-scoped), and journal every `--at` that lowers the
   notch. Not a security boundary — the user owns the machine and can edit their own config — but
   an honest answer to "is this the default here?"

   **Answer:**
   > _(empty — fill in when decided)_

6. **Whether `describe` gets an exposure view, or exposure gets its own verb.**
   Story 5 wants nouns (which files, which credentials, which direction the network goes), and
   `describe`'s current shape is counts. Bolting `--exposure` onto `describe` keeps one verb;
   splitting it admits that "what is this environment" and "what can it reach" are different
   questions with different audiences.

   _Leaning:_ `describe --exposure` as a view, not a verb — same resolved description, different
   projection. But fix the `grants` line unconditionally: an explicit `network outbound:
   unrestricted` row belongs in the default output, because the current phrasing produced a wrong
   sentence in a customer document.

   **Answer:**
   > _(empty — fill in when decided)_

7. **Whether Linux `guest` (bwrap + Landlock) is a promise or a hypothesis.**
   The design's three-row table lists it as the Linux mechanism for the middle notch, and that
   row is the evidence the dial is real rather than a story told about two macOS backends. No such
   code exists.

   _Leaning:_ mark it explicitly as unbuilt in the table until it runs, and do not ship the
   three-notch vocabulary in user-facing copy until `guest` renders surfaces on at least one
   platform (G3). Story 2 is what shipping it early costs.

   **Answer:**
   > _(empty — fill in when decided)_

8. **What `yolo --at host` prints as its banner.**
   Today launch prints `YOLO JAIL — AGENT BRIEFING` and a "WHAT YOU KEEP (shared with the host)"
   section built entirely around the container. At `host` every line of it is wrong, and the
   design's "the name stays" argument depends on `jail` reading as a *level* rather than the
   product.

   _Leaning:_ one banner, parameterized: `yolo — host level (no confinement)`. The name survives
   because the level is named in the same breath. Leaving the word "JAIL" in a `host` banner is
   the fastest way to make the reframing look like marketing.

   **Answer:**
   > _(empty — fill in when decided)_

9. **Whether the notch names are defensible as terminology.**
   The middle notch was `sandbox` in the first draft of these stories, and it does not survive
   scrutiny (design doc §4.0): "sandbox" is the industry's *generic* term for the whole column —
   Kubernetes' `PodSandbox`, gVisor, Firecracker, Chrome's seccomp/Seatbelt renderer — so it names
   containers and VMs too. This codebase already spends the word on the jail three times
   (`internal/cli/help.go:39` "a sandboxed container jail", `internal/agents/agentsmd.go:114`,
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

   **Answer:**
   > _(empty — fill in when decided)_
