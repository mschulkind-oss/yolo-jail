# `cgroup-delegate` — the jail's control over its own cgroup

One `loophole` contribution and nothing else. The delegate itself is **yolo's own
in-process goroutine** on the launcher side and the in-jail client (`cmd/yolo-cglimit`)
is baked into the image, so nothing here ships a binary.

**This manifest declares no daemon, and that is the point.** It is an *identity*: the
thing that puts the delegate in `yolo loopholes list`, `yolo loopholes status`, `yolo check`
and the briefing under the same vocabulary as every other loophole. The socket, the
goroutine and the teardown are still the run pipeline's.

## Selected by default

The manifest says `selected_by_default = true`, so **every launch selects this pack with no
`packs` line naming it** and `yolo-cglimit` works in a jail with an empty config
([LG-D13](../../docs/reference/loophole-grants.md#LG-D13),
[PD-13](../../docs/reference/loophole-grants.md#PD-13)). The loophole is ungated
(it declares no `[[grants]]`): the delegate can only narrow the jail, creating child cgroups
inside the container's own with the `cpu`, `memory` and `pids` controllers and bounded
values, and it reads and changes nothing of the host's
([`security-shim.md`](../../docs/reference/security-shim.md)).

- **The key is honored for a pack shipped with yolo only.** A local or fetched pack that
  declares it is refused when it resolves, naming the line to delete.
- **It is not a coding agent and does not count as one**: a launch whose only packs are
  default-selected still prints the "no coding agent" notice.
- **Where it cannot run, the launch says nothing** (macOS, Apple Container, a kernel without
  cgroup v2), since you did not choose it; `yolo loopholes status` reports it.
- **It cannot be turned off yet.** Removing a `packs` line naming it leaves it selected. Its
  per-workspace `off` arrives with the pack switch of
  [LG-D9](../../docs/reference/loophole-grants.md#LG-D9).

## Why it is still on AF_UNIX

Every other host service is on loopback-TLS. This one cannot be, and the obvious reading
of why — *"its in-image client is still generated Python"* — is false: `yolo-cglimit` is
a baked Go binary. What does not survive the hop is **SO_PEERCRED**.

The delegate's whole security model is kernel-attested identity: `create_and_join` writes
the peer's **host-namespace pid**, read off the connection by the kernel and never sent by
the caller, into the job cgroup's `cgroup.procs` — and that write is what moves the caller
into the cgroup. A TCP connection carries no peer credential at all, and a loopback-TLS
*front* would be worse than nothing, because `SO_PEERCRED` on the upstream socket would
attest **yolo's own** pid. See [`security-shim.md`](../../docs/reference/security-shim.md) §2
and `startCgroupDelegate` in `internal/cli/run/loopholesruntime.go`.

## What the conversion also retired

`paths.BuiltinLoopholeNames` is **gone**, and with it the last "builtin service" channel:

- The reservation over `cgroup-delegate` had to go, or the name pre-flight — which is
  fatal — would refuse every launch that selects this pack.
- `internal/config` stopped refusing `loopholes.cgroup-delegate` by name. That refusal
  existed because the name could not be a loophole; now it is one, and refusing it would
  make the switch unwritable.
- The spawn loop's builtin-name skip went with the list. It was the branch that made a
  manifest under a builtin name into *half a loophole* — daemon dropped in silence,
  binds and `jail_env` already crossed — and there is no builtin name left for it to
  fire on.

**The lookup is shadowable now, and that is a consequence rather than an oversight.** The
in-process delegate starts when a selected pack's loophole record named `cgroup-delegate`
is active and origin-approved — so a pack you install that ships a loophole of that name runs
it. What a pack may **do** is bounded by the origin gate
([OQ-A3](../../docs/reference/loophole-system.md#oq-a3)), installing one is a deliberate
user-scope act, and the most this name can buy is a capability you could grant yourself with
one `yolo pack add`.

## Verifying

```console
$ yolo pack lint packs/cgroup-delegate
$ yolo loopholes list                   # cgroup-delegate, source `pack`, transport none
$ yolo loopholes status                 # cgroup-delegate, selected by default
$ yolo-cglimit --help                   # in any jail on Linux with cgroup v2
```
