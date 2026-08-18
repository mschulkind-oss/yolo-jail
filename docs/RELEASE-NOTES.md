---
title: "Release notes — behaviour changes that need announcing"
date: 2026-08-18
status: living
tags: [release, breaking-changes]
summary: "Changes that alter what an existing setup does on upgrade. Not a changelog of every commit — only the things a user finds out about the hard way if nobody writes them down."
---

# Release notes — behaviour changes that need announcing

**This file exists because a design ruling discharged its residual risk as *"a release note"* and
there was nowhere to put one** (found 2026-08-18, verifying the `default_enabled` rename). A
mitigation that lives only in a design doc's risk table has not been communicated to anyone.

**What belongs here:** a change that makes an existing, working setup behave differently after an
upgrade — a default that flips, a warning that becomes fatal, a command that starts refusing.
**What does not:** features, fixes, and anything a user cannot notice. The commit history is the
changelog; this is the subset that bites.

---

## Unreleased

### ⚠️ `audio` is now off by default

**What changed.** A loophole's manifest declares `default_enabled`, and absent now means **off**.
`audio` ships `default_enabled: false`, where it was previously on whenever the host's PulseAudio
socket existed.

**Who is affected.** Anyone relying on sound in a jail — `/voice`, `sox`, `ffmpeg`, any ALSA client.

**What to do.** Enable it explicitly:

```jsonc
// ~/.config/yolo-jail/config.jsonc
"loopholes": { "audio": { "enabled": true } }
```

**Why.** *"We don't give host access by default."* Being useful is not a reason to be automatic —
[`loophole-activation.md`](design/loophole-activation.md) R1 and R4.

> [!WARNING]
> **Downgrade hazard, and it cannot be fixed from inside the new build.** An **older** yolo reading a
> **newer** manifest does not recognise `default_enabled`, falls back to the retired key's
> default-of-true, and **runs `audio` on**. Verified 2026-08-18 by probing ten candidate in-binary
> tripwires against the old build: `version` is accepted as int, string, float and bool without an
> enum check, and unknown top-level, nested and `requires` keys are all tolerated — so a new manifest
> has no way to make an old binary refuse it.
>
> The exposure is narrower than it sounds: the three **bundled** manifests are compiled into the
> binary and read content-addressed from its own embed, so a binary can only ever read its own copy.
> The hazard is real for a **pack-shipped** loophole read by an older yolo, and for a yolo-jail
> developer whose checkout is newer than their installed binary.

### ⚠️ An unreachable host service now refuses the launch

**What changed.** At boot, the jail dials every jail-facing service this launch wired up. An enabled
service it cannot reach used to warn; it now **fails the launch**, across all three fault classes
(unreachable, unpublished, rejected).

**Who is affected.** Most sharply: **a dead Claude OAuth broker singleton refuses every jail on that
host**, not just one — its endpoint variable is wired whenever the loophole is active, with no
publish gate.

**What to do.**

```console
$ YOLO_ALLOW_UNREACHABLE_SERVICES=1 yolo …
```

The refusal names that variable. It is honoured loudly and says plainly that nothing was repaired.

**What does *not* refuse:** a host yolo could not ask to forward loopback (an old passt, an explicit
`network.mode`, a rootful or unrecognised runtime) is never punished for what it cannot help —
`YOLO_HOST_LOOPBACK=unsupported`/`unknown`/absent never escalate.
📄 [`loopback-tls-reachability.md`](design/loopback-tls-reachability.md) OQ-R2, OQ-R3.

### ⚠️ A non-interactive launch no longer auto-accepts config changes

**What changed.** With no TTY, a changed `yolo-jail.jsonc` used to be accepted silently and the
approval snapshot rewritten. It is now a **refused launch**.

**Who is affected.** CI, scripts, and any non-interactive `yolo` invocation in a repo whose config
has changed since the last approved launch — including one that changed by accident.

**What to do.** Opt in explicitly:

```console
$ yolo --accept-config-changes …
```

**Why.** Auto-accept made *"humans must approve config changes"* conditional on somebody happening to
have a terminal attached — and the scripted case is exactly where nobody is watching. Non-interactive
use still works; it works via an explicit yes rather than an implicit one.
📄 [`config-safety.md`](design/config-safety.md) OQ-D2.

> [!NOTE]
> **Related, and not a behaviour change you can see:** the approval snapshot moved out of the
> workspace to host-side state the jail never mounts, so the record of what you approved is no longer
> writable by the thing being approved (OQ-D1).
