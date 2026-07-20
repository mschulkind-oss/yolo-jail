# Principle: fill the matrix, don't support every tool

**Audience:** anyone adding a setup path, a runtime, a builder, an installer,
or a "how do I do X on platform Y" answer to yolo-jail. Read this before
adding a second way to do something.

## The principle

For any capability, pick **one** happy-path tool that works across the whole
support matrix, and make *that* the documented, tooling-blessed path. Do not
enumerate every tool that *could* work. Coverage of the matrix is the goal;
breadth of supported tools is not.

> Fill the matrix. Support one path per cell. Everything else is an escape
> hatch at most — never a co-equal option.

## Why

Every additional "supported" tool is a standing cost: more docs to keep
correct, more `yolo check` probes, more failure modes, more bug reports, more
drift as each tool changes. A menu of options also pushes the *choice* onto
every user — which is exactly the work we should do once, for them. Three
mediocre options a user has to evaluate is worse than one that just works.

The upfront cost is fine. We (the maintainers) happily pay a one-time setup
cost — publish a cache, wire a CI job — so that every user, on every
platform, at any time, gets the trivial path. "Easy and consistent for
everyone, even if there's work up front" beats "flexible but everyone
fends for themselves."

## How to choose the one tool

Rank candidates by, in order:

1. **Matrix coverage.** Does it work on *every* platform we support (Linux,
   macOS arm64, macOS Intel), for a user with no special setup? A tool that
   only works in some cells loses to one that works in all.
2. **Least per-user infrastructure.** Prefer *download* over *build*; prefer
   *built-in* over *install a third-party thing*; prefer *zero daemons* over
   *run a VM*. The best path asks the user to install nothing new.
3. **Consistency.** The same command/answer on every platform beats a
   different dance per OS. One thing to learn, document, and debug.
4. **Acceptable one-time maintainer cost.** Prefer paths where *we* absorb a
   one-time setup (a cache, a CI job, a baked config) to give users the easy
   path — over paths that push recurring effort onto each user.
5. **Minimal support surface.** Fewer moving parts we have to keep working.

A second option only earns its place if it covers a matrix cell the first
one genuinely cannot — not because it's someone's preference or "also works."

## Consequences (how this shows up in the code)

- **`yolo check` and CLI messages name THE path, not a menu.** When something
  is missing, point at the one fix. Don't list three.
- **Tooling is wired for one path.** `just` recipes, CI jobs, and config
  default to the chosen tool. Alternatives, if documented at all, live in a
  prose "escape hatch" note — never in a check probe or a recommended command.
- **Docs lead with the happy path.** Alternatives come after, clearly marked
  "you probably don't need this," and are dropped entirely once they stop
  covering a unique cell.

## Worked example — building the jail image on macOS

The capability: get a runnable Linux OCI image on any host.

- **Chosen happy path: a published binary cache (Cachix).** Fills every
  matrix cell identically — the user *downloads* the prebuilt image; no
  builder, no VM, no Nix knowledge. We pay the one-time cost (publish the
  cache, a release-gated CI push). This is the answer for everybody,
  everywhere. See [handoff-cachix-cache.md](../plans/handoff-cachix-cache.md).
- **Single fallback (only when the cache can't help): `nix-darwin
  linux-builder`.** Needed just for a custom package that isn't cached, or
  before the cache is live. Chosen because it's the *official* Nix tool,
  works on any macOS Nix install, and is one command — it wins criteria
  1–3 among builders.
- **Deliberately NOT supported:** Colima as a Nix builder (it's a Docker VM,
  not a Nix builder — strictly more setup, loses criterion 2), a hand-driven
  QEMU VM (more moving parts, loses 5), and a remote Linux host (you must
  already own one — fails criterion 1, "everywhere"). Each could *work*; none
  covers a cell the fallback doesn't, so none is worth the support surface.
  A remote host survives only as a one-line "advanced" prose note.

The old instinct — "the error said no builder, so document Colima + QEMU +
remote as options A/B/C/D" — is exactly what this principle rejects.
