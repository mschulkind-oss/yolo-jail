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

*(Everything below has landed since **v0.8.0**, tagged 2026-08-13 — the last version cut. This file
was created on 2026-08-18, after that tag, which is why it has no released sections yet. Whether the
next cut is triggered by this file filling up or by a cadence is an open product question; see
[`plans/further-roadmap-ideas.md`](plans/further-roadmap-ideas.md) §I5.)*

### ⚠️ Apple Container: your host `~/.claude/settings.json` starts applying

**What changed** (2026-08-24, `e3c995b6`). A pack's `reads-host` grant — the one file yolo lets a
pack read out of your host home — was emitted as a **single-file bind mount**, and Apple Container
cannot bind a single file (apple/container#1089). It did not error. The file simply never arrived,
and the entrypoint reads the host layer fail-open, so the surface composed from its defaults alone.
Two shipped packs declare a grant: `claude`
(`~/.claude/settings.json`) and `pi` (`~/.pi/agent/settings.json`). The file is now **copied** into
the jail home and the entrypoint is told where to look.

**Who this bites.** Anyone on `runtime: container` who has one of those files. Everything in yours
— model choice, statusline, hooks, `apiKeyHelper`, permission defaults — was silently absent from
the jail's composed settings, and **starts applying on the next launch**. If you have been tuning a
jail around what it actually did rather than what you configured, that is the change.

**And nothing gave you a reason to look**, which is why this is a release note rather than a fix.
The launch's own disclosure kept printing `claude: reads-host .claude/settings.json` on every Apple
Container launch — a positive assertion of a read that did not happen.

**You do not have to do anything**, except decide whether the settings you wrote months ago are
still the settings you want. Read the host file once before your first Apple Container launch after
upgrading.

**One shape difference on this backend.** The grant arrives as a copy in the jail home
(`~/.yolo-ctx/`), not a `:ro` mount. Same bytes, read at the same point in boot; it is simply no
longer read-only from inside the jail.

**Not affected:** podman, where the bind always worked. **Not fixed on `macos-user`**, which has no
`/ctx` at all — a `reads-host` grant still never crosses there. It no longer says nothing about it:
as of `4402e33a` that launch warns, and the notices section below covers it.

**NOT verified on hardware.** Whether Apple Container drops or hard-errors on a single-file bind is
a Mac-only fact; every statement in this tree assumes it drops, and the fix is correct under either
reading. Nobody has watched the composed file appear on a Mac.

### ⚠️ Apple Container: a `host_files` file entry stops being an empty file and becomes your file

**What changed** (2026-08-24, `c22e25b5`). The same backend limitation as above, with a sharper
consequence. A `host_files` entry whose `source` is a **file** was also emitted as a single-file
bind — but here the entrypoint swallowed the read error and wrote the destination **anyway**, at
the `readonly` default mode. So the jail held an **empty `0o444` file** where your `~/.npmrc`
should have been, and could not repair it from inside, because the file was not writable. The
source is now copied across the same way.

**Who this bites.** Anyone on `runtime: container` with a `host_files` entry that names a `source`
pointing at a file. **Directory entries were never affected** — Apple Container nests directory
mounts fine, so those were already honored, and they are deliberately left alone.

**What to do.** For the default mode of a source-bearing entry (`readonly`), and for `copy`:
nothing. Both re-render every boot, so the empty file is replaced by your real content on the next
launch.

**`once` does not re-render**, and that is the one case with an action. It seeds only when the
destination is **absent**, and the empty file it seeded is present — so it will keep it forever. If
you wrote `"mode": "once"` on a source-bearing entry, delete that destination inside the jail once
and relaunch. (`capture` re-renders too, but its whole contract is that an in-jail edit outranks the
host layer permanently — so if an agent edited the empty file, check the destination.)

**NOT verified on hardware**, on the same terms as the entry above.

### ⚠️ `macos-user`: your agent starts launching with the `--dangerously-*` flags it was always meant to have

**Read this one before upgrading: it changes what an agent may do without asking you.**

**What changed** (2026-08-24, `dc1349a6`). Pack `launch` flags were injected inside the container
path, which the `macos-user` arm returns before reaching. Nothing failed; the backend just launched
without them. The injection now happens above the backend dispatch, so both arms consume one result.
The flags are `--dangerously-skip-permissions` (`claude`, `agy`), `--yolo --no-auto-update`
(`copilot`), and `--dangerously-bypass-approvals-and-sandbox` (`codex`).

**Who this bites**, and the two casualties were not symmetric:

- **`copilot` was a 100% drop** — a plain `launch` contribution with no config half to fall back on.
  It has been prompting you for approval and self-updating on that backend. It now runs `--yolo`,
  which approves everything, and stops updating itself.
- **`claude` fell back to `defaultMode: acceptEdits`**, the settings half of the same declaration,
  which auto-accepts **edits** and not Bash or WebFetch. With the flag, those are accepted too.

**What to do.** Nothing, if you wanted a YOLO jail — this is the posture every container backend has
always had, and the one `macos-user` was already configured for. But if the prompting was
load-bearing for you, note that **there is no config key that turns this off**: autonomy is a
property of the notch (a jail renders the autonomous posture), not a switch. Decide it deliberately
rather than discovering it after an upgrade.

The flags apply only when you **name the binary** — `yolo -- claude`. A bare `yolo` opens an
interactive zsh on this backend and gets none of them.

**NOT verified on hardware.** The flag is asserted at the seam the backend receives (the argv handed
to the native launcher) and the assertion is mutation-verified, but nobody has watched the resulting
agent start on a Mac.

### ⚠️ Apple Container: context mounts you were granted read-only stop arriving at all

**What changed** (2026-08-24, `0d7e8f58`). Apple Container **accepts `-v src:dest:ro` and ignores
the `:ro`**. yolo has known that about the config `mounts` key for a long time and skipped it there.
It did not know it anywhere else, because the rule was a local variable inside that one loop — so
three other emitters kept handing out the same bind and trusting the same suffix:

- a **pack `mount` grant**, which is the sharp one. It is origin-gated, and for a fetched pack a
  human approves it at `pack install` against the words *read-only*. On this backend the agent got
  write access to that directory in your real home.
- a pack `mount` whose source is a single **file**, which could never arrive here at all
  (apple/container#1089) and said nothing.
- your host **`~/.config/nvim`**, bound at `/ctx/host-nvim-config` so the jail can copy your editor
  config in at boot. The copy happens once; the writable mount lasted the whole session.

All three are now **skipped with a printed reason**, matching what config `mounts` already did.

**Who this bites.** Anyone on `runtime: container` whose packs declare a `mount`, and anyone whose
agent used their nvim config in the jail. **You lose the mount** — that is the change, and it is a
real loss, not just a warning. The alternative was keeping a writable window into your home on the
backend people pick *for* isolation.

**What to do.** Use `YOLO_RUNTIME=podman` if you need the mount; the bind works correctly there.
For nvim specifically, the visible symptom is the jail's nvim starting unconfigured — that is this
change and not a broken install. If a pack's `mount` is load-bearing for your workflow, that pack
currently has no Apple Container story, which is worth saying in its README.

**Not affected:** podman. **`macos-user`** has no mounts at all and skips these silently — a
pre-existing gap, unchanged here.

**NOT verified on hardware.** That Apple Container ignores `:ro` is this repo's long-standing
position, recorded before today and unverified on a Mac by me. If it turns out to *honor* `:ro`,
these three skips become unnecessary rather than wrong.

### New notices at launch on `macos-user` and Apple Container — nothing broke

**What changed** (2026-08-24, `35448719`, `8ab03d2e`, `6a53a2a3`). A sweep for capabilities that
render, validate and then **evaporate** on a backend that cannot honor them turned up more than the
ones fixed above. The rest cannot be fixed at launch time, so the launch now **names** them. Nothing
about what those backends *do* has changed — only what they say. **If you see new yellow text on
your next launch, this is it, and it is not a new fault.**

**On `macos-user`:**

- **One line per inert loophole.** Every pack-shipped loophole is inert on that backend, and it was
  the one inert backend that said nothing. Loopholes declared by your **own** config
  (`loopholes.<name>.command`) are now reported too — that half was missing on **both** inert
  backends.
- **`resources` are read and ignored.** macOS has no cgroups and there is no VM to size.
- **`cache_relocations` are not implemented**, and the documented "just symlink it yourself"
  workaround does not work either: the Seatbelt profile denies writes outside the workspace and the
  sandbox home, and denies reads under `/Volumes`.
- **Every pack `state` dir is shared across all workspaces.** `.claude`, `.codex`, `.pi`, `.copilot`,
  `.gemini` are per-workspace on every other backend; this one has a single home
  (`/Users/_yolojail`) with no workspace component. This is issue #39's mirror image, and it is
  **warned rather than fixed** on purpose — splitting the home would break the machine-wide
  credential tier to repair the per-workspace one.
- **Briefings and skills are not delivered at all** — no `AGENTS.md`, no `CLAUDE.md`, and no skills
  including the built-in suite. The blocked-tool shims *are* generated, so `grep -r` exits 127 with
  nothing on the page explaining why.
- **`lsp_servers` renders and enables the tool while the binaries are never installed**, because the
  installer is a generated bootstrap script this backend deliberately does not run.
- **Host bytes never reach a config surface**, added `4402e33a`. Two channels, failing differently:
  a pack **`reads-host`** grant renders from its *defaults* layer, so the agent runs on a settings
  file you did not write and cannot tell from one you did; a **`host_files` entry with a `source`**
  is dropped from the launch entirely, leaving no file at that path. The second is the better
  failure. Both were silent, and the first is the one to read your warnings for.

**On Apple Container:** an **explicit** `network.mode: "host"` now warns. It is not honored there,
and it is *worse* than leaving the key unset — both port keys are bridge-gated, so asking for host
mode also drops every published port and `forward_host_ports`. The default (`bridge`) does not warn:
it is genuinely honored, and a warning on every launch is a warning people learn to skip.

**What to do.** Read them once; for most, nothing. Two are worth acting on: drop an explicit
`network.mode: "host"` from an Apple Container config (or use `YOLO_RUNTIME=podman` for real host
networking), and stop expecting skills or a briefing to reach an agent on `macos-user`. **None of
these refuses a launch** — they are notices, not the fatal reachability witness.

### ⚠️ Apple Container: shared credentials become shared again — and workspaces that had separate logins converge

**What changed** (2026-08-24, #39). Pack-declared **shared** dirs — the machine-wide tier:
`~/.claude-shared-credentials` and `~/.gemini-shared-credentials` today — were never mounted on
`runtime: container`. `packload.SharedDirs` was called once in the whole run package, inside the
podman branch, so on Apple Container the directory simply lived in the per-workspace state dir that
`/home/agent` points at. Nothing errored; the tier quietly collapsed. It is now mounted from
`GlobalHome` on that backend too.

**You do not have to do anything, and you should not lose a login.** The first launch after
upgrading copies a stranded credential up out of the workspace state dir into the machine-wide
location (copy, never move, and only into a file that is missing).

**The one behaviour to expect, because nothing can preserve it.** If you were on Apple Container
and logged in **separately in several workspaces**, those logins were genuinely independent — that
was the bug. They now converge: whichever workspace launches first wins the machine-wide slot, and
every other workspace uses that credential from then on. Each workspace's old copy is left in place
(`<workspace>/.yolo/home/<shared-dir>/`, e.g. `.claude-shared-credentials/`) and is simply no longer
read; delete it when you are satisfied.

**Not affected:** podman on any platform (this always worked there), and `macos-user`, whose single
`/Users/_yolojail` home makes the machine-wide tier correct by construction.

### ⚠️ `macos-user`: `workspace_readonly` used to do nothing, and now it does what it says

**What changed** (2026-08-23, `d0961f2c`). `workspace_readonly: true` was delivered as a `-v …:ro`
bind by the **container** pipeline only. `macos-user` has no bind mounts at all, so on that backend
the key was a **silent no-op**: the config said the workspace was read-only, the launch reported no
problem, and the agent could write to it. It is now enforced through the Seatbelt profile.

**Who this bites.** Anyone on `macos-user` who set `workspace_readonly: true` and has been running
agents that write to the workspace. **The key now means what it says, so those writes will start
failing** — which is the point, but it is a behaviour change on an existing, working setup.

**Two smaller changes ride along:**

- **`per_side_paths` now WARNS on `macos-user`** instead of being quietly absent. Per-side shadowing
  needs a mount namespace, and Seatbelt cannot fork a path — so the honest move is to say so.
  *Shipping a new default that is silently missing on one backend would repeat the exact defect
  above.*
- **`node_modules` is shadowed per side by default**, which changes the frozen container argv. This
  is an intended change, not drift; three golden argv tests were updated with it.

**NOT verified on hardware:** that Seatbelt enforces the new denies at runtime. Everything above was
measured in a Linux jail — the profile is generated correctly and its call sites are pinned by
mutation-verified tests, but nobody has watched a write fail on a Mac.

### The Claude OAuth broker ships inside the `claude` pack — nothing to do, unless you had it disabled

**What changed.** `claude-oauth-broker`'s manifest moved out of `bundled_loopholes/` and into the
official **`claude` pack**, as a `loophole` contribution. `bundled_loopholes/` is now empty and
deleted: every loophole yolo ships is a pack's.

**Do you have to do anything? No.** The broker is a contribution of the pack you already select to
get Claude Code — `packs: ["claude"]` — so if you use claude in a jail, the broker installs and
activates exactly as before. It keeps `default_enabled: true`, the one shipped loophole that does,
because a jail-only claude user who loses it is not merely without a feature: they are running
unserialized single-use refresh-token races against Anthropic. There is **no new config line**, no
`packs` entry to add, and no state to migrate — the loophole keeps its name, so its CA and leaf
certs stay where they are (`~/.local/share/yolo-jail/state/claude-oauth-broker/`) and running jails
keep trusting them.

**If you do NOT select the `claude` pack, the broker is gone** — no host singleton, no in-jail
terminator, no CA. That is the intended shape (yolo does not run a daemon no selected pack names),
and it is the one case where the move is visible. Selecting the pack restores it.

**If you had `loopholes.claude-oauth-broker.enabled: false`, keep it.** The key is unchanged and
still wins over the manifest default, from either config scope. Nothing about disabling it moved.

**One activation change, and it is a widening.** The manifest used to declare
`requires.command_on_path: "claude"` — a probe for `claude` on the **host's** PATH. That read false
for exactly the user yolo exists for: someone who installs claude *inside* the jail (via the lazy
launcher) and never on the host. Those users were silently losing refresh serialization, with the
loophole showing as inactive in `yolo loopholes list` and no reason given. The probe is deleted;
selecting the pack is the dependency it was approximating. **Consequence:** on a host without
`claude` installed, selecting the claude pack now starts the broker singleton where it previously
did not. It is idle until a jail asks it for a token.

**For pack authors: the pack-shipped subset is now universal.** It used to be an asymmetry between
two channels — a *bundled* manifest could declare `publishes: "endpoint"`, `jail_env`, an absolute
`ca_cert` and an unscoped `requires.file_exists`; a pack-shipped one could not. With the bundled
channel retired, **every module manifest yolo reads is held to the subset, including its own**.
Two other rules changed shape with it: `loopholes.ReservedLoopholeNames` is deleted (no name is
reserved any more — exclusivity across packs is what refuses a duplicate), and the §4.3a placement
rule no longer exempts anything, since yolo's own loopholes are staged outside every workspace.

### ⚠️ The per-jail Claude OAuth broker relay is gone; `yolo broker status` reports differently

**What changed.** The broker's jail-facing hop used to be a **per-jail relay process**
(`yolo internal daemon broker-relay`), one per running jail, with its own pid, lock and socket
files in `/tmp`. It is deleted. The host-wide broker singleton is unchanged and still lives at
`/tmp/yolo-claude-oauth-broker.sock`; what fronts it is now yolo's ordinary loopback-TLS front,
one per jail, owned by the `yolo` process that launched that jail. The jail sees exactly what it
saw before — the same `YOLO_SERVICE_CLAUDE_OAUTH_BROKER_ENDPOINT` naming the same
`/run/yolo-services/claude-oauth-broker.endpoint` — and refresh serialization across multiple
jails on one host is unchanged, because it was always the singleton's flock doing it.

**Three things a user can notice.**

1. **`yolo broker status` no longer has a `ping:` line.** It has `socket accept:` instead, and
   `yolo check`'s broker row says `daemon live (pid=…, socket accepting)` rather than `ping ok`.
   The singleton now expects yolo's connection preamble on every connection, so a host-side
   prober cannot speak its protocol without asserting a jail identity it does not have — liveness
   is "the socket accepts" for the same reason it already was for every other fronted daemon.
   The end-to-end protocol check is still run by `yolo check`, which reaches the broker through
   a real jail's endpoint and therefore gets a real preamble. **Consequence to know:** a broker
   that is listening but wedged in its handler now reads as live where the old ping called it
   dead.
2. **`yolo check`'s wording changed.** Rows that said `relay` now say `broker endpoint` or
   `front`; the layers they distinguish are the same ones.
3. **An upgrade leaves stale relay processes behind.** A host that was running jails under the
   previous version still has those relays and their `/tmp` files. Nothing kills them at the next
   launch any more — `yolo prune --apply` sweeps them (so does a reboot, since they live in
   `/tmp`). They are harmless meanwhile: nothing publishes to them.

**Attaching to a jail no longer repairs its broker wiring.** `yolo` attaching to a running jail
used to re-spawn a dead relay. The front belongs to the launching process, so a jail whose
launcher is gone is **relaunched**, not attached-and-repaired.

**No config changes.** No manifest, no `yolo-jail.jsonc` key, and no environment variable moved.

### ⚠️ Restart the broker singleton after upgrading, or every OAuth refresh on that host fails

**This is the one thing on this page that needs a command run.** `just deploy` runs it for you
(`yolo broker restart`, at the end of the recipe). If you upgraded any other way — a plain
`just install`, a shipped bundle, a package — do it by hand **once**:

```console
$ yolo broker restart
```

**Why.** The singleton's socket path is deliberately unchanged (`/tmp/yolo-claude-oauth-broker.sock`),
which is what makes the move need no state migration — and it is also what makes a *previous
version's* broker, still running from before the upgrade, get picked up and reused. That daemon
was written for a host-to-host socket where the first bytes on a connection were the client's own
request. It now sits behind yolo's front, which prepends a **connection preamble**, so the old
daemon reads yolo's preamble **as the request**, answers it, and the jail's real request is never
read. Every Claude OAuth token refresh on that host then fails.

**And it does not look broken.** Liveness is "the socket accepts" now, so the pre-upgrade daemon
reports healthy everywhere it is asked: `yolo broker status`'s `socket accept:` row is green,
`yolo check`'s `daemon live (pid=…, socket accepting)` row is green, the front publishes its
endpoint, and the in-jail reachability witness — which connects and closes — passes, so the launch
is not refused. The one probe that catches it is `yolo check`'s **per-jail** row, which makes the
full protocol round trip through a real front and reports `front up, broker unreachable`. A reboot
also fixes it, since the daemon lives in `/tmp`.

**Nothing needs doing on a host that was not running a broker** — a fresh install, or a machine
rebooted since. The next launch spawns the current daemon, which reads the preamble.

### ⚠️ A workspace config that enables a loophole now needs the pack selected, or the jail will not start

**What changed.** Every loophole yolo ships is now carried by a pack (`audio`,
`cgroup-delegate`, `host-processes`, `journal` — see the entries below — plus
`claude-oauth-broker`, which rides the `claude` pack), and selecting the pack is what
*installs* it. `packs` is **user-config only**. So a workspace
`yolo-jail.jsonc` that says `"loopholes": {"journal": {"enabled": true}}` while
`~/.config/yolo-jail/config.jsonc` does not say `"packs": ["journal"]` is a config **error**,
and a config error refuses the launch:

```
Invalid jail config:
  • config.loopholes.journal: …/yolo-jail.jsonc enables a loophole that is
    not installed on this machine. …
```

This rule is not new — enabling an uninstalled loophole from an agent-editable file has
always been refused — but until now none of yolo's own loopholes could *be* uninstalled.
Three of the four were bundled into the binary or were builtin services, so the name always
resolved.

**Who is affected.** Anyone whose **committed** `yolo-jail.jsonc` switches one of those four
loopholes on. That file is shared, so this hits every clone whose owner has not selected the
pack — including teammates and CI, not just the person who wrote the line.

**What to do.** Put the selection in your user config; leave the switch wherever it is
(`enabled` is honored from either scope):

```jsonc
// ~/.config/yolo-jail/config.jsonc
{ "packs": ["journal"] }
```

**The refusal now says this.** It used to offer exactly one remedy — write
`"loopholes": {"journal": {"command": ["<host daemon argv>"]}}` — which predates packs, was
unwritable in the file being refused (installing is user-scope too), and would have had you
hand-roll a daemon yolo already ships. It names `packs` first now, and says that key is
user-scope as well. The user-config half of the same situation was a warning saying the entry
"is a no-op"; it carries the same remedy.

> [!NOTE]
> **`loopholes.audio-alsa` is answered by name.** That loophole was merged into `audio` (see
> the `audio` entry below), and an entry still using the old name got the generic message
> above — a launch refusal in a workspace file, a "this entry is a no-op" warning in a user
> file, and in neither case the word `audio`. It is now reported as retired, naming its
> replacement and the pack that ships it.

### ⚠️ A loophole manifest with both `settings` and a `jail_daemon` must now declare `state_files`

**What changed.** yolo writes a loophole's resolved settings — the values your config supplied
under `loopholes.<name>.settings` — into a file in that loophole's own state dir. A loophole
that also runs a **jail daemon** gets that state dir bind-mounted into the container, and with
`state_files` **absent** the mount is the whole directory, settings file included. Such a
manifest is now **refused at load**, naming `state_files`; so is a `state_files` that lists
`settings.json` outright.

**Who is affected.** Authors of third-party loopholes that declare `settings` *and* a
`jail_daemon`. No pack yolo ships does both, so nothing in a stock install changes. A manifest
that does will fail to load, which makes the loophole vanish with a warning naming the file.

**What to do.** List the state files that genuinely have to cross, and leave the settings file
out:

```jsonc
// manifest.jsonc
"state_files": ["ca.crt", "server.crt"]
```

A jail-side process that needs configuration gets it through `jail_env`, which is the channel
that was always meant for it.

**Why.** `scope: "user"` on a setting exists to keep a value out of a file the agent can reach.
Publishing the *resolved* value into the jail read-only hands the agent the same value one
directory over — and the file's `0600` mode protects nothing there, because a jail's agent runs
as UID 0 by design. Requiring the manifest to say what crosses is the least-privilege spelling
`state_files` was introduced for; the alternative — quietly subtracting one file from a mount
the author declared — would be a carve-out the author cannot see.
📄 [`pack-config-keys.md`](design/pack-config-keys.md) §2.3.

### ⚠️ `yolo-cglimit` stops working out of the box

**What changed.** The cgroup delegate — the host-side helper that lets a jail set limits on
its own sub-processes — is **opt-in**. It used to start whenever the platform allowed
(*"Linux only, cgroup v2 only"*) with **no config key anywhere**: there was nothing to turn
on and nothing to turn off. It is now an ordinary pack-shipped loophole, off until you ask.

**Who is affected.** Anyone who runs `yolo-cglimit` inside a jail, and anyone whose agents
do. On upgrade the jail's boot output says `cgroup delegate: not available (no host daemon
socket)` and `yolo-cglimit` reports that it is not wired up.

**What to do.** Two lines, in your user config:

```jsonc
// ~/.config/yolo-jail/config.jsonc
{
  "packs": ["claude", "cgroup-delegate"],
  "loopholes": { "cgroup-delegate": { "enabled": true } }
}
```

Nothing else changes: the socket, the client and the per-job limits are what they were.

**Why.** This is a **stated, accepted cost** rather than a discovered one — the ruling that
made it opt-in named it in the same sentence. The delegate was the last presence-activated
host-side service in the tree, and *"the moment one builtin stays presence-activated,
'presence never activates' stops being a rule anyone can rely on while reading the code."*
The counter-argument was heard and overruled: the delegate hands a jail control of **its
own** cgroup rather than reading host state, so the severity is genuinely lower — but the
rule is about the mechanism, not the severity, and a rule with one exception is two rules.
📄 [`loophole-activation.md`](design/loophole-activation.md) OQ-A4, R1.

> [!NOTE]
> **`loopholes.cgroup-delegate` used to be a config ERROR** — the name was reserved for the
> built-in — and that refusal is gone, because it would have made the new switch
> unwritable. A config **cannot** redefine the delegate: with the pack selected, `command`
> and `doctor_cmd` under that name are refused by the ordinary override rule.

### ⚠️ The top-level `journal` key is gone, and `yolo-journalctl` needs a pack now

**What changed.** Two things, and either one alone stops `yolo-journalctl` working.

The **top-level `journal` key is no longer recognised** — a config that still carries it
is **refused**, naming the replacement. Unlike `host_processes`, this one got no
warning release first: the key had no migration window because the thing it switched on
did not exist as a loophole until now.

And the bridge **ships in a pack**, `journal`, instead of being a service compiled into
yolo and started by hand. Nothing is on by default, so it is off until you select the
pack — the same rule as any agent pack you never listed.

**Who is affected.** Anyone whose config sets `journal` at all, in either scope, to any
value — including `"journal": "off"` and `"journal": false`, which were previously inert.

**What to do.** Two lines for what `"journal": "user"` (or the bare `true`) used to do:

```jsonc
// ~/.config/yolo-jail/config.jsonc
{
  "packs": ["claude", "journal"],
  "loopholes": { "journal": { "enabled": true } }
}
```

`"journal": "off"` / `false` / absent needs nothing at all — don't select the pack.

**`"journal": "full"` needs one more key, and it has to be in your USER config:**

```jsonc
// ~/.config/yolo-jail/config.jsonc  — NOT a workspace yolo-jail.jsonc
"loopholes": { "journal": { "enabled": true, "settings": { "full": true } } }
```

That scope is the point rather than a detail. `"journal": "full"` reads the **whole host
journal** — every unit, every user — and it was settable from a workspace
`yolo-jail.jsonc`, which is a file the agent inside the jail can rewrite, with no scope
rule anywhere. The setting declares `"scope": "user"`, so a workspace file supplying it
is now refused by name. A workspace may still switch the bridge **on** (`enabled` is
honored from either scope) and gets the narrow, `--user`-forced view.

It is a **boolean** rather than the old three-valued string on purpose: the settings type
set has no `enum`, so a string mode could carry any word — and the daemon narrows on the
exact literal `"user"`, meaning every *other* spelling, including a typo, would have read
as **full**.

**Why.** Core's config schema named exactly two loopholes by hand, and `host_processes`
(above) was the first to go. With this one gone, **yolo's config schema names no loophole
at all** — which is what makes converting a loophole to a pack mean something rather than
moving a file. 📄 [`loophole-activation.md`](design/loophole-activation.md) §1.4, OQ-A6 ·
[`pack-config-keys.md`](design/pack-config-keys.md) OQ-K4.

> [!NOTE]
> **Nothing jail-facing moved.** `yolo-journalctl` is unchanged, the env var is still
> `YOLO_SERVICE_JOURNAL_ENDPOINT`, and the endpoint is still
> `/run/yolo-services/journal.endpoint`. Under the hood the daemon stopped publishing its
> own loopback-TLS endpoint and now binds a plain socket behind yolo's front, because a
> pack-shipped loophole must — but that is invisible from inside a jail.

### ⚠️ The Claude OAuth broker no longer runs on every host

**What changed.** yolo used to start the broker singleton — and one relay per jail — on
**every launch, for every user**, with no lookup of any kind. It now starts only when this
launch's `claude-oauth-broker` loophole is active, which is the same predicate that already
decided whether the jail was *wired* to it.

**Who is affected.** Anyone whose jails were not using the broker anyway, which included
everyone with `packs: []`, everyone who had set
`"loopholes": {"claude-oauth-broker": {"enabled": false}}`, and everyone with no `claude` on
the **host's** PATH. All three were getting a host daemon they never asked for *and* no
protection from it — the jail wiring was gated even while the daemon was not.

**What to do.** Nothing, unless you were relying on the singleton for something outside a
jail. If you want it back on a host where the loophole is inactive, the switch is the
loophole's own:

```jsonc
// ~/.config/yolo-jail/config.jsonc
"loopholes": { "claude-oauth-broker": { "enabled": true } }
```

Note the remaining gate this does **not** remove: the loophole still requires `claude` on the
host's PATH, so a jail-only Claude user is still unprotected. That is a live defect with its
own fix pending — the loophole moving inside `packs/claude`, where selecting the pack is the
dependency. 📄 [`loophole-activation.md`](design/loophole-activation.md) §1.1, OQ-A11.

### ⚠️ The top-level `host_processes` key is gone, and `yolo-ps` needs a pack now

**What changed.** Two things, and either one alone stops `yolo-ps` working.

The **top-level `host_processes` block is no longer recognised** — a config that still
carries it is now **refused**, naming the replacement. It was honored-with-a-warning
through the previous release; this is the deletion that warning was for.

And the loophole itself **ships in a pack**, `host-processes`, instead of being bundled
into the binary. Nothing is on by default, so it is off until you select the pack — the
same rule as any agent pack you never listed.

**Who is affected.** Anyone using `yolo-ps`, and anyone whose config carries
`host_processes` at all — including a config where it was already inert.

**What to do.** Three lines, in two files, and all three are needed:

```jsonc
// ~/.config/yolo-jail/config.jsonc   — user scope
{
  "packs": ["claude", "host-processes"],
  "loopholes": { "host-processes": { "enabled": true } }
}
```

```jsonc
// <workspace>/yolo-jail.jsonc        — workspace scope
{
  "loopholes": {
    "host-processes": { "settings": { "visible": ["sway", "waykeeper"] } }
  }
}
```

**Note the spelling.** The loophole is `host-processes` (hyphen); the retired key was
`host_processes` (underscore). And the allowlist is still resolved once at launch, so
editing it needs a jail restart.

**Why.** Core's config schema named two loopholes by hand, and that is what made
"convert the loophole to a pack" a separation in appearance only — the manifest would
leave core while core went on naming it. The keys now belong to the loophole's own
manifest. The refusal exists rather than silence because this block decided what a host
daemon would reveal about your machine: a config that still writes it and gets nothing
has been denied a capability it asked for, in the one direction where silence reads as
success. 📄 [`loophole-activation.md`](design/loophole-activation.md) §1.4 ·
[`pack-config-keys.md`](design/pack-config-keys.md).

### ⚠️ `host_processes.visible` moved, and it no longer applies without a restart

**What changed.** Two things, and the second is the one that bites.

The keys moved. `host_processes.visible` and `host_processes.fields` are now
`loopholes.host-processes.settings.visible` and `.fields` — declared by the
host-processes loophole's own `manifest.jsonc` instead of by yolo's config schema.
**Note the spelling:** the loophole is `host-processes` (hyphen); the retired top-level
key is `host_processes` (underscore).

And the allowlist is **frozen at launch**. The daemon used to re-read your workspace
`yolo-jail.jsonc` on *every request*, so editing `visible` took effect immediately.
It now reads a file yolo writes once, at jail start, so **an edit needs a jail
restart**.

**Who is affected.** Anyone who has ever put names in `host_processes.visible` — the
allowlist behind `yolo-ps`. The old key **still worked in this release**: it was folded
into the new settings at launch and warned, naming the replacement. Where both
spellings were present the new one won **per key**, so a half-migrated config did not
lose the key it did not touch. *(It stopped working in the entry above, which is the
deletion this migration window existed for.)*

**What to do.** Rewrite the block, and expect to restart the jail after changing it:

```jsonc
"loopholes": {
  "host-processes": {
    "settings": { "visible": ["sway", "waykeeper"] }
  }
}
```

**Why.** The live re-read was an affordance and a hole wearing the same face. The
property that let *you* widen an allowlist without restarting let the **agent in the
jail** widen its own — mid-session, with no launch, and therefore with no
config-approval prompt anywhere in the causal path. Freezing the value at launch puts
the change back behind the gate that already exists. 📄
[`pack-config-keys.md`](design/pack-config-keys.md) OQ-K3.

> [!NOTE]
> **This was the first half of a two-step move, and the second half has now shipped** —
> see the entry above. The top-level key was deleted once `host-processes` became a
> pack; deleting it before there was a pack to carry it would have stranded every
> existing config.

### `settings` — a loophole can declare its own config keys

**What changed.** A loophole's `manifest.jsonc` may declare a `settings` block naming
the config keys it owns, each with a `type` (`string`, `bool`, `int`, `string_list`), an
optional `default`, and a `scope`. Users supply values under
`loopholes.<name>.settings`, and yolo validates them against the declaration before
writing them to a file it hands the daemon.

**Who is affected.** Nobody's existing config changes meaning — the block is new and
optional. It matters to **pack authors**, who no longer need a key in yolo's own schema
to make a loophole configurable, and it is what the `host_processes` move above is built
on.

**What to do.** Nothing, unless you write a loophole. If you do, declare the keys rather
than expecting an opaque map: an undeclared key is a config **error** naming the keys
that do exist, and an unrecognised key *inside* a declaration is refused rather than
ignored — yolo will not hand a host daemon a value it could not validate.

One default worth knowing: **an undeclared `scope` means user-config-only.** A setting
can reach a host daemon, so silence is the strict answer; a key a workspace
`yolo-jail.jsonc` may set has to say `"scope": "workspace"` out loud. 📄
[`pack-config-keys.md`](design/pack-config-keys.md).

### ⚠️ npm-installed agent CLIs no longer update themselves

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

> [!IMPORTANT]
> **`claude` and `agy` are NOT covered by this, and the heading was narrowed to say so.** They are
> declared `program via installer`, not `via npm`, and that launcher is untouched: it still runs the
> vendor's own updater (`"$REAL_BIN" install`) on the first invocation after a boot and hourly after
> that. Measured in a development jail on 2026-08-18 — four `claude` binaries dating from June to
> July, ~250 MB each, none of them installed by a `yolo` command.
>
> This is a **gap, not a decision**. Two reasons it was not simply closed alongside the npm half:
> an `installer` declaration has **no version or digest field at all**, so there is nothing yolo could
> pin even if it wanted to; and the updater belongs to the vendor, so suppressing it is a different
> act from declining to run one ourselves. Which of those to do is being worked out in
> [`program-delivery.md`](design/program-delivery.md).

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

### ⚠️ `macos-user`: launchers stop hitting the network on every single invocation

**What changed.** The generated lazy-install launchers aged their throttle stamp with
`stat -c %Y`, which is **GNU-only**. The `macos-user` backend runs those launchers *natively* on
your Mac, where BSD `stat` rejects `-c` — and the error was swallowed, so the stamp's mtime read
as epoch 0 and every age came out ~56 years. That is permanently past `UPDATE_INTERVAL` and
permanently under `RETRY_INTERVAL`, so **every throttle was defeated**: each launch polled the
npm registry, ran a native agent's self-update, or retried an install that had already failed.

**Who is affected.** `macos-user` users only, and every launcher on that backend. The container
backends were never affected — the jail image is Linux, so `stat -c` worked there and the
throttles behaved as designed.

**What to do.** Nothing. The fix is in the generated launchers, so it applies on the next launch
that regenerates them.

**Why it went unnoticed.** No test exercised these scripts under BSD `stat`, and the failure mode
was extra traffic rather than an error — the launchers still worked, just noisily and slowly. It
surfaced only when `TestUnpinnedNpmLauncherTimeline` (added for the no-evergreen-npm ruling above)
ran on a Mac and asserted that a fresh stamp keeps the launcher off the network entirely.

### ⚠️ `audio` is now off by default, and it needs a pack

**What changed.** Two things, in two steps, and the second one landed after the first.

A loophole's manifest declares `default_enabled`, and absent now means **off**. `audio` ships
`default_enabled: false`, where it was previously on whenever the host's PulseAudio socket existed.

And the loophole **moved into the official `audio` pack**. It used to be bundled into the binary,
so it was present on every machine; now it arrives only if you select the pack — the same rule as
any agent pack you never listed. (If you had already selected `audio` for its `audio-alsa`
loophole: that loophole is **gone**, merged back into the plain `audio` name it only ever avoided
because the bundled copy had reserved it. `loopholes.audio-alsa.*` names nothing now.)

**Who is affected.** Anyone relying on sound in a jail — `/voice`, `sox`, `ffmpeg`, any ALSA client.

**What to do.** Both lines, in your user config:

```jsonc
// ~/.config/yolo-jail/config.jsonc
{
  "packs": ["claude", "audio"],
  "loopholes": { "audio": { "enabled": true } }
}
```

Everything the bundled loophole did, the pack does: both host sockets, `/dev/snd`, and the
ALSA→PipeWire routing. Two details differ and neither changes what works:

- The ALSA fragment lands at `/etc/alsa/conf.d/50-yolo-audio-alsa.conf` rather than
  `/etc/asound.conf`. alsa-lib reads the first before the second, so the routing is identical.
- `PULSE_SERVER` and `PIPEWIRE_REMOTE` are now set on **every** launch that selects the pack,
  rather than only when the loophole is active — a pack may not declare a loophole's `jail_env`, so
  they travel as the pack's `env` contribution. On a machine with no audio socket they point at
  something that is not there, which fails the same way it would on a host with no daemon.
- The Linux-only gate is now a `platforms: ["linux"]` declaration rather than a probe for the Pulse
  socket. On a Linux host with no audio you will see one `skipping bind mount, host source missing`
  warning per socket instead of the loophole quietly going inactive — which is the better answer
  for someone who just asked for audio.

**Why.** *"We don't give host access by default."* Being useful is not a reason to be automatic —
[`loophole-activation.md`](design/loophole-activation.md) R1 and R4. The move out of the binary is
the same rule one level up: shipped-in-the-binary is not installed.

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

> [!NOTE]
> **The approve path needs an interactive terminal and a reachable remote.** `yolo pack install`
> refuses to read a piped answer (`yes |` is not consent) and it is the one command that fetches, so
> in CI or offline only the other two choices are available — edit the pack, or drop it from `packs`.
> **And `yolo check` does not yet predict this refusal:** it reports the pack `[PASS]` and the launch
> then refuses, so the preflight is not the place to confirm a pack is approved. Nothing else reports
> it either — `yolo pack status` shows pins and drift, not approvals — so until that is closed the
> lockfile's own `approvedHostAccess` (beside your config, `packs.lock.json`) is the only record, and
> `yolo pack footprint` is what shows the claims it has to cover.

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
