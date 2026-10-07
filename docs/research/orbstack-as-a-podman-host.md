---
title: "Running yolo on OrbStack without a Docker backend: podman inside an OrbStack Linux machine"
date: 2026-10-06
status: in-review
stage: DESIGN
next: "Rule [OQ-ORB1](#OQ-ORB1) for setup ownership; then build the independent stdin slice from the checked handoff"
tags: [research, macos, orbstack, podman, backend]
summary: "Checked post-release build handoff for yolo's environment on OrbStack: keep Podman, split setup, jail I/O, daemon reachability and native CI, and require an OrbStack-specific Mac proof."
vantage:
  status-chip: true
---

# Running yolo on OrbStack without a Docker backend

**Status:** 2026-10-06, support is queued after 0.12.0. The maintainer requires a working setup,
with Podman preferred and Docker allowed if needed. No support implementation is built; the
measurements below remain the original research, not verification of the proposed changes.
- **MEASURED** on one Mac (M4, macOS 26.5) with the OrbStack trial install from
  [the runtime comparison](macos-vm-runtime-comparison.md), one run each.
- **SOURCED** from this repository's code, with the file named.
- **INFERRED:** every recommendation, and every reading a measurement does not show directly.

**The question,** in the maintainer's words (2026-10-06): *"what would it take to support orbstack
in yolo officially?"*

> **In short.**
>
> - **No Docker backend is needed.** Podman inside an OrbStack Linux machine runs at OrbStack's
>   speed and gives memory back as OrbStack does ([§2](#2-podman-in-an-orbstack-machine-is-as-fast-as-orbstack)).
> - **A real yolo jail already runs on it**, through yolo's existing podman path, with all three
>   host services reachable ([§3](#3-a-yolo-jail-on-it-today)). It needed a manual machine setup
>   and two workarounds.
> - **Official support is several bounded changes plus a native CI proof** ([checked build handoff](orbstack-as-a-podman-host-plan.md)); it does not require restoring Docker as a backend.
> - **Unchanged:** OrbStack is closed source and paid for commercial use.

## Terms

- **OrbStack machine** — a full Linux distribution OrbStack runs in its one VM beside its Docker
  engine (`orb create fedora <name>`). It sees the Mac's `/Users` at the same paths, through
  OrbStack's shared-folder code, the one the comparison measured.
- **Podman connection** — a named remote endpoint the Mac's `podman` client drives over SSH
  (`podman system connection add`). Every Mac podman is remote: Podman Machine is a connection too.
  yolo honours one through `CONTAINER_CONNECTION`.
- **Host services** — the daemons yolo runs on the Mac for a jail (the OAuth brokers, AWS
  credentials). They bind the Mac's loopback, and the jail dials the host name yolo advertises,
  `host.containers.internal` by default
  ([`svcendpoint/listen.go`](../../internal/svcendpoint/listen.go)).
- **`host.orb.internal`** — the name OrbStack gives, inside its VM, to the Mac's loopback.

## 1. The setup

A Fedora 44 machine, `orb create fedora benchpod`. Inside it:
- podman, rootless, with pasta networking. Fedora 44's is 5.8.7; the [jail run](#3-a-yolo-jail-on-it-today) used 6.1.2
  from Fedora rawhide, installed while chasing the [SSH proxy fault](#31-orbstacks-ssh-proxy-drops-output-after-stdin-closes), which it turned out not to be.
- `matt:100000:1000000` in `/etc/subuid` and `/etc/subgid`;
- `systemctl --user enable --now podman.socket`.

On the Mac, `podman system connection add orb-benchpod --identity ~/.orbstack/ssh/id_ed25519
'ssh://benchpod@127.0.0.1:32222/run/user/501/podman/podman.sock'`, through OrbStack's own SSH
endpoint on port 32222.

## 2. Podman in an OrbStack machine is as fast as OrbStack

The comparison's workload, run with `podman -c orb-benchpod` (MEASURED, one run):

| On a shared Mac folder | Apple Container | OrbStack's Docker | Podman in the machine |
| :--- | ---: | ---: | ---: |
| pip install | 8.35 s | 5.34 s | 4.96 s |
| `stat` 20k files | 2,171 ms | 241 ms | 236 ms |
| open and read 20k files | 6,070 ms | 2,204 ms | 1,772 ms |
| Postgres rw tps | 4,103 | 7,101 | 6,578 |
| Postgres ro tps | 55k | 108k | 110k |

On the VM's own disk it matches OrbStack's Docker, including the weak Postgres write rate: 2,924
read-write tps against 2,940.

**Memory:** the comparison's 2 GiB test ([its §4](macos-vm-runtime-comparison.md#4-memory-does-a-vm-give-a-freed-2-gib-back)),
reading the resident size of OrbStack's VM process, went from 1,338 MiB idle to 3,347 MiB holding
the load, and back to 1,195 MiB 60 s after it was freed. A deleted 2 GiB file's cache was back
within 120 s. That is the return OrbStack's Docker showed, a little slower (10 s there), and
neither open VM measured returns anything.

## 3. A yolo jail on it today

`yolo -- bash -c …` from a workspace under `/Users/Shared`, with the installed host yolo
(0.11.1+15) and no code change (MEASURED):
- **The image** built on the Mac and was copied into the machine's podman as a 3.3 GB archive, the
  way Podman Machine gets it: 62 s the first time, nothing on later launches.
- **The workspace** mounted at the same path, and a file the jail wrote appeared on the Mac owned
  by the Mac user.
- **The boot completed**, and two faults remained.

### 3.1 OrbStack's SSH proxy drops output after stdin closes

The launch hung after the boot finished. yolo starts the jail's main process with
`podman run -i` and stdin at `/dev/null`, and waits for its "boot done" line on stderr
([`run/jailmain.go`](../../internal/cli/run/jailmain.go), `startJailMain`). Through OrbStack's
port 32222 that line never arrives. The same `podman run -i … </dev/null` printing to stderr
(MEASURED):

| Path | Output arrives |
| :--- | :--- |
| Mac → OrbStack's SSH proxy (port 32222), podman 5.8.7 or 6.1.2 | no |
| Same, through an OpenSSH socket forward over port 32222 | no |
| Same, stdin a pipe that stays open | yes |
| Same, without `-i` | yes |
| `podman --remote` inside the machine, no SSH | yes |
| Mac → the machine's own OpenSSH `sshd` | yes |
| Mac → a Podman Machine (podman 6.0.2) | yes |

INFERRED: OrbStack's SSH server ends a forwarded channel when the client half-closes it, where
OpenSSH keeps the other direction open. A plain `ssh … 'cat; echo after'` through port 32222 does
print `after`, so only forwarded channels show it. Not yet reported to OrbStack.

**Workaround used:** OpenSSH in the machine (`dnf install openssh-server`, the OrbStack key in
`authorized_keys`) and the connection pointed at it through a tunnel, `ssh -N -p 32222
-L 127.0.0.1:2222:127.0.0.1:22 benchpod@127.0.0.1`. Two simpler routes failed: podman's dial
to the machine's IPv4 address, `benchpod.orb.local`, got "no route to host" while the `ssh`
command reached it (INFERRED: macOS's local-network permission, which Homebrew's podman lacks), and
podman 6.0.2 rejects the IPv6 address in a connection URL, bracketing it twice.

### 3.2 Host services need `host.orb.internal`

The jail could reach none of its three host services: `host.containers.internal` is the
machine's own gateway, and pasta's `--map-host-loopback` reaches the machine's loopback, not the
Mac's. The launcher had set `YOLO_HOST_LOOPBACK=unknown` and added no network option, so the
in-jail check warned rather than refusing. With `YOLO_SVC_ADVERTISE_HOST=host.orb.internal` it
reported `3/3 enabled service(s) reachable` (MEASURED).

So the working launch, after the [machine setup](#1-the-setup) and [SSH workaround](#31-orbstacks-ssh-proxy-drops-output-after-stdin-closes), was:

```console
$ CONTAINER_CONNECTION=orb-sshd YOLO_RUNTIME=podman YOLO_SVC_ADVERTISE_HOST=host.orb.internal yolo
```

## 4. What official support would take

**Delivery requirement, ruled 2026-10-06:** the maintainer's "we NEED a happy path" requires a
normal setup that works without a user-maintained SSH tunnel or a manually supplied host name.
Follow the [happy-path principle](../reference/happy-path-principle.md). Prefer Podman, but not
at the expense of a reliable setup. Docker is allowed as a fallback if the Podman route cannot
deliver that result; the ruling does not restore the removed runtime or change the default.

The implementation is split into independently testable owners in the
[checked build handoff](orbstack-as-a-podman-host-plan.md). Current-tree checks establish these
boundaries, not that any proposed change already works:

1. **Setup, runtime selection and image delivery.** `YOLO_RUNTIME=podman` selects yolo's existing
   Podman path, and `CONTAINER_CONNECTION` makes Podman target a named endpoint. On a Mac that
   path already chooses `deliverViaArchive` and `podman load -i`, because `containers-storage:`
   belongs inside the Podman VM; retain it unless a native OrbStack test fails. `ReadMachineShares`
   declines to guess when a remote connection is selected, and macOS `yolo check` currently labels
   `podman machine info` as Podman Machine readiness. The happy path still needs an owner ruling
   about the first-party setup surface and who owns the machine/connection; see [OQ-ORB1](#OQ-ORB1).
2. **Mounts and jail I/O.** [`startJailMain`](../../internal/cli/run/jailmain.go) passes nil stdin
   (therefore `/dev/null`) to the Podman run client; a regression unit test can distinguish an open,
   unwritten pipe from EOF, then the OrbStack runner must prove output survives the SSH half-close.
   Audit the other bounded `-i` callers in [`assemble.go`](../../internal/cli/run/assemble.go),
   [`jailmain.go`](../../internal/cli/run/jailmain.go) and [`run.go`](../../internal/cli/run/run.go),
   plus stdin forwarding in [`proxy_other.go`](../../internal/cli/run/proxy_other.go): the first-session
   and attach `exec -i` paths must retain their intentional EOF behavior. Native proof includes a fresh
   launch and attach with closed invoking stdin. The connection-specific share probe returns unknown,
   not pass, so native mount evidence must come from an actual jail bind and write.
3. **Host daemon reachability.** [`hostloopback.go`](../../internal/cli/run/hostloopback.go) owns
   the assembled in-jail loopback disposition, but currently returns empty host-loopback facts on
   macOS. [`podmanready.go`](../../internal/cli/run/podmanready.go) stores Podman readiness facts only
   for the non-machine case, so the selected remote endpoint's answer must be carried into this
   path. The daemon's published hostname is selected separately by `advertiseHostFor` in
   [`loopholesruntime.go`](../../internal/cli/run/loopholesruntime.go), called by
   `startLoopholesMatching` and passed as `YOLO_SVC_ADVERTISE_HOST` to a loopback-TLS child;
   [`svcendpoint/listen.go`](../../internal/svcendpoint/listen.go) consumes that explicit override
   or uses its existing default. Those host services start in a separate keeper with fresh
   `Options` ([`keeper.go`](../../internal/cli/run/keeper.go)), so any launch facts for advertisement
   must cross [`keeperplan.go`](../../internal/cli/run/keeperplan.go) / [`keeperspawn.go`](../../internal/cli/run/keeperspawn.go).
   ORB-D2 proposes positive OrbStack identification, `host.orb.internal` advertisement and a
   matching disposition; no such automatic behavior is built. Caller-level regressions must prove
   the assembled disposition and actual daemon publication agree. Unit logic does not establish
   the Mac-to-jail network hop.
4. **Native CI.** Use the existing self-hosted Mac already used by
   [`apple-container.yml`](../../.github/workflows/apple-container.yml), with an OrbStack-specific
   dispatch-only workflow and integration test. The job must prove it used the requested named
   Podman connection, not a default Podman Machine. `workflow_dispatch`, read-only contents
   permission and the repository guard are the established self-hosted safety pattern.

**Native verification is required, not claimed.** The existing self-hosted Mac already used by
[`apple-container.yml`](../../.github/workflows/apple-container.yml) has OrbStack per the maintainer;
this Linux checkout has not run OrbStack, inspected the Mac's account, license, machine, connection
or credentials, or dispatched a workflow. The [handoff](orbstack-as-a-podman-host-plan.md) specifies
the dispatch boundary, exact runner command, explicit Podman-connection/kernel assertion and the
jail outcomes required. No Linux/nested Podman run stands in for native proof of the SSH proxy,
shared-folder behavior or host loopback. Functional success does not establish commercial-license
compliance; the maintainer remains responsible for that prerequisite.

The [handoff](orbstack-as-a-podman-host-plan.md) gives the current-source map, exclusive owners,
test cases and build order. The first buildable slice is the failing-before-fix stdin regression and
lifetime fix; setup implementation stops on [OQ-ORB1](#OQ-ORB1). A Docker-API backend remains an alternative
only if native Podman support cannot meet ORB-D1; it needs its own bounded design and complete
coverage rather than an automatic reversion to the retired backend.

## Open questions

1. 🔒 **OQ-ORB1: Should OrbStack setup create a yolo-owned machine or use a user-selected one?**

   The happy path needs a first-party way to reach a working setup. A public setup/repair command
   and its authority to create, claim or change a machine and Podman connection are not ruled.
   A helper script may implement the command but is not itself a product decision.

   - **A — Create a separate yolo-owned machine and connection.** A first-party command asks before
     creation or configuration; `yolo check` stays read-only.
   - **B — Use an explicitly selected existing machine and connection.** A first-party command
     configures only what the user authorizes; it never claims or rewrites unrelated state.

   <!-- vantage: question id=OQ-ORB1 leaning="A — create a separate yolo-owned machine and connection only after explicit consent; keep yolo check non-mutating and never claim existing state." -->

   _Leaning:_ A — prefer a separate yolo-owned machine and connection, with explicit consent for
   creation/configuration and a read-only `yolo check`; never claim existing state automatically.

   **Answer:**

   > _(awaiting maintainer ruling)_

## Decision Ledger

| ID | Ruling / Decision | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| ORB-D1 | Support is post-release work; the normal setup must work, not merely document workarounds. | 2026-10-06 | [Support requirements](#4-what-official-support-would-take) | — |
| ORB-D2 | Podman is preferred; Docker is permitted if needed for reliable support, not restored by this ruling alone. | 2026-10-06 | [Support requirements](#4-what-official-support-would-take) | — |
| ORB-D3 | Use the existing Mac runner, which the maintainer says already has OrbStack, for native verification. | 2026-10-06 | [Verification](#4-what-official-support-would-take) | — |

## 5. What to clean up after the trial

On the Mac: the `orb-benchpod` and `orb-sshd` connections, the tunnel, and OrbStack itself with its
`benchpod` machine. Nothing in this repository depends on them.
