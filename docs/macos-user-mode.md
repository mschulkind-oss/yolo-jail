# Running agents natively on macOS: `macos-user` mode

**What this is:** a way to run a coding agent (Claude Code, Codex, etc.) in a
real sandbox on macOS **without a container or a VM** — as a dedicated,
locked-down macOS user wrapped in Apple's built-in sandbox. Native arm64
speed, no Linux VM, no Docker/Podman machine.

**Status:** experimental. It runs, but it hasn't been hardened across every
macOS version and workflow yet. The container backend remains the default and
the security-maximum option; `macos-user` is explicit opt-in.

> **Two docs, one topic.** This is the *how do I use it* doc. Its companion,
> [macOS-user security model](macos-user-security-model.md), is the *what
> exactly does it protect* doc — read that before trusting it with anything.

## Why you'd want it

The container backend (Podman / Apple Container) is a true VM/kernel boundary
and stays the default. But on macOS a container means a Linux VM: slower
startup, a memory budget to manage, x86-vs-arm friction for some tools.

`macos-user` mode trades some of that isolation strength for **native speed
and simplicity**: the agent runs as arm64-native macOS processes, starting in
seconds, using the tools already on your Mac. It's the right choice when you
want *"let a YOLO-mode agent work autonomously without letting it wreck my Mac
or read my credentials"* — a **trusted-but-autonomous** agent — rather than
sandboxing genuinely adversarial code (use the container for that).

| | `macos-user` | container (default) |
|---|---|---|
| Isolation | separate macOS user + Apple sandbox | full VM / kernel boundary |
| Speed | native, instant | Linux VM overhead |
| Reads your `~`/creds | **no** | no |
| Writes outside the workspace | **no** | no |
| Resource caps (CPU/RAM) | no | yes |
| Network egress control | no | no (both allow egress) |
| Best for | trusted-but-autonomous agents | untrusted/adversarial code |

The full honest comparison is in the [security model](macos-user-security-model.md).

## One-time setup

```console
$ yolo macos-setup
```

This creates a hidden, unprivileged macOS user (`_yolojail`) that the agent
runs as, and a shared directory where your projects live. It needs admin
rights, so **sudo will prompt for your password** — that's expected (we don't
change your system's sudo policy). It's idempotent; re-run it any time.

You'll also want a real `python3` (Homebrew or Xcode Command Line Tools) —
`macos-setup` tells you if it can't find one.

## Where your projects live: `/Users/Shared/yolo/`

**This is the one thing that's different from every other backend, so it's
worth understanding up front.**

The agent and you share your project **live** — same files, real-time edits,
no copying. But for that sharing to be safe, the project must sit on **neutral
ground: a directory outside everyone's home**. The default is:

```
/Users/Shared/yolo/<your-project>
```

So instead of working in `~/code/my-app`, you keep the project at
`/Users/Shared/yolo/my-app`. Move an existing one there once:

```console
$ mv ~/code/my-app /Users/Shared/yolo/my-app
$ cd /Users/Shared/yolo/my-app
```

**Why not just my project where it already is?** Sharing a folder that lives
*inside your home* would mean poking an access hole through your home
directory — exactly the kind of subtle, error-prone permission plumbing that
leaks an SSH key by accident. A neutral, clearly-named shared directory
removes the whole problem: nothing your home contains is ever exposed, and
"what can the agent reach?" has a one-sentence answer. (Details:
[security model](macos-user-security-model.md).)

If you prefer a different location, set `macos_shared_root` in
`yolo-jail.jsonc` to any absolute **non-home** path (e.g. `/opt/yolo`, an
external volume). A path inside a home is rejected with a clear error.

**The one real constraint:** a project layout that reaches *outside* the
shared root — a monorepo that expects a sibling repo back in `~`, or a symlink
into your home — won't work. Keep related trees together under the shared
root.

## Running

From your project directory under the shared root:

```console
$ cd /Users/Shared/yolo/my-app
$ cat yolo-jail.jsonc
{ "runtime": "macos-user", "agents": ["claude"] }

$ yolo                 # launches the agent in the sandbox
```

(Or `YOLO_RUNTIME=macos-user yolo` without editing config.)

To see exactly what will happen before it happens — the full plan, no
execution:

```console
$ yolo run --dry-run   # prints the sandbox profile, the launch, everything
```

## What you'll notice inside

- **You're a different user.** The prompt shows `_yolojail`, not you. That's
  the sandbox — the agent literally runs as a separate macOS account.
- **You can't wander out of the project.** `cd ..` appears to work but then
  `ls` says `Operation not permitted`, and `pwd` looks stale. That's the
  boundary doing its job — you can step out of the workspace but can't *read*
  anything out there. (The exact mechanics, and why `pwd` goes stale, are in
  the [security model](macos-user-security-model.md#1-why-cd--succeeds-but-you-still-cant-see-anything).)
- **Your edits are live on both sides.** Change a file in the sandbox and it's
  instantly there for you on the host, and vice-versa. Same files, no sync.
- **Your credentials aren't reachable.** The agent cannot read your `~/.ssh`,
  cloud tokens, keychain, or other users' files — by design.

## Cleaning up

```console
# Return a shared project to plain permissions (removes the sandbox's access):
$ yolo macos-unshare /Users/Shared/yolo/my-app

# Remove the sandbox user + group entirely (your files are untouched):
$ yolo macos-teardown
```

## Troubleshooting

**The agent can't write (or read) some files I moved in.** Files *created*
under `/Users/Shared/yolo/` are shared automatically. But files you **moved or
copied-with-`-p`** in from elsewhere (e.g. `mv ~/old-proj /Users/Shared/yolo/`)
keep their original permissions and don't pick up the share. One command fixes
it:

```console
$ yolo macos-fix-permissions                       # the whole shared root
$ yolo macos-fix-permissions /Users/Shared/yolo/my-app   # or one project
```

You rarely need this — only after bringing *pre-existing* files in. (This is
why starting a project *in* the shared root, or letting the agent create it,
just works.)

## Honest limits (read before you rely on it)

`macos-user` is a **credible** boundary but **weaker than the container**, and
you should know where:

- **Shared kernel.** Everything runs on your Mac's kernel. A kernel-level
  exploit escapes the sandbox; the container interposes a VM there.
- **Network is open.** The agent has full internet/LAN access. This mode does
  **not** stop data exfiltration — use the container for exfil-sensitive work.
- **No resource caps.** No CPU/RAM/PID limits; a runaway agent can peg your
  machine.
- **Built on a deprecated Apple tool.** `sandbox-exec` still works everywhere
  and is used by Chrome, Bazel, and Anthropic's own runtime, but Apple has
  formally deprecated it.

If your threat model is "sandbox genuinely hostile code," use the container
backend. If it's "let a trusted agent run wild without trashing my Mac or
reading my secrets," this fits. The full accounting — every allow and deny,
ranked by how strongly it holds — is in the
[security model](macos-user-security-model.md).

## See also

- [macOS-user security model](macos-user-security-model.md) — the complete
  mental model: the actual sandbox config and exactly what it does and doesn't
  protect.
- [macOS setup](macos.md) — general macOS installation (all backends).
- [Platform comparison](platform-comparison.md) — the full feature matrix.
