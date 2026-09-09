---
status: current
verified: 2026-09-09
verified_commit: 356bcec8
covers:
  - internal/agentcfg/luahook/
  - internal/packdecl/contributes.go
  - internal/loopholedecl/loopholedecl.go
  - internal/frameproto/
  - flake.nix
tags: [packs, third-party, logic, protocol, trust, nix]
---

# Third-party pack logic — how a pack ships computation

**Status:** CURRENT as of 2026-09-09, verified against `356bcec8`.

A pack can ship **one** kind of computation: a sandboxed Lua producer in the
[`derive` slot](pack-system.md#the-derive-slot). That is the whole of pack-supplied logic — there is
no reshape operation DSL, no pack-supplied effect code, and nothing else in a pack executes. A
projection that a fixed combine rule cannot express is a `derive` function; a projection that
`derive` cannot express does not exist in the tree.

This page is the reference for the question behind that: **what a third-party pack author can and
cannot do, and why the constraints are what they are.** The mechanisms it names all already exist,
which is the load-bearing part — a pack needing "real logic" reaches for a precedent rather than a
new subsystem.

| Concern | Lives in |
| :--- | :--- |
| The Lua sandbox and its registration API | `internal/agentcfg/luahook` (`DeriveCtx`) |
| What a pack manifest may declare, field by field | `internal/packdecl` (`Contribution`) |
| A loophole's presence requirement | `internal/loopholedecl` (`Requires`: `CommandOnPath`, `FileExists`) |
| The frozen jail↔host wire format | `internal/frameproto` |
| Build-from-source and pinned-nixpkgs package specs | `flake.nix` |

**Reads with:** [`pack-system.md`](pack-system.md) (the manifest, the kinds, and the derive slot's
full contract), [`what-yolo-is.md`](what-yolo-is.md) (the three code-execution seams and the
hermeticity traps), [`loophole-protocol.md`](loophole-protocol.md) (the wire format).

---

## The retraction that matters: "can't be Go" was wrong

The real constraint is the **`goSrc` fileset**: the hermetic image build sees only `go.mod`,
`go.sum`, `vendor/`, `cmd/`, `internal/` and `packs/`, so a Go package outside that set vanishes
from the jail.

That rules out exactly one thing: **linking third-party Go code into the yolo binary.** It does not
rule out third-party Go code — it rules out third-party Go code *inside our compilation unit*. A
separate binary, built separately, invoked over a protocol, is unaffected. Collapsing "can't be
linked" into "can't be Go" is an easy slip and it changes every downstream conclusion.

> [!WARNING]
> **Do not cite the fileset as a reason a pack cannot ship compiled code.** Cite it as the reason
> *official* pack logic lives in-repo. The fileset is a fact about linking; the trust and delivery
> questions are separate and answered separately.

## The four mechanisms that already exist

None of what a pack author needs has to be invented. Each of these is a live precedent.

### A build-system allowance — the `packages` key already has one

The `packages` config key accepts three spec forms, all hash-pinned: a bare name (latest from the
image's nixpkgs), `{name, nixpkgs: "<commit>"}` (pinned to an arbitrary nixpkgs commit, fetched via
`builtins.fetchTarball`), and `{name, version, url, hash}` (an `overrideAttrs` with a fetched source
tarball — build this package from source at that URL). The third form is a genuine
build-from-arbitrary-source path with a hash pin. `yolo config-ref` documents the key; `flake.nix`
is the implementation.

The fetch is legal inside the hermetic build because these are fixed-output derivations — see
[`what-yolo-is.md`](what-yolo-is.md#traps) on why hermeticity is "no *unpinned* network".

> [!WARNING]
> **A pinned or override spec with a bad hash or rev aborts the whole nix eval, and `tryEval` cannot
> catch a builtin fetch error.** Only plain-string specs degrade gracefully; pinned and override
> specs are all-or-nothing, and `flake.nix` says so at the branch. So a pack contributing to the
> image needs **pre-flight validation at install time**, not fail-open at build time — otherwise a
> pack's typo turns a config error into an unbuildable image. This is the same conclusion every
> other mechanism in this cluster reaches.

### "Require a nix package" — the `requires` field

`loopholedecl.Requires` supports `command_on_path` and `file_exists`, and the gate is
**degrade-to-inactive, not crash**: if something pathological has the binary missing, the loophole
goes inactive rather than failing the boot. That is exactly the semantics a pack's capability
requirement wants. The pack-manifest twin is the `requires` contribution kind, which carries
`install_hints` for the dependency checker.

### A protocol — `internal/frameproto`, already frozen

`internal/frameproto` is the frame protocol spoken between a jail-side client and a host-side
loophole daemon, and **the wire format is a frozen interop contract**: length-prefixed frames,
stream ids, a signed exit code that round-trips negative values for signal deaths. It is
transport-agnostic — every function takes an `io.Reader`/`io.Writer` — which is why the transport
unification left it untouched.

And loopholes already execute third-party programs. The command is an argv `[]string` rather than a
shell string, so there is no shell-injection surface, and a loophole's own directory is bind-mounted
`:ro` into the jail under `/etc/yolo-jail/loopholes/<name>` — with a *tested* contract for a
loophole shipping and running its own script.

### "Something from mise" — viable, and the weakest of the four

mise is present and can install a pack's tool. But mise is the wrong layer for *pack logic*: it is a
per-jail tool installer, its store is deliberately separate from the host's — installs never cross
the boundary in either direction — and the project's own three-way rule (`packages` vs `mise_tools`
vs a project manifest) exists specifically to stop version pins proliferating. Adding pack logic as a
fourth pin site is the anti-pattern that rule prevents.

> **Use mise for a pack's *tool* needs where a jail-local tool is right. Do not use it as the
> pack-logic mechanism.**

## Official packs use the same seam

This is what keeps "official and third-party packs are structurally identical" honest. An official
pack's logic is a `derive` function like anyone else's; where an official pack needs a *process*, it
names a **yolo subcommand** — which is exactly how loopholes already do it, e.g. the
`host-processes` daemon's argv. So official-pack logic is compiled Go, inside `goSrc`, type-checked
and `go test`-able, while being reached through the same declarative surface a third party writes.

The `goSrc` fileset therefore stops being a constraint on the design and becomes merely the reason
official logic lives in-repo.

## Trust

A `derive` function is the *only* pack computation, and it is sandboxed by construction: the Lua VM
opens base, table, string and math and nothing else, under an instruction budget, so it cannot read
a file or spawn a process ([`what-yolo-is.md`](what-yolo-is.md#the-three-code-execution-seams)).

Everything else a pack ships is data, and the trust model for it is stated plainly in
[`pack-system.md`](pack-system.md#the-credential-boundary-disclosure-not-consent): the boundary is
drawn at **selection**, not at a prompt. Naming a pack in `packs` requires writing the user config
as the host user — user scope only, inexpressible at workspace scope by construction — and the
launch banner says what each loaded pack actually reaches.

> [!WARNING]
> **Do not add an install-time approval gate for a pack that ships logic.** There was one, and it
> was deleted as theatre: selecting the pack already required strictly more authority than the gate
> withheld ([`gate-placement-principle.md`](gate-placement-principle.md#test-1--the-authority-test-could-this-actor-already-do-it),
> [`OQ-TP9`](../design/trust-paths.md#decision-ledger)). What replaces it is disclosure.
> Any argument for re-adding one has to name an actor who has *less* authority than "can write the
> user config as the host user" — and if it can, that is a different, smaller, structural gate.

For the trust *level* itself, MCP is the precedent: yolo already installs and runs arbitrary
third-party executables on first boot. A pack's `derive` function is a strictly smaller risk than
that, because it has no filesystem handle and no network.

## What is not built

**The subprocess "projector" — a pack declaring a program yolo runs, writing a request on stdin and
reading config values on stdout — is designed and deliberately unbuilt.** It was tier 2 of this
design's two-tier plan, an escape hatch for a pack needing computation the declarative layer could
not express, and it was ruled **not needed** once every real projection turned out to be
expressible. It is parked as `BACKLOG.md` **C7** (with `D7`, staging a third-party projector binary
into the jail, parked behind it), and the word `projector` appears nowhere in `internal/` or `cmd/`.

If it is ever revived, three things the design settled are worth not re-deriving: the projector would
be a **pure function** handed JSON on stdin and nothing else — no filesystem paths, no credentials,
enforceable by construction rather than by sandbox; it would be **one request/response**, needing
none of `frameproto`'s streaming, copying only the discipline of a versioned frozen contract; and of
the three ways its binary could arrive, an **interpreted script shipped in the pack** is the cheap
default (content-addressed with the pack, and `python3` is already in the image) while a **nix
package via `packages`** is the only genuinely reproducible one. A prebuilt binary in the pack tree
is the weakest claim — a fetched artifact with no build provenance.

> [!NOTE]
> Two premises of the original design have since expired, and both are worth knowing before reading
> its argument in git: `bundled_loopholes/` left the `goSrc` fileset when the directory was deleted,
> and the "one new gate" a projector was said to need was the fetched-pack approval prompt, which no
> longer exists.

## What this does not license

- **Not a second execution seam.** A pack requests a named `hook` core implements, or writes a
  `derive` function. It does not ship an effect, a shell command, or a program yolo invokes.
- **Not pack content as an image input.** Content and config values are read at compose time.
  Baking them would put the pack in the image's store path, so editing a prompt would cost a rebuild
  and a host load — which destroys the point of packs. A pack needing a system *capability* is a
  different claim and feeds the derivation exactly as a `packages` entry does.
- **Not a licence to invent a protocol.** If a new host↔jail conversation is genuinely needed,
  `frameproto` is frozen and transport-agnostic, and the loophole framework is how a program on
  either side is named and started.
- **Not a claim that the projector is scheduled.** It is designed, ruled unnecessary, and parked.
  Reviving it needs a case the `derive` slot cannot serve, not a preference for subprocesses.
