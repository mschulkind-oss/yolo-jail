---
status: current
verified: 2026-11-09
summary: "Everything yolo writes for a reader who is not reading its code — printed messages, usage text, the agent briefing and skills, the user guide, the changelog — is written to that reader, in their terms, in plain sentences, and names what to do next."
tags: [principle, cli, ux, errors, docs, writing]
---

# The plain-language principle

**Status:** PRINCIPLE, adopted 2026-11-09. The rules are the owner's: written first for the
changelog in the [`cutting-releases`](../../.pi/skills/cutting-releases/SKILL.md) skill
(2026-10-01 and 2026-11-06), and moved here so every kind of reader-facing text uses one set.

## What it covers

Any text a person or an agent reads without opening yolo's source:

- what yolo prints: refusals, errors, hints, `yolo check` findings, prompts, progress and
  launch lines, `--help` and usage text;
- what an agent reads: the briefing and the built-in skills;
- the [user guide](../../userguide/README.md) and the [changelog](../../CHANGELOG.md).

It does not cover design docs, reference docs, code comments or test names. Their reader has the
repository open, and those docs have their own rules (the `design-doc` and `vantage-docs`
skills).

## The rules

The numbers are stable: the [message audit](../research/message-rewrites/README.md) cites them.

1. **Write to the reader, in their terms.** Say what they can now do, see or must do. Name no
   package, Go symbol, commit hash, or file the reader never touches. A file they do touch
   (`config.toml`, `yolo-jail.toml`) and an exact command are fine.
2. **Be short by cutting ideas, not grammar.** Drop a clause or an explanation the reader does not
   need right now. Never squeeze a sentence into shorthand. Write for a smart reader with no
   context.
3. **Every sentence has a subject and a verb**, headings and bold leads included: "**Agents can
   open pull requests without asking.**", not "**GitHub writes that need no answer.**"
4. **No verb used as a noun.** Say who does what: "an agent writes to GitHub", not "GitHub
   writes"; "when yolo refuses, it lists every command you can run", not "a refusal lists every
   answer".
5. **No noun stacks or headline shorthand.** Unpack "the run-as-you refusal" or "the host-run
   message fixes" into a clause.
6. **No project-internal words.** No decision ids (`OQ-…`, `…-D…`), no design-doc paths, and none
   of the project's own words (notch, doorway, ceiling, floor, surface, contribution, working set,
   keeper, prism) or a setup or loophole name the reader has not met. Use the everyday word, or
   define the term in the same sentence where it first appears.
7. **One idea per sentence.** Split a run-on list chained with commas, semicolons or dashes.
8. **Say what to do, not how it works.** Never explain the internals, why it was designed so, or
   how a bug was found, unless the reader needs it to act.

## What overrides shortness

- **Every stop names the next step**
  ([the happy path principle](happy-path-principle.md)). A rewrite may shorten a refusal but
  never drop its command, question, or the person who can act.
- **Keep it true.** The text says exactly what the code does. A short sentence that is false is
  worse than a long one that is true.
- **Advice names a `yolo` verb, not a file to hand-edit** (AGENTS.md, "Config is TOML"). Keep the
  verb's exact spelling, its flags, and any `%s`-style placeholder and what fills it.
- American English.

## Read it back

Read each line as a reader who has never seen the design docs. If a phrase only makes sense after
reading them, rewrite it.
