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

### ⚠️ Agent CLIs no longer update themselves

**What changed.** A pack's `program via npm` — every agent CLI yolo ships (`pi`, `copilot`,
`codex`, `opencode`) — used to keep itself current. Its launcher polled the npm registry on the
first invocation after a jail boot and hourly after that, and **reinstalled** whenever the
registry had moved. It no longer installs anything: the same poll now only **prints** that a
newer version is available, at most once an hour.

**Who is affected.** Anyone with an existing jail home. Those homes persist across boots, so the
CLI in yours is frozen at whatever it last installed and will stay there until you ask for a new
one. A **fresh** jail is unaffected — the first install on first use still happens, because a
first install is not a poll.

**What to do.** Run the new act, inside the jail:

```console
$ yolo pack update
```

It refreshes every npm-declared program the jail's packs contribute. `yolo pack install` no
longer resolves a new version at all — the two verbs used to be one code path and now behave
differently. Run `update` **in the jail**: an agent CLI is installed into that jail's npm prefix,
so on the host there is nothing to refresh (it says so rather than doing nothing).

A pack that pins its own version (`"package": "@scope/tool@1.2.3"`) is unaffected in both
directions: it never polled, and `update` honours the pin rather than overriding it.

**Why.** *"I don't want magical evergreen npm packages."* A binary that changes between two
invocations with nobody present is a silent-change path that no pin, lockfile or approval prompt
can ever cover, because there is no act to attach them to. Deleting the mechanism is cheaper than
gating it. 📄 [`trust-paths.md`](design/trust-paths.md) §1 row 1 (OQ-TP5).

> [!NOTE]
> **The lockfile half of that ruling is not built.** `update` resolves the registry's latest and
> installs it; nothing yet **records** which version it got, so `install` has no pin to reinstall
> from. There is nowhere to put one: `LockEntry` has no package-version field, and the lockfile is
> per *fetched* pack while all four packs declaring npm programs are *embedded*. That is
> [`trust-paths.md`](design/trust-paths.md) OQ-TP4, still open. The user-visible consequence is
> only that two jails updated at different times can hold different versions — which was already
> true, and is now at least the result of somebody asking.

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

### ⚠️ A pack whose claims you never approved now refuses the launch

**What changed.** A **fetched** pack (a `git+https://…` entry in `packs`) may only read your host or
run code on it for the claims you approved at `yolo pack install`. An unapproved claim used to print
`Warning: refused installer …` and hand you a **working jail with that part of the pack switched
off**. It now **refuses the launch**: no jail starts.

**Who is affected.** Anyone with a fetched pack that was selected in `packs` but never run through
`yolo pack install` — or whose pin moved and *gained* a claim since the last approval. Embedded packs
(`claude`, `pi`, …) and local `file://` packs are unaffected: their origin already carries your own
authority, so they refuse nothing. A fetched pack that reads nothing from the host is unaffected too
— there is nothing to approve.

Newly covered by this, because the launch never checked it before: a wrapped agent **plugin's**
`hooks`/`mcpServers`/`lspServers`, which travel inside the pack's skills tree.

**What to do.** The refusal names the pack, the exact claim, and these three choices:

```console
$ yolo pack install     # review every claim the pack makes and approve it
```

…or edit the pack so it stops asking, or delete it from `packs` in
`~/.config/yolo-jail/config.jsonc`. **There is no "run it anyway" flag**, deliberately: a fourth
choice would be the partial pack this change exists to retire.

**Why.** The refusal was computed on the host and then *not carried into the jail*, which re-derived
it from a hardcoded permissive answer — so the curl-to-bash launcher was written for a fetched,
unapproved pack anyway. The warning was true about the decision and false about the outcome. Refusing
on the host deletes the problem instead of plumbing a decision across the boundary.
📄 [`trust-paths.md`](design/trust-paths.md) §3.1, OQ-TP6.

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
