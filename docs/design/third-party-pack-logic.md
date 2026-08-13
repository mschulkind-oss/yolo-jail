# Third-party agent packs: how logic actually ships

**Status:** design, 2026-07-26. Answers: *"I definitely want third party agent packs. How
would we design this exactly? Why so immediately 'can't be Go'? Can't we design some
protocol? Give some build system allowance? Require a nix package? Require something from
mise?"*

**Reads with:** [three-decisions.md](three-decisions.md) (the decision this settles),
[packs-and-the-prism.md](packs-and-the-prism.md) (provision/compose phases, typed exports),
[what-yolo-is.md](what-yolo-is.md) (the earlier, narrower option table).

---

## 0. First: "can't be Go" was too fast, and the reason matters

What I actually established is narrower than what I said. The real constraint is the
**`goSrc` fileset** (`flake.nix:61`): the hermetic image build only sees `go.mod`, `go.sum`,
`vendor/`, `cmd/`, `internal/`, `bundled_loopholes/`. A Go package outside that set vanishes
from the image.

That rules out exactly one thing: **linking third-party Go code into the yolo binary.** It
does *not* rule out third-party Go code — it rules out third-party Go code *inside our
compilation unit*. A separate binary, built separately, invoked over a protocol, is
unaffected. I collapsed "can't be linked" into "can't be Go," which is wrong and was
load-bearing for the recommendation.

Every one of the four mechanisms you named is available. Three of them already ship here.

---

## 1. The mechanisms that already exist

This is the important part: **none of what follows needs inventing.** Each is a live
precedent in this repo.

### 1.1 A build-system allowance — already shipped, and more capable than I credited

The `packages` config key already accepts three spec forms
(`flake.nix:204-232`), all hash-pinned:

| Form | What it does |
|---|---|
| `"strace"` | latest from the image's nixpkgs |
| `{name, nixpkgs: "<commit>"}` | **pinned to an arbitrary nixpkgs commit** — fetched via `builtins.fetchTarball` |
| `{name, version, url, hash}` | **`overrideAttrs` with a fetched source tarball** — i.e. *build this package from source at that URL* |

So "give some build system allowance" is **already the shipped behavior**, and the third form
is a genuine build-from-arbitrary-source path with a hash pin. A pack contributing to
`provision` is contributing to a mechanism that exists.

The fetch is legal inside the hermetic build because these are fixed-output derivations —
verified earlier: `builtins.fetchTarball` with a wrong hash returns a *hash mismatch*, i.e.
it reached the network. Hermeticity is "no unpinned network," not "no network."

**Known sharp edge, already documented in the flake** (`flake.nix:~255`): a pinned or
override spec with a bad hash/rev **aborts the whole eval**, and `tryEval` cannot catch
builtin fetch errors. Only plain-string specs degrade gracefully. So a pack's provision
contribution needs *pre-flight validation*, not fail-open — which is the same conclusion as
everywhere else in this cluster.

### 1.2 "Require a nix package" — already the `requires` field

`bundled_loopholes/host-processes/manifest.jsonc` is close to the exact design you're
describing:

```jsonc
{
  "name": "host-processes",
  "version": 1,
  "enabled": true,
  "requires": { "command_on_path": "ps" },
  "transport": "loopback-tls",
  "lifecycle": "spawned",
  "host_daemon": { "cmd": ["yolo", "internal", "daemon", "host-processes", "--endpoint", "{endpoint}"] },
  "doctor_cmd": ["yolo", "internal", "daemon", "host-processes", "--self-check"]
}
```

`Requires` (`loopholes.go:107`) supports `command_on_path` and `file_exists`, and the gate is
**degrade-to-inactive, not crash** (`loopholes.go:162-166`) — the manifest's own comment says
"if something pathological has it missing, the loophole goes inactive rather than crashing."
That is exactly the semantics a pack's capability requirement wants.

### 1.3 A protocol — already frozen

`internal/frameproto` is "the frame protocol v1 spoken between a jail-side client and a
host-side loophole daemon. **The wire format is a frozen interop contract.**" Length-prefixed
frames, stream IDs, a signed exit code that round-trips negative values for signal deaths.
It is transport-agnostic — every function takes an `io.Reader`/`io.Writer` — which is why the
`loopback-tls` unification left it untouched.

And loopholes already execute third-party programs: `cmd` is an **argv `[]string`**, not a
shell string (`loopholes.go:94`), and a loophole's own directory is bind-mounted `:ro` into
the jail at `/etc/yolo-jail/loopholes/<name>` — with a *tested* fixture of a loophole
shipping and running its own script (`runtime_test.go:189`:
`{"python3", "/etc/yolo-jail/loopholes/jd-mod/jail.py"}`).

### 1.4 "Something from mise" — viable, and the weakest of the four

mise is present with a 986-entry registry, so `mise` can install a pack's tool. But mise is
the wrong layer for *pack logic*: it is a per-jail tool installer, its store is deliberately
separated from the host's (a shared store corrupted `mise install`), and the project's own
three-way rule (`packages` / `mise_tools` / project manifest) exists specifically to stop
version pins proliferating. Adding pack logic as a fourth pin site is the anti-pattern that
rule prevents.

**Use mise for a pack's *tool* needs where a jail-local tool is right. Do not use it as the
pack-logic mechanism.**

---

## 2. The design

Third-party agent packs, with logic, using only the above.

### 2.1 Two logic tiers, chosen by what the pack needs

**Tier 1 — declarative projection (the default, covers most packs).**
The typed operation set from [three-decisions.md](three-decisions.md): `rename`, `fold`,
`inject`, `default`, `omit_if_absent`, `suffix_key`, `route_to`, `tombstone`. Derived from
the five real projections, so it is a closed set, not a guess. No execution, no trust
question, works identically for official and third-party packs. **Every agent pack should be
expressible here**; if one isn't, that is evidence the operation set is missing something
real.

**Tier 2 — a projector program, over a protocol.**
For a pack that genuinely needs computation. The pack declares:

```jsonc
{
  "name": "acme-agent",
  "projector": {
    "cmd": ["acme-projector", "--project"],
    "requires": { "command_on_path": "acme-projector" }
  }
}
```

yolo runs it as a subprocess, writes a request on stdin, reads a response on stdout:

```
→ {"schema":1, "exports": {"mcp_servers": {...}}, "surface": "settings", "computed_only": true}
← {"schema":1, "values": {...}}     or   {"schema":1, "error": "..."}
```

Properties that make this cheap rather than a new subsystem:

- **The projector is a pure function.** Input is the exports it consumes; output is config
  values. It gets no filesystem paths, no credentials, no jail access. Enforceable by
  construction, because we only hand it JSON on stdin.
- **It can be written in anything** — Go, Rust, Python, a shell script. This is the answer to
  "why can't it be Go": a projector *can* be Go. It just can't be *linked into yolo*.
- **It runs where composition runs.** Under host-side composition (decision 1) it runs on the
  host, before the container exists — so a failing projector is a pre-flight error, not a
  boot-time warning.
- **The protocol is one request/response**, so it needs none of `frameproto`'s streaming. Copy
  the *discipline* (schema version, frozen contract), not the framing.

### 2.2 Where the projector binary comes from

Three tiers, in the order a pack should prefer them:

| | How | Cost | Reproducible |
|---|---|---|---|
| **a. interpreted, shipped in-pack** | a Python/shell script in the pack tree; `requires: {command_on_path: "python3"}` | none | yes (the script is content-addressed with the pack) |
| **b. nix package via `packages`** | pack declares a provision contribution; the existing `{name, version, url, hash}` form builds it | one image rebuild | **yes — fully** |
| **c. prebuilt binary in-pack** | pack ships a compiled artifact per platform | none | only as much as the pack's hash |

**(a) is the recommended default** — `python3` is already in the image, the script is small,
and nothing needs building. **(b) is the recommended path for a real compiled projector**,
because it is the only option that is genuinely reproducible and it reuses shipped machinery.
**(c) should be allowed but discouraged**: it is a fetched binary with no build provenance,
which is a materially weaker claim than (b).

Note the phase split from [packs-and-the-prism.md §2.5](packs-and-the-prism.md) is exactly
what makes this coherent: (b) is a **provision** contribution (touches the image, costs a
rebuild), while the projector's *execution* is **compose** (free, every boot).

### 2.3 Trust

A projector is arbitrary code, so state the model plainly rather than implying a sandbox:

- **Packs are user-scope only** (settled), so installing one is already an act of user-level
  trust — the same trust level that lets user config name `~/.ssh/id_ed25519`.
- **The lockfile + explicit approval already exist** in the pack plan. A pack gaining or
  changing a `projector` should require **re-approval**, because it is a materially different
  permission from shipping a skill file. This is the one new gate.
- **The projector sees only JSON we hand it.** Not a sandbox — a *narrow interface*. It is
  the same reason `frameproto`'s argv-not-shell choice matters.
- **MCP is the precedent for the trust level itself**: yolo already installs and runs
  arbitrary third-party executables (`shell.go:172`), so a projector is not a new category of
  risk. It is a smaller one, because it has no network and no filesystem handle.

### 2.4 Official packs use the same two tiers

This is what keeps "structurally identical" honest. An official pack uses tier 1 where it can
and tier 2 where it must — and when it uses tier 2, its projector is a **yolo subcommand**,
which is exactly how loopholes already do it
(`["yolo", "internal", "daemon", "host-processes", ...]`). So official-pack logic is compiled
Go, inside `goSrc`, type-checked and `go test`-able — while being invoked through the *same*
protocol a third-party projector uses.

That resolves the tension cleanly: **official packs get compiled Go without a special
mechanism, and third parties get the same interface.** The `goSrc` fileset stops being a
constraint on the design and becomes merely the reason official projectors live in-repo.

---

## 3. What this changes

- **Decision 2 is answered**: projections are *declarative data by default, with a subprocess
  projector as the escape hatch*. Not "data or code" — both, at a defined seam.
- **Third-party agent packs are viable with logic**, which was the requirement.
- **"Can't be Go" is retracted.** A third-party projector may be Go; it may not be linked into
  yolo. Different claim, and the difference is the whole design.
- **It strengthens decision 1.** A subprocess projector wants to run where failures are
  pre-flight — i.e. host-side composition. The two decisions now reinforce rather than
  interact awkwardly.

## 4. Still open

- **Does tier 1 actually cover the five real projections?** It should be *proven* by porting
  them before the operation set is frozen. If `conditional-OMIT` vs `tombstone-null` or
  gemini's cross-type derivation needs tier 2, the operation set is wrong and it is much
  cheaper to learn that now.
- **Projector caching.** Re-running a subprocess per surface per boot is probably fine
  host-side, but unmeasured. Worth a number before committing.
- **Pre-flight validation of provision contributions.** A bad hash aborts the entire nix eval
  and `tryEval` cannot catch it (documented in `flake.nix`), so a pack's `{version, url, hash}`
  contribution must be validated at pack-install time or it turns a config error into an
  unbuildable image.
- **Cross-platform tier (c).** If prebuilt binaries are allowed, per-platform selection and
  the macos-user/aarch64 story need answers.
