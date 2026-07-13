# How the macOS-user sandbox actually contains an agent

**Audience:** anyone reasoning about what the `runtime: "macos-user"` backend
does and does not protect — to build a correct mental model, not a hopeful one.
**Status:** describes the code as it runs today (`src/cli/macos_user.py`),
which differs from the original proposal in one load-bearing way (see
[§5](#5-the-gap-between-this-doc-and-the-design-doc)).
**Reads with:** [macos-native-user-sandbox-design.md](macos-native-user-sandbox-design.md)
(the *why* + the honest security delta vs. the container).

This is the doc to argue with. If a claim here doesn't match what you can
actually do from inside the sandbox, that's a bug in the code or the doc —
tell me which.

## 0. The one-sentence version

An agent under `macos-user` runs as a **different real macOS user**
(`_yolojail`) wrapped in an **Apple Seatbelt profile**, so it can read most of
the system but can **write almost nothing except your workspace**, and
**cannot read the things that matter** — your home's private files, other
users, the keychain files, raw disk. It is a *credible* boundary, *weaker than
the Linux container* (shared kernel, no resource caps), and it depends on a
deprecated-but-ubiquitous Apple tool.

## 1. Why you're in the same directory but `cd ..` fails

This is the most useful thing to understand, because it's exactly what you saw.

The agent is launched **cd'd into your workspace** — the same path you ran
`yolo` from — on purpose. The launch command ends with
`zsh -c "cd <workspace> && exec <agent>"` (`macos_user.py:482`,
`launch_argv`). We match the container backend's "you're in your project"
feel; there's no `/workspace` remapping, you're at the real path.

But you are **not you** in that shell. You're `_yolojail`, a different UID. And
the Seatbelt profile says (`macos_user.py:397`):

```lisp
(deny file-read* (subpath "/Users"))          ; everything under /Users: no read
(allow file-read*
    (literal "/Users")                          ; the bare directory entry only
    (literal "/Users/Shared")
    (subpath "<your workspace>")                ; your project: full read
    (subpath "/Users/_yolojail"))               ; the sandbox's own home
```

So `cd ..` (from `/Users/matt/yolo_test` up to `/Users/matt`) hits the deny:
`/Users/matt` is under `/Users`, and it is **not** one of the re-allowed paths.
The kernel refuses the directory read/traversal → **permission error**. Your
workspace is a lit room; one step up is a locked door. That error is the
sandbox working, not something misconfigured.

(There is a narrow exception so a workspace *nested* under your home is even
reachable: each ancestor between `/Users` and the workspace gets a
**metadata-only** allow — `stat` the directory to resolve the path, but not
list or read its contents. `macos_user.py:354`, the `file-read-metadata`
block. That's why you can be *inside* `/Users/matt/yolo_test` even though you
can't read `/Users/matt`.)

## 2. The two walls (this is the whole model)

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

## 3. What the agent CAN do (be honest about the blast radius)

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

## 4. How strong is the guarantee, really

Ranked strongest → weakest:

1. **Writes staying inside the workspace** — *strong.* Deny-all-writes +
   narrow re-allow, enforced by the kernel sandbox. To escape it needs a
   Seatbelt bypass or a kernel bug.
2. **Keychain secrets** — *strong.* Protected by UID crypto (Wall 1)
   *and* the file deny (Wall 2). Two independent mechanisms.
3. **Your home's private files (`~/.ssh`, `~/.aws`, `~/.gitconfig`)** —
   *good, but single-walled today.* See [§5](#5-the-gap-between-this-doc-and-the-design-doc):
   right now only Wall 2 (the profile deny on `/Users`) protects these; the
   POSIX belt-and-suspenders (`chmod 750 ~`) is **not** applied by the code.
   One correct rule is protecting them, not two.
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

## 5. The gap between this doc and the design doc

The design doc ([macos-native-user-sandbox-design.md:60-63,104-111](macos-native-user-sandbox-design.md))
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
yet. Tracked in [§7](#7-open-questions).

Why the code doesn't `chmod ~` today: doing it silently mutates the operator's
home permissions, which — like the sudo-policy question we already settled — is
arguably the user's call, not something the tool should do unannounced. That
tension is unresolved (see Open questions), which is *why* it's currently
one-walled rather than deliberately so.

## 6. How to verify any of this yourself (from inside the sandbox)

Don't trust the doc — check it. Launch `yolo` on a `macos-user` workspace, then:

```sh
# You are the sandbox user, not you:
whoami                      # -> _yolojail
id                          # different uid/gid

# Wall in action — these MUST fail:
cat ~matt/.ssh/id_ed25519   # permission denied (/Users read-denied)
cd .. && ls                 # permission denied one level up
cat /Library/Keychains/System.keychain   # denied (world-readable file, but sandbox-denied)
echo x > /usr/local/should-not-write      # denied (writes are workspace-only)

# These SHOULD work (by design):
touch ./scratch-file        # workspace is writable
curl -sI https://example.com | head -1    # network is open
cat /usr/bin/sw_vers >/dev/null            # system reads are open
```

To watch the kernel enforce it live, in another terminal on the host:
`log stream --predicate 'sender=="Sandbox"'` — you'll see the denials as they
happen. If a "MUST fail" line *succeeds*, that's a real finding worth a bug.

## 7. Open questions

1. **Should the run path `chmod 750` the host home** to add the POSIX second
   wall the design specified (§5)? Options: do it (mutates the operator's
   home, needs consent, like the sudo decision), warn-and-skip (current, but
   undocumented), or make it an explicit opt-in flag. Until resolved, the
   credential boundary is single-walled.
2. **Egress**: leave network open (matches SandVault, current) or add an
   opt-in localhost-proxy / deny for exfil-sensitive work? A design doc +
   approval gate, per the SandVault-parity rule.
3. **Resource caps**: accept "no limits," or bolt on `taskpolicy`/`setrlimit`
   / a memory watchdog for runaway agents? No cgroup analog exists on macOS.
4. **`sandbox-exec` longevity**: pre-invest in an Endpoint Security fallback,
   or accept the risk with a documented "fall back to user-account-only"
   posture if Apple removes it?

## References

- Implementation: `src/cli/macos_user.py` — `seatbelt_profile` (the profile),
  `launch_argv` (the launch), `create_user_commands` (the account),
  `workspace_acl_apply_script` (the workspace share + ancestor traversal),
  `run_macos_user` (the orchestrator).
- Rationale + honest delta vs. container:
  [macos-native-user-sandbox-design.md](macos-native-user-sandbox-design.md),
  especially "The honest verdict, up front."
- Where it sits among options: [platform-comparison.md](platform-comparison.md),
  [happy-path-principle.md](happy-path-principle.md).
- Prior art we match: [SandVault](https://github.com/webcoyote/sandvault).
