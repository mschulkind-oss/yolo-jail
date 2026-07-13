# How the macOS-user sandbox actually contains an agent

**Audience:** anyone reasoning about what the `runtime: "macos-user"` backend
does and does not protect — to build a correct mental model, not a hopeful one.
**Status:** describes the code as it runs today (`src/cli/macos_user.py`),
which differs from the original proposal in one load-bearing way (see
[§6](#6-the-gap-between-this-doc-and-the-design-doc)).
**Reads with:** [macos-native-user-sandbox-design.md](macos-native-user-sandbox-design.md)
(the *why* + the honest security delta vs. the container).

This is the doc to argue with. If a claim here doesn't match what you can
actually do from inside the sandbox, that's a bug in the code or the doc —
tell me which.

## 0. The one-sentence version

An agent under `macos-user` runs as a **different real macOS user**
(`_yolojail`) wrapped in an **Apple Seatbelt profile**, sharing exactly one
directory with you — a **neutral location outside everyone's home**
(`/Users/Shared/yolo/<project>`), never your `~`. It can read most of the
system but can **write almost nothing except that shared workspace**, and
**cannot read the things that matter** — your home's private files, other
users, the keychain files, raw disk. It is a *credible* boundary, *weaker than
the Linux container* (shared kernel, no resource caps), and it depends on a
deprecated-but-ubiquitous Apple tool.

## 1. Why you're in the same directory but `cd ..` fails

This is the most useful thing to understand, because it's exactly what you saw.

The agent is launched **cd'd into your workspace** — the same path you ran
`yolo` from — on purpose. The launch command ends with
`zsh -c "cd <workspace> && exec <agent>"` (`macos_user.py`, `launch_argv`).
We match the container backend's "you're in your project" feel; there's no
`/workspace` remapping, you're at the real path.

The workspace lives on **neutral ground** — a dedicated shared directory
outside every user's home, `/Users/Shared/yolo/<project>` by default (the
run path *refuses* a workspace inside a home; see [§2](#2-where-work-lives-neutral-ground-not-your-home)).

But you are **not you** in that shell. You're `_yolojail`, a different UID.
And the Seatbelt profile says (`macos_user.py`, `seatbelt_profile`):

```lisp
(deny file-read* (subpath "/Users"))          ; everything under /Users: no read
(allow file-read*
    (literal "/Users")                          ; the bare directory entry only
    (literal "/Users/Shared")                   ; traverse INTO the shared area
    (subpath "<your workspace>")                ; your project: full read
    (subpath "/Users/_yolojail"))               ; the sandbox's own home
```

So `cd ..` out of your workspace eventually hits `/Users/<you>` (your home),
which is under the `/Users` deny and **not** re-allowed. The kernel refuses
the read/traversal → **permission error**. Your workspace is a lit room; step
far enough up and you hit a locked door. That error is the sandbox working,
not something misconfigured.

Note what is *not* here anymore: there are **no per-ancestor grants**. Because
the workspace sits under `/Users/Shared` (neutral ground), the two `literal`
entries (`/Users`, `/Users/Shared`) are the only traversal the sandbox needs
to reach it — nothing is opened up inside your home to make a nested project
reachable. That "thread a grant through `~`" machinery is gone (see §2).

## 2. Where work lives — neutral ground, not your home

This is the design decision that makes the whole thing easy to reason about.

**The rule: the workspace is shared host↔sandbox live (same files, real-time
edits, no copy) — but it may never live inside anyone's home directory.**

- Default location: **`/Users/Shared/yolo/<project>`**. Overridable to any
  non-home path via config `macos_shared_root` (e.g. `/opt/yolo`, an external
  volume). A path inside a home is rejected by config validation *and* by the
  run path (`home_containing` in `macos_user.py` — one source of truth).
- You put the project there to start (`mv ~/code/proj /Users/Shared/yolo/`,
  or clone/start it there). That's the one ergonomic cost. There is **no**
  copy-in/sync-back, no `sv-clone`-style git round-trip — you and the agent
  edit the *same inodes* in real time, exactly like the container backend's
  bind mount.

**Why this and not "share my project where it already is":** to share a
project nested at `~/code/proj` with a different UID, you'd have to open a
traversal path *through your home* (`/Users/you`, `/Users/you/code`, …). That
is layered access control threaded through the most sensitive directory on
the machine — precisely where a mistake (an over-broad grant, a leftover ACE)
silently exposes `~/.ssh`. A neutral, sibling directory removes the entire
problem: **no grant ever touches your home**, and the mental model collapses
to one sentence — *"the agent and I share exactly one directory named `Shared`,
and nothing else of mine is reachable."* Nobody drops an ssh key into a
directory called `Shared/yolo` by accident.

**How the one shared directory is shared** (all on that tree, never your home):
- `macos-setup` provisions the root `mkdir -p` + `chown you:_yolojail` +
  `chmod 2770` — **setgid** so projects you create under it inherit the shared
  `_yolojail` group, group-rwx, no access for "other"
  (`shared_root_provision_commands`).
- At run start, an **inheriting ACL** (`chmod +a`, the dir/file split) is
  stamped on the project subtree so both UIDs read+write the same inodes and
  new files inherit the grant. macOS ACLs grant independent of the umask, so
  this survives `git checkout`/`tar`/`unzip` writing restrictive modes
  (`workspace_acl_apply_script`).
- Teardown is clean: `yolo macos-unshare <project>` runs `chmod -h -N` to
  strip every ACL back to plain POSIX (`workspace_acl_strip_script`). Because
  we only ever ACL neutral ground, this provably leaves nothing of yours
  outside the workspace altered.

**Bonus:** `/Users/Shared` also sidesteps macOS TCC entirely (Documents /
Desktop / Downloads / iCloud are both TCC-protected *and* inside your home —
a workspace there would be double-blocked; this dodges both).

The honest cost: a monorepo or symlink layout that **straddles** the boundary
(e.g. expects a sibling repo at `../other` back in `~`) won't work — keep the
related trees together under the shared root. Everything else you'd "lose" by
moving out of `~` is host access the sandbox exists to deny.

## 3. The two walls (this is the whole model)

The boundary is **two independent mechanisms**. Understanding which one is
doing the work in each case is the mental model.

### Wall 1 — a different UID (macOS user separation)

`yolo macos-setup` creates a dedicated account `_yolojail`
(`macos_user.py:create_user_commands`): hidden from the login window
(`IsHidden 1`), stripped from the `staff` group, its own home at
`/Users/_yolojail`, a random password never passed on a command line
(`_set_random_password`). The agent runs as this user via
`sudo --user=_yolojail` (`macos_user.py:launch_argv`).

What this wall gives you **for free**, without any profile:
- **The login keychain is cryptographically unreachable.** A different UID
  cannot unlock your Keychain — this is enforced by the OS crypto, not by a
  rule we wrote. Secrets stored in Keychain (Safari passwords, many app
  tokens) are safe structurally.
- **A fresh, empty TCC database.** The sandbox user hasn't granted any app
  access to Documents/Photos/etc.
- **Normal UNIX file permissions apply.** Anything mode `0600`/`0700` owned by
  *you* is unreadable to `_yolojail` the ordinary way.

### Wall 2 — the Seatbelt profile (`sandbox-exec`)

A root-owned, `0444`, per-session profile the agent can't edit
(`macos_user.py:seatbelt_profile`, installed to `/var/yolo-jail/profile-*.sb`).
Its shape is **`(allow default)` + targeted denies** — permissive base, deny
what matters (`macos_user.py:372`). Last-match-wins, so each deny is followed
by narrower re-allows.

The denies that are the actual boundary:

| Rule (`macos_user.py`) | Effect | Why it's load-bearing |
|---|---|---|
| `(deny file-write* (subpath "/"))` then re-allow workspace + sandbox home + `/tmp` + `/var/folders` + `/dev` | **Writes: nothing except your workspace and scratch** | This is the big one — the agent cannot modify your host, /usr, /etc, /Applications, other projects. |
| `(deny file-read* (subpath "/Users"))` + re-allows | Other users' homes **and your own home** are unreadable | The credential wall: `~/.ssh`, `~/.aws`, `~/.gitconfig` sit under `/Users/<you>` → denied. |
| `(deny file-read* (subpath "/Library/Keychains"))` | The keychain **files** are unreadable | `System.keychain` is world-readable `0644` on stock macOS, so UID separation alone doesn't cover it — this deny does. |
| `(deny file-read* (subpath "/Volumes"))` + re-allow boot volume | External/other volumes unreadable | Stops reading mounted disks, backups, other APFS volumes. |
| `(deny … (regex #"^/dev/r?disk") … #"^/dev/bpf")` | Raw disk devices + packet capture denied | Blocks reading the raw filesystem (bypassing perms) and sniffing network traffic. |

## 4. What the agent CAN do (be honest about the blast radius)

Not a jail in the "nothing gets out" sense. Inside the sandbox the agent can:

- **Read most of the system**: `/usr`, `/bin`, `/Library` (except Keychains),
  `/Applications`, system frameworks, Homebrew, the boot volume generally.
  The base is `(allow default)` for reads — we *deny-list* secrets, we don't
  *allow-list* a minimal set. So "can it read X?" → yes unless X is in the
  deny table above.
- **Write to your workspace** (that's the point) and to `/tmp`,
  `/var/folders`, `/dev`.
- **Full network access.** Egress is **not** restricted — `(allow default)`
  covers the network and there is no deny for it (`macos_user.py:341`). The
  agent can reach the internet, localhost, and LAN. If your threat model
  includes exfiltration, this backend does not stop it.
- **See other processes** (`(allow process-info*)`, `sysctl-read`) — needed by
  normal tooling; means it can enumerate what's running.
- **Consume unbounded CPU/RAM/PIDs.** There is **no resource limit** — no
  cgroup analog, nothing in the code caps it (`grep rlimit/taskpolicy` →
  none). A runaway agent can peg your machine.

The honest framing from the design doc holds: this protects against *"don't
let a YOLO-mode agent wreck my host or read my creds,"* **not** against
*adversarial code trying to escape or exfiltrate.*

## 5. How strong is the guarantee, really

Ranked strongest → weakest:

1. **Writes staying inside the workspace** — *strong.* Deny-all-writes +
   narrow re-allow, enforced by the kernel sandbox. To escape it needs a
   Seatbelt bypass or a kernel bug.
2. **Keychain secrets** — *strong.* Protected by UID crypto (Wall 1)
   *and* the file deny (Wall 2). Two independent mechanisms.
3. **Your home's private files (`~/.ssh`, `~/.aws`, `~/.gitconfig`)** —
   *good, but single-walled today.* See [§6](#6-the-gap-between-this-doc-and-the-design-doc):
   only Wall 2 (the profile deny on `/Users`) protects these; there is no
   POSIX belt-and-suspenders (`chmod 750 ~`). One correct rule protects them,
   not two. Note the neutral-workspace model makes this *cleaner* than before
   — the workspace is out of your home and no grant is threaded through it —
   but the second layer still isn't there.
4. **Reading the rest of the system** — *not protected, by design.* It's an
   `(allow default)` read base.
5. **Network egress** — *not protected, by design.*
6. **Resource exhaustion** — *not protected.*
7. **Kernel-level escape** — *this is the ceiling.* Everything runs on the
   host kernel (XNU). A kernel LPE escapes the sandbox entirely. The container
   backend interposes a VM/hypervisor here; this backend does not. This is the
   structural reason it's "weaker than the container," full stop.

And one durability caveat: **`sandbox-exec` is deprecated** (since macOS 10.12,
prints a warning every run, SBPL format officially undocumented). It's still
used by Chrome, Bazel, Swift PM, Codex, and Anthropic's own runtime, so it
isn't going away tomorrow — but it's a dependency Apple has disavowed. If it
vanished, Wall 1 (the separate user) would remain a real, if lesser, boundary.

## 6. The gap between this doc and the design doc

The design doc ([macos-native-user-sandbox-design.md](macos-native-user-sandbox-design.md))
specifies the credential boundary as **two layers**: (a) the Seatbelt
`file-read*` deny on `/Users`, **and** (b) `chmod 750` on your host home so a
different UID can't traverse in via POSIX either.

**The code today only does (a).** `run_macos_user` (`macos_user.py`) installs
the profile, applies the workspace ACL, stages the entrypoint, and launches —
it never `chmod`s your host home (grep the orchestrator: no such call). So:

- Your `~/.ssh` is protected **because the Seatbelt profile denies it**, and
  only that. It is not *also* protected by filesystem permissions on the home
  directory itself.
- If the Seatbelt profile were bypassed or disabled, POSIX would **not** catch
  the read the way the design intends — because your home is likely still
  `0755` and `_yolojail` could traverse it.

This is a real single-point-of-failure that the design called out as needing
two layers. It is **not** an argument to panic (the profile deny is a genuine
kernel-enforced control), but you should know the second layer isn't there
yet. Tracked in [§8](#8-open-questions).

Why the code doesn't `chmod ~` today: doing it silently mutates the operator's
home permissions, which — like the sudo-policy question we already settled — is
arguably the user's call, not something the tool should do unannounced. (The
neutral-workspace model deliberately made the home *irrelevant* to sharing, so
this is now purely about the last-ditch read barrier, not about reachability.)

## 7. How to verify any of this yourself (from inside the sandbox)

Don't trust the doc — check it. Launch `yolo` on a `macos-user` workspace, then:

```sh
# You are the sandbox user, not you:
whoami                      # -> _yolojail
id                          # different uid/gid
pwd                         # -> /Users/Shared/yolo/<project> (neutral ground)

# Wall in action — these MUST fail:
cat ~matt/.ssh/id_ed25519   # permission denied (/Users read-denied)
ls /Users/matt              # permission denied — your home is unreachable
cat /Library/Keychains/System.keychain   # denied (world-readable file, but sandbox-denied)
echo x > /usr/local/should-not-write      # denied (writes are workspace-only)

# These SHOULD work (by design):
touch ./scratch-file        # the shared workspace is writable
curl -sI https://example.com | head -1    # network is open
cat /usr/bin/sw_vers >/dev/null            # system reads are open
```

Then confirm the **share is live** from the host side (as *you*, in another
terminal): edit `./scratch-file` in your editor and watch the agent see the
change instantly, and vice-versa — same inodes, no copy. When done,
`yolo macos-unshare /Users/Shared/yolo/<project>` strips the ACLs back to
plain POSIX.

To watch the kernel enforce it live, in another terminal on the host:
`log stream --predicate 'sender=="Sandbox"'` — you'll see the denials as they
happen. If a "MUST fail" line *succeeds*, that's a real finding worth a bug.

## 8. Open questions

1. **Should the run path `chmod 750` the host home** to add the POSIX second
   wall the design specified (§6)? Options: do it (mutates the operator's
   home, needs consent, like the sudo decision), warn-and-skip (current, but
   undocumented), or make it an explicit opt-in flag. Until resolved, the
   credential boundary is single-walled. (Lower priority now that the
   workspace itself lives outside the home and no grant is threaded through
   it — this is only the last-ditch read barrier.)
2. **Egress**: leave network open (matches SandVault, current) or add an
   opt-in localhost-proxy / deny for exfil-sensitive work? A design doc +
   approval gate, per the SandVault-parity rule.
3. **Resource caps**: accept "no limits," or bolt on `taskpolicy`/`setrlimit`
   / a memory watchdog for runaway agents? No cgroup analog exists on macOS.
4. **`sandbox-exec` longevity**: pre-invest in an Endpoint Security fallback,
   or accept the risk with a documented "fall back to user-account-only"
   posture if Apple removes it?
5. **Straddling layouts**: a monorepo/symlink setup that references paths
   outside the shared root won't work. Accept "keep related trees under the
   shared root," or add a way to share multiple neutral trees to one session?

## References

- Implementation: `src/cli/macos_user.py` — `seatbelt_profile` (the profile),
  `launch_argv` (the launch), `create_user_commands` (the account),
  `home_containing` (the non-home enforcement), `workspace_acl_apply_script` /
  `workspace_acl_strip_script` (the flat workspace share + clean teardown),
  `shared_root_provision_commands` (the neutral root), `run_macos_user` (the
  orchestrator).
- Rationale + honest delta vs. container:
  [macos-native-user-sandbox-design.md](macos-native-user-sandbox-design.md),
  especially "The honest verdict, up front."
- Where it sits among options: [platform-comparison.md](platform-comparison.md),
  [happy-path-principle.md](happy-path-principle.md).
- Prior art we match: [SandVault](https://github.com/webcoyote/sandvault).
