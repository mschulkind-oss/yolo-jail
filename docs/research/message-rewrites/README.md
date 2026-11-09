---
title: User-facing message rewrites
status: research
date: 2026-11-07
---

# User-facing message rewrites

This is an audit of every message yolo prints to a person or to an agent, measured against
[the plain-language principle](../../reference/plain-language-principle.md): write to the reader
in their own terms, name no internals, be short, and end every stop with an easy next step (the
[happy path principle](../../reference/happy-path-principle.md)). A "rule N" in the parts is that
principle's rule N.

**It found two kinds of problem, fixed as two separate pieces of work:**

1. **Messages that are wrong** — false advice, advice a TOML config cannot take, formatting bugs,
   text that can never print. These are bugs, fixed first and on their own, from
   [Messages that are wrong today](#messages-that-are-wrong-today) and each part's "Wrong today"
   section.
2. **Messages that are badly written** — true, but not in the plain-language style. This is the
   language sweep, done afterwards, from [the patterns](#patterns-that-recur-across-the-parts)
   and the parts' before/after entries. Each part below lists messages
that break a rule. Each entry gives the current text ("Before"), a proposed rewrite ("After") and
the rule it breaks ("Why"). This is research: it changes no code, and no rewrite has been applied.

The audit is a snapshot of the tree on 2026-11-07. Line numbers move as code changes, so treat
each `file:line` as the place to start looking, not a promise.

## The parts

The source was split by package. Each part was read once, then read again to settle what the first
pass left open.

| Part | Covers |
|---|---|
| [CLI commands](cli.md) | `internal/cli` (top level), including `yolo --help`, every command's usage text and `config_ref.txt` |
| [Launching and `yolo check`](launch-and-check.md) | `internal/cli/run`, `internal/cli/check` |
| [Inside the jail](jail-boot.md) | `internal/entrypoint`, `internal/jailcontent` (the briefing and the built-in skills an agent reads), every `cmd/` binary |
| [Config](config.md) | `internal/config`, `internal/configedit`, `internal/configmigrate`, `internal/configdoc` |
| [Packs](packs.md) | `internal/packload`, `packdecl`, `packsrc`, `agentcfg`, `render`, `hostskills`, `loopholes`, `loopholedecl`, `hostfloor` |
| [Brokers, macOS and the rest](brokers-macos-rest.md) | every other `internal/` package: the git and GitHub brokers, approvals, `callbroker`, macos-user, images, self-update, auth daemons |

Each part opens with its own patterns: one problem repeated across many messages, with a single
fix for all of them. After that come the individual entries. Each part ends with "Wrong today"
(messages that state something false) and "Not covered" (what its pass did not read).

## Patterns that recur across the parts

The same problems show up in every part. A fix for one is a sweep across the tree, not a rewrite
per file.

- **Old config spellings in advice.** Messages tell the user to edit `config.jsonc` or
  `yolo-jail.jsonc`, or give a JSON snippet, or name a legacy key such as `packages`,
  `env_sources`, `profile` or `loopholes.<name>`. A TOML user is sent to a file they do not have,
  in a syntax their file rejects. The fix is to name a `yolo config set|add|unset` command,
  `yolo active-provider` or `yolo pack add|remove`. Where a message must name a file, use
  `paths.UserConfigDisplayPath()`. Every part has this pattern.
- **Design IDs and repository paths in printed text.** Users see `OQ-…`, `…-D…` and
  `docs/reference/…`. They cannot act on an ID, and most of them have no checkout. Drop the ID,
  and link the published user guide when a link helps.
- **Project words used undefined.** Examples: "notch", "floor", "surface", "contribution",
  "keeper", "doorway", "front", "working set", "Allow once" used as a noun, and "X asks" meaning
  "X needs the human's approval". Say what the thing does instead.
- **Explaining the design instead of giving the fix.** Messages tell the threat model or the
  history of a key ("REMOVED — …", "user-scope only — <reason>") and end with no command.
- **A stop with no real next step.** Examples: "retry" after a failure that repeats every time,
  "relaunch the jail" for internal state the user cannot see, "then: yolo check" appended to about
  50 notes, and `just install` advice given to people who installed from Homebrew or a release
  archive.
- **Code leaking into text.** Go package prefixes, `%T` types, Python-style value echoes, boot
  step names such as `generate_bashrc`, and em-dash run-ons that join several thoughts.

## Messages that are wrong today

Each part's "Wrong today" section gives the details. These are the ones worth fixing as bugs,
whatever happens to the rewrites:

- **Unusable advice in a TOML config.** Messages give JSON and `config.jsonc` advice, which a TOML
  config cannot take: `profiles.go` (providers), `prism.go` (the dropped-entry lines), the
  `host_management` advice, the macOS memory guard, the AWS profile refusal, the CA hint, and
  `config_ref.txt`'s examples.
- **Advice that is false.**
  - The macOS guest refusal names a `loopholes` key that no longer exists.
  - The jail-mechanics recipe describes a host-side loophole "switch" that was retired.
  - `yolo-ps` gives its two steps in the wrong order.
  - `gitbroker/access.go` says "then relaunch" where the running broker picks up the grant itself.
  - The `--accept-config-changes` refusal says "THIS LAUNCH ONLY", but the approval is recorded
    for later launches.
  - The briefing's "Limitations" section says authenticated git fails without jail credentials,
    which is false when the broker runs.
- **Formatting bugs.**
  - `yolo run --help` lists `--update` twice.
  - `backgroundadvance.go` prints a bare "in .json".
  - `reachability.go:574` prints a sentence that does not parse.
  - The tools recipe double-wraps a sentence in backticks.
  - The macOS certificate-trust disclosure gets plurals and nouns wrong.
- **Text that can never print.** The `--add` branches in the brokers and `hostfloor`'s
  `EnsureFresh` are unreachable. Delete them; do not reword them.
- **A wrong cause.** `loopholes/discover.go` reports a missing loophole directory as "outside the
  pack".

Already fixed since the audit: `yolo init-user-config` no longer writes `config.jsonc` beside an
existing `config.toml` (commit `2bbdd801f`).

## Calls the parts left open, and how they are settled

The parts left these open. Each follows from a standing rule, so none waits on the owner
(settled 2026-11-09):

1. **Unremovable keys.** `yolo config unset` refuses `allow.<pack>.<key>` keys such as
   `allow.github.sets`, so a message about removing one cannot name a verb. Advice names a verb
   (AGENTS.md, "Config is TOML"), so `yolo config unset` learns to remove them; that is part of
   the wrong-messages fix ([brokers-macos-rest.md, Wrong today](brokers-macos-rest.md#wrong-today)).
2. **No verb for `host_floor`.** The per-pack `host_floor` opt-out has no verb either, because
   the schema has only the top-level key. Same rule, same fix: the verb learns the per-pack key
   ([launch-and-check.md, Wrong today](launch-and-check.md#wrong-today)).
3. **Skill-file cuts.** The two built-in skills and the skills briefing are agent-facing text, so
   rules 6 and 8 apply: cut history and internals an agent does not need to act
   ([jail-boot.md](jail-boot.md)). Done in the language sweep.
4. **Bug-only refusals.** A refusal only a yolo bug can cause ends by saying so and linking the
   issues page: the [happy path principle](../../reference/happy-path-principle.md) requires
   naming who can act ([jail-boot.md](jail-boot.md)).
5. **Two unconfirmed facts** behind rewrites: the overlay file-naming rule
   (`packoverlay/conventional.go:107`), and whether `env.set.SSL_CERT_FILE` counts as the
   caller's own bundle in the CA disclosure ([brokers-macos-rest.md](brokers-macos-rest.md)).
   Whoever rewrites those two messages checks the code first.

## Follow-ups outside the messages

- **A possible bug:** `legacyFromDeclaration` in `internal/entrypoint/hostbriefing.go` reads only
  `pack.json`, so a local pack written in `pack.toml` may skip its "from names AGENTS.md" check.
  Not yet verified.
- **Rewrite texts left between parts:** each part's "Not covered" section lists texts whose
  owning package another part did not read in full. Examples are `changetiming`,
  `render/modes.go`, `run/mechanicsfacts.go` and `run/workspaceskills.go`. Pick them up with the
  sweep for their pattern.
- **The config reference** (`config_ref.txt`) is being rewritten with TOML as the primary form and
  a legacy JSONC section kept in step, which covers that file's entries in [cli.md](cli.md).

## How the audit was checked

- **Citations:** every `file:line` in the six parts was checked to name an existing file and a
  line inside it. Two wrong citations were found and fixed.
- **Quotes:** for each entry whose "Before" block holds a plain run of words, three consecutive
  words were looked up in the cited file. All but three matched. Those three are strings the code
  assembles at runtime, and each was confirmed by hand.
- **Not checked:** the rewrites themselves have not been reviewed against every caller of the code
  they reword.
