---
title: "Mac run: the per-session-profiles checks, a pack add/remove mismatch, and the first nightly after the triage fixes"
date: 2026-11-07
tags: [lab-report, macos, macos-user, ci, per-session-profiles, config]
summary: "At 4b26d1dfc the three per-session-profiles Mac checks pass, apart from line order. yolo pack add and yolo pack remove choose different user-config files. Nightly 42372802408 has four failed test shards; the CircleCI broker test fails on every Mac because the test moves XDG_CONFIG_HOME away from podman's connection list."
---

# Mac run: session homes, a pack add/remove mismatch, and nightly 42372802408

**Run 2026-11-07 on the owner's Mac** (darwin/arm64). Everything ran at main `4b26d1dfc`, from
dev bundle `650bd6dc8ede2c25…/bin/yolo`, which reports `yolo-jail 0.17.0+82.g4b26d1dfcfb2`.
Nothing in yolo was fixed. The owner approved adding the `omp` pack to their user config for the
duration of the checks; it was removed afterwards ([§2](#2-yolo-pack-add-and-yolo-pack-remove-choose-different-files)).

## Observed

| Item | Result |
| :--- | :--- |
| [per-session-profiles §13](../design/per-session-profiles.md#13-mac-checks) check 1 | **Passes, except the order**: `pi` is listed first, while the witness expects `oh-omp` first |
| Check 2 | **Passes** |
| Check 3 | **Passes** |
| `yolo pack add omp`, then `yolo pack remove omp` | **The remove wrote nothing**: add wrote to a drop-in file and remove looked only in `config.toml` |
| Nightly 42372802408 (`4b26d1dfc`) | Still running when written. Four shards had failed tests and three runners lost communication; see [§3](#3-nightly-42372802408) |
| `TestCircleCIFakeHostCLIUsesRealClassifier` | **Fails on this Mac too**: a test defect, cause confirmed |
| Triage [W5](2026-11-06-mac-ci-triage-witnesses.md#3-the-version-stamp-on-a-tag-commit) | **Still open**: the stamp test passed, but on the off-tag form `0.17.0+82`, not on the tag commit |
| Triage [W6](2026-11-06-mac-ci-triage-witnesses.md#4-the-60-minute-step-cap) | **First reading: the longest test step was 59 min** (shard 5), inflated by one 20-minute test timeout |

### 1. The per-session-profiles checks

The workspace was `/Users/Shared/yolo/sp-check`, launched with
`YOLO_SETUP=seatbelt-self YOLO_ALLOW_MISSING_PROGRAMS=1`. `pi` and `omp` were both selected,
through the user config. A project-scope `packs` is refused: *"config.packs: user-scope only;
move this key to the user config"*. No check started an agent session or called a model.

**Check 1.** `printf "%s\n" "$YOLO_SESSION_HOMES"` printed two lines; tabs are shown here as
`<TAB>`, and `<ws hash>` is
`c74d885a10f23eb878636aac252307898eadfbef79e20367163785ec3db0f3ea`:

```text
pi<TAB>PI_CODING_AGENT_DIR<TAB>/Users/Shared/yolo/sp-check/.yolo/setups/seatbelt-self/home/.yolo-native-sessions/<ws hash>/e07faa8b1aafc2a6/.pi/agent
oh-omp<TAB>PI_CODING_AGENT_DIR<TAB>/Users/Shared/yolo/sp-check/.yolo/setups/seatbelt-self/home/.yolo-native-sessions/<ws hash>/e07faa8b1aafc2a6/.oh-omp/agent
```

- The second launch printed the same two lines with the id `c20008e7bd5a1bd5`, so the id differs
  per launch, as expected.
- The paths sit under `$HOME/.yolo-native-sessions/<ws hash>/<16-hex id>/`, and `$HOME` is the
  setup's home, as expected.
- **Order.** `pi` comes first. The witness says *"`oh-omp` first"*. Every other part matches.
  Whether the order is meant to be fixed is not stated in
  [SP-D36](../design/per-session-profiles.md#SP-D36); the lines are keyed by program, so the
  order may not matter.
- The first launch took 134 s, much of it installing `@oh-labs/oh-omp@0.15.3`, and the second
  took 57 s.

**Check 2.** In one launch, with id `cb930be26c82a27d`, `bash -x` on each launcher with
`--version` traced:

```text
-- oh-omp
+ export PI_CODING_AGENT_DIR=…/.yolo-native-sessions/<ws hash>/cb930be26c82a27d/.oh-omp/agent
-- pi
+ export PI_CODING_AGENT_DIR=…/.yolo-native-sessions/<ws hash>/cb930be26c82a27d/.pi/agent
```

Each launcher points the variable at its own program's home, and the two homes are the ones
`YOLO_SESSION_HOMES` names in that launch.

**Check 3.** `ls -l` of the oh-omp session home showed:

- `config.yml` and `models.yml` are regular files, 4 bytes each.
- `agent.db` is a link to `/Users/Shared/yolo/sp-check/.yolo/setups/seatbelt-self/home/.oh-omp/agent/agent.db`.
  In this session `~` is the setup's home, so that is `~/.oh-omp/agent/agent.db`, as expected.
- The link's target does not exist yet, because no omp session has run.
- Thirty-one other entries are links into the same directory.

pi's session home has no `models.yml` and no `config.yml`. It has regular `mcp.json`,
`models.json`, `settings.json`, `yolo-model-lists.json` and `yolo-openai-codex-models.json`.

### 2. yolo pack add and yolo pack remove choose different files

This user config is split between `~/.config/yolo-jail/config.toml` and
`~/.config/yolo-jail/config.d/work.toml`. `work.toml` is a link into the owner's dotfiles, and
`yolo config why packs` reports *"set in ~/.config/yolo-jail/config.d/work.toml:2:9, under it,
also set in ~/.config/yolo-jail/config.toml:5:9"*.

- `yolo pack add omp` appended `"omp"` to **`config.d/work.toml`**:
  *"Added "omp" (shipped with yolo)."*
- `yolo pack remove omp` then said *"yolo pack remove: "omp" is not in
  /Users/matt/.config/yolo-jail/config.toml; nothing written. `yolo pack ls` lists the packs a
  launch reads and where each is listed"*, and exited without writing.
- `yolo config why packs` still listed `omp`. The dotfiles diff was exactly the one appended
  item, so it was reverted with `git checkout` of that file. `config.toml` was byte-identical
  to a copy taken before the add.

The cause is in `internal/cli/packadd.go`. Add resolves its target from an `Append` mutation
(`:102`). Remove resolves it from a probe `Remove` of `packs[0]` (`:153`). Both go through
`configedit.ResolveTarget` with `EditTargetUser`, which picks a different file for each.

Two failures follow from this:

- A pack that `yolo pack add` placed cannot be taken out by `yolo pack remove`.
- The remove's message says the pack "is not in" `config.toml` but does not say where it is,
  although `yolo config why packs` can.

**Decision needed (owner):** which file both verbs should edit when the `packs` key is set in
more than one user file. Whatever the answer, the remove's message should name the file that
holds the pack.

### 3. Nightly 42372802408

This was the first scheduled Nightly macOS Integration run since the triage fixes, at
`4b26d1dfc`. It was still in progress when this was written: every shard had finished, and
`archive-delivery-macos` was running.

| Shard | Failure | Rerun on this Mac (`YOLO_SETUP=podman-self`) | Class |
| :--- | :--- | :--- | :--- |
| 2 | `TestCircleCIFakeHostCLIUsesRealClassifier` (2.2 s): `yolo check` failed with *"Container runtime installed but not started … unable to connect to Podman socket: … /var/folders/…/T/storage-run-501/podman/podman.sock: connect: no such file or directory"* | **Fails the same way** (1.9 s) | Test defect, below |
| 5 | `TestAJailLaunchesOnTOMLFiles` (1206 s): *"yolo timed out after 20m0s: yolo run --accept-config-changes -- bash -lc command -v hello && echo HELLO-ON-PATH"*, with `podman run … yolo-005-…` still running | **Passes** (15.9 s) | Unknown. It is another test that asks for `hello` (`readableconfig_test.go:69`); see triage [§Open 1](2026-11-06-mac-ci-triage-witnesses.md#1-the-hello-image-build-on-the-mac) |
| 18 | `TestBedrockTakesTheRegionOfTheHostsAWSConfig` (263 s): *"opencode: preparing it failed … selected sealed npm producer failed (status 1)"* | **Passes** (25.4 s) | Unknown; the log quotes only the producer's exit status |
| 18 | `TestPackagesEntryInstallsWhatNixBuildBuilds/an_output_of_a_top-level_package`: the eval for `gtk4.dev` timed out downloading from `cache.nixos.org` and `api.github.com` | Not rerun | Infra (the network) |
| 12, 14, 15 | *"The hosted runner lost communication with the server"* | — | Infra |

**The CircleCI test's cause.** The test runs `yolo check` with
`XDG_CONFIG_HOME=<t.TempDir()>/xdg` (`integration/circleci_broker_test.go:30`). On macOS, podman
finds its machine through `~/.config/containers/podman-connections.json`, under
`XDG_CONFIG_HOME`. With that variable moved, podman falls back to a local socket that does not
exist. Reproduced directly on the host:

```text
$ XDG_CONFIG_HOME=$(mktemp -d) podman info
Error: unable to connect to Podman socket: Get "http://d/v6.1.4/libpod/_ping": dial unix /var/folders/j6/…/T/storage-run-501/podman/podman.sock: connect: no such file or directory
$ podman info --format '{{.Host.Arch}}'
arm64
```

It is the same class as `95514c8c5`, where a test's HOME dropped what podman needs. That fix's
guard, `TestNoJailTestPointsHOMEAtABareTempDir`, checks HOME, not `XDG_CONFIG_HOME`. Linux root
podman reads no per-user connection, so this test passes there.

**W5.** `TestTheJailsYoloCarriesItsMatchedBundleStamp` passed in shard 18 (75.8 s). The commit is
`0.17.0+82`, the off-tag form. The tag `v0.17.0` points at `6ac121c04`, and no nightly has run
that commit, so the on-tag form still has no witness.

**W6.** Integration-step times, in whole minutes, for the 15 shards whose step finished:

| Shards | Minutes |
| :--- | :--- |
| 5 | **59**, including the 20-minute timeout above |
| 1 | 37 |
| 6 | 35 |
| 16 | 33 |
| 18 | 32 |
| 8 | 30 |
| 10 | 28 |
| 4, 17 | 25 |
| 9 | 22 |
| 11 | 20 |
| 7 | 17 |
| 3, 13 | 16 |
| 2 | 13, ending at the failure |

Shards 12, 14 and 15 lost their runner. Without the timeout, shard 5 would have run about
39 minutes, so the 55-minute line the triage report sets is crossed only because of a hung test.
This is one run of the two that W6 asks for.

## Fixed and committed

Nothing in yolo. The owner's dotfiles are as they were (`work.toml` checked out). This report
is the only commit.

## Open

1. [§2](#2-yolo-pack-add-and-yolo-pack-remove-choose-different-files): which user file `yolo pack
   add` and `yolo pack remove` edit when `packs` is in two files. (Owner.)
2. `TestCircleCIFakeHostCLIUsesRealClassifier` fails on every Mac while the test moves
   `XDG_CONFIG_HOME`; [§3](#3-nightly-42372802408) has the cause.
3. `TestAJailLaunchesOnTOMLFiles` hung for 20 minutes on CI but passes here in 16 s, which
   matches the `hello` build trouble the triage report leaves open.
4. `TestBedrockTakesTheRegionOfTheHostsAWSConfig` failed on CI with only *"status 1"* from the
   sealed npm producer, and passes here. Its producer log was not in the job output.
5. Check 1's line order: whether `oh-omp` must come first.
6. W5 needs a nightly on the tag commit `6ac121c04`; W6 needs one more scheduled run.

## Evidence

- The three checks: `yolo -- bash -lc …` as in
  [per-session-profiles §13](../design/per-session-profiles.md#13-mac-checks), run three
  times in total, every launch `rc=0`.
- The nightly: `gh run view -R mschulkind-oss/yolo-jail 42372802408 --json jobs`, the jobs API
  for step times, and `gh api --allow-escape-sequences …/actions/jobs/<id>/logs` for jobs
  128404825146 (shard 2), 128404825084 (shard 5) and 128404826704 (shard 18). The check-run
  annotations covered jobs 128404825189, 128404826529 and 128404826502 (shards 12, 14 and 15).
- The local reruns: `YOLO_SETUP=podman-self go test -count=1 -timeout 0 -v -run '^<test>$'
  ./integration` for each of the three tests in [§3](#3-nightly-42372802408).
