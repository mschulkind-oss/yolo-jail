---
status: accepted
stage: DECIDED
next: "Item 6 (lifetime: stop the slice while a member runs, relaunch) is still owed on a host"
depends-on: [resource-groups-plan.md]
---

# Resource groups: host witness, 2026-11-26

Evidence for [Brief 4's items 1 to 5](resource-groups-plan.md#brief-4--verified-execution-boundary-coverage).
The owner ran the witness script once, on one host. The excerpts below are lines copied from its
stdout (the original sat under the owner's untracked `.yolo/host-run/`), trimmed to the evidence.
In the excerpts `$S` stands for
`/sys/fs/cgroup/user.slice/user-1000.slice/user@1000.service/batch.slice/batch-yolo.slice`.

## Host

Kernel 7.3.5 (Arch), rootless podman with the systemd cgroup manager on cgroup v2, yolo
`0.20.1+21.ga91ddedea28e`. The workspace filesystem is device `0:31` on dm `253:0` over nvme `259:0`;
the limit is set on `259:0`, and the throttle held through the dm layer on this host. One host only.

## Items 1 and 2: both child slices, their limits, the parent's controllers

The read-only check printed the plan:

```text
I/O policy: 259:0 rbps: finite assigned 2000000000 agent=100000000 batch=1900000000; ancestor policy unavailable
I/O policy: 259:0 wbps: finite assigned 1000000000 agent=50000000 batch=950000000; ancestor policy unavailable
```

After a mapped launch, the kernel held:

```text
$S/batch-yolo-agent.slice/io.max: 259:0 rbps=100000000 wbps=50000000 riops=max wiops=max
$S/batch-yolo-batch.slice/io.max: 259:0 rbps=1900000000 wbps=950000000 riops=max wiops=max
$S/io.max: 259:0 rbps=2000000000 wbps=1000000000 riops=max wiops=max
$S/cgroup.subtree_control: cpuset cpu io memory pids
```

`systemctl --user status` listed `batch-yolo-agent.slice` and `batch-yolo-batch.slice` active. The
agent and batch ceilings sum to the parent's, as the plan printed.

## Item 3: launcher and keeper membership

```text
launcher pid 2000145 -> 0::/user.slice/user-1000.slice/user@1000.service/batch.slice/batch-yolo.slice/batch-yolo-agent.slice/yolo-jail-launcher-2000145.scope
launcher child pid 2005045 -> 0::/user.slice/user-1000.slice/user@1000.service/batch.slice/batch-yolo.slice/batch-yolo-agent.slice/yolo-jail-keeper-yolo-rg-witness-e266b81b-podman-self-2005045.scope
```

## Item 4: the container scope

```text
container pid 2007275 -> 0::/user.slice/user-1000.slice/user@1000.service/batch.slice/batch-yolo.slice/batch-yolo-agent.slice/libpod-439ac40aeba206e52de959af394027fe2593ee76f6e1d9a7e77823973aafe3cb.scope/container
CgroupParent=batch-yolo-agent.slice
```

The container's `libpod-….scope` sits inside `batch-yolo-agent.slice`, not beside it.

## Item 5: a bounded write inside the jail

An attaching launch (the jail was already up) ran `dd bs=1M count=256 oflag=direct` on a path in the
workspace:

```text
workspace /home/matt/rg-witness is on block device   0:31 ; the limit applies only to a device named in the agent io.max: 259:0 rbps=100000000 wbps=50000000 riops=max wiops=max
268435456 bytes (268 MB, 256 MiB) copied, 5.23028 s, 51.3 MB/s
```

`268435456 / 5.23028 s` is 51.3 MB/s against `wbps=50000000`. At exactly 50 MB/s the write takes
5.37 s, so the measured time is within 3% of the limit. The agent slice's `io.stat` for `259:0` rose
410525696 write bytes over the run, more than `dd` wrote, because the slice also holds the jail's
other running work; that delta is not a throughput figure, and the claim rests on `dd`'s own rate.
The attach reused the one pair: after the run only two child units existed under the slice.

```text
batch-yolo-agent.slice loaded active active Slice /batch/yolo/agent
batch-yolo-batch.slice loaded active active Slice /batch/yolo/batch
```

## Not witnessed

Item 6, the lifetime case (stop `batch-yolo.slice` while a member runs, relaunch), was not run. Two
simultaneous launches were not run either; the attach reuse above is the evidence for one pair.
