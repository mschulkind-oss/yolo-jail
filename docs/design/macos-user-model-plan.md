---
title: "Plan: rebuild macos-user on one deny-by-default profile, run as you by default"
status: accepted
stage: DECIDED
next: "Slices 11, 12b and 13. The account mode's shared /tmp is closed (MMP-D29, MMP-D36) and waits on Mac check W-TMP1; OQ-MM10 was ruled 2026-11-03"
depends-on:
  - macos-user-model.md
tags: [macos-user, seatbelt, plan, safehouse, home, parity]
vantage:
  status-chip: true
---

# Plan: rebuild macos-user on one deny-by-default profile, run as you by default

**Status:** 2026-11-12. Slices 1 to 10 and 12a are built and in main. W1 ran on a Mac and
failed; its fixes were re-run by witness W1b on 2026-11-03, and W1c, the one W1b left failing (an
unchanged relaunch keeps its delivered files), passed on 2026-11-04
([the lab report](../lab-reports/2026-11-03-mac-slice-12a-witness-w1.md#13-2026-11-04-w1c-w-su5-claude--p-on-bedrock-and-just-done);
[Mac checks](#mac-checks)). The account mode's shared `/tmp` is now closed, with the same hatch as
self mode ([MMP-D29](#MMP-D29), [MMP-D36](#MMP-D36)); Mac check [W-TMP1](#W-TMP1) has not run yet.
Next are slices 11 ([OQ-MM10](macos-user-model.md#OQ-MM10), ruled 2026-11-03), 12b and 13. Loopback and the shared `/tmp` are ruled and
built ([MMP-D14](#MMP-D14), [MMP-D18](#MMP-D18)); Homebrew's visibility is the one profile
parameter still at the prototype's value ([Blockers](#blockers)). With 12a the production default,
the second account, runs under the deny-by-default profile with a home per workspace, and a second
workspace launches beside a running session instead of being refused ([MMP-D25](#MMP-D25)). The
account mode's temp dirs were ruled by the owner on 2026-11-03 ([MMP-D29](#MMP-D29)), and the shared
`/tmp` closed for it once W1b and W1c passed.

**Design:** [macos-user's model](macos-user-model.md#7-the-ruled-model-two-modes-one-profile). The
measured starting point is the
[prototype profile](../plans/runbooks/macos-user-model-prototype/profile.sb) and its
[runbook](../plans/runbooks/macos-user-model-prototype/README.md).

**Supersedes:** the concurrent-workspaces design and its plan, retired on 2026-11-03
([MMP-D28](#MMP-D28)); git history has both. Their rulings are carried in
[the model's ledger](macos-user-model.md#decision-ledger) ([OQ-MCW2](macos-user-model.md#decision-ledger) to [OQ-MCW4](macos-user-model.md#decision-ledger)), and the two
account-mode mechanisms this plan reuses are rows below under their own ids: the machine root
([MCWP-D2](#MCWP-D2)) and the guest scope check ([MCWP-D10](#MCWP-D10)). Its machine-tier lock is
not reused ([MMP-D27](#MMP-D27)), and its HOME geometry (`.yolo/native-home`) is replaced by
[MMP-D5](#MMP-D5).

**Precedence:** the design wins on behavior. The tree wins on fact when the drift is cosmetic (a
moved line, or a renamed helper with the same role): follow the tree and say so in the report and
the commit. This plan is advice and is the first thing to be wrong. Never twist the code to match
it. **Stop and report, and do not commit**, when:

- a task's red test does not fail the way the task predicts;
- a named file is dirty with someone else's work;
- a named symbol is gone;
- the fix needs a file outside the task's fence;
- a gate fails outside the task's scope.

## Settled — do not re-open

Quoted from the design's [§7](macos-user-model.md#7-the-ruled-model-two-modes-one-profile) and
[Decision Ledger](macos-user-model.md#decision-ledger):

- *"Every macos-user launch runs under a deny-by-default Seatbelt profile covering files, system
  services, signals and process inspection … Which user runs it is a parameter of the profile, not
  a second profile."*
- *"Default mode: the agent runs as you. No account, admin setup, access grants or projects
  folder. A workspace may be anywhere, including your home folder."*
- *"Optional mode: the second account, off by default, built from day one. The same profile run as
  `_yolojail`. The mode alone brings the account's setup and switching, and the access grants."*
- *"Shared by both modes: a per-launch `HOME` under the workspace's `.yolo/home`, so two workspaces
  run at once in either mode without a shared account home; mounts at their original path with a
  context-dir link; the host keychain reached only through the loophole."* The keychain ruling is
  [OQ-KC4](keychain-from-a-jail.md#8-decision-ledger): *"always the host's keychain"*. The
  sandbox never reaches it.
- *"install trees such as `/opt/homebrew`, never a parent such as `/opt`"*
  ([OQ-MM2](macos-user-model.md#OQ-MM2)).
- Loopback: denied by default, with yolo's own service ports allowed. *"A workspace may allow more
  ports in its config; a change to that list goes through the same config-change approval"*
  ([OQ-MM3](macos-user-model.md#OQ-MM3)). That ruling is re-raised by the first Mac run. See
  [Blockers](#blockers).
- *"No transition for Macs already set up for the second account. Launches move to the default
  mode, and nothing migrates, cleans up or announces the old account"*
  ([OQ-MM4](macos-user-model.md#OQ-MM4)).
- *"No per-language home grants … Package caches are shared with your real caches by the general
  rule for every backend ([OQ-BF11](../reference/disk-footprint.md#OQ-BF11)) … their config files,
  which hold registry tokens, never are."*
- *"Selecting the mode is a user-scope setting; a workspace config cannot choose it. Its name and
  spelling are the plan's to choose."*
- *"What done looks like: with no setting, `yolo -- claude` in `~/code/alpha` launches with no admin
  step, and the agent cannot read `/opt/src/clientX`, `~/.ssh` or the keychain; with the optional
  mode set on a Mac where setup ran, the same launch runs as `_yolojail` with the same denies; two
  workspaces launch at once in either mode."*
- The threat model is [the misguided-agent principle](../reference/misguided-agent-principle.md):
  keep credentials and wider access out of an eager agent's reach. Do not add defenses aimed at an
  adversary.

## Implementation decisions

These are mechanism choices made under the rulings above, not owner rulings. The ids are
*coined here*, so commits and code comments can cite them.

| ID *(coined here)* | Decision | Why |
| :--- | :--- | :--- |
| <a id="MMP-D1"></a>MMP-D1 | **The profile is rendered as literal text per session**, by a new `DenyDefaultProfile(ProfileParams) string` in a new `internal/macosuser/seatbeltdeny.go`. Values are escaped with `sbplStr`, not passed as `sandbox-exec -D` parameters. The old `seatbeltProfile` stays untouched until slice 13. | Every launch already writes a 0444 per-session file (`SessionProfilePath`), and every pinned test reads text. The prototype's `-D` slots (eight ancestors, four ports) were a script's limit, not a design. |
| <a id="MMP-D2"></a>MMP-D2 | **`macosuser.Mode`**: `ModeAccount` is the zero value, `ModeSelf` the other. It is carried on `PlanSession.Mode` beside a new `PlanSession.HostHome` (the invoking user's resolved home). `internal/cli/commands.go` sets both explicitly on every launch. | With account as the zero value, the hundreds of existing plan tests keep building today's plan unchanged. The orchestrator never relies on the zero value. |
| <a id="MMP-D3"></a>MMP-D3 | **The setting is `"macos_user_mode": "self" \| "account"`**, top level and user scope only. It is read through `UserScopeConfigOrEmpty`, so the per-workspace user layer is not consulted. An absent key means `account` until slice 11, then `self`. | The values match the prototype's `--mode self\|account`. One machine-wide mode keeps a second mental model out of each workspace (design [§4](macos-user-model.md#4-what-the-second-account-buys-and-what-it-costs)). |
| <a id="MMP-D4"></a>MMP-D4 | **One crossing value, `RunAs{Mode}`**, with `Argv(setHome bool, argv ...string)`. Account mode returns `RunAsSandboxArgv` byte for byte. Self mode returns `/usr/bin/env argv…`, with no sudo. Every caller of `RunAsSandboxArgv` goes through it. | One switch per launch, so no consumer can be left crossing into an account that self mode does not have. |
| <a id="MMP-D5"></a>MMP-D5 | **HOME is `paths.WorkspaceHomeState(ws)`, `<ws>/.yolo/home` itself, in both modes.** Agent state sits there as real dot-named directories (`.claude`, `.pi/agent`, `.config`, `.local`, `.npm-global`, `go`, `.yolo/bin`), which is the layout Apple Container already binds at `/home/agent`. There are no links from HOME into the sidecar. Existing undotted sidecar dirs (`claude/` and the rest, written by today's account layout or by podman) are left in place and unused. | This is design [§3](macos-user-model.md#3-podman-mapped-onto-the-safehouse-model) taken literally. The retired concurrent-workspaces plan rejected `.yolo/home` only because of the account home's name mapping, which this model removes. Leaving the old dirs unused is [OQ-MM4](macos-user-model.md#OQ-MM4). |
| <a id="MMP-D6"></a>MMP-D6 | **The machine root.** Self mode uses a new `paths.MacosUserMachineRoot()` = `<GlobalStorage>/macos-user`, holding `.cache`, `Library/Caches` and `.yolo/mise`. HOME links `.cache` and `Library/Caches` into it, and `MISE_DATA_DIR` names `<root>/.yolo/mise` absolutely. Selected `scope: machine` pack dirs link to `paths.GlobalHome()/<dir>`, the same store podman binds. Account mode keeps `SandboxHome()` for all of these (MCWP-D2). | `GlobalCache()` and `GlobalMise()` hold Linux artifacts on a Mac that also runs podman or Apple Container, so darwin binaries must not land there. The credential dirs are OS-neutral, so sharing them with podman keeps one login per machine, as every backend does today. Cheap to reverse. |
| <a id="MMP-D7"></a>MMP-D7 | **`YOLO_DARWIN_REAL_HOME`** joins the protected env pairs (`sandboxEnvPairs`, `protectedSandboxEnvKeys` in `envfile.go`). Self mode sets it to the invoking user's home, account mode to `SandboxHome()`. One `paths` helper, called from `cli.Main` and `cmd/yolo-jaild`, judges guest workspace scope against it instead of `$HOME` (MCWP-D10's shape). | With HOME inside the workspace, every guest verb that creates `.yolo` would otherwise see "the workspace contains your home" and refuse. Do not use `os/user`: yolo is built without cgo, and without cgo `os/user` on darwin answers from `$HOME`. |
| <a id="MMP-D8"></a>MMP-D8 | **The staging root stays `/Users/Shared/yolo-staged` in both modes.** When it is absent, a self-mode launch creates it through `ensureHostOwnedRoot`'s `mkdir` branch, which needs no root because `/Users/Shared` is 1777. A root that is foreign, a link, or unsafe stays a refusal. | No refactor of `StagingRoot()`'s many callers. The profile grants it read only, so the sandbox cannot write it even as you. |
| <a id="MMP-D9"></a>MMP-D9 | **The darwin bootstrap runs under the session profile** (`sandbox-exec -f <profile>`) in self mode, and in account mode from slice 12. | Run unconfined as you, a link the agent planted in its own writable HOME would carry a bootstrap write into your real home. Confined, the bootstrap can write only what the agent can. |
| <a id="MMP-D10"></a>MMP-D10 | **Superseded by [MMP-D14](#MMP-D14)** ([`OQ-MM7`](./macos-user-model.md#OQ-MM7) allows the whole loopback); its code is deleted. **The loopback allow list** had three parts, with no new config key. The first is each `YOLO_SERVICE_*_ENDPOINT` file's port, read with `svcendpoint.Read` after the host services start (they start in `run.go` before `RunMacosUser`). The second is the served addresses' launcher-reserved ports. The third is the JAIL port of each `network.forward_host_ports` entry. It is rendered only when the policy denies loopback. | `forward_host_ports` already means "host loopback ports this jail may reach", and every workspace-config change already goes through the approval diff (`CheckConfigChanges`). A second key would be a second spelling of one fact. |
| <a id="MMP-D11"></a>MMP-D11 | **The overlay install keeps a record**, `<sidecar>/.yolo-home-overlay-installed.json`, written after every install. A recorded destination that the current list no longer names, or covers, is removed. The removal goes through the same roots and `overlayLinks.route` as an install, and a recorded entry that fails `normalizeOverlayDests` is ignored. | It mirrors the cache-relocation manifest (`writeCacheRelocationManifest`). It lives in the sidecar because today's HOME is shared between workspaces. Under [MMP-D5](#MMP-D5) the sidecar is HOME, so it is the same file. |
| <a id="MMP-D12"></a>MMP-D12 | **Package-cache aliasing in self mode.** A HOME link at the path the sandbox's own copy of the tool uses on macOS points at the host's real cache, with a profile read/write allow on the resolved target (new id `cache-alias-allow`). `hostcas.Store` gains `DarwinHost` and `DarwinRel`. The `decide` gates admit `macos-user` only when `Facts.MacosUserSelf` is set. | This is the link-plus-allow shape that `cache_relocations` already uses on this backend. The ownership gate is satisfied by construction, because the agent is you ([OQ-BF11](../reference/disk-footprint.md#OQ-BF11)). |
| <a id="MMP-D13"></a>MMP-D13 | **The pending rulings are `ProfileParams` fields**: `Tmp TmpPolicy` (`TmpShared` or `TmpPrivate`) and `HostSoftware []string`. Their production values are named constants in `internal/macosuser/seatbeltdeny.go` (`productionTmp`, `productionHostSoftware`), set to the prototype's choices until slice 11. The third, `Loopback`, left with its ruling ([MMP-D14](#MMP-D14)). `productionTmp` is now `TmpPrivate` ([OQ-MM8](macos-user-model.md#OQ-MM8), [MMP-D18](#MMP-D18)), and `TmpPolicy` governs `/tmp` alone; `TmpShared` remains as the hatch's value. | Each ruling becomes a one-constant edit, and the tests cover both values of each. |
| <a id="MMP-D14"></a>MMP-D14 | **The whole loopback is allowed** ([OQ-MM7](macos-user-model.md#OQ-MM7), ruled 2026-10-23), in every mode that renders the deny-by-default profile: one block, id `loopback-allow`, allows `network-outbound` to `(remote ip "localhost:*")` and `network-bind network-inbound` on `(local ip "localhost:*")`. The loopback deny, the per-port allow, `LoopbackPolicy`, `loopbackports.go`, `JailDaemons.Served`, the run pipeline's served-address handoff and the profile verb's `--loopback` and `--allow-port` flags are deleted. A self-mode launch prints one disclosure line saying the loopback is open. Podman's served-address and port-forward plumbing is untouched. | A ruling with one value needs no policy type, and a second path kept "in case" is what the escape-hatch rule forbids. An explicit allow states the decision in the profile even though `network-allow` already admits any IP remote. |
| <a id="MMP-D15"></a>MMP-D15 | **This session's host-services dir gets its own read-only grant, by exact path, and stays in `/private/tmp`.** `ProfileParams.ServicesDirs` renders `services-dir-read-allow`: `file-read-metadata` on the parent (a stat, never a listing) and `file-read*` on the dir. `services-lock-deny` then denies `.session.lock` and `.session.lock.pending`, because a read-only descriptor can `flock` and would hold the session alive. The dirs come from the session env's and the jail-daemon env's `YOLO_SERVICE_*_ENDPOINT` values, and only a name `paths.IsHostServicesSessionDirName` accepts is granted. The cleanup glob is unchanged. | Moving the dir would touch prune, `yolo ps`, the keeper record, the session listing and the front-socket hashing, for no gain over an exact grant. Under the sandbox's writable HOME or TMPDIR the agent could forge its own endpoints. Reading the dirs from the env covers a keeper join and a dry run's placeholder with no new crossing. [MMP-D18](#MMP-D18) has since closed the shared `/tmp`, so the grant is what remains. |
| <a id="MMP-D16"></a>MMP-D16 | **Unix sockets bind and connect only under the sandbox's own writable roots**: the workspace, HOME and the sandbox TMPDIR, id `unix-socket-allow`, as `(local unix-socket (subpath …))` for `network-bind` and `(remote unix-socket (subpath …))` for `network-outbound`. That is Codex's form; Safehouse has no unix-socket allow. There is no `network-inbound` for unix sockets. The closing launchd-listener, ssh-agent and container-socket denies follow it and still win. | tmux, overmind and Python's multiprocessing put their sockets under TMPDIR or the workspace. A connect is `network-outbound`, so without this the agent cannot reach a socket it made itself. |
| <a id="MMP-D17"></a>MMP-D17 | **A self-mode session gets its own TMPDIR**, `<DARWIN_USER_TEMP_DIR>/yolo-<first 8 of the session id>` (`SessionTmpDir`, new `internal/macosuser/sessiontmp.go`). A host stage step makes it with `mkdir -m 0700`, without `-p`, so an existing path fails the stage. Teardown removes it with `rm -rf`, and macOS sweeps whatever a killed launch leaves in that dir. The session env file sets `TMPDIR`, `TMUX_TMPDIR=$TMPDIR` and `TMPPREFIX=$TMPDIR/zsh`. A TMPDIR an env layer composed is kept, and tmux and zsh follow it. `OVERMIND_SOCKET` was then set only on containers; [MMP-D18](#MMP-D18) adds `$TMPDIR/overmind.sock` in self mode. Account mode is unchanged. | Before this the launch's `env -i` left TMPDIR unset, so tools fell back to the shared `/tmp`. A short path keeps tmux's and Python's sockets under the 104-byte `sun_path` cap, and Python falls back to `/tmp` past it. The dir is the profile's `SandboxTmp`, so it is writable and a unix-socket root. |
| <a id="MMP-D18"></a>MMP-D18 | **The shared `/tmp` is closed in the deny-by-default profile, with a user-scope hatch** ([OQ-MM8](macos-user-model.md#OQ-MM8), ruled 2026-10-23 as a trial). `productionTmp = TmpPrivate`: no rule grants `/private/tmp` or `/tmp`, so `(deny default)` closes both, read and write; there is no named `/tmp` deny, which would also beat the services-dir grant ([MMP-D15](#MMP-D15)). The running user's `DARWIN_USER_TEMP_DIR` gets its own always-on grant, id `user-tmp-allow`, and the session TMPDIR ([MMP-D17](#MMP-D17)) lives under it at about 68 bytes (measured: the physical user temp dir is about 54), not in `/tmp`. The hatch is the boolean `macos_user_shared_tmp`, read only from the user config (`config.MacosUserSharedTmp`; a workspace value is refused by `validateMacosUserSharedTmp`) and handed from one read to the profile (`macosuser.Options.SharedTmp` to `PlanSession.SharedTmp`, which selects `TmpShared`, id `shared-tmp-allow`) and to the briefing (`run.Options.MacosUserSharedTmp`). A launch with the hatch prints one disclosure line. A self-mode briefing without the hatch gains one line saying `/tmp` is closed and to use `$TMPDIR` (`macosUserTmpLimit`). The self-mode session env also sets `OVERMIND_SOCKET=$TMPDIR/overmind.sock` unless a layer set it. Account mode still runs the old profile and is unchanged. | The ruling. A user-scope key, like `macos_user_mode`, keeps a cloned workspace from reopening `/tmp` for itself. Seatbelt's refusal is a bare "Operation not permitted", so the briefing line is the agent's only next step. overmind's default socket is in the workspace, but a user who sets it under `/tmp` would now fail. A TMPDIR an env layer composes outside the granted roots is refused ([MMP-D20](#MMP-D20)). |
| <a id="MMP-D20"></a>MMP-D20 | **A TMPDIR the user's env composes outside what the run-as-you profile grants refuses the launch**, naming the fix: remove the override, point it inside the workspace, or, for `/tmp`, set the hatch (`composedTmpdirProblem`, read by `PlanInvariants`). The granted roots are the workspace, the agent's home, the user's `DARWIN_USER_TEMP_DIR` (which holds the session's own) and, with the hatch, `/private/tmp`, compared lexically with `/tmp`, `/var` and `/etc` in their `/private` spelling. One inside a root is kept, and tmux and zsh follow it. | Granting an arbitrary user path would widen the profile from config; refusing names the one-line fix, and every value that works today inside the workspace keeps working. |
| <a id="MMP-D21"></a>MMP-D21 | **Run-as-you captures and builds get a TMPDIR of their own, and the self-mode bootstrap is told the session's.** A capture or a fork/dev-override build sets `TMPDIR=<staging tree>/tmp` (`CaptureStagingTmp`), made with the tree and removed with it, in its env file (the driver, the installer, the build line) and its bootstrap's env, over any composed one; both capture profiles take the hatch (`CaptureOptions.SharedTmp`, read by cli). A self-mode launch's bootstrap gets `TMPDIR` too. | All three run under `env -i`, so with no TMPDIR `go build`, `mktemp -d`, installers and the bootstrap's children fell back to the `/tmp` [MMP-D18](#MMP-D18) closed. The staging tree is already the capture profile's one writable root, and a sibling of the home and out dirs, so nothing in it is captured. |
| <a id="MMP-D19"></a>MMP-D19 | **The per-user cache dir (`<DARWIN_USER_CACHE_DIR>`, the `.../C` beside `.../T`) and `/private/var/tmp` are not granted.** No tool was verified to need either, so both are in [Open items](#open-items) until a Mac run shows one does. | [`OQ-MM8`](./macos-user-model.md#OQ-MM8)'s instruction: grant them only on a verified need. Each grant is one line in `DenyDefaultProfile` when it comes. |
| <a id="MMP-D22"></a>MMP-D22 | **A run-as-you capture or fork build tells its bootstrap your real home, lets it stat that home, and the bootstrap admits the capture's staging home.** `buildCapturePlanAt`'s self branch sets `YOLO_DARWIN_REAL_HOME` (`paths.DarwinRealHomeEnv`) on the bootstrap's `env -i` list and nothing else from a launch's self branch: the machine root and shared store are what the capture profile denies, the mise store is the staging home's, and a capture names no sidecar, so no workspace-home layout reads them. `SeatbeltSelfCaptureProfile` sets the new `ProfileParams.HostHomeStat`, which renders `host-home-stat-allow`: `file-read-metadata` on the real home and each directory above it, before the closing denies. `selfCapturePlanProblems` refuses a plan missing either half. `darwinBootstrapSelfHomeRefusal` admits one more shape: no sidecar, and `HOME` equal to `macosuser.CaptureStagingHome(<workspace>)`, unless that home is or holds your real home or sits in yolo's own directories. | The [2026-10-24 Mac report](../lab-reports/2026-10-24-mac-verification-after-gitbroker.md#1b-why-as-read-from-the-code-not-yet-confirmed-by-a-fix), section 1b. With no variable, `paths.GuestRealHome` answered `""` and the bootstrap took the account mode's identity check, which stats `/Users/_yolojail`. Reading the code confirmed both follow-on refusals the report predicted. The capture profile's granted roots (the staging tree under `/Users/Shared`, the staging root, the user temp dir) are not under the real home, so no ancestor literal reached it and the stat would have been denied. A capture's `HOME` is its staging home, so the launch-only HOME guard would have refused it next. A stat lists nothing and reads nothing below the home. |
| <a id="MMP-D23"></a>MMP-D23 | **Only the diagnostic fields of the two build-decision wires are exempt from the "names the second account" check.** The checks (`selfModeProblems`, `selfCapturePlanProblems`, through `argvWordNamingAccount`) still judge every word of every argv: paths, flags, env pairs, JSON wires and shell bodies. The one exemption: for `YOLO_FORK_BUILDS` (`entrypoint.ForkDelivery`) and `YOLO_PATCHED_TREES` (`entrypoint.TreeDelivery`), each entry's `reason` and `cause` are removed before the check (`diagnosticWireFields`). Every other field, including ones added later, is judged. A value that does not decode is judged whole, so the exemption fails closed. A match is the account name as a whole token (`namesSandboxAccount`): `/Users/_yolojail`, `USER=_yolojail` and `~_yolojail` match, but a workspace called `my_yolojail_fork` does not. The refusal names the offending word. `TestDiagnosticWireFieldsMatchTheEntrypointTypes` pins the exempt names to the types' JSON tags. | The maintainer's ruling (2026-10-24) on [section 1c](../lab-reports/2026-10-24-mac-verification-after-gitbroker.md#1c-a-self-mode-build-failure-now-refuses-the-whole-launch-hatch-included): a failed build's `YOLO_FORK_BUILDS` quoted the bootstrap's stderr, which named `/Users/_yolojail`, so every self-mode launch was refused and `YOLO_ALLOW_MISSING_PROGRAMS=1` could not help. This replaces a first version that judged only "location-shaped" words. Review found it admitted `USER=_yolojail`, a launch body naming the account's home, JSON wires that locate something (`YOLO_MCP_SERVERS`, `YOLO_DARWIN_CACHE_ALIASES`) and `HOME=~_yolojail`; the maintainer ruled for the narrow exemption instead. |
| <a id="MMP-D24"></a>MMP-D24 | **`YOLO_FORK_BUILDS` stays on the bootstrap's argv.** It is not moved to a file. | With [MMP-D23](#MMP-D23) the viability defect is gone either way. The session env file cannot carry it, because the bootstrap's hydrator drops every `YOLO_` name from that file on purpose ([OQ-PD29](program-delivery.md#decision-ledger)). A launcher-owned file would be a second channel in `internal/entrypoint` for one variable. Revisit it if the value's size nears `ARG_MAX`, or if a decision ever carries something that must not show in `ps`. |
| <a id="MMP-D25"></a>MMP-D25 | **Slice 12 moves ahead of slice 11 and splits.** *12a, concurrency in account mode:* account mode takes the self branch of `BuildRunPlanWithStages` for its homes and profile: `LaunchHomes(ModeAccount, ws, _)` returns `Homes{Home: WorkspaceHomeState(ws), Machine: SandboxHome(), Shared: SandboxHome(), Real: SandboxHome()}`; the profile is `DenyDefaultProfile` with those homes; the bootstrap runs confined ([MMP-D9](#MMP-D9)) and is told `DarwinMachineHomeEnv`/`DarwinSharedHomeEnv`/`DarwinRealHomeEnv` as self mode is; `MISE_DATA_DIR` is `SandboxMiseData(SandboxHome())`; the sudo crossing, the grant stage, every account precondition and the `HomeContaining` refusal stay as they are; `HoldAccountHome` is not called in any mode and the `account_home` step goes. *12b, a workspace inside a home in account mode* (ancestor traverse grants, `HomeContaining` stops refusing) stays where the plan had it, after 11. Slice 13's deletions that 12a orphans (`accounthomehold.go`, `Deps.HoldAccountHome`, `PreflightLaunch`'s hold parameter, the `Mirrors` routing, `ContextLinkOwnerEnv` retargeting) land with 12a or the commit after it, not after 13. | Account mode is the default until slice 11's flip ships, so it is what macOS users run today: the users owed concurrency *now* are account-mode users, and gating their fix on the flip would make the ruling true only for people who opt into the run-as-you mode. Reusing the self branch is the smallest change: `DenyDefaultProfile` already renders a Machine root distinct from HOME (self mode's `<GlobalStorage>/macos-user`), and the bootstrap's workspace-home layout (`DeriveDarwinWorkspaceHome`) already runs for a HOME equal to the sidecar. Keeping `HomeContaining` keeps 12a off the access-grant questions that make 12b a Mac question of its own. |
| <a id="MMP-D26"></a>MMP-D26 | **The refusal is rewritten now, and deleted with 12a.** Until 12a lands, the three `accountHome*` texts say what is true: yolo does not yet give the sandbox account a home per workspace, so two workspaces cannot run as it at once, and that is being fixed with no change to the user's config; and they give the no-edit next step: end the other session (`yolo stop` run in that workspace, or quit it) and run `yolo` here again. No config edit, no file name, no runtime or mode switch is offered; the `step` clause (`containerStep`) is dropped from these messages and the `step` parameter from `HoldAccountHome`, `holdAccountHomeIn`, `probeAccountHome`, `Deps.HoldAccountHome` and `PreflightLaunch`. `TestALiveSessionOfAnotherWorkspaceRefusesTheLaunch` and the integration test's `want` list lose `"runtime": "container"` and gain `yolo stop`. | A message is not the fix (the owner, 2026-11-03), so it may not offer the user work; the Happy Path Principle still wants a next step that leads to what they asked for, and ending the other session is the only one that needs no edit and no new runtime. Naming `yolo-jail.jsonc` by hand was already wrong for a TOML workspace. |
| <a id="MMP-D27"></a>MMP-D27 | **No machine-tier lock in 12a.** Account mode on the model shares its machine root (`SandboxHome()`: the mise store, `.cache`, `Library/Caches`, the shared credential dirs) across concurrent workspaces exactly as self mode shares `<GlobalStorage>/macos-user` today, with no lock. The one known cross-workspace write in that root, the alias manifest sweep ([Open items](#open-items)), is self-only today (`cacheAliasProblems` refuses an alias in account mode) and is fixed for both modes in slice 12c: a launch removes from the machine root only alias links *its own* manifest laid and that no other workspace's manifest still names, with the manifests keyed per workspace. The retired plan's machine-tier lock (its MCWP-D8, a 60-second `flock` around the machine dirs) is not reused. | A lock serializes a write that should not be shared; the defect is the single manifest, and the self-mode open item is the same defect. A `flock` that a wedged bootstrap could hold against every other workspace is a second failure mode for no gain. Reversible: if a Mac run shows a concurrent mise-store write corrupt, the lock is one function. |
| <a id="MMP-D28"></a>MMP-D28 | **The concurrent-workspaces design and its plan are retired.** Their three ledger rows ([OQ-MCW2](macos-user-model.md#decision-ledger), [OQ-MCW3](macos-user-model.md#decision-ledger), [OQ-MCW4](macos-user-model.md#decision-ledger)) moved verbatim, with dates, into [the model doc's ledger](macos-user-model.md#decision-ledger) as the account mode's concurrency rulings; MCWP-D2 and MCWP-D10 are rows in this table; every inbound link ([`macos-user-home-tiers.md`](../reference/macos-user-home-tiers.md), [`macos-user-claude-refresh.md`](macos-user-claude-refresh.md), the roadmap's entry, the model doc's [§2](macos-user-model.md#2-the-three-models) table, this plan's header, the 2026-10-2x lab reports) is repointed at the model doc's ledger or [§7](macos-user-model.md#7-the-ruled-model-two-modes-one-profile); both files are deleted in the same commit. | The concurrent doc said `accepted` and *"Do not build from this doc"* in the same header, which is the status-line drift the design-doc skill names; its rulings are not dead, its *design* is (the model removed the shared account home it worked around). Keeping a superseded doc for its ledger is what the maintainer's delete-after-repointing rule is against; the rows survive in the owning ledger and git keeps the argument. |
| <a id="MMP-D29"></a>MMP-D29 | **The account mode keeps its temp dirs under the deny-by-default profile, for now.** *Owner's ruling, 2026-11-03, on the implementation decision taken building 12a:* keep it as built, and close the shared `/tmp` for the account mode once [W1b](#mac-checks) passes, as [OQ-MM8](macos-user-model.md#OQ-MM8)'s trial closed it for self mode. Owner: *"agree with /tmp ruling. you acknowledge this risk by using a separate user as your containment."* The account mode keeps `TmpShared` (the shared `/tmp` open, as it was under the `(allow default)` profile), gets no session `TMPDIR` ([MMP-D17](#MMP-D17) stays self-only), and is granted the ACCOUNT's own `DARWIN_USER_TEMP_DIR`, asked through the sudo rule (`sudo -n --user=_yolojail /usr/bin/env /usr/bin/getconf DARWIN_USER_TEMP_DIR`, `AccountUserTempDirArgv`) and resolved to its `/private/var` spelling; the profile's run-as user is the account. The hatch `macos_user_shared_tmp` still means nothing in the account mode. **Built 2026-11-12, the condition met** (W1b ran 2026-11-03, W1c passed 2026-11-04): `denyDefaultProfileParams` no longer sets `TmpShared` for the account mode, so its profile takes the production `TmpPrivate` and renders no `shared-tmp-allow`; the account's `DARWIN_USER_TEMP_DIR` grant (`user-tmp-allow`) and its `TMPDIR` ([MMP-D30](#MMP-D30)) stay, and it still gets no session `TMPDIR`. The hatch now opens the shared `/tmp` in the account mode too ([MMP-D36](#MMP-D36)). | 12a moves the account mode, which is what macOS users run today until slice 11's flip ships, onto a profile that would otherwise close every temp dir its tools used yesterday. The account's temp dir is not yours, so self mode's `getconf`, run as you, names the wrong one. Closing `/tmp` for the account mode is [OQ-MM8](macos-user-model.md#OQ-MM8)'s trial extended to a mode it was not ruled for, so it is left to a ruling rather than taken here. |
| <a id="MMP-D30"></a>MMP-D30 | **The account mode's `TMPDIR` is the account's own temp dir.** *Implementation decision, from W1's run of 2026-11-03.* The sandbox environment is a closed list, and only self mode set `TMPDIR` ([MMP-D17](#MMP-D17)), so the account mode ran with none. It now sets `TMPDIR` to the resolved `DarwinUserTempDir` that [MMP-D29](#MMP-D29) already grants (`withAccountTmpEnv`), unless a composed `TMPDIR` is set; an empty resolution sets nothing. No `TMUX_TMPDIR`, `TMPPREFIX` or `OVERMIND_SOCKET`: those are self mode's session-dir variables (*amended 2026-11-12 by [MMP-D36](#MMP-D36):* `TMUX_TMPDIR` and `TMPPREFIX` now follow it, since the shared `/tmp` they default to is closed; `OVERMIND_SOCKET` stays unset). | The grant without the variable left every tool to fall back to `/tmp`; `getconf DARWIN_USER_TEMP_DIR` run in the sandbox printing nothing is a separate question for [W1b](#mac-checks). |
| <a id="MMP-D31"></a>MMP-D31 | **The account mode names the XDG base dirs under the workspace home.** *Implementation decision, from W1's run.* `XDG_CONFIG_HOME`, `XDG_CACHE_HOME`, `XDG_DATA_HOME` and `XDG_STATE_HOME` are set to `<ws>/.yolo/home/.config`, `.cache`, `.local/share` and `.local/state` (`withAccountXDGEnv`), each unless composed. | The workspace home is owned by you and the sandbox runs as `_yolojail`, so nix warned *"$HOME … is not owned by you"* on every call and read its user config from `/Users/_yolojail`. Nix consults `getHome()` only for a base dir whose `XDG_*` variable is unset (measured on Linux, nix 2.34.9, with a HOME another uid owns: warning with the variables unset, none with them set). The values are the defaults a tool would derive from HOME anyway. |
| <a id="MMP-D32"></a>MMP-D32 | **An Apple Container home is refused by name, not adopted.** A macos-user launch whose inherited user-config destination is Apple Container's writable copy (a regular file you own, writable, one link, first line yolo's generated header) refuses before the sandbox (`refuseAppleContainerHome`), naming the file, that Apple Container used this workspace's home, the move that clears it (`mv <ws>/.yolo/home <ws>/.yolo/home.apple-container`), and `YOLO_RUNTIME=container` to keep using Apple Container. Any other occupant keeps the generic refusal. | Since 12a both backends use `<ws>/.yolo/home` with the same dotted layout. Nothing proves Apple Container's writable copy unedited, so adopting it could overwrite the owner's edit, and the rest of that home lacks the account's ACLs and holds container-era links; moving the whole home aside clears every such residue at once and loses nothing. |
| <a id="MMP-D33"></a>MMP-D33 | **An occupied workspace home is cleared one path at a time.** When the layout's HOME is the workspace home (`DeriveDarwinWorkspaceHome`), its refusal (`occupiedWorkspaceHomeError`) is headed *"In THIS WORKSPACE'S HOME"*, names where each occupied path's link points, and offers `sudo rm -rf` of that path alone. The account layout's refusal is unchanged. | It offered `sudo rm -rf <ws>/.yolo/home` under the pre-12a heading *"the SANDBOX ACCOUNT HOME"*: the workspace's whole home, transcripts included, to clear one directory. |
| <a id="MMP-D34"></a>MMP-D34 | **An empty `kern.procargs2` environment is an unread one.** Self mode's sandbox-marker reader treats a process whose environment comes back empty as unknown (`procArgs2Env`), so a live one counts as the sandbox's, as the tri-state in `guestprocs.go` already says for an unreadable one. | macOS 26.5 returns a platform binary's arguments and no environment, to its owner too (the W1 run's [§6](../lab-reports/2026-11-03-mac-slice-12a-witness-w1.md#6-kernprocargs2-hides-a-platform-binarys-environment)), so a sandboxed `/bin/sh` read as known and unmarked and dropped out of the pack-tree holder proof. Every process the sandbox starts carries a marker, so an empty environment never proves *not the sandbox's*; counting one costs disk until the next prune. |
| <a id="MMP-D35"></a>MMP-D35 | **The home overlay delivers copies on macos-user, in both modes; no `file-link` grant.** *Owner's ruling, 2026-11-03, A.* The guest stops trying `linkat` from the staging root and copies each staged file; an unchanged relaunch keeps a delivered copy whose content still matches the staged file (no rewrite, the inode stays), and replaces one that differs. The deny-by-default profile gets no `(allow file-link …)`. Owner: *"yes, A for your question."* | W1b measured that the profile is the only obstacle to the link and that self mode copies too ([§10](../lab-reports/2026-11-03-mac-slice-12a-witness-w1.md#10-rerun-witness-w1b-and-the-just-done-fixes-witness)). A hard link is one inode under two names, and Seatbelt checks the path written through: in self mode the sandbox owns the staged file, so it could make it writable and edit a file other workspaces share. Copying a few dozen small files per relaunch costs almost nothing. |
| <a id="MMP-D36"></a>MMP-D36 | **Closing the account mode's shared `/tmp` ([MMP-D29](#MMP-D29)) brings self mode's hatch and checks with it.** *Implementation decision, 2026-11-12.* (1) `macos_user_shared_tmp = true` opens the shared `/tmp` in the account mode too (`PlanSession.SharedTmp` selects `TmpShared` in either mode; `RunPlan.SharedTmp` and its disclosure line follow). (2) The briefing's `/tmp` line (`macosUserTmpLimit`) is printed in both modes without the hatch, and names `yolo config set macos_user_shared_tmp true --user`. (3) A `TMPDIR` or program temp variable an env layer composes outside the account mode's roots (the workspace, its home, the account's temp dir, and `/tmp` under the hatch) refuses the launch, as [MMP-D20](#MMP-D20) does in self mode; the validation context baked into the launchers carries the same roots. (4) The account mode sets `TMUX_TMPDIR` and `TMPPREFIX` to follow its `TMPDIR`, unless set, and hands its `TMPDIR` to the bootstrap ([MMP-D21](#MMP-D21)); no `OVERMIND_SOCKET`. (5) The account mode's install capture keeps its `(allow default)` profile, which grants `/tmp` and all of `/var/folders`; its validation context names exactly those two. | "As [OQ-MM8](macos-user-model.md#OQ-MM8)'s trial closed it for self mode" includes the hatch: without it, a user whose tool needs `/tmp` has no next step. The `TMPDIR` check exists because a composed `/tmp` value would now fail every temporary file with a bare "Operation not permitted". tmux and zsh ignore `TMPDIR` and default to `/tmp`. `OVERMIND_SOCKET` stays unset because the account's temp dir is shared by every account-mode workspace, and overmind's default socket is in the workspace. The capture runs a vendor installer, not an agent, and has no `TMPDIR` of its own in the account mode; closing `/tmp` there would first need [MMP-D21](#MMP-D21)'s staging `TMPDIR` for the account's staging tree. |
| <a id="MCWP-D2"></a>MCWP-D2 | **Machine root stays `/Users/_yolojail`** (`macosuser.SandboxHome()`), passed to the bootstrap as the env var `YOLO_DARWIN_MACHINE_HOME` and to every sandbox argv. Absent, or equal to HOME, means today's single-home layout, byte for byte. *Carried from the retired concurrent-workspaces plan (2026-10-22), where it was decided.* | Moves no shared bytes. The directory-service record (`NFSHomeDirectory`) and the bootstrap's identity and scope checks stay on the account. |
| <a id="MCWP-D10"></a>MCWP-D10 | **Guest yolo judges workspace scope by the account.** `sandboxEnvPairs` emits `YOLO_DARWIN_MACHINE_HOME` (protected) in private mode. `cli.Main` and `cmd/yolo-jaild` call one `paths` helper that, when that variable names a directory owned by the process euid, applies `paths.GuardScopeUnder` to it. The three per-verb copies (bootstrap, log sink, capture materialize) may stay. *Carried from the retired concurrent-workspaces plan (2026-10-22), where it was decided.* | With HOME inside the workspace, every guest verb that creates `.yolo` would otherwise see "workspace CONTAINS your home" and refuse. `yolo-jaild` reaches `nixroots` and `durable`, which create it. Ownership, not the variable, is the identity check, so the host user is unaffected even if it has the variable set. |

## Map

| Path | Change | Slice |
| :--- | :--- | :--- |
| [`internal/entrypoint/darwinoverlay.go`](../../internal/entrypoint/darwinoverlay.go) | install record and removal of dropped destinations ([MMP-D11](#MMP-D11)) | 1 |
| `internal/config/macosusermode.go` | new: key constant, `MacosUserMode()`, `macosUserModeValue`, `macosUserModeProblem` | 2 |
| [`internal/config/validate.go`](../../internal/config/validate.go), [`internal/config/config.go`](../../internal/config/config.go) | `validateMacosUserMode` in `ValidateConfig`'s list; key in `knownTopLevelConfigKeys` | 2 |
| [`internal/config/inherit.go`](../../internal/config/inherit.go), [`internal/render/configkeys.go`](../../internal/render/configkeys.go), [`internal/setupcensus/configkeys.go`](../../internal/setupcensus/configkeys.go) | census rows for the new key | 2 |
| [`internal/cli/config_ref.txt`](../../internal/cli/config_ref.txt), [`userguide/reference/settings-per-setup.md`](../../userguide/reference/settings-per-setup.md) | key documented | 2 |
| [`internal/cli/commands.go`](../../internal/cli/commands.go) | read the mode, refuse `self` (until slice 6), fill `PlanSession.Mode`/`HostHome` (`~:1399`) | 2, 6, 11 |
| `internal/macosuser/mode.go` | new: `Mode`, `ParseMode`, `RunAs`, `RunAs.Argv` | 3 |
| [`internal/macosuser/macosuser.go`](../../internal/macosuser/macosuser.go), [`privilege.go`](../../internal/macosuser/privilege.go), [`provision.go`](../../internal/macosuser/provision.go), [`jaildaemon.go`](../../internal/macosuser/jaildaemon.go), [`serviceprobe.go`](../../internal/macosuser/serviceprobe.go), [`ctxlinks.go`](../../internal/macosuser/ctxlinks.go), [`capture.go`](../../internal/macosuser/capture.go) | every `RunAsSandboxArgv` call goes through `RunAs` | 3 |
| `internal/macosuser/seatbeltdeny.go` | new: `ProfileParams`, `TmpPolicy`, `DenyDefaultProfile`, production constants; loopback, unix-socket and services-dir rules ([MMP-D14](#MMP-D14) to [MMP-D16](#MMP-D16)) | 4 |
| `internal/macosuser/sessiontmp.go` | new: the self-mode session TMPDIR and the services-dir derivation ([MMP-D15](#MMP-D15), [MMP-D17](#MMP-D17)) | 6 |
| `internal/config/macosusersharedtmp.go` | new: the `macos_user_shared_tmp` hatch, its reader and its user-scope check ([MMP-D18](#MMP-D18)); registered like `macos_user_mode` in slice 2's files | 6 |
| `integration/macosuserdenydefault_test.go` | new: registry of the new profile's ids (Linux, `-short`); runtime cases (macOS CI, slice 10) | 4, 10 |
| [`internal/paths/paths.go`](../../internal/paths/paths.go), [`internal/paths/workspacescope.go`](../../internal/paths/workspacescope.go) | `MacosUserMachineRoot`; guest scope helper ([MMP-D7](#MMP-D7)) | 5 |
| [`internal/entrypoint/darwinhomelayout.go`](../../internal/entrypoint/darwinhomelayout.go) | `DarwinHomes`, `DeriveDarwinWorkspaceHome`; `darwinHomeLayoutFor` picks it when HOME is the sidecar | 5 |
| [`internal/cli/internal.go`](../../internal/cli/internal.go) (`~:362`), `cmd/yolo-jaild` | guest scope helper call sites | 5 |
| [`internal/macosuser/envfile.go`](../../internal/macosuser/envfile.go) | `YOLO_DARWIN_REAL_HOME`, `YOLO_DARWIN_MACHINE_HOME`, `YOLO_DARWIN_SHARED_HOME` join `protectedSandboxEnvKeys` | 5 |
| [`internal/macosuser/runplan.go`](../../internal/macosuser/runplan.go) | self-mode branch of `BuildRunPlanWithStages`: homes, profile, argvs, no ACL or endpoint grants, confined bootstrap; `PlanInvariants` self-mode rules | 6 |
| [`internal/macosuser/orchestrator.go`](../../internal/macosuser/orchestrator.go), [`preconditions.go`](../../internal/macosuser/preconditions.go) | self mode skips the hold, the account preconditions and the grant stage; creates the staging root | 6 |
| [`internal/macosuser/ctxlinks.go`](../../internal/macosuser/ctxlinks.go) | rule 5 (no source in a home) and the sudo preflight only in account mode | 6 |
| [`internal/macosuser/macosuser.go`](../../internal/macosuser/macosuser.go) `HomeContaining` call sites | the in-home refusal only in account mode | 6 |
| `internal/macosuser/loopbackports.go` | deleted with [MMP-D10](#MMP-D10) by [MMP-D14](#MMP-D14) | 7 |
| [`internal/hostcas/hostcas.go`](../../internal/hostcas/hostcas.go), [`internal/cli/run/hostcasalias.go`](../../internal/cli/run/hostcasalias.go), `internal/hostcas/probe_darwin.go` (new) | darwin rows, `MacosUserSelf` gate, darwin probe | 8 |
| [`internal/macosuser/seatbeltcapture.go`](../../internal/macosuser/seatbeltcapture.go), [`capture.go`](../../internal/macosuser/capture.go) | self-mode capture: crossing, the denied "shared home" becomes the machine root and shared dirs | 9 |
| `internal/cli` (new hidden verb `yolo internal macos-user-profile`), [`run-checks.sh`](../plans/runbooks/macos-user-model-prototype/run-checks.sh), [prototype README](../plans/runbooks/macos-user-model-prototype/README.md) | render the generated profile; `--profile-from-yolo` option | 10 |
| [`NOTICE`](../../NOTICE) | Agent Safehouse (Apache-2.0) attribution for the adapted rules | 4 |
| docs listed under [Ships with](#ships-with) | behavior flips | 11, 12, 13 |
| `internal/cli/run/accounthomehold.go` | deleted, with `Deps.HoldAccountHome`, `PreflightLaunch`'s hold parameter and the `commands.go` wiring (done in 12a) | 12a |

## Reuse before you write

- **Escaping and ancestor walking.** Use `sbplStr` for every path. Write a general
  `ancestorLiteralsAll(paths ...string)` beside `ancestorLiterals` (`seatbelt.go:513`). The existing
  one deliberately covers only `/Users/Shared/` and must keep that shape for the old profile until
  slice 13.
- **Context, relocation, device and readonly blocks.** Call `contextReadAllow`,
  `contextWriteAllow`, `contextReadonlyDeny` (`ctxlinks.go:1152-1183`), `relocationReadAllow`,
  `relocationWriteAllow`, `readonlyDenies`, `homeReadonlyDenies` and `deviceIoctlAllow` from the new
  builder. Do not copy them. Their ids carry over unchanged ([Test-id ledger](#test-id-ledger)).
- **The prototype is the rule text.** Port
  [`profile.sb`](../plans/runbooks/macos-user-model-prototype/profile.sb) block by block, with its
  `[source]` tags and its Apache-2.0 header, into the generated text. The header ships inside every
  installed profile. Add one Safehouse line to `NOTICE`.
- **User-scope key.** Mirror [`perflogging.go`](../../internal/config/perflogging.go): a value
  function split from the real-home read, and a `…Problem(v)` shape check. The workspace refusal
  mirrors `validateHostManagement` (`validate.go:565-584`). The tests mirror
  [`hostwrappers_test.go`](../../internal/config/hostwrappers_test.go).
- **No-follow manifest writes.** Copy `InstallDarwinCacheRelocations`' shape
  (`darwinhomelayout.go:832`): `os.OpenRoot`, then `readCacheRelocationManifest` and
  `writeCacheRelocationManifest` (`:932`, `:950`) for the record format and rename.
- **Overlay test fixture.** `newOverlayFixture`, `stage`, `install`, `requireFile` and
  `requireAbsent` in [`darwinoverlay_test.go`](../../internal/entrypoint/darwinoverlay_test.go)
  build the whole state slice 1 needs.
- **Plan test fixture.** `testRunPlan` and `hasProblem`
  ([`launchconfinement_test.go`](../../internal/macosuser/launchconfinement_test.go)), plus
  `containsArgPair`.
- **Endpoint ports.** `svcendpoint.Read(path)` returns `Endpoint.HostPort`. `isServiceEndpointVar`
  (`runplan.go:989`) already picks the variables.
- **Port-key parsing.** `macosuserportrelay.go` already walks `network.forward_host_ports` entries
  into JAIL and HOST ports. Reuse its parse, and do not write a third.
- **Bounded lock.** Not needed: 12a takes no machine-tier lock ([MMP-D27](#MMP-D27)), and the
  hold file whose `lockBounded` shape this item pointed at is deleted (git history has it).
- **Native test fixtures (slices 10 and 12).** `requireMacosUserSeatbelt`, the bare-control pattern,
  and `seatbeltCases` in
  [`macosuserseatbelt_test.go`](../../integration/macosuserseatbelt_test.go). The runbook's check
  list (a1 to f1) is the case list to port.

## Traps

- **SBPL is last-match-wins, and the explicit denies must stay last.** The prototype ends with the
  host-secret, keychain, socket and service denies, then `(deny appleevent-send)`. A context or
  relocation allow placed after them re-opens a keychain. So a `mounts` entry naming `~/.ssh`
  stays denied on macOS. That is intended, and it is as strict as today's
  `contextReadAllow`-before-keychains ordering.
- **Keep `(deny appleevent-send)` as the final form, pinned as the last line in a unit test**, as
  defense in depth. The first Mac run's "Finder answered `osascript`" was a false alarm: `get name`
  sends no event. Measured 2026-10-23 on macOS 26.5, a real event fails without the rule.
- **Every `(deny …)` line needs `;; #seatbelt-test-id:<id>#` on the line directly above it, and the
  id must be unique per deny.** `TestEverySeatbeltDenyCarriesATestID` reads exactly that shape. A
  multi-deny block like the prototype's `mach-lookup` list is one form with one id, not one per
  name.
- **Paths must be physical.** The kernel resolves symlinks before the policy is consulted, so a rule
  on `/tmp` or `/var/folders/…` matches nothing (`BuildRunPlanWithStages`' header records the
  measurement). Resolve every root once, as the plan already does for the workspace.
  `DARWIN_USER_TEMP_DIR` comes back unresolved and with a trailing slash from
  `getconf DARWIN_USER_TEMP_DIR`.
- **`/usr` covers `/usr/local`, which is Intel Homebrew.** If the ruling hides host software,
  `/usr/local` needs its own deny after the `/usr` allow. `HostSoftware` decides both
  `/opt/homebrew` and `/usr/local`.
- **`DARWIN_USER_TEMP_DIR` in self mode is your own temp dir**, which every process of yours shares.
  It is the same exposure as the shared `/tmp` (check b14), so `TmpPolicy` must govern both.
- **A declared device now needs file access, not just ioctl.** Under `(allow default)` a
  `/dev/cu.*` open already succeeded. Under deny-default it does not, so `deviceIoctlAllow` alone
  leaves `config.devices` broken. Emit `file-read* file-write*` beside the ioctl allow, under the
  same id.
- **`darwinoverlaylink.go`'s seal rests on the uid split** (staged files are `a-w` and owned by the
  host user, so the account "cannot chmod it"). In self mode the agent is that owner, and
  `homeReadonlyDenies` (`file-write*` includes mode changes) is the only protection. Never drop the
  home-content denies from the new profile.
- **`MkdirAll` follows links.** HOME is agent-writable, so the bootstrap must check the HOME chain
  before it creates anything (`linkedSidecarPaths`' shape). Confining the bootstrap
  ([MMP-D9](#MMP-D9)) bounds the damage, but it does not replace the check.
- **`SandboxMiseData(home)` follows HOME.** Fed the workspace HOME, it silently creates a
  per-workspace mise store. Pass the machine root ([MMP-D6](#MMP-D6)).
- **HOME inside the workspace.** A tool walking up from a cwd under HOME finds the workspace's
  `.git` (`.yolo/.gitignore` hides `.yolo`). `cd ~ && yolo config …` in the guest finds
  `<HOME>/.yolo` as a workspace marker. `workspace_readonly: ["."]` makes HOME read-only. All three
  are accepted divergences, as in the concurrent plan's traps. Do not move HOME.
- **Ephemeral loophole ports.** `svcendpoint.Listen` binds `127.0.0.1:0`, so a port is known only
  after its daemon publishes. The profile is written at `install_profile`, after `run.go` starts the
  host services. If an endpoint file is missing at plan time, the launch must say which service and
  stop. It must not render a profile that silently omits the port, because the in-jail witness
  would then refuse the launch anyway, and less clearly.
- **The registry test renders one profile.** `TestMacosUserSeatbeltRegistryMatchesTheProfile` reads
  only the old profile, so the new ids get their own registry and test (slice 4). Do not union them
  into the old one, because slice 13 deletes the old one whole.
- **`hostcas` source paths are Linux defaults.** On macOS, `os.UserCacheDir()` is
  `~/Library/Caches`, and `probe_other.go` reports every store absent. Without a darwin probe,
  slice 8 aliases nothing and every unit test still passes.
- **The capture profile shares the account-home assumption.** `seatbeltcapture.go` denies
  `SandboxHome()` explicitly. In self mode, the store it must keep a captured installer away from is
  the machine root plus `GlobalHome()`'s shared dirs (slice 9).
- **Integration tests for the old account path keep running in macOS CI until slice 13.**
  `macosuseraccounthome_test.go` asserts the hold. It must keep passing, because account mode keeps
  the hold until slice 12.

## Test-id ledger

Every `#seatbelt-test-id:` in today's profile, and what becomes of it in the deny-default profile
(requirement 1). **Kept** means the same id on a rule with the same effect. **Subsumed** means the
behavior holds through `deny-default`, the id retires with the old profile in slice 13, and its
runtime case is re-registered against `deny-default`.

| Today's id | In the new profile |
| :--- | :--- |
| `write-outside-deny` | subsumed by `deny-default` (writes outside the allow list are refused) |
| `workspace-write-allow`, `workspace-read-allow` | kept |
| `workspace-readonly-deny`, `home-content-write-deny`, `home-content-anchor-deny` | kept, same position after the writable allows |
| `volumes-read-deny` | subsumed by `deny-default` |
| `boot-volume-read-allow` | retired: nothing is granted by a `/Volumes` path, and the id was registered unproven |
| `raw-device-deny` | kept as an explicit deny in the closing block |
| `users-read-deny` | subsumed by `deny-default`; the named secrets get `host-secrets-deny` |
| `staging-root-read-allow` | kept (read only) |
| `library-keychains-deny`, `system-keychains-deny` | kept, in the closing block |
| `cross-process-procargs-deny`, `cross-process-pidinfo-deny`, `same-sandbox-pidinfo-allow` | kept |
| `file-ioctl-deny` | subsumed by `deny-default` |
| `file-ioctl-tty-allow` | kept |
| `device-ioctl-allow` | kept, widened to file read and write on the node (see [Traps](#traps)) |
| `macos-log-deny` | kept, in the closing block |
| `context-read-allow`, `context-write-allow`, `context-readonly-deny` | kept |
| `cache-relocation-read-allow`, `cache-relocation-write-allow` | kept |

New ids: `deny-default`, `system-read-allow`, `host-software-read-allow`, `ancestor-literal-allow`,
`home-allow`, `machine-dir-allow`, `tmp-allow`, `process-allow`, `mach-lookup-allow`,
`host-secrets-deny`, `launchd-listener-deny`, `ssh-agent-socket-deny`, `container-socket-deny`,
`service-deny`, `appleevent-send-deny`, `loopback-allow` ([MMP-D14](#MMP-D14); it replaced
`loopback-deny` and `loopback-port-allow`), `unix-socket-allow` ([MMP-D16](#MMP-D16)),
`services-dir-read-allow` and `services-lock-deny` ([MMP-D15](#MMP-D15)), `user-tmp-allow` and
`shared-tmp-allow` (the hatch's; [MMP-D18](#MMP-D18)), `host-home-stat-allow` (the run-as-you
capture's; [MMP-D22](#MMP-D22)), and `cache-alias-allow` (slice 8). The names are cheap and the implementer's to adjust. Each one needs
a registry entry.

## Tasks

The slices in order. A **Mac** column entry of "human" means a person runs the named check on a
real Mac before the slice lands. "CI" means the macOS runner in `.github/workflows/macos-user.yml`
runs it after landing, as a backstop.

| Slice | Touches | Mac | Order |
| :--- | :--- | :--- | :--- |
| 1 | overlay install record | no (CI backstop) | independent |
| 2 | `macos_user_mode` key | no | independent |
| 3 | `RunAs` crossing | no | independent |
| 4 | deny-default profile builder | no | independent |
| 5 | self-mode homes, layout, guest scope | no | after 3 |
| 6 | self-mode plan and orchestrator, opt-in `self` | no (CI backstop) | after 2, 3, 4, 5 |
| 7 | loopback port collection | no | after 6, only if loopback stays denied |
| 8 | package-cache aliasing in self mode | no (verified in 11) | after 6 |
| 9 | self-mode install capture | CI | after 6 |
| 10 | profile render verb, runbook option, policy suite | CI | after 4 |
| 11 | rulings in, rerun gate, default flips to `self` | **human** | after 6 to 10 |
| 12 | account mode on the model | **human** | after 11 |
| 13 | delete the old profile, shared home and hold | CI | after 12 |

### Slice 1 — the overlay forgets nothing it installed (independent)

- **Settled:** a destination a later launch no longer lists is removed, like an unmounted bind on
  podman. Agent state beside it stays ([MMP-D11](#MMP-D11)). Do not re-open.
- **Preconditions:** `installHomeOverlayDestinations(overlay, home string, roots []overlayRoot, links overlayLinks) error`
  exists at `darwinoverlay.go:342`. Otherwise stop.
- **Tests first.** Append to `internal/entrypoint/darwinoverlay_test.go`:

  ```go
  // A destination the next overlay stops listing is REMOVED, the way a dropped bind
  // vanishes on podman; the one still listed is replaced and the agent's state stays.
  func TestOverlayInstallRemovesADestinationTheNextOverlayNoLongerLists(t *testing.T) {
  	f := newOverlayFixture(t)
  	f.stage(t) // lists .pi/agent/skills and .pi/agent/AGENTS.md
  	if err := f.install(t); err != nil {
  		t.Fatal(err)
  	}
  	state := f.agentState(t)

  	if err := os.RemoveAll(f.overlay); err != nil {
  		t.Fatal(err)
  	}
  	writeTreeFile(t, filepath.Join(f.overlay, ".pi", "agent", "skills", "demo", "SKILL.md"), "next skill")
  	if _, err := WriteHomeOverlayManifest(f.overlay, []string{".pi/agent/skills"}); err != nil {
  		t.Fatal(err)
  	}
  	if err := f.install(t); err != nil {
  		t.Fatal(err)
  	}
  	requireAbsent(t, filepath.Join(f.home, ".pi", "agent", "AGENTS.md"),
  		"a destination the overlay stopped listing lingered")
  	requireFile(t, filepath.Join(f.home, ".pi", "agent", "skills", "demo", "SKILL.md"), "next skill")
  	requireFile(t, state, "pi's sign-in")
  }

  // The record is in the agent-writable sidecar, so an entry naming a path outside the
  // home is ignored, never followed.
  func TestOverlayInstallIgnoresARecordEntryOutsideTheHome(t *testing.T) {
  	f := newOverlayFixture(t)
  	f.stage(t)
  	if err := f.install(t); err != nil {
  		t.Fatal(err)
  	}
  	outside := filepath.Join(filepath.Dir(f.home), "outside.txt")
  	writeTreeFile(t, outside, "not yolo's")
  	writeTreeFile(t, filepath.Join(f.sidecar, ".yolo-home-overlay-installed.json"),
  		`{"destinations": ["../outside.txt", "/etc/passwd", ".pi/agent/AGENTS.md"]}`)
  	if err := f.install(t); err != nil {
  		t.Fatal(err)
  	}
  	requireFile(t, outside, "not yolo's")
  	requireFile(t, filepath.Join(f.home, ".pi", "agent", "AGENTS.md"), "new briefing")
  }
  ```

  Red: `go test -short -run 'TestOverlayInstallRemovesADestination|TestOverlayInstallIgnoresARecordEntry' ./internal/entrypoint`.
  The first test fails with "a destination the overlay stopped listing lingered". The second may
  pass already. It is a guard that must stay green.
- **Change:** in `installHomeOverlayDestinations`, after the install loop, read the record from the
  sidecar root (open it beneath the sidecar's `overlayRoot`, `O_NOFOLLOW`, size-capped as
  `HomeOverlayDigest` reads), normalize it with `normalizeOverlayDests`, and remove every entry not
  equal to or under a current destination. Remove through a sibling of `installOverlayDestination`
  that resolves the route and roots the same way. Then write the current list as the new record
  (rename into place). With no sidecar root, keep no record and say nothing. Name the constant
  beside `HomeOverlayManifestName`.
- **Gates:** the red command green; `go test -short ./internal/entrypoint`; `just format`.
- **Fences:** no host-side change (`internal/cli/run/macoshomeoverlay.go`), no profile change.
- **Commit:** `fix(macos-user): remove overlay destinations a later launch no longer lists`
- **Report:** the red output, the green gates, and whether the second test was already green.

### Slice 2 — the mode setting (independent)

- **Settled:** user scope only; a workspace value is refused loudly ([MMP-D3](#MMP-D3)). Until
  slice 6, `self` is refused at launch with a next step, so the key never reads as protection that is
  not there.
- **Tests first.** New `internal/config/macosusermode_test.go`:

  ```go
  package config

  import (
  	"os"
  	"path/filepath"
  	"strings"
  	"testing"
  )

  func TestMacosUserModeReadsUserConfigOnly(t *testing.T) {
  	home := t.TempDir()
  	t.Setenv("HOME", home)
  	t.Setenv("YOLO_VERSION", "")
  	userCfg := filepath.Join(home, ".config", "yolo-jail", "config.jsonc")
  	if err := os.MkdirAll(filepath.Dir(userCfg), 0o755); err != nil {
  		t.Fatal(err)
  	}
  	if got, set := MacosUserMode(); set || got != "" {
  		t.Errorf("no user config: MacosUserMode() = %q, %v; want \"\", false", got, set)
  	}
  	write(t, userCfg, `{"macos_user_mode": "account"}`)
  	if got, set := MacosUserMode(); !set || got != "account" {
  		t.Errorf("MacosUserMode() = %q, %v; want account, true", got, set)
  	}
  	ws := t.TempDir()
  	t.Chdir(ws)
  	write(t, filepath.Join(ws, WorkspaceConfigName), `{"macos_user_mode": "self"}`)
  	if got, _ := MacosUserMode(); got != "account" {
  		t.Fatalf("a WORKSPACE config chose the macos-user mode (%q)", got)
  	}
  }

  func TestValidateMacosUserModeRefusesWorkspaceScope(t *testing.T) {
  	ws := t.TempDir()
  	t.Setenv("YOLO_VERSION", "")
  	write(t, filepath.Join(ws, WorkspaceConfigName), `{"macos_user_mode": "account"}`)
  	var errs []string
  	validateMacosUserMode(decode(t, `{"macos_user_mode": "account"}`), ws, &errs)
  	if joined := strings.Join(errs, "\n"); !strings.Contains(joined, "user-scope only") {
  		t.Errorf("errors = %v, want one saying 'user-scope only'", errs)
  	}
  }

  func TestValidateMacosUserModeShape(t *testing.T) {
  	ws := t.TempDir()
  	t.Setenv("YOLO_VERSION", "")
  	for _, bad := range []string{`"jail"`, `true`, `"Self"`} {
  		var errs []string
  		validateMacosUserMode(decode(t, `{"macos_user_mode": `+bad+`}`), ws, &errs)
  		if len(errs) == 0 || !strings.Contains(errs[0], `"self" or "account"`) {
  			t.Errorf("%s: errors = %v, want one naming \"self\" or \"account\"", bad, errs)
  		}
  	}
  	for _, good := range []string{`"self"`, `"account"`} {
  		var errs []string
  		validateMacosUserMode(decode(t, `{"macos_user_mode": `+good+`}`), ws, &errs)
  		if len(errs) != 0 {
  			t.Errorf("%s: unexpected errors %v", good, errs)
  		}
  	}
  }
  ```

  In `internal/cli`, add `TestMacosLaunchRefusesSelfModeUntilItIsBuilt`. It drives the
  `commands.go` mode read through its seam with the user config `{"macos_user_mode": "self"}` and
  asserts exit 1 with a message containing `macos_user_mode` and "remove it". This test is
  rewritten in slice 6.

  Red: `go test -short -run 'MacosUserMode' ./internal/config` fails to compile (`undefined: MacosUserMode`).
- **Change:** `internal/config/macosusermode.go` holds `macosUserModeKey = "macos_user_mode"`,
  `MacosUserMode() (value string, set bool)` over `UserScopeConfigOrEmpty()`, and
  `macosUserModeProblem`. `validateMacosUserMode` is added to `ValidateConfig`'s list (keep its
  order; append beside `validatePerfLogging`). Then add the key to `knownTopLevelConfigKeys`,
  `render.hostConfigKeys`, the `inheritCensus` (not inherited: a nested jail is Linux), the
  `setupcensus` entry (cells: macos-user "reads it", the other three setups "ignored: not this
  backend") with its guide row, and `config_ref.txt`. In `commands.go`, read the mode before
  building `macosuser.Options`. `self` refuses: `macos_user_mode "self" is not built yet; remove it
  from ~/.config/yolo-jail/config.jsonc to launch as today` (happy path: it names the next step).
- **Gates:** the red command green; `go test -short ./internal/config ./internal/setupcensus ./internal/render ./internal/cli`; `just format`.
  `configrefcoverage_test.go`, `census_test.go` and `guide_test.go` fail until each surface carries
  the key, which is the point.
- **Fences:** nothing under `internal/macosuser`.
- **Commit:** `feat(config): add the user-scope macos_user_mode setting`
- **Report:** red, green, and the coverage tests that went red before each surface was added.

### Slice 3 — one crossing value (independent)

- **Settled:** account-mode argvs stay byte for byte as they are ([MMP-D4](#MMP-D4)).
- **Tests first.** New `internal/macosuser/mode_test.go`:

  ```go
  package macosuser

  import (
  	"os"
  	"path/filepath"
  	"slices"
  	"strings"
  	"testing"
  )

  func TestRunAsAccountIsTheSudoRule(t *testing.T) {
  	got := RunAs{}.Argv(true, "-i", "/usr/bin/true")
  	want := RunAsSandboxArgv([]string{"--set-home"}, "-i", "/usr/bin/true")
  	if !slices.Equal(got, want) {
  		t.Errorf("account crossing = %v, want %v", got, want)
  	}
  }

  func TestRunAsSelfCrossesNoAccount(t *testing.T) {
  	got := RunAs{Mode: ModeSelf}.Argv(true, "-i", "/usr/bin/true")
  	if want := []string{"/usr/bin/env", "-i", "/usr/bin/true"}; !slices.Equal(got, want) {
  		t.Errorf("self crossing = %v, want %v", got, want)
  	}
  	if len(RunAsArgvProblems("self", got)) != 0 {
  		t.Errorf("a self crossing reports sudo-rule problems: %v", RunAsArgvProblems("self", got))
  	}
  }

  func TestParseMode(t *testing.T) {
  	for in, want := range map[string]Mode{"": ModeAccount, "account": ModeAccount, "self": ModeSelf} {
  		if got, err := ParseMode(in); err != nil || got != want {
  			t.Errorf("ParseMode(%q) = %v, %v; want %v", in, got, err, want)
  		}
  	}
  	if _, err := ParseMode("jail"); err == nil {
  		t.Error("ParseMode(\"jail\") accepted")
  	}
  }

  // The call-site pin: no file but privilege.go and mode.go spells the sudo crossing.
  func TestEveryCrossingGoesThroughRunAs(t *testing.T) {
  	files, err := filepath.Glob("*.go")
  	if err != nil {
  		t.Fatal(err)
  	}
  	for _, f := range files {
  		if strings.HasSuffix(f, "_test.go") || f == "privilege.go" || f == "mode.go" {
  			continue
  		}
  		b, err := os.ReadFile(f)
  		if err != nil {
  			t.Fatal(err)
  		}
  		if strings.Contains(string(b), "RunAsSandboxArgv(") {
  			t.Errorf("%s calls RunAsSandboxArgv directly; cross through RunAs", f)
  		}
  	}
  }
  ```

  Red: `go test -short -run 'RunAs|ParseMode|EveryCrossing' ./internal/macosuser` fails to compile.
- **Change:** create `mode.go`. Give `LaunchArgvWithGuard`, `ProvisionArgv`, `DarwinBootstrapArgv`,
  the jail-daemon and service-probe argv builders, `ContextPreflight` and the capture driver argvs a
  `RunAs` (first parameter, or a field on the struct they already take). Callers in this slice pass
  `RunAs{}`, which renders today's argv. Thread `PlanSession.Mode` into `BuildRunPlanWithStages` as
  `RunAs{Mode: session.Mode}`, but do not branch on it yet. Add `PlanSession.Mode` and
  `PlanSession.HostHome` now, and leave them unread.
- **Gates:** the red command green; `go test -short ./internal/macosuser ./internal/cli/... ./integration`; `just format`.
  Every existing argv test must stay green unchanged. One that needs editing means the account argv
  moved, so stop and report.
- **Fences:** no change to any argv's words in account mode; no profile change.
- **Commit:** `refactor(macos-user): cross into the sandbox through one RunAs value`
- **Report:** red, green, and every call site converted.

### Slice 4 — the deny-default profile builder (independent)

- **Settled:** one deny-by-default profile with the run-as user as a parameter, started from the
  prototype ([MMP-D1](#MMP-D1), [MMP-D13](#MMP-D13)). Nothing calls it in production yet.
- **Tests first.** New `internal/macosuser/seatbeltdeny_test.go`:

  ```go
  package macosuser

  import (
  	"strings"
  	"testing"
  )

  func testProfileParams() ProfileParams {
  	return ProfileParams{
  		Workspace:         "/Users/you/code/alpha",
  		Home:              "/Users/you/code/alpha/.yolo/home",
  		HostHome:          "/Users/you",
  		SandboxTmp:        "/private/var/folders/ab/xyz/T/yolo-alpha",
  		DarwinUserTempDir: "/private/var/folders/ab/xyz/T",
  		StagingRoot:       StagingRoot(),
  		MachineDirs:       []string{"/Users/you/.local/share/yolo-jail/macos-user/.cache"},
  		Tmp:               TmpShared,
  		HostSoftware:      []string{"/opt/homebrew", "/usr/local"},
  	}
  }

  func TestDenyDefaultProfileStartsFromNothing(t *testing.T) {
  	p := DenyDefaultProfile(testProfileParams())
  	if strings.Contains(p, "(allow default)") {
  		t.Fatal("the deny-default profile allows by default")
  	}
  	d, a := strings.Index(p, "(deny default)"), strings.Index(p, "(allow ")
  	if d < 0 || a < d {
  		t.Fatalf("(deny default) must precede every allow (deny %d, first allow %d)", d, a)
  	}
  	for _, want := range []string{
  		`(subpath "/Users/you/code/alpha")`,
  		`(literal "/Users")`, `(literal "/Users/you")`, `(literal "/Users/you/code")`,
  		`(subpath "/opt/homebrew")`, `(literal "/opt")`,
  		`(remote ip "localhost:31053")`,
  	} {
  		if !strings.Contains(p, want) {
  			t.Errorf("profile lacks %s", want)
  		}
  	}
  	for _, never := range []string{`(subpath "/Users/you")`, `(subpath "/opt")`, `(subpath "/Users")`} {
  		if strings.Contains(p, never) {
  			t.Errorf("profile grants %s, a parent of what it needs", never)
  		}
  	}
  }

  func TestDenyDefaultProfileEndsWithTheAppleEventDeny(t *testing.T) {
  	p := strings.TrimSpace(DenyDefaultProfile(testProfileParams()))
  	if !strings.HasSuffix(p, "(deny appleevent-send)") {
  		t.Errorf("the last rule is not (deny appleevent-send):\n%s", p[max(0, len(p)-200):])
  	}
  }

  func TestDenyDefaultProfileKeepsSecretsDeniedAfterAContextAllow(t *testing.T) {
  	pp := testProfileParams()
  	pp.Context = []ContextLink{{Dest: "/ctx/ssh", Source: "/Users/you/.ssh", Dir: true}}
  	p := DenyDefaultProfile(pp)
  	allow := strings.Index(p, "#seatbelt-test-id:context-read-allow#")
  	deny := strings.Index(p, "#seatbelt-test-id:host-secrets-deny#")
  	if allow < 0 || deny < allow {
  		t.Errorf("host-secrets-deny (%d) must follow context-read-allow (%d)", deny, allow)
  	}
  }

  func TestDenyDefaultProfileTmpPolicyGovernsBothTempDirs(t *testing.T) {
  	pp := testProfileParams()
  	pp.Tmp = TmpPrivate
  	p := DenyDefaultProfile(pp)
  	for _, never := range []string{`(subpath "/private/tmp")`, `(subpath "/private/var/folders/ab/xyz/T")`} {
  		if strings.Contains(p, never) {
  			t.Errorf("TmpPrivate still grants %s", never)
  		}
  	}
  	if !strings.Contains(p, `(subpath "/private/var/folders/ab/xyz/T/yolo-alpha")`) {
  		t.Error("TmpPrivate lost the launch's own TMPDIR")
  	}
  }

  func TestDenyDefaultProfileHidesUsrLocalWithoutHostSoftware(t *testing.T) {
  	pp := testProfileParams()
  	pp.HostSoftware = nil
  	p := DenyDefaultProfile(pp)
  	if strings.Contains(p, `(subpath "/opt/homebrew")`) || !strings.Contains(p, `(deny file-read* (subpath "/usr/local"))`) {
  		t.Error("with no host software, /opt/homebrew must be absent and /usr/local denied after /usr")
  	}
  }
  ```

  Extend `TestEverySeatbeltDenyCarriesATestID` (`seatbeltdenies_test.go:36`) to run its loop over
  `DenyDefaultProfile` as well, with every optional input set. In
  `integration/macosuserdenydefault_test.go`, add `denyDefaultRules` (every new and kept id, each
  `unproven: "runtime case lands in slice 10"`) and
  `TestMacosUserDenyDefaultRegistryMatchesTheProfile`, which mirrors
  `TestMacosUserSeatbeltRegistryMatchesTheProfile` (`macosuserseatbelt_test.go:939`) but renders
  the new profile twice (loopback denied and allowed, tmp shared and private) and takes the union of
  the ids.

  Red: `go test -short -run 'DenyDefault' ./internal/macosuser ./integration` fails to compile.
- **Change:** `seatbeltdeny.go` (types, builder, the three production constants set to the
  prototype's values: loopback denied, `TmpShared`, `{"/opt/homebrew", "/usr/local"}`). Port the
  prototype's blocks in its order. Its Apache-2.0 header becomes the profile's opening comment.
  Insert the reused blocks ([Reuse](#reuse-before-you-write)) where the old profile places them
  relative to the writable allow and the closing denies. `RunAs` is not a field: the profile text is
  identical in both modes, which is the ruling. Add the Safehouse line to `NOTICE`.
- **Gates:** the red command green; `go test -short ./internal/macosuser ./integration`; `just format`.
- **Fences:** `seatbelt.go`'s `seatbeltProfile` unchanged; no caller.
- **Commit:** `feat(macos-user): generate the deny-by-default Seatbelt profile`
- **Report:** red, green, and every prototype rule dropped or changed in the port, with its reason.

### Slice 5 — self-mode homes, layout and guest scope (after 3)

- **Settled:** HOME is `<ws>/.yolo/home` with real dot-named dirs ([MMP-D5](#MMP-D5)); the machine
  root and shared dirs per [MMP-D6](#MMP-D6); guest scope per [MMP-D7](#MMP-D7).
- **Tests first.** Append to `internal/entrypoint/darwinhomelayout_test.go`:

  ```go
  func TestDeriveDarwinWorkspaceHomeKeepsStateAsRealDirsInTheHome(t *testing.T) {
  	ws := "/Users/you/code/alpha"
  	h := DarwinHomes{
  		Home:    ws + "/.yolo/home",
  		Machine: "/Users/you/.local/share/yolo-jail/macos-user",
  		Shared:  "/Users/you/.local/share/yolo-jail/home",
  	}
  	l := DeriveDarwinWorkspaceHome(h, []string{".claude", ".pi/agent"}, []string{".claude-shared-credentials"})
  	got := map[string]string{}
  	for _, ln := range l.Links {
  		got[ln.Path] = ln.Target
  		if strings.HasPrefix(ln.Target, h.Home+"/") {
  			t.Errorf("%s links back into HOME (%s); state is a real dir there", ln.Path, ln.Target)
  		}
  	}
  	for p, want := range map[string]string{
  		h.Home + "/.cache":                     h.Machine + "/.cache",
  		h.Home + "/Library/Caches":             h.Machine + "/Library/Caches",
  		h.Home + "/.claude-shared-credentials": h.Shared + "/.claude-shared-credentials",
  	} {
  		if got[p] != want {
  			t.Errorf("%s -> %q, want %q", p, got[p], want)
  		}
  	}
  	if len(l.Links) != 3 || len(l.Mirrors) != 0 {
  		t.Errorf("links %v, mirrors %v; want 3 links, no mirrors", l.Links, l.Mirrors)
  	}
  	for _, d := range []string{".claude", ".pi/agent", ".npm-global", ".local", "go", ".yolo/bin", ".config"} {
  		if !slices.Contains(l.Dirs, h.Home+"/"+d) {
  			t.Errorf("Dirs lacks %s", h.Home+"/"+d)
  		}
  	}
  	for _, ln := range l.Links {
  		if strings.Contains(ln.Path, "/.yolo/mise") {
  			t.Errorf("%s: MISE_DATA_DIR names the machine store; HOME gets no mise link", ln.Path)
  		}
  	}
  }
  ```

  Append to `internal/paths/workspacescope_test.go` a
  `TestGuestScopeJudgesAgainstTheRealHome`: with `HOME=<ws>/.yolo/home` and
  `YOLO_DARWIN_REAL_HOME=<tmp>/you` (a directory owned by the test's euid),
  `EnsureWorkspaceStateDir(ws)` succeeds. With the variable unset it refuses as today. Append to
  `internal/macosuser/macosuser_test.go` a `TestSandboxEnvPairsCarryTheHomes`: the self-mode
  pairs carry `HOME=<ws>/.yolo/home`, `MISE_DATA_DIR=<machine>/.yolo/mise`,
  `YOLO_DARWIN_REAL_HOME=<host home>`, and the three variables are in `ProtectedSandboxEnvNames()`.

  Red: `go test -short -run 'DeriveDarwinWorkspaceHome|GuestScopeJudges|SandboxEnvPairsCarry' ./internal/entrypoint ./internal/paths ./internal/macosuser`
  fails to compile.
- **Change:** add `paths.MacosUserMachineRoot()` beside `GlobalMise`. Add
  `entrypoint.DarwinHomes` and `DeriveDarwinWorkspaceHome`. `darwinHomeLayoutFor` uses it when
  `$HOME` equals `$YOLO_DARWIN_HOME_SIDECAR`, reading `YOLO_DARWIN_MACHINE_HOME` and
  `YOLO_DARWIN_SHARED_HOME`. Before any `MkdirAll`, `Apply` refuses a link anywhere on
  `<ws>/.yolo` → HOME (reuse `LinkedSidecarError`). `internal/macosuser` gets a
  `LaunchHomes(mode, workspace, hostHome) Homes{Home, Machine, Shared, Real}`. `ModeAccount` returns
  today's values (`SandboxHome()` for all but `Real`). `sandboxEnvPairs` takes `Homes`. The guest
  scope helper lives in `paths`, and `cli.Main` and `cmd/yolo-jaild` call it. The one HOME source on
  the guest is `internal/cli/internal.go:362-368`.
- **Gates:** the red command green; `go test -short ./internal/entrypoint ./internal/paths ./internal/macosuser ./internal/cli`; `just format`.
- **Fences:** no caller passes `ModeSelf` yet; `DeriveDarwinHomeLayout` and `Apply`'s account path
  unchanged.
- **Commit:** `feat(macos-user): derive the run-as-you workspace home`
- **Report:** red, green.

### Slice 6 — the self-mode launch, opt-in (after 2, 3, 4, 5)

- **Settled:** default mode behavior from the design (no account, no admin, no grants, workspace
  anywhere). The production default stays account. `"macos_user_mode": "self"` becomes an opt-in
  that works.
- **Preconditions:** slices 2 to 5 landed; `PlanSession.Mode` exists. Otherwise stop.
- **Tests first.** New `internal/macosuser/selfmode_test.go`:

  ```go
  package macosuser

  import (
  	"strings"
  	"testing"

  	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
  )

  func selfRunPlan(t *testing.T, ws string) RunPlan {
  	t.Helper()
  	return BuildRunPlanWithDaemons(ws, jsonx.NewOrderedMap(), nil, []string{"claude"},
  		"/usr/local/bin/yolo", "", HomeOverlay{}, HostContext{}, jsonx.NewOrderedMap(), nil, nil,
  		JailDaemons{}, FloorStage{}, PlanSession{Mode: ModeSelf, HostHome: "/Users/you"})
  }

  func TestSelfModePlanRunsNoSudoAndGrantsNothing(t *testing.T) {
  	plan := selfRunPlan(t, "/Users/you/code/alpha")
  	argvs := append([][]string{plan.LaunchArgv, plan.BootstrapArgv, plan.ProvisionArgv}, plan.StageCommands...)
  	for _, a := range argvs {
  		joined := strings.Join(a, " ")
  		if len(a) > 0 && a[0] == "/usr/bin/sudo" {
  			t.Errorf("self mode crosses into an account: %s", joined)
  		}
  		if strings.Contains(joined, "chmod +a") || strings.Contains(joined, "_yolojail") {
  			t.Errorf("self mode grants or names the account: %s", joined)
  		}
  	}
  	if problems := PlanInvariants(plan); len(problems) != 0 {
  		t.Errorf("self-mode plan has problems:\n  %s", strings.Join(problems, "\n  "))
  	}
  }

  func TestSelfModeAcceptsAWorkspaceInTheHome(t *testing.T) {
  	if plan := selfRunPlan(t, "/Users/you/code/alpha"); plan.OffendingHomeSet {
  		t.Errorf("self mode refuses a workspace in the home (%s)", plan.OffendingHome)
  	}
  }

  func TestSelfModeUsesTheDenyDefaultProfileAndTheWorkspaceHome(t *testing.T) {
  	plan := selfRunPlan(t, "/Users/you/code/alpha")
  	if !strings.Contains(plan.Seatbelt, "(deny default)") || strings.Contains(plan.Seatbelt, "(allow default)") {
  		t.Error("self mode does not run under the deny-default profile")
  	}
  	if !containsArg(plan.LaunchArgv, "HOME=/Users/you/code/alpha/.yolo/home") {
  		t.Errorf("launch HOME is not the workspace home: %v", plan.LaunchArgv)
  	}
  	if !containsArgPair(plan.BootstrapArgv, "/usr/bin/sandbox-exec", "-f", plan.ProfilePath) {
  		t.Errorf("the bootstrap is not confined (MMP-D9): %v", plan.BootstrapArgv)
  	}
  }

  func TestAccountModePlanIsUnchanged(t *testing.T) {
  	if !strings.Contains(testRunPlan(t).Seatbelt, "(allow default)") {
  		t.Error("the zero-mode plan stopped using today's profile")
  	}
  }
  ```

  `containsArgPair` is production code (`capture.go:775`); add a test-only `containsArg` in the
  new file if no test helper of that name exists. In the orchestrator tests, add
  `TestSelfModeTakesNoAccountHomeHold`, in which a `Deps.HoldAccountHome` stub that fails the test
  if called is never called for `Options{Mode: ModeSelf}`. Add
  `TestSelfModeCreatesAnAbsentStagingRoot`, in which the stubbed `Run` records the `mkdir -m … /Users/Shared/yolo-staged`
  argv and no sudo argv. Rewrite slice 2's `TestMacosLaunchRefusesSelfModeUntilItIsBuilt` to the
  new behavior, `TestMacosLaunchPassesSelfModeToTheBackend`. It is a rewrite, not a repair.

  Red: `go test -short -run 'SelfMode|AccountModePlanIsUnchanged' ./internal/macosuser ./internal/cli`.
- **Change:** in `BuildRunPlanWithStages`, branch on `session.Mode` where the plan reads homes, the
  profile, grants and the in-home check:
  - homes come from `LaunchHomes`;
  - the profile is `DenyDefaultProfile` with the production constants, and the loopback ports stay
    empty until slice 7;
  - `endpointGrantCommands`, workspace ACLs and `CacheRelocationACECommands` are skipped;
  - the bootstrap argv runs under `sandbox-exec -f <profile>`;
  - `OffendingHome` is not computed.

  `PlanInvariants` gains the rule that a self-mode plan carries no sudo argv. In `RunMacosUser`, for
  self mode:
  - no `HoldAccountHome`;
  - the preconditions run only `macos`, `not-root`, `sandbox-exec` and the staging-root checks, and
    an absent root is created ([MMP-D8](#MMP-D8));
  - no grant stage.

  `SiteContextLinks` rule 5 and the sudo `ContextPreflight` apply only to account mode; self mode
  probes with `RunAs{Mode: ModeSelf}`. `commands.go` passes the parsed mode and
  `os.UserHomeDir()` (resolved) into `Options`.
- **Gates:** the red command green;
  `go test -short ./internal/macosuser ./internal/cli/... ./internal/entrypoint`;
  `go test -run 'TestMacosUser' ./integration` (skips off a Mac; must compile); `just format`.
- **Fences:** absent key still means account; no doc claims self mode works.
- **Commit:** `feat(macos-user): launch as the invoking user when macos_user_mode is self`
- **Report:** red, green, and the list of account-only steps self mode skips.

### Slice 7 — loopback ports (after 6; build only if loopback stays denied)

**Superseded.** [`OQ-MM7`](./macos-user-model.md#OQ-MM7) allowed the loopback, so [MMP-D14](#MMP-D14) deleted what this slice built,
and its plan with it; git history keeps both.

### Slice 8 — package caches shared in self mode (after 6)

- **Settled:** [OQ-BF11](../reference/disk-footprint.md#OQ-BF11): a fixed list, caches never configs,
  only where files are the user's, a matching platform, and each alias announced. Self mode is
  eligible because the agent is you.
- **Tests first.** Append to `internal/hostcas/hostcas_test.go`:

  ```go
  func TestMacosUserSelfModeAliasesAPathKeyedStore(t *testing.T) {
  	home := t.TempDir()
  	f := Facts{
  		Runtime: "macos-user", MacosUserSelf: true, IsMacOS: true,
  		HostPlatform: "darwin/arm64", JailPlatform: "darwin/arm64",
  		Ownership:  OwnershipYours,
  		HostGetenv: func(k string) string { return map[string]string{"HOME": home}[k] },
  	}
  	probe := func(string) Presence { return Presence{Exists: true, IsDir: true, Writable: true} }
  	d := decide(storeNamed(t, "pip-cache"), f, probe)
  	if !d.Aliased {
  		t.Fatalf("pip-cache not aliased in self mode: %s (%s)", d.Code, d.Reason)
  	}
  	if want := filepath.Join(home, "Library", "Caches", "pip"); d.Source != want {
  		t.Errorf("source = %q, want the macOS default %q", d.Source, want)
  	}
  }

  func TestMacosUserAccountModeDeclinesEveryStore(t *testing.T) {
  	f := Facts{Runtime: "macos-user", IsMacOS: true, HostPlatform: "darwin/arm64", JailPlatform: "darwin/arm64"}
  	for _, s := range Stores {
  		if d := decide(s, f, func(string) Presence { return Presence{} }); d.Aliased || d.Code != CodeBackend {
  			t.Errorf("%s in account mode: aliased=%v code=%s", s.Name, d.Aliased, d.Code)
  		}
  	}
  }
  ```

  If `storeNamed` does not exist, add it to the test file. Add a row test pinning every store's
  `DarwinRel`, and extend `TestNoStoreNamesAConfigOrCredentialPath` to read `DarwinHost` and
  `DarwinRel` too.
  Red: `go test -short -run 'MacosUser' ./internal/hostcas`.
- **Change:** add `Store.DarwinHost` and `Store.DarwinRel`, and `Facts.MacosUserSelf`. In `decide`,
  the backend and macOS gates admit `macos-user` with `MacosUserSelf`, and darwin resolves the
  source with `DarwinHost`. Add `probe_darwin.go` (same contract as Linux's probe). In
  `hostcasalias.go`, the macos-user caller sets `Ownership: OwnershipYours` and
  `JailPlatform: "darwin/"+GOARCH`, and delivers each alias as a HOME link plus a
  `cache-alias-allow` block (the relocation blocks' shape), never `-v`. Candidate darwin rows:
  - pip: `~/Library/Caches/pip`;
  - Go build: `~/Library/Caches/go-build`;
  - Go modules: `~/go/pkg/mod`;
  - uv: `~/.cache/uv`;
  - npm: `~/.npm/_cacache`;
  - yarn: `~/Library/Caches/Yarn`;
  - pnpm store: `~/Library/pnpm/store`;
  - cargo: `~/.cargo/registry`;
  - pants: `~/.cache/pants/lmdb_store`;
  - mise downloads: under `MISE_DATA_DIR`.

  `DarwinRel` is the same path relative to HOME, unless the sandbox env overrides it (for example
  `NPM_CONFIG_CACHE`, `entrypoint/shell.go:259`). Slice 11's Mac check confirms each row. A row the
  Mac shows unused is dropped, not guessed again.
- **Gates:** red green; `go test -short ./internal/hostcas ./internal/cli/run ./internal/macosuser`; `just format`.
- **Commit:** `feat(hostcas): share package caches with macos-user's run-as-you mode`

### Slice 9 — install capture in self mode (after 6; macOS CI)

- **Settled:** a capture never reaches the shared credential store. In self mode that store is the
  machine root plus `GlobalHome()`'s shared dirs.
- **Tests first:** in `seatbeltcapture_test.go`, add
  `TestSelfModeCaptureProfileDeniesTheMachineRootAndSharedDirs`, a sibling of
  `TestCaptureProfileDeniesWritesToTheSharedHome`. Add a capture-plan test asserting that no sudo
  argv appears when the mode is self.
- **Change:** the capture profile builder takes the denied roots from `LaunchHomes` instead of
  `SandboxHome()`. The capture and fork-build acts cross through `RunAs`. In self mode,
  `privilegePreconditions` reduces to the staging and capture roots.
- **Gates:** `go test -short ./internal/macosuser`; macOS CI runs `macosusercaptureseatbelt_test.go` after landing.
- **Commit:** `feat(macos-user): run install capture as the invoking user in self mode`

### Slice 10 — render the generated profile; the policy suite (after 4; macOS CI)

- **Change.** Add a hidden verb, `yolo internal macos-user-profile --mode self|account --workspace W --home H --tmp T [--services-dir D]…`
(it had `--allow-port P` until [MMP-D14](#MMP-D14)),
  which prints `DenyDefaultProfile` for those inputs with the production constants. Give
  [`run-checks.sh`](../plans/runbooks/macos-user-model-prototype/run-checks.sh) a
  `--profile-from-yolo <yolo>` option. With it, after building its workspace, home and temp dir,
  the script renders the profile through that verb and runs every check against it instead of
  `profile.sb`. Port the runbook's a, b, c, d and e checks into
  `integration/macosuserdenydefault_test.go` as runtime cases, each with its bare control, and
  clear the `unproven` reasons slice 4 left.
- **Tests first:** `TestMacosUserProfileVerbPrintsTheGeneratedProfile` in `internal/cli` compares
  the verb's stdout with `DenyDefaultProfile` for the same inputs. The registry test from slice 4
  now fails on every case that carries no id, until each is registered.
- **Gates:** `go test -short ./internal/cli ./integration`; `bash -n` on the script; macOS CI runs the new cases after landing.
- **Commit:** `test(macos-user): run the prototype checks against the generated profile`
- **Report:** the CI run id and its pass, fail and skip counts.

### Slice 11 — the rerun gate, then the default flips (after 6 to 10 and 12a; human Mac run)

- **Ruled by [OQ-MM10](macos-user-model.md#OQ-MM10), 2026-11-03:** what an absent `macos_user_mode` means on
  a Mac set up for the account. Nothing in 12a does.
- **Preconditions (stop and ask if any fails):**
  1. The maintainer has ruled loopback, the shared `/tmp`, and Homebrew's visibility, in the design
     doc ([Blockers](#blockers)).
  2. A human ran `./run-checks.sh --mode self --profile-from-yolo <dev yolo>` and
     `--mode account --account _yolojail` on a Mac, with the profile built from those rulings. Both
     must be free of FAIL lines, and the output must be pasted into the design's
     [first Mac run](macos-user-model.md#first-mac-run-2026-10-22) section as a dated second run.
  3. On the same Mac, with `"macos_user_mode": "self"` set, `yolo -- claude` started in a fresh
     `~/code/alpha` with no admin step. Claude logged in and stayed logged in across two launches
     (design [§6](macos-user-model.md#6-what-a-prototype-must-show) items 1 and 6). Two workspaces
     ran at once. A `pip download` inside wrote into the host's `~/Library/Caches/pip`, and each
     slice 8 row was confirmed or dropped.
- **Change:**
  - set `productionHostSoftware` to its ruling (loopback and `/tmp` are already set:
    [MMP-D14](#MMP-D14), [MMP-D18](#MMP-D18));
  - make `MacosUserMode`'s absent value resolve to `self` in `commands.go`;
  - flip the setupcensus cell and `config_ref.txt` default;
  - update the docs under [Ships with](#ships-with).
- **Tests first:** rewrite `TestMacosLaunchPassesSelfModeToTheBackend` so that an absent key also
  yields `ModeSelf`. Add one test per constant pinning the ruled value. These are rewrites to the
  new behavior, not repairs.
- **Gates:** `just check-ci`; `go test -run 'TestMacosUser' ./integration` on macOS CI after landing.
- **Commit:** `feat(macos-user): run as the invoking user by default`
- **Report:** the two pasted runs, the CI run id, and the rulings each constant encodes.

### Slice 12a — two workspaces at once in account mode (after 6 to 10; human Mac run)

- **Status:** built 2026-11-03 and unit-tested on Linux, with `GOOS=darwin` vetted; not landed
  until W1 passes on a Mac. Built as below, plus [MMP-D29](#MMP-D29) (the account's temp dirs),
  the agent env files moving to the workspace home (`<ws>/.yolo/home/.config/yolo-agent-env`), and
  the briefing's Home naming the workspace home in both modes. Not built here: the orphaned
  `Mirrors` routing and `ContextLinkOwnerEnv` retargeting in `internal/entrypoint`, left for the
  commit after it as [MMP-D25](#MMP-D25) allows.
- **Settled:** the same profile run as `_yolojail`; HOME in the workspace; two workspaces at once,
  so no hold ([MMP-D25](#MMP-D25), [MMP-D27](#MMP-D27)). The design's *"two workspaces launch at
  once in either mode"* is owed first to account-mode users: account mode is the default until
  slice 11's flip ships, so it is what macOS users run today.
- **Change:**
  - account mode takes the self-mode branch of `BuildRunPlanWithStages` for its homes, profile and
    confined bootstrap, keeping the sudo crossing, the grant stage, the account preconditions and
    the `HomeContaining` refusal ([MMP-D25](#MMP-D25));
  - the machine root is `SandboxHome()`, with no lock ([MMP-D27](#MMP-D27));
  - `HoldAccountHome` is not taken in any mode, and its file, `Deps.HoldAccountHome` and
    `PreflightLaunch`'s hold parameter go.
- **Tests first, on Linux:** `TestAccountModePlanIsUnchanged` is rewritten to pin the deny-default
  profile and the workspace HOME for the zero mode; `TestSelfModeBootstrapIsToldTheHomes` gains an
  account-mode twin with `Machine == Shared == Real == SandboxHome()`;
  `TestTheAccountHomeHoldGatesTheBuildAndSpansTheSession` and `TestARefusedHoldRunsNoContextPreflight`
  are deleted with the hold; `PlanInvariants` gains the account-mode half of `selfModeProblems`
  (deny-default profile, confined bootstrap, sudo crossing, grants present). Then
  `integration/macosuseraccounthome_test.go`'s refusal test becomes
  `TestMacosUserTwoWorkspacesRunAtOnceAsTheAccount`: A holds a session; B launches and prints;
  `$HOME` in each is its own `<ws>/.yolo/home`; nothing under `/Users/_yolojail` is a link into
  either sidecar. Account-mode readers that follow HOME: `run/claudecredentialview.go` (*"account
  mode keeps its undotted location"*), `run/agentenvfiles.go:writeMacosUserAgentEnvFiles`
  (passes `SandboxHome()`), `run/inheritscope.go` (the sidecar layout derivation),
  `macosuser/homereadonly.go`, and `ContextLinkOwnerEnv` in `runplan.go`;
  `rg -n 'SandboxHome\(\)' internal/cli/run internal/macosuser` is the checklist.
- **Gate:** witness W1 in [Mac checks](#mac-checks), then `just check-ci`, and macOS CI. Land only
  after W1.
- **Commit:** `feat(macos-user): two workspaces at once in account mode`

### Slice 12b — a workspace inside a home in account mode (after 11; human Mac run)

- **Settled:** access grants let the account reach a workspace inside your home: the design's
  done-state launches `~/code/alpha` in account mode, and
  [MM-R1](macos-user-model.md#decision-ledger) accepts the grant risk.
- **Change:**
  - the workspace grant extends a traverse-only entry to each ancestor inside a home;
  - `HomeContaining` stops refusing.
- **Tests first:** rewrite `TestHomeContaining` and `inhomeorder_test.go` to the new behavior.
- **Gate:** a human reruns `run-checks.sh --mode account` with the generated profile from a
  workspace inside a home. Then `just check-ci`, and macOS CI.
- **Commit:** `feat(macos-user): run the optional account mode from a workspace inside a home`

### Slice 12c — one alias manifest per workspace (after 12a; macOS CI)

- **Change:** the machine root's alias links are recorded per workspace, and a launch removes only
  links its own record laid that no other workspace's record names ([MMP-D27](#MMP-D27)); closes
  the [Open items](#open-items) entry "One workspace can sweep another's alias links", for both
  modes.
- **Gate:** witness W3 in [Mac checks](#mac-checks); `just check-ci`; macOS CI.
- **Commit:** `fix(macos-user): one cache-alias manifest per workspace`

### Slice 13 — delete what nothing uses (after 12; macOS CI)

- **Change:** delete:
  - `seatbeltProfile` and its wrappers, and the old registry entries (the [Test-id ledger](#test-id-ledger)
    "subsumed" and "retired" rows);
  - `accounthomehold.go`, `Deps.HoldAccountHome` and `PreflightLaunch`'s hold parameter;
  - the shared-home routing in `darwinhomelayout.go` (`Mirrors`, `homeFileThroughLayout`'s
    account branch, `occupiedLayoutError`'s `sudo rm` remedy);
  - `overlayLinks`' cross-workspace refusals;
  - `ctxlink.go`'s `ContextLinkOwnerEnv` retargeting;
  - `DarwinLoginPathEnv`'s shared-home rationale, if nothing else reads it.

  Run `rg -n` for each symbol before deleting it; a remaining caller means stop and report. What
  slice 12a already deleted ([MMP-D25](#MMP-D25)) is not here twice.
- **Gates:** `just check-ci`; macOS CI.
- **Commit:** `refactor(macos-user): delete the shared account home and the allow-default profile`

## Ships with

- **Unit, per slice:** as each task names. The cases that catch a regression end to end:
  - `TestSelfModePlanRunsNoSudoAndGrantsNothing` (a crossing added without `RunAs`);
  - `TestEveryCrossingGoesThroughRunAs` (the call-site pin);
  - `TestDenyDefaultProfileEndsWithTheAppleEventDeny` (the first Mac run's escape);
  - `TestOverlayInstallRemovesADestinationTheNextOverlayNoLongerLists`.
- **Integration:** `integration/macosuserdenydefault_test.go` (registry on Linux, runtime on macOS
  CI) is the permanent home of the prototype's checks
  ([§8.1](../research/agent-safehouse.md#81-a-policy-assertion-suite-for-macos-user--highest-value-moderate-cost)).
  The two-workspace concurrent launch (slice 12) replaces the hold's refusal test.
- **Tests that must be rewritten, not repaired:**
  - slice 2's refusal test (in slice 6);
  - `TestHomeContaining`, the `inhomeorder_test.go` cases and the hold tests in
    `terminate_test.go` and `accounthomehold_test.go` (slices 12 and 13);
  - `TestMacosUserASecondWorkspaceIsRefusedWhileASessionHoldsTheAccountHome` (slice 12; rewritten
    in 12a as `TestMacosUserTwoWorkspacesRunAtOnceAsTheAccount`);
  - the old seatbelt registry (slice 13).
- **Docs that will describe the old behavior after slice 11:**
  - [`userguide/guides/macos.md`](../../userguide/guides/macos.md): setup, the projects folder,
    access grants, the in-home refusal;
  - [`settings-per-setup.md`](../../userguide/reference/settings-per-setup.md);
  - [`macos-user-home-tiers.md`](../reference/macos-user-home-tiers.md): the shared account home;
  - [`macos-user-provisioning.md`](../reference/macos-user-provisioning.md);
  - [`context-mounts.md`](../reference/context-mounts.md#the-seatbelt-rules): rule 5 and the
    profile blocks;
  - [`disk-footprint.md`](../reference/disk-footprint.md): "never a macOS host" and "macos-user
    waits for its run-as-you mode";
  - [`macos-user-nix-and-features.md`](../reference/macos-user-nix-and-features.md);
  - [`AGENTS.md`](../../AGENTS.md): the host-cache line's "never macOS", and the agent-log
    location line ("the account home's links point there");
  - [`docs/research/macos-support-matrix.md`](../research/macos-support-matrix.md): its profile
    claims.

  Check each doc's "only" and "always" claims, not just its links.
- **Design doc:** fill the `Built` column of the [ledger](macos-user-model.md#decision-ledger) at
  slices 11 and 12. The second Mac run goes into its first-run section (slice 11).
- **Changelog:** slice 11 only. It gets one `### Changed` entry: the default now runs as you with
  no setup, the second account is `"macos_user_mode": "account"`, and existing setups need no
  action. Slice 8 gets one `### Added` line.
- **Config surfaces:** `config_ref.txt`, `setupcensus`, `render.hostConfigKeys`, `inheritCensus`
  (slice 2), and the default flip (slice 11).
- **Cheap, and the implementer's:** the new ids' names; helper names; whether `RunAs` is a parameter
  or a struct field; the record file's name.
- **Stop and ask:**
  - a prototype rule that the port needs to widen past what the prototype measured (any new
    `subpath` on a parent directory, which is [OQ-MM2](macos-user-model.md#OQ-MM2)'s line);
  - any change to which credential store self mode shares ([MMP-D6](#MMP-D6));
  - slice 11 starting without the three rulings.

## Don't

- Don't pass the profile's values through `sandbox-exec -D`. A per-session text file is how every
  launch already works ([MMP-D1](#MMP-D1)).
- Don't open per-language home folders (`~/.cargo`, `~/.nvm`, …) "so tools work". The ruling is no
  per-language home grants. Caches cross only through slice 8's fixed list, and never a config
  file.
- Don't grant the keychain, its services, or `~/Library/Keychains` to make an agent's login work. A
  login lands in `<ws>/.yolo/home` as on podman, and the host keychain is the loophole's
  ([OQ-KC4](keychain-from-a-jail.md#8-decision-ledger)).
- Don't migrate, delete or announce anything about an existing `_yolojail` account, its home or its
  sudo rule ([OQ-MM4](macos-user-model.md#OQ-MM4)).
- Don't let a workspace config choose the mode, even as a "hint" ([MMP-D3](#MMP-D3)).
- Don't build the concurrent-workspace plan's slices as well. This plan replaces its HOME geometry.
- Don't flip the default in any slice but 11, and don't add a preview environment variable to reach
  self mode early. The opt-in key is the preview.
- Don't widen `/opt` or `/Users` to fix a start-up read. Find the exact path in
  `log show --predicate 'sender == "Sandbox"'` and add that literal or subpath.

## Mac checks

[MMP-D22](#MMP-D22) and [MMP-D23](#MMP-D23) are pinned by unit tests on Linux, but neither has run on a
Mac. The maintainer's Mac agent runs:

- `YOLO_TEST_MACOS_USER=1 go test -count=1 -timeout 0 -v -run '^TestMacosUserPackTreeProgram' ./integration`.
  Expected: the three `/self` subtests pass (`…BuildsOnceAndRebuildsOnChange/self`,
  `…OverrideBuildsTheCheckout/self`, `…BuildFailureRefusesOnlyIt/self`). If a `/self` build still
  fails, its refusal carries the bootstrap's stderr. A `stat` of the real home refused there means
  `host-home-stat-allow` is too narrow. A `refusing to generate into` line means the HOME guard did
  not take the capture shape.

That check is Step 1 of [the 2026-10-24 Mac session](../plans/runbooks/mac-claude-login-session.md),
which goes on to whether macos-user's Claude reads the credential view.

**Witnesses for the second-workspace work** (macOS only; the owner's Mac agent runs them, never
the colleague who reported the refusal):

- **W0 (today's tree, before anything lands):** with no `macos_user_mode` key, launch `yolo -- bash`
  in `/Users/Shared/yolo/a`, then `yolo -- bash` in `/Users/Shared/yolo/b`. Expected: the refusal
  naming the live session, verbatim, and `readlink /Users/_yolojail/.yolo/bin` still inside
  `a/.yolo/home`. Confirms the report is the shipped default, not the reporter's config.
- **W1 (slice 12a, gates its landing):** the same two launches on the 12a build, in two fresh
  workspaces. Expected: both run, with no refusal; `echo $HOME` prints
  `/Users/Shared/yolo/a/.yolo/home` and `…/b/.yolo/home`; no link under `/Users/_yolojail` points
  into either workspace (a pre-12a account home may keep its old `.yolo/bin`, `.config`,
  `.npm-global`, `.local` or `go` links into an older workspace: nothing removes them
  ([OQ-MM4](macos-user-model.md#OQ-MM4)), and nothing reads them); in A, `ls ~/.claude` is
  unchanged after B starts; `echo $TMPDIR` names the account's own `/var/folders/…/T/` and
  `touch "$TMPDIR/x" /tmp/x` succeeds ([MMP-D29](#MMP-D29)) (*note, 2026-11-12:* that was true
  when W1 ran; since [MMP-D36](#MMP-D36) the `/tmp/x` half is refused without the hatch, and
  [W-TMP1](#W-TMP1) checks the new behavior);
  `ls ~/.config/yolo-agent-env` lists the agent env files; in a workspace used before 12a, the
  undotted `<ws>/.yolo/home/claude` and `config` are left in place and unused, so a per-workspace
  Claude setting kept there is not carried over (the shared login is); the briefing's **Home**
  line names the workspace home; `integration/TestMacosUserTwoWorkspacesRunAtOnceAsTheAccount`
  passes; the full `-run '^TestMacosUser'` suite is run, and its account-mode tests that still
  encode the pre-12a shared-home layout are rewritten from what the run shows, not repaired: the
  links-in-the-account-home premise of `macosuserhometier_test.go` and
  `macosuserlayoutrefusal_test.go`, the undotted sidecar paths in
  `macosuserhomeroothostfiles_test.go`, `macosusercontent_test.go` and `macosuserseatbelt_test.go`,
  and the account-home tier reads in `macosusertools_test.go` (12a already moved the identity
  test's `$HOME` and the jail-daemon tests' log dir to `.local`); `claude -p 'Reply with the single word: ok'`
  answers in both; `log show --last 5m --predicate 'sender == "Sandbox"'` shows no denial on either
  workspace's paths; `./run-checks.sh --mode account --account _yolojail --profile-from-yolo <dev
  yolo>` has no FAIL line. Paste into the model doc's *Self-mode rerun* section as the account-mode
  run.
  *Run 2026-11-03 at `90ff72f2c`* ([lab report](../lab-reports/2026-11-03-mac-slice-12a-witness-w1.md)):
  two workspaces ran at once and `run-checks.sh` had no FAIL line, but `$TMPDIR` was empty, nine
  `^TestMacosUser` tests that pass on 12a's parent failed (five not listed above), and `claude -p`
  could not authenticate (a revoked login). W1 is not yet passed. What each finding was, by the
  report's section:
  - **[§2](../lab-reports/2026-11-03-mac-slice-12a-witness-w1.md#2-tmpdir-is-empty-in-account-mode), `$TMPDIR` empty:** a defect, fixed ([MMP-D30](#MMP-D30)). `getconf DARWIN_USER_TEMP_DIR`
    printing nothing *inside* the sandbox is not fixed: the likely cause is a denied lookup of the
    directory service the call makes, and no grant is widened without the denial in hand (W1b).
  - **[§3](../lab-reports/2026-11-03-mac-slice-12a-witness-w1.md#3-the-macos-user-suite-nine-failures-all-from-12a), the nix warning:** a defect, fixed ([MMP-D31](#MMP-D31)); the test is unchanged.
  - **[§3](../lab-reports/2026-11-03-mac-slice-12a-witness-w1.md#3-the-macos-user-suite-nine-failures-all-from-12a), the config capture missing the session's `settings.json` edit:** a defect, fixed. The
    capture mapped `~/.claude` to the undotted `<ws>/.yolo/home/claude` for macos-user, where 12a's
    HOME keeps it dotted (`jailHomeRel`).
  - **[§3](../lab-reports/2026-11-03-mac-slice-12a-witness-w1.md#3-the-macos-user-suite-nine-failures-all-from-12a), the floor resolving to mise shims:** a stale test, rewritten. The mise store is the
    machine root's (`MISE_DATA_DIR`), outside HOME by design, so the probe now drops it too.
  - **[§3](../lab-reports/2026-11-03-mac-slice-12a-witness-w1.md#3-the-macos-user-suite-nine-failures-all-from-12a), the unchanged relaunch replacing delivered files:** ruled 2026-11-03 ([MMP-D35](#MMP-D35)): copies, kept when unchanged; no `file-link` grant. Before the ruling: The
    delivered file is the account's own, so `linkat` from the staging root fell back to a copy.
    Pre-12a the bootstrap ran under `(allow default)` and linked; the deny-by-default profile is
    the change. The likely fix is an `(allow file-link …)` for the staging root, but an operation
    name the Mac's Seatbelt does not know fails the profile's compile on every launch, so it waits
    for W1b's errno and probe.
  - **[§3](../lab-reports/2026-11-03-mac-slice-12a-witness-w1.md#3-the-macos-user-suite-nine-failures-all-from-12a), the overlay agent-state symlink:** a stale test, rewritten. `~/.oh-omp` is now a real
    directory in the workspace home, not a link into it.
  - **[§3](../lab-reports/2026-11-03-mac-slice-12a-witness-w1.md#3-the-macos-user-suite-nine-failures-all-from-12a), the four predicted stale tests** (`macosuserhometier_test.go`,
    `macosuserlayoutrefusal_test.go`, `macosuserhomeroothostfiles_test.go`,
    `macosusertools_test.go`): rewritten to 12a's layout. The layout refusal's remedy was also a
    defect, fixed ([MMP-D33](#MMP-D33)).
  - **[§4](../lab-reports/2026-11-03-mac-slice-12a-witness-w1.md#4-the-briefing-still-describes-the-shared-account-home), the briefing's shared-home wording:** a defect, fixed. Both notches say the home is this
    workspace's own and only its machine-scope links are shared.
  - **[§5](../lab-reports/2026-11-03-mac-slice-12a-witness-w1.md#5-an-apple-container-home-blocks-a-later-account-mode-launch), an Apple Container home refusing a macos-user launch:** a happy-path refusal, not
    support ([MMP-D32](#MMP-D32)).
  - **[§6](../lab-reports/2026-11-03-mac-slice-12a-witness-w1.md#6-kernprocargs2-hides-a-platform-binarys-environment), `kern.procargs2` hiding a platform binary's environment:** the four short-suite tests
    now start a stand-in `sh` that is not a platform binary, found on `PATH` or in a nix profile,
    and skip naming `nix profile install nixpkgs#bash` when there is none. The one production
    reader, self mode's sandbox marker, is fixed ([MMP-D34](#MMP-D34)); `internal/prune` reads
    only the exec path.
  - **[§7](../lab-reports/2026-11-03-mac-slice-12a-witness-w1.md#7-run-checkssh-g4-and-a-concurrent-launch), `run-checks.sh` g4 beside a launch:** ours, fixed. The script now holds each fake
    session's liveness lock while it runs, so a launch's sweep of dead sessions keeps them.
- **W1b (the W1 fixes, before 12a lands):** on the branch carrying them, the owner's Mac agent
  re-runs exactly:
  - the nine tests, by name. Expected: all pass but `…UnchangedRelaunch…`; and in the home-tier
    run, `readlink ~/Library/Caches` prints `/Users/_yolojail/Library/Caches`:

    ```
    YOLO_TEST_MACOS_USER=1 go test -count=1 -timeout 0 -v -run '^(TestMacosUserHomeTierIsPerWorkspace|TestMacosUserLayoutRefusesAnOccupiedSidecarMirror|TestMacosUserHomeRootHostFilesArePerWorkspace|TestMacosUserDeclaredToolsArrive|TestMacosUserNixIsTheHostDaemonClientWithNoFlags|TestMacosUserCapturesConfigEditsWhenTheSessionEnds|TestMacosUserFloorReachesTheSandboxPath|TestMacosUserUnchangedRelaunchKeepsTheDeliveredFiles|TestMacosUserOverlayInstallKeepsTheAgentStateBesideItsSkills)$' ./integration
    ```

  - for the relaunch: the boot log's `home overlay: ~/… was delivered as a copy … refused (<err>)`
    line, verbatim; whether `sandbox-exec -p '(version 1)(allow default)(deny file-link)' /bin/ln a
    b` compiles, and refuses the link; and the same relaunch test in self mode, whether its
    delivered file is also a copy;
  - in an account-mode session: `echo $TMPDIR` names the account's `/var/folders/…/T/`;
    `getconf DARWIN_USER_TEMP_DIR` (paste its output, and `log show --last 2m --predicate 'sender
    == "Sandbox"'` if it prints nothing); `nix --version` prints no `$HOME … is not owned by you`
    line;
  - the [§5](../lab-reports/2026-11-03-mac-slice-12a-witness-w1.md#5-an-apple-container-home-blocks-a-later-account-mode-launch) case: a workspace launched with `YOLO_RUNTIME=container`, then `yolo -- true` with
    macos-user; paste the refusal, which names Apple Container and the `mv`;
  - the short suite: `go test -short ./integration -run
    '^(TestStopHomeProcesses|TestProcessEnvironReadsALiveChildsHome)'` passes, or skips naming
    the install;
  - `./run-checks.sh --mode account --account _yolojail --profile-from-yolo <dev yolo>` started
    while a `yolo -- sleep 60` runs in another workspace: no FAIL line, g4 included.
  *Run 2026-11-03 at `fd702e9fb`* ([lab report §10](../lab-reports/2026-11-03-mac-slice-12a-witness-w1.md#10-rerun-witness-w1b-and-the-just-done-fixes-witness)):
  every item came back as expected. The relaunch's errno is `operation not permitted`.
  `(deny file-link)` compiles and refuses the link, and `_yolojail` links the staged file outside
  Seatbelt, so the profile is the only obstacle. Self mode delivers copies too (link count 1, the
  inode moves). The short-suite guard checks only `sh`, while two of the tests still run
  `/bin/sleep`.
- **W1c ([MMP-D35](#MMP-D35), the home overlay delivers copies):** on the branch carrying it, the
  owner's Mac agent runs exactly:

  ```
  YOLO_TEST_MACOS_USER=1 go test -count=1 -timeout 0 -v -run '^TestMacosUserUnchangedRelaunchKeepsTheDeliveredFiles$' ./integration
  ```

  Expected: PASS, with both subtests (`/account` and `/self`) passing: the delivered skill's inode
  is the same across the unchanged relaunch, its bytes equal the staged file's, and a copy edited
  between launches holds the staged bytes again after the next launch. The workspace's
  `.yolo/boot.log` has no `home overlay: ~/… was delivered as a copy … refused` line (the test
  checks it; paste `rg -n 'delivered as a copy' <ws>/.yolo/boot.log` printing nothing, from a
  manual `yolo -- true` relaunch, as the record).
  *Run 2026-11-04 at `3ccc28def`* ([lab report](../lab-reports/2026-11-03-mac-slice-12a-witness-w1.md#w1c-passes)):
  PASS, `/account` and `/self`, and the manual relaunch's `rg` printed nothing. W1's `claude -p`
  line also answered `ok` on the Bedrock profile; the subscription login is still revoked.
  *2026-11-04, with admin for `log show`* ([lab report](../lab-reports/2026-11-03-mac-slice-12a-witness-w1.md#why-copilot-cannot-reach-github-the-account-mode-now-denies-securityserver)):
  W1's Sandbox-log line passes. **New:** the account-mode profile denies Copilot
  `com.apple.SecurityServer` and the system trust settings, so Copilot 1.0.105 cannot make HTTPS
  requests in account mode: #65's failure, until now run-as-you's alone.
- **W2 (slice 11's precondition 3, the rows not yet recorded):** with `macos_user_mode = "self"` in
  the user config, `yolo -- claude` in a fresh `~/code/alpha` twice; Claude is logged in both times
  without a `/login`; a second workspace runs at the same time; `pip download requests` inside
  writes into the host's `~/Library/Caches/pip`. Paste as a dated run beside W1.
  *Run 2026-11-05* ([lab report](../lab-reports/2026-11-04-host-workspace-skills-link-and-ssh.md#9-2026-11-05-the-claude-subscription-lines-of-w1-and-w2-pass)),
  through `YOLO_SETUP=seatbelt-self` and no config edit: **passes**. Claude answered on both
  `~/code/alpha` launches with no `/login`, and `~/code/beta` ran at the same time. The pip line
  passed with Homebrew's pip, because the sandbox has no working one
  ([lab report, W2's cache line](../lab-reports/2026-11-04-host-workspace-skills-link-and-ssh.md#w2s-cache-line-mmp-d12-run-as-you-mode-shares-host-caches)).
  W1's `claude -p` on the subscription also answered in both of its workspaces.
- <a id="W-TMP1"></a>**W-TMP1 ([MMP-D29](#MMP-D29), [MMP-D36](#MMP-D36): the account mode's shared
  `/tmp` is closed, with the hatch):** at the first main commit that contains MMP-D36 (`git log -1
  --format=%h -S MMP-D36 -- docs/design/macos-user-model-plan.md`) or later. Setup: the installed
  `yolo` built from that commit, a fresh workspace `/Users/Shared/yolo/tmp1`, and no
  `macos_user_shared_tmp` key in the user config. In the workspace, with
  `YOLO_SETUP=seatbelt-yolo-account`, start `yolo -- bash` and run, one at a time:

  ```
  echo "$TMPDIR"
  ```

  ```
  touch "$TMPDIR/x" && echo OK
  ```

  ```
  touch /tmp/x
  ```

  ```
  echo "$TMUX_TMPDIR $TMPPREFIX"
  ```

  Expected: the first prints the account's own `/private/var/folders/…/T`; the second prints `OK`;
  the third fails with `touch: /tmp/x: Operation not permitted`; the fourth prints the same
  directory, then that directory followed by `/zsh`. The agent briefing (for
  Claude, `~/.claude/CLAUDE.md`) contains the line starting "The shared `/tmp` (`/private/tmp`) is closed
  to this sandbox". Exit, then on the host:

  ```
  yolo config set macos_user_shared_tmp true --user
  ```

  Relaunch `yolo -- bash` the same way. The launch prints a line starting `tmp: the shared /tmp is
  open to the sandbox`. Inside, `touch /tmp/x && echo OK` prints `OK`, and the briefing no longer
  has the closed-`/tmp` line. Then remove the key again (`yolo config unset
  macos_user_shared_tmp --user`) and delete `/tmp/x` (`sudo -n --user=_yolojail /usr/bin/env
  /bin/rm -f /tmp/x`). Also run the Mac-only tests this change edited, and paste their result:

  ```
  YOLO_TEST_MACOS_USER=1 go test -count=1 -timeout 0 -v -run '^(TestMacosUserSerialClientDrivesAHostPtyFromTheSandbox|TestMacosUserCopiesAPackFileMountIntoTheContextDir|TestMacosUserHostFloorIsTheHostUsersAlone)$' ./integration
  ```

  and `TestMacosUserMacosLogBridgeScopesToTheSandbox` the same way. Report: the
  four outputs, the refusal text, the launch's `tmp:` line, the test results, and
  `log show --last 5m --predicate 'sender == "Sandbox"'` lines naming `/private/tmp` if any tool
  you use failed. It decides whether [MMP-D29](#MMP-D29)'s closing stands for the account mode.
- **W3 (slice 12c):** two account-mode workspaces with different `cache_relocations` or alias sets
  launched in turn; A's alias links under `/Users/_yolojail/.cache` survive B's launch.

## Blockers

Only slice 11 is blocked. The rest build without the rulings, because each pending decision is a
parameter ([MMP-D13](#MMP-D13)).

- **Loopback (check d3)** and **the shared `/tmp` (check b14)** are ruled:
  [OQ-MM7](macos-user-model.md#OQ-MM7) ([MMP-D14](#MMP-D14)) and
  [OQ-MM8](macos-user-model.md#OQ-MM8) ([MMP-D18](#MMP-D18)), both built and not yet run on a Mac.
- **Host software (Homebrew) visibility**: the prototype allows `/opt/homebrew` read-only (and
  `/usr/local`, through `/usr`), and the first run passed with it. This plan keeps that as
  `productionHostSoftware` and marks it pending. It is not filed in the design.

## Open items

None of these blocks a slice, and none is fixed yet.

- **Nix builds run outside Seatbelt.** A `nix build` in the sandbox hands the build to the host's
  nix daemon, which runs it as the daemon's build users under no session profile, so a derivation
  the agent writes can read what those users can.
- **The per-user cache dir and `/private/var/tmp` are closed** ([MMP-D19](#MMP-D19)). Grant
  either in `DenyDefaultProfile` only when a Mac run shows a real tool needs it.
- **One workspace can sweep another's alias links.** The cache-alias manifest sits in the shared
  machine root, so a launch of one workspace reads and may remove alias links another workspace
  laid there. Slice 12c closes it for both modes ([MMP-D27](#MMP-D27)).
