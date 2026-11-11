# guardrails

Tools a launch refuses in favour of a faster one for the same work: in a jail, and in the program
`yolo host --` starts, whose PATH yolo composes, so the same shims go first on it
([HE-D11](../../docs/reference/host-agent-environment.md#he-d11)). `yolo host apply` starts nothing
and blocks nothing.

Not shipped into the jail — a README at a pack root is not a briefing source (that
is a `briefing/` directory, which this pack does not have), and nothing here needs to
reach an agent up front. The blocker
prints its own message and its own alternative at the moment it refuses, which is
where that information belongs. This file is for whoever edits these rules.

## Workspace-state guards

`mv`, `rm` and `git clean` run normally unless they can remove this launch's workspace
state. The workspace's `.yolo`, its parents and the durable directory itself are protected;
your own files below `.yolo/durable` remain yours to remove. This is an opt-in PATH speed
bump, not a security boundary. `YOLO_BYPASS_SHIMS=1` bypasses it, and a user blocked-tool
entry of the same name replaces the pack entry whole.

For Git, other commands and clean dry-runs pass unchanged. The guard respects global
options such as chained `-C`, `--git-dir`, `--work-tree` and `-c`. Use `git clean -n` to
inspect first, then clean an explicit ordinary path, for example `git clean -fdx -- build`.
Broad destructive cleans that reach protected state are refused, including `-fdx`, `-fdX`
and interactive removal. Wildcard, escaped, magic and case-insensitive pathspecs are
conservatively refused when the worktree can reach protected state, as are unknown clean
options (an abbreviation can negate dry-run). No exclusions are
injected: `-X` would give an exclusion the opposite effect.

## Git's optional index locks

The pack sets `GIT_OPTIONAL_LOCKS=0`. Git skips optional work that takes an index lock, such as
writing a refreshed index during `git status`; commands that need a lock to change the index, such
as `git add` and `git commit`, still use one. Set `GIT_OPTIONAL_LOCKS=1` in `env_sources` to opt
back in; that explicit user value overrides the pack default.

## Why these two

**`find` → `fd`.** `fd` is faster for the same work. That is the entire reason: not
safety, not scope, not tokens. A tool that does the same job slower is worth
replacing, and the block is how the replacement actually gets used rather than
merely being available.

**`grep -r` → `rg`.** Same reason, and the same magnitude.

## Why `grep` is only half-blocked

`grep` is blocked for recursive invocations only, `find` for all of them, and that
asymmetry is deliberate rather than an oversight.

`... | grep <foo>` is extremely common, and it is **not** the thing `rg` is better
at. Filtering a pipe is not a recursive search; `rg` brings nothing to it, so
refusing it would cost a familiar tool and return nothing. The recursive case is
the one where `rg` wins, so that is the case that is blocked.

`find` has no equivalent carve-out because it has no equivalent common non-recursive
use — and, unlike `grep`, nothing about its syntax marks the recursive case, since
`find` is recursive by nature and only has flags that *limit* it.

## `allow_flags`

Wired, and set by nothing here. `block_flags` matches on a flag being PRESENT and
has no negated form, so "block `find` unless it carries a depth limit" is not
expressible with it. `allow_flags` exempts an invocation and is scanned first, which
makes that rule expressible the day someone wants it.

Deliberately not used yet: the refactor that moved these rules into a pack is not
the place to also change which rules there are.

## What a block cannot reach

A blocker is the first `grep` or `find` a PATH lookup finds, so two things never meet it. A shell
that resets PATH: a login shell on macOS runs `path_helper`, and Debian's `/etc/profile` sets PATH
outright, so a `bash -l` started under the agent loses the block dir. And an agent's own shell
function of the same name: Claude Code's Bash tool defines `grep` and `find` as functions that run
its bundled search tools, so Claude's own commands skip these blocks in a jail and at the host
alike (measured in Claude Code 2.1.289, 2026-10-04). Every host launch that blocks anything says
both.
