---
title: "RUNBOOK — turn a Mac into the Apple Container CI runner"
status: current
date: 2026-09-14
tags: [ci, macos, apple-container, self-hosted, runbook]
summary: "The one-time procedure for registering a maintainer's Mac as the self-hosted runner apple-container.yml has been waiting for. Covers the fine-grained PAT the poll job needs, the runner registration and its four labels, Apple Container's per-user apiserver, and the account decision — including the launchd constraint that rules out the hidden service account pattern the rest of this repo uses."
---

# RUNBOOK — turn a Mac into the Apple Container CI runner

**Audience:** the maintainer, at the Mac. **Time:** ~20 minutes, most of it waiting on
GitHub's UI. **Needs:** admin on the Mac, admin on the repository.

**What it buys.** [`apple-container.yml`](../../../.github/workflows/apple-container.yml) is
written, merged and inert: it polls every two hours for a runner that does not exist, finds
none, and skips green. This procedure is the missing half. Apple Container is **the backend
no CI job has ever run** — the README recommends it for macOS, and both defects ever found
in it (#39, #44) were found by a human on hardware, eight months apart.

> [!IMPORTANT]
> **Read the workflow's own header before this file.** It states the design — why
> self-hosted is forced rather than preferred, why the poll is a poll and not a queue, and
> the fork-safety rule. This runbook does not restate any of it; it is the *procedure*.

## 0. The account decision, and the constraint that drives it

**This repo's usual pattern does not work here.** Every service account on the maintainer's
Mac is a hidden one — `sandvault-matt` (uid 601) and `_yolojail` (uid 602) are both
`IsHidden: 1`, matching `macosuser.CreateUserCommands`. That pattern is right for yolo's own
macos-user backend, which reaches its account through `sudo` and `sandbox-exec`.

It is wrong for a runner, and the reason is **launchd**:

- GitHub's `svc.sh` on macOS installs a **LaunchAgent** at
  `~/Library/LaunchAgents/actions.runner.<owner>-<repo>.<name>.plist`.
- Apple Container's apiserver is likewise **per-user** — its app-root is
  `~/Library/Application Support/com.apple.container`, and `container system status` answers
  for the invoking user only.
- A LaunchAgent needs the user's launchd domain, and **a session-less account has none.**
  Measured 2026-09-14 on macOS 26.5: `launchctl print user/602` and `gui/602` both report no
  such domain, while `user/501` (the logged-in human) exists.

So a hidden `_ghrunner` can host neither the runner service nor the apiserver. Three ways
out, and the trade is isolation against friction:

| Option | Works? | Trade |
| :--- | :--- | :--- |
| Hidden service account | **No** | Blocked by the above. Do not spend time here. |
| Dedicated *loginable* account | Yes | A job cannot read the human's home. Costs: add it to nix `trusted-users`, log in once per boot (or enable auto-login), start its own apiserver. |
| The maintainer's own account | Yes | Zero setup — nix trust, apiserver, Go and PATH all already work. A job runs with full read access to that home. |

**The maintainer's own account was chosen (2026-09-14), and the reasoning is recorded
because it is only valid while its premise holds:** the triggers are `schedule` and
`workflow_dispatch`, neither of which a fork can cause, and `workflow_dispatch` needs write
access — which one person has. So the code that executes is code that person merged, which
is what they already run locally as themselves.

> [!WARNING]
> **That premise is now enforced, not trusted.**
> `integration/selfhostedtriggers_test.go` fails if any workflow targeting a `self-hosted`
> runner declares a trigger a fork can cause, and fails if such a workflow has no
> `github.repository ==` guard. It is keyed on the `self-hosted` label rather than on a
> filename, so a workflow added later is covered without anybody remembering. It runs under
> `-short`, on every push.
>
> Two residual differences from "pull and run it myself", worth knowing rather than fixing:
> a scheduled run is **unattended** (including on commits an agent pushed), and the actions
> are on **mutable tags** (`actions/checkout@v7`, `actions/setup-go@v7`) rather than pinned
> SHAs.

## 1. The probe token (do this first — the job is inert without it)

The poll job asks `GET /repos/{owner}/{repo}/actions/runners`, which requires **admin**
access. `administration` is not a scope `permissions:` can grant to `GITHUB_TOKEN`, so a PAT
is required and the built-in token cannot substitute.

⚠ An existing PAT will not do unless it carries this permission. Verified 2026-09-14: the
maintainer's working `GH_TOKEN` returns `403 Resource not accessible by personal access
token` on that endpoint — the same answer the workflow's probe would report as "cannot ask",
and it then declines and skips **green**. A missing permission looks exactly like a Mac that
is switched off.

1. GitHub → Settings → Developer settings → **Fine-grained tokens** → Generate new token.
2. Repository access: **only `mschulkind-oss/yolo-jail`**.
3. Repository permissions: **Administration: Read-only**. Nothing else.
4. Repo → Settings → Secrets and variables → Actions → New repository secret:
   name **`YOLO_RUNNER_PROBE_TOKEN`**, value the token.

Check it before going further:

```console
$ GH_TOKEN=<the new token> gh api repos/mschulkind-oss/yolo-jail/actions/runners -q .total_count
0
```

`0` is the correct answer here — the runner does not exist yet. A `403` means step 3 did not
take.

## 2. Apple Container's apiserver

Installed is not running. On a fresh Mac `container system status` reports *"apiserver is not
running and not registered with launchd"*, and every test in the subset would fail on it.

```console
$ container --version
$ container system start          # registers the per-user launchd agent
$ container system status
```

`container system start` **prompts to install a kernel** unless given
`--enable-kernel-install` or `--disable-kernel-install`. Answer it by hand now, interactively,
rather than discovering it inside a CI step that has no terminal.

> [!NOTE]
> **Record the version — the workflow asks for it on purpose.** Its `Runner facts` step
> prints `container --version` because that version decides half of what this job measures
> (the `:ro` question especially). The only Apple Container hardware result in this repo's
> research is **0.12.3**; this Mac carries **1.1.0** (2026-09-14), so the first green run is
> also the first data point on that line.

## 3. Register the runner

The labels are the contract. `apple-container.yml` selects
`runs-on: [self-hosted, macOS, ARM64, apple-container]`, **and the poll job separately greps
for the `apple-container` label** — two spellings of one fact that YAML cannot derive from
each other. Get one wrong and the job either never selects the machine or the poll never sees
it.

1. Repo → Settings → Actions → Runners → **New self-hosted runner** → macOS / arm64.
2. Run the commands it gives you (they carry a short-lived registration token). Install into
   its own directory, not a repo checkout:

```console
$ mkdir -p ~/actions-runner && cd ~/actions-runner
$ curl -o actions-runner-osx-arm64.tar.gz -L <URL from the UI>
$ tar xzf actions-runner-osx-arm64.tar.gz
$ ./config.sh --url https://github.com/mschulkind-oss/yolo-jail \
    --token <TOKEN from the UI> \
    --name <this-mac> \
    --labels apple-container \
    --work _work
```

`self-hosted`, `macOS` and `ARM64` are added by the runner automatically — **`--labels` adds
`apple-container` on top of them.** Passing all four is harmless; passing
`--labels self-hosted` *only* is not, because it replaces nothing but reads as if it did.

Then install the service:

```console
$ ./svc.sh install
$ ./svc.sh start
$ ./svc.sh status
```

## 4. Verify

```console
$ GH_TOKEN=<probe token> gh api repos/mschulkind-oss/yolo-jail/actions/runners \
    -q '.runners[] | "\(.name) status=\(.status) busy=\(.busy) labels=\([.labels[].name]|join(","))"'
<this-mac> status=online busy=false labels=self-hosted,macOS,ARM64,apple-container
```

All four labels must appear. Then drive it by hand rather than waiting for the poll:

```console
$ gh workflow run apple-container.yml --repo mschulkind-oss/yolo-jail --ref main
$ gh run watch
```

**Expect the `runner-check` job to say `an apple-container runner is online and idle`.** If it
says *"no YOLO_RUNNER_PROBE_TOKEN secret"* go back to [§1](#1-the-probe-token-do-this-first--the-job-is-inert-without-it); if it says *"no idle online runner
carries the apple-container label"* the labels are wrong, not the Mac.

### The step most likely to fail first, and it is not the runner

**`Realize the jail image (substituting from Cachix)`.** A Mac cannot *build* the Linux
closure — there is no Linux builder — so every derivation must be substituted. That became
possible only on 2026-09-13 (`1006fe6d`, pushing to Cachix on every nightly run rather than
only on a release tag), and **this route has never run on a Mac.**

Two prerequisites are easy to miss:

- **The user must be in nix `trusted-users`**, or `--accept-flake-config` silently ignores the
  flake's substituter and the build falls back to compiling a Linux closure it cannot compile.
  Check `nix store info` reports `Trusted: 1`. (On this Mac
  `trusted-users = root matt sandvault-matt` already, verified 2026-09-14 — a *new* dedicated
  account would have needed adding.)
- **The nightly must have pushed this commit's closure.** The image's whole input set is
  `flake.nix` + `flake.lock`, so any commit touching neither reuses the same closure — but a
  commit that moves either needs a nightly to have run since.

The workflow names its own fallback if this step is what breaks: copy `nightly-macos.yml`'s
shape — an ubuntu `build-image` job that uploads the tar, a download here, and
`container image load -i`.

> [!TIP]
> **There is a third option the workflow does not mention, already proven on hardware.**
> [`mac-ac-container-builder.md`](mac-ac-container-builder.md) stands up a **Linux builder
> using Apple Container itself**, zero sudo — proven 2026-07-17 on macOS 26.5 arm64, with the
> host nix reporting `Trusted: 1` and a proof build returning
> `AC-CONTAINER-BUILDER-WORKS`. That turns "cannot build a Linux closure" into a solved
> problem on this exact machine. Heavier than substituting, lighter than the artifact route,
> and it removes the Cachix dependency entirely.

## 5. Housekeeping this runner needs and a hosted one does not

**The runner is not ephemeral.** Every other macOS job in this repo runs on a fresh VM that
is discarded; here a leaked container or an unremovable workspace persists on an actual
laptop until the next run trips over it. The workflow's last step **reports** leftovers
without deleting them, deliberately — the fixtures clean up after themselves, so anything
still standing is evidence of one that failed to, and reaping it would destroy the only
trace. Read that step's output when a run is red.

To stop or remove the runner:

```console
$ cd ~/actions-runner && ./svc.sh stop
$ ./svc.sh uninstall            # keeps the registration
$ ./config.sh remove --token <a fresh removal token from the UI>
```

## What is deliberately not here

- **Whether Apple Container works at all.** It does —
  [`mac-ac-container-builder.md`](mac-ac-container-builder.md) and
  [`../../research/macos-support-matrix.md`](../../research/macos-support-matrix.md) carry the
  hardware results. This file is only about the runner.
- **What the subset tests assert.** `integration/applecontainer_test.go`'s header is the
  authority, including the rule for growing it: one test at a time, each added only after it
  has passed here.
- **The podman macOS suite.** `nightly-macos.yml` is pinned to `macos-26-intel` because
  GitHub's hosted Apple Silicon runners are themselves VMs and lack nested virtualization. A
  *real* Mac has Virtualization.framework, so this machine could in principle serve that job
  too — considered and deferred 2026-09-14, to get one green Apple Container run first.
