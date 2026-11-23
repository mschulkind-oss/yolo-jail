---
title: "Mac native run: sandbox-home collisions and two false check warnings"
date: 2026-10-30
tags: [lab-report, macos, macos-user, check]
summary: "Dangling layout links left by integration runs, and a pack's files delivered into another pack's state folder, refuse real macos-user launches; two yolo check warnings name fixes that do not apply."
---

# Mac native run: sandbox-home collisions and false check warnings

**Run 2026-10-30 on the maintainer's Mac** (macOS 26.5, arm64). The source is `main` at
`a8a96bd66`, deployed with `just deploy` as `yolo-jail 0.15.1+889.ga8a96bd665a2`. The run follows the
2026-10-29 fix checks (retired; the open finding is in [BACKLOG](../plans/BACKLOG.md#macos-unshared-parent-loop)).

**Terms.**

- *macos-user* is the backend that runs the agent under macOS Seatbelt as one dedicated account,
  `_yolojail`, whose home is `/Users/_yolojail` ([macOS backends](../../userguide/guides/macos.md)).
- The *account home* is that home. Every workspace's launch on this Mac shares it.
- A *layout link* is a symbolic link the launch's `darwin_home_layout` boot step lays in the
  account home, from a selected pack's state folder (such as `~/.gemini`) into the workspace's own
  sidecar, `<workspace>/.yolo/home/…`
  ([home tiers](../reference/macos-user-home-tiers.md)).
- The *overlay install* is the `install_home_overlay` boot step, which copies skills, briefings
  and pack `files` contributions into the account home.

## 1. Results

| # | Finding | Kind |
| :--- | :--- | :--- |
| 1 | Integration runs leave dangling layout links in the real account home, and one refused a real launch | Defect ([§2](#2-dangling-layout-links-from-integration-runs)) |
| 2 | A pack's `files` delivered into a folder another pack keeps state in creates a real folder there; every later launch selecting the owning pack is refused, and the refusal says to delete the whole account home | Defect ([§3](#3-a-files-delivery-into-another-packs-state-folder)) |
| 3 | `yolo check` says aws-auth names no profile when the profile is set in the legacy JSONC spelling | False warning ([§4](#4-false-check-warnings)) |
| 4 | `yolo check` blames `only`/`exclude` filters for an empty conventional local pack, which has neither | False warning ([§4](#4-false-check-warnings)) |

None is fixed. The maintainer's `matt` pack update fixed `matt/pi-subagents`; `matt/pi-archimedes`
and `matt/pi-session-search` still fail, now as patch branches that do not merge into v2.12.0 and
v1.6.1 ([§5](#5-also-seen)).

## 2. Dangling layout links from integration runs

A real launch from a fresh workspace under `/Users/Shared/yolo`, with `{}` as its config and the
maintainer's user config, stopped at the overlay install:

```
Error: install_home_overlay: nothing was delivered at /Users/_yolojail/.gemini: it is a symbolic link in the account home that is not one of this launch's layout links to a directory — typically another workspace's, for a pack this launch does not select — […] Remove the link (what it points at is left alone):
  sudo rm /Users/_yolojail/.gemini
```

The account home held three layout links whose targets no longer existed. All three point into
integration-test workspaces (`yolo-it-*`), which each test deletes when it ends:

| Link | Target | Link time |
| :--- | :--- | :--- |
| `.gemini` | `/Users/Shared/yolo/yolo-it-1873416182/.yolo/home/gemini` | Oct 18 14:13 |
| `.mptreeagent` | `/Users/Shared/yolo/yolo-it-415317130/.yolo/home/mptreeagent` | Oct 27 01:05 |
| `.oh-omp` | `/Users/Shared/yolo/yolo-it-1034033264/.yolo/home/oh-omp` | Oct 29 23:00 |

The `.oh-omp` link's time matches the native run of
`TestMacosUserOverlayInstallKeepsTheAgentStateBesideItsSkills` in the previous report: that test
selects the `omp` pack, so its launch lays `~/.oh-omp` into the test's temporary workspace. No
test removes the link it laid. `integration/macosuserhomeroothostfiles_test.go` already uses a fixed
name for its own home-root link so that each run reuses it and does not add another dangling link.
The layout links have no such rule.

The link blocked this launch because the maintainer's `matt-agentwork` pack delivers a `files`
contribution into `.gemini/config/agents`, and no pack this launch selects owns `.gemini`. Yesterday's
launch with the same link and the same pack exited 0; this run did not find out what changed.

The maintainer removed the three links with `sudo rm`, and the same launch then exited 0.

## 3. A `files` delivery into another pack's state folder

After the links were removed, the passing launch created a real folder,
`/Users/_yolojail/.gemini/config/agents/design-research.md`, owned by `_yolojail`. The shipped
`agy` pack declares `.gemini` as its state folder (`[[state]] at = ".gemini"` in
[`packs/agy/pack.toml`](../../packs/agy/pack.toml)), so a launch selecting `agy` lays a layout link
there.

A throwaway integration test (deleted after the run) selected `agy` through `packHome` and
launched once with `runMacosUser`. The launch was refused, rc 1:

```
Error: darwin_home_layout: the per-workspace home layout cannot be laid: 1 path is real, where the workspace tier's symlink belongs.
[…]
In the SANDBOX ACCOUNT HOME, which predates this layout:
  /Users/_yolojail/.gemini
Move what you want to keep, then reset the account:
  sudo rm -rf /Users/_yolojail
```

- **The next step deletes far more than it needs to.** Only `.gemini` is in the way. The advice comes
  from `occupiedLayoutError`
  ([`darwinhomelayout.go`](../../internal/entrypoint/darwinhomelayout.go#L439-L451)), which assumes a
  real folder in the account home is one that "predates this layout". Here a pack the user selects
  made it.
- **The refused launch still wrote into the real folder.** After the refusal,
  `/Users/_yolojail/.gemini/antigravity-cli/` held agy's generated `settings.json`,
  `mcp_config.json`, `cache/onboarding.json` and the `antigravity-oauth-token` link to the shared
  credential folder. These came from steps that kept running after `darwin_home_layout` failed.
  None of it is a credential.
- **Nothing warns ahead of time.** No check reports that a selected pack's `files` destination
  sits inside a folder that another shipped pack declares as state.

The maintainer's config does not select `agy` (the launch reports `agy/settings has no owner (the
agy pack is not selected)`), so their own launches are not affected today. If they select it later,
it will be refused until `/Users/_yolojail/.gemini` is removed.

The fix is designed in [`pack-files-for-agent-state.md`](../design/pack-files-for-agent-state.md).

## 4. False check warnings

`yolo check --no-build` on the maintainer's config:

```
[WARN] ~/.dotfiles/tag-work/config/yolo-jail/work.jsonc:25:12: packs lists aws-auth, but [allow.aws-auth] names no profile, so it serves nothing.
     -> Set one: yolo config set allow.aws-auth.profile <value>
```

`work.jsonc` sets `loopholes.aws-auth.settings.profile` to `hssandbox-BedrockInference`, and the
same check's loophole self-test uses that profile (it reports that profile's SSO session as
expired). `AllowPackNudges`
([`readableallow.go`](../../internal/config/readableallow.go#L1442-L1491)) reads only the new
`allow.<pack>` table. It is a warning in `yolo check` only: `AllowPackSwitchErrors`, the launch's
caller, skips this kind.

```
[WARN] local: stages 0 files
     -> Its `only`/`exclude` filters match none of its files. Fix them in its `packs` entry in /Users/matt/.config/yolo-jail/config.jsonc […]
```

`~/.config/yolo-jail/local` is an empty folder. `localPackEntry`
([`packs.go`](../../internal/config/packs.go#L376-L387)) adds it as an implicit pack whenever the
folder exists. So it has no `packs` entry and no filters, and the fix the warning names does not
exist. The branch is in [`check/packs.go`](../../internal/cli/check/packs.go#L279-L284). The working
fixes are to put content in the folder or remove it.

## 5. Also seen

- **Patch series.** With no hatches, `matt/pi-archimedes` (conflicts in three `package.json` files
  and `pnpm-lock.yaml`) and `matt/pi-session-search` stop the launch. Their refusals now offer
  `YOLO_USE_CACHED_GOOD=<name>`, which runs the last good build; the launch passed with
  `YOLO_USE_CACHED_GOOD=matt/pi-archimedes,matt/pi-session-search`.
- **The integration warmup hits the unshared-parent loop.** The suite's warmup workspace is under
  `$TMPDIR`, so on macos-user it gets the false "MOVED or copied into /Users/Shared/yolo" refusal from
  [the BACKLOG entry for the 2026-10-29 finding](../plans/BACKLOG.md#macos-unshared-parent-loop),
  and the suite logs `DEGRADED: warmup jail failed`.
- **Expired sign-ins in `yolo check`.** The Claude shared credentials and the AWS SSO session for
  `hssandbox-BedrockInference` had expired. Both need the maintainer's own sign-in.

## 6. Cleanup

- Removed the throwaway workspace `/Users/Shared/yolo/launch-check` and the throwaway test file.
- Left in place, because removing them needs sudo: `/Users/_yolojail/.gemini`, holding
  `matt-agentwork`'s `config/agents` and the `antigravity-cli` files from the refused `agy` launch.
  `sudo rm -rf /Users/_yolojail/.gemini` removes it. The next `matt-agentwork` launch recreates
  `config/agents`.
