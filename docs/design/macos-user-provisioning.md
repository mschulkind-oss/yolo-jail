---
title: "macos-user has no floor and no provisioning stage"
status: accepted
date: 2026-09-04
tags: [macos-user, provisioning, packages, mise, backend-parity]
summary: "Almost every imperative provisioning step the container path runs — mise install, the LSP/MCP npm installs, the npm half of the agent CLI installers — is missing on macos-user, and so is the package floor those steps need to run at all. (Measured 2026-09-11: the `via: installer` half of the agent CLIs does work there, needing only macOS's own curl and bash.) Two separable halves, in that order: give the noncontainer profile a core set, then run the same stage, confined, inside the sandbox. Where the stage's state lives is settled by the container's own partition; how much floor and whether it is GNU or BSD are the two rulings left."
---

# macos-user has no floor and no provisioning stage

**Status:** **HALF ONE BUILT, 2026-09-12** ([§9](#9-what-shipped-half-one)); half two
designed and unbuilt. DESIGN 2026-09-11, DESIGN SKETCH 2026-09-04. All four questions are
ruled and compacted into the [Decision Ledger](#decision-ledger).

> [!WARNING]
> **Every runtime claim about half one is NOT MEASURED.** It was implemented from a Linux
> jail, where there is no `sandbox-exec`, no `_yolojail` account, and `RunMacosUser` fails
> closed on `!deps.IsMacOS()`. What IS measured is the nix evaluation — the floor's
> composition and the fatal, both read with `nix eval` for `aarch64-darwin` and
> `x86_64-darwin` from Linux ([§9](#9-what-shipped-half-one)) — and the Go half, unit-tested
> with fake homes. That the closure BUILDS on a Mac, that the sandbox gets the PATH, and
> what the first launch costs are all owed a hardware run.

> **In short.** A container jail gets its tools from an image **floor** and an
> imperative **stage**; macos-user has neither, so four config keys render and install
> nothing. The fix is the same floor (smaller) and the same stage (confined), and the
> stage's state goes exactly where the container already puts it — machine-wide
> for mise's data, per-workspace for everything else.

**Why it matters.** `mise_tools`, `lsp_servers` and `mcp_presets` all render config and install
nothing, and all three warn. The lazy agent-CLI installers were the fourth item on that list until
they were measured on 2026-09-11: **the `via: installer` half works there, the `via: npm` half fails
at the moment the agent is invoked** ([§2](#2-what-this-costs-today)).

**The shape.** Half one adds a core set to the noncontainer nix profile. Half two
runs the container's `setupScript` body as a **new, Seatbelt-confined step** between
the bootstrap and the agent ([§4](#4-the-proposed-shape)).

**Cost.** A native darwin closure for the floor, built once per machine. The stage could not
ship before the home split landed the per-workspace surfaces it writes into
([`OQ-P3`](#decision-ledger)); the split is **built** as of 2026-09-12
([`macos-user-home-tiers.md` §10](macos-user-home-tiers.md#10-what-shipped)), so that
dependency is discharged.

**Start at [§6](#6-alternatives)** — "the same as everywhere else" has a cost on this
backend it does not have in an image, and the ruling turns on whether it is worth
paying.

**Needs your ruling:** **None** — both closed 2026-09-11 ([Decision Ledger](#decision-ledger)). Half one is built ([§9](#9-what-shipped-half-one)); half two is ready to build, with the home-split dependency gone.

> [!NOTE]
> **Terms coined here.** The **floor** is the set of packages present in a jail
> before any config asks for anything — the image bakes one, the native profile does
> not. The **stage** is the imperative provisioning step a launch runs *inside* the
> jail, after the floor exists and before the agent starts. Neither is a yolo config
> key and neither appears in the code under these names; they are this document's
> words for two things that had none, which is why the gap between them was never
> stated as one problem. ⚠ The stage is *not* the darwin bootstrap: that runs yolo's
> own generators, installs nothing, and — measured 2026-09-11 — runs **outside** the
> Seatbelt profile ([§1](#1-the-two-missing-halves)).

**Reads with:** [`../reference/nix-across-backends.md`](../reference/nix-across-backends.md)
(what nix produces for each backend, and why the image is a floor),
[`macos-user-home-tiers.md`](macos-user-home-tiers.md) (the home split, whose
[§5](macos-user-home-tiers.md#5-the-proposal) supplies [`OQ-P3`](#decision-ledger)'s answer and
which shipped on 2026-09-12, unblocking this doc's half two), and
[`macos-user-nix-and-features.md`](../reference/macos-user-nix-and-features.md) (the backend).

---

## 1. The two missing halves

They are separable, and they are ordered. Naming them apart is most of the design,
because a fix that addresses one is not a partial fix — it is no fix.

**The floor.** `flake.nix` assembles the image root as
`[ variantBinPathLinks ] ++ corePackages ++ variantFullPackages ++ extraPackages`,
where `corePackages` is `[ jailPrefixLinks imageIdentity ] ++ corePackagesFromNixpkgs`
and `corePackagesFromNixpkgs` is the list that matters here: **36 nixpkgs entries**
when counted 2026-09-11 — `bashInteractive`, `coreutils-full`, `git`, `ripgrep`, `fd`,
`curl`, `cacert`, `mise`, `findutils`, `which`, `nodejs_24`, `python3`, `go`, `neovim`,
`gh`, `gnused`, `gnugrep`, `gawk`, `gnupatch`, `diffutils`, `gzip`, `bzip2`, `xz`,
`gnutar`, `unzip`, `zip`, `zlib`, `procps`, `overmind`, `jq`, `uv`, `iptables`, `socat`,
`sox`, `openssl`, `tzdata`. The native profile is `noncontainerPackages` — the user's
declared list, minus what has no native build, and **nothing else** (its sole input is
the `YOLO_EXTRA_PACKAGES` config list). There is no core.

> ⚠ **Retracted (2026-09-11):** the first draft cited three `flake.nix` line ranges and
> "~19 packages". All three ranges were stale and the count was wrong by half. The
> attribute names above are the anchors now; a line number in a 1,600-line flake is the
> kind of claim that is wrong within weeks.

**The stage.** `setupScript` (`internal/cli/run/command.go:19-30`) runs, inside the
jail, on every container launch: a store prune, `mise install --quiet`, then
`~/.yolo-bootstrap.sh` (the generated script that npm-installs LSP servers and MCP
presets) and `~/.yolo-venv-precreate.sh`. It is part of the **container command
wrapper**. On macos-user, `RunMacosUser` runs exactly two things after staging
(`internal/macosuser/orchestrator.go:432-440`): the darwin bootstrap, then the agent.
Neither is a stage:

- **The bootstrap** is `sudo --user=_yolojail /usr/bin/env -i … <stagedYolo> internal
  darwin-bootstrap` (`internal/macosuser/runplan.go:103-110`) — yolo's own generators,
  run as the sandbox user **with no `sandbox-exec`**, i.e. outside the Seatbelt
  profile. It renders config and installs nothing; `GenerateBootstrapScript` has no
  call site on this path (its only production caller is the container boot loop,
  `internal/entrypoint/boot.go:536` — verified 2026-09-11).
- **The launch** is `sudo --login --set-home --user=_yolojail env -i … sandbox-exec -f
  <profile> -- /bin/zsh -c 'cd <ws> && exec <agent>'` (`macosuser.go:482-516`) —
  straight to the agent.

> ⚠ **Retracted (2026-09-11):** the first draft said the launch goes to `zsh -l`; it is
> `zsh -c` under `sudo --login`. Cosmetic — but the same paragraph implied the bootstrap
> ran under the sandbox, and that is the load-bearing error: half two is a **new
> confined step**, not a line added to an already-confined one
> ([§4](#4-the-proposed-shape)).
>
> **And `--login` is not cosmetic after all** — it mangles the forwarded command, measured
> the same day. See [§1.1](#11-the-forwarded-command-is-not-passed-through-faithfully).

### 1.1 The forwarded command is not passed through faithfully

**A defect, found by measurement on 2026-09-11 and not yet fixed.** It is stated here because this
section owns the launch argv; it is orthogonal to this doc's thesis, and it is not part of
[§4](#4-the-proposed-shape)'s proposal.

`sudo --login` does not `execve` the argv it is given. Per sudo(8) `-i`: *"If a command is
specified, it is passed to the shell as a simple command using the -c option. The command and any
args are **concatenated, separated by spaces, after escaping each character (including white space)
with a backslash** — except for alphanumerics, underscores, hyphens, and dollar signs."* Two
consequences follow from that one sentence, and **both were measured on hardware**
(`YOLO_RUNTIME=macos-user yolo -- bash -lc …`, host `yolo` `0.8.0+1336.gecb17e8c`):

| Probe | Sound behaviour | Measured |
| :--- | :--- | :--- |
| `bash -lc $'echo A\necho B'` | `A` then `B` | **`Aecho B`** — the newline arrived as a `\`-continuation and bash removed it, joining the lines |
| `bash -lc 'X=inner; echo got=$X'` | `got=inner` | **`got=`** — `$X` is unescaped, so the intermediate login shell expanded it (unset) before the sandbox's bash saw the string |

**Why it is worse than it looks.** Neither failure is an error. The wrong command runs, exits 0, and
prints plausible output — the first attempt at
[`provisioner-sets.md` §15](provisioner-sets.md#15-what-a-mac-session-should-measure) M1 collapsed
nine probes into five and reported five successes, none of which had run. Every container backend
passes argv through `podman exec` untouched, so this is a **backend-parity defect**: the same
`yolo --` invocation means different things per backend, and only this one rewrites it.

**Why the obvious fix is wrong.** `--login` is load-bearing for
[`OQ-1`](../plans/runbooks/mac-go-port-verification.md#2-macos-user-backend--real-launch-oq-1-the-load-bearing-unknown):
the login rc files `WriteLoginRC` generates are what re-prepend PATH after macOS `path_helper`
reorders it, and that is the acceptance bar the runbook passed on 2026-09-10. Dropping the flag to
get a faithful argv would trade one measured behaviour for another.

**What to weigh instead** (a fix, not a design — deliberately unresolved here): the outer login
shell's own rc work is *already* discarded, because the very next word in the argv is
`/usr/bin/env -i`, which wipes the environment it just built; PATH inside the sandbox comes from the
explicit `PATH=` in that `env -i` list. If that reading holds, `sudo --user=… --set-home` plus an
inner **`zsh -l -c`** keeps every rc file that matters (a login zsh reads `.zprofile`; `.zshrc` is
not read by either spelling, since neither is interactive) while letting sudo `execve` the argv
verbatim. `PlanInvariants` pins the current shape and would move with it. **Not verified** — it
needs the same one-password launch these probes needed, and a fix that re-plumbs the launch argv
wants its own test at the plan level first.

**Verified on the machine, not inferred — and one observation retracted.** No `mise`
binary exists on any path the sandbox can read — the host's is at `/opt/homebrew/bin`,
which is not on `SandboxPath` and whose state lives under `/Users`, which the profile
denies. No mise *data* dir exists (nothing creates one). ⚠ **But "no mise config" was
wrong**: `RunDarwinBootstrap` runs `ConfigureMisePrism` on every launch
(`internal/entrypoint/darwin.go:85`), and it always emits a `[tools]` table
(`internal/entrypoint/prism_mise.go:65-82`), so `~/.config/mise/config.toml` *is*
written — into the shared home, carrying this workspace's `mise_tools`. The step has been
on the darwin path since 2026-07-21 (`731dbe56`), so the 2026-09-04 listing missed it or
the home had been recreated since; re-measure before citing the
directory listing again.

## 2. What this costs today

| Config key | Container | macos-user | Told? |
| :--- | :--- | :--- | :--- |
| `mise_tools` | installed by the stage | nothing; shims dir on PATH so it *looks* provisioned; `~/.config/mise/config.toml` written to the shared home | warns, host-side (`internal/cli/run/loopholeinert.go:309-315`, since 2026-09-04) |
| `lsp_servers` | npm-installed by the stage | config renders, binaries absent | warns, host-side (`loopholeinert.go:316-321`) |
| `mcp_presets` | npm-installed by the stage | wrappers skipped | warns — **in the bootstrap only** (`darwin.go:100-105`), so `--dry-run` never shows it |
| agent CLIs (lazy launchers), `via: installer` | launcher execs the vendor installer | **works** — `curl` and `bash` are at `/usr/bin`; MEASURED 2026-09-11, three of three packs, two installing from scratch | n/a — nothing to tell |
| agent CLIs (lazy launchers), `via: npm` | launcher execs `npm install -g` | fails — no node, no npm | **loud, at run time**: `npm: command not found` then `⚠ <bin> not available`, exit 1 (MEASURED 2026-09-11). `GenerateAgentLaunchers` still has no *generation*-time precondition (`internal/entrypoint/shims.go`, verified 2026-09-11), so nothing warns at launch |
| `packages:` | baked into the image | realized natively | works |

`packages:` works because it is the only declarative mechanism here — the only one that never
needed a runtime to already be present. **It is also the escape hatch the warnings already
point at**: `mise` and `nodejs` in `packages:` give a user the minimum floor today,
and the `mise_tools` warning says so verbatim.

> [!IMPORTANT]
> **The agent-CLI row SPLIT on 2026-09-11, when it was finally measured on hardware, and the split
> is the most load-bearing correction this doc has taken.** It used to be one row reading *"launcher
> generated, but no node and no npm to run it — silent"*, which generalised the npm case to all six
> packs. Measured: **three of the six install and run** — `claude`, `codex` and `agy` are
> `via: installer`, and a vendor installer needs only the `curl` and `bash` that macOS ships. So the
> guest is not a notch where agent CLIs cannot arrive; it is one where they arrive **by exactly one
> of the two mechanisms** ([`provisioner-sets.md` §15](provisioner-sets.md#15-what-a-mac-session-should-measure)
> M1, and its [§3](provisioner-sets.md#3-the-provisioner-inventory-per-environment) rows 5 and 6).
>
> Two live edges survive the correction. **The npm half is loud but late** — the launcher prints
> `npm: command not found` and exits 1 when the agent is invoked, so the failure still lands on the
> user's first real command rather than on the launch they could have read; a generation-time or
> launch-time warning is still cheap and should still not wait for this design. **And the evergreen
> half is unproven** — `claude`'s hourly update ran and failed (`status 124`, the vendor's own),
> unbounded because `timeout(1)` does not exist on macOS (`internal/entrypoint/shims.go:1020-1029`
> rules that explicitly). Installing once is measured; staying current is not.

## 3. Principles

**P1. A backend either provides a mechanism or refuses it out loud.** Rendering the
config for a mechanism that does nothing is the failure this whole document is
about; it has produced five instances and cost a day each time.

**P2. The declarative path is the one that composes.** `packages:` works here
precisely because nix needs nothing pre-installed. Every imperative installer
assumes a runtime that something else put there.

**P3. Convergence beats a second dialect.** Two ways to say "install neovim"
depending on backend is a tax on every user and every doc. Where the backends can
run the same step, they should run the same step. This is the local form of the
parity constraint stated in review 2026-09-11
([`macos-user-home-tiers.md` §5.0](macos-user-home-tiers.md#50-the-constraint-that-outranks-the-layout-choice-one-mechanism-every-backend)):
one mechanism on every backend, and only the primitive enforcing the boundary may
differ.

**P4. Third-party installers run confined; yolo's own generators may not need to.**
*(coined here, from the measurement in [§1](#1-the-two-missing-halves).)* The
bootstrap runs outside Seatbelt and that is tolerable because it executes only yolo's
code against a root-owned staged tree. The stage runs `npm install` postinstall hooks
and mise plugins — vendor code — and the container runs it inside the jail. Running it
unconfined here would be a regression the container never had.

> [!NOTE]
> **Measured 2026-09-11, and it sharpens P4 rather than confirming it.** Two vendor installers ran
> under the session profile — so *confined* — and both still reached past their own prefix into
> **yolo's generated files**: `agy` appended a PATH export to `.bashrc`, `.zshrc`, `.zprofile` and
> `.bash_profile`, and `codex` prompted `Start Codex now? [y/N]` on `/dev/tty` and waited for a human
> ([`provisioner-sets.md` §15.1](provisioner-sets.md#151-what-a-vendor-installer-does-to-the-generated-home)).
> Seatbelt was doing its job: the sandbox home is *supposed* to be writable, and the tty is the one
> the launch legitimately owns. **So "run it confined" does not mean "run it safely" for anything
> inside the sandbox home** — which is where every generated PATH-ordering and launcher artifact
> lives. A stage that runs installers on a schedule inherits both effects, unprompted.

## 4. The proposed shape

**Half one: a core set for the noncontainer profile.** `yoloNoncontainerPackages`
gains a core list, the way the image has one — the same attr, evaluated for the
native system. Minimum viable core is whatever the stage needs to run: `mise` and
`nodejs`. Whether it extends toward the image's 36 is **[`OQ-P1`](#decision-ledger)**.

**Half two: a provisioning stage inside the sandbox.** The macos-user launch grows a
**third** step between the bootstrap and the agent (`orchestrator.go:432-440`): the
same `setupScript` body, run as the sandbox user **under `sandbox-exec -f <profile>`**
— the profile is already installed before the bootstrap (`orchestrator.go:417`), so
nothing new has to exist for the stage to be confined, and a separately-launched
`sandbox-exec` process is not the nested-profile case `sandbox_apply` refuses
([`macos-revival-and-distribution-plan.md`](../plans/macos-revival-and-distribution-plan.md),
*Seatbelt does not nest a different profile*). `RunDarwinBootstrap` starts generating
`~/.yolo-bootstrap.sh` so there is something to run.

Ordering is not a preference: `mise install` needs `mise`, and the bootstrap script
needs `npm`. Half two without half one is a script that fails on its first line.

- **Trigger: every launch, unconditionally, before the agent** — like the container's,
  and idempotent for the same reason: the config can change between launches and the
  jail must reflect it ([`OQ-P4`](#decision-ledger)). **Not on demand**, and not as a
  matter of taste: the lazy launchers exist for agent CLIs and cover neither
  `mise_tools` nor LSP servers, so triggering off them would leave one config with
  tools absent on this backend and present on podman — the second dialect P3 in
  [§3](#3-principles) forbids. The container already splits the two the right way,
  staging eagerly and installing agent CLIs lazily; this backend takes the same split.
- **Failure: a failing stage must not abort the launch.** The container path tees to
  `<workspace>/.yolo/startup.log` and marks `PROVISIONING FAILED`, which the briefing
  then reports — and **the reader half is already wired on this arm**:
  `refreshJailBriefings` runs on the macos-user branch (`run.go:397`) and fills
  `ProvisioningFailed: jailcontent.ReadProvisioningFailed(o.Workspace)`
  (`internal/cli/run/prepare.go:147`). Only the emitter is missing, and it needs one
  seam: the `startupLog` constant is rooted at the container's fixed `/workspace` bind
  (`command.go:32`) and must be rebound to the real workspace's `.yolo/` sidecar here,
  the way `YOLO_DARWIN_WORKSPACE` already rebinds the workspace for the generators
  (`darwin.go:59-61`).
- **Degenerate input:** no `mise_tools`, no `lsp_servers`, no `mcp_presets` and no agent
  packs → the stage is skipped entirely, so a bare `yolo -- bash` pays nothing.
- **Concurrency:** two launches on one workspace run two stages against one sidecar; the
  container serializes that with a courtesy flock (`internal/cli/run/flock.go:41-57`)
  whose one production caller is inside `runContainer` — taking it on this arm is a
  moved call, sequenced with the stage.

**Where the stage's state lands** is settled by [`OQ-P3`](#decision-ledger) and is the
container's own partition, verified 2026-09-11 in `internal/cli/run/assemble_parts.go`:

| State | Container | macos-user, after the home split |
| :--- | :--- | :--- |
| mise data (`installs/`, `shims/`) | machine-wide: `MISE_DATA_DIR=/mise`, a store dir or named volume (`assemble.go:814`, `assemble_parts.go:172-176`) | machine-wide, **shipped 2026-09-12**: `macosuser.SandboxMiseData` names `<home>/.yolo/mise` in the launch env, the bootstrap env and the PATH's shims dir, because the unset default `$HOME/.local/share/mise` (`internal/entrypoint/env.go`) falls inside the per-workspace `~/.local` symlink |
| mise config (`~/.config/mise/config.toml`) | per-workspace: `config` bind (`assemble_parts.go:119`) | per-workspace: the `config` sidecar symlink |
| npm prefix (`~/.npm-global`) | per-workspace: `npm-global` bind (`:108`) | per-workspace: sidecar symlink |
| agent CLI installs (`~/.local`) | per-workspace: `local` bind (`:109`) | per-workspace: sidecar symlink |

> [!WARNING]
> **Sharing mise's DATA dir between workspaces is not a collision, and the fear that it
> was is refuted** (checked 2026-09-11). `installs/<tool>/<version>` is keyed by tool and
> version (`internal/cli/run/command.go:21`), so two workspaces asking for different
> tools *add* to the store rather than reshape it — which is why every container backend
> shares it machine-wide to begin with. The collision that is real is mise's **config**,
> and it is already live today with no stage at all ([§1](#1-the-two-missing-halves)).
> Do not "fix" the data dir by making it per-workspace: that is the inverse of every
> other backend ([§5](#5-what-this-does-not-propose)).

## 5. What this does NOT propose

- **Not an image for macos-user.** The absence of one is the backend's whole
  reason to exist. A floor is a nix profile, not a filesystem.
- **Not changing the container path.** Every claim here is about giving macos-user
  what the container already has.
- **Not a second config surface.** No `macos_packages`, no per-backend `mise_tools`.
  If a package is Linux-only, `platforms: ["linux"]` already says so
  (`internal/config/derived.go`, `EffectivePackages`; the refusal message at
  `orchestrator.go:379-396` names the spelling).
- **Not fixing the shared home.** The stage will write into it, which is what makes
  [`OQ-P3`](#decision-ledger) depend on the split, but the split itself is
  [`macos-user-home-tiers.md`](./macos-user-home-tiers.md).
- **Not a per-workspace `MISE_DATA_DIR`.** The first draft named it as the
  alternative to blocking on the split. It is doubly wrong: a mechanism no other
  backend has, *and* the inverse of the container's partition, which shares mise's
  data machine-wide by design.
- **Not running the stage unconfined**, even though the bootstrap is (P4).

## 6. Alternatives

| Alternative | Verdict |
| :--- | :--- |
| **A. Floor + stage** ([§4](#4-the-proposed-shape)) | **Recommended.** The only one that satisfies P3. Costs a native core closure and the two questions still open. |
| **B. Declarative only** — delete the imperative surfaces on this backend, refuse `mise_tools`/`lsp_servers`/`mcp_presets` loudly, tell users to write `packages:` | **Rejected, but it is the honest runner-up.** It satisfies P1 and P2 fully and costs nothing to build — today's warnings are already 80% of it, and the `mise_tools` warning already tells users to do exactly this. It fails P3: a user with one config across a Mac and a Linux host would need two spellings of the same intent. Revisit if the core closure in A proves painful. |
| **C. Status quo + warnings** (what ships today) | **Rejected as an end state**, accepted as the interim. It is honest and it is not a backend anyone can use for real work. |
| **D. Floor only** — core packages, no stage | **Rejected.** Puts `mise` on PATH and never runs `mise install`, which is a worse lie than the current absence: the tool exists and reports nothing to do. |
| **E. Stage on demand** — let the lazy launchers trigger it | **Rejected**, settled by [`OQ-P4`](#decision-ledger): its only benefit is already delivered by the skip rule in [§4](#4-the-proposed-shape), and it would make `yolo -- bash` behave differently per backend. |

## 7. Risks

| Risk | Mitigation |
| :--- | :--- |
| A core package has no native darwin build | Handled, and it is **fatal** rather than the warn-and-skip `packages:` gets: a floor with a hole in it is not a floor ([§9](#9-what-shipped-half-one)). ⚠ **This row's own example was wrong.** It said "at least two of the image's 36 are Linux-only by nature (`iptables`, `procps`)". The eval says **one**: on darwin nixpkgs resolves `procps` to `unixtools.procps` (name `procps-1003.1-2008`), a wrapper around the Mac's own BSD `ps`/`pgrep` — which is also what [`OQ-P2`](#decision-ledger) wants. Guessing would have cost a working tool, which is the argument for deriving the list rather than writing it. |
| First launch builds a large closure natively | One-off per machine; nix caches. Cachix already applies (`--accept-flake-config`). Measure before assuming it is a problem. |
| The stage's state lands in the shared home | Settled: the container's partition ([§4](#4-the-proposed-shape), [`OQ-P3`](#decision-ledger)). The residual risk is the **inverted default** — `MISE_DATA_DIR` unset once `~/.local` is a sidecar symlink — and it is closed by setting the variable explicitly. |
| The mise *config* collision ships today, without any stage | Real and already live ([§1](#1-the-two-missing-halves)); fixed by the same `config` sidecar symlink, which is why half two waits for the split rather than the other way round. |
| GNU-vs-BSD userland surprise | [`OQ-P2`](#decision-ledger), and its exclusions have their own assertion rather than a maintainer: `internal/darwinpkg/floor_policy_test.go` tests the DERIVED floor against a predicate, so a GNU package added to the image core tomorrow fails without anyone editing a list ([§9](#9-what-shipped-half-one)). |
| The stage runs vendor postinstall scripts | Confined under the same profile as the agent (P4). |

## 8. Sequencing

Ship the unwarned agent-launcher case first — it is independent of every question
below and it is the one failure that lands on a user's first real command. Then
half one, gated on [`OQ-P1`](#decision-ledger) and [`OQ-P2`](#decision-ledger). Then half two, whose dependency on the home
split is **discharged**: the split is built ([`macos-user-home-tiers.md` §10](macos-user-home-tiers.md#10-what-shipped)),
and [`OQ-P3`](#decision-ledger)'s `MISE_DATA_DIR` half shipped with it. Half two is worth nothing before half one, so there
is no partial-credit ordering to be clever about.

**Does the stated dependency hold?** Checked 2026-09-11: **yes, narrowed.** Half two
writes to three per-workspace surfaces (`config`, `npm-global`, `local`) that exist on
this backend only once the sidecar symlinks of the home split are laid; without them
the stage writes per-workspace content into the shared home and reproduces the race
the split exists to end. The machine-wide half (mise data) does not depend on the
split at all — it depends on setting `MISE_DATA_DIR`, which the split makes
*necessary* rather than optional.

## 9. What shipped (half one)

**Built 2026-09-12**, in four commits, on a Linux jail — so read the warning at the top of
this document before treating any runtime sentence here as measured.

### 9.1 The floor's composition, and how it was derived

`flake.nix`'s image core became a list of nixpkgs attr NAMES (`coreFloorNames`), because two
consumers now need the same 36 names resolved against two different package sets:
`corePackagesFromNixpkgs` maps them over `imagePkgs` for the image, and
`noncontainerFloorPackages` maps them over `pkgs` for this flake's own `system`. **The image's
package set did not move** — `imageClosureRoot.drvPath`, which is the nixpkgs half alone, is
byte-identical across the change.

**The unbuildable set was DERIVED, not guessed.** `nix eval` is cross-platform, so all 36
names were read for `meta.platforms` / `meta.available` against **both** darwin systems this
flake locks — nixpkgs `c043004d` for `aarch64-darwin`, `nixpkgs-26.05-darwin` `c19db427` for
`x86_64-darwin` — from a Linux jail, in about a second. Exactly **one** came back unavailable
on either:

| Attr | Result | Evidence |
| :--- | :--- | :--- |
| `iptables` | **unavailable on both** | `meta.unsupported = true`; `meta.platforms` lists 24 entries, none of them darwin |
| `procps` | **available** | no `meta.platforms` at all — on darwin the attr resolves to `unixtools.procps`, `name = procps-1003.1-2008`, a wrapper around the Mac's own BSD `ps`/`pgrep`/`pkill` |
| the other 34 | available | `availableOn` and `meta.available` both true on both systems |

⚠ **`procps` is the reason to derive rather than guess.** This document's own
[§7](#7-risks) and the roadmap row both said it was Linux-only "by nature". It is not, and
excluding it on that belief would have removed a working tool from every Mac jail for a
reason nobody would have re-checked.

So the exclusion list is **9 of 36** — one by necessity, eight by policy — and the floor is
**27**:

```console
$ nix eval --impure --json .#yoloNoncontainerFloorNames.aarch64-darwin
["bashInteractive","git","ripgrep","fd","curl","cacert","mise","which","nodejs_24",
 "python3","go","neovim","gh","gzip","bzip2","xz","unzip","zip","zlib","procps",
 "overmind","jq","uv","socat","sox","openssl","tzdata"]
```

### 9.2 The fatal, measured

Removing `iptables` from `noncontainerFloorUnbuildable` and evaluating the darwin profile
from this Linux jail:

```
error: yolo: the non-container package FLOOR has a hole in it: "iptables" has no
       aarch64-darwin build.
       …
       Fix it in flake.nix, in one of two ways:
         • add "iptables" to `noncontainerFloorUnbuildable`, with the reason it cannot build; or
         • drop it from `coreFloorNames` if the image does not need it either.
```

That is [`OQ-P1`](#decision-ledger) working: the eval dies naming the package, rather than
the launch succeeding with a hole in it.

### 9.3 The policy assertion, which is the part nix cannot do

The fatal covers necessity and **cannot cover policy** — a GNU-userland package left off the
list builds fine and ships silently. `internal/darwinpkg/floor_policy_test.go` closes that,
and the shape is what matters:

- it reads the **derived floor**, not the exclusion list, so it examines names added after it
  was written;
- `IsGNUUserland` is a **predicate**, not a list: the `gnu`-prefix half catches a package
  nobody thought to exclude, and it over-reaches on purpose (`gnupg`, `gnuplot` would be
  flagged) because a false positive costs one deliberate decision where a false negative
  costs a silent shipment;
- a **mutation cell** re-derives the floor with each policy exclusion put back and asserts
  the gate fires, so the predicate cannot become vacuous;
- `floor_drift_test.go` parses `flake.nix`'s three lists and fails when the Go copy
  disagrees — without it the assertion above would pass about a list that no longer describes
  the jail.

Measured three ways round: dropping `gnused` from both policy lists trips it; adding
`gnumake` to the image core trips it **with no list edit at all**; deleting
`noncontainerFloorPackages` from the profile's `paths` trips the call-site gate.

⚠ **One judgement call is flagged rather than made.** `gzip` is GNU gzip and macOS ships a
NetBSD one, so it is the closest call on the floor — and [`OQ-P2`](#decision-ledger)'s own
table does not name it. It is on the floor today, and the predicate deliberately does not
claim it; widening a ruling is not an implementer's call. `bashInteractive` is deliberately
not claimed either, for a stated reason: macOS's own `/bin/bash` **is** GNU bash, frozen at
3.2 by a licence change, so a modern bash is not a BSD-vs-GNU surprise.

### 9.4 Two profile attrs, and why that is not a spelling choice

`yoloNoncontainerPackages` **kept** its meaning — the declared `packages:` alone — and the
floor went into a new `yoloNoncontainerProfile`. The reason is a consumer this document had
not considered: the container path's store delivery (`YOLO_STORE_PACKAGES=1`) realizes the
same attr into `/run/yolo/packages/bin`, a directory that sits **ahead of `/bin`** on PATH.
Putting the floor there would have silently rerouted 27 names the image already bakes through
a boot-written farm, on a backend this design is not about.

### 9.5 What the floor changed by arriving, with no code change of its own

`$YOLO_DARWIN_LOGIN_PATH` is what `entrypoint.agentPath` returns, and **three generators** ask
it *"will the agent have this binary?"* before writing anything. Widening it flips all three:

| Generator | Before the floor | After |
| :--- | :--- | :--- |
| `GenerateShims` | the `guardrails` pack's `grep`/`find` rules were **dropped**, because a blocker is only written when its declared replacement is on PATH and `rg`/`fd` were not there | both blockers are generated |
| `launchercollision` | a pack declaring `program git` got a lazy launcher, ahead of everything | no launcher — the environment provides it |
| `AssertRequiredBins` | a pack's `requires: rg` warned | satisfied |

All three are pinned in both directions in `internal/entrypoint/darwinfloor_test.go`. ⚠ Its
system dirs are deliberately **fake**: that suite runs inside the yolo-jail image, where
`/bin/rg` exists, so the real ones would make the floorless cells pass on a Mac and fail in
CI for a reason unrelated to the code.

### 9.6 The cost this bought, stated

Two things a user will notice, both accepted by [`OQ-P1`](#decision-ledger) rather than
overlooked:

- **Every macos-user launch now needs the repo root**, where a bare `yolo -- bash` with an
  empty `packages:` previously needed none. The exemption in `run.Run` is gone and its
  message rewritten to say the backend builds its core set from the flake whether or not you
  declare anything. `--dry-run` is still exempt: it materializes nothing.
- **The first launch on a machine builds or substitutes a 27-package native closure.** ⚠ NOT
  MEASURED — the design's own [§7](#7-risks) says *"measure before assuming it is a
  problem"*, and that measurement is still owed. Cachix applies
  (`--accept-flake-config` is on every call).

### 9.7 What is left

Half two — the confined provisioning stage — is unchanged and unbuilt
([§4](#4-the-proposed-shape)). Its dependency on the home split is discharged
([§8](#8-sequencing)). The `via: npm` agent-launcher failure is *no longer* the loud-but-late
case [§2](#2-what-this-costs-today) describes, because the floor supplies node and npm — but
that claim is NOT MEASURED and is exactly the sort of thing a hardware run should check
first.

## Open Questions

**None — both closed 2026-09-11.** The rulings are in the [Decision Ledger](#decision-ledger) and
folded into [§4](#4-the-proposed-shape).


## Decision Ledger

| ID | Ruling / Decision | Date | Settled in |
| :--- | :--- | :--- | :--- |
| OQ-P1 | **The floor is EVERYTHING the container image bakes, minus an EXPLICIT darwin exclusion list.** Never a silent skip: *"I'd rather pain than something silently skipped […] if something's not available on Darwin we need to explicitly exclude it rather than silently skip it, because that will lead to sadness. And if we have a fatal error, then we have the opportunity to fix it."* A package that is neither buildable on darwin nor on the exclusion list is a **fatal**, not an omission. Ruled *against* the leaning's minimum-plus-`git`. | 2026-09-11 | [§4](#4-the-proposed-shape) |
| OQ-P2 | **No GNU userland.** This backend's proposition is *"your Mac, confined"* — an agent whose `sed -i` behaves differently from the human's is a surprise in the direction that costs more, and yolo's own darwin shims already speak BSD (`GNUStat=false`). Revisit if a pack turns out to depend on GNU behavior. | 2026-09-11 | [§4](#4-the-proposed-shape) |
| OQ-P1 | **Still open** — how much floor. Rule [`OQ-P2`](#decision-ledger) first; it decides nine of the 36. | — | [Open Questions](#open-questions) |
| OQ-P2 | **Still open** — GNU userland or the Mac's own. | — | [Open Questions](#open-questions) |
| OQ-P3 | The container's own partition: mise **data** machine-wide with `MISE_DATA_DIR` set **explicitly**, because the unset default would land inside the per-workspace `~/.local` symlink; mise config, the npm prefix and `~/.local` per-workspace through the home split's sidecar symlinks. A per-workspace `MISE_DATA_DIR` is rejected twice over. | 2026-09-11 | [§4](#4-the-proposed-shape), [§5](#5-what-this-does-not-propose) |
| OQ-P4 | Unconditional, before the agent, matching the container. [§4](#4-the-proposed-shape)'s own skip rule already delivers on-demand's only benefit, and on-demand would be a second dialect of "when are my tools there". | 2026-09-11 | [§4](#4-the-proposed-shape), [§6 row E](#6-alternatives) |


> [!IMPORTANT]
> **The two rulings compose: [`OQ-P2`](#decision-ledger) populates the first entries of
> [`OQ-P1`](#decision-ledger)'s exclusion list.** And the list has **two kinds of entry**, which the
> mechanism must keep apart because only one of them is protected by the fatal:
>
> | Kind | Why excluded | What happens if you forget |
> | :--- | :--- | :--- |
> | **Unbuildable on darwin** — e.g. `iptables`, which is Linux netfilter | necessity | the build **fails**, which is the fatal doing its job |
> | **GNU userland** — `gnused`, `gnugrep`, `gawk`, `coreutils-full`, `findutils`, `gnupatch`, `diffutils`, `gnutar` | **policy**, per [`OQ-P2`](#decision-ledger) | it builds fine and **ships silently**, and the agent gets GNU `sed` on a Mac — the exact surprise P2 rules out |
>
> **So the fatal covers necessity and cannot cover policy.** A GNU package left off the list is
> precisely the silent skip this ruling exists to prevent, arriving by the other door — which means
> the policy exclusions need their own assertion (a test over the darwin floor), not just a list
> someone maintains.
>
> **Deriving the list is cheap and does not need the Mac.** `nix eval` is cross-platform, so
> evaluating the floor for `aarch64-darwin` from a Linux jail names the unbuildable set without
> hardware. Do that before hand-writing entries — the 2026-09-11 Mac session deliberately did *not*
> ask for this, on the grounds that it is faster from here.