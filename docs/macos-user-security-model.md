# macOS-user sandbox: the complete security model

**Audience:** anyone reasoning about what the `runtime: "macos-user"` backend
does and does not protect — to build a correct mental model, not a hopeful one.
No prior knowledge of the project assumed; §3 shows the actual sandbox config
verbatim.
**Status:** describes the code as it runs today (`src/cli/macos_user.py`).
One spot is single-layer rather than two (world-readable files in a `0755`
home — see [§7](#7-the-one-single-layer-spot-world-readable-home-files)).
**Companion:** [Running agents natively on macOS](macos-user-mode.md) — the
*how do I use it* doc. This is the *what exactly does it protect* doc.

This is the doc to argue with. If a claim here doesn't match what you can
actually do from inside the sandbox, that's a bug in the code or the doc —
tell me which.

**The 30-second model.** The agent runs as a **separate, hidden macOS user**
(`_yolojail`), inside an **Apple Seatbelt profile**, sharing exactly **one
directory** with you — on neutral ground outside everyone's home. Two
independent walls (a different UID *and* a kernel sandbox profile) mean it can
read most of the OS but **cannot write anything except that one shared folder**
and **cannot read your home, your credentials, the keychain, or other users**.
It is a *credible* boundary, deliberately *weaker than a container* (shared
kernel, no resource caps, network open) — right for a trusted-but-autonomous
agent, not for genuinely hostile code.

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

## 1. Why `cd ..` "succeeds" but you still can't see anything

This is the most useful thing to understand, and it's subtler than a plain
"cd .. fails" — here's exactly what you'll observe:

```console
_yolojail@host yolo_test % cd ..
_yolojail@host yolo_test % ls
ls: .: Operation not permitted          # ← the denial surfaces HERE, on read
_yolojail@host yolo_test % pwd
/Users/Shared/yolo/yolo_test            # ← stale: still the workspace, not the parent
```

`cd ..` returns **no error**, `ls` then fails, and `pwd` and the prompt still
show the workspace. That's not a bug — it's the shape of the boundary. Why:

The agent is launched **cd'd into your workspace** — the same path you ran
`yolo` from. The launch ends with `zsh -c "cd <workspace> && exec <agent>"`
(`macos_user.py`, `launch_argv`); there's no `/workspace` remapping, you're at
the real path, on **neutral ground** (`/Users/Shared/yolo/<project>` by
default; the run path refuses a workspace inside a home — see [§2](#2-where-work-lives--neutral-ground-not-your-home)).

But you are **not you** in that shell — you're `_yolojail`, a different UID —
and the Seatbelt profile (`macos_user.py`, `seatbelt_profile`) denies reads
under `/Users`, re-allowing only four things:

```lisp
(deny file-read* (subpath "/Users"))          ; everything under /Users: no read
(allow file-read*
    (literal "/Users")                          ; the bare entry, for traversal only
    (literal "/Users/Shared")                   ; the bare entry, for traversal only
    (subpath "<your workspace>")                ; your project: full read
    (subpath "/Users/_yolojail"))               ; the sandbox's own home
```

Note what is **not** in that list: the workspace's *parent*,
`/Users/Shared/yolo`. So there are two different permissions in play, and this
is the whole trick:

- **Traverse (chdir/`cd`)** needs only *search* on a directory. `cd ..` into
  `/Users/Shared/yolo` works because it's group-traversable (mode `2770`,
  `_yolojail` is in the group) — hence **no error**.
- **List/read (`ls`, globbing, reading files)** needs *read-data*, which the
  profile denies on `/Users/Shared/yolo`. Hence `ls: Operation not permitted`.
- **`pwd` goes stale** because zsh's `getcwd()` reconstructs the path by
  reading directory entries *upward* — and it just hit a dir it can't read, so
  it falls back to the cached `$PWD` (still the workspace). You really did move
  up; the shell simply can't confirm where to. Same reason the prompt segment
  doesn't update.

So the honest one-liner isn't "cd .. errors" — it's: **you can step out of
your workspace, but the moment you try to *see* anything out there, the kernel
says no, and even your shell loses track of where it is.** The room next door
isn't just locked — it's unlit and unmarked. That's the boundary working.

There are **no per-ancestor grants**: because the workspace sits under
`/Users/Shared` (neutral ground), the two `literal` entries are the only
traversal the sandbox needs — nothing is opened up inside your home. That
"thread a grant through `~`" machinery is gone (see §2).

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

**How the one shared directory is shared** (all on that tree, never your home)
— and the key point, **it's a one-time setup cost, not a per-run one**:
- `macos-setup` provisions the root once: `mkdir -p` + `chown you:_yolojail`
  + `chmod 2770` (**setgid**, group-rwx, no "other" access) + the **inheriting
  ACL ACEs** (`chmod +a` dir-rights + file-inherit template) on the root
  itself (`shared_root_provision_commands`).
- Because macOS applies inheritable ACEs to everything created under an
  ACL'd directory *at create-time*, **every project and file you or the agent
  later create under the root inherits the shared-group grant automatically**
  — same inodes, both UIDs read+write, no per-run walk. macOS ACLs grant
  independent of the umask, so this survives `git checkout`/`tar` writing
  restrictive modes. The launch path does **zero** ACL work.
- The one exception inheritance misses: **pre-existing files moved or
  preserve-copied in** (`mv ~/old-proj …`, `cp -p`) keep their original ACL
  and don't re-trigger inheritance, so they lack the grant. Fix on demand
  with `yolo macos-fix-permissions` (`fix_permissions_script` — batched, off
  the hot path); you rarely need it.
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

## 3. The sandbox config, verbatim

Everything below is exactly what the code generates — no summary, no
paraphrase. This is the whole surface; if it's not here, the agent doesn't get
it.

**(a) The account** (`yolo macos-setup`, once). A hidden, unprivileged user +
a shared group, both you and it are members of:

```
dscl . -create /Users/_yolojail IsHidden 1          # off the login window
dseditgroup -o edit -d _yolojail -t user staff      # NOT a normal login user
dseditgroup -o edit -a _yolojail -t user _yolojail  # sandbox user in the group
dseditgroup -o edit -a <you>     -t user _yolojail  # you in the group too
chmod 750 /Users/_yolojail                           # its own home
# + a random password never passed on a command line (never visible in `ps`)
```

**(b) The shared root** (`yolo macos-setup`, once). The neutral ground:

```
mkdir -p /Users/Shared/yolo
chown <you>:_yolojail /Users/Shared/yolo
chmod 2770 /Users/Shared/yolo    # setgid + owner/group rwx + NO access for "other"
```

**(c) The Seatbelt profile** (per run, root-owned `0444` so the agent can't
edit its own sandbox). For a workspace at `/Users/Shared/yolo/my-app`:

```lisp
(version 1)
(allow default)                                   ; permissive base...

;; ...then deny all writes, re-allow only the agent's writable set:
(deny file-write* (subpath "/"))
(allow file-write*
    (subpath "/Users/Shared/yolo/my-app")         ; the workspace
    (subpath "/Users/_yolojail")                   ; the sandbox's own home
    (subpath "/tmp") (subpath "/private/tmp")
    (subpath "/var/folders") (subpath "/private/var/folders")
    (subpath "/dev"))

;; other volumes: no read (except the boot volume)
(deny file-read* (subpath "/Volumes"))
(allow file-read* (subpath "/Volumes/Macintosh HD"))

;; raw disk + packet capture: never (would bypass file perms / sniff traffic)
(deny file-read* file-write*
    (regex #"^/dev/r?disk") (regex #"^/private/dev/r?disk") (regex #"^/dev/bpf"))

;; everyone's homes: no read, re-allow only traversal entries + the
;; (neutral) workspace + the sandbox's own home. Your home is NOT re-allowed.
(deny file-read* (subpath "/Users"))
(allow file-read*
    (literal "/Users") (literal "/Users/Shared")
    (subpath "/Users/Shared/yolo/my-app")
    (subpath "/Users/_yolojail"))

;; the machine keychain file: world-readable 0644, so this deny is load-bearing
(deny file-read* (subpath "/Library/Keychains"))

(allow process-info*)                              ; tooling needs to see procs
(allow sysctl-read)
```

**(d) The launch** (per run). Run as the sandbox user, with a scrubbed
environment, under the profile:

```
sudo --login --user=_yolojail \
  /usr/bin/env -i \                                # empty env — nothing inherited
    HOME=/Users/_yolojail USER=_yolojail SHELL=/bin/zsh \
    PATH=/Users/_yolojail/.yolo-shims:...:/usr/bin:/bin \
    TERM=... \                                      # + git identity, no host secrets
  /usr/bin/sandbox-exec -f /var/yolo-jail/profile-<session>.sb \
  -- /bin/zsh -c "cd /Users/Shared/yolo/my-app && exec claude ..."
```

`env -i` is load-bearing: without it, `HOME` would still point at *your* home
and the agent would read your `~/.gitconfig`/`~/.ssh`. The identity vars
(`HOME`/`USER`/`SHELL`/`PATH`) are fixed and not caller-overridable.

The rest of this doc explains *why* each line is there and how strongly it
holds.

## 4. The two walls (this is the whole model)

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
what matters (the full text is in [§3c](#3-the-sandbox-config-verbatim)).
Last-match-wins, so each deny is followed by narrower re-allows.

The denies that are the actual boundary:

| Rule (`macos_user.py`) | Effect | Why it's load-bearing |
|---|---|---|
| `(deny file-write* (subpath "/"))` then re-allow workspace + sandbox home + `/tmp` + `/var/folders` + `/dev` | **Writes: nothing except your workspace and scratch** | This is the big one — the agent cannot modify your host, /usr, /etc, /Applications, other projects. |
| `(deny file-read* (subpath "/Users"))` + re-allows | Other users' homes **and your own home** are unreadable | The credential wall. Most dev secrets are already `0600`/`0700` (`~/.ssh`, `~/.aws/credentials`) so the UID switch alone covers them — but the load-bearing case is **world-readable (`0644`) files** a differently-configured home might expose: `~/.npmrc` (npm writes it `0644`, and it can hold an auth token), `~/.gitconfig`, misc app configs. This deny makes the boundary hold **regardless of any user's home permissions** (which vary by macOS version and MDM), so it's not just belt-and-suspenders. |
| `(deny file-read* (subpath "/Library/Keychains"))` | The **machine** keychain file is unreadable | `System.keychain` (Wi-Fi passwords, 802.1X/VPN creds, machine certs — distinct from your UID-gated *login* keychain) is a world-readable `0644` file, so the UID switch alone doesn't cover the file. The secret material inside is additionally Security-framework-encrypted, but denying the read is correct defense-in-depth. |
| `(deny file-read* (subpath "/Volumes"))` + re-allow boot volume | External/other volumes unreadable | Stops reading mounted disks, backups, other APFS volumes. |
| `(deny … (regex #"^/dev/r?disk") … #"^/dev/bpf")` | Raw disk devices + packet capture denied | Blocks reading the raw filesystem (bypassing perms) and sniffing network traffic. |

## 5. What the agent CAN do (be honest about the blast radius)

Not a jail in the "nothing gets out" sense. Inside the sandbox the agent can:

- **Read most of the system**: `/usr`, `/bin`, `/Library` (except Keychains),
  `/Applications`, system frameworks, Homebrew, the boot volume generally.
  The base is `(allow default)` for reads — we *deny-list* secrets, we don't
  *allow-list* a minimal set. So "can it read X?" → yes unless X is in the
  deny table above.
- **Write to your workspace** (that's the point) and to `/tmp`,
  `/var/folders`, `/dev`.
- **Full network access.** Egress is **not** restricted — `(allow default)`
  covers the network and there is no deny for it (see the profile in §3c —
  no network rule). The agent can reach the internet, localhost, and LAN. If
  your threat model includes exfiltration, this backend does not stop it.
- **See other processes** (`(allow process-info*)`, `sysctl-read`) — needed by
  normal tooling; means it can enumerate what's running.
- **Consume unbounded CPU/RAM/PIDs.** There is **no resource limit** — no
  cgroup analog, nothing in the code caps it (`grep rlimit/taskpolicy` →
  none). A runaway agent can peg your machine.

The honest framing from the design doc holds: this protects against *"don't
let a YOLO-mode agent wreck my host or read my creds,"* **not** against
*adversarial code trying to escape or exfiltrate.*

## 6. How strong is the guarantee, really

Ranked strongest → weakest:

1. **Writes staying inside the workspace** — *strong.* Deny-all-writes +
   narrow re-allow, enforced by the kernel sandbox. To escape it needs a
   Seatbelt bypass or a kernel bug.
2. **Login-keychain secrets** — *strong.* Your per-user login keychain is
   cryptographically gated to your UID — a different UID can't unlock it,
   Seatbelt or not.
3. **Your `0600`/`0700` home secrets (`~/.ssh`, `~/.aws/credentials`)** —
   *strong.* SSH keys are `0600` (ssh refuses looser), the AWS CLI writes
   credentials `0600`, `~/.ssh` is `0700`. The UID switch alone blocks these;
   Seatbelt is a redundant second layer for them.
4. **Your world-readable (`0644`) home files (`~/.npmrc` token, `~/.gitconfig`,
   sloppy app configs)** — *held by Seatbelt (Wall 2) regardless of home
   perms.* The UID switch alone would NOT stop these if a given user's home is
   `0755` (home mode varies by macOS version + MDM), so the `/Users` read-deny
   is genuinely load-bearing here, not belt-and-suspenders. It gives the same
   guarantee for every user without the tool having to inspect or change
   anyone's home permissions.
5. **Reading the rest of the system** — *not protected, by design.* It's an
   `(allow default)` read base.
6. **Network egress** — *not protected, by design.*
7. **Resource exhaustion** — *not protected.*
8. **Kernel-level escape** — *this is the ceiling.* Everything runs on the
   host kernel (XNU). A kernel LPE escapes the sandbox entirely. The container
   backend interposes a VM/hypervisor here; this backend does not. This is the
   structural reason it's "weaker than the container," full stop.

And one durability caveat: **`sandbox-exec` is deprecated** (since macOS 10.12,
prints a warning every run, SBPL format officially undocumented). It's still
used by Chrome, Bazel, Swift PM, Codex, and Anthropic's own runtime, so it
isn't going away tomorrow — but it's a dependency Apple has disavowed. If it
vanished, Wall 1 (the separate user) would remain a real, if lesser, boundary.

## 7. The one single-layer spot: world-readable home files

For most of your home (the `0600`/`0700` secrets), the boundary is two
independent layers — the UID switch AND the Seatbelt `/Users` deny. For
**world-readable (`0644`) files in a `0755` home** (a `~/.npmrc` token, say),
it's **one** layer: only the Seatbelt `/Users` read-deny stops the read. If a
user's home is `0700` (macOS default has trended that way, but MDM/older
setups vary), the UID switch covers even those — but we don't depend on it.

We deliberately do **not** `chmod` your home to add a POSIX second layer.
Doing so silently mutates the operator's home permissions — the user's call,
not the tool's (same principle as not touching the sudo policy). The
neutral-workspace model makes the home *irrelevant to sharing* — no grant is
ever threaded through it — so there is no functional reason to touch it.

**This matches SandVault.** SandVault also relies on the user switch + the
Seatbelt `/Users` deny and does **not** `chmod` your home either (verified in
its source — it only touches its own shared dir and the sandbox user's home).
So this is not a gap versus the prior art; it's the same posture. (An earlier
version of this doc claimed SandVault does `chmod 750 ~` and that we had a
"single-walled gap" against it — that was wrong; corrected here.)

The residual risk is narrow and honest: a bypass/removal of Seatbelt would
expose `0644` files in a `0755` home. A `chmod 750 ~` opt-in flag could close
that at the POSIX layer for users who want it, without imposing it — tracked
in [§9](#9-open-questions).

## 8. How to verify any of this yourself (from inside the sandbox)

Don't trust the doc — check it. Launch `yolo` on a `macos-user` workspace, then:

```sh
# You are the sandbox user, not you:
whoami                      # -> _yolojail
id                          # different uid/gid
pwd                         # -> /Users/Shared/yolo/<project> (neutral ground)

# Wall in action — these MUST fail (all "Operation not permitted"):
cat ~matt/.ssh/id_ed25519   # your home is read-denied
ls /Users/matt              # your home is unreachable
ls /Users/Shared/yolo       # even the workspace's PARENT can't be listed
cat /Library/Keychains/System.keychain   # world-readable file, but sandbox-denied
echo x > /usr/local/should-not-write      # writes are workspace-only

# The subtle one — traverse-but-not-read (see §1): `cd` works, `ls` doesn't.
cd .. ; ls                  # cd: ok (silent) ; ls: Operation not permitted
pwd                         # stale — still shows the workspace, not the parent
cd -                        # back into the workspace

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

## 9. Open questions

1. **Offer an opt-in `chmod 750 ~`** as a POSIX second layer for the
   world-readable-home-file case (§7)? Strictly opt-in — never imposed, since
   it mutates the operator's home. Not a "gap vs SandVault" (SandVault doesn't
   do it either); purely a hardening knob for users who want belt-and-braces.
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
  `home_containing` (the non-home enforcement), `shared_root_provision_commands`
  (the neutral root + the inheriting ACL that shares everything under it),
  `fix_permissions_script` (the on-demand retrofit for moved-in files) /
  `workspace_acl_strip_script` (clean teardown), `run_macos_user` (the
  orchestrator).
- Rationale + honest delta vs. container:
  [macos-native-user-sandbox-design.md](macos-native-user-sandbox-design.md),
  especially "The honest verdict, up front."
- Where it sits among options: [platform-comparison.md](platform-comparison.md),
  [happy-path-principle.md](happy-path-principle.md).
- Prior art we match: [SandVault](https://github.com/webcoyote/sandvault).
