---
title: "Plan: build named setups — one name chooses every wall"
date: 2026-11-03
status: accepted
stage: DECIDED
next: "The combined 6a-iii + 11b + 6b candidate is built, held for W-SU1/W-SU4 on branch setups-6a3-6b (SUP-D36 to SUP-D49). W-SU1, W-SU4 as revised (SUP-D49) and W-SU12 from the owner's Mac agent, then land the pair serially"
tags: [plan, setups, confinement, macos-user, seatbelt, podman, apple-container]
summary: "The build hand-off for named setups: eleven slices, in order, each with its files, its red test, its gates and its fences. A new stdlib-only package internal/setup holds the model; the launch resolves one setup and derives from it the three values the pipeline already consumes (the render Kind, the backend string, the macos-user Mode); the old keys, flag and env var are refused naming their replacements; homes and run state move under <project>/.yolo/setups/<name>/ keyed by a setup-qualified jail name; host-yolo-account runs the account plan with no Seatbelt. Mac-only checks are witnesses for the owner's Mac agent."
vantage:
  status-chip: true
---

# Plan: build named setups

**Implementation status (2026-11-09):** Main contains slices 4b, 5 and 9, and slice 6a-ii (per-setup homes and capture stores, the legacy-home move and every home reader; [SUP-D32](#SUP-D32) to [SUP-D35](#SUP-D35)). The combined candidate of slice 6a-iii (the setup-qualified jail name, per-setup boot baseline and pack tree), 11b and 6b (`yolo stop`, `ps`, `stores` and `setup list` per setup; [SUP-D46](#SUP-D46) to [SUP-D48](#SUP-D48)) is built, held for W-SU1/W-SU4, on branch `setups-6a3-6b`: it waits on W-SU1/W-SU4 and [W-SU12](#mac-checks). Separate local nested rootful Linux Podman/crun probes establish limited bind-mount `-v` source-resolution and descriptor-holder behavior, including a negative direct guest-FD exposure countercontrol; they are not whole-slice verification. The outside/foreign-pack pathname red no longer gates landing: it is the same check-then-start bind-source swap that [OQ-SU7](named-setups.md#OQ-SU7) accepts as a risk ([SUP-D30](#SUP-D30)). W-SU1 (unchanged) and W-SU4 (revised to follow SUP-D9, [SUP-D49](#SUP-D49)) still gate primary landing; [OQ-SU5](named-setups.md#OQ-SU5) is ruled (silent move once no jail runs, [SUP-D9](#SUP-D9)); [OQ-SU6](named-setups.md#decision-ledger) is ruled (2026-11-06): one machine-wide list, designed in [the design's §12](named-setups.md#12-listing-what-runs-yolo-ps-and-yolo-stores) and planned here as [Slice 11](#slice-11--yolo-ps-and-yolo-stores-list-every-technology-after-4a-independent-of-6a)'s six sub-slices, of which 11a (SU-D10's prune fix) and 11c (every `yolo host` launch holds a session) are built, and 11b (the container launch record) is built on a branch. See the evidence boundary in [Slice 6a](#slice-6a--one-home-and-run-state-per-setup-after-4b-5-and-9-launch-path).

**Plan origin:** 2026-11-03. The planner completed this tree-grounded hand-off against `ce92cf26e`
(the design's [SU-R6](named-setups.md#SU-R6) commit); at that planning snapshot nothing was built.

**Design:** [`named-setups.md`](named-setups.md). It wins on behavior; this file is advice about
the tree and is the first thing to be wrong.

**Precedence.** The design wins on behavior. The tree wins on fact when the drift is cosmetic (a
moved line, a renamed helper with the same role): follow the tree and say so in the report and the
commit. Never twist the code to match this plan. **Stop and report, and do not commit**, when:

- a task's red test does not fail the way the task predicts;
- a file the task names is dirty with someone else's work;
- a symbol the task names is gone;
- the fix needs a file outside the task's fence;
- a gate fails outside the task's scope.

**Roles.** The designer wrote no seed for this plan; the planner read the tree and wrote every
section below. The implementer builds one slice from its task alone and does not read the design.

**Terms.** *Setup*, *confinement*, *run_as*, *the yolo account* and *walls* are the design's
([§1](named-setups.md#1-the-problem), [§2](named-setups.md#2-the-idea)). Two words are the tree's,
and both are replaced by this plan: a **notch** is today's three-value `confinement` dial
(`jail`, `guest`, `host`; `config.Confinement` in `internal/config/confinement.go`), and the
**runtime** or **backend** is today's `podman | container | macos-user` string
(`paths.AllRuntimes`). A **jail name** *(coined here)* is the per-running-thing key the host keeps
state under: today the container name `runtime.FromWorkspace(ws)`, after slice 6a that name with
the setup appended ([SUP-D2](#SUP-D2)).

## Settled — do not re-open

Quoted from the design so a slice never re-derives it:

- *"A setup has two fields, `confinement` and `run_as`"*; values `podman | apple-container |
  seatbelt | landlock | host` and `self | yolo-account`; *"Every field has one default of its
  own"*: `podman` and `self` ([§4](named-setups.md#4-the-fields), [OQ-SU1](named-setups.md#OQ-SU1)).
  *"No code path reads one setup field to decide another's meaning or default."*
- Selection order: `--setup`, then `YOLO_SETUP`, then the user config's `setup`, then the OS
  default: Linux `podman-self`, macOS `apple-container-self` ([§2](named-setups.md#2-the-idea)).
  **The Mac default is not flipped here** ([OQ-SU4](named-setups.md#OQ-SU4)).
- Built-ins are `<confinement>-<run_as>`, one per combination, eight names
  ([§5](named-setups.md#5-the-built-in-setups)); `landlock-*`, `*-yolo-account` on Linux, and
  `podman`/`apple-container` with `yolo-account` refuse naming the nearest built one
  ([OQ-MT7](named-setups.md#OQ-MT7)). `host-yolo-account` is **built on macOS** by
  [SU-R6](named-setups.md#SU-R6), through `yolo host` only.
- *"Setups are user scope only, defined and chosen"*: a `setup` line or a `[setups.*]` table in a
  project file is an error, never ignored ([OQ-MT5](named-setups.md#OQ-MT5)); built-in names cannot
  be redefined; a `host` setup is never selected by config ([§8](named-setups.md#8-the-security-ceiling)).
- The network is not a field; `network.mode` stays the project key, and a written value the
  technology cannot hold is refused: `host` under `apple-container`, `bridge` under `seatbelt` or
  `host` ([SU-R3](named-setups.md#SU-R3), [SU-R5](named-setups.md#SU-R5), [SU-D8](named-setups.md#SU-D8)).
- One home and run state per setup under `<project>/.yolo/setups/<name>/`; approvals, grants,
  `.yolo/durable`, `handover.md` and `launch.log` stay per project; an existing `.yolo/home` is
  adopted on a yes or by a printed `mv`, never deleted ([SU-D6](named-setups.md#SU-D6)).
- Removed and refused naming the replacement, never aliased: `confinement`, `runtime`,
  `YOLO_RUNTIME`, `macos_user_mode`, `--at`, the four `macos-*` verbs
  ([§10](named-setups.md#10-what-is-removed), [MT-R3](named-setups.md#decision-ledger)). The
  refusal texts are [§6](named-setups.md#6-config-by-example)'s, verbatim.
- Seatbelt has one rule list, deny-by-default ([SU-R4](named-setups.md#SU-R4)); deleting the
  allow-by-default `seatbeltProfile` is the macos-user plan's
  [slice 13](macos-user-model-plan.md#slice-13--delete-what-nothing-uses-after-12-macos-ci), not
  this plan's.

## Implementation decisions

Mechanism choices the design leaves to the plan. Each is one answer; the owner may overrule any
of them, and a slice that finds one wrong stops and reports rather than picking another.

| ID | Decision | Why |
| :--- | :--- | :--- |
| <a id="SUP-D1"></a>SUP-D1 | **The model is a new stdlib-only package, `internal/setup`.** It holds the two field types, the `Setup` value, the built-ins, the per-OS default, name validation, selection, buildability and the network check, and the bridges to the tree's three internal values as plain strings and an enum of its own: `Backend()` (`"podman"`, `"container"`, `"macos-user"`, `""` for host), `Kind()` (jail/guest/host as `setup.Kind`, mapped to `render.Kind` by a one-line switch in `internal/render`), and `RunAs` read by `macosuser.ModeFor`. | `internal/config` must import it to validate `[setups.*]`, and `internal/render` imports nothing from config, so the package may import neither (`rg -n 'yolo-jail/internal/' internal/render/*.go` shows `agentcfg`, `jsonx`, `packdecl`, `paths` only). The three derived values are what every call site consumes today; deriving them is not a field deciding another field. |
| <a id="SUP-D2"></a>SUP-D2 | **A setup-qualified jail name.** `runtime.JailName(ws, setup)` = `runtime.FromWorkspace(ws) + "-" + setup`, used for everything that identifies a *running* jail or its per-run state: the container name, `paths.JailShortHash` (host-services dirs, sessions, the host launch's `jailName()`), `keeperKey`, `paths.PackTreeRoot`, `paths.HomeSkeletonRoot`, container tracking files, `macosuser.SessionKey`. `runtime.FromWorkspace` keeps its frozen contract unchanged as the **project key**, used for what stays per project: the approval records (`internal/config/snapshot.go:46`, `scopeapproval.go:291,301`, `loopholegrantgate.go:43`). | Two setups in one project must hold two of each per-run thing, and today every one is keyed by `cname` (`AgentsDir()/<cname>/pack-trees`, `owners/<key>.keeper.json`, `yolo-host-services-<hash8(cname)>-…`). One suffix at one function changes them all; `FromWorkspace`'s golden test (`internal/runtime/naming_test.go:23`) stays green. |
| <a id="SUP-D3"></a>SUP-D3 | **Setup names match `^[a-z0-9]([a-z0-9-]{0,30}[a-z0-9])?$`** (lowercase, hyphens, at most 32), checked where a user setup is read and where `--setup`/`YOLO_SETUP` is parsed. | The name joins a container name (`[a-zA-Z0-9][a-zA-Z0-9_.-]*` on podman and Apple Container) and a keeper key where `.` separates fields (`keeperstate.go:519-524`), and `macosuser.parseSessionKey` requires `[a-z0-9-]+` before the dot (`sessionfiles.go:88-92`). |
| <a id="SUP-D4"></a>SUP-D4 | **`YOLO_SETUP=<name>` is also the session marker.** The launch exports it into the session it starts, replacing `config.NotchEnv` (`YOLO_CONFINEMENT`); in-jail readers (the footer, `yolo config drift`'s baseline path, the macos-user backend's next-step clause) read the setup from it. | One variable is both the user's override and the session's fact, as `YOLO_SETUP=podman-self yolo -- claude` already means "this session is podman-self". The in-jail readers of `config-boot.json` resolve the workspace from `cwd` (`drift.go:38-43`), so they need the setup from the environment to find `<ws>/.yolo/setups/<name>/`. |
| <a id="SUP-D5"></a>SUP-D5 | **`host-yolo-account` runs the macos-user orchestrator's account plan with `Unconfined: true`**, not `hostLaunch`: the sudo crossing, the grant stage, the account preconditions, the bootstrap, the workspace home, every argv exactly as `seatbelt-yolo-account` builds them, minus the `/usr/bin/sandbox-exec -f <profile> --` prefix; `yolo host --setup host-yolo-account` dispatches there. | The owner's ruling: *"reuse the existing account plumbing (the macos-user account launch path) minus the Seatbelt profile."* `hostLaunch` (`internal/cli/host.go:703`) renders config into **your** real home and execs as you, which is the opposite of the account boundary [SU-R6](named-setups.md#SU-R6) buys. |
| <a id="SUP-D6"></a>SUP-D6 | **`YOLO_RUNTIME` refuses on the host and warns in a launched session.** On the host a set `YOLO_RUNTIME` refuses the launch and `yolo check` with the [§10](named-setups.md#10-what-is-removed) text, naming the `YOLO_SETUP` value the old one meant (`podman` → `podman-self`, `container` → `apple-container-self`, `macos-user` → `seatbelt-yolo-account` or `seatbelt-self`). In a launched session (`banner.Side()` says in-jail) it is ignored with one warning line naming `YOLO_SETUP`. The same host-error, in-jail-warning split applies to the removed config keys, which is the tree's retired-key pattern (`validateAgentsRetired`, `internal/config/validate.go:283-296`). | Every jail the shipped launcher started exports `YOLO_RUNTIME=podman` (`assemble.go:1358`), and this project is developed from inside its own jail: a refusal there would stop every nested verification launch until the host launcher is reinstalled. A warning is not silence. |
| <a id="SUP-D7"></a>SUP-D7 | **The `just load` input is renamed `YOLO_LOAD_RUNTIME`**; the integration harness's `detectRuntime` reads `YOLO_SETUP` (decoding a built-in name to its technology) before its platform fallback; the CI workflows set `YOLO_SETUP` where they set `YOLO_RUNTIME` today. | `Justfile:1` reads `YOLO_RUNTIME` for the image copy, and `ci.yml:490`, `packs.yml:226,271` and `nightly-macos.yml:828,1023,1053` set it job-wide, so after [SUP-D6](#SUP-D6) every launch in those jobs would refuse. The load path needs a container runtime, not a setup, so it keeps a variable of its own under a name yolo never reads. |
| <a id="SUP-D8"></a>SUP-D8 | **The setup census keeps four columns, renamed to built-in setups:** `podman-self / Linux`, `podman-self / macOS`, `apple-container-self`, and `seatbelt-*` covering both Seatbelt setups; a cell that differs by `run_as` is an aspect (`setupcensus` already has them). `host-self` and `host-yolo-account` stay `render.hostConfigKeys`'s. | `internal/setupcensus` coined "setup" for a backend on an OS (`census.go:6-9`), and `guide_test.go` ties `userguide/reference/settings-per-setup.md`'s columns to `Setup.String()`; renaming the strings moves the page and the code together. Splitting Seatbelt into two columns would duplicate every cell. |
| <a id="SUP-D9"></a>SUP-D9 | **Silent automatic move, gated on no running jail** ([OQ-SU5](named-setups.md#OQ-SU5), ruled 2026-11-06). A launch that finds an identifiable legacy `.yolo/home` moves it to the setup's home with the existing destination-exclusive rename, only when no jail of the project is running (the same liveness the per-project state already asks; "could not ask" declines and retries at a later launch), only when the source is a real directory (`Lstat`, not a symbolic link) owned by the invoking user, and prints one informational line naming both paths. No prompt. Any other source is left in place with a printed `mv`. | The source-swap race needs a concurrent writer of `.yolo`; with no jail running the only one is a same-user host process with the same reach already. The `Lstat` check closes the symbolic-link substitution for the move. |
| <a id="SUP-D10"></a>SUP-D10 | **A running container under the legacy name refuses the launch.** Before adoption, `probeJailPresence(runtime.FromWorkspace(ws))` (`stop.go:399`) finds a container from before this change; the launch refuses naming `podman stop <name>` (or `container stop`) and that `yolo stop` from the previous yolo also stops it. | The legacy container holds `.yolo/home` bind-mounted; adopting that home under it, or running a second agent beside it on the same bind, is a mess with no next step. One probe is cheap. |
| <a id="SUP-D11"></a>SUP-D11 | **Verbs without `--setup` act on every setup directory under `<ws>/.yolo/setups/`:** `yolo stop` stops each, naming each; `yolo ps` lists each; `yolo config drift` and `yolo describe` with none use the *selected* setup (the same resolution as a launch) and say which. | [§6](named-setups.md#6-config-by-example): *"`yolo stop` stops both, naming each; `yolo stop --setup seatbelt-self` stops one."* Drift and describe answer for one jail, and the selection already names one. |
| <a id="SUP-D12"></a>SUP-D12 | **Inside a launched session, a `YOLO_SETUP` naming a setup this machine cannot run is ignored, with one disclosure line.** When `YOLO_VERSION` is set and no `--setup` is typed, a `YOLO_SETUP` that names no setup this machine defines, or one `setup.Buildable` refuses for this OS, is dropped; the selection goes on to the user config's `setup`, then the OS default, and the line names the value, why it cannot run, and the setup chosen instead. So a nested launch inside an Apple Container jail runs `podman-self`. Outside a session it stays a refusal. | Ruled in the slice 4a review: the inherited value describes the outer session, not a choice made for this machine. The launch exports `YOLO_SETUP` into every session it starts ([SUP-D4](#SUP-D4)), so without this rule every nested launch inside an `apple-container-self` jail would refuse. |
| <a id="SUP-D13"></a>SUP-D13 | **The network refusal judges what was written or typed, never the resolved default, and names the step for where it came from.** One function, `run.NetworkRefusal(setup, cfg, flag, sources)` (`internal/cli/run/networkrefusal.go`), serves the launch (called in `Run` right after `resolveSetup`, rc 1) and `yolo check` (`internal/cli/check/section_network.go`, one Merged Configuration error). A typed `--network` is judged in place of the key, as it wins over the key, and its refusal (`setup.NetworkFlagProblem`) names the flag and "launch without `--network <mode>`", not an `unset`. A written key's refusal leads with the `file:line:col` the config's source record names and ends in `yolo config unset network.mode` with the scope of that file: `--user` for a user-scope file, `--local` for `yolo-jail.local.*`, else `--project` (`setup.NetworkModeProblemIn`). Under the seal nothing is judged, since a build is always bridged. | The plan's literal `setup.NetworkModeProblem(o.Setup, o.resolveNetMode(cfg))` would refuse every Seatbelt launch: `resolveNetMode` answers `bridge` for an unwritten mode, and [SU-R5](named-setups.md#SU-R5) refuses only a written one. `network.mode` is readable in both scopes, so a fixed `--project` would name a step that does not remove a user-scope key. |
| <a id="SUP-D14"></a>SUP-D14 | **A host act's jail runs as the setup a launch selects when that setup runs on the act's backend, else as that backend's built-in.** `actSetup(rt)` (`internal/cli/setupbackend.go`) is the setup a capture jail, a fork's build jail and the host floor's jail boot as: `config.SelectSetup`'s choice when its backend is `rt`, otherwise `podman-self`, `apple-container-self` or `seatbelt-yolo-account`. Who a macos-user act runs as (`actMacosUserMode`) is `macosuser.ModeFor` of that setup's `run_as`, read by `yolo capture`, the host floor's Mac probes, prune's guest pack-tree sweep and `yolo check`'s macOS section. | The launch reads its own setup's `run_as`; an act that read anything else would capture or prune as a different account than the launch it serves. With `macos_user_mode` removed, the selected setup is the only place the account is written. |
| <a id="SUP-D15"></a>SUP-D15 | **`yolo config migrate` converts only the user-scope values that name one setup.** A user file's `runtime = "podman"` becomes `setup = "podman-self"` and `"container"` `setup = "apple-container-self"`; `confinement = "jail"` is dropped, since an unset `setup` selects the same default. `confinement = "guest"` or `"host"`, `runtime = "macos-user"` and every `macos_user_mode` stay unconverted, with the refusal naming the setups and the unset. A project file's value is never converted: it is left with its refusal, which names `yolo config unset <key> --project`. In a merged read, a value is converted only when the user file holds it. | [§10](named-setups.md#10-what-is-removed): *"`migrate` writes the replacement where the old value names exactly one."* A project cannot choose where its agent runs ([§8](named-setups.md#8-the-security-ceiling)), so writing `setup` from a project's key would move a choice the project never had. The legacy corpus records the load and project-scope migrate refusals as owner-ruled ([MT-R3](named-setups.md#decision-ledger)) rather than weakening `TestLegacyConfigsLaunchUnchanged`. |
| <a id="SUP-D16"></a>SUP-D16 | **A removed key selects nothing, and `yolo check` reads the notch from the selected setup.** `confinement`, `runtime` and `macos_user_mode` are an error on the host and a warning in a session; in neither case do they reach `config.SelectSetup`, so a launch, `yolo apply`, `yolo check` and the listing verbs all run the selection without them. `yolo check`'s package-profile notch is the selected setup's `Kind`, and its macOS section's account is the selected setup's `run_as` only when that setup runs on macos-user. | Two readers of the old keys would disagree with the launch they predict. The macOS section had read the default `apple-container-self`'s `run_as` (`self`) as the run-as-you sandbox. |
| <a id="SUP-D17"></a>SUP-D17 | **A TOML file holding `runtime`, `confinement` or `macos_user_mode` is read, refused by its value, edited by the steps the refusal names, and converted by `yolo config migrate` as a JSONC file is.** v0.16.1 wrote these keys to TOML (`yolo config set runtime podman --user`, and its migrate copied them verbatim), so the TOML reader no longer refuses them with one generic line: `validateSetupsRetired` gives the value-aware text a JSONC file gets (`confinement = "host"` is told `yolo host`, never `podman-self`), names `--user`, `--project` or `--local` from the file holding the key, and adds `yolo config migrate` for a user TOML value naming one setup. `yolo config unset <key>` removes the key at any scope, and `yolo config set setup <name> --user` is not blocked by the key it replaces. `yolo config migrate` rewrites a user TOML file in place (as it respells an include) with SUP-D15's conversion, proved like a converted file; a project's value, or a user value naming no one setup, refuses the migration naming its own next step, as a JSONC file's does. | The coordinator's decision under [RC-D50](readable-config.md#RC-D50) (2026-11-04), from the review of slices 4b and 5: a refusal whose step is itself refused is a stop with no next step, which the [happy path principle](../reference/happy-path-principle.md) forbids. The legacy corpus's `realistic/v0161-toml-*` cases hold the launch's refusal for each key at each scope. |
| <a id="SUP-D18"></a>SUP-D18 | **Launch disclosures use compact source labels:** `--setup`, `YOLO_SETUP`, `user config` and `default`; the setup list and other explanatory surfaces keep `Source.String()`'s full wording. Both container and macos-user launches use one `setup.LaunchDisclosure` function. | The first launch line is a one-line operational statement; concise source labels preserve the invocation's meaning without repeating the longer prose used in setup list output. |
| <a id="SUP-D19"></a>SUP-D19 | **Slice 6a lands in two parts.** **6a-i** lands now: `runtime.JailName`, the pure path helpers `paths.SetupsDir`/`SetupStateDir`/`SetupHomeState`, `paths.EnsureSetupStateDir` (creates `.yolo/setups/<name>` through a no-link descriptor chain and returns the open directory), and the [SUP-D9](#SUP-D9) move as a function, `adoptLegacyHome`, with its unit tests. **6a-ii** is everything that changes what a launch binds or names: minting the qualified `cname`, the home, baseline and prism-store callers, deleting `WorkspaceHomeState`, the call site of `adoptLegacyHome`, the [SUP-D10](#SUP-D10) refusal and the integration red. 6a-ii waits on the custody proof and unchanged W-SU1/W-SU4, as before. | Nothing in 6a-i binds a path into a guest or changes a launch, so it never reaches the held-bind boundary. The move cannot be wired before the switch: a launch that moved `.yolo/home` and still bound `.yolo/home` would start on an empty home. Until 6a-ii, `adoptLegacyHome` has no production caller, and its tests pin only the callee. The test that fails when the call site is deleted is the legacy variant of `TestNamedSetupsHaveDistinctHomes`, and it lands with 6a-ii. |
| <a id="SUP-D20"></a>SUP-D20 | **How the move attributes a legacy home and answers each case.** The home is attributed to the **selected** setup when that setup's technology could have laid it: on Linux only `podman` ever did; on macOS `apple-container` and `seatbelt` share the layout since slice 12a. On macOS the Apple Container layout signal (`isAppleContainerInheritedCopy` on a generated user-config copy) attributes the home to Apple Container only. A host setup never adopts one. The caller supplies one tri-state answer about every jail of the project: the legacy name and each setup's `JailName`. The checks run in this order: the source's type, its owner, then attribution (the layout signal reads files beneath the home, so it is asked only about a real directory of the user's), then liveness. Outcomes: (a) no legacy home: nothing, no line; (b) movable, but a jail runs or the runtime could not be asked: left in place, with one line saying that this setup keeps using it ([SUP-D21](#SUP-D21)) and that `yolo stop` and a new launch move it; (c) quiesced, a real directory owned by the invoking user (or by the yolo account for `seatbelt-yolo-account`, [SUP-D31](#SUP-D31)), attributable, and the destination absent or an empty directory: moved with the destination-exclusive rename (`renameat2(RENAME_NOREPLACE)`, `renameatx_np(RENAME_EXCL)` on macOS) after a kernel-checked `rmdir` of an empty destination, with one informational line naming both paths; (d) anything else, whatever runs: left in place, with one line naming the condition and the one step that clears it, after which the next quiesced launch does the move. The steps: a link at `.yolo/home` gets `rm` of the link only; another file type gets `mv -n` aside; another owner gets `sudo chown -R`; a home laid by an Apple Container setup gets "launch an Apple Container setup"; a destination that holds something (a link included) gets `mv -n` of it aside; a linked `setups` gets `rm` of the link; a linked `.yolo` gets "replace the link with the directory it points at". The line prints a manual `mkdir -p … && mv -n …` only where the rename itself is ruled out: `EXDEV` (the home is its own mount) or `EINVAL`/`ENOTSUP` (a filesystem without a no-replace rename, named as such). By [SUP-D21](#SUP-D21) the destination is absent then, so the manual move lands at the setup's home and not inside it. The move never refuses the launch and never deletes anything but an empty directory. | These are implementation answers inside [OQ-SU5](named-setups.md#OQ-SU5)'s ruling. "Attributed to one setup" is read as the setup that launches first. Today both Mac backends read the shared home as their own, so moving it to whichever one runs first keeps today's behavior, and the Apple Container signal keeps an Apple Container home away from Seatbelt (MMP-D32). A raw `mv src dest` was dropped (6a-i review, major 1): it fails while the parent is missing, and it nests the home inside a destination that already exists. A step that clears the condition leaves the rename to yolo, which is never wrong about the destination. |
| <a id="SUP-D21"></a>SUP-D21 | **Until the legacy home has moved, the setup it belongs to keeps using it.** A launch whose selected setup the legacy `<ws>/.yolo/home` is attributed to ([SUP-D20](#SUP-D20)), where the home is still in place (deferred or left), binds `.yolo/home` as its home for that run and creates no `.yolo/setups/<name>/home`. Every other setup uses its own setup home. As a second guard, the quiesced move replaces an empty directory at the destination. **This is a 6a-ii entry condition:** the switch that binds `SetupHomeState` must read `adoptLegacyHome`'s result first. | A deferred move was otherwise stranded for good (6a-i review, major 2): the deferring launch would create an empty setup home, and every later move would then find the destination taken. Using the old home for that run is also today's behavior, so a deferral costs nothing. A recorded fresh-home marker was rejected as a second state to keep true. The kernel's `rmdir` emptiness check needs none. |
| <a id="SUP-D22"></a>SUP-D22 | **A source's answer is `runtime.SourceAnswer{Confinement, State, Names}`, and which container sources exist is `setup.Buildable` of each technology's built-in.** A container source is installed when `setup.Buildable(podman-self or apple-container-self, goos, LookPath)` returns nil, `not-installed` on a `*setup.MissingTechnologyError`, and not a source of this OS on any other error. `Confinement` holds the setup vocabulary (`podman`, `apple-container`, `seatbelt`, `host`). | One answer to "is this technology here" already exists for the launch and `yolo setup list`; a second `LookPath` table in `ps` would disagree with it. |
| <a id="SUP-D23"></a>SUP-D23 | **The container launch record stays at `paths.ContainerDir()/<name>`, as JSON, and the session record keeps `notch` beside the same fields.** A file whose first byte is not `{` is a legacy record: its trimmed text is the workspace and every other field is unknown. `SessionRecord` gains the launch-record fields and keeps `notch`, which `MacosUserSessions` and prune's session reads key on. | `prune.TrackedContainerNames`, `forkbuild`'s artifact check, `forgetGoneContainer` and the orphan reapers key on that path by name; moving it would change every one for no gain. Running jails launched before the change keep one-line records, and owner's "no compatibility" covers output, not the records of jails still running. |
| <a id="SUP-D24"></a>SUP-D24 | **Each source runs under its own `context.WithTimeout`, and `timed-out` is the context's deadline, not a slow exit.** The runner gets the context and kills the child on expiry; a source whose context expired is `timed-out` whatever the child's exit status. The budgets (10 s per source, 5 s per row) are `psDeps` fields so a test can shrink them. | [§12.1](named-setups.md#121-discovery-every-source-in-parallel-each-with-its-own-answer) fixes the budgets; the mechanism is the one Go has, and a dep is the only way a unit test can reach the `timed-out` state without waiting 10 s. |
| <a id="SUP-D25"></a>SUP-D25 | **Podman's start time is `{{.StartedAt}}` in the listing's own Go template; Apple Container's comes only from the launch record until a change reads `startedDate`.** (A Mac measured its encoding 2026-11-06: RFC 3339 UTC, second precision, inside a `status` object; [W-SU11](#mac-checks).) The listing argv becomes `podman ps --filter name=^yolo- --format '{{.Names}}\t{{.Status}}\t{{.StartedAt}}'`; a missing or non-positive value is unknown (`?`), never an error. | `prune.RunningJailStarts` already parses that field as unix seconds and its test pins the argv; `--format json` would add a second parser for one field. Reading an encoding nobody has measured would print a wrong age with confidence. |
| <a id="SUP-D26"></a>SUP-D26 | **Until slice 11d, a single-runtime `yolo ps` on a Mac with both runtimes installed prunes no tracking file.** Slice 11a ships SU-D10's legacy half alone; the other installed runtime was not asked, so it is `unreachable` for the prune. | The alternative, asking the second runtime only for the prune, is 11d's discovery built twice. A record left behind protects only its own name, and the launch's `forgetGoneContainer` still removes every record whose jail ended normally. |
| <a id="SUP-D27"></a>SUP-D27 | **`--setup` is resolved by `setup.Lookup` over the built-ins and the user config's `[setups]`**, the same lookup a launch's `--setup` uses, so the refusal text is the launch's. | [§12.6](named-setups.md#126-narrowing-with---setup) requires the launch's message; one lookup cannot drift from it. |
| <a id="SUP-D28"></a>SUP-D28 | **The host source is `runtime.ListSessions` narrowed to `notch: host`, and the Seatbelt source the rest.** A session with no record (an older yolo's) stays in the Seatbelt source, as `MacosUserSessions` places it today; on Linux the host source is the only session source. | One directory listing serves both; splitting by the record's notch is what `MacosUserSessions` already does. |
| <a id="SUP-D29"></a>SUP-D29 | **A `yolo host` launch that execs its program hands the session lock across the exec, and a session it cannot open is a warning.** `HostSession.KeepAcrossExec` clears close-on-exec on the lock, so the program holds it, and the session record carries the launch's pid, which the exec keeps. A session is live only while the lock is held AND that pid runs (`runtime.ListSessions`, which 11b and 11d read): every process the program spawns inherits the lock, so a held lock alone outlives the agent. Its dir is collected by the next session open after the last holder exits. A failed open prints that `yolo ps` will not list this launch and goes on. | The program replaces yolo, so nothing of yolo's survives to hold the lock or remove the dir; the menu's lock and the session homes' leases already cross the exec the same way (MM-D27). A listing record is never a reason to refuse an agent: `openServicesSession`'s record write is not fatal either. |
| <a id="SUP-D30"></a>SUP-D30 | **No bind-source custody on any backend.** 6a-ii binds per-setup homes by pathname after the existing no-link check, as `.yolo/home` is bound today: no held `/proc/<pid>/fd/<n>` source, no project-wide refusal of side-by-side setups, no mask over `.yolo/setups`. The custody entry condition and the rootless held-descriptor witness are dropped from 6a-ii, and the outside/foreign-pack pathname red, the same swap on a pack's shared-directory bind source, stops gating landing. Supersedes [SUP-D19](#SUP-D19)'s wait on the custody proof. | The owner's ruling on [OQ-SU7](named-setups.md#OQ-SU7) (2026-11-06) makes the swap race an accepted risk under the [misguided-agent principle](../reference/misguided-agent-principle.md); the design's [§9](named-setups.md#9-state-per-setup) records it, and the two mounts that would close it on podman (`.yolo` bound onto itself, an empty read-only bind over `.yolo/setups`) as available, not adopted. |
| <a id="SUP-D31"></a>SUP-D31 | **The legacy-home move also accepts a home owned by the yolo account, when the selected setup is `seatbelt-yolo-account`.** That setup was taken to lay its home as `_yolojail`. Measured on a Mac 2026-11-06 ([run report §4](../lab-reports/2026-11-06-mac-ci-triage-witnesses-run.md#4-who-owns-a-seatbelt-yolo-account-home)), that is true of the home's contents but not of the directory: `.yolo/home` itself is owned by the invoking user (group `_yolojail`, with inherited ACL entries), and nearly every entry inside it is `_yolojail`'s. The move's owner check reads the directory, so such a home already passes under the ordinary rule, and a rename needs write access only to `.yolo`. This acceptance stays, as harmless defense for a home whose directory the account does own: such a home is moved silently under [SUP-D20](#SUP-D20) case (c) like one the invoking user owns. Every other setup, and any other owner, still takes case (d): left in place, with the one step. | The owner's ruling (2026-11-06), choosing option A: *"yes, A"*. Option B, moving only user-owned homes, would print a manual `mv` on every `seatbelt-yolo-account` launch, against [OQ-SU5](named-setups.md#OQ-SU5)'s *"we shouldn't be bothering users about this"*. |
| <a id="SUP-D32"></a>SUP-D32 | **6a-ii lands as the home switch; the rest of the per-setup run state is 6a-iii.** 6a-ii binds `.yolo/setups/<name>/home` (or the legacy home under [SUP-D21](#SUP-D21)) on every backend, calls `adoptLegacyHome` from the launch, and points every reader of a home at the one the jail bound ([SUP-D34](#SUP-D34)). The setup-qualified container name (`runtime.JailName`), the per-setup boot baseline (`config-boot.json`), staged pack tree and assembled records, and the [SUP-D10](#SUP-D10) refusal are **6a-iii**; the prism store moved forward into 6a-ii ([SUP-D35](#SUP-D35)). Until then every setup of a project is keyed under `runtime.FromWorkspace`, so the running-jail check the move reads (`Options.projectJails`) probes that one name: its keeper and its macos-user and guest keeper keys, a live session record under any of them (kept or not), and a container of that name in each container runtime on `PATH`. "Could not ask" is unknown, and the move waits. | The home is the state that holds the user's data, and the move is what [SUP-D19](#SUP-D19) said cannot be wired before the switch. The qualified name changes every container, keeper and lock key, so it is a launch-path change of its own and keeps its own landing gate. |
| <a id="SUP-D33"></a>SUP-D33 | **The pack-files mountpoint record lives beside each home and moves with the legacy home.** `pack-files-mountpoints.json` sits in the home's parent: `.yolo/` for the legacy home, `.yolo/setups/<name>/` for a setup's. When `adoptLegacyHome` renames the home, it also renames the record, beneath the same two directory descriptors and never replacing a file; a record that stays behind only leaves those mountpoints unowned. | Without the record, a setup's launches could not retire the mountpoint scaffolding that the legacy launches left in the moved home. |
| <a id="SUP-D34"></a>SUP-D34 | **Every reader of a home reads the one the jail bound, and none guesses.** The rules, one per reader: <br>• **Launch.** `Options.homeDir()`, fixed by `selectHome` after every gate and before any home is read. A link anywhere on the `.yolo/setups/<name>/home` chain refuses the launch (`linkedWorkspaceState`). <br>• **Keeper.** Its plan and record carry `Home`, so a teardown capture and `yolo stop`'s reap of an unkept jail read the bound home. <br>• **In-jail capture.** `capture-run --surface-root-of=<workspace>` resolves the home the launch told the session it bound (`YOLO_HOME_STATE`, [SUP-D35](#SUP-D35)); a session from an older launch falls back to its `YOLO_SETUP` home, else the legacy home. <br>• **MCP-auth share.** It carries `Home`. <br>• **Claude credential view.** It carries `Home`, and its registration is keyed per home. <br>• **Sweeps.** Prune dedup, the agent-log purge and build-jail log keeping iterate `paths.WorkspaceHomeStates`. <br>• **`yolo config`.** Its jail target reads `run.HomeFor` host-side and `YOLO_HOME_STATE` in the jail, and its store is the one beside that home ([SUP-D35](#SUP-D35)). <br>• **macos-user.** The arm receives the home as the overlay's `Sidecar`. `macos-user-profile --home` defaults to the mode's built-in setup's home. The run-as-you bootstrap admits any workspace home, provided `HOME` and the sidecar name the same one. | Two setups of one project each hold a home. A reader that assumed `.yolo/home`, or re-derived the setup itself, would read another setup's files or none. Carrying the bound path from the launch is the one answer that cannot disagree with the bind. |
| <a id="SUP-D35"></a>SUP-D35 | **The capture store (the prism sidecars) lives beside each home, and moves with the legacy home.** It is `.yolo/prism` for the legacy home and `.yolo/setups/<name>/prism` for a setup's (`paths.PrismStateRel`). The launch tells the session the home it bound, relative to `.yolo`, as `YOLO_HOME_STATE` on both the container argv and the macos-user launch environment, and the boot's render target keys its store to it (`render.Target.WithHomeState`). Host readers take the same store from the home: the teardown capture from the home the jail bound, `yolo config` from `run.HomeFor`, the launch record from `Options.homeDir`. `adoptLegacyHome` renames `prism` beside the home it moves, beneath the same two descriptors and never replacing, as [SUP-D33](#SUP-D33)'s record. A session that names no valid home keeps `.yolo/prism`. This pulls the prism-store piece of [SUP-D32](#SUP-D32) forward into 6a-ii. | A baseline belongs to the home it was rendered into. With one store and a home per setup, setup A's boot diffed A's file against setup B's render, read A's stale value as an edit back, and lost B's edit (6a-ii review, M2, reproduced at `ComposeStateful`). The per-home store is the one answer that cannot pair a home with another home's render, and it is what [§9](named-setups.md#9-state-per-setup) lists as per setup. |
| <a id="SUP-D36"></a>SUP-D36 | **The legacy home's decision is made under the project lock, and the project lock rides on the launch lock.** The project lock is the launch lock file of the legacy jail name (`runtime.FromWorkspace`): every launch of the project that might move or bind the legacy home takes it, whatever its setup, and an older yolo took it as its own launch lock. It is released with the launch lock (`workspaceLock.ride`), and a launch lock handed to a keeper leaves it held until the launch lets it go (`releaseHomeLock`). The order is always this lock first, then the launch lock. | Once the launch lock is keyed by the setup-qualified name, two setups no longer share one, so it cannot keep a second setup from moving the home between the first's decision and its bind (6a-ii review m2). Riding on the launch lock keeps every existing release point. Pinned by `TestTheProjectLockGoesWithTheLaunchLock`. |
| <a id="SUP-D37"></a>SUP-D37 | **A launch's records are its setup's.** `config-boot.json`, `config-assembled.json` and `config-assembled-origins.json` live in `.yolo/setups/<setup>/` (`internal/config/setupstate.go`). Readers take a setup name. `""` means this session's setup (`YOLO_SETUP`), and on the host, where none is set, the read is the named error `config.ErrNoSessionSetup`. The host-side `yolo config drift` passes the setup a launch here selects. The legacy `.yolo/config-boot.json` is read only to tell a workspace (`BootBaselineMarked`), and `yolo config migrate` respells each setup's baseline. | Two setups of one project run side by side, each with its own effective config, so one baseline cannot describe both. The named error stops a reader from silently reading another setup's record. |
| <a id="SUP-D38"></a>SUP-D38 | **A fork build's staging record names its jail.** `writeForkBuildRuntime` writes the jail name as a second line, beside the runtime, and every later reader takes the name from the record (`stagingJailName`). A record with the runtime alone, as an older yolo wrote it, names the project key. A build that has just run reads what its jail recorded, and falls back to the name it predicted (`actJailName`) only when the jail recorded none (`stagingJailNameOr`). | The act's setup may change between runs, so a retry cannot re-derive the retained jail's name. Pinned by `TestAForkBuildsRecordNamesItsJail`. |
| <a id="SUP-D39"></a>SUP-D39 | **What is the project's keeps the project key.** The approval, scope and grant records (`config.ApprovalRecordFiles`) and the host perf log's `jail=` label stay keyed by `runtime.FromWorkspace`. The container, keeper keys, launch lock, host-services hash, staged pack tree, tracking file and tool disk take the jail name. | An approval is the user's answer for the project, and re-asking it per setup would be a regression. Everything that identifies one running jail must be per setup, or two setups collide. |
| <a id="SUP-D40"></a>SUP-D40 | **`yolo stop` stops the selected setup's jail and a legacy jail.** It stops the jail a launch from here would key by now (`workspaceJailName`). It then stops a jail under the legacy name when anything of that jail is left (`run.JailKnown`), and says so in one line. Stopping every setup's jail is 6b. | The [SUP-D10](#SUP-D10) refusal names `yolo stop` as the remedy, so the stop must reach that jail. Pinned by `TestAStopFromTheFrontDoorAlsoStopsTheLegacyJail`. |
| <a id="SUP-D41"></a>SUP-D41 | **A host-side config write treats a legacy jail as running.** `refuseWhileJailRuns` refuses when the selected setup's jail or the legacy-named jail is live, and names whichever runs. | A legacy jail binds the home this setup's launch would use until a quiesced launch moves it, so its surfaces are live. Pinned by `TestHostSideResetRefusesWhileTheLegacyJailRuns`. |
| <a id="SUP-D42"></a>SUP-D42 | **The reapers parse setup-qualified names.** The orphan sweep's name pattern accepts an optional setup suffix, and its workspace-gone check and the tool-disk attribution use `runtime.IsJailOfResolved`, which matches the project key with or without a setup. The miseuse record carries the jail's `Setup`, so a usage row names its container. "Could not ask" stays unknown and is never reaped. | Without this, a setup jail's per-name files and its tool disk would read as nobody's and leak, or a still-referenced one would be reaped. |
| <a id="SUP-D43"></a>SUP-D43 | **The [SUP-D10](#SUP-D10) refusal asks `jailRuns`, and an unanswered probe does not refuse.** It asks the three ways the legacy-home move asks: the keeper of the legacy name and of its macos-user and guest keys, a live session record under any of them, and a container of the name in each container runtime on `PATH`. A running answer refuses before the home's move, and names `yolo stop` and the runtime's own stop. An unknown answer launches. The move that follows then reads unknown as running and leaves the home in place. The move's own probe (`projectJails`) asks the legacy name, the selected setup's name, and the name of every setup with a directory under `.yolo/setups`. | A launch is not refused on a question nobody answered, and the home stays safe either way. Pinned by `TestALegacyJailRunningRefusesTheLaunchBeforeAnyMove` and `TestTheMoveAsksEverySetupsJail`. |
| <a id="SUP-D44"></a>SUP-D44 | **The host-daemon spawn guard (HD-D4) stays the project's, and a keeper keeps the jail its plan names.** The keeper's spawn guard is keyed by `runtime.FromWorkspace`, as `yolo host` keys it, because the settings file it guards is not per setup. A keeper's `Options` answers `jailName` with the plan's `Cname` (`keeperJailName`) before any selection. | A keeper carries its setup's name only and cannot resolve a custom setup again, so a re-derived name would make its teardown leave its own jail's grant and agent-env files. Pinned by `TestTheJailsTeardownRemovesItsGrantAndAgentEnvFiles` (a custom setup's jail) and `TestTheMacosUserArmRefusesWhenItCannotTakeTheSpawnGuard` (the project's guard path). |
| <a id="SUP-D45"></a>SUP-D45 | **With setup-qualified names, the workspace-state-safety pieces key by the jail, and `--fresh-home` asks only the jail it would join.** `yolo --fresh-home` ([SD-D14](workspace-state-safety.md#SD-D14)) takes its own jail's launch lock and is refused while that jail runs or cannot be asked (`jailRuns` of its setup-qualified name). Another setup's jail does not refuse it, since a launch never joins another setup's jail. A jail under the legacy name refuses it as it refuses every launch ([SUP-D10](#SUP-D10)), before any fresh home is made. `yolo check`'s moved-`.yolo` finding ([SD-D5](workspace-state-safety.md#SD-D5)) reads every keeper start record whose jail is the workspace's, legacy or setup-qualified (`workspaceKeeperKeys`, `runtime.IsJailOf`), not `.yolo/setups`, since the `.yolo` it checks may be the one that moved. The workspace README names `setups/<setup>/config-boot.json` and `config-assembled*.json`, where [SUP-D37](#SUP-D37) puts them. | Implementation decision of the 6a-iii rebase onto the workspace-state-safety work (2026-11-09). On main the fresh-home check asked `projectJails`, which under 6a-ii was the one jail every setup shared. Keeping it would refuse `--fresh-home` beside a jail it can never join; and taking the legacy name's launch lock would hold that lock as this launch's own, so its real launch lock would never be taken. Pinned by `TestFreshHomeIsNotRefusedByAnotherSetupsJail` and `TestCheckFindsASetupJailsMovedState`. |
| <a id="SUP-D46"></a>SUP-D46 | **`yolo stop` stops every setup's jail, each on its own technology, and names each one it ended.** It stops the selected setup's jail first, always, on the runtime the selection resolves. It then stops each other setup with state under `.yolo/setups`, by its setup-qualified name and its setup's technology (`setup.Backend`), when anything of that jail is left (`run.JailKnown`), and finally the legacy jail ([SUP-D40](#SUP-D40)). Each jail it ended prints `stopped <setup>`. `--setup <name>` stops that one setup's jail alone and refuses an undefined name with the launch's lookup message (exit 2). A setup no longer defined is stopped on the runtime its keeper recorded; with none recorded, the stop fails naming `yolo setup new <name> --from <setup>`. A host setup runs no jail, so the stop says that a `yolo host` session ends when its program exits. The other-runtime hint (`noteOtherRuntimes`) is deleted. | Implementation decision of slice 6b (2026-11-09). With one jail name per setup, each setup's technology is known from its name, so the hint guessing at another runtime has nothing left to guess. Asking only setups with something left keeps a plain stop quiet about setups that never ran here. Pinned by `TestAStopStopsEverySetupsJailNamingEach`, `TestAStopLeavesASetupWithNothingLeftAlone`, `TestAStopWithSetupStopsThatSetupsJailAlone` and `TestAStopWithSetupRefusesWhatNamesNoJail`. |
| <a id="SUP-D47"></a>SUP-D47 | **Until slice 11d, `yolo setup list`'s running column asks each jail directly.** For each setup with state under this workspace's `.yolo/setups`, it asks `run.JailRunState`, which is the launch's own `jailRuns` (keepers, live sessions, and a container of the name in each runtime on PATH). The line says `running` when the jail runs and `may be running` when a source could not answer. A setup with no state here is not asked, and nothing is asked inside a jail, whose keepers and sessions are the host's. `JailRunState` is one exported reader added to `internal/cli/run`, which slice 6b fences; nothing in `run` changes behavior. | Implementation decision of slice 6b (2026-11-09). The plan reads slice 11's discovery for this column ([§12.6](named-setups.md#126-narrowing-with---setup)), and 11d is not built. `jailRuns` is the probe the legacy-home move and `--fresh-home` already trust, so `setup list` and those refusals agree. When 11d lands, the column moves to its discovery. Pinned by `TestSetupListMarksTheSetupsRunningHere` and `TestJailRunStateAsksTheJailsKeepers`. |
| <a id="SUP-D48"></a>SUP-D48 | **`yolo ps` gains a SETUP column, and `yolo stores`' Seatbelt rows say their copies are per jail.** The table reads `CONTAINER  SETUP  STATUS  WORKSPACE`. Each row's setup is its launch record's `setup`; else the setup its name carries (`runtime.JailName` of its workspace); else `?` in the table and `null` in the JSON `setup` key, for a jail an older yolo started ([§12.2](named-setups.md#122-what-a-row-knows-and-where-it-reads-it)). The macos-user staged-dir rows (home overlay, `/ctx`, `env`, the Seatbelt profiles) name each copy as a jail's, one per workspace and setup, and say that `<cname>` is that jail's name, as `yolo ps` lists it. The overlay and `/ctx` rows count `jails`. | Implementation decision of slice 6b (2026-11-09). The plan's 6b test asks for a SETUP column; §12.3's full table (`WORKSPACE SETUP STATUS AGE PACKS`) is 11d's and replaces this layout then. Deriving the setup from the name answers records written before 11b, which name no setup. Pinned by `TestPsListsARowPerSetupWithItsSetup`, `TestRenderPsTable` and `TestTheMacosUserSectionListsTheStateDirAndTheHomesStores`. |
| <a id="SUP-D49"></a>SUP-D49 | **W-SU4 is revised to follow [SUP-D9](#SUP-D9).** It no longer expects a `[y/N]` prompt: with no jail of the project running, the move happens unasked and prints one line naming both paths; with a jail running, or with `.yolo/home` a symbolic link, nothing moves and one line names the next step. | Owner's rule (2026-11-09): the plan follows the design, and where they disagree the plan is wrong. The design ruled [OQ-SU5](named-setups.md#OQ-SU5) as SUP-D9's silent move, so W-SU4's prompt contradicted it. |

## Map

One row per file that changes. New files are marked. `…` means "and the sibling tests".

| Path | Change |
| :--- | :--- |
| `internal/setup/setup.go`, `builtins.go`, `select.go`, `network.go` … | **new** — the model ([slice 1](#slice-1--the-model-internalsetup-independent)) |
| `internal/render/target.go` (~`:250-257` `KindForNotch`) | gains `KindForSetup(setup.Kind) Kind`; `kindNames` output labels become setup words ([slice 1](#slice-1--the-model-internalsetup-independent), [9](#slice-9--the-launch-line-the-footer-and-the-briefing-name-the-setup-after-4a)) |
| `internal/config/setups.go` … | **new** — `SelectedSetupFromConfig`, `UserSetups`, `validateSetups` ([slice 2](#slice-2--the-config-keys-setup-and-setups-after-1)) |
| `internal/config/config.go:79-100`, `workspacescope.go:9`, `inherit.go` census, `schema.go:463`, `readablevalidate.go:49-52` | register `setup`, `setups`, `setups.*`, `setups.*.confinement`, `setups.*.run_as` ([slice 2](#slice-2--the-config-keys-setup-and-setups-after-1)) |
| `internal/changetiming/changetiming.go:87-106`, `internal/setupcensus/configkeys.go:21-36`, `internal/render/configkeys.go:133-151` | rows for the new keys ([slice 2](#slice-2--the-config-keys-setup-and-setups-after-1)); rows for the removed keys deleted ([slice 5](#slice-5--refuse-the-removed-spellings-and-delete-the-notch-code-after-4a)) |
| `internal/cli/config_ref.txt:201-236`, `:1600-1630`, `:3233`, `:3288` | `setup`/`setups` sections ([slice 2](#slice-2--the-config-keys-setup-and-setups-after-1)); the removed keys' sections become refusals ([slice 5](#slice-5--refuse-the-removed-spellings-and-delete-the-notch-code-after-4a)) |
| `internal/cli/userconfig.jsonc:11-14` | the `runtime` comment becomes a `setup` example ([slice 5](#slice-5--refuse-the-removed-spellings-and-delete-the-notch-code-after-4a)) |
| `internal/cli/setupcmd.go` … | **new** — `yolo setup list \| show \| new` ([slice 3](#slice-3--yolo-setup-list--show--new-after-2-and-7)) |
| `internal/cli/dispatch.go:27-58`, `help.go:29,76,97-109`, `subhelp.go:82,132-135`, `commands.go:35-145` | verbs: `setup` added ([slice 3](#slice-3--yolo-setup-list--show--new-after-2-and-7)); `yolo-account` added and `macos-*` refused ([slice 7](#slice-7--yolo-yolo-account-create--remove--share--unshare-independent)) |
| `internal/cli/runcmd.go:149-150`, `:347-352`; `internal/cli/valueflags.go:41-46` | `--setup` replaces `--at` in `runFlags` and `launchValueFlags` ([slice 4a](#slice-4a--the-launch-resolves-a-setup-after-3-launch-path)) |
| `internal/cli/run/runcmd.go:83-89`, `:544-548` | `Options.Setup setup.Setup` replaces `Notch`/`atNotch`/`MacosUserMode` ([slice 4a](#slice-4a--the-launch-resolves-a-setup-after-3-launch-path)) |
| `internal/cli/run/run.go:234-252`, `:514-525`, `:748-749`, `:1118`, `:3672-3798` | the notch gate and runtime resolution become setup-derived; `YOLO_SETUP` exported ([slice 4a](#slice-4a--the-launch-resolves-a-setup-after-3-launch-path)) |
| `internal/cli/run/preflight.go:185-200`, `probes.go:104`, `keeperspawn.go:910-915`, `keeperplan.go:46-49,122` | backend from the setup; keeper notch from the setup ([slice 4a](#slice-4a--the-launch-resolves-a-setup-after-3-launch-path)) |
| `internal/cli/dispatch.go:147-203` (`hostNotchArgv`), `host.go:315-355,383-437`, `apply.go:72-115,227`, `config.go:183-230`, `configtarget.go:190-215`, `packbuild.go:59-75` | `--at` → `--setup`; `--at` refused ([slice 4a](#slice-4a--the-launch-resolves-a-setup-after-3-launch-path)) |
| `internal/cli/commands.go:1140-1149`, `:1235-1258`, `:1382-1390`, `:1469-1497`; `describe.go:87-92,279-286`; `hostfloor.go:836-841`; `devbundlereap.go:44`; `internal/prune/prunecmd.go:267`; `internal/cli/check/probes.go:19-105,190,365-372`, `check.go:328,363`, `nextsteps.go:303`, `section_packageprofile.go:44`, `sections_macos.go:90` | every reader of the notch, the runtime key or `macos_user_mode` reads the selected setup ([slice 4a](#slice-4a--the-launch-resolves-a-setup-after-3-launch-path)) |
| `internal/cli/run/assemble.go:1358`, `inherit.go:343-346` (`runtime` row), `missingbuilds.go:227-228` | `-e YOLO_RUNTIME=podman` dropped; remedy hatches name `YOLO_SETUP` ([slice 4a](#slice-4a--the-launch-resolves-a-setup-after-3-launch-path)) |
| `integration/harness_test.go:395-410,538`, `macosusergate_test.go:659-673`, `applecontainer_test.go:166-167`, `applecontainerkeeper_test.go:315`, `macarchivedelivery_test.go:199`, `treeremovable_test.go:297`, `keeperreadywindow_regression_test.go:68`, `imageskew_parse_test.go:117,149,161`, `archivedeliveryworkflow_test.go:148`; `Justfile:1-2,196-216`; `.github/workflows/ci.yml:490`, `packs.yml:226,271`, `nightly-macos.yml:828,1023,1053`, `apple-container.yml:333-337` | `YOLO_RUNTIME` → `YOLO_SETUP` / `YOLO_LOAD_RUNTIME` ([SUP-D7](#SUP-D7), [slice 4a](#slice-4a--the-launch-resolves-a-setup-after-3-launch-path)) |
| `internal/cli/run/assemble.go:636-662`, `backendcaps.go:200-215`, `internal/cli/check` (a new `section_network` check) | `network.mode` refused where the technology cannot hold it ([slice 4b](#slice-4b--refuse-a-networkmode-the-technology-cannot-hold-after-4a)) |
| `internal/config/confinement.go`, `confinementruntime_test.go`, `macosusermode.go` …, `validate.go:95-96,218-230`, `renames.go:33-39`, `config.go:114-117`; `internal/configmigrate/lower.go:179-200` | the removed keys refused and migrated; the notch code deleted ([slice 5](#slice-5--refuse-the-removed-spellings-and-delete-the-notch-code-after-4a)) |
| `internal/paths/paths.go:659-759`, `statefile.go:27-115`, `workspacescope.go` | `SetupStateDir`, `SetupHomeState`, `SetupsDir`; delete `WorkspaceHomeState`; retain `EnsureWorkspaceStateDir` as the `.yolo` creation chokepoint and the existing no-follow root helpers for jail-writable state ([slice 6a](#slice-6a--one-home-and-run-state-per-setup-after-4b-5-and-9-launch-path)) |
| Home readers/writers in `internal/cli/run`, `internal/cli`, `internal/macosuser`, `internal/claudeview`, `internal/prune`; config baseline/capture paths in `internal/config`, `internal/configmigrate`, `internal/render`, `internal/entrypoint` (the complete map and their consumers are in [slice 6a](#slice-6a--one-home-and-run-state-per-setup-after-4b-5-and-9-launch-path)) | per-setup home, baseline and prism store; preserve capture routing and no-follow reads ([slice 6a](#slice-6a--one-home-and-run-state-per-setup-after-4b-5-and-9-launch-path)) |
| `internal/runtime/naming.go`; running-jail identity call sites in `internal/cli/run`, `internal/cli`, `internal/hostrun`, `internal/macosuser` and `internal/prune` (enumerated in [slice 6a](#slice-6a--one-home-and-run-state-per-setup-after-4b-5-and-9-launch-path)) | setup-qualified jail identity; keep per-project approval/grant keys on `FromWorkspace` ([SUP-D2](#SUP-D2), [slice 6a](#slice-6a--one-home-and-run-state-per-setup-after-4b-5-and-9-launch-path)) |
| `internal/cli/run/inheritscope.go:742-808`, a new `internal/cli/run/legacyhome.go` | adoption and the legacy-container refusal ([SUP-D9](#SUP-D9), [SUP-D10](#SUP-D10), [slice 6a](#slice-6a--one-home-and-run-state-per-setup-after-4b-5-and-9-launch-path)) |
| `internal/cli/stop.go:82-196,323-420,601`, `setupcmd.go` (running column) | enumerate setups ([slice 6b](#slice-6b--stop-ps-stores-and-setup-list-see-every-setup-after-6a)) |
| `internal/cli/ps.go`, `internal/runtime/tracking.go`, `macosusersessions.go`, `display.go`; `internal/cli/stores/inventory.go`; `internal/cli/run/servicessession.go`, `loopholesruntime.go` | one machine-wide list ([slice 11](#slice-11--yolo-ps-and-yolo-stores-list-every-technology-after-4a-independent-of-6a), sketch) |
| `internal/macosuser/runplan.go` (`RunPlan`, `BuildRunPlanWithStages:604`, `PlanInvariants:1613`, argv builders), `provision.go:148-169`, `serviceprobe.go:46-64,119`, `jaildaemon.go:487`, `orchestrator.go:949,1325`; `internal/cli/host.go:214-250,606`; `commands.go` (the host-yolo-account dispatch) | `host-yolo-account` ([SUP-D5](#SUP-D5), [slice 8](#slice-8--host-yolo-account-on-macos-after-6a-mac-witness)) |
| `internal/cli/run/banner.go:23-33`, `run.go:2524,3026,3549`; `internal/footer/footer.go:78-87,173-185`; `internal/jailcontent/briefing.go:128-133,280,308-320,390-455,526-531`; `internal/cli/run/prepare.go:182`, `mechanicsfacts.go:31`, `sharednetwork.go:20`; `internal/cli/check/section_packageprofile.go`; `internal/macosuser/orchestrator.go:888-900`; all pack footer adapter templates (`packs/claude`, `agy`, `copilot`, `omp`, `opencode`, `pi`) | the setup named everywhere the notch was ([slice 9](#slice-9--the-launch-line-the-footer-and-the-briefing-name-the-setup-after-4a)) |
| `internal/cli/commands.go:112-145`, `dispatch.go:55-58`, `help.go:97-109`, `subhelp.go:132-135`; `internal/macosuser/commands.go`, `preconditions.go`, `privilege.go:158-165`, `macosuser.go`, `capture.go`, `ctxlinks.go`, `sessionfiles.go`, `mode.go:17`; `internal/cli/hostfloor.go:341`, `packbuild.go:353`, `hosttrees.go`; `internal/cli/run/run.go:269`, `macosuserrelocations.go`, `launchlog.go`, `autocapture.go`; `internal/entrypoint/programcompatibility.go:145`, `boot.go`; `integration/macosusergate_test.go:607,753-755`, `macosuserrelocations_test.go:131`, `macosusernorootsudo_test.go:357-366`, `hostfloor_test.go:427`; `.github/workflows/macos-user.yml:312,652` | the `yolo-account` verbs ([slice 7](#slice-7--yolo-yolo-account-create--remove--share--unshare-independent)) |
| `internal/setupcensus/census.go:56-80` | column names ([SUP-D8](#SUP-D8), [slice 10](#slice-10--docs-after-5-and-6a)) |
| `userguide/guides/confinement.md`, `userguide/reference/settings-per-setup.md`, `userguide/guides/macos.md`, `userguide/reference/cli-reference.md:35,193-201`, `userguide/getting-started.md:247-254,291-303,616`, `userguide/guides/storage.md:15-17,59-64`, `userguide/README.md:83`, `README.md:24,46-47,118`; `docs/reference/what-yolo-is.md:109-132`, `loophole-grants.md:383-395`, `storage-and-config.md:366,507`, `jail-home.md:182,194,558,1683`, `agent-footer.md:69,129-139,565`, `config-target-resolution.md:116,166,201-221,632`, `report-tiers.md:575`; `AGENTS.md` (the `macos_user_mode` line and the backend table) | docs ([slice 10](#slice-10--docs-after-5-and-6a)) |

## Reuse before you write

- **The retired-key pattern**, to copy for `confinement`, `runtime`, `macos_user_mode`:
  `validateAgentsRetired` (`internal/config/validate.go:283-296`: error on the host, warning in a
  jail), the key kept in `knownTopLevelConfigKeys` (`config.go:79-100`) and added to
  `retiredTopLevelConfigKeys` (`config.go:114-117`), a `RenameDescriptor` with `TOMLRefusal`
  (`renames.go:33-39`, consumed by `readableUnknownMessage` at `readablevalidate.go:243-251` and
  `configmigrate.unconvertedReason` at `lower.go:257-265`), and `config_ref.txt`'s `REMOVED`
  body (`TestConfigRefAnnouncesTheRefusalForEveryRetiredKey`, `internal/cli/configrefcoverage_test.go:115`).
- **A migrated key**: `packages` → `install.nix` (`renames.go:40`, `lower.go:179-184`
  `RenameMove`). `setup` is a `RenameValueRewrite` of `confinement`/`runtime`/`macos_user_mode`
  with a value table, user scope only; a project-file `confinement`/`runtime` is `RenameDrop`
  naming `yolo config unset … --project`.
- **A user-scope-only key with a workspace refusal**: `validateMacosUserMode`
  (`internal/config/macosusermode.go:54-73`) and `MacosUserMode()` reading
  `UserScopeConfigOrEmpty()` (`hostwrappers.go:95-102`). `validateSetups` mirrors it; the readable
  path already refuses a user-only key in a workspace file at `readablevalidate.go:49-52` with a
  generic sentence, so the design's sentence goes in a per-key advice table consulted there.
- **A value-taking launch flag**: `readValueFlag(args, i, "--at")` (`internal/cli/runcmd.go:347`),
  `launchValueFlags` (`valueflags.go:41-46`), `TestRunUsageListsEveryRunFlag`
  (`runcmd_test.go:219`) and `TestTheLaunchHasNoQuietFlag` (`:682`). `--setup` is `--at`'s
  shape with a new name.
- **The host verb's flag grammar**: `parseHostExecFlags` (`host.go:383`) already takes `--at host`
  and refuses another notch (`refuseHostNotchContradiction`, `:340`); `--setup host-self` and
  `--setup host-yolo-account` are the two values it accepts, every other setup refused with the
  [§6](named-setups.md#6-config-by-example) `host` sentence turned around.
- **The one crossing**: `RunAs.Argv` (`internal/macosuser/mode.go:66`) and `RunAsSandboxArgv`
  (`privilege.go:63`); `TestEveryCrossingGoesThroughRunAs` fails a file that builds sudo itself.
  Slice 8 adds no crossing.
- **Deriving the Mode**: `macosuser.ParseMode` (`mode.go:36`) becomes `ModeFor(setup.RunAs)`;
  `LaunchHomes(mode, ws, hostHome)` (`:76`) is unchanged but takes the setup's home.
- **Per-run keys**: `keeperKey(cname, notch)` (`internal/cli/run/keeperstate.go:519`),
  `paths.JailShortHash` (`paths.go:439`), `macosuser.SessionKey` (`sessionfiles.go:86`),
  `paths.PackTreeRoot` (`packtree.go:21`), `paths.HomeSkeletonRoot` (`homeskeleton.go:25`),
  `hostrun.jailName` (`internal/hostrun/host.go:127`): all take `cname`; slice 6a changes what
  `cname` is at its minting sites, not these functions.
- **The Apple Container home predicate**: `isAppleContainerInheritedCopy` and
  `refuseAppleContainerHome` (`internal/cli/run/inheritscope.go:760-808`) are the layout
  identification [SUP-D9](#SUP-D9) reuses; the refusal itself goes (a setup's home is its own).
- **Fixtures**: `testconfig.User(t, text)` / `Workspace(t, text)` (`internal/testconfig/fixture.go:20-39`);
  `userConfigWith(t, home, body)` (`internal/config/programs_test.go:33`); the config package's
  `TestMain` pins `HOME` to an empty temp dir (`testmain_test.go:34-44`). Integration:
  `isolateHome`/`packHome(t, userConfig)` (`integration/packs_test.go:335`) for user scope,
  `writeProject(t, json)` (`harness_test.go:1079`) for the workspace, `withEnv("KEY=V")` (`:789`).
- **A per-OS table of names already shipped**: `setupcensus.Setups()` and `String()`
  (`census.go:71-80`) are the shape `setup.BuiltIns()` mirrors for ordering and labels.
- **Rich refusal text**: `richtext.Printer` and `o.pr(o.Stderr).printf("[bold red]…")`
  (`run.go:3678`); every refusal in this plan is one `printf` with the design's words.

## House style that isn't obvious

- A refusal's last line names the next step (`docs/reference/happy-path-principle.md`): every
  message in [§6](named-setups.md#6-config-by-example) already does; copy them whole.
- Config is read from `*jsonx.OrderedMap` by key; there is no struct. A new key needs rows in
  **six** censuses or `just test-fast` fails naming the missing one: `knownTopLevelConfigKeys`,
  `WorkspaceUserScopeOnlyKey`, `inheritCensus`, `changetiming.schemaRows`,
  `setupcensus.configKeys`, `render.hostConfigKeys`, plus a `config_ref.txt` section at two-space
  indent (`TestConfigRefDocumentsEveryLiveKey`).
- A removed spelling keeps its key in `knownTopLevelConfigKeys` so the targeted message is the
  only error (the comment above `retiredTopLevelConfigKeys`).
- `shquote.Join` for anything that crosses into a `bash -c`.
- Commit messages are conventional (`feat(setups): …`), no attribution trailers.
- Tests that assert the old behavior are **rewritten to the new, not repaired until green**
  (each slice lists them).

## Traps

- **Two vocabularies, one word.** `internal/setupcensus` already calls a backend-on-an-OS a
  "setup" and `settings-per-setup.md` is its page; `docs/design/named-setups.md` uses the word for
  the new noun. [SUP-D8](#SUP-D8) reconciles them in slice 10; until then do not rename the census
  type.
- **The outer jail exports `YOLO_RUNTIME=podman`** (`assemble.go:1358`, through the shipped
  launcher). A nested verification launch from inside this jail sees it; so does the integration
  suite's `os.Environ()`. [SUP-D6](#SUP-D6)'s in-jail warning keeps both running; before slice 4a
  lands, run nested launches with `env -u YOLO_RUNTIME` to see the host behavior.
- **`FromWorkspace` is frozen** (`naming.go:1-5`, golden at `naming_test.go:23`). Do not change
  it; add `JailName` beside it ([SUP-D2](#SUP-D2)). Approval records must keep using
  `FromWorkspace`: changing them makes approvals per setup, which [SU-D6](named-setups.md#SU-D6)
  forbids.
- **`keeperKey` appends `.notch`** for macos-user (`keeperstate.go:519-524`) and `StopMacosUser`
  iterates both notch spellings (`keeperspawn.go:1753-1768`). Slice 4a keeps the key shape and
  feeds it `setup.Kind()`; slice 6a changes the `cname` half only.
- **`appliedNetMode` answers `"host"` for macos-user silently** (`backendcaps.go:207-215`) and
  Apple Container only *warns* on `host` (`assemble.go:654-662`). Slice 4b turns both into
  refusals; the `setupcensus` cells at `configkeys.go:350-363` assert the warning line and will
  fail: rewrite them to `refused(...)`.
- **The notch gate runs before the runtime probe** (`run.go:234-238`) and `refuseUnbuiltNotch`
  exits with rc 2 for an unknown `--at`. Keep that order and that code for an unknown `--setup`.
- **`hostNotchArgv` reroutes `yolo --at host` to the `host` verb** (`dispatch.go:147-203`). The
  same reroute for `--setup host-self` is what keeps `yolo --setup host-self -- cmd` meaning
  `yolo host`, as [§8](named-setups.md#8-the-security-ceiling) item 3 requires; dropping it makes
  the launcher refuse what the design allows.
- **`config.WorkspaceConfigBootPath("")` uses `cwd`** (`drift.go:38-43`) because in-jail readers
  have no workspace argument; the setup reaches them only through `YOLO_SETUP`
  ([SUP-D4](#SUP-D4)). Slice 4a must export it before slice 6a moves the file.
- **`PlanInvariants` asserts `sandbox-exec -f <profile>` on every argv** (`runplan.go:1933,
  2030, 2195, 2253, 2471`). Slice 8's unconfined plan needs the inverse assertions, not a
  relaxation: an unconfined plan with a profile on any argv is the defect.
- **macOS-only code is vetted with `GOOS=darwin go vet ./internal/macosuser/...`**; the Linux
  gate does not compile it (project memory: darwin-only breaks reach CI first).
- **`git add` before any nested image build**: untracked new files vanish from the image.
- **`just check-ci` is whole-tree**: land from a clean worktree, one slice per worktree under
  `$YOLO_DURABLE_DIR/worktrees/<slice>`.

## Tasks

Build order is a property of each task's heading. **Parallel groups**: **A** = slices 1 and 7;
**B** = 2; **C** = 3; **D** = 4a; **E** = 4b, 5 and 9; **F** = 6a; **G** = 6b, 8 and 10. Tasks in
one group touch disjoint files. For 6a→6b, use separate commits but one materialized combined candidate: 6b needs the 6a commit and the exact W-SU1 witness exercises 6b. No primary landing of either commit before W-SU1/W-SU4. Slices **4a, 4b, 6a, 8 and 9 are launch-path changes**: whoever
lands one runs the full `go test -count=1 -timeout 0 ./integration` once on the combined tree
(the `TestMacosUser*` and `TestAppleContainer*` suites run on macOS CI, and their Mac witnesses
are below).

### Slice 1 — the model, `internal/setup` (independent)

- **Settled:** the fields, values, defaults, built-ins and selection order under
  [Settled](#settled--do-not-re-open). *"A built-in marked not built is still a name: selecting
  it refuses, naming the nearest built one."*
- **Preconditions:** `ls internal/setup` does not exist; `go test -short ./internal/render/...` green.
- **Tests first** (`internal/setup/setup_test.go`), each a table case, red because the package
  does not exist:

  ```go
  func TestBuiltInNamesDecodeToTheirFields(t *testing.T) {
  	for _, s := range setup.BuiltIns() {
  		want := string(s.Confinement) + "-" + string(s.RunAs)
  		if s.Name != want {
  			t.Fatalf("built-in %q is not <confinement>-<run_as> (%q)", s.Name, want)
  		}
  	}
  	if n := len(setup.BuiltIns()); n != 8 {
  		t.Fatalf("eight built-ins, got %d", n)
  	}
  }

  func TestAnOmittedFieldTakesItsOwnDefault(t *testing.T) {
  	s, err := setup.Decode("mac-as-me", map[string]string{"confinement": "seatbelt"})
  	if err != nil || s.RunAs != setup.RunAsSelf {
  		t.Fatalf("run_as default is self regardless of confinement; got %+v, %v", s, err)
  	}
  	s, err = setup.Decode("lab", map[string]string{"run_as": "yolo-account"})
  	if err != nil || s.Confinement != setup.ConfinementPodman {
  		t.Fatalf("confinement default is podman regardless of run_as; got %+v, %v", s, err)
  	}
  }

  func TestDefaultPerOS(t *testing.T) {
  	if setup.Default("linux").Name != "podman-self" || setup.Default("darwin").Name != "apple-container-self" {
  		t.Fatal("defaults are podman-self on Linux and apple-container-self on macOS")
  	}
  }

  func TestSelectionOrder(t *testing.T) {
  	user := map[string]setup.Setup{"mac-as-me": {Name: "mac-as-me", Confinement: setup.ConfinementSeatbelt, RunAs: setup.RunAsSelf}}
  	got, src, err := setup.Select("", "", "mac-as-me", user, "darwin")
  	if err != nil || got.Name != "mac-as-me" || src != setup.FromUserConfig {
  		t.Fatalf("user config: %+v %v %v", got, src, err)
  	}
  	got, src, _ = setup.Select("", "podman-self", "mac-as-me", user, "darwin")
  	if got.Name != "podman-self" || src != setup.FromEnv {
  		t.Fatalf("env outranks config: %+v %v", got, src)
  	}
  	got, src, _ = setup.Select("seatbelt-self", "podman-self", "mac-as-me", user, "darwin")
  	if got.Name != "seatbelt-self" || src != setup.FromFlag {
  		t.Fatalf("flag outranks env: %+v %v", got, src)
  	}
  	got, src, _ = setup.Select("", "", "", nil, "linux")
  	if got.Name != "podman-self" || src != setup.FromDefault {
  		t.Fatalf("default: %+v %v", got, src)
  	}
  	if _, _, err := setup.Select("fast", "", "", user, "linux"); err == nil || !strings.Contains(err.Error(), `setup "fast" is not defined`) {
  		t.Fatalf("undefined name: %v", err)
  	}
  }

  func TestNotBuiltRefusesNamingTheNearest(t *testing.T) {
  	cases := map[string]string{ // name on linux -> required substring
  		"seatbelt-self":         "uses Seatbelt, which only macOS has",
  		"landlock-self":         "is not built yet. The nearest built setup on Linux:\n    yolo --setup podman-self",
  		"host-yolo-account":     "not built",
  		"apple-container-self":  "Apple Container",
  	}
  	for name, want := range cases {
  		err := setup.Buildable(setup.MustBuiltIn(name), "linux", func(string) bool { return true })
  		if err == nil || !strings.Contains(err.Error(), want) {
  			t.Errorf("%s on linux: %v", name, err)
  		}
  	}
  	if err := setup.Buildable(setup.MustBuiltIn("host-yolo-account"), "darwin", nil); err != nil {
  		t.Fatalf("host-yolo-account is built on macOS (SU-R6): %v", err)
  	}
  }

  func TestNetworkModeTheTechnologyCannotHold(t *testing.T) {
  	if p := setup.NetworkModeProblem(setup.MustBuiltIn("apple-container-self"), "host"); !strings.Contains(p, "always runs bridged") {
  		t.Fatal(p)
  	}
  	if p := setup.NetworkModeProblem(setup.MustBuiltIn("seatbelt-self"), "bridge"); !strings.Contains(p, "always shares the Mac's network") {
  		t.Fatal(p)
  	}
  	for _, mode := range []string{"", "bridge", "host"} {
  		if p := setup.NetworkModeProblem(setup.MustBuiltIn("podman-self"), mode); p != "" {
  			t.Fatal(p)
  		}
  	}
  }

  func TestNameGrammar(t *testing.T) {
  	for _, bad := range []string{"Fast", "a.b", "-x", "x-", strings.Repeat("a", 33), ""} {
  		if setup.ValidName(bad) {
  			t.Errorf("%q accepted", bad)
  		}
  	}
  }
  ```

  Red: `go test -short ./internal/setup/` (no such package).
- **Change:**
  - `internal/setup/setup.go` (new): `type Confinement string`, `type RunAs string`, the seven
    value constants (`landlock` included, `host` included), `type Setup struct{Name string;
    Confinement Confinement; RunAs RunAs; BuiltIn bool}`, `Decode(name string, fields
    map[string]string) (Setup, error)` (unknown field → the `policy` sentence of
    [§6](named-setups.md#6-config-by-example) generalized; unknown value → the values list),
    `ValidName`, `Describe(Setup) string` (*"Seatbelt, deny-by-default, as you; shares the Mac's
    network"*: one phrase per confinement, one per run_as, the network phrase from the
    technology), `Kind()`, `Backend()`.
  - `builtins.go` (new): `BuiltIns()` in the [§5](named-setups.md#5-the-built-in-setups) table's
    order, `MustBuiltIn`, `IsBuiltIn(name)`, `Default(goos)`, `Buildable(s, goos, hasBinary)`
    returning the [§6](named-setups.md#6-config-by-example) refusals (Linux `seatbelt`/`apple-container`, every `landlock`, Linux
    `yolo-account`, `podman`/`apple-container` with `yolo-account` naming `podman-self`, the
    missing technology naming its install and `yolo setup list`).
  - `select.go` (new): `type Source int` (`FromFlag`, `FromEnv`, `FromUserConfig`,
    `FromDefault`), `Select(flag, env, userSetup string, user map[string]Setup, goos string)
    (Setup, Source, error)`; the error for an unknown name lists the defined names.
  - `network.go` (new): `NetworkModeProblem(s, mode string) string` with the two [§6](named-setups.md#6-config-by-example) sentences,
    each ending in the `yolo --setup podman-self -- claude` line and the `unset` line.
  - `internal/render/target.go`: `KindForSetup(k setup.Kind) Kind` beside `KindForNotch` (~`:250`);
    nothing else in render changes here.
- **Gates:** `go test -short ./internal/setup/ ./internal/render/`; `just format`.
- **Fences:** nothing under `internal/config`, `internal/cli`; `KindForNotch` stays (slice 5 deletes it).
- **Commit:** `feat(setup): the named-setup model — fields, built-ins, selection, buildability`
- **Report:** the red run, the green run, and the exact refusal strings as implemented (slice 2's
  tests quote them).

### Slice 2 — the config keys `setup` and `setups` (after 1)

- **Settled:** *"Setups are user scope only, defined and chosen"*; a project-file `setup` or
  `[setups.*]` is an error naming `--setup`, `yolo config set setup <name> --user` and `yolo
  config unset setup --project`; built-ins cannot be redefined; a `host` setup in `setup` is
  refused naming `yolo host`. Do not re-open.
- **Preconditions:** slice 1 landed (`go doc ./internal/setup Select` prints); `just test-fast` green.
- **Tests first** (`internal/config/setups_test.go`; red because nothing reads the keys):

  ```go
  func TestSelectedSetupReadsUserScopeOnly(t *testing.T) {
  	home := setupsHome(t) // mirrors programsHome: HOME at a temp dir
  	userConfigWith(t, home, `setup = "seatbelt-self"`+"\n"+"[setups.mac-as-me]\nconfinement = \"seatbelt\"\n")
  	name, user, set := config.SelectedSetupFromConfig()
  	if !set || name != "seatbelt-self" || user["mac-as-me"].RunAs != setup.RunAsSelf {
  		t.Fatalf("got %q %+v %v", name, user, set)
  	}
  }

  func TestAProjectFileCannotChooseOrDefineASetup(t *testing.T) {
  	ws := t.TempDir()
  	write(t, filepath.Join(ws, "yolo-jail.toml"), "setup = \"podman-self\"\n\n[setups.fast]\nconfinement = \"podman\"\n")
  	_, errs, _ := validateScopedIn(t, ws, "", "")
  	wantAll(t, errs,
  		"yolo-jail.toml:1: setup: a project cannot choose where its agent runs; that is yours to choose.",
  		"yolo config set setup podman-self --user",
  		"yolo config unset setup --project",
  		"setups.fast: a project cannot define a setup; setups are yours.",
  		"yolo setup new fast --from")
  }

  func TestABuiltInCannotBeRedefined(t *testing.T) {
  	home := setupsHome(t)
  	userConfigWith(t, home, "[setups.podman-self]\nconfinement = \"podman\"\n")
  	_, errs, _ := validateScopedIn(t, t.TempDir(), "", "")
  	wantAll(t, errs, "[setups.podman-self] redefines a built-in setup", "yolo setup new my-podman --from podman-self")
  }

  func TestAHostSetupIsNeverChosenByConfig(t *testing.T) {
  	home := setupsHome(t)
  	userConfigWith(t, home, `setup = "host-self"`+"\n")
  	_, errs, _ := validateScopedIn(t, t.TempDir(), "", "")
  	wantAll(t, errs, `setup "host-self" puts no walls around the agent, and only `+"`yolo host`"+` runs one`)
  }

  func TestAnUnknownSetupFieldIsRefused(t *testing.T) {
  	home := setupsHome(t)
  	userConfigWith(t, home, "[setups.lab]\nconfinement = \"seatbelt\"\npolicy = \"allow-default\"\n")
  	_, errs, _ := validateScopedIn(t, t.TempDir(), "", "")
  	wantAll(t, errs, "setups.lab: `policy` is not a setup field", "yolo config unset setups.lab.policy --user")
  }
  ```

  Red: `go test -short -run 'Setup' ./internal/config/` (undefined `config.SelectedSetupFromConfig`).
  `validateScopedIn` is `loopholeplacement_test.go:18`'s helper; `wantAll` is a five-line local
  helper asserting every substring appears in the joined errors.
- **Change:**
  - `internal/config/setups.go` (new): `SelectedSetupFromConfig() (name string, user
    map[string]setup.Setup, set bool)` reading `UserScopeConfigOrEmpty()` (mirror
    `macosusermode.go:21-38`); `UserSetups(cfg)` decoding every `[setups.<name>]` through
    `setup.Decode`; `validateSetups(config, workspace, errs, edit...)` with the four refusals above,
    wired beside `validateMacosUserMode` in `validate.go`.
  - `config.go:79-100`: add `"setup", "setups"`; `workspacescope.go:9`: add both;
    `workspacescopefacts.go:28-29` likewise; `inherit.go` census: both not inherited (*"chooses
    where the agent runs on the host; a nested jail is one setup's inside"*); `schema.go:463`:
    `setup` a string, `setups.*` an object with `confinement` and `run_as` strings (follow
    `mcp_servers.*` at `:392`).
  - `readablevalidate.go:49-52`: a per-key advice map so the user-only refusal for `setup` and
    `setups` carries the design's sentence with the `file:line:` prefix the walk already adds
    (`:284`).
  - `changetiming.go:87-106`, `setupcensus/configkeys.go:21`, `render/configkeys.go:133`: rows
    for `setup` and `setups` (`AtNextRestart`; honored everywhere; at the host notch `setup` is
    `KeyHonored` — *"`yolo host` runs `host-self` or `host-yolo-account`"* — and `setups`
    `KeyHonored`).
  - `internal/cli/config_ref.txt`: a `setup` section and a `setups` section after `:236`, in the
    file's voice, with the [§6](named-setups.md#6-config-by-example) examples; the TOML FORMAT
    section names `yolo setup new` (`TestConfigRefTOMLSectionNamesTheVerbsTheCLIHas` lists verbs
    at `configreftoml_test.go:79-86`; add `yolo setup` there).
  - `yolo config set setup <name> --user` must write the key: `configeditverbs.go:44-99` is
    generic over the schema, so a test that runs it against a temp user config and reads back
    `setup = "seatbelt-self"` is the proof (`TestConfigSetWritesTheSetupKeyAtUserScope` in
    `internal/cli`).
- **Gates:** `go test -short ./internal/config/ ./internal/cli/ ./internal/changetiming/
  ./internal/setupcensus/ ./internal/render/`; then `just test-fast`.
- **Fences:** no reader of the new keys outside `internal/config` (slices 3 and 4a add them); the
  removed keys are untouched (slice 5).
- **Commit:** `feat(config): setup and [setups.*] keys, user scope only, built-ins unredefinable`
- **Report:** the red run, the green gates, and which of the six censuses needed a row.

### Slice 3 — `yolo setup list | show | new` (after 2 and 7)

- **Settled:** `yolo setup list` prints every built-in and user setup, which the selection picks
  and why; `show <name>` prints the table as [§6](named-setups.md#6-config-by-example) spells it;
  `new <name> --from <setup>` writes a `[setups.<name>]` table into the user config with the source
  setup's values. The *running* column is slice 6b's.
- **Preconditions:** slices 2 and 7 landed; `yolo setup` is not a registered verb
  (`rg -n '"setup"' internal/cli/dispatch.go` prints nothing).
- **Tests first** (`internal/cli/setupcmd_test.go`):

  ```go
  func TestSetupShowPrintsTheBuiltInAsATable(t *testing.T) {
  	var out bytes.Buffer
  	if rc := setupMain([]string{"show", "podman-self"}, &out, io.Discard, false); rc != 0 {
  		t.Fatal(rc)
  	}
  	want := "[setups.podman-self]\nconfinement = \"podman\"\nrun_as = \"self\"\n"
  	if out.String() != want {
  		t.Fatalf("got %q", out.String())
  	}
  }

  func TestSetupListMarksTheSelectionAndItsSource(t *testing.T) {
  	home := setupsHome(t)
  	userConfigWith(t, home, `setup = "podman-self"`+"\n[setups.mac-as-me]\nconfinement = \"seatbelt\"\n")
  	var out bytes.Buffer
  	setupMain([]string{"list"}, &out, io.Discard, false)
  	for _, want := range []string{"podman-self", "built-in, your default", "mac-as-me", "yours", "Seatbelt, as you"} {
  		if !strings.Contains(out.String(), want) {
  			t.Errorf("missing %q in\n%s", want, out.String())
  		}
  	}
  }

  func TestSetupNewCopiesTheSourceIntoTheUserConfig(t *testing.T) {
  	home := setupsHome(t)
  	userConfigWith(t, home, "")
  	if rc := setupMain([]string{"new", "lab", "--from", "seatbelt-yolo-account"}, io.Discard, io.Discard, false); rc != 0 {
  		t.Fatal(rc)
  	}
  	_, user, _ := config.SelectedSetupFromConfig()
  	if s := user["lab"]; s.Confinement != setup.ConfinementSeatbelt || s.RunAs != setup.RunAsYoloAccount {
  		t.Fatalf("%+v", s)
  	}
  	if rc := setupMain([]string{"new", "podman-self", "--from", "podman-self"}, io.Discard, io.Discard, false); rc == 0 {
  		t.Fatal("a built-in name is refused")
  	}
  }
  ```

  Red: `go test -short -run 'TestSetup' ./internal/cli/` (undefined `setupMain`).
- **Change:** `internal/cli/setupcmd.go` (new) with `runSetup`/`setupMain`; register `"setup"` in
  `dispatch.go:27-58`, `help.go` (`TestEveryRegisteredCommandIsListedInHelp`, `help_test.go:115`),
  `subhelp.go:82` (`TestEveryRegisteredCommandAnswersHelp`). `new` writes through the same path
  `yolo config set` uses (`runConfigEditWithMode`, `configeditverbs.go:44`) so the user-config
  edit rules (in-jail refusal `configEditNeedsHostForJailedUserScope`, `:311`) hold.
- **Gates:** `go test -short ./internal/cli/`; `just test-fast`.
- **Fences:** `runFlags`, `--setup`, `YOLO_SETUP` (slice 4a); no running column (slice 6b).
- **Commit:** `feat(cli): yolo setup list, show and new`
- **Report:** the red and green runs; the `list` output verbatim.

### Slice 4a — the launch resolves a setup (after 3; launch-path)

- **Settled:** the selection order; *"`host` confinement is never selected by config"*;
  `yolo --setup host-self -- cmd` is `yolo host -- cmd`; `--at` and `YOLO_RUNTIME` are refused
  naming `--setup` and `YOLO_SETUP` ([§10](named-setups.md#10-what-is-removed)); a default whose
  technology is missing refuses naming the install and the built-ins available
  ([SU-D2](named-setups.md#SU-D2)). The Mac default stays `apple-container-self`. In this slice the
  home is still `<ws>/.yolo/home` (slice 6a moves it). Do not re-open.
- **Preconditions:** slice 3 landed; `go test -short ./internal/cli/... ./internal/config/...` green;
  `rg -n '"--at"' internal/cli/runcmd.go` prints `:149` and `:347`.
- **Tests first:**
  - `internal/cli/runcmd_test.go`: `TestSetupFlagAndEnvReachTheLaunch` — `parseRunArgs([]string{"--setup",
    "seatbelt-self", "--", "claude"}, &opts)` leaves `opts.SetupName == "seatbelt-self"`;
    `parseRunArgs([]string{"--at", "guest", "--", "claude"}, &opts)` is refused and the stderr
    contains "`--at` is removed. Use `--setup <name>`" and `yolo host` for `--at host`.
    `TestRunUsageListsEveryRunFlag` (`:219`) passes with `--setup` in `runUsage`.
  - `internal/cli/run/notchgate_test.go` → **rewrite** as `setupgate_test.go`: a Linux launch
    with `Setup = seatbelt-self` refuses with the [§6](named-setups.md#6-config-by-example) Seatbelt sentence and rc 2; with
    `landlock-self` names `podman-self`; with `host-self` names `yolo host`; `YOLO_RUNTIME=podman`
    set on the host refuses naming `YOLO_SETUP=podman-self`; the same in a session
    (`YOLO_VERSION` set) warns and launches. `TestNotchRuntimeIsTheMacosGuestsBackendAlone`,
    `TestSelectedRuntimePrecedence`, `TestNotchRuntimeConflict` (`confinementruntime_test.go`)
    are deleted in slice 5, not here.
  - `internal/cli/frontdoornotch_test.go:108-125` → rewrite: `yolo --setup host-self -- cmd` routes
    to the host verb; `yolo host --setup seatbelt-self` refuses naming both spellings.
  - `internal/cli/run/run.go` session env: `TestTheSessionIsToldItsSetup` — the launch env holds
    `YOLO_SETUP=<name>` and no `YOLO_CONFINEMENT` (pin with the existing launch-env test shape near
    `run.go:743-749`'s caller tests).
  - `internal/cli/check`: `guestnotch_test.go` → rewrite as `setup_test.go`: `yolo check` under
    `YOLO_SETUP=seatbelt-self` on Linux reports the refusal; under `YOLO_RUNTIME=podman` on the host
    reports the removal.
  - Integration (run by whoever lands): `TestMacosUserSeatbeltSelfLaunchNamesTheSetupInItsBriefingAndFooter`
    (`integration/macosusersetupfooter_test.go`) verifies the selected setup reaches the native launch,
    its briefing, and footer; `macosUserRunEnv()` (`macosusergate_test.go:664`)
    → `YOLO_SETUP=seatbelt-yolo-account`; `appleContainerEnv()` (`applecontainer_test.go:166`) →
    `YOLO_SETUP=apple-container-self`; `keeperEnv` and `macarchivedelivery_test.go:199` map
    `rt` to the built-in name; `detectRuntime` (`harness_test.go:395-410`) decodes `YOLO_SETUP`.
    Red first: `go test -short ./integration` compiles but these launch tests fail on the dev
    bundle until the launcher reads `YOLO_SETUP`.
- **Change** (the shape, not the body):
  - `internal/cli/run/runcmd.go`: `Options.SetupName string` (typed) and `Options.Setup
    setup.Setup` (resolved); delete `Notch`, `atNotch`, `MacosUserMode`, `MacosUserSharedTmp`
    stays. `runFlags:149`: `--setup` replaces `--at`; parse at `:347`; `valueflags.go:41-46`.
  - `internal/cli/run/run.go:234-252`: `refuseUnbuiltNotch` → `resolveSetup(o, cfg)`: `setup.Select(o.SetupName,
    o.Getenv("YOLO_SETUP"), configName, userSetups, goos)`, then `setup.Buildable(...)`, then
    the removed-input checks ([SUP-D6](#SUP-D6)); `o.resolveRuntime(cfg)` (`preflight.go:185-200`)
    becomes `validateExplicitRuntime(o.Setup.Backend(), "setup "+o.Setup.Name)` (the PATH and
    connectivity gates at `:265-286` stay; the probe loop at `:195-200` goes). `:748-749`:
    `launchEnv.Set("YOLO_SETUP", o.Setup.Name)`; `unknownlaunchenv.go:55` lists it. `:1118`
    `macosuser.LaunchHomes(macosuser.ModeFor(o.Setup.RunAs), …)`. Delete `launchNotch`,
    `notchConfig`, `containerStepClause` (`:3762-3798`); `BriefingInput.Confinement` and
    `FactSheet.Confinement` carry the setup name (slice 9 renders it).
  - `keeperspawn.go:910-915`: notch from `o.Setup.Kind()`; `keeperplan.go:46-49`: `Runtime` from
    `Backend()`, `Setup string` added, `MacosUserMode` from `ModeFor`.
  - `internal/cli/dispatch.go:173-203`: `hostNotchArgv` reads `opts.SetupName`, reroutes
    `host-self` and `host-yolo-account`, strips `--setup`; `host.go:315-355,383-437`: accept
    `--setup host-self|host-yolo-account` (`host-yolo-account` refuses "not built yet" until
    slice 8), refuse others; `apply.go:72-115,227`: `--setup`; `config.go:183-230`
    `extractAtFlag` → `extractSetupFlag`, `configtarget.go:190-215` resolves through
    `render.KindForSetup`; `packbuild.go:59-75` likewise. `--at` anywhere: one refusal string
    in one place (`internal/cli/atremoved.go`, new) that every parser calls.
  - `internal/cli/commands.go:1140-1149` `detectListingRuntime` → `selectedSetup(workspace).Backend()`;
    `:1382-1390` `macosUserLaunchMode` → `macosuser.ModeFor`; `:1235-1258,1469-1497` pass the
    setup. `describe.go:87-92,279-286`: `--setup` flag, prints `setup <name> (<Describe>) - from
    <source>`. `hostfloor.go:836-841`, `devbundlereap.go:44`, `prunecmd.go:267`: the backend from
    the selected setup. `internal/cli/check/probes.go:19-105,190,365-372`, `check.go:328,363`,
    `nextsteps.go:303`, `section_packageprofile.go:44`, `sections_macos.go:90`: the same.
  - `internal/macosuser/mode.go:36`: `ModeFor(setup.RunAs) Mode` beside `ParseMode` (slice 5
    deletes `ParseMode`).
  - `assemble.go:1358`: drop `-e YOLO_RUNTIME=podman`; `inherit.go:343-346`: the `runtime` row's
    reason stays until slice 5. `missingbuilds.go:227-228`: hatches `{Env: "YOLO_SETUP", Value:
    "apple-container-self"}` and `podman-self`. Every `YOLO_RUNTIME=…` remedy string listed under
    *internal/cli/run messages only* in the map (`assemble.go:662,889`, `backendcaps.go:91`,
    `forkbuild.go:269-270`, `hostports.go:100`, `inheritscope.go:780`, `patchedtrees.go:431`,
    `stop.go:601`, `ps.go:223`, `stores/inventory.go:619,939,1153,1190`, `prunecmd.go:1337,1600`,
    `loopholes/guestrun.go:50-51`, `entrypoint/programcompatibility.go:145`,
    `macosuser/commands.go:212`) says `YOLO_SETUP=<built-in>` instead.
  - `Justfile:1-2`: `runtime := env("YOLO_LOAD_RUNTIME", "podman")`; the four workflows set
    `YOLO_SETUP: podman-self` where they set `YOLO_RUNTIME: podman`, and `apple-container.yml:333-337`
    sets `YOLO_LOAD_RUNTIME: container` for `just load` ([SUP-D7](#SUP-D7)). `harness_test.go:538`
    and `imageskew_parse_test.go:117,149,161` say `YOLO_LOAD_RUNTIME=%s just load`.
- **Gates:** `go test -short ./internal/cli/... ./internal/config/... ./internal/macosuser/...`;
  `GOOS=darwin go vet ./internal/cli/... ./internal/macosuser/...`; `just check-ci`; nested
  verification: `just build-dev-bundle`, then from a throwaway workspace
  `env -u YOLO_RUNTIME <bundle>/yolo --setup podman-self -- true`, `… --setup seatbelt-self -- true`
  (the Linux refusal), `YOLO_RUNTIME=podman … -- true` (the host refusal, since `-u` cleared the
  session marker only if you also unset `YOLO_VERSION`; otherwise expect the warning). Landing:
  the full integration suite.
- **Fences:** `internal/paths`, `internal/runtime/naming.go` (slice 6a); the network refusal
  (4b); `internal/config/confinement.go` and the removed keys' validators (5); the footer,
  briefing and banner words (9); `internal/macosuser/runplan.go`'s profile and argv (8).
- **Commit:** `feat(setups): a launch resolves one named setup; --at and YOLO_RUNTIME refused`
- **Report:** the red runs, the green gates, the nested-run output for the three commands, and
  every `YOLO_RUNTIME` remedy string you changed (the list above is from `rg`; name any it missed).

### Slice 4b — refuse a `network.mode` the technology cannot hold (after 4a)

- **Settled:** [SU-R5](named-setups.md#SU-R5): `host` under `apple-container` and `bridge` under
  `seatbelt` or `host` are refused, in `yolo check` and at launch, with the two
  [§6](named-setups.md#6-config-by-example) sentences; an unwritten value is the technology's
  network. Do not re-open.
- **Preconditions:** slice 4a landed; `rg -n 'is NOT honored on Apple Container' internal/cli/run/assemble.go`
  prints `:659`.
- **Tests first:** `internal/cli/run/networkrefusal_test.go` — `Options{Setup:
  apple-container-self}` with a workspace `network.mode = "host"` refuses before assembly with
  the [§6](named-setups.md#6-config-by-example) text and `yolo config unset network.mode --project`; `seatbelt-self` with `bridge`
  likewise; `podman-self` with either passes; `--network host` (the flag) under
  `apple-container-self` refuses naming the flag. `internal/cli/check/network_test.go` — the same
  config under the same setups produces one error line in `yolo check`. **Rewrite:**
  `setupcensus/configkeys.go:350-363`'s `warned(...)` and `dropped(...)` cells for `network.mode`
  become `refused(...)`; `TestNoUserGuidePageMakesAClaimTheCensusContradicts` (`guide_test.go:477`)
  then fails on `settings-per-setup.md:351+` until slice 10 rewrites that row — land 4b with the
  one-line guide edit for that row. Red: `go test -short -run 'Network' ./internal/cli/run/ ./internal/cli/check/`.
- **Change:** one call of `setup.NetworkModeProblem(o.Setup, o.resolveNetMode(cfg))` in
  `run.go` right after `resolveSetup`, refusing with rc 1; the same in a new
  `internal/cli/check/section_network.go`; delete the Apple Container warning branch
  (`assemble.go:654-662`) and leave `appliedNetMode` (`backendcaps.go:207-215`) as the applied
  fact for an unwritten value.
- **Gates:** `go test -short ./internal/cli/... ./internal/setupcensus/`; `just check-ci`. Landing
  runs the integration suite (the Apple Container suite's
  `TestAppleContainerExplicitHostModeKeepsPublishedPorts`, `applecontainerparity_test.go:485`,
  asserts the *warning*: rewrite it to the refusal; it runs on macOS CI).
- **Fences:** nothing under `internal/config`; the briefing's network paragraph (slice 9).
- **Commit:** `feat(setups): refuse a network.mode the setup's technology cannot hold`
- **Report:** the red and green runs; the rewritten census cells.
- **Built as:** [SUP-D13](#SUP-D13). The integration test is renamed
  `TestAppleContainerExplicitHostModeIsRefused`: its host half is now a hard refusal assertion,
  and its port experiment runs the default launch only. Left for later: `yolo host` (host-self)
  reads no `network.mode`, so a written `bridge` there is refused by `yolo check` and not by the
  host launch; slice 8's host launch path is where that call belongs.

### Slice 5 — refuse the removed spellings and delete the notch code (after 4a)

- **Settled:** [§10](named-setups.md#10-what-is-removed)'s table, verbatim; *"`migrate` writes
  the replacement where the old value names exactly one."* Do not re-open.
- **Preconditions:** slice 4a landed and nothing outside `internal/config` references
  `config.ResolveConfinement`, `config.NotchRuntime`, `config.SelectedRuntime`,
  `config.ConfiguredRuntime`, `config.NotchRuntimeConflict`, `config.RuntimeKey`,
  `config.MacosUserMode`, `config.NotchEnv`, `config.SessionNotch`, `config.ContainerStepClause`,
  `macosuser.ParseMode`, `render.KindForNotch`: `rg -n` each; a remaining caller is stop and report.
- **Tests first** (`internal/config/setupsretired_test.go`):

  ```go
  func TestRemovedSpellingsRefuseNamingTheirSetup(t *testing.T) {
  	cases := []struct{ cfg, want string }{
  		{`confinement = "jail"`, "`confinement` now lives in a setup. For a container: `yolo config set setup podman-self --user`"},
  		{`confinement = "guest"`, "`guest` is now a setup: `yolo config set setup seatbelt-yolo-account --user`"},
  		{`confinement = "host"`, "Run on your own machine with `yolo host -- <cmd>`"},
  		{`runtime = "container"`, "`runtime` is now a setup: `yolo config set setup apple-container-self --user`"},
  		{`runtime = "macos-user"`, "seatbelt-yolo-account"},
  		{`macos_user_mode = "self"`, "Choose the account by setup: `seatbelt-self` or `seatbelt-yolo-account`"},
  	}
  	for _, c := range cases {
  		home := setupsHome(t)
  		userConfigWith(t, home, c.cfg+"\n")
  		_, errs, _ := validateScopedIn(t, t.TempDir(), "", "")
  		wantAll(t, errs, c.want)
  	}
  }

  func TestMigrateWritesTheOneSetupTheOldValueMeant(t *testing.T) {
  	// JSONC user config with "runtime": "container" migrates to setup = "apple-container-self";
  	// "confinement": "guest" is NOT converted (two Seatbelt setups) and the skip names both.
  	// Shape: configmigrate's existing lowering tests (internal/configmigrate/lower_test.go).
  }
  ```

  Red: `go test -short -run 'Removed|Migrate.*Setup' ./internal/config/ ./internal/configmigrate/`.
  **Rewrite, not repair:** `confinementruntime_test.go` (delete whole), `macosusermode_test.go`
  (delete), `TestNotchNamesMatchTheConfigVocabulary` (`internal/render/notchnames_test.go:27`) →
  pins `setup.Kind` ↔ `render.Kind`, `inherit_test.go` rows, `schema_test.go:11,222`,
  `TestConfigRefAnnouncesTheRefusalForEveryRetiredKey` now covers the four keys.
- **Change:** `validate.go:95-96,218-230`: `validateRuntime`/`validateConfinement` →
  `validateSetupsRetired` (one function, the retired pattern, host error / jail warning);
  `config.go:114-117` adds the four keys to `retiredTopLevelConfigKeys`; `renames.go:33-39`:
  descriptors with `TOMLRefusal` for all four (`RenameValueRewrite` for `runtime` whose value
  maps one-to-one, `RenameUnrepresentable` for `confinement = "guest"` and `macos_user_mode`,
  since each names two setups); `configmigrate/lower.go:179-200`: the `runtime` arm writes
  `setup` at user scope; `inherit.go:229-232,343-346`: the three rows become retired rows;
  `changetiming.go:88,103`, `setupcensus/configkeys.go:21-36,585-602`,
  `render/configkeys.go:133-137,146-151`: rows removed; `config_ref.txt:201-236,1600-1616,3233,
  3288`: `REMOVED` bodies with the [§10](named-setups.md#10-what-is-removed) sentences, the example config's `"runtime"` line and the
  env table's `YOLO_RUNTIME` row replaced by `setup` and `YOLO_SETUP`; `userconfig.jsonc:11-14`.
  Delete `confinement.go` whole, `macosusermode.go` whole (keep `macosusersharedtmp.go`:
  *"Out for now"*, [§7](named-setups.md#7-what-else-goes-in-and-what-stays-out)),
  `macosuser.ParseMode`, `render.KindForNotch` and `SelectableNotches` (`target.go:235-257`);
  `paths.AllRuntimes`/`NativeRuntimes`/`SupportedRuntimes` (`paths.go:23-31`) stay: they are the
  backend vocabulary `setup.Backend()` returns into.
- **Gates:** `just test-fast`; `just check-ci`.
- **Fences:** `internal/cli/run` (slices 4b, 9 run beside this); `internal/paths` (6a).
- **Commit:** `feat(config): refuse confinement, runtime and macos_user_mode naming their setup; migrate what maps`
- **Report:** the red and green runs; the `yolo config migrate` output on a JSONC user config
  holding `"runtime": "podman"` and `"confinement": "guest"`.

### Slice 6a — one home and run state per setup (after 4b, 5 and 9; launch-path)

**6a-ii built (2026-11-06).** Every backend now binds the selected setup's home,
`.yolo/setups/<name>/home`. The exception is [SUP-D21](#SUP-D21): a setup the legacy home is
still attributed to keeps binding `.yolo/home`. The launch calls `adoptLegacyHome` first
(`Options.selectHome` in `internal/cli/run/legacyhome.go`), and it prints the move's one line.
Every reader of a home follows [SUP-D34](#SUP-D34). The mountpoint record and the capture store
move with the home ([SUP-D33](#SUP-D33), [SUP-D35](#SUP-D35)), and each home keeps its own store.
The move waits while any jail of the project runs, by keeper, by session record or by container,
and decides under the workspace launch lock while a legacy home is in place.
`TestNamedSetupsHaveDistinctHomes` (`integration/setups_test.go`) and its legacy variant pin the
call site. Until 6a-iii, two setups of one project do not run side by side. A container launch
of a second setup while the first runs is refused at attach: the running jail uses one setup, and
this launch selected another, and the refusal names how to stop it (`refuseAttachSetupTruth`). The two Seatbelt setups share
one macos-user keeper key, and the keeper record names the first session's home. The move
of a `seatbelt-yolo-account` home has run only on Linux. A Mac measured that such a home's
directory is the invoking user's, so it passes the ordinary owner rule ([SUP-D31](#SUP-D31));
witness W7 in
[the Mac triage report](../lab-reports/2026-11-06-mac-ci-triage-witnesses.md#5-the-legacy-homes-move-under-the-yolo-account)
is still the live confirmation. 6a-ii does not cover the setup-qualified jail name, the per-setup boot baseline, the
pack tree or the [SUP-D10](#SUP-D10) refusal. Those are **6a-iii** ([SUP-D32](#SUP-D32)), which
keeps this slice's remaining **Tests first** rows and gates.

**6a-iii is built, held for W-SU1/W-SU4 (2026-11-09).** It is on branch `setups-6a3-6b`, rebased onto main
with the workspace-state-safety work ([SUP-D45](#SUP-D45)), together with 11b and 6b. Every running-jail key takes
`runtime.JailName`: the container, the keeper keys, the launch lock, the host-services hash, the
staged pack tree, the tracking file and the tool disk. What is the project's keeps the project key
([SUP-D39](#SUP-D39)). The launch records are per setup ([SUP-D37](#SUP-D37)). A legacy jail
refuses the launch before the move ([SUP-D43](#SUP-D43)), and `yolo stop` reaches it
([SUP-D40](#SUP-D40)). The legacy home's decision runs under the project lock
([SUP-D36](#SUP-D36)). Its unit tests are `TestJailNameAppendsTheSetup`,
`TestLaunchRecordsAreTheSetups`, `TestASessionThatNamesNoSetupHasNoRecords`,
`TestALegacyJailRunningRefusesTheLaunchBeforeAnyMove`, `TestTheMoveAsksEverySetupsJail`,
`TestTheProjectLockGoesWithTheLaunchLock`, `TestAStopFromTheFrontDoorAlsoStopsTheLegacyJail` and
`TestAForkBuildsRecordNamesItsJail`. `TestNamedSetupsHaveDistinctHomes` also asserts each
setup's own records. The integration suite names a launch's jail through `jailName`
(`integration/jailname_test.go`), and the harness removes every name a jail of a test workspace
may run under. Landing waits on W-SU1/W-SU4 and [W-SU12](#mac-checks).

**Focused source review (2026-11-06; not acceptance).** The private prototype's focused review closes three synthetic-only findings: strict phases bind the complete selection and actual spawned PID; the synthetic receiver validates singleton roles and descriptor kinds before taking ownership, requiring the lock at file descriptor (FD) 5 while valid reserved sockets at FDs 6/7 remain unchanged; and withdrawal waits for operations holding real duplicated FDs before `WITHDRAWN`. An FD here is an open kernel handle, not a pathname. This is not a whole-source verdict: the separate SU5 legacy-home adoption RED remains. The native outside/foreign-pack pathname semantic RED (a selected pack's shared-directory bind source swapped after validation, before the engine reads it) rests on the same custody requirement as per-setup homes, so [SUP-D30](#SUP-D30) covers it: it is an accepted risk, and the test that pins it red is not a landing gate. The only complete `go test -count=1 ./internal/cli/run` run was 340.601s, RC 1; the focused race run (21.535s), scoped vet and Darwin/arm64 compile passed.

**Local Podman bind-mount and holder evidence (one cell; not production integration).** In one local
nested Linux 7.2.9 cell using Podman 6.1.4/crun (`rootless=false`, `remote=false`), owned dummy
containers used bind-mount `-v`. In these cells Podman stored a source path, and crun resolved it
at START: swapping the ancestor after CREATE mounted the original writable tree when the source
named a live directory FD, but mounted the replacement for a pathname-only source.

Reusing the same process and FD number after closing the original also mounted the replacement,
although the stored source string was unchanged. With no descriptor at START, crun returned 125 and
left the container created; that was an engine failure, not an application refusal. A Go
`ExtraFiles`-inherited directory FD remained usable in its holder after the producer exited, keeping
the original writable bind. A separate object created before holder death but started only after the
FD disappeared failed at START; this does not determine what happens to an already-started
container's bind after holder death.

This supports only those local rootful cells. Production does not use descriptor custody ([SUP-D30](#SUP-D30)); this and the evidence below are kept as the record of what custody would need, should the threat model ever change.

A separate one-command local `podman run --preserve-fds=1` countercontrol passed the raw original directory FD into guest code. The guest verified that FD3 matched the mounted source and used FD-relative parent traversal to read an owned sibling outside the mounted leaf. No unrelated host path or credential was tested. This is a negative isolation result: direct raw-FD pass-through is not a production fix. The run had no detached create/start phase, and the engine process resolving `/proc/self` was not identified. Local `create --help` did not advertise this option; these observations do not establish what a detached creation path accepts.

Had custody been required, the next engineering proof would have had to identify a supported engine consume-before-guest path that preserves verified host-side FD custody through every admission and operation, while keeping the raw directory FD out of guest code. The source FD must be consumed and closed before untrusted guest code starts; do not substitute file modes (including `0700`), a token, pathname/PID/FD-slot identity, effective UID, or guest bootstrap ordering for that boundary. Verify the engine resolver and lifetime directly, and check guest descriptor absence plus lack of parent-relative access. Do not infer this from a PID/FD-slot path or pre-parse `fstat`. No new runtime wrapper, custom namespace, public flag, privilege change, or backend exclusion is settled here; the raw-FD pass-through result is not activation authority. The synthetic prototype still conservatively retains uncertain-COMMIT FD copies in-process; neither that closure nor the local probes prove a production release operation. No lease or queued-lookup protocol, native activation, rootless/remote Podman, Apple Container, macOS or Seatbelt behavior is established. No backend fallback or source activation follows from it.

**Held `/proc/<holder>/fd/<n>` probe (2026-11-06; rootful nested Podman 6.1.4 with crun, Linux;
scripts and logs under `$YOLO_DURABLE_DIR/slice6a/probes/`, outside the repository).** A host
holder process opened a source directory through an `O_DIRECTORY|O_NOFOLLOW` chain, marked
close-on-exec. Before `podman run -d --rm --init`, it renamed the source's parent aside and
planted a symbolic link to an outside directory in its place. The run named the source as
`/proc/<holder-pid>/fd/<n>`, once with `-v` and once with `--mount type=bind` (`procfd_holder.py`;
`v.log`, `mount.log`).

- **Resolution time.** The stored mount source stayed the unresolved `/proc/...` string at create,
  so the engine resolved it at start.
- **What the guest saw.** The guest read the original directory's marker, and mountinfo named the
  original directory as the mount root.
- **No descriptor in the guest.** No guest process held a directory descriptor: the shell held
  only standard streams and pipes, and init held only standard streams and a signalfd.
- **No parent-relative access.** `..` of the mount point was the container's own `/mnt`, the
  outside sibling was absent, and the holder's `/proc` entry was not visible.
- **After the holder closed it.** After start, the holder closed the descriptor and a second
  rename moved the original again. Guest writes still landed in the original directory, and
  nothing reached the outside directory or a decoy.

**Restart re-resolves.** A container whose holder closed the descriptor after start and reused
the same slot for a decoy directory was `podman restart`ed. The restart returned 0, and the guest
then wrote into both the original and the decoy (`procfd_restart.py`; `restart.log`). The holder
must therefore keep the descriptor for the container's whole life, and nothing yolo runs may
restart a container. Production launches use `run --rm` (`internal/cli/run/assemble.go`) and yolo
restarts none, but a host user's manual `podman restart` would resolve whatever then holds the
slot. A holder that lives as long as the container closes that gap.

**Masking a subtree does not stop a jail's writes.** The probe bound a workspace read-write and
mounted a tmpfs at `/workspace/.yolo/setups` (`mask_rename.py`; `mask.log`). Renaming the masked
mount point failed with EBUSY. But the jail renamed the parent `.yolo` aside, recreated it, and
planted `.yolo/setups -> ../.yolo-aside/setups`, and that link was visible on the host. Hiding
part of the path alone does not stop the writer, because jails share the workspace and a jail can still
rewrite a directory above the hidden part. A second probe that also bound `.yolo` onto itself,
so that renaming it failed with EBUSY, closed the race on podman; that pair is recorded as available,
not adopted ([SUP-D30](#SUP-D30)).

**Assessed but not probed:**

- **`openat2` with `RESOLVE_NO_SYMLINKS` or `RESOLVE_BENEATH`.** yolo cannot make the engine
  resolve this way. A yolo-side `openat2` check is only validation, and it still leaves the window
  before the engine reads the path.
- **A host-side bind into a private staging directory.** Mounting needs mount privilege, which
  rootless podman has only inside `podman unshare`'s namespace. That is a new privileged step, not
  a cheap mechanism.

**What these probes do not show.** Rootless podman is unverified: a nested jail runs with
`--userns=host`. Reading another process's `/proc/<pid>/fd` needs a ptrace read-mode check against
the same user, which rootless podman's runtime should pass, but no probe has run it. macOS podman
runs its engine in a VM, which cannot open the host's `/proc`, and Apple Container has no `/proc`
at all, so neither is covered. Seatbelt mounts nothing. [OQ-SU7](named-setups.md#OQ-SU7) is ruled against custody on every backend, so
none of this is built ([SUP-D30](#SUP-D30)).

**Landing split ([SUP-D19](#SUP-D19), 2026-11-06).** *6a-i, which lands now*, covers the
`internal/runtime/naming.go` `JailName` row of **Tests first**, the `internal/paths/setupstate_test.go`
row (with `WorkspaceHomeState` kept), the `legacyhome_test.go` move cases ([SUP-D20](#SUP-D20)),
and their code. *6a-ii* is the rest of this slice, unchanged, and keeps its gates. 6a-i
adds no production caller. Its gates are `just check-ci` plus a nested launch, which shows the
launch unchanged (`.yolo/home` still used, no `.yolo/setups`).

**6a-ii entry conditions added by the 6a-i review (2026-11-06):**

- **No custody ([SUP-D30](#SUP-D30)).** The switch binds `.yolo/setups/<name>/home` (and each per-directory child) by pathname after the existing no-link check, as `.yolo/home` is bound today. Add no held descriptor, no refusal of a second setup while another jail of the project runs, and no mount over `.yolo` or `.yolo/setups`. The restart rule from the held-descriptor probe does not carry over: it guarded a reused descriptor slot, and a pathname bind that restarts re-resolves the same path, which is the accepted race. yolo still runs `--rm` and restarts none.

- **[SUP-D21](#SUP-D21).** The home switch reads `adoptLegacyHome`'s result first. While a legacy home attributed to the selected setup is in place, that launch binds `.yolo/home` and creates no setup home. Add a test that fails if the switch creates the setup home beside an unmoved legacy home.
- **Who owns a `seatbelt-yolo-account` home.** Check on the Mac whether a `seatbelt-yolo-account` launch leaves `<ws>/.yolo/home` owned by the invoking user or by the sandbox account; `macosuser.LaunchHomes` uses `WorkspaceHomeState` in both modes. If the sandbox account owns it, SUP-D20's owner rule leaves every such home in place with a `sudo chown` step. Then rule, before wiring, whether the yolo account counts as an owner the move may take a home from (a design question for the owner, with this witness attached). *Measured 2026-11-06* ([lab report](../lab-reports/2026-11-06-mac-ci-triage-witnesses-run.md#4-who-owns-a-seatbelt-yolo-account-home)): `.yolo/home` itself is the invoking user's (`matt:_yolojail`, with inherited `_yolojail` ACLs), and nearly every entry in it is `_yolojail`'s.

- **Settled:** *"Each setup in a project has its own home and run state, under
  `<project>/.yolo/setups/<name>/`"*; per setup: the home, the `per_side_paths` copies, the
  staged pack tree, the boot-time baseline; per project: approvals, grants, `.yolo/durable`,
  `handover.md`, `launch.log` (*"whose every line names its setup"*); the same setup twice
  behaves as today; an existing identifiable `.yolo/home` that is a real directory moves
  silently once no jail of the project runs, otherwise it stays with a printed `mv`, never
  deleted ([§9](named-setups.md#9-state-per-setup)). [SUP-D2](#SUP-D2), [SUP-D9](#SUP-D9),
  [SUP-D10](#SUP-D10). Do not reopen the accepted per-setup split. The legacy-home move is
  [SUP-D9](#SUP-D9): silent, gated on no running jail and a real-directory source.
- **Preconditions:** slices 4b, 5 and 9 landed (so `run.go` is quiet);
  `rg -n 'WorkspaceHomeState\(|FromWorkspace\(' internal --glob '*.go' | rg -v '_test.go'`
  is the call-site inventory. Classify each use by lifetime: per-setup home or running-jail
  identity versus per-project approval/grant identity; follow the actual bind, capture and
  discovery paths too, since a search count is not a complete map. Stop on a missing caller,
  unexpected dirty file, or a behavior conflict with the accepted design. `runtime.JailName`,
  the setup-path helpers and `adoptLegacyHome` arrive with 6a-i ([SUP-D19](#SUP-D19)); 6a-ii
  also meets the entry conditions listed under the landing split. The legacy-home move follows
  [SUP-D9](#SUP-D9) ([OQ-SU5](named-setups.md#OQ-SU5) is ruled). Continue independent caller, path and fixture work without
  reinterpreting the accepted adoption behavior.
- **Tests first:**
  - **Behavioral red first:** `integration/setups_test.go`, `TestNamedSetupsHaveDistinctHomes` (new): in one workspace, run `yolo run --setup podman-self -- true` and `yolo host --setup host-self -- true`; assert both `.yolo/setups/<name>/home` directories exist and differ. Red: `go test -count=1 -run '^TestNamedSetupsHaveDistinctHomes$' ./integration`; it must fail because the current launch reuses `.yolo/home`. A compile error or an assertion that a future helper is absent is not the behavioral red.
  - `internal/paths/setupstate_test.go`: `SetupStateDir(ws, "podman-self") == ws+"/.yolo/setups/podman-self"`,
    `SetupHomeState(ws, name) == …/home`; `SetupsDir(ws)` returns the path
    `ws+"/.yolo/setups"` and creates nothing; `WorkspaceHomeState` is removed. Test that a
    linked `.yolo`, `setups` or named setup child is refused before any host write.
  - `internal/runtime/naming_test.go`: add `TestJailNameAppendsTheSetup`
    (`JailName("/w/alpha", "seatbelt-self") == FromWorkspace("/w/alpha")+"-seatbelt-self"`);
    `TestFromResolvedGolden` (`:23`) unchanged and still green.
  - `internal/config/drift_test.go`: the baseline path is `SetupStateDir(ws, os.Getenv("YOLO_SETUP"))/config-boot.json`;
    with `YOLO_SETUP` unset in-jail, `ReadWorkspaceBootBaseline` returns a named error (no silent
    `.yolo/config-boot.json`).
  - `internal/cli/run/legacyhome_test.go`: per [SUP-D9](#SUP-D9): a quiesced
    first launch moves the home with no prompt and one informational line; a running jail of
    the project leaves it in place; a symbolic-link or foreign-owned source is left in place
    with the printed `mv`. The source-swap regression is retired: the ruling accepts that race
    once no jail runs. Keep decline and nonterminal
    preservation/refusal tests independent where they do not move a legacy home. A running
    legacy container under `runtime.FromWorkspace(ws)` must still refuse before any move
    or other state effect.
  - **Rewrite** every test asserting `.yolo/home`: unit — `internal/cli/run/inheritscope_test.go`
    (the MMP-D32 refusal becomes the adoption prompt), `internal/macosuser/*_test.go` reading
    `WorkspaceHomeState`; integration — `isolation_test.go:39`, `hostfiles_test.go:205`,
    `mcp_test.go:102-129`, `capturematerialize_test.go:139-160`, `applecontainerhome_test.go:71`,
    `applecontainerparity_test.go:236`, and the `TestMacosUser*` homes list in the macos-user plan's
    W1 (`macosuserhometier_test.go`, `macosuseraccounthome_test.go:21`, `macosuseridentity_test.go:32`,
    `macosuserdenydefault_test.go:147,327,1521`, `macosuserseatbelt_test.go:180`): `$HOME` is
    `<ws>/.yolo/setups/<name>/home`.
- **Change:**
  - `internal/paths/paths.go:664-759`: `SetupsDir(ws)`, `SetupStateDir(ws, name)`,
    `SetupHomeState(ws, name)`; delete `WorkspaceHomeState`. `SetupsDir` names the path;
    6b enumerates it. `EnsureWorkspaceStateDir` remains the `.yolo` chokepoint. New
    setup-state creation and readers must use the no-link chain formed by `OpenStateDirRoot`
    and `OpenStateSubdirRoot` (`statefile.go:33-85`), not unchecked joins or
    `OpenWorkspaceStateSubdir` (read-only and only for an existing direct child).
  - `internal/runtime/naming.go`: `JailName(ws, setup string) string`.
  - Follow the actual `WorkspaceHomeState` readers/writers, including
    `run/prepare.go`, `assemble.go`/`persistencemap.go`/`wsstatebeneath.go`,
    `run/inheritscope.go`, `run/jailgrant.go`, `run/agentlogreap.go`,
    `cli/buildjaillogs.go`, `cli/internal.go`, `cli/capturehost.go`, `cli/forkbuild.go`,
    `macosuser/{mode,runplan,orchestrator,provision,jaildaemon,homereadonly}.go`,
    `claudeview.go`, and `prune/prune.go`. The direct-call search is not a complete
    mount/capture inventory: `persistencemap.go:117` hand-joins `home`; shadow binds,
    Apple Container's whole-home bind, podman's per-dir binds and native `$HOME` must agree.
    In-jail session paths use `YOLO_SETUP`; do not invent a `WorkspaceHomeState` caller in
    `entrypoint/reachability.go` (there is none at this source stamp).
  - `config/drift.go:38-43`, `assembled.go:59,89`, `configmigrate/plan.go:1099` and
    its record application in `configmigrate/apply.go:235-239`: put boot baseline and
    assembled records under `SetupStateDir`, retaining the session digest check.
    `internal/cli/configtarget.go:114-125,329-438`, `configcapture.go:79`,
    `run/launchcontext.go:663`, `render/target.go:403-415` and `entrypoint/prism.go:42-53`
    must resolve the **same** setup-qualified prism store; `configtarget.go` and
    `configcapture.go` live in `internal/cli`, not `run`. Preserve `render.Target`'s host-own
    capture routing, sealed-workspace behavior, and no-follow readers through the nested
    state-root chain. `configtarget.go:366-374` also treats the boot baseline as a workspace
    marker; keep its discovery working.
  - Mint the qualified `cname` after setup selection at `run/run.go:358`;
    `run.go:168` initializes perf before selection, so move that per-run identity
    initialization without losing early-probe timing. Follow the actual running-jail
    callers in `internal/cli/run` (`keeperspawn`, `hostdoorways`, `hostlaunchcontext`,
    `hosttiming`, `macosuserguestdaemons`, `launchrecord`, `trackingcleanup`, `launchlog`,
    `liveapply`, `inheritscope`, `migrateoffer`, `runcmd`); `internal/cli` (`stop`,
    `configrunningjail`, `programcapture`, `programcaptureproducer`, `capturehost`,
    `backgroundadvance`, `forkbuild`, `check/sections_loopholes`); `internal/hostrun/host.go`;
    `internal/macosuser/runplan.go` (`cnameFn`); and prune (`miseversions` plus orphan/volume
    discovery). Staging workspaces for capture/fork use the setup their acts really run under
    ([SUP-D14](#SUP-D14)). Carry `cname` through keeper plan/spawn, lease and private MCP
    outcome generation (`keeperplan.go`, `mcpteardown.go`, `mcpoutcomeretire.go`), broker
    scope files, native `SessionKey`, host-service endpoint hash, pack-tree root, attach and
    cleanup. **Do not** qualify the approval/grant project key (`config/{snapshot,
    scopeapproval,loopholegrantgate}.go` retain `FromWorkspace`).
  - The selected MCP launch input flows through `launchinputmcp.go` → `keeperplan.go:106` →
    `keeperspawn.go:169-264` → `loopholesruntime.go`; resource placement remains default OFF.
    The current final-harvest receipt requires an observed stop of the selected service,
    confirmed jail absence, and no harvest failure before `ok` is published; carry the selected
    service and generation identity with the setup-qualified jail key. G1's HTTP snapshot
    extension remains unlanded. Make no fresh-origin, grant-file or resource-policy change in
    6a. If G1 lands first, re-read its keeper and selected-service callers and compose only the
    setup-qualified lifetime/identity wiring.
  - `internal/cli/run/legacyhome.go` (proposed, [SUP-D9](#SUP-D9)):
    `adoptLegacyHome(o)` and `refuseLegacyContainer(o)` would run after `resolveSetup` but
    before home preparation. Check the **old** `runtime.FromWorkspace` container name before
    any move, and move only when no jail of the project runs, with `Lstat` (real directory,
    owned by the invoking user) and the destination-exclusive rename; no prompt. Retain
    `isAppleContainerInheritedCopy` as a layout signal, not `.claude.json`. The built move gets
    the slice's independent review.
  - `launch.log`: retain the existing setup-prefix behavior in `attachLaunchLog` (`launchlog.go`);
    do not duplicate or rename the project-wide file.
- **Gates:** `just test-fast`; `GOOS=darwin go vet ./internal/macosuser/... ./internal/cli/...`;
  `just check-ci`; build `just build-dev-bundle` under the original build `HOME` and effective
  Go settings, then invoke its absolute emitted launcher from a throwaway workspace with only the
  execution `HOME` isolated; do not isolate `HOME` for the build or change its effective Go
  settings. Keep the normal `YOLO_TEST_JAIL_TIMEOUT` integration-helper budget (300 seconds by
  default); do not disable or silently shrink it. Launch once with
  `--setup podman-self -- true` and once with `yolo host --setup host-self -- true`, then inspect
  `ls .yolo/setups/`. On the combined 6a+6b candidate, run the full integration suite and obtain
  **W-SU1 (unchanged) and W-SU4 (revised, SUP-D49) from the owner's Mac agent before either primary landing**. W-SU1
  checks 6b's `setup list`/`stop`; build and test 6b on the 6a commit first, then serially land
  the reviewed pair. A nested rootful run does not witness rootless-only or native Mac paths.
- **Fences:** `yolo stop`/`ps` enumeration (6b); `internal/macosuser/runplan.go` argv (8).
- **Commit:** `feat(setups): one home and run state per setup under .yolo/setups/<name>; setup-qualified jail names`
- **Report:** the red and green runs; the `ls .yolo/setups` output; the source-grounded
  identity/home/capture caller map and the unchanged native witness output before landing.

### Slice 6b — stop, ps, stores and `setup list` see every setup (after 6a)

- **Settled:** *"`yolo stop` stops both, naming each; `yolo stop --setup seatbelt-self` stops
  one"*; `yolo setup list` shows which setups are running in this project
  ([§6](named-setups.md#6-config-by-example), [§11](named-setups.md#11-what-done-looks-like));
  [SUP-D11](#SUP-D11).
- **`yolo ps` and `yolo stores` are [slice 11](#slice-11--yolo-ps-and-yolo-stores-list-every-technology-after-4a-independent-of-6a)'s**,
  ruled by [OQ-SU6](named-setups.md#decision-ledger). Once 6a's setup-qualified names exist, they
  already list one row per setup; this slice adds nothing to them beyond the `ps_test.go` case
  below. `yolo setup list`'s running column reads slice 11's discovery narrowed to the workspace
  ([§12.6](named-setups.md#126-narrowing-with---setup)).
- **Preconditions:** slice 6a committed and its applicable tests green in the same
  isolated candidate (not yet primary-landed); `go doc ./internal/paths SetupsDir` prints.
  Do not make 6b a primary-tree change while 6a waits on W-SU1: that exact
  witness exercises this slice's `setup list` and `stop`.
- **Tests first:** `internal/cli/stop_test.go` (**rewrite** `stopruntime_test.go`, `stopgone_test.go`):
  with `.yolo/setups/{podman-self,seatbelt-self}` present and a stubbed runner, `yolo stop` issues
  one stop per jail name and prints `stopped podman-self` / `stopped seatbelt-self`;
  `yolo stop --setup seatbelt-self` issues one. `ps_test.go`: a row per setup with a `SETUP`
  column. `setupcmd_test.go`: `list` shows `running` for a setup whose keeper record is live
  (stub `probeKeeper`). `stores/macosuserrows_test.go`: the Seatbelt rows name the setup. Red:
  `go test -short -run 'Stop|Ps|SetupList' ./internal/cli/...`.
- **Change:** `stop.go:82-196,323-420`: iterate `paths.SetupsDir(ws)` (or the one `--setup`),
  decode each name through `setup.Select`-style lookup (built-in or user table), stop by its
  technology; `:601` other-runtime hint deleted. `ps.go:68-99,191-235`: same iteration;
  `commands.go:1068,436` wiring. `setupcmd.go`: the running column. `stores/inventory.go:547-548,
  1148-1153`: per setup.
- **Gates:** `go test -short ./internal/cli/...`; `just check-ci`; on the
  frozen combined 6a+6b candidate, W-SU1 (unchanged) and W-SU4 (revised, SUP-D49) from the owner's
  Mac agent plus 6a's full integration gate before either primary landing.
- **Fences:** `internal/paths`, `internal/cli/run`.
- **Commit:** `feat(cli): stop, ps, stores and setup list act per setup`
- **Report:** the red and green runs; `yolo stop` output with two setups present.

**Built, held for W-SU1/W-SU4 (2026-11-09).** 6b is on branch `setups-6a3-6b` with 6a-iii and 11b,
and is not landed. `yolo stop` stops every setup's jail on its own technology and prints
`stopped <setup>` for each one it ended; `--setup <name>` stops one ([SUP-D46](#SUP-D46)).
`yolo setup list` marks a setup `running` or `may be running` in this workspace
([SUP-D47](#SUP-D47)). `yolo ps` has a SETUP column, and `yolo stores`' Seatbelt rows say their
copies are per workspace and setup ([SUP-D48](#SUP-D48)). Its unit tests are
`TestAStopStopsEverySetupsJailNamingEach`, `TestAStopWithSetupStopsThatSetupsJailAlone`,
`TestAStopLeavesASetupWithNothingLeftAlone`, `TestAStopWithSetupRefusesWhatNamesNoJail`,
`TestSetupListMarksTheSetupsRunningHere`, `TestJailRunStateAsksTheJailsKeepers`,
`TestPsListsARowPerSetupWithItsSetup` and the rewritten `TestRenderPsTable`. The integration
test `TestStopStopsEverySetupsJail` is W-SU1's Linux half: two podman setups running at once, both
marked `running` by `setup list`, and both ended by one `yolo stop`. Landing waits on W-SU1/W-SU4
and [W-SU12](#mac-checks).

### Slice 7 — `yolo yolo-account create | remove | share | unshare` (independent)

- **Settled:** [OQ-MT8](named-setups.md#OQ-MT8): the four verbs on both systems; `macos-setup`,
  `macos-teardown`, `macos-fix-permissions`, `macos-unshare` refused with *"Now `yolo
  yolo-account create`"*, `remove`, `share`, `unshare`; `_yolojail` is "the yolo account" in
  every message ([OQ-MT4](named-setups.md#OQ-MT4)). The verbs' behavior is unchanged.
- **Preconditions:** `rg -n '"macos-setup"' internal/cli/dispatch.go` prints `:55`.
- **Tests first:** `internal/cli/yoloaccount_test.go`: `yolo macos-setup` exits non-zero with
  stderr `yolo macos-setup is now yolo yolo-account create`; the same for the other three;
  `yolo yolo-account` with no subverb prints usage; `TestEveryRegisteredCommandIsListedInHelp`
  and `TestEveryRegisteredCommandAnswersHelp` pass with the new verb;
  `hintcommands_test.go:326-327` and `privilege_test.go:55-56` **rewritten** to the new
  spellings. Red: `go test -short -run 'YoloAccount|RegisteredCommand' ./internal/cli/`.
- **Change:** `internal/cli/commands.go:35-145`: one `runYoloAccount` dispatching to
  `macosuser.MacosSetup`/`Teardown`/`FixPermissions`/`Unshare` (rename the Go funcs to
  `AccountCreate`/`Remove`/`Share`/`Unshare`, `internal/macosuser/commands.go:21,240,281,305`);
  the four old verbs become one `refuseRenamedVerb`; `dispatch.go:55-58`, `help.go:97-109`,
  `subhelp.go:132-135`. Every string holding `yolo macos-…` or "sandbox account"/"second
  account"/"hidden user" in the map's slice-7 row says the new verb and "the yolo account"
  (`rg -n 'macos-setup|macos-teardown|macos-fix-permissions|macos-unshare|sandbox account|second account' internal --type go | rg -v _test`
  is the checklist; `preconditions.go` has 21, `macosuser/commands.go` 24). Integration helpers
  `macosUserWorkspace` (`macosusergate_test.go:607`) and the asserting tests
  (`:753-755`, `macosuserrelocations_test.go:131`, `macosusernorootsudo_test.go:357-366`,
  `hostfloor_test.go:427`, `macosuserprovision_test.go:252`) say the new verbs;
  `.github/workflows/macos-user.yml:312,652` run `yolo-account create`.
- **Gates:** `go test -short ./internal/cli/ ./internal/macosuser/`; `GOOS=darwin go vet
  ./internal/macosuser/...`; `just check-ci`.
- **Fences:** nothing under `internal/cli/run`, `internal/config`, `internal/setup`.
- **Commit:** `feat(cli): yolo yolo-account create, remove, share, unshare; the macos-* verbs refused`
- **Report:** the red and green runs; the count of strings changed per file.

### Slice 8 — `host-yolo-account` on macOS (after 6a; Mac witness)

- **Settled:** [SU-R6](named-setups.md#SU-R6): *"no Seatbelt, run as the yolo account, through
  `yolo host` only"*; Linux refuses (*"waits on the yolo account there"*); the home is
  `<ws>/.yolo/setups/host-yolo-account/home`; [SUP-D5](#SUP-D5). Do not re-open.
- **Preconditions:** slice 6a landed; `rg -n 'func BuildRunPlanWithStages' internal/macosuser/runplan.go`
  prints `:604`; `PlanInvariants` at `:1613`.
- **Tests first** (`internal/macosuser/unconfined_test.go`, Linux-runnable like
  `selfmode_test.go`): an account-mode plan built with `PlanSession{Unconfined: true}` has **no**
  `/usr/bin/sandbox-exec` on `LaunchArgv`, `BootstrapArgv`, `ProvisionArgv`, any daemon argv or
  the host-service witness argv, keeps `RunAsSandboxArgv`'s sudo crossing on each, and
  `PlanInvariants` returns no problem; the same plan with `Unconfined: false` is byte-identical
  but for the prefix (diff the argvs). `internal/cli/host_test.go`: `yolo host --setup
  host-yolo-account -- true` on Linux refuses *"not built on Linux"* naming `host-self`; on
  darwin (table-driven with `goos` injected) routes to the macos-user orchestrator, not
  `hostLaunch`. Red: `go test -short -run 'Unconfined|HostYoloAccount' ./internal/macosuser/ ./internal/cli/`.
- **Change:** `RunPlan.Unconfined bool` and `PlanSession.Unconfined`; `BuildRunPlanWithStages`
  (`:604`) skips `DenyDefaultProfile` (`:1109-1118`) and `ProfilePath` when unconfined;
  `ProvisionArgv` (`provision.go:148-169`), `serviceprobe.go:46-64` (`:119` invariant),
  `JailDaemonArgv` (`jaildaemon.go:487`), the launch argv builder and
  `LaunchArgvWithGuard` (`macosuser.go:1405`) take the prefix from one `confinementPrefix(plan)`
  that returns nothing when unconfined; `PlanInvariants:1933,2030,2195,2253,2471` gain the
  inverse checks; `orchestrator.go:1325-1329` writes no profile when there is none; the launch
  disclosure line reads *"host-yolo-account runs the agent as the yolo account with no Seatbelt"*.
  `internal/cli/host.go:214-250,606`: `--setup host-yolo-account` hands to
  `commands.go`'s macos-user arm with `Unconfined: true` and `ModeAccount` (the preconditions,
  the sudo rule, `HomeContaining`, the grant stage all as `seatbelt-yolo-account`).
- **Gates:** `go test -short ./internal/macosuser/ ./internal/cli/`; `GOOS=darwin go vet
  ./internal/macosuser/... ./internal/cli/...`; `just check-ci`. Landing waits on witness
  **W-SU3** (below): nothing on Linux proves an unconfined darwin launch.
- **Fences:** `seatbeltdeny.go`, `seatbelt.go` (slice 13 of the macos-user plan deletes
  `seatbeltProfile`; do not touch it here); `internal/paths`.
- **Commit:** `feat(setups): host-yolo-account on macOS — the account plan with no Seatbelt`
- **Report:** the red and green runs, the argv diff, and the W-SU3 paste.

### Slice 9 — the launch line, the footer and the briefing name the setup (after 4a)

- **Settled:** *"Every launch line, the agent's footer and its briefing name the setup and
  spell its fields"* ([§2](named-setups.md#2-the-idea)): `yolo: setup seatbelt-self (Seatbelt,
  deny-by-default, as you; shares the Mac's network) - from --setup`. Footer `guest`, "second
  account", "sandbox account" and "jail" for the walls are replaced by the setup name, "the yolo
  account" and "a container" ([§10](named-setups.md#10-what-is-removed)).
- **Preconditions:** slice 4a landed (`YOLO_SETUP` is in the session env).
- **Tests first:** `internal/footer/footer_test.go`: `Setup()` returns `os.Getenv("YOLO_SETUP")` in a session and `host-self` outside one; `DefaultTemplate` renders `yolo: … · seatbelt-self`. `internal/jailcontent/setupbriefing_test.go`: the header for
  `seatbelt-self` says *"You are running under setup **seatbelt-self**: Seatbelt, deny-by-default,
  as you"*, for `podman-self` *"a container"* and never the word "jail" for the walls; for
  `host-self` the host header stays. `internal/macosuser/setupdisclosure_test.go` exercises the native
  caller through `RunMacosUser`, and `internal/cli/macosusermode_test.go` pins the setup/source passed
  by the front door. `internal/cli/run/banner_test.go`: `LaunchBanner` prints
  `setup podman-self (Podman, as you; bridged) - from default` as its first part. Red:
  `go test -short -run 'Setup|Banner|Header' ./internal/footer/ ./internal/jailcontent/ ./internal/cli/run/`.
  **Rewrite:** `TestMacosUserFooterSaysItsSetup` (`integration/macosuserfooter_test.go:20`) pins the
  native footer; `TestMacosUserSeatbeltSelfLaunchNamesTheSetupInItsBriefingAndFooter`
  (`integration/macosusersetupfooter_test.go`) retains the real-launch self identity, Claude
  surface, briefing, and session/footer marker checks. Every shipped adapter template that used
  `{yolo.notch}` changes to `{yolo.setup}` (claude, agy, copilot, omp, opencode, pi).
- **Change:** `footer.go:78-87,173-185`; `briefing.go:128-133` (`BriefingInput.Setup`,
  `SetupDescription`), `:280`, `:308-320`, `:390-455`, `:526-531`; `prepare.go:182`,
  `mechanicsfacts.go:31`, `sharednetwork.go:20`; `banner.go:23-33` and its callers at
  `run.go:2524,3026`; the macos-user arm prints the same line (today it prints none,
  `orchestrator.go:971-973`); `orchestrator.go:888-900` reads `YOLO_SETUP`; `launchlog.go` lines
  carry the setup (6a prefixes every line; here the first line names it).
- **Gates:** `go test -short ./internal/footer/ ./internal/jailcontent/ ./internal/cli/run/`;
  `just check-ci`; landing runs the integration suite (the footer test on macOS CI).
- **Fences:** `internal/config`, `internal/paths`, `stop.go`, `ps.go`.
- **Commit:** `feat(setups): the launch line, footer and briefing name the setup and its fields`
- **Report:** the red and green runs; one launch's first yolo line verbatim. The slice-9 source changes are integrated at `06b3deab9`; the combined CI and full integration gates passed on source tree `35c14f452`. The native macOS witness remains outstanding.

### Slice 10 — docs (after 5 and 6a)

- **Settled:** the user guide describes behavior, not argument (project memory: capabilities
  first, plain works/doesn't). The authority for the new vocabulary is the design; the guide
  pages are rewritten to it, not patched.
- **Preconditions:** slices 5 and 6a landed (the keys refuse; the homes have moved).
- **Tests first:** `internal/setupcensus/guide_test.go` ties `settings-per-setup.md` to the census:
  rename `Setup.String()` (`census.go:76-80`) per [SUP-D8](#SUP-D8) and watch
  `TestNoUserGuidePageMakesAClaimTheCensusContradicts` (`:477`) go red on the page's old column
  headers; it is green when the page is rewritten.
- **Change** (each by path; the line numbers are where the false claim sits today):
  - `userguide/guides/confinement.md`: rewrite whole as "Where agents run: setups" — the two
    fields, the built-ins table, `--setup`/`YOLO_SETUP`/`setup =`, the per-OS default, the refusals,
    `yolo setup list|show|new`, homes under `.yolo/setups/<name>/`; delete the Guest section
    (`:175-187`) and `--at` (`:189-197`).
  - `userguide/reference/settings-per-setup.md:28-37` column table and every header (`:52` …);
    rows `:486-487,498-499` (`runtime`, `confinement`, `macos_user_mode`) removed; `:208,468`
    homes; `:80,84,114,317,321,411` `YOLO_RUNTIME` → `YOLO_SETUP`; `:54,150,154,323,327` the verbs
    and "the yolo account".
  - `userguide/guides/macos.md:17-30` "Choosing a setup"; `:187-240` the Seatbelt setups
    (`seatbelt-self`, `seatbelt-yolo-account`, `host-yolo-account`); `:203-212,285-290` the verbs.
  - `userguide/reference/cli-reference.md:35` (`--setup`), `:193-201` (`yolo yolo-account`, `yolo setup`).
  - `userguide/getting-started.md:247-254,291-303,616`; `userguide/guides/storage.md:15-17,59-64`;
    `userguide/README.md:83`; `README.md:24,46-47,118`.
  - `docs/reference/what-yolo-is.md:109-132` "The confinement dial" → "The setup", keeping the
    charter's rule that host is never inferred; `loophole-grants.md:383-395` rows named by setup;
    `storage-and-config.md:366,507`, `jail-home.md:182,194,558,1683` the home path;
    `agent-footer.md:69,129-139,565` the setup marker; `config-target-resolution.md:116,166,
    201-221,632` `--setup`; `report-tiers.md:575` the sample line.
  - `AGENTS.md`: the backend sentence under "Packs, and what core does not know" (*"Backends are
    `podman`, `container` … and `macos-user`"*) names the three technologies as setup
    confinements; the `macos_user_mode` mention (`:294`) becomes `seatbelt-self`.
  - `docs/plans/roadmap.md:481`: the sentence *"its implementation plan comes next"* links this
    plan.
- **Gates:** `scripts/vantage-check.sh userguide/ docs/reference/what-yolo-is.md
  docs/reference/loophole-grants.md docs/reference/storage-and-config.md docs/reference/jail-home.md
  docs/reference/agent-footer.md docs/reference/config-target-resolution.md docs/reference/report-tiers.md
  docs/plans/roadmap.md`; `go test -short ./internal/setupcensus/`; `just check-ci`.
- **Fences:** `docs/design/*`; no code outside `internal/setupcensus/census.go`.
- **Commit:** `docs(setups): the user guide and references describe named setups`
- **Report:** the vantage output; the census rename diff.

### Slice 11 — `yolo ps` and `yolo stores` list every technology (after 4a; independent of 6a)

**Build contract, planner-grounded 2026-11-06 against the tree at `3f131dfe2`.** The design is
[§12](named-setups.md#12-listing-what-runs-yolo-ps-and-yolo-stores) and its ledger rows
[SU-D9](named-setups.md#SU-D9) to [SU-D14](named-setups.md#SU-D14), under
[OQ-SU6](named-setups.md#OQ-SU6). The mechanism choices it leaves open are
[SUP-D22](#SUP-D22) to [SUP-D29](#SUP-D29). Six sub-slices, smallest first; each lands alone,
with its tests, and leaves `yolo ps` and `yolo stores` working.

- **Settled:** one list across every source of the OS, a state per source, `complete`,
  `schema: "yolo.ps/1"` and `"yolo.stores/1"`, `null` for unknown, setup vocabulary for
  `confinement`/`run_as`, exit 0, `YOLO_SETUP` ignored and `--setup` narrowing, pruning per
  technology, every `yolo host --` launch holding a session record. No compatibility with
  today's `psReport` or `Report.Runtime`.
- **The two unknowns, settled from the tree:**
  - **Podman reports a start time yolo already reads.** `prune.RunningJailStarts`
    (`internal/prune/copilotpkg.go`) asks `podman ps --format '{{.Names}} {{.StartedAt}}'` and
    parses unix seconds; its unit test pins that argv. `yolo ps` takes the same field
    ([SUP-D25](#SUP-D25)); `--format json` is not needed.
  - **Apple Container's start time is not one yolo can read yet.** `container ls --all --format
    json` exists and is parsed today (`appleImagesInUse`, `internal/prune/appleimages.go`), but
    only for `configuration.image`; `prune.RunningJailStarts`'s comment records that
    `status.startedDate` in `container inspect` has an encoding nobody has measured. A Mac has
    since measured it (witness W-SU11, under [Mac checks](#mac-checks)): an RFC 3339 UTC string
    with second precision. Until a change reads it, an Apple Container row's age comes from its
    launch record and is `?` without one. Nothing blocks on it.
- **A source's answer is one type** ([SUP-D22](#SUP-D22)), in `internal/runtime/tracking.go`:
  `SourceState` (`listed`, `not-installed`, `unreachable`, `timed-out`) and `SourceAnswer
  {Confinement, State, Names}`. `internal/cli` decides which container sources this OS has and
  whether each is installed, through `setup.Buildable` of each technology's built-in
  (`podman-self`, `apple-container-self`) for `platformGOOS()`.

#### 11a — prune a launch record only on its technology's answer ([SU-D10](named-setups.md#SU-D10))

The defect fix, alone. Today every tracking file names no technology, so this sub-slice ships the
rule's legacy half, which is the half that protects every record that exists.

- **Tests first (red):** `internal/runtime/tracking_test.go`
  `TestPruneKeepsRecordsUntilEveryContainerSourceListed`: records for an Apple Container jail and
  a dead Podman jail; Podman `listed` with no names and Apple Container `unreachable`: nothing is
  removed; Apple Container `not-installed`: both go; both `listed` with Apple Container listing its
  jail: only the Podman record goes. `internal/cli/ps_test.go`
  `TestPsPodmanOnlyOnAMacKeepsAppleContainerRecords`: GOOS darwin, `podman` and `container` both
  installed, the asked runtime `podman`, Podman lists nothing; the Apple Container jail's record
  survives. On Linux Podman is every container source, so `TestPsPrunesStaleTracking` stays green.
- **Change:** `runtime.PruneStaleTrackingFiles(answers []SourceAnswer) []string` replaces the
  one-set signature: a record goes when every answer is `listed` or `not-installed` and no
  `listed` answer names it. `internal/cli/ps.go`: `psDeps` gains `GOOS string` and
  `HasBinary func(string) bool` (real: `platformGOOS()`, `exec.LookPath`); `psContainerAnswers`
  builds one answer per container source of the GOOS: the asked one `listed` with its names, every
  other installed one `unreachable` (it was not asked), every missing one `not-installed`. Both
  prune call sites in `psRun` pass it.
- **Interim cost, accepted ([SUP-D26](#SUP-D26)):** on a Mac with both runtimes installed, `yolo ps`
  prunes no record until 11d asks every source. A record whose jail ended normally is still
  removed by the launch's own `forgetGoneContainer`; only a crash leaves one behind.
- **Gates:** `go test -short -run 'Prune|Ps|Tracking' ./internal/runtime/ ./internal/cli/`;
  `just check-ci`. No integration test reads `PruneStaleTrackingFiles`.
- **Commit:** `fix(ps): ...` (the SU-D10 defect).

#### 11b — the container launch record ([SU-D11](named-setups.md#SU-D11), container half)

- **Tests first:** `tracking_test.go`: `WriteLaunchRecord`/`ReadLaunchRecord` round-trip every
  field; a legacy one-line file reads as a workspace with every other field unknown; a record
  naming `podman` is pruned on Podman's `listed` answer alone while Apple Container is
  `unreachable` (the rule's technology half). `internal/cli/run`: the launch guard's record call
  writes a record whose `setup`, `confinement` and `packs` are the launch's, pinning the call site
  and not only the callee.
- **Change:** `internal/runtime/tracking.go`: `LaunchRecord{Name, Setup, Confinement, RunAs,
  Workspace, Started, Packs, YoloVersion}`, JSON keys as in
  [§12.4](named-setups.md#124-the-json-document); `WriteLaunchRecord` replaces
  `WriteContainerTracking` at the same path, `paths.ContainerDir()/<name>` ([SUP-D23](#SUP-D23));
  `ReadContainerWorkspace` reads through `ReadLaunchRecord`; the prune applies the technology
  half. Callers: `internal/cli/run/run.go` `runtimeWriteTracking` and `sliceutil.go`
  `writeTracking` (shared with slice 6a: one call-site edit carrying `o.Setup` and the staged
  packs' names), and the test helpers that write tracking files (`forkbuild*_test.go`,
  `stopruntime_test.go`, `ps_test.go`), which pass a workspace only.
- **Gates:** `go test -short ./internal/runtime/ ./internal/cli/...`; `just check-ci`; this is the
  launch path, so the full integration suite at landing.
- **Commit:** `feat(run): the tracking file becomes the launch record`

**Built, held with 6a-iii on branch `setups-6a3-6b` (2026-11-09).** The record is JSON with null for an unknown, and
it is written through a temporary file and a rename, so `yolo ps` never reads half of one. A
record that names its technology is judged by that technology's answer alone. A `not-installed`
answer keeps it, which closes 11a's PATH-miss gap for every record written since. The call site
is `runtimeWriteTracking(o.containerLaunchRecord(cname, loadedPacks))`. The record's version is
the launcher's own (`version.Binary`), never a `YOLO_VERSION` override. Tests:
`TestALaunchRecordRoundTripsEveryField`, `TestALegacyTrackingFileReadsAsTheWorkspaceAlone`,
`TestARecordNamingItsTechnologyIsJudgedByThatTechnology` and
`TestTheLaunchRecordCarriesTheLaunchsSetupAndPacks` (the call-site pin). Its landing still needs
the full integration suite.

#### 11c — every `yolo host --` launch holds a session ([SU-D12](named-setups.md#SU-D12))

**Built** as the session alone; the record's new fields moved to 11b, which defines them once
for both records ([SUP-D23](#SUP-D23)).

- **Tests:** `internal/cli/hostsession_test.go`
  `TestHostLaunchWithNoDoorwayHoldsASessionAcrossTheExec`: at the exec of a `yolo host -- claude`
  that opens no doorway, exactly one session exists, its record names `host`, the workspace and
  the launch's pid, a listing reads it live, and its lock descriptor is not close-on-exec; when
  the launch returns instead, the dir is gone.
  `TestHostLaunchWithServicesButNoDoorwayHoldsOneSession`: a resident launch with a launch-owned
  service holds exactly one session while it runs and none after. `macosusersessions_test.go`
  `TestASessionWhoseRecordedProcessExitedIsGone`: a held lock whose recorded pid has exited lists
  as gone, and the listing removes nothing. `macosusersessions_test.go` `TestOnlyTheSpawnOpensAServicesSession`
  now admits `OpenHostSession` as the second creator and requires `HostSession.End` to release
  the lock.
- **Change:** `internal/cli/run/servicessession.go`: `OpenHostSession`, `HostSession.End`,
  `HostSession.KeepAcrossExec` ([SUP-D29](#SUP-D29)). `internal/cli/host.go` `hostLaunch`: a
  launch with no doorway plans opens the session before its services and ends it on return; the
  exec path hands the lock to the program beside the menu's and the session homes' leases.
  `run.HostDoorways.Start` still opens the doorway launch's session; `run.go` is untouched.
- **Readers of a host session,** read before relying on it: `runtime.MacosUserSessions` (so
  `yolo ps` on macos-user, `prune.MacosUserJailStarts` and prune's guest pack-tree pass) leaves
  `notch: host` out; `prune.SessionStagingNames` and `prune.LiveSessionKeys` now keep a
  doorway-less `yolo host` launch's workspace names while it runs, the safe direction;
  `collectDeadServicesSessions` collects its dir once the program has exited.
- **Commit:** `feat(host): every yolo host launch holds a session record`

#### 11d — `yolo ps` is one list ([SU-D9](named-setups.md#SU-D9), [SU-D13](named-setups.md#SU-D13))

- **Tests first** (`internal/cli/ps_test.go`, rewritten): a darwin fixture with four stubbed
  sources, Apple Container `unreachable`, lists the other three's rows, prints that source's line
  with `container system start`, and JSON has `complete: false`; a source that sleeps past its
  budget is `timed-out` while the others list (the budget is a dep, milliseconds in the test); an
  unknown-setup row prints `?` and the footnote, `null` in JSON; `YOLO_SETUP` set changes nothing;
  sort order workspace, setup, name; `~` for home; every empty state of
  [§12.3](named-setups.md#123-sort-order-and-the-table); exit 0 with an unreachable source; the
  prune passes every source's answer, so a Mac where both runtimes listed prunes again.
- **Change:** `internal/cli/ps.go` rewritten: one collector per source, run concurrently, each
  under its own 10 s `context.WithTimeout` ([SUP-D24](#SUP-D24)); `psDeps.RunCmd` takes a
  context; rows filled from the launch record, then the runtime, in
  [§12.2](named-setups.md#122-what-a-row-knows-and-where-it-reads-it)'s order, with per-row calls
  under 5 s; `psReport`/`psJail` replaced by the [§12.4](named-setups.md#124-the-json-document)
  document; `psMacosUser` folds into the Seatbelt collector, and a host collector reads
  `runtime.ListSessions` narrowed to `notch: host` ([SUP-D28](#SUP-D28)).
  `internal/runtime/display.go` `RenderPsTable` becomes the five-column table; Podman's listing
  argv gains `{{.StartedAt}}` ([SUP-D25](#SUP-D25)). `internal/cli/commands.go` `runPs` drops
  `listingDetect`; its note becomes the source line. **Close 11a's review gap here:**
  `not-installed` is a `LookPath` miss, so a runtime installed off `yolo ps`'s PATH reads as
  absent and its jails' legacy records can be pruned. 11d (or 11b's technology-named records)
  must not let a PATH miss alone authorize deleting a record.
- **Integration:** `integration/macosuserps_test.go` reads the old JSON and is rewritten to
  [§12.4](named-setups.md#124-the-json-document) in this commit (coordinate with whoever holds
  `integration/macosuser*`); it runs only on a Mac, so its run is a witness for the owner's Mac
  agent.
- **Gates:** `go test -short ./internal/cli/ ./internal/runtime/`; `just check-ci`.
- **Commit:** `feat(ps): yolo ps lists every technology in one list`

#### 11e — `--setup` narrows `yolo ps` ([§12.6](named-setups.md#126-narrowing-with---setup))

- **Tests first:** `--setup podman-self` asks Podman alone and lists only that setup's rows; an
  undefined name is refused with the launch's message, exit 2; a setup this machine cannot build
  lists nothing and says why; unknown-setup rows are counted in the footnote; no prune runs.
- **Change:** `runPs` accepts `--setup`, resolved through `setup.Lookup` with the user's setups
  ([SUP-D27](#SUP-D27)); the collectors are filtered by technology before any is asked.
- **Commit:** `feat(ps): --setup narrows the list to one setup`

#### 11f — `yolo stores` is one inventory ([SU-D14](named-setups.md#SU-D14))

- **Tests first** (`internal/cli/stores/`): a two-technology fixture shows each image section once
  per technology, with its `confinement` and heading; one durable row per project across sources;
  a liveness judgment declines, naming the source, when a container source is not `listed`; JSON
  has `schema`, `sources` and `complete` and no `runtime`; `--setup` keeps the machine-wide rows
  and only that technology's sections.
- **Change:** `stores.Options.DetectRuntime` is replaced by 11d's source discovery, passed in as
  answers so `stores` execs nothing new; `Inventory` runs `imageStores`, `scratchStores`,
  `toolDiskStores` and `nixStores` once per `listed` source; `durableStores` merges projects
  across sources; `onMacosUserContainerPass` generalizes to naming the row's own technology's
  prune (`YOLO_SETUP=<setup> yolo prune --apply`); the union of `prune.LiveYoloContainers` over
  sources, and its decline, live in the caller. `absentruntime.go` is then `prune`'s alone.
- **Integration:** `integration/macosuserstores_test.go` reads only `stores[]`, which keeps its
  shape; it reruns on the Mac witness.
- **Commit:** `feat(stores): yolo stores inventories every technology once`

**Fences, every sub-slice:** `internal/callbroker`; `internal/cli/run` beyond the call sites 11b
and 11c name; `integration/macosuser*` beyond the two rewrites named. **The Mac "what done looks
like" bullet** of [§11](named-setups.md#11-what-done-looks-like) is a witness for the owner's Mac
agent after 11d.

## Ships with

- **Tests by level.** Unit: every case under *Tests first*. Integration, the ones that would catch
  this breaking end to end: a Linux `podman-self` launch with `YOLO_SETUP` and with `--setup`
  (`integration/isolation_test.go` rewritten, slice 6a); `TestMacosUserSeatbeltSelfLaunchesTheSandbox`
  (4a); the footer test (9); `TestAppleContainerExplicitHostModeKeepsPublishedPorts` rewritten
  to the refusal (4b); a new `integration/setups_test.go` (6a): two setups' homes exist after
  `--setup podman-self` and `yolo host --setup host-self` in one workspace, and `yolo setup list`
  (6b) shows both.
- **Tests that break and should** (rewritten, never repaired): listed per slice; the classes are
  the notch tests (`notchgate_test.go`, `guestnotch_test.go`, `confinementruntime_test.go`,
  `notchnames_test.go`, `frontdoornotch_test.go`), the `.yolo/home` path assertions, the
  `YOLO_RUNTIME` message assertions (`imageskew_parse_test.go`, `macosuserpatchedfork_test.go:218`,
  `macosuserpatchedext_test.go:342`, `archivedeliveryworkflow_test.go:148`,
  `macosuserpackinstalls_test.go:399-402`), the census cells for `network.mode`, the verb-name
  assertions, and `TestMacosLaunchPassesSelfModeToTheBackend` (reads `macos_user_mode`; becomes
  "passes the setup's run_as").
- **Docs that describe the old thing:** slice 10's list, plus the macos-user plan's own
  references to `macos_user_mode` and `YOLO_RUNTIME=container` in [MMP-D3](macos-user-model-plan.md#MMP-D3),
  [MMP-D32](macos-user-model-plan.md#MMP-D32), slice 11 and W2: a one-paragraph note at the top
  of that plan's *Ships with* saying the setting is now `run_as` of a setup and the Apple
  Container refusal became adoption (slice 6a's commit; do not rewrite that plan's history).
- **Surfaces that are neither:** config keys `setup`, `setups.<name>.confinement`,
  `setups.<name>.run_as` (defaults `podman`, `self`); env `YOLO_SETUP`; flag `--setup`; verbs
  `yolo setup list|show|new`, `yolo yolo-account create|remove|share|unshare`; refusal texts
  ([§6](named-setups.md#6-config-by-example), [§10](named-setups.md#10-what-is-removed)); the
  session marker `YOLO_SETUP` replacing `YOLO_CONFINEMENT`; the launch log's `[<setup>]` prefix;
  `config_ref.txt` (regenerated by hand; `TestConfigRefDocumentsEveryLiveKey` enforces);
  `Justfile`'s `YOLO_LOAD_RUNTIME`; the four workflows' env; the `settings-per-setup.md` census
  columns.
- **Observability with behavior:** `yolo setup list` (which, running, selected, why);
  `yolo describe --setup`; the first launch line; `launch.log` naming the setup on every line.
- **Cheap, and theirs:** the exact phrasing of `Describe()`; the order of `yolo setup list`
  columns; whether `Buildable`'s `hasBinary` is `exec.LookPath` or the existing
  `runtimeIsConnectable` (`preflight.go:211`) — advice: `LookPath` for the model, the connectivity
  gate stays where it is; the advice map's shape in `readablevalidate.go`; the `legacyhome.go`
  prompt wording beyond the `mv` line.
- **Stop and ask:** nothing in this plan is left to the owner (see [Blockers](#blockers)); a
  slice that needs a behavior the design does not fix stops and reports.

## Don't

- Don't make `Default(goos)` probe for an installed runtime: [SU-D2](named-setups.md#SU-D2) rules
  one named default per OS, and the probe loop at `preflight.go:195-200` is deleted on purpose.
- Don't let `confinement` imply `run_as` or the reverse, not even as a "sensible default":
  [OQ-SU1](named-setups.md#OQ-SU1).
- Don't flip the Mac default to `seatbelt-self`: [OQ-SU4](named-setups.md#OQ-SU4) ties it to the
  owner's call with the macos-user plan's slice 11.
- Don't delete `seatbeltProfile` (`internal/macosuser/seatbelt.go:162`): the macos-user plan's
  [slice 13](macos-user-model-plan.md#slice-13--delete-what-nothing-uses-after-12-macos-ci) owns it.
- Don't alias an old spelling, not even for one release ([MT-R3](named-setups.md#decision-ledger)).
- Don't change `runtime.FromResolved`/`FromWorkspace` or key approvals by the jail name
  ([SUP-D2](#SUP-D2)).
- Don't add a `yolo setup` sub-verb that edits a built-in; `yolo setup new --from` is the only writer.
- Don't accept `--at host` as a quiet alias of `--setup host-self`: it is refused naming the
  replacement ([§10](named-setups.md#10-what-is-removed)).
- Don't remove `macos_user_shared_tmp`: *"Out for now"* ([§7](named-setups.md#7-what-else-goes-in-and-what-stays-out)).
- Don't build `landlock-*`, the Linux yolo account, or a container as the yolo account: each
  refuses naming the nearest built setup ([§11](named-setups.md#11-what-done-looks-like)).

## Mac checks

Nothing in a Linux jail can verify darwin; the owner's Mac agent runs these and pastes the output
into the slice's report (see the macos-user plan's
[Mac checks](macos-user-model-plan.md#mac-checks) for the shape). Each names the slice it gates.

- **W-SU1 (combined 6a+6b candidate, gates both primary landings):** in one fresh workspace under `/Users/Shared/yolo/a`,
  `yolo --setup seatbelt-self -- bash` in terminal 1 and `yolo --setup seatbelt-yolo-account --
  bash` in terminal 2. Expected: both run; `echo $HOME` prints `…/a/.yolo/setups/seatbelt-self/home`
  and `…/a/.yolo/setups/seatbelt-yolo-account/home`; `id -un` prints your login in 1 and
  `_yolojail` in 2; `yolo setup list` from a third terminal marks both `running`; `yolo stop`
  prints two `stopped <setup>` lines and both shells end; `YOLO_TEST_MACOS_USER=1 go test
  -count=1 -timeout 0 -run '^TestMacosUser' ./integration` passes with `YOLO_SETUP` set by the
  harness (no `YOLO_RUNTIME` anywhere in the environment: `env | grep YOLO_RUNTIME` prints nothing).
- **W-SU2 (slice 4b):** a workspace whose `yolo-jail.toml` holds `[network]\nmode = "host"`:
  `yolo --setup apple-container-self -- true` refuses with the
  [§6](named-setups.md#6-config-by-example) sentence ending in `yolo --setup podman-self -- claude`
  and `yolo config unset network.mode --project`; with `mode = "bridge"`, `yolo --setup
  seatbelt-self -- true` refuses with its sentence; `yolo check` in each prints the same line once.
  Then, on the same Mac: with no `mode` written, `yolo --setup apple-container-self --network host
  -- true` refuses naming `--network host` and "launch without --network host" (no `unset`);
  `yolo --setup seatbelt-self -- true` and `yolo --setup apple-container-self -- true` both run;
  with `mode = "host"` written in `~/.config/yolo-jail/config.toml` instead, the Apple Container
  refusal ends in `yolo config unset network.mode --user`; and `YOLO_TEST_APPLE_CONTAINER=1 go
  test -count=1 -timeout 0 -run '^TestAppleContainerExplicitHostModeIsRefused$' ./integration`
  fails nowhere in its refusal half (the port half is an experiment, so either answer passes).
  This witness is the only evidence for slice 4b's behavior on a real network stack: a nested
  jail is blind to network-mode changes (AGENTS.md, Testing), and on Linux every Mac setup is
  refused by the setup gate before the network refusal runs.
- **W-SU3 (slice 8, gates its landing):** `yolo host --setup host-yolo-account -- bash` in a
  workspace under `/Users/Shared/yolo/b`. Expected: the first yolo line names
  `host-yolo-account (no walls, as the yolo account; shares the Mac's network)`; `id -un` prints
  `_yolojail`; `ps -o command= -p $$` and `ps -ef | grep sandbox-exec` show no `sandbox-exec` for
  this session; `echo $HOME` prints `…/b/.yolo/setups/host-yolo-account/home`; `touch
  ~/Desktop/x` in your real home fails (the account cannot write it); `log show --last 2m
  --predicate 'sender == "Sandbox"'` shows no denial from this session's processes; `claude -p
  'Reply with the single word: ok'` answers. On a Mac without the account, the launch refuses
  with *"Create it once (it asks for your admin password): `yolo yolo-account create`"*.
- **W-SU4 (slice 6a; revised 2026-11-09 to follow [SUP-D9](#SUP-D9), [SUP-D49](#SUP-D49)):** a
  workspace last used by Apple Container before this change (its `.yolo/home/.claude.json`
  present). Negative half first, on copies of that workspace: (a) with a jail of the project
  running (`yolo --setup seatbelt-self -- bash` in another terminal), `yolo --setup
  apple-container-self -- true` asks nothing, prints one line saying the home moves once no jail
  of the project runs and naming `yolo stop`, and `.yolo/home/.claude.json` is still there; (b)
  with no jail running and `.yolo/home` replaced by a symbolic link to that directory, the same
  launch asks nothing, prints one line saying the link was left in place with the command to run,
  and the link and what it points at are unchanged. Then the move: with no jail of the project
  running and `.yolo/home` a real directory, `yolo --setup apple-container-self -- true` asks
  nothing and prints one informational line naming both paths (`Moved this project's home … to
  …`); afterwards `ls .yolo/home` reports no such file and
  `ls .yolo/setups/apple-container-self/home/.claude.json` exists. Then
  `YOLO_SETUP=apple-container-self go test -count=1 -timeout 0 -run '^TestAppleContainer'
  ./integration` passes.
- **W-SU12 (slice 6a-iii, gates its landing):** first, with the installed yolo from before 6a-iii,
  start `yolo --setup apple-container-self -- bash` in a workspace under `/Users/Shared/yolo/c`,
  and leave it running. Then, with the 6a-iii build, run `yolo --setup apple-container-self --
  true` in the same workspace. Expected: it refuses with *"Refusing to launch: a jail an earlier
  yolo started for this workspace (yolo-c-…) is still running"*, and the next line names `yolo
  stop` and `container stop yolo-c-…`. `ls .yolo/setups` shows no new directory. `yolo stop` prints
  *"Stopping the jail an earlier yolo started for this workspace"*, and the first shell ends. The
  launch then runs, and `container ls` names the jail `yolo-c-…-apple-container-self`. Repeat the
  same steps with `seatbelt-self` in place of `apple-container-self`, under
  `/Users/Shared/yolo/d`. The refusal there names `yolo stop` alone. Last, with the Apple
  Container jail stopped, run `yolo prune` and `container volume ls`. The tool disk
  `yolo-c-…-apple-container-self.mise` stays, and prune does not offer to delete it.
- **W-SU5 (slice 7):** `yolo macos-setup` prints `yolo macos-setup is now yolo yolo-account
  create` and exits non-zero; `yolo yolo-account share <dir>` does what `macos-fix-permissions`
  did (the ACL on `<dir>` lists `_yolojail`). Also, on a Mac where the yolo account exists:
  `yolo yolo-account create` prints `The yolo account _yolojail already exists.` and exits 0;
  `yolo yolo-account unshare <dir>` exits 0 and `ls -lde <dir>` then lists no `_yolojail` entry;
  `yolo yolo-account create --help` prints `Usage: yolo yolo-account create` and runs no sudo
  (no password prompt). `remove` is not run here: it deletes the account the other witnesses use.
  *Run 2026-11-04 at `3ccc28def`* ([lab report](../lab-reports/2026-11-03-mac-slice-12a-witness-w1.md#w-su5-one-expectation-does-not-match)):
  all as expected except `create` with the account present. It asks sudo for a password to
  re-check `/etc/sudoers.d/yolo-jail`, and without one exits 1, exactly as the old
  `yolo macos-setup` does.
- **W-SU11 (slice 11d):** on a Mac with Podman and Apple Container both installed, one jail
  running in each: `yolo ps` lists both rows with their setups, `yolo ps --format json` has
  `"complete": true`; then `container system stop` and `yolo ps` again: the Podman row is listed,
  the Apple Container line names `container system start`, JSON has `"complete": false`, and
  `ls ~/.local/share/yolo-jail/containers/` still holds the Apple Container jail's record. Also
  paste `container inspect <name> | jq '.[0].status'` from the running jail, which settles how
  `startedDate` is encoded ([SUP-D25](#SUP-D25)).
  *`startedDate` measured 2026-11-06* ([run report §3](../lab-reports/2026-11-06-mac-ci-triage-witnesses-run.md#3-apple-containers-starteddate-for-w-su11)): `status` is an object, not a string, with the keys `networks`, `startedDate` and `state`; `container ls --all --format json` carries the same object. `startedDate` is an RFC 3339 timestamp in UTC with second precision, e.g. `"2026-11-06T19:57:12Z"`, and it matched the wall clock at start. The rest of W-SU11 waits on 11d.

## Blockers

[OQ-SU5](named-setups.md#OQ-SU5) is ruled
([SUP-D9](#SUP-D9)), so no owner question blocks the legacy-home move.
[OQ-SU7](named-setups.md#OQ-SU7) is ruled ([SUP-D30](#SUP-D30)), so no custody mechanism and no
rootless witness for one block 6a-ii; its entry conditions are [SUP-D21](#SUP-D21) and
[SUP-D31](#SUP-D31)'s yolo-account owner case, both ruled. Slices 6a/6b still need independent review and
**W-SU1 and W-SU4** on the combined candidate before primary landing: W-SU1 unchanged, W-SU4 as
revised to follow [SUP-D9](#SUP-D9) ([SUP-D49](#SUP-D49)). 6a-iii also needs W-SU12. Slice 8 separately needs W-SU3. No native Mac or
rootless-host witness is established by the private Linux fixture.
