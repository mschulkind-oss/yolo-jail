---
title: "Running yolo on OrbStack without a Docker backend: podman inside an OrbStack Linux machine"
date: 2026-10-06
status: in-review
stage: DESIGN
next: "The maintainer decides whether yolo supports OrbStack as a podman host; if so, plan the five changes in §4, starting with the two that make a stock setup work (stdin and the advertised host name), and ask OrbStack about the dropped output (§3.1)"
tags: [research, macos, orbstack, podman, backend]
summary: "The maintainer asked what official OrbStack support in yolo would take. The runtime comparison assumed it meant a Docker-API backend, reversing Docker's removal. It does not: podman installed in an OrbStack Linux machine and driven from the Mac as a podman connection ran the benchmark workload at OrbStack's own speed, gave a freed 2 GiB back to macOS within 60 s, and ran a full yolo jail with all three host services reachable, with no yolo code change. Two faults needed workarounds: OrbStack's SSH proxy drops a container's output once the client closes stdin, which yolo always does, and the jail must be told to reach host services at host.orb.internal. Support would be five small changes on yolo's existing podman path plus a self-hosted CI Mac."
vantage:
  status-chip: true
---

# Running yolo on OrbStack without a Docker backend

**Status:** 2026-10-06, research only, and nothing is ruled.
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
> - **Official support is five changes plus CI** ([§4](#4-what-official-support-would-take)), all on
>   the podman path. The alternative, a Docker-API backend, would undo
>   [Docker's removal](../reference/fill-the-matrix-principle.md) for no measured gain.
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
- podman, rootless, with pasta networking. Fedora 44's is 5.8.7; the jail run in §3 used 6.1.2
  from Fedora rawhide, installed while chasing the fault in §3.1, which it turned out not to be.
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

So the working launch, after the setup in §1 and §3.1, was:

```console
$ CONTAINER_CONNECTION=orb-sshd YOLO_RUNTIME=podman YOLO_SVC_ADVERTISE_HOST=host.orb.internal yolo
```

## 4. What official support would take

All INFERRED, smallest first. Every change lands on the `podman` runtime; there is no new
backend name, so no new cell in [the fill-the-matrix rule](../reference/fill-the-matrix-principle.md)'s
matrix beyond a host flavour of an existing one.

1. **Keep the main process's stdin open.** Give `startJailMain` a pipe nobody writes instead of
   `/dev/null`. The hold reads nothing either way, and §3.1's table shows an open stdin gets the
   output through OrbStack's proxy. That makes OrbStack's own connection on port 32222 usable and
   removes the sshd and the tunnel. Checking every other `-i` with a closed stdin yolo runs is
   part of it. Report the proxy fault to OrbStack as well.
2. **Advertise `host.orb.internal` on OrbStack.** Recognise the host (the machine's kernel name
   is `7.0.14-orbstack-…`, which `podman info` reports) and advertise that name, recording a
   loopback disposition for it. `TestEveryBackendDeclaresALoopbackDisposition` and the
   reachability witness's severity rule
   ([`OQ-R3`](../reference/loopback-tls-reachability.md#oq-r3)) then apply as on any backend.
3. **Set the machine up for the user.** A `yolo` command, or a documented recipe, that creates the
   machine, installs podman, writes `/etc/subuid`, enables the socket and adds the connection; and
   a `yolo check` section that says which of those is missing and the command that fixes it.
4. **Check what assumes `podman machine`.** `ReadMachineShares` returns no answer under
   `CONTAINER_CONNECTION` ([`runtime/machineshares.go`](../../internal/runtime/machineshares.go)),
   and memory sizing reads `podman machine inspect`. Neither failed the launch above, but neither
   was checked: OrbStack shares more than `/Users` (a workspace under `/tmp` was not tried), and
   the nix daemon socket mount was not tested.
5. **CI.** A self-hosted Mac with a licensed OrbStack, like the dispatch-only Apple Container
   workflow. Without it the backend is untested by anything but its users.

**The alternative**, a Docker-API backend, would mean restoring what Docker's removal deleted and
another branch in each of the roughly 70 files that switch on the runtime name, for the same VM and
the same shared folder measured in §2.

## 5. What to clean up after the trial

On the Mac: the `orb-benchpod` and `orb-sshd` connections, the tunnel, and OrbStack itself with its
`benchpod` machine. Nothing in this repository depends on them.
