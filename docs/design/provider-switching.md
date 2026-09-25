---
title: "Deselecting a profile clears the model id yolo wrote, and keeps the one you wrote"
date: 2026-09-04
status: draft
tags: [providers, profiles, selection, models, deselection]
summary: "When you stop passing a profile, codex, pi and opencode keep the provider and model id yolo wrote into their config file, so the next launch asks the wrong endpoint for the old provider's model. The fix is one new rule in the selection mechanism: clear the id when yolo wrote it, and keep it when you wrote it. Claude is not affected."
---

# Deselecting a profile clears the model id yolo wrote, and keeps the one you wrote

**The question this doc answers.** When a user stops selecting a profile, should yolo clear the
model id it wrote into the agent's config, and should it say so when it does?

**Status:** DECIDED, 2026-09-25. Ruled by user: OQ-PSW2 decided (deselecting clears what yolo wrote via omission, falling back to native defaults or host layer).

- **Omission keeps a host-file value**, and the clear holds on the launch after it.
- **A tombstone deletes the host value for one launch**, and the value comes back on the next.
- **Omission has one failure:** on a boot that adopts the file, it keeps yolo's stale id as
  the user's.

The same run found that only pi has a host layer to keep, and that a selection over a
host-layer value is refused ([`OQ-SW1`](#OQ-SW1)). Code claims cite a symbol, never a line.

**Where things stand.** Ruled on 2026-09-25: `OQ-PSW2` settles the deselection clear via omission.
The rule this revises is [`OQ-CS2`](../reference/providers.md#oq-cs2) in the providers reference.
The alias and shipped-model questions moved out on 2026-09-25 ([Where the rest went](#where-the-rest-went)).

**Needs your ruling:**

- [`OQ-PSW4`](#OQ-PSW4) 🤷: should the clear print a one-line notice? Leaning: genuinely your call,
  with a mild preference to print it.
- [`OQ-SW1`](#OQ-SW1), found by the 2026-09-25 measurement: should a selection outrank a value
  that came from your host config rather than from an in-jail edit? Today it does not. Once
  any launch has rendered your host `settings.json`'s model into the jail's pi file, `-p` no
  longer changes pi's model. Leaning: yes.

**The most important section is [§4](#4-the-fourth-row-clear-what-yolo-wrote-keep-what-the-user-wrote).**
It covers the only live defect.

**Scope note.** This doc was split out of [`bedrock-plumbing.md`](bedrock-plumbing.md). Its
principle P1 ([§1 there](bedrock-plumbing.md#1-verdict-and-principles)) says an auth mode moves
its credential, environment and model ids together. That doc raised the problem and declined to
solve it. Nothing here is Bedrock-specific, and the fix lands in the provider system rather than
in a pack.

**Reads with:**

- [`../reference/providers.md`](../reference/providers.md), the mechanism this amends. See
  especially [Selection: write on activation, never on absence](../reference/providers.md#selection-write-on-activation-never-on-absence).
- [`model-lists-and-pickers.md`](model-lists-and-pickers.md), for aliases, first-party providers
  and shipped ids.
- [`provider-credential-scope.md`](provider-credential-scope.md), for which credentials reach
  which agent.

---

## Terms, in plain words

- **Provider.** A pack entry that describes one model service: where it lives, if it has a
  URL, and a `models` map from short aliases to that service's own model ids.
- **Profile.** A name you select (`yolo -p <name>`, or `use_profiles` in config) that picks one
  provider for an agent. Selecting a profile is how you tell yolo "this agent talks to that
  service".
- **Selection keys.** The config lines naming the provider and model an agent starts on: codex's
  `model_provider` + `model`, pi's `defaultProvider` + `defaultModel`, and opencode's `model`.
  yolo writes them only while a profile is selected.
- **Selection record.** A per-agent, per-file sidecar,
  `<workspace>/.yolo/prism/<agent>-<name>.selection.json`, holding what yolo's selection
  mechanism last wrote, key by key. It is how yolo tells its own value from yours.
- **Stateful render, computed layer, capture overlay.** At boot, yolo's *stateful render*
  rebuilds each agent config file from stacked layers. The *capture overlay* holds edits you made
  inside the jail, so they survive the rebuild. The *computed* layer is what packs' derive scripts
  emit, and it outranks both the capture overlay and your host file. See [`jail-home.md`](../reference/jail-home.md).
- **Null tombstone.** A JSON `null` in a layer, which the fold treats as "delete this key"
  ([RFC 7386](https://www.rfc-editor.org/rfc/rfc7386)).
- **Tier indirection** *(coined here)*. A user-typed, provider-relative model name that the agent
  itself resolves to a provider's id, such as Claude Code's `opus`.

---

## 1. The defect, and the principle behind it

Run `yolo -p bedrock -- codex`, and yolo writes a Bedrock provider and model id into
`~/.codex/config.toml`. Then run `yolo -- codex`. Both lines stay. Codex keeps asking for the
Bedrock model, because nothing ever removes what yolo wrote.

**The principle: deselection is a state, not the absence of one.** Today "no profile active"
means "whatever the last profile left behind". You reach that state by doing nothing, which is
why it is the one that bites.

---

## 2. Why claude needs no fix

The agents split on whether they have a tier indirection.

**Claude has one.** `/model opus` is provider-relative by design. The claude env derive
(`yolo.env("claude", …)` in `packs/claude/derive.lua`) emits `ANTHROPIC_MODEL` and the
`ANTHROPIC_DEFAULT_*_MODEL` variables *only while a provider is selected*. These are process
environment variables rebuilt every launch, and nothing is written to a file. So when you
deselect, the variables are simply absent, and Claude Code falls back to its own aliases. Two
later commits (`f7b14308`, 2026-09-15, and `caaaae1b`, 2026-09-16) made a profile's `model` option
pin every tier. That still happens only through the environment, so it vanishes on deselect too.

**Codex, pi and opencode have none.** yolo resolves the alias itself and writes a literal id into
a config file, through the reserved `selection` namespace. That file persists between launches.

| | claude | codex · pi · opencode |
| :--- | :--- | :--- |
| Who resolves the tier | the agent | yolo's derive |
| Where the id lives | a process env var, rebuilt per launch | a line in a config file, kept |
| What deselection does today | the var is not emitted: **correct** | the line stays: **the bug** |
| What is missing here | nothing | a clear |

> [!IMPORTANT]
> **Do not "fix" claude's half.** Its env path is already right, and a claude-side clear would be
> work with no defect under it. Claude's remaining gap is a missing `models` table, which is
> [`model-lists-and-pickers.md`](model-lists-and-pickers.md)'s business.

---

## 3. What happens today

`agentcfg.ApplySelection` decides each selection key once per launch. Its doc comment states the
contract, and its body implements it:

| Situation | Today | Right? |
| :--- | :--- | :--- |
| Key absent, selection names it | write it (activation) | yes |
| File value == what yolo last wrote, selection moved | write the new value | yes |
| File value != what yolo last wrote | keep the user's value | yes |
| Selection stops naming the key | **keep the file's value, keep the record** | **no** |

The code comment reads *"not selected → lift cur … never clear"* and cites
[`OQ-CS2`](../reference/providers.md#oq-cs2). That ruling was right about the danger it named: an
interactive `/model` choice must survive the next launch. But the rule it produced is broader than
that danger.
One behavior protects two different values. It protects **the user's** value, which must never be
touched, and **yolo's own** stale value, which nothing should protect. The selection record
already tells them apart, and row three uses it. Row four simply does not ask.

> [!WARNING]
> **Omitting the key is not clearing it**, as `ApplySelection`'s doc comment argues. The render
> rewrites the file wholesale, and the capture overlay may still hold the key's pre-yolo value.
> A key no layer asserts would fall back to that stale value instead of disappearing. So the
> design clears by lifting an explicit **null tombstone**, which `agentcfg.mergeValue` honors as a
> deletion.
>
> ⚠ **A tombstone deletes through every layer below it, the user's own host file included.**
> MEASURED 2026-09-20: three tombstones in `packs/claude/derive.lua` deleted a user's deliberate
> plugin enables on every boot (`5e15b964`). The selection lift lands on the computed layer, so
> a tombstone clear would also remove a `model` the user set in their **host** config: "clear"
> would mean the agent's built-in default, never the host value. [`OQ-PSW2`](#OQ-PSW2)'s leaning
> says that is not acceptable, so a tombstone that does this fails the ruling.

> [!NOTE]
> **MEASURED 2026-09-25: the omission premise is stale, and omission keeps a host-file value.**
> This was found UNMEASURED on 2026-09-24. `ApplySelection`'s comment dates from `36dbc88e`
> (2026-09-02). Overlay narrowing, `agentcfg.narrowOverlay`, landed later in `7b0cc818`
> (2026-09-10). On every boot it drops overlay keys that the computed layer asserts, and it
> persists the narrowed overlay. So a selected key no longer sits in the overlay, and omitting
> it falls through to the host layer. The run is described in [§11](#11-evidence). No
> mechanism is chosen here: that is still build step 1's, under [`OQ-PSW2`](#OQ-PSW2)'s ruling.
>
> Each boot runs on the sidecars the previous boot left. "Deselect ×2" means two launches with
> no profile.
>
> | Sequence (pi unless named) | Today | Tombstone | Omission |
> | :--- | :--- | :--- | :--- |
> | No host value: select, deselect ×2 (Done 1). Same result for pi, opencode and codex | yolo's ids stay | keys absent on both launches | keys absent on both launches |
> | Host value, fresh home: select, deselect ×2 (Done 3) | yolo's ids stay | launch 1: keys **absent**, so the host value is gone; launch 2: host value back | host value on both launches |
> | As above, with `defaultModel` hand-edited before the deselect (Done 2) | edit kept | edit kept; `defaultProvider` absent, then host value | edit kept; `defaultProvider` is the host value on both launches |
> | As above, but `last_render` deleted just before the first deselect | yolo's ids stay | keys absent, then host value | **yolo's stale ids kept as the user's.** The record entry is dropped, so no later deselect clears them |
> | Re-select after the clear, host value present (Done 4) | — | **refused**: host value kept | **refused**: host value kept |
>
> Four consequences for build step 1:
>
> 1. **Only omission matches [`OQ-PSW2`](#OQ-PSW2)'s leaning in the steady state.** A tombstone
>    returns to the agent's default for one launch and then to the host value, so it flaps.
> 2. **Omission is wrong on an adopting boot.** That is a boot whose `last_render` sidecar is
>    absent or untrusted. The adoption residue re-captures yolo's own id from the file. The
>    tombstone still clears the id on that boot.
> 3. **Both mechanisms need call-site work in `composeStatefulSurface`** (`internal/entrypoint/prism.go`).
>    Today it keeps `next` only when `len(lift) > 0`, and `writeSelectionRecord` never writes an
>    empty record. Omission lifts nothing, so it needs `next` kept even when the lift is empty.
>    Both need an emptied record deleted (read from code for the tombstone). Measured without
>    those changes, omission clears the file but the record keeps the stale ids. Per [§4](#4-the-fourth-row-clear-what-yolo-wrote-keep-what-the-user-wrote),
>    a later hand-typed identical id would then read as yolo's.
> 4. **Only pi has a host value to keep, and Done 4 fails on it.** Of the three id-writing
>    surfaces, only pi's `settings` declares `readsHost` in its pack manifest. codex's
>    `config.toml` and opencode's `opencode.json` compose no host layer. A host `config.toml`
>    carrying `model = "host-model"` rendered no `model` key at any boot. So Done 3 applies to
>    pi alone, and for codex and opencode the two mechanisms render the same. Done 4 fails on
>    pi with a host value, because the host value is back in the file and yolo has no record
>    for it. That is [`OQ-SW1`](#OQ-SW1).

---

## 4. The fourth row: clear what yolo wrote, keep what the user wrote

The settled design is one new branch in `ApplySelection`, keyed on the record it already holds. It runs
per key, so a provider-and-model pair clears as a pair:

| Situation, key not selected | New behavior |
| :--- | :--- |
| File value == the record (**yolo's own value**) | **omit from lift** (clear to native default or host layer), and **drop the key from the record** |
| File value != the record (the user changed it) | lift the current value (unchanged) |
| No record for the key (yolo never wrote it) | lift the current value (unchanged) |
| Key absent from the file | nothing, except that the record entry is dropped |

**Dropping the record entry is what makes the clear safe.** If yolo cleared the key but
remembered the value, a user who later typed that same id by hand would lose it on the next
deselect. After a clear, yolo has no claim. That matches the rule the mechanism already follows
elsewhere: "a lost or corrupt record claims nothing".

When all keys on a surface are cleared, `composeStatefulSurface` updates `selectionRecord` to the
empty map, and `writeSelectionRecord` removes the empty `.selection.json` sidecar from disk.

### 4.1 Native First-Party Provider Resolution (`openai-codex` for `codex`)

An agent CLI's first-party subscription provider (such as `openai-codex` for `codex`) must not be
refused by protocol resolution, and must not configure an invalid third-party `model_providers`
entry in the agent's configuration:

1. **Protocol declaration**: Codex CLI natively speaks `openai-responses`. `packs/codex/pack.json`
   must declare `protocols: ["openai-responses", "openai"]` so pointing `codex` at `openai-codex`
   (which declares `openai-responses`) succeeds directly.
2. **Native authentication fallback**: In `packs/codex/derive.lua`, `openai-codex` is recognized as
   Codex's built-in first-party provider (backed by the host's `openai-auth-broker`). When selected,
   the derive sets `selection = { model = modelAlias }` without setting `model_provider`, allowing
   Codex CLI to run natively with OAuth credentials while clearing any stale third-party
   `model_provider` residue. `openai-codex` is also excluded from the third-party `model_providers`
   TOML table.

```mermaid
stateDiagram-v2
    [*] --> Unpinned: no profile ever selected
    Unpinned --> Bedrock: -p bedrock
    Bedrock --> FirstParty: -p anthropic
    FirstParty --> Bedrock: -p bedrock
    Bedrock --> Unpinned: no profile (§4 clears yolo's id)
    FirstParty --> Unpinned: no profile (§4 clears yolo's id)
    note right of Unpinned
        Today this state keeps the
        previous provider's model id.
    end note
```

`FirstParty` is a first-party provider entry, which does not exist yet. It is proposed in
[`model-lists-and-pickers.md`](model-lists-and-pickers.md#OQ-PSW3). This doc only needs the arrows
back to `Unpinned`.

---

## 5. What this does not license

- **No clearing a value yolo did not write.** The record is the whole authority. No record means
  no claim, and a corrupt record means no claim either.
- **No touching claude's env path.** [§2](#2-why-claude-needs-no-fix)'s warning stands.
- **No cross-agent selection.** Each derive writes only its own agent's keys, and a selection is
  per CLI name. This matches the [`OQ-BR4`](provider-credential-scope.md#OQ-BR4) ruling (*"as
  specific as possible"*).
- **No new persistence.** The selection record already exists per surface and gains no fields.

---

## 6. Alternatives considered

| Alternative | Verdict |
| :--- | :--- |
| **Clear by omitting the key** instead of tombstoning it | **Rejected**, because the capture overlay re-supplies the stale value. ⚠ MEASURED 2026-09-25: that premise is stale, and omission is the only mechanism that keeps a host value in the steady state. Its one failure is the adopting boot ([§3](#3-what-happens-today)'s note). Not re-decided here: the mechanism is build step 1's. |
| **Always re-assert the selection every boot** | **Rejected.** It reverts an interactive `/model` on the next launch, which is the hazard [`OQ-CS2`](../reference/providers.md#oq-cs2) exists to prevent. |
| **Drop the record on deselect, keep the file value** | **Rejected.** The residue becomes permanent *and* unattributable: the next selection reads the stale id as the user's and refuses to move it. |
| **Refuse the launch when a config holds an id the selected provider's `models` does not contain** | **Rejected for v1 — reconsider later.** It would catch the residue loudly, but it also refuses every legitimate hand-picked model, which is most of them. It cannot see a mid-session `/model` switch either. The intent, an enforced allowlist, is now [`OQ-WG3`](wire-bridge-gateway.md#OQ-WG3) in the wire bridge, which sees every request. |
| **Document "always pass `-p`"** | **Rejected.** That rule would be enforced by memory. `use_profiles` in user config is the legitimate persistent form, and it is unaffected. |

---

## 7. Behavior this design fixes

**Degenerate inputs.** A key in the file with no record is never cleared and never claimed. A
record entry for a key absent from the file leaves nothing to clear, so the entry is dropped. An
empty `models` map produces no selection value and no clear.

**Failure paths.** An unreadable or corrupt record is treated as absent, so nothing is cleared.
That is the existing fail-safe. A failed tombstone write fails the render at the boot step and
refuses the jail. There is no partial apply, because the file is written wholesale.

**Concurrency and ordering.** The record is per workspace, per agent and per surface, and the
render writes it once per launch. Two concurrent launches are last-writer-wins on the record,
which is already true and already benign.

**Defaults, trigger, pre-existing state, one writer.** There are no new knobs, timeouts or
retries. The trigger is the stateful render, once per launch. There is no migration: the new
branch first runs on the first deselect after it ships. The selection mechanism stays the
record's only writer, and the clear only lets it give a key back.

**Forbidden.** Never clear a key the record does not hold. Never clear on a *changed* selection,
which is row three's job. Never emit a selection key for a provider whose catalog row the same
gate dropped.

**What done looks like.**

- **Done 1.** Run `yolo -p bedrock -- codex`, then `yolo -- codex`, then `yolo -- codex` again.
  After both later launches, `~/.codex/config.toml` has no `model` or `model_provider` key, and
  codex starts on its own default. The third launch was the UNMEASURED case from
  [§3](#3-what-happens-today). MEASURED 2026-09-25 through the boot render (not a live
  launch): with no host value, both mechanisms leave the keys absent on the third launch too.
- **Done 2.** Run the same sequence with a `/model` change in between. The user's id survives
  untouched.
- **Done 3.** Run the Done 1 sequence with a `model` set in the user's host
  `~/.codex/config.toml`. After the deselect, the jail's file holds that host value again, not
  the agent's default and not the Bedrock id.
  ⚠ MEASURED 2026-09-25: codex's `config` surface declares no `readsHost`, so a host
  `config.toml` never reaches the jail, and this condition cannot be met for codex as written.
  pi is the only id-writing agent with a host layer. Its form of this condition is a
  `defaultModel` in the host `~/.pi/agent/settings.json`, and omission meets it
  ([§3](#3-what-happens-today)'s note). Rewording it is left to the ruling.
- **Done 4.** Re-selecting after a clear writes the id again through the activation branch.
  ⚠ MEASURED 2026-09-25: this fails on pi under both mechanisms whenever the host sets the key.
  After the clear, the host value is back in the file, and a value yolo has no record for
  reads as the user's. That is [`OQ-SW1`](#OQ-SW1). It holds for opencode, which has no host
  layer.

---

## 8. Risks

| Risk | Mitigation |
| :--- | :--- |
| **R1.** The clear surprises someone relying on today's stickiness: they used `-p` once and expected it to persist. | `use_profiles` is the supported persistent form, and it is unaffected. The clear goes in the release notes and is visible on the first deselect, not silently later. [`OQ-PSW4`](#OQ-PSW4)'s notice would make it visible in the moment. |
| **R2.** The new branch lands, and no test fails when its call site is deleted. This is the repo's recurring test shape. | The done-conditions are file-state assertions after a multi-launch sequence, which is where the call site (`internal/entrypoint/prism.go`) actually runs. |

---

## 9. What I would build, in order

- **Step 1. The fourth row** ([§4](#4-the-fourth-row-clear-what-yolo-wrote-keep-what-the-user-wrote)),
  with a multi-launch test. Select, deselect, launch again, and assert the keys are gone. Then
  select, hand-edit, deselect, and assert the edit survives. Run the tombstone and omission
  variants first, and keep whichever passes while also preserving a host-file value (Done 3). If
  neither does, stop and bring [`OQ-PSW2`](#OQ-PSW2) back rather than ship a clear that deletes
  the host value. It is the only defect here, and it depends on nothing else.
  MEASURED 2026-09-25 ([§3](#3-what-happens-today)'s note): omission passes Done 1 to 3 in the
  steady state, and the tombstone fails Done 3 for one launch. Neither passes Done 4 on pi with
  a host value, which waits on [`OQ-SW1`](#OQ-SW1). Omission needs the adopting-boot case
  handled, and both need the call-site changes that note lists.
- **Step 2. Fold into [`providers.md`](../reference/providers.md) and retire this doc** through
  `system-doc`. The selection table there grows a row.

The pre-split doc's build steps 2 to 4 (the claude `models` map, the alias vocabulary and the
first-party providers) moved to [`model-lists-and-pickers.md`](model-lists-and-pickers.md).

---

## 10. Open Questions

1. 💬 🤷 **OQ-PSW4: Should a deselect that clears a key say so on stderr?** The
   clear is otherwise invisible: a file loses a line between two launches. A one-line notice
   ("cleared the `model` yolo set for profile `bedrock`") makes it legible. Because the clear drops
   the record entry, the notice fires on exactly one launch per deselect, not on every later one.
   Stakes: only how loud the transition is.

   <!-- vantage: oq id=OQ-PSW4 leaning="Genuinely your call. I would print it: it fires on one launch per deselect, and a silent config change is the thing this doc is complaining about. But it is noise on a routine path, and I have no technical argument either way." -->

   _Leaning:_ Genuinely your call. I would print it, because it fires on one launch per deselect
   and a silent config change is the thing this doc is complaining about. But it is noise on a
   routine path, and I have no technical argument either way.

   **Answer:**
   > _(empty — fill in when decided)_

2. 💬 <a id="OQ-SW1"></a>**OQ-SW1: Should a selection outrank a value that came from your host
   config, not from an in-jail edit?** The id is coined here: `SW` stands for switching.
   It was filed while this doc's older series was still spelled `OQ-PS`, which
   [`provisioner-sets.md`](provisioner-sets.md) also uses for its own questions. That
   older series was renamed `OQ-PSW` (provider switching) on 2026-09-25, digits kept, so
   the old `PS2` is now [`OQ-PSW2`](#decision-ledger). This id kept its spelling.

   The 2026-09-25 measurement found this ([§3](#3-what-happens-today)'s note,
   [§11](#11-evidence)). `ApplySelection` reads any file value it has no record for as the
   user's ("the user changed it, or yolo never wrote it"). A value composed from pi's host layer
   is such a value. So once one launch has rendered a host `defaultProvider`/`defaultModel` into
   the jail's `settings.json`, `-p zai -- pi` keeps the host value and writes no record. On a
   fresh home the same selection wins, because the key is not in the file yet. After a clear
   the host value is back, so Done 4 cannot pass on pi. codex and opencode compose no host
   layer, so they are unaffected.

   Stakes: whether `-p` works for pi at all for a user whose host config names a model. It is
   separate from [`OQ-PSW2`](#decision-ledger) (activation, not deselection), but Done 4 depends on it.

   - **A. Keep today's rule.** A host value is the user's choice, and `-p` never overrides it.
     Done 3 and Done 4 are reworded to say so. A user who wants `-p` to work for pi removes the
     model from the host file.
   - **B. A selection outranks a host-layer value.** It overrides a value that equals what the
     host layer supplies when no record claims it. An in-jail edit still wins, because the
     capture overlay makes it differ from the host value. The inputs already exist: READ FROM
     CODE, `composeStatefulSurface` receives the host bytes, and the per-key provenance record
     (`prismProvenancePath`) names the layer that set each key.
   - **C. Keep today's rule, but say so.** The launch prints that the selection was not applied
     because the host config sets the key.

   <!-- vantage: oq id=OQ-SW1 leaning="B. An explicit -p is a more specific choice than a standing host default, and the hazard OQ-CS2 protects is an in-jail interactive choice, which B still protects because a captured edit differs from the host value. Paired with omission as the clear, B makes deselect return to the host value, which is OQ-PSW2's leaning. But whether a launch flag may override a host config is a product call." -->

   _Leaning:_ B. An explicit `-p` is a more specific choice than a standing host default. The
   hazard [`OQ-CS2`](../reference/providers.md#oq-cs2) protects is an in-jail interactive choice,
   and B still protects it. Paired with omission as the clear, B makes a deselect return to the
   host value, which is [`OQ-PSW2`](#decision-ledger)'s leaning. But whether a launch flag may override a
   host config is a product call, so this is yours.

   **Answer:**
   > _(empty — fill in when decided)_

---

### Decision Ledger

| ID | Ruling / Decision | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| OQ-PSW2 | **Deselecting a profile clears yolo's written selection keys via omission**, allowing the surface to fall back to the native mechanism or host layer. The selection record entry is dropped. | 2026-09-25 | [§4](#4-the-fourth-row-clear-what-yolo-wrote-keep-what-the-user-wrote) | — |
| OQ-CS2 | **Never write the selection key when no profile is active**, because an interactive in-agent choice must survive the next launch. This is the rule [`OQ-PSW2`](#decision-ledger) narrowed. | undated there | [providers.md](../reference/providers.md#oq-cs2) | yes |
| OQ-PSW3 | **Answered by the [`OQ-BR3`](model-lists-and-pickers.md#OQ-BR3) ruling**, which says to ship model defaults in a built-in pack. The row lives in [model-lists-and-pickers.md](model-lists-and-pickers.md#OQ-PSW3). | 2026-09-25 | [`model-lists-and-pickers.md`](model-lists-and-pickers.md#OQ-PSW3) | — |

---

## Where the rest went

Moved on 2026-09-25. Each anchor is kept here so inbound links resolve.

- <a id="OQ-PSW1"></a>[**OQ-PSW1**](#OQ-PSW1) (does claude's derive move to capability aliases?) →
  [`model-lists-and-pickers.md#OQ-PSW1`](model-lists-and-pickers.md#OQ-PSW1).
- <a id="OQ-PSW3"></a>[**OQ-PSW3**](#OQ-PSW3) (does yolo ship model ids?) →
  [`model-lists-and-pickers.md#OQ-PSW3`](model-lists-and-pickers.md#OQ-PSW3), answered there by the
  [`OQ-BR3`](model-lists-and-pickers.md#OQ-BR3) ruling.
- <a id="OQ-PSW5"></a>[**OQ-PSW5**](#OQ-PSW5) (which credentials a profile lets reach an agent) →
  [`provider-credential-scope.md`](provider-credential-scope.md), split out 2026-09-22. It uses a
  different mechanism: the launch's env channel in
  [`userenv.go`](../../internal/cli/run/userenv.go), not selection. Its live questions are
  [`OQ-CN1`](provider-credential-scope.md#OQ-CN1) through
  [`OQ-CN6`](provider-credential-scope.md#OQ-CN6).
- **The shared tier vocabulary** (`default`/`fast`/`balanced`), **first-party
  providers**, the pre-split doc's **principle P2**, the no-catalog and open-vocabulary
  non-goals, the "canonical translation table" alternative, the "provider declaring only
  `default` warns" behavior, and the pre-split doc's done-conditions 3 and 4, risks R2 and R3
  and build steps 2 to 4 all went to [`model-lists-and-pickers.md`](model-lists-and-pickers.md).
  This doc's own numbering restarted after the split.
- **The "refuse the launch on an id outside the provider's `models`" alternative** stays in
  [§6](#6-alternatives-considered), where it is still rejected as a launch-time check. Its intent,
  an enforced model allowlist, moved to the wire bridge as
  [`OQ-WG3`](wire-bridge-gateway.md#OQ-WG3).

---

## 11. Evidence

Code, re-verified 2026-09-24. Each claim names the symbol or file that carries it, so it survives
an edit elsewhere in the file:

| Claim | Anchor |
| :--- | :--- |
| The four-row selection contract, stated | `agentcfg.ApplySelection`'s doc comment (`internal/agentcfg/selection.go`) |
| The not-selected branch keeps the file value and the record | the `case !selected:` arm of `agentcfg.ApplySelection` |
| Why omission was judged not to clear (wholesale rewrite + capture overlay) | `agentcfg.ApplySelection`'s doc comment, from `36dbc88e` (2026-09-02) |
| The overlay is narrowed against the computed layer every boot | `agentcfg.narrowOverlay` and its call in `internal/agentcfg/staterender.go`, from `7b0cc818` (2026-09-10) |
| Null tombstones delete a key through every lower layer | `agentcfg.mergeValue` (`internal/agentcfg/engine.go`); the `LiteralNulls` field comment in `internal/agentcfg/compose.go`; `5e15b964` |
| The selection lift lands on the computed layer | `agentcfg.ApplySelection`'s call site in `internal/entrypoint/prism.go` |
| claude emits its model vars only from a selected provider | `yolo.env("claude", …)` in `packs/claude/derive.lua` |
| packs/claude's `bedrock` provider declares no `models` | the `kind: "provider"` entry named `bedrock` in `packs/claude/pack.json` |
| The id-writing selection keys | the `selection` table returned by `packs/codex/derive.lua` (`model_provider`, `model`), `packs/pi/derive.lua` (`defaultProvider`, `defaultModel`) and `packs/opencode/derive.lua` (`model`) |
| Selection record path | `<workspace>/.yolo/prism/<agent>-<name>.selection.json` |
| Only pi's id-writing surface composes a host layer | `"readsHost": true` appears on pi's `settings` surface and on no config surface in `packs/codex/pack.json` or `packs/opencode/pack.json` (`rg -n '"readsHost"' packs/*/pack.json`) |
| The call site keeps `next` only when the lift is non-empty | the `len(lift) > 0` condition around `agentcfg.ApplySelection` in `composeStatefulSurface` (`internal/entrypoint/prism.go`) |
| An empty selection record is never written, so it is never cleared either | the `len(record) == 0` early return in `writeSelectionRecord` (`internal/entrypoint/prism.go`) |

**The 2026-09-25 measurement** behind [§3](#3-what-happens-today)'s note. It was run on a
private copy of the working tree at `5a44129d`, never in `/workspace`, because it patches
production code. The harness is a test, `TestPS2Harness`, in the copy's `internal/entrypoint`.
It drives `ConfigurePackSurfaces` over the embedded pi, opencode, zai and codex packs through
the existing multi-boot helpers `newPioencodeRender` and `newSelectionRender`. It starts no
agent and makes no API call. The host file sits at `$YOLO_CTX_ROOT/host-pi/settings.json`,
carrying `defaultProvider = "hostprov"` and `defaultModel = "host-model"`. Each variant changes
the `!selected` arm of `ApplySelection`:

- A key the file no longer holds is dropped from `next`.
- A key whose file value equals the record is dropped from `next`. The **tombstone** variant
  lifts `nil` for it, and the **omission** variant lifts nothing.
- In both, the call site also takes `next` when the lift is empty, and deletes an emptied record
  file. A third variant, omission with `ApplySelection` changed alone, is what shows the stale
  record.

Command:
`env -u YOLO_VERSION -u YOLO_HOST_LAYERS HOME=$(mktemp -d) go test -short -count=1 -run TestPS2Harness -v ./internal/entrypoint/`.

Vendor, 2026-09-04: Claude Code's aliases resolve per provider, which is what
`ANTHROPIC_DEFAULT_OPUS_MODEL` and its siblings exist to repoint
([model configuration](https://code.claude.com/docs/en/model-config)).
