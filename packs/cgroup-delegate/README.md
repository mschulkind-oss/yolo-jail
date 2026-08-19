# `cgroup-delegate` — the jail's control over its own cgroup

One `loophole` contribution and nothing else. The delegate itself is **yolo's own
in-process goroutine** on the launcher side and the in-jail client (`cmd/yolo-cglimit`)
is baked into the image, so nothing here ships a binary.

**This manifest declares no daemon, and that is the point.** It is a *switch and an
identity*: the thing that makes the delegate opt-in like everything else, and the thing
that puts it in `yolo loopholes list`, `yolo check` and the briefing under the same
vocabulary as every other loophole. The socket, the goroutine and the teardown are still
the run pipeline's; what the run pipeline no longer does is decide by itself that the
delegate should be running.

## ⚠️ `yolo-cglimit` does not work until you ask for it

This is the sprint's one **accepted cost**, stated in the ruling rather than discovered
afterwards ([`loophole-activation.md`](../../docs/design/loophole-activation.md) OQ-A4).

```jsonc
// ~/.config/yolo-jail/config.jsonc
{
  "packs": ["claude", "cgroup-delegate"],
  "loopholes": { "cgroup-delegate": { "enabled": true } }
}
```

Until then the jail's boot output says `cgroup delegate: not available (no host daemon
socket)` and `yolo-cglimit` reports that it is not wired up in this jail.

## Why it was converted at all

It was **presence-activated**: it started whenever the platform allowed — *"Linux only,
cgroup v2 only"* — with **no config key anywhere**. That is precisely the shape R1
deletes, in a host-side service, and it was the last one left.

The counter-argument was considered and overruled. The delegate hands a jail control of
**its own** cgroup rather than reading host state, so R4's *"we don't give host access by
default"* is genuinely weaker here — but *"weaker is not absent, and R1 is about the
mechanism, not the severity. The moment one builtin stays presence-activated, 'presence
never activates' stops being a rule anyone can rely on while reading the code."*

## Why it is still on AF_UNIX

Every other host service is on loopback-TLS. This one cannot be, and the obvious reading
of why — *"its in-image client is still generated Python"* — is false: `yolo-cglimit` is
a baked Go binary. What does not survive the hop is **SO_PEERCRED**.

The delegate's whole security model is kernel-attested identity: `create_and_join` writes
the peer's **host-namespace pid**, read off the connection by the kernel and never sent by
the caller, into the job cgroup's `cgroup.procs` — and that write is what moves the caller
into the cgroup. A TCP connection carries no peer credential at all, and a loopback-TLS
*front* would be worse than nothing, because `SO_PEERCRED` on the upstream socket would
attest **yolo's own** pid. See [`security-shim.md`](../../docs/design/security-shim.md) §2
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
in-process delegate starts when a loophole record named `cgroup-delegate` is enabled,
active and origin-approved — so a pack you install that ships a loophole of that name can
turn it on. That is exactly the case OQ-A3 already admits (*"a fetched pack can declare
itself on"*): what a pack may **do** is bounded by the origin gate, installing one is a
deliberate user-scope act, and the most this particular switch can buy is a capability
you could grant yourself with one config line.

## Verifying

```console
$ yolo pack lint packs/cgroup-delegate
$ yolo loopholes list                   # cgroup-delegate, source `pack`, transport none
$ yolo-cglimit --help                   # in a jail that selected AND enabled it
```
