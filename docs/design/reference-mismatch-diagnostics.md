---
title: "What a mistyped name does to you today"
date: 2026-08-30
status: in-review
tags: [packs, config, validation, diagnostics, ux, principles]
summary: "Closing the gap between what stringly-typed-references-principle.md asks for and what the code does, written from the user's side. Today a mistyped pack name, an invented wire_api, and a plaintext credential in a base_url all return [PASS]; a mistyped KEY one line away returns [FAIL]. And a whole class of diagnostic — the bare stderr warning — is structurally excluded from the summary line users actually read (measured: 5 warnings printed, summary says 2). This is what changes, what starts failing, and what each message becomes."
---

# What a mistyped name does to you today

**Status:** DESIGN, 2026-08-30. Nothing built. Executes the amended
[`stringly-typed-references-principle.md`](stringly-typed-references-principle.md) — its §7 census
is the gap; this doc is how it closes, from the user's side.

**The short version.** Four references that name a component by string are unchecked today, and the
way you find out is that nothing happens. A fifth — capability supersession — *is* checked, produces
an excellent message, and prints it to a channel the summary line does not count. So the work is not
"add validation": it is **move three checks to a surface that can decide them, make one of them
exist at all, and stop printing findings where nobody reads them.** No new mechanism, no new config
key, no new manifest field.

**The most important sections are [§1](#1-the-reproduction) (the four-line config that returns
`[PASS]`), [§4](#4-every-message-before-and-after) (what each message becomes), and
[§6](#6-what-starts-failing-that-works-today) (what breaks for you).**

**Reads with:** [`stringly-typed-references-principle.md`](stringly-typed-references-principle.md)
(R1–R5, which this executes), [`gate-placement-principle.md`](gate-placement-principle.md) (R5's
parent — put the gate where the authority changes), and
[`profiles-as-pack-variants.md`](profiles-as-pack-variants.md) §8 (the same two-questions split,
applied to a design that has not shipped).

---

## 1. The reproduction

Not an argument — a measurement, taken 2026-08-30 in this jail against `yolo` 0.8.0+614.

```jsonc
// yolo-jail.jsonc
{
  "agent_profiles": { "cloude": "bedrock" },
  "providers": {
    "bedrock": {
      "base_url": "https://user:sk-secret@x.com/v1",
      "wire_api": "totally-not-a-wire-api"
    }
  }
}
```

```console
$ yolo check --no-build
  [PASS] Merged config is semantically valid
  …
Summary
  30 passed, 2 warnings
```

Three defects in four lines, all clean:

1. **`cloude` names no pack.** The profile is set for a pack that does not exist; the pack you meant
   gets nothing. Nothing anywhere says so — not at `check`, not at launch, not in the jail.
2. **`wire_api: "totally-not-a-wire-api"`** is accepted. It reaches `packs/pi`'s and
   `packs/codex`'s derives, which pass it straight through into `models.json` / `config.toml`. You
   find out from the agent, later, as a protocol error.
3. **A plaintext credential sits in a git-tracked config file** and validation does not mind. The one
   field guarded against this is `api_key_env_name`
   ([`validate.go:876-881`](../../internal/config/validate.go#L876-L881)); `base_url` is wide open.

Now move one typo, from a *value* to a *key*, in the same file:

```console
$ yolo check --no-build
  [FAIL] config.agent_profilez: unknown key
  [FAIL] config.providers.bedrock.wire_apid: unknown key
```

> [!IMPORTANT]
> **That contrast is the whole problem.** Field names live in a closed namespace and are enforced
> at the first opportunity, with a clean message. References to components by name do not and are
> not. Same file, same command, same class of typo, opposite outcome.

---

## 2. The three experiences a mismatch produces today

| What you get | Which mechanisms | What it costs you |
| :--- | :--- | :--- |
| **`[PASS]`, then nothing works** | `agent_profiles` keys, `wire_api`, `base_url` | The whole debugging distance. The symptom is an agent using the wrong endpoint or no profile at all, several layers from the typo. |
| **A warning you will not see** | `supersedes` capability match, `env_sources` missing files | Printed, then buried — see §3. |
| **`[FAIL]`, named, with the fix** | every config **key** | Nothing. This is the model. |

---

## 3. The buried-warning class is structural, not stylistic

`yolo check` has two diagnostic channels and only one of them reaches the summary.

- `[WARN]` rows, emitted by the checker itself, increment the counter behind the summary line.
- Bare `Warning:` lines on stderr, emitted by config resolution and loophole discovery, do not.

Measured, same jail, same day — three bogus `env_sources` entries:

```console
$ yolo check --no-build 2>&1 | rg -c '^Warning:'
5
$ yolo check --no-build 2>&1 | tail -2
Summary
  30 passed, 2 warnings
```

**Five warnings printed; the summary says two.** The summary is the line a user reads; the other
three are scrollback. Every finding in the second channel is invisible to the only surface that
aggregates.

This matters here specifically because **the best mismatch diagnostic in the tree lives in that
channel.** An unmatched `supersedes` produces:

```
warning: pack 'aws-bedrock' supersedes capability 'claude-oauth-refersh', which NO loophole
on this machine serves — did you mean 'claude-oauth-refresh'? Served here: [claude-oauth-refresh,
host-process-list]. Nothing was superseded, so every loophole keeps running
```

Offending string, declaring pack, did-you-mean, the candidate set, and the consequence — this is
principle R3 done correctly, and it is the template for §4. **Nothing about this message changes.**
What changes is that it becomes a refusal on the launch path, and stops being a line the summary
cannot count.

---

## 4. Every message, before and after

The user-visible deliverable in one table. Nothing here is a new mechanism; each row moves an
existing check or gives an existing namespace the check its neighbours already have.

### 4.1 A profile set for a pack that does not exist

| | |
| :--- | :--- |
| **Today** | `[PASS] Merged config is semantically valid` |
| **After** | `[FAIL] config.pack_profiles.cloude: no pack named 'cloude' is selected — did you mean 'claude'? Selected packs: [claude, pi, codex]. Add the pack to 'packs', or remove this entry.` |
| **Where** | `yolo check` **and** launch preflight. Both, because a config edit and a launch are different moments and the second is where it bites. |
| **Also fixed** | `-p <name> -- <bin>` resolves the binary to a pack slug and refuses when no pack owns that bin. Today it keys the profile by binary basename with no check; every shipped pack happens to have `bin == slug`, so it works by coincidence. |

### 4.2 An invented `wire_api`

| | |
| :--- | :--- |
| **Today** | `[PASS]`, then the value lands verbatim in `~/.pi/agent/models.json` and `~/.codex/config.toml` |
| **After** | `[FAIL] config.providers.bedrock.wire_api: unknown protocol 'totally-not-a-wire-api' — expected one of: anthropic, openai-chat, openai-completions, responses` |
| **Where** | `yolo check`, at parse time. It is a closed enum; nothing needs resolving. |

> **CORRECTED 2026-09-02 by OQ-PT1 in [`provider-table-fidelity.md`](provider-table-fidelity.md)
> §3.0a/§3.1.** This mock-up is where the enum's four values were minted, and the list it quoted is
> retired: `anthropic`, `openai-chat`, `openai-completions` and `responses` were the union of the
> spellings three agents happened to use, in which two names covered ONE protocol and the protocol
> pi and codex really differ over had only codex's spelling. The vocabulary is now three
> **canonical protocol names** — `anthropic`, `openai-chat-completions`, `openai-responses` —
> chosen to be **nobody's dialect** (defined: a name that names a protocol, never a value an
> agent's config file reads). Translation, not pass-through, is the contract: each derive maps
> canonical → its own agent's spelling and emits nothing for a protocol that agent cannot speak
> (§3.4). "The value lands verbatim" in the **Today** row describes a world that ended twice:
> `0bc29bd5` (2026-09-01) began refusing values outside the set, and `0f04632d` (2026-09-02) made
> the derives translate a value inside it. The one part of the mock-up that was ever load-bearing
> can no longer go stale the way it just did: `validateWireAPI` renders its list by asking
> `packdecl.KnownWireAPIs` rather than quoting a frozen literal
> ([`validate.go`](../../internal/config/validate.go) `validateWireAPI`), so the message tracks
> the vocabulary instead of outliving it.

### 4.3 A credential in a URL

| | |
| :--- | :--- |
| **Today** | `[PASS]` — and the file is usually git-tracked |
| **After** | `[FAIL] config.providers.bedrock.base_url: URL carries embedded credentials ('user:…@'). Put the secret in a 0600 file referenced by env_sources and name the variable in api_key_env_name.` |
| **Where** | `yolo check`. Also `yolo pack lint`, so a pack author hears it before publishing. |

### 4.4 A supersession that matches nothing

| | |
| :--- | :--- |
| **Today** | a stderr `warning:` at discovery, uncounted by the summary; the launch proceeds with the loophole still running |
| **After** | the **same sentence**, as a launch refusal, and as a `[FAIL]` row in `yolo check`'s Loopholes section |
| **Where** | the host launch path, which holds the complete bundled+pack+user+config set. Not the in-jail entrypoint, which cannot resolve it. |
| **Stays a report** | `yolo loopholes list` and `status` — the commands you run *to diagnose this* must not be the commands it takes down. |

### 4.5 An active profile whose credential was never hydrated

| | |
| :--- | :--- |
| **Today** | launch succeeds; the agent fails to authenticate with a provider-side error |
| **After** | `Refusing to launch: profile 'bedrock' is active and provider 'bedrock' expects DEEPSEEK_API_KEY, which is unset. Consulted env_sources: ~/.config/claude/env (not found), ~/.config/yolo-jail/secrets.env (not found).` |
| **Where** | launch preflight only. |
| **Scope** | **Active profiles only.** A configured-but-unselected provider with no key on this machine stays inert — that is the ordinary shared-config case and must not refuse. |

### 4.6 Skew: your image is older than your tree

The one genuinely new sentence, and the reason the refusals in §4.1–4.4 are affordable.

| | |
| :--- | :--- |
| **Today** | you get the mismatch message and no reason for it, or (before this work) nothing at all |
| **After** | `… no loophole serves 'foo'. Your image predates your working tree (image: <hash-a>, tree: <hash-b>) — run 'just load' on the host, then retry.` |
| **Why it matters** | `/workspace` is live-mounted and the binaries are frozen until `just load`, so tree-newer-than-image is the normal state between loads. A refusal that cannot name that condition sends you looking for a typo you did not make. This is the `tier` incident's shape, and the repo already solved it once for the test suite (`ensureJailImage` aborts with the fix command). |

---

## 5. Where each check lands, and why not somewhere else

Three surfaces, three jobs. The principle's R5 assigns them.

```mermaid
flowchart TD
    EDIT["you edit config or a pack.json"] --> CHECK["yolo check / yolo pack lint"]
    CHECK -->|"decidable from the declaration alone:<br/>enums, URLs, credential shapes"| CFAIL["[FAIL], named, with the fix"]
    CHECK -->|"needs the resolved set"| CWARN["reported here, enforced at launch"]

    LAUNCH["yolo &lt;anything&gt;"] --> PRE["launch preflight<br/>(host: full resolved set in hand)"]
    PRE -->|"pack slugs, capabilities,<br/>active-profile credentials"| REFUSE["refuse, naming the fix<br/>— or naming SKEW"]

    DIAG["yolo loopholes list / status"] --> REPORT["always reports, never refuses"]
```

**Why not the in-jail entrypoint.** It cannot resolve the bundled+pack+user+config loophole set —
that is the package's own cycle argument — so it cannot decide the reference, and you cannot run
`just load` from inside a jail anyway. Both of R5's tests fail there. The check moves upstream; it
does not get downgraded.

**Why the diagnostic commands stay non-fatal.** `yolo loopholes list` is what you run to find out why
something is off. A refusal there over a pack's typo takes down the tool you are holding.

---

## 6. What starts failing that works today

The honest list. Each of these is a config that launches now and will not after.

| Config | Today | After | Recovery |
| :--- | :--- | :--- | :--- |
| A profile keyed to a pack you did not select | silently inert | **refused** | select the pack, or delete the entry |
| A `wire_api` outside the four known protocols | passes through to the agent | **refused** | use one of the four; if you need a fifth, it is a one-line enum addition |
| A `base_url` with embedded credentials | accepted | **refused** | move the secret to `env_sources` + `api_key_env_name` |
| A pack superseding a capability nothing serves | warns, launch proceeds | **refused at launch** | fix the capability name, or drop the `supersedes` |
| An active profile whose key was never hydrated | launches, fails at the agent | **refused** | populate the `env_sources` file |
| A pack tree newer than the image | any of the above, unexplained | **refused, naming `just load`** | `just load` on the host |

> **CORRECTED 2026-09-02 by OQ-PT1 in [`provider-table-fidelity.md`](provider-table-fidelity.md)
> §3.0a/§3.4.** Row 2's "four known protocols" and its recovery column are both retired. The
> vocabulary is the three canonical names above (§4.2's note), and adding one is **not** "a
> one-line enum addition": it is a line in `packdecl`'s `knownWireAPIs` **plus a dialect row in
> every derive that can speak it**, because a protocol no derive translates is a name in the list
> that delivers nothing to any agent. That cost is the point of OQ-PT1 — a canonical name that
> nobody translates is worthless by construction, so the enum may no longer grow ahead of the
> derives that would give it meaning.

> [!WARNING]
> **Row 4 is the behaviour change with the widest blast radius**, because a supersession is how a
> pack turns a loophole off. Today an unmatched claim leaves the loophole *running* — the safe
> direction. After this, an unmatched claim stops the launch. That is the intended trade: silent
> non-supersession is indistinguishable from a bug, and the person debugging it is not the person
> who caused it.

**We are pre-1.0 with one maintainer and no external pack ecosystem, and these are cheap to fix
when they fire.** That is the premise this work is built on; it is a ruling, not an assumption
(§9 ledger).

---

## 7. Sequencing, by user-visible payoff

1. **Count the second channel.** Make bare `Warning:` lines reach `yolo check`'s summary, or route
   them through the reporter. **Everything else in this doc is worth less until findings are
   visible** — and this alone makes the supersedes diagnostic reach the user it was written for.
2. **`pack_profiles` key validation.** No ruling needed, no skew story, nothing else waits on it. It
   is the §1 reproduction's first line, and it is the smallest change in the doc.
3. **`wire_api` enum and `base_url` credential refusal.** Parse-time, decidable, no registry.
4. **Relocate the supersession match to the launch path.** Message unchanged; disposition and
   surface change. Needs OQ-RM2 ruled first.
5. **The skew diagnostic.** Ships with or before step 4 — a refusal that cannot say "your image is
   old" is a worse refusal than the warning it replaces.
6. **The active-profile credential preflight.** Last, because it is the only one that needs a notion
   of "active profile" that is still being designed
   ([`profiles-as-pack-variants.md`](profiles-as-pack-variants.md)).

Steps 1–3 are independent of every design question in flight.

---

## 8. Non-goals

- **Not a change to `env_sources`' permissiveness.** A host file absent on this machine is
  portability, not a typo, and there is no candidate set to suggest from. It stays a skip. Only its
  *visibility* changes (step 1), plus the derived credential check in §4.5.
- **Not a new config key, manifest field, or contribution kind.** Every check here is on a name that
  already exists.
- **Not validation of provider reachability.** Whether `base_url` answers is a runtime fact about a
  network; this doc is about names.
- **Not a general audit of every warning in the tree.** Step 1 fixes the *channel*; individual
  warnings elsewhere keep their current severity unless a later doc argues otherwise.
- **Not `pack-fragment` target resolution.** That mechanism does not exist; see
  [`profiles-as-pack-variants.md`](profiles-as-pack-variants.md) §8.

---

## 9. Open Questions

1. 💬 **OQ-RM1: Does `yolo check` refuse, or only report, the launch-only checks?** §4.1's pack-slug
   check is decidable at `check` time. §4.4's supersession match is decidable there too, on the
   host. But `check` is also the command you run *to find out what is wrong*, which is the argument
   that kept the supersedes finding non-fatal in the first place. **This decides whether `check`
   ever exits non-zero for a reference mismatch, or only ever shows `[FAIL]` rows that the launch
   then enforces.**

   _Leaning:_ `check` shows `[FAIL]` and exits non-zero — it already does exactly this for unknown
   keys, and a `check` that passes on a config the next launch refuses is the defect roadmap 💬 10
   is about. The "don't break the diagnostic tool" carve-out belongs to `loopholes list`, which
   reports one subsystem, not to `check`, which is the pre-flight.

   **Answer:**
   > _(empty — fill in when decided)_

2. 💬 **OQ-RM2: When a supersession matches nothing, do we refuse the launch or refuse the pack?**
   Two dispositions with very different feels. **(a)** Refuse the launch: nothing starts until the
   claim is fixed. **(b)** Refuse the *pack*: it does not load, its other contributions do not
   render, the launch proceeds without it — which is
   [`trust-paths.md`](trust-paths.md) OQ-TP6's rule ("a refused contribution refuses the launch",
   built 2026-08-18) read the other way.

   _Leaning:_ (a), refuse the launch, for consistency with the shipped TP6 rule — *"no partial
   packs: fix it, remove it, or approve it."* A pack that half-loads is the state that rule exists
   to delete, and a supersession is precisely a claim about the environment other packs are running
   in.

   **Answer:**
   > _(empty — fill in when decided)_

3. 💬 **OQ-RM3: How does the skew message get its two hashes?** §4.6 wants to say *"image
   `<hash-a>`, tree `<hash-b>`"*. `ensureJailImage` does this with `nix eval .#installPrefix.outPath`
   against `readlink /bin/yolo-entrypoint` — an eval, ~0.3 s, never a build. On the launch path that
   is 0.3 s added to **every** launch, to produce a sentence needed on almost none of them.

   _Leaning:_ Compute it **lazily — only when a reference has already failed to match.** The refusal
   is the slow path by definition, and 0.3 s on the way to an error message nobody minds. Do not put
   it on the happy path.

   **Answer:**
   > _(empty — fill in when decided)_

4. 💬 🤷 **OQ-RM4: Is there an escape hatch, and what is it called?** Every other fatal in this repo
   has one — `YOLO_ALLOW_STALE_IMAGE`, `YOLO_ALLOW_UNREACHABLE_SERVICES`, `YOLO_NO_HOST_LOOPBACK` —
   each loud, each naming itself in the refusal.

   _Leaning:_ **No hatch, at least at first.** Those three exist because the condition can be true
   through no fault of the user's config (an offline machine, a host that cannot forward loopback).
   A mistyped name is always the config, and the fix is always shorter than the workaround. Add one
   only if a real case turns up — and if it does, `YOLO_ALLOW_UNMATCHED_REFERENCES` is the spelling
   that matches the family.

   **Answer:**
   > _(empty — fill in when decided)_

---

## Decision Ledger

| ID | Ruling / Decision | Date | Settled in |
| :--- | :--- | :--- | :--- |
| RM-P1 | **Fail closed, and break things.** Pre-1.0, one maintainer, no external pack ecosystem — a breaking change with a one-command recovery is cheaper than a silent wrong result. *"it's breaking, so it breaks, what's wrong with that? we're early, we can break things."* | 2026-08-30 | §6, and R1 of [`stringly-typed-references-principle.md`](stringly-typed-references-principle.md) |
| RM-P2 | **Skew is not an exemption from fail-closed; it is a message.** The remedy for a version-boundary mismatch is a diagnostic that names the rebuild, not a downgrade to a warning. The repo already does this in `ensureJailImage`. | 2026-08-30 | §4.6, and R5 |
| RM-P3 | **The gate moves, the severity does not.** Where a validation point cannot resolve the registry or its actor cannot act, relocate the check upstream rather than lowering it. | 2026-08-30 | §5, and R5 |
