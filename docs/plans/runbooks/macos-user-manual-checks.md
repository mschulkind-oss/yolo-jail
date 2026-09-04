# What only a Mac with a password can verify

**Audience:** whoever has a Mac and five minutes. Every item here is something no
test can reach, and the list is deliberately short — everything that *could* be
automated was, in `internal/entrypoint/darwinbootstrap_darwin_test.go` and
`internal/macosuser/staging_darwin_test.go`, both of which run on any Mac with no
privilege at all.

**What those cover, so you do not re-check it by hand:** the generated home (shims,
launcher dir, login rc files, mise config), a blocker actually executing and
refusing with its message and suggestion, the composed home overlay landing at its
destinations and replacing a removed pack's subtree, a missing overlay degrading to
a warning, the staging commands really running, the a+rX mode, the replace-not-merge
shape, and the J2 fresh-inode rule (re-stage, then exec).

What remains needs either root or a kernel, and is three facts rather than a
mechanism.

---

## 1. The privilege transition

```console
$ YOLO_RUNTIME=macos-user yolo -- bash -lc 'whoami; pwd'
```

**Expect:** `_yolojail`, and the workspace path. One sudo prompt.

This is the whole irreducible remainder: `sudo` running at all, the `-u _yolojail`
switch landing, and the staged binary self-exec'ing as that user. Everything before
and after it is covered by the harnesses above.

## 2. Seatbelt is actually applied

```console
$ YOLO_RUNTIME=macos-user yolo -- bash -lc 'ls /Users/$(logname)/.ssh; ls /Library/Keychains'
```

**Expect:** `Operation not permitted` for both.

> [!WARNING]
> **Not `~/.ssh`.** Inside the jail `~` is the SANDBOX's home, which has no `.ssh`,
> so that spelling returns "No such file or directory" — which proves nothing and
> looks like a pass. The check has to name the HOST user's path explicitly, because
> the thing being tested is that the sandbox cannot read a home that is not its own.
> This runbook said `~/.ssh` on its first outing and got exactly that non-answer.

The profile is generated as a pure string and pinned by unit tests; what no test can
check is that the kernel loaded it. A jail that looks right and confines nothing is
the failure this catches, and it is silent otherwise.

## 3. The acceptance bar — `packages:` reaches the agent

**Name a package your own config actually declares.** Check first:

```console
$ yolo config-dump 2>/dev/null | grep -A5 '"packages"'
```

then ask for one of those:

```console
$ YOLO_RUNTIME=macos-user yolo -- bash -lc 'which just'   # or any declared package
```

**Expect:** a `/nix/store/…` path.

> [!WARNING]
> **A tool macOS already ships proves nothing here.** `which jq` returning
> `/usr/bin/jq` is not a failure of the acceptance bar — it means `jq` was never in
> `packages:` and the system copy answered. This runbook suggested `jq` on its first
> outing and got that non-answer. Use a package the config declares, or the check
> cannot distinguish "nix delivered it" from "macOS already had it".

This is the backend's founding requirement — it honors `packages:` via native nix or
it does not ship. It exercises the whole native chain in one command: the build, the
GC root, the PATH prefix, and the login-rc re-prepend surviving macOS `path_helper`
(the OQ-1 question). A Homebrew path here means the re-prepend lost.

## 4. Content actually reached the agent

```console
$ YOLO_RUNTIME=macos-user yolo -- bash -lc 'ls ~/.claude/skills; head -3 ~/.claude/CLAUDE.md'
```

**Expect:** the built-in skills, and briefing prose.

The install is covered by test; what is not is that the launch composes and stages
it *for real*, through sudo, into the actual sandbox home.

---

## Known-absent, do not report as bugs

- **`mcp_presets`** are not delivered here — the wrappers hardcode Linux paths this
  backend never provisions. The launch says so.
- **`mise_tools` are not installed.** The shims dir is on PATH so it looks
  provisioned, but nothing provides a `mise` binary the sandbox can reach and nothing
  runs `mise install` — that step is in the container provisioning script this backend
  does not run. Declare the tool in `packages:` instead, which IS materialized here.
- **`per_side_paths`, `resources`, `cache_relocations`** are read and ignored, each
  for a structural reason (no mount namespace, no cgroups, no binds). Each warns.
- **One home for every workspace.** A second workspace launched concurrently
  overwrites this one's briefings. See `docs/design/macos-user-home-tiers.md`.
- **A jail here cannot launch a jail.** `sudo` cannot exec under Seatbelt and
  `sandbox_apply` refuses a second profile, so verify macos-user changes from an
  unsandboxed shell.
