# Retired decisions

**What this is.** Things we decided **not** to build, and architectures we rejected — kept because
a decision with no record gets re-proposed. Each entry says what was considered, what was chosen
instead, and *why*, so a future reader (or a future agent) can tell "we never thought of that" from
"we thought of that and it does not work."

**This is not a changelog.** Work that shipped is recorded in
[`../plans/shipped-2026-08-12.md`](../plans/shipped-2026-08-12.md) and its siblings, or in the commit
history. Open work is in [`../plans/roadmap.md`](../plans/roadmap.md). **Nothing in
this file is pending.**

**Adding an entry.** Move it here the moment the decision is made, not later — the reasoning is
freshest at the point of rejection, and the cost of losing it is someone rebuilding the thing.

---

## Thread A — why there is no `claude-teams` pack

The review question that dissolved most of this thread: *"say you don't use either auth pack — you
try to log in to a Teams account and Claude's going to let you."*

Verified: the base `claude` pack already ships `hook shared_credentials` + machine-scope
`.claude-shared-credentials` state, plus the `claude-oauth-broker` loophole gated on
`command_on_path: claude`. So **`packs: ["claude"]` alone is already the complete Teams setup** —
Claude Code does its own `/login`, the credential is machine-shared, and the broker serializes
refreshes.

So the two "modes" are not peers competing for a slot. **Teams is the floor; Bedrock replaces the
floor.** Three things were retired on that basis:

1. **Moving `shared_credentials` off the base pack is WRONG and withdrawn** — it would make the good
   default worse, costing credential sharing for a pack that adds nothing.
2. **There is no `claude-teams` pack.** Nothing shareable would be in it.
3. **`provides`/capability exclusivity is retired for this case.** A mechanism was built for a
   conflict between peers, then no peers were found.

What remains is **one pack, `claude-bedrock`**: `config-overlay` carrying `CLAUDE_CODE_USE_BEDROCK`,
`AWS_REGION` and the Bedrock-shaped model IDs, with AWS keys in `env_sources` so the pack stays
secret-free and therefore shareable. Deselecting it withdraws the overlay, which fixes the bug that
started the thread — a manual switch left a `us.anthropic.` model pin behind because it had been
hand-edited into `settings.json`.

**Also retired: `requires_pack` / pack→pack composition.** Its motivating case was two auth packs
excluding each other; with one Bedrock pack there is nothing to exclude, and a personal pack selected
without it is additive and harmless (an MCP server whose key is absent is already inert via
`requires_env`). Build it when something breaks without it.

## A6 — why capability supersession beat the cheaper options

Five options were costed for making the Bedrock path disable the OAuth broker. Option 3 (the loophole
declares `serves: ["claude-oauth-refresh"]`; a pack declares `supersedes` with a mandatory `because`)
won **against the recommendation at the time**, and the reasoning generalizes:

- *"Wait for a second consumer"* is right when **we** pay for being wrong, and wrong when a stranger
  does. A loophole manifest is a surface other people build on, so the first outside author who needs
  this either cannot do it or invents a workaround we support forever.
- It changes **what** gets built, not just when. `enabled: false` says *"turn that thing off"*; the
  true statement is *"that job does not need doing"*. Only the second survives the loophole being
  renamed or reimplemented, and only the second lets an alternative implementation participate. The
  general design is not the expensive version of the specific one — the specific one was a latent bug.

**A tempting option that is WRONG, recorded so nobody re-proposes it:** gating on the shared
credentials file being non-empty. It reads as exactly the right question and is broken by
chicken-and-egg — a fresh Teams user's creds file is empty *before* their first `/login`, so the
broker would not start and that session's refreshes would run unserialized. Precisely the race it
exists to prevent.

## The local pack IS layer 4

yolo owns `~/.claude/skills` and `~/.claude/CLAUDE.md` wholesale, so a user contribution has nowhere
else to live — and "commit it to a repo pack" is not an answer for a half-baked skill, a
machine-specific one, or scratch space you do not want in git. The jail already had this slot (layer
4, "the user's OWN skills tree, written last so a same-named local skill wins"). The local pack is
that slot given a home yolo does not overwrite, which is why it was a defect rather than a design
choice. As an ordinary pack entry appended last it already holds layer 4's precedence, so the fix was
to DELETE the fourth layer, not repoint it.
