# guardrails

Tools the jail refuses in favour of a faster one for the same work.

Not shipped into the jail — a README at a pack root is not a briefing source (that
is `AGENTS.md`), and nothing here needs to reach an agent up front. The blocker
prints its own message and its own alternative at the moment it refuses, which is
where that information belongs. This file is for whoever edits these rules.

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
