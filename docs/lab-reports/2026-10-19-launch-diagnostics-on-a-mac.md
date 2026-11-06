---
title: "Two launch diagnostics that sent a reader the wrong way"
date: 2026-10-19
status: open
tags: [lab-report, apple-container, podman, macos, symlinks, diagnostics, happy-path, provisioning]
summary: "Three diagnostics from one session. Two launches reported as podman were Apple Container, because YOLO_BACKEND is not a yolo variable and the line naming the runtime goes only to the launch log — both halves now ruled: name the backend, and warn on an unknown YOLO_*. A Ctrl-C during provisioning blamed permissions and disk while the log was complete and 67 GiB were free. And the first real podman launch died on a bare statfs naming a path that exists, because the workspace was spelled through the /tmp symlink, which podman's macOS VM cannot resolve."
---

# Two launch diagnostics that sent a reader the wrong way, 2026-10-19

A [lab report](README.md). Host: macOS 26.5, arm64. Workspace `/tmp/jail_test`, yolo
`0.14.0+555.gc3a38e634b7e`. Both findings are about what a launch *says*, not what it does, and
each cost the maintainer a wrong conclusion in this session.

## 1. The runtime a launch chose is never printed, and `YOLO_BACKEND` is not a variable

**Observed.** Two launches were made as
`YOLO_ALLOW_MISSING_PROGRAMS=1 YOLO_BACKEND=podman yolo`, and reported as podman runs. Both ran
on **Apple Container**: `/tmp/jail_test/.yolo/launch.log` records
`jail: yolo-jail-test-1d7fd0de  runtime=container  notch=jail` for the 10:39:22 and 10:40:43
launches.

**Two causes, and either alone would have been caught.**

- **`YOLO_BACKEND` is not a yolo dial.** The spelling appears nowhere in the tree — no Go source,
  no doc, no config example. The dial is `YOLO_RUNTIME`
  ([`config-ref`'s `ENVIRONMENT VARIABLES`](../reference/storage-and-config.md)). An unknown
  `YOLO_*` variable is accepted in silence, so a typo or a misremembered name is
  indistinguishable from a working override.
- **Nothing on screen names the backend.** The line that carries it is written with
  `o.logRecord` ([`launchcontext.go:633`](../../internal/cli/run/launchcontext.go)), and
  `logRecord` writes to `LaunchLogOnly(o.Stderr)`
  ([`launchcontext.go:81`](../../internal/cli/run/launchcontext.go)) — the launch log alone.
  ⚠ The `yolo: container.provision took …` timing line is **not** a disclosure of which backend
  ran: `container.provision` is recorded for any container backend
  ([`jailperf.go:151`](../../internal/cli/run/jailperf.go)), so it reads the same for podman and
  for Apple Container.

**Why it matters more than a typo.** The two backends fail differently, and this session has one
defect that reaches only one of them: the Apple Container build gate rejects every real
`container ls` ([the store-build design's Apple arm](../design/pi-extension-store-builds.md),
on the roadmap), and its gate is `runtime == "container"`, so podman never meets it. A reader who
believes they switched runtimes and did not will attribute a backend-specific failure to both,
which is exactly what happened. **Podman remains untested on this host.**

**RULED 2026-10-19, by the maintainer, both halves:** the launch **names the backend on
screen**, and an unrecognized `YOLO_*` variable **warns**
(*"I like the idea of adding an unknown YOLO_* warning … and yes we should name the backend"*). ⚠ The warning half is not free, and
whoever builds it inherits this: yolo
deliberately gives `YOLO_*` dials no central registry
([AGENTS.md](../../AGENTS.md), *"`YOLO_*` environment dials have no authority row, because they
have no authority"*), so "unknown" has no cheap definition today, and a warning list is a second
copy of a set that is deliberately distributed. The screen line needs no registry.

## 2. An interrupted provisioning stage blames permissions and disk

**Observed.** A `Ctrl-C` during the mise stage printed:

```text
Provisioning log could not be written: /workspace/.yolo/startup.log — repair workspace .yolo
permissions or free disk space, then retry yolo.
```

**Both named causes were false, and the run itself disproves them.**

| Claim | Measured |
| :--- | :--- |
| the log could not be written | `/tmp/jail_test/.yolo/startup.log` is 6637 bytes, mtime 10:45, and its **last line is `PROVISIONING FAILED (exit 141)`** — written by a later append in the same run |
| repair `.yolo` permissions | `-rw-r--r--  1 matt  wheel`, owned by the invoking user |
| free disk space | 67 GiB free on the volume |

**The mechanism.** The provisioning stage runs `(setup) 2>&1 | yolo internal log-append --tee`
and reads `PIPESTATUS`: `_prc` is the setup's status, `_plog` the log writer's
([`internal/provision/provision.go`](../../internal/provision/provision.go)). `Ctrl-C` signals
the whole foreground process group, so the writer is killed too and `_plog` is non-zero;
`logFailure` then fires with its permissions-and-disk text. `_prc` is **141** — `128 + 13`,
SIGPIPE — because the setup's stdout closed when the writer died first.

> [!NOTE]
> **The run contradicts itself inside one stage, which is the cleanest evidence available.**
> `_plog` says the log is unwritable, and the very next `printf … | log-append` wrote
> `PROVISIONING FAILED (exit 141)` into that same file successfully. A guard that fires on a
> signalled writer cannot be reporting a write failure.

**What a fix has to decide.** A signalled writer is not a failed one: a `_plog` of 130 (SIGINT)
or 141 (SIGPIPE) is the user's own interrupt, and its next step is nothing. Whether that is
distinguished by status, or whether the message simply stops naming causes it has not checked,
is a ruling — [the happy-path principle](../reference/happy-path-principle.md) says a stop names
*the easiest next step that keeps the user's trust*, and naming two wrong ones spends it instead.

## 3. A podman launch cannot use a workspace reached through a symlink

**Observed.** The first real podman launch of that workspace — `YOLO_RUNTIME=podman yolo` in
`/tmp/jail_test` — got as far as starting the keeper and then died:

```text
Error: statfs /tmp/jail_test/.yolo/home/local: no such file or directory
```

**The named path exists**, and yolo had just created it: `<ws>/.yolo/home/local` is one of the
bind sources `preparePodmanBindSources` makes, and the overlay now holds the whole undotted set
(`local`, `config`, `npm-global`, `go`, `ssh`, `yolo-bin`, `claude`, `codex`, `copilot`, `pi`)
beside the dotted set an earlier Apple Container launch left.

**The cause is the `/tmp` symlink, not a missing directory.** Measured by binding each path with
podman directly:

| Bind source | Result |
| :--- | :--- |
| `/Users`, `/private/tmp`, `/Users/Shared/yolo` | bind |
| `/tmp/jail_test` | `Error: statfs /tmp/jail_test: no such file or directory` |
| **`/private/tmp/jail_test`** | **binds** |

`/tmp` is a symlink to `private/tmp`. Podman on macOS runs in a VM, and the VM cannot resolve the
host's `/tmp` symlink, so **any bind source spelled through a symlinked prefix fails** while the
resolved spelling of the same directory works. The workspace is shared into the VM either way.

**The path the error names is just whichever bind podman checks first.** Two later launches from
`/tmp/jail_test` the same day named different paths:

| Launch | Error names | On the host |
| :--- | :--- | :--- |
| 13:09, installed `yolo` (`0.14.0+562.g39bc3302286b`) | `.yolo/home/yolo-gitconfig` | **absent** |
| 13:13, the same source plus a print after the gitconfig write | `.yolo/home/npm-global` | exists |

The second run's print showed the composed gitconfig written without error (418 bytes), and
podman still refused a directory that exists, so one cause covers all three errors.

⚠ **One fact this does not explain:** before the 13:13 run, `yolo-gitconfig` was missing from the
overlay and `.yolo/home` had not been modified since 10:50, although `host-perf.log` records
`assemble.host_git_identity` running at 13:09. That launch should have created the file, through
[`gitIdentityMountArgs`](../../internal/cli/run/assemble_parts.go), which discards the write's
error. The same write succeeds when run alone and in the 13:13 launch. The failure is
unreproduced and does not change the diagnosis, but a bind source written with its error thrown
away and then found missing is the shape `preparePodmanBindSources` was written to rule out.

> [!WARNING]
> **This is [AGENTS.md](../../AGENTS.md)'s darwin symlink class, on the production side.** The
> documented version is a test comparing an unresolved fixture path against code that resolves;
> here it is yolo handing podman a path it has *not* resolved, to a consumer that cannot. The
> guard that exists for the adjacent cause —
> [`preparePodmanBindSources`](../../internal/cli/run/prepare.go), whose comment says podman's
> bare `statfs` *"reads as a yolo bug rather than a missing directory"* — creates the sources but
> never resolves the path they are spelled with, so the symptom it was written to prevent arrives
> anyway, by another route.

**Workaround, verified at the bind level:** launch from the resolved path
(`cd /private/tmp/jail_test`). A full launch there was not run, because this host's VPN stalls the
mise stage ([below](#not-a-yolo-defect)).

**What a fix has to decide.** Whether the launch resolves the workspace (and every bind source)
with `EvalSymlinks` before handing it to podman — which makes the jail's own paths differ from
what the user typed, and `/workspace` is a mount destination, not a host path — or whether it
preflights and refuses with the resolved spelling as the next step. The first is invisible and
total; the second keeps the user's spelling and teaches the rule once.

**The investigating agent's recommendation (not a ruling):** resolve. The user never typed the bind
sources, so a refusal stops them over a spelling they did not choose, and the jail still sees the
workspace at `/workspace`, whatever the host path is.

## 4. `yolo host --` calls its cwd a bind-mounted workspace, and refuses `aws-auth` in the home

**Observed.** `yolo host -- claude`, run from `/Users/matt`:

```text
Refusing to start loophole aws-auth: loophole 'aws-auth': module dir
/Users/matt/.local/share/yolo-jail/embedded-packs/<hash>/aws-auth/loopholes/aws-auth is inside
the workspace this launch bind-mounts :rw, where an agent can rewrite it between launches
```

**The premise is false at that notch: `yolo host` bind-mounts no workspace.** The host notch takes
its workspace from `os.Getwd()` ([`internal/cli/host.go:2399`](../../internal/cli/host.go)) and
hands it to the placement rule, whose workspace branch is worded for a jail —
*"the workspace this launch bind-mounts :rw"*
([`loopholeplacement.go:110`](../../internal/config/loopholeplacement.go)). Run from `$HOME`, that
tree contains `~/.local/share/yolo-jail`, so the rule fires on yolo's own store.

⚠ **The refused tree is the one deliberately placed to satisfy this rule.**
`paths.EmbeddedPacksDir` is kept out of `cache/` precisely because *"every jail mounts
[cache] read-write"* ([AGENTS.md](../../AGENTS.md)), so no agent writes `embedded-packs` — and the
rule rejects it anyway, for being under the home the user happened to `cd` into.

**The empty-workspace path already exists and the host notch does not take it.**
`agentWritableTrees` documents *"workspace may be empty (a caller with no workspace in hand), in
which case only the jail home tree is checked"*, and `internal/loopholes` passes `""` in two
places. A notch that mounts nothing has no workspace in hand.

**Two more things the same run showed.**

- **The refusal did not stop the doorway.** Three lines later:
  *"opened the `aws-auth` doorway (pack `aws-auth`, pid 30076) on 127.0.0.1:61750 for claude"*.
  Claude then started on Bedrock, so the practical damage here is a red refusal with a false
  reason rather than a broken launch — but a gate that prints "Refusing" and is then bypassed is
  worth a look on its own.
- **A jail launch would have refused the cwd outright.** `refuseWorkspaceScope` makes a workspace
  that IS `$HOME` a hard refusal — *"the SECOND thing `Run` does"* — and the host notch does not
  go through `Run`, so `$HOME` is accepted there and only this rule notices.

**Workaround:** run `yolo host` from any directory that is not an ancestor of
`~/.local/share/yolo-jail` — the repo checkout under `/Users/Shared` does it.

**What a fix has to decide.** Whether the host notch passes no workspace (the rule then checks the
jail home tree alone, which is what that notch actually hands an agent), or whether the rule keeps
a workspace at this notch and the message stops claiming a mount. The first looks right and is
the smaller change; neither is mine to pick.

## Not a yolo defect

**Opus 5.5 missing from Claude Code's `/model` menu is not yolo's doing** — it is the user's own
`model` pin, and ⚠ **an earlier version of this section got the reason wrong**: it claimed Claude
Code 2.1.303 ships no Opus entry at all. **It does.** `claude --model opus -p …` resolves and
answers on this host, on Bedrock. What a pin to a *custom* Opus id does is **replace** the
built-in Opus row with a `Custom Opus model` row — so pinning `us.anthropic.claude-opus-5[1m]`
hid the Opus 5.5 the menu would otherwise offer. The maintainer had already observed the
converse and said so: *"rejecting pins makes it appear on linux."* A pin both freezes the
version and hides its successor.

Nothing on the host caches a model list, and that much was checked four ways and holds:
`packs/bedrock/pack.json` carries **no model ids at all**; `yolo config diff claude` shows the
only captured edit for `claude/settings` is `enabledPlugins {}`, itself flagged *"same as yolo's
last render — redundant capture"*, with no `model` key; both `claude` binaries on the machine are
2.1.303 installed the same day, so the host floor's copy is not stale; and the one place yolo
touches a Bedrock model value — `ANTHROPIC_MODEL`'s `matches` in
[`packs/claude/pack.toml`](../../packs/claude/pack.toml) — is a prefix check on the
inference-profile/ARN shape that admits every Opus 5.5 id tested, filtering no family. Bedrock
had the model all along (`us.anthropic.claude-opus-5-5`, `ACTIVE` in `us-east-1`); repinning to
it gave `Opus 5.5 (1M context) · Amazon Bedrock` on the next launch.

> [!NOTE]
> **The lesson is about method, not models.** Two machine-side explanations were checked and
> cleared before the user's own one-line observation settled it, and the guess offered in between
> was wrong in a way the available evidence already contradicted. A menu is rendered from state
> the user owns; read that state before theorising about the renderer.

The mise stage's long stall — `github:pantsbuild/scie-pants@0.13.2` retrying
`mise-versions.jdx.dev` and then `api.github.com` at 20 s a time for over two minutes — is **the
maintainer's VPN blocking container egress, a known issue on this host**, not yolo's. Recorded
only so a later reader does not re-diagnose it: the host reached both URLs in 0.42 s and 0.11 s
while the jail could not, which is the signature of that VPN and not of a yolo network decision.
No bound or hint from yolo is claimed to be at fault here.

## Evidence

```console
$ grep -rn YOLO_BACKEND .                 # no match anywhere in the tree
$ grep 'runtime=' /tmp/jail_test/.yolo/launch.log | tail -1
  jail: yolo-jail-test-1d7fd0de  runtime=container  notch=jail
$ tail -1 /tmp/jail_test/.yolo/startup.log
PROVISIONING FAILED (exit 141)
$ ls -l /tmp/jail_test/.yolo/startup.log   # -rw-r--r-- 1 matt wheel 6637
$ df -h /tmp | tail -1                     # 67Gi avail
$ curl -s -o /dev/null -m 12 -w '%{http_code} %{time_total}\n' https://api.github.com/
200 0.113234
```
