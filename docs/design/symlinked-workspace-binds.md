---
title: "A workspace reached through a symlink cannot be bound by podman on a Mac"
date: 2026-11-06
status: in-review
stage: DESIGN
next: "Rule OQ-SWB1; the repair in internal/cli/run waits on it, and its verification needs podman on a Mac"
tags: [podman, macos, symlinks, mounts, workspace, happy-path]
summary: "On a Mac, podman runs in a VM that cannot follow the host's /tmp → /private/tmp link, so a launch from /tmp/project dies on a bare statfs naming a path that exists. One ruling decides whether yolo resolves the paths it hands podman or refuses and names the resolved spelling."
---

# A workspace reached through a symlink cannot be bound by podman on a Mac

**Status:** 2026-11-06. Nothing built. Measured on a Mac on 2026-10-19
([the lab report's §3](../lab-reports/2026-10-19-launch-diagnostics-on-a-mac.md#3-a-podman-launch-cannot-use-a-workspace-reached-through-a-symlink)).
Any fix needs a podman launch on a Mac to verify; a Linux run cannot reproduce the VM's view.

> **In short.** The paths yolo hands podman as bind sources are yolo's own arguments, not the user's
> files, so spelling them the way the VM can reach them is not a guess about a link.

**Why it matters.** The launch dies with `Error: statfs /tmp/jail_test/.yolo/home/local: no such
file or directory`, naming a directory yolo created a moment earlier. Nothing in the message points at
the link, so it reads as a yolo bug, which is the symptom `preparePodmanBindSources` (in
`internal/cli/run`) exists to prevent.

**Needs your ruling:** [OQ-SWB1](#OQ-SWB1).

**Reads with:** [the 2026-10-19 lab report](../lab-reports/2026-10-19-launch-diagnostics-on-a-mac.md)
(the measurement), [`no-symlink-guessing-principle.md`](../reference/no-symlink-guessing-principle.md)
(the rule any answer must keep), [`jail-home.md`](../reference/jail-home.md) (the binds a launch
makes).

---

## 1. What resolves today, and what does not

A **bind source** is the host path on the left of a `-v <source>:<destination>` argument. A launch
makes several: the workspace itself at `/workspace`, and the overlay directories and files under
`<workspace>/.yolo/home` that `preparePodmanBindSources` creates.

- **Already resolved:** the jail's name (`runtime.FromWorkspace` resolves the workspace with
  `runtime.ResolveWorkspace`), the container tracking record (`runtimeWriteTracking`), and the
  sources of a user's own `mounts` entries (`resolvePath` in `internal/cli/run`).
- **Not resolved:** the workspace bind and every bind source under its `.yolo`, which keep the
  spelling the launch was started with.

On Linux the kernel follows the link, so both spellings bind. Podman on a Mac runs its containers in
a VM, and the VM cannot follow the host's `/tmp` link, so only the resolved spelling binds.

## 2. What any answer must keep

The [no-symlink-guessing principle](../reference/no-symlink-guessing-principle.md) governs moving or
writing a user's file: act on the name given, and never infer anything from where a link points.
Reading through a link is allowed. Resolving an argument yolo builds for podman reads through the link
and writes nothing; resolving the workspace itself, so that every path yolo records and prints changes
spelling, goes further, and is not offered below.

Every container backend takes the same answer ([backend parity](backend-parity.md)):
a resolved spelling binds on Linux too, so no backend needs its own branch.

## Open Questions

1. 💬 **OQ-SWB1: Should a launch from `/tmp/project` on a Mac work with podman?**

   Matt runs `YOLO_SETUP=podman-self yolo` in `/tmp/jail_test` on his Mac. yolo creates the
   jail's directories under `/tmp/jail_test/.yolo/home`, hands them to podman, and the launch
   dies with `statfs /tmp/jail_test/.yolo/home/local: no such file or directory`. The directory
   exists. On a Mac `/tmp` is a link to `/private/tmp`, and podman's VM cannot follow it; the
   same directory spelled `/private/tmp/jail_test` binds.

   - **A — Resolve the bind sources.** yolo resolves each bind source before it hands podman the
     argument, on every backend. Matt's launch works from `/tmp/jail_test`; the jail still sees
     `/workspace`, and every message keeps the path he typed.
   - **B — Refuse and name the resolved spelling.** Before starting podman, the launch checks
     whether the workspace path goes through a link and, if so, stops with *"podman cannot bind a
     path through a symlink on this Mac; run yolo from /private/tmp/jail_test instead."* Matt
     changes directory once and learns the rule; nothing is resolved behind his back.

   <!-- vantage: question id=OQ-SWB1 leaning="A — resolve the bind sources. The user never typed them, so refusing stops them over a spelling they did not choose, and resolving an argument reads through the link without writing anything." -->

   _Leaning:_ A — resolve the bind sources. The user never typed them, so refusing stops them over
   a spelling they did not choose, and resolving an argument reads through the link without
   writing anything.

   **Answer:**
   > _(empty — fill in when decided)_
