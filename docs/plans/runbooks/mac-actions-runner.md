---
title: "RUNBOOK — turn a Mac into the Apple Container CI runner"
status: current
date: 2026-09-14
tags: [ci, macos, apple-container, self-hosted, runbook]
summary: "The one-time procedure for registering a maintainer's Mac as the self-hosted runner apple-container.yml has been waiting for: the runner registration and the one label it needs, Apple Container's per-user apiserver, the optional launchd dispatcher that replaced a cron plus an admin PAT, and the account decision — including the launchd constraint that rules out the hidden service account pattern the rest of this repo uses. It needs no repository secret: a runner is an outbound client, so the Mac can answer 'am I online' locally for free."
---

# RUNBOOK — turn a Mac into the Apple Container CI runner

**Audience:** the maintainer, at the Mac. **Time:** ~20 minutes, most of it waiting on
GitHub's UI. **Needs:** admin on the Mac, admin on the repository.

**What it buys.** [`apple-container.yml`](../../../.github/workflows/apple-container.yml) is
written, merged and **inert** — it is dispatch-only, and there is no runner to dispatch it
onto. This procedure is the missing half. Apple Container is **the backend
no CI job has ever run** — the README recommends it for macOS, and both defects ever found
in it (#39, #44) were found by a human on hardware, eight months apart.

> [!IMPORTANT]
> **Read the workflow's own header before this file.** It states the design — why
> self-hosted is forced rather than preferred, why the scheduling lives on the Mac instead of
> in a cron, and the fork-safety rule. This runbook does not restate any of it; it is the
> *procedure*.

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
because it is only valid while its premise holds:** the only trigger is
`workflow_dispatch`, which a fork cannot cause and which needs write access — held by one
person. So the code that executes is code that person merged, which
is what they already run locally as themselves.

> [!WARNING]
> **That premise is now enforced, not trusted.**
> `integration/selfhostedtriggers_test.go` fails if any workflow targeting a `self-hosted`
> runner declares a trigger a fork can cause, and fails if such a workflow has no
> `github.repository ==` guard. It is keyed on the `self-hosted` label rather than on a
> filename, so a workflow added later is covered without anybody remembering. It runs under
> `-short`, on every push.
>
> Two residual differences from "pull and run it myself", worth knowing rather than fixing.
> The dispatcher in [§5](#5-optional--let-the-mac-dispatch-itself) is **unattended** if you install it — it fires on wake and hourly,
> including on commits an agent pushed — and the actions are on **mutable tags**
> (`actions/checkout@v7`, `actions/setup-go@v7`) rather than pinned SHAs. Skipping [§5](#5-optional--let-the-mac-dispatch-itself) removes
> the first one entirely: without it nothing runs that you did not type.

## 1. Nothing to do — the token is gone

**Earlier drafts of this runbook started with a fine-grained PAT
(`Administration: Read-only`) stored as the secret `YOLO_RUNNER_PROBE_TOKEN`.** It is no
longer needed and the secret should not be created.

Why it existed, and why it does not: the workflow used to carry a `schedule:` plus a hosted
`runner-check` job that asked `GET /repos/{owner}/{repo}/actions/runners` whether this Mac
was online, so a run could be *skipped green* rather than queued for 24 hours against an
absent runner. That endpoint needs admin access, and `administration` is not a scope
`permissions:` can grant to `GITHUB_TOKEN` — hence a PAT.

**But a runner is an outbound long-poll client.** It dials GitHub and holds the connection
open; that is why a self-hosted runner needs no inbound ports and works behind NAT. GitHub
knew the Mac was online *only because the Mac had told it*. So the question could be answered
on the Mac, for free:

```console
$ launchctl list | grep 'actions\.runner\.'
```

The schedule and the poll job are therefore both deleted, and the Mac dispatches instead
([§5](#5-optional--let-the-mac-dispatch-itself)). Verified 2026-09-14: the maintainer's existing `gh` login already carries the
`repo` scope, which is all `gh workflow run` needs — so this route adds **no credential at
all**.

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

The label is the contract: `apple-container.yml` selects
`runs-on: [self-hosted, apple-container]`, and a runner missing either is never chosen.
GitHub does not report that as an error — the run simply sits waiting for a runner matching
the labels, which reads like a machine that is off.

**`--labels apple-container` is the only one you pass.** `self-hosted` is added
automatically, and the OS/arch labels are deliberately NOT selected on: `apple-container` is
carried by one machine, so they narrowed nothing while adding two strings that must match
GitHub's spelling exactly. Verified 2026-09-14 that this is not a theoretical worry — the
runner's own `_diag` logs report `self-hosted`, `ARM64` and `apple-container`, but for the OS
they say `Darwin` and `OSX`, never `macOS`.

(An earlier design had the label written **twice** — once here and once in a hosted poll job's
`jq` — with no way for YAML to derive one from the other. Deleting the poll deleted that
duplication too; the dispatcher matches on the `actions.runner.` launchd label prefix, which
the runner installs itself and nobody has to keep in sync.)

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

`--labels` ADDS to the automatic ones rather than replacing them, so `apple-container` is all
you need. Add `--unattended` to skip the interactive prompts (runner group, name, work
folder — every default is fine).

Then install the service:

```console
$ ./svc.sh install
$ ./svc.sh start
$ ./svc.sh status
```

## 4. Verify

Check the runner locally — this is the same question the deleted poll job asked GitHub, and
it needs no credentials:

```console
$ launchctl list | grep 'actions\.runner\.'
-   0   actions.runner.mschulkind-oss-yolo-jail.<this-mac>
```

Then confirm GitHub agrees. The **Settings → Actions → Runners** page shows the runner and its
labels without a token; `gh` can only answer it with an admin PAT, which is exactly the
credential this design removed:

> Idle · `self-hosted` `macOS` `ARM64` `apple-container`

Extra labels there are fine — the job selects on two of them.

Now drive a run:

```console
$ gh workflow run apple-container.yml --repo mschulkind-oss/yolo-jail --ref main
$ gh run watch
```

**Expect the job to start within seconds, not to sit queued.** Queued means the runner is not
connected — check the `launchctl` line above — and **"waiting for a runner matching the
labels"** means a label is missing rather than the Mac being down, which is the failure
[§3](#3-register-the-runner) exists to prevent.

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

## 5. Optional — let the Mac dispatch itself

Everything above gives a runner you drive by hand. This makes it automatic, and it is the
half that replaced the cron plus the admin PAT.

The workflow has **no `schedule:`** — the scheduling lives here instead, because the Mac is
the only party that knows whether it is up. Two files, both committed:

```console
$ install -m 755 scripts/mac-runner-dispatch.sh ~/.local/bin/mac-runner-dispatch.sh
$ sed "s|__HOME__|$HOME|g" scripts/com.yolo-jail.mac-runner-dispatch.plist \
    > ~/Library/LaunchAgents/com.yolo-jail.mac-runner-dispatch.plist
$ launchctl bootstrap gui/$(id -u) ~/Library/LaunchAgents/com.yolo-jail.mac-runner-dispatch.plist
```

`RunAtLoad` plus `StartInterval 300`, so it fires when the Mac **wakes** and every five
minutes after — which a cron on GitHub's side could not do. Watch it:

```console
$ tail -f ~/.local/state/yolo-jail/mac-runner-dispatch.log
2026-09-14T11:55:51-0400  no actions.runner launchd agent is loaded, so a dispatch would
                          queue instead of running. Start it with: (cd ~/actions-runner && ./svc.sh start)
```

Three properties worth knowing, each a deliberate choice in the script:

- **It checks the runner locally before dispatching.** A dispatch onto a machine whose runner
  is not listening queues — the exact failure the poll existed to prevent — and the Mac being
  awake is necessary but not sufficient (`svc.sh install` may never have run).
- **It dispatches a commit at most once.** It records the SHA it dispatched, so an hourly
  agent does not re-run an unchanged `main` and spin the fans to re-prove a green result. A
  deliberate re-run is `gh workflow run` by hand, which is where a flake decision belongs.
- **Every outcome exits 0 and is logged.** launchd has no terminal, so an unlogged message is
  lost, and a non-zero exit from a periodic agent buys only noise in the system log.
  "Runner not running" and "nothing new" are ordinary states, not failures.

**Why five minutes is not aggressive**, measured 2026-09-14 on this Mac: a tick is ~0.4 s wall
and **~0.1 s CPU** (25 ms for the local `launchctl` check, ~370 ms for the GitHub SHA check,
almost all of it network wait). That is 288 ticks a day — roughly **29 seconds of CPU daily**,
and 12 API calls an hour against an authenticated limit of 5000. The interval bounds how fast
a new commit is noticed; it does **not** bound how many jobs run, because the script
dispatches a given SHA once. And launchd does not wake a sleeping Mac — a missed interval
fires once on wake — so it is only paid while the machine is already up.

Uninstall:

```console
$ launchctl bootout gui/$(id -u)/com.yolo-jail.mac-runner-dispatch
$ rm ~/Library/LaunchAgents/com.yolo-jail.mac-runner-dispatch.plist
```

## 6. Housekeeping this runner needs and a hosted one does not

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
