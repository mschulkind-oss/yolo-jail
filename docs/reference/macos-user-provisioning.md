---
status: current
verified: 2026-09-21
verified_commit: 753bcb88
covers:
  - flake.nix
  - internal/darwinpkg/floor.go
  - internal/darwinpkg/darwinpkg.go
  - internal/darwinpkg/materialize.go
  - internal/darwinpkg/gcroot.go
  - internal/provision/provision.go
  - internal/macosuser/provision.go
  - internal/macosuser/orchestrator.go
  - internal/macosuser/runplan.go
  - internal/macosuser/envfile.go
  - internal/macosuser/macosuser.go
  - internal/entrypoint/darwinstage.go
  - internal/entrypoint/darwin.go
  - internal/entrypoint/darwinhomelayout.go
  - internal/entrypoint/shell.go
  - internal/config/lsp.go
  - internal/cli/run/run.go
  - internal/cli/run/flock.go
  - internal/cli/run/loopholeinert.go
  - .github/workflows/macos-user.yml
tags: [macos-user, provisioning, packages, mise, floor, stage, backend-parity]
summary: "macos-user provisions itself the way a container jail does, by two mechanisms it had neither of until 2026-09-12: a native darwin FLOOR (every nixpkgs name the image bakes, minus an explicit exclusion list, fatal on a hole) and a Seatbelt-confined provisioning STAGE run as a privileged step between the bootstrap and the agent. This is what each delivers, where the stage's state lands, which claims a hardware session and the nightly CI job have measured, and the one cost nobody has recorded."
---

# macos-user provisions itself — a native floor, then a confined stage

**Status:** CURRENT as of 2026-09-21, verified against `753bcb88`.

A container jail gets its tools from two places: an image **floor** that exists before any
config asks for anything, and an imperative **stage** that runs inside the jail before the
agent starts. `macos-user` bakes no image and, until 2026-09-12, ran no stage — so
`mise_tools` and `lsp_servers` rendered agent config and installed nothing. It now has both,
built from the same sources the container path uses: the floor is the image's own core list
resolved natively for this Mac, and the stage is the container's own step body run as a
**privileged step confined by the session's Seatbelt profile**, between the darwin bootstrap
and the agent.

> **In short.** The floor is *everything the image bakes, minus an explicit exclusion list* —
> a name that is neither buildable on darwin nor excluded is a **fatal**, never a silent skip.
> The stage is *four of the container's six steps*, run under `sandbox-exec` with the session
> profile, skipped entirely when the config declares no tools. The stage's state lands exactly
> where the container puts it: mise's tool store machine-wide, everything else per-workspace.

> [!NOTE]
> **Two coined terms, neither of which is a config key and neither of which appears in the
> code under these names.** The **floor** is the set of packages present before any config
> asks for anything. The **stage** is the imperative provisioning step a launch runs inside
> the jail, after the floor exists and before the agent starts. ⚠ The stage is **not** the
> darwin bootstrap: that runs yolo's own generators, installs nothing, and runs **outside**
> the Seatbelt profile.

| Component | Lives in |
| :--- | :--- |
| The floor, resolved and made fatal | [`flake.nix`](../../flake.nix) (`coreFloorNames`, `noncontainerFloorUnbuildable`, `noncontainerFloorPolicy`, `noncontainerFloorPackages`, `yoloNoncontainerProfile`) |
| The floor's Go mirror and the policy predicate | `internal/darwinpkg` (`floor.go`: `ImageCoreNames`, `FloorExcludedUnbuildable`, `FloorExcludedPolicy`, `FloorNames`, `IsGNUUserland`) |
| Native materialization of the floor profile | `internal/darwinpkg` (`materialize.go`: `Materialize`, `materializeArgv`; `darwinpkg.go`: `FloorProfileAttr`, `BuildFloorProfileArgv`) |
| The stage body, shared by both backends | `internal/provision` (`Setup`, `Script`, `StartupLog`, `FailedMarker`, the `Step*` constants) |
| This backend's subset, argv and skip rule | `internal/macosuser` (`provision.go`: `ProvisionSetup`, `ProvisionScript`, `ProvisionArgv`, `ProvisionNeeded`, `ProvisionBootstrapScript`) |
| The third step, and its failure policy | `internal/macosuser` (`orchestrator.go`: `runProvisionStage`) |
| The plan, and the invariants that pin the stage | `internal/macosuser` (`runplan.go`: `BuildRunPlan`, `PlanInvariants`) |
| The stage's environment | `internal/macosuser` (`envfile.go`: `SandboxEnvFile`, `SandboxEnvFileContent`, `ExecWithEnvFile`) |
| The generated script the stage execs | `internal/entrypoint` (`darwinstage.go`: `DarwinBootstrapScriptPath`, `GenerateDarwinBootstrapScript`) |
| The per-workspace launch lock, reached through a seam | `internal/cli/run` (`flock.go`: `AcquireWorkspaceLockFor`) |
| What still warns on this backend | `internal/cli/run` (`loopholeinert.go`: `noteMacosUserContentGaps`) |
| The nightly instrument | [`.github/workflows/macos-user.yml`](../../.github/workflows/macos-user.yml), `integration/macosuser*_test.go` |

**Reads with:** [`nix-across-backends.md`](nix-across-backends.md) (what nix produces for each
backend, and why an image is a floor),
[`macos-user-nix-and-features.md`](macos-user-nix-and-features.md) (the backend itself),
[`macos-user-home-tiers.md`](macos-user-home-tiers.md) (the home split the stage writes
through), and
[`../plans/runbooks/macos-user-manual-checks.md`](../plans/runbooks/macos-user-manual-checks.md)
(the spec for what only a Mac can verify, and the automated twins that stand in for six of its
ten items).

---

## The floor

`flake.nix` keeps the image core as a list of nixpkgs attr **names** (`coreFloorNames`),
because two consumers resolve the same names against different package sets:
`corePackagesFromNixpkgs` maps them over the image's package set, and
`noncontainerFloorPackages` maps them over this flake's own `system`. The floor for a
non-container notch is that core, **minus an explicit exclusion list, minus anything the user
declared in `packages:`**.

The last subtraction is not tidiness. `buildEnv` dedups two identical store paths but
**collides** on two different builds of one name, so a user who pins `{"name": "git",
"version": …}` would otherwise turn their own declaration into an eval failure. Dropping the
floor's copy lets the user's spec win.

**A hole in the floor is fatal.** `noncontainerFloorPackages` resolves each name lazily and
`throw`s when a name is present in the core, absent from the exclusion list, and unavailable
for the target system — naming the package and both ways to fix it (add it to
`noncontainerFloorUnbuildable` with a reason, or drop it from `coreFloorNames`). Reading the
diagnostic attrs never forces the list, so the floor stays **inspectable by `nix eval` on a system
where building it would throw** — which is a human's instrument, not `yolo check`'s. Nothing in Go
reads those attrs today: check's `Declared packages` section resolves the profile's GC-root
symlink and never invokes nix (`internal/cli/check/section_packageprofile.go`), so it can report
an absent, dangling or resolved root and never a hole in the floor.

**The unbuildable half is derived, not guessed.** `nix eval` is cross-platform, so the set was
read from a Linux jail against both darwin systems this flake locks. Exactly one name came
back unavailable: `iptables` (Linux netfilter; `meta.unsupported = true` on both). ⚠ `procps`
is **not** one of them, though several docs once said it was Linux-only "by nature": on darwin
nixpkgs resolves that attr through `unixtools`, a wrapper around the Mac's own BSD
`ps`/`pgrep`/`pkill` — which is also exactly what the no-GNU-userland ruling wants. Guessing
would have removed a working tool from every Mac jail for a reason nobody would have
re-checked.

### The exclusion list has two kinds of entry, and the fatal protects only one

| Kind | Attr | Why excluded | What happens if you forget an entry |
| :--- | :--- | :--- | :--- |
| **Unbuildable on darwin** | `noncontainerFloorUnbuildable` | necessity | the eval **fails**, naming the package — the fatal doing its job |
| **GNU userland** | `noncontainerFloorPolicy` | policy | it **builds fine and ships silently**, and the agent gets GNU `sed` on a Mac |

**So the fatal covers necessity and cannot cover policy**, and nix has no predicate for "this
is GNU userland". `internal/darwinpkg/floor_policy_test.go` is that assertion instead, and its
shape is the load-bearing part:

- it reads the **derived floor**, not the exclusion list, so it examines names added after it
  was written;
- `IsGNUUserland` is a **predicate**, not a list: the `gnu`-prefix half catches a package
  nobody thought to exclude, and it over-reaches on purpose (`gnupg`, `gnuplot` would be
  flagged) because a false positive costs one deliberate decision where a false negative costs
  a silent shipment;
- a **mutation cell** re-derives the floor with each policy exclusion put back and asserts the
  gate fires, so the predicate cannot become vacuous;
- `floor_drift_test.go` parses `flake.nix`'s three lists, fails when the Go copy disagrees, and
  separately fails if `noncontainerFloorPackages` stops reaching `yoloNoncontainerProfile`'s
  `paths` — without which the assertion above would pass about a list that no longer describes
  the jail.

> [!WARNING]
> **The predicate's unprefixed half is a known-incomplete SUPPLEMENT, not an enumeration, so
> adding a name to `coreFloorNames` still needs a human to ask the question.** Measured
> 2026-09-12: adding `cpio` (GNU cpio 2.15) or `ed` (GNU ed 1.22.5) passes the policy gate
> green, and `m4`, `nano`, `bc`, `time`, `texinfo`, `groff` and `wget` are the same shape. The
> honest oracle is `meta.homepage` containing gnu.org, which is a nix evaluation a Go test
> cannot perform.
>
> **`which` is the case that already happened.** A Go comment asserted that darwin resolved
> both `which` and `procps` through `unixtools`; only the `procps` half was true. `pkgs.which`
> is GNU `which-2.25`, so GNU `which` shipped on a floor whose governing ruling is *no GNU
> userland* with the policy gate green. It is now on both policy lists, and macOS's own
> `/usr/bin/which` serves instead. ⚠ `gzip` is the closest remaining call — it **is** GNU gzip
> and macOS ships a NetBSD one — and the predicate deliberately does not claim it, because
> widening a maintainer's ruling is not an implementer's call.

### Two profile attrs, which is not a spelling choice

`yoloNoncontainerPackages` means the **declared `packages:` alone**; the floor plus the
declared set is a separate attr, `yoloNoncontainerProfile` (`darwinpkg.ProfileAttr` and
`darwinpkg.FloorProfileAttr`). The reason is a second consumer: the container path's
store delivery (`YOLO_STORE_PACKAGES=1`) realizes `yoloNoncontainerPackages` into
`/run/yolo/packages/bin`, a directory that sits **ahead of `/bin`** on PATH. Putting the floor
there would silently reroute every floor name the image already bakes through a boot-written
farm, on a backend this mechanism is not about.

`darwinpkg.Materialize` — whose only caller is the macos-user launch — builds
`FloorProfileAttr` and writes its `--out-link` to `ProfileRootLink(home)`, so the closure the
agent executes from is GC-rooted. `MaterializeAt`, the container's caller, takes `ProfileAttr`
with a content-keyed link.

### What the floor changed by arriving

`$YOLO_DARWIN_LOGIN_PATH` is the PATH a generator probes before it writes anything:
`entrypoint.agentPath` and `entrypoint.imageProbePath` both return it, and every generator that
asks *"will the agent have this binary?"* reads one of the two. Widening it flips the three
answers below, and `PlanInvariants` fails a plan whose bootstrap env does not carry the store bin
dirs for exactly this reason — each of them answers *wrong* rather than failing:

| Generator | Floorless | With the floor |
| :--- | :--- | :--- |
| `GenerateShims` | the `guardrails` pack's `grep`/`find` blockers were **dropped**, because a blocker is written only when its declared replacement is on PATH and `rg`/`fd` were not there | both blockers are generated |
| `launchercollision`'s `imageProbePath` | a pack declaring `program git` got a lazy launcher, ahead of everything | no launcher — the environment provides it |
| `AssertRequiredBins` | a pack's `requires: rg` warned as absent | satisfied |

Those are the readers whose answer the floor demonstrably moved, not all of them: the
package-manager launchers, the launch-flag wrapper and the chromium probe read the same PATH
through `imageProbePath` and ask about names the floor does not carry (`pnpm`, `chromium`), so
their answers are unchanged. The full set is the callers of those two accessors —
`rg -n 'agentPath\(|imageProbePath\(' internal/entrypoint` — rather than a number on this page.

Two costs a user notices, both accepted rather than overlooked:

- **Every macos-user launch needs the repo root**, where a bare `yolo -- bash` with an empty
  `packages:` previously needed none, because every launch now builds the floor
  (`internal/cli/run/run.go`). `--dry-run` stays exempt: it materializes nothing.
- **A successful build that contributes no `bin` dir is fatal**, and only became possible when
  the floor made the closure unconditional. Before that an empty profile was the honest answer
  to an empty `packages:`; now it means the build returned something nothing could derive a
  PATH entry from. `PlanInvariants` carries the same rule for the non-nil case; the
  orchestrator covers the nil, which no invariant can see.

## The stage

The macos-user launch is five privileged steps, and the stage is the one added by this
mechanism:

1. the Seatbelt profile, installed root-owned (`0444`) at the session path, plus the staging
   commands that copy the yolo binary, the pack tree, the home overlay and the `/ctx` tree;
2. the **session env file** — everything this launch composed, root-owned `0600`, readable by
   the sandbox account, swept on every exit path below it;
3. the **darwin bootstrap** — `sudo --user=… /usr/bin/env -i … <staged yolo> internal
   darwin-bootstrap`, yolo's own generators, **with no `sandbox-exec`**;
4. **the stage** — `runProvisionStage`, confined;
5. the agent launch, under the same profile, with the workspace lock already released.

⚠ **The source numbers these 2, 2.5, 3, 3.5 and 4** (`internal/macosuser/orchestrator.go`), the
halves marking what was inserted later, and its *"the third privileged step"* counts only the three
the workspace lock covers — profile-plus-staging, bootstrap, stage. Against the list above the
stage is the fourth of five, so the ordinal is worth nothing on its own; the positional claim
(between the bootstrap and the agent) is what both spellings agree on.

**The stage's argv** (`ProvisionArgv`):

```text
sudo --user=_yolojail /usr/bin/env -i YOLO_BYPASS_SHIMS=1 \
     HOME=… USER=… SHELL=… PATH=… MISE_DATA_DIR=… YOLO_DARWIN_LOGIN_PATH=… YOLO_DARWIN_ENV_FILE=… \
     /usr/bin/sandbox-exec -f /var/yolo-jail/profile-<session>.sb -- \
     /bin/sh -c '<source the session env file, then exec "$@">' <name> <env file> \
     /bin/bash -c '<the wrapped stage script>'
```

`HOME`, `USER`, `SHELL`, `PATH`, `MISE_DATA_DIR` and `YOLO_DARWIN_LOGIN_PATH` are the **protected
set** — `macosuser.ProtectedSandboxEnvNames`, the identity quartet plus the store that travels
with PATH plus the login-PATH copy — and no caller may set any of them, on the argv or through the
file, because they are what make the process the sandbox user's rather than a copy of whatever the
invoking shell had. `YOLO_DARWIN_ENV_FILE` is on that argv and is **not** in the set: it is a path,
not an identity, so `SandboxArgvEnvProblems` allowlists it separately (alongside
`YOLO_BYPASS_SHIMS`) and the argv is free to name the file it reads. `/bin/sh` is the reader's
interpreter deliberately and is *not* the shell the wrapped command uses: the wrapper's whole job
is `.` and `exec`, which is POSIX, while the stage wants `bash` and the agent launch wants `zsh`
for their own bodies.

**The profile is the session's own** — the same one the agent gets, installed two steps
earlier — so nothing new has to exist for the stage to be confined, and a separately launched
`sandbox-exec` process is not the nested-profile case Seatbelt refuses. The stage is therefore
the **first** process under that profile, not a second.

> [!WARNING]
> **`(allow default)` is what that profile is**, so "confined" bounds the stage *outside* the
> sandbox and promises nothing inside it. Measured on hardware 2026-09-11: two vendor
> installers run under this very profile still appended a PATH export to
> `.bashrc`/`.zshrc`/`.zprofile`/`.bash_profile`, and one prompted `Start Codex now? [y/N]` on
> `/dev/tty` and waited for a human. Seatbelt was working correctly both times — the sandbox
> home is *supposed* to be writable and the tty is the launch's own. Every generated
> PATH-ordering and launcher artifact lives inside that home, so a stage that runs installers
> on a schedule inherits both effects, unprompted.

### What the stage runs, and what it does not

`ProvisionSetup` takes **four of `internal/provision`'s six steps**, and both omissions are
decisions with stated reasons:

| Step | Container | macos-user | Why |
| :--- | :--- | :--- | :--- |
| prune dangling store symlinks | ✅ | ❌ | gated on `YOLO_STORE_PRUNE_OK`, which the container's launcher sets only after proving no other jail is live. Nothing here computes that proof, so the step would be permanently inert — a line that reads like a feature and is one only on the other backend. |
| announce + `mise install --quiet` | ✅ | ✅ | the announce lines are steps, not decoration: a tee'd `startup.log` shows a reader the last thing that *started*. |
| announce + the generated bootstrap script | ✅ | ✅ | by **absolute path**, not `~/.yolo-bootstrap.sh` — see below |
| `~/.yolo-venv-precreate.sh` | ✅ | ❌ | its body tests `/workspace/mise.toml` and shells out to `/bin/python3`, so on a Mac it would find neither and exit 0 on every launch — a step that reports success having never run. Nothing generates it here either. **A Mac workspace configuring `_.python.venv` gets no pre-created venv.** |

**The skip rule** (`ProvisionNeeded`): the stage runs when `mise_tools` **or** `lsp_servers` is
non-empty, so a bare `yolo -- bash` in a workspace that declares no tools pays nothing — no
extra privileged step, no sudo, no `mise install` against an empty config. The plan carries no
stage argv at all in that case, and every invariant is written to say nothing about an empty
one.

**`mcp_presets` is deliberately not in the skip rule.** The preset *wrappers* are not generated
on this backend — their bodies are Linux-absolute, and `RunDarwinBootstrap` warns and says so —
so the npm packages behind them have nothing to be spawned by. Counting presets would start a
stage whose only work is a download nothing can exec.

> [!WARNING]
> **One stranded case, stated rather than closed.** The generated script's **uninstall** loop
> reads the sentinel of what the last run installed, so removing the *final* entry from
> `lsp_servers` flips `ProvisionNeeded` to false and that loop never runs: the package stays in
> the workspace's own npm prefix. Closing it needs a filesystem probe, and the plan is a pure
> function of the config by deliberate choice — a dry run has to describe the launch without
> touching the disk. Re-adding the key and removing it with a launch in between collects it.

### The generated script and the LSP sentinel are sidecar files, not home files

On the container, `~/.yolo-bootstrap.sh` is a **bind** of
`<workspace>/.yolo/home/yolo-bootstrap.sh`: the home path is the bind's appearance and the
sidecar path is where the bytes are. macos-user has no binds and one account home shared by
every workspace, so a home-rooted copy would put one workspace's generated script — its MCP
list, its receipts path — where the next workspace's launch overwrites it. That is the
cross-workspace write-write race the home split exists to end, re-created rather than
inherited.

So `GenerateDarwinBootstrapScript` writes `DarwinBootstrapScriptPath(<sidecar>)` and the stage
execs it absolutely through `provision.StepRunBootstrapAt`. Nothing on this backend needs
`~/.yolo-bootstrap.sh` to exist, because the only thing that execs the script is an argv yolo
composes itself — and a plan invariant asserts the argv names the exact path the generator
writes, which is meaningful only while both come from that one function.

**The LSP sentinel moved for the same reason, and it is the sharper case**
(`entrypoint.lspSentinelExpr`): it records what the last run installed into a prefix
(`~/.npm-global`) that the home split already made per-workspace, so a shared sentinel would
claim installs living in another workspace's directory. The container's half is unchanged — its
`$HOME/.yolo-installed-lsps` is already that workspace's file, by bind — which is why the
generator renders a shell **expression** rather than a path.

### The session env file is the stage's environment

`ProvisionArgv` puts the protected set and the env file's *path* on the command line, and
everything the launch **composed** — `env_sources`, provider credentials, git identity, the two
LSP install lists — inside the root-owned `0600` file the argv is wrapped to read
(`ExecWithEnvFile`). The rule applies to **every argv the sandbox user runs under Seatbelt**, so
both the stage and the agent launch are checked for it; the **bootstrap is excluded**, and bakes
its generator environment onto its own argv. Two consequences a maintainer needs:

- **The stage needs the composed values as much as the agent does.** `mise install` and the
  generated script read registry tokens and proxy settings out of `env_sources`, so the wrap is
  not belt-and-braces: a stage that composed its environment separately would install against a
  different environment than the agent then runs in, and the two spellings would be free to
  drift one key at a time.
- **A new composed variable fails a check by existing.** `SandboxArgvEnvProblems` holds a
  closed allowlist of what may appear on these argvs, so the next secret cannot arrive on a
  world-readable command line by someone adding a pair.

**`YOLO_BYPASS_SHIMS=1` is in the process environment**, not in an `sh -c '…'` prefix the way
the container spells it. The bypass is not optional — the generated script runs `find`, which the
`guardrails` pack blocks unconditionally with exit 127 (its `grep` blocker is gated on the
recursive flags and does not catch the script's own `grep -qxF`) — and putting it in the environment
also frees the script to embed an absolute path without nesting quotes. The agent, a separate
process, does not inherit it.

**The two LSP install lists cross into both environments, and "both" is the whole invariant.**
`YOLO_LSP_NPM_INSTALL` and `YOLO_LSP_GO_INSTALL` are resolved once per channel from
`config.LSPInstalls` — the one recipe table both backends share — and set into the **bootstrap
env** (where the catalog's orphan finders and the evergreen refresh set read them) *and* into
the **session env file** (where the stage's install loop reads them). `PlanInvariants` fails a
plan carrying only one crossing, because setting either alone is a launch that reports success
and installs nothing.

### No `sudo --login`, anywhere in this backend

`sudo -i` does not `execve` the argv it is given. Per sudo(8) it **concatenates** the command
and its arguments, backslash-escaping every character *except* alphanumerics, underscores,
hyphens and **dollar signs**, and hands the result to a login shell. Both consequences were
measured on hardware (macOS 26.5, arm64, 2026-09-11), through the launch argv, which carried
the flag at the time:

| Probe | With `--login` | Without |
| :--- | :--- | :--- |
| `bash -lc $'echo A\necho B'` | `Aecho B` — the newline arrived as a `\`-continuation and was removed | **`A` / `B`**, two lines |
| `bash -lc 'X=inner; echo got=$X'` | `got=` — an intermediate login shell expanded `$X` first | **`got=inner`** |
| a nine-binary `for b in …` floor probe | **nine blank lines** | all nine labelled, each resolving into the store profile |
| `command -v fzf` (the PATH acceptance bar) | store profile | **store profile** — unchanged |

> [!WARNING]
> **Neither failure is an error: the wrong command runs, exits 0, and prints plausible
> output.** That is why it cost a measurement before it was found — one runbook probe printed
> nine blank lines and an earlier probe run reported five successes for commands that never
> ran. Every container backend passes argv through `podman exec` untouched, so this was a
> backend-parity defect: the same `yolo --` invocation meant different things per backend, and
> only this one rewrote it.

**The flag is gone from both argvs, and `PlanInvariants` refuses it on each with the reason in
the message**, because the natural later edit is to make the two argvs "consistent". The last
row of the table is what settled it: what re-prepends PATH after macOS `path_helper` is the
user's **own downstream login shell** — `yolo -- bash -lc …` reads `/etc/profile` and then the
rc file `WriteLoginRC` generates, inside the sandbox — never sudo's outer shell, whose
environment `/usr/bin/env -i` wipes on the very next word. So the PATH the stage and the agent
run with is the explicit `PATH=` in the `env -i` list, the same `SandboxPath` value either way,
and there was no trade to make. The inner shell stayed `zsh -c` rather than becoming `zsh -l
-c`: a login zsh would newly run `/etc/zprofile`'s `path_helper` *inside* the sandbox, which is
a PATH change to buy nothing.

### The workspace lock

The launch takes the per-workspace lock through a `Deps` seam wired to
`run.AcquireWorkspaceLockFor` — `internal/macosuser` cannot import `internal/cli/run`, so the
call moved and the implementation did not. A second flock over there would be the third
hand-rolled copy in the tree and the one nothing compares to the others.

**The window is bootstrap-through-stage**, and the lock is taken above the first side effect to
cover it: the bootstrap generates the very script the stage execs, into the same sidecar, so a
second launch bootstrapping in between would have this one exec its script. It is **released
before the agent** — holding it across the session would make a second terminal in the same
workspace block until the first ended, a serialisation no backend has and which reads as a
hang. A lock that cannot be taken still launches, on the container's own reasoning: a workspace
lock is a courtesy against a self-inflicted race, not a safety property worth refusing over.

### A failing stage does not abort the launch

`provision.Script` wraps the body in a tee-to-log, the `PROVISIONING FAILED` marker, a red
console line and a continue/abort prompt. Its **exit status says exactly one thing: whether the
human asked to stop.** Every failure it survives — including every non-interactive one, where
there is nobody to ask — completes with status 0, because a jail whose tools did not install is
still a jail the user asked for and the record is in the log. Only the `n` answer propagates.

> [!WARNING]
> **The process's exit status is a different question, and conflating the two inverted the
> rule.** `sudo` refusing authorization, `sandbox-exec` rejecting the profile and a missing
> `/bin/bash` are all non-zero for reasons nobody chose. While the orchestrator treated every
> non-zero as a veto, a workspace that merely *declared* `mise_tools` could not launch at all,
> and the message blamed the user for it.

**The marker is the discriminator**, because the script writes it and it therefore exists if
and only if the script ran (`runProvisionStage`):

- non-zero **and** the marker is in this launch's log → a deliberate veto: abort, naming the log.
- non-zero **and no marker** → the stage never ran, so the status came from the exec layer: warn
  that the declared tools are *not* installed, and launch anyway.

The log is cleared first so *"this launch's log"* is a fact rather than a hope — the script
truncates it one instruction later anyway — and an unreadable log is treated as a veto, because
honoring a veto that was not given is recoverable and ignoring one that was is not.

**Three readers of that marker, only one of which is Go**, which is why it is defined once
beside its producer: `jailcontent.ReadProvisioningFailed` greps the startup log to decide
whether the briefing shows its banner, the built-in `diagnosing-the-jail` skill tells every
agent to look for exactly this string, and a human reads it in the log.

⚠ **The briefing reports the PREVIOUS launch's stage**, on this backend exactly as on the
container: each arm composes the briefing before it runs anything — `refreshJailBriefings` is
called inside the `macos-user` arm of `Run`, above `MacosUserRun`, and again inside `runContainer`
(`internal/cli/run/run.go`) — so a stage that fails today is bannered tomorrow.

`provision.StartupLog(workspace)` is a **function of the workspace**, not a constant rooted at
the container's fixed `/workspace` bind. The reader has always resolved the path from the
host's workspace, and the two agreed only because the container's bind made them one file; the
container's composed bytes are unchanged by the move, which is what makes it verifiable rather
than merely plausible.

## Where the stage's state lands

The container's own partition, which is also what makes a machine-wide mise store correct here:

| State | Container | macos-user |
| :--- | :--- | :--- |
| mise data (`installs/`, `shims/`) | machine-wide: `MISE_DATA_DIR=/mise`, a store dir or named volume (`assemble_parts.go`) | machine-wide: `macosuser.SandboxMiseData` names `<account home>/.yolo/mise` in the launch env, the bootstrap env and the PATH's shims dir |
| mise config (`~/.config/mise/config.toml`) | per-workspace: the `config` bind | per-workspace: the `config` sidecar symlink |
| npm prefix (`~/.npm-global`) | per-workspace: the `npm-global` bind | per-workspace: sidecar symlink |
| agent CLI installs (`~/.local`) | per-workspace: the `local` bind | per-workspace: sidecar symlink |
| the generated script, the LSP sentinel | per-workspace, *by bind*, at home-rooted paths | per-workspace, *by absolute path* into the sidecar |

`MISE_DATA_DIR` is set **explicitly**, and that is the point rather than a detail: mise's own
unset default is `$HOME/.local/share/mise`, and under the home-tier layout `~/.local` is a
symlink into `<workspace>/.yolo/home` — so leaving the default in place would put the tool store
in the **per-workspace** tier, which is the inverse of every other backend. It is emitted
alongside `PATH` because the two are one fact: the shims dir on that PATH is
`<MISE_DATA_DIR>/shims`, so a process resolving the shims from one value and the store from
another would run a shim whose install is elsewhere.

`~/.yolo/mise` sits under yolo's own namespace in the account home, whose sibling `~/.yolo/bin`
**is** a layout symlink. The two tiers meeting inside one directory is the layout's rule working
rather than an exception to it: a path is workspace tier when the layout links it and machine
tier otherwise.

> [!WARNING]
> **Sharing mise's DATA dir between workspaces is not a collision.**
> `installs/<tool>/<version>` is keyed by tool and version, so two workspaces asking for
> different tools *add* to the store rather than reshape it — which is why every container
> backend shares it machine-wide to begin with. Do not "fix" it by making it per-workspace:
> that is a mechanism no other backend has *and* the inverse of the container's partition. The
> collision that is real is mise's **config**, and it is closed by the `config` sidecar
> symlink.

## What each imperative config key delivers here

| Config key | Container | macos-user | Told? |
| :--- | :--- | :--- | :--- |
| `mise_tools` | installed by the stage | **installed**: the floor puts `mise` on the sandbox PATH, `MISE_DATA_DIR` names a real machine-wide store, and the stage runs `mise install` | no warning — the gap is closed |
| `lsp_servers` | npm/go-installed by the stage | **installed**, since the two install variables cross into both environments | no warning — the gap is closed |
| `mcp_presets` | npm-installed by the stage | wrappers not generated, packages not installed | warns — **from inside the bootstrap** (`RunDarwinBootstrap`), so `--dry-run` never shows it |
| agent CLIs, `via: installer` | the launcher execs the vendor installer | **works** — `curl` and `bash` are at `/usr/bin` | n/a — nothing to tell |
| agent CLIs, `via: npm` | the launcher execs `npm install -g` | the floor supplies node and npm, so the launcher can run — **not measured on hardware** | `GenerateAgentLaunchers` has no *generation*-time precondition, so nothing warns at launch; a failure lands on the user's first real command |
| `packages:` | baked into the image | realized natively, and now composed with the floor | works |

`rg -n '"via": "(installer|npm)"' packs/*/pack.json` is the split; do not write the membership
down, it has already grown once. The hardware measurement that established the `installer` half
works covered the packs that existed on 2026-09-11 (three of six, two installing from scratch);
the set is larger now.

> [!WARNING]
> **The evergreen half of the agent CLIs is a different claim from the install half, and it is
> unproven here.** The generated launchers bound their update arm with `timeout(1)`, which is
> GNU coreutils: the image bakes it and a stock macOS does not — and `coreutils-full` is a
> policy exclusion on this floor, so `_bounded` takes its unbounded branch. Running unbounded
> is a better answer there than not updating at all, and a vendor updater that hangs has
> nothing to stop it. Installing once is measured; staying current is not.

## The two retired warnings, and the rule that retired them

`mise_tools` and `lsp_servers` each carried a launch warning naming a mechanism — *"nothing runs
`mise install`"*, *"the installer is a generated bootstrap script this backend deliberately does
not run"* — and both mechanisms became false. The warnings are gone, on the rule the
surrounding code already applies: **a warning describing a closed gap teaches the reader to
distrust the warnings that are still true**, and a reader would have acted on these by writing
their config around a limitation that no longer exists. What is *not* retired is `mcp_presets`,
which really is still undelivered.

> [!WARNING]
> **One of the two retirements was premature, and the absence of a warning is not evidence of a
> feature.** `mise_tools` was correct — two declared tools installed from scratch into the
> machine-tier store on hardware. `lsp_servers` was not: the stage did exec the generated
> script, but the script installs from `$YOLO_LSP_NPM_INSTALL` / `$YOLO_LSP_GO_INSTALL`, and
> the only producer of either in the tree was the container's podman `-e` lines. This backend
> set `YOLO_LSP_SERVERS` — the table that *renders* an agent's config — and neither install
> variable, so the stage **exited 0 having installed nothing** while the config it had just
> rendered named servers that were not on disk.
>
> The gap was wired shut rather than re-warned, and the corrective is structural: the positive
> half of a retirement lives where it can be observed. A test drives the orchestrator and fails
> if the stage's call site is deleted; `PlanInvariants` fails when either LSP crossing is
> deleted; and `integration/TestMacosUserDeclaredToolsArrive/lsp_servers` is the oracle,
> because **the wiring is a source fact and the install is a hardware one.**

## What this backend does not do

- **No image.** The absence of one is the backend's whole reason to exist; a floor is a nix
  profile, not a filesystem.
- **No second config surface.** There is no `macos_packages` and no per-backend `mise_tools`.
  If a package is Linux-only, `platforms: ["linux"]` already says so, and the refusal message
  names the spelling.
- **No per-workspace `MISE_DATA_DIR`.** Doubly wrong: a mechanism no other backend has, *and*
  the inverse of the container's partition.
- **The stage is never run unconfined**, even though the bootstrap is.
- **`--dry-run` materializes nothing**, which is why it is the one thing exempt from the
  repo-root requirement the floor introduced.

## What is measured, and what is not

Two instruments, and the difference between them is worth keeping straight.

**A hardware session on 2026-09-12** — one Apple Silicon Mac, macOS 26.5 arm64, with the host
`yolo` taken to HEAD first, because on this backend the host binary *is* the implementation.
Recorded per item in
[`macos-user-manual-checks.md`](../plans/runbooks/macos-user-manual-checks.md). What it settled:

- **The floor reaches the sandbox PATH** (runbook item 6) — all nine probed floor binaries
  resolved into the noncontainer profile's `bin`, **including `git` and `curl`, which have
  `/usr/bin` rivals**, so the re-prepend beats `path_helper` for the floor and not only for
  `packages:`. And the policy half in full: every `FloorExcludedPolicy` name resolved to
  `/usr/bin`, never the store.
- **The stage runs, is confined, reaches the network and writes its prefixes** (item 7) — the
  startup log was truncated to that launch, recorded `mise install` and `bootstrap`, carried no
  `PROVISIONING FAILED`, and its banner timestamp fell inside the launch's own window. That one
  launch settles four things at once, and the timestamp check is the one a `--login`-mangled
  script cannot pass while still exiting 0.
- **`mise_tools` arrive, in the right tier** (item 9) — two tools installed from scratch under
  `/Users/_yolojail/.yolo/mise/installs`, a **real directory in the account home** while its
  sibling `~/.yolo/bin` is a symlink. The contrast is the assertion: without it, "not a symlink"
  also passes on a launch where the layout never ran.
- **The forwarding fix**, measured both ways in one launch each (the table above).

**The nightly CI job** — [`macos-user.yml`](../../.github/workflows/macos-user.yml), 07:00 UTC
on `macos-latest`, running the gated `^TestMacosUser` suite. It needs no jail image, which is
the point: a macos-user launch returns before `runContainer` and never loads one, so none of
the chain that blocks the container macOS nightly reaches it. It has run nightly since
2026-09-13. Six of the runbook's ten items have twins there; the stage's own twin is
`TestMacosUserProvisioningStageRunsAndRecordsItself`.

⚠ **A suite that skips must not look like a suite that passes.** Every one of these tests skips
on every machine that develops this repo, and `go test` reports a skip as a pass — so the job
sets `YOLO_TEST_MACOS_USER=1` and a declared run that executed **zero** of them exits non-zero,
printing every skip and its reason. The mechanism is `integration/macosusergate_test.go`, and
the declaration lives in the workflow because only the step that scheduled the work knows what
it was scheduled to do.

**What no instrument covers:**

- **The `via: npm` agent launchers on this backend.** The floor supplies node and npm, so the
  loud-but-late `npm: command not found` should be gone. Nothing has run it.
- **Whether a real `sandbox-exec` rejection takes the continue branch.** The fault injection
  the runbook prescribes (`chmod 000 /usr/bin/sandbox-exec`) is a global, SIP-adjacent mutation
  no test should make, and the stage argv names `/usr/bin/sandbox-exec` absolutely so no PATH
  shim can reach it. Closing it needs a seam in `internal/macosuser`, not a cleverer test.
- **The interactive veto.** `provision.Script` gates its prompt on `[ -t 0 ]` and no test in
  the suite gives a child a terminal. The twin does assert that no unanswerable prompt is
  emitted without a tty, which is the failure the gating exists to prevent.
- **Which `bash` parses the stage script.** The Go test parses it with whatever `bash` is on
  the test machine's PATH, while the argv hands it to `/bin/bash` — Apple's **3.2**. The bodies
  were read for 4-plus-only constructs and none was found (`${PIPESTATUS[0]}`, `${v//pat/}`,
  `local` and `<<<` are all 3.2-safe), so the risk is low; the gap is that the claim covers
  more than the instrument does.

Two items that were once on this list are **resolved rather than owed**. The stage's working
directory is always the workspace it is provisioning — `yolo` has no way to name a workspace
other than by being in it (no flag, no walk-up, no host-side override), and neither `sudo`
without `--login` nor `env -i` changes directory — so the launch "from outside the tree" that
would test it **cannot be spelled**; it launches a *different* workspace. And a failing stage's
banner reaching the briefing is covered end to end by
`TestMacosUserAFailingProvisioningStageDoesNotAbortTheLaunch`, which asserts the evidence — the
marker in the log and the red console line — before the verdict.

### The one unmeasured claim — what a first stage costs

**Nobody has recorded what a first macos-user launch costs**, and this is the residue that
survives everything above. An LSP-configured workspace's first launch builds the whole native
floor closure, then runs `mise install` plus an `npm install -g` per server, **in series**,
before the agent starts. The floor comes from `cache.nixos.org` — this project's own
substituter serves Linux closures only — so whatever part of it has no darwin build compiles
on the spot.

Three places depend on that number not existing:

- **The nightly job is deliberately not wired to `push`/`pull_request`**, and its own header
  says why: wiring it would commit every PR touching this backend to an unmeasured wait. *The
  first green run is the measurement*; a `paths:` filter is the obvious next step once there is
  a duration to size it against.
- **Both timeouts are ceilings on waste, not estimates** — the job's `timeout-minutes` and the
  suite's per-launch deadline (`YOLO_TEST_MACOS_USER_TIMEOUT`, seconds). The job's cap is sized
  so that one launch hitting its deadline still leaves room for the remaining tests to report,
  because a cap that fires first replaces six results with none. **Revise either from a number
  a green run produced, never from a guess.**
- **The number is emitted but not read.** `TestMacosUserProvisioningStageRunsAndRecordsItself`
  logs the launch total, the part before the stage (floor build + staging + bootstrap) and the
  part after it, split at the banner — which is the stage's own first instruction. It is a
  `t.Logf` and not an assertion on purpose: there is no budget to compare against, and
  inventing one would turn the first real number into a failure.

## Why it is this way

Rulings a future change would otherwise undo, kept with the IDs source comments cite.

| Ruling | Why it holds |
| :--- | :--- |
| <a id="oq-p1"></a>[`OQ-P1`](#oq-p1) — the floor is **everything the image bakes, minus an explicit darwin exclusion list**, and a name that is neither buildable nor excluded is a **fatal** | *"I'd rather pain than something silently skipped […] if something's not available on Darwin we need to explicitly exclude it rather than silently skip it, because that will lead to sadness. And if we have a fatal error, then we have the opportunity to fix it."* Ruled 2026-09-11 **against** its own leaning, which was a minimum of mise + nodejs + git. Cited across `flake.nix`, `internal/darwinpkg`, `internal/entrypoint`, `internal/cli/run`, `internal/macosuser` and the integration suite — `rg -n 'OQ-P'` over the tree is the list, and do not transcribe it here. |
| <a id="oq-p2"></a>[`OQ-P2`](#oq-p2) — **no GNU userland** on this floor | This backend's proposition is *"your Mac, confined"*, and an agent whose `sed -i` behaves differently from the human's is a surprise in the direction that costs more; yolo's own darwin shims already speak BSD (`GNUStat=false`). Revisit if a pack turns out to depend on GNU behaviour. Cited by `flake.nix`, `darwinpkg/floor.go`, both of that package's floor gates and the integration floor twin; the same grep finds them. |
| [`OQ-P1`](#oq-p1) + [`OQ-P2`](#oq-p2) compose — the policy exclusions need their own assertion | The fatal covers **necessity** and cannot cover **policy**: a GNU package left off the list builds fine and ships silently, which is the same silent skip [`OQ-P1`](#oq-p1) exists to prevent arriving by the other door. Hence a predicate and a mutation cell, not a maintained list. |
| <a id="oq-p3"></a>[`OQ-P3`](#oq-p3) — the container's own partition: mise **data** machine-wide with `MISE_DATA_DIR` set **explicitly**; config, the npm prefix and `~/.local` per-workspace | The unset default lands inside the per-workspace `~/.local` symlink, so the machine tier has to be *named* to stay the machine tier. A per-workspace store is rejected twice over — no other backend has it, and it inverts the container's partition. Cited by `macosuser/macosuser.go` and `macosuser/misedatadir_test.go`. |
| <a id="oq-p4"></a>[`OQ-P4`](#oq-p4) — the stage runs **unconditionally, before the agent**, never on demand | The config can change between launches and the jail must reflect it. The lazy launchers cover agent CLIs and neither `mise_tools` nor `lsp_servers`, so triggering off them would leave one config with tools absent here and present on podman — a second dialect of *"when are my tools there"*. The skip rule already delivers on-demand's only benefit. |
| <a id="oq-p5"></a>[`OQ-P5`](#oq-p5) — **wire** the two LSP install variables; do not restore the retired warning | A warning is what you leave when the gap stays; this one did not have to. Both variables now cross into the bootstrap env **and** the session env file, from the one recipe table both backends share. |
| **P1** — a backend either provides a mechanism or **refuses it out loud** | Rendering the config for a mechanism that does nothing is the failure this whole mechanism exists to end, and it has produced an instance per imperative key on this backend. |
| **P2** — the declarative path is the one that composes | `packages:` works here precisely because nix needs nothing pre-installed. Every imperative installer assumes a runtime something else put there — which is why the floor has to exist before the stage can. |
| **P3** — convergence beats a second dialect | Two ways to say "install neovim" depending on backend is a tax on every user and every doc. One mechanism on every backend; only the primitive enforcing the boundary may differ. |
| **P4** — third-party installers run confined; yolo's own generators need not | The bootstrap runs outside Seatbelt, which is tolerable because it executes only yolo's code against a root-owned staged tree. The stage runs `npm install` postinstall hooks and mise plugins — vendor code — and the container runs that inside the jail, so running it unconfined here would be a regression the container never had. ⚠ Sharpened by measurement rather than confirmed: see the `(allow default)` warning. |
| **The ordering is not a preference** | `mise install` needs `mise` and the generated script needs `npm`, so a stage without a floor is a script that fails on its first line. And the stage could not ship before the home split laid the per-workspace surfaces it writes into — plus one the split does not cover, because the layout links directories and the generated script and the LSP sentinel are home-*root* files, which the stage had to place itself. |

## Current values

The prose above explains what each of these is for; this table is the only place the values
themselves are stated. Every row names where its value is defined, so a row is checkable
against that file rather than against a commit stamp. **Do not transcribe the floor's members
or its size into prose** — the count has already drifted once in this corpus, on the commit
that removed `which` — read them:

```console
$ nix eval --json '.#yoloNoncontainerFloorNames.aarch64-darwin'
```

> [!WARNING]
> **Those diagnostic attrs are not what the drift gate reads.**
> `yoloNoncontainerFloorNames`, `yoloImageCoreNames` and `yoloNoncontainerFloorExclusions` exist
> so the floor is inspectable without building anything, and none of them forces
> `noncontainerFloorPackages` — which is what lets a diagnostic report a hole the fatal refuses
> to build through. But `floor_drift_test.go` parses `flake.nix` **textually** (a `go test`
> cannot evaluate a flake) and what it parses is the three **internal** lists. Renaming an attr
> in that diagnostic block therefore breaks no test; renaming an internal list does.

| Value | Setting | Defined in |
| :--- | :--- | :--- |
| The image core the floor derives from | `coreFloorNames`, in flake order | [`flake.nix`](../../flake.nix); mirrored as `darwinpkg.ImageCoreNames`, pinned entry-for-entry by `floor_drift_test.go` |
| Unbuildable exclusions | `iptables` | `flake.nix` (`noncontainerFloorUnbuildable`); `darwinpkg.FloorExcludedUnbuildable` |
| Policy exclusions | the GNU userland set | `flake.nix` (`noncontainerFloorPolicy`); `darwinpkg.FloorExcludedPolicy` |
| The floor itself | core − both exclusions − the user's `packages:` (the last subtraction in nix only) | `flake.nix` (`noncontainerFloorNames`); `darwinpkg.FloorNames` |
| Declared-packages profile attr | `yoloNoncontainerPackages` | `internal/darwinpkg/darwinpkg.go` (`ProfileAttr`) |
| Floor + declared profile attr | `yoloNoncontainerProfile` — what every macos-user launch builds | `internal/darwinpkg/darwinpkg.go` (`FloorProfileAttr`), `materialize.go` (`Materialize`) |
| Floor profile GC root | `ProfileRootLink(home)`, the build's own `--out-link` | `internal/darwinpkg/gcroot.go` |
| Stage step set | four of six: announce + `mise install --quiet`, announce + the generated script | `internal/macosuser/provision.go` (`ProvisionSetup`); the six in `internal/provision/provision.go` |
| Stage skip rule | `mise_tools` **or** `lsp_servers` non-empty | `internal/macosuser/provision.go` (`ProvisionNeeded`) |
| Provisioning log | `<workspace>/.yolo/startup.log` | `internal/provision/provision.go` (`StartupLog`) |
| Failure marker | `PROVISIONING FAILED` | `internal/provision/provision.go` (`FailedMarker`) |
| Generated bootstrap script | `<workspace>/.yolo/home/yolo-bootstrap.sh` | `internal/entrypoint/darwinstage.go` (`DarwinBootstrapScriptPath`); `macosuser.ProvisionBootstrapScript` |
| LSP sentinel | `<workspace>/.yolo/home/yolo-installed-lsps` here; `$HOME/.yolo-installed-lsps` on the container | `internal/entrypoint/shell.go` (`lspSentinelExpr`) |
| mise data dir | `<account home>/.yolo/mise`, set explicitly as `MISE_DATA_DIR` | `internal/macosuser/macosuser.go` (`SandboxMiseData`, `sandboxEnvPairs`) |
| Session Seatbelt profile | `/var/yolo-jail/profile-<session>.sb`, root-owned `0444` | `internal/macosuser/macosuser.go` (`SessionProfilePath`); installed by `orchestrator.go` |
| Session env file | `/var/yolo-jail/env/<session>.env`, root-owned `0600`, named by `YOLO_DARWIN_ENV_FILE` | `internal/macosuser/envfile.go` (`SandboxEnvFile`, `SandboxEnvFileEnv`) |
| Sandbox PATH, and the login copy | `macosuser.SandboxPath`, carried as `PATH` and as `YOLO_DARWIN_LOGIN_PATH` | `internal/macosuser/macosuser.go`; `internal/entrypoint/darwinhomelayout.go` (`DarwinLoginPathEnv`) |
| LSP install channels | `YOLO_LSP_NPM_INSTALL`, `YOLO_LSP_GO_INSTALL` — both, in **both** environments | `internal/config` (`LSPInstalls`); `internal/macosuser/runplan.go` (`BuildRunPlan`, `PlanInvariants`) |
| Shim bypass | `YOLO_BYPASS_SHIMS=1`, in the stage process's environment | `internal/macosuser/provision.go` (`ProvisionArgv`) |
| Workspace launch lock | `<global storage>/locks/<session>.lock`, held bootstrap-through-stage | `internal/cli/run/flock.go` (`AcquireWorkspaceLockFor`) |
| Prompt gate | `[ -t 0 ]` — ⚠ the `YOLO_PROVISION_PROMPT` clause beside it has **no writer** on any backend | `internal/provision/provision.go` (`Script`) |
| CI schedule and caps | nightly 07:00 UTC on `macos-latest`; `timeout-minutes` and `YOLO_TEST_MACOS_USER_TIMEOUT` are ceilings on waste | [`.github/workflows/macos-user.yml`](../../.github/workflows/macos-user.yml) |
