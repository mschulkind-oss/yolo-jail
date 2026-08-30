---
title: "Where Relative env_sources Paths Resolve: Refusal Today, the Declaring File as the Open Question"
date: 2026-08-30
status: in-review
tags: [env, config, host, security, env-sources]
summary: "A relative env_sources file entry resolves against the launch directory — the right anchor inside a jail and an ACE-shaped hole at the host notch, where the cwd belongs to a workspace. This doc records the refusal that closed the hole, the re-anchoring option that would replace it, and why the clean version of that option costs a provenance rewrite of the config merge."
---

# Where relative `env_sources` paths resolve — refusal today, the declaring file as the open question

**Status:** DESIGN SKETCH, 2026-08-30. The refusal is IMPLEMENTED (commit `b08dda02`,
verified 2026-08-30); the re-anchoring option is unbuilt and awaits OQ-E1.

**The short version.** A relative `env_sources` file entry (`"prod.env"`) has to resolve
against *something*. Today that something is the launch directory — correct in a jail,
where the workspace declaring env for its own container is the ordinary case and the
container is the boundary, and wrong at the host notch, where the launch directory is a
workspace an agent edits: `cd` into a cloned repo, `yolo host -- claude`, and the repo's
`.env` feeds a process outside every sandbox. The landed fix refuses relative entries at
the host notch with the remedy in the message. The open question is whether "relative to
the declaring file" — security-sound, conventional (git's `include.path` works this way) —
should replace that refusal. It cannot be done cheaply: the config merge flattens
`env_sources` into one list with no per-entry provenance, so the honest version needs the
loader to carry file-of-origin, and it changes what a user-config entry means in a jail.

**Reads with:** [`host-agent-environment.md`](host-agent-environment.md) (the design this
extends — its §5.4/§6.1 step 3 define the env_sources channel at the host notch),
[`storage-and-config.md`](storage-and-config.md) (the user/workspace config scopes the
boundary argument leans on).

---

## 1. What a relative entry is, and the rule today

An `env_sources` entry is one of two shapes (the `env_sources` entry in `yolo config-ref`;
`ResolveEnvSources`, `internal/config/envsources.go:62`):

- a **string** — a dotenv FILE to read, or
- an **object** — inline vars, with `null` spelling unset.

Only the string shape has a path, and only a path that starts with neither `/` nor `~` is
*relative*. Resolution is one function, `ResolveEnvSourcePath`
(`internal/config/envsources.go:46`): expand `~`, pass absolutes through, and **join
relative entries against the workspace root**. Every notch used that one rule until
2026-08-30.

The host notch now scopes first: `hostScopedEnvSources` (`internal/cli/host.go:289`),
applied at `internal/cli/host.go:261` before BOTH the assignment pass and the removal
pass, drops relative string entries and warns:

```text
env_sources: "prod.env" is relative and ignored by `yolo host` — it would resolve
against the current directory, which a workspace controls. Use an absolute path or ~/…
```

Absolute and `~`-relative entries resolve exactly as before; inline entries are not paths
and never see the filter. Jails are untouched.

## 2. Why the host notch refuses

The composed config at the host notch is user-scope only (commit `ecfd2255`): the
workspace `yolo-jail.jsonc` is agent-editable — `/workspace` is bind-mounted rw — so
letting it set `LD_PRELOAD`/`BASH_ENV`/`NODE_OPTIONS` for a host process is arbitrary
code execution, reached by cloning a repo. That ruling closed the *config-merge* channel.
A relative path re-opened the same boundary through the *filesystem*: the entry lives in
the user's config, but the file it names is looked up in a directory the workspace
controls. Same payload, different door — which is why the refusal is at the host notch
specifically and not a general ban: in a jail, workspace-relative is the documented and
correct meaning.

## 3. The option space

| Option | Rule | Verdict |
| :--- | :--- | :--- |
| **A. Refusal** (current) | Relative entries at the host notch are skipped with a warning naming the remedy | **IMPLEMENTED** (`b08dda02`). Costs nothing ongoing; the intent is expressible today as `"~/.config/yolo-jail/prod.env"` — a spelling already in live use (the maintainer's own user config references `~/.config/yolo-jail/secrets.env` this way, observed 2026-08-30). |
| **B. Anchor at the user config's dir, host notch only** | `"prod.env"` → `~/.config/yolo-jail/prod.env` under `yolo host`; workspace-relative everywhere else | **Rejected.** One spelling, two meanings picked by which surface reads it — the exact two-readers-disagree class the one-pass `resolveEnvSources` rewrite (`19f92de1`) exists to kill. Also unimplementable *honestly*: without provenance (§4) it guesses the declaring file, and an `include_if_found` entry would guess wrong. |
| **C. Relative to the declaring file, everywhere** | A path in a file means "beside me" — git's `include.path` convention | **Open (OQ-E1).** Security-sound (§4) and the only non-split re-anchoring. Costs a provenance rewrite of the merge and a semantic shift for user-config entries in jails. |

> [!WARNING]
> B is the tempting one — it looks like a one-line anchor swap in `hostEnvVars` — and it
> is the one that must not ship. The dialect split is permanent once configs exist that
> rely on it, and the wrong-file guess for included entries is invisible to the user.

## 4. What C actually costs

**The merge destroys provenance before resolution ever runs.** By the time
`hostEnvVars` sees `env_sources`, the loader has concatenated entries from
`config.jsonc`, every `include_if_found` file, any `--user-layer`, and (jail-side) the
workspace config into one flat, ordered list — user list first, workspace list second, as
`yolo config-ref` documents. The chain is `loadUserScopeConfig`
(`internal/config/userlayer.go:144`) → `MergeConfig` (`internal/config/load.go:94`) → the
jail-side merge in `LoadConfig`. "The declaring file" is not a thing the resolver can
ask for; making it one means the loader carries file-of-origin per entry (or rewrites
relative→absolute per file at load time, which would also rewrite the assembled snapshot
the jail reads verbatim).

**The unification property that makes C worth considering at all:** the workspace config
sits *at* the workspace root, so beside-the-file **is** workspace-relative for
workspace-declared entries — jail behavior for those does not change by one byte. The
only meaning that moves is a **user-config entry inside a jail launch**: today
`~/.config/yolo-jail/config.jsonc`'s `"prod.env"` resolves against the workspace in a
jail; under C it would resolve against `~/.config/yolo-jail/`. That is arguably the more
defensible meaning (the file beside the config that named it), but it is a change to a
documented rule, and it must be ruled deliberately, not arrive as a side effect.

**Security under C is sound.** The anchor is always a user-owned location: the user
config, a user-typed `include_if_found` path, or a `--user-layer` path the human named on
argv. None of those is workspace-reachable, so the boundary the refusal defends stays
closed — the question is cost and dialect, not safety.

## 5. Non-goals

- No change to jail-side resolution for **workspace-declared** entries — C preserves it
  structurally, and any option that doesn't (B) is rejected on that basis alone.
- No dotenv dialect changes: files have no "unset" syntax and will not get one
  (`envsources.go`'s removal comment already rules that line).
- Nothing about inline entries, `null` removals, or ordering — settled 2026-08-30 in
  `19f92de1`.
- Not a secrets-management design: where secrets *live* is `storage-and-config.md`'s
  subject; this doc is only about what a relative path points at.

## 6. Risks (if C is adopted)

| Risk | Mitigation |
| :--- | :--- |
| Provenance plumbing touches the most load-bearing config code (`LoadJSONCWithIncludes`, `MergeConfig`, the snapshot) | Land behind the existing tests plus a golden for include-relative resolution; the merge's ORDER is pinned, its per-entry bookkeeping is new |
| User-config entries silently change meaning in existing jails | `yolo check` warning when a user-config relative entry would resolve differently than it did pre-C (old anchor known at check time) |
| The dialect split re-enters as "host-only C" once the plumbing exists | OQ-E2 rules this out in advance: C is unified or not at all |

## Open Questions

1. 💬 **OQ-E1: Refusal (A) or declaring-file anchoring (C) at the host notch?**

   Decides whether `hostScopedEnvSources`'s refusal is final, and whether the config
   merge gains per-entry provenance — the largest consequence in this doc. If ruled C,
   OQ-E2 must be ruled with it.

   _Leaning:_ Keep A until the ergonomics actually hurt — the remedy is expressible
   today and already in live use, and C buys ~20 characters at the cost of the merge
   rewrite. If relative entries are wanted at all, rule C; B is not on the menu.

   **Answer:**
   > _(empty — fill in when decided)_

2. 🔒 **OQ-E2: If C — unified everywhere, or host-notch only?**

   Blocked on OQ-E1 (live only if C is chosen). Host-notch-only C is option B wearing
   C's implementation, so the real question is whether the jail's user-config entries
   move to the declaring-file anchor too.

   _Leaning:_ Unified, or don't do it — the split is the one thing this design must not
   ship. The jail-side shift is the more defensible meaning and costs one `yolo check`
   warning to land safely.

   **Answer:**
   > _(empty — fill in when decided)_
